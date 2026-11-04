package store

// Per-workspace settings: configuration that belongs to one workspace but is not
// part of its identity, its engine policy, or the runtime's global settings. Today
// that is the browser-automation switch and the list of skills disabled in that
// workspace; the shape is a small closed model on purpose (no UI types, no maps of
// arbitrary keys), so a workspace-settings page cannot grow a second configuration
// system by accident.
//
// Storage: one JSON row per workspace in the kv table, under
// "workspace.<id>.settings". That is deliberately a kv row and not a table:
//   - no schema migration, so an existing install gains the feature by upgrading;
//   - the data is read whole and never filtered by SQL, which is exactly the case
//     the store reserves kv for;
//   - Settings() only reads the "setting:" prefix, so these rows can never leak
//     into the global settings snapshot (or be written back by a whole-snapshot
//     save). Inspect() hides the "workspace." namespace from its status KV for the
//     same reason.
//
// Writes are validated (browser name, port range, skill names) and normalized
// (browser lowercased, disabled skills deduplicated and sorted) so two runtimes
// saving the same intent produce the same bytes.

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/zeokeel/super-agent/internal/domain"
)

const (
	// workspaceSettingsPrefix / suffix make the kv key self-describing:
	// "workspace.<id>.settings". The id may itself contain dots; the suffix is
	// stripped once when reading, so the mapping stays unambiguous.
	workspaceSettingsPrefix = "workspace."
	workspaceSettingsSuffix = ".settings"

	// maxWorkspaceSettingsIDLen bounds the id part of the key.
	maxWorkspaceSettingsIDLen = 200
	// maxDisabledSkills bounds one workspace's disabled-skill list.
	maxDisabledSkills = 256
	// maxSkillNameLen matches the skills loader's name budget (skills.parse.go).
	maxSkillNameLen = 64

	// BrowserChrome and BrowserEdge are the browsers this build can drive. "" means
	// "not chosen yet": the browser feature cannot run without a choice, and the
	// settings page shows the two options.
	BrowserChrome = "chrome"
	BrowserEdge   = "edge"

	// minBrowserPort is the lowest port a caller may pin for the browser
	// automation; 0 means "let the runtime pick a free port". Ports below 1024 are
	// privileged and are refused rather than attempted and failed at launch.
	minBrowserPort = 1024
	maxBrowserPort = 65535
)

// browserNamePattern is the skills name pattern, kept local: the store must not
// depend on the skills loader, but a disabled-skill list that names something the
// loader would refuse is a configuration bug worth reporting here.
var browserSettingsSkillPattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// WorkspaceBrowserSettings is the browser-automation half of one workspace's
// settings. It is intentionally primitives-only so a future UI change cannot alter
// the persisted shape.
type WorkspaceBrowserSettings struct {
	// Enabled turns browser automation on for this workspace.
	Enabled bool `json:"enabled"`
	// Browser is "" (not chosen), "chrome" or "edge".
	Browser string `json:"browser"`
	// Headless runs the browser without a window.
	Headless bool `json:"headless"`
	// Port is 0 (pick a free port) or a non-privileged port in [1024, 65535].
	Port int `json:"port"`
}

// WorkspaceSkillsSettings is the skills half of one workspace's settings.
type WorkspaceSkillsSettings struct {
	// DisabledSkills are skill names this workspace refuses. Sorted and unique in
	// the stored form.
	DisabledSkills []string `json:"disabledSkills"`
}

// WorkspaceSettings is everything one workspace's own settings page persists.
// UpdatedAt is set by the store on save (a caller-supplied value is ignored), so
// "when was this changed" cannot be forged by a stale window.
type WorkspaceSettings struct {
	Browser   WorkspaceBrowserSettings `json:"browser"`
	Skills    WorkspaceSkillsSettings  `json:"skills"`
	UpdatedAt time.Time                `json:"updatedAt"`
}

// Validate checks and normalizes a WorkspaceSettings value. It is exported so the
// panel can validate before saving and show the same message the store would
// return; SaveWorkspaceSettings always runs it.
func (ws WorkspaceSettings) Validate() (WorkspaceSettings, error) {
	out := ws
	out.Browser.Browser = strings.ToLower(strings.TrimSpace(ws.Browser.Browser))
	switch out.Browser.Browser {
	case "", BrowserChrome, BrowserEdge:
	default:
		return WorkspaceSettings{}, fmt.Errorf("store: unknown browser %q (want %q, %q or empty)", ws.Browser.Browser, BrowserChrome, BrowserEdge)
	}
	if out.Browser.Port != 0 && (out.Browser.Port < minBrowserPort || out.Browser.Port > maxBrowserPort) {
		return WorkspaceSettings{}, fmt.Errorf("store: browser port %d is out of range (0 or %d-%d)", ws.Browser.Port, minBrowserPort, maxBrowserPort)
	}

	disabled := ws.Skills.DisabledSkills
	if len(disabled) > maxDisabledSkills {
		return WorkspaceSettings{}, fmt.Errorf("store: more than %d disabled skills", maxDisabledSkills)
	}
	seen := make(map[string]bool, len(disabled))
	out.Skills.DisabledSkills = make([]string, 0, len(disabled))
	for _, raw := range disabled {
		name := strings.TrimSpace(raw)
		if name == "" {
			return WorkspaceSettings{}, fmt.Errorf("store: empty skill name in the disabled list")
		}
		if len(name) > maxSkillNameLen {
			return WorkspaceSettings{}, fmt.Errorf("store: skill name %q is longer than %d bytes", name, maxSkillNameLen)
		}
		if !browserSettingsSkillPattern.MatchString(name) {
			return WorkspaceSettings{}, fmt.Errorf("store: skill name %q is not kebab-case", name)
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		out.Skills.DisabledSkills = append(out.Skills.DisabledSkills, name)
	}
	sort.Strings(out.Skills.DisabledSkills)
	return out, nil
}

// WorkspaceSettings returns one workspace's settings. A workspace that never saved
// any returns the zero value (browser disabled, no browser chosen, no disabled
// skills), not an error: "no settings yet" is a normal state, and the caller's
// defaults are then unambiguous.
func (s *Store) WorkspaceSettings(id domain.WorkspaceID) (WorkspaceSettings, error) {
	key, err := workspaceSettingsKey(id)
	if err != nil {
		return WorkspaceSettings{}, err
	}
	raw, err := s.kvGet(key)
	if errors.Is(err, sql.ErrNoRows) {
		return WorkspaceSettings{}, nil
	}
	if err != nil {
		return WorkspaceSettings{}, err
	}
	return decodeWorkspaceSettings(id, key, raw)
}

// SaveWorkspaceSettings validates and stores one workspace's settings. UpdatedAt is
// stamped by the store. The row is written in one statement; a failed validation
// writes nothing.
func (s *Store) SaveWorkspaceSettings(id domain.WorkspaceID, settings WorkspaceSettings) error {
	key, err := workspaceSettingsKey(id)
	if err != nil {
		return err
	}
	normalized, err := settings.Validate()
	if err != nil {
		return err
	}
	normalized.UpdatedAt = s.clock().UTC()
	payload, err := json.Marshal(normalized)
	if err != nil {
		return fmt.Errorf("store: encode workspace %s settings: %w", id, err)
	}
	if err := s.kvSet(key, string(payload)); err != nil {
		return fmt.Errorf("store: save workspace %s settings: %w", id, err)
	}
	return nil
}

// AllWorkspaceSettings returns every workspace that has saved settings, keyed by
// workspace id. Workspaces that never saved are simply absent (their zero value is
// the same as not storing one). A corrupt row is an error, not a silent skip: the
// caller is about to project configuration and guessing would hide a bad write.
//
// Rows whose workspace is no longer registered are still returned: the store does
// not own the workspace list, so callers intersect with it (and use
// DeleteWorkspaceSettings on the workspace-delete path).
func (s *Store) AllWorkspaceSettings() (map[domain.WorkspaceID]WorkspaceSettings, error) {
	rows, err := s.Query(`SELECT k, v FROM kv WHERE k LIKE ? ORDER BY k`, workspaceSettingsPrefix+"%"+workspaceSettingsSuffix)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[domain.WorkspaceID]WorkspaceSettings{}
	for rows.Next() {
		var k, raw string
		if err := rows.Scan(&k, &raw); err != nil {
			return nil, err
		}
		if !strings.HasPrefix(k, workspaceSettingsPrefix) || !strings.HasSuffix(k, workspaceSettingsSuffix) {
			continue
		}
		id := domain.WorkspaceID(strings.TrimSuffix(strings.TrimPrefix(k, workspaceSettingsPrefix), workspaceSettingsSuffix))
		if id == "" {
			continue
		}
		ws, err := decodeWorkspaceSettings(id, k, raw)
		if err != nil {
			return nil, err
		}
		out[id] = ws
	}
	return out, rows.Err()
}

// DeleteWorkspaceSettings removes one workspace's settings row. It is here for the
// workspace-delete path: leaving the row behind would resurrect old settings if an
// id were ever reused.
func (s *Store) DeleteWorkspaceSettings(id domain.WorkspaceID) error {
	key, err := workspaceSettingsKey(id)
	if err != nil {
		return err
	}
	_, err = s.Exec(`DELETE FROM kv WHERE k=?`, key)
	return err
}

// workspaceSettingsKey renders and validates the kv key for one workspace id.
func workspaceSettingsKey(id domain.WorkspaceID) (string, error) {
	raw := strings.TrimSpace(string(id))
	if raw == "" {
		return "", fmt.Errorf("store: workspace id is required")
	}
	if len(raw) > maxWorkspaceSettingsIDLen {
		return "", fmt.Errorf("store: workspace id is longer than %d bytes", maxWorkspaceSettingsIDLen)
	}
	for _, r := range raw {
		if unicode.IsControl(r) || r == '/' || r == '\\' {
			return "", fmt.Errorf("store: workspace id %q contains a control character or path separator", raw)
		}
	}
	return workspaceSettingsPrefix + raw + workspaceSettingsSuffix, nil
}

// decodeWorkspaceSettings parses one stored row, with the row's key in errors so a
// corrupt value is actionable.
func decodeWorkspaceSettings(id domain.WorkspaceID, key, raw string) (WorkspaceSettings, error) {
	var ws WorkspaceSettings
	if strings.TrimSpace(raw) == "" {
		return ws, nil
	}
	if err := json.Unmarshal([]byte(raw), &ws); err != nil {
		return WorkspaceSettings{}, fmt.Errorf("store: workspace %s settings (%s) are not readable JSON: %w", id, key, err)
	}
	if ws.Skills.DisabledSkills == nil {
		ws.Skills.DisabledSkills = []string{}
	}
	return ws, nil
}

// WorkspaceCounts is the cheap, SQL-derived projection of one workspace: the panel
// uses it for the workspace row (how much work is there, when did it last move)
// without loading every Ticket.
//
// One "conversation" in the panel is one Ticket, so Tickets/TicketsByStage are the
// conversation counts; the runtime does not need to compute them itself.
type WorkspaceCounts struct {
	// TicketsByStage counts this workspace's Tickets per persisted stage ("ended",
	// "working/queued", "person/answer", ...). It reads the cached ticket.stage
	// column, which PutTicket wrote from domain.StageOf at save time — the honest
	// number from the file, not a re-derivation with another clock.
	TicketsByStage map[string]int
	// Tickets is every Ticket of the workspace (open and ended).
	Tickets int
	// OpenTickets is the subset without ended_at, the live set.
	OpenTickets int
	// Threads is the number of Thread rows bound to this workspace, and
	// LastActivity is the newest ticket.created_at or thread.last_at, whichever is
	// later.
	Threads      int
	LastActivity time.Time
}

// WorkspaceCounts returns the counts above for one workspace. Every query is an
// indexed read scoped by workspace_id; a workspace with no work returns zeros.
func (s *Store) WorkspaceCounts(id domain.WorkspaceID) (WorkspaceCounts, error) {
	raw := strings.TrimSpace(string(id))
	if raw == "" {
		return WorkspaceCounts{}, fmt.Errorf("store: workspace id is required")
	}
	out := WorkspaceCounts{TicketsByStage: map[string]int{}}

	rows, err := s.Query(`SELECT stage, COUNT(*) FROM ticket WHERE workspace_id=? GROUP BY stage`, raw)
	if err != nil {
		return WorkspaceCounts{}, err
	}
	for rows.Next() {
		var (
			stage string
			n     int
		)
		if err := rows.Scan(&stage, &n); err != nil {
			rows.Close()
			return WorkspaceCounts{}, err
		}
		out.TicketsByStage[stage] = n
		out.Tickets += n
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return WorkspaceCounts{}, err
	}
	rows.Close()

	if err := s.QueryRow(`SELECT COUNT(*) FROM ticket WHERE workspace_id=? AND ended_at IS NULL`, raw).
		Scan(&out.OpenTickets); err != nil {
		return WorkspaceCounts{}, err
	}

	var threadLast string
	if err := s.QueryRow(`SELECT COUNT(*), IFNULL(MAX(last_at),'') FROM thread WHERE workspace_id=?`, raw).
		Scan(&out.Threads, &threadLast); err != nil {
		return WorkspaceCounts{}, err
	}
	if t, err := ParseTime(threadLast); err == nil && t.After(out.LastActivity) {
		out.LastActivity = t
	}

	var ticketLast string
	if err := s.QueryRow(`SELECT IFNULL(MAX(created_at),'') FROM ticket WHERE workspace_id=?`, raw).
		Scan(&ticketLast); err != nil {
		return WorkspaceCounts{}, err
	}
	if t, err := ParseTime(ticketLast); err == nil && t.After(out.LastActivity) {
		out.LastActivity = t
	}
	return out, nil
}
