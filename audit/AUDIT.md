# ZeoKeel Super Agent 审计报告

> 审计日期：2026-11-03　审计对象：`/data/ZeoKeel-Super-Agent`（HEAD `03b25e9`，VERSION `1.0.0`，CHANGELOG 自称 `1.0.0-rc.1`）
> 需求基准 A：`_ref/` = `github.com/deepseek-ai/dsh-superagent-prototype`（commit `6337fc8`，docs 000–008，src 12.4k 行 TS）
> 需求基准 B：用户提供的《Super-agent 设计探索》原文（五模块架构；设计文档 §4.5 转引并称"官方双层"）
> 设计基准：`design/zeokeel-go-mygo-design.md`（1861 行）；代码基准：`internal/**`、`cmd/**`、`tools/**`（生产 26,980 行 + 测试 16,974 行，234 个 Go 文件）
> 方法：7 个只读并行审计（4 个"框架/自研"切片 + 3 个"需求符合性"切片），逐文件精读 + 在 GOMODCACHE/GOROOT 核对库源码 + 复现构建/测试；未修改任何源码。分片报告见文末索引。

---

## 0. 一页结论

### 0.1 问题一：有哪些"不必要的自研"、该用框架？

**总体判断：框架选型是对的，真正重写 SDK 的地方不多；"不必要自研"集中在 Agent 工具层、IM/GitHub 接入层和 UI 局部，而且其中几处是"框架早就给了、代码没用"。**

| 判定 | 数量 | 代表 |
| --- | --- | --- |
| 应改用框架/标准库/已有依赖 | 22 项 | eino `patchtoolcalls`/`filesystem`/`backgroundtask`/`TurnLoop`/`skill` 中间件；mygo `List`/`Table`/`Theme`；`os.Root`；`doublestar`；`humanize.Comma`；官方 Lark/钉钉 Stream SDK；`go-github` + `x/oauth2` |
| 自研合理（框架确实没有 / 领域不变量） | 37 项 | 内容派生 ID、Ticket 阶段表、Effects/Outbox、gate/路由、Eino 压缩/截断/子智能体胶水、mygo 快照架构、中文相对时间、reason 码表、SKILL.md 扁平 front matter |
| 低优先/内部重复 | 22 项 | 4 套 truncate、3 份 humanAge、2 份 humanBytes、2 套保存条、6 个 chip 变体、4 处设置解析 |
| 死代码/未接线 | 13 项 | `engine_react.go`、2,229 行 IM 驱动、`view/demo*.go` 572 行、MCP 管理 API、workflow 持久层、9 个未用图标、`seedstate` |

> 计数为 4 个分片报告的去重近似值；单项可能跨类（如飞书驱动既是死代码、又应换 SDK），不要求四类相加等于条目总数。

**最值钱的五个发现（按修复收益排序）**

1. **P0｜飞书/钉钉驱动写了 2,229 行，但根本没接进二进制**：`cmd/zeokeel` 不 import `internal/im/{feishu,dingtalk}`（`go list -deps ./cmd/zeokeel` 可证），`startIMLoop`（`cmd/zeokeel/extras.go:157`）无调用者，`cmd` 也没有任何 IM flag。README/CHANGELOG/设计却都宣称"飞书与钉钉已实现"。同时这两个驱动是**手写 HTTP 客户端**，官方纯 Go SDK（`larksuite/oapi-sdk-go/v3`、`open-dingtalk/dingtalk-stream-sdk-go`）已覆盖 token 缓存、签名校验、AES 解密、WebSocket 长连接——即"该用 SDK"与"写了没用"是同一处。
2. **P0｜悬空 tool_call 的恢复逻辑没接线**：`patchInterruptedToolCalls`（`internal/agent/session.go:58-109`）只被 `Runner.Resume`（`agent.go:308`）调用，而 `Resume` 在生产代码里**零调用者**；崩溃重启走 `RunSession`（`runtime/worker.go:282`），未配对的 tool_call 会原样重放给真实 provider（400 风险）。eino 官方 `adk/middlewares/patchtoolcalls` 就是干这个的。
3. **P1｜`glob` 承诺 `**` 但用的是 `filepath.Glob`**（`internal/agent/tools.go:124,135`）：`**` 等价于 `*`，实测三层目录匹配不到；`bmatcuk/doublestar/v4` 已因 eino 在模块图里。同类：`inside()` 只做词法越界（`tools.go:294-308`），挡不住符号链接逃逸，标准库 `os.Root` 才是真约束。
4. **P1｜UI 把 mygo 控件手工拼回去 4 处**：消息流 `Scroll+Column`（`ui/stream.go:17-36`，无虚拟化）、评估页手搓表头（`ui/eval.go:97-127`）、命令面板手搓 ↑↓ 状态机（`ui/search.go:98-143`）、两个 `ui.List` 没设 `ListState.Key`（`ui/app.go:352-353`）→ 列表按创建时间倒序重排时，**已展开的行会指向另一条工单**（真 bug）。另有 `groupedNumber` 重复 `humanize.Comma`、`trimSpace` 重复 `strings.TrimSpace`（且全角空格会被当输入派发）。
5. **P1｜cron 与 DST 自研可以缩回 stdlib**：`internal/schedule/cron.go:87-111` 的 `N/step`（如 `0/6 * * * *`）只匹配 N 而不是 N–max/step，是**静默错调度**；`next.go:60-89` 用 17 次时区探测求 DST，`time.Date` 往返 8 行即可（已在 go1.27.3 上实测 spring gap / 30 分钟 DST / 午夜空洞）。注意：`robfig/cron/v3` 只能换 parser 且有 DOM/DOW 语义差异与更弱的 DST，**不建议**引入。

**结论**：大多数问题**不需要新增依赖**——用 stdlib（`os.Root`、`time.Date`、`strings`、`text/template` 级别的能力）或仓库/模块图里已有的东西（eino 中间件、doublestar、humanize、x/time/rate）即可；只有 IM/GitHub 三处建议引入官方纯 Go SDK（各 M 级工作量，且迁移要重跑零 cgo 守卫）。真正该守住的自研（内容 ID、Effects/Outbox、阶段表、Worker 隔离）没有被"框架化"掉，这是这个项目做对的地方。

### 0.2 问题二：设计方案是否符合 dsh-superagent-prototype / Super-agent 的要求？

**分三层回答，结论不同：**

| 层次 | 结论 |
| --- | --- |
| **设计文档的"意图层"** | **基本符合**。§1.5/§4.1–§4.4/附录 A 把原型最值钱的确定性机制都点名了（内容派生 ID、阶段查表、gate+admission、Mandate/revalidate、Effects/Outbox、三段式投递、`followupOwners`/`letGo`、PR `follows`）；三条扩展（单二进制桌面、多工作区、自研进程内 Runtime）都有 ADR 与范围声明。 |
| **设计文档的"已实现"声明（§11/§10/§8）** | **不符合，且大量高报**。§11 把"§4.3 gate+admission ✅""§4.4 不变量 11 条全部有测试 ✅""§5.2 会话/Resume ✅""§4.8 IM ✅""§5.8 崩溃恢复 ✅""§7 v1–v3 ✅"等标成完成；代码里 admission table、Eval 聚合、revalidate、followupOwners、投递三段式、pause/retry/resume/stop、IM 接线、崩溃恢复（Assignment）都不存在。 |
| **交付物（代码）对需求的符合性** | **不符合"完整移植/1:1"**。可核对需求条目中 CONFORM 10.0%、PARTIAL 45.2%、MISSING 37.6%（明细见 §4）；官方 Super-agent 五模块里**只落地了 Worker 一层**，Router 层（持久会话/批处理/全局记忆/统一回复）、订阅直达、全双工异步、Watchdog 都是空的。 |

**一句话**：**方案（设计文档）方向大体对、但没有被实现；"设计的方案是否符合要求"要看你问的是"计划"还是"已交付物"——计划基本符合，交付物不符合，而且设计文档把未实现的写成了已实现。**

**最关键的语义偏离（不是"少做了功能"，而是改掉了承重规则）**

1. **确定性授权被模型接管**：原型 gate 必须确定性地产出 `strength ∈ {explicit, inferred}`（002:11-16,50-54）；本项目 gate 只返回 `ok bool`（`engine.go:338-369`），strength 来自模型的 JSON（`prompts/defaults.go:74`）再决定 accept mode 安全阀（`engine.go:216-221`）——直接违反 002 与设计自己引用的"prompt is not permission"。
2. **审批关卡取代 Eval**：所有非本人、非本地的 accept 一律降级为 `DecPropose`（`engine.go:240-244`），`Approve` 才创建 Ticket/Mandate（`engine.go:636-679`）→ 违反 001:35"Only Eval creates one"、002:101"explicit 在任何 accept mode 下都生效"、`auto` 模式永远无法自主开单。设计 §4.10 用"代理授权"论证了这个扩展，但没有处理它对 001/002 的破坏，也没有更新规格——这是**未支付的语义债**。
3. **订阅只做了一半**：fan-out 把文本塞进 `Ticket.ContextInput`（`engine.go:312`），但 (a) 同一条消息**继续走 Eval**（违反 001:37"人的消息走扇出就不进 Eval"），(b) `ContextInput` **从不投递给 Worker**（Worker 只拿 Origin 原文，`runtime/worker.go:476-484`），(c) 没有 `followupOwners`/`letGo`/退订/交回。结果是"后续讨论丢失"和"重复开单"同时存在。
4. **Ticket 没有正常结束路径**：`Engine.Report` 只在 `dropped` 时写 `Ended`（`engine.go:855-857`），`ready` 停在"等待评审"；没有 stop/retry/pause/resume，`Attention` 没有回答入口（`engine.go:727-739`，`ui/app.go:32-35` 只有批准/拒绝）→ 工单会永久卡住。设计 §1.2 自己列的"pause/retry/resume/stop 必须全部保留"一条都没兑现。
5. **没有 Router，只有 Worker**：`engine/router.go:15-17,52-124` 的"Router"退化为一次无状态 `Assessor.Assess` 调用；没有 `orchestrator` 会话、没有全局记忆、没有回合内 Trigger 批处理、没有统一回复的 Router（回复是 `runtime/reply.go:34-116` 的模板）。设计 ADR-3/§4.5 的"官方双层 1:1"不成立。
6. **没有全双工/后台作业**：bash 同步执行（`agent/tools.go:34-153`），无 `start()/stop()`、无 jobs、无 steer/inject/已读回执/turnEnded；003 的 5 秒转后台、30 分钟规则、handoff、投递三段式全部失去前置条件。讽刺的是 **eino 已经提供 `adk/backgroundtask`、`adk.TurnLoop`、`adk/middlewares/patchtoolcalls`**，是"框架可用、功能未接"。
7. **入口边界绕过持久化**：GitHub/定时入口直调裸 `engine.Receive`（`cmd/zeokeel/github.go:70`、`extras.go:125`），不经过 `engine.Persistence.Receive`（它才写库并入 launch effect）→ durable 模式下这些 Trigger 不落库、不产生 launch effect、重启即丢。游标也只是 goroutine 局部变量（`github.go:58`），重启后从"现在"开始。**这直接击穿 README 的"kill -9 后接着跑"承诺。**
8. **假 sent + 未接线**：没有 driver 时 `sourceSender.Send` 直接返回 `OutcomeSent`（`cmd/zeokeel/sender.go:40-48`），面板显示"已发"但实际没发；IM 驱动又没接线 → 飞书/钉钉方向的消息全部"假成功"。
9. **运维面整块缺失**：没有 `status` 子命令、没有 0/1/2/3 退出码、没有文件日志、没有备份/归档、没有 watchdog/开机自启/休眠唤醒、没有管理 API；006 的"每小时巡检 + status 退出码"与文章 §6 Watchdog 都没有替代物。设计 §6.7/§8.4 的对应声明为假。
10. **诚实状态系统性失真**：`ov.Runtime.MaxJobs` 硬编码 2（`runtime.go:175`）、`Running/Today` 从不赋值、`ui/chrome.go:45` 恒显示"0/2 执行中"、总览恒"正常/今日 0"、评估页前两个总数恒等（`runtime/pages.go:202`）；违反 005 与设计 §6.5"计数必须等于它链接的列表长度"。

**做得对、应保留的部分**：内容派生 ID 与 workspace 前缀隔离；`StageOf` 派生而非状态字段（除 schema 里的只写缓存列）；单写者 SQLite + 嵌套 SAVEPOINT + afterCommit；durable Effects + 幂等 Outbox + GitHub marker 幂等；Worker 每 Ticket 一个会话、不直接对外说话（工具集无 reply）；eino ADK + 官方 summarization/reduction/subagent 中间件；mygo 快照架构（atomic.Pointer + Invalidate）；零 cgo 守卫真实通过；470 个测试 `CGO_ENABLED=0 go test ./... -skip Live` 全绿。

---

## 1. 审计基线与方法

| 项 | 值 |
| --- | --- |
| Go / 依赖 | go1.27.3；直接依赖 6 个：eino、eino-ext/openai、mygo、eino-contrib/jsonschema、MCP go-sdk、modernc.org/sqlite |
| 规模 | 234 个 Go 文件；生产 26,980 行 / 测试 16,974 行；`_ref` TS 12,390 行；原型 docs 000–008 共 1,973 行 |
| 构建/测试 | `CGO_ENABLED=0 go build ./cmd/zeokeel` 通过；`CGO_ENABLED=0 go test ./... -skip Live` 全绿（470 个测试）；`scripts/check-nocgo.sh` 四步 PASS |
| 约束 | 全项目零 cgo（硬约束）；mygo 主线程每帧重跑；单文件单二进制 |
| 已知环境限制 | 无真实显示器/托盘；未调用真实 GitHub/飞书/钉钉凭据；真实模型 live 测试需环境变量（未跑）；macOS/Windows 真机安装未验证 |

**严重度口径**：P0 = 会破坏 1.0 的可靠性/安全承诺或 CI 必红；P1 = 用户可见错误、明确违反需求、或框架能力已具备却未用；P2 = 可维护性/重复/死代码。

---

## 2. 框架使用盘点（先肯定）

| 框架 | 使用情况 | 评价 |
| --- | --- | --- |
| `cloudwego/eino` + `eino-ext` | ADK `ChatModelAgent` + `Runner` + 官方 `session.FileStore`；`summarization`（压缩）、`reduction`（工具结果落盘）、`subagent`（子智能体）三个官方中间件都在用；MCP 侧用官方 go-sdk | **正确**。Runtime 主体是"框架 + 领域胶水"，没有重写 agent loop（`engine_react` 是死的另一条路，见 §3） |
| `egoist/mygo` | `Themed/Modal/SearchField/TextInput/List/Scroll/Icon/Image/Tray` 都在用；快照架构与主线程约束执行严格；没有自造 UI 框架、markdown/diff/editor/chart | **正确**，只有 4 处把已有控件手工拼回去（§3.2） |
| `modernc.org/sqlite` | 单写者、WAL+synchronous=FULL、单连接 | **正确**（符合零 cgo） |
| 官方 MCP go-sdk | stdio + streamable HTTP、工具名规范化、错误映射 | **正确**（无手写 JSON-RPC） |
| `CGO_ENABLED=0` | 全链路可构建；CI 有交叉编译 job | **正确**（但 CI 的 `-race` step 与它冲突，见 §3.3） |

一句话：**这个项目不是"什么都要自己写"的项目**；问题集中在几个具体层，而不是架构性 NIH。

---

## 3. 不必要的自研（合并 4 个切片，去重后按优先级）

> 每条格式：现状（file:line）→ 应使用 → 为什么 / 代价。完整证据与迁移草稿见各分片报告。

### 3.1 P0（先修，否则 1.0 的承诺不成立）

| # | 现状 | 应使用 | 说明 |
| --- | --- | --- | --- |
| N-1 | 飞书 1,266 行 + 钉钉 963 行手写 HTTP 客户端，且**未接线**（`internal/im/feishu/*`、`internal/im/dingtalk/*`；`startIMLoop` 无调用者） | `github.com/larksuite/oapi-sdk-go/v3`（纯 Go：token 缓存、发/回/卡片/成员、签名校验、AES 解密、WS 长连接）+ `open-dingtalk/dingtalk-stream-sdk-go`（纯 Go，只补 Stream，别整体迁 alibabacloud SDK） | 先决策：1.0 去掉（删代码/加 build tag + 改文档）或接线；若接线，用 SDK 顺带补上 README 承认缺失的 WS |
| N-2 | 悬空 tool_call 恢复未接线：`Runner.Resume`（`agent.go:308`）零生产调用者，重启走 `RunSession` | eino `adk/middlewares/patchtoolcalls` + 明确"重启走 Resume 还是 RunSession" | 崩溃后真实 provider 可能 400；这是 1.0 可靠性承诺的直接反例 |
| N-3 | `internal/agent/tools.go` 200 行手写 read/write/edit/glob/grep | eino `adk/middlewares/filesystem`（同名工具、行号 read、`**` glob、大结果落盘，`ToolConfig.Name/Desc` 可保留现有名字/中文描述） | 项目自己的设计文档 §5.1 就写了用 eino 脚手架；迁移时注意关掉它的落盘避免与现有 reduction 双写 |
| N-4 | CI 闸门：`.github/workflows/ci.yml:8-10` 全局 `CGO_ENABLED=0`，`:35` 跑 `go test -race` | 二者取一：该 step 单独 `CGO_ENABLED=1`（并在文档解释与硬规则的字面冲突），或去掉 `-race` | 实测 `-race requires cgo`；当前 CI 按写法不可能绿（本地 release-check 不带 -race 所以"本地全过/CI 红"） |

### 3.2 P1（用户可见错误 / 明确该用框架）

| # | 现状 | 应使用 | 说明 |
| --- | --- | --- | --- |
| N-5 | `glob` 用 `filepath.Glob` 却宣传 `**`（`agent/tools.go:124,135`） | `bmatcuk/doublestar/v4`（已因 eino 在模块图，零新增网络成本） | 实测 `a/b/c/z.go` 匹配不到；工具描述是"对模型的承诺" |
| N-6 | `inside()` 词法越界检查（`agent/tools.go:294-308`） | stdlib `os.Root`（Go 1.24+，内核级不越界）+ `filepath.IsLocal` | 符号链接可逃逸；与 skills 的同类问题一起改 |
| N-7 | `internal/skills` 847 行自建 SKILL.md 加载/匹配/注入 | eino `adk/middlewares/skill`（frontmatter、filesystem Backend、`skill` 工具、fork/fork_with_context）+ `agentsmd`（AGENTS.md 注入） | 需评估：本项目用"确定性关键词匹配 + 16KB 预算"替代模型选技能，语义不同；至少把 front matter/幂等注入/刷新交给框架，匹配策略保留并写明理由。另：`adk/middlewares/agentsmd` 存在，而本项目只在 scan 里读 AGENTS.md 标题、从未注入 Worker |
| N-8 | UI 4 处手搓 mygo 控件：消息流（`ui/stream.go:17-36`）、评估页表头（`ui/eval.go:97-127`）、命令面板选择（`ui/search.go:98-143`）、两个 List 未设 `ListState.Key`（`ui/app.go:352-353`） | mygo `ui.List`(`ListState{Key,FollowEnd}`)、`ui.Table`、`ui.List`+`Header` | `Key` 是**真 bug**（倒序重排后展开行错位）；其余是虚拟化/键盘语义倒退 |
| N-9 | `tokens.go:33-83` 两套 hex 硬编码、`theme()` 永远 `LightTheme()` | `c.Theme()` / `ui.DarkTheme()` / `ui.Themed`（mygo 自带跟随系统外观） | 暗色模式实际失效；两套 `Radius` 已漂移（8 vs 12） |
| N-10 | `groupedNumber`（`ui/widgets.go:98-116`）重复 `humanize.Comma`；`trimSpace`（`ui/input.go:48-57`）重复 `strings.TrimSpace` | `github.com/dustin/go-humanize`（indirect 已在图）、stdlib | 全角空格会被当任务派发；humanize 已是间接依赖 |
| N-11 | `humanAge` 三份不同措辞（`ui/overview.go:354`、`runtime/pages.go:365`、`brief/digest.go:161`）、`humanBytes` 两份、`truncate`/`clip` 五份 | 一份内部 helper + `humanize`（英文场景）；中文相对时间保留但收敛 | 用户可见不一致（"12 分前"/"12 分钟前"） |
| N-12 | cron `N/step` 只匹配 N（`schedule/cron.go:87-111`）；DST 用 17 次时区探测（`schedule/next.go:60-89`） | 修 parser；DST 换 `time.Date` 往返（实测 8 行，含 gap/30 分钟 DST/午夜空洞） | **静默错调度**（如 `0/6`），无测试覆盖；`robfig/cron` 不建议（DOM/DOW 语义差异 + DST 更弱） |
| N-13 | `schema_v3.go` 用旁路 marker（`SchemaVersion` 仍 =2，v3 表由各包 `EnsureV3` 建） | 折进现有 `migrations[]` + `SchemaVersion=3`（不需要新的 migration 框架） | 两个版本真相；且 v1/v2 用裸 `CREATE TABLE`、v3 用 `IF NOT EXISTS`，按注释折叠会"table already exists"让老库打不开 |
| N-14 | GitHub `gh` CLI 子进程 + 手写分页/幂等（`sources/github/poll.go`、`channel.go:107-131`） | `github.com/google/go-github/vXX` + `x/oauth2`（已在图）+ `gh auth token` 取凭据 | 现状：无 ETag/条件请求、单页 50/100 上限、marker 只搜第一页 → 线程 >100 评论会**重复评论**、通知 >50/轮静默丢；引入 SDK 后 HTTP error 也能替代 stderr 正则。ADR-7 选 gh 有"复用登录态"的理由，但代价被低估 |
| N-15 | 钉钉 transport 内 `im.Do` 重试 POST，绕开 driver 的 `sent` 去重（`dingtalk/transport.go:304`） | 发送 `Attempts:1`，重试交给 Outbox | 重复消息风险（与 N-1 同一处，接 SDK 时一并修） |

### 3.3 P2（可维护性，不急）

- **eino `backgroundtask`/`TurnLoop` 未用**：后台作业、5 秒转后台、steer/inject 都是 eino 已有能力（`adk/backgroundtask`、`adk.TurnLoop` Push/Preempt、`adk.CheckPointStore`），项目却只有同步 bash → 这是"N-2 同类"的能力缺口，不是 NIH，但修复时应优先用框架原语（见 §5）。
- **`engine_react.go` + `Runner.Run` 是死生产代码**（只有 7 处测试调用点）；CHANGELOG 宣称修复的 `bufferToolCallChecker` 只活在这条死路，ADK 生产路径没吃到。
- **4 处设置解析重复**（`runtime/durable.go:78-127`）、**MCP 结果截断重复 reduction**（`mcp/result.go`）、**MCP 管理 API/Restart/Ping 未接线**、**workflow 的 SQLite 持久层只有 `-pipeline` 在用**、**`SessionStats`/`LoadEvents` 测试专属**。
- **`design/prototype/` 是 3,015 行陈旧分叉**：`go.mod:14` `replace => /tmp/mygo`，不可复现；与 `internal/ui` 重复 15 个函数/28 个 hex；不在根 module 测试范围。要么钉版本+标注"视觉参考、勿回迁"，要么删。
- **死代码/资产**：`view/demo*.go` 572 行（生产零引用，M0 文档承诺删除）；9/30 未用图标；`App.inspect/focusComposer/wsPick...`；`store.Health`/`OpenTickets`/`domain.ParseThreadKey`/`brief.Threshold/OneLine`；`tools/seedstate`（`-allow` 用 `fmt.Sscanf("%[^:]")` 必失败）。
- **`bash` 的 blockedCommands 默认无效**：`main.go:76` 写 `"git push --force"`，`tools.go:230` 只比对 `firstWord` → 永远匹配不到（测试用单词 `"git"` 所以没暴露）。
- **工具 error 会整轮失败**：bash 超时/read 越界/edit 命中多次返回 Go error，eino 把工具 error 当整轮失败（`compose/tool_node.go:1347`），与设计 §5.5/§5.8"工具问题应是模型可读 observation"相反。

### 3.4 结论：需要新增依赖吗？

- **不需要**（除 N-1/N-14 三处官方 SDK）：`os.Root`、`time.Date`、`strings`、`text/template` 级别的能力、eino 官方中间件，以及模块图里已有的 `doublestar`、`humanize`、`x/time/rate`（注意它们目前是 `// indirect` 且仓库代码未 import；提为直接依赖不需要网络）。
- **只有 3 处建议新依赖**：Lark v3、DingTalk Stream SDK、go-github（均可保持零 cgo；迁移后必须跑 `scripts/check-nocgo.sh` 第 3 步实测）。
- 反过来，**不建议**为 cron 引入 `robfig/cron`（语义差异 + DST 更弱），也不建议用 backoff/retryablehttp 替换 IM 重试（`Retry-After` 与"发送去重分层"是领域不变量）。

---

## 4. 对 dsh-superagent-prototype（_ref/docs 000–008）的符合性

### 4.1 总表

| 需求组 | 条目 | CONFORM | PARTIAL | MISSING | DEVIATES-JUSTIFIED | DEVIATES-UNJUSTIFIED | 备注 |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 000 哲学 | 7 | 1 | 5 | 0 | 0 | 1 | 面板注意力优先 ✅；"few concepts"被 Proposal/Goal/Schedule/ReplyMode 破坏 |
| 001 领域 | 20 | 5 | 9 | 5 | 0 | 1 | 内容 ID/幂等/executor 在领域外 ✅；Eval/Thread/Mandate 语义缺失 |
| 002 授权 | 23 | 1 | 6 | 16 | 0 | 0 | 确定性 strength/admission/Mandate 面全部缺失 |
| 003 执行 | 18 | 0 | 10 | 8 | 0 | 0 | 阶段表骨架 ✅，结束/PR/投递三段式/slot/retry 缺失 |
| 004 提示词 | 23 | 3 | 9 | 9 | 1 | 1 | 分层+插值真；版本/base-hash/占位符/长度/热更新缺 |
| 005 面板 | 19 | 1 | 13 | 4 | 0 | 1 | 页面在、诚实状态系统性失真 |
| 006 部署 | 20 | 0 | 7 | 10 | 2 | 0 | 桌面形态有据，但 CLI/退出码/日志/备份/巡检整块缺失（另有 1 项 EXTRA） |
| 007–008 架构 + 官方架构 | 80 | 10 | 36 | 27 | 3 | 1 | Effects/Outbox/Store 最强；入口持久化/Router/watchdog 最弱（含 2 EXTRA、1 NOT-REQUIRED、11 条官方架构） |
| 008 扩展路径 | （含上） | — | — | — | — | — | "加一个 Source"今天必须改 `cmd/zeokeel`，11 步 cookbook 不可执行 |
| **合计（可核对项）** | **210** | **21 (10.0%)** | **95 (45.2%)** | **79 (37.6%)** | **6 (2.9%)** | **5 (2.4%)** | 另有 EXTRA 3、NOT-REQUIRED 1；007–008 行含 11 条官方架构条目，故与纯原型 199 条略有差异 |

> 数字不是为了"打分"，而是说明结构：**骨架和确定性内核在，承重语义大面积缺**。

### 4.2 最强的偏离（按后果）

| 后果 | 需求（doc:line） | 现状（file:line） |
| --- | --- | --- |
| 越权/安全阀失效 | 002:11-16,50-54,101 确定性 gate 产出 strength | gate 只返回 bool（`engine.go:338-369`）；strength 来自模型 JSON（`prompts/defaults.go:74`）→（`engine.go:216-221`） |
| `auto` 永不生效、Ticket 由人而非 Eval 创建 | 001:35；002:89,99,101 | 所有同事 accept → `DecPropose`（`engine.go:240-244`）；`Approve` 建 Ticket（`engine.go:636-679`）；测试自认（`engine_test.go:493-497`） |
| 重复评论/重复工单 | 001:37"扇出不进 Eval"；002:20,22 | fan-out 只 append `ContextInput`（`engine.go:312`），同条消息继续 Eval（`engine.go:163-211`） |
| 后续讨论丢失、follow-up 归属全错 | 001:37；003:112 | `ContextInput` 从不投递（`runtime/worker.go:476-484`）；无 `followupOwners`/`letGo`/handback 工具 |
| 工单永久卡住 | 003:94；001:101 | ready 不结束 Ticket（`engine.go:851-857`）；`Attention` 无清除路径（`engine.go:727-739`）；无 stop/retry/pause |
| 重启丢事件 / 丢执行 | 007:46,68,190/008:322；003:144 | GitHub/定时直调裸 `Receive`（`github.go:70`、`extras.go:125`）；游标为局部变量（`github.go:58`）；`Resume` 零调用 |
| 谎报已发送 | 003:175-191（Source off → waiting） | 无 driver 时 `OutcomeSent`（`sender.go:40-48`） |
| 无 PR 生命周期 | 003:96-110；001:37 | 无 `follows`/`followsDone`/`merged/closed`；`Ended.Outcome` 四值成死值 |
| 并发/人的优先 | 003:55-61（人的消息 +1 slot） | 只有 effects `Limit`，无 human 额外 slot；launch 还会自动重试（`durable.go:41-45`） |
| 无异步/后台 | 003:31-39；003:112 | bash 同步；无 jobs/steer/inject/turnEnded（eino 有原语，未接） |
| 设置与凭据 | 006:218；ADR-12 | API Key 明文进 SQLite（`onboarding.go:68`）；worker bash 继承全量环境含 `*KEY*/*TOKEN*`（`agent/tools.go:247-253`） |
| 向导配置不落库 | 006:143-158 | `SaveOnboarding` 只改内存 `r.settings`（`onboarding.go:104-148`），重启丢失（`main.go:113-121` 再从 store 读） |
| 消息面缺失 | 001:27（Source 覆盖） | GitHub 只做 notifications+comments（无 CI/PR 订阅/接收全部）；Feishu 未接线；DingTalk `StreamEvents` 恒 `ErrUnsupported` |
| 扩展路径不可用 | 008（11 步 cookbook） | 无 Source 实例持久配置/面板表单/host 对象/游标持久化/threadContext/policy part/env/文档 |

### 4.3 设计自己新增的概念（EXTRA，按 000 的"少概念/少设置"判定）

| 新增物 | 判定 | 说明 |
| --- | --- | --- |
| `Workspace` 持久实体 + 全表 `workspace_id` | **DEVIATES-JUSTIFIED（有据）** | 多仓库/数据目录是原型单仓库假设的直接后果；设计 §4.5 付了对价（范围声明 + ID 隔离纪律）。代价是 001 的 D-9/D-18/A-3 都要按工作区重写，而它们没有 |
| `Decision.kind=propose` + `Proposal` 四要素 + 待批单 | **未支付的语义债** | 对"代理授权放大"是合理答案，但同时改掉 001:35/002 的 explicit/auto 语义且未同步规格 |
| `ReplyPlan`/`ReplyMode{notify,assist,auto}` | **概念可留、实现空转** | `ReplyPlan` 只进面板展示（`runtime.go:279`），`deliverReply` 不读它（`reply.go:81-116`）→ 默认 notify 没挡住任何对外消息 |
| `Goal` 实体 + 表；`Schedule` 实体 + 两表 | **违反 000:19 / 设计 §5.9 自己的承诺** | 设计说 goal = Ticket `follows` + Assignment 继续轮次、schedule = Source kind；实现另建实体/表/Manager。功能可用，但概念膨胀 |
| 四个新设置（concurrency/command_timeout/model_timeout/poll_interval） | 部分合理 | `poll_interval` 与"一个默认值够用就不做设置"冲突 |
| `local` Source、`Self` 事实、`Artifact`、Workspace Brief | 可接受 | 有设计论证；Brief 的半成品（无 `workspace_propose` 待确认队列/预算/降级）需登记 |

---

## 5. 对官方《Super-agent 设计探索》（五模块）的符合性

> 用户提供的文章（design §4.5 转引）。独立核对 13 条架构要求 + 004–006 交集 6 条，去重后：

| 文章模块 | 要求 | 判定 | 证据 |
| --- | --- | --- | --- |
| §2 Router 层 | 唯一常驻全局 LLM Session；全局上下文/记忆；承接人类交互、路由派发、统一回复；不执行、可无 Bash；回合内 Trigger 合并批处理 | **MISSING** | 没有 orchestrator 会话（`grep -i orchestrator` 为空）；`engine/router.go:52-124` 是一次 `Assessor.Assess`；回复由模板拼（`runtime/reply.go:34-116`）；无批处理（`engine.go:198-289` 逐条 decide） |
| §2 Worker 层 | 每 Ticket 一个上下文隔离 Session；完整工具；不直接对外说话；向 Router 汇报；可含 subagent | **PARTIAL** | 隔离/完整工具/无 reply 工具 ✅（`runtime/worker.go:246-290`、`runtime/artifacts.go:20-113`）；"向 Router 汇报"不成立（没有 Router，汇报进 engine）；subagent ✅、agent-team 未做 |
| §3 两级过滤 | 确定性 Fast Path（source/scope/whitelist）+ 语义 Slow Path | **PARTIAL** | 结构在（`github/scope.go`、`im/addressing.go`、`engine/router.go:86-97`）；但确定性层不产出 strength，admission table/bot list 缺失 |
| §3 订阅直通 | Thread 后续事件直达订阅 Worker、绕过 Router；多对多；可退订/交回；Router 只派发未订阅 | **MISSING** | `engine.fanoutLocked` 只 append `ContextInput`（`engine.go:289-318`）；无投递/退订/交回；同条消息仍进 Eval |
| §3 Inline & Spill + 链式召回 | ID+摘要内联；长内容 spill 可按需读；Trigger→Thread→Session ID 链 | **PARTIAL** | brief 内联 ✅、工具输出落盘 ✅；无 `read_domain`/`workspace_read`/`recent_tickets` 工具，模型无法按 ID 展开 |
| §3 投机执行 | 不确定结果时后台提前行动，有价值就交付 | **MISSING** | 无对应机制 |
| §4.1 全双工异步 | `start()/stop()`、后台结果作为新事件、不阻塞新输入、steer/inject/已读回执/turnEnded | **MISSING**（面板可输入只算一半） | bash 同步（`agent/tools.go:34-153`）；无 jobs/steer/inject；`RunSession` 只能整体 cancel；eino 已有 `backgroundtask`/`TurnLoop` 未用 |
| §4.2 多信道适配 | IM 低噪音（≤100 词硬规则）、GitHub/邮件结构化高密度 | **MISSING** | 全 Source 同一段通用回复（`runtime/reply.go:34-76`）；无频道参数/100 词校验；Worker 提示无频道/受众信息 |
| 无会话管理 | 用户不应感知 Session；UI 是连续单一面 | **CONFORM** | 面板无会话列表/切换；会话只是内部文件 |
| §5 计算/状态/环境分离 | 无状态 loop + 状态在 DB；本地 daemon；跨设备/休眠/离线 | **DEVIATES-JUSTIFIED** | 单机单二进制（ADR-1）有论证；保住：SQLite 状态、Effects/Outbox、崩溃恢复、schema 守卫；丢失：休眠唤醒、离线补偿、跨设备、daemon 边界；会话状态在 ticket workdir 的 eino JSONL 而非 DB |
| §6 Watchdog | 外部独立 Control Loop（完整工具 Session、定时驱动、监控基础设施+任务进展、可在线修正、主服务挂了仍在） | **MISSING** | 无 watchdog/status/0-1-2-3/小时巡检/管理 API；托盘 + 2s republish 不是监督；设计 §6.7/§8.4 声明为假 |
| §8 开放问题（A2A/多租户/安全） | 文章明确列为开放问题 | **NOT-REQUIRED** | 不应按缺失计 |

**四特征记分卡**

| 特征 | 现状 | 判定 |
| --- | --- | --- |
| 连续 | 跨 IM/GitHub 的单一 Agent 体验没有载体（无 Router 全局记忆/统一会话）；Worker 内有一定连续性 | ❌ 大部分缺 |
| 高效 | Worker 可并发（effects 队列），但 Eval 在**全局 Engine 锁内调真实模型**（`engine.go:152-154` + `router.go:95-97`），UI 主线程同步 `Dispatch`（`ui/input.go:36-43`）→ 面板最长冻结 90s、所有 Source 串行；无全双工 | ❌ 明显不成立 |
| 主动 | 事件源 + gate/observe + 定时/目标工具；无投机执行；飞书/钉钉收不到消息 | 🟡 部分 |
| 持久 | 崩溃后 SQLite/Effects/Outbox 可恢复；但无 watchdog/自启/唤醒，Assignment 崩溃不恢复，入口绕过持久化 | 🟡 部分 |

---

## 6. 设计文档自身的可信度（"以代码为准"后的核对）

| 设计声明 | 结论 | 关键证据 |
| --- | --- | --- |
| §11 "§4 领域层 ✅"、"§4.3 gate + admission + accept mode ✅"、"§4.4 不变量 11 条全部有测试 ✅" | **高报**：domain 无 `Thread`/`Eval`/`UserError` 类型；无 admission table；`internal/domain/invariants_test.go` 不存在，12 条不变量约 3 条有测试 | `design:1837-1859`；`internal/domain/` 仅 5 个测试 |
| §11 "§5.2 会话/Resume ✅""§5.8 崩溃恢复 ✅" | **高报**：Resume 零生产调用；无运行中 Assignment 恢复；入口绕过持久化 | `agent.go:308`；`cmd/zeokeel/github.go:70` |
| §11 "§4.8 IM ✅" | **假**：驱动未接线 | `go list -deps ./cmd/zeokeel`；`extras.go:157` 无调用者 |
| §11 "§5.10 能力矩阵 ✅" | **部分假**：无 jobs/todo/web/plan mode；search 仅面板 ⌘K | `agent/tools.go` 六工具 + `runtime/artifacts.go` |
| §11 "§7 v1–v3 ✅""§7.3 归档第一天就做" | **假**：v3 走旁路不升版本；无 archive/VACUUM/TTL | `schema_v3.go`；`grep archive` 无实现 |
| §8.1 "CLI：`zeokeel status|check|login|setup`"；§8.4 "slog 日志 + status 退出码 0/1/2/3" | **假**：无子命令、无 slog 文件日志、无 0/1/2/3；且 §6.7 写 0/1/2、§8.4 写 0/1/2/3 自相矛盾 | `cmd/zeokeel/main.go:32-53` |
| §8.3 ".dmg/NSIS/deb/PKGBUILD/updater 由 mygo build 提供" | **假**：只有 zip/tar + icons + Info.plist/desktop/install.ps1 | `scripts/package.sh` |
| §6.7 "托盘 + 开机自启 + 进程内 watchdog + 休眠唤醒补 pass" | **三项不存在**（只剩托盘/关窗隐藏） | `ui/tray.go`；`grep Watchdog` |
| ADR-12 "系统钥匙串" | **假**：API Key 明文 SQLite（文件 0600） | `runtime/onboarding.go:68` |
| §1.5 "维护 `design/rejected-concepts.md`" | **文件不存在** | `design/` 列表 |
| §1.2 "pause/retry/resume/stop、PR follows、三段式投递、admission 全部保留" | **均未达到** | 分片矩阵 |
| §4.1 类型映射表（`domain.Source/Thread/Eval/UserError` 等） | **目标而非现状** | `internal/domain/` |
| `design/prototype/` "可运行真代码" | **不可复现**：`replace => /tmp/mygo`，且 `/tmp/mygo` 与 v0.7.4 不同 | `design/prototype/go.mod:14` |
| §9 零 cgo | **真** | `scripts/check-nocgo.sh` 实测 PASS |

**结论**：设计文档的"要什么"部分有价值（它确实读懂了原型），但"已有什么"部分不可作为状态依据。按文档自己的规则"有冲突时以代码为准"，§11 需要整体重写；README/CHANGELOG 的"完整移植/已实现 IM/崩溃恢复"也要降级。

---

## 7. 建议的处置顺序

### 第一步：先决定"产品是什么"（否则修不完）
- 方案 A（推荐给 1.0）：**承认这是"简化版单机 AI 同事"**，把 README/CHANGELOG/设计 §11 改成真实实现，删掉/标注未接线的 IM、watchdog、status、退出码、`follows`/`followupOwners` 等承诺；1.0 只承诺"本地面板 + 单工作区 + 单一 Source"。
- 方案 B（按设计文档补齐）：按 M2/M4 重做 Router 会话 + Eval 批处理 + subscription 直达 + followups + 投递三段式 + slots/stop/retry + watchdog；这是数周级工作，且要先修 §3 的 P0。
- 两条路都必须先处理下面的"事实性 bug"。

### 第二步：P0 事实性修复（1–3 天量级）
1. IM 决策：接线或删除 + 改文档（N-1）。
2. 悬空 tool_call 恢复接线（`patchtoolcalls`）（N-2）。
3. 入口走 `Persistence.Receive`（GitHub/定时/Dispatch 统一），游标落 kv，修假 `sent`。
4. CI `-race` 与零 cgo 取一（N-4）。
5. `Attention` 增加"回答"命令/API；`ready` 按 003 规则结束 Ticket（或明确产品语义并改文档）；补 stop/retry/pause（至少 stop）。
6. Eval 移出全局锁（锁内只做状态迁移，模型调用在锁外/队列里）+ 修 `ov.Runtime` 硬编码（N-10 的诚实状态）。
7. `blockedCommands` 多词匹配、glob `**`、schema v3 折叠、onboarding 落库。

### 第三步：框架化改造（每项 S–M）
- Agent 工具层迁 eino `filesystem`（N-3）、`os.Root`（N-6）、`doublestar`（N-5）、`patchtoolcalls`（N-2）。
- IM/GitHub 三处官方 SDK（N-1/N-14），迁移后跑零 cgo 守卫。
- UI 四处控件 + `ListState.Key` + theme（N-8/N-9）、`humanize`（N-10/N-11）。
- cron `N/step` + `time.Date`（N-12）；`skills`/`agentsmd` 评估 eino 中间件（N-7）。

### 第四步：清理（不影响行为）
删死代码（`engine_react`、IM 未接线、`demo*.go`、MCP 管理 API、workflow 持久层、9 个图标、`seedstate`）、合并重复 helper、`design/prototype` 钉版本或删。

---

## 8. 证据与分片报告索引

| 分片 | 文件 | 行数 | 内容 |
| --- | --- | ---: | --- |
| Agent Runtime / MCP / workflow / goal / prompts | [`audit/nih-runtime.md`](nih-runtime.md) | 581 | 24 项判定；P0 悬空 tool_call；eino 原语盘点（backgroundtask/TurnLoop/checkpoint） |
| Store / engine / domain / effects / outbox / schedule / skills / brief / scan | [`audit/nih-core.md`](nih-core.md) | 438 | 20 项判定；cron `N/step` bug、DST、schema v3、`os.Root`；不需要新依赖 |
| IM / GitHub / tooling | [`audit/nih-integrations.md`](nih-integrations.md) | 487 | 15 项判定；IM 未接线、官方 SDK、gh CLI、CI -race |
| UI / view | [`audit/nih-ui.md`](nih-ui.md) | 487 | 26 项判定；mygo 4 处手搓、ListState.Key bug、theme、honest count |
| 000–003 符合性 | [`audit/conformance-domain.md`](conformance-domain.md) | 382 | 68 条矩阵 + 13 条官方架构 + EXTRA 11 项 |
| 004–006 符合性 | [`audit/conformance-product.md`](conformance-product.md) | 238 | 62 条矩阵 + 官方架构 6 项 + 设计声明核对 |
| 007–008 + 官方架构符合性 | [`audit/conformance-arch.md`](conformance-arch.md) | 446 | 69 条矩阵 + 11 条官方架构 + schema diff + 扩展路径 |

复核命令（均可复现）：
```sh
export PATH=/usr/local/go/bin:$PATH CGO_ENABLED=0
go build ./cmd/zeokeel && go test ./... -skip Live     # 全绿（470 tests）
sh scripts/check-nocgo.sh                               # PASS
go list -deps ./cmd/zeokeel | grep internal/im          # 只有 internal/im，没有 feishu/dingtalk
go mod why -m github.com/bmatcuk/doublestar/v4          # 经 eino 在模块图，仓库未使用
```

**审计限制**：未接真实 GitHub/飞书/钉钉凭据、未跑真实模型 live 测试、无真实显示器/托盘、未在 macOS/Windows 真机验证；所有"能力已提供"结论均已核对库源码符号，但迁移可行性仍需 spike 验证。

---

*本报告由 7 个并行只读审计合并；所有 file:line 均指向审计时的 HEAD `03b25e9`。*

---

# 9. 整改结果（2026-11-03，分支 `fix/audit-remediation`）

> 本节是本报告的修复后状态；§0–§8 保留原始审计（历史快照，HEAD 当时为 `03b25e9`）。
> 详细逐条状态：[`audit/fixes/STATUS.md`](fixes/STATUS.md)；各 owner 交接：[`audit/fixes/`](fixes/)；独立反证：[`audit/verify/wave1.md`](verify/wave1.md)、[`audit/verify/wave2.md`](verify/wave2.md)、[`audit/verify/engine.md`](verify/engine.md)。

## 9.1 结论

**审计发现的 P0/P1 缺陷已全部关闭；框架化改造完成；dsh-superagent-prototype 与《Super-agent 设计探索》的承重语义补齐到"可实现范围"，不可实现项以 documented-deviation 明写；文档与代码已对齐，不再有"设计 §11 高报"。**

- 机械闸门：`sh scripts/verify-fixes.sh` → **5/5 PASS**（零 cgo build、test、vet、check-nocgo、`CGO_ENABLED=1 -race` 全绿）；`scripts/release-check.sh` → **16 通过 / 0 失败 / 6 跳过**（跳过的都是需要真机/真实凭据的项）。
- 独立反证：Wave1 69 PASS/10 FAIL（FAIL 均属 Wave2 在途或文档，非 Wave1 行为）；Wave2 **96 PASS / 1 FAIL（仅 5 处文档行号，已修）/ 1 UNVERIFIED，无代码缺陷**；engine 27/27、runtime 20/20、UI 9/9、gate 5/5。
- 全量测试：`CGO_ENABLED=0 go test ./... -skip Live -count=1` 连续 2 次全绿（27 包）；`-race` 全绿（含 cmd/runtime/engine/store）。
- 交叉编译：linux/darwin/windows × amd64/arm64 全过；`scripts/check-nocgo.sh` PASS；`go vet` / `gofmt` 干净。
- 真实二进制 CLI：`status` 空安装=1、`check` 空安装=3、`setup` 无参=3、`stop` 写 `<stateDir>/STOPPED` 且 status=0、`backup`/`archive --dry-run`、`-headless` 信号 drain 均验证。
- 依赖：仅新增经批准的纯 Go 依赖（`open-dingtalk/dingtalk-stream-sdk-go v0.9.1` + `gorilla/websocket` indirect）；`doublestar`、`go-humanize`、`x/sys` 由 indirect 提升为 direct；零 cgo 守卫仍 PASS。
- `VERSION` 保持 `1.0.0-rc.1`（发布需 macOS/Windows 真机、真实凭据、签名/公证等 §9.4 剩余项；不提前标 1.0.0）。

## 9.2 主要改动

| 领域 | 改动 |
| --- | --- |
| Agent Runtime | eino `patchtoolcalls` 接入 + `RunSession` 崩溃恢复；read/write/edit/glob/grep 迁 eino `adk/middlewares/filesystem` + `os.Root`；`glob` 用 doublestar；eino `backgroundtask`（5s 前后台、job 工具、30 分钟规则）；工具错误转 observation；blockedCommands 全词前缀；worker shell 环境变量脱敏（KEY/TOKEN/SECRET/PASSWORD/CREDENTIAL）；删除 `engine_react`/`Runner.Run`/`Runner.Resume` 与死 API |
| 领域/Engine | 重写：确定性 gate strength + 3-cell admission table + bot list + accept mode；两阶段 Eval（Receive 不调模型，`EvaluateDue` 锁外调用、批处理、deadline/3 次超时、policy 重排、pause 顺延）；Ticket Inputs/Delivery/Follows/Ownership/CanRetry；`StageOf` 严格 003 表；ready/dropped/stop/retry/pause/resume/AnswerAttention；订阅 fan-out→Input、owned 不进 Eval、HandBack/DropSubscription/MarkUnrelated、ended-not-stopped resume + revalidate；Mandate basis/rules/scope；深拷贝快照（`TicketSnapshot`/`AllTickets`/`Feed`）与 `InterruptAssignment`；Persistence 全命令持久化 + human slot effect |
| Runtime/CLI | Worker inbox + 投递三段式（delivered/read/turnEnded）+ 投递失败重试 + Assignment 崩溃恢复；2s Eval pass；入口统一 Persistence + kv 游标；sender 不再假 sent；onboarding 落库；诚实投影（Running/MaxJobs/Queued/Today/Activity/Health/EvalCounts/PolicyVersion）；PR 生命周期（`ThreadClosed`/`follow` tool）；prepare 命令 + 执行变量（10m、失败不自动重试）；`status/check/setup/login/stop/backup/archive` + 0/1/2/3 + `--json`；真正 `-headless` 服务；slog 文件日志（按天轮转/14 天）；进程内 supervisor；`VACUUM INTO` 备份与 `archive.db` 归档；提示词团队三段/版本历史/base-hash/校验/热更新；按频道回复（IM ≤100 runes、GitHub 结构化）+ ReplyPlan Held/notify 生效 |
| 存储/Schema | SchemaVersion=3，v3 折叠进有序 migrations（旧 v1/v2/M4 库原地升级）；stateDir flock + `store.Inspect`（locked 时只读 status）；`database/sql` Tx + 嵌套 SAVEPOINT + afterCommit；trigger.at 定宽；trigger_thread/decision.thread/outbox.dropped_*；cron `N/step` 修复 + DST `time.Date` 重写；skills `os.Root` |
| 集成 | 飞书 token 单飞/签名/AES/分页/重试分层；钉钉官方 Stream SDK 接收（重连/去重/背压）+ JSON 转义 + 发送去重；GitHub 全分页/ETag/marker 全页/ReceiveAll/ObserveCI；IM 真实接线 + 来源 health |
| UI | mygo `List/Table/Header/ListState.Key`（选择跟随实体）；Theme 派生 + 暗色；humanize/TrimSpace/humanfmt；总览动作（回答/重试/停止）+ Ticket 详情 + 五组设置 + 部署控制台 + glossary；诚实计数；删除 demo/未用图标/死字段/无动作托盘项 |
| 文档/CI | CI 唯一闸门 `verify-fixes.sh`（-race 单步 cgo）；README/CHANGELOG/RELEASE/STEPS/设计 §13 全面真实性重写（每个 ✅ 指生产代码）；新增 `design/ops-runbook.md`、`design/how-to-add-a-source.md`、`design/rejected-concepts.md`；systemd/launchd/watchdog-check；prototype 钉 mygo v0.7.4 可离线构建 |

## 9.3 明确记录的偏离（documented-deviation）

这些是产品/平台取舍，不是未修 bug；均在 README/设计/ops-runbook 与实际行为中一致写明：

1. **Router 是"逻辑 Router"**：持久化全局状态 + 两阶段批处理 + 统一回复，不是单个常驻 LLM Session（文章允许 Router 内部非单一 Session/部分非 LLM）。
2. **订阅直通**：Input/ownership/HandBack/退订已实现；**mid-turn steer/inject 未接**（Worker 在下一轮收到后续消息）；eino TurnLoop 接入列为后续。
3. **全双工后台**：jobs/后台唤醒/30 分钟规则实现；**start/stop 之外的 steer/inject** 未做。
4. **Watchdog**：进程内 supervisor + systemd/launchd 外部 watchdog 脚本；**独立的完整工具 Watchdog Session** 未做（桌面无宿主）。
5. **计算/状态/环境分离**：单机单二进制（ADR-1/10），保留 SQLite/崩溃恢复；**跨设备/休眠唤醒/离线补偿**不实现。
6. **管理 HTTP API**：桌面无 HTTP；`zeokeel status` CLI 为等价运维面。
7. **凭据**：worker env 已脱敏；API Key 仍为 0600 SQLite（**OS 钥匙串**未接）。
8. **IM/GitHub 传输**：DingTalk 官方 Stream SDK；Feishu HTTP 长轮询（**Feishu WS/媒体下载**未做）；GitHub 仍 `gh` CLI（**go-github 迁移**未做，已补分页/ETag/幂等）。
9. **Worker bash 的 ticket 环境变量**：prepare 命令与 prompt 已有；bash 子进程注入未接（prompt 携带值）。
10. **打包**：tar/zip 已有；**.dmg/NSIS/deb/PKGBUILD/updater/CI 出包**未做。
11. **领域类型**：`domain.Thread`/`UserError` 未新增（设计目标 vs 实现已在 §4.1 标明）；`Goal`/`Schedule` 仍为独立实体（设计 §5.9 的 Source-kind 方案未采纳，理由见 rejected-concepts）。
12. **文章 §8** 的 A2A/多租户/安全为开放问题，**NOT-REQUIRED**。

## 9.4 仍不可在此环境验证（发布前必做）

- 真实 GitHub/飞书/钉钉凭据端到端（尤其 DingTalk Stream 连接、Feishu 事件/图片）；未联网调用真实 API。
- 真实模型 live 测试（`ZEOKEEL_LIVE_*`）。
- 真实窗口/托盘/X11/Wayland、macOS 签名/公证/.dmg、Windows NSIS/真机安装。
- 跨机器 7×24 常驻（systemd/launchd 模板仅静态验证）。

> 修复分支的完整 diff、逐条状态与反证证据见 `audit/fixes/STATUS.md` 与 `audit/verify/`。
