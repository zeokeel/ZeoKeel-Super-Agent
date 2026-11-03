package runtime

import (
	"strings"
	"testing"
	"time"

	"github.com/zeokeel/super-agent/internal/domain"
	"github.com/zeokeel/super-agent/internal/engine"
	"github.com/zeokeel/super-agent/internal/view"
)

// One table, checked against the panel: every state a Ticket can be in must read
// the same in 总览 (needs a person?), 执行 (which bucket?), and the stage badge.
// If these disagree the panel is lying about what is happening.
func TestStageBucketAndOverviewAgree(t *testing.T) {
	type want struct {
		stage        domain.Stage
		bucket       view.TicketBucket
		needsAPerson bool
	}
	cases := []struct {
		name  string
		apply func(t *testing.T, e *engine.Engine, id domain.ID)
		want  want
	}{
		{
			name:  "just accepted, not started",
			apply: func(*testing.T, *engine.Engine, domain.ID) {},
			want:  want{domain.StageWorkingQueued, view.BucketPending, false},
		},
		{
			name: "running",
			apply: func(t *testing.T, e *engine.Engine, id domain.ID) {
				if err := e.StartAssignment(id); err != nil {
					t.Fatal(err)
				}
			},
			want: want{domain.StageWorking, view.BucketWorking, false},
		},
		{
			name: "reported ready",
			apply: func(t *testing.T, e *engine.Engine, id domain.ID) {
				if err := e.Report(id, domain.ReportReady, "写好了"); err != nil {
					t.Fatal(err)
				}
			},
			// Report(ready) ends a Ticket with no open follows (003:94); the
			// person/review row is for a ready report that still awaits a PR.
			want: want{domain.StageEnded, view.BucketEnded, false},
		},
		{
			name: "reported blocked",
			apply: func(t *testing.T, e *engine.Engine, id domain.ID) {
				if err := e.Report(id, domain.ReportBlocked, "等你给权限"); err != nil {
					t.Fatal(err)
				}
			},
			want: want{domain.StagePersonBlocked, view.BucketWaiting, true},
		},
		{
			name: "reported failed",
			apply: func(t *testing.T, e *engine.Engine, id domain.ID) {
				if err := e.Report(id, domain.ReportFailed, "测试没通过"); err != nil {
					t.Fatal(err)
				}
			},
			want: want{domain.StagePersonFailed, view.BucketWaiting, true},
		},
		{
			name: "agent asked a question",
			apply: func(t *testing.T, e *engine.Engine, id domain.ID) {
				if err := e.RaiseAttention(id, "配置项叫 session_timeout 还是 session.timeout？", nil); err != nil {
					t.Fatal(err)
				}
			},
			want: want{domain.StagePersonAnswer, view.BucketWaiting, true},
		},
		{
			name: "finished",
			apply: func(t *testing.T, e *engine.Engine, id domain.ID) {
				if err := e.Report(id, domain.ReportDropped, "重复提单"); err != nil {
					t.Fatal(err)
				}
			},
			want: want{domain.StageEnded, view.BucketEnded, false},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			eng := engine.New(engine.RuleAssessor{})
			eng.Register(domain.Workspace{
				ID: "ws_web", Name: "web", Root: "/tmp/web", Kind: domain.KindRepo,
				Brief: domain.Brief{Purpose: "webapp", Keywords: []string{"webapp"}},
			}, engine.Policy{Version: "v1"})
			if _, err := eng.Dispatch(engine.DispatchInput{Text: "在 webapp 里做点事", Workspace: "ws_web"}); err != nil {
				t.Fatal(err)
			}
			out := evalNow(eng)
			c.apply(t, eng, out.Ticket)

			rt := New(eng, view.NewSnapshot())
			rt.Publish()

			tk, ok := eng.TicketSnapshot(out.Ticket)
			if !ok {
				t.Fatal("ticket disappeared")
			}
			now := rt.clock()
			if got := domain.StageOf(&tk, now); got != c.want.stage {
				t.Fatalf("stage = %q, want %q", got, c.want.stage)
			}
			if got := view.BucketOf(string(domain.StageOf(&tk, now)), len(tk.Assignments) > 0); got != c.want.bucket {
				t.Fatalf("bucket = %q, want %q", got, c.want.bucket)
			}

			ov := rt.snap.Overview()
			listed := false
			for _, row := range ov.NeedsPerson {
				if row.ID == string(tk.ID) {
					listed = true
				}
			}
			if listed != c.want.needsAPerson {
				t.Fatalf("overview needs-a-person = %v, want %v (rows: %+v)",
					listed, c.want.needsAPerson, ov.NeedsPerson)
			}

			tickets := rt.ticketsVM(now)
			var row *view.TicketRow
			for i := range tickets.Rows {
				if tickets.Rows[i].ID == string(tk.ID) {
					row = &tickets.Rows[i]
				}
			}
			if row == nil {
				t.Fatal("the execution page does not list the ticket")
			}
			if row.Bucket != c.want.bucket {
				t.Fatalf("execution page bucket = %q, want %q", row.Bucket, c.want.bucket)
			}
			// The bucket counts must match the rows, or the chips mislead.
			for _, b := range tickets.Buckets {
				if b.Bucket == c.want.bucket && b.Count == 0 {
					t.Fatalf("bucket %q shows count 0 while listing a row", c.want.bucket)
				}
			}
		})
	}
}

// The runtime card must report the real executor budget, not a constant: a
// person who set 并发执行器 to 4 and reads "0/2" has been told something false.
func TestOverviewReportsTheRealExecutorBudget(t *testing.T) {
	rt := New(engine.New(engine.RuleAssessor{}), view.NewSnapshot())
	rt.EnableWorker(WorkerConfig{BaseURL: "http://x", Model: "m"})
	settleRuntime(t, rt)
	if _, err := rt.SaveSettings("execution", map[string]string{"concurrency": "4"}); err != nil {
		t.Fatal(err)
	}
	rt.Publish()
	ov := rt.snap.Overview()
	var found string
	for _, h := range ov.Health {
		if h.Name == "执行器" {
			found = h.Detail
		}
	}
	if found != "0/4 在跑" {
		t.Fatalf("executor health says %q, want 0/4", found)
	}
}

// The eval page must not print a raw code as if it were a sentence. This drives a
// real decision through the engine and checks the projection, not the template
// table (which has its own test).
func TestEvalPageRendersReasonText(t *testing.T) {
	eng := engine.New(engine.RuleAssessor{})
	eng.Register(domain.Workspace{
		ID: "ws_web", Name: "web", Root: "/tmp/web", Kind: domain.KindRepo,
		Brief: domain.Brief{Purpose: "webapp", Keywords: []string{"webapp"}},
	}, engine.Policy{Version: "v1"})
	if _, err := eng.Dispatch(engine.DispatchInput{
		Text: "在 webapp 里把登录超时改成可配置", Workspace: "ws_web",
	}); err != nil {
		t.Fatal(err)
	}
	out := evalNow(eng)
	if out.Ticket == "" {
		t.Fatalf("no ticket: %+v", out)
	}
	rt := New(eng, view.NewSnapshot())
	vm := rt.decisionsVM(timeNow())
	if len(vm.Rows) == 0 {
		t.Fatal("no decision was projected")
	}
	row := vm.Rows[0]
	if row.ReasonText == row.ReasonCode {
		t.Fatalf("reason text is the raw code %q", row.ReasonCode)
	}
	if row.ReasonText == "" {
		t.Fatal("the reason has no sentence")
	}
}

func timeNow() time.Time { return time.Now() }

// The 24h card must count what actually happened: a hardcoded zero for Evals meant
// the card disagreed with the eval page next to it.
func TestOverviewCountsAgreeWithTheEngine(t *testing.T) {
	eng := engine.New(engine.RuleAssessor{})
	eng.Register(domain.Workspace{
		ID: "ws_web", Name: "web", Root: "/tmp/web", Kind: domain.KindRepo,
		Brief: domain.Brief{Purpose: "webapp", Keywords: []string{"webapp"}},
	}, engine.Policy{Version: "v1"})
	if _, err := eng.Dispatch(engine.DispatchInput{Text: "在 webapp 里改点东西", Workspace: "ws_web"}); err != nil {
		t.Fatal(err)
	}
	evalNow(eng)
	rt := New(eng, view.NewSnapshot())
	rt.Publish()
	ov := rt.snap.Overview()
	if ov.Counts.Tickets != 1 || ov.Counts.Decisions != 1 {
		t.Fatalf("counts wrong: %+v", ov.Counts)
	}
	if ov.Counts.Evals == 0 {
		t.Fatalf("Evals is zero while a decision exists: %+v", ov.Counts)
	}
}

// Health must say the executor cannot run when no model is configured. A green
// card that hides this is worse than no card.
func TestHealthReportsAMissingModel(t *testing.T) {
	rt := New(engine.New(engine.RuleAssessor{}), view.NewSnapshot())
	rt.EnableWorker(WorkerConfig{Root: "/tmp/x"})
	settleRuntime(t, rt) // no model
	rt.Publish()
	found := false
	for _, h := range rt.snap.Overview().Health {
		if h.Name == "模型" {
			found = true
			if h.OK {
				t.Fatalf("health claims the model is fine: %+v", h)
			}
			if h.Detail == "" {
				t.Fatal("health must say what is wrong")
			}
		}
	}
	if !found {
		t.Fatal("no model health item")
	}

	// And it flips to OK once a model is set, without a restart.
	rt.SetWorkerModel("https://api.example.com/v1", "sk-x", "deepseek-chat")
	rt.Publish()
	for _, h := range rt.snap.Overview().Health {
		if h.Name == "模型" && !h.OK {
			t.Fatalf("health still says not ok: %+v", h)
		}
	}
}

// The overview row has to carry the reason, not just the category: "失败" with no
// explanation sends a person digging through logs to learn nothing new.
func TestNeedsPersonRowCarriesTheReason(t *testing.T) {
	eng := engine.New(engine.RuleAssessor{})
	eng.Register(domain.Workspace{
		ID: "ws_web", Name: "web", Root: "/tmp/web", Kind: domain.KindRepo,
		Brief: domain.Brief{Purpose: "webapp", Keywords: []string{"webapp"}},
	}, engine.Policy{Version: "v1"})
	if _, err := eng.Dispatch(engine.DispatchInput{Text: "在 webapp 里做点事", Workspace: "ws_web"}); err != nil {
		t.Fatal(err)
	}
	out := evalNow(eng)
	if err := eng.Report(out.Ticket, domain.ReportFailed,
		"模型调用失败：status code 500, upstream down"); err != nil {
		t.Fatal(err)
	}
	rt := New(eng, view.NewSnapshot())
	rt.Publish()
	var detail string
	for _, row := range rt.snap.Overview().NeedsPerson {
		if row.ID == string(out.Ticket) {
			detail = row.Detail
		}
	}
	if detail == "" {
		t.Fatal("the failed ticket is not in needs-a-person")
	}
	if !strings.Contains(detail, "500") {
		t.Fatalf("the reason is missing from the row: %q", detail)
	}
	if len([]rune(detail)) > 120 {
		t.Fatalf("the row is too long: %d runes", len([]rune(detail)))
	}
}
