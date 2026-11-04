package engine

// Evals: batching a Thread's Triggers, calling the assessor outside the lock,
// applying the Decision inside it, and the timeout/unavailable rules of
// 002:147-151 (design §2.1).

import (
	"fmt"
	"sort"
	"time"

	"github.com/zeokeel/super-agent/internal/brief"
	"github.com/zeokeel/super-agent/internal/domain"
)

// EvaluateDue judges the Threads whose Eval is due. The model is called outside
// the lock; only marking the Eval running and applying its Decision happen inside
// it. One Outcome is returned per Eval that finished or was requeued.
func (e *Engine) EvaluateDue(now time.Time) ([]Outcome, error) {
	if now.IsZero() {
		now = time.Now()
	}
	e.mu.Lock()
	if e.paused {
		e.mu.Unlock()
		return nil, nil
	}
	e.expireDueLocked(now)
	due := e.dueEvalsLocked()
	e.mu.Unlock()

	outs := make([]Outcome, 0, len(due))
	for _, ev := range due {
		in, ok := e.beginEval(ev.Thread, ev.ID, now)
		if !ok {
			continue
		}
		judged, assessErr := e.assess(in)
		out, err := e.finishEval(ev.Thread, ev.ID, judged, assessErr, now)
		if err != nil {
			return outs, err
		}
		outs = append(outs, out)
	}
	return outs, nil
}

// assess calls the configured assessor, guarding against a nil one.
func (e *Engine) assess(in AssessInput) (AssessOutput, error) {
	if e.assessor == nil {
		return AssessOutput{}, fmt.Errorf("engine: no assessor configured")
	}
	return e.assessor.Assess(contextBackground(), in)
}

// beginEval marks one queued Eval running and builds the assessment input.
func (e *Engine) beginEval(threadKey string, evalID domain.ID, now time.Time) (AssessInput, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	slot, ok := e.evals[threadKey]
	if !ok || slot.Eval.ID != evalID || slot.Eval.State != domain.EvalQueued {
		return AssessInput{}, false
	}
	slot.Eval.State = domain.EvalRunning
	slot.Eval.StartedAt = now
	slot.Eval.Deadline = now.Add(e.evalTimeout)
	slot.Eval.CandidateDelivered = true
	slot.Eval.PausedAt = time.Time{}
	return e.assessInputLocked(&slot.Eval), true
}

// expireDueLocked requeues an Eval whose delivered candidate ran out of time; the
// third consecutive expiry records unavailable (002:149-151).
func (e *Engine) expireDueLocked(now time.Time) {
	for _, slot := range e.evals {
		ev := &slot.Eval
		if ev.State == domain.EvalRunning {
			// A running Eval only exists inside EvaluateDue; reaching here means a
			// previous process crashed mid-Eval, so its batch is queued again.
			ev.State = domain.EvalQueued
			ev.Deadline = now.Add(e.evalTimeout)
			continue
		}
		if ev.State != domain.EvalQueued || !ev.CandidateDelivered || ev.Deadline.IsZero() || ev.Deadline.After(now) {
			continue
		}
		ev.Attempts++
		if ev.Attempts > 2 {
			ev.State = domain.EvalUnavailable
			ev.LastError = "timeout"
			e.recordUnavailableLocked(ev, []domain.Reason{{Code: domain.ReasonTimeout}})
			e.finishSlotLocked(slot)
			continue
		}
		ev.Deadline = now.Add(e.evalTimeout)
	}
}

// recordUnavailableLocked stores the unavailable Decision of an Eval.
func (e *Engine) recordUnavailableLocked(ev *domain.Eval, reasons []domain.Reason) domain.Decision {
	dec := domain.Decision{
		ID:         domain.Key(domain.Global, "decision", ev.ID),
		Kind:       domain.DecUnavailable,
		Reasons:    reasons,
		Workspace:  ev.Workspace,
		Thread:     ev.Thread,
		EvalID:     ev.ID,
		TriggerIDs: append([]domain.ID(nil), ev.TriggerIDs...),
		At:         e.clock(),
	}
	e.decisions[dec.ID] = dec
	ev.DecisionID = dec.ID
	for _, id := range ev.TriggerIDs {
		d := e.dispositions[id]
		d.DecisionID = dec.ID
		d.EvalID = ev.ID
		d.Kind = domain.DispDecided
		e.dispositions[id] = d
	}
	return dec
}

// finishEval applies the assessor's judgement inside the lock.
func (e *Engine) finishEval(threadKey string, evalID domain.ID, judged AssessOutput, assessErr error, now time.Time) (Outcome, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	slot, ok := e.evals[threadKey]
	if !ok || slot.Eval.ID != evalID {
		return Outcome{Kind: domain.DispEvaluating}, nil
	}
	ev := &slot.Eval
	out := Outcome{Trigger: ev.Primary, Workspace: ev.Workspace}
	for _, id := range ev.TriggerIDs {
		out.TriggerIDs = append(out.TriggerIDs, id)
	}
	// A pause that began while the model was thinking does not discard the work.
	if e.paused {
		ev.State = domain.EvalQueued
		ev.Deadline = now.Add(e.evalTimeout)
		ev.PausedAt = e.pauseStarted
		out.Kind = domain.DispHeld
		return out, nil
	}
	// A Policy change while the model was thinking makes the judgement stale.
	if current, ok := e.policies[ev.Workspace]; ok && current.Version != "" && current.Version != ev.PolicyVer {
		e.requeueStaleLocked(slot, now)
		e.finishSlotLocked(slot)
		out.Kind = domain.DispEvaluating
		out.Reasons = []domain.Reason{{Code: domain.ReasonPolicyChanged}}
		return out, nil
	}
	if assessErr != nil {
		ev.Attempts++
		ev.LastError = assessErr.Error()
		// An assessment failure records unavailable; a later Trigger starts a new
		// Eval (002:151). A timeout retries twice first (002:149-151).
		dec := e.recordUnavailableLocked(ev, []domain.Reason{{Code: domain.ReasonTimeout,
			Values: map[string]string{"why": assessErr.Error()}}})
		ev.State = domain.EvalUnavailable
		out.Kind = domain.DispDecided
		out.Decision = dec
		out.Reasons = dec.Reasons
		e.finishSlotLocked(slot)
		return out, nil
	}
	dec, applyOut := e.applyEvalLocked(slot, judged, now)
	ev.State = domain.EvalDone
	ev.DecisionID = dec.ID
	out.Kind = domain.DispDecided
	out.Decision = dec
	out.Reasons = dec.Reasons
	out.Ticket = applyOut.Ticket
	out.Workspace = dec.Workspace
	out.NeedsPerson = applyOut.NeedsPerson
	out.Proposal = applyOut.Proposal
	out.Question = applyOut.Question
	e.finishSlotLocked(slot)
	return out, nil
}

// finishSlotLocked starts the next batch of a Thread after one Eval finished, so
// Triggers that arrived during it are judged next, not lost (001:29).
func (e *Engine) finishSlotLocked(slot *evalSlot) {
	if len(slot.Pending) == 0 {
		return
	}
	pending := append([]domain.ID(nil), slot.Pending...)
	slot.Pending = nil
	primary, ok := e.triggers[pending[0]]
	if !ok {
		return
	}
	policy, ws := e.policyForLocked(primary)
	g := gate(policy, *primary, e.participatingLocked(primary))
	if !g.Admitted {
		return
	}
	now := e.clock()
	slot.Eval = domain.Eval{
		ID:         domain.Key(domain.Global, "eval", slot.Eval.Thread, policy.Version, fmt.Sprint(now.UnixNano()), string(primary.ID)),
		Thread:     slot.Eval.Thread,
		Workspace:  ws,
		TriggerIDs: pending,
		Primary:    primary.ID,
		Strength:   g.Strength,
		Actor:      primary.Actor,
		Basis:      g.Basis,
		PolicyVer:  policy.Version,
		State:      domain.EvalQueued,
		QueuedAt:   now,
	}
}

// requeueStaleLocked queues an Eval's batch again under the current Policy.
func (e *Engine) requeueStaleLocked(slot *evalSlot, now time.Time) {
	ev := &slot.Eval
	ev.State = domain.EvalQueued
	ev.Attempts = 0
	ev.Deadline = now.Add(e.evalTimeout)
	ev.CandidateDelivered = false
	if p, ok := e.policies[ev.Workspace]; ok {
		ev.PolicyVer = p.Version
	}
}

// assessInputLocked snapshots everything an Eval may judge: the batch, the
// Thread, every Ticket on it including ended ones, the current Policy and the
// recent Decisions (001:33).
func (e *Engine) assessInputLocked(ev *domain.Eval) AssessInput {
	primary := e.triggerOrNil(ev.Primary)
	pol, ok := e.policies[ev.Workspace]
	if !ok && primary != nil {
		// A global Trigger has no workspace yet; the Eval runs under the merged
		// policy the gate used (002:11).
		pol, _ = e.policyForLocked(primary)
	}
	in := AssessInput{EvalID: ev.ID, Policy: policyViewOf(pol)}
	if primary != nil {
		in.Trigger = *primary
		in.Batch = e.batchTriggersLocked(ev)
	}
	seen := map[domain.WorkspaceID]bool{}
	add := func(id domain.WorkspaceID, score int, why string) {
		if id == "" || seen[id] {
			return
		}
		if _, ok := e.workspaces[id]; !ok {
			return
		}
		seen[id] = true
		in.Candidates = append(in.Candidates, brief.Candidate{ID: id, Score: score, Matched: []string{why}})
	}
	if primary != nil {
		add(primary.DefaultWorkspace, 1000, "source-bound")
		if e.index != nil {
			for _, c := range e.index.Recall(primary.Text) {
				add(c.ID, c.Score, c.Matched[0])
			}
		}
		in.Briefs = map[domain.WorkspaceID]string{}
		tickets := e.ticketSliceLocked()
		for _, c := range in.Candidates {
			if w := e.workspaces[c.ID]; w != nil {
				in.Briefs[c.ID] = brief.RenderWith(*w, e.clock(),
					brief.Digest(*w, tickets, e.clock(), brief.DigestMaxRunes))
			}
		}
	}
	th := e.threads[ev.Thread]
	in.Thread = ThreadSnapshot{Key: ev.Thread}
	if th != nil {
		in.Thread.TriggerIDs = append([]domain.ID(nil), th.triggerIDs...)
		in.Thread.TicketIDs = append([]domain.ID(nil), th.ticketIDs...)
		for _, id := range th.decisionIDs {
			if d, ok := e.decisions[id]; ok {
				in.Thread.Decisions = append(in.Thread.Decisions, d)
				in.Decisions = append(in.Decisions, d)
			}
		}
	}
	// Every Ticket on the Thread, including ended ones, so the assessment can
	// avoid duplicates (001:33,52).
	for _, tid := range in.Thread.TicketIDs {
		if t, ok := e.tickets[tid]; ok {
			in.Tickets = append(in.Tickets, copyTicket(t))
		}
	}
	sort.Slice(in.Decisions, func(i, j int) bool { return in.Decisions[i].At.Before(in.Decisions[j].At) })
	return in
}

// applyEvalLocked turns an assessor judgement into a Decision, applying the
// accept mode, the target workspace's authorization and the Ticket lifecycle.
func (e *Engine) applyEvalLocked(slot *evalSlot, judged AssessOutput, now time.Time) (domain.Decision, Outcome) {
	ev := &slot.Eval
	out := Outcome{Trigger: ev.Primary, Workspace: ev.Workspace}
	primary, hasPrimary := e.triggers[ev.Primary]
	if !hasPrimary && len(ev.TriggerIDs) > 0 {
		primary, hasPrimary = e.triggers[ev.TriggerIDs[0]]
	}
	if !hasPrimary {
		return domain.Decision{ID: domain.Key(domain.Global, "decision", ev.ID), Kind: domain.DecIgnore}, out
	}
	ws := judged.Workspace
	if ws == "" {
		ws = ev.Workspace
	}
	strength := ev.Strength
	if _, ok := e.workspaces[ws]; !ok {
		// A decision without a workspace cannot start work: ask a person.
		dec := domain.Decision{
			ID: domain.Key(domain.Global, "decision", ev.ID), Kind: domain.DecIgnore,
			Reasons:   []domain.Reason{{Code: domain.ReasonClarify, Values: map[string]string{"why": "no workspace"}}},
			Workspace: ws, Thread: ev.Thread, EvalID: ev.ID, TriggerIDs: ev.TriggerIDs, At: now,
		}
		e.recordDecisionLocked(dec, ev)
		out.NeedsPerson = true
		out.Question = "不知道该派给哪个工作区：" + firstSentence(primary.Text)
		return dec, out
	}
	// Each target workspace authorizes its own Ticket (§4.5, risk 15).
	wsGate := e.gateForWorkspaceLocked(primary, ws)
	if judged.Kind == domain.DecAccept && !wsGate.Admitted {
		judged.Kind = domain.DecIgnore
		judged.Reasons = wsGate.Reasons
	}
	if judged.Kind == domain.DecAccept && strengthRank(wsGate.Strength) < strengthRank(strength) {
		strength = wsGate.Strength
	}
	p := e.policies[ws]

	dec := domain.Decision{
		ID:         domain.Key(domain.Global, "decision", ev.ID),
		Kind:       judged.Kind,
		Reasons:    judged.Reasons,
		Workspace:  ws,
		Thread:     ev.Thread,
		EvalID:     ev.ID,
		TriggerIDs: append([]domain.ID(nil), ev.TriggerIDs...),
		RulesHit:   judged.Rules,
		At:         now,
	}
	if len(dec.Reasons) == 0 {
		dec.Reasons = []domain.Reason{{Code: domain.ReasonSaid, Values: map[string]string{"text": primary.Text}}}
	}

	switch dec.Kind {
	case domain.DecAccept:
		matched := matchedRuleIDs(judged.Rules)
		// A proactive accept needs at least one matched enabled rule (002:101).
		if strength != domain.StrengthExplicit && len(matched) == 0 {
			dec.Kind = domain.DecIgnore
			dec.Reasons = []domain.Reason{{Code: domain.ReasonNoRuleMatched}}
			break
		}
		// The accept mode is the safety valve for inferred candidates only; an
		// explicit request works in every mode (002:95-101).
		if strength != domain.StrengthExplicit {
			switch acceptMode(p) {
			case domain.AcceptAuto:
			case domain.AcceptOff:
				dec.Kind = domain.DecIgnore
				dec.Reasons = []domain.Reason{{Code: domain.ReasonAcceptModeOff}}
			default:
				dec.Kind = domain.DecIgnore
				dec.Reasons = []domain.Reason{{Code: domain.ReasonObserved,
					Values: map[string]string{"would_accept": string(ws)}}}
			}
		}
		if dec.Kind == domain.DecAccept {
			dec.Mandate = &domain.Mandate{
				Strength:      strength,
				PolicyVersion: p.Version,
				Actor:         primary.Actor,
				Workspace:     ws,
				Scope:         primary.Scope,
				Basis:         ev.Basis,
				Rules:         matched,
			}
		}
	case domain.DecStop:
		dec.Reasons = append(dec.Reasons, domain.Reason{Code: domain.ReasonHumanStop})
	}

	// The approval gate is opt-in; the default path is the prototype's Eval
	// accept creating the Ticket (001:35).
	if dec.Kind == domain.DecAccept && p.RequireApproval && strength != domain.StrengthExplicit {
		dec.Kind = domain.DecPropose
		dec.Proposal = e.proposalLocked(primary, dec)
		dec.Mandate = nil
	}

	dec.Reply = e.replyPlanLocked(primary, dec, p)

	switch dec.Kind {
	case domain.DecAccept:
		// The same Trigger accepted twice creates one Ticket, even across retries
		// of its Eval (001:28).
		if existing, ok := e.accepted[primary.ID]; ok {
			dec.Kind = domain.DecIgnore
			dec.Mandate = nil
			dec.Reasons = []domain.Reason{{Code: domain.ReasonAcceptedBefore, Values: map[string]string{"ticket": string(existing)}}}
			break
		}
		if tk := e.createTicketLocked(dec, ev, now); tk != nil {
			out.Ticket = tk.ID
		}
	case domain.DecStop:
		if toEnd := e.stopTargetLocked(ev); toEnd != nil {
			e.endTicketLocked(toEnd, "human", domain.OutcomeStopped, domain.ReasonHumanStop, now)
		}
	}
	if dec.Kind == domain.DecPropose {
		out.Proposal = true
		if dec.Proposal != nil {
			out.Question = dec.Proposal.Intent
		}
	}
	e.recordDecisionLocked(dec, ev)
	out.Workspace = ws
	out.Reasons = dec.Reasons
	return dec, out
}

// recordDecisionLocked stores a Decision and links it to its Thread and Triggers.
func (e *Engine) recordDecisionLocked(dec domain.Decision, ev *domain.Eval) {
	e.decisions[dec.ID] = dec
	if len(ev.TriggerIDs) > 0 {
		e.decisionTrigger[dec.ID] = ev.TriggerIDs[len(ev.TriggerIDs)-1]
	}
	for _, id := range ev.TriggerIDs {
		d := e.dispositions[id]
		d.DecisionID = dec.ID
		d.EvalID = ev.ID
		d.Kind = domain.DispDecided
		if dec.Mandate != nil {
			d.Strength = dec.Mandate.Strength
			d.Basis = dec.Mandate.Basis
		}
		e.dispositions[id] = d
	}
	if th, ok := e.threads[ev.Thread]; ok {
		th.decisionIDs = append(th.decisionIDs, dec.ID)
	}
}

// createTicketLocked creates the Ticket an accept decided, with the batch as its
// first Inputs and one pending Assignment (001:35).
func (e *Engine) createTicketLocked(dec domain.Decision, ev *domain.Eval, now time.Time) *domain.Ticket {
	ws := dec.Workspace
	ticketID := domain.Key(domain.WorkspaceScope(ws), "ticket", dec.ID)
	if existing, ok := e.tickets[ticketID]; ok {
		return existing
	}
	primary := e.triggerOrNil(ev.Primary)
	title := ""
	if primary != nil {
		title = firstSentence(primary.Text)
	}
	e.nextNumber++
	t := &domain.Ticket{
		ID:        ticketID,
		Number:    e.nextNumber,
		Origin:    ev.Primary,
		Workspace: ws,
		Mandate:   *dec.Mandate,
		Title:     title,
		CreatedAt: now,
		Assignments: []domain.Assignment{{
			ID:    domain.Key(domain.WorkspaceScope(ws), "assignment", ticketID, 0),
			State: domain.AssignPending,
		}},
	}
	for _, id := range ev.TriggerIDs {
		tr, ok := e.triggers[id]
		if !ok {
			continue
		}
		t.SubscribedThreads = append(t.SubscribedThreads, threadKeys(tr.Threads, tr.Scope)...)
		t.AddInput(domain.Input{
			ID:        domain.Key(domain.Global, "input", id, "ticket", ticketID),
			Kind:      domain.InputTrigger,
			TriggerID: id,
			Thread:    threadKeyOf(tr),
			Text:      tr.Text,
			At:        tr.At,
			Human:     tr.Human,
		})
		e.accepted[id] = ticketID
	}
	t.DecisionIDs = append(t.DecisionIDs, dec.ID)
	e.tickets[ticketID] = t
	for _, id := range ev.TriggerIDs {
		tr, ok := e.triggers[id]
		if !ok {
			continue
		}
		for _, ref := range tr.Threads {
			th := e.threadFor(tr.Scope, ref, tr.At)
			if !containsID(th.ticketIDs, ticketID) {
				th.ticketIDs = append(th.ticketIDs, ticketID)
			}
		}
	}
	return t
}

// stopTargetLocked picks the Ticket an Eval's stop is about.
func (e *Engine) stopTargetLocked(ev *domain.Eval) *domain.Ticket {
	th := e.threads[ev.Thread]
	if th == nil {
		return nil
	}
	var best *domain.Ticket
	for _, id := range th.ticketIDs {
		t := e.tickets[id]
		if t == nil || t.Ended != nil {
			continue
		}
		if best == nil || t.CreatedAt.After(best.CreatedAt) {
			best = t
		}
	}
	return best
}

// matchedRuleIDs returns the ids of the rules an assessment matched.
func matchedRuleIDs(hits []domain.RuleHit) []string {
	out := make([]string, 0, len(hits))
	for _, h := range hits {
		if h.Matched {
			out = append(out, h.ID)
		}
	}
	return out
}

// proposalLocked writes the four things a person needs to judge someone else's
// request (design §4.10); it runs only when RequireApproval is on.
func (e *Engine) proposalLocked(t *domain.Trigger, dec domain.Decision) *domain.Proposal {
	w := e.workspaces[dec.Workspace]
	deliverable := "一个 PR（含变更说明与验证结果）"
	intent := "在仓库里改代码并提交 PR"
	var sensitive []string
	if w != nil {
		switch w.Kind {
		case domain.KindData:
			deliverable = "一份汇总（不含明细）+ 一段结论"
			intent = "从该工作区取数、汇总，然后把结论回复给 " + t.Actor.ID
			sensitive = append(sensitive, "客户数据外发")
		case domain.KindDocs:
			deliverable = "一份文档改动"
			intent = "修改文档并回复 " + t.Actor.ID
		case domain.KindRepo:
			intent = "在仓库里改代码、提交 PR，然后回复 " + t.Actor.ID
			if t.Source == "github" {
				sensitive = append(sensitive, "回复会出现在公开仓库")
			}
		}
	}
	sensitive = append(sensitive, "来自同事的请求：需要你确认它值不值得做")
	return &domain.Proposal{
		From:        t.Actor.ID,
		Said:        clip(t.Text, 60),
		Intent:      intent,
		Plan:        []string{"确认任务与范围", "在 " + string(dec.Workspace) + " 里执行", "整理产出并回复 " + t.Actor.ID},
		Deliverable: deliverable,
		Workspace:   string(dec.Workspace),
		Sensitive:   sensitive,
	}
}

// replyPlanLocked decides how to answer a message (design §4.10).
func (e *Engine) replyPlanLocked(t *domain.Trigger, dec domain.Decision, p Policy) domain.ReplyPlan {
	if t.Self || t.Source == "local" {
		return domain.ReplyPlan{Route: domain.ReplyDirect, Purpose: domain.PurposeAnswer,
			AuthorizedBy: "policy:operator"}
	}
	if dec.Kind != domain.DecAccept {
		if len(dec.Reasons) > 0 && dec.Reasons[0].Code == domain.ReasonClarify {
			return domain.ReplyPlan{Route: domain.ReplyOperator, Purpose: domain.PurposeAttention,
				Reason: dec.Reasons, AuthorizedBy: "policy:clarify"}
		}
		return domain.ReplyPlan{Route: domain.ReplyNone, Purpose: domain.PurposeAck,
			Reason: dec.Reasons, AuthorizedBy: "policy:no-work"}
	}
	mode := p.ReplyMode
	if mode == "" {
		mode = domain.ReplyModeNotify
	}
	if w := e.workspaces[dec.Workspace]; w != nil && w.Kind != domain.KindRepo && w.Kind != domain.KindMixed {
		return domain.ReplyPlan{Route: domain.ReplyOperator, Purpose: domain.PurposeAttention,
			Reason: []domain.Reason{{Code: domain.ReasonSensitiveWS,
				Values: map[string]string{"kind": string(w.Kind)}}},
			AuthorizedBy: "policy:kind"}
	}
	switch mode {
	case domain.ReplyModeAssist, domain.ReplyModeAuto:
		return domain.ReplyPlan{Route: domain.ReplyDirect, Purpose: domain.PurposeAck,
			Reason: []domain.Reason{{Code: "allowed_by_mode"}}, AuthorizedBy: "policy:" + string(mode)}
	default:
		return domain.ReplyPlan{Route: domain.ReplyOperator, Purpose: domain.PurposeAttention,
			Reason: []domain.Reason{{Code: domain.ReasonReplyNotify}}, AuthorizedBy: "policy:notify"}
	}
}
