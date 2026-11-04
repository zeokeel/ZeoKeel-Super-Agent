package main

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zeokeel/super-agent/internal/agent/mockllm"
	"github.com/zeokeel/super-agent/internal/domain"
	"github.com/zeokeel/super-agent/internal/engine"
	"github.com/zeokeel/super-agent/internal/runtime"
	"github.com/zeokeel/super-agent/internal/view"
)

// The 1.0 acceptance path, as one test: a colleague's chat message arrives, Eval
// judges it, a person approves (approval is not dispatch), the Worker runs in a
// durable queue, a deliverable appears, and the answer goes back to the same
// conversation. Each piece has its own tests; this proves they are wired together.
func TestEndToEndFromChatToReply(t *testing.T) {
	srv, err := mockllm.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	srv.ToolName = "bash"
	srv.ToolArgs = `{"command":"echo done > report.md"}`

	state := filepath.Join(t.TempDir(), "state")
	eng := engine.New(engine.RuleAssessor{})
	d, err := openDurable(eng, state, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	rt := runtime.New(eng, view.NewSnapshot())
	if err := attachDurable(rt, d); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := d.pers.Register(domain.Workspace{
		ID: "ws_web", Name: "web", Root: t.TempDir(), Kind: domain.KindRepo,
		Brief: domain.Brief{Purpose: "面向客户的 web 控制台", Keywords: []string{"webapp"}},
	}, enginePolicy("ding")); err != nil {
		t.Fatal(err)
	}
	rt.EnableWorker(runtime.WorkerConfig{
		BaseURL: srv.URL, APIKey: "sk-mock", Model: "mock-tool",
		Root: filepath.Join(state, "work"),
	})

	// 1. A colleague asks in a direct message.
	tr := domain.Trigger{
		ID:     domain.Key(domain.WorkspaceScope("ws_web"), "trigger", "am-e2e"),
		Source: "dingtalk", Human: true,
		Addressed:    domain.Addressed{Direct: true},
		Text:         "把 webapp 的登录超时改成可配置",
		Actor:        domain.Actor{ID: "ding"},
		Threads:      []domain.ThreadRef{{Source: "dingtalk", ID: "dm-e2e"}},
		Conversation: domain.Conversation{Kind: domain.ConvDirect, ID: "dm-e2e"},
		Channel:      domain.ChannelRef{Source: "dingtalk", Thread: "dm-e2e"},
		At:           now,
	}
	// DSH flow: the explicit IM request is accepted directly (no approval step).
	if _, err := d.pers.Receive(tr); err != nil {
		t.Fatal(err)
	}
	// 2. The 2s pass turns the request into a Ticket.
	out := evalPersistence(d.pers)
	if out.Ticket == "" {
		t.Fatalf("a colleague request must create a Ticket: %+v", out)
	}

	// 4. The durable queue launches the Worker; wait for the deliverable.
	deadline := time.Now().Add(25 * time.Second)
	var tk domain.Ticket
	for time.Now().Before(deadline) {
		got, ok := eng.TicketSnapshot(out.Ticket)
		if ok && len(got.Artifacts) > 0 {
			tk = got
			if tk.LastReport() != nil && tk.LastReport().State == domain.ReportReady {
				break
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	if len(tk.Artifacts) == 0 {
		t.Fatal("the Worker produced no deliverable")
	}
	if tk.LastReport() == nil || tk.LastReport().State != domain.ReportReady {
		t.Fatalf("the Worker never reported ready: %+v", tk.LastReport())
	}

	// 5. The answer goes back to the conversation it came from. The Worker reports
	// `ready` before it queues the outbound reply, so wait for the reply instead of
	// assuming the two are visible in the same instant (CI -race is slower).
	replyDeadline := time.Now().Add(15 * time.Second)
	var replies []runtime.Reply
	for time.Now().Before(replyDeadline) {
		replies = rt.Replies()
		if len(replies) > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if len(replies) == 0 {
		t.Fatal("no reply was produced")
	}
	last := replies[len(replies)-1]
	if last.Source != "dingtalk" || !strings.Contains(last.Text, "已完成") {
		t.Fatalf("bad reply: %+v", last)
	}
	if !strings.Contains(last.Text, tk.Artifacts[0].Title) {
		t.Fatalf("the reply must name the deliverable: %q", last.Text)
	}

	// 6. Everything survives a restart: the Ticket, its deliverable and the receipt.
	d.Close()
	eng2 := engine.New(engine.RuleAssessor{})
	d2, err := openDurable(eng2, state, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer d2.Close()
	restored, ok := eng2.TicketSnapshot(out.Ticket)
	if !ok {
		t.Fatal("the Ticket did not survive the restart")
	}
	if len(restored.Artifacts) == 0 {
		t.Fatal("the deliverable did not survive the restart")
	}
	if len(eng2.Workspaces()) != 1 {
		t.Fatal("the workspace did not survive the restart")
	}
}

// enginePolicy allows one DingTalk account to ask in this workspace.
func enginePolicy(user string) engine.Policy {
	return engine.Policy{Version: "v1",
		Allowed: map[string]map[string]bool{"dingtalk": {user: true}}}
}
