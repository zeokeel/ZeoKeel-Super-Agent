package runtime

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/zeokeel/super-agent/internal/agent/mockllm"
	"github.com/zeokeel/super-agent/internal/domain"
	"github.com/zeokeel/super-agent/internal/effects"
	"github.com/zeokeel/super-agent/internal/engine"
	"github.com/zeokeel/super-agent/internal/outbox"
	"github.com/zeokeel/super-agent/internal/store"
	"github.com/zeokeel/super-agent/internal/view"
)

// The durable path goes through the effects queue instead of a goroutine. The
// panel must see the same transcript either way — a tool card, not just "done"
// (design §7.2 + §6).
func TestDurableLaunchProducesToolSteps(t *testing.T) {
	srv, err := mockllm.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	srv.ToolName = "bash"
	srv.ToolArgs = `{"command":"echo hi"}`

	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "zeokeel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	q, err := effects.New(st)
	if err != nil {
		t.Fatal(err)
	}
	box, err := outbox.Open(st, q, noopSender{})
	if err != nil {
		t.Fatal(err)
	}
	eng := engine.New(engine.RuleAssessor{})
	pers, err := engine.NewPersistence(eng, st, q, box)
	if err != nil {
		t.Fatal(err)
	}
	if err := pers.Register(domain.Workspace{
		ID: "ws_web", Name: "web", Root: t.TempDir(), Kind: domain.KindRepo,
		Brief: domain.Brief{Purpose: "webapp", Keywords: []string{"webapp"}},
	}, engine.Policy{Version: "v1"}); err != nil {
		t.Fatal(err)
	}

	rt := New(eng, view.NewSnapshot())
	rt.EnableWorker(WorkerConfig{
		BaseURL: srv.URL, APIKey: "sk-mock", Model: "mock-tool", Root: t.TempDir(),
	})
	settleRuntime(t, rt)
	if err := rt.EnableDurable(pers, q); err != nil {
		t.Fatal(err)
	}
	q.Start()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = q.Drain(ctx)
	}()

	if err := rt.Dispatch("在 webapp 里跑个命令"); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		vm := rt.stream()
		if hasKind(vm, view.LineTool) && hasText(vm, "执行完成") {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	vm := rt.stream()
	var got []string
	for _, th := range vm.Threads {
		for _, s := range th.Steps {
			got = append(got, string(s.Kind)+":"+s.Text+s.Tool)
		}
	}
	t.Fatalf("durable launch never produced a tool step; got %v", got)
}

func contextWithTimeout(d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), d)
}

type noopSender struct{}

func (noopSender) Send(context.Context, outbox.Message) (outbox.SendOutcome, error) {
	return outbox.OutcomeSent, nil
}

// Two tickets at once is the normal state of a busy panel: the panel's own
// dispatch plus a timer or a chat request. The single-writer engine and the
// per-ticket worktrees must keep them apart.
func TestTwoTicketsRunConcurrently(t *testing.T) {
	srv, err := mockllm.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	srv.ToolName = "bash"
	srv.ToolArgs = `{"command":"echo hi > out.txt"}`

	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "zeokeel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	q, err := effects.New(st)
	if err != nil {
		t.Fatal(err)
	}
	box, err := outbox.Open(st, q, noopSender{})
	if err != nil {
		t.Fatal(err)
	}
	eng := engine.New(engine.RuleAssessor{})
	pers, err := engine.NewPersistence(eng, st, q, box)
	if err != nil {
		t.Fatal(err)
	}
	if err := pers.Register(domain.Workspace{
		ID: "ws_web", Name: "web", Root: t.TempDir(), Kind: domain.KindRepo,
		Brief: domain.Brief{Purpose: "webapp", Keywords: []string{"webapp"}},
	}, engine.Policy{Version: "v1"}); err != nil {
		t.Fatal(err)
	}

	rt := New(eng, view.NewSnapshot())
	rt.EnableWorker(WorkerConfig{
		BaseURL: srv.URL, APIKey: "sk-mock", Model: "mock-tool", Root: filepath.Join(dir, "work"),
	})
	settleRuntime(t, rt)
	if err := rt.EnableDurable(pers, q); err != nil {
		t.Fatal(err)
	}
	q.Start()
	defer func() {
		ctx, cancel := contextWithTimeout(2 * time.Second)
		defer cancel()
		_ = q.Drain(ctx)
	}()

	for _, text := range []string{"在 webapp 里做 A", "在 webapp 里做 B"} {
		if err := rt.Dispatch(text); err != nil {
			t.Fatal(err)
		}
	}
	if n := len(eng.AllTickets()); n != 2 {
		t.Fatalf("expected two tickets, got %d", n)
	}

	deadline := time.Now().Add(25 * time.Second)
	for time.Now().Before(deadline) {
		done := 0
		for _, tk := range allSnapshots(eng) {
			if len(tk.Artifacts) > 0 {
				done++
			}
		}
		if done == 2 {
			rt.WaitIdle(20 * time.Second)
			// Each ticket has its own worktree, so the files cannot collide.
			dirs, _ := filepath.Glob(filepath.Join(dir, "work", "ticket-*", "out.txt"))
			if len(dirs) != 2 {
				t.Fatalf("expected two worktrees, got %v", dirs)
			}
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("both tickets did not finish")
}
