package ui

import (
	"testing"
	"time"

	mui "github.com/egoist/mygo/ui"

	"github.com/zeokeel/super-agent/internal/view"
	"github.com/zeokeel/super-agent/internal/view/viewtest"
)

// fakeSaver records a settings save. It embeds fakeCmd so the app still has a
// Commander, and adds the optional SettingsSaver the settings page looks for.
type fakeSaver struct {
	fakeCmd
	group  string
	values map[string]string
	rev    uint64
	err    error
}

func (f *fakeSaver) SaveSettings(group string, values map[string]string) (uint64, error) {
	f.group, f.values = group, values
	if f.err != nil {
		return 0, f.err
	}
	f.rev++
	return f.rev, nil
}

// pageApp publishes every page's projection, so one tester can walk the whole
// inspection screen the way a person would.
func pageApp(t *testing.T) *App {
	t.Helper()
	now := time.Now()
	snap := view.NewSnapshot()
	snap.PublishOverview(viewtest.DemoOverview(now))
	snap.PublishStream(demoStream())
	snap.PublishSources(viewtest.DemoSource(now))
	snap.PublishThreads(viewtest.DemoThreads(now))
	snap.PublishDecisions(viewtest.DemoDecisions(now))
	snap.PublishTickets(viewtest.DemoTickets(now))
	snap.PublishSettings(viewtest.DemoSettings(now))
	snap.PublishPrompts(viewtest.DemoPrompts(now))
	snap.PublishSchedules(viewtest.DemoSchedules(now))
	snap.PublishGoals(viewtest.DemoGoals(now))
	a := newApp(snap, &fakeCmd{})
	a.now = now
	return a
}

func click(t *testing.T, tt *mui.Tester, text string) {
	t.Helper()
	if err := tt.Click(text); err != nil {
		t.Fatalf("click %q: %v (texts: %q)", text, err, tt.Texts())
	}
}

func wantTexts(t *testing.T, tt *mui.Tester, want ...string) {
	t.Helper()
	for _, s := range want {
		if !tt.HasText(s) {
			t.Fatalf("missing %q; texts: %q", s, tt.Texts())
		}
	}
}

// 来源: every Source shows type, state, account facts, cursor and last poll;
// the failed one shows the actionable line, and configuration points to 设置
// instead of appearing inline (design §6.3).
func TestSourcePageShowsFactsCursorAndActionableFailure(t *testing.T) {
	a := pageApp(t)
	tt := mui.NewTester(a.view, 1440, 900)

	click(t, tt, "来源")
	wantTexts(t, tt,
		"每个事件源各管各的账号与游标；过滤在这里，Agent 之前",
		"GitHub", "@acme · acme/webapp", "已启用",
		"游标 已推进 · 上次同步 12 秒前",
		"上次轮询", "拉取 12 条 · 接收 12 · 丢弃 2",
		"24h 触发 42 · 丢弃 23",
		"机器人账号 / 自身回复", "被丢弃的 Trigger 仍保留在库里，可审计",
		"GitHub 剩余配额",
		"轮询失败", "99991663", "设置 › 来源",
	)
	if !tt.HasText("gh auth") && !tt.HasText("App Secret") {
		t.Fatalf("the failure must say what to do; texts: %q", tt.Texts())
	}

	// The read-only page links to the one place configuration lives.
	click(t, tt, "设置 › 来源")
	if a.sec != view.SecSettings || a.settingsGroup != view.SettingsSource {
		t.Fatalf("hint went to %q/%q, want settings/source", a.sec, a.settingsGroup)
	}
}

// 触发: Threads by recent activity on the left, the chosen Thread's timeline on
// the right; selecting another Thread swaps the timeline.
func TestTriggerPageSelectsThreadTimeline(t *testing.T) {
	a := pageApp(t)
	tt := mui.NewTester(a.view, 1440, 900)

	click(t, tt, "触发")
	// The first Thread is open by default, with its trigger, decision and the
	// reason code that produced it.
	wantTexts(t, tt,
		"线程", "按最近活动排序",
		"#42 修改登录超时配置", "Issue #42 · 修改登录超时配置",
		"命中工作区 acme/webapp 的关键词「登录」", "routed",
		"订阅只增加上下文，永不授权（Policy v7）",
	)

	// Choosing another row shows that Thread's own timeline.
	click(t, tt, "#44 auth 包测试超时")
	wantTexts(t, tt, "Issue #44 · auth 包测试超时", "失败日志含 timeout → 建单", "started_ticket")
	if tt.HasText("命中工作区 acme/webapp 的关键词「登录」") {
		t.Fatalf("the old timeline should be gone; texts: %q", tt.Texts())
	}
}

// 评估: every Decision shows its kind, reason code and the rule it hit; the
// kind filter narrows the list without a new publish.
func TestEvalPageFiltersByDecisionKind(t *testing.T) {
	a := pageApp(t)
	tt := mui.NewTester(a.view, 1440, 900)

	click(t, tt, "评估")
	wantTexts(t, tt,
		"每个 Decision 都带可检查的 reason 码与命中规则；不用自由文本猜",
		"#42 新建 Issue", "routed",
		"命中规则：Issue 含「可配置 / 默认值」→ ticket",
		"每日 CI 巡检", "Policy v7 · 只读",
	)

	click(t, tt, "需要批准 1")
	if !tt.HasText("PR #128 CI 失败") {
		t.Fatalf("the propose row should remain; texts: %q", tt.Texts())
	}
	if tt.HasText("每日 CI 巡检") {
		t.Fatalf("ignore rows should be filtered out; texts: %q", tt.Texts())
	}

	click(t, tt, "全部 6")
	if !tt.HasText("每日 CI 巡检") {
		t.Fatalf("clearing the filter should show every row; texts: %q", tt.Texts())
	}
}

// 执行: rows show number, title, workspace, stage and recent activity; a row
// opens in place (navigation, not an action), and the stage filter narrows it.
func TestExecutionPageFiltersAndOpensRowInPlace(t *testing.T) {
	a := pageApp(t)
	tt := mui.NewTester(a.view, 1440, 900)

	click(t, tt, "执行")
	wantTexts(t, tt,
		"#42", "修改登录超时配置", "acme/webapp", "执行中",
		"12 分前 · edit 工具调用", "点一行就地展开 · 动作在总览",
		"共 8 个任务 · 当前筛选 8 个 · 按最近活动排序",
	)

	// Opening a row shows why it waits and what authorized it, in place.
	click(t, tt, "修改登录超时配置")
	wantTexts(t, tt, "step 7 · 正在补 SessionTimeout 默认值", "explicit · policy v7", "PR #128 草案")

	// The stage filter is honest: its count is the published bucket count.
	click(t, tt, "已结束 1")
	if tt.HasText("修改登录超时配置") {
		t.Fatalf("working rows should be filtered out; texts: %q", tt.Texts())
	}
	wantTexts(t, tt, "把登录超时改成可配置", "已结束", "共 8 个任务 · 当前筛选 1 个 · 按最近活动排序")

	click(t, tt, "全部 8")
	if !tt.HasText("修改登录超时配置") {
		t.Fatalf("clearing the filter should show every row; texts: %q", tt.Texts())
	}
}

// 设置: the groups exist, 来源 and 执行 are real forms, the other groups keep
// their structure and say what is missing. The save bar appears only when
// something changed, and saving goes through the runtime (no optimistic update).
func TestSettingsPageFormsAndSaveBarOnlyWhenChanged(t *testing.T) {
	a := pageApp(t)
	saver := &fakeSaver{}
	a.cmd = saver
	tt := mui.NewTester(a.view, 1440, 900)

	click(t, tt, "设置")
	wantTexts(t, tt,
		"来自 mygo.json", "来源", "评估", "执行", "提示词", "部署",
		"轮询间隔（秒）", "GitHub Token", "飞书订阅群", "丢弃机器人消息",
		"每块只有一个保存条；能从默认值来的，就不做成设置项",
	)
	if tt.HasText("处未保存更改") {
		t.Fatalf("a clean group must show no save bar; texts: %q", tt.Texts())
	}

	// A real edit makes the group dirty, and only then the bar appears.
	g := a.snap.Settings().Group(view.SettingsSource)
	if g == nil {
		t.Fatal("demo settings has no source group")
	}
	var poll view.SettingField
	for _, f := range g.Fields {
		if f.Key == "poll_interval" {
			poll = f
		}
	}
	ptr := a.settingsPtr(view.SettingsSource, poll)
	*ptr = "30"
	tt.Frame()
	wantTexts(t, tt, "1 处未保存更改", "保存")

	click(t, tt, "保存")
	if saver.group != "source" || saver.values["poll_interval"] != "30" {
		t.Fatalf("saved %q %v", saver.group, saver.values)
	}
	if _, ok := saver.values[SettingsRevisionKey]; !ok {
		t.Fatalf("the save must carry the base revision: %v", saver.values)
	}
	// No optimistic update: the edit stays until the child publishes it.
	if a.settingsDirty(g) != 1 {
		t.Fatalf("dirty = %d after save, want 1 until the new snapshot arrives", a.settingsDirty(g))
	}

	// The new projection clears it.
	now := time.Now()
	next := viewtest.DemoSettings(now)
	for i := range next.Groups[0].Fields {
		if next.Groups[0].Fields[i].Key == "poll_interval" {
			next.Groups[0].Fields[i].Value = "30"
		}
	}
	next.Revision = saver.rev
	a.snap.PublishSettings(next)
	tt.Frame()
	fresh := a.snap.Settings().Group(view.SettingsSource)
	if fresh == nil {
		t.Fatal("the published settings lost the source group")
	}
	if a.settingsDirty(fresh) != 0 {
		t.Fatalf("dirty = %d after the new snapshot, want 0", a.settingsDirty(fresh))
	}
	if tt.HasText("处未保存更改") {
		t.Fatalf("the save bar should be gone; texts: %q", tt.Texts())
	}

	// 提示词 is a real editor now: its three layers, their revisions and the
	// default/overridden split, with the group's facts still below them.
	click(t, tt, "提示词")
	if a.settingsGroup != view.SettingsPrompt {
		t.Fatalf("rail chose %q, want prompt", a.settingsGroup)
	}
	wantTexts(t, tt,
		"Router 提示词", "Worker 提示词", "评估规则说明",
		"提示词 rev 7", "已覆盖", "默认", "编辑 Router",
		"提示词版本", "压缩触发预算",
	)
	if tt.HasText("处未保存更改") {
		t.Fatalf("a clean prompt group must show no save bar; texts: %q", tt.Texts())
	}
}

// A concurrent change in another window is detected, not silently overwritten:
// the save carries the revision the edits were based on (optimistic concurrency,
// design §7 single-writer store).
func TestSettingsSaveCarriesBaseRevision(t *testing.T) {
	a := pageApp(t)
	saver := &fakeSaver{}
	a.cmd = saver
	tt := mui.NewTester(a.view, 1440, 900)
	click(t, tt, "设置")

	g := a.snap.Settings().Group(view.SettingsSource)
	var poll view.SettingField
	for _, f := range g.Fields {
		if f.Key == "poll_interval" {
			poll = f
		}
	}
	*a.settingsPtr(view.SettingsSource, poll) = "30"
	tt.Frame()

	// Another window saves first: the published revision moves under our edits.
	next := viewtest.DemoSettings(time.Now())
	next.Revision = 9
	a.snap.PublishSettings(next)
	tt.Frame()
	wantTexts(t, tt, "设置已被其他窗口修改（rev 9）")

	click(t, tt, "保存")
	if got := saver.values[SettingsRevisionKey]; got != "7" {
		t.Fatalf("save carried revision %q, want the base 7", got)
	}
}

// A runtime that has not implemented SettingsSaver must be visible, not silent:
// the bar is disabled and says so, and a click explains instead of swallowing it.
func TestSettingsSaveBarSaysWhenUnwired(t *testing.T) {
	a := pageApp(t) // fakeCmd does not implement SettingsSaver
	tt := mui.NewTester(a.view, 1440, 900)
	click(t, tt, "设置")
	// 执行 is also a sidebar label, so choose the settings group in app state;
	// the rail's own click is covered by 提示词 in the test above.
	a.settingsGroup = view.SettingsExecution
	tt.Frame()

	g := a.snap.Settings().Group(view.SettingsExecution)
	var conc view.SettingField
	for _, f := range g.Fields {
		if f.Key == "concurrency" {
			conc = f
		}
	}
	*a.settingsPtr(view.SettingsExecution, conc) = "4"
	tt.Frame()

	wantTexts(t, tt, "1 处未保存更改", "当前运行时不能保存设置")
	click(t, tt, "保存")
	if !tt.HasText("当前运行时不能保存设置：没有保存通道。") {
		t.Fatalf("a disabled save must say why; texts: %q", tt.Texts())
	}
	if a.settingsDirty(g) != 1 {
		t.Fatal("an unwired save must not drop the edit")
	}
}

// The pages that are not the workbench load their own snapshot and say so
// honestly before the runtime publishes one.
func TestPagesSayWhenSnapshotIsMissing(t *testing.T) {
	snap := view.NewSnapshot()
	a := newApp(snap, &fakeCmd{})
	a.now = time.Now()
	for _, sec := range []view.Section{view.SecWorkspace, view.SecSource, view.SecTrigger, view.SecEval, view.SecExecution, view.SecSettings} {
		a.sec = sec
		tt := mui.NewTester(a.view, 1440, 900)
		if !tt.HasText("等待运行时发布") {
			t.Fatalf("%q did not say it waits for a snapshot; texts: %q", sec, tt.Texts())
		}
	}
}

// The filter helpers live in internal/view, so the page never sorts while
// drawing. This pins the count shown for each filter to the rows it draws.
func TestFilterCountsMatchRows(t *testing.T) {
	now := time.Now()
	tickets := viewtest.DemoTickets(now)
	for _, b := range tickets.Buckets {
		if got := len(view.FilterTickets(tickets.Rows, b.Bucket)); got != b.Count {
			t.Errorf("bucket %q counts %d, draws %d", b.Bucket, b.Count, got)
		}
	}
	if got, want := len(view.FilterTickets(tickets.Rows, "")), tickets.Total; got != want {
		t.Errorf("no filter draws %d rows, total says %d", got, want)
	}
	decisions := viewtest.DemoDecisions(now)
	for _, k := range decisions.Kinds {
		if got := len(view.FilterDecisions(decisions.Rows, k.Kind)); got != k.Count {
			t.Errorf("kind %q counts %d, draws %d", k.Kind, k.Count, got)
		}
	}
}
