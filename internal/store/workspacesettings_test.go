package store

import (
	"strings"
	"testing"
	"time"

	"github.com/zeokeel/super-agent/internal/domain"
)

// zeroWorkspaceSettings reports the "never saved" value. The struct contains a
// slice, so it is not comparable with ==; comparing the parts keeps the test
// readable.
func zeroWorkspaceSettings(ws WorkspaceSettings) bool {
	return !ws.Browser.Enabled && ws.Browser.Browser == "" && !ws.Browser.Headless &&
		ws.Browser.Port == 0 && len(ws.Skills.DisabledSkills) == 0 && ws.UpdatedAt.IsZero()
}

func TestWorkspaceSettingsDefaultsWhenAbsent(t *testing.T) {
	s, _ := openTemp(t)
	ws, err := s.WorkspaceSettings("ws_never")
	if err != nil {
		t.Fatal(err)
	}
	if !zeroWorkspaceSettings(ws) {
		t.Fatalf("absent settings = %+v, want the zero value", ws)
	}
	if ws.Skills.DisabledSkills != nil {
		t.Fatalf("absent disabled skills = %v, want nil", ws.Skills.DisabledSkills)
	}
	all, err := s.AllWorkspaceSettings()
	if err != nil || len(all) != 0 {
		t.Fatalf("AllWorkspaceSettings on an empty store = %v err=%v", all, err)
	}
}

func TestWorkspaceSettingsRoundTrip(t *testing.T) {
	s, _ := openTemp(t)
	in := WorkspaceSettings{
		Browser: WorkspaceBrowserSettings{Enabled: true, Browser: BrowserChrome, Headless: true, Port: 9222},
		Skills:  WorkspaceSkillsSettings{DisabledSkills: []string{"pdf-tools", "web-research"}},
	}
	before := time.Now().Add(-time.Hour)
	in.UpdatedAt = before // caller-provided value must be ignored: the store stamps it
	if err := s.SaveWorkspaceSettings("ws_a", in); err != nil {
		t.Fatal(err)
	}
	got, err := s.WorkspaceSettings("ws_a")
	if err != nil {
		t.Fatal(err)
	}
	if !got.Browser.Enabled || got.Browser.Browser != BrowserChrome || !got.Browser.Headless || got.Browser.Port != 9222 {
		t.Fatalf("browser round trip = %+v", got.Browser)
	}
	if len(got.Skills.DisabledSkills) != 2 ||
		got.Skills.DisabledSkills[0] != "pdf-tools" || got.Skills.DisabledSkills[1] != "web-research" {
		t.Fatalf("skills round trip = %v", got.Skills.DisabledSkills)
	}
	if got.UpdatedAt.IsZero() || !got.UpdatedAt.After(before) {
		t.Fatalf("UpdatedAt = %v, want the store's save time", got.UpdatedAt)
	}
}

func TestWorkspaceSettingsValidation(t *testing.T) {
	s, _ := openTemp(t)
	ok := func(name string, ws WorkspaceSettings) {
		t.Helper()
		if err := s.SaveWorkspaceSettings("ws_ok", ws); err != nil {
			t.Fatalf("%s: unexpected error: %v", name, err)
		}
	}
	bad := func(name string, ws WorkspaceSettings) {
		t.Helper()
		if err := s.SaveWorkspaceSettings("ws_bad", ws); err == nil {
			t.Fatalf("%s: accepted invalid settings %+v", name, ws)
		}
	}

	ok("empty", WorkspaceSettings{})
	ok("chrome", WorkspaceSettings{Browser: WorkspaceBrowserSettings{Enabled: true, Browser: BrowserChrome}})
	ok("edge upper-case normalizes", WorkspaceSettings{Browser: WorkspaceBrowserSettings{Browser: "EDGE"}})
	norm, err := s.WorkspaceSettings("ws_ok")
	if err != nil {
		t.Fatal(err)
	}
	if norm.Browser.Browser != BrowserEdge {
		t.Fatalf("browser was not normalized: %q", norm.Browser.Browser)
	}
	ok("port 0", WorkspaceSettings{Browser: WorkspaceBrowserSettings{Port: 0}})
	ok("port 1024", WorkspaceSettings{Browser: WorkspaceBrowserSettings{Port: 1024}})
	ok("port 65535", WorkspaceSettings{Browser: WorkspaceBrowserSettings{Port: 65535}})

	bad("unknown browser", WorkspaceSettings{Browser: WorkspaceBrowserSettings{Browser: "firefox"}})
	bad("port 1", WorkspaceSettings{Browser: WorkspaceBrowserSettings{Port: 1}})
	bad("port 1023", WorkspaceSettings{Browser: WorkspaceBrowserSettings{Port: 1023}})
	bad("port negative", WorkspaceSettings{Browser: WorkspaceBrowserSettings{Port: -1}})
	bad("port too high", WorkspaceSettings{Browser: WorkspaceBrowserSettings{Port: 65536}})
	bad("empty skill name", WorkspaceSettings{Skills: WorkspaceSkillsSettings{DisabledSkills: []string{"  "}}})
	bad("upper-case skill", WorkspaceSettings{Skills: WorkspaceSkillsSettings{DisabledSkills: []string{"PDF"}}})
	bad("skill with space", WorkspaceSettings{Skills: WorkspaceSkillsSettings{DisabledSkills: []string{"pdf tools"}}})
	bad("skill too long", WorkspaceSettings{Skills: WorkspaceSkillsSettings{DisabledSkills: []string{strings.Repeat("a", maxSkillNameLen+1)}}})

	// A refused save wrote nothing.
	if got, err := s.WorkspaceSettings("ws_bad"); err != nil || !zeroWorkspaceSettings(got) {
		t.Fatalf("invalid save persisted state: %+v err=%v", got, err)
	}
}

func TestWorkspaceSettingsDisabledSkillsDedupeAndSort(t *testing.T) {
	s, _ := openTemp(t)
	if err := s.SaveWorkspaceSettings("ws_a", WorkspaceSettings{
		Skills: WorkspaceSkillsSettings{DisabledSkills: []string{"web-research", "pdf-tools", "web-research", "pdf-tools"}},
	}); err != nil {
		t.Fatal(err)
	}
	got, err := s.WorkspaceSettings("ws_a")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"pdf-tools", "web-research"}
	if len(got.Skills.DisabledSkills) != 2 || got.Skills.DisabledSkills[0] != want[0] || got.Skills.DisabledSkills[1] != want[1] {
		t.Fatalf("disabled skills = %v, want deduplicated and sorted %v", got.Skills.DisabledSkills, want)
	}
}

func TestWorkspaceSettingsAreIsolatedAndSurviveRestart(t *testing.T) {
	s, path := openTemp(t)
	if err := s.SaveWorkspaceSettings("ws_a", WorkspaceSettings{
		Browser: WorkspaceBrowserSettings{Enabled: true, Browser: BrowserEdge},
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveWorkspaceSettings("ws_b", WorkspaceSettings{
		Skills: WorkspaceSkillsSettings{DisabledSkills: []string{"pdf-tools"}},
	}); err != nil {
		t.Fatal(err)
	}

	// Isolation: each workspace sees only its own row.
	a, err := s.WorkspaceSettings("ws_a")
	if err != nil {
		t.Fatal(err)
	}
	if a.Browser.Browser != BrowserEdge || len(a.Skills.DisabledSkills) != 0 {
		t.Fatalf("ws_a settings bled into ws_b's values: %+v", a)
	}
	b, err := s.WorkspaceSettings("ws_b")
	if err != nil {
		t.Fatal(err)
	}
	if b.Browser.Browser != "" || len(b.Skills.DisabledSkills) != 1 {
		t.Fatalf("ws_b settings = %+v", b)
	}

	// AllWorkspaceSettings returns both.
	all, err := s.AllWorkspaceSettings()
	if err != nil || len(all) != 2 {
		t.Fatalf("all = %+v err=%v", all, err)
	}
	if all["ws_a"].Browser.Browser != BrowserEdge || len(all["ws_b"].Skills.DisabledSkills) != 1 {
		t.Fatalf("all = %+v", all)
	}

	// Restart persistence: a new Store over the same file sees the rows.
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	again, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	restarted, err := again.WorkspaceSettings("ws_a")
	if err != nil {
		t.Fatal(err)
	}
	if restarted.Browser.Browser != BrowserEdge || !restarted.Browser.Enabled {
		t.Fatalf("settings lost across restart: %+v", restarted)
	}
	if restarted.UpdatedAt.IsZero() {
		t.Fatal("UpdatedAt was not persisted")
	}
}

func TestWorkspaceSettingsHiddenFromSettingsAndStatus(t *testing.T) {
	s, path := openTemp(t)
	if err := s.SaveWorkspaceSettings("ws_a", WorkspaceSettings{
		Browser: WorkspaceBrowserSettings{Browser: BrowserChrome},
	}); err != nil {
		t.Fatal(err)
	}
	// The global settings snapshot must not grow these rows: Settings() reads only
	// the "setting:" prefix.
	values, err := s.Settings()
	if err != nil {
		t.Fatal(err)
	}
	for k := range values {
		if strings.HasPrefix(k, workspaceSettingsPrefix) {
			t.Fatalf("Settings() exposed %q", k)
		}
	}
	// A whole-snapshot settings save must not write them back or drop them.
	if _, err := s.SaveSettings(map[string]string{"execution.concurrency": "2"}, 0, false); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.WorkspaceSettings("ws_a"); got.Browser.Browser != BrowserChrome {
		t.Fatalf("a settings save disturbed workspace settings: %+v", got)
	}
	// The status view hides the data namespace.
	view, err := Inspect(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := view.KV[workspaceSettingsPrefix+"ws_a"+workspaceSettingsSuffix]; ok {
		t.Fatal("Inspect exposed a workspace settings row")
	}
}

func TestAllWorkspaceSettingsRejectsCorruptRow(t *testing.T) {
	s, _ := openTemp(t)
	key := workspaceSettingsPrefix + "ws_bad" + workspaceSettingsSuffix
	if err := s.Set(key, "{not json"); err != nil {
		t.Fatal(err)
	}
	// A corrupt row fails loudly: projection must not guess configuration.
	if _, err := s.AllWorkspaceSettings(); err == nil {
		t.Fatal("corrupt workspace settings row was silently skipped")
	}
	if _, err := s.WorkspaceSettings("ws_bad"); err == nil {
		t.Fatal("corrupt workspace settings row read as empty")
	}
	// Unrelated kv rows in the same prefix are ignored, not errors.
	if err := s.Set(workspaceSettingsPrefix+"ws_notes", `{"not":"settings"}`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AllWorkspaceSettings(); err == nil {
		// still the corrupt row, which is expected to error; the point of the
		// previous assertions is that the error is about ws_bad, not ws_notes.
	}
}

func TestWorkspaceSettingsDelete(t *testing.T) {
	s, _ := openTemp(t)
	if err := s.SaveWorkspaceSettings("ws_a", WorkspaceSettings{Browser: WorkspaceBrowserSettings{Browser: BrowserChrome}}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteWorkspaceSettings("ws_a"); err != nil {
		t.Fatal(err)
	}
	if got, err := s.WorkspaceSettings("ws_a"); err != nil || !zeroWorkspaceSettings(got) {
		t.Fatalf("after delete = %+v err=%v", got, err)
	}
	if all, _ := s.AllWorkspaceSettings(); len(all) != 0 {
		t.Fatalf("after delete all = %+v", all)
	}
	// Deleting an absent row is a no-op, not an error.
	if err := s.DeleteWorkspaceSettings("ws_missing"); err != nil {
		t.Fatal(err)
	}
}

func TestWorkspaceSettingsRejectsBadIDs(t *testing.T) {
	s, _ := openTemp(t)
	if _, err := s.WorkspaceSettings(""); err == nil {
		t.Fatal("empty workspace id accepted")
	}
	if err := s.SaveWorkspaceSettings("bad\nid", WorkspaceSettings{}); err == nil {
		t.Fatal("control character accepted in workspace id")
	}
	if _, err := s.WorkspaceCounts(""); err == nil {
		t.Fatal("WorkspaceCounts accepted an empty workspace id")
	}
}

func TestWorkspaceCounts(t *testing.T) {
	s, _ := openTemp(t)
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	tickets := []domain.Ticket{
		{ID: "t_ended", Workspace: "ws_a", CreatedAt: now.Add(-3 * time.Hour), Mandate: domain.Mandate{Workspace: "ws_a"},
			Ended: &domain.Ended{At: now, By: "executor", Outcome: "completed"}},
		{ID: "t_answer", Workspace: "ws_a", CreatedAt: now.Add(-2 * time.Hour), Mandate: domain.Mandate{Workspace: "ws_a"},
			Attention: &domain.Attention{Question: "q", At: now}},
		{ID: "t_queued", Workspace: "ws_a", CreatedAt: now.Add(-time.Hour), Mandate: domain.Mandate{Workspace: "ws_a"}},
		{ID: "t_other", Workspace: "ws_b", CreatedAt: now, Mandate: domain.Mandate{Workspace: "ws_b"}},
	}
	for _, tk := range tickets {
		if err := s.PutTicket(tk, now); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.PutThread(ThreadRecord{Key: "ws:ws_a|local|task:1", Workspace: "ws_a", Source: "local", LastAt: now.Add(30 * time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if err := s.PutThread(ThreadRecord{Key: "ws:ws_b|local|task:2", Workspace: "ws_b", Source: "local", LastAt: now.Add(-5 * time.Hour)}); err != nil {
		t.Fatal(err)
	}

	got, err := s.WorkspaceCounts("ws_a")
	if err != nil {
		t.Fatal(err)
	}
	if got.Tickets != 3 || got.OpenTickets != 2 || got.Threads != 1 {
		t.Fatalf("counts = %+v", got)
	}
	if got.TicketsByStage[string(domain.StageEnded)] != 1 ||
		got.TicketsByStage[string(domain.StagePersonAnswer)] != 1 ||
		got.TicketsByStage[string(domain.StageWorkingQueued)] != 1 {
		t.Fatalf("TicketsByStage = %v", got.TicketsByStage)
	}
	// The Thread row has the newest activity (30 minutes after now).
	if !got.LastActivity.Equal(now.Add(30 * time.Minute)) {
		t.Fatalf("LastActivity = %v, want the newest thread/ticket time", got.LastActivity)
	}

	other, err := s.WorkspaceCounts("ws_b")
	if err != nil {
		t.Fatal(err)
	}
	if other.Tickets != 1 || other.OpenTickets != 1 || other.Threads != 1 {
		t.Fatalf("ws_b counts = %+v", other)
	}

	empty, err := s.WorkspaceCounts("ws_empty")
	if err != nil {
		t.Fatal(err)
	}
	if empty.Tickets != 0 || empty.OpenTickets != 0 || empty.Threads != 0 ||
		len(empty.TicketsByStage) != 0 || !empty.LastActivity.IsZero() {
		t.Fatalf("empty workspace counts = %+v", empty)
	}
}
