package mcp

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"unicode/utf8"
)

// longTextTool 返回 n 行文本，用来验证截断。
func longTextTool(name string, lines int) fakeTool {
	return fakeTool{
		Name: name,
		Desc: "returns many lines",
		Handler: func(context.Context, map[string]any) (string, error) {
			var b strings.Builder
			for i := 0; i < lines; i++ {
				fmt.Fprintf(&b, "line-%03d\n", i)
			}
			return strings.TrimSuffix(b.String(), "\n"), nil
		},
	}
}

func newLongToolManager(t *testing.T, lines int, cfg Config) *Manager {
	t.Helper()
	hub := newFakeHub(t)
	hub.addServer("s1", longTextTool("dump", lines))
	cfg.Servers = []ServerConfig{{Name: "s1", Command: "fake"}}
	m := newTestManager(t, hub, cfg)
	mustStart(t, m)
	t.Cleanup(func() { _ = m.Close() })
	return m
}

// TestTruncationSpillsFullOutput 覆盖：超长结果 → 只留前 N 行 + 落盘路径 + 全文可读回。
func TestTruncationSpillsFullOutput(t *testing.T) {
	dir := t.TempDir()
	m := newLongToolManager(t, 300, Config{MaxResultLines: 10, MaxResultBytes: 1 << 20, SpillDir: dir})

	out, err := toolsByName(t, m)["mcp__s1__dump"].InvokableRun(context.Background(), "{}")
	if err != nil {
		t.Fatalf("InvokableRun: %v", err)
	}
	if !strings.Contains(out, "结果已截断") || !strings.Contains(out, "原 300 行") {
		t.Fatalf("缺少截断提示: %q", out)
	}
	if !strings.Contains(out, "line-000") || !strings.Contains(out, "line-009") {
		t.Errorf("前 10 行应保留: %q", out)
	}
	if strings.Contains(out, "line-010") {
		t.Errorf("第 11 行不该出现: %q", out)
	}
	// 提示里必须给出可用的落盘路径，并且文件真的是完整内容。
	idx := strings.Index(out, "完整输出已落盘：")
	if idx < 0 {
		t.Fatalf("没有落盘路径: %q", out)
	}
	path := strings.TrimSpace(strings.Fields(out[idx+len("完整输出已落盘："):])[0])
	full, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取落盘文件 %q: %v", path, err)
	}
	if !strings.HasPrefix(path, dir) {
		t.Errorf("落盘路径不在 SpillDir 下: %q", path)
	}
	if got := strings.Count(string(full), "\n") + 1; got != 300 {
		t.Errorf("落盘内容应是完整 300 行，实际 %d", got)
	}
	if !strings.HasSuffix(string(full), "line-299") {
		t.Errorf("落盘内容尾部不对: %q", tail(string(full), 40))
	}
}

// TestTruncationWithoutSpillDir 覆盖：没有落盘目录时也要截断，但不能给出假路径。
func TestTruncationWithoutSpillDir(t *testing.T) {
	m := newLongToolManager(t, 50, Config{MaxResultLines: 5, MaxResultBytes: 1 << 20})
	out, err := toolsByName(t, m)["mcp__s1__dump"].InvokableRun(context.Background(), "{}")
	if err != nil {
		t.Fatalf("InvokableRun: %v", err)
	}
	if !strings.Contains(out, "结果已截断") {
		t.Fatalf("应截断: %q", out)
	}
	if strings.Contains(out, "完整输出已落盘") {
		t.Errorf("没有 SpillDir 时不该出现落盘提示: %q", out)
	}
}

// TestByteLimitKeepsUTF8Valid 覆盖：字节上限截断不能把中文字符劈成乱码。
func TestByteLimitKeepsUTF8Valid(t *testing.T) {
	text := strings.Repeat("中文输出很长，", 500) // 无换行的超长单行
	got := limitResult(text, 1000, 64, "", "", "")
	if !utf8.ValidString(got) {
		t.Fatal("截断后出现非法 UTF-8")
	}
	if !strings.Contains(got, "结果已截断") {
		t.Errorf("缺少截断提示: %q", got)
	}
}

// TestNoTruncationWithinBudget 覆盖：预算内必须原样返回（不做任何改写）。
func TestNoTruncationWithinBudget(t *testing.T) {
	text := "short\nresult"
	if got := limitResult(text, 10, 1024, "", "", ""); got != text {
		t.Errorf("预算内不该改写: %q", got)
	}
}

// TestCountLines 覆盖行数定义（末尾换行不算多一行）。
func TestCountLines(t *testing.T) {
	cases := map[string]int{"": 0, "a": 1, "a\nb": 2, "a\nb\n": 2, "\n": 1}
	for in, want := range cases {
		if got := countLines(in); got != want {
			t.Errorf("countLines(%q) = %d，期望 %d", in, got, want)
		}
	}
}

// TestSpillNamesAreUnique 覆盖：并发同名调用不能互相覆盖。
func TestSpillNamesAreUnique(t *testing.T) {
	dir := t.TempDir()
	p1, err := spill(dir, "srv", "tool", "one")
	if err != nil {
		t.Fatalf("spill: %v", err)
	}
	p2, err := spill(dir, "srv", "tool", "two")
	if err != nil {
		t.Fatalf("spill: %v", err)
	}
	if p1 == p2 {
		t.Fatalf("两次落盘路径相同: %q", p1)
	}
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
