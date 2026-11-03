package scan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTree(t *testing.T, root string) {
	t.Helper()
	files := map[string]string{
		"go.mod":            "module example.com/demo\n",
		"README.md":         "# 客户数据服务\n\n处理 acme 的用量与账单。\n",
		"cmd/api/main.go":   "package main\n",
		"cmd/job/main.go":   "package main\n",
		"internal/db/db.go": "package db\n",
		"docs/schema.md":    "# schema\n",
		"scripts/run.sh":    "#!/bin/sh\n",
		".git/HEAD":         "ref: refs/heads/main\n",
		".git/config":       "[core]\n\trepositoryformatversion = 0\n[remote \"origin\"]\n\turl = git@github.com:acme/customer-data.git\n",
	}
	for name, body := range files {
		full := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// The scan is what fills the brief with facts nobody typed by hand: stack, commands,
// remote, and an outline of the directories.
func TestScanReadsAWorkspace(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root)

	f := Scan(root)
	if f.Remote != "git@github.com:acme/customer-data.git" {
		t.Fatalf("remote = %q", f.Remote)
	}
	if f.DefaultBranch != "main" {
		t.Fatalf("branch = %q", f.DefaultBranch)
	}
	if f.Language != "Go" || f.PackageMgr != "go modules" {
		t.Fatalf("stack = %s/%s", f.Language, f.PackageMgr)
	}
	if f.BuildCmd != "go build ./..." || f.TestCmd != "go test ./..." {
		t.Fatalf("commands = %q %q", f.BuildCmd, f.TestCmd)
	}
	if !strings.Contains(f.AgentsMD, "客户数据服务") {
		t.Fatalf("heading = %q", f.AgentsMD)
	}
	if len(f.DirSummary) == 0 {
		t.Fatal("no directory outline")
	}
	var names []string
	for _, d := range f.DirSummary {
		names = append(names, d.Name)
	}
	joined := strings.Join(names, ",")
	for _, want := range []string{"cmd", "docs", "internal", "scripts"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("outline missing %q: %s", want, joined)
		}
	}
	if strings.Contains(joined, ".git") {
		t.Fatalf("outline leaked hidden dirs: %s", joined)
	}
	if f.ObservedAt.IsZero() {
		t.Fatal("facts have no timestamp")
	}
}

// Missing roots and empty directories degrade instead of failing.
func TestScanIsBestEffort(t *testing.T) {
	if f := Scan(filepath.Join(t.TempDir(), "nope")); !f.ObservedAt.IsZero() && f.Language != "" {
		t.Fatalf("missing root produced facts: %+v", f)
	}
	empty := t.TempDir()
	f := Scan(empty)
	if f.Language != "" || len(f.DirSummary) != 0 {
		t.Fatalf("empty root produced facts: %+v", f)
	}
	if f.ObservedAt.IsZero() {
		t.Fatal("even an empty scan records when it ran")
	}
}

// The scan must not write into the workspace it reads.
func TestScanOnlyReads(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root)
	before := snapshot(t, root)
	Scan(root)
	after := snapshot(t, root)
	if before != after {
		t.Fatalf("the scan changed the tree:\n%s\n---\n%s", before, after)
	}
}

func snapshot(t *testing.T, root string) string {
	t.Helper()
	var out []string
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		out = append(out, rel)
		return nil
	})
	return strings.Join(out, "\n")
}
