package runtime

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zeokeel/super-agent/internal/domain"
	"github.com/zeokeel/super-agent/internal/engine"
	"github.com/zeokeel/super-agent/internal/view"
)

// The storage card must report the real database size and session count, not a
// dash forever: "存储与保留" is there to tell a person whether the agent is
// recording.
func TestStorageCardSeesTheFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "zeokeel.db"), make([]byte, 4096), 0o644); err != nil {
		t.Fatal(err)
	}
	sessions := filepath.Join(dir, "work", "ticket-1", ".zeokeel", "sessions")
	if err := os.MkdirAll(sessions, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"s1.jsonl", "s2.jsonl"} {
		if err := os.WriteFile(filepath.Join(sessions, name), []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	rt := New(engine.New(engine.RuleAssessor{}), view.NewSnapshot())
	rt.SetStateDir(dir)
	st := rt.storageInfo(rt.clock())
	if st.Events != 2 {
		t.Fatalf("events = %d, want 2", st.Events)
	}
	if st.DB == "—" {
		t.Fatal("the database size was not read")
	}
}

// Without a state directory the card says "unknown", never zero: a fresh number
// would be a claim about a disk the runtime has not looked at.
func TestStorageWithoutStateDirIsUnknown(t *testing.T) {
	rt := New(engine.New(engine.RuleAssessor{}), view.NewSnapshot())
	st := rt.storageInfo(time.Now())
	if st.Events != -1 {
		t.Fatalf("events = %d, want -1 (unknown)", st.Events)
	}
	if st.DB != "—" {
		t.Fatalf("DB = %q", st.DB)
	}
}

// A deliverable must say which workspace and ticket produced it: six weeks later
// that is the only way to find it again.
func TestArtifactsProjectionCarriesContext(t *testing.T) {
	eng := engine.New(engine.RuleAssessor{})
	eng.Register(domain.Workspace{
		ID: "ws_web", Name: "web", Root: "/tmp/web", Kind: domain.KindRepo,
		Brief: domain.Brief{Purpose: "webapp", Keywords: []string{"webapp"}},
	}, engine.Policy{Version: "v1"})
	if _, err := eng.Dispatch(engine.DispatchInput{Text: "在 webapp 里做点事", Workspace: "ws_web"}); err != nil {
		t.Fatal(err)
	}
	out := evalNow(eng)
	if _, err := eng.Present(out.Ticket, domain.Artifact{
		Kind: domain.ArtifactFile, Title: "hello.txt", Path: "hello.txt",
	}); err != nil {
		t.Fatal(err)
	}
	rt := New(eng, view.NewSnapshot())
	vm := rt.artifactsVM(rt.clock())
	if vm.Total != 1 || len(vm.Rows) != 1 {
		t.Fatalf("rows = %+v", vm.Rows)
	}
	row := vm.Rows[0]
	if row.Workspace != "ws_web" || row.Ticket != "#1" {
		t.Fatalf("missing context: %+v", row)
	}
	if row.Kind != "file" {
		t.Fatalf("kind = %q", row.Kind)
	}
}
