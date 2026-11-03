package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/zeokeel/super-agent/internal/sources"
)

// channelRouter 模拟一条 Thread 的评论列表 + POST 响应。GET 与 POST 走同一个
// 路径，用 "-X" 区分；这样测试能断言"到底有没有真的发"。
func channelRouter(t *testing.T, existing string) func([]string) ([]byte, error) {
	t.Helper()
	return func(args []string) ([]byte, error) {
		if len(args) >= 2 && args[1] == "-X" {
			return []byte(`{"id":9001}`), nil
		}
		if path := pathOf(args); strings.HasPrefix(path, "repos/acme/webapp/issues/12/comments") {
			return []byte(existing), nil
		}
		t.Errorf("unexpected gh call: %v", args)
		return nil, fmt.Errorf("unexpected gh call: %v", args)
	}
}

// existingCommentJSON 构造一条已存在的评论。
func existingCommentJSON(t *testing.T, id int64, login, body string) string {
	t.Helper()
	raw, err := json.Marshal([]map[string]any{{
		"id":   id,
		"body": body,
		"user": map[string]any{"login": login},
	}})
	if err != nil {
		t.Fatalf("marshal existing comment: %v", err)
	}
	return string(raw)
}

func outboundFor(key string) sources.Outbound {
	return sources.Outbound{
		Key:     key,
		Thread:  "acme/webapp#12",
		Address: map[string]string{"repository": "acme/webapp", "number": "12"},
		Text:    "已修复，请验证。",
	}
}

func TestSendPostsCommentWithHiddenMarker(t *testing.T) {
	cmd := &fakeCommand{respond: channelRouter(t, "[]")}
	src := testSource(t, cmd, nil)

	receipt, err := src.Channel().Send(context.Background(), outboundFor("outbox-key-1"))
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if receipt.Gone {
		t.Fatal("Send reported gone for a live thread")
	}
	if receipt.MessageID != "9001" {
		t.Fatalf("MessageID = %q, want the POST response id", receipt.MessageID)
	}

	bodies := cmd.postedBodies()
	if len(bodies) != 1 {
		t.Fatalf("posted %d comments, want 1", len(bodies))
	}
	if !strings.Contains(bodies[0], "已修复，请验证。") {
		t.Fatalf("posted body lost the reply text: %q", bodies[0])
	}
	marker := markerOf("outbox-key-1")
	if !strings.Contains(bodies[0], marker) {
		t.Fatalf("posted body has no idempotency marker:\n%s\nmarker: %s", bodies[0], marker)
	}
	if !strings.HasPrefix(marker, "<!-- zeokeel:") || !strings.HasSuffix(marker, "-->") {
		t.Fatalf("marker %q is not a hidden HTML comment", marker)
	}
}

func TestSendIsIdempotentWhenMarkerAlreadyPublished(t *testing.T) {
	key := "outbox-key-2"
	existing := existingCommentJSON(t, 777, "zeokeel-agent", "已修复，请验证。\n\n"+markerOf(key))
	cmd := &fakeCommand{respond: channelRouter(t, existing)}
	src := testSource(t, cmd, nil)

	receipt, err := src.Channel().Send(context.Background(), outboundFor(key))
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if receipt.MessageID != "777" {
		t.Fatalf("MessageID = %q, want the existing comment's id", receipt.MessageID)
	}
	if bodies := cmd.postedBodies(); len(bodies) != 0 {
		t.Fatalf("retry posted a duplicate comment: %v", bodies)
	}
}

func TestSendIgnoresMarkerWrittenBySomeoneElse(t *testing.T) {
	key := "outbox-key-3"
	// 别人正文里恰好出现同样的标记，不是我们的回执；不能因此跳过发送。
	existing := existingCommentJSON(t, 778, "alice", "引用："+markerOf(key))
	cmd := &fakeCommand{respond: channelRouter(t, existing)}
	src := testSource(t, cmd, nil)

	if _, err := src.Channel().Send(context.Background(), outboundFor(key)); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if bodies := cmd.postedBodies(); len(bodies) != 1 {
		t.Fatalf("posted %d comments, want 1", len(bodies))
	}
}

func TestSendReportsGoneOnDeletedIssue(t *testing.T) {
	cmd := &fakeCommand{respond: func(args []string) ([]byte, error) {
		if len(args) >= 2 && args[1] == "-X" {
			return nil, &CLIError{Args: args, Code: 410, Stderr: "gh: Gone (HTTP 410)"}
		}
		return []byte("[]"), nil
	}}
	src := testSource(t, cmd, nil)

	receipt, err := src.Channel().Send(context.Background(), outboundFor("outbox-key-4"))
	if err != nil {
		t.Fatalf("a deleted Issue must be a gone receipt, not a retryable error: %v", err)
	}
	if !receipt.Gone {
		t.Fatal("Receipt.Gone = false, want true")
	}
}

func TestSendRetriesNotFound(t *testing.T) {
	// 404 可能是私有仓库权限暂时丢失，不能当永久失败（原型同样重试）。
	cmd := &fakeCommand{respond: func(args []string) ([]byte, error) {
		if len(args) >= 2 && args[1] == "-X" {
			return nil, &CLIError{Args: args, Code: 404, Stderr: "gh: Not Found (HTTP 404)"}
		}
		return []byte("[]"), nil
	}}
	src := testSource(t, cmd, nil)

	receipt, err := src.Channel().Send(context.Background(), outboundFor("outbox-key-5"))
	if err == nil {
		t.Fatal("a 404 must be retryable, not swallowed")
	}
	if receipt.Gone {
		t.Fatal("Receipt.Gone = true for a retryable error")
	}
}

func TestSendReportsGoneOnLockedLocked(t *testing.T) {
	cmd := &fakeCommand{respond: func(args []string) ([]byte, error) {
		if len(args) >= 2 && args[1] == "-X" {
			return nil, &CLIError{Args: args, Code: 403, Stderr: "gh: Forbidden (HTTP 403): issue is locked"}
		}
		return []byte("[]"), nil
	}}
	src := testSource(t, cmd, nil)

	receipt, err := src.Channel().Send(context.Background(), outboundFor("outbox-key-6"))
	if err != nil || !receipt.Gone {
		t.Fatalf("locked issue: receipt=%+v err=%v, want a gone receipt", receipt, err)
	}
}

func TestSendRejectsMissingKeyOrTarget(t *testing.T) {
	src := testSource(t, &fakeCommand{}, nil)

	noKey := outboundFor("")
	if _, err := src.Channel().Send(context.Background(), noKey); err == nil {
		t.Fatal("Send accepted a message without an idempotency key")
	}

	noTarget := sources.Outbound{Key: "k", Text: "hi"}
	if _, err := src.Channel().Send(context.Background(), noTarget); err == nil {
		t.Fatal("Send accepted a message without a reply target")
	}
}

func TestSendRejectsForeignRepository(t *testing.T) {
	src := testSource(t, &fakeCommand{}, nil)
	msg := outboundFor("k")
	msg.Address["repository"] = "other/repo"
	if _, err := src.Channel().Send(context.Background(), msg); err == nil {
		t.Fatal("Send posted into a repository this Source does not watch")
	}
}

func TestSendFallsBackToThreadIDForTarget(t *testing.T) {
	cmd := &fakeCommand{respond: channelRouter(t, "[]")}
	src := testSource(t, cmd, nil)

	msg := sources.Outbound{Key: "k", Thread: "acme/webapp#12", Text: "hi"}
	if _, err := src.Channel().Send(context.Background(), msg); err != nil {
		t.Fatalf("Send with only a Thread id: %v", err)
	}
}

func TestSendFailsClosedWhenExistingCommentsAreUnreadable(t *testing.T) {
	// 查不清是否已经发过，就不能再发：宁可不发，让 Outbox 重试。
	cmd := &fakeCommand{respond: func(args []string) ([]byte, error) {
		return []byte("not json"), nil
	}}
	src := testSource(t, cmd, nil)

	if _, err := src.Channel().Send(context.Background(), outboundFor("k")); err == nil {
		t.Fatal("Send posted although it could not verify idempotency")
	}
	if bodies := cmd.postedBodies(); len(bodies) != 0 {
		t.Fatalf("Send posted despite an unreadable thread: %v", bodies)
	}
}

func TestMarkerIsStableAndDoesNotLeakTheKey(t *testing.T) {
	a1, a2 := markerOf("same-key"), markerOf("same-key")
	if a1 != a2 {
		t.Fatal("markerOf is not deterministic")
	}
	if a1 == markerOf("other-key") {
		t.Fatal("two different keys produced the same marker")
	}
	if strings.Contains(a1, "same-key") {
		t.Fatalf("marker %q leaks the raw outbox key", a1)
	}
	if strings.ContainsAny(a1, "\n\r") {
		t.Fatal("marker contains a newline")
	}
}

func TestIsGoneOnlyForPermanentErrors(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"410", &CLIError{Code: 410}, true},
		{"403 locked", &CLIError{Code: 403, Stderr: "issue is Locked"}, true},
		{"403 other", &CLIError{Code: 403, Stderr: "forbidden"}, false},
		{"404", &CLIError{Code: 404}, false},
		{"plain", errors.New("network"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isGone(tc.err); got != tc.want {
				t.Fatalf("isGone(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
