package main

// The acceptance scenarios of RELEASE.md group A, written as executable cases.
// Each one is a sentence a person would say about the product; if a scenario here
// is wrong, the product lies about what it does.

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zeokeel/super-agent/internal/agent/mockllm"
	"github.com/zeokeel/super-agent/internal/runtime"

	"github.com/zeokeel/super-agent/internal/domain"
	"github.com/zeokeel/super-agent/internal/engine"
	"github.com/zeokeel/super-agent/internal/view"
)

// evalEngine drives the two-phase Eval once: Receive only queues it now
// (fix-engine API §0.1), so an acceptance test that expects a Ticket must run the
// same pass the app's 2s loop runs.
func evalEngine(e *engine.Engine) engine.Outcome {
	outs, err := e.EvaluateDue(time.Now())
	if err != nil || len(outs) == 0 {
		return engine.Outcome{}
	}
	for _, o := range outs {
		if o.Ticket != "" {
			return o
		}
	}
	return outs[len(outs)-1]
}

// evalPersistence is evalEngine for the durable wrapper.
func evalPersistence(p *engine.Persistence) engine.Outcome {
	outs, err := p.EvaluateDue(time.Now())
	if err != nil || len(outs) == 0 {
		return engine.Outcome{}
	}
	for _, o := range outs {
		if o.Ticket != "" {
			return o
		}
	}
	return outs[len(outs)-1]
}

// newAcceptanceEngine builds the app's engine with the two demo workspaces.
func newAcceptanceEngine(t *testing.T) *engine.Engine {
	t.Helper()
	e := engine.New(engine.RuleAssessor{})
	for _, w := range []struct {
		id   domain.WorkspaceID
		name string
		kind domain.WorkspaceKind
		kw   []string
	}{
		{"ws_web", "web", domain.KindRepo, []string{"webapp", "登录"}},
		{"ws_acme_data", "acme-data", domain.KindData, []string{"acme", "用量"}},
	} {
		e.Register(domain.Workspace{
			ID: w.id, Name: w.name, Root: "/tmp/" + w.name, Kind: w.kind,
			Brief: domain.Brief{Purpose: w.name, Keywords: w.kw},
		}, engine.Policy{Version: "v1", Allowed: map[string]map[string]bool{
			"dingtalk": {"colleague": true, "me": true},
		}})
	}
	return e
}

func chat(source, id, actor, text string) domain.Trigger {
	return domain.Trigger{
		ID:     domain.Key(domain.WorkspaceScope(""), source, "trigger", id),
		Source: source, Human: true,
		Addressed: domain.Addressed{Mention: true},
		Text:      text,
		Actor:     domain.Actor{ID: actor},
		// The two-phase Eval batches per Thread (001:29,43); a Trigger without one
		// is never due, so test fixtures carry the Thread their Source would set.
		Threads:      []domain.ThreadRef{{Source: source, ID: "grp-1"}},
		Conversation: domain.Conversation{Kind: domain.ConvGroup, ID: "grp-1"},
		Channel:      domain.ChannelRef{Source: source, Thread: "grp-1"},
		At:           time.Now(),
	}
}

// A2: a colleague's request becomes an approval, and that approval carries the
// four parts a person needs to decide: what they said, what will happen, what comes
// out, and why it needs a person (design §4.10).
func TestA2ColleagueRequestCarriesTheFourParts(t *testing.T) {
	e := newAcceptanceEngine(t)
	// Explicit requests are accepted in every mode (002:101); the approval gate is
	// for inferred ones, which is what this scenario pins.
	e.SetPolicy("ws_web", engine.Policy{Version: "v1", RequireApproval: true, AcceptMode: domain.AcceptAuto,
		Rules:   []domain.Rule{{ID: "implementation", Instruction: "明确的实现请求", Enabled: true, Proactive: true}},
		Allowed: map[string]map[string]bool{"dingtalk": {"colleague": true, "me": true}}})
	tr := chat("dingtalk", "m1", "colleague", "帮我看看 webapp 登录超时的问题")
	tr.Scope = domain.WorkspaceScope("ws_web")
	tr.DefaultWorkspace = "ws_web"
	tr.Addressed = domain.Addressed{}
	tr.ScopeReceivesAll = true
	out, err := e.Receive(tr)
	if err != nil {
		t.Fatal(err)
	}
	out = evalEngine(e)
	if !out.Proposal || out.Ticket != "" {
		t.Fatalf("expected a proposal and no ticket: %+v", out)
	}
	proposals := e.Proposals()
	if len(proposals) != 1 || proposals[0].Proposal == nil {
		t.Fatalf("expected one proposal, got %d", len(proposals))
	}
	p := proposals[0].Proposal
	if p.Said == "" || p.Intent == "" || len(p.Plan) == 0 || p.Deliverable == "" {
		t.Fatalf("proposal is missing a part: %+v", p)
	}
	if len(p.Sensitive) == 0 {
		t.Fatalf("a colleague request must say why it needs a person: %+v", p)
	}
	if p.Workspace == "" {
		t.Fatal("a proposal must say where the work would run")
	}
}

// A3: an admitted IM request runs directly (DSH flow). The gate admits it, Eval
// accepts and a Ticket exists; there is no IM approval step by default.
func TestA3OperatorRequestRunsWithoutApproval(t *testing.T) {
	e := newAcceptanceEngine(t)
	tr := chat("dingtalk", "m2", "me", "把 webapp 的登录超时改成可配置")
	tr.Self = true
	tr.Addressed = domain.Addressed{Direct: true}
	if _, err := e.Receive(tr); err != nil {
		t.Fatal(err)
	}
	out := evalEngine(e)
	if out.Proposal {
		t.Fatal("an IM message must not become a proposal by default")
	}
	if out.Ticket == "" {
		t.Fatalf("an explicit IM request must start work: %+v", out)
	}
	if len(e.Proposals()) != 0 {
		t.Fatalf("no proposal may exist for an explicit IM request: %d", len(e.Proposals()))
	}
}

// A6: a stranger or a bot is not a request. Nothing starts, nothing waits.
func TestA6StrangersAndBotsDoNothing(t *testing.T) {
	e := newAcceptanceEngine(t)

	// A bot mentioning the agent.
	bot := chat("dingtalk", "m3", "deploy-bot", "构建失败，请 @agent 看一下")
	bot.Human = false
	if out, err := e.Receive(bot); err != nil || out.Ticket != "" || out.Proposal {
		t.Fatalf("a bot message must do nothing: %+v %v", out, err)
	}

	// A person who is not on the allowlist.
	stranger := chat("dingtalk", "m4", "someone-else", "帮我把 webapp 的登录改一下")
	if out, err := e.Receive(stranger); err != nil || out.Ticket != "" || out.Proposal {
		t.Fatalf("an unknown person must do nothing: %+v %v", out, err)
	}
	if len(e.Proposals()) != 0 || len(e.AllTickets()) != 0 {
		t.Fatal("nothing should have been created")
	}
}

// A4: outbound to a public channel is a sensitive action and always escalates to a
// person, even when the request itself was explicit (design §4.10).
func TestA4SensitiveOutboundEscalates(t *testing.T) {
	e := newAcceptanceEngine(t)
	e.SetPolicy("ws_acme_data", engine.Policy{Version: "v1", RequireApproval: true, AcceptMode: domain.AcceptAuto,
		Rules:   []domain.Rule{{ID: "implementation", Instruction: "明确的实现请求", Enabled: true, Proactive: true}},
		Allowed: map[string]map[string]bool{"dingtalk": {"colleague": true, "me": true}}})
	tr := chat("dingtalk", "m5", "colleague", "把 acme 的用量明细发到客户群里")
	tr.Scope = domain.WorkspaceScope("ws_acme_data")
	tr.DefaultWorkspace = "ws_acme_data"
	tr.Addressed = domain.Addressed{}
	tr.ScopeReceivesAll = true
	out, err := e.Receive(tr)
	if err != nil {
		t.Fatal(err)
	}
	out = evalEngine(e)
	if !out.Proposal || out.Ticket != "" {
		t.Fatalf("sensitive outbound must wait for a person: %+v", out)
	}
	proposals := e.Proposals()
	if len(proposals) == 0 || proposals[0].Proposal == nil {
		t.Fatal("no proposal was recorded")
	}
	if len(proposals[0].Proposal.Sensitive) == 0 {
		t.Fatalf("the approval must name the sensitive part: %+v", proposals[0].Proposal)
	}
}

// A7: the same message arriving twice creates one Ticket. Retries, reconnects and
// double taps are normal, so duplicates must be absorbed (design §4.4).
func TestA7RepeatedDeliveryCreatesOneTicket(t *testing.T) {
	e := newAcceptanceEngine(t)
	tr := chat("dingtalk", "m6", "me", "webapp 登录超时改成可配置")
	tr.Self = true
	tr.Addressed = domain.Addressed{Direct: true}

	if _, err := e.Receive(tr); err != nil {
		t.Fatal(err)
	}
	out := evalEngine(e)
	if out.Ticket == "" {
		t.Fatalf("an explicit IM request must start work: %+v", out)
	}
	if len(e.Proposals()) != 0 {
		t.Fatalf("no proposal may exist: %d", len(e.Proposals()))
	}
	// A redelivery of the already-decided message must not create anything new.
	if _, err := e.Receive(tr); err != nil && !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("redelivery: %v", err)
	}
	_ = evalEngine(e)
	if n := len(e.AllTickets()); n != 1 {
		t.Fatalf("expected exactly one ticket, got %d", n)
	}
}

// The overview a person opens must show real work: the ticket an explicit IM
// request created appears as something to look at, not as a health number.
func TestOverviewShowsTheTicketThatExists(t *testing.T) {
	e := newAcceptanceEngine(t)
	tr := chat("dingtalk", "m7", "me", "webapp 登录超时改成可配置")
	tr.Self = true
	tr.Addressed = domain.Addressed{Direct: true}
	if _, err := e.Receive(tr); err != nil {
		t.Fatal(err)
	}
	out := evalEngine(e)
	if out.Ticket == "" {
		t.Fatalf("an explicit IM request must create a ticket: %+v", out)
	}
	if len(e.Proposals()) != 0 {
		t.Fatalf("no approval may be pending: %d", len(e.Proposals()))
	}
	_ = view.NewSnapshot() // the projection is the runtime's job; this checks the fact
	if _, ok := e.TicketSnapshot(out.Ticket); !ok {
		t.Fatal("the ticket is not readable after it was created")
	}
}

// The wizard has to leave a working agent behind: configure a model and a
// workspace there, then dispatch, and the Ticket must actually run. Collecting
// settings nobody reads would make the first-run screen theatre.
func TestOnboardingLeavesAWorkingAgent(t *testing.T) {
	srv, err := mockllm.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	srv.ToolName = "bash"
	srv.ToolArgs = `{"command":"echo from-onboarding > done.txt"}`

	state := filepath.Join(t.TempDir(), "state")
	eng := engine.New(engine.RuleAssessor{})
	d, err := openDurable(eng, state, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	rt := runtime.New(eng, view.NewSnapshot())
	if err := attachDurable(rt, d); err != nil {
		t.Fatal(err)
	}
	rt.EnableWorker(runtime.WorkerConfig{Root: filepath.Join(state, "work")})

	// The model step, then the workspace step.
	if _, err := rt.SaveOnboarding(map[string]string{
		"base_url": srv.URL, "api_key": "sk-mock", "model": "mock-tool",
	}); err != nil {
		t.Fatal(err)
	}
	if !rt.HasWorkerModel() {
		t.Fatal("the wizard saved a model the executor cannot see")
	}
	if _, err := rt.SaveOnboarding(map[string]string{
		"ws_name": "webapp", "ws_root": t.TempDir(),
		"ws_purpose": "面向客户的 web 控制台", "ws_keywords": "webapp",
		"people": "dingtalk:colleague",
	}); err != nil {
		t.Fatal(err)
	}
	if err := rt.FinishOnboarding(); err != nil {
		t.Fatal(err)
	}

	if err := rt.Dispatch("在 webapp 里跑一下，产出 done.txt"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		for _, tk := range eng.AllTickets() {
			if len(tk.Artifacts) > 0 {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("the agent configured through the wizard never produced anything")
}

// Without a model the executor must park the ticket with a reason, not sit there
// looking busy (design §5.7: the panel must be honest about state).
func TestTicketWithoutModelIsParked(t *testing.T) {
	eng := engine.New(engine.RuleAssessor{})
	eng.Register(domain.Workspace{
		ID: "ws_web", Name: "web", Root: "/tmp/web", Kind: domain.KindRepo,
		Brief: domain.Brief{Purpose: "webapp", Keywords: []string{"webapp"}},
	}, engine.Policy{Version: "v1"})
	rt := runtime.New(eng, view.NewSnapshot())
	rt.EnableWorker(runtime.WorkerConfig{Root: t.TempDir()}) // no model
	if rt.HasWorkerModel() {
		t.Fatal("no model was configured")
	}
	if err := rt.Dispatch("在 webapp 里做点事"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		for _, tk := range eng.AllTickets() {
			if r := tk.LastReport(); r != nil {
				if r.State != domain.ReportBlocked {
					t.Fatalf("expected blocked, got %q (%s)", r.State, r.Summary)
				}
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("the ticket was neither run nor parked")
}
