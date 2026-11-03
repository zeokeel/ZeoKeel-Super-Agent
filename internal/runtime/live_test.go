package runtime

// Live-model acceptance: the only part of this project that a mock cannot prove.
// It is skipped unless the operator points it at a real OpenAI-compatible endpoint,
// and the credentials come from the environment so they never reach the repo:
//
//	ZEOKEEL_LIVE_BASE_URL=http://host:8080/v1 \
//	ZEOKEEL_LIVE_API_KEY=... \
//	ZEOKEEL_LIVE_MODEL=qwen3.8-flash-next \
//	go test ./internal/runtime/ -run Live -v
//
// What it checks is the whole loop with a real model: Eval reads the message and
// picks a workspace, the Worker calls a real tool, a deliverable appears, and the
// answer goes back to the conversation. Nothing is stubbed.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zeokeel/super-agent/internal/assess"
	"github.com/zeokeel/super-agent/internal/domain"
	"github.com/zeokeel/super-agent/internal/engine"
	"github.com/zeokeel/super-agent/internal/view"
)

func liveConfig(t *testing.T) (baseURL, apiKey, model string) {
	t.Helper()
	if os.Getenv("ZEOKEEL_LIVE_BASE_URL") == "" {
		t.Skip("set ZEOKEEL_LIVE_BASE_URL to run against a real model")
	}
	baseURL = os.Getenv("ZEOKEEL_LIVE_BASE_URL")
	apiKey = os.Getenv("ZEOKEEL_LIVE_API_KEY")
	model = os.Getenv("ZEOKEEL_LIVE_MODEL")
	if model == "" {
		model = "qwen3.8-flash-next"
	}
	if apiKey == "" {
		t.Fatal("ZEOKEEL_LIVE_API_KEY is required")
	}
	return baseURL, apiKey, model
}

// A real model must be able to judge a request inside the candidate workspaces.
func TestLiveEvalPicksTheWorkspace(t *testing.T) {
	baseURL, apiKey, model := liveConfig(t)
	gen, err := assess.NewEinoGenerator(t.Context(), assess.EinoConfig{
		BaseURL: baseURL, APIKey: apiKey, Model: model,
	})
	if err != nil {
		t.Fatal(err)
	}
	a, err := assess.New(assess.Config{
		Model: model,
		Rules: []assess.Rule{
			{ID: "implementation", Instruction: "明确要求修改这个仓库的代码或配置", Enabled: true},
			{ID: "data-request", Instruction: "要求从这个工作区取数或出报表", Enabled: true},
			{ID: "off-topic", Instruction: "与这个工作区无关的闲聊、通知或公告", Enabled: true},
		},
	}, gen)
	if err != nil {
		t.Fatal(err)
	}
	eng := engine.New(a)
	eng.Register(domain.Workspace{
		ID: "ws_web", Name: "web", Root: t.TempDir(), Kind: domain.KindRepo,
		Brief: domain.Brief{Purpose: "面向客户的 web 控制台：登录、账单、设置",
			Keywords: []string{"webapp", "登录", "账单"}},
	}, engine.Policy{Version: "v1"})
	eng.Register(domain.Workspace{
		ID: "ws_acme_data", Name: "acme-data", Root: t.TempDir(), Kind: domain.KindData,
		Brief: domain.Brief{Purpose: "客户 acme 的用量数据与计费报表",
			Keywords: []string{"acme", "用量", "计费"}},
	}, engine.Policy{Version: "v1"})

	out, err := eng.Dispatch(engine.DispatchInput{
		Text: "在 webapp 里把登录超时改成可配置",
	})
	if err != nil {
		t.Fatalf("the real model did not produce a usable decision: %v", err)
	}
	if out.Workspace != "ws_web" {
		t.Fatalf("the model routed to %q, expected ws_web", out.Workspace)
	}
	if out.Ticket == "" {
		t.Fatalf("no ticket was created: %+v", out)
	}
}

// The full loop with a real model: worker runs a real shell command, a deliverable
// is collected, the ticket reports ready and the conversation gets an answer.
func TestLiveWorkerDelivers(t *testing.T) {
	baseURL, apiKey, model := liveConfig(t)
	work, mkErr := os.MkdirTemp("", "zeokeel-live-*")
	if mkErr != nil {
		t.Fatal(mkErr)
	}
	t.Logf("workdir: %s", work)
	eng := engine.New(engine.RuleAssessor{})
	eng.Register(domain.Workspace{
		ID: "ws_web", Name: "web", Root: t.TempDir(), Kind: domain.KindRepo,
		Brief: domain.Brief{Purpose: "webapp", Keywords: []string{"webapp"}},
	}, engine.Policy{Version: "v1", Allowed: map[string]map[string]bool{
		"dingtalk": {"colleague": true},
	}})
	rt := New(eng, view.NewSnapshot())
	rt.EnableWorker(WorkerConfig{
		BaseURL: baseURL, APIKey: apiKey, Model: model, Root: work,
		ContextBudget: 200000, ToolOutputLimit: 40000,
	})
	settleRuntime(t, rt)

	tr := domain.Trigger{
		ID:     domain.Key(domain.WorkspaceScope(""), "dingtalk", "live-1"),
		Source: "dingtalk", Human: true,
		Addressed:    domain.Addressed{Direct: true},
		Text:         "在 webapp 工作区根目录创建文件 hello.txt，内容为 zeokeel-live-test，做完用一句话汇报",
		Actor:        domain.Actor{ID: "colleague"},
		Conversation: domain.Conversation{Kind: domain.ConvDirect, ID: "dm-live"},
		Channel:      domain.ChannelRef{Source: "dingtalk", Thread: "dm-live"},
		At:           time.Now(),
	}
	if _, err := eng.Receive(withThread(tr)); err != nil {
		t.Fatal(err)
	}
	var decision domain.ID
	for _, p := range eng.Proposals() {
		decision = p.ID
	}
	approved, err := eng.Approve(decision)
	if err != nil {
		t.Fatal(err)
	}
	// Approval creates the Ticket; something still has to run it. In the app that
	// is the durable launch effect, so the test does what the effect does.
	if tk, ok := eng.TicketSnapshot(approved.Ticket); ok {
		go rt.runTicket(tk)
	}

	// Long tasks are normal here, so the wait is configurable instead of fixed:
	// a real 1M-context model on a slow link can take minutes per turn.
	wait := 20 * time.Minute
	if raw := os.Getenv("ZEOKEEL_LIVE_DEADLINE"); raw != "" {
		if d, err := time.ParseDuration(raw); err == nil {
			wait = d
		}
	}
	deadline := time.Now().Add(wait)
	for time.Now().Before(deadline) {
		if tk, ok := eng.TicketSnapshot(approved.Ticket); ok {
			if tk.LastReport() != nil {
				rt.WaitIdle(30 * time.Second)
				break
			}
		}
		time.Sleep(500 * time.Millisecond)
	}

	tk, ok := eng.TicketSnapshot(approved.Ticket)
	if !ok || tk.LastReport() == nil {
		t.Fatal("the worker never reported")
	}
	if got := tk.LastReport().State; got != domain.ReportReady {
		t.Fatalf("report state %q: %s", got, tk.LastReport().Summary)
	}
	files, _ := filepath.Glob(filepath.Join(work, "ticket-*", "hello.txt"))
	if len(files) == 0 {
		var tree []string
		_ = filepath.Walk(work, func(path string, info os.FileInfo, err error) error {
			if err == nil && !info.IsDir() {
				tree = append(tree, path)
			}
			return nil
		})
		t.Fatalf("the real model never created hello.txt; workdir=%s files=%v\nanswer=%q",
			work, tree, tk.LastReport().Summary)
	}
	content, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "zeokeel-live-test") {
		t.Fatalf("wrong content: %q", content)
	}
	if len(rt.Replies()) == 0 {
		t.Fatal("the requester was not answered")
	}
}

// Does the real provider actually reuse our prefix? This is the only way to know:
// run a multi-turn task and read the cache counters the provider reports. A zero hit
// rate means something in our request changes between turns, and the structural
// test in internal/agent says it should not.
func TestLiveCacheReuse(t *testing.T) {
	baseURL, apiKey, model := liveConfig(t)
	work := t.TempDir()
	eng := engine.New(engine.RuleAssessor{})
	eng.Register(domain.Workspace{
		ID: "ws_web", Name: "webapp", Root: t.TempDir(), Kind: domain.KindRepo,
		Brief: domain.Brief{Purpose: "webapp", Keywords: []string{"webapp"}},
	}, engine.Policy{Version: "v1"})
	rt := New(eng, view.NewSnapshot())
	rt.EnableWorker(WorkerConfig{
		BaseURL: baseURL, APIKey: apiKey, Model: model, Root: work,
		ContextBudget: 200000, ToolOutputLimit: 40000,
	})
	settleRuntime(t, rt)

	// Three forced tool turns: write, read back, list. Each later turn resends the
	// whole prefix, which is exactly what the cache should serve.
	if err := rt.Dispatch("在 webapp 里：1) 创建 a.txt 内容 one 2) 读回来确认 3) 列出目录 4) 一句话汇报"); err != nil {
		t.Fatal(err)
	}
	wait := 20 * time.Minute
	if raw := os.Getenv("ZEOKEEL_LIVE_DEADLINE"); raw != "" {
		if d, err := time.ParseDuration(raw); err == nil {
			wait = d
		}
	}
	deadline := time.Now().Add(wait)
	var finished bool
	for time.Now().Before(deadline) {
		for _, t2 := range allSnapshots(eng) {
			if t2.LastReport() != nil {
				finished = true
			}
		}
		if finished {
			rt.WaitIdle(30 * time.Second)
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if !finished {
		t.Fatal("the live run never finished")
	}
	u := rt.usageSnapshot()
	t.Logf("usage: calls=%d prompt=%d cached=%d (%.0f%%) completion=%d",
		u.Calls, u.Prompt, u.Cached, 100*float64(u.Cached)/float64(max1(u.Prompt)), u.Completion)
	if u.Calls < 2 {
		t.Skip("the run needed only one model call; nothing to compare")
	}
	if u.Cached == 0 {
		t.Fatalf("the provider reported no cache hits over %d calls: our prefix is not being reused", u.Calls)
	}
}

func max1(n int) int {
	if n <= 0 {
		return 1
	}
	return n
}
