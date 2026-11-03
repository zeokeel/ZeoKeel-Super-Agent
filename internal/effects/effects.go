// Package effects is the durable side-effect queue. A command enqueues its side
// effects inside its own transaction; the effect runs only after that transaction
// commits, and a crash between the two leaves the row on disk for the next start.
// See design §7.2 and appendix A ("durable Effects").
//
// The row is the queue. There is no in-memory-only state that a crash could drop:
// coalescing, retries, backoff and the failure threshold all live in SQLite, so a
// restarted process picks up exactly where the old one stopped.
package effects

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/zeokeel/super-agent/internal/store"
)

// FailuresBeforeError is how many consecutive failures mark an effect kind as
// error. The prototype matched the Sources' poll threshold; three is enough for a
// transient network error to clear itself without hiding a real breakage.
const FailuresBeforeError = 3

// DefaultRetry is the backoff the reference implementation uses: start at two
// seconds, double each failure, cap at one minute. A queue never fails fast; a
// side effect that matters waits rather than being lost.
var DefaultRetry = [2]time.Duration{2 * time.Second, 60 * time.Second}

// laterError reports that an effect cannot run now. It is not a failure: the row
// is rescheduled after the initial delay without its attempt count moving, which
// is what keeps a paused queue from looking broken.
type laterError struct{ reason string }

func (e *laterError) Error() string {
	if e.reason == "" {
		return "effects: later"
	}
	return "effects: later: " + e.reason
}

// Later reports that the effect cannot run right now (processing paused, worker
// slots full, an earlier message of the same ticket still waiting). It reschedules
// the row after the initial retry delay and does not count as a failure.
func Later(reason string) error { return &laterError{reason: reason} }

// IsLater reports whether err means "not now" rather than "this failed".
func IsLater(err error) bool {
	var l *laterError
	return errors.As(err, &l)
}

// Handler runs one effect. The key is the row's business key and is the same value
// the enqueuing command used; payload is the JSON the command stored. Returning
// Later reschedules without counting a failure; any other error is retried with
// exponential backoff and counts toward FailuresBeforeError.
type Handler func(ctx context.Context, key string, payload []byte) error

// Options tune one kind. The zero value is "one at a time, default backoff, no
// ordering guarantee, report failures".
type Options struct {
	// Limit is how many effects of this kind may run at once. Zero holds the kind
	// without failing it (processing paused). Nil means one.
	Limit func() int
	// Retry is [initial, maximum] backoff. The zero value is DefaultRetry.
	Retry [2]time.Duration
	// Ordered runs one effect at a time, oldest first, and a not-yet-due oldest
	// row holds back later rows of the same kind. Use it when order is meaning.
	Ordered bool
	// Quiet keeps the kind out of the error state after repeated failures; used
	// when the failure is already surfaced on a Ticket (the delivery kind).
	Quiet bool
}

// Status is what one queued kind looks like right now.
type Status struct {
	Waiting  int
	Since    time.Time
	Attempts int // most attempts any waiting row has made
	Error    string
	Failing  bool // Attempts reached FailuresBeforeError
}

// Option configures a Queue.
type Option func(*Queue)

// WithClock replaces the clock, for tests and for a deterministic demo.
func WithClock(now func() time.Time) Option {
	return func(q *Queue) { q.now = now }
}

type handler struct {
	kind string
	run  Handler
	opts Options
}

type row struct {
	key        string
	payload    []byte
	seq        int64
	generation int64
	attempts   int
	due        time.Time
}

type task struct {
	kind string
	key  string
	done chan struct{}
}

// Queue is the process's durable effect dispatcher. One Queue serves every kind.
type Queue struct {
	store *store.Store
	now   func() time.Time

	mu        sync.Mutex
	runMu     sync.Mutex // serializes dispatch passes so one row is never started twice
	handlers  map[string]*handler
	kinds     []string // sorted, so a pass visits kinds in a stable order
	tasks     map[*task]struct{}
	broken    map[string]bool
	scheduled bool
	started   bool
	stopped   bool
	seq       int64
}

// New returns a queue over st. Nothing runs until Start; rows queued before that
// wait on disk, which is what makes "enqueue during setup" safe.
func New(st *store.Store, opts ...Option) (*Queue, error) {
	q := &Queue{
		store:    st,
		now:      time.Now,
		handlers: map[string]*handler{},
		tasks:    map[*task]struct{}{},
		broken:   map[string]bool{},
	}
	for _, o := range opts {
		o(q)
	}
	if err := st.QueryRow(`SELECT IFNULL(MAX(seq),0) FROM effect`).Scan(&q.seq); err != nil {
		return nil, fmt.Errorf("effects: read queue position: %w", err)
	}
	return q, nil
}

// Handle registers the handler for a kind. It must be called once per kind,
// before any effect of that kind can run.
func (q *Queue) Handle(kind string, run Handler, opts Options) error {
	if kind == "" {
		return errors.New("effects: empty kind")
	}
	if run == nil {
		return fmt.Errorf("effects: kind %q has no handler", kind)
	}
	if opts.Retry[0] <= 0 {
		opts.Retry[0] = DefaultRetry[0]
	}
	if opts.Retry[1] <= 0 {
		opts.Retry[1] = DefaultRetry[1]
	}
	if opts.Retry[1] < opts.Retry[0] {
		opts.Retry[1] = opts.Retry[0]
	}
	q.mu.Lock()
	if _, dup := q.handlers[kind]; dup {
		q.mu.Unlock()
		return fmt.Errorf("effects: kind %q already has a handler", kind)
	}
	q.handlers[kind] = &handler{kind: kind, run: run, opts: opts}
	q.kinds = append(q.kinds, kind)
	sort.Strings(q.kinds)
	q.mu.Unlock()
	q.report(kind)
	q.kick()
	return nil
}

// Enqueue stores one effect in the caller's transaction. When the command commits,
// the effect runs; when it rolls back, the row is gone with it. Enqueue the same
// kind+key again before it runs and the newer payload replaces the old one, keeps
// the old queue position (order is preserved) and becomes immediately due.
//
// Call it on the command goroutine, inside Atomic, so the row and the command are
// one atom.
func (q *Queue) Enqueue(kind, key string, payload any) error {
	if kind == "" || key == "" {
		return errors.New("effects: enqueue needs a kind and a key")
	}
	raw, err := marshalPayload(payload)
	if err != nil {
		return fmt.Errorf("effects: encode %s/%s: %w", kind, key, err)
	}
	now := q.now()
	q.mu.Lock()
	q.seq++
	seq := q.seq
	q.mu.Unlock()

	// The upsert is deliberately a cover, not an insert: the waiting row keeps its
	// seq (so a newer payload cannot jump the queue) while due moves to now, so
	// the latest state of the world is what runs next.
	_, err = q.store.Exec(`
		INSERT INTO effect(kind,key,seq,generation,attempts,due,error,payload,since)
		VALUES(?,?,?,0,0,?,'',?,?)
		ON CONFLICT(kind,key) DO UPDATE SET
		  payload=excluded.payload,
		  generation=generation+1,
		  attempts=0,
		  error='',
		  due=excluded.due`,
		kind, key, seq, store.FormatTime(now), string(raw), store.FormatTime(now))
	if err != nil {
		return fmt.Errorf("effects: enqueue %s/%s: %w", kind, key, err)
	}
	// Only after the surrounding command commits. AfterCommit runs immediately when
	// there is no transaction, so an enqueue from outside one is still executed.
	q.store.AfterCommit(q.kick)
	return nil
}

func marshalPayload(payload any) ([]byte, error) {
	switch v := payload.(type) {
	case nil:
		return []byte("null"), nil
	case json.RawMessage:
		return v, nil
	case []byte:
		return v, nil
	default:
		return json.Marshal(payload)
	}
}

// Cancel drops a queued row. If the effect is already running, that attempt
// finishes normally; its result is discarded because the row it would have
// written no longer matches.
func (q *Queue) Cancel(kind, key string) error {
	if _, err := q.store.Exec(`DELETE FROM effect WHERE kind=? AND key=?`, kind, key); err != nil {
		return err
	}
	q.store.AfterCommit(func() { q.report(kind) })
	return nil
}

// Pending reports whether an effect of this kind (and key, when given) is still
// waiting on disk.
func (q *Queue) Pending(kind string, key ...string) (bool, error) {
	var n int
	var err error
	if len(key) > 0 {
		err = q.store.QueryRow(`SELECT COUNT(*) FROM effect WHERE kind=? AND key=?`, kind, key[0]).Scan(&n)
	} else {
		err = q.store.QueryRow(`SELECT COUNT(*) FROM effect WHERE kind=?`, kind).Scan(&n)
	}
	return n > 0, err
}

// Start begins dispatching. Rows queued before Start wait on disk.
func (q *Queue) Start() {
	q.mu.Lock()
	q.started = true
	q.mu.Unlock()
	q.kick()
}

// Kick schedules a dispatch pass. It is safe to call from any goroutine.
func (q *Queue) Kick() { q.kick() }

func (q *Queue) kick() {
	q.mu.Lock()
	if !q.started || q.stopped || q.scheduled {
		q.mu.Unlock()
		return
	}
	q.scheduled = true
	q.mu.Unlock()
	go func() {
		q.mu.Lock()
		q.scheduled = false
		q.mu.Unlock()
		q.dispatch()
	}()
}

// Idle waits until no effect of kind (or of any kind, when kind is empty) is
// running and none of them can start. Call it after a command commits, never from
// inside Atomic: the command goroutine holds the store's write guard.
func (q *Queue) Idle(ctx context.Context, kind string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		q.dispatch()
		running := q.runningTasks(kind)
		if len(running) == 0 {
			return nil
		}
		for _, t := range running {
			select {
			case <-t.done:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
}

// Drain stops starting new effects and waits for the running ones. A row left by a
// running attempt stays on disk for the next start; it is deliberately not marked
// failed, because a shutdown is not the effect's fault.
func (q *Queue) Drain(ctx context.Context) error {
	q.mu.Lock()
	q.stopped = true
	q.mu.Unlock()
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		running := q.runningTasks("")
		if len(running) == 0 {
			return nil
		}
		for _, t := range running {
			select {
			case <-t.done:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
}

func (q *Queue) runningTasks(kind string) []*task {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]*task, 0, len(q.tasks))
	for t := range q.tasks {
		if kind == "" || t.kind == kind {
			out = append(out, t)
		}
	}
	return out
}

// Status reports what one kind looks like on disk.
func (q *Queue) Status(kind string) (Status, error) {
	var st Status
	var since sql.NullString
	var attempts sql.NullInt64
	err := q.store.QueryRow(`
		SELECT COUNT(*), MIN(since), MAX(attempts),
		       IFNULL((SELECT error FROM effect WHERE kind=? AND attempts>0 ORDER BY seq DESC LIMIT 1),'')
		FROM effect WHERE kind=?`, kind, kind).
		Scan(&st.Waiting, &since, &attempts, &st.Error)
	if err != nil {
		return Status{}, err
	}
	if since.Valid {
		st.Since, _ = store.ParseTime(since.String)
	}
	st.Attempts = int(attempts.Int64)
	q.mu.Lock()
	h := q.handlers[kind]
	q.mu.Unlock()
	st.Failing = st.Attempts >= FailuresBeforeError && (h == nil || !h.opts.Quiet)
	return st, nil
}

// dispatch runs one serialized pass over every registered kind. runMu makes sure
// two passes can never both decide to start the same row.
func (q *Queue) dispatch() {
	q.runMu.Lock()
	defer q.runMu.Unlock()
	q.mu.Lock()
	if !q.started || q.stopped {
		q.mu.Unlock()
		return
	}
	hs := make([]*handler, 0, len(q.kinds))
	for _, k := range q.kinds {
		hs = append(hs, q.handlers[k])
	}
	q.mu.Unlock()
	for _, h := range hs {
		q.dispatchKind(h)
	}
}

func (q *Queue) dispatchKind(h *handler) {
	now := q.now()
	var runningKeys map[string]bool
	var runningCount int
	q.mu.Lock()
	runningKeys = make(map[string]bool, len(q.tasks))
	for t := range q.tasks {
		if t.kind == h.kind {
			runningKeys[t.key] = true
			runningCount++
		}
	}
	q.mu.Unlock()

	limit := 1
	if h.opts.Limit != nil {
		limit = h.opts.Limit()
	}
	if h.opts.Ordered && limit > 1 {
		limit = 1
	}
	if limit <= runningCount {
		return
	}
	rows, err := q.readyRows(h, now, runningKeys, limit-runningCount)
	if err != nil {
		q.failed(h.kind, err)
		return
	}
	room := limit - runningCount
	for _, r := range rows {
		if room <= 0 {
			break
		}
		if q.start(h, r) {
			room--
		}
	}
	if q.clearBroken(h.kind) {
		// A pass that could read the queue means whatever broke has healed.
		q.report(h.kind)
	}
}

// readyRows reads the rows a pass may start. All queue reads go through
// Store.Guarded so they never join a command transaction that is still open: an
// effect must not start because of a command that is about to roll back.
func (q *Queue) readyRows(h *handler, now time.Time, running map[string]bool, room int) ([]row, error) {
	var out []row
	err := q.store.Guarded(func() error {
		var (
			rows *sql.Rows
			err  error
		)
		if h.opts.Ordered {
			// Only the oldest row is visible: an earlier row that is not due holds
			// back everything behind it, which is what ordered means.
			rows, err = q.store.Query(`SELECT key,payload,seq,generation,attempts,due
				FROM effect WHERE kind=? ORDER BY seq LIMIT 1`, h.kind)
		} else {
			limit := 16 + len(running)
			if limit > room {
				limit = room
			}
			rows, err = q.store.Query(`SELECT key,payload,seq,generation,attempts,due
				FROM effect WHERE kind=? AND due<=? ORDER BY seq LIMIT ?`,
				h.kind, store.FormatTime(now), limit)
		}
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var r row
			var payload, due string
			if err := rows.Scan(&r.key, &payload, &r.seq, &r.generation, &r.attempts, &due); err != nil {
				return err
			}
			r.payload = []byte(payload)
			r.due, err = store.ParseTime(due)
			if err != nil {
				return fmt.Errorf("effects: bad due %q: %w", due, err)
			}
			if running[r.key] || r.due.After(now) {
				continue
			}
			out = append(out, r)
		}
		return rows.Err()
	})
	return out, err
}

// start registers the task and launches it. The stopped/started check and the
// registration happen under the same lock as Drain's snapshot, so a shutdown can
// never cut a task that Drain did not see.
func (q *Queue) start(h *handler, r row) bool {
	t := &task{kind: h.kind, key: r.key, done: make(chan struct{})}
	q.mu.Lock()
	if !q.started || q.stopped {
		q.mu.Unlock()
		return false
	}
	q.tasks[t] = struct{}{}
	q.mu.Unlock()

	go func() {
		q.attempt(h, r)
		q.mu.Lock()
		delete(q.tasks, t)
		q.mu.Unlock()
		close(t.done)
		q.kick()
	}()
	return true
}

func (q *Queue) attempt(h *handler, r row) {
	ctx := context.Background()
	runErr := h.run(ctx, r.key, r.payload)

	q.mu.Lock()
	stopped := q.stopped
	q.mu.Unlock()

	own := `kind=? AND key=? AND seq=? AND generation=?`
	args := []any{h.kind, r.key, r.seq, r.generation}
	switch {
	case runErr == nil:
		// Match seq and generation: if the row was replaced or re-queued while this
		// attempt ran, the newer payload must stay and run again.
		q.write(func() error {
			_, err := q.store.Exec(`DELETE FROM effect WHERE `+own, args...)
			return err
		})
	case stopped:
		// Shutdown, not failure: leave the row exactly as it was.
	case IsLater(runErr):
		due := store.FormatTime(q.now().Add(h.opts.Retry[0]))
		q.write(func() error {
			_, err := q.store.Exec(`UPDATE effect SET due=? WHERE `+own, append([]any{due}, args...)...)
			return err
		})
	default:
		delay := h.backoff(r.attempts)
		due := store.FormatTime(q.now().Add(delay))
		q.write(func() error {
			_, err := q.store.Exec(`UPDATE effect SET attempts=attempts+1, error=?, due=? WHERE `+own,
				append([]any{truncate(runErr.Error(), 1000), due}, args...)...)
			return err
		})
	}
	if !stopped {
		q.report(h.kind)
	}
}

// write runs a bookkeeping statement outside any command transaction.
func (q *Queue) write(fn func() error) {
	_ = q.store.Guarded(fn)
}

func (h *handler) backoff(attempts int) time.Duration {
	if attempts < 0 {
		attempts = 0
	}
	if attempts > 16 {
		attempts = 16
	}
	delay := h.opts.Retry[0] * time.Duration(int64(1)<<uint(attempts))
	if delay > h.opts.Retry[1] || delay <= 0 {
		delay = h.opts.Retry[1]
	}
	return delay
}

func (q *Queue) failed(kind string, err error) {
	q.mu.Lock()
	q.broken[kind] = true
	q.mu.Unlock()
	q.setHealth(kind, "error", "the effect queue failed: "+truncate(err.Error(), 500))
}

func (q *Queue) clearBroken(kind string) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	if !q.broken[kind] {
		return false
	}
	delete(q.broken, kind)
	return true
}

// report writes the kind's health entry. It is how "three consecutive failures"
// becomes visible outside the queue without the queue throwing work away.
func (q *Queue) report(kind string) {
	q.mu.Lock()
	h := q.handlers[kind]
	q.mu.Unlock()
	entry := healthEntry(kind)
	_ = q.store.Guarded(func() error {
		var (
			waiting   int
			since     sql.NullString
			attempts  sql.NullInt64
			lastError string
		)
		err := q.store.QueryRow(`
			SELECT COUNT(*), MIN(since), MAX(attempts),
			       IFNULL((SELECT error FROM effect WHERE kind=? AND attempts>0 ORDER BY seq DESC LIMIT 1),'')
			FROM effect WHERE kind=?`, kind, kind).
			Scan(&waiting, &since, &attempts, &lastError)
		if err != nil {
			return err
		}
		state, detail := "ready", "nothing waiting"
		if waiting > 0 {
			detail = fmt.Sprintf("%d waiting since %s", waiting, since.String)
		}
		failing := attempts.Int64 >= int64(FailuresBeforeError)
		if h != nil && h.opts.Quiet {
			failing = false
		}
		if failing {
			state = "error"
			detail = detail + "; the last failure: " + lastError
		}
		return q.setHealthLocked(entry, state, detail)
	})
}

func (q *Queue) setHealth(kind, state, detail string) {
	_ = q.store.Guarded(func() error {
		return q.setHealthLocked(healthEntry(kind), state, detail)
	})
}

func (q *Queue) setHealthLocked(entry, state, detail string) error {
	_, err := q.store.Exec(`
		INSERT INTO health(entry,state,detail,at) VALUES(?,?,?,?)
		ON CONFLICT(entry) DO UPDATE SET state=excluded.state,
		  detail=excluded.detail, at=excluded.at`,
		entry, state, truncate(detail, 1000), store.FormatTime(q.now()))
	return err
}

func healthEntry(kind string) string { return "effect:" + kind }

// truncate caps a stored error/health detail at n runes, not bytes: the messages
// are Chinese, and cutting inside a rune would store invalid UTF-8. It keeps the
// fixed-width intent of the byte budget without splitting characters.
func truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n])
}
