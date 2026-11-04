package browser

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

// --- fakes -------------------------------------------------------------------

type fakeProcess struct {
	pid int

	mu     sync.Mutex
	signal os.Signal
	killed bool
	exit   chan struct{}
	once   sync.Once
}

func newFakeProcess(pid int) *fakeProcess {
	return &fakeProcess{pid: pid, exit: make(chan struct{})}
}

func (p *fakeProcess) PID() int { return p.pid }
func (p *fakeProcess) Signal(sig os.Signal) error {
	p.mu.Lock()
	p.signal = sig
	p.mu.Unlock()
	p.once.Do(func() { close(p.exit) })
	return nil
}
func (p *fakeProcess) Kill() error {
	p.mu.Lock()
	p.killed = true
	p.mu.Unlock()
	p.once.Do(func() { close(p.exit) })
	return nil
}
func (p *fakeProcess) Wait() error {
	<-p.exit
	return nil
}
func (p *fakeProcess) wasKilled() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.killed
}
func (p *fakeProcess) gotSignal() os.Signal {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.signal
}

// stubbornProcess ignores the graceful signal and only exits on Kill (Stop must
// fall back to Kill after StopTimeout).
type stubbornProcess struct {
	*fakeProcess
	dead chan struct{}
}

func (s *stubbornProcess) Signal(os.Signal) error { return nil }
func (s *stubbornProcess) Kill() error {
	s.mu.Lock()
	s.killed = true
	s.mu.Unlock()
	s.once.Do(func() { close(s.dead) })
	return nil
}
func (s *stubbornProcess) Wait() error {
	<-s.dead
	return nil
}

type fakeLauncher struct {
	mu       sync.Mutex
	specs    []LaunchSpec
	procs    []*fakeProcess
	startErr error
	stubborn bool
}

func (l *fakeLauncher) Start(_ context.Context, spec LaunchSpec) (Process, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.specs = append(l.specs, spec)
	if l.startErr != nil {
		return nil, l.startErr
	}
	p := newFakeProcess(4000 + len(l.procs))
	l.procs = append(l.procs, p)
	if l.stubborn {
		return &stubbornProcess{fakeProcess: p, dead: make(chan struct{})}, nil
	}
	return p, nil
}
func (l *fakeLauncher) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.specs)
}
func (l *fakeLauncher) spec() LaunchSpec {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.specs[len(l.specs)-1]
}

type fakeProber struct {
	err  error
	urls []string
}

func (p *fakeProber) Wait(_ context.Context, url string) error {
	p.urls = append(p.urls, url)
	return p.err
}

type fakeMCPTool struct{ name string }

func (f *fakeMCPTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: f.name, Desc: "fake browser tool"}, nil
}
func (f *fakeMCPTool) InvokableRun(context.Context, string, ...tool.Option) (string, error) {
	return "ok", nil
}

type fakeMCPHandle struct {
	tools  []tool.InvokableTool
	closed *int
}

func (h *fakeMCPHandle) Tools() []tool.InvokableTool { return h.tools }
func (h *fakeMCPHandle) Close() error {
	if h.closed != nil {
		*h.closed++
	}
	return nil
}

type fakeMCPStarter struct {
	mu     sync.Mutex
	specs  []MCPSpec
	tools  []tool.InvokableTool
	err    error
	closed int
}

func (s *fakeMCPStarter) Start(_ context.Context, spec MCPSpec) (MCPHandle, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.specs = append(s.specs, spec)
	if s.err != nil {
		return nil, s.err
	}
	return &fakeMCPHandle{tools: s.tools, closed: &s.closed}, nil
}
func (s *fakeMCPStarter) spec() MCPSpec {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.specs[len(s.specs)-1]
}

func fakeDetector() []Browser {
	return []Browser{{ID: Chrome, Name: "Google Chrome", Path: "/fake/chrome"}, {ID: Edge, Name: "Microsoft Edge", Path: "/fake/edge"}}
}

type testEnv struct {
	m      *Manager
	launch *fakeLauncher
	probe  *fakeProber
	mcp    *fakeMCPStarter
	dir    string
}

func newTestEnv(t *testing.T, tweak func(*Options)) *testEnv {
	t.Helper()
	dir := t.TempDir()
	launch := &fakeLauncher{}
	probe := &fakeProber{}
	mcpStarter := &fakeMCPStarter{tools: []tool.InvokableTool{&fakeMCPTool{name: "mcp__browser__navigate_page"}}}
	opts := Options{
		Launcher: launch, Prober: probe, Detector: fakeDetector, MCPStarter: mcpStarter,
		StartTimeout: 2 * time.Second, StopTimeout: 200 * time.Millisecond,
		Now: func() time.Time { return time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC) },
	}
	if tweak != nil {
		tweak(&opts)
	}
	m, err := NewManager(dir, opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })
	te := &testEnv{m: m, launch: launch, probe: probe, mcp: mcpStarter, dir: dir}
	if l, ok := opts.Launcher.(*fakeLauncher); ok {
		te.launch = l
	}
	if p, ok := opts.Prober.(*fakeProber); ok {
		te.probe = p
	}
	if st, ok := opts.MCPStarter.(*fakeMCPStarter); ok {
		te.mcp = st
	}
	return te
}

func freeTestPort(t *testing.T) int {
	t.Helper()
	p, err := freeLoopbackPort()
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// --- tests -------------------------------------------------------------------

func TestStartLaunchesIsolatedBrowserAndMCP(t *testing.T) {
	env := newTestEnv(t, nil)
	port := freeTestPort(t)
	status, err := env.m.Start(context.Background(), "ws_web", Config{Enabled: true, Browser: "chrome", Headless: true, Port: port})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if status.State != StateRunning || status.Port != port || status.PID == 0 {
		t.Fatalf("status = %+v", status)
	}
	if status.MCPState != "ready" || len(env.mcp.specs) != 1 {
		t.Fatalf("mcp = %+v", status)
	}
	spec := env.launch.spec()
	if spec.Executable != "/fake/chrome" {
		t.Fatalf("executable = %q", spec.Executable)
	}
	wantProfile := filepath.Join(env.dir, "workspaces", "ws_web", "browser-profile")
	if spec.ProfileDir != wantProfile || status.ProfileDir != wantProfile {
		t.Fatalf("profile = %q / %q", spec.ProfileDir, status.ProfileDir)
	}
	joined := strings.Join(spec.Args, " ")
	for _, want := range []string{
		"--remote-debugging-port=" + itoa(port),
		"--user-data-dir=" + wantProfile,
		"--no-first-run",
		"--no-default-browser-check",
		"--headless=new",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("args %q miss %q", joined, want)
		}
	}
	mcpSpec := env.mcp.spec()
	if mcpSpec.Workspace != "ws_web" || mcpSpec.BrowserURL != status.DevToolsURL {
		t.Fatalf("mcp spec = %+v, devtools = %q", mcpSpec, status.DevToolsURL)
	}
	if len(env.probe.urls) != 1 || env.probe.urls[0] != status.DevToolsURL {
		t.Fatalf("probe urls = %v", env.probe.urls)
	}
	tools, err := env.m.MCPTools("ws_web")
	if err != nil || len(tools) != 1 {
		t.Fatalf("MCPTools = %d/%v", len(tools), err)
	}
	// Persisted facts: pid/port/workspace under the state dir, 0600.
	path := filepath.Join(env.dir, "workspaces", "ws_web", "browser.json")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("state file: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("state file mode = %v", info.Mode().Perm())
	}
	raw, _ := os.ReadFile(path)
	for _, want := range []string{`"pid"`, `"port"`, `"workspace": "ws_web"`} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("state file misses %q: %s", want, raw)
		}
	}
}

func TestStartIsIdempotent(t *testing.T) {
	env := newTestEnv(t, nil)
	cfg := Config{Enabled: true, Browser: "edge"}
	first, err := env.m.Start(context.Background(), "ws_web", cfg)
	if err != nil {
		t.Fatal(err)
	}
	second, err := env.m.Start(context.Background(), "ws_web", cfg)
	if err != nil {
		t.Fatal(err)
	}
	if first.PID != second.PID || first.Port != second.Port {
		t.Fatalf("second start relaunched: %+v vs %+v", first, second)
	}
	if env.launch.count() != 1 || len(env.mcp.specs) != 1 {
		t.Fatalf("launches=%d mcp=%d", env.launch.count(), len(env.mcp.specs))
	}
}

func TestStartDisabledStopsAndReportsDisabled(t *testing.T) {
	env := newTestEnv(t, nil)
	if _, err := env.m.Start(context.Background(), "ws_web", Config{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	status, err := env.m.Start(context.Background(), "ws_web", Config{Enabled: false})
	if err != nil {
		t.Fatal(err)
	}
	if status.State != StateDisabled || status.PID != 0 {
		t.Fatalf("disabled status = %+v", status)
	}
	if !env.launch.procs[0].wasKilled() && env.launch.procs[0].gotSignal() == nil {
		t.Fatal("disabled channel did not stop the browser")
	}
	if _, err := env.m.MCPTools("ws_web"); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("MCPTools after disable = %v", err)
	}
}

func TestPortCollisionAcrossWorkspaces(t *testing.T) {
	env := newTestEnv(t, nil)
	port := freeTestPort(t)
	if _, err := env.m.Start(context.Background(), "ws_a", Config{Enabled: true, Port: port}); err != nil {
		t.Fatal(err)
	}
	_, err := env.m.Start(context.Background(), "ws_b", Config{Enabled: true, Port: port})
	if !errors.Is(err, ErrPortUnavailable) {
		t.Fatalf("collision error = %v", err)
	}
	status := env.m.Status("ws_b")
	if status.State != StateError {
		t.Fatalf("ws_b status = %+v", status)
	}
	// Auto allocation never reuses the reserved port.
	other, err := env.m.Start(context.Background(), "ws_c", Config{Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if other.Port == port {
		t.Fatalf("auto port reused reserved port %d", port)
	}
}

func TestReadinessFailureKillsAndReleasesPort(t *testing.T) {
	env := newTestEnv(t, func(o *Options) {
		o.Prober = &fakeProber{err: errors.New("connection refused")}
	})
	port := freeTestPort(t)
	_, err := env.m.Start(context.Background(), "ws_web", Config{Enabled: true, Port: port})
	if !errors.Is(err, ErrStartTimeout) {
		t.Fatalf("readiness error = %v", err)
	}
	if len(env.launch.procs) != 1 || !env.launch.procs[0].wasKilled() {
		t.Fatal("a browser that never became ready was not killed")
	}
	if len(env.mcp.specs) != 0 {
		t.Fatal("MCP started although the browser was not ready")
	}
	if !portFree(port) {
		t.Fatalf("port %d still reserved after failure", port)
	}
	if status := env.m.Status("ws_web"); status.State != StateError {
		t.Fatalf("status = %+v", status)
	}
}

func TestBrowserMissingIsActionable(t *testing.T) {
	env := newTestEnv(t, func(o *Options) {
		o.Detector = func() []Browser { return nil }
	})
	_, err := env.m.Start(context.Background(), "ws_web", Config{Enabled: true})
	if !errors.Is(err, ErrBrowserNotDetected) {
		t.Fatalf("error = %v", err)
	}
	status := env.m.Status("ws_web")
	if status.State != StateError || !strings.Contains(status.Detail, "Chrome") || !strings.Contains(status.Detail, "Edge") {
		t.Fatalf("status = %+v", status)
	}
	if len(env.launch.specs) != 0 {
		t.Fatal("launched a browser although none was detected")
	}
}

func TestMCPFailureStopsBrowserAndReportsAction(t *testing.T) {
	env := newTestEnv(t, func(o *Options) {
		o.MCPStarter = &fakeMCPStarter{err: ErrNodeMissing}
	})
	_, err := env.m.Start(context.Background(), "ws_web", Config{Enabled: true})
	if !errors.Is(err, ErrMCPStart) || !errors.Is(err, ErrNodeMissing) {
		t.Fatalf("error = %v", err)
	}
	if len(env.launch.procs) != 1 || !env.launch.procs[0].wasKilled() {
		t.Fatal("browser left running after MCP failure")
	}
	status := env.m.Status("ws_web")
	if status.State != StateError || status.MCPState != "error" || !strings.Contains(status.MCPDetail, "browser: npx/node not found") {
		t.Fatalf("status = %+v", status)
	}
	if _, err := env.m.MCPTools("ws_web"); !errors.Is(err, ErrMCPStart) {
		t.Fatalf("MCPTools error = %v", err)
	}
}

func TestStopGracefulAndTimeoutFallback(t *testing.T) {
	env := newTestEnv(t, nil)
	if _, err := env.m.Start(context.Background(), "ws_web", Config{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := env.m.Stop("ws_web"); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if sig := env.launch.procs[0].gotSignal(); sig == nil {
		t.Fatal("Stop did not signal the browser gracefully")
	}
	if env.mcp.closed == 0 {
		t.Fatal("Stop did not close the MCP server")
	}
	if status := env.m.Status("ws_web"); status.State != StateStopped || status.PID != 0 {
		t.Fatalf("stopped status = %+v", status)
	}
	if err := env.m.Stop("ws_web"); err != nil {
		t.Fatalf("second Stop: %v", err)
	}

	stubborn := newTestEnv(t, func(o *Options) { o.Launcher = &fakeLauncher{stubborn: true}; o.StopTimeout = 30 * time.Millisecond })
	if _, err := stubborn.m.Start(context.Background(), "ws_web", Config{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := stubborn.m.Stop("ws_web"); err != nil {
		t.Fatalf("stubborn Stop: %v", err)
	}
	if !stubborn.launch.procs[0].wasKilled() {
		t.Fatal("a browser that ignored SIGTERM was not killed after the timeout")
	}
}

func TestProfileDirValidationAndIsolation(t *testing.T) {
	env := newTestEnv(t, nil)
	got := env.m.ProfileDir("ws_ok-1")
	want := filepath.Join(env.dir, "workspaces", "ws_ok-1", "browser-profile")
	if got != want {
		t.Fatalf("ProfileDir = %q, want %q", got, want)
	}
	if env.m.ProfileDir("../escape") != "" || env.m.ProfileDir("a/b") != "" {
		t.Fatal("an unsafe workspace id produced a profile dir")
	}
	if _, err := env.m.Start(context.Background(), "../escape", Config{Enabled: true}); !errors.Is(err, ErrInvalidWorkspace) {
		t.Fatalf("invalid workspace error = %v", err)
	}
	if _, err := env.m.Start(context.Background(), "ws", Config{Enabled: true, Port: 80}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("invalid port error = %v", err)
	}
	if _, err := env.m.Start(context.Background(), "ws", Config{Enabled: true, Browser: "safari"}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("invalid browser error = %v", err)
	}
}

func TestCloseStopsEveryWorkspaceAndIsFinal(t *testing.T) {
	env := newTestEnv(t, nil)
	for _, ws := range []string{"ws_a", "ws_b"} {
		if _, err := env.m.Start(context.Background(), ws, Config{Enabled: true}); err != nil {
			t.Fatal(err)
		}
	}
	if err := env.m.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	for _, p := range env.launch.procs {
		if !p.wasKilled() && p.gotSignal() == nil {
			t.Fatal("Close left a browser running")
		}
	}
	if err := env.m.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if _, err := env.m.Start(context.Background(), "ws_a", Config{Enabled: true}); !errors.Is(err, ErrClosed) {
		t.Fatalf("Start after Close = %v", err)
	}
}

func TestMCPToolsRequiresRunningBrowser(t *testing.T) {
	env := newTestEnv(t, nil)
	if _, err := env.m.MCPTools("ws_web"); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("MCPTools before start = %v", err)
	}
	if _, err := env.m.Start(context.Background(), "ws_web", Config{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	tools, err := env.m.MCPTools("ws_web")
	if err != nil || len(tools) != 1 {
		t.Fatalf("MCPTools = %v/%d", err, len(tools))
	}
	info, err := tools[0].Info(context.Background())
	if err != nil || info.Name == "" {
		t.Fatalf("tool info = %+v/%v", info, err)
	}
}

func TestMCPCommandConfigurable(t *testing.T) {
	env := newTestEnv(t, func(o *Options) {
		o.MCPCommand = "node"
		o.MCPArgs = []string{"custom-mcp.js"}
	})
	if _, err := env.m.Start(context.Background(), "ws_web", Config{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	spec := env.mcp.spec()
	if spec.Command != "node" || strings.Join(spec.Args, " ") != "custom-mcp.js" {
		t.Fatalf("mcp spec = %+v", spec)
	}
}

func TestScrubEnvAndMCPArgs(t *testing.T) {
	env := []string{
		"HOME=/home/me", "PATH=/usr/bin", "DISPLAY=:0",
		"OPENAI_API_KEY=sk-secret", "GITHUB_TOKEN=ghp_x", "DB_PASSWORD=pw", "AUTH_COOKIE=c",
	}
	scrubbed := scrubEnv(env)
	joined := strings.Join(scrubbed, " ")
	for _, kept := range []string{"HOME=/home/me", "PATH=/usr/bin", "DISPLAY=:0"} {
		if !strings.Contains(joined, kept) {
			t.Fatalf("scrub dropped %q: %v", kept, scrubbed)
		}
	}
	for _, dropped := range []string{"OPENAI_API_KEY", "GITHUB_TOKEN", "DB_PASSWORD", "AUTH_COOKIE"} {
		if strings.Contains(joined, dropped) {
			t.Fatalf("scrub left %q: %v", dropped, scrubbed)
		}
	}
	args := mcpServerArgs([]string{"-y", "chrome-devtools-mcp@latest"}, "http://127.0.0.1:9333")
	want := "-y chrome-devtools-mcp@latest --browserUrl http://127.0.0.1:9333"
	if strings.Join(args, " ") != want {
		t.Fatalf("mcpServerArgs = %v, want %q", args, want)
	}
}

func TestDefaultMCPStarterChecksNpx(t *testing.T) {
	looked := ""
	starter := newDefaultMCPStarter("", nil, func(name string) (string, error) {
		looked = name
		return "", errors.New("not found")
	}, nil)
	_, err := starter.Start(context.Background(), MCPSpec{Workspace: "ws", BrowserURL: "http://127.0.0.1:1"})
	if !errors.Is(err, ErrNodeMissing) {
		t.Fatalf("error = %v", err)
	}
	if looked != "npx" {
		t.Fatalf("looked up %q, want npx", looked)
	}
	if !strings.Contains(err.Error(), "Node.js") || !strings.Contains(err.Error(), "npx") {
		t.Fatalf("error is not actionable: %v", err)
	}
}

func TestManagerRejectsEmptyStateDir(t *testing.T) {
	if _, err := NewManager("", Options{}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("empty state dir error = %v", err)
	}
}

func itoa(n int) string { return strconv.Itoa(n) }
