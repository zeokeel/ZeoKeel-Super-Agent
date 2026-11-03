package effects_test

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zeokeel/super-agent/internal/effects"
	"github.com/zeokeel/super-agent/internal/store"
)

// fakeClock is the effect queue's clock. Tests advance it; nothing else does.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{t: time.Date(2026, 9, 9, 0, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Set(t time.Time) {
	c.mu.Lock()
	c.t = t
	c.mu.Unlock()
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func newStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "effects.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func newQueue(t *testing.T, st *store.Store, clock *fakeClock) *effects.Queue {
	t.Helper()
	q, err := effects.New(st, effects.WithClock(clock.Now))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		q.Drain(ctx)
	})
	return q
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func mustHandle(t *testing.T, q *effects.Queue, kind string, h effects.Handler, opts effects.Options) {
	t.Helper()
	if err := q.Handle(kind, h, opts); err != nil {
		t.Fatal(err)
	}
}

type recorder struct {
	mu    sync.Mutex
	order []string
}

func (r *recorder) add(key string) {
	r.mu.Lock()
	r.order = append(r.order, key)
	r.mu.Unlock()
}

func (r *recorder) all() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.order...)
}

func effectDue(t *testing.T, st *store.Store, kind, key string) time.Time {
	t.Helper()
	var due string
	if err := st.QueryRow(`SELECT due FROM effect WHERE kind=? AND key=?`, kind, key).Scan(&due); err != nil {
		t.Fatalf("read due: %v", err)
	}
	at, err := store.ParseTime(due)
	if err != nil {
		t.Fatalf("parse due %q: %v", due, err)
	}
	return at
}

func effectAttempts(t *testing.T, st *store.Store, kind, key string) int {
	t.Helper()
	var n int
	if err := st.QueryRow(`SELECT attempts FROM effect WHERE kind=? AND key=?`, kind, key).Scan(&n); err != nil {
		t.Fatalf("read attempts: %v", err)
	}
	return n
}

// An effect queued inside a transaction runs only when that transaction commits.
func TestEffectRunsOnlyAfterCommit(t *testing.T) {
	st := newStore(t)
	clock := newFakeClock()
	q := newQueue(t, st, clock)
	rec := &recorder{}
	mustHandle(t, q, "relay", func(_ context.Context, key string, _ []byte) error {
		rec.add(key)
		return nil
	}, effects.Options{})
	q.Start()

	ctx := context.Background()
	if err := st.Atomic(func() error {
		return q.Enqueue("relay", "kept", map[string]string{"text": "a"})
	}); err != nil {
		t.Fatal(err)
	}
	if err := q.Idle(ctx, ""); err != nil {
		t.Fatal(err)
	}

	boom := errors.New("rolled back")
	if err := st.Atomic(func() error {
		if err := q.Enqueue("relay", "dropped", map[string]string{"text": "b"}); err != nil {
			return err
		}
		return boom
	}); !errors.Is(err, boom) {
		t.Fatalf("rollback returned %v", err)
	}
	if err := q.Idle(ctx, ""); err != nil {
		t.Fatal(err)
	}

	if got := rec.all(); len(got) != 1 || got[0] != "kept" {
		t.Fatalf("ran %v, want only [kept]", got)
	}
	if pending, _ := q.Pending("relay", "kept"); pending {
		t.Fatal("the successful effect was not removed")
	}
	if pending, _ := q.Pending("relay", "dropped"); pending {
		t.Fatal("a rolled-back effect left a row behind")
	}
}

// Re-enqueueing the same kind+key replaces the payload and runs again, keeping the
// queue position. A payload that arrives while the effect runs is not lost.
func TestEffectCoalescesLatestPayloadAndReruns(t *testing.T) {
	st := newStore(t)
	clock := newFakeClock()
	q := newQueue(t, st, clock)

	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	rec := &recorder{}
	mustHandle(t, q, "relay", func(_ context.Context, key string, payload []byte) error {
		var p struct{ Text string }
		if err := json.Unmarshal(payload, &p); err != nil {
			return err
		}
		rec.add(p.Text)
		if p.Text == "first" {
			once.Do(func() { close(started) })
			<-release
		}
		return nil
	}, effects.Options{})
	q.Start()

	if err := q.Enqueue("relay", "k", map[string]string{"text": "first"}); err != nil {
		t.Fatal(err)
	}
	<-started
	if err := q.Enqueue("relay", "k", map[string]string{"text": "second"}); err != nil {
		t.Fatal(err)
	}
	if err := q.Enqueue("relay", "k", map[string]string{"text": "third"}); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := q.Idle(context.Background(), ""); err != nil {
		t.Fatal(err)
	}

	got := rec.all()
	want := []string{"first", "third"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("ran %v, want %v", got, want)
	}
}

// A kind runs oldest first, at most Limit at a time, and Limit 0 holds it without
// failing anything.
func TestEffectLimitAndQueueOrder(t *testing.T) {
	st := newStore(t)
	clock := newFakeClock()
	q := newQueue(t, st, clock)

	var limit atomic.Int64
	limit.Store(2)
	rec := &recorder{}
	holds := map[string]chan struct{}{
		"a": make(chan struct{}), "b": make(chan struct{}), "c": make(chan struct{}),
	}
	var mu sync.Mutex
	mustHandle(t, q, "launch", func(_ context.Context, key string, _ []byte) error {
		rec.add(key)
		mu.Lock()
		hold := holds[key]
		mu.Unlock()
		<-hold
		return nil
	}, effects.Options{Limit: func() int { return int(limit.Load()) }})
	q.Start()

	if err := st.Atomic(func() error {
		for _, key := range []string{"a", "b", "c"} {
			if err := q.Enqueue("launch", key, nil); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the first two launches", func() bool { return len(rec.all()) == 2 })
	// Two effects run concurrently here, so which one the scheduler lets record
	// first is not an assertion; that both of the oldest two started and the third
	// did not is.
	if got := rec.all(); !sameSet(got, "a", "b") {
		t.Fatalf("started %v, want exactly the oldest two [a b]", got)
	}

	// Limit 0 holds the kind: finishing a does not start c while the limit is off.
	limit.Store(0)
	mu.Lock()
	close(holds["a"])
	mu.Unlock()
	waitFor(t, "a to be removed", func() bool {
		pending, _ := q.Pending("launch", "a")
		return !pending
	})
	q.Kick()
	time.Sleep(20 * time.Millisecond)
	if got := rec.all(); len(got) != 2 {
		t.Fatalf("limit 0 started more work: %v", got)
	}

	limit.Store(2)
	mu.Lock()
	close(holds["b"])
	mu.Unlock()
	waitFor(t, "c to start after b", func() bool { return len(rec.all()) == 3 })
	mu.Lock()
	close(holds["c"])
	mu.Unlock()
	if err := q.Idle(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
}

// Later means "not now": the row is rescheduled after the initial delay and its
// attempt count does not move.
func TestLaterReschedulesWithoutCountingAFailure(t *testing.T) {
	st := newStore(t)
	clock := newFakeClock()
	q := newQueue(t, st, clock)

	var calls atomic.Int64
	mustHandle(t, q, "delivery", func(context.Context, string, []byte) error {
		calls.Add(1)
		return effects.Later("processing is paused")
	}, effects.Options{})
	q.Start()

	ctx := context.Background()
	if err := q.Enqueue("delivery", "d", nil); err != nil {
		t.Fatal(err)
	}
	if err := q.Idle(ctx, ""); err != nil {
		t.Fatal(err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("ran %d times, want 1", got)
	}
	if n := effectAttempts(t, st, "delivery", "d"); n != 0 {
		t.Fatalf("Later counted as a failure: attempts=%d", n)
	}
	if due, want := effectDue(t, st, "delivery", "d"), clock.Now().Add(effects.DefaultRetry[0]); !due.Equal(want) {
		t.Fatalf("due %v, want %v", due, want)
	}

	clock.Advance(effects.DefaultRetry[0] - time.Millisecond)
	if err := q.Idle(ctx, ""); err != nil {
		t.Fatal(err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("ran before it was due: %d times", got)
	}
	clock.Advance(time.Millisecond)
	if err := q.Idle(ctx, ""); err != nil {
		t.Fatal(err)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("ran %d times after the delay, want 2", got)
	}
	if n := effectAttempts(t, st, "delivery", "d"); n != 0 {
		t.Fatalf("Later counted as a failure: attempts=%d", n)
	}
}

// Failures double the delay up to the cap, and three consecutive failures mark the
// kind's health entry as error without throwing the work away.
func TestFailureBackoffCapsAndMarksError(t *testing.T) {
	st := newStore(t)
	clock := newFakeClock()
	q := newQueue(t, st, clock)

	mustHandle(t, q, "notice", func(context.Context, string, []byte) error {
		return errors.New("session busy")
	}, effects.Options{})
	q.Start()

	ctx := context.Background()
	if err := q.Enqueue("notice", "prompts", nil); err != nil {
		t.Fatal(err)
	}
	// 2s, 4s, 8s, 16s, 32s, then the 60s cap.
	want := []time.Duration{
		2 * time.Second, 4 * time.Second, 8 * time.Second,
		16 * time.Second, 32 * time.Second, 60 * time.Second, 60 * time.Second,
	}
	for i, delay := range want {
		if err := q.Idle(ctx, ""); err != nil {
			t.Fatal(err)
		}
		due := effectDue(t, st, "notice", "prompts")
		if got := due.Sub(clock.Now()); got != delay {
			t.Fatalf("attempt %d: backoff %v, want %v", i+1, got, delay)
		}
		if i == 2 {
			if n := effectAttempts(t, st, "notice", "prompts"); n != effects.FailuresBeforeError {
				t.Fatalf("attempts %d, want %d", n, effects.FailuresBeforeError)
			}
			status, err := q.Status("notice")
			if err != nil {
				t.Fatal(err)
			}
			if !status.Failing || status.Attempts < effects.FailuresBeforeError {
				t.Fatalf("status not marked failing: %+v", status)
			}
			entries, err := st.HealthEntries()
			if err != nil {
				t.Fatal(err)
			}
			var got *store.HealthEntry
			for i := range entries {
				if entries[i].Entry == "effect:notice" {
					got = &entries[i]
				}
			}
			if got == nil {
				t.Fatalf("health entry missing: %+v", entries)
			}
			if got.State != "error" || got.Detail == "" {
				t.Fatalf("health state %q detail %q, want error with detail", got.State, got.Detail)
			}
		}
		clock.Set(due)
	}
}

// A queued row survives a process restart and runs on the next start.
func TestEffectSurvivesRestart(t *testing.T) {
	st := newStore(t)
	clock := newFakeClock()

	before := newQueue(t, st, clock)
	// No handler and no Start: exactly what a crash before the executor was wired
	// would leave behind.
	if err := st.Atomic(func() error {
		return before.Enqueue("relay", "survivor", map[string]string{"text": "again"})
	}); err != nil {
		t.Fatal(err)
	}

	after := newQueue(t, st, clock)
	var got string
	mustHandle(t, after, "relay", func(_ context.Context, key string, payload []byte) error {
		got = string(payload)
		return nil
	}, effects.Options{})
	after.Start()
	if err := after.Idle(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	if got != `{"text":"again"}` {
		t.Fatalf("replayed payload %q", got)
	}
	if pending, _ := after.Pending("relay", "survivor"); pending {
		t.Fatal("the replayed effect was not removed")
	}
}

// Drain stops starting work and keeps what a running attempt was cut short on;
// the next start retries it rather than counting it as a failure.
func TestDrainKeepsCutShortEffectForNextStart(t *testing.T) {
	st := newStore(t)
	clock := newFakeClock()

	q1 := newQueue(t, st, clock)
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	mustHandle(t, q1, "relay", func(context.Context, string, []byte) error {
		once.Do(func() { close(started) })
		<-release
		return errors.New("aborted by shutdown")
	}, effects.Options{})
	q1.Start()
	if err := q1.Enqueue("relay", "cut", nil); err != nil {
		t.Fatal(err)
	}
	<-started

	drained := make(chan error, 1)
	go func() { drained <- q1.Drain(context.Background()) }()
	time.Sleep(10 * time.Millisecond)
	close(release)
	if err := <-drained; err != nil {
		t.Fatal(err)
	}
	if pending, _ := q1.Pending("relay", "cut"); !pending {
		t.Fatal("a shutdown counted a cut-short attempt as done")
	}
	if n := effectAttempts(t, st, "relay", "cut"); n != 0 {
		t.Fatalf("a shutdown counted as a failure: attempts=%d", n)
	}

	q2 := newQueue(t, st, clock)
	var ran int
	mustHandle(t, q2, "relay", func(context.Context, string, []byte) error {
		ran++
		return nil
	}, effects.Options{})
	q2.Start()
	if err := q2.Idle(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	if ran != 1 {
		t.Fatalf("next start ran the cut effect %d times, want 1", ran)
	}
}

// A nested transaction that rolls back drops only its own effect.
func TestNestedRollbackDropsOnlyInnerEffect(t *testing.T) {
	st := newStore(t)
	clock := newFakeClock()
	q := newQueue(t, st, clock)
	rec := &recorder{}
	mustHandle(t, q, "relay", func(_ context.Context, key string, _ []byte) error {
		rec.add(key)
		return nil
	}, effects.Options{})
	q.Start()

	inner := errors.New("inner failed")
	if err := st.Atomic(func() error {
		if err := q.Enqueue("relay", "outer", nil); err != nil {
			return err
		}
		if err := st.Atomic(func() error {
			if err := q.Enqueue("relay", "inner", nil); err != nil {
				return err
			}
			return inner
		}); !errors.Is(err, inner) {
			t.Fatalf("inner returned %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := q.Idle(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	got := rec.all()
	if len(got) != 1 || got[0] != "outer" {
		t.Fatalf("ran %v, want only [outer]", got)
	}
	if pending, _ := q.Pending("relay", "inner"); pending {
		t.Fatal("the rolled-back inner effect left a row")
	}
}

// An ordered kind runs one at a time, and an oldest row that is not due yet holds
// the rest of the kind back.
func TestOrderedKindHoldsLaterRows(t *testing.T) {
	st := newStore(t)
	clock := newFakeClock()
	q := newQueue(t, st, clock)

	var concurrent, maxConcurrent atomic.Int64
	rec := &recorder{}
	mustHandle(t, q, "delivery", func(_ context.Context, key string, _ []byte) error {
		n := concurrent.Add(1)
		defer concurrent.Add(-1)
		for {
			old := maxConcurrent.Load()
			if n <= old || maxConcurrent.CompareAndSwap(old, n) {
				break
			}
		}
		rec.add(key)
		time.Sleep(time.Millisecond)
		if key == "a" {
			return errors.New("first delivery failed")
		}
		return nil
	}, effects.Options{Ordered: true, Limit: func() int { return 8 }})
	q.Start()

	ctx := context.Background()
	if err := q.Enqueue("delivery", "a", nil); err != nil {
		t.Fatal(err)
	}
	if err := q.Idle(ctx, ""); err != nil {
		t.Fatal(err)
	}
	// a failed and is now waiting for its backoff. b is due but must wait: an
	// ordered kind runs in queue order, not whenever a row happens to be ready.
	if err := q.Enqueue("delivery", "b", nil); err != nil {
		t.Fatal(err)
	}
	if err := q.Idle(ctx, ""); err != nil {
		t.Fatal(err)
	}
	if got := rec.all(); len(got) != 1 || got[0] != "a" {
		t.Fatalf("ordered kind ran %v, want only [a] while a waits", got)
	}
	if pending, _ := q.Pending("delivery", "b"); !pending {
		t.Fatal("b was let through ahead of the ordered queue")
	}
	if got := maxConcurrent.Load(); got > 1 {
		t.Fatalf("%d ordered effects ran at once", got)
	}

	// Once a is due again it runs first, still holding b back.
	clock.Set(effectDue(t, st, "delivery", "a"))
	if err := q.Idle(ctx, ""); err != nil {
		t.Fatal(err)
	}
	if got := rec.all(); len(got) != 2 || got[1] != "a" {
		t.Fatalf("ordered kind ran %v, want a again before b", got)
	}
}

// With a limit of one the queue is strictly oldest first.
func TestEffectRunsInQueueOrder(t *testing.T) {
	st := newStore(t)
	clock := newFakeClock()
	q := newQueue(t, st, clock)
	rec := &recorder{}
	mustHandle(t, q, "relay", func(_ context.Context, key string, _ []byte) error {
		rec.add(key)
		return nil
	}, effects.Options{Limit: func() int { return 1 }})
	q.Start()

	if err := st.Atomic(func() error {
		for _, key := range []string{"a", "b", "c"} {
			if err := q.Enqueue("relay", key, nil); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := q.Idle(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	got := rec.all()
	want := []string{"a", "b", "c"}
	if len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("ran %v, want %v", got, want)
	}
}

func sameSet(got []string, want ...string) bool {
	if len(got) != len(want) {
		return false
	}
	seen := map[string]int{}
	for _, g := range got {
		seen[g]++
	}
	for _, w := range want {
		seen[w]--
		if seen[w] != 0 {
			return false
		}
	}
	return true
}

// Re-enqueueing one kind+key inside the same command keeps a single row and the
// row's original queue position, with the latest payload and an immediate due time.
func TestEnqueueTwiceInOneCommandCoalescesInPlace(t *testing.T) {
	st := newStore(t)
	clock := newFakeClock()
	q := newQueue(t, st, clock)

	clock.Advance(time.Minute) // the re-enqueue must move due to the new now
	if err := st.Atomic(func() error {
		if err := q.Enqueue("relay", "a", map[string]string{"text": "first"}); err != nil {
			return err
		}
		if err := q.Enqueue("relay", "b", map[string]string{"text": "other"}); err != nil {
			return err
		}
		if err := q.Enqueue("relay", "a", map[string]string{"text": "second"}); err != nil {
			return err
		}
		clock.Advance(time.Minute)
		return q.Enqueue("relay", "a", map[string]string{"text": "third"})
	}); err != nil {
		t.Fatal(err)
	}

	var rows int
	if err := st.QueryRow(`SELECT COUNT(*) FROM effect WHERE kind='relay'`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 2 {
		t.Fatalf("%d rows, want 2: the same kind+key must coalesce", rows)
	}
	var seqA, seqB int64
	if err := st.QueryRow(`SELECT seq FROM effect WHERE kind='relay' AND key='a'`).Scan(&seqA); err != nil {
		t.Fatal(err)
	}
	if err := st.QueryRow(`SELECT seq FROM effect WHERE kind='relay' AND key='b'`).Scan(&seqB); err != nil {
		t.Fatal(err)
	}
	if seqA >= seqB {
		t.Fatalf("coalesced row moved to the back: seq(a)=%d seq(b)=%d", seqA, seqB)
	}
	var payload string
	if err := st.QueryRow(`SELECT payload FROM effect WHERE kind='relay' AND key='a'`).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	if payload != `{"text":"third"}` {
		t.Fatalf("payload %q, want the latest", payload)
	}
	// The last enqueue is due immediately, not after the coalesced row's original
	// due time.
	if due, want := effectDue(t, st, "relay", "a"), clock.Now(); !due.Equal(want) {
		t.Fatalf("due %v, want %v", due, want)
	}
}
