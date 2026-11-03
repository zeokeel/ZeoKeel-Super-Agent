package domain

import "time"

// WorkspaceKind says what a workspace's root directory holds, which decides the
// tool allowlist its Workers get. A workspace is not necessarily a code
// repository: "the customer's data" may live in a directory of exports and a
// query script. See design §4.7 修正 2.
type WorkspaceKind string

const (
	KindRepo  WorkspaceKind = "repo"  // git/gh allowed
	KindData  WorkspaceKind = "data"  // sqlite + scripts; no git push
	KindDocs  WorkspaceKind = "docs"  // documents; no shell
	KindMixed WorkspaceKind = "mixed" // both
)

// Workspace is one project the super agent serves: a local root plus what it can
// do there. It is the unit of execution, authorization, budget and credentials.
type Workspace struct {
	ID     WorkspaceID
	Name   string
	Root   string // the only required field
	Remote string // empty for a data/docs workspace
	Kind   WorkspaceKind
	Brief  Brief
}

// Brief is the small, bounded description the Router keeps in context for one
// workspace. It is a cache, never the truth: when it disagrees with what
// workspace_read returns, the read wins. See design §4.6.
type Brief struct {
	// Written by a person, in the workspace management page.
	Purpose     string
	Scope       []string // what it is responsible for
	OutOfScope  []string // explicitly not its job, often naming another workspace
	Constraints []string
	Keywords    []string // customer names, code names, short forms: routing uses these
	Aliases     []string

	// Observed by the scanner, refreshed on demand and weekly.
	Facts Facts
}

// Facts are what the scanner observed. Every fact carries when it was seen, so
// the Router can tell a stale brief from a fresh one.
type Facts struct {
	ObservedAt    time.Time
	Remote        string
	DefaultBranch string
	Language      string
	PackageMgr    string
	BuildCmd      string
	TestCmd       string
	LintCmd       string
	DirSummary    []DirEntry
	AgentsMD      string // a summary, not the whole file
	OpenPRs       int
	LastActivity  time.Time
}

// DirEntry is one top-level directory and what it holds.
type DirEntry struct {
	Name    string
	Summary string
}

// StaleAfter is how long observed facts stay trusted.
const StaleAfter = 7 * 24 * time.Hour

// Stale reports whether the observed facts are older than StaleAfter.
func (b Brief) Stale(now time.Time) bool {
	return b.Facts.ObservedAt.IsZero() || now.Sub(b.Facts.ObservedAt) > StaleAfter
}
