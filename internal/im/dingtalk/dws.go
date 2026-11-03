package dingtalk

// DingTalk DWS 传输（DingTalk Workspace CLI，真实契约）。
//
// 事实来源：github.com/DingTalk-Real-AI/dingtalk-workspace-cli（Apache-2.0，Go CLI）。
// 它不是可 import 的客户端库：公开面只有 root 包的 EmbeddedSkills，命令实现都在
// internal/。所以适配方式是"外部命令 + NDJSON 长连接"，不是 JSON poll 契约：
//
//	接收：dws event +listen-im --kind at-me|group|all-direct|all-group \
//	        [--chat-query <q>] --profile <p> --flatten -f ndjson
//	      stdout 每行一个事件 JSON；stderr 等到 `[event] ready` 才算就绪。
//	      底层 fallback：dws event consume <EventKey> --flatten -f ndjson
//	发送：dws chat +messages-send --as user --group <cid> --text <t>
//	      （单聊用 --open-dingtalk-id <sender_open_dingtalk_id>；
//	      复杂/底层场景：chat message send / send-by-bot；没有 --idempotency-key，
//	      幂等靠文本尾部隐藏 marker。）
//	授权：dws auth status --format json；登录 dws auth login（--device 为 headless）。
//	      DWS 是**操作员个人 OAuth 身份**，不是 bot/app；app-key/secret 只属于
//	      stream（官方机器人 SDK）模式。
//
// 工程纪律：
//   - 每渠道一个长驻 consumer（不轮询、没有 cursor）；断线由本适配器按
//     1s→30s 退避重连，按 event id 去重；
//   - 短命令（auth/send）串行：execMu 包住每次 exec 的启动，两个命令永不同时
//     起子进程；长驻 consumer 进程按 CLI 设计常驻（发送时不需要打断它），
//     如果真实 CLI 在 profile 上有互斥，可用 PauseListenOnSend 让发送前停、
//     后重启；
//   - 永不经过 shell；30s 超时 + WaitDelay 杀子进程；stderr 脱敏后进 health。

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/zeokeel/super-agent/internal/domain"
	"github.com/zeokeel/super-agent/internal/im"
)

const (
	defaultDWSTimeout    = 30 * time.Second
	defaultDWSBackoff    = time.Second
	defaultDWSMaxBackoff = 30 * time.Second
	dwsWaitDelay         = 500 * time.Millisecond
	maxDWSEvents         = 256
	maxDWSBatch          = 50
	maxDWSSeenIDs        = 1024
	maxDWSDetail         = 300
	maxDWSStderrLines    = 20
	maxDWSLineBytes      = 1 << 20
)

// DWS 事件监听范围（`--kind`）。
const (
	ListenAtMe      = "at-me"
	ListenGroup     = "group"
	ListenAllDirect = "all-direct"
	ListenAllGroup  = "all-group"
)

var (
	// ErrDWSClosed 表示渠道已关闭；继续 Subscribe/Send 是编程错误。
	ErrDWSClosed = im.ErrClosed
	// ErrDWSAuthRequired 表示 dws 未登录（需要用户授权），可被 errors.Is。
	ErrDWSAuthRequired = errors.New("dingtalk: dws is not logged in")
	// ErrAuthRequired 是短名别名（runtime/outbox 可读的名字）。
	ErrAuthRequired = ErrDWSAuthRequired
	// ErrDWSUndefined 表示 CLI 不提供某命令（例如老版本没有 auth status）。
	ErrDWSUndefined = errors.New("dingtalk: dws does not support this command")
)

// DWSMarkerMode 是文本尾部隐藏去重标记的形态。
type DWSMarkerMode string

const (
	MarkerHTMLComment DWSMarkerMode = "html"
	MarkerZeroWidth   DWSMarkerMode = "zero-width"
)

// DWSConfig 是 DWS 渠道配置。identity 来自当前 profile 的 OAuth 登录态；
// AppKey/AppSecret 不在这里（它们只属于 stream 模式）。
type DWSConfig struct {
	// Command 是 dws 可执行文件/包装脚本；空用 "dws"。
	Command string
	// Profile 是 dws profile 名称；空用 CLI 当前 profile。
	Profile string
	// ListenKind 是 --kind：at-me（默认）| group | all-direct | all-group。
	ListenKind string
	// ChatQuery 是 --chat-query 的自然语言目标（可选；CLI 内唯一解析）。
	ChatQuery string
	// EventKey 非空时改用 `event consume <EventKey> --flatten -f ndjson`。
	EventKey string
	// SendAs 是发送身份：user（默认，个人 OAuth）或 bot。
	SendAs string
	// RobotCode 是 SendAs=bot 时的机器人 code。
	RobotCode string
	// Timeout 是短命令/单次启动的超时；<=0 用 30s。
	Timeout time.Duration
	// Backoff/MaxBackoff 是 consumer 断线重连退避（1s→30s）。
	Backoff, MaxBackoff time.Duration
	// PauseListenOnSend=true 时，发送前停 consumer、发送后重启（profile 互斥
	// 的真实 CLI 需要；默认 false，因为 NDJSON 监听期间通常仍可发送）。
	PauseListenOnSend bool
	// LoginHeadless 为 true 时 LoginCommand 给出 `dws auth login --device`。
	LoginHeadless bool
	// Secrets 是需要从 stderr/health 里脱敏的字符串（例如手动配置的令牌）。
	Secrets []string
	// MarkerMode 选择隐藏去重标记形态（默认 HTML 注释）。
	MarkerMode DWSMarkerMode
	// Client 允许注入 CLI 替身（测试/自定义包装脚本）；nil 用 exec 实现。
	Client DWSCLI
}

func (c DWSConfig) withDefaults() DWSConfig {
	c.Command = strings.TrimSpace(c.Command)
	if c.Command == "" {
		c.Command = "dws"
	}
	c.Profile = strings.TrimSpace(c.Profile)
	c.ChatQuery = strings.TrimSpace(c.ChatQuery)
	c.EventKey = strings.TrimSpace(c.EventKey)
	c.ListenKind = strings.ToLower(strings.TrimSpace(c.ListenKind))
	switch c.ListenKind {
	case "":
		c.ListenKind = ListenAtMe
	case ListenAtMe, ListenGroup, ListenAllDirect, ListenAllGroup:
	default:
		c.ListenKind = ListenAtMe
	}
	c.SendAs = strings.ToLower(strings.TrimSpace(c.SendAs))
	if c.SendAs == "" {
		c.SendAs = "user"
	}
	if c.Timeout <= 0 {
		c.Timeout = defaultDWSTimeout
	}
	if c.Backoff <= 0 {
		c.Backoff = defaultDWSBackoff
	}
	if c.MaxBackoff <= 0 {
		c.MaxBackoff = defaultDWSMaxBackoff
	}
	if c.MaxBackoff < c.Backoff {
		c.MaxBackoff = c.Backoff
	}
	if c.MarkerMode == "" {
		c.MarkerMode = MarkerHTMLComment
	}
	return c
}

// AuthStatus 是 `dws auth status --format json` 的归一结果。
type AuthStatus struct {
	LoggedIn bool   `json:"loggedIn"`
	Account  string `json:"account"`
	Profile  string `json:"profile"`
	AuthURL  string `json:"authUrl"`
	// LoginCommand 是给人/UI 的可执行登录命令（不是 CLI 原字段）。
	LoginCommand string `json:"-"`
}

// DWSHealth 是旧的渠道健康形状（runtime 兼容）；新代码用 Status。
type DWSHealth struct {
	State     string
	Detail    string
	AuthURL   string
	LoggedIn  bool
	Account   string
	Profile   string
	LoginCmd  string
	LastError string
	Failures  int
}

// DWSMessage 是一条已解析的 DWS IM 事件（驱动层形状）。
type DWSMessage struct {
	ID               string
	ConversationID   string
	ConversationType string // "group" | "direct"
	ConversationName string
	SenderID         string
	SenderName       string
	SenderBot        bool
	Text             string
	At               time.Time
	Mentions         []string
	ReplyToBot       bool
	AddressedToBot   bool
}

// PollPage 是旧 DWSClient 兼容形状（新路径没有 cursor）。
type PollPage struct {
	Cursor   string
	Messages []DWSMessage
	Skipped  int
}

// DWSError 是一次 DWS 调用失败。
type DWSError struct {
	Op         string
	Status     int
	Code       int
	Permanent  bool
	RetryAfter time.Duration
	Detail     string
	AuthURL    string
	Err        error
}

func (e *DWSError) Error() string {
	var b strings.Builder
	b.WriteString("dingtalk: dws ")
	if e.Op != "" {
		b.WriteString(e.Op)
		b.WriteString(": ")
	}
	if e.Status != 0 {
		fmt.Fprintf(&b, "HTTP %d: ", e.Status)
	} else if e.Code != 0 {
		fmt.Fprintf(&b, "code %d: ", e.Code)
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

func (e *DWSError) Unwrap() error { return e.Err }

// Temporary 实现 im.IsTransient：4xx/参数/命令缺失是永久；其余（5xx/限流/
// 超时/取消）交给 Outbox 的跨重启退避（取消也不丢回执）。
func (e *DWSError) Temporary() bool { return !e.Permanent }

// IsPermanent 报告错误是否永久（4xx/命令缺失/参数非法）。
func IsPermanent(err error) bool {
	var de *DWSError
	if errors.As(err, &de) {
		return de.Permanent
	}
	return false
}

// IsAuthRequired 报告错误是否意味着"dws 未登录"。
func IsAuthRequired(err error) bool {
	if errors.Is(err, ErrAuthRequired) {
		return true
	}
	var de *DWSError
	if errors.As(err, &de) {
		return de.Status == 401 || de.Status == 403 || de.AuthURL != ""
	}
	return false
}

// IsRetryable 给 outbox 用：永久失败/未登录不重试，其余可重试。
func IsRetryable(err error) bool {
	if err == nil || IsPermanent(err) || IsAuthRequired(err) {
		return false
	}
	return true
}

// RetryAfter 返回错误里携带的建议退避。
func RetryAfter(err error) time.Duration {
	var de *DWSError
	if errors.As(err, &de) {
		return de.RetryAfter
	}
	return 0
}

// --- 隐藏 marker ---

// MarkerFor 返回指定形态的标记。
func MarkerFor(key string, mode DWSMarkerMode) string {
	if mode == MarkerZeroWidth {
		return markerFor(key)
	}
	return markerForDWS(key, MarkerHTMLComment)
}

// HasMarker 报告文本尾部是否带本包生成的标记。
func HasMarker(text string, mode DWSMarkerMode) bool {
	if mode == MarkerZeroWidth {
		return strings.ContainsRune(text, markerStart)
	}
	return strings.Contains(text, "<!-- zeokeel:")
}

// StripMarker 去掉尾部标记，返回人类可读正文。
func StripMarker(text string, mode DWSMarkerMode) string {
	if mode == MarkerZeroWidth {
		runes := []rune(text)
		for i := len(runes) - 1; i >= 0; i-- {
			if runes[i] == markerStart {
				return string(runes[:i])
			}
		}
		return text
	}
	if idx := strings.LastIndex(text, "<!-- zeokeel:"); idx >= 0 && strings.HasSuffix(text, "-->") {
		return strings.TrimRight(text[:idx], "\n")
	}
	return text
}

// MarkerPossiblyVisible 报告该形态是否可能在纯文本渲染里可见。
func MarkerPossiblyVisible(mode DWSMarkerMode) bool { return mode != MarkerZeroWidth }

func markerForDWS(key string, mode DWSMarkerMode) string {
	if mode == MarkerZeroWidth {
		return markerFor(key)
	}
	safe := strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\r' || r == '\t':
			return ' '
		case r == '\u2028' || r == '\u2029':
			return ' '
		case isControlOrFormat(r):
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
		var b strings.Builder
		for _, r := range []byte(key) {
			fmt.Fprintf(&b, "%02x", r)
		}
		safe = b.String()
	}
	return "\n\n<!-- zeokeel:" + safe + " -->"
}

func isControlOrFormat(r rune) bool {
	switch {
	case r < 0x20, r == 0x7f:
		return true
	case r >= 0x80 && r <= 0x9f:
		return true
	case r == 0x200b || r == 0x200c || r == 0x200d || r == 0x2060:
		return true
	}
	return false
}

// --- CLI 抽象 ---

// DWSStream 是一个已启动的长驻子进程。
type DWSStream interface {
	Stdout() io.Reader
	Stderr() io.Reader
	// Wait 等待进程退出（reader 结束前可能已返回）；Kill 立即杀进程。
	Wait() error
	Kill() error
}

// DWSCLI 是外部 dws 进程的最小接口；测试注入 fake，生产用 exec 实现。
//
// Run 用于短命令（auth/send）；StartStream 用于长驻事件 consumer。
// 两者都由 DWSChannel 串行调度（短命令互斥；consumer 常驻）。
type DWSCLI interface {
	Run(ctx context.Context, args ...string) (stdout []byte, stderr []byte, err error)
	StartStream(ctx context.Context, args ...string) (DWSStream, error)
}

// NewExecDWSCLI 构造真实的 exec 实现。
func NewExecDWSCLI(cfg DWSConfig) DWSCLI { return &execDWSCLI{cfg: cfg.withDefaults()} }

type execDWSCLI struct{ cfg DWSConfig }

func (c *execDWSCLI) Run(ctx context.Context, args ...string) ([]byte, []byte, error) {
	if c.cfg.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.cfg.Timeout)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, c.cfg.Command, args...)
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return cmd.Process.Kill()
	}
	cmd.WaitDelay = dwsWaitDelay
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if err != nil && ctx.Err() != nil {
		err = ctx.Err()
	}
	return stdout.Bytes(), stderr.Bytes(), err
}

type execStream struct {
	cmd    *exec.Cmd
	stdout io.ReadCloser
	stderr io.ReadCloser
}

func (s *execStream) Stdout() io.Reader { return s.stdout }
func (s *execStream) Stderr() io.Reader { return s.stderr }
func (s *execStream) Wait() error       { return s.cmd.Wait() }
func (s *execStream) Kill() error {
	if s.cmd.Process == nil {
		return nil
	}
	return s.cmd.Process.Kill()
}

func (c *execDWSCLI) StartStream(ctx context.Context, args ...string) (DWSStream, error) {
	cmd := exec.CommandContext(ctx, c.cfg.Command, args...)
	cmd.WaitDelay = dwsWaitDelay
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &execStream{cmd: cmd, stdout: stdout, stderr: stderr}, nil
}

// --- 兼容的旧 DWSClient（runtime 尚未切到 DWSChannel 时的 seam） ---

// DWSClient 是旧 poll 形状的兼容接口（runtime 占位/测试 fake 使用）。
// Deprecated: 新代码用 DWSChannel（长驻 NDJSON consumer，无 cursor）。
type DWSClient interface {
	AuthStatus(ctx context.Context) (AuthStatus, error)
	Poll(ctx context.Context, cursor string, limit int) (PollPage, error)
	Send(ctx context.Context, conversationID, text, idempotencyKey string) (string, error)
	Health(ctx context.Context) (DWSHealth, error)
}

// NewCommandDWSClient 返回一个懒启动 DWSChannel 的兼容客户端。
// Deprecated: runtime 应直接用 NewDWSChannel/NewDWS。
func NewCommandDWSClient(cfg DWSConfig) DWSClient { return &commandDWSClient{cfg: cfg} }

type commandDWSClient struct {
	cfg  DWSConfig
	once sync.Once
	ch   *DWSChannel
	err  error
}

func (c *commandDWSClient) ensure() (*DWSChannel, error) {
	c.once.Do(func() { c.ch, c.err = NewDWS(c.cfg) })
	return c.ch, c.err
}

func (c *commandDWSClient) AuthStatus(ctx context.Context) (AuthStatus, error) {
	ch, err := c.ensure()
	if err != nil {
		return AuthStatus{}, err
	}
	return ch.AuthStatus(ctx)
}

func (c *commandDWSClient) Poll(ctx context.Context, _ string, limit int) (PollPage, error) {
	ch, err := c.ensure()
	if err != nil {
		return PollPage{}, err
	}
	msgs, err := ch.NextBatch(ctx, limit)
	if err != nil {
		return PollPage{}, err
	}
	return PollPage{Messages: msgs}, nil
}

func (c *commandDWSClient) Send(ctx context.Context, conversationID, text, idempotencyKey string) (string, error) {
	ch, err := c.ensure()
	if err != nil {
		return "", err
	}
	kind := domain.ConvGroup
	if strings.HasPrefix(conversationID, "dm:") {
		kind = domain.ConvDirect
	}
	receipt, err := ch.Send(ctx, im.Outbound{Key: idempotencyKey, Conv: domain.Conversation{Kind: kind, ID: conversationID}, Text: text})
	return receipt.MessageID, err
}

func (c *commandDWSClient) Health(ctx context.Context) (DWSHealth, error) {
	ch, err := c.ensure()
	if err != nil {
		return DWSHealth{State: "error", Detail: err.Error()}, err
	}
	return ch.DWSHealth(ctx)
}

// --- 渠道（长驻 consumer + 串行命令 + Provider/Sender） ---

type dwsConn struct {
	proc   DWSStream
	cancel context.CancelFunc
	events chan DWSMessage
	exit   chan error
	ready  chan struct{}
	once   sync.Once
}

// DWSChannel 是 DWS 渠道的生产句柄：一个长驻 consumer + 串行短命令。
// 实现 im.Provider / im.Receiver / im.Sender / im.Closer。
type DWSChannel struct {
	cfg DWSConfig
	cli DWSCLI
	now func() time.Time

	cmdMu     sync.Mutex // 短命令 + consumer 启动互斥（同一时刻只起一个子进程）
	dialMu    sync.Mutex
	closeOnce sync.Once
	done      chan struct{} // Close 时关闭，唤醒阻塞的 NextBatch/Subscribe

	mu       sync.Mutex
	conn     *dwsConn
	closed   bool
	seen     map[string]struct{}
	seenList []string
	drops    int
	dups     int
	failures int
	backoff  time.Duration
	lastErr  error
	state    string
	detail   string
	auth     AuthStatus
	stderr   []string
	status   func(im.HealthState, string)
}

var (
	_ im.Provider = (*DWSChannel)(nil)
	_ im.Receiver = (*DWSChannel)(nil)
	_ im.Sender   = (*DWSChannel)(nil)
	_ im.Closer   = (*DWSChannel)(nil)
)

// NewDWS 用真实 exec CLI 构造渠道。
func NewDWS(cfg DWSConfig) (*DWSChannel, error) {
	return NewDWSChannel(cfg, nil)
}

// NewDWSChannel 构造 DWS 渠道；cli 可为 nil（用 exec 实现），测试注入 fake CLI。
// runtime 每渠道只应构造一个：它同时负责收件、发件与授权检查的串行化。
func NewDWSChannel(cfg DWSConfig, cli DWSCLI) (*DWSChannel, error) {
	cfg = cfg.withDefaults()
	if cli == nil {
		cli = NewExecDWSCLI(cfg)
	}
	return &DWSChannel{
		cfg:     cfg,
		cli:     cli,
		now:     time.Now,
		done:    make(chan struct{}),
		seen:    map[string]struct{}{},
		state:   "starting",
		detail:  "not connected yet",
		backoff: cfg.Backoff,
	}, nil
}

// Name 实现 im.Provider。
func (*DWSChannel) Name() string { return "dingtalk" }

// Capabilities 实现 im.Provider：DWS 是个人身份，支持文本/Markdown 发送与
// @ 接收；没有话题、文件、历史读取。
func (*DWSChannel) Capabilities() im.Capabilities {
	return im.Capabilities{Markdown: true, Mentions: true, MaxTextLen: 8000}
}

// Self 实现 im.Provider：DWS 个人身份来自 profile 的登录账号。
func (c *DWSChannel) Self(ctx context.Context) (im.Identity, error) {
	st, err := c.AuthStatus(ctx)
	if err != nil {
		return im.Identity{}, err
	}
	if !st.LoggedIn {
		return im.Identity{}, fmt.Errorf("dingtalk: %w (login: %s)", ErrDWSAuthRequired, st.LoginCommand)
	}
	id := st.Account
	if id == "" {
		id = st.Profile
	}
	return im.Identity{ID: id, Name: st.Account}, nil
}

// Members 实现 im.Provider：DWS 契约没有成员查询。
func (*DWSChannel) Members(context.Context, string) ([]im.Member, error) {
	return nil, fmt.Errorf("%w: dws has no member listing", im.ErrUnsupported)
}

// ReadThread 实现 im.Sender：DWS 事件流没有历史消息 API。
func (*DWSChannel) ReadThread(context.Context, domain.Conversation, string) (im.Page, error) {
	return im.Page{Done: true}, fmt.Errorf("%w: dws has no message history API", im.ErrUnsupported)
}

// SetStatus 注入健康回调（runtime 把渠道状态投到面板）。
func (c *DWSChannel) SetStatus(fn func(im.HealthState, string)) {
	c.mu.Lock()
	c.status = fn
	c.mu.Unlock()
}

// Status 返回当前渠道状态（starting|ready|auth|error|stopped）与 detail。
func (c *DWSChannel) Status() (state, detail string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state, c.detail
}

// Dropped/Duplicates/Failures/LastError 供运维与监督。
func (c *DWSChannel) Dropped() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.drops
}

func (c *DWSChannel) Duplicates() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.dups
}

func (c *DWSChannel) Failures() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.failures
}

func (c *DWSChannel) LastError() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastErr
}

// Reset 清空渠道重启/切换传输时必须丢弃的内存状态（去重集合、失败/退避、
// 最近错误），并停止旧 consumer。runtime 切换 transport 时还必须清 KV 游标
// （DWS 本身没有 cursor；这是为了不让旧的飞书/stream 游标残留）。
func (c *DWSChannel) Reset() {
	c.stopConsumer()
	c.mu.Lock()
	c.seen = map[string]struct{}{}
	c.seenList = nil
	c.drops, c.dups = 0, 0
	c.failures, c.backoff = 0, c.cfg.Backoff
	c.lastErr = nil
	c.state, c.detail = "starting", "reset"
	c.stderr = nil
	c.mu.Unlock()
}

// Close 实现 im.Closer：停 consumer、杀子进程；幂等。
func (c *DWSChannel) Close() error {
	var err error
	c.closeOnce.Do(func() {
		c.mu.Lock()
		c.closed = true
		c.state, c.detail = "stopped", "channel closed"
		status := c.status
		c.mu.Unlock()
		close(c.done)
		c.stopConsumer()
		if status != nil {
			status(im.HealthPaused, "channel stopped")
		}
	})
	return err
}

// Subscribe 实现 im.Receiver：消费 NDJSON 事件流，重连/去重由本渠道负责。
func (c *DWSChannel) Subscribe(ctx context.Context, out chan<- im.RawEvent) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		msgs, err := c.NextBatch(ctx, maxDWSBatch)
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			if errors.Is(err, im.ErrClosed) {
				return err
			}
			// NextBatch/ensureConsumer 已记录退避；这里继续（下一轮会等待）。
			continue
		}
		for _, msg := range msgs {
			ev := rawEventFromDWS(msg)
			select {
			case out <- ev:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
}

// NextBatch 返回下一批（至少一条）已去重事件；失败时按退避重试在下次调用。
func (c *DWSChannel) NextBatch(ctx context.Context, limit int) ([]DWSMessage, error) {
	if limit <= 0 || limit > maxDWSBatch {
		limit = maxDWSBatch
	}
	conn, err := c.ensureConsumer(ctx)
	if err != nil {
		return nil, err
	}
	select {
	case msg := <-conn.events:
		batch := make([]DWSMessage, 0, limit)
		batch = append(batch, msg)
		for len(batch) < limit {
			select {
			case next := <-conn.events:
				batch = append(batch, next)
			default:
				c.recordSuccess(len(batch))
				return batch, nil
			}
		}
		c.recordSuccess(len(batch))
		return batch, nil
	case err := <-conn.exit:
		c.recordFailure(err)
		return nil, err
	case <-ctx.Done():
		c.stopConsumer()
		return nil, ctx.Err()
	case <-c.done:
		return nil, im.ErrClosed
	case <-conn.ready:
		// ready 只是状态信号；继续等事件（避免把 ready 当数据）。
		select {
		case msg := <-conn.events:
			c.recordSuccess(1)
			return []DWSMessage{msg}, nil
		case err := <-conn.exit:
			c.recordFailure(err)
			return nil, err
		case <-ctx.Done():
			c.stopConsumer()
			return nil, ctx.Err()
		case <-c.done:
			return nil, im.ErrClosed
		}
	}
}

func (c *DWSChannel) ensureConsumer(ctx context.Context) (*dwsConn, error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, im.ErrClosed
	}
	conn := c.conn
	failures := c.failures
	wait := time.Duration(0)
	if failures > 0 {
		wait = c.backoff
	}
	c.mu.Unlock()
	if conn != nil {
		return conn, nil
	}
	if wait > 0 {
		timer := time.NewTimer(wait)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		}
		timer.Stop()
	}
	c.dialMu.Lock()
	defer c.dialMu.Unlock()
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, im.ErrClosed
	}
	if c.conn != nil {
		conn := c.conn
		c.mu.Unlock()
		return conn, nil
	}
	c.mu.Unlock()

	lifetime, cancel := context.WithCancel(context.Background())
	conn = &dwsConn{
		cancel: cancel,
		events: make(chan DWSMessage, maxDWSEvents),
		exit:   make(chan error, 1),
		ready:  make(chan struct{}),
	}
	c.cmdMu.Lock()
	proc, err := c.cli.StartStream(lifetime, c.listenArgs()...)
	c.cmdMu.Unlock()
	if err != nil {
		cancel()
		c.recordFailure(c.classifyCmdError("event listen", err, nil, nil))
		return nil, c.LastError()
	}
	conn.proc = proc
	c.mu.Lock()
	c.conn = conn
	c.state, c.detail = "starting", "consumer started; waiting for ready"
	status := c.status
	c.mu.Unlock()
	if status != nil {
		status(im.HealthStarting, "dws consumer started; waiting for ready")
	}
	go c.readLoop(lifetime, conn)
	go c.stderrLoop(lifetime, conn)
	go c.waitLoop(lifetime, conn)
	return conn, nil
}

func (c *DWSChannel) listenArgs() []string {
	args := []string{"event"}
	if c.cfg.EventKey != "" {
		args = append(args, "consume", c.cfg.EventKey, "--flatten", "-f", "ndjson")
	} else {
		args = append(args, "+listen-im", "--kind", c.cfg.ListenKind)
		if c.cfg.ChatQuery != "" {
			args = append(args, "--chat-query", c.cfg.ChatQuery)
		}
		args = append(args, "--flatten", "-f", "ndjson")
	}
	return appendProfile(args, c.cfg.Profile)
}

func appendProfile(args []string, profile string) []string {
	if strings.TrimSpace(profile) == "" {
		return args
	}
	return append(args, "--profile", strings.TrimSpace(profile))
}

func (c *DWSChannel) readLoop(lifetime context.Context, conn *dwsConn) {
	reader := bufio.NewReaderSize(conn.proc.Stdout(), 64*1024)
	for {
		line, err := readLimitedLine(reader, maxDWSLineBytes)
		if len(bytes.TrimSpace(line)) > 0 {
			msg, ok := parseDWSLine(line, c.cfg.ListenKind)
			if !ok {
				c.mu.Lock()
				c.drops++
				c.mu.Unlock()
			} else if !c.rememberSeen(msg.ID) {
				c.mu.Lock()
				c.dups++
				c.mu.Unlock()
			} else {
				c.markReadyOnce(conn, "event received")
				select {
				case conn.events <- msg:
				case <-lifetime.Done():
					return
				}
			}
		}
		if err != nil {
			return
		}
	}
}

func readLimitedLine(reader *bufio.Reader, max int) ([]byte, error) {
	var buf []byte
	for {
		chunk, err := reader.ReadSlice('\n')
		if len(buf)+len(chunk) > max {
			// 超长行：丢弃该行剩余部分，计一次 skip（调用方按空行跳过）。
			for err == bufio.ErrBufferFull {
				_, err = reader.ReadSlice('\n')
			}
			return nil, nil
		}
		buf = append(buf, chunk...)
		if err == nil {
			return buf, nil
		}
		if err == bufio.ErrBufferFull {
			continue
		}
		return buf, err
	}
}

func (c *DWSChannel) stderrLoop(lifetime context.Context, conn *dwsConn) {
	reader := bufio.NewScanner(conn.proc.Stderr())
	reader.Buffer(make([]byte, 0, 64*1024), maxDWSLineBytes)
	for reader.Scan() {
		line := c.redact(strings.TrimSpace(reader.Text()))
		if line == "" {
			continue
		}
		c.mu.Lock()
		c.stderr = append(c.stderr, line)
		if len(c.stderr) > maxDWSStderrLines {
			c.stderr = c.stderr[len(c.stderr)-maxDWSStderrLines:]
		}
		c.mu.Unlock()
		if strings.Contains(strings.ToLower(line), "[event] ready") {
			c.markReadyOnce(conn, line)
		}
	}
}

func (c *DWSChannel) waitLoop(lifetime context.Context, conn *dwsConn) {
	err := conn.proc.Wait()
	if lifetime.Err() != nil {
		return
	}
	exitErr := fmt.Errorf("dingtalk: dws consumer exited: %w", err)
	if err == nil {
		exitErr = errors.New("dingtalk: dws consumer exited unexpectedly")
	}
	select {
	case conn.exit <- exitErr:
	default:
	}
	c.mu.Lock()
	if c.conn == conn {
		c.conn = nil
	}
	c.mu.Unlock()
}

func (c *DWSChannel) markReadyOnce(conn *dwsConn, detail string) {
	conn.once.Do(func() { close(conn.ready) })
	c.mu.Lock()
	if c.conn == conn {
		c.state, c.detail = "ready", detail
	}
	status := c.status
	c.mu.Unlock()
	if status != nil {
		status(im.HealthReady, "dws event stream ready")
	}
}

func (c *DWSChannel) recordSuccess(n int) {
	c.mu.Lock()
	c.failures = 0
	c.backoff = c.cfg.Backoff
	c.lastErr = nil
	if n > 0 {
		c.state = "ready"
		c.detail = fmt.Sprintf("received %d event(s)", n)
	}
	status := c.status
	state := c.state
	detail := c.detail
	c.mu.Unlock()
	if status != nil {
		status(im.HealthReady, detail)
	}
	_ = state
}

func (c *DWSChannel) recordFailure(err error) {
	if err == nil {
		err = errors.New("unknown dws failure")
	}
	c.mu.Lock()
	c.lastErr = err
	c.failures++
	if c.failures == 1 {
		c.backoff = c.cfg.Backoff
	} else if next := c.backoff * 2; next > c.cfg.MaxBackoff {
		c.backoff = c.cfg.MaxBackoff
	} else {
		c.backoff = next
	}
	if IsAuthRequired(err) {
		c.state = "auth"
		c.detail = c.authDetailLocked()
	} else {
		c.state = "error"
		c.detail = truncateDetail(c.redact(err.Error()))
	}
	status := c.status
	detail := c.detail
	c.mu.Unlock()
	if status != nil {
		status(im.HealthError, detail)
	}
}

func (c *DWSChannel) authDetailLocked() string {
	cmd := c.loginCommand()
	if c.auth.AuthURL != "" {
		return "dws 未登录：运行 `" + cmd + "` 授权，或打开 " + c.auth.AuthURL
	}
	return "dws 未登录：运行 `" + cmd + "` 完成授权"
}

func (c *DWSChannel) loginCommand() string {
	cmd := strings.TrimSpace(c.cfg.Command)
	if cmd == "" {
		cmd = "dws"
	}
	if c.cfg.LoginHeadless {
		return cmd + " auth login --device"
	}
	return cmd + " auth login"
}

func (c *DWSChannel) stopConsumer() {
	c.mu.Lock()
	conn := c.conn
	c.conn = nil
	c.mu.Unlock()
	if conn == nil {
		return
	}
	if conn.cancel != nil {
		conn.cancel()
	}
	if conn.proc != nil {
		_ = conn.proc.Kill()
	}
}

func (c *DWSChannel) rememberSeen(id string) bool {
	if id == "" {
		return true
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.seen[id]; ok {
		return false
	}
	c.seen[id] = struct{}{}
	c.seenList = append(c.seenList, id)
	if len(c.seenList) > maxDWSSeenIDs {
		delete(c.seen, c.seenList[0])
		c.seenList = c.seenList[1:]
	}
	return true
}

// AuthStatus 实现授权检查：`dws auth status --format json`。
func (c *DWSChannel) AuthStatus(ctx context.Context) (AuthStatus, error) {
	if err := c.ensureOpen(); err != nil {
		return AuthStatus{}, err
	}
	stdout, stderr, err := c.runCommand(ctx, appendProfile([]string{"auth", "status", "--format", "json"}, c.cfg.Profile)...)
	if err != nil {
		cerr := c.classifyCmdError("auth status", err, stdout, stderr)
		var de *DWSError
		if errors.As(cerr, &de) && (de.Status == 401 || de.Status == 403) {
			de.Err = ErrDWSAuthRequired
			de.AuthURL = extractURL(string(stderr))
		}
		st := AuthStatus{LoggedIn: false, AuthURL: extractURL(string(stderr)), LoginCommand: c.loginCommand()}
		c.mu.Lock()
		c.auth = st
		c.state, c.detail = "auth", c.authDetailLocked()
		status := c.status
		detail := c.detail
		c.mu.Unlock()
		if status != nil {
			status(im.HealthError, detail)
		}
		return st, cerr
	}
	st, perr := parseAuthStatus(stdout)
	if perr != nil {
		return AuthStatus{}, perr
	}
	st.LoginCommand = c.loginCommand()
	c.mu.Lock()
	c.auth = st
	if st.LoggedIn {
		c.state = "ready"
		c.detail = "dws logged in as " + orDefaultText(st.Account, st.Profile)
	} else {
		c.state = "auth"
		c.detail = c.authDetailLocked()
	}
	status := c.status
	detail := c.detail
	c.mu.Unlock()
	if status != nil {
		if st.LoggedIn {
			status(im.HealthReady, detail)
		} else {
			status(im.HealthError, detail)
		}
	}
	return st, nil
}

func parseAuthStatus(stdout []byte) (AuthStatus, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(stdout, &raw); err != nil {
		return AuthStatus{}, &DWSError{Op: "auth status", Detail: "unreadable JSON: " + truncateDetail(string(stdout)), Err: err}
	}
	get := func(keys ...string) string {
		for _, key := range keys {
			if v, ok := raw[key]; ok {
				var s string
				if json.Unmarshal(v, &s) == nil && s != "" {
					return s
				}
			}
		}
		return ""
	}
	loggedIn := false
	for _, key := range []string{"loggedIn", "logged_in", "login", "ok"} {
		if v, ok := raw[key]; ok {
			var b bool
			if json.Unmarshal(v, &b) == nil {
				loggedIn = b
				break
			}
		}
	}
	return AuthStatus{
		LoggedIn: loggedIn,
		Account:  orDefaultText(get("account", "user", "name"), get("accountName")),
		Profile:  get("profile", "profileName", "profile_name"),
		AuthURL:  get("authUrl", "auth_url", "loginUrl", "login_url", "verificationUri"),
	}, nil
}

// DWSHealth 兼容旧 runtime/fake 的健康形状；新代码用 Status。
func (c *DWSChannel) DWSHealth(ctx context.Context) (DWSHealth, error) {
	state, detail := c.Status()
	h := DWSHealth{State: state, Detail: detail, Failures: c.Failures()}
	c.mu.Lock()
	h.AuthURL = c.auth.AuthURL
	h.LoggedIn = c.auth.LoggedIn
	h.Account = c.auth.Account
	h.Profile = c.auth.Profile
	h.LoginCmd = c.loginCommand()
	if err := c.lastErr; err != nil {
		h.LastError = err.Error()
	}
	c.mu.Unlock()
	return h, nil
}

// Send 实现 im.Sender：`dws chat +messages-send --as user ...`。
func (c *DWSChannel) Send(ctx context.Context, msg im.Outbound) (im.Receipt, error) {
	if err := c.ensureOpen(); err != nil {
		return im.Receipt{}, err
	}
	key := strings.TrimSpace(msg.Key)
	if key == "" {
		key = im.DeriveKey("dingtalk", msg.Conv.ID, msg.Text)
	}
	if strings.TrimSpace(msg.Conv.ID) == "" {
		return im.Receipt{}, &DWSError{Op: "send", Permanent: true, Detail: "conversation id is empty"}
	}
	if c.cfg.PauseListenOnSend {
		c.stopConsumer()
		defer func() { _, _ = c.ensureConsumer(context.Background()) }()
	}
	args := c.sendArgs(msg, key)
	stdout, stderr, err := c.runCommand(ctx, args...)
	if err != nil {
		return im.Receipt{}, c.classifyCmdError("send", err, stdout, stderr)
	}
	return im.Receipt{MessageID: parseSendID(stdout)}, nil
}

func (c *DWSChannel) sendArgs(msg im.Outbound, key string) []string {
	args := []string{"chat", "+messages-send", "--as", c.cfg.SendAs}
	switch c.cfg.SendAs {
	case "bot":
		args = append(args, "--robot-code", c.cfg.RobotCode, "--groups", stripDirectPrefix(msg.Conv.ID))
	default:
		if msg.Conv.Kind == domain.ConvDirect || strings.HasPrefix(msg.Conv.ID, "dm:") {
			args = append(args, "--open-dingtalk-id", stripDirectPrefix(msg.Conv.ID))
		} else {
			args = append(args, "--group", msg.Conv.ID)
		}
	}
	content := msg.Text + MarkerFor(key, c.cfg.MarkerMode)
	if msg.Markdown {
		args = append(args, "--markdown", content)
	} else {
		args = append(args, "--text", content)
	}
	args = append(args, "--format", "json")
	return appendProfile(args, c.cfg.Profile)
}

func stripDirectPrefix(id string) string { return strings.TrimPrefix(id, "dm:") }

func parseSendID(stdout []byte) string {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(stdout, &raw); err != nil {
		return ""
	}
	get := func(keys ...string) string {
		for _, key := range keys {
			if v, ok := raw[key]; ok {
				var s string
				if json.Unmarshal(v, &s) == nil && s != "" {
					return s
				}
			}
		}
		return ""
	}
	for _, key := range []string{"messageId", "message_id", "openTaskId", "open_task_id", "taskId"} {
		if s := get(key); s != "" {
			return s
		}
	}
	if data, ok := raw["data"]; ok {
		var nested map[string]json.RawMessage
		if json.Unmarshal(data, &nested) == nil {
			for _, key := range []string{"messageId", "message_id", "openTaskId", "open_task_id"} {
				if v, ok := nested[key]; ok {
					var s string
					if json.Unmarshal(v, &s) == nil && s != "" {
						return s
					}
				}
			}
		}
	}
	return ""
}

func (c *DWSChannel) runCommand(ctx context.Context, args ...string) ([]byte, []byte, error) {
	c.cmdMu.Lock()
	defer c.cmdMu.Unlock()
	return c.cli.Run(ctx, args...)
}

func (c *DWSChannel) classifyCmdError(op string, err error, stdout, stderr []byte) error {
	rawDetail := truncateDetail(string(stderr))
	if rawDetail == "" {
		rawDetail = truncateDetail(string(stdout))
	}
	de := &DWSError{Op: op, Err: err}
	if errors.Is(err, context.DeadlineExceeded) {
		de.Detail = c.redact(rawDetail)
		return de
	}
	if errors.Is(err, context.Canceled) {
		de.Detail = c.redact(rawDetail)
		return de
	}
	var execErr *exec.Error
	var pathErr *fs.PathError
	switch {
	case errors.As(err, &execErr):
		de.Permanent = true
		de.Detail = "dws 未安装或不可执行（not executable）: " + c.redact(execErr.Error())
		return de
	case errors.As(err, &pathErr) && (errors.Is(pathErr, fs.ErrNotExist) || errors.Is(err, fs.ErrNotExist)):
		de.Permanent = true
		de.Detail = "dws 未安装或不可执行（not executable）: " + c.redact(pathErr.Error())
		return de
	case errors.Is(err, exec.ErrNotFound) || errors.Is(err, fs.ErrNotExist):
		de.Permanent = true
		de.Detail = "dws 未安装或不可执行（not executable）: " + c.redact(err.Error())
		return de
	}
	status, code := detectDWSError(stderr, stdout)
	de.Status, de.Code = status, code
	de.AuthURL = extractURL(rawDetail)
	de.RetryAfter = detectDWSRetryAfter(stderr, stdout)
	switch {
	case status == 401 || status == 403:
		de.Permanent = true
		de.Err = ErrDWSAuthRequired
	case status == 429:
		de.Permanent = false
	case status >= 400 && status < 500:
		de.Permanent = true
	case status >= 500:
		de.Permanent = false
	case code != 0:
		if class := classifyDWSCode(code); class == 401 || class == 403 {
			de.Permanent = true
			de.Err = ErrDWSAuthRequired
		} else if class >= 400 && class < 500 {
			de.Permanent = true
		}
	}
	if de.Status == 0 && de.Code != 0 {
		de.Status = classifyDWSCode(de.Code)
	}
	de.Detail = c.redact(rawDetail)
	return de
}

func (c *DWSChannel) ensureOpen() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return im.ErrClosed
	}
	return nil
}

func (c *DWSChannel) redact(s string) string { return redactDetail(s, c.cfg.Secrets) }

// --- 事件解析 ---

// parseDWSLine 解析一行 NDJSON（真实 CLI 字段见 event-im-output.md）：
// type/event_id/message_id/conversation_id/sender/sender_open_dingtalk_id/
// content/create_time/event_time/timestamp/quoted_message。
func parseDWSLine(line []byte, listenKind string) (DWSMessage, bool) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(line, &raw); err != nil {
		return DWSMessage{}, false
	}
	str := func(keys ...string) string {
		for _, key := range keys {
			if v, ok := raw[key]; ok {
				var s string
				if json.Unmarshal(v, &s) == nil {
					s = strings.TrimSpace(s)
					if s != "" {
						return s
					}
				}
			}
		}
		return ""
	}
	id := str("event_id", "eventId", "message_id", "messageId", "msg_id", "id")
	convID := str("conversation_id", "conversationId", "chat_id", "chatId")
	if id == "" || convID == "" {
		return DWSMessage{}, false
	}
	if len(id) > 512 || len(convID) > 512 {
		return DWSMessage{}, false
	}
	eventType := str("type", "event_type")
	convType := strings.ToLower(str("conversation_type", "conversationType", "chat_type", "chatType"))
	switch convType {
	case "group", "2":
		convType = "group"
	case "direct", "1", "single", "p2p":
		convType = "direct"
	default:
		lowerType := strings.ToLower(eventType)
		switch {
		case strings.Contains(lowerType, "o2o"), strings.Contains(lowerType, "direct"), strings.Contains(lowerType, "single"):
			convType = "direct"
		default:
			convType = "group"
		}
	}
	// 单聊的发送目标是稳定的 openDingTalkId；用 dm: 前缀把它编码进会话 id，
	// 发送时再转成 `--open-dingtalk-id`（DWS 的 chat handoff 契约）。
	senderOpenID := str("sender_open_dingtalk_id", "senderOpenDingtalkId", "sender_id", "senderId")
	conv := convID
	if convType == "direct" && senderOpenID != "" {
		conv = "dm:" + senderOpenID
	}
	at, ok := firstTime(raw, "create_time", "event_time", "timestamp", "createTime", "eventTime")
	if !ok {
		return DWSMessage{}, false
	}
	text := extractEventText(raw)
	mentions, _ := stringSlice(raw, "mentions", "at_users", "atUsers")
	replyTo := false
	if q, ok := raw["quoted_message"]; ok {
		var quoted map[string]json.RawMessage
		if json.Unmarshal(q, &quoted) == nil {
			// 无法可靠判断 quoted 是否机器人→本人；保持 false，由上层 @ 事实决定。
			_ = quoted
		}
	}
	return DWSMessage{
		ID:               id,
		ConversationID:   conv,
		ConversationType: convType,
		ConversationName: str("conversation_name", "chat_name", "conversationTitle"),
		SenderID:         senderOpenID,
		SenderName:       str("sender", "sender_name", "senderNick"),
		SenderBot:        boolField(raw, "sender_bot", "senderBot", "bot"),
		Text:             text,
		At:               at,
		Mentions:         mentions,
		ReplyToBot:       replyTo,
		AddressedToBot:   addressedByKind(listenKind, convType),
	}, true
}

func addressedByKind(listenKind, convType string) bool {
	switch strings.ToLower(strings.TrimSpace(listenKind)) {
	case ListenAtMe, ListenGroup:
		return true
	case ListenAllDirect:
		return true
	case ListenAllGroup:
		return convType == "direct"
	default:
		return convType == "direct"
	}
}

func extractEventText(raw map[string]json.RawMessage) string {
	if v, ok := raw["text"]; ok {
		var s string
		if json.Unmarshal(v, &s) == nil && s != "" {
			return s
		}
	}
	if v, ok := raw["content"]; ok {
		// content 可能是纯字符串，也可能是对象（{text}|{content}|{title,text}）。
		var s string
		if json.Unmarshal(v, &s) == nil {
			return s
		}
		var obj map[string]json.RawMessage
		if json.Unmarshal(v, &obj) == nil {
			for _, key := range []string{"text", "content", "title"} {
				if inner, ok := obj[key]; ok {
					var s string
					if json.Unmarshal(inner, &s) == nil && s != "" {
						return s
					}
				}
			}
			if inner, ok := obj["content"]; ok {
				var parts []map[string]json.RawMessage
				if json.Unmarshal(inner, &parts) == nil {
					var b strings.Builder
					for _, part := range parts {
						for _, key := range []string{"text", "content"} {
							if pv, ok := part[key]; ok {
								var s string
								if json.Unmarshal(pv, &s) == nil && s != "" {
									b.WriteString(s)
								}
							}
						}
					}
					if b.Len() > 0 {
						return b.String()
					}
				}
			}
		}
	}
	return ""
}

func firstTime(raw map[string]json.RawMessage, keys ...string) (time.Time, bool) {
	for _, key := range keys {
		v, ok := raw[key]
		if !ok {
			continue
		}
		if t, ok := parseEventTime(v); ok {
			return t, true
		}
	}
	return time.Time{}, false
}

func parseEventTime(v json.RawMessage) (time.Time, bool) {
	var s string
	if json.Unmarshal(v, &s) == nil {
		s = strings.TrimSpace(s)
		if s == "" {
			return time.Time{}, false
		}
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			return t, true
		}
		if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
			return t, true
		}
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			return unixMillis(n), true
		}
		return time.Time{}, false
	}
	var n int64
	if json.Unmarshal(v, &n) == nil {
		return unixMillis(n), true
	}
	var f float64
	if json.Unmarshal(v, &f) == nil && f > 0 {
		return unixMillis(int64(f)), true
	}
	return time.Time{}, false
}

func unixMillis(n int64) time.Time {
	if n > 1e15 { // 微秒
		return time.Unix(0, n*1000).UTC()
	}
	if n > 1e12 { // 毫秒
		return time.UnixMilli(n).UTC()
	}
	return time.Unix(n, 0).UTC()
}

func boolField(raw map[string]json.RawMessage, keys ...string) bool {
	for _, key := range keys {
		if v, ok := raw[key]; ok {
			var b bool
			if json.Unmarshal(v, &b) == nil {
				return b
			}
			var s string
			if json.Unmarshal(v, &s) == nil {
				return strings.EqualFold(s, "true") || s == "1"
			}
		}
	}
	return false
}

func stringSlice(raw map[string]json.RawMessage, keys ...string) ([]string, bool) {
	for _, key := range keys {
		v, ok := raw[key]
		if !ok {
			continue
		}
		var out []string
		if json.Unmarshal(v, &out) == nil {
			return compactStrings(out), true
		}
		var objects []map[string]json.RawMessage
		if json.Unmarshal(v, &objects) == nil {
			for _, obj := range objects {
				for _, idKey := range []string{"open_dingtalk_id", "openDingtalkId", "id", "staff_id", "staffId"} {
					if iv, ok := obj[idKey]; ok {
						var s string
						if json.Unmarshal(iv, &s) == nil && s != "" {
							out = append(out, s)
							break
						}
					}
				}
			}
			return compactStrings(out), true
		}
	}
	return nil, false
}

func compactStrings(in []string) []string {
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

func rawEventFromDWS(msg DWSMessage) im.RawEvent {
	kind := domain.ConvGroup
	if msg.ConversationType == "direct" {
		kind = domain.ConvDirect
	}
	return im.RawEvent{
		ID:             msg.ID,
		Conv:           domain.Conversation{Kind: kind, ID: msg.ConversationID, Name: msg.ConversationName},
		Actor:          domain.Actor{ID: msg.SenderID, Bot: msg.SenderBot},
		Text:           msg.Text,
		Mentions:       append([]string(nil), msg.Mentions...),
		ReplyToBot:     msg.ReplyToBot,
		AddressedToBot: msg.AddressedToBot,
		At:             msg.At.UnixMilli(),
	}
}

// --- 错误解析（stderr/JSON）---

type dwsWireError struct {
	Status     int             `json:"status"`
	HTTPStatus int             `json:"httpStatus"`
	StatusCode int             `json:"statusCode"`
	RetryAfter int             `json:"retryAfter"`
	Code       json.RawMessage `json:"code"`
	Message    string          `json:"message"`
	Error      json.RawMessage `json:"error"`
}

func detectDWSError(stderr, stdout []byte) (status, code int) {
	for _, blob := range [][]byte{stderr, stdout} {
		if s, c := parseDWSErrorJSON(blob); s != 0 || c != 0 {
			return s, c
		}
	}
	for _, blob := range [][]byte{stderr, stdout} {
		if m := httpStatusText.FindSubmatch(blob); m != nil {
			if n, err := strconv.Atoi(string(m[1])); err == nil {
				return n, 0
			}
		}
	}
	return 0, 0
}

func parseDWSErrorJSON(blob []byte) (status, code int) {
	start := bytes.IndexByte(blob, '{')
	if start < 0 {
		return 0, 0
	}
	candidates := [][]byte{blob[start:]}
	if end := bytes.LastIndexByte(blob, '}'); end > start {
		candidates = append(candidates, blob[start:end+1])
	}
	for _, candidate := range candidates {
		var wire dwsWireError
		if err := json.Unmarshal(candidate, &wire); err != nil {
			continue
		}
		status = firstNonZero(wire.Status, wire.HTTPStatus, wire.StatusCode)
		if status < 100 || status > 599 {
			status = 0
		}
		code = parseJSONCode(wire.Code)
		if code == 0 {
			code = parseJSONCode(wire.Error)
		}
		if status != 0 || code != 0 {
			return status, code
		}
	}
	return 0, 0
}

func firstNonZero(values ...int) int {
	for _, v := range values {
		if v != 0 {
			return v
		}
	}
	return 0
}

func parseJSONCode(raw json.RawMessage) int {
	if len(raw) == 0 {
		return 0
	}
	var n int
	if json.Unmarshal(raw, &n) == nil {
		return n
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		if parsed, err := strconv.Atoi(strings.TrimSpace(s)); err == nil {
			return parsed
		}
	}
	return 0
}

func classifyDWSCode(code int) int {
	switch {
	case code >= 100000:
		return code / 1000
	case code >= 400 && code <= 599:
		return code
	default:
		return 0
	}
}

var retryAfterText = regexp.MustCompile(`(?i)retry[-_ ]?after["']?\s*[:=]\s*"?([0-9]+)`)

func detectDWSRetryAfter(stderr, stdout []byte) time.Duration {
	for _, blob := range [][]byte{stderr, stdout} {
		if m := retryAfterText.FindSubmatch(blob); m != nil {
			if n, err := strconv.Atoi(string(m[1])); err == nil && n > 0 {
				return time.Duration(n) * time.Second
			}
		}
	}
	return 0
}

var httpStatusText = regexp.MustCompile(`(?i)\b(?:HTTP[ /]?|status["']?\s*[:=]\s*)["']?([45]\d{2})\b`)

// redactDetail 去掉 detail 里的凭据/令牌。
func redactDetail(s string, secrets []string) string {
	for _, secret := range secrets {
		if len(secret) >= 4 {
			s = strings.ReplaceAll(s, secret, "[redacted]")
		}
	}
	s = secretKVPattern.ReplaceAllString(s, "$1=[redacted]")
	s = bearerPattern.ReplaceAllString(s, "$1 [redacted]")
	s = urlQuerySecretPattern.ReplaceAllString(s, "$1[redacted]")
	return s
}

var (
	secretKVPattern       = regexp.MustCompile(`(?i)\b(authorization|proxy-authorization|access[_-]?token|refresh[_-]?token|client[_-]?secret|app[_-]?secret|sessionwebhook|ticket|token|secret|password)\b\s*[:=]\s*("[^"]*"|'[^']*'|[^\s,;]+)`)
	bearerPattern         = regexp.MustCompile(`(?i)\b(bearer|basic)\s+[A-Za-z0-9._~+/=-]+`)
	urlQuerySecretPattern = regexp.MustCompile(`(?i)([?&](?:token|ticket|secret|access_token|session)[^=]*=)[^&\s]+`)
)

var urlPattern = regexp.MustCompile(`https?://[^\s"'<>]+`)

func extractURL(text string) string {
	if m := urlPattern.FindString(text); m != "" {
		return m
	}
	return ""
}

func truncateDetail(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > maxDWSDetail {
		return s[:maxDWSDetail] + "…"
	}
	return s
}

func orDefaultText(value, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
}
