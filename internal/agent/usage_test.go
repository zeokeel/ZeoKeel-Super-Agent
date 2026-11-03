package agent

import (
	"context"
	"testing"

	"github.com/cloudwego/eino/components/tool"
)

// 用量必须从 eino 的模型回调里拿到并带进事件：面板要显示"本 Ticket 已用 X tokens"
// （design §5.4）。一次工具闭环是两次模型调用，所以应该收到两条用量事件。
func TestUsageEventsAndSessionStats(t *testing.T) {
	srv := startMock(t)
	r := newTestRunner(t, testConfig(srv, t.TempDir(), "mock-tool",
		func(c *Config) { c.ExtraTools = []tool.BaseTool{echoTool()} }))

	var usages []Usage
	res, err := r.RunSession(context.Background(), RunRequest{System: "sys", Task: "echo hi"}, func(e Event) {
		if e.Kind == EventUsage && e.Usage != nil {
			usages = append(usages, *e.Usage)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(usages) != 2 {
		t.Fatalf("want 2 usage events (one per model call), got %d: %+v", len(usages), usages)
	}
	for _, u := range usages {
		if u.PromptTokens != 42 || u.CompletionTokens != 17 {
			t.Fatalf("unexpected usage %+v; mock reports prompt=42 completion=17", u)
		}
	}
	if res.Usage.TotalTokens != 118 || res.Usage.PromptTokens != 84 || res.Usage.CompletionTokens != 34 {
		t.Fatalf("result usage = %+v, want prompt=84 completion=34 total=118", res.Usage)
	}

	stats := sessionStatsForTest(t, r, res.SessionID)
	if stats.Turns != 1 || !stats.Idle {
		t.Fatalf("stats turns=%d idle=%v", stats.Turns, stats.Idle)
	}
	if stats.TotalTokens != 118 || stats.PromptTokens != 84 || stats.CompletionTokens != 34 {
		t.Fatalf("stats usage = %+v, want the same totals as the events", stats)
	}
	if stats.Messages < 3 {
		t.Fatalf("stats.messages = %d, want at least user+assistant+tool+assistant", stats.Messages)
	}
}
