package runtime

// rc2 UX tests: streaming chat, apply-back, plan visibility, update check.

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zeokeel/super-agent/internal/domain"
	"github.com/zeokeel/super-agent/internal/engine"
	"github.com/zeokeel/super-agent/internal/view"
)

// sseServer streams the given chunks with a small delay, OpenAI style.
func sseServer(t *testing.T, chunks []string, delay time.Duration) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		for _, c := range chunks {
			fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":%q}}]}\n\n", c)
			fl.Flush()
			time.Sleep(delay)
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
		fl.Flush()
	}))
}

func TestChatStreamsPartialThenFinal(t *testing.T) {
	srv := sseServer(t, []string{"你", "好"}, 40*time.Millisecond)
	defer srv.Close()
	rt, _, snap := newRound3(t)
	rt.EnableWorker(WorkerConfig{Root: t.TempDir()})
	if _, err := rt.SaveSettings("worker", map[string]string{
		"base_url": srv.URL, "api_key": "k", "model": "m",
	}); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- rt.Dispatch("你好") }()

	// The UI sees a partial assistant message before the final one.
	partialSeen := false
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if vm := snap.Chat(); vm != nil {
			if last, ok := vm.Last(); ok && last.Role == view.ChatAssistant && last.Text == "你" {
				partialSeen = true
				break
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err := <-done; err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if !partialSeen {
		t.Fatal("no partial streaming message was published")
	}
	msgs := rt.chatHistory()
	if len(msgs) == 0 || msgs[len(msgs)-1].Text != "你好" {
		t.Fatalf("final chat = %+v", msgs)
	}
}

func TestStopChatCancelsTheStream(t *testing.T) {
	srv := sseServer(t, []string{"第一段", "第二段"}, 300*time.Millisecond)
	defer srv.Close()
	rt, _, _ := newRound3(t)
	rt.EnableWorker(WorkerConfig{Root: t.TempDir()})
	if _, err := rt.SaveSettings("worker", map[string]string{
		"base_url": srv.URL, "api_key": "k", "model": "m",
	}); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- rt.Dispatch("你好") }()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && !rt.ChatStreaming() {
		time.Sleep(5 * time.Millisecond)
	}
	rt.StopChat()
	<-done
	msgs := rt.chatHistory()
	if len(msgs) == 0 || !strings.Contains(msgs[len(msgs)-1].Text, "已停止生成") {
		t.Fatalf("stopped chat = %+v", msgs)
	}
}

func TestApplyArtifactsCopiesWithBackupAndSkipsUnsafe(t *testing.T) {
	rt, eng, _ := newRound3(t)
	execRoot := t.TempDir()
	wsRoot := t.TempDir()
	if err := rt.NewConversation("ws_web", "在 webapp 里跑个命令"); err != nil {
		t.Fatal(err)
	}
	id := soleTicketID(t, eng)
	// The workspace root is the registered one; point it at our temp root.
	ws, _ := eng.Workspace("ws_web")
	ws.Root = wsRoot
	eng.Register(ws, enginePolicyForTest())
	tk, _ := eng.TicketSnapshot(id)
	workdir := ticketWorkdir(&WorkerConfig{Root: execRoot}, tk)
	if err := os.MkdirAll(filepath.Join(workdir, "dist"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workdir, "dist", "app.txt"), []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wsRoot, "dist", "app.txt"), []byte("old"), 0o644); err != nil {
		if err := os.MkdirAll(filepath.Join(wsRoot, "dist"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(wsRoot, "dist", "app.txt"), []byte("old"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, a := range []domain.Artifact{
		{Kind: domain.ArtifactFile, Title: "app", Path: "dist/app.txt"},
		{Kind: domain.ArtifactFile, Title: "unsafe", Path: "../escape.txt"},
		{Kind: domain.ArtifactFile, Title: "abs", Path: "/etc/passwd"},
	} {
		if _, err := eng.Present(id, a); err != nil {
			t.Fatal(err)
		}
	}
	rt.EnableWorker(WorkerConfig{Root: execRoot})
	copied, err := rt.ApplyArtifacts(string(id))
	if err != nil {
		t.Fatal(err)
	}
	if len(copied) != 1 || copied[0] != "dist/app.txt" {
		t.Fatalf("copied = %v", copied)
	}
	got, _ := os.ReadFile(filepath.Join(wsRoot, "dist", "app.txt"))
	if string(got) != "new" {
		t.Fatalf("project file = %q", got)
	}
	backups, _ := filepath.Glob(filepath.Join(wsRoot, "dist", "app.txt.bak-*"))
	if len(backups) != 1 {
		t.Fatalf("no backup was made: %v", backups)
	}
	old, _ := os.ReadFile(backups[0])
	if string(old) != "old" {
		t.Fatalf("backup content = %q", old)
	}
}

func TestPlanTasksSortedFromFiles(t *testing.T) {
	rt, eng, _ := newRound3(t)
	root := t.TempDir()
	rt.EnableWorker(WorkerConfig{Root: root})
	if err := rt.NewConversation("ws_web", "在 webapp 里跑个命令"); err != nil {
		t.Fatal(err)
	}
	id := soleTicketID(t, eng)
	dir := filepath.Join(root, "ticket-1", ".zeokeel", "plan")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(dir, "2.json"), []byte(`{"id":"2","subject":"测试","description":"d","status":"completed"}`), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "1.json"), []byte(`{"id":"1","subject":"实现","description":"d","status":"in_progress"}`), 0o644)
	_ = os.WriteFile(filepath.Join(dir, ".highwatermark"), []byte("2"), 0o644)
	plan := rt.PlanTasks(string(id))
	if len(plan) != 2 || plan[0].Title != "实现" || plan[0].Status != "in_progress" || plan[1].Order != 2 {
		t.Fatalf("plan = %+v", plan)
	}
	rt.Publish()
	published := rt.snap.Plan()
	if published == nil || published.TicketID != string(id) || len(published.Items) != 2 {
		t.Fatalf("published plan = %+v", published)
	}
}

func TestCheckForUpdate(t *testing.T) {
	old := httpDoUpdate
	defer func() { httpDoUpdate = old }()
	httpDoUpdate = func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200,
			Body: io.NopCloser(strings.NewReader(`{"tag_name":"v0.2.0","html_url":"https://github.com/zeokeel/ZeoKeel-Super-Agent/releases/tag/v0.2.0"}`))}, nil
	}
	rt, _, _ := newRound3(t)
	rt.SetVersion("v0.2.0")
	latest, upToDate, url, err := rt.CheckForUpdate(context.Background())
	if err != nil || latest != "v0.2.0" || !upToDate || !strings.Contains(url, "releases/tag") {
		t.Fatalf("update = %q %v %q %v", latest, upToDate, url, err)
	}
	httpDoUpdate = func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 500, Body: io.NopCloser(strings.NewReader("x"))}, nil
	}
	if _, _, _, err := rt.CheckForUpdate(context.Background()); err == nil {
		t.Fatal("500 did not error")
	}
}

// enginePolicyForTest keeps the workspace's policy when the test rewrites Root.
func enginePolicyForTest() engine.Policy {
	return engine.Policy{Version: "v1", Allowed: map[string]map[string]bool{"dingtalk": {"colleague": true}}}
}
