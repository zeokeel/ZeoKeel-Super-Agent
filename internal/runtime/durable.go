package runtime

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/zeokeel/super-agent/internal/effects"
	"github.com/zeokeel/super-agent/internal/engine"
	"github.com/zeokeel/super-agent/internal/outbox"
)

// EnableDurable turns on the durable path: domain writes are committed to
// SQLite, and starting a Ticket becomes a durable `launch` effect instead of a
// goroutine. That is what makes a crash recoverable rather than merely unlucky
// (design §7.2): the row survives, and the queue runs it on the next start.
func (r *Runtime) EnableDurable(p *engine.Persistence, q *effects.Queue) error {
	if p == nil || q == nil {
		return fmt.Errorf("runtime: durable mode needs both a persistence and a queue")
	}
	r.mu.Lock()
	r.persist, r.queue = p, q
	r.mu.Unlock()

	// The executor is the launch handler. It runs off the queue's goroutine, so a
	// slow model never blocks a command. Two kinds give a person's message its own
	// slot (003:59): normal work cannot starve a follow-up answer.
	launch := engine.LaunchHandler(func(ctx context.Context, payload engine.LaunchPayload) error {
		t, ok := p.Engine().TicketSnapshot(payload.TicketID)
		if !ok {
			return nil // the Ticket is gone: nothing to launch
		}
		if t.Ended != nil {
			return nil
		}
		// The whole multi-turn run happens here; the effect row stays until it is
		// done, so a crash mid-run is retried on the next start.
		return r.runTicket(t)
	})
	kinds := []struct {
		kind  string
		limit func() int
	}{
		{engine.EffectLaunch, r.concurrency},
		{engine.EffectLaunchHuman, r.concurrencyHuman},
	}
	for _, k := range kinds {
		if err := q.Handle(k.kind, launch, effects.Options{
			// Read the limit every time instead of once: editing 并发执行器 in the
			// panel must take effect without a restart (design §6.5).
			Limit: k.limit,
			// A delivery failure retries 2s→1min (003:11); a model failure reports
			// the Ticket failed instead of hammering the endpoint.
			Retry: [2]time.Duration{2 * time.Second, time.Minute},
		}); err != nil {
			return err
		}
	}
	return nil
}

// Persistence is the durable wrapper the command uses instead of bare engine
// calls. It is nil when the app runs in memory (the demo and the tests).
func (r *Runtime) Persistence() *engine.Persistence {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.persist
}

// Queue returns the effect queue, or nil in memory mode. The CLI's status/check
// and the supervisor need it; nothing else should reach through the runtime.
func (r *Runtime) Queue() *effects.Queue {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.queue
}

// Outbox returns the delivery queue, or nil.
func (r *Runtime) OutboxQ() *outbox.Outbox {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.ob
}

// SetOutbox records the outbox so the panel can show pending deliveries.
func (r *Runtime) SetOutbox(ob *outbox.Outbox) {
	r.mu.Lock()
	r.ob = ob
	r.mu.Unlock()
}

// concurrencyHuman is the extra slot a person's own message gets: it may run even
// when every normal executor is busy, because a live person is waiting (003:59).
func (r *Runtime) concurrencyHuman() int { return 1 }

// concurrency is the executor budget: how many Tickets may run at once. It reads
// the saved setting on each call, falls back to 2, and never returns 0 (a zero
// limit would park every queue forever).
func (r *Runtime) concurrency() int {
	r.mu.Lock()
	raw := r.settings["execution.concurrency"]
	r.mu.Unlock()
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n < 1 {
		return 2
	}
	if n > 8 {
		// More workers than this on a desktop is a foot-gun, not a feature.
		return 8
	}
	return n
}

// commandTimeout is the per-command limit the panel configures, in seconds.
func (r *Runtime) commandTimeout() time.Duration {
	r.mu.Lock()
	raw := r.settings["execution.command_timeout"]
	r.mu.Unlock()
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n < 5 {
		return 0 // let the agent use its own default
	}
	if n > 3600 {
		n = 3600
	}
	return time.Duration(n) * time.Second
}

// PollInterval is how often a Source should ask for new events, from settings.
func (r *Runtime) PollInterval() time.Duration { return r.pollInterval() }

// pollInterval is how often a long-polling Source asks for new events.
func (r *Runtime) pollInterval() time.Duration {
	r.mu.Lock()
	raw := r.settings["source.poll_interval"]
	r.mu.Unlock()
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n < 5 {
		return 60 * time.Second
	}
	if n > 600 {
		n = 600
	}
	return time.Duration(n) * time.Second
}

// modelTimeout is the per-request model limit from the panel, in seconds. Large
// models on slow links need minutes, so this is a setting, not a constant.
func (r *Runtime) modelTimeout() time.Duration {
	r.mu.Lock()
	raw := r.settings["execution.model_timeout"]
	r.mu.Unlock()
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n < 10 {
		return 0 // agent default
	}
	if n > 3600 {
		n = 3600
	}
	return time.Duration(n) * time.Second
}
