# Changelog

本文件记录用户可见的变化。版本号遵循语义化版本；1.0.0 之前的 `-rc.N` / `-dev` 后缀表示
发布清单（RELEASE.md）尚未全部通过——`1.0.0-rc.1` 不表示功能完成。

## [1.0.0-rc.1] — 发布候选（2026-11-03，Wave 2 后）

**候选版不等于功能完成。** Wave 2 把审计中的领域/引擎/运行时/运维大部分缺口做完了；机械闸门
（build/vet/zero-cgo/package/release-check 文档产物）在 2026-11-03 最终冻结树木实测通过：
非 race 全量 `go test ./... -skip Live` 连续 2 次全绿，全量 `-race` 全绿。仍未完成的是产品面：
**打包只有 tar/zip（无 .dmg/deb/安装器）**、**系统钥匙串未做（API Key 明文 SQLite 0600）**、
**设置页规则/admission/Source 实例不可编辑**、**飞书只有 HTTP 长轮询（无 WS）且无媒体下载**、`steer/inject` 未做。
`VERSION` 是否升 1.0.0 由发布负责人按 RELEASE.md F 组决定。

机械闸门只有一条命令，本地与 CI 完全相同：

```sh
sh scripts/verify-fixes.sh    # CGO_ENABLED=0 build/test/vet + check-nocgo + CGO_ENABLED=1 -race
sh scripts/release-check.sh   # 版本/产物/文档 + 上面的闸门
```

**Wave 2 已交付（相对 Wave 1 的校正）**：
- 确定性授权：gate 产出 `Strength`/`Basis`（模型不再声明）、admission table 三格、bot list、团队规则、`RequireApproval`。
- 两阶段 Eval：`Receive` 只入队，`EvaluateDue` 每 2s 按 Thread 批量判定（模型调用不在入口锁内）。
- 生命周期：`Report` 结束 Ticket、`StopTicket`/`RetryTicket`/`AnswerAttention`/`Pause`/`Resume`、
  `DeliverInput`/`MarkInputsDelivered`/`MarkInputsRead`/`MarkTurnEnded`/`DeliveryFailed`、
  `BindFollow`/`ThreadClosed`/`HandBack`/`RevalidateAndResume`/`PolicyChanged`（`internal/engine/lifecycle.go`）。
- 崩溃恢复：启动时 `RecoverAssignments` 把中断的 Assignment 写成 note Input、重开 Assignment、唤醒；
  eino `patchtoolcalls` 补悬空 tool_call（不再重放坏历史）。**`Runner.Resume` 已在 Wave 2 清理中删除**：
  恢复只走 `RecoverAssignments` + `patchtoolcalls`，不再有"生产未用的恢复 API"。
- PR/Issue 生命周期（003:96-106）：GitHub 轮询遇到 PR/Issue 关闭时调用 `CloseThread` →
  `Persistence.ThreadClosed(merged|closed)` 结束跟随该 Thread 的工单；Worker 新增 `follow` 工具（own/context）→ `BindFollow`；
  `ready` 在跟随的 PR 未关闭前保持等待，关闭后按 merged/closed 结束（`runtime/github_lifecycle.go:39,88`、`runtime/artifacts.go:102`、
  `engine/lifecycle.go:48-53,368`、`cmd/zeokeel/github.go:76`）。
- 工单准备 `execution.prepare`（003:49-63）：`sh -c` 在 execution root（工单目录的父目录）执行、10 分钟超时、继承环境先剥离 `*KEY*/*TOKEN*/*SECRET*/*PASSWORD*`、
  再注入 `ZEOKEEL_WORKDIR/ZEOKEEL_TICKET_DIR/ZEOKEEL_TICKET/ZEOKEEL_TICKET_NAME/ZEOKEEL_REPO`；失败即失败、不自动重试；
  提示词展示同一组变量（`runtime/prepare.go:27,30,67,103,122`）。`execution.background_timeout` 设置（默认 1800s）已加到设置›执行。
- 30 分钟后台作业规则（003:33-39）：`watchJob` 到上限后 kill 作业并 `ReportWaiting`（写明 job id + 最后输出），**不唤醒** session；
  子智能体不是 shell job，不适用该规则（`runtime/background.go:22,89,127`）。
- worker 的 bash 子进程现在与 prepare/提示词拿到同一组 scrub 后工单变量：`agent.Config.Env`（`internal/agent/agent.go:52-56`）
  由 runtime 用 `executionVars` 填充（`internal/runtime/worker.go:429`）；凭证类变量在三处均已剥离。
- Agent 工具层迁到 eino 官方中间件：`filesystem`（read/write/edit/glob/grep，doublestar）、
  `backgroundtask`（`job_output`/`job_kill`）、`patchtoolcalls`、`reduction`、`summarization`、`subagent`；
  删除了死代码 `engine_react.go` 与自研 `bufferToolCallChecker`。
- 面板：待办行内动作（批准/拒绝/回答/重试/停止）、暂停恢复、Ticket 详情（Mandate/Inputs/Assignments/Follows/Artifacts/Delivery/Attention）、
  名词表（⌘/）、数量诚实化（Running/MaxJobs/Today/Queued/24h 活动由 runtime 真实投影）、深浅色跟随系统。
- 提示词：扩展为 6 层（Router/Worker/规则说明 + 团队三段 communication/scope/delivery），保存时校验未知变量/必须保留的占位符/超长拒绝、与默认相同则清除覆盖；`internal/runtime/prompts.go` 另有 base-hash/“Default updated”/最近 20 版历史的实现，保存路径已调用 `recordPromptMeta`（`pages.go:557`）。
- IM：飞书/钉钉**已接线**（`-feishu-*`/`-dingtalk-*` flag 或设置›来源 8 个字段；钉钉走官方 Stream SDK 长连接，
  飞书走 HTTP 长轮询（无 WS）、均不下载媒体；未配置=不监听）；
  Outbox 在无驱动时不再返回假 `sent`，而是返回错误让投递问题可见（sender.go）。
- 运维：`status/check/setup/login/backup/archive/stop`、`-headless` 真服务、slog JSON 日志按天轮转 14 天、
  进程内 supervisor（60s、health 表、卡住 Assignment）、state dir 单实例锁（早于恢复）、systemd/launchd 样例、
  `scripts/watchdog-check.sh`（006 小时巡检语义；`stop` 写 `STOPPED` 标记）、schema v3（v3 表折入有序迁移）。
- 提示词热更新：研判提示每次 Eval 重读设置（`cmd/zeokeel/assess.go` 的 `hotAssessor`）；Worker 提示下一次运行生效。

**仍未完成（RELEASE.md F 组）**：Source 实例/规则/admission/accept mode 的设置 UI；系统钥匙串；
`.dmg`/deb/PKGBUILD/NSIS/自动更新；`domain.Source`/`domain.Thread`/`domain.UserError`；`steer/inject`；
飞书 WS 与 IM 媒体下载；开机自启/休眠唤醒（委托 systemd/launchd）。
**测试状态（最终冻结树）**：非 race 2× 全绿、`-race` 全绿（含 `cmd/zeokeel` 5.2s / `internal/runtime` 25.7s）。
此前 20:47 的三个 runtime 红与 cmd 的 `TestEndToEndFromChatToReply` 偶发 flake 在冻结后未再复现。

## [1.0.0] — 未发布

发布清单见 [RELEASE.md](RELEASE.md)。1.0.0 的定义（**目标，当前尚未全部满足**；缺口见 RELEASE.md F 组）：

- 三平台安装包可从 CI 产出并安装（当前 CI 只交叉编译 + 查体积，未出包）
- 一条请求能从本地面板/GitHub 走到交付物；**IM 入口待接线**
- 崩溃恢复覆盖运行中的 Assignment（Wave 2 已实现：`RecoverAssignments` + patchtoolcalls）
- 零 cgo 在全链路成立（已成立）
- 首次运行向导可完成并落库（Wave 2 已实现）

### 已实现（截至本版）

**领域与执行**
- 领域状态机：Trigger / Thread / Decision / Ticket / Mandate / Assignment / Subscription / Attention
- Ticket 阶段由事实推导（`StageOf`），不存状态字段
- 内容派生 ID，workpace 前缀隔离，重放安全
- 两种来源：自身账号（无条件执行）与同事（待批单 + 四要素）
- 应答策略：direct / operator / both / none，含 reply mode

**Agent 运行时**
- eino ADK 内核：`adk.Runner` + 官方 `session.FileStore` 事件落盘
- 崩溃/中断恢复：启动时 `RecoverAssignments`（note Input + 新 Assignment + 唤醒）；悬空 tool_call 由 eino `patchtoolcalls` 修补。`Runner.Resume` 已删除，恢复路径唯一
- 上下文压缩（官方 `summarization`）、工具结果落盘（官方 `reduction`）
- 工具集：eino `filesystem` 中间件提供 read/write/edit/glob/grep（doublestar）、bash（eino backgroundtask 转后台作业）、工作区路径拒绝；沙箱不是安全边界（文档已声明）
- 用量统计、取消、流式文本
- 工具调用检查器：Wave 2 起由官方 `adk/middlewares/patchtoolcalls` 承担；旧自研 `engine_react.go`/`bufferToolCallChecker` 已删除（不再存在死代码路径）

**可靠性**
- 单写者 SQLite（schema **v3**，v3 表折入有序迁移），事务 + 嵌套 SAVEPOINT + afterCommit
- durable Effects 队列：限流、退避、`Later`、重试上限
- 幂等 Outbox：回执永久保留、`sent`/`gone`、同会话保序
- 崩溃恢复（Wave 2 完成）：重启后状态/Effects/Outbox 都在；运行中的 Assignment 由 `RecoverAssignments` 恢复；GitHub/定时/IM 入口统一走 `Runtime.ReceiveTrigger`（→ `Persistence.Receive`），游标落 kv

**面板**
- 原生 UI（mygo，无 webview）：总览 / 消息 / 来源 / 触发 / 评估 / 执行 / 设置
- 总览是待办（待你批准 + 需要我处理），消息是只读时间线
- 常驻派活输入条；工作项动作只在总览，配置动作只在设置
- 无头 `ui.NewTester` 测试覆盖每页交互

**接入与扩展**
- GitHub Source：gh CLI 封装、Trigger 映射、游标只在全部成功后推进、幂等评论 marker
- IM 驱动：飞书与钉钉（长轮询、@ 边界、机器人判定、群/单聊/话题、幂等发送、退避重试）——**已接线**：
  `cmd/zeokeel/imwiring.go` import 驱动包、`startConfiguredIM` 在 GUI/headless 都启动、flag 与设置（`source.feishu_*`/`source.dingtalk_*`）均可配置；
  未配置不监听（无“在线”假象），WebSocket 长连接未实现（HTTP 长轮询兜底），真实凭据 E2E 未跑
- MCP 客户端：stdio 与 streamable HTTP，工具名 `mcp__<server>__<tool>`，坏 server 只让自己工具消失
- Skill 库：`SKILL.md` 加载、确定性匹配、注入 Worker 提示词（16KB 预算、UTF-8 安全截断）
- 定时任务：at/every/daily/weekly/cron，DST 安全，不重叠（跳过并记原因），重启不重复触发
- 目标：轮次上限、同一阻塞条件连续 N 轮才判 blocked

**仓库清理**
- 移除误入库的 `seedstate` 二进制（11MB）与根目录 `zeokeel`（44MB，本就该被忽略）
- 删除 `dist/`（254MB）与 spike 里的编译产物（约 190MB）：都是可再生的构建输出
- `design/prototype/` 的 16 张界面截图（00-flow … 15-task-inbox）与 contact-sheet（8.8MB）不再入库，用 `go run . -out shots` 重新生成
- 删除 5 个未被调用的死函数；`.gitignore` 去重并补上 `/seedstate`
- 保留：设计文档、研究笔记、spike 源码（引用证据）、6 张最新实机截图
- 仓库体积 498MB → 29MB；跟踪内容 2.6MB

**工作区自理：200 字近况 + 内容大纲**
- 新增 `internal/brief.Digest`：由近 14 天工单、交付物、目录大纲、最近活动推导 ≤200 字近况，实时推导不落库
- 新增 `internal/scan`：只读扫描工作区，产出远端/分支/语言/构建测试命令/目录大纲/README 标题/最近活动
- 启动时自动扫描并在 Router 的候选简报里带上「近况」行；机器观察与人写的字段互不覆盖
- Router 不能创建工作区（有意为之），但会在 `ask` 里说明缺哪类工作区、该负责什么、哪些关键词该路由给它
- 修复两个因此暴露的问题：`routeLocked` 重入加锁导致的首个 Dispatch 死锁；上下文预算小于系统提示时压缩反复触发（现在预算有 4000 下限，Agent 步数有默认上限）

**大模型缓存可观测且已利用**
- 采集并显示 provider 的缓存命中（`PromptTokenDetails.CachedTokens`）：消息页逐轮显示「缓存命中 99%」，总览显示加权命中率
- 结构性测试守护缓存前提：system 逐字节稳定、工具列表顺序稳定、历史只追加
- 真实端点实测：5 次调用 11559 prompt tokens，命中 9341（81%；首轮为 0%，之后 94–99%）
- 修复：工具参数的中文以 `\uXXXX` 转义显示在面板（`jsonField` 改为真正解码 JSON）

**提示词整体重写（对照 dsh 评估）**
- 新增 `internal/prompts`：有序 section 组装 + `{{variable}}` 插值，替代散落各包的单体字符串
- Worker 提示词分九节：身份 / 工作区（明确"工作目录已是根"）/ 工具纪律 / 验证 / **完成契约** / **升级求助** / 长任务 / 安全 / 语言
- Router 提示词分六节，accept 五要素与原因码表成为硬约束
- 子智能体有独立契约与输出格式（≤150 词、结论先行、带路径证据）
- **补上缺失的 `ask_user_question` 工具**：之前执行器无法正式提问，真实模型只能把问题写在正文里
- 工具描述按 dsh 水准重写：何时用、语义、失败如何表现（非零退出是结果、越界是观察）
- 提示词编辑器保留 `{{workspace}}` 占位符，运行时展开；未知变量保持可见
- 修复两处"假承诺"：评估规则说明之前从未进入研判提示；技能块声称可用 `load_skill` 加载但工具不存在（注意：研判提示的规则说明仍在**进程启动时**读取，改完保存对当前进程不生效）
- 真实模型验收：研判 12.6→8.8 秒，执行 24.6→12.6 秒
- 评估与设计细节见 [design/prompts.md](design/prompts.md)

**真实模型验收（qwen3.8-flash-next · 1M 上下文）**
- 研判：真实模型读消息、在候选工作区里选对、产出可校验的 Decision（12–16 秒）
- 执行：真实模型调 `write`/`bash` 创建文件、读回验证、报告 ready、回写原会话（13–25 秒）
- 期间修掉四个真实 bug：标题在 `.` 处截断导致 Worker 只收到半个指令；Worker 不知道"已在工作区内"；工具级拒绝被当成任务失败；非 demo 模式也塞示例数据

**真实模型跑出来的三个问题**
- Worker 提示词现在明确「工作目录就是工作区根目录」——真实模型曾把 "webapp 工作区" 理解成在自己的工作区里再建一个 `webapp/` 目录
- shell 的临时文件放到工作区内（`.tmp`），不再堆在共享 `/tmp`
- 明示限制：shell 不是安全边界，同机多工单之间没有文件隔离

**长任务（数小时）**
- 面板记录有上限（每单 2000 条），更早的折叠成一行说明——十小时的任务不会把内存吃光
- 工具输出在面板只留预览（4KB，UTF-8 安全截断），完整内容早已落盘
- 正在执行的工单显示「已运行 3 小时 12 分」，不再是"刚刚"
- 模型单次请求超时默认 30 分钟并可配置（`execution.model_timeout`）；之前的 120 秒会把慢模型判成坏模型
- 路径越界等工具级问题改成"观察"返回给模型（它可以自己改路径重试），只有基础设施故障才中止任务

**真实模型验收就绪**
- 新增 live 测试（`ZEOKEEL_LIVE_*` 环境变量门控，默认跳过）：研判选对工作区、执行器真跑工具、交付物落盘、回执发出
- 凭据只走环境变量，不进仓库

**启动失败也要说清楚**
- 执行器连构造都失败（状态目录不可写）时，工单标失败、回执请求方，不再永远停在"排队中"
- 总览待办行显示**失败原因**而不是只有"失败，需要你处理"（原因截断到一行）

**长任务真的会压缩**
- 端到端验证：200KB 工具输出落盘、模型只收到 `<persisted-output>` 指针、面板出现"上下文已压缩"
- 之前只有 agent 包单测，没人验证过这条链路在真实运行里成立

**关窗口不会打断执行**
- 退出时先在后台等执行器收尾（最多 10 秒），状态栏显示"正在等执行器收尾…"
- 等待结束再在 UI 线程真正退出；没等完的部分由 durable 队列下次启动接着跑
- 无 drain 配置时（测试/无头）立即退出

**多窗口不再互相覆盖**
- 设置版本号落到数据库，跨窗口的乐观并发才真的成立（之前是进程内计数，第二个窗口永远以为自己是新的）
- 冲突在**保存事务内**比对，第二个窗口拿到"已被其他窗口改过，请重新加载"
- 执行器协程可在关闭/测试时等待（`WaitIdle`），修掉测试里"临时目录先于 worker 消失"的抖动

**故障时也是诚实的**
- 模型调用失败：工单标失败、面板显示可读原因、**并回消息告诉请求人**（之前默默变红，请求人永远等不到结果）
- 失败摘要不再暴露 eino 的 `node path` 堆栈，只保留状态码与原因
- 数据库不可用时保存报错而不是假装成功，窗口照常刷新

**交付物可翻全**
- 总览「全部」不再是点不动的标签：打开完整交付物面板
- 面板按工作区筛选，每行显示来源工单、路径、大小与时间
- 空态明确说明"工单产出后会出现在这里"

**用得多少看得见**
- 总览运行卡片显示「N 次调用 · X tokens（本次运行）」，随每次模型调用累加
- 明确标注"本次运行"：计数在内存里，重启会重新开始，不假装是全量账单

**谁可以让它干活，现在能管了**
- 修复：首次向导收集的允许名单从未写进授权策略，走完向导后同事消息全被拒
- 名单格式改为「来源:账号」（`dingtalk:ding_zhangsan, github:lin`），写错即在向导里报错
- 设置页新增「允许名单」分组：改完保存立即生效，可随时撤销

**总览数字不再是常量**
- 总览 Eval 计数不再恒为 0（注意：仍是**全量**计数，不是 24h 窗口；执行中/今日数字仍恒 0，Wave 2 修）
- 健康卡片新增「模型」：未配置时显示异常并说明"工单会挂起"
- 「存储与保留」显示真实数据库大小与会话事件条数；未知时显示 `—` 而不是 `0`

**来源页说真话**
- 多个来源各自独立上报，一次轮询不再把别的来源从页面上清掉
- 关闭来源会真的从页面移除，不留"上次轮询正常"的残影
- 游标是驱动的内部 token，页面改成"已推进/尚未开始"，不再把 `owner/name|时间` 甩给用户

**判定原因可读**
- 评估页把 reason 码渲染成中文（之前把码原样当句子，出现两个 "said" 并排）
- 13 个已知码都有文案；未知码显式显示"这个判定码还没有文案"，不猜
- 研判提示词限定原因码取值，减少面板显示不出来的码

**状态永远说真话**
- 工单现在会记录"执行器接手"这一事实，面板能真正显示"执行中"（之前整个运行期间都显示排队中）
- 工单会记录执行器接手（Assignment），面板能显示"执行中"；但总览执行器卡片仍是硬编码上限（2）且 `Running` 未投影，`0/2` 仍然失真（AUDIT §0.2-10，Wave 2 修）
- 一张表把 7 种工单状态的「阶段 / 执行页桶 / 总览是否待办」钉死，防止三处各说各话

**设置真的生效**
- 提示词编辑器改动会进入执行器（下一次运行生效）与研判器（**重启后**生效；无热更新/版本历史，Wave 2）
- 「并发执行器」「命令超时」「轮询间隔」真正改变行为
- 删掉存了没人用的假开关（GitHub Token、丢弃机器人、预算、自动继续），页面只列生效项

**首次运行真的能用**
- 向导填的模型现在会进入执行器与 Eval（之前只存进设置，没人读）
- 启动时命令行优先，其次用设置里保存的模型
- 没配模型时工单**明确挂起并说明原因**，不是"看起来在跑"

**验收**
- RELEASE.md A 组每条都对应一个可执行场景（`cmd/zeokeel/acceptance_test.go`）
- 待批单的「敏感点」现在总是说明"为什么需要你批准"（之前普通同事请求这里是空的）
- 两个工单并发执行互不干扰（各自工作树）

**配置**
- 设置与提示词落进 SQLite（kv 表），重启后还在；没有数据库时页面明说"内存"
- 应用图标由 `tools/genicon` 生成，随包分发：macOS `.icns`、Windows `.ico`、Linux `.png`

**能力可见**
- 触发页新增「定时」分区（下次触发、上次结果、启用状态）与「目标」卡片；总览出现目标条
- 定时任务现在会保存任务描述（之前只存 id），面板显示"这个定时是干什么的"
- 定时规格有中文可读标签（每天 09:30 / 每 2 小时 / cron …）

**能力对 agent 可见**
- `schedule_task`：把「每天 09:30」「每 2 小时」「cron …」变成真实定时任务；看不懂的时间直接拒绝，不猜
- `set_goal`：记录跨轮次的长期目标
- 能力未启用时工具不出现——不告诉 agent 一扇打不开的门

**workflow 编排**
- `internal/workflow`：步骤是数据、handler 启动时注册，每步落库，中断后可恢复（当前只有 `-pipeline` 在用它；agent 工具侧没有 workflow 编排）
- 步骤级重试（`ErrRetry`）、阻塞（`ErrBlocked`）、`SkipIf`
- SQLite 存储；`-pipeline FILE` 可无窗口跑批量任务（失败即非零退出，适合 cron）

**执行器**
- 子智能体：`explorer`（只读调查）与 `verifier`（只跑命令验证），能力差异由工具白名单强制，不用提示词约束
- 默认开启，`-no-subagents` 关闭

**首次运行**
- 全新安装不再打开空仪表盘，而是四步向导：模型 / 工作区 / 来源（可选）/ 允许名单（可选）
- 必填项没完成时「开始使用」不出现；保存工作区即刻注册，无需重启

**交付闭环**
- 交付物（文件/报告/PR/链接）与报告状态；运行结束自动收集改动过的文件
- 回执按来源会话组织，经幂等 outbox 发出，崩溃重试不重复
- 提示词编辑器（Router/Worker/评估规则三层，乐观并发、冲突重载）
- 跨区搜索（⌘K）：工单/消息/决策/工作区/来源五类

**工程**
- 全链路零 cgo，六平台交叉编译
- `check-nocgo.sh` 四步守卫（含负例验证）
- 三平台打包：Linux tar.gz（含 .desktop）、macOS .app zip（未签名）、Windows zip + install.ps1（无 MSI/NSIS；.dmg/deb/PKGBUILD/自动更新未实现）
- 470+ 个测试（`CGO_ENABLED=0 go test ./... -skip Live`），`modernc.org/sqlite` + `mygo` + `eino` 均为纯 Go
