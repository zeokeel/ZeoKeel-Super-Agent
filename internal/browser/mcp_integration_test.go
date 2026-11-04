package browser

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zeokeel/super-agent/internal/mcp"
)

// TestDefaultMCPStarterOverInjectedTransport runs the real internal/mcp Manager
// (real discovery + tool wrapping) against an in-memory MCP server: no npx, no
// network, no external process.
func TestDefaultMCPStarterOverInjectedTransport(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	dial := func(context.Context, mcp.ServerConfig) (mcpsdk.Transport, error) {
		clientConn, serverConn := net.Pipe()
		srv := mcpsdk.NewServer(&mcpsdk.Implementation{Name: "browser", Version: "test"}, nil)
		srv.AddTool(&mcpsdk.Tool{
			Name: "navigate_page", Description: "open a page",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{"url": map[string]any{"type": "string"}}},
		}, func(context.Context, *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
			return &mcpsdk.CallToolResult{Content: []mcpsdk.Content{
				&mcpsdk.TextContent{Text: "navigated"},
			}}, nil
		})
		go func() { _ = srv.Run(ctx, &mcpsdk.IOTransport{Reader: serverConn, Writer: serverConn}) }()
		return &mcpsdk.IOTransport{Reader: clientConn, Writer: clientConn}, nil
	}
	looked := false
	env := newTestEnv(t, func(o *Options) {
		o.MCPStarter = nil // use the production starter
		o.MCPDial = dial
		o.LookPath = func(string) (string, error) {
			looked = true
			return "", errors.New("lookPath must not run when a transport is injected")
		}
	})
	if _, err := env.m.Start(context.Background(), "ws_web", Config{Enabled: true}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if looked {
		t.Fatal("the default starter probed npx although a fake transport was injected")
	}
	tools, err := env.m.MCPTools("ws_web")
	if err != nil || len(tools) != 1 {
		t.Fatalf("MCPTools = %d/%v", len(tools), err)
	}
	info, err := tools[0].Info(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(info.Name, "mcp__browser__") || !strings.Contains(info.Name, "navigate_page") {
		t.Fatalf("tool name = %q", info.Name)
	}
	out, err := tools[0].InvokableRun(context.Background(), `{"url":"https://example.test"}`)
	if err != nil || out != "navigated" {
		t.Fatalf("tool run = %q/%v", out, err)
	}
	if err := env.m.Stop("ws_web"); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if _, err := env.m.MCPTools("ws_web"); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("MCPTools after Stop = %v", err)
	}
}
