package engine

// verifier-B: adversarial verification of the Wave-2 engine claims (wave2-spec
// §2, fix-engine.md, fix-engine-api.md). The point is to falsify, not confirm:
// the fixtures below are written from the docs/002/003 rules, not from the
// owner's engine_test.go helpers.

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zeokeel/super-agent/internal/domain"
)

// -----------------------------------------------------------------------------
// fixtures
// -----------------------------------------------------------------------------

var v2Base = time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

func v2WS() domain.Workspace {
	return domain.Workspace{ID: "ws_web", Name: "web", Root: "/tmp/v2web",
		Kind: domain.KindRepo, Brief: domain.Brief{Purpose: "web app", Keywords: []string{"webapp"}}}
}

func v2Policy() Policy {
	return Policy{
		Version: "pv-1",
		Rules: []domain.Rule{
			{ID: "implementation", Instruction: "改代码", Enabled: true, Proactive: true},
			{ID: "off-topic", Instruction: "闲聊", Enabled: true},
		},
		Allowed: map[string]map[string]bool{"github": {"lin": true}},
		Bots:    map[string]map[string]bool{"github": {"dependabot[bot]": true}},
		Admissions: map[string]domain.AdmissionRow{
			"github|comment": {Source: "github", Scope: "comment", Observed: true,
				ListedUnaddressed: domain.CellInferred, UnlistedAddressed: domain.CellIgnore,
				UnlistedUnaddressed: domain.CellIgnore},
		},
		AcceptMode: domain.AcceptAuto,
	}
}

type v2TriggerOpt func(*domain.Trigger)

func v2WithThread(id string) v2TriggerOpt {
	return func(t *domain.Trigger) {
		t.Threads = []domain.ThreadRef{{Source: "github", ID: id}}
	}
}

func v2WithScope(scope domain.Scope, defaultWS domain.WorkspaceID) v2TriggerOpt {
	return func(t *domain.Trigger) {
		t.Scope = scope
		t.DefaultWorkspace = defaultWS
	}
}

func v2Trigger(id, actor, text string, human, addressed bool, opts ...v2TriggerOpt) domain.Trigger {
	tr := domain.Trigger{
		ID:               domain.ID("v2-" + id),
		Scope:            domain.WorkspaceScope("ws_web"),
		Source:           "github",
		Threads:          []domain.ThreadRef{{Source: "github", ID: "owner/repo#12"}},
		Actor:            domain.Actor{ID: actor, Bot: !human},
		Human:            human,
		Addressed:        domain.Addressed{Mention: addressed},
		Row:              "comment",
		ScopeReceivesAll: true,
		DefaultWorkspace: "ws_web",
		Text:             text,
		At:               v2Base,
	}
	for _, o := range opts {
		o(&tr)
	}
	return tr
}

func v2Engine(t *testing.T) *Engine {
	t.Helper()
	e := New(RuleAssessor{})
	e.Register(v2WS(), v2Policy())
	e.SetClock(func() time.Time { return v2Base })
	return e
}

// v2Accept drives Receive + EvaluateDue one time and returns the created Ticket.
func v2Accept(t *testing.T, e *Engine, tr domain.Trigger) domain.Ticket {
	t.Helper()
	if _, err := e.Receive(tr); err != nil && !errors.Is(err, ErrDuplicate) {
		t.Fatalf("Receive: %v", err)
	}
	outs, err := e.EvaluateDue(v2Base)
	if err != nil {
		t.Fatalf("EvaluateDue: %v", err)
	}
	for _, o := range outs {
		if o.Ticket != "" {
			if tk, ok := e.TicketSnapshot(o.Ticket); ok {
				return tk
			}
		}
	}
	t.Fatalf("no Ticket created: outs=%+v decisions=%+v", outs, e.Decisions())
	return domain.Ticket{}
}

// v2TurnTaken simulates the worker taking and reading the Ticket's inputs.
func v2TurnTaken(t *testing.T, e *Engine, id domain.ID) {
	t.Helper()
	if err := e.MarkInputsDelivered(id); err != nil {
		t.Fatal(err)
	}
	if err := e.MarkInputsRead(id); err != nil {
		t.Fatal(err)
	}
}

type v2Assessor struct {
	mu       sync.Mutex
	calls    int
	batches  [][]domain.ID
	inFlight int
	maxIn    int
	started  chan struct{} // closed when the first Assess starts (when non-nil)
	release  chan struct{} // Assess blocks until closed (when non-nil)
	out      AssessOutput
	err      error
}

func (a *v2Assessor) Assess(_ context.Context, in AssessInput) (AssessOutput, error) {
	a.mu.Lock()
	a.calls++
	a.inFlight++
	if a.inFlight > a.maxIn {
		a.maxIn = a.inFlight
	}
	a.batches = append(a.batches, append([]domain.ID(nil), in.Trigger.ID))
	if a.started != nil && a.calls == 1 {
		close(a.started)
	}
	started, release := a.started, a.release
	a.mu.Unlock()
	if started != nil && release != nil && a.calls == 1 {
		<-release
	}
	a.mu.Lock()
	a.inFlight--
	a.mu.Unlock()
	if a.err != nil {
		return AssessOutput{}, a.err
	}
	if a.out.Kind != "" {
		out := a.out
		if out.Workspace == "" {
			out.Workspace = in.Trigger.DefaultWorkspace
		}
		return out, nil
	}
	return AssessOutput{Kind: domain.DecAccept, Workspace: in.Trigger.DefaultWorkspace,
		Reasons: []domain.Reason{{Code: domain.ReasonSaid}}}, nil
}

func (a *v2Assessor) stats() (calls int, batches [][]domain.ID, maxIn int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.calls, append([][]domain.ID(nil), a.batches...), a.maxIn
}

// -----------------------------------------------------------------------------
// §2.2 deterministic gate
// -----------------------------------------------------------------------------

// The admission table has nine cells (3 columns x 3 cell values). Every cell must
// be read exactly as configured, and an explicit cell on the unlisted-addressed
// column must beat "not on the allow list".
func TestV2GateAdmissionNineCells(t *testing.T) {
	type cellCase struct {
		listed, addressed bool
		column            string
	}
	columns := []cellCase{
		{true, false, "listedUnaddressed"},
		{false, true, "unlistedAddressed"},
		{false, false, "unlistedUnaddressed"},
	}
	for _, col := range columns {
		for _, cell := range []domain.AdmissionCell{domain.CellExplicit, domain.CellInferred, domain.CellIgnore} {
			row := domain.AdmissionRow{Source: "github", Scope: "comment", Observed: true}
			switch col.column {
			case "listedUnaddressed":
				row.ListedUnaddressed = cell
			case "unlistedAddressed":
				row.UnlistedAddressed = cell
			case "unlistedUnaddressed":
				row.UnlistedUnaddressed = cell
			}
			p := v2Policy()
			p.Admissions = map[string]domain.AdmissionRow{"github|comment": row}
			tr := v2Trigger("cell", "lin", "x", true, col.addressed)
			if !col.listed {
				tr.Actor.ID = "stranger"
			}
			g := gate(p, tr, false)
			want := domain.StrengthNotAdmitted
			switch {
			case cell == domain.CellExplicit:
				want = domain.StrengthExplicit
			case cell == domain.CellInferred:
				want = domain.StrengthInferred
			}
			if g.Strength != want {
				t.Errorf("%s cell=%s: strength=%s want=%s (admitted=%v reasons=%v)",
					col.column, cell, g.Strength, want, g.Admitted, g.Reasons)
			}
		}
	}
	// listed+addressed never reads a cell: it is explicit.
	p := v2Policy()
	p.Admissions["github|comment"] = domain.AdmissionRow{Source: "github", Scope: "comment",
		Observed: true, ListedUnaddressed: domain.CellIgnore,
		UnlistedAddressed: domain.CellIgnore, UnlistedUnaddressed: domain.CellIgnore}
	if g := gate(p, v2Trigger("x", "lin", "hi", true, true), false); g.Strength != domain.StrengthExplicit {
		t.Fatalf("listed+addressed strength=%s, want explicit", g.Strength)
	}
	// Without observe/receive-all the unaddressed cells are unreachable.
	p2 := v2Policy()
	p2.Admissions["github|comment"] = domain.AdmissionRow{Source: "github", Scope: "comment",
		Observed: false, ListedUnaddressed: domain.CellExplicit}
	tr := v2Trigger("y", "lin", "hi", true, false)
	tr.ScopeReceivesAll = false
	if g := gate(p2, tr, false); g.Admitted {
		t.Fatalf("an unobserved, unreceived unaddressed message was admitted (strength %s)", g.Strength)
	}
}

// Six canonical strength cases from 002:11-16/50-54/63-79.
func TestV2GateStrengthSixCases(t *testing.T) {
	p := v2Policy()
	cases := []struct {
		name string
		tr   domain.Trigger
		want domain.Strength
	}{
		{"operator's own account", func() domain.Trigger {
			tr := v2Trigger("s1", "lin", "fix", true, false)
			tr.Self = true
			return tr
		}(), domain.StrengthExplicit},
		{"local panel", func() domain.Trigger {
			tr := v2Trigger("s2", "operator", "fix", true, false)
			tr.Source = "local"
			return tr
		}(), domain.StrengthExplicit},
		{"listed+addressed", v2Trigger("s3", "lin", "fix", true, true), domain.StrengthExplicit},
		{"listed+unaddressed (cell inferred)", v2Trigger("s4", "lin", "fix", true, false), domain.StrengthInferred},
		{"unlisted+addressed (cell ignore)", v2Trigger("s5", "stranger", "fix", true, true), domain.StrengthNotAdmitted},
		{"unlisted+unaddressed (cell ignore)", v2Trigger("s6", "stranger", "fix", true, false), domain.StrengthNotAdmitted},
	}
	for _, c := range cases {
		if g := gate(p, c.tr, false); g.Strength != c.want {
			t.Errorf("%s: strength=%s want=%s (reasons %v)", c.name, g.Strength, c.want, g.Reasons)
		}
	}
	// Follow-up on a Thread with a live Ticket: listed sender is explicit, an
	// unlisted sender is inferred unless accept mode is off.
	if g := gate(p, v2Trigger("f1", "lin", "more", true, false), true); g.Strength != domain.StrengthExplicit {
		t.Errorf("listed follow-up strength=%s want explicit", g.Strength)
	}
	if g := gate(p, v2Trigger("f2", "stranger", "more", true, false), true); g.Strength != domain.StrengthInferred {
		t.Errorf("unlisted follow-up strength=%s want inferred", g.Strength)
	}
	off := p
	off.AcceptMode = domain.AcceptOff
	if g := gate(off, v2Trigger("f3", "stranger", "more", true, false), true); g.Admitted {
		t.Errorf("off-mode admitted an unlisted follow-up (strength %s)", g.Strength)
	}
}

// A listed bot can only produce an inferred candidate, even when it addresses the
// agent; an unlisted bot is never admitted.
func TestV2BotNeverExplicit(t *testing.T) {
	p := v2Policy()
	bot := v2Trigger("b1", "dependabot[bot]", "bump deps", false, true)
	if g := gate(p, bot, false); g.Strength != domain.StrengthInferred {
		t.Fatalf("listed bot strength=%s, want inferred (bots are never explicit)", g.Strength)
	}
	unlisted := v2Trigger("b2", "some-bot[bot]", "bump", false, true)
	if g := gate(p, unlisted, false); g.Admitted {
		t.Fatalf("unlisted bot was admitted (strength %s)", g.Strength)
	}
	// Even the strongest human path must not turn a bot explicit.
	selfBot := v2Trigger("b3", "dependabot[bot]", "x", false, true)
	selfBot.Self = true
	if g := gate(p, selfBot, false); g.Strength == domain.StrengthExplicit {
		t.Fatal("a bot message with Self=true became explicit")
	}
}

// The model's output type has no strength field at all, and a model accept can
// never upgrade a gate-computed inferred strength.
func TestV2ModelCannotDeclareStrength(t *testing.T) {
	if f, ok := reflect.TypeOf(AssessOutput{}).FieldByName("Strength"); ok {
		t.Fatalf("AssessOutput still has field %q: the model can declare a strength", f.Name)
	}
	e := New(&v2Assessor{out: AssessOutput{Kind: domain.DecAccept,
		Rules: []domain.RuleHit{{ID: "implementation", Matched: true}, {ID: "off-topic", Matched: false}}}})
	e.Register(v2WS(), v2Policy())
	e.SetClock(func() time.Time { return v2Base })
	// listed+unaddressed → inferred by gate; model accepts.
	tk := v2Accept(t, e, v2Trigger("m1", "lin", "do it", true, false))
	if tk.Mandate.Strength != domain.StrengthInferred {
		t.Fatalf("mandate strength=%s, want the gate's inferred", tk.Mandate.Strength)
	}
	if tk.Mandate.Basis.Via != domain.ViaAdmission {
		t.Fatalf("mandate basis.via=%s, want admission", tk.Mandate.Basis.Via)
	}
}

// -----------------------------------------------------------------------------
// §2.1 two-phase Eval
// -----------------------------------------------------------------------------

func TestV2ReceiveDoesNotCallModel(t *testing.T) {
	a := &v2Assessor{}
	e := New(a)
	e.Register(v2WS(), v2Policy())
	e.SetClock(func() time.Time { return v2Base })
	out, err := e.Receive(v2Trigger("r1", "lin", "do it", true, true))
	if err != nil {
		t.Fatal(err)
	}
	if calls, _, _ := a.stats(); calls != 0 {
		t.Fatalf("Receive called the assessor %d times; it must not call the model", calls)
	}
	if out.Kind != domain.DispEvaluating || out.Ticket != "" {
		t.Fatalf("Receive outcome=%+v, want evaluating and no Ticket", out)
	}
	if e.QueueLen() != 1 {
		t.Fatalf("QueueLen=%d, want 1", e.QueueLen())
	}
	if _, err := e.EvaluateDue(v2Base); err != nil {
		t.Fatal(err)
	}
	if calls, _, _ := a.stats(); calls != 1 {
		t.Fatalf("EvaluateDue called the assessor %d times, want 1", calls)
	}
}

// While the assessor (outside the lock) is blocked, Receive/TicketSnapshot/read
// calls must not deadlock; and only one Assess may be in flight.
func TestV2EvaluateDueRunsOutsideLock(t *testing.T) {
	a := &v2Assessor{started: make(chan struct{}), release: make(chan struct{})}
	e := New(a)
	e.Register(v2WS(), v2Policy())
	e.SetClock(func() time.Time { return v2Base })
	if _, err := e.Receive(v2Trigger("l1", "lin", "one", true, true)); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := e.EvaluateDue(v2Base)
		done <- err
	}()
	select {
	case <-a.started:
	case <-time.After(5 * time.Second):
		t.Fatal("EvaluateDue never entered the assessor")
	}
	// The engine lock must be free while the assessor runs.
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		_ = e.AllTickets()
		_ = e.Evals()
		if _, err := e.Receive(v2Trigger("l2", "lin", "two", true, true, v2WithThread("owner/repo#99"))); err != nil {
			t.Errorf("concurrent Receive: %v", err)
		}
	}()
	select {
	case <-readDone:
	case <-time.After(2 * time.Second):
		t.Fatal("engine calls blocked while the assessor was running: the lock is still held across the model call")
	}
	close(a.release)
	if err := <-done; err != nil {
		t.Fatalf("EvaluateDue: %v", err)
	}
	if _, _, maxIn := a.stats(); maxIn != 1 {
		t.Fatalf("max concurrent Assess calls = %d, want 1", maxIn)
	}
}

// Two Triggers of the same Thread that arrive before the pass are judged as one
// batch, and the batch keeps its order.
func TestV2SameThreadBatchMerges(t *testing.T) {
	a := &v2Assessor{}
	e := New(a)
	e.Register(v2WS(), v2Policy())
	e.SetClock(func() time.Time { return v2Base })
	for _, tr := range []domain.Trigger{
		v2Trigger("batch1", "lin", "first", true, true),
		v2Trigger("batch2", "lin", "second", true, true),
	} {
		if _, err := e.Receive(tr); err != nil {
			t.Fatal(err)
		}
	}
	if e.QueueLen() != 1 {
		t.Fatalf("QueueLen=%d, want 1 merged Eval", e.QueueLen())
	}
	evs := e.Evals()
	if len(evs) != 1 || len(evs[0].TriggerIDs) != 2 {
		t.Fatalf("Evals=%+v, want one Eval with two Triggers", evs)
	}
	if _, err := e.EvaluateDue(v2Base); err != nil {
		t.Fatal(err)
	}
	calls, _, _ := a.stats()
	if calls != 1 {
		t.Fatalf("assessor calls=%d, want 1 for the merged batch", calls)
	}
}

// While one Eval of a Thread is running, a newly arrived Trigger is pending: it
// does not start a second concurrent assessment.
func TestV2OneEvalInFlightPerThread(t *testing.T) {
	a := &v2Assessor{started: make(chan struct{}), release: make(chan struct{})}
	e := New(a)
	e.Register(v2WS(), v2Policy())
	e.SetClock(func() time.Time { return v2Base })
	if _, err := e.Receive(v2Trigger("o1", "lin", "one", true, true)); err != nil {
		t.Fatal(err)
	}
	done := make(chan []Outcome, 1)
	go func() {
		outs, _ := e.EvaluateDue(v2Base)
		done <- outs
	}()
	<-a.started
	out, err := e.Receive(v2Trigger("o2", "lin", "two", true, true))
	if err != nil {
		t.Fatal(err)
	}
	if out.Kind != domain.DispEvaluating {
		t.Fatalf("Receive during a running Eval returned %s", out.Kind)
	}
	if calls, _, _ := a.stats(); calls != 1 {
		t.Fatalf("assessor calls=%d while the first Eval was blocked, want 1", calls)
	}
	close(a.release)
	<-done
	// The pending Trigger forms the next batch, not a lost one.
	if e.QueueLen() != 1 {
		t.Fatalf("pending batch was not queued after the first Eval: QueueLen=%d", e.QueueLen())
	}
	if _, err := e.EvaluateDue(v2Base.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	calls, _, _ := a.stats()
	if calls != 2 {
		t.Fatalf("assessor calls=%d, want 2 (the second batch runs after the first)", calls)
	}
}

// Expiry: the third consecutive timeout records `unavailable`; an undelivered
// candidate does not expire.
func TestV2ExpiryThreeTimesUnavailable(t *testing.T) {
	e := New(&v2Assessor{})
	e.Register(v2WS(), v2Policy())
	e.SetClock(func() time.Time { return v2Base })
	e.SetEvalTimeout(30 * time.Second)
	if _, err := e.Receive(v2Trigger("e1", "lin", "work", true, true)); err != nil {
		t.Fatal(err)
	}
	evs := e.Evals()
	if len(evs) != 1 {
		t.Fatalf("Evals=%v", evs)
	}
	key := evs[0].Thread
	seed := func() {
		e.mu.Lock()
		defer e.mu.Unlock()
		slot := e.evals[key]
		slot.Eval.State = domain.EvalQueued
		slot.Eval.CandidateDelivered = true
		slot.Eval.Deadline = v2Base.Add(-time.Second)
	}
	// Attempt 1 and 2: requeued, still not unavailable.
	for i := 1; i <= 2; i++ {
		seed()
		e.mu.Lock()
		e.expireDueLocked(v2Base)
		slot := e.evals[key]
		e.mu.Unlock()
		if slot.Eval.State != domain.EvalQueued {
			t.Fatalf("after expiry %d: state=%s, want queued", i, slot.Eval.State)
		}
		if slot.Eval.Attempts != i {
			t.Fatalf("after expiry %d: attempts=%d", i, slot.Eval.Attempts)
		}
	}
	// Third expiry: unavailable + a Decision of kind unavailable.
	seed()
	e.mu.Lock()
	e.expireDueLocked(v2Base)
	slot := e.evals[key]
	e.mu.Unlock()
	if slot.Eval.State != domain.EvalUnavailable {
		t.Fatalf("third expiry: state=%s, want unavailable", slot.Eval.State)
	}
	var gotUnavailable bool
	for _, d := range e.Decisions() {
		if d.Kind == domain.DecUnavailable {
			gotUnavailable = true
		}
	}
	if !gotUnavailable {
		t.Fatalf("no unavailable Decision recorded: %+v", e.Decisions())
	}

	// Undelivered candidate: a past deadline must not expire it (the rule is
	// "a candidate that never reached the main Session does not expire").
	e2 := New(&v2Assessor{})
	e2.Register(v2WS(), v2Policy())
	e2.SetClock(func() time.Time { return v2Base })
	e2.SetEvalTimeout(30 * time.Second)
	if _, err := e2.Receive(v2Trigger("e2", "lin", "work", true, true)); err != nil {
		t.Fatal(err)
	}
	key2 := e2.Evals()[0].Thread
	e2.mu.Lock()
	e2.evals[key2].Eval.CandidateDelivered = false
	e2.evals[key2].Eval.Deadline = v2Base.Add(-time.Second)
	e2.expireDueLocked(v2Base)
	state2 := e2.evals[key2].Eval.State
	attempts2 := e2.evals[key2].Eval.Attempts
	e2.mu.Unlock()
	if state2 != domain.EvalQueued || attempts2 != 0 {
		t.Fatalf("an undelivered candidate expired: state=%s attempts=%d", state2, attempts2)
	}
}

// Pause: no expiry while paused, and the deadline shifts by the pause length on
// resume.
func TestV2PauseShiftsDeadline(t *testing.T) {
	now := v2Base
	e := New(&v2Assessor{})
	e.Register(v2WS(), v2Policy())
	e.SetClock(func() time.Time { return now })
	e.SetEvalTimeout(time.Minute)
	if _, err := e.Receive(v2Trigger("p1", "lin", "work", true, true)); err != nil {
		t.Fatal(err)
	}
	key := e.Evals()[0].Thread
	e.mu.Lock()
	e.evals[key].Eval.CandidateDelivered = true
	e.evals[key].Eval.Deadline = now.Add(10 * time.Second)
	e.mu.Unlock()
	e.Pause()
	if !e.Paused() {
		t.Fatal("Paused() = false after Pause()")
	}
	before := e.Evals()[0].Deadline
	now = now.Add(10 * time.Minute)
	if _, err := e.EvaluateDue(now); err != nil {
		t.Fatal(err)
	}
	for _, d := range e.Decisions() {
		if d.Kind == domain.DecUnavailable {
			t.Fatal("an Eval expired while processing was paused")
		}
	}
	e.Resume()
	after := e.Evals()[0].Deadline
	if !after.After(before) {
		t.Fatalf("deadline did not shift by the pause: before=%s after=%s", before, after)
	}
	if after.Sub(before) != 10*time.Minute {
		t.Fatalf("deadline shifted by %s, want the 10m pause", after.Sub(before))
	}
}

// A Policy change requeues Evals under the new version and gives the affected
// live Ticket a policy Input.
func TestV2PolicyChangedRequeues(t *testing.T) {
	e := v2Engine(t)
	tk := v2Accept(t, e, v2Trigger("pc1", "lin", "work", true, true))
	if tk.Mandate.PolicyVersion != "pv-1" {
		t.Fatalf("mandate policy version=%q", tk.Mandate.PolicyVersion)
	}
	if _, err := e.Receive(v2Trigger("pc2", "lin", "another", true, true, v2WithThread("owner/repo#77"))); err != nil {
		t.Fatal(err)
	}
	if e.QueueLen() != 1 {
		t.Fatalf("QueueLen=%d", e.QueueLen())
	}
	if err := e.PolicyChanged("pv-1", "pv-2"); err != nil {
		t.Fatal(err)
	}
	var requeued *domain.Eval
	for _, ev := range e.Evals() {
		if ev.State == domain.EvalQueued {
			evCopy := ev
			requeued = &evCopy
		}
	}
	if requeued == nil || requeued.PolicyVer != "pv-2" {
		t.Fatalf("queued Eval not requeued under the new policy: %+v", e.Evals())
	}
	snap, _ := e.TicketSnapshot(tk.ID)
	var gotPolicyInput bool
	for _, in := range snap.Inputs {
		if in.Kind == domain.InputPolicy {
			gotPolicyInput = true
		}
	}
	if !gotPolicyInput {
		t.Fatalf("no policy Input on the affected Ticket: %+v", snap.Inputs)
	}
}

// -----------------------------------------------------------------------------
// §2.3 lifecycle
// -----------------------------------------------------------------------------

func TestV2LifecycleReadyDroppedStopRetryAttention(t *testing.T) {
	// ready with no follows ends the Ticket as completed.
	e := v2Engine(t)
	tk := v2Accept(t, e, v2Trigger("lc1", "lin", "work", true, true))
	v2TurnTaken(t, e, tk.ID)
	if err := e.Report(tk.ID, domain.ReportReady, "done"); err != nil {
		t.Fatal(err)
	}
	snap, _ := e.TicketSnapshot(tk.ID)
	if snap.Ended == nil || snap.Ended.Outcome != domain.OutcomeCompleted {
		t.Fatalf("ready report did not complete the Ticket: %+v", snap.Ended)
	}
	if domain.StageOf(&snap, v2Base) != domain.StageEnded {
		t.Fatalf("stage=%s, want ended", domain.StageOf(&snap, v2Base))
	}

	// dropped ends the Ticket immediately.
	e = v2Engine(t)
	tk = v2Accept(t, e, v2Trigger("lc2", "lin", "work", true, true))
	v2TurnTaken(t, e, tk.ID)
	if err := e.Report(tk.ID, domain.ReportDropped, "not needed"); err != nil {
		t.Fatal(err)
	}
	snap, _ = e.TicketSnapshot(tk.ID)
	if snap.Ended == nil || snap.Ended.Outcome != domain.OutcomeDropped {
		t.Fatalf("dropped report did not end the Ticket: %+v", snap.Ended)
	}

	// stop withdraws the Mandate.
	e = v2Engine(t)
	tk = v2Accept(t, e, v2Trigger("lc3", "lin", "work", true, true))
	if err := e.StopTicket(tk.ID, "operator asked"); err != nil {
		t.Fatal(err)
	}
	snap, _ = e.TicketSnapshot(tk.ID)
	if snap.Ended == nil || snap.Ended.Outcome != domain.OutcomeStopped {
		t.Fatalf("stop outcome=%+v", snap.Ended)
	}
	if snap.Mandate.Withdrawn == "" {
		t.Fatal("StopTicket did not withdraw the Mandate")
	}

	// retry lifts a blocked Ticket and starts a new pending Assignment.
	e = v2Engine(t)
	tk = v2Accept(t, e, v2Trigger("lc4", "lin", "work", true, true))
	v2TurnTaken(t, e, tk.ID)
	if err := e.Report(tk.ID, domain.ReportBlocked, "need credentials"); err != nil {
		t.Fatal(err)
	}
	snap, _ = e.TicketSnapshot(tk.ID)
	if got := domain.StageOf(&snap, v2Base); got != domain.StagePersonBlocked {
		t.Fatalf("blocked stage=%s", got)
	}
	if !snap.CanRetry() {
		t.Fatal("CanRetry=false on a blocked Ticket")
	}
	if err := e.RetryTicket(tk.ID); err != nil {
		t.Fatalf("RetryTicket: %v", err)
	}
	snap, _ = e.TicketSnapshot(tk.ID)
	if snap.Ended != nil {
		t.Fatalf("retry left the Ticket ended: %+v", snap.Ended)
	}
	if snap.LastAssignment().State != domain.AssignPending {
		t.Fatalf("retry assignment state=%s, want pending", snap.LastAssignment().State)
	}

	// AnswerAttention clears the question and delivers an Input.
	e = v2Engine(t)
	tk = v2Accept(t, e, v2Trigger("lc5", "lin", "work", true, true))
	if err := e.RaiseAttention(tk.ID, "which env?", []string{"dev", "prod"}); err != nil {
		t.Fatal(err)
	}
	snap, _ = e.TicketSnapshot(tk.ID)
	if got := domain.StageOf(&snap, v2Base); got != domain.StagePersonAnswer {
		t.Fatalf("attention stage=%s", got)
	}
	if err := e.AnswerAttention(tk.ID, "prod"); err != nil {
		t.Fatalf("AnswerAttention: %v", err)
	}
	snap, _ = e.TicketSnapshot(tk.ID)
	if snap.Attention != nil {
		t.Fatal("AnswerAttention did not clear the Attention")
	}
	var answerInput bool
	for _, in := range snap.Inputs {
		if in.Kind == domain.InputText && in.Text == "prod" {
			answerInput = true
		}
	}
	if !answerInput {
		t.Fatalf("answer was not delivered as an Input: %+v", snap.Inputs)
	}
}

// A Ticket with follows waits for the pull request to close; the close decides the
// outcome (merged/closed), and an unfinished Ticket is not ended.
func TestV2FollowsOutcomes(t *testing.T) {
	e := v2Engine(t)
	tk := v2Accept(t, e, v2Trigger("f1", "lin", "work", true, true))
	prThread := domain.ThreadKey(domain.WorkspaceScope("ws_web"), "github", "owner/repo#44")
	if err := e.BindFollow(tk.ID, prThread, true); err != nil {
		t.Fatal(err)
	}
	v2TurnTaken(t, e, tk.ID)
	if err := e.Report(tk.ID, domain.ReportReady, "PR open"); err != nil {
		t.Fatal(err)
	}
	snap, _ := e.TicketSnapshot(tk.ID)
	if snap.Ended != nil {
		t.Fatalf("a Ticket with an open follow ended on ready: %+v", snap.Ended)
	}
	if got := domain.StageOf(&snap, v2Base); got != domain.StagePersonReview {
		t.Fatalf("stage=%s, want person/review", got)
	}
	if err := e.ThreadClosed(prThread, "merged", v2Base.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	snap, _ = e.TicketSnapshot(tk.ID)
	if snap.Ended == nil || snap.Ended.Outcome != domain.OutcomeMerged {
		t.Fatalf("PR close did not end the Ticket as merged: %+v", snap.Ended)
	}
	// An unfinished Ticket (unread input) is not ended by a close.
	e = v2Engine(t)
	tk = v2Accept(t, e, v2Trigger("f2", "lin", "work", true, true))
	if err := e.BindFollow(tk.ID, prThread, true); err != nil {
		t.Fatal(err)
	}
	// Its initial Trigger Input is still unread → Unfinished.
	if err := e.ThreadClosed(prThread, "closed", v2Base.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	snap, _ = e.TicketSnapshot(tk.ID)
	if snap.Ended != nil {
		t.Fatalf("a close ended a Ticket with unread inputs: %+v", snap.Ended)
	}
}

// -----------------------------------------------------------------------------
// §2.4 subscription / follow-up
// -----------------------------------------------------------------------------

func TestV2OwnedFollowupHandBackAndDrop(t *testing.T) {
	e := v2Engine(t)
	tk := v2Accept(t, e, v2Trigger("o1", "lin", "work", true, true))
	v2TurnTaken(t, e, tk.ID)

	// A person's message on the Ticket's own Thread is owned by its Executor: no
	// Eval is queued for it.
	before := e.QueueLen()
	out, err := e.Receive(v2Trigger("o2", "stranger", "any news?", true, false))
	if err != nil {
		t.Fatal(err)
	}
	if out.Kind != domain.DispOwned {
		t.Fatalf("owned follow-up disposition=%s, want owned", out.Kind)
	}
	if e.QueueLen() != before {
		t.Fatalf("an owned human message queued an Eval: QueueLen %d -> %d", before, e.QueueLen())
	}
	snap, _ := e.TicketSnapshot(tk.ID)
	var owned bool
	for _, in := range snap.Inputs {
		if in.Kind == domain.InputTrigger && in.Thread == domain.ThreadKey(domain.WorkspaceScope("ws_web"), "github", "owner/repo#12") && !in.Context {
			owned = true
		}
	}
	if !owned {
		t.Fatalf("owned message was not delivered as an Input: %+v", snap.Inputs)
	}

	// HandBack releases it and requeues the Trigger for Eval.
	thread := domain.ThreadKey(domain.WorkspaceScope("ws_web"), "github", "owner/repo#12")
	if err := e.HandBack(tk.ID, thread, "not ours"); err != nil {
		t.Fatal(err)
	}
	if e.QueueLen() == 0 {
		t.Fatal("HandBack did not requeue the owned Trigger for Eval")
	}

	// DropSubscription removes the Ticket's subscription to the Thread.
	if err := e.DropSubscription(tk.ID, thread, "person asked"); err != nil {
		t.Fatal(err)
	}
	snap, _ = e.TicketSnapshot(tk.ID)
	for _, s := range snap.SubscribedThreads {
		if s == thread {
			t.Fatalf("subscription to %q survived DropSubscription", thread)
		}
	}
	// MarkUnrelated refuses the source Thread, but works for a related Thread.
	if err := e.MarkUnrelated(tk.ID, thread, "unrelated"); err == nil {
		t.Fatal("MarkUnrelated accepted the Ticket's source thread")
	}
	related := domain.ThreadKey(domain.WorkspaceScope("ws_web"), "github", "owner/repo#55")
	if err := e.BindFollow(tk.ID, related, false); err != nil {
		t.Fatal(err)
	}
	if err := e.MarkUnrelated(tk.ID, related, "not related"); err != nil {
		t.Fatalf("MarkUnrelated(related): %v", err)
	}
}

// An ended-stopped Ticket is not resumed; an ended-not-stopped one is, unless
// revalidation fails — then the Mandate is revoked and the message falls through.
func TestV2EndedResumeAndRevalidateRevoke(t *testing.T) {
	// Not stopped → resume with no Eval/Decision.
	e := v2Engine(t)
	tk := v2Accept(t, e, v2Trigger("r1", "lin", "work", true, true))
	v2TurnTaken(t, e, tk.ID)
	if err := e.Report(tk.ID, domain.ReportReady, "done"); err != nil {
		t.Fatal(err)
	}
	decisionsBefore := len(e.Decisions())
	out, err := e.Receive(v2Trigger("r2", "lin", "continue", true, false))
	if err != nil {
		t.Fatal(err)
	}
	if out.Kind != domain.DispResumed {
		t.Fatalf("ended-not-stopped disposition=%s, want resumed", out.Kind)
	}
	snap, _ := e.TicketSnapshot(tk.ID)
	if snap.Ended != nil {
		t.Fatal("resumed Ticket is still ended")
	}
	if len(e.Decisions()) != decisionsBefore {
		t.Fatal("resume created a new Decision (it must not re-run Eval)")
	}
	if snap.LastAssignment().State != domain.AssignPending {
		t.Fatalf("resume assignment=%s", snap.LastAssignment().State)
	}

	// Stopped → not resumed.
	e2 := v2Engine(t)
	tk2 := v2Accept(t, e2, v2Trigger("r3", "lin", "work", true, true))
	if err := e2.StopTicket(tk2.ID, "stop"); err != nil {
		t.Fatal(err)
	}
	out, err = e2.Receive(v2Trigger("r4", "lin", "continue", true, false))
	if err != nil {
		t.Fatal(err)
	}
	if out.Kind == domain.DispResumed {
		t.Fatal("a Ticket stopped by a person received a resume")
	}
	snap2, _ := e2.TicketSnapshot(tk2.ID)
	if snap2.Ended == nil {
		t.Fatal("stopped Ticket was reopened")
	}

	// Revalidation failure → revoke, do not reopen.
	e3 := v2Engine(t)
	tk3 := v2Accept(t, e3, v2Trigger("r5", "lin", "work", true, true))
	v2TurnTaken(t, e3, tk3.ID)
	if err := e3.Report(tk3.ID, domain.ReportReady, "done"); err != nil {
		t.Fatal(err)
	}
	p := v2Policy()
	p.Allowed = map[string]map[string]bool{} // the sender is no longer allowed
	if err := e3.SetPolicy("ws_web", p); err != nil {
		t.Fatal(err)
	}
	if _, err := e3.Receive(v2Trigger("r6", "lin", "continue", true, false)); err != nil {
		t.Fatal(err)
	}
	snap3, _ := e3.TicketSnapshot(tk3.ID)
	if snap3.Ended == nil {
		t.Fatal("a Ticket whose Mandate no longer holds was reopened")
	}
	if snap3.Mandate.Revoked == "" {
		t.Fatal("revalidation failure did not revoke the Mandate")
	}
}

// -----------------------------------------------------------------------------
// TicketSnapshot under concurrency (-race is the evidence)
// -----------------------------------------------------------------------------

func TestV2TicketSnapshotRace(t *testing.T) {
	e := v2Engine(t)
	tk := v2Accept(t, e, v2Trigger("snap1", "lin", "work", true, true))
	var stop atomic.Bool
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !stop.Load() {
				if snap, ok := e.TicketSnapshot(tk.ID); ok {
					_ = snap.LastReport()
					_ = snap.CanRetry()
				}
				_ = e.AllTickets()
			}
		}()
	}
	for i := 0; i < 200; i++ {
		_ = e.DeliverInput(tk.ID, domain.Input{Text: fmt.Sprintf("note-%d", i), Kind: domain.InputNote})
		_ = e.Report(tk.ID, domain.ReportProgress, "working")
		_ = e.MarkInputsDelivered(tk.ID)
	}
	stop.Store(true)
	wg.Wait()
}

// TicketSnapshot must not alias nested pointers: a panel reading it must not be
// able to rewrite a follow's close time or a report.
func TestV2TicketSnapshotDoesNotAliasNestedPointers(t *testing.T) {
	e := v2Engine(t)
	tk := v2Accept(t, e, v2Trigger("alias1", "lin", "work", true, true))
	prThread := domain.ThreadKey(domain.WorkspaceScope("ws_web"), "github", "owner/repo#44")
	if err := e.BindFollow(tk.ID, prThread, true); err != nil {
		t.Fatal(err)
	}
	closed := v2Base.Add(time.Minute)
	if err := e.ThreadClosed(prThread, "merged", closed); err != nil {
		t.Fatal(err)
	}
	snap, _ := e.TicketSnapshot(tk.ID)
	if len(snap.Follows) == 0 || snap.Follows[0].ClosedAt == nil {
		t.Fatalf("fixture: follow not closed: %+v", snap.Follows)
	}
	mutated := closed.Add(24 * time.Hour)
	*snap.Follows[0].ClosedAt = mutated
	again, _ := e.TicketSnapshot(tk.ID)
	if len(again.Follows) > 0 && again.Follows[0].ClosedAt != nil && again.Follows[0].ClosedAt.Equal(mutated) {
		t.Fatal("TicketSnapshot aliases Follow.ClosedAt (*time.Time): a reader rewrote engine state")
	}
}

// Feed must not hand out internal Ticket pointers.
func TestV2FeedDoesNotExposeEnginePointers(t *testing.T) {
	e := v2Engine(t)
	tk := v2Accept(t, e, v2Trigger("feed1", "lin", "work", true, true))
	// A second trigger fans out to the live Ticket and records it in the
	// disposition, which is where Feed attaches its Ticket.
	if _, err := e.Receive(v2Trigger("feed2", "someone", "context", true, false)); err != nil {
		t.Fatal(err)
	}
	var feedTicket *domain.Ticket
	for _, it := range e.Feed() {
		if it.Ticket != nil {
			feedTicket = it.Ticket
		}
	}
	if feedTicket == nil {
		t.Fatalf("fixture: Feed attached no Ticket")
	}
	feedTicket.Title = "mutated through Feed"
	snap, _ := e.TicketSnapshot(tk.ID)
	if snap.Title == "mutated through Feed" {
		t.Fatal("Feed exposes an internal *Ticket: a reader can rewrite engine state")
	}
}

// When revalidation fails, the person's message must fall back to Eval instead of
// being swallowed: it is a new request under the current Policy.
func TestV2RevalidationFailureReturnsMessageToEval(t *testing.T) {
	e := v2Engine(t)
	tk := v2Accept(t, e, v2Trigger("rev1", "lin", "work", true, true))
	v2TurnTaken(t, e, tk.ID)
	if err := e.Report(tk.ID, domain.ReportReady, "done"); err != nil {
		t.Fatal(err)
	}
	p := v2Policy()
	p.Allowed = map[string]map[string]bool{}
	if err := e.SetPolicy("ws_web", p); err != nil {
		t.Fatal(err)
	}
	before := e.QueueLen()
	out, err := e.Receive(v2Trigger("rev2", "lin", "also do this", true, true))
	if err != nil {
		t.Fatal(err)
	}
	snap, _ := e.TicketSnapshot(tk.ID)
	if snap.Ended == nil || snap.Mandate.Revoked == "" {
		t.Fatalf("mandate was not revoked: ended=%+v revoked=%q", snap.Ended, snap.Mandate.Revoked)
	}
	// The message must not be lost: it has to be judged under the current Policy,
	// either queued for Eval or already decided — not silently dropped as "resumed".
	if out.Kind == domain.DispResumed {
		t.Fatal("a failed revalidation still reported the message as resumed")
	}
	if e.QueueLen() == before {
		// It may also have been filtered by the gate; then the disposition must say so.
		disp, _ := e.Disposition(domain.ID("v2-rev2"))
		if disp.Kind != domain.DispEvaluating && disp.Kind != domain.DispFiltered {
			t.Fatalf("message neither queued for Eval nor explicitly filtered: %+v", disp)
		}
	}
}
