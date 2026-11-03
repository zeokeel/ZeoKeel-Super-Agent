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
	"github.com/zeokeel/super-agent/internal/skills"
	"github.com/zeokeel/super-agent/internal/view"
)

// The worker's transcript must reach the stream: a person watching the panel has
// to see the tool the agent ran, not only that something finished.
func TestWorkerStepsReachTheStream(t *testing.T) {
	srv, err := mockllm.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	srv.ToolName = "bash"
	srv.ToolArgs = `{"command":"echo hi"}`

	eng := engine.New(engine.RuleAssessor{})
	eng.Register(domain.Workspace{
		ID: "ws_web", Name: "web", Root: t.TempDir(), Kind: domain.KindRepo,
		Brief: domain.Brief{Purpose: "webapp", Keywords: []string{"webapp"}},
	}, engine.Policy{Version: "v1"})

	rt := New(eng, view.NewSnapshot())
	rt.EnableWorker(WorkerConfig{
		BaseURL: srv.URL, APIKey: "sk-mock", Model: "mock-tool",
		Root: t.TempDir(),
	})
	settleRuntime(t, rt)
	if err := rt.Dispatch("在 webapp 里跑个命令"); err != nil {
		t.Fatal(err)
	}

	// Wait for the transcript to settle rather than sleeping a fixed time.
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		vm := rt.stream()
		if hasKind(vm, view.LineTool) && hasText(vm, "执行完成") {
			rt.WaitIdle(20 * time.Second)
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	vm := rt.stream()
	var kinds []string
	for _, th := range vm.Threads {
		for _, s := range th.Steps {
			kinds = append(kinds, string(s.Kind)+":"+s.Text+s.Tool)
		}
	}
	t.Fatalf("the stream never showed a tool step; got %v", kinds)
}

func hasKind(vm *view.StreamVM, k view.LineKind) bool {
	for _, th := range vm.Threads {
		for _, s := range th.Steps {
			if s.Kind == k {
				return true
			}
		}
	}
	return false
}

func hasText(vm *view.StreamVM, want string) bool {
	for _, th := range vm.Threads {
		for _, s := range th.Steps {
			if s.Text == want {
				return true
			}
		}
	}
	return false
}

// A finished run must leave a deliverable and a report behind: the panel's
// 交付物 section and the "waiting for review" stage both depend on it.
func TestWorkerRunProducesArtifactAndReport(t *testing.T) {
	srv, err := mockllm.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	srv.ToolName = "bash"
	srv.ToolArgs = `{"command":"echo hi > hello.txt"}`

	eng := engine.New(engine.RuleAssessor{})
	eng.Register(domain.Workspace{
		ID: "ws_web", Name: "web", Root: t.TempDir(), Kind: domain.KindRepo,
		Brief: domain.Brief{Purpose: "webapp", Keywords: []string{"webapp"}},
	}, engine.Policy{Version: "v1"})

	rt := New(eng, view.NewSnapshot())
	rt.EnableWorker(WorkerConfig{
		BaseURL: srv.URL, APIKey: "sk-mock", Model: "mock-tool", Root: t.TempDir(),
	})
	settleRuntime(t, rt)
	if err := rt.Dispatch("在 webapp 里跑个命令"); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		vm := rt.stream()
		if hasKind(vm, view.LineArtifact) && hasText(vm, "执行完成") {
			// The ticket has to be finished, not still working: Report(ready) ends
			// it when no follow is open (003:94).
			for _, t2 := range allSnapshots(eng) {
				if domain.StageOf(&t2, time.Now()) == domain.StageEnded {
					return
				}
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	vm := rt.stream()
	var got []string
	for _, th := range vm.Threads {
		for _, s := range th.Steps {
			got = append(got, string(s.Kind)+":"+s.Text)
		}
	}
	t.Fatalf("no artifact/report after the run; stream=%v", got)
}

// With subagents attached the normal run must still work: the spawn tool is an
// extra option in the Worker's toolbox, not a change to how it works.
func TestWorkerRunsWithSubagentsAttached(t *testing.T) {
	srv, err := mockllm.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	srv.ToolName = "bash"
	srv.ToolArgs = `{"command":"echo hi"}`

	eng := engine.New(engine.RuleAssessor{})
	eng.Register(domain.Workspace{
		ID: "ws_web", Name: "web", Root: t.TempDir(), Kind: domain.KindRepo,
		Brief: domain.Brief{Purpose: "webapp", Keywords: []string{"webapp"}},
	}, engine.Policy{Version: "v1"})

	rt := New(eng, view.NewSnapshot())
	rt.EnableWorker(WorkerConfig{
		BaseURL: srv.URL, APIKey: "sk-mock", Model: "mock-tool", Root: t.TempDir(),
		Subagents: DefaultSubagents(),
	})
	settleRuntime(t, rt)
	if err := rt.Dispatch("在 webapp 里跑个命令"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if hasKind(rt.stream(), view.LineTool) {
			// Let the worker finish before the temp directory disappears under it.
			rt.WaitIdle(20 * time.Second)
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("the run with subagents attached never produced a tool step")
}

// A skill that matches the task must reach the Worker's system prompt; one that
// does not must stay out, or the prompt grows without bound (design §5.10).
func TestMatchedSkillIsInjectedIntoThePrompt(t *testing.T) {
	srv, err := mockllm.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	srv.ToolName = "bash"
	srv.ToolArgs = `{"command":"echo hi"}`

	// The library lives in the test's own directory: a test that depends on state
	// in /tmp passes or fails for the wrong reason.
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "go-tests")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(
		"---\nname: go-tests\ndescription: Go 测试检查清单\n"+
			"when-to-use: 任务里出现测试、test、断言\nwhen-not-to-use: 纯文档改动\n---\n"+
			"写测试时：每个新函数至少一个用例；断言失败信息要能定位。\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	set, err := skills.Load(dir, skills.Options{})
	if err != nil {
		t.Fatalf("load skills: %v", err)
	}

	eng := engine.New(engine.RuleAssessor{})
	eng.Register(domain.Workspace{
		ID: "ws_web", Name: "web", Root: t.TempDir(), Kind: domain.KindRepo,
		Brief: domain.Brief{Purpose: "webapp", Keywords: []string{"webapp"}},
	}, engine.Policy{Version: "v1"})

	rt := New(eng, view.NewSnapshot())
	rt.EnableWorker(WorkerConfig{
		BaseURL: srv.URL, APIKey: "sk-mock", Model: "mock-tool", Root: t.TempDir(),
		Skills: set,
	})
	settleRuntime(t, rt)
	if err := rt.Dispatch("在 webapp 里补一个测试"); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		req := srv.LastRequest()
		if len(req.Messages) > 0 && req.Messages[0].Role == "system" {
			if strings.Contains(req.Messages[0].Content, "go-tests") {
				rt.WaitIdle(20 * time.Second)
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("the matched skill never reached the system prompt")
}

// The Worker must be told which workspace it is already inside. A real model read
// "在 webapp 工作区里…" and created a directory named webapp inside its own
// workspace, because nothing said the cwd already was that workspace.
func TestWorkerPromptNamesTheWorkspace(t *testing.T) {
	srv, err := mockllm.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	srv.ToolName = "bash"
	srv.ToolArgs = `{"command":"echo hi"}`

	eng := engine.New(engine.RuleAssessor{})
	eng.Register(domain.Workspace{
		ID: "ws_web", Name: "webapp", Root: t.TempDir(), Kind: domain.KindRepo,
		Brief: domain.Brief{Purpose: "面向客户的 web 控制台", Keywords: []string{"webapp"}},
	}, engine.Policy{Version: "v1"})
	rt := New(eng, view.NewSnapshot())
	rt.EnableWorker(WorkerConfig{
		BaseURL: srv.URL, APIKey: "sk-mock", Model: "mock-tool", Root: t.TempDir(),
	})
	settleRuntime(t, rt)
	if err := rt.Dispatch("在 webapp 工作区里跑个命令"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		req := srv.LastRequest()
		if len(req.Messages) > 0 && req.Messages[0].Role == "system" {
			sys := req.Messages[0].Content
			if strings.Contains(sys, "当前工作区：webapp") &&
				strings.Contains(sys, "工作目录就是它的根目录") {
				rt.WaitIdle(20 * time.Second)
				return
			}
			t.Fatalf("the prompt does not name the workspace: %q", sys)
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("the worker never ran")
}

// The Worker must receive the request as written. Using the Ticket's display title
// as the task cut everything after the first dot: a real model was asked to create
// "hello.txt" and only saw "…创建文件 hello" (found by running against a live model).
func TestWorkerGetsTheFullRequestNotTheTitle(t *testing.T) {
	srv, err := mockllm.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	srv.ToolName = "bash"
	srv.ToolArgs = `{"command":"echo hi"}`

	eng := engine.New(engine.RuleAssessor{})
	eng.Register(domain.Workspace{
		ID: "ws_web", Name: "webapp", Root: t.TempDir(), Kind: domain.KindRepo,
		Brief: domain.Brief{Purpose: "webapp", Keywords: []string{"webapp"}},
	}, engine.Policy{Version: "v1"})
	rt := New(eng, view.NewSnapshot())
	rt.EnableWorker(WorkerConfig{
		BaseURL: srv.URL, APIKey: "sk-mock", Model: "mock-tool", Root: t.TempDir(),
	})
	settleRuntime(t, rt)
	// A dot inside a filename must not truncate anything.
	request := "在 webapp 工作区根目录创建文件 hello.txt，内容为 zeokeel-live-test"
	if err := rt.Dispatch(request); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		req := srv.LastRequest()
		for _, m := range req.Messages {
			if m.Role != "user" {
				continue
			}
			if !strings.Contains(m.Content, "hello.txt") ||
				!strings.Contains(m.Content, "zeokeel-live-test") {
				t.Fatalf("the worker got a truncated task: %q", m.Content)
			}
			rt.WaitIdle(20 * time.Second)
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("the worker never ran")
}

// An edited prompt keeps its placeholders: the panel shows a template, and the
// runtime fills in the workspace it is running for.
func TestEditedPromptStillExpandsWorkspacePlaceholders(t *testing.T) {
	srv, err := mockllm.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	srv.ToolName = "bash"
	srv.ToolArgs = `{"command":"echo hi"}`

	eng := engine.New(engine.RuleAssessor{})
	eng.Register(domain.Workspace{
		ID: "ws_web", Name: "webapp", Root: t.TempDir(), Kind: domain.KindRepo,
		Brief: domain.Brief{Purpose: "面向客户的 web 控制台", Keywords: []string{"webapp"}},
	}, engine.Policy{Version: "v1"})
	rt := New(eng, view.NewSnapshot())
	rt.EnableWorker(WorkerConfig{
		BaseURL: srv.URL, APIKey: "sk-mock", Model: "mock-tool", Root: t.TempDir(),
	})
	settleRuntime(t, rt)
	if _, err := rt.SaveSettings("prompt", map[string]string{
		"worker": "你是 {{workspace}} 的执行器（{{purpose}}）。CUSTOM-EDIT-7",
	}); err != nil {
		t.Fatal(err)
	}
	if err := rt.Dispatch("在 webapp 里跑个命令"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		req := srv.LastRequest()
		if len(req.Messages) > 0 && req.Messages[0].Role == "system" {
			sys := req.Messages[0].Content
			rt.WaitIdle(20 * time.Second)
			if !strings.Contains(sys, "CUSTOM-EDIT-7") {
				t.Fatalf("the edit did not reach the model: %q", sys)
			}
			if strings.Contains(sys, "{{") {
				t.Fatalf("placeholders were not expanded: %q", sys)
			}
			if !strings.Contains(sys, "webapp") || !strings.Contains(sys, "面向客户的 web 控制台") {
				t.Fatalf("workspace variables missing: %q", sys)
			}
			rt.WaitIdle(20 * time.Second)
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("the worker never ran")
}

// Tool arguments arrive as JSON; Chinese text in them is \u-escaped on the wire. The
// panel must show the sentence, not the escapes (seen in a live screenshot).
func TestJSONFieldDecodesEscapes(t *testing.T) {
	args := `{"kind":"report","title":"报告 ready","summary":"\u5728 webapp \u91cc\u521b\u5efa\u4e86 a.txt","n":3,"ok":true}`
	if got := jsonField(args, "summary"); got != "在 webapp 里创建了 a.txt" {
		t.Fatalf("summary = %q", got)
	}
	if got := jsonField(args, "title"); got != "报告 ready" {
		t.Fatalf("title = %q", got)
	}
	if got := jsonField(args, "n"); got != "3" {
		t.Fatalf("number = %q", got)
	}
	if got := jsonField(args, "ok"); got != "true" {
		t.Fatalf("bool = %q", got)
	}
	if got := jsonField("not json", "summary"); got != "" {
		t.Fatalf("invalid JSON should yield empty, got %q", got)
	}
}
