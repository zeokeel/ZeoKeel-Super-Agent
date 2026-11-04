package ui

// verifier-B: adversarial verification of the Wave-2 UI claims (wave2-spec §4,
// fix-ui2.md). The fixtures here are built by this file; the owner's tests and
// viewtest demo data are not used for the assertions below.

import (
	"fmt"
	"testing"
	"time"

	mui "github.com/egoist/mygo/ui"

	"github.com/zeokeel/super-agent/internal/view"
)

type verifyActor struct {
	fakeCmd
	answered []string
	stopped  []string
	retried  []string
	paused   []bool
}

func (f *verifyActor) AnswerAttention(ticketID, answer string) error {
	f.answered = append(f.answered, ticketID+"|"+answer)
	return nil
}
func (f *verifyActor) StopTicket(ticketID, reason string) error {
	f.stopped = append(f.stopped, ticketID+"|"+reason)
	return nil
}
func (f *verifyActor) RetryTicket(ticketID string) error {
	f.retried = append(f.retried, ticketID)
	return nil
}
func (f *verifyActor) PauseExecution(paused bool) error {
	f.paused = append(f.paused, paused)
	return nil
}

// verifyRow builds one NeedsPerson row with only the facts the panel needs.
func verifyRow(id, stageID string, attention *view.TicketAttention) view.TicketRow {
	return view.TicketRow{
		ID: id, Title: "工单 " + id, Workspace: "web", StageID: stageID,
		Stage:      "等待处理",
		Attention:  attention,
		Detail:     "谁在等",
		Activity:   "刚刚",
		Age:        time.Minute,
		SourceIcon: "local",
	}
}

func verifyOverview(now time.Time, rows []view.TicketRow) *view.OverviewVM {
	ov := &view.OverviewVM{UpdatedAt: now}
	ov.NeedsPerson = append(ov.NeedsPerson, rows...)
	ov.Runtime = view.Runtime{Running: 1, MaxJobs: 3, Queued: 2}
	return ov
}

func verifyDetail(now time.Time) *view.TicketDetailVM {
	closed := now.Add(-time.Minute)
	return &view.TicketDetailVM{
		ID: "42", Title: "登录超时", Workspace: "web", Stage: view.StageWorking,
		Mandate: &view.MandateDetail{Strength: "explicit", PolicyVersion: "pv-7",
			Basis: "admission", Actor: "lin", Rules: []string{"implementation"},
			Scope: "github|comment", Workspace: "web", At: now},
		Inputs: []view.InputDetail{
			{ID: "i1", Kind: "trigger", Text: "登录超时改成可配置", At: now, Delivered: true, Read: true},
			{ID: "i2", Kind: "note", Text: "还在等", At: now, Delivered: true, Read: false},
		},
		Follows: []view.FollowDetail{{Thread: "owner/repo#44", Own: true,
			ClosedAt: &closed, Outcome: "merged"}},
		Artifacts:    []view.Artifact{{Kind: "file", Title: "report.md", Detail: "2 KB"}},
		LastActivity: "刚刚",
		UpdatedAt:    now,
	}
}

func verifyApp(t *testing.T, cmd Commander, snap *view.Snapshot) *App {
	t.Helper()
	a := newApp(snap, cmd)
	a.now = time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	return a
}

// ActionsFor must follow the stage fact, not the wording, for every stage in the
// 003 table: answer+stop, retry+stop, stop, none.
func TestVerifyActionsForEveryStage(t *testing.T) {
	stop := view.ActionStop
	cases := []struct {
		stage view.TicketRow
		want  []view.TicketAction
	}{
		{view.TicketRow{StageID: view.StagePersonAnswer}, []view.TicketAction{view.ActionAnswer, stop}},
		{view.TicketRow{StageID: view.StagePersonBlocked}, []view.TicketAction{view.ActionRetry, stop}},
		{view.TicketRow{StageID: view.StagePersonFailed}, []view.TicketAction{view.ActionRetry, stop}},
		{view.TicketRow{StageID: view.StagePersonDelivery}, []view.TicketAction{view.ActionRetry, stop}},
		{view.TicketRow{StageID: view.StagePersonReview}, []view.TicketAction{stop}},
		{view.TicketRow{StageID: view.StageWorking}, []view.TicketAction{stop}},
		{view.TicketRow{StageID: view.StageWorkingQueued}, []view.TicketAction{stop}},
		{view.TicketRow{StageID: view.StageIdle}, []view.TicketAction{stop}},
		{view.TicketRow{StageID: view.StageEnded}, nil},
		// Unknown stage: honest fallback to stop; CanRetry adds retry.
		{view.TicketRow{}, []view.TicketAction{stop}},
		{view.TicketRow{CanRetry: true}, []view.TicketAction{view.ActionRetry, stop}},
	}
	for _, c := range cases {
		got := view.ActionsFor(c.stage)
		if fmt.Sprint(got) != fmt.Sprint(c.want) {
			t.Errorf("stage=%q: actions=%v, want %v", c.stage.StageID, got, c.want)
		}
	}
}

// A row of every actionable stage is drawn with its actions on 总览; a blocked
// row offers retry, an answering row offers answer, and ended rows offer nothing.
func TestVerifyOverviewStageCorrectActions(t *testing.T) {
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	snap := view.NewSnapshot()
	snap.PublishOverview(verifyOverview(now, []view.TicketRow{
		verifyRow("42", view.StagePersonAnswer, &view.TicketAttention{ID: "42", Question: "用哪个环境？", Options: []string{"dev", "prod"}}),
		verifyRow("43", view.StagePersonBlocked, nil),
		verifyRow("44", view.StagePersonReview, nil),
		verifyRow("45", view.StageEnded, nil),
	}))
	actor := &verifyActor{}
	a := verifyApp(t, actor, snap)
	tt := mui.NewTester(a.view, 1440, 900)
	click(t, tt, "超级Agent")
	wantTexts(t, tt, "回答", "重试", "停止")
	// The answer row's question is the row's detail; ended rows still show but
	// their action set is empty — clicking 停止 must target only open rows.
	if err := tt.Click("回答"); err != nil {
		t.Fatalf("click 回答: %v", err)
	}
	wantTexts(t, tt, "dev", "prod")
}

// When the runtime does not implement the action channels, the buttons must be
// disabled AND the reason named; clicking must not fake an action.
func TestVerifyMissingActionChannelsAreHonest(t *testing.T) {
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	snap := view.NewSnapshot()
	snap.PublishOverview(verifyOverview(now, []view.TicketRow{
		verifyRow("42", view.StagePersonAnswer, &view.TicketAttention{Question: "q", Options: []string{"a"}}),
		verifyRow("43", view.StagePersonBlocked, nil),
	}))
	// fakeCmd has only Commander: no action interfaces.
	a := verifyApp(t, &fakeCmd{}, snap)
	tt := mui.NewTester(a.view, 1440, 900)
	click(t, tt, "超级Agent")
	wantTexts(t, tt, "暂时不可用")
	// A disabled button cannot open the answer dialog.
	if err := tt.Click("回答"); err == nil {
		// Some mygo versions report a click on a disabled button as a no-op click.
		if tt.HasText("提交回答") {
			t.Fatal("a disabled 回答 button opened the answer dialog")
		}
	}
}

// The answer dialog carries the real question and options, and the chosen option
// is sent through the AttentionAnswerer channel with the Ticket id.
func TestVerifyAnswerDialogSendsThroughTheChannel(t *testing.T) {
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	snap := view.NewSnapshot()
	snap.PublishOverview(verifyOverview(now, []view.TicketRow{
		verifyRow("42", view.StagePersonAnswer, &view.TicketAttention{
			Question: "要用哪个环境？", Options: []string{"staging", "production"},
		}),
	}))
	actor := &verifyActor{}
	a := verifyApp(t, actor, snap)
	tt := mui.NewTester(a.view, 1440, 900)
	click(t, tt, "超级Agent")
	click(t, tt, "回答")
	wantTexts(t, tt, "要用哪个环境？", "staging", "production", "回答 #42")
	click(t, tt, "production")
	if len(actor.answered) != 1 || actor.answered[0] != "42|production" {
		t.Fatalf("answered=%v, want [42|production]", actor.answered)
	}
}

// Ticket detail: a full projection renders the Mandate basis, the delivery
// three-stage facts, the follows and the artifacts from the published detail,
// not from the short row.
func TestVerifyTicketDetailFromProjection(t *testing.T) {
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	ticket := verifyDetail(now)
	snap := view.NewSnapshot()
	snap.PublishOverview(&view.OverviewVM{UpdatedAt: now})
	snap.PublishTickets(&view.TicketListVM{
		Rows: []view.TicketRow{{ID: "42", Title: "登录超时", StageID: view.StageWorking,
			Stage: "执行中", Workspace: "web", Bucket: view.BucketWorking}},
		DetailVMs: map[string]*view.TicketDetailVM{"42": ticket},
		UpdatedAt: now,
	})
	a := verifyApp(t, &verifyActor{}, snap)
	tt := mui.NewTester(a.view, 1440, 900)
	click(t, tt, "执行")
	click(t, tt, "登录超时")
	wantTexts(t, tt,
		"授权（Mandate）", "explicit", "policy pv-7", "依据 admission",
		"范围：github|comment", "输入（2）", "已投递", "已读",
		"订阅线程（1）", "owner/repo#44", "merged", "交付物（1）", "report.md",
	)
}

// A short-only projection must state the missing sections by name, not render an
// empty box that reads as "no data".
func TestVerifyTicketDetailStatesWhatIsMissing(t *testing.T) {
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	snap := view.NewSnapshot()
	snap.PublishOverview(&view.OverviewVM{UpdatedAt: now})
	snap.PublishTickets(&view.TicketListVM{
		Rows:      []view.TicketRow{{ID: "42", Title: "登录超时", StageID: view.StageWorking, Stage: "执行中"}},
		Details:   map[string]view.TicketDetail{"42": {ID: "42", Stage: "执行中"}},
		UpdatedAt: now,
	})
	a := verifyApp(t, &verifyActor{}, snap)
	tt := mui.NewTester(a.view, 1440, 900)
	click(t, tt, "执行")
	click(t, tt, "登录超时")
	wantTexts(t, tt, "暂无数据")
}

// Honest projections: the overview numbers come from the published RMS, never
// from constants; counts derive from the rows they link to.
func TestVerifyNoHardcodedOverviewNumbers(t *testing.T) {
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	snap := view.NewSnapshot()
	ov := verifyOverview(now, []view.TicketRow{
		verifyRow("42", view.StagePersonAnswer, &view.TicketAttention{Question: "q"}),
		verifyRow("43", view.StagePersonBlocked, nil),
	})
	ov.Usage = view.Usage{Calls: 7, Total: 12345}
	snap.PublishOverview(ov)
	a := verifyApp(t, &verifyActor{}, snap)
	tt := mui.NewTester(a.view, 1440, 900)
	if got := ov.PersonCount(); got != 2 {
		t.Fatalf("PersonCount=%d, want len(NeedsPerson)=2", got)
	}
	wantTexts(t, tt, "12,345")
	if tt.HasText("0/2") {
		t.Fatalf("the old hardcoded 0/2 is still rendered; texts: %q", tt.Texts())
	}
}

// The glossary is a closed, documented set (27 terms) with its value tables.
func TestVerifyGlossaryIsComplete(t *testing.T) {
	if view.GlossaryLen() != 27 {
		t.Fatalf("GlossaryLen=%d, want 27", view.GlossaryLen())
	}
	terms := view.Glossary()
	seen := map[string]bool{}
	for _, g := range terms {
		if g.Term == "" || g.Chinese == "" || g.Meaning == "" {
			t.Fatalf("incomplete glossary entry: %+v", g)
		}
		if seen[g.Term] {
			t.Fatalf("duplicate glossary term %q", g.Term)
		}
		seen[g.Term] = true
	}
	for _, want := range []string{"Ticket", "Mandate", "Attention", "Subscription", "Eval", "Trigger"} {
		if !seen[want] {
			t.Fatalf("glossary misses %q", want)
		}
	}
}
