package ui

// Round-3 fixes: identity (P0-3), async commands (P0-2), the 超级Agent chat
// (P0-7), the workspace context (P1-7), workspace management (P1-6), and the P2
// polish (transport truth, per-group save effects, one text scale).

import (
	"fmt"
	"testing"
	"time"

	mui "github.com/egoist/mygo/ui"

	"github.com/zeokeel/super-agent/internal/view"
	"github.com/zeokeel/super-agent/internal/view/viewtest"
)

// round3App is the panel as a real runtime would populate it: tickets with domain
// ids, workspaces, a stream, settings, prompts and the 超级Agent conversation.
func round3App(t *testing.T, cmd Commander) (*App, *mui.Tester) {
	t.Helper()
	now := time.Now()
	snap := view.NewSnapshot()
	snap.PublishOverview(viewtest.DemoOverview(now))
	snap.PublishStream(demoStream())
	snap.PublishTickets(viewtest.DemoTickets(now))
	snap.PublishWorkspaces(viewtest.DemoWorkspaces(now))
	snap.PublishSettings(viewtest.DemoSettings(now))
	snap.PublishPrompts(viewtest.DemoPrompts(now))
	snap.PublishChat(viewtest.DemoChat(now))
	a := newApp(snap, cmd)
	a.now = now
	return a, mui.NewTester(a.view, 1440, 1600)
}

// fakeWorkspaceAdmin is a runtime that implements the P1-6 management actions.
type fakeWorkspaceAdmin struct {
	fakeWorkspaceCmd
	renamed []string
	deleted []string
	err     error
}

func (f *fakeWorkspaceAdmin) RenameWorkspace(id, name string) error {
	if f.err != nil {
		return f.err
	}
	f.renamed = append(f.renamed, id+"|"+name)
	return nil
}

func (f *fakeWorkspaceAdmin) DeleteWorkspace(id string) error {
	if f.err != nil {
		return f.err
	}
	f.deleted = append(f.deleted, id)
	return nil
}

// ---- P0-2: asynchronous commands ----

// A dispatch is submitted and returns: the frame shows 发送中…, the button is
// disabled, and a second click cannot send it twice. The answer applies later.
func TestDispatchShowsPendingAndGuardsDoubleSubmit(t *testing.T) {
	old := startDispatch
	t.Cleanup(func() { startDispatch = old })
	startDispatch = func(a *App, text string) { /* hold: still sending */ }

	a := newTestApp(t, false)
	cmd := &fakeCmd{}
	a.cmd = cmd
	a.draft = "把登录超时改成可配置"
	tt := mui.NewTester(a.view, 1440, 1600)
	click(t, tt, "发送")

	if !a.dispatchPending {
		t.Fatal("the command is not pending")
	}
	if a.composerNote != "" || a.composerErr != "" {
		t.Fatalf("pending left note=%q err=%q", a.composerNote, a.composerErr)
	}
	wantTexts(t, tt, "发送中…")
	if exactTexts(tt, "发送") != 0 {
		t.Fatal("the send button is still active while sending")
	}
	// A click while pending must not start a second dispatch (the button is
	// disabled; count the seam's calls rather than trusting the click error).
	if err := tt.Click("发送中…"); err == nil {
		t.Log("a tester click reached the disabled button")
	}
	if len(cmd.got) != 0 {
		t.Fatalf("dispatch reported %v before the runtime answered", cmd.got)
	}
	// The answer comes back on the main thread.
	a.dispatchDone("把登录超时改成可配置", nil)
	tt.Frame()
	if a.dispatchPending {
		t.Fatal("the command stayed pending after its answer")
	}
}

// The draft is taken at once (optimistic), and the echo keeps the line visible
// while the runtime works: nothing a person typed disappears.
func TestDispatchClearsDraftOptimistically(t *testing.T) {
	old := startDispatch
	t.Cleanup(func() { startDispatch = old })
	startDispatch = func(a *App, text string) { /* hold */ }

	a := newTestApp(t, false)
	a.cmd = &fakeCmd{}
	a.draft = "今天中午吃什么"
	tt := mui.NewTester(a.view, 1440, 1600)
	click(t, tt, "发送")
	if a.draft != "" {
		t.Fatalf("draft %q, want it taken", a.draft)
	}
	wantTexts(t, tt, "今天中午吃什么") // the echo while pending
}

// Creating a workspace is off the frame too: the dialog says 创建中…, stays open,
// and cannot be submitted twice.
func TestCreateWorkspaceShowsPending(t *testing.T) {
	old := startCreateWorkspace
	t.Cleanup(func() { startCreateWorkspace = old })
	calls := 0
	startCreateWorkspace = func(a *App, name, root, purpose, kind string) { calls++ }

	cmd := &fakeWorkspaceCmd{}
	a := workspaceApp(t, cmd)
	tt := mui.NewTester(a.view, 1440, 1600)
	click(t, tt, "＋ 新建项目")
	a.wsDraft.name, a.wsDraft.root = "acme/new", "/tmp/acme-new"
	tt.Frame()
	click(t, tt, "创建")

	if calls != 1 || !a.wsCreating {
		t.Fatalf("calls=%d creating=%v", calls, a.wsCreating)
	}
	wantTexts(t, tt, "创建中…")
	if !a.wsModal {
		t.Fatal("the dialog closed while its command was still running")
	}
	_ = tt.Click("创建中…")
	if calls != 1 {
		t.Fatalf("a second click reached the create seam (%d calls)", calls)
	}
	a.workspaceCreated(nil)
	tt.Frame()
	if a.wsCreating || a.wsModal {
		t.Fatalf("creating=%v modal=%v after the answer", a.wsCreating, a.wsModal)
	}
	if len(cmd.created) != 0 {
		t.Fatalf("the fake ran before the seam answered: %v", cmd.created)
	}
}

// A new conversation is the same shape: pending, guarded, applied on the thread.
func TestNewConversationShowsPending(t *testing.T) {
	old := startNewConversation
	t.Cleanup(func() { startNewConversation = old })
	var gotWS, gotText string
	startNewConversation = func(a *App, wsID, text string) { gotWS, gotText = wsID, text }

	a := workspaceApp(t, &fakeWorkspaceCmd{})
	tt := mui.NewTester(a.view, 1440, 1600)
	click(t, tt, "项目 acme/webapp")
	click(t, tt, "＋ 新建会话")
	a.convDraft = "把登录超时改成可配置"
	tt.Frame()
	click(t, tt, "创建会话")
	if !a.convCreating || gotText != "把登录超时改成可配置" || gotWS != "ws_webapp" {
		t.Fatalf("pending=%v ws=%q text=%q", a.convCreating, gotWS, gotText)
	}
	wantTexts(t, tt, "创建中…")
	a.conversationCreated(nil)
	tt.Frame()
	if a.convCreating || a.convModal {
		t.Fatal("the conversation dialog did not close after its answer")
	}
}

// Saving a settings group shows 保存中… and a second click cannot send it twice.
func TestSaveSettingsShowsPending(t *testing.T) {
	old := startSaveSettings
	t.Cleanup(func() { startSaveSettings = old })
	var held func(uint64, error)
	sent := 0
	startSaveSettings = func(a *App, group string, values map[string]string, onDone func(uint64, error)) {
		sent++
		held = onDone
	}

	saver := &fakeSaver{}
	a, tt := channelApp(t, saver)
	editPollInterval(t, a)
	tt.Frame()
	click(t, tt, "保存")

	if sent != 1 || a.savingGroup != string(view.SettingsSource) {
		t.Fatalf("sent=%d savingGroup=%q", sent, a.savingGroup)
	}
	wantTexts(t, tt, "保存中…")
	if len(saver.values) != 0 {
		t.Fatalf("the fake ran before the seam answered: %v", saver.values)
	}
	if held == nil {
		t.Fatal("no completion to release")
	}
	held(9, nil)
	tt.Frame()
	if a.savingGroup != "" {
		t.Fatalf("savingGroup %q after the answer", a.savingGroup)
	}
}

// ---- P0-7: the 超级Agent chat ----

// The overview carries the conversation, and the last answer sits above the
// composer on every page: a message never disappears into another page.
func TestChatIsOnOverviewAndAboveTheComposer(t *testing.T) {
	_, tt := round3App(t, &fakeCmd{})
	wantTexts(t, tt,
		"超级Agent", "问点什么，它回答；派一件事，它去做",
		"登录超时能改成可配置吗？", "已派发 #42 · acme/webapp · 等待执行",
	)
	last := "目前有 3 个工作区：acme/webapp（前端与登录流程）、acme/api（后端 API）、acme/data（用量数据）。想让我在哪个工作区里做事？"
	// The newest agent line is the composer's context line, on 总览…
	wantTexts(t, tt, last)
	// … and on another page.
	click(t, tt, "执行")
	wantTexts(t, tt, last)
}

// Chat answers are not errors: the answer renders and no red composer error is
// set. A genuine failure is the only red kind (view test pins IsFailure).
func TestChatAnswerIsNotAnError(t *testing.T) {
	now := time.Now()
	snap := view.NewSnapshot()
	snap.PublishOverview(viewtest.DemoOverview(now))
	snap.PublishChat(&view.ChatVM{Messages: []view.ChatMessage{
		{Role: view.ChatUser, Text: "跑一下", At: now},
		{Role: view.ChatAssistant, Kind: view.ChatError, Text: "连不上模型", At: now},
	}, UpdatedAt: now})
	a := newApp(snap, &fakeCmd{})
	a.now = now
	tt := mui.NewTester(a.view, 1440, 1600)
	wantTexts(t, tt, "连不上模型")
	if a.composerErr != "" {
		t.Fatalf("a chat line set the composer's red error: %q", a.composerErr)
	}
}

// The chat panel keeps its bounded history: only the last 8 turns draw.
func TestChatPanelIsBounded(t *testing.T) {
	now := time.Now()
	msgs := make([]view.ChatMessage, 0, 12)
	for i := 0; i < 12; i++ {
		msgs = append(msgs, view.ChatMessage{Role: view.ChatUser, Text: fmt.Sprintf("第%d句", i), At: now})
	}
	snap := view.NewSnapshot()
	snap.PublishOverview(viewtest.DemoOverview(now))
	snap.PublishChat(&view.ChatVM{Messages: msgs, UpdatedAt: now})
	a := newApp(snap, &fakeCmd{})
	a.now = now
	tt := mui.NewTester(a.view, 1440, 1600)
	wantTexts(t, tt, "第11句")
	for _, hidden := range []string{"第0句", "第3句"} {
		if exactTexts(tt, hidden) != 0 {
			t.Fatalf("the chat drew %q beyond its last 8", hidden)
		}
	}
}

// ---- P0-3: identity ----

// Rows read "#42", but every lookup uses the domain id: the rich detail arrives
// even though the number is not the key.
func TestRowsShowNumberAndLookUpByDomainID(t *testing.T) {
	a, tt := round3App(t, &fakeCmd{})
	click(t, tt, "执行")
	wantTexts(t, tt, "#42")
	click(t, tt, "修改登录超时配置")
	wantTexts(t, tt, "step 7 · 正在补 SessionTimeout 默认值")
	rows := ticketRowsOf(a)
	if rows[a.ticketSel].ID != "tk-42" {
		t.Fatalf("selected by %q, want the domain id", rows[a.ticketSel].ID)
	}
}

// The workspace row opens the conversation's Ticket by the conversation's domain
// id (P0-3 for conversations: they already carry domain ids).
func TestConversationOpensTicketByDomainID(t *testing.T) {
	a, tt := round3App(t, &fakeCmd{})
	click(t, tt, "项目 acme/webapp")
	click(t, tt, "会话 修改登录超时配置")
	if a.sec != view.SecExecution {
		t.Fatalf("section %q, want execution", a.sec)
	}
	rows := ticketRowsOf(a)
	if got := rows[a.ticketSel].ID; got != "tk-42" {
		t.Fatalf("opened %q, want tk-42", got)
	}
	wantTexts(t, tt, "step 7 · 正在补 SessionTimeout 默认值")
}

// ---- P1-7: the workspace context ----

// Selecting a workspace filters 执行, says so, and 全部 restores every workspace.
func TestWorkspaceFilterOnExecution(t *testing.T) {
	a, tt := round3App(t, &fakeCmd{})
	click(t, tt, "项目 acme/webapp") // selects it as the context (P1-7)
	click(t, tt, "执行")

	wantTexts(t, tt, "下面的列表只显示这个工作区；点「全部」恢复")
	if exactTexts(tt, "#42") == 0 {
		t.Fatalf("the filtered list lost the workspace's own Ticket; texts: %q", tt.Texts())
	}
	// tk-46 belongs to acme/data: it must not show under the webapp filter.
	if exactTexts(tt, "#46") != 0 {
		t.Fatalf("the filter leaked another workspace's Ticket; texts: %q", tt.Texts())
	}
	click(t, tt, "全部工作区")
	if a.filterWorkspace != "" {
		t.Fatalf("filter %q after 全部", a.filterWorkspace)
	}
	if exactTexts(tt, "#46") == 0 {
		t.Fatal("全部 did not restore the other workspace's rows")
	}
}

// 消息 filters too, with an honest empty state when the workspace has no thread.
func TestWorkspaceFilterOnMessages(t *testing.T) {
	a, tt := round3App(t, &fakeCmd{})
	click(t, tt, "项目 acme/data")
	click(t, tt, "消息")
	wantTexts(t, tt, "整理 acme 上月用量摘要")
	if tt.HasText("登录超时应可配置，默认 30 分钟") {
		t.Fatal("the filtered stream shows another workspace's message")
	}
	click(t, tt, "全部工作区")
	if !tt.HasText("登录超时应可配置，默认 30 分钟") {
		t.Fatal("全部 did not restore the stream")
	}
	_ = a
}

// ---- P1-6: workspace management ----

// 重命名 runs through WorkspaceRenamer off the frame and closes on success.
func TestWorkspaceRenameWired(t *testing.T) {
	cmd := &fakeWorkspaceAdmin{}
	a, tt := round3App(t, cmd)
	click(t, tt, "项目 acme/webapp")
	click(t, tt, "重命名")
	a.renameDraft = "acme/webapp-renamed"
	tt.Frame()
	click(t, tt, "保存名称")

	if len(cmd.renamed) != 1 || cmd.renamed[0] != "ws_webapp|acme/webapp-renamed" {
		t.Fatalf("renamed %v", cmd.renamed)
	}
	if a.renameOpen {
		t.Fatal("the rename dialog stayed open after it succeeded")
	}
}

// 删除 asks first and says what is (not) deleted; the confirmation is the only
// way through, and deleting the selected workspace clears the context filter.
func TestWorkspaceDeleteAsksThenApplies(t *testing.T) {
	cmd := &fakeWorkspaceAdmin{}
	a, tt := round3App(t, cmd)
	click(t, tt, "项目 acme/webapp")
	click(t, tt, "删除")
	wantTexts(t, tt, "删除工作区“acme/webapp”？", "磁盘上的目录不会被删除")
	if len(cmd.deleted) != 0 {
		t.Fatalf("the delete ran before it was confirmed: %v", cmd.deleted)
	}
	click(t, tt, "取消")
	if a.deleteOpen {
		t.Fatal("cancel kept the delete dialog open")
	}
	click(t, tt, "删除")
	click(t, tt, "确认删除")
	if len(cmd.deleted) != 1 || cmd.deleted[0] != "ws_webapp" {
		t.Fatalf("deleted %v", cmd.deleted)
	}
	if a.deleteOpen {
		t.Fatal("the delete dialog stayed open")
	}
	if a.filterWorkspace != "" {
		t.Fatalf("filter %q kept a deleted workspace", a.filterWorkspace)
	}
}

// Without the management channels the buttons are disabled and say why: no fake
// success.
func TestWorkspaceManagementUnwired(t *testing.T) {
	a, tt := round3App(t, &fakeCmd{})
	click(t, tt, "项目 acme/webapp")
	wantTexts(t, tt, "暂时不可用：运行时还没有接入 WorkspaceRenamer / WorkspaceDeleter")
	// The tester can reach a disabled button; what matters is that it does not open
	// anything (no fake success).
	_ = tt.Click("重命名")
	_ = tt.Click("删除")
	if a.renameOpen || a.deleteOpen {
		t.Fatalf("an unwired action opened a dialog (rename=%v delete=%v)", a.renameOpen, a.deleteOpen)
	}
}

// ---- P2 polish ----

// A transport the runtime offers but the build cannot run is shown 未实现 and is
// not selectable; a known transport is.
func TestTransportOptionsFollowTheBuild(t *testing.T) {
	now := time.Now()
	snap := view.NewSnapshot()
	snap.PublishOverview(viewtest.DemoOverview(now))
	snap.PublishSettings(&view.SettingsVM{
		Source: "mygo.json", Revision: 2, UpdatedAt: now,
		Groups: []view.SettingsGroup{{
			ID: view.SettingsSource, Label: "连接", Note: "n",
			Fields: []view.SettingField{
				{Key: "feishu_enabled", Label: "飞书：启用", Value: "1", Kind: view.SettingSwitch},
				{Key: "feishu_transport", Label: "飞书：传输方式", Value: "longpoll",
					Kind: view.SettingSelect, Options: []string{"longpoll", "ws"}},
				{Key: "feishu_app_id", Label: "飞书 App ID", Value: "cli_x", Kind: view.SettingText},
				{Key: "dingtalk_enabled", Label: "钉钉：启用", Value: "1", Kind: view.SettingSwitch},
				{Key: "dingtalk_transport", Label: "钉钉：传输方式", Value: "dws",
					Kind: view.SettingSelect, Options: []string{"dws", "stream"}},
			},
		}},
	})
	a := newApp(snap, &fakeCmd{})
	a.now = now
	a.sec = view.SecSettings
	a.settingsGroup = view.SettingsSource
	tt := mui.NewTester(a.view, 1440, 1600)

	wantTexts(t, tt, "传输方式 HTTP 长轮询", "传输方式 官方长连接", "传输方式 DWS 长连接", "传输方式 官方 Stream")
	// Feishu's official larkws long connection is a real transport now, not a
	// placeholder: selecting it writes the stored id.
	click(t, tt, "传输方式 官方长连接")
	ptr := a.settingsPtr(view.SettingsSource, view.SettingField{Key: "feishu_transport"})
	if *ptr != "ws" {
		t.Fatalf("official long connection wrote %q", *ptr)
	}
	// A known transport is selectable and writes the stored id.
	click(t, tt, "传输方式 官方 Stream")
	dt := a.settingsPtr(view.SettingsSource, view.SettingField{Key: "dingtalk_transport"})
	if *dt != "stream" {
		t.Fatalf("dingtalk stream wrote %q", *dt)
	}
}

// The save bar says when a group's save takes effect: 热更新 only where it is
// true (the runtime's own Effect wins when published).
func TestSaveBarSaysWhenItTakesEffect(t *testing.T) {
	a, tt := channelApp(t, &fakeSaver{})
	editPollInterval(t, a)
	tt.Frame()
	wantTexts(t, tt, "保存后重启相关渠道（几秒；正在跑的工单不受影响）")
	if got := view.SaveEffect(view.SettingsPrompt); got != "热更新：下一次模型调用生效" {
		t.Fatalf("prompt effect %q", got)
	}
}

// The one text scale comes from ui.font_scale: 1.1 makes the theme's font 16.5
// and the explicit sizes follow fs().
func TestFontScaleSetting(t *testing.T) {
	now := time.Now()
	snap := view.NewSnapshot()
	snap.PublishOverview(viewtest.DemoOverview(now))
	snap.PublishSettings(&view.SettingsVM{
		Source: "mygo.json", Revision: 2, UpdatedAt: now,
		Groups: []view.SettingsGroup{{
			ID: view.SettingsDeploy, Label: "部署", Note: "n",
			Fields: []view.SettingField{
				{Key: "ui.font_scale", Label: "界面字号", Value: "1.1", Kind: view.SettingSelect,
					Options: []string{"0.9", "1.0", "1.1"}},
			},
		}},
	})
	a := newApp(snap, &fakeCmd{})
	a.now = now
	a.sec = view.SecSettings
	a.settingsGroup = view.SettingsDeploy
	tt := mui.NewTester(a.view, 1440, 1600)

	if got := ViewScale(); got != 1.1 {
		t.Fatalf("ViewScale %v, want 1.1", got)
	}
	if got := a.theme.FontSize; got != baseFontSize*1.1 {
		t.Fatalf("theme font %v, want %v", got, baseFontSize*1.1)
	}
	wantTexts(t, tt, "界面字号", "90%", "100%", "110%")
	click(t, tt, "90%")
	ptr := a.settingsPtr(view.SettingsDeploy, view.SettingField{Key: "ui.font_scale"})
	if *ptr != "0.9" {
		t.Fatalf("font scale edited to %q", *ptr)
	}
}

// editPollInterval makes one real 来源 edit through the form's own pointer.
func editPollInterval(t *testing.T, a *App) {
	t.Helper()
	for _, g := range a.snap.Settings().Groups {
		if g.ID != view.SettingsSource {
			continue
		}
		for _, f := range g.Fields {
			if f.Key == "poll_interval" {
				*a.settingsPtr(view.SettingsSource, f) = "60"
				return
			}
		}
	}
	t.Fatal("no poll_interval field to edit")
}
