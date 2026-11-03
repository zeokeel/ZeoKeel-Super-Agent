package store

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/zeokeel/super-agent/internal/domain"
)

// seedInspectable puts one row of each kind the status aggregates count into the
// store: workspaces, Tickets in three different derived stages, and waiting effects
// of two kinds (one due in the past, one held for the future).
func seedInspectable(t *testing.T, s *Store, now time.Time) {
	t.Helper()
	for _, id := range []domain.WorkspaceID{"ws_a", "ws_b"} {
		if err := s.PutWorkspace(domain.Workspace{ID: id, Name: string(id), Root: "/" + string(id), Kind: domain.KindRepo}); err != nil {
			t.Fatal(err)
		}
	}
	tickets := []domain.Ticket{
		{ID: "t_ended", Workspace: "ws_a", CreatedAt: now, Mandate: domain.Mandate{Workspace: "ws_a"},
			Ended: &domain.Ended{At: now, By: "executor", Outcome: "completed"}},
		{ID: "t_answer", Workspace: "ws_a", CreatedAt: now, Mandate: domain.Mandate{Workspace: "ws_a"},
			Attention: &domain.Attention{Question: "which one?", At: now}},
		{ID: "t_queued", Workspace: "ws_b", CreatedAt: now, Mandate: domain.Mandate{Workspace: "ws_b"},
			Assignments: []domain.Assignment{{ID: "a1", State: domain.AssignPending}}},
	}
	for _, tk := range tickets {
		if err := s.PutTicket(tk, now); err != nil {
			t.Fatal(err)
		}
	}
	// The effect queue deletes a row when it runs, so any row is a waiting effect.
	// One is due now, one is held by backoff: both must be counted.
	for i, e := range []struct {
		kind, key, due string
	}{
		{"reply", "r1", FormatTime(now.Add(-time.Minute))},
		{"reply", "r2", FormatTime(now.Add(time.Hour))},
		{"launch", "l1", FormatTime(now)},
	} {
		if _, err := s.Exec(`INSERT INTO effect(kind,key,seq,generation,attempts,due,error,payload,since)
			VALUES(?,?,?,0,0,?,'','{}',?)`,
			e.kind, e.key, i+1, e.due, FormatTime(now)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestInspectAggregateFacts(t *testing.T) {
	s, path := openTemp(t)
	now := time.Date(2026, 6, 1, 9, 0, 0, 0, time.UTC)
	seedInspectable(t, s, now)

	view, err := Inspect(path)
	if err != nil {
		t.Fatal(err)
	}
	if view.Workspaces != 2 {
		t.Fatalf("Workspaces = %d, want 2", view.Workspaces)
	}
	wantStages := map[string]int{
		string(domain.StageEnded):         1,
		string(domain.StagePersonAnswer):  1,
		string(domain.StageWorkingQueued): 1,
	}
	if len(view.TicketsByStage) != len(wantStages) {
		t.Fatalf("TicketsByStage = %v, want %v", view.TicketsByStage, wantStages)
	}
	for stage, want := range wantStages {
		if got := view.TicketsByStage[stage]; got != want {
			t.Fatalf("TicketsByStage[%q] = %d, want %d (all: %v)", stage, got, want, view.TicketsByStage)
		}
	}
	wantEffects := map[string]int{"reply": 2, "launch": 1}
	if len(view.EffectsByKind) != len(wantEffects) {
		t.Fatalf("EffectsByKind = %v, want %v", view.EffectsByKind, wantEffects)
	}
	for kind, want := range wantEffects {
		if got := view.EffectsByKind[kind]; got != want {
			t.Fatalf("EffectsByKind[%q] = %d, want %d (all: %v)", kind, got, want, view.EffectsByKind)
		}
	}
	// The pre-existing fields are unchanged by the new aggregates.
	if !view.SchemaVersionKnown || view.SchemaVersion != SchemaVersion {
		t.Fatalf("SchemaVersion = %d known=%v", view.SchemaVersion, view.SchemaVersionKnown)
	}
	if view.Health != nil {
		t.Fatalf("Health = %+v, want nil for a store that wrote none", view.Health)
	}
}

// Inspect must see the aggregates while another handle holds the writable Store:
// that is the whole point of the read-only status path.
func TestInspectAggregatesWhileWriterRuns(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	seedInspectable(t, s, time.Date(2026, 6, 1, 9, 0, 0, 0, time.UTC))

	view, err := Inspect(path)
	if err != nil {
		t.Fatalf("Inspect while the writer runs: %v", err)
	}
	if view.Workspaces != 2 || len(view.TicketsByStage) != 3 ||
		view.EffectsByKind["reply"] != 2 || view.EffectsByKind["launch"] != 1 {
		t.Fatalf("aggregates while locked: %+v", view)
	}
	if _, err := Open(path); err == nil {
		t.Fatal("a second writer opened the locked state directory")
	}
}

// A database that is not ours (or a v0 file) must yield zero values, not an error
// and not a guess.
func TestInspectForeignDatabaseYieldsZeroes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "foreign.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`CREATE TABLE something_else(x TEXT)`); err != nil {
		t.Fatal(err)
	}
	raw.Close()

	view, err := Inspect(path)
	if err != nil {
		t.Fatalf("Inspect of a foreign database: %v", err)
	}
	if view.SchemaVersionKnown || view.SchemaVersion != 0 {
		t.Fatalf("SchemaVersion = %d known=%v, want unknown", view.SchemaVersion, view.SchemaVersionKnown)
	}
	if view.Workspaces != 0 || len(view.TicketsByStage) != 0 || len(view.EffectsByKind) != 0 {
		t.Fatalf("foreign database produced facts: %+v", view)
	}
	if len(view.KV) != 0 || view.Health != nil {
		t.Fatalf("foreign database produced kv/health: %+v", view)
	}
}
