package mcp

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/tool"
)

// newTestManager 建 Manager 并把 dial 换成假 hub。
func newTestManager(t *testing.T, hub *fakeHub, cfg Config) *Manager {
	t.Helper()
	m, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	m.dial = hub.dial
	return m
}

func mustStart(t *testing.T, m *Manager) {
	t.Helper()
	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
}

// toolsByName 把 Manager 暴露的 eino 工具按前缀后的名字索引，方便断言。
func toolsByName(t *testing.T, m *Manager) map[string]tool.InvokableTool {
	t.Helper()
	out := map[string]tool.InvokableTool{}
	for _, bt := range m.Tools() {
		it, ok := bt.(tool.InvokableTool)
		if !ok {
			t.Fatalf("工具 %T 没有实现 tool.InvokableTool", bt)
		}
		info, err := it.Info(context.Background())
		if err != nil {
			t.Fatalf("Info: %v", err)
		}
		out[info.Name] = it
	}
	return out
}

// testHealth 是测试侧的状态快照。Manager.Health 作为没有生产调用者的导出 API
// 已删除（F15），测试直接读连接内部状态。
type testHealth struct {
	Name      string
	State     State
	Tools     int
	LastError string
}

func healthOf(t *testing.T, m *Manager, name string) testHealth {
	t.Helper()
	key := sanitizeName(name)
	for _, c := range m.servers {
		if sanitizeName(c.cfg.Name) != key {
			continue
		}
		c.mu.Lock()
		defer c.mu.Unlock()
		h := testHealth{Name: c.cfg.Name, State: c.state, Tools: len(c.entries)}
		if c.lastErr != nil {
			h.LastError = c.lastErr.Error()
		}
		return h
	}
	t.Fatalf("Manager 里没有 server %q", name)
	return testHealth{}
}

// TestManagerDiscoversAndPrefixesTools 覆盖：工具发现、名字前缀、描述与 JSON Schema 透传。
func TestManagerDiscoversAndPrefixesTools(t *testing.T) {
	hub := newFakeHub(t)
	hub.addServer("fs",
		fakeTool{Name: "read_file", Desc: "read a file", Handler: func(context.Context, map[string]any) (string, error) {
			return "file-body", nil
		}},
		fakeTool{Name: "write-file", Desc: "", Handler: func(context.Context, map[string]any) (string, error) {
			return "ok", nil
		}},
	)
	m := newTestManager(t, hub, Config{Servers: []ServerConfig{{Name: "fs", Command: "fake"}}})
	mustStart(t, m)
	defer m.Close()

	tools := toolsByName(t, m)
	if len(tools) != 2 {
		t.Fatalf("期望 2 个工具，实际 %d: %v", len(tools), keysOf(tools))
	}
	read, ok := tools["mcp__fs__read_file"]
	if !ok {
		t.Fatalf("缺少 mcp__fs__read_file，实际: %v", keysOf(tools))
	}
	info, err := read.Info(context.Background())
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if info.Desc != "read a file" {
		t.Errorf("描述没有透传: %q", info.Desc)
	}
	if info.ParamsOneOf == nil {
		t.Fatal("inputSchema 没有转成 ParamsOneOf")
	}
	js, err := info.ParamsOneOf.ToJSONSchema()
	if err != nil {
		t.Fatalf("ToJSONSchema: %v", err)
	}
	raw, err := js.MarshalJSON()
	if err != nil {
		t.Fatalf("schema 序列化: %v", err)
	}
	if !strings.Contains(string(raw), `"text"`) || !strings.Contains(string(raw), `"string"`) {
		t.Errorf("schema 丢了 text 属性: %s", raw)
	}
	// '-' 在 provider 允许的字符集里，保留它比改成下划线更容易对上 server 里的原名；
	// 真正要收敛的是空格、点、斜杠这类。
	if _, ok := tools["mcp__fs__write-file"]; !ok {
		t.Fatalf("带连字符的工具名应保留，实际: %v", keysOf(tools))
	}
}

// TestToolCallRoundTrip 覆盖：调用成功路径。
func TestToolCallRoundTrip(t *testing.T) {
	hub := newFakeHub(t)
	hub.addServer("s1", echoTool("echo"))
	m := newTestManager(t, hub, Config{Servers: []ServerConfig{{Name: "s1", Command: "fake"}}})
	mustStart(t, m)
	defer m.Close()

	echo := toolsByName(t, m)["mcp__s1__echo"]
	if echo == nil {
		t.Fatal("缺少 mcp__s1__echo")
	}
	out, err := echo.InvokableRun(context.Background(), `{"text":"hi"}`)
	if err != nil {
		t.Fatalf("InvokableRun: %v", err)
	}
	if out != "echo:hi" {
		t.Errorf("结果 = %q，期望 echo:hi", out)
	}
}

// TestToolCallAcceptsEmptyArguments 覆盖：无参工具的三种写法都要能过。
func TestToolCallAcceptsEmptyArguments(t *testing.T) {
	hub := newFakeHub(t)
	hub.addServer("s1", fakeTool{Name: "noop", Desc: "no args", Handler: func(context.Context, map[string]any) (string, error) {
		return "done", nil
	}})
	m := newTestManager(t, hub, Config{Servers: []ServerConfig{{Name: "s1", Command: "fake"}}})
	mustStart(t, m)
	defer m.Close()

	noop := toolsByName(t, m)["mcp__s1__noop"]
	for _, args := range []string{"", "null", "{}"} {
		out, err := noop.InvokableRun(context.Background(), args)
		if err != nil {
			t.Fatalf("参数 %q: %v", args, err)
		}
		if out != "done" {
			t.Errorf("参数 %q 结果 = %q", args, out)
		}
	}
}

// TestToolLevelErrorIsReturnedAsText 覆盖：isError=true 不能变成 Go error。
// 理由：eino 的 ToolsNode 会把 Go error 当成整轮失败，一个"文件不存在"不该终止 Ticket。
func TestToolLevelErrorIsReturnedAsText(t *testing.T) {
	hub := newFakeHub(t)
	hub.addServer("s1", fakeTool{Name: "bad", Desc: "fails", IsError: true, Handler: func(context.Context, map[string]any) (string, error) {
		return "boom: no such file", nil
	}})
	m := newTestManager(t, hub, Config{Servers: []ServerConfig{{Name: "s1", Command: "fake"}}})
	mustStart(t, m)
	defer m.Close()

	out, err := toolsByName(t, m)["mcp__s1__bad"].InvokableRun(context.Background(), "{}")
	if err != nil {
		t.Fatalf("工具级错误不该返回 Go error: %v", err)
	}
	if !strings.Contains(out, "MCP 工具返回错误") || !strings.Contains(out, "no such file") {
		t.Errorf("错误文本没喂回模型: %q", out)
	}
}

// TestInvalidArgumentsArePermanent 覆盖：坏参数是永久错误，且不会把 server 拉黑。
func TestInvalidArgumentsArePermanent(t *testing.T) {
	hub := newFakeHub(t)
	hub.addServer("s1", echoTool("echo"))
	m := newTestManager(t, hub, Config{Servers: []ServerConfig{{Name: "s1", Command: "fake"}}})
	mustStart(t, m)
	defer m.Close()

	_, err := toolsByName(t, m)["mcp__s1__echo"].InvokableRun(context.Background(), "{not json")
	if err == nil {
		t.Fatal("期望参数解析失败")
	}
	if k, ok := KindOf(err); !ok || k != KindPermanent {
		t.Errorf("分类 = %v/%v，期望 permanent", k, ok)
	}
	if h := healthOf(t, m, "s1"); h.State != StateReady {
		t.Errorf("参数错误不该让 server 下线，state=%s", h.State)
	}
}

// TestCallTimeoutMarksServerDown 覆盖：超时 → 可重试 + 工具从列表消失。
func TestCallTimeoutMarksServerDown(t *testing.T) {
	hub := newFakeHub(t)
	hub.addServer("slow", fakeTool{Name: "block", Desc: "blocks", Handler: func(ctx context.Context, _ map[string]any) (string, error) {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(3 * time.Second):
			return "too late", nil
		}
	}})
	m := newTestManager(t, hub, Config{
		Servers: []ServerConfig{{Name: "slow", Command: "fake", CallTimeout: 120 * time.Millisecond}},
	})
	mustStart(t, m)
	defer m.Close()

	_, err := toolsByName(t, m)["mcp__slow__block"].InvokableRun(context.Background(), "{}")
	if err == nil {
		t.Fatal("期望超时错误")
	}
	if !IsRetryable(err) {
		t.Errorf("超时应被判为可重试: %v", err)
	}
	var ce *CallError
	if !errors.As(err, &ce) || ce.Server != "slow" || ce.Tool != "block" {
		t.Errorf("CallError 缺少上下文: %v", err)
	}
	if got := toolsByName(t, m); len(got) != 0 {
		t.Errorf("下线的 server 不该继续提供工具: %v", keysOf(got))
	}
	if h := healthOf(t, m, "slow"); h.State != StateDown || h.LastError == "" {
		t.Errorf("Health 应显示 down + 原因: %+v", h)
	}
}

// TestBrokenServerDoesNotBlockOthers 覆盖：一个 server 挂了，其它 server 的工具照常可用。
func TestBrokenServerDoesNotBlockOthers(t *testing.T) {
	hub := newFakeHub(t)
	hub.addServer("bad", echoTool("echo"))
	hub.addServer("good", echoTool("echo"))
	hub.setDown("bad", true)

	m := newTestManager(t, hub, Config{Servers: []ServerConfig{
		{Name: "bad", Command: "fake"},
		{Name: "good", Command: "fake"},
	}})
	// 关键：Start 不该因为一个 server 连不上就整体失败。
	mustStart(t, m)
	defer m.Close()

	tools := toolsByName(t, m)
	if len(tools) != 1 {
		t.Fatalf("期望只有 1 个可用工具，实际: %v", keysOf(tools))
	}
	if _, ok := tools["mcp__good__echo"]; !ok {
		t.Fatalf("健康 server 的工具应可用: %v", keysOf(tools))
	}
	if out, err := tools["mcp__good__echo"].InvokableRun(context.Background(), `{"text":"x"}`); err != nil || out != "echo:x" {
		t.Fatalf("健康 server 调用失败: out=%q err=%v", out, err)
	}
	if h := healthOf(t, m, "bad"); h.State != StateDown || h.LastError == "" {
		t.Errorf("坏 server 的状态应带原因: %+v", h)
	}
	// 坏 server 修复后，进程重启（重新 Start）即可恢复；Wave 1 删掉了没有调用者的
	// 在线重连 API（F15）。
	hub.setDown("bad", false)
	fresh := newTestManager(t, hub, Config{Servers: []ServerConfig{
		{Name: "bad", Command: "fake"},
		{Name: "good", Command: "fake"},
	}})
	mustStart(t, fresh)
	defer fresh.Close()
	if got := len(fresh.Tools()); got != 2 {
		t.Errorf("修复并重启后应有 2 个工具，实际 %d", got)
	}
}

// TestStartFailsWhenAllServersFail 覆盖：全挂时 Start 报错（配置整体不可用的信号）。
func TestStartFailsWhenAllServersFail(t *testing.T) {
	hub := newFakeHub(t)
	hub.addServer("a", echoTool("echo"))
	hub.addServer("b", echoTool("echo"))
	hub.setDown("a", true)
	hub.setDown("b", true)
	m := newTestManager(t, hub, Config{Servers: []ServerConfig{
		{Name: "a", Command: "fake"},
		{Name: "b", Command: "fake"},
	}})
	defer m.Close()
	if err := m.Start(context.Background()); err == nil {
		t.Fatal("全部 server 失败时 Start 应返回错误")
	}
}

// TestCloseIsIdempotent 覆盖：关闭后不再给工具，重复关闭不报错。
func TestCloseIsIdempotent(t *testing.T) {
	hub := newFakeHub(t)
	hub.addServer("s1", echoTool("echo"))
	m := newTestManager(t, hub, Config{Servers: []ServerConfig{{Name: "s1", Command: "fake"}}})
	mustStart(t, m)

	if err := m.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := m.Tools(); len(got) != 0 {
		t.Errorf("关闭后不该再有工具: %d", len(got))
	}
	if err := m.Close(); err != nil {
		t.Errorf("重复 Close 应幂等: %v", err)
	}
	if h := healthOf(t, m, "s1"); h.State != StateClosed {
		t.Errorf("状态应为 closed，实际 %s", h.State)
	}
}

// TestNewManagerValidation 覆盖：配置校验。
func TestNewManagerValidation(t *testing.T) {
	cases := []struct {
		name    string
		cfg     Config
		wantSub string
	}{
		{"no servers", Config{}, "没有配置"},
		{"empty name", Config{Servers: []ServerConfig{{Command: "x"}}}, "name 不能为空"},
		{"both transports", Config{Servers: []ServerConfig{{Name: "a", Command: "x", URL: "http://y"}}}, "同时配置"},
		{"no transport", Config{Servers: []ServerConfig{{Name: "a"}}}, "需要 command"},
		{"duplicate", Config{Servers: []ServerConfig{{Name: "a", Command: "x"}, {Name: "a", Command: "y"}}}, "重复"},
		{"duplicate after sanitize", Config{Servers: []ServerConfig{{Name: "a.b", Command: "x"}, {Name: "a_b", Command: "y"}}}, "重复"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewManager(tc.cfg)
			if err == nil || !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("err = %v，期望包含 %q", err, tc.wantSub)
			}
		})
	}
}

func keysOf[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
