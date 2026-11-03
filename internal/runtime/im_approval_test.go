package runtime

// IM proposals (Lead requirement): every inbound feishu/dingtalk message is a
// pending approval, never auto-run. Approve authorizes work; Reject leaves nothing;
// two messages are two independent approvals.

import (
	"testing"
	"time"

	"github.com/zeokeel/super-agent/internal/domain"
	"github.com/zeokeel/super-agent/internal/engine"
	"github.com/zeokeel/super-agent/internal/view"
)

func newApprovalRuntime(t *testing.T) (*Runtime, *engine.Engine) {
	t.Helper()
	eng := engine.New(engine.RuleAssessor{})
	eng.Register(domain.Workspace{ID: "ws_web", Name: "web", Root: t.TempDir(), Kind: domain.KindRepo,
		Brief: domain.Brief{Purpose: "webapp", Keywords: []string{"webapp"}}},
		engine.Policy{Version: "v1", Allowed: map[string]map[string]bool{
			"feishu": {"ou_zhangsan": true}, "dingtalk": {"ding_zhangsan": true},
		}})
	return New(eng, view.NewSnapshot()), eng
}

func pendingIMTrigger(source, actor, conv, id, text string) domain.Trigger {
	return domain.Trigger{
		ID:           domain.Key(domain.Global, source, "trigger", conv, id),
		Scope:        domain.Global,
		Source:       source,
		Threads:      []domain.ThreadRef{{Source: source, ID: conv}},
		Human:        true,
		Addressed:    domain.Addressed{Direct: true},
		Actor:        domain.Actor{ID: actor},
		Conversation: domain.Conversation{Kind: domain.ConvDirect, ID: conv},
		Channel:      domain.ChannelRef{Source: source, Thread: conv},
		Text:         text,
		At:           time.Now(),
	}
}

func waitProposals(t *testing.T, eng *engine.Engine, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if len(eng.Proposals()) == n {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("proposals = %d, want %d", len(eng.Proposals()), n)
}

func TestIMMessagesBecomePendingApprovals(t *testing.T) {
	rt, eng := newApprovalRuntime(t)
	if _, err := rt.ReceiveTrigger(pendingIMTrigger("feishu", "ou_zhangsan", "oc_1", "m1",
		"把 webapp 的登录超时改一下")); err != nil {
		t.Fatal(err)
	}
	waitProposals(t, eng, 1)
	if len(eng.AllTickets()) != 0 {
		t.Fatal("an IM proposal created a Ticket before approval")
	}
	rt.Publish()
	ov := rt.snap.Overview()
	if len(ov.Approvals) != 1 {
		t.Fatalf("approvals = %+v", ov.Approvals)
	}
	row := ov.Approvals[0]
	if row.Source != "feishu" || row.Said == "" || row.DecisionID == "" {
		t.Fatalf("approval row lacks source/text/decision: %+v", row)
	}

	if err := rt.Approve(row.DecisionID); err != nil {
		t.Fatal(err)
	}
	if len(eng.AllTickets()) != 1 {
		t.Fatal("Approve did not create the Ticket")
	}
	tk := eng.AllTickets()[0]
	if domain.StageOf(&tk, time.Now()) == domain.StageEnded {
		t.Fatalf("approved ticket ended instead of being queued: %+v", tk.Ended)
	}
}

func TestIMProposalRejectLeavesNothing(t *testing.T) {
	rt, eng := newApprovalRuntime(t)
	if _, err := rt.ReceiveTrigger(pendingIMTrigger("dingtalk", "ding_zhangsan", "dm_1", "m2",
		"把 webapp 的登录超时改一下")); err != nil {
		t.Fatal(err)
	}
	waitProposals(t, eng, 1)
	id := eng.Proposals()[0].ID
	if err := rt.Reject(string(id)); err != nil {
		t.Fatal(err)
	}
	if len(eng.Proposals()) != 0 || len(eng.AllTickets()) != 0 {
		t.Fatalf("reject left state: proposals=%d tickets=%d", len(eng.Proposals()), len(eng.AllTickets()))
	}
}

func TestTwoIMMessagesAreTwoApprovals(t *testing.T) {
	rt, eng := newApprovalRuntime(t)
	for _, tr := range []domain.Trigger{
		pendingIMTrigger("feishu", "ou_zhangsan", "oc_1", "a", "把 webapp 的登录超时改一下"),
		pendingIMTrigger("dingtalk", "ding_zhangsan", "dm_2", "b", "给 webapp 加一个登录超时配置"),
	} {
		if _, err := rt.ReceiveTrigger(tr); err != nil {
			t.Fatal(err)
		}
	}
	waitProposals(t, eng, 2)
	if len(eng.AllTickets()) != 0 {
		t.Fatal("batching approved/ran IM messages")
	}
	// Health/来源 say work is waiting on a person.
	rt.Publish()
	found := false
	for _, item := range rt.snap.Overview().Health {
		if item.Name == "待审批" {
			found = true
		}
	}
	if !found {
		t.Fatalf("health does not mention pending approvals: %+v", rt.snap.Overview().Health)
	}
}
