package view

// 目标：long-running objectives the agent works toward across rounds. The panel
// shows them read-only: a 目标 strip on 总览 when one is active, and the full list
// on 触发 (both are "what makes it start work"). Actions on goals are work-item
// actions and would belong to 总览 if they were added; this projection only
// describes them (design §6.3).

import (
	"fmt"
	"time"
)

// GoalState is a goal's lifecycle as the page shows it.
type GoalState string

const (
	GoalActive    GoalState = "active"
	GoalPaused    GoalState = "paused"
	GoalCompleted GoalState = "completed"
	GoalBlocked   GoalState = "blocked"
)

// Label is how the state reads in Chinese. An unknown state is shown as itself,
// never hidden: a projection from a newer core must not make the goal disappear.
func (s GoalState) Label() string {
	switch s {
	case GoalActive:
		return "进行中"
	case GoalPaused:
		return "已暂停"
	case GoalCompleted:
		return "已完成"
	case GoalBlocked:
		return "受阻"
	}
	return string(s)
}

// GoalRow is one long-running objective as the window draws it.
type GoalRow struct {
	ID        string
	Workspace string
	Objective string
	State     GoalState
	// Rounds is how many rounds have finished; MaxRounds is the hard budget the
	// goal was created with. The page shows the round in progress: Rounds+1 while
	// the goal is active, Rounds otherwise.
	Rounds    int
	MaxRounds int
	// Revision is the goal's optimistic-concurrency revision. It is not shown, but
	// a future action needs it, and publishing it keeps the projection complete.
	Revision uint64
	// BlockedReason says why a blocked goal stopped waiting for progress.
	BlockedReason string
	UpdatedAt     time.Time
}

// IsActive reports whether the goal is still being worked toward.
func (r GoalRow) IsActive() bool { return r.State == GoalActive }

// ProgressLabel is the round progress in the words the strip uses: "第 3 / 12 轮".
// A goal without a budget says only which round it is in.
func (r GoalRow) ProgressLabel() string {
	round := r.Rounds
	if r.IsActive() {
		round = r.Rounds + 1
	}
	if round < 1 {
		round = 1
	}
	if r.MaxRounds > 0 && round > r.MaxRounds {
		round = r.MaxRounds
	}
	if r.MaxRounds <= 0 {
		return fmt.Sprintf("第 %d 轮", round)
	}
	return fmt.Sprintf("第 %d / %d 轮", round, r.MaxRounds)
}

// GoalVM is the goals the window shows.
type GoalVM struct {
	// Rows are the goals most recently updated first, as the core lists them.
	Rows      []GoalRow
	UpdatedAt time.Time
}

// Empty reports whether no goal exists.
func (vm *GoalVM) Empty() bool { return vm == nil || len(vm.Rows) == 0 }

// ActiveCount counts the goals still being worked toward.
func (vm *GoalVM) ActiveCount() int {
	if vm == nil {
		return 0
	}
	n := 0
	for _, r := range vm.Rows {
		if r.IsActive() {
			n++
		}
	}
	return n
}

// Active returns the active goals, most recently updated first, at most limit of
// them (all of them when limit is 0). 总览 shows "最多 2 条" through it, so the
// strip's length and its count cannot disagree.
func (vm *GoalVM) Active(limit int) []GoalRow {
	if vm == nil {
		return nil
	}
	out := make([]GoalRow, 0, len(vm.Rows))
	for _, r := range vm.Rows {
		if !r.IsActive() {
			continue
		}
		if limit > 0 && len(out) == limit {
			break
		}
		out = append(out, r)
	}
	return out
}
