// Package assess 是 Eval 的语义层：把一批候选 Trigger 交给模型，换回一个可校验
// 的 Decision。它只负责"判断"，不碰状态——授权、幂等与状态迁移仍在 Engine 里
// （design §4.3：gate 是确定性的，assessment 才是模型）。
package assess

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/zeokeel/super-agent/internal/domain"
	"github.com/zeokeel/super-agent/internal/engine"
	"github.com/zeokeel/super-agent/internal/prompts"
)

// Rule 是一条可被评估的团队规则。模型必须对每条 enabled 规则给出结论，少一条就
// 判为无效输出——否则"命中规则"会变成模型随口一说（design §4.3）。
type Rule struct {
	ID          string
	Instruction string
	Enabled     bool
}

// Mode 是接受模式：observe 只记录"本来会做"，auto 才真的开单。
type Mode string

const (
	ModeOff     Mode = "off"
	ModeObserve Mode = "observe"
	ModeAuto    Mode = "auto"
)

// Config 是一次研判的配置。
type Config struct {
	// Rules 是本轮要评估的规则；模型必须逐条回答。
	Rules []Rule
	// Model 是输出 JSON 的模型名。
	Model string
	// Timeout 是单次研判的上限。
	Timeout time.Duration
	// WorktreePattern 校验模型给出的工单短名。
	WorktreePattern *regexp.Regexp
	// SystemPrompt overrides the built-in router prompt. The panel's prompt editor
	// writes it, so an edit there has to reach the model or the editor is theatre
	// (design §5.3). Empty means the built-in default.
	SystemPrompt string
	// RulesNote is the team's explanation of the rules, from the panel's third
	// prompt layer. It is appended to the router prompt: the codes are compiled in,
	// but the wording of "when does a rule apply" is a team artifact.
	RulesNote string
}

// Generator 是一次"给提示词、拿文本"的调用。抽出来是为了让测试不必依赖模型，
// 也让 provider 可以换（design §5.1：provider 是可替换层）。
type Generator interface {
	Generate(ctx context.Context, system, user string) (string, error)
}

// Assessor 实现 engine.Assessor。
type Assessor struct {
	cfg Config
	gen Generator
	now func() time.Time
}

// New 构造一个模型驱动的研判器。
func New(cfg Config, gen Generator) (*Assessor, error) {
	if gen == nil {
		return nil, errors.New("assess: a generator is required")
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 90 * time.Second
	}
	if cfg.WorktreePattern == nil {
		// 1–4 个小写 kebab 词，≤32 字符（design §4.3）。
		cfg.WorktreePattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+){0,3}$`)
	}
	return &Assessor{cfg: cfg, gen: gen, now: time.Now}, nil
}

// verdict 是模型返回的结构化判断。字段是校验的输入，不是真理：校验不过就作废。
type verdict struct {
	Kind string `json:"kind"` // accept | stop | ignore | ask
	// Workspace 是目标工作区 id；ask 时可以为空。
	Workspace string `json:"workspace"`
	// Reason 是 code；ReasonText 是给人看的一句话。
	Reason     string `json:"reason"`
	ReasonText string `json:"reason_text"`
	// Ask 在 kind=ask 时给出要问人的问题。
	Ask string `json:"ask"`
	// Rules 必须覆盖每条 enabled 规则。
	Rules []ruleVerdict `json:"rules"`
	// Title 是工单标题（accept 时给出）。
	Title string `json:"title"`
	// WorktreeName 是工单短名，accept 时必填。
	WorktreeName string `json:"worktree_name"`
	// IntentEvidence 是"这是一条请求"的证据，accept 时不能为空。
	IntentEvidence string `json:"intent_evidence"`
}

type ruleVerdict struct {
	ID      string `json:"id"`
	Matched bool   `json:"matched"`
	Note    string `json:"note"`
}

// Assess 实现 engine.Assessor。
func (a *Assessor) Assess(ctx context.Context, in engine.AssessInput) (engine.AssessOutput, error) {
	prompt := buildPrompt(a.cfg, in)
	ctx, cancel := context.WithTimeout(ctx, a.cfg.Timeout)
	defer cancel()

	sys := prompts.RouterPrompt()
	if strings.TrimSpace(a.cfg.SystemPrompt) != "" {
		sys = a.cfg.SystemPrompt
	}
	// The gate owns a Trigger's strength; the judge must not claim one (002:11-16).
	sys += "\n\n## 授权边界\n强度（explicit/inferred）由确定性 gate 决定，你不得声明强度；你只回答\"这条消息要什么、派给哪个工作区\"。"
	// The rules note has to reach the prompt or the third editor layer is a text
	// box that changes nothing (the same failure as the worker prompt).
	if note := strings.TrimSpace(a.cfg.RulesNote); note != "" {
		sys += "\n\n## 团队规则说明\n" + note
	}
	raw, err := a.gen.Generate(ctx, sys, prompt)
	if err != nil {
		// 模型不可用不是"忽略"：调用方据此排队重试并最终记 unavailable
		// （design §4.3）。
		return engine.AssessOutput{}, fmt.Errorf("assess: model: %w", err)
	}
	v, err := parseVerdict(raw)
	if err != nil {
		return engine.AssessOutput{}, err
	}
	if err := a.validate(v, in); err != nil {
		return engine.AssessOutput{}, err
	}
	return toOutput(v), nil
}

// validate 是 assessmentOutcome 校验的落点。宁可整批判为无效，也不接受一个
// "看起来对"的判断——无人值守时错误的接受比漏接受更贵（design §4.3）。
func (a *Assessor) validate(v *verdict, in engine.AssessInput) error {
	switch v.Kind {
	case "accept":
	case "ask":
		if strings.TrimSpace(v.Ask) == "" {
			return errors.New("assess: ask without a question")
		}
		return nil
	case "ignore", "stop":
		return nil
	default:
		return fmt.Errorf("assess: unknown kind %q", v.Kind)
	}

	// accept 的四个前置条件。
	if v.Workspace == "" {
		return errors.New("assess: accept without a workspace")
	}
	known := false
	for _, c := range in.Candidates {
		if string(c.ID) == v.Workspace {
			known = true
			break
		}
	}
	if !known {
		return fmt.Errorf("assess: accept for %q, which is not a candidate", v.Workspace)
	}
	if strings.TrimSpace(v.IntentEvidence) == "" {
		return errors.New("assess: accept without intent evidence")
	}
	if !a.cfg.WorktreePattern.MatchString(v.WorktreeName) {
		return fmt.Errorf("assess: worktree name %q is not 1-4 lowercase kebab words", v.WorktreeName)
	}
	// 每条 enabled 规则必须恰好评估一次；规则来自 Eval 的 Policy（002:101）。
	want := map[string]bool{}
	for _, r := range ruleSet(a.cfg, in) {
		if r.Enabled {
			want[r.ID] = true
		}
	}
	got := map[string]int{}
	matched := 0
	for _, rv := range v.Rules {
		if _, ok := want[rv.ID]; !ok {
			return fmt.Errorf("assess: rule %q is not enabled", rv.ID)
		}
		got[rv.ID]++
		if rv.Matched {
			matched++
		}
	}
	for id := range want {
		if got[id] != 1 {
			return fmt.Errorf("assess: rule %q evaluated %d times", id, got[id])
		}
	}
	if matched == 0 {
		return errors.New("assess: accept matched no enabled rule")
	}
	return nil
}

// ruleSet is the rule list the assessment must judge: the Eval's Policy when it
// carries one, the configured fallback otherwise.
func ruleSet(cfg Config, in engine.AssessInput) []Rule {
	if len(in.Policy.Rules) > 0 {
		out := make([]Rule, 0, len(in.Policy.Rules))
		for _, r := range in.Policy.Rules {
			out = append(out, Rule{ID: r.ID, Instruction: r.Instruction, Enabled: r.Enabled})
		}
		return out
	}
	return cfg.Rules
}

// toOutput 把校验过的判断翻译成 Engine 的决定。
func toOutput(v *verdict) engine.AssessOutput {
	out := engine.AssessOutput{
		Workspace: domain.WorkspaceID(v.Workspace),
		Title:     v.Title,
	}
	switch v.Kind {
	case "ask":
		out.Ask = v.Ask
		return out
	case "accept":
		out.Kind = domain.DecAccept
	case "stop":
		out.Kind = domain.DecStop
	default:
		out.Kind = domain.DecIgnore
	}
	reason := v.Reason
	if reason == "" {
		reason = "said"
	}
	out.Reasons = []domain.Reason{{Code: reason,
		Values: map[string]string{"why": v.ReasonText}}}
	return out
}

// parseVerdict 读取模型给出的 JSON。模型常把 JSON 包在代码块或解说里，所以先
// 抽出最外层的对象再解析，而不是要求输出绝对干净。
func parseVerdict(raw string) (*verdict, error) {
	body := extractJSON(raw)
	if body == "" {
		return nil, fmt.Errorf("assess: no JSON object in the reply")
	}
	var v verdict
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		return nil, fmt.Errorf("assess: parse reply: %w", err)
	}
	return &v, nil
}

func extractJSON(s string) string {
	if i := strings.IndexByte(s, '{'); i >= 0 {
		depth := 0
		for j := i; j < len(s); j++ {
			switch s[j] {
			case '{':
				depth++
			case '}':
				depth--
				if depth == 0 {
					return s[i : j+1]
				}
			}
		}
	}
	return ""
}
