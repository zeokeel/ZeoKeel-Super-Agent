package store

import (
	"encoding/json"
	"testing"

	"github.com/zeokeel/super-agent/internal/domain"
)

func mustJSON(t *testing.T, s string) map[string]json.RawMessage {
	t.Helper()
	var doc map[string]json.RawMessage
	if err := json.Unmarshal([]byte(s), &doc); err != nil {
		t.Fatalf("unmarshal %q: %v", s, err)
	}
	return doc
}

func putWorkspaceWithPolicy(t *testing.T, s *Store, id domain.WorkspaceID, policy string) {
	t.Helper()
	if err := s.PutWorkspace(domain.Workspace{ID: id, Name: string(id), Root: "/" + string(id), Kind: domain.KindRepo}); err != nil {
		t.Fatal(err)
	}
	if err := s.PutWorkspacePolicy(id, policy); err != nil {
		t.Fatal(err)
	}
}

// The allowlist helpers exist because the engine's Policy carries more than the
// allowlist (accept/reply mode, admits-unaddressed, team rules). Patching only
// Allowed must leave everything else byte-for-byte intact.
func TestUpdateWorkspacePolicyAllowedPreservesOtherFields(t *testing.T) {
	s, _ := openTemp(t)
	const original = `{"Version":"v1","Allowed":{"local":{"operator":true}},` +
		`"AdmitsUnaddressed":true,"AcceptMode":"auto","ReplyMode":"notify",` +
		`"Rules":[{"ID":"r1","Instruction":"only weekdays"}]}`
	putWorkspaceWithPolicy(t, s, "ws_a", original)

	updated, err := s.UpdateWorkspacePolicyAllowed("ws_a", map[string]map[string]bool{
		"dingtalk": {"zhangsan": true},
	})
	if err != nil || !updated {
		t.Fatalf("update: updated=%v err=%v", updated, err)
	}
	policies, err := s.WorkspacePolicies()
	if err != nil {
		t.Fatal(err)
	}
	got := mustJSON(t, policies["ws_a"])
	before := mustJSON(t, original)

	if string(got["Allowed"]) != `{"dingtalk":{"zhangsan":true}}` {
		t.Fatalf("Allowed = %s", got["Allowed"])
	}
	for _, field := range []string{"Version", "AdmitsUnaddressed", "AcceptMode", "ReplyMode", "Rules"} {
		if string(got[field]) != string(before[field]) {
			t.Fatalf("field %q changed: got %s want %s", field, got[field], before[field])
		}
	}

	// A workspace that is not there is (false, nil): iterating a projection must
	// not fail because a workspace was deleted in between.
	if updated, err := s.UpdateWorkspacePolicyAllowed("ws_missing", map[string]map[string]bool{"x": {"y": true}}); err != nil || updated {
		t.Fatalf("missing workspace: updated=%v err=%v", updated, err)
	}
}

func TestUpdateAllWorkspacePoliciesAllowedPreservesFields(t *testing.T) {
	s, _ := openTemp(t)
	const withExtras = `{"Version":"v1","Allowed":{"local":{"operator":true}},` +
		`"AdmitsUnaddressed":false,"AcceptMode":"observe","ReplyMode":"auto"}`
	putWorkspaceWithPolicy(t, s, "ws_a", withExtras)
	putWorkspaceWithPolicy(t, s, "ws_b", `{"Version":"v1","Allowed":{}}`)

	allowed := map[string]map[string]bool{"dingtalk": {"zhangsan": true}}
	n, err := s.UpdateAllWorkspacePoliciesAllowed(allowed)
	if err != nil || n != 2 {
		t.Fatalf("update all: n=%d err=%v", n, err)
	}
	policies, err := s.WorkspacePolicies()
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []domain.WorkspaceID{"ws_a", "ws_b"} {
		doc := mustJSON(t, policies[id])
		var gotAllowed map[string]map[string]bool
		if err := json.Unmarshal(doc["Allowed"], &gotAllowed); err != nil {
			t.Fatalf("%s Allowed: %v", id, err)
		}
		if !gotAllowed["dingtalk"]["zhangsan"] || gotAllowed["local"]["operator"] {
			t.Fatalf("%s Allowed = %v", id, gotAllowed)
		}
	}
	extras := mustJSON(t, policies["ws_a"])
	if string(extras["AcceptMode"]) != `"observe"` || string(extras["ReplyMode"]) != `"auto"` {
		t.Fatalf("per-workspace fields were wiped: AcceptMode=%s ReplyMode=%s", extras["AcceptMode"], extras["ReplyMode"])
	}
}

// One bad policy row fails the whole allowlist save: half-updated workspaces are
// exactly the inconsistent state the per-workspace loop used to leave behind.
func TestUpdateAllWorkspacePoliciesAllowedIsAtomic(t *testing.T) {
	s, _ := openTemp(t)
	const good = `{"Version":"v1","Allowed":{"local":{"operator":true}},"AcceptMode":"auto"}`
	putWorkspaceWithPolicy(t, s, "ws_good", good)
	putWorkspaceWithPolicy(t, s, "ws_bad", `{not json`)

	if _, err := s.UpdateAllWorkspacePoliciesAllowed(map[string]map[string]bool{"x": {"y": true}}); err == nil {
		t.Fatal("a corrupt policy row was silently skipped")
	}
	policies, err := s.WorkspacePolicies()
	if err != nil {
		t.Fatal(err)
	}
	if policies["ws_good"] != good {
		t.Fatalf("rolled-back transaction changed ws_good: %s", policies["ws_good"])
	}
	if policies["ws_bad"] != `{not json` {
		t.Fatalf("rolled-back transaction changed ws_bad: %s", policies["ws_bad"])
	}

	// Fixing the row makes the same save succeed.
	if err := s.PutWorkspacePolicy("ws_bad", `{"Version":"v1"}`); err != nil {
		t.Fatal(err)
	}
	if n, err := s.UpdateAllWorkspacePoliciesAllowed(map[string]map[string]bool{"x": {"y": true}}); err != nil || n != 2 {
		t.Fatalf("second update all: n=%d err=%v", n, err)
	}
}

func TestPatchPolicyAllowedShapes(t *testing.T) {
	allowed := map[string]map[string]bool{"local": {"operator": true}}
	for _, in := range []string{"", "{}", "null", "  "} {
		out, err := PatchPolicyAllowed(in, allowed)
		if err != nil {
			t.Fatalf("PatchPolicyAllowed(%q): %v", in, err)
		}
		doc := mustJSON(t, out)
		if string(doc["Allowed"]) != `{"local":{"operator":true}}` {
			t.Fatalf("PatchPolicyAllowed(%q) Allowed = %s", in, doc["Allowed"])
		}
	}
	// A lowercase/aliased field is patched in place rather than duplicated.
	out, err := PatchPolicyAllowed(`{"allowed":{"x":{"y":true}},"Version":"v1"}`, allowed)
	if err != nil {
		t.Fatal(err)
	}
	doc := mustJSON(t, out)
	if len(doc) != 2 {
		t.Fatalf("patch created a second Allowed field: %s", out)
	}
	if _, ok := doc["allowed"]; !ok {
		t.Fatalf("patch renamed the existing field: %s", out)
	}
	// Non-object documents are refused instead of being overwritten.
	for _, in := range []string{`{not json`, `[]`, `"text"`, `42`} {
		if _, err := PatchPolicyAllowed(in, allowed); err == nil {
			t.Fatalf("PatchPolicyAllowed(%q) accepted a non-object", in)
		}
	}
}
