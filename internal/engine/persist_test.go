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

// Migration: a decision row written by the removed approval mechanism (kind
// "propose" plus proposal/rejectedAt JSON) must still load, and it loads as a
// historical ignore. No proposal API exists any more.
func TestLegacyProposalRowsLoadAsIgnore(t *testing.T) {
	st := openTestStore(t)
	// A row in the old shape, including fields the struct no longer has.
	raw := `{"ID":"dec-legacy","Kind":"propose","Workspace":"ws_web","Thread":"t-legacy",` +
		`"Proposal":{"From":"old","Said":"please","Intent":"do it"},` +
		`"RejectedAt":"2026-11-03T10:00:00Z","At":"2026-11-03T09:00:00Z"}`
	// The raw value above is intentionally shaped like the pre-DSH JSON; the
	// columns mirror store.PutDecision.
	if _, err := st.Exec(`INSERT OR REPLACE INTO decision(id, workspace_id, kind, json) VALUES (?,?,?,?)`,
		"dec-legacy", "ws_web", "propose", raw); err != nil {
		t.Fatal(err)
	}
	_, p, _ := restart(t, st)
	dec, ok := p.Engine().Decision("dec-legacy")
	if !ok {
		t.Fatal("the legacy decision row did not load")
	}
	if dec.Kind != domain.DecIgnore {
		t.Fatalf("legacy proposal kind = %q, want ignore", dec.Kind)
	}
	var migrated bool
	for _, r := range dec.Reasons {
		if r.Code == domain.ReasonLegacyImport {
			migrated = true
		}
	}
	if !migrated {
		t.Fatalf("the migration must be audited: %+v", dec.Reasons)
	}
	// It is a historical record only: nothing pending, nothing to approve.
	if n := len(p.Engine().AllTickets()); n != 0 {
		t.Fatalf("a legacy proposal created work: %d tickets", n)
	}
}
