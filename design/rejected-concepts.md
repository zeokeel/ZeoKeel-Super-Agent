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
| DWS 外部 CLI 作为钉钉默认 transport | **接受，登记为集成偏离** | 官方 Stream SDK 需要公网订阅；本项目默认 `dws` 网关 CLI，长驻 NDJSON consumer（无 poll/cursor）+ event id 去重 + 1s→30s 重连 + 隐藏幂等 marker。代价：依赖外部命令、能力受契约限制（无成员/历史）。官方 Stream 保留为 `stream` 选项（`internal/im/dingtalk/dws.go`、`design/im-channels.md`） |
| IM 渠道独立开关 + 检查/授权按钮 | 接受（EXTRA） | 原型没有"渠道管理"概念；桌面单机需要明确"未启用≠在线"，且设置变更只重启变化渠道（`internal/runtime/im.go:189-328`、`internal/ui/channels.go`） |
| eino `plantask`（TaskCreate/Get/Update/List）与 `agentsmd` | 接受，不自研 | 直接采用框架中间件；任务 JSON 存 `.zeokeel/plan`、AGENTS.md 以**不可信仓库内容信封**（≤64KB）注入（`internal/agent/builtin.go:30-44,71,158-270`）。禁止再手写一套 todo/instructions 注入 |
| `web_fetch` / `web_search` 内置工具 | 接受（EXTRA） | DSH 能力对齐；web_fetch 拒绝私网（SSRF）、10s/2MiB；web_search 前 5 条、端点可配（`internal/agent/webtools.go`） |
| 面板工作区树 / 会话=工单（我创建 vs 超级Agent 创建） | 接受（EXTRA） | 原型以 chat 为中心；桌面版把工作区作为一等容器，会话是 Ticket 的视图（`internal/view/workspace.go`、`internal/runtime/workspaces.go`） |
| 原生目录选择器（mygo `Dialog.Open`） | 接受 | 不在面板里手写目录浏览器；三平台由 mygo 处理，后台 goroutine 回填，测试 seam（`internal/ui/workspace.go:19-52`） |
| 面板消息必然得到答复（"未受理"修复） | 接受（2026-11-04 升级为聊天+派活双态） | 提问→模型回答/确定性回答；派活→「已派发 #N」；待批→「需要你确认」；回答内联输入框上方与总览；非失败不标红（`internal/runtime/chat.go`、`internal/ui/chat.go`、`runtime/runtime.go:812-880`） |

## E. 修复波（2026-11-04）新增/变化的概念登记

| 概念 / 实现 | 判定 | 说明与证据 |
| --- | --- | --- |
| ~~**IM 入站审批优先**~~ → **DSH 对齐（rc.3 推翻，rc.4 彻底删除）** | **撤销旧 release policy；改 DSH 主链** | admitted 明确 IM/GitHub 请求直接 Eval 接单 → Ticket；推断按 accept mode off/observe/auto（auto 需规则）；rc.4 删除 proposal/Approval/`RequireApproval`，旧 DB propose 载入为 ignore；收件箱 attention-only（`internal/engine/evaluate.go:348-390`、`gate.go:52-56`、`persist.go:156-162`、`ui/inbox.go:1-140`、`cmd/zeokeel/acceptance_test.go:129-160`） |
| 飞书官方 `larkws` 长连接为推荐接收（longpoll 为显式 fallback） | 接受（替换“只做长轮询”） | 低延迟、无需公网回调；`larksuite/oapi-sdk-go/v3 v3.12.2` + `OnP2MessageReceiveV1`；longpoll 保留 cursor 兼容旧环境（`internal/im/feishu/ws.go`、`go.mod:12`）。webhook 拒绝。 |
| DWS=操作员个人 OAuth，Stream=bot | 接受，身份模型偏离 | 两者**不是同一身份**：DWS 不用 app key/secret，靠 `dws auth login` 的 profile；Stream 是官方机器人 SDK（app key/secret/robot）。文档与设置字段按身份分开（`internal/im/dingtalk/dws.go:1-28`、`internal/runtime/pages.go:483-500`） |
| DWS 长驻 NDJSON consumer（无 poll/cursor） | 接受，替换早期“JSON poll 契约” | 真实 CLI 只提供 `event +listen-im … -f ndjson`；event id 去重、断线 1s→30s 重连、短命令串行；发送无 idempotency-key，靠隐藏 marker（`dws.go`、`design/im-channels.md` §4） |
| UI 异步命令边界（后台队列 + 发送中/保存中/重启中） | 接受 | 禁止 handler 内同步 DB/模型/渠道重启；`ui/commands.go` 的 seam 把命令交给 goroutine，`postToUI` 回结果；面板显示 pending 状态（`internal/ui/commands.go`、`internal/ui/input.go`、`internal/ui/settings.go`） |
| 超级Agent = 聊天 + 派活双态 | 接受（产品拍板） | 全局输入既回答问题也派活；回答有模型/确定性两条路径；内联反馈；只有真实失败才红（`internal/runtime/chat.go`、`internal/view/chat.go`、`internal/ui/chat.go`） |
| Ticket 身份 = domain ID（显示 #number） | 接受 | 行/详情查找键统一 domain ID，避免“编号字符串当键”；显示层用 TicketLabel 出 `#N`；旧“42 fixture”错误被列为回归测试（`internal/runtime/pages.go:302,310`、`internal/view/vm.go:87-95`） |
| 工作区 opaque ID + 可改名 + 过滤上下文 | 接受 | 名字可变、身份/历史不变；中文名不再塌缩；选中工作区过滤「消息/执行」；删除空工作区（有工单拒绝）而不是假删（`internal/runtime/workspaces.go:23-190`、`internal/store/store.go:390`、`internal/ui/workspace.go:346-400`） |
| AGENTS.md = 不可信仓库内容信封（≤64KB） | **接受，安全边界** | 不再把仓库指令当权威系统指令；包 `<untrusted-repository-content>` 信封并限 64KB；仓库内容视为数据（`internal/agent/builtin.go:30-44,158-270`） |
| web 内容 = 不可信数据 + DNS pin/Proxy:nil | 接受，安全边界 | web_fetch 解析后 pin IP 拨号、显式禁用环境代理、拒绝私网、2MiB/10s、返回 `<untrusted-web-content>`；web 工具可按设置关闭（`internal/agent/webtools.go:40-44,98-160,226-290`、`runtime/pages.go:520-525`） |
| 设置唯一 bool 编码 + 三态启用 + 一次性迁移 + flag bootstrap | 接受 | UI 开关读写 1/0（读兼容 true/false）；`LoadSettings` 一次性迁移（bool/IM 启用/transport/im_self）并写 schema stamp；显式值优先，缺省按凭据/flags 推导；flag-only 部署启动时补写（`internal/runtime/settingscodec.go`、`internal/runtime/im.go:33-130`、`internal/runtime/pages.go:678-707`） |
| settings 保存事务化 + secret redaction | 接受 | 先构造新快照→事务提交→成功才换内存；失败面板不变；secret 不回显，status/Inspect/health 用 `***`（`runtime/pages.go:567-690,539-550`、`store/redact.go`、`store/inspect.go`） |
| 界面字号 scale token（`ui.font_scale`）+ 最小窗口 1100×720 | 接受（替换 magic +2） | 显式字号走 `fs()` 缩放，三档 0.9/1.0/1.1，设置›部署可编辑并投影（`internal/ui/tokens.go:48-78`、`internal/ui/app.go:238-264`、`internal/runtime/pages.go:537-539`） |

## F. UI/UX 优化轮（2026-11-04）新增/变化的概念登记

| 概念 / 实现 | 判定 | 说明与证据 |
| --- | --- | --- |
| 日常五项导航 + 折叠「诊断」 | **接受，替换“监控”平铺** | 超级Agent / 收件箱 / 项目 / 执行 / 设置；工程页收进默认折叠「诊断（高级）」。原型/DSh 的监控页不再占日常路径（`internal/view/vm.go:486-520`、`internal/ui/chrome.go:60-110`） |
| UI 叫「项目」而不是「工作区」 | 接受（产品词） | 领域/代码仍是 workspace；UI 文案统一「项目」，减少“工作区/项目/会话/工单”混用（`internal/ui/workspace.go`、`internal/view/vm.go:525-535`） |
| 多行 composer + target chip（聊天/项目派活） | **接受，替换单行隐式路由** | Enter 换行、⌘/Ctrl+Enter 发送（IME 安全）；chip 明确提交目标；失败保留草稿+再次发送；回答可展开/复制/查看全部（`internal/ui/composer.go:19-220`、`internal/ui/commands.go:137-188`、`internal/runtime/ux.go:134-172`） |
| 统一收件箱（审批 + 任务动作） | **接受，替换分散在总览/来源** | 一个 `AttentionCount` 单源「待你决定 N」；批准/拒绝/回答/重试/停止；j/k/Enter；隐藏窗口系统通知（`internal/ui/inbox.go:1-233`、`internal/runtime/ux.go:314-438`、`internal/view/attention.go`） |
| 模型设置成为一等设置组 + 测试连接 | 接受（替换仅 onboarding/CLI） | 设置›模型端点/API Key/模型名；测试已保存配置（401/403/404 分类）；保存即时生效；未配模型停车卡（`internal/runtime/ux.go:36-125`、`internal/runtime/pages.go:543-558`、`internal/ui/settings.go:501-535`） |
| 首次运行可「稍后设置（演示模式）」 | 接受 | 不再强制 model+workspace 才能进入；任务停在待你决定，总览说明并给「去配置模型」（`internal/runtime/ux.go:423-438`、`internal/ui/onboarding.go:140-180`、`internal/ui/overview.go:76-95`） |
| 路径真相：项目目录 vs 执行副本 | **接受，诚实披露当前语义** | 详情分列两路径、可打开/复制；提示词明确只在副本工作、不修改项目；交付物是副本路径（`internal/ui/ticketdetail.go:250-286`、`internal/runtime/worker.go:570-582`） |
| 连接向导（draft 校验/测试/通过才保存） | 接受 | 只显示所选 transport 字段；用填写值测试、不保存；DWS 版本/登录命令/授权链接；Feishu chats ws 可选/longpoll 必填；暂停监听（`internal/ui/channelwizard.go:1-520`、`internal/runtime/ux_im.go`、`internal/runtime/im.go:1392-1505`） |
| 可访问性最小集（对比度/focus/标签/尺寸） | 接受（rc.2 接入系统级） | 提高 faint 对比度与 focus token；`Focusable/Role/Label/Tooltip`；28px 目标/16px 图标；状态栏去 debug（`internal/ui/tokens.go:100-145`、`internal/ui/uxsupport.go`、`internal/ui/chrome.go:143-160`）；rc.2 另接系统 TextScale/ReduceMotion/HighContrast/Accent（§G） |
| 窗口 StateKey + 最小尺寸 | 接受 | `StateKey:"main"` 记忆尺寸/位置；最小 1100×720；不引入多窗口（`internal/ui/app.go:320-330`） |
| **仍未做（rc.2 时点）**：真正原项目执行、系统钥匙串、多窗口、自动更新下载/安装、真实租户 E2E | **拒绝假装** | 明确写入 README 已知限制/RELEASE F-7：执行副本 + 显式 apply-back 是当前语义；密钥仍 SQLite 0600；更新只检查不下载 |

## G. rc.2（2026-11-04）新增概念登记

| 概念 / 实现 | 判定 | 说明与证据 |
| --- | --- | --- |
| 流式聊天 + 停止生成 | 接受（替换一次性回答） | SSE 增量 60ms 节流发布；延迟降低；停止只取消本次请求，已生成部分保留并标「已停止生成」；最终文本只追加一次（`internal/runtime/rc2.go:24-190`、`internal/ui/composer.go:241-305`） |
| 聊天 Markdown 渲染 | 接受（安全子集） | 标题/粗斜体/行内代码/围栏代码/列表/http(s) 链接；**不解释 HTML**，避免注入；未匹配标记按字面；链接外部浏览器打开（`internal/ui/markdown.go:19-180`） |
| 计划/待办可见 | 接受（替换“只有日志”） | `.zeokeel/plan/*.json` → `PlanVM`；执行详情+对话任务卡；只读投影，不是计划编辑器（`internal/view/plan.go`、`internal/runtime/rc2.go:302-433`、`internal/ui/planpanel.go`） |
| 应用到项目（显式回写） | **接受，替代“自动原地执行”** | 不假装 Agent 改了项目：显式按钮、只复制记录交付物、越界拒绝、同名先 `.bak-<timestamp>`、返回相对路径、确认框列出文件/目标/覆盖警告。真正同步/合并仍拒绝（`internal/runtime/rc2.go:193-270`、`internal/ui/applyproject.go`） |
| ~~拒绝无损 + 撤销~~ | **rc.4 随审批机制一起删除** | 没有 Reject/UndoReject/proposal/RejectedAt；旧 DB propose 只作历史 ignore（`internal/engine/persist.go:156-162`） |
| 原生应用菜单 | 接受（替换只有窗口快捷键） | App/文件/编辑/窗口 + 标准编辑 roles；⌘N/⌘,；托盘保留（`internal/ui/menu.go:1-72`、`internal/ui/app.go:345`） |
| 系统无障碍偏好 | 接受（系统级接入） | TextScale×`ui.font_scale`、ReduceMotion、HighContrast、系统 Accent；不新增自有“无障碍模式”（`internal/ui/prefs.go:1-61`、`internal/ui/app.go:441-462`） |
| 关于 / 更新检查（只查不下载） | 接受 | 版本/许可证；查询 GitHub Releases API，显示最新版本/发布页；**不下载、不安装、不自动更新**（`internal/ui/about.go:1-117`、`internal/runtime/rc2.go:355-415`） |
| **仍拒绝假装**：原项目原地执行、钥匙串、多窗口、自动更新下载、真实租户 E2E | **明确未做** | 写入 README 已知限制/RELEASE F-7：执行副本 + apply-back 是当前真实语义；密钥仍 SQLite 0600；没有多窗口；更新只检查 |

## H. rc.3（2026-11-04）DSH 对齐与项目级能力登记

| 概念 / 实现 | 判定 | 说明与证据 |
| --- | --- | --- |
| **推翻“IM 审批优先”**，回到 DSH 主链 | **接受（rc.4 进一步删除 opt-in 审批）** | 明确/admitted 请求直接 Eval→Ticket；推断请求按 accept mode off/observe/auto（auto 需规则）；rc.4 删除 `DecPropose`/`RequireApproval`/Approve/Reject/UndoReject；旧 DB propose → ignore；收件箱 attention-only（`internal/engine/evaluate.go:348-390`、`gate.go:52-56`、`persist.go:156-162`、`ui/inbox.go:1-140`） |
| 项目页 概览/设置/统计 | 接受 | 每项目浏览器开关/Chrome·Edge/无头/CDP 端口/profile 只读、启停/状态、技能开关（全局默认开/项目可关）、统计计数/最近活动/工具可用性（`view/workspacepanel.go`、`ui/workspacepanel.go:193-520`、`runtime/workspacesettings.go:75-160`） |
| per-workspace 浏览器 + `chrome-devtools-mcp` | **接受（单机多项目隔离）** | 默认路径仅检测 Chrome/Edge；独立 profile `<stateDir>/workspaces/<ws>/browser-profile`；唯一 CDP 端口；`npx -y chrome-devtools-mcp@latest --browserUrl ...`；`mcp__browser__*` 只注入该项目 Worker；env scrub；缺依赖可操作错误（`internal/browser/`、`runtime/workspacesettings.go:427-480`） |
| `read_image` 元数据-only | 接受（诚实偏离） | 模型端点 text-only，不附像素；只报告格式/字节/尺寸，10MB/路径约束；需要视觉用 bash OCR/ImageMagick 或问人；升级路径是多模态 tool result（`internal/agent/imageread.go`） |
| `job_list` / `subagent` 命名 | 接受 | 补齐 DSH job_output/list/kill；eino 默认 `agent` 改名 `subagent`（`internal/agent/jobs.go:275-309`、`subagent.go:55`） |
| Worker 域工具 `ack/handback/thread_unrelated/request_status` | 接受 | DSH Executor parity；按 Ticket/Thread 粒度，`ack` 无 purpose 元数据；prompt 已列出（`runtime/artifacts.go:121-222`、`worker.go:580`） |
| SuperAgent **无工具** | **接受（有意缺席）** | SuperAgent 是问答，不是 orchestrator；请求 payload 无 tools；Worker 已有 `read_resource`（orchestrator_read 等价）；DSH main orchestrator 工具集仍没有模型可调用面（主 Session 是确定性 assessor）（`runtime/rc2.go:121-132`、`runtime/rc3.go:17-160`、`audit/tool-gap.md`） |
| 仍拒绝假装：原项目原地执行、钥匙串、多窗口、自动更新下载、read_image 像素、DSH main orchestrator 工具、浏览器无 Chrome/Edge+node | **明确未做** | 写入 README 已知限制/RELEASE F-7/K：执行副本+apply-back 是真实语义；密钥 SQLite 0600；无多窗口；更新只检查；视觉需 vision endpoint |

## I. rc.4（2026-11-04）去历史包袱与前置检查登记

| 概念 / 实现 | 判定 | 说明与证据 |
| --- | --- | --- |
| **旧式审批/提案机制彻底删除** | **接受（去掉历史包袱）** | Decision 只有 accept/stop/ignore/unavailable；无 Approve/Reject/UndoReject/proposal/`RequireApproval`；旧 DB `propose` 行载入为 ignore（`legacyImport`），旧 `requireApproval` 忽略；所有审批 UI/文案移除（`internal/domain/ticket.go:10-13`、`internal/engine/persist.go:156-162`、`internal/ui/inbox.go:1-140`） |
| 收件箱 = attention-only | 接受 | 只显示任务问题/受阻/失败；历史 proposal 不显示/不可操作；`待你决定 N` 仍单一计数（`internal/ui/inbox.go:1-140`） |
| Node.js/npx 前置检查 | **接受（不自动安装）** | `browser.Prerequisites` 检测 node/npx 与版本；缺失时警告+OS 安装命令（brew/winget/apt）+nodejs.org+重新检查；启动按钮禁用并说明原因；不自动安装/不假装运行（`internal/browser/prereq.go:1-140`、`internal/runtime/rc3.go:170-205`、`internal/ui/workspacepanel.go:613-692`） |
| `read_resource`（DSH orchestrator_read 等价） | **接受，替代缺口** | 分页读自己的 ticket/inputs/trigger/thread（只读、归属/路径校验、文本 redaction、默认 20/上限 200）；Worker prompt 列出；SuperAgent 仍无工具（`internal/runtime/rc3.go:17-160`、`internal/runtime/artifacts.go:205-214`、`runtime/worker.go:580`） |
| 仍拒绝假装：原项目原地执行、钥匙串、多窗口、自动更新下载、read_image 像素、DSH main orchestrator 工具 | **明确未做** | README/RELEASE F-7/K：执行副本+apply-back；密钥 SQLite 0600；更新只检查；视觉需 vision endpoint |
