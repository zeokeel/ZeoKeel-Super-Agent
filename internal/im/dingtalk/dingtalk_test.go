package dingtalk

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zeokeel/super-agent/internal/domain"
	"github.com/zeokeel/super-agent/internal/im"
)

// fakeTransport 是测试用 Stream/HTTP 传输：所有交互可断言，绝不联网。
type fakeTransport struct {
	mu sync.Mutex

	bot   Token
	pages []EventsPage
	err   error
	// streamOK 表示该假传输声称支持 Stream 接收（默认 false，模拟 HTTP 传输）。
	streamOK bool

	sends     []SendRequest
	sendErr   error
	sendID    string
	members   []MembersPage
	memberErr error
	calls     int
}

func (f *fakeTransport) AccessToken(context.Context) (Token, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return Token{Value: "dt-fake", Expire: time.Hour}, nil
}

func (f *fakeTransport) SendRobot(_ context.Context, req SendRequest) (SendResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sends = append(f.sends, req)
	if f.sendErr != nil {
		return SendResult{}, f.sendErr
	}
	id := f.sendID
	if id == "" {
		id = "dt_msg"
	}
	return SendResult{MessageID: id}, nil
}

func (f *fakeTransport) ListMembers(context.Context, string, string) (MembersPage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.memberErr != nil {
		return MembersPage{}, f.memberErr
	}
	if len(f.members) == 0 {
		return MembersPage{}, nil
	}
	page := f.members[0]
	f.members = f.members[1:]
	return page, nil
}

// SupportsStream 实现 StreamSupport。
func (f *fakeTransport) SupportsStream() bool { return f.streamOK }

func (f *fakeTransport) StreamEvents(context.Context, string) (EventsPage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return EventsPage{}, f.err
	}
	if len(f.pages) == 0 {
		return EventsPage{}, nil
	}
	page := f.pages[0]
	f.pages = f.pages[1:]
	return page, nil
}

func (f *fakeTransport) lastSend(t *testing.T) SendRequest {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.sends) == 0 {
		t.Fatal("no send recorded")
	}
	return f.sends[len(f.sends)-1]
}

func (f *fakeTransport) sendCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.sends)
}

func newDriver(t *testing.T, tr Transport) *Driver {
	t.Helper()
	if tr == nil {
		tr = &fakeTransport{}
	}
	return NewWithTransport(Config{
		RobotCode: "ding_robot",
		BotName:   "ZeoKeel",
		Now:       func() time.Time { return time.UnixMilli(1_700_000_100_000) },
		IdleDelay: time.Millisecond,
		Retry:     im.RetryPolicy{Base: time.Millisecond},
	}, tr)
}

// helperEvent 构造一条 Stream 回调。
func helperEvent() Event {
	return Event{
		MsgID: "dt_1", MsgType: "text", Content: `{"content":"@ZeoKeel 帮我看下"}`,
		SenderStaffID: "staff_alice", SenderNick: "张三",
		ConversationID: "cid_group", ConversationType: "2", ConversationTitle: "研发群",
		CreateAt:   1_700_000_000_000,
		AtUsers:    []AtUser{{DingTalkID: "ding_robot", Name: "ZeoKeel"}},
		IsInAtList: true,
	}
}

// 群聊 @ 机器人：mention 命中、正文去掉 @名字、会话是 group。
func TestTranslateGroupMention(t *testing.T) {
	d := newDriver(t, nil)
	self, _ := d.Self(context.Background())
	ev, err := d.translate(helperEvent(), self.ID)
	if err != nil {
		t.Fatal(err)
	}
	if ev.Conv.Kind != domain.ConvGroup || ev.Actor.ID != "staff_alice" || ev.Actor.Bot {
		t.Fatalf("event = %+v", ev)
	}
	if len(ev.Mentions) != 1 || ev.Mentions[0] != "ding_robot" {
		t.Fatalf("mentions = %v", ev.Mentions)
	}
	if ev.Text != "帮我看下" {
		t.Fatalf("text = %q, want bot mention stripped", ev.Text)
	}
	if !im.AddressedFor(ev, self.ID).Mention {
		t.Fatal("expected Mention for a group @ the bot")
	}
}

// @所有人 不是 @ 了 agent：atUsers 里是 @all，必须剔除；正文保留可读的 @所有人。
func TestTranslateAllMentionIsNotAddressed(t *testing.T) {
	d := newDriver(t, nil)
	event := helperEvent()
	event.Content = `{"content":"@所有人 今晚 8 点发版"}`
	event.AtUsers = []AtUser{{DingTalkID: "@all", Name: "所有人"}}
	event.IsInAtList = true
	ev, err := d.translate(event, "ding_robot")
	if err != nil {
		t.Fatal(err)
	}
	if len(ev.Mentions) != 0 {
		t.Fatalf("mentions = %v, want empty for @everyone", ev.Mentions)
	}
	if im.AddressedFor(ev, "ding_robot").Mention {
		t.Fatal("@everyone must not address the bot")
	}
	if !strings.Contains(ev.Text, "@所有人") {
		t.Fatalf("text = %q, want readable @所有人", ev.Text)
	}
}

// 少数回调不带 atUsers：isInAtList 为真时用"正文里是否有 @机器人名"兜底；
// @所有人 的正文不等于机器人名，所以不会误判。
func TestMentionFallbackByBotName(t *testing.T) {
	d := newDriver(t, nil)
	event := helperEvent()
	event.AtUsers = nil
	event.IsInAtList = true
	event.Content = `{"content":"@ZeoKeel 在吗"}`
	ev, err := d.translate(event, "ding_robot")
	if err != nil {
		t.Fatal(err)
	}
	if len(ev.Mentions) != 1 || ev.Mentions[0] != "ding_robot" {
		t.Fatalf("mentions = %v, want fallback self id", ev.Mentions)
	}

	// 只有 @所有人 时不能靠兜底命中。
	event.Content = `{"content":"@所有人 开会"}`
	ev, err = d.translate(event, "ding_robot")
	if err != nil {
		t.Fatal(err)
	}
	if len(ev.Mentions) != 0 {
		t.Fatalf("mentions = %v, @everyone must not fall back to self", ev.Mentions)
	}
}

// 单聊：conversationType=1 → Direct；这也意味着"直接对 agent 说"。
func TestTranslateDirectConversation(t *testing.T) {
	d := newDriver(t, nil)
	event := helperEvent()
	event.ConversationType = "1"
	event.ConversationID = "dm:ding_zhangsan"
	event.Content = `{"content":"帮我看下"}`
	event.AtUsers = nil
	event.IsInAtList = false
	ev, err := d.translate(event, "ding_robot")
	if err != nil {
		t.Fatal(err)
	}
	if ev.Conv.Kind != domain.ConvDirect || !im.AddressedFor(ev, "ding_robot").Direct {
		t.Fatalf("event = %+v, want direct", ev)
	}
}

// 缺少 conversationType 时保守按群处理：误判成单聊会绕过 @ 检查、放大权限。
func TestMissingConversationTypeDefaultsToGroup(t *testing.T) {
	d := newDriver(t, nil)
	event := helperEvent()
	event.ConversationType = ""
	ev, err := d.translate(event, "ding_robot")
	if err != nil {
		t.Fatal(err)
	}
	if ev.Conv.Kind != domain.ConvGroup {
		t.Fatalf("kind = %q, want group", ev.Conv.Kind)
	}
}

// 没有 staffId 的发送者按机器人处理（钉钉企业内的人一定有 staffId）。
func TestTranslateBotSenderHeuristic(t *testing.T) {
	d := newDriver(t, nil)
	event := helperEvent()
	event.SenderStaffID = ""
	event.SenderID = "$:LWCP_v1:$bot"
	ev, err := d.translate(event, "ding_robot")
	if err != nil {
		t.Fatal(err)
	}
	if !ev.Actor.Bot {
		t.Fatal("sender without staff id must be marked as a bot")
	}
}

// Stream 只给"这是一条回复"的布尔值，不给父消息 id：ReplyTo 留空、
// ReplyToBot 置真（回复事实仍然成立）。
func TestTranslateReplyFlag(t *testing.T) {
	d := newDriver(t, nil)
	event := helperEvent()
	event.IsReplyMsg = true
	ev, err := d.translate(event, "ding_robot")
	if err != nil {
		t.Fatal(err)
	}
	if ev.ReplyTo != "" {
		t.Fatalf("replyTo = %q, want empty (no parent id in stream callbacks)", ev.ReplyTo)
	}
	if !ev.ReplyToBot || !im.AddressedFor(ev, "ding_robot").ReplyTo {
		t.Fatal("isReplyMsg must set the reply fact")
	}
}

// 不认识的类型标 Unsupported，不阻塞整批。
func TestTranslateUnsupportedType(t *testing.T) {
	d := newDriver(t, nil)
	event := helperEvent()
	event.MsgType = "interactive"
	event.Content = `{}`
	ev, err := d.translate(event, "ding_robot")
	if err != nil {
		t.Fatal(err)
	}
	if !ev.Unsupported {
		t.Fatal("interactive card must be marked Unsupported")
	}
}

// 坏数据（缺 msg id / 会话 / 发送者 / 时间）报错，由调用方逐条跳过计数。
func TestTranslateRejectsMalformedEvents(t *testing.T) {
	d := newDriver(t, nil)
	for name, mutate := range map[string]func(*Event){
		"no msg id":       func(e *Event) { e.MsgID = "" },
		"no conversation": func(e *Event) { e.ConversationID = "" },
		"no sender":       func(e *Event) { e.SenderStaffID = ""; e.SenderID = "" },
		"no create time":  func(e *Event) { e.CreateAt = 0 },
	} {
		event := helperEvent()
		mutate(&event)
		if _, err := d.translate(event, "ding_robot"); err == nil {
			t.Fatalf("%s: expected error", name)
		}
	}
}

// 不可见 marker：同 key 稳定、不同 key 不同、全部是零宽字符、可解出哈希。
func TestMarkerIsInvisibleStableAndRecoverable(t *testing.T) {
	first, second := markerFor("k-1"), markerFor("k-1")
	if first != second {
		t.Fatal("same key must produce the same marker")
	}
	if first == markerFor("k-2") {
		t.Fatal("different key must produce a different marker")
	}
	for _, r := range first {
		if r != markerStart && r != '\u200b' && r != '\u200c' {
			t.Fatalf("marker contains printable rune %q", r)
		}
	}
	want := sha256.Sum256([]byte("k-1"))
	if hash, ok := markerHash("正文" + first); !ok || hash != hex.EncodeToString(want[:8]) {
		t.Fatalf("marker hash = %q ok=%v, want %x", hash, ok, want[:8])
	}
	if stripMarker("正文"+first) != "正文" {
		t.Fatalf("stripMarker = %q", stripMarker("正文"+first))
	}
	if _, ok := markerHash("no marker"); ok {
		t.Fatal("plain text must not look like it has a marker")
	}
}

// 发送：正文带 marker、msgKey 正确、群聊用 openConversationId。
func TestSendAppendsMarkerAndUsesGroupShape(t *testing.T) {
	tr := &fakeTransport{}
	d := newDriver(t, tr)
	receipt, err := d.Send(context.Background(), im.Outbound{
		Key: "k-1", Conv: domain.Conversation{Kind: domain.ConvGroup, ID: "cid_group"}, Text: "收到",
	})
	if err != nil || receipt.MessageID != "dt_msg" {
		t.Fatalf("send = %+v, %v", receipt, err)
	}
	req := tr.lastSend(t)
	if req.MsgKey != "sampleText" || req.Direct || req.ConversationID != "cid_group" || req.RobotCode != "ding_robot" {
		t.Fatalf("request = %+v", req)
	}
	var param struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal([]byte(req.MsgParam), &param); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(param.Content, "收到") || stripMarker(param.Content) != "收到" {
		t.Fatalf("content = %q, want text plus invisible marker", param.Content)
	}
	if _, ok := markerHash(param.Content); !ok {
		t.Fatal("sent content must carry the marker")
	}
}

// 单聊发消息需要对方 userId：约定会话 id "dm:<userId>"。
func TestSendDirectUsesUserID(t *testing.T) {
	tr := &fakeTransport{}
	d := newDriver(t, tr)
	if _, err := d.Send(context.Background(), im.Outbound{
		Key: "k-dm", Conv: domain.Conversation{Kind: domain.ConvDirect, ID: "dm:ding_zhangsan"}, Text: "你好",
	}); err != nil {
		t.Fatal(err)
	}
	req := tr.lastSend(t)
	if !req.Direct || req.UserID != "ding_zhangsan" || req.ConversationID != "dm:ding_zhangsan" {
		t.Fatalf("request = %+v", req)
	}
	if _, err := d.Send(context.Background(), im.Outbound{
		Key: "k-dm2", Conv: domain.Conversation{Kind: domain.ConvDirect, ID: "dm:"}, Text: "x",
	}); err == nil {
		t.Fatal("empty direct user id must fail")
	}
}

// markdown 走 sampleMarkdown 模板，并把首行当标题。
func TestSendMarkdownUsesSampleMarkdown(t *testing.T) {
	tr := &fakeTransport{}
	d := newDriver(t, tr)
	if _, err := d.Send(context.Background(), im.Outbound{
		Key: "k-md", Conv: domain.Conversation{Kind: domain.ConvGroup, ID: "cid"}, Text: "结果\n已完成", Markdown: true,
	}); err != nil {
		t.Fatal(err)
	}
	req := tr.lastSend(t)
	if req.MsgKey != "sampleMarkdown" {
		t.Fatalf("msgKey = %q", req.MsgKey)
	}
	var param struct {
		Title string `json:"title"`
		Text  string `json:"text"`
	}
	if err := json.Unmarshal([]byte(req.MsgParam), &param); err != nil {
		t.Fatal(err)
	}
	if param.Title != "结果" || !strings.Contains(param.Text, "已完成") {
		t.Fatalf("param = %+v", param)
	}
	if _, ok := markerHash(param.Title); ok {
		t.Fatal("marker must stay in the body, not the title")
	}
	if _, ok := markerHash(param.Text); !ok {
		t.Fatal("body must carry the marker for audit/dedup")
	}
}

// 进程内幂等：同一个 key 重发不会产生第二条消息（钉钉没有服务端去重）。
func TestSendDedupesSameKeyInProcess(t *testing.T) {
	tr := &fakeTransport{}
	d := newDriver(t, tr)
	msg := im.Outbound{Key: "k-dup", Conv: domain.Conversation{Kind: domain.ConvGroup, ID: "cid"}, Text: "只发一次"}
	first, err := d.Send(context.Background(), msg)
	if err != nil {
		t.Fatal(err)
	}
	second, err := d.Send(context.Background(), msg)
	if err != nil {
		t.Fatal(err)
	}
	if first.MessageID != second.MessageID {
		t.Fatalf("receipts differ: %+v vs %+v", first, second)
	}
	if got := tr.sendCount(); got != 1 {
		t.Fatalf("transport sends = %d, want 1", got)
	}
	msg.Key = "k-other"
	if _, err := d.Send(context.Background(), msg); err != nil {
		t.Fatal(err)
	}
	if got := tr.sendCount(); got != 2 {
		t.Fatalf("transport sends = %d, want 2", got)
	}
}

// 永久不可达：回执 Gone，不返回错误。
func TestSendMapsGone(t *testing.T) {
	tr := &fakeTransport{sendErr: fmt.Errorf("%w: robot not in group", im.ErrGone)}
	d := newDriver(t, tr)
	receipt, err := d.Send(context.Background(), im.Outbound{Key: "k", Conv: domain.Conversation{Kind: domain.ConvGroup, ID: "cid"}, Text: "x"})
	if err != nil {
		t.Fatalf("gone must not be an error: %v", err)
	}
	if !receipt.Gone {
		t.Fatal("expected Gone receipt")
	}
}

// Stream 批量事件：游标由传输决定；空游标保持原值（不能把读位置丢掉）。
func TestPollUsesTransportCursor(t *testing.T) {
	tr := &fakeTransport{pages: []EventsPage{
		{Events: []Event{helperEvent()}, Cursor: "c1"},
		{Events: nil, Cursor: ""},
	}}
	d := newDriver(t, tr)
	events, cursor, err := d.Poll(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].ID != "dt_1" || cursor != "c1" {
		t.Fatalf("events=%d cursor=%q", len(events), cursor)
	}
	_, next, err := d.Poll(context.Background(), cursor)
	if err != nil {
		t.Fatal(err)
	}
	if next != "c1" {
		t.Fatalf("empty cursor page must keep %q, got %q", cursor, next)
	}
}

// 轮询失败不推进游标。
func TestPollFailureKeepsCursor(t *testing.T) {
	tr := &fakeTransport{err: errors.New("stream disconnected")}
	d := newDriver(t, tr)
	events, next, err := d.Poll(context.Background(), "c9")
	if err == nil || len(events) != 0 || next != "c9" {
		t.Fatalf("events=%d next=%q err=%v", len(events), next, err)
	}
}

// 成员接口不支持时原样抛 ErrUnsupported：上层降级为手工白名单，而不是
// 把人全关掉（空名单是更坏的默认）。
func TestMembersUnsupportedPropagates(t *testing.T) {
	tr := &fakeTransport{memberErr: fmt.Errorf("%w: no member api", im.ErrUnsupported)}
	d := newDriver(t, tr)
	if _, err := d.Members(context.Background(), "cid"); !errors.Is(err, im.ErrUnsupported) {
		t.Fatalf("err = %v, want ErrUnsupported", err)
	}
}

// 钉钉没有机器人历史消息 API：ReadThread 明确报不支持，而不是返回空页。
func TestReadThreadUnsupported(t *testing.T) {
	d := newDriver(t, nil)
	if _, err := d.ReadThread(context.Background(), domain.Conversation{ID: "cid"}, ""); !errors.Is(err, im.ErrUnsupported) {
		t.Fatalf("err = %v, want ErrUnsupported", err)
	}
}

// 身份就是 robotCode，启动时不需要网络调用。
func TestSelfIsRobotCodeWithoutNetwork(t *testing.T) {
	tr := &fakeTransport{}
	d := newDriver(t, tr)
	self, err := d.Self(context.Background())
	if err != nil || self.ID != "ding_robot" {
		t.Fatalf("self = %+v, %v", self, err)
	}
	if tr.calls != 0 {
		t.Fatalf("Self made %d transport calls, want 0", tr.calls)
	}
}

// New 拒绝空 robotCode/凭据。
func TestNewRequiresCredentials(t *testing.T) {
	if _, err := New(Config{}); err == nil {
		t.Fatal("empty config must fail")
	}
	if _, err := New(Config{RobotCode: "r"}); err == nil {
		t.Fatal("missing app credentials must fail")
	}
}

// 能力声明：钉钉没有话题/卡片/文件；markdown 通过模板支持。
func TestCapabilities(t *testing.T) {
	d := newDriver(t, nil)
	if d.Name() != "dingtalk" {
		t.Fatalf("name = %q", d.Name())
	}
	caps := d.Capabilities()
	if !caps.Markdown || caps.RichCard || caps.Threads || caps.Files || caps.MaxTextLen != maxTextLen {
		t.Fatalf("capabilities = %+v", caps)
	}
}

// 正文里的控制字符必须经过 encoding/json 转义：手拼 JSON 会产出非法字符串，
// 钉钉会拒收（audit §5 的 quote() 条目）。
func TestSendParamIsValidJSONForControlChars(t *testing.T) {
	const raw = "第一行\b\x00\f\"引号\"\\反斜杠\n第二行\t尾"
	for _, markdown := range []bool{false, true} {
		tr := &fakeTransport{}
		d := newDriver(t, tr)
		if _, err := d.Send(context.Background(), im.Outbound{
			Key: "k-ctl", Conv: domain.Conversation{Kind: domain.ConvGroup, ID: "cid"}, Text: raw, Markdown: markdown,
		}); err != nil {
			t.Fatal(err)
		}
		param := tr.lastSend(t).MsgParam
		if markdown {
			var body struct {
				Title string `json:"title"`
				Text  string `json:"text"`
			}
			if err := json.Unmarshal([]byte(param), &body); err != nil {
				t.Fatalf("markdown param is not valid JSON: %v (%q)", err, param)
			}
			if stripMarker(body.Text) != raw {
				t.Fatalf("markdown body = %q, want %q", stripMarker(body.Text), raw)
			}
			continue
		}
		var body struct {
			Content string `json:"content"`
		}
		if err := json.Unmarshal([]byte(param), &body); err != nil {
			t.Fatalf("text param is not valid JSON: %v (%q)", err, param)
		}
		if stripMarker(body.Content) != raw {
			t.Fatalf("content = %q, want %q", stripMarker(body.Content), raw)
		}
	}
}

// Stream 重连会重放最后一帧：同一条消息 id 只能交付一次。
func TestPollDedupesReplayedMessages(t *testing.T) {
	ev := helperEvent()
	tr := &fakeTransport{pages: []EventsPage{{Events: []Event{ev}, Cursor: "c1"}, {Events: []Event{ev}, Cursor: "c2"}}}
	d := newDriver(t, tr)
	first, next, err := d.Poll(context.Background(), "")
	if err != nil || len(first) != 1 || next != "c1" {
		t.Fatalf("first poll = %d events, cursor %q, %v", len(first), next, err)
	}
	second, _, err := d.Poll(context.Background(), next)
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != 0 {
		t.Fatalf("replayed message delivered twice: %d events", len(second))
	}
	if d.Unreadable() != 0 {
		t.Fatalf("unreadable = %d, want 0", d.Unreadable())
	}
}

// HTTP 传输没有 Stream：Subscribe 必须立刻失败并说明原因，而不是永久退避重试。
func TestSubscribeReportsUnsupportedStream(t *testing.T) {
	d := newDriver(t, &fakeTransport{})
	out := make(chan im.RawEvent, 1)
	done := make(chan error, 1)
	go func() { done <- d.Subscribe(context.Background(), out) }()
	select {
	case err := <-done:
		if !errors.Is(err, im.ErrUnsupported) {
			t.Fatalf("err = %v, want ErrUnsupported", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Subscribe did not fail fast for an unsupported stream")
	}
	if state := d.Health(context.Background()).State; state != im.HealthError {
		t.Fatalf("health = %q, want error", state)
	}
}

// 真 Stream 传输（声明 SupportsStream）走同一个长轮询循环：交付事件、ctx 取消即停。
func TestSubscribeDeliversStreamFrames(t *testing.T) {
	tr := &fakeTransport{streamOK: true, pages: []EventsPage{{Events: []Event{helperEvent()}, Cursor: "c1"}}}
	d := newDriver(t, tr)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out := make(chan im.RawEvent, 4)
	done := make(chan error, 1)
	go func() { done <- d.Subscribe(ctx, out) }()
	select {
	case ev := <-out:
		if ev.ID != "dt_1" {
			t.Fatalf("event id = %q", ev.ID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no event delivered")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Subscribe did not stop on cancel")
	}
}

// 成员分页不收敛时返回错误，而不是半份白名单。
func TestMembersPageLimitIsAnError(t *testing.T) {
	tr := &fakeTransport{}
	for i := 0; i < 25; i++ {
		tr.members = append(tr.members, MembersPage{
			Items:     []MemberItem{{ID: fmt.Sprintf("u%d", i), Name: "x"}},
			PageToken: fmt.Sprintf("p%d", i),
			HasMore:   true,
		})
	}
	d := newDriver(t, tr)
	_, err := d.Members(context.Background(), "cid")
	if err == nil || !strings.Contains(err.Error(), "exceeded") {
		t.Fatalf("err = %v, want a page-limit error", err)
	}
}

// 下面两个 helper 只被测试使用（解析/剥离不可见 marker），所以放在测试文件里，
// 不占生产代码路径。
// markerHash 从文本尾部解出 marker 的哈希（十六进制 16 位）；没有 marker 时
// 返回 ok=false。用于审计与测试，不参与发送去重。
func markerHash(text string) (string, bool) {
	runes := []rune(text)
	index := -1
	for i := len(runes) - 1; i >= 0; i-- {
		if runes[i] == markerStart {
			index = i
			break
		}
	}
	if index < 0 {
		return "", false
	}
	tail := runes[index+1:]
	if len(tail) != 64 {
		return "", false
	}
	runes = tail
	var bits [8]byte
	for i, r := range runes {
		if r != '\u200b' && r != '\u200c' {
			return "", false
		}
		if r == '\u200c' {
			bits[i/8] |= 1 << uint(7-i%8)
		}
	}
	return hex.EncodeToString(bits[:]), true
}

// stripMarker 去掉不可见 marker，返回人类可读的正文。
func stripMarker(text string) string {
	runes := []rune(text)
	for i := len(runes) - 1; i >= 0; i-- {
		if runes[i] == markerStart {
			return string(runes[:i])
		}
	}
	return text
}

// 生命周期：Close 幂等，且关闭后 Subscribe 诚实返回 im.ErrClosed（不空转）。
func TestDriverCloseIsGracefulAndIdempotent(t *testing.T) {
	d := NewWithTransport(Config{Now: time.Now}, &fakeTransport{})
	if err := d.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := d.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := d.Subscribe(ctx, make(chan im.RawEvent, 1))
	if !errors.Is(err, im.ErrClosed) {
		t.Fatalf("Subscribe after Close = %v, want im.ErrClosed", err)
	}
}
