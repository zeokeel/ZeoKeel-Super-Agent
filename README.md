# ZeoKeel Super Agent

你的 AI 同事：从聊天里接活，自己研判该不该做、该派给哪个项目，干完把交付物和结论送回原处。
单人模式、单机、单二进制、原生窗口——**不是又一个聊天框**。

> **当前状态：`1.0.0-rc.1` 候选，代码 2026-11-04 最终状态（修复波已落地）。** 本轮修复了 IM 审批优先、
> 飞书官方长连接、DWS 真实契约、异步命令边界、超级Agent 聊天双态、Ticket 身份、项目 ID/过滤、设置事务与 secret 脱敏、
> 工具信任边界。**2026-11-04 UI/UX 优化轮**把首跑、输入、收件箱、模型设置、路径真相、连接向导、可访问性
> 按交互审计重做（见下）。**未完成的是产品面**：打包只有 tar/zip（无 `.dmg`/deb/安装器）、系统钥匙串未做（API Key 明文存库）、
> 设置页规则/admission/Source 实例仍只读、飞书 webhook/IM 媒体下载、`steer/inject` 与 `domain.Thread`/`UserError`；
> 系统 TextScale/ReduceMotion/高对比、原生 app 菜单、多窗口、自动更新仍未做（见「已知限制」）。
> 全量闸门由 Lead 合并前统一跑（本文不预称全绿）。`VERSION` 是否升 1.0.0 由发布负责人按 [RELEASE.md](RELEASE.md) F 组决定。
> 渠道契约见 [design/im-channels.md](design/im-channels.md)，变更见 [CHANGELOG](CHANGELOG.md)。

## 当前状态（2026-11-04，rc.1 候选）

| 层 | 已实现（代码证据见设计 §13） | 未完成 / 限制 |
| --- | --- | --- |
| 领域与授权 | Deterministic gate（Strength/Basis）、admission table、bot list、团队规则、accept mode、Mandate/Policy version、Proposal 审批、两阶段 Eval、Input/Delivery、PR/Issue 生命周期（`follow` + `CloseThread → ThreadClosed`）、followup owners、hand-back、revalidate、pause/resume/stop/retry、`AnswerAttention`、投递三段式；**IM 入站审批优先**：每条飞书/钉钉消息（含操作员自己的）先变成一个 `DecPropose` 待批准项，Approve 才建 Ticket，Reject 什么都不留；来源页显示「待审批 N」 | 领域仍未搬 `Thread`/`UserError`；Eval 的 deadline/暂停细节仍在演进；审批逃生阀 `AutoApproveIM` 默认关闭 |
| 执行 | 每 Ticket 一个 Worker 会话 + inbox；eino `filesystem`/`backgroundtask`/`patchtoolcalls`/`reduction`/`summarization`/`subagent`；skills/MCP/定时/目标；崩溃恢复；`execution.prepare`（`sh -c`、10m、scrub 后 `ZEOKEEL_*`、失败不自动重试）；30 分钟后台作业规则；**UI 命令异步边界**：发送/新建会话/保存设置都提交到 runtime 后台命令队列，面板显示「发送中/保存中/渠道重启中」，主线程不等 DB/模型/渠道 | `Runner.Resume` 已删除；无 steer/inject；跨工作区自动路由仍只靠 Router |
| 来源 | GitHub（notifications + Issue/PR 评论；receive-all/CI 仅配置结构）；IM：**飞书官方 SDK 长连接（ws：新装默认/推荐；旧凭据迁移时 pin longpoll）与 HTTP 长轮询（longpoll，显式 fallback）**，**钉钉默认 DWS 个人 OAuth CLI（长驻 NDJSON consumer，无 poll/cursor）**，官方 Stream SDK 为 bot 模式可选 transport；按渠道三态启停（显式开关优先，缺省按凭据/flag 推导）、设置变更只重启变化渠道、失败批不推进 cursor、DWS 断线 1s→30s 重连并按 event id 去重；本地面板；定时任务 | 无 Source 实例表/面板“添加来源”：GitHub 仍需 `-repo/-gh-login`；飞书 webhook 未实现、无 IM 媒体下载；检查只验证凭据/订阅，不代替真实消息 E2E |
| 面板 | 日常导航：超级Agent / 收件箱 / 项目 / 执行 / 设置，外加折叠的「诊断」组（对话记录·消息·连接·触发·评估）；**收件箱统一「待你决定 N」**，含 IM 批准与任务回答/重试/停止；输入条多行、Enter 换行、⌘/Ctrl+Enter 发送、target chip 明确聊天/项目（派活）；回答可展开/复制/看全部（对话记录）；模型设置在设置›模型，可测试连接；项目树/执行行内动作/`#number`；窗口记忆尺寸/位置、最小 1100×720；界面字号 90/100/110%（`ui.font_scale`） | 会话不能重命名/删除；评估组只读；删除有工单的项目会被拒绝；系统级 TextScale/ReduceMotion/高对比、原生菜单、多窗口、自动更新未做 |
| 项目与会话 | UI 把工作区叫**项目**：注册项目（名称/根目录/用途/类型 + 原生目录选择器；根目录归一为绝对路径，重复 root 拒绝；opaque `ws_*` ID，名字可改且历史不断）；项目→会话（Ticket）按操作员/超级Agent 创建分组，最近 50 条；选中项目后「消息/执行」默认只看该项目；详情分开显示「项目目录 / 执行副本」 | 删除仅限“没有工单”的项目；会话列表每项目 50 条上限；Agent 只在执行副本工作 |
| 内置工具 | eino `plantask`（TaskCreate/Get/Update/List，任务 JSON 存 `.zeokeel/plan`）、**AGENTS.md 作为不可信仓库内容信封注入（≤64KB，带显式 untrusted 标记）**、`web_fetch`（仅 http(s)、DNS 解析后 pin IP、拒绝私网、显式 `Proxy:nil`、10s、2MiB、返回 untrusted-content 信封）、`web_search`（前 5 条，端点可用 `execution.web_search_url` 覆盖）；设置›执行有 `plan_tasks/agents_md/web_tools` 开关（默认开） | 无浏览器渲染/JS；web 检索仍是 HTML 解析，受站点反爬/限流影响；工具能力在工单里的可视化仍有限 |
| 运维 | `status/check/setup/login/backup/archive/stop`（退出码 0/1/2/3；stop 写 STOPPED）；`-headless` 服务；slog JSON 日志按天轮转 14 天；supervisor；单实例锁；systemd/launchd；watchdog；**settings 保存说明真实生效方式**（来源=重启相关渠道几秒、执行=下次新工单、提示词=热更新、部署=重启后生效）；`status --json`/Inspect 对 secret 做 redaction | 无开机自启/休眠唤醒/自动更新；无设置页 Source 实例/规则编辑；真实 IM 凭据 E2E 仍待环境 |

## 界面结构（2026-11-04 UI/UX 优化轮后）

**日常导航只有五项 + 诊断组**（`internal/ui/chrome.go`、`internal/view/vm.go:486-520`）：

- **超级Agent**：总览 + 全局输入（聊天/派活）。
- **收件箱**：所有「待你决定」——IM 请求批准，以及需要回答/重试/停止的任务。
- **项目**（旧称工作区）：树；展开看会话，点会话打开执行详情；新建/重命名/删除。
- **执行**：任务列表与行内动作。
- **设置**：来源（连接）/评估/执行/提示词/允许名单/模型/部署。
- **诊断（高级，默认折叠）**：对话记录 / 消息 / 连接 / 触发 / 评估——工程排障用，不在日常路径。

**首跑**（`internal/ui/onboarding.go`、`internal/runtime/ux.go`）：四步向导，必填/可选有标记；
模型步骤可「保存并测试」，可「稍后设置（演示模式）」；项目根目录有**原生目录选择器**「浏览…」；
开始使用后落总览，未配模型时显示「去配置模型」和「任务会先停在待你决定」的停车卡。

**输入条**（`internal/ui/composer.go`）：多行输入，**Enter 换行、⌘/Ctrl+Enter 发送**（IME 合成回车不会误发）；
上方 target chip 明确「发给 超级Agent（聊天）」或「项目 X（派活）」，切换后提交到该目标；
发送失败保留草稿并给「再次发送」；最新回答内联可**展开/复制/查看全部**，完整对话在「诊断›对话记录」。

**收件箱**（`internal/ui/inbox.go`、`internal/runtime/ux.go:316`）：统一「待你决定 N」一个数；
请求批准卡片（谁/哪条消息/要做什么/敏感项）可批准/拒绝；任务卡可回答/重试/停止/打开；
`j/k` 选择、`Enter` 打开；切换有 pending 与就地错误；窗口隐藏时新待办发**系统通知**。

**消息与派活**：提问先走超级Agent 回答（无模型时确定性回答）；「派活」由 target chip 指定项目，
结果「已派发到 项目：#N」；不指定时由路由器判断。**回答是聊天，不等于任务**。
Ticket 行/详情 ID 都是 domain ID，显示 `#N`。

**项目与会话**（`internal/ui/workspace.go`、`internal/runtime/workspaces.go`）：名称/根目录/用途/类型；
原生选择器、绝对路径、重复 root/名字拒绝；opaque `ws_*` ID，改名不改历史；删除空项目（有工单拒绝）。
会话=工单，按「我创建的 / 超级Agent 创建的」分组，每项目最近 50 条。**路径真相**：
详情「位置」分别列出「项目目录」与「执行副本」，可打开/复制；Agent 只在执行副本里工作，项目目录不会被自动修改；
交付物可打开/复制，提示词也明确这一点（`internal/ui/ticketdetail.go:250-286`、`internal/runtime/ux.go:279`、`worker.go:570-582`）。

**执行**（`internal/ui/execution.go:150-210`）：行内直接给「回答/重试/停止」，各自 pending 与就地错误，
不用跳总览；`#N` 标签与筛选 tabs（全部/待开工/排队/执行中/等你处理/已结束）。

**设置**：来源（连接流）、评估、执行、提示词、允许名单、**模型**、部署。
模型组可填端点/API Key/模型名并「测试连接」（测试**已保存**配置；401/403 会直接说鉴权失败），保存后立即生效；
模型响应超时在「执行›模型响应超时」。来源页的连接向导见下文「IM 渠道」。

**视觉/可访问性**（`internal/ui/tokens.go:100-145`、`internal/ui/uxsupport.go`）：提高次要文字对比度、
统一 focus token；可点击行用 `Focusable/Role/Label/Tooltip`；主要目标 28px、图标 16px；
状态栏去掉版本/rev/Go/SQLite 调试串；窗口用 `StateKey:"main"` 记忆尺寸/位置，最小 1100×720；
快捷键 ⌘K 搜索、⌘/ 名词表、⌘N 新会话、⌘, 设置。

## 内置工具（2026-11-04 最终）

Worker 默认拿到这些内置工具；设置›执行可逐个关闭（`plan_tasks/agents_md/web_tools`，默认开）：

| 工具 | 作用 | 关键限制 |
| --- | --- | --- |
| `TaskCreate` / `TaskGet` / `TaskUpdate` / `TaskList` | eino `plantask` 结构化计划/待办；任务 JSON 存 `.zeokeel/plan`，重启可读 | 只在 Worker 工作目录内；子智能体按白名单继承 |
| `AGENTS.md`（非工具，注入） | 工作区根 `AGENTS.md` 包成**不可信仓库内容**信封注入模型输入 | ≤64KB；缺失不报错；内容明确标注 untrusted，不作为权威指令 |
| `web_fetch` | 抓公网 URL 并转纯文本 | 仅 http(s)、解析后 **pin IP** 拨号、拒绝 localhost/私网、`Proxy:nil`、10s、2MiB；返回带 `<untrusted-web-content>` 信封 |
| `web_search` | 搜一次，返回标题/URL/摘要前 5 条 | 默认 DuckDuckGo HTML；`execution.web_search_url` 可覆盖；结果同样是不可信内容 |

工具名与 eino 注册名一致（PascalCase），提示词宣传同一组名字；工具调用作为 observation，不因工具错误崩轮。

## IM 渠道（2026-11-04 UX 轮后）

完整契约、DWS 命令格式、审批优先与运维步骤见 **[design/im-channels.md](design/im-channels.md)**；要点：

- **审批优先**：飞书/钉钉的每条消息（包括操作员自己的）先变成**一个**待批准项；**收件箱「待你决定 N」**与来源页
  「待审批 N」指向它。Approve 才建 Ticket 并唤醒；Reject 什么都不留；重复投递只产生一个待批项。
- **连接向导**：来源页每渠道「连接」打开向导；只显示所选 transport 的字段；先校验必填、再**用填写值测试（不保存）**，
  通过后才保存并启用；也可显式「仍然保存（未验证）」。DWS 显示本机版本、登录命令（可复制）与授权链接；
  飞书 401/403 给开放平台授权入口；钉钉有「发送时暂停监听」开关。飞书 `feishu_chats`：ws 可选，longpoll 必填。
- **飞书**：新装默认官方 SDK 长连接（`larksuite/oapi-sdk-go/v3` 的 `larkws`，推荐）；HTTP 长轮询是显式 fallback（带 cursor）。
  旧版本只有凭据、没有 transport 键的配置在升级时会被 pin 到 longpoll，保持原有接收路径不变。
  检查用 app id/secret 换 tenant token，401/403 给 `open.feishu.cn/app` 授权入口。
- **钉钉**：默认 **DWS CLI**，是操作员个人 OAuth 身份（不是 bot/app，不用 app key/secret）：
  `dws event +listen-im … -f ndjson` 长驻接收（无 poll/cursor，断线 1s→30s 重连、event id 去重）、
  `dws chat +messages-send --as user …` 发送（无 idempotency-key，靠隐藏 marker）、
  `dws auth status --format json` 检查登录（headless 用 `dws auth login --device`）。
  命令缺失/未登录时来源明确报错并给修复动作，不自动 fallback 到 Stream。
  官方 Stream SDK 是 `dingtalk_transport=stream` 的 bot 模式（需要 app key/secret/robot code）。
- **三态启用**：显式 `1/0` 优先；缺省按凭据/flags 推导；flag-only 部署由 bootstrap 补写启用（升级不会静默停摆）。
- **secret**：设置页不回显（留空=保留）；`status --json`/Inspect/health detail 统一 redaction。
- 设置键新增：`feishu_transport`、`feishu_self_id`、`dingtalk_transport`、`dingtalk_dws_cmd`、`dingtalk_profile`、
  `dingtalk_listen_kind`、`dingtalk_chat_query`、`dingtalk_bot_id`；旧 `im_self` 作为兼容回退。

## 安装

```sh
# 从源码构建（需要 Go 1.27.3+；产品构建恒 CGO_ENABLED=0）
CGO_ENABLED=0 go build -tags mygo_noinspector -o zeokeel ./cmd/zeokeel

# 或用发布包（VERSION 目前是 1.0.0-rc.1）
tar -xzf zeokeel-<VERSION>-linux-x86_64.tar.gz   # Linux
unzip zeokeel-<VERSION>-macos-arm64.zip          # macOS（未签名，右键 → 打开）
unzip zeokeel-<VERSION>-windows-x86_64.zip       # Windows（zip + install.ps1，无 MSI/NSIS）
```

**零 cgo**：`CGO_ENABLED=0` 可构建，不需要 gcc，也不需要 WebView。唯一例外是开发时的
`-race` 测试步骤（见 `scripts/verify-fixes.sh`；产品构建不接受 cgo）。

## 第一次运行

```sh
./zeokeel
```

空数据库会进入**初次设置**向导（模型 / 项目 / 来源 / 允许名单；均可稍后改或跳过），设置会写入 SQLite，
重启不丢。无显示器的机器可以非交互配置：

```sh
./zeokeel setup --state-dir ~/.zeokeel \
  --model deepseek-chat --base-url https://api.deepseek.com/v1 --api-key sk-... \
  --workspace webapp --root ~/code/webapp --keywords webapp,控制台 \
  --people github:alice
./zeokeel -headless -state-dir ~/.zeokeel     # 服务模式（systemd/launchd 用）
```

想直接看效果（内置假模型，不需要 API Key）：`./zeokeel -demo-model`。

## 命令行

| 命令 | 说明 |
| --- | --- |
| `zeokeel` | 打开窗口（托盘常驻，关窗 = 隐藏） |
| `zeokeel -headless` | 服务模式：无窗口，SIGINT/SIGTERM 排空后以 0/1/2/3 退出 |
| `zeokeel status [--json]` | 运行状态/健康/schema/setup；退出码 0 正常 / 1 可重启修复 / 2 组件错误 / 3 等待 setup |
| `zeokeel check [--json]` | 启动前自检（目录/数据库/日志/模型/setup）；可对运行中的应用执行 |
| `zeokeel setup ...` | 非交互首次配置（见上） |
| `zeokeel login [--gh gh]` | 检查 GitHub CLI 登录（IM 凭据配置方法见 `design/ops-runbook.md`） |
| `zeokeel stop [--pid N] [--no-signal]` | 写 `<stateDir>/STOPPED` 手动停标记（巡检不会自动重启），可选向 pid 发 SIGTERM；下次启动清除标记 |
| `zeokeel backup [--out FILE]` | `VACUUM INTO` 一致性备份；应用运行时拒绝执行 |
| `zeokeel archive [--before 180d] [--dry-run]` | 把结束超过 N 天的工单/触发/处置移入 `archive.db` |
| `zeokeel -pipeline FILE` | 跑一个 JSON 批量流水线后退出（pipeline 私有退出码） |
| `zeokeel -demo` | 领域 walkthrough（无窗口、无副作用） |

## 常用启动参数

| 参数 | 说明 |
| --- | --- |
| `-state-dir DIR` | 数据库、日志、工作树的根目录（默认 `~/.zeokeel`） |
| `-model/-base-url/-api-key` | 执行器用的 OpenAI 兼容模型（也可在向导/设置里保存） |
| `-assess-model` | 研判用的模型；留空则用确定性规则替代 |
| `-repo owner/name -gh-login NAME` | 盯一个 GitHub 仓库（需要 `gh` 已登录；账号即执行账号） |
| `-gh-cli PATH` | gh 命令或包装脚本（默认 `gh`） |
| `-feishu-*` / `-dingtalk-*` / `-im-self` | 只覆盖 IM **凭据**；是否启用/用哪种 transport 以设置›来源为准（默认都不启用） |
| `-skills DIR` / `-mcp FILE` | 技能库 / MCP server 配置 |
| `-no-schedule` / `-no-subagents` | 关闭定时任务 / 子智能体 |
| `-demo-model` | 内置 mock 模型，用于离线演示与测试 |

## 数据在哪

```
~/.zeokeel/
  zeokeel.db              # 状态 + 设置（SQLite v3，WAL；文件权限 0600）
  .zeokeel.lock           # 单实例锁（flock / LockFileEx）
  logs/zeokeel.log        # slog JSON，按天轮转，保留 14 天（0600）
  backups/                # zeokeel backup 的默认目录
  archive.db              # zeokeel archive 写入
  work/ticket-<N>/        # 每个工单的工作树 + 会话事件日志
```

卸载 = 删掉这个目录。**注意**：模型 API Key 当前以明文存在 `zeokeel.db`（文件 0600）里，
没有系统钥匙串（ADR-12 的目标态尚未实现）。复制/备份数据库时请按密钥对待。

## 运维

- 退出码语义唯一来源：`internal/runtime/status.go`（006:113-124）。0 正常；1 没在跑/不响应/运行时报错（可重启修复）；
  2 组件错误（Source/sync/launch 队列等，不要自动重启）；3 等待 setup（不要自动重启）。
- 日志：`<stateDir>/logs/zeokeel.log`（slog JSON，按天轮转，保留 14 天）。GUI 与 `-headless` 都写；`-demo`/`-pipeline` 只写 stderr。
- 进程内 supervisor：`-headless` 下每 60s 检查 health 表/launch 队列/运行超过 6h 的 Assignment，
  连续 3 次失败把 runtime health 标 error（`status` 读同一张表）。
- 外部巡检：`scripts/watchdog-check.sh` 按 006:133-141 执行 `status --json`（0 静默、1 重启一次、2/3 上报不重启、
  `manually_stopped=true` 则不动）。`zeokeel stop` 写 `<stateDir>/STOPPED` 标记；`status` 在“手动停且未运行”时返回 0，
  `stop` 之后手动启动会清除标记。
- systemd/launchd：`packaging/systemd/zeokeel.service`、`packaging/launchd/com.zeokeel.agent.plist`；
  单元只负责"进程死了重拉"，2/3 是否重启由巡检脚本决定。完整 runbook 见 [design/ops-runbook.md](design/ops-runbook.md)。
- 备份/归档：`zeokeel backup`（`VACUUM INTO`，应用运行时拒绝）、`zeokeel archive --before 180d`。

## 验证

```sh
export PATH=/usr/local/go/bin:$PATH
sh scripts/verify-fixes.sh   # 唯一的机械闸门：零 cgo build/test/vet + CGO_ENABLED=1 -race
sh scripts/release-check.sh  # 发布闸门：版本/产物/文档 + 上面的闸门
```

**代码最终状态：2026-11-04（P0/P1 修复 + UI/UX 优化轮已落地）。** 本轮文档按最终代码同步；**全量闸门由 Lead 在合并前统一跑**
（`sh scripts/verify-fixes.sh` + `sh scripts/release-check.sh`），本文不预称"已全绿"。
上一轮 2026-11-03 的 2×非 race + 1×-race 全绿是历史快照，P0/P1 修复后必须重跑；`VERSION` 仍 `1.0.0-rc.1`，无新 tag。

## 依赖

- 必需：Go 1.27.3+（构建时）；运行时无 Node、无浏览器、无 gcc、无数据库服务
- 直接依赖（go.mod）：`cloudwego/eino` 全家桶、`eino-ext` openai、`egoist/mygo`、`modernc.org/sqlite`、
  `modelcontextprotocol/go-sdk`、`eino-contrib/jsonschema`、`bmatcuk/doublestar/v4`、`dustin/go-humanize`、
  `golang.org/x/sys`、**`open-dingtalk/dingtalk-stream-sdk-go v0.9.1`**（钉钉 Stream bot）、
  **`larksuite/oapi-sdk-go/v3 v3.12.2`**（飞书官方长连接）
- indirect（不直接使用）：`gorilla/websocket`（钉钉 SDK 的传输依赖）等
- 可选：`gh`（GitHub 来源）；飞书应用凭据（ws/longpoll）；钉钉的 `dws` CLI（默认，个人 OAuth）或 Stream bot 凭据（不启用则不监听）

## 真实模型验收（只在本地）

```sh
export ZEOKEEL_LIVE_BASE_URL=https://your-endpoint/v1
export ZEOKEEL_LIVE_API_KEY=...
export ZEOKEEL_LIVE_MODEL=your-model
go test ./internal/runtime/ -run Live -v
```

CI 用 `-skip 'Live'` 排除它：真实模型和密钥不参与构建。

## 开发

```sh
make verify    # = scripts/verify-fixes.sh（build/test/vet/nocgo/race）
make test      # 全部测试（无需显示器；live 测试自跳过）
make dist      # 三平台二进制
make package   # 三平台发布包（tar.gz / 未签名 .app zip / Windows zip）
make release   # verify + package + release-check
```

设计文档在 `design/`；新增 Source 的 Go 版 cookbook：`design/how-to-add-a-source.md`；
发布清单在 [RELEASE.md](RELEASE.md)；审计与修复契约在 [audit/](audit/)。

## 常见问题（FAQ）

**我发了一句话，为什么没有工单？**
超级Agent 是聊天+派活双态。输入条上方的 target chip 决定目标：「发给 超级Agent（聊天）」会回答；
「项目 X（派活）」会作为该项目的工单派发并回「已派发到 项目：#N」。不指定项目时由路由器判断。
提问不是任务；要强制派活就先在 chip 里选项目（或进项目页「＋ 新建会话」）。回答内联在输入条上方，
可展开/复制/「查看全部」；完整对话在「诊断›对话记录」。

**收件箱是什么？为什么顶栏一直显示「待你决定 N」？**
收件箱把所有等你的事收在一个地方：IM 请求批准，以及需要回答/重试/停止的任务。`待你决定 N` 是唯一计数
（顶栏/侧栏/收件箱同源）。`j/k` 选择、`Enter` 打开；批准才开工，拒绝不建任务。窗口关着时新待办会发系统通知。

**IM 收到消息后为什么只显示「待审批」？**
这是 release policy：飞书/钉钉每条消息（包括你自己的）先变成**一个**待批准项，防止自动开工。
到收件箱批准/拒绝；批准才建 Ticket 并执行，拒绝什么都不留。来源页仍显示「待审批 N」。重复投递只产生一个待批项。

**IM 显示「未启用」/检查失败怎么办？**
来源页每渠道点「连接」进向导：只填所选连接方式的字段 → 校验 →「检查连接」（测试填写值，不保存）→
通过后「保存并启用」。飞书：选 ws（官方长连接，推荐；会话 ID 可留空）或 longpoll（会话 ID 必填）；
401/403 按提示去 `open.feishu.cn/app` 修权限。钉钉 DWS：向导会显示本机 `dws` 版本和可复制的登录命令；
未安装/未登录时先安装并 `dws auth login`（headless `--device`），再回来检查。Stream 是 bot 模式（app key/secret/robot）。
禁用=未监听，不会假装在线。

**模型怎么配、怎么知道配对了？**
设置›模型填端点（如 `https://api.deepseek.com/v1`）、API Key、模型名，保存后立即生效；点「测试连接」探测
**已保存**配置——401/403 会明确说鉴权失败，404 会提示检查 `/v1` 和模型名。模型响应超时在设置›执行。
未配模型时总览显示「去配置模型」，任务会停在待你决定，不会假装执行。

**为什么任务说改了、项目目录却没变？**
Agent 在**执行副本**里工作，项目目录不会被自动修改。执行详情「位置」分别列出项目目录与执行副本，
可打开/复制；交付物也在副本里，可打开/复制。要合并回项目由你决定。这是当前的真实语义，不是 bug。

**web 工具会不会访问内网/被网页内容带跑？**
`web_fetch` 只允许 http(s)，先用 DNS 解析校验并**pin 住 IP 拨号**（防 DNS rebinding），`Proxy:nil`（不受环境代理影响），
拒绝 localhost/私网，10s / 2MiB 上限；返回内容包成 `<untrusted-web-content>`。
`AGENTS.md` 同理：作为**不可信仓库内容**信封注入（≤64KB），不作为权威指令。
设置›执行可关闭 `plan_tasks/agents_md/web_tools`。

**界面字太小/窗口裁切怎么办？**
设置›部署有界面字号 90/100/110%（`ui.font_scale`，保存后立即生效）；窗口会记住尺寸/位置，
最小 1100×720。系统级 TextScale/ReduceMotion/高对比尚未接入（见已知限制）。

**为什么「删除项目」被拒绝？**
只有当项目没有工单时才会真正删除（项目行+默认策略+路由）；有工单时会拒绝并说明数量。可以重命名项目，历史不受影响。

**Ticket 的 #number 和列表里的 ID 是什么关系？**
行/详情查找键是内部 domain ID；界面显示 `#number` 方便人读。同一 Ticket 在消息流、执行列表、项目会话中指向同一对象。

**快捷键有哪些？**
⌘K 搜索、⌘/ 名词表、⌘N 新会话、⌘, 设置；收件箱 `j/k` 选择、`Enter` 打开；输入条 Enter 换行、⌘/Ctrl+Enter 发送；
Esc 关闭弹层。

## 已知限制（诚实清单）

- macOS 包**未签名/未公证**，首次打开需右键 → 打开；`.dmg`、Debian 包、PKGBUILD、自动更新均未实现。
- Windows 只有 zip + `install.ps1`，没有 MSI/NSIS 安装器；CI 只交叉编译与查体积，不产包。
- **项目**：可新建、重命名、删除（仅空项目；有工单拒绝）；会话不能重命名/删除；每项目会话列表只投影最近 50 条；
  根目录创建时归一为绝对路径并拒绝重复，创建后不能改根目录/类型。
- **执行语义**：Agent 只在**执行副本**里工作，项目目录不会被自动修改；目前没有“真正在原项目里执行”的同步/合并。
- **IM**：飞书 webhook/媒体下载未实现；DWS 依赖外部 `dws` CLI，真实 CLI 版本/登录流程与真实租户 E2E 未在本机验证；
  Stream 是 bot 模式（需 app key/secret/robot code）；两者都不下载图片/文件。
- **设置页**：**来源组**用连接向导配置 IM；**评估组**只读（规则/admission/accept mode 不可编辑）；
  没有 Source 实例表，GitHub 仓库/登录仍只能 `-repo/-gh-login`（改设置需重启）。
- **系统钥匙串未实现**：API Key 明文存 SQLite（0600）；读取侧已做 redaction（`status --json`/Inspect/health detail 不泄密），
  worker 的 shell 环境已剥离凭证变量。复制/备份数据库时仍请按密钥保管。
- **可访问性**：已提高对比度、加 focus/标签/工具提示，但**没有**系统 TextScale、ReduceMotion、高对比模式接入。
- **平台集成**：没有 mygo 原生 app 菜单项（只有窗口内快捷键/托盘「打开/退出」）；没有多窗口；没有自动更新/About 版本检查。
- **计划/任务可见性**：Worker 有 TaskCreate/Get/Update/List，但面板还没有显示计划/待办的可视化页面。
- 提示词：保存时校验未知变量/占位符/超长，历史每层最近 20 版已记录；面板展示"内置默认值已更新"，但还没有历史列表页。
- 领域类型 `domain.Thread`/`domain.UserError` 未落地；无 `steer/inject`；`Runner.Resume` 已删除。
- 没有开机自启/休眠唤醒（交给 systemd/launchd）。
- **工具策略是全局开关**（`plan_tasks/agents_md/web_tools`），没有按项目的 allowlist；web 默认开启，需要时在设置›执行关闭。
- `execution.prepare` 在 **execution root（工单目录的父目录）** 执行，工作区根由 `$ZEOKEEL_WORKDIR` 提供；失败不自动重试。
- **shell 工具不是安全边界**：它能执行的命令可以读取本机任何可读文件（包括其它工单的工作树）。
  单机单人使用没问题；多人共机或处理他人数据时，请用独立系统账号或容器隔离（设计 §5.5）。
- **settings 迁移**：启动时一次性执行（`LoadSettings` → `migrateSettings` + `SettingsSchema` stamp）：bool 归一为 `1/0`、
  旧 IM 凭据推导启用/transport（**旧飞书凭据-only 配置 pin `longpoll`**；新装仍默认 `ws`）、`im_self` 拆分到每渠道；
  旧 `true/false` 读取仍兼容。

## License

MIT License. See [LICENSE](LICENSE).
