package mcp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// ErrorKind 是 MCP 调用失败的两分类。
//
// 为什么要分类：无人值守任务里"重试"和"放弃"必须是显式决策。把超时/连接断
// 重试掉能救回一个跑了几十分钟的 Ticket；而对"参数非法"重试只会把同一堵墙撞穿。
type ErrorKind int

const (
	// KindPermanent 表示重试没有意义（参数错、方法不存在、调用方主动取消）。
	KindPermanent ErrorKind = iota
	// KindRetryable 表示链路级问题，重连或稍后重试有意义。
	KindRetryable
)

func (k ErrorKind) String() string {
	if k == KindRetryable {
		return "retryable"
	}
	return "permanent"
}

// CallError 是一次 MCP 工具调用的失败，带 server/tool 与分类，
// 让上层（面板、重试策略）不必解析错误字符串。
type CallError struct {
	Server string
	Tool   string
	Kind   ErrorKind
	Err    error
}

func (e *CallError) Error() string {
	return fmt.Sprintf("mcp: server %q tool %q 调用失败（%s）: %v", e.Server, e.Tool, e.Kind, e.Err)
}

func (e *CallError) Unwrap() error { return e.Err }

func classifyCallError(server, tool string, err error) *CallError {
	return &CallError{Server: server, Tool: tool, Kind: classify(err), Err: err}
}

// classify 把底层错误归到可重试/永久两类。
//
// 默认落在"可重试"：未知错误更可能是链路抖动而不是参数问题，且重试预算由上层
// 控制，不会无限循环；而把一次可恢复的抖动误判成永久失败，代价是整个 Ticket 失败。
func classify(err error) ErrorKind {
	if err == nil {
		return KindPermanent
	}
	// 1) 调用方主动取消：重试等于违背意图（人可能刚点了"停止"）。
	if errors.Is(err, context.Canceled) {
		return KindPermanent
	}
	// 2) 超时：server 冷启动/忙，重连或重试有意义。
	if errors.Is(err, context.DeadlineExceeded) {
		return KindRetryable
	}
	// 3) 连接被关闭 / EOF / 管道断开：server 进程挂了，重连后可以重试。
	if errors.Is(err, mcpsdk.ErrConnectionClosed) ||
		errors.Is(err, io.EOF) ||
		errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, net.ErrClosed) {
		return KindRetryable
	}
	// 4) JSON-RPC 协议级错误：参数/方法错重试无意义；其余（如 internal error）
	//    可能是 server 临时状态，按可重试处理。
	var wireErr *jsonrpc.Error
	if errors.As(err, &wireErr) {
		switch wireErr.Code {
		case jsonrpc.CodeParseError, jsonrpc.CodeMethodNotFound, jsonrpc.CodeInvalidParams:
			return KindPermanent
		}
		return KindRetryable
	}
	// 5) 兜底：SDK 底层有些连接错误是 fmt.Errorf 包出来的，只有文本可认。
	msg := strings.ToLower(err.Error())
	for _, needle := range []string{
		"connection closed", "broken pipe", "connection reset",
		"use of closed network connection", "eof", "transport",
	} {
		if strings.Contains(msg, needle) {
			return KindRetryable
		}
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return KindRetryable
	}
	return KindRetryable
}

// IsRetryable 判断一个错误（通常是 CallError）是否值得重试。
func IsRetryable(err error) bool {
	k, ok := KindOf(err)
	return ok && k == KindRetryable
}

// KindOf 取出错误里的分类；不是 CallError 时返回 false。
func KindOf(err error) (ErrorKind, bool) {
	var ce *CallError
	if errors.As(err, &ce) {
		return ce.Kind, true
	}
	return KindPermanent, false
}
