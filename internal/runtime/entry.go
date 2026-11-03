package runtime

import (
	"github.com/zeokeel/super-agent/internal/domain"
	"github.com/zeokeel/super-agent/internal/engine"
)

// This file is the runtime's single entry point for Triggers. Every Source
// (GitHub poller, schedule loop, IM driver) and the panel go through it, so a
// durable install cannot have a path that writes memory and skips the database
// (the GitHub/schedule loops used to call the bare engine and lose their work on a
// crash — AUDIT §0.2-7).

// ReceiveTrigger takes one Trigger through the durable wrapper when the app is in
// durable mode, and through the in-memory engine otherwise. The durable path is
// what makes "kill -9 then restart" true: the Trigger row and its launch effect
// commit together, and the next start picks the effect up.
func (r *Runtime) ReceiveTrigger(t domain.Trigger) (engine.Outcome, error) {
	t = withTriggerThread(t)
	r.mu.Lock()
	p := r.persist
	r.mu.Unlock()

	var (
		out engine.Outcome
		err error
	)
	if p != nil {
		out, err = p.Receive(t)
	} else {
		out, err = r.eng.Receive(t)
	}
	if err != nil {
		r.setErr(err.Error())
		return out, err
	}
	r.setErr("")
	// Receive now only queues the Eval (fix-engine API §0.1): advance it at once so
	// a Source's message becomes a Ticket in this pass, not two seconds later.
	if _, evalErr := r.EvaluateDue(r.clock()); evalErr != nil {
		return out, evalErr
	}
	if out.Ticket != "" && p == nil {
		// Memory mode: nothing will launch the executor, so the runtime does.
		// Durable mode leaves it to the launch effect (runtime/durable.go).
		r.startLocalRun(out.Ticket)
	}
	r.Publish()
	return out, nil
}

// startLocalRun starts one Ticket's Worker in the background when there is no
// durable queue to do it.
func (r *Runtime) startLocalRun(id domain.ID) {
	t, ok := r.ticketSnapshot(id)
	if !ok {
		return
	}
	go func() {
		if err := r.runTicket(t); err != nil {
			r.slogger().Warn("launch will be retried", "ticket", t.Number, "error", err)
		}
	}()
}

// ticketSnapshot returns a copy of a Ticket taken under the engine lock.
//
// Engine.Ticket currently returns the engine's own pointer (engine.go:372-377),
// which races with Report/StartAssignment on worker goroutines. Reading through
// AllTickets copies under the lock, which is the same invariant
// Engine.TicketSnapshot will expose (fix-engine, Wave2). Switch to that method
// when it lands; until then this helper is the safe read path (fix-docs-ci race
// report: runtime/disk_test.go:72-78, runtime/faults_test.go:81).
func (r *Runtime) ticketSnapshot(id domain.ID) (domain.Ticket, bool) {
	return r.eng.TicketSnapshot(id)
}

// ticketSnapshots returns every Ticket as a DEEP copy taken under the engine
// lock. Engine.AllTickets is a shallow struct copy: its Assignments slice shares
// the Report pointers, so reading LastReport on it races with a concurrent Report
// (found by -race in the stream projection). TicketSnapshot deep-copies.
func (r *Runtime) ticketSnapshots() []domain.Ticket {
	all := r.eng.AllTickets()
	out := make([]domain.Ticket, 0, len(all))
	for _, t := range all {
		if deep, ok := r.eng.TicketSnapshot(t.ID); ok {
			out = append(out, deep)
		}
	}
	return out
}

// withTriggerThread guarantees the Trigger names a Thread. The two-phase Eval keys
// its batch by Thread (001:29,43); a Trigger without one would be marked
// "evaluating" and never become due. Sources normally set Threads, but a
// hand-built Trigger (tests, a new Source) must not silently disappear.
func withTriggerThread(t domain.Trigger) domain.Trigger {
	if len(t.Threads) > 0 {
		return t
	}
	id := t.Conversation.ID
	if id == "" {
		id = t.Channel.Thread
	}
	if id == "" {
		id = string(t.ID)
	}
	source := t.Source
	if source == "" {
		source = "local"
	}
	t.Threads = []domain.ThreadRef{{Source: source, ID: id}}
	return t
}

// questionFrom returns the question a just-evaluated batch asked a person, if
// any. That is the Router refusing to guess which workspace owns the work.
func questionFrom(outs []engine.Outcome) string {
	for _, o := range outs {
		if o.NeedsPerson && o.Question != "" {
			return o.Question
		}
	}
	return ""
}
