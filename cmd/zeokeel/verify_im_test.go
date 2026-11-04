package main

// verifier-B: independent IM startup/inbound e2e on the frozen tree. Both
// drivers are the in-memory fakes from internal/im: no network, no credentials.
// The assertion is that a pushed chat event reaches the runtime entry, becomes a
// Trigger, and produces a Ticket (i.e. the whole wiring works, not just "no
// error"), plus that an outbound message goes through the live driver.

import (
	"context"
	"testing"
	"time"

	"github.com/zeokeel/super-agent/internal/domain"
	"github.com/zeokeel/super-agent/internal/engine"
	"github.com/zeokeel/super-agent/internal/im"
	"github.com/zeokeel/super-agent/internal/outbox"
	"github.com/zeokeel/super-agent/internal/runtime"
	"github.com/zeokeel/super-agent/internal/view"
)

func verifyIMRuntime(t *testing.T) (*runtime.Runtime, *engine.Engine) {
	t.Helper()
	eng := engine.New(engine.RuleAssessor{})
	eng.Register(domain.Workspace{ID: "ws_web", Name: "web", Root: t.TempDir(),
		Kind: domain.KindRepo, Brief: domain.Brief{Purpose: "webapp", Keywords: []string{"webapp"}}},
		engine.Policy{
			Version: "v1",
			Rules:   []domain.Rule{{ID: "implementation", Instruction: "改代码", Enabled: true, Proactive: true}},
			Allowed: map[string]map[string]bool{
				"feishu":   {"ou_zhangsan": true},
				"dingtalk": {"ding_zhangsan": true},
			},
			AcceptMode: domain.AcceptAuto,
		})
	rt := runtime.New(eng, view.NewSnapshot())
	rt.EnableWorker(runtime.WorkerConfig{Root: t.TempDir()})
	return rt, eng
}

func verifyIMInbound(t *testing.T, drv im.Provider, tr *im.Transport, actor, conv string) {
	t.Helper()
	rt, eng := verifyIMRuntime(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cfg := runtime.IMConfig{SelfID: "bot-self", FeishuTransport: "longpoll", DingTransport: "stream"}
	opts := runtime.IMManagerOptions{
		PollInterval: 10 * time.Millisecond,
		BuildFeishu:  func(runtime.IMConfig) (im.Provider, error) { return drv, nil },
		BuildDingtalk: func(runtime.IMConfig) (im.Provider, error) {
			return drv, nil
		},
	}
	if drv.Name() == "feishu" {
		cfg.FeishuEnabled = true
		cfg.FeishuAppID, cfg.FeishuAppSecret, cfg.FeishuChats = "cli_x", "sec", "oc_1"
	} else {
		cfg.DingEnabled = true
		cfg.DingAppKey, cfg.DingAppSecret, cfg.DingRobot = "key", "sec", "robot"
	}
	m := runtime.NewIMManager(rt, opts)
	m.Start(ctx, cfg)
	defer m.Stop()

	ev := im.RawEvent{
		ID:       "m1",
		Text:     "把 webapp 的登录超时改一下",
		Actor:    domain.Actor{ID: actor},
		Conv:     domain.Conversation{Kind: domain.ConvDirect, ID: conv},
		At:       time.Now().UnixMilli(),
		Mentions: []string{"bot-self"},
	}
	tr.Push(ev)
	// DSH flow: an admitted explicit IM message reaches the engine, is accepted
	// by Eval and the Ticket is created without an approval step.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, decisions, tickets := eng.Counts(); decisions > 0 && tickets > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	triggers, decisions, tickets := eng.Counts()
	t.Fatalf("inbound IM event did not become a Ticket: triggers=%d decisions=%d tickets=%d sources=%+v",
		triggers, decisions, tickets, rt.SourceRows(time.Now()))
}

func TestVerifyFeishuLongPollInboundBecomesTicket(t *testing.T) {
	tr := im.NewTransport()
	verifyIMInbound(t, im.Feishu{T: tr}, tr, "ou_zhangsan", "oc_chat_1")
}

func TestVerifyDingTalkStreamInboundBecomesTicket(t *testing.T) {
	tr := im.NewTransport()
	verifyIMInbound(t, im.DingTalk{T: tr}, tr, "ding_zhangsan", "dm:ding_zhangsan")
}

// The outbox route uses the live driver for a source (no fake "sent").
func TestVerifyOutboundRoutesThroughLiveIMDriver(t *testing.T) {
	tr := im.NewTransport()
	s := newSourceSender()
	s.setIM("feishu", im.Feishu{T: tr})
	out, err := s.Send(context.Background(), outbox.Message{
		ID: "key-1", Source: "feishu", Conversation: "oc_chat_1", Text: "已完成",
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if out != outbox.OutcomeSent {
		t.Fatalf("outcome=%q", out)
	}
	sent := tr.Sent()
	if len(sent) != 1 || sent[0].Key != "key-1" || sent[0].Text != "已完成" {
		t.Fatalf("live driver did not receive the message: %+v", sent)
	}
}
