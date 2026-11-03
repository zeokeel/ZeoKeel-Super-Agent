package github

import (
	"strings"
	"testing"
	"time"

	"github.com/zeokeel/super-agent/internal/domain"
	"github.com/zeokeel/super-agent/internal/sources"
)

func TestTranslateCommentMapsFacts(t *testing.T) {
	src := testSource(t, &fakeCommand{}, nil)
	tr, err := src.Translate(sources.Raw{
		Kind: RawComment,
		Data: commentElement(t, 501, "alice", "@zeokeel-agent 请修一下这个", 12),
	})
	if err != nil {
		t.Fatalf("Translate: %v", err)
	}

	if tr.Source != "github" {
		t.Errorf("Source = %q, want github", tr.Source)
	}
	if len(tr.Threads) != 1 || tr.Threads[0].Source != "github" || tr.Threads[0].ID != "acme/webapp#12" {
		t.Errorf("Threads = %+v, want [{github acme/webapp#12}]", tr.Threads)
	}
	if tr.Actor.ID != "alice" || tr.Actor.Bot {
		t.Errorf("Actor = %+v, want human alice", tr.Actor)
	}
	if !tr.Human || tr.Self {
		t.Errorf("Human = %v, Self = %v, want true/false", tr.Human, tr.Self)
	}
	if !tr.Addressed.Mention {
		t.Error("Addressed.Mention = false, want true")
	}
	if tr.Addressed.Direct || tr.Addressed.ReplyTo {
		t.Errorf("Addressed = %+v, want mention only", tr.Addressed)
	}
	if tr.Text != "@zeokeel-agent 请修一下这个" {
		t.Errorf("Text = %q", tr.Text)
	}
	if tr.Conversation.Kind != domain.ConvGroup ||
		tr.Conversation.ID != "acme/webapp#12" ||
		tr.Conversation.Name != "acme/webapp" {
		t.Errorf("Conversation = %+v", tr.Conversation)
	}
	wantAt := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	if !tr.At.Equal(wantAt) {
		t.Errorf("At = %v, want %v", tr.At, wantAt)
	}

	// 工作区作用域：Thread key、Trigger ID 都带同一个 scope，DefaultWorkspace
	// 供 Router 直接使用。
	if tr.Scope != domain.WorkspaceScope("ws_acme") {
		t.Errorf("Scope = %q, want %q", tr.Scope, domain.WorkspaceScope("ws_acme"))
	}
	if tr.DefaultWorkspace != "ws_acme" {
		t.Errorf("DefaultWorkspace = %q", tr.DefaultWorkspace)
	}

	// 回信地址：仓库 + 编号 + 可点开的 URL + 事件形状（admission table 的行）。
	addr := tr.Channel.Address
	if tr.Channel.Source != "github" || tr.Channel.Thread != "acme/webapp#12" {
		t.Errorf("Channel = %+v", tr.Channel)
	}
	if addr["repository"] != "acme/webapp" || addr["number"] != "12" {
		t.Errorf("address = %+v", addr)
	}
	if addr["kind"] != RawComment {
		t.Errorf("address kind = %q", addr["kind"])
	}
	if !strings.Contains(addr["url"], "issuecomment-501") {
		t.Errorf("address url = %q, want the comment permalink", addr["url"])
	}
}

func TestTranslateGlobalScopeWithoutWorkspace(t *testing.T) {
	src := testSource(t, &fakeCommand{}, func(cfg *Config) { cfg.Workspace = "" })
	tr, err := src.Translate(sources.Raw{Kind: RawComment, Data: commentElement(t, 1, "alice", "@zeokeel-agent hi", 12)})
	if err != nil {
		t.Fatalf("Translate: %v", err)
	}
	if tr.Scope != domain.Global {
		t.Errorf("Scope = %q, want global", tr.Scope)
	}
	if tr.DefaultWorkspace != "" {
		t.Errorf("DefaultWorkspace = %q, want empty for a global Source", tr.DefaultWorkspace)
	}
}

func TestTranslateIDIsContentDerived(t *testing.T) {
	src := testSource(t, &fakeCommand{}, nil)
	raw := sources.Raw{Kind: RawComment, Data: commentElementAt(t, 501, "alice", "@zeokeel-agent hi", 12, "2026-01-02T03:04:05Z", "User")}

	first, err := src.Translate(raw)
	if err != nil {
		t.Fatalf("Translate #1: %v", err)
	}
	again, err := src.Translate(raw)
	if err != nil {
		t.Fatalf("Translate #2: %v", err)
	}
	if first.ID != again.ID {
		t.Fatalf("same event gave different IDs: %s vs %s（重放必须幂等）", first.ID, again.ID)
	}

	// 编辑过的评论是另一条事实：updated_at 进 ID 的版本段。
	edited, err := src.Translate(sources.Raw{
		Kind: RawComment,
		Data: commentElementAt(t, 501, "alice", "@zeokeel-agent hi", 12, "2026-01-02T04:04:05Z", "User"),
	})
	if err != nil {
		t.Fatalf("Translate edited: %v", err)
	}
	if edited.ID == first.ID {
		t.Fatal("edited comment kept the same Trigger ID, so the edit would be silently dropped")
	}

	// 不同评论 id 必须不同：ID 的主体是评论 id。
	other, err := src.Translate(sources.Raw{Kind: RawComment, Data: commentElement(t, 502, "alice", "@zeokeel-agent hi", 12)})
	if err != nil {
		t.Fatalf("Translate other comment: %v", err)
	}
	if other.ID == first.ID {
		t.Fatal("two different comments share a Trigger ID")
	}

	// Scope 也是 ID 的一部分：两个工作区各自的 #12 不能串成一张单。
	global := testSource(t, &fakeCommand{}, func(cfg *Config) { cfg.Workspace = "" })
	globalTrigger, err := global.Translate(raw)
	if err != nil {
		t.Fatalf("Translate global: %v", err)
	}
	if globalTrigger.ID == first.ID {
		t.Fatal("the same comment in two scopes shares a Trigger ID")
	}
}

func TestTranslateBotIsNotHuman(t *testing.T) {
	cases := []struct {
		name     string
		login    string
		userType string
		wantBot  bool
		wantHum  bool
	}{
		{"app bot", "dependabot[bot]", "Bot", true, false},
		{"bot suffix with User type", "helper[bot]", "User", true, false},
		{"person", "alice", "User", false, true},
		{"person without type", "alice", "", false, true},
		{"organization", "acme-inc", "Organization", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := testSource(t, &fakeCommand{}, nil)
			tr, err := src.Translate(sources.Raw{
				Kind: RawComment,
				Data: commentElementAt(t, 7, tc.login, "@zeokeel-agent hi", 1, "2026-01-02T03:04:05Z", tc.userType),
			})
			if err != nil {
				t.Fatalf("Translate: %v", err)
			}
			if tr.Actor.Bot != tc.wantBot {
				t.Errorf("Actor.Bot = %v, want %v", tr.Actor.Bot, tc.wantBot)
			}
			if tr.Human != tc.wantHum {
				t.Errorf("Human = %v, want %v", tr.Human, tc.wantHum)
			}
		})
	}
}

func TestTranslateAgentOwnCommentIsNotHuman(t *testing.T) {
	src := testSource(t, &fakeCommand{}, nil)
	tr, err := src.Translate(sources.Raw{
		Kind: RawComment,
		Data: commentElement(t, 9, "zeokeel-agent", "@zeokeel-agent 自己回复自己", 1),
	})
	if err != nil {
		t.Fatalf("Translate: %v", err)
	}
	if tr.Human {
		t.Fatal("the agent's own comment was marked human; the gate would let it start work")
	}
	if tr.Actor.Bot {
		t.Fatal("the agent's account is a user account, not a [bot]")
	}
}

func TestTranslateSelfAccount(t *testing.T) {
	src := testSource(t, &fakeCommand{}, func(cfg *Config) { cfg.Self = []string{"Zeokeel-Operator"} })
	tr, err := src.Translate(sources.Raw{
		Kind: RawComment,
		Data: commentElement(t, 11, "zeokeel-operator", "@zeokeel-agent 跑一下", 1),
	})
	if err != nil {
		t.Fatalf("Translate: %v", err)
	}
	if !tr.Self || !tr.Human {
		t.Fatalf("Self = %v, Human = %v, want true/true: the operator is a person too", tr.Self, tr.Human)
	}
}

func TestTranslateAddressedOnlyOnExplicitMention(t *testing.T) {
	cases := []struct {
		body string
		want bool
	}{
		{"@zeokeel-agent 请修", true},
		{"hi @Zeokeel-Agent", true},        // GitHub handle 大小写不敏感
		{"(@zeokeel-agent)", true},         // 括号是合法边界
		{"@zeokeel-agent-other 你好", false}, // 更长的 handle 不是本 agent
		{"a@zeokeel-agent.com", false},     // 邮箱里的 @ 不是 mention
		{"x@zeokeel-agent", false},         // 前缀是字母，不是独立 mention
		{"@everyone 今晚发版", false},          // 群公告不是 addressed
		{"@所有人 今晚发版", false},               // 同理
		{"zeokeel-agent 请修", false},        // 没有 @ 就不是
		{"", false},
	}
	for _, tc := range cases {
		src := testSource(t, &fakeCommand{}, nil)
		tr, err := src.Translate(sources.Raw{
			Kind: RawComment,
			Data: commentElement(t, 21, "alice", tc.body, 1),
		})
		if err != nil {
			t.Fatalf("Translate(%q): %v", tc.body, err)
		}
		if tr.Addressed.Mention != tc.want {
			t.Errorf("MentionsAgent(%q) = %v, want %v", tc.body, tr.Addressed.Mention, tc.want)
		}
	}
}

func TestTranslateCustomMentionHandle(t *testing.T) {
	src := testSource(t, &fakeCommand{}, func(cfg *Config) {
		cfg.Mention = "zeokeel"
	})
	tr, err := src.Translate(sources.Raw{Kind: RawComment, Data: commentElement(t, 31, "alice", "@zeokeel 在吗", 1)})
	if err != nil {
		t.Fatalf("Translate: %v", err)
	}
	if !tr.Addressed.Mention {
		t.Fatal("custom mention handle was not recognised")
	}
	// 配置叫 zeokeel 时，@zeokeel-agent 是另一个更长的 handle，不该命中。
	if MentionsAgent("@zeokeel-agent hi", "zeokeel") {
		t.Fatal("@zeokeel-agent matched the shorter handle zeokeel")
	}
}

func TestTranslateRejectsBadData(t *testing.T) {
	src := testSource(t, &fakeCommand{}, nil)
	cases := []struct {
		name string
		kind string
		data string
	}{
		{"not json", RawComment, `{`},
		{"unknown kind", "issue", `{"id":1}`},
		{"no id", RawComment, `{"body":"x"}`},
		{"no author", RawComment, `{"id":1,"body":"x"}`},
		{"no issue url", RawComment, `{"id":1,"user":{"login":"a"},"created_at":"2026-01-02T03:04:05Z"}`},
		{"no timestamp", RawComment, `{"id":1,"user":{"login":"a"},"issue_url":"https://api.github.com/repos/acme/webapp/issues/12"}`},
		{"bad timestamp", RawComment, `{"id":1,"user":{"login":"a"},"issue_url":"https://api.github.com/repos/acme/webapp/issues/12","created_at":"not-a-time"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := src.Translate(sources.Raw{Kind: tc.kind, Data: []byte(tc.data)}); err == nil {
				t.Fatal("Translate accepted an unreadable event")
			}
		})
	}
}

func TestParseIssueRef(t *testing.T) {
	cases := []struct {
		url    string
		repo   string
		number int
		isPR   bool
		wantOK bool
	}{
		{"https://api.github.com/repos/acme/webapp/issues/12", "acme/webapp", 12, false, true},
		{"https://api.github.com/repos/acme/webapp/pulls/12", "acme/webapp", 12, true, true},
		{"https://github.com/acme/webapp/pull/12", "acme/webapp", 12, true, true},
		{"https://github.com/acme/webapp/issues/12#issuecomment-501", "acme/webapp", 12, false, true},
		{"https://api.github.com/repos/acme/webapp/issues/12?x=1", "acme/webapp", 12, false, true},
		{"https://example.com/acme/webapp/issues/12", "acme/webapp", 12, false, true},
		{"https://api.github.com/repos/acme/webapp/issues/0", "", 0, false, false},
		{"https://api.github.com/repos/acme/webapp/not-an-issue/12", "", 0, false, false},
		{"", "", 0, false, false},
	}
	for _, tc := range cases {
		repo, number, isPR, ok := ParseIssueRef(tc.url)
		if ok != tc.wantOK || repo != tc.repo || number != tc.number || isPR != tc.isPR {
			t.Errorf("ParseIssueRef(%q) = (%q,%d,%v,%v), want (%q,%d,%v,%v)",
				tc.url, repo, number, isPR, ok, tc.repo, tc.number, tc.isPR, tc.wantOK)
		}
	}
}

func TestThreadIDRoundTrip(t *testing.T) {
	id := ThreadID("acme/webapp", 12)
	if id != "acme/webapp#12" {
		t.Fatalf("ThreadID = %q", id)
	}
	repo, number, ok := SplitThreadID(id)
	if !ok || repo != "acme/webapp" || number != 12 {
		t.Fatalf("SplitThreadID(%q) = (%q,%d,%v)", id, repo, number, ok)
	}
	for _, bad := range []string{"", "acme/webapp", "acme/webapp#", "acme/webapp#x", "acme/webapp#0"} {
		if _, _, ok := SplitThreadID(bad); ok {
			t.Errorf("SplitThreadID(%q) accepted a malformed id", bad)
		}
	}
}

func TestIsBot(t *testing.T) {
	if !IsBot("dependabot[bot]") || !IsBot("Dependabot[BOT]") {
		t.Fatal("the [bot] suffix was not recognised")
	}
	if IsBot("alice") || IsBot("[bot]alice") {
		t.Fatal("a normal login was classified as a bot")
	}
}
