package mcp

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/cloudwego/eino/components/tool"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// serverConn 是一个 MCP server 的连接。
//
// 同一 server 上的调用被一把互斥锁串行化。理由：MCP 会话是有状态的（服务器可以
// 保存进度、发通知），并行调用带来的收益远小于状态错乱的风险；而不同 server 各有
// 各的锁，跨 server 的并行（eino 的并行 tool call）不受影响。
type serverConn struct {
	cfg         ServerConfig
	dial        dialFunc
	callTimeout time.Duration

	// mu 保护下面的状态字段；临界区必须短，任何网络 I/O 都不能在它里面做。
	mu      sync.Mutex
	sess    *mcpsdk.ClientSession
	entries []*toolEntry
	state   State
	lastErr error

	// callMu 串行化同一 server 上的调用。
	// 与 mu 分开是刻意的：Close 只需要 mu 就能拿到会话与取消函数，
	// 若调用持有 mu 做网络 I/O，关窗口就要等满一个调用超时（可能 60s）。
	callMu sync.Mutex
	// callCtx/cancelCalls 取消当前连接上所有 in-flight 调用。
	// 官方 SDK 的 ClientSession.Close 是"优雅关闭"：它会等正在处理的请求返回。
	// 桌面应用关窗口时不能等一个 60s 的 MCP 调用，所以必须先把调用 cancel 掉，
	// 让 Close 立刻能排空。
	callCtx     context.Context
	cancelCalls context.CancelFunc
}

var errManagerClosed = errors.New("mcp: 连接已关闭")

func (c *serverConn) connect(ctx context.Context, cfg Config) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.connectLocked(ctx, cfg)
}

func (c *serverConn) connectLocked(ctx context.Context, cfg Config) error {
	if c.state == StateClosed {
		return errManagerClosed
	}
	// 重连前先收掉旧会话；连接失败时不能留下"半死"状态，
	// 否则 Tools() 会把不可用的工具继续交给模型。
	//
	// 顺序：先 cancel 旧调用再 Close。ClientSession.Close 会等正在处理的请求
	// 返回，若此时有 in-flight 调用，Close 会一直等到它的超时——而 connectLocked
	// 持有 mu，等于把 Manager.Close 也一起堵死。
	if c.cancelCalls != nil {
		c.cancelCalls()
		c.cancelCalls = nil
	}
	c.callCtx = nil
	if c.sess != nil {
		_ = c.sess.Close()
		c.sess = nil
	}
	c.entries = nil

	dctx, cancel := context.WithTimeout(ctx, cfg.ConnectTimeout)
	defer cancel()

	callCtx, cancelCalls := context.WithCancel(context.Background())
	tr, err := c.dial(dctx, c.cfg)
	if err != nil {
		cancelCalls()
		c.markDownLocked(fmt.Errorf("mcp: server %q 启动 transport 失败: %w", c.cfg.Name, err))
		return c.lastErr
	}
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "zeokeel", Version: "1"}, nil)
	sess, err := client.Connect(dctx, tr, nil)
	if err != nil {
		cancelCalls()
		c.markDownLocked(fmt.Errorf("mcp: server %q 初始化失败: %w", c.cfg.Name, err))
		return c.lastErr
	}
	entries, err := listTools(dctx, c.cfg.Name, sess)
	if err != nil {
		cancelCalls()
		_ = sess.Close()
		c.markDownLocked(fmt.Errorf("mcp: server %q 工具发现失败: %w", c.cfg.Name, err))
		return c.lastErr
	}

	c.callCtx = callCtx
	c.cancelCalls = cancelCalls
	c.sess = sess
	c.entries = entries
	c.state = StateReady
	c.lastErr = nil
	return nil
}

// listTools 用官方 SDK 的分页迭代器取全量工具。
//
// 用迭代器而不是手写 cursor 循环：MCP 的 tools/list 是分页的，server 可以无限
// 返回 nextCursor，手写容易漏页或死循环。
func listTools(ctx context.Context, serverName string, sess *mcpsdk.ClientSession) ([]*toolEntry, error) {
	var out []*toolEntry
	for t, err := range sess.Tools(ctx, nil) {
		if err != nil {
			return nil, err
		}
		entry, err := newToolEntry(serverName, t)
		if err != nil {
			return nil, err
		}
		out = append(out, entry)
	}
	return out, nil
}

// call 执行一次 tools/call。
//
// 返回 isError=true 表示"服务器正常返回了一个工具级错误"（例如文件不存在）。
// 这和传输层失败必须区分：前者要把错误文本喂回模型让它自己纠，后者是链路问题，
// 要标记 server 不健康并交给上层重试。
func (c *serverConn) call(ctx context.Context, toolName, argsJSON string) (text string, isError bool, err error) {
	args, err := decodeArgs(argsJSON)
	if err != nil {
		return "", false, &CallError{Server: c.cfg.Name, Tool: toolName, Kind: KindPermanent, Err: err}
	}

	c.callMu.Lock()
	defer c.callMu.Unlock()

	c.mu.Lock()
	sess, state, connCtx := c.sess, c.state, c.callCtx
	c.mu.Unlock()
	if state != StateReady || sess == nil {
		return "", false, &CallError{
			Server: c.cfg.Name, Tool: toolName, Kind: KindRetryable,
			Err: fmt.Errorf("server 不可用（state=%s）", state),
		}
	}

	cctx, cancel := context.WithTimeout(ctx, c.callTimeout)
	defer cancel()
	if connCtx != nil {
		// 连接被关闭/降级时，AfterFunc 会 cancel 掉这次调用，
		// 从而让 ClientSession.Close 立刻排空而不是等满超时。
		if stop := context.AfterFunc(connCtx, cancel); stop != nil {
			defer stop()
		}
	}
	res, callErr := sess.CallTool(cctx, &mcpsdk.CallToolParams{Name: toolName, Arguments: args})
	if callErr != nil {
		ce := classifyCallError(c.cfg.Name, toolName, callErr)
		// 只有链路级失败才降级。参数错误之类的永久错误不该把整个 server 拉黑。
		if ce.Kind == KindRetryable {
			c.mu.Lock()
			c.markDownLocked(callErr)
			c.mu.Unlock()
		}
		return "", false, ce
	}

	return flattenResult(res), res.IsError, nil
}

// einoTools 把当前工具包装成 eino 的工具。
//
// 每次调用都新建包装对象（而不是缓存），因为 cfg 里的截断预算可能被上层调整；
// 包装对象很轻，只持有指针与配置副本。
func (c *serverConn) einoTools(cfg *Config) []tool.BaseTool {
	c.mu.Lock()
	entries, state := c.entries, c.state
	c.mu.Unlock()
	if state != StateReady || len(entries) == 0 {
		return nil
	}
	out := make([]tool.BaseTool, 0, len(entries))
	for _, e := range entries {
		out = append(out, &serverTool{conn: c, entry: e, cfg: *cfg})
	}
	return out
}

func (c *serverConn) close() error {
	c.mu.Lock()
	c.state = StateClosed
	c.entries = nil
	sess := c.sess
	cancelCalls := c.cancelCalls
	c.sess = nil
	c.callCtx = nil
	c.cancelCalls = nil
	c.mu.Unlock()

	// 顺序很重要：先 cancel in-flight 调用，再优雅关闭会话。
	// 反过来会卡在"等待请求返回"上（SDK 的 Close 语义）。
	if cancelCalls != nil {
		cancelCalls()
	}
	if sess != nil {
		return sess.Close()
	}
	return nil
}

func (c *serverConn) currentState() State {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state
}

func (c *serverConn) markDownLocked(cause error) {
	if c.state == StateClosed {
		return
	}
	c.state = StateDown
	c.lastErr = cause
	if c.cancelCalls != nil {
		c.cancelCalls()
		c.cancelCalls = nil
	}
	c.callCtx = nil
	if c.sess != nil {
		_ = c.sess.Close()
		c.sess = nil
	}
}
