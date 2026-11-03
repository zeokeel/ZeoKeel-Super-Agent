package im

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zeokeel/super-agent/internal/domain"
)

// step 是假 Poller 的一次应答。
type step struct {
	events []RawEvent
	next   string
	err    error
}

// scriptPoller 按脚本应答，并记录每次调用时的游标——用来证明"失败不推进"
// 与"交付后才推进"这两条契约。
type scriptPoller struct {
	mu     sync.Mutex
	steps  []step
	calls  []string
	called chan string
}

func (p *scriptPoller) Poll(_ context.Context, cursor string) ([]RawEvent, string, error) {
	p.mu.Lock()
	p.calls = append(p.calls, cursor)
	var current step
	if len(p.steps) > 0 {
		current = p.steps[0]
		p.steps = p.steps[1:]
	} else {
		current = step{next: cursor}
	}
	p.mu.Unlock()
	select {
	case p.called <- cursor:
	default:
	}
	if current.err != nil {
		return nil, cursor, current.err
	}
	return current.events, current.next, nil
}

func (p *scriptPoller) callList() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.calls...)
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// 游标必须在事件真正交给调用方之后才推进：out 没人读时不能继续拉下一批，
// 否则崩溃/取消会丢掉"已越过但没交付"的事件。
func TestSubscribeAdvancesCursorOnlyAfterDelivery(t *testing.T) {
	p := &scriptPoller{
		steps:  []step{{events: []RawEvent{{ID: "e1"}}, next: "p1"}},
		called: make(chan string, 8),
	}
	out := make(chan RawEvent) // 无缓冲：没人读就一直阻塞
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- SubscribeLongPoll(ctx, p, SubscribeOptions{Backoff: time.Millisecond, IdleDelay: time.Millisecond, MaxPages: 1}, out)
	}()

	select {
	case cursor := <-p.called:
		if cursor != "" {
			t.Fatalf("first poll cursor = %q, want empty", cursor)
		}
	case <-time.After(time.Second):
		t.Fatal("subscribe never polled")
	}
	// 事件还没被读走，循环必须停在发送上，不能再拉一次。
	time.Sleep(30 * time.Millisecond)
	if calls := p.callList(); len(calls) != 1 {
		t.Fatalf("polled %d times before delivery, want 1: %v", len(calls), calls)
	}

	select {
	case ev := <-out:
		if ev.ID != "e1" {
			t.Fatalf("event id = %q", ev.ID)
		}
	case <-time.After(time.Second):
		t.Fatal("no event delivered")
	}
	waitFor(t, "cursor to advance", func() bool {
		calls := p.callList()
		return len(calls) >= 2 && calls[1] == "p1"
	})
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("subscribe did not stop on cancel")
	}
}

// 一次失败不能推进游标：重试必须用轮次开始处的游标（宁可重读，不可丢消息）。
func TestSubscribeRetriesFromSameCursorOnFailure(t *testing.T) {
	p := &scriptPoller{
		steps:  []step{{err: context.DeadlineExceeded}, {}},
		called: make(chan string, 8),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out := make(chan RawEvent, 1)
	go func() {
		_ = SubscribeLongPoll(ctx, p, SubscribeOptions{Backoff: time.Millisecond, IdleDelay: time.Millisecond, MaxPages: 1}, out)
	}()
	waitFor(t, "two polls", func() bool { return len(p.callList()) >= 2 })
	calls := p.callList()
	if calls[0] != "" || calls[1] != "" {
		t.Fatalf("retry cursor = %v, want both empty (no advance on failure)", calls)
	}
}

// 连续失败到阈值（3 次）才把连接标红：一次抖动不该在面板上报警。
func TestSubscribeHealthReportsAfterThreeFailures(t *testing.T) {
	p := &scriptPoller{
		steps:  []step{{err: context.DeadlineExceeded}, {err: context.DeadlineExceeded}, {err: context.DeadlineExceeded}, {err: context.DeadlineExceeded}},
		called: make(chan string, 8),
	}
	health := NewHealthTracker()
	ctx, cancel := context.WithCancel(context.Background())
	out := make(chan RawEvent, 1)
	go func() {
		_ = SubscribeLongPoll(ctx, p, SubscribeOptions{Backoff: time.Millisecond, IdleDelay: time.Millisecond, MaxPages: 1, Health: health}, out)
	}()
	waitFor(t, "health error", func() bool { return health.Snapshot().State == HealthError })
	if detail := health.Snapshot().Detail; !strings.Contains(detail, "3") {
		t.Fatalf("health detail = %q, want failure count", detail)
	}
	cancel()
}

// 一轮中途失败时，整轮丢弃并回到轮次开始的游标。
func TestPollOnceResetsCursorWhenRoundFails(t *testing.T) {
	p := &scriptPoller{steps: []step{{events: []RawEvent{{ID: "e1"}}, next: "p1"}, {err: context.DeadlineExceeded}}, called: make(chan string, 8)}
	events, next, err := PollOnce(context.Background(), p, "", 2)
	if err == nil {
		t.Fatal("expected error")
	}
	if len(events) != 0 {
		t.Fatalf("failed round returned %d events, want none", len(events))
	}
	if next != "" {
		t.Fatalf("next = %q, want round-start cursor", next)
	}
}

// 三条 @ 事实的边界：@所有人/别人都不算 @ 了 agent，单聊才是 Direct，
// ReplyTo 只看"回复的是不是 agent 自己的消息"。
func TestAddressedForRules(t *testing.T) {
	self := "ou_bot"
	cases := []struct {
		name string
		ev   RawEvent
		want domain.Addressed
	}{
		{
			name: "self mention in group",
			ev:   RawEvent{Conv: domain.Conversation{Kind: domain.ConvGroup}, Mentions: []string{"ou_alice", self}},
			want: domain.Addressed{Mention: true},
		},
		{
			name: "other people mentioned only",
			ev:   RawEvent{Conv: domain.Conversation{Kind: domain.ConvGroup}, Mentions: []string{"ou_alice"}},
			want: domain.Addressed{},
		},
		{
			name: "everyone mention is empty id and never matches",
			ev:   RawEvent{Conv: domain.Conversation{Kind: domain.ConvGroup}, Mentions: []string{""}},
			want: domain.Addressed{},
		},
		{
			name: "direct message is direct",
			ev:   RawEvent{Conv: domain.Conversation{Kind: domain.ConvDirect}},
			want: domain.Addressed{Direct: true},
		},
		{
			name: "reply to another person's message is not a reply fact",
			ev:   RawEvent{Conv: domain.Conversation{Kind: domain.ConvGroup}, ReplyTo: "om_x"},
			want: domain.Addressed{},
		},
		{
			name: "reply to the bot's own message",
			ev:   RawEvent{Conv: domain.Conversation{Kind: domain.ConvGroup}, ReplyTo: "om_own", ReplyToBot: true},
			want: domain.Addressed{ReplyTo: true},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := AddressedFor(tc.ev, self); got != tc.want {
				t.Fatalf("AddressedFor = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// 空 selfID 时任何 id 都不算命中：避免把 "空 vs 空" 误当成 @ 了自己。
func TestMentionedNeedsNonEmptySelf(t *testing.T) {
	ev := RawEvent{Mentions: []string{""}}
	if Mentioned(ev, "") {
		t.Fatal("empty self id must never match")
	}
}

// 幂等键必须由内容稳定派生，且不同输入不同键。
func TestDeriveKeyIsStableAndDistinct(t *testing.T) {
	a := DeriveKey("feishu", "oc_1", "hi")
	if a != DeriveKey("feishu", "oc_1", "hi") {
		t.Fatal("same input must derive the same key")
	}
	if a == DeriveKey("feishu", "oc_1", "hi!") || a == DeriveKey("feishu", "oc_2", "hi") {
		t.Fatal("different input must derive a different key")
	}
	if len(a) != 32 {
		t.Fatalf("key length = %d, want 32", len(a))
	}
}

// 截断按 rune：中文不能被切成非法 UTF-8，也不能超过上限。
func TestTruncateKeepsValidUTF8(t *testing.T) {
	text := strings.Repeat("中", 10)
	got := Truncate(text, 5)
	if len([]rune(got)) > 5 {
		t.Fatalf("truncated to %d runes", len([]rune(got)))
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("truncated text = %q, want ellipsis", got)
	}
	if Truncate("short", 10) != "short" {
		t.Fatal("text under the limit must be unchanged")
	}
}
