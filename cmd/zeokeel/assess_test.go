package main

// Prompt hot-update regression: the assessor must read the saved prompt at Eval
// time, not the value that existed when the process started (004 prompt layer,
// wave2-spec §3 提示词热更新).

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zeokeel/super-agent/internal/engine"
	"github.com/zeokeel/super-agent/internal/store"
)

type recordingGenerator struct {
	system string
	user   string
	reply  string
}

func (g *recordingGenerator) Generate(_ context.Context, system, user string) (string, error) {
	g.system, g.user = system, user
	if g.reply == "" {
		return `{"kind":"ignore","reason":"off-topic","reason_text":"not work"}`, nil
	}
	return g.reply, nil
}

func TestAssessorReadsTheSavedPromptPerEval(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "zeokeel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	gen := &recordingGenerator{}
	ha := &hotAssessor{gen: gen, store: st, model: "mock"}
	in := engine.AssessInput{}

	// First eval: the built-in router prompt, no override saved yet.
	if _, err := ha.Assess(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(gen.system, "自定义研判提示") {
		t.Fatal("an override was used before it was saved")
	}

	// Save an edited prompt; the next eval must use it without a restart.
	if _, err := st.SaveSettings(map[string]string{
		"prompt.router":     "自定义研判提示：只接明确请求。",
		"prompt.eval_rules": "规则说明：不许猜。",
	}, 0, false); err != nil {
		t.Fatal(err)
	}
	if _, err := ha.Assess(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(gen.system, "自定义研判提示") {
		t.Fatalf("the saved prompt was not used: %q", gen.system)
	}
	if !strings.Contains(gen.system, "规则说明") {
		t.Fatalf("the saved rules note was not used: %q", gen.system)
	}
}
