package agent

// verifier-A: independent falsification of the Wave-1 agent claims (audit N-2,
// N-3, N-5, N-6, F22 and the "tool error is an observation" rule). The fixtures
// here are built by this file; only the mock model server / runner helpers are
// shared with the package's own tests.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/adk/filesystem"
	"github.com/cloudwego/eino/adk/session"
	"github.com/cloudwego/eino/schema"
)

// A nested pattern must find files at every depth (`**`), which the hand-written
// filepath.Glob never did.
func TestVerifyGlobDoubleStarIsRecursive(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{"a/b/c/z.go", "internal/x/y/x.go", "top.go", "notes.md"} {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("package x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	be := &localBackend{root: root}
	for _, pattern := range []string{"**/*.go", "**/x.go", "a/**/*.go"} {
		got, err := be.GlobInfo(context.Background(), &filesystem.GlobInfoRequest{Pattern: pattern})
		if err != nil {
			t.Fatalf("GlobInfo(%q): %v", pattern, err)
		}
		var names []string
		for _, g := range got {
			names = append(names, g.Path)
		}
		joined := strings.Join(names, ",")
		switch pattern {
		case "**/*.go":
			for _, want := range []string{"z.go", "x.go", "top.go"} {
				if !strings.Contains(joined, want) {
					t.Fatalf("**/*.go did not match %s (got %v)", want, names)
				}
			}
			if strings.Contains(joined, "notes.md") {
				t.Fatalf("**/*.go matched a .md file: %v", names)
			}
		case "**/x.go":
			if !strings.Contains(joined, "internal/x/y/x.go") {
				t.Fatalf("**/x.go did not match the nested file: %v", names)
			}
		}
	}
}

// A symlink inside the workspace pointing outside must be refused by the os.Root
// backend: neither reading nor writing through it may touch the outside file.
func TestVerifySymlinkEscapeRefused(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(secret, []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link.txt")
	if err := os.Symlink(secret, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	dirLink := filepath.Join(root, "linkdir")
	if err := os.Symlink(outside, dirLink); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	be := &localBackend{root: root}
	if _, err := be.Read(context.Background(), &filesystem.ReadRequest{FilePath: "link.txt"}); err == nil {
		t.Fatal("read through an escaping symlink succeeded")
	}
	if err := be.Write(context.Background(), &filesystem.WriteRequest{FilePath: "link.txt", Content: "overwritten"}); err == nil {
		t.Fatal("write through an escaping symlink succeeded")
	}
	if got, err := os.ReadFile(secret); err != nil || string(got) != "outside" {
		t.Fatalf("outside file changed/read: %q err=%v", got, err)
	}
	if _, err := be.Read(context.Background(), &filesystem.ReadRequest{FilePath: "linkdir/secret.txt"}); err == nil {
		t.Fatal("read through an escaping directory symlink succeeded")
	}
	// The refusal must be an observation for the model, not a turn-fatal error.
	if _, ok := toolErrorObservation(&refusalError{msg: "x"}); !ok {
		t.Fatal("a refusal is not classified as an observation")
	}
}

// A multi-word blocked command must match, while a harmless command starting with
// the same word must pass. This is exactly the bug that made `git push --force`
// never match (the old code compared only the first word).
func TestVerifyBlockedCommandsMultiWord(t *testing.T) {
	b := newBlockedCommands([]string{"git push --force", "rm"})
	if entry, ok := b.match("git push --force origin main"); !ok || entry != "git push --force" {
		t.Fatalf("multi-word block did not match: entry=%q ok=%v", entry, ok)
	}
	if _, ok := b.match("git status"); ok {
		t.Fatal("git status was refused by a `git push --force` rule")
	}
	if _, ok := b.match("rm -rf /tmp/x"); !ok {
		t.Fatal("single-word legacy entry stopped matching")
	}
	// Whitespace normalization: tabs/newlines in the command line still match.
	if _, ok := b.match("git   push\t--force"); !ok {
		t.Fatal("whitespace normalization lost the multi-word match")
	}
}

// A super slow command must be an observation with exit code 124, never a Go
// error (which would fail the whole turn), and it must return promptly.
func TestVerifyBashTimeoutIsAnObservation(t *testing.T) {
	root := t.TempDir()
	// The production path always has the background-task job registry: the
	// foreground window is longer than the command timeout, so the command is
	// killed by its own deadline and reported as 124.
	jobs, err := newJobRegistry(context.Background(), 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	res, err := runBash(context.Background(), root, nil, time.Second, jobs,
		BashArgs{Command: "sleep 5", Timeout: 1})
	if err != nil {
		t.Fatalf("a timed-out command returned a Go error: %v", err)
	}
	if res.ExitCode != 124 {
		t.Fatalf("timed-out exit code = %d, want 124 (output %q)", res.ExitCode, res.Output)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("a 100ms timeout took %s", elapsed)
	}
}

// A blocked command is a refusal observation (exit 126), not a Go error.
func TestVerifyBlockedCommandIsObservation(t *testing.T) {
	root := t.TempDir()
	res, err := runBash(context.Background(), root,
		newBlockedCommands([]string{"git push --force"}), time.Second, nil,
		BashArgs{Command: "git push --force origin main"})
	if err != nil {
		t.Fatalf("blocked command returned a Go error: %v", err)
	}
	if res.ExitCode == 0 || !strings.Contains(res.Output, "refused") {
		t.Fatalf("blocked command = %+v, want a refusal observation", res)
	}
}

// The production restart path is RunSession: given a log that ends with TWO
// unmatched tool calls (a crash mid-parallel-tools), it must pair both with
// interrupted tool messages and let the model continue instead of failing.
func TestVerifyRunSessionRepairsMultipleDanglingToolCalls(t *testing.T) {
	srv := startMock(t)
	dir := t.TempDir()
	r := newTestRunner(t, testConfig(srv, dir, "mock-text"))
	ctx := context.Background()
	const sid = "worker-dangling-2"

	st, err := session.NewFileStore[*schema.Message](r.Config().SessionDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	ev := func() string {
		id, err := newSessionEventID()
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	now := time.Now().UTC()
	crash := []*adk.SessionEvent[*schema.Message]{
		{EventID: ev(), Timestamp: now, Kind: adk.SessionEventSessionStatusRunning,
			Lifecycle: &adk.LifecycleEvent{State: adk.SessionRunStateRunning}},
		{EventID: ev(), Timestamp: now, Kind: adk.SessionEventMessage, Message: schema.UserMessage("do it")},
		{EventID: ev(), Timestamp: now, Kind: adk.SessionEventMessage,
			Message: schema.AssistantMessage("", []schema.ToolCall{
				{ID: "call_a", Function: schema.FunctionCall{Name: "bash", Arguments: `{"command":"echo a"}`}},
				{ID: "call_b", Function: schema.FunctionCall{Name: "bash", Arguments: `{"command":"echo b"}`}},
			})},
	}
	if err := st.AppendEvents(ctx, sid, crash); err != nil {
		t.Fatal(err)
	}
	res, err := r.RunSession(ctx, RunRequest{SessionID: sid, System: "sys", Task: "continue"}, nil)
	if err != nil {
		t.Fatalf("RunSession after a two-tool crash failed: %v", err)
	}
	if !res.Resumed {
		t.Fatal("RunSession did not take the recovery path")
	}
	last := srv.LastRequest()
	paired := map[string]bool{}
	for _, m := range last.Messages {
		if m.Role == "tool" && strings.Contains(m.Content, "interrupted") {
			paired[m.ToolCallID] = true
		}
	}
	if !paired["call_a"] || !paired["call_b"] {
		t.Fatalf("dangling calls not all paired; tool messages: %v", paired)
	}
}

// The file tools must keep the names and descriptions the prompts refer to after
// the migration to the eino filesystem middleware.
func TestVerifyFileToolNamesAndDescriptions(t *testing.T) {
	want := []string{"read", "write", "edit", "glob", "grep"}
	if strings.Join(fileToolNames, ",") != strings.Join(want, ",") {
		t.Fatalf("file tool names/order = %v, want %v (the prompt-cache prefix depends on it)", fileToolNames, want)
	}
	for name, desc := range map[string]string{
		"read": readToolDesc, "write": writeToolDesc, "edit": editToolDesc,
		"glob": globToolDesc, "grep": grepToolDesc,
	} {
		if strings.TrimSpace(desc) == "" {
			t.Fatalf("tool %q has no model-facing description", name)
		}
	}
	if !strings.Contains(editToolDesc, "old_string") || strings.Contains(editToolDesc, "old_text") {
		t.Fatalf("edit description does not match the official schema's parameter names: %q", editToolDesc)
	}
	if !strings.Contains(globToolDesc, "glob") {
		t.Fatalf("glob description lost its anchor: %q", globToolDesc)
	}
}
