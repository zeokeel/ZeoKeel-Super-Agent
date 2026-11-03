package runtime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zeokeel/super-agent/internal/agent/mockllm"
	"github.com/zeokeel/super-agent/internal/domain"
	"github.com/zeokeel/super-agent/internal/engine"
	"github.com/zeokeel/super-agent/internal/view"
)

// A long run must compact its own context and offload oversized tool output. Both
// are the design's answer to "the window is finite" (§5.4), and both are easy to
// configure and never see, which is exactly why they need an end-to-end check.
func TestLongRunCompactsAndOffloads(t *testing.T) {
	srv, err := mockllm.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	srv.ToolName = "bash"
	// 200 KB of output is far past any sane tool-result limit.
	srv.ToolArgs = `{"command":"head -c 200000 /dev/zero | tr '\\0' 'x'"}`

	eng := engine.New(engine.RuleAssessor{})
	eng.Register(domain.Workspace{
		ID: "ws_web", Name: "web", Root: t.TempDir(), Kind: domain.KindRepo,
		Brief: domain.Brief{Purpose: "webapp", Keywords: []string{"webapp"}},
	}, engine.Policy{Version: "v1"})
	rt := New(eng, view.NewSnapshot())
	work := filepath.Join(t.TempDir(), "work")
	rt.EnableWorker(WorkerConfig{
		BaseURL: srv.URL, APIKey: "sk-mock", Model: "mock-tool", Root: work,
		// A budget below the fixed prompt would make the summarizer fire forever, so
		// the floor applies; the message-count trigger is what forces compaction here.
		ContextBudget: 4000, CompactAfterMessages: 3, ToolOutputLimit: 512,
	})
	settleRuntime(t, rt)
	if err := rt.Dispatch("在 webapp 里跑一个会喷出大量输出的命令"); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		done := false
		for _, tk := range allSnapshots(eng) {
			if tk.LastReport() != nil {
				done = true
			}
		}
		if done {
			rt.WaitIdle(20 * time.Second)
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	// The oversized output must be on disk, not in the model's context.
	offloaded, _ := filepath.Glob(filepath.Join(work, "ticket-*", ".zeokeel", "tool-output", "*"))
	if len(offloaded) == 0 {
		t.Fatalf("no tool output was offloaded under %s", work)
	}
	info, err := os.Stat(offloaded[0])
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() < 1000 {
		t.Fatalf("the offloaded file is only %d bytes", info.Size())
	}

	// The model must have been told about it by pointer instead of by content: the
	// hint names the file, and the message stays far below the 200 KB the command
	// actually produced.
	// Look across every model call, not just the last one: the summarizer's own
	// call is the final request and does not carry the tool message.
	var sawPersisted bool
	var toolBytes int
	for _, req := range srv.RecordedRequests() {
		for _, m := range req.Messages {
			if m.Role != "tool" {
				continue
			}
			if len(m.Content) > toolBytes {
				toolBytes = len(m.Content)
			}
			if strings.Contains(m.Content, "<persisted-output>") &&
				strings.Contains(m.Content, "Use read to view") {
				sawPersisted = true
			}
		}
	}
	if !sawPersisted {
		t.Fatal("the model never saw the persisted-output hint")
	}
	if toolBytes > 20000 {
		t.Fatalf("the tool result still carries %d bytes into the model", toolBytes)
	}

	// And the context was compacted: the panel must be able to show that early
	// history was summarised, not silently dropped.
	vm := rt.stream()
	found := false
	for _, th := range vm.Threads {
		for _, step := range th.Steps {
			if strings.Contains(step.Text, "上下文已压缩") {
				found = true
			}
		}
	}
	if !found {
		var texts []string
		for _, th := range vm.Threads {
			for _, step := range th.Steps {
				texts = append(texts, step.Text)
			}
		}
		t.Fatalf("no compaction was reported to the panel: %v", texts)
	}
}
