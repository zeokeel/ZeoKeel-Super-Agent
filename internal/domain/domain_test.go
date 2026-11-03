package domain

import (
	"testing"
	"time"
)

// Two workspaces must never share a Thread or a Ticket for the same source id.
func TestIDsAreScopedPerWorkspace(t *testing.T) {
	a, b := WorkspaceID("ws_a"), WorkspaceID("ws_b")
	ta := ThreadKey(WorkspaceScope(a), "github", "12")
	tb := ThreadKey(WorkspaceScope(b), "github", "12")
	if ta == tb {
		t.Fatalf("workspace A and B produced the same Thread key %q", ta)
	}
	ka := Key(WorkspaceScope(a), "ticket", "eval-1")
	kb := Key(WorkspaceScope(b), "ticket", "eval-1")
	if ka == kb {
		t.Fatalf("workspaces collided on Ticket id %q", ka)
	}
}

// A global Source has no workspace, so its Trigger ids are scoped to Global.
func TestGlobalScopeIsDistinct(t *testing.T) {
	g := Key(Global, "dingtalk", "m1")
	w := Key(WorkspaceScope("ws_a"), "dingtalk", "m1")
	if g == w {
		t.Fatal("global and workspace ids collided")
	}
}

// Content-derived IDs: equal inputs give equal ids, and a changed input gives a
// new one (001:28 — the basis of idempotence).
func TestIDsAreContentDerived(t *testing.T) {
	a := Key(Global, "github", "12")
	b := Key(Global, "github", "12")
	if a != b {
		t.Fatal("equal inputs must produce equal ids")
	}
	if Key(Global, "github", "13") == a {
		t.Fatal("different inputs must produce different ids")
	}
}

func TestBriefStaleness(t *testing.T) {
	b := Brief{Facts: Facts{ObservedAt: time.Now()}}
	if b.Stale(time.Now()) {
		t.Fatal("fresh facts must not be stale")
	}
	b.Facts.ObservedAt = time.Now().Add(-StaleAfter - time.Hour)
	if !b.Stale(time.Now()) {
		t.Fatal("old facts must be stale")
	}
}

// TestStageOf is the golden stage table of 003:9-17, read top to bottom.
func TestStageOf(t *testing.T) {
	now := time.Now()
	ended := &Ended{At: now, By: "executor", Outcome: OutcomeCompleted}
	cases := []struct {
		name   string
		ticket *Ticket
		want   Stage
	}{
		{"ended wins over everything", &Ticket{Ended: ended, Attention: &Attention{Question: "q"},
			Delivery: &DeliveryState{Failed: 1}}, StageEnded},
		{"delivery beats answer", &Ticket{Delivery: &DeliveryState{Failed: 1},
			Attention: &Attention{Question: "q"}}, StagePersonDelivery},
		{"answer beats review", &Ticket{Attention: &Attention{Question: "q"},
			Assignments: []Assignment{{State: AssignEnded, Report: &Report{State: ReportReady, At: now}}}}, StagePersonAnswer},
		{"ready is review", &Ticket{Assignments: []Assignment{{State: AssignEnded,
			Report: &Report{State: ReportReady, At: now}}}}, StagePersonReview},
		{"blocked waits for a person", &Ticket{Assignments: []Assignment{{State: AssignEnded,
			Report: &Report{State: ReportBlocked, At: now}}}}, StagePersonBlocked},
		{"failed waits for a person", &Ticket{Assignments: []Assignment{{State: AssignEnded,
			Report: &Report{State: ReportFailed, At: now}}}}, StagePersonFailed},
		{"no assignment is queued", &Ticket{}, StageWorkingQueued},
		{"pending assignment is queued", &Ticket{Assignments: []Assignment{{State: AssignPending}}}, StageWorkingQueued},
		{"waiting is idle", &Ticket{Assignments: []Assignment{{State: AssignEnded,
			Report: &Report{State: ReportWaiting, At: now}}}}, StageIdle},
		{"ended assignment is idle", &Ticket{Assignments: []Assignment{{State: AssignEnded, EndedAt: now}}}, StageIdle},
		{"running is working", &Ticket{Assignments: []Assignment{{State: AssignRunning}}}, StageWorking},
	}
	for _, c := range cases {
		if got := StageOf(c.ticket, now); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}

// A person's message after a blocked/failed report releases the Ticket back to
// its normal stage (003:19).
func TestStageOfHumanMessageReleasesBlocked(t *testing.T) {
	now := time.Now()
	report := &Report{State: ReportBlocked, At: now.Add(-time.Minute)}
	tk := &Ticket{Assignments: []Assignment{{State: AssignEnded, Report: report}}}
	if got := StageOf(tk, now); got != StagePersonBlocked {
		t.Fatalf("blocked ticket stage %q", got)
	}
	tk.Inputs = append(tk.Inputs, Input{ID: "in-1", Kind: InputTrigger, Human: true, At: now})
	if got := StageOf(tk, now); got != StageIdle {
		t.Fatalf("after a human message the ticket must leave person/blocked, got %q", got)
	}
}

// CanRetry is true only for an open Ticket stalled on a person or a failed
// delivery (003:137).
func TestCanRetry(t *testing.T) {
	now := time.Now()
	blocked := &Ticket{Assignments: []Assignment{{State: AssignEnded,
		Report: &Report{State: ReportBlocked, At: now}}}}
	if !blocked.CanRetry() {
		t.Fatal("blocked ticket must be retryable")
	}
	delivery := &Ticket{Delivery: &DeliveryState{Failed: 1}}
	if !delivery.CanRetry() {
		t.Fatal("failed delivery must be retryable")
	}
	ended := &Ticket{Ended: &Ended{At: now}, Delivery: &DeliveryState{Failed: 1}}
	if ended.CanRetry() {
		t.Fatal("an ended ticket is never retryable")
	}
	if (&Ticket{Assignments: []Assignment{{State: AssignRunning}}}).CanRetry() {
		t.Fatal("a working ticket is not retryable")
	}
}

// FollowsDone follows 003:100: own Threads decide; a Ticket that opened none is
// done when every followed Thread closed, as of the latest close.
func TestFollowsDone(t *testing.T) {
	now := time.Now()
	later := now.Add(time.Minute)
	tk := &Ticket{Follows: []Follow{
		{Thread: "a", Own: true, ClosedAt: &now, Outcome: OutcomeMerged},
		{Thread: "b", Own: true, ClosedAt: &later, Outcome: OutcomeClosed},
	}}
	outcome, done := tk.FollowsDone()
	if !done || outcome != OutcomeClosed {
		t.Fatalf("want the last close (closed), got %q done=%v", outcome, done)
	}
	tk.Follows = append(tk.Follows[:1], Follow{Thread: "c", Own: true})
	if _, done := tk.FollowsDone(); done {
		t.Fatal("an open own Thread keeps the ticket open")
	}
	contextOnly := &Ticket{Follows: []Follow{{Thread: "a", ClosedAt: &now, Outcome: OutcomeMerged}}}
	if outcome, done := contextOnly.FollowsDone(); !done || outcome != OutcomeMerged {
		t.Fatal("with no own Thread every followed Thread decides")
	}
}

// LetGo: an end releases the human Triggers the worker never read, acked or
// handed back; a person's stop releases none (002:34,36).
func TestReleaseOnEnd(t *testing.T) {
	tk := &Ticket{Inputs: []Input{
		{ID: "1", Kind: InputTrigger, TriggerID: "t1", Human: true},
		{ID: "2", Kind: InputTrigger, TriggerID: "t2", Human: true, Read: true},
		{ID: "3", Kind: InputTrigger, TriggerID: "t3", Human: true, Acked: true},
		{ID: "4", Kind: InputTrigger, TriggerID: "t4", Human: true, HandedBack: true},
		{ID: "5", Kind: InputTrigger, TriggerID: "t5", Human: true, Context: true},
		{ID: "6", Kind: InputNote, Text: "note"},
	}}
	got := tk.ReleaseOnEnd(false)
	if len(got) != 1 || got[0] != "t1" {
		t.Fatalf("want only t1 released, got %v", got)
	}
	if got := tk.ReleaseOnEnd(true); len(got) != 0 {
		t.Fatalf("a person's stop releases nothing, got %v", got)
	}
}

// The three-stage delivery facts are idempotent (003:112).
func TestInputDeliveryStages(t *testing.T) {
	tk := &Ticket{}
	tk.AddInput(Input{ID: "a", Kind: InputTrigger})
	tk.AddInput(Input{ID: "a", Kind: InputTrigger}) // idempotent by id
	if len(tk.Inputs) != 1 {
		t.Fatal("AddInput must be idempotent")
	}
	if len(tk.UndeliveredInputs()) != 1 || len(tk.UnreadInputs()) != 1 {
		t.Fatal("new input is undelivered and unread")
	}
	tk.MarkDelivered(time.Now())
	if len(tk.UndeliveredInputs()) != 0 || len(tk.UnreadInputs()) != 1 {
		t.Fatal("delivered is not read")
	}
	tk.MarkRead(time.Now())
	if len(tk.UnreadInputs()) != 0 {
		t.Fatal("read input must leave the unread set")
	}
	tk.MarkTurnEnded(time.Now())
	if !tk.Inputs[0].TurnEnded {
		t.Fatal("a turn end marks the read inputs")
	}
}

// Mandate immutability is a fact readers can rely on: nothing in the domain
// model rewrites it.
func TestMandateFieldsCarryTheBasis(t *testing.T) {
	m := Mandate{Strength: StrengthInferred, PolicyVersion: "pv-1", Actor: Actor{ID: "u"},
		Basis: Basis{Via: ViaAdmission, Row: "issue", Cell: CellInferred}, Rules: []string{"impl"}}
	if m.Strength != StrengthInferred || m.Basis.Via != ViaAdmission || m.Rules[0] != "impl" {
		t.Fatal("mandate must record strength, basis and matched rules")
	}
}
