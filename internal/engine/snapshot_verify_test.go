package engine

// Independent verification (verifier role): TicketSnapshot is a deep copy and is
// safe to read from a goroutine while the engine writes (run with -race).

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/zeokeel/super-agent/internal/domain"
)

// deep-copy check for every nested pointer/slice a Ticket carries. A reader that
// mutates its snapshot must never change engine state.
func TestVerifyTicketSnapshotDeepCopiesNestedState(t *testing.T) {
	e := vfEngine(t, vfPolicy(domain.AcceptAuto))
	tk := vfAccept(t, e, vfTrigger("snap1", "lin", true, true, true))
	if err := e.BindFollow(tk.ID, "github|owner/repo#77", true); err != nil {
		t.Fatal(err)
	}
	if err := e.RaiseAttention(tk.ID, "pick one", []string{"a", "b"}); err != nil {
		t.Fatal(err)
	}
	if err := e.Report(tk.ID, domain.ReportBlocked, "need access"); err != nil {
		t.Fatal(err)
	}
	if err := e.DeliveryFailed(tk.ID, "boom"); err != nil {
		t.Fatal(err)
	}

	snap, ok := e.TicketSnapshot(tk.ID)
	if !ok {
		t.Fatal("no snapshot")
	}
	// Mutate every nested field a reader can reach.
	if snap.Attention != nil && len(snap.Attention.Options) > 0 {
		snap.Attention.Options[0] = "mutated"
		snap.Attention.Question = "mutated"
	}
	if len(snap.Inputs) > 0 {
		snap.Inputs[0].Text = "mutated"
	}
	if len(snap.Assignments) > 0 && snap.Assignments[0].Report != nil {
		snap.Assignments[0].Report.Summary = "mutated"
	}
	if snap.Delivery != nil {
		snap.Delivery.LastError = "mutated"
	}
	if len(snap.Follows) > 0 {
		snap.Follows[0].Thread = "mutated"
		if snap.Follows[0].ClosedAt != nil {
			*snap.Follows[0].ClosedAt = time.Unix(0, 0)
		}
	}

	again, _ := e.TicketSnapshot(tk.ID)
	if again.Attention != nil && again.Attention.Question == "mutated" {
		t.Fatal("snapshot aliases Ticket.Attention")
	}
	if len(again.Inputs) > 0 && again.Inputs[0].Text == "mutated" {
		t.Fatal("snapshot aliases Ticket.Inputs")
	}
	if len(again.Assignments) > 0 && again.Assignments[0].Report != nil &&
		again.Assignments[0].Report.Summary == "mutated" {
		t.Fatal("snapshot aliases Assignment.Report")
	}
	if again.Delivery != nil && again.Delivery.LastError == "mutated" {
		t.Fatal("snapshot aliases Ticket.Delivery")
	}
	if len(again.Follows) > 0 && again.Follows[0].Thread == "mutated" {
		t.Fatal("snapshot aliases Ticket.Follows")
	}

	// Closing the follow gives Follow.ClosedAt a pointer; mutating the snapshot's
	// copy must not rewrite the engine's close time.
	if err := e.ThreadClosed("github|owner/repo#77", domain.OutcomeMerged, time.Now()); err != nil {
		t.Fatal(err)
	}
	closed, _ := e.TicketSnapshot(tk.ID)
	if len(closed.Follows) == 0 || closed.Follows[0].ClosedAt == nil {
		t.Fatalf("fixture: follow was not closed: %+v", closed.Follows)
	}
	want := *closed.Follows[0].ClosedAt
	mutated := want.Add(time.Hour)
	*closed.Follows[0].ClosedAt = mutated
	original, _ := e.TicketSnapshot(tk.ID)
	if len(original.Follows) > 0 && original.Follows[0].ClosedAt != nil &&
		original.Follows[0].ClosedAt.Equal(mutated) {
		t.Fatalf("snapshot aliases Follow.ClosedAt (*time.Time): a panel read can rewrite a close time")
	}
}

// Ended and Mandate pointers are copied too.
func TestVerifyTicketSnapshotCopiesMandateAndEnded(t *testing.T) {
	p := vfPolicy(domain.AcceptAuto)
	row := p.Admissions["github|comment"]
	row.UnlistedAddressed = domain.CellInferred
	p.Admissions["github|comment"] = row
	e := vfEngine(t, p)
	tk := vfAccept(t, e, vfTrigger("snap2", "stranger", true, true, true))
	if len(tk.Mandate.Rules) == 0 {
		t.Fatalf("fixture: inferred mandate must carry matched rules: %+v", tk.Mandate)
	}
	vfFinishTurn(t, e, tk.ID)
	if err := e.Report(tk.ID, domain.ReportReady, "done"); err != nil {
		t.Fatal(err)
	}
	snap, _ := e.TicketSnapshot(tk.ID)
	snap.Mandate.Rules[0] = "mutated"
	if snap.Ended != nil {
		snap.Ended.Outcome = "mutated"
	}
	again, _ := e.TicketSnapshot(tk.ID)
	if len(again.Mandate.Rules) > 0 && again.Mandate.Rules[0] == "mutated" {
		t.Fatal("snapshot aliases Mandate.Rules")
	}
	if again.Ended != nil && again.Ended.Outcome == "mutated" {
		t.Fatal("snapshot aliases Ticket.Ended")
	}
}

// Feed hands out internal Ticket pointers (no copy), so a panel goroutine reading
// Feed aliases writer state. This is a separate safety claim from TicketSnapshot.
func TestVerifyFeedDoesNotAliasEngineState(t *testing.T) {
	e := vfEngine(t, vfPolicy(domain.AcceptAuto))
	tk := vfAccept(t, e, vfTrigger("feed1", "lin", true, true, true))
	// A fanned-out context message records the Ticket id on its disposition, which
	// is where Feed attaches its *Ticket pointer.
	vfReceive(t, e, vfTrigger("feed1b", "ci[bot]", false, false, false))
	items := e.Feed()
	var withTicket *domain.Ticket
	for _, it := range items {
		if it.Ticket != nil {
			withTicket = it.Ticket
		}
	}
	if withTicket == nil {
		t.Fatalf("fixture: Feed attached no Ticket: %+v", items)
	}
	withTicket.Title = "mutated through Feed"
	again, _ := e.TicketSnapshot(tk.ID)
	if again.Title == "mutated through Feed" {
		t.Fatalf("Feed exposes an internal *Ticket: a reader mutated engine state")
	}
}

// The race check: readers use only the copy-returning APIs while a writer keeps
// mutating. Run with -race; a regression here means a projector can crash the app.
func TestVerifyTicketSnapshotRaceWithWriter(t *testing.T) {
	e := vfEngine(t, vfPolicy(domain.AcceptAuto))
	tk := vfAccept(t, e, vfTrigger("race1", "lin", true, true, true))
	const iterations = 300
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			_ = e.DeliverInput(tk.ID, domain.Input{
				ID: domain.ID(fmt.Sprintf("input-%d", i)), Kind: domain.InputNote,
				Text: "note", At: time.Now(),
			})
			_ = e.MarkInputsDelivered(tk.ID)
			_ = e.MarkInputsRead(tk.ID)
			_ = e.MarkTurnEnded(tk.ID)
			_ = e.Report(tk.ID, domain.ReportProgress, "working")
			_, _ = e.Present(tk.ID, domain.Artifact{Kind: domain.ArtifactLink, Title: "a", URL: "u"})
			_ = e.BindFollow(tk.ID, "github|owner/repo#5", false)
			_ = e.RaiseAttention(tk.ID, "q", []string{"a"})
			_ = e.AnswerAttention(tk.ID, "a")
			vfReceive(t, e, vfTrigger(fmt.Sprintf("race-in-%d", i), "lin", true, true, true))
			_, _ = e.EvaluateDue(time.Now())
		}
	}()

	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				_, _ = e.TicketSnapshot(tk.ID)
				_ = e.AllTickets()
				_ = e.TicketsOnThread("global|github|owner/repo#12")
				_ = e.Decisions()
				_, _ = e.LastTicket()
				_, _ = e.TicketByNumber(tk.Number)
				_ = e.Evals()
				_ = e.QueueLen()
				_, _, _ = e.Counts()
			}
		}()
	}
	wg.Wait()
}
