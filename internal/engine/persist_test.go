package engine

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/zeokeel/super-agent/internal/domain"
	"github.com/zeokeel/super-agent/internal/effects"
	"github.com/zeokeel/super-agent/internal/store"
)

func openTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "engine.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// restart simulates the next process: a brand-new Engine over the same file.
func restart(t *testing.T, st *store.Store) (*Engine, *Persistence, *effects.Queue) {
	t.Helper()
	q, err := effects.New(st)
	if err != nil {
		t.Fatal(err)
	}
	e := New(RuleAssessor{})
	p, err := NewPersistence(e, st, q, nil)
	if err != nil {
		t.Fatal(err)
	}
	return e, p, q
}

func registerWeb(t *testing.T, p *Persistence) {
	t.Helper()
	if err := p.Register(repoWS("ws_web"), basePolicy()); err != nil {
		t.Fatal(err)
	}
}

// durableGitHub is a Trigger whose accept goes through Persistence.Receive and
// Persistence.EvaluateDue, exactly as the runtime path does.
func durableGitHub(id, text string) domain.Trigger {
	return ghTrigger(id, "lin", text, true, true)
}

// A restart rebuilds the workspace, the policy, the Trigger, its Disposition,
// the Decision and the Ticket from disk; the same Trigger afterwards is a
// duplicate, not a second Ticket (001:28, design §5.8).
func TestPersistenceRebuildsStateAfterRestart(t *testing.T) {
	st := openTestStore(t)
	_, p1, _ := restart(t, st)
	registerWeb(t, p1)

	tr := durableGitHub("d1", "webapp: change the timeout")
	if _, err := p1.Receive(tr); err != nil {
		t.Fatal(err)
	}
	outs, err := p1.EvaluateDue(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var ticketID domain.ID
	for _, o := range outs {
		if o.Ticket != "" {
			ticketID = o.Ticket
		}
	}
	if ticketID == "" {
		t.Fatalf("durable EvaluateDue created no ticket: %+v", outs)
	}
	if err := p1.SaveTicket(ticketID); err != nil {
		t.Fatal(err)
	}

	// The next process.
	e2, p2, _ := restart(t, st)
	_ = p2
	got, ok := e2.TicketSnapshot(ticketID)
	if !ok {
		t.Fatal("the ticket did not survive the restart")
	}
	if got.Workspace != "ws_web" || got.Mandate.Strength != domain.StrengthExplicit {
		t.Fatalf("bad restored ticket: %+v", got.Mandate)
	}
	if len(got.Inputs) == 0 {
		t.Fatal("the Triggers of the batch must be restored as Inputs")
	}
	if _, err := e2.Receive(tr); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("a replayed Trigger must be a duplicate after restart, got %v", err)
	}
	var decisions int
	for _, d := range e2.Decisions() {
		if d.Kind == domain.DecAccept {
			decisions++
		}
	}
	if decisions == 0 {
		t.Fatal("the Decision did not survive the restart")
	}
}

// A queued Eval survives a restart: the candidate is not lost between Receive
// and EvaluateDue (design §5.8, 002:149).
func TestEvalQueueSurvivesRestart(t *testing.T) {
	st := openTestStore(t)
	_, p1, _ := restart(t, st)
	registerWeb(t, p1)
	if _, err := p1.Receive(durableGitHub("q1", "webapp: change the timeout")); err != nil {
		t.Fatal(err)
	}
	if p1.Engine().QueueLen() != 1 {
		t.Fatal("the Eval must be queued")
	}
	// Crash before EvaluateDue.
	e2, p2, _ := restart(t, st)
	if e2.QueueLen() != 1 {
		t.Fatalf("the queued Eval must survive the restart, queue=%d", e2.QueueLen())
	}
	outs, err := p2.EvaluateDue(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(outs) == 0 || outs[0].Ticket == "" {
		t.Fatalf("the restored Eval must be judged, got %+v", outs)
	}
}

// Creating a Ticket enqueues a durable launch effect; the row survives a crash
// and the next process runs it (design §7.2).
func TestLaunchEffectRerunsAfterRestart(t *testing.T) {
	st := openTestStore(t)
	_, p1, q1 := restart(t, st)
	registerWeb(t, p1)
	launched := make(chan domain.ID, 4)
	if err := q1.Handle(EffectLaunchHuman, LaunchHandler(func(_ context.Context, p LaunchPayload) error {
		launched <- p.TicketID
		return nil
	}), effects.Options{}); err != nil {
		t.Fatal(err)
	}
	if _, err := p1.Receive(durableGitHub("lf1", "webapp: change the timeout")); err != nil {
		t.Fatal(err)
	}
	outs, err := p1.EvaluateDue(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var ticketID domain.ID
	for _, o := range outs {
		if o.Ticket != "" {
			ticketID = o.Ticket
		}
	}
	if ticketID == "" {
		t.Fatal("no ticket")
	}
	q1.Start()
	if err := q1.Idle(context.Background(), EffectLaunchHuman); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-launched:
		if got != ticketID {
			t.Fatalf("launch effect ran for %q, want %q", got, ticketID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the human-priority launch effect did not run")
	}

	// A crash after the effect row exists but before it ran: the next process
	// re-runs it. A fresh store keeps this deterministic (the first queue above is
	// already started and would consume it immediately).
	st2 := openTestStore(t)
	_, p3, _ := restart(t, st2)
	registerWeb(t, p3)
	if _, err := p3.Receive(durableGitHub("lf2", "webapp: change the second timeout")); err != nil {
		t.Fatal(err)
	}
	if _, err := p3.EvaluateDue(time.Now()); err != nil {
		t.Fatal(err)
	}
	_, _, q2 := restart(t, st2)
	rerun := make(chan domain.ID, 4)
	if err := q2.Handle(EffectLaunchHuman, LaunchHandler(func(_ context.Context, p LaunchPayload) error {
		rerun <- p.TicketID
		return nil
	}), effects.Options{}); err != nil {
		t.Fatal(err)
	}
	q2.Start()
	defer q2.Drain(context.Background())
	select {
	case <-rerun:
	case <-time.After(2 * time.Second):
		t.Fatal("a pending launch effect must be re-run after a restart")
	}
}

// Opt-in proposals are Decisions: they survive a restart and can still be approved
// after it (Approve needs the Decision→Trigger edge, which Load rebuilds).
func TestRequireApprovalProposalsSurviveRestart(t *testing.T) {
	st := openTestStore(t)
	_, p1, _ := restart(t, st)
	if err := p1.Register(repoWS("ws_web"), approvalPolicy()); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"ip1", "ip2"} {
		if _, err := p1.Receive(approvalTrigger(id, "chat-r-"+id)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := p1.EvaluateDue(time.Now()); err != nil {
		t.Fatal(err)
	}
	if got := len(p1.Engine().Proposals()); got != 2 {
		t.Fatalf("want two proposals before the restart, got %d", got)
	}

	e2, p2, _ := restart(t, st)
	_ = p2
	ps := e2.Proposals()
	if len(ps) != 2 {
		t.Fatalf("the IM approvals must survive a restart, got %d", len(ps))
	}
	out, err := e2.Approve(ps[0].ID)
	if err != nil {
		t.Fatalf("Approve after restart: %v", err)
	}
	if out.Ticket == "" {
		t.Fatal("Approve after restart must create the Ticket")
	}
	if len(e2.Proposals()) != 1 {
		t.Fatalf("one approval must remain pending, got %d", len(e2.Proposals()))
	}
}

// A rejection can be undone across a restart: the proposal content, the rejection
// instant and the restored kind all live in the decision row.
func TestUndoRejectSurvivesRestart(t *testing.T) {
	st := openTestStore(t)
	_, p1, _ := restart(t, st)
	if err := p1.Register(repoWS("ws_web"), approvalPolicy()); err != nil {
		t.Fatal(err)
	}
	if _, err := p1.Receive(approvalTrigger("urr1", "chat-urr")); err != nil {
		t.Fatal(err)
	}
	if _, err := p1.EvaluateDue(time.Now()); err != nil {
		t.Fatal(err)
	}
	if got := len(p1.Engine().Proposals()); got != 1 {
		t.Fatalf("want one proposal, got %d", got)
	}
	id := p1.Engine().Proposals()[0].ID
	if err := p1.Reject(id, "later"); err != nil {
		t.Fatal(err)
	}
	// Restart after the rejection.
	e2, p2, _ := restart(t, st)
	if len(e2.Proposals()) != 0 {
		t.Fatal("the rejection must survive the restart")
	}
	out, err := p2.UndoReject(id)
	if err != nil {
		t.Fatalf("undo after restart: %v", err)
	}
	if !out.Proposal || out.Ticket != "" {
		t.Fatalf("restart undo returned %+v", out)
	}
	// The restored proposal survives another restart and can be approved.
	e3, p3, _ := restart(t, st)
	ps := e3.Proposals()
	if len(ps) != 1 || ps[0].ID != id {
		t.Fatalf("the restored proposal must survive a restart: %+v", ps)
	}
	if _, err := p3.Approve(id); err != nil {
		t.Fatalf("approve restored proposal: %v", err)
	}
	if len(e3.AllTickets()) != 1 {
		t.Fatalf("approve must create one ticket, got %d", len(e3.AllTickets()))
	}
}
