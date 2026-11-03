package engine

// Independent verification (verifier role): Ticket lifecycle + subscriptions.
// Claims: ready/dropped end the Ticket, stop withdraws the Mandate and receives
// nothing, retry lifts blocked/failed, pause holds launches and shifts Eval
// deadlines, AnswerAttention clears the stage and delivers the answer, fan-out
// delivers context without authority, handback returns a message to Eval only
// when no Ticket still owns it, and a failed revalidation returns messages to Eval.

import (
	"errors"
	"testing"
	"time"

	"github.com/zeokeel/super-agent/internal/domain"
)

func vfEngine(t *testing.T, p Policy) *Engine {
	t.Helper()
	e := New(RuleAssessor{})
	e.Register(vfRepo("ws_web"), p)
	return e
}

func vfAccept(t *testing.T, e *Engine, tr domain.Trigger) domain.Ticket {
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
	t.Fatalf("no ticket among %+v", outs)
	return domain.Ticket{}
}

func vfFinishTurn(t *testing.T, e *Engine, ticketID domain.ID) {
	t.Helper()
	if err := e.MarkInputsDelivered(ticketID); err != nil {
		t.Fatal(err)
	}
	if err := e.MarkInputsRead(ticketID); err != nil {
		t.Fatal(err)
	}
}

// ---- check 3: lifecycle -----------------------------------------------------

func TestVerifyReadyAndDroppedEndTheTicket(t *testing.T) {
	t.Run("ready without follows ends completed", func(t *testing.T) {
		e := vfEngine(t, vfPolicy(domain.AcceptAuto))
		tk := vfAccept(t, e, vfTrigger("l1", "lin", true, true, true))
		vfFinishTurn(t, e, tk.ID)
		if err := e.Report(tk.ID, domain.ReportReady, "done"); err != nil {
			t.Fatal(err)
		}
		got, _ := e.TicketSnapshot(tk.ID)
		if got.Ended == nil || got.Ended.Outcome != domain.OutcomeCompleted {
			t.Fatalf("ready must end the ticket as completed, got %+v", got.Ended)
		}
		if st := domain.StageOf(&got, time.Now()); st != domain.StageEnded {
			t.Fatalf("stage after ready = %q, want ended", st)
		}
	})
	t.Run("dropped ends the ticket", func(t *testing.T) {
		e := vfEngine(t, vfPolicy(domain.AcceptAuto))
		tk := vfAccept(t, e, vfTrigger("l2", "lin", true, true, true))
		vfFinishTurn(t, e, tk.ID)
		if err := e.Report(tk.ID, domain.ReportDropped, "no work"); err != nil {
			t.Fatal(err)
		}
		got, _ := e.TicketSnapshot(tk.ID)
		if got.Ended == nil || got.Ended.Outcome != domain.OutcomeDropped {
			t.Fatalf("dropped must end the ticket, got %+v", got.Ended)
		}
	})
}

func TestVerifyStopWithdrawsMandateAndReceivesNothing(t *testing.T) {
	e := vfEngine(t, vfPolicy(domain.AcceptAuto))
	tk := vfAccept(t, e, vfTrigger("s1", "lin", true, true, true))
	vfFinishTurn(t, e, tk.ID)
	if err := e.StopTicket(tk.ID, "person asked"); err != nil {
		t.Fatal(err)
	}
	got, _ := e.TicketSnapshot(tk.ID)
	if got.Ended == nil || got.Ended.Outcome != domain.OutcomeStopped || got.Ended.By != "human" {
		t.Fatalf("stop must end the ticket as a human stop, got %+v", got.Ended)
	}
	if got.Mandate.Withdrawn == "" {
		t.Fatal("stop must withdraw the Mandate")
	}
	inputsBefore := len(got.Inputs)

	// A later human message on the same Thread must not reach the stopped Ticket.
	vfReceive(t, e, vfTrigger("s1b", "lin", true, true, true))
	after, _ := e.TicketSnapshot(tk.ID)
	if after.Ended == nil {
		t.Fatal("a stopped ticket must stay ended")
	}
	if len(after.Inputs) != inputsBefore {
		t.Fatalf("a stopped ticket received %d new Inputs", len(after.Inputs)-inputsBefore)
	}
	for _, in := range after.Inputs {
		if in.TriggerID == domain.Key(domain.Global, "vf", "s1b") {
			t.Fatal("the post-stop message was delivered to the stopped ticket")
		}
	}
	// The message itself reached no Ticket, so it takes the Eval path (002:127-129).
	if e.QueueLen() != 1 {
		t.Fatalf("the post-stop message must be judged as new work, queue=%d", e.QueueLen())
	}
	if d, _ := e.Disposition(domain.Key(domain.Global, "vf", "s1b")); d.Kind == domain.DispResumed || d.Kind == domain.DispOwned {
		t.Fatalf("the post-stop message was treated as %q instead of new work", d.Kind)
	}
}

func TestVerifyRetryLiftsBlockedAndFailed(t *testing.T) {
	for _, state := range []domain.ReportState{domain.ReportBlocked, domain.ReportFailed} {
		t.Run(string(state), func(t *testing.T) {
			e := vfEngine(t, vfPolicy(domain.AcceptAuto))
			tk := vfAccept(t, e, vfTrigger("rt-"+string(state), "lin", true, true, true))
			vfFinishTurn(t, e, tk.ID)
			if err := e.Report(tk.ID, state, "stuck"); err != nil {
				t.Fatal(err)
			}
			got, _ := e.TicketSnapshot(tk.ID)
			wantStage := domain.StagePersonBlocked
			if state == domain.ReportFailed {
				wantStage = domain.StagePersonFailed
			}
			if st := domain.StageOf(&got, time.Now()); st != wantStage {
				t.Fatalf("stage after %s = %q, want %q", state, st, wantStage)
			}
			if err := e.RetryTicket(tk.ID); err != nil {
				t.Fatalf("retry after %s: %v", state, err)
			}
			got, _ = e.TicketSnapshot(tk.ID)
			if got.LastAssignment().State != domain.AssignPending || len(got.Assignments) != 2 {
				t.Fatalf("retry must queue a fresh Assignment: %+v", got.Assignments)
			}
			if st := domain.StageOf(&got, time.Now()); st != domain.StageWorkingQueued {
				t.Fatalf("after retry stage = %q, want working/queued", st)
			}
		})
	}
	// A finished ticket is not retryable.
	e := vfEngine(t, vfPolicy(domain.AcceptAuto))
	tk := vfAccept(t, e, vfTrigger("rt-ready", "lin", true, true, true))
	vfFinishTurn(t, e, tk.ID)
	if err := e.Report(tk.ID, domain.ReportReady, "done"); err != nil {
		t.Fatal(err)
	}
	if err := e.RetryTicket(tk.ID); !errors.Is(err, ErrNotRetryable) {
		t.Fatalf("retry of a completed ticket must return ErrNotRetryable, got %v", err)
	}
}

func TestVerifyPauseHoldsLaunchAndEvalDeadline(t *testing.T) {
	t.Run("a paused engine judges nothing until resumed", func(t *testing.T) {
		e := vfEngine(t, vfPolicy(domain.AcceptAuto))
		e.Pause()
		out, err := e.Receive(vfTrigger("p1", "lin", true, true, true))
		if err != nil {
			t.Fatal(err)
		}
		if !out.Held {
			t.Fatalf("a paused engine must report the Trigger as held, got %+v", out)
		}
		if outs, err := e.EvaluateDue(time.Now()); err != nil || len(outs) != 0 {
			t.Fatalf("paused EvaluateDue returned %v, %v", outs, err)
		}
		if len(e.AllTickets()) != 0 {
			t.Fatal("a paused engine created a Ticket")
		}
		e.Resume()
		if _, err := e.EvaluateDue(time.Now()); err != nil {
			t.Fatal(err)
		}
		if len(e.AllTickets()) != 1 {
			t.Fatal("after resume the queued request must be judged")
		}
	})

	t.Run("pause shifts a running Eval deadline by the pause length", func(t *testing.T) {
		a := newVFBlockingAssessor()
		e := New(a)
		e.Register(vfRepo("ws_web"), vfPolicy(domain.AcceptAuto))
		e.SetEvalTimeout(10 * time.Second)
		t0 := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
		now := t0
		e.SetClock(func() time.Time { return now })

		vfReceive(t, e, vfTrigger("pause-deadline", "lin", true, true, true))
		done := make(chan struct{})
		go func() {
			defer close(done)
			_, _ = e.EvaluateDue(t0) // beginEval sets Deadline = t0+10s
		}()
		select {
		case <-a.started:
		case <-time.After(2 * time.Second):
			t.Fatal("assessor never started")
		}
		e.Pause() // pauseStarted = t0
		now = t0.Add(30 * time.Second)
		close(a.release)
		<-done

		// The judgement completed while paused: it is held, not applied.
		var held domain.Eval
		for _, ev := range e.Evals() {
			held = ev
		}
		if held.State != domain.EvalQueued || held.PausedAt.IsZero() {
			t.Fatalf("a paused judgement must be requeued with PausedAt set: %+v", held)
		}
		if len(e.AllTickets()) != 0 {
			t.Fatal("a paused engine applied a judgement")
		}
		e.Resume()
		var resumed domain.Eval
		for _, ev := range e.Evals() {
			resumed = ev
		}
		wantDeadline := t0.Add(30*time.Second + 10*time.Second)
		if !resumed.Deadline.Equal(wantDeadline) {
			t.Fatalf("deadline after resume = %s, want %s (pause length must be added)", resumed.Deadline, wantDeadline)
		}
		if !resumed.PausedAt.IsZero() {
			t.Fatal("Resume must clear PausedAt")
		}
	})
}

func TestVerifyAnswerAttentionClearsStageAndDeliversInput(t *testing.T) {
	e := vfEngine(t, vfPolicy(domain.AcceptAuto))
	tk := vfAccept(t, e, vfTrigger("att1", "lin", true, true, true))
	if err := e.RaiseAttention(tk.ID, "which env?", []string{"staging", "prod"}); err != nil {
		t.Fatal(err)
	}
	got, _ := e.TicketSnapshot(tk.ID)
	if st := domain.StageOf(&got, time.Now()); st != domain.StagePersonAnswer {
		t.Fatalf("stage with an Attention = %q, want person/answer", st)
	}
	if err := e.AnswerAttention(tk.ID, "staging"); err != nil {
		t.Fatal(err)
	}
	got, _ = e.TicketSnapshot(tk.ID)
	if got.Attention != nil {
		t.Fatal("the answer must clear the Attention")
	}
	var answer *domain.Input
	for i := range got.Inputs {
		if got.Inputs[i].Kind == domain.InputText && got.Inputs[i].Text == "staging" {
			answer = &got.Inputs[i]
		}
	}
	if answer == nil {
		t.Fatalf("the answer must be delivered as an Input: %+v", got.Inputs)
	}
	if answer.Delivered || answer.Read {
		t.Fatalf("a new answer must be neither delivered nor read yet: %+v", *answer)
	}
	if st := domain.StageOf(&got, time.Now()); st == domain.StagePersonAnswer {
		t.Fatalf("stage after the answer is still person/answer")
	}
	// The three-stage delivery works for the answer like any other Input.
	if err := e.MarkInputsDelivered(tk.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.MarkInputsRead(tk.ID); err != nil {
		t.Fatal(err)
	}
	got, _ = e.TicketSnapshot(tk.ID)
	for _, in := range got.Inputs {
		if in.Kind == domain.InputText && in.Text == "staging" && (!in.Delivered || !in.Read) {
			t.Fatalf("answer Input was not marked delivered/read: %+v", in)
		}
	}
}

// ---- check 4: subscriptions, follow-ups, handback, revalidation -------------

func TestVerifyFanoutDeliversButGrantsNothing(t *testing.T) {
	e := vfEngine(t, vfPolicy(domain.AcceptAuto))
	tk := vfAccept(t, e, vfTrigger("f1", "lin", true, true, true))
	// An unlisted bot's CI message on the Thread reaches the Ticket as context.
	ci := vfTrigger("f1b", "ci[bot]", false, false, false)
	vfReceive(t, e, ci)

	got, _ := e.TicketSnapshot(tk.ID)
	var delivered bool
	for _, in := range got.Inputs {
		if in.TriggerID == ci.ID {
			delivered = true
		}
	}
	if !delivered {
		t.Fatal("fan-out must deliver the message as an Input")
	}
	if e.QueueLen() != 0 {
		t.Fatal("a refused bot message must not queue an Eval")
	}
	if len(e.Decisions()) != 1 {
		t.Fatalf("fan-out must not produce a Decision for the context message, decisions=%d", len(e.Decisions()))
	}
}

func TestVerifyOwnedHumanMessageTakesNoEval(t *testing.T) {
	e := vfEngine(t, vfPolicy(domain.AcceptAuto))
	tk := vfAccept(t, e, vfTrigger("o1", "lin", true, true, true))
	before := len(e.Decisions())

	follow := vfTrigger("o1b", "lin", true, true, true)
	out, err := e.Receive(follow)
	if err != nil {
		t.Fatal(err)
	}
	if out.Kind != domain.DispOwned {
		t.Fatalf("a message the Executor owns must be disposition owned, got %q", out.Kind)
	}
	if e.QueueLen() != 0 {
		t.Fatal("an owned message must take no Eval")
	}
	if len(e.Decisions()) != before {
		t.Fatal("an owned message must produce no Decision")
	}
	got, _ := e.TicketSnapshot(tk.ID)
	var owned bool
	for _, in := range got.Inputs {
		if in.TriggerID == follow.ID {
			owned = true
		}
	}
	if !owned {
		t.Fatal("the owned message must be delivered to the Ticket")
	}
}

func TestVerifyHandBackRequeuesOnlyWhenNoOwner(t *testing.T) {
	e := vfEngine(t, vfPolicy(domain.AcceptAuto))
	first := vfAccept(t, e, vfTrigger("h1", "lin", true, true, true))

	// A second Ticket on the same Thread owns the same human messages. A human
	// follow-up would be owned, so create it from a listed bot's message instead.
	second := vfAccept(t, e, vfTrigger("h2", "dependabot[bot]", false, true, true))
	if second.ID == first.ID {
		t.Fatal("fixture: expected two tickets on the Thread")
	}
	if len(e.AllTickets()) != 2 {
		t.Fatalf("fixture: want two tickets, got %d", len(e.AllTickets()))
	}

	owned := vfTrigger("h3", "lin", true, true, true)
	out, err := e.Receive(owned)
	if err != nil {
		t.Fatal(err)
	}
	if out.Kind != domain.DispOwned {
		t.Fatalf("the human message must be owned, got %q", out.Kind)
	}

	// Both Tickets are live owners: one Executor handing the message back must not
	// create a fresh Eval while the other still owns it (002:30).
	thread := vfThreadKey(owned)
	if err := e.HandBack(first.ID, thread, "out of scope"); err != nil {
		t.Fatal(err)
	}
	if e.QueueLen() != 0 {
		t.Fatalf("handback created a fresh Eval while ticket %s still owns the message (queue=%d)",
			second.ID, e.QueueLen())
	}
}

func TestVerifyEndedNotStoppedResumesWithoutEval(t *testing.T) {
	e := vfEngine(t, vfPolicy(domain.AcceptAuto))
	tk := vfAccept(t, e, vfTrigger("r1", "lin", true, true, true))
	vfFinishTurn(t, e, tk.ID)
	if err := e.Report(tk.ID, domain.ReportReady, "done"); err != nil {
		t.Fatal(err)
	}
	before := len(e.Decisions())

	follow := vfTrigger("r1b", "lin", true, true, true)
	out, err := e.Receive(follow)
	if err != nil {
		t.Fatal(err)
	}
	if out.Kind != domain.DispResumed {
		t.Fatalf("an ended-not-stopped ticket must resume, got %q", out.Kind)
	}
	if e.QueueLen() != 0 {
		t.Fatal("resume must not take an Eval")
	}
	if len(e.Decisions()) != before {
		t.Fatal("resume must not produce a Decision")
	}
	got, _ := e.TicketSnapshot(tk.ID)
	if got.Ended != nil {
		t.Fatal("the ticket must be open again")
	}
	if got.LastAssignment().State != domain.AssignPending {
		t.Fatalf("resume must queue an Assignment: %+v", got.Assignments)
	}
}

func TestVerifyRevalidationFailureReturnsMessageToEval(t *testing.T) {
	p := vfPolicy(domain.AcceptAuto)
	row := p.Admissions["github|comment"]
	row.UnlistedAddressed = domain.CellInferred
	p.Admissions["github|comment"] = row
	e := vfEngine(t, p)

	tr := vfTrigger("rv1", "stranger", true, true, true)
	tk := vfAccept(t, e, tr)
	if tk.Mandate.Strength != domain.StrengthInferred {
		t.Fatalf("fixture: want an inferred mandate, got %q", tk.Mandate.Strength)
	}
	vfFinishTurn(t, e, tk.ID)
	if err := e.Report(tk.ID, domain.ReportReady, "done"); err != nil {
		t.Fatal(err)
	}

	// Weaken the policy so the inferred Mandate no longer holds.
	weakened := p
	weakened.Version = "v2"
	weakened.AcceptMode = domain.AcceptOff
	if err := e.SetPolicy("ws_web", weakened); err != nil {
		t.Fatal(err)
	}

	// The person's follow-up message cannot resume the Ticket. 002:141: it is
	// treated as having reached no Ticket, so it goes to Eval.
	vfReceive(t, e, vfTrigger("rv1b", "stranger", true, true, true))
	got, _ := e.TicketSnapshot(tk.ID)
	if got.Ended == nil {
		t.Fatal("a revoked Mandate must leave the ticket ended")
	}
	if got.Mandate.Revoked == "" {
		t.Fatal("the failed revalidation must be recorded on the Mandate")
	}
	if e.QueueLen() != 1 {
		t.Fatalf("the message that failed revalidation must go to Eval, queue=%d", e.QueueLen())
	}
}

func TestVerifyContextOnlyFollowDoesNotResumeEndedTicket(t *testing.T) {
	e := vfEngine(t, vfPolicy(domain.AcceptAuto))
	tk := vfAccept(t, e, vfTrigger("c1", "lin", true, true, true))
	const contextThread = "global|github|owner/repo#99"
	if err := e.BindFollow(tk.ID, contextThread, false); err != nil {
		t.Fatal(err)
	}
	vfFinishTurn(t, e, tk.ID)
	// dropped ends the Ticket regardless of unclosed context follows.
	if err := e.Report(tk.ID, domain.ReportDropped, "done"); err != nil {
		t.Fatal(err)
	}

	other := vfTrigger("c1b", "lin", true, true, true)
	other.Threads = []domain.ThreadRef{{Source: "github", ID: "owner/repo#99"}}
	vfReceive(t, e, other)

	got, _ := e.TicketSnapshot(tk.ID)
	if got.Ended == nil {
		t.Fatal("a context-only Thread must not resume an ended Ticket")
	}
	if len(got.Assignments) != 1 {
		t.Fatalf("a context-only message queued an Assignment: %+v", got.Assignments)
	}
	// It is ordinary mail for a fresh Eval instead.
	if e.QueueLen() != 1 {
		t.Fatalf("the context-Thread message must be judged as new mail, queue=%d", e.QueueLen())
	}
}
