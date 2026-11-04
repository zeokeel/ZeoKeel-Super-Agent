package ui

// 项目面板: 概览/设置/统计 tabs, browser detection/config, skill switches, stats and
// the DSH-aligned inbox copy.

import (
	"strings"
	"testing"
	"time"

	mui "github.com/egoist/mygo/ui"

	"github.com/zeokeel/super-agent/internal/view"
)

// wsPanelCmd implements every optional 项目 panel capability.
type wsPanelCmd struct {
	fakeCmd
	detected    []view.BrowserInfo
	settings    view.WorkspaceSettings
	hasSettings bool
	saved       []view.WorkspaceSettings
	stats       view.WorkspaceStats
	skills      []view.SkillInfo
	running     bool
	detail      string
	started     int
	stopped     int
	saveErr     error
}

func (c *wsPanelCmd) DetectedBrowsers() []view.BrowserInfo { return c.detected }

func (c *wsPanelCmd) WorkspaceSettings(workspaceID string) (view.WorkspaceSettings, bool) {
	return c.settings, c.hasSettings
}

func (c *wsPanelCmd) SaveWorkspaceSettings(workspaceID string, s view.WorkspaceSettings) error {
	if c.saveErr != nil {
		return c.saveErr
	}
	c.saved = append(c.saved, s)
	c.settings = s
	c.hasSettings = true
	return nil
}

func (c *wsPanelCmd) WorkspaceStats(workspaceID string) (view.WorkspaceStats, bool) {
	return c.stats, true
}

func (c *wsPanelCmd) Skills() []view.SkillInfo { return c.skills }

func (c *wsPanelCmd) StartBrowser(workspaceID string) error {
	c.started++
	c.running = true
	c.detail = "CDP 9333"
	return nil
}

func (c *wsPanelCmd) StopBrowser(workspaceID string) error {
	c.stopped++
	c.running = false
	c.detail = "已停止"
	return nil
}

func (c *wsPanelCmd) BrowserStatus(workspaceID string) (bool, string) {
	return c.running, c.detail
}

// openWorkspacePanel opens acme/webapp's panel and one tab by its labelled button.
func openWorkspacePanel(t *testing.T, cmd Commander, tabLabel string) (*App, *mui.Tester) {
	t.Helper()
	a := workspaceApp(t, cmd)
	tt := mui.NewTester(a.view, 1440, 1600)
	click(t, tt, "项目 acme/webapp")
	if tabLabel != "" {
		click(t, tt, tabLabel)
	}
	return a, tt
}

// The panel has three tabs; 概览 is the default and keeps the conversations.
func TestWorkspacePanelTabs(t *testing.T) {
	a, tt := openWorkspacePanel(t, &wsPanelCmd{}, "")
	wantTexts(t, tt, "概览", "设置", "统计", "我创建的", "把登录超时改成可配置")
	click(t, tt, "项目设置")
	wantTexts(t, tt, "浏览器", "技能")
	click(t, tt, "项目统计")
	wantTexts(t, tt, "统计", "会话", "最近活动")
	click(t, tt, "项目概览")
	wantTexts(t, tt, "我创建的")
	_ = a
}

// Browser + skills settings save through the optional saver.
func TestWorkspaceBrowserAndSkillsSettings(t *testing.T) {
	cmd := &wsPanelCmd{
		detected: []view.BrowserInfo{
			{ID: "chrome", Name: "Chrome", Path: "/opt/chrome", Version: "120", Available: true},
			{ID: "edge", Name: "Edge", Path: "/opt/edge", Version: "120", Available: true},
		},
		settings:    view.WorkspaceSettings{Browser: "chrome", ProfileDir: "/state/browser/ws_webapp"},
		hasSettings: true,
		skills: []view.SkillInfo{
			{ID: "web", Name: "网页搜索", Description: "搜索网页", EnabledByDefault: true},
			{ID: "plan", Name: "计划", Description: "任务计划", EnabledByDefault: true},
		},
	}
	a, tt := openWorkspacePanel(t, cmd, "项目设置")
	wantTexts(t, tt, "Chrome", "Edge", "/opt/chrome", "/state/browser/ws_webapp")

	// Turn one skill off, choose Edge and headless, set the port, save.
	click(t, tt, "技能 网页搜索")
	click(t, tt, "浏览器 Edge")
	click(t, tt, "无头模式")
	a.wsPort = 9333
	tt.Frame()
	click(t, tt, "保存项目设置")
	tt.Frame()
	if len(cmd.saved) != 1 {
		t.Fatalf("saved %d times", len(cmd.saved))
	}
	got := cmd.saved[0]
	if got.Browser != "edge" || !got.Headless || got.Port != 9333 {
		t.Fatalf("saved settings %+v", got)
	}
	if len(got.DisabledSkills) != 1 || got.DisabledSkills[0] != "web" {
		t.Fatalf("disabled skills %+v", got.DisabledSkills)
	}
	wantTexts(t, tt, "项目设置已保存")
}

// Without the capabilities the panel says so; nothing pretends to work.
func TestWorkspacePanelUnwired(t *testing.T) {
	_, tt := openWorkspacePanel(t, &fakeCmd{}, "项目设置")
	wantTexts(t, tt,
		"暂时不可用：运行时还没有接入工作区设置（WorkspaceSettingsProvider / SaveWorkspaceSettings）。",
		"暂时不可用：运行时还没有接入浏览器检测",
		"暂时不可用：运行时还没有接入浏览器控制",
		"暂时不可用：运行时还没有提供技能库（SkillLibrary）",
		"暂时不可用：运行时还没有接入保存项目设置",
	)
}

// Stats show the runtime's counts, recent activity and tool categories; an absent
// runtime falls back to the published conversation count, honestly labelled.
func TestWorkspaceStats(t *testing.T) {
	cmd := &wsPanelCmd{stats: view.WorkspaceStats{
		Conversations: 12, Tasks: 5, Running: 2, Waiting: 1, Completed: 8, Failed: 1,
		Recent: []view.ActivityRow{{Text: "完成了登录超时的改动", At: time.Now().Add(-2 * time.Minute)}},
		Tools: []view.ToolCategory{
			{ID: "fs", Label: "文件读写", Description: "read/write/edit", Available: true},
			{ID: "web", Label: "网络", Description: "web_fetch", Available: false},
		},
		UpdatedAt: time.Now(),
	}}
	_, tt := openWorkspacePanel(t, cmd, "项目统计")
	wantTexts(t, tt, "12", "5", "2", "1", "8",
		"完成了登录超时的改动", "文件读写", "可用", "网络", "未启用")

	_, unTT := openWorkspacePanel(t, &fakeCmd{}, "项目统计")
	wantTexts(t, unTT, "运行时还没有提供任务级统计；下面只按已发布的会话计数。",
		"运行时还没有报告工具清单（不在这里猜能力）")
}

// 启动/停止浏览器 is pending then reflects the controller's reported status only.
func TestWorkspaceBrowserStartStop(t *testing.T) {
	cmd := &wsPanelCmd{}
	_, tt := openWorkspacePanel(t, cmd, "项目设置")
	wantTexts(t, tt, "已停止")
	click(t, tt, "启动浏览器")
	tt.Frame()
	if cmd.started != 1 {
		t.Fatalf("started %d times", cmd.started)
	}
	wantTexts(t, tt, "运行中", "CDP 9333")
	click(t, tt, "停止浏览器")
	tt.Frame()
	if cmd.stopped != 1 {
		t.Fatalf("stopped %d times", cmd.stopped)
	}
	wantTexts(t, tt, "已停止")
}

// 设置 shows the detected browsers (default-path detection) and is honest when
// nothing is wired.
func TestDetectedBrowsersInSettings(t *testing.T) {
	cmd := &wsPanelCmd{detected: []view.BrowserInfo{
		{ID: "chrome", Name: "Chrome", Path: "/opt/chrome", Version: "120", Available: true},
	}}
	_, tt := channelApp(t, cmd)
	click(t, tt, "部署")
	wantTexts(t, tt, "浏览器（默认路径检测）", "Chrome", "/opt/chrome", "120")

	_, unTT := channelApp(t, &fakeCmd{})
	click(t, unTT, "部署")
	wantTexts(t, unTT, "没有检测到 Chrome/Edge")
}

// The inbox is no longer approval-first: attention/questions/failures first, and a
// legacy approval is still usable when one is present.
func TestInboxIsNotApprovalFirst(t *testing.T) {
	a := newApp(approvalSnap(t), &fakeCmd{})
	a.now = time.Now()
	tt := mui.NewTester(a.view, 1440, 1400)
	click(t, tt, "收件箱")
	if strings.Contains(strings.Join(tt.Texts(), " "), "批准后才会开工") {
		t.Fatal("the inbox still leads with approval copy")
	}
	wantTexts(t, tt, "旧式请求（需要批准）", "批准", "拒绝")

	// The overview keeps the legacy card, but not as the default workflow.
	ov := newApp(approvalSnap(t), &fakeCmd{})
	ov.now = time.Now()
	tt2 := mui.NewTester(ov.view, 1440, 1400)
	wantTexts(t, tt2, "旧式请求待你决定", "旧式流程：批准后才开工；新请求会直接进入任务")
}
