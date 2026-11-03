package ui

import (
	"testing"
	"time"

	mui "github.com/egoist/mygo/ui"

	"github.com/zeokeel/super-agent/internal/view"
	"github.com/zeokeel/super-agent/internal/view/viewtest"
)

// The settings page always shows its five groups, even when the runtime has
// published only some of them: a missing group is a projection gap, not a group
// the operator does not need.
func TestSettingsPageShowsTheFiveGroups(t *testing.T) {
	a := pageApp(t)
	tt := mui.NewTester(a.view, 1440, 900)
	click(t, tt, "设置")
	wantTexts(t, tt, "连接", "评估", "执行", "提示词", "部署")
}

// 部署 is the operator's console: the CLI's fixed paths and exit codes, and the
// live health rows — no editable field, so no save bar.
func TestDeploymentGroupIsTheOperatorsConsole(t *testing.T) {
	a := pageApp(t)
	tt := mui.NewTester(a.view, 1440, 900)
	click(t, tt, "设置")
	a.settingsGroup = view.SettingsDeploy
	tt.Frame()

	wantTexts(t, tt,
		"运维面（命令行）",
		"<stateDir>/logs/zeokeel.log",
		"zeokeel backup",
		"zeokeel archive",
		"0 健康 · 1 可重启修复 · 2 组件错误 · 3 等待初次设置",
		"健康",
		"GitHub Token",
	)
	if tt.HasText("处未保存更改") {
		t.Fatalf("部署 must not offer a save bar; texts: %q", tt.Texts())
	}
}

// A group whose form has not been projected says what it is waiting for, by
// name, instead of showing an empty form that reads as "nothing to set".
func TestSettingsGroupSaysWhatItWaitsFor(t *testing.T) {
	a := pageApp(t)
	tt := mui.NewTester(a.view, 1440, 900)
	click(t, tt, "设置")
	a.settingsGroup = view.SettingsEval
	tt.Frame()
	wantTexts(t, tt, "还没有数据：",
		"规则列表（id / 是否启用 / instruction / proactive）", "受理档位 accept mode")
}

// A runtime that publishes only one group still gets the other four: the page
// synthesizes them from view.RequiredSettingsGroups and marks them as gaps.
func TestMissingSettingsGroupAppearsAsProjectionGap(t *testing.T) {
	now := time.Now()
	snap := view.NewSnapshot()
	snap.PublishOverview(viewtest.DemoOverview(now))
	snap.PublishSettings(&view.SettingsVM{
		Groups: []view.SettingsGroup{{
			ID: view.SettingsExecution, Label: "执行",
			Fields: []view.SettingField{{Key: "concurrency", Label: "并发执行器", Value: "2", Kind: view.SettingNumber}},
		}},
		Source: "mygo.json", Revision: 3, UpdatedAt: now,
	})
	a := newApp(snap, &fakeCmd{})
	a.now = now
	tt := mui.NewTester(a.view, 1440, 900)
	click(t, tt, "设置")

	// The rail carries all five, and 评估 says what it is missing.
	wantTexts(t, tt, "连接", "评估", "执行", "提示词", "部署")
	a.settingsGroup = view.SettingsEval
	tt.Frame()
	wantTexts(t, tt, "还没有数据：", "规则列表（id / 是否启用 / instruction / proactive）")

	// The published group keeps its own form: no fake fields were invented.
	a.settingsGroup = view.SettingsExecution
	tt.Frame()
	wantTexts(t, tt, "并发执行器", "2")
}

// The pause switch follows the projection, not the click: with the channel wired
// the command is sent, and the chip changes only when the runtime says so.
func TestPauseControlSendsAndFollowsTheProjection(t *testing.T) {
	actor := &fakeActor{}
	a := pageApp(t)
	a.cmd = actor
	tt := mui.NewTester(a.view, 1440, 900)
	click(t, tt, "设置")
	a.settingsGroup = view.SettingsExecution
	tt.Frame()
	wantTexts(t, tt, "自动派工", "状态未知")
	// Without the projected state both directions are offered: neither guesses.
	wantTexts(t, tt, "暂停状态未投影")

	// The runtime publishes the state: one toggle now follows it.
	ov := viewtest.DemoOverview(a.now)
	ov.Runtime.Paused, ov.Runtime.PausedKnown = false, true
	a.snap.PublishOverview(ov)
	tt.Frame()
	wantTexts(t, tt, "运行中")

	click(t, tt, "暂停")
	if len(actor.paused) != 1 || actor.paused[0] != true {
		t.Fatalf("paused %v, want [true]", actor.paused)
	}
	// No optimistic update: still running until the projection moves.
	wantTexts(t, tt, "运行中")

	ov = viewtest.DemoOverview(a.now)
	ov.Runtime.Paused, ov.Runtime.PausedKnown = true, true
	a.snap.PublishOverview(ov)
	tt.Frame()
	wantTexts(t, tt, "已暂停", "恢复")
	click(t, tt, "恢复")
	if len(actor.paused) != 2 || actor.paused[1] != false {
		t.Fatalf("paused %v, want [true false]", actor.paused)
	}
}

// A runtime without PauseExecution gets a disabled switch and the reason; the
// click sends nothing and silently does nothing.
func TestPauseControlUnwired(t *testing.T) {
	a := pageApp(t) // fakeCmd has no ExecutionPauser
	tt := mui.NewTester(a.view, 1440, 900)
	click(t, tt, "设置")
	a.settingsGroup = view.SettingsExecution
	tt.Frame()
	wantTexts(t, tt, "暂停通道未接线：运行时未实现 ExecutionPauser")
	if err := tt.Click("暂停"); err != nil {
		t.Fatalf("click 暂停: %v", err)
	}
	if a.pauseErr != "" {
		t.Fatalf("a disabled pause reported %q", a.pauseErr)
	}
}
