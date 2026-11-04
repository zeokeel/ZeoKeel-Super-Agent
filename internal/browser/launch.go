package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	defaultStartTimeout = 15 * time.Second
	defaultStopTimeout  = 5 * time.Second
	probeInterval       = 100 * time.Millisecond
)

// launchArgs are the flags the per-workspace instance always gets. The port is
// loopback-only (Chrome/Edge default to 127.0.0.1) and the profile is isolated.
func launchArgs(port int, profileDir string, headless bool) []string {
	args := []string{
		fmt.Sprintf("--remote-debugging-port=%d", port),
		"--user-data-dir=" + profileDir,
		"--no-first-run",
		"--no-default-browser-check",
	}
	if headless {
		args = append(args, "--headless=new")
	}
	return args
}

// secretEnv matches environment variable names that must not reach the browser
// child (the panel's process may hold provider keys, IM credentials, tokens).
var secretEnv = regexp.MustCompile(`(?i)(TOKEN|SECRET|PASSWORD|PASSWD|API[_-]?KEY|CREDENTIAL|AUTH)`)

// scrubEnv drops secret-looking variables but keeps HOME, PATH, DISPLAY,
// WAYLAND_DISPLAY, XDG_* etc. so a GUI/headless browser still starts.
func scrubEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		key, _, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		if secretEnv.MatchString(key) {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// execLauncher is the production Launcher.
type execLauncher struct{}

func (execLauncher) Start(_ context.Context, spec LaunchSpec) (Process, error) {
	cmd := exec.Command(spec.Executable, spec.Args...)
	cmd.Env = spec.Env
	// Browser diagnostics are not needed by the product; discarding them avoids
	// filling the app log with Chrome's own verbose output.
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	p := &execProcess{cmd: cmd, done: make(chan error, 1)}
	go func() { p.done <- cmd.Wait() }()
	return p, nil
}

type execProcess struct {
	cmd  *exec.Cmd
	done chan error
}

func (p *execProcess) PID() int                   { return p.cmd.Process.Pid }
func (p *execProcess) Signal(sig os.Signal) error { return p.cmd.Process.Signal(sig) }
func (p *execProcess) Kill() error                { return p.cmd.Process.Kill() }
func (p *execProcess) Wait() error                { return <-p.done }

// httpProber polls <devtoolsURL>/json/version until it answers 200. Tests
// inject a fake prober so no port is opened.
type httpProber struct {
	client   *http.Client
	interval time.Duration
}

func newHTTPProber() *httpProber {
	return &httpProber{client: &http.Client{Timeout: time.Second}, interval: probeInterval}
}

func (p *httpProber) Wait(ctx context.Context, devtoolsURL string) error {
	url := strings.TrimRight(devtoolsURL, "/") + "/json/version"
	var lastErr error
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		resp, err := p.client.Do(req)
		if err == nil {
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
			lastErr = fmt.Errorf("devtools endpoint answered HTTP %d", resp.StatusCode)
		} else {
			lastErr = err
		}
		t := time.NewTimer(p.interval)
		select {
		case <-ctx.Done():
			t.Stop()
			if lastErr != nil {
				return fmt.Errorf("%w: %v", ErrStartTimeout, lastErr)
			}
			return fmt.Errorf("%w: %v", ErrStartTimeout, ctx.Err())
		case <-t.C:
		}
	}
}

// statePath is <stateDir>/workspaces/<wsID>/browser.json (0600).
func (m *Manager) statePath(wsID string) string {
	return filepath.Join(m.stateDir, "workspaces", wsID, "browser.json")
}

// diskStatus is the persisted fact set (PID/port/workspace) requested by the
// runtime for diagnostics; it is not the source of truth for a running manager.
type diskStatus struct {
	Workspace   string    `json:"workspace"`
	State       State     `json:"state"`
	Browser     string    `json:"browser"`
	BrowserPath string    `json:"browser_path"`
	PID         int       `json:"pid"`
	Port        int       `json:"port"`
	Headless    bool      `json:"headless"`
	ProfileDir  string    `json:"profile_dir"`
	DevToolsURL string    `json:"devtools_url"`
	MCPState    string    `json:"mcp_state,omitempty"`
	StartedAt   time.Time `json:"started_at,omitempty"`
}

func writeStatusFile(stateDir, wsID string, st Status) error {
	dir := filepath.Join(stateDir, "workspaces", wsID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(diskStatus{
		Workspace: st.Workspace, State: st.State, Browser: st.Browser,
		BrowserPath: st.BrowserPath, PID: st.PID, Port: st.Port,
		Headless: st.Headless, ProfileDir: st.ProfileDir,
		DevToolsURL: st.DevToolsURL, MCPState: st.MCPState, StartedAt: st.StartedAt,
	}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "browser.json"), append(raw, '\n'), 0o600)
}

// releasePortLocked forgets a port reservation (m.mu held).
func (m *Manager) releasePortLocked(port int) {
	if port != 0 {
		delete(m.ports, port)
	}
}

// waitDone waits for done up to timeout, returning false on timeout.
func waitDone(done <-chan error, timeout time.Duration) bool {
	t := time.NewTimer(timeout)
	defer t.Stop()
	select {
	case <-done:
		return true
	case <-t.C:
		return false
	}
}

var errNoProcess = errors.New("browser: no process")
