package mcp

// 为什么这条链路值得单独测（而不是只测 Manager）：
//
//	Manager.Tools() 返回 eino tool.BaseTool，但"能构造"不等于"能进 Worker 跑通"。
//	中间至少有三处会静默断掉：
//	  1. agent.New 只在构造时快照一次工具列表（agent.go:211），拼错或漏拼都不会报错；
//	  2. eino 只在 schema 合法时才把工具发给模型——"有工具"和"模型看得见工具"是两件事；
//	  3. 模型发回的 tool_call 名字必须能解析回我们包装的那个 InvokableRun；前缀拼错时
//	     表现为 unknown tool 或者干脆不调用，而不是"MCP 没接上"。
//	这三处只有"真 Manager + 真 agent.Run + 本地 OpenAI 兼容流"一起跑才会暴露，
//	所以下面跑的是完整两轮：模型看见工具 → 发起调用 → MCP 往返 → 结果回灌 → 最终答案。
//
// 测试全程不联网、不起外部进程：假 MCP server 用官方 SDK 的 server 端 + net.Pipe
// 的 IOTransport（与 stdio 同一套换行分帧），模型用 internal/agent/mockllm 的
// loopback 服务。

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zeokeel/super-agent/internal/agent"
	"github.com/zeokeel/super-agent/internal/agent/mockllm"
)

// mcpEchoTool 是契约工具：参数 text，返回 mcp-echo:<text>。
// 用固定前缀而不是复用 echoTool，是为了让断言能一眼区分"MCP 往返的结果"和
// "mock 自己编的答案"。
func mcpEchoTool() fakeTool {
	return fakeTool{
		Name: "echo",
		Desc: "echo the given text back through MCP",
		Handler: func(_ context.Context, args map[string]any) (string, error) {
			text, _ := args["text"].(string)
			return "mcp-echo:" + text, nil
		},
	}
}

// ---- 记录模型实际收到了什么 ----

type recordedRequest struct {
	// ToolNames 是请求里 tools[].function.name，即"模型真的看得见"的证据。
	ToolNames []string
	// ToolParams 是每个工具的参数 schema 原文。
	ToolParams map[string]string
	// ToolReplies 是 role=tool 的消息内容，即回灌给模型的结果。
	ToolReplies []string
}

type toolRecorder struct {
	mu   sync.Mutex
	reqs []recordedRequest
}

func (r *toolRecorder) add(body []byte) {
	var wire struct {
		Tools []struct {
			Function struct {
				Name       string          `json:"name"`
				Parameters json.RawMessage `json:"parameters"`
			} `json:"function"`
		} `json:"tools"`
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &wire); err != nil {
		return // 不是合法请求体就跳过记录，转发仍然继续
	}
	rr := recordedRequest{ToolParams: map[string]string{}}
	for _, tl := range wire.Tools {
		rr.ToolNames = append(rr.ToolNames, tl.Function.Name)
		rr.ToolParams[tl.Function.Name] = string(tl.Function.Parameters)
	}
	for _, m := range wire.Messages {
		if m.Role == "tool" {
			rr.ToolReplies = append(rr.ToolReplies, rawText(m.Content))
		}
	}
	r.mu.Lock()
	r.reqs = append(r.reqs, rr)
	r.mu.Unlock()
}

func (r *toolRecorder) snapshot() []recordedRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]recordedRequest, len(r.reqs))
	copy(out, r.reqs)
	return out
}

// rawText 兼容 content 是字符串或 parts 数组两种写法。
func rawText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return string(raw)
}

// newRecordingProxy 把请求体抄一份再转发给 mockllm。
//
// 为什么用代理而不是改 mockllm：mockllm 属于 internal/agent，本次改动限定在
// internal/mcp；更重要的是，代理看到的就是 eino 实际发出去的字节，"模型收到什么"
// 是协议层的事实，比在模型实现里加个字段更接近真实。
func newRecordingProxy(t *testing.T, target string) (*httptest.Server, *toolRecorder) {
	t.Helper()
	u, err := url.Parse(target)
	if err != nil {
		t.Fatalf("解析 mock 地址 %q: %v", target, err)
	}
	rp := httputil.NewSingleHostReverseProxy(u)
	rec := &toolRecorder{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
			return
		}
		_ = r.Body.Close()
		r.Body = io.NopCloser(bytes.NewReader(body))
		r.ContentLength = int64(len(body))
		rec.add(body)
		rp.ServeHTTP(w, r)
	}))
	t.Cleanup(ts.Close)
	return ts, rec
}

// silenceLogs 关掉 mockllm 的 log.Printf，避免测试输出被"每个请求一行"淹没。
func silenceLogs(t *testing.T) {
	t.Helper()
	prev := log.Writer()
	log.SetOutput(io.Discard)
	t.Cleanup(func() { log.SetOutput(prev) })
}

// toolNamesOf 返回 Manager 当前提供的工具名（排序后，便于断言）。
func toolNamesOf(t *testing.T, m *Manager) []string {
	t.Helper()
	byName := toolsByName(t, m)
	names := make([]string, 0, len(byName))
	for name := range byName {
		names = append(names, name)
	}
	// 工具数量很少，这里用插入排序避免再引入 sort 依赖噪音。
	for i := 1; i < len(names); i++ {
		for j := i; j > 0 && names[j] < names[j-1]; j-- {
			names[j], names[j-1] = names[j-1], names[j]
		}
	}
	return names
}

// TestMCPToolReachesWorkerAndRuns 是本次改动的核心断言：
// MCP 工具真的进了 Worker、真的被模型看见、真的被调用、结果真的回灌。
func TestMCPToolReachesWorkerAndRuns(t *testing.T) {
	silenceLogs(t)

	hub := newFakeHub(t)
	hub.addServer("ext", mcpEchoTool())
	mgr := newTestManager(t, hub, Config{Servers: []ServerConfig{{Name: "ext", Command: "fake"}}})
	mustStart(t, mgr)
	defer mgr.Close()

	// 前置条件：Manager 侧给出的名字必须是约定的 mcp__<server>__<tool>。
	if names := toolNamesOf(t, mgr); len(names) != 1 || names[0] != "mcp__ext__echo" {
		t.Fatalf("Manager 工具名 = %v，期望 [mcp__ext__echo]", names)
	}

	mock, err := mockllm.Start()
	if err != nil {
		t.Fatalf("mockllm: %v", err)
	}
	defer mock.Close()
	// 让模型调用 MCP 工具，而不是它默认的 echo_upper。
	mock.ToolName = "mcp__ext__echo"
	mock.ToolArgs = `{"text":"hi"}`
	proxy, rec := newRecordingProxy(t, mock.Root)

	runner, err := agent.New(agent.Config{
		BaseURL: proxy.URL + "/v1",
		APIKey:  "sk-mock",
		Model:   "mock-tool",
		Workdir: t.TempDir(),
		// 被测对象：Manager 的工具通过 ExtraTools 进 Worker。
		ExtraTools: mgr.Tools(),
	})
	if err != nil {
		t.Fatalf("agent.New: %v", err)
	}

	var (
		mu     sync.Mutex
		events []agent.Event
	)
	res, err := runner.RunSession(context.Background(), agent.RunRequest{
		System: "you are a test agent", Task: "call echo with hi",
	}, func(e agent.Event) {
		mu.Lock()
		events = append(events, e)
		mu.Unlock()
	})
	final := res.Answer
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	mu.Lock()
	gotEvents := append([]agent.Event(nil), events...)
	mu.Unlock()

	reqs := rec.snapshot()
	// 两轮：第一轮模型决定调用工具，第二轮拿到工具结果后给答案。
	if len(reqs) < 2 {
		t.Fatalf("期望至少 2 次模型调用（工具前后各一），实际 %d；事件: %v", len(reqs), kindsOf(gotEvents))
	}

	// (1) 模型真的看得见这个工具。
	if !containsString(reqs[0].ToolNames, "mcp__ext__echo") {
		t.Fatalf("第 1 次请求的 tools = %v，不含 mcp__ext__echo", reqs[0].ToolNames)
	}
	// (2) 参数 schema 也要在：只有名字没有 schema 的工具有些模型会拒绝调用。
	if params := reqs[0].ToolParams["mcp__ext__echo"]; !strings.Contains(params, `"text"`) {
		t.Errorf("工具 schema 丢了 text 参数: %s", params)
	}

	// (3) 工具真的执行了，并且结果进了事件流（面板靠这条画工具卡片）。
	var toolEnd *agent.Event
	for i := range gotEvents {
		if gotEvents[i].Kind == agent.EventToolEnd && gotEvents[i].Tool == "mcp__ext__echo" {
			toolEnd = &gotEvents[i]
		}
	}
	if toolEnd == nil {
		t.Fatalf("没有 mcp__ext__echo 的 tool_end 事件；事件: %v", kindsOf(gotEvents))
	}
	if !strings.Contains(toolEnd.Output, "mcp-echo:hi") {
		t.Errorf("tool_end.Output = %q，期望含 mcp-echo:hi", toolEnd.Output)
	}
	if toolEnd.Duration < 0 {
		t.Errorf("tool_end.Duration 异常: %s", toolEnd.Duration)
	}

	// (4) 结果被回灌给模型：第二次请求里应有一条 role=tool 的消息带这个结果。
	if !containsSubstring(reqs[1].ToolReplies, "mcp-echo:hi") {
		t.Errorf("第 2 次请求的 tool 消息 = %v，不含 mcp-echo:hi", reqs[1].ToolReplies)
	}

	// (5) 最终答案由模型基于工具结果给出（mock 会把 tool 内容回显进答案）。
	if !strings.Contains(final, "mcp-echo:hi") {
		t.Errorf("最终答案 = %q，期望含 mcp-echo:hi", final)
	}
}

// TestMCPToolDegradationKeepsOtherServersWorking 覆盖降级：
// 一个 server 挂掉后，它的工具从 Tools() 消失，但另一个 server 的工具照常能用。
//
// 只测 Manager 不够：Worker 拿到的是"构造时快照"的工具集合，所以"消失"必须
// 体现在下一次构造 Worker 时的 Tools() 上——这里就按真实用法验证这一点。
func TestMCPToolDegradationKeepsOtherServersWorking(t *testing.T) {
	silenceLogs(t)

	hub := newFakeHub(t)
	hub.addServer("alpha", mcpEchoTool())
	hub.addServer("beta", mcpEchoTool())
	mgr := newTestManager(t, hub, Config{
		Servers: []ServerConfig{
			{Name: "alpha", Command: "fake"},
			{Name: "beta", Command: "fake"},
		},
		DefaultCallTimeout: time.Second,
	})
	mustStart(t, mgr)
	defer mgr.Close()

	if names := toolNamesOf(t, mgr); len(names) != 2 {
		t.Fatalf("两个 server 都应就绪，实际 %v", names)
	}

	// 干掉 beta。健康状态是"用时才发现"的，所以先拿旧句柄调用一次把它标记为 down
	// ——这与生产一致：崩溃不会发通知，下一次调用失败才知道。
	betaTool := toolsByName(t, mgr)["mcp__beta__echo"]
	if betaTool == nil {
		t.Fatal("缺少 mcp__beta__echo")
	}
	hub.kill("beta")
	if _, err := betaTool.InvokableRun(context.Background(), `{"text":"x"}`); err == nil {
		t.Fatal("beta 已经崩了，调用不该成功")
	}

	// beta 的工具消失，alpha 的还在。
	names := toolNamesOf(t, mgr)
	if len(names) != 1 || names[0] != "mcp__alpha__echo" {
		t.Fatalf("beta 挂掉后工具应只剩 alpha，实际 %v", names)
	}
	if h := healthOf(t, mgr, "beta"); h.State != StateDown || h.LastError == "" {
		t.Errorf("beta 的 Health 应显示 down + 原因，实际 %+v", h)
	}

	// "其他工具照常"要用真 Worker 验证，而不是只比名字：跑一次完整的两轮调用。
	mock, err := mockllm.Start()
	if err != nil {
		t.Fatalf("mockllm: %v", err)
	}
	defer mock.Close()
	mock.ToolName = "mcp__alpha__echo"
	mock.ToolArgs = `{"text":"still-alive"}`
	proxy, _ := newRecordingProxy(t, mock.Root)

	runner, err := agent.New(agent.Config{
		BaseURL:    proxy.URL + "/v1",
		APIKey:     "sk-mock",
		Model:      "mock-tool",
		Workdir:    t.TempDir(),
		ExtraTools: mgr.Tools(),
	})
	if err != nil {
		t.Fatalf("agent.New: %v", err)
	}
	var toolEnd agent.Event
	found := false
	res, err := runner.RunSession(context.Background(), agent.RunRequest{
		System: "you are a test agent", Task: "call echo",
	}, func(e agent.Event) {
		if e.Kind == agent.EventToolEnd && e.Tool == "mcp__alpha__echo" {
			toolEnd, found = e, true
		}
	})
	final := res.Answer
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !found {
		t.Fatalf("beta 挂掉后 alpha 的工具没有被调用，最终答案 %q", final)
	}
	if !strings.Contains(toolEnd.Output, "mcp-echo:still-alive") {
		t.Errorf("alpha 工具输出 = %q", toolEnd.Output)
	}
}

func kindsOf(events []agent.Event) []string {
	out := make([]string, 0, len(events))
	for _, e := range events {
		out = append(out, string(e.Kind)+":"+e.Tool)
	}
	return out
}

func containsString(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

func containsSubstring(xs []string, want string) bool {
	for _, x := range xs {
		if strings.Contains(x, want) {
			return true
		}
	}
	return false
}
