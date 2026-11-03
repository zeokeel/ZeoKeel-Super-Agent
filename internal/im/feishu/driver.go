package feishu

import (
	"context"
	"encoding/base64"
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
	// maxTextLen 是飞书单条消息的安全上限（官方限制比它更宽，这里取一个
	// 不会触发"消息过长"拒收的值；Capabilities 也用它，Router 据此裁剪）。
	maxTextLen = 30000
	// maxOwnIDs 是"自己发出的消息 id"缓存上限，用于判定 ReplyToBot。
	// 只保留最近若干条：判定父消息是不是自己的，历史太久远的回复本来也
	// 不会成为新 Trigger。
	maxOwnIDs = 512
	// defaultBackfill 是首次轮询的回看窗口。新装实例绝不能从头重放历史
	//（那会把几年前的群聊全收一遍），所以默认只看最近一天。
	defaultBackfill = 24 * time.Hour
	// maxMemberPages 是成员分页的硬上限：超过它说明分页没收敛（服务端行为
	// 异常或 token 不前进）。只返回部分名单会把白名单悄悄裁掉，所以报错。
	maxMemberPages = 20
)

// Chat 是一个要监听的会话。飞书没有"机器人全部会话"的拉取 API，监听范围
// 必须显式配置：这既是能力限制，也是权限最小化（不听的群不该被读）。
type Chat struct {
	ID   string
	Name string
	// Kind 是会话类型；历史接口不一定返回 chat_type，因此由配置声明。
	// 零值按 group 处理。
	Kind domain.ConversationKind
}

// Config 是飞书驱动配置。凭据为空时 New 会拒绝：真实驱动不该在没有任何
// 身份的情况下悄悄跑起来，那只会让面板显示一个永远连不上的来源。
type Config struct {
	AppID     string
	AppSecret string
	BaseURL   string
	// Chats 是要监听的会话；空表示不监听任何会话（轮询返回空）。
	Chats []Chat
	// SelfID/SelfName 是无 bot/v3/info 权限时的回退身份（@ 判定需要它）。
	SelfID   string
	SelfName string

	Client *http.Client
	Retry  im.RetryPolicy
	Now    func() time.Time
	// InitialBackfill 是首轮回看窗口，默认 24h。
	InitialBackfill time.Duration
	// IdleDelay 传给长轮询循环：没有新消息时先等一会儿，避免忙等。
	IdleDelay time.Duration
	// PageSize 是每次拉取的页大小。
	PageSize int
}

// Driver 实现 im.Provider/Receiver/Poller/Sender 与 im.HealthReporter。
type Driver struct {
	cfg    Config
	tr     Transport
	health *im.HealthTracker

	mu         sync.Mutex
	self       im.Identity
	hasSelf    bool
	own        map[string]struct{}
	ownOrder   []string
	unreadable int
}

// New 用真实 HTTP 传输构造驱动。
func New(cfg Config) (*Driver, error) {
	if cfg.AppID == "" || cfg.AppSecret == "" {
		return nil, errors.New("feishu: app id and app secret are required")
	}
	// 会话列表是"监听范围"的声明：空 id 会让轮询去请求一个空会话，重复 id
	// 会让两个配置项共用同一份游标（后写的覆盖前面的），都必须在构造时拒绝。
	seen := make(map[string]bool, len(cfg.Chats))
	for _, chat := range cfg.Chats {
		if strings.TrimSpace(chat.ID) == "" {
			return nil, errors.New("feishu: chat id is empty")
		}
		if seen[chat.ID] {
			return nil, fmt.Errorf("feishu: duplicate chat id %q", chat.ID)
		}
		seen[chat.ID] = true
	}
	return NewWithTransport(cfg, NewHTTPTransport(cfg.BaseURL, cfg.AppID, cfg.AppSecret, cfg.Client)), nil
}

// NewWithTransport 允许注入传输（测试、或换成 WebSocket 长连接实现）。
func NewWithTransport(cfg Config, tr Transport) *Driver {
	if tr == nil {
		panic("feishu: nil transport")
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.InitialBackfill <= 0 {
		cfg.InitialBackfill = defaultBackfill
	}
	if cfg.PageSize <= 0 {
		cfg.PageSize = 50
	}
	return &Driver{cfg: cfg, tr: tr, health: im.NewHealthTracker(), own: map[string]struct{}{}}
}

// Name 实现 im.Provider。
func (*Driver) Name() string { return "feishu" }

// Capabilities 只声明本驱动真正实现了的能力：编辑/已读/文件发送尚未实现，
// 标 false 让 Router 不要发一条"以为能编辑"的消息。
func (*Driver) Capabilities() im.Capabilities {
	return im.Capabilities{
		Markdown:   true, // 以 interactive 卡片发 markdown
		RichCard:   true,
		Files:      false,
		Threads:    true, // 话题群 + reply 端点
		Mentions:   true,
		MaxTextLen: maxTextLen,
	}
}

// Self 返回机器人身份：优先用配置回退，其次问开放平台并缓存。
func (d *Driver) Self(ctx context.Context) (im.Identity, error) {
	if d.cfg.SelfID != "" {
		return im.Identity{ID: d.cfg.SelfID, Name: d.cfg.SelfName}, nil
	}
	d.mu.Lock()
	if d.hasSelf {
		self := d.self
		d.mu.Unlock()
		return self, nil
	}
	d.mu.Unlock()
	bot, err := d.tr.BotInfo(ctx)
	if err != nil {
		return im.Identity{}, err
	}
	d.mu.Lock()
	d.self, d.hasSelf = im.Identity{ID: bot.OpenID, Name: bot.Name}, true
	d.mu.Unlock()
	return im.Identity{ID: bot.OpenID, Name: bot.Name}, nil
}

// Members 分页拉取会话成员，用于白名单同步。
func (d *Driver) Members(ctx context.Context, chatID string) ([]im.Member, error) {
	var out []im.Member
	token := ""
	for page := 0; page < maxMemberPages; page++ {
		pageData, err := d.tr.ListMembers(ctx, chatID, token)
		if err != nil {
			return nil, err
		}
		for _, item := range pageData.Items {
			if item.ID == "" {
				// 空 id 会匹配任何空 actor：白名单里绝不能出现它。
				continue
			}
			out = append(out, im.Member{ID: item.ID, Name: item.Name})
		}
		if !pageData.HasMore || pageData.PageToken == "" || pageData.PageToken == token {
			return out, nil
		}
		token = pageData.PageToken
	}
	return nil, fmt.Errorf("feishu: member list exceeded %d pages", maxMemberPages)
}

// Send 发送一条消息。幂等交给飞书的 uuid：同一条 Outbox 记录重试时 uuid 不变，
// 服务端据此只产生一条消息。Gone 由传输层的错误码归一后转成回执。
func (d *Driver) Send(ctx context.Context, msg im.Outbound) (im.Receipt, error) {
	if msg.Conv.ID == "" && msg.ReplyTo == "" {
		return im.Receipt{}, errors.New("feishu: message has no conversation")
	}
	text := im.Truncate(msg.Text, maxTextLen)
	req := SendRequest{ChatID: msg.Conv.ID, ReplyTo: msg.ReplyTo, UUID: msg.Key}
	if req.UUID == "" {
		// 没有业务 key 时用内容派生：崩溃重放仍可算出同一个值。
		req.UUID = im.DeriveKey("feishu", msg.Conv.ID, msg.ReplyTo, text)
	}
	if msg.Markdown {
		req.MsgType = "interactive"
		req.Content = markdownCard(text)
	} else {
		req.MsgType = "text"
		req.Content = textBody(text)
	}
	result, err := d.tr.SendMessage(ctx, req)
	if err != nil {
		if errors.Is(err, im.ErrGone) {
			// 永久不可达：回执 Gone 而不是错误，让 Outbox 丢弃并停止重试。
			return im.Receipt{Gone: true}, nil
		}
		return im.Receipt{}, err
	}
	d.rememberOwn(result.MessageID)
	return im.Receipt{MessageID: result.MessageID}, nil
}

// ReadThread 读一页会话历史，供评估需要更多上下文时使用。cursor 是本驱动
// 自己编码的（毫秒 + 页游标），空表示从回看窗口开始。
func (d *Driver) ReadThread(ctx context.Context, conv domain.Conversation, cursor string) (im.Page, error) {
	if conv.ID == "" {
		return im.Page{}, errors.New("feishu: read needs a conversation")
	}
	state := d.initialThreadState()
	if cursor != "" {
		parsed, err := decodeThreadCursor(cursor)
		if err != nil {
			return im.Page{}, err
		}
		state = parsed
	}
	page, err := d.tr.ListMessages(ctx, ListRequest{ChatID: conv.ID, StartMillis: state.Millis, PageToken: state.Token, PageSize: d.cfg.PageSize})
	if err != nil {
		return im.Page{}, err
	}
	selfID, _ := d.selfID(ctx)
	chat := d.chatOf(conv.ID, conv.Kind, conv.Name)
	var messages []im.RawEvent
	maxMillis := state.Millis
	for _, m := range page.Messages {
		if m.Deleted {
			continue
		}
		ev, err := d.translate(m, chat, selfID)
		if err != nil {
			d.noteUnreadable()
			continue
		}
		if m.CreateMillis > maxMillis {
			maxMillis = m.CreateMillis
		}
		messages = append(messages, ev)
	}
	state.Token = ""
	if page.HasMore {
		// 翻页期间保持起点不变：续页依赖 page_token + 同一查询窗口，
		// 提前推进起点可能让服务端把后续页过滤掉（评估上下文不能少读）。
		state.Token = page.PageToken
	} else {
		state.Millis = maxMillis
	}
	return im.Page{Messages: messages, Next: encodeThreadCursor(state), Done: !page.HasMore || page.PageToken == ""}, nil
}

// Poll 是长轮询兜底：逐个会话读"上一轮毫秒（含）"之后的消息，返回整轮事件
// 与下一游标。任何会话失败都让整轮失败、游标停在轮次开始处——已取到但未
// 交付的事件会被重读，重复由内容派生 ID 吸收；丢消息则不可接受。
func (d *Driver) Poll(ctx context.Context, cursor string) ([]im.RawEvent, string, error) {
	if len(d.cfg.Chats) == 0 {
		return nil, cursor, nil
	}
	states, err := d.decodeCursor(cursor)
	if err != nil {
		return nil, cursor, err
	}
	selfID, _ := d.selfID(ctx)
	var events []im.RawEvent
	for _, chat := range d.cfg.Chats {
		state := states[chat.ID]
		page, err := d.tr.ListMessages(ctx, ListRequest{ChatID: chat.ID, StartMillis: state.Millis, PageToken: state.Token, PageSize: d.cfg.PageSize})
		if err != nil {
			return nil, cursor, err
		}
		for _, m := range page.Messages {
			if m.Deleted {
				continue // 撤回的消息不该变成 Trigger
			}
			if seenID(state, m.MessageID) {
				continue // 已经交付过：边界重复只交付一次
			}
			ev, err := d.translate(m, chat, selfID)
			if err != nil {
				d.noteUnreadable() // 坏一条只算一条，不拖垮整批
				// 坏消息也要记住：否则每轮都会重新计数，面板会显示虚高的跳过数。
				state.Seen = rememberSeen(state.Seen, m.MessageID)
				continue
			}
			if m.CreateMillis > state.Millis {
				state.Millis = m.CreateMillis
			}
			state.Seen = rememberSeen(state.Seen, m.MessageID)
			events = append(events, ev)
		}
		state.Token = ""
		if page.HasMore {
			state.Token = page.PageToken
		}
		states[chat.ID] = state
	}
	return events, encodeCursor(states, d.cfg.Chats), nil
}

// Subscribe 实现 im.Receiver：先用历史接口长轮询兜底。
//
// 桌面应用在 NAT 后面，飞书的首选接入是事件订阅长连接（WebSocket）。接上
// 长连接（或 webhook）后只需要给 Driver 换一个 Transport 实现：
//   - 长连接：Transport 的 ListMessages/Poll 由 WS 帧实现，驱动与领域层不动；
//   - webhook/事件推送：事件体先过 event.go 的 CheckEventSignature /
//     DecryptEvent（纯 stdlib），再映射成 im.RawEvent 交给 Driver.translate
//     的同款翻译逻辑。
//
// 长轮询本身是正确路径而不是临时 hack：游标只在整轮交付成功后推进，失败重读
// 由内容派生 ID 吸收（§4.4 不变量 1），Health 会如实报告连接状态。
func (d *Driver) Subscribe(ctx context.Context, out chan<- im.RawEvent) error {
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

// Unreadable 返回被跳过的坏消息数（面板可解释"为什么少了一条"）。
func (d *Driver) Unreadable() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.unreadable
}

// --- 内部状态 ---

func (d *Driver) selfID(ctx context.Context) (string, error) {
	self, err := d.Self(ctx)
	if err != nil {
		// 身份拿不到时不阻塞收消息：@ 判定会退化为"按 id 精确匹配"，
		// 但驱动仍应把消息交给上层（宁可让人看到消息，也不要因为一次
		// bot 信息读取失败而整条流停住）。
		return "", err
	}
	return self.ID, nil
}

func (d *Driver) rememberOwn(id string) {
	if id == "" {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.own[id]; ok {
		return
	}
	d.own[id] = struct{}{}
	d.ownOrder = append(d.ownOrder, id)
	if len(d.ownOrder) > maxOwnIDs {
		oldest := d.ownOrder[0]
		d.ownOrder = d.ownOrder[1:]
		delete(d.own, oldest)
	}
}

func (d *Driver) isOwn(id string) bool {
	if id == "" {
		return false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	_, ok := d.own[id]
	return ok
}

func (d *Driver) noteUnreadable() {
	d.mu.Lock()
	d.unreadable++
	d.mu.Unlock()
}

func (d *Driver) chatOf(id string, kind domain.ConversationKind, name string) Chat {
	for _, chat := range d.cfg.Chats {
		if chat.ID == id {
			return chat
		}
	}
	if kind == "" {
		kind = domain.ConvGroup
	}
	return Chat{ID: id, Name: name, Kind: kind}
}

func (d *Driver) initialMillis() int64 {
	return d.cfg.Now().Add(-d.cfg.InitialBackfill).UnixMilli()
}

func (d *Driver) initialThreadState() threadCursor {
	return threadCursor{Millis: d.initialMillis()}
}

func (d *Driver) initialChatState(chatID string) chatCursor {
	return chatCursor{ID: chatID, Millis: d.initialMillis()}
}

// maxSeenIDs 是每个会话记住的已交付消息数上限：一页 50 条、连续几十轮
// 足够覆盖"同秒重复"窗口，又不会让游标无限膨胀。
const maxSeenIDs = 256

func seenID(state chatCursor, id string) bool {
	for _, seen := range state.Seen {
		if seen == id {
			return true
		}
	}
	return false
}

func rememberSeen(seen []string, id string) []string {
	if id == "" || seenID(chatCursor{Seen: seen}, id) {
		return seen
	}
	seen = append(seen, id)
	if len(seen) > maxSeenIDs {
		seen = seen[len(seen)-maxSeenIDs:]
	}
	return seen
}

// chatCursor 是一个会话的读取位置：毫秒（含）+ 页游标 + 已交付的消息 id。
//
// 为什么要 Seen：飞书历史接口的 start_time 是"秒"且语义接近"含起点"，
// 如果只用毫秒推进，同一条消息（或同一秒内的消息）会在每次轮询里重复返回，
// 订阅循环就会原地打转、不停投递同一条。Seen 让边界只交付一次，同时保留
// "同一秒内新到的消息"（它不在 Seen 里，仍会被交付）。
type chatCursor struct {
	ID     string   `json:"i"`
	Millis int64    `json:"m"`
	Token  string   `json:"t,omitempty"`
	Seen   []string `json:"s,omitempty"`
}

// threadCursor 是 ReadThread 的单会话游标。
type threadCursor struct {
	Millis int64  `json:"m"`
	Token  string `json:"t,omitempty"`
}

// encodeCursor 把每个会话的位置编码成一个不透明字符串。为什么不是单一
// 全局时间戳：多个会话的读取速率不同，一个全局值会让慢会话重复、快会话丢消息。
func encodeCursor(states map[string]chatCursor, chats []Chat) string {
	ordered := make([]chatCursor, 0, len(chats))
	for _, chat := range chats {
		state, ok := states[chat.ID]
		if !ok {
			continue
		}
		state.ID = chat.ID
		ordered = append(ordered, state)
	}
	raw, _ := json.Marshal(cursorEnvelope{Chats: ordered})
	return base64.RawURLEncoding.EncodeToString(raw)
}

type cursorEnvelope struct {
	Chats []chatCursor `json:"c"`
}

// decodeCursor 解码游标；空串表示首轮（各会话从回看窗口开始）。
func (d *Driver) decodeCursor(cursor string) (map[string]chatCursor, error) {
	states := map[string]chatCursor{}
	if cursor == "" {
		for _, chat := range d.cfg.Chats {
			states[chat.ID] = d.initialChatState(chat.ID)
		}
		return states, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return nil, fmt.Errorf("feishu: malformed cursor: %w", err)
	}
	var env cursorEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("feishu: malformed cursor: %w", err)
	}
	for _, state := range env.Chats {
		if state.ID == "" {
			continue
		}
		states[state.ID] = state
	}
	for _, chat := range d.cfg.Chats {
		if _, ok := states[chat.ID]; !ok {
			states[chat.ID] = d.initialChatState(chat.ID)
		}
	}
	return states, nil
}

func encodeThreadCursor(state threadCursor) string {
	raw, _ := json.Marshal(state)
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodeThreadCursor(cursor string) (threadCursor, error) {
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return threadCursor{}, fmt.Errorf("feishu: malformed thread cursor: %w", err)
	}
	var state threadCursor
	if err := json.Unmarshal(raw, &state); err != nil {
		return threadCursor{}, fmt.Errorf("feishu: malformed thread cursor: %w", err)
	}
	return state, nil
}
