package view

import (
	"strings"
	"testing"
)

// Every code the engine can emit must have a sentence a person can read. The list
// here is the contract: adding a code without a template fails this test, which is
// how the panel avoids showing "not_a_member" to someone who does not read code.
func TestEveryKnownReasonCodeHasText(t *testing.T) {
	codes := []string{
		"said", "routed", "allowed_by_mode", "started_ticket",
		"notification", "not_addressed", "not_a_member", "clarify",
		"observed", "accept_mode_off", "reply_mode_notify",
		"rejected_by_person", "sensitive_workspace",
	}
	for _, code := range codes {
		text := ReasonText(code)
		if text == "" {
			t.Fatalf("%q has no text", code)
		}
		if text == code {
			t.Fatalf("%q is rendered as itself, which hides the missing template", code)
		}
		if strings.Contains(text, "还没有文案") {
			t.Fatalf("%q fell through to the unrecovered branch", code)
		}
	}
}

// An unknown code is shown honestly, not swallowed and not guessed.
func TestUnknownReasonCodeIsExplicit(t *testing.T) {
	if got := ReasonText("future_code"); !strings.Contains(got, "future_code") ||
		!strings.Contains(got, "还没有文案") {
		t.Fatalf("unknown code rendered as %q", got)
	}
	if got := ReasonText(""); got == "" {
		t.Fatal("an empty code still needs a sentence")
	}
}
