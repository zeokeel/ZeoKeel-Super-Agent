package runtime

// Channel adaptation tests: per-source composition, the IM length bound, and the
// ReplyPlan gate that keeps a notify-mode reply off a colleague's channel.

import (
	"strings"
	"testing"

	"github.com/zeokeel/super-agent/internal/domain"
)

func TestComposeReplyPerSource(t *testing.T) {
	report := &domain.Report{State: domain.ReportReady, Summary: "把登录超时改成可配置，默认 30 分钟"}
	arts := []domain.Artifact{
		{Kind: domain.ArtifactFile, Title: "config.md"},
		{Kind: domain.ArtifactFile, Title: "session.go"},
	}

	im, reason := composeReply("dingtalk", domain.Ticket{Title: "改登录超时"}, report, arts)
	if reason != "result" {
		t.Fatalf("reason = %q", reason)
	}
	if n := len([]rune(im)); n > imMaxRunes {
		t.Fatalf("IM reply is %d runes, over the %d limit: %q", n, imMaxRunes, im)
	}
	if strings.Contains(im, "**") {
		t.Fatalf("IM reply used GitHub markdown: %q", im)
	}

	gh, _ := composeReply("github", domain.Ticket{Title: "改登录超时"}, report, arts)
	for _, want := range []string{"**背景**", "**改动**", "**验证**", "**交付物**", "config.md"} {
		if !strings.Contains(gh, want) {
			t.Fatalf("GitHub reply missing %q:\n%s", want, gh)
		}
	}

	local, _ := composeReply("local", domain.Ticket{}, report, arts)
	if strings.Contains(local, "**背景**") {
		t.Fatalf("local reply should stay concise: %q", local)
	}
}

func TestComposeIMReplyIsBounded(t *testing.T) {
	long := strings.Repeat("很长的结论。", 60)
	text, _ := composeReply("feishu", domain.Ticket{}, &domain.Report{State: domain.ReportFailed, Summary: long}, nil)
	if n := len([]rune(text)); n > imMaxRunes {
		t.Fatalf("bounded IM reply = %d runes: %q", n, text)
	}
	if !strings.HasSuffix(text, "…") {
		t.Fatalf("a cut reply should say it was cut: %q", text)
	}
}

func TestReplyPlanGatesDirectReplies(t *testing.T) {
	colleague := domain.Trigger{Source: "dingtalk", Human: true, Actor: domain.Actor{ID: "ding"}}
	operator := domain.Trigger{Source: "dingtalk", Self: true, Human: true, Actor: domain.Actor{ID: "me"}}
	local := domain.Trigger{Source: "local", Human: true, Actor: domain.Actor{ID: "operator"}}

	if mayReplyDirectly(colleague, domain.ReplyPlan{Route: domain.ReplyDirect}, true) != true {
		t.Fatal("a direct plan should allow the colleague channel")
	}
	if mayReplyDirectly(colleague, domain.ReplyPlan{Route: domain.ReplyOperator}, true) != false {
		t.Fatal("a notify/operator plan must not send to the colleague")
	}
	if mayReplyDirectly(colleague, domain.ReplyPlan{}, false) != false {
		t.Fatal("no recorded plan must fail closed for a colleague")
	}
	if !mayReplyDirectly(operator, domain.ReplyPlan{}, false) {
		t.Fatal("the operator's own message is always answerable")
	}
	if !mayReplyDirectly(local, domain.ReplyPlan{Route: domain.ReplyNone}, true) {
		t.Fatal("the local panel is not a colleague channel")
	}
}
