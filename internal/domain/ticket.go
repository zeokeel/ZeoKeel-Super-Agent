package domain

import "time"

// DecisionKind is what Eval decided about a batch of Triggers (001:109).
type DecisionKind string

const (
	// DecPropose is "I would accept this, but it needs a person's approval first".
	// It creates no Ticket: work that is not authorized yet is not a Ticket.
	// It is kept for deployments that opt into the approval gate; the default
	// path is the prototype one, where Eval's accept creates the Ticket (001:35).
	DecPropose     DecisionKind = "propose"
	DecAccept      DecisionKind = "accept"
	DecStop        DecisionKind = "stop"
	DecIgnore      DecisionKind = "ignore"
	DecUnavailable DecisionKind = "unavailable"
)

// Reason is why something happened, as a code plus values rather than free text,
// so it can be localized and checked (001 / 002). The codes are the prototype's
// src/core/reasons.ts.
type Reason struct {
	Code   string
	Values map[string]string
}

// Reason codes ported from the prototype's src/core/reasons.ts.
const (
	ReasonSaid           = "said"
	ReasonTimeout        = "timeout"
	ReasonPolicyChanged  = "policyChanged"
	ReasonNoAdmissible   = "noAdmissible"
	ReasonStartedTicket  = "startedTicket"
	ReasonAcceptedBefore = "acceptedBefore"
	ReasonRouted         = "routed"
	ReasonExplicitRouted = "explicitRouted"
	ReasonHumanStop      = "humanStop"
	ReasonLegacyImport   = "legacyImport"
	ReasonForwarded      = "forwarded"
	ReasonSenderNotMemb  = "senderNotMember"
	ReasonRetried        = "retried"
	ReasonHostRestarted  = "hostRestarted"
	ReasonStopUnknown    = "stopUnknown"
	ReasonStopNotExpl    = "stopNotExplicit"
	ReasonDirUnsettled   = "directionUnsettled"
	ReasonNoRuleMatched  = "noRuleMatched"
	ReasonIgnoredCard    = "ignoredFromCard"
	ReasonObserved       = "observed"
	// Codes this port uses for paths the prototype encodes as literals.
	ReasonNotification   = "notification"
	ReasonNotAddressed   = "not_addressed"
	ReasonNotAllowed     = "not_a_member"
	ReasonClarify        = "clarify"
	ReasonAdmissionIgnor = "admission_ignore"
	ReasonAcceptModeOff  = "accept_mode_off"
	ReasonReplyNotify    = "reply_mode_notify"
	ReasonSensitiveWS    = "sensitive_workspace"
	ReasonOwnedFollowup  = "owned_followup"
	ReasonMandateRevoked = "mandateRevoked"
	ReasonNotRetryable   = "notRetryable"
	ReasonPaused         = "paused"
	// ReasonIMApproval marks a Decision that waits for the operator because the
	// message arrived on an IM Source: inbound IM work is always proposed, never
	// started on its own (release policy; see engine.isIMSource).
	ReasonIMApproval = "im_approval_required"
)

// ReplyRoute is who an answer goes to. Design §4.10.
type ReplyRoute string

const (
	ReplyDirect   ReplyRoute = "direct"
	ReplyOperator ReplyRoute = "operator"
	ReplyBoth     ReplyRoute = "both"
	ReplyNone     ReplyRoute = "none"
)

// ReplyPurpose is why a reply is sent; it is what stops "just say something".
type ReplyPurpose string

const (
	PurposeAck       ReplyPurpose = "acknowledgement"
	PurposeAnswer    ReplyPurpose = "answer"
	PurposeResult    ReplyPurpose = "result"
	PurposeAttention ReplyPurpose = "attention"
)

// AcceptMode decides what an inferred candidate may do. It mirrors ReplyMode:
// the safe default is to record, not to act (design §4.3).
type AcceptMode string

const (
	AcceptOff     AcceptMode = "off"
	AcceptObserve AcceptMode = "observe"
	AcceptAuto    AcceptMode = "auto"
)

// ReplyMode is the workspace's policy for answering people other than the
// operator. It mirrors the accept mode: trust is earned, not assumed.
type ReplyMode string

const (
	ReplyModeNotify ReplyMode = "notify"
	ReplyModeAssist ReplyMode = "assist"
	ReplyModeAuto   ReplyMode = "auto"
)

// ReplyPlan is the decision about how to answer a message. Every reply carries
// one, so "it decided to speak on its own" is always explainable.
type ReplyPlan struct {
	Route   ReplyRoute
	Purpose ReplyPurpose
	Reason  []Reason
	// AuthorizedBy says what allowed this route: "policy:<mode>", "rule:<id>",
	// or "person" for an approval made in the panel.
	AuthorizedBy string
}

// Strength is how strong the basis of a Mandate or an Eval is (001:105, 002:48).
// The gate computes it deterministically; a model never declares it.
type Strength string

const (
	StrengthExplicit    Strength = "explicit"
	StrengthInferred    Strength = "inferred"
	StrengthNotAdmitted Strength = "not_admitted"
)

// BasisVia says how the Source received the Trigger. A Mandate records it so a
// later revalidation can re-derive the same conclusion (002:133-139).
type BasisVia string

const (
	ViaAddressed BasisVia = "addressed"
	ViaFollowup  BasisVia = "followup"
	ViaAdmission BasisVia = "admission"
	ViaListed    BasisVia = "listed"
	ViaBot       BasisVia = "bot"
	ViaOperator  BasisVia = "operator"
)

// Basis is the explanation of a Mandate's strength: which path admitted the
// Trigger, and (for the admission table) which row and cell decided it.
type Basis struct {
	Via    BasisVia      `json:"via,omitempty"`
	Source string        `json:"source,omitempty"`
	Row    string        `json:"row,omitempty"`
	Cell   AdmissionCell `json:"cell,omitempty"`
}

// AdmissionCell is one cell of the admission table (002:63-69).
type AdmissionCell string

const (
	CellExplicit AdmissionCell = "explicit"
	CellInferred AdmissionCell = "inferred"
	CellIgnore   AdmissionCell = "ignore"
)

// AdmissionRow is one row of the admission table: the messages of one receiving
// scope, split by listed/unlisted and addressed/unaddressed (002:58-71).
type AdmissionRow struct {
	// Source and Scope name the receiving scope, e.g. "github" + "issue" or
	// "feishu" + "<chatID>".
	Source string `json:"source,omitempty"`
	Scope  string `json:"scope,omitempty"`
	// Observed is true when this scope receives every message (all chat messages
	// or a GitHub scope with its receive-all switch on). The two unaddressed
	// cells are available only then (002:71).
	Observed            bool          `json:"observed,omitempty"`
	ListedUnaddressed   AdmissionCell `json:"listedUnaddressed,omitempty"`
	UnlistedAddressed   AdmissionCell `json:"unlistedAddressed,omitempty"`
	UnlistedUnaddressed AdmissionCell `json:"unlistedUnaddressed,omitempty"`
}

// Rule is one kind of proactive work the team wrote down (002:101-105). Eval
// judges every enabled rule exactly once; a proactive accept needs at least one
// match, and the matched rules go into the Mandate.
type Rule struct {
	ID          string
	Instruction string
	Enabled     bool
	Proactive   bool
	// Requirements is what this rule asks of the process and communication for
	// the Ticket it accepts (002:105).
	Requirements string `json:"requirements,omitempty"`
}

// RuleHit is one rule's judgement inside an Eval.
type RuleHit struct {
	ID      string `json:"id"`
	Matched bool   `json:"matched"`
	Note    string `json:"note,omitempty"`
}

// Mandate is what an accept grants (001:34). It is immutable: only a person's
// stop withdraws it and a Policy that no longer holds it revokes it.
type Mandate struct {
	Strength      Strength    `json:"strength,omitempty"`
	PolicyVersion string      `json:"policyVersion,omitempty"`
	Actor         Actor       `json:"actor,omitempty"`
	Workspace     WorkspaceID `json:"workspace,omitempty"`
	// Scope is the Source scope the Mandate was granted on (design §4.7).
	Scope Scope `json:"scope,omitempty"`
	// Basis explains the strength (002:133-139).
	Basis Basis `json:"basis,omitempty"`
	// Rules are the matched rule ids (002:103).
	Rules []string `json:"rules,omitempty"`
	// Withdrawn is set by a person's stop (002:129).
	Withdrawn string `json:"withdrawn,omitempty"`
	// Revoked is set when a revalidation proved the Policy no longer holds the
	// Mandate (002:129,141). It is a fact for audit; the Ticket stays ended.
	Revoked string `json:"revoked,omitempty"`
}

// Decision is the result of an Eval. It always names its target workspace,
// because a Trigger may not have one (a global Source) and the Router may
// dispatch across workspaces.
type Decision struct {
	ID        ID
	Kind      DecisionKind
	Reasons   []Reason
	Workspace WorkspaceID
	// Thread is the Thread the decision was about, when the Trigger arrived on one.
	// It is persisted in decision.thread so the panel can list Decisions by Thread
	// after a restart instead of reversing an in-memory projection.
	Thread string `json:"thread,omitempty"`
	// EvalID links the Decision to the Eval that produced it.
	EvalID ID `json:"evalId,omitempty"`
	// TriggerIDs is the batch the Eval judged (001:33).
	TriggerIDs []ID      `json:"triggerIds,omitempty"`
	RulesHit   []RuleHit `json:"rulesHit,omitempty"`
	Mandate    *Mandate  `json:"mandate,omitempty"`
	// Reply is how the message is answered: to the sender, to the operator, both,
	// or not at all.
	Reply ReplyPlan `json:"reply,omitempty"`
	// Proposal is set when Kind is DecPropose: what the person must judge before
	// the work may start.
	Proposal *Proposal `json:"proposal,omitempty"`
	At       time.Time `json:"at,omitempty"`
}

// ReportState is what an Assignment reports back (001:112).
type ReportState string

const (
	ReportProgress ReportState = "progress"
	ReportWaiting  ReportState = "waiting"
	ReportReady    ReportState = "ready"
	ReportBlocked  ReportState = "blocked"
	ReportFailed   ReportState = "failed"
	ReportDropped  ReportState = "dropped"
)

// ArtifactKind is what a deliverable is. The panel draws each kind differently,
// so the kind is a closed set rather than a free string.
type ArtifactKind string

const (
	ArtifactFile   ArtifactKind = "file"
	ArtifactImage  ArtifactKind = "image"
	ArtifactReport ArtifactKind = "report"
	ArtifactPR     ArtifactKind = "pr"
	ArtifactLink   ArtifactKind = "link"
)

// Artifact is one deliverable a Ticket produced. It records where the bytes are,
// not the bytes: large outputs belong on disk, not in the database (design §7.3).
type Artifact struct {
	ID       ID
	Ticket   ID
	Kind     ArtifactKind
	Title    string
	Detail   string
	Path     string
	URL      string
	Verified bool
	At       time.Time
}

// Report is the state an Assignment reports to its Ticket.
type Report struct {
	State   ReportState
	Summary string
	At      time.Time
}

// AssignmentState is the lifecycle of one binding of a Ticket to a worker.
type AssignmentState string

const (
	AssignPending AssignmentState = "pending"
	AssignRunning AssignmentState = "running"
	AssignEnded   AssignmentState = "ended"
)

// Assignment binds a Ticket to one Worker run. A Ticket has several over time
// (001:36).
type Assignment struct {
	ID     ID
	State  AssignmentState
	Report *Report
	// StartedAt is when an executor picked this up.
	StartedAt time.Time
	// EndedAt is when it ended, however it ended.
	EndedAt time.Time `json:"endedAt,omitempty"`
	// EndReason is why it ended: "ready" | "dropped" | "retried" |
	// "hostRestarted" | "failed" | "ticketEnded" ...
	EndReason string `json:"endReason,omitempty"`
	// SessionID is the Executor Session this Assignment ran in, so a retry can
	// reuse the same Session (003:141).
	SessionID string `json:"sessionId,omitempty"`
}

// Proposal is what the agent puts in front of a person before acting on someone
// else's request. All four parts are required, because a person cannot decide
// without them. It belongs to a Decision, not a Ticket.
type Proposal struct {
	From        string
	Said        string
	Intent      string
	Plan        []string
	Deliverable string
	Workspace   string
	Sensitive   []string
}

// Attention is a question on a Ticket that waits for a person's answer, from the
// agent's ask_user_question. The Ticket already exists; this is different from an
// approval, which is about whether a Ticket should exist at all (001:101).
type Attention struct {
	Question string
	Options  []string
	At       time.Time
	// Answer is recorded when a person answers, so the panel can show what was
	// decided; the Attention itself is cleared at that moment.
	Answer string `json:"answer,omitempty"`
}

// InputKind names why context reached a Ticket (001:103, 003:103-112).
type InputKind string

const (
	// InputTrigger is a fanned-out Trigger from a subscribed Thread.
	InputTrigger InputKind = "trigger"
	// InputNote is a note from the main Session about other work; context only.
	InputNote InputKind = "note"
	// InputPolicy is a policy-changed notice (002:111).
	InputPolicy InputKind = "policy"
	// InputResumed marks the person's message that resumed an ended Ticket.
	InputResumed InputKind = "resumed"
	// InputText is an answer to an Attention or text typed for the Ticket.
	InputText InputKind = "text"
)

// Input is context delivered to a Ticket (001:103). Delivered and Read are the
// three-stage delivery facts of 003:112: delivered = handed to the worker, read =
// taken into a step, and a Report or the end of a turn after Read is turnEnded.
type Input struct {
	ID        ID
	Kind      InputKind
	TriggerID ID     `json:"triggerId,omitempty"`
	Thread    string `json:"thread,omitempty"`
	Text      string `json:"text,omitempty"`
	At        time.Time
	// Human is true when a person produced the message this Input carries.
	Human bool `json:"human,omitempty"`
	// Context is true when the Input reached the Ticket by fan-out from a Thread
	// it follows for context; context grants no authority and does not resume.
	Context bool `json:"context,omitempty"`
	// Delivered is set when the Input was handed to the worker.
	Delivered bool `json:"delivered,omitempty"`
	// Read is set when the Input became part of a model step.
	Read bool `json:"read,omitempty"`
	// TurnEnded is set when the turn that read the Input ended (003:112).
	TurnEnded bool `json:"turnEnded,omitempty"`
	// Acked is set when the Executor acknowledged or answered the message.
	Acked bool `json:"acked,omitempty"`
	// HandedBack is set when the Executor handed the message back for routing.
	HandedBack bool `json:"handedBack,omitempty"`
}

// DeliveryState records a failed delivery of an Input to the worker, until a
// delivery succeeds or the Ticket ends (003:11).
type DeliveryState struct {
	Failed    int       `json:"failed,omitempty"`
	LastError string    `json:"lastError,omitempty"`
	At        time.Time `json:"at,omitempty"`
}

// Follow is one Thread a Ticket follows and how it ended, for the pull-request
// lifecycle of 003:96-104. Own means the Ticket opened it; the Ticket is done
// when none of its own Threads is open (or, with no own Thread, when every
// followed Thread closed).
type Follow struct {
	Thread   string     `json:"thread"`
	Own      bool       `json:"own,omitempty"`
	ClosedAt *time.Time `json:"closedAt,omitempty"`
	Outcome  string     `json:"outcome,omitempty"`
}

// Ticket is a unit of authorized work. Its stage is derived, never stored: see
// StageOf (design §4.2).
type Ticket struct {
	ID ID
	// Number is the short number a person reads in the panel, "#42". The ID stays
	// content-derived for idempotence; a hash is unusable in conversation, and a
	// sequential number is not safe as an identity. Keep them separate.
	Number    int
	Workspace WorkspaceID
	Mandate   Mandate
	Title     string

	CreatedAt         time.Time
	Ended             *Ended
	Attention         *Attention
	Assignments       []Assignment
	SubscribedThreads []string
	// Inputs is everything delivered to the Ticket (001:103).
	Inputs []Input
	// Delivery is the latest failed Input delivery (003:11).
	Delivery *DeliveryState
	// Follows is the pull-request lifecycle of 003:96-104.
	Follows []Follow
	// FollowupOwners are the Executor Sessions that currently own human messages
	// on the Ticket's Threads (002:24). Empty means routing is free.
	FollowupOwners []string
	// DecisionIDs is the Decision history of the Ticket's Thread, by id (001:43).
	DecisionIDs []ID `json:"decisionIds,omitempty"`

	// ContextInput is the pre-Inputs field; Load migrates it into Inputs.
	ContextInput []string `json:"contextInput,omitempty"`

	// Origin is the Trigger that started this work. The reply goes back to the
	// conversation that Trigger came from (design §4.7).
	Origin ID
	// Artifacts are the deliverables this Ticket produced, in the order they
	// appeared.
	Artifacts []Artifact
}

// Ended records how a Ticket finished.
type Ended struct {
	At      time.Time `json:"at,omitempty"`
	By      string    `json:"by,omitempty"`      // "executor" | "human"
	Outcome string    `json:"outcome,omitempty"` // "completed" | "merged" | "closed" | "dropped" | "stopped"
	Reason  string    `json:"reason,omitempty"`
}

// Ticket end outcomes (003:94-104).
const (
	OutcomeCompleted = "completed"
	OutcomeMerged    = "merged"
	OutcomeClosed    = "closed"
	OutcomeDropped   = "dropped"
	OutcomeStopped   = "stopped"
)

// EvalState is where an Eval is in its own lifecycle (001:33, design §2.1).
type EvalState string

const (
	EvalQueued      EvalState = "queued"
	EvalRunning     EvalState = "running"
	EvalUnavailable EvalState = "unavailable"
	EvalDone        EvalState = "done"
)

// Eval is the judgement on a batch of Triggers of one Thread. A Thread has at
// most one running or queued Eval; Triggers that arrive meanwhile merge into the
// pending batch (001:29,43). It holds the deadline facts 002:147-151 needs.
type Eval struct {
	ID         ID
	Thread     string
	Workspace  WorkspaceID
	TriggerIDs []ID
	Primary    ID
	Strength   Strength
	Actor      Actor
	Basis      Basis
	PolicyVer  string
	State      EvalState
	QueuedAt   time.Time
	StartedAt  time.Time `json:"startedAt,omitempty"`
	// Deadline is when the candidate's time runs out. It starts when the
	// candidate reaches the main Session (002:149).
	Deadline time.Time `json:"deadline,omitempty"`
	// Attempts counts consecutive expiries; the third records unavailable
	// (002:151).
	Attempts int `json:"attempts,omitempty"`
	// PausedAt is when a processing pause started; the deadline is shifted by the
	// pause length when processing resumes (003:200).
	PausedAt time.Time `json:"pausedAt,omitempty"`
	// CandidateDelivered is false while the candidate waits for delivery; an
	// undelivered candidate never expires (002:149).
	CandidateDelivered bool   `json:"candidateDelivered,omitempty"`
	DecisionID         ID     `json:"decisionId,omitempty"`
	LastError          string `json:"lastError,omitempty"`
}

// Stage is what a Ticket waits for. The first matching row wins; changing the
// order changes the semantics, so it must match 003 Execution exactly.
type Stage string

const (
	StageEnded          Stage = "ended"
	StagePersonDelivery Stage = "person/delivery"
	StagePersonAnswer   Stage = "person/answer"
	StagePersonReview   Stage = "person/review"
	StagePersonBlocked  Stage = "person/blocked"
	StagePersonFailed   Stage = "person/failed"
	StageWorkingQueued  Stage = "working/queued"
	StageIdle           Stage = "idle"
	StageWorking        Stage = "working"
)

// StageOf derives a Ticket's stage from its facts instead of reading a stored
// field. Row order is the 003:9-17 table, read top to bottom (design §4.2).
func StageOf(t *Ticket, now time.Time) Stage {
	if t.Ended != nil {
		return StageEnded
	}
	if t.Delivery != nil {
		return StagePersonDelivery
	}
	if t.Attention != nil {
		return StagePersonAnswer
	}
	last := t.LastReport()
	lastState := AssignmentState("")
	if len(t.Assignments) > 0 {
		lastState = t.LastAssignment().State
	}
	if last != nil && last.State == ReportReady {
		return StagePersonReview
	}
	if last != nil && last.State == ReportBlocked && t.WaitsForPerson() {
		return StagePersonBlocked
	}
	if last != nil && last.State == ReportFailed && t.WaitsForPerson() {
		return StagePersonFailed
	}
	if len(t.Assignments) == 0 || lastState == AssignPending {
		return StageWorkingQueued
	}
	if (last != nil && last.State == ReportWaiting) || lastState == AssignEnded {
		return StageIdle
	}
	return StageWorking
}

// LastAssignment returns the most recent Assignment, which always exists when
// the caller has already checked for the empty case.
func (t *Ticket) LastAssignment() Assignment {
	if len(t.Assignments) == 0 {
		return Assignment{State: AssignEnded}
	}
	return t.Assignments[len(t.Assignments)-1]
}

// LastReport returns the most recent Report, or nil.
func (t *Ticket) LastReport() *Report {
	for i := len(t.Assignments) - 1; i >= 0; i-- {
		if t.Assignments[i].Report != nil {
			return t.Assignments[i].Report
		}
	}
	return nil
}

// WaitsForPerson reports whether the Ticket cannot continue without a person:
// the latest Report is blocked or failed and no person's message reached it
// since (001:31, 003:19).
func (t *Ticket) WaitsForPerson() bool {
	last := t.LastReport()
	if last == nil || (last.State != ReportBlocked && last.State != ReportFailed) {
		return false
	}
	// A later report that is neither, a person's message, or a retry (which
	// starts a new Assignment) lifts the hold (003:132).
	if a := t.LastAssignment(); len(t.Assignments) > 0 && a.Report == nil && a.State != AssignEnded {
		return false
	}
	return !t.humanInputAfter(last.At)
}

// LastTurnEndedAfter reports whether the Session reported or ended a turn after
// the given time, which is what turns a Read Input into a settled one (003:112).
func (t *Ticket) LastTurnEndedAfter(at time.Time) bool {
	for i := len(t.Assignments) - 1; i >= 0; i-- {
		a := t.Assignments[i]
		if a.Report != nil && a.Report.At.After(at) {
			return true
		}
		if a.State == AssignEnded && a.EndedAt.After(at) {
			return true
		}
	}
	return false
}

func (t *Ticket) humanInputAfter(at time.Time) bool {
	for _, in := range t.Inputs {
		if in.Human && !in.Context && in.Kind == InputTrigger && in.At.After(at) {
			return true
		}
	}
	return false
}

// CanRetry reports whether a person may retry the Ticket (003:137): an open
// Ticket whose latest Assignment is stalled on a person or a failed delivery.
func (t *Ticket) CanRetry() bool {
	if t.Ended != nil {
		return false
	}
	return t.WaitsForPerson() || t.Delivery != nil
}

// AddInput adds an Input once, keyed by its ID, and reports whether it was new.
func (t *Ticket) AddInput(in Input) bool {
	if in.ID == "" {
		return false
	}
	for _, existing := range t.Inputs {
		if existing.ID == in.ID {
			return false
		}
	}
	t.Inputs = append(t.Inputs, in)
	return true
}

// Input returns one Input by id.
func (t *Ticket) Input(id ID) (Input, bool) {
	for _, in := range t.Inputs {
		if in.ID == id {
			return in, true
		}
	}
	return Input{}, false
}

// UndeliveredInputs returns the Inputs handed to no worker yet, oldest first.
func (t *Ticket) UndeliveredInputs() []Input {
	var out []Input
	for _, in := range t.Inputs {
		if !in.Delivered {
			out = append(out, in)
		}
	}
	return out
}

// UnreadInputs returns the Inputs a worker has not taken into a step yet.
func (t *Ticket) UnreadInputs() []Input {
	var out []Input
	for _, in := range t.Inputs {
		if !in.Read {
			out = append(out, in)
		}
	}
	return out
}

// MarkDelivered marks every Input delivered, clearing the delivery error.
func (t *Ticket) MarkDelivered(now time.Time) {
	for i := range t.Inputs {
		t.Inputs[i].Delivered = true
	}
	t.Delivery = nil
}

// MarkRead marks every listed Input read.
func (t *Ticket) MarkRead(now time.Time) {
	for i := range t.Inputs {
		t.Inputs[i].Read = true
	}
}

// MarkTurnEnded marks the read Inputs of a finished turn (003:112).
func (t *Ticket) MarkTurnEnded(now time.Time) {
	for i := range t.Inputs {
		if t.Inputs[i].Read {
			t.Inputs[i].TurnEnded = true
		}
	}
}

// ReleaseOnEnd implements letGo for the Inputs a worker never took up when the
// Ticket ends (002:34, 003:112): human Triggers that were never read, acked or
// handed back go back to Eval. A person's stop releases none of them (002:36).
func (t *Ticket) ReleaseOnEnd(stoppedByHuman bool) []ID {
	if stoppedByHuman {
		return nil
	}
	var out []ID
	for _, in := range t.Inputs {
		if in.Kind != InputTrigger || !in.Human || in.Context {
			continue
		}
		if in.Read || in.Acked || in.HandedBack {
			continue
		}
		out = append(out, in.TriggerID)
	}
	return out
}

// FollowsOpen reports whether any followed Thread is still open.
func (t *Ticket) FollowsOpen() bool {
	for _, f := range t.Follows {
		if f.ClosedAt == nil {
			return true
		}
	}
	return false
}

// FollowsDone returns the outcome the Ticket should end with once its Threads
// are done (003:100), or ok=false while it must wait. Own Threads decide first;
// a Ticket that opened none is done when every followed Thread closed.
func (t *Ticket) FollowsDone() (string, bool) {
	own := make([]Follow, 0, len(t.Follows))
	for _, f := range t.Follows {
		if f.Own {
			own = append(own, f)
		}
	}
	counted := own
	if len(counted) == 0 {
		counted = t.Follows
	}
	if len(counted) == 0 {
		return "", false
	}
	var latest *Follow
	for i := range counted {
		if counted[i].ClosedAt == nil {
			return "", false
		}
		if latest == nil || counted[i].ClosedAt.After(*latest.ClosedAt) {
			latest = &counted[i]
		}
	}
	return latest.Outcome, true
}

// BindFollow records or updates one followed Thread.
func (t *Ticket) BindFollow(thread string, own bool) {
	for i := range t.Follows {
		if t.Follows[i].Thread == thread {
			t.Follows[i].Own = t.Follows[i].Own || own
			return
		}
	}
	t.Follows = append(t.Follows, Follow{Thread: thread, Own: own})
}

// CloseFollow records that a followed Thread closed.
func (t *Ticket) CloseFollow(thread, outcome string, at time.Time) {
	for i := range t.Follows {
		if t.Follows[i].Thread == thread {
			t.Follows[i].ClosedAt = &at
			t.Follows[i].Outcome = outcome
			return
		}
	}
	t.Follows = append(t.Follows, Follow{Thread: thread, ClosedAt: &at, Outcome: outcome})
}

// Unfinished reports whether resumed, retried or recovered work is still in
// flight, so a PR close must not end the Ticket yet (003:102).
func (t *Ticket) Unfinished() bool {
	if t.Attention != nil || t.Delivery != nil {
		return true
	}
	for _, in := range t.Inputs {
		if !in.Read && !in.TurnEnded {
			return true
		}
	}
	if len(t.Assignments) > 0 && t.LastAssignment().State != AssignEnded {
		return true
	}
	return false
}
