package main

// Runtime ↔ internal/browser adapter: the browser package owns detection/launch/
// MCP; the runtime only sees its BrowserBackend contract.

import (
	"context"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	"github.com/zeokeel/super-agent/internal/browser"
	"github.com/zeokeel/super-agent/internal/runtime"
)

type browserAdapter struct {
	m *browser.Manager
}

func (a *browserAdapter) Detect() []runtime.BrowserInfo {
	if a == nil || a.m == nil {
		return nil
	}
	var out []runtime.BrowserInfo
	for _, b := range a.m.Detect() {
		out = append(out, runtime.BrowserInfo{ID: string(b.ID), Name: b.Name, Path: b.Path})
	}
	return out
}

func (a *browserAdapter) Start(ctx context.Context, wsID, browserID string, headless bool, port int) (int, error) {
	if a == nil || a.m == nil {
		return 0, fmt.Errorf("浏览器后端未接线")
	}
	status, err := a.m.Start(ctx, wsID, browser.Config{
		Enabled: true, Browser: browserID, Headless: headless, Port: port,
	})
	if err != nil {
		return 0, err
	}
	return status.Port, nil
}

func (a *browserAdapter) Stop(_ context.Context, wsID string) error {
	if a == nil || a.m == nil {
		return nil
	}
	return a.m.Stop(wsID)
}

func (a *browserAdapter) Port(wsID string) (int, bool) {
	if a == nil || a.m == nil {
		return 0, false
	}
	st := a.m.Status(wsID)
	if st.State != browser.StateRunning || st.Port == 0 {
		return 0, false
	}
	return st.Port, true
}

func (a *browserAdapter) Status(wsID string) (bool, int, string) {
	if a == nil || a.m == nil {
		return false, 0, "浏览器后端未接线"
	}
	st := a.m.Status(wsID)
	running := st.State == browser.StateRunning
	detail := strings.TrimSpace(st.Detail)
	if running {
		detail = fmt.Sprintf("%s · CDP %d", st.Browser, st.Port)
	}
	if st.MCPState != "" {
		detail += " · MCP " + st.MCPState
		if st.MCPState == "error" && st.MCPDetail != "" {
			detail += "：" + st.MCPDetail
		}
	}
	return running, st.Port, detail
}

func (a *browserAdapter) Tools(wsID string) ([]tool.BaseTool, error) {
	if a == nil || a.m == nil {
		return nil, fmt.Errorf("浏览器后端未接线")
	}
	invokable, err := a.m.MCPTools(wsID)
	if err != nil {
		return nil, err
	}
	out := make([]tool.BaseTool, 0, len(invokable))
	for _, t := range invokable {
		out = append(out, t)
	}
	return out, nil
}

func (a *browserAdapter) ProfileDir(wsID string) string {
	if a == nil || a.m == nil {
		return ""
	}
	return a.m.ProfileDir(wsID)
}

func (a *browserAdapter) Close() error {
	if a == nil || a.m == nil {
		return nil
	}
	return a.m.Close()
}

// startBrowserBackend wires internal/browser into the runtime. A failure is a
// health fact, not fatal: the rest of the app works without a browser.
func startBrowserBackend(rt *runtime.Runtime, stateDir string, logger interface{ Warn(string, ...any) }) *browser.Manager {
	opts := browser.Options{}
	if cmd := rt.BrowserMCPCommand(); cmd != "" {
		opts.MCPCommand = cmd
	}
	m, err := browser.NewManager(stateDir, opts)
	if err != nil {
		if logger != nil {
			logger.Warn("browser backend unavailable", "error", err)
		}
		rt.SetComponentHealth("browser", "error", "浏览器后端不可用："+err.Error())
		return nil
	}
	rt.SetBrowserBackend(&browserAdapter{m: m})
	return m
}
