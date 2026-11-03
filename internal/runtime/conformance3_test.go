package runtime

// 003 conformance tests for this pass: PR lifecycle (follow + close ends by
// outcome), the prepare command and its environment, and the 30-minute background
// rule.

import (
	"context"
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

func newWSRuntime(t *testing.T) (*Runtime, *engine.Engine, domain.Workspace) {
	t.Helper()
	eng := engine.New(engine.RuleAssessor{})
	ws := domain.Workspace{ID: "ws_web", Name: "web", Root: t.TempDir(), Kind: domain.KindRepo,
		Brief: domain.Brief{Purpose: "webapp", Keywords: []string{"webapp"}}}
	eng.Register(ws, engine.Policy{Version: "v1"})
	return New(eng, view.NewSnapshot()), eng, ws
}

// soleTicketID waits until exactly one Ticket exists (Dispatch is synchronous
// enough for this, but the first projection can lag the launch).
func soleTicketID(t *testing.T, eng *engine.Engine) domain.ID {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if all := eng.AllTickets(); len(all) == 1 {
			return all[0].ID
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("expected one ticket, got %d", len(eng.AllTickets()))
	return ""
}

func newTicket(t *testing.T, rt *Runtime, eng *engine.Engine) domain.Ticket {
	t.Helper()
	if err := rt.Dispatch("在 webapp 里做点事"); err != nil {
		t.Fatal(err)
	}
	all := eng.AllTickets()
	if len(all) != 1 {
		t.Fatalf("tickets = %d", len(all))
	}
	tk, ok := eng.TicketSnapshot(all[0].ID)
	if !ok {
		t.Fatal("no ticket")
	}
	return tk
}

// A ready Ticket that follows an open PR must not end; the PR close ends it with
// the PR's outcome, and a second close is harmless.
func TestFollowThenCloseEndsTicketByOutcome(t *testing.T) {
	rt, eng, _ := newWSRuntime(t)
	tk := newTicket(t, rt, eng)

	if err := rt.BindThread(tk.ID, "acme/webapp#12", true); err != nil {
		t.Fatal(err)
	}
	// A worked Ticket has consumed its originating input; otherwise Unfinished()
	// (correctly) keeps it open even after the PR closes.
	for _, mark := range []func(domain.ID) error{rt.markInputsDelivered, rt.markInputsRead, rt.markTurnEnded} {
		if err := mark(tk.ID); err != nil {
			t.Fatal(err)
		}
	}
	if err := rt.reportTicket(tk.ID, domain.ReportReady, "PR 开好了"); err != nil {
		t.Fatal(err)
	}
	ready, _ := eng.TicketSnapshot(tk.ID)
	if ready.Ended != nil {
		t.Fatalf("a ticket following an open PR ended early: %+v", ready.Ended)
	}
	if got := domain.StageOf(&ready, time.Now()); got != domain.StagePersonReview {
		t.Fatalf("stage = %q, want person/review (waiting for the PR)", got)
	}

	closeTr := domain.Trigger{
		ID: "trig-pr-close", Source: "github", Scope: domain.Global,
		Threads: []domain.ThreadRef{{Source: "github", ID: "acme/webapp#12"}},
		Channel: domain.ChannelRef{Source: "github", Address: map[string]string{
			"kind": "pull", "state": "pull request merged", "repository": "acme/webapp", "number": "12",
		}},
		At: time.Now(),
	}
	outcome, err := rt.CloseThread(closeTr)
	if err != nil || outcome != domain.OutcomeMerged {
		t.Fatalf("CloseThread = %q, %v", outcome, err)
	}
	ended, _ := eng.TicketSnapshot(tk.ID)
	if ended.Ended == nil || ended.Ended.Outcome != domain.OutcomeMerged {
		t.Fatalf("ticket did not end by merged: %+v", ended.Ended)
	}
	// Idempotent: closing again must not change the outcome or double-end.
	if _, err := rt.CloseThread(closeTr); err != nil {
		t.Fatal(err)
	}
	again, _ := eng.TicketSnapshot(tk.ID)
	if again.Ended == nil || again.Ended.Outcome != domain.OutcomeMerged {
		t.Fatalf("second close changed the outcome: %+v", again.Ended)
	}

	// A non-closing PR trigger is not a thread close.
	if outcome, _ := rt.CloseThread(domain.Trigger{Source: "github",
		Channel: domain.ChannelRef{Address: map[string]string{"state": "pull request open"}}}); outcome != "" {
		t.Fatalf("an open PR closed its thread: %q", outcome)
	}
}

func TestPrepareRunsWithVariablesAndFailsCleanly(t *testing.T) {
	rt, _, ws := newWSRuntime(t)
	dir := t.TempDir()
	cfg := &WorkerConfig{Root: dir}
	tk := domain.Ticket{ID: "t-1", Number: 1, Workspace: ws.ID}
	workdir := filepath.Join(dir, "ticket-1")

	if _, err := rt.SaveSettings("execution", map[string]string{
		"prepare": `mkdir -p ticket-1 && echo "$ZEOKEEL_TICKET|$ZEOKEEL_TICKET_DIR|$ZEOKEEL_REPO" > ticket-1/vars.txt`,
	}); err != nil {
		t.Fatal(err)
	}
	if err := rt.runPrepare(context.Background(), tk, cfg, workdir); err != nil {
		t.Fatalf("prepare failed: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(workdir, "vars.txt"))
	if err != nil {
		t.Fatalf("prepare did not run in the workspace root: %v", err)
	}
	if got := strings.TrimSpace(string(raw)); !strings.HasPrefix(got, "1|") || !strings.Contains(got, "ticket-1") {
		t.Fatalf("prepare env vars = %q", got)
	}
	if !strings.Contains(string(raw), ws.Name) {
		t.Fatalf("ZEOKEEL_REPO missing: %q", raw)
	}

	// Failure: ticket dir must not be created, and the output is in the error.
	failCfg := &WorkerConfig{Root: t.TempDir()}
	failTk := domain.Ticket{ID: "t-2", Number: 2, Workspace: ws.ID}
	if _, err := rt.SaveSettings("execution", map[string]string{"prepare": "echo boom; exit 3"}); err != nil {
		t.Fatal(err)
	}
	err = rt.runPrepare(context.Background(), failTk, failCfg, filepath.Join(failCfg.Root, "ticket-2"))
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("failure output missing: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(failCfg.Root, "ticket-2")); !os.IsNotExist(statErr) {
		t.Fatal("a failed prepare created the ticket directory")
	}

	// Credential-looking variables are scrubbed from the inherited environment.
	scrubbed := scrubEnv([]string{"PATH=/bin", "API_KEY=x", "GH_TOKEN=y", "DB_PASSWORD=z", "HOME=/root"})
	joined := strings.Join(scrubbed, ",")
	if strings.Contains(joined, "API_KEY") || strings.Contains(joined, "GH_TOKEN") || strings.Contains(joined, "DB_PASSWORD") {
		t.Fatalf("secrets leaked into prepare env: %v", scrubbed)
	}
	if !strings.Contains(joined, "PATH=/bin") || !strings.Contains(joined, "HOME=/root") {
		t.Fatalf("safe vars were removed: %v", scrubbed)
	}
}

// A failed prepare fails the Ticket with its output and is not retried.
func TestPrepareFailureFailsTicketOnce(t *testing.T) {
	rt, eng, _ := newWSRuntime(t)
	counter := filepath.Join(t.TempDir(), "runs")
	if _, err := rt.SaveSettings("execution", map[string]string{
		"prepare": "echo ran >> " + counter + "; echo boom; exit 1",
	}); err != nil {
		t.Fatal(err)
	}
	srv, err := mockllm.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	rt.EnableWorker(WorkerConfig{
		BaseURL: srv.URL, APIKey: "sk-mock", Model: "mock-text", Root: t.TempDir(),
	})
	settleRuntime(t, rt)
	if err := rt.Dispatch("在 webapp 里做点事"); err != nil {
		t.Fatal(err)
	}
	id := soleTicketID(t, eng)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		got, _ := eng.TicketSnapshot(id)
		if rep := got.LastReport(); rep != nil {
			if rep.State != domain.ReportFailed || !strings.Contains(rep.Summary, "boom") {
				t.Fatalf("report = %+v", rep)
			}
			raw, _ := os.ReadFile(counter)
			if strings.Count(string(raw), "ran") != 1 {
				t.Fatalf("prepare ran %d times, want exactly 1 (no retry): %q", strings.Count(string(raw), "ran"), raw)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("a failed prepare did not fail the ticket")
}

// A job still running when the turn ends keeps the Ticket waiting; at the limit it
// is killed and the Ticket reports waiting with the job id and last output.
func TestBackgroundJobThirtyMinuteRule(t *testing.T) {
	srv, err := mockllm.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	srv.ToolName = "bash"
	srv.ToolArgs = `{"command":"sleep 5"}`

	rt, eng, _ := newWSRuntime(t)
	rt.EnableWorker(WorkerConfig{
		BaseURL: srv.URL, APIKey: "sk-mock", Model: "mock-tool",
		Root: t.TempDir(), BackgroundAfter: 10 * time.Millisecond,
	})
	settleRuntime(t, rt)
	rt.SetBackgroundLimit(120 * time.Millisecond)
	if err := rt.Dispatch("在 webapp 里跑个长命令"); err != nil {
		t.Fatal(err)
	}
	id := soleTicketID(t, eng)

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		got, _ := eng.TicketSnapshot(id)
		if rep := got.LastReport(); rep != nil && rep.State == domain.ReportWaiting &&
			strings.Contains(rep.Summary, "终止") {
			if got.Ended != nil {
				t.Fatalf("the ticket ended while waiting on a killed job: %+v", got.Ended)
			}
			if !strings.Contains(rep.Summary, "后台作业") {
				t.Fatalf("the waiting report does not name the job: %q", rep.Summary)
			}
			// Killed jobs must not wake the session: the model-call count stops
			// growing after the kill.
			time.Sleep(300 * time.Millisecond)
			if calls := srv.RequestCount(); calls != 2 {
				t.Fatalf("a killed job woke the session: %d model calls (want 2: tool + answer)", calls)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	got, _ := eng.TicketSnapshot(id)
	t.Fatalf("the background rule never fired: %+v", got.LastReport())
}

// A Worker's bash child must see the Ticket facts as environment variables: the
// prompt alone cannot be used in a shell command.
func TestWorkerBashSeesTicketEnv(t *testing.T) {
	srv, err := mockllm.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	srv.ToolName = "bash"
	srv.ToolArgs = `{"command":"printf '%s|%s|%s|%s' \"$ZEOKEEL_TICKET\" \"$ZEOKEEL_TICKET_DIR\" \"$ZEOKEEL_TICKET_NAME\" \"$ZEOKEEL_REPO\" > env.txt"}`

	rt, eng, _ := newWSRuntime(t)
	root := t.TempDir()
	rt.EnableWorker(WorkerConfig{
		BaseURL: srv.URL, APIKey: "sk-mock", Model: "mock-tool", Root: root,
	})
	settleRuntime(t, rt)
	if err := rt.Dispatch("在 webapp 里跑个命令"); err != nil {
		t.Fatal(err)
	}
	id := soleTicketID(t, eng)
	workdir := filepath.Join(root, "ticket-1")

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if raw, err := os.ReadFile(filepath.Join(workdir, "env.txt")); err == nil {
			got := string(raw)
			for _, want := range []string{"1|", workdir, "ticket-1", "web"} {
				if !strings.Contains(got, want) {
					t.Fatalf("bash env missing %q: %q", want, got)
				}
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	got, _ := eng.TicketSnapshot(id)
	t.Fatalf("the worker bash never saw the env vars: %+v", got.LastReport())
}

// The framework tools the panel promises are switched on for every Worker.
func TestWorkerToolSwitches(t *testing.T) {
	rt, _, ws := newWSRuntime(t)
	cfg := &WorkerConfig{Root: t.TempDir()}
	tk := domain.Ticket{ID: "t-1", Number: 1, Workspace: ws.ID}
	run, err := rt.runnerFor(tk, cfg, ticketWorkdir(cfg, tk))
	if err != nil {
		t.Fatal(err)
	}
	got := run.Config()
	if !got.PlanTasks || !got.AgentsMD || !got.WebTools {
		t.Fatalf("tool switches not enabled: plan=%v agentsmd=%v web=%v",
			got.PlanTasks, got.AgentsMD, got.WebTools)
	}
	prompt := rt.systemPromptFor(tk, cfg)
	for _, want := range []string{"TaskCreate", "web_fetch", "AGENTS.md"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("worker prompt does not mention %q", want)
		}
	}
}

// The web_search endpoint is configurable through settings (default: agent's DDG).
func TestWebSearchURLComesFromSettings(t *testing.T) {
	rt, _, ws := newWSRuntime(t)
	if _, err := rt.SaveSettings("execution", map[string]string{
		"web_search_url": "https://search.test/?q=%s",
	}); err != nil {
		t.Fatal(err)
	}
	cfg := &WorkerConfig{Root: t.TempDir()}
	tk := domain.Ticket{ID: "t-1", Number: 1, Workspace: ws.ID}
	run, err := rt.runnerFor(tk, cfg, ticketWorkdir(cfg, tk))
	if err != nil {
		t.Fatal(err)
	}
	if got := run.Config().WebSearchURL; got != "https://search.test/?q=%s" {
		t.Fatalf("WebSearchURL = %q", got)
	}
}
