package dingtalk

// DingTalk DWS 传输（DingTalk Workspace 网关 CLI）。
//
// 为什么是外部命令：DWS 是一个由企业安装/登录的本地网关 CLI，负责钉钉的
// 授权与消息收发（登录态、令牌刷新都在它那里）。ZeoKeel 不重复实现登录，
// 只承诺适配规格里那组 JSON 命令（audit/fixes/im-channel-spec.md §2）：
//
//	dws auth status --json
//	dws poll --cursor <c> --limit 50 --json
//	dws send --conversation <id> --text <t> --idempotency-key <k> --json
//	dws health --json
//
// 三条纪律：
//  1. 每渠道一个串行 runner：poll tick、send、auth check 共用一条队列，同一
//     时刻最多一个子进程（登录态/网关不能被打断）；
//  2. 永不经过 shell：exec.CommandContext + args 数组，消息文本不会被解释；
//  3. 游标由调用方（runtime）拥有：DWSTransport 只回传"下一轮起点"，持久化
//     由 SubscribeFrom 的 save 回调完成，且只在整批交付成功后调用。
//
// 健康语义（见 DWSHealth）：state 为 ok / auth / error / starting；auth 时带
// 可打开的 authUrl 或登录命令，绝不静默 fallback 到 Stream。

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/zeokeel/super-agent/internal/im"
)

var (
	_ Transport     = (*DWSTransport)(nil)
	_ StreamSupport = (*DWSTransport)(nil)
	_ StreamCloser  = (*DWSTransport)(nil)
	_ im.TextSender = (*DWSTransport)(nil)
	_ DWSClient     = (*commandDWSClient)(nil)
)

const (
	// defaultDWSTimeout 是单次 CLI 调用的硬超时（规格 §2）。
	defaultDWSTimeout = 30 * time.Second
	// defaultDWSPollInterval 是两次 poll 之间的最小间隔（规格 §2：2s tick）。
	defaultDWSPollInterval = 2 * time.Second
	// defaultDWSPollLimit 是每次 poll 的条数上限（规格 §2：50）。
	defaultDWSPollLimit = 50
	// maxDWSPollLimit 是 CLI 侧允许的最大 limit：再大网关也不保证。
	maxDWSPollLimit = 100
	// maxDWSMessageIDs 是进程内"已交付消息 id"的上限（重放只可能发生在最近
	// 一小段，512 足够，又不会让内存无界增长）。
	maxDWSMessageIDs = 512
	// maxDWSMentions 是单条消息 @ 列表的上限（协议不可能有几百个 @）。
	maxDWSMentions = 64
	// maxDWSDetail 是 stderr/详情进入 health 的截断长度。
	maxDWSDetail = 300
	// dwsWaitDelay 是子进程被杀后等待其 I/O 管道关闭的上限。DWS 若是 shell
	// 包装脚本，被杀的是 shell，子进程可能还握着 stdout——不设 WaitDelay 会让
	// cmd.Run 卡到子进程结束（取消/超时就失去意义）。
	dwsWaitDelay = 500 * time.Millisecond
)

// ErrDWSClosed 表示传输已关闭（Subscribe 退出后继续 poll 是编程错误）。
var ErrDWSClosed = errors.New("dingtalk: dws transport is closed")

// ErrDWSAuthRequired 表示 DWS 未登录：需要用户运行 `dws auth login` 或打开
// authUrl。调用方应把它映射成渠道状态 auth，而不是普通网络错误。
var ErrDWSAuthRequired = errors.New("dingtalk: dws is not logged in")

// ErrDWSUndefined 表示 DWS 没有实现某个能力（例如 health 子命令）。
var ErrDWSUndefined = errors.New("dingtalk: dws does not support this command")

// DWSConfig 是 command-backed DWS 客户端的配置。
type DWSConfig struct {
	// Command 是 dws 可执行文件/包装脚本；空用 "dws"（从 PATH 找）。
	Command string
	// Timeout 是单次调用超时；<=0 用 30s。
	Timeout time.Duration
	// PollInterval 是两次 poll 之间的最小间隔；<=0 用 2s。
	PollInterval time.Duration
	// PollLimit 是 poll 条数；<=0 用 50，>100 截到 100。
	PollLimit int
	// Backoff/MaxBackoff 是 poll 失败后的退避（1s→30s）。
	Backoff, MaxBackoff time.Duration
}

func (c DWSConfig) withDefaults() DWSConfig {
	c.Command = strings.TrimSpace(c.Command)
	if c.Command == "" {
		c.Command = "dws"
	}
	if c.Timeout <= 0 {
		c.Timeout = defaultDWSTimeout
	}
	if c.PollInterval <= 0 {
		c.PollInterval = defaultDWSPollInterval
	}
	if c.PollLimit <= 0 {
		c.PollLimit = defaultDWSPollLimit
	}
	if c.PollLimit > maxDWSPollLimit {
		c.PollLimit = maxDWSPollLimit
	}
	if c.Backoff <= 0 {
		c.Backoff = time.Second
	}
	if c.MaxBackoff <= 0 {
		c.MaxBackoff = 30 * time.Second
	}
	if c.MaxBackoff < c.Backoff {
		c.MaxBackoff = c.Backoff
	}
	return c
}

// AuthStatus 是 `dws auth status --json` 的结果。
type AuthStatus struct {
	LoggedIn bool   `json:"loggedIn"`
	Account  string `json:"account"`
	AuthURL  string `json:"authUrl"`
}

// DWSMessage 是一条 DWS 轮询消息（已解析、已校验）。
type DWSMessage struct {
	ID               string
	ConversationID   string
	ConversationType string // "group" | "direct"（已归一化小写）
	ConversationName string
	SenderID         string
	SenderName       string
	SenderBot        bool
	Text             string
	At               time.Time
	Mentions         []string
	ReplyToBot       bool
}

// PollPage 是一轮 DWS poll 的结果。
type PollPage struct {
	Cursor   string
	Messages []DWSMessage
	// Skipped 是坏 item 数（缺 id/会话/时间、类型非法……）。逐条跳过而不是
	// 整批失败：一条坏消息不该让别人能干活的消息一起丢（与其他 driver 一致）。
	Skipped int
}

// DWSHealth 是渠道级健康（规格 §2：ok / auth / error；starting 为初始态）。
type DWSHealth struct {
	State    string // "ok" | "auth" | "error" | "starting"
	Detail   string
	AuthURL  string
	LoggedIn bool
	Account  string
}

// DWSClient 是 DWS CLI 的最小客户端契约。command 实现见 commandDWSClient；
// 测试可注入假实现，从而在无网络/无 dws 的机器上验证整条链路。
type DWSClient interface {
	AuthStatus(ctx context.Context) (AuthStatus, error)
	Poll(ctx context.Context, cursor string, limit int) (PollPage, error)
	Send(ctx context.Context, conversationID, text, idempotencyKey string) (messageID string, err error)
	Health(ctx context.Context) (DWSHealth, error)
}

// DWSError 是一次 DWS 调用失败。Status 是尽量解析出的 HTTP 状态码（0=未知）；
// Permanent 表示 4xx 一类的"重试也不会好"（或命令不存在/参数非法）。
type DWSError struct {
	Op        string
	Status    int
	Permanent bool
	Detail    string
	AuthURL   string
	Err       error
}

// Error 实现 error。
func (e *DWSError) Error() string {
	var b strings.Builder
	b.WriteString("dingtalk: dws ")
	if e.Op != "" {
		b.WriteString(e.Op)
		b.WriteString(": ")
	}
	if e.Status != 0 {
		fmt.Fprintf(&b, "HTTP %d: ", e.Status)
	}
	if e.Detail != "" {
		b.WriteString(e.Detail)
	} else if e.Err != nil {
		b.WriteString(e.Err.Error())
	} else {
		b.WriteString("failed")
	}
	return b.String()
}

// Unwrap 让 errors.Is/As 能穿过错误链。
func (e *DWSError) Unwrap() error { return e.Err }

// Temporary 实现 im.IsTransient 的接口：只有"可能自己好起来"的失败才重试。
func (e *DWSError) Temporary() bool {
	if e.Permanent {
		return false
	}
	// 调用方取消不是"会自己好"的失败；超时（网关慢）是，交给退避重试。
	if e.Err != nil && errors.Is(e.Err, context.Canceled) {
		return false
	}
	return true
}

// IsPermanent 报告错误是否是永久失败（4xx/命令缺失/参数非法）。没有实现
// Temporary 的错误按"未知"处理，返回 false（交给 Outbox 的默认策略重试）。
func IsPermanent(err error) bool {
	var de *DWSError
	if errors.As(err, &de) {
		return de.Permanent
	}
	return false
}

// truncateDetail 截断进入 health/错误文本的 stderr，避免日志爆炸。
func truncateDetail(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > maxDWSDetail {
		return s[:maxDWSDetail] + "…"
	}
	return s
}

// --- command-backed DWS client ---

// commandDWSClient 通过 exec.CommandContext 调 dws（不经过 shell）。
type commandDWSClient struct {
	cfg DWSConfig
}

// NewCommandDWSClient 构造命令行 DWS 客户端。command 不存在/不可执行只在真正
// 调用时才会暴露（构造不做 I/O，便于启动不因外部工具缺失而阻塞）。
func NewCommandDWSClient(cfg DWSConfig) DWSClient {
	return &commandDWSClient{cfg: cfg.withDefaults()}
}

// opError 把一次子进程失败归一成 DWSError。
func (c *commandDWSClient) opError(op string, err error, stderr, stdout []byte) error {
	detail := truncateDetail(string(stderr))
	if detail == "" {
		detail = truncateDetail(string(stdout))
	}
	de := &DWSError{Op: op, Detail: detail, Err: err}
	// 超时/取消是可重试的（ctx 生命周期由调用方决定），但不是 4xx。
	if errors.Is(err, context.DeadlineExceeded) {
		return de
	}
	if errors.Is(err, context.Canceled) {
		de.Permanent = true // 调用方自己取消，不重试
		return de
	}
	// 命令不存在/不可执行：永久错误（重试只会重复失败），detail 可操作。
	// 不同平台/调用方式会给出 *exec.Error、*fs.PathError 或裸 exec.ErrNotFound，
	// 三种都要归类，否则 health 会把"dws 未安装"当成可重试抖动。
	const notExecutable = "dws 未安装或不可执行（not executable）"
	var execErr *exec.Error
	if errors.As(err, &execErr) {
		de.Permanent = true
		de.Detail = notExecutable + ": " + execErr.Error()
		return de
	}
	var pathErr *fs.PathError
	if errors.As(err, &pathErr) && (errors.Is(pathErr, fs.ErrNotExist) || errors.Is(err, fs.ErrNotExist)) {
		de.Permanent = true
		de.Detail = notExecutable + ": " + pathErr.Error()
		return de
	}
	if errors.Is(err, exec.ErrNotFound) || errors.Is(err, fs.ErrNotExist) {
		de.Permanent = true
		de.Detail = notExecutable + ": " + err.Error()
		return de
	}
	if status := detectDWSStatus(stderr, stdout); status != 0 {
		de.Status = status
		de.Permanent = status >= 400 && status < 500
		if status == 401 || status == 403 {
			de.AuthURL = extractURL(detail)
			de.Err = ErrDWSAuthRequired
		}
		return de
	}
	// 未知退出码：按可重试处理（与"宁可重试也不丢"的仓规一致）。
	return de
}

// run 执行一次 dws 调用，返回 stdout。超时由 cfg.Timeout 兜底；ctx 取消会
// 立刻杀掉子进程（exec.CommandContext 语义）。
func (c *commandDWSClient) run(ctx context.Context, op string, args ...string) ([]byte, error) {
	if c.cfg.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.cfg.Timeout)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, c.cfg.Command, args...)
	// 显式 Cancel（默认也是 Kill）+ WaitDelay：ctx 取消/超时后最多再等
	// dwsWaitDelay 让 os/exec 的 I/O goroutine 退出，调用方不会被 Shell 包装
	// 脚本留下的子进程拖住。
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return cmd.Process.Kill()
	}
	cmd.WaitDelay = dwsWaitDelay
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		// 超时/取消时子进程被 SIGKILL，cmd.Run 返回的是 "signal: killed"；
		// 用 ctx.Err() 才能拿到 DeadlineExceeded/Canceled 的语义。
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, c.opError(op, ctxErr, stderr.Bytes(), stdout.Bytes())
		}
		return nil, c.opError(op, err, stderr.Bytes(), stdout.Bytes())
	}
	return stdout.Bytes(), nil
}

// AuthStatus 实现 DWSClient：`dws auth status --json`。
func (c *commandDWSClient) AuthStatus(ctx context.Context) (AuthStatus, error) {
	out, err := c.run(ctx, "auth status", "auth", "status", "--json")
	if err != nil {
		return AuthStatus{}, err
	}
	var st AuthStatus
	if err := json.Unmarshal(out, &st); err != nil {
		return AuthStatus{}, &DWSError{Op: "auth status", Detail: "unreadable JSON: " + truncateDetail(string(out)), Err: err}
	}
	return st, nil
}

// Health 实现 DWSClient：`dws health --json`。命令不存在时返回
// ErrDWSUndefined，让上层降级为"用 auth status 判断"。
func (c *commandDWSClient) Health(ctx context.Context) (DWSHealth, error) {
	out, err := c.run(ctx, "health", "health", "--json")
	if err != nil {
		var de *DWSError
		if errors.As(err, &de) && de.Status == 0 && !de.Permanent && de.Detail != "" {
			// 老版本 dws 可能没有 health 子命令：当作"可选能力未实现"。
			return DWSHealth{}, fmt.Errorf("%w: %v", ErrDWSUndefined, err)
		}
		return DWSHealth{}, err
	}
	var wire struct {
		OK     bool   `json:"ok"`
		Detail string `json:"detail"`
	}
	if err := json.Unmarshal(out, &wire); err != nil {
		return DWSHealth{}, &DWSError{Op: "health", Detail: "unreadable JSON: " + truncateDetail(string(out)), Err: err}
	}
	state := "ok"
	if !wire.OK {
		state = "error"
	}
	return DWSHealth{State: state, Detail: wire.Detail}, nil
}

// pollWire 是 `dws poll --json` 的线上形状。
type pollWire struct {
	Cursor   string `json:"cursor"`
	Messages []struct {
		ID           string `json:"id"`
		Conversation struct {
			ID   string `json:"id"`
			Type string `json:"type"`
			Name string `json:"name"`
		} `json:"conversation"`
		Sender struct {
			ID   string `json:"id"`
			Name string `json:"name"`
			Bot  bool   `json:"bot"`
		} `json:"sender"`
		Text       string   `json:"text"`
		At         string   `json:"at"`
		Mentions   []string `json:"mentions"`
		ReplyToBot bool     `json:"replyToBot"`
	} `json:"messages"`
}

// Poll 实现 DWSClient：`dws poll --cursor <c> --limit <n> --json`。
func (c *commandDWSClient) Poll(ctx context.Context, cursor string, limit int) (PollPage, error) {
	if limit <= 0 {
		limit = c.cfg.PollLimit
	}
	if limit > maxDWSPollLimit {
		limit = maxDWSPollLimit
	}
	args := []string{"poll"}
	if cursor != "" {
		args = append(args, "--cursor", cursor)
	}
	args = append(args, "--limit", strconv.Itoa(limit), "--json")
	out, err := c.run(ctx, "poll", args...)
	if err != nil {
		return PollPage{}, err
	}
	var wire pollWire
	if err := json.Unmarshal(out, &wire); err != nil {
		return PollPage{}, &DWSError{Op: "poll", Detail: "unreadable JSON: " + truncateDetail(string(out)), Err: err}
	}
	page := PollPage{Cursor: wire.Cursor}
	for _, item := range wire.Messages {
		msg, ok := parseDWSMessage(item.ID, item.Conversation.ID, item.Conversation.Type, item.Conversation.Name,
			item.Sender.ID, item.Sender.Name, item.Sender.Bot, item.Text, item.At, item.Mentions, item.ReplyToBot)
		if !ok {
			page.Skipped++
			continue
		}
		page.Messages = append(page.Messages, msg)
	}
	return page, nil
}

// Send 实现 DWSClient：`dws send ...`，文本尾部附加隐藏 marker。
//
// 参数校验失败按永久错误返回（重试不会改变参数）。
func (c *commandDWSClient) Send(ctx context.Context, conversationID, text, idempotencyKey string) (string, error) {
	conversationID = strings.TrimSpace(conversationID)
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if conversationID == "" {
		return "", &DWSError{Op: "send", Permanent: true, Detail: "conversation id is empty"}
	}
	if idempotencyKey == "" {
		return "", &DWSError{Op: "send", Permanent: true, Detail: "idempotency key is empty"}
	}
	marked := text + markerForDWS(idempotencyKey)
	args := []string{"send", "--conversation", conversationID, "--text", marked, "--idempotency-key", idempotencyKey, "--json"}
	out, err := c.run(ctx, "send", args...)
	if err != nil {
		return "", err
	}
	var wire struct {
		MessageID string `json:"messageId"`
	}
	if err := json.Unmarshal(out, &wire); err != nil {
		return "", &DWSError{Op: "send", Detail: "unreadable JSON: " + truncateDetail(string(out)), Err: err}
	}
	return wire.MessageID, nil
}

// markerForDWS 是文本尾部的隐藏去重标记（规格 §2）。用 HTML 注释而不是零宽
// 字符：DWS/钉钉的可读文本里不可见，且审计/读回时一眼能认出。
func markerForDWS(key string) string {
	// marker 是 HTML 注释：注释内部不能出现 "--"，也不能有控制/行分隔字符。
	// 普通业务 key 原样保留（可读、可 grep）；含危险字符时整键 hex 编码，
	// 审计时仍可反查（hex 是确定的）。
	safe := strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\r' || r == '\t':
			return ' '
		case r == '\u2028' || r == '\u2029':
			return ' '
		case unicode.IsControl(r) || unicode.Is(unicode.Cf, r):
			return ' '
		default:
			return r
		}
	}, key)
	safe = strings.TrimSpace(safe)
	if safe == "" {
		safe = "unknown"
	}
	if strings.Contains(safe, "--") || strings.ContainsAny(safe, "<>") {
		safe = hex.EncodeToString([]byte(key))
	}
	return "\n\n<!-- zeokeel:" + safe + " -->"
}

// parseDWSMessage 校验并归一一条线上消息；坏 item 返回 ok=false（调用方计数）。
func parseDWSMessage(id, convID, convType, convName, senderID, senderName string, senderBot bool,
	text, at string, mentions []string, replyToBot bool) (DWSMessage, bool) {
	id = strings.TrimSpace(id)
	convID = strings.TrimSpace(convID)
	if id == "" || convID == "" {
		return DWSMessage{}, false
	}
	if len(id) > 256 || len(convID) > 256 {
		return DWSMessage{}, false
	}
	kind := strings.ToLower(strings.TrimSpace(convType))
	if kind != "group" && kind != "direct" {
		return DWSMessage{}, false
	}
	when, err := time.Parse(time.RFC3339, strings.TrimSpace(at))
	if err != nil {
		return DWSMessage{}, false
	}
	if when.IsZero() {
		return DWSMessage{}, false
	}
	if len(text) > maxStreamContentBytes {
		return DWSMessage{}, false
	}
	clean := make([]string, 0, len(mentions))
	for _, mention := range mentions {
		mention = strings.TrimSpace(mention)
		if mention == "" {
			continue
		}
		if len(clean) == maxDWSMentions {
			break
		}
		clean = append(clean, mention)
	}
	return DWSMessage{
		ID:               id,
		ConversationID:   convID,
		ConversationType: kind,
		ConversationName: convName,
		SenderID:         strings.TrimSpace(senderID),
		SenderName:       senderName,
		SenderBot:        senderBot,
		Text:             text,
		At:               when,
		Mentions:         clean,
		ReplyToBot:       replyToBot,
	}, true
}

// statusPattern 从 CLI 的 stderr/stdout 文本里抓 HTTP 状态码（DWS 契约没有结构化
// 错误码，只能兼容常见渲染：`HTTP 404` / `status: 503` / `"status":404`）。
var statusPattern = regexp.MustCompile(`(?i)(?:HTTP[ /]?|status["']?\s*[:=]\s*|"code"\s*:\s*)[" ]?(\d{3})`)

func detectDWSStatus(stderr, stdout []byte) int {
	for _, blob := range [][]byte{stderr, stdout} {
		if m := statusPattern.FindSubmatch(blob); m != nil {
			if n, err := strconv.Atoi(string(m[1])); err == nil && n >= 400 && n <= 599 {
				return n
			}
		}
	}
	return 0
}

var urlPattern = regexp.MustCompile(`https?://[^\s"'<>]+`)

func extractURL(text string) string {
	if m := urlPattern.FindString(text); m != "" {
		return m
	}
	return ""
}

// --- serialized runner ---

// dwsJob 是 runner 队列里的一项。
type dwsJob struct {
	ctx      context.Context
	priority int
	seq      int64
	fn       func(context.Context) (any, error)
	result   chan dwsJobResult
}

type dwsJobResult struct {
	value any
	err   error
}

// 优先级：send 优先于 auth/health，再优先于 poll。
const (
	dwsPrioritySend = iota
	dwsPriorityAuth
	dwsPriorityPoll
)

// dwsRunner 是"每渠道一个串行 worker"的实现：所有 CLI 调用进同一队列，同一
// 时刻只有一个子进程。send 插到 poll 前面（登录态/回执优先），同优先级 FIFO。
type dwsRunner struct {
	mu       sync.Mutex
	cond     *sync.Cond
	queue    []*dwsJob
	started  bool
	closed   bool
	current  context.CancelFunc
	timeout  time.Duration
	seq      int64
	finished chan struct{}
}

func newDWSRunner(timeout time.Duration) *dwsRunner {
	if timeout <= 0 {
		timeout = defaultDWSTimeout
	}
	r := &dwsRunner{timeout: timeout, finished: make(chan struct{})}
	r.cond = sync.NewCond(&r.mu)
	return r
}

// Do 把一个调用排进串行队列并等结果。ctx 取消时立即返回（正在跑的子进程由
// job ctx 杀掉；尚未开跑的 job 在轮到时直接失败）。
func (r *dwsRunner) Do(ctx context.Context, priority int, fn func(context.Context) (any, error)) (any, error) {
	job := &dwsJob{ctx: ctx, priority: priority, fn: fn, result: make(chan dwsJobResult, 1)}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil, ErrDWSClosed
	}
	if !r.started {
		r.started = true
		go r.loop()
	}
	r.seq++
	job.seq = r.seq
	r.enqueueLocked(job)
	r.cond.Signal()
	r.mu.Unlock()

	select {
	case res := <-job.result:
		return res.value, res.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (r *dwsRunner) enqueueLocked(job *dwsJob) {
	at := len(r.queue)
	for i, other := range r.queue {
		if other.priority > job.priority {
			at = i
			break
		}
	}
	r.queue = append(r.queue, nil)
	copy(r.queue[at+1:], r.queue[at:])
	r.queue[at] = job
}

// Stop 关闭 runner：取消正在跑的子进程，让排队中的调用失败。
func (r *dwsRunner) Stop() {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return
	}
	r.closed = true
	if r.current != nil {
		r.current()
	}
	started := r.started
	r.cond.Broadcast()
	r.mu.Unlock()
	if started {
		<-r.finished
	}
}

func (r *dwsRunner) loop() {
	defer close(r.finished)
	for {
		r.mu.Lock()
		for len(r.queue) == 0 && !r.closed {
			r.cond.Wait()
		}
		if r.closed {
			pending := r.queue
			r.queue = nil
			r.mu.Unlock()
			for _, job := range pending {
				select {
				case job.result <- dwsJobResult{err: ErrDWSClosed}:
				default:
				}
			}
			return
		}
		job := r.queue[0]
		r.queue = r.queue[1:]
		r.mu.Unlock()

		if err := job.ctx.Err(); err != nil {
			job.result <- dwsJobResult{err: err}
			continue
		}
		callCtx, cancel := context.WithTimeout(job.ctx, r.timeout)
		r.mu.Lock()
		r.current = cancel
		r.mu.Unlock()
		value, err := job.fn(callCtx)
		cancel()
		r.mu.Lock()
		r.current = nil
		r.mu.Unlock()
		select {
		case job.result <- dwsJobResult{value: value, err: err}:
		default:
			// 调用方已经因 ctx 取消离开；结果无人接收是正常情况。
		}
	}
}

// --- DWSTransport ---

// DWSTransport 实现钉钉 Transport 契约：接收走 DWS poll（StreamEvents 拉模式），
// 发送走 DWS send（TextSender），令牌/成员由 DWS 自己管理（不实现）。
type DWSTransport struct {
	*HTTPTransport

	client DWSClient
	runner *dwsRunner
	cfg    DWSConfig
	now    func() time.Time

	mu       sync.Mutex
	seen     map[string]struct{}
	seenList []string
	drops    int
	dups     int
	closed   bool
	status   func(state im.HealthState, detail string)
	health   DWSHealth
	lastPoll time.Time
	failures int
	backoff  time.Duration
}

// NewDWSTransport 构造 DWS 传输。httpTr 可为 nil（DWS 自己管登录时不需要
// HTTP 令牌）；保留它只是为了复用现有 driver 的成员/健康接口。
func NewDWSTransport(httpTr *HTTPTransport, client DWSClient, cfg DWSConfig) (*DWSTransport, error) {
	if client == nil {
		return nil, errors.New("dingtalk: dws transport needs a client")
	}
	cfg = cfg.withDefaults()
	t := &DWSTransport{
		HTTPTransport: httpTr,
		client:        client,
		runner:        newDWSRunner(cfg.Timeout),
		cfg:           cfg,
		now:           time.Now,
		seen:          map[string]struct{}{},
		backoff:       cfg.Backoff,
		health:        DWSHealth{State: "starting"},
	}
	return t, nil
}

// SupportsStream 实现 StreamSupport：DWS 能接收，Subscribe 走轮询循环。
func (*DWSTransport) SupportsStream() bool { return true }

// SetStatus 注入健康回调（driver 用来把状态写进面板）。
func (t *DWSTransport) SetStatus(fn func(state im.HealthState, detail string)) {
	t.mu.Lock()
	t.status = fn
	t.mu.Unlock()
}

// Dropped 返回被丢弃的坏消息数（解析失败/超限），Duplicates 返回重放去重数。
func (t *DWSTransport) Dropped() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.drops
}

// Duplicates 返回按消息 id 去重掉的重放数。
func (t *DWSTransport) Duplicates() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.dups
}

// Close 实现 StreamCloser：停 runner、取消在跑的子进程。
func (t *DWSTransport) Close() error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil
	}
	t.closed = true
	t.mu.Unlock()
	t.runner.Stop()
	return nil
}

// StreamEvents 实现 Transport：拉一轮 DWS 消息，映射成驱动层 Event。
//
// 时序：两次 poll 之间至少 PollInterval；poll 失败按 Backoff/MaxBackoff 记录
// 退避（下一次调用先等退避）。cursor 原样传给 CLI，返回的下一轮起点由调用方
// 持久化（driver.SubscribeFrom 的 SaveCursor）。
func (t *DWSTransport) StreamEvents(ctx context.Context, cursor string) (EventsPage, error) {
	if err := t.waitTurn(ctx); err != nil {
		return EventsPage{}, err
	}
	page, err := t.poll(ctx, cursor)
	if err != nil {
		t.recordFailure(err)
		return EventsPage{}, err
	}
	events := t.mapPage(page)
	t.recordSuccess(len(events))
	return EventsPage{Events: events, Cursor: t.nextCursor(cursor, page)}, nil
}

// SendText 实现 im.TextSender：DWS send 的文本 + 业务幂等键。
func (t *DWSTransport) SendText(ctx context.Context, conversationID, text, idempotencyKey string) (string, error) {
	if strings.TrimSpace(idempotencyKey) == "" {
		return "", &DWSError{Op: "send", Permanent: true, Detail: "idempotency key is empty"}
	}
	value, err := t.runner.Do(ctx, dwsPrioritySend, func(ctx context.Context) (any, error) {
		return t.client.Send(ctx, conversationID, text, idempotencyKey)
	})
	if err != nil {
		return "", err
	}
	id, _ := value.(string)
	return id, nil
}

// AuthStatus 实现 DWS 授权检查（供 runtime 的"检查/授权"按钮使用）。
func (t *DWSTransport) AuthStatus(ctx context.Context) (AuthStatus, error) {
	value, err := t.runner.Do(ctx, dwsPriorityAuth, func(ctx context.Context) (any, error) {
		return t.client.AuthStatus(ctx)
	})
	if err != nil {
		return AuthStatus{}, err
	}
	st, ok := value.(AuthStatus)
	if !ok {
		return AuthStatus{}, &DWSError{Op: "auth status", Detail: "client returned an unexpected value"}
	}
	t.mu.Lock()
	t.health.LoggedIn = st.LoggedIn
	t.health.Account = st.Account
	t.health.AuthURL = st.AuthURL
	t.mu.Unlock()
	return st, nil
}

// DWSHealth 是渠道级健康：优先 `dws health`，失败或未实现时用 auth status 判断。
func (t *DWSTransport) DWSHealth(ctx context.Context) (DWSHealth, error) {
	value, err := t.runner.Do(ctx, dwsPriorityAuth, func(ctx context.Context) (any, error) {
		return t.client.Health(ctx)
	})
	if err == nil {
		if h, ok := value.(DWSHealth); ok {
			if h.State == "" {
				h.State = "ok"
			}
			t.setHealthState(h.State, h.Detail, h.AuthURL)
			return h, nil
		}
	}
	// health 命令坏了/未实现不是致命：用 auth status 判断登录态即可（错误细节
	// 已通过 status 回调/日志路径暴露；这里不把渠道判死）。
	status, authErr := t.AuthStatus(ctx)
	if authErr != nil {
		h := DWSHealth{State: "error", Detail: "dws auth status failed: " + truncateDetail(authErr.Error())}
		t.setHealthState(h.State, h.Detail, "")
		return h, authErr
	}
	h := DWSHealth{State: "ok", Detail: "dws is logged in as " + status.Account, LoggedIn: status.LoggedIn, Account: status.Account}
	if !status.LoggedIn {
		h.State = "auth"
		h.AuthURL = status.AuthURL
		h.Detail = dwsLoginDetail(status.AuthURL)
	}
	t.setHealthState(h.State, h.Detail, h.AuthURL)
	return h, nil
}

// SendRobot 实现 Transport，但 DWS 的发送只有"文本 + 幂等键"形态：
// 驱动应走 SendText（im.TextSender）。这里明确报不支持，避免用错误的
// 模板参数/隐藏 marker 拼出一条 DWS 不认识的命令。
func (t *DWSTransport) SendRobot(context.Context, SendRequest) (SendResult, error) {
	return SendResult{}, fmt.Errorf("%w: dws uses SendText", im.ErrUnsupported)
}

// AccessToken 实现 Transport：登录态归 DWS 管理，ZeoKeel 不持有令牌。
func (t *DWSTransport) AccessToken(context.Context) (Token, error) {
	return Token{}, fmt.Errorf("%w: dws manages its own login", im.ErrUnsupported)
}

// ListMembers 实现 Transport：DWS 契约没有成员查询。
func (t *DWSTransport) ListMembers(context.Context, string, string) (MembersPage, error) {
	return MembersPage{}, fmt.Errorf("%w: dws has no member listing", im.ErrUnsupported)
}

func (t *DWSTransport) poll(ctx context.Context, cursor string) (PollPage, error) {
	value, err := t.runner.Do(ctx, dwsPriorityPoll, func(ctx context.Context) (any, error) {
		return t.client.Poll(ctx, cursor, t.cfg.PollLimit)
	})
	if err != nil {
		return PollPage{}, err
	}
	page, ok := value.(PollPage)
	if !ok {
		return PollPage{}, &DWSError{Op: "poll", Detail: "client returned an unexpected value"}
	}
	return page, nil
}

// mapPage 把消息映射成 Event 并按消息 id 去重（有界集合）。
func (t *DWSTransport) mapPage(page PollPage) []Event {
	out := make([]Event, 0, len(page.Messages))
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, msg := range page.Messages {
		if _, ok := t.seen[msg.ID]; ok {
			t.dups++
			continue
		}
		t.seen[msg.ID] = struct{}{}
		t.seenList = append(t.seenList, msg.ID)
		if len(t.seenList) > maxDWSMessageIDs {
			delete(t.seen, t.seenList[0])
			t.seenList = t.seenList[1:]
		}
		out = append(out, eventFromDWS(msg))
	}
	t.drops += page.Skipped
	return out
}

func (t *DWSTransport) nextCursor(previous string, page PollPage) string {
	if strings.TrimSpace(page.Cursor) == "" {
		return previous
	}
	return page.Cursor
}

func (t *DWSTransport) waitTurn(ctx context.Context) error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return ErrDWSClosed
	}
	wait := time.Duration(0)
	failures := t.failures
	if failures > 0 {
		wait = t.backoff
	} else if !t.lastPoll.IsZero() {
		if since := t.now().Sub(t.lastPoll); since < t.cfg.PollInterval {
			wait = t.cfg.PollInterval - since
		}
	}
	t.mu.Unlock()
	if wait <= 0 {
		return nil
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (t *DWSTransport) recordSuccess(events int) {
	t.mu.Lock()
	t.lastPoll = t.now()
	t.failures = 0
	t.backoff = 0
	t.health.State = "ok"
	t.health.Detail = fmt.Sprintf("dws poll ok: %d message(s)", events)
	status := t.status
	t.mu.Unlock()
	if status != nil && events > 0 {
		status(im.HealthStarting, "dws stream connected; waiting for messages")
	}
}

func (t *DWSTransport) recordFailure(err error) {
	t.mu.Lock()
	t.lastPoll = t.now()
	t.failures++
	if t.backoff == 0 {
		t.backoff = t.cfg.Backoff // 第一次失败后的等待 = 1s
	} else if next := t.backoff * 2; next > t.cfg.MaxBackoff {
		t.backoff = t.cfg.MaxBackoff
	} else {
		t.backoff = next
	}
	t.health.State = "error"
	t.health.Detail = truncateDetail(err.Error())
	var de *DWSError
	if errors.As(err, &de) && (de.Status == 401 || de.Status == 403 || de.AuthURL != "" || errors.Is(err, ErrDWSAuthRequired)) {
		t.health.State = "auth"
		t.health.AuthURL = de.AuthURL
		t.health.Detail = dwsLoginDetail(de.AuthURL)
	}
	status := t.status
	detail := t.health.Detail
	t.mu.Unlock()
	if status != nil {
		status(im.HealthError, detail)
	}
}

func (t *DWSTransport) setHealthState(state, detail, authURL string) {
	t.mu.Lock()
	t.health.State = state
	t.health.Detail = detail
	if authURL != "" {
		t.health.AuthURL = authURL
	}
	t.mu.Unlock()
}

// Health 返回最近一次已知的渠道健康（不触发 CLI，供面板/状态页读）。
func (t *DWSTransport) Health() DWSHealth {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.health
}

// dwsLoginDetail 是"未登录"时给人看的可操作文案。
func dwsLoginDetail(authURL string) string {
	base := "dws 未登录：运行 `dws auth login` 完成授权"
	if authURL != "" {
		return base + "，或打开 " + authURL
	}
	return base
}

// eventFromDWS 把已校验的 DWS 消息拍成驱动层 Event；mapping 负责 @/类型语义。
func eventFromDWS(msg DWSMessage) Event {
	content, _ := json.Marshal(map[string]string{"content": msg.Text})
	convType := "2"
	if msg.ConversationType == "direct" {
		convType = "1"
	}
	atUsers := make([]AtUser, 0, len(msg.Mentions))
	for _, mention := range msg.Mentions {
		atUsers = append(atUsers, AtUser{DingTalkID: mention})
	}
	return Event{
		MsgID:             msg.ID,
		MsgType:           "text",
		Content:           string(content),
		SenderStaffID:     msg.SenderID,
		SenderID:          msg.SenderID,
		SenderNick:        msg.SenderName,
		SenderIsBot:       msg.SenderBot,
		ConversationID:    msg.ConversationID,
		ConversationType:  convType,
		ConversationTitle: msg.ConversationName,
		CreateAt:          msg.At.UnixMilli(),
		IsInAtList:        len(msg.Mentions) > 0,
		IsReplyMsg:        msg.ReplyToBot,
		AtUsers:           atUsers,
	}
}
