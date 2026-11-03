package prompts

import (
	"strings"
	"testing"
)

// The completion contract is what makes a ticket finishable: present + report. A
// prompt that loses it produces a chat answer and a hanging ticket.
func TestWorkerPromptKeepsTheContract(t *testing.T) {
	p := Worker("webapp", "面向客户的 web 控制台")
	for _, want := range []string{
		"webapp", "面向客户的 web 控制台",
		"ALREADY the root", // the live-model failure: it created a webapp/ subdir
		"present()", "report(state=\"ready\"",
		"ask_user_question", "report(state=\"blocked\"",
		"without a check you performed",
		"language of the request",
	} {
		if !strings.Contains(p, want) {
			t.Fatalf("worker prompt lost %q", want)
		}
	}
	if strings.Contains(p, "{{") {
		t.Fatal("unexpanded variable in the worker prompt")
	}
}

// The router is a judge: it must keep the accept contract and the reason codes the
// panel can render.
func TestRouterPromptKeepsTheJudgeContract(t *testing.T) {
	p := RouterPrompt()
	for _, want := range []string{
		"kind=accept", "kind=ignore", "kind=ask", "kind=stop",
		"intent_evidence", "worktree_name", "one entry per ENABLED rule",
		"said=", "not_addressed=", "no_rule_matched=",
		"one JSON object",
	} {
		if !strings.Contains(p, want) {
			t.Fatalf("router prompt lost %q", want)
		}
	}
}

func TestSubagentPromptsStayNarrow(t *testing.T) {
	if p := Explorer(); !strings.Contains(p, "read-only") || !strings.Contains(p, "Do not modify") {
		t.Fatalf("explorer is not clearly read-only: %q", p)
	}
	if p := Verifier(); !strings.Contains(p, "exit code") || !strings.Contains(p, "Never say") {
		t.Fatalf("verifier does not demand evidence: %q", p)
	}
}

func TestSectionsAreOrderedAndOverridable(t *testing.T) {
	s := New(
		Section{Name: "b", Order: 20, Body: "beta"},
		Section{Name: "a", Order: 10, Body: "alpha"},
	)
	if got := strings.Join(s.Names(), ","); got != "a,b" {
		t.Fatalf("order = %s", got)
	}
	s.Add(Section{Name: "a", Order: 10, Body: "ALPHA"})
	if got := s.Render(nil); !strings.HasPrefix(got, "ALPHA") {
		t.Fatalf("override did not replace: %q", got)
	}
	s.Remove("b")
	if got := s.Render(nil); strings.Contains(got, "beta") {
		t.Fatalf("remove failed: %q", got)
	}
}

func TestUnknownVariableStaysVisible(t *testing.T) {
	s := New(Section{Name: "x", Order: 1, Body: "hello {{who}}"})
	if got := s.Render(nil); !strings.Contains(got, "{{who}}") {
		t.Fatalf("an unknown variable must stay visible, got %q", got)
	}
}

// The router cannot create a workspace, but it is the component that notices one is
// missing. Its question has to carry enough for the operator to decide.
func TestRouterAsksForAMissingWorkspace(t *testing.T) {
	p := RouterPrompt()
	for _, want := range []string{
		"you cannot create one",
		"kind=ask",
		"repository, data or docs",
		"which keywords should route to it",
	} {
		if !strings.Contains(p, want) {
			t.Fatalf("router prompt lost %q", want)
		}
	}
}

// 002:11-16: authorization (admission + strength) belongs to the deterministic gate,
// not the model. The judge prompt must not ask for a `strength` field and must say so.
// The engine's AssessOutput has no Strength field; a prompt that asks for one is a
// stale contract that invites the model to claim access the gate refused.
func TestRouterPromptLeavesAuthorizationToTheGate(t *testing.T) {
	p := RouterPrompt()
	if strings.Contains(p, `"strength"`) || strings.Contains(p, "- strength:") {
		t.Fatalf("router prompt still asks the model for a strength: %q", p)
	}
	if strings.Contains(p, `"strength":`) {
		t.Fatal("the JSON example still has a strength key")
	}
	for _, want := range []string{
		"not yours to decide",
		"deterministic gate",
		"Do NOT output or infer a strength",
		"explicit, inferred or not admitted",
	} {
		if !strings.Contains(p, want) {
			t.Fatalf("router prompt lost the deterministic-gate note %q", want)
		}
	}
	// The contract that remains is the model's job: what is asked, who owns it, and
	// one verdict per enabled rule.
	for _, want := range []string{"intent_evidence", "worktree_name", "one entry per ENABLED rule"} {
		if !strings.Contains(p, want) {
			t.Fatalf("router prompt lost the judge contract %q", want)
		}
	}
}
