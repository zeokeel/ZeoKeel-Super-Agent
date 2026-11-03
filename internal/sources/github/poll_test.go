package github

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/zeokeel/super-agent/internal/domain"
	"github.com/zeokeel/super-agent/internal/sources"
)

func TestPollReceivesAddressedCommentAndAdvancesCursor(t *testing.T) {
	cmd := &fakeCommand{respond: pollRouter(t,
		notificationsJSON("acme/webapp", 12),
		commentsArray(t, commentElement(t, 501, "alice", "@zeokeel-agent 请修一下", 12)),
	)}
	src := testSource(t, cmd, nil)
	var collector receiveCollector

	res, err := src.Poll(context.Background(), sources.PollRequest{}, collector.receive)
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if res.Received != 1 || res.Filtered != 0 || res.Unreadable != 0 {
		t.Fatalf("PollResult = %+v, want one received", res)
	}
	if len(collector.got) != 1 {
		t.Fatalf("receive called %d times, want 1", len(collector.got))
	}
	if collector.triggers[0].Threads[0].ID != "acme/webapp#12" {
		t.Fatalf("received Thread = %+v", collector.triggers[0].Threads)
	}

	// 第一次轮询从现在开始：通知的 since 是 testNow，而不是零值/很久以前。
	firstPoll := cmd.paths()[0]
	if want := url.QueryEscape(testNow.Format(time.RFC3339)); !strings.Contains(firstPoll, "since="+want) {
		t.Fatalf("first poll since = %q, want %q", firstPoll, want)
	}
	// 游标只在本轮真的有"更新的条目"时前进：最新评论的时间戳等于本轮起点，
	// 回退 5 秒后并不比 since 更晚，所以游标留在起点（原型 readStream 的
	// moved > since 语义）。这样下一轮可以复用 ETag 换 304。
	if got, want := cursorSince(t, res.Next), testNow; !got.Equal(want) {
		t.Fatalf("Next cursor since = %v, want %v", got, want)
	}
}

func TestPollFiltersUnaddressedCommentByDefault(t *testing.T) {
	cmd := &fakeCommand{respond: pollRouter(t,
		notificationsJSON("acme/webapp", 12),
		commentsArray(t, commentElement(t, 502, "alice", "这个接口我明天改", 12)),
	)}
	src := testSource(t, cmd, nil)
	var collector receiveCollector

	res, err := src.Poll(context.Background(), sources.PollRequest{}, collector.receive)
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if res.Received != 0 || res.Filtered != 1 {
		t.Fatalf("PollResult = %+v, want the unaddressed comment filtered at the Source", res)
	}
	if len(collector.got) != 0 {
		t.Fatalf("unaddressed comment reached receive: %v", collector.got)
	}
}

func TestPollReceivesUnaddressedCommentWhenObserving(t *testing.T) {
	cmd := &fakeCommand{respond: pollRouter(t,
		"[]",
		commentsArray(t, commentElement(t, 503, "alice", "这个接口我明天改", 12)),
	)}
	src := testSource(t, cmd, func(cfg *Config) { cfg.ObserveComments = true })
	var collector receiveCollector

	res, err := src.Poll(context.Background(), sources.PollRequest{}, collector.receive)
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if res.Received != 1 || res.Filtered != 0 {
		t.Fatalf("PollResult = %+v, want the unaddressed comment received in observe mode", res)
	}
	if collector.triggers[0].Addressed.AddressesAgent() {
		t.Fatal("observed comment should stay unaddressed as a fact; the admission table decides")
	}
	if scope := src.Scope(collector.triggers[0]); !scope.Listened || !scope.Observed {
		t.Fatalf("Scope = %+v, want listened+observed", scope)
	}
}

func TestPollSkipsAgentOwnComment(t *testing.T) {
	cmd := &fakeCommand{respond: pollRouter(t,
		notificationsJSON("acme/webapp", 12),
		commentsArray(t, commentElement(t, 504, "zeokeel-agent", "@zeokeel-agent 自己回复自己", 12)),
	)}
	src := testSource(t, cmd, nil)
	var collector receiveCollector

	res, err := src.Poll(context.Background(), sources.PollRequest{}, collector.receive)
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if res.Received != 0 || res.Filtered != 1 {
		t.Fatalf("PollResult = %+v, want the agent's own comment filtered", res)
	}
}

func TestPollSkipsEmptyComment(t *testing.T) {
	cmd := &fakeCommand{respond: pollRouter(t,
		notificationsJSON("acme/webapp", 12),
		commentsArray(t, commentElement(t, 505, "alice", "   ", 12)),
	)}
	src := testSource(t, cmd, nil)
	var collector receiveCollector

	res, err := src.Poll(context.Background(), sources.PollRequest{}, collector.receive)
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if res.Received != 0 || res.Filtered != 1 {
		t.Fatalf("PollResult = %+v, want an empty body filtered", res)
	}
}

func TestPollReceivesBotCommentForPolicy(t *testing.T) {
	// Source 不替 Policy 决定机器人能不能发起工作：它只把 Human=false 的事实
	// 交出去，bot list 在 Gate 里查（§4.9）。
	cmd := &fakeCommand{respond: pollRouter(t,
		notificationsJSON("acme/webapp", 12),
		commentsArray(t, commentElementAt(t, 506, "ci-robot[bot]", "@zeokeel-agent 构建挂了", 12, "2026-01-02T03:04:05Z", "Bot")),
	)}
	src := testSource(t, cmd, nil)
	var collector receiveCollector

	res, err := src.Poll(context.Background(), sources.PollRequest{}, collector.receive)
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if res.Received != 1 {
		t.Fatalf("PollResult = %+v, want the bot comment delivered as a fact", res)
	}
	if collector.triggers[0].Human != false || !collector.triggers[0].Actor.Bot {
		t.Fatalf("bot trigger facts = %+v", collector.triggers[0])
	}
}

func TestPollSkipsUnreadableItemsOneByOne(t *testing.T) {
	cmd := &fakeCommand{respond: pollRouter(t,
		notificationsJSON("acme/webapp", 12),
		commentsArray(t,
			commentElement(t, 601, "alice", "@zeokeel-agent one", 12),
			json.RawMessage(`"this is not a comment object"`),
			json.RawMessage(`{"id":0}`), // 解出来是空评论，Translate 拒绝
			commentElement(t, 602, "bob", "@zeokeel-agent two", 12),
		),
	)}
	src := testSource(t, cmd, nil)
	var collector receiveCollector

	res, err := src.Poll(context.Background(), sources.PollRequest{}, collector.receive)
	if err != nil {
		t.Fatalf("one bad item must not fail the batch: %v", err)
	}
	if res.Received != 2 {
		t.Fatalf("Received = %d, want the two good comments", res.Received)
	}
	if res.Unreadable != 2 {
		t.Fatalf("Unreadable = %d, want 2 (bad JSON + missing id)", res.Unreadable)
	}
	if len(collector.got) != 2 {
		t.Fatalf("receive called %d times", len(collector.got))
	}
	// 有坏 item 也不影响游标推进：好的都处理完了。
	if res.Next == "" {
		t.Fatal("cursor did not advance after a round with skipped items")
	}
}

func TestPollCursorStaysWhenBatchIsUnreadable(t *testing.T) {
	cursor := encodeCursor("acme/webapp", testNow.Add(-time.Hour))
	cmd := &fakeCommand{respond: pollRouter(t, notificationsJSON("acme/webapp", 12), "not json at all")}
	src := testSource(t, cmd, nil)
	var collector receiveCollector

	res, err := src.Poll(context.Background(), sources.PollRequest{Cursor: cursor}, collector.receive)
	if err == nil {
		t.Fatal("a whole unreadable comments response must fail the round")
	}
	if res.Next != cursor {
		t.Fatalf("Next = %q, want the unchanged input cursor %q", res.Next, cursor)
	}
}

func TestPollCursorStaysWhenNotificationReadFails(t *testing.T) {
	cursor := encodeCursor("acme/webapp", testNow.Add(-time.Hour))
	cmd := &fakeCommand{respond: func(args []string) ([]byte, error) {
		return nil, errors.New("gh exploded")
	}}
	src := testSource(t, cmd, nil)
	var collector receiveCollector

	res, err := src.Poll(context.Background(), sources.PollRequest{Cursor: cursor}, collector.receive)
	if err == nil {
		t.Fatal("a failed notification read must fail the round")
	}
	if res.Next != cursor {
		t.Fatalf("Next = %q, want the unchanged input cursor %q", res.Next, cursor)
	}
}

func TestPollCursorOnlyAdvancesAfterEveryReceiveSucceeds(t *testing.T) {
	cursor := encodeCursor("acme/webapp", testNow.Add(-time.Hour))
	cmd := &fakeCommand{respond: pollRouter(t,
		notificationsJSON("acme/webapp", 12),
		commentsArray(t,
			commentElement(t, 701, "alice", "@zeokeel-agent one", 12),
			commentElement(t, 702, "bob", "@zeokeel-agent two", 12),
		),
	)}
	src := testSource(t, cmd, nil)

	// 第二条 receive 失败：整轮失败，游标绝不能动。
	var failing receiveCollector
	failing.failOnCall = 2
	res, err := src.Poll(context.Background(), sources.PollRequest{Cursor: cursor}, failing.receive)
	if err == nil {
		t.Fatal("Poll succeeded although a receive failed")
	}
	if res.Next != cursor {
		t.Fatalf("Next = %q, want the unchanged cursor %q", res.Next, cursor)
	}
	if res.Received != 1 {
		t.Fatalf("Received = %d, want 1 before the failure", res.Received)
	}

	// 重试同一批：两条都成功，游标才推进。第一条会重复，靠内容 ID 去重。
	var retry receiveCollector
	res2, err := src.Poll(context.Background(), sources.PollRequest{Cursor: cursor}, retry.receive)
	if err != nil {
		t.Fatalf("retry Poll: %v", err)
	}
	if len(retry.got) != 2 {
		t.Fatalf("retry received %d triggers, want the whole batch replayed", len(retry.got))
	}
	if res2.Next == cursor {
		t.Fatal("cursor did not advance after a fully successful retry")
	}
	if got, want := cursorSince(t, res2.Next), testNow.Add(-cursorSlack); !got.Equal(want) {
		t.Fatalf("Next cursor since = %v, want %v", got, want)
	}
}

func TestPollReadsSubscribedThreadsWithoutNotification(t *testing.T) {
	cmd := &fakeCommand{respond: pollRouter(t,
		"[]",
		commentsArray(t,
			commentElement(t, 801, "alice", "@zeokeel-agent 在订阅的 Thread 里", 12),
			commentElement(t, 802, "bob", "@zeokeel-agent 没有订阅的 Thread", 99),
		),
	)}
	src := testSource(t, cmd, nil)
	var collector receiveCollector

	req := sources.PollRequest{Threads: []domain.ThreadRef{
		{Source: "github", ID: "acme/webapp#12"},
		{Source: "feishu", ID: "chat-1"},       // 别的 Source，忽略
		{Source: "github", ID: "other/repo#5"}, // 别的仓库，忽略
	}}
	res, err := src.Poll(context.Background(), req, collector.receive)
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if res.Received != 1 {
		t.Fatalf("Received = %d, want only the subscribed Thread's comment", res.Received)
	}
	if len(collector.triggers) != 1 || collector.triggers[0].Threads[0].ID != "acme/webapp#12" {
		t.Fatalf("received = %+v", collector.triggers)
	}
	if res.Filtered != 1 {
		t.Fatalf("Filtered = %d, want the uninteresting Thread's comment counted", res.Filtered)
	}
}

func TestPollIgnoresNotificationsFromOtherRepository(t *testing.T) {
	cmd := &fakeCommand{respond: pollRouter(t, notificationsJSON("other/repo", 99), "[]")}
	src := testSource(t, cmd, nil)
	var collector receiveCollector

	res, err := src.Poll(context.Background(), sources.PollRequest{}, collector.receive)
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if res.Received != 0 {
		t.Fatalf("Received = %d, want 0", res.Received)
	}
	// 没有感兴趣的 Thread 时不该去读仓库评论，省下 rate limit。
	if cmd.calledPath("repos/acme/webapp/issues/comments") {
		t.Fatalf("polled comments for a repository with no notifications or subscriptions: %v", cmd.paths())
	}
}

func TestPollUsesStoredCursorAsSince(t *testing.T) {
	since := testNow.Add(-time.Hour)
	cursor := encodeCursor("acme/webapp", since)
	cmd := &fakeCommand{respond: pollRouter(t, "[]", "[]")}
	src := testSource(t, cmd, nil)
	var collector receiveCollector

	if _, err := src.Poll(context.Background(), sources.PollRequest{Cursor: cursor}, collector.receive); err != nil {
		t.Fatalf("Poll: %v", err)
	}
	firstPoll := cmd.paths()[0]
	if want := url.QueryEscape(since.Format(time.RFC3339)); !strings.Contains(firstPoll, "since="+want) {
		t.Fatalf("notification path = %q, want since=%q", firstPoll, want)
	}
}

func TestSinceOfResetsBadOrForeignCursors(t *testing.T) {
	src := testSource(t, &fakeCommand{}, nil)
	started := testNow

	cases := []struct {
		name   string
		cursor sources.Cursor
		want   time.Time
	}{
		{"empty starts now", "", started},
		{"valid cursor", encodeCursor("acme/webapp", started.Add(-time.Hour)), started.Add(-time.Hour)},
		{"foreign repository", encodeCursor("other/repo", started.Add(-time.Hour)), started},
		{"no separator", "garbage", started},
		{"bad timestamp", "acme/webapp|not-a-time", started},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := src.sinceOf(tc.cursor, started); !got.Equal(tc.want) {
				t.Fatalf("sinceOf(%q) = %v, want %v", tc.cursor, got, tc.want)
			}
		})
	}
}
