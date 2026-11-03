package ui

// Round 5: Markdown chat, streaming, plan panel, apply-to-project, undo reject,
// system accessibility preferences, and About/update.

import (
	"context"
	"strings"
	"testing"
	"time"

	mui "github.com/egoist/mygo/ui"

	"github.com/zeokeel/super-agent/internal/view"
	"github.com/zeokeel/super-agent/internal/view/viewtest"
)

// chatSnap publishes a chat with one Markdown answer.
func chatSnap(t *testing.T) *view.Snapshot {
	t.Helper()
	now := time.Now()
	snap := view.NewSnapshot()
	snap.PublishOverview(viewtest.DemoOverview(now))
	snap.PublishChat(&view.ChatVM{Messages: []view.ChatMessage{
		{Role: view.ChatUser, Text: "解释一下", At: now},
		{Role: view.ChatAssistant, Kind: view.ChatAnswer, Text: "# 标题\n**粗体** 和 `code()`\n- 项目一\n1. 第一步\n```\nfmt.Println(\"hi\")\n```\n[发布页](https://example.test/releases)", At: now},
	}, UpdatedAt: now})
	return snap
}

// Markdown renders headings, emphasis, code, lists and links; links open through
// the browser seam.
func TestMarkdownChatRendersAndOpensLinks(t *testing.T) {
	a := newApp(chatSnap(t), &fakeCmd{})
	a.now = time.Now()
	a.sec = view.SecChat
	tt := mui.NewTester(a.view, 1440, 1400)
	wantTexts(t, tt, "标题", "粗体", "code()", "项目一", "第一步", "fmt.Println(\"hi\")")

	opened := ""
	oldOpen := openExternalURL
	t.Cleanup(func() { openExternalURL = oldOpen })
	openExternalURL = func(url string) error { opened = url; return nil }
	click(t, tt, "发布页")
	if opened != "https://example.test/releases" {
		t.Fatalf("link opened %q", opened)
	}
}

// A streaming answer shows a typing cursor and offers 停止; after the stream ends
// the cursor and the stop button are gone.
func TestStreamingChatShowsCursorAndStop(t *testing.T) {
	cmd := &stopCmd{}
	a := newApp(chatSnap(t), cmd)
	a.now = time.Now()
	a.snap.PublishChat(&view.ChatVM{Messages: []view.ChatMessage{
		{Role: view.ChatAssistant, Kind: view.ChatAnswer, Text: "正在回答"},
	}, Streaming: true, UpdatedAt: a.now})
	a.sec = view.SecChat
	tt := mui.NewTester(a.view, 1440, 1400)
	wantTexts(t, tt, "正在回答 ▍", "停止生成")
	click(t, tt, "停止生成")
	if cmd.stopped != 1 {
		t.Fatalf("stop called %d times", cmd.stopped)
	}
	tt.Frame()
	wantTexts(t, tt, "已停止生成")

	// The runtime's ChatStreaming is enough even when the VM does not set the flag.
	fallback := newApp(chatSnap(t), &stopCmd{})
	fallback.now = time.Now()
	fallback.sec = view.SecChat
	ttFallback := mui.NewTester(fallback.view, 1440, 1400)
	wantTexts(t, ttFallback, "停止生成")

	// Without the capability: disabled button + honest reason.
	b := newApp(chatSnap(t), &fakeCmd{})
	b.now = time.Now()
	b.snap.PublishChat(&view.ChatVM{Messages: []view.ChatMessage{
		{Role: view.ChatAssistant, Kind: view.ChatAnswer, Text: "正在回答"},
	}, Streaming: true, UpdatedAt: b.now})
	b.sec = view.SecChat
	tt2 := mui.NewTester(b.view, 1440, 1400)
	wantTexts(t, tt2, "暂时不可用：运行时还没有接入停止生成")
}

type stopCmd struct {
	fakeCmd
	stopped int
}

func (c *stopCmd) ChatStreaming() bool { return true }
func (c *stopCmd) StopChat()           { c.stopped++ }

// The plan panel draws the task list with statuses in the execution detail, and
// says so when nothing is planned.
func TestPlanPanelInExecutionDetail(t *testing.T) {
	a := detailApp(t, true)
	a.snap.PublishPlan(viewtest.DemoPlan(a.now))
	tt := mui.NewTester(a.view, 1440, 1600)
	click(t, tt, "修改登录超时配置")
	wantTexts(t, tt, "计划与待办", "读 auth 的 session 代码", "已完成",
		"补 SessionTimeout 默认值", "进行中", "更新配置文档", "受阻")

	empty := detailApp(t, true)
	empty.snap.PublishPlan(&view.PlanVM{UpdatedAt: empty.now})
	tt2 := mui.NewTester(empty.view, 1440, 1600)
	click(t, tt2, "修改登录超时配置")
	wantTexts(t, tt2, "还没有计划：任务开始后，它会在这里列出步骤。")
}

// The chat page shows the running task's plan card.
func TestPlanCardOnChatPage(t *testing.T) {
	a := newApp(chatSnap(t), &fakeCmd{})
	a.now = time.Now()
	a.snap.PublishPlan(viewtest.DemoPlan(a.now))
	a.sec = view.SecChat
	tt := mui.NewTester(a.view, 1440, 1600)
	wantTexts(t, tt, "计划与待办", "1/4 完成", "补 SessionTimeout 默认值")
}

// applyCmd implements the optional ApplyToProject capability.
type applyCmd struct {
	fakeCmd
	applied []string
	err     error
}

func (c *applyCmd) ApplyArtifacts(ticketID string) ([]string, error) {
	if c.err != nil {
		return nil, c.err
	}
	c.applied = append(c.applied, ticketID)
	return []string{"report.md"}, nil
}

func (c *applyCmd) ArtifactRelPaths(ticketID string) []string {
	return []string{"report.md", "docs/login-timeout.md"}
}

// 应用到项目 confirms destination/files/overwrite, then applies off the frame.
func TestApplyToProjectDialog(t *testing.T) {
	cmd := &applyCmd{}
	a := detailApp(t, true)
	a.cmd = cmd
	tt := mui.NewTester(a.view, 1440, 1600)
	click(t, tt, "修改登录超时配置")
	click(t, tt, "应用到项目")
	wantTexts(t, tt, "应用到项目？", "/tmp/project", "report.md", "docs/login-timeout.md", "会被覆盖")
	click(t, tt, "确认应用")
	if len(cmd.applied) != 1 || cmd.applied[0] != "tk-42" {
		t.Fatalf("applied %v", cmd.applied)
	}
	tt.Frame()
	wantTexts(t, tt, "已应用到项目：1 个文件")
}

// Without the capability the button is disabled and says why.
func TestApplyToProjectUnwired(t *testing.T) {
	a := detailApp(t, true)
	tt := mui.NewTester(a.view, 1440, 1600)
	click(t, tt, "修改登录超时配置")
	wantTexts(t, tt, "暂时不可用：运行时还没有接入应用到项目")
	_ = tt.Click("应用到项目")
	if a.applyOpen {
		t.Fatal("an unwired apply opened a dialog")
	}
}

// undoCmd implements UndoReject.
type undoCmd struct {
	fakeCmd
	undone []string
}

func (c *undoCmd) UndoReject(decisionID string) error {
	c.undone = append(c.undone, decisionID)
	return nil
}

// approvalSnap publishes the overview with one request waiting for a decision.
func approvalSnap(t *testing.T) *view.Snapshot {
	t.Helper()
	now := time.Now()
	snap := view.NewSnapshot()
	snap.PublishOverview(&view.OverviewVM{
		Approvals: []view.ApprovalRow{{
			DecisionID: "d-1", From: "张三", Source: "钉钉", Said: "把用量发我",
			Intent: "跑用量查询", Deliverable: "report.md", Workspace: "acme/data",
			Age: 2 * time.Minute,
		}},
		UpdatedAt: now,
	})
	return snap
}

// Rejecting in the inbox offers a 10s 撤销 when the runtime can undo it.
func TestRejectOffersUndo(t *testing.T) {
	cmd := &undoCmd{}
	a := newApp(approvalSnap(t), &fakeCmd{})
	a.now = time.Now()
	a.cmd = cmd
	tt := mui.NewTester(a.view, 1440, 1400)
	click(t, tt, "收件箱")
	click(t, tt, "拒绝")
	tt.Frame()
	wantTexts(t, tt, "已拒绝：不会建任务", "撤销")
	click(t, tt, "撤销")
	tt.Frame()
	if len(cmd.undone) != 1 {
		t.Fatalf("undo called %d times", len(cmd.undone))
	}
	wantTexts(t, tt, "已撤销拒绝：这条请求回到收件箱")
}

// Without UndoReject there is no fake 撤销 button.
func TestRejectWithoutUndoHasNoFakeAction(t *testing.T) {
	a := newApp(approvalSnap(t), &fakeCmd{})
	a.now = time.Now()
	tt := mui.NewTester(a.view, 1440, 1400)
	click(t, tt, "收件箱")
	click(t, tt, "拒绝")
	tt.Frame()
	if exactTexts(tt, "撤销") != 0 {
		t.Fatalf("an unwired undo button appeared; texts: %q", tt.Texts())
	}
}

// System preferences: text scale multiplies, high contrast strengthens tokens, the
// system accent wins, and reduced motion turns off the stream animation.
func TestSystemAccessibilityPreferences(t *testing.T) {
	oldRead := readUIPrefs
	oldScale := viewScale
	t.Cleanup(func() { readUIPrefs = oldRead; viewScale = oldScale })
	readUIPrefs = func(a *App) uiPrefs {
		return uiPrefs{TextScale: 1.3, ReduceMotion: true, HighContrast: true, Accent: mui.Hex("#123456")}
	}
	a := pageApp(t)
	tt := mui.NewTester(a.view, 1440, 1400)
	if got := ViewScale(); got != 1.3 {
		t.Fatalf("ViewScale %v, want 1.3 (OS text scale)", got)
	}
	if !a.reduceMotion {
		t.Fatal("reduce-motion preference was not applied")
	}
	if a.tk.TextFaint != a.tk.Text || a.tk.TextMuted != a.tk.Text {
		t.Fatal("high contrast did not strengthen the text tokens")
	}
	if a.tk.Accent != mui.Hex("#123456") {
		t.Fatalf("accent %v, want the system accent", a.tk.Accent)
	}
	click(t, tt, "消息")
	if a.streamList.FollowAnimation != nil {
		t.Fatal("reduced motion still animates the stream")
	}
}

// updateCmd implements UpdateChecker.
type updateCmd struct {
	fakeCmd
	latest   string
	upToDate bool
	url      string
	version  string
}

func (c *updateCmd) Version() string { return c.version }

func (c *updateCmd) CheckForUpdate(ctx context.Context) (string, bool, string, error) {
	return c.latest, c.upToDate, c.url, nil
}

// 设置 › 关于 shows version/license and the update result; a newer version offers
// the release page.
func TestAboutAndUpdateCheck(t *testing.T) {
	cmd := &updateCmd{latest: "9.9.9", url: "https://example.test/releases", version: "1.1.0-rc2"}
	_, tt := channelApp(t, cmd)
	click(t, tt, "关于")
	wantTexts(t, tt, "版本", "1.1.0-rc2", "MIT")
	click(t, tt, "检查更新")
	tt.Frame()
	wantTexts(t, tt, "有新版本：9.9.9", "打开发布页")

	opened := ""
	oldOpen := openExternalURL
	t.Cleanup(func() { openExternalURL = oldOpen })
	openExternalURL = func(url string) error { opened = url; return nil }
	click(t, tt, "打开发布页")
	if opened != "https://example.test/releases" {
		t.Fatalf("opened %q", opened)
	}

	// Up to date.
	upA, upTT := channelApp(t, &updateCmd{upToDate: true})
	_ = upA
	click(t, upTT, "关于")
	click(t, upTT, "检查更新")
	upTT.Frame()
	wantTexts(t, upTT, "已是最新版本")

	// Unwired.
	_, unTT := channelApp(t, &fakeCmd{})
	click(t, unTT, "关于")
	wantTexts(t, unTT, "暂时不可用：运行时还没有接入检查更新")
}

// The markdown inline parser never lets a stray marker eat the rest of a line.
func TestMarkdownInlineIsSafe(t *testing.T) {
	spans := parseInline("a * b **c** `d` [e](https://x.test)")
	var got strings.Builder
	for _, s := range spans {
		got.WriteString(s.text)
		if s.bold || s.italic || s.code || s.link != "" {
			got.WriteString("|")
		}
	}
	if !strings.Contains(got.String(), "a * b") {
		t.Fatalf("stray marker was consumed: %q", got.String())
	}
}

// When the runtime publishes the unified attention inbox, 收件箱 renders it —
// not the overview's fallback lists — and it works even without an OverviewVM.
func TestAttentionInboxConsumesPublishedProjection(t *testing.T) {
	now := time.Now()
	snap := view.NewSnapshot()
	snap.PublishOverview(&view.OverviewVM{
		Approvals: []view.ApprovalRow{{DecisionID: "fallback-1", From: "旧卡片", Said: "不该出现"}},
		UpdatedAt: now,
	})
	snap.PublishAttention(&view.AttentionVM{Items: []view.AttentionItemVM{
		{Kind: "proposal", DecisionID: "d-1", Sender: "张三",
			Source: "钉钉", Text: "把用量发我", Workspace: "acme/data",
			Actions: []string{"approve", "reject"}, At: now},
		{Kind: "ticket", TicketID: "tk-42", Number: 42, Text: "配置项名用哪个？",
			Actions: []string{"answer", "retry", "stop", "open"}, At: now.Add(-time.Minute)},
	}, UpdatedAt: now})
	a := newApp(snap, &fakeCmd{})
	a.now = now
	tt := mui.NewTester(a.view, 1440, 1400)
	click(t, tt, "收件箱")
	wantTexts(t, tt, "把用量发我", "#42", "配置项名用哪个？", "待你决定", "批准", "拒绝", "打开")
	if tt.HasText("旧卡片") {
		t.Fatal("the overview fallback was drawn even though AttentionVM is published")
	}
	// No OverviewVM at all: the inbox still works from the attention projection.
	snap2 := view.NewSnapshot()
	snap2.PublishAttention(&view.AttentionVM{Items: []view.AttentionItemVM{
		{Kind: "proposal", DecisionID: "d-2", Sender: "李四", Text: "再看一下", Actions: []string{"approve", "reject"}},
	}, UpdatedAt: now})
	b := newApp(snap2, &fakeCmd{})
	b.now = now
	b.sec = view.SecInbox
	tt2 := mui.NewTester(b.view, 1440, 1400)
	wantTexts(t, tt2, "再看一下")
}
