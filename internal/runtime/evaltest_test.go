package runtime

// Test helpers for the two-phase Eval. Receive/Dispatch now only queue an Eval
// (fix-engine API §0.1); a test that needs the Ticket calls evalNow, which runs the
// same pass the 2s loop runs.

import (
	"testing"
	"time"

	"github.com/zeokeel/super-agent/internal/domain"
	"github.com/zeokeel/super-agent/internal/engine"
)

// evalNow advances the Eval queue and returns the first Outcome that created a
// Ticket, falling back to the last Outcome when the batch asked a question or was
// filtered.
func evalNow(eng *engine.Engine) engine.Outcome {
	outs, err := eng.EvaluateDue(time.Now())
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

// evalAll advances the Eval queue and returns every Outcome it produced.
func evalAll(eng *engine.Engine) []engine.Outcome {
	outs, err := eng.EvaluateDue(time.Now())
	if err != nil {
		return nil
	}
	return outs
}

// withThread gives a test Trigger the Thread key the two-phase Eval batches by.
func withThread(t domain.Trigger) domain.Trigger {
	if len(t.Threads) > 0 {
		return t
	}
	id := t.Conversation.ID
	if id == "" {
		id = t.Channel.Thread
	}
	if id == "" {
		id = string(t.ID)
	}
	t.Threads = []domain.ThreadRef{{Source: t.Source, ID: id}}
	return t
}

// allSnapshots returns deep copies of every Ticket. Engine.AllTickets is shallow
// (shared Report pointers), so tests that read Ticket data must use this under
// -race.
func allSnapshots(e *engine.Engine) []domain.Ticket {
	all := e.AllTickets()
	out := make([]domain.Ticket, 0, len(all))
	for _, t := range all {
		if deep, ok := e.TicketSnapshot(t.ID); ok {
			out = append(out, deep)
		}
	}
	return out
}

// ticketNow is a snapshot read for assertions; it never holds the engine pointer.
func ticketNow(t *testing.T, eng *engine.Engine, id domain.ID) domain.Ticket {
	t.Helper()
	tk, ok := eng.TicketSnapshot(id)
	if !ok {
		t.Fatalf("ticket %q disappeared", id)
	}
	return tk
}

// settleRuntime waits for async Workers (and the effects they launch) before the
// test's t.TempDir cleanup runs. A worker still writing its session/deliverables
// makes cleanup fail with "directory not empty" (observed as a release-run flake).
func settleRuntime(t *testing.T, rt *Runtime) {
	t.Helper()
	t.Cleanup(func() {
		if !rt.WaitIdle(20 * time.Second) {
			t.Errorf("runtime workers did not drain before temp-dir cleanup")
		}
		// The Worker goroutine can be done while an agent-internal session writer
		// still flushes; wait for the roots to go quiet so TempDir cleanup does not
		// race the last write.
		if !rt.WaitRootsQuiet(300*time.Millisecond, 10*time.Second) {
			t.Errorf("worker roots kept changing during cleanup")
		}
	})
}

// approvePending approves every waiting proposal (IM messages are proposals by
// policy now) and returns the newest Ticket id, "" when nothing was approved.
func approvePending(t *testing.T, eng *engine.Engine) domain.ID {
	t.Helper()
	for _, d := range eng.Proposals() {
		if _, err := eng.Approve(d.ID); err != nil {
			t.Fatalf("approve %q: %v", d.ID, err)
		}
	}
	if all := eng.AllTickets(); len(all) > 0 {
		return all[len(all)-1].ID
	}
	return ""
}

// ticketAfterProposal approves any proposal an IM trigger produced and returns
// the Ticket id; it asserts that exactly one Ticket now exists.
func ticketAfterProposal(t *testing.T, eng *engine.Engine, what string) domain.ID {
	t.Helper()
	id := approvePending(t, eng)
	if id == "" {
		t.Fatalf("%s: no ticket after approving the IM proposal", what)
	}
	return id
}
