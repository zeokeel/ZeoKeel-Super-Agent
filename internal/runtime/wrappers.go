package runtime

// The narrow action surface the panel calls (fix-contract §2.2). ui.App detects
// these through optional interfaces, so it never imports this package. Every
// command goes through Persistence when the app is durable, then wakes the Ticket
// if the command left work for an executor.

import (
	"fmt"

	"github.com/zeokeel/super-agent/internal/domain"
	"github.com/zeokeel/super-agent/internal/engine"
)

// AnswerAttention answers the question a blocked Ticket is waiting on. The answer
// becomes a read Input and the Ticket is woken again (001, 003:120).
func (r *Runtime) AnswerAttention(ticketID string, answer string) error {
	id := domain.ID(ticketID)
	r.mu.Lock()
	p := r.persist
	r.mu.Unlock()
	var err error
	if p != nil {
		err = p.AnswerAttention(id, answer)
	} else {
		err = r.eng.AnswerAttention(id, answer)
	}
	if err != nil {
		return fmt.Errorf("runtime: answer attention: %w", err)
	}
	return r.afterCommand(id)
}

// StopTicket stops a Ticket by a person's decision. Its Mandate is withdrawn and
// no further Input reaches it (002:144, 003:130).
func (r *Runtime) StopTicket(ticketID string, reason string) error {
	id := domain.ID(ticketID)
	if reason == "" {
		reason = "stopped in the panel"
	}
	r.mu.Lock()
	p := r.persist
	r.mu.Unlock()
	var err error
	if p != nil {
		err = p.StopTicket(id, reason)
	} else {
		err = r.eng.StopTicket(id, reason)
	}
	if err != nil {
		return fmt.Errorf("runtime: stop ticket: %w", err)
	}
	r.Publish()
	return nil
}

// RetryTicket starts a fresh Assignment when the Ticket can be retried. The
// domain decides retryability (ErrNotRetryable), the runtime only launches it.
func (r *Runtime) RetryTicket(ticketID string) error {
	id := domain.ID(ticketID)
	r.mu.Lock()
	p := r.persist
	r.mu.Unlock()
	var err error
	if p != nil {
		err = p.RetryTicket(id)
	} else {
		err = r.eng.RetryTicket(id)
	}
	if err != nil {
		return fmt.Errorf("runtime: retry ticket: %w", err)
	}
	return r.afterCommand(id)
}

// PauseExecution pauses or resumes automated work: Evals do not time out and
// launches wait (003:198).
func (r *Runtime) PauseExecution(paused bool) error {
	r.mu.Lock()
	p := r.persist
	r.mu.Unlock()
	if p != nil {
		if paused {
			if err := p.Pause(); err != nil {
				return err
			}
		} else if err := p.Resume(); err != nil {
			return err
		}
	} else if paused {
		r.eng.Pause()
	} else {
		r.eng.Resume()
	}
	r.Publish()
	return nil
}

// Paused reports the pause state for the panel.
func (r *Runtime) Paused() bool {
	r.mu.Lock()
	p := r.persist
	r.mu.Unlock()
	if p != nil {
		return p.Paused()
	}
	return r.eng.Paused()
}

// PendingInputs lists the undelivered input texts of a Ticket, for the detail
// page. It is a read: no facts change.
func (r *Runtime) PendingInputs(ticketID string) []string {
	t, ok := r.ticketSnapshot(domain.ID(ticketID))
	if !ok {
		return nil
	}
	inputs := t.UndeliveredInputs()
	out := make([]string, 0, len(inputs))
	for _, in := range inputs {
		out = append(out, in.Text)
	}
	return out
}

// TicketDetail returns a deep copy for the detail page.
func (r *Runtime) TicketDetail(ticketID string) (domain.Ticket, bool) {
	return r.ticketSnapshot(domain.ID(ticketID))
}

// afterCommand wakes the Ticket if the command left it with pending work. A
// command that ended the Ticket needs no wake: the snapshot shows Ended.
func (r *Runtime) afterCommand(id domain.ID) error {
	r.Publish()
	t, ok := r.ticketSnapshot(id)
	if !ok || t.Ended != nil {
		return nil
	}
	return r.wakeTicket(t)
}

// wakeTicket makes sure one executor run is queued for the Ticket, using the same
// human/normal split the engine uses for initial launches (003:59).
func (r *Runtime) wakeTicket(t domain.Ticket) error {
	r.mu.Lock()
	q := r.queue
	r.mu.Unlock()
	as := t.LastAssignment()
	if as.ID == "" {
		return fmt.Errorf("runtime: ticket #%d has no assignment to launch", t.Number)
	}
	if q == nil {
		// Memory mode: no durable row, so run it here.
		r.startLocalRun(t.ID)
		return nil
	}
	human := false
	if tr, ok := r.eng.Trigger(t.Origin); ok {
		human = tr.Human || tr.Self
	}
	kind := engine.EffectLaunch
	priority := "normal"
	if human {
		kind = engine.EffectLaunchHuman
		priority = "human"
	}
	// The assignment id is the effect key: a second wake for the same Assignment
	// coalesces instead of running it twice.
	if pending, err := q.Pending(kind, string(as.ID)); err == nil && pending {
		return nil
	}
	if err := q.Enqueue(kind, string(as.ID), engine.LaunchPayload{
		TicketID: t.ID, AssignmentID: as.ID, Priority: priority,
	}); err != nil {
		return err
	}
	return nil
}
