package browser

import (
	"context"
	"fmt"
	"os/exec"

	"github.com/cloudwego/eino/components/tool"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zeokeel/super-agent/internal/mcp"
)

const defaultMCPCommand = "npx"

var defaultMCPArgs = []string{"-y", "chrome-devtools-mcp@latest"}

// defaultMCPStarter runs chrome-devtools-mcp as an MCP stdio server through
// internal/mcp. Browser tests inject a fake MCPStarter and never start npx.
type defaultMCPStarter struct {
	command  string
	args     []string
	lookPath func(string) (string, error)
	dial     func(ctx context.Context, cfg mcp.ServerConfig) (mcpsdk.Transport, error)
}

func (s *defaultMCPStarter) Start(ctx context.Context, spec MCPSpec) (MCPHandle, error) {
	command := spec.Command
	if command == "" {
		command = s.command
	}
	if command == "" {
		command = defaultMCPCommand
	}
	args := spec.Args
	if len(args) == 0 {
		args = s.args
	}
	if len(args) == 0 {
		args = defaultMCPArgs
	}
	if s.dial == nil {
		if _, err := s.lookPath(command); err != nil {
			return nil, fmt.Errorf("%w: 未找到 %q；安装 Node.js（含 npx）或检查配置的命令路径: %v",
				ErrNodeMissing, command, err)
		}
	}
	serverArgs := mcpServerArgs(args, spec.BrowserURL)
	cfg := mcp.Config{Servers: []mcp.ServerConfig{{
		Name:    "browser",
		Command: command,
		Args:    serverArgs,
	}}}
	var (
		mgr *mcp.Manager
		err error
	)
	if s.dial != nil {
		mgr, err = mcp.NewManagerWithDial(cfg, s.dial)
	} else {
		mgr, err = mcp.NewManager(cfg)
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMCPStart, err)
	}
	if err := mgr.Start(ctx); err != nil {
		_ = mgr.Close()
		return nil, fmt.Errorf("%w: %v", ErrMCPStart, err)
	}
	handle := &defaultMCPHandle{mgr: mgr}
	if len(handle.Tools()) == 0 {
		_ = handle.Close()
		return nil, fmt.Errorf("%w: server 没有返回任何工具", ErrMCPStart)
	}
	return handle, nil
}

type defaultMCPHandle struct{ mgr *mcp.Manager }

func (h *defaultMCPHandle) Tools() []tool.InvokableTool {
	base := h.mgr.Tools()
	out := make([]tool.InvokableTool, 0, len(base))
	for _, bt := range base {
		if it, ok := bt.(tool.InvokableTool); ok {
			out = append(out, it)
		}
	}
	return out
}

func (h *defaultMCPHandle) Close() error { return h.mgr.Close() }

// mcpServerArgs is the exact argv handed to chrome-devtools-mcp (configurable
// base args + the browser URL).
func mcpServerArgs(base []string, browserURL string) []string {
	return append(append([]string(nil), base...), "--browserUrl", browserURL)
}

// newDefaultMCPStarter wires the production starter with the configured
// command/args and a lookPath seam (tests can replace the starter entirely).
func newDefaultMCPStarter(command string, args []string, lookPath func(string) (string, error),
	dial func(ctx context.Context, cfg mcp.ServerConfig) (mcpsdk.Transport, error)) MCPStarter {
	if command == "" {
		command = defaultMCPCommand
	}
	if len(args) == 0 {
		args = defaultMCPArgs
	}
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	return &defaultMCPStarter{command: command, args: args, lookPath: lookPath, dial: dial}
}
