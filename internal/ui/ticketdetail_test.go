package ui

import (
	"testing"
	"time"

	mui "github.com/egoist/mygo/ui"

	"github.com/zeokeel/super-agent/internal/view"
	"github.com/zeokeel/super-agent/internal/view/viewtest"
)

// ticketDetailFixture is one Ticket with everything the projector can publish,
// so the rich detail is drawn from real fields rather than placeholders.
func ticketDetailFixture(now time.Time) *view.TicketDetailVM {
	return &view.TicketDetailVM{
		ID: "tk-42", Number: 42, Title: "修改登录超时配置", Workspace: "acme/webapp",
		ProjectRoot: "/tmp/project", ExecutionRoot: "/tmp/project/.zeokeel/work/ticket-42",
		Stage: view.StageWorking, StageLabel: "执行中", Tone: view.ToneInfo,
		Source: "GitHub · Issue #42", Thread: "th-42", Actor: "@lin",
		Detail:       "step 7 · 正在补 SessionTimeout 默认值",
		LastActivity: "12 分前 · edit internal/auth/session.go",
		Mandate: &view.MandateDetail{
			Strength: "explicit", PolicyVersion: "v7", Rules: []string{"issue-mentions-agent"},
			Actor: "@lin", Basis: "addressed", Scope: "acme/webapp", Workspace: "acme/webapp",
			At: now.Add(-4 * time.Hour),
		},
		Assignments: []view.AssignmentDetail{{
			ID: "a-42", Executor: "worker-42", State: "running",
			StartedAt: now.Add(-20 * time.Minute),
			Report: &view.ReportDetail{
				State: "progress", Summary: "补默认值", Detail: "edit internal/auth/session.go",
				At:        now.Add(-12 * time.Minute),
				Artifacts: []view.Artifact{{Kind: "pr", Title: "PR #128 草案", Detail: "+128 −36"}},
			},
		}},
		Inputs: []view.InputDetail{
			{ID: "in-1", Kind: "trigger", Text: "登录超时应可配置", At: now.Add(-4 * time.Hour),
				Delivered: true, Read: true},
			{ID: "in-2", Kind: "text", Text: "默认值别动", At: now.Add(-time.Hour), Delivered: true},
		},
		Follows:   []view.FollowDetail{{Thread: "th-128"}},
		Artifacts: []view.Artifact{{Kind: "pr", Title: "PR #128 草案", Detail: "+128 −36 · 9 files"}},
	}
}

// detailApp publishes the execution page with (or without) the full detail of #42.
func detailApp(t *testing.T, withFull bool) *App {
	t.Helper()
	now := time.Now()
	snap := view.NewSnapshot()
	snap.PublishOverview(viewtest.DemoOverview(now))
	vm := viewtest.DemoTickets(now)
	if withFull {
		vm.DetailVMs = map[string]*view.TicketDetailVM{"tk-42": ticketDetailFixture(now)}
	}
	snap.PublishTickets(vm)
	a := newApp(snap, &fakeCmd{})
	a.now = now
	a.sec = view.SecExecution
	return a
}

// A full projection is drawn as the Ticket's detail: Mandate, reports, Inputs,
// follows and artifacts, each from its own field.
func TestTicketDetailShowsTheFullProjection(t *testing.T) {
	a := detailApp(t, true)
	tt := mui.NewTester(a.view, 1440, 900)
	click(t, tt, "修改登录超时配置")

	wantTexts(t, tt,
		"授权（Mandate）", "explicit · policy v7", "命中规则", "issue-mentions-agent",
		"派工与报告（1）", "worker-42", "报告：progress · 补默认值",
		"输入（2）", "登录超时应可配置", "已读", "已投递", "默认值别动",
		"订阅线程（1）", "th-128",
		"交付物（1）", "PR #128 草案",
		"来源", "GitHub · Issue #42", "线程", "th-42", "发起人", "@lin",
	)
	// The one section the projection leaves empty is named, not hidden: Attention
	// is the only nil part of this fixture.
	if !tt.HasText("暂无数据：Attention") {
		t.Fatalf("the missing Attention section is not named; texts: %q", tt.Texts())
	}
	if tt.HasText("还没有数据完整详情") {
		t.Fatalf("a full projection still claims not to be projected; texts: %q", tt.Texts())
	}
}

// Without the full detail the short form is shown, plus one honest line naming
// what is missing — never empty boxes that read as "nothing happened".
func TestTicketDetailShortSaysWhatIsMissing(t *testing.T) {
	a := detailApp(t, false)
	tt := mui.NewTester(a.view, 1440, 900)
	click(t, tt, "修改登录超时配置")
	wantTexts(t, tt,
		"step 7 · 正在补 SessionTimeout 默认值", "PR #128 草案",
		"暂无数据：Mandate 依据 · Assignments / Reports · Inputs · Follows",
	)
}

// Opening a 总览 row is navigation: it lands on 执行 with that Ticket's row open,
// without sending anything.
func TestOverviewRowOpensTheTicketOnExecution(t *testing.T) {
	a := pageApp(t)
	tt := mui.NewTester(a.view, 1440, 900)
	if a.sec != view.SecOverview {
		t.Fatalf("home section %q", a.sec)
	}
	click(t, tt, "#42  修改登录超时配置")
	if a.sec != view.SecExecution {
		t.Fatalf("section %q, want execution", a.sec)
	}
	rows := ticketRowsOf(a)
	if a.ticketSel < 0 || a.ticketSel >= len(rows) || rows[a.ticketSel].ID != "tk-42" {
		t.Fatalf("opened row %d of %d, want #42", a.ticketSel, len(rows))
	}
	wantTexts(t, tt, "step 7 · 正在补 SessionTimeout 默认值")
}

// A Ticket the current projection does not hold does not open a random row: the
// click says why it could not go there.
func TestOpenTicketOutsideTheProjection(t *testing.T) {
	a := pageApp(t)
	a.openTicket("999")
	if a.sec != view.SecOverview {
		t.Fatalf("a failed open navigated to %q", a.sec)
	}
	if a.actionErr == "" {
		t.Fatal("a failed open said nothing")
	}
	wantTexts(t, mui.NewTester(a.view, 1440, 900), "不在当前的执行列表里")
}
