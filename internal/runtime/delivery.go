package runtime

// Delivery of a Ticket's inputs: the three-stage facts the prototype records
// (delivered → read → turnEnded, 003:112) and the failure path that turns a
// broken delivery into person/delivery instead of a silent loss.
//
// Every call goes through Persistence when the app is durable, so the fact and the
// command commit together; memory mode falls back to the bare engine.

import (
	"time"

	"github.com/zeokeel/super-agent/internal/domain"
	"github.com/zeokeel/super-agent/internal/engine"
)

// EvaluateDue advances the two-phase Eval. Receive no longer calls the model, so
// the pass (every 2s) is what produces Decisions, Tickets and launch effects
// (fix-engine API contract §0.1). In memory mode it must also start the new
// Tickets, because no durable launch effect exists.
func (r *Runtime) EvaluateDue(now time.Time) ([]engine.Outcome, error) {
	r.mu.Lock()
	p := r.persist
	r.mu.Unlock()

	var (
		outs []engine.Outcome
		err  error
	)
	if p != nil {
		outs, err = p.EvaluateDue(now)
	} else {
		outs, err = r.eng.EvaluateDue(now)
	}
	if err != nil {
		r.setErr(err.Error())
		r.slogger().Error("eval pass failed", "error", err)
		return nil, err
	}
	for _, out := range outs {
		if out.Ticket != "" && p == nil {
			r.startLocalRun(out.Ticket)
		}
	}
	r.Publish()
	return outs, nil
}

// startAssignment opens the Assignment for this run. There is no Persistence
// wrapper for StartAssignment yet (fix-engine API §1 keeps the engine signature),
// so the durable path pairs it with SaveTicket.
func (r *Runtime) startAssignment(id domain.ID) error {
	r.mu.Lock()
	p := r.persist
	r.mu.Unlock()
	if p != nil {
		if err := p.Engine().StartAssignment(id); err != nil {
			return err
		}
		return p.SaveTicket(id)
	}
	return r.eng.StartAssignment(id)
}

func (r *Runtime) markInputsDelivered(id domain.ID) error {
	r.mu.Lock()
	p := r.persist
	r.mu.Unlock()
	if p != nil {
		return p.MarkInputsDelivered(id)
	}
	return r.eng.MarkInputsDelivered(id)
}

func (r *Runtime) markInputsRead(id domain.ID) error {
	r.mu.Lock()
	p := r.persist
	r.mu.Unlock()
	if p != nil {
		return p.MarkInputsRead(id)
	}
	return r.eng.MarkInputsRead(id)
}

func (r *Runtime) markTurnEnded(id domain.ID) error {
	r.mu.Lock()
	p := r.persist
	r.mu.Unlock()
	if p != nil {
		return p.MarkTurnEnded(id)
	}
	return r.eng.MarkTurnEnded(id)
}

func (r *Runtime) deliveryFailed(id domain.ID, text string) error {
	r.mu.Lock()
	p := r.persist
	r.mu.Unlock()
	if p != nil {
		return p.DeliveryFailed(id, text)
	}
	return r.eng.DeliveryFailed(id, text)
}

// deliverInput hands one Input (fan-out context, policy note, attention answer)
// to a Ticket and wakes its Worker. In durable mode the launch effect is what
// wakes it; in memory mode the runtime starts the run.
func (r *Runtime) deliverInput(id domain.ID, in domain.Input) error {
	r.mu.Lock()
	p := r.persist
	r.mu.Unlock()
	var err error
	if p != nil {
		err = p.DeliverInput(id, in)
	} else {
		err = r.eng.DeliverInput(id, in)
	}
	if err != nil {
		return err
	}
	if p == nil {
		r.startLocalRun(id)
	}
	r.Publish()
	return nil
}
