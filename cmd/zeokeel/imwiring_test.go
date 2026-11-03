package main

// IM wiring tests: no credentials means no listener, half credentials is an error
// row, an unsupported receiver surfaces as a source error, a real driver's event
// becomes a Trigger, and the outbox sender uses the live IM driver.

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/zeokeel/super-agent/internal/domain"
	"github.com/zeokeel/super-agent/internal/engine"
	"github.com/zeokeel/super-agent/internal/im"
	"github.com/zeokeel/super-agent/internal/outbox"
	"github.com/zeokeel/super-agent/internal/runtime"
	"github.com/zeokeel/super-agent/internal/view"
)

func newIMRuntime(t *testing.T) *runtime.Runtime {
	t.Helper()
	eng := engine.New(engine.RuleAssessor{})
	eng.Register(domain.Workspace{ID: "ws_web", Name: "web", Root: "/tmp/web", Kind: domain.KindRepo,
		Brief: domain.Brief{Purpose: "webapp", Keywords: []string{"webapp"}}}, engine.Policy{Version: "v1"})
	rt := runtime.New(eng, view.NewSnapshot())
	rt.EnableWorker(runtime.WorkerConfig{Root: t.TempDir()})
	return rt
}

func sourceRows(rt *runtime.Runtime) []view.SourceRow {
	return rt.SourceRows(time.Now())
}

func TestUnconfiguredIMDoesNotListen(t *testing.T) {
	rt := newIMRuntime(t)
	startConfiguredIM(context.Background(), rt, newSourceSender(), imOptions{}, nil)
	if rows := sourceRows(rt); len(rows) != 0 {
		t.Fatalf("an unconfigured build listed sources: %+v", rows)
	}
}

func TestPartialIMConfigIsAnErrorRow(t *testing.T) {
	rt := newIMRuntime(t)
	startConfiguredIM(context.Background(), rt, newSourceSender(), imOptions{FeishuAppID: "cli_x"}, nil)
	rows := sourceRows(rt)
	if len(rows) != 1 {
		t.Fatalf("sources = %+v", rows)
	}
	if rows[0].Enabled || !containsAll(rows[0].LastPoll.Problem, "配置不完整", "app secret") {
		t.Fatalf("half-configured IM must be an error row, got %+v", rows[0])
	}
}

type unsupportedReceiver struct{}

func (unsupportedReceiver) Name() string                  { return "feishu" }
func (unsupportedReceiver) Capabilities() im.Capabilities { return im.Capabilities{} }
func (unsupportedReceiver) Self(context.Context) (im.Identity, error) {
	return im.Identity{ID: "bot"}, nil
}
func (unsupportedReceiver) Members(context.Context, string) ([]im.Member, error) { return nil, nil }
func (unsupportedReceiver) Subscribe(context.Context, chan<- im.RawEvent) error {
	return fmt.Errorf("transport: %w", im.ErrUnsupported)
}

func TestUnsupportedReceiverSurfacesAsSourceError(t *testing.T) {
	rt := newIMRuntime(t)
	go startIMLoop(context.Background(), rt, unsupportedReceiver{}, "飞书", "", nil)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		rows := sourceRows(rt)
		if len(rows) == 1 && !rows[0].Enabled && containsAll(rows[0].LastPoll.Problem, "不支持接收") {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("ErrUnsupported was not surfaced: %+v", sourceRows(rt))
}

func TestIMLoopTurnsEventsIntoTriggers(t *testing.T) {
	rt := newIMRuntime(t)
	tr := im.NewTransport()
	drv := im.DingTalk{T: tr}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go startIMLoop(ctx, rt, drv, "钉钉", "bot", nil)

	// The mock stream hands events to a subscriber; give it a moment to attach.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		tr.Push(im.RawEvent{ID: "m1", Text: "把 webapp 的登录超时改一下",
			Actor: domain.Actor{ID: "colleague"},
			Conv:  domain.Conversation{Kind: domain.ConvDirect, ID: "dm"},
			At:    time.Now().UnixMilli()})
		for _, row := range sourceRows(rt) {
			if row.Enabled && row.LastPoll.Outcome == view.PollOK {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("the IM event never reached the runtime: %+v", sourceRows(rt))
}

type fakeIMSender struct {
	sent []im.Outbound
	gone bool
}

func (f *fakeIMSender) Send(_ context.Context, msg im.Outbound) (im.Receipt, error) {
	f.sent = append(f.sent, msg)
	return im.Receipt{MessageID: "x", Gone: f.gone}, nil
}

// ReadThread is part of im.Sender but not used by the outbox route.
func (f *fakeIMSender) ReadThread(context.Context, domain.Conversation, string) (im.Page, error) {
	return im.Page{Done: true}, nil
}

func TestSenderRoutesThroughLiveIMDriver(t *testing.T) {
	f := &fakeIMSender{}
	s := newSourceSender()
	s.setIM("dingtalk", f)
	if _, err := s.Send(context.Background(), outbox.Message{ID: "key-1", Source: "dingtalk", Conversation: "dm", Text: "hello"}); err != nil {
		t.Fatal(err)
	}
	if len(f.sent) != 1 || f.sent[0].Text != "hello" || f.sent[0].Key != "key-1" {
		t.Fatalf("IM driver did not receive the message: %+v", f.sent)
	}

	gone := &fakeIMSender{gone: true}
	s.setIM("feishu", gone)
	outcome, err := s.Send(context.Background(), outbox.Message{ID: "key-2", Source: "feishu", Conversation: "dm", Text: "x"})
	if err != nil || outcome != outbox.OutcomeGone {
		t.Fatalf("a gone receipt must map to OutcomeGone: %q %v", outcome, err)
	}
}

func containsAll(s string, needles ...string) bool {
	for _, n := range needles {
		if !contains(s, n) {
			return false
		}
	}
	return true
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
