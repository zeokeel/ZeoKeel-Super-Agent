package feishu

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zeokeel/super-agent/internal/domain"
	"github.com/zeokeel/super-agent/internal/im"
)

// fakeTransport 是测试用传输：所有网络交互都变成可断言的函数字段，
// 测试绝不联网。
type fakeTransport struct {
	mu sync.Mutex

	bot     Bot
	botErr  error
	token   Token
	sendErr error
	sendID  string

	// list 按请求返回一页消息；nil 表示空页。
	list func(req ListRequest) (MessagesPage, error)
	// members 按页返回成员；nil 表示空页。
	members func(chatID, pageToken string) (MembersPage, error)

	tokenCalls int
	sends      []SendRequest
	listCalls  []ListRequest
}

func (f *fakeTransport) TenantToken(context.Context) (Token, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tokenCalls++
	if f.token.Value == "" {
		f.token = Token{Value: "t-fake", Expire: time.Hour}
	}
	return f.token, nil
}

func (f *fakeTransport) BotInfo(context.Context) (Bot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.botErr != nil {
		return Bot{}, f.botErr
	}
	if f.bot.OpenID == "" {
		f.bot = Bot{OpenID: "ou_bot", Name: "ZeoKeel"}
	}
	return f.bot, nil
}

func (f *fakeTransport) SendMessage(_ context.Context, req SendRequest) (SendResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sends = append(f.sends, req)
	if f.sendErr != nil {
		return SendResult{}, f.sendErr
	}
	id := f.sendID
	if id == "" {
		id = "om_sent"
	}
	return SendResult{MessageID: id}, nil
}

func (f *fakeTransport) ListMembers(_ context.Context, chatID, pageToken string) (MembersPage, error) {
	f.mu.Lock()
	fn := f.members
	f.mu.Unlock()
	if fn == nil {
		return MembersPage{}, nil
	}
	return fn(chatID, pageToken)
}

func (f *fakeTransport) ListMessages(_ context.Context, req ListRequest) (MessagesPage, error) {
	f.mu.Lock()
	f.listCalls = append(f.listCalls, req)
	fn := f.list
	f.mu.Unlock()
	if fn == nil {
		return MessagesPage{}, nil
	}
	return fn(req)
}

func (f *fakeTransport) lastSend(t *testing.T) SendRequest {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.sends) == 0 {
		t.Fatal("no message was sent")
	}
	return f.sends[len(f.sends)-1]
}

func (f *fakeTransport) sendCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.sends)
}

// newDriver 构造一个只在内存里跑的飞书驱动：配置声明两个会话，身份用回退值。
func newDriver(t *testing.T, tr Transport) *Driver {
	t.Helper()
	if tr == nil {
		tr = &fakeTransport{}
	}
	return NewWithTransport(Config{
		AppID: "cli_test", AppSecret: "secret",
		Chats: []Chat{
			{ID: "oc_group", Name: "研发群", Kind: domain.ConvGroup},
			{ID: "oc_dm", Name: "张三", Kind: domain.ConvDirect},
		},
		SelfID:    "ou_bot",
		SelfName:  "ZeoKeel",
		Now:       func() time.Time { return time.UnixMilli(1_700_000_100_000) },
		IdleDelay: time.Millisecond,
		// 订阅循环的退避放到毫秒级：测试要验证"连续失败才报警"，
		// 不能等真实的 1s/2s/4s。
		Retry: im.RetryPolicy{Base: time.Millisecond},
	}, tr)
}

// 群聊里 @ 机器人：会话是 group、actor 是发送者、mention 命中、正文去掉占位符。
func TestTranslateGroupMentioningBot(t *testing.T) {
	d := newDriver(t, nil)
	self, _ := d.Self(context.Background())
	ev, err := d.translate(Message{
		MessageID: "om_1", ChatID: "oc_group", ChatType: "group", MsgType: "text",
		Content:  `{"text":"@_user_1 帮我看下登录页"}`,
		SenderID: "ou_alice", SenderType: "user",
		Mentions:     []Mention{{Key: "@_user_1", OpenID: "ou_bot", Name: "ZeoKeel"}},
		CreateMillis: 1_700_000_000_000,
	}, Chat{ID: "oc_group", Kind: domain.ConvGroup}, self.ID)
	if err != nil {
		t.Fatal(err)
	}
	if ev.Conv.Kind != domain.ConvGroup || ev.Actor.ID != "ou_alice" || ev.Actor.Bot {
		t.Fatalf("unexpected event: %+v", ev)
	}
	if len(ev.Mentions) != 1 || ev.Mentions[0] != "ou_bot" {
		t.Fatalf("mentions = %v, want [ou_bot]", ev.Mentions)
	}
	if ev.Text != "帮我看下登录页" {
		t.Fatalf("text = %q, want mention placeholder removed", ev.Text)
	}
	if !im.AddressedFor(ev, self.ID).Mention {
		t.Fatal("expected Mention for a group message @ the bot")
	}
}

// @所有人 不是 @ 了 agent：open_id 为空，必须被剔除。
func TestTranslateAllMentionIsNotAddressed(t *testing.T) {
	d := newDriver(t, nil)
	ev, err := d.translate(Message{
		MessageID: "om_2", ChatID: "oc_group", ChatType: "group", MsgType: "text",
		Content:  `{"text":"@_all 今晚 8 点发版"}`,
		SenderID: "ou_alice", SenderType: "user",
		Mentions:     []Mention{{Key: "@_all", OpenID: "", Name: "所有人"}},
		CreateMillis: 1_700_000_000_001,
	}, Chat{ID: "oc_group", Kind: domain.ConvGroup}, "ou_bot")
	if err != nil {
		t.Fatal(err)
	}
	if len(ev.Mentions) != 0 {
		t.Fatalf("mentions = %v, want empty for @everyone", ev.Mentions)
	}
	if im.AddressedFor(ev, "ou_bot").Mention {
		t.Fatal("@everyone must not count as addressing the bot")
	}
	if !strings.Contains(ev.Text, "@所有人") {
		t.Fatalf("text = %q, want readable @所有人", ev.Text)
	}
}

// 群里 @ 的是同事：不是对 agent 说话。
func TestTranslateMentioningSomeoneElse(t *testing.T) {
	d := newDriver(t, nil)
	ev, err := d.translate(Message{
		MessageID: "om_3", ChatID: "oc_group", ChatType: "group", MsgType: "text",
		Content:  `{"text":"@_user_1 你看下"}`,
		SenderID: "ou_alice", SenderType: "user",
		Mentions:     []Mention{{Key: "@_user_1", OpenID: "ou_bob", Name: "Bob"}},
		CreateMillis: 1_700_000_000_002,
	}, Chat{ID: "oc_group", Kind: domain.ConvGroup}, "ou_bot")
	if err != nil {
		t.Fatal(err)
	}
	if len(ev.Mentions) != 1 || ev.Mentions[0] != "ou_bob" {
		t.Fatalf("mentions = %v", ev.Mentions)
	}
	if im.AddressedFor(ev, "ou_bot").Mention {
		t.Fatal("@ mentioning a colleague must not address the bot")
	}
}

// 单聊：Conversation.Kind 是 direct，Addressed.Direct 为真；act 的类型由
// 配置兜底（历史接口可能不返回 chat_type）。
func TestTranslateDirectMessageFallsBackToConfigKind(t *testing.T) {
	d := newDriver(t, nil)
	ev, err := d.translate(Message{
		MessageID: "om_4", ChatID: "oc_dm", MsgType: "text",
		Content:  `{"text":"你好"}`,
		SenderID: "ou_alice", SenderType: "user",
		CreateMillis: 1_700_000_000_003,
	}, Chat{ID: "oc_dm", Name: "张三", Kind: domain.ConvDirect}, "ou_bot")
	if err != nil {
		t.Fatal(err)
	}
	if ev.Conv.Kind != domain.ConvDirect {
		t.Fatalf("kind = %q, want direct", ev.Conv.Kind)
	}
	if !im.AddressedFor(ev, "ou_bot").Direct {
		t.Fatal("direct conversation must set Addressed.Direct")
	}
}

// 话题群：root 进入 Thread 身份；普通群不因回复把消息拆成新 Thread。
func TestTranslateTopicCarriesRoot(t *testing.T) {
	d := newDriver(t, nil)
	topic, err := d.translate(Message{
		MessageID: "om_5", ChatID: "oc_topic", ChatType: "topic", RootID: "om_root", MsgType: "text",
		Content: `{"text":"继续"}`, SenderID: "ou_alice", SenderType: "user", CreateMillis: 1_700_000_000_004,
	}, Chat{ID: "oc_topic", Kind: domain.ConvTopic}, "ou_bot")
	if err != nil {
		t.Fatal(err)
	}
	if topic.Conv.Kind != domain.ConvTopic || topic.Conv.RootID != "om_root" {
		t.Fatalf("conversation = %+v", topic.Conv)
	}
	group, err := d.translate(Message{
		MessageID: "om_6", ChatID: "oc_group", ChatType: "group", RootID: "om_root", MsgType: "text",
		Content: `{"text":"楼上说得对"}`, SenderID: "ou_alice", SenderType: "user", CreateMillis: 1_700_000_000_005,
	}, Chat{ID: "oc_group", Kind: domain.ConvGroup}, "ou_bot")
	if err != nil {
		t.Fatal(err)
	}
	if group.Conv.RootID != "" {
		t.Fatalf("root = %q, a group reply must not become a new thread", group.Conv.RootID)
	}
}

// 机器人发的消息要标 Bot：Source 据此决定它最多只是 inferred 候选。
func TestTranslateMarksBotSender(t *testing.T) {
	d := newDriver(t, nil)
	ev, err := d.translate(Message{
		MessageID: "om_7", ChatID: "oc_group", ChatType: "group", MsgType: "text",
		Content: `{"text":"构建失败"}`, SenderID: "cli_app", SenderType: "app", CreateMillis: 1_700_000_000_006,
	}, Chat{ID: "oc_group", Kind: domain.ConvGroup}, "ou_bot")
	if err != nil {
		t.Fatal(err)
	}
	if !ev.Actor.Bot {
		t.Fatal("sender_type=app must be marked as a bot")
	}
}

// 富文本 post：正文、@ 与图片都要被拍平；@ 自己的人称要保留 @ 事实。
func TestTranslatePostPayload(t *testing.T) {
	d := newDriver(t, nil)
	post, _ := json.Marshal(map[string]any{
		"title": "标题",
		"content": [][]map[string]any{
			{{"tag": "text", "text": "看下这个"}, {"tag": "at", "user_id": "ou_bot", "user_name": "ZeoKeel"}, {"tag": "img", "image_key": "img_1"}},
			{{"tag": "a", "text": "链接", "href": "https://example.com"}},
		},
	})
	ev, err := d.translate(Message{
		MessageID: "om_8", ChatID: "oc_group", ChatType: "group", MsgType: "post",
		Content: string(post), SenderID: "ou_alice", SenderType: "user", CreateMillis: 1_700_000_000_007,
	}, Chat{ID: "oc_group", Kind: domain.ConvGroup}, "ou_bot")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ev.Text, "看下这个") || !strings.Contains(ev.Text, "链接") {
		t.Fatalf("text = %q", ev.Text)
	}
	if len(ev.Mentions) != 1 || ev.Mentions[0] != "ou_bot" {
		t.Fatalf("mentions = %v", ev.Mentions)
	}
	if len(ev.MediaRef) != 1 || ev.MediaRef[0] != "img_1" {
		t.Fatalf("media = %v", ev.MediaRef)
	}
}

// 不认识的类型不整批失败：标 Unsupported，让 Source 计数而不是建 Ticket。
func TestTranslateUnsupportedType(t *testing.T) {
	d := newDriver(t, nil)
	ev, err := d.translate(Message{
		MessageID: "om_9", ChatID: "oc_group", ChatType: "group", MsgType: "sticker",
		Content: `{}`, SenderID: "ou_alice", SenderType: "user", CreateMillis: 1_700_000_000_008,
	}, Chat{ID: "oc_group", Kind: domain.ConvGroup}, "ou_bot")
	if err != nil {
		t.Fatal(err)
	}
	if !ev.Unsupported {
		t.Fatal("sticker must be marked Unsupported")
	}
}

// 坏数据（缺 id / 缺时间）要报错，由调用方逐条跳过并计数。
func TestTranslateRejectsMalformedMessages(t *testing.T) {
	d := newDriver(t, nil)
	chat := Chat{ID: "oc_group", Kind: domain.ConvGroup}
	if _, err := d.translate(Message{ChatID: "oc_group", MsgType: "text", Content: `{"text":"x"}`, SenderID: "ou_a", CreateMillis: 1}, chat, "ou_bot"); err == nil {
		t.Fatal("missing message id must fail")
	}
	if _, err := d.translate(Message{MessageID: "om_x", ChatID: "oc_group", MsgType: "text", Content: `{"text":"x"}`, SenderID: "ou_a"}, chat, "ou_bot"); err == nil {
		t.Fatal("missing create time must fail")
	}
}

// 只有确证父消息是自己发的，ReplyToBot 才为真：这决定 Addressed.ReplyTo。
func TestReplyToBotNeedsOwnMessage(t *testing.T) {
	tr := &fakeTransport{sendID: "om_own"}
	d := newDriver(t, tr)
	if _, err := d.Send(context.Background(), im.Outbound{Key: "k1", Conv: domain.Conversation{ID: "oc_group"}, Text: "收到"}); err != nil {
		t.Fatal(err)
	}
	self, _ := d.Self(context.Background())
	own, err := d.translate(Message{
		MessageID: "om_10", ChatID: "oc_group", ChatType: "group", MsgType: "text",
		Content: `{"text":"再改一下"}`, SenderID: "ou_alice", SenderType: "user",
		ParentID: "om_own", CreateMillis: 1_700_000_000_009,
	}, Chat{ID: "oc_group", Kind: domain.ConvGroup}, self.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !own.ReplyToBot || !im.AddressedFor(own, self.ID).ReplyTo {
		t.Fatal("a reply to our own message must be a reply fact")
	}
	other, err := d.translate(Message{
		MessageID: "om_11", ChatID: "oc_group", ChatType: "group", MsgType: "text",
		Content: `{"text":"同意"}`, SenderID: "ou_alice", SenderType: "user",
		ParentID: "om_someone_else", CreateMillis: 1_700_000_000_010,
	}, Chat{ID: "oc_group", Kind: domain.ConvGroup}, self.ID)
	if err != nil {
		t.Fatal(err)
	}
	if other.ReplyToBot {
		t.Fatal("replying to another person must not be a reply-to-agent fact")
	}
}

// 发送：幂等键进 uuid、markdown 走卡片、回复走 reply 目标。
func TestSendUsesUUIDAndCardForMarkdown(t *testing.T) {
	tr := &fakeTransport{sendID: "om_out"}
	d := newDriver(t, tr)
	plain, err := d.Send(context.Background(), im.Outbound{Key: "k-plain", Conv: domain.Conversation{ID: "oc_group"}, Text: "收到"})
	if err != nil || plain.MessageID != "om_out" {
		t.Fatalf("send = %+v, %v", plain, err)
	}
	req := tr.lastSend(t)
	if req.UUID != "k-plain" || req.MsgType != "text" || req.ChatID != "oc_group" {
		t.Fatalf("request = %+v", req)
	}
	if _, err := d.Send(context.Background(), im.Outbound{Key: "k-md", Conv: domain.Conversation{ID: "oc_group"}, Text: "**bold**", Markdown: true, ReplyTo: "om_parent"}); err != nil {
		t.Fatal(err)
	}
	md := tr.lastSend(t)
	if md.MsgType != "interactive" || md.ReplyTo != "om_parent" || !strings.Contains(md.Content, "markdown") {
		t.Fatalf("markdown request = %+v", md)
	}
	// 没有业务 key 时用内容派生，重放可算出同一个 uuid。
	if _, err := d.Send(context.Background(), im.Outbound{Conv: domain.Conversation{ID: "oc_group"}, Text: "无 key"}); err != nil {
		t.Fatal(err)
	}
	if derived := tr.lastSend(t).UUID; derived == "" || len(derived) < 16 {
		t.Fatalf("derived uuid = %q", derived)
	}
}

// 目标永久不可达：回执 Gone（不返回错误），Outbox 据此停止重试。
func TestSendMapsGone(t *testing.T) {
	tr := &fakeTransport{sendErr: fmt.Errorf("%w: bot not in chat", im.ErrGone)}
	d := newDriver(t, tr)
	receipt, err := d.Send(context.Background(), im.Outbound{Key: "k", Conv: domain.Conversation{ID: "oc_gone"}, Text: "hi"})
	if err != nil {
		t.Fatalf("gone must not be an error: %v", err)
	}
	if !receipt.Gone {
		t.Fatal("expected Gone receipt")
	}
}

// 超长文本按上限截断，不把整段历史塞给 IM 接口。
func TestSendTruncatesToCapabilityLimit(t *testing.T) {
	tr := &fakeTransport{}
	d := newDriver(t, tr)
	long := strings.Repeat("中", maxTextLen+10)
	if _, err := d.Send(context.Background(), im.Outbound{Key: "k", Conv: domain.Conversation{ID: "oc_group"}, Text: long}); err != nil {
		t.Fatal(err)
	}
	var body struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal([]byte(tr.lastSend(t).Content), &body); err != nil {
		t.Fatal(err)
	}
	if runes := []rune(body.Text); len(runes) > maxTextLen {
		t.Fatalf("sent %d runes, cap is %d", len(runes), maxTextLen)
	}
}

// 轮询是"游标 + 增量"：第一轮回看窗口内的消息，第二轮只取新消息。
func TestPollIsIncremental(t *testing.T) {
	tr := &fakeTransport{}
	tr.list = func(req ListRequest) (MessagesPage, error) {
		var page MessagesPage
		if req.ChatID != "oc_group" {
			return page, nil
		}
		for _, m := range []Message{
			{MessageID: "om_a", ChatID: req.ChatID, MsgType: "text", Content: `{"text":"早"}`, SenderID: "ou_alice", SenderType: "user", CreateMillis: 1_700_000_050_000},
			{MessageID: "om_b", ChatID: req.ChatID, MsgType: "text", Content: `{"text":"早2"}`, SenderID: "ou_alice", SenderType: "user", CreateMillis: 1_700_000_060_000},
		} {
			if m.CreateMillis >= req.StartMillis {
				page.Messages = append(page.Messages, m)
			}
		}
		return page, nil
	}
	d := newDriver(t, tr)
	first, cursor, err := d.Poll(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	// 只有 oc_group 有消息 → 2 个事件；第二次轮询从推进后的毫秒开始，不应重复。
	if len(first) != 2 {
		t.Fatalf("first poll delivered %d events, want 2", len(first))
	}
	if cursor == "" {
		t.Fatal("first poll must return a cursor")
	}
	second, next, err := d.Poll(context.Background(), cursor)
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != 0 {
		t.Fatalf("second poll repeated %d events (boundary must be delivered once)", len(second))
	}
	// 再来一轮也不能重复：实现用"已交付 id"过滤边界重复，而不是无脑重读。
	third, _, err := d.Poll(context.Background(), cursor)
	if err != nil || len(third) != 0 {
		t.Fatalf("third poll = %d events, %v; want none", len(third), err)
	}
	if next == "" {
		t.Fatal("cursor must not be lost")
	}
}

// 分页：一页有 has_more 时游标带页 token，下一页读完清空 token。
func TestPollPaginatesAcrossCalls(t *testing.T) {
	pages := []MessagesPage{
		{Messages: []Message{{MessageID: "om_p1", ChatID: "oc_group", MsgType: "text", Content: `{"text":"1"}`, SenderID: "ou_a", SenderType: "user", CreateMillis: 1_700_000_050_000}}, PageToken: "t2", HasMore: true},
		{Messages: []Message{{MessageID: "om_p2", ChatID: "oc_group", MsgType: "text", Content: `{"text":"2"}`, SenderID: "ou_a", SenderType: "user", CreateMillis: 1_700_000_060_000}}},
	}
	var index int
	tr := &fakeTransport{}
	tr.list = func(req ListRequest) (MessagesPage, error) {
		if req.ChatID != "oc_group" {
			return MessagesPage{}, nil // 第二个会话没有消息
		}
		if index >= len(pages) {
			return MessagesPage{}, nil
		}
		page := pages[index]
		index++
		return page, nil
	}
	d := newDriver(t, tr)
	first, cursor, err := d.Poll(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 1 || first[0].ID != "om_p1" {
		t.Fatalf("first = %+v", first)
	}
	second, _, err := d.Poll(context.Background(), cursor)
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != 1 || second[0].ID != "om_p2" {
		t.Fatalf("second = %+v", second)
	}
}

// 轮询失败：整轮失败、游标停在轮次开始处（不能越过没交付的消息）。
func TestPollFailureKeepsCursor(t *testing.T) {
	tr := &fakeTransport{}
	fail := false
	tr.list = func(req ListRequest) (MessagesPage, error) {
		if fail {
			return MessagesPage{}, errors.New("network down")
		}
		return MessagesPage{Messages: []Message{{MessageID: "om_x", ChatID: req.ChatID, MsgType: "text", Content: `{"text":"x"}`, SenderID: "ou_a", SenderType: "user", CreateMillis: 1_700_000_050_000}}}, nil
	}
	d := newDriver(t, tr)
	_, cursor, err := d.Poll(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	fail = true
	events, next, err := d.Poll(context.Background(), cursor)
	if err == nil {
		t.Fatal("expected poll error")
	}
	if len(events) != 0 || next != cursor {
		t.Fatalf("failed poll returned events=%d next %q, want none and cursor %q", len(events), next, cursor)
	}
}

// Subscribe 按顺序交付事件，ctx 取消后停止。
func TestSubscribeDeliversAndStops(t *testing.T) {
	tr := &fakeTransport{}
	delivered := false
	tr.list = func(req ListRequest) (MessagesPage, error) {
		if req.ChatID != "oc_group" || delivered {
			return MessagesPage{}, nil
		}
		delivered = true
		return MessagesPage{Messages: []Message{{MessageID: "om_s", ChatID: req.ChatID, MsgType: "text", Content: `{"text":"来活了"}`, SenderID: "ou_a", SenderType: "user", CreateMillis: 1_700_000_050_000}}}, nil
	}
	d := newDriver(t, tr)
	ctx, cancel := context.WithCancel(context.Background())
	out := make(chan im.RawEvent, 4)
	done := make(chan error, 1)
	go func() { done <- d.Subscribe(ctx, out) }()
	select {
	case ev := <-out:
		if ev.ID != "om_s" || ev.Text != "来活了" {
			t.Fatalf("event = %+v", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no event through Subscribe")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Subscribe did not return after cancel")
	}
}

// 连续失败 3 次后健康状态变 error，恢复后变 ready。
func TestHealthTransitions(t *testing.T) {
	tr := &fakeTransport{}
	// 订阅循环在另一个 goroutine 里读这个开关，测试协程写它：必须用 atomic，
	// 否则 -race 会报数据竞争（被测代码没竞争，测试自己有）。
	var failing atomic.Bool
	failing.Store(true)
	tr.list = func(ListRequest) (MessagesPage, error) {
		if failing.Load() {
			return MessagesPage{}, errors.New("token expired")
		}
		return MessagesPage{}, nil
	}
	d := newDriver(t, tr)
	ctx, cancel := context.WithCancel(context.Background())
	out := make(chan im.RawEvent, 1)
	go func() { _ = d.Subscribe(ctx, out) }()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && d.Health(context.Background()).State != im.HealthError {
		time.Sleep(time.Millisecond)
	}
	if d.Health(context.Background()).State != im.HealthError {
		t.Fatalf("health = %+v, want error after 3 failures", d.Health(context.Background()))
	}
	failing.Store(false)
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && d.Health(context.Background()).State != im.HealthReady {
		time.Sleep(time.Millisecond)
	}
	if d.Health(context.Background()).State != im.HealthReady {
		t.Fatalf("health = %+v, want ready after recovery", d.Health(context.Background()))
	}
	cancel()
}

// 成员分页：两页拼起来，空 id 被丢掉（空 id 会匹配任何 actor）。
func TestMembersPaginate(t *testing.T) {
	tr := &fakeTransport{}
	tr.members = func(chatID, pageToken string) (MembersPage, error) {
		if pageToken == "" {
			return MembersPage{Items: []MemberItem{{ID: "ou_a", Name: "A"}, {ID: "", Name: "bad"}}, PageToken: "p2", HasMore: true}, nil
		}
		return MembersPage{Items: []MemberItem{{ID: "ou_b", Name: "B"}}}, nil
	}
	d := newDriver(t, tr)
	members, err := d.Members(context.Background(), "oc_group")
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 2 || members[0].ID != "ou_a" || members[1].ID != "ou_b" {
		t.Fatalf("members = %+v", members)
	}
}

// 读线程：分页，Done 表示是否还有下一页。
func TestReadThreadPaginates(t *testing.T) {
	tr := &fakeTransport{}
	tr.list = func(req ListRequest) (MessagesPage, error) {
		if req.PageToken == "" {
			return MessagesPage{Messages: []Message{{MessageID: "om_1", ChatID: "oc_group", MsgType: "text", Content: `{"text":"1"}`, SenderID: "ou_a", SenderType: "user", CreateMillis: 1_700_000_050_000}}, PageToken: "p2", HasMore: true}, nil
		}
		return MessagesPage{Messages: []Message{{MessageID: "om_2", ChatID: "oc_group", MsgType: "text", Content: `{"text":"2"}`, SenderID: "ou_a", SenderType: "user", CreateMillis: 1_700_000_060_000}}}, nil
	}
	d := newDriver(t, tr)
	page, err := d.ReadThread(context.Background(), domain.Conversation{ID: "oc_group"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Messages) != 1 || page.Done || page.Next == "" {
		t.Fatalf("page = %+v", page)
	}
	second, err := d.ReadThread(context.Background(), domain.Conversation{ID: "oc_group"}, page.Next)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Messages) != 1 || !second.Done {
		t.Fatalf("second page = %+v", second)
	}
}

// 损坏的游标必须报错而不是静默从头重放（那会把历史再收一遍）。
func TestMalformedCursorIsRejected(t *testing.T) {
	d := newDriver(t, nil)
	if _, _, err := d.Poll(context.Background(), "not-base64!!"); err == nil {
		t.Fatal("malformed cursor must fail")
	}
	if _, err := d.ReadThread(context.Background(), domain.Conversation{ID: "oc_group"}, "not-base64!!"); err == nil {
		t.Fatal("malformed thread cursor must fail")
	}
}

// 身份回退：配了 SelfID 就不必调 bot/v3/info（无权限也能 @ 判定）。
func TestSelfFallsBackToConfigWithoutNetwork(t *testing.T) {
	tr := &fakeTransport{botErr: errors.New("no permission")}
	d := newDriver(t, tr)
	self, err := d.Self(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if self.ID != "ou_bot" || self.Name != "ZeoKeel" {
		t.Fatalf("self = %+v", self)
	}
}

// New 拒绝没有凭据的配置：真实驱动不该在无身份时悄悄启动。
func TestNewRequiresCredentials(t *testing.T) {
	if _, err := New(Config{}); err == nil {
		t.Fatal("empty credentials must fail")
	}
}

// 能力声明与名字。
func TestIdentityAndCapabilities(t *testing.T) {
	d := newDriver(t, nil)
	if d.Name() != "feishu" {
		t.Fatalf("name = %q", d.Name())
	}
	caps := d.Capabilities()
	if !caps.Markdown || !caps.RichCard || !caps.Threads || caps.MaxTextLen != maxTextLen {
		t.Fatalf("capabilities = %+v", caps)
	}
	if caps.Files {
		t.Fatal("files are not implemented and must not be advertised")
	}
}

// 构造时就拒绝坏会话列表：空 id 会去请求一个空会话，重复 id 会共用同一份游标。
func TestNewValidatesChatList(t *testing.T) {
	cfg := func(chats ...Chat) Config {
		return Config{AppID: "cli_test", AppSecret: "secret", Chats: chats}
	}
	if _, err := New(cfg(Chat{ID: ""})); err == nil {
		t.Fatal("empty chat id must be rejected")
	}
	if _, err := New(cfg(Chat{ID: "oc_1"}, Chat{ID: "oc_1"})); err == nil {
		t.Fatal("duplicate chat id must be rejected")
	}
	if _, err := New(cfg()); err != nil {
		t.Fatalf("empty chat list is legal (listen to nothing): %v", err)
	}
	if _, err := New(cfg(Chat{ID: "oc_1"}, Chat{ID: "oc_2"})); err != nil {
		t.Fatalf("distinct chat ids must be accepted: %v", err)
	}
}

// 成员分页不收敛时必须报错，而不是返回半份名单把白名单悄悄裁掉。
func TestMembersPageLimitIsAnError(t *testing.T) {
	tr := &fakeTransport{}
	page := 0
	tr.members = func(string, string) (MembersPage, error) {
		page++
		return MembersPage{
			Items:     []MemberItem{{ID: fmt.Sprintf("ou_%d", page), Name: "x"}},
			PageToken: fmt.Sprintf("p%d", page),
			HasMore:   true,
		}, nil
	}
	d := newDriver(t, tr)
	_, err := d.Members(context.Background(), "oc_group")
	if err == nil || !strings.Contains(err.Error(), "exceeded") {
		t.Fatalf("err = %v, want a page-limit error", err)
	}
}

// 图片/文件/语音消息要保留 media 引用（Wave-2 的媒体读取按它去取文件）。
func TestTranslateMediaMessagesCarryRefs(t *testing.T) {
	d := newDriver(t, nil)
	chat := Chat{ID: "oc_group", Kind: domain.ConvGroup}
	cases := map[string]struct {
		content string
		wantRef string
		wantTxt string
	}{
		"image": {`{"image_key":"img_1"}`, "img_1", "[图片]"},
		"file":  {`{"file_key":"file_1","file_name":"a.pdf"}`, "file_1", "[文件] a.pdf"},
		"media": {`{"file_key":"media_1"}`, "media_1", "[视频]"},
		"audio": {`{"file_key":"audio_1"}`, "audio_1", "[语音]"},
	}
	for msgType, tc := range cases {
		ev, err := d.translate(Message{
			MessageID: "om_" + msgType, ChatID: "oc_group", ChatType: "group",
			MsgType: msgType, Content: tc.content, SenderID: "ou_alice",
			SenderType: "user", CreateMillis: 1,
		}, chat, "ou_bot")
		if err != nil {
			t.Fatalf("%s: %v", msgType, err)
		}
		if len(ev.MediaRef) != 1 || ev.MediaRef[0] != tc.wantRef {
			t.Fatalf("%s: media = %v, want %q", msgType, ev.MediaRef, tc.wantRef)
		}
		if !strings.Contains(ev.Text, tc.wantTxt) {
			t.Fatalf("%s: text = %q, want %q", msgType, ev.Text, tc.wantTxt)
		}
	}
}
