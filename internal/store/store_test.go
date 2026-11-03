package store

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/zeokeel/super-agent/internal/domain"
)

func tempStore(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "zeokeel.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, path
}

func ws(id domain.WorkspaceID, root string) domain.Workspace {
	return domain.Workspace{
		ID: id, Name: string(id), Root: root, Kind: domain.KindRepo,
		Brief: domain.Brief{Purpose: "测试用工作区", Keywords: []string{"acme"}},
	}
}

// A workspace survives a restart, and its id does not depend on its root: moving
// the directory must not orphan the history (design §4.5).
func TestWorkspaceSurvivesRestartAndIgnoresRoot(t *testing.T) {
	s, path := tempStore(t)
	if err := s.PutWorkspace(ws("ws_a", "/old/path")); err != nil {
		t.Fatal(err)
	}
	s.Close()

	again, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()

	got, err := again.Workspaces()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "ws_a" {
		t.Fatalf("unexpected workspaces %+v", got)
	}

	// Moving the directory updates the root but keeps the id.
	if err := again.PutWorkspace(ws("ws_a", "/new/path")); err != nil {
		t.Fatal(err)
	}
	got, _ = again.Workspaces()
	if len(got) != 1 || got[0].Root != "/new/path" {
		t.Fatalf("root not updated: %+v", got)
	}
}

// Two workspaces with the same source and the same source-side number are two
// separate records.
func TestTriggerIsScopedPerWorkspace(t *testing.T) {
	s, _ := tempStore(t)
	now := time.Now()
	mk := func(w domain.WorkspaceID) (domain.Trigger, domain.Disposition) {
		tr := domain.Trigger{
			ID:    domain.Key(domain.WorkspaceScope(w), "github", "12"),
			Scope: domain.WorkspaceScope(w), Source: "github",
			At: now, Human: true,
			Threads:          []domain.ThreadRef{{Source: "github", ID: "12"}},
			DefaultWorkspace: w,
		}
		return tr, domain.Disposition{TriggerID: tr.ID, Kind: domain.DispFiltered}
	}
	a, da := mk("ws_a")
	b, db := mk("ws_b")
	if a.ID == b.ID {
		t.Fatal("ids collided")
	}
	if err := s.PutTrigger(a, da); err != nil {
		t.Fatal(err)
	}
	if err := s.PutTrigger(b, db); err != nil {
		t.Fatal(err)
	}
	if got, ok, err := s.Trigger(a.ID); err != nil || !ok || got.Scope != domain.WorkspaceScope("ws_a") {
		t.Fatalf("trigger a: %+v ok=%v err=%v", got, ok, err)
	}
	if got, ok, _ := s.Trigger(b.ID); !ok || got.Scope != domain.WorkspaceScope("ws_b") {
		t.Fatalf("trigger b: %+v ok=%v", got, ok)
	}
}

// A rollback drops the command and its side effects; a commit runs them.
func TestAtomicAndAfterCommit(t *testing.T) {
	s, _ := tempStore(t)
	var ran int

	err := s.Atomic(func() error {
		s.AfterCommit(func() { ran++ })
		if err := s.PutWorkspace(ws("ws_x", "/x")); err != nil {
			return err
		}
		return errors.New("boom")
	})
	if err == nil {
		t.Fatal("expected the transaction to fail")
	}
	if ran != 0 {
		t.Fatal("AfterCommit ran on rollback")
	}
	if got, _ := s.Workspaces(); len(got) != 0 {
		t.Fatalf("rolled-back workspace persisted: %+v", got)
	}

	if err := s.Atomic(func() error {
		s.AfterCommit(func() { ran++ })
		return s.PutWorkspace(ws("ws_y", "/y"))
	}); err != nil {
		t.Fatal(err)
	}
	if ran != 1 {
		t.Fatalf("AfterCommit ran %d times, want 1", ran)
	}
	if got, _ := s.Workspaces(); len(got) != 1 || got[0].ID != "ws_y" {
		t.Fatalf("committed workspace missing: %+v", got)
	}
}

// Nested Atomic rolls back only the inner scope.
func TestNestedAtomic(t *testing.T) {
	s, _ := tempStore(t)
	err := s.Atomic(func() error {
		if err := s.PutWorkspace(ws("ws_outer", "/o")); err != nil {
			return err
		}
		inner := s.Atomic(func() error {
			if err := s.PutWorkspace(ws("ws_inner", "/i")); err != nil {
				return err
			}
			return errors.New("inner failed")
		})
		return inner // propagate: the outer must roll back too
	})
	if err == nil {
		t.Fatal("expected failure")
	}
	if got, _ := s.Workspaces(); len(got) != 0 {
		t.Fatalf("nothing should have committed: %+v", got)
	}

	// A nested success inside an outer success commits both.
	if err := s.Atomic(func() error {
		if err := s.PutWorkspace(ws("ws_a", "/a")); err != nil {
			return err
		}
		return s.Atomic(func() error { return s.PutWorkspace(ws("ws_b", "/b")) })
	}); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Workspaces(); len(got) != 2 {
		t.Fatalf("expected both workspaces, got %+v", got)
	}
}

// The stage column is a cache: it must equal what StageOf derives.
func TestTicketStageColumnMatchesDomain(t *testing.T) {
	s, _ := tempStore(t)
	now := time.Now()
	tk := domain.Ticket{
		ID: "t1", Workspace: "ws_a", CreatedAt: now,
		Mandate:     domain.Mandate{Workspace: "ws_a"},
		Assignments: []domain.Assignment{{ID: "a1", State: domain.AssignRunning}},
	}
	if err := s.PutTicket(tk, now); err != nil {
		t.Fatal(err)
	}
	var stage string
	if err := s.DB().QueryRow(`SELECT stage FROM ticket WHERE id='t1'`).Scan(&stage); err != nil {
		t.Fatal(err)
	}
	if stage != string(domain.StageWorking) {
		t.Fatalf("cached stage %q, want %q", stage, domain.StageWorking)
	}
	// The partial index (WHERE ended_at IS NULL) is the live-set read every pass
	// uses; it must see the open Ticket and nothing from another workspace.
	var open int
	if err := s.QueryRow(`SELECT count(*) FROM ticket WHERE ended_at IS NULL AND workspace_id='ws_a'`).Scan(&open); err != nil {
		t.Fatal(err)
	}
	if open != 1 {
		t.Fatalf("open tickets = %d, want 1", open)
	}
	var other int
	if err := s.QueryRow(`SELECT count(*) FROM ticket WHERE ended_at IS NULL AND workspace_id='ws_b'`).Scan(&other); err != nil {
		t.Fatal(err)
	}
	if other != 0 {
		t.Fatalf("another workspace saw %d tickets", other)
	}
}

// A newer schema is refused rather than misread.
func TestNewerSchemaIsRefused(t *testing.T) {
	s, path := tempStore(t)
	if err := s.Set("schema", "999"); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if _, err := Open(path); !errors.Is(err, ErrNewerSchema) {
		t.Fatalf("got %v, want ErrNewerSchema", err)
	}
}
