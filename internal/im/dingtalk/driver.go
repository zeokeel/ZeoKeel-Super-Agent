package dingtalk

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/zeokeel/super-agent/internal/domain"
	"github.com/zeokeel/super-agent/internal/im"
)

const (
	// maxTextLen 是钉钉机器人文本的安全上限（官方限制更宽，取保守值）。
	maxTextLen = 8000
	// maxSentKeys 是进程内发送去重缓存的上限。
	maxSentKeys = 256
	// maxOwnIDs 保留字段语义与飞书一致，这里只用于审计（钉钉无引用回复）。
	maxOwnIDs = 256
	// maxMemberPages 是成员分页的硬上限：超过它说明分页没收敛（服务端行为
	// 异常或 token 不前进），只返回部分名单会把白名单悄悄裁掉，所以报错。
	maxMemberPages = 20
	// maxSeenMsgs 是进程内"已交付消息 id"的上限：Stream 重连可能重放最后
	// 一帧，同一条消息不应变成两条 RawEvent。
	maxSeenMsgs = 512
)

// Config 是钉钉驱动配置。AppKey/AppSecret 同时用于两件事：HTTP 传输换
// access_token，以及 Stream 长连接的 clientId/clientSecret（钉钉自建应用的
// 同一套凭据）。只要 New() 拿到它们，接收端就默认走 Stream；只有显式
// NoStream=true（或测试注入自定义 Transport）时才退回"只发不收"。
type Config struct {
	RobotCode string
	BotName   string
	AppKey    string
	AppSecret string
	BaseURL   string

	Client *http.Client
	Retry  im.RetryPolicy
	Now    func() time.Time
	// IdleDelay 传给长轮询循环，避免没有事件时忙等。
	IdleDelay time.Duration

	// NoStream 关闭 Stream 接收（只用 HTTP 发送/成员）。用于没有出网 WebSocket
	// 的部署；此时 Subscribe 会诚实地报 ErrUnsupported，而不是假装在线。
	NoStream bool
	// StreamKeepAlive / StreamBackoff / StreamMaxBackoff 透传给 Stream 接收端。
	StreamKeepAlive                 time.Duration
	StreamBackoff, StreamMaxBackoff time.Duration
	// StreamClient 允许注入 SDK 客户端替身（测试用）；nil 时用官方 SDK。
	StreamClient StreamClient
}

// Driver 实现 im.Provider/Receiver/Poller/Sender 与 im.HealthReporter。
type Driver struct {
	cfg    Config
	tr     Transport
	health *im.HealthTracker

	mu         sync.Mutex
	unreadable int
	sent       map[string]im.Receipt
	sentOrder  []string
	seen       map[string]struct{}
	seenOrder  []string
}

// New 构造真实驱动：发送/成员/令牌走 HTTP，接收默认走 Stream 长连接。
// 凭据不全或显式 NoStream 时不启用 Stream（Subscribe 会诚实报不支持）。
func New(cfg Config) (*Driver, error) {
	if cfg.RobotCode == "" {
		return nil, errors.New("dingtalk: robot code is required")
	}
	if cfg.AppKey == "" || cfg.AppSecret == "" {
		return nil, errors.New("dingtalk: app key and app secret are required")
	}
	httpTr := NewHTTPTransport(cfg.BaseURL, cfg.AppKey, cfg.AppSecret, cfg.Client)
	var tr Transport = httpTr
	if !cfg.NoStream {
		streamTr, err := NewStreamTransport(httpTr, StreamConfig{
			ClientID:     cfg.AppKey,
			ClientSecret: cfg.AppSecret,
			KeepAlive:    cfg.StreamKeepAlive,
			Backoff:      cfg.StreamBackoff,
			MaxBackoff:   cfg.StreamMaxBackoff,
			Client:       cfg.StreamClient,
		})
		if err != nil {
			return nil, err
		}
		tr = streamTr
	}
	d := NewWithTransport(cfg, tr)
	if streamTr, ok := tr.(*StreamTransport); ok {
		streamTr.SetStatus(d.setStreamStatus)
	}
	return d, nil
}

// setStreamStatus 把 Stream 传输的连接状态写到驱动 health（面板"来源"页读它）。
func (d *Driver) setStreamStatus(state im.HealthState, detail string) {
	d.health.Set(im.Health{State: state, Detail: detail, At: d.cfg.Now()})
}

// NewWithTransport 允许注入传输（测试，或真实的 Stream WebSocket 实现）。
func NewWithTransport(cfg Config, tr Transport) *Driver {
	if tr == nil {
		panic("dingtalk: nil transport")
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Driver{cfg: cfg, tr: tr, health: im.NewHealthTracker(),
		sent: map[string]im.Receipt{}, seen: map[string]struct{}{}}
}

// Name 实现 im.Provider。
func (*Driver) Name() string { return "dingtalk" }

// Capabilities 只声明真正实现了的能力：钉钉没有话题（Threads=false），
// 文件发送需要先上传媒体（Files=false），不支持编辑已发消息。
func (*Driver) Capabilities() im.Capabilities {
	return im.Capabilities{
		Markdown:   true, // sampleMarkdown 模板
		RichCard:   false,
		Files:      false,
		Threads:    false,
		Mentions:   true,
		MaxTextLen: maxTextLen,
	}
}

// Self 返回机器人身份：钉钉机器人的身份就是 robotCode，不需要网络调用
// （Stream 回调里才有 chatbotUserId，启动时没有消息可读）。
func (d *Driver) Self(context.Context) (im.Identity, error) {
	if d.cfg.RobotCode == "" {
		return im.Identity{}, errors.New("dingtalk: robot code is empty")
	}
	return im.Identity{ID: d.cfg.RobotCode, Name: d.cfg.BotName}, nil
}

// Members 分页拉取会话成员；传输不支持时返回 im.ErrUnsupported，让上层
// 降级成"面板手工维护白名单"，而不是用空名单把人全关掉。
func (d *Driver) Members(ctx context.Context, convID string) ([]im.Member, error) {
	var out []im.Member
	token := ""
	for page := 0; page < 20; page++ {
		pageData, err := d.tr.ListMembers(ctx, convID, token)
		if err != nil {
			return nil, err
		}
		for _, item := range pageData.Items {
			if item.ID == "" {
				continue // 空 id 会匹配任何空 actor
			}
			out = append(out, im.Member{ID: item.ID, Name: item.Name})
		}
		if !pageData.HasMore || pageData.PageToken == "" || pageData.PageToken == token {
			return out, nil
		}
		token = pageData.PageToken
	}
	return nil, fmt.Errorf("dingtalk: member list exceeded %d pages", maxMemberPages)
}

// Send 发送一条消息。
//
// 幂等：钉钉机器人发送接口没有幂等键字段，所以这里用两层：
//  1. 进程内按业务 key 去重（覆盖同一次运行里的 Outbox 重试）；
//  2. 正文末尾追加不可见 marker（key 的哈希的零宽编码）。跨进程重启后无法
//     阻止重复发送，但 marker 让"这条是重试产生的"在审计/读回时可识别，
//     真正的业务幂等仍由 Outbox 的 key + 回执保证（§7.2）。
func (d *Driver) Send(ctx context.Context, msg im.Outbound) (im.Receipt, error) {
	key := msg.Key
	if key == "" {
		key = im.DeriveKey("dingtalk", msg.Conv.ID, msg.Text)
	}
	if cached, ok := d.sentReceipt(key); ok {
		// 已发过同 key：直接回执，不再打一次接口（钉钉没有服务端去重）。
		return cached, nil
	}
	marker := markerFor(key)
	// 为不可见 marker 预留长度：截断后再拼 marker 会超出接口上限。
	text := im.Truncate(msg.Text, maxTextLen-len([]rune(marker)))

	req := SendRequest{RobotCode: d.cfg.RobotCode, ConversationID: msg.Conv.ID, ReplyTo: msg.ReplyTo}
	if msg.Conv.Kind == domain.ConvDirect {
		req.Direct = true
		req.UserID = directUserID(msg.Conv.ID)
		if req.UserID == "" {
			return im.Receipt{}, errors.New("dingtalk: direct message needs a user id (conversation id dm:<user>)")
		}
	}
	if msg.Markdown {
		// 标题用干净正文：marker 只该出现在消息体里，不该混进标题（虽然不可见，
		// 但会让审计/去重时"标题看起来不同"）。
		req.MsgKey = "sampleMarkdown"
		req.MsgParam = markdownParam(text, marker)
	} else {
		req.MsgKey = "sampleText"
		req.MsgParam = textParam(text + marker)
	}
	result, err := d.tr.SendRobot(ctx, req)
	if err != nil {
		if errors.Is(err, im.ErrGone) {
			return im.Receipt{Gone: true}, nil
		}
		return im.Receipt{}, err
	}
	receipt := im.Receipt{MessageID: result.MessageID}
	d.rememberSent(key, receipt)
	return receipt, nil
}

// ReadThread 在钉钉上不可用：机器人侧没有历史消息 API。返回 ErrUnsupported
// 而不是空页，避免上层把"读不到上下文"误当成"没有上下文"而做出错误判断。
func (*Driver) ReadThread(context.Context, domain.Conversation, string) (im.Page, error) {
	return im.Page{}, fmt.Errorf("%w: dingtalk has no message history API for robots", im.ErrUnsupported)
}

// Poll 取一批 Stream 事件。
func (d *Driver) Poll(ctx context.Context, cursor string) ([]im.RawEvent, string, error) {
	page, err := d.tr.StreamEvents(ctx, cursor)
	if err != nil {
		return nil, cursor, err
	}
	selfID, _ := d.Self(ctx)
	var events []im.RawEvent
	for _, e := range page.Events {
		// 幂等键是消息 id：Stream 重连/游标回退会重放最后一段，重复交付会在
		// 上游变成重复 Trigger（跨进程由内容派生 ID 兜底，这里省掉无谓的扇出）。
		if d.alreadySeen(e.MsgID) {
			continue
		}
		d.rememberSeen(e.MsgID)
		ev, err := d.translate(e, selfID.ID)
		if err != nil {
			d.noteUnreadable()
			continue
		}
		events = append(events, ev)
	}
	next := page.Cursor
	if next == "" {
		next = cursor
	}
	return events, next, nil
}

// Subscribe 实现 im.Receiver：钉钉的 Stream 是长连接，传输层把它抽象成
// "取下一批帧"；换成真实 WebSocket 传输后，这个循环不用改。
//
// 传输层明确报告"没有接收端"（HTTPTransport）时立刻返回错误，而不是进入
// 退避重试直到永远：面板/接线方应该看到"来源不可用（需要 Stream 传输）"，
// 而不是一个永远 connecting 的来源。ErrUnsupported 用 %w 保留，调用方可判别。
func (d *Driver) Subscribe(ctx context.Context, out chan<- im.RawEvent) error {
	if support, ok := d.tr.(StreamSupport); ok && !support.SupportsStream() {
		d.health.Failed(d.cfg.Now(), "dingtalk Stream mode is not configured (NoStream or HTTP-only transport)")
		return fmt.Errorf("dingtalk: %w", im.ErrUnsupported)
	}
	// 订阅退出时关闭 Stream 长连接：SDK 的读循环与自动重连是常驻 goroutine，
	// 不关会泄漏（测试与关闭窗口都会走到这里）。
	if closer, ok := d.tr.(StreamCloser); ok {
		defer func() { _ = closer.Close() }()
	}
	return im.SubscribeLongPoll(ctx, d, im.SubscribeOptions{
		Backoff:    d.cfg.Retry.Base,
		MaxBackoff: 30 * time.Second,
		IdleDelay:  d.cfg.IdleDelay,
		Health:     d.health,
		Now:        d.cfg.Now,
	}, out)
}

// Health 实现 im.HealthReporter。
func (d *Driver) Health(context.Context) im.Health { return d.health.Snapshot() }

// Unreadable 返回被跳过的坏消息数。
func (d *Driver) Unreadable() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.unreadable
}

func (d *Driver) noteUnreadable() {
	d.mu.Lock()
	d.unreadable++
	d.mu.Unlock()
}

func (d *Driver) sentReceipt(key string) (im.Receipt, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	receipt, ok := d.sent[key]
	return receipt, ok
}

func (d *Driver) rememberSent(key string, receipt im.Receipt) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.sent[key]; ok {
		return
	}
	d.sent[key] = receipt
	d.sentOrder = append(d.sentOrder, key)
	if len(d.sentOrder) > maxSentKeys {
		oldest := d.sentOrder[0]
		d.sentOrder = d.sentOrder[1:]
		delete(d.sent, oldest)
	}
}

// directUserID 从会话 id 里取单聊对方 userId。约定 "dm:<userId>"；也接受
// 裸 userId（有些接线方直接用工号当会话 id）。
func directUserID(convID string) string {
	if strings.HasPrefix(convID, "dm:") {
		return strings.TrimPrefix(convID, "dm:")
	}
	return convID
}

// textParam / markdownParam 生成钉钉机器人模板参数。
//
// 用 encoding/json 而不是手拼字符串：正文可能含任意控制字符（\b、\f、0x00
// 等），手工转义只覆盖 5 个字符，漏一个就产出非法 JSON 被钉钉拒收（见 audit
// §5 的 quote() 条目）。json.Marshal 的 map 键序稳定（编码后按键排序），
// 测试可以直接解析，不需要断言字符串字面量。
func textParam(text string) string {
	encoded, _ := json.Marshal(map[string]string{"content": text})
	return string(encoded)
}

func markdownParam(text, marker string) string {
	title := "Super Agent"
	if lines := strings.SplitN(strings.TrimSpace(text), "\n", 2); lines[0] != "" {
		title = im.Truncate(strings.TrimSpace(lines[0]), 40)
	}
	encoded, _ := json.Marshal(map[string]string{"title": title, "text": text + marker})
	return string(encoded)
}

// rememberSeen/alreadySeen 是进程内"已交付消息 id"的有界集合。
func (d *Driver) alreadySeen(id string) bool {
	if id == "" {
		return false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	_, ok := d.seen[id]
	return ok
}

func (d *Driver) rememberSeen(id string) {
	if id == "" {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.seen[id]; ok {
		return
	}
	d.seen[id] = struct{}{}
	d.seenOrder = append(d.seenOrder, id)
	if len(d.seenOrder) > maxSeenMsgs {
		oldest := d.seenOrder[0]
		d.seenOrder = d.seenOrder[1:]
		delete(d.seen, oldest)
	}
}

// markerStart 是 marker 的不可见起始符（U+2063 INVISIBLE SEPARATOR），
// 后面跟 64 位零宽编码：U+200B 表示 0、U+200C 表示 1。
const markerStart = '\u2063'

// markerFor 生成确定性的不可见后缀。测试可据此断言"同一 key 同一标记、
// 不同 key 不同标记、标记里没有任何可打印字符"。
func markerFor(key string) string {
	sum := sha256.Sum256([]byte(key))
	var b strings.Builder
	b.WriteRune(markerStart)
	for i := 0; i < 8; i++ {
		for bit := 7; bit >= 0; bit-- {
			if sum[i]>>uint(bit)&1 == 1 {
				b.WriteRune('\u200c')
			} else {
				b.WriteRune('\u200b')
			}
		}
	}
	return b.String()
}
