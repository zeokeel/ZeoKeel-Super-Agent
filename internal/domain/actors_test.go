package domain

import (
	"strings"
	"testing"
	"time"
)

// An empty workspace id must never create a fake workspace scope; it is Global.
func TestWorkspaceScopeEmptyIsGlobal(t *testing.T) {
	scope := WorkspaceScope("")
	if scope != Global {
		t.Fatalf("WorkspaceScope(\"\") = %q, want %q", scope, Global)
	}
	if _, ok := scope.WorkspaceIDOf(); ok {
		t.Fatal("the Global scope must not name a workspace")
	}
	// The same content must not hash differently just because a caller passed an
	// empty workspace instead of Global.
	if Key(WorkspaceScope(""), "github", "12") != Key(Global, "github", "12") {
		t.Fatal("an empty workspace scope must hash exactly like Global")
	}
	// A handcrafted bare "ws:" scope is malformed and names no workspace.
	for _, fake := range []Scope{"ws:", "ws", "ws:|ws_a|12", "ws: "} {
		if id, ok := fake.WorkspaceIDOf(); ok {
			t.Fatalf("malformed scope %q was accepted as workspace %q", fake, id)
		}
	}
	// A real workspace round-trips.
	id, ok := WorkspaceScope("ws_a").WorkspaceIDOf()
	if !ok || id != "ws_a" {
		t.Fatalf("WorkspaceScope(\"ws_a\") did not round-trip: %q %v", id, ok)
	}
	if WorkspaceScope("ws_a") == Global {
		t.Fatal("a real workspace must not collapse to Global")
	}
}

// NewWorkspaceID must be unique, well formed and accepted as a workspace id.
func TestNewWorkspaceID(t *testing.T) {
	seen := map[WorkspaceID]bool{}
	for i := 0; i < 200; i++ {
		id := NewWorkspaceID()
		if !id.Valid() {
			t.Fatalf("generated id %q is not valid", id)
		}
		if !strings.HasPrefix(string(id), WorkspaceIDPrefix) {
			t.Fatalf("generated id %q lacks the %q prefix", id, WorkspaceIDPrefix)
		}
		if got := len(string(id)) - len(WorkspaceIDPrefix); got != 12 {
			t.Fatalf("generated id %q has %d random hex chars, want 12", id, got)
		}
		if seen[id] {
			t.Fatalf("duplicate workspace id %q", id)
		}
		seen[id] = true
		if ws, ok := WorkspaceScope(id).WorkspaceIDOf(); !ok || ws != id {
			t.Fatalf("generated id %q does not round-trip through its scope", id)
		}
	}
	if (WorkspaceID("")).Valid() {
		t.Fatal("the empty id must not be valid")
	}
	if (WorkspaceID("a|b")).Valid() {
		t.Fatal("an id containing the Thread separator must not be valid")
	}
}

// "The operator's own message" is a fact combination, not a Source-name check.
func TestOperatorMessageSemantics(t *testing.T) {
	cases := []struct {
		name string
		tr   Trigger
		want bool
	}{
		{"Self on a colleague Source", Trigger{Source: "dingtalk", Actor: Actor{ID: "me"}, Self: true}, true},
		{"operator actor id on GitHub", Trigger{Source: "github", Actor: Actor{ID: OperatorActorID}}, true},
		{"panel message with default actor", Trigger{Source: "local"}, true},
		{"panel message from the operator", Trigger{Source: "local", Actor: Actor{ID: OperatorActorID}}, true},
		{"colleague on the panel", Trigger{Source: "local", Actor: Actor{ID: "alice"}}, false},
		{"listed colleague on GitHub", Trigger{Source: "github", Actor: Actor{ID: "lin"}}, false},
		{"a bot", Trigger{Source: "github", Actor: Actor{ID: "ci[bot]", Bot: true}}, false},
	}
	for _, c := range cases {
		if got := c.tr.IsOperatorMessage(); got != c.want {
			t.Errorf("%s: IsOperatorMessage=%v want %v", c.name, got, c.want)
		}
		if got := c.tr.CreatedByMe(); got != c.want {
			t.Errorf("%s: CreatedByMe=%v want %v", c.name, got, c.want)
		}
	}

	// A Ticket's creation is read from its Mandate, not its last message.
	op := Ticket{Mandate: Mandate{Actor: Actor{ID: OperatorActorID}}}
	if !op.CreatedByOperator() {
		t.Fatal("an operator-granted Ticket must count as operator-created")
	}
	opVia := Ticket{Mandate: Mandate{Actor: Actor{ID: "lin"}, Basis: Basis{Via: ViaOperator}}}
	if !opVia.CreatedByOperator() {
		t.Fatal("a ViaOperator Mandate must count as operator-created")
	}
	colleague := Ticket{Mandate: Mandate{Actor: Actor{ID: "lin"}, Basis: Basis{Via: ViaAddressed}}}
	if colleague.CreatedByOperator() {
		t.Fatal("a colleague's request must not count as operator-created")
	}
}

// A missing or implausible event timestamp becomes now; a good one is preserved.
func TestEnsureAtAndValidate(t *testing.T) {
	now := time.Date(2026, 11, 4, 10, 0, 0, 0, time.UTC)
	good := Trigger{ID: "t1", Source: "github", At: now.Add(-time.Hour),
		Threads: []ThreadRef{{Source: "github", ID: "owner/repo#1"}}, Human: true, Actor: Actor{ID: "lin"}}
	if good.EnsureAt(now) {
		t.Fatal("a valid timestamp must not be rewritten")
	}
	if err := good.Validate(); err != nil {
		t.Fatalf("a complete Trigger must validate: %v", err)
	}

	for name, at := range map[string]time.Time{
		"zero":       {},
		"1970":       time.Unix(0, 0),
		"1999":       time.Date(1999, 12, 31, 23, 59, 0, 0, time.UTC),
		"far future": now.Add(48 * time.Hour),
	} {
		tr := good
		tr.At = at
		if !tr.EnsureAt(now) {
			t.Fatalf("%s: EnsureAt did not fix the timestamp", name)
		}
		if !tr.At.Equal(now) {
			t.Fatalf("%s: EnsureAt left %s, want %s", name, tr.At, now)
		}
	}

	bad := []struct {
		name string
		tr   Trigger
	}{
		{"no id", Trigger{Source: "github", At: now, Threads: good.Threads}},
		{"no source", Trigger{ID: "t2", At: now, Threads: good.Threads}},
		{"source separator", Trigger{ID: "t3", Source: "git|hub", At: now, Threads: good.Threads}},
		{"zero time", Trigger{ID: "t4", Source: "github", Threads: good.Threads}},
		{"no thread", Trigger{ID: "t5", Source: "github", At: now}},
		{"incomplete thread", Trigger{ID: "t6", Source: "github", At: now, Threads: []ThreadRef{{Source: "github"}}}},
		{"human without actor", Trigger{ID: "t7", Source: "github", At: now, Threads: good.Threads, Human: true}},
	}
	for _, c := range bad {
		if err := c.tr.Validate(); err == nil {
			t.Errorf("%s: Validate accepted an incomplete Trigger", c.name)
		}
	}
}
