package main

import (
	"context"

	"github.com/zeokeel/super-agent/internal/assess"
	"github.com/zeokeel/super-agent/internal/engine"
	"github.com/zeokeel/super-agent/internal/store"
)

// defaultRules are the checks Eval must evaluate for every candidate. They live
// in code until the engine's team rules (Wave2 engine API) reach the runtime.
func defaultRules() []assess.Rule {
	return []assess.Rule{
		{ID: "implementation", Instruction: "明确要求修改这个仓库的代码或配置", Enabled: true},
		{ID: "data-request", Instruction: "要求从这个工作区取数或出报表", Enabled: true},
		{ID: "off-topic", Instruction: "与这个工作区无关的闲聊、通知或公告", Enabled: true},
	}
}

// newAssessor picks the semantic layer of Eval. The prompts are NOT snapshotted
// here: the panel's editor writes prompt.router / prompt.eval_rules to the store,
// and every Assess call re-reads them, so "热更新" is true rather than a claim
// (004 prompt layering, fix-docs-ci handoff §2.3).
func newAssessor(model, baseURL, apiKey string, st *store.Store) engine.Assessor {
	fallback := engine.RuleAssessor{Notices: []string{"今晚 8 点发版，注意别推代码"}}
	if model == "" || baseURL == "" {
		return fallback
	}
	gen, err := assess.NewEinoGenerator(context.Background(), assess.EinoConfig{
		BaseURL: baseURL, APIKey: apiKey, Model: model,
	})
	if err != nil {
		return fallback
	}
	return &hotAssessor{gen: gen, store: st, model: model}
}

// hotAssessor rebuilds the (cheap) assess.Config from the saved settings on every
// Eval. The expensive part — the model client — is built once.
type hotAssessor struct {
	gen   assess.Generator
	store *store.Store
	model string
}

func (h *hotAssessor) Assess(ctx context.Context, in engine.AssessInput) (engine.AssessOutput, error) {
	cfg := assess.Config{Model: h.model, Rules: defaultRules()}
	if h.store != nil {
		if saved, err := h.store.Settings(); err == nil {
			cfg.SystemPrompt = saved["prompt.router"]
			cfg.RulesNote = saved["prompt.eval_rules"]
		}
	}
	a, err := assess.New(cfg, h.gen)
	if err != nil {
		return engine.AssessOutput{}, err
	}
	return a.Assess(ctx, in)
}
