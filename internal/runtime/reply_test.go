package runtime

import (
	"strings"
	"testing"
	"time"

	"github.com/zeokeel/super-agent/internal/domain"
	"github.com/zeokeel/super-agent/internal/engine"
	"github.com/zeokeel/super-agent/internal/view"
)

func TestComposeReplySaysWhatHappened(t *testing.T) {
	artifacts := []domain.Artifact{
		{Title: "hello.txt", Detail: "16 B"},
		{Title: "report.md", Detail: "1.2 KB"},
	}
	cases := []struct {
		name   string
		report *domain.Report
		want   []string
		reason string
	}{
		{"ready", &domain.Report{State: domain.ReportReady, Summary: "写好了"},
			[]string{"已完成", "写好了", "hello.txt", "report.md"}, "result"},
		{"failed", &domain.Report{State: domain.ReportFailed, Summary: "测试没通过"},
			[]string{"没能完成", "测试没通过"}, "failed"},
		{"blocked", &domain.Report{State: domain.ReportBlocked, Summary: "等你给权限"},
			[]string{"卡住了", "等你给权限"}, "blocked"},
		{"dropped", &domain.Report{State: domain.ReportDropped, Summary: "重复提单"},
			[]string{"不需要处理", "重复提单"}, "dropped"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			text, reason := composeReply("local", domain.Ticket{Number: 1}, c.report, artifacts)
			if reason != c.reason {
				t.Fatalf("reason %q, want %q", reason, c.reason)
			}
			for _, w := range c.want {
				if !strings.Contains(text, w) {
					t.Fatalf("reply missing %q:\n%s", w, text)
				}
			}
		})
	}
}

// A Ticket that came from a conversation answers that conversation. The reply is
// recorded even without a wired channel, so the panel never hides what was said.
func TestDeliverReplyAnswersTheOriginConversation(t *testing.T) {
	eng := engine.New(engine.RuleAssessor{})
	eng.Register(domain.Workspace{
		ID: "ws_web", Name: "web", Root: "/tmp/web", Kind: domain.KindRepo,
		Brief: domain.Brief{Purpose: "webapp", Keywords: []string{"webapp"}},
	}, engine.Policy{Version: "v1", RequireApproval: true, ReplyMode: domain.ReplyModeAuto,
		Allowed: map[string]map[string]bool{"dingtalk": {"ding": true}}})

	tr := domain.Trigger{
		ID:     domain.Key(domain.WorkspaceScope("ws_web"), "trigger", "am-1"),
		Source: "dingtalk", Human: true,
		Addressed:    domain.Addressed{Mention: true},
		Text:         "把 webapp 的登录超时改成可配置",
		Actor:        domain.Actor{ID: "ding"},
		Conversation: domain.Conversation{Kind: domain.ConvDirect, ID: "dm-ding"},
		Channel:      domain.ChannelRef{Source: "dingtalk", Thread: "dm-ding"},
		At:           time.Now(),
	}
	if _, err := eng.Receive(withThread(tr)); err != nil {
		t.Fatal(err)
	}
	// IM messages wait for the operator's approval; approving authorizes the
	// Ticket the reply test needs.
	out := evalNow(eng)
	if out.Ticket == "" {
		if id := approvePending(t, eng); id != "" {
			out.Ticket = id
		}
	}
	if out.Ticket == "" {
		t.Fatalf("expected a ticket after approving the colleague request: %+v", out)
	}
	tk, ok := eng.TicketSnapshot(out.Ticket)
	if !ok {
		t.Fatal("no ticket after approval")
	}
	if tk.Origin != tr.ID {
		t.Fatalf("ticket lost its origin: %q", tk.Origin)
	}

	rt := New(eng, view.NewSnapshot())
	if _, err := eng.Present(tk.ID, domain.Artifact{Kind: domain.ArtifactFile, Title: "hello.txt"}); err != nil {
		t.Fatal(err)
	}
	if err := eng.Report(tk.ID, domain.ReportReady, "改好了"); err != nil {
		t.Fatal(err)
	}
	tk, _ = eng.TicketSnapshot(tk.ID)
	rt.deliverReply(tk)

	replies := rt.Replies()
	if len(replies) != 1 {
		t.Fatalf("expected one reply, got %d", len(replies))
	}
	if replies[0].Source != "dingtalk" || !strings.Contains(replies[0].Text, "hello.txt") {
		t.Fatalf("bad reply: %+v", replies[0])
	}
}
