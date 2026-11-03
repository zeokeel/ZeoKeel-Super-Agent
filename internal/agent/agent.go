// Package agent 是 Worker 的大脑：拿一个 assignment，在固定的工作区里用带工具的
// 模型跑完，并把每一步变成事件交给面板。
//
// 编排用 eino 的 ADK（ChatModelAgent + Runner + 官方 middlewares）；模型走
// OpenAI 兼容端点。这一层真正要补的是 SDK 不管的部分：给桌面面板的事件流、
// 会话事件日志的落盘与崩溃恢复、上下文压缩与工具结果截断。
package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/adk/session"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	einoopenai "github.com/cloudwego/eino-ext/components/model/openai"
)

// Config 描述一次 Worker 运行的配置。
type Config struct {
	// BaseURL 是 OpenAI 兼容端点，例如本地 mock 或模型网关。
	BaseURL string
	APIKey  string
	Model   string
	// Workdir 是工具唯一可以触碰的目录。
	Workdir string
	// BlockedCommands 以"同名命令不可用"的方式拒绝，只是默认值，不是安全边界（design §5.5）。
	BlockedCommands []string
	MaxSteps        int
	// ModelTimeout is the limit for one model request, streaming included. Zero
	// means thirty minutes: a large reasoning model on a slow link can think for a
	// long time per turn, and a short cap turns "slow" into "broken" (design §5.8).
	ModelTimeout time.Duration
	// BashTimeout is the default limit for one shell command. Zero means 60s.
	// A deployment with slow builds raises it in the panel (design §5.5).
	BashTimeout time.Duration
	// BackgroundAfter is how long a command may stay in the foreground before it
	// becomes a background job (design §5.6: 5s). Zero means the default 5s; a
	// negative value disables backgrounding and makes bash block until the command
	// finishes or its timeout expires.
	BackgroundAfter time.Duration
	// PlanTasks injects eino's adk/middlewares/plantask tools (TaskCreate, TaskGet,
	// TaskUpdate, TaskList) over a Backend rooted at Workdir, so a Worker can keep a
	// structured plan while it works. Off by default: the runtime enables it.
	PlanTasks bool
	// AgentsMD injects the workspace's AGENTS.md into the model input through
	// eino's adk/middlewares/agentsmd. Off by default; a missing file is not an
	// error (the middleware reports it through a warning callback).
	AgentsMD bool
	// WebTools adds the web_fetch and web_search tools. Off by default; the runtime
	// enables it for deployments that may reach the network.
	WebTools bool
	// WebSearchURL is the HTML search endpoint web_search uses, with a %s slot for
	// the URL-encoded query. Empty means the DuckDuckGo HTML endpoint.
	WebSearchURL string
	// Env is injected into the bash child's environment after the OS environment is
	// scrubbed. The runtime uses it for non-secret per-Ticket variables
	// (ZEOKEEL_*); values win over same-named OS/agent variables and are never
	// logged. Because the runtime owns this map, it must not put credentials here.
	Env map[string]string
	// ExtraTools 追加在工作区工具之后，给测试和特定工作区能力用。
	ExtraTools []tool.BaseTool

	// SessionDir 是会话事件日志的落盘目录。留空时放在 <Workdir>/.zeokeel/sessions。
	// 事件日志即事实来源（design §5.2）：进程崩了之后，RunSession 从它重建模型输入，
	// 不依赖任何内存状态。
	SessionDir string
	// SessionStore 允许上层替换事件存储（例如落 SQLite）。留空时用 eino 官方的
	// 文件实现；只要实现 adk.SessionEventStore 接口即可，Runner 不关心存在哪。
	SessionStore adk.SessionEventStore[*schema.Message]
	// ContextBudget 是估算的上下文 token 预算。超预算时把早期消息摘要成一条
	// compact 消息、只留最近几条原文，用官方 summarization 中间件实现。
	ContextBudget int
	// CompactAfterMessages 是消息条数的粗粒度上限：超过它也会触发压缩。
	// token 估算在没有真实 usage 时不可靠（例如 mock 或本地小模型），条数上限是
	// 一道兜底；0 表示只用 token 预算。
	CompactAfterMessages int
	// Subagents, when set, are handed to the Worker through the official subagent
	// middleware: the main agent can push a bounded investigation into a nested
	// agent instead of carrying it in one context (design §5.6). Empty means no
	// subagents, which is the default: they cost a model call each.
	Subagents []SubagentSpec
	// ToolOutputLimit 是回给模型的单条工具结果字节上限。超长输出落盘并把摘要与
	// 文件路径交给模型，否则一次 cat 大文件就能吃掉整个上下文预算（design §5.4）。
	ToolOutputLimit int
}

// SubagentSpec is one nested agent the Worker may spawn. It gets its own
// instruction and a subset of tools, so an investigator cannot write files and a
// writer cannot roam the repo (design §5.6: 能力差异靠工具白名单，不靠提示词).
type SubagentSpec struct {
	Name          string
	Description   string
	Instruction   string
	Tools         []string
	MaxIterations int
}

// EventKind 说明一次运行中的某一步是什么。
type EventKind string

const (
	// EventText 是模型答案的一段增量文本。
	EventText EventKind = "text"
	// EventToolStart 是一个即将执行的工具调用。
	EventToolStart EventKind = "tool_start"
	// EventToolEnd 是一个执行完的工具调用及其输出。
	EventToolEnd EventKind = "tool_end"
	// EventDone 关闭一次运行。
	EventDone EventKind = "done"
	// EventError 报告一次失败。
	EventError EventKind = "error"
	// EventUsage 是一次模型调用的 token 用量，供面板显示"已用 X tokens"（design §5.4）。
	EventUsage EventKind = "usage"
	// EventCompact 表示上下文压缩刚发生：面板要能画出"早期历史已被摘要"。
	EventCompact EventKind = "compact"
	// EventCanceled 表示运行被 context 取消，而不是模型或工具失败。
	EventCanceled EventKind = "canceled"
	// EventResume 表示这是一次从会话日志恢复后的继续运行。
	EventResume EventKind = "resume"
)

// Usage 是一次模型调用的 token 用量。
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
	// CachedTokens is how much of the prompt the provider served from its KV cache.
	// It is the only observable signal of whether our request prefix is stable, so
	// it is reported rather than inferred (design §5.4: 预算可观测).
	CachedTokens int `json:"cached_tokens"`
	// ReasoningTokens is the thinking budget some models report separately.
	ReasoningTokens int `json:"reasoning_tokens"`
}

// Add 把另一次调用的用量累加进来。摘要调用也算真实成本，所以一并计入。
func (u *Usage) Add(other Usage) {
	if u == nil {
		return
	}
	u.PromptTokens += other.PromptTokens
	u.CompletionTokens += other.CompletionTokens
	u.TotalTokens += other.TotalTokens
	u.CachedTokens += other.CachedTokens
	u.ReasoningTokens += other.ReasoningTokens
}

// CompactInfo 描述一次上下文压缩：压缩前后消息数，以及摘要文本（截断后给面板用）。
type CompactInfo struct {
	MessagesBefore int    `json:"messages_before"`
	MessagesAfter  int    `json:"messages_after"`
	Summary        string `json:"summary"`
}

// Event 是运行向面板上报的一步。面板把它画成流里的一行。
//
// Seq 在单个会话内单调递增。事件可能来自 eino 的执行协程（工具/模型回调）与
// 运行协程两边，所以 emit 的调用顺序不保证等于发生顺序，面板应按 Seq 排序。
type Event struct {
	Kind     EventKind
	Tool     string
	Args     string
	Output   string
	ExitCode int
	Duration time.Duration
	Text     string
	Err      error

	Seq       uint64
	SessionID string
	// Usage 只在 EventUsage 上非空。
	Usage *Usage
	// Compact 只在 EventCompact 上非空。
	Compact *CompactInfo
}

// RunRequest 描述一次带会话的运行。SessionID 留空表示新建会话。
type RunRequest struct {
	SessionID string
	System    string
	Task      string
}

// RunResult 是一次会话运行的结果。
type RunResult struct {
	SessionID string
	Answer    string
	// Usage 是本次运行所有模型调用（含摘要调用）的累计用量。
	Usage Usage
	// Compactions 是本次运行触发的上下文压缩次数。
	Compactions int
	// Resumed 表示这次运行在运行前修复了中断的会话日志（补了悬空 tool_call）。
	Resumed bool
}

// sessionStats 是从会话事件日志里算出来的面板指标（design §5.4：预算可观测）。
// 它只作为包内统计使用：没有生产调用者时不要导出成 API（F7），面板若要展示
// 应让 runtime 投影自己的 usage/events，而不是解析 agent 的事件日志。
type sessionStats struct {
	SessionID        string
	Turns            int
	Messages         int
	Compactions      int
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
	// Interrupted 是日志里没有配到结果的工具调用数。大于 0 说明上次运行在工具调用中途断了。
	Interrupted int
	// Idle 表示日志以"回合正常结束"收尾。
	Idle bool
	// LastAssistant 是最后一条非空助手消息，面板可以拿它显示"上次说到哪"。
	LastAssistant string
}

// Runner 为一个工作区执行 assignment。
type Runner struct {
	cfg   Config
	tools []tool.BaseTool
	// store 是事件存储（默认 eino ADK 的文件实现）：append-only、按会话分。Runner
	// 对同一会话是单写者（design §5.2），并发由上层保证。
	store adk.SessionEventStore[*schema.Message]
	// jobs 是后台作业注册表（design §5.6）：bash 命令在前台超时后转入这里，
	// job_output/job_kill 工具与 Runner.Jobs() 都走它。
	jobs *jobRegistry
}

var sessionIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// New 构造一个 Runner。工具绑定在 cfg.Workdir 上，两个工作区不会共用一个。
func New(cfg Config) (*Runner, error) {
	if cfg.MaxSteps == 0 {
		cfg.MaxSteps = 24
	}
	if cfg.ContextBudget == 0 {
		cfg.ContextBudget = 64 * 1024
	}
	if cfg.ToolOutputLimit == 0 {
		cfg.ToolOutputLimit = 32 * 1024
	}
	if cfg.Workdir == "" {
		return nil, errors.New("agent: a workspace directory is required")
	}
	root, err := filepath.Abs(cfg.Workdir)
	if err != nil {
		return nil, fmt.Errorf("agent: workdir: %w", err)
	}
	cfg.Workdir = root
	if cfg.SessionDir == "" {
		cfg.SessionDir = filepath.Join(root, ".zeokeel", "sessions")
	}
	sessionDir, err := filepath.Abs(cfg.SessionDir)
	if err != nil {
		return nil, fmt.Errorf("agent: session dir: %w", err)
	}
	cfg.SessionDir = sessionDir

	tools, jobs, err := workspaceTools(cfg)
	if err != nil {
		return nil, err
	}
	if cfg.WebTools {
		web, err := newWebTools(cfg.WebSearchURL)
		if err != nil {
			return nil, err
		}
		tools = append(tools, web...)
	}
	tools = append(tools, cfg.ExtraTools...)

	store := cfg.SessionStore
	if store == nil {
		fileStore, err := session.NewFileStore[*schema.Message](sessionDir, nil)
		if err != nil {
			return nil, fmt.Errorf("agent: session store: %w", err)
		}
		store = fileStore
	}
	return &Runner{cfg: cfg, tools: tools, store: store, jobs: jobs}, nil
}

// Config 返回 Runner 生效中的配置（默认值已填好），便于上层决定会话目录等。
func (r *Runner) Config() Config { return r.cfg }

// RunSession 跑一次带会话的 assignment：事件落盘到 SessionDir，崩溃后以同一
// SessionID 再跑一次就是恢复（也是 runtime 的生产路径，见 runtime/worker.go）。
//
// 同一 SessionID 的第二次调用会把日志里的历史重建成模型输入再继续，所以它天然
// 支持"人补充一句话，agent 接着干"的语义；日志里若有未配对的 tool_call，会先
// 补 interrupted 说明再续跑（F6/N-2）。system 是本次运行的 system prompt，它不进
// 事件日志；恢复时调用方需要再传一次（通常来自同一份 Worker 配置）。
//
// emit 可能被 eino 的执行协程调用，但 recorder 会串行化，所以 emit 内部不必再加锁；
// 它必须只做转发，不能长时间阻塞，否则会拖住模型/工具回调。
func (r *Runner) RunSession(ctx context.Context, req RunRequest, emit func(Event)) (RunResult, error) {
	if emit == nil {
		emit = func(Event) {}
	}
	if req.SessionID == "" {
		id, err := newSessionID()
		if err != nil {
			return RunResult{}, err
		}
		req.SessionID = id
	}
	if err := validateSessionID(req.SessionID); err != nil {
		return RunResult{}, err
	}
	if req.Task == "" {
		return RunResult{}, errors.New("agent: task is required")
	}
	// 崩溃重启走的是 RunSession（runtime/worker.go 用同一个 session id 再跑一遍）；
	// 它也是唯一的恢复入口（导出方法 Resume 因生产零调用者已删除）。恢复语义因此
	// 必须长在 RunSession 上：日志里若
	// 有"有 tool_call、没 tool 结果"的历史，先补一条 interrupted 说明再继续，
	// 否则重启后的第一次模型请求会被真实 provider 以 400 拒绝（design §5.8）。
	resumed := false
	events, err := r.loadEvents(ctx, req.SessionID)
	if err != nil {
		return RunResult{}, err
	}
	if len(events) > 0 {
		patched, err := r.patchInterruptedToolCalls(ctx, req.SessionID, events)
		if err != nil {
			return RunResult{}, err
		}
		if patched > 0 {
			req.Task = recoveryTask(patched) + "\n\n" + req.Task
			resumed = true
		}
	}
	return r.runADK(ctx, req, emit, resumed)
}

func (r *Runner) loadEvents(ctx context.Context, sessionID string) ([]*adk.SessionEvent[*schema.Message], error) {
	res, err := r.store.LoadEvents(ctx, sessionID, nil)
	if err != nil {
		// 进程在 append 中途被杀会留下没有换行的半行；官方 FileStore 把它当
		// 损坏并拒绝加载。恢复的定义就是"能接着跑"，所以我们先裁掉那半行再读一次。
		if repairErr := r.repairTornEventLog(sessionID); repairErr == nil {
			res, err = r.store.LoadEvents(ctx, sessionID, nil)
		}
	}
	if err != nil {
		return nil, fmt.Errorf("agent: load session events: %w", err)
	}
	if res == nil {
		return nil, nil
	}
	return res.Events, nil
}

func (r *Runner) newChatModel(ctx context.Context) (model.ToolCallingChatModel, error) {
	mb, err := einoopenai.NewChatModel(ctx, &einoopenai.ChatModelConfig{
		BaseURL: r.cfg.BaseURL,
		APIKey:  r.cfg.APIKey,
		Model:   r.cfg.Model,
		Timeout: modelTimeout(r.cfg.ModelTimeout),
	})
	if err != nil {
		return nil, fmt.Errorf("agent: model: %w", err)
	}
	return mb, nil
}

func validateSessionID(id string) error {
	if !sessionIDPattern.MatchString(id) {
		return fmt.Errorf("agent: invalid session id %q", id)
	}
	return nil
}

// newSessionID 用 crypto/rand 生成会话 ID，避免引入额外依赖，也避免两个 Worker 撞号。
func newSessionID() (string, error) {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("agent: session id: %w", err)
	}
	return "worker-" + hex.EncodeToString(b[:]), nil
}

// newSessionEventID 给手写进日志的事件（如 interrupted 补丁）生成唯一 ID；
// adk 的 FileStore 要求 EventID 非空且会话内唯一。
func newSessionEventID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("agent: session event id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// nowFunc 允许测试在需要时替换时钟；现在只由事件耗时使用。
var nowFunc = time.Now

// recorder 把 eino 两侧（事件循环与回调）产生的步骤收敛成一个带序号的流。
type recorder struct {
	mu sync.Mutex
	// emitMu 串行化 emit：事件来自事件循环与 eino 回调两侧，调用方（面板/运行时）
	// 不该被迫自己处理并发。代价是 emit 变慢会拖住回调，所以 emit 必须只做转发。
	emitMu    sync.Mutex
	cfg       Config
	sessionID string
	emit      func(Event)
	seq       uint64

	// streamWG 等旁路文本发完再收尾，否则 done 可能先于最后几个 text 事件；
	// usageWG 等同理（用量在后台读回调流）。两者都在发 done 前 wait。
	streamWG sync.WaitGroup
	usageWG  sync.WaitGroup

	answer      []byte
	usage       Usage
	compactions int
	// compactSink 让压缩事件除了发给面板，还能写进会话日志；它在 runADK 里绑定。
	compactSink func(CompactInfo)

	toolStart map[string]toolStartInfo
}

type toolStartInfo struct {
	name  string
	args  string
	start time.Time
}

func newRecorder(cfg Config, sessionID string, emit func(Event)) *recorder {
	return &recorder{cfg: cfg, sessionID: sessionID, emit: emit, toolStart: map[string]toolStartInfo{}}
}

// send 串行化事件序号。emit 在锁外调用：面板的 emit 可能同步做很多事，不能让它
// 阻塞其他回调。
func (r *recorder) send(ev Event) {
	r.mu.Lock()
	r.seq++
	ev.Seq = r.seq
	if ev.SessionID == "" {
		ev.SessionID = r.sessionID
	}
	r.mu.Unlock()
	r.emitMu.Lock()
	r.emit(ev)
	r.emitMu.Unlock()
}

func (r *recorder) addText(text string) {
	if text == "" {
		return
	}
	r.mu.Lock()
	r.answer = append(r.answer, text...)
	r.mu.Unlock()
	r.send(Event{Kind: EventText, Text: text})
}

// addUsage 接受 eino 回调的用量类型。模型每调用一次（包括压缩时的摘要调用）都会
// 上报一次，面板据此显示"本 Ticket 已用 X tokens"。
func (r *recorder) addUsage(u *model.TokenUsage) {
	if u == nil || (u.PromptTokens == 0 && u.CompletionTokens == 0 && u.TotalTokens == 0) {
		return
	}
	usage := Usage{
		PromptTokens:     u.PromptTokens,
		CompletionTokens: u.CompletionTokens,
		TotalTokens:      u.TotalTokens,
		CachedTokens:     u.PromptTokenDetails.CachedTokens,
		ReasoningTokens:  u.CompletionTokensDetails.ReasoningTokens,
	}
	if usage.TotalTokens == 0 {
		usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	}
	r.mu.Lock()
	r.usage.Add(usage)
	r.mu.Unlock()
	r.send(Event{Kind: EventUsage, Usage: &usage})
}

func (r *recorder) addCompact(info CompactInfo) {
	r.mu.Lock()
	r.compactions++
	sink := r.compactSink
	r.mu.Unlock()
	r.send(Event{Kind: EventCompact, Compact: &info})
	if sink != nil {
		sink(info)
	}
}

func (r *recorder) result(sessionID string, resumed bool) RunResult {
	r.mu.Lock()
	defer r.mu.Unlock()
	return RunResult{
		SessionID:   sessionID,
		Answer:      string(r.answer),
		Usage:       r.usage,
		Compactions: r.compactions,
		Resumed:     resumed,
	}
}

func (r *recorder) waitStreams() {
	r.streamWG.Wait()
	r.usageWG.Wait()
}

func (r *recorder) finalAnswer() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return string(r.answer)
}

// done 收尾：发 done，并把累计答案带上（老面板只读 Text 也能工作）。
func (r *recorder) done(start time.Time) {
	r.mu.Lock()
	answer := string(r.answer)
	r.mu.Unlock()
	r.send(Event{Kind: EventDone, Text: answer, Duration: time.Since(start)})
}

// modelTimeout is the per-request limit, with a default generous enough for a
// reasoning model on a slow link.
func modelTimeout(d time.Duration) time.Duration {
	if d > 0 {
		return d
	}
	return 30 * time.Minute
}
