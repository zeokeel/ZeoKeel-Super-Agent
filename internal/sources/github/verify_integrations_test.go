package github

// verifier-A: independent falsification of the Wave-1 GitHub claims (audit
// N-14): pagination must not silently cap, and the idempotency marker must be
// searched on every page so a long thread cannot get a duplicate comment.
// Everything goes through fakeCommand: no gh process, no network.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/zeokeel/super-agent/internal/domain"
	"github.com/zeokeel/super-agent/internal/sources"
)

func verifySource(t *testing.T, fc *fakeCommand) *Source {
	t.Helper()
	src, err := New(Config{Repository: "acme/webapp", Login: "agent"}, fc)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return src
}

// pageArg returns the `page=N` value of an API path, or 0.
func pageArg(path string) int {
	i := strings.LastIndex(path, "page=") // not "per_page="
	if i < 0 {
		return 0
	}
	var n int
	_, _ = fmt.Sscanf(path[i+len("page="):], "%d", &n)
	return n
}

// The marker sits on page 3 of a 300-comment thread. The old code searched only
// page 1 and would have posted a duplicate comment.
func TestVerifyMarkerFoundOnLaterPage(t *testing.T) {
	fc := &fakeCommand{}
	key := "reply-key-1"
	marker := markerOf(key)
	fc.respond = func(args []string) ([]byte, error) {
		if len(args) > 1 && args[1] == "-X" {
			return []byte(`{"id":999}`), nil
		}
		path := lastArg(args)
		page := pageArg(path)
		switch page {
		case 1, 2:
			return verifyCommentsPage(t, listPageSize, int64(page*1000)), nil
		case 3:
			comments := []map[string]any{{
				"id":   4242,
				"body": "earlier reply\n\n" + marker,
				"user": map[string]any{"login": "agent", "type": "User"},
			}}
			return json.Marshal(comments)
		default:
			return []byte("[]"), nil
		}
	}
	src := verifySource(t, fc)
	receipt, err := src.Channel().Send(context.Background(), sources.Outbound{
		Key:     key,
		Thread:  "acme/webapp#7",
		Address: map[string]string{"repository": "acme/webapp", "number": "7"},
		Text:    "new reply",
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if receipt.MessageID != "4242" {
		t.Fatalf("Send returned %q, want the existing comment 4242 (it would have duplicated)", receipt.MessageID)
	}
	if got := len(fc.postedBodies()); got != 0 {
		t.Fatalf("posted %d comment(s) despite the marker being on page 3: %v", got, fc.postedBodies())
	}
	if paths := fc.paths(); len(paths) != 3 {
		t.Fatalf("marker scan pages = %d (%v), want 3", len(paths), paths)
	}
}

// When every page is full and the marker is absent, the scan must fail closed
// (no POST) at the documented cap instead of guessing "not sent yet".
func TestVerifyMarkerScanFailsClosedAtCap(t *testing.T) {
	fc := &fakeCommand{}
	fc.respond = func(args []string) ([]byte, error) {
		if len(args) > 1 && args[1] == "-X" {
			return []byte(`{"id":999}`), nil
		}
		return verifyCommentsPage(t, listPageSize, 1), nil // always a full page
	}
	src := verifySource(t, fc)
	_, err := src.Channel().Send(context.Background(), sources.Outbound{
		Key:     "reply-key-2",
		Thread:  "acme/webapp#7",
		Address: map[string]string{"repository": "acme/webapp", "number": "7"},
		Text:    "new reply",
	})
	if err == nil {
		t.Fatal("a never-ending marker scan was allowed to POST a comment")
	}
	if got := len(fc.postedBodies()); got != 0 {
		t.Fatalf("posted %d comment(s) after a failed marker scan", got)
	}
	if got := len(fc.paths()); got != maxMarkerPages {
		t.Fatalf("marker scan stopped at %d pages, want the cap %d", got, maxMarkerPages)
	}
	if !strings.Contains(err.Error(), "idempotency") {
		t.Fatalf("error does not explain the failed idempotency check: %v", err)
	}
}

// A list must be assembled from every page, and exceeding the page cap must be an
// error (with the cursor kept by the caller), never a silent truncation.
func TestVerifyListPaginatesAndFailsOverCap(t *testing.T) {
	fc := &fakeCommand{}
	fc.respond = func(args []string) ([]byte, error) {
		page := pageArg(lastArg(args))
		switch {
		case page <= 2:
			return verifyCommentsPage(t, listPageSize, int64(page*1000)), nil
		case page == 3:
			return verifyCommentsPage(t, 5, 3000), nil
		default:
			return []byte("[]"), nil
		}
	}
	src := verifySource(t, fc)
	got, err := src.list(context.Background(),
		"repos/acme/webapp/issues/comments?sort=updated&direction=asc", testNow, "")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got.Items) != 2*listPageSize+5 {
		t.Fatalf("list returned %d items, want %d (a page was dropped)", len(got.Items), 2*listPageSize+5)
	}
	if calls := len(fc.paths()); calls != 3 {
		t.Fatalf("list made %d requests, want 3", calls)
	}

	// Always-full pages: fail at the cap instead of returning a truncated list.
	over := &fakeCommand{}
	over.respond = func(args []string) ([]byte, error) {
		return verifyCommentsPage(t, listPageSize, 1), nil
	}
	src = verifySource(t, over)
	if _, err := src.list(context.Background(),
		"repos/acme/webapp/issues/comments?sort=updated&direction=asc", testNow, ""); err == nil {
		t.Fatalf("list did not fail at the %d-page cap (silent truncation)", maxListPages)
	} else if !strings.Contains(err.Error(), "pagination exceeded") {
		t.Fatalf("unexpected over-cap error: %v", err)
	}
	if calls := len(over.paths()); calls != maxListPages {
		t.Fatalf("over-cap scan made %d requests, want %d", calls, maxListPages)
	}
}

// The poll loop must call receive for events on later pages too (no silent loss
// of the tail of a busy window).
func TestVerifyPollReceivesEveryPage(t *testing.T) {
	fc := &fakeCommand{}
	comment := func(id int64, body string) map[string]any {
		return map[string]any{
			"id": id, "body": body,
			"issue_url":  "https://api.github.com/repos/acme/webapp/issues/7",
			"user":       map[string]any{"login": "alice", "type": "User"},
			"created_at": "2026-01-02T03:04:05Z",
			"updated_at": "2026-01-02T03:04:05Z",
		}
	}
	fc.respond = func(args []string) ([]byte, error) {
		path := lastArg(args)
		switch {
		case strings.Contains(path, "notifications"):
			return []byte(`[]`), nil
		case strings.Contains(path, "issues/comments"):
			page := pageArg(path)
			var out []map[string]any
			if page == 1 {
				for i := 0; i < listPageSize; i++ {
					out = append(out, comment(int64(i+1), "@agent please do it"))
				}
				return json.Marshal(out)
			}
			if page == 2 {
				out = append(out, comment(5001, "@agent second page"))
				return json.Marshal(out)
			}
			return []byte("[]"), nil
		default:
			return []byte("[]"), nil
		}
	}
	src := verifySource(t, fc)
	src.cfg.ObserveComments = true
	src.cfg.ReceiveAll = false
	var received []string
	var cursor sources.Cursor
	res, err := src.Poll(context.Background(), sources.PollRequest{Cursor: cursor},
		func(tr domain.Trigger) error { received = append(received, string(tr.ID)); return nil })
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if len(received) != listPageSize+1 {
		t.Fatalf("received %d triggers, want %d (a comment page was dropped)", len(received), listPageSize+1)
	}
	if res.Next == "" {
		t.Fatal("Poll returned no cursor")
	}
}

// manyComments renders a page of filler comments from other users.
func verifyCommentsPage(t *testing.T, n int, base int64) []byte {
	t.Helper()
	out := make([]map[string]any, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, map[string]any{
			"id":         base + int64(i),
			"body":       "filler",
			"issue_url":  "https://api.github.com/repos/acme/webapp/issues/7",
			"user":       map[string]any{"login": "someone-else", "type": "User"},
			"created_at": time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		})
	}
	data, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal comments: %v", err)
	}
	return data
}
