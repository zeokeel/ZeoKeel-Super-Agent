package dingtalk

// DingTalk Stream 接收端。钉钉机器人没有可拉取的历史消息 HTTP 接口，官方方案
// 是 Stream 模式：机器人主动连出去的 WebSocket 长连接（桌面在 NAT 后面也能收）。
// 本文件把官方 SDK（github.com/open-dingtalk/dingtalk-stream-sdk-go）接到既有
// 的 Transport 契约上：SDK 回调 → dingtalk.Event → driver.translate → RawEvent。
//
// 为什么需要一层适配而不是直接用 SDK：
//   - 驱动的其余部分（发送/成员/健康/白名单）仍走 HTTP Transport，Stream 只补
//     "接收"这一半；
//   - SDK 是推模式回调，驱动的 Poller 是拉模式（StreamEvents），适配层负责把
//     回调推进有界队列、按批交给 Poll；
//   - 测试必须能在没有网络/凭据的情况下驱动整条链路，所以 SDK 客户端是一个
//     可注入的接口（StreamClient），生产实现是官方 SDK 的薄封装。
//
// 秘密处理：ClientID/ClientSecret 只在构造 SDK 时使用，不写日志、不进 Event、
// 不进错误文本；本文件不记录 SessionWebhook（它带 token）。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/open-dingtalk/dingtalk-stream-sdk-go/chatbot"
	"github.com/open-dingtalk/dingtalk-stream-sdk-go/client"

	"github.com/zeokeel/super-agent/internal/im"
)

const (
	// streamEventBuffer 是回调 → Poller 的有界队列。满时回调阻塞（背压），
	// 而不是丢消息；阻塞会让 SDK 的 ack 变慢，钉钉重投的重复由 driver 按
	// MsgID 去重吸收（不静默丢）。
	streamEventBuffer = 256
	// streamBatchMax 是一次 StreamEvents 最多交出的条数：让长轮询循环有机会
	// 推进健康状态/让出时间片，而不是一次吞下无限消息。
	streamBatchMax = 50
	// maxStreamContentBytes 是单条回调 content 的上限：贴纸/富文本可能带大
	// base64，超过它的消息无法安全交给模型，计数丢弃而不是撑爆内存。
	maxStreamContentBytes = 1 << 20
	// maxStreamIDs 是消息/会话 id 的长度上限（协议是短 id，超长说明负载可疑）。
	maxStreamIDs = 256
)

// StreamConfig 是 Stream 接收端的配置。ClientID/ClientSecret 就是钉钉自建应用
// 的 AppKey/AppSecret（SDK 的命名）。
type StreamConfig struct {
	ClientID     string
	ClientSecret string
	// KeepAlive 是 SDK 的连接保活间隔（默认 SDK 的 120s；测试可调小）。
	KeepAlive time.Duration
	// Backoff/MaxBackoff 是"建立连接失败"时的重试退避；连接建立后的断线重连
	// 由 SDK 自己负责（WithAutoReconnect）。
	Backoff, MaxBackoff time.Duration
	// Client 允许注入一个 SDK 客户端替身（测试用）；nil 时用官方 SDK。
	Client StreamClient
}

func (c StreamConfig) withDefaults() StreamConfig {
	if c.Backoff <= 0 {
		c.Backoff = time.Second
	}
	if c.MaxBackoff <= 0 {
		c.MaxBackoff = 30 * time.Second
	}
	if c.MaxBackoff < c.Backoff {
		c.MaxBackoff = c.Backoff
	}
	return c
}

// StreamClient 是官方 SDK 客户端里本包用到的最小面。变成接口是为了测试：
// 用假客户端喂回调、制造连接失败，不需要真实网络或凭据。
type StreamClient interface {
	// RegisterChatBotCallbackRouter 注册机器人消息回调（官方 SDK 同名方法）。
	RegisterChatBotCallbackRouter(handler func(ctx context.Context, data *chatbot.BotCallbackDataModel) ([]byte, error))
	// Start 建立长连接；官方 SDK 在连接成功后立刻返回，内部另起读循环。
	Start(ctx context.Context) error
	// Close 关闭连接（可重复调用）。
	Close()
}

// StreamCloser 是可选能力：驱动在 Subscribe 退出时关闭传输（释放 SDK 连接）。
type StreamCloser interface {
	Close() error
}

// sdkStreamClient 是官方 SDK 的薄封装。
type sdkStreamClient struct {
	cli *client.StreamClient
}

// NewSDKStreamClient 构造真实的 Stream 客户端（生产路径）。
func NewSDKStreamClient(cfg StreamConfig) (StreamClient, error) {
	if strings.TrimSpace(cfg.ClientID) == "" || strings.TrimSpace(cfg.ClientSecret) == "" {
		return nil, errors.New("dingtalk: stream needs a client id and client secret")
	}
	opts := []client.ClientOption{
		client.WithAppCredential(client.NewAppCredentialConfig(cfg.ClientID, cfg.ClientSecret)),
		client.WithAutoReconnect(true),
	}
	if cfg.KeepAlive > 0 {
		opts = append(opts, client.WithKeepAlive(cfg.KeepAlive))
	}
	return &sdkStreamClient{cli: client.NewStreamClient(opts...)}, nil
}

// RegisterChatBotCallbackRouter 实现 StreamClient。
func (c *sdkStreamClient) RegisterChatBotCallbackRouter(handler func(ctx context.Context, data *chatbot.BotCallbackDataModel) ([]byte, error)) {
	c.cli.RegisterChatBotCallbackRouter(handler)
}

// Start 实现 StreamClient。
func (c *sdkStreamClient) Start(ctx context.Context) error { return c.cli.Start(ctx) }

// Close 实现 StreamClient。
func (c *sdkStreamClient) Close() { c.cli.Close() }

// StreamTransport 是"接收走 Stream、发送/成员走 HTTP"的传输实现。
//
// 它嵌入 *HTTPTransport：AccessToken/SendRobot/ListMembers 等方法原样继承，
// 只覆盖 StreamEvents/SupportsStream。这样驱动持有的 Transport 契约不变。
type StreamTransport struct {
	*HTTPTransport

	cfg     StreamConfig
	factory func(StreamConfig) (StreamClient, error)

	events chan Event
	errs   chan error
	done   chan struct{}

	startOnce sync.Once
	closeOnce sync.Once

	mu      sync.Mutex
	client  StreamClient
	lastErr error
	drops   int
	closed  bool
	status  func(state im.HealthState, detail string)
}

// NewStreamTransport 构造真实的 Stream 传输。httpTr 提供发送/成员/令牌能力。
func NewStreamTransport(httpTr *HTTPTransport, cfg StreamConfig) (*StreamTransport, error) {
	if httpTr == nil {
		return nil, errors.New("dingtalk: stream transport needs an HTTP transport")
	}
	cfg = cfg.withDefaults()
	if strings.TrimSpace(cfg.ClientID) == "" {
		cfg.ClientID = httpTr.AppKey
	}
	if strings.TrimSpace(cfg.ClientSecret) == "" {
		cfg.ClientSecret = httpTr.AppSecret
	}
	if cfg.Client == nil && (cfg.ClientID == "" || cfg.ClientSecret == "") {
		return nil, errors.New("dingtalk: stream needs a client id and client secret")
	}
	tr := newStreamTransport(httpTr, cfg, nil)
	if cfg.Client != nil {
		tr.factory = func(StreamConfig) (StreamClient, error) { return cfg.Client, nil }
	}
	return tr, nil
}

// NewStreamTransportWithClient 用注入的客户端构造传输（测试用）。
func NewStreamTransportWithClient(httpTr *HTTPTransport, cfg StreamConfig, cli StreamClient) *StreamTransport {
	if cli != nil {
		cfg.Client = cli
	}
	cfg = cfg.withDefaults()
	tr := newStreamTransport(httpTr, cfg, cli)
	return tr
}

func newStreamTransport(httpTr *HTTPTransport, cfg StreamConfig, cli StreamClient) *StreamTransport {
	tr := &StreamTransport{
		HTTPTransport: httpTr,
		cfg:           cfg,
		events:        make(chan Event, streamEventBuffer),
		errs:          make(chan error, 1),
		done:          make(chan struct{}),
	}
	if cli != nil {
		tr.factory = func(StreamConfig) (StreamClient, error) { return cli, nil }
	} else {
		tr.factory = NewSDKStreamClient
	}
	return tr
}

// SupportsStream 实现 StreamSupport：有 Stream 传输才能接收。
func (*StreamTransport) SupportsStream() bool { return true }

// SetStatus 注入健康状态回调（driver 用来把连接状态写到面板的 health 行）。
func (s *StreamTransport) SetStatus(fn func(state im.HealthState, detail string)) {
	s.mu.Lock()
	s.status = fn
	s.mu.Unlock()
}

// Dropped 返回因负载非法/超限被丢弃的回调数（用于测试与审计）。
func (s *StreamTransport) Dropped() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.drops
}

// Close 关闭 Stream 连接与后台循环。可重复调用。
func (s *StreamTransport) Close() error {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		cli := s.client
		s.mu.Unlock()
		close(s.done)
		if cli != nil {
			cli.Close()
		}
	})
	return nil
}

// StreamEvents 实现 Transport：阻塞等待下一批回调，按批返回。
//
// 语义与 Poller 契约一致：没有消息时一直等（长连接本来就是推模式），ctx 取消
// 立刻返回；连接建立失败时返回错误，让 SubscribeLongPoll 退避重试并在连续
// 失败后把 health 标红（不伪装成"没有新消息"）。
func (s *StreamTransport) StreamEvents(ctx context.Context, _ string) (EventsPage, error) {
	s.start()
	select {
	case ev := <-s.events:
		batch := make([]Event, 0, streamBatchMax)
		batch = append(batch, ev)
		for len(batch) < streamBatchMax {
			select {
			case next := <-s.events:
				batch = append(batch, next)
			default:
				return EventsPage{Events: batch}, nil
			}
		}
		return EventsPage{Events: batch}, nil
	case err := <-s.errs:
		return EventsPage{}, fmt.Errorf("dingtalk stream: %w", err)
	case <-ctx.Done():
		return EventsPage{}, ctx.Err()
	case <-s.done:
		return EventsPage{}, errors.New("dingtalk: stream transport closed")
	}
}

func (s *StreamTransport) start() {
	s.startOnce.Do(func() {
		go s.run()
	})
}

func (s *StreamTransport) run() {
	backoff := s.cfg.Backoff
	for {
		select {
		case <-s.done:
			return
		default:
		}
		cli, err := s.factory(s.cfg)
		if err != nil {
			s.reportError(err)
			if !s.sleep(backoff) {
				return
			}
			backoff = nextBackoff(backoff, s.cfg.MaxBackoff)
			continue
		}
		cli.RegisterChatBotCallbackRouter(s.handleCallback)
		if err := cli.Start(context.Background()); err != nil {
			cli.Close()
			s.reportError(err)
			if !s.sleep(backoff) {
				return
			}
			backoff = nextBackoff(backoff, s.cfg.MaxBackoff)
			continue
		}
		// Start 成功：官方 SDK 在内部读循环里处理消息与自动重连。这里只等关闭。
		// 成功会覆盖之前建连失败的错误：清掉 errs 里尚未被消费的那条，避免
		// 轮询循环在连接已经可用时仍读到旧错误。
		select {
		case <-s.errs:
		default:
		}
		s.mu.Lock()
		s.client = cli
		s.lastErr = nil
		status := s.status
		s.mu.Unlock()
		if status != nil {
			status(im.HealthStarting, "stream connected; waiting for messages")
		}
		<-s.done
		cli.Close()
		return
	}
}

func (s *StreamTransport) handleCallback(_ context.Context, data *chatbot.BotCallbackDataModel) ([]byte, error) {
	ev, err := messageFromCallback(data)
	if err != nil {
		// 坏回调要 ack：让钉钉停止重投，同时计数让面板能解释"少了一条"。
		s.mu.Lock()
		s.drops++
		s.mu.Unlock()
		return []byte(""), nil
	}
	select {
	case s.events <- ev:
	case <-s.done:
	}
	// 空 ack 表示"收到"；真正的回执由 Outbox 的发送路径负责，不在回调里回复。
	return []byte(""), nil
}

func (s *StreamTransport) reportError(err error) {
	if err == nil {
		return
	}
	s.mu.Lock()
	s.lastErr = err
	status := s.status
	s.mu.Unlock()
	if status != nil {
		status(im.HealthError, "stream connect failed: "+err.Error())
	}
	// 只保留最新错误：StreamEvents 每次只消费一条，旧错误已经被退避覆盖。
	select {
	case <-s.errs:
	default:
	}
	select {
	case s.errs <- err:
	default:
	}
}

func (s *StreamTransport) sleep(d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-s.done:
		return false
	}
}

func nextBackoff(current, max time.Duration) time.Duration {
	if current <= 0 {
		current = time.Second
	}
	next := current * 2
	if next > max {
		return max
	}
	return next
}

// messageFromCallback 把官方 SDK 的机器人回调拍平成驱动层的 Event。
//
// 只做"事实翻译"：不判断是否 @ 了机器人（mapping.resolveMentions 负责），
// 不判断消息类型是否支持（mapping.renderContent 负责，未知类型会标 Unsupported）。
func messageFromCallback(data *chatbot.BotCallbackDataModel) (Event, error) {
	if data == nil {
		return Event{}, errors.New("dingtalk: nil stream callback")
	}
	msgID := strings.TrimSpace(data.MsgId)
	convID := strings.TrimSpace(data.ConversationId)
	if msgID == "" {
		return Event{}, errors.New("dingtalk: stream callback without msg id")
	}
	if convID == "" {
		return Event{}, errors.New("dingtalk: stream callback without conversation id")
	}
	if len(msgID) > maxStreamIDs || len(convID) > maxStreamIDs {
		return Event{}, errors.New("dingtalk: stream callback id is too long")
	}
	if data.CreateAt <= 0 {
		return Event{}, errors.New("dingtalk: stream callback without create time")
	}
	msgType := strings.ToLower(strings.TrimSpace(data.Msgtype))
	content, err := callbackContent(msgType, data)
	if err != nil {
		return Event{}, err
	}
	atUsers := make([]AtUser, 0, len(data.AtUsers))
	for _, u := range data.AtUsers {
		atUsers = append(atUsers, AtUser{
			DingTalkID: strings.TrimSpace(u.DingtalkId),
			StaffID:    strings.TrimSpace(u.StaffId),
		})
	}
	return Event{
		MsgID:             msgID,
		MsgType:           msgType,
		Content:           content,
		SenderStaffID:     strings.TrimSpace(data.SenderStaffId),
		SenderID:          strings.TrimSpace(data.SenderId),
		SenderNick:        data.SenderNick,
		ConversationID:    convID,
		ConversationType:  strings.TrimSpace(data.ConversationType),
		ConversationTitle: data.ConversationTitle,
		ChatbotUserID:     data.ChatbotUserId,
		// IsReplyMsg：官方回调模型没有这个字段（没有父消息 id），因此 Stream
		// 路径无法把"回复机器人"当作 addressed 的事实；@ 与单聊仍然可靠。
		CreateAt:   data.CreateAt,
		IsInAtList: data.IsInAtList,
		AtUsers:    atUsers,
	}, nil
}

// callbackContent 把 SDK 的 content 统一成驱动 mapping 期望的 JSON 字符串。
//
// 文本消息的正文在 Text.Content；其它类型（picture/file/audio/video/richText/
// markdown）在 Content（interface{}），原样 JSON 化即可——mapping.renderContent
// 按 msgtype 解析同一套键。
func callbackContent(msgType string, data *chatbot.BotCallbackDataModel) (string, error) {
	if msgType == "text" {
		return marshalContent(map[string]string{"content": data.Text.Content})
	}
	if data.Content == nil {
		// 没有 content 的类型（例如仅通知）交给 mapping 标 Unsupported。
		return "", nil
	}
	return marshalContent(data.Content)
}

func marshalContent(v any) (string, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("dingtalk: unusable stream content: %w", err)
	}
	if len(raw) > maxStreamContentBytes {
		return "", fmt.Errorf("dingtalk: stream content is %d bytes (limit %d)", len(raw), maxStreamContentBytes)
	}
	return string(raw), nil
}
