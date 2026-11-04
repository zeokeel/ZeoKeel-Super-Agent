package browser

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	"github.com/cloudwego/eino/components/tool"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zeokeel/super-agent/internal/mcp"
)

var workspaceIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// Options are the manager seams. Zero values give production behaviour; tests
// replace Launcher/Prober/MCPStarter so no real browser or npx is started.
type Options struct {
	// Launcher starts the browser child process (default exec).
	Launcher Launcher
	// Prober waits for http://127.0.0.1:<port>/json/version (default HTTP).
	Prober Prober
	// Detector returns installed browsers (default Detect()).
	Detector func() []Browser
	// MCPStarter starts chrome-devtools-mcp (default internal/mcp over npx).
	MCPStarter MCPStarter
	// LookPath is the executable probe used by the default MCP starter.
	LookPath func(string) (string, error)
	// MCPCommand/MCPArgs configure the MCP server command
	// (default npx -y chrome-devtools-mcp@latest).
	MCPCommand string
	MCPArgs    []string
	// MCPDial is a test/embedding seam: when set, the default MCP starter uses it
	// instead of spawning the real command (no npx, no network).
	MCPDial func(ctx context.Context, cfg mcp.ServerConfig) (mcpsdk.Transport, error)
	// StartTimeout bounds browser readiness + MCP startup.
	StartTimeout time.Duration
	// StopTimeout bounds the graceful (SIGTERM) wait before Kill.
	StopTimeout time.Duration
	// Now is a clock seam for tests.
	Now func() time.Time
}

// Manager owns one browser + MCP server per workspace.
type Manager struct {
	stateDir string
	opts     Options

	mu     sync.Mutex
	ws     map[string]*workspace
	ports  map[int]string // port -> workspace
	closed bool
}

type workspace struct {
	mu     sync.Mutex
	cfg    Config
	status Status
	proc   Process
	mcp    MCPHandle
}

// NewManager builds the manager for one app state directory. Browsers are
// started lazily by Start.
func NewManager(stateDir string, opts Options) (*Manager, error) {
	if len(stateDir) == 0 {
		return nil, fmt.Errorf("%w: state dir is empty", ErrInvalidConfig)
	}
	if opts.Launcher == nil {
		opts.Launcher = execLauncher{}
	}
	if opts.Prober == nil {
		opts.Prober = newHTTPProber()
	}
	if opts.Detector == nil {
		opts.Detector = Detect
	}
	if opts.MCPStarter == nil {
		opts.MCPStarter = newDefaultMCPStarter(opts.MCPCommand, opts.MCPArgs, opts.LookPath, opts.MCPDial)
	}
	if opts.StartTimeout <= 0 {
		opts.StartTimeout = defaultStartTimeout
	}
	if opts.StopTimeout <= 0 {
		opts.StopTimeout = defaultStopTimeout
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &Manager{
		stateDir: stateDir,
		opts:     opts,
		ws:       map[string]*workspace{},
		ports:    map[int]string{},
	}, nil
}

// Detect returns the supported browsers on this machine (default paths only).
func (m *Manager) Detect() []Browser { return m.opts.Detector() }

// ProfileDir is the isolated user-data directory for one workspace
// (<stateDir>/workspaces/<wsID>/browser-profile); "" for an invalid id.
func (m *Manager) ProfileDir(wsID string) string {
	if !workspaceIDPattern.MatchString(wsID) {
		return ""
	}
	return filepath.Join(m.stateDir, "workspaces", wsID, "browser-profile")
}

// validateConfig checks the fields the caller controls.
func (m *Manager) validateConfig(wsID string, cfg Config) error {
	if !workspaceIDPattern.MatchString(wsID) {
		return fmt.Errorf("%w: %q（只允许 A-Za-z0-9_-，最长 64）", ErrInvalidWorkspace, wsID)
	}
	if cfg.Port != 0 && (cfg.Port < 1024 || cfg.Port > 65535) {
		return fmt.Errorf("%w: 端口 %d 超出 1024-65535", ErrInvalidConfig, cfg.Port)
	}
	switch cfg.Browser {
	case "", string(Chrome), string(Edge):
	default:
		return fmt.Errorf("%w: 浏览器 %q（chrome / edge / 留空自动）", ErrInvalidConfig, cfg.Browser)
	}
	return nil
}

func (m *Manager) workspace(wsID string) *workspace {
	m.mu.Lock()
	defer m.mu.Unlock()
	w := m.ws[wsID]
	if w == nil {
		w = &workspace{status: Status{Workspace: wsID, State: StateStopped}}
		m.ws[wsID] = w
	}
	return w
}

// Start launches the workspace browser (and its MCP server) or returns the
// existing running instance (idempotent). Enabled=false stops it and reports
// disabled.
func (m *Manager) Start(ctx context.Context, wsID string, cfg Config) (Status, error) {
	m.mu.Lock()
	closed := m.closed
	m.mu.Unlock()
	if closed {
		return Status{Workspace: wsID, State: StateError, Detail: ErrClosed.Error()}, ErrClosed
	}
	if err := m.validateConfig(wsID, cfg); err != nil {
		return Status{Workspace: wsID, State: StateError, Detail: err.Error()}, err
	}
	w := m.workspace(wsID)
	w.mu.Lock()
	defer w.mu.Unlock()

	if !cfg.Enabled {
		if stopErr := m.stopLocked(wsID, w); stopErr != nil {
			return w.status, stopErr
		}
		w.status = Status{Workspace: wsID, State: StateDisabled, Detail: "未启用浏览器",
			ProfileDir: m.ProfileDir(wsID)}
		_ = writeStatusFile(m.stateDir, wsID, w.status)
		return w.status, nil
	}
	if w.proc != nil && (w.status.State == StateRunning || w.status.State == StateStarting) {
		return w.status, nil // idempotent: one browser per workspace
	}
	if w.proc != nil || w.mcp != nil {
		_ = m.stopLocked(wsID, w)
	}

	detected := m.Detect()
	b, ok := browserByID(detected, cfg.Browser)
	if !ok {
		detail := "未检测到 Google Chrome 或 Microsoft Edge（只查默认安装路径）；安装后重试"
		w.status = Status{Workspace: wsID, State: StateError, Detail: detail,
			ProfileDir: m.ProfileDir(wsID), MCPState: "disabled"}
		_ = writeStatusFile(m.stateDir, wsID, w.status)
		return w.status, fmt.Errorf("%w: %s", ErrBrowserNotDetected, detail)
	}
	port, err := m.pickPort(wsID, cfg.Port)
	if err != nil {
		w.status = Status{Workspace: wsID, State: StateError, Detail: err.Error(),
			Browser: string(b.ID), BrowserPath: b.Path, ProfileDir: m.ProfileDir(wsID)}
		_ = writeStatusFile(m.stateDir, wsID, w.status)
		return w.status, err
	}
	profileDir := m.ProfileDir(wsID)
	if err := os.MkdirAll(profileDir, 0o700); err != nil {
		m.releasePort(port)
		w.status = Status{Workspace: wsID, State: StateError, Detail: "无法创建浏览器 profile：" + err.Error()}
		_ = writeStatusFile(m.stateDir, wsID, w.status)
		return w.status, err
	}
	devtoolsURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	args := launchArgs(port, profileDir, cfg.Headless)
	w.status = Status{Workspace: wsID, State: StateStarting,
		Detail: "启动浏览器…", Browser: string(b.ID), BrowserPath: b.Path,
		Port: port, Headless: cfg.Headless, ProfileDir: profileDir, DevToolsURL: devtoolsURL,
		MCPState: "starting", StartedAt: m.opts.Now()}
	_ = writeStatusFile(m.stateDir, wsID, w.status)

	proc, err := m.opts.Launcher.Start(ctx, LaunchSpec{
		Executable: b.Path, Args: args, Env: scrubEnv(os.Environ()),
		Workspace: wsID, ProfileDir: profileDir, Port: port,
		Headless: cfg.Headless, DevToolsURL: devtoolsURL,
	})
	if err != nil {
		m.releasePort(port)
		detail := "无法启动浏览器：" + err.Error()
		w.status.State, w.status.Detail = StateError, detail
		_ = writeStatusFile(m.stateDir, wsID, w.status)
		return w.status, err
	}
	w.proc = proc
	w.status.PID = proc.PID()

	readyCtx, cancel := context.WithTimeout(ctx, m.opts.StartTimeout)
	probeErr := m.opts.Prober.Wait(readyCtx, devtoolsURL)
	cancel()
	if probeErr != nil {
		_ = proc.Kill()
		_ = proc.Wait()
		w.proc = nil
		m.releasePort(port)
		detail := "浏览器没有在 " + m.opts.StartTimeout.String() + " 内就绪（DevTools 端点无响应）"
		w.status.State, w.status.Detail, w.status.MCPState = StateError, detail, "disabled"
		_ = writeStatusFile(m.stateDir, wsID, w.status)
		return w.status, fmt.Errorf("%w: %v", ErrStartTimeout, probeErr)
	}

	mcpCtx, cancel := context.WithTimeout(ctx, m.opts.StartTimeout)
	handle, mcpErr := m.opts.MCPStarter.Start(mcpCtx, MCPSpec{
		Workspace: wsID, BrowserURL: devtoolsURL,
		Command: m.opts.MCPCommand, Args: m.opts.MCPArgs,
	})
	cancel()
	if mcpErr != nil {
		_ = proc.Kill()
		_ = proc.Wait()
		w.proc = nil
		m.releasePort(port)
		detail := "浏览器已启动，但 chrome-devtools MCP 启动失败：" + mcpErr.Error()
		w.status.State, w.status.Detail = StateError, detail
		w.status.MCPState, w.status.MCPDetail = "error", mcpErr.Error()
		_ = writeStatusFile(m.stateDir, wsID, w.status)
		return w.status, fmt.Errorf("%w: %w", ErrMCPStart, mcpErr)
	}
	w.mcp = handle
	w.cfg = cfg
	w.status.State = StateRunning
	w.status.Detail = "浏览器已就绪"
	w.status.MCPState, w.status.MCPDetail = "ready", ""
	_ = writeStatusFile(m.stateDir, wsID, w.status)
	return w.status, nil
}

// Stop terminates the workspace's MCP server and browser, gracefully first and
// with Kill after StopTimeout. Idempotent; profile data is kept.
func (m *Manager) Stop(wsID string) error {
	if !workspaceIDPattern.MatchString(wsID) {
		return fmt.Errorf("%w: %q", ErrInvalidWorkspace, wsID)
	}
	w := m.workspace(wsID)
	w.mu.Lock()
	defer w.mu.Unlock()
	return m.stopLocked(wsID, w)
}

func (m *Manager) stopLocked(wsID string, w *workspace) error {
	var errs []error
	if w.mcp != nil {
		if err := w.mcp.Close(); err != nil {
			errs = append(errs, err)
		}
		w.mcp = nil
	}
	if w.proc != nil {
		proc := w.proc
		w.proc = nil
		if err := proc.Signal(terminateSignal()); err != nil {
			// Already gone is fine; anything else is worth reporting after kill.
			if !processGone(err) {
				errs = append(errs, err)
			}
		}
		done := make(chan error, 1)
		go func() { done <- proc.Wait() }()
		if !waitDone(done, m.opts.StopTimeout) {
			if err := proc.Kill(); err != nil && !processGone(err) {
				errs = append(errs, err)
			}
			<-done
		}
		m.releasePort(w.status.Port)
	}
	profileDir := m.ProfileDir(wsID)
	w.status = Status{Workspace: wsID, State: StateStopped, Detail: "已停止",
		ProfileDir: profileDir}
	if err := writeStatusFile(m.stateDir, wsID, w.status); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// Status returns the last known state (never blocks on the browser).
func (m *Manager) Status(wsID string) Status {
	if !workspaceIDPattern.MatchString(wsID) {
		return Status{Workspace: wsID, State: StateError, Detail: ErrInvalidWorkspace.Error()}
	}
	w := m.workspace(wsID)
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.status
}

// MCPTools returns the discovered chrome-devtools tools for one workspace, to be
// injected into the Worker through agent.Config.ExtraTools. It returns an error
// with an actionable detail when the browser/MCP is not running.
func (m *Manager) MCPTools(wsID string) ([]tool.InvokableTool, error) {
	if !workspaceIDPattern.MatchString(wsID) {
		return nil, fmt.Errorf("%w: %q", ErrInvalidWorkspace, wsID)
	}
	w := m.workspace(wsID)
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.mcp == nil {
		if w.status.MCPState == "error" {
			return nil, fmt.Errorf("%w: %s", ErrMCPStart, w.status.MCPDetail)
		}
		return nil, fmt.Errorf("%w: %s", ErrNotRunning, wsID)
	}
	tools := w.mcp.Tools()
	return append([]tool.InvokableTool(nil), tools...), nil
}

// Close stops every workspace's browser/MCP. Idempotent; later Start calls fail
// with ErrClosed.
func (m *Manager) Close() error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	ids := make([]string, 0, len(m.ws))
	for id := range m.ws {
		ids = append(ids, id)
	}
	m.mu.Unlock()
	var errs []error
	for _, id := range ids {
		w := m.workspace(id)
		w.mu.Lock()
		if err := m.stopLocked(id, w); err != nil {
			errs = append(errs, err)
		}
		w.mu.Unlock()
	}
	return errors.Join(errs...)
}

// pickPort reserves one debugging port for a workspace. m.mu guards the
// reservation table; it is never held while waiting on a workspace lock.
func (m *Manager) pickPort(wsID string, requested int) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if requested != 0 {
		if owner, used := m.ports[requested]; used && owner != wsID {
			return 0, fmt.Errorf("%w: 端口 %d 已被工作区 %q 使用；改用 0 自动分配", ErrPortUnavailable, requested, owner)
		}
		if !portFree(requested) {
			return 0, fmt.Errorf("%w: 端口 %d 在本机不可用", ErrPortUnavailable, requested)
		}
		m.ports[requested] = wsID
		return requested, nil
	}
	for attempt := 0; attempt < 20; attempt++ {
		port, err := freeLoopbackPort()
		if err != nil {
			return 0, fmt.Errorf("%w: 无法分配调试端口: %v", ErrPortUnavailable, err)
		}
		if port < 1024 || port > 65535 {
			continue
		}
		if _, used := m.ports[port]; used {
			continue
		}
		m.ports[port] = wsID
		return port, nil
	}
	return 0, fmt.Errorf("%w: 20 次尝试都没有拿到空闲端口", ErrPortUnavailable)
}

func (m *Manager) releasePort(port int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.releasePortLocked(port)
}

func freeLoopbackPort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	addr, ok := l.Addr().(*net.TCPAddr)
	if !ok {
		return 0, fmt.Errorf("unexpected listener address %T", l.Addr())
	}
	return addr.Port, nil
}

func portFree(port int) bool {
	l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return false
	}
	_ = l.Close()
	return true
}

func processGone(err error) bool {
	return errors.Is(err, os.ErrProcessDone) || errors.Is(err, os.ErrClosed)
}
