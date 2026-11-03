package github

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// manyComments 构造一页 n 条"agent 自己写的、不带 marker"的评论。
func manyComments(t *testing.T, n int, idBase int64) string {
	t.Helper()
	elements := make([]map[string]any, 0, n)
	for i := 0; i < n; i++ {
		elements = append(elements, map[string]any{
			"id":   idBase + int64(i),
			"body": fmt.Sprintf("第 %d 条旧评论", i),
			"user": map[string]any{"login": "zeokeel-agent"},
		})
	}
	raw, err := json.Marshal(elements)
	if err != nil {
		t.Fatalf("marshal comments: %v", err)
	}
	return string(raw)
}

// marker 在第 2 页也必须被找到：只看第一页会漏判"已发过"，重发就是重复评论。
func TestSendScansAllCommentPagesForTheMarker(t *testing.T) {
	posted := 0
	cmd := &fakeCommand{respond: func(args []string) ([]byte, error) {
		if len(args) >= 2 && args[1] == "-X" {
			posted++
			return []byte(`{"id":9001}`), nil
		}
		path := pathOf(args)
		if !strings.HasPrefix(path, "repos/acme/webapp/issues/12/comments") {
			return nil, fmt.Errorf("unexpected gh call: %v", args)
		}
		if strings.Contains(path, "&page=2") {
			return []byte(existingCommentJSON(t, 777, "zeokeel-agent", "旧回复\n\n"+markerOf("k-1"))), nil
		}
		return []byte(manyComments(t, listPageSize, 1000)), nil
	}}
	src := testSource(t, cmd, nil)
	receipt, err := src.Channel().Send(context.Background(), outboundFor("k-1"))
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if receipt.MessageID != "777" {
		t.Fatalf("MessageID = %q, want the existing comment 777", receipt.MessageID)
	}
	if posted != 0 {
		t.Fatalf("posted %d duplicate comments, want 0", posted)
	}
	if !containsSubstring(cmd.paths(), "&page=2") {
		t.Fatalf("marker scan did not reach page 2: %v", cmd.paths())
	}
}

// 找不到 marker 且页数超过上限时必须失败关闭：宁可让 Outbox 重试，也不重复评论。
func TestSendFailsClosedWhenMarkerScanExceedsCap(t *testing.T) {
	posted := 0
	cmd := &fakeCommand{respond: func(args []string) ([]byte, error) {
		if len(args) >= 2 && args[1] == "-X" {
			posted++
			return []byte(`{"id":9001}`), nil
		}
		path := pathOf(args)
		if !strings.HasPrefix(path, "repos/acme/webapp/issues/12/comments") {
			return nil, fmt.Errorf("unexpected gh call: %v", args)
		}
		return []byte(manyComments(t, listPageSize, 1000)), nil
	}}
	src := testSource(t, cmd, nil)
	_, err := src.Channel().Send(context.Background(), outboundFor("k-missing"))
	if err == nil || !strings.Contains(err.Error(), "cannot verify idempotency") {
		t.Fatalf("err = %v, want a fail-closed idempotency error", err)
	}
	if posted != 0 {
		t.Fatalf("posted %d comments while unsure, want 0", posted)
	}
	markerCalls := 0
	for _, call := range cmd.recorded() {
		if !hasArg(call, "-X") {
			markerCalls++
		}
	}
	if markerCalls != maxMarkerPages {
		t.Fatalf("marker scan pages = %d, want the cap %d", markerCalls, maxMarkerPages)
	}
}
