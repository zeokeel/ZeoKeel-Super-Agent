package view

// 定时：the timed triggers that make the agent start work by itself. The panel
// shows them read-only on 触发 (what makes it start) — configuration actions stay
// in 设置 (design §6.3: 动作只有一个地方). The rows answer one question per column:
// what fires, where, when next, and what the last attempt did.

import "time"

// ScheduleRow is one timed trigger as the 触发 page draws it.
type ScheduleRow struct {
	ID string
	// Task is what it fires, in a person's words. Empty shows the ID instead, so
	// a schedule the runtime could not describe is still visible.
	Task      string
	Workspace string
	// Spec is how the schedule reads: "每天 09:30", "每 2 小时",
	// "cron 0 9 * * 1-5". It is rendered by the core so the panel never parses a
	// timer spec (design §6.2: the projection carries the words, not the code).
	Spec    string
	Enabled bool
	// NextAt is the next occurrence. Zero when nothing more is due (a one-shot
	// that fired) or while the schedule is disabled.
	NextAt time.Time
	// LastFiredAt and LastStatus say what the last attempt did: "fired",
	// "skipped", or "" before the first one. LastSkipReason explains a skip:
	// a skipped fire is not an error, and must not look like one.
	LastFiredAt    time.Time
	LastStatus     string
	LastSkipReason string
	// Running is true while a fire is claimed and not completed.
	Running bool
}

// LastStatusLabel is how the last attempt reads in Chinese.
func (r ScheduleRow) LastStatusLabel() string {
	switch r.LastStatus {
	case "fired":
		return "已触发"
	case "skipped":
		return "已跳过"
	}
	return "还没触发过"
}

// EnabledLabel is how the on/off state reads in Chinese.
func (r ScheduleRow) EnabledLabel() string {
	if r.Enabled {
		return "已启用"
	}
	return "已停用"
}

// ScheduleVM is everything the 触发 page's 定时 section draws.
type ScheduleVM struct {
	// Rows are the schedules in the order the core publishes them, which is the
	// order it lists them in (oldest first): the page never sorts.
	Rows      []ScheduleRow
	UpdatedAt time.Time
}

// Empty reports whether no schedule exists.
func (vm *ScheduleVM) Empty() bool { return vm == nil || len(vm.Rows) == 0 }

// EnabledCount counts the schedules that can fire; a disabled schedule is still
// listed, because it is still configuration a person made.
func (vm *ScheduleVM) EnabledCount() int {
	if vm == nil {
		return 0
	}
	n := 0
	for _, r := range vm.Rows {
		if r.Enabled {
			n++
		}
	}
	return n
}

// RunningCount counts the fires claimed and not completed.
func (vm *ScheduleVM) RunningCount() int {
	if vm == nil {
		return 0
	}
	n := 0
	for _, r := range vm.Rows {
		if r.Running {
			n++
		}
	}
	return n
}
