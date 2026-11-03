// Package workflow 是一个线性的多步骤执行器：把一个任务拆成命名步骤，按顺序
// 交给已注册的 handler，并记录每步的状态与输出。
//
// 它不提供跨进程恢复：Store 是可注入的（应用里用 MemoryStore，进程退出即丢），
// 历史上那套 SQLite 持久层与 Resume 没有生产调用者，已按 F18 删除。若真要做
// durable 编排，应当用 eino compose.Graph + CheckPointStore，而不是继续维护一份
// 只有 CLI 在用的持久化外壳（design §5.10 描述的能力属于后续版本）。
package workflow

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// Step is one unit of work. Its fields are data so a run can be replayed.
type Step struct {
	ID    string
	Title string
	// Handler is the name of a registered handler. Empty means "record only",
	// which is useful for gate/wait steps.
	Handler string
	// Args are passed to the handler verbatim.
	Args map[string]string
}

// Definition is a named sequence of steps.
type Definition struct {
	Name  string
	Steps []Step
}

// Validate refuses a definition that could not be replayed deterministically.
func (d Definition) Validate() error {
	if strings.TrimSpace(d.Name) == "" {
		return errors.New("workflow: a definition needs a name")
	}
	if len(d.Steps) == 0 {
		return errors.New("workflow: a definition needs at least one step")
	}
	seen := map[string]bool{}
	for _, s := range d.Steps {
		if strings.TrimSpace(s.ID) == "" {
			return errors.New("workflow: a step needs an id")
		}
		if seen[s.ID] {
			return fmt.Errorf("workflow: duplicate step id %q", s.ID)
		}
		seen[s.ID] = true
	}
	return nil
}

// Handler does one step. It receives the run's accumulated outputs, so a later
// step can use what an earlier one produced.
type Handler func(ctx context.Context, in HandlerInput) (string, error)

// HandlerInput is everything a handler needs.
type HandlerInput struct {
	RunID  string
	StepID string
	Args   map[string]string
	// Outputs are the results of the steps before this one, keyed by step id.
	Outputs map[string]string
	// Input is the run's original input.
	Input map[string]string
}

// Status is where a run is.
type Status string

const (
	StatusRunning     Status = "running"
	StatusDone        Status = "done"
	StatusFailed      Status = "failed"
	StatusBlocked     Status = "blocked"
	StatusInterrupted Status = "interrupted"
)

// Run is one execution of a definition.
type Run struct {
	ID         string
	Workflow   string
	Input      map[string]string
	Status     Status
	StepStatus map[string]string // step id → pending|running|done|failed|skipped
	Outputs    map[string]string
	Error      string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// Store persists runs. It is an interface so the engine can use SQLite in the app
// and memory in tests, without the workflow package owning a schema rule.
type Store interface {
	SaveRun(ctx context.Context, r *Run) error
	LoadRun(ctx context.Context, id string) (*Run, error)
}

// ErrRetry asks the runner to try the step again later.
var ErrRetry = errors.New("workflow: retry later")

// ErrBlocked parks the run: a person or an event has to act before it continues.
var ErrBlocked = errors.New("workflow: blocked")

// Runner executes definitions against a registry of handlers.
type Runner struct {
	store Store
	reg   map[string]Handler
	now   func() time.Time
	mu    sync.Mutex
	clock func() time.Time
}

// New builds a runner.
func New(store Store) (*Runner, error) {
	if store == nil {
		return nil, errors.New("workflow: a store is required")
	}
	return &Runner{store: store, reg: map[string]Handler{}, now: time.Now}, nil
}

// Register adds a handler. Registering the same name twice is a programming error,
// not a runtime one: it means two features claim the same step type.
func (r *Runner) Register(name string, h Handler) error {
	if name == "" || h == nil {
		return errors.New("workflow: handler needs a name and a body")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.reg[name]; ok {
		return fmt.Errorf("workflow: handler %q registered twice", name)
	}
	r.reg[name] = h
	return nil
}

// Handlers lists the registered handler names, for health output.
func (r *Runner) Handlers() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.reg))
	for n := range r.reg {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Start begins a run and executes it until it finishes, blocks or fails.
func (r *Runner) Start(ctx context.Context, d Definition, input map[string]string) (*Run, error) {
	if err := d.Validate(); err != nil {
		return nil, err
	}
	now := r.now()
	run := &Run{
		ID:         fmt.Sprintf("%s-%d", d.Name, now.UnixNano()),
		Workflow:   d.Name,
		Input:      clone(input),
		Status:     StatusRunning,
		StepStatus: map[string]string{},
		Outputs:    map[string]string{},
		CreatedAt:  now, UpdatedAt: now,
	}
	for _, s := range d.Steps {
		run.StepStatus[s.ID] = "pending"
	}
	return r.run(ctx, d, run)
}

// run is the single execution loop. It persists after every step, so the store is
// always the truth (design §7.2: an effect must survive a crash).
func (r *Runner) run(ctx context.Context, d Definition, run *Run) (*Run, error) {
	for _, s := range d.Steps {
		if st := run.StepStatus[s.ID]; st == "done" || st == "skipped" {
			continue
		}
		if s.Handler == "" {
			run.StepStatus[s.ID] = "done"
			r.touch(run)
			if err := r.store.SaveRun(ctx, run); err != nil {
				return run, err
			}
			continue
		}
		r.mu.Lock()
		h := r.reg[s.Handler]
		r.mu.Unlock()
		if h == nil {
			run.Status = StatusFailed
			run.Error = fmt.Sprintf("no handler %q for step %q", s.Handler, s.ID)
			r.touch(run)
			_ = r.store.SaveRun(ctx, run)
			return run, fmt.Errorf("workflow: %s", run.Error)
		}

		run.StepStatus[s.ID] = "running"
		r.touch(run)
		if err := r.store.SaveRun(ctx, run); err != nil {
			return run, err
		}

		out, err := h(ctx, HandlerInput{
			RunID: run.ID, StepID: s.ID, Args: clone(s.Args),
			Outputs: clone(run.Outputs), Input: clone(run.Input),
		})
		switch {
		case err == nil:
			run.Outputs[s.ID] = out
			run.StepStatus[s.ID] = "done"
		case errors.Is(err, ErrBlocked):
			run.StepStatus[s.ID] = "pending"
			run.Status = StatusBlocked
			run.Error = err.Error()
			r.touch(run)
			_ = r.store.SaveRun(ctx, run)
			return run, err
		case errors.Is(err, ErrRetry):
			run.StepStatus[s.ID] = "pending"
			run.Status = StatusInterrupted
			run.Error = err.Error()
			r.touch(run)
			_ = r.store.SaveRun(ctx, run)
			return run, err
		default:
			run.StepStatus[s.ID] = "failed"
			run.Status = StatusFailed
			run.Error = err.Error()
			r.touch(run)
			_ = r.store.SaveRun(ctx, run)
			return run, err
		}
		r.touch(run)
		if err := r.store.SaveRun(ctx, run); err != nil {
			return run, err
		}
	}
	run.Status = StatusDone
	r.touch(run)
	if err := r.store.SaveRun(ctx, run); err != nil {
		return run, err
	}
	return run, nil
}

func (r *Runner) touch(run *Run) { run.UpdatedAt = r.now() }

func clone(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
