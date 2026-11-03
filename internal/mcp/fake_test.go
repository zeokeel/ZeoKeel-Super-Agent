package mcp

// 假 MCP server：用官方 SDK 的 server 端 + net.Pipe 实现"stdio 管道"，
// 全程在测试进程内，不依赖任何外部进程或网络。
//
// 为什么不用 CommandTransport 起一个真子进程：验收要求测试不得依赖外部进程，
// 而起子进程还需要 `go build` 一个 helper、处理平台差异。net.Pipe 上的
// IOTransport 走的是与 stdio 完全相同的"换行分隔 JSON-RPC"编解码路径，
// 协议覆盖度一样，但没有进程可泄漏。

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// fakeTool 是假 server 上的一个工具。
type fakeTool struct {
	Name    string
	Desc    string
	IsError bool // 结果带 isError=true（工具级错误，不是协议错误）
	// Handler 收到解析后的参数与 server 侧 ctx；返回的 error 会变成协议级错误。
	Handler func(ctx context.Context, args map[string]any) (string, error)
	// Schema 覆盖默认 inputSchema；nil 时用 {"type":"object",...}
	Schema map[string]any
}

// fakeHub 管理假 server 的创建与销毁，用来模拟崩溃、重启与"配置坏了连不上"。
type fakeHub struct {
	mu      sync.Mutex
	tools   map[string][]fakeTool
	down    map[string]bool // 人为标记不可达：dial 直接失败
	cancels map[string]context.CancelFunc
	dials   map[string]int // 记录重连次数，验证"重启后确实重新 dial 了"
}

func newFakeHub(t *testing.T) *fakeHub {
	t.Helper()
	h := &fakeHub{
		tools:   map[string][]fakeTool{},
		down:    map[string]bool{},
		cancels: map[string]context.CancelFunc{},
		dials:   map[string]int{},
	}
	t.Cleanup(h.stopAll)
	return h
}

func (h *fakeHub) addServer(name string, tools ...fakeTool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.tools[name] = tools
}

func (h *fakeHub) setDown(name string, down bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.down[name] = down
}

func (h *fakeHub) dialCount(name string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.dials[name]
}

// kill 模拟 server 进程崩溃：取消 server ctx 并关闭管道。
func (h *fakeHub) kill(name string) {
	h.mu.Lock()
	cancel := h.cancels[name]
	delete(h.cancels, name)
	h.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (h *fakeHub) stopAll() {
	h.mu.Lock()
	cancels := make([]context.CancelFunc, 0, len(h.cancels))
	for _, c := range h.cancels {
		cancels = append(cancels, c)
	}
	h.cancels = map[string]context.CancelFunc{}
	h.mu.Unlock()
	for _, c := range cancels {
		c()
	}
}

// dial 是注入 Manager 的 transport 工厂：每次调用都起一个新的假 server。
func (h *fakeHub) dial(_ context.Context, cfg ServerConfig) (mcpsdk.Transport, error) {
	h.mu.Lock()
	h.dials[cfg.Name]++
	if h.down[cfg.Name] {
		h.mu.Unlock()
		return nil, fmt.Errorf("fake hub: server %q 不可达", cfg.Name)
	}
	tools := h.tools[cfg.Name]
	clientConn, serverConn := net.Pipe()
	srv := mcpsdk.NewServer(&mcpsdk.Implementation{Name: cfg.Name, Version: "test"}, nil)
	for _, ft := range tools {
		addFakeTool(srv, ft)
	}
	sctx, cancel := context.WithCancel(context.Background())
	h.cancels[cfg.Name] = func() {
		cancel()
		_ = serverConn.Close()
	}
	h.mu.Unlock()

	go func() {
		defer serverConn.Close()
		_ = srv.Run(sctx, &mcpsdk.IOTransport{Reader: serverConn, Writer: serverConn})
	}()
	return &mcpsdk.IOTransport{Reader: clientConn, Writer: clientConn}, nil
}

// addFakeTool 把 fakeTool 注册到任意 MCP server 上（stdio 与 HTTP 共用）。
func addFakeTool(srv *mcpsdk.Server, ft fakeTool) {
	schema := ft.Schema
	if schema == nil {
		schema = map[string]any{
			"type":       "object",
			"properties": map[string]any{"text": map[string]any{"type": "string"}},
		}
	}
	srv.AddTool(&mcpsdk.Tool{Name: ft.Name, Description: ft.Desc, InputSchema: schema},
		func(ctx context.Context, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
			args := map[string]any{}
			if req != nil && req.Params != nil && len(req.Params.Arguments) > 0 {
				if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
					return nil, err
				}
			}
			text, err := ft.Handler(ctx, args)
			if err != nil {
				return nil, err
			}
			return &mcpsdk.CallToolResult{
				Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: text}},
				IsError: ft.IsError,
			}, nil
		})
}

// echoTool 是最常用的假工具：把 text 原样返回并加前缀。
func echoTool(name string) fakeTool {
	return fakeTool{
		Name: name,
		Desc: "echo the given text",
		Handler: func(_ context.Context, args map[string]any) (string, error) {
			text, _ := args["text"].(string)
			return "echo:" + text, nil
		},
	}
}
