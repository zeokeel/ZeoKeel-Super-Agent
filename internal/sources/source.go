// Package sources is the one contract every place work can arrive from
// implements: GitHub, Feishu, a schedule, the local panel. A Source turns its
// own events into domain.Triggers and delivers replies back to the same place.
// See design §4.8 (IM 接入抽象) and §4.9 (会话类型与意图).
//
// 设计纪律：新增入口一律用 Source 吸收，不给领域加概念（§5.9）。所以这里有
// 且只有三类东西：接收（Translate + Scope）、拉取（可选 Poller）、回写
// （Channel）。领域层、Engine、Runtime 都不认识任何具体 Source。
package sources

import (
	"context"
	"time"

	"github.com/zeokeel/super-agent/internal/domain"
)

// Name is the stable id of a Source kind. It appears in Trigger.Source, Thread
// keys and Policy, so renaming one breaks history: a new kind gets a new name,
// an existing kind never changes它。
type Name string

// Raw is one event exactly as a Source received it, before translation.
// Kind 是 Source 自己的事件形状（"comment" / "notification" / "message"…），
// Data 是原生负载（通常是 JSON）。保持字节形状有两个理由：测试可以喂固定
// JSON，Source 自己拥有解析权（坏一条只算一条，不拖垮整批）。
type Raw struct {
	Kind string
	Data []byte
}

// ScopeResult 是 Source 对一个 Trigger 接收范围的判定：这条我到底听不听，
// 以及是不是在"接收全部"模式下特意收下的。
type ScopeResult struct {
	// Listened 为 false 表示这条事件不在本 Source 的地盘（别的仓库、别的
	// 会话）：绝不能 receive。Source 侧先过滤掉，比让 Gate 拒绝更诚实——
	// 它根本不该成为一条 Trigger，更不该扇出。
	Listened bool

	// Observed 为 true 表示 Source 配置了"接收全部"，所以一条没有明确对
	// agent 说话的事件也被收下了。此时是否成为候选交给 Policy 的
	// admission table（§4.3），Source 不替它做决定。默认 false：群里一条
	// 公告不该把 agent 叫起来（§4.9）。
	Observed bool

	// Row 是这条事件在 admission table 里的行名（"issue" / "pull" /
	// "comment" / "ci"），让 Gate 不必认识某个 Source 的配置就能查表。
	Row string
}

// Kind is one Source implementation. 编译期注册而不是热插拔：单机桌面不需要
// 运行时加载插件，接口 + 重新编译换来了类型安全和无协议版本问题（ADR-6）。
//
// 只有四个方法，故意做小：写一个 fake 只要实现四行，不需要网络、不需要 gh。
type Kind interface {
	// Name is the stable id（"github" / "feishu"）.
	Name() Name

	// Scope 判定一条 Trigger 是否落在本 Source 的接收范围内。它是确定性的、
	// 由配置驱动的，绝不调用模型（§4.3 的 Gate 是事实层，不是语义层）。
	Scope(t domain.Trigger) ScopeResult

	// Translate 把一条原始事件翻译成 domain.Trigger。事件读不懂时返回
	// error；拉取方把它记成一条 unreadable 然后跳过，不让整批失败。
	Translate(raw Raw) (domain.Trigger, error)

	// Channel 返回本 Source 的回写适配器。
	Channel() Channel
}

// Poller is an optional capability: a Source that pulls new events.
// 被长连接推进来的 Source（飞书、钉钉）不实现它；GitHub 这种要轮询的才实现。
type Poller interface {
	// Poll 读取一批新事件，把每条翻译好的 Trigger 交给 receive，并返回下一
	// 个游标。
	//
	// 两个约定是这个抽象里最容易写错的地方，所以写在接口上：
	//
	//  1. 游标只在"全部 receive 成功"后推进。任何一条 receive 返回 error，
	//     Poll 必须立刻停下、返回该 error，并让 Next 停在轮次开始的位置。
	//     调用方只在 err == nil 时持久化 Next。重读一批是安全的：Trigger ID
	//     内容派生，重复的那几条会被幂等挡掉（§4.4 不变量 1）。
	//
	//  2. 坏数据逐条跳过并计数（PollResult.Unreadable），不整批失败。JSON 里
	//     一条坏 item 不该让别人能干活的评论一起丢掉。
	Poll(ctx context.Context, req PollRequest, receive Receiver) (PollResult, error)
}

// Receiver 是 Source 递一条 Trigger 进来的唯一通道。实现方（Runtime 的适配
// 器）必须把"重复"当作成功返回 nil：内容派生的 ID 让重放天然安全，重放不
// 是错误。
type Receiver func(domain.Trigger) error

// PollRequest is one polling round's input.
type PollRequest struct {
	// Cursor 是上一轮成功结束后持久化的读取位置；空表示"从现在开始"。
	// 新装的实例绝不能从头重放历史（会把几年前的评论全收一遍）。
	Cursor Cursor

	// Threads 是 Engine 当前订阅的 Thread（有活工单、或最近有活动）。Source
	// 只读这些加上自己的通知，轮询次数才是有界的（原型 pollThreads 同理）。
	Threads []domain.ThreadRef
}

// PollResult is what one round did.
type PollResult struct {
	// Next 是要持久化的读取位置。契约见 Poller.Poll：只有 err == nil 时它
	// 才代表"这一批全部处理完了"。
	Next Cursor

	// Received 是成功交给 receive 的 Trigger 数。
	Received int

	// Filtered 是本 Source 地盘内、但被 Scope 排除（或本不该成为请求，比如
	// 自己账号发的评论）的事件数。丢掉也要计数，否则"为什么这条没进来"
	// 在面板上无法回答。
	Filtered int

	// Unreadable 是解析失败或字段不合法、被逐条跳过的 item 数。它不阻塞
	// 同批其他 item。
	Unreadable int
}

// Cursor is a Source's own read position, opaque to everything else.
// 只有产生它的 Source 知道怎么解释和编码；存储层把它当字符串存进 KV。
type Cursor string

// Channel posts replies back to the conversation a Trigger came from.
type Channel interface {
	// Send 投递一条消息。Key 是幂等键：同一个 Key 投两次最多产生一条消息，
	// 崩溃后重试也不重复。Gone 表示目标永久不可达（Issue 被删、被锁），
	// Outbox 据此永久保留回执而不是无限重试（§7.2）。
	Send(ctx context.Context, msg Outbound) (Receipt, error)
}

// Outbound is one message a Source must deliver.
type Outbound struct {
	// Key 是业务幂等键（Outbox 的 key），不是投递 ID。
	Key string

	// Thread 是 Source 自己的 Thread id，与 Trigger.Threads 同一个命名空间。
	Thread string

	// ReplyTo 可选：这条回复针对的事件 id（在 review thread 里回复时用到）。
	ReplyTo string

	Text string

	// Address 是从 Trigger.Channel.Address 原样带过来的 Source 原生寻址信息
	// （仓库、Issue 号、URL…）。Source 的 Channel 自己解释它。
	Address map[string]string
}

// Receipt is the outcome of one send.
type Receipt struct {
	// MessageID 是 Source 侧记住的已投递消息 id；重放时可以用来对账。
	MessageID string

	// Gone 为真表示永久不可达，Outbox 丢弃它并停止重试。
	Gone bool
}

// Member 是 Source 身份空间里的一个人，用于同步白名单。
// 身份从不跨 Source 映射（001）：同一个人的 GitHub 和飞书是两个 Member。
type Member struct {
	ID   string
	Name string
	Bot  bool
}

// MemberLister is an optional capability: a Source whose白名单可以从工具
// 侧同步（聊天成员、仓库协作者），而不是让用户手打。
type MemberLister interface {
	Members(ctx context.Context) ([]Member, error)
}

// HealthState is one Source's readiness，对应面板"来源"页的一行状态。
type HealthState string

const (
	HealthStarting HealthState = "starting"
	HealthReady    HealthState = "ready"
	HealthPaused   HealthState = "paused"
	HealthError    HealthState = "error"
)

// Health is one read-only status line for the panel.
type Health struct {
	State  HealthState
	Detail string
	At     time.Time
}

// HealthReporter is an optional capability: a Source that can检查自己的凭据
// 与最近一次轮询，并报出人话状态。缺失时面板只显示"未知"。
type HealthReporter interface {
	Health(ctx context.Context) Health
}
