// Package engine holds ZeoKeel's single-writer state machine. There is exactly
// one Engine for the whole process, however many workspaces it serves: splitting
// it per workspace would cut the Router's brain apart and make a decision that
// spans two repositories impossible to apply atomically. See design §4.5.
//
// Two rules shape the package:
//
//   - Authorization is deterministic (gate.go): a model never declares a
//     Trigger's strength, only what the admitted Trigger asks for (002).
//   - An Eval never runs under the engine lock: Receive records facts and queues
//     a batch; EvaluateDue takes one batch, calls the assessor outside the lock,
//     and applies the Decision inside it (design §2.1).
package engine

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/zeokeel/super-agent/internal/brief"
	"github.com/zeokeel/super-agent/internal/domain"
)

// ErrDuplicate reports a Trigger whose ID was already received.
var ErrDuplicate = errors.New("engine: duplicate trigger")

// ErrNotRetryable reports a Ticket whose stage offers no retry (003:137).
var ErrNotRetryable = errors.New("engine: the ticket is not retryable")

// ErrNotPaused reports a resume when processing is not paused.
var ErrNotPaused = errors.New("engine: processing is not paused")

// ErrNotRejected reports an undo on a Decision that is not a rejected proposal.
var ErrNotRejected = errors.New("engine: the decision is not a rejected proposal")

// ErrUndoSuperseded reports an undo blocked by a newer decision on the Thread.
var ErrUndoSuperseded = errors.New("engine: a newer decision on the thread supersedes this rejection")

// contextBackground is the parent context for one assessor call.
func contextBackground() context.Context { return context.Background() }

// thread is the engine's view of one Thread: its Triggers, the Tickets
// subscribed to it, and its Eval slot (at most one, 001:29).
type thread struct {
	key         string
	triggerIDs  []domain.ID
	ticketIDs   []domain.ID
	decisionIDs []domain.ID
	lastAt      time.Time
}

// evalSlot is one Thread's Eval plus the batch that arrived while it ran. The
// pending batch becomes the Thread's next Eval, so a Thread still has at most
// one Eval at a time (001:29,43).
type evalSlot struct {
	Eval    domain.Eval
	Pending []domain.ID
}

// Engine is the single writer of Trigger, Thread, Eval, Decision and Ticket
// state.
type Engine struct {
	mu sync.Mutex

	workspaces map[domain.WorkspaceID]*domain.Workspace
	policies   map[domain.WorkspaceID]Policy
	index      *brief.Index

	triggers     map[domain.ID]*domain.Trigger
	dispositions map[domain.ID]domain.Disposition
	threads      map[string]*thread
	tickets      map[domain.ID]*domain.Ticket
	decisions    map[domain.ID]domain.Decision
	// decisionTrigger remembers which Trigger a Decision came from.
	decisionTrigger map[domain.ID]domain.ID
	// accepted maps a Trigger to the Ticket its accept created, so a replayed
	// Trigger cannot open a second one even when a retry produced a new Eval.
	accepted map[domain.ID]domain.ID
	evals    map[string]*evalSlot

	outbox   []Outbound
	sentKeys map[string]bool

	nextNumber int
	assessor   Assessor
	clock      func() time.Time

	paused       bool
	pauseStarted time.Time
	evalTimeout  time.Duration
}

// Outbound is one message queued for delivery to a Source.
type Outbound struct {
	Key     string
	Ticket  domain.ID
	Source  string
	Conv    domain.Conversation
	ReplyTo string
	Text    string
}

// DefaultEvalTimeout is how long a candidate may take once it reaches the
// assessor (002:149).
const DefaultEvalTimeout = 90 * time.Second

// New returns an engine that asks the given assessor to judge candidates.
func New(assessor Assessor) *Engine {
	return &Engine{
		workspaces:      map[domain.WorkspaceID]*domain.Workspace{},
		policies:        map[domain.WorkspaceID]Policy{},
		triggers:        map[domain.ID]*domain.Trigger{},
		dispositions:    map[domain.ID]domain.Disposition{},
		threads:         map[string]*thread{},
		tickets:         map[domain.ID]*domain.Ticket{},
		decisions:       map[domain.ID]domain.Decision{},
		decisionTrigger: map[domain.ID]domain.ID{},
		accepted:        map[domain.ID]domain.ID{},
		evals:           map[string]*evalSlot{},
		sentKeys:        map[string]bool{},
		assessor:        assessor,
		clock:           time.Now,
		evalTimeout:     DefaultEvalTimeout,
	}
}

// SetClock replaces the clock (tests only).
func (e *Engine) SetClock(now func() time.Time) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.clock = now
}

// SetEvalTimeout changes how long a candidate may take before it expires.
func (e *Engine) SetEvalTimeout(d time.Duration) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if d > 0 {
		e.evalTimeout = d
	}
}

// Register adds or replaces a workspace and rebuilds the routing index, which is
// derived from the briefs so there is only one source of truth.
func (e *Engine) Register(w domain.Workspace, p Policy) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.workspaces[w.ID] = &w
	if p.Version == "" {
		p.Version = policyVersion(p)
	}
	e.policies[w.ID] = p
	e.rebuildIndexLocked()
}

// Unregister removes a workspace and its policy. The caller must have checked
// that no Ticket refers to it; the routing index is rebuilt so no stale brief
// can keep routing work into a deleted workspace.
func (e *Engine) Unregister(id domain.WorkspaceID) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.workspaces[id]; !ok {
		return false
	}
	delete(e.workspaces, id)
	delete(e.policies, id)
	e.rebuildIndexLocked()
	return true
}

// SetPolicy replaces one workspace's policy (a panel save, or a Source sync).
func (e *Engine) SetPolicy(id domain.WorkspaceID, p Policy) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.workspaces[id]; !ok {
		return fmt.Errorf("engine: no workspace %q", id)
	}
	if p.Version == "" {
		p.Version = policyVersion(p)
	}
	e.policies[id] = p
	return nil
}

// PolicyOf returns one workspace's policy.
func (e *Engine) PolicyOf(id domain.WorkspaceID) (Policy, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	p, ok := e.policies[id]
	if !ok {
		return Policy{}, false
	}
	return copyPolicy(p), true
}

func (e *Engine) rebuildIndexLocked() {
	ws := make([]domain.Workspace, 0, len(e.workspaces))
	for _, w := range e.workspaces {
		ws = append(ws, *w)
	}
	e.index = brief.NewIndex(ws)
}

// Workspaces returns every workspace, in ID order.
func (e *Engine) Workspaces() []domain.Workspace {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]domain.Workspace, 0, len(e.workspaces))
	for _, w := range e.workspaces {
		out = append(out, copyWorkspace(*w))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Workspace returns one workspace by id.
func (e *Engine) Workspace(id domain.WorkspaceID) (domain.Workspace, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	w, ok := e.workspaces[id]
	if !ok {
		return domain.Workspace{}, false
	}
	return copyWorkspace(*w), true
}

// UpdateFacts records what a scan observed without touching the policy.
func (e *Engine) UpdateFacts(id domain.WorkspaceID, facts domain.Facts) {
	e.mu.Lock()
	defer e.mu.Unlock()
	w, ok := e.workspaces[id]
	if !ok {
		return
	}
	w.Brief.Facts = facts
	e.rebuildIndexLocked()
}

// Outcome is what receiving one Trigger produced.
type Outcome struct {
	Trigger    domain.ID
	TriggerIDs []domain.ID `json:"triggerIds,omitempty"`
	Kind       domain.DispositionKind
	Reasons    []domain.Reason
	Ticket     domain.ID
	Workspace  domain.WorkspaceID
	Decision   domain.Decision
	// NeedsPerson is set when the Router could not choose a workspace.
	NeedsPerson bool
	// Proposal is true when the request waits for the operator's approval.
	Proposal bool
	Question string
	// Held is true when the Trigger was accepted but processing is paused.
	Held bool
}

// String renders an Outcome for logs and the demo.
func (o Outcome) String() string {
	if o.Ticket != "" {
		return fmt.Sprintf("%s → ticket %s (ws=%s)", o.Kind, o.Ticket, o.Workspace)
	}
	return fmt.Sprintf("%s %v", o.Kind, o.Reasons)
}

// Disposition returns the audit record for a Trigger.
func (e *Engine) Disposition(id domain.ID) (domain.Disposition, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	d, ok := e.dispositions[id]
	if !ok {
		return domain.Disposition{}, false
	}
	return copyDisposition(d), true
}

// Trigger returns a stored Trigger by id.
func (e *Engine) Trigger(id domain.ID) (domain.Trigger, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	t, ok := e.triggers[id]
	if !ok {
		return domain.Trigger{}, false
	}
	return copyTrigger(*t), true
}

// TicketSnapshot returns a deep copy of a Ticket, safe to read from another
// goroutine (the panel projector and the runtime read this way).
func (e *Engine) TicketSnapshot(id domain.ID) (domain.Ticket, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	t, ok := e.tickets[id]
	if !ok {
		return domain.Ticket{}, false
	}
	return copyTicket(t), true
}

// copyTrigger deep-copies the nested state a Trigger carries, so a reader in
// another goroutine never aliases engine state.
func copyTrigger(t domain.Trigger) domain.Trigger {
	out := t
	out.Threads = append([]domain.ThreadRef(nil), t.Threads...)
	if t.Channel.Address != nil {
		out.Channel.Address = make(map[string]string, len(t.Channel.Address))
		for k, v := range t.Channel.Address {
			out.Channel.Address[k] = v
		}
	}
	return out
}

// copyDecision deep-copies every nested slice/pointer a Decision carries.
func copyDecision(d domain.Decision) domain.Decision {
	out := d
	out.Reasons = copyReasons(d.Reasons)
	out.TriggerIDs = append([]domain.ID(nil), d.TriggerIDs...)
	out.RulesHit = append([]domain.RuleHit(nil), d.RulesHit...)
	out.Reply.Reason = copyReasons(d.Reply.Reason)
	if d.Mandate != nil {
		m := *d.Mandate
		m.Rules = append([]string(nil), d.Mandate.Rules...)
		out.Mandate = &m
	}
	if d.Proposal != nil {
		pr := *d.Proposal
		pr.Plan = append([]string(nil), d.Proposal.Plan...)
		pr.Sensitive = append([]string(nil), d.Proposal.Sensitive...)
		out.Proposal = &pr
	}
	return out
}

func copyReasons(in []domain.Reason) []domain.Reason {
	if in == nil {
		return nil
	}
	out := make([]domain.Reason, len(in))
	for i, r := range in {
		out[i] = r
		if r.Values != nil {
			vals := make(map[string]string, len(r.Values))
			for k, v := range r.Values {
				vals[k] = v
			}
			out[i].Values = vals
		}
	}
	return out
}

// copyEval deep-copies one Eval's slice.
func copyEval(ev domain.Eval) domain.Eval {
	ev.TriggerIDs = append([]domain.ID(nil), ev.TriggerIDs...)
	return ev
}

// copyWorkspace deep-copies the brief slices a Workspace carries.
func copyWorkspace(w domain.Workspace) domain.Workspace {
	w.Brief.Scope = append([]string(nil), w.Brief.Scope...)
	w.Brief.OutOfScope = append([]string(nil), w.Brief.OutOfScope...)
	w.Brief.Constraints = append([]string(nil), w.Brief.Constraints...)
	w.Brief.Keywords = append([]string(nil), w.Brief.Keywords...)
	w.Brief.Aliases = append([]string(nil), w.Brief.Aliases...)
	w.Brief.Facts.DirSummary = append([]domain.DirEntry(nil), w.Brief.Facts.DirSummary...)
	return w
}

// copyPolicy deep-copies the maps a Policy carries so a reader cannot mutate
// authorization data.
func copyPolicy(p Policy) Policy {
	out := p
	out.Rules = append([]domain.Rule(nil), p.Rules...)
	out.Allowed = copyStringSetMap(p.Allowed)
	out.Bots = copyStringSetMap(p.Bots)
	if p.Admissions != nil {
		out.Admissions = make(map[string]domain.AdmissionRow, len(p.Admissions))
		for k, v := range p.Admissions {
			out.Admissions[k] = v
		}
	}
	if p.Listened != nil {
		out.Listened = make(map[string]bool, len(p.Listened))
		for k, v := range p.Listened {
			out.Listened[k] = v
		}
	}
	return out
}

func copyStringSetMap(in map[string]map[string]bool) map[string]map[string]bool {
	if in == nil {
		return nil
	}
	out := make(map[string]map[string]bool, len(in))
	for k, set := range in {
		if set == nil {
			out[k] = nil
			continue
		}
		cp := make(map[string]bool, len(set))
		for kk, v := range set {
			cp[kk] = v
		}
		out[k] = cp
	}
	return out
}

// copyTicket deep-copies the slices a Ticket carries, so a reader never aliases
// engine state.
func copyTicket(t *domain.Ticket) domain.Ticket {
	out := *t
	out.SubscribedThreads = append([]string(nil), t.SubscribedThreads...)
	out.ContextInput = append([]string(nil), t.ContextInput...)
	out.Inputs = append([]domain.Input(nil), t.Inputs...)
	out.Follows = make([]domain.Follow, len(t.Follows))
	for i, f := range t.Follows {
		out.Follows[i] = f
		if f.ClosedAt != nil {
			closed := *f.ClosedAt
			out.Follows[i].ClosedAt = &closed
		}
	}
	out.FollowupOwners = append([]string(nil), t.FollowupOwners...)
	out.DecisionIDs = append([]domain.ID(nil), t.DecisionIDs...)
	out.Artifacts = append([]domain.Artifact(nil), t.Artifacts...)
	out.Assignments = make([]domain.Assignment, len(t.Assignments))
	for i, a := range t.Assignments {
		out.Assignments[i] = a
		if a.Report != nil {
			r := *a.Report
			out.Assignments[i].Report = &r
		}
	}
	if t.Attention != nil {
		a := *t.Attention
		a.Options = append([]string(nil), t.Attention.Options...)
		out.Attention = &a
	}
	if t.Delivery != nil {
		d := *t.Delivery
		out.Delivery = &d
	}
	if t.Ended != nil {
		end := *t.Ended
		out.Ended = &end
	}
	if len(t.Mandate.Rules) > 0 {
		out.Mandate.Rules = append([]string(nil), t.Mandate.Rules...)
	}
	return out
}

// TicketsOnThread returns deep copies of the Tickets subscribed to a Thread
// (including ended ones), for the panel and for runtime.
func (e *Engine) TicketsOnThread(threadKey string) []domain.Ticket {
	e.mu.Lock()
	defer e.mu.Unlock()
	th, ok := e.threads[threadKey]
	if !ok {
		return nil
	}
	out := make([]domain.Ticket, 0, len(th.ticketIDs))
	for _, id := range th.ticketIDs {
		if t, ok := e.tickets[id]; ok {
			out = append(out, copyTicket(t))
		}
	}
	return out
}

// AllTickets returns every Ticket as a deep copy, newest first, so a reader in
// another goroutine can never alias engine state.
func (e *Engine) AllTickets() []domain.Ticket {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.ticketSliceLocked()
}

// Counts returns the activity totals the overview shows.
func (e *Engine) Counts() (triggers, decisions, tickets int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.triggers), len(e.decisions), len(e.tickets)
}

// TicketByNumber finds a Ticket by the number a person reads.
func (e *Engine) TicketByNumber(n int) (domain.Ticket, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, t := range e.tickets {
		if t.Number == n {
			return copyTicket(t), true
		}
	}
	return domain.Ticket{}, false
}

// LastTicket returns the most recently created Ticket.
func (e *Engine) LastTicket() (domain.Ticket, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	var best *domain.Ticket
	for _, t := range e.tickets {
		if best == nil || t.CreatedAt.After(best.CreatedAt) {
			best = t
		}
	}
	if best == nil {
		return domain.Ticket{}, false
	}
	return copyTicket(best), true
}

// Decisions returns every Decision as a deep copy, oldest first.
func (e *Engine) Decisions() []domain.Decision {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]domain.Decision, 0, len(e.decisions))
	for _, d := range e.decisions {
		out = append(out, copyDecision(d))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].At.Equal(out[j].At) {
			return out[i].ID < out[j].ID
		}
		return out[i].At.Before(out[j].At)
	})
	return out
}

// Decision returns one Decision by id.
func (e *Engine) Decision(id domain.ID) (domain.Decision, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	d, ok := e.decisions[id]
	if !ok {
		return domain.Decision{}, false
	}
	return copyDecision(d), true
}

// Replies returns everything queued for delivery.
func (e *Engine) Replies() []Outbound {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]Outbound(nil), e.outbox...)
}

// Reply queues one message. The key makes a retry idempotent.
func (e *Engine) Reply(ticketID domain.ID, text string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	key := string(domain.Key(domain.Global, "reply", ticketID, text))
	if e.sentKeys[key] {
		return
	}
	e.sentKeys[key] = true
	e.outbox = append(e.outbox, Outbound{Key: key, Ticket: ticketID, Text: text})
}

// Present records one deliverable on a Ticket.
func (e *Engine) Present(ticketID domain.ID, a domain.Artifact) (domain.Artifact, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	t, ok := e.tickets[ticketID]
	if !ok {
		return domain.Artifact{}, fmt.Errorf("engine: no ticket %q", ticketID)
	}
	if a.ID == "" {
		a.ID = domain.Key(domain.WorkspaceScope(t.Workspace), "artifact", ticketID, string(a.Kind), a.Path, a.Title)
	}
	a.Ticket = ticketID
	if a.At.IsZero() {
		a.At = e.clock()
	}
	for _, existing := range t.Artifacts {
		if existing.ID == a.ID {
			return existing, nil
		}
	}
	t.Artifacts = append(t.Artifacts, a)
	return a, nil
}

// Artifacts returns a Ticket's deliverables, newest last.
func (e *Engine) Artifacts(ticketID domain.ID) []domain.Artifact {
	e.mu.Lock()
	defer e.mu.Unlock()
	t, ok := e.tickets[ticketID]
	if !ok {
		return nil
	}
	return append([]domain.Artifact(nil), t.Artifacts...)
}

// AllArtifacts returns every deliverable, newest first.
func (e *Engine) AllArtifacts() []domain.Artifact {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []domain.Artifact
	for _, t := range e.tickets {
		out = append(out, t.Artifacts...)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].At.After(out[j].At) })
	return out
}

// StartAssignment records that an executor has picked the Ticket up.
func (e *Engine) StartAssignment(ticketID domain.ID) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	t, ok := e.tickets[ticketID]
	if !ok {
		return fmt.Errorf("engine: no ticket %q", ticketID)
	}
	if t.Ended != nil {
		return nil
	}
	now := e.clock()
	if n := len(t.Assignments); n > 0 && t.Assignments[n-1].State == domain.AssignPending {
		t.Assignments[n-1].State = domain.AssignRunning
		t.Assignments[n-1].StartedAt = now
		return nil
	}
	t.Assignments = append(t.Assignments, domain.Assignment{
		ID:        domain.Key(domain.WorkspaceScope(t.Workspace), "assignment", ticketID, len(t.Assignments)),
		State:     domain.AssignRunning,
		StartedAt: now,
	})
	return nil
}

// Evals returns the Threads' Evals as deep copies, newest first, for the panel.
func (e *Engine) Evals() []domain.Eval {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]domain.Eval, 0, len(e.evals))
	for _, slot := range e.evals {
		out = append(out, copyEval(slot.Eval))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].QueuedAt.After(out[j].QueuedAt) })
	return out
}

// QueueLen is how many Evals wait to be judged.
func (e *Engine) QueueLen() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	n := 0
	for _, slot := range e.evals {
		if slot.Eval.State == domain.EvalQueued {
			n++
		}
	}
	return n
}

// DueEvals returns the queued Evals in queue order.
func (e *Engine) DueEvals(now time.Time) []domain.Eval {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.dueEvalsLocked()
}

func (e *Engine) dueEvalsLocked() []domain.Eval {
	out := make([]domain.Eval, 0, len(e.evals))
	for _, slot := range e.evals {
		if slot.Eval.State == domain.EvalQueued {
			out = append(out, slot.Eval)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].QueuedAt.Equal(out[j].QueuedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].QueuedAt.Before(out[j].QueuedAt)
	})
	return out
}

// copyDisposition deep-copies the slices a Disposition carries.
func copyDisposition(d domain.Disposition) domain.Disposition {
	out := d
	out.Reason = append([]domain.Reason(nil), d.Reason...)
	out.TicketIDs = append([]domain.ID(nil), d.TicketIDs...)
	return out
}

// FeedItem is one received Trigger with what the engine did about it.
type FeedItem struct {
	Trigger  domain.Trigger
	Disp     domain.Disposition
	Ticket   *domain.Ticket
	Decision *domain.Decision
}

// Feed returns everything the panel's stream shows, oldest first.
func (e *Engine) Feed() []FeedItem {
	e.mu.Lock()
	defer e.mu.Unlock()
	items := make([]FeedItem, 0, len(e.triggers))
	for id, t := range e.triggers {
		it := FeedItem{Trigger: copyTrigger(*t), Disp: copyDisposition(e.dispositions[id])}
		if it.Disp.DecisionID != "" {
			if d, ok := e.decisions[it.Disp.DecisionID]; ok {
				dd := copyDecision(d)
				it.Decision = &dd
			}
		}
		for _, tid := range it.Disp.TicketIDs {
			if tk, ok := e.tickets[tid]; ok {
				cp := copyTicket(tk)
				it.Ticket = &cp
				break
			}
		}
		items = append(items, it)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Trigger.At.Before(items[j].Trigger.At) })
	return items
}

// Proposals returns the decisions waiting for the operator's approval.
func (e *Engine) Proposals() []domain.Decision {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]domain.Decision, 0)
	for _, d := range e.decisions {
		if d.Kind == domain.DecPropose {
			out = append(out, copyDecision(d))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// TriggerOf returns the Trigger a decision came from.
func (e *Engine) TriggerOf(decisionID domain.ID) (domain.Trigger, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	id, ok := e.decisionTrigger[decisionID]
	if !ok {
		return domain.Trigger{}, false
	}
	t, ok := e.triggers[id]
	if !ok {
		return domain.Trigger{}, false
	}
	return copyTrigger(*t), true
}

// ---- internal helpers shared by the receive/evaluate/lifecycle files ----

func (e *Engine) ticketSliceLocked() []domain.Ticket {
	out := make([]domain.Ticket, 0, len(e.tickets))
	for _, t := range e.tickets {
		out = append(out, copyTicket(t))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out
}

func (e *Engine) batchTriggersLocked(ev *domain.Eval) []domain.Trigger {
	out := make([]domain.Trigger, 0, len(ev.TriggerIDs))
	for _, id := range ev.TriggerIDs {
		if t, ok := e.triggers[id]; ok {
			out = append(out, *t)
		}
	}
	return out
}

// policyForLocked returns the Policy that gates a Trigger.
func (e *Engine) policyForLocked(t *domain.Trigger) (Policy, domain.WorkspaceID) {
	if t.DefaultWorkspace != "" {
		if p, ok := e.policies[t.DefaultWorkspace]; ok {
			return p, t.DefaultWorkspace
		}
	}
	if len(e.policies) == 1 {
		for id, p := range e.policies {
			return p, id
		}
	}
	// Iterate in id order: a merged policy must not depend on Go's map order, or
	// authorization would change between two identical runs (002).
	names := make([]string, 0, len(e.policies))
	for id := range e.policies {
		names = append(names, string(id))
	}
	sort.Strings(names)
	ps := make([]Policy, 0, len(names))
	for _, name := range names {
		ps = append(ps, e.policies[domain.WorkspaceID(name)])
	}
	if len(ps) == 0 {
		return Policy{}, ""
	}
	return mergePolicies(ps, names), t.DefaultWorkspace
}

// gateForWorkspaceLocked re-admits a Trigger against the workspace a Ticket
// would go to (§4.5, risk 15).
func (e *Engine) gateForWorkspaceLocked(t *domain.Trigger, ws domain.WorkspaceID) gateResult {
	p, ok := e.policies[ws]
	if !ok {
		return notAdmitted(domain.ReasonNotAllowed, map[string]string{"why": "unknown_workspace"})
	}
	return gate(p, *t, e.participatingLocked(t))
}

// participatingLocked reports whether the Trigger arrives on a Thread that
// already carries live Tickets.
func (e *Engine) participatingLocked(t *domain.Trigger) bool {
	for _, ref := range t.Threads {
		th, ok := e.threads[domain.ThreadKey(t.Scope, ref.Source, ref.ID)]
		if !ok {
			continue
		}
		for _, id := range th.ticketIDs {
			if tk := e.tickets[id]; tk != nil && tk.Ended == nil {
				return true
			}
		}
	}
	return false
}

// threadFor returns the Thread record for a ref, creating it.
func (e *Engine) threadFor(scope domain.Scope, ref domain.ThreadRef, at time.Time) *thread {
	key := domain.ThreadKey(scope, ref.Source, ref.ID)
	th, ok := e.threads[key]
	if !ok {
		th = &thread{key: key}
		e.threads[key] = th
	}
	if at.After(th.lastAt) {
		th.lastAt = at
	}
	return th
}

// threadKeyOf returns the Trigger's own Thread.
func threadKeyOf(t *domain.Trigger) string {
	if t.OwnThread != "" {
		if strings.Contains(t.OwnThread, "|") {
			return t.OwnThread
		}
		return domain.ThreadKey(t.Scope, t.Source, t.OwnThread)
	}
	if len(t.Threads) == 0 {
		return ""
	}
	return domain.ThreadKey(t.Scope, t.Threads[0].Source, t.Threads[0].ID)
}

// ticketOwnsThread reports whether a Ticket treats a Thread as its own.
func (e *Engine) ticketOwnsThread(tk *domain.Ticket, threadKey string) bool {
	for _, f := range tk.Follows {
		if f.Own && f.Thread == threadKey {
			return true
		}
	}
	if origin, ok := e.triggers[tk.Origin]; ok {
		for _, ref := range origin.Threads {
			if domain.ThreadKey(origin.Scope, ref.Source, ref.ID) == threadKey {
				return true
			}
		}
	}
	return false
}

func threadKeys(refs []domain.ThreadRef, scope domain.Scope) []string {
	out := make([]string, 0, len(refs))
	for _, r := range refs {
		out = append(out, domain.ThreadKey(scope, r.Source, r.ID))
	}
	return out
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func containsID(list []domain.ID, id domain.ID) bool {
	for _, v := range list {
		if v == id {
			return true
		}
	}
	return false
}

func ticketFollows(t *domain.Ticket, thread string) bool {
	for _, f := range t.Follows {
		if f.Thread == thread {
			return true
		}
	}
	return false
}

func (e *Engine) triggerOrNil(id domain.ID) *domain.Trigger {
	if t, ok := e.triggers[id]; ok {
		return t
	}
	return nil
}

// firstSentence is the display title of a Ticket.
func firstSentence(s string) string {
	for i, r := range s {
		switch r {
		case '。', '\n', '！', '？', '!', '?':
			return strings.TrimSpace(s[:i])
		}
	}
	if runes := []rune(s); len(runes) > 40 {
		return strings.TrimSpace(string(runes[:40])) + "…"
	}
	return strings.TrimSpace(s)
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
