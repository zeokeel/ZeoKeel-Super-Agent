package agent

// Prompt caching is not a switch we flip: it is a property of the request. A
// provider reuses its KV cache only when the serialized prefix (system prompt,
// tool list, earlier messages) is byte-identical to the previous request. So the
// test of "do we use the cache" is a test of prefix stability.

import (
	"context"
	"strings"
	"testing"

	"github.com/zeokeel/super-agent/internal/agent/mockllm"
)

// Across the turns of one run, the prefix must not change: same system prompt,
// same tool list in the same order, and each request's messages extend the last.
func TestRequestPrefixIsStableAcrossTurns(t *testing.T) {
	srv, err := mockllm.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	srv.ToolName = "bash"
	srv.ToolArgs = `{"command":"echo hi"}`

	r, err := New(Config{
		BaseURL: srv.URL, APIKey: "sk-mock", Model: "mock-tool",
		Workdir: t.TempDir(), MaxSteps: 4,
	})
	if err != nil {
		t.Fatal(err)
	}
	system := "你是执行器。用 bash 创建 hello.txt，然后一句话汇报。"
	if _, err := r.RunSession(context.Background(), RunRequest{
		System: system, Task: "创建 hello.txt",
	}, nil); err != nil {
		t.Fatal(err)
	}

	reqs := srv.RecordedRequests()
	if len(reqs) < 2 {
		t.Fatalf("expected two model calls (tool round), got %d", len(reqs))
	}

	first := reqs[0]
	for i, req := range reqs[1:] {
		if req.System != first.System {
			t.Fatalf("request %d changed the system prompt (cache miss by construction)", i+1)
		}
		if strings.Join(req.ToolNames, ",") != strings.Join(first.ToolNames, ",") {
			t.Fatalf("request %d changed the tool list/order:\n%v\n%v",
				i+1, first.ToolNames, req.ToolNames)
		}
		// Append-only history: the previous request's messages must be a prefix.
		if len(req.Messages) < len(first.Messages) {
			t.Fatalf("request %d has fewer messages than the first", i+1)
		}
		for j, m := range first.Messages {
			if req.Messages[j].Role != m.Role || req.Messages[j].Content != m.Content {
				t.Fatalf("request %d rewrote message %d:\n%q %q\n%q %q",
					i+1, j, m.Role, m.Content, req.Messages[j].Role, req.Messages[j].Content)
			}
		}
	}
}

// Stable does not mean "identical forever": a resumed session appends to the same
// prefix, which is still cacheable.
func TestResumeKeepsThePrefix(t *testing.T) {
	srv, err := mockllm.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	srv.ToolName = "bash"
	srv.ToolArgs = `{"command":"echo hi"}`

	dir := t.TempDir()
	r, err := New(Config{
		BaseURL: srv.URL, APIKey: "sk-mock", Model: "mock-tool",
		Workdir: dir, MaxSteps: 4, SessionDir: dir + "/sessions",
	})
	if err != nil {
		t.Fatal(err)
	}
	system := "你是执行器。"
	const session = "cache-resume"
	if _, err := r.RunSession(context.Background(), RunRequest{
		SessionID: session, System: system, Task: "第一步",
	}, nil); err != nil {
		t.Fatal(err)
	}
	first := srv.RecordedRequests()[0]

	// A second run on the same session (as after a restart) must extend it.
	if _, err := r.RunSession(context.Background(), RunRequest{
		SessionID: session, System: system, Task: "第二步",
	}, nil); err != nil {
		t.Fatal(err)
	}
	reqs := srv.RecordedRequests()
	last := reqs[len(reqs)-1]
	if last.System != first.System {
		t.Fatal("the resumed run changed the system prompt")
	}
	for j, m := range first.Messages {
		if j >= len(last.Messages) {
			t.Fatal("the resumed run dropped earlier messages")
		}
		if last.Messages[j].Role != m.Role || last.Messages[j].Content != m.Content {
			t.Fatalf("the resumed run rewrote message %d", j)
		}
	}
}
