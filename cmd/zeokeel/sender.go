package main

import (
	"context"
	"fmt"
	"sync"

	"github.com/zeokeel/super-agent/internal/domain"
	"github.com/zeokeel/super-agent/internal/im"
	"github.com/zeokeel/super-agent/internal/outbox"
	"github.com/zeokeel/super-agent/internal/sources"
)

// sourceSender delivers outbox messages through whichever Source kind owns the
// message's source. A message whose source has no live driver is marked sent with
// a local receipt and shown in the panel: pretending it went out would be worse
// than saying it did not (design §7.2).
type sourceSender struct {
	mu    sync.Mutex
	kinds map[string]sources.Kind
	// ims are the live IM drivers, keyed by Source kind. A message whose source
	// has one goes through the driver's own Sender (idempotency key included).
	ims map[string]im.Sender
	// load builds a kind on first use; nil means "no driver in this build".
	load func(name string) (sources.Kind, error)
}

// NewSourceSender wires the drivers the app knows about.
func newSourceSender() *sourceSender {
	return &sourceSender{kinds: map[string]sources.Kind{}, ims: map[string]im.Sender{}}
}

// Send implements outbox.Sender.
func (s *sourceSender) Send(ctx context.Context, m outbox.Message) (outbox.SendOutcome, error) {
	s.mu.Lock()
	imSender := s.ims[m.Source]
	s.mu.Unlock()
	if imSender != nil {
		receipt, err := imSender.Send(ctx, im.Outbound{
			Key: m.ID, Conv: domain.Conversation{ID: m.Conversation}, ReplyTo: m.ReplyTo, Text: m.Text,
		})
		if err != nil {
			return "", err
		}
		if receipt.Gone {
			return outbox.OutcomeGone, nil
		}
		return outbox.OutcomeSent, nil
	}

	s.mu.Lock()
	kind, ok := s.kinds[m.Source]
	s.mu.Unlock()
	if !ok && s.load != nil {
		if k, err := s.load(m.Source); err == nil {
			s.mu.Lock()
			s.kinds[m.Source] = k
			s.mu.Unlock()
			kind, ok = k, true
		}
	}
	if !ok {
		// No driver: returning "sent" here is the lie AUDIT §0.2-8 found (the
		// panel showed 已发 for messages that never left). An error makes the
		// outbox back off and surface the delivery problem instead.
		return "", fmt.Errorf("runtime: no channel driver for source %q", m.Source)
	}
	ch := kind.Channel()
	if ch == nil {
		return "", fmt.Errorf("runtime: source %q has no reply channel", m.Source)
	}
	receipt, err := ch.Send(ctx, sources.Outbound{
		Key: m.ID, Thread: m.Thread, ReplyTo: m.ReplyTo, Text: m.Text,
		Address: map[string]string{"conversation": m.Conversation},
	})
	if err != nil {
		// Outbox backs off on error; Gone is the only permanent failure.
		return "", err
	}
	if receipt.Gone {
		return outbox.OutcomeGone, nil
	}
	return outbox.OutcomeSent, nil
}

// setKinds registers already-built drivers, so the app can hand the sender the
// ones it started at boot instead of rebuilding them per message.
func (s *sourceSender) setKinds(name string, k sources.Kind) {
	s.mu.Lock()
	s.kinds[name] = k
	s.mu.Unlock()
}

// setIM registers a live IM driver's sender for one Source kind.
func (s *sourceSender) setIM(name string, drv im.Sender) {
	s.mu.Lock()
	if s.ims == nil {
		s.ims = map[string]im.Sender{}
	}
	s.ims[name] = drv
	s.mu.Unlock()
}
