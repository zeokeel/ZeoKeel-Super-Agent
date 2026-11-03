package runtime

// In-process supervisor and runtime self-report. The prototype's 006:133-141
// asks for a periodic check that records health and recovers what a restart would
// fix; this file is that check, plus the health/exit-code pair the headless
// service returns on shutdown.

import (
	"context"
	"fmt"
	"log/slog"
	"runtime/debug"
	"strings"
	"time"

	"github.com/zeokeel/super-agent/internal/domain"
	"github.com/zeokeel/super-agent/internal/engine"
	"github.com/zeokeel/super-agent/internal/store"
)

// healthRuntime is this process's own health row. Other rows belong to the
// effect queue and the Source loops.
const healthRuntime = "runtime"

// stuckAfter bounds how long one Assignment may look "running" before the
// supervisor calls it out. Long tasks are legitimate; silently wedged ones are not.
const stuckAfter = 6 * time.Hour

// Health is the runtime's answer to "can this process keep doing its job?".
// state is "ready" | "setup" | "error"; the exit code is the CLI contract value.
func (r *Runtime) Health() (state, detail string, exitCode int) {
	var component, setup []string
	if !r.HasWorkerModel() {
		setup = append(setup, "还没有配置模型")
	}
	if !r.SetupComplete() {
		setup = append(setup, "首次配置尚未完成")
	}

	r.mu.Lock()
	st := r.settingsStore
	q := r.queue
	r.mu.Unlock()

	if st == nil {
		// Memory mode: there is nothing durable to be unhealthy about, but a
		// missing model still means the process is waiting for a person.
		if len(setup) > 0 {
			return "setup", strings.Join(setup, "；"), ExitSetup
		}
		return "ready", "内存模式", ExitOK
	}
	if err := st.QueryRow(`SELECT 1`).Scan(new(int)); err != nil {
		return "error", "数据库不可读: " + err.Error(), ExitComponent
	}
	if q != nil {
		if s, err := q.Status(engine.EffectLaunch); err == nil && s.Failing {
			component = append(component, fmt.Sprintf("launch 队列连续失败 %d 次: %s", s.Attempts, s.Error))
		}
	}
	if entries, err := st.HealthEntries(); err == nil {
		for _, e := range entries {
			if e.State == "error" && e.Entry != healthRuntime {
				component = append(component, e.Entry+": "+e.Detail)
			}
		}
	}
	if len(component) > 0 {
		return "error", strings.Join(component, "；"), ExitComponent
	}
	if len(setup) > 0 {
		return "setup", strings.Join(setup, "；"), ExitSetup
	}
	return "ready", "", ExitOK
}

// writeHealth upserts one row in the health table. A memory-only install has no
// table and quietly does nothing.
func (r *Runtime) writeHealth(entry, state, detail string) {
	r.mu.Lock()
	st := r.settingsStore
	r.mu.Unlock()
	if st == nil {
		return
	}
	_, err := st.Exec(`INSERT INTO health(entry,state,detail,at) VALUES(?,?,?,?)
		ON CONFLICT(entry) DO UPDATE SET state=excluded.state, detail=excluded.detail, at=excluded.at`,
		entry, state, clipLine(detail, 500), store.FormatTime(r.clock()))
	if err != nil {
		r.mu.Lock()
		r.lastErr = "health: " + err.Error()
		r.mu.Unlock()
	}
}

// SetComponentHealth records one component's self-report. Source loops and IM
// drivers use it so `zeokeel status` (which never opens the engine) can show
// per-component state from the same health table.
func (r *Runtime) SetComponentHealth(part, state, detail string) {
	r.writeHealth(part, state, detail)
}

// StartSupervisor runs the periodic check until ctx ends. It never panics the
// process: a check that could not run is a health problem, not a crash.
func (r *Runtime) StartSupervisor(ctx context.Context, every time.Duration, logger *slog.Logger) {
	if every <= 0 {
		every = time.Minute
	}
	if logger == nil {
		logger = slog.Default()
	}
	go func() {
		defer func() {
			if p := recover(); p != nil {
				logger.Error("supervisor panicked", "panic", p, "stack", string(debug.Stack()))
				r.writeHealth(healthRuntime, "error", fmt.Sprintf("巡检崩溃: %v", p))
			}
		}()
		t := time.NewTicker(every)
		defer t.Stop()
		fails := 0
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
			problems := r.SuperviseOnce(logger)
			if len(problems) > 0 {
				fails++
				logger.Warn("supervisor found problems", "count", len(problems), "detail", strings.Join(problems, "；"))
			} else {
				fails = 0
			}
			state, detail := "ready", "巡检正常"
			if fails >= 3 {
				state, detail = "error", strings.Join(problems, "；")
			} else if len(problems) > 0 {
				state, detail = "ready", strings.Join(problems, "；")
			}
			r.writeHealth(healthRuntime, state, detail)
			if fails >= 3 {
				r.setErr(detail)
			}
		}
	}()
}

// SuperviseOnce runs one check and returns what needs attention. It is exported
// for tests and for a CLI `check` that wants the in-process view.
func (r *Runtime) SuperviseOnce(logger *slog.Logger) []string {
	if logger == nil {
		logger = slog.Default()
	}
	var problems []string
	defer func() {
		if p := recover(); p != nil {
			problems = append(problems, fmt.Sprintf("巡检崩溃: %v", p))
			logger.Error("supervisor check panicked", "panic", p, "stack", string(debug.Stack()))
		}
	}()

	r.mu.Lock()
	st := r.settingsStore
	q := r.queue
	r.mu.Unlock()
	if st == nil {
		return nil
	}
	if err := st.QueryRow(`SELECT 1`).Scan(new(int)); err != nil {
		problems = append(problems, "数据库不可写: "+err.Error())
	}
	if q != nil {
		// Kick is cheap and idempotent: it wakes the dispatcher, which is exactly
		// the recovery for a launch row that became due while nothing was running.
		q.Kick()
		if s, err := q.Status(engine.EffectLaunch); err == nil && s.Failing {
			problems = append(problems, fmt.Sprintf("launch 队列连续失败 %d 次: %s", s.Attempts, s.Error))
		}
	}
	if stuck := r.stuckAssignment(r.clock()); stuck != "" {
		problems = append(problems, stuck)
	}
	if len(problems) > 0 {
		logger.Warn("runtime check", "problems", problems)
	}
	return problems
}

// stuckAssignment names the oldest Assignment that has been "running" for longer
// than any legitimate task. It does not end it: only the executor's own context
// can do that safely, and the point of the check is to make the situation visible.
func (r *Runtime) stuckAssignment(now time.Time) string {
	var (
		number int
		age    time.Duration
		found  bool
	)
	for _, t := range r.ticketSnapshots() {
		if t.Ended != nil {
			continue
		}
		as := t.LastAssignment()
		if as.State != domain.AssignRunning || as.StartedAt.IsZero() {
			continue
		}
		a := now.Sub(as.StartedAt)
		if a < stuckAfter {
			continue
		}
		if !found || a > age {
			number, age, found = t.Number, a, true
		}
	}
	if !found {
		return ""
	}
	return fmt.Sprintf("工单 #%d 的 Assignment 已运行 %s（可能卡住，见执行页）",
		number, humanDuration(age))
}
