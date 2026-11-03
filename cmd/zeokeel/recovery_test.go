package main

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/zeokeel/super-agent/internal/agent/mockllm"
	"github.com/zeokeel/super-agent/internal/domain"
	"github.com/zeokeel/super-agent/internal/engine"
	"github.com/zeokeel/super-agent/internal/runtime"
	"github.com/zeokeel/super-agent/internal/view"
)

// The promise that makes this agent usable unattended: a Ticket that was accepted
// but not executed survives the process dying, and the next start actually runs it
// (design §7.2). This test does not restart a real OS process; it closes every
// handle exactly as a crash would and opens a second one on the same files.
func TestAcceptedTicketRunsAfterRestart(t *testing.T) {
	srv, err := mockllm.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	srv.ToolName = "bash"
	srv.ToolArgs = `{"command":"echo recovered > done.txt"}`

	state := filepath.Join(t.TempDir(), "state")
	work := filepath.Join(state, "work")

	// First process: accept the work, then "crash" before the executor runs.
	eng := engine.New(engine.RuleAssessor{})
	d, err := openDurable(eng, state, nil)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := d.pers.Register(domain.Workspace{
		ID: "ws_web", Name: "web", Root: t.TempDir(), Kind: domain.KindRepo,
		Brief: domain.Brief{Purpose: "webapp", Keywords: []string{"webapp"}},
	}, enginePolicy("me")); err != nil {
		t.Fatal(err)
	}
	if _, err := d.pers.Dispatch(engine.DispatchInput{Text: "在 webapp 里写个文件", Workspace: "ws_web", At: now}); err != nil {
		t.Fatal(err)
	}
	// The 2s pass creates the Ticket and enqueues the launch effect; without it
	// nothing durable exists for the restart to recover.
	out := evalPersistence(d.pers)
	if out.Ticket == "" {
		t.Fatal("no ticket was created")
	}
	// No runtime was ever attached, so nothing launched the executor.
	if arts := eng.Artifacts(out.Ticket); len(arts) != 0 {
		t.Fatal("nothing should have run yet")
	}
	d.Close()

	// Second process: open the same directory, attach the executor, and wait for
	// the queued launch effect to do its job.
	eng2 := engine.New(engine.RuleAssessor{})
	d2, err := openDurable(eng2, state, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer d2.Close()
	rt := runtime.New(eng2, view.NewSnapshot())
	// Start the model before the durable queue: attachDurable starts the launch
	// effect immediately, and on a slow CI runner it can run before EnableWorker.
	rt.EnableWorker(runtime.WorkerConfig{
		BaseURL: srv.URL, APIKey: "sk-mock", Model: "mock-tool", Root: work,
	})
	if err := attachDurable(rt, d2); err != nil {
		t.Fatal(err)
	}

	// CI runners are slower (and -race doubles the cost of the mock model); give
	// the recovered launch effect a wide margin instead of a tight wall clock.
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		// Read a deep snapshot, never the engine's live pointer: the launch handler
		// goroutine may still be writing the Ticket while this loop reads it.
		tk, ok := eng2.TicketSnapshot(out.Ticket)
		if ok && len(tk.Artifacts) > 0 {
			// The deliverable exists on disk, not just in the domain.
			if _, err := filepath.Glob(filepath.Join(work, "ticket-*", "done.txt")); err != nil {
				t.Fatal(err)
			}
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	snap, _ := eng2.TicketSnapshot(out.Ticket)
	t.Fatalf("the restarted executor never ran the queued ticket: %+v", snap.LastReport())
}
