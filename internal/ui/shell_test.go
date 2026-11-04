package ui

import (
	"os"
	"testing"

	mui "github.com/egoist/mygo/ui"

	"github.com/zeokeel/super-agent/internal/view"
	"github.com/zeokeel/super-agent/internal/view/viewtest"
)

// TestMain keeps the old page-internals tests on the panel (their subject) while
// the shell tests opt into the host views explicitly.
func TestMain(m *testing.M) {
	initialMode = "panel"
	os.Exit(m.Run())
}

// hostApp is the DSH shell on the host home.
func hostApp(t *testing.T) (*App, *mui.Tester) {
	t.Helper()
	a := pageApp(t)
	a.mode = "home"
	a.snap.PublishWorkspaces(viewtest.DemoWorkspaces(a.now))
	return a, mui.NewTester(a.view, 1440, 900)
}

// The shell is a 280px host sidebar, a home composer, and the 超级Agent as a
// separate panel with native tabs.
func TestShellHomeAndSidebar(t *testing.T) {
	a, tt := hostApp(t)
	for _, want := range []string{"ZeoKeel", "＋ 新对话", "超级Agent", "自动化任务", "项目", "设置", "更多", "开始做点什么"} {
		if !tt.HasText(want) {
			t.Fatalf("home shell missing %q; texts: %q", want, tt.Texts())
		}
	}
	// Old shell chrome is gone.
	for _, gone := range []string{"监控", "收件箱", "诊断", "状态栏"} {
		if tt.HasText(gone) {
			t.Fatalf("legacy chrome %q is still drawn", gone)
		}
	}
	if tt.HasText("超级Agent 会回答") || tt.HasText("发给 超级Agent") {
		t.Fatalf("home copy still names the panel as a chat partner; texts: %q", tt.Texts())
	}
	_ = a
}

// Sending from the home composer opens the session view.
func TestShellSendOpensSession(t *testing.T) {
	a, tt := hostApp(t)
	a.draft = "把登录超时改成可配置"
	click(t, tt, "发送")
	tt.Frame()
	if a.mode != "session" {
		t.Fatalf("mode %q, want session after sending", a.mode)
	}
	if !tt.HasText("新对话") && !tt.HasText("把登录超时改成可配置") {
		t.Fatalf("session view missing its header/message; texts: %q", tt.Texts())
	}
	// A session never mentions the task-intake panel or its product name.
	for _, gone := range []string{"打开面板", "超级Agent：", "超级Agent 会回答"} {
		if tt.HasText(gone) {
			t.Fatalf("session still carries %q", gone)
		}
	}
	if !tt.HasText("Agent") {
		t.Fatal("the assistant has no neutral speaker label")
	}
}

// 超级Agent is a panel with native tabs; each tab reuses the page content.
func TestShellSuperAgentPanelTabs(t *testing.T) {
	a, tt := hostApp(t)
	click(t, tt, "超级Agent")
	tt.Frame()
	if a.mode != "panel" {
		t.Fatalf("mode %q, want panel", a.mode)
	}
	wantTexts(t, tt, "超级Agent · 接收任务")
	if tt.HasText("超级Agent：") {
		t.Fatal("the panel still labels chat lines as 超级Agent")
	}
	for _, tab := range []string{"总览", "来源", "触发", "评估", "执行", "设置"} {
		if !tt.HasText(tab) {
			t.Fatalf("panel missing tab %q; texts: %q", tab, tt.Texts())
		}
	}
	click(t, tt, "来源")
	tt.Frame()
	if a.panelTab != 1 || a.sec != view.SecSource {
		t.Fatalf("tab state %d/%q, want source", a.panelTab, a.sec)
	}
	click(t, tt, "执行")
	tt.Frame()
	if a.panelTab != 4 || a.sec != view.SecExecution {
		t.Fatalf("tab state %d/%q, want execution", a.panelTab, a.sec)
	}
}

// 设置 is the host settings view; the panel is reachable from it and back.
func TestShellSettingsView(t *testing.T) {
	a, tt := hostApp(t)
	click(t, tt, "设置")
	tt.Frame()
	if a.mode != "settings" {
		t.Fatalf("mode %q, want settings", a.mode)
	}
	wantTexts(t, tt, "设置", "返回首页")
	click(t, tt, "返回首页")
	tt.Frame()
	if a.mode != "home" {
		t.Fatalf("mode %q after back, want home", a.mode)
	}
}

// Selecting a project shows its sessions in the sidebar and its page in the main
// area; the composer targets that project.
func TestShellProjectAndSession(t *testing.T) {
	now := snapshotWithWorkspaces(t, pageApp(t))
	_ = now
	a, tt := hostApp(t)
	click(t, tt, "项目 acme/webapp")
	tt.Frame()
	if a.workspaceSel != "ws_webapp" || a.mode != "project" {
		t.Fatalf("project selection %q mode %q", a.workspaceSel, a.mode)
	}
	if a.targetWS != "ws_webapp" {
		t.Fatalf("composer target %q, want the selected project", a.targetWS)
	}
	if !tt.HasText("会话 修改登录超时配置") {
		t.Fatalf("the sidebar lost the project's sessions; texts: %q", tt.Texts())
	}
	click(t, tt, "会话 修改登录超时配置")
	tt.Frame()
	if a.mode != "session" {
		t.Fatalf("mode %q after opening a session", a.mode)
	}
}

// snapshotWithWorkspaces republishes a workspace panel for tests that need one.
func snapshotWithWorkspaces(t *testing.T, a *App) *view.Snapshot {
	t.Helper()
	now := a.now
	a.snap.PublishWorkspaces(viewtest.DemoWorkspaces(now))
	return a.snap
}
