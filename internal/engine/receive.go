package engine

// Receiving a Trigger: fan-out, the deterministic gate, ownership, resume, and
// queueing for the Thread's next Eval. No model is called here (design §2.1).

import (
	"fmt"
	"sort"
	"time"

	"github.com/zeokeel/super-agent/internal/domain"
)

// Receive takes one Trigger through fan-out, the deterministic gate, ownership
// and the Eval queue. It never calls a model: EvaluateDue does that outside the
// engine lock.
func (e *Engine) Receive(t domain.Trigger) (Outcome, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if _, ok := e.triggers[t.ID]; ok {
		return Outcome{}, ErrDuplicate
	}
	if t.At.IsZero() {
		t.At = e.clock()
	}
	tr := t
	e.triggers[t.ID] = &tr
	out := Outcome{Trigger: t.ID}

	// 1. Fan-out: every message on a subscribed Thread reaches its Tickets as
	//    context, unconditionally, and grants no authority (001:37, 002:20).
	fanout := e.fanoutLocked(&tr)

	// 2. Gate: the deterministic layer (002:11-16).
	policy, ws := e.policyForLocked(&tr)
	g := gate(policy, tr, e.participatingLocked(&tr))
	if !g.Admitted && tr.DefaultWorkspace == "" {
		// A global Trigger has no receiving workspace of its own. The merged policy
		// is conservative, so it may refuse a request one workspace would admit.
		// The Eval still runs (the assessor judges it and every candidate workspace
		// re-gates the accept), with the strictest strength that any workspace
		// grants: authorization is never widened, but a request is not dropped
		// before it is understood.
		if a, ok := e.anyWorkspaceAdmissionLocked(&tr); ok {
			g = a
		}
	}
	disp := domain.Disposition{TriggerID: t.ID, Strength: g.Strength, Basis: g.Basis,
		Reason: g.Reasons, TicketIDs: fanout}

	// 3. Ownership comes before the gate's refusal: every message on a
	//    subscribed Thread reaches its Tickets, and a person's message a live
	//    Ticket owns is its Executor's to decide, even if the gate would refuse it
	//    as a new request (002:20-24).
	if tr.Human {
		if owners := e.ownersLocked(&tr); len(owners) > 0 {
			for _, id := range owners {
				e.addOwnedInputLocked(&tr, id)
			}
			disp.Kind = domain.DispOwned
			disp.TicketIDs = append(owners, fanout...)
			e.dispositions[t.ID] = disp
			out.Kind = disp.Kind
			out.Reasons = []domain.Reason{{Code: domain.ReasonOwnedFollowup}}
			return out, nil
		}
		// 4. An ended Ticket the person did not stop resumes under its unchanged
		//    Mandate, with no Eval and no Decision (002:127). A Mandate the current
		//    Policy no longer holds is revoked and the message falls through (002:141).
		if resumed, handled, queued := e.resumeEndedLocked(&tr, &disp); handled {
			disp.Kind = domain.DispResumed
			if queued {
				// The message could not resume the Ticket; 002:141 sends it to Eval.
				disp.Kind = domain.DispEvaluating
			}
			if e.paused {
				disp.Kind = domain.DispHeld
				out.Held = true
			}
			disp.TicketIDs = append(resumed, fanout...)
			e.dispositions[t.ID] = disp
			out.Kind = disp.Kind
			return out, nil
		}
	}

	if !g.Admitted {
		disp.Kind = domain.DispFiltered
		if len(fanout) > 0 {
			disp.Kind = domain.DispFanout
		}
		e.dispositions[t.ID] = disp
		out.Kind = disp.Kind
		out.Reasons = g.Reasons
		return out, nil
	}

	// 5. Queue for the Thread's next Eval, merging into a pending batch when one
	//    is already waiting (001:29,43).
	evalID := e.enqueueEvalLocked(&tr, policy, ws, g)
	disp.Kind = domain.DispEvaluating
	disp.EvalID = evalID
	e.dispositions[t.ID] = disp
	out.Kind = disp.Kind
	if e.paused {
		out.Held = true
	}
	return out, nil
}

// anyWorkspaceAdmissionLocked returns the strictest admission any workspace
// grants a global Trigger, or false when none admits it.
func (e *Engine) anyWorkspaceAdmissionLocked(t *domain.Trigger) (gateResult, bool) {
	names := make([]string, 0, len(e.policies))
	for id := range e.policies {
		names = append(names, string(id))
	}
	sort.Strings(names)
	var best gateResult
	found := false
	for _, name := range names {
		g := gate(e.policies[domain.WorkspaceID(name)], *t, e.participatingLocked(t))
		if !g.Admitted {
			continue
		}
		if !found || strengthRank(g.Strength) < strengthRank(best.Strength) {
			best = g
			found = true
		}
	}
	return best, found
}

// fanoutLocked delivers a Trigger as context Input to every live Ticket
// subscribed to any of its Threads. It returns those Ticket ids.
func (e *Engine) fanoutLocked(t *domain.Trigger) []domain.ID {
	seen := map[domain.ID]bool{}
	var out []domain.ID
	for _, ref := range t.Threads {
		key := domain.ThreadKey(t.Scope, ref.Source, ref.ID)
		th, ok := e.threads[key]
		if !ok {
			continue
		}
		th.triggerIDs = append(th.triggerIDs, t.ID)
		if t.At.After(th.lastAt) {
			th.lastAt = t.At
		}
		for _, id := range th.ticketIDs {
			if seen[id] {
				continue
			}
			tk := e.tickets[id]
			if tk == nil || tk.Ended != nil {
				continue
			}
			if wsID, ok := t.Scope.WorkspaceIDOf(); ok && tk.Workspace != wsID {
				// A Thread belongs to one workspace; a Ticket never receives
				// another workspace's Thread by fan-out.
				continue
			}
			ctx := !e.ticketOwnsThread(tk, key)
			if tk.AddInput(domain.Input{
				ID:        domain.Key(domain.Global, "input", t.ID, key),
				Kind:      domain.InputTrigger,
				TriggerID: t.ID,
				Thread:    key,
				Text:      t.Text,
				At:        t.At,
				Human:     t.Human,
				Context:   ctx,
			}) {
				out = append(out, id)
			}
			seen[id] = true
		}
	}
	return out
}

// ownersLocked returns the live Tickets that own a human message (002:22-24).
func (e *Engine) ownersLocked(t *domain.Trigger) []domain.ID {
	seen := map[domain.ID]bool{}
	var out []domain.ID
	for _, ref := range t.Threads {
		key := domain.ThreadKey(t.Scope, ref.Source, ref.ID)
		th, ok := e.threads[key]
		if !ok {
			continue
		}
		for _, id := range th.ticketIDs {
			tk := e.tickets[id]
			if tk == nil || tk.Ended != nil || seen[id] {
				continue
			}
			if e.ticketOwnsThread(tk, key) {
				seen[id] = true
				out = append(out, id)
			}
		}
	}
	return out
}

// addOwnedInputLocked delivers a human message to the Ticket that owns it and
// records the ownership so the main Session may only stop (002:24-26).
func (e *Engine) addOwnedInputLocked(t *domain.Trigger, ticketID domain.ID) {
	tk := e.tickets[ticketID]
	if tk == nil {
		return
	}
	tk.AddInput(domain.Input{
		ID:        domain.Key(domain.Global, "input", t.ID, tk.ID),
		Kind:      domain.InputTrigger,
		TriggerID: t.ID,
		Thread:    threadKeyOf(t),
		Text:      t.Text,
		At:        t.At,
		Human:     true,
	})
	if !containsString(tk.FollowupOwners, string(tk.ID)) {
		tk.FollowupOwners = append(tk.FollowupOwners, string(tk.ID))
	}
}

// resumeEndedLocked resumes the ended Ticket a person's message waits for, under
// its unchanged Mandate. It returns the resumed ids and whether the message was
// handled. A failed revalidation leaves the Ticket ended and lets the message
// fall through to Eval (002:141).
func (e *Engine) resumeEndedLocked(t *domain.Trigger, disp *domain.Disposition) ([]domain.ID, bool, bool) {
	var handled, queued bool
	var resumed []domain.ID
	for _, ref := range t.Threads {
		key := domain.ThreadKey(t.Scope, ref.Source, ref.ID)
		th, ok := e.threads[key]
		if !ok {
			continue
		}
		for _, id := range append([]domain.ID(nil), th.ticketIDs...) {
			tk := e.tickets[id]
			if tk == nil || tk.Ended == nil {
				continue
			}
			if tk.Ended.By == "human" && tk.Ended.Outcome == domain.OutcomeStopped {
				continue // a stopped Ticket receives nothing (001:37, 002:129)
			}
			if !e.ticketOwnsThread(tk, key) {
				continue // context-only subscription does not resume (002:42)
			}
			in := domain.Input{
				ID:        domain.Key(domain.Global, "resume", t.ID, tk.ID),
				Kind:      domain.InputResumed,
				TriggerID: t.ID,
				Thread:    key,
				Text:      t.Text,
				At:        t.At,
				Human:     true,
			}
			if e.paused {
				tk.AddInput(in)
				handled = true
				continue
			}
			p, ok := e.policies[tk.Workspace]
			if !ok || !revalidate(p, tk.Mandate) {
				tk.Mandate.Revoked = domain.ReasonMandateRevoked
				tk.AddInput(in)
				e.releaseWaitingLocked(tk)
				// 002:141: the message is treated as having reached no Ticket, so it
				// goes to Eval even when the current accept mode would not admit it
				// as new mail.
				e.enqueueEvalLocked(t, p, tk.Workspace, gateResult{
					Admitted: true, Strength: tk.Mandate.Strength, Basis: tk.Mandate.Basis})
				handled = true
				queued = true
				continue
			}
			e.reopenLocked(tk, in)
			resumed = append(resumed, tk.ID)
			handled = true
		}
	}
	if handled {
		disp.TicketIDs = append(disp.TicketIDs, resumed...)
	}
	return resumed, handled, queued
}

// reopenLocked makes an ended Ticket live again with a resumed marker queued
// ahead of the messages that waited.
func (e *Engine) reopenLocked(tk *domain.Ticket, in domain.Input) {
	tk.Ended = nil
	tk.Delivery = nil
	tk.FollowupOwners = nil
	tk.Inputs = append([]domain.Input{in}, tk.Inputs...)
	tk.Assignments = append(tk.Assignments, domain.Assignment{
		ID:        domain.Key(domain.WorkspaceScope(tk.Workspace), "assignment", tk.ID, len(tk.Assignments)),
		State:     domain.AssignPending,
		SessionID: tk.LastAssignment().SessionID,
	})
}

// releaseWaitingLocked sends a Ticket's untouched waiting Inputs back to Eval.
func (e *Engine) releaseWaitingLocked(tk *domain.Ticket) []domain.ID {
	var released []domain.ID
	for i := range tk.Inputs {
		in := &tk.Inputs[i]
		if in.Kind != domain.InputTrigger || !in.Human || in.Context || in.Read || in.Acked || in.HandedBack {
			continue
		}
		in.HandedBack = true
		released = append(released, in.TriggerID)
	}
	for _, triggerID := range released {
		e.requeueTriggerLocked(triggerID)
	}
	return released
}

// requeueTriggerLocked queues one Trigger for a fresh Eval on its Thread if no
// Eval holds it. The gate judges it again as ordinary mail (002:32).
func (e *Engine) requeueTriggerLocked(triggerID domain.ID) {
	t, ok := e.triggers[triggerID]
	if !ok {
		return
	}
	key := threadKeyOf(t)
	if key == "" {
		return
	}
	if slot, ok := e.evals[key]; ok {
		switch slot.Eval.State {
		case domain.EvalQueued:
			slot.Eval.TriggerIDs = append(slot.Eval.TriggerIDs, triggerID)
			slot.Eval.Primary = triggerID
			return
		case domain.EvalRunning:
			slot.Pending = append(slot.Pending, triggerID)
			return
		case domain.EvalUnavailable:
			slot.Eval.State = domain.EvalQueued
			slot.Eval.Attempts = 0
			slot.Eval.TriggerIDs = append(slot.Eval.TriggerIDs, triggerID)
			return
		}
	}
	policy, ws := e.policyForLocked(t)
	g := gate(policy, *t, e.participatingLocked(t))
	if !g.Admitted {
		e.dispositions[triggerID] = domain.Disposition{TriggerID: triggerID,
			Kind: domain.DispFiltered, Strength: g.Strength, Reason: g.Reasons}
		return
	}
	e.enqueueEvalLocked(t, policy, ws, g)
}

// enqueueEvalLocked adds a Trigger to the Thread's Eval slot, merging into a
// queued batch or into the next batch when one runs (001:29,43).
func (e *Engine) enqueueEvalLocked(t *domain.Trigger, policy Policy, ws domain.WorkspaceID, g gateResult) domain.ID {
	key := threadKeyOf(t)
	if key == "" {
		return ""
	}
	if _, ok := e.threads[key]; !ok {
		e.threads[key] = &thread{key: key}
	}
	if slot, ok := e.evals[key]; ok {
		switch slot.Eval.State {
		case domain.EvalQueued:
			slot.Eval.TriggerIDs = append(slot.Eval.TriggerIDs, t.ID)
			slot.Eval.Primary = t.ID
			return slot.Eval.ID
		case domain.EvalRunning:
			slot.Pending = append(slot.Pending, t.ID)
			return slot.Eval.ID
		}
	}
	now := e.clock()
	eval := domain.Eval{
		ID:         domain.Key(domain.Global, "eval", key, policy.Version, fmt.Sprint(now.UnixNano()), string(t.ID)),
		Thread:     key,
		Workspace:  ws,
		TriggerIDs: []domain.ID{t.ID},
		Primary:    t.ID,
		Strength:   g.Strength,
		Actor:      t.Actor,
		Basis:      g.Basis,
		PolicyVer:  policy.Version,
		State:      domain.EvalQueued,
		QueuedAt:   now,
	}
	e.evals[key] = &evalSlot{Eval: eval}
	if th := e.threads[key]; th != nil && now.After(th.lastAt) {
		th.lastAt = now
	}
	return eval.ID
}

// DispatchInput is a task a person typed in the panel.
type DispatchInput struct {
	Text      string
	Workspace domain.WorkspaceID // empty means "let the Router decide"
	At        time.Time
}

// Dispatch turns a panel task into work through the same Receive path
// (design §6.9).
func (e *Engine) Dispatch(in DispatchInput) (Outcome, error) {
	at := in.At
	if at.IsZero() {
		at = e.clock()
	}
	scope := domain.Global
	if in.Workspace != "" {
		scope = domain.WorkspaceScope(in.Workspace)
	}
	id := domain.Key(scope, "local", in.Text, at.UnixMilli())
	tr := domain.Trigger{
		ID:               id,
		Scope:            scope,
		Source:           "local",
		Threads:          []domain.ThreadRef{{Source: "local", ID: "task:" + string(id)}},
		Actor:            domain.Actor{ID: "operator"},
		Human:            true,
		Self:             true,
		Addressed:        domain.Addressed{Direct: true},
		Conversation:     domain.Conversation{Kind: domain.ConvDirect, ID: "panel", Name: "本地面板"},
		Text:             in.Text,
		At:               at,
		Channel:          domain.ChannelRef{Source: "local", Thread: "task:" + string(id)},
		DefaultWorkspace: in.Workspace,
		Row:              "panel",
		ScopeReceivesAll: true,
	}
	return e.Receive(tr)
}
