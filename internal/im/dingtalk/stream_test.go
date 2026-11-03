package dingtalk

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/open-dingtalk/dingtalk-stream-sdk-go/chatbot"

	"github.com/zeokeel/super-agent/internal/im"
)

// fakeStreamClient 是 SDK 客户端替身：记录注册/启动/关闭，并允许测试直接喂
// chatbot 回调。它不打开任何网络连接，也不需要凭据。
type fakeStreamClient struct {
	mu       sync.Mutex
	handler  func(context.Context, *chatbot.BotCallbackDataModel) ([]byte, error)
	startErr []error // 依次返回的 Start 错误；空表示第一次就成功
	starts   int32
	closes   int32
	started  chan struct{}
	once     sync.Once
}

func newFakeStreamClient(startErrs ...error) *fakeStreamClient {
	return &fakeStreamClient{startErr: startErrs, started: make(chan struct{})}
}

func (f *fakeStreamClient) RegisterChatBotCallbackRouter(handler func(context.Context, *chatbot.BotCallbackDataModel) ([]byte, error)) {
	f.mu.Lock()
	f.handler = handler
	f.mu.Unlock()
}

func (f *fakeStreamClient) Start(context.Context) error {
	atomic.AddInt32(&f.starts, 1)
	f.mu.Lock()
	var err error
	if len(f.startErr) > 0 {
		err = f.startErr[0]
		f.startErr = f.startErr[1:]
	}
	f.mu.Unlock()
	if err != nil {
		return err
	}
	f.once.Do(func() { close(f.started) })
	return nil
}

func (f *fakeStreamClient) Close() { atomic.AddInt32(&f.closes, 1) }

func (f *fakeStreamClient) startCount() int { return int(atomic.LoadInt32(&f.starts)) }

func (f *fakeStreamClient) closeCount() int { return int(atomic.LoadInt32(&f.closes)) }

// deliver 调一次已注册的机器人回调（模拟 SDK 收到一帧）。
func (f *fakeStreamClient) deliver(t *testing.T, data *chatbot.BotCallbackDataModel) {
	t.Helper()
	f.mu.Lock()
	handler := f.handler
	f.mu.Unlock()
	if handler == nil {
		t.Fatal("callback router was not registered")
	}
	if _, err := handler(context.Background(), data); err != nil {
		t.Fatalf("callback returned error: %v", err)
	}
}

func (f *fakeStreamClient) waitStarted(t *testing.T) {
	t.Helper()
	select {
	case <-f.started:
	case <-time.After(5 * time.Second):
		t.Fatal("fake stream client never started")
	}
}

// textCallback 构造一条文本机器人回调（群聊，@ 机器人）。
func textCallback(msgID, convID, content string) *chatbot.BotCallbackDataModel {
	return &chatbot.BotCallbackDataModel{
		MsgId:             msgID,
		ConversationId:    convID,
		ConversationType:  "2",
		ConversationTitle: "研发群",
		Msgtype:           "text",
		Text:              chatbot.BotCallbackDataTextModel{Content: content},
		SenderStaffId:     "staff_alice",
		SenderId:          "sender_alice",
		SenderNick:        "张三",
		CreateAt:          1_700_000_000_000,
		IsInAtList:        true,
		AtUsers:           []chatbot.BotCallbackDataAtUserModel{{DingtalkId: "ding_robot", StaffId: "staff_robot"}},
	}
}

func newTestStreamTransport(t *testing.T, fake *fakeStreamClient) *StreamTransport {
	t.Helper()
	httpTr := NewHTTPTransport("http://127.0.0.1:1", "app-key", "app-secret", nil)
	tr := NewStreamTransportWithClient(httpTr, StreamConfig{
		Backoff:    time.Millisecond,
		MaxBackoff: 5 * time.Millisecond,
	}, fake)
	t.Cleanup(func() { _ = tr.Close() })
	return tr
}

// 群聊/单聊文本回调 → dingtalk.Event 的事实（id/会话/发送者/@/时间/正文）。
func TestStreamEventsMapsGroupAndDirectMessages(t *testing.T) {
	fake := newFakeStreamClient()
	tr := newTestStreamTransport(t, fake)
	tr.start()
	fake.waitStarted(t)

	group := textCallback("msg_group", "cid_group", "@ZeoKeel 帮我看下")
	fake.deliver(t, group)
	direct := textCallback("msg_dm", "dm_staff_alice", "私聊一句")
	direct.ConversationType = "1"
	fake.deliver(t, direct)

	page, err := tr.StreamEvents(context.Background(), "")
	if err != nil {
		t.Fatalf("StreamEvents: %v", err)
	}
	if len(page.Events) != 2 {
		t.Fatalf("events = %d, want 2", len(page.Events))
	}
	ev := page.Events[0]
	if ev.MsgID != "msg_group" || ev.ConversationID != "cid_group" || ev.ConversationType != "2" {
		t.Fatalf("group event = %+v", ev)
	}
	if ev.SenderStaffID != "staff_alice" || ev.SenderNick != "张三" || ev.CreateAt != 1_700_000_000_000 {
		t.Fatalf("group sender facts = %+v", ev)
	}
	if !ev.IsInAtList || len(ev.AtUsers) != 1 ||
		ev.AtUsers[0].DingTalkID != "ding_robot" || ev.AtUsers[0].StaffID != "staff_robot" {
		t.Fatalf("group mentions = %+v", ev.AtUsers)
	}
	var text struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal([]byte(ev.Content), &text); err != nil || text.Content != "@ZeoKeel 帮我看下" {
		t.Fatalf("group content = %q (%v)", ev.Content, err)
	}
	if page.Events[1].ConversationType != "1" || page.Events[1].MsgID != "msg_dm" {
		t.Fatalf("direct event = %+v", page.Events[1])
	}
}

// 非文本回调（图片/文件等）把 Content 原样 JSON 化，mapping 再按 msgtype 解释。
func TestStreamEventsMapsNonTextContent(t *testing.T) {
	fake := newFakeStreamClient()
	tr := newTestStreamTransport(t, fake)
	tr.start()
	fake.waitStarted(t)

	cb := textCallback("msg_pic", "cid_group", "")
	cb.Msgtype = "picture"
	cb.Content = map[string]string{"downloadCode": "dl_1"}
	fake.deliver(t, cb)

	page, err := tr.StreamEvents(context.Background(), "")
	if err != nil || len(page.Events) != 1 {
		t.Fatalf("StreamEvents = %+v, %v", page, err)
	}
	if !strings.Contains(page.Events[0].Content, `"downloadCode":"dl_1"`) {
		t.Fatalf("content = %q", page.Events[0].Content)
	}
}

// 坏回调（缺 id/会话/时间、超长 id、超大 content）不能进队列，计数后 ack。
func TestStreamCallbackRejectsMalformedAndOversizedPayloads(t *testing.T) {
	cases := map[string]func(*chatbot.BotCallbackDataModel){
		"nil":            nil,
		"no msg id":      func(c *chatbot.BotCallbackDataModel) { c.MsgId = "" },
		"no conv id":     func(c *chatbot.BotCallbackDataModel) { c.ConversationId = "" },
		"no create time": func(c *chatbot.BotCallbackDataModel) { c.CreateAt = 0 },
		"long msg id":    func(c *chatbot.BotCallbackDataModel) { c.MsgId = strings.Repeat("m", maxStreamIDs+1) },
		"oversized content": func(c *chatbot.BotCallbackDataModel) {
			c.Msgtype = "picture"
			c.Content = map[string]string{"downloadCode": strings.Repeat("x", maxStreamContentBytes+1)}
		},
	}
	for name, mutate := range cases {
		var data *chatbot.BotCallbackDataModel
		if mutate != nil {
			data = textCallback("m1", "c1", "hi")
			mutate(data)
		}
		if _, err := messageFromCallback(data); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}

	// 回调层：坏消息返回空 ack（让钉钉停止重投），并计入 Dropped。
	fake := newFakeStreamClient()
	tr := newTestStreamTransport(t, fake)
	tr.start()
	fake.waitStarted(t)
	bad := textCallback("", "c1", "hi")
	if _, err := fake.handlerForTest()(context.Background(), bad); err != nil {
		t.Fatalf("callback must ack malformed payloads: %v", err)
	}
	if tr.Dropped() != 1 {
		t.Fatalf("dropped = %d, want 1", tr.Dropped())
	}
}

// 建连失败要退避重试，并把错误交给轮询循环（health 才能变红）；恢复后照常收。
func TestStreamTransportRetriesStartAndReportsErrors(t *testing.T) {
	fake := newFakeStreamClient(errors.New("dial 1"), errors.New("dial 2"))
	tr := newTestStreamTransport(t, fake)
	var mu sync.Mutex
	var states []im.HealthState
	tr.SetStatus(func(state im.HealthState, detail string) {
		mu.Lock()
		states = append(states, state)
		mu.Unlock()
	})
	tr.start()

	// 第一次 StreamEvents 会拿到 Startup 错误（不伪装成"没有消息"）。
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := tr.StreamEvents(ctx, ""); err == nil {
		t.Fatal("expected a start error")
	}
	// 后面的重试最终成功：等状态回调看到"已连接"，再喂一条消息。等通知而不是
	// 等 Start 返回，是为了确保成功路径已经清掉旧的启动错误。
	fake.waitStarted(t)
	waitFor(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(states) > 0 && states[len(states)-1] == im.HealthStarting
	}, "stream never reported a connected state")
	fake.deliver(t, textCallback("msg_after_retry", "cid", "hi"))
	page, err := tr.StreamEvents(context.Background(), "")
	if err != nil || len(page.Events) != 1 || page.Events[0].MsgID != "msg_after_retry" {
		t.Fatalf("after retry: %+v, %v", page, err)
	}
	if fake.startCount() < 3 {
		t.Fatalf("starts = %d, want >= 3 (two failures then success)", fake.startCount())
	}
	mu.Lock()
	defer mu.Unlock()
	sawError := false
	for _, state := range states {
		if state == im.HealthError {
			sawError = true
		}
	}
	if !sawError {
		t.Fatalf("status states = %v, want at least one error", states)
	}
}

// ctx 取消立刻返回（不把 StreamEvents 卡死），并关闭底层客户端。
func TestStreamEventsStopsOnContextCancel(t *testing.T) {
	fake := newFakeStreamClient()
	tr := newTestStreamTransport(t, fake)
	tr.start()
	fake.waitStarted(t)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := tr.StreamEvents(ctx, "")
		done <- err
	}()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("StreamEvents ignored context cancellation")
	}
	if err := tr.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	waitFor(t, func() bool { return fake.closeCount() >= 1 }, "fake client was not closed")
}

// 端到端接线（复刻 runtime 的调用）：New(Config{AppKey,AppSecret,RobotCode,BotName})
// 默认启用 Stream；Subscribe 收到 Stream 消息 → RawEvent，重复 MsgID 只交付一次。
func TestSubscribeDeliversStreamEventsAndDedupes(t *testing.T) {
	fake := newFakeStreamClient()
	d, err := New(Config{
		RobotCode:    "ding_robot",
		BotName:      "ZeoKeel",
		AppKey:       "app-key",
		AppSecret:    "app-secret",
		Now:          func() time.Time { return time.UnixMilli(1_700_000_100_000) },
		Retry:        im.RetryPolicy{Base: time.Millisecond},
		IdleDelay:    time.Millisecond,
		StreamClient: fake,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, ok := d.tr.(*StreamTransport); !ok {
		t.Fatalf("New with credentials must use the Stream transport, got %T", d.tr)
	}

	ctx, cancel := context.WithCancel(context.Background())
	out := make(chan im.RawEvent, 8)
	subErr := make(chan error, 1)
	go func() { subErr <- d.Subscribe(ctx, out) }()
	fake.waitStarted(t)

	cb := textCallback("msg_1", "cid_group", "@ZeoKeel 帮我看看")
	fake.deliver(t, cb)
	fake.deliver(t, cb) // 重连重放：同一个 MsgID 只能交付一次

	select {
	case ev := <-out:
		if ev.ID != "msg_1" || ev.Conv.ID != "cid_group" || ev.Text == "" {
			t.Fatalf("raw event = %+v", ev)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no RawEvent delivered from the stream")
	}
	select {
	case dup := <-out:
		t.Fatalf("duplicate MsgID delivered again: %+v", dup)
	case <-time.After(100 * time.Millisecond):
	}

	waitFor(t, func() bool { return d.Health(context.Background()).State == im.HealthReady }, "health never turned ready")
	cancel()
	select {
	case err := <-subErr:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Subscribe err = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Subscribe did not stop on cancel")
	}
	waitFor(t, func() bool { return fake.closeCount() >= 1 }, "stream client not closed on subscribe exit")
}

// 没有 Stream（NoStream 或 HTTP-only 注入）时 Subscribe 必须诚实报不支持。
func TestSubscribeWithoutStreamIsHonest(t *testing.T) {
	d, err := New(Config{
		RobotCode: "ding_robot", AppKey: "app-key", AppSecret: "app-secret",
		NoStream: true, Now: func() time.Time { return time.UnixMilli(1_700_000_100_000) },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, ok := d.tr.(*StreamTransport); ok {
		t.Fatal("NoStream must not build the Stream transport")
	}
	done := make(chan error, 1)
	go func() { done <- d.Subscribe(context.Background(), make(chan im.RawEvent, 1)) }()
	select {
	case err := <-done:
		if !errors.Is(err, im.ErrUnsupported) {
			t.Fatalf("err = %v, want ErrUnsupported", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Subscribe did not fail fast without a stream")
	}
	if state := d.Health(context.Background()).State; state != im.HealthError {
		t.Fatalf("health = %q, want error", state)
	}
}

// 未知消息类型不阻塞接收：交付 Unsupported 的 RawEvent，由上层决定不建单。
func TestSubscribeDeliversUnsupportedEventForUnknownType(t *testing.T) {
	fake := newFakeStreamClient()
	tr := newTestStreamTransport(t, fake)
	d := NewWithTransport(Config{
		RobotCode: "ding_robot",
		Now:       func() time.Time { return time.UnixMilli(1_700_000_100_000) },
		Retry:     im.RetryPolicy{Base: time.Millisecond},
		IdleDelay: time.Millisecond,
	}, tr)
	tr.SetStatus(d.setStreamStatus)
	ctx, cancel := context.WithCancel(context.Background())
	out := make(chan im.RawEvent, 8)
	go func() { _ = d.Subscribe(ctx, out) }()
	fake.waitStarted(t)

	cb := textCallback("msg_sticker", "cid", "")
	cb.Msgtype = "sticker"
	cb.Content = map[string]string{"unknown": "x"}
	fake.deliver(t, cb)
	select {
	case ev := <-out:
		if !ev.Unsupported || ev.ID != "msg_sticker" {
			t.Fatalf("event = %+v, want Unsupported sticker", ev)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("unsupported message was dropped instead of delivered")
	}
	cancel()
}

// handlerForTest 返回已注册的回调，便于直接测 ack/丢弃路径。
func (f *fakeStreamClient) handlerForTest() func(context.Context, *chatbot.BotCallbackDataModel) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.handler
}

func waitFor(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal(msg)
}
