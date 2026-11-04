package runtime

// Workspace settings + browser/MCP integration (DSH round C/D/E/F): each
// workspace can enable a browser (chrome|edge, headless, unique CDP port) and
// disable skills; per-ticket tools/skills are scoped to that workspace.
//
// The browser launch/detection backend is owned by internal/browser (conf-arch).
// Runtime talks to it through BrowserBackend so this file compiles before the
// package lands; cmd wires the adapter once it does. Until then
// DetectedBrowsers/StartBrowser return a precise "未接线" error and Tickets run
// without browser tools.

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	"github.com/zeokeel/super-agent/internal/domain"
	"github.com/zeokeel/super-agent/internal/skills"
	"github.com/zeokeel/super-agent/internal/store"
	"github.com/zeokeel/super-agent/internal/view"
)

// BrowserInfo is one detected browser.
type BrowserInfo struct {
	ID   string
	Name string
	Path string
}

// BrowserBackend is the internal/browser adapter contract (conf-arch package).
type BrowserBackend interface {
	Detect() []BrowserInfo
	Start(ctx context.Context, workspaceID, browser string, headless bool, port int) (int, error)
	Stop(ctx context.Context, workspaceID string) error
	Port(workspaceID string) (int, bool)
	Status(workspaceID string) (running bool, port int, detail string)
	// Tools returns the workspace's browser MCP tools (nil when MCP is down).
	Tools(workspaceID string) ([]tool.BaseTool, error)
	// ProfileDir reports the per-workspace profile (reported, not edited).
	ProfileDir(workspaceID string) string
	Close() error
}

// SetBrowserBackend installs the adapter (cmd calls this when wired).
func (r *Runtime) SetBrowserBackend(b BrowserBackend) {
	r.mu.Lock()
	r.browserBackend = b
	r.mu.Unlock()
}

// DetectedBrowsers lists browsers the backend can find (empty when unwired).
// It returns the UI's view type so the panel needs no runtime import.
func (r *Runtime) DetectedBrowsers() []view.BrowserInfo {
	r.mu.Lock()
	b := r.browserBackend
	r.mu.Unlock()
	if b == nil {
		return nil
	}
	found := b.Detect()
	out := make([]view.BrowserInfo, 0, len(found))
	for _, info := range found {
		out = append(out, view.BrowserInfo{ID: info.ID, Name: info.Name, Path: info.Path, Available: info.Path != ""})
	}
	return out
}

// WorkspaceSettings is the UI projection (view type, bool when the workspace exists).
func (r *Runtime) WorkspaceSettings(workspaceID string) (view.WorkspaceSettings, bool) {
	cfg, err := r.workspaceSettingsConfig(workspaceID)
	if err != nil {
		return view.WorkspaceSettings{}, false
	}
	profile := ""
	if b := r.browser(); b != nil {
		profile = b.ProfileDir(workspaceID)
	}
	return view.WorkspaceSettings{
		BrowserEnabled: cfg.BrowserEnabled, Browser: cfg.Browser, Headless: cfg.Headless,
		Port: cfg.Port, ProfileDir: profile, DisabledSkills: cfg.DisabledSkills,
	}, true
}

// SaveWorkspaceSettings applies the UI's view type through the validated path.
func (r *Runtime) SaveWorkspaceSettings(workspaceID string, s view.WorkspaceSettings) error {
	return r.saveWorkspaceSettingsConfig(workspaceID, WorkspaceSettings{
		BrowserEnabled: s.BrowserEnabled, Browser: s.Browser, Headless: s.Headless,
		Port: s.Port, DisabledSkills: s.DisabledSkills,
	})
}

func (r *Runtime) browser() BrowserBackend {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.browserBackend
}

// Skills lists the global library for the panel (enabled by default; a workspace
// can disable names).
func (r *Runtime) Skills() []view.SkillInfo {
	r.mu.Lock()
	set := r.skills
	r.mu.Unlock()
	if set == nil {
		return nil
	}
	var out []view.SkillInfo
	for _, sk := range set.All() {
		out = append(out, view.SkillInfo{ID: sk.Name, Name: sk.Name, Description: sk.Description, EnabledByDefault: true})
	}
	return out
}

// WorkspaceStats is the UI projection of the workspace counts.
func (r *Runtime) WorkspaceStats(workspaceID string) (view.WorkspaceStats, bool) {
	if !r.hasWorkspace(domain.WorkspaceID(strings.TrimSpace(workspaceID))) {
		return view.WorkspaceStats{}, false
	}
	st := r.workspaceStats(workspaceID)
	out := view.WorkspaceStats{
		Conversations: st.Conversations, Tasks: st.Conversations,
		Running: st.Running, Waiting: st.Waiting, Completed: st.Ended,
		UpdatedAt: r.clock(),
	}
	// Failed = Tickets whose latest report is failed.
	for _, t := range r.ticketSnapshots() {
		if string(t.Workspace) != workspaceID {
			continue
		}
		if rep := t.LastReport(); rep != nil && rep.State == domain.ReportFailed {
			out.Failed++
		}
	}
	out.Tools = []view.ToolCategory{
		{ID: "browser", Label: "浏览器", Description: "browser MCP", Available: r.browser() != nil},
		{ID: "plan", Label: "计划", Description: "plantask", Available: r.settingBool("execution.plan_tasks", true)},
		{ID: "web", Label: "网络", Description: "web_fetch/web_search", Available: r.settingBool("execution.web_tools", true)},
	}
	return out, true
}

// WorkspaceSettings is the per-workspace configuration the panel edits.
type WorkspaceSettings struct {
	BrowserEnabled bool     `json:"browserEnabled"`
	Browser        string   `json:"browser"` // "chrome" | "edge"
	Headless       bool     `json:"headless"`
	Port           int      `json:"port"`
	DisabledSkills []string `json:"disabledSkills"`
}

const defaultWorkspaceSettingsKey = "workspace.%s.settings"

// WorkspaceSettings loads one workspace's settings (defaults when never saved).
// With a store it reads the dedicated API (kv workspace.<id>.settings); memory
// mode keeps the same JSON in the settings map so tests and dev builds behave.
func (r *Runtime) workspaceSettingsConfig(id string) (WorkspaceSettings, error) {
	wsID := strings.TrimSpace(id)
	if wsID == "" || !r.hasWorkspace(domain.WorkspaceID(wsID)) {
		return WorkspaceSettings{}, fmt.Errorf("工作区 %q 不存在", id)
	}
	out := WorkspaceSettings{Browser: "chrome"}
	key := fmt.Sprintf(defaultWorkspaceSettingsKey, wsID)
	r.mu.Lock()
	st := r.settingsStore
	r.mu.Unlock()
	if st != nil {
		stored, err := st.WorkspaceSettings(domain.WorkspaceID(wsID))
		if err != nil {
			return out, err
		}
		out = WorkspaceSettings{
			BrowserEnabled: stored.Browser.Enabled,
			Browser:        stored.Browser.Browser,
			Headless:       stored.Browser.Headless,
			Port:           stored.Browser.Port,
			DisabledSkills: stored.Skills.DisabledSkills,
		}
		if out.Browser == "" {
			out.Browser = "chrome"
		}
		return out, nil
	}
	if st == nil {
		r.mu.Lock()
		raw := r.settings[key]
		r.mu.Unlock()
		if raw == "" {
			return out, nil
		}
		if err := json.Unmarshal([]byte(raw), &out); err != nil {
			return out, nil
		}
		return out, nil
	}
	raw, ok, err := st.Get(key)
	if err != nil {
		return out, err
	}
	if !ok || raw == "" {
		return out, nil
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return out, fmt.Errorf("工作区设置损坏：%w", err)
	}
	if out.Browser == "" {
		out.Browser = "chrome"
	}
	return out, nil
}

// SaveWorkspaceSettings validates and persists one workspace's settings, then
// restarts/stops its browser to match.
func (r *Runtime) saveWorkspaceSettingsConfig(id string, cfg WorkspaceSettings) error {
	wsID := strings.TrimSpace(id)
	if wsID == "" || !r.hasWorkspace(domain.WorkspaceID(wsID)) {
		return fmt.Errorf("工作区 %q 不存在", id)
	}
	cfg.Browser = strings.ToLower(strings.TrimSpace(cfg.Browser))
	if cfg.Browser == "" {
		cfg.Browser = "chrome"
	}
	if cfg.Browser != "chrome" && cfg.Browser != "edge" {
		return fmt.Errorf("浏览器 %q 不支持（chrome / edge）", cfg.Browser)
	}
	if cfg.Port != 0 && (cfg.Port < 1024 || cfg.Port > 65535) {
		return fmt.Errorf("CDP 端口 %d 不在 1024..65535", cfg.Port)
	}
	cfg.DisabledSkills = dedupeStrings(cfg.DisabledSkills)
	r.mu.Lock()
	st := r.settingsStore
	r.mu.Unlock()
	if st != nil {
		set := store.WorkspaceSettings{
			Browser: store.WorkspaceBrowserSettings{
				Enabled: cfg.BrowserEnabled, Browser: cfg.Browser, Headless: cfg.Headless, Port: cfg.Port,
			},
			Skills: store.WorkspaceSkillsSettings{DisabledSkills: cfg.DisabledSkills},
		}
		normalized, err := set.Validate()
		if err != nil {
			return err
		}
		if err := st.SaveWorkspaceSettings(domain.WorkspaceID(wsID), normalized); err != nil {
			return fmt.Errorf("工作区设置保存失败：%w", err)
		}
	} else {
		raw, err := json.Marshal(cfg)
		if err != nil {
			return err
		}
		key := fmt.Sprintf(defaultWorkspaceSettingsKey, wsID)
		r.mu.Lock()
		if r.settings == nil {
			r.settings = map[string]string{}
		}
		r.settings[key] = string(raw)
		r.mu.Unlock()
	}
	// Apply the browser change now: disabled stops the process, enabled ensures it.
	if !cfg.BrowserEnabled {
		_ = r.StopBrowser(wsID)
	}
	r.Publish()
	return nil
}

func dedupeStrings(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// WorkspaceStats are the counts the workspace panel shows.
type WorkspaceStats struct {
	Conversations int
	Running       int
	Waiting       int
	Ended         int
}

// WorkspaceStats counts one workspace's Tickets. With a store it uses the
// store's cached stage counts; memory mode derives them from the engine.
func (r *Runtime) workspaceStats(id string) WorkspaceStats {
	wsID := domain.WorkspaceID(strings.TrimSpace(id))
	r.mu.Lock()
	st := r.settingsStore
	r.mu.Unlock()
	if st != nil {
		if counts, err := st.WorkspaceCounts(wsID); err == nil {
			out := WorkspaceStats{Conversations: counts.Tickets, Ended: counts.TicketsByStage["ended"]}
			out.Running = counts.TicketsByStage["working"] + counts.TicketsByStage["working/queued"]
			for stage, n := range counts.TicketsByStage {
				if strings.HasPrefix(stage, "person/") {
					out.Waiting += n
				}
			}
			return out
		}
	}
	var out WorkspaceStats
	now := r.clock()
	for _, t := range r.ticketSnapshots() {
		if t.Workspace != wsID {
			continue
		}
		out.Conversations++
		if t.Ended != nil {
			out.Ended++
			continue
		}
		st := domain.StageOf(&t, now)
		switch {
		case st == domain.StageWorking || st == domain.StageWorkingQueued:
			out.Running++
		case isPersonStage(st):
			out.Waiting++
		}
	}
	return out
}

// --- Browser wrappers ---

// BrowserStatus reports what is true (UI's primitive signature).
func (r *Runtime) BrowserStatus(workspaceID string) (running bool, detail string) {
	b := r.browser()
	if b == nil {
		return false, "浏览器后端未接线（internal/browser）"
	}
	running, port, detail := b.Status(workspaceID)
	if running && port > 0 {
		detail = fmt.Sprintf("%s · CDP %d", detail, port)
	}
	return running, detail
}

// BrowserMCPCommand is the optional MCP command override (settings), used by cmd
// when constructing the browser Manager Options.
func (r *Runtime) BrowserMCPCommand() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return strings.TrimSpace(r.settings["browser.mcp_cmd"])
}

// BrowserPort is the internal/test helper (UI uses BrowserStatus).
func (r *Runtime) BrowserPort(workspaceID string) (int, bool) {
	if b := r.browser(); b != nil {
		return b.Port(workspaceID)
	}
	return 0, false
}

// StartBrowser launches the workspace's browser (per-workspace profile + CDP port).
func (r *Runtime) StartBrowser(workspaceID string) error {
	ctx := context.Background()
	cfg, err := r.workspaceSettingsConfig(workspaceID)
	if err != nil {
		return err
	}
	if !cfg.BrowserEnabled {
		return fmt.Errorf("工作区 %q 未启用浏览器", workspaceID)
	}
	r.mu.Lock()
	b := r.browserBackend
	r.mu.Unlock()
	if b == nil {
		return fmt.Errorf("浏览器后端未接线（internal/browser）；请等待浏览器集成")
	}
	port, err := b.Start(ctx, workspaceID, cfg.Browser, cfg.Headless, cfg.Port)
	if err != nil {
		r.SetComponentHealth("browser:"+workspaceID, "error", err.Error())
		return err
	}
	r.SetComponentHealth("browser:"+workspaceID, "ready",
		fmt.Sprintf("%s · CDP %d", cfg.Browser, port))
	r.Publish()
	return nil
}

// StopBrowser stops the workspace browser and closes its browser MCP tools.
func (r *Runtime) StopBrowser(workspaceID string) error {
	r.closeBrowserMCP(workspaceID)
	r.mu.Lock()
	b := r.browserBackend
	r.mu.Unlock()
	if b == nil {
		return nil
	}
	if err := b.Stop(context.Background(), workspaceID); err != nil {
		return err
	}
	r.SetComponentHealth("browser:"+workspaceID, "ready", "已停止")
	r.Publish()
	return nil
}

// CloseBrowserBackend stops every browser and closes the backend (cmd shutdown).
func (r *Runtime) CloseBrowserBackend() {
	r.closeAllBrowserMCP()
	r.mu.Lock()
	b := r.browserBackend
	r.mu.Unlock()
	if b != nil {
		_ = b.Close()
	}
}

// --- Browser MCP tools (per workspace) ---

type browserMCP struct {
	tools []tool.BaseTool
}

// browserToolsFor ensures the workspace browser is running and returns its
// browser MCP tools (mcp__browser__*). Nil when browser is disabled/unavailable.
// The backend (internal/browser) owns launch + MCP lifecycle; runtime only caches
// the handles it was given.
func (r *Runtime) browserToolsFor(ctx context.Context, wsID domain.WorkspaceID) []tool.BaseTool {
	cfg, err := r.workspaceSettingsConfig(string(wsID))
	if err != nil || !cfg.BrowserEnabled {
		return nil
	}
	key := string(wsID)
	b := r.browser()
	if b == nil {
		return nil
	}
	if b == nil {
		return nil
	}
	if running, _, _ := b.Status(key); !running {
		if err := r.StartBrowser(key); err != nil {
			return nil
		}
	}
	// Fetch every run: the tool set changes with MCP server health (conf-arch);
	// on error fall back to the last good set so a transient MCP hiccup does not
	// silently strip tools mid-ticket.
	tools, err := b.Tools(key)
	r.mu.Lock()
	prev := r.browserMCP[key]
	r.mu.Unlock()
	if err != nil {
		r.SetComponentHealth("browser:"+key, "error", "browser MCP 连接失败："+err.Error())
		if prev != nil && len(prev.tools) > 0 {
			return prev.tools
		}
		return nil
	}
	entry := &browserMCP{tools: tools}
	r.mu.Lock()
	if r.browserMCP == nil {
		r.browserMCP = map[string]*browserMCP{}
	}
	r.browserMCP[key] = entry
	r.mu.Unlock()
	r.SetComponentHealth("browser:"+key, "ready", fmt.Sprintf("browser MCP %d 个工具", len(tools)))
	return tools
}

func (r *Runtime) closeBrowserMCP(wsID string) {
	r.mu.Lock()
	entry := r.browserMCP[wsID]
	delete(r.browserMCP, wsID)
	r.mu.Unlock()
	_ = entry // the backend owns the MCP lifecycle
}

func (r *Runtime) closeAllBrowserMCP() {
	r.mu.Lock()
	entries := r.browserMCP
	r.browserMCP = map[string]*browserMCP{}
	r.mu.Unlock()
	_ = entries // the backend owns the MCP lifecycle
}

// --- Skills per workspace ---

// disabledSkills returns the workspace's disabled skill names.
func (r *Runtime) disabledSkills(wsID domain.WorkspaceID) map[string]bool {
	cfg, err := r.workspaceSettingsConfig(string(wsID))
	if err != nil {
		return nil
	}
	out := map[string]bool{}
	for _, name := range cfg.DisabledSkills {
		out[name] = true
	}
	return out
}

// skillPromptForTicket renders only the workspace-enabled skills for a Ticket.
func (r *Runtime) skillPromptForTicket(t domain.Ticket, limit int) string {
	cfg := r.workerConfig()
	if cfg == nil || cfg.Skills == nil {
		return ""
	}
	matches := cfg.Skills.Match(t.Title, limit+4)
	disabled := r.disabledSkills(t.Workspace)
	kept := make([]skills.Match, 0, limit)
	for _, m := range matches {
		if m.Skill == nil || disabled[m.Skill.Name] {
			continue
		}
		kept = append(kept, m)
		if len(kept) == limit {
			break
		}
	}
	return cfg.Skills.Render(kept)
}

// skillAllowed reports whether a Worker may load a skill by name.
func (r *Runtime) skillAllowed(wsID domain.WorkspaceID, name string) bool {
	return !r.disabledSkills(wsID)[name]
}

// browserPortFor is used by the health projection/tests.
func (r *Runtime) browserPortFor(wsID string) (int, bool) {
	r.mu.Lock()
	b := r.browserBackend
	r.mu.Unlock()
	if b == nil {
		return 0, false
	}
	return b.Port(wsID)
}
