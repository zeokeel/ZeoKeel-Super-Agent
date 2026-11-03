package im

import (
	"context"
	"sync"

	"github.com/zeokeel/super-agent/internal/domain"
)

// Transport is the fake network both mock drivers run on: it hands events to a
// subscriber and records what was sent. The point of the two drivers below is to
// prove that Feishu and DingTalk differ only in capabilities and formatting, not
// in workflow.
type Transport struct {
	mu      sync.Mutex
	queue   []RawEvent
	waiters []chan RawEvent
	sent    []Outbound
	gone    map[string]bool // conversation ids that are permanently undeliverable
}

// NewTransport returns an empty fake network.
func NewTransport() *Transport {
	return &Transport{gone: map[string]bool{}}
}

// Push queues an event for the subscriber.
func (t *Transport) Push(ev RawEvent) {
	t.mu.Lock()
	if len(t.waiters) > 0 {
		ch := t.waiters[0]
		t.waiters = t.waiters[1:]
		t.mu.Unlock()
		ch <- ev
		return
	}
	t.queue = append(t.queue, ev)
	t.mu.Unlock()
}

// MarkGone marks a conversation as permanently undeliverable.
func (t *Transport) MarkGone(convID string) {
	t.mu.Lock()
	t.gone[convID] = true
	t.mu.Unlock()
}

// Sent returns every message sent so far.
func (t *Transport) Sent() []Outbound {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]Outbound(nil), t.sent...)
}

func (t *Transport) subscribe(ctx context.Context, out chan<- RawEvent) error {
	for {
		t.mu.Lock()
		if len(t.queue) > 0 {
			ev := t.queue[0]
			t.queue = t.queue[1:]
			t.mu.Unlock()
			select {
			case out <- ev:
			case <-ctx.Done():
				return ctx.Err()
			}
			continue
		}
		ch := make(chan RawEvent, 1)
		t.waiters = append(t.waiters, ch)
		t.mu.Unlock()
		select {
		case ev := <-ch:
			select {
			case out <- ev:
			case <-ctx.Done():
				return ctx.Err()
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (t *Transport) send(msg Outbound) (Receipt, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.gone[msg.Conv.ID] {
		return Receipt{Gone: true}, nil
	}
	t.sent = append(t.sent, msg)
	return Receipt{MessageID: msg.Key}, nil
}

// Feishu is a mock Feishu driver: a long connection, rich cards, files, topics.
type Feishu struct{ T *Transport }

// Name implements Provider.
func (Feishu) Name() string { return "feishu" }

// Capabilities implements Provider.
func (Feishu) Capabilities() Capabilities {
	return Capabilities{Markdown: true, RichCard: true, Files: true, Threads: true,
		Mentions: true, ReadReceipt: true, SupportsEdit: true, MaxTextLen: 30000}
}

// Self implements Provider.
func (Feishu) Self(context.Context) (Identity, error) {
	return Identity{ID: "ou_bot", Name: "ZeoKeel"}, nil
}

// Members implements Provider.
func (Feishu) Members(context.Context, string) ([]Member, error) {
	return []Member{{ID: "ou_zhangsan", Name: "张三"}}, nil
}

// Subscribe implements Receiver.
func (f Feishu) Subscribe(ctx context.Context, out chan<- RawEvent) error {
	return f.T.subscribe(ctx, out)
}

// Send implements Sender.
func (f Feishu) Send(_ context.Context, msg Outbound) (Receipt, error) {
	return f.T.send(msg)
}

// ReadThread implements Sender.
func (Feishu) ReadThread(context.Context, domain.Conversation, string) (Page, error) {
	return Page{Done: true}, nil
}

// DingTalk is a mock DingTalk driver: a stream connection, no topics, plain text
// plus limited markdown.
type DingTalk struct{ T *Transport }

// Name implements Provider.
func (DingTalk) Name() string { return "dingtalk" }

// Capabilities implements Provider.
func (DingTalk) Capabilities() Capabilities {
	return Capabilities{Markdown: true, Files: true, Mentions: true, MaxTextLen: 8000}
}

// Self implements Provider.
func (DingTalk) Self(context.Context) (Identity, error) {
	return Identity{ID: "ding_bot", Name: "ZeoKeel"}, nil
}

// Members implements Provider.
func (DingTalk) Members(context.Context, string) ([]Member, error) {
	return []Member{{ID: "ding_zhangsan", Name: "张三"}}, nil
}

// Subscribe implements Receiver.
func (d DingTalk) Subscribe(ctx context.Context, out chan<- RawEvent) error {
	return d.T.subscribe(ctx, out)
}

// Send implements Sender.
func (d DingTalk) Send(_ context.Context, msg Outbound) (Receipt, error) {
	return d.T.send(msg)
}

// ReadThread implements Sender.
func (DingTalk) ReadThread(context.Context, domain.Conversation, string) (Page, error) {
	return Page{Done: true}, nil
}
