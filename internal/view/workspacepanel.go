package view

// 工作区设置与统计: the single panel behind 项目 › 概览 / 设置 / 统计. The runtime
// reports browsers, per-workspace settings, the skill library and stats through
// optional capabilities; these types are the plain data the panel draws.

import "time"

// BrowserInfo is one browser the runtime detected (default install paths).
type BrowserInfo struct {
	ID        string // "chrome" | "edge"
	Name      string
	Path      string
	Version   string
	Available bool
}

// SkillInfo is one skill in the global library. Skills are on by default; a
// workspace can disable individual ones.
type SkillInfo struct {
	ID               string
	Name             string
	Description      string
	EnabledByDefault bool
}

// ToolCategory is one group of tools the Agent can use, as the runtime reports
// it: the panel shows categories, never invented capabilities.
type ToolCategory struct {
	ID          string
	Label       string
	Description string
	Available   bool
}

// ActivityRow is one recent event of a workspace.
type ActivityRow struct {
	Text string
	At   time.Time
}

// WorkspaceSettings is the per-workspace configuration the panel edits.
type WorkspaceSettings struct {
	// BrowserEnabled runs a browser for this project (browser MCP / preview).
	BrowserEnabled bool
	// Browser is "chrome" or "edge".
	Browser string
	// Headless runs the browser without a window.
	Headless bool
	// Port is the CDP port; 0 means auto.
	Port int
	// ProfileDir is the per-workspace browser profile directory (reported, not
	// edited: the runtime owns it).
	ProfileDir string
	// DisabledSkills are the skills this workspace turned off (global default on).
	DisabledSkills []string
}

// SkillEnabled reports whether a skill is on in this workspace.
func (s WorkspaceSettings) SkillEnabled(id string, byDefault bool) bool {
	for _, d := range s.DisabledSkills {
		if d == id {
			return false
		}
	}
	return byDefault
}

// SetSkillEnabled turns one skill on or off in the draft, keeping the list
// duplicate-free.
func (s *WorkspaceSettings) SetSkillEnabled(id string, enabled, byDefault bool) {
	out := s.DisabledSkills[:0]
	for _, d := range s.DisabledSkills {
		if d != id {
			out = append(out, d)
		}
	}
	if !enabled {
		out = append(out, id)
	}
	s.DisabledSkills = out
	_ = byDefault
}

// WorkspaceStats is what 项目 › 统计 shows.
type WorkspaceStats struct {
	Conversations int
	Tasks         int
	Running       int
	Waiting       int
	Completed     int
	Failed        int
	// Tools are the tool categories the runtime reports for this workspace.
	Tools []ToolCategory
	// Recent is the newest activity, newest first.
	Recent    []ActivityRow
	UpdatedAt time.Time
}
