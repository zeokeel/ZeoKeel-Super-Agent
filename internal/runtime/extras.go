package runtime

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/zeokeel/super-agent/internal/domain"
	"github.com/zeokeel/super-agent/internal/goal"
	"github.com/zeokeel/super-agent/internal/scan"
	"github.com/zeokeel/super-agent/internal/schedule"
	"github.com/zeokeel/super-agent/internal/store"
	"github.com/zeokeel/super-agent/internal/view"
)

// EnableSchedules attaches the timer manager, which also unlocks the Worker's
// schedule_task tool. Nil is fine: the tool simply is not offered.
func (r *Runtime) EnableSchedules(m *schedule.Manager) {
	r.mu.Lock()
	r.sched = m
	r.mu.Unlock()
}

// EnableGoals attaches the goal service.
func (r *Runtime) EnableGoals(s *goal.Service) {
	r.mu.Lock()
	r.goals = s
	r.mu.Unlock()
}

// ScheduleTask creates a timer from what a person said in words. It is the single
// implementation behind the Worker's schedule_task tool, so the panel and the
// agent cannot drift (design §5.10).
//
// spec examples: "每天 09:30" / "每 2 小时" / "每周一 10:00" / "cron 0 9 * * 1-5".
func (r *Runtime) ScheduleTask(id, task, spec string, workspace domain.WorkspaceID) (schedule.Definition, error) {
	r.mu.Lock()
	m := r.sched
	r.mu.Unlock()
	if m == nil {
		return schedule.Definition{}, fmt.Errorf("runtime: 定时器未启用")
	}
	if strings.TrimSpace(id) == "" {
		return schedule.Definition{}, fmt.Errorf("runtime: 定时任务需要一个 id")
	}
	if strings.TrimSpace(task) == "" {
		return schedule.Definition{}, fmt.Errorf("runtime: 定时任务需要一个任务描述")
	}
	parsed, err := parseWhen(spec)
	if err != nil {
		return schedule.Definition{}, err
	}
	return m.Put(schedule.Definition{
		ID: id, Task: task, WorkspaceID: workspace, Spec: parsed, Enabled: true,
	})
}

// parseWhen turns a human phrase into a schedule spec. It is intentionally small:
// a wrong guess here would fire work at the wrong time, so an unrecognised phrase
// is an error, never a default.
func parseWhen(spec string) (schedule.Spec, error) {
	s := strings.TrimSpace(strings.ToLower(spec))
	switch {
	case strings.HasPrefix(s, "cron "):
		return schedule.Spec{Kind: schedule.Cron, Cron: strings.TrimSpace(s[5:])}, nil
	case strings.HasPrefix(s, "每天") || strings.HasPrefix(s, "daily"):
		rest := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(s, "每天"), "daily"))
		h, m, err := parseClock(rest)
		if err != nil {
			return schedule.Spec{}, err
		}
		return schedule.Spec{Kind: schedule.Daily, Hour: h, Minute: m}, nil
	case strings.HasPrefix(s, "每 ") || strings.HasPrefix(s, "每") || strings.HasPrefix(s, "every "):
		rest := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(s, "every "), "每"))
		d, err := parseEvery(rest)
		if err != nil {
			return schedule.Spec{}, err
		}
		return schedule.Spec{Kind: schedule.Every, Every: d}, nil
	}
	return schedule.Spec{}, fmt.Errorf("runtime: 看不懂这个时间：%q（支持 每天 09:30 / 每 2 小时 / cron …）", spec)
}

func parseClock(rest string) (int, int, error) {
	parts := strings.SplitN(rest, ":", 2)
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("runtime: 时间要写成 HH:MM，收到 %q", rest)
	}
	h, err1 := strconv.Atoi(strings.TrimSpace(parts[0]))
	m, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err1 != nil || err2 != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, 0, fmt.Errorf("runtime: 时间要写成 HH:MM，收到 %q", rest)
	}
	return h, m, nil
}

func parseEvery(rest string) (time.Duration, error) {
	rest = strings.ReplaceAll(rest, " ", "")
	var n int
	var unit time.Duration
	if _, err := fmt.Sscanf(rest, "%d", &n); err != nil {
		return 0, fmt.Errorf("runtime: 间隔要写成 数字+单位，收到 %q", rest)
	}
	num := strconv.Itoa(n)
	tail := strings.TrimPrefix(rest, num)
	switch {
	case strings.HasPrefix(tail, "分钟") || strings.HasPrefix(tail, "分") || strings.HasPrefix(tail, "min"):
		unit = time.Minute
	case strings.HasPrefix(tail, "小时") || strings.HasPrefix(tail, "时") || strings.HasPrefix(tail, "h"):
		unit = time.Hour
	case strings.HasPrefix(tail, "天") || strings.HasPrefix(tail, "d"):
		unit = 24 * time.Hour
	default:
		return 0, fmt.Errorf("runtime: 不认识时间单位 %q", tail)
	}
	if n <= 0 {
		return 0, fmt.Errorf("runtime: 间隔必须大于 0")
	}
	return time.Duration(n) * unit, nil
}

// CreateGoal records a durable objective for a workspace.
func (r *Runtime) CreateGoal(workspace domain.WorkspaceID, objective string, maxRounds int) (goal.Goal, error) {
	r.mu.Lock()
	s := r.goals
	r.mu.Unlock()
	if s == nil {
		return goal.Goal{}, fmt.Errorf("runtime: 目标服务未启用")
	}
	return s.Create(workspace, objective, maxRounds)
}

// Goals lists the objectives this install is working toward.
func (r *Runtime) Goals() ([]goal.Goal, error) {
	r.mu.Lock()
	s := r.goals
	r.mu.Unlock()
	if s == nil {
		return nil, nil
	}
	// An empty workspace lists every goal on this install.
	return s.List("")
}

// schedulesVM projects the timers. The panel never parses a timer spec, so the
// human label is produced here (design §6.2).
func (r *Runtime) schedulesVM(now time.Time) *view.ScheduleVM {
	r.mu.Lock()
	m := r.sched
	r.mu.Unlock()
	vm := &view.ScheduleVM{UpdatedAt: now}
	if m == nil {
		return vm
	}
	list, err := m.List()
	if err != nil {
		return vm
	}
	for _, d := range list {
		vm.Rows = append(vm.Rows, view.ScheduleRow{
			ID: d.ID, Task: d.Task, Workspace: string(d.WorkspaceID),
			Spec: d.Spec.Label(), Enabled: d.Enabled,
			NextAt: d.NextAt, LastFiredAt: d.LastFiredAt,
			LastStatus: d.LastStatus, LastSkipReason: d.LastSkipReason,
			Running: d.RunningFire != "",
		})
	}
	return vm
}

// goalsVM projects the long-running objectives.
func (r *Runtime) goalsVM(now time.Time) *view.GoalVM {
	r.mu.Lock()
	s := r.goals
	r.mu.Unlock()
	vm := &view.GoalVM{UpdatedAt: now}
	if s == nil {
		return vm
	}
	list, err := s.List("")
	if err != nil {
		return vm
	}
	for _, g := range list {
		row := view.GoalRow{
			ID: g.ID, Workspace: string(g.Workspace), Objective: g.Objective,
			State: view.GoalState(g.State), Rounds: len(g.Rounds),
			MaxRounds: g.MaxRounds, Revision: g.Revision,
			UpdatedAt: g.UpdatedAt,
		}
		if g.State == goalBlocked(g.State) {
			row.BlockedReason = g.BlockedCondition
		}
		vm.Rows = append(vm.Rows, row)
	}
	return vm
}

// goalBlocked is a tiny helper so the comparison reads in the caller.
func goalBlocked(s goal.State) goal.State { return goal.Blocked }

// RefreshWorkspaces scans every workspace and records the observed facts. It runs
// at startup and on demand: the briefs the Router reads are only as good as the
// last scan (design §4.6).
func (r *Runtime) RefreshWorkspaces() {
	for _, w := range r.eng.Workspaces() {
		if w.Root == "" {
			continue
		}
		facts := scan.Scan(expandHome(w.Root))
		r.eng.UpdateFacts(w.ID, facts)
		if st := r.durStore(); st != nil {
			// Persist so the panel and the next start see the same facts.
			w.Brief.Facts = facts
			_ = st.PutWorkspace(w)
		}
	}
	r.Publish()
}

// RefreshWorkspace scans one workspace.
func (r *Runtime) RefreshWorkspace(id domain.WorkspaceID) {
	w, ok := r.eng.Workspace(id)
	if !ok || w.Root == "" {
		return
	}
	facts := scan.Scan(expandHome(w.Root))
	r.eng.UpdateFacts(id, facts)
	if st := r.durStore(); st != nil {
		w.Brief.Facts = facts
		_ = st.PutWorkspace(w)
	}
	r.Publish()
}

// durStore is the store behind a durable runtime, or nil in memory mode.
func (r *Runtime) durStore() *store.Store {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.settingsStore
}

// expandHome resolves a leading ~ so a workspace registered by hand still scans.
func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	return p
}
