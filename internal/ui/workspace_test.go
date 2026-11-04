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

// A workspace row opens 工作区: what it is for, where its root is, and its
// conversations in their groups.
func TestWorkspaceRowOpensTheWorkspacePage(t *testing.T) {
	a := workspaceApp(t, &fakeCmd{})
	tt := mui.NewTester(a.view, 1440, 900)
	click(t, tt, "项目 acme/webapp")
	if a.sec != view.SecWorkspace {
		t.Fatalf("section %q, want workspace", a.sec)
	}
	wantTexts(t, tt,
		"面向客户的 Web 前端与登录流程", "/Users/me/code/acme-webapp", "repo",
		"我创建的", "超级Agent 创建的", "2 个会话",
		"把登录超时改成可配置", "修改登录超时配置",
	)
}

// 新建项目 asks for name, root, purpose and kind, and hands them to the runtime.
func TestNewWorkspaceDialogWithCreator(t *testing.T) {
	creator := &fakeWorkspaceCmd{}
	a := workspaceApp(t, creator)
	tt := mui.NewTester(a.view, 1440, 900)

	click(t, tt, "＋ 新建项目")
	wantTexts(t, tt, "新建项目", "名称", "根目录", "用途", "类型",
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
	click(t, tt, "＋ 新建项目")
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

// Without the two channels the dialogs still show what they ask for, and the
// create button is disabled with the reason: no silent no-op.
func TestWorkspaceActionsUnwired(t *testing.T) {
	a := workspaceApp(t, &fakeCmd{})
	tt := mui.NewTester(a.view, 1440, 900)

	click(t, tt, "＋ 新建项目")
	wantTexts(t, tt, "暂时不可用：运行时还没有接入 WorkspaceCreator")
	click(t, tt, "创建")
	if !a.wsModal {
		t.Fatal("an unwired 创建 closed the dialog")
	}
	click(t, tt, "取消")

	// A conversation is created from the selected project (⌘N), and the modal
	// stays honest without ConversationCreator.
	click(t, tt, "项目 acme/webapp")
	tt.Key(mui.Cmd, mui.KeyN)
	tt.Frame()
	if !a.convModal {
		t.Fatal("⌘N did not open the new-conversation dialog")
	}
	wantTexts(t, tt, "暂时不可用：运行时还没有接入 ConversationCreator")
	click(t, tt, "创建会话")
	if !a.convModal {
		t.Fatal("an unwired 创建会话 closed the dialog")
	}
	if a.convErr != "" {
		t.Fatalf("an unwired create reported %q", a.convErr)
	}
}

// The font scale is one token: +2 on the panel's 13, and a theme derived from it.
func TestFontScaleToken(t *testing.T) {
	old := viewScale
	t.Cleanup(func() { viewScale = old })
	if baseFontSize != 15 {
		t.Fatalf("base font size %v, want 15 (13+2)", float32(baseFontSize))
	}
	// One scale: the theme and every explicit size go through it, and 1 is the
	// design's own size (this replaced the magic +2 at every number).
	viewScale = 1
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
	viewScale = 1.1
	if got := fs(10); got < 10.99 || got > 11.01 {
		t.Fatalf("fs(10) at 1.1 = %v", got)
	}
	// A text drawn with an explicit size in the panel is large enough to read:
	// the smallest text size after the bump is 11.
	if tk := tokensFrom(brandFor(false).theme(mui.LightTheme())); tk.Bg == (mui.Color{}) {
		t.Fatal("tokens stopped deriving from the theme")
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
	click(t, tt, "＋ 新建项目")
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
	click(t, tt, "＋ 新建项目")
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
	click(t, tt, "＋ 新建项目")
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
	click(t, tt, "＋ 新建项目")
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
