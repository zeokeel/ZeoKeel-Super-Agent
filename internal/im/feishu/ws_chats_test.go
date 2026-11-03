package feishu

import (
	"context"
	"testing"
	"time"

	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"

	"github.com/zeokeel/super-agent/internal/im"
)

func TestWSChatAllowed(t *testing.T) {
	d := &Driver{}
	if !d.wsChatAllowed("oc_any") {
		t.Fatal("empty chats must listen to every chat the app can see")
	}
	d = &Driver{cfg: Config{Chats: []Chat{{ID: "oc_1"}}}}
	if !d.wsChatAllowed("oc_1") {
		t.Fatal("configured chat rejected")
	}
	if d.wsChatAllowed("oc_2") {
		t.Fatal("a chat outside the filter was allowed")
	}
}

// A WS event from a chat outside the configured list must not become a RawEvent.
func TestWSSubscribeFiltersChats(t *testing.T) {
	ev := p2Direct("om_1", "oc_other", "hello", nil)
	conn := &fakeWSConnector{events: []*larkim.P2MessageReceiveV1{ev}, started: make(chan struct{})}
	cfg := Config{
		AppID: "app", AppSecret: "secret", Transport: TransportWS,
		Chats:              []Chat{{ID: "oc_only"}},
		WSConnectorFactory: func(Config) (WSConnector, error) { return conn, nil },
	}
	d := NewWithTransport(cfg, NewHTTPTransport(cfg.BaseURL, cfg.AppID, cfg.AppSecret, nil))
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	out := make(chan im.RawEvent, 4)
	go func() { _ = d.Subscribe(ctx, out) }()
	<-conn.started
	select {
	case ev := <-out:
		t.Fatalf("out-of-scope chat delivered %+v", ev)
	case <-time.After(150 * time.Millisecond):
	}
}
