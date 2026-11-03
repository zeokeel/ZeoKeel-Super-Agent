# 被拒绝的概念（Rejected Concepts）

> 设计 §1.5（design:108）承诺维护本文件：**每拒绝一个概念，写在这里，防止设计腐败。**
> 规则：新增概念前先读这里；要推翻某条"拒绝"，必须先在设计文档写 ADR 并更新本表。
> 条目分两类：**A. 原型明确否决的概念**（需求基准，我们照抄）；**B. 本项目在 2026-11-03 审计后主动否决的**。

## A. 原型明确否决的概念（需求基准，_ref/docs）

| 被拒绝的概念 / 做法 | 原型给出的理由（引文） | 我们的落地状态 |
| --- | --- | --- |
| "人"作为一个领域实体 | "A person is not a domain concept. A Trigger carries its actor's identifier as the Source gives it."（001:39） | ✅ 遵守：`domain.Actor` 只是字符串来源身份；允许名单是 Policy 的 Source 部分 |
| 为"等待/定时"新建 Trigger 调度器 | "A condition that needs waiting is an ordinary Source, so Eval never depends back on Sources"（001:50） | 🟡 定时/目标被实现成 `schedule`/`goal` 两个**新实体**（AUDIT §4.3 判为违背"少概念"）——登记为**未支付的语义债**，未来要么并入 Source/Ticket 字段，要么在设计里补 ADR |
| 每个新业务需求都加一个领域概念 | "A business need becomes an attribute of one of them, or a prompt the agent follows. The design does not add a concept for it."（000:19） | 🟡 `Decision.kind=propose`/Proposal、`ReplyPlan`、Schedule、Goal 是四个新增物；AUDIT §4.3 逐条判定 |
| 用设置表达团队行为 | "A default replaces a setting when one value suits almost every team. Behaviour that a team describes in words is prompt text, not a setting."（000:20） | 🟡 新增了 `poll_interval`/`command_timeout`/`model_timeout`/`concurrency`；前两个有默认值合理性，`poll_interval` 与"一个默认值够用"冲突 |
| Executor 自己宣布"人让停" | "A person saying \"stop\" in a Thread is an ordinary Trigger, and Eval decides stop. An Executor cannot declare that a person asked it to stop."（002:143） | ❌ 未实现 stop/followup 链路（Wave 2 契约：`StopTicket` 仍由人/工具发起，模型不得自宣布 stop） |
| 让 Eval 等待/轮询 | "Waiting conditions belong to Sources … Eval never waits."（002:145） | ✅ Eval 是单次 `Assess` 调用，不等待 |
| 把 Subscription 当授权 | "A matching Trigger fans out to every live subscribed Ticket … lends it no authority"（001:37；002:42） | 🟡 fanout 存在且不授权；但投递/退订/交回未实现（Wave 2） |
| 多用户 / 多租户 | 002 全文以单 operator 的授权模型为前提；006 用专用 OS 账号隔离而不是多租户 | ✅ 单机单人（设计 §1.3 明确排除多租户） |
| GitHub Enterprise Server | "GitHub Enterprise Server is not supported."（006:25） | ✅ 未实现（未承诺） |
| 用 GitHub App 当 agent 账号 | "A GitHub App cannot be the agent, because dsh-superagent reads the account's notifications."（006:33） | ✅ 未实现 GitHub App 路径 |
| 为已存在的 profile 重跑初始化 | "Run the profile-creation command only for a new installation … Do not use it for upgrades."（006:87） | N/A（桌面版无 profile；升级走 SQLite 迁移，见 §7.1） |

## B. 本项目主动否决的概念（2026-11-03，含理由与替代）

| 被拒绝 | 理由 | 替代 / 现状 |
| --- | --- | --- |
| Webview / HTML 前端 | 用户选定 mygo 原生控件；也是"单二进制、零 cgo"的前提 | `internal/ui`（design §1.3、ADR-1） |
| 复刻 DSH 宿主（插件市场、preset 市场、open-in-app、Office/PDF 转换、语音、CoT 翻译、产品遥测） | 宿主级工程与产品无关，且授权成本高 | 不做（design §1.3）；`ask_user_question`/`present` 等**能力**保留 |
| 迁移原型的 schema 10→11 历史包袱 | 新库从 v1 开始，没有 legacy 数据 | `internal/store/schema.go`（v1→v2 + v3 旁路） |
| 全量 i18n | 首版中文 + 关键英文；文案码表结构保留 | 未做（design §1.3）；本文件与 README 的"不做"要有 ADR 才能推翻 |
| 每个工作区一个进程 / 每个窗口混多工作区 | 会把 Router 大脑切碎、跨区派发无法原子 | Engine 全局唯一 + `workspace_id` 属性（ADR-14） |
| 用 UUID 替代内容派生 ID | 幂等路径全部建立"同一语义输入 → 同一 ID" | `domain.Key`（design §4.1） |
| 把权限判断交给提示词 / 模型 | "Prompt is not permission"（002:5；design §1.5） | gate + Mandate + tool checks 在代码（虽然 gate 语义仍待 Wave 2 修正） |
| 第二个派活通道（绕过 Decision/Mandate/Ticket 的快捷入口） | 审计、幂等、resume 会分叉 | 只有一条：面板输入 → Trigger → Eval → Ticket（design §6.9） |
| 为 cron 引入 `robfig/cron` | DOM/DOW 语义差异 + DST 更弱；现有 parser 的 bug 应就修 | 修 `N/step` + `time.Date`（AUDIT §3.4、N-12） |
| 用 backoff/retryablehttp 替换 IM 重试 | `Retry-After` 与"发送去重分层"是领域不变量，框架会藏掉 | 保留 `internal/im/retry.go` 的分层（AUDIT §3.4） |
| 把 `design/prototype/` 的代码回迁到 `internal/ui` | 两套 tokens 已漂移（Radius 8 vs 12、hex 重复）；原型是视觉参考 | 原型钉 `mygo v0.7.4` 可渲染，仅参考（`design/prototype/README.md`） |
| 多用户、多副本、Postgres、租约 | 单机桌面隐含单人；设计 §1.3 明确排除 | 单写者 SQLite（ADR-5） |
| 设备休眠补跑 / 离线补偿 / 跨设备状态（官方架构 §5 的一部分） | 产品是单机桌面（ADR-1）；这些需要 daemon/服务端 | **明确放弃**，写入设计 §11"明确未做"；若要 7×24 请等 Wave 2 的无头服务 + 外部 watchdog |
| 用"内存里的假数据"充当面板事实（demo projection 进入生产） | 违反 005 诚实状态；`view/demo*.go` 生产零引用 | 只允许测试/演示使用；生产投影必须来自 runtime（Wave 2 修计数） |
| 在提示词里承诺不存在的能力 | 设计 §1.5"提示词里承诺的能力必须真的存在" | `ask_user_question` 现在能 raise 不能 resolve，属于**已知反例**；Wave 2 修，修好前在 README/CHANGELOG 明说 |

## C. Wave 2 之后新增/变化的概念登记（2026-11-03）

> 规则同前：新概念必须登记；**被删除的实现也要留下"为什么"**，防止以后又被加回来。

| 概念 / 实现 | 判定 | 说明与证据 |
| --- | --- | --- |
| `RequireApproval`（可选审批门） | **接受，登记为偏离** | 原型 001:35 是"Eval 创建 Ticket"；本项目保留 Proposal 作为可选安全阀，默认关闭（`internal/engine/gate.go:52-55`）。它改变 001/002 的默认语义，属于**未完全支付的债**：`auto` 模式 + `RequireApproval=true` 的组合需要文档化 |
| `domain.Eval` 持久队列 + 两阶段 Eval | 接受 | `Receive` 只入队、`EvaluateDue` 批量判定；这是对 001"Eval 调度"的工程化，不新增业务概念（`internal/domain/ticket.go:469`） |
| `archive.db`（归档文件） | 接受 | 原型只有"必须做归档"的要求、没有形态；单机 SQLite 用 ATTACH 到独立文件（`cmd/zeokeel/ops.go:90`）。不是新领域概念，是运维产物 |
| `zeokeel status/check/setup/login/backup/archive` CLI | 接受（EXTRA） | 桌面 app 的运维面；原型用 tmux 脚本 + 管理 API，这里是 CLI + 退出码 0/1/2/3（`cmd/zeokeel/cli.go`） |
| 进程内 supervisor + health 表 | **部分接受** | 替代原型的"外部 watchdog"（006 §6）只实现了一半：能检查/记录/标 error，但**不能像外部控制 Session 那样在线改规则/提示词**；006 的完整形态仍缺（`internal/runtime/supervisor.go`） |
| eino 官方 `filesystem`/`backgroundtask`/`patchtoolcalls`/`reduction`/`summarization`/`subagent` | 接受，删除自研 | 自研 `engine_react.go` 与 `bufferToolCallChecker` 已删除（Wave 2 清理）；以后禁止再写"第二套工具循环" |
| `Runner.Resume` | **已删除（Wave 2 清理）** | 恢复改走 `RecoverAssignments` + `patchtoolcalls`；不再保留"存在但生产没人调用"的死代码（测试注释已注明已删除） |
| `ReplyPlan` 由 `deliverReply` 消费 | **已支付（Wave 2）** | 设计 §4.10 的"默认 notify 不代你说话"有机制保证：notify/both/none → `Held` 只进面板，不直发（`internal/runtime/reply.go:213-264`） |
| Glossary（名词表） | 接受（EXTRA） | 原型 001 的术语表在面板中的等价物；`internal/view/glossary.go`，⌘/ 打开 |
| 演示数据（`view/demo*.go`） | **只允许测试/演示** | 生产投影必须来自 runtime；demo 设置里不得再出现"钥匙串""每日备份"等不存在的能力（Wave 1 已清理过一次） |
| 开机自启 / 休眠唤醒 / 自动更新 | **拒绝内置实现** | 单机桌面交给 systemd/launchd 与系统设置；文档写明，不假装"内置 watchdog"（RELEASE F-10） |
| 系统钥匙串（ADR-12） | **仍为拒绝现状** | 未实现前，API Key 明文存 SQLite（0600）是已登记的安全债，不是"设计如此"（ADR-12 目标态） |
| IM 专用启动接线（`imwiring.go` + 专用 settings 键） | 接受，登记为过渡形态 | 不用通用 Source 实例表，而是 flag/设置键 + 驱动直启；代价是新增 IM 类 Source 仍需改 `cmd/zeokeel`，已在如何新增 Source §8 登记 |
| 提示词 6 层 + base-hash + 历史（`internal/runtime/prompts.go`） | 接受（已接线） | 保存路径调用 `recordPromptMeta`（`internal/runtime/pages.go:557`）；历史每层最近 20 版 + `prompt.base.<layer>`；面板暂无历史列表页（RELEASE F） |

## D. Round 2/3 新增概念登记（2026-11-03 晚）

| 概念 / 实现 | 判定 | 说明与证据 |
| --- | --- | --- |
| DWS 外部 CLI 作为钉钉默认 transport | **接受，登记为集成偏离** | 官方 Stream SDK 需要公网订阅；本项目默认 `dws` 网关 CLI，串行 worker + cursor 整批成功才推进 + 隐藏幂等 marker。代价：依赖外部命令、能力受契约限制（无成员/历史）。官方 Stream 保留为 `stream` 选项（`internal/im/dingtalk/dws.go`、`design/im-channels.md`） |
| IM 渠道独立开关 + 检查/授权按钮 | 接受（EXTRA） | 原型没有"渠道管理"概念；桌面单机需要明确"未启用≠在线"，且设置变更只重启变化渠道（`internal/runtime/im.go:189-328`、`internal/ui/channels.go`） |
| eino `plantask`（TaskCreate/Get/Update/List）与 `agentsmd` | 接受，不自研 | 直接采用框架中间件；任务 JSON 存工单目录、AGENTS.md 工作区根注入（`internal/agent/builtin.go:44,96`）。禁止再手写一套 todo/instructions 注入 |
| `web_fetch` / `web_search` 内置工具 | 接受（EXTRA） | DSH 能力对齐；web_fetch 拒绝私网（SSRF）、10s/2MiB；web_search 前 5 条、端点可配（`internal/agent/webtools.go`） |
| 面板工作区树 / 会话=工单（我创建 vs 超级Agent 创建） | 接受（EXTRA） | 原型以 chat 为中心；桌面版把工作区作为一等容器，会话是 Ticket 的视图（`internal/view/workspace.go`、`internal/runtime/workspaces.go`） |
| 原生目录选择器（mygo `Dialog.Open`） | 接受 | 不在面板里手写目录浏览器；三平台由 mygo 处理，后台 goroutine 回填，测试 seam（`internal/ui/workspace.go:19-52`） |
| 面板消息必然得到答复（"未受理"修复） | 接受 | 本地消息无 Ticket 时输出「已答复」+ 超级Agent 文本（列工作区/说明下一步），确定性不耗模型；真活仍建 Ticket（`internal/runtime/runtime.go:456-482`、`workspaces.go:159-214`）。它替代"沉默=未受理"的旧行为 |
