package main

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/zeokeel/super-agent/internal/engine"
)

// The app's own composition must survive a restart: workspaces, the Ticket a
// person dispatched, and the queue row that would launch it (design §7.2).
func TestAppStateSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(dir, "state")

	// First run: register workspaces and dispatch one task.
	eng := engine.New(engine.RuleAssessor{})
	d, err := openDurable(eng, state, nil)
	if err != nil {
		t.Fatal(err)
	}
	registerWorkspaces(eng, d)
	// No runtime attached: this test is about the rows that survive a crash, so
	// the launch effect must still be pending (with a runtime the queue would have
	// consumed it before the "restart").
	if _, err := d.pers.Dispatch(engine.DispatchInput{
		Text: "在 webapp 工作区里写一个 hello.txt", Workspace: "ws_web", At: time.Now(),
	}); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	out := evalPersistence(d.pers)
	if out.Ticket == "" {
		t.Fatal("no ticket was created")
	}
	if n := len(eng.AllTickets()); n != 1 {
		t.Fatalf("expected one ticket, got %d", n)
	}
	d.Close()

	// Second run: a fresh engine must find what the first one wrote.
	eng2 := engine.New(engine.RuleAssessor{})
	d2, err := openDurable(eng2, state, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer d2.Close()

	if got := eng2.Workspaces(); len(got) != 2 {
		t.Fatalf("expected two workspaces after restart, got %d", len(got))
	}
	tickets := eng2.AllTickets()
	if len(tickets) != 1 {
		t.Fatalf("expected one ticket after restart, got %d", len(tickets))
	}
	if tickets[0].Title == "" {
		t.Fatal("the restored ticket lost its title")
	}
	// The launch effect written by the first run is still queued, so the worker
	// runs on the next start instead of the work being lost.
	// A person's own message uses the human-priority kind (003:59); either kind is
	// the durable "this Ticket still has to run" row.
	pending, err := d2.queue.Pending(engine.EffectLaunch)
	if err != nil {
		t.Fatal(err)
	}
	pendingHuman, err := d2.queue.Pending(engine.EffectLaunchHuman)
	if err != nil {
		t.Fatal(err)
	}
	if !pending && !pendingHuman {
		t.Fatal("the launch effect did not survive the restart")
	}
}
