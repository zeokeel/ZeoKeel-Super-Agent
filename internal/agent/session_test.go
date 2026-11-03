package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/adk/session"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

// sessionStatsForTest 从事件日志重算面板指标。Runner.sessionStats 作为没有生产
// 调用者的导出 API 已删除（F7），但事件日志仍是唯一事实来源，测试直接断言它。
func sessionStatsForTest(t *testing.T, r *Runner, sessionID string) sessionStats {
	t.Helper()
	events, err := r.loadEvents(context.Background(), sessionID)
	if err != nil {
		t.Fatal(err)
	}
	return computesessionStats(sessionID, events)
}

// waitIdle 轮询到日志收尾为 idle：取消后的收尾事件由 adk 的后台流程写入，
// 与 Run 返回之间没有严格顺序，测试要等它真正落盘再断言。
func waitIdle(t *testing.T, r *Runner, sessionID string) sessionStats {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var stats sessionStats
	for time.Now().Before(deadline) {
		stats = sessionStatsForTest(t, r, sessionID)
		if stats.Idle {
			return stats
		}
		time.Sleep(10 * time.Millisecond)
	}
	return stats
}

// 会话事件必须真的落盘：同一 SessionID 的下一轮要从日志重建历史再继续。
func TestSessionPersistsAndContinues(t *testing.T) {
	srv := startMock(t)
	dir := t.TempDir()
	r := newTestRunner(t, testConfig(srv, dir, "mock-text"))
	ctx := context.Background()

	first, err := r.RunSession(ctx, RunRequest{System: "sys", Task: "第一轮任务"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if first.Answer == "" {
		t.Fatal("empty first answer")
	}
	// 事件日志文件必须落在 SessionDir 里，且非空。
	entries, err := os.ReadDir(r.Config().SessionDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatalf("no session event log under %s", r.Config().SessionDir)
	}
	events, err := r.loadEvents(ctx, first.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) == 0 {
		t.Fatal("session has no persisted events")
	}

	second, err := r.RunSession(ctx, RunRequest{
		SessionID: first.SessionID, System: "sys", Task: "第二轮任务",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if second.Answer == "" {
		t.Fatal("empty second answer")
	}
	// 第二轮必须看到第一轮的历史：模型收到的消息里要有第一轮的任务与答案。
	last := srv.LastRequest()
	var sawFirstTask, sawFirstAnswer bool
	for _, m := range last.Messages {
		if m.Role == "user" && strings.Contains(m.Content, "第一轮任务") {
			sawFirstTask = true
		}
		if m.Role == "assistant" && strings.Contains(m.Content, first.Answer) {
			sawFirstAnswer = true
		}
	}
	if !sawFirstTask || !sawFirstAnswer {
		t.Fatalf("second turn did not replay history: task=%v answer=%v messages=%+v",
			sawFirstTask, sawFirstAnswer, last.Messages)
	}

	stats := sessionStatsForTest(t, r, first.SessionID)
	if stats.Turns != 2 {
		t.Fatalf("stats.turns = %d, want 2", stats.Turns)
	}
}

// context 取消必须干净返回且可判别；之后以同一 SessionID 重跑能从日志接着跑。
// （Runner.Resume 已删除：生产重启路径本来就是 RunSession。）
func TestRunSessionContinuesAfterCancel(t *testing.T) {
	srv := startMock(t)
	dir := t.TempDir()
	slow := newTestRunner(t, testConfig(srv, dir, "mock-slow"))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var canceled bool
	res, err := slow.RunSession(ctx, RunRequest{System: "sys", Task: "慢慢说"}, func(e Event) {
		if e.Kind == EventText {
			cancel()
		}
		if e.Kind == EventCanceled {
			canceled = true
		}
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want a context.Canceled error, got %v", err)
	}
	if !canceled {
		t.Fatal("no canceled event reported to the panel")
	}
	if res.SessionID == "" {
		t.Fatal("canceled run must still report its session id")
	}
	// 等被取消那一轮的收尾事件真正落盘，再模拟"重启后继续"：真实崩溃时旧进程
	// 已经不在了，不会与新一轮并发写日志。
	slog := waitIdle(t, slow, res.SessionID)
	if slog.Turns == 0 {
		t.Fatal("canceled run left no turn marker in the log")
	}

	// 模拟进程重启：新 Runner（新 store 实例）读同一个会话目录，用更快的模型继续。
	fast := newTestRunner(t, testConfig(srv, dir, "mock-text"))
	continued, err := fast.RunSession(context.Background(), RunRequest{
		SessionID: res.SessionID, System: "sys", Task: "继续完成原任务",
	}, nil)
	if err != nil {
		t.Fatalf("continuing after cancel failed: %v", err)
	}
	if continued.Answer == "" {
		t.Fatal("continuing after cancel produced no answer")
	}
	stats := waitIdle(t, fast, res.SessionID)
	if stats.Turns != 2 || !stats.Idle {
		events, _ := fast.loadEvents(context.Background(), res.SessionID)
		for _, ev := range events {
			t.Logf("event kind=%s", ev.Kind)
		}
		t.Fatalf("after resume turns=%d idle=%v", stats.Turns, stats.Idle)
	}
}

// 崩溃点落在"工具调用中"时，日志里有 assistant.tool_calls 却没有 tool 结果。
// 生产重启路径 RunSession 必须先补一条 interrupted 说明再继续，否则真实 provider
// 会直接拒绝历史（F6/N-2 的回归测试：这条测试只走生产路径 RunSession）。
func TestRunSessionPatchesInterruptedToolCall(t *testing.T) {
	srv := startMock(t)
	dir := t.TempDir()
	r := newTestRunner(t, testConfig(srv, dir, "mock-text",
		func(c *Config) { c.ExtraTools = []tool.BaseTool{echoTool()} }))
	ctx := context.Background()
	const sid = "worker-crashpoint"

	store, err := session.NewFileStore[*schema.Message](r.Config().SessionDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	eventID := func() string {
		id, err := newSessionEventID()
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	now := time.Now().UTC()
	crashLog := []*adk.SessionEvent[*schema.Message]{
		{EventID: eventID(), Timestamp: now, Kind: adk.SessionEventSessionStatusRunning,
			Lifecycle: &adk.LifecycleEvent{State: adk.SessionRunStateRunning}},
		{EventID: eventID(), Timestamp: now, Kind: adk.SessionEventMessage,
			Message: schema.UserMessage("echo hello")},
		{EventID: eventID(), Timestamp: now, Kind: adk.SessionEventMessage,
			Message: schema.AssistantMessage("", []schema.ToolCall{{
				ID:       "call_crash_1",
				Function: schema.FunctionCall{Name: "echo_upper", Arguments: `{"text":"hi"}`},
			}})},
	}
	if err := store.AppendEvents(ctx, sid, crashLog); err != nil {
		t.Fatal(err)
	}

	before := sessionStatsForTest(t, r, sid)
	if before.Interrupted != 1 {
		t.Fatalf("stats.interrupted = %d, want 1 before resume", before.Interrupted)
	}

	// 生产崩溃重启走 RunSession（runtime/worker.go 用同一 session id 再跑）；
	// 恢复语义必须在这条路径上生效（Runner.Resume 已删除）。
	res, err := r.RunSession(ctx, RunRequest{SessionID: sid, System: "sys", Task: "继续完成任务"}, nil)
	if err != nil {
		t.Fatalf("RunSession after a crash failed: %v", err)
	}
	if !res.Resumed || res.Answer == "" {
		t.Fatalf("RunSession result = %+v", res)
	}
	if !strings.Contains(res.Answer, "interrupted") {
		t.Fatalf("model did not see the interrupted tool note: %q", res.Answer)
	}
	// 模型收到的历史里必须有一条 tool 消息来配对那个 tool_call。
	last := srv.LastRequest()
	var paired bool
	for _, m := range last.Messages {
		if m.Role == "tool" && m.ToolCallID == "call_crash_1" && strings.Contains(m.Content, "interrupted") {
			paired = true
		}
	}
	if !paired {
		t.Fatalf("no interrupted tool message paired with call_crash_1: %+v", last.Messages)
	}

	after := waitIdle(t, r, sid)
	if after.Interrupted != 0 {
		t.Fatalf("stats.interrupted = %d after patching, want 0", after.Interrupted)
	}
	if !after.Idle {
		t.Fatal("resumed session should end idle")
	}
}

func TestInvalidSessionID(t *testing.T) {
	srv := startMock(t)
	r := newTestRunner(t, testConfig(srv, t.TempDir(), "mock-text"))
	if _, err := r.RunSession(context.Background(), RunRequest{SessionID: "../escape", Task: "x"}, nil); err == nil {
		t.Fatal("a session id with path separators must be rejected")
	}
}

// 事件日志必须真的在磁盘上按会话分文件，这样崩溃恢复不依赖进程内状态。
func TestSessionLogFileOnDisk(t *testing.T) {
	srv := startMock(t)
	dir := t.TempDir()
	r := newTestRunner(t, testConfig(srv, dir, "mock-text"))
	res, err := r.RunSession(context.Background(), RunRequest{System: "sys", Task: "hi"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	matches, err := filepath.Glob(filepath.Join(r.Config().SessionDir, "*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) == 0 {
		t.Fatalf("no event log for %s", res.SessionID)
	}
	data, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), res.SessionID) && !strings.Contains(matches[0], res.SessionID) {
		t.Fatalf("event log %q does not belong to session %q", matches[0], res.SessionID)
	}
}

// 取消必须干净返回且可判别（生产路径 RunSession）。
func TestRunCanceled(t *testing.T) {
	srv := startMock(t)
	r := newTestRunner(t, testConfig(srv, t.TempDir(), "mock-slow"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var canceled bool
	_, err := r.RunSession(ctx, RunRequest{System: "sys", Task: "hi"}, func(e Event) {
		if e.Kind == EventText {
			cancel()
		}
		if e.Kind == EventCanceled {
			canceled = true
		}
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
	if !canceled {
		t.Fatal("no canceled event")
	}
}

// 进程在 append 中途被杀会留下没有换行的半行。恢复的语义是"能接着跑"，
// 生产重启路径 RunSession 要能裁掉那半行而不要求人工修日志。
func TestRunSessionRepairsTornEventLog(t *testing.T) {
	srv := startMock(t)
	dir := t.TempDir()
	r := newTestRunner(t, testConfig(srv, dir, "mock-text"))
	ctx := context.Background()
	first, err := r.RunSession(ctx, RunRequest{System: "sys", Task: "hi"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	logs, err := filepath.Glob(filepath.Join(r.Config().SessionDir, "*.evlog"))
	if err != nil || len(logs) == 0 {
		t.Fatalf("no event log: %v %v", logs, err)
	}
	f, err := os.OpenFile(logs[0], os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	// 模拟写到一半崩溃：没有结尾换行的半行。
	if _, err := f.WriteString(`torn\tmessage\t{"message":`); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	restarted := newTestRunner(t, testConfig(srv, dir, "mock-text"))
	resumed, err := restarted.RunSession(ctx, RunRequest{
		SessionID: first.SessionID, System: "sys", Task: "继续",
	}, nil)
	if err != nil {
		t.Fatalf("RunSession did not repair the torn log: %v", err)
	}
	if resumed.Answer == "" {
		t.Fatal("RunSession after a torn log produced no answer")
	}
}

// 取消后立刻继续（不等待旧进程收尾）也必须可用：这就是"点停止、再点继续"的普通交互。
func TestRunSessionImmediatelyAfterCancel(t *testing.T) {
	srv := startMock(t)
	dir := t.TempDir()
	slow := newTestRunner(t, testConfig(srv, dir, "mock-slow"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	res, err := slow.RunSession(ctx, RunRequest{System: "sys", Task: "慢慢说"}, func(e Event) {
		if e.Kind == EventText {
			cancel()
		}
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
	fast := newTestRunner(t, testConfig(srv, dir, "mock-text"))
	continued, err := fast.RunSession(context.Background(), RunRequest{
		SessionID: res.SessionID, System: "sys", Task: "继续",
	}, nil)
	if err != nil {
		t.Fatalf("immediate continue failed: %v", err)
	}
	if continued.Answer == "" {
		t.Fatalf("continue result = %+v", continued)
	}
	if stats := waitIdle(t, fast, res.SessionID); !stats.Idle || stats.Turns < 1 {
		t.Fatalf("session did not settle: turns=%d idle=%v", stats.Turns, stats.Idle)
	}
}

// 事件存储是可注入的：上层可以按 design §5.1 换成 SQLite，只要实现官方的
// adk.SessionEventStore 接口；Runner 不关心事件存在哪。
func TestInjectedSessionStore(t *testing.T) {
	srv := startMock(t)
	mem := session.NewInMemoryStore[*schema.Message](nil)
	r := newTestRunner(t, testConfig(srv, t.TempDir(), "mock-text", func(c *Config) {
		c.SessionStore = mem
	}))
	ctx := context.Background()
	first, err := r.RunSession(ctx, RunRequest{System: "sys", Task: "第一轮"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := r.RunSession(ctx, RunRequest{SessionID: first.SessionID, System: "sys", Task: "第二轮"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if second.Answer == "" {
		t.Fatal("empty answer on injected store")
	}
	events, err := r.loadEvents(ctx, first.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) == 0 {
		t.Fatal("injected store received no events")
	}
	if stats := sessionStatsForTest(t, r, first.SessionID); stats.Turns != 2 {
		t.Fatalf("stats=%+v", stats)
	}
}
