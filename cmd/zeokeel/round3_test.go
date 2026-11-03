package main

// Round-3 durable paths: a workspace created from the panel and a conversation
// started in it must both survive a restart (ui2-spec §4).

import (
	"strings"
	"testing"
	"time"

	"github.com/zeokeel/super-agent/internal/domain"
	"github.com/zeokeel/super-agent/internal/engine"
	"github.com/zeokeel/super-agent/internal/runtime"
	"github.com/zeokeel/super-agent/internal/view"
)

func TestDurableWorkspaceAndConversationSurviveRestart(t *testing.T) {
	state := t.TempDir()
	root := t.TempDir()
	eng := engine.New(engine.RuleAssessor{})
	d, err := openDurable(eng, state, nil)
	if err != nil {
		t.Fatal(err)
	}
	rt := runtime.New(eng, view.NewSnapshot())
	if err := attachDurable(rt, d); err != nil {
		d.Close()
		t.Fatal(err)
	}

	if err := rt.CreateWorkspace("报表", root, "每天早上出用量报表", "data"); err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	var ws domain.WorkspaceID
	for _, w := range eng.Workspaces() {
		if w.Name == "报表" {
			ws = w.ID
		}
	}
	if ws == "" {
		t.Fatalf("workspace not registered: %+v", eng.Workspaces())
	}
	if err := rt.NewConversation(string(ws), "在报表里跑个命令"); err != nil {
		t.Fatalf("NewConversation: %v", err)
	}
	if n := len(eng.AllTickets()); n != 1 {
		t.Fatalf("tickets = %d", n)
	}
	// Let the queued launch handler park (no model configured) before closing.
	rt.WaitIdle(10 * time.Second)
	d.Close()

	eng2 := engine.New(engine.RuleAssessor{})
	d2, err := openDurable(eng2, state, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer d2.Close()
	foundWS := false
	for _, w := range eng2.Workspaces() {
		if w.Name == "报表" && strings.Contains(w.Brief.Purpose, "用量") {
			foundWS = true
		}
	}
	if !foundWS {
		t.Fatalf("workspace did not survive restart: %+v", eng2.Workspaces())
	}
	if n := len(eng2.AllTickets()); n != 1 {
		t.Fatalf("conversation did not survive restart: tickets = %d", n)
	}
}
