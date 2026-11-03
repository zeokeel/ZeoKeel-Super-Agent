// Package domain holds ZeoKeel's domain types. It performs no I/O and imports
// nothing outside the standard library, so it can be tested without a store,
// a model or a UI.
package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"strings"
)

// ID is a content-derived identifier: equal inputs give equal IDs, which is the
// basis of every idempotent path (see design §4 / 001 Domain model).
type ID string

// Scope is the first segment of every content-derived ID. A Trigger that comes
// from a workspace-scoped Source (GitHub, a Feishu group) carries that
// workspace; a Trigger from a global Source (a DingTalk direct message, a local
// panel message) carries Global, because no workspace is known until the Router
// decides. See design §4.7.
type Scope string

// Global is the Scope of a Source that is not bound to one workspace.
const Global = Scope("global")

// WorkspaceID identifies a workspace. It is generated once, when the workspace
// is created, and persisted; it is never derived from a path, because moving or
// renaming a directory must not orphan the workspace's history. See design §4.5.
type WorkspaceID string

// WorkspaceScope returns the Scope of one workspace.
func WorkspaceScope(id WorkspaceID) Scope { return Scope("ws:" + string(id)) }

// Key derives an ID from a scope and a list of parts. Including the scope first
// keeps two workspaces from colliding: repo A's issue #12 and repo B's issue #12
// must not become the same Thread.
func Key(scope Scope, parts ...any) ID {
	h := sha256.New()
	io.WriteString(h, string(scope))
	for _, p := range parts {
		io.WriteString(h, "\x00")
		fmt.Fprint(h, p)
	}
	return ID(hex.EncodeToString(h.Sum(nil))[:32])
}

// ThreadKey renders a human-readable Thread key, `<scope>|<source>|<id>`, so a
// log line or a panel row names the Thread without a lookup.
func ThreadKey(scope Scope, source, id string) string {
	return strings.Join([]string{string(scope), source, id}, "|")
}

// WorkspaceIDOf reports which workspace a scope names, or false for Global.
func (s Scope) WorkspaceIDOf() (WorkspaceID, bool) {
	const prefix = "ws:"
	if len(s) <= len(prefix) || string(s)[:len(prefix)] != prefix {
		return "", false
	}
	return WorkspaceID(string(s)[len(prefix):]), true
}
