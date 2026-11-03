package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zeokeel/super-agent/internal/domain"
	"github.com/zeokeel/super-agent/internal/sources"
)

// testNow 是所有测试的固定时钟：游标与 At 断言不能依赖真实时间。
var testNow = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

// fakeCommand 是按路径返回固定 JSON 的假 gh。它记录每一次调用，所以测试既能
// 断言"读到了什么"，也能断言"某些请求发没发出去"。任何测试都不存在真的 gh
// 进程或网络连接。
type fakeCommand struct {
	mu      sync.Mutex
	calls   [][]string
	respond func(args []string) ([]byte, error)
}

func (f *fakeCommand) Run(_ context.Context, args ...string) ([]byte, error) {
	f.mu.Lock()
	f.calls = append(f.calls, append([]string(nil), args...))
	respond := f.respond
	f.mu.Unlock()

	if respond == nil {
		return []byte("[]"), nil
	}
	return respond(args)
}

// recorded 返回所有调用的快照。
func (f *fakeCommand) recorded() [][]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([][]string(nil), f.calls...)
}

// paths 返回每次调用的 API 路径（gh api 的第二个参数）。
func (f *fakeCommand) paths() []string {
	var out []string
	for _, call := range f.recorded() {
		out = append(out, pathOf(call))
	}
	return out
}

// postedBodies 返回所有 POST 的 body 参数，用于断言发了什么评论。
func (f *fakeCommand) postedBodies() []string {
	var out []string
	for _, call := range f.recorded() {
		if !hasArg(call, "-X") {
			continue
		}
		for _, arg := range call {
			if strings.HasPrefix(arg, "body=") {
				out = append(out, strings.TrimPrefix(arg, "body="))
			}
		}
	}
	return out
}

// calledPath 报告是否调用过某个路径前缀。
func (f *fakeCommand) calledPath(prefix string) bool {
	for _, path := range f.paths() {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

// lastArg 返回调用的最后一个参数：`gh api -i <path>` 里 path 在最后。
func lastArg(call []string) string {
	if len(call) == 0 {
		return ""
	}
	return call[len(call)-1]
}

func pathOf(call []string) string {
	if len(call) < 2 {
		return ""
	}
	return call[1]
}

func hasArg(call []string, want string) bool {
	for _, arg := range call {
		if arg == want {
			return true
		}
	}
	return false
}

// testSource 构造一个绑到 workspace 的 Source，并把时钟钉在 testNow。
func testSource(t *testing.T, cmd Command, mutate func(*Config)) *Source {
	t.Helper()
	cfg := Config{Repository: "acme/webapp", Login: "zeokeel-agent", Workspace: "ws_acme"}
	if mutate != nil {
		mutate(&cfg)
	}
	src, err := New(cfg, cmd)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	src.now = func() time.Time { return testNow }
	return src
}

// commentElement 构造一条 API 评论。测试只声明用到的字段，和 Source 的解析
// 保持同样的最小面。
func commentElement(t *testing.T, id int64, login, body string, number int) json.RawMessage {
	t.Helper()
	return commentElementAt(t, id, login, body, number, "2026-01-02T03:04:05Z", "User")
}

func commentElementAt(t *testing.T, id int64, login, body string, number int, at, userType string) json.RawMessage {
	t.Helper()
	record := map[string]any{
		"id":         id,
		"body":       body,
		"user":       map[string]any{"login": login, "type": userType},
		"issue_url":  fmt.Sprintf("https://api.github.com/repos/acme/webapp/issues/%d", number),
		"html_url":   fmt.Sprintf("https://github.com/acme/webapp/issues/%d#issuecomment-%d", number, id),
		"created_at": at,
		"updated_at": at,
	}
	raw, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("marshal comment: %v", err)
	}
	return raw
}

// commentsArray 把若干元素拼成 GitHub 返回的 JSON 数组。元素可以是合法评论，
// 也可以是故意写坏的原始 JSON。
func commentsArray(t *testing.T, elements ...json.RawMessage) string {
	t.Helper()
	raw, err := json.Marshal(elements)
	if err != nil {
		t.Fatalf("marshal comments array: %v", err)
	}
	return string(raw)
}

// notificationsJSON 构造 GitHub 通知数组。编号对应仓库里的 Issue/PR。
func notificationsJSON(repo string, numbers ...int) string {
	items := make([]map[string]any, 0, len(numbers))
	for _, number := range numbers {
		items = append(items, map[string]any{
			"repository": map[string]any{"full_name": repo},
			"subject": map[string]any{
				"url": fmt.Sprintf("https://api.github.com/repos/%s/issues/%d", repo, number),
			},
		})
	}
	raw, _ := json.Marshal(items)
	return string(raw)
}

// pollRouter 是 Poll 测试的假 gh：只认通知与仓库级评论两个路径，其他路径立刻
// 报错——测试会因此失败，而不是悄悄回退到真实网络。
func pollRouter(t *testing.T, notifications, comments string) func([]string) ([]byte, error) {
	t.Helper()
	return func(args []string) ([]byte, error) {
		path := pathOf(args)
		switch {
		case strings.HasPrefix(path, "notifications"):
			return []byte(notifications), nil
		case strings.HasPrefix(path, "repos/acme/webapp/issues/comments"):
			return []byte(comments), nil
		default:
			t.Errorf("unexpected gh call: %v", args)
			return nil, fmt.Errorf("unexpected gh call: %v", args)
		}
	}
}

// receiveCollector 是测试用 Receiver：记录收到的 Trigger，并按需让某一条失败。
type receiveCollector struct {
	got        []string
	triggers   []domain.Trigger
	failOnCall int // 第几次调用（从 1 开始）返回错误；0 表示从不失败
	err        error
}

func (r *receiveCollector) receive(trigger domain.Trigger) error {
	r.got = append(r.got, string(trigger.ID))
	r.triggers = append(r.triggers, trigger)
	if r.failOnCall > 0 && len(r.got) == r.failOnCall {
		if r.err != nil {
			return r.err
		}
		return errors.New("receive failed")
	}
	return nil
}

func cursorSince(t *testing.T, cursor sources.Cursor) time.Time {
	t.Helper()
	parts := strings.Split(string(cursor), "|")
	if len(parts) < 2 {
		t.Fatalf("cursor %q has no repository separator", cursor)
	}
	if parts[0] != "acme/webapp" {
		t.Fatalf("cursor repository = %q, want acme/webapp", parts[0])
	}
	when, err := time.Parse(time.RFC3339Nano, parts[1])
	if err != nil {
		t.Fatalf("cursor %q has a bad timestamp: %v", cursor, err)
	}
	return when
}

// fakeHeaderCommand 是实现了 HeaderCommand 的假 gh：可分别指定"响应头 + 状态码"，
// 用来测 ETag / 304 路径（生产实现见 ExecCommand.RunWithHeaders）。
type fakeHeaderCommand struct {
	fakeCommand
	respondWithHeaders func(args []string) ([]byte, int, http.Header, error)
}

func (f *fakeHeaderCommand) RunWithHeaders(ctx context.Context, args ...string) ([]byte, int, http.Header, error) {
	f.mu.Lock()
	f.calls = append(f.calls, append([]string(nil), args...))
	respond := f.respondWithHeaders
	f.mu.Unlock()
	if respond == nil {
		return nil, 0, nil, errors.New("fakeHeaderCommand has no responder")
	}
	return respond(args)
}
