package im

import (
	"context"
	"testing"
	"time"

	"github.com/zeokeel/super-agent/internal/domain"
)

// Two different tools, one interface: the same code path receives and sends for
// both, which is the whole point of design §4.8.
func TestTwoDriversShareOneInterface(t *testing.T) {
	tf, td := NewTransport(), NewTransport()
	fs := Driver{Provider: Feishu{T: tf}, Receiver: Feishu{T: tf}, Sender: Feishu{T: tf}}
	dt := Driver{Provider: DingTalk{T: td}, Receiver: DingTalk{T: td}, Sender: DingTalk{T: td}}

	if fs.Name() == dt.Name() {
		t.Fatal("drivers must have distinct names")
	}
	// Capabilities differ, and the Router adapts to them.
	cf, cd := EffectiveCapabilities(fs), EffectiveCapabilities(dt)
	if !cf.RichCard || cd.RichCard {
		t.Fatalf("expected Feishu cards and no DingTalk cards, got %+v / %+v", cf, cd)
	}
	if !cf.Threads || cd.Threads {
		t.Fatalf("expected Feishu topics and no DingTalk topics")
	}
	if cd.MaxTextLen == 0 {
		t.Fatal("text limit must be filled in")
	}

	// Receive through the same interface.
	for _, tc := range []struct {
		name string
		d    Driver
		tr   *Transport
	}{{"feishu", fs, tf}, {"dingtalk", dt, td}} {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		out := make(chan RawEvent, 1)
		go func() { _ = tc.d.Receiver.Subscribe(ctx, out) }()
		time.Sleep(20 * time.Millisecond)
		tc.tr.Push(RawEvent{ID: "m1", Text: "hi", Conv: domain.Conversation{Kind: domain.ConvDirect, ID: "c1"}})
		select {
		case ev := <-out:
			if ev.ID != "m1" {
				t.Fatalf("%s: got %q", tc.name, ev.ID)
			}
		case <-time.After(time.Second):
			t.Fatalf("%s: no event", tc.name)
		}
		cancel()
		tc.d.Sender.Send(context.Background(), Outbound{Key: "r1", Conv: domain.Conversation{ID: "c1"}, Text: "收"})
		if n := len(tc.tr.Sent()); n != 1 {
			t.Fatalf("%s: expected one sent message, got %d", tc.name, n)
		}
	}
}

// A send to a deleted conversation reports Gone so the Outbox stops retrying.
func TestSendReportsGone(t *testing.T) {
	tf := NewTransport()
	tf.MarkGone("c9")
	r, err := Feishu{T: tf}.Send(context.Background(), Outbound{Key: "k", Conv: domain.Conversation{ID: "c9"}})
	if err != nil {
		t.Fatal(err)
	}
	if !r.Gone {
		t.Fatal("expected Gone for a deleted conversation")
	}
}
