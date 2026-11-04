// Package browser manages a per-workspace Chrome/Edge instance and the
// chrome-devtools MCP server that drives it.
//
// Security model:
//   - every workspace gets its own user-data directory at
//     <stateDir>/workspaces/<wsID>/browser-profile; the workspace id is limited
//     to [A-Za-z0-9_-] so it can never escape that directory;
//   - Stop/Close only terminate processes this manager started; profile data is
//     never deleted (logins survive a restart);
//   - the browser child process is launched with secret-looking environment
//     variables (TOKEN/SECRET/PASSWORD/API_KEY/CREDENTIAL/AUTH) removed, while
//     HOME/DISPLAY/WAYLAND_DISPLAY/PATH etc. are kept so a real GUI or headless
//     browser can start; the MCP child process inherits the process environment
//     because npx needs PATH/HOME (documented limitation, see MCPStarterIntro);
//   - only documented default browser install paths are probed; there is no
//     PATH search and no use of another user's profile.
//
// Nothing in this package touches the UI; the runtime owns config and projects
// Status. Detection/launch/MCP transport are seams so tests never start a real
// browser or npx.
package browser

import (
	"context"
	"errors"
	"os"
	"time"

	"github.com/cloudwego/eino/components/tool"
)

// ID is a supported browser engine.
type ID string

const (
	Chrome ID = "chrome"
	Edge   ID = "edge"
)

// Browser is one detected installation (default install path only).
type Browser struct {
	ID   ID
	Name string
	Path string
}

// Config is the per-workspace configuration the runtime passes in.
type Config struct {
	// Enabled starts the browser + MCP for this workspace; false stops them.
	Enabled bool
	// Browser is "chrome", "edge" or "" (prefer Chrome, then Edge).
	Browser string
	// Headless runs the browser without a window (`--headless=new`).
	Headless bool
	// Port is the remote-debugging port: 0 picks a free port, otherwise it must
	// be in 1024-65535 and is never shared between two workspaces.
	Port int
}

// State is the browser lifecycle state.
type State string

const (
	StateStopped  State = "stopped"
	StateStarting State = "starting"
	StateRunning  State = "running"
	StateError    State = "error"
	StateDisabled State = "disabled"
)

// Status is the runtime-facing view of one workspace's browser + MCP.
type Status struct {
	Workspace   string
	State       State
	Detail      string
	Browser     string
	BrowserPath string
	PID         int
	Port        int
	Headless    bool
	ProfileDir  string
	// DevToolsURL is the HTTP endpoint the MCP server is pointed at
	// (http://127.0.0.1:<port>); empty unless a browser is running.
	DevToolsURL string
	// MCPState is "ready", "error", "disabled" or "" (not started).
	MCPState  string
	MCPDetail string
	StartedAt time.Time
}

// Errors are stable so the runtime can render the right action.
var (
	ErrInvalidWorkspace   = errors.New("browser: invalid workspace id")
	ErrInvalidConfig      = errors.New("browser: invalid config")
	ErrBrowserNotDetected = errors.New("browser: no supported browser found")
	ErrPortUnavailable    = errors.New("browser: debugging port unavailable")
	ErrStartTimeout       = errors.New("browser: browser did not become ready")
	ErrNotRunning         = errors.New("browser: workspace browser is not running")
	ErrNodeMissing        = errors.New("browser: npx/node not found")
	ErrMCPStart           = errors.New("browser: chrome-devtools MCP failed to start")
	ErrClosed             = errors.New("browser: manager is closed")
)

// Launcher starts one browser process. It is a seam so tests never exec a real
// browser.
type Launcher interface {
	Start(ctx context.Context, spec LaunchSpec) (Process, error)
}

// Process is the subset of os.Process the manager uses.
type Process interface {
	PID() int
	Signal(sig os.Signal) error
	Kill() error
	Wait() error
}

// Prober waits until the DevTools endpoint answers. Tests inject a fake so no
// real port is opened.
type Prober interface {
	Wait(ctx context.Context, devtoolsURL string) error
}

// LaunchSpec is one browser launch request; Args already contains every flag.
type LaunchSpec struct {
	Executable  string
	Args        []string
	Env         []string
	Workspace   string
	ProfileDir  string
	Port        int
	Headless    bool
	DevToolsURL string
}

// PrereqStatus is the Node.js/npx prerequisite view used by the panel to remind
// a customer to install Node.js. Checking is local only: LookPath + `<tool>
// --version` with a timeout; no network, no package install.
type PrereqStatus struct {
	NodeFound   bool
	NodeVersion string
	NodePath    string
	NPXFound    bool
	NPXVersion  string
	NPXPath     string
	CheckedAt   time.Time
	// InstallHints are actionable, ordered sentences (install/repair/PATH).
	InstallHints []string
	// InstallURL is the official Node.js download page (never fetched here).
	InstallURL string
}

// MCPSpec is one chrome-devtools MCP stdio server request.
type MCPSpec struct {
	Workspace  string
	BrowserURL string
	// Command/Args come from Manager.Options (default npx -y
	// chrome-devtools-mcp@latest); the starter appends --browserUrl.
	Command string
	Args    []string
}

// MCPHandle is a started MCP server: its discovered worker tools and a Close.
//
// MCPStarterIntro documents the known limitation: the MCP child process
// inherits this process's environment (npx needs PATH/HOME); unlike the browser
// child it is not secret-scrubbed until internal/mcp grows an env override.
type MCPHandle interface {
	Tools() []tool.InvokableTool
	Close() error
}

// MCPStarter starts one MCP server. Tests inject a fake; production uses
// internal/mcp with a stdio npx command.
type MCPStarter interface {
	Start(ctx context.Context, spec MCPSpec) (MCPHandle, error)
}

// mcpStarterIntro is referenced in docs so the limitation is greppable.
const mcpStarterIntro = "MCP child processes inherit the environment; see package doc."
