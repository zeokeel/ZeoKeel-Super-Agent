package dingtalk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zeokeel/super-agent/internal/domain"
	"github.com/zeokeel/super-agent/internal/im"
)

// fakeDWSStream 是一条假的长驻 consumer：测试通过 emit 往 stdout 写 NDJSON，
// ready 往 stderr 写 ready marker；Kill/exit 模拟断线。
type fakeDWSStream struct {
	stdoutR *io.PipeReader
	stdoutW *io.PipeWriter
	stderrR *io.PipeReader
	stderrW *io.PipeWriter
	exit    chan error
	once    sync.Once
}

func newFakeDWSStream() *fakeDWSStream {
	outR, outW := io.Pipe()
	errR, errW := io.Pipe()
	return &fakeDWSStream{stdoutR: outR, stdoutW: outW, stderrR: errR, stderrW: errW, exit: make(chan error, 1)}
}

func (s *fakeDWSStream) Stdout() io.Reader { return s.stdoutR }
func (s *fakeDWSStream) Stderr() io.Reader { return s.stderrR }
func (s *fakeDWSStream) Wait() error       { return <-s.exit }
func (s *fakeDWSStream) Kill() error {
	s.finish(errors.New("killed"))
	return nil
}

func (s *fakeDWSStream) finish(err error) {
	s.once.Do(func() {
		_ = s.stdoutW.Close()
		_ = s.stderrW.Close()
		s.exit <- err
	})
}

func (s *fakeDWSStream) emit(t *testing.T, line string) {
	t.Helper()
	if _, err := io.WriteString(s.stdoutW, line+"\n"); err != nil {
		t.Fatalf("emit: %v", err)
	}
}

func (s *fakeDWSStream) ready(t *testing.T) {
	t.Helper()
	if _, err := io.WriteString(s.stderrW, "[event] ready\n"); err != nil {
		t.Fatalf("ready: %v", err)
	}
}

// fakeDWSCLI 记录调用、统计并发，并可配置 Run/StartStream 行为。
type fakeDWSCLI struct {
	mu      sync.Mutex
	runFn   func(args []string) ([]byte, []byte, error)
	startFn func(args []string) (DWSStream, error)
	runs    [][]string
	starts  [][]string

	active    int32
	maxActive int32
}

func (f *fakeDWSCLI) enter() {
	active := atomic.AddInt32(&f.active, 1)
	for {
		cur := atomic.LoadInt32(&f.maxActive)
		if active <= cur || atomic.CompareAndSwapInt32(&f.maxActive, cur, active) {
			return
		}
	}
}

func (f *fakeDWSCLI) leave() { atomic.AddInt32(&f.active, -1) }

func (f *fakeDWSCLI) Run(_ context.Context, args ...string) ([]byte, []byte, error) {
	f.enter()
	defer f.leave()
	f.mu.Lock()
	f.runs = append(f.runs, append([]string(nil), args...))
	fn := f.runFn
	f.mu.Unlock()
	if fn == nil {
		return []byte(`{}`), nil, nil
	}
	return fn(args)
}

func (f *fakeDWSCLI) StartStream(_ context.Context, args ...string) (DWSStream, error) {
	f.enter()
	defer f.leave()
	f.mu.Lock()
	f.starts = append(f.starts, append([]string(nil), args...))
	fn := f.startFn
	f.mu.Unlock()
	if fn == nil {
		return newFakeDWSStream(), nil
	}
	return fn(args)
}

func (f *fakeDWSCLI) runArgs() [][]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([][]string(nil), f.runs...)
}

func (f *fakeDWSCLI) startArgs() [][]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([][]string(nil), f.starts...)
}

func (f *fakeDWSCLI) maxConcurrent() int { return int(atomic.LoadInt32(&f.maxActive)) }

// eventLine 构造一条真实 CLI `--flatten -f ndjson` 事件行。
func eventLine(id, convID, convType, senderID, senderName, text string, atMillis int64, extra map[string]any) string {
	record := map[string]any{
		"event_id":                id,
		"event_type":              map[string]string{"group": "user_im_message_receive_group_all", "direct": "user_im_message_receive_o2o_all"}[convType],
		"conversation_id":         convID,
		"conversation_type":       convType,
		"sender":                  senderName,
		"sender_open_dingtalk_id": senderID,
		"content":                 map[string]any{"text": text},
		"create_time":             atMillis,
	}
	for k, v := range extra {
		record[k] = v
	}
	raw, _ := json.Marshal(record)
	return string(raw)
}

func newTestChannel(t *testing.T, cli DWSCLI, mutate func(*DWSConfig)) *DWSChannel {
	t.Helper()
	cfg := DWSConfig{
		Command:    "dws",
		ListenKind: ListenAtMe,
		Timeout:    time.Second,
		Backoff:    time.Millisecond,
		MaxBackoff: 5 * time.Millisecond,
	}
	if mutate != nil {
		mutate(&cfg)
	}
	ch, err := NewDWSChannel(cfg, cli)
	if err != nil {
		t.Fatalf("NewDWSChannel: %v", err)
	}
	t.Cleanup(func() { _ = ch.Close() })
	return ch
}

// NDJSON 事件 → RawEvent：会话、发送者、正文、@ 事实、单聊 dm: 编码。
func TestDWSChannelConsumesNDJSON(t *testing.T) {
	stream := newFakeDWSStream()
	cli := &fakeDWSCLI{startFn: func([]string) (DWSStream, error) { return stream, nil }}
	ch := newTestChannel(t, cli, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out := make(chan im.RawEvent, 8)
	done := make(chan error, 1)
	go func() { done <- ch.Subscribe(ctx, out) }()

	stream.ready(t)
	stream.emit(t, eventLine("e1", "cid_g", "group", "u1", "张三", "@bot 帮我看下", 1_700_000_000_000, nil))
	// 同 event id 重放：只交付一次。
	stream.emit(t, eventLine("e1", "cid_g", "group", "u1", "张三", "@bot 帮我看下", 1_700_000_000_000, nil))
	// 单聊：conversation id 编码成 dm:<openDingTalkId>，发送时才能换 --open-dingtalk-id。
	stream.emit(t, eventLine("e2", "cid_d", "direct", "u2", "李四", "私聊一句", 1_700_000_100_000, nil))

	first := <-out
	if first.ID != "e1" || first.Conv.ID != "cid_g" || first.Conv.Kind != domain.ConvGroup {
		t.Fatalf("group raw event = %+v", first)
	}
	if first.Actor.ID != "u1" || first.Actor.Bot {
		t.Fatalf("actor = %+v", first.Actor)
	}
	if first.Text != "@bot 帮我看下" || !first.AddressedToBot {
		t.Fatalf("text/addressed = %q/%v", first.Text, first.AddressedToBot)
	}
	if first.At != 1_700_000_000_000 {
		t.Fatalf("At = %d", first.At)
	}
	second := <-out
	if second.ID != "e2" || second.Conv.Kind != domain.ConvDirect || second.Conv.ID != "dm:u2" {
		t.Fatalf("direct raw event = %+v", second)
	}
	select {
	case dup := <-out:
		t.Fatalf("duplicate event delivered: %+v", dup)
	case <-time.After(100 * time.Millisecond):
	}
	if ch.Duplicates() != 1 {
		t.Fatalf("duplicates = %d, want 1", ch.Duplicates())
	}
	// 命令模板：event +listen-im --kind at-me --flatten -f ndjson
	starts := cli.startArgs()
	if len(starts) != 1 {
		t.Fatalf("starts = %v", starts)
	}
	joined := strings.Join(starts[0], " ")
	if !strings.Contains(joined, "event +listen-im") || !strings.Contains(joined, "--kind at-me") ||
		!strings.Contains(joined, "--flatten -f ndjson") {
		t.Fatalf("listen args = %v", starts[0])
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Subscribe did not stop")
	}
}

// 坏行跳过并计数；ready marker 只更新状态，不当作事件。
func TestDWSChannelSkipsMalformedLinesAndUsesReadyMarker(t *testing.T) {
	stream := newFakeDWSStream()
	cli := &fakeDWSCLI{startFn: func([]string) (DWSStream, error) { return stream, nil }}
	ch := newTestChannel(t, cli, nil)
	var states []im.HealthState
	var stateMu sync.Mutex
	ch.SetStatus(func(state im.HealthState, _ string) {
		stateMu.Lock()
		states = append(states, state)
		stateMu.Unlock()
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out := make(chan im.RawEvent, 4)
	go func() { _ = ch.Subscribe(ctx, out) }()

	stream.ready(t)
	stream.emit(t, "this is not json")
	stream.emit(t, `{"event_id":"e3"}`) // 缺会话/时间
	stream.emit(t, eventLine("e3", "cid", "group", "u1", "张三", "ok", 1, nil))
	ev := <-out
	if ev.ID != "e3" {
		t.Fatalf("event = %+v", ev)
	}
	if ch.Dropped() != 2 {
		t.Fatalf("dropped = %d, want 2", ch.Dropped())
	}
	stateMu.Lock()
	sawReady := false
	for _, s := range states {
		if s == im.HealthReady {
			sawReady = true
		}
	}
	stateMu.Unlock()
	if !sawReady {
		t.Fatalf("status states = %v, want ready after marker", states)
	}
	cancel()
}

// 断线 → 退避重连 → 重放被去重；Failures 在成功后清零。
func TestDWSChannelReconnectsAndDedupes(t *testing.T) {
	var mu sync.Mutex
	streams := []*fakeDWSStream{newFakeDWSStream(), newFakeDWSStream()}
	startCount := 0
	cli := &fakeDWSCLI{startFn: func([]string) (DWSStream, error) {
		mu.Lock()
		defer mu.Unlock()
		if startCount >= len(streams) {
			return nil, errors.New("no more fake streams")
		}
		s := streams[startCount]
		startCount++
		return s, nil
	}}
	ch := newTestChannel(t, cli, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out := make(chan im.RawEvent, 8)
	go func() { _ = ch.Subscribe(ctx, out) }()

	streams[0].emit(t, eventLine("e1", "cid", "group", "u1", "张三", "first", 1, nil))
	if ev := <-out; ev.ID != "e1" {
		t.Fatalf("first event = %+v", ev)
	}
	// consumer 退出：Subscribe 内部重连（下一轮 NextBatch 等退避后重启）。
	streams[0].finish(errors.New("connection reset"))
	waitForCond(t, func() bool { return len(cli.startArgs()) >= 2 }, "consumer reconnect")
	streams[1].emit(t, eventLine("e1", "cid", "group", "u1", "张三", "first", 1, nil)) // 重放
	streams[1].emit(t, eventLine("e2", "cid", "group", "u1", "张三", "second", 2, nil))
	select {
	case ev := <-out:
		if ev.ID != "e2" {
			t.Fatalf("after reconnect got %+v, want e2 (e1 was replayed)", ev)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no event after reconnect")
	}
	waitForCond(t, func() bool { return ch.Failures() == 0 }, "failures reset")
}

// auth status 命令：--format json、profile、状态与登录命令。
func TestDWSChannelAuthStatus(t *testing.T) {
	var mu sync.Mutex
	var args [][]string
	cli := &fakeDWSCLI{runFn: func(command []string) ([]byte, []byte, error) {
		mu.Lock()
		args = append(args, append([]string(nil), command...))
		mu.Unlock()
		return []byte(`{"loggedIn":true,"account":"ops@corp","profile":"work"}`), nil, nil
	}}
	ch := newTestChannel(t, cli, func(cfg *DWSConfig) { cfg.Profile = "work" })
	st, err := ch.AuthStatus(context.Background())
	if err != nil || !st.LoggedIn || st.Account != "ops@corp" || st.Profile != "work" {
		t.Fatalf("AuthStatus = %+v, %v", st, err)
	}
	mu.Lock()
	got := strings.Join(args[0], " ")
	mu.Unlock()
	if !strings.Contains(got, "auth status") || !strings.Contains(got, "--format json") || !strings.Contains(got, "--profile work") {
		t.Fatalf("auth args = %q", got)
	}
	state, _ := ch.Status()
	if state != "ready" {
		t.Fatalf("state = %q", state)
	}

	// 未登录：state auth + authUrl + login 命令。
	cli2 := &fakeDWSCLI{runFn: func([]string) ([]byte, []byte, error) {
		return []byte(`{"loggedIn":false,"authUrl":"https://login.example/abc"}`), nil, nil
	}}
	ch2 := newTestChannel(t, cli2, func(cfg *DWSConfig) { cfg.LoginHeadless = true })
	st2, err := ch2.AuthStatus(context.Background())
	if err != nil || st2.LoggedIn || st2.AuthURL == "" {
		t.Fatalf("not logged in = %+v, %v", st2, err)
	}
	if !strings.Contains(st2.LoginCommand, "auth login --device") {
		t.Fatalf("login command = %q", st2.LoginCommand)
	}
	state2, detail2 := ch2.Status()
	if state2 != "auth" || !strings.Contains(detail2, "dws auth login") {
		t.Fatalf("status = %q/%q", state2, detail2)
	}
}

// 发送命令模板：群/单聊不同 flag、marker、无 --idempotency-key。
func TestDWSChannelSendTemplates(t *testing.T) {
	var mu sync.Mutex
	var calls [][]string
	cli := &fakeDWSCLI{runFn: func(args []string) ([]byte, []byte, error) {
		mu.Lock()
		calls = append(calls, append([]string(nil), args...))
		mu.Unlock()
		return []byte(`{"messageId":"msg_1"}`), nil, nil
	}}
	ch := newTestChannel(t, cli, nil)
	receipt, err := ch.Send(context.Background(), im.Outbound{
		Key: "k-1", Conv: domain.Conversation{Kind: domain.ConvGroup, ID: "cid_g"}, Text: "收到",
	})
	if err != nil || receipt.MessageID != "msg_1" {
		t.Fatalf("Send = %+v, %v", receipt, err)
	}
	mu.Lock()
	groupArgs := strings.Join(calls[len(calls)-1], " ")
	mu.Unlock()
	if !strings.Contains(groupArgs, "chat +messages-send --as user --group cid_g") {
		t.Fatalf("group args = %q", groupArgs)
	}
	if !strings.Contains(groupArgs, "--text 收到") || !strings.Contains(groupArgs, "<!-- zeokeel:k-1 -->") {
		t.Fatalf("marker missing: %q", groupArgs)
	}
	if strings.Contains(groupArgs, "--idempotency-key") {
		t.Fatalf("DWS has no idempotency flag, got %q", groupArgs)
	}

	// 单聊：dm:<openDingTalkId> → --open-dingtalk-id
	if _, err := ch.Send(context.Background(), im.Outbound{
		Key: "k-2", Conv: domain.Conversation{Kind: domain.ConvDirect, ID: "dm:u9"}, Text: "hi",
	}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	directArgs := strings.Join(calls[len(calls)-1], " ")
	mu.Unlock()
	if !strings.Contains(directArgs, "--open-dingtalk-id u9") || strings.Contains(directArgs, "--group") {
		t.Fatalf("direct args = %q", directArgs)
	}

	// Markdown 走 --markdown；openTaskId 也可作为回执 id。
	cli.runFn = func([]string) ([]byte, []byte, error) { return []byte(`{"openTaskId":"task_9"}`), nil, nil }
	receipt, err = ch.Send(context.Background(), im.Outbound{
		Key: "k-3", Conv: domain.Conversation{Kind: domain.ConvGroup, ID: "cid_m"}, Text: "# 标题", Markdown: true,
	})
	if err != nil || receipt.MessageID != "task_9" {
		t.Fatalf("markdown Send = %+v, %v", receipt, err)
	}
}

// 4xx 永久 / 5xx 可重试；stderr 里的秘密被脱敏。
func TestDWSChannelSendErrorClassificationAndRedaction(t *testing.T) {
	ch := newTestChannel(t, &fakeDWSCLI{runFn: func([]string) ([]byte, []byte, error) {
		return nil, []byte("HTTP 404 Not Found: client_secret=topsecret token=abc123"), errors.New("exit 1")
	}}, func(cfg *DWSConfig) { cfg.Secrets = []string{"topsecret"} })
	_, err := ch.Send(context.Background(), im.Outbound{
		Key: "k", Conv: domain.Conversation{Kind: domain.ConvGroup, ID: "cid"}, Text: "x",
	})
	if err == nil || !IsPermanent(err) || IsRetryable(err) {
		t.Fatalf("404 err = %v (want permanent)", err)
	}
	msg := err.Error()
	for _, secret := range []string{"topsecret", "abc123"} {
		if strings.Contains(msg, secret) {
			t.Fatalf("secret %q leaked in %q", secret, msg)
		}
	}

	ch2 := newTestChannel(t, &fakeDWSCLI{runFn: func([]string) ([]byte, []byte, error) {
		return nil, []byte("HTTP 503 Service Unavailable retry-after: 2"), errors.New("exit 1")
	}}, nil)
	_, err = ch2.Send(context.Background(), im.Outbound{
		Key: "k", Conv: domain.Conversation{Kind: domain.ConvGroup, ID: "cid"}, Text: "x",
	})
	if err == nil || IsPermanent(err) || !IsRetryable(err) {
		t.Fatalf("503 err = %v (want retryable)", err)
	}
	if d := RetryAfter(err); d != 2*time.Second {
		t.Fatalf("RetryAfter = %v", d)
	}
}

// 短命令串行：并发 Send/AuthStatus 时同一时刻只有一个 CLI 调用。
func TestDWSChannelSerializesCommands(t *testing.T) {
	cli := &fakeDWSCLI{runFn: func(args []string) ([]byte, []byte, error) {
		time.Sleep(5 * time.Millisecond)
		if len(args) > 0 && args[0] == "auth" {
			return []byte(`{"loggedIn":true}`), nil, nil
		}
		return []byte(`{"messageId":"m1"}`), nil, nil
	}}
	ch := newTestChannel(t, cli, nil)
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, _ = ch.Send(context.Background(), im.Outbound{Key: fmt.Sprint(i), Conv: domain.Conversation{ID: "cid"}, Text: "x"})
		}()
		go func() {
			defer wg.Done()
			_, _ = ch.AuthStatus(context.Background())
		}()
	}
	wg.Wait()
	if got := cli.maxConcurrent(); got != 1 {
		t.Fatalf("CLI calls overlapped: maxConcurrent=%d", got)
	}
}

// Close：杀 consumer、幂等、状态 stopped。
func TestDWSChannelCloseStopsConsumer(t *testing.T) {
	stream := newFakeDWSStream()
	cli := &fakeDWSCLI{startFn: func([]string) (DWSStream, error) { return stream, nil }}
	ch := newTestChannel(t, cli, nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- ch.Subscribe(ctx, make(chan im.RawEvent, 1)) }()
	waitForCond(t, func() bool { return len(cli.startArgs()) == 1 }, "consumer start")
	// 让 NextBatch 阻塞在事件上。
	if err := ch.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Subscribe did not stop after Close")
	}
	if state, _ := ch.Status(); state != "stopped" {
		t.Fatalf("state = %q, want stopped", state)
	}
	if err := ch.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	cancel()
}

// Status/Reset/Failures：失败可观察，Reset 清空且停 consumer。
func TestDWSChannelStatusAndReset(t *testing.T) {
	failStream := func([]string) (DWSStream, error) { return nil, errors.New("spawn failed") }
	cli := &fakeDWSCLI{startFn: failStream}
	ch := newTestChannel(t, cli, nil)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := ch.NextBatch(ctx, 1); err == nil {
		t.Fatal("expected a start failure")
	}
	if ch.Failures() == 0 || ch.LastError() == nil {
		t.Fatalf("failures/lastErr = %d/%v", ch.Failures(), ch.LastError())
	}
	if state, detail := ch.Status(); state != "error" || detail == "" {
		t.Fatalf("status = %q/%q", state, detail)
	}
	ch.Reset()
	if ch.Failures() != 0 || ch.LastError() != nil {
		t.Fatalf("Reset left failures=%d lastErr=%v", ch.Failures(), ch.LastError())
	}
	if state, _ := ch.Status(); state != "starting" {
		t.Fatalf("state after Reset = %q", state)
	}
}

func waitForCond(t *testing.T, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}
