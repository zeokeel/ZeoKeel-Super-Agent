package runtime

// UI/UX backend tests: model settings + probe, composer target, ticket actions,
// execution-path truth, attention inbox/notification, onboarding skip/demo.

import (
	"context"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zeokeel/super-agent/internal/domain"
	"github.com/zeokeel/super-agent/internal/engine"
)

func TestModelSettingsApplyLive(t *testing.T) {
	rt, _, _ := newRound3(t)
	rt.EnableWorker(WorkerConfig{Root: t.TempDir()})
	if _, err := rt.SaveSettings("worker", map[string]string{
		"base_url": "https://api.test/v1", "api_key": "sk-x", "model": "m1",
	}); err != nil {
		t.Fatal(err)
	}
	cfg := rt.workerConfig()
	if cfg.BaseURL != "https://api.test/v1" || cfg.APIKey != "sk-x" || cfg.Model != "m1" {
		t.Fatalf("worker config not applied: %+v", cfg)
	}
}

func TestCheckModelClassifiesAuthErrors(t *testing.T) {
	old := httpDoModel
	defer func() { httpDoModel = old }()
	rt, _, _ := newRound3(t)
	rt.EnableWorker(WorkerConfig{Root: t.TempDir()})
	if _, err := rt.SaveSettings("worker", map[string]string{
		"base_url": "https://api.test/v1", "api_key": "bad", "model": "m1",
	}); err != nil {
		t.Fatal(err)
	}
	httpDoModel = func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 401, Body: io.NopCloser(strings.NewReader(`{"error":"bad key"}`))}, nil
	}
	if ok, detail := rt.CheckModel(context.Background()); ok || !strings.Contains(detail, "鉴权失败") {
		t.Fatalf("401 classified as %v/%q", ok, detail)
	}
	httpDoModel = func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"choices":[]}`))}, nil
	}
	if ok, detail := rt.CheckModel(context.Background()); !ok {
		t.Fatalf("200 classified as %v/%q", ok, detail)
	}
	httpDoModel = func(*http.Request) (*http.Response, error) { return nil, errors.New("network down") }
	if ok, detail := rt.CheckModel(context.Background()); ok || !strings.Contains(detail, "连接失败") {
		t.Fatalf("network classified as %v/%q", ok, detail)
	}
}

func TestParkedTicketPointsAtModelSettings(t *testing.T) {
	rt, eng, _ := newRound3(t)
	rt.EnableWorker(WorkerConfig{Root: t.TempDir()}) // no model
	if err := rt.Dispatch("在 webapp 里跑个命令"); err != nil {
		t.Fatal(err)
	}
	id := soleTicketID(t, eng)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if tk, ok := eng.TicketSnapshot(id); ok {
			if rep := tk.LastReport(); rep != nil {
				if !strings.Contains(rep.Summary, "设置 › 模型") {
					t.Fatalf("parked message = %q", rep.Summary)
				}
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the parked ticket never reported")
}

func TestDispatchToTargetsWorkspace(t *testing.T) {
	rt, eng, _ := newRound3(t)
	if err := rt.DispatchTo("ws_web", "在 webapp 里跑个命令"); err != nil {
		t.Fatal(err)
	}
	all := eng.AllTickets()
	if len(all) != 1 || all[0].Workspace != "ws_web" {
		t.Fatalf("explicit dispatch = %+v", all)
	}
	if err := rt.DispatchTo("ws_missing", "x"); err == nil || !strings.Contains(err.Error(), "不存在") {
		t.Fatalf("unknown target err = %v", err)
	}
	// Empty target stays the super-agent chat path.
	if err := rt.DispatchTo("", "你好"); err != nil {
		t.Fatalf("chat path err = %v", err)
	}
}

func TestTicketActionsExposeStopRetryAnswer(t *testing.T) {
	rt, eng, _ := newRound3(t)
	rt.EnableWorker(WorkerConfig{Root: t.TempDir()})
	if err := rt.Dispatch("在 webapp 里跑个命令"); err != nil {
		t.Fatal(err)
	}
	id := soleTicketID(t, eng)
	// Running: stop must be offered.
	if err := eng.StartAssignment(id); err != nil {
		t.Fatal(err)
	}
	foundStop := false
	for _, a := range rt.TicketActions() {
		if a.ID == string(id) && a.CanStop {
			foundStop = true
			if !containsAll(strings.Join(a.Actions, ","), "stop") {
				t.Fatalf("actions = %+v", a)
			}
		}
	}
	if !foundStop {
		t.Fatalf("a running ticket has no stop action: %+v", rt.TicketActions())
	}
	// Failed: retry.
	if err := eng.Report(id, domain.ReportFailed, "模型失败"); err != nil {
		t.Fatal(err)
	}
	foundRetry := false
	for _, a := range rt.TicketActions() {
		if a.ID == string(id) && a.CanRetry {
			foundRetry = true
		}
	}
	if !foundRetry {
		t.Fatalf("a failed ticket has no retry action: %+v", rt.TicketActions())
	}
}

func TestExecutionPathsTellTheTruth(t *testing.T) {
	rt, eng, _ := newRound3(t)
	root := t.TempDir()
	rt.EnableWorker(WorkerConfig{Root: root})
	if err := rt.Dispatch("在 webapp 里跑个命令"); err != nil {
		t.Fatal(err)
	}
	id := soleTicketID(t, eng)
	paths, err := rt.ExecutionPaths(string(id))
	if err != nil {
		t.Fatal(err)
	}
	if paths.WorkspaceRoot == "" || !strings.Contains(paths.ExecutionDir, "ticket-1") {
		t.Fatalf("paths = %+v", paths)
	}
	prompt := rt.systemPromptFor(engineTicket(t, eng, id), &WorkerConfig{Root: root})
	if !strings.Contains(prompt, "项目根目录") || !strings.Contains(prompt, "执行副本") ||
		!strings.Contains(prompt, "不要说") {
		t.Fatalf("prompt does not state the copy truth:\n%s", prompt)
	}
}

func engineTicket(t *testing.T, eng *engine.Engine, id domain.ID) domain.Ticket {
	t.Helper()
	tk, ok := eng.TicketSnapshot(id)
	if !ok {
		t.Fatal("no ticket")
	}
	return tk
}

func TestAttentionInboxUnifiesAndNotifiesOnce(t *testing.T) {
	rt, eng, _ := newRound3(t)
	var notified []AttentionItem
	rt.SetAttentionNotify(func(item AttentionItem) { notified = append(notified, item) })

	// One IM proposal.
	if _, err := rt.ReceiveTrigger(pendingIMTrigger("dingtalk", "colleague", "dm_1", "m1",
		"把 webapp 的登录超时改一下")); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && len(rt.attentionItems(nil)) == 0 {
		time.Sleep(10 * time.Millisecond)
	}
	rt.Publish()
	rt.Publish() // a second publish must not repeat the notification
	items := rt.AttentionItems()
	if len(items) != 1 || items[0].Kind != "proposal" || items[0].Source != "dingtalk" ||
		items[0].Sender != "colleague" || items[0].Text == "" {
		t.Fatalf("attention items = %+v", items)
	}
	if len(notified) != 1 {
		t.Fatalf("notifications = %d, want 1", len(notified))
	}
	if rt.AttentionCount() != 1 {
		t.Fatalf("canonical count = %d", rt.AttentionCount())
	}
	att := rt.snap.Attention()
	if att == nil || len(att.Items) != rt.AttentionCount() {
		t.Fatalf("published attention = %+v, count %d", att, rt.AttentionCount())
	}
	if att.Items[0].Kind != "proposal" || att.Items[0].DecisionID == "" {
		t.Fatalf("attention item = %+v", att.Items[0])
	}
	// One canonical number on every surface: AttentionCount == AttentionVM.Items
	// == OverviewVM.PersonCount (nih-ui nav badge/inbox/overview).
	if got := rt.snap.Overview().PersonCount(); got != rt.AttentionCount() || got != att.Count() {
		t.Fatalf("counts disagree: overview=%d attention=%d count=%d",
			got, rt.AttentionCount(), att.Count())
	}
	_ = eng
}

func TestSkipOnboardingEnablesDemoMode(t *testing.T) {
	rt, _, _ := newRound3(t)
	if _, err := rt.SkipOnboarding(); err != nil {
		t.Fatal(err)
	}
	if !rt.DemoMode() {
		t.Fatal("skip did not set demo mode")
	}
	if rt.NeedsOnboarding() {
		t.Fatal("skip did not finish onboarding")
	}
	if err := rt.FinishOnboarding(); err != nil {
		t.Fatalf("demo finish: %v", err)
	}
}

// Observation-only fallback used by tests above that need a plain snapshot.
func (r *Runtime) AttentionItems() []AttentionItem { return r.attentionItems(nil) }

// Deliverable paths are absolute paths in the execution copy (P0-3/P0-C): the
// panel must be able to open/copy them without guessing the workdir.
func TestArtifactPathsAreAbsoluteInExecutionCopy(t *testing.T) {
	rt, eng, _ := newRound3(t)
	root := t.TempDir()
	rt.EnableWorker(WorkerConfig{Root: root})
	if err := rt.Dispatch("在 webapp 里跑个命令"); err != nil {
		t.Fatal(err)
	}
	id := soleTicketID(t, eng)
	if _, err := eng.Present(id, domain.Artifact{Kind: domain.ArtifactFile, Title: "out.txt", Path: "out.txt"}); err != nil {
		t.Fatal(err)
	}
	rt.Publish()
	vm := rt.artifactsVM(rt.clock())
	if len(vm.Rows) != 1 {
		t.Fatalf("rows = %+v", vm.Rows)
	}
	want := filepath.Join(root, "ticket-1", "out.txt")
	if vm.Rows[0].Path != want {
		t.Fatalf("artifact path = %q, want %q", vm.Rows[0].Path, want)
	}
}
