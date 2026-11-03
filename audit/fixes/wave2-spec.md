# Wave 2 规格：语义补齐（Lead 决策版）

> 前置：Wave 1 已完成并全量构建通过。本文件是 fix-engine / fix-runtime / fix-ui2 / fix-docs-final 的共同目标；与 `audit/fix-contract.md` 冲突时以本文件为准（Lead 更新）。
> 原则：**能实现的行为必须实现；只有平台级取舍（单机桌面、无跨设备/多租户/A2A）允许 documented-deviation，且必须在文档中明写。**

## 1. 目标（对应审计条目）

| 审计条目 | 目标行为 | 判定 |
| --- | --- | --- |
| 002 确定性授权 | gate 由代码计算 `strength ∈ {explicit,inferred,not_admitted}`；模型只做语义判断；admission table（按 source+scope 三 cell）+ bot list + accept mode + Mandate revalidate | 必须实现 |
| 001 Eval/Thread | 每 Thread 同时最多一个 Eval；期间 Trigger 合并进下一批；Eval 有 deadline/最多 3 次超时→unavailable；policy 变更丢弃/重排队 | 必须实现 |
| 003 Ticket 生命周期 | ready 无 follows → completed；有 follows 等 PR 关闭按 merged/closed；人 stop/retry/pause/resume；canRetry；人的消息 +1 slot | 必须实现 |
| 001/002 订阅与 follow-up | fan-out 投递 Input；人的消息到已订阅 Ticket 不再进 Eval；Executor 拥有/交回/letGo；ended 未 stop 可 resume | 必须实现（mid-turn 见 §5 决策） |
| Attention | 结构化提问可回答/清除；blocked/failed 由人的消息释放；`person/delivery` 可达 | 必须实现 |
| 003 投递三段式 | delivered（交给 worker）/read（进入模型历史）/turnEnded（回合结束） | 必须实现 |
| 003 后台/并发 | worker slot、人的消息额外 slot、5s 前台转后台、30 分钟规则、handoff | 最小真实实现（eino backgroundtask；细节见 §5） |
| 文章 Router 层 | 全局上下文/记忆（持久化 router state）、批处理、统一回复、轻量模型 | 实现"逻辑 Router"：持久化状态 + 批处理 + 统一 reply composer；**不要求**单个常驻 LLM Session（文章允许 Router 内部非单一 Session） |
| 文章订阅直通 | Thread 后续事件直达订阅 Worker、可退订/交回 | 必须实现（允许"下一次 run 前投递"作为无 steer 时的降级，见 §5） |
| 文章全双工 | start/stop jobs、steer/inject、后台结果事件 | 用 eino `adk/backgroundtask` + `adk.TurnLoop` 实现；若 TurnLoop 集成失败，保留 jobs+后台结果+下轮投递，文档写清 |
| 004 提示词 | 团队三段、逐段覆盖、版本历史、base-hash+Default updated、占位符/长度校验、热更新 | 必须实现（版本历史用 store 新表，Wave1 未加则由 fix-engine 加） |
| 005 面板 | 诚实状态（计数=len(rows)）、Ticket 详情、回答/重试/停止动作、设置 Sources/Eval/Execution/Prompts/Deployment、glossary、深浅色 | 必须实现 |
| 006 部署 | status 0/1/2/3、slog 文件日志、setup checks、备份/归档/VACUUM、进程内 watchdog、systemd/launchd 外部 watchdog 文档 | 必须实现（管理 HTTP API 不做，桌面无 HTTP 是 ADR 决策，写 documented-deviation） |
| 007/008 | 入口统一持久化、游标持久化、schema 完整（trigger_thread/decision.thread/outbox.dropped/assignment）、扩展路径可执行 | 必须实现 |
| 文章 §5 存算/环境分离 | 单机单二进制 | documented-deviation（ADR-1/10；丢失跨设备/休眠/离线补偿，写清） |
| 文章 §8 A2A/多租户/安全 | 不在范围 | NOT-REQUIRED（文章明列开放问题） |

## 2. Engine 重构（fix-engine）

### 2.1 两阶段 Eval（消除"持全局锁调模型"）

```
Receive(trigger):
  lock; store trigger; fanout→Input; gate→strength; if not admitted: disposition filtered; if fanout: return
      if trigger is human message on a Thread with a live owner Ticket: deliver Input to owner(s); no Eval; return
      else: enqueue Eval batch on primary Thread (merge pending); unlock
  // 不调用模型
EvaluateDue(now):            // runtime 每 2s 调用，或 Receive 后唤醒
  lock; for each due Eval: mark evaluating; unlock; out := assessor.Assess(batch); lock; applyDecision; persist via callback
```

- `Engine` 保持无 I/O；持久化由 `Persistence` 包裹：`Persistence.Receive` 写入 trigger/disposition/eval 行 + launch effect；`EvaluateDue` 结果事务写入。
- 批量：同一 Thread 的 pending Triggers 在下一次 Eval 一起给出；`AssessInput` 增加 `Thread` 快照（全部 Ticket，含 ended）、`Policy`（版本/规则/admission/bots）、历史 Decision。
- 模型输出不再含 strength；`AssessOutput` 保留 kind/workspace/rules/intent/ask；strength 由 gate 决定。
- Eval 字段：ID/thread/triggerIDs/primary/strength/actor/basis/policyVersion/startedAt/deadline/attempts/pausedAt；`candidateDelivered` 计时；3 次超时→unavailable 并记录；未投递不过期；pause 期间不超时。
- `PolicyChanged(from,to)`：丢弃 running Eval 重排队；给受影响 Ticket 发 `Input{Kind:"policy"}`；不撤销 Mandate。

### 2.2 确定性 gate + admission

- `Trigger` 增加 `Row string`（接收范围行：github:issue|pull|comment|ci；feishu:<chatID>|dm；local:panel；schedule）。
- gate 顺序（与 002 一致）：
  1. bot：在 bot list 且（addressed 或 scope 收全部）→ inferred；否则 not_admitted。
  2. 人：allowed+addressed → explicit；allowed+unaddressed → 按 cell（默认 inferred/ignore）。
  3. 不在名单：addressed → cell 第二列（explicit/inferred/ignore，默认 ignore）；unaddressed → 第三列（默认 ignore）。
  4. self（operator 本人账号）→ explicit（记录）；local Source → explicit。
  5. 已订阅 Thread 上的人消息若不被 owner 拥有 → follow-up 规则（默认 inferred，cell 可为 explicit）。
- `Mandate` 记录 `Basis{via: addressed|followup|admission|listed|bot}`、`Rules []string`、`Scope`、`PolicyVersion`、`Actor`、`Workspace`。
- `RevalidateAndResume`：按 002 的规则逐条检查；失败 → 保持 ended + 消息回 Eval；成功 → 新 Assignment + 队首 `resumed` Input（无 Eval/Decision）。
- `Policy` 增加 `Rules []domain.Rule{ID,Instruction,Enabled,Proactive,Requirements}`、`Admissions map[string]domain.AdmissionRow`、`Bots map[string]map[string]bool`。持久化 JSON 兼容旧值。

### 2.3 Ticket 生命周期 / Inputs / Follows / Attention

- `Ticket.Inputs []Input`（kind: trigger|note|policy|resumed|text；Delivered/Read/At）。
- `Ticket.Follows []Follow`（thread, own, closedAt, outcome）；`BindFollow`/`ThreadClosed`；ready 无 follows → ended completed；有 follows 等 closes；dropped 立即 ended；stop → ended stopped + 撤 Mandate；retry/pause/resume。
- `Ticket.Delivery *Delivery{Attempts,LastError,At}`：投递失败进入 person/delivery，2s→1min 退避，成功/新 Assignment/结束清除。
- `StageOf` 严格逐行 golden 测试（含 delivery、waitsForPerson）。
- `AnswerAttention(ticketID, answer)` 清除 Attention + 生成 `Input{Kind:"text",Read:true}` 交给 Executor？按原型：回答是给 Ticket 的输入；实现为 Input + 启动/唤醒 worker。
- `StopTicket`（人 stop 撤 Mandate）、`RetryTicket`（canRetry 时新 Assignment）、`Pause/Resume`（命令层开关，Eval 不超时、launch 等待）。
- 人 stop 的 Trigger 不自动重评；letGo 规则逐条实现。

### 2.4 Human slot

- `Engine`/`Persistence` 暴露 `LaunchEffect{Priority:"human"|"normal"}`；runtime 注册两个 effect kind（launch 与 launch-human，limit=1 额外）；人的消息/status 请求用 human 优先级，其他 Input 用 normal。

## 3. Runtime（fix-runtime）

- **Worker inbox**：`runTicket` 前取 `Ticket.Inputs` 未读（按 At/ID 序）+ 原始 task；启动前 `MarkInputsDelivered`；进入模型前 `MarkInputsRead`；run 结束/Report 后 `turnEnded`；若仍有未读/未投递 Input，再起一轮（最多连续 N 轮、间隔退避）；投递失败写 Delivery。
- **Assignment 恢复**：启动扫描 `running` Assignment → 标记 interrupted/ended + 新 Assignment（recovery note），oldest first；`agent.RunSession` 在已有事件时走恢复（fix-agent 提供）。
- **入口统一**：`startGitHub`/`startSchedule`/IM/`Dispatch`/`Approve` 一律通过 `Persistence` 方法；游标写 `kv`（按 source 实例）；sender 无 driver → 返回错误让 Outbox 退避（不得 OutcomeSent）。
- **Router**：新增 `internal/runtime/router.go`：
  - 持久化 `routerState{recentDecisions, lastPolicyVersion, workspaceBriefDigests, lastActiveAt}` 到 kv；
  - 每 2s pass：`eng.EvaluateDue(now)` 产生 Decisions → 统一 reply composer（按 source/route/purpose）；
  - 跨工作区派发（现有 engine 支持）+ 全局上下文（最近 N 条 Decision/Ticket 摘要注入 AssessInput）；
  - 记录 Router 指标（batch size、evaluate 延迟）到 health/panel。
- **提示词**：`internal/runtime/prompts.go` 扩展：团队三段（communication/scope/delivery）+ per-layer 覆盖 + base hash + versions 表（Wave1 store 若未加，fix-engine 加 `prompt_version` 表；否则 kv JSON）；保存校验（未知 key/占位符/超长/identical 拒绝/删除）；每轮 model 调用前读最新（assessor 不再启动时冻结）。
- **状态/退出码**：`cmd/zeokeel` 解析 `status|check|setup|login|backup|archive`；`status` 用 store.Inspect 读健康/锁/配置，退出码 0/1/2/3（006:113-124）；`--json` 可选。`-headless` 变成真正的服务模式（不初始化 mygo；跑 core + 2s pass + watchdog；信号优雅退出）。
- **日志**：`slog` JSON/Text → `<stateDir>/logs/zeokeel.log` 按天轮转（保留 14 天）；关键路径（启动/结束/错误/投递失败/模型错误）都记录；不记录密钥。
- **watchdog**：进程内 supervisor（60s）：检查 health、effects 队列积压/过期、running Assignment 超时、DB 可写；记录 + 自动恢复（重新入队 launch、标记失败）；连续 3 次失败写 health error。外部 systemd/launchd 模板由 docs 提供。
- **备份/归档**：`backup` → `VACUUM INTO` 副本到 `<stateDir>/backups/zeokeel-<date>.db`；`archive` → ended>180d 的 ticket/trigger/decision/outbox 移到 `archive.db`（ATTACH + INSERT + DELETE 事务），保留 outbox ID 集合；每日自动 VACUUM（低频）。
- **通道适配**：`reply.go` 按 source 生成：IM（中文 ≤100 词/字符校验，直接结论+问题）、GitHub（结构化：背景/改动/验证/交付物）、本地（简洁）；`ReplyPlan` 真正参与投递决策（notify 档不得直发同事，只能 operator 渠道）。

## 4. UI（fix-ui2）

- 通过 optional interfaces 调 runtime：`AnswerAttention(ticketID, answer)`、`StopTicket`、`RetryTicket`、`PauseExecution`；不 import runtime。
- Overview：按 stage 显示动作（批准/拒绝/回答/重试/停止）；回答用 `ui.Modal`+`TextInput`（选项按钮）；动作只在总览。
- Ticket 详情：点击/⌘K 打开（Modal 或独立页），显示 Mandate（strength/policy/rules）、Inputs、Assignments/Reports、Follows、Artifacts、Attention。
- Settings：Sources（GitHub/Feishu/DingTalk/IM admission table/bot list）、Eval（rules/enabled/accept mode/policy version）、Execution（concurrency/prepare/command timeout/blocked commands）、Prompts（已有+团队三段+版本历史+Default updated 提示）、Deployment（日志/备份/归档/健康/退出码说明）。
- 诚实状态：所有计数从 `len(rows)` 派生；Running/MaxJobs/Today/Queued/health 读 runtime 投影（runtime 修）；评估页 Evaluated/Decisions 不得恒等；总览"正常"按 health 计算。
- glossary（?）Modal；深浅色跟随系统；所有新动作有 `ui.NewTester` 测试。

## 5. 明确决策（写进文档，避免反复）

1. **Router 是"逻辑 Router"**：持久化全局状态 + 批处理 + 统一回复；不是单个常驻 LLM Session。文章允许 Router 内部非单一 Session/部分非 LLM，行为指标（批处理、连续性、统一回复）达标。文档写清这是实现变体。
2. **订阅直通**：有 TurnLoop 时支持 mid-turn inject；否则在下一 run 前投递（事件不丢、不重复 Eval）。若最终未实现 mid-turn，文档把 003:31-39 的"下一步"标注为"下一轮"偏差，并给出 eino TurnLoop 后续计划。
3. **计算/状态/环境分离**：单机单二进制（ADR-1/10），保留 SQLite 状态+崩溃恢复；跨设备/休眠/离线补偿 documented-deviation。
4. **管理 HTTP API**：桌面无 HTTP（ADR）；`status` CLI 是等价运维面，documented-deviation。
5. **A2A/多租户/安全**：文章开放问题，NOT-REQUIRED。
6. **依赖**：默认零新增；IM/GitHub 官方 SDK 迁移仅在 Lead 批准后；否则修复现有驱动 + 文档记录 ADR 理由（gh CLI 复用登录态；飞书/钉钉长轮询替代 WS 的边界）。

## 6. 验收（verifier 用）

- `CGO_ENABLED=0 go build ./... && go test ./... -count=1 -skip Live`；`go vet ./...`；`scripts/check-nocgo.sh`；`CGO_ENABLED=1 go test -race ./... -skip Live`（CI 专用步骤）。
- 行为矩阵：gate strength 6 例、admission 9 例、Eval 串行/合并/超时 5 例、ticket 生命周期 8 例、followup/letGo 6 例、delivery 三段 4 例、pause/stop/retry 5 例、crash restart 4 例、prompt 校验 6 例、status 退出码 4 例、channel 适配 4 例。
- 反证：verifier 独立写测试/脚本尝试证伪；失败项必须由 owner 修，不允许改测试绕过。
