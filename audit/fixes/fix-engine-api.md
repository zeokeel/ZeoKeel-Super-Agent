# fix-engine API contract (Wave 2, for nih-runtime)

> 状态：Stage 1–5 的非测试代码已编译（`CGO_ENABLED=0 go build ./internal/engine ./internal/domain ./internal/assess`），
> `go test ./internal/engine` 的测试正在重写。语义对应 `audit/fixes/wave2-spec.md` §2 与 `audit/fix-contract.md` §2.1。
> 所有权：engine 写 `internal/engine/**`（含 persist.go）。runtime 只需调用下面签名，不要改 engine 内部。

## 0. 与旧 API 的兼容性（必须知道的 4 点）

1. **Receive 不再调用模型、不再立即建 Ticket**：它只落 Trigger/扇出/ownership/gate，并把批次排进 Eval 队列。
   `Outcome.Kind` 通常是 `evaluating`（或 `filtered`/`fanout`/`owned`/`resumed`/`held`），`Outcome.Ticket` 为空。
   **runtime 的 2s pass 必须调用 `EvaluateDue(now)`**，Ticket 与 Decision 由它产生。
2. `Engine.Ticket(id)` 仍返回内部指针（仅供 engine 内部/兼容）；**runtime/UI 跨 goroutine 读取一律用
   `TicketSnapshot(id) (domain.Ticket, bool)`**（深拷贝）或 `AllTickets()`（已是拷贝）。
   `TicketByNumber`/`LastTicket` 也改为返回拷贝 `(domain.Ticket, bool)`。
3. `Outcome.TriggerIDs []domain.ID`（一批），`Outcome.Decision domain.Decision`。
4. 没有 `Strength` 的模型输出：`AssessOutput` 不再有 `Strength` 字段（assess 包已改）。

## 1. Engine 方法（新签名；`go doc` 可查）

```go
// 构造/装配
func New(assessor Assessor) *Engine
func (e *Engine) Register(w domain.Workspace, p Policy)
func (e *Engine) SetPolicy(id domain.WorkspaceID, p Policy) error
func (e *Engine) PolicyOf(id domain.WorkspaceID) (Policy, bool)
func (e *Engine) SetEvalTimeout(d time.Duration)          // 默认 90s；002:149
func (e *Engine) SetClock(now func() time.Time)            // tests

// 入口（不调模型）
func (e *Engine) Receive(t domain.Trigger) (Outcome, error) // ErrDuplicate 表示重放
func (e *Engine) Dispatch(in DispatchInput) (Outcome, error)

// 两阶段 Eval
func (e *Engine) EvaluateDue(now time.Time) ([]Outcome, error) // 每 2s pass 调用；锁外调模型
func (e *Engine) DueEvals(now time.Time) []domain.Eval
func (e *Engine) QueueLen() int
func (e *Engine) Evals() []domain.Eval

// 生命周期（人工命令；全部可持久化）
func (e *Engine) AnswerAttention(ticketID domain.ID, answer string) error
func (e *Engine) StopTicket(ticketID domain.ID, reason string) error
func (e *Engine) RetryTicket(ticketID domain.ID) error        // ErrNotRetryable
func (e *Engine) Pause()
func (e *Engine) Resume()
func (e *Engine) Paused() bool
func (e *Engine) RaiseAttention(ticketID domain.ID, question string, options []string) error
func (e *Engine) Report(ticketID domain.ID, state domain.ReportState, summary string) error // ready/dropped 会结束 Ticket（003:94-106）
func (e *Engine) StartAssignment(ticketID domain.ID) error
// InterruptAssignment: runtime 启动恢复用。把当前 running/pending Assignment 标为 ended
// (EndReason="hostRestarted", EndedAt=now)，追加一个 pending Assignment（沿用 SessionID），
// 并入队一条 Input{Kind:note, Text:"host restarted: ..."} 的 recovery note（003:146）。
func (e *Engine) InterruptAssignment(ticketID domain.ID) error
func (e *Engine) Present(ticketID domain.ID, a domain.Artifact) (domain.Artifact, error)

// 投递三段式（runtime worker inbox 用；delivered → read → turnEnded，003:112）
func (e *Engine) DeliverInput(ticketID domain.ID, in domain.Input) error
func (e *Engine) MarkInputsDelivered(ticketID domain.ID) error
func (e *Engine) MarkInputsRead(ticketID domain.ID) error
func (e *Engine) MarkTurnEnded(ticketID domain.ID) error
func (e *Engine) DeliveryFailed(ticketID domain.ID, errText string) error // 进入 person/delivery

// follows / follow-up / revalidate / policy
func (e *Engine) BindFollow(ticketID domain.ID, thread string, own bool) error
func (e *Engine) ThreadClosed(thread, outcome string, at time.Time) error
func (e *Engine) HandBack(ticketID domain.ID, thread, reason string) error
func (e *Engine) DropSubscription(ticketID domain.ID, thread, reason string) error
func (e *Engine) MarkUnrelated(ticketID domain.ID, thread, reason string) error // 源线程/own PR 线程拒绝
func (e *Engine) RevalidateAndResume(ticketID domain.ID) error
func (e *Engine) PolicyChanged(from, to string) error

// 读（拷贝；跨 goroutine 安全）
func (e *Engine) TicketSnapshot(id domain.ID) (domain.Ticket, bool)
func (e *Engine) TicketsOnThread(threadKey string) []domain.Ticket
func (e *Engine) AllTickets() []domain.Ticket
func (e *Engine) TicketByNumber(n int) (domain.Ticket, bool)
func (e *Engine) LastTicket() (domain.Ticket, bool)
func (e *Engine) Decisions() []domain.Decision
func (e *Engine) Decision(id domain.ID) (domain.Decision, bool)
func (e *Engine) Feed() []FeedItem
func (e *Engine) Counts() (triggers, decisions, tickets int)
```

## 2. Persistence 包装（durable 模式；runtime 一律用这些入口，不要裸调 Engine）

```go
func NewPersistence(e *Engine, st *store.Store, q *effects.Queue, ob *outbox.Outbox) (*Persistence, error)
func (p *Persistence) Engine() *Engine
func (p *Persistence) Load() error
func (p *Persistence) Register(w domain.Workspace, policy Policy) error
func (p *Persistence) Receive(t domain.Trigger) (Outcome, error)
func (p *Persistence) Dispatch(in DispatchInput) (Outcome, error)
func (p *Persistence) EvaluateDue(now time.Time) ([]Outcome, error) // 事务写 Decision/Ticket/Eval + enqueue launch
func (p *Persistence) SetPolicy(id domain.WorkspaceID, pol Policy) error
func (p *Persistence) PolicyChanged(from, to string) error
func (p *Persistence) AnswerAttention(ticketID domain.ID, answer string) error
func (p *Persistence) StopTicket(ticketID domain.ID, reason string) error
func (p *Persistence) RetryTicket(ticketID domain.ID) error
func (p *Persistence) HandBack(ticketID domain.ID, thread, reason string) error
func (p *Persistence) DropSubscription(ticketID domain.ID, thread, reason string) error
func (p *Persistence) MarkUnrelated(ticketID domain.ID, thread, reason string) error
func (p *Persistence) RevalidateAndResume(ticketID domain.ID) error
func (p *Persistence) BindFollow(ticketID domain.ID, thread string, own bool) error
func (p *Persistence) ThreadClosed(thread, outcome string, at time.Time) error
func (p *Persistence) DeliverInput(ticketID domain.ID, in domain.Input) error
func (p *Persistence) MarkInputsDelivered(ticketID domain.ID) error
func (p *Persistence) MarkInputsRead(ticketID domain.ID) error
func (p *Persistence) MarkTurnEnded(ticketID domain.ID) error
func (p *Persistence) DeliveryFailed(ticketID domain.ID, errText string) error
func (p *Persistence) Pause() error
func (p *Persistence) Resume() error
func (p *Persistence) Paused() bool
func (p *Persistence) InterruptAssignment(ticketID domain.ID) error // 持久化同一语义
func (p *Persistence) SaveTicket(id domain.ID) error   // 已有，worker 改 Ticket 后调用
func (p *Persistence) Queue(m outbox.Message) (bool, error)
func (p *Persistence) Reply(ticketID domain.ID, text string) (bool, error)
```

## 3. Launch effect 优先级（Stage 5，需 runtime 注册两个 kind）

- payload：`engine.LaunchPayload{TicketID, AssignmentID, Priority string}`，`Priority` = `"human"` | `"normal"`。
- kind：`engine.EffectLaunch`（"launch"）与 **新** `engine.EffectLaunchHuman`（"launch-human"）。
- Persistence 自动选择 kind：来源 Trigger 的 `Human||Self` → human，否则 normal。
- runtime 注册两个 handler：`q.Handle(engine.EffectLaunch, engine.LaunchHandler(run), effects.Options{Limit: r.concurrencyNormal})` 与
  `q.Handle(engine.EffectLaunchHuman, engine.LaunchHandler(run), effects.Options{Limit: r.concurrencyHuman})`；
  human 的 limit 建议 `1`（额外 slot），normal 为配置并发（003:59）。
- 重启恢复：EvaluateDue 只在**新建** Ticket 时入队 launch；`Persistence.Load` 不变（effect 行仍在）。

## 4. Eval / Ticket 语义要点（runtime 依赖）

- `Ticket.Inputs []domain.Input{ID,Kind,TriggerID,Thread,Text,At,Human,Context,Delivered,Read,TurnEnded,Acked,HandedBack}`。
  worker 每轮：取 `UndeliveredInputs()` → `MarkInputsDelivered` → 拼入任务 → `MarkInputsRead` → 回合结束
  （report/Report 或 `MarkTurnEnded`）。
- `Ticket.Delivery *domain.DeliveryState` 非空 = `person/delivery`（重试 2s→1min 由 runtime 决定；成功后
  `MarkInputsDelivered` 清除）。
- `Ticket.Follows []domain.Follow` + `BindFollow/ThreadClosed`：ready 后 `Report` 只在 follows 全 closed 时结束。
- `Ticket.Attention *domain.Attention`：`AnswerAttention` 清空并追加 `Input{Kind:text}` + pending Assignment。
- `domain.StageOf(t, now)` 已按 003 表逐行（含 delivery、waitsForPerson）。
- `StopTicket` 之后 Ticket 收不到任何 Input/消息（Receive 的 ended 分支跳过 stopped）。
- 人消息被 live Ticket 拥有（own Thread）时 `Receive` 返回 `DispOwned` 且**不进 Eval**；runtime 只需投递 Input。

## 5. runtime 需要配合的调用点（breaking）

1. 2s pass 里加 `p.EvaluateDue(now)`（或 `e.EvaluateDue`），并把返回的 `Outcome.Ticket` 交给 launch（durable 已自动入队）。
2. `r.eng.Ticket(...)` 的**跨 goroutine 读**改 `TicketSnapshot`（Lead 已通知）。
3. `Report` 之后不再需要 runtime 自己判断结束；`Report(ready)` 会结束 Ticket，runtime 应只负责回复与产物。
4. 旧的 `ContextInput` 字段仍在 domain（Load 会迁移成 Inputs 并清空），不要再写它。
5. `AssessOutput.Strength` 已删除：任何构造/读取它的代码要改。
6. `engine.Policy` 新增 `Rules []domain.Rule`、`Admissions map[string]domain.AdmissionRow`、`Bots map[string]map[string]bool`、
   `Listened map[string]bool`、`RequireApproval bool`；旧 `Allowed/AcceptMode/ReplyMode/AdmitsUnaddressed` 保留。
   `RuleAssessor` 仍可用（测试/demo），但它现在从 `AssessInput.Policy` 取规则。
7. 编译错误示例（当前仓库）：`internal/runtime/worker.go:327` `deliverReply(*fresh)`，若 `ticketSnapshot` 返回
   `domain.Ticket` 值应改成 `deliverReply(fresh)`；这是 runtime 侧一行改动。

## 6. 未完成 / 交给 runtime 的 TODO

- `internal/prompts/defaults.go:74` 仍让模型输出 `strength`（**跨 scope**，engine 不能改）：请删掉该行与 output 模板里的
  `"strength":"explicit|inferred"`；assess 已在系统提示里追加"强度由 gate 决定，你不得声明"。
- `Assessment` 的 `Title/WorktreeName/IntentEvidence` 由 assessor 填；runtime 不需要。
- `LaunchPayload.Priority` 需要 runtime 注册 human kind，否则 human 优先级不生效（编译不受影响）。


## 合并策略语义（2026-11 更新）

全局 Trigger 的合并 Policy 是**保守**的：重叠 admission cell 取更严者（ignore > inferred > explicit）、
accept mode 取更严者（off > observe > auto）、reply mode（notify > assist > auto）、rules 去重且禁用优先、
allowed/bots 取并集；全局 Trigger 在合并策略拒绝时仍会以"任一工作区最严的可准入 strength"进入 Eval，
Ticket 创建前再按目标工作区 re-gate（不会因合并扩大授权）。`AssessInput.Policy` 因此总是非空（Version/Rules）。
