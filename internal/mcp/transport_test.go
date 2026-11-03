package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// newHTTPFakeServer 起一个 streamable HTTP 的假 MCP server（进程内 httptest）。
//
// 这条测试覆盖 defaultDial 的 HTTP 分支：不注入 dial，直接走生产代码路径，
// 否则"URL 配好了但连不上"这类问题永远测不出来。
func newHTTPFakeServer(t *testing.T, tools ...fakeTool) *httptest.Server {
	t.Helper()
	srv := mcpsdk.NewServer(&mcpsdk.Implementation{Name: "http-fake", Version: "test"}, nil)
	for _, ft := range tools {
		addFakeTool(srv, ft)
	}
	h := mcpsdk.NewStreamableHTTPHandler(func(*http.Request) *mcpsdk.Server { return srv }, nil)
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	return ts
}

// TestStreamableHTTPTransport 覆盖：streamable HTTP 的发现与调用。
func TestStreamableHTTPTransport(t *testing.T) {
	ts := newHTTPFakeServer(t, echoTool("echo"))

	m, err := NewManager(Config{Servers: []ServerConfig{{
		Name:       "http",
		URL:        ts.URL,
		HTTPClient: ts.Client(),
	}}})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer m.Close()
	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	tools := toolsByName(t, m)
	echo, ok := tools["mcp__http__echo"]
	if !ok {
		t.Fatalf("HTTP transport 没发现工具: %v", keysOf(tools))
	}
	out, err := echo.InvokableRun(context.Background(), `{"text":"over-http"}`)
	if err != nil {
		t.Fatalf("HTTP 调用: %v", err)
	}
	if out != "echo:over-http" {
		t.Errorf("结果 = %q", out)
	}
	if h := healthOf(t, m, "http"); h.State != StateReady || h.Tools != 1 {
		t.Errorf("Health = %+v", h)
	}
}

// TestStreamableHTTPUnreachable 覆盖：HTTP 端点不可达时 Start 报错（全部失败）。
func TestStreamableHTTPUnreachable(t *testing.T) {
	ts := newHTTPFakeServer(t, echoTool("echo"))
	url := ts.URL
	ts.Close() // 立刻关掉，模拟"server 不在"

	m, err := NewManager(Config{
		Servers:        []ServerConfig{{Name: "http", URL: url, HTTPClient: &http.Client{Timeout: 500 * time.Millisecond}}},
		ConnectTimeout: 500 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer m.Close()
	if err := m.Start(context.Background()); err == nil {
		t.Fatal("端点不可达时 Start 应报错")
	}
	if h := healthOf(t, m, "http"); h.State != StateDown || h.LastError == "" {
		t.Errorf("Health 应含失败原因: %+v", h)
	}
}

// TestCloseInterruptsInflightCall 覆盖：关闭时不该等一个慢调用跑完。
//
// 桌面应用里这条很关键：用户关窗口时，如果 Close 要等 60s 的工具超时，
// 进程就会卡在退出上，看起来像"关不掉"。
func TestCloseInterruptsInflightCall(t *testing.T) {
	hub := newFakeHub(t)
	hub.addServer("slow", fakeTool{Name: "block", Desc: "blocks", Handler: func(ctx context.Context, _ map[string]any) (string, error) {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(10 * time.Second):
			return "too late", nil
		}
	}})
	m := newTestManager(t, hub, Config{
		Servers: []ServerConfig{{Name: "slow", Command: "fake", CallTimeout: 10 * time.Second}},
	})
	mustStart(t, m)

	done := make(chan error, 1)
	go func() {
		_, err := toolsByName(t, m)["mcp__slow__block"].InvokableRun(context.Background(), "{}")
		done <- err
	}()
	time.Sleep(50 * time.Millisecond) // 让调用真的进到网络等待里

	start := time.Now()
	if err := m.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("Close 被 in-flight 调用拖住了 %s", elapsed)
	}

	select {
	case err := <-done:
		if err == nil {
			t.Error("被关闭打断的调用应返回错误")
		} else if !strings.Contains(err.Error(), "mcp:") {
			t.Errorf("错误不像 MCP 调用错误: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Close 之后调用仍未返回")
	}
}
