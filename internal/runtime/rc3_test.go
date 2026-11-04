package runtime

// DSH round tests: workspace settings/stats, browser wrapper contract, worker
// tool list, skills scoping, and SuperAgent chat requests carrying no tools.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/zeokeel/super-agent/internal/domain"
	"github.com/zeokeel/super-agent/internal/view"
)

type fakeBrowser struct {
	started map[string]int
	stopped map[string]bool
}

func (f *fakeBrowser) Detect() []BrowserInfo {
	return []BrowserInfo{{ID: "chrome", Name: "Chrome", Path: "/usr/bin/chrome"}}
}
func (f *fakeBrowser) Start(_ context.Context, ws, browser string, headless bool, port int) (int, error) {
	if f.started == nil {
		f.started = map[string]int{}
	}
	f.started[ws] = 9222
	return 9222, nil
}
func (f *fakeBrowser) Stop(_ context.Context, ws string) error {
	if f.stopped == nil {
		f.stopped = map[string]bool{}
	}
	f.stopped[ws] = true
	delete(f.started, ws)
	return nil
}
func (f *fakeBrowser) Port(ws string) (int, bool) { p, ok := f.started[ws]; return p, ok }
func (f *fakeBrowser) Status(ws string) (bool, int, string) {
	p, ok := f.started[ws]
	if !ok {
		return false, 0, "未启动"
	}
	return true, p, "运行中"
}
func (f *fakeBrowser) Tools(string) ([]tool.BaseTool, error) { return nil, nil }
func (f *fakeBrowser) ProfileDir(ws string) string           { return "/tmp/profile-" + ws }
func (f *fakeBrowser) Close() error                          { return nil }

func TestWorkspaceSettingsRoundTripAndValidation(t *testing.T) {
	rt, _, _ := newRound3(t)
	if _, err := rt.SaveSettings("source", map[string]string{}); err != nil {
		t.Fatal(err)
	}
	cfg := view.WorkspaceSettings{BrowserEnabled: true, Browser: "edge", Headless: true,
		DisabledSkills: []string{" b ", "a", "a"}}
	if err := rt.SaveWorkspaceSettings("ws_web", cfg); err != nil {
		t.Fatal(err)
	}
	got, ok := rt.WorkspaceSettings("ws_web")
	if !ok {
		t.Fatal("workspace settings not found")
	}
	if !got.BrowserEnabled || got.Browser != "edge" || !got.Headless {
		t.Fatalf("settings = %+v", got)
	}
	if strings.Join(got.DisabledSkills, ",") != "a,b" {
		t.Fatalf("disabled skills = %v", got.DisabledSkills)
	}
	if err := rt.SaveWorkspaceSettings("ws_web", view.WorkspaceSettings{Browser: "safari"}); err == nil {
		t.Fatal("unknown browser accepted")
	}
	if err := rt.SaveWorkspaceSettings("ws_web", view.WorkspaceSettings{Browser: "chrome", Port: 80}); err == nil {
		t.Fatal("invalid port accepted")
	}
	if err := rt.SaveWorkspaceSettings("ws_missing", view.WorkspaceSettings{}); err == nil {
		t.Fatal("unknown workspace accepted")
	}
	if rt.skillAllowed("ws_web", "a") || rt.skillAllowed("ws_web", "b") || !rt.skillAllowed("ws_web", "c") {
		t.Fatalf("skill scoping wrong: %v", got.DisabledSkills)
	}
}

func TestWorkspaceStats(t *testing.T) {
	rt, eng, _ := newRound3(t)
	if err := rt.NewConversation("ws_web", "在 webapp 里跑个命令"); err != nil {
		t.Fatal(err)
	}
	id := soleTicketID(t, eng)
	if err := eng.StartAssignment(id); err != nil {
		t.Fatal(err)
	}
	stats, ok := rt.WorkspaceStats("ws_web")
	if !ok || stats.Conversations != 1 || stats.Running != 1 {
		t.Fatalf("stats = %+v ok=%v", stats, ok)
	}
	if _, ok := rt.WorkspaceStats("ws_other"); ok {
		t.Fatal("other workspace exists")
	}
}

func TestBrowserWrappersDelegateToBackend(t *testing.T) {
	rt, _, _ := newRound3(t)
	fake := &fakeBrowser{}
	rt.SetBrowserBackend(fake)
	if got := rt.DetectedBrowsers(); len(got) != 1 || got[0].ID != "chrome" || !got[0].Available {
		t.Fatalf("detected = %+v", got)
	}
	if err := rt.StartBrowser("ws_web"); err == nil {
		t.Fatal("browser started without being enabled in workspace settings")
	}
	if err := rt.SaveWorkspaceSettings("ws_web", view.WorkspaceSettings{BrowserEnabled: true, Browser: "chrome"}); err != nil {
		t.Fatal(err)
	}
	if err := rt.StartBrowser("ws_web"); err != nil {
		t.Fatal(err)
	}
	running, detail := rt.BrowserStatus("ws_web")
	port, hasPort := rt.BrowserPort("ws_web")
	if !running || !hasPort || port != 9222 || detail == "" {
		t.Fatalf("status = %v %d %q", running, port, detail)
	}
	if err := rt.StopBrowser("ws_web"); err != nil || !fake.stopped["ws_web"] {
		t.Fatalf("stop = %v %+v", err, fake.stopped)
	}
}

func TestWorkerToolListHasDSHExecutorTools(t *testing.T) {
	rt, _, _ := newRound3(t)
	tk := domain.Ticket{ID: "t1", Number: 1, Workspace: "ws_web"}
	ctx := context.Background()
	names := map[string]bool{}
	for _, bt := range rt.workerTools(tk) {
		if info, err := bt.Info(ctx); err == nil && info != nil {
			names[info.Name] = true
		}
	}
	for _, want := range []string{"report", "present", "ask_user_question", "follow",
		"ack", "handback", "thread_unrelated", "request_status"} {
		if !names[want] {
			t.Fatalf("worker tool %q missing; have %v", want, names)
		}
	}
}

// The SuperAgent chat call must carry no tools: it is not an orchestrator.
func TestSuperAgentChatRequestHasNoTools(t *testing.T) {
	capturedCh := make(chan []byte, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		select {
		case capturedCh <- body:
		default:
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"好\"}}]}\n\ndata: [DONE]\n\n")
		if fl, ok := w.(http.Flusher); ok {
			fl.Flush()
		}
	}))
	defer srv.Close()
	rt, _, _ := newRound3(t)
	rt.EnableWorker(WorkerConfig{Root: t.TempDir()})
	if _, err := rt.SaveSettings("worker", map[string]string{
		"base_url": srv.URL, "api_key": "k", "model": "m",
	}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	go func() { _ = rt.Dispatch("你好") }()
	var captured []byte
	select {
	case captured = <-capturedCh:
	case <-time.After(5 * time.Second):
		t.Fatal("the chat model was never called")
	}
	_ = deadline
	var req map[string]json.RawMessage
	if err := json.Unmarshal(captured, &req); err != nil {
		t.Fatalf("request body = %q", captured)
	}
	if _, ok := req["tools"]; ok {
		t.Fatalf("chat request injected tools: %s", captured)
	}
	if _, ok := req["tool_choice"]; ok {
		t.Fatalf("chat request injected tool_choice: %s", captured)
	}
}

var _ tool.BaseTool = nil
