# Changelog

本文件记录用户可见的变化。版本号遵循语义化版本；1.0.0 之前的 `-rc.N` / `-dev` 后缀表示
发布清单（RELEASE.md）尚未全部通过——`1.0.0-rc.N` 不表示功能完成。

## [1.0.0-rc.4] — 发布候选（2026-11-04）

rc.4 去掉历史包袱：旧式审批/提案机制彻底删除，补上浏览器 MCP 前置检查与 Worker `read_resource`。
`VERSION` 由发布负责人 bump 为 `1.0.0-rc.4`；发布说明见 [RELEASE-NOTES-v1.0.0-rc.4.md](RELEASE-NOTES-v1.0.0-rc.4.md)。
不预称闸门全绿，由 Lead bump 后重跑。

- **旧式审批/提案机制彻底移除**：Decision 只有 `accept`/`stop`/`ignore`/`unavailable`；删除
  Approve/Reject/UndoReject/proposal/`RequireApproval` 及所有审批 UI/文案；旧 DB 的 `propose` 行载入为 `ignore`
  并记 `legacyImport`，旧 `requireApproval` 被忽略；收件箱改为 **attention-only**（任务问题/受阻/失败）。
  （`internal/domain/ticket.go:10-13`、`internal/engine/persist.go:156-162`、`internal/engine/gate.go:52-56`、
  `internal/ui/inbox.go:1-140`）
- **浏览器 MCP 前置检查**：`browser.Prerequisites` 检测 node/npx 与版本；缺失时设置/项目页显示醒目警告、
  当前 OS 安装命令（brew/winget/apt 等）、nodejs.org 链接、「重新检查」；「启动浏览器」禁用并说明原因；
  不自动安装、不假装运行（`internal/browser/prereq.go:1-140`、`internal/runtime/rc3.go:170-205`、
  `internal/ui/workspacepanel.go:613-692`、`internal/browser/manager.go:189-197`）。
- **`read_resource`（DSH `orchestrator_read` 等价）**：分页读自己的 ticket/inputs/trigger/thread（只读、redacted、
  offset/limit 默认 20/上限 200）；Worker prompt 已列出；SuperAgent 仍无工具
  （`internal/runtime/rc3.go:17-160`、`internal/runtime/artifacts.go:205-214`、`internal/runtime/worker.go:580`、
  `internal/runtime/rc2.go:121-132`）。
- **工具/状态**：`read_image`（元数据-only，无像素）、`job_list`、`subagent` 命名、Worker 域工具
  `ack/handback/thread_unrelated/request_status`、per-project `mcp__browser__*`。

**rc.4 诚实限制**：真正在原项目执行未实现（执行副本 + apply-back）；系统钥匙串未实现（SQLite 0600）；多窗口未实现；
自动更新只检查不下载；真实飞书/DWS 租户 E2E 未验证；`read_image` 无像素（需 vision endpoint）；
打包仍只有 tar/zip（无 `.dmg`/deb/安装器）。

## [1.0.0-rc.3] — 发布候选（2026-11-04）

rc.3 对齐 DSH 主链，并把项目级配置/浏览器/工具补齐。`VERSION` 由发布负责人 bump 为 `1.0.0-rc.3`；
发布说明见 [RELEASE-NOTES-v1.0.0-rc.3.md](RELEASE-NOTES-v1.0.0-rc.3.md)。**不预称闸门全绿**，由 Lead bump 后重跑。

- **DSH 对齐（行为变更）**：admitted 的明确 IM/GitHub 请求由 Eval 直接接单 → Ticket，移除“每条消息先审批”的 release policy；
  推断请求按 accept mode `off`/`observe`/`auto`（auto 需匹配规则）；`DecPropose` 只在 Policy 显式 opt-in
  `RequireApproval` 且请求为推断时产生；旧式 proposal 在收件箱标「旧式请求（需要批准）」；`AutoApproveIM` 移除
  （`internal/engine/evaluate.go:348-390`、`internal/engine/gate.go:52-56`、`internal/ui/inbox.go:106`、`internal/ui/overview.go:109`）。
- **项目面板**：项目页 概览/设置/统计；per-workspace 浏览器设置（Chrome/Edge、无头、CDP 端口 0=自动、profile 只读）、
  启停/状态、技能开关（全局默认开/项目可关）、统计（会话/任务/进行中/等待/完成/失败/最近活动[无投影时空态]/工具可用性）
  （`internal/view/workspacepanel.go:1-99`、`internal/ui/workspacepanel.go:193-520`、`internal/runtime/workspacesettings.go:75-160`、
  `internal/store/workspacesettings.go`）。
- **浏览器 + MCP**：默认路径检测 Chrome/Edge；每项目独立 profile `<stateDir>/workspaces/<ws>/browser-profile`、
  唯一 CDP 端口、`--remote-debugging-port/--user-data-dir/--no-first-run/--headless=new`；`npx -y chrome-devtools-mcp@latest
  --browserUrl http://127.0.0.1:<port>`；`mcp__browser__*` 只注入该项目 Worker；browser env scrub；npx/浏览器缺失可操作错误
  （`internal/browser/detect.go:10-65`、`internal/browser/manager.go:99-107,185-245`、`internal/browser/launch.go:22-56`、`internal/browser/mcp.go:12-15,44-48,118-140`、
  `internal/runtime/worker.go:468`、`internal/runtime/workspacesettings.go:427-480`）。
- **工具**：`read_image`（仅元数据/尺寸/格式；模型端点 text-only，不附像素；10MB/路径约束/拒绝 observation）、
  `job_list`（job_output/kill/list 三件套）、`subagent` 命名与 DSH 对齐；Worker 域工具
  `ack/handback/thread_unrelated/request_status`；SuperAgent 聊天**不带工具**（非 orchestrator）；仍缺
  `orchestrator_read` 与 DSH main orchestrator 工具集（主 Session 无 tool loop）（`internal/agent/imageread.go:1-120`、
  `internal/agent/jobs.go:275-309`、`internal/agent/subagent.go:55`、`internal/runtime/artifacts.go:121-222`、
  `internal/runtime/rc2.go:121-132`、`audit/tool-gap.md`）。

**rc.3 诚实限制**：真正在原项目执行未实现（执行副本 + apply-back）；系统钥匙串未实现（SQLite 0600）；多窗口未实现；
自动更新只检查不下载；真实飞书/DWS 租户 E2E 未验证；`read_image` 无视觉；无 `orchestrator_read`；
浏览器需要 Chrome/Edge + node/npx；打包仍只有 tar/zip（无 `.dmg`/deb/安装器）。

## [1.0.0-rc.2] — 发布候选（2026-11-04）

rc.2 在修复波 + UI/UX 优化轮之上补齐交互审计剩余项。`VERSION` 由发布负责人 bump 为 `1.0.0-rc.2`；
发布说明见 [RELEASE-NOTES-v1.0.0-rc.2.md](RELEASE-NOTES-v1.0.0-rc.2.md)。**仍不预称闸门全绿**，由 Lead 在 bump 后重跑。

- **聊天流式输出 + 停止生成**：OpenAI 兼容 SSE 客户端；增量文本 60ms 节流发布，打字光标；`停止生成` 取消
  并保留已生成部分（标「已停止生成」）；最终文本只追加一次，不重复（`internal/runtime/rc2.go:24-190`、`internal/ui/composer.go:241-305`）。
- **Markdown 渲染**：标题/粗体/斜体/行内代码/围栏代码块/有序/无序列表/http(s) 链接；不解释 HTML；
  链接用系统浏览器打开（`internal/ui/markdown.go:19-180`、`internal/ui/chat.go:75`）。
- **计划/待办可见**：`.zeokeel/plan/*.json` → `view.PlanVM`；执行详情与对话任务卡显示步骤/状态/计数
  （`internal/view/plan.go:1-51`、`internal/runtime/rc2.go:302-433`、`internal/ui/planpanel.go:29-85`、`internal/ui/ticketdetail.go:97-98`）。
- **应用到项目**：只复制记录过的交付物；拒绝绝对/越界路径；同名文件先备份 `.bak-<timestamp>`；返回相对路径；
  UI 确认列出目标/文件/覆盖警告（`internal/runtime/rc2.go:193-270`、`internal/ui/applyproject.go:1-199`）。
- **撤销拒绝**：拒绝无损（保留 proposal + `RejectedAt`）；10s 内可恢复待批（幂等；同一 Thread 有更新决策则拒绝；
  重启安全）；不建 Ticket/不启动/不回复（`internal/engine/lifecycle.go`、`internal/engine/persist.go`、
  `internal/runtime/rc2.go`、`internal/ui/inbox.go:59-66`）。
- **原生应用菜单**：App/文件/编辑/窗口；新建对话 ⌘N、设置 ⌘,、打开主窗口、标准编辑 roles
  （`internal/ui/menu.go:1-72`、`internal/ui/app.go:345`）。
- **系统无障碍偏好**：TextScale×`ui.font_scale`、ReduceMotion、HighContrast、系统 Accent
  （`internal/ui/prefs.go:1-61`、`internal/ui/app.go:441-462`、`internal/ui/stream.go:59`）。
- **关于/更新检查**：设置›关于显示版本/许可证；「检查更新」只查询 GitHub Releases API（不下载），
  显示最新版本/发布页（`internal/ui/about.go:1-117`、`internal/runtime/rc2.go:355-415`）。

**rc.2 诚实限制**：真正在原项目执行未实现（执行副本 + 显式 apply-back）；系统钥匙串未实现（密钥 SQLite 0600）；
多窗口未实现；自动更新只检查不下载；真实飞书/DWS 租户 E2E 未验证；打包仍只有 tar/zip（无 `.dmg`/deb/安装器）。

## [1.0.0-rc.1] — 发布候选（2026-11-03，Wave 2 后）

**候选版不等于功能完成。** Wave 2 + 2026-11-04 修复波把领域/引擎/运行时/UI/IM/工具的主要缺口做完了：
IM 改为审批优先、飞书接入官方长连接、钉钉 DWS 按真实 CLI 契约重写、UI 命令异步化、超级Agent 聊天+派活、
Ticket/工作区身份与设置/secret 收口。全量机械闸门由 Lead 在合并前统一跑（本文不预称全绿）。
仍未完成的是产品面：**打包只有 tar/zip（无 .dmg/deb/安装器）**、**系统钥匙串未做（API Key 明文 SQLite 0600）**、
**设置页规则/admission/Source 实例不可编辑**、**飞书 webhook/IM 媒体下载**、`steer/inject` 未做；
Round 2/3 已补侧栏/工作区/IM 渠道管理/内置工具，2026-11-04 修复波补上 IM 审批优先、飞书 WS、DWS 真实契约、异步命令边界、聊天双态、身份/迁移/secret/工具安全；打包与钥匙串仍未做。
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
- IM：飞书/钉钉**已接线**（flag 或设置›来源可配）；2026-11-04 修复波改为**审批优先**、飞书官方长连接（longpoll fallback）、
  钉钉 DWS 真实契约（Stream 为 bot 模式可选）；均不下载媒体，未启用=不监听；Outbox 无驱动时返回错误而非假 `sent`。
- 运维：`status/check/setup/login/backup/archive/stop`、`-headless` 真服务、slog JSON 日志按天轮转 14 天、
  进程内 supervisor（60s、health 表、卡住 Assignment）、state dir 单实例锁（早于恢复）、systemd/launchd 样例、
  `scripts/watchdog-check.sh`（006 小时巡检语义；`stop` 写 `STOPPED` 标记）、schema v3（v3 表折入有序迁移）。
- 提示词热更新：研判提示每次 Eval 重读设置（`cmd/zeokeel/assess.go` 的 `hotAssessor`）；Worker 提示下一次运行生效。

### Round 2/3 + 修复波（2026-11-04，同一 rc.1，不新增 tag）

- **IM 审批优先（rc.1 当时的行为；rc.3 已推翻，见上）**：飞书/钉钉的每条消息（包括操作员自己的）先变成**恰好一个**待批准项
  （`DecPropose`，不建 Ticket/不启动 Worker/不自动回复）；Approve 才创建并唤醒，Reject 什么都不留；
  来源页与 health 显示「待审批 N」；重复投递只产生一个待批项。
  验收 A3 改为“IM 消息也要批准”，A7 先审批后建单（`cmd/zeokeel/acceptance_test.go:129-160,207-232`；
  `internal/engine/evaluate.go:396-461`）。
- **飞书接收**：接入官方 `larksuite/oapi-sdk-go/v3 v3.12.2` 的 `larkws` 长连接：**新装默认 transport=ws**；
  旧凭据-only 配置升级时 pin `longpoll`（保持原接收路径）；HTTP 长轮询是显式 fallback（cursor），面板 Options 只有 ws/longpoll；凭据检查仍走 tenant token（401/403 给 `open.feishu.cn/app`）。
  媒体仍只保留引用，webhook 未实现。
- **钉钉 DWS 重写为真实契约**：默认 transport 是 DWS（操作员个人 OAuth，不是 bot/app；DWS 不使用 app-key/secret）。
  用长驻 `dws event +listen-im … -f ndjson` 接收（**无 poll/cursor**，event id 去重，断线 1s→30s 重连）；
  `dws chat +messages-send --as user …` 发送（无 `--idempotency-key`，隐藏 marker 去重）；
  `dws auth status --format json` 检查、`dws auth login [--device]` 登录；短命令串行、30s 超时、stderr 脱敏。
  官方 Stream SDK 保留为 `dingtalk_transport=stream` 的 bot 模式。
- **IM 生命周期/配置**：三态启用（显式 1/0 优先，缺省按凭据/flag 推导；flag-only 部署由 `BootstrapIMFlags` 补写）；
  bool 编码读兼容 `true/false/1/0`、写统一 `1/0`；每渠道 generation + 调用串行化，
  停止 cancel→Close driver→注销 outbox sender，切换 transport 清 cursor；`CheckChannel` 复用运行中的 DWS 后端。
  设置键新增 transport/self/profile/listen-kind/chat-query 等；secret 不回显。
- **异步命令边界**：Dispatch/CreateWorkspace/NewConversation/SaveSettings 从 UI 帧移出到后台命令锁，
  面板显示「发送中…/保存中…/连接中」；错误不再默认标红。
- **超级Agent 聊天 + 派活双态**：全局输入提问得到模型回答（未配置模型时确定性回答并说明），
  派活回「已派发 #N」，待批回「需要你确认」；最近回答内联在输入框上方（所有页面）与总览对话（最多 8 条，
  历史最多 20 条并持久化）；非失败不标红；回答不再只落在「消息」页。
- **Ticket 身份**：执行列表行/详情查找键与详情 VM 的 ID 都用 domain ID，`Number` 单独显示 `#number`；工作区会话点击同一 Ticket。
- **工作区**：opaque `ws_*` ID（重命名不改历史）、绝对路径/重复 root 拒绝、重命名已接线、删除空工作区（有工单拒绝）；
  工作区过滤作用于「消息/执行」（顶部「工作区 X / 全部」）。
- **设置/安全**：UI 开关读写 canonical `1/0`（读兼容旧 true/false）；`LoadSettings` 启动时一次性迁移 bool、旧 IM 启用/transport、
  `im_self` 拆分并写 schema stamp；SaveSettings 改为“构造快照→事务提交→再换内存”并在失败时不改内存；
  设置 VM/Inspect/status/health detail 统一 redaction（`***`）；worker 执行侧仍剥离凭证变量。
- **工具**：plantask 工具名与注册一致（TaskCreate/Get/Update/List），任务 JSON 移到 `.zeokeel/plan`；
  AGENTS.md 改为不可信仓库内容信封（≤64KB）；web_fetch 用 DNS 解析后 pin IP、`Proxy:nil`、拒绝私网、
  返回 `<untrusted-web-content>` 信封；设置›执行新增 `plan_tasks/agents_md/web_tools` 开关（默认开）。
- **界面**：字号从 magic +2 改为 `ui.font_scale`（设置›部署可编辑，90/100/110%）经 `fs()` 统一缩放；启动窗口最小 1100×720；
  OS/arch·时区仍在 footer 与设置›部署；保存条按组写真实生效方式（来源=重启渠道几秒/执行=下次工单/提示词=热更新/部署=重启后生效）。

### UI/UX 优化轮（2026-11-04，同一 rc.1，不新增 tag）

按交互审计（novice/journeys）把日常路径从“工程可用”改为“运营可用”：

- **导航/IA**：日常只留 超级Agent / 收件箱 / 项目 / 执行 / 设置；工程页（对话记录、消息、连接、触发、评估）收进
  默认折叠的「诊断（高级）」组；UI 用「项目」称呼原工作区（`internal/view/vm.go:486-520`、`internal/ui/chrome.go:60-110`）。
- **模型设置**：设置›模型（端点/API Key/模型名）+「测试连接」探测已保存配置（401/403/404 分类提示），保存后即时生效
  （`runtime/ux.go:36-125`、`runtime/pages.go:543-558`、`ui/settings.go:501-535`）；未配模型时总览「去配置模型」，
  任务停在待你决定（`ui/overview.go:76-95`）。模型响应超时在设置›执行。
- **首跑/onboarding**：必填/可选标记、模型步骤「保存并测试」、第 1 步可「稍后设置（演示模式）」跳过、
  项目根目录原生选择器；完成后给落点提示（`ui/onboarding.go:60-180`、`ui/uxsupport.go:100-170`、`runtime/ux.go:445-452`）。
- **输入条**：多行 TextArea，Enter 换行、⌘/Ctrl+Enter 发送（IME 安全）；target chip 明确「聊天/项目（派活）」，
  提交到所选项目；失败保留草稿+「再次发送」；回答内联可展开/复制/「查看全部」到对话记录
  （`ui/composer.go:19-220`、`ui/commands.go:137-188`、`runtime/ux.go:134-172`）。
- **收件箱**：统一「待你决定 N」（runtime `AttentionCount` 单源），批准/拒绝卡片 + 任务回答/重试/停止，
  `j/k`/`Enter`，pending 与就地错误，窗口隐藏时新待办发系统通知（`ui/inbox.go:1-360`、`runtime/ux.go:279-440`、
  `view/attention.go`、`ui/uxsupport.go:286-300`）。
- **执行页动作**：行内「回答/重试/停止」+ pending/错误，不再要求去总览；`#N` 标签
  （`ui/execution.go:150-210`、`view/ticket.go:69-88`、`runtime/ux.go:231-260`）。
- **路径真相**：执行详情分开「项目目录 / 执行副本」，可打开/复制；交付物可打开/复制；提示词明确“只在副本工作、
  不声称修改项目”（`ui/ticketdetail.go:257-290`、`runtime/ux.go:279-303`、`runtime/worker.go:570-582`）。
- **IM 连接向导**：按 transport 只显示该方式字段；先校验、用填写值测试（不保存），通过才保存并启用，或显式“仍然保存”；
  DWS 显示版本/可复制登录命令/授权链接，飞书 401/403 给授权页；钉钉「发送时暂停监听」；飞书 chats：ws 可选、longpoll 必填
  （`ui/channelwizard.go:1-520`、`runtime/ux_im.go`、`runtime/im.go:1392-1505`、`im/dingtalk/dws_setup.go`、`im/feishu/ws_chats.go`）。
- **视觉/可访问性**：对比度 token 提升、focus token；行用 `Focusable/Role/Label/Tooltip`；28px 目标/16px 图标；
  状态栏只留“上下文/帮助/待你决定”，移除版本/rev/Go/SQLite；`StateKey:"main"` 记忆窗口、最小 1100×720；
  ⌘N 新会话、⌘, 设置、⌘K/⌘/ 保留（`ui/tokens.go:100-145`、`ui/chrome.go:143-160`、`ui/app.go:320-330,441-455`）。

**仍未完成（RELEASE.md F 组）**：Source 实例/规则/admission/accept mode 的设置 UI；系统钥匙串；
`.dmg`/deb/PKGBUILD/NSIS/自动更新；`domain.Source`/`domain.Thread`/`domain.UserError`；`steer/inject`；
飞书 webhook 与 IM 媒体下载；mygo 原生 app 菜单、系统 TextScale/ReduceMotion/高对比、多窗口、自动更新；
计划/任务在面板的可视化、真正在原项目执行（当前为执行副本）；开机自启/休眠唤醒（委托 systemd/launchd）。
**测试状态**：2026-11-03 的冻结树曾非 race 2× / `-race` 全绿；之后又合入修复波与 UI/UX 优化轮（并发/IM/UI 大量改动），
**不能拿旧快照当本轮证据**。全量闸门由 Lead 在 push 前重跑 `sh scripts/verify-fixes.sh` 与 `sh scripts/release-check.sh`，本文不预称全绿。

## [1.0.0] — 未发布

发布清单见 [RELEASE.md](RELEASE.md)。1.0.0 的定义（**目标，当前尚未全部满足**；缺口见 RELEASE.md F 组）：

- 三平台安装包可从 CI 产出并安装（当前 CI 只交叉编译 + 查体积，未出包）
- 一条请求能从本地面板/GitHub 走到交付物；**IM 入口已接线（DSH 对齐，rc.3）**
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
- 提示词编辑器改动进入执行器（下一次运行生效）与研判器（热更新）；历史/base-hash/Default updated 已接线（每层最近 20 版）
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
