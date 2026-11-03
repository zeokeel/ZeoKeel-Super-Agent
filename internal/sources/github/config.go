// Package github 是 GitHub 这一种 Source kind：把 gh CLI 读到的通知与
// Issue/PR 评论翻译成 domain.Trigger，并把回复以幂等评论写回去。
//
// 为什么走 `gh` 而不是自己发 HTTP：认证、企业域名、私有仓库权限这些都由 gh
// 处理，ZeoKeel 不再造一套凭据栈。但 CLI 被封装成 Command 接口——测试永远
// 不真的调用 gh 或网络，生产才用 ExecCommand。
package github

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/zeokeel/super-agent/internal/domain"
	"github.com/zeokeel/super-agent/internal/sources"
)

// Name is the stable Source id. 它出现在 Trigger.Source、Thread key 前缀和
// Policy 里，所以永远不改（ADR-6）。
const Name = sources.Name("github")

// Config is one GitHub Source instance's configuration. 一个实例盯一个仓库：
// GitHub Source 是 workspace-scoped 的，绑到哪个工作区由 Workspace 决定，
// 两个仓库的 Thread key 与游标因此天然隔离（§4.7 修正 1）。
type Config struct {
	// Repository is the watched repository, "owner/name".
	Repository string `json:"repository"`

	// CLI 是 gh 命令：PATH 上的名字，或一个用 GH_CONFIG_DIR 登录了 agent
	// 账号的包装脚本的绝对路径。空表示 "gh"。
	CLI string `json:"cli,omitempty"`

	// Login 是 agent 自己的 GitHub 登录名。它自己的评论永远不是请求，别人
	// @ 它就是"明确对 agent 说话"。
	Login string `json:"login"`

	// Mention 是人们在正文里写的 handle，默认等于 Login。分开是为了让账号
	// 叫 zeokeel-agent、而大家 @zeokeel 时也能认出来。
	Mention string `json:"mention,omitempty"`

	// Workspace 把本 Source 绑到一个工作区。设置后 Trigger 带该工作区的
	// Scope 与 DefaultWorkspace；为空时是 Global，由 Router 决定去哪儿。
	Workspace domain.WorkspaceID `json:"workspace,omitempty"`

	// ObserveComments 打开"接收仓库里的评论"：没 @ agent 的评论也收下，由
	// Policy 的 admission table 决定是否成为候选（§4.3 / §4.9）。默认关闭：
	// 群里/仓库里没 @ 的讨论默认不进 Eval，宁可漏接也不误接。
	ObserveComments bool `json:"observeComments,omitempty"`

	// ReceiveAll 打开"接收仓库里的一切"：评论、Issue、PR 本体与 PR 行内
	// review 评论。这是原型 Observable 四格里的"接收全部"（参考原型
	// observeIssues/observePulls/observeComments）；打开后 Scope 对没 @ 的
	// 事件返回 Observed=true，是否成为候选仍由 Policy 的 admission table 决定。
	// 它隐含 ObserveComments，保留后者是为了旧配置继续可用。
	ReceiveAll bool `json:"receiveAll,omitempty"`

	// ObserveCI 打开"接收默认分支 CI"：检查默认分支 HEAD 的 check-runs，
	// 失败集合变化时发一条 Trigger（第一次观察只建基线；检查还在跑时不报
	// "恢复"，避免误报）。见原型 pollBranchCi。
	ObserveCI bool `json:"observeCi,omitempty"`

	// Self 是 operator 自己的 GitHub 账号（可多个）。它们的评论置
	// Trigger.Self，同一个请求从"你"嘴里说出来和从同事嘴里说出来含义不同
	// （§4.10）。
	Self []string `json:"self,omitempty"`
}

// repositoryPattern 与 GitHub 的 owner/name 形状一致。在 Source 里先校验，
// 免得把一个拼错的仓库带到每一轮轮询的 URL 里。
var repositoryPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

// Source is one configured GitHub Source instance.
type Source struct {
	cfg Config
	cmd Command
	// now 可替换，让游标与时间戳的测试不依赖真实时钟。
	now func() time.Time
}

// 编译期断言：接口一旦漂移，构建就失败，而不是等到 Runtime 里断言 panic。
var (
	_ sources.Kind           = (*Source)(nil)
	_ sources.Poller         = (*Source)(nil)
	_ sources.MemberLister   = (*Source)(nil)
	_ sources.HealthReporter = (*Source)(nil)
)

// New builds one GitHub Source. cmd 由调用方注入：生产传 ExecCommand，测试
// 传 fake，所以构造本身永远不碰网络。
func New(cfg Config, cmd Command) (*Source, error) {
	if cmd == nil {
		return nil, errors.New("github: nil command")
	}
	cfg.Repository = strings.TrimSpace(cfg.Repository)
	cfg.Login = strings.TrimPrefix(strings.TrimSpace(cfg.Login), "@")
	cfg.Mention = strings.TrimPrefix(strings.TrimSpace(cfg.Mention), "@")
	cfg.CLI = strings.TrimSpace(cfg.CLI)
	if cfg.Repository == "" {
		return nil, errors.New("github: repository is required (owner/name)")
	}
	if !repositoryPattern.MatchString(cfg.Repository) {
		return nil, fmt.Errorf("github: repository %q is not owner/name", cfg.Repository)
	}
	if cfg.Login == "" {
		return nil, errors.New("github: login is required")
	}
	if cfg.Mention == "" {
		cfg.Mention = cfg.Login
	}
	for i, self := range cfg.Self {
		cfg.Self[i] = strings.TrimPrefix(strings.TrimSpace(self), "@")
	}
	return &Source{cfg: cfg, cmd: cmd, now: time.Now}, nil
}

// Name implements sources.Kind.
func (*Source) Name() sources.Name { return Name }

// mention 是正文里用来 @ 本 agent 的 handle。
func (s *Source) mention() string { return s.cfg.Mention }

// triggerScope 把配置里的工作区变成 Trigger 的 Scope / DefaultWorkspace。
// 没有工作区时是 Global：Trigger 不能自己猜工作区，那是 Router 的活（§4.7）。
func (s *Source) triggerScope() (domain.Scope, domain.WorkspaceID) {
	if s.cfg.Workspace != "" {
		return domain.WorkspaceScope(s.cfg.Workspace), s.cfg.Workspace
	}
	return domain.Global, ""
}

// isSelf 判断某个 login 是不是 operator 自己的账号。
func (s *Source) isSelf(login string) bool {
	for _, self := range s.cfg.Self {
		if strings.EqualFold(self, login) {
			return true
		}
	}
	return false
}

// init 在编译期把本 kind 注册进 sources 注册表：新增 Source 就是一次代码
// 改动 + 重新编译，没有运行时插件加载（ADR-6）。
func init() {
	sources.Register(Name, func(raw []byte) (sources.Kind, error) {
		var cfg Config
		if len(bytes.TrimSpace(raw)) > 0 {
			if err := json.Unmarshal(raw, &cfg); err != nil {
				return nil, fmt.Errorf("github: bad config: %w", err)
			}
		}
		return New(cfg, ExecCommand{Path: strings.TrimSpace(cfg.CLI)})
	})
}
