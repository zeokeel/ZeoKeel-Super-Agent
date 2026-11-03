package ui

import (
	"testing"
	"time"

	mui "github.com/egoist/mygo/ui"

	"github.com/zeokeel/super-agent/internal/view"
)

// fakeActor is a runtime stand-in with the four action channels the panel probes
// for. It records what the panel sent, so a test can tell a sent action from a
// button that only looks enabled.
type fakeActor struct {
	fakeCmd
	answered []string
	stopped  []string
	retried  []string
	paused   []bool
	err      error
}

func (f *fakeActor) AnswerAttention(ticketID, answer string) error {
	if f.err != nil {
		return f.err
	}
	f.answered = append(f.answered, ticketID+"|"+answer)
	return nil
}

func (f *fakeActor) StopTicket(ticketID, reason string) error {
	if f.err != nil {
		return f.err
	}
	f.stopped = append(f.stopped, ticketID+"|"+reason)
	return nil
}

func (f *fakeActor) RetryTicket(ticketID string) error {
	if f.err != nil {
		return f.err
	}
	f.retried = append(f.retried, ticketID)
	return nil
}

func (f *fakeActor) PauseExecution(paused bool) error {
	if f.err != nil {
		return f.err
	}
	f.paused = append(f.paused, paused)
	return nil
}

// actionOverview is 总览 with one row per stage, so every action rule is on
// screen at once: answer, blocked (retry), review and working (stop), and an
// ended row with no action.
func actionOverview(now time.Time) *view.OverviewVM {
	return &view.OverviewVM{
		NeedsPerson: []view.TicketRow{
			{ID: "42", Title: "修改登录超时配置", Workspace: "acme/webapp", SourceIcon: "github",
				Stage: "等待回答", StageID: view.StagePersonAnswer, Detail: "等一个配置项名",
				Tone: view.ToneWarn, Age: 12 * time.Minute,
				Attention: &view.TicketAttention{
					ID:       "att-42",
					Question: "配置项名用 session_timeout 还是 session.timeout？",
					Options:  []string{"session_timeout", "session.timeout"},
					AskedAt:  now.Add(-12 * time.Minute),
				}},
			{ID: "44", Title: "CI 失败：auth 包测试超时", Workspace: "acme/api", SourceIcon: "github",
				Stage: "受阻", StageID: view.StagePersonBlocked, Detail: "等待授权重跑流水线",
				Tone: view.ToneDanger, Age: 34 * time.Minute, CanRetry: true},
			{ID: "45", Title: "为 session 配置补迁移脚本", Workspace: "acme/webapp", SourceIcon: "github",
				Stage: "等待评审", StageID: view.StagePersonReview, Detail: "PR 已就绪",
				Tone: view.ToneInfo, Age: 42 * time.Minute},
			{ID: "46", Title: "整理 acme 上月用量摘要", Workspace: "acme/data", SourceIcon: "local",
				Stage: "执行中", StageID: view.StageWorking, Detail: "bash 取数",
				Tone: view.ToneInfo, Age: 5 * time.Minute},
			{ID: "48", Title: "把登录超时改成可配置", Workspace: "acme/webapp", SourceIcon: "local",
				Stage: "已结束", StageID: view.StageEnded, Detail: "已合并",
				Tone: view.ToneNeutral, Age: 2 * time.Hour},
		},
		Runtime:   view.Runtime{Running: 1, MaxJobs: 2},
		UpdatedAt: now,
	}
}

// actionApp publishes 总览 and gives the window the given command object.
func actionApp(t *testing.T, cmd Commander) *App {
	t.Helper()
	now := time.Now()
	snap := view.NewSnapshot()
	snap.PublishOverview(actionOverview(now))
	a := newApp(snap, cmd)
	a.now = now
	return a
}

// Every stage offers its own action on 总览: 回答 for a question, 重试 for a
// blocked Ticket, 停止 for the rest, and nothing for an ended one.
func TestOverviewOffersStageCorrectActions(t *testing.T) {
	actor := &fakeActor{}
	a := actionApp(t, actor)
	tt := mui.NewTester(a.view, 1440, 900)
	wantTexts(t, tt, "回答", "重试", "停止")
	// An ended Ticket offers nothing: four open rows, so four 停止 buttons. A
	// button registers its label and its text, so each shows twice.
	if got := exactTexts(tt, "停止") / 2; got != 4 {
		t.Fatalf("%d 停止 buttons, want 4 (ended Ticket must offer none); texts: %q", got, tt.Texts())
	}
	if got := exactTexts(tt, "回答") / 2; got != 1 {
		t.Fatalf("%d 回答 buttons, want 1 (only person/answer); texts: %q", got, tt.Texts())
	}
}

// 回答 opens the question and its options; choosing one sends it through
// AttentionAnswerer.
func TestAnswerDialogSendsTheChosenOption(t *testing.T) {
	actor := &fakeActor{}
	a := actionApp(t, actor)
	tt := mui.NewTester(a.view, 1440, 900)

	click(t, tt, "回答")
	if a.answerRow == nil || a.answerRow.ID != "42" {
		t.Fatalf("the answer dialog opened for %+v, want #42", a.answerRow)
	}
	wantTexts(t, tt, "回答 #42", "配置项名用 session_timeout 还是 session.timeout？",
		"session_timeout", "session.timeout")

	click(t, tt, "session.timeout")
	if len(actor.answered) != 1 || actor.answered[0] != "42|session.timeout" {
		t.Fatalf("answered %v", actor.answered)
	}
	if a.answerRow != nil {
		t.Fatal("the dialog stayed open after a sent answer")
	}
	if tt.HasText("回答 #42") {
		t.Fatalf("the dialog is still drawn; texts: %q", tt.Texts())
	}
}

// A free-text answer goes through the same channel; the draft is trimmed and an
// empty one cannot be sent.
func TestAnswerDialogSendsFreeText(t *testing.T) {
	actor := &fakeActor{}
	a := actionApp(t, actor)
	tt := mui.NewTester(a.view, 1440, 900)

	click(t, tt, "回答")
	tt.Type("用 session_timeout，环境变量也一起改")
	tt.Frame()
	click(t, tt, "提交回答")
	if len(actor.answered) != 1 || actor.answered[0] != "42|用 session_timeout，环境变量也一起改" {
		t.Fatalf("answered %v", actor.answered)
	}
}

// 重试 sends the retry for the blocked row, through TicketRetrier.
func TestRetrySendsThroughTheChannel(t *testing.T) {
	actor := &fakeActor{}
	a := actionApp(t, actor)
	tt := mui.NewTester(a.view, 1440, 900)

	click(t, tt, "重试")
	if len(actor.retried) != 1 || actor.retried[0] != "44" {
		t.Fatalf("retried %v, want [44]", actor.retried)
	}
}

// 停止 asks first, and only the confirmation sends it — with the panel's reason,
// which names where the stop came from.
func TestStopAsksThenSends(t *testing.T) {
	actor := &fakeActor{}
	a := actionApp(t, actor)
	tt := mui.NewTester(a.view, 1440, 900)

	click(t, tt, "停止")
	if a.stopRow == nil || a.stopRow.ID != "42" {
		t.Fatalf("the confirmation opened for %+v, want #42", a.stopRow)
	}
	wantTexts(t, tt, "停止 #42？", "撤销它的 Mandate", "确认停止")
	if len(actor.stopped) != 0 {
		t.Fatalf("a stop was sent before the confirmation: %v", actor.stopped)
	}

	click(t, tt, "取消")
	if a.stopRow != nil || len(actor.stopped) != 0 {
		t.Fatalf("cancel sent %v (row %+v)", actor.stopped, a.stopRow)
	}

	click(t, tt, "停止")
	click(t, tt, "确认停止")
	if len(actor.stopped) != 1 || actor.stopped[0] != "42|"+stopReason {
		t.Fatalf("stopped %v", actor.stopped)
	}
}

// A runtime without the channels gets disabled actions and one honest line: no
// button silently does nothing.
func TestMissingActionChannelsAreDisabledWithAReason(t *testing.T) {
	a := actionApp(t, &fakeCmd{}) // no action interfaces
	tt := mui.NewTester(a.view, 1440, 900)

	wantTexts(t, tt, "动作未接线：运行时未提供",
		"回答（AttentionAnswerer）", "重试（TicketRetrier）", "停止（TicketStopper）")

	// The disabled 回答 does nothing: no dialog, no state.
	if err := tt.Click("回答"); err != nil {
		t.Fatalf("click 回答: %v", err)
	}
	if a.answerRow != nil {
		t.Fatalf("a disabled 回答 opened the dialog: %+v", a.answerRow)
	}
	if tt.HasText("回答 #42") {
		t.Fatalf("a disabled 回答 drew the dialog; texts: %q", tt.Texts())
	}
	for _, act := range []string{"重试", "停止"} {
		if err := tt.Click(act); err != nil {
			t.Fatalf("click %s: %v", act, err)
		}
	}
	if len(a.snap.Overview().NeedsPerson) != 5 {
		t.Fatal("a disabled action changed the overview")
	}
}

// A failed send keeps the runtime's error on screen instead of closing as if it
// had worked.
func TestActionErrorIsShown(t *testing.T) {
	actor := &fakeActor{err: errFake("引擎正忙")}
	a := actionApp(t, actor)
	tt := mui.NewTester(a.view, 1440, 900)

	click(t, tt, "重试")
	if a.actionErr != "引擎正忙" {
		t.Fatalf("action error %q", a.actionErr)
	}
	wantTexts(t, tt, "引擎正忙")

	click(t, tt, "回答")
	// An empty draft draws no 提交回答: there is nothing to send.
	if tt.HasText("提交回答") {
		t.Fatalf("an empty draft offers to submit; texts: %q", tt.Texts())
	}
	if len(actor.answered) != 0 {
		t.Fatalf("an empty answer was sent: %v", actor.answered)
	}
	tt.Type("就 session_timeout")
	tt.Frame()
	click(t, tt, "提交回答")
	if a.answerErr != "引擎正忙" {
		t.Fatalf("answer error %q, want the runtime's", a.answerErr)
	}
	wantTexts(t, tt, "引擎正忙")
}

type errFake string

func (e errFake) Error() string { return string(e) }

// Work-item actions are 总览's alone (design §6.3): 执行 draws no action buttons.
func TestActionsExistOnlyOnOverview(t *testing.T) {
	a := pageApp(t)
	tt := mui.NewTester(a.view, 1440, 900)
	click(t, tt, "执行")
	for _, action := range []string{"回答", "重试", "停止"} {
		if got := exactTexts(tt, action); got != 0 {
			t.Fatalf("执行 drew the %q action %d times; texts: %q", action, got, tt.Texts())
		}
	}
}
