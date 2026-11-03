# 一致性审计：原型 007-architecture / 008-cookbook（架构 / 边界 / 数据 / 持久化 / 扩展）

- 审计对象：`/data/ZeoKeel-Super-Agent`（Go 1.27.3，`CGO_ENABLED=0`）
- 需求来源：`_ref/docs/007-architecture.md`（617 行）与 `_ref/docs/008-cookbook.md`（425 行，下称 007/008）；另加 Lead 追加的《Super-agent 设计探索》官方架构（下称“官方文章”，设计文档在 §4.5:355-359 引用其原文）。
- 设计文档：`design/zeokeel-go-mygo-design.md`（下称 design）；实现为最终事实来源。
- 方法：先逐行读 007/008 抽取规范项，再按主题批量核对 design 与代码；`_ref/src` 仅在文档歧义处查阅（`src/app/store.ts`、`src/kv.ts`、`src/app/migration.ts`）。**只读**，未修改任何源文件；仅新增本文件。
- 本审计**不重做** 000-003 的 domain/authorization/execution 语义；相关项只从“架构机制是否存在、是否落在正确的边界上”角度裁决，并注明应由 000-003 审计覆盖。
- 环境事实：`go build ./...` 通过；`go test ./... -count=1` 24 个有测试的包全绿（本机实测，见 §7）。

---

## 1. 裁决统计与底线结论

### 1.1 计票（§2 矩阵 69 项 + §3 官方架构 11 项）

| 裁决 | 007/008 矩阵 | 官方架构 | 合计 |
| --- | ---: | ---: | ---: |
| CONFORM | 9 | 1 | 10 |
| PARTIAL | 33 | 3 | 36 |
| MISSING | 22 | 5 | 27 |
| DEVIATES-JUSTIFIED | 2 | 1 | 3 |
| DEVIATES-UNJUSTIFIED | 1 | 0 | 1 |
| EXTRA | 2 | 0 | 2 |
| NOT-REQUIRED（官方文章 §8 开放问题） | 0 | 1 | 1 |
| **合计** | **69** | **11** | **80** |

### 1.2 底线结论（一段话）

Go 项目**保住了原型最“重”的那块确定性内核**：durable Effects（事务内 enqueue、提交后执行、kind+key 合并、`Later`、退避、3 次失败健康、`drain`）、幂等 Outbox（先落库再发、业务 ID 幂等、同 Ticket 同会话保序、`gone` 与临时错误区分、GitHub 隐藏 marker 的远端幂等）、单写者 SQLite（WAL + `synchronous=FULL` + `MaxOpenConns(1)` + SAVEPOINT + `afterCommit`）、内容派生 ID 与 `StageOf` 查表、零 cgo 守卫、GitHub Source 的翻译/scope/自我过滤/游标推进纪律——这些是本审计里 CONFORM 最集中的地方，也是原型附录 A 列出的“不可简化资产”中最容易丢的部分。但**架构层面有三条主线没有兑现**：(1) 原型与官方文章的“唯一持久 Router 全局 LLM Session（批处理 + 统一回复 + 无执行工具）”在 Go 里不存在，`engine/router.go` 只是确定性召回 + 一次结构化模型调用，回复由 `runtime/reply.go` 的代码模板拼装；(2) 订阅快速路径（后续事件直达订阅 Worker、可 unsubscribe/hand-back、Worker 级输入通道）完全缺失，`ContextInput` 只写不读；(3) 外部 watchdog（独立 full-tool 控制回路 + `status` 退出码 0/1/2/3 + 每小时检查）与 `-headless` 常驻模式都不存在，design §6.7/§8.4 承诺的“进程内 watchdog + `zeokeel status` + flock”在代码中为 0。更严重的是**生产接线把耐久性绕过了**：GitHub 轮询与定时任务都直接调 `engine.Receive`（裸 Engine），不经过 `Persistence`，因此不落库、不产生 `launch` effect、重启即丢；游标只活在 goroutine 局部变量里，重启后静默跳过停机期间的事件。**结论：持久化原语（Effects/Outbox/Store）是工程质量最高的部分，但“谁能进领域”的入口边界与“官方架构的 Router/订阅/watchdog”两条主承重墙没有建成；按 007/008 的架构一致性计，整体处于 PARTIAL/MISSING 区间（CONFORM 12.5%，10/80），其中扩展路径（008）实质不可用。**

---

## 2. 需求矩阵（007 / 008，69 项）

> 裁决只有一个。严重度：高=会导致数据丢失/功能不可用/违反硬约束；中=语义漂移或维护性风险；低=命名/文档/局部不一致。
> 设计证据列给 design 的节与行；代码证据列给 file:line。

### A. 组合根与生命周期（9）

| ID | 需求（引文 + doc:line） | 裁决 | 设计证据 | 代码证据 | 严重度 | 说明 |
| --- | --- | --- | --- | --- | --- | --- |
| A01 | “`Runtime` is the composition root; … its constructor locks the state directory, opens the Store and builds each component” (007:31) | PARTIAL | §3.1:160-188 | `cmd/zeokeel/main.go:99-165`；`internal/runtime/runtime.go:71`；`internal/store/store.go:43-65` | 高 | 组合根存在（cmd 装配 + Runtime 发布投影），但**没有任何 stateDir 锁**（`store.Open` 无 flock/O_EXCL；`grep flock` 为空），单实例仅靠 `ui/app.go:183` 的 mygo 锁，`-demo/-headless/-pipeline` 绕过。 |
| A02 | 启动序：注册路由、Setup、hooks、确保主 Session、Policy 同步、续跑被打断的 Assignment、启动 passes/effects、`ready` (007:33) | PARTIAL | §3.1:160-188 | `cmd/zeokeel/main.go:161-186`；`internal/runtime/durable.go:20-46,74-89` | 高 | effects 在 `queue.Start()` 后运行 ✅；**无主 Session、无 Policy sync、无 interrupted-Assignment 续跑、无 passes、无 `ready` 标志**。 |
| A03 | “Shutdown cancels turns, waits for passes, effects and agents, restores the process environment, closes the Store and releases the lock.” (007:35) | PARTIAL | §3.1:160-188 | `cmd/zeokeel/main.go:238-244`；`cmd/zeokeel/durable.go:74-89` | 中 | 等 worker（`Drain: rt.WaitIdle`）与 effects（`queue.Drain`）✅、关 Store ✅；**无环境恢复（本实现从不改环境）、无锁可释放**。 |
| A04 | Setup：探测 CLI、写并应用 worker 环境与命令 shim、保留 setup 问题；Settings 复用 (007:39) | MISSING | §8.2:1579-1587 | `internal/runtime/onboarding.go:15-260`；无 `internal/setup` 包 | 高 | 只有 onboarding 向导（模型/工作区/来源/名单四个输入），**不探测 gh/lark-cli、不写环境变量、无 shim、无 setup 问题模型**；`SETUP_FIELDS` 无对应物。附带缺陷：向导值 `SaveOnboarding` 只写内存（`runtime/onboarding.go:104-148`），**不调用 `SaveSettings`**；由代码推断：重启后模型配置丢失（`main.go:112-126` 只从 store 读；除非用户之后手动保存过一次设置，才会把内存快照写库）。 |
| A05 | Passes：“One pass at a time, every two seconds and whenever a command asks: the due effects, then `Engine.evaluateDue`; a Source's Trigger reaches the Engine through it” (007:40) | MISSING | §3.3:221-259 | `cmd/zeokeel/main.go:180-186`（2s ticker 只 `Publish`）；无 passes/`evaluateDue` | 高 | 2 秒 ticker 是面板重绘，不是调度；没有任何 due 检查/触发批处理/超时。 |
| A06 | Outbox：“the only writer of the receipts: `send` keeps a message's receipt and queues its `reply` effect in one transaction … a message waits while its Source is off, and is dropped when the conversation is gone or not listened to” (007:41) | PARTIAL | §7.2:1541-1546 | `internal/outbox/outbox.go:147-192`；`cmd/zeokeel/sender.go:40-48` | 高 | “回执 + effect 同事务” ✅；“Source off 时等待” **不成立**：无 driver 时 sender 直接返回 `OutcomeSent`（假成功），消息既不发也不重试；`dropped` 语义只在 `gone`。 |
| A07 | Notices：领域事件与 worker 活动转成主会话消息（`eventNotice`/`reportRelay`/`followupRelay`）(007:42) | MISSING | §4.10:806-863 | `internal/runtime/runtime.go:201-274` | 中 | 只有面板 stream 的投影；无“主会话”可通知，也无 relay/notice effect kind。 |
| A08 | Source 插件经窄 `SourceHost` / `SourceContext` 注入 Runtime (007:35,193) | MISSING | §4.8:628-716 | `internal/sources/source.go:55-69`；`internal/sources/github/config.go:135-145` | 高 | Source 只拿到 `Command`（exec）与 `receive` 回调；无 deployment/commands/ready/configured/config/atomic/state/threads/tickets/triggers/notify/report 的 host 对象。 |
| A09 | 宿主整体替换：伪宿主契约（Session/panel/HTTP/setup/sandbox）由自研等价物提供 | DEVIATES-JUSTIFIED | §3.1:160-188；ADR-2:141 | `internal/agent/*`、`internal/view/*`、`internal/ui/*`、`internal/store/*` | — | 用“进程内自研 runtime + view 投影 + 原生面板 + SQLite”替换 DSH 宿主；方向正当（ADR-2 已论证），但等价物在 A02/A04/A05/A07 处并不完整。 |

### B. Engine / 端口 / 并发（10）

| ID | 需求（引文 + doc:line） | 裁决 | 设计证据 | 代码证据 | 严重度 | 说明 |
| --- | --- | --- | --- | --- | --- | --- |
| B01 | “`Engine` is the only writer of Triggers, Threads, Decisions, Tickets and Assignments” (007:46) | PARTIAL | §3.3:221-259 | `internal/engine/engine.go:62-83,152` | 高 | 内存侧单一写者 ✅（`engine.mu`）；但**外部入口绕过 `Persistence`**：`cmd/github.go:70`、`cmd/extras.go:125` 直接 `eng.Receive`，这类命令不落库、不产生 launch effect——同一条“命令”有两套写路径。 |
| B02 | Engine 从 ports 收 `Repositories`、Policy getter、`SourceScope`、`Assessor`、`Executor`、`Outbox` (007:46) | PARTIAL | §3.2:189-221 | `internal/engine/engine.go:88`；`internal/engine/persist.go:20-24` | 中 | 只有 `Assessor` 是 port；`Repository/Executor/Outbox` 是具体包（persist.go import store/effects/outbox），`SourceScope` 由 Engine 自己调 `Kind.Scope`。 |
| B03 | “All Engine methods are synchronous. Each command changes its aggregates in one transaction … Invalid requests throw `UserError`.” (007:48) | PARTIAL | §3.3:221-259 | `internal/engine/persist.go:196-210,317-329`；`internal/store/store.go:191-261` | 中 | 同步 ✅；但聚合先在内存改、再单独事务写库（失败靠 `resync()` 全量重载），不是“命令=一个事务”；无 `UserError` 码表/拒绝分类。 |
| B04 | Engine 分五个 part（Intake/Evals/Tickets/Assignments/Replies）且依赖单向；调用方只拿角色的 `Pick`（007:52-64） | MISSING | §4.5:381-395 | `internal/engine/engine.go`（单文件，无 part） | 中 | 无 part 拆分、无 `EngineFor*` 接口（design §1.5:122 声称“5 个窄 interface”，实际 grep 为 0）；所有调用方共享完整 `*engine.Engine`。 |
| B05 | `receive(trigger)`：存一次并记 disposition（`stored`/`duplicate`/`filtered`）、扇出订阅 Ticket、为 Thread 排 Eval (007:68) | PARTIAL | §3.3:221-259 | `internal/engine/engine.go:152-289` | 高 | 幂等去重 ✅、扇出 ✅（但只追加文本）；无 `filtered` 返回值（gate 拒绝返回 `Outcome{Kind: filtered}` 而非 error）、无 per-Thread Eval 队列/批处理、无“人的消息直达 Ticket”。 |
| B06 | `bindThread` / `threadClosed`：Source 绑定 Thread，Thread 关闭驱动 Ticket 结束（`followsDone`）(007:69-70) | MISSING | §4.7:489-510 | 全仓 grep `bindThread/threadClosed` 为 0 | 高 | 无 Thread 生命周期绑定；GitHub PR 关闭不会结束 Ticket（000-003 覆盖语义，但架构上也缺命令）。 |
| B07 | 变更后 emit `DomainEvents`（列表），Runtime 把 3 类转 notice (007:105-109) | MISSING | §4.10:806-863 | 无 events 类型/emit | 中 | 无事件总线；面板靠定时全量重投影。 |
| B08 | 领域类型化：Ticket `origin/execution/delivery/follows`、`MandateBasis{via,…}`、Trigger/Thread `meta` (007:113-134) | PARTIAL | §7.1:1523-1541 | `internal/domain/ticket.go:220-245`；`internal/domain/trigger.go:93-153` | 中 | `Mandate` 有 strength/policyVersion/actor/workspace，**无 `basis{vìa}`**；`follows`/`delivery` 缺失；Trigger/Thread 无 `meta`（见 F06）。 |
| B09 | Policy：`settingsPolicy` 每次新 Policy；`gate`/`revalidate`/`admitsInferred`；`policyVersion` 哈希规则+accept mode+Source policy (007:138-147) | PARTIAL | §4.3:324-335 | `internal/engine/engine.go:24-44,339-370` | 中 | 有 per-workspace `Policy{Allowed,AdmitsUnaddressed,ReplyMode,AcceptMode}`；**无 `revalidate`、无 policyVersion 哈希（只有字符串常量 "v1"）、无 policyEffects**。 |
| B10 | `WorkerSlots` 预留容量；“A human message can use one slot beyond `maxWorkers`, and wakes have priority over launches”；瞬时失败 3 次重试、失败回合 15/30/60 续跑 (007:203,205) | MISSING | §4.4:335-354 | `internal/runtime/durable.go:44`；`internal/runtime/worker.go:254-260` | 高 | `Limit: r.concurrency` 只限制**launch effect 的并发**，handler 里 `go func` 起 worker 后立即返回，**worker goroutine 实际不受限**（N 张工单可同时跑）；无“人 +1 槽”、无 wake 优先、无 3 次重试/续跑消息。 |
| B11 | 启动时 `resume` 报告 interrupted Assignments（`hostRestarted`），oldest Ticket first (007:92,207) | MISSING | §5.8:1074-1084 | 无启动扫描；`grep hostRestarted` 为 0；`Runner.Resume` 无生产调用者（`internal/agent/agent.go:308`，仅测试） | 高 | 崩溃后 Ticket 永远停在 `working`（`domain/ticket.go:285-288`），会话日志永不续跑。 |

### C. Store / 数据模型（11）

| ID | 需求（引文 + doc:line） | 裁决 | 设计证据 | 代码证据 | 严重度 | 说明 |
| --- | --- | --- | --- | --- | --- | --- |
| C01 | “Runtime acquires the directory lock before opening it, preventing a second process from migrating or writing the same state.” (007:151) | MISSING | §6.7:1359-1362 | `internal/store/store.go:43-65`；`internal/ui/app.go:183` | 高 | 无目录锁/文件锁；两个进程（GUI + `-pipeline`，或两个 GUI 之一绕过 mygo 锁）可同时写同一 SQLite，各自内存态独立 → 丢失更新/重复建单。 |
| C02 | 域仓储分组（`atomic` + triggers/threads/tickets/decisions 的 get/save/list）+ 只读 `read` 层，读方法绝不写 (007:155-156) | PARTIAL | §7.1:1523-1541 | `internal/store/store.go:295-497`；`internal/store/load.go`；`internal/runtime/pages.go:69-238` | 中 | 有 typed get/put 与 `Atomic`；**没有仓储接口抽象**，面板读的是 Engine 内存投影而非 Store 查询层（引擎全量 Load 进内存，读放大可接受但违背“读查询层”设计）。 |
| C03 | `threads.describe` / `threads.patchMeta`：Source 维护自己 Thread 的 `label/url/facts` (007:157) | MISSING | §4.8:656-692 | 全仓无 meta 字段/接口 | 中 | 来源页只能显示从 Trigger 推出的信息，无法由 Source 补充展示名/链接/事实。 |
| C04 | Settings 与 Prompts 用编号 `Versions`（`settings-version:<n>`、`settings-block-revision`、`prompt-version:<n>`、`prompt-built-in:<base>`、`session-prompts:<sid>`）(007:158,465,473-476) | MISSING | §7.1:1523-1541 | `internal/store/settings.go:11-95`；`internal/runtime/prompts.go:14-90` | 中 | 只有一个全局 revision（`setting.__revision`）+ 扁平 key 快照；**无历史、无逐 block revision、无 origin(panel/tool/sync)、无 prompt 版本/内置 base/“默认已更新”**。 |
| C05 | 回执操作 get/waiting/behind/add/settle/withdraw；`behind` = 同一 Ticket+会话、更早且失败<3 次；withdrawn 以 `channelGone`/`channelIgnored` 结算；读查询 `failingReplies`/`droppedReplies`/`droppedSince` (007:159) | PARTIAL | §7.2:1541-1546 | `internal/outbox/outbox.go:195-354` | 中 | get/waiting/behind/settle ✅；**Withdraw 直接 DELETE 回执**（outbox.go:230-251），与“回执永久保留、ID 永不二次入队”相反；无 dropped 时间/原因、面板无失败/丢弃回执列表。 |
| C06 | KV 类型化注册表：每个 key/value 类型/owner 只在一处拼写，读者全部经它 (007:468-478) | MISSING | §7.1:1539 | `internal/store/store.go:174,475-490`；`internal/store/schema_v3.go:94,124`；`internal/store/settings.go:11,17` | 中 | 无 `KV` 注册表；仅 3 个字面量散落 2 个文件；`tests/store.spec` 的“key 只出现在 kv.ts”等价约束不存在。 |
| C07 | `afterCommit` 只在最外层提交后执行，回滚不执行 (007:162) | CONFORM | §7.2:1541-1546 | `internal/store/store.go:279-293,234-255` | 低 | 行为正确（回滚清空 callback，提交后 guard 外执行）。 |
| C08 | 持久化表集合：trigger(+`at`)、disposition(完整去向)、trigger_thread(倒排+`<at>\|<id>` cursor)、thread(generated `active`/`subscribed`+meta)、ticket(generated `open`/`active`+assignments/inputs/follows)、decision(by thread)、effect、outbox(+`dropped` 生成列)、kv (007:454-465) | PARTIAL | §7.1:1523-1541 | `internal/store/schema.go:25-113,133-150`；`internal/store/schema_v3.go:32-85` | 高 | 见 §5 表级 diff：缺 `trigger_thread`、`decision.thread`、`thread.active/subscribed`、`outbox.dropped`、独立 `assignment` 表、`workspace`/`health`/`session_event` 为原型没有的 EXTRA（其中 `session_event` 建了但**无人写**）。 |
| C09 | 生成列 + 局部索引，让每次 pass 只扫活跃行；“a database that predates a column gets it when the Store opens it” (007:466) | PARTIAL | §7.1:1541 | `internal/store/schema.go:35-36,52,62,92,137-149`；`internal/store/store.go:126-183` | 中 | 有 `ticket_open` 局部索引与 `trigger_by_*`；**无 `thread.active/subscribed`/`outbox.pending(composite)`/`ticket.active`**；新列靠有序 migration，无“开库补列”机制（与“跳过 legacy”一致，但 schema 演进时需手工迁移）。 |
| C10 | `SCHEMA_VERSION` 只增；更新的库拒绝启动；旧库一次迁移 (007:486) | PARTIAL | §7.1:1523-1541 | `internal/store/store.go:126-156`；`internal/store/schema_v3.go:10-27,92-133`；`internal/store/schema_v3_test.go:33-44` | 高 | `ErrNewerSchema` ✅（`store_test.go:201`）；但 **v3（schedule/goal）不在 `SchemaVersion`/`migrations` 里**，靠 `kv` 旁路 marker，测试还显式断言“global schema 不动、旧 build 不应拒绝”——一个 v2 build 打开 v3 库会静默忽略 schedule/goal 表，定时/目标停摆。 |
| C11 | Trigger 时间存“the one form `Date#toISOString` gives … so times compare as text”；时间线按 time then id (007:456) | PARTIAL | §7.1:1526（“at 统一 RFC3339Nano（字典序可比）”） | `internal/store/store.go:361`（RFC3339Nano 写入）；`internal/store/schema.go:31`（注释称可字典序比较）；`internal/store/load.go:52-56`（`ORDER BY t.at,t.id`） | 中 | **定宽形式只在 durable half 用**（`store.go:115-124` `FormatTime`，正确）；`trigger.at`/`ticket.created_at`/`thread.last_at` 用 `RFC3339Nano`，非定宽，同秒内文本排序错误（`store.go:117-119` 自己的注释已说明这个坑）。 |
| C12 | 原型没有 TTL/清理，记录无限增长（007:484 事实陈述） | CONFORM | §7.3:1546-1557（design 承诺归档，未实现） | 无 archive/VACUUM 代码（grep 为 0） | 低 | 与原型行为一致；但 design §7.3“第一天就做归档”未落地，是**设计承诺缺口**而非 007 偏差（见附录 A）。 |

### D. Effects / Outbox（8）

| ID | 需求（引文 + doc:line） | 裁决 | 设计证据 | 代码证据 | 严重度 | 说明 |
| --- | --- | --- | --- | --- | --- | --- |
| D01 | “A command calls `enqueue(kind, key, payload)` inside its transaction; execution begins only after commit.” (007:166) | CONFORM | §7.2:1541-1546 | `internal/effects/effects.go:192-226`；`internal/store/store.go:279-293` | 低 | `Enqueue` 走 `AfterCommit(q.kick)`；回滚则行随事务消失。 |
| D02 | 每个 kind/key 一行；重复 enqueue 替换 payload、立即到期、不改队列序 (007:168) | CONFORM | §7.2:1541-1546 | `internal/effects/effects.go:206-221` | 低 | `ON CONFLICT DO UPDATE` 保留 `seq`、`due=now`。 |
| D03 | 每 kind 一个 handler；due 行 oldest first，最多并发 limit；`Ordered` 语义 (007:168) | CONFORM | §7.2:1541-1546 | `internal/effects/effects.go:155-183,396-438,443-487` | 低 | handler 唯一性、`readyRows` 按 seq、`Ordered` 只看最老一行。 |
| D04 | 失败指数退避；`Later` 不计失败；`start`/`drain`/`pending`/`idle` (007:169-170) | CONFORM | §7.2:1541-1546 | `internal/effects/effects.go:318-374,513-570` | 低 | 与原型语义一致；`drain` 保留行、`Later` 不计 attempts。 |
| D05 | 每 kind 的并发/重试表：assessment 4、launch=空闲 worker 槽、delivery 4、relay 1（保序）、notice 1、reply 1（等待前序，5s→300s）(007:172-179) | PARTIAL | §7.2:1541-1546 | `internal/effects/effects.go:27-77`；`internal/runtime/durable.go:41-45`；`internal/outbox/outbox.go:128-139` | 中 | 只有 `launch`（5s→5min）与 `reply`（默认 2s→60s，非 007 的 5s→300s）两个 kind；assessment/delivery/relay/notice 全缺；launch 的 limit 语义见 B10。 |
| D06 | 每 kind 一条健康条目；连续 3 次失败报 error（`delivery` 例外）；relay/notice 保序 (007:181) | PARTIAL | §7.2:1541-1546 | `internal/effects/effects.go:24-27,589-642` | 中 | `effect:<kind>` 健康 + 3 次阈值 + `Quiet`（reply 例外）✅；无 relay/notice，健康条目也没进面板（面板健康是运行时手拼，`runtime.go:123-129`）。 |
| D07 | Outbox：先落库再发；业务 ID 幂等；回执**永久**保留；同 Ticket 同会话保序；`gone` vs 临时错误 (007:463；design §7.2:1541-1546) | PARTIAL | §7.2:1541-1546 | `internal/outbox/outbox.go:147-192,300-354`；`internal/outbox/outbox.go:230-251` | 高 | 前四条 ✅；`Withdraw` 删除回执、注释明说“同一 ID 之后可以再记录”，与“永久保留/永不二次入队”冲突，崩溃/重放路径可重复外发。 |
| D08 | 远端幂等：`message.id` 作为 idempotency key；只有永久失败返 `gone`；其他失败抛错重试 (007:41,179；008:177-178) | CONFORM | §7.2:1541-1546 | `internal/sources/github/channel.go:31-90`；`internal/sources/source.go:128-161` | 低 | GitHub 用隐藏 HTML marker 先查后发；410/被锁 → `Gone`；CLI 错误 → error 让 Outbox 退避。 |

### E. Sessions / events / Executor（6）

| ID | 需求（引文 + doc:line） | 裁决 | 设计证据 | 代码证据 | 严重度 | 说明 |
| --- | --- | --- | --- | --- | --- | --- |
| E01 | Sessions 管理两个角色：`ensure`/`install`/`prompt`（每 request id 一次）/`relay`/`notify`；模型请求绑定当前 prompt revision，durable 记录 `promptVersion` 与 `mayRead` (007:235-241) | MISSING | §5.2:999-1027 | 无 Sessions 类；`internal/agent/agent.go:283-333`（一次性 Run/RunSession/Resume）；`internal/runtime/prompts.go` | 高 | 无主会话管理、无 request-id 去重投递、无 steer/inject/followup 两级投递、无 prompt revision → 会话绑定。 |
| E02 | 会话事件日志持久化；事件种类/顺序；未配对 tool call 标 interrupted + recovery note；面板时间线与会话同一事实源 (007:92,239；design §5.2:999-1027、§5.7:1066-1074) | PARTIAL | §5.2:999-1027；§5.7:1066-1074 | `internal/agent/agent.go:246-249,308-333`；`internal/agent/session.go:46-87`；`internal/runtime/worker.go:118-172`；`internal/store/schema.go:94-101` | 高 | 事件落 eino `FileStore`（JSONL，在 ticket workdir 内）✅；interrupted patch/recovery note **已实现但生产从不调用**（`Resume` 只被测试调用）；面板读的是**内存** `eventLog`（2000 条上限、重启清零），与 JSONL 不是同一事实源；SQLite `session_event` 表建了但**没有任何读写者**。 |
| E03 | 投递三段式：`inputsDelivered` / `inputsRead`（成为 `user/message` 才算读到）/ `turnEnded` (007:84-86,92) | MISSING | §5.2:999-1027 | 无 Inputs/Inbox；`grep inputsRead/turnEnded` 为 0 | 高 | 无 Worker 输入通道，自然也没有已读回执/回合结束判定；000-003 语义层因此失去事实来源。 |
| E04 | `DshExecutor`：每 Ticket 一会话 + 一目录；`start`/`deliver` 入队 `launch`/`delivery`；`stop` 经 `afterCommit` 取消回合 (007:199) | PARTIAL | §3.3:221-259 | `internal/runtime/worker.go:194-341`；`internal/runtime/durable.go:20-46`；`internal/engine/persist.go:29-35,400-410` | 高 | 每 Ticket 一目录/一会话 ✅、`launch` effect ✅；**无 `delivery` effect、无 `stop`/取消**（UI 没有 retry/stop 动作）。 |
| E05 | `prepare(ticket, outcome)` hook：短名、目录、`ticketExecution` assignment 文本；`workerSessionId(ticket.id)` 派生 (007:201) | PARTIAL | §5.5:1048-1058 | `internal/runtime/worker.go:225-233`；`internal/engine/engine.go:277-280` | 中 | 目录 `Root/ticket-<N>` 与 `worker-<N>` session id 是确定性派生 ✅；**无 prepare 命令钩子、无短名、无 assignment 文本模板**。 |
| E06 | `install` 订阅 Session 事件；turn end 正常报 `waiting`（readiness/human 除外），错误报 `failed`；启动 resume 报告 interrupted，oldest first (007:207) | PARTIAL | §5.8:1074-1084 | `internal/runtime/worker.go:296-351`；无 install 订阅 | 中 | 运行结束按结果报 `ready`/`failed` ✅；无事件订阅、无 `waiting` 语义、无启动 resume。 |

### F. Sources 与扩展契约（12）

| ID | 需求（引文 + doc:line） | 裁决 | 设计证据 | 代码证据 | 严重度 | 说明 |
| --- | --- | --- | --- | --- | --- | --- |
| F01 | `SourceKind` 声明 identity/title/description/config `schema`/`fields`/`facts`/`health`，可选 `scope`/`channel(allowed,send,link)`/`threadContext`/`find`/`policy`/`environment`/`refreshLabels` (007:185) | PARTIAL | §4.8:656-716 | `internal/sources/source.go:55-198` | 高 | `Kind` = Name/Scope/Translate/Channel + 可选 `Poller/MemberLister/HealthReporter`；**title/description/schema/fields/facts/health 声明/find/threadContext/policy/environment/refreshLabels 全无**（面板无法生成表单）。 |
| F02 | Registry 拥有 kinds 与**实例**、实现 `SourceScope`、健康条目 claim 与冲突拒绝、SourceContext 只能写自己 claim 的条目 (007:187,189) | PARTIAL | §4.8:656-692 | `internal/sources/registry.go:12-91` | 高 | 编译期注册 + 空名/重名拒绝 ✅；**无实例、无 SourceScope 接口、无健康 claim、无 per-kind 状态写权限**。 |
| F03 | 泛型代码经 registry 访问行为：`send` 返回 `sent`/`gone`/**`waiting`（kind 未安装）**；`link`；`threadContext`；坏配置禁用实例 (007:191) | DEVIATES-UNJUSTIFIED | §4.8:656-716 | `cmd/zeokeel/sender.go:40-48`；`internal/outbox/outbox.go:274-280` | **高** | Go 在“无 driver”时返回 `OutcomeSent`（假回执），既不是 `waiting` 也不是失败：飞书/钉钉消息会被永久标记 sent 而从未发出。 |
| F04 | Source 实例来自持久配置（`source:<kind>` 设置块），面板可添加/开关/填写；坏配置只禁用该实例 (007:187,191；008:31-79) | MISSING | §4.8:656-716 | `cmd/zeokeel/github.go:30-45`（仅 CLI flag）；`internal/runtime/pages.go:238-275`（只读） | **高** | 没有 Source 实例的存储/加载/面板编辑；GitHub 仅 `-repo/-gh-login` 启动参数；onboarding 的 `source_kind/source_repo` 写了没人读。 |
| F05 | 翻译契约：稳定内容派生 ID、Thread 主线程、Source 原生 actor、`human`/`addressed` 真事实、自身消息不产生 Trigger、坏 item 逐条跳过 (008:98-123,299-328) | PARTIAL | §4.9:723-800 | `internal/sources/github/trigger.go:40-125`；`internal/sources/github/poll.go:43-190` | 低 | 核心翻译全部正确（含 `updated_at` 版本化、bot/self 过滤、空正文跳过、Unreadable 计数）；**`meta` 字段缺失**（见 F06），bad JSON 逐条容错只覆盖 item 解码，shell 坏整体失败。 |
| F06 | Trigger/Thread `meta` 归 Source 所有、核心不解释；Channel address 携带回信事实 (007:134；008:115,123) | MISSING | §7.1:1526-1527 | `internal/domain/trigger.go:93-153`（无 Meta 字段）；`internal/sources/github/trigger.go:100-116`（塞进 `Channel.Address`） | 中 | Source 私有事实只能塞进 `Channel.Address`（回信寻址语义被复用成元数据），无法存 Thread 级 `/root`、conversation 等；重放时语义耦合。 |
| F07 | 游标：存 typed `ctx.state`/KV；只有全部 `receive` 成功才推进；中断轮次不动；重放安全；时间戳游标 `cursorAt(-5s)` (008:322-324；007:190,477) | PARTIAL | §1.5:127-129 | `internal/sources/github/poll.go:43-79,191-218`；`cmd/zeokeel/github.go:58-102` | **高** | 推进纪律 ✅（err 时 Next=Cursor；`encodeCursor` 带 repo 防串仓库；-5s slack ✅）；**游标从未持久化**：`var cursor sources.Cursor` 是轮询 goroutine 局部变量，只在 `SetSourceStatus` 里显示；重启后 `since=now`，停机期间事件静默丢失。 |
| F08 | 订阅 Thread 轮询（推+拉）、`find(query)` 源特有引用、`threadContext` 供 Eval 读上下文 (007:364-365；008:127-133,182-210) | MISSING | §4.6:460-476；§5.4:1039-1048 | `cmd/zeokeel/github.go:68`（`PollRequest{Cursor}`，`Threads` 永远空）；无 `find`；无 threadContext | 高 | `PollRequest.Threads` 存在但生产从不填；`Find`/`ThreadContext` 接口/实现均无；评估拿不到 Thread 窗口。 |
| F09 | Source 的 Policy part：`allowed` 名单 + admission `rows`/cells + `explicit` 文案 + `bots` + 可选 `sync`；参与 policyVersion (008:212-234；007:147) | PARTIAL | §4.3:324-335 | `internal/engine/engine.go:24-44`；`internal/runtime/onboarding.go:226-260`（`parseAllowlist`） | 中 | 有 workspace 级 `Allowed[source][actor]`（“谁能发起”）✅；**无 per-Source policy schema/rows/cells/bots/sync**，admission table 不存在，policyVersion 是常量。 |
| F10 | Source 声明的 Executor 环境变量（`DSH_...<KIND>_…`，`values(trigger)`；不同来源返回空；不含凭据）(008:236-250) | MISSING | §5.5:1048-1058 | 无 Source environment 接口；`internal/agent/tools.go:251-253` 只注入 TMPDIR/ZEOKEEL_WORKDIR | 中 | 无变量声明/求值/面板展示；Worker 无法从环境拿到“本工单来自哪个会话/仓库”。 |
| F11 | 插件入口与循环：`name`/`inject`/`apply`；effect 注册/注销 kind；循环等 `ready`、尊重开关、`abort`、健康计数、逐条容错 (008:252-328；007:400) | PARTIAL | §4.8:628-716 | `cmd/zeokeel/github.go:47-120`；`internal/sources/github/config.go:135-145`；`cmd/zeokeel/extras.go:154-188`（`startIMLoop`，**无调用者**） | 高 | Go 无插件机制，等价物是 `init()` 注册 + cmd 里手写循环；循环没有 `ready/configured/开关/abort`（用 ctx + 固定 ticker），`HealthReporter` 有但未接面板；IM 的 adapter 写好了却没接线。 |
| F12 | 扩展交付物：构建入口/包导出/patch 插入；**在 Sources 设置与 Eval→On each Source 出现**；模块与测试表、`tests/docs.spec` 完整；测试脚手架与 11 步 checklist (008:334-425) | MISSING | §4.8:628-716 | 无扩展文档（全仓 grep “新增 Source/add a source” 仅 design 两行声明）；无 docs.spec；`internal/sources/github` 55 个测试但无通用 `sourceContextOf` 脚手架 | **高** | 新 Source 今天只能靠改 `cmd/zeokeel` + 自造游标持久化 + 无面板表单；008 的“注册后自动出现在设置”完全不存在，扩展契约不可执行。 |

### G. 面板 / 模型面契约 / 配置 / 依赖规则（9）

| ID | 需求（引文 + doc:line） | 裁决 | 设计证据 | 代码证据 | 严重度 | 说明 |
| --- | --- | --- | --- | --- | --- | --- |
| G01 | 管理 API：`/orchestrator/api` 同源检查、请求上限、每类失败一个状态码 (007:345) | DEVIATES-JUSTIFIED | §3.1:160-188（“没有 HTTP、没有 IPC”） | `internal/view/*`（不可变投影）；`internal/ui/app.go:28-36`（`Commander` 命令接口） | — | 宿主替换为进程内只读投影 + 单一命令接口；**等价能力**：面板不可写库、只能经 `Cmd` 提交。失去的是“外部可调用的运维接口”（直接影响官方 watchdog，见 O-09）。 |
| G02 | 面板/模型资源契约：分页 `{items,next_offset}`、文本页 4000/8000 字符、列表 20/50 条；`RESOURCES` 与 `read_domain` 等 read 工具 (007:250,552-579) | MISSING | §4.6:460-476 | `internal/view/search.go:101-105`（仅搜索分组 20 条上限）；`internal/runtime/pages.go` 全量列表；无 resources 包 | 高 | 面板全部一次性渲染，无分页/游标；模型侧无 `read_domain`/`eval_read`/`workspace_read`/`recent_tickets` 工具，读不到 Trigger/Thread/Ticket 资源。 |
| G03 | 模型面工具：主会话 14 个应用工具；Executor 6 个（read/ack/report/thread_unrelated/thread_follow/trigger_handback）(007:492-532) | PARTIAL | §5.3:1027-1039 | `internal/runtime/artifacts.go:24-104`；`internal/agent/tools.go:1-350`；`internal/runtime/extras.go`（capabilityTools） | 中 | 实际 12 个工具：bash/read/write/edit/glob/grep/report/present/ask_user_question/load_skill/schedule_task/set_goal（+MCP）。**无主会话角色及其 14 工具**；Executor 侧只有 report/present/ask，缺 ack/handback/follow/unrelated/read_domain。 |
| G04 | 注入会话的消息内容契约：candidate/follow_up/worker_report/worker_attention；其他通知不唤醒；不重复通知自己刚做的操作 (007:583-594) | MISSING | §4.10:806-863 | 无 host inbox；`internal/runtime/runtime.go:201-274` 只是面板流 | 高 | 无 steer/inject/notice 通道，契约无处落地；000-003 的归属/去重语义因此缺少运行时事实。 |
| G05 | 面板专属动作（dismiss unavailable、重跑 setup、刷新 Source 名称、拒绝 requeue）与“提示词在 `prompts/*.md`” (007:598-602) | PARTIAL | §6.3:1222-1317 | `internal/ui/overview.go:402`（Approve）；无 dismiss/requeue/setup 重跑；`internal/prompts/*` 是 Go 字符串 | 中 | 只有 Approve/Reject；提示词编辑器存在（3 层），但无版本/diff/base，且内置文本在 Go 常量而非 md 文件。 |
| G06 | 配置重载：除 `stateDir` 外插件配置键全部 volatile，用到时读取即可生效；改 setup 字段触发重启并重读 (007:604-608) | PARTIAL | §6.5:1331-1348 | `internal/runtime/durable.go:81-94,113-124`（concurrency/poll/timeout 每次读） | 中 | 面板的并发/超时/轮询间隔即改即生效 ✅；无 plugin Config/setup 重启语义。 |
| G07 | 依赖方向：core 不 import 外部/不做 I/O；sources 不 import app；panel 只 import 类型 (007:258) | CONFORM | §3.2:189-221 | `internal/domain`（0 internal import）；`internal/agent`（0 internal import）；`internal/sources`→domain only；`internal/ui`→view only（`go list` 实测） | 低 | 方向确实单向、无循环、domain 零 I/O。小瑕疵：`internal/engine` import `effects/outbox/store` 的**具体包**（设计矩阵写“窄接口”），且 design 列出的 `internal/policy`/`internal/executor` 包不存在（executor 逻辑在 `internal/runtime/worker.go`）。 |
| G08 | “one writer per state”：Trigger/Thread/Decision/Ticket=Engine；Source 定位/meta=Source；Settings=Settings；Prompts=Prompts；effect 行=Effects；回执=Outbox；健康=claim 者 (007:259-271) | PARTIAL | §3.2:189-221 | `internal/engine/engine.go`；`internal/effects/effects.go`；`internal/outbox/outbox.go`；`internal/store/settings.go`；`internal/store/schema.go:103-108` | 高 | 大体成立；但 (a) Source 定位无写者（B/F07）；(b) `health` 表只有 effects 一个写者（`effects.go:635`），其余“健康”是 `runtime.Publish` 手拼（`runtime.go:123-129`），不是被 claim 的状态；(c) 外部入口绕过 Persistence 造成同一聚合两条写路径（B01）。 |
| G09 | 命名：请求用祈使（`stopTicket`），适配器事实用过去式（`assignmentStarted`）；非法请求 `UserError` 码表 + 面板文案 (007:272-274) | CONFORM | §9.3:1647-1656 | `internal/engine/engine.go:152,494,636,730,832,898`；`internal/view/reason.go:9-60` | 低 | 命名基本一致（`Receive/Dispatch/Approve/Report/StartAssignment`）；reason 码有面板渲染表 + 未覆盖码显式提示。无 `UserError`（无 HTTP 层，影响有限）。 |

### H. EXTRA（原型没有、design 增加）（2）

| ID | 内容 | 裁决 | 设计证据 | 代码证据 | 严重度 | 说明 |
| --- | --- | --- | --- | --- | --- | --- |
| H01 | 多工作区实体：`workspace` 表 + 全表 `workspace_id` 属性（“多工作区不多租户”） | EXTRA | §7.1:1523-1525；ADR-14:154 | `internal/store/schema.go:14-23`；`internal/domain/workspace.go` | 低 | 实现与设计一致；注意 `ticket.stage` 缓存列与“阶段不存字段”（design §4.2:281-324）相悖，属自加缓存（`store.go:390-409`），风险是时间相关阶段变陈旧。 |
| H02 | `goal` / `schedule` / `workflow` 独立包与表 | EXTRA | §5.9:1084-1103；§5.10:1103-1152 | `internal/goal/*`、`internal/schedule/*`、`internal/workflow/*`；`internal/store/schema_v3.go:32-85` | 中 | 能力存在且有测试；但设计说“schedule 是 Source kind”（§5.9:1094），实现是**直接 `eng.Receive` 造 Trigger**（`cmd/zeokeel/extras.go:96-137`），不走 sources.Kind、也不走 `Persistence`（见 O 与风险 1）。 |

---

## 3. 对官方 Super-agent 架构的符合性（架构/扩展交集）

> 需求来源：Lead 追加的《Super-agent 设计探索》官方文章（模块 §2 双层、§3 事件驱动、§5 计算/状态/环境分离、§6 watchdog、§8 开放问题）。design 文档引用了其中 Router/Worker 原文（§4.5:355-359）并把它扩成“官方两层 + 工作区执行作用域”（§4.5:361-380）。以下把文章要求映射为可检项；**本节裁决不计入 §2 的 69 项**，合计见 §1.1。

| ID | 官方文章要求（模块） | 裁决 | design:line | code evidence | 说明 |
| --- | --- | --- | --- | --- | --- |
| O-01 | §2 “常驻的 Router 承接所有入口的人类交互，维护全局上下文，本身不执行具体任务…结果由 Router 统一回复”；即**唯一持久的全局 LLM Session** | MISSING | §4.5:355-359（引文）、§5.1:968-999、§5.3:1027-1039、§3.3:221-259 | `internal/engine/router.go:45-120`（确定性召回 + `assessor.Assess` 一次调用）；`internal/assess/assess.go:50-90`、`internal/assess/eino.go:33-58`（无会话、单次 Generate）；`internal/runtime/reply.go:34-116`（回复由代码模板拼装） | 没有 Router 会话、没有全局上下文/记忆、没有 Router 工具集；Eval 是“每次 Trigger 一次结构化模型调用”。design §5.3:1029-1033 列的 orchestrator 工具（`read_domain`/`eval_read`/`eval_decide`/`route`/`reply`…）一个都不存在。 |
| O-02 | §2 Router“批处理”：一个回合内累积的 Trigger 合并评估（成本与一致性都依赖它） | MISSING | §4.5:381-395 | `internal/engine/engine.go:198-289`（`Receive` 内立即 decide）；`internal/engine/router.go:96-110`（每条 Trigger 调一次 assessor） | 无批处理/无 Eval 对象/无 deadline；每条消息一次模型调用，成本随消息线性增长，且同 Thread 的多条消息会各自产生 Decision。 |
| O-03 | §2 Worker：“上下文隔离、完整执行工具、专注单一任务、不直接对外说话，结果由 Router 统一回复” | PARTIAL | §4.5:361-380 | `internal/runtime/worker.go:194-341`（每 Ticket 一 workdir + 一 session）；`internal/runtime/artifacts.go:24-104`（report/present/ask）；`internal/engine/engine.go:832-860`（Report） | 上下文隔离/完整工具/不直接回人类 ✅（回复由 runtime 统一拼装，不在 Worker）；“结果回 Router”不成立——结果回 Engine + runtime 模板，没有 Router 参与判断或统一措辞；Worker 还直接 `ask_user_question` 到面板（对 operator，尚可）。 |
| O-04 | §3(a) 事件驱动 + **确定性 fast path + LLM slow path** | CONFORM | §4.6:502-515；§4.9:723-742 | `internal/engine/router.go:45-95`（Layer1 关键词/绑定工作区）；`internal/engine/engine.go:339-370`（gate 确定性）；`internal/brief/brief.go`（brief 召回） | 两层路由与 gate 都是确定性的；`local` direct-dispatch 直接跳过语义评估（router.go:73-85）。 |
| O-05 | §3(b) Thread 订阅快速路径：**后续事件直达订阅 Worker、绕过 Router；多对多；Worker 可 unsubscribe / hand back** | MISSING | §4.5:361-380、§4.9:759（“通知照样扇出给已订阅的 Ticket 作为上下文”） | `internal/engine/engine.go:291-321`（扇出只 `tk.ContextInput = append(...)`，`engine.go:312`）；`grep ContextInput` 全仓**无读取者**（除 `WaitsForPerson` 布尔）；无 subscribe/unsubscribe/handback 命令 | 订阅事实存在（Ticket.SubscribedThreads / thread.ticketIDs），但“直达 Worker 的输入通道”不存在：扇出文本被写进一个没人读的字段，随后仍走 Router 决策（代码推断：通常再开一张 Ticket）。`unsubscribe`/`handback`/`followForContext` 均无命令（007:81,89-91）。 |
| O-06 | §3(c) Inline & Spill + 链式召回：对象内联下一跳 ID，按需经工具展开（Trigger→Thread→Worker Session），Router 有 `workspace_list/read/propose`、`recent_tickets` | PARTIAL | §4.6:460-476、§4.5:413-417、§5.4:1039-1048 | Inline：`internal/brief/brief.go:40-75`（含 stale 提示 `workspace_read`）；Spill/recall：无（grep `workspace_read/workspace_list/recent_tickets/read_domain/eval_read` 仅出现在注释/提示词） | Inline 这一半做到了（brief 常驻 + stale 标记 + 路由候选召回）；Spill（按需展开）与 ID 链式召回完全没有工具支撑，Router 拿到的是候选 brief 摘要，无法顺着 Trigger→Thread→旧 Ticket 追下去。 |
| O-07 | §2 Router 用轻量模型、Worker 用最强模型（角色模型分离） | PARTIAL | §4.5:359、ADR-3:142 | `cmd/zeokeel/main.go:37,46,131`（`-model` 与 `-assess-model`）；`internal/assess/eino.go`（独立 provider） | 机制存在（两个独立模型配置）✅；但没有按角色的模型选择/预算/降级策略；`-assess-model` 为空时**退化为确定性 RuleAssessor**（`cmd/zeokeel/assess.go:27-40`），生产会静默变成“无 LLM 的 Router”。 |
| O-08 | §5 计算/状态/环境分离：无状态 agent loop + 会话状态持久化在 DB；执行环境留在本地 but behind a light daemon（BYOM） | DEVIATES-JUSTIFIED | §5.1:968-999（“CheckPoint/EventStore 自己落 SQLite”）、ADR-1:140、ADR-11:151、§8.1:1559-1569 | `internal/agent/agent.go:246-249`（eino `FileStore` JSONL 落 `ticket workdir/.zeokeel/sessions`）；`internal/runtime/worker.go:225-229`；`internal/agent/tools.go`（工具在进程内 `exec`） | 单二进制桌面（ADR-1）使“本地执行环境”正当；但**丢掉的具体要求**：(1) 会话状态不在 DB（是文件，且绑在 ticket workdir 里，删 worktree 即丢会话；design §5.1 自己承诺“落 SQLite”，`session_event` 表建而未用）；(2) 无 daemon 边界：agent loop、模型 client、工具执行、调度、UI 同进程，无法独立重启/升级/远程化执行环境；(3) 无 BYOM 的多 provider 注册/切换层（只有 OpenAI 兼容 base URL，满足 BYOM 的最小形态）。 |
| O-09 | §6 Watchdog：**独立的外部控制回路**（另一个 full-tool Session、定时驱动）监控基础设施与“模型判断的任务进展”，能在线修复，且**主服务宕机时仍在跑** | MISSING | §6.7:1363-1364（“进程内 watchdog”）、§8.4:1608（`zeokeel status` 0/1/2/3）、§11-5:1766 | 无 `status` 子命令（`cmd/zeokeel/main.go:31-53` flags 列表）；无 HTTP API（G01）；无 hourly check；`grep Watchdog/Supervisor/hourly/heartbeat` 为 **0**；`health` 表唯一写者是 effects（`internal/effects/effects.go:635`） | 三项全无：进程内 watchdog、外部 `status`+退出码、每小时 check。原型 006:133-138 明确要求“check 在**另一个 profile**，服务宕机时仍在跑”，这正是文章 §6 的独立回路；Go 侧连它的输入（status/health 契约）都没有。design §6.7/§8.4/§11 的实现状态表因而**是假的**（见附录 A）。 |
| O-10 | §6 watchdog 的输入契约：分层退出码（1 可重启、2/3 重启无用）+ 部件级 errors/setup 详情 | MISSING | §6.7:1363-1364；§8.4:1605-1612 | 无 exit-code 分层（仅 0/1，见 `cmd/zeokeel/pipeline.go:52-82`）；无 `errors`/`setup` 输出结构 | 与 O-09 同源：没有 `status` 就没有分层退出码与“哪个部件坏了”的结构化输出；watchdog 无法据此决策（006:266-268）。 |
| O-11 | §8 跨实例 A2A / 多租户 / 安全边界（文章明确列为**开放问题**） | NOT-REQUIRED | §1.3:65-73（不做多租户）、§12-8:1796（建议排除多 operator）、ADR-13:153、§5.5:1048-1058（“不是安全边界”） | — | 文章本身未给规范，且 design 明确不做；不计入缺口。唯一值得记录的是安全基调要诚实：`README` 已写明 shell 不是安全边界，与 §5.5 一致。 |

**官方架构小节结论**：官方文章的 5 个模块里，只有 §3(a) 的两级路由（快/慢路径）被较完整实现；§2 的 Router 持久会话与批处理、§3(b) 订阅直达、§3(c) 的 Spill/链式召回、§5 的“状态进 DB + daemon 边界”、§6 的外部 watchdog 均缺失或被产品形态正当替换（§5）。因此“Go 版 = 官方双层架构的移植”（design §4.5:361-380、§11:1839-1844）这一自我定位**不能成立**：现状更接近“确定性 intake + 单次 LLM 评估 + 每 Ticket 独立 worker + 代码模板回复”的简化形态。

---

## 4. 非 CONFORM 项详述

> 只对 MISSING / DEVIATES-* 与高严重度 PARTIAL 展开；每条给“需求 / Go 行为 / 后果 / 修复”。其余 PARTIAL 的差异已在 §2 说明列写清。

### 4.1 组合根、Setup 与生命周期

**A01/A03 无 stateDir 锁、无 ready、无环境恢复**
- 需求：007:31/33/35。
- Go：`store.Open` 直接开库（`store.go:43-65`）；单实例只在 UI 初始化时用 mygo 锁（`ui/app.go:183`），GUI 与 `-pipeline`/`-headless` 之间没有互斥。
- 后果：两进程同库各自持有内存 Engine → 后写覆盖、重复建 Ticket、effects 行被另一进程误删；崩溃恢复依赖“只有一个人写”的前提，而这个前提没有强制。
- 修复：`store.Open` 前 `flock(<stateDir>/lock)`（含 `--headless`/`--pipeline` 路径）；把“打开成功”作为唯一入口；补 `ready` 状态位给面板/健康。

**A04 Setup 缺失 + onboarding 不落盘**
- 需求：007:39（探测 CLI、写环境与 shim、保留 setup 问题）。
- Go：`runtime/onboarding.go` 只收集四类输入；`SaveOnboarding`（`onboarding.go:104-148`）只改内存 `r.settings` 并 `SetWorkerModel`，**不调用 `store.SaveSettings`**；`FinishOnboarding` 也只写内存 `onboard.done`。
- 后果：走完向导直接退出，重启后模型/base URL/api key 全部丢失（`main.go:112-126` 从 store 读不到），执行器回到未配置；`gh`/`lark` 登录态、工作目录、allowlist 的 setup 检查全无。
- 修复：onboarding 每一步落库（复用 `SaveSettings` 的 revision 乐观并发）；补 `SetupError`/`SetupProblem` 模型与探测（gh auth、目录可写、模型连通性）；补“改 setup 字段 → 重启并重读”的路径。

**A05 无 Passes / evaluateDue**
- 需求：007:40（每 2 秒一趟、due effects、evaluateDue）；007:93（deadline/暂停/三次 unavailable）。
- Go：`main.go:180-186` 的 2s ticker 只做 `rt.Publish()`；没有 due 扫描、没有 Thread 队列、没有超时。
- 后果：000-003 的 Eval deadline/暂停移动/过期重排全部没有承载者；面板的“评估中”永远不会超时。
- 修复：把 2s ticker 换成真正的 pass：先 `effects.Kick`，再 `engine.EvaluateDue(now)`（可复用 effects 的串行 dispatch 保证“一次一趟”）。

**A07 无 Notices / 主会话消息**
- 需求：007:42、007:109、007:583-594。
- Go：仅 `runtime.stream()` 投影。
- 后果：没有 steer/inject 通道，事件通知只能靠人看面板；G04 的消息契约无落点。
- 修复：若补 Router 会话（O-01），把它作为 notice/relay 的目标；否则明确文档化为“无主会话”的产品裁剪，并从 design 中删除对应承诺。

### 4.2 Engine 边界与并发

**B01/B10 入口双路径 + 并发上限失真**
- 需求：007:46（唯一写者）、007:203（slot 预留、人 +1、wake 优先）。
- Go：`cmd/github.go:70` 与 `cmd/extras.go:125` 直接 `eng.Receive`；`EnableDurable` 的 launch handler（`durable.go:30-45`）调用 `runTicketOnce` → `runTicket` 内 `go func`（`worker.go:254-255`）后立即返回，effect 完成、槽位释放，而 worker goroutine 继续跑。
- 后果：GitHub/定时触发的工单不落库、不产生 launch effect、**永远不会被执行**（durable 模式）；面板“执行器 N/M 在跑”实为“StageWorking 的工单数”，可超过上限；人的消息没有 +1 槽、也没有 wake 优先。
- 修复：(1) 所有入口只经一个 `Runtime.Receive/Poll` 门面，内部用 `Persistence.Receive`；(2) launch handler **阻塞到 worker 结束**（或显式 slot 信号量 + `effects.Later`），让 `Limit` 真正约束并发；(3) 人消息走独立配额并优先唤醒。

**B04/B05 无 part、无角色 Pick、无 Eval 队列**
- 需求：007:52-64、007:68。
- Go：单文件 Engine，全部方法对任何调用方开放；`Receive` 内同步决策。
- 后果：授权/批处理/超时/挂起等规则没有独立可测单元；`EngineForSources` 式的“Source 只能 receive/bind”边界形同虚设（`github` 拿到的是完整 Engine）。
- 修复：抽 `ports`（Repositories/Policy/SourceScope/Executor/Outbox）与 5 个 part；对外只暴露角色接口（可先用 `interface{ Receive(...) }` 缩窄）。

### 4.3 Store / Schema

**C01 目录锁**：见 A01。

**C08/C10 Schema 差异**（完整表见 §5）：最 load-bearing 的缺失是
- `trigger_thread` 倒排（时间线分页 + `<at>|<id>` cursor 防同秒跳过）；
- `decision.thread`（面板/Eval 按 Thread 列 Decision）；
- `outbox.dropped`（丢弃时间与原因码，供 `GET sources` / 面板审计）；
- `thread.active/subscribed`、`ticket.active` 生成列（pass 只扫活跃集）；
- `assignment` 独立表（design §7.1 承诺，实际嵌在 `ticket.json`）；
- `session_event` 建而无用。
- 修复优先级：`trigger_thread` 与 `outbox.dropped` 高（直接影响 007:458/463 与面板契约），其余中。

**C10 v3 旁路**：`SchemaV3` 不在 `migrations`，`EnsureV3` 用 `kv('schema.schedule_goal')='3'`（`schema_v3.go:92-133`），且测试断言全局版本不动（`schema_v3_test.go:41-44`）。后果：降级不拒绝、旧 build 静默停掉定时/目标；备份/恢复按“schema=2”理解会漏两张表。修复：v3 进入有序 migration 并递增 `SchemaVersion`（需要同步更新 pin 住版本的测试）。

**C11 时间形式**：`store.go:361` 写 `RFC3339Nano`，`schema.go:31` 注释称“sorts lexicographically”（错）；`store.go:117-119` 已解释为什么必须定宽。修复：所有持久化时间统一 `FormatTime`（定宽 UTC），或把比较全部移到 Go 侧。

### 4.4 Effects / Outbox

**D07 Withdraw 删除回执**
- 需求：007:463（回执永久保留、ID 永不二次入队）、design §7.2:1543。
- Go：`outbox.go:230-251` `DELETE FROM outbox WHERE key=? AND state='pending'`，注释承认“同一 ID 之后可以再记录”。
- 后果：撤回后同业务 ID 的再次发送会重复外发（尤其在 worker 重试/重放路径）；与 `settle` 的“in-flight 撤回也要重建成 settled”自相矛盾（`outbox.go:322-354`）。
- 修复：撤回改为 `state='withdrawn'`（或 `dropped` + `reason='withdrawn'`），保留 key；`Send` 对已存在非 pending 回执返回 `created=false`。

**D05/D06 kind 覆盖不全**：补 `delivery`/`relay`/`notice`/`assessment` 前，先明确是否有 Router/Worker 输入通道（E03）；没有通道时这些 kind 无意义，应在文档中裁剪而不是留“设计已实现”的印象。

### 4.5 Sessions / events / Executor

**E02/E06 + B11 崩溃恢复只做了一半**
- 需求：007:92/207（interrupted assignment 以 `hostRestarted` 结束并新建；启动 resume 报告）、design §5.8:1074-1084。
- Go：`Runner.Resume` + `patchInterruptedToolCalls` 实现完整（`agent.go:308-333`、`session.go:46-87`），但**生产代码从不调用**（唯一调用者是测试）；启动没有任何“running assignment”扫描；`launch` effect 在 worker 刚起 goroutine 时就被删除。
- 后果：kill -9 后工单永久 `working`，无 recovery note、不续跑；面板与真实状态脱节；CHANGELOG“重启接着跑”只对“已接受未启动”成立。
- 修复：`launch` effect 覆盖整个 worker 生命周期（或启动时扫 `Assignments[last].State==running`），对每张此类工单：`Resume(sessionID)`（带 recovery note），无日志则新建 Assignment；`hostRestarted` 记录到 Assignment；oldest first。

**E01/E03 无 Router/Worker 会话语义**：需要先决定是否补 Router 会话（O-01）。若不补，则 design §5.2 的 steer/inject/已读回执/回合结束、§5.3 的角色工具白名单都应从设计里降级为“未实现”，而不是保持 ✅。

### 4.6 Sources 与扩展

**F03 假 `sent`**
- 需求：007:191（未安装 kind 返回 `waiting`）、008:178-179（Source off 时等待）。
- Go：`sender.go:40-48` 无 driver 直接 `OutcomeSent`。
- 后果：飞书/钉钉（未接线）消息永久标记 sent；`outbox` 的失败/重试/健康全部失效，用户以为已回复。
- 修复：无 driver → 返回 `effects.Later`/`OutcomeWaiting`（新增状态）；面板显示“来源未安装”；`Withdraw`/`settle` 语义同步。

**F04/F12 扩展路径不可执行**（详见 §6）。

**F07 游标不持久化**（详见 §7 风险 2）。

### 4.7 面板 / 模型面 / 配置

**G02 无分页/无 read resources**：面板全量渲染 + 模型无 domain 读取工具，直接导致 O-06 的 Spill/链式召回无法实现；也违反 007:556-577 的页大小契约。修复：先做 3 个 read 工具（`ticket/<id>`、`thread/<key>`、`recent_tickets`）与统一 `{items,next_offset}` 投影，再补模型可见的 ID 链。

**G07/G08 依赖与单写者**：`engine` import 具体 `effects/outbox/store`，`internal/policy`/`internal/executor` 包不存在；`health` 只有 effects 一个写者。修复：把 persist.go 挪到 `internal/runtime`（组合根）或定义 ports；健康表由各部件 claim（与 007:269 对齐）。

### 4.8 官方架构缺口的落地顺序（与 007 修复的交集）

1. 先修“入口唯一 + 持久化”（B01/F07/B10）——否则 Router/订阅/watchdog 都建在流沙上。
2. 再补“Router 会话 + 批处理”（O-01/O-02）与“会话事件/steering”（E01/E03）——两者互为前提（Router 的通知通道就是 steering）。
3. 然后补“订阅直达 Worker”（O-05/E03）与“read/Spill 工具”（O-06/G02）。
4. 最后补“外部 watchdog”（O-09/O-10）：`status` 命令/HTTP 是它的输入契约，必须先有健康与错误分层。

---

## 5. Schema 对照（prototype `_ref/src/app/store.ts` vs `internal/store`）

### 5.1 表级 diff

| prototype（`store.ts:72-86`，schema 11） | Go（`schema.go:25-113`，v2；v3 旁路） | 裁决 |
| --- | --- | --- |
| `trigger(id, value, at)` + `trigger_at` | `trigger(id, scope, workspace_id, default_workspace, source, at, human, json)` + `trigger_by_scope_at` / `trigger_by_ws_at` | 有，**列扩展**（workspace 作用域） |
| `disposition(id, value)` | `disposition(trigger_id, kind, reason, ticket_ids)` | 有；**丢字段**：Thread 列表（`domain.Disposition.Thread` 未持久化）、requeue/handback/acked 去向 |
| `thread(key, value)` + 生成列 `active`/`subscribed` | `thread(key, workspace_id, source, last_at, json)`；json 只有 `{triggers,tickets}` | **缺生成列**；**缺 meta/subscriptions/pending/evaluating/expired** |
| `ticket(id, value)` + 生成列 `open`/`active` | `ticket(id, workspace_id, stage, ended_at, created_at, json)` + `ticket_open` 局部索引 | 有；`open` 用局部索引等价；**缺 `active`**；`stage` 是**反设计**的派生缓存列（design §4.2） |
| `decision(id, thread, value)` + `decision_thread` | `decision(id, workspace_id, kind, json)` | **缺 `thread` 列**（无法按 Thread 列出 Decision） |
| `outbox(id, value)` + 生成列 `dropped`/`pending`（`pending` 是 `ticketId + channel.thread` 复合） | `outbox(key, workspace_id, ticket_id, channel, state, text, created_at, conversation, thread, reply_to, kind, updated_at)` + `outbox_pending`/`outbox_order` | 有；**缺 `dropped`（时间/原因）**；`state` 取值 `pending/sent/gone`（原型 `pending/sent/dropped`，008:178 要求“retained as dropped”） |
| `trigger_thread(thread, id, at, human)` + `trigger_thread_at` | **无** | **缺失（load-bearing）**：时间线分页/`<at>|<id>` cursor/同秒不丢在原型就靠它 |
| `effect(kind, key, value, seq, generation, attempts, error, due INTEGER, since)` + `effect_due` | `effect(kind, key, seq, generation, attempts, due TEXT, error, since, payload)` + `effect_by_seq`/`effect_by_due` | 有；语义等价，`due` 用定宽文本（正确） |
| `kv(key, value)` | `kv(k, v)` | 有；**缺 typed registry**（007:468；原型 `kv.ts:19-56`） |
| — | `workspace(id,name,root,remote,kind,brief,policy,created_at,updated_at)` | EXTRA（design 多工作区） |
| — | `health(entry,state,detail,at)` | EXTRA；写入者只有 effects（`effects.go:635`） |
| — | `session_event(session_id,seq,at,kind,payload)` | EXTRA；**无读写者**（会话在 eino JSONL） |
| — | `schedule/schedule_fire/goal`（v3 旁路） | EXTRA；不在 `SchemaVersion`/`migrations`（C10） |

### 5.2 语义差异（load-bearing 排序）

1. **`trigger_thread` 缺失（高）**：007:458 要求“a page of a Thread's timeline continues from a `<at>|<id>` cursor, and Triggers that share a time are not skipped”；Go 面板 `threadsVM` 每次全量 `eng.Feed()` 后内存分组排序（`runtime/pages.go:96-166`），无 cursor、无同秒 tie-break（`Feed` 只按 `At` 排序，`engine.go:796-798`）。
2. **`outbox.dropped` 缺失（高）**：007:463 与 006:173 的 `GET sources` 都要求 dropped 的 `at` 与 `reason`（`channelGone`/`channelIgnored`）；Go 只有 `state='gone'`，没有时间/原因，面板也没有失败/丢弃回执视图。
3. **`decision.thread` 缺失（中）**：面板“评估”页只能从内存 Feed 反推（`runtime/pages.go:168-206`），与“Store 按 Thread 查询”的契约不同；重启后无法按 Thread 重建历史视图。
4. **`outbox` 状态名与撤回语义（中）**：见 D07；`Withdraw` 删除回执是“永久保留”的直接反例。
5. **`ticket.stage` 缓存列（低-中）**：`domain.StageOf` 是唯一真源，但 `stage` 列在 `PutTicket` 时计算（`store.go:392-409`）；时间/暂停相关阶段会陈旧，且与 design §4.2“不存状态字段”自相矛盾。
6. **`at` 非定宽（中）**：见 C11。
7. **`session_event` 死表（低）**：建表成本 + 误解风险（看起来像“会话在 DB”）。

### 5.3 结论

“同构 schema”（design §7.1:1523）只对**聚合 JSON + effect/outbox/kv** 成立；原型用来支撑**时间线、按 Thread 查询、丢弃审计、活跃集扫描**的 4 个结构（`trigger_thread`、`decision.thread`、`outbox.dropped`、生成列 active/subscribed）缺失。它们不是“实现细节”，而是 007 明确点名的读路径与审计契约；建议在补 Router/订阅之前先补这些，否则面板与审计会继续依赖内存投影。

---

## 6. 扩展路径裁决：今天能不能加一个 Source？

**裁决：机制有一半，路径不可用（MISSING，§2 F12/F04/F09/F10/F08）。**

### 6.1 现状

- 接口：`sources.Kind{Name, Scope, Translate, Channel}` + 可选 `Poller/MemberLister/HealthReporter`（`internal/sources/source.go:55-198`）。这是好的最小内核：fake 只要四行、可选能力用 type assertion（符合 design §1.5 “interface + optional”）。
- 注册：`init()` + `sources.Register`（`github/config.go:135-145`），空名/重名 panic；`sources.New(name, configJSON)` 可按配置构造（`registry.go:85-91`）。
- 样板：GitHub 实现完整（翻译/scope/游标纪律/幂等评论/health/55 个测试），可作为复制模板。
- 但“从注册到能工作”的其余环节**都不存在**：
  - 无 Source 实例存储：没有 `source:<kind>` 设置块、没有从 store 加载实例；GitHub 只由 `-repo/-gh-login` 构造（`cmd/zeokeel/github.go:30-45`），onboarding 的 `source_kind/source_repo` 无人读；
  - 无面板表单/开关：来源页只读（`ui/source.go:12-48`），`SettingsSource` 只有 `poll_interval` 一项（`runtime/pages.go:258-262`）；
  - 无 host 对象：Source 拿不到 threads/tickets/state/notify/report（A08），因此**游标只能写在内存**（F07）；
  - 无 admission/policy part、无环境变量、无 `find`/`threadContext`；
  - 无扩展文档、无 docs.spec、无“模块/测试表”索引（F12）。

### 6.2 008 checklist 逐条

| 008 步骤 | Go 对应 | 裁决 |
| --- | --- | --- |
| 1 映射 Trigger/Thread/actor/Channel/scope | 有（`domain.Trigger/ThreadRef/ChannelRef`，github 模板） | ✅ |
| 2 kind：schema/fields/health/facts | 无（只有 Config struct + 手写校验） | ❌ |
| 3 翻译 + 自身消息丢弃 | 有（`github/trigger.go`） | ✅ |
| 4 `scope(meta, config)` 仅依赖 meta+配置 | 有（`github/scope.go:17-36`），但**没有 meta**（读 `Channel.Address`）；revalidate 无调用者 | PARTIAL |
| 5 Channel `send` 一次一条、仅永久失败 `gone` | 有（`github/channel.go:43-90`）；但 sender 层“无 driver 假 sent”破坏语义 | PARTIAL |
| 6 `threadContext` | 无 | ❌ |
| 7 Policy part（allowed/rows/bots） | 只有 workspace 级 `Allowed` map；无 rows/bots/sync | PARTIAL |
| 8 Executor 环境变量 | 无 | ❌ |
| 9 插件入口 + 循环（ready/开关/游标/逐条容错/健康/abort） | 无插件；cmd 手写循环缺 ready/开关/abort；游标不持久化 | PARTIAL |
| 10 构建入口/导出/patch/文档表 | 无插件构建/导出；无文档表/docs.spec | ❌ |
| 11 测试脚手架 | 包内 fake `Command` 测试 ✅；无 `sourceContextOf` 式公共脚手架 | PARTIAL |

**最小修复集（让“加一个 Source”可用）**：
1. `SourceSpec` 元数据（title/description/config schema/fields/health/facts）+ `internal/sourcecfg` 从 settings 加载/校验实例；
2. 一个 `Host` 接口（`Receive/State/Threads/Tickets/Notify/Report/Commands`），由 runtime 注入；游标经 KV 持久化（先做 `state` API）；
3. sender 的 `waiting` 语义 + `allowed(address)` 校验；
4. 面板：来源页“添加/开关/编辑”（按 schema 生成表单）；
5. 文档：`design/sources.md`（008 的 11 步 Go 版）+ 一条 `go test` 脚手架 + 在 design/README 索引里登记。

---

## 7. 优先级风险清单（架构一致性）

| # | 风险 | 证据 | 触发条件 | 后果 | 修复 |
| --- | --- | --- | --- | --- | --- |
| **1** | **外部入口绕过持久化**：GitHub 轮询与定时任务直接调裸 `engine.Receive` | `cmd/zeokeel/github.go:70`、`cmd/zeokeel/extras.go:125`、`cmd/zeokeel/main.go:219,235` vs `internal/engine/persist.go:196` | 默认 durable 模式下 `-repo` 收到 @ 或定时触发 | Trigger/Ticket 不入库、无 `launch` effect、**永不执行**；重启即丢；面板显示“待执行”假象 | 全部入口收敛到 `Runtime.Receive`（内部走 `Persistence`）；加 e2e 测试“durable + fake source → Ticket 落库并执行” |
| **2** | **游标不持久化** | `cmd/zeokeel/github.go:58,68,91`（局部变量） | 进程重启/崩溃 | 重启后 `since=now`，**停机期间事件静默丢失**（恰好是最该补的一段） | 游标写 KV（`github-cursor:<repo>`），只在整轮成功后 `Set`；启动读回；补“重启接续”测试 |
| **3** | **运行中 Assignment 无恢复** | 无 `hostRestarted` 扫描；`agent.Runner.Resume` 无生产调用者（`agent.go:308`） | kill -9/崩溃在 worker 运行期间 | 工单永久 `working`、会话日志永不续跑；无人值守可靠性承诺失效 | 启动扫描 `running` 的 Assignment → `Resume`；无日志则新建并记 `hostRestarted`；oldest first |
| **4** | **假 `sent` / IM 未接线** | `cmd/zeokeel/sender.go:40-48`；`cmd/zeokeel/extras.go:157`（无调用者） | 飞书/钉钉来源或任何未注册 kind | 外发回执永久假成功、消息丢失且无重试；README 的头号场景（钉钉派活）实际收不到消息 | 无 driver → `waiting`/`Later`；接线 `startIMLoop` 并提供来源配置/开关 |
| **5** | **schema/时间/并发基础不稳**：v3 旁路、`at` 非定宽、无目录锁、store 单连接 SAVEPOINT 假设被多 goroutine 打破 | `schema_v3.go:92-133`、`store.go:361`、`store.go:185-191`（注释“one command goroutine”）vs `runtime/reply.go:109`/`worker.go:249` 从 worker goroutine 写库 | 升级/降级、跨秒排序、双进程、worker 与 UI 并发提交 | 降级静默停定时；时间线错序；双进程丢失更新；跨协程 SAVEPOINT 可能把 A 的命令纳入 B 的事务（回滚即静默丢失已“成功”的保存；**代码推断，未故障复现**） | v3 进 `SchemaVersion`；时间统一 `FormatTime`；加 flock；store 提供 `Command` 串行入口（channel/单 goroutine）或禁止 worker 直接 `Atomic` |
| **6** | **扩展路径断裂** | §2 F03/F04/F07/F12；§6 | 任何“新增 Source/接入新 IM”的想法 | 只能改 `cmd/zeokeel` + 自造游标 + 无面板/文档；008 的“11 步”不可执行 | 见 §6.2 最小修复集 |
| **7** | **官方架构三条主线缺失**：Router 会话、订阅直达、外部 watchdog | §3 O-01/O-02/O-05/O-09/O-10 | 产品宣称“双层架构/无人值守自愈” | 与文章/原型的能力叙事不符；任务批处理成本高、follow-up 无直达、无人自愈 | 见 §4.8 顺序 |
| **8** | **组合根硬编码 demo 工作区** | `cmd/zeokeel/main.go:168-172`（非空库也进 `registerWorkspaces`）、`main.go:252-288` | 完成 onboarding 后再次启动 | 每次启动把 `ws_web`/`ws_acme_data`（假 root、假关键词）注入真实库并参与路由 | 只在该库“未完成 onboarding”时注册演示数据；生产路径从 settings/workspaces 读 |
| **9** | **归档/保留未实现** | design §7.3:1546-1557 承诺；无 archive/VACUUM 代码 | 长期运行 | 库无限增长（原型已知坑被保留）；备份/迁移变慢 | 至少实现 outbox 回执压缩 + 180 天 Ticket 归档；`storage` 卡片给真实库大小（已有 `runtime/storage.go:33-60`） |
| **10** | **向导配置不落盘** | `runtime/onboarding.go:104-148`（无 `SaveSettings`） | 向导完成即退出 | 重启后模型/API key 丢失、执行器未配置 | onboarding 每步落库；加“重启后仍可用”的 acceptance 测试 |

---

## 8. 覆盖说明

### 8.1 已读文档

- `_ref/docs/007-architecture.md` 全部 617 行（逐节抽取需求，含 Runtime/Engine/Model/Policy/Store/Effects/Sources/Executor/Assessor/Settings/Prompts/Sessions/Glue/依赖规则/模块表/测试表/Persistence/Model-facing/Configuration reload/Open questions）。
- `_ref/docs/008-cookbook.md` 全部 425 行（逐步骤 + checklist）。
- `design/zeokeel-go-mygo-design.md`：§0、§1.3-1.5、§2、§3.1-3.3、§4.5-4.6、§5.1-5.10、§6.7、§7.1-7.3、§8.1-8.4、§9.1-9.4、§10、§11（风险表与实现状态表）、附录 A/B。
- `README.md`、`CHANGELOG.md`、`design/M0-tasks.md`（Source/面板/接线相关行）、`.github/workflows/ci.yml`、`Makefile`、`scripts/check-nocgo.sh`、`scripts/release-check.sh`（部分）、`_ref/src/app/store.ts`、`_ref/src/kv.ts`、`_ref/src/app/migration.ts`（DDL 与 KV 键）、`research/reference-analysis.md`（grep 相关条目）。

### 8.2 已读代码（关键文件）

- sources：`source.go`、`registry.go`、`github/{config,scope,poll,channel,trigger,thread}.go`
- im：`im.go`、`addressing.go`、`retry.go`、`status.go`、`poll.go`、`feishu/mapping.go`（部分）
- store：`store.go`、`schema.go`、`schema_v3.go`、`schema_v3_test.go`、`load.go`、`settings.go`
- effects/outbox：`effects/effects.go`、`outbox/outbox.go`
- engine：`engine.go`、`router.go`、`persist.go`
- domain：`ticket.go`、`trigger.go`（`workspace.go`、`ids.go` 部分）
- agent：`agent.go`、`session.go`、`recorder.go`、`tools.go`（部分）
- runtime：`runtime.go`、`worker.go`、`durable.go`、`reply.go`、`pages.go`、`storage.go`、`prompts.go`、`onboarding.go`、`artifacts.go`
- cmd：`main.go`、`github.go`、`extras.go`、`durable.go`、`sender.go`、`assess.go`、`pipeline.go`
- 其他：`internal/prompts/prompts.go`、`internal/skills/skills.go`、`internal/mcp/mcp.go`、`internal/view/{search,reason}.go`、`internal/ui/{app,input,source}.go`（部分）、`internal/assess/{assess,eino}.go`
- 测试抽样：`cmd/zeokeel/recovery_test.go`、`cmd/zeokeel/e2e_test.go`、`internal/runtime/durable_test.go`、`internal/store/store_test.go`、`internal/store/schema_v3_test.go`、`internal/agent/session_test.go`
- 实测：`CGO_ENABLED=0 go build ./...` 通过；`CGO_ENABLED=0 go test ./... -count=1` 全绿（24 个包）；`CGO_ENABLED=0 go test -race ./internal/domain` → `go: -race requires cgo`（⇒ ci.yml 的 test 步骤按当前写法不可能绿）。

### 8.3 不可验证项

- 真实 GitHub/Feishu/DingTalk 网络与凭据路径（仓库内只有 fake transport 测试；README 也承认“真实凭据下的端到端尚未验证”）。
- GUI 真实渲染/托盘/关窗行为（无显示器；`ui.NewTester` 只验证无头逻辑，未在本审计中运行 E2E）。
- 原型代码的所有行为（只按文档 + `store.ts/kv.ts` 核对；007/008 的描述被当作需求）。
- “历史提交是否曾经绿过 CI”（只看当前 `ci.yml` 与本地工具链能力）。
- 模型真实行为（assessor 的判定质量、Worker 工具成功率）——不属于架构一致性。
- 生产事故的真实频率（推断自代码路径，未做故障注入；仓库内 `recovery_test.go` 只覆盖“已接受未启动”）。

---

## 附录 A：design §11 实现状态表逐行核对（1833-1861）

| 行 | 声明 | 核对结果 | 证据 |
| --- | --- | --- | --- |
| 1839 | §3.1 进程模型 ✅ | 部分：单进程 UI+core ✅；但“无目录锁/无 ready/`-headless` 实为 demo” | A01-A03；`main.go:64-67`（`-headless` → `runDemo`）；`store.go:43-65` |
| 1840 | §3.2 分层与依赖规则 ✅ | 部分：单向依赖 ✅；但 `internal/policy`/`internal/executor` 不存在、engine 直连 effects/outbox/store、无 `EngineFor*` | G07；`go list` 依赖实测；`engine/persist.go:20-24` |
| 1846 | §4.8 IM 抽象 ✅（HTTP 长轮询兜底） | **不成立**：驱动在，但生产无调用者 | `cmd/zeokeel/extras.go:157`（`startIMLoop` 未被引用）；`grep` 仅测试 |
| 1850 | §5.2 会话与轮次 ✅（`adk.Runner`+FileStore+`Resume`） | 部分：Runner/FileStore ✅；**`Resume` 生产零调用** | `agent/agent.go:308`；全文 grep 仅 `_test` |
| 1854 | §5.7 可观测性 ✅ | 部分：面板有内存 transcript；无持久 transcript 读取路径（重启即空） | `runtime/worker.go:118-172`；`runtime/runtime.go:201-274` |
| 1855 | §5.8 崩溃恢复 ✅ | 部分：durable effects ✅；运行中 Assignment 不恢复、Resume 未接线 | B11；E02 |
| 1856 | §5.10 能力矩阵 ✅ | 部分：MCP/skill/schedule/goal ✅；**web 工具缺失**、workflow 仅 `-pipeline` CLI、无 `ask`→Attention 的原生对话框（有 ask 工具但走面板待办） | `internal/agent/tools.go` 工具清单；`cmd/zeokeel/pipeline.go` |
| 1858 | §7 持久化与 Effects/Outbox ✅（v1–v3） | 部分：v3 不在 schema 版本；归档未做；`session_event` 死表 | C08/C10；§5 |
| 1859 | §9 零 cgo ✅ | 守卫脚本成立；但 CI 的 `-race` 步骤与 `CGO_ENABLED=0` 冲突，CI 按当前写法是红 | `scripts/check-nocgo.sh`；`.github/workflows/ci.yml:35`；本机实测 |
| 1363-1364（§6.7）/1608（§8.4） | 进程内 watchdog、`zeokeel status` 0/1/2/3、flock | **全部未实现** | O-09/O-10；`cmd/zeokeel/main.go:31-53`（无 status 子命令） |
| 1370（§6.7）/1766（风险 5） | 休眠唤醒补 pass、watchdog | **未实现**（无 pass、无 wake 监听） | `grep wake/suspend` 为 0 |
| §7.3:1546-1557 | 归档/保留/VACUUM | **未实现** | `grep archive/VACUUM` 为 0 |
| §7.1:1523-1541 | schema 表清单（含 `assignment`、`trigger_thread`、`outbox.dropped`、settings/prompt 版本 KV） | 与实现不符 | §5.1 |
| §1.5:122 | “Go 用 5 个小 interface（`EngineFor*`）” | **未实现** | grep `EngineFor` 为 0 |
| §5.2:999-1027 | 两级投递/已读回执/回合结束/崩溃重放 | **未实现** | E01/E03 |

## 附录 B：008 的 11 步对 Go 的映射（速查）

| 008 步骤 | Go 对应物 | 状态 |
| --- | --- | --- |
| 1 映射领域概念 | `domain.Trigger/ThreadRef`；github 模板 | ✅ |
| 2 kind 元数据（schema/fields/health/facts） | 无；`sources.Kind` 四方法 | ❌ |
| 3 翻译 + 自我过滤 | `github/trigger.go` + `poll.go` | ✅ |
| 4 `scope` 只依赖 meta+config | `github/scope.go`（无 meta；revalidate 无调用者） | PARTIAL |
| 5 Channel（幂等/`gone`/allowed） | `github/channel.go`；sender 假 sent | PARTIAL |
| 6 `threadContext` | 无 | ❌ |
| 7 Policy part | workspace `Allowed` map | PARTIAL |
| 8 环境变量 | 无 | ❌ |
| 9 插件 + 循环（ready/开关/游标/健康/abort） | cmd 手写循环；游标内存 | PARTIAL |
| 10 构建/导出/patch/文档表 | 无 | ❌ |
| 11 测试脚手架 | 包内 fake 测试 ✅；无公共脚手架 | PARTIAL |

---

*审计完成时间：2026-11-03。所有代码行号基于本次只读快照（`git status` 干净，未做任何修改）。*
