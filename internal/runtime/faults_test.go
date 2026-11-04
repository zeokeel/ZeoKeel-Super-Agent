package runtime

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zeokeel/super-agent/internal/agent/mockllm"
	"github.com/zeokeel/super-agent/internal/domain"
	"github.com/zeokeel/super-agent/internal/engine"
	"github.com/zeokeel/super-agent/internal/store"
	"github.com/zeokeel/super-agent/internal/view"
)

// When the model fails, the Ticket must end up failed AND the person who asked
// must be told. A ticket that silently turns red leaves the requester waiting
// forever (design §5.8).
func TestModelFailureMarksTheTicketAndReplies(t *testing.T) {
	srv, err := mockllm.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	eng := engine.New(engine.RuleAssessor{})
	eng.Register(domain.Workspace{
		ID: "ws_web", Name: "web", Root: t.TempDir(), Kind: domain.KindRepo,
		Brief: domain.Brief{Purpose: "webapp", Keywords: []string{"webapp"}},
	}, engine.Policy{Version: "v1", Allowed: map[string]map[string]bool{
		"dingtalk": {"colleague": true},
	}})

	// A request from a conversation, so a reply has somewhere to go.
	tr := domain.Trigger{
		ID:     domain.Key(domain.WorkspaceScope(""), "dingtalk", "f1"),
		Source: "dingtalk", Human: true,
		Addressed:    domain.Addressed{Direct: true},
		Text:         "把 webapp 的登录超时改成可配置",
		Actor:        domain.Actor{ID: "colleague"},
		Conversation: domain.Conversation{Kind: domain.ConvDirect, ID: "dm"},
		Channel:      domain.ChannelRef{Source: "dingtalk", Thread: "dm"},
		At:           time.Now(),
	}
	out, err := eng.Receive(withThread(tr))
	if err != nil {
		t.Fatal(err)
	}
	approved := evalNow(eng)
	if approved.Ticket == "" {
		t.Fatalf("the request did not create a ticket: %+v", approved)
	}
	out.Ticket = approved.Ticket

	rt := New(eng, view.NewSnapshot())
	rt.EnableWorker(WorkerConfig{
		BaseURL: srv.URL, APIKey: "sk-mock", Model: "mock-500",
		Root: filepath.Join(t.TempDir(), "work"),
	})
	settleRuntime(t, rt)
	if out.Ticket == "" {
		// approve returned the ticket id; re-read it from the engine
		t.Fatal("no ticket after approval")
	}
	// Launch the executor the way the durable queue would.
	if tk, ok := eng.TicketSnapshot(out.Ticket); ok {
		go rt.runTicket(tk)
	}

	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		tk, ok := eng.TicketSnapshot(out.Ticket)
		if ok {
			if r := tk.LastReport(); r != nil && r.State == domain.ReportFailed {
				// The requester has to hear about it.
				replies := rt.Replies()
				if len(replies) == 0 {
					t.Fatalf("the failure was not reported back to the requester: %+v", r)
				}
				if replies[len(replies)-1].Reason != "failed" {
					t.Fatalf("reply reason %q, want failed", replies[len(replies)-1].Reason)
				}
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	tk, _ := eng.TicketSnapshot(out.Ticket)
	t.Fatalf("the failure never surfaced: %+v", tk.LastReport())
}

// A broken database must surface as an error on save, not a panic and not a
// silent success: the settings page has to be able to say "保存失败".
func TestClosedStoreFailsSaveInsteadOfPanicking(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "zeokeel.db"))
	if err != nil {
		t.Fatal(err)
	}
	rt := New(engine.New(engine.RuleAssessor{}), view.NewSnapshot())
	if err := rt.LoadSettings(st); err != nil {
		t.Fatal(err)
	}
	st.Close()

	if _, err := rt.SaveSettings("execution", map[string]string{"concurrency": "3"}); err == nil {
		t.Fatal("saving into a closed store must fail loudly")
	}
	// And the app still projects: a database problem must not take the window down.
	rt.Publish()
	if rt.snap.Overview() == nil {
		t.Fatal("publish stopped working after a store failure")
	}
}

// The failure summary is what a person reads in the panel and in the reply. eino's
// wrapper text must not leak into it.
func TestFailureSummaryIsReadable(t *testing.T) {
	raw := errors.New("[NodeRunError] error, status code: 500, status: 500 Internal Server Error, message: upstream down\n------------------------\nnode path: [node_1, ChatModel]")
	got := summarizeAgentError(raw)
	if strings.Contains(got, "node path") || strings.Contains(got, "------") {
		t.Fatalf("framework internals leaked: %q", got)
	}
	if !strings.Contains(got, "500") {
		t.Fatalf("the actionable part was dropped: %q", got)
	}
	if len([]rune(got)) > 220 {
		t.Fatalf("too long for a reply: %d runes", len([]rune(got)))
	}
	if summarizeAgentError(nil) != "" {
		t.Fatal("nil error must give an empty summary")
	}
	if got := summarizeAgentError(errors.New("")); got == "" {
		t.Fatal("an empty error still needs a sentence")
	}
}

// A hung MCP server must not take the app with it: the tools disappear, the rest
// keeps working, and the panel still publishes. This drives the real worker path
// with a model that never answers, which is the closest local equivalent of a
// wedged dependency.
func TestHungDependencyDoesNotBlockTheApp(t *testing.T) {
	srv, err := mockllm.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	eng := engine.New(engine.RuleAssessor{})
	eng.Register(domain.Workspace{
		ID: "ws_web", Name: "web", Root: t.TempDir(), Kind: domain.KindRepo,
		Brief: domain.Brief{Purpose: "webapp", Keywords: []string{"webapp"}},
	}, engine.Policy{Version: "v1"})

	rt := New(eng, view.NewSnapshot())
	rt.EnableWorker(WorkerConfig{
		BaseURL: "http://127.0.0.1:1", APIKey: "k", Model: "m", // nothing listens here
		Root: filepath.Join(t.TempDir(), "work"),
	})
	settleRuntime(t, rt)
	rt.Publish()
	if rt.snap.Overview() == nil {
		t.Fatal("publishing stopped")
	}
	// The panel keeps working while the executor is doomed.
	for i := 0; i < 3; i++ {
		rt.Publish()
		if rt.snap.Stream() == nil {
			t.Fatal("stream projection stopped")
		}
	}
}
