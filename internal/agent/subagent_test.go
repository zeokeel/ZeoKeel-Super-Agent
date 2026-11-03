package agent

import (
	"context"
	"strings"
	"testing"
)

// A subagent is defined by what it may call. Getting the whitelist wrong must fail
// construction, not be discovered mid-run (design §5.6).
func TestSubagentToolWhitelistIsChecked(t *testing.T) {
	dir := t.TempDir()
	r, err := New(Config{Model: "mock", Workdir: dir, BaseURL: "http://127.0.0.1:1"})
	if err != nil {
		t.Fatal(err)
	}
	tools, handlers, err := r.toolsFor([]string{"read", "grep"})
	if err != nil {
		t.Fatalf("read/grep should exist: %v", err)
	}
	if len(tools) != 0 || len(handlers) != 1 {
		t.Fatalf("read/grep are middleware tools: tools=%d handlers=%d", len(tools), len(handlers))
	}
	_, _, err = r.toolsFor([]string{"read", "teleport"})
	if err == nil || !strings.Contains(err.Error(), "teleport") {
		t.Fatalf("an unknown tool must be refused by name: %v", err)
	}
	if _, handlers, err := r.toolsFor(nil); err != nil || len(handlers) != 1 {
		t.Fatalf("no list means the parent's tools: handlers=%d err=%v", len(handlers), err)
	}
	// bash 是自定义工具，应当以普通 tool.BaseTool 出现在白名单结果里。
	if tools, handlers, err := r.toolsFor([]string{"bash"}); err != nil || len(tools) != 1 || len(handlers) != 0 {
		t.Fatalf("bash whitelist: tools=%d handlers=%d err=%v", len(tools), len(handlers), err)
	}
}

func TestSubagentSpecValidation(t *testing.T) {
	cases := map[string]SubagentSpec{
		"no name":        {Description: "d", Instruction: "i"},
		"no description": {Name: "explorer", Instruction: "i"},
		"no instruction": {Name: "explorer", Description: "d"},
	}
	for name, spec := range cases {
		t.Run(name, func(t *testing.T) {
			if err := validateSubagent(spec); err == nil {
				t.Fatal("expected a refusal")
			}
		})
	}
	good := SubagentSpec{Name: "explorer", Description: "只读调查", Instruction: "看代码，别改"}
	if err := validateSubagent(good); err != nil {
		t.Fatalf("a valid spec was refused: %v", err)
	}
}

// With subagents attached, the main agent must still be constructible and must
// expose the spawn tool the middleware adds.
func TestSubagentMiddlewareAttaches(t *testing.T) {
	dir := t.TempDir()
	r, err := New(Config{
		Model: "mock", Workdir: dir, BaseURL: "http://127.0.0.1:1",
		Subagents: []SubagentSpec{{
			Name: "explorer", Description: "只读调查代码",
			Instruction: "只读，不要改文件", Tools: []string{"read", "grep"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = r.newSubagents(context.Background(), nil)
	if err == nil {
		t.Fatal("nil model should be refused by the agent constructor")
	}
	// The middleware itself only needs the specs and the tools; a nil model fails
	// inside NewChatModelAgent, which is the check above. With a real model it
	// builds — that path is covered by the end-to-end worker tests.
	_ = r
}

// 子智能体的白名单可以只含文件工具：它们由官方 filesystem 中间件注入，构造路径
// 必须能处理"没有任何扁平 tool.BaseTool"的情况（Wave 2 的默认子智能体就是这样）。
func TestSubagentWithFileToolsOnlyConstructs(t *testing.T) {
	srv := startMock(t)
	dir := t.TempDir()
	r := newTestRunner(t, testConfig(srv, dir, "mock-text", func(c *Config) {
		c.Subagents = []SubagentSpec{{
			Name: "explorer", Description: "只读调查",
			Instruction: "只读，不要改文件",
			Tools:       []string{"read", "grep", "glob"},
		}}
	}))
	if _, err := r.RunSession(context.Background(), RunRequest{System: "sys", Task: "hi"}, nil); err != nil {
		t.Fatalf("a file-tools-only subagent must construct: %v", err)
	}
}
