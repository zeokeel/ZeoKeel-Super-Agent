package feishu

// 飞书官方长连接（WebSocket）接收：larksuite/oapi-sdk-go/v3 的 larkws.Client。
//
// 这是飞书官方推荐的接收机制（比 HTTP 长轮询更低延迟、无需公网回调地址）。
// 契约（SDK v3.12.2）：
//
//	dispatcher := event.NewEventDispatcher("", "").
//	    OnP2MessageReceiveV1(func(ctx, event *larkim.P2MessageReceiveV1) error {...})
//	client := larkws.NewClient(appID, appSecret, larkws.WithEventHandler(dispatcher))
//	err := client.Start(ctx)   // 阻塞；ctx 取消即优雅停止，SDK 自带重连
//
// 我们只依赖这两个公开入口，其余（事件→RawEvent 翻译、去重、bot/self 过滤、
// health 回调）都在本文件；测试通过 WSConnectorFactory 注入 fake，不碰网络。

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	larkdispatcher "github.com/larksuite/oapi-sdk-go/v3/event/dispatcher"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	larkws "github.com/larksuite/oapi-sdk-go/v3/ws"

	"github.com/zeokeel/super-agent/internal/im"
)

// WSConnector 是官方 larkws.Client 的最小替身接口：Run 阻塞消费事件，直到
// ctx 取消或连接终止；handler 可能被并发调用（实现须自行同步）。
type WSConnector interface {
	Run(ctx context.Context, handler func(*larkim.P2MessageReceiveV1) error) error
}

// WSConnectorFactory 构造一个连接器；测试注入 fake，生产用 newLarkWSConnector。
type WSConnectorFactory func(cfg Config) (WSConnector, error)

type larkWSConnector struct {
	client *larkws.Client

	mu      sync.Mutex
	handler func(*larkim.P2MessageReceiveV1) error
}

func (c *larkWSConnector) Run(ctx context.Context, handler func(*larkim.P2MessageReceiveV1) error) error {
	c.mu.Lock()
	c.handler = handler
	c.mu.Unlock()
	return c.client.Start(ctx)
}

func (c *larkWSConnector) deliver(ctx context.Context, event *larkim.P2MessageReceiveV1) error {
	c.mu.Lock()
	handler := c.handler
	c.mu.Unlock()
	if handler == nil {
		return nil
	}
	return handler(event)
}

// newLarkWSConnector 构造真实 SDK 客户端。
func newLarkWSConnector(cfg Config) (WSConnector, error) {
	if strings.TrimSpace(cfg.AppID) == "" || strings.TrimSpace(cfg.AppSecret) == "" {
		return nil, errors.New("feishu: ws transport requires app id and app secret")
	}
	conn := &larkWSConnector{}
	dispatcher := larkdispatcher.NewEventDispatcher("", "").
		OnP2MessageReceiveV1(func(ctx context.Context, event *larkim.P2MessageReceiveV1) error {
			return conn.deliver(ctx, event)
		})
	conn.client = larkws.NewClient(cfg.AppID, cfg.AppSecret,
		larkws.WithEventHandler(dispatcher),
	)
	return conn, nil
}

// wsMaxSeen 是 WS 重连后事件可能重放的去重窗口。
const wsMaxSeen = 1024

// subscribeWS 是 Config.Transport=="ws" 时的接收循环。
func (d *Driver) subscribeWS(ctx context.Context, out chan<- im.RawEvent) error {
	factory := d.cfg.WSConnectorFactory
	if factory == nil {
		factory = newLarkWSConnector
	}
	conn, err := factory(d.cfg)
	if err != nil {
		d.health.Set(im.Health{State: im.HealthError, Detail: "飞书长连接初始化失败：" + err.Error(), At: d.cfg.Now()})
		return err
	}
	selfID, _ := d.selfID(ctx)
	d.health.Set(im.Health{State: im.HealthStarting, Detail: "飞书长连接建立中", At: d.cfg.Now()})

	var mu sync.Mutex
	seen := map[string]bool{}
	order := make([]string, 0, wsMaxSeen)
	deliver := func(event *larkim.P2MessageReceiveV1) error {
		raw, ok := d.wsEventToRaw(event, selfID)
		if !ok {
			return nil
		}
		mu.Lock()
		if seen[raw.ID] {
			mu.Unlock()
			return nil
		}
		seen[raw.ID] = true
		order = append(order, raw.ID)
		if len(order) > wsMaxSeen {
			delete(seen, order[0])
			order = order[1:]
		}
		mu.Unlock()
		select {
		case out <- raw:
			d.health.Set(im.Health{State: im.HealthReady, Detail: "飞书长连接已就绪", At: d.cfg.Now()})
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	err = conn.Run(ctx, deliver)
	if ctxErr := ctx.Err(); ctxErr != nil {
		d.health.Set(im.Health{State: im.HealthPaused, Detail: "飞书长连接已停止", At: d.cfg.Now()})
		return ctxErr
	}
	if err == nil {
		err = errors.New("feishu: websocket long connection ended")
	}
	d.health.Set(im.Health{State: im.HealthError, Detail: "飞书长连接断开：" + err.Error(), At: d.cfg.Now()})
	return err
}

// wsEventToRaw 把 P2MessageReceiveV1 翻译成驱动层 RawEvent。
// 事实规则：忽略 bot/自己发的消息；群/单聊由 chat_type 决定；@ 列表沿用
// renderContent 的 Mention 归一（@全体 open_id 为空会被剔除）。
func (d *Driver) wsEventToRaw(event *larkim.P2MessageReceiveV1, selfID string) (im.RawEvent, bool) {
	if event == nil || event.Event == nil || event.Event.Message == nil {
		return im.RawEvent{}, false
	}
	msg := event.Event.Message
	sender := event.Event.Sender
	if msg.MessageId == nil || strings.TrimSpace(*msg.MessageId) == "" {
		return im.RawEvent{}, false
	}
	chatID := derefString(msg.ChatId)
	if chatID == "" {
		return im.RawEvent{}, false
	}
	chatType := derefString(msg.ChatType)
	if sender == nil {
		return im.RawEvent{}, false
	}
	if sender.SenderType != nil && strings.EqualFold(*sender.SenderType, "bot") {
		return im.RawEvent{}, false // 不把自己的/其它机器人消息变成 Trigger
	}
	var senderOpenID, senderUserID string
	if sender.SenderId != nil {
		senderOpenID = derefString(sender.SenderId.OpenId)
		senderUserID = derefString(sender.SenderId.UserId)
	}
	if selfID != "" && senderOpenID == selfID {
		return im.RawEvent{}, false // 忽略自己：否则自己发的消息会自触发
	}
	senderID := senderOpenID
	if senderID == "" {
		senderID = senderUserID
	}
	if senderID == "" {
		return im.RawEvent{}, false
	}
	createMillis, _ := parseMillis(derefString(msg.CreateTime))
	if createMillis == 0 {
		return im.RawEvent{}, false
	}
	mentions := make([]Mention, 0, len(msg.Mentions))
	for _, mention := range msg.Mentions {
		if mention == nil {
			continue
		}
		m := Mention{Key: derefString(mention.Key), Name: derefString(mention.Name)}
		if mention.Id != nil {
			m.OpenID = derefString(mention.Id.OpenId)
		}
		mentions = append(mentions, m)
	}
	message := Message{
		MessageID:    derefString(msg.MessageId),
		ChatID:       chatID,
		ChatType:     chatType,
		MsgType:      derefString(msg.MessageType),
		Content:      derefString(msg.Content),
		Mentions:     mentions,
		SenderID:     senderID,
		SenderType:   derefString(sender.SenderType),
		ParentID:     derefString(msg.ParentId),
		RootID:       derefString(msg.RootId),
		CreateMillis: createMillis,
	}
	chat := Chat{ID: chatID, Kind: conversationKind(chatType, "")}
	raw, err := d.translate(message, chat, selfID)
	if err != nil {
		return im.RawEvent{}, false
	}
	return raw, true
}

func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func parseMillis(s string) (int64, error) {
	if s == "" {
		return 0, fmt.Errorf("empty timestamp")
	}
	var n int64
	if _, err := fmt.Sscanf(s, "%d", &n); err != nil {
		return 0, err
	}
	if n > 0 && n < 1e12 {
		n *= 1000 // 秒级时间戳
	}
	return n, nil
}
