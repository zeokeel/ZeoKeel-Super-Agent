package view_test

// Tests for the Ticket detail the panel expands, the actions a stage offers, and
// the glossary the (?) entry shows. They live in the view package so the rules
// are pinned without a window; the panel's own tests then only check wiring.

import (
	"testing"

	. "github.com/zeokeel/super-agent/internal/view"
)

// The stage decides the actions, not the label: answering waits on a person,
// blocked/failed/delivery may retry, and everything still open can be stopped.
func TestActionsForStage(t *testing.T) {
	for _, tc := range []struct {
		stage string
		want  []TicketAction
	}{
		{StageEnded, nil},
		{StagePersonAnswer, []TicketAction{ActionAnswer, ActionStop}},
		{StagePersonBlocked, []TicketAction{ActionRetry, ActionStop}},
		{StagePersonFailed, []TicketAction{ActionRetry, ActionStop}},
		{StagePersonDelivery, []TicketAction{ActionRetry, ActionStop}},
		{StagePersonReview, []TicketAction{ActionStop}},
		{StageWorking, []TicketAction{ActionStop}},
		{StageWorkingQueued, []TicketAction{ActionStop}},
		{StageIdle, []TicketAction{ActionStop}},
	} {
		got := ActionsFor(TicketRow{StageID: tc.stage})
		if len(got) != len(tc.want) {
			t.Fatalf("%q: actions %v, want %v", tc.stage, got, tc.want)
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Fatalf("%q: action %d is %q, want %q", tc.stage, i, got[i], tc.want[i])
			}
		}
	}
}

// A stage the panel does not know is still stoppable (the runtime checks it), and
// a projector that says the Ticket may retry adds 重试 without changing the stage.
func TestActionsForUnknownStageAndCanRetry(t *testing.T) {
	if got := ActionsFor(TicketRow{StageID: "future/stage"}); len(got) != 1 || got[0] != ActionStop {
		t.Fatalf("unknown stage actions %v, want [stop]", got)
	}
	if got := ActionsFor(TicketRow{StageID: "future/stage", CanRetry: true}); len(got) != 2 ||
		got[0] != ActionRetry || got[1] != ActionStop {
		t.Fatalf("unknown stage with CanRetry actions %v, want [retry stop]", got)
	}
	// An ended Ticket offers nothing, whatever the projector says about retries.
	if got := ActionsFor(TicketRow{StageID: StageEnded, CanRetry: true}); got != nil {
		t.Fatalf("an ended Ticket offers %v", got)
	}
}

// An ended row is the only one with no actions; the label wording is never read.
func TestActionsIgnoreTheLabel(t *testing.T) {
	row := TicketRow{Stage: "等待回答", StageID: ""}
	if got := ActionsFor(row); len(got) != 1 || got[0] != ActionStop {
		t.Fatalf("a row without a stage offered %v", got)
	}
	if (TicketAction(ActionStop)).Label() != "停止" || (TicketAction(ActionAnswer)).Label() != "回答" ||
		(TicketAction(ActionRetry)).Label() != "重试" {
		t.Fatal("action labels changed")
	}
	if !(TicketAction(ActionStop)).NeedsConfirm() {
		t.Fatal("停止 must ask before it ends a Ticket")
	}
	if (TicketAction(ActionAnswer)).NeedsConfirm() || (TicketAction(ActionRetry)).NeedsConfirm() {
		t.Fatal("回答 and 重试 must not ask")
	}
}

// The detail says which of its parts the projector has not filled in, so the
// panel can say "暂无数据" about the right ones.
func TestTicketDetailEmptySections(t *testing.T) {
	var nilVM *TicketDetailVM
	if nilVM.EmptySections() != nil {
		t.Fatal("a nil detail listed sections")
	}
	vm := &TicketDetailVM{ID: "42"}
	want := []string{"Mandate 依据", "Attention", "Assignments / Reports", "Inputs", "Follows", "Artifacts"}
	got := vm.EmptySections()
	if len(got) != len(want) {
		t.Fatalf("empty sections %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("section %d is %q, want %q", i, got[i], want[i])
		}
	}
	full := &TicketDetailVM{
		Mandate:     &MandateDetail{Strength: "explicit"},
		Attention:   &TicketAttention{Question: "选哪个？"},
		Assignments: []AssignmentDetail{{ID: "a1"}},
		Inputs:      []InputDetail{{ID: "i1"}},
		Follows:     []FollowDetail{{Thread: "th-42"}},
		Artifacts:   []Artifact{{Title: "PR #128"}},
	}
	if got := full.EmptySections(); len(got) != 0 {
		t.Fatalf("a full detail still lists empty sections: %v", got)
	}
}

// The glossary is the prototype's: every term has both languages and a meaning,
// and no term repeats.
func TestGlossaryIsComplete(t *testing.T) {
	entries := Glossary()
	if len(entries) < 20 {
		t.Fatalf("glossary has %d entries", len(entries))
	}
	seen := map[string]bool{}
	for _, e := range entries {
		if e.Term == "" || e.Chinese == "" || e.Meaning == "" {
			t.Fatalf("incomplete entry %+v", e)
		}
		if seen[e.Term] {
			t.Fatalf("duplicate term %q", e.Term)
		}
		seen[e.Term] = true
	}
	for _, term := range []string{"Ticket", "Mandate", "Attention", "Assignment", "Subscription", "Strength"} {
		if !seen[term] {
			t.Fatalf("glossary is missing %q", term)
		}
	}
	if GlossaryLen() != len(entries) {
		t.Fatalf("GlossaryLen %d, entries %d", GlossaryLen(), len(entries))
	}
	groups := GlossaryValues()
	if len(groups) < 4 {
		t.Fatalf("value groups %d, want at least 4", len(groups))
	}
	for _, g := range groups {
		if g.Title == "" || len(g.Values) == 0 {
			t.Fatalf("empty value group %+v", g)
		}
	}
}

// The settings page always has its six groups (model first): a runtime that publishes only
// some of them still gets the others as a projection gap, never as a hidden
// group.
func TestRequiredSettingsGroups(t *testing.T) {
	groups := RequiredSettingsGroups()
	wantIDs := []SettingsGroupID{SettingsWorker, SettingsSource, SettingsEval, SettingsExecution, SettingsAbout, SettingsPrompt, SettingsDeploy}
	if len(groups) != len(wantIDs) {
		t.Fatalf("%d required groups, want %d", len(groups), len(wantIDs))
	}
	for i, g := range groups {
		if g.ID != wantIDs[i] {
			t.Fatalf("group %d is %q, want %q", i, g.ID, wantIDs[i])
		}
		if g.Label == "" || g.Note == "" {
			t.Fatalf("group %q has no label or note", g.ID)
		}
		if len(g.Pending) == 0 && g.ID != SettingsAbout {
			// 关于 is drawn from the build (version/license) and the optional
			// update checker, not from a projection: nothing to wait for.
			t.Fatalf("group %q does not say what it is waiting for", g.ID)
		}
	}
}
