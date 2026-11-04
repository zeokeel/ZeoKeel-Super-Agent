package runtime

// DSH flow: IM messages are ordinary requests. An admitted explicit request is
// accepted directly like GitHub or the panel; inferred candidates follow the
// accept mode. There is no approval runtime.

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

func waitTickets(t *testing.T, eng *engine.Engine, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if len(eng.AllTickets()) == n {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("tickets = %d, want %d", len(eng.AllTickets()), n)
}

// An explicit request from an admitted sender runs directly: the Trigger is
// accepted, a Ticket exists and nothing waits for the operator's approval.
func TestIMMessagesExecuteDirectly(t *testing.T) {
	rt, eng := newApprovalRuntime(t)
	if _, err := rt.ReceiveTrigger(pendingIMTrigger("feishu", "ou_zhangsan", "oc_1", "m1",
		"把 webapp 的登录超时改一下")); err != nil {
		t.Fatal(err)
	}
	waitTickets(t, eng, 1)
	tk := eng.AllTickets()[0]
	if tk.Mandate.Strength != domain.StrengthExplicit {
		t.Fatalf("the mandate must be explicit: %+v", tk.Mandate)
	}
	rt.Publish()
}

// A sender the policy does not admit stays filtered: no Ticket and no proposal.
func TestIMStrangerIsFiltered(t *testing.T) {
	rt, eng := newApprovalRuntime(t)
	if _, err := rt.ReceiveTrigger(pendingIMTrigger("dingtalk", "someone-else", "dm_9", "m2",
		"把 webapp 的登录超时改一下")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond) // give the eval pass a chance to run
	if len(eng.AllTickets()) != 0 {
		t.Fatalf("a stranger must be filtered, tickets=%d", len(eng.AllTickets()))
	}
}

// Two messages in two conversations are two independent tasks, not one batch.
func TestTwoIMConversationsCreateTwoTasks(t *testing.T) {
	rt, eng := newApprovalRuntime(t)
	for _, tr := range []domain.Trigger{
		pendingIMTrigger("feishu", "ou_zhangsan", "oc_1", "a", "把 webapp 的登录超时改一下"),
		pendingIMTrigger("dingtalk", "ding_zhangsan", "dm_2", "b", "给 webapp 加一个登录超时配置"),
	} {
		if _, err := rt.ReceiveTrigger(tr); err != nil {
			t.Fatal(err)
		}
	}
	waitTickets(t, eng, 2)
}
