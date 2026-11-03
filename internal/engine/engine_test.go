package engine

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zeokeel/super-agent/internal/domain"
)

// ---- helpers ----------------------------------------------------------------

func repoWS(id domain.WorkspaceID) domain.Workspace {
	return domain.Workspace{ID: id, Name: string(id), Root: "/tmp/" + string(id),
		Kind: domain.KindRepo, Brief: domain.Brief{Purpose: "web app", Keywords: []string{"webapp"}}}
}

func basePolicy() Policy {
	return Policy{
		Version: "v1",
		Rules: []domain.Rule{
			{ID: "implementation", Instruction: "要求改代码", Enabled: true, Proactive: true},
			{ID: "off-topic", Instruction: "闲聊", Enabled: true},
		},
		Allowed: map[string]map[string]bool{"github": {"lin": true}},
		Admissions: map[string]domain.AdmissionRow{
			"github|comment": {Source: "github", Scope: "comment", Observed: true,
				ListedUnaddressed:   domain.CellInferred,
				UnlistedAddressed:   domain.CellIgnore,
				UnlistedUnaddressed: domain.CellIgnore},
		},
		Bots:       map[string]map[string]bool{"github": {"dependabot[bot]": true}},
		AcceptMode: domain.AcceptAuto,
	}
}

func newTestEngine(p Policy) *Engine {
	e := New(RuleAssessor{})
	e.Register(repoWS("ws_web"), p)
	return e
}

func ghTrigger(id, actor, text string, human, addressed bool) domain.Trigger {
	return domain.Trigger{
		ID:               domain.Key(domain.Global, "github", id),
		Scope:            domain.Global,
		Source:           "github",
		Threads:          []domain.ThreadRef{{Source: "github", ID: "owner/repo#12"}},
		Actor:            domain.Actor{ID: actor, Bot: !human},
		Human:            human,
		Addressed:        domain.Addressed{Mention: addressed},
		Row:              "comment",
		ScopeReceivesAll: true,
		Text:             text,
		At:               time.Now(),
	}
}

// accept drives Receive + EvaluateDue until the Trigger produced a Ticket.
func accept(t *testing.T, e *Engine, tr domain.Trigger) domain.Ticket {
	t.Helper()
	if _, err := e.Receive(tr); err != nil && !errors.Is(err, ErrDuplicate) {
		t.Fatalf("receive: %v", err)
	}
	outs, err := e.EvaluateDue(time.Now())
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	for _, o := range outs {
		if o.Ticket != "" {
			if tk, ok := e.TicketSnapshot(o.Ticket); ok {
				return tk
			}
		}
	}
	t.Fatalf("no ticket: %+v", outs)
	return domain.Ticket{}
}

// finishTurn simulates the worker taking and reading the Ticket's Inputs.
func finishTurn(t *testing.T, e *Engine, ticketID domain.ID) {
	t.Helper()
	if err := e.MarkInputsDelivered(ticketID); err != nil {
		t.Fatal(err)
	}
	if err := e.MarkInputsRead(ticketID); err != nil {
		t.Fatal(err)
	}
}

func mustDecision(t *testing.T, e *Engine, triggerID domain.ID) domain.Decision {
	t.Helper()
	d, ok := e.Disposition(triggerID)
	if !ok {
		t.Fatal("no disposition")
	}
	if d.DecisionID == "" {
		return domain.Decision{}
	}
	dec, _ := e.Decision(d.DecisionID)
	return dec
}

// ---- Stage 1: deterministic gate --------------------------------------------

func TestGateStrengthCases(t *testing.T) {
	cases := []struct {
		name  string
		build func() (Policy, domain.Trigger)
		want  domain.Strength
	}{
		{"operator's own account", func() (Policy, domain.Trigger) {
			tr := ghTrigger("s1", "lin", "fix it", true, false)
			tr.Self = true
			return basePolicy(), tr
		}, domain.StrengthExplicit},
		{"listed and addressed", func() (Policy, domain.Trigger) {
			return basePolicy(), ghTrigger("s2", "lin", "fix it", true, true)
		}, domain.StrengthExplicit},
		{"listed, unaddressed, observed inferred cell", func() (Policy, domain.Trigger) {
			return basePolicy(), ghTrigger("s3", "lin", "maybe this?", true, false)
		}, domain.StrengthInferred},
		{"unlisted, addressed, ignore cell", func() (Policy, domain.Trigger) {
			return basePolicy(), ghTrigger("s4", "stranger", "fix it", true, true)
		}, domain.StrengthNotAdmitted},
		{"unlisted, addressed, explicit cell", func() (Policy, domain.Trigger) {
			p := basePolicy()
			row := p.Admissions["github|comment"]
			row.UnlistedAddressed = domain.CellExplicit
			p.Admissions["github|comment"] = row
			return p, ghTrigger("s5", "stranger", "fix it", true, true)
		}, domain.StrengthExplicit},
		{"unlisted and unaddressed is not admitted", func() (Policy, domain.Trigger) {
			return basePolicy(), ghTrigger("s6", "stranger", "hi all", true, false)
		}, domain.StrengthNotAdmitted},
		{"unobserved scope loses the unaddressed cells", func() (Policy, domain.Trigger) {
			p := basePolicy()
			row := p.Admissions["github|comment"]
			row.Observed = false
			p.Admissions["github|comment"] = row
			tr := ghTrigger("s7", "lin", "hi all", true, false)
			tr.ScopeReceivesAll = false
			return p, tr
		}, domain.StrengthNotAdmitted},
	}
	for _, c := range cases {
		p, tr := c.build()
		e := newTestEngine(p)
		if _, err := e.Receive(tr); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		d, _ := e.Disposition(tr.ID)
		if d.Strength != c.want {
			t.Errorf("%s: strength %q want %q (reasons %v)", c.name, d.Strength, c.want, d.Reason)
		}
	}
}

// The admission table's three cells, each with its possible values (002:63-69).
func TestAdmissionCells(t *testing.T) {
	cases := []struct {
		name      string
		configure func(*domain.AdmissionRow)
		actor     string
		addressed bool
		want      domain.Strength
	}{
		{"listed unaddressed inferred", func(r *domain.AdmissionRow) { r.ListedUnaddressed = domain.CellInferred }, "lin", false, domain.StrengthInferred},
		{"listed unaddressed ignore", func(r *domain.AdmissionRow) { r.ListedUnaddressed = domain.CellIgnore }, "lin", false, domain.StrengthNotAdmitted},
		{"unlisted addressed explicit", func(r *domain.AdmissionRow) { r.UnlistedAddressed = domain.CellExplicit }, "stranger", true, domain.StrengthExplicit},
		{"unlisted addressed inferred", func(r *domain.AdmissionRow) { r.UnlistedAddressed = domain.CellInferred }, "stranger", true, domain.StrengthInferred},
		{"unlisted addressed ignore", func(r *domain.AdmissionRow) { r.UnlistedAddressed = domain.CellIgnore }, "stranger", true, domain.StrengthNotAdmitted},
		{"unlisted unaddressed inferred", func(r *domain.AdmissionRow) { r.UnlistedUnaddressed = domain.CellInferred }, "stranger", false, domain.StrengthInferred},
		{"unlisted unaddressed ignore", func(r *domain.AdmissionRow) { r.UnlistedUnaddressed = domain.CellIgnore }, "stranger", false, domain.StrengthNotAdmitted},
	}
	for i, c := range cases {
		p := basePolicy()
		row := p.Admissions["github|comment"]
		c.configure(&row)
		p.Admissions["github|comment"] = row
		p.AcceptMode = domain.AcceptAuto
		e := newTestEngine(p)
		tr := ghTrigger("c"+string(rune('a'+i)), c.actor, "hello", true, c.addressed)
		if _, err := e.Receive(tr); err != nil {
			t.Fatal(err)
		}
		d, _ := e.Disposition(tr.ID)
		if d.Strength != c.want {
			t.Errorf("%s: strength %q want %q (%v)", c.name, d.Strength, c.want, d.Reason)
		}
	}
}

func TestBotList(t *testing.T) {
	// A listed bot that addresses the agent is an inferred candidate.
	e := newTestEngine(basePolicy())
	bot := ghTrigger("b1", "dependabot[bot]", "bump dep", false, true)
	if _, err := e.Receive(bot); err != nil {
		t.Fatal(err)
	}
	if d, _ := e.Disposition(bot.ID); d.Strength != domain.StrengthInferred {
		t.Fatalf("listed bot strength %q", d.Strength)
	}
	// An unlisted bot is not admitted at all (002:79).
	e2 := newTestEngine(basePolicy())
	other := ghTrigger("b2", "random[bot]", "hello", false, true)
	if _, err := e2.Receive(other); err != nil {
		t.Fatal(err)
	}
	if d, _ := e2.Disposition(other.ID); d.Strength != domain.StrengthNotAdmitted {
		t.Fatalf("unlisted bot must be refused, got %q", d.Strength)
	}
	// With accept mode off nothing inferred is admitted (002:97).
	p := basePolicy()
	p.AcceptMode = domain.AcceptOff
	e3 := newTestEngine(p)
	bot3 := ghTrigger("b3", "dependabot[bot]", "bump dep", false, true)
	_, _ = e3.Receive(bot3)
	if d, _ := e3.Disposition(bot3.ID); d.Strength != domain.StrengthNotAdmitted {
		t.Fatalf("off mode must refuse inferred bugs, got %q", d.Strength)
	}
}

// ---- Stage 1/2: accept mode and the two-phase Eval ---------------------------

func TestExplicitWorksInEveryMode(t *testing.T) {
	for _, mode := range []domain.AcceptMode{domain.AcceptOff, domain.AcceptObserve, domain.AcceptAuto} {
		p := basePolicy()
		p.AcceptMode = mode
		e := newTestEngine(p)
		tr := ghTrigger("e-"+string(mode), "lin", "webapp: change the timeout", true, true)
		tk := accept(t, e, tr)
		if tk.Mandate.Strength != domain.StrengthExplicit {
			t.Fatalf("%s: explicit request must start work, got strength %q", mode, tk.Mandate.Strength)
		}
	}
}

func TestInferredAcceptModes(t *testing.T) {
	// observe records would_accept and starts nothing.
	p := basePolicy()
	p.AcceptMode = domain.AcceptObserve
	row := p.Admissions["github|comment"]
	row.UnlistedAddressed = domain.CellInferred
	p.Admissions["github|comment"] = row
	e := newTestEngine(p)
	tr := ghTrigger("i1", "stranger", "webapp: add a button", true, true)
	if _, err := e.Receive(tr); err != nil {
		t.Fatal(err)
	}
	if e.QueueLen() != 1 {
		t.Fatal("receive must queue an Eval")
	}
	if _, ok := e.LastTicket(); ok {
		t.Fatal("receive must not create a ticket (design §2.1)")
	}
	outs, err := e.EvaluateDue(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range outs {
		if o.Ticket != "" {
			t.Fatal("observe mode must start nothing")
		}
	}
	dec := mustDecision(t, e, tr.ID)
	if dec.Kind != domain.DecIgnore || len(dec.Reasons) == 0 || dec.Reasons[0].Code != domain.ReasonObserved {
		t.Fatalf("observe must record would_accept, got %+v", dec)
	}
	// auto accepts when a rule matched.
	p2 := basePolicy()
	p2.Admissions["github|comment"] = domain.AdmissionRow{Source: "github", Scope: "comment", Observed: true,
		ListedUnaddressed: domain.CellInferred, UnlistedAddressed: domain.CellInferred, UnlistedUnaddressed: domain.CellIgnore}
	e2 := newTestEngine(p2)
	tk := accept(t, e2, ghTrigger("i2", "stranger", "webapp: add a button", true, true))
	if tk.Mandate.Strength != domain.StrengthInferred || len(tk.Mandate.Rules) == 0 {
		t.Fatalf("auto accept must record inferred strength and matched rules: %+v", tk.Mandate)
	}
}

// Accepting the same Trigger twice creates one Ticket (001:28).
func TestAcceptTwiceOneTicket(t *testing.T) {
	e := newTestEngine(basePolicy())
	tr := ghTrigger("d1", "lin", "webapp: change the timeout", true, true)
	tk := accept(t, e, tr)
	// A replay is a duplicate, not a second ticket.
	if _, err := e.Receive(tr); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("want ErrDuplicate, got %v", err)
	}
	all := e.AllTickets()
	if len(all) != 1 || all[0].ID != tk.ID {
		t.Fatalf("want exactly the one ticket, got %d", len(all))
	}
}

// A Trigger queued while an Eval runs merges into the next batch, not a second
// concurrent Eval on the same Thread (001:29,43, design §2.1).
func TestEvalMergesWhileRunning(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	e := New(blockingAssessor{started: started, release: release})
	e.Register(repoWS("ws_web"), basePolicy())

	tr1 := ghTrigger("m1", "lin", "webapp: first", true, true)
	if _, err := e.Receive(tr1); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if _, err := e.EvaluateDue(time.Now()); err != nil {
			t.Errorf("evaluate: %v", err)
		}
	}()
	<-started // the first Eval is inside the assessor
	// A second Trigger arrives while the model runs; Receive must not block on it.
	tr2 := ghTrigger("m2", "lin", "webapp: second", true, true)
	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, err := e.Receive(tr2); err != nil {
			t.Errorf("receive while evaluating: %v", err)
		}
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Receive blocked while an Eval held the engine")
	}
	close(release)
	wg.Wait()

	// The first batch produced one Ticket; the second Trigger is queued for the
	// Thread's next Eval, not lost.
	if len(e.AllTickets()) != 1 {
		t.Fatalf("want 1 ticket, got %d", len(e.AllTickets()))
	}
	if e.QueueLen() != 1 {
		t.Fatalf("the second Trigger must wait for the next Eval, queue=%d", e.QueueLen())
	}
}

type blockingAssessor struct {
	started chan struct{}
	release chan struct{}
}

func (b blockingAssessor) Assess(_ context.Context, in AssessInput) (AssessOutput, error) {
	if b.started != nil {
		select {
		case b.started <- struct{}{}:
		default:
		}
	}
	<-b.release
	return RuleAssessor{}.Assess(context.Background(), in)
}

// ---- Stage 2: timeout / unavailable / policy change -------------------------

func TestEvalExpiryAndUnavailable(t *testing.T) {
	e := newTestEngine(basePolicy())
	tr := ghTrigger("x1", "lin", "webapp: slow", true, true)
	if _, err := e.Receive(tr); err != nil {
		t.Fatal(err)
	}
	key := threadKeyOf(&tr)
	// Three expiries in a row: the first two queue the batch again, the third
	// records unavailable (002:149-151).
	for i := 0; i < 3; i++ {
		e.mu.Lock()
		slot := e.evals[key]
		slot.Eval.State = domain.EvalQueued
		slot.Eval.CandidateDelivered = true
		slot.Eval.Deadline = time.Now().Add(-time.Second)
		e.expireDueLocked(time.Now())
		state := e.evals[key].Eval.State
		e.mu.Unlock()
		if i < 2 && state != domain.EvalQueued {
			t.Fatalf("expiry %d must requeue, state=%q", i+1, state)
		}
	}
	e.mu.Lock()
	state := e.evals[key].Eval.State
	attempts := e.evals[key].Eval.Attempts
	e.mu.Unlock()
	if state != domain.EvalUnavailable || attempts < 3 {
		t.Fatalf("third expiry must record unavailable, state=%q attempts=%d", state, attempts)
	}
	var found bool
	for _, d := range e.Decisions() {
		if d.Kind == domain.DecUnavailable {
			found = true
		}
	}
	if !found {
		t.Fatal("no unavailable decision was recorded")
	}
}

func TestPolicyChangeRequeuesEval(t *testing.T) {
	e := newTestEngine(basePolicy())
	tr := ghTrigger("p1", "lin", "webapp: change timeout", true, true)
	if _, err := e.Receive(tr); err != nil {
		t.Fatal(err)
	}
	if err := e.PolicyChanged("v1", "v2"); err != nil {
		t.Fatal(err)
	}
	// The queued Eval is re-stamped with the new version and judged under it.
	if _, err := e.EvaluateDue(time.Now()); err != nil {
		t.Fatal(err)
	}
	if len(e.AllTickets()) != 1 {
		t.Fatalf("policy change must not drop the queued request, tickets=%d", len(e.AllTickets()))
	}
}

// ---- Stage 3: ticket lifecycle ---------------------------------------------

func TestTicketLifecycle(t *testing.T) {
	t.Run("ready without follows ends completed", func(t *testing.T) {
		e := newTestEngine(basePolicy())
		tk := accept(t, e, ghTrigger("l1", "lin", "webapp: change timeout", true, true))
		finishTurn(t, e, tk.ID)
		if err := e.Report(tk.ID, domain.ReportReady, "done"); err != nil {
			t.Fatal(err)
		}
		got, _ := e.TicketSnapshot(tk.ID)
		if got.Ended == nil || got.Ended.Outcome != domain.OutcomeCompleted {
			t.Fatalf("ready must end the ticket as completed, got %+v", got.Ended)
		}
	})

	t.Run("ready with follows waits for closes", func(t *testing.T) {
		e := newTestEngine(basePolicy())
		tk := accept(t, e, ghTrigger("l2", "lin", "webapp: change timeout", true, true))
		if err := e.BindFollow(tk.ID, "github|owner/repo#77", true); err != nil {
			t.Fatal(err)
		}
		finishTurn(t, e, tk.ID)
		if err := e.Report(tk.ID, domain.ReportReady, "PR open"); err != nil {
			t.Fatal(err)
		}
		if got, _ := e.TicketSnapshot(tk.ID); got.Ended != nil {
			t.Fatal("ready with an open pull request must not end the ticket")
		}
		if err := e.ThreadClosed("github|owner/repo#77", domain.OutcomeMerged, time.Now()); err != nil {
			t.Fatal(err)
		}
		got, _ := e.TicketSnapshot(tk.ID)
		if got.Ended == nil || got.Ended.Outcome != domain.OutcomeMerged {
			t.Fatalf("the close ends the ticket with the merged outcome, got %+v", got.Ended)
		}
	})

	t.Run("dropped ends", func(t *testing.T) {
		e := newTestEngine(basePolicy())
		tk := accept(t, e, ghTrigger("l3", "lin", "webapp: not reproducible", true, true))
		finishTurn(t, e, tk.ID)
		if err := e.Report(tk.ID, domain.ReportDropped, "no work"); err != nil {
			t.Fatal(err)
		}
		if got, _ := e.TicketSnapshot(tk.ID); got.Ended == nil || got.Ended.Outcome != domain.OutcomeDropped {
			t.Fatal("dropped must end the ticket")
		}
	})

	t.Run("stop withdraws the mandate and receives nothing", func(t *testing.T) {
		e := newTestEngine(basePolicy())
		tk := accept(t, e, ghTrigger("l4", "lin", "webapp: change timeout", true, true))
		if err := e.StopTicket(tk.ID, "person asked"); err != nil {
			t.Fatal(err)
		}
		got, _ := e.TicketSnapshot(tk.ID)
		if got.Ended == nil || got.Ended.Outcome != domain.OutcomeStopped {
			t.Fatal("stop must end the ticket")
		}
		if got.Mandate.Withdrawn == "" {
			t.Fatal("stop must withdraw the mandate")
		}
		// A later message reaches nothing (002:129), and is not owned.
		if _, err := e.Receive(ghTrigger("l4b", "lin", "status?", true, true)); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("retry starts a new assignment", func(t *testing.T) {
		e := newTestEngine(basePolicy())
		tk := accept(t, e, ghTrigger("l5", "lin", "webapp: change timeout", true, true))
		finishTurn(t, e, tk.ID)
		if err := e.Report(tk.ID, domain.ReportBlocked, "need access"); err != nil {
			t.Fatal(err)
		}
		if err := e.RetryTicket(tk.ID); err != nil {
			t.Fatal(err)
		}
		got, _ := e.TicketSnapshot(tk.ID)
		if got.LastAssignment().State != domain.AssignPending || len(got.Assignments) != 2 {
			t.Fatalf("retry must queue a new assignment: %+v", got.Assignments)
		}
	})

	t.Run("retry lifts the blocked stage", func(t *testing.T) {
		e := newTestEngine(basePolicy())
		tk := accept(t, e, ghTrigger("l5b", "lin", "webapp: change timeout", true, true))
		finishTurn(t, e, tk.ID)
		if err := e.Report(tk.ID, domain.ReportBlocked, "need access"); err != nil {
			t.Fatal(err)
		}
		if err := e.RetryTicket(tk.ID); err != nil {
			t.Fatal(err)
		}
		got, _ := e.TicketSnapshot(tk.ID)
		if st := domain.StageOf(&got, time.Now()); st != domain.StageWorkingQueued {
			t.Fatalf("after a retry the ticket waits in working/queued, got %q", st)
		}
	})

	t.Run("human message releases blocked", func(t *testing.T) {
		e := newTestEngine(basePolicy())
		tr := ghTrigger("l6", "lin", "webapp: change timeout", true, true)
		tk := accept(t, e, tr)
		finishTurn(t, e, tk.ID)
		if err := e.Report(tk.ID, domain.ReportBlocked, "need access"); err != nil {
			t.Fatal(err)
		}
		if got, _ := e.TicketSnapshot(tk.ID); domain.StageOf(&got, time.Now()) != domain.StagePersonBlocked {
			t.Fatal("blocked report must show person/blocked")
		}
		// The person answers on the Ticket's own Thread: the executor owns it, no
		// Eval runs, and the Ticket leaves person/blocked.
		if _, err := e.Receive(ghTrigger("l6b", "lin", "here is the token", true, true)); err != nil {
			t.Fatal(err)
		}
		if e.QueueLen() != 0 {
			t.Fatal("a message the executor owns must take no Eval")
		}
		if got, _ := e.TicketSnapshot(tk.ID); domain.StageOf(&got, time.Now()) == domain.StagePersonBlocked {
			t.Fatal("a human message must release the blocked stage")
		}
	})

	t.Run("answer attention queues the answer", func(t *testing.T) {
		e := newTestEngine(basePolicy())
		tk := accept(t, e, ghTrigger("l7", "lin", "webapp: change timeout", true, true))
		if err := e.RaiseAttention(tk.ID, "which env?", []string{"staging", "prod"}); err != nil {
			t.Fatal(err)
		}
		if got, _ := e.TicketSnapshot(tk.ID); domain.StageOf(&got, time.Now()) != domain.StagePersonAnswer {
			t.Fatal("attention must show person/answer")
		}
		if err := e.AnswerAttention(tk.ID, "staging"); err != nil {
			t.Fatal(err)
		}
		got, _ := e.TicketSnapshot(tk.ID)
		if got.Attention != nil {
			t.Fatal("answer must clear the attention")
		}
		var answered bool
		for _, in := range got.Inputs {
			if in.Kind == domain.InputText && in.Text == "staging" {
				answered = true
			}
		}
		if !answered {
			t.Fatal("the answer must become an Input for the worker")
		}
	})

	t.Run("end releases unread human messages to Eval", func(t *testing.T) {
		e := newTestEngine(basePolicy())
		tk := accept(t, e, ghTrigger("l8", "lin", "webapp: change timeout", true, true))
		// A person's message the worker never read arrives first.
		if _, err := e.Receive(ghTrigger("l8b", "lin", "also change the logo", true, true)); err != nil {
			t.Fatal(err)
		}
		before := mustDecision(t, e, ghTrigger("l8b", "lin", "also change the logo", true, true).ID)
		_ = before
		if e.QueueLen() != 0 {
			t.Fatal("the second message is owned while the ticket is live")
		}
		// Dropping the Ticket lets go of it: it must be queued for Eval again.
		if err := e.Report(tk.ID, domain.ReportDropped, "nothing to do"); err != nil {
			t.Fatal(err)
		}
		if e.QueueLen() != 1 {
			t.Fatalf("an unread human message must return to Eval, queue=%d", e.QueueLen())
		}
	})
}

// ---- Stage 4: subscriptions, ownership, revalidate -------------------------

func TestOwnershipAndResume(t *testing.T) {
	t.Run("fanned-out message is an Input and grants nothing", func(t *testing.T) {
		e := newTestEngine(basePolicy())
		tk := accept(t, e, ghTrigger("o1", "lin", "webapp: change timeout", true, true))
		// A bot's CI message on the thread fans out to the live Ticket, no Eval.
		ci := ghTrigger("o1b", "ci[bot]", "build failed", false, false)
		if _, err := e.Receive(ci); err != nil {
			t.Fatal(err)
		}
		if e.QueueLen() != 0 {
			t.Fatal("a bot message the gate refuses must not queue an Eval")
		}
		got, _ := e.TicketSnapshot(tk.ID)
		var found bool
		for _, in := range got.Inputs {
			if in.TriggerID == ci.ID {
				found = true
			}
		}
		if !found {
			t.Fatal("fan-out must deliver the message as an Input")
		}
	})

	t.Run("ended and not stopped resumes without an Eval", func(t *testing.T) {
		e := newTestEngine(basePolicy())
		tr := ghTrigger("r1", "lin", "webapp: change timeout", true, true)
		tk := accept(t, e, tr)
		finishTurn(t, e, tk.ID)
		if err := e.Report(tk.ID, domain.ReportReady, "done"); err != nil {
			t.Fatal(err)
		}
		// A person's message on the Ticket's own Thread resurrects it.
		if _, err := e.Receive(ghTrigger("r1b", "lin", "actually also the logo", true, true)); err != nil {
			t.Fatal(err)
		}
		if e.QueueLen() != 0 {
			t.Fatal("resume must not take an Eval")
		}
		got, _ := e.TicketSnapshot(tk.ID)
		if got.Ended != nil {
			t.Fatal("the ended ticket must be open again")
		}
		if got.LastAssignment().State != domain.AssignPending {
			t.Fatal("resume must queue a new assignment")
		}
	})

	t.Run("stopped ticket is not resumed", func(t *testing.T) {
		e := newTestEngine(basePolicy())
		tk := accept(t, e, ghTrigger("r2", "lin", "webapp: change timeout", true, true))
		if err := e.StopTicket(tk.ID, "no"); err != nil {
			t.Fatal(err)
		}
		if _, err := e.Receive(ghTrigger("r2b", "lin", "please continue", true, true)); err != nil {
			t.Fatal(err)
		}
		if got, _ := e.TicketSnapshot(tk.ID); got.Ended == nil {
			t.Fatal("a stopped ticket must stay ended")
		}
	})

	t.Run("weakened policy revokes instead of resuming", func(t *testing.T) {
		p := basePolicy()
		p.Admissions["github|comment"] = domain.AdmissionRow{Source: "github", Scope: "comment", Observed: true,
			UnlistedAddressed: domain.CellInferred}
		e := newTestEngine(p)
		tk := accept(t, e, ghTrigger("r3", "stranger", "webapp: change timeout", true, true))
		if tk.Mandate.Strength != domain.StrengthInferred {
			t.Fatalf("want inferred mandate, got %q", tk.Mandate.Strength)
		}
		if err := e.Report(tk.ID, domain.ReportReady, "done"); err != nil {
			t.Fatal(err)
		}
		// Weaken the policy to off mode: the Mandate no longer holds.
		p2 := p
		p2.Version = "v2"
		p2.AcceptMode = domain.AcceptOff
		if err := e.SetPolicy("ws_web", p2); err != nil {
			t.Fatal(err)
		}
		if _, err := e.Receive(ghTrigger("r3b", "stranger", "please continue", true, true)); err != nil {
			t.Fatal(err)
		}
		got, _ := e.TicketSnapshot(tk.ID)
		if got.Ended == nil {
			t.Fatal("a revoked mandate must leave the ticket ended")
		}
		if got.Mandate.Revoked == "" {
			t.Fatal("the revalidation failure must be recorded on the mandate")
		}
	})
}

// A person can drop a Subscription; the source thread stays. An executor's
// handback of an owned message returns it to Eval (001:37, 002:30).
func TestUnsubscribeAndHandBack(t *testing.T) {
	e := newTestEngine(basePolicy())
	tk := accept(t, e, ghTrigger("u1", "lin", "webapp: change timeout", true, true))
	// A person's message on the ticket's own thread is owned: no Eval.
	if _, err := e.Receive(ghTrigger("u1b", "lin", "also the logo", true, true)); err != nil {
		t.Fatal(err)
	}
	if e.QueueLen() != 0 {
		t.Fatal("the follow-up belongs to the executor while the ticket is live")
	}
	// The executor hands it back: it returns to Eval, and the Ticket keeps its
	// Subscription (002:30).
	if err := e.HandBack(tk.ID, "global|github|owner/repo#12", "out of scope"); err != nil {
		t.Fatal(err)
	}
	if e.QueueLen() != 1 {
		t.Fatalf("a handed-back message must return to Eval, queue=%d", e.QueueLen())
	}
	// A person can drop a context subscription.
	if err := e.BindFollow(tk.ID, "github|owner/repo#9", false); err != nil {
		t.Fatal(err)
	}
	if err := e.DropSubscription(tk.ID, "github|owner/repo#9", "person"); err != nil {
		t.Fatal(err)
	}
	got, _ := e.TicketSnapshot(tk.ID)
	if containsString(got.SubscribedThreads, "github|owner/repo#9") {
		t.Fatal("the subscription must be dropped")
	}
	// The source thread cannot be dropped by a MarkUnrelated.
	if err := e.MarkUnrelated(tk.ID, "global|github|owner/repo#12", "no"); err == nil {
		t.Fatal("the source thread must not be droppable")
	}
}

func ghTriggerThread(id, actor, text, thread string) domain.Trigger {
	tr := ghTrigger(id, actor, text, true, true)
	tr.Threads = []domain.ThreadRef{{Source: "github", ID: thread}}
	return tr
}

// ---- pause / stop / queue len / snapshot race ------------------------------

func TestPauseHoldsAndResumes(t *testing.T) {
	e := newTestEngine(basePolicy())
	tr := ghTrigger("pa1", "lin", "webapp: change timeout", true, true)
	if _, err := e.Receive(tr); err != nil {
		t.Fatal(err)
	}
	e.Pause()
	outs, err := e.EvaluateDue(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(outs) != 0 {
		t.Fatal("a paused engine must not judge candidates")
	}
	if len(e.AllTickets()) != 0 {
		t.Fatal("a paused engine must not create tickets")
	}
	e.Resume()
	if _, err := e.EvaluateDue(time.Now()); err != nil {
		t.Fatal(err)
	}
	if len(e.AllTickets()) != 1 {
		t.Fatal("after resume the queued request must be judged")
	}
}

// TicketSnapshot copies state so a reader never aliases the writer (run with
// -race to catch regressions).
func TestTicketSnapshotIsACopy(t *testing.T) {
	e := newTestEngine(basePolicy())
	tk := accept(t, e, ghTrigger("sn1", "lin", "webapp: change timeout", true, true))
	snap, ok := e.TicketSnapshot(tk.ID)
	if !ok {
		t.Fatal("no snapshot")
	}
	snap.Title = "mutated by the reader"
	again, _ := e.TicketSnapshot(tk.ID)
	if again.Title == "mutated by the reader" {
		t.Fatal("the snapshot must not alias engine state")
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			_ = e.Report(tk.ID, domain.ReportProgress, "working")
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			_, _ = e.TicketSnapshot(tk.ID)
			_ = e.AllTickets()
		}
	}()
	wg.Wait()
}

// ---- Wave 2 verifier regressions (engine defects fixed) --------------------

// HandBack must not queue a fresh Eval while another live Ticket still owns the
// message (002:30).
func TestRegressionHandBackKeepsEvalWhenAnotherOwner(t *testing.T) {
	e := newTestEngine(basePolicy())
	first := accept(t, e, ghTrigger("hb1", "lin", "webapp: one", true, true))
	second := accept(t, e, ghTrigger("hb2", "dependabot[bot]", "webapp: two", false, true))
	if first.ID == second.ID {
		t.Fatal("fixture: want two tickets")
	}
	owned := ghTrigger("hb3", "lin", "please also fix the footer", true, true)
	if out, err := e.Receive(owned); err != nil || out.Kind != domain.DispOwned {
		t.Fatalf("fixture: want an owned message, got %v %v", out.Kind, err)
	}
	if err := e.HandBack(first.ID, "global|github|owner/repo#12", "out of scope"); err != nil {
		t.Fatal(err)
	}
	if e.QueueLen() != 0 {
		t.Fatalf("a handback queued an Eval while %s still owns the message", second.ID)
	}
}

// A failed revalidation returns the waiting message to Eval (002:141).
func TestRegressionRevalidationFailureQueuesEval(t *testing.T) {
	p := basePolicy()
	row := p.Admissions["github|comment"]
	row.UnlistedAddressed = domain.CellInferred
	p.Admissions["github|comment"] = row
	e := newTestEngine(p)
	tk := accept(t, e, ghTrigger("rv1", "stranger", "webapp: change the timeout", true, true))
	finishTurn(t, e, tk.ID)
	if err := e.Report(tk.ID, domain.ReportReady, "done"); err != nil {
		t.Fatal(err)
	}
	weakened := p
	weakened.Version = "v2"
	weakened.AcceptMode = domain.AcceptOff
	if err := e.SetPolicy("ws_web", weakened); err != nil {
		t.Fatal(err)
	}
	out, err := e.Receive(ghTrigger("rv1b", "stranger", "please continue", true, true))
	if err != nil {
		t.Fatal(err)
	}
	if out.Kind == domain.DispResumed {
		t.Fatal("a failed revalidation must not report a resume")
	}
	got, _ := e.TicketSnapshot(tk.ID)
	if got.Mandate.Revoked == "" {
		t.Fatal("the failed revalidation must be recorded on the mandate")
	}
	if e.QueueLen() != 1 {
		t.Fatalf("the message must go to Eval, queue=%d", e.QueueLen())
	}
}

// TicketSnapshot must deep-copy nested pointers: Follow.ClosedAt is the one that
// a panel read could otherwise rewrite.
func TestRegressionSnapshotCopiesFollowCloseTime(t *testing.T) {
	e := newTestEngine(basePolicy())
	tk := accept(t, e, ghTrigger("sn1", "lin", "webapp: change the timeout", true, true))
	if err := e.BindFollow(tk.ID, "github|owner/repo#77", true); err != nil {
		t.Fatal(err)
	}
	if err := e.ThreadClosed("github|owner/repo#77", domain.OutcomeMerged, time.Now()); err != nil {
		t.Fatal(err)
	}
	snap, _ := e.TicketSnapshot(tk.ID)
	if len(snap.Follows) == 0 || snap.Follows[0].ClosedAt == nil {
		t.Fatalf("fixture: follow not closed: %+v", snap.Follows)
	}
	*snap.Follows[0].ClosedAt = snap.Follows[0].ClosedAt.Add(time.Hour)
	again, _ := e.TicketSnapshot(tk.ID)
	if again.Follows[0].ClosedAt.Equal(*snap.Follows[0].ClosedAt) {
		t.Fatal("snapshot aliases Follow.ClosedAt")
	}
}

// Feed must hand out copies, not internal Ticket pointers.
func TestRegressionFeedDoesNotAlias(t *testing.T) {
	e := newTestEngine(basePolicy())
	tk := accept(t, e, ghTrigger("fd1", "lin", "webapp: change the timeout", true, true))
	if _, err := e.Receive(ghTrigger("fd2", "ci[bot]", "build failed", false, false)); err != nil {
		t.Fatal(err)
	}
	var got *domain.Ticket
	for _, it := range e.Feed() {
		if it.Ticket != nil {
			got = it.Ticket
		}
	}
	if got == nil {
		t.Fatal("fixture: Feed attached no Ticket")
	}
	got.Title = "mutated"
	snap, _ := e.TicketSnapshot(tk.ID)
	if snap.Title == "mutated" {
		t.Fatal("Feed aliases engine state")
	}
}

// A merged policy must not depend on Go's map iteration order.
func TestRegressionMergedPolicyDeterministic(t *testing.T) {
	pa := basePolicy()
	rowA := pa.Admissions["github|comment"]
	rowA.UnlistedAddressed = domain.CellInferred
	pa.Admissions["github|comment"] = rowA
	pb := basePolicy()
	rowB := pb.Admissions["github|comment"]
	rowB.UnlistedAddressed = domain.CellExplicit
	pb.Admissions["github|comment"] = rowB
	e := New(RuleAssessor{})
	e.Register(repoWS("ws_a"), pa)
	e.Register(repoWS("ws_b"), pb)
	tr := ghTrigger("mp1", "stranger", "hello", true, true)
	first := domain.Strength("")
	for i := 0; i < 50; i++ {
		e.mu.Lock()
		p, _ := e.policyForLocked(&tr)
		g := gate(p, tr, false)
		e.mu.Unlock()
		if i == 0 {
			first = g.Strength
			continue
		}
		if g.Strength != first {
			t.Fatalf("merged policy changed between calls: %q then %q", first, g.Strength)
		}
	}
}

// InterruptAssignment turns a dead running Assignment into history and leaves one
// pending Assignment plus a recovery note (003:146).
func TestInterruptAssignment(t *testing.T) {
	e := newTestEngine(basePolicy())
	tk := accept(t, e, ghTrigger("ia1", "lin", "webapp: change the timeout", true, true))
	if err := e.StartAssignment(tk.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.InterruptAssignment(tk.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := e.TicketSnapshot(tk.ID)
	if len(got.Assignments) != 2 {
		t.Fatalf("want an old and a new assignment, got %d", len(got.Assignments))
	}
	old := got.Assignments[0]
	if old.State != domain.AssignEnded || old.EndReason != domain.ReasonHostRestarted {
		t.Fatalf("the interrupted assignment must end as hostRestarted: %+v", old)
	}
	if got.LastAssignment().State != domain.AssignPending {
		t.Fatalf("a new pending assignment is required: %+v", got.LastAssignment())
	}
	var note bool
	for _, in := range got.Inputs {
		if in.Kind == domain.InputNote && strings.Contains(in.Text, "host restarted") {
			note = true
		}
	}
	if !note {
		t.Fatal("the recovery note must be queued as an Input")
	}
}

// A merged policy is conservative: the stricter workspace wins regardless of the
// order the workspaces were registered in. Authorization must never be widened by
// a merge (§4.5 / 002).
func TestConservativeMergeRegardlessOfOrder(t *testing.T) {
	mk := func(listedAddressed domain.AdmissionCell) Policy {
		p := basePolicy()
		row := p.Admissions["github|comment"]
		row.UnlistedAddressed = listedAddressed
		p.Admissions["github|comment"] = row
		return p
	}
	permissive := mk(domain.CellExplicit)
	restrictive := mk(domain.CellIgnore)
	tr := ghTrigger("cm1", "stranger", "hello", true, true)

	for _, order := range []struct {
		name string
		a, b Policy
		ida  domain.WorkspaceID
	}{
		{"restrictive first", restrictive, permissive, "ws_a"},
		{"restrictive second", permissive, restrictive, "ws_b"},
	} {
		e := New(RuleAssessor{})
		e.Register(repoWS("ws_a"), order.a)
		e.Register(repoWS("ws_b"), order.b)
		e.mu.Lock()
		p, _ := e.policyForLocked(&tr)
		g := gate(p, tr, false)
		e.mu.Unlock()
		if g.Admitted {
			t.Fatalf("%s: the restrictive cell must win, merged admitted (%q, %v)", order.name, g.Strength, g.Reasons)
		}
	}
	// The stricter accept mode wins too (off beats auto).
	mkMode := func(mode domain.AcceptMode) Policy {
		p := basePolicy()
		row := p.Admissions["github|comment"]
		row.UnlistedAddressed = domain.CellInferred
		p.Admissions["github|comment"] = row
		p.AcceptMode = mode
		return p
	}
	e := New(RuleAssessor{})
	e.Register(repoWS("ws_a"), mkMode(domain.AcceptAuto))
	e.Register(repoWS("ws_b"), mkMode(domain.AcceptOff))
	e.mu.Lock()
	p, _ := e.policyForLocked(&tr)
	mode := acceptMode(p)
	e.mu.Unlock()
	if mode != domain.AcceptOff {
		t.Fatalf("the stricter accept mode must win, got %q", mode)
	}
}

// AllTickets must deep-copy nested state: a reader that touches a Report while a
// worker writes one is a real -race in the panel projector without this.
func TestAllTicketsDeepCopyUnderRace(t *testing.T) {
	e := newTestEngine(basePolicy())
	tk := accept(t, e, ghTrigger("at1", "lin", "webapp: change the timeout", true, true))
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		defer close(stop)
		for i := 0; i < 300; i++ {
			_ = e.Report(tk.ID, domain.ReportProgress, "progress")
		}
	}()
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			for _, got := range e.AllTickets() {
				if r := got.LastReport(); r != nil {
					r.Summary = "reader-touched"
				}
				if len(got.Inputs) > 0 {
					got.Inputs[0].Text = "reader-touched"
				}
				if got.Delivery != nil {
					got.Delivery.LastError = "reader-touched"
				}
			}
		}
	}()
	wg.Wait()
}

// Every read API that returns a slice/aggregate hands out copies, so the panel can
// never rewrite engine state (005 honest state, race safety).
func TestReaderAPIsDoNotAlias(t *testing.T) {
	e := newTestEngine(basePolicy())
	tr := ghTrigger("ra1", "lin", "webapp: change the timeout", true, true)
	tk := accept(t, e, tr)
	thread := threadKeyOf(&tr)
	if err := e.BindFollow(tk.ID, "github|owner/repo#77", true); err != nil {
		t.Fatal(err)
	}

	// TicketsOnThread
	onThread := e.TicketsOnThread(thread)
	if len(onThread) == 0 {
		t.Fatal("fixture: no ticket on the thread")
	}
	onThread[0].Title = "mutated"
	onThread[0].Mandate.Rules = append(onThread[0].Mandate.Rules, "mutated")
	_ = onThread

	// Decisions
	decisions := e.Decisions()
	if len(decisions) == 0 {
		t.Fatal("fixture: no decisions")
	}
	d := decisions[0]
	if len(d.Reasons) > 0 {
		d.Reasons[0].Code = "mutated"
	}
	if d.Mandate != nil && len(d.Mandate.Rules) > 0 {
		d.Mandate.Rules[0] = "mutated"
	}
	if len(d.TriggerIDs) > 0 {
		d.TriggerIDs[0] = "mutated"
	}

	// Evals
	for _, ev := range e.Evals() {
		for i := range ev.TriggerIDs {
			ev.TriggerIDs[i] = "mutated"
		}
	}

	// Disposition
	disp, ok := e.Disposition(tr.ID)
	if !ok {
		t.Fatal("fixture: no disposition")
	}
	for i := range disp.Reason {
		disp.Reason[i].Code = "mutated"
	}
	disp.TicketIDs = append(disp.TicketIDs, "mutated")

	// Nothing above may have changed the engine.
	snap, _ := e.TicketSnapshot(tk.ID)
	if snap.Title == "mutated" {
		t.Fatal("TicketsOnThread aliases engine state")
	}
	for _, id := range snap.Mandate.Rules {
		if id == "mutated" {
			t.Fatal("Ticket mandate aliases engine state")
		}
	}
	again, _ := e.Decision(decisions[0].ID)
	if len(again.Reasons) > 0 && again.Reasons[0].Code == "mutated" {
		t.Fatal("Decisions aliases engine state")
	}
	if again.Mandate != nil {
		for _, id := range again.Mandate.Rules {
			if id == "mutated" {
				t.Fatal("Decision mandate aliases engine state")
			}
		}
	}
	if len(again.TriggerIDs) > 0 && again.TriggerIDs[0] == "mutated" {
		t.Fatal("Decision trigger ids alias engine state")
	}
	disp2, _ := e.Disposition(tr.ID)
	for _, r := range disp2.Reason {
		if r.Code == "mutated" {
			t.Fatal("Disposition aliases engine state")
		}
	}
	// The policy reader must not let a panel mutate authorization data either.
	pol, _ := e.PolicyOf("ws_web")
	pol.Allowed["github"]["mutated"] = true
	if again, _ := e.PolicyOf("ws_web"); again.Allowed["github"]["mutated"] {
		t.Fatal("PolicyOf aliases engine authorization data")
	}
}

// A driver that leaves the event time at the epoch must not poison ordering or
// deadlines: the engine normalizes the timestamp on receipt (domain.EnsureAt).
func TestReceiveNormalizesEpochTimestamps(t *testing.T) {
	e := newTestEngine(basePolicy())
	tr := ghTrigger("ep1", "lin", "webapp: change the timeout", true, true)
	tr.At = time.Unix(0, 0)
	if _, err := e.Receive(tr); err != nil {
		t.Fatal(err)
	}
	got, ok := e.Trigger(tr.ID)
	if !ok {
		t.Fatal("trigger was not stored")
	}
	if got.At.Year() < 2000 {
		t.Fatalf("an epoch timestamp must be normalized on receipt, got %s", got.At)
	}
}

// ---- Release policy: every inbound IM message is a pending approval ---------

func imPolicy() Policy {
	p := basePolicy()
	p.Allowed["dingtalk"] = map[string]bool{"ding": true}
	p.Admissions["dingtalk|chat"] = domain.AdmissionRow{Source: "dingtalk", Scope: "chat", Observed: true,
		ListedUnaddressed: domain.CellInferred, UnlistedAddressed: domain.CellIgnore, UnlistedUnaddressed: domain.CellIgnore}
	return p
}

func imTrigger(id, actor, thread, text string) domain.Trigger {
	return domain.Trigger{
		ID:               domain.Key(domain.Global, "dingtalk", id),
		Scope:            domain.Global,
		Source:           "dingtalk",
		Threads:          []domain.ThreadRef{{Source: "dingtalk", ID: thread}},
		Actor:            domain.Actor{ID: actor},
		Human:            true,
		Addressed:        domain.Addressed{Mention: true},
		Conversation:     domain.Conversation{Kind: domain.ConvGroup, ID: thread, Name: "运维群"},
		Row:              "chat",
		ScopeReceivesAll: true,
		Text:             text,
		At:               time.Now(),
	}
}

// evaluateOnce receives one Trigger and runs the due Eval, returning the Outcome.
func evaluateOnce(t *testing.T, e *Engine, tr domain.Trigger) Outcome {
	t.Helper()
	if _, err := e.Receive(tr); err != nil {
		t.Fatalf("receive: %v", err)
	}
	outs, err := e.EvaluateDue(time.Now())
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if len(outs) == 0 {
		t.Fatalf("no outcome for %s", tr.ID)
	}
	return outs[len(outs)-1]
}

func TestIMExplicitBecomesProposal(t *testing.T) {
	e := New(RuleAssessor{})
	e.Register(repoWS("ws_web"), imPolicy())
	out := evaluateOnce(t, e, imTrigger("im1", "ding", "chat-1", "webapp: change the timeout"))
	if out.Kind != domain.DispDecided {
		t.Fatalf("disposition %q", out.Kind)
	}
	if out.Ticket != "" {
		t.Fatal("an IM message must not create a Ticket before approval")
	}
	if len(e.AllTickets()) != 0 {
		t.Fatal("an IM message must not create a Ticket before approval")
	}
	ps := e.Proposals()
	if len(ps) != 1 {
		t.Fatalf("want one pending approval, got %d", len(ps))
	}
	if ps[0].Kind != domain.DecPropose {
		t.Fatalf("decision kind %q, want propose", ps[0].Kind)
	}
	if ps[0].Proposal == nil || ps[0].Proposal.From != "ding" {
		t.Fatalf("proposal must name the requester: %+v", ps[0].Proposal)
	}
	if ps[0].Reply.Route != domain.ReplyNone {
		t.Fatalf("a proposal must not reply on its own, route %q", ps[0].Reply.Route)
	}
	if len(e.Replies()) != 0 {
		t.Fatal("a proposal must not queue an outbound reply")
	}
	// The gate's strength is still recorded for the audit, even though the message
	// waits for the operator.
	disp, _ := e.Disposition(domain.Key(domain.Global, "dingtalk", "im1"))
	if disp.Strength != domain.StrengthExplicit {
		t.Fatalf("the gate must record explicit strength, got %q", disp.Strength)
	}
}

func TestIMApproveCreatesTicketRejectCreatesNothing(t *testing.T) {
	e := New(RuleAssessor{})
	e.Register(repoWS("ws_web"), imPolicy())
	evaluateOnce(t, e, imTrigger("im2", "ding", "chat-2", "webapp: change the timeout"))
	ps := e.Proposals()
	if len(ps) != 1 {
		t.Fatalf("want one proposal, got %d", len(ps))
	}
	out, err := e.Approve(ps[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if out.Ticket == "" {
		t.Fatal("Approve must create the Ticket")
	}
	if _, ok := e.TicketSnapshot(out.Ticket); !ok {
		t.Fatal("the approved Ticket must exist")
	}
	if len(e.Proposals()) != 0 {
		t.Fatal("the approved proposal must leave the pending list")
	}

	// Reject leaves nothing running.
	e2 := New(RuleAssessor{})
	e2.Register(repoWS("ws_web"), imPolicy())
	evaluateOnce(t, e2, imTrigger("im3", "ding", "chat-3", "webapp: change the timeout"))
	ps = e2.Proposals()
	if err := e2.Reject(ps[0].ID, "no"); err != nil {
		t.Fatal(err)
	}
	if len(e2.Proposals()) != 0 || len(e2.AllTickets()) != 0 {
		t.Fatal("Reject must leave no ticket and no pending item")
	}
}

func TestNonIMExplicitStillAccepts(t *testing.T) {
	// GitHub explicit keeps the prototype behavior.
	e := newTestEngine(basePolicy())
	tk := accept(t, e, ghTrigger("gp1", "lin", "webapp: change the timeout", true, true))
	if tk.Mandate.Strength != domain.StrengthExplicit {
		t.Fatalf("GitHub explicit strength %q", tk.Mandate.Strength)
	}
	if len(e.Proposals()) != 0 {
		t.Fatal("GitHub must not require approval")
	}
	// The local panel keeps the prototype behavior too.
	e2 := newTestEngine(basePolicy())
	if _, err := e2.Dispatch(DispatchInput{Text: "change the timeout", Workspace: "ws_web"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e2.EvaluateDue(time.Now()); err != nil {
		t.Fatal(err)
	}
	if len(e2.AllTickets()) != 1 || len(e2.Proposals()) != 0 {
		t.Fatal("a local panel task must still start work directly")
	}
}

// Two IM messages on one Thread produce two separate approvals, never one batched
// item.
func TestIMMessagesAreSeparateProposals(t *testing.T) {
	e := New(RuleAssessor{})
	e.Register(repoWS("ws_web"), imPolicy())
	for _, id := range []string{"imb1", "imb2"} {
		if _, err := e.Receive(imTrigger(id, "ding", "chat-9", "webapp: "+id)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.EvaluateDue(time.Now()); err != nil {
		t.Fatal(err)
	}
	ps := e.Proposals()
	if len(ps) != 2 {
		t.Fatalf("two messages must become two approvals, got %d", len(ps))
	}
	seen := map[domain.ID]bool{}
	for _, p := range ps {
		if seen[p.ID] {
			t.Fatalf("duplicate proposal id %s", p.ID)
		}
		seen[p.ID] = true
		if p.Proposal == nil || len(p.TriggerIDs) != 1 {
			t.Fatalf("each approval must cover exactly one message: %+v", p)
		}
	}
	if len(e.AllTickets()) != 0 {
		t.Fatal("no Ticket may exist before approval")
	}
	// Approving one leaves the other waiting and creates exactly one Ticket.
	if _, err := e.Approve(ps[0].ID); err != nil {
		t.Fatal(err)
	}
	if len(e.AllTickets()) != 1 || len(e.Proposals()) != 1 {
		t.Fatalf("approving one must leave the other pending: tickets=%d proposals=%d",
			len(e.AllTickets()), len(e.Proposals()))
	}
}

// The gate still decides who may ask: an unlisted sender's IM message is filtered,
// not turned into an approval.
func TestIMStrangerIsNotProposed(t *testing.T) {
	e := New(RuleAssessor{})
	p := imPolicy()
	row := p.Admissions["dingtalk|chat"]
	row.UnlistedAddressed = domain.CellIgnore
	p.Admissions["dingtalk|chat"] = row
	e.Register(repoWS("ws_web"), p)
	if _, err := e.Receive(imTrigger("ims1", "stranger", "chat-4", "webapp: change the timeout")); err != nil {
		t.Fatal(err)
	}
	if _, err := e.EvaluateDue(time.Now()); err != nil {
		t.Fatal(err)
	}
	if len(e.Proposals()) != 0 {
		t.Fatal("an unlisted sender must be filtered, not proposed")
	}
}

// ---- Undo reject: a rejected proposal is reversible until superseded ---------

// A rejection keeps the proposal, and UndoReject puts it back on the approval
// list without a Ticket, a launch or a reply; approving it then works.
func TestUndoRejectRestoresProposal(t *testing.T) {
	e := New(RuleAssessor{})
	e.Register(repoWS("ws_web"), imPolicy())
	evaluateOnce(t, e, imTrigger("ur1", "ding", "chat-ur", "webapp: change the timeout"))
	ps := e.Proposals()
	if len(ps) != 1 {
		t.Fatalf("want one proposal, got %d", len(ps))
	}
	id := ps[0].ID
	if err := e.Reject(id, "not now"); err != nil {
		t.Fatal(err)
	}
	if len(e.Proposals()) != 0 {
		t.Fatal("a rejected proposal must leave the pending list")
	}
	// The rejection keeps the proposal data so the undo needs no Eval.
	if dec, _ := e.Decision(id); dec.Proposal == nil {
		t.Fatal("Reject must keep the proposal for a later undo")
	}
	out, err := e.UndoReject(id)
	if err != nil {
		t.Fatal(err)
	}
	if !out.Proposal || out.Ticket != "" {
		t.Fatalf("undo must return a proposal and no ticket: %+v", out)
	}
	ps = e.Proposals()
	if len(ps) != 1 || ps[0].ID != id {
		t.Fatalf("the restored proposal must be pending again: %+v", ps)
	}
	if len(e.AllTickets()) != 0 {
		t.Fatal("undo must not create a Ticket")
	}
	if len(e.Replies()) != 0 {
		t.Fatal("undo must not queue a reply")
	}
	// The restored proposal can be approved normally.
	if _, err := e.Approve(id); err != nil {
		t.Fatalf("approve after undo: %v", err)
	}
	if len(e.AllTickets()) != 1 {
		t.Fatalf("approve after undo must create the ticket, got %d", len(e.AllTickets()))
	}
	if out, err := e.UndoReject(id); err == nil || out.Ticket != "" {
		t.Fatal("undo after approve must fail")
	}
}

// Undo is idempotent while the proposal is pending, and audited.
func TestUndoRejectIsIdempotentAndAudited(t *testing.T) {
	e := New(RuleAssessor{})
	e.Register(repoWS("ws_web"), imPolicy())
	evaluateOnce(t, e, imTrigger("ur2", "ding", "chat-ur2", "webapp: change the timeout"))
	id := e.Proposals()[0].ID
	if err := e.Reject(id, "later"); err != nil {
		t.Fatal(err)
	}
	first, err := e.UndoReject(id)
	if err != nil {
		t.Fatal(err)
	}
	second, err := e.UndoReject(id)
	if err != nil {
		t.Fatalf("a second undo of an already pending proposal must be idempotent: %v", err)
	}
	if !second.Proposal || second.Ticket != "" {
		t.Fatalf("idempotent undo returned %+v", second)
	}
	_ = first
	dec, _ := e.Decision(id)
	var rejection, undo bool
	for _, r := range dec.Reasons {
		switch r.Code {
		case domain.ReasonRejectedByPerson:
			rejection = true
		case domain.ReasonUndoReject:
			undo = true
		}
	}
	if !rejection || !undo {
		t.Fatalf("the decision must audit both the rejection and the undo: %+v", dec.Reasons)
	}
	if len(e.AllTickets()) != 0 {
		t.Fatal("undo must not create a Ticket")
	}
}

// Undo fails when a newer decision on the same Thread has superseded the rejection.
func TestUndoRejectSupersededByNewerDecision(t *testing.T) {
	e := New(RuleAssessor{})
	e.Register(repoWS("ws_web"), imPolicy())
	evaluateOnce(t, e, imTrigger("ur3", "ding", "chat-ur3", "webapp: first"))
	old := e.Proposals()[0].ID
	if err := e.Reject(old, "not now"); err != nil {
		t.Fatal(err)
	}
	// Advance the clock so the new decision is unambiguously newer.
	now := time.Now()
	e.SetClock(func() time.Time { return now.Add(time.Minute) })
	evaluateOnce(t, e, imTrigger("ur3b", "ding", "chat-ur3", "webapp: second"))
	if _, err := e.UndoReject(old); !errors.Is(err, ErrUndoSuperseded) {
		t.Fatalf("want ErrUndoSuperseded, got %v", err)
	}
	// The newer proposal itself is still pending and approvable.
	if len(e.Proposals()) != 1 {
		t.Fatalf("want the newer proposal pending, got %d", len(e.Proposals()))
	}
}

// Undoing something that was never a rejected proposal fails clearly.
func TestUndoRejectRejectsNonRejection(t *testing.T) {
	e := New(RuleAssessor{})
	e.Register(repoWS("ws_web"), imPolicy())
	evaluateOnce(t, e, imTrigger("ur4", "ding", "chat-ur4", "webapp: change the timeout"))
	id := e.Proposals()[0].ID
	if _, err := e.UndoReject(id); err != nil {
		t.Fatalf("undo of a pending proposal is idempotent, got %v", err)
	}
	if out, err := e.UndoReject("no-such-decision"); err == nil || out.Proposal {
		t.Fatal("undo of an unknown decision must fail")
	}
}
