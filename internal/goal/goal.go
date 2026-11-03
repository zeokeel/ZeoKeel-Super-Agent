// Package goal owns long-running objectives: an immutable objective, a small
// control state (active / paused / completed / blocked), numbered rounds with
// their evidence, a hard round budget, and the rule that a blocking condition
// must repeat before the goal is called blocked. See design §5.10 ("goal is
// auto-continue with hard limits") and §7.2 (the store's durable single writer).
package goal

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/zeokeel/super-agent/internal/domain"
	"github.com/zeokeel/super-agent/internal/store"
)

// State is the lifecycle of a goal.
type State string

const (
	Active    State = "active"
	Paused    State = "paused"
	Completed State = "completed"
	Blocked   State = "blocked"
)

// DefaultBlockThreshold is how many consecutive rounds must report the same
// blocking condition before the goal becomes blocked. Three is enough to tell a
// real blocker from a transient one; a person can still mark it blocked by hand.
const DefaultBlockThreshold = 3

var (
	// ErrNotFound reports an unknown goal id.
	ErrNotFound = errors.New("goal: not found")
	// ErrConflict reports a stale revision: the caller read an older state.
	ErrConflict = errors.New("goal: revision conflict")
	// ErrNotActive reports a mutation that needs an active goal.
	ErrNotActive = errors.New("goal: not active")
	// ErrInvalidTransition reports a state change the lifecycle does not allow.
	ErrInvalidTransition = errors.New("goal: invalid state transition")
	// ErrInvalid reports an invalid create or edit value.
	ErrInvalid = errors.New("goal: invalid")
)

// Round is one step of a goal. Summary and Evidence are what the panel shows and
// what the next round's model input is built from; Blocked names the condition
// the round hit, empty when it made progress.
type Round struct {
	Index    int       `json:"index"`
	Summary  string    `json:"summary"`
	Evidence []string  `json:"evidence,omitempty"`
	Blocked  string    `json:"blocked,omitempty"`
	At       time.Time `json:"at"`
}

// Goal is one durable objective. Objective never changes: a changed objective is
// a different goal, which keeps the round history meaningful.
type Goal struct {
	ID             string             `json:"id"`
	Workspace      domain.WorkspaceID `json:"workspace,omitempty"`
	Objective      string             `json:"objective"`
	State          State              `json:"state"`
	Revision       uint64             `json:"revision"`
	MaxRounds      int                `json:"max_rounds"`
	BlockThreshold int                `json:"block_threshold"`

	// ConsecutiveBlocked and BlockedCondition are the threshold accounting: the
	// same condition must repeat; a different one or a productive round resets it.
	ConsecutiveBlocked int    `json:"consecutive_blocked,omitempty"`
	BlockedCondition   string `json:"blocked_condition,omitempty"`
	BlockedReason      string `json:"blocked_reason,omitempty"`
	EndReason          string `json:"end_reason,omitempty"`

	Rounds    []Round   `json:"rounds,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// RoundInput is what one advance reports.
type RoundInput struct {
	Summary  string
	Evidence []string
	// Blocked is the condition that stopped this round, if any. Repeating the
	// same condition across rounds is what eventually marks the goal blocked.
	Blocked string
}

// Editable is the part of a goal a person may change. Objective is deliberately
// absent: changing it would silently rewrite the meaning of the round history.
type Editable struct {
	MaxRounds      int
	BlockThreshold int
}

// Option configures a Service.
type Option func(*Service)

// WithClock replaces the clock for tests and replays.
func WithClock(now func() time.Time) Option { return func(s *Service) { s.now = now } }

// WithBlockThreshold changes the default threshold for goals created by this
// service. A goal keeps the threshold it was created with unless edited.
func WithBlockThreshold(n int) Option {
	return func(s *Service) {
		if n > 0 {
			s.threshold = n
		}
	}
}

// Service is the goal domain service. It owns no goroutines: the runtime decides
// when a round happens and asks Advance; durability is a single SQL command.
type Service struct {
	st        *store.Store
	now       func() time.Time
	threshold int
}

// New opens the goal service. The goal tables are part of the ordered schema
// (v3), so opening the store already migrated the file.
func New(st *store.Store, opts ...Option) (*Service, error) {
	if st == nil {
		return nil, fmt.Errorf("goal: nil store")
	}
	s := &Service{st: st, now: time.Now, threshold: DefaultBlockThreshold}
	for _, opt := range opts {
		opt(s)
	}
	return s, nil
}

// Create starts a goal. Its ID is content-derived from the workspace and the
// objective, so retrying the same tool call returns the same goal instead of
// starting a second round history.
func (s *Service) Create(ws domain.WorkspaceID, objective string, maxRounds int) (Goal, error) {
	objective = strings.TrimSpace(objective)
	if objective == "" {
		return Goal{}, fmt.Errorf("%w: empty objective", ErrInvalid)
	}
	if maxRounds <= 0 {
		return Goal{}, fmt.Errorf("%w: max rounds must be positive", ErrInvalid)
	}
	scope := domain.Global
	if ws != "" {
		scope = domain.WorkspaceScope(ws)
	}
	id := string(domain.Key(scope, "goal", objective))
	if existing, ok, err := s.Get(id); err != nil {
		return Goal{}, err
	} else if ok {
		return existing, nil // idempotent create
	}
	now := s.now().UTC()
	g := Goal{
		ID: id, Workspace: ws, Objective: objective, State: Active, Revision: 1,
		MaxRounds: maxRounds, BlockThreshold: s.threshold,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := s.save(g, 0); err != nil {
		return Goal{}, err
	}
	return g, nil
}

// Get returns one goal.
func (s *Service) Get(id string) (Goal, bool, error) {
	var payload string
	err := s.st.QueryRow(`SELECT json FROM goal WHERE id=?`, id).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return Goal{}, false, nil
	}
	if err != nil {
		return Goal{}, false, err
	}
	var g Goal
	if err := json.Unmarshal([]byte(payload), &g); err != nil {
		return Goal{}, false, fmt.Errorf("goal: decode %s: %w", id, err)
	}
	return g, true, nil
}

// List returns the workspace's goals, most recently updated first. An empty
// workspace lists every goal, which the panel's cross-workspace view uses.
func (s *Service) List(ws domain.WorkspaceID) ([]Goal, error) {
	rows, err := s.st.Query(`SELECT json FROM goal
		WHERE (? = '' OR workspace_id = ?)
		ORDER BY updated_at DESC, id`, string(ws), string(ws))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Goal
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var g Goal
		if err := json.Unmarshal([]byte(payload), &g); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// Pause stops automatic rounds. Only an active goal can pause.
func (s *Service) Pause(id string, rev uint64) (Goal, error) {
	return s.mutate(id, rev, func(g *Goal) error {
		if g.State != Active {
			return fmt.Errorf("%w: pause from %s", ErrInvalidTransition, g.State)
		}
		g.State = Paused
		g.EndReason = "paused by operator"
		return nil
	})
}

// Resume returns a paused or blocked goal to active and clears the blocking
// accounting: after a person's decision the old condition should not count
// toward a new block.
func (s *Service) Resume(id string, rev uint64) (Goal, error) {
	return s.mutate(id, rev, func(g *Goal) error {
		if g.State != Paused && g.State != Blocked {
			return fmt.Errorf("%w: resume from %s", ErrInvalidTransition, g.State)
		}
		g.State = Active
		g.EndReason = ""
		g.BlockedReason = ""
		g.BlockedCondition = ""
		g.ConsecutiveBlocked = 0
		return nil
	})
}

// Complete ends a goal by a person's decision. Any live state may complete;
// completing twice is an error, not a silent no-op.
func (s *Service) Complete(id string, rev uint64, summary string) (Goal, error) {
	return s.mutate(id, rev, func(g *Goal) error {
		if g.State == Completed {
			return fmt.Errorf("%w: already completed", ErrInvalidTransition)
		}
		g.State = Completed
		if strings.TrimSpace(summary) == "" {
			summary = "completed by operator"
		}
		g.EndReason = summary
		return nil
	})
}

// Edit changes the limits, never the objective. Completed goals are frozen.
func (s *Service) Edit(id string, rev uint64, fn func(*Editable) error) (Goal, error) {
	return s.mutate(id, rev, func(g *Goal) error {
		if g.State == Completed {
			return fmt.Errorf("%w: completed goals are frozen", ErrInvalidTransition)
		}
		e := Editable{MaxRounds: g.MaxRounds, BlockThreshold: g.BlockThreshold}
		if fn != nil {
			if err := fn(&e); err != nil {
				return err
			}
		}
		if e.MaxRounds <= 0 || e.MaxRounds < len(g.Rounds) {
			return fmt.Errorf("%w: max rounds %d is below the %d already done", ErrInvalid, e.MaxRounds, len(g.Rounds))
		}
		if e.BlockThreshold <= 0 {
			return fmt.Errorf("%w: block threshold must be positive", ErrInvalid)
		}
		g.MaxRounds = e.MaxRounds
		g.BlockThreshold = e.BlockThreshold
		return nil
	})
}

// Advance records one round. It is the only way the round count grows, and it
// enforces both hard limits: the round budget completes the goal, and the same
// blocking condition repeated BlockThreshold times blocks it.
func (s *Service) Advance(id string, rev uint64, in RoundInput) (Goal, error) {
	return s.mutate(id, rev, func(g *Goal) error {
		if g.State != Active {
			return fmt.Errorf("%w: advance from %s", ErrNotActive, g.State)
		}
		now := s.now().UTC()
		r := Round{
			Index: len(g.Rounds) + 1, Summary: strings.TrimSpace(in.Summary),
			Evidence: append([]string(nil), in.Evidence...), Blocked: strings.TrimSpace(in.Blocked),
			At: now,
		}
		g.Rounds = append(g.Rounds, r)

		if r.Blocked != "" {
			if g.ConsecutiveBlocked > 0 && strings.EqualFold(g.BlockedCondition, r.Blocked) {
				g.ConsecutiveBlocked++
			} else {
				g.ConsecutiveBlocked = 1
				g.BlockedCondition = r.Blocked
			}
			if g.ConsecutiveBlocked >= g.BlockThreshold {
				g.State = Blocked
				g.BlockedReason = fmt.Sprintf("连续 %d 轮同一阻塞：%s", g.ConsecutiveBlocked, r.Blocked)
				g.EndReason = g.BlockedReason
			}
		} else {
			g.ConsecutiveBlocked = 0
			g.BlockedCondition = ""
		}

		// The hard round budget wins over another round: "到上限停止" must not
		// depend on the model choosing to stop.
		if g.State == Active && len(g.Rounds) >= g.MaxRounds {
			g.State = Completed
			g.EndReason = fmt.Sprintf("达到轮次上限 %d", g.MaxRounds)
		}
		return nil
	})
}

// mutate loads, checks the caller's revision, applies fn, bumps the revision and
// writes with a compare-and-set so a stale writer can never overwrite a newer
// state (the panel and the runtime both advance goals).
func (s *Service) mutate(id string, rev uint64, fn func(*Goal) error) (Goal, error) {
	g, ok, err := s.Get(id)
	if err != nil {
		return Goal{}, err
	}
	if !ok {
		return Goal{}, ErrNotFound
	}
	if g.Revision != rev {
		return Goal{}, fmt.Errorf("%w: have %d, want %d", ErrConflict, g.Revision, rev)
	}
	if err := fn(&g); err != nil {
		return Goal{}, err
	}
	g.Revision++
	g.UpdatedAt = s.now().UTC()
	if err := s.save(g, rev); err != nil {
		return Goal{}, err
	}
	return g, nil
}

// save writes g, requiring the stored revision to still be expected. expected 0
// is an insert (Create); any other value is a compare-and-set update.
func (s *Service) save(g Goal, expected uint64) error {
	payload, err := json.Marshal(g)
	if err != nil {
		return err
	}
	var res sql.Result
	if expected == 0 {
		res, err = s.st.Exec(`
			INSERT INTO goal(id,workspace_id,state,revision,objective,max_rounds,block_threshold,
			  blocked_reason,consecutive_blocked,rounds_count,created_at,updated_at,json)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			g.ID, string(g.Workspace), string(g.State), g.Revision, g.Objective, g.MaxRounds,
			g.BlockThreshold, g.BlockedReason, g.ConsecutiveBlocked, len(g.Rounds),
			formatTime(g.CreatedAt), formatTime(g.UpdatedAt), string(payload))
	} else {
		res, err = s.st.Exec(`
			UPDATE goal SET state=?, revision=?, objective=?, max_rounds=?, block_threshold=?,
			  blocked_reason=?, consecutive_blocked=?, rounds_count=?, updated_at=?, json=?
			WHERE id=? AND revision=? AND created_at=?`,
			string(g.State), g.Revision, g.Objective, g.MaxRounds, g.BlockThreshold,
			g.BlockedReason, g.ConsecutiveBlocked, len(g.Rounds), formatTime(g.UpdatedAt), string(payload),
			g.ID, expected, formatTime(g.CreatedAt))
	}
	if err != nil {
		return fmt.Errorf("goal: save: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrConflict
	}
	return nil
}

func formatTime(t time.Time) string { return store.FormatTime(t) }
