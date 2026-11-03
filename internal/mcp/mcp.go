// Package mcp 把外部 MCP server 的工具接进 Worker。
//
// 位置（design §5.10）：MCP 是"组织与扩展"组里的能力，属于 Agent 的外挂工具集，
// 和内置的 bash/read/write 同一层，通过 agent.Config.ExtraTools 进入 eino 的
// ToolsNode。本包只负责"连接、发现、包装、调用、降级"，不涉及模型与提示词。
//
// 三个刻意的设计选择：
//
//  1. 工具名加前缀 mcp__<server>__<tool>。模型需要看出"这个工具来自外部进程"，
//     否则一个叫 read 的 MCP 工具会和内置 read 撞名，静默改掉语义；前缀也让面板
//     能把工具卡片按 server 分组。撞名在 eino 里不会报错，只会有一个被覆盖——
//     这正是必须自己加前缀的原因。
//
//  2. 用官方 github.com/modelcontextprotocol/go-sdk（纯 Go，已实测 CGO_ENABLED=0
//     可构建）。自己重写 JSON-RPC 只在拉不到依赖时才值得：初始化握手、协议版本
//     协商、分页、取消通知这些边角一旦自研就是长期维护负担。
//
//  3. 单个 server 挂掉只让它的工具消失，不影响其它 server。调用失败按可重试/
//     永久分类，由上层决定是否重试，避免在工具层做无限重试。
package mcp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/cloudwego/eino/components/tool"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// State 是一个 MCP server 连接的生命周期状态。
type State string

const (
	// StateNew 是"已配置、尚未连上"。
	StateNew State = "new"
	// StateReady 表示连接与 tools/list 都成功，工具可用。
	StateReady State = "ready"
	// StateDown 表示曾经连上、现在不可用；工具从列表里消失，等重连。
	StateDown State = "down"
	// StateClosed 表示 Manager 已关闭，不再重连。
	StateClosed State = "closed"
)

// 默认值都在这里，调用方传零值即可。给默认值是必要的：无人值守任务里
// 一个没设超时的 MCP 调用可以把整个 Ticket 挂死几十分钟。
const (
	defaultCallTimeout    = 60 * time.Second
	defaultConnectTimeout = 20 * time.Second
	defaultMaxResultLines = 120
	defaultMaxResultBytes = 32 * 1024
)

// ServerConfig 描述一个外部 MCP server。
//
// Command/URL 二选一：前者用 stdio 起子进程（最常见，npx/uvx/自带二进制），
// 后者连 streamable HTTP 端点。
type ServerConfig struct {
	// Name 是工具名前缀的来源，必须非空且在 Manager 内唯一。只允许
	// [a-zA-Z0-9_-]，其它字符会被替换成 _，因为它最终要变成符合 provider
	// 约束的工具名。
	Name string
	// Command/Args/Env 用于 stdio transport。
	Command string
	Args    []string
	Env     []string // "K=V" 形式，追加在 os.Environ 之后
	// URL 用于 streamable HTTP transport。
	URL        string
	HTTPClient *http.Client // 可选；nil 时用默认客户端
	// CallTimeout 覆盖全局单次调用超时。
	CallTimeout time.Duration
}

// Config 是 Manager 的配置。
type Config struct {
	Servers []ServerConfig

	DefaultCallTimeout time.Duration
	ConnectTimeout     time.Duration

	// MaxResultLines/MaxResultBytes 是单次工具结果的截断预算（design §5.4）。
	// 按"前 N 行"和字节数双上限裁：行数贴近人的阅读习惯，字节数兜住超长单行。
	//
	// 被 agent 挂载时（ExtraTools），模型看到的截断其实由 agent 的 reduction
	// 中间件统一负责；这里的上限是"MCP 独立使用/未来 CLI"时的自我保护，故意
	// 保持较宽，不重复做落盘（F13：两套落盘命名策略只应保留一套）。
	MaxResultLines int
	MaxResultBytes int
	// SpillDir 非空时，被截断的完整结果落盘到该目录，提示里给出路径。
	// 留空则只截断不落盘（纯测试或不允许写盘的场景）。
	SpillDir string
}

// Manager 管理一组 MCP server 的连接与工具。
//
// Manager 并发安全：工具调用可能来自 eino 的并行 tool call，关闭来自 UI 线程。
type Manager struct {
	cfg     Config
	servers []*serverConn

	mu     sync.RWMutex
	closed bool

	// dial 是 transport 的注入点。生产走 stdio/HTTP，测试走内存管道，
	// 这样"重启后重连"可以真跑而不需要外部进程。
	dial dialFunc
}

// NewManager 校验配置并创建 Manager，但不建立连接（用 Start）。
func NewManager(cfg Config) (*Manager, error) {
	cfg = withDefaults(cfg)
	if len(cfg.Servers) == 0 {
		return nil, errors.New("mcp: 没有配置任何 server")
	}
	m := &Manager{cfg: cfg, dial: defaultDial}
	seen := make(map[string]bool, len(cfg.Servers))
	for _, sc := range cfg.Servers {
		if err := validateServer(sc); err != nil {
			return nil, err
		}
		key := sanitizeName(sc.Name)
		if seen[key] {
			return nil, fmt.Errorf("mcp: server 名 %q 重复（去掉非法字符后为 %q）", sc.Name, key)
		}
		seen[key] = true
		m.servers = append(m.servers, &serverConn{
			cfg:         sc,
			dial:        m.dialTransport,
			callTimeout: callTimeoutFor(sc, cfg),
			state:       StateNew,
		})
	}
	return m, nil
}

func withDefaults(cfg Config) Config {
	if cfg.DefaultCallTimeout <= 0 {
		cfg.DefaultCallTimeout = defaultCallTimeout
	}
	if cfg.ConnectTimeout <= 0 {
		cfg.ConnectTimeout = defaultConnectTimeout
	}
	if cfg.MaxResultLines <= 0 {
		cfg.MaxResultLines = defaultMaxResultLines
	}
	if cfg.MaxResultBytes <= 0 {
		cfg.MaxResultBytes = defaultMaxResultBytes
	}
	return cfg
}

func callTimeoutFor(sc ServerConfig, cfg Config) time.Duration {
	if sc.CallTimeout > 0 {
		return sc.CallTimeout
	}
	return cfg.DefaultCallTimeout
}

func validateServer(sc ServerConfig) error {
	if sc.Name == "" {
		return errors.New("mcp: server name 不能为空")
	}
	if sanitizeName(sc.Name) == "" {
		return fmt.Errorf("mcp: server name %q 没有可用字符", sc.Name)
	}
	hasCmd, hasURL := sc.Command != "", sc.URL != ""
	switch {
	case hasCmd && hasURL:
		return fmt.Errorf("mcp: server %q 不能同时配置 command 与 url", sc.Name)
	case !hasCmd && !hasURL:
		return fmt.Errorf("mcp: server %q 需要 command（stdio）或 url（HTTP）之一", sc.Name)
	}
	return nil
}

// Start 建立所有连接并做首次 tools/list。
//
// 单个 server 失败不返回错误：一个配错的 server 不该让整个 Worker 起不来，
// 它的工具只是不出现在 Tools() 里。只有"配了 N 个、全部失败"才返回错误，因为
// 那基本是配置或环境整体坏掉了，值得让上层立刻知道。
func (m *Manager) Start(ctx context.Context) error {
	m.mu.RLock()
	closed := m.closed
	m.mu.RUnlock()
	if closed {
		return errors.New("mcp: manager 已关闭")
	}

	var errs []error
	for _, c := range m.servers {
		if err := c.connect(ctx, m.cfg); err != nil {
			errs = append(errs, err)
		}
	}
	if len(m.servers) > 0 && len(errs) == len(m.servers) {
		return fmt.Errorf("mcp: %d 个 server 全部连接失败: %w", len(errs), errors.Join(errs...))
	}
	return nil
}

// Tools 返回当前健康 server 的全部工具，形状与内置工具一致（eino tool.BaseTool）。
//
// 每次都重新取快照，因为工具集合会随 server 健康状态变化。eino 的 ChatModelAgent
// 只在构造时接收一次工具列表，所以上层应当在每轮开始前重新取（接线点见报告）。
func (m *Manager) Tools() []tool.BaseTool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.closed {
		return nil
	}
	var out []tool.BaseTool
	for _, c := range m.servers {
		out = append(out, c.einoTools(&m.cfg)...)
	}
	return out
}

// Close 关闭所有连接。可重复调用。
func (m *Manager) Close() error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	servers := m.servers
	m.mu.Unlock()

	var errs []error
	for _, c := range servers {
		if err := c.close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// dialFunc 建立一个 transport。抽成函数是为了让测试能用内存管道替换子进程，
// 从而在没有外部进程的前提下真跑 stdio 的换行分帧路径。
type dialFunc func(ctx context.Context, cfg ServerConfig) (mcpsdk.Transport, error)

// dialTransport 在调用时才读 m.dial，这样测试可以在 NewManager 之后注入假 hub。
// 若在构造时就把函数值拷进 serverConn，注入会静默失效——这类"看起来生效其实没生效"
// 的注入是测试里最贵的坑。
func (m *Manager) dialTransport(ctx context.Context, cfg ServerConfig) (mcpsdk.Transport, error) {
	return m.dial(ctx, cfg)
}

// defaultDial 是生产用的 transport 工厂。
func defaultDial(_ context.Context, cfg ServerConfig) (mcpsdk.Transport, error) {
	if cfg.URL != "" {
		return &mcpsdk.StreamableClientTransport{Endpoint: cfg.URL, HTTPClient: cfg.HTTPClient}, nil
	}
	cmd := exec.Command(cfg.Command, cfg.Args...)
	if len(cfg.Env) > 0 {
		cmd.Env = append(os.Environ(), cfg.Env...)
	}
	// stderr 直接透传：MCP server 的日志几乎全写 stderr，吞掉它会让"为什么连不上"
	// 变成完全无从排查。stdio 传输用 stdout 走协议，所以这里不冲突。
	cmd.Stderr = os.Stderr
	return &mcpsdk.CommandTransport{Command: cmd}, nil
}
