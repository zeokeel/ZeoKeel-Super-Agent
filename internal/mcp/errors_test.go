package mcp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestBuildToolName 覆盖：前缀、非法字符收敛、长度上限与唯一性。
func TestBuildToolName(t *testing.T) {
	name, err := buildToolName("fs", "read_file")
	if err != nil {
		t.Fatalf("buildToolName: %v", err)
	}
	if name != "mcp__fs__read_file" {
		t.Errorf("名字 = %q", name)
	}

	// provider 的工具名一般限制 64 字符，超了必须收敛。
	long := strings.Repeat("x", 80)
	name, err = buildToolName("server", long)
	if err != nil {
		t.Fatalf("长名字: %v", err)
	}
	if len(name) > maxToolNameLen {
		t.Errorf("名字长度 %d 超过上限 %d: %q", len(name), maxToolNameLen, name)
	}
	// 两个不同的长名字收敛后不能撞车，否则工具会互相覆盖。
	other, err := buildToolName("server", long+"y")
	if err != nil {
		t.Fatalf("长名字 2: %v", err)
	}
	if name == other {
		t.Errorf("长名字收敛后撞车: %q", name)
	}

	if _, err := buildToolName("fs", "   "); err == nil {
		t.Error("空工具名应报错")
	}
}

// TestSanitizeName 覆盖：只做字符替换，不做删除（长度可预测）。
func TestSanitizeName(t *testing.T) {
	cases := map[string]string{
		"fs":          "fs",
		"my server":   "my_server",
		"a.b/c":       "a_b_c",
		"中文服务":        "____",
		"ok-Name_123": "ok-Name_123",
	}
	for in, want := range cases {
		if got := sanitizeName(in); got != want {
			t.Errorf("sanitizeName(%q) = %q，期望 %q", in, got, want)
		}
	}
}

// TestClassify 覆盖错误分类规则。
func TestClassify(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want ErrorKind
	}{
		{"nil", nil, KindPermanent},
		{"canceled", context.Canceled, KindPermanent},
		{"deadline", fmt.Errorf("call: %w", context.DeadlineExceeded), KindRetryable},
		{"connection closed", fmt.Errorf("rpc: %w", mcpsdk.ErrConnectionClosed), KindRetryable},
		{"eof", fmt.Errorf("read: %w", io.EOF), KindRetryable},
		{"unexpected eof", io.ErrUnexpectedEOF, KindRetryable},
		{"net closed", net.ErrClosed, KindRetryable},
		{"invalid params", &jsonrpc.Error{Code: jsonrpc.CodeInvalidParams, Message: "bad"}, KindPermanent},
		{"method not found", &jsonrpc.Error{Code: jsonrpc.CodeMethodNotFound}, KindPermanent},
		{"parse error", &jsonrpc.Error{Code: jsonrpc.CodeParseError}, KindPermanent},
		{"internal error", &jsonrpc.Error{Code: jsonrpc.CodeInternalError}, KindRetryable},
		{"text broken pipe", errors.New("write: broken pipe"), KindRetryable},
		{"text closed network", errors.New("use of closed network connection"), KindRetryable},
		{"unknown", errors.New("something odd"), KindRetryable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classify(tc.err); got != tc.want {
				t.Errorf("classify(%v) = %v，期望 %v", tc.err, got, tc.want)
			}
		})
	}
}

// TestCallErrorWrapping 覆盖：CallError 带上下文且能 Unwrap 到原始错误。
func TestCallErrorWrapping(t *testing.T) {
	root := context.DeadlineExceeded
	err := classifyCallError("srv", "tool", root)
	if !errors.Is(err, root) {
		t.Error("CallError 应能 Unwrap 到原始错误")
	}
	msg := err.Error()
	for _, want := range []string{"srv", "tool", "retryable"} {
		if !strings.Contains(msg, want) {
			t.Errorf("错误信息 %q 缺少 %q", msg, want)
		}
	}
	if !IsRetryable(err) {
		t.Error("IsRetryable 应为 true")
	}
	if k, ok := KindOf(err); !ok || k != KindRetryable {
		t.Errorf("KindOf = %v/%v", k, ok)
	}
	// 普通错误没有分类，IsRetryable 必须返回 false，避免上层对任意错误做重试。
	if IsRetryable(errors.New("plain")) {
		t.Error("普通错误不应被判为可重试")
	}
	if _, ok := KindOf(errors.New("plain")); ok {
		t.Error("普通错误不该有分类")
	}
}
