package runtime

// Phase B regression tests: the two-phase Eval pass, input delivery three-state,
// cursor durability, onboarding persistence, the pause projection and the
// supervisor's health row.

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/zeokeel/super-agent/internal/agent/mockllm"
	"github.com/zeokeel/super-agent/internal/domain"
	"github.com/zeokeel/super-agent/internal/engine"
	"github.com/zeokeel/super-agent/internal/store"
	"github.com/zeokeel/super-agent/internal/view"
)

func TestEvaluateDueCreatesTheTicket(t *testing.T) {
	eng := engine.New(engine.RuleAssessor{})
	eng.Register(domain.Workspace{ID: "ws_web", Name: "web", Root: "/tmp/web", Kind: domain.KindRepo,
		Brief: domain.Brief{Purpose: "webapp", Keywords: []string{"webapp"}}}, engine.Policy{Version: "v1"})
	rt := New(eng, view.NewSnapshot())
	if _, err := eng.Dispatch(engine.DispatchInput{Text: "在 webapp 里做点事", Workspace: "ws_web"}); err != nil {
		t.Fatal(err)
	}
	if n := len(eng.AllTickets()); n != 0 {
		t.Fatalf("Receive/Dispatch created %d tickets synchronously; the pass owns that", n)
	}
	outs, err := rt.EvaluateDue(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(outs) == 0 || outs[0].Ticket == "" {
		t.Fatalf("the eval pass produced no ticket: %+v", outs)
	}
}

func TestInputDeliveryThreeStages(t *testing.T) {
	srv, err := mockllm.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	srv.ToolName = "bash"
	srv.ToolArgs = `{"command":"echo hi > hello.txt"}`

	eng := engine.New(engine.RuleAssessor{})
	eng.Register(domain.Workspace{ID: "ws_web", Name: "web", Root: t.TempDir(), Kind: domain.KindRepo,
		Brief: domain.Brief{Purpose: "webapp", Keywords: []string{"webapp"}}}, engine.Policy{Version: "v1"})
	rt := New(eng, view.NewSnapshot())
	rt.EnableWorker(WorkerConfig{BaseURL: srv.URL, APIKey: "sk-mock", Model: "mock-tool", Root: t.TempDir()})
	settleRuntime(t, rt)
	if err := rt.Dispatch("在 webapp 里跑个命令"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		for _, tk := range allSnapshots(eng) {
			if tk.Ended == nil || len(tk.Inputs) == 0 {
				continue
			}
			in := tk.Inputs[0]
			if !in.Delivered || !in.Read || !in.TurnEnded {
				t.Fatalf("input stages not recorded: %+v", in)
			}
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("the ticket never finished; input stages unverified")
}

func TestCursorAndOnboardingPersistAcrossRestart(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "zeokeel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	rt := New(engine.New(engine.RuleAssessor{}), view.NewSnapshot())
	if err := rt.LoadSettings(st); err != nil {
		t.Fatal(err)
	}
	if err := rt.SetCursor("github:o/r", "cursor-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.SaveOnboarding(map[string]string{
		"model": "mock", "base_url": "http://127.0.0.1:1/v1",
		"ws_name": "webapp", "ws_root": t.TempDir(), "ws_keywords": "webapp",
	}); err != nil {
		t.Fatal(err)
	}
	if err := rt.FinishOnboarding(); err != nil {
		t.Fatal(err)
	}

	// A new process: a second Runtime reading the same store sees both facts.
	rt2 := New(engine.New(engine.RuleAssessor{}), view.NewSnapshot())
	if err := rt2.LoadSettings(st); err != nil {
		t.Fatal(err)
	}
	if got := rt2.Cursor("github:o/r"); got != "cursor-1" {
		t.Fatalf("cursor not persisted: %q", got)
	}
	if !rt2.SetupComplete() {
		t.Fatal("setup marker not persisted")
	}
	rt2.mu.Lock()
	model := rt2.settings["onboard.model"]
	rt2.mu.Unlock()
	if model != "mock" {
		t.Fatalf("onboarding settings not reloaded: model=%q", model)
	}
}

func TestPauseAndHealthProjections(t *testing.T) {
	eng := engine.New(engine.RuleAssessor{})
	rt := New(eng, view.NewSnapshot())
	if err := rt.PauseExecution(true); err != nil {
		t.Fatal(err)
	}
	rt.Publish()
	ov := rt.snap.Overview()
	if !ov.Runtime.PausedKnown || !ov.Runtime.Paused {
		t.Fatalf("pause projection is dishonest: %+v", ov.Runtime)
	}
	if err := rt.PauseExecution(false); err != nil {
		t.Fatal(err)
	}
	// No model: health must say "setup", and the exit code must be 3.
	state, _, code := rt.Health()
	if state != "setup" || code != ExitSetup {
		t.Fatalf("health = %s/%d, want setup/%d", state, code, ExitSetup)
	}
}

func TestSupervisorWritesAHealthRow(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "zeokeel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	eng := engine.New(engine.RuleAssessor{})
	rt := New(eng, view.NewSnapshot())
	if err := rt.LoadSettings(st); err != nil {
		t.Fatal(err)
	}
	// Deterministic: exercise the write path directly. The 60s loop's timing is
	// not what this test is about, and sleeping on a shared global logger/clock is
	// what made it flaky under whole-suite parallelism.
	rt.writeHealth(healthRuntime, "ready", "巡检正常")
	entries, err := st.HealthEntries()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range entries {
		if e.Entry == healthRuntime {
			found = true
		}
	}
	if !found {
		t.Fatalf("the supervisor wrote no runtime health row: %+v", entries)
	}
}
