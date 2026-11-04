package engine

// Persistence is the engine's durable half: it writes each command's result to the
// store as one transaction and rebuilds the in-memory state from disk on start, so
// a restart continues where the old process stopped (design §5.8, §7).
//
// The engine itself stays I/O-free and unchanged. A process wires persistence once
// with NewPersistence and then calls the entry points below instead of the plain
// Engine methods. Everything that must survive a crash is here: the Trigger and its
// Disposition, the Decision, the Ticket, the Thread subscriptions that make fan-out
// work after a restart, and the launch effect a restarted process has to re-run.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/zeokeel/super-agent/internal/domain"
	"github.com/zeokeel/super-agent/internal/effects"
	"github.com/zeokeel/super-agent/internal/outbox"
	"github.com/zeokeel/super-agent/internal/store"
)

// EffectLaunch is the durable effect kind that starts a Ticket's pending
// Assignment. The executor registers a handler for it; a row left behind by a
// crash is re-run by the next start (design §7.2).
const EffectLaunch = "launch"

// LaunchPayload is the payload of an EffectLaunch row.
type LaunchPayload struct {
	TicketID     domain.ID `json:"ticketId"`
	AssignmentID domain.ID `json:"assignmentId"`
	// Priority is "human" for a person's message or a status request, "normal"
	// otherwise: the human kind has one slot of its own (003:59).
	Priority string `json:"priority,omitempty"`
}

// Persistence writes engine commands to the store and rebuilds the engine from it.
type Persistence struct {
	e   *Engine
	st  *store.Store
	q   *effects.Queue
	ob  *outbox.Outbox
	now func() time.Time
}

// NewPersistence attaches e to st and loads the state a previous process left.
// q and ob may be nil: without a queue no launch effect is enqueued, and without
// an outbox replies stay in memory only.
func NewPersistence(e *Engine, st *store.Store, q *effects.Queue, ob *outbox.Outbox) (*Persistence, error) {
	if e == nil || st == nil {
		return nil, errors.New("engine: persistence needs an engine and a store")
	}
	p := &Persistence{e: e, st: st, q: q, ob: ob, now: time.Now}
	if err := p.Load(); err != nil {
		return nil, err
	}
	return p, nil
}

// Engine returns the in-memory engine this persistence writes for.
func (p *Persistence) Engine() *Engine { return p.e }

// Outbox returns the wired outbox, or nil.
func (p *Persistence) Outbox() *outbox.Outbox { return p.ob }

// Load throws away the in-memory state and rebuilds it from the store. It is
// called once on start and again after a failed write, so memory never keeps a
// command the disk refused.
func (p *Persistence) Load() error {
	wss, err := p.st.Workspaces()
	if err != nil {
		return fmt.Errorf("engine: load workspaces: %w", err)
	}
	policies, err := p.st.WorkspacePolicies()
	if err != nil {
		return fmt.Errorf("engine: load policies: %w", err)
	}
	triggers, err := p.st.Triggers()
	if err != nil {
		return fmt.Errorf("engine: load triggers: %w", err)
	}
	threads, err := p.st.Threads()
	if err != nil {
		return fmt.Errorf("engine: load threads: %w", err)
	}
	decisions, err := p.st.Decisions()
	if err != nil {
		return fmt.Errorf("engine: load decisions: %w", err)
	}
	tickets, err := p.st.Tickets()
	if err != nil {
		return fmt.Errorf("engine: load tickets: %w", err)
	}
	keys, err := p.st.OutboxKeys()
	if err != nil {
		return fmt.Errorf("engine: load outbox keys: %w", err)
	}
	pending, err := p.st.PendingOutbox()
	if err != nil {
		return fmt.Errorf("engine: load pending outbox: %w", err)
	}

	e := p.e
	e.mu.Lock()
	defer e.mu.Unlock()

	e.workspaces = make(map[domain.WorkspaceID]*domain.Workspace, len(wss))
	e.policies = make(map[domain.WorkspaceID]Policy, len(policies))
	e.triggers = make(map[domain.ID]*domain.Trigger, len(triggers))
	e.dispositions = make(map[domain.ID]domain.Disposition, len(triggers))
	e.threads = make(map[string]*thread, len(threads))
	e.tickets = make(map[domain.ID]*domain.Ticket, len(tickets))
	e.decisions = make(map[domain.ID]domain.Decision, len(decisions))
	e.decisionTrigger = make(map[domain.ID]domain.ID, len(decisions))
	e.outbox = nil
	e.sentKeys = make(map[string]bool, len(keys))
	e.nextNumber = 0

	for _, w := range wss {
		ws := w
		e.workspaces[ws.ID] = &ws
		var pol Policy
		if raw := policies[ws.ID]; raw != "" {
			if err := json.Unmarshal([]byte(raw), &pol); err != nil {
				return fmt.Errorf("engine: load policy for %s: %w", ws.ID, err)
			}
		}
		e.policies[ws.ID] = pol
	}
	e.rebuildIndexLocked()

	for _, rec := range triggers {
		tr := rec.Trigger
		e.triggers[tr.ID] = &tr
		if rec.Disposition.Kind != "" {
			e.dispositions[tr.ID] = rec.Disposition
		}
	}
	for _, th := range threads {
		e.threads[th.Key] = &thread{
			key:        th.Key,
			triggerIDs: th.TriggerIDs,
			ticketIDs:  th.TicketIDs,
		}
	}
	// A Decision's ID is content-derived from its Trigger, so Load can rebuild the
	// Trigger↔Decision edge without storing it twice.
	decisionOf := make(map[domain.ID]domain.ID, len(triggers))
	for id := range e.triggers {
		decisionOf[domain.Key(domain.Global, "decision", id)] = id
	}
	for _, d := range decisions {
		// Migration: a pre-DSH row may be a proposal. Approval no longer exists, so
		// it loads as a historical ignore (its proposal/rejectedAt JSON fields are
		// ignored by the struct). No new proposal can be created.
		if d.Kind == domain.DecisionKind("propose") {
			d.Kind = domain.DecIgnore
			d.Reasons = append(d.Reasons, domain.Reason{Code: domain.ReasonLegacyImport})
		}
		e.decisions[d.ID] = d
		// The Trigger a Decision is about: the record itself carries the batch for an
		// Eval's Decisions, and the content-derived key covers the older shape.
		if n := len(d.TriggerIDs); n > 0 {
			e.decisionTrigger[d.ID] = d.TriggerIDs[n-1]
			continue
		}
		if trigID, ok := decisionOf[d.ID]; ok {
			e.decisionTrigger[d.ID] = trigID
		}
	}
	for _, tk := range tickets {
		t := tk
		// Pre-Wave-2 Tickets carried fanned-out text in ContextInput; it becomes
		// ordinary context Inputs so one delivery model serves every Ticket.
		for i, text := range t.ContextInput {
			t.AddInput(domain.Input{
				ID:   domain.Key(domain.Global, "input", t.ID, "legacy", fmt.Sprint(i), text),
				Kind: domain.InputTrigger, Text: text, Context: true,
			})
		}
		t.ContextInput = nil
		e.tickets[t.ID] = &t
		// Rebuild the accepted map: a replayed Trigger must not open a second
		// Ticket after a restart (001:28).
		e.accepted[t.Origin] = t.ID
		for _, in := range t.Inputs {
			if in.TriggerID != "" {
				e.accepted[in.TriggerID] = t.ID
			}
		}
		if t.Number > e.nextNumber {
			e.nextNumber = t.Number
		}
	}
	if err := p.loadEvals(); err != nil {
		return err
	}
	if raw, ok, err := p.st.Get(pausedKV); err == nil && ok && raw == "1" {
		e.paused = true
		e.pauseStarted = p.now()
	}
	for _, k := range keys {
		// A message queued before the restart must not be queued again after it.
		e.sentKeys[k] = true
	}
	for _, rec := range pending {
		e.outbox = append(e.outbox, Outbound{
			Key: rec.Key, Ticket: rec.Ticket, Source: rec.Channel,
			Conv:    domain.Conversation{ID: rec.Conversation},
			ReplyTo: rec.ReplyTo, Text: rec.Text,
		})
	}
	return nil
}

// Register stores a workspace and the policy that goes with it. The policy is what
// gates a Source, so a restart that forgot it would reopen the gate (design §4.5).
func (p *Persistence) Register(w domain.Workspace, policy Policy) error {
	p.e.Register(w, policy)
	raw, err := json.Marshal(policy)
	if err != nil {
		return err
	}
	return p.commit(func() error {
		if err := p.st.PutWorkspace(w); err != nil {
			return err
		}
		return p.st.PutWorkspacePolicy(w.ID, string(raw))
	})
}

// Receive is the durable counterpart of Engine.Receive.
func (p *Persistence) Receive(t domain.Trigger) (Outcome, error) {
	out, err := p.e.Receive(t)
	if err != nil {
		if !errors.Is(err, ErrDuplicate) {
			// The command failed after the engine had already seen the Trigger (an
			// assessor error, for example). Drop the half-applied command.
			p.resync()
		}
		return out, err
	}
	if err := p.commit(func() error { return p.saveOutcome(out) }); err != nil {
		return Outcome{}, err
	}
	return out, nil
}

// Dispatch is the durable counterpart of Engine.Dispatch.
func (p *Persistence) Dispatch(in DispatchInput) (Outcome, error) {
	out, err := p.e.Dispatch(in)
	if err != nil {
		if !errors.Is(err, ErrDuplicate) {
			p.resync()
		}
		return out, err
	}
	if err := p.commit(func() error { return p.saveOutcome(out) }); err != nil {
		return Outcome{}, err
	}
	return out, nil
}

// RaiseAttention records a question on a Ticket and persists the Ticket.
func (p *Persistence) RaiseAttention(ticketID domain.ID, question string, options []string) error {
	if err := p.e.RaiseAttention(ticketID, question, options); err != nil {
		p.resync()
		return err
	}
	return p.SaveTicket(ticketID)
}

// SaveTicket writes a Ticket that a worker changed through the Engine. The Ticket
// is the only aggregate workers mutate, so this is the hook that keeps their
// progress across a restart (design §5.8).
func (p *Persistence) SaveTicket(id domain.ID) error {
	return p.commit(func() error {
		p.e.mu.Lock()
		defer p.e.mu.Unlock()
		tk, ok := p.e.tickets[id]
		if !ok {
			return fmt.Errorf("engine: no ticket %q to save", id)
		}
		return p.st.PutTicket(*tk, p.now())
	})
}

// Queue records one outbound message in the durable Outbox. The message ID is the
// idempotency key; queueing the same message twice delivers it once.
func (p *Persistence) Queue(m outbox.Message) (bool, error) {
	if p.ob == nil {
		return false, errors.New("engine: no outbox is wired")
	}
	return p.ob.Send(m)
}

// Reply queues the engine's in-memory reply and, when an Outbox is wired, records
// it durably under the same content-derived key. It reports whether this call
// queued a new message. The default channel is the local panel; a reply that must
// go out through a Source should use Queue with a real channel instead.
func (p *Persistence) Reply(ticketID domain.ID, text string) (bool, error) {
	key := string(domain.Key(domain.Global, "reply", ticketID, text))
	p.e.mu.Lock()
	before := len(p.e.outbox)
	p.e.mu.Unlock()
	p.e.Reply(ticketID, text) // idempotent in memory
	if p.ob == nil {
		p.e.mu.Lock()
		grew := len(p.e.outbox) > before
		p.e.mu.Unlock()
		return grew, nil // in memory only; the caller has not wired delivery yet
	}
	// The receipt is the durable idempotency check, so Send is what reports whether
	// this reply was new. Calling it again is safe and repairs a reply whose first
	// receipt write failed.
	return p.ob.Send(outbox.Message{
		ID: key, Ticket: ticketID, Source: "local", Conversation: "panel",
		Kind: "reply", Text: text,
	})
}

// commit runs fn as one store transaction. A failed write reloads the memory from
// disk: a command that the disk refused must not look applied in the panel either.
func (p *Persistence) commit(fn func() error) error {
	if err := p.st.Atomic(fn); err != nil {
		p.resync()
		return err
	}
	return nil
}

// resync reloads after a failure. A reload error is deliberately swallowed: the
// caller already has the real error, and panicking on a second failure would lose
// it.
func (p *Persistence) resync() { _ = p.Load() }

// saveOutcome writes everything one command changed. It must be called inside the
// command's transaction. The outcome names the Trigger, so the Decision, the
// Disposition, the Tickets and the Threads it touched can all be found from it as
// the engine's single writer sees them.
func (p *Persistence) saveOutcome(out Outcome) error {
	e := p.e
	e.mu.Lock()
	defer e.mu.Unlock()

	t, ok := e.triggers[out.Trigger]
	if !ok {
		// A decision created without a Trigger is not a shape this engine produces.
		return fmt.Errorf("engine: persist: no trigger %q", out.Trigger)
	}
	if err := p.st.PutTrigger(*t, e.dispositions[out.Trigger]); err != nil {
		return err
	}

	// Persist every Decision this command produced: the disposition names it, so
	// a retry that created a new Eval's Decision is saved too.
	savedDec := map[domain.ID]bool{}
	if out.Decision.ID != "" {
		if dec, ok := e.decisions[out.Decision.ID]; ok {
			if err := p.st.PutDecision(dec); err != nil {
				return err
			}
			savedDec[dec.ID] = true
		}
	}
	if id := e.dispositions[out.Trigger].DecisionID; id != "" && !savedDec[id] {
		if dec, ok := e.decisions[id]; ok {
			if err := p.st.PutDecision(dec); err != nil {
				return err
			}
		}
	}

	// Tickets this command touched: the one it created, every Ticket the
	// Disposition names, and every Ticket subscribed to the Threads the Trigger
	// arrived on (fan-out appends context to those before the gate decides, and a
	// "needs a person" disposition does not list them).
	saved := map[domain.ID]bool{}
	for _, tid := range e.dispositions[out.Trigger].TicketIDs {
		saved[tid] = true
	}
	if out.Ticket != "" {
		saved[out.Ticket] = true
	}

	// Rebuild the fan-out graph on disk: without these rows a restart would not
	// know which Tickets a later message on a subscribed Thread reaches.
	for _, ref := range t.Threads {
		key := domain.ThreadKey(t.Scope, ref.Source, ref.ID)
		th, ok := e.threads[key]
		if !ok {
			continue
		}
		for _, tid := range th.ticketIDs {
			saved[tid] = true
		}
		rec := store.ThreadRecord{
			Key: key, Source: ref.Source, LastAt: t.At,
			TriggerIDs: th.triggerIDs, TicketIDs: th.ticketIDs,
		}
		if wsID, ok := t.Scope.WorkspaceIDOf(); ok {
			rec.Workspace = wsID
		}
		if err := p.st.PutThread(rec); err != nil {
			return err
		}
	}

	for tid := range saved {
		tk, ok := e.tickets[tid]
		if !ok {
			continue
		}
		if err := p.st.PutTicket(*tk, p.now()); err != nil {
			return err
		}
	}

	if err := p.saveEvalsLocked(); err != nil {
		return err
	}

	// A new Ticket's first Assignment is started by a durable effect, so a crash
	// before the executor picked it up does not lose the work.
	if out.Ticket != "" && p.q != nil {
		if tk, ok := e.tickets[out.Ticket]; ok && len(tk.Assignments) > 0 {
			assignment := tk.Assignments[0]
			payload := LaunchPayload{TicketID: tk.ID, AssignmentID: assignment.ID}
			if err := p.q.Enqueue(EffectLaunch, string(assignment.ID), payload); err != nil {
				return err
			}
		}
	}
	return nil
}

// saveEvalsLocked writes the Eval queue while the caller holds e.mu.
func (p *Persistence) saveEvalsLocked() error {
	slots := make([]evalSlot, 0, len(p.e.evals))
	for _, slot := range p.e.evals {
		slots = append(slots, *slot)
	}
	raw, err := json.Marshal(slots)
	if err != nil {
		return err
	}
	return p.st.Set(evalsKV, string(raw))
}

// LaunchHandler is the handler the executor registers for EffectLaunch. It is here
// so a process can wire the engine's launch row without re-deriving the payload
// type and kind name.
func LaunchHandler(run func(ctx context.Context, p LaunchPayload) error) effects.Handler {
	return func(ctx context.Context, key string, payload []byte) error {
		var p LaunchPayload
		if len(payload) > 0 {
			if err := json.Unmarshal(payload, &p); err != nil {
				return fmt.Errorf("engine: launch effect %s: %w", key, err)
			}
		}
		return run(ctx, p)
	}
}

// ---- Wave 2: Eval queue, lifecycle commands and priority launches ----

// EffectLaunchHuman is the durable effect kind that starts an Assignment a
// person is waiting for: it has its own slot budget so new work cannot starve a
// person's request (003:59).
const EffectLaunchHuman = "launch-human"

// evalsKV is the kv key that holds the in-flight Eval queue. Evals are
// short-lived; one JSON value is enough and avoids a schema change while the
// queue is rewritten on every Eval transition.
const evalsKV = "engine.evals"

// pausedKV remembers a processing pause across a restart (003:198).
const pausedKV = "engine.paused"

// saveEvalsTx writes the Eval queue. Callers hold the engine lock and run inside
// the store transaction.
func (p *Persistence) saveEvalsTx() error {
	p.e.mu.Lock()
	defer p.e.mu.Unlock()
	slots := make([]evalSlot, 0, len(p.e.evals))
	for _, slot := range p.e.evals {
		slots = append(slots, *slot)
	}
	raw, err := json.Marshal(slots)
	if err != nil {
		return err
	}
	return p.st.Set(evalsKV, string(raw))
}

// loadEvals rebuilds the Eval queue. A corrupt value is a startup error: the
// queue is the only record of a candidate that has not been decided yet.
func (p *Persistence) loadEvals() error {
	raw, ok, err := p.st.Get(evalsKV)
	if err != nil {
		return err
	}
	p.e.evals = map[string]*evalSlot{}
	if !ok || raw == "" {
		return nil
	}
	var slots []evalSlot
	if err := json.Unmarshal([]byte(raw), &slots); err != nil {
		return fmt.Errorf("engine: load evals: %w", err)
	}
	for i := range slots {
		slot := slots[i]
		// A running Eval from a dead process is queued again; its batch was never
		// decided (design §5.8).
		if slot.Eval.State == domain.EvalRunning {
			slot.Eval.State = domain.EvalQueued
		}
		p.e.evals[slot.Eval.Thread] = &slot
	}
	return nil
}

// saveTicketTx writes one Ticket inside the command's transaction.
func (p *Persistence) saveTicketTx(id domain.ID) error {
	p.e.mu.Lock()
	defer p.e.mu.Unlock()
	tk, ok := p.e.tickets[id]
	if !ok {
		return nil
	}
	return p.st.PutTicket(*tk, p.now())
}

// commitTicket runs one engine command and persists the Ticket it touched, all
// in one store transaction.
func (p *Persistence) commitTicket(id domain.ID, fn func() error) error {
	if err := fn(); err != nil {
		p.resync()
		return err
	}
	return p.commit(func() error {
		if err := p.saveTicketTx(id); err != nil {
			return err
		}
		return p.saveEvalsTx()
	})
}

// EvaluateDue is the durable counterpart of Engine.EvaluateDue: it judges the
// due Evals and writes every Decision, Ticket and queue change in the same
// transaction, then enqueues launch effects for new Tickets.
func (p *Persistence) EvaluateDue(now time.Time) ([]Outcome, error) {
	outs, err := p.e.EvaluateDue(now)
	if err != nil {
		p.resync()
		return outs, err
	}
	if len(outs) == 0 {
		return outs, nil
	}
	touched := map[domain.ID]bool{}
	for _, out := range outs {
		if out.Ticket != "" {
			touched[out.Ticket] = true
		}
	}
	if err := p.commit(func() error {
		p.e.mu.Lock()
		defer p.e.mu.Unlock()
		savedDec := map[domain.ID]bool{}
		for _, out := range outs {
			for _, tid := range out.TriggerIDs {
				if tr, ok := p.e.triggers[tid]; ok {
					if err := p.st.PutTrigger(*tr, p.e.dispositions[tid]); err != nil {
						return err
					}
				}
				// One Eval decides a batch; persist every disposition's Decision so a
				// per-Trigger Decision is not lost.
				if id := p.e.dispositions[tid].DecisionID; id != "" && !savedDec[id] {
					if dec, ok := p.e.decisions[id]; ok {
						if err := p.st.PutDecision(dec); err != nil {
							return err
						}
						savedDec[dec.ID] = true
					}
				}
			}
			if out.Decision.ID != "" && !savedDec[out.Decision.ID] {
				if dec, ok := p.e.decisions[out.Decision.ID]; ok {
					if err := p.st.PutDecision(dec); err != nil {
						return err
					}
					savedDec[dec.ID] = true
				}
			}
		}
		for id := range touched {
			tk, ok := p.e.tickets[id]
			if !ok {
				continue
			}
			if err := p.st.PutTicket(*tk, p.now()); err != nil {
				return err
			}
		}
		if err := p.saveEvalsLocked(); err != nil {
			return err
		}
		for id := range touched {
			tk, ok := p.e.tickets[id]
			if !ok || len(tk.Assignments) == 0 {
				continue
			}
			if err := p.enqueueLaunchTx(*tk, tk.Assignments[0]); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return outs, err
	}
	return outs, nil
}

// enqueueLaunchTx queues the launch effect for one Assignment. A person's
// message uses the human-priority kind so it gets its own slot (003:59).
func (p *Persistence) enqueueLaunchTx(tk domain.Ticket, assignment domain.Assignment) error {
	if p.q == nil {
		return nil
	}
	human := false
	if tr := p.e.triggerOrNil(tk.Origin); tr != nil {
		human = tr.Human || tr.Self
	}
	kind := EffectLaunch
	priority := "normal"
	if human {
		kind = EffectLaunchHuman
		priority = "human"
	}
	payload := LaunchPayload{TicketID: tk.ID, AssignmentID: assignment.ID, Priority: priority}
	return p.q.Enqueue(kind, string(assignment.ID), payload)
}

// AnswerAttention persists the answer and the Ticket that carries it.
func (p *Persistence) AnswerAttention(ticketID domain.ID, answer string) error {
	return p.commitTicket(ticketID, func() error { return p.e.AnswerAttention(ticketID, answer) })
}

// StopTicket persists a manual stop (002:144).
func (p *Persistence) StopTicket(ticketID domain.ID, reason string) error {
	return p.commitTicket(ticketID, func() error { return p.e.StopTicket(ticketID, reason) })
}

// RetryTicket persists a retry (003:137).
func (p *Persistence) RetryTicket(ticketID domain.ID) error {
	return p.commitTicket(ticketID, func() error { return p.e.RetryTicket(ticketID) })
}

// HandBack persists a follow-up handback (002:30).
func (p *Persistence) HandBack(ticketID domain.ID, thread, reason string) error {
	return p.commitTicket(ticketID, func() error { return p.e.HandBack(ticketID, thread, reason) })
}

// RevalidateAndResume persists a resume, and the messages it returns to Eval.
func (p *Persistence) RevalidateAndResume(ticketID domain.ID) error {
	return p.commitTicket(ticketID, func() error { return p.e.RevalidateAndResume(ticketID) })
}

// BindFollow persists a new followed Thread (003:98).
func (p *Persistence) BindFollow(ticketID domain.ID, thread string, own bool) error {
	return p.commitTicket(ticketID, func() error { return p.e.BindFollow(ticketID, thread, own) })
}

// ThreadClosed persists the end of the Tickets a closed Thread finishes.
func (p *Persistence) ThreadClosed(thread, outcome string, at time.Time) error {
	before := map[domain.ID]bool{}
	p.e.mu.Lock()
	for id, t := range p.e.tickets {
		if t.Ended == nil && ticketFollows(t, thread) {
			before[id] = true
		}
	}
	p.e.mu.Unlock()
	if err := p.e.ThreadClosed(thread, outcome, at); err != nil {
		return err
	}
	return p.commit(func() error {
		for id := range before {
			if err := p.saveTicketTx(id); err != nil {
				return err
			}
		}
		return p.saveEvalsTx()
	})
}

// DeliverInput persists one Input queue addition (003:76,112).
func (p *Persistence) DeliverInput(ticketID domain.ID, in domain.Input) error {
	return p.commitTicket(ticketID, func() error { return p.e.DeliverInput(ticketID, in) })
}

// MarkInputsDelivered persists the delivery facts of the three-stage delivery.
func (p *Persistence) MarkInputsDelivered(ticketID domain.ID) error {
	return p.commitTicket(ticketID, func() error { return p.e.MarkInputsDelivered(ticketID) })
}

// MarkInputsRead persists the read facts of the three-stage delivery.
func (p *Persistence) MarkInputsRead(ticketID domain.ID) error {
	return p.commitTicket(ticketID, func() error { return p.e.MarkInputsRead(ticketID) })
}

// MarkTurnEnded persists the turn-end facts of the three-stage delivery.
func (p *Persistence) MarkTurnEnded(ticketID domain.ID) error {
	return p.commitTicket(ticketID, func() error { return p.e.MarkTurnEnded(ticketID) })
}

// DeliveryFailed persists a failed Input delivery (003:11).
func (p *Persistence) DeliveryFailed(ticketID domain.ID, errText string) error {
	return p.commitTicket(ticketID, func() error { return p.e.DeliveryFailed(ticketID, errText) })
}

// Pause persists the processing pause and its start time.
func (p *Persistence) Pause() error {
	p.e.Pause()
	return p.commit(func() error {
		if err := p.st.Set(pausedKV, "1"); err != nil {
			return err
		}
		return p.saveEvalsTx()
	})
}

// Resume persists the end of a processing pause.
func (p *Persistence) Resume() error {
	p.e.Resume()
	return p.commit(func() error {
		if err := p.st.Set(pausedKV, "0"); err != nil {
			return err
		}
		return p.saveEvalsTx()
	})
}

// Paused reports whether processing is paused.
func (p *Persistence) Paused() bool { return p.e.Paused() }

// SetPolicy persists a Policy version change and its effects on live Tickets
// (002:109-117).
func (p *Persistence) SetPolicy(id domain.WorkspaceID, pol Policy) error {
	if err := p.e.SetPolicy(id, pol); err != nil {
		return err
	}
	raw, err := json.Marshal(pol)
	if err != nil {
		return err
	}
	return p.commit(func() error {
		if err := p.st.PutWorkspacePolicy(id, string(raw)); err != nil {
			return err
		}
		return p.saveEvalsTx()
	})
}

// PolicyChanged records a version transition: stale Evals are queued again and
// affected Tickets get a policy-changed Input (002:111-117).
func (p *Persistence) PolicyChanged(from, to string) error {
	if err := p.e.PolicyChanged(from, to); err != nil {
		return err
	}
	return p.commit(func() error {
		p.e.mu.Lock()
		defer p.e.mu.Unlock()
		for id, t := range p.e.tickets {
			if t.Ended == nil && len(t.Inputs) > 0 && t.Inputs[len(t.Inputs)-1].Kind == domain.InputPolicy {
				if err := p.st.PutTicket(*t, p.now()); err != nil {
					return err
				}
				_ = id
			}
		}
		return p.saveEvalsTx()
	})
}

// EvaluateQueueLen reports how many Evals wait.
func (p *Persistence) EvaluateQueueLen() int { return p.e.QueueLen() }

// DropSubscription persists a dropped Subscription (001:37).
func (p *Persistence) DropSubscription(ticketID domain.ID, thread, reason string) error {
	return p.commitTicket(ticketID, func() error { return p.e.DropSubscription(ticketID, thread, reason) })
}

// MarkUnrelated persists an executor's unsubscribe-and-handback (002:30).
func (p *Persistence) MarkUnrelated(ticketID domain.ID, thread, reason string) error {
	return p.commitTicket(ticketID, func() error { return p.e.MarkUnrelated(ticketID, thread, reason) })
}

// InterruptAssignment persists the recovery of a running Assignment left by a
// dead process: it ends it with "hostRestarted" and appends a new pending
// Assignment carrying the Session and the recovery note (003:146, design §5.8).
func (p *Persistence) InterruptAssignment(ticketID domain.ID) error {
	return p.commitTicket(ticketID, func() error { return p.e.InterruptAssignment(ticketID) })
}
