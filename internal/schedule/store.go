package schedule

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/zeokeel/super-agent/internal/domain"
	"github.com/zeokeel/super-agent/internal/store"
)

var (
	// ErrNotFound reports an unknown schedule id.
	ErrNotFound = errors.New("schedule: not found")
	// ErrUnknownFire reports a fire id that was never recorded.
	ErrUnknownFire = errors.New("schedule: unknown fire")
)

// Definition is a persisted schedule plus the durable trigger bookkeeping.
// NextAt is the only field ClaimDue compares, so a poll is one indexed read.
type Definition struct {
	ID string
	// Task is what the timer does when it fires. It is the sentence a person
	// reads in the panel, so it is persisted with the definition.
	Task        string
	WorkspaceID domain.WorkspaceID // empty for a global schedule
	Spec        Spec
	Enabled     bool
	CreatedAt   time.Time
	UpdatedAt   time.Time

	LastFiredAt    time.Time
	NextAt         time.Time // zero when nothing is due again (a fired "at")
	RunningFire    string    // non-empty while a fire is claimed and not completed
	LastStatus     string
	LastSkipReason string
	LastSkipAt     time.Time
}

// Claim is one due occurrence. A skipped claim is not an error: the runtime sees
// it so the panel can explain why a scheduled trigger did not happen.
type Claim struct {
	FireID     string
	Definition Definition
	PlannedAt  time.Time
	FiredAt    time.Time
	Status     string // "fired" | "skipped"
	Reason     string
}

// Fire is one recorded occurrence, fired or skipped.
type Fire struct {
	ID         string
	ScheduleID string
	PlannedAt  time.Time
	FiredAt    time.Time
	Status     string
	Reason     string
}

// Manager persists schedules and claims their due occurrences. It holds no
// timers: the runtime calls ClaimDue on its own pass, which is what keeps the
// clock out of the durable state.
type Manager struct {
	st  *store.Store
	now func() time.Time
}

// Option configures a Manager.
type Option func(*Manager)

// WithClock replaces the clock, for tests and for replaying a boot.
func WithClock(now func() time.Time) Option { return func(m *Manager) { m.now = now } }

// New opens the schedule manager. The schedule tables are part of the ordered
// schema (v3), so there is nothing to create here: opening the store already
// migrated the file.
func New(st *store.Store, opts ...Option) (*Manager, error) {
	if st == nil {
		return nil, fmt.Errorf("schedule: nil store")
	}
	m := &Manager{st: st, now: time.Now}
	for _, opt := range opts {
		opt(m)
	}
	return m, nil
}

// Put validates and stores a definition. An empty ID is rejected: the runtime
// derives it from the Source and workspace so a retried tool call is idempotent.
func (m *Manager) Put(def Definition) (Definition, error) {
	if def.ID == "" {
		return Definition{}, fmt.Errorf("schedule: empty id")
	}
	sch, err := Compile(def.Spec)
	if err != nil {
		return Definition{}, err
	}
	now := m.now().UTC()
	if def.CreatedAt.IsZero() {
		def.CreatedAt = now
	}
	def.UpdatedAt = now
	if def.Enabled && def.NextAt.IsZero() {
		if next, ok := sch.Next(now); ok {
			def.NextAt = next
		}
	}
	if !def.Enabled {
		def.NextAt = time.Time{}
	}
	b, err := json.Marshal(def.Spec)
	if err != nil {
		return Definition{}, err
	}
	var ws any
	if def.WorkspaceID != "" {
		ws = string(def.WorkspaceID)
	}
	_, err = m.st.Exec(`
		INSERT INTO schedule(id,task,workspace_id,spec,enabled,created_at,updated_at,
		  last_fired_at,next_at,running_fire,last_status,last_skip_reason,last_skip_at)
		VALUES(?,?,?,?,?,?,?,?,?,'',?,'',?)
		ON CONFLICT(id) DO UPDATE SET
		  task=excluded.task,
		  workspace_id=excluded.workspace_id, spec=excluded.spec, enabled=excluded.enabled,
		  updated_at=excluded.updated_at, last_fired_at=excluded.last_fired_at,
		  next_at=excluded.next_at, last_status=excluded.last_status`,
		def.ID, def.Task, ws, string(b), boolInt(def.Enabled), formatTime(def.CreatedAt), formatTime(now),
		nullTime(def.LastFiredAt), nullTime(def.NextAt), def.LastStatus, nullTime(def.LastSkipAt))
	if err != nil {
		return Definition{}, fmt.Errorf("schedule: put: %w", err)
	}
	return def, nil
}

// Get returns one definition.
func (m *Manager) Get(id string) (Definition, bool, error) {
	row := m.st.QueryRow(`
		SELECT id,task,workspace_id,spec,enabled,created_at,updated_at,last_fired_at,next_at,
		       running_fire,last_status,last_skip_reason,last_skip_at
		FROM schedule WHERE id=?`, id)
	def, err := scanDefinition(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Definition{}, false, nil
	}
	if err != nil {
		return Definition{}, false, err
	}
	return def, true, nil
}

// List returns every definition, oldest first. The panel shows the same order.
func (m *Manager) List() ([]Definition, error) {
	rows, err := m.st.Query(`
		SELECT id,task,workspace_id,spec,enabled,created_at,updated_at,last_fired_at,next_at,
		       running_fire,last_status,last_skip_reason,last_skip_at
		FROM schedule ORDER BY created_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Definition
	for rows.Next() {
		def, err := scanDefinition(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, def)
	}
	return out, rows.Err()
}

// SetEnabled turns a definition on or off. Enabling recomputes the next
// occurrence from now, so time spent disabled never produces a catch-up burst.
func (m *Manager) SetEnabled(id string, enabled bool) (Definition, error) {
	def, ok, err := m.Get(id)
	if err != nil {
		return Definition{}, err
	}
	if !ok {
		return Definition{}, ErrNotFound
	}
	sch, err := Compile(def.Spec)
	if err != nil {
		return Definition{}, err
	}
	now := m.now().UTC()
	def.UpdatedAt = now
	var next time.Time
	if enabled {
		if n, ok := sch.Next(now); ok {
			next = n
		}
	}
	_, err = m.st.Exec(`
		UPDATE schedule SET enabled=?, next_at=?, updated_at=? WHERE id=?`,
		boolInt(enabled), nullTime(next), formatTime(now), id)
	if err != nil {
		return Definition{}, err
	}
	def.Enabled = enabled
	def.NextAt = next
	return def, nil
}

// Delete removes the definition; fire history stays for audit.
func (m *Manager) Delete(id string) error {
	res, err := m.st.Exec(`DELETE FROM schedule WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ClaimDue records and returns every occurrence due at now. It runs as one
// command: a crash cannot leave a fire row without its schedule bookkeeping, or
// the bookkeeping without the row.
//
// Catch-up policy: at most one occurrence fires per schedule per call. The
// bookkeeping then advances from now, so a week of missed daily runs becomes one
// run, not seven. Overlap policy: while a previous fire is still marked running
// the due occurrence is recorded skipped with the reason, never queued.
func (m *Manager) ClaimDue(now time.Time) ([]Claim, error) {
	now = now.UTC()
	var claims []Claim
	err := m.st.Atomic(func() error {
		rows, err := m.st.Query(`
			SELECT id,task,workspace_id,spec,enabled,created_at,updated_at,last_fired_at,next_at,
			       running_fire,last_status,last_skip_reason,last_skip_at
			FROM schedule
			WHERE enabled=1 AND next_at IS NOT NULL AND next_at <= ?
			ORDER BY next_at, id`, formatTime(now))
		if err != nil {
			return err
		}
		var due []Definition
		for rows.Next() {
			def, err := scanDefinition(rows)
			if err != nil {
				rows.Close()
				return err
			}
			due = append(due, def)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()

		for _, def := range due {
			sch, err := Compile(def.Spec)
			if err != nil {
				return fmt.Errorf("schedule: stored definition %s: %w", def.ID, err)
			}
			planned := def.NextAt
			fireID := fireID(def, planned)
			next, hasNext := sch.Next(now)
			nextAt := nullTime(next)
			if !hasNext {
				nextAt = nil
			}

			if def.RunningFire != "" {
				reason := fmt.Sprintf("上一轮仍在运行（fire %s），本轮跳过", def.RunningFire)
				if _, err := m.st.Exec(`
					INSERT INTO schedule_fire(id,schedule_id,planned_at,fired_at,status,reason,created_at)
					VALUES(?,?,?,?,?,?,?)
					ON CONFLICT(id) DO NOTHING`,
					fireID, def.ID, formatTime(planned), formatTime(now), "skipped", reason, formatTime(now)); err != nil {
					return err
				}
				if _, err := m.st.Exec(`
					UPDATE schedule SET last_fired_at=?, next_at=?, updated_at=?,
					  last_status='skipped', last_skip_reason=?, last_skip_at=? WHERE id=?`,
					formatTime(now), nextAt, formatTime(now), reason, formatTime(now), def.ID); err != nil {
					return err
				}
				def.LastFiredAt = now
				def.NextAt = next
				def.LastStatus = "skipped"
				def.LastSkipReason = reason
				def.LastSkipAt = now
				claims = append(claims, Claim{FireID: fireID, Definition: def, PlannedAt: planned,
					FiredAt: now, Status: "skipped", Reason: reason})
				continue
			}

			if _, err := m.st.Exec(`
				INSERT INTO schedule_fire(id,schedule_id,planned_at,fired_at,status,reason,created_at)
				VALUES(?,?,?,?,?,?,?)
				ON CONFLICT(id) DO NOTHING`,
				fireID, def.ID, formatTime(planned), formatTime(now), "fired", "", formatTime(now)); err != nil {
				return err
			}
			if _, err := m.st.Exec(`
				UPDATE schedule SET last_fired_at=?, next_at=?, updated_at=?,
				  running_fire=?, last_status='fired' WHERE id=?`,
				formatTime(now), nextAt, formatTime(now), fireID, def.ID); err != nil {
				return err
			}
			def.LastFiredAt = now
			def.NextAt = next
			def.RunningFire = fireID
			def.LastStatus = "fired"
			claims = append(claims, Claim{FireID: fireID, Definition: def, PlannedAt: planned,
				FiredAt: now, Status: "fired"})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return claims, nil
}

// ClaimDueNow claims at the manager's clock.
func (m *Manager) ClaimDueNow() ([]Claim, error) { return m.ClaimDue(m.now()) }

// Complete marks a fired occurrence finished, clearing the overlap guard. It is
// idempotent: completing an already completed fire is not an error, which makes
// a runtime retry after a crash safe.
func (m *Manager) Complete(fireID string) error {
	return m.st.Atomic(func() error {
		var status string
		err := m.st.QueryRow(`SELECT status FROM schedule_fire WHERE id=?`, fireID).Scan(&status)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrUnknownFire
		}
		if err != nil {
			return err
		}
		if status == "completed" {
			return nil
		}
		if _, err := m.st.Exec(`UPDATE schedule_fire SET status='completed', reason='' WHERE id=?`, fireID); err != nil {
			return err
		}
		_, err = m.st.Exec(`UPDATE schedule SET running_fire='' WHERE running_fire=?`, fireID)
		return err
	})
}

// Recover settles occurrences a crash left running. Called once at startup,
// before the first ClaimDue: without it the overlap guard would skip every
// future occurrence of a schedule whose worker died mid-run.
func (m *Manager) Recover(now time.Time) (int, error) {
	n := 0
	err := m.st.Atomic(func() error {
		rows, err := m.st.Query(`SELECT id FROM schedule_fire WHERE status='fired'`)
		if err != nil {
			return err
		}
		var ids []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, id)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		for _, id := range ids {
			if _, err := m.st.Exec(`UPDATE schedule_fire SET status='interrupted',
				reason='进程重启时仍在运行，上一轮结果未知' WHERE id=?`, id); err != nil {
				return err
			}
		}
		if _, err := m.st.Exec(`UPDATE schedule SET running_fire='' WHERE running_fire != ''`); err != nil {
			return err
		}
		n = len(ids)
		return nil
	})
	return n, err
}

// Fires returns the most recent recorded occurrences of one schedule.
func (m *Manager) Fires(scheduleID string, limit int) ([]Fire, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := m.st.Query(`
		SELECT id,schedule_id,planned_at,fired_at,status,reason
		FROM schedule_fire WHERE schedule_id=? ORDER BY fired_at DESC LIMIT ?`, scheduleID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Fire
	for rows.Next() {
		var f Fire
		var planned, fired string
		if err := rows.Scan(&f.ID, &f.ScheduleID, &planned, &fired, &f.Status, &f.Reason); err != nil {
			return nil, err
		}
		f.PlannedAt, _ = store.ParseTime(planned)
		f.FiredAt, _ = store.ParseTime(fired)
		out = append(out, f)
	}
	return out, rows.Err()
}

// fireID is the Trigger id of one occurrence: content-derived from the schedule
// and the planned instant, so a retried claim or a restarted process uses the
// same id and the domain's idempotency catches it.
func fireID(def Definition, planned time.Time) string {
	scope := domain.Global
	if def.WorkspaceID != "" {
		scope = domain.WorkspaceScope(def.WorkspaceID)
	}
	return string(domain.Key(scope, "schedule", def.ID, store.FormatTime(planned)))
}

type rowScanner interface{ Scan(...any) error }

func scanDefinition(r rowScanner) (Definition, error) {
	var (
		def                           Definition
		ws                            sql.NullString
		spec                          string
		enabled                       int
		created, updated              string
		lastFired, nextAt, lastSkipAt sql.NullString
	)
	if err := r.Scan(&def.ID, &def.Task, &ws, &spec, &enabled, &created, &updated,
		&lastFired, &nextAt, &def.RunningFire, &def.LastStatus, &def.LastSkipReason, &lastSkipAt); err != nil {
		return Definition{}, err
	}
	def.WorkspaceID = domain.WorkspaceID(ws.String)
	def.Enabled = enabled != 0
	if err := json.Unmarshal([]byte(spec), &def.Spec); err != nil {
		return Definition{}, fmt.Errorf("schedule: spec of %s: %w", def.ID, err)
	}
	def.CreatedAt, _ = store.ParseTime(created)
	def.UpdatedAt, _ = store.ParseTime(updated)
	if lastFired.Valid {
		def.LastFiredAt, _ = store.ParseTime(lastFired.String)
	}
	if nextAt.Valid {
		def.NextAt, _ = store.ParseTime(nextAt.String)
	}
	if lastSkipAt.Valid {
		def.LastSkipAt, _ = store.ParseTime(lastSkipAt.String)
	}
	return def, nil
}

func formatTime(t time.Time) string { return store.FormatTime(t) }

func nullTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return store.FormatTime(t)
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
