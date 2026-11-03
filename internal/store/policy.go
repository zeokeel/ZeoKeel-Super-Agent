package store

// Workspace policy persistence. The policy row is opaque JSON owned by the engine
// (engine.Policy); the store never interprets most of it. It does know one thing:
// the document has an "Allowed" object mapping source -> actor -> bool, which is
// what the panel's global allowlist edits. Without a read-modify-write helper, a
// caller saving the allowlist has to marshal a whole new Policy — and the only
// value it has is the allowlist, so every other field (accept mode, reply mode,
// admits-unaddressed, team rules) is silently wiped. That was design review R2
// finding D9; these helpers are the store-side fix.
//
// The contract for callers:
//   - use UpdateWorkspacePolicyAllowed / UpdateAllWorkspacePoliciesAllowed when
//     changing only the allowlist;
//   - use PutWorkspacePolicy only when writing a complete policy document.
// Both are single-transaction read-modify-writes; UpdateAll... patches every
// workspace atomically so a failure cannot leave half the workspaces updated.

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/zeokeel/super-agent/internal/domain"
)

// PolicyAllowedField is the policy document field the allowlist helpers patch.
// Matching is case-insensitive so a future JSON tag change does not silently create
// a second field.
const PolicyAllowedField = "Allowed"

// PatchPolicyAllowed returns policyJSON with only its Allowed field replaced by
// allowed; every other field is preserved verbatim. An empty or "null" document is
// treated as "{}" (a workspace registered before it had a policy), which is what
// lets a v1-era row be patched without a data migration. The result is compact JSON.
func PatchPolicyAllowed(policyJSON string, allowed map[string]map[string]bool) (string, error) {
	doc := map[string]json.RawMessage{}
	trimmed := strings.TrimSpace(policyJSON)
	if trimmed != "" && trimmed != "null" {
		if err := json.Unmarshal([]byte(trimmed), &doc); err != nil {
			return "", fmt.Errorf("policy is not a JSON object: %w", err)
		}
	}
	if doc == nil {
		doc = map[string]json.RawMessage{}
	}
	if allowed == nil {
		allowed = map[string]map[string]bool{}
	}
	rawAllowed, err := json.Marshal(allowed)
	if err != nil {
		return "", fmt.Errorf("encode allowed list: %w", err)
	}
	key := PolicyAllowedField
	for k := range doc {
		if strings.EqualFold(k, PolicyAllowedField) {
			key = k
			break
		}
	}
	doc[key] = rawAllowed
	out, err := json.Marshal(doc)
	if err != nil {
		return "", fmt.Errorf("encode policy: %w", err)
	}
	return string(out), nil
}

// UpdateWorkspacePolicyAllowed replaces one workspace's Allowed list and leaves the
// rest of its policy untouched. It reports whether the workspace exists; a missing
// workspace is (false, nil), because callers iterate a projection and a workspace
// deleted in between is not an error.
func (s *Store) UpdateWorkspacePolicyAllowed(ws domain.WorkspaceID, allowed map[string]map[string]bool) (bool, error) {
	updated := false
	err := s.Atomic(func() error {
		var current string
		err := s.QueryRow(`SELECT policy FROM workspace WHERE id=?`, string(ws)).Scan(&current)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		patched, err := PatchPolicyAllowed(current, allowed)
		if err != nil {
			return fmt.Errorf("workspace %s: %w", ws, err)
		}
		res, err := s.Exec(`UPDATE workspace SET policy=? WHERE id=?`, patched, string(ws))
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		updated = n > 0
		return nil
	})
	return updated, err
}

// UpdateAllWorkspacePoliciesAllowed replaces the Allowed list of every workspace in
// one transaction. The allowlist is a global setting, so the save is global too; a
// single transaction is what makes the multi-workspace state all-or-nothing (the
// previous per-workspace loop could stop half way and leave mixed grants).
func (s *Store) UpdateAllWorkspacePoliciesAllowed(allowed map[string]map[string]bool) (int, error) {
	type row struct {
		id     string
		policy string
	}
	var rows []row
	err := s.Atomic(func() error {
		cursor, err := s.Query(`SELECT id, policy FROM workspace ORDER BY id`)
		if err != nil {
			return err
		}
		for cursor.Next() {
			var r row
			if err := cursor.Scan(&r.id, &r.policy); err != nil {
				cursor.Close()
				return err
			}
			rows = append(rows, r)
		}
		if err := cursor.Err(); err != nil {
			cursor.Close()
			return err
		}
		cursor.Close()
		for _, r := range rows {
			patched, err := PatchPolicyAllowed(r.policy, allowed)
			if err != nil {
				return fmt.Errorf("workspace %s: %w", r.id, err)
			}
			if _, err := s.Exec(`UPDATE workspace SET policy=? WHERE id=?`, patched, r.id); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return len(rows), nil
}
