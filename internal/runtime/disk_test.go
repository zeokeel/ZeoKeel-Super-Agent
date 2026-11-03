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
	"github.com/zeokeel/super-agent/internal/view"
)

// An unwritable state directory is the local stand-in for a full disk: the
// executor cannot even start. The Ticket must say so and the requester must be
// told, instead of sitting in 排队中 forever (design §5.8).
func TestUnwritableStateDirectoryFailsTheTicket(t *testing.T) {
	srv, err := mockllm.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	eng := engine.New(engine.RuleAssessor{})
	eng.Register(domain.Workspace{
		ID: "ws_web", Name: "web", Root: t.TempDir(), Kind: domain.KindRepo,
		Brief: domain.Brief{Purpose: "webapp", Keywords: []string{"webapp"}},
	}, engine.Policy{Version: "v1", RequireApproval: true, Allowed: map[string]map[string]bool{
		"dingtalk": {"colleague": true},
	}})

	tr := domain.Trigger{
		ID:     domain.Key(domain.WorkspaceScope(""), "dingtalk", "d1"),
		Source: "dingtalk", Human: true,
		Addressed:    domain.Addressed{Direct: true},
		Text:         "把 webapp 的登录超时改成可配置",
		Actor:        domain.Actor{ID: "colleague"},
		Conversation: domain.Conversation{Kind: domain.ConvDirect, ID: "dm"},
		Channel:      domain.ChannelRef{Source: "dingtalk", Thread: "dm"},
		At:           time.Now(),
	}
	if _, err := eng.Receive(withThread(tr)); err != nil {
		t.Fatal(err)
	}
	// An IM request becomes an approval; approving it authorizes the work.
	approved := evalNow(eng)
	if approved.Ticket == "" {
		approved.Ticket = ticketAfterProposal(t, eng, "disk")
	}

	// Occupy the session path with a regular file: creating the directory then
	// fails even for root, which is the portable stand-in for an unwritable disk.
	root := t.TempDir()
	sessions := filepath.Join(root, "ticket-1", ".zeokeel", "sessions")
	if err := os.MkdirAll(filepath.Dir(sessions), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sessions, []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}

	rt := New(eng, view.NewSnapshot())
	rt.EnableWorker(WorkerConfig{
		BaseURL: srv.URL, APIKey: "sk-mock", Model: "mock-tool", Root: root,
	})
	settleRuntime(t, rt)
	if tk, ok := eng.TicketSnapshot(approved.Ticket); ok {
		go rt.runTicket(tk)
	}

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if tk, ok := eng.TicketSnapshot(approved.Ticket); ok {
			if rep := tk.LastReport(); rep != nil {
				if rep.State != domain.ReportFailed && rep.State != domain.ReportBlocked {
					t.Fatalf("state = %q", rep.State)
				}
				if strings.TrimSpace(rep.Summary) == "" {
					t.Fatal("the failure has no explanation")
				}
				if len(rt.Replies()) == 0 {
					t.Fatal("the requester was never told")
				}
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("the ticket stayed queued forever after the executor could not start")
}
