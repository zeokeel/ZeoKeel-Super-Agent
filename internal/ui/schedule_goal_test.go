package ui

import (
	"testing"

	mui "github.com/egoist/mygo/ui"

	"github.com/zeokeel/super-agent/internal/view"
	"github.com/zeokeel/super-agent/internal/view/viewtest"
)

// 触发 lists the timed triggers read-only: what fires, where, when next, and what
// the last attempt did. The one guide row is the way to the configuration, which
// belongs to 设置 (design §6.3).
func TestTriggerPageListsSchedulesWithNextFireAndLastResult(t *testing.T) {
	a := pageApp(t)
	tt := mui.NewTester(a.view, 1440, 900)
	click(t, tt, "触发")

	wantTexts(t, tt,
		"定时", "由 agent 创建",
		"sch-daily-ci", "每天 09:00 跑一次 CI 巡检", "每天 09:00", "下次", "已触发",
		"sch-usage", "每 2 小时", "已跳过", "上一个定时还在执行，本次跳过",
		"sch-backup", "每天 02:00 备份状态库", "已停用",
		"在设置 › 执行里管理",
	)

	// The schedule is configuration: editing it is one navigation away, not an
	// inline form on a read-only page.
	click(t, tt, "在设置 › 执行里管理")
	if a.sec != view.SecSettings || a.settingsGroup != view.SettingsExecution {
		t.Fatalf("guide row went to %q/%q, want settings/execution", a.sec, a.settingsGroup)
	}
}

// A published but empty schedule list is a fact ("none yet"), not a blank section;
// a missing projection is a different fact and says it waits.
func TestScheduleSectionEmptyStateAndMissingSnapshot(t *testing.T) {
	a := pageApp(t)
	a.snap.PublishSchedules(&view.ScheduleVM{UpdatedAt: a.now})
	tt := mui.NewTester(a.view, 1440, 900)
	click(t, tt, "触发")
	wantTexts(t, tt, "还没有定时任务，可以在消息里让它自己建")
	if tt.HasText("sch-daily-ci") {
		t.Fatalf("an empty schedule list still drew rows; texts: %q", tt.Texts())
	}

	// No projection at all: the section says it waits instead of pretending none
	// exist. The sidebar still works with an empty snapshot.
	empty := newApp(view.NewSnapshot(), &fakeCmd{})
	empty.now = a.now
	tt = mui.NewTester(empty.view, 1440, 900)
	click(t, tt, "触发")
	wantTexts(t, tt, "等待运行时发布定时快照…", "等待运行时发布目标快照…")
}

// 总览 shows at most two active goals, and only while something is active. The whole
// row jumps to the complete 触发 list, where the goal is highlighted.
func TestOverviewGoalStripShowsAtMostTwoActiveGoalsAndJumps(t *testing.T) {
	a := pageApp(t)
	tt := mui.NewTester(a.view, 1440, 900)
	if a.sec != view.SecOverview {
		t.Fatalf("home section %q, want overview", a.sec)
	}

	wantTexts(t, tt,
		// The chip counts the two rows the strip draws; the hint carries the
		// honest total of active goals.
		"目标", "2", "共 3 个目标进行中 · 点一条看它在做什么",
		"把登录超时配置化并合入主干", "第 3 / 12 轮", "进行中",
		"补齐 auth 包的测试覆盖到 80%", "第 1 / 5 轮",
	)
	// The cap is two, and only active goals show: neither the third active goal
	// nor paused or finished ones belong on the dashboard.
	for _, notWanted := range []string{
		"调研 MCP 工具接入的可行方案",
		"把 acme 用量报表迁移到新模板",
		"清理 2026-10 的过期通知",
	} {
		if tt.HasText(notWanted) {
			t.Fatalf("%q must not show on 总览; texts: %q", notWanted, tt.Texts())
		}
	}

	// The row is navigation: it opens 触发 and locates the goal in the full list.
	click(t, tt, "把登录超时配置化并合入主干")
	if a.sec != view.SecTrigger {
		t.Fatalf("section %q, want trigger", a.sec)
	}
	if a.goalFocus != "goal-login-timeout" {
		t.Fatalf("focused goal %q, want goal-login-timeout", a.goalFocus)
	}
	wantTexts(t, tt,
		"定位", "调研 MCP 工具接入的可行方案", "第 2 / 3 轮",
		"已暂停", "已完成",
	)
	// A highlight belongs to the page that was jumped to.
	click(t, tt, "总览")
	if a.goalFocus != "" {
		t.Fatalf("navigation kept the goal focus %q", a.goalFocus)
	}
}

// With nothing active the strip is absent: 默认安静, no empty banner. The paused
// goal is still listed on 触发, where the full list lives.
func TestOverviewGoalStripHiddenWithoutActiveGoals(t *testing.T) {
	a := pageApp(t)
	goals := viewtest.DemoGoals(a.now)
	kept := make([]view.GoalRow, 0, 2)
	for _, g := range goals.Rows {
		if !g.IsActive() {
			kept = append(kept, g)
		}
	}
	a.snap.PublishGoals(&view.GoalVM{Rows: kept, UpdatedAt: a.now})
	tt := mui.NewTester(a.view, 1440, 900)

	if a.snap.Goals().ActiveCount() != 0 {
		t.Fatalf("fixture still has %d active goals", a.snap.Goals().ActiveCount())
	}
	if tt.HasText("目标") || tt.HasText("把登录超时配置化并合入主干") {
		t.Fatalf("the strip must not show without an active goal; texts: %q", tt.Texts())
	}

	click(t, tt, "触发")
	wantTexts(t, tt, "把 acme 用量报表迁移到新模板", "已暂停", "清理 2026-10 的过期通知", "已完成")
}
