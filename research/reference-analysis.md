# 参考原型分析：dsh-superagent → Go 1:1 移植 + 自研 Agent 运行时

> 对象：`/data/ZeoKeel-Super-Agent/_ref`（`dsh-superagent` 0.1.0，MIT，README 自述为研究原型）。
> **定位已确认**：完整移植 GitHub/Feishu → Eval → Ticket → Executor → PR 全链路，只把技术栈换成 Go + mygo 原生 UI；不再依赖 DSH。
> 因此 §1–§6 是**必须 1:1 对齐的语义**（领域/状态机/授权/提示词/持久化），§7–§10 是**自研 Agent 运行时必须补齐的能力**（工具契约、提示词里的工具名依赖、会话/后台语义、可靠性），§11–§13 是宿主解耦、桌面化与取舍，§14 是决策清单。

## 0. 一句话结论

原型 = **确定性领域状态机（Engine + SQLite）+ 两个 LLM 角色（orchestrator / worker）+ Source 适配器 + durable 副作用队列（Effects）+ 幂等出站队列（Outbox）**。LLM 从不拥有状态：授权、幂等、状态迁移全在代码里，Agent 只通过 `orchestrator_*` 工具提交判断与请求。**这是移植时最该保留的架构决策**；真正需要重写的是它脚下那块 DSH 运行时。

## 1. 领域模型（docs/001 + `src/core/model.ts`）

| 概念 | 定义 | 关键约束 |
| --- | --- | --- |
| Source | 事件流：`kind`（可复用实现）+ 一个实例配置 | 持自己的读取游标与 bot 身份；先过滤自身消息与范围外事件；把原生事件翻译成 Trigger |
| Trigger | 一条不可变事件 | 属于 1..N 个 Thread，`threads[0]` 为主线程（承载 Eval）；同 Trigger 接受两次只产生一个 Ticket |
| Thread | 按时间排序的 Trigger | **同 Thread 同时最多一个 Eval**；可多个 Ticket；持 subscriptions / 运行中 Eval / 决策历史 |
| Correlation | 一个 Trigger 同属多个 Thread | 只加语境，**不路由** |
| Channel | Thread 的写侧（地址由 Source 定义） | 一个 Thread 可多个 Channel；Ticket 持 Channel 集合 |
| Policy | 版本化规则 + 每个 Source 的部分（allowed、admission、bot list） | 版本变 → 丢弃运行中 Eval 重排队 + 给受影响 Ticket 发 policy-changed；**不撤销 Mandate** |
| Eval | 对一批 Trigger 的判断（gate 确定性 + assessment 模型层） | 无状态、串行；期间到达的 Trigger 合并进下一批 |
| Mandate | accept 授予：strength、policyVersion、basis、actor、scope | **不可变**；只有 accept 建、人 stop 撤、revalidate 失败吊销；扩权需新 Ticket |
| Ticket | 授权的工作单元 | 只能由 Eval 创建；自定状态（不跟随 Session）；open ↔ ended（人的消息可 resume） |
| Assignment | Ticket 与一个 Executor 实例的绑定 | 一个 Ticket 可有多个；`pending→running→ended` 独立于 Ticket；只有最终 Report 能从 Executor 侧结束 Ticket |
| Subscription | Ticket ↔ Thread 多对多（由 Thread 持有） | 匹配 Trigger **无条件**扇出给存活订阅；人的消息走扇出不进 Eval；被 stop 的 Ticket 收不到任何东西 |
| Executor | 运行 Assignment 的端口：每 Ticket 一个 Session + 一个目录 | 在领域外，仅通过 Assignment 接入 |
| Input | 投递上下文：扇出 Trigger、policy-changed、resumed、text、note | `note` 是语境，不授予权限 |
| Report | Assignment 向 Ticket 上报 | `progress/waiting/ready/blocked/failed/dropped` |
| Attention | Ticket 上等人回答的问题 | 由结构化 `human_action` 产生，**不从自由文本推断** |
| Disposition | Trigger 去向记录 | 支撑幂等、follow-up 归属、hand-back |
| Scope / Strength | 允许范围 / 授权强度 | `explicit` / `inferred` |

聚合：Source 持读取位；**Thread 持 subscriptions + 运行中 Eval + 决策历史**；Ticket 持 assignments/channels/inputs 并以 id 引用 Decision。`Thread.context[]` 是"仅语境订阅"的 Ticket（消息照投，但不授予 follow-up 归属、不 resume）。`Ticket.follows[] = {thread, own, closed{at,outcome}}` 表示 Source 令其跟随的 Thread（典型是 PR）：`ready` Report 只有 `followsDone()` 成立且 `!unfinished()` 时才结束 Ticket，并按最后一次 close 的 outcome（`merged`/`closed`）结束。

被明确拒绝的概念：Principal/Membership 实体、跨 Source 身份映射、Eval 自调度延时 Trigger、waiting/revoked 的 Eval、每 Thread 单 Ticket、"append to Ticket" 决策、Subscription 上的准入、跟随 Session 的 Ticket 状态、Executor 声明"人要求停"、策略变更时取消 Ticket、失败即锁死工具。

### 1.1 状态机

**Ticket 阶段**（`src/core/stage.ts`，自上而下首个匹配；也是 `GET tickets?state=` 的过滤值）：

| stage/action | 条件 | 面板 |
| --- | --- | --- |
| `ended` | `ticket.ended != null` | — |
| `person/delivery` | `ticket.delivery`（Input 投递失败，2s 起指数退避到 1min） | Input undelivered |
| `person/answer` | `ticket.attention` | Awaiting answer |
| `person/review` | 最新 Assignment 的 report=`ready` | Awaiting review |
| `person/blocked`\|`failed` | 最新 report ∈ {blocked,failed} 且此后无人消息（`waitsForPerson`） | Blocked · Failed |
| `working/queued` | 无 Assignment 或最新为 `pending` | Queued |
| `idle` | 最新 report=`waiting` 或最新 Assignment 已 `ended` | Waiting for a Trigger |
| `working` | 其余 | Working |

`canRetry = !ended && (waitsForPerson || delivery)`。人的新消息使 blocked/failed 回到正常阶段（通常 `working`）。

**Ticket 结束**：`ended{at, by: executor|human, outcome: completed|merged|closed|dropped|stopped, reason?}`。`ready` 无 follows → `completed`；有 follows → 等 `followsDone` 按其 outcome；`dropped` → 结束（默认无消息）；PR 全关 → 按最后 close outcome，保留 Subscriptions；人 stop → 撤回 Mandate、此后不接收任何输入。resume：清 `ended` → `policy.revalidate(mandate)` → 通不过则 `refuse`（放回未读消息、永不 resume）；通过则新建 Assignment 并在队首插 `resumed` Input（**无 Eval、无 Decision**）。

**Assignment**：`pending→running→ended{endedReason}`。**Decision**：`kind ∈ {accept, stop, ignore, unavailable}`（类型里还有历史值 `reopen`）。**Eval**：`{id, threadKey, triggerIds[], primary, strength, actor, basis, policyVersion, startedAt, delivered?, deadline, pausedAt?}`；超时默认 120s（30–600）；连续第 3 次超时记 `unavailable`；未投递到 main 的 Eval 永不过期；pause 期间不超时且 deadline 顺延。

### 1.2 ID 方案（`src/core/ids.ts`）

`key(...parts) = sha256(JSON.stringify(parts)).slice(0,32)`，**内容派生、等输入等 id** —— 全部幂等的基础，Go 版不要换成 UUID。

| 对象 | 生成 |
| --- | --- |
| Trigger | Source 决定（如 `key('github', comment.id)`；编辑类事件把 `updated_at` 纳入 id） |
| Thread key | `` `${source}:${id}` ``（`github:12`、`feishu:<chat>:<root>`） |
| Eval | `key('eval', threadKey, policyVersion, at, ...pending)`；另有 `'ignored'`（gate 直接忽略）与 `'direct'`（绕过队列的直接评估，含 kind/ticketId） |
| Decision / Ticket | `key('decision', evalId)` / `key('ticket', evalId)`（id 层保证"一 Eval 最多一 Ticket"） |
| Assignment | `key('assignment', ticketId, String(len))`（序号即版本） |
| Input | `key('input', ticketId,'trigger',triggerId)` / `'note',at,text` / `'resumed',triggerId` / `'policy',from,to,at` / `'restart',assignmentId` |
| Attention / Ack / reply | `key('attention',ticketId,question,at)` / `key('ack',triggerId)` / `key('reply',triggerId|ticketId,text)`、`key('attention-reply',attentionId)` |
| Session id | main = `orchestrator-<key(stateDir)>`（存 KV `master-id`）；worker = `worker-<ticketId>` |
| 投递给 Session 的消息 id | `MessageId('dsh-superagent:'+requestId)`（跨重启去重） |

### 1.3 最容易翻错的不变量

1. 人的消息**尽量不进 Eval**：先扇出，只有 `followupOwners` 为空才入队。
2. `settled(decision)`：stop/unavailable、以及带 `stopNotExplicit/stopUnknown/routed` 的决策不算"已处理完"。
3. Ticket 结束时按 `letGo` 放回：仅"未投递/未读 + 未 ack/answer/hand-back + 未被 settled"的人消息回 Eval；stop 结束的那条不自动重评。
4. `waitsForPerson` 期间自动输入（CI/bot/note）被扣住但不丢弃；失败启动也不自重试。
5. 同一 Ticket 对同一会话的出站消息保序：后者等前者，直到前者连续失败 3 次。
6. 策略变更不撤销已发 Mandate，只在 revalidate（resume 前）或 policyEffects 计算时生效。

## 2. 授权模型（docs/002 + `src/policy.ts` + `src/admission.ts`）

两条路径：**扇出**（无条件给订阅 Ticket 作为 Input，不授权、不做准入）与 **Eval**（仅当无人拥有该消息）。

来源声明：`addressed: 'mention'|'direct'`、`human`（人 vs bot/CI/自身）。机器消息先查 bot list（命中最多 `inferred`，否则拒绝）；人的消息按 allowed + admission table + 是否 follow-up 组合判定：

| strength | 条件（`settingsPolicy().gate`） |
| --- | --- |
| `explicit` | 名单内 + addressed；或 admission cell=`explicit`；或名单内的人在已有 Ticket 的 Thread follow-up |
| `inferred` | 名单外的人 follow-up 且 mode≠off；admission cell=`inferred` 且 mode≠off；bot list 中 bot 且 addressed 或 `scope.observed` |
| 不准入 | 其他 bot；admission=ignore；`off` 下所有 inferred；reason：`not_human`/`not_a_member`/`no_mention`/`admission_ignored`/`source_disabled`/`source_not_installed` |

- **explicit 优先于 follow-up**：unlisted 的 explicit cell 在 Thread 有 Ticket 时仍 explicit，且任何 mode 都工作。
- follow-up 例外：`addressed==='direct'` 且发送者不在名单 → 不算 follow-up；`off` → 只由表决定。

**Admission table**：行=接收范围（Feishu 群/私聊；GitHub 新 Issue、新 PR、评论、默认分支 CI），列=三个 cell：

| cell | 取值 | 默认 | 可用条件 |
| --- | --- | --- | --- |
| `listedUnaddressed` | inferred, ignore | inferred | 需 `observed=true` |
| `unlistedAddressed` | explicit, inferred, ignore | ignore | 总是 |
| `unlistedUnaddressed` | inferred, ignore | ignore | 需 `observed=true` |

CI 无 sender → 只提供最后一格。`SourceScope.scope(trigger) → {listened, observed, row?}`；`row` 必须对齐 Policy 行 id。表属于 Policy 版本；弱化 cell 不停已开 Ticket，但 resume 前 revalidate 会吊销。

**Accept mode**：`off` / `observe`（默认，正向结论记 `ignore`+reason `observed`，audit 带 `would_accept`+`proposal`）/ `auto`（直接 accept）。每 Session 只读 Agent：每个 enabled rule **必须恰好评估一次**；accept 还需方向已定（`implementation_requested && target_basis≠undetermined && !requires_design_decision`）、至少命中一条 enabled rule、`intent_evidence` 非空、`worktree_name` 合法（1–4 个小写 kebab 词 ≤32 字符）。命中规则名+instruction 注入该 Ticket 的 worker 提示词并覆盖默认 ack/沟通要求。`admitsInferred(rules)= mode==='auto' && rules.length>0 && 全部 enabled`。

**Mandate**：`{strength, policyVersion, basis, actor, scope}`；`basis = Received{source, scope, row, sender?, rules?} ∪ via`：

| via | 含义 | revalidate |
| --- | --- | --- |
| `addressed` | 名单内的人 @ | 发送者仍需在名单 |
| `followup` | 已有 Ticket 的 Thread 上跟进 | explicit 直接过；inferred 需 auto + 规则 enabled |
| `admission` | 表格某 cell | 现表该 cell 仍授予 ≥ 原 strength（sender 名单变化会移动 cell）；inferred 还需 auto + 规则 |
| `listed` | 早期版本隐含依据 | 发送者仍需在名单 |
| `bot` | bot list | bot 仍在列表 + auto + 规则 |

另：Source 必须仍 `listened` 原 Thread（不要求 `observed`）。失败 → Ticket 保持 ended、拒收等待消息，那些消息按"未到任何 Ticket"重走 Eval。

**Reason**（`core/reasons.ts`，码+模板+可选 triggerId，`reasonText` 渲染，面板按 `reason.<code>` 本地化）：`said`、`timeout`、`policyChanged`、`noAdmissible`、`startedTicket`、`acceptedBefore`、`routed`、`explicitRouted`、`humanStop`、`legacyImport`、`forwarded`、`senderNotMember`、`retried`、`hostRestarted`、`stopUnknown`、`stopNotExplicit`、`directionUnsettled`、`noRuleMatched`、`ignoredFromCard`、`observed`（另有 gate/scope 自带字面码）。

**Policy version** = `key('mandate-v3', JSON.stringify({rules: policy.rules, mode: eval.mode, sources: policy.sources}))`（模型/并发/pause/轮询间隔不参与）。`policyEffects(tickets, old, now)` 只给受影响的 open Ticket 写 Input（`rules[].change`、`requesterAllowed:false`、`proactive:false`），运行中 Eval 走 `reassess → unavailable + 重排队`。

## 3. 执行模型（docs/003 + `src/executor/dsh.ts`）

一次 Assignment：`create`(pending) → `launch` effect → 占 slot → 目录不存在则跑 prepare → `ensure(worker-<id>)` 建 Session → 等前一个后台停下 → 清 inbox 旧副本 → `prompt` 首条消息（title / assignment≤1500 字 / 源 Trigger 卡片 / 同批 Trigger）→ `assignmentStarted`。后续 Assignment 只发增量（`{ticket_id, assignment:n, note?}`）。

**目录**：默认 `<stateDir>/work/<name>`；`name = <worktree_name>-<ticketId 前 12>`；`execution{name, directory, assignment}` 由 `prepareTicket` 钩子落在 Ticket。**prepare**：在父目录 `sh -c <cmd>`，10 分钟超时，带执行环境变量；失败 → `PrepareFailed`（**不自动重试**，因为"目录存在即视为已准备"），空目录删除；成功 `mkdir -p` + `verifyDirectory`。

**执行环境变量**（无凭据）：`DSH_SUPERAGENT_DIR/NAME/TICKET/BLOCKED_COMMANDS`、`..._GITHUB_REPOSITORY/ACCOUNT/COMMIT_EMAIL/NUMBER/PR_HEAD`、`..._FEISHU_BOT/CHAT`。worker 通过 `worker-bin` 的 shim 用 `gh`/`lark-cli`/`git`（绑 agent 账号）；`GIT_CONFIG_GLOBAL` 指向独立 gitconfig、`GIT_ASKPASS=''`、清 `GH_TOKEN/GITHUB_TOKEN/GIT_DIR...`；`blockedCommands` 用"同名命令不可用"实现（**非安全边界**）。`WORKER_COMMANDS=['gh','git','lark-cli']` 不可 block。

**并发（`WorkerSlots`）**：`maxWorkers` 1–16 默认 3，**人的消息可额外占 1 个 slot**；一个 Ticket 从 launch（含 prepare）或唤醒起持 slot，直到 turn 结束且后台也结束；`launches() = 总数 − running/正在 wake`（wake 优先于 launch）；launch 并发执行，慢 prepare 只拖自己。

**三段式投递语义（必须移植）**：

| 回调 | 触发点 | 语义 |
| --- | --- | --- |
| `inputsDelivered` | steer 进 Session 后 | 清 `undelivered`、清 `delivery` 失败、记 `deliveredTo` |
| `inputsRead` | `session/event: user/message` 且 source 为本插件 | 进入 history（模型真能看到）才算 read；被 cancel 的 step 不算 |
| `turnEnded` | 真 idle 且后台静默 | 写 `idleAt`，是"未读消息放回 Eval"的基准 |

`deliverQueued` 每投一条都重读 Ticket 状态；Ticket 结束或换 Assignment 立即停。§3.5 后台规则：回合结束但 shell job/subagent 仍在 → 继续持 slot、不报 waiting；**30 分钟后**杀 shell job 并报 `waiting`（subagent 允许继续）；handoff（ready/attention/blocked/failed）**立刻释放 slot**、停 subagent、shell job 留给用户（不受 30min 限制、结束不唤醒）。

**Pause**：停 dispatch/新 Eval/超时/投递/回信；已开始 launch 等 resume；Source 继续记录；运行中回合不打断；Eval deadline 顺延。**Retry**：仅 `person/delivery|blocked|failed`；结束当前 Assignment→新 Assignment（同 Session/目录/分支/PR）；`requeueUntaken` 把投递过但未被 step 取走的 Input 放回队首。**Restart**：running 的以 `hostRestarted` 结束并带 recovery note 重开（oldest first）；waiting/ready/blocked/failed 不动。**Transient**（408/429/5xx/连接重置等）：3 次重试（launch 走 effect 5s→300s；worker turn 用续跑消息 15/30/60s）；prepare 失败**不重试**；其余 → `failed`（工具保留，人能进 Session 继续，自动输入被扣住）。

**Trigger→PR 端到端**：Source 轮询（GitHub 默认 60s：notifications + 已订阅 Thread + open Ticket 的 PR CI；Feishu 事件+历史+topic）→ `Trigger` → `Engine.receive`（单事务）→ Intake 去重/scope 过滤/关联/扇出/`queueForEval`/`wake` → 每 2s pass 的 `evaluateDue` → `Evals.start`（成批；剔除已被 worker 拥有、已产 Ticket 的；逐条 gate；取"最强+最新"为 `primary`；算 evalId/deadline）→ `assessment` effect 把卡片 steer 给 main Session 并 `candidateDelivered` → main Agent 读卡片/`orchestrator_eval[_read]` → `orchestrator_eval_decide`（或 explicit 用 `orchestrator_route`）→ `accept`：Mandate + Decision + `Tickets.create`（ticketId、channels、prepareTicket、pending Assignment、订阅、ack 回复）→ `launch` → worker 用 bash/git/gh 干活、`orchestrator_ack`/`orchestrator_report`（带 `pr_number` 触发 `claimPull` 绑定 Thread）→ Report relay 给 main → main 决定 `orchestrator_reply`/retry/note/stop → PR `threadClosed` → `followsDone`+`!unfinished` → `end(merged|closed)` → `pullEndNotices` 入 Outbox → `reply` effect → `SourceRegistry.send`。人在 Thread 上继续说话：扇出给存活 Ticket 的 worker（自己判断越界并 handback）；ended 且非人 stop → 直接 `wake` resume，无 Eval。

## 4. 提示词（docs/004 + `src/app/prompts.ts` + `prompts/`）

- **3 个团队段**：Communication / Ticket scope / Delivery requirements（披露行、review bot、commit author、auto-merge 阈值、是否静默）。
- **15 个内置 part**：`runtime/shared|orchestrator|worker|subscriptions|replies|dispatch|work|admission|handoff/{orchestrator,worker}|environment|policy|policy/none`、`skill/{feishu,github}`；文本在 `prompts/*.md`（`promptFile` 把"一行一句"拼成段落）。
- **生成段模板 + 必保留占位符**：`runtime/environment` 需 `{variables}`；`runtime/policy` 需 `{version}/{mode}/{rules}`；`runtime/policy/none` 需 `{version}/{mode}`；缺占位符拒绝保存。
- **组装顺序**：shared → role → 3 团队段 → subscriptions/replies → dispatch(main)/work(worker) → admission(仅 main) → handoff/{role} → environment(生成) → policy(仅 main，**最后**)。worker 额外读 DSH coding prompt 与仓库 `AGENTS.md`；inferred Ticket 末尾追加"仅限命中规则"约束（`mandateInstructions`）。
- **覆盖与版本**：`PromptVersion{revision, previousRevision, savedAt, source, config}`；`overrides[part] = {text, base}`，`base = builtInBase(原文) = key('prompt-built-in', text)`，默认原文存 KV → 升级后 `defaultChanged`（保留我的/用新默认/编辑）。保存校验：未知 key、保留 skill 名、未知 part、缺占位符、空文本、超 `promptMaxChars` 一律拒绝，**永不静默截断**；与默认相同的覆盖被移除。**每个模型请求重读最新版本**；每个 Session 用过的 revision 记 KV `session-prompts:<id>`。
- **强制力排序**：工具/宿主强制事实 > 内置/生成段 > 命中规则（仅该 worker）> 团队段/skills > 外部文本（数据，永不改规则）。
- **Skills**：名字 `^[a-z][a-z0-9-]*$`，注册前缀 `dsh-superagent-`；内置 `skill/github|feishu` 可被覆盖，自定义 skill 描述取正文前 120 字。

## 5. 架构 / 数据流 / 持久化 / API / client（docs/007）

- `src/core` **零 I/O**（只 import node:crypto），经 `ports.ts` 的 `Repositories/Policy/SourceScope/Assessor/Executor/Outbox` 与外接；所有命令**同步**、一命令一事务；调用方只拿 `Pick` 出的窄接口（`EngineForSources/MainSession/People/Settings/Executor`）。`src/sources` 不 import `src/app`；`src/client` 只 import `api.ts` 类型与少数纯值模块。
- 单写者：Trigger/Thread/Decision/Ticket/Assignment 只由 Engine 写；Source 位/Thread meta 由 Source 写；Settings/Prompts/Effects/Outbox/Health 各有唯一写者。
- **SQLite 一张库**（`<stateDir>/orchestrator.sqlite`，WAL + `synchronous=FULL`，先 `proper-lockfile` 锁 `stateDir`，stale 30s）：`trigger`(JSON+at)、`disposition`、`trigger_thread`(倒排)、`thread`/`ticket`/`decision`(JSON)、`outbox`(回执，永久保留)、`effect`(kind+key 唯一、seq 保序、generation 防旧任务删新负载、attempts/error/due)、`kv`(settings/prompt 版本、master-id、session-prompts、source 游标/labels/CI 指纹…)。生成列+局部索引做活跃集（`ticket.open/active`、`thread.active/subscribed`、`outbox.pending/dropped`）。`atomic()`=SAVEPOINT（可嵌套），`afterCommit()` 只在最外层提交后跑。`SCHEMA_VERSION=11`；**无 TTL**（原型已知问题）。
- **HTTP**（`/orchestrator/api`，仅 loopback + 同源，body ≤128KB，`UserError.missing`→404 其余→400）：`GET state|overview|search|sources|settings|models|prompts|policy/history`；`GET threads[/:key]`、`POST threads/:key/unsubscribe`、`POST triggers/:id/requeue`；`GET decisions`、`POST decisions/:id/dismiss`；`GET tickets[/:id]`、`POST tickets/:id/retry|stop`；`POST executor/pause`；`PUT settings/:block`（`revision` 乐观并发）、`POST policy-sync/:id`、`POST setup/check`；`PUT prompts`（`baseRevision`，冲突拒绝）、`POST prompts/preview`。
- **client**：浏览器插件注册主面板 + 侧栏图标 + Plugin Manager 配置页；Tabs = Overview/Source/Trigger/Eval/Execution/Settings；轮询 typed API；双语 `locales.ts`（含 001 词表）；不 import Node 模块。核心顺序"要人处理 → 健康 → 历史"。

## 6. Source 扩展点（docs/008，Go interface 设计的直接输入）

`SourceKind<C>` 必须实现/可选实现：

| 成员 | 必需 | 作用 |
| --- | --- | --- |
| `kind` / `title` / `description` | ✔ | 稳定 id（进 Trigger.source、thread key 前缀、settings/policy 键） |
| `schema` / `fields` / `health` | ✔ | 校验+默认（默认应收不到任何东西）；生成设置表单；声明 health 条目（注册即 claim，禁 `sync:`） |
| `scope({meta},config)→{listened,observed,row?}` | ✖ | 是否监听/是否观察全部/admission 行 id；**只依赖 meta 与 config**（revalidate 会拿 Mandate 存的 meta 再调） |
| `channel{allowed?,send,link}` | ✖ | 幂等发送（用 message.id 作幂等键）；永久不可达返回 `'gone'`，其余抛错重试 |
| `threadContext{messageId,read,images?,image?}` | ✖ | 评估用的有界 Thread 读取（thread/root/parent 分页） |
| `find` / `repliesTo` / `facts` / `refreshLabels` | ✖ | 搜索 / 回复目标 / 只读展示值 / 展示名刷新 |
| `policy{schema,fields,explicit,rows(config),bots?,sync?,unnamed?}` | ✖ | Source 的 Policy 部分（allowed、admission 行、bot list、成员同步） |
| `environment{variables,values(trigger,config)}` | ✖ | 执行环境变量（`DSH_SUPERAGENT_<KIND>_*`，无凭据） |
| 插件入口 + 轮询 | ✔ | 等 `ready`、尊重开关、按 abort 退出、逐条容错、`receive` 后才推进游标、失败计数 3 次才报 error |

`SourceContext`（给插件的能力）：`deployment/commands/ready/configured()/config(kind)/receive/atomic/state{get,set,keys}/threads{get,subscribed,describe,patch}/tickets{get,open,bindThread,threadClosed}/triggers{get,latest}/notify/report`。新增 Source 的 11 步与检查清单见 `docs/008`。

## 7. 工具契约（`src/app/tools/*.ts`）—— Go 版工具设计的直接输入

**main Session（14 个）**：

| 工具 | 入参要点 | 返回 |
| --- | --- | --- |
| `orchestrator_read` | `resource`（见 RESOURCES 语法）、`offset`、`limit`(文本默认 4000/上限 8000；列表默认 20/上限 50) | 文本 `{resource,offset,total_chars,next_offset,text}` 或列表 `{items,next_offset}`，可带 `next_resource` |
| `orchestrator_route` | `decisions[1..20]`，每项 `{kind: accept\|attach\|ignore\|stop, trigger_id, ticket_id?, title?, worktree_name?, acknowledgement?, note?(≤30 词), reason?}` | `{results:[{trigger_id,kind,decision?,reason,ticket?,refused?}]}`，逐项独立 |
| `orchestrator_ticket_retry` | `ticket_id` | `{reason,ticket?}` |
| `orchestrator_ticket_request_status` | `ticket_id` | `{reason,ticket?}` |
| `orchestrator_ticket_note` | `ticket_id`、`text`(≤100 词) | `{reason,ticket?}` |
| `orchestrator_trigger_requeue` | `trigger_id`（被 worker 拥有的 follow-up 会拒绝） | `{queued}` |
| `orchestrator_eval` | `trigger_id` | 候选窗口：`trigger_id/strength/mode/source/text(≤4000)/images?/batch?/related_tickets(≤8)+tickets_total/policy_version/thread{complete,loaded_messages,source_found,next_cursor?,messages[]}` |
| `orchestrator_eval_read` | `{trigger_id, resource: message\|root\|parent\|thread\|file\|directory\|image, path?, message_id?, cursor?, image_id?, offset}` | 分页文本或图片附件 |
| `orchestrator_eval_decide` | `trigger_id` + `{decision, rule_assessments[], direction{implementation_requested,target_basis,requires_design_decision,evidence}, title, worktree_name?, scope, intent_evidence?, acknowledgement?, reason}` | `{decision,reason,ticket?}` |
| `orchestrator_reply` | `{purpose: answer\|result\|attention, text, trigger_id?, ticket_id?, human_action?}` | `{status}` |
| `orchestrator_thread_unsubscribe` | `thread` | `{removed,thread}` |
| `orchestrator_settings_save` | `{block: policy\|eval\|executor\|pause\|source, source?, value, revision}` | 保存后的块 |
| `orchestrator_policy_sync` | `source` | `{source,allowed,added,removed}` |
| `orchestrator_prompts_save` | `{config, baseRevision?, preview?}` | 版本号 或 preview 资源 |

**Executor Session（6 个）**：

| 工具 | 入参 | 返回 |
| --- | --- | --- |
| `orchestrator_read` | 同上，但只能读本 Ticket 可读资源 | 同上 |
| `orchestrator_ack` | `{trigger_id, text}`（≤100 词） | `{trigger_id, acknowledgement, reused}` |
| `orchestrator_thread_unrelated` | `{thread, reason≤300}` | `{removed:true,thread}` |
| `orchestrator_thread_follow` | `{thread, reason≤300}` | `{followed,thread}` |
| `orchestrator_trigger_handback` | `{trigger_id, reason≤300}` | `{trigger_id, queued}` |
| `orchestrator_report` | `{state: progress\|ready\|blocked\|failed\|dropped, summary, human_action?(≤1000), question_for_trigger_id?, pr_number?, reply_to_trigger_id?}` | `{reported:true, notification: orchestrator\|internal_only, attention?}` |

**只有面板能做（Agent 无工具）**：dismiss `unavailable` Decision；重跑 setup（`POST setup/check`）；刷新 Source 展示名（`POST sources/:id/labels`）；把被 Executor 拥有的 follow-up 送进 Eval（`orchestrator_trigger_requeue` 故意拒绝）；**直接手动 stop**（面板 `stopTicket` 记为 manual stop，Agent 只能经 `orchestrator_route` kind=stop 且需 explicit 强度）。

**结果与错误约定**：路由结果 `{decision?, reason, ticket?}`；所有拒绝是 `UserError(code, values)`（missing→404/refused→400），面板按 code 本地化；工具输出统一 JSON 文本，图片走 `output.image` 附件。

## 8. 提示词对 DSH 工具名的依赖（必须在新工具集里对应或改写）

| 出处 | 依赖的 DSH 工具/语义 | Go 版处理 |
| --- | --- | --- |
| `shared/orchestrator/worker/dispatch/admission` | `bash`（只是"不要用 bash 绕过评估"） | 改写为新产品名（如 `shell`/`exec`），语义保留 |
| 全部 | `read` / `read_image`、"read pointer / read resource" | 保留为 `read_file`/`read_image` + 统一"资源读取"概念；`orchestrator_read` 与它并存（前者读领域资源，后者读文件/图片） |
| `worker/work` | 后台 job（长时间命令转后台、结束唤醒、`job_output` 轮询禁令） | 必须实现：前台命令超时可转后台作业 + 完成事件唤醒；否则 `prompts/work.md` 的"长命令放后台"整段失效 |
| `prompts/cli.md`、`worker.md`、skills | `skill`（加载 `dsh-superagent-github/feishu`）、`skills read lark-im` | 必须实现 skill 注册与 `skill` 工具；或把 skill 文本直接注入（简化但失去按需加载） |
| `prompts/worker.md`、`dispatch` | `subagent` 概念（prompt 里提"delegate to another Session"被禁止，但 worker preset 允许 subagent） | 可保留 subagent（并行/调查）或明确删除并在提示词改写；注意 `background.ts` 的 subagent 生命周期依赖它 |
| `team/communication.md`、feishu skill | `present`（禁止向人展示 UI）、`ask_user_question`（禁止向人提问） | 新工具集里**不要提供**这类工具；提示词里对它们的"禁止"改写为"不存在"或删除 |
| `prompts/admission.md` | "不要用 bash 绕过这些工具"、"不要委派给另一个 Session/评估器" | 保留为通用禁令（工具名替换） |
| `prompts/orchestrator.md` | "never wait with sleep, polling or job waits"；`job_output/job_list/job_kill` 语义 | 必须实现 job 工具族，或把该段改成"不要阻塞等待"（配合事件驱动） |
| `prompts/worker.md` | `gh`/`git`/`lark-cli` CLI（非 DSH 工具，exec 环境 shim） | 与 DSH 无关，照搬 shim 方案 |
| `prompts/team/delivery.md`、feishu skill | GitHub GraphQL / `gh api` 具体调用 | 与工具集无关，纯 CLI 用法，照搬 |
| skills 头部 | `name`/`description` frontmatter、`resourceBase` 目录 | skill 装载器需支持 frontmatter + 资源根 |

结论：**领域工具（`orchestrator_*`）自成体系，可 1:1 复刻**；真正要重做的是"文件系统工具 + shell + 后台作业 + skill + 图片读取"这一层，并且 `prompts/work.md`、`prompts/cli.md`、`prompts/orchestrator.md`、`prompts/dispatch.md` 里所有工具名/等待语义必须逐句核对改写。`present`/`ask_user_question`/`plugin_manager`/`schedule`/`goal`/`workflow`/`fork subagent` 这些 DSH 独有能力在 worker preset 里本来就被排除，Go 版**不需要实现**，只需保证 worker 拿不到等价能力。

## 9. 会话 / 轮次 / 后台 / 子智能体（`src/app/sessions.ts` + `src/executor/background.ts`）

DSH 提供、Go 必须自建的语义：

| 语义 | DSH 现在怎么做 | Go 版必须实现 |
| --- | --- | --- |
| Session 创建与目录绑定 | `sessionController.create({sessionId, workspaceId, agentPreset})` + `workspaceRegistry.create(cwd)` | 会话对象 + 工作目录 + 角色（main/worker）+ 模型选择；worker 会话按 `worker-<ticketId>` 稳定 id |
| 预设/权限 | `dsh-superagent-worker/router` preset；`danger-full-access`；UI 误选 preset 后强制改回 | 工具白名单 = 预设（main 只读+路由；worker 文件/shell/job/web/skill/todo/subagent）；无需动态改回（自己创建就自己定） |
| 系统提示词挂载 | `systemPrompt.section({order:9999, interpolate:false, complete})`，每次请求重算 | 组装函数 + 每请求重算（保证提示词/策略热更新） |
| 工具注册/限制 | `tools.register/restrict/guard`；worker 的 `workOnly` 在 Ticket 结束时拒绝所有工具调用 | 工具注册表 + 白/黑名单 + "Ticket 已结束"守卫 |
| 消息投递 | `followup`（下一轮）/`steer`（当前轮）/`inject`（不唤醒）+ `sessionPersistence.flush()` + 去重 id | 三档投递 + 落盘 + 幂等 id（`MessageId('...:'+requestId)`） |
| 已读判定 | `session/event: user/message` 且 source 为本插件 | 会话事件流 + 消息来源标记（区分"人打的"与"系统投递"） |
| 回合结束 | `agent/status: idle` → `whenIdle()` → `untilQuiet()` | 事件回调：turn start/end、idle、error、cancel |
| 取消 | `agent.cancel({kind:'user'},{keepInbox})` | 取消当前 LLM 调用/工具执行，保留 inbox |
| 手打消息 | `agent/inbox/claimed` 且 `source.kind==='user'` | 区分人直接输入与系统投递（影响 `personal` 时间戳与"人可以在 Session 里继续"） |
| 后台作业 | `ctx.jobs.list/wait/kill/events`（owner=sessionId） | 自己的 job registry（命令、状态、输出、完成事件、owner） |
| 子智能体 | `ctx.subagents.listChildren/waitForChildren/drainChildren` | 若保留 subagent：父子会话关系 + 结束时 drain + 结束事件不误唤醒 |
| 上下文压缩 | DSH `compaction-basic` + tool-result pruner（阈值 8192/4096/1024） | 自建压缩策略（否则长 Ticket 必然爆上下文） |
| 模型预算重试 | `retry-health.ts` 监听事件、面板 `model_retry` | 可选：至少要有 provider 限流/预算错误的识别与退避，不要伪装成工作失败 |

后台规则（必须移植）：30 分钟上限、handoff 留 job、Assignment 结束杀 job/释放 subagent、`killJobs` 时先开始 wait 再 kill（收集结束事件避免唤醒）、`stopSubagents` 用 `cancelled` WeakSet 区分"停 subagent 引发的 idle"。这些细节决定"人能不能在 Session 里继续操作"与"是否会重复唤醒"。

## 10. 可靠性机制（`effects.ts` / `outbox.ts` / `notices.ts`）—— 必须保留

| 机制 | 实现要点 | 为什么必须保留 |
| --- | --- | --- |
| durable Effects | 命令事务内 `enqueue(kind,key,payload)`；`kind+key` 唯一、`seq` 保序、`generation` 防旧任务删新行、`due/attempts/error`；`Later` 退避不计失败；`drain()` 保留未执行行 | 崩溃/重启后"该做的副作用"不丢；launch/delivery/relay/notice/reply 全靠它 |
| 限流与有序 | `assessment` 4、`launch`=空闲 slot、`delivery` 4(quiet)、`relay/notice` 1 且 ordered、`reply` 1；同 kind 失败会阻塞后续 ordered 行 | 防止并发超编、保证通知顺序 |
| 退避 | 2s→60s（assessment/delivery/relay/notice）、5s→300s（launch/reply）；连续 3 次失败才置 health error（`delivery` 除外） | 网络抖动不误报，真故障可被面板看到 |
| Outbox 幂等 | 先写 receipt（`pending`）再发；`message.id` 幂等；`sent`/`dropped{at,reason}` 永久保留；`behind(id,3)` 保证同 Ticket 同会话保序；`withdraw` 取消未发 | 防重复评论/重复 IM；drop 后永不重发 |
| drop 语义 | Source 不再监听 → `channelIgnored`；目标永久不可达（删除 Issue/撤回消息）→ `channelGone`；Source 关闭 → 等待 | 区分"暂时失败"与"永远送不到" |
| notice/relay | notice 用名称做合并键（未发时后者覆盖前者）；relay 按 id 幂等、有序；`inject` 不唤醒 | 避免刷屏，且不打断 main 的当前回合 |
| 健康板 | 每个条目单写者 claim（runtime/evals/models/worker/effects/sources/sync）；只写声明过的条目 | 面板/状态页/自愈判断的唯一来源 |
| 状态目录 | `<stateDir>`：DB + 锁 + `worker-bin` + `worker-gitconfig` + `work/`（日志在 checkout 的 `.artifacts/`）；先锁后开库 | 单实例、可备份、可迁移 |

## 11. 与 DSH 宿主的耦合点：Go 自研替代品要补哪些能力

**它如何真正驱动 Agent（关键发现）**：不是起 `dsh` 子进程，也不是自己调模型。外层脚本用 tmux 起一个宿主进程（`dsh --profile`），插件**在进程内**建 Session、挂系统提示词、注册工具、用 `followup/steer/inject` 投消息、监听 `agent/*` 与 `session/*` 事件、用 `agent.cancel/whenIdle` 控制回合。**评估也是同一个 main Session 这个 LLM 做的**（`orchestrator_eval*`），没有独立模型调用；`unavailable` 来自超时或模型失败。

| 能力 | DSH 提供 | Go 版必须自建 |
| --- | --- | --- |
| 模型调用 | `dsh-llm` + 会话级 role model | provider 适配（OpenAI 兼容/DeepSeek）、流式、tool-call 循环、结构化输出校验、重试/限流/token 统计 |
| 工具执行 | preset 的 `bash/fs/fs-search/jobs/web/skill/todo/subagent` + sandbox 超时 | 工具注册表 + JSON Schema + shell/文件/搜索/图片工具 + 前台转后台 + 取消超时 + `blockedCommands` 与 shim 整合 |
| 会话 | `dsh-session` + persistence（事件流、inbox nextTurn/nextStep、steer/followup/inject、cancel、快照） | 会话存储（append-only 事件）、投递队列与已读语义、跨重启恢复、压缩、并发控制 |
| 提示词/技能 | `dsh-system-prompt` section、`dsh-skill` scope、`dsh-agent-preset` | 文本已有；"每请求组装 + skill 注册/加载 + 工具白名单"自建 |
| 工作区/仓库 | 只给 `workspaceRegistry.create(cwd)`；worktree 靠用户配的 prepare 命令 | 目录管理 + prepare 执行 + `git worktree` 默认值 + 凭据/git 配置隔离（照搬 `infra/environment.ts`）+ 目录就绪校验 |
| GitHub | 自研 `gh` 包装（限定 repo、校验输出、ETag、通知游标、CI 指纹、PR 规则） | 继续 shell out `gh`（推荐，复用登录态与既有 1500+ 行成熟逻辑），或写 REST/GraphQL + App 认证 |
| Feishu | 自研 `lark-cli` 包装 + 事件/历史/topic/图片/成员同步 | **1:1 移植**：事件消费者 + 历史恢复 + 已订阅 topic 轮询 + 图片读取 + 群成员同步（`sync:<id>`） |
| Web 面板 | 宿主 UI slot + webserver + React 运行时 + i18n + 路由 | mygo 原生 UI 替代；HTTP API 面 1:1 保留（供 UI 与外部状态检查） |
| 持久化/队列 | 自研 SQLite + Effects + Outbox + KV | 直接照搬设计（Go 版 `database/sql` + savepoint/事务函数） |
| 插件生命周期 | Cordis DI、`ctx.effect` 自动 dispose、config volatile、`fiber.restart()` | 自己的组件生命周期 + 配置热加载 + 优雅停机 + 进程重启（见 §12） |
| 图片/附件 | `dsh-attachment` | 下载 + 多模态编码 |
| 进程守护 | `scripts/dsh-superagent`（tmux + `dsh`，退出码 0/1/2/3、status 汇总 health、`--respect-stop`、日志保留 20 份） | 桌面托盘 + 单实例 + 状态页/CLI + 日志轮转（见 §12） |

## 12. 桌面化（mygo 单二进制 + 托盘驻留）与"长跑"的等价实现

原型里为"在 DSH 宿主进程内 7×24 长跑"而存在、但换到桌面进程后需要重新设计的机制：

| 原型机制 | 存在原因 | 桌面等价实现 |
| --- | --- | --- |
| `stateDir` + `proper-lockfile`（stale 30s，`onCompromised` → health error + abort lifetime） | 阻止第二个宿主进程并发迁移/写库 | 保留 `stateDir` 与**排他文件锁**；桌面双击第二次启动 → 通过本地 socket/命名互斥量把窗口唤到前台，而不是起第二个运行时 |
| `HealthBoard` 单写者 claim + `/api/state` + `scripts status` 退出码 0/1/2/3 | 面板、外部检查、自动恢复共用一个真相源 | 保留 HealthBoard + 状态 API + CLI `status` 子命令；托盘图标/角标映射 state（ready/paused/error/setup） |
| 2s `Passes` ticker + `effects.kick()` | 轮询式推进 Eval/副作用 | 保留：一个后台 scheduler goroutine + 事件唤醒（有新 Trigger 立即 request），窗口关闭不影响 |
| Effects 未执行行在重启后重跑；`afterCommit` | 崩溃恢复 | 保留（DB 行 + 启动时 `drain→start`）；桌面休眠唤醒后先跑一次 pass |
| Setup 探测 CLI 账号 + 改配置后 `fiber.restart()` | 宿主插件无法安全热改进程级环境（PATH/gitconfig） | 改为"探测 → 写 shim/gitconfig → 重建 Executor 组件"的软重启；仅 `stateDir` 变更要求真正重启 app |
| `agent-preset/selected` 纠正（UI 误选 preset） | 宿主 Web UI 可能给别人开 Session | 不需要：桌面 app 只创建自己的两种角色 |
| DSH 的 model budget retry health | 宿主模型配额 | 保留最小版：provider 429/预算错误识别 + 退避 + 状态展示 |
| tmux 守护 + 每小时外部 check + `--respect-stop` + 日志 `.artifacts/` | 在无 systemd 的单机上保活 | 托盘常驻 + 开机自启（LaunchAgent/systemd user unit）；进程内 watchdog（pass 失败、DB 锁丢失、Source 连续失败）；托盘通知与"打开日志"；`manually_stopped` 标记写入 stateDir |
| 窗口 = 浏览器面板 | 宿主 Web | mygo 窗口可关，进程常驻托盘；"关闭窗口 = 隐藏"；可选"开机启动"与"防止休眠"开关（长 CI 等待时有用） |

要点：**7×24 的关键不是窗口，而是"进程与窗口解耦 + 状态持久 + 唤醒后补偿"**。桌面 app 应默认托盘常驻、单实例、崩溃后自动重启（由 OS 服务或自身 supervisor），并把"当前 health/queue/未完成 Assignment"作为首屏。

## 13. 可复用的设计资产 与 可以简化的偶然复杂度

**必须照搬的资产**：领域模型 + Ticket 阶段表（可直接变 Go `func Stage(*Ticket) Stage`）；内容派生 ID 的全部幂等点；5 个窄角色接口（Go 用小 interface 实现同隔离）；gate + admission table + MandateBasis + revalidate；Effects 队列（事务内入队、提交后执行、限流/退避/有序）；Outbox receipt（先落库再发、id 幂等、永久回执、同会话保序、gone/ignored 区分）；三段式投递（delivered/read/turnEnded）；提示词 part/override/base-hash/占位符校验/版本；SourceKind 契约；`follows/followsDone/unfinished`；Reason 码 + UserError 码（码+模板，可本地化、可被 Agent 读）；工具面切分（main 只读+路由 vs worker 6 个）；`followupOwners/handback/acked` 的归属规则；单写者 + DomainEvents；`orchestrator_read` 的分页资源模型（文本 4000/8000、列表 20/50、`next_offset`/`next_resource`）；IM 100 词/note 30 词的硬限制与中文分词计数。

**可以简化的偶然复杂度（业务语义一律不砍）**：

| 可简化 | 说明 |
| --- | --- |
| `src/app/migration.ts`（46KB）与全部 legacy 分支 | schema 10→11、`legacyImport`、`authority`/`listed`/`reopen`/旧 marker（`dsh-tag`/`dsh-go`/`dsh-freelancer`）；新库从 schema 11 起 |
| DSH 宿主兼容层 | preset 纠正、`fiber.restart`、settings service 重新 `describe`、`bundle-patch.spec`、`docs.spec`/`class-docs.spec` 这类文档一致性测试 |
| `model_retry` / `retry-health` / `role-models` 的会话级模型 | 统一默认模型 + 简化重试（`observe`/`auto` 语义不变） |
| 面板双层 i18n 与复刻宿主控件（`controls.tsx`、`locales.ts` 73KB） | mygo UI 用单一语言或轻量 i18n；保留术语表（001 词表）即可 |
| `cache.ts`/`TextCache`、`store-read` 的部分视图查询 | 直接 SQL；分页语义保留 |
| 搜索（`find`/`sourceLink` 缓存/`repliedTo`） | 可延后；`orchestrator_read` 的 `trigger/<id>` 已覆盖主要用途 |
| `defaultChanged` 默认文本 diff UI | 保留 base 记录与"默认已更新"提示即可，diff 展示可后置 |
| `workflow`/`goal`/`schedule`/`plugin_manager`/`present`/`ask_user_question` 等 DSH 独有能力 | worker preset 本就排除；Go 版不实现，提示词里相关的"禁止/不要用"改写即可（后台 job 与（可选）subagent 不在此列） |
| 桌面端的"管理 API 鉴权" | 单机 + 本地 socket/回环 + 同源校验即可；不需要多用户体系 |

## 14. Go 重写必须做决策的问题

1. **Agent 运行时形态**：worker 是 Go 进程内的 LLM 循环 + 工具执行 + 会话（等价 DSH 的进程内 Session），还是把每个 worker 委托给外部 coding CLI 子进程（开发量小，但**丢失 steer/inject/已读/中断**，会连带破坏 follow-up 归属与"未读消息回 Eval"）？建议前者。
2. **会话与上下文**：一个 Ticket 一个长驻会话（含压缩）还是每 Assignment 重开？subagent 是否保留？30 分钟后台规则、handoff 留 job 是否 1:1 保留（影响"人在 Session 里继续"的体验）？
3. **工具集边界**：`bash/fs/edit/search/job/skill/read_image` 的最小实现是什么？shell 是否沙箱（原型明确不是安全边界）？`blockedCommands` 用 shim 还是 PATH 拦截？后台作业用 PTY 还是管道 + 子进程组？
4. **提示词改写范围**：`prompts/*.md` 里 DSH 工具名（bash/job/skill/subagent/read/read_image/present/ask_user_question）逐句替换，还是重写为"新工具契约"版本？`team/*` 与 skills 的文本要保留到什么程度（这决定提示词的回归测试集）。
5. **持久化选型**：单文件 SQLite（JSON-in-row + 生成列 + WAL + savepoint）是否照搬？还是规范化表？是否需要保留"JSON 聚合 + 活动集生成列"这一模式（对 1:1 迁移最省事）。无 TTL 的增长问题是否在 MVP 内解决？
6. **评估仍由 Agent 完成还是拆出**：保留"main Session 是 LLM，通过 `orchestrator_eval*` 提交 Decision"（1:1、可解释，但成本/时延高、依赖模型稳定性），还是 gate 之后用独立评分调用？observe/`would_accept` 必须保留。
7. **可靠性与单实例**：是否保留 durable Effects + Outbox + health 板 + 状态目录锁（推荐 1:1）？桌面 app 的单实例用文件锁 + 本地 socket 还是 OS 互斥量？崩溃后自动重启由自身 supervisor 还是 OS 服务承担？
8. **平台接入方式与范围**：GitHub 继续 `gh` CLI（复用现成 wrapper 与登录态，依赖 CLI 输出格式）还是 REST/GraphQL + App；Feishu 首版接入到哪一层（事件+历史+topic 必需，图片/成员同步是否同期）；HTTP API + mygo UI 是否保持 1:1 路由（外部状态检查/自愈脚本会依赖它）。
