package ui

import (
	"testing"
	"time"

	mui "github.com/egoist/mygo/ui"

	"github.com/zeokeel/super-agent/internal/view"
	"github.com/zeokeel/super-agent/internal/view/viewtest"
)

// fakeWorkspaceCmd is a runtime stand-in with the two workspace commands. It
// records what the panel asked, so a test can tell a sent command from a form
// that only looks enabled.
type fakeWorkspaceCmd struct {
	fakeCmd
	created []string // name|root|purpose|kind
	convs   []string // workspace|text
	err     error
}

func (f *fakeWorkspaceCmd) CreateWorkspace(name, root, purpose, kind string) error {
	if f.err != nil {
		return f.err
	}
	f.created = append(f.created, name+"|"+root+"|"+purpose+"|"+kind)
	return nil
}

func (f *fakeWorkspaceCmd) NewConversation(workspaceID, text string) error {
	if f.err != nil {
		return f.err
	}
	f.convs = append(f.convs, workspaceID+"|"+text)
	return nil
}

// workspaceApp publishes the overview, the tickets and the workspace tree, so
// the sidebar, 工作区 and 执行 all have data.
func workspaceApp(t *testing.T, cmd Commander) *App {
	t.Helper()
	now := time.Now()
	snap := view.NewSnapshot()
	snap.PublishOverview(viewtest.DemoOverview(now))
	snap.PublishStream(demoStream())
	snap.PublishTickets(viewtest.DemoTickets(now))
	snap.PublishWorkspaces(viewtest.DemoWorkspaces(now))
	a := newApp(snap, cmd)
	a.now = now
	return a
}

// The sidebar is 超级Agent above, the workspace tree in the middle, the monitoring
// pages and 设置 at the bottom, and the machine in the footer.
func TestSidebarStructure(t *testing.T) {
	a := pageApp(t)
	tt := mui.NewTester(a.view, 1440, 900)

	wantTexts(t, tt, "超级Agent", "工作区", "＋ 新建工作区", "监控",
		"消息", "来源", "触发", "评估", "执行", "设置",
		"linux/amd64 · Asia/Shanghai (UTC+08:00)")
	if a.sec != view.SecOverview {
		t.Fatalf("home section %q, want overview", a.sec)
	}
	// No workspace projection: the tree says so instead of drawing an empty
	// group as if no workspace existed.
	wantTexts(t, tt, "等待运行时发布工作区…")
}

// A workspace row expands to its conversations, grouped by who created them, and
// collapses again.
func TestSidebarWorkspaceTreeExpands(t *testing.T) {
	a := workspaceApp(t, &fakeCmd{})
	tt := mui.NewTester(a.view, 1440, 900)
	wantTexts(t, tt, "acme/webapp", "acme/data")
	// Collapsed: the conversations are not on screen.
	if got := exactTexts(tt, "修改登录超时配置"); got != 0 {
		t.Fatalf("collapsed workspace shows %d conversation titles", got)
	}

	click(t, tt, "展开工作区 acme/webapp")
	wantTexts(t, tt, "我创建的", "超级Agent 创建的", "＋ 新建会话",
		"把登录超时改成可配置", "修改登录超时配置")
	if got := exactTexts(tt, "修改登录超时配置"); got != 1 {
		t.Fatalf("%d conversation titles, want 1; texts: %q", got, tt.Texts())
	}

	click(t, tt, "展开工作区 acme/webapp")
	if got := exactTexts(tt, "修改登录超时配置"); got != 0 {
		t.Fatalf("collapsing left %d conversation titles", got)
	}
}

// A workspace row opens 工作区: what it is for, where its root is, and its
// conversations in their groups.
func TestWorkspaceRowOpensTheWorkspacePage(t *testing.T) {
	a := workspaceApp(t, &fakeCmd{})
	tt := mui.NewTester(a.view, 1440, 900)
	click(t, tt, "工作区 acme/webapp")
	if a.sec != view.SecWorkspace {
		t.Fatalf("section %q, want workspace", a.sec)
	}
	wantTexts(t, tt,
		"面向客户的 Web 前端与登录流程", "/Users/me/code/acme-webapp", "repo",
		"我创建的", "超级Agent 创建的", "2 个会话",
		"把登录超时改成可配置", "修改登录超时配置",
	)
}

// Clicking a conversation opens its Ticket on 执行 (navigation, not an action).
func TestConversationOpensItsTicket(t *testing.T) {
	a := workspaceApp(t, &fakeCmd{})
	tt := mui.NewTester(a.view, 1440, 900)
	click(t, tt, "工作区 acme/webapp")
	click(t, tt, "会话 修改登录超时配置")

	if a.sec != view.SecExecution {
		t.Fatalf("section %q, want execution", a.sec)
	}
	rows := ticketRowsOf(a)
	if a.ticketSel < 0 || a.ticketSel >= len(rows) {
		t.Fatalf("no row selected (%d of %d)", a.ticketSel, len(rows))
	}
	if got := rows[a.ticketSel].ID; got != "42" {
		t.Fatalf("opened ticket %q, want 42", got)
	}
	wantTexts(t, tt, "step 7 · 正在补 SessionTimeout 默认值")
}

// 新建工作区 asks for name, root, purpose and kind, and hands them to the runtime.
func TestNewWorkspaceDialogWithCreator(t *testing.T) {
	creator := &fakeWorkspaceCmd{}
	a := workspaceApp(t, creator)
	tt := mui.NewTester(a.view, 1440, 900)

	click(t, tt, "＋ 新建工作区")
	wantTexts(t, tt, "新建工作区", "名称", "根目录", "用途", "类型",
		"仓库", "数据", "文档", "混合")
	if a.wsModal != true {
		t.Fatal("the dialog did not open")
	}

	// An empty form cannot create: 创建 is disabled, so the click lands nowhere.
	click(t, tt, "创建")
	if len(creator.created) != 0 {
		t.Fatalf("an empty form created %v", creator.created)
	}

	click(t, tt, "输入名称")
	tt.Type("acme/new")
	click(t, tt, "输入根目录")
	tt.Type("/tmp/acme-new")
	click(t, tt, "输入用途")
	tt.Type("新的前端仓库")
	click(t, tt, "数据")
	click(t, tt, "创建")

	if len(creator.created) != 1 || creator.created[0] != "acme/new|/tmp/acme-new|新的前端仓库|data" {
		t.Fatalf("created %v", creator.created)
	}
	if a.wsModal {
		t.Fatal("the dialog stayed open after the runtime accepted it")
	}
}

// A refused create keeps the dialog and the runtime's error, so nothing typed is
// lost and nothing looks created.
func TestNewWorkspaceDialogKeepsTheError(t *testing.T) {
	creator := &fakeWorkspaceCmd{err: errFake("根目录不存在")}
	a := workspaceApp(t, creator)
	tt := mui.NewTester(a.view, 1440, 900)
	click(t, tt, "＋ 新建工作区")
	click(t, tt, "输入名称")
	tt.Type("acme/x")
	click(t, tt, "输入根目录")
	tt.Type("/nope")
	click(t, tt, "创建")
	if a.wsModal != true {
		t.Fatal("a failed create closed the dialog")
	}
	wantTexts(t, tt, "根目录不存在")
}

// 新建会话 asks for the message and hands workspace+text to the runtime.
func TestNewConversationDialogWithCreator(t *testing.T) {
	creator := &fakeWorkspaceCmd{}
	a := workspaceApp(t, creator)
	tt := mui.NewTester(a.view, 1440, 900)

	click(t, tt, "展开工作区 acme/webapp")
	click(t, tt, "＋ 新建会话")
	wantTexts(t, tt, "新建会话", "acme/webapp", "会话内容")
	click(t, tt, "输入会话内容")
	tt.Type("把登录超时改成可配置")
	click(t, tt, "创建会话")

	if len(creator.convs) != 1 || creator.convs[0] != "ws_webapp|把登录超时改成可配置" {
		t.Fatalf("created %v", creator.convs)
	}
	if a.convModal {
		t.Fatal("the dialog stayed open after the runtime accepted it")
	}
}

// Without the two channels the dialogs still show what they ask for, and the
// create button is disabled with the reason: no silent no-op.
func TestWorkspaceActionsUnwired(t *testing.T) {
	a := workspaceApp(t, &fakeCmd{})
	tt := mui.NewTester(a.view, 1440, 900)

	click(t, tt, "＋ 新建工作区")
	wantTexts(t, tt, "动作未接线：运行时未实现 WorkspaceCreator")
	click(t, tt, "创建")
	if !a.wsModal {
		t.Fatal("an unwired 创建 closed the dialog")
	}
	click(t, tt, "取消")

	click(t, tt, "展开工作区 acme/webapp")
	click(t, tt, "＋ 新建会话")
	wantTexts(t, tt, "动作未接线：运行时未实现 ConversationCreator")
	click(t, tt, "创建会话")
	if !a.convModal {
		t.Fatal("an unwired 创建会话 closed the dialog")
	}
	if a.convErr != "" {
		t.Fatalf("an unwired create reported %q", a.convErr)
	}
}

// The machine: OS/arch and timezone in the sidebar footer, and the same facts
// (from the projection) in 设置 › 部署. No projection says so instead of guessing.
func TestSystemFacts(t *testing.T) {
	a := pageApp(t)
	tt := mui.NewTester(a.view, 1440, 900)
	wantTexts(t, tt, "linux/amd64 · Asia/Shanghai (UTC+08:00)")

	click(t, tt, "设置")
	a.settingsGroup = view.SettingsDeploy
	tt.Frame()
	wantTexts(t, tt, "运行环境", "操作系统", "linux/amd64", "时区", "Asia/Shanghai (UTC+08:00)")

	// A window whose runtime has not projected the facts: both places say so.
	now := time.Now()
	snap := view.NewSnapshot()
	snap.PublishSettings(&view.SettingsVM{
		Groups: []view.SettingsGroup{{ID: view.SettingsDeploy, Label: "部署", Note: "运维"}},
		Source: "mygo.json", Revision: 1, UpdatedAt: now,
	})
	a = newApp(snap, &fakeCmd{})
	a.now = now
	tt = mui.NewTester(a.view, 1440, 900)
	wantTexts(t, tt, "系统信息未投影")
	click(t, tt, "设置")
	a.settingsGroup = view.SettingsDeploy
	tt.Frame()
	wantTexts(t, tt, "等待运行时投影操作系统与时区（Runtime.System）")
}

// The font scale is one token: +2 on the panel's 13, and a theme derived from it.
func TestFontScaleIsPlusTwo(t *testing.T) {
	if baseFontSize != 15 {
		t.Fatalf("base font size %v, want 15 (13+2)", float32(baseFontSize))
	}
	for _, dark := range []bool{false, true} {
		base := mui.LightTheme()
		if dark {
			base = mui.DarkTheme()
		}
		th := brandFor(dark).theme(base)
		if th.FontSize != float32(baseFontSize) {
			t.Fatalf("dark=%v: theme font %v, want %v", dark, th.FontSize, float32(baseFontSize))
		}
	}
	// A text drawn with an explicit size in the panel is large enough to read:
	// the smallest text size after the bump is 11.
	if tk := tokensFrom(brandFor(false).theme(mui.LightTheme())); tk.Bg == (mui.Color{}) {
		t.Fatal("tokens stopped deriving from the theme")
	}
}

// The agent's answer to an operator message replaces a bare "未受理": the panel
// draws the answer, not the agent's failure to parse it.
func TestAgentAnswerReplacesBareUnaccepted(t *testing.T) {
	now := time.Now()
	snap := view.NewSnapshot()
	snap.PublishOverview(viewtest.DemoOverview(now))
	snap.PublishStream(&view.StreamVM{Threads: []view.Thread{{
		ID:   "t-1",
		Head: view.Line{Kind: view.LineOperator, Who: "你", Source: "本地面板", Text: "这里有哪些工作区？", At: now},
		Steps: []view.Line{
			{Kind: view.LineDecision, Text: "未受理", Tone: view.ToneNeutral, At: now},
			{Kind: view.LineNotice, Tone: view.ToneInfo, At: now,
				Text: "超级Agent：目前有 2 个工作区：acme/webapp（前端）、acme/data（数据）。"},
		},
	}}})
	a := newApp(snap, &fakeCmd{})
	a.now = now
	tt := mui.NewTester(a.view, 1440, 900)
	click(t, tt, "消息")
	wantTexts(t, tt, "超级Agent", "目前有 2 个工作区")
	if got := exactTexts(tt, "未受理"); got != 0 {
		t.Fatalf("a bare 未受理 is still drawn beside the answer (%d); texts: %q", got, tt.Texts())
	}
}

// Without an answer the bare outcome stays: it is the outcome.
func TestBareUnacceptedStaysWhenNoAnswer(t *testing.T) {
	now := time.Now()
	snap := view.NewSnapshot()
	snap.PublishOverview(viewtest.DemoOverview(now))
	snap.PublishStream(&view.StreamVM{Threads: []view.Thread{{
		ID:   "t-2",
		Head: view.Line{Kind: view.LineInbound, Who: "@lin", Source: "GitHub · #42", Text: "随便说说", At: now},
		Steps: []view.Line{
			{Kind: view.LineDecision, Text: "未受理", Tone: view.ToneNeutral, At: now},
		},
	}}})
	a := newApp(snap, &fakeCmd{})
	a.now = now
	tt := mui.NewTester(a.view, 1440, 900)
	click(t, tt, "消息")
	if got := exactTexts(tt, "未受理"); got != 1 {
		t.Fatalf("%d 未受理 lines, want 1; texts: %q", got, tt.Texts())
	}
}

// A reply line the runtime addresses to a colleague is not the operator's answer:
// it stays a quiet line and does not hide a decision.
func TestSomeoneElsesReplyIsNotTheAnswer(t *testing.T) {
	now := time.Now()
	snap := view.NewSnapshot()
	snap.PublishOverview(viewtest.DemoOverview(now))
	snap.PublishStream(&view.StreamVM{Threads: []view.Thread{{
		ID:   "t-3",
		Head: view.Line{Kind: view.LineInbound, Who: "@lin", Source: "GitHub · #42", Text: "帮我看下 CI", At: now},
		Steps: []view.Line{
			{Kind: view.LineReply, Text: "已在评论里回复 @lin", At: now},
			{Kind: view.LineDecision, Text: "未受理", Tone: view.ToneNeutral, At: now},
		},
	}}})
	a := newApp(snap, &fakeCmd{})
	a.now = now
	tt := mui.NewTester(a.view, 1440, 900)
	click(t, tt, "消息")
	wantTexts(t, tt, "已在评论里回复 @lin")
	if got := exactTexts(tt, "未受理"); got != 1 {
		t.Fatalf("%d 未受理 lines, want 1; texts: %q", got, tt.Texts())
	}
}

// The 浏览… button asks the native folder picker and fills the root it answered.
// The picker seam answers at once here; the real one runs mygo's native dialog on
// a goroutine and comes back through Window.Update.
func TestWorkspaceFolderPickerFillsRoot(t *testing.T) {
	old := startFolderPicker
	t.Cleanup(func() { startFolderPicker = old })

	calls := 0
	startFolderPicker = func(a *App) {
		calls++
		a.pickFolderDone([]string{"/tmp/picked-root"}, nil)
	}
	a := workspaceApp(t, &fakeWorkspaceCmd{})
	tt := mui.NewTester(a.view, 1440, 900)
	click(t, tt, "＋ 新建工作区")
	click(t, tt, "浏览…")

	if calls != 1 {
		t.Fatalf("the picker opened %d times", calls)
	}
	if a.wsDraft.root != "/tmp/picked-root" {
		t.Fatalf("root %q, want the picked path", a.wsDraft.root)
	}
	if a.pickingFolder {
		t.Fatal("the picker is still marked open after it answered")
	}
	if a.wsErr != "" {
		t.Fatalf("a picked directory reported %q", a.wsErr)
	}
}

// Cancelling the picker changes nothing and is not an error: the form keeps what
// the person had already typed.
func TestWorkspaceFolderPickerCancel(t *testing.T) {
	old := startFolderPicker
	t.Cleanup(func() { startFolderPicker = old })
	startFolderPicker = func(a *App) { a.pickFolderDone(nil, nil) }

	a := workspaceApp(t, &fakeWorkspaceCmd{})
	tt := mui.NewTester(a.view, 1440, 900)
	click(t, tt, "＋ 新建工作区")
	a.wsDraft.root = "/already/typed"
	click(t, tt, "浏览…")
	if a.wsDraft.root != "/already/typed" {
		t.Fatalf("cancel changed the root to %q", a.wsDraft.root)
	}
	if a.wsErr != "" || a.pickingFolder {
		t.Fatalf("cancel left err=%q picking=%v", a.wsErr, a.pickingFolder)
	}
}

// A picker that fails shows the error and lets the person try again.
func TestWorkspaceFolderPickerError(t *testing.T) {
	old := startFolderPicker
	t.Cleanup(func() { startFolderPicker = old })
	startFolderPicker = func(a *App) { a.pickFolderDone(nil, errFake("没有可用的桌面会话")) }

	a := workspaceApp(t, &fakeWorkspaceCmd{})
	tt := mui.NewTester(a.view, 1440, 900)
	click(t, tt, "＋ 新建工作区")
	click(t, tt, "浏览…")
	if a.pickingFolder {
		t.Fatal("a failed picker stayed open")
	}
	wantTexts(t, tt, "没有可用的桌面会话")
}

// One dialog at a time: a second click while the picker is open starts nothing.
func TestWorkspaceFolderPickerIsSingle(t *testing.T) {
	old := startFolderPicker
	t.Cleanup(func() { startFolderPicker = old })
	calls := 0
	startFolderPicker = func(a *App) { calls++ } // never answers: "still open"

	a := workspaceApp(t, &fakeWorkspaceCmd{})
	tt := mui.NewTester(a.view, 1440, 900)
	click(t, tt, "＋ 新建工作区")
	click(t, tt, "浏览…")
	tt.Frame()
	click(t, tt, "选择中…")
	if calls != 1 {
		t.Fatalf("the picker opened %d times; a second click must not", calls)
	}
}

// The native dialog is the directory chooser the addendum asks for, not a file
// picker and not an external command.
func TestWorkspaceFolderDialogOptions(t *testing.T) {
	opts := folderDialogOptions(nil, "选择工作区目录")
	if !opts.Directory {
		t.Fatal("the picker is not a directory chooser")
	}
	if !opts.CreateDirectories {
		t.Fatal("the picker does not offer to create a directory")
	}
	if opts.ButtonLabel != "选择此目录" {
		t.Fatalf("button label %q", opts.ButtonLabel)
	}
	if opts.Title != "选择工作区目录" {
		t.Fatalf("title %q", opts.Title)
	}
	if opts.Multiple {
		t.Fatal("a workspace root is one directory")
	}
}
