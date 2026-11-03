package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/zeokeel/super-agent/internal/sources"
)

// pagingResponder 是"多页"假 gh：通知与评论各两页（100 + 5 条），用来证明
// 单页上限不再静默丢事件（N-14）。
func pagingResponder(t *testing.T, notificationsPerPage, commentsPerPage int) func([]string) ([]byte, error) {
	t.Helper()
	return func(args []string) ([]byte, error) {
		path := pathOf(args)
		switch {
		case strings.HasPrefix(path, "notifications"):
			page := 1
			if strings.Contains(path, "&page=2") {
				page = 2
			}
			if page == 1 {
				numbers := make([]int, 0, notificationsPerPage)
				for i := 1; i <= notificationsPerPage; i++ {
					numbers = append(numbers, i)
				}
				return []byte(notificationsJSON("acme/webapp", numbers...)), nil
			}
			numbers := make([]int, 0, 5)
			for i := notificationsPerPage + 1; i <= notificationsPerPage+5; i++ {
				numbers = append(numbers, i)
			}
			return []byte(notificationsJSON("acme/webapp", numbers...)), nil
		case strings.HasPrefix(path, "repos/acme/webapp/issues/comments"):
			page := 1
			if strings.Contains(path, "&page=2") {
				page = 2
			}
			var elements []json.RawMessage
			if page == 1 {
				for i := 1; i <= commentsPerPage; i++ {
					elements = append(elements, commentElement(t, int64(1000+i), "alice", "@zeokeel-agent ping", i))
				}
			} else {
				for i := commentsPerPage + 1; i <= commentsPerPage+5; i++ {
					elements = append(elements, commentElement(t, int64(1000+i), "alice", "@zeokeel-agent ping", i))
				}
			}
			return []byte(commentsArray(t, elements...)), nil
		default:
			t.Errorf("unexpected gh call: %v", args)
			return nil, fmt.Errorf("unexpected gh call: %v", args)
		}
	}
}

// 通知与评论都必须翻页：单页 100 条的上限会静默丢事件。
func TestPollPaginatesNotificationsAndComments(t *testing.T) {
	cmd := &fakeCommand{respond: pagingResponder(t, 100, 100)}
	src := testSource(t, cmd, nil)
	var collector receiveCollector

	res, err := src.Poll(context.Background(), sources.PollRequest{}, collector.receive)
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if res.Received != 105 {
		t.Fatalf("Received = %d, want 105 (100 first page + 5 second page)", res.Received)
	}
	paths := cmd.paths()
	if !containsSubstring(paths, "notifications?all=true&per_page=100&since=") ||
		!containsSubstring(paths, "&page=2") {
		t.Fatalf("notifications were not paginated: %v", paths)
	}
	if !containsSubstring(paths, "issues/comments") || !containsSubstring(paths, "&page=2") {
		t.Fatalf("comments were not paginated: %v", paths)
	}
}

// 分页不收敛时必须失败并保持游标，而不是静默截断。
func TestPollFailsClosedWhenPaginationExceedsCap(t *testing.T) {
	var notificationCalls int
	cmd := &fakeCommand{respond: func(args []string) ([]byte, error) {
		path := pathOf(args)
		if !strings.HasPrefix(path, "notifications") {
			t.Errorf("unexpected gh call: %v", args)
			return nil, fmt.Errorf("unexpected gh call: %v", args)
		}
		notificationCalls++
		numbers := make([]int, 0, listPageSize)
		for i := 1; i <= listPageSize; i++ {
			numbers = append(numbers, i)
		}
		return []byte(notificationsJSON("acme/webapp", numbers...)), nil
	}}
	src := testSource(t, cmd, nil)
	var collector receiveCollector
	cursor := sources.Cursor("acme/webapp|" + testNow.Add(-time.Hour).Format(time.RFC3339Nano) + "||")

	res, err := src.Poll(context.Background(), sources.PollRequest{Cursor: cursor}, collector.receive)
	if err == nil || !strings.Contains(err.Error(), "pagination exceeded") {
		t.Fatalf("err = %v, want a pagination-limit error", err)
	}
	if res.Next != cursor {
		t.Fatalf("Next = %q, want the unchanged cursor %q", res.Next, cursor)
	}
	if notificationCalls != maxListPages {
		t.Fatalf("notification calls = %d, want %d (the cap)", notificationCalls, maxListPages)
	}
}

// ETag/304：同一个 since 下，下一轮带 If-None-Match；304 当作"没有新事件"，
// 游标与 ETag 原样保留（N-14 的 rate-limit 优化）。
func TestPollUsesETagAndTreats304AsNoChange(t *testing.T) {
	const notifETag = `W/"notif-1"`
	const commentETag = `W/"comment-1"`
	var notifCalls, commentCalls int
	cmd := &fakeHeaderCommand{respondWithHeaders: func(args []string) ([]byte, int, http.Header, error) {
		path := lastArg(args)
		switch {
		case strings.HasPrefix(path, "notifications"):
			notifCalls++
			if notifCalls > 1 { // 第二轮：带 If-None-Match 的请求
				return nil, http.StatusNotModified, http.Header{"Etag": {notifETag}}, nil
			}
			return []byte("[]"), http.StatusOK, http.Header{"Etag": {notifETag}}, nil
		case strings.HasPrefix(path, "repos/acme/webapp/issues/comments"):
			commentCalls++
			if commentCalls > 1 {
				return nil, http.StatusNotModified, http.Header{"Etag": {commentETag}}, nil
			}
			return []byte(commentsArray(t, commentElement(t, 601, "alice", "@zeokeel-agent 看一下", 12))),
				http.StatusOK, http.Header{"Etag": {commentETag}}, nil
		default:
			t.Errorf("unexpected gh call: %v", args)
			return nil, 0, nil, fmt.Errorf("unexpected gh call: %v", args)
		}
	}}
	src := testSource(t, cmd, func(cfg *Config) { cfg.ObserveComments = true })
	var first receiveCollector

	res, err := src.Poll(context.Background(), sources.PollRequest{}, first.receive)
	if err != nil {
		t.Fatalf("first Poll: %v", err)
	}
	if res.Received != 1 {
		t.Fatalf("Received = %d, want 1", res.Received)
	}
	state, ok := decodeCursor(res.Next)
	if !ok {
		t.Fatalf("cursor %q does not decode", res.Next)
	}
	if state.NotifETag != notifETag || state.CommentETag != commentETag {
		t.Fatalf("cursor ETags = %q/%q, want %q/%q", state.NotifETag, state.CommentETag, notifETag, commentETag)
	}

	var second receiveCollector
	res2, err := src.Poll(context.Background(), sources.PollRequest{Cursor: res.Next}, second.receive)
	if err != nil {
		t.Fatalf("second Poll: %v", err)
	}
	if len(second.got) != 0 {
		t.Fatalf("304 delivered %d events, want 0", len(second.got))
	}
	if res2.Next != res.Next {
		t.Fatalf("cursor changed on 304: %q -> %q", res.Next, res2.Next)
	}
	if !anyArgContains(cmd.recorded(), "If-None-Match: "+notifETag) ||
		!anyArgContains(cmd.recorded(), "If-None-Match: "+commentETag) {
		t.Fatalf("conditional headers missing: %v", cmd.recorded())
	}
}

// ReceiveAll：Issue、PR（含已合并状态）与 PR 行内 review 评论都变成 Trigger，
// 并落到 admission table 的 issues/pulls/comments 行（001:27 / 003 / 006）。
func TestPollReceiveAllCoversIssuesPullsAndReviewComments(t *testing.T) {
	issue := `{"id":1,"number":7,"title":"登录报错","body":"@zeokeel-agent 看看这个","state":"open",` +
		`"user":{"login":"alice","type":"User"},"html_url":"https://github.com/acme/webapp/issues/7",` +
		`"created_at":"2026-01-02T03:00:00Z","updated_at":"2026-01-02T03:04:00Z"}`
	pull := `{"id":2,"number":12,"title":"修复登录","body":"","state":"closed",` +
		`"user":{"login":"bob","type":"User"},"html_url":"https://github.com/acme/webapp/pull/12",` +
		`"created_at":"2026-01-02T02:00:00Z","updated_at":"2026-01-02T03:05:00Z",` +
		`"pull_request":{"merged_at":"2026-01-02T03:05:00Z"}}`
	review := `{"id":3,"body":"@zeokeel-agent 这里加个测试","user":{"login":"carol","type":"User"},` +
		`"pull_request_url":"https://api.github.com/repos/acme/webapp/pulls/12",` +
		`"html_url":"https://github.com/acme/webapp/pull/12#discussion_r3",` +
		`"created_at":"2026-01-02T03:06:00Z","updated_at":"2026-01-02T03:06:00Z"}`
	cmd := &fakeCommand{respond: func(args []string) ([]byte, error) {
		path := pathOf(args)
		switch {
		case strings.HasPrefix(path, "notifications"):
			return []byte("[]"), nil
		case strings.HasPrefix(path, "repos/acme/webapp/issues/comments"):
			return []byte("[]"), nil
		case strings.HasPrefix(path, "repos/acme/webapp/pulls/comments"):
			return []byte(commentsArray(t, json.RawMessage(review))), nil
		case strings.HasPrefix(path, "repos/acme/webapp/issues?"):
			return []byte("[" + issue + "," + pull + "]"), nil
		default:
			t.Errorf("unexpected gh call: %v", args)
			return nil, fmt.Errorf("unexpected gh call: %v", args)
		}
	}}
	src := testSource(t, cmd, func(cfg *Config) { cfg.ReceiveAll = true })
	var collector receiveCollector

	res, err := src.Poll(context.Background(), sources.PollRequest{}, collector.receive)
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if res.Received != 3 {
		t.Fatalf("Received = %d, want issue + pull + review comment (3)", res.Received)
	}
	rows := map[string]string{}
	texts := map[string]string{}
	for _, tr := range collector.triggers {
		scope := src.Scope(tr)
		if !scope.Listened || !scope.Observed {
			t.Fatalf("trigger %s scope = %+v, want listened+observed under ReceiveAll", tr.ID, scope)
		}
		rows[tr.Channel.Address["kind"]] = scope.Row
		texts[tr.Channel.Address["kind"]] = tr.Text
	}
	if rows[RawIssue] != RowIssue || rows[RawPull] != RowPull || rows[RawReviewComment] != RowComment {
		t.Fatalf("admission rows = %v", rows)
	}
	if !strings.Contains(texts[RawIssue], "登录报错") || !strings.Contains(texts[RawIssue], "[issue open]") {
		t.Fatalf("issue text = %q", texts[RawIssue])
	}
	if !strings.Contains(texts[RawPull], "[pull request merged]") {
		t.Fatalf("pull text = %q", texts[RawPull])
	}
	if !strings.Contains(texts[RawReviewComment], "加个测试") {
		t.Fatalf("review comment text = %q", texts[RawReviewComment])
	}
}

// PR 生命周期：状态变化改变 updated_at → 版本段变化 → 新 Trigger（不是原地改）。
func TestPullLifecycleChangeGetsANewTriggerID(t *testing.T) {
	src := testSource(t, &fakeCommand{}, nil)
	base := `{"id":2,"number":12,"title":"修复登录","body":"","state":"open",` +
		`"user":{"login":"bob","type":"User"},"html_url":"https://github.com/acme/webapp/pull/12",` +
		`"created_at":"2026-01-02T02:00:00Z","updated_at":%q,"pull_request":{"merged_at":%s}}`
	open := fmt.Sprintf(base, "2026-01-02T03:00:00Z", "null")
	merged := fmt.Sprintf(base, "2026-01-02T04:00:00Z", `"2026-01-02T04:00:00Z"`)
	a, err := src.Translate(sources.Raw{Kind: RawPull, Data: []byte(open)})
	if err != nil {
		t.Fatal(err)
	}
	b, err := src.Translate(sources.Raw{Kind: RawPull, Data: []byte(merged)})
	if err != nil {
		t.Fatal(err)
	}
	if a.ID == b.ID {
		t.Fatal("a state change must produce a new content-derived Trigger ID")
	}
	if b.Channel.Address["state"] != "pull request merged" {
		t.Fatalf("merged state = %q", b.Channel.Address["state"])
	}
	again, err := src.Translate(sources.Raw{Kind: RawPull, Data: []byte(merged)})
	if err != nil {
		t.Fatal(err)
	}
	if again.ID != b.ID {
		t.Fatal("re-reading the same item must be idempotent")
	}
	if again.Channel.Address["repository"] != "acme/webapp" || again.Channel.Address["number"] != "12" {
		t.Fatalf("address = %v", again.Channel.Address)
	}
}

func containsSubstring(items []string, sub string) bool {
	for _, item := range items {
		if strings.Contains(item, sub) {
			return true
		}
	}
	return false
}

func anyArgContains(calls [][]string, sub string) bool {
	for _, call := range calls {
		for _, arg := range call {
			if strings.Contains(arg, sub) {
				return true
			}
		}
	}
	return false
}

// Trigger 必须自带 gate 需要的行事实：Row（单数行名）与 ScopeReceivesAll。
// engine/gate.go 只读 Trigger.Row / Trigger.ScopeReceivesAll，不再回头问 Source。
func TestTriggersCarryAdmissionRowFacts(t *testing.T) {
	commentary := commentElement(t, 900, "alice", "@zeokeel-agent hi", 12)
	byDefault := testSource(t, &fakeCommand{}, nil)
	tr, err := byDefault.Translate(sources.Raw{Kind: RawComment, Data: commentary})
	if err != nil {
		t.Fatal(err)
	}
	if tr.Row != RowComment || tr.ScopeReceivesAll {
		t.Fatalf("default comment row = %q / receivesAll = %v", tr.Row, tr.ScopeReceivesAll)
	}

	observing := testSource(t, &fakeCommand{}, func(cfg *Config) { cfg.ObserveComments = true })
	tr, err = observing.Translate(sources.Raw{Kind: RawComment, Data: commentary})
	if err != nil {
		t.Fatal(err)
	}
	if tr.Row != RowComment || !tr.ScopeReceivesAll {
		t.Fatalf("observed comment row = %q / receivesAll = %v", tr.Row, tr.ScopeReceivesAll)
	}

	all := testSource(t, &fakeCommand{}, func(cfg *Config) { cfg.ReceiveAll = true })
	issue := `{"id":11,"number":3,"title":"t","body":"b","state":"open",` +
		`"user":{"login":"alice","type":"User"},"html_url":"https://github.com/acme/webapp/issues/3",` +
		`"created_at":"2026-01-02T03:00:00Z","updated_at":"2026-01-02T03:00:00Z"}`
	issueTrigger, err := all.Translate(sources.Raw{Kind: RawIssue, Data: []byte(issue)})
	if err != nil {
		t.Fatal(err)
	}
	if issueTrigger.Row != RowIssue || !issueTrigger.ScopeReceivesAll {
		t.Fatalf("issue row = %q / receivesAll = %v", issueTrigger.Row, issueTrigger.ScopeReceivesAll)
	}
	pull := `{"id":12,"number":4,"title":"t","body":"b","state":"open",` +
		`"user":{"login":"alice","type":"User"},"html_url":"https://github.com/acme/webapp/pull/4",` +
		`"created_at":"2026-01-02T03:00:00Z","updated_at":"2026-01-02T03:00:00Z","pull_request":{}}`
	pullTrigger, err := all.Translate(sources.Raw{Kind: RawPull, Data: []byte(pull)})
	if err != nil {
		t.Fatal(err)
	}
	if pullTrigger.Row != RowPull || !pullTrigger.ScopeReceivesAll {
		t.Fatalf("pull row = %q / receivesAll = %v", pullTrigger.Row, pullTrigger.ScopeReceivesAll)
	}
}
