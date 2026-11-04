package engine

// Ticket lifecycle: reports, ends (003:92-106), stop/retry/pause/resume, the
// three-stage Input delivery, follows and revalidation. All of it is domain
// state; the runtime decides when to launch a worker.

import (
	"fmt"
	"strings"
	"time"

	"github.com/zeokeel/super-agent/internal/domain"
)

// Report records what an Assignment concluded and applies the end rules of
// 003:92-106.
func (e *Engine) Report(ticketID domain.ID, state domain.ReportState, summary string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	t, ok := e.tickets[ticketID]
	if !ok {
		return fmt.Errorf("engine: no ticket %q", ticketID)
	}
	if t.Ended != nil {
		return nil // a finished Ticket does not take reports any more
	}
	now := e.clock()
	if len(t.Assignments) == 0 {
		t.Assignments = append(t.Assignments, domain.Assignment{
			ID:    domain.Key(domain.WorkspaceScope(t.Workspace), "assignment", ticketID, 0),
			State: domain.AssignRunning,
		})
	}
	last := &t.Assignments[len(t.Assignments)-1]
	last.Report = &domain.Report{State: state, Summary: summary, At: now}
	switch state {
	case domain.ReportProgress:
		// The Assignment goes on.
	default:
		last.State = domain.AssignEnded
		last.EndedAt = now
		last.EndReason = string(state)
	}
	// A report after the Session read an Input is that Input's turn end (003:112).
	t.MarkTurnEnded(now)

	switch state {
	case domain.ReportReady:
		if len(t.Follows) == 0 {
			e.endTicketLocked(t, "executor", domain.OutcomeCompleted, "", now)
		} else if outcome, done := t.FollowsDone(); done && !t.Unfinished() {
			e.endTicketLocked(t, "executor", outcome, "", now)
		}
	case domain.ReportDropped:
		e.endTicketLocked(t, "executor", domain.OutcomeDropped, "", now)
	}
	return nil
}

// endTicketLocked ends a Ticket and lets go of the messages its worker never
// took up (002:34, 003:94-104).
func (e *Engine) endTicketLocked(t *domain.Ticket, by, outcome, reason string, at time.Time) []domain.ID {
	if t.Ended != nil {
		return nil
	}
	if by == "human" && outcome == domain.OutcomeStopped {
		if t.Mandate.Withdrawn == "" {
			t.Mandate.Withdrawn = reason
		}
	}
	t.Ended = &domain.Ended{At: at, By: by, Outcome: outcome, Reason: reason}
	t.Attention = nil
	t.Delivery = nil
	t.FollowupOwners = nil
	for i := range t.Assignments {
		if t.Assignments[i].State != domain.AssignEnded {
			t.Assignments[i].State = domain.AssignEnded
			t.Assignments[i].EndedAt = at
			if t.Assignments[i].EndReason == "" {
				t.Assignments[i].EndReason = "ticketEnded"
			}
		}
	}
	stopped := by == "human" && outcome == domain.OutcomeStopped
	released := t.ReleaseOnEnd(stopped)
	for _, triggerID := range released {
		e.requeueTriggerLocked(triggerID)
	}
	return released
}

// StopTicket is a panel command: it ends the Ticket directly and withdraws its
// Mandate (002:129,144).
func (e *Engine) StopTicket(ticketID domain.ID, reason string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	t, ok := e.tickets[ticketID]
	if !ok {
		return fmt.Errorf("engine: no ticket %q", ticketID)
	}
	if t.Ended != nil {
		return nil
	}
	if reason == "" {
		reason = domain.ReasonHumanStop
	}
	e.endTicketLocked(t, "human", domain.OutcomeStopped, reason, e.clock())
	return nil
}

// RetryTicket ends a stalled Assignment and starts a new one in the same
// Session, directory and branch (003:137-143).
func (e *Engine) RetryTicket(ticketID domain.ID) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	t, ok := e.tickets[ticketID]
	if !ok {
		return fmt.Errorf("engine: no ticket %q", ticketID)
	}
	if !t.CanRetry() {
		return ErrNotRetryable
	}
	now := e.clock()
	session := ""
	if n := len(t.Assignments); n > 0 {
		last := &t.Assignments[n-1]
		session = last.SessionID
		if last.State != domain.AssignEnded {
			last.State = domain.AssignEnded
			last.EndedAt = now
			last.EndReason = "retried"
		}
	}
	t.Delivery = nil
	t.Assignments = append(t.Assignments, domain.Assignment{
		ID:        domain.Key(domain.WorkspaceScope(t.Workspace), "assignment", ticketID, len(t.Assignments)),
		State:     domain.AssignPending,
		SessionID: session,
	})
	return nil
}

// AnswerAttention records a person's answer, clears the question and queues the
// answer as an Input for the worker (001:101).
func (e *Engine) AnswerAttention(ticketID domain.ID, answer string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	t, ok := e.tickets[ticketID]
	if !ok {
		return fmt.Errorf("engine: no ticket %q", ticketID)
	}
	if t.Attention == nil {
		return fmt.Errorf("engine: ticket %q has no question", ticketID)
	}
	now := e.clock()
	answer = strings.TrimSpace(answer)
	t.Attention = nil
	t.Delivery = nil
	t.AddInput(domain.Input{
		ID:   domain.Key(domain.Global, "input", ticketID, "answer", answer, fmt.Sprint(now.UnixNano())),
		Kind: domain.InputText, Text: answer, At: now, Human: true,
	})
	if t.Ended == nil {
		if len(t.Assignments) == 0 || t.LastAssignment().State == domain.AssignEnded {
			t.Assignments = append(t.Assignments, domain.Assignment{
				ID:        domain.Key(domain.WorkspaceScope(t.Workspace), "assignment", ticketID, len(t.Assignments)),
				State:     domain.AssignPending,
				SessionID: t.LastAssignment().SessionID,
			})
		}
	}
	return nil
}

// RaiseAttention records that a Ticket waits for a person's answer.
func (e *Engine) RaiseAttention(ticketID domain.ID, question string, options []string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	t, ok := e.tickets[ticketID]
	if !ok {
		return fmt.Errorf("engine: no ticket %q", ticketID)
	}
	t.Attention = &domain.Attention{Question: question, Options: options, At: e.clock()}
	return nil
}

// Pause stops dispatch, new Evals, Eval timeouts, Input delivery and replies
// (003:194-200).
func (e *Engine) Pause() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.paused {
		return
	}
	e.paused = true
	e.pauseStarted = e.clock()
	for _, slot := range e.evals {
		if slot.Eval.State != domain.EvalDone && slot.Eval.PausedAt.IsZero() {
			slot.Eval.PausedAt = e.pauseStarted
		}
	}
}

// Resume ends a processing pause and resumes the Tickets whose person message
// waited for it (003:198-200).
func (e *Engine) Resume() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.paused {
		return
	}
	now := e.clock()
	delta := now.Sub(e.pauseStarted)
	e.paused = false
	e.pauseStarted = time.Time{}
	for _, slot := range e.evals {
		if slot.Eval.State == domain.EvalDone || slot.Eval.PausedAt.IsZero() {
			continue
		}
		if !slot.Eval.Deadline.IsZero() {
			slot.Eval.Deadline = slot.Eval.Deadline.Add(delta)
		}
		slot.Eval.PausedAt = time.Time{}
	}
	for _, t := range e.tickets {
		if t.Ended == nil {
			continue
		}
		resume := false
		for _, in := range t.Inputs {
			if in.Kind == domain.InputResumed && !in.Delivered {
				resume = true
				break
			}
		}
		if !resume {
			continue
		}
		p, ok := e.policies[t.Workspace]
		if !ok || !revalidate(p, t.Mandate) {
			e.releaseWaitingLocked(t)
			continue
		}
		t.Ended = nil
		t.Delivery = nil
		t.FollowupOwners = nil
		t.Assignments = append(t.Assignments, domain.Assignment{
			ID:        domain.Key(domain.WorkspaceScope(t.Workspace), "assignment", t.ID, len(t.Assignments)),
			State:     domain.AssignPending,
			SessionID: t.LastAssignment().SessionID,
		})
	}
}

// Paused reports whether processing is paused.
func (e *Engine) Paused() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.paused
}

// DeliverInput records one Input for a Ticket (fan-out, note, policy, resume or
// text). It is idempotent by Input ID.
func (e *Engine) DeliverInput(ticketID domain.ID, in domain.Input) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	t, ok := e.tickets[ticketID]
	if !ok {
		return fmt.Errorf("engine: no ticket %q", ticketID)
	}
	if t.Ended != nil {
		return fmt.Errorf("engine: ticket %q has ended", ticketID)
	}
	if in.ID == "" {
		in.ID = domain.Key(domain.Global, "input", ticketID, string(in.Kind), in.Text, fmt.Sprint(e.clock().UnixNano()))
	}
	if in.At.IsZero() {
		in.At = e.clock()
	}
	t.AddInput(in)
	return nil
}

// MarkInputsDelivered records that the worker was handed its pending Inputs; a
// successful delivery clears the delivery error (003:11).
func (e *Engine) MarkInputsDelivered(ticketID domain.ID) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	t, ok := e.tickets[ticketID]
	if !ok {
		return fmt.Errorf("engine: no ticket %q", ticketID)
	}
	t.MarkDelivered(e.clock())
	return nil
}

// MarkInputsRead records that the Session took its Inputs into a model step
// (003:112).
func (e *Engine) MarkInputsRead(ticketID domain.ID) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	t, ok := e.tickets[ticketID]
	if !ok {
		return fmt.Errorf("engine: no ticket %q", ticketID)
	}
	t.MarkRead(e.clock())
	return nil
}

// MarkTurnEnded records that the Session's turn ended (003:112).
func (e *Engine) MarkTurnEnded(ticketID domain.ID) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	t, ok := e.tickets[ticketID]
	if !ok {
		return fmt.Errorf("engine: no ticket %q", ticketID)
	}
	t.MarkTurnEnded(e.clock())
	return nil
}

// DeliveryFailed records a failed Input delivery; the Ticket enters
// person/delivery until delivery succeeds, a new Assignment or the end (003:11).
func (e *Engine) DeliveryFailed(ticketID domain.ID, errText string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	t, ok := e.tickets[ticketID]
	if !ok {
		return fmt.Errorf("engine: no ticket %q", ticketID)
	}
	failed := 1
	if t.Delivery != nil {
		failed = t.Delivery.Failed + 1
	}
	t.Delivery = &domain.DeliveryState{Failed: failed, LastError: errText, At: e.clock()}
	return nil
}

// BindFollow records that a Ticket follows a Thread (its own pull request, or a
// related Thread for later discussion). Re-binding reopens it (003:98).
func (e *Engine) BindFollow(ticketID domain.ID, thread string, own bool) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	t, ok := e.tickets[ticketID]
	if !ok {
		return fmt.Errorf("engine: no ticket %q", ticketID)
	}
	if t.Ended != nil {
		return fmt.Errorf("engine: ticket %q has ended", ticketID)
	}
	t.BindFollow(thread, own)
	if !containsString(t.SubscribedThreads, thread) {
		t.SubscribedThreads = append(t.SubscribedThreads, thread)
	}
	parts := strings.SplitN(thread, "|", 3)
	if len(parts) == 3 {
		th := e.threadFor(domain.Scope(parts[0]), domain.ThreadRef{Source: parts[1], ID: parts[2]}, e.clock())
		if !containsID(th.ticketIDs, ticketID) {
			th.ticketIDs = append(th.ticketIDs, ticketID)
		}
	}
	return nil
}

// ThreadClosed records that a followed Thread (a pull request) closed and ends
// the Tickets whose work is finished with the outcome of the last close
// (003:100-106).
func (e *Engine) ThreadClosed(thread, outcome string, at time.Time) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if at.IsZero() {
		at = e.clock()
	}
	for _, t := range e.tickets {
		if t.Ended != nil || !ticketFollows(t, thread) {
			continue
		}
		t.CloseFollow(thread, outcome, at)
		if t.Unfinished() {
			continue
		}
		if _, done := t.FollowsDone(); !done {
			continue
		}
		e.endTicketLocked(t, "executor", outcome, "", at)
	}
	return nil
}

// DropSubscription removes one Ticket's Subscription to a Thread and, when the
// Ticket followed it as its own pull request, stops following it. A person may
// drop every Subscription on a Thread; an Executor only its own Ticket's
// (001:37).
func (e *Engine) DropSubscription(ticketID domain.ID, thread, reason string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	t, ok := e.tickets[ticketID]
	if !ok {
		return fmt.Errorf("engine: no ticket %q", ticketID)
	}
	e.dropSubscriptionLocked(t, thread)
	return nil
}

// MarkUnrelated drops a Ticket's Subscription to a Thread its Executor found
// unrelated, and hands back the person's messages there that it decided
// (002:30). The source and own pull-request Threads stay subscribed.
func (e *Engine) MarkUnrelated(ticketID domain.ID, thread, reason string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	t, ok := e.tickets[ticketID]
	if !ok {
		return fmt.Errorf("engine: no ticket %q", ticketID)
	}
	for _, f := range t.Follows {
		if f.Own && f.Thread == thread {
			return fmt.Errorf("engine: thread %q is the ticket's own pull request", thread)
		}
	}
	if origin, ok := e.triggers[t.Origin]; ok {
		for _, ref := range origin.Threads {
			if domain.ThreadKey(origin.Scope, ref.Source, ref.ID) == thread {
				return fmt.Errorf("engine: thread %q is the ticket's source thread", thread)
			}
		}
	}
	for i := range t.Inputs {
		in := &t.Inputs[i]
		if in.Thread != thread || in.Kind != domain.InputTrigger || !in.Human || in.Read || in.Acked || in.HandedBack {
			continue
		}
		in.HandedBack = true
		e.requeueTriggerLocked(in.TriggerID)
	}
	e.dropSubscriptionLocked(t, thread)
	return nil
}

// dropSubscriptionLocked removes a Thread from a Ticket's Subscriptions and
// follows (the caller holds e.mu).
func (e *Engine) dropSubscriptionLocked(t *domain.Ticket, thread string) {
	for i, s := range t.SubscribedThreads {
		if s == thread {
			t.SubscribedThreads = append(t.SubscribedThreads[:i], t.SubscribedThreads[i+1:]...)
			break
		}
	}
	if th, ok := e.threads[thread]; ok {
		for i, id := range th.ticketIDs {
			if id == t.ID {
				th.ticketIDs = append(th.ticketIDs[:i], th.ticketIDs[i+1:]...)
				break
			}
		}
	}
	for i := range t.Follows {
		if t.Follows[i].Thread == thread {
			t.Follows = append(t.Follows[:i], t.Follows[i+1:]...)
			break
		}
	}
}

// HandBack releases a Ticket's ownership of a Thread's human messages and
// requeues them when no other Ticket owns them (002:30-32). The Ticket keeps its
// Subscription.
func (e *Engine) HandBack(ticketID domain.ID, thread, reason string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	t, ok := e.tickets[ticketID]
	if !ok {
		return fmt.Errorf("engine: no ticket %q", ticketID)
	}
	var candidates []domain.ID
	for i := range t.Inputs {
		in := &t.Inputs[i]
		if in.Thread != thread || in.Kind != domain.InputTrigger || !in.Human || in.Read || in.Acked || in.HandedBack {
			continue
		}
		// The Trigger that opened the Ticket is not a follow-up to hand back.
		if in.TriggerID == t.Origin {
			continue
		}
		in.HandedBack = true
		candidates = append(candidates, in.TriggerID)
	}
	// A message goes to a fresh Eval only after no Ticket still owns it (002:30-32).
	for _, triggerID := range candidates {
		if e.ownedByOtherLocked(triggerID, ticketID) {
			continue
		}
		e.requeueTriggerLocked(triggerID)
	}
	for i := range t.FollowupOwners {
		if t.FollowupOwners[i] == string(ticketID) {
			t.FollowupOwners = append(t.FollowupOwners[:i], t.FollowupOwners[i+1:]...)
			break
		}
	}
	return nil
}

// ownedByOtherLocked reports whether a live Ticket other than except still owns
// the human message that a Trigger delivered.
func (e *Engine) ownedByOtherLocked(triggerID, except domain.ID) bool {
	for id, tk := range e.tickets {
		if id == except || tk.Ended != nil {
			continue
		}
		for _, in := range tk.Inputs {
			if in.TriggerID == triggerID && in.Kind == domain.InputTrigger && in.Human &&
				!in.Read && !in.Acked && !in.HandedBack {
				return true
			}
		}
	}
	return false
}

// InterruptAssignment ends a running Assignment whose process died and starts a
// new one in the same Session, with the recovery note the executor reads before
// repeating an outside action (003:146, design §5.8). Persisted by the
// Persistence wrapper of the same name.
func (e *Engine) InterruptAssignment(ticketID domain.ID) error {
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
	session := ""
	if n := len(t.Assignments); n > 0 {
		last := &t.Assignments[n-1]
		session = last.SessionID
		if last.State != domain.AssignEnded {
			last.State = domain.AssignEnded
			last.EndedAt = now
			last.EndReason = domain.ReasonHostRestarted
		}
	}
	t.AddInput(domain.Input{
		ID:   domain.Key(domain.Global, "input", ticketID, "recovery", fmt.Sprint(now.UnixNano())),
		Kind: domain.InputNote, At: now,
		Text: "host restarted: check the directory, git history and GitHub before repeating any outside action",
	})
	t.Assignments = append(t.Assignments, domain.Assignment{
		ID:        domain.Key(domain.WorkspaceScope(t.Workspace), "assignment", ticketID, len(t.Assignments)),
		State:     domain.AssignPending,
		SessionID: session,
	})
	return nil
}

// RevalidateAndResume re-checks an ended Ticket's Mandate against the current
// Policy and resumes it when the Mandate still holds (002:133-141).
func (e *Engine) RevalidateAndResume(ticketID domain.ID) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	t, ok := e.tickets[ticketID]
	if !ok {
		return fmt.Errorf("engine: no ticket %q", ticketID)
	}
	if t.Ended == nil {
		return nil
	}
	if t.Ended.By == "human" && t.Ended.Outcome == domain.OutcomeStopped {
		return fmt.Errorf("engine: ticket %q was stopped by a person", ticketID)
	}
	p, ok := e.policies[t.Workspace]
	if !ok || !revalidate(p, t.Mandate) {
		t.Mandate.Revoked = domain.ReasonMandateRevoked
		e.releaseWaitingLocked(t)
		return nil
	}
	t.Ended = nil
	t.Delivery = nil
	t.FollowupOwners = nil
	t.Assignments = append(t.Assignments, domain.Assignment{
		ID:        domain.Key(domain.WorkspaceScope(t.Workspace), "assignment", t.ID, len(t.Assignments)),
		State:     domain.AssignPending,
		SessionID: t.LastAssignment().SessionID,
	})
	return nil
}

// PolicyChanged records a Policy version transition: Evals are queued again under
// the new version, affected live Tickets get a policy-changed Input, and no
// Mandate is revoked (002:109-117, 001:58).
func (e *Engine) PolicyChanged(from, to string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	now := e.clock()
	// A workspace still on the old version moves to the new one, so a requeued
	// Eval is judged under the current Policy.
	for id, pol := range e.policies {
		if pol.Version == from && to != "" {
			pol.Version = to
			e.policies[id] = pol
		}
	}
	for _, slot := range e.evals {
		if slot.Eval.State == domain.EvalDone || slot.Eval.State == domain.EvalUnavailable {
			continue
		}
		slot.Eval.PolicyVer = to
		slot.Eval.Deadline = now.Add(e.evalTimeout)
		if slot.Eval.State == domain.EvalRunning {
			slot.Eval.State = domain.EvalQueued
			slot.Eval.Attempts = 0
		}
	}
	for _, t := range e.tickets {
		if t.Ended != nil || t.Mandate.PolicyVersion != from {
			continue
		}
		why := e.policyEffectLocked(t, from, to)
		t.AddInput(domain.Input{
			ID:   domain.Key(domain.Global, "input", t.ID, "policy", from, to, fmt.Sprint(now.UnixNano())),
			Kind: domain.InputPolicy, At: now,
			Text: "Policy changed from " + from + " to " + to + "：" + why,
		})
	}
	return nil
}

// policyEffectLocked explains how a Policy transition bears on one Ticket: a
// matched rule changed or no longer applies, the requester may no longer start
// work, or proactive work is no longer accepted (002:113-115).
func (e *Engine) policyEffectLocked(t *domain.Ticket, from, to string) string {
	p := e.policies[t.Workspace]
	if !revalidate(p, t.Mandate) {
		return "该请求人可能已不能再发起工作（或匹配规则不再启用）"
	}
	if t.Mandate.Strength == domain.StrengthInferred && acceptMode(p) != domain.AcceptAuto {
		return "主动工作不再被接受（accept mode 已不是 auto）"
	}
	var missing []string
	for _, id := range t.Mandate.Rules {
		if !p.ruleEnabled(id) {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		return "匹配的规则已变更或不再适用：" + strings.Join(missing, ", ")
	}
	return "该变更不影响这张工单"
}
