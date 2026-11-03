# 一致性审计：原型文档 000–003 ↔ 设计 §4 ↔ Go 实现

> 审计对象：`/data/ZeoKeel-Super-Agent`（下称"本项目"）
> 需求基准：`_ref/docs/000-philosophy.md`、`001-domain.md`、`002-authorization.md`、`003-execution.md`（原型 commit `6337fc8`）
> 设计基准：`design/zeokeel-go-mygo-design.md`（1861 行）
> 代码基准：`internal/{domain,engine,assess,runtime,store,goal,schedule,outbox,effects,prompts}`，以及 `cmd/zeokeel/*`
> 方法：先完整读四篇文档抽取规范性条目，再逐条读设计、读代码。**注意设计 §11 自己写明"有冲突时以代码为准"（design:1835）**，本文所有"实现证据"均为直接读过的代码，未读不写。推断处显式标注"（推断）"。
> 只读审计：未修改任何源码，只新建本文件。

---

## 1. 判定计数与总体结论

### 1.1 计数

| 文档 | 条目数 | CONFORM | PARTIAL | MISSING | DEVIATES-JUSTIFIED | DEVIATES-UNJUSTIFIED | EXTRA |
| --- | --- | --- | --- | --- | --- | --- | --- |
| 000 philosophy | 7 | 1 | 5 | 0 | 0 | 1 | （见 §4） |
| 001 domain | 20 | 5 | 9 | 5 | 0 | 1 | （见 §4） |
| 002 authorization | 23 | 1 | 6 | 16 | 0 | 0 | （见 §4） |
| 003 execution | 18 | 0 | 10 | 8 | 0 | 0 | （见 §4） |
| **合计** | **68** | **7** | **30** | **29** | **0** | **2** | — |

（同源机制合并为一行时保留各条 ID 并分别给出判定，详见行内备注；EXTRA 是独立清单，不占这 68 行。）

### 1.2 一句话结论

**设计文档在"意图层"基本忠实于 000–003（§1.5、§4.1–§4.4、附录 A 把原型最值钱的确定性机制都点名了），但"实现层"远未达到它自己 §11 的 ✅ 声明：001 的 Eval/Thread 聚合、002 的确定性 strength、admission table、Mandate 不可变与 revalidate、follow-up 归属（`followupOwners`/`letGo`）、003 的 Ticket 结束/PR `follows`/投递三段式/并发 slot/retry/pause/prepare 全部缺失或只剩空壳；同时设计自己新增了 Proposal（`Decision{kind:propose}`）+ 审批关卡、ReplyPlan/ReplyMode、Workspace/Brief、Goal 实体等原型没有的概念。** 最强烈的一条偏差是：原型把"谁可以让我干活"做成**确定性 gate + admission table + accept mode**，而本项目把它换成了**模型自己声明 strength（prompt 文本：`"explicit" when the person plainly asked for it`）＋把所有同事请求变成待批单**。结果是 002 的 explicit 语义、observe/auto 安全阀、001 的"Only Eval creates one"同时失效，模型输出（可被外部文本影响）成了权限判断的一部分——这正撞在 002 与设计 §1.5 自己写的"prompt is not permission"上。

### 1.2.1 加入第二需求源（官方架构）后的补充结论

把《Super-agent 设计探索》的 13 条规范条目一并计入后，结论更尖锐：**本项目实现的是"Worker + 领域状态机"，而不是官方的"Router/Worker 双层"**。13 条中 CONFORM 1（Event = 半结构化事实）、PARTIAL 8、MISSING 4（全局单会话 Router、高效并行、Thread→Worker 的订阅直达、异步 ReAct）。设计文档 §4.5 用"官方双层 1:1"（ADR-3:142）为架构背书，但代码里 Router 只是一次性 `Assessor.Assess` 调用（`engine/router.go:15-17`、`cmd/zeokeel/assess.go:23-42`），既无持久会话、无全局记忆、无回合合并，也没有统一回复角色；`Engine.Receive` 还在全局锁内调用真实模型（`engine.go:152-154`、`router.go:95-97`），`ui/input.go:36-43` 又从 UI 主线程同步调用它 → "高效"与 003 的并发 slot 同时不成立。本节的缺失不改变 §2 的逐条判定，但把"最强的两条偏差"扩为三条：**授权被模型接管（R1）→ 审批关卡取代 accept mode（R2）→ Router 层整体缺失（R11/R12/R13）**。

### 1.3 最强烈偏差（按后果排序的摘要）

1. **授权语义被模型接管**：`assess` 的 `strength` 来自模型 JSON（`internal/prompts/defaults.go:74`），`engine.Receive` 用它决定是否走 accept mode 安全阀（`internal/engine/engine.go:216-221`）；gate 只返回 `ok bool`，不产出 strength（`internal/engine/engine.go:338-369`）。违反 002:11/13/50-54 与设计 §4.3:328。
2. **所有同事请求被强制待批**：`!t.Self && t.Source != "local"` 的 accept 一律降级为 `DecPropose`（`internal/engine/engine.go:240-244`），`Approve` 才创建 Ticket/Mandate（`internal/engine/engine.go:636-679`）。这使 `auto` 模式永远无法自主开单（测试自己承认：`internal/engine/engine_test.go:476-500` 注释 "auto mode only changes the accept-mode gate, not the approval requirement"），并违反 002:101"Explicit requests work in every mode"。
3. **Ticket 永远不会因 ready 而结束**：`Engine.Report` 只在 `dropped` 时写 `Ended`（`internal/engine/engine.go:855-857`），`ready` 只结束 Assignment 并停在 `person/review`（`internal/engine/engine.go:851-854`）；全仓库没有第二种结束路径。违反 003:94，且 `Ended.Outcome` 的 `completed/merged/closed/stopped` 四个值（`internal/domain/ticket.go:246`）是死值 → 面板永远显示"等待你评审"（stuck ticket）。
4. **fan-out 与 Eval 双轨**：订阅扇出把消息文本塞进 `Ticket.ContextInput`（`internal/engine/engine.go:312`）后，同一条消息**继续**走 gate→assess→Decision（`internal/engine/engine.go:167-211`）。违反 001:37"a person's message that reaches a Ticket this way takes no Eval"、002:22，并用 001 "Rejected concepts"里的 "append requires no Eval"（001:53）守护的东西重新漏了回来 → 重复评论/重复 Ticket 风险。
5. **`ContextInput` 从不投递给 Worker**：Worker 只拿 `Origin` Trigger 的原文（`internal/runtime/worker.go:476-484` `taskText`），`runTicket` 单次 `RunSession`（`internal/runtime/worker.go:276-290`），没有 inbox/steer/inject/turnEnded。001:37 的"Why it exists"（"Later discussion reaches the Executor without anyone mentioning the agent again"）与 003:112 的已读语义完全落空。

---

## 2. 需求矩阵

> 判定口径：CONFORM＝原型语义在代码里可指认；PARTIAL＝机制存在但语义/覆盖面不足；MISSING＝原型要求的行为在设计与代码中都找不到；DEVIATES-JUSTIFIED＝有意偏离且有设计论证；DEVIATES-UNJUSTIFIED＝有意偏离且无论证或与 000/001 冲突；EXTRA＝原型没有的新概念/行为（见 §4）。
> 严重度：S1 = 会产生重复评论/PR、越权、丢失 resume、卡死 Ticket、人消息被饿死；S2 = 语义漂移但可观察、可恢复；S3 = 术语/展示级。

### 2.1 000 philosophy（8 条）

| ID | 要求（引文 + doc:line） | 判定 | 设计证据 | 实现证据 | 严重度 | 备注 |
| --- | --- | --- | --- | --- | --- | --- |
| P-1 | "It acts only when asked, or when a written rule allows it… Work nobody asked for needs a rule the team wrote, and starts in a mode that only records what the agent would have done"（000:11） | PARTIAL | §1.1:42-48；§4.3:330；§8.2:1577 | `engine.go:338-369`（gate）、`213-235`（accept mode，默认 observe）；`cmd/zeokeel/assess.go:12-18`（规则硬编码） | S2 | observe 默认 ✅；但"team wrote 的 rule"不存在：规则是代码里的 `defaultRules()`，不是 Policy 数据，也没有规则 CRUD/版本。 |
| P-2 | "It confirms a request once and names what it will do. It stays quiet while it works. It writes again when a result is ready… go to the conversation where the request was made"（000:12） | PARTIAL | §4.10:810-962；§6.3:1231-1233 | `engine.go:588-632`（`replyPlanLocked`）；`runtime/reply.go:81-116`（`deliverReply`） | S2 | 回原会话 ✅；"确认一次/工作中安静"无对应实现（无 ack 阶段）；`ReplyPlan` 只用于面板展示（`runtime.go:279`），投递路径完全不读它（见 §4 ReplyPlan）。另一条哲学要求"stops by impact, not by effort"（000:14）按 000:20 属提示词职责，`prompts/defaults.go:38-44` 有 escalation 段，无法从代码验证。 |
| P-3 | "It asks one concrete question at a time, in a structured form, so the question reaches the person instead of staying inside a long report"（000:13） | PARTIAL | §5.10:1147；§6.9 | `domain/ticket.go:202-209`（`Attention`）；`engine.go:727-739`；`runtime/artifacts.go:83-112`（`ask_user_question` 工具） | S1 | 结构化 ✅，但**没有回答入口**：`Attention` 一旦写入没有任何清除路径，面板在 `person/answer` 永久停留（`ui/app.go:32-35` 只有 Approve/Reject）。 |
| P-5 | "It reports honestly. A draft is marked unfinished, a failure is reported as a failure, and a limit is never bypassed to avoid asking"（000:15） | PARTIAL | §4.4:350；§8.4 | `runtime/worker.go:300-311`（失败→`ReportFailed` + 回复）；`engine.go:832-859` | S2 | 失败诚实 ✅；无"draft/unfinished"概念；"limit never bypassed"无对象——设计 §5.10 承诺的"每 Ticket token 预算硬上限"未实现（`runtime/usage.go` 只统计，不设限）。 |
| P-6 | "Few concepts. The design has only its domain concepts… A business need becomes an attribute of one of them, or a prompt the agent follows. The design does not add a concept for it"（000:19） | DEVIATES-UNJUSTIFIED | §4.10:866-878（新增 `propose`+`Proposal`）；§5.9:1090-1100（宣称零新增概念，同时新增 schedule/goal 实体）；§4.6（Brief） | `domain/ticket.go:12-15,189-200`；`engine.go:240-244,636-679`；`internal/goal/goal.go:61-83`；`internal/store/schema_v3.go:68-84`；`internal/schedule/store.go:21-64` | S2 | 见 §4 EXTRA：Proposal、ReplyMode、Workspace/Brief 属"产品扩展"；Goal/Schedule 的独立实体+独立表直接违反 000:19 与设计 §5.9 自己的承诺。 |
| P-7 | "Few settings. A default replaces a setting when one value suits almost every team. Behaviour that a team describes in words is prompt text, not a setting"（000:20） | PARTIAL | §4.10:920-930（reply mode）；§7.1:1527（每工作区 budget/slots） | `runtime/durable.go:78-127`（concurrency/command_timeout/model_timeout/poll_interval）；`engine.go:25-39`（ReplyMode/AcceptMode） | S3 | AcceptMode 是原型设置，合理；ReplyMode 三档 + 四个数值设置是原型没有的新设置面（设计以"复用 accept mode 思想"论证，属可辩解的扩展）。 |
| P-8 | "A panel that shows only what needs a person. It lists first what waits for a person. A healthy state takes no space"（000:21） | CONFORM | §6.3:1280-1315；§6.5:1346 | `ui/overview.go:13-45`（先 `approvals` 后 `needsPerson`，健康/活动在下方列） | S3 | 主界面确为"待办优先"；但 `approvals` 与 `needsPerson` 是两个动作带（设计 §4.10:863-876 有意分开），与"一个概念一个地方"是张力而非违反。 |

### 2.2 001 domain（21 条）

| ID | 要求（引文 + doc:line） | 判定 | 设计证据 | 实现证据 | 严重度 | 备注 |
| --- | --- | --- | --- | --- | --- | --- |
| D-1 | "Work moves from Trigger to Eval to execution… Source produces Triggers… every other Trigger enters Eval, which decides whether it asks for new work"（001:7） | PARTIAL | §3.3:221-254 | `engine.go:150-287`（`Receive`：fan-out→gate→route→decide→ticket） | S2 | 链路存在，但**没有 Eval 聚合、没有 Thread 快照、没有批量**；"Eval"退化为一次同步 assessor 调用。 |
| D-2 | "Holds its read position and its own bot identity. Defines what counts as an event… Drops the bot's own messages and Triggers outside what it listens to before they enter the domain. Translates each Trigger into the Threads it belongs to and the Channel addresses"（001:27） | PARTIAL | §7.1:1528-1536（kv 存 Source 游标）；§4.8 | `internal/sources/github/poll.go:40-78`；`github/scope.go:17-38`；`github/trigger.go:101`（`Self`）；`cmd/zeokeel/github.go:57-91` | S2 | 事件形状/scope/自身消息过滤 ✅；**读位置只存在 goroutine 局部变量**（`cmd/zeokeel/github.go:58` `var cursor sources.Cursor`），重启后 `sinceOf` 空游标退回"现在"（`github/poll.go:203-214`）→ 设计 §7.1 承诺的 kv 游标未实现，停机期间事件永久丢失（不重放，但也不补）。001:27 的"restart must not replay"本身成立（空游标=从现在开始），代价是用漏读换不重放。 |
| D-4 | "Belongs to one or more Threads. Accepting a Trigger twice creates one Ticket"（001:28） | CONFORM | §4.4-1:339；§4.1:279 | `engine.go:156-158`（`ErrDuplicate`）；`engine.go:262-264`（Ticket ID 由 Decision 派生）；`engine_test.go:182` | S1 | 幂等成立；内容派生 ID 在 `domain/ids.go:39-47`。 |
| D-5 | 内容派生 ID（无 UUID）、首段为 scope（001:28 + 设计 §4.1:279、§4.5:391） | CONFORM | §4.1:275-279；§4.5:389-393 | `domain/ids.go:14-47`；`domain/domain_test.go:10-42` | S2 | 一处不一致：IM Trigger 用 `domain.WorkspaceScope("")`（`cmd/zeokeel/extras.go:194`）而非 `domain.Global`，与设计 §4.7:573"scope = ws_xxx 或 global"不符（内部自洽，但全局命名空间被分裂）。 |
| D-6 | "At most one Eval runs on it at a time… holds its Subscriptions, the running Eval and its Decision history"（001:29、43） | MISSING | §4.4-2:340；§4.2 未覆盖 Eval | `engine.go:46-52`（`thread` 只有 `triggerIDs/ticketIDs`）；全仓库无 `Eval` 类型（`grep "type Eval"` 为空） | S1 | 无串行化、无批次、无"期间 Trigger 合并进下一批"；两个并发 Trigger 可以各自起一次 assessor 调用（`Receive` 持锁串行，但每次都独立判定，不合并）。 |
| D-8 | "A Thread may have several Channels; a Ticket holds a set of them"（001:31） | PARTIAL | §4.1:268 | `domain/trigger.go:54-59`（单个 `ChannelRef`）；`domain/ticket.go:227`（`SubscribedThreads []string`） | S3 | Ticket 上没有 Channel 集合，只有 Thread 字符串；回写靠 `Outbound`/outbox 临时拼。 |
| D-9 | "Policy… The Policy version covers the rules, the accept mode and the Source parts. A change that bears on a live Ticket… is delivered to its Assignment; it does not revoke Mandates. Identities on different Sources are never mapped"（001:32） | PARTIAL | §4.3:324-333；§7.1:1527 | `engine.go:23-39`（`Policy{Version, Allowed, AdmitsUnaddressed, ReplyMode, AcceptMode}`）；`engine.go:104-112`（`Register` 直接替换） | S2 | version+allowed ✅；rules/admission/bots 不在 Policy 内；没有任何"策略变更投递到 Assignment"的路径；身份不跨 Source 映射 ✅（`engine.go:27-29` 注释 + 结构）。 |
| D-10 | "Eval — Input: the Trigger batch, a Thread snapshot, every Ticket on the Thread including ended ones, and the current Policy. Output: a Decision. Holds no state"（001:33） | MISSING | §3.3:232-237；§4.3:324-333 | `engine/router.go:19-25`（`AssessInput{Trigger, Candidates, Briefs}`）；`engine.go:181-193` | S1 | assessor 看不到任何 Ticket，也看不到 Policy；原型"assessment avoids duplicates from the Ticket list"（001:52）失去依据 → 重复工单。 |
| D-11 | "Mandate: …the Policy version, the scope, the strength, the matched rules, the sender's identity on that Source and how the Source received the Trigger. Immutable; wider scope means a new Ticket. A Ticket's end does not end it: only a person's stop withdraws it, and a Policy that no longer holds it revokes it. Revalidated against the current Policy before an ended Ticket resumes"（001:34） | PARTIAL | §4.3:331；§4.1:270 | `domain/ticket.go:99-106`（只有 `Strength/PolicyVersion/Actor/Workspace`）；创建后从不改写（immutable ✅） | S1 | 缺 scope、matched rules、basis（via/way-received）；**没有 stop、没有 revoke、没有 revalidate**——"Immutable"只剩"从不修改"的空语义。 |
| D-12 | "Ticket: A unit of authorized work. **Only Eval creates one.** Refers to its Mandate; holds its Channels and Inputs. Goes open → ended, and back to open when a person's message… Defines its own state"（001:35） | DEVIATES-UNJUSTIFIED | §4.10:866-878（`propose`；"批准之后才创建 Ticket 与 Mandate"） | `engine.go:636-679`（`Approve` 创建 Ticket+Mandate）；`engine.go:240-244`；无 resume | S1 | 两处违反：**批准（人）而非 Eval 创建 Ticket**；**没有 Inputs/Channel 集合，没有 ended→open**。设计在 §4.10:876 用"未授权的工作不是工单"论证 propose，但没有处理它与 001:35"Only Eval creates one"的冲突（属未支付的语义债）。 |
| D-13 | "Assignment: the binding of a Ticket to one Executor instance. A Ticket has several over time. Goes pending → running → ended independently of the Ticket. Only its final report may end the Ticket on the Executor's side"（001:36） | PARTIAL | §4.1:272；§4.4-11:349 | `domain/ticket.go:170-187`；`engine.go:829-859`（`Report`）；`engine.go:895-921`（`StartAssignment`） | S2 | 三态存在；但没有 executor 实例标识（只有 `Assignment.ID`）；最终 report 只结束 Assignment，除 `dropped` 外从不结束 Ticket（与 003:94 一并失效）。 |
| D-14 | "A matching Trigger fans out to every live subscribed Ticket, with no check of who sent it; a person's message that reaches a Ticket this way takes no Eval"（001:37） | PARTIAL | §4.4-3:341；§4.9:759 | `engine.go:289-318`（fan-out）、`167-211`（同一条消息继续 Eval） | S1 | 无条件扇出 ✅（含 bot）；**"takes no Eval"被违反**：fan-out 后仍走 gate→assess→Decision，同一条改请求可能既进上下文又开新单。 |
| D-15 | "A Thread an Executor follows for context reaches its Ticket the same way, but lends it no authority and does not resume it"（001:37） | MISSING | 设计未区分 own/context 订阅 | `engine.go:229-231` `ContextInput`；无 follow/unsubscribe 工具（`runtime/artifacts.go:20-113` 只有 report/present/ask） | S2 | 没有"上下文订阅"概念，也没有 follow/handback 工具，设计 §5.3:1033 列出的 `handback/follow/ack` 工具均不存在。 |
| D-16 | "An ended Ticket keeps its Subscriptions… a person's message resumes it under the same Mandate, with no Eval or Decision; messages from bots and CI do not reach it… A Ticket a person stopped receives nothing"（001:37） | MISSING | §4.3:331（"恢复已结束 Ticket 前必须 revalidate"）；§4.4-5:343（`letGo`） | `engine.go:304`（fan-out 直接跳过 ended）；无 resume；无 stop | S1 | 已结束 Ticket 的订阅实际上被无视：人的消息也不可能唤醒它 → 003:100 的"later human comment to resume"无从发生。 |
| D-17 | "The main Session attaches a Thread to an existing Ticket only when its message points at that Ticket explicitly. A person can drop every Subscription on a Thread; an Executor can drop only its own Ticket's Subscription…"（001:37） | MISSING | 设计未覆盖 | 无 attach/unsubscribe API | S2 | 订阅关系只能由创建时写入（`engine.go:320-336`），之后不可管理。 |
| D-18 | "A person is not a domain concept. A Trigger carries its actor's identifier as the Source gives it. An allowed list is a Source's part of the Policy… That one person has accounts on several Sources is prompt text"（001:39） | CONFORM | §4.8:642 | `domain/trigger.go:41-46`（`Actor`）；`engine.go:27-29`；`engine.go:363-367` | S2 | 概念层面 ✅；但 gate 对"所有工作区的 allowed 并集"做 OR（`engine.go:363-367`），而原型是"该接收 scope 的 Source 部分"——见 A-3/A-5。 |
| D-19 | "The Executor, with its Sessions and directories, is outside the domain and connects through Assignments"（001:41） | CONFORM | §3.2:196-219 | `case`：`internal/agent` 的依赖图不含 `internal/domain`（`go list -deps` 已验证）；`domain.Ticket` 无 session 字段 | S3 | 分层纪律成立；代价是 Assignment 里没有任何 executor 标识（见 D-13）。 |
| D-20 | 001 "Rejected concepts" 全表（001:49-60） | PARTIAL | §1.5:108（称维护 `design/rejected-concepts.md`） | 逐条：无 Principal ✅；无 waiting/revoked Eval ✅（无 Eval）；无 append Decision ✅；无 correlation 路由 ✅（概念整体缺失，`Correlation` 未建模——负面规则真空成立，"关联证据"能力也没有）；无"每 Thread 单 Ticket" ✅；Ticket 状态不跟随 Session ✅（`ticket.go:265-291`）；无 executor 声明 stop ✅；无策略变更取消 Ticket ✅（真空）；失败不锁工具 ✅；不用 report 自由文本推断等待 ✅（`Attention`/`ReportState`） | S2 | 负面清单基本守住；但设计 §1.5:108 承诺的 `design/rejected-concepts.md` **文件不存在**（`design/` 只有 design/M0-tasks.md、design/prompts.md、design/prototype），且实现反向新增了 Goal/Schedule/Proposal 实体（§4）。 |
| D-21 | "Decision: accept·stop·ignore·unavailable；Strength: explicit·inferred；Assignment: pending·running·ended；Report: progress·waiting·ready·blocked·failed·dropped"（001:109-112） | CONFORM | §4.1:269-273 | `domain/ticket.go:8-19`（含额外 `propose`）；`125-135`；`170-177` | S3 | 枚举齐全；`unavailable` 只有枚举与面板渲染（`runtime/pages.go:395`），从无写入路径（见 A-25）；`propose` 是 EXTRA。 |
| D-22 | "Attention — A question on a Ticket that waits for a person's answer"（001:101） | PARTIAL | §5.10:1147 | `domain/ticket.go:202-209`；`engine.go:727-739`；`runtime/artifacts.go:290-301` | S1 | 定义 ✅；生命周期 ❌：无 `Attention = nil` 的代码路径，面板无"回答"命令 → 工单永久 `person/answer`。 |

### 2.3 002 authorization（22 条）

| ID | 要求（引文 + doc:line） | 判定 | 设计证据 | 实现证据 | 严重度 | 备注 |
| --- | --- | --- | --- | --- | --- | --- |
| A-1 | "The host enforces allowed lists, rules and Mandates. Messages, comments and quoted documents are inputs to a decision; they cannot change those permissions"（002:5） | PARTIAL | §1.5:109,111；§4.6:458 | `engine.go:338-369`；`assess/assess.go:144-207`；`runtime/artifacts.go:20-113`（工具集无权限工具） | S2 | allowed list ✅；rules 不是 Policy 数据；Mandate 在执行侧不起约束作用（Worker 工具不受 scope 限制）。 |
| A-2 | "The Source records whether a message addresses the agent: `mention`… and `direct`"（002:11） | CONFORM | §4.9:764-786 | `internal/im/addressing.go:12-18`；`domain/trigger.go:26-39` | S3 | `@所有人` 不算 addressed、ReplyTo 只是事实，两条易错规则都在注释与实现里。 |
| A-3 | "An allowed person who addresses the agent makes an explicit request"（002:13） | PARTIAL | §4.3-1:328（"gate 判定 `strength ∈ {explicit, inferred}`"） | `engine.go:338-369`（只返回 `ok`，不返回 strength）；strength 来自模型输出（`prompts/defaults.go:74`；`assess.go:172-174`） | S1 | **确定性 gate 没有产生 strength**——这是 002 四层的第一层被掏空。模型把 obvious request 判成 `inferred` 就进 observe，判成 `explicit` 就绕过安全阀。 |
| A-4 | "A person's follow-up on a Thread with Tickets follows the rules below"（002:14） | MISSING | §4.4-3:341 | 无 `followupOwners`；无 follow-up strength 计算 | S1 | 见 A-9/A-10。 |
| A-5 | "Other human messages use the admission table for their receiving scope"（002:15） | MISSING | §4.3-2:329 | `engine.go:30-32`（`AdmitsUnaddressed bool`，跨所有工作区 OR，`349-353`） | S1 | 表被压缩成一个 bool，且不区分 scope。 |
| A-6 | "A bot on the Source's bot list can produce an inferred candidate, never an explicit request. Other bots are not admitted"（002:16） | MISSING | 设计 §4.3 未列 bot list | `engine.go:340-344`（所有非人类一律拒绝，注释自认 "this skeleton does not model"）；`Policy` 无 bots 字段（`engine.go:25-39`） | S2 | 机器人**永远不能**成为 inferred candidate；CI/bot 消息只能 fan-out，不能进入判定。 |
| A-7 | "Every message on a subscribed Thread reaches its subscribed Tickets as context, including messages from bots and CI. This delivery bypasses the gate and grants no new authority"（002:20） | PARTIAL | §4.3:326；§4.9:759 | `engine.go:163-165,289-318`；`runtime/worker.go:476-484` | S1 | 扇出先于 gate ✅、不授权 ✅；但**从未投递给 Executor**：Worker 只拿 Origin 文本，`ContextInput` 无消费者。 |
| A-8 | "A person's message on a Ticket's own Thread belongs to that Ticket's Executor to decide… While an Executor owns the message, it takes no Eval and gets no Decision"（002:22） | MISSING | §4.4-3:341 | `engine.go:167-211`（无条件 Eval） | S1 | 同 D-14。 |
| A-9 | "Ownership continues while the Ticket is live, even if a person removes its Subscription. It also covers an ended Ticket that was not stopped by a person and has the message waiting to resume it"（002:24）；"the main Session may only stop work on the person's request; accept, attach, ignore and requeue are refused"（002:26） | MISSING | 设计未覆盖 ownership 生命周期 | 无 ownership/stop/refuse 机制 | S1 | 与 A-4/A-8 同源；这就是设计 §1.5:114 点名的 `followupOwners` 缺失。 |
| A-10 | "An Executor hands a message back once… A message goes to a fresh Eval only after no Ticket still owns it. Its candidate includes the Executors' reasons… a Ticket ends, it releases follow-ups its Executor did not read or take up"（002:30-34） | MISSING | §4.4-5:343（`letGo`） | 无 handback/letGo；Ticket 只可能因 `dropped` 结束（`engine.go:855-857`） | S1 | 设计 §4.4-5 的"未读人消息放回 Eval"没有实现，且没有 Ticket 结束路径去触发它。 |
| A-11 | "Strength: explicit / inferred / not admitted"（002:50-53）；"For an unlisted person, an explicit admission cell takes precedence…"（002:54） | MISSING | §4.3:330 | 无 strength 计算，无 `not admitted` 概念（被拒=Disposition filtered，`engine.go:170-178`） | S1 | "被准入但强度不足"（inferred in `off` mode）与"未被准入"在实现里不可区分。 |
| A-12 | Admission table：行=接收 scope（Feishu 每个群/其他群/私聊；GitHub issue/PR/comment/CI），三格，选项与默认值，`observed` 限制，CI 只有最后一格，bot 用 bot list（002:58-79） | MISSING | §4.3-2:329；§4.9:737 | `engine.go:30-32`；`internal/sources/github/scope.go:17-38`（`Observed`/`Row` 计算后**被丢弃**：`domain.Trigger` 无对应字段，`domain/trigger.go:64-89`） | S1 | Source 侧留了接线口（`sources/source.go:32-52`），Gate 侧完全没接 → 设计 §4.3"表行=接收范围，三格"未落地。 |
| A-13 | "The admission table is part of the Policy version. Weakening a cell does not immediately stop open Tickets. It can revoke their Mandates when they are revalidated before resuming"（002:75） | MISSING | §4.3-4:331 | 无表、无 revalidate（`domain/ticket.go:100` 只有注释） | S2 | 与 A-12/A-21 同源。 |
| A-14 | "Accepting an inferred candidate always requires an assessment. Direct accept refuses a Trigger that the gate did not admit as explicit"（002:89）；"The main Session routes explicit candidates with `orchestrator_route`. One call can contain several accept, attach, stop or ignore decisions"（002:85） | PARTIAL | §3.3:234-236（`eval_read`/`eval_decide` 工具）；§5.3:1034 | `engine/router.go:86-97`（总是 assess）；`assess.go:181-206`（explicit 也强制 ≥1 rule） | S2 | 没有 route 工具、没有"批量相互独立"的入口；反而是 explicit 也被迫走完整 assessment + 规则命中，比原型**更严**（原型 explicit 可直接 route 接受）。 |
| A-15 | "The accept mode… `off` / `observe`（default）/ `auto`… Explicit requests work in every mode. Each assessment judges every enabled rule exactly once… A proactive accept requires at least one matching rule"（002:95-101） | PARTIAL | §4.3-3:330 | `engine.go:213-235`（默认 observe ✅）；`assess.go:181-206`（规则覆盖 ✅） | S1 | 三个模式存在，但 `auto` 对同事请求无效（`engine.go:240-244` 提议拦截），且 explicit 也被强制 assessment → 002:101 首句失效。 |
| A-16 | "The Ticket's Mandate records the matched rules, and its Executor receives their names and instructions… a rule's process and communication requirements override the defaults"（002:103-105） | MISSING | §4.3:331 | `domain/ticket.go:99-106`（无 rules）；`runtime/worker.go:255-280`（Worker 提示词无规则段） | S2 | 规则既不在 Mandate，也不进 Worker。 |
| A-17 | "Rules, accept mode and each Source's Policy form the Policy version… A version change discards running Evals and queues their Triggers again. It also sends a policy-changed Input to each affected live Ticket"（002:109-117） | MISSING | §4.4-7:345 | `engine.go:104-112`（`Register` 静默替换）；无版本比较、无 policy-changed Input | S1 | 没有 running Eval 可丢弃（D-6），但"policy-changed Input 投递给受影响 Ticket"是独立要求，也没有。 |
| A-18 | "Source configuration is separate from Policy… A Mandate records its Source and receiving scope. Resuming needs the Source to still listen… Replies wait while their Source is off. They are dropped if the Source no longer listens"（002:119-121） | MISSING | §7.2:1544 | Mandate 无 source/scope（`domain/ticket.go:99-106`）；outbox 有 `Gone`（`outbox/outbox.go:38-51`）但无 source-off 概念，`outbox.Open` 未传 `WithLimit`（`cmd/zeokeel/durable.go:50`） | S2 | "Source off 时回复等待"没有实现路径。 |
| A-20 | "An ended Ticket keeps its Mandate. A person's message on one of its own Threads can resume the same work without a new Eval or Decision… Bots and CI do not wake ended Tickets"（002:127） | MISSING | §4.3:331 | `engine.go:304`（跳过 ended）；无 resume | S1 | 同 D-16。 |
| A-21 | "Only three operations change a Mandate: accept creates it, a person's stop withdraws it, and failed revalidation revokes it. A Ticket stopped by a person stays ended and receives nothing more"（002:129） | MISSING | §4.3:331 | 只有"创建"存在；无 stop、无 revoke | S1 | 设计 §4.4:342 的 `settled` 概念在 Go 里完全没有对应实现。 |
| A-22 | Revalidation：Source 仍需监听 + 按 basis 分类复核（explicit follow-up 直接通过；inferred 需 auto + 规则仍启用；table cell 需仍满足原强度；bot 需仍在 bot list；其他需 sender 仍在名单）；失败则仍在 ended、不收等待消息、每条消息当作"无 Ticket"进 Eval、不告诉 main session 已到 worker、attach 被拒（002:133-141） | MISSING | §4.3-4:331；附录 A:1809 | 无 `Revalidate` 函数（`grep Revalidate` 仅注释 `domain/ticket.go:100`） | S1 | 设计自己列为"不可简化资产"，实现为零。 |
| A-23 | "A person saying `stop` in a Thread is an ordinary Trigger, and Eval decides stop. An Executor cannot declare that a person asked it to stop… Stop in the panel is a command"（002:143-144） | PARTIAL | §4.4:342 | `domain/ticket.go:14`（`DecStop`）；`assess.go:152,222`（模型可返回 stop）；`engine.go:256-260`（记录后无动作）；面板无 stop | S1 | Eval 能判 stop ✅，但 stop 不改变任何状态；"panel stop 是命令"完全缺失。 |
| A-25 | "The time limit starts when the candidate reaches the main Session… An expired Eval queues its Triggers for another attempt. After two consecutive retries, the third expiry records `unavailable`… The `evals` health entry reports unresolved unavailable Decisions from the last hour… The panel asks for human attention only when the unavailable Decision is less than 24 hours old…"（002:147-155） | MISSING | 设计未落地（§4.3 未提 deadline/重试） | `DecUnavailable` 无写入路径（`grep` 仅 `runtime/pages.go:395` 渲染）；`assess.go:126-131` 只把模型错误往上抛；`engine.Receive` 出错直接返回（`engine.go:182-184`） | S2 | 002 的"Eval 未完成"整节缺失：无 deadline、无重试计数、无 unavailable、无 health 聚合、无面板 attention 规则。 |

### 2.4 003 execution（19 条）

| ID | 要求（引文 + doc:line） | 判定 | 设计证据 | 实现证据 | 严重度 | 备注 |
| --- | --- | --- | --- | --- | --- | --- |
| E-1 | 阶段表七行、自上而下首个匹配；`person` 阶段=没有人的输入就无法继续；"A new human message releases a `blocked` or `failed` Ticket… usually `working`"；`idle` 语义（003:9-21） | PARTIAL | §4.2:285-322 | `domain/ticket.go:249-291`（`StageOf`）；`domain/domain_test.go:56-82` | S1 | 三处偏差：①`StagePersonDelivery` 常量在但**永不可达**（无 `Delivery` 字段，`ticket.go:267-291` 不检查）；②`blocked/failed` 不判 `waitsForPerson`（`ticket.go:278-281`），`WaitsForPerson()`（`312-320`）**从未被 `StageOf` 调用** → 人回复后仍停 `person/blocked`；③`idle` 无"Trigger 可继续"的入口（无唤醒逻辑）。 |
| E-2 | "Each Ticket gets an Executor Session named `worker-<ticket id>` and a directory named after the Ticket's short name. Directories are created under the configured execution root, which defaults to `<stateDir>/work`"（003:25） | PARTIAL | §5.5:1052 | `runtime/worker.go:207`（`ticket-<Number>`）；`worker.go:261`（`worker-<Number>`） | S3 | 命名规则不同（number ≠ ticket id ≠ short name）；assessor 校验的 `worktree_name`（`assess.go:178-180`）被 `toOutput` 丢弃（`assess.go:210-234`）。003:27-29 的 preset 概念整体缺失：无 `dsh-superagent-worker/router` preset、无 5s 前台转后台（`runtime/worker.go:210-225`、`agent/tools.go`），也没有 Main Session 的 router 会话（只有一次性 assessor 调用）。 |
| E-4 | 后台作业占住 worker slot 并在回合结束时保持 working、完成才唤醒；30 分钟上限杀 shell job、报 waiting；handoff 立即释放 slot；交接时 subagent 停止、shell job 可继续；Assignment 结束时杀 job/释放 subagent（003:33-47） | MISSING | §5.6:1058-1064（明确"简化不破坏领域语义，可延后"）；§4.4-10:348 | 无 turn 循环、无 slot、无 handoff（`runtime/worker.go:194-322` 是一次 `RunSession`）；"人在 Session 里直接说话"没有通道（003:41），Assignment 结束/重启策略缺失。会话级恢复则真实存在：`internal/agent/session.go:23-123` 修补未配对 tool_call 并给 recovery prompt，对应 003:45 的后半句。 | S2 | 设计把它推到 M4（§10:1743），但 §11:1853 又把"§5.6 子智能体"标 ✅，容易被误读为完成。 |
| E-6 | prepare：第一次 Assignment 前目录不存在则以 `sh` 在 execution root 跑一次、10 分钟；失败则 Ticket failed 并记输出；"A retry or a person's message… tries preparation again"；PR 归属按 branch 末段匹配短名（003:51-53） | MISSING | §5.5:1053（"失败不自动重试"） | 无 `prepare` 实现（`grep -i prepare internal/ cmd/` 无命中）；`runtime/worker.go:207` 直接 `workdir := filepath.Join(root, "ticket-N")` | S1 | 设计 §1.5:115 专门强调的"反直觉但正确"的规则，在代码里连对象都不存在。 |
| E-7 | Concurrency：slot 从 launch（含 prepare）或唤醒投递开始持有；配置上限 + "one extra slot for a person's message or a status request"；其他 Input 等普通 slot；launch 在 `working/queued` 等待；launch 并发（003:55-61） | PARTIAL | §4.4（未列 slot）；§1.5:113（人的消息 +1）；§7.1:1527 | `runtime/durable.go:78-94`（`concurrency()` 2..8）；`durable.go:41-45`（effects `Limit`） | S1 | 有一个进程级上限，但：**没有 person 额外 slot**（`concurrency()` 只读一个数）、没有 per-ticket slot、没有"人消息抢下一空位"、queued 不可见为 launch 队列。003:59 的"人永远不被机器挤掉"未实现。 |
| E-8 | Execution variables 表；prepare 与 Executor 指令收到同一批；"None contains credentials"；blocked commands 按名失败；"These wrappers and directories establish defaults, not a security boundary"（003:65-80） | PARTIAL | §5.5:1054-1055；设计 §1.5:123 | `agent/tools.go:30`（blocked 按 firstWord）、`251-253`（`cmd.Env = append(os.Environ(), ...)`）；无任何 `DSH_SUPERAGENT_*` 变量 | S2 | blocked ✅、诚实边界 ✅；变量表全缺；**`os.Environ()` 全量继承**意味着 Worker 能看到宿主环境里的 token（003:65"None contains credentials"被违背的可能路径）。 |
| E-9 | "Auto-merge is governed by the Delivery requirements prompt. The host has no merge-specific decision logic… The default instructions allow auto-merge only for a pull request opened for the Ticket… fewer than 200 lines"（003:84-90） | PARTIAL | 设计未写默认 merge 段 | 宿主无 merge 逻辑 ✅（002:123"结果条件不属于 Policy"因此成立）；`prompts/defaults.go` 无 merge/delivery-requirements 段 | S2 | 负面要求成立；默认指令缺失（003:88 的自动合并边界没有任何提示词承载）。 |
| E-10 | "Ready without a pull request: a `ready` Report ends the Ticket as completed"（003:94） | MISSING | 设计**未写**该规则（§4.4、§5.x 均无） | `engine.go:851-854`（ready 只把 Assignment 置 ended）；`engine.go:842-859` | S1 | Ticket 永远停在 `person/review`；这是"stuck ticket"的主因，也是 §4.4 遗漏的原型规则。 |
| E-11 | "Nothing to do: an Executor… reports `dropped`. The Ticket ends without a message, unless a matched rule asks for one, and a person's later message on its Threads resumes it"（003:95） | PARTIAL | §4.4 未覆盖 | `engine.go:855-857`（dropped→Ended）；无 rule 决定是否发消息；无 resume | S2 | 结束 ✅；"rule 决定是否通知"与"后续消息 resume" ❌。 |
| E-12 | PR 生命周期：跟随起始 PR 与从本分支开的 PR、reopen 重新跟随；"The Ticket is done when none of the pull requests it opened remain open… ends with the outcome of the last relevant close and keeps its Subscriptions"；chat 渠道一条最终通知；移除订阅即停止跟随（003:98-104） | MISSING | §4.4-9:347；附录 A:1812；§4.4-1 的"follows" | 无 `Follows`/`followsDone`/PR 概念；`domain/ticket.go` 无 follow 字段；`grep -i follow` 仅注释与 UI 文案 | S1 | 003 最核心的"Ticket 何时算完成"整节缺失。 |
| E-13 | "A close does not end the Ticket while resumed, retried or recovered work is unfinished… `Awaiting review` therefore means a `ready` Report with a pull request"（003:102-106） | MISSING | §4.4-9:347 | `domain/ticket.go:276`（任何 ready 都判 review） | S2 | 无 unfinished/acknowledged-follow-up 判定；`Awaiting review` 语义与原型不同（无 PR 也显示 review）。 |
| E-14 | Follow-ups & context Threads：Executor 决定并 handback；"An Input counts as read after the Session takes it into a step and subsequently reports or ends a turn"；结束时未读/未确认的回 Eval；context-only 不拥有不 resume；订阅 Thread 轮询到"最后一个 Trigger 与订阅 Ticket 结束两者较晚者之后两周"（003:110-116） | MISSING | §4.4-10:348；§5.2:1023 | 无 Input 读取记录、无 handback、无 context-only、无订阅驱动轮询窗口（`cmd/zeokeel/github.go` 只按通知轮询） | S1 | 003 与 002 共用的一整块归属/已读机制缺失，设计 §1.5:114 把它列为"必须实现"，附录 A:1808 列为不可简化资产。 |
| E-15 | 失败与恢复：blocked/failed 不收自动 Input；"a passing failure of the model provider or the network… tried again first, three times with a growing backoff… a failed prepare command is not tried again"；"Once failed, CI and bot messages are recorded on it, but they are not delivered"；"A launch that failed is not tried again on its own"；hold 由存储的 Reports/Inputs 推导（003:122-134） | PARTIAL | §5.8:1074-1082；§7.2:1541-1544 | `runtime/worker.go:300-311`（失败→failed）；`durable.go:41-45`（effects `Retry: [5s,5m]`，会**自动重试 launch**）；`agent` 无 provider 级重试 | S2 | 失败上报 ✅；"passing failure 重试 3 次"❌；"failed launch 不自行重试"被 effects 的 `Retry` 违背（DEVIATES）；"CI/bot 只记录不投递"没有投递机制可谈；hold 无实现。 |
| E-16 | Retry：只在 `person/delivery`、`person/blocked`、`person/failed` 可用；"a new Assignment, which starts its worker again and takes the Inputs that wait"；"A retry ends the current Assignment and starts a new one in the same Executor Session, directory, branch and pull request"（003:137-143） | MISSING | 设计 §11 未标 ✅（未声称），§10:1743 列为 M4 | 无 retry 命令（面板只有 Approve/Reject：`ui/app.go:32-35`）；`canRetry` 概念不存在 | S1 | 修复 stuck 工单的唯一手段缺失。 |
| E-17 | Restart：running 的 Assignment 以 "host restarted" 结束并按 oldest first 新建、带 recovery note；waiting/ready/blocked/failed 保持不动，除非之后有 Input；Sessions/dirs/subscriptions/pending messages/读取记录跨重启存活；retry/restart 只重投未取走的消息且不重复（003:146-152） | PARTIAL | §5.8:1074-1082 | `agent/session.go:23-123`（会话日志修复/续跑）；`engine/persist.go`（Ticket/Decision/Effect 落库）；无 Assignment 重启、无 pending Input 恢复、无"读取记录" | S2 | 会话级恢复是真实的 ✅；Assignment/Input/已读层面全缺。 |
| E-18 | Stored work：决定后的动作先落库再执行；relay/delivery 失败 2s 起翻倍到 1min 重试；launch 失败则 Ticket 失败（除非 passing）；每类工作连续失败 3 次才报错；relay 保序、失败挡住同类后者（003:155-161） | PARTIAL | §7.2:1541-1544 | `internal/effects/effects.go`（提交后执行、`Later`、3 次失败标 error、`Ordered`）；`engine/persist.go` 先改内存再 `SaveTicket`（如 `runtime/artifacts.go:139-152`） | S2 | effects 队列质量不错 ✅；但 domain mutation 是"先内存后落库"（并非"先存储后执行"），崩溃窗口存在；重试参数与原型不同（5s/5m vs 2s..1min）；launch 被自动重试（与 E-15 冲突）。 |
| E-19 | Channel failure：出站消息存储后重试（5s 起翻倍到 300）；同一 Ticket 同一会话保序，"前者连续失败 3 次才放行后者"；失败消息算 system problem；仅在 Source 不再监听/永久不可达时丢弃；丢弃保留记录、永不重排、ack 显示 dropped（003:177-191） | PARTIAL | §7.2:1544 | `outbox/outbox.go:34-51`（pending/sent/gone）、`300-334`（`behind` 保序、`settle`）；`outbox_test.go:295,356,207` | S3 | 这是实现质量最高的一块（保序/Gone/回执永久）；但"Source off 时等待"（A-18）与"失败 3 次放行"的阈值需对照，`behind` 的放行条件未逐行核对（推断：有等价实现）。 |
| E-20 | Pause processing：停 dispatch/新 Eval/Eval 超时/Input 投递/回复；launch 等待恢复；Source 继续记录、运行中的 turn 不打断；暂停期间到期的 Eval deadline 顺延；暂停结束后第一条 pass 恢复 Ticket 若 Mandate 仍成立（003:194-200） | MISSING | 设计 §10:1743（M4） | 无 pause 命令/状态（`grep` 只有 goal 的 `GoalPaused` 与 `HealthPaused` 展示值，`view/goal.go:19`、`im/status.go:17`） | S1 | 设计 §11 未声称 ✅，但与 §10 承诺的 M4 不符；`EvalCounts` 等面板元素会成为误导。 |

---

## 3. 非 CONFORM 项详述

> 按主题分组；每条给"原型要求 / Go 现状 / 后果 / 修复建议"。矩阵里已列的引文不再重复，只标 ID。

### 3.1 授权：确定性 gate 被模型与审批关卡取代（A-3、A-5、A-6、A-11、A-12、A-15、A-16；P-1；D-9）

**原型要求**：002:11-16 把"谁能让我干活"做成确定性函数：Source 提供 `addressed` 事实，gate 查 allowed list、follow-up 规则、admission table、bot list，输出 `explicit|inferred|not admitted`；admission table 的行是接收 scope，三格有默认值（002:58-79）；accept mode 只控制 inferred 候选（002:95-99），explicit 在任何模式下都工作（002:101）；每条启用规则必须被评估恰好一次，proactive accept 至少命中一条规则（002:101），Mandate 记录命中规则（002:103）。

**Go 现状**：`Engine.gateLocked` 只返回 `([]Reason, bool)`（`internal/engine/engine.go:338-369`）：`not human` 一律拒、`not addressed` 用一个跨工作区布尔 `AdmitsUnaddressed`、`Self/local` 放行、否则 OR 所有工作区的 allowed。**strength 不在 gate 产生**，而是模型 JSON 字段（`internal/prompts/defaults.go:74`），`assess.validate` 只校验字面值 ∈ {explicit,inferred}（`internal/assess/assess.go:172-174`），不校验与 Policy 的一致性。`engine.Receive` 拿这个模型自报的 strength 决定是否走 accept mode 安全阀（`internal/engine/engine.go:216-221`）——模型说 explicit 就绕过 observe。admission table 在 Source 侧算出了 `Observed/Row`（`internal/sources/github/scope.go:31-37`），但 `domain.Trigger` 没有字段承载（`internal/domain/trigger.go:64-89`），gate 也不用。bot list 在 Policy 中不存在（`internal/engine/engine.go:25-39`），非人类消息在 gate 第一行被拒（`340-344`）。规则是 `cmd/zeokeel/assess.go:12-18` 的硬编码三条，既不随 Policy 版本，也不写进 Mandate。

**后果**：①"prompt is not permission"（002 与设计 §1.5:111）被打破：改一句提示词、给一条外部文本，就可能让模型把 inferred 报成 explicit，从而跳过"先观察不行动"的产品级安全阀；②`auto` 模式对真实同事请求无意义（见 3.2）；③同一句消息在不同模型/不同提示词下得到不同授权结论，审计不成立；④admission table 的"某群仅 @ / 某 scope 全部消息"配置能力为零。

**修复建议**：把 strength 计算移回 `engine`：新增 `policy.Gate(trigger, sourcePolicy, admissionRow, observed, senderMembership) (strength, reasons)`，输入只允许 `meta+配置`（设计 §1.5:130 的纪律），assessor 的 `strength` 字段删除或仅作校验（不一致即整批判无效）；把 `Policy` 扩为 `{Version, Rules[], Sources{Allowed, Bots, Admission}}`；`domain.Trigger` 增加 `Row/Observed` 事实字段（Source 已算好）；`Mandate` 增加 `Scope/Rules[]/Basis`。

### 3.2 审批关卡取代 accept mode（D-12；A-14、A-15；P-6；§4 EXTRA）

**原型要求**：001:35"Only Eval creates one"；002:89 允许 explicit 直接 accept，002:99 `auto` 对 inferred 候选应"positive assessment accepts it"；002:101 explicit 在任何模式都工作。

**Go 现状**：`Receive` 在 accept mode 处理之后，把所有"非本人、非 local"的 accept 降级为 `DecPropose` 并清空 Mandate（`internal/engine/engine.go:240-244`）；`Approve` 才创建 Ticket+Mandate（`636-679`），且 Mandate strength 被硬写为 `"explicit"`（`651-654`）。测试自述这是有意为之：`internal/engine/engine_test.go:493-497`"Colleague requests still become proposals; auto mode only changes the accept-mode gate, not the approval requirement"。

**后果**：①`off/observe/auto` 三档退化为两档（off/observe 之外 auto 也不开单），002:95-101 的设计意图消失；②explicit 请求的语义从"可以开工"变成"提请 operator 批准"，002:13/101 失效；③Ticket 由人的批准创建，001:35 被违反；④原型没有的 `Proposal`/`DecPropose` 成为必经路径，给"待批准"增加了一个新的、持久的、需要面板分组的对象（000:19 概念膨胀）。

**判定**：设计 §4.10:831-878 为它写了论证（代理授权放大、data workspace 外发），且 `WorkspaceKind=data` 的敏感场景确有真实需求 → 作为"产品扩展"可理解；但它没有与 002/001 的冲突对齐，是**未支付的语义债**。修复建议：把"第二道门"限制在 `kind != repo` 或"敏感内容"命中的情形（设计 §4.10:880-891 的敏感清单），repo 类请求回到原型路径；或把它明确写成 `Decision{kind:propose}` 只用于 `inferred`，explicit 走原路径；同时在 001 层给 `Proposal` 一个不创建 Ticket 的正式定义（而不是"Eval 之外的第二个创建者"）。

### 3.3 Eval 聚合缺失的连锁反应（D-1、D-6、D-10；A-4、A-7、A-8、A-9、A-10；D-14）

**原型要求**：Thread 至多一个 Eval，期间到达的 Trigger 合并进下一批（001:29、43）；Eval 的输入含 Trigger 批次、Thread 快照、该 Thread 上所有 Ticket（含 ended）、当前 Policy（001:33）；human 消息先扇出，只有 `followupOwners` 为空才进 Eval（001:37、002:22）；Ticket 结束时未读人消息 `letGo` 回 Eval（002:34）。

**Go 现状**：`thread` 只有 `triggerIDs/ticketIDs`（`internal/engine/engine.go:46-52`）；没有 `Eval` 类型、没有批次、没有快照；`AssessInput` 只有单条 Trigger + 候选 + brief（`internal/engine/router.go:19-25`）；`Receive` 对每条 Trigger 同步调一次 assessor（`engine.go:181-193`）；fan-out 后不判断归属，继续 gate→Eval（`167-211`）；没有 follow-up 归属、没有 handback、没有 `letGo`。

**后果**：①同一 Thread 上的两条消息各自开单（重复评论/PR，正是附录 A:1807 点名的症状）；②Eval 看不到 Ticket 列表，无法做原型"assessment avoids duplicates from the Ticket list"（001:52）；③A-7/D-14 的"同一消息既上下文又 Eval"使"订阅不授权"的设计被绕过；④人消息在已订阅 Thread 上完全可能被重复处理。

**修复建议**：引入最小 `domain.Eval`（state: queued/running；batch: []TriggerID；thread；deadline）挂在 `thread.eval` 上，`Receive` 只入队，`passes` 取一批跑 assessor；`AssessInput` 增加 `Tickets []domain.Ticket`（含 ended）；实现 `followupOwners(trigger, records)` 并在 fork 前判空；补 `letGo`。

### 3.4 Ticket 生命周期与结束（D-11、D-13、D-16、D-20、E-10、E-11、E-12、E-13；A-20、A-21、A-22、A-23）

**原型要求**：Ticket open→ended→open（001:35）；ended 保留订阅，人的消息同 Mandate 恢复、无 Eval（001:37、002:127）；只有三种操作改 Mandate（002:129）；ready 无 PR 即 completed 结束（003:94）；PR 生命周期决定结束（003:98-104）；close 在 unfinished/未确认 follow-up 时等待（003:102）。

**Go 现状**：`Ended` 只在 `ReportDropped` 时写（`internal/engine/engine.go:855-857`）；`ready` 不结束（`851-854`）；没有 `Follows` 字段、没有 `followsDone/unfinished`、没有 `revalidate`、没有 stop、没有 resume；`Ended.Outcome` 的 `completed/merged/closed/stopped`（`internal/domain/ticket.go:246`）永远不会被写入；fan-out 跳过 ended（`engine.go:304`）。

**后果**：①每个正常完成的 Ticket 永远停在"等待你评审"（面板 `person/review`，`runtime/runtime.go:593`），列表无限增长，000:21"健康不占空间"失效；②没有任何"完成"事实，回复永远按 `report.Summary` 发（`runtime/reply.go:37-79`）；③PR 合并/关闭完全不影响 Ticket；④人无法 stop，Ticket 只能被 executor 的 dropped 结束。

**修复建议**：按 003:92-106 实现 `Ticket.end(by, outcome)` 与 `Report(ready)` 的结束判定；增加 `Follows []Follow{thread, own, closed}` 与 `followsDone/unfinished`；加 `stop`（panel 命令 + Eval 决定）与 `resume`（人消息 + revalidate）；把 `Ended.Outcome` 真正写出来。

### 3.5 投递、已读与恢复（D-15、D-16、E-4、E-14、E-15、E-16、E-17、E-18、E-19；A-9、A-10、A-18）

**原型要求**：投递三段式 delivered/read/turnEnded（设计 §1.5:114、§4.4-10:348）；回复保序、失败退避；restart/retry 重投未取走消息、不重复（003:146-161）。

**Go 现状**：`runtime.runTicket` 一次 `RunSession`（`internal/runtime/worker.go:194-322`），没有 inbox、没有 steer/inject、没有 `turnEnded`/`inputsRead` 回调；`agent` 只有事件日志与崩溃修复（`internal/agent/session.go:23-123`）；`ContextInput` 无消费者；retry/stop/pause 不存在。**唯一实现得不错的是出站**：`internal/outbox` 有 pending/sent/gone、`behind` 保序、回执永久（`outbox.go:34-51,300-334`），effects 有提交后执行/`Later`/3 次失败/`Ordered`（`internal/effects/effects.go:60-73`）。

**后果**：①订阅上下文永远到不了 Worker，"后来讨论自动到达执行器"失效；②没有已读概念，未来即使补 lease-back 也无法判"未读回 Eval"；③崩溃后 pending Input 与 Assignment 不恢复（只有会话事件恢复），与 §5.8:1074-1082 的宣称不符；④人消息没有"额外 slot"保护（E-7），并发满时可能被新任务饿死（设计 §1.5:113 的明确要求）。

**修复建议**：在 `domain.Ticket` 增加 `Inputs []Input{id, kind, triggerID, deliveredAt, readAt, ackedAt}`；Worker 循环改为"取 Input → 回合 → turnEnded"并在三个点回调 domain；slot 计数区分 `by=person` 并允许多 1 个；retry/pause 作为 Engine 命令。

### 3.6 阶段表与注意力闭环（E-1、E-2；P-3、D-22；E-13）

**原型要求**：七行阶段表自上而下首个匹配；`delivery` 是真实阶段；`blocked/failed` 需 `waitsForPerson`；人消息让 blocked/failed 回到通常阶段；`person/answer` 等人回答；`idle` 可被订阅 Trigger 继续（003:9-21）。

**Go 现状**：`StageOf` 缺 `Delivery` 分支（`internal/domain/ticket.go:267-291`），`WaitsForPerson()` 存在但**无人调用**（`312-320`），所以 `blocked/failed` 永远停在 person 阶段；`StagePersonDelivery` 的文案在面板里存在（`runtime/runtime.go:601,669`）但永不可达；`Attention` 没有清除路径（`engine.go:727-739`）；没有"新 Input 唤醒 idle"的机制。

**后果**：①`blocked` 工单在人回复后不回到 working（`engine.go:832-859` 只覆盖 executor 上报）；②`person/answer` 永久卡住；③面板出现"投递失败"标签但无实际阶段（诚实状态问题，005 原则）；④`WaitsForPerson` 的"自动输入被扣住"（设计 §4.4-6:344）没有对象。

**修复建议**：`Ticket` 加 `Delivery *Delivery` 与 `Inputs`；`StageOf` 严格照 003 表（含 `waitsForPerson`）；`AnswerAttention`/`DeliverInput` 命令；`Report` 前检查"是否有人消息新于 report"。

### 3.7 prepare、沙箱与执行变量（E-6、E-8；P-5）

**原型要求**：prepare 命令 10 分钟、失败记录输出、不自动重试但 retry/人消息会再试（003:51-53；设计 §5.5:1053 特意强调"这类规则一律照抄"）；执行变量由 Executor/Sources 注册且不含凭据（003:65-76）；blocked commands 只是默认值不是安全边界（003:78-80）。

**Go 现状**：无 prepare（`grep -i prepare` 为空），Worker 目录直接 `os.MkdirAll` 语义（`runtime/worker.go:207`）；无 `DSH_SUPERAGENT_*` 变量；bash 工具 `cmd.Env = append(os.Environ(), ...)`（`internal/agent/tools.go:251-253`）→ 继承全部宿主环境；blocked 按名拒绝（`tools.go:30`）且文档诚实（设计 §5.5:1055）。

**后果**：①多仓库/ticket 目录隔离靠 nothing（`ticket-N` 平铺在 `cfg.Root` 下），没有 worktree 归属，PR 与 Ticket 无绑定（003:53 后半句）；②"prepare 失败不重试"这条被设计点名为"反直觉但正确"的规则无法验证；③凭据可能进入 Worker 进程环境（与 003:65 冲突），且 `os.Environ` 也让"执行环境变量"概念失效。

**修复建议**：实现 `prepare`（`effects` 里的 `launch` 步骤：目录不存在→`sh -c`，超时 10min，输出落库；失败→`ReportFailed` 且标记 `prepared=false`）；执行变量白名单化（`cmd.Env` 只构造允许清单）；给 Ticket 增加 `Directory/ShortName/Follows`。

### 3.8 概念与设置膨胀（P-6、P-7；A-16；E-12）

000:19/20 是"判定标准"而非软建议：设计新增了 `Proposal`、`ReplyPlan/ReplyMode`、`Workspace`（持久实体）、`Brief`、`Artifact.Verified`、`Goal` 实体、`Schedule` 实体、`Skill` 库、`MCP`、`Workflow`。其中 `Proposal` 与 `Goal` 是真正的新领域概念（有自己的状态/表/面板分组），与 000:19 冲突；`ReplyMode` 是"用设置代替提示词"的典型（000:20），应写进提示词或复用 accept mode；`Workspace`/`Brief`/`Artifact` 是部署扩展，可接受（见 §4）。后果是概念数从 9 个（原型 001:23 的核心 + 支撑）涨到 15+，每个都带来面板位置、设置项、迁移与测试成本，直接抬高 000 的"注意力成本"。修复建议：`Goal` 收进 Ticket 的 `follows`+Assignment 连续轮次（设计 §5.9:1099 自己也是这么说的）；`Proposal` 要么并入 Decision 的 reason 维度，要么在 001 正式登记为 Eval 输出的一个 kind（并修 001:35 的表述）；`ReplyMode` 降级为提示词/规则。

### 3.9 Eval 未完成与健康（A-25；P-1；P-5）

002:147-155 的"Eval 超时→重排→三次后 unavailable→health 上报→面板只对 24 小时内的最新且未 dismiss 的 unavailable 求注意力"整节没有实现：`DecUnavailable` 无写入路径，`view.EvalCounts` 只是展示，`evalPage` 渲染 demo 数据。后果：模型超时/输出非法时，`engine.Receive` 直接返回 error（`engine.go:182-184`），调用方（`cmd/zeokeel/github.go` 的 poll 或 IM loop）把整轮当失败或丢弃 → Trigger 无声丢失，与 002:151"later Trigger can start a new Eval"和 003 的"不丢"承诺冲突；面板也不会提示。修复建议：加 `Eval` 状态机 + `deadline` + `attempts`，写 `DecUnavailable` 与 health 条目，面板按 002:155 的四条件过滤。

### 3.10 多工作区扩展与 "Router/Worker" 命名（P-6；D-5；D-18；设计 §4.5/§4.7）

**原型**：单仓库；术语是 Main Session / Executor Session（001:80-81 术语表：`orchestrator`/`worker`），没有 "Router" 概念，也没有 Workspace 实体。

**设计**：ADR-14（design:154）与 §4.5:354-411 把"官方双层"改称 Router/Worker，并把 Workspace 提升为**持久实体**（§7.1:1527 第一张表，所有表带 `workspace_id`），同时声明"工作区是执行与授权的作用域，不是并行的状态机"（:383）。§4.7:561-575 又承认 `workspace` 是 Ticket 属性、Trigger 可无工作区。

**判断**：①术语改名有据（ADR-3:142 明确"代码里叫 `orchestrator`"），但代码里**既没有 Router 会话也没有 orchestrator 会话**——只有一个 `assess` 一次性调用，因此"Router 全局唯一持久会话"（§3.1:185、§5.3:1034）是未实现的承诺，"官方双层"在该项目中只落实了"Worker 每 Ticket 一个"一层。②Workspace 作为持久实体是**部署扩展**：多仓库/数据目录确实是原型单仓库假设的直接后果，000:19"业务需求变成已有概念的属性"在这里被"新实体 + 全局 `workspace_id` 列 + 每工作区 Policy/预算/凭据"替代，设计用 §4.5/§4.7/§1.3:71 付了对价（明确的范围声明与 ID 隔离纪律），**可判为 DEVIATES-JUSTIFIED / EXTRA（有据）**；但 `Workspace Brief`（§4.6）与 `workspace_list/read/propose` 工具（:462-466）在代码里只有 `internal/brief` 的索引与渲染，`workspace_propose` 的"待确认队列"与"人工批准"（§4.6:458）不存在，属于**声明超出实现**。

**风险**：`engine.gateLocked` 用"所有工作区 allowed 的并集"授权（`engine.go:363-367`），在跨区场景下等于"在 A 仓库被允许的人可以在 B 仓库提需求"——设计 §4.5 第 2 条（:385）和风险 15（:1776）明确要求"每张 Ticket 的 Mandate 必须由目标工作区的 Policy 授权"，但 strength 与 allowed 的判定并没有按目标工作区收窄。这是一个**真实的越权面**（S1）。

---

## 4. EXTRA 清单（原型没有的概念/实体/设置）

| # | 新增物 | 设计位置 | 代码位置 | 类型 | 判断 |
| --- | --- | --- | --- | --- | --- |
| X-1 | `Workspace` 持久实体 + 全表 `workspace_id` + 每工作区 Policy/预算/凭据 | §4.5:389-411；§7.1:1527 | `domain/workspace.go:18-27`；`store/schema.go:14-23` | 部署扩展 | **可接受（有据）**：原型单仓库假设的必然延伸，设计明确声明了范围与 ID 纪律（:391-393）；但它确实是 001 概念表之外的实体，代价是 §2 的 D-9/D-18/A-3 全部要按工作区重写。 |
| X-2 | `Workspace Brief` + `brief.Index` + 关键词召回 | §4.6:419-513 | `domain/workspace.go:29-73`；`internal/brief/*`；`engine/router.go:52-84` | 产品扩展 | **半实现**：brief 与召回有；`workspace_propose` 的"待确认队列/人批准"（§4.6:458）没有；"≤300 token / >10 工作区降级 / stale 标记"只有 `Stale()`（`workspace.go:72-73`），没有预算与降级实现。 |
| X-3 | `Router` 作为新角色名 + "官方双层"映射 | ADR-3:142；ADR-14:154；§4.5:354-415 | 无独立会话；仅 `assess` 一次调用 + `Worker` 单层（`runtime/worker.go`） | 术语 + 架构承诺 | **命名可接受，机制未落地**：001:80-81 的 Main/Executor Session 被改称 Router/Worker，ADR-3 说明了代码名；但"Router 全局唯一持久、有全局上下文、eval_read/eval_decide/route 工具"（§3.1:185、§5.3:1034）全缺 → 设计 §11:1841 "§4 领域层 ✅" 与 §10:1742 M2 的"完整 Eval"不成立。 |
| X-4 | `Decision.kind = propose` + `domain.Proposal`（四要素）+ 待批单面板分组 | §4.10:831-878 | `domain/ticket.go:12-15,189-200`；`engine.go:240-244,526-569,636-679`；`ui/overview.go:48-...` | **产品扩展（与 000/001 冲突）** | **未支付的语义债**：见 3.2。它对"代理授权放大"（设计 §4.7:602-614）是合理答案，但把 002 的 explicit/auto 语义、001:35"Only Eval creates one"一起改掉了，且没有同步改 001 的规格。 |
| X-5 | `ReplyPlan{route,purpose,authorizedBy}` + `ReplyMode{notify,assist,auto}` + `ReplyPurpose` | §4.10:810-962 | `domain/ticket.go:41-97`；`engine.go:588-632`；`runtime/reply.go` | 产品扩展（新设置） | **概念可留、实现是空转**：`ReplyPlan` 只进面板展示（`runtime/runtime.go:279`），`deliverReply` 不读它（`reply.go:81-116`）——默认 notify 档并没有挡住任何对外消息。要么让 outbox 发送前置检查 plan，要么删掉这套设置（000:20"few settings"）。 |
| X-6 | `Artifact{Kind,Verified,Detail}` + 自动扫描产物 | §6.10:1468-1517 | `domain/ticket.go:137-161`；`engine.go:804-827`；`runtime/artifacts.go:159-215` | 产品扩展 | **可接受**：000:21 的"结果可用"需要呈现；`Verified` 字段目前只有 bool 无写入口（`grep` 未见赋值），属声明超出实现。 |
| X-7 | `Goal` 实体 + `goal` 表 + 轮次/阻塞状态机 | §5.9:1099（宣称"不需要新概念"）；§5.10:1121 | `internal/goal/goal.go:61-83`；`store/schema_v3.go:68-84`；`runtime/extras.go:126` | **违反 000:19 / 001 精神** | 设计自己说 goal = "Ticket 的 follows + Assignment 继续轮次策略"，实现却另建实体+表+状态机；`Advance/Resume` 等只有测试与 `set_goal` 创建路径调用（`runtime/extras.go`），没有"自动继续轮次"的 driver → 既膨胀又空转。 |
| X-8 | `Schedule` 实体 + `schedule`/`schedule_fire` 表 + Manager | §5.9:1094（应做 Source kind） | `internal/schedule/store.go`；`store/schema_v3.go:35-63`；`cmd/zeokeel/extras.go:97-152` | 产品扩展（偏离设计自己的方案） | 功能上"定时器产 Trigger 进 Engine"是对的（`extras.go:138-152`），但没有做成 `sources.Kind`，而是独立 service+两表+自己的 claim/DST/cadence 逻辑；好处是正确性可控（有测试），代价是 Source 契约（001:27）与面板 Source 页不覆盖它。**可接受但登记为偏离**。 |
| X-9 | `local` Source（面板派活） | §5.9:1095；§6.9:1400-1418 | `engine.go:481-524`（`Dispatch`）；`cmd/zeokeel/*` | 产品扩展 | **可接受**：仍走 `Receive`→Decision→Mandate→Ticket，符合"新增入口用 Source 吸收"；但 `Dispatch` 里"同一句话+毫秒"派生 ID（`engine.go:508`）在原型"内容派生"纪律下是弱幂等（重试可得两个 Ticket），建议把 `at` 换成"发送动作 ID"。 |
| X-10 | `Self`（operator 本人事实）+ `Conversation/Addressed` 事实 | §4.9:764-786；§4.10:820-829 | `domain/trigger.go:5-39,73-78`；`cmd/zeokeel/extras.go` | 产品扩展 | **可接受**：原型没有"自己 vs 同事"的显式事实（只有 allowed list），但它把 002 的"person"具体化成可检查的字段，符合"结构化事实"原则；代价是它成为绕过授权的主路径（`engine.go:360-362`），需配合 X-4 一起收敛。 |
| X-11 | 四个新设置：`execution.concurrency`、`command_timeout`、`model_timeout`、`source.poll_interval` | 设计未列 | `runtime/durable.go:78-127` | 产品扩展（设置膨胀） | `concurrency` 对应原型"并发上限"（合理）；`model_timeout` 对应"Eval 时限"（但语义不同）；`poll_interval` 原型是固定 60s（003/006），做成设置违反 000:20 的"一个默认值够用就不做设置"——建议固定或写进部署文档。 |

---

## 5. 设计自身主张的核对

### 5.1 §4.4 "必须保住的不变量" 12 条 vs 原型与实现

| # | 设计 §4.4 条目（design:339-350） | 原型有无 | 当条是否有测试 | 核对结论 |
| --- | --- | --- | --- | --- |
| 1 | 同一 Trigger 接受两次 → 一个 Ticket | 有（001:28） | `engine_test.go:182` | ✅ 忠实且已测 |
| 2 | 每 Thread 同时最多一个 Eval，期间合并 | 有（001:29、43） | 无 | ❌ 无 Eval 类型，条目是空头承诺 |
| 3 | 人消息优先扇出，`followupOwners` 为空才进 Eval | 有（001:37、002:22） | 仅测"扇出但不开单"（`engine_test.go:124`） | ❌ 无 ownership；且 fan-out 后仍进 Eval |
| 4 | `settled(decision)`：stop/unavailable/带 `stopNotExplicit|stopUnknown|routed` 的不算处理完 | 有（`_ref/src/core/model.ts:93` + `reasons.ts:12,20-21`） | 无对应函数 | ❌ 概念未移植（理由码只留注释） |
| 5 | Ticket 结束 `letGo` 未读人消息；人 stop 的不自动重评 | 有（002:34） | 无 | ❌ 无 letGo、无结束路径触发 |
| 6 | `waitsForPerson` 期间自动输入被扣住 | 有（003:122、002） | 无（`WaitsForPerson` 定义了但没人调用） | ❌ 空函数 |
| 7 | 策略变更不撤销 Mandate，只写 policy-changed Input | 有（002:111-117） | 无 | ❌ 无策略变更处理 |
| 8 | 同 Ticket 同会话出站保序；前者连续失败 3 次才放行 | 有（003:179） | `outbox_test.go:295` | ✅ 基本落地（阈值需逐行核对，推断等价） |
| 9 | `followsDone()` 且 `!unfinished()` 才按 PR 结局结束 | 有（003:100-102） | 无 | ❌ 无 Follows/followsDone |
| 10 | 三个投递回调 delivered/read/turnEnded | 有（003:112） | 无 | ❌ 全缺 |
| 11 | 只有最终 Report 能从 Executor 侧结束 Ticket | 有（001:36） | 部分（`engine_test.go:561` 只测 stage） | ⚠️ Report 从不结束 Ticket（除 dropped），条目与实现相反 |
| 12 | reason/error 一律"码+模板+参数" | 有（`reasons.ts`） | `view/reason_test.go` 存在 | ⚠️ `Reason{Code,Values}` 有，但码表只有 8 个（`domain/ticket.go:28-38` vs 原型 21 个），渲染在 `view/reason.go`（推测等价，未逐条核对） |

**§4.4 相对原型的遗漏（load-bearing 规则没进清单）**：
- "ready 无 PR 即 completed 结束"（003:94）——设计通篇未写，清单也没有；实现因此永远不结束 Ticket。
- "Ticket 结束时把未读消息送回 Eval"只在第 5 条出现，但没有"什么算读完/未读"的依据（`readUntil`/`undelivered` 在原型 `_ref/src/core/model.ts:340-354`，设计未移植）。
- "bot list / admission table 的 observed 限制"（002:71、79）不在清单，也不在 §4.3 的四层里。
- "并发 slot 人的消息 +1"只在 §1.5:113 一句，没进不变量清单，实现就漏了。
- "prepare 失败不重试"只在 §1.5:115 与 §5.5:1053，没进清单。
- **发明**：第 4 条的 `stopNotExplicit|stopUnknown|routed` 虽在原型 `reasons.ts` 存在，但"settled"函数原型只有 `kind ∈ {stop,unavailable}` 两种判定（`_ref/src/core/model.ts:93`），设计把 reason 维度也加进去，属**扩大解释**（可接受，但应标注）。

### 5.2 §11 "实现状态对照" 的 ✅ 抽样核对

| §11 行（design:1839-1859） | 声称 | 实际 | 结论 |
| --- | --- | --- | --- |
| §4 领域层（Trigger/Thread/Decision/Ticket/Mandate/Artifact） | ✅ | **没有 `Thread` 类型**（只有 `engine.thread` 内部结构），没有 `Eval`、`Subscription`、`Input`；`Mandate` 只有 4 个字段 | ❌ 不实 |
| §4.2 阶段查表而非状态字段 | ✅ | `StageOf` 存在，但缺 delivery 行、不判 `waitsForPerson`（`domain/ticket.go:267-291`） | ⚠️ 半真 |
| §4.3 gate + admission + accept mode | ✅ | 无 admission table、无确定性 strength、无 bot list；accept mode 被 propose 拦截（`engine.go:240-244`） | ❌ 不实 |
| §4.4 不变量 | ✅（"11 条全部有测试"） | 见 5.1：约 3 条有测试；`design/M0-tasks.md:23` 自己写"12 条不变量中的 1/3/10/11 尚未写" | ❌ 与项目自己的 M0 记录矛盾 |
| §5.2 会话与轮次（`adk.Runner`+`session.FileStore`+`Resume`） | ✅ | `agent/agent.go:246-252` + `session.go:23-123` 确实存在 | ✅ 基本属实 |
| §5.6 子智能体 | ✅ | `runtime/worker.go:361-380` 两个 subagent + eino 中间件 | ✅ 属实（但与 003:39-47 的 subagent 生命周期无关） |
| §5.10 能力矩阵（MCP/skill/定时/目标/搜索/workflow） | ✅；"定时与目标由 agent 创建并在触发页可见" | MCP/skill 有实现；schedule 会被 agent 创建、由 `startScheduleLoop` 触发（`cmd/zeokeel/extras.go:97-136`）**但 Trigger 的 `Actor{"schedule"}`、`Human:true` 会被 gate 当作普通人类消息**（`extras.go:141-151`），而 `goal` 只有创建/状态机，没有"自动继续轮次"的 driver（`grep "\.Advance("` 仅测试） | ⚠️ 定时半真、goal 不实 |
| §7 持久化与 Effects/Outbox | ✅ | effects/outbox/store 确有实现，测试较全；但 `Source 游标` 未落 kv（`cmd/zeokeel/github.go:58`），`ticket.stage` 是缓存列（`store/schema.go:57`） | ⚠️ 大方向属实，细节不实 |
| §9 零 cgo | ✅ | `scripts/check-nocgo.sh` 存在（本次未运行，见 §6） | 未验证 |

**结论**：§11 的 ✅ 表整体不可作为"已完成"的依据；至少 §4、§4.3、§4.4、§5.10(goal)、§7(游标) 五行应降级为 🟡/❌，并补"领域/授权/执行的真实缺口"。这与 `design/M0-tasks.md:17-33` 的自我记录也矛盾——M0 记录是诚实的，§11 反而更乐观。

---

## 6. 覆盖与不可验证项

**已完整阅读**：
- 需求：`_ref/docs/000-philosophy.md`（33 行全文）、`001-domain.md`（112 行全文）、`002-authorization.md`（155 行全文）、`003-execution.md`（203 行全文）。
- 设计：`design/zeokeel-go-mygo-design.md` 全文 1861 行（§0–§12 + 附录 A/B，含 §4.5/§4.6/§4.7/§4.10/§5.x/§6.x/§7/§11）。
- 代码：`internal/domain/{ids,trigger,ticket,workspace}.go` 全文；`internal/engine/{engine,router}.go` 全文（`persist.go` 抽样）；`internal/assess/{assess,prompt}.go`；`internal/runtime/{worker,durable,reply,artifacts}.go` 与 `runtime.go`/`pages.go` 相关段；`internal/prompts/defaults.go`；`internal/store/{schema,schema_v3}.go`；`internal/outbox/outbox.go`（结构 + 关键函数）；`internal/effects/effects.go`（结构 + Options/Tests 名称）；`internal/agent/{agent,session,tools}.go` 相关段；`internal/sources/{source.go,github/{scope,poll}}.go`；`internal/im/addressing.go`；`internal/goal/goal.go`、`internal/schedule/store.go` 结构；`cmd/zeokeel/{main,github,assess,extras,durable}.go` 相关段；测试清单（`engine_test`、`persist_test`、`outbox_test`、`effects_test`、`stages_test`、`worker_test`、`assess_test`、`domain_test`）。
- 第二需求源：《Super-agent 设计探索》中与本切片相关的 13 条规范要点（由 Lead 转述；设计 §4.5:356-360 引用了同一篇的"Router 只分派，Worker 只干活"），逐条对照见 §8。
- 原型代码交叉核对（仅用于澄清文档歧义）：`_ref/src/core/{stage,ids,model,tickets,reasons}.ts`、`_ref/src/admission.ts`、`_ref/src/policy.ts`（抽样）。

**未验证/无法验证**：
1. **零 cgo 守卫**：未运行 `scripts/check-nocgo.sh`（审计为只读，构建产物会污染工作区；且非 000–003 语义）。§11 的 ✅ 未核实。
2. **真实模型路径**：`assess.NewEinoGenerator`、`agent.RunSession` 的真实行为未跑（需 API Key）。关于"模型会遵守 `strength` 指令"的判断来自提示词文本与校验代码，属**推断**。
3. **`outbox.behind` 的"连续失败 3 次才放行"**：读了结构（`outbox.go:300-334`）与测试名，但没有逐行核对失败计数与放行条件（推断：有等价实现，标 PARTIAL 而非 CONFORM）。
4. **`view/reason.go` 的码表渲染**：只看到测试存在，未逐条比对 21 个原型 reason code 与 Go 侧 8 个码的映射完整性（推断：覆盖不足）。
5. **Feishu/DingTalk 驱动**：未逐行审，只确认 `im.AddressedFor` 与 `Self` 事实；003:27-29 的 preset 语义无从对应（项目无 preset）。
6. **003:203（存储的坏 Source 配置禁用实例）**：未找到运行时路径，也未穷尽 `runtime/onboarding.go` 与 settings 保存路径，故未列入矩阵（未验证，不判定）。
7. **`runtime/pages.go` 的 Eval 页**：只确认 `DecStop/DecUnavailable` 有渲染分支；面板无法"重新评估"（`ui/app.go` 无该命令）。
8. 未读：`internal/{skills,mcp,workflow,scan,brief,ui}` 的全量实现（与 000–003 相关性低，只在 EXTRA 判断里抽样）。

---

## 7. 按优先级排列的一致性风险

| 优先级 | 风险 | 证据（doc:line vs file:line） | 触发的症状 |
| --- | --- | --- | --- |
| **R1** | **strength 由模型声明，accept 安全阀可被模型输出绕过；admission table/bot list 不存在** | 002:11-16,50-54,101 vs `prompts/defaults.go:74`、`engine.go:216-221`、`engine.go:338-369`、`engine.go:340-344` | 越权（该观察的自动开单）、不该接的通知被接、审计不可解释 |
| **R2** | **所有同事 accept 被强制变成待批单，`auto`/explicit 语义失效；Ticket 由批准而非 Eval 创建** | 001:35、002:89,99,101 vs `engine.go:240-244`、`engine.go:636-679`、`engine_test.go:493-497` | operator 成为瓶颈；000 的"take as little human attention as possible"反噬；observe 模式名存实亡 |
| **R3** | **订阅 fan-out 后同一条消息仍进 Eval，且 ContextInput 从不投递给 Worker** | 001:37、002:20,22 vs `engine.go:163-211,289-318`、`runtime/worker.go:476-484` | 重复评论/重复 PR；后续讨论丢失；`followupOwners` 类冲突 |
| **R4** | **Ticket 没有结束路径（ready 不结束、无 PR follows、无 stop），并缺 delivery 阶段与 waitsForPerson，Attention 无法回答** | 003:9-21,94,98-106,129 vs `engine.go:851-859`、`domain/ticket.go:267-291,312-320`、`engine.go:727-739`、`ui/app.go:32-35` | 所有完成的工单永久卡在"等待你评审"；面板/列表无限增长；"需要我处理"永久为真 |
| **R5** | **人消息没有额外 slot、无 retry/pause/stop，排队工单不可恢复；launch 被 effects 自动重试（与"failed launch 不自行重试"相反）** | 003:55-61,129-143,194-200 vs `runtime/durable.go:78-94`、`durable.go:41-45`、无 retry/pause 命令 | 人的急事被新任务饿死；失败/阻塞工单只能人工重启进程；重试语义与原型相反 |
| **R6** | **Eval 无聚合/无批次/无截止时间/无 unavailable，模型异常时 Trigger 静默丢失** | 001:29,33、002:147-155 vs 无 `Eval` 类型、`assessor` 错误直接上抛（`engine.go:182-184`、`assess.go:126-131`） | 丢失请求；无 health 提示；观察模式下模型超时等于无声失败 |
| **R7** | **执行面缺 prepare/执行变量/凭据隔离，Worker 继承 `os.Environ()`** | 003:51-53,65-76 vs 无 prepare、`agent/tools.go:251-253` | 多仓库目录隔离靠约定；提示"不含凭据"的环境反而可能泄漏；"prepare 失败不重试"无法验收 |
| **R8** | **多工作区授权未按目标工作区收窄（gate 用所有工作区 allowed 的并集）；IM Trigger 的 scope 用 `ws:` 而非 `global`** | 001:32、设计 §4.5:385、:1776 vs `engine.go:363-367`、`cmd/zeokeel/extras.go:194` | 跨仓库越权（A 仓库被授权者可要求 B 仓库干活）；幂等命名空间与设计文档不一致 |
| **R9** | **§11 与 §4.4 的 ✅/测试声明不实，M0 记录与 §11 自相矛盾，`design/rejected-concepts.md` 不存在** | design:1844 vs `design/M0-tasks.md:23`；design:108 vs `ls design/` | 后续开发者被状态表误导，把"已完成"的假设叠上去；被拒绝概念纪律失守（Goal/Schedule 实体即后果） |
| **R10** | **ReplyPlan/ReplyMode 是空转设置（只展示不执行），默认 notify 不挡任何外发** | 002:5、000:12/20 vs `engine.go:588-632`、`runtime/reply.go:81-116`、`runtime/runtime.go:279` | 对外说话的授权边界形同虚设，且多了一组无价值设置（注意力成本） |

> R11–R13（官方 Super-agent 架构相关，见 §8.4）不在本表内，以避免与第二需求源重复计数。

---

*证据约定：`doc:line` 指 `_ref/docs/*.md` 行号；`design:line` 指 `design/zeokeel-go-mygo-design.md` 行号；`file:line` 指项目源码行号。未运行需要网络/模型/窗口的命令；结论均来自阅读。*

---

## 8. 对官方 Super-agent 架构的符合性（第二需求源）

> 需求来源：用户提供的《Super-agent 设计探索》中与本切片（000–003 + engine/agent/runtime）相关的规范条目；设计文档 §4.5:356-360 也引用了同一篇文章的"Router 只分派，Worker 只干活"。下方引文按文章要点转述（非逐字），代码/设计证据照旧。这一节独立计数，不并入 §2 的 68 条。

### 8.1 判定计数

| 来源 | 条目数 | CONFORM | PARTIAL | MISSING |
| --- | --- | --- | --- | --- |
| 官方 Super-agent 架构 | 13 | 1 | 8 | 4 |

### 8.2 需求矩阵（O-1 – O-13）

| ID | 官方要求（要点） | 判定 | 设计证据 | 实现证据 | 严重度 | 备注 |
| --- | --- | --- | --- | --- | --- | --- |
| O-1 | **连续**：同一个 agent 贯穿 IM/GitHub，不切换会话；一个连续的上下文/记忆 | PARTIAL | §3.1:185（一个全局 Engine + 一个 Router 会话）；§6.3:1272-1278（单一消息流） | `cmd/zeokeel/main.go:230-245`（单进程装配 Engine/RT/UI）；`runtime/worker.go:261`（会话按 `worker-<N>` 分） | S1 | 产品层"一条流"✅；**没有跨 Ticket 的连续会话/全局记忆**——每个 Ticket 一个新 Session，Router 会话不存在（见 O-5）。 |
| O-2 | **高效**：并行任务运行时仍能响应；不阻塞在上一条命令上 | MISSING | §1.1:42-48（注意力成本）；§6.1:1156-1162（主线程不阻塞）；设计未写并发/锁纪律 | `engine.go:152-154`（`Receive` 全程持 `e.mu`）；`router.go:95-97`（在锁内调 `assessor.Assess`，超时 90s：`assess.go:75-77`）；`ui/input.go:36-43`（主线程同步 `cmd.Dispatch`）；`runtime/runtime.go:484-519`（Dispatch 不带 workspace → 必走模型） | S1 | **UI 主线程会被一次 Eval 的模型调用冻结最长 90 s**；`input.go:32-33` 注释"Dispatch is memory-only, so it cannot stall a frame"与现实相反。IM/GitHub 侧的消息也在全局锁内串行，一条慢判定阻塞所有 Source 与所有命令。这同时违反 003 的"并发 slot"精神与 000 的注意力原则。 |
| O-3 | **主动**：观察环境事件，规则允许时主动行动，否则保持安静 | PARTIAL | §4.3:330（accept mode）；§4.9:795-798（宁可漏接） | `engine.go:213-235`（observe/auto）；`engine.go:240-244`（同事请求一律 propose） | S1 | 观察✅、默认安静✅；但"规则允许时主动行动"被审批关卡架空：`auto` 也不开单（见 R2）。 |
| O-4 | **持久**：always online，崩溃/重启自动恢复 | PARTIAL | §5.8:1074-1082；§7.2:1541-1544 | `runtime/durable.go:20-46`（launch effect）；`internal/store/*`（SQLite + schema 守卫）；`internal/agent/session.go:23-123`（会话续跑）；`cmd/zeokeel/main.go:238-245`（`Drain: rt.WaitIdle`） | S2 | durable effects + 会话恢复是真的；但 Assignment/Input 不恢复（E-17）、无 watchdog/服务化（设计 ADR-1 托盘）；"always online"在桌面进程依赖用户不关机（设计 §11 风险 5 自认）。 |
| O-5 | **Router = 唯一的持久全局 LLM Session**：轻量模型、全局上下文/记忆、承接所有人类交互、路由与派发、统一对外回复；**不执行任务、可以没有 Bash**；Router 回合中到达的 Trigger 累积合并到下一回合 | MISSING | §3.1:185（一个 Router 会话）；§5.2:1020-1025（两级投递）；§5.3:1034（orchestrator 工具：`eval_read/eval_decide/route/reply`）；§10:1742（M2 完整 Eval） | `engine/router.go:15-17,52-124`（Router 退化为一次性 `Assessor.Assess`）；`cmd/zeokeel/assess.go:23-42`（每次 `newAssessor` 一个无状态对象）；无 orchestrator/router 会话（`grep -i orchestrator internal/agent` 为空）；`runtime/reply.go:37-79`（回复是模板，不是 Router 写的） | S1 | 五条要求全缺：没有持久会话、没有全局记忆、没有回合内 Trigger 累积/合并（D-6）、没有统一回复的 Router、也没有 Router 的工具集。设计 §11:1841/§10:1742 的"完整 Eval（持久 orchestrator 会话 + eval_read/eval_decide/route 全工具）"**不成立**。唯一"符合"的是"不执行/无 Bash"——因为根本没有 Router。 |
| O-6 | **Worker = 每 Ticket 一个上下文隔离 Session**：完整执行工具、单一任务、**不直接对外说话**、向 Router 汇报；可透明包含 subagent/agent-team | PARTIAL | ADR-3:142；§5.3:1033；§5.6:1058-1064 | `runtime/worker.go:246-290`（每 Ticket 一个 `RunSession`，system prompt 只含本工作区）；`agent/tools.go:34-153`（bash/read/write/edit/glob/grep）；`runtime/artifacts.go:20-113`（对外只有 report/present/ask，无 reply 工具 ✅）；`runtime/worker.go:361-380`（explorer/verifier subagent） | S2 | 隔离✅、完整工具✅、不直连渠道✅、subagent 部分✅；**"向 Router 汇报"不成立**（汇报进 domain/engine，Router 不存在）；agent-team 未做（设计 §5.10:1125 明示 P2 不做）。 |
| O-7 | **事件驱动 + 两级过滤**：确定性程序 Fast Path（source/scope/whitelist），再交给 LLM Slow Path 做语义判断 | PARTIAL | §4.9:734-738（事实层/配置层/语义层三层）；§4.3:328 | `internal/sources/github/scope.go:17-38`（确定性 scope）；`im/addressing.go:12-18`；`engine/router.go:86-97`（Slow Path 一次模型调用） | S2 | 两级结构✅；但 Fast Path 只有 repo/scope/allowed list，缺 admission table/bot list（A-5/A-12），且"确定性"没有产出 strength（A-3）→ Fast Path 的授权结论要由 Slow Path 的模型再猜一次。 |
| O-8 | **Thread = 事件流；Router 为 Thread 首个事件建 Worker 后，Worker 订阅该 Thread，后续事件直接投递给 Worker（绕过 Router）**；订阅多对多；Worker 可退订/交回；Router 仍观察全部事件，只派发未被订阅的 | MISSING | §4.4-3:341（`followupOwners`）；§4.9:759；§5.2:1022 | `engine.go:289-318`（fan-out 只追加 `ContextInput []string`）；`runtime/worker.go:476-484`（Worker 只拿 Origin 文本）；无订阅投递/退订/交回工具（`runtime/artifacts.go:20-113`）；`engine.go:167-211`（所有消息仍进 Eval，Router 并未"只派发未订阅者"） | S1 | 这是官方架构与 001/002 的**同一条**要求（订阅=直接交付+不授权）。实现只做到"记下文本"，**从未投递给 Worker**，也没有 Router 侧的"已订阅则跳过"，因此既丢上下文又重复 Eval。 |
| O-9 | **Router 统一对外回复；Worker 绝不直接输出到渠道** | PARTIAL | §4.10:810-962（ReplyPlan）；§6.3:1231-1233 | `runtime/artifacts.go:20-113`（Worker 无 reply 工具 ✅）；`runtime/reply.go:37-79`（回复由 runtime 模板生成）；`engine.go:588-632` + `reply.go:81-116`（ReplyPlan 不参与投递） | S2 | "Worker 不直接说话"✅（且这是本项目做得对的一处）；但"统一回复"没有 Router 承担，模板化回复 + 忽略 ReplyPlan 使对外语气/授权不可控。 |
| O-10 | **Inline & Spill**：注入 ID+摘要；就近内联相关对象；长内容落盘并给按需读取句柄 | PARTIAL | §4.6:433-468（brief inline / `workspace_read` spill）；§5.4:1039-1046 | `internal/brief/*` + `engine/router.go:86-94`（brief 内联）；`internal/agent/truncate.go:14-40`（工具输出落盘 + reduction 中间件）；无 `workspace_read`/`read_domain` 工具（`runtime/artifacts.go:20-113`） | S2 | brief 内联✅、工具输出 spill✅；**领域资源的"摘要+计数+next_offset"分页读取**（设计 §5.4:1044 称为"原型控制成本的关键设计"）完全没有。 |
| O-11 | **Chain-of-Recall**：每个对象内联下一跳 ID，模型可顺 Trigger→Thread→Worker Session 找回旧任务 | PARTIAL | §4.5:413-415；§4.6:468 | `domain/ticket.go:235`（`Origin`）、`151-161`（`Artifact.Ticket`）、`trigger.go:48-52`（`ThreadRef`）；Worker session 名在运行时按 Ticket 号拼（`runtime/worker.go:261`），**未进领域模型** | S2 | ID 链在数据层基本齐全；但模型没有可调用的"顺着 ID 读"工具（无 `read_domain`），Worker Session 与 Ticket 的关联也不可被模型检索 → "链式召回"只对人（面板）成立。 |
| O-12 | **全双工异步 ReAct**：昂贵工具 `start()`/`stop()` 返回 "Started"；后台结果作为新事件注入；新的用户输入在当前推理步之后即可处理，不必等后台命令 | MISSING | §5.2:1020-1025（steer/inject/followup）；§5.6:1058-1064（5s 转后台、job 唤醒）；§4.4-10:348（三段式投递） | `agent/tools.go:34-153`（bash 同步执行，无 jobs 管理）；无 steer/inject/turnEnded（`internal/agent/session.go` 只有事件日志与修复）；`runtime/worker.go:276-290`（一次 `RunSession`，回合内无新输入通道） | S1 | 003:27 的"5 s 前台转后台"、003:33-39 的后台占 slot/唤醒、003:112 的 delivered/read/turnEnded 全部建立在这条能力上；缺失使 E-4/E-14/E-15 无法实现，也让"高效"在 Worker 侧同样不成立。 |
| O-13 | **Trigger = 半结构化的环境事件** | CONFORM | §4.9:764-786；§4.1:264-276 | `domain/trigger.go:61-89`（`Trigger` 含 Source/Actor/Addressed/Conversation/Text/Channel/Threads 事实）；`domain/trigger.go:91-108`（`Disposition` 审计） | S3 | 结构事实✅（含 @everyone/ReplyTo 的正确边界，`im/addressing.go`）。 |

### 8.3 与 000–003 结论的交叉影响

1. **O-2 + O-5 让"高效"与"Router 单会话"同时落空**：`Engine.Receive` 在全局互斥锁（`engine.go:152-154`）内调用真实模型（`router.go:95-97`），且 UI 主线程同步调用 `Dispatch`（`ui/input.go:36-43`）。这解释了为什么 002 的"Eval 截止时间/重试"（A-25）无法实现：当前设计里"Eval"就是一次阻塞调用，没有可取消、可截止、可批处理的边界。修复顺序建议：先把 Eval 改成 Engine 命令 + 队列 + goroutine（锁内只做状态迁移），再加 deadline/attempts；Router 会话之上再谈"回合内合并"。
2. **O-8 与 001 D-14/D-15、002 A-8/A-9 是同一件事的两种表述**：官方的"Worker 订阅 Thread、事件直达、可退订"正好是原型 `followupOwners`+`Subscription`+`letGo` 的来源。本项目在 `engine.fanoutLocked` 里只保留"追加文本"这一半，另一半（直达 Worker / 退订 / 交回）缺失，导致 R3（重复评论）与"后续讨论丢失"同时存在。
3. **O-12 是 003 执行语义的前置条件**：没有异步 ReAct，就没有"回合结束但后台仍在跑"、没有 handoff、没有 30 分钟规则、没有 turnEnded 三段式。设计把它列在 M4（§10:1743），但 003 的语义是"必须全部保留"（§1.2:63），因此这是一处**排期与范围声明的自相矛盾**，不只是实现延后。
4. **O-6 的"Worker 不直接对外说话"是本项目少数结构性正确之处**：Worker 工具集没有 reply（`runtime/artifacts.go:20-113`），回写统一走 `deliverReply`/outbox；但"统一"目前是模板而非 Router，若补 Router 会话，这条应当保留为硬约束（设计 §5.3:1034 已如此要求）。
5. **对官方架构的总判定**：13 条中 1 条 CONFORM、8 条 PARTIAL、4 条 MISSING。**"官方双层"在本项目里只落地了"Worker 一层"**：Router 被降级为一次性 assessor 调用；设计 §4.5 与 §1.5 的"1:1 映射"（ADR-3:142）不成立。这是"产品扩展/未完成实现"而非故意违反，但必须在 §11 与 README 里如实降级，不能继续用"官方双层"作为已实现论据。

### 8.4 追加风险（并入 §7 排序）

| 优先级 | 风险 | 证据 | 症状 |
| --- | --- | --- | --- |
| **R11** | 一次 Eval 的模型调用持有全局 Engine 锁，UI 主线程同步调用 Dispatch | `engine.go:152-154`、`router.go:95-97`、`ui/input.go:36-43`、`runtime/runtime.go:484-519` | 面板最长冻结 90 s；所有 Source/命令串行；"并行任务运行时仍能响应"不成立 |
| **R12** | 无 Router 持久会话/全局记忆/回合合并/统一回复；13 条官方要求里 4 条 MISSING，Router 层整体缺失 | `engine/router.go:15-17`、`cmd/zeokeel/assess.go:23-42`、`runtime/reply.go:37-79` | 跨会话连续性、跨工作区决策、统一语气与审计均无载体；设计/文档的"官方双层"表述误导后续排期 |
| **R13** | 无异步 ReAct（start/stop、后台注入、steer/inject、三段式投递） | `agent/tools.go:34-153`、`internal/agent/session.go`、`runtime/worker.go:276-290` | 003 的 5s 后台/30 分钟/handoff/turnEnded 全部无法实现，Ticket 只能"一次跑完" |
