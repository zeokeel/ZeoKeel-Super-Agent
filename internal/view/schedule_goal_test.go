package view_test

import (
	"testing"
	"time"

	// The exported view API is dot-imported, and the sample projections come from
	// viewtest: these tests read like the package's own, as they did before the
	// fixtures moved out of the production package.
	. "github.com/zeokeel/super-agent/internal/view"
	"github.com/zeokeel/super-agent/internal/view/viewtest"
)

// The 定时 section tells the three states apart that a person must act on: a
// healthy schedule, one skipped for a reason, and one switched off. It never
// hides a schedule: off is still configuration.
func TestDemoSchedulesShowEveryState(t *testing.T) {
	now := time.Now()
	vm := viewtest.DemoSchedules(now)
	if vm.Empty() {
		t.Fatal("the demo has no schedules")
	}
	if got, want := len(vm.Rows), 3; got != want {
		t.Fatalf("%d rows, want %d", got, want)
	}
	if got := vm.EnabledCount(); got != 2 {
		t.Errorf("enabled %d, want 2", got)
	}
	if got := vm.RunningCount(); got != 0 {
		t.Errorf("running %d, want 0", got)
	}

	seen := map[string]bool{}
	for _, r := range vm.Rows {
		if r.ID == "" || r.Task == "" || r.Spec == "" {
			t.Errorf("row %+v is missing its identity, task or spec", r)
		}
		if seen[r.ID] {
			t.Errorf("schedule %q appears twice", r.ID)
		}
		seen[r.ID] = true
		if r.Enabled && r.NextAt.IsZero() {
			t.Errorf("enabled schedule %q has no next occurrence", r.ID)
		}
		if !r.Enabled && !r.NextAt.IsZero() {
			t.Errorf("disabled schedule %q claims a next occurrence", r.ID)
		}
		if r.LastFiredAt.IsZero() {
			t.Errorf("schedule %q fired nothing in the demo; last result is untested", r.ID)
		}
	}

	// One skipped: the reason is the whole point, and it is not an error.
	var skipped *ScheduleRow
	for i := range vm.Rows {
		if vm.Rows[i].LastStatus == "skipped" {
			skipped = &vm.Rows[i]
		}
	}
	if skipped == nil {
		t.Fatal("no skipped schedule; the reason line would go untested")
	}
	if skipped.LastStatusLabel() != "已跳过" || skipped.LastSkipReason == "" {
		t.Errorf("skipped schedule reads %q / %q", skipped.LastStatusLabel(), skipped.LastSkipReason)
	}
	if got := (ScheduleRow{LastStatus: "fired"}).LastStatusLabel(); got != "已触发" {
		t.Errorf("fired label %q", got)
	}
	if got := (ScheduleRow{}).LastStatusLabel(); got != "还没触发过" {
		t.Errorf("never-fired label %q", got)
	}
	if got := (ScheduleRow{Enabled: true}).EnabledLabel(); got != "已启用" {
		t.Errorf("enabled label %q", got)
	}
	if got := (ScheduleRow{}).EnabledLabel(); got != "已停用" {
		t.Errorf("disabled label %q", got)
	}
}

// The 目标 list keeps the core's order (recent first), and the round label says
// which round is in progress: a goal with 2 finished rounds is on 3.
func TestDemoGoalsProgressAndStateLabels(t *testing.T) {
	vm := viewtest.DemoGoals(time.Now())
	if vm.Empty() {
		t.Fatal("the demo has no goals")
	}
	if got, want := len(vm.Rows), 5; got != want {
		t.Fatalf("%d goals, want %d", got, want)
	}
	for i := 1; i < len(vm.Rows); i++ {
		if vm.Rows[i].UpdatedAt.After(vm.Rows[i-1].UpdatedAt) {
			t.Errorf("goal %q is newer than the one above it", vm.Rows[i].ID)
		}
	}
	if got := vm.ActiveCount(); got != 3 {
		t.Errorf("active %d, want 3", got)
	}
	// 总览 shows at most two, in the same order as the full list.
	top := vm.Active(2)
	if len(top) != 2 {
		t.Fatalf("active(2) returned %d", len(top))
	}
	if top[0].ID != vm.Rows[0].ID || top[1].ID != vm.Rows[1].ID {
		t.Errorf("active(2) order %v, want the first two active rows", []string{top[0].ID, top[1].ID})
	}
	if got := len(vm.Active(0)); got != 3 {
		t.Errorf("active(0) returned %d, want every active goal", got)
	}
	if vm.Rows[2].IsActive() != true {
		t.Error("the third goal is active in the demo, for the cap test to catch a missing limit")
	}

	cases := []struct {
		row  GoalRow
		want string
	}{
		{GoalRow{State: GoalActive, Rounds: 2, MaxRounds: 12}, "第 3 / 12 轮"},
		{GoalRow{State: GoalActive, Rounds: 0, MaxRounds: 5}, "第 1 / 5 轮"},
		{GoalRow{State: GoalPaused, Rounds: 3, MaxRounds: 8}, "第 3 / 8 轮"},
		{GoalRow{State: GoalCompleted, Rounds: 4, MaxRounds: 4}, "第 4 / 4 轮"},
		{GoalRow{State: GoalActive, Rounds: 1, MaxRounds: 0}, "第 2 轮"},
		{GoalRow{State: GoalActive, Rounds: 5, MaxRounds: 3}, "第 3 / 3 轮"},
	}
	for _, c := range cases {
		if got := c.row.ProgressLabel(); got != c.want {
			t.Errorf("%+v reads %q, want %q", c.row, got, c.want)
		}
	}

	labels := map[GoalState]string{
		GoalActive: "进行中", GoalPaused: "已暂停",
		GoalCompleted: "已完成", GoalBlocked: "受阻",
	}
	for state, want := range labels {
		if got := state.Label(); got != want {
			t.Errorf("state %q reads %q, want %q", state, got, want)
		}
	}
	if got := GoalState("new-state").Label(); got != "new-state" {
		t.Errorf("an unknown state must read as itself, not disappear: %q", got)
	}
}
