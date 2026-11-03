package dingtalk

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zeokeel/super-agent/internal/domain"
	"github.com/zeokeel/super-agent/internal/im"
)

// fakeDWSClient 是 DWSClient 的测试替身：记录调用、检测并发重叠、按脚本返回
// 结果。所有测试都不需要真的安装 dws。
type fakeDWSClient struct {
	mu sync.Mutex

	auth     AuthStatus
	authErr  error
	poll     func(cursor string, limit int) (PollPage, error)
	send     func(conv, text, key string) (string, error)
	health   DWSHealth
	healthFn func() (DWSHealth, error)

	active    int32
	maxActive int32
	overlap   int32 // >0 表示同时出现过两个调用
	delay     time.Duration

	pollCursors []string
	sends       []sendRecord
	authCalls   int32
	healthCalls int32
}

type sendRecord struct {
	ConversationID string
	Text           string
	Key            string
}

func (f *fakeDWSClient) enter() {
	if f.delay > 0 {
		time.Sleep(f.delay)
	}
	if atomic.AddInt32(&f.active, 1) > 1 {
		atomic.StoreInt32(&f.overlap, 1)
	}
	for {
		cur := atomic.LoadInt32(&f.maxActive)
		if n := atomic.LoadInt32(&f.active); n > cur {
			if atomic.CompareAndSwapInt32(&f.maxActive, cur, n) {
				break
			}
			continue
		}
		break
	}
}

func (f *fakeDWSClient) leave() { atomic.AddInt32(&f.active, -1) }

func (f *fakeDWSClient) AuthStatus(context.Context) (AuthStatus, error) {
	atomic.AddInt32(&f.authCalls, 1)
	f.enter()
	defer f.leave()
	if f.authErr != nil {
		return AuthStatus{}, f.authErr
	}
	return f.auth, nil
}

func (f *fakeDWSClient) Poll(_ context.Context, cursor string, limit int) (PollPage, error) {
	f.mu.Lock()
	f.pollCursors = append(f.pollCursors, cursor)
	fn := f.poll
	f.mu.Unlock()
	f.enter()
	defer f.leave()
	if fn == nil {
		return PollPage{Cursor: cursor}, nil
	}
	return fn(cursor, limit)
}

func (f *fakeDWSClient) Send(_ context.Context, conv, text, key string) (string, error) {
	f.mu.Lock()
	f.sends = append(f.sends, sendRecord{ConversationID: conv, Text: text, Key: key})
	fn := f.send
	f.mu.Unlock()
	f.enter()
	defer f.leave()
	if fn == nil {
		return "dw_msg", nil
	}
	return fn(conv, text, key)
}

func (f *fakeDWSClient) Health(context.Context) (DWSHealth, error) {
	atomic.AddInt32(&f.healthCalls, 1)
	f.enter()
	defer f.leave()
	if f.healthFn != nil {
		return f.healthFn()
	}
	return f.health, nil
}

func (f *fakeDWSClient) cursors() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.pollCursors...)
}

func (f *fakeDWSClient) sendRecords() []sendRecord {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]sendRecord(nil), f.sends...)
}

func (f *fakeDWSClient) maxConcurrent() int { return int(atomic.LoadInt32(&f.maxActive)) }

func (f *fakeDWSClient) sawOverlap() bool { return atomic.LoadInt32(&f.overlap) == 1 }

func newTestDWSTransport(t *testing.T, client DWSClient, cfg DWSConfig) *DWSTransport {
	t.Helper()
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = time.Millisecond
	}
	if cfg.Backoff <= 0 {
		cfg.Backoff = time.Millisecond
	}
	if cfg.MaxBackoff <= 0 {
		cfg.MaxBackoff = 5 * time.Millisecond
	}
	tr, err := NewDWSTransport(nil, client, cfg)
	if err != nil {
		t.Fatalf("NewDWSTransport: %v", err)
	}
	t.Cleanup(func() { _ = tr.Close() })
	return tr
}

// dwsMessage 构造一条合法 DWS 消息。
func dwsMessage(id, convID, convType, text, at string, mentions ...string) DWSMessage {
	when, _ := time.Parse(time.RFC3339, at)
	return DWSMessage{
		ID:               id,
		ConversationID:   convID,
		ConversationType: convType,
		ConversationName: "研发群",
		SenderID:         "u1",
		SenderName:       "张三",
		Text:             text,
		At:               when,
		Mentions:         mentions,
	}
}

// 群聊/单聊消息映射成驱动层 Event，游标原样回传。
func TestStreamEventsMapsDWSMessagesAndCursor(t *testing.T) {
	fake := &fakeDWSClient{}
	fake.poll = func(cursor string, limit int) (PollPage, error) {
		if limit != defaultDWSPollLimit {
			t.Errorf("limit = %d, want %d", limit, defaultDWSPollLimit)
		}
		return PollPage{
			Cursor: "c1",
			Messages: []DWSMessage{
				dwsMessage("m1", "cid_group", "group", "@bot 帮我看下", "2026-11-03T10:00:00+08:00", "bot"),
				dwsMessage("m2", "cid_dm", "direct", "私聊一句", "2026-11-03T10:01:00+08:00"),
			},
		}, nil
	}
	tr := newTestDWSTransport(t, fake, DWSConfig{})

	page, err := tr.StreamEvents(context.Background(), "")
	if err != nil {
		t.Fatalf("StreamEvents: %v", err)
	}
	if page.Cursor != "c1" || len(page.Events) != 2 {
		t.Fatalf("page = %+v", page)
	}
	group := page.Events[0]
	if group.MsgID != "m1" || group.ConversationID != "cid_group" || group.ConversationType != "2" {
		t.Fatalf("group event = %+v", group)
	}
	if len(group.AtUsers) != 1 || group.AtUsers[0].DingTalkID != "bot" || !group.IsInAtList {
		t.Fatalf("mentions = %+v", group.AtUsers)
	}
	var body struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal([]byte(group.Content), &body); err != nil || body.Content != "@bot 帮我看下" {
		t.Fatalf("content = %q (%v)", group.Content, err)
	}
	if group.CreateAt != time.Date(2026, 11, 3, 10, 0, 0, 0, time.FixedZone("CST", 8*3600)).UnixMilli() {
		t.Fatalf("CreateAt = %d", group.CreateAt)
	}
	if page.Events[1].ConversationType != "1" {
		t.Fatalf("direct conv type = %q", page.Events[1].ConversationType)
	}
	if got := fake.cursors(); len(got) != 1 || got[0] != "" {
		t.Fatalf("poll cursors = %v, want [\"\"]", got)
	}
	// 下一轮把上一轮的游标传回去。
	if _, err := tr.StreamEvents(context.Background(), page.Cursor); err != nil {
		t.Fatal(err)
	}
	if got := fake.cursors(); len(got) != 2 || got[1] != "c1" {
		t.Fatalf("poll cursors = %v", got)
	}
}

// 重放/重复消息按 id 去重（有界集合），并计数。
func TestStreamEventsDedupesReplayedMessages(t *testing.T) {
	fake := &fakeDWSClient{}
	pages := []PollPage{
		{Cursor: "c1", Messages: []DWSMessage{dwsMessage("m1", "cid", "group", "hi", "2026-11-03T10:00:00Z")}},
		{Cursor: "c2", Messages: []DWSMessage{
			dwsMessage("m1", "cid", "group", "hi", "2026-11-03T10:00:00Z"),
			dwsMessage("m2", "cid", "group", "second", "2026-11-03T10:01:00Z"),
		}},
	}
	fake.poll = func(string, int) (PollPage, error) {
		page := pages[0]
		pages = pages[1:]
		return page, nil
	}
	tr := newTestDWSTransport(t, fake, DWSConfig{})
	if page, _ := tr.StreamEvents(context.Background(), ""); len(page.Events) != 1 {
		t.Fatalf("first page events = %d", len(page.Events))
	}
	page, err := tr.StreamEvents(context.Background(), "c1")
	if err != nil || len(page.Events) != 1 || page.Events[0].MsgID != "m2" {
		t.Fatalf("second page = %+v, %v", page, err)
	}
	if tr.Duplicates() != 1 {
		t.Fatalf("duplicates = %d, want 1", tr.Duplicates())
	}
	if tr.Dropped() != 0 {
		t.Fatalf("dropped = %d, want 0", tr.Dropped())
	}
}

// 未登录：poll 返回 401 → health auth + authUrl，错误可被 runtime 识别。
func TestStreamEventsReportsAuthState(t *testing.T) {
	fake := &fakeDWSClient{
		auth: AuthStatus{LoggedIn: false, AuthURL: "https://login.example/x"},
		poll: func(string, int) (PollPage, error) {
			return PollPage{}, &DWSError{Op: "poll", Status: 401, Permanent: true, AuthURL: "https://login.example/x"}
		},
		healthFn: func() (DWSHealth, error) { return DWSHealth{}, ErrDWSUndefined },
	}
	tr := newTestDWSTransport(t, fake, DWSConfig{})
	var states []im.HealthState
	tr.SetStatus(func(state im.HealthState, detail string) {
		states = append(states, state)
		if state != im.HealthError || !containsAll(detail, "dws 未登录", "https://login.example/x") {
			t.Errorf("status = %v %q, want error with login URL", state, detail)
		}
	})
	_, err := tr.StreamEvents(context.Background(), "")
	if err == nil {
		t.Fatal("expected auth error")
	}
	var de *DWSError
	if !errors.As(err, &de) || de.Status != 401 || de.AuthURL == "" {
		t.Fatalf("err = %v", err)
	}
	if len(states) == 0 {
		t.Fatal("status callback was not called")
	}
	h, herr := tr.DWSHealth(context.Background())
	if herr != nil {
		t.Fatalf("DWSHealth: %v", herr)
	}
	if h.State != "auth" || h.AuthURL != "https://login.example/x" || h.LoggedIn {
		t.Fatalf("DWSHealth = %+v", h)
	}
	if !containsAll(h.Detail, "dws 未登录", "https://login.example/x") {
		t.Fatalf("detail = %q", h.Detail)
	}
}

// 失败退避 1s→30s 的本地版本：每两次 poll 之间有等待，成功后重置。
func TestStreamEventsBackoffBetweenFailures(t *testing.T) {
	var attempts int32
	fake := &fakeDWSClient{}
	fake.poll = func(string, int) (PollPage, error) {
		if atomic.AddInt32(&attempts, 1) < 3 {
			return PollPage{}, errors.New("gateway hiccup")
		}
		return PollPage{Cursor: "c9", Messages: []DWSMessage{
			dwsMessage("m9", "cid", "group", "ok", "2026-11-03T10:00:00Z"),
		}}, nil
	}
	tr := newTestDWSTransport(t, fake, DWSConfig{Backoff: 20 * time.Millisecond, MaxBackoff: 40 * time.Millisecond})

	start := time.Now()
	if _, err := tr.StreamEvents(context.Background(), ""); err == nil {
		t.Fatal("expected first failure")
	}
	if _, err := tr.StreamEvents(context.Background(), ""); err == nil {
		t.Fatal("expected second failure")
	}
	elapsed := time.Since(start)
	if elapsed < 25*time.Millisecond {
		t.Fatalf("retries were not backed off (elapsed %v)", elapsed)
	}
	if h := tr.Health(); h.State != "error" {
		t.Fatalf("health after failures = %+v", h)
	}
	if _, err := tr.StreamEvents(context.Background(), ""); err != nil {
		t.Fatalf("third poll: %v", err)
	}
	if h := tr.Health(); h.State != "ok" {
		t.Fatalf("health after success = %+v", h)
	}
}

// 串行：poll/send/auth/health 并发调用时，同一时刻只有一个客户端方法在跑。
func TestDWSTransportSerializesEveryClientCall(t *testing.T) {
	fake := &fakeDWSClient{auth: AuthStatus{LoggedIn: true, Account: "a"}, health: DWSHealth{State: "ok"}, delay: 5 * time.Millisecond}
	fake.poll = func(_ string, _ int) (PollPage, error) {
		return PollPage{Cursor: "c1", Messages: []DWSMessage{
			dwsMessage("m1", "cid", "group", "hi", "2026-11-03T10:00:00Z"),
		}}, nil
	}
	fake.send = func(string, string, string) (string, error) { return "id1", nil }
	tr := newTestDWSTransport(t, fake, DWSConfig{PollInterval: time.Millisecond})

	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = tr.StreamEvents(context.Background(), "")
		}()
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = tr.SendText(context.Background(), "cid", "hello", "k1")
		}()
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = tr.AuthStatus(context.Background())
		}()
	}
	wg.Wait()
	if fake.sawOverlap() || fake.maxConcurrent() != 1 {
		t.Fatalf("calls overlapped: maxConcurrent=%d overlap=%v", fake.maxConcurrent(), fake.sawOverlap())
	}
}

// 排队的调用在 ctx 取消时必须立刻返回：runner 不能在持锁状态下等一个
// 不理会 ctx 的通道，也不能让已取消的 job 继续执行。
func TestDWSRunnerCancelledJobReturnsImmediatelyAndNeverRuns(t *testing.T) {
	r := newDWSRunner(time.Second)
	defer r.Stop()
	release := make(chan struct{})
	started := make(chan struct{})
	go func() {
		_, _ = r.Do(context.Background(), dwsPriorityPoll, func(context.Context) (any, error) {
			close(started)
			<-release // 模拟一个不理会 ctx 的长调用
			return nil, nil
		})
	}()
	<-started

	ran := make(chan struct{}, 1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 入队前就取消
	start := time.Now()
	_, err := r.Do(ctx, dwsPriorityPoll, func(context.Context) (any, error) {
		ran <- struct{}{}
		return nil, nil
	})
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("cancelled queued call blocked for %v", elapsed)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	close(release)
	time.Sleep(20 * time.Millisecond)
	select {
	case <-ran:
		t.Fatal("a cancelled job was executed")
	default:
	}
}

// runner 的优先级：send 必须排在已排队的 poll 前面（登录态/回执优先）。
func TestDWSRunnerSendsBeforeQueuedPolls(t *testing.T) {
	r := newDWSRunner(time.Second)
	defer r.Stop()
	release := make(chan struct{})
	var mu sync.Mutex
	var order []string
	started := make(chan struct{}, 4)
	blockerDone := make(chan struct{})

	go func() {
		_, _ = r.Do(context.Background(), dwsPriorityPoll, func(context.Context) (any, error) {
			close(started)
			<-release
			close(blockerDone)
			return nil, nil
		})
	}()
	<-started

	record := func(name string) func(context.Context) (any, error) {
		return func(context.Context) (any, error) {
			mu.Lock()
			order = append(order, name)
			mu.Unlock()
			return nil, nil
		}
	}
	poll1 := make(chan struct{})
	go func() {
		_, _ = r.Do(context.Background(), dwsPriorityPoll, record("poll1"))
		close(poll1)
	}()
	time.Sleep(5 * time.Millisecond) // 让 poll1 先入队
	sendDone := make(chan struct{})
	go func() {
		_, _ = r.Do(context.Background(), dwsPrioritySend, record("send"))
		close(sendDone)
	}()
	time.Sleep(5 * time.Millisecond) // 让 send 入队
	close(release)
	<-blockerDone
	<-sendDone
	<-poll1
	mu.Lock()
	defer mu.Unlock()
	if len(order) != 2 || order[0] != "send" || order[1] != "poll1" {
		t.Fatalf("execution order = %v, want [send poll1]", order)
	}
}

// SendText 原样转发 conversation/text/key（marker 由 command client 负责）。
func TestDWSTransportSendTextForwardsKey(t *testing.T) {
	fake := &fakeDWSClient{send: func(conv, text, key string) (string, error) {
		return "dw_1", nil
	}}
	tr := newTestDWSTransport(t, fake, DWSConfig{})
	id, err := tr.SendText(context.Background(), "cid_x", "hello", "k-42")
	if err != nil || id != "dw_1" {
		t.Fatalf("SendText = %q, %v", id, err)
	}
	records := fake.sendRecords()
	if len(records) != 1 || records[0].ConversationID != "cid_x" || records[0].Key != "k-42" || records[0].Text != "hello" {
		t.Fatalf("send records = %+v", records)
	}
	if _, err := tr.SendText(context.Background(), "cid", "hi", ""); err == nil || !IsPermanent(err) {
		t.Fatalf("empty idempotency key err = %v, want permanent", err)
	}
}

// DWSHealth：health 命令可用时用它；未实现时退回 auth status。
func TestDWSTransportHealthFallsBackToAuth(t *testing.T) {
	fake := &fakeDWSClient{
		healthFn: func() (DWSHealth, error) { return DWSHealth{}, ErrDWSUndefined },
		auth:     AuthStatus{LoggedIn: true, Account: "robot@corp"},
	}
	tr := newTestDWSTransport(t, fake, DWSConfig{})
	h, err := tr.DWSHealth(context.Background())
	if err != nil || h.State != "ok" || h.Account != "robot@corp" || !h.LoggedIn {
		t.Fatalf("health = %+v, %v", h, err)
	}

	fake2 := &fakeDWSClient{
		healthFn: func() (DWSHealth, error) { return DWSHealth{State: "error", Detail: "gateway down"}, nil },
	}
	tr2 := newTestDWSTransport(t, fake2, DWSConfig{})
	h2, err := tr2.DWSHealth(context.Background())
	if err != nil || h2.State != "error" || h2.Detail != "gateway down" {
		t.Fatalf("health2 = %+v, %v", h2, err)
	}
}

// Close 之后继续 poll 是编程错误：明确报 ErrDWSClosed。
func TestDWSTransportClosed(t *testing.T) {
	tr := newTestDWSTransport(t, &fakeDWSClient{}, DWSConfig{})
	if err := tr.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := tr.StreamEvents(context.Background(), ""); !errors.Is(err, ErrDWSClosed) {
		t.Fatalf("err = %v, want ErrDWSClosed", err)
	}
	// Close 必须可重复且不挂起。
	if err := tr.Close(); err != nil {
		t.Fatal(err)
	}
}

// --- driver 层集成 ---

func newDWSTestDriver(t *testing.T, client DWSClient, mutate func(*Config)) *Driver {
	t.Helper()
	cfg := Config{
		Transport:       "dws",
		DWSClient:       client,
		SelfID:          "bot-1",
		BotName:         "ZeoKeel",
		Now:             func() time.Time { return time.UnixMilli(1_700_000_100_000) },
		Retry:           im.RetryPolicy{Base: time.Millisecond},
		IdleDelay:       time.Millisecond,
		DWSPollInterval: time.Millisecond,
		DWSBackoff:      time.Millisecond,
		DWSMaxBackoff:   5 * time.Millisecond,
	}
	if mutate != nil {
		mutate(&cfg)
	}
	d, err := New(cfg)
	if err != nil {
		t.Fatalf("New(dws): %v", err)
	}
	return d
}

// runtime 的调用形态：New(Config{Transport:"dws", DWSClient, SelfID}) → 默认
// DWS 传输；SubscribeFrom 交付 RawEvent、把下一轮游标交给 save。
func TestDWSDriverSubscribesAndPersistsCursor(t *testing.T) {
	fake := &fakeDWSClient{}
	fake.poll = func(cursor string, _ int) (PollPage, error) {
		if cursor == "" {
			return PollPage{Cursor: "c1", Messages: []DWSMessage{
				dwsMessage("m1", "cid_group", "group", "@bot 帮我看下", "2026-11-03T10:00:00Z", "bot-1"),
			}}, nil
		}
		return PollPage{Cursor: cursor, Messages: nil}, nil
	}
	d := newDWSTestDriver(t, fake, nil)
	if _, ok := d.tr.(*DWSTransport); !ok {
		t.Fatalf("transport = %T, want *DWSTransport", d.tr)
	}

	ctx, cancel := context.WithCancel(context.Background())
	out := make(chan im.RawEvent, 8)
	var mu sync.Mutex
	var saved []string
	save := func(next string) error {
		mu.Lock()
		saved = append(saved, next)
		mu.Unlock()
		return nil
	}
	subErr := make(chan error, 1)
	go func() { subErr <- d.SubscribeFrom(ctx, "", save, out) }()

	var ev im.RawEvent
	select {
	case ev = <-out:
	case <-time.After(5 * time.Second):
		t.Fatal("no RawEvent from the DWS driver")
	}
	if ev.ID != "m1" || ev.Conv.ID != "cid_group" || ev.Conv.Kind != domain.ConvGroup {
		t.Fatalf("raw event = %+v", ev)
	}
	if len(ev.Mentions) != 1 || ev.Mentions[0] != "bot-1" {
		t.Fatalf("mentions = %v", ev.Mentions)
	}
	waitFor(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(saved) > 0
	}, "cursor was not persisted")
	mu.Lock()
	if saved[0] != "c1" {
		t.Fatalf("saved cursors = %v, want c1", saved)
	}
	mu.Unlock()

	// 重启：SubscribeFrom 用保存的游标继续，CLI 收到 c1 而不是空。
	cancel()
	select {
	case err := <-subErr:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("SubscribeFrom err = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("SubscribeFrom did not stop")
	}
	if got := fake.cursors(); len(got) < 2 || got[len(got)-1] != "c1" {
		t.Fatalf("poll cursors = %v, want a later poll with c1", got)
	}
}

// Send 走 DWS 的"文本 + 幂等键"，而不是 HTTP 模板参数；重复 key 命中进程内缓存。
func TestDWSDriverSendUsesTextSender(t *testing.T) {
	fake := &fakeDWSClient{send: func(string, string, string) (string, error) { return "dw_9", nil }}
	d := newDWSTestDriver(t, fake, nil)
	receipt, err := d.Send(context.Background(), im.Outbound{
		Key: "k-1", Conv: domain.Conversation{Kind: domain.ConvGroup, ID: "cid_group"}, Text: "收到",
	})
	if err != nil || receipt.MessageID != "dw_9" {
		t.Fatalf("Send = %+v, %v", receipt, err)
	}
	records := fake.sendRecords()
	if len(records) != 1 || records[0].ConversationID != "cid_group" || records[0].Text != "收到" || records[0].Key != "k-1" {
		t.Fatalf("send records = %+v", records)
	}
	// 同 key 重发：不再打 DWS。
	if _, err := d.Send(context.Background(), im.Outbound{
		Key: "k-1", Conv: domain.Conversation{Kind: domain.ConvGroup, ID: "cid_group"}, Text: "收到",
	}); err != nil {
		t.Fatal(err)
	}
	if got := len(fake.sendRecords()); got != 1 {
		t.Fatalf("DWS send called %d times, want 1 (in-process dedupe)", got)
	}
}

// bot 发送者与 replyToBot 的事实来自 DWS，不是 staffId 启发式。
func TestDWSDriverPollCarriesBotAndReplyFacts(t *testing.T) {
	fake := &fakeDWSClient{}
	fake.poll = func(string, int) (PollPage, error) {
		msg := dwsMessage("m1", "cid", "group", "部署完成", "2026-11-03T10:00:00Z")
		msg.SenderBot = true
		msg.ReplyToBot = true
		return PollPage{Cursor: "c1", Messages: []DWSMessage{msg}}, nil
	}
	d := NewWithTransport(Config{
		Transport: "dws", SelfID: "bot-1",
		Now: func() time.Time { return time.UnixMilli(1_700_000_100_000) },
	}, newTestDWSTransport(t, fake, DWSConfig{}))
	events, next, err := d.Poll(context.Background(), "")
	if err != nil || len(events) != 1 || next != "c1" {
		t.Fatalf("Poll = %+v, %q, %v", events, next, err)
	}
	if !events[0].Actor.Bot || events[0].Actor.ID != "u1" {
		t.Fatalf("actor = %+v, want bot u1", events[0].Actor)
	}
	if !events[0].ReplyToBot {
		t.Fatal("replyToBot fact was lost")
	}
}

// 未知 transport 与 NoDWS 的诚实失败。
func TestDWSDriverHonestUnsupportedAndUnknownMode(t *testing.T) {
	if _, err := New(Config{Transport: "bogus"}); err == nil {
		t.Fatal("unknown transport must be rejected")
	}
	d := newDWSTestDriver(t, &fakeDWSClient{}, func(cfg *Config) { cfg.NoDWS = true })
	done := make(chan error, 1)
	go func() { done <- d.Subscribe(context.Background(), make(chan im.RawEvent, 1)) }()
	select {
	case err := <-done:
		if !errors.Is(err, im.ErrUnsupported) {
			t.Fatalf("err = %v, want ErrUnsupported", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Subscribe did not fail fast with NoDWS")
	}
	if state := d.Health(context.Background()).State; state != im.HealthError {
		t.Fatalf("health = %q, want error", state)
	}
}

// 编译期检查：runtime 通过这两个方法做"检查/授权"。
func TestDWSDriverExposesAuthAndHealth(t *testing.T) {
	fake := &fakeDWSClient{auth: AuthStatus{LoggedIn: true, Account: "a@corp"}, health: DWSHealth{State: "ok", Detail: "fine"}}
	d := newDWSTestDriver(t, fake, nil)
	st, err := d.AuthStatus(context.Background())
	if err != nil || !st.LoggedIn || st.Account != "a@corp" {
		t.Fatalf("AuthStatus = %+v, %v", st, err)
	}
	h, err := d.DWSHealth(context.Background())
	if err != nil || h.State != "ok" || h.Detail != "fine" {
		t.Fatalf("DWSHealth = %+v, %v", h, err)
	}
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}

// 命令缺失/不可执行：驱动 health 变成 error（detail 可操作），而不是假装在线。
func TestDWSDriverMissingCommandReportsHealthError(t *testing.T) {
	requirePOSIX(t)
	d, err := New(Config{
		Transport:  "dws",
		DWSCommand: filepath.Join(t.TempDir(), "no-such-dws"),
		SelfID:     "bot-1",
		Now:        func() time.Time { return time.UnixMilli(1_700_000_100_000) },
		Retry:      im.RetryPolicy{Base: time.Millisecond},
		IdleDelay:  time.Millisecond,
		DWSBackoff: time.Millisecond,
	})
	if err != nil {
		t.Fatalf("New(dws): %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- d.Subscribe(ctx, make(chan im.RawEvent, 1)) }()
	waitFor(t, func() bool { return d.Health(context.Background()).State == im.HealthError }, "health never turned error for a missing dws")
	detail := d.Health(context.Background()).Detail
	if !strings.Contains(strings.ToLower(detail), "not executable") {
		t.Fatalf("health detail = %q, want an actionable command error", detail)
	}
	h, herr := d.DWSHealth(context.Background())
	if herr == nil || h.State != "error" {
		t.Fatalf("DWSHealth = %+v, %v; want state error", h, herr)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Subscribe did not stop")
	}
}
