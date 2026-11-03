package agent

import (
	"context"
	"fmt"
	"testing"
)

// 上下文超预算时，官方 summarization 中间件要把早期消息摘要成一条 compact 消息，
// 并把"压缩发生了"上报面板、写进事件日志（design §5.4）。
func TestCompactionOverMessageBudget(t *testing.T) {
	srv := startMock(t)
	r := newTestRunner(t, testConfig(srv, t.TempDir(), "mock-text", func(c *Config) {
		// 用消息条数做确定性触发：mock 的 token 估算不稳定，条数上限是可复现的。
		c.CompactAfterMessages = 4
	}))

	ctx := context.Background()
	var compacts []CompactInfo
	var sid string
	for i := 0; i < 3; i++ {
		res, err := r.RunSession(ctx, RunRequest{
			SessionID: sid,
			System:    "你是测试 worker",
			Task:      fmt.Sprintf("第 %d 轮：请继续分析这个工作区", i),
		}, func(e Event) {
			if e.Kind == EventCompact && e.Compact != nil {
				compacts = append(compacts, *e.Compact)
			}
		})
		if err != nil {
			t.Fatalf("turn %d: %v", i, err)
		}
		sid = res.SessionID
		if i == 2 && res.Compactions == 0 {
			t.Fatal("third turn should have compacted the early history")
		}
	}
	if len(compacts) == 0 {
		t.Fatal("no compact event reached the panel")
	}
	for _, c := range compacts {
		if c.MessagesBefore <= c.MessagesAfter {
			t.Fatalf("compaction did not shrink history: %+v", c)
		}
		if c.Summary == "" {
			t.Fatalf("compact event has no summary preview: %+v", c)
		}
	}

	stats := sessionStatsForTest(t, r, sid)
	if stats.Compactions == 0 {
		t.Fatal("compaction was not recorded in the session event log")
	}
	if stats.Messages == 0 {
		t.Fatal("session stats lost the message count")
	}
}

// 预算足够大时不应该压缩：压缩是成本，不是每轮都做的事。
func TestNoCompactionUnderBudget(t *testing.T) {
	srv := startMock(t)
	r := newTestRunner(t, testConfig(srv, t.TempDir(), "mock-text", func(c *Config) {
		c.CompactAfterMessages = 0
		c.ContextBudget = 1 << 20
	}))
	var compacts int
	if _, err := r.RunSession(context.Background(), RunRequest{System: "sys", Task: "hello"}, func(e Event) {
		if e.Kind == EventCompact {
			compacts++
		}
	}); err != nil {
		t.Fatal(err)
	}
	if compacts != 0 {
		t.Fatalf("compacted without exceeding the budget: %d events", compacts)
	}
}
