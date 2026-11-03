// Package domain holds ZeoKeel's domain types. It performs no I/O and imports
// nothing outside the standard library, so it can be tested without a store,
// a model or a UI.
package domain

import (
	"crypto/rand"
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

// WorkspaceIDPrefix marks a real workspace id. A Scope of a workspace is
// "ws:<id>"; the bare prefix is never a workspace.
const WorkspaceIDPrefix = "ws_"

// NewWorkspaceID returns a fresh, stable workspace id. It is generated once when
// the workspace is created and persisted with it; it is never derived from a
// path or a display name, so moving or renaming a repository cannot orphan the
// workspace's history (design §4.5). The id is random, not content-derived: a
// workspace is a local configuration entity with no content to hash.
//
// crypto/rand can only fail when the platform has no entropy source at all; a
// process that cannot mint an identity should not pretend it did, so the failure
// panics with a clear message instead of producing a duplicate id.
func NewWorkspaceID() WorkspaceID {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("domain: cannot generate a workspace id: " + err.Error())
	}
	return WorkspaceID(WorkspaceIDPrefix + hex.EncodeToString(b[:]))
}

// Valid reports whether a workspace id is usable as an identity: non-empty and
// free of the separators that scope and Thread keys use.
func (id WorkspaceID) Valid() bool {
	s := string(id)
	return s != "" && s == strings.TrimSpace(s) && !strings.ContainsAny(s, "|:")
}

// WorkspaceScope returns the Scope of one workspace. An empty id is not a
// workspace: it yields Global, never the malformed "ws:" scope, because a fake
// workspace scope would let two different things collide on one id namespace.
func WorkspaceScope(id WorkspaceID) Scope {
	if id == "" {
		return Global
	}
	return Scope("ws:" + string(id))
}

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

// WorkspaceIDOf reports which workspace a scope names, or false for Global and
// for malformed scopes such as the bare "ws:".
func (s Scope) WorkspaceIDOf() (WorkspaceID, bool) {
	const prefix = "ws:"
	if len(s) <= len(prefix) || !strings.HasPrefix(string(s), prefix) {
		return "", false
	}
	id := WorkspaceID(string(s)[len(prefix):])
	if !id.Valid() {
		return "", false
	}
	return id, true
}
