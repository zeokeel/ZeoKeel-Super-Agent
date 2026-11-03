package main

import (
	"testing"
	"time"

	"github.com/zeokeel/super-agent/internal/domain"
	"github.com/zeokeel/super-agent/internal/im"
	"github.com/zeokeel/super-agent/internal/schedule"
)

// A timer becomes the same Trigger shape a chat message produces, so nothing
// downstream needs to special-case timers.
func TestScheduleFireBecomesATrigger(t *testing.T) {
	c := schedule.Claim{
		FireID:    "fire-1",
		PlannedAt: time.Unix(1700000000, 0),
		FiredAt:   time.Unix(1700000002, 0),
		Status:    "fired",
		Definition: schedule.Definition{
			ID: "daily-report", WorkspaceID: "ws_acme_data",
		},
	}
	tr := triggerForSchedule(c)
	if tr.ID != "fire-1" || tr.Source != "schedule" {
		t.Fatalf("bad trigger: %+v", tr)
	}
	if tr.DefaultWorkspace != "ws_acme_data" {
		t.Fatal("the timer's workspace must travel with the trigger")
	}
	if !tr.Addressed.Direct || !tr.Human {
		t.Fatal("a timer is an instruction from the operator, not ambient chatter")
	}
	if tr.At.IsZero() || tr.Text == "" {
		t.Fatal("the trigger must carry a time and a task text")
	}
}

// A chat message keeps the driver's facts: @a colleague is not addressing the
// agent, and a bot is not a person (design §4.9).
func TestChatEventBecomesATrigger(t *testing.T) {
	ev := im.RawEvent{
		ID: "am-1", Text: "把登录超时改成可配置",
		Actor: domain.Actor{ID: "ding"},
		Conv:  domain.Conversation{Kind: domain.ConvDirect, ID: "dm-1"},
		At:    time.Now().UnixMilli(),
	}
	tr := triggerForIM("dingtalk", ev, "bot-self")
	if tr.Source != "dingtalk" || !tr.Human {
		t.Fatalf("bad trigger: %+v", tr)
	}
	if !tr.Addressed.Direct {
		t.Fatal("a direct message addresses the agent")
	}
	if tr.Channel.Thread != "dm-1" {
		t.Fatal("the reply must go back to the same conversation")
	}

	// The same text in a group without a mention is not addressed to the agent.
	ev.Conv = domain.Conversation{Kind: domain.ConvGroup, ID: "grp-1"}
	ev.Mentions = []string{"someone-else"}
	tr = triggerForIM("dingtalk", ev, "bot-self")
	if tr.Addressed.Mention || tr.Addressed.Direct {
		t.Fatal("mentioning a colleague must not address the agent")
	}

	// A bot message is never a person.
	ev.Actor.Bot = true
	if tr := triggerForIM("dingtalk", ev, "bot-self"); tr.Human {
		t.Fatal("a bot message must not count as a person")
	}
}
