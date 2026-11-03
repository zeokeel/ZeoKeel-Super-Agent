package runtime

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/zeokeel/super-agent/internal/agent/mockllm"
	"github.com/zeokeel/super-agent/internal/domain"
	"github.com/zeokeel/super-agent/internal/engine"
	"github.com/zeokeel/super-agent/internal/view"
)

// "How much has it cost me?" has to be answerable. The counter is per process, so
// the panel labels it "本次运行" rather than pretending it is all-time.
func TestUsageAccumulatesAcrossRuns(t *testing.T) {
	srv, err := mockllm.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	srv.ToolName = "bash"
	srv.ToolArgs = `{"command":"echo hi"}`

	eng := engine.New(engine.RuleAssessor{})
	eng.Register(domain.Workspace{
		ID: "ws_web", Name: "web", Root: t.TempDir(), Kind: domain.KindRepo,
		Brief: domain.Brief{Purpose: "webapp", Keywords: []string{"webapp"}},
	}, engine.Policy{Version: "v1"})
	rt := New(eng, view.NewSnapshot())
	rt.EnableWorker(WorkerConfig{
		BaseURL: srv.URL, APIKey: "sk-mock", Model: "mock-tool",
		Root: filepath.Join(t.TempDir(), "work"),
	})
	settleRuntime(t, rt)
	if err := rt.Dispatch("在 webapp 里跑个命令"); err != nil {
		t.Fatal(err)
	}

	// Wait for the run to finish, not for the first token count: a two-round run
	// reports usage once per model call.
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		done := false
		for _, tk := range allSnapshots(eng) {
			if tk.LastReport() != nil {
				done = true
			}
		}
		if done {
			u := rt.usageSnapshot()
			// The mock reports usage on every call, and a two-round run has two.
			if u.Calls < 2 {
				t.Fatalf("expected at least two model calls, got %d", u.Calls)
			}
			if u.Prompt == 0 || u.Completion == 0 {
				t.Fatalf("usage has holes: %+v", u)
			}
			rt.Publish()
			if got := rt.snap.Overview().Usage.Total; got != u.Total {
				t.Fatalf("overview shows %d, runtime counted %d", got, u.Total)
			}
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("no usage was recorded")
}
