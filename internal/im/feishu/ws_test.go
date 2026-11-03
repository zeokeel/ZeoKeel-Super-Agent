package feishu

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"

	"github.com/zeokeel/super-agent/internal/domain"
	"github.com/zeokeel/super-agent/internal/im"
)

func strp(s string) *string { return &s }

// fakeWSConnector 模拟官方 larkws.Client：按顺序投递事件，然后阻塞到 ctx 取消。
type fakeWSConnector struct {
	events  []*larkim.P2MessageReceiveV1
	started chan struct{}
	once    sync.Once
}

func (f *fakeWSConnector) Run(ctx context.Context, handler func(*larkim.P2MessageReceiveV1) error) error {
	f.once.Do(func() { close(f.started) })
	for _, ev := range f.events {
		if err := handler(ev); err != nil {
			return err
		}
	}
	<-ctx.Done()
	return ctx.Err()
}

func p2Direct(messageID, chatID, text string, mentions []*larkim.MentionEvent) *larkim.P2MessageReceiveV1 {
	return &larkim.P2MessageReceiveV1{
		Event: &larkim.P2MessageReceiveV1Data{
			Sender: &larkim.EventSender{
				SenderId:   &larkim.UserId{OpenId: strp("ou_user")},
				SenderType: strp("user"),
			},
			Message: &larkim.EventMessage{
				MessageId:   strp(messageID),
				ChatId:      strp(chatID),
				ChatType:    strp("p2p"),
				MessageType: strp("text"),
				Content:     strp(`{"text":"` + text + `"}`),
				CreateTime:  strp("1700000000000"),
				Mentions:    mentions,
			},
		},
	}
}

func newWSDriver(t *testing.T, factory WSConnectorFactory) *Driver {
	t.Helper()
	cfg := Config{
		AppID: "app", AppSecret: "secret", Transport: TransportWS,
		SelfID: "ou_bot", SelfName: "ZeoKeel",
		WSConnectorFactory: factory,
		Now:                time.Now,
	}
	return NewWithTransport(cfg, &fakeTransport{})
}

// 官方 P2MessageReceiveV1 → RawEvent：单聊、正文、@ 事实。
func TestWSEventConversion(t *testing.T) {
	d := newWSDriver(t, func(Config) (WSConnector, error) { return &fakeWSConnector{}, nil })
	ev := p2Direct("om_1", "oc_p2p", "hello @_user_1 帮我看下", []*larkim.MentionEvent{{
		Key: strp("@_user_1"), Id: &larkim.UserId{OpenId: strp("ou_bot")}, Name: strp("ZeoKeel"),
	}})
	raw, ok := d.wsEventToRaw(ev, "ou_bot")
	if !ok {
		t.Fatal("event was ignored")
	}
	if raw.ID != "om_1" || raw.Conv.ID != "oc_p2p" || raw.Conv.Kind != domain.ConvDirect {
		t.Fatalf("raw = %+v", raw)
	}
	if raw.Actor.ID != "ou_user" || raw.Actor.Bot {
		t.Fatalf("actor = %+v", raw.Actor)
	}
	if !strings.Contains(raw.Text, "hello") || strings.Contains(raw.Text, "@_user_1") {
		t.Fatalf("text = %q (mention placeholder should be removed)", raw.Text)
	}
	if len(raw.Mentions) != 1 || raw.Mentions[0] != "ou_bot" {
		t.Fatalf("mentions = %v", raw.Mentions)
	}
	if raw.At != 1_700_000_000_000 {
		t.Fatalf("at = %d", raw.At)
	}
}

// bot 与自己发的消息必须被忽略（否则会自触发/回复机器人）。
func TestWSIgnoresBotAndSelf(t *testing.T) {
	d := newWSDriver(t, func(Config) (WSConnector, error) { return &fakeWSConnector{}, nil })

	bot := p2Direct("om_bot", "oc", "bot says", nil)
	bot.Event.Sender.SenderType = strp("bot")
	if _, ok := d.wsEventToRaw(bot, "ou_bot"); ok {
		t.Fatal("bot message was not ignored")
	}
	self := p2Direct("om_self", "oc", "self says", nil)
	self.Event.Sender.SenderId.OpenId = strp("ou_bot")
	if _, ok := d.wsEventToRaw(self, "ou_bot"); ok {
		t.Fatal("self message was not ignored")
	}
}

// long connection 订阅：投递、按 message_id 去重、ctx 取消优雅停止。
func TestWSSubscribeDeliversDedupesAndStops(t *testing.T) {
	events := []*larkim.P2MessageReceiveV1{
		p2Direct("om_1", "oc_1", "first", nil),
		p2Direct("om_1", "oc_1", "first", nil), // 重放
		p2Direct("om_2", "oc_2", "second", nil),
	}
	conn := &fakeWSConnector{events: events, started: make(chan struct{})}
	d := newWSDriver(t, func(Config) (WSConnector, error) { return conn, nil })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out := make(chan im.RawEvent, 8)
	done := make(chan error, 1)
	go func() { done <- d.Subscribe(ctx, out) }()

	got := []string{}
	for len(got) < 2 {
		select {
		case raw := <-out:
			got = append(got, raw.ID)
		case <-time.After(3 * time.Second):
			t.Fatalf("got events %v, want 2", got)
		}
	}
	if got[0] != "om_1" || got[1] != "om_2" {
		t.Fatalf("delivered = %v (dedupe failed)", got)
	}
	select {
	case extra := <-out:
		t.Fatalf("duplicate delivered: %+v", extra)
	case <-time.After(50 * time.Millisecond):
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Subscribe err = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Subscribe did not stop after cancel")
	}
}

// transport 选项只发布真正实现的：longpoll + ws。
func TestFeishuTransportModes(t *testing.T) {
	modes := TransportModes()
	if len(modes) != 2 {
		t.Fatalf("modes = %v", modes)
	}
	for _, want := range []string{TransportLongPoll, TransportWS} {
		found := false
		for _, mode := range modes {
			if mode == want {
				found = true
			}
		}
		if !found || !SupportsTransport(want) {
			t.Fatalf("mode %q missing/unsupported (modes=%v)", want, modes)
		}
	}
	if SupportsTransport("webhook") || SupportsTransport("nonsense") {
		t.Fatal("unimplemented transport must not be advertised")
	}
}

// ws 模式缺凭据时诚实拒绝构造，而不是空转到运行时。
func TestNewWSRequiresCredentials(t *testing.T) {
	if _, err := New(Config{Transport: TransportWS}); err == nil {
		t.Fatal("New(ws) without credentials should fail")
	}
	if _, err := New(Config{Transport: "webhook", AppID: "a", AppSecret: "s"}); err == nil {
		t.Fatal("unknown transport should fail")
	}
}

// 生命周期：Driver.Close 取消在跑的 WS 订阅（runtime Stop 不泄漏连接）。
func TestWSDriverCloseStopsSubscription(t *testing.T) {
	conn := &fakeWSConnector{started: make(chan struct{})}
	d := newWSDriver(t, func(Config) (WSConnector, error) { return conn, nil })
	done := make(chan error, 1)
	go func() { done <- d.Subscribe(context.Background(), make(chan im.RawEvent, 1)) }()
	select {
	case <-conn.started:
	case <-time.After(2 * time.Second):
		t.Fatal("WS connector did not start")
	}
	if err := d.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Subscribe err = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not stop the WS subscription")
	}
	if err := d.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}
