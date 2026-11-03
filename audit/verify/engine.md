# 独立验证：Engine（Wave 2 语义）— verifier

> 角色：独立验证者（fix-agent 兼任）。只读 engine 生产代码；新增 4 个 `*_verify_test.go`，未修改
> `internal/engine` 的任何生产文件或 owner 测试。测试基准：`_ref/docs/002-authorization.md`、
> `_ref/docs/003-execution.md`、`audit/conformance-domain.md`（002/003 矩阵）、`audit/fixes/fix-engine-api.md`。
> 复现命令在 §6。验证时间 2026-11-03 20:16–20:31（Asia/Shanghai）。

## 0. 验证状态（重要）

engine 源码在验证窗口内**仍在被 owner 修改**（`engine.go` 20:28、`evaluate.go`/`receive.go` 20:29、
`lifecycle.go` 20:29、`engine_test.go` 20:30）。本报告结论对应下面这组最终文件哈希（sha256）：

```
e97ba261a63d780c1e952b4a9f9737bacfe922227767594b9755bc04653027ba  internal/engine/engine.go
253e79fb65a412f9d203a2eb4263f30fe57d1d4e28c1c3eece85574998ad9972  internal/engine/evaluate.go
a65891313a49d8f8bc5e4001a93ec0db6be6c36e27aba2d02c496d831e025159  internal/engine/gate.go
19ea03e93c5f028c0319ecd94fad2aeea8da0accd97b41503b5bacea7039ae5b  internal/engine/lifecycle.go
7265dd896f7c4d54bccf24b6314f5c013449028eee7fd1e5aa67b96a064a2a97  internal/engine/receive.go
9c4fc39bf206c144897a96019aefa5817373c555b94b0e8f4711c3d077dd65b1  internal/engine/router.go
```

**结论：5 组检查（Lead 指定的 1–5）全部 PASS。** 验证过程中发现 6 个真实缺陷（§4），其中 5 个
（F1–F5）在验证窗口内已由 owner 修复；F6（global Trigger 的 assessor 看不到 Policy）也在最终哈希中
修复。每个缺陷都留下了独立回归测试（§4 各行的 verify 测试名），owner 也补了对应 `TestRegression*`。

## 1. 新增的独立验证测试

只新增、不改 owner 文件：

| 文件 | 覆盖 |
| --- | --- |
| `internal/engine/gate_verify_test.go` | 检查 1：gate 矩阵、bot 永不 explicit、模型不能声明/升级/越权、merged policy 确定性、global 触发器的 PolicyView |
| `internal/engine/eval_verify_test.go` | 检查 2：Receive 不调模型、EvaluateDue 出 Ticket、锁外调模型、单 Thread 单 Eval、批合并 |
| `internal/engine/lifecycle_verify_test.go` | 检查 3/4：ready/dropped/stop/retry/pause/AnswerAttention、fan-out/ownership/handback/resume/revalidate/context-only |
| `internal/engine/snapshot_verify_test.go` | 检查 5：TicketSnapshot 深拷贝、Feed 不别名、-race 读写 |

注：`internal/engine/verify_wave2_test.go` 是**另一个 verifier** 的文件，不在本报告范围；两套独立测试互不
冲突（双方均通过）。

## 2. 检查结果总表

| # | Lead 检查项 | 判定 | 证据（测试） |
| --- | --- | --- | --- |
| 1 | gate strength 确定性：self/allowed/unlisted/bot × addressed/unaddressed × admission cells × accept modes；模型不能覆盖；bot 永不 explicit | **PASS** | `TestVerifyGateMatrix`（17 子例 × 3 模式 + gate 调用两次同结果）、`TestVerifyBotNeverExplicit`（24 组合循环）、`TestVerifyAssessOutputHasNoStrength`、`TestVerifyModelCannotUpgradeStrength`、`TestVerifyModelCannotWidenWorkspaceAuthorization`、`TestVerifyMergedPolicyIsDeterministic`、`TestVerifyMergedPolicyChangesFinalStrength`、`TestVerifyGlobalTriggerAssessmentSeesPolicy` |
| 2 | Eval 批处理：同 Thread 运行中到达的 Trigger 进下一批；每 Thread 至多一个 Eval；EvaluateDue 产出 Ticket；锁外调模型（blocking assessor + 并发 Receive/UI 读不 deadlock） | **PASS** | `TestVerifyReceiveDoesNotCallModel`、`TestVerifyEvaluateDuePassProducesTickets`、`TestVerifyAssessorRunsOutsideLockAndBatches` |
| 3 | 生命周期：ready 无 follows → completed；dropped 结束；stop 撤 Mandate 且不再接收；retry 解除 blocked/failed；pause 冻结 launch/Eval deadline；AnswerAttention 清 stage 并投递输入 | **PASS** | `TestVerifyReadyAndDroppedEndTheTicket`、`TestVerifyStopWithdrawsMandateAndReceivesNothing`、`TestVerifyRetryLiftsBlockedAndFailed`、`TestVerifyPauseHoldsLaunchAndEvalDeadline`、`TestVerifyAnswerAttentionClearsStageAndDeliversInput` |
| 4 | 订阅/follow-up：fan-out 投递 Input；owned 人消息不进 Eval；handback 仅在无人拥有时重排队；ended-not-stopped resume；revalidate 失败把消息交回 Eval | **PASS** | `TestVerifyFanoutDeliversButGrantsNothing`、`TestVerifyOwnedHumanMessageTakesNoEval`、`TestVerifyHandBackRequeuesOnlyWhenNoOwner`、`TestVerifyEndedNotStoppedResumesWithoutEval`、`TestVerifyRevalidationFailureReturnsMessageToEval`、`TestVerifyContextOnlyFollowDoesNotResumeEndedTicket` |
| 5 | `TicketSnapshot` race-safe（-race with writer） | **PASS** | `TestVerifyTicketSnapshotRaceWithWriter`（4 读 goroutine × 300 轮，writer 做 Receive/EvaluateDue/Report/Input/Attention/Present/Follow）、`TestVerifyTicketSnapshotDeepCopiesNestedState`、`TestVerifyTicketSnapshotCopiesMandateAndEnded`、`TestVerifyFeedDoesNotAliasEngineState` |

## 3. 逐项证据（关键断言）

### 检查 1 — 确定性授权

- 矩阵覆盖：self（含未在名单/未 addressed）→ explicit；local operator → explicit；listed+addressed →
  explicit（off/observe/auto 全模式）；listed follow-up（Thread 已有 Ticket）→ explicit；listed
  unaddressed 按 cell（inferred/ignore）× 模式；unobserved scope 关闭未 addressed 两格；unlisted
  addressed 的 explicit/inferred/ignore 三值 × 三模式；unlisted unaddressed（observed）两值 × 三模式；
  unlisted follow-up 在 off 下 not_admitted；listed bot addressed / receive-all → inferred，off 下拒绝；
  未观察 scope 的 bot、未列名单 bot → not_admitted；bot 声称 `Self` 仍非 explicit。
  同一 policy+trigger 连续两次 `gate()` 得到相同 `Strength/Admitted`（确定性断言）。
- bot 永不 explicit：4 维循环（3 模式 × addressed × receive-all × listed）24 组，全部 `!= explicit`。
- 模型不能覆盖：`reflect` 断言 `AssessOutput`/`AssessInput` **没有 Strength 字段**；assessor 选更宽松的
  ws（explicit cell）无法把 inferred 升级（最终 Mandate 仍 inferred）；assessor 选拒绝该 sender 的 ws
  不能开票（`AllTickets()==0`）。
- merged policy：同一 global Trigger + 相同两个 workspace policy，200 次 `policyForLocked` 结果一致；
  60 个全新 engine 的最终 Mandate.Strength 全部一致（`map[inferred:60]`）；assessor 看到的
  `AssessInput.Policy.Version/Rules` 非空（global Trigger 的 merged policy 已传入判断层）。

### 检查 2 — 两阶段 Eval / 批处理

- `Receive` 后 assessor 调用次数 = 0、Ticket = 0、QueueLen = 1；`EvaluateDue` 后 assessor 调用 = 1、
  Ticket = 1、QueueLen = 0。
- blocking assessor 停留在 `Assess` 内时：另起 goroutine 执行 `AllTickets/Counts/QueueLen/Evals/Feed/
  Decisions/PolicyOf/TicketSnapshot/Receive` 在 2s 内全部返回（证明全局锁未持有）；此时
  `Evals()` 只有 1 个 running Eval，`slot.Pending` 长度为 1（同 Thread 运行中到达的 Trigger 进入下一批）。
- 释放后：第一批 Decision 的 `TriggerIDs` 只含 tr1（运行中的批次不被改写）；第二批 `EvaluateDue`
  产生第二个 Ticket（同 Thread 两个 Trigger 各得到一次判定）。
- 空 pass：没有 due Eval 的 `EvaluateDue` 不产生新 Decision（幂等空转）。
- 2s pass 语义：循环调用 `EvaluateDue` 直至出现 Ticket（不依赖任何内部字段）。

### 检查 3 — Ticket 生命周期

- ready（无 follows）→ `Ended.Outcome == completed` 且 `StageOf == ended`；dropped → `dropped`。
- stop → `Ended.By=="human"`/`Outcome=="stopped"`、`Mandate.Withdrawn != ""`；之后同 Thread 的人消息：
  stopped Ticket 的 `Inputs` 数量不变、不含该 Trigger；消息本身 `DispKind` 不是 resumed/owned，
  QueueLen = 1（按新工作进 Eval）。
- blocked/failed 后 `RetryTicket` → 新 Assignment `pending`、`StageOf == working/queued`；已完成 Ticket
  retry → `ErrNotRetryable`。
- Pause：paused 时 `Receive` 返回 `Held`、`EvaluateDue` 返回空、无 Ticket；Resume 后产生 Ticket。
  运行中的 Eval 在 pause 下完成 → 不落 Decision、requeue 且 `PausedAt` 被设置；Resume 后
  `Deadline == 原 deadline + 暂停时长`（假时钟，精确到 30s）。
- `RaiseAttention` → `person/answer`；`AnswerAttention` → Attention 清空、answer 变成 `InputText`
  （未 delivered/read）；`MarkInputsDelivered/MarkInputsRead` 后两标志置位；stage 不再 person/answer。

### 检查 4 — 订阅 / follow-up

- fan-out：未列名单 bot 的 CI 消息进入 Ticket 的 `Inputs`，但不进 Eval、不产生 Decision。
- owned：live Ticket own Thread 上的人消息 → `DispOwned`、Input 入 Ticket、QueueLen=0、Decision 数不变。
- handback（关键）：同 Thread 两个 live Ticket（第二个由 listed bot 的消息 accept 创建）都拥有该人消息；
  ticket A `HandBack` 后 QueueLen **仍为 0**（B 仍拥有）。
- ended-not-stopped：ready 结束的 Ticket 收到 own Thread 的人消息 → `DispResumed`、不产生 Eval/Decision、
  Ticket 重开且新增 `pending` Assignment。
- revalidation 失败：inferred Mandate + 改成 off 模式后，人消息不能 resume：Ticket 保持 ended、
  `Mandate.Revoked` 非空、QueueLen = 1（消息交回 Eval）。
- context-only：`BindFollow(own=false)` 的 Thread 上人消息不 resume 已 dropped 的 Ticket，且不新增
  Assignment（按新邮件进 Eval）。

### 检查 5 — Snapshot 拷贝与并发

- 深拷贝：reader 修改 snapshot 的 `Attention.Options/Question`、`Inputs[0].Text`、
  `Assignments[0].Report.Summary`、`Delivery.LastError`、`Follows[0].Thread`、
  `Follows[0].ClosedAt`（指针）、`Mandate.Rules`、`Ended.Outcome` 后，重新 `TicketSnapshot` 全部仍是原值。
- Feed：`Feed()` 返回的 Ticket 指针不再指向 engine 内部状态（修改 Feed item 的 Title 不影响 snapshot）。
- -race：writer 300 轮做 `DeliverInput/MarkInputsDelivered/MarkInputsRead/MarkTurnEnded/Report/Present/
  BindFollow/RaiseAttention/AnswerAttention/Receive/EvaluateDue`，4 个 reader 各 300 轮读
  `TicketSnapshot/AllTickets/TicketsOnThread/Decisions/LastTicket/TicketByNumber/Evals/QueueLen/Counts` —
  `CGO_ENABLED=1 go test -race` 绿。

## 4. 验证过程中发现、已修复的缺陷（回归测试保留）

> 发现时 engine 仍在改动；下面给出**发现时**的最小复现与当前处置。所有测试现在均 PASS，作为回归护栏。

| # | 发现时症状（复现） | 影响 | 发现时位置 | 当前处置 |
| --- | --- | --- | --- | --- |
| F1 | `TestVerifyMergedPolicyIsDeterministic`：同一 global Trigger 200 次 `policyForLocked` 得到 `map[explicit:25 inferred:175]`；`TestVerifyMergedPolicyChangesFinalStrength` 同输入 Mandate strength 不稳定 | 多 workspace + global Source 时 gate strength 依赖 Go map 迭代顺序，违反"确定性授权" | `engine.go:policyForLocked`（range `e.policies` 构造 `ps`），`gate.go:mergePolicies`（first-come 合并 admission/accept mode） | **已修**：`policyForLocked` 先 `sort.Strings(names)` 再按 id 顺序合并；回归测试 `TestVerifyMergedPolicyIsDeterministic`、`TestVerifyMergedPolicyChangesFinalStrength` + owner `TestRegressionMergedPolicyDeterministic` |
| F2 | `TestVerifyHandBackRequeuesOnlyWhenNoOwner`：同 Thread 两个 live Ticket 都拥有人消息，A `HandBack` 后 `QueueLen()==1` | 消息在 B 仍拥有时被重复送 Eval → 重复开单风险（002:30-32） | `lifecycle.go:HandBack`（无 owner 复查，直接 `requeueTriggerLocked`） | **已修**：HandBack 收集候选后 `ownedByOtherLocked` 复查，仍被其他 Ticket 拥有则不排队；回归测试 `TestVerifyHandBackRequeuesOnlyWhenNoOwner` + owner `TestRegressionHandBackKeepsEvalWhenAnotherOwner` |
| F3 | `TestVerifyRevalidationFailureReturnsMessageToEval`：inferred Mandate 失效后，触发 resume 的人消息 `QueueLen()==0`（消息丢失） | 002:141 要求"revalidation 失败的消息当作没有到达 Ticket，进 Eval"；实现只把 `InputResumed` 塞回 ended Ticket | `receive.go:resumeEndedLocked`（`!revalidate` 分支没有重排队当前 Trigger） | **已修**：失败分支 `enqueueEvalLocked(t, p, tk.Workspace, ...)`；回归测试 `TestVerifyRevalidationFailureReturnsMessageToEval` + owner `TestRegressionRevalidationFailureQueuesEval` |
| F4 | `TestVerifyTicketSnapshotDeepCopiesNestedState`：修改 snapshot 的 `Follows[0].ClosedAt` 指针后，engine 里该关闭时间被改写 | `copyTicket` 声称深拷贝但 `Follow.ClosedAt *time.Time` 共享指针，面板读取可改 engine 事实 | `engine.go:copyTicket`（`out.Follows = append([]domain.Follow(nil), t.Follows...)`） | **已修**：逐个复制 `ClosedAt`；回归测试 `TestVerifyTicketSnapshotDeepCopiesNestedState` + owner `TestRegressionSnapshotCopiesFollowCloseTime` |
| F5 | `TestVerifyFeedDoesNotAliasEngineState`：`Feed()` 返回的 `*domain.Ticket` 指向 engine 内部状态，reader 修改可见 | 面板 goroutine 读 Feed 与 writer 并发 = 数据竞争（与 `TicketSnapshot` 的安全承诺不一致） | `engine.go:Feed`（`it.Ticket = tk`） | **已修**：`cp := copyTicket(tk); it.Ticket = &cp`；回归测试 `TestVerifyFeedDoesNotAliasEngineState` + owner `TestRegressionFeedDoesNotAlias` |
| F6 | `TestVerifyGlobalTriggerAssessmentSeesPolicy`：global Trigger 的 `AssessInput.Policy.Version/Rules` 为空 | global Source 的判断层看不到 Policy（assess 有 cfg fallback 掩盖，但 Version/Admissions/Bots 缺失） | `evaluate.go:assessInputLocked`（`e.policies[ev.Workspace]`，global Eval 的 `Workspace==""`） | **已修**：缺失时回退 `policyForLocked(primary)`；回归测试 `TestVerifyGlobalTriggerAssessmentSeesPolicy` |

## 5. 残留风险 / 非阻塞观察（不改变 PASS 判定）

1. **merged policy 的合并优先级是"按 workspace id 字典序 first-come"，不是语义上的"取更严"**。
   现在确定（F1 已修），但若按字典序靠前的 workspace 配了宽松 cell，merged 初始 strength 可能偏高；
   `applyEvalLocked` 会用目标 workspace 再次 gate 并只降不升，因此不会越权（`TestVerifyModelCannotUpgradeStrength`
   验证）。建议 owner 明确写成"保守合并（off/ignore > inferred > explicit）"或注释说明字典序契约。
2. `Engine.Ticket()` 仍返回内部指针（兼容 API，注释已写"跨 goroutine 用 TicketSnapshot"）；
   runtime/UI 必须用拷贝 API。验证覆盖的拷贝 API 均安全；`Feed` 已改为拷贝。
3. `EvaluateDue` 在一次 pass 内**串行**判断所有 due Thread：一个慢 assessor 会推迟同 pass 的其它 Thread
   （最坏 N×90s）。这是 runtime 2s pass 的延迟风险而非正确性问题；建议 Wave 2 runtime 限制单 pass 时间或
   并发判断。
4. 未在 Lead 本轮清单内、未独立覆盖：002:147-151 的三次 timeout→unavailable 细节（owner `TestEvalExpiry...`
   覆盖）、policy-change 与在跑判断的竞态、delivered/read/turnEnded 的全部组合、`persist.go` 的 durable 包装
   （owner `persist_test.go` 覆盖）。这些留给后续 verifier 轮次。

## 6. 复现命令与结果

```sh
export PATH=/usr/local/go/bin:$PATH
cd /data/ZeoKeel-Super-Agent

# 1) 独立验证测试（26 个顶层 Test 全 PASS）
CGO_ENABLED=0 go test ./internal/engine/ -run 'TestVerify' -count=1 -v
#   ok  github.com/zeokeel/super-agent/internal/engine  0.178s

# 2) 全 engine 包（owner 测试 + 独立验证）
CGO_ENABLED=0 go test ./internal/engine/ -count=1
#   ok  github.com/zeokeel/super-agent/internal/engine  0.336s

# 3) 只跑 owner 测试（证明验证文件未破坏基线）
CGO_ENABLED=0 go test ./internal/engine/ -skip 'TestVerify' -count=1
#   ok  github.com/zeokeel/super-agent/internal/engine  0.142s

# 4) 竞态（含 TicketSnapshot/Feed 并发验证）
CGO_ENABLED=1 go test -race ./internal/engine/ -count=1
#   ok  github.com/zeokeel/super-agent/internal/engine  3.806s
```

## 7. 限制

- 未接真实模型：所有判断层用确定性/可编程 assessor；`assess` 与 provider 的 live 行为不在本报告。
- `-race` 需要 `CGO_ENABLED=1`（CI 例外步骤），产品构建仍 `CGO_ENABLED=0`。
- 验证窗口内 engine 源码被 owner 持续修改；结论只对 §0 的哈希成立。复跑前先核对哈希/时间。
- 本报告不覆盖 runtime/UI 对 engine 的调用是否遵守"跨 goroutine 用拷贝 API"（Wave 2 runtime 自己的验证）。

## 8. 增量（2026-11-03 20:55 后）：verifier-B 指出的 fixture 问题

verifier-B 指出 `internal/engine/gate_verify_test.go` 有一个名为 unobserved 的用例其 policy row
`Observed=true`（他们看到的行号窗口约 209–212）。复核当前文件：

- 两个 unobserved 用例（human "unobserved scope loses the unaddressed cells"、bot "listed bot in an
  unobserved scope is not admitted"）**都已经** `Observed=false`（此前的 inline 副本问题已在本轮修复）。
- 为防回归，抽出 `vfUnobservedPolicy(mode)` helper（row `Observed=false`），两个用例改用它；
  新增 `TestVerifyUnobservedScopeFixture`：断言 helper 的 row `Observed==false`，且 unobserved scope 下
  listed-unaddressed 人消息与 listed-unaddressed bot 都 not_admitted（3 个 accept mode 各跑）。
- 产品行为未被修改（fixture 修正 + 守护测试）；`TestVerifyGateMatrix` 中该用例现在因为正确的
  `Observed=false` 而通过。

复跑（2026-11-03 20:57）：

```sh
CGO_ENABLED=0 go test ./internal/engine/ -run 'TestVerify' -count=1   # ok（27 个顶层验证测试）
CGO_ENABLED=0 go test ./internal/engine/ -count=1                      # ok
CGO_ENABLED=1 go test -race ./internal/engine/ -count=1                # ok 4.188s
```

结论不变：§2 检查 1–5 全部 PASS。
