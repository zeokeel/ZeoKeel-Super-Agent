package view

// What a person can do to a Ticket, and what the detail of one carries. The
// actions are derived here, in view, so the panel draws the same rule the
// runtime enforces, and so the rule can be tested without a window (design
// §6.3: work-item actions live on 总览, and only there).

import "time"

// TicketAction is one action a person can take on a Ticket from 总览.
type TicketAction string

const (
	// ActionAnswer answers an Attention: the Ticket waits for a person's answer.
	ActionAnswer TicketAction = "answer"
	// ActionRetry starts another Assignment: the last one was blocked, failed, or
	// its delivery failed, and the runtime allows a retry.
	ActionRetry TicketAction = "retry"
	// ActionStop ends the Ticket and withdraws its Mandate. It is the one action
	// that ends work early, so the panel asks before sending it.
	ActionStop TicketAction = "stop"
)

// Label is how the action reads on a button.
func (a TicketAction) Label() string {
	switch a {
	case ActionAnswer:
		return "回答"
	case ActionRetry:
		return "重试"
	case ActionStop:
		return "停止"
	}
	return string(a)
}

// NeedsConfirm reports whether the action changes a Ticket for good: 停止 ends
// it and withdraws the Mandate, so the panel confirms it; answering and retrying
// do not.
func (a TicketAction) NeedsConfirm() bool { return a == ActionStop }

// Stage IDs a Ticket can be in. They are the derived stage's own values
// (domain.Stage), kept as strings so view stays leaf-level; the panel maps a
// stage to actions with them, never by reading the label (design §6.5: the
// label is wording, the id is the fact).
const (
	StageEnded          = "ended"
	StagePersonDelivery = "person/delivery"
	StagePersonAnswer   = "person/answer"
	StagePersonReview   = "person/review"
	StagePersonBlocked  = "person/blocked"
	StagePersonFailed   = "person/failed"
	StageWorkingQueued  = "working/queued"
	StageIdle           = "idle"
	StageWorking        = "working"
)

// ActionsFor is the actions a row offers, by the stage it is in. A person
// answering, retrying or stopping is a work-item action, so it exists only where
// the row is drawn — 总览 (design §6.3).
//
// Rules:
//   - person/answer: 回答, and 停止 while the Ticket is still open;
//   - person/blocked, person/failed, person/delivery, or a row the projector
//     marks CanRetry: 重试, and 停止;
//   - working, working/queued, idle, person/review: 停止;
//   - ended: nothing — the work is over, and an ended Ticket a person stopped
//     takes no further command.
func ActionsFor(r TicketRow) []TicketAction {
	if r.StageID == StageEnded {
		return nil
	}
	stop := TicketAction(ActionStop)
	switch r.StageID {
	case StagePersonAnswer:
		return []TicketAction{ActionAnswer, stop}
	case StagePersonBlocked, StagePersonFailed, StagePersonDelivery:
		return []TicketAction{ActionRetry, stop}
	case StagePersonReview, StageWorking, StageWorkingQueued, StageIdle:
		return []TicketAction{stop}
	}
	// A row whose stage the projector did not publish, but which is not ended:
	// stopping is still the honest offer, and the runtime checks it.
	if r.CanRetry {
		return []TicketAction{ActionRetry, stop}
	}
	return []TicketAction{stop}
}

// TicketAttention is a question a Ticket waits to have answered: what the agent
// asked, and the structured options it offered, if any. The free-text answer is
// always allowed.
type TicketAttention struct {
	ID       string
	Question string
	Options  []string
	AskedAt  time.Time
}

// MandateDetail is the authorization a Ticket runs under: why work may be done,
// and how far it may go (001: Mandate).
type MandateDetail struct {
	// Strength is the deterministic basis: "explicit" or "inferred" (002).
	Strength string
	// PolicyVersion is the Policy the Mandate was granted under.
	PolicyVersion string
	// Rules are the team rules that matched, by id.
	Rules []string
	// Actor is the sender's identity on the Source that asked.
	Actor string
	// Basis is how the acceptance came about: addressed, followup, admission,
	// listed or bot.
	Basis string
	// Scope is the work the Mandate allows.
	Scope string
	// Workspace is where the work runs.
	Workspace string
	// At is when the Mandate was granted.
	At time.Time
}

// ReportDetail is what one Assignment reported back.
type ReportDetail struct {
	// State is the report's own state: progress, waiting, ready, blocked,
	// failed, dropped.
	State   string
	Summary string
	Detail  string
	At      time.Time
	// Artifacts are the deliverables this report named.
	Artifacts []Artifact
}

// AssignmentDetail is one binding of the Ticket to an executor over time.
type AssignmentDetail struct {
	ID       string
	Executor string
	// State is pending, running or ended.
	State     string
	StartedAt time.Time
	EndedAt   time.Time
	Report    *ReportDetail
}

// InputDetail is one piece of context delivered to the Ticket: a fanned-out
// Trigger, a policy change, a resume, or a person's text (001: Input).
type InputDetail struct {
	ID   string
	Kind string
	Text string
	At   time.Time
	// Delivered is "handed to the worker"; Read is "in the model's history"
	// (003:112). They are separate facts, never one.
	Delivered bool
	Read      bool
}

// FollowDetail is a Thread the Ticket follows for context, and where that
// follow ended.
type FollowDetail struct {
	Thread string
	// Own is true for the Thread the Ticket started on.
	Own bool
	// ClosedAt is set once the Thread (or its pull request) closed.
	ClosedAt *time.Time
	Outcome  string
}

// DeliveryDetail is the last delivery attempt of the Ticket's inputs.
type DeliveryDetail struct {
	Attempts  int
	LastError string
	At        time.Time
}

// TicketDetailVM is everything a Ticket's expanded detail draws. It is built by
// the projector; empty sections are a fact the panel states ("暂无数据"), never
// something it fills in with a guess.
type TicketDetailVM struct {
	// ID is the Ticket's domain id (the lookup key); Number is the "#42" a
	// person reads.
	ID        string
	Number    int
	Title     string
	Workspace string
	// Stage is the derived stage id (see StagePersonAnswer and the rest) and
	// StageLabel its wording.
	Stage      string
	StageLabel string
	Tone       Tone
	// Origin: where the work came from.
	Source string
	Thread string
	Actor  string
	// Detail is why it waits, or what the last report said.
	Detail string
	// Mandate is the authorization, nil when the projector has none.
	Mandate   *MandateDetail
	Attention *TicketAttention
	// Assignments are the executor bindings, oldest first.
	Assignments []AssignmentDetail
	// Inputs are the delivered context, oldest first.
	Inputs []InputDetail
	// Follows are the Threads this Ticket follows.
	Follows []FollowDetail
	// Artifacts are the deliverables this Ticket produced.
	Artifacts []Artifact
	// Delivery is the last failed delivery attempt, nil when there is none.
	Delivery     *DeliveryDetail
	LastActivity string
	UpdatedAt    time.Time
}

// EmptySections lists the parts of the detail the projector has not filled in,
// in the words the panel shows, so a person can tell "nothing happened" from
// "not projected yet".
func (d *TicketDetailVM) EmptySections() []string {
	if d == nil {
		return nil
	}
	var out []string
	if d.Mandate == nil {
		out = append(out, "Mandate 依据")
	}
	if d.Attention == nil {
		out = append(out, "Attention")
	}
	if len(d.Assignments) == 0 {
		out = append(out, "Assignments / Reports")
	}
	if len(d.Inputs) == 0 {
		out = append(out, "Inputs")
	}
	if len(d.Follows) == 0 {
		out = append(out, "Follows")
	}
	if len(d.Artifacts) == 0 {
		out = append(out, "Artifacts")
	}
	return out
}
