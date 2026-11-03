package assess

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/zeokeel/super-agent/internal/brief"
	"github.com/zeokeel/super-agent/internal/domain"
	"github.com/zeokeel/super-agent/internal/engine"
)

// fakeGen stands in for the model so validation is tested without one. The
// generator is the only seam a model hides behind (design §5.1).
type fakeGen struct {
	reply string
	err   error
	// sawPrompt records what the assessor asked for, so a prompt regression fails
	// a test rather than quietly weakening every decision.
	sawPrompt string
	// sawSystem is the system prompt handed to the model.
	sawSystem string
}

func (f *fakeGen) Generate(_ context.Context, system, user string) (string, error) {
	f.sawPrompt, f.sawSystem = user, system
	return f.reply, f.err
}

func input() engine.AssessInput {
	return engine.AssessInput{
		Trigger: domain.Trigger{
			Source: "dingtalk", Human: true,
			Addressed: domain.Addressed{Direct: true},
			Text:      "帮我把 acme 上个月的用量数据发我一下",
			Actor:     domain.Actor{ID: "ding"},
		},
		Candidates: []brief.Candidate{{ID: "ws_acme_data", Score: 3}},
		Briefs: map[domain.WorkspaceID]string{
			"ws_acme_data": "## ws_acme_data · acme-data\n目的  客户 acme 的用量数据",
		},
	}
}

func rules() []Rule {
	return []Rule{
		{ID: "data-request", Instruction: "同事要客户数据属于可接的工作", Enabled: true},
		{ID: "off-topic", Instruction: "与仓库无关的闲聊不接", Enabled: true},
		{ID: "disabled", Instruction: "已停用", Enabled: false},
	}
}

func newAssessor(t *testing.T, gen Generator) *Assessor {
	t.Helper()
	a, err := New(Config{Rules: rules(), Model: "m"}, gen)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

const goodAccept = `{
  "kind": "accept",
  "workspace": "ws_acme_data",
  "reason": "said",
  "reason_text": "同事明确要 acme 的用量数据",
  "rules": [
    {"id": "data-request", "matched": true, "note": "正是取数请求"},
    {"id": "off-topic", "matched": false, "note": "不是闲聊"}
  ],
  "worktree_name": "acme-usage-report",
  "intent_evidence": "帮我把 acme 上个月的用量数据发我一下"
}`

func TestAcceptWithAllRulesEvaluated(t *testing.T) {
	a := newAssessor(t, &fakeGen{reply: goodAccept})
	out, err := a.Assess(context.Background(), input())
	if err != nil {
		t.Fatal(err)
	}
	if out.Kind != domain.DecAccept || out.Workspace != "ws_acme_data" {
		t.Fatalf("got %+v", out)
	}
}

// The prompt must carry the facts a decision needs, and the rules to evaluate.
func TestPromptCarriesFactsAndRules(t *testing.T) {
	gen := &fakeGen{reply: goodAccept}
	a := newAssessor(t, gen)
	if _, err := a.Assess(context.Background(), input()); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"ws_acme_data", "acme 的用量数据", "data-request", "off-topic"} {
		if !strings.Contains(gen.sawPrompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, gen.sawPrompt)
		}
	}
	if strings.Contains(gen.sawPrompt, "disabled") {
		t.Fatal("a disabled rule must not be sent for evaluation")
	}
}

func TestValidationRejectsIncompleteOutcomes(t *testing.T) {
	cases := map[string]string{
		"missing a rule": `{"kind":"accept","workspace":"ws_acme_data","strength":"explicit",
			"rules":[{"id":"data-request","matched":true}],"worktree_name":"acme-usage",
			"intent_evidence":"x"}`,
		"rule twice": `{"kind":"accept","workspace":"ws_acme_data","strength":"explicit",
			"rules":[{"id":"data-request","matched":true},{"id":"data-request","matched":true},
			         {"id":"off-topic","matched":false}],
			"worktree_name":"acme-usage","intent_evidence":"x"}`,
		"unknown rule": `{"kind":"accept","workspace":"ws_acme_data","strength":"explicit",
			"rules":[{"id":"data-request","matched":true},{"id":"off-topic","matched":false},
			         {"id":"made-up","matched":true}],
			"worktree_name":"acme-usage","intent_evidence":"x"}`,
		"no rule matched": `{"kind":"accept","workspace":"ws_acme_data","strength":"explicit",
			"rules":[{"id":"data-request","matched":false},{"id":"off-topic","matched":false}],
			"worktree_name":"acme-usage","intent_evidence":"x"}`,
		"workspace not a candidate": `{"kind":"accept","workspace":"ws_elsewhere","strength":"explicit",
			"rules":[{"id":"data-request","matched":true},{"id":"off-topic","matched":false}],
			"worktree_name":"acme-usage","intent_evidence":"x"}`,
		"bad worktree name": `{"kind":"accept","workspace":"ws_acme_data","strength":"explicit",
			"rules":[{"id":"data-request","matched":true},{"id":"off-topic","matched":false}],
			"worktree_name":"Acme Usage Report","intent_evidence":"x"}`,
		"no intent evidence": `{"kind":"accept","workspace":"ws_acme_data","strength":"explicit",
			"rules":[{"id":"data-request","matched":true},{"id":"off-topic","matched":false}],
			"worktree_name":"acme-usage"}`,
		"ask without question": `{"kind":"ask","rules":[]}`,
		"unknown kind":         `{"kind":"maybe"}`,
	}
	for name, reply := range cases {
		t.Run(name, func(t *testing.T) {
			a := newAssessor(t, &fakeGen{reply: reply})
			if _, err := a.Assess(context.Background(), input()); err == nil {
				t.Fatalf("expected the outcome to be refused:\n%s", reply)
			}
		})
	}
}

// A notice is a legitimate decision and needs no rules.
func TestIgnoreNeedsNoRules(t *testing.T) {
	a := newAssessor(t, &fakeGen{reply: `{"kind":"ignore","reason":"notification","reason_text":"发版公告"}`})
	out, err := a.Assess(context.Background(), input())
	if err != nil {
		t.Fatal(err)
	}
	if out.Kind != domain.DecIgnore || len(out.Reasons) == 0 || out.Reasons[0].Code != "notification" {
		t.Fatalf("got %+v", out)
	}
}

// Models wrap JSON in prose or fences; the parser must still find it.
func TestParsesJSONInsideProse(t *testing.T) {
	reply := "我判断如下：\n```json\n" + goodAccept + "\n```\n以上。"
	a := newAssessor(t, &fakeGen{reply: reply})
	if _, err := a.Assess(context.Background(), input()); err != nil {
		t.Fatal(err)
	}
}

// A model failure is an error, not a decision: the caller records unavailable.
func TestModelErrorIsNotADecision(t *testing.T) {
	a := newAssessor(t, &fakeGen{err: errors.New("boom")})
	if _, err := a.Assess(context.Background(), input()); err == nil {
		t.Fatal("a model failure must surface as an error")
	}
}

// With no candidates the prompt says so, so the model cannot accept into thin air.
func TestNoCandidates(t *testing.T) {
	gen := &fakeGen{reply: `{"kind":"ask","ask":"这条要派给哪个工作区？"}`}
	a := newAssessor(t, gen)
	in := input()
	in.Candidates, in.Briefs = nil, nil
	out, err := a.Assess(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if out.Ask == "" {
		t.Fatal("expected a question")
	}
	if !strings.Contains(gen.sawPrompt, "没有任何候选") {
		t.Fatal("the prompt should say there are no candidates")
	}
}

// The third prompt layer (the rules explanation) must reach the judge: it used to
// be shown in the editor and used nowhere.
func TestRulesNoteReachesTheJudge(t *testing.T) {
	gen := &fakeGen{reply: goodAccept}
	a, err := New(Config{
		Rules: rules(), Model: "m",
		RulesNote: "团队约定：客户数据的取数请求算 data-request；群发通知一律 off-topic。",
	}, gen)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Assess(context.Background(), input()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(gen.sawSystem, "团队约定") {
		t.Fatalf("the rules note never reached the prompt: %q", gen.sawSystem)
	}
	if !strings.Contains(gen.sawSystem, "客户数据的取数请求") {
		t.Fatalf("the note was truncated: %q", gen.sawSystem)
	}
}

// Without a note the prompt stays as it was: no stray heading.
func TestNoRulesNoteMeansNoSection(t *testing.T) {
	gen := &fakeGen{reply: goodAccept}
	a, _ := New(Config{Rules: rules(), Model: "m"}, gen)
	if _, err := a.Assess(context.Background(), input()); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(gen.sawSystem, "团队规则说明") {
		t.Fatalf("an empty note produced a section: %q", gen.sawSystem)
	}
}
