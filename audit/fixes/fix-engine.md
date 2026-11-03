# fix-engine handoff (Wave 2)

## 完成的条目（对应 wave2-spec §2 / conformance-domain）

- **Stage 1 确定性授权（002）**：`internal/engine/gate.go` 新增 `Policy{Rules,Admissions,Bots,Listened,RequireApproval}`、
  `domain.Strength/AdmissionCell/AdmissionRow/Rule/RuleHit/Basis`；gate 确定性产出 `explicit|inferred|not_admitted`，
  按 002:11-16/50-54/63-79 的两条路径（self/operator、allowed+addressed、admission 三格、bot list、follow-up 强度、
  direct-message 例外、observed 限制）；模型不再声明 strength（`AssessOutput.Strength` 删除，assess 提示词追加
  "强度由 gate 决定"）。accept mode 只作用于 inferred；Mandate 记录 `Strength/Basis{via,source,row,cell}/Rules/Scope/PolicyVersion`。
- **Stage 2 两阶段 Eval（001:29,33 + design §2.1）**：`Receive` 只落 trigger/扇出/gate/ownership 并排队；
  `EvaluateDue(now)` 锁外调 `Assessor.Assess`、锁内 apply；每 Thread 至多一个 Eval，运行期到达的 Trigger 进 `pending`
  成为下一批（`evalSlot.Pending`）；Eval 有 `Deadline/Attempts/CandidateDelivered/PausedAt`；评估错误/第三次过期记
  `unavailable`；`PolicyChanged` 丢弃重排；pause 顺延 deadline。`AssessInput` 增加 `Batch/Policy(ThreadSnapshot/
  Tickets(含 ended)/Decisions`。
- **Stage 3 Ticket 生命周期（003:92-106）**：`Ticket.Inputs/Delivery/Follows/FollowupOwners/DecisionIDs`；
  `StageOf` 严格 003 表（delivery 可达、blocked/failed 需 `WaitsForPerson`、idle）；`Report(ready)` 无 follows →
  `completed`，有 follows 等 close 取最后 close outcome；`dropped` 结束；`StopTicket` 撤 Mandate；`RetryTicket`
  （`CanRetry`）；`Pause/Resume/Paused`；`AnswerAttention`；`letGo`（结束释放未读人消息回 Eval）；`TicketSnapshot`
  深拷贝 + race 测试。
- **Stage 4 订阅/follow-up/revalidate（001:37、002:20-42,127-141）**：fan-out 投递 `Input`（context 标记、来源无关）；
  live Ticket 拥有的 Thread 上人消息 → `DispOwned`、不进 Eval；ended-not-stopped → `revalidate` 通过则重开（无 Eval/
  Decision），失败则 `Mandate.Revoked` 且消息回落（等待消息回 Eval）；`HandBack`；按 basis 的复核（follow-up/admission/
  bot/listed/operator、cell 强度、规则仍启用、accept mode）。
- **Stage 5 持久化**：`Persistence` 新增同名包装（EvaluateDue/AnswerAttention/StopTicket/RetryTicket/Pause/Resume/
  Paused/DeliverInput/MarkInputsDelivered/MarkInputsRead/MarkTurnEnded/DeliveryFailed/BindFollow/ThreadClosed/HandBack/
  RevalidateAndResume/PolicyChanged/SetPolicy）；Eval 队列以 kv JSON（`engine.evals`）随命令事务落库（无需 store 改表）；
  新 Decision 按 disposition/outcome 保存；旧 `ContextInput` 在 Load 迁移成 Inputs 并清空；`accepted` 映射 Load 重建。
  **human slot**：`LaunchPayload.Priority` + 新 kind `EffectLaunchHuman`，Persistence 按来源 Trigger 的 Human/Self 选择。

## 改动文件（全部在本 scope）

- `internal/domain/ticket.go`（重写：Strength/Basis/Admission/Rule/Input/Delivery/Follow/Eval/StageOf 等）
- `internal/domain/trigger.go`（Trigger.Row/ScopeReceivesAll/OwnThread；Disposition strength/basis/eval/decision；新增
  DispOwned/DispResumed/DispHeld）
- `internal/domain/domain_test.go`（重写：stage golden 12 行、canRetry、followsDone、letGo、三段投递）
- `internal/engine/gate.go`（新）
- `internal/engine/receive.go`（新）
- `internal/engine/evaluate.go`（新）
- `internal/engine/lifecycle.go`（新）
- `internal/engine/engine.go`（重写：状态、读 API、`TicketSnapshot`、feed、dispatch）
- `internal/engine/router.go`（重写：Assessor/AssessInput/AssessOutput/PolicyView/ThreadSnapshot/RuleAssessor）
- `internal/engine/persist.go`（扩展：Eval kv、生命周期包装、launch 优先级、Load 迁移）
- `internal/engine/engine_test.go` / `persist_test.go`（重写：gate 8 例、admission 7 格、bots、accept modes、
  串行/合并、超时 unavailable、policy change、lifecycle 8、ownership/resume、pause、snapshot race、持久化 3）
- `internal/assess/assess.go`、`prompt.go`、`assess_test.go`、`e2e_test.go`（去掉 strength；规则来自 `in.Policy`；
  提示词含 Thread/Tickets；e2e 走两阶段）

## 新增/修改的测试与结果

```
CGO_ENABLED=0 go test ./internal/engine/ ./internal/domain/ ./internal/assess/ -count=1
ok  engine 0.086s / domain 0.003s / assess 0.012s
CGO_ENABLED=1 go test -race ./internal/engine/ -count=1   → ok 1.979s
CGO_ENABLED=0 go vet ./internal/engine ./internal/domain ./internal/assess → clean
gofmt -l internal/{engine,domain,assess} → 空
```

## 行为变化与兼容性风险（runtime/UI 必读）

1. `Receive` 不再调模型、不再立即建 Ticket：必须每 2s 调 `EvaluateDue`（durable 用 `Persistence.EvaluateDue`）。
2. `Ticket()` 仍返回内部指针（兼容，但跨 goroutine 必须 `TicketSnapshot`）；`TicketByNumber/LastTicket` 已返回拷贝。
3. `Report(ready)` 现在会结束 Ticket（003:94）——runtime 不再需要自己判断结束。
4. `AssessOutput.Strength` 删除；`Policy` 新增字段；`RuleAssessor` 仍可用（从 `AssessInput.Policy` 取规则）。
5. 旧 `Ticket.ContextInput` 保留但 Load 会迁移并清空，不要写。
6. `LaunchPayload.Priority` + `EffectLaunchHuman`：runtime 需注册两个 effect kind（human limit 1）。
7. 提案/审批改为 opt-in（`Policy.RequireApproval` 默认 false = 原型路径）：默认同事 accept 由 Eval 直接建 Ticket。
8. 仓库当前 `go build ./...` 失败在 `cmd/zeokeel`（`runCLI/setupLogging/serveHeadless` 未定义）——是 runtime 的
   Phase B 在写，不是 engine；`internal/runtime` 的 `worker.go:327 deliverReply(*fresh)` 也需 runtime 改成 `fresh`。

## 诚实缺口清单（实现语义 vs 仅未测试）

- **实现且测试**：以上 Stage 1-5 全部主路径；缺口如下仅列**未实现**或**只做到契约点**的：
  1. **未实现**：Source 的 `Scope()` 结果（Observed/Row、ScopeReceivesAll）目前只有 GitHub Source 计算，尚未传入
     `domain.Trigger.Row/ScopeReceivesAll`（integrations scope 接线；engine 已支持字段与表）。
  2. **已实现**（本轮补上）：`DropSubscription`（人可丢订阅）、`MarkUnrelated`（executor 丢自己的订阅并交回，
     源线程/own PR 线程拒绝）、`HandBack`；均有测试 `TestUnsubscribeAndHandBack`。
  3. **未实现（runtime）**：投递三段式的 worker 侧调用（engine API 已就绪）、`person/delivery` 2s→1min 退避、
     Assignment 崩溃恢复、human slot 的第二 effect kind 注册。
  4. **部分实现**：Eval 的"第三次过期→unavailable"只在**重启/崩溃后 queued 且 deadline 已过**的路径可达；
     进程内 assessor 调用有 90s ctx 超时，失败直接记 unavailable（002:151 的 assessment failure 语义）。
     即：hung 的在途模型调用不会走 3 次重试；这对原型是"expired candidates 重排队"，当前靠 assessor 超时+重启恢复。
  5. **部分实现**：`Policy.Listened` 的来源（Source 是否仍监听）无运行时写入方；revalidate 的 listen 检查在未设置时视为监听。
  6. **部分实现**：`EvaluateDue` 逐 Thread 顺序判 Eval（无并行 assess）；模型调用已不在锁内，但一次 pass 的多个 Eval 串行。
  7. **未实现**：policy-changed Input 的三种解释文本（只写 "Policy changed from X to Y"）；受影响 Ticket 判定按
     `Mandate.PolicyVersion==from`（未按 rule/mode 分类）。
  8. **未实现**：`Ticket.DecisionIDs` 只在新 Ticket 记录；Thread 决策历史在 `thread.decisionIDs`，未回填 Ticket。
  9. **跨 scope 待办**：`internal/prompts/defaults.go:74` 与 output 模板仍让模型输出 `strength`，需删。
  10. **未实现**：`RuleAssessor` 的 `Rules` 命中是"全部命中"，仅用于测试/demo（真实模型走 assess 的严格校验）。
 11. **未实现**：Eval 队列用 kv JSON（无 `eval` 表）；若 Lead 要求 schema 表，SQL 已在 `audit/fixes/fix-engine-api.md` 说明。
 12. **未实现**：`RequireApproval` 的 UI/命令路径未接（engine 保留 Approve/Reject）——默认关闭。

## 跨 scope 待办（file:line + 期望改动）

1. `internal/prompts/defaults.go:74`、`:84-88`：删 `strength` 指令与 JSON 字段（否则模型仍输出被忽略的字段）。
2. `internal/runtime/worker.go:327`：`deliverReply(*fresh)` → `deliverReply(fresh)`（TicketSnapshot 返回值拷贝）。
3. `internal/runtime/*`：2s pass 调 `Persistence.EvaluateDue`；注册 `EffectLaunchHuman`；worker inbox 三段式；
   `Ticket()` 跨 goroutine 读取改 `TicketSnapshot`。
4. `internal/sources/github/scope.go:31-38`：把 `Row/Observed` 写入 `domain.Trigger.Row/ScopeReceivesAll`；
   IM 同（engine 已有字段）。
5. `cmd/zeokeel`：`Dispatch` 回来的 Outcome 不再有 Ticket（异步），需要 pass 后从面板/Feed 看结果。


## Wave 2 verifier 修复（第二轮）

验证者 5 个缺陷已修，全部有生产代码修改 + 自有回归测试：
1. `HandBack` 只在**无其他 owner** 时把消息送回 Eval（新增 `ownedByOtherLocked`；并排除 Ticket 的 origin Trigger：
   handback 不释放创建该 Ticket 的那条消息）。
2. revalidation 失败：`resumeEndedLocked` 显式 `enqueueEvalLocked`（002:141），返回 `DispEvaluating` 而非 `DispResumed`；
   消息不会丢。
3. `copyTicket` 深拷贝 `Follow.ClosedAt *time.Time`（以及 Assignments/Report/Inputs/Attention/Delivery/Ended/
   Mandate.Rules）；`TicketSnapshot` 不再与引擎共享嵌套指针。
4. `Feed` 返回拷贝：`FeedItem.Ticket` 是 `copyTicket` 的新指针，`Disposition`/`Trigger.Threads` 也复制。
5. merged policy 确定性：`policyForLocked` 先按 workspace id 排序再合并（此前依赖 map 迭代顺序）。
另：`InterruptAssignment(ticketID)`（engine + Persistence）——运行中 Assignment 标 `hostRestarted` 结束、追加
pending Assignment（沿用 SessionID）、入队 `Input{Kind:note}` recovery note（003:146）；已通知 nih-runtime。

回归测试（新增）：TestRegressionHandBackKeepsEvalWhenAnotherOwner、TestRegressionRevalidationFailureQueuesEval、
TestRegressionSnapshotCopiesFollowCloseTime、TestRegressionFeedDoesNotAlias、TestRegressionMergedPolicyDeterministic、
TestInterruptAssignment。另修掉 verifier 也覆盖的全局 Trigger 的 PolicyView 为空（assessInput 用 merged policy）。

验证：`CGO_ENABLED=0 go test ./internal/engine ./internal/domain ./internal/assess -count=1` ok；
`CGO_ENABLED=1 go test -race ./internal/engine` ok；`go vet` clean；`gofmt -l` 空；`CGO_ENABLED=0 go build ./...` 全绿。


## 合并策略：保守优先（本轮）

`mergePolicies` 不再受注册顺序/ID 字典序影响地"取第一个"，而是**更严格者胜**（002：合并不能扩大授权）：
- 重叠的 admission cell：ignore > inferred > explicit；任一工作区把该行视为 not-observed，则未 @ 两格保持关闭；
- accept mode：off > observe > auto；reply mode：notify > assist > auto；
- allowed/bots：并集（按工作区 re-gate 收窄最终授权）；rules：按 ID 去重并集，任一工作区禁用即视为禁用；
- 全局 Trigger（无 DefaultWorkspace）：合并策略可能比某个工作区更严而拒绝，但 Receive 会用"任一工作区准入中最严的
  strength"继续排队 Eval（assessor 判语义；最终 Ticket 仍由目标工作区 re-gate 收紧），既不扩大授权也不丢请求。
测试：`TestConservativeMergeRegardlessOfOrder`（两个注册顺序 + accept mode off 胜 auto）；verifier 的
merged-policy 确定性/最终强度测试仍绿。


## 只读 API 全面深拷贝（docs/verifier round）

- `AllTickets()`（经 `ticketSliceLocked`）现在返回 `copyTicket` 深拷贝：嵌套 `*Report`、`Assignment.EndedAt`、
  `*Attention`、`*Delivery`、`Follows[].ClosedAt`、`Mandate.Rules`、Inputs 均不再与引擎共享 —— 修复
  `runtime/pages.go` 读 Ticket 报告与 engine 写入之间的真实 `-race`。
- 同类读者一并深拷贝：`TicketSnapshot`（既有）、`TicketsOnThread`（既有 copy）、`Decisions`/`Decision`（新增
  `copyDecision`：Reasons.Values、TriggerIDs、RulesHit、Mandate+Rifts、Reply.Reason、Proposal）、`Disposition`
  （`copyDisposition`）、`Trigger`/`TriggerOf`（`copyTrigger`：Threads、Channel.Address）、`Evals`（TriggerIDs）、
  `Feed`（Trigger+Disposition+Decision 全深拷贝）、`Proposals`、`Workspaces`/`Workspace`/`PolicyOf`（brief 切片、
  Policy 的 Allowed/Bots/Admissions/Listened/Rules map 均复制）。
- 测试：`TestAllTicketsDeepCopyUnderRace`（写 Report 与读 AllTickets 并改嵌套字段并发，`-race` 下通过）、
  `TestReaderAPIsDoNotAlias`（TicketsOnThread/Decisions/Evals/Disposition/PolicyOf 逐项验证不回写引擎）。
- 验证：engine/domain/assess tests ok；`CGO_ENABLED=1 go test -race ./internal/engine` ok；vet/fmt clean；
  `CGO_ENABLED=0 go build ./...` 绿；`go vet ./internal/runtime` 通过（API 不变）。

- `Engine.Ticket(id) (*domain.Ticket, bool)` 已删除（全仓零调用方，且返回内部指针是潜在 race 风险）；
  跨 goroutine/面板读取一律走 `TicketSnapshot`（深拷贝）。engine 测试与 `go build ./...` 均绿。


## 结构小修（domain + engine 同源正确性问题）

1. **`WorkspaceScope("")` = Global**（不再产生畸形 `"ws:"`）：`Scope.WorkspaceIDOf` 额外校验 id 非空/无空白/无分隔符，
   裸 `ws:`、`ws:|…` 一律不算工作区；`Key(WorkspaceScope(""), …)` 与 `Key(Global, …)` 完全一致。
   `cmd` 里 IM Trigger 用 `WorkspaceScope("")` 的地方因此归到 Global（符合设计 §4.7；无 cmd 改动，构建绿）。
2. **`domain.NewWorkspaceID()`**：`crypto/rand` 6 字节 → `ws_` + 12 hex；`WorkspaceID.Valid()` 校验；测试 200 个唯一、
   前缀/长度、scope round-trip；entropy 不可能时才 panic（不静默产生重复 id）。
3. **operator/self 语义**（`internal/domain/actors.go`）：`OperatorActorID`、`Trigger.IsOperatorMessage()`/`CreatedByMe()`
   = `Self` 或 actor==operator 或（local 且无显式 actor）；`Mandate.IsOperatorGrant()` / `Ticket.CreatedByOperator()`
   读 Mandate（授权事实），不被后续同事消息改写。测试覆盖 7 种来源组合。
4. **事件时间**：`MinEventTime`（2000-01-01）、`Trigger.EnsureAt(now)`（零值/2000 前/未来 24h+ → now）、
   `Trigger.Validate()`（id/source/时间/Threads/human actor）。engine `Receive` 改用 `EnsureAt`（同源正确性，
   防止 IM 1970 事件污染排序与 Eval deadline）；engine 测试 `TestReceiveNormalizesEpochTimestamps`。
验证：domain/engine/assess 测试绿；`go build ./...` 绿；`gofmt` 干净。
（第 5 项独立验证 `audit/verify/design-fix-verify.md` 待 Lead 通知后写。）


## 产品规则变更：入站 IM 一律待批（supersedes auto-processing）

- `internal/engine/gate.go`：`imSources = {feishu, dingtalk}`、`isIMSource`、`Policy.AutoApproveIM`（默认 false，
  仅未来显式 opt-out 用）。这是代码里的发布策略，不随工作区配置静默失效。
- `internal/engine/evaluate.go`：Eval 本会 `DecAccept` 的 IM 消息一律转 `DecPropose`（**不看 strength / accept mode /
  RequireApproval**）；批内**每条消息各自一个** proposal（`proposeIMLocked`，Decision ID = key(decision, evalID, triggerID)），
  不建 Ticket、不 launch Worker、不发 IM 回复（`Reply.Route=none`），只进待办。`Approve(decisionID)` 是唯一建 Ticket 的路径；
  Reject/ignore 什么都不留。Decision 的 reason = `im_approval_required`，disposition 仍记录 gate 的 strength/basis（审计）。
- 非 IM（GitHub、local 面板、schedule、测试）行为不变（仍按 gate + accept mode）。
- 持久化：`Persistence.EvaluateDue` 会保存批内每条 disposition 的 Decision；`Load` 从 `Decision.TriggerIDs` 重建
  Decision→Trigger 边，使重启后 `Approve` 仍可用（新增 `TestIMProposalsSurviveRestart`）。
- 测试：`TestIMExplicitBecomesProposal`、`TestIMApproveCreatesTicketRejectCreatesNothing`、
  `TestNonIMExplicitStillAccepts`、`TestIMMessagesAreSeparateProposals`、`TestIMStrangerIsNotProposed`、
  `TestIMProposalsSurviveRestart`。

**跨 scope 影响**：`cmd/zeokeel/acceptance_test.go` 的 A3/A7/Overview 三个用例仍断言"操作员自己的 IM 消息直接开单"，
这是被本次产品规则取代的旧行为，需要 runtime/Lead 更新（engine 不编辑 cmd）。


## Undo reject（rc2，撤销已拒绝的请求）

- API：`Engine.UndoReject(decisionID domain.ID) (Outcome, error)`（别名 `RestoreProposal`）；`Persistence.UndoReject`（同签名，单事务持久化）。
- 表示（最不侵入）：`Reject` 不再清空 `Decision.Proposal`，只把 Kind 置 ignore、Reasons=`rejected_by_person`、
  记录 `Decision.RejectedAt`；因此 Undo 不需要重跑 Eval、不丢提案内容。JSON 兼容（新字段 omitempty）。
- 语义：只接受"经 Reject 的 ignore"（pending 提案→幂等成功；accept 或已建 Ticket→error；其余→`ErrNotRejected`）；
  同 Thread 上出现 `At/RejectedAt` 晚于 `RejectedAt` 的更新 Decision → `ErrUndoSuperseded`；Thread 为空则不设上限。
  恢复后 Kind=propose、追加 reason `undoReject`（审计）、**不建 Ticket/不 launch/不回复**；`Approve` 仍是唯一建单路径。
- 测试：`TestUndoRejectRestoresProposal`、`TestUndoRejectIsIdempotentAndAudited`、`TestUndoRejectSupersededByNewerDecision`、
  `TestUndoRejectRejectsNonRejection`、`TestUndoRejectSurvivesRestart`（拒绝→重启→undo→再重启→proposal 仍在→Approve 建单）。
- 验证：engine/domain/assess 测试 + engine `-race` 绿；`go build ./...` 绿；runtime/ui/cmd 测试绿（runtime 首次全量有一次
  已知时序 flake，重跑绿）。
