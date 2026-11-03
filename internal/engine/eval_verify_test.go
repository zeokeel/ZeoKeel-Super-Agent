package engine

// Independent verification (verifier role): two-phase Eval and batching.
// Claims: Receive never calls a model; EvaluateDue produces Tickets; a second
// Trigger on the same Thread while an Eval runs merges into the next batch; a
// Thread has at most one Eval; the assessor runs outside the engine lock.

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zeokeel/super-agent/internal/domain"
)

type vfCountingAssessor struct {
	calls int32
}

func (a *vfCountingAssessor) Assess(ctx context.Context, in AssessInput) (AssessOutput, error) {
	atomic.AddInt32(&a.calls, 1)
	return RuleAssessor{}.Assess(ctx, in)
}

// vfBlockingAssessor blocks inside Assess until release is closed and reports
// each entry through the buffered started channel.
type vfBlockingAssessor struct {
	started chan struct{}
	release chan struct{}
	calls   int32
}

func newVFBlockingAssessor() *vfBlockingAssessor {
	return &vfBlockingAssessor{started: make(chan struct{}, 8), release: make(chan struct{})}
}

func (a *vfBlockingAssessor) Assess(ctx context.Context, in AssessInput) (AssessOutput, error) {
	atomic.AddInt32(&a.calls, 1)
	select {
	case a.started <- struct{}{}:
	default:
	}
	<-a.release
	return RuleAssessor{}.Assess(ctx, in)
}

// vfReceive receives and fails the test on a real error.
func vfReceive(t *testing.T, e *Engine, tr domain.Trigger) {
	t.Helper()
	if _, err := e.Receive(tr); err != nil {
		t.Fatalf("receive: %v", err)
	}
}

// Check 2a: Receive records facts only; the Ticket appears from EvaluateDue.
func TestVerifyReceiveDoesNotCallModel(t *testing.T) {
	a := &vfCountingAssessor{}
	e := New(a)
	e.Register(vfRepo("ws_web"), vfPolicy(domain.AcceptAuto))
	vfReceive(t, e, vfTrigger("r1", "lin", true, true, true))

	if got := atomic.LoadInt32(&a.calls); got != 0 {
		t.Fatalf("Receive called the assessor %d times; it must never call a model", got)
	}
	if e.QueueLen() != 1 {
		t.Fatalf("a queued Eval is expected, queue=%d", e.QueueLen())
	}
	if len(e.AllTickets()) != 0 {
		t.Fatal("Receive created a Ticket; only EvaluateDue may")
	}

	outs, err := e.EvaluateDue(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if got := atomic.LoadInt32(&a.calls); got != 1 {
		t.Fatalf("EvaluateDue assessor calls = %d, want 1", got)
	}
	if len(e.AllTickets()) != 1 {
		t.Fatalf("EvaluateDue produced %d tickets, want 1 (outcomes %+v)", len(e.AllTickets()), outs)
	}
	if e.QueueLen() != 0 {
		t.Fatalf("the judged Eval must leave the queue, queue=%d", e.QueueLen())
	}
}

// Check 2b/2c: while the assessor runs, the engine serves reads and receives, and
// the Thread keeps exactly one Eval. The Trigger that arrived meanwhile is judged
// in the next batch, not the running one.
func TestVerifyAssessorRunsOutsideLockAndBatches(t *testing.T) {
	a := newVFBlockingAssessor()
	e := New(a)
	e.Register(vfRepo("ws_web"), vfPolicy(domain.AcceptAuto))

	tr1 := vfTrigger("b1", "lin", true, true, true)
	vfReceive(t, e, tr1)

	var evalWG sync.WaitGroup
	evalWG.Add(1)
	var evalErr error
	go func() {
		defer evalWG.Done()
		_, evalErr = e.EvaluateDue(time.Now())
	}()
	select {
	case <-a.started:
	case <-time.After(2 * time.Second):
		t.Fatal("the assessor never started")
	}

	// The engine must still serve reads and a new Receive while the model runs:
	// if EvaluateDue held the global mutex, these would block forever.
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = e.AllTickets()
		_, _, _ = e.Counts()
		_ = e.QueueLen()
		_ = e.Evals()
		_ = e.Feed()
		_ = e.Decisions()
		_, _ = e.PolicyOf("ws_web")
		_, _ = e.TicketSnapshot(tr1.ID)
		if _, err := e.Receive(vfTrigger("b2", "lin", true, true, true)); err != nil {
			t.Errorf("receive while evaluating: %v", err)
		}
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("engine reads/Receive blocked while the assessor ran: the model is under the lock")
	}

	// Exactly one Eval exists for the Thread, and it is the running one.
	if evals := e.Evals(); len(evals) != 1 || evals[0].State != domain.EvalRunning {
		t.Fatalf("want one running Eval on the Thread, got %+v", evals)
	}
	e.mu.Lock()
	slot := e.evals[vfThreadKey(tr1)]
	pending := 0
	if slot != nil {
		pending = len(slot.Pending)
	}
	e.mu.Unlock()
	if pending != 1 {
		t.Fatalf("the Trigger that arrived while running must wait for the next batch, pending=%d", pending)
	}

	close(a.release)
	evalWG.Wait()
	if evalErr != nil {
		t.Fatal(evalErr)
	}

	// First batch: only tr1, one Ticket.
	all := e.AllTickets()
	if len(all) != 1 {
		t.Fatalf("first batch produced %d tickets, want 1", len(all))
	}
	var firstDec domain.Decision
	for _, d := range e.Decisions() {
		if len(d.TriggerIDs) > 0 && d.TriggerIDs[0] == tr1.ID {
			firstDec = d
		}
	}
	if len(firstDec.TriggerIDs) != 1 || firstDec.TriggerIDs[0] != tr1.ID {
		t.Fatalf("the running Eval's batch was modified: %+v", firstDec.TriggerIDs)
	}

	// Next batch: tr2, judged by a later EvaluateDue.
	if e.QueueLen() != 1 {
		t.Fatalf("the pending Trigger must become the Thread's next Eval, queue=%d", e.QueueLen())
	}
	if _, err := e.EvaluateDue(time.Now()); err != nil {
		t.Fatal(err)
	}
	if got := len(e.AllTickets()); got != 2 {
		t.Fatalf("second batch produced %d tickets in total, want 2", got)
	}
}

// Check 2d: EvaluateDue is the only path that creates Tickets, and one call judges
// every due Thread (the runtime's 2s pass).
func TestVerifyEvaluateDuePassProducesTickets(t *testing.T) {
	e := New(RuleAssessor{})
	e.Register(vfRepo("ws_web"), vfPolicy(domain.AcceptAuto))
	vfReceive(t, e, vfTrigger("p1", "lin", true, true, true))

	deadline := time.Now().Add(2 * time.Second)
	for len(e.AllTickets()) == 0 && time.Now().Before(deadline) {
		if _, err := e.EvaluateDue(time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	if got := len(e.AllTickets()); got != 1 {
		t.Fatalf("the 2s EvaluateDue pass never produced the Ticket (tickets=%d, queue=%d)", got, e.QueueLen())
	}
	// A second pass with nothing due must be a no-op.
	before := len(e.Decisions())
	if _, err := e.EvaluateDue(time.Now()); err != nil {
		t.Fatal(err)
	}
	if after := len(e.Decisions()); after != before {
		t.Fatalf("an empty pass recorded decisions: %d -> %d", before, after)
	}
}
