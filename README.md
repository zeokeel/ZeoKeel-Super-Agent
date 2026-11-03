# ZeoKeel Super Agent

你的 AI 同事：从聊天里接活，自己研判该不该做、该派给哪个工作区，干完把交付物和结论送回原处。
单人模式、单机、单二进制、原生窗口——**不是又一个聊天框**。

> **当前状态：`1.0.0-rc.1` 候选。** 机械闸门（build/test/vet/zero-cgo/(CGO=1) `-race`/package/release-check）
> 已在 2026-11-03 最终快照全绿。**未完成的是产品面**：打包只有 tar/zip（无 `.dmg`/deb/安装器）、
> 系统钥匙串未做（API Key 明文存库）、设置页规则/admission/Source 实例仍只读、飞书只做 HTTP 长轮询（无 WS）、无 IM 媒体下载、
> `steer/inject` 与 `domain.Thread`/`UserError` 未落地。Round 2/3 已补侧栏/工作区树/原生目录选择器/IM 渠道管理/内置工具，
详见 [CHANGELOG](CHANGELOG.md) 与 [design/im-channels.md](design/im-channels.md)。`VERSION` 是否升 1.0.0 由发布负责人按 [RELEASE.md](RELEASE.md) F 组决定。

## 当前状态（2026-11-03，Wave 2 之后）

| 层 | 已实现（代码证据见设计 §13） | 未完成 / 限制 |
| --- | --- | --- |
| 领域与授权 | Deterministic gate（Strength/Basis）、admission table（三格 × 九种）、bot list、团队规则（Rules）、accept mode、Mandate/Policy version、Proposal 审批、两阶段 Eval（`Receive` 入队、`EvaluateDue` 按 Thread 批量判定，不占入口锁）、Input/Delivery、PR/Issue 生命周期（`follow` 工具 + `CloseThread → ThreadClosed`：ready 等跟随 PR 关闭，merged/closed 结束工单）、followup owners、hand-back、revalidate、pause/resume/stop/retry、`AnswerAttention`、投递三段式（delivered/read/turnEnded） | 领域仍未搬 `Thread`/`UserError` 类型；Eval 的 deadline/暂停细节仍在 spec/代码之间演进 |
| 执行 | 每 Ticket 一个 Worker 会话 + inbox（未投递/未读 Input、上下文无关权威标注）；eino `filesystem` 工具、`backgroundtask`（`job_output`/`job_kill`）、`patchtoolcalls`、`reduction`、`summarization`、`subagent`；skills/MCP/定时/目标；崩溃恢复（中断 note + 新 Assignment + `patchtoolcalls` 补悬空调用）；**`execution.prepare` 工单准备**（`sh -c`、10 分钟、scrub 后的 env + `ZEOKEEL_*`、失败即失败不自动重试、提示词展示变量）；**30 分钟后台作业规则**（`execution.background_timeout` 默认 1800s，到点 kill + `ReportWaiting` 含 job id/最后输出、不唤醒；子智能体不适用） | `Runner.Resume` 已删除（恢复只走 `RecoverAssignments` + `patchtoolcalls`）；无 steer/inject；worker bash 子进程获得与 prepare/提示词相同的 scrub 后 `ZEOKEEL_*` 变量（`ZEOKEEL_WORKDIR/TICKET_DIR/TICKET/TICKET_NAME/REPO`） |
| 来源 | GitHub（notifications + Issue/PR 评论；receive-all/CI 仅配置结构）；**IM 渠道可单独启用/停用**：飞书 HTTP 长轮询、钉钉默认 **DWS 外部 CLI**（官方 Stream SDK 可选 transport），设置›来源有开关/transport/凭据/检查/授权，未启用=未监听；本地面板；定时任务；Source `Kind`/registry/配置/游标持久化/Channel 抽象完整 | 无 Source 实例表/面板“添加来源”：GitHub 仍需 `-repo/-gh-login`；暂无飞书 WS/webhook、无 IM 媒体下载；“检查”只验证凭据/订阅，不代替真实消息 E2E |
| 面板 | **侧边栏：超级Agent / 工作区（树）/ 监控（消息·来源·触发·评估·执行）/ 设置**，底部显示 OS/arch 与时区；工作区页与「＋ 新建工作区」弹窗（名称/根目录/用途/类型，根目录用**原生目录选择器**）；会话按「我创建的 / 超级Agent 创建的」分组；全局字号 15；待办行内动作、Ticket 详情、名词表（⌘/）、⌘K 搜索、提示词编辑器（6 层 + 团队三段 + 热更新）；来源组每渠道卡片（开关/transport/凭据/检查/授权）；执行组含 prepare、web_search 端点、background_timeout；部署组含日志/备份/schema/OS/时区 | 会话/工作区还不能重命名或删除；设置页**评估组**只读（规则/admission/accept mode 不可编辑）；`ui/overview` 仍有 demo 数据路径只供测试 |
| 工作区与会话 | 面板注册工作区（scan 事实 + 持久化 + 默认策略）；工作区→会话（Ticket）投影，按创建者分组，最近 50 条；「＋ 新建会话」显式指定工作区并**直接派发**，不过 Router | 工作区/会话不能重命名、删除或改根目录；会话列表只有最近 50 条 |
| 内置工具 | eino `plantask`（TaskCreate/TaskGet/TaskUpdate/TaskList，任务 JSON 存工单目录）、`agentsmd`（工作区根 `AGENTS.md` 注入，≤256KB，缺失不报错）、`web_fetch`（仅 http(s)、拒绝 localhost/私网、10s、2MiB、HTML→文本）、`web_search`（默认 DuckDuckGo HTML，取前 5 条，端点可用 `execution.web_search_url` 覆盖）；运行时默认对 Worker 全开，子智能体按白名单继承 | 没有内置浏览器渲染/JS；web 工具仍受网络与站点反爬影响；`web_search` 结果是 HTML 解析，不是结构化 API |
| 运维 | `zeokeel status/check/setup/login/backup/archive/stop`（status 退出码 0/1/2/3；stop 写 STOPPED 标记）；`-headless` 真服务模式（SIGINT/SIGTERM 排空后按 006 退出码退出）；slog JSON 日志 `<stateDir>/logs/zeokeel.log` 按天轮转保留 14 天；进程内 supervisor（每 60s 检查 health/launch 队列/卡住的 Assignment，连续 3 次失败写 error）；state dir 单实例锁（flock/LockFileEx，在 store 打开时即拿，早于 recovery）；systemd/launchd 样例；`scripts/watchdog-check.sh`（006 小时巡检语义） | 无开机自启/休眠唤醒/自动更新（交给 systemd/launchd）；无设置页 Source 实例/规则编辑 |

## 界面结构（2026-11-03 Round 2/3）

侧边栏按 DSH 的组织方式收敛（`internal/ui/chrome.go:54-99`）：

- **超级Agent**：顶部唯一大项，打开总览（待办/健康/最近/用量）和全局输入条。任何问题都可以先问它。
- **工作区**：一棵树。每个工作区行可展开看会话，再点进「执行」打开该 Ticket；底部「＋ 新建工作区」。
- **监控**：消息 / 来源 / 触发 / 评估 / 执行（执行带运行中计数）。
- **设置**：唯一配置入口；侧栏底部一行显示当前机器 `OS/arch · 时区 (UTC±h)`（`internal/ui/chrome.go:92-99`）。

**工作区与会话**（`internal/ui/workspace.go`、`internal/runtime/workspaces.go`、`internal/runtime/pages.go:953-995`）：

- 「＋ 新建工作区」填名称 / 根目录 / 用途 / 类型（repo·data·docs·mixed）。根目录用 **mygo 原生目录选择器**
  （`mygo.Dialog.Open(Directory:true)`，Linux GTK/portal、Windows、macOS 同一 API；对话框在后台 goroutine，
  选完回填，不卡帧）。
- 工作区行展开后，会话按**「我创建的」/「超级Agent 创建的」**分组，按最近活动倒序，每工作区最多 50 条
  （`internal/view/workspace.go` 的 `Groups`；`runtime/pages.go:963` 的 50 上限）。
- 「＋ 新建会话」弹窗**预填该工作区**：输入一句话 → runtime 用 `Dispatch`（显式 workspace，不再问 Router）
  立即变成 Ticket 并唤醒执行（`runtime/workspaces.go:101`）。这是"人已经指明工作区"的快捷方式，
  **总是派发**，不会回复"未受理"。
- 全局字号 15（`internal/ui/tokens.go:49` 的 `baseFontSize`）。

**面板消息一定有结果**（修复"未受理"）：

- 操作员从面板发消息后，如果能匹配工作区/工作请求 → 建 Ticket 并派发；否则**超级Agent 会在同一条消息下追加
  一条回答行**（"超级Agent：…"）：问"有哪些工作区"就列出名称/用途/类型；否则说明为什么没派发、下一步怎么做
  （选择工作区新建会话，或直接说清任务）。决策行从"未受理"改为"已答复"+原因码（`runtime/runtime.go:456-482`）。
- 这条回答是确定性文本，不调用模型；它只解决"人发了话却只看到未受理"的体验，不改变"真活才建 Ticket"的规则。
- 来源：`runtime/workspaces.go:159-214`（`workspaceAnswer`/`noDispatchMessage`/`localAnswer`）；
  UI 侧不再把裸"未受理"画出来（`internal/ui/stream.go:129-141`）。

## 内置工具（Round 2/3）

Worker 默认拿到这些内置工具（`internal/runtime/worker.go:480-482` 打开；名字与 eino 一致）：

| 工具 | 作用 | 关键限制 |
| --- | --- | --- |
| `TaskCreate` / `TaskGet` / `TaskUpdate` / `TaskList` | eino `plantask` 的结构化计划/待办；任务以 JSON 存在工单目录，重启可读 | 只在 Worker 工作目录内；子智能体按白名单继承（`internal/agent/builtin.go:44`，`subagent.go:104-130`） |
| `AGENTS.md`（非工具，注入） | eino `agentsmd` 把工作区根的 `AGENTS.md` 注入模型输入 | ≤256KB；文件缺失不报错（`builtin.go:28-30,96`） |
| `web_fetch` | 抓一个公网 URL 并转纯文本（去 script/style/tag） | 仅 http(s)、拒绝 localhost/私网、10s 超时、2MiB 上限（`internal/agent/webtools.go:26-27,65-76,169-200`） |
| `web_search` | 搜一次，返回标题/URL/摘要前 5 条 | 默认 DuckDuckGo HTML；端点可用 `execution.web_search_url` 覆盖（`webtools.go:29,79`；`runtime/pages.go:496`） |

工具清单写进 Worker 提示词（`internal/runtime/worker.go:573-575`），模型不需要猜名字。

## IM 渠道（Round 3）

完整契约、DWS 命令格式与运维步骤见 **[design/im-channels.md](design/im-channels.md)**；要点：

- **按渠道启用/停用**：设置›来源里飞书、钉钉各有开关；禁用=取消订阅/停止轮询、状态"未启用"、不假装在线；
  保存后只重启有变化的渠道。
- **飞书**：HTTP 长轮询（无 WS/webhook）；「检查通道」用 app id/secret 换 tenant token，
  401/403 时给 `https://open.feishu.cn/app` 授权入口。
- **钉钉**：默认走本机 **`dws` CLI**（`dws auth status/poll/send/health --json`，单次 30s、每渠道一个串行 worker、
  poll 失败 1s→30s 退避、**整批被接受后才推进 cursor**、发送带隐藏幂等标记）。
  未安装/未登录时来源明确报错并给 `dws auth login`；**不会自动 fallback 到 Stream**。
  官方 Stream SDK 保留为 `dingtalk_transport=stream` 的可选 transport（见下方已知限制里的 UI 偏差）。
- 设置键：`feishu_enabled/feishu_transport/feishu_app_id/feishu_app_secret/feishu_chats`、
  `dingtalk_enabled/dingtalk_transport/dingtalk_dws_cmd/dingtalk_bot_id/dingtalk_app_key/dingtalk_app_secret/dingtalk_robot`、
  `im_self`（`internal/runtime/im.go:31,51`；`runtime/pages.go:458-484`）。
- 命令行 `-feishu-*` / `-dingtalk-*` / `-im-self` 只覆盖**凭据**，不覆盖 enable/transport（以设置为准）。

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

空数据库会进入**初次设置**向导（模型 / 工作区 / 来源 / 允许名单），设置会写入 SQLite，
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

**2026-11-03 最终冻结树（20:53–20:55）**：`CGO_ENABLED=0 go build/vet/check-nocgo` 全部 PASS；
非 race 全量 `go test ./... -skip Live` **连续 2 次全绿**；全量 `CGO_ENABLED=1 go test -race ./... -skip Live` **全绿**；
`make package` 产出三平台包；`sh scripts/release-check.sh` **16 通过 / 0 失败 / 6 跳过**（跳过项需 CI/真机/真实凭据）。
此前 20:47 的 3 个 runtime 测试红与 cmd 偶发 flake 在冻结后未再出现。以后任何代码改动都要重跑 `sh scripts/verify-fixes.sh`。

**Round 2/3 增量验证（2026-11-03 23:10，docs pass 时实测）**：`CGO_ENABLED=0 go build ./...`、`go vet ./...` 通过；
`go test ./internal/ui ./internal/view ./internal/runtime ./internal/agent ./cmd/zeokeel -skip Live` 通过。
上面的全量闸门数字是之前冻结树的；Round 2/3 改动后**发布前必须重跑** `sh scripts/verify-fixes.sh` 与 `release-check.sh`。

## 依赖

- 必需：Go 1.27.3+（构建时）；运行时无 Node、无浏览器、无 gcc、无数据库服务
- 直接依赖（go.mod）：`cloudwego/eino` 全家桶、`eino-ext` openai、`egoist/mygo`、`modernc.org/sqlite`、
  `modelcontextprotocol/go-sdk`、`eino-contrib/jsonschema`、`bmatcuk/doublestar/v4`、`dustin/go-humanize`、
  `golang.org/x/sys`、**`open-dingtalk/dingtalk-stream-sdk-go v0.9.1`**（钉钉 Stream 长连接）
- indirect（不直接使用）：`gorilla/websocket`（钉钉 SDK 的传输依赖）等
- 可选：`gh`（GitHub 来源；执行者也用同一个 gh 登录）；飞书应用凭据；钉钉的 `dws` CLI（默认 transport）或官方 Stream 凭据（不启用则不监听）

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

**我发了一句话，只看到「已答复」/没有工单，是不是失败了？**
不是。面板消息一定会得到一个结果：要么建 Ticket 派发（真活），要么超级Agent 在同一条消息下直接回答
（列出工作区，或说明这次没有派发的原因和下一步）。回答是确定性文本，不调用模型；想强制派发就选一个工作区
用「＋ 新建会话」，它总是派发。

**IM 显示「未启用」/检查失败怎么办？**
设置›来源打开渠道开关并填凭据；飞书点「检查通道」，401/403 时按提示去 `open.feishu.cn/app` 修应用权限；
钉钉默认需要本机 `dws`：`dws auth login` 后 `dws auth status --json` 应为 `loggedIn:true`，
或把 `dingtalk_dws_cmd` 指到绝对路径/包装脚本。禁用渠道=未监听，不会自动 fallback。

**web 工具会不会访问内网？**
不会。`web_fetch` 只允许 http(s) 且拒绝 localhost/私网（含解析后的地址），10s / 2MiB 上限；
`web_search` 默认 DuckDuckGo HTML、前 5 条，可用 `execution.web_search_url` 换端点。

## 已知限制（诚实清单）

- macOS 包**未签名/未公证**，首次打开需右键 → 打开；`.dmg`、Debian 包、PKGBUILD、自动更新均未实现。
- Windows 只有 zip + `install.ps1`，没有 MSI/NSIS 安装器；CI 只交叉编译与查体积，不产包。
- **工作区/会话页已有，但只能新建**：不能重命名、删除工作区，也不能重命名/删除会话或改根目录；
  会话列表只投影每个工作区最近 50 条（含已结束）。原生目录选择器在无显示器的 CI 里不可用（有测试 seam）。
- **IM 需要外部条件**：飞书要 app id/secret/chats；钉钉默认要本机安装并登录 `dws` CLI（`dws auth login`），
  或用 `dingtalk_transport=stream` 填 app key/secret/robot code。设置›来源里启用/检查/授权；命令行 flag 只覆盖凭据。
  飞书只有 HTTP 长轮询（无 WS/webhook）；两者都**不下载图片/文件**（只保留引用）；未启用=未监听。
- **钉钉 Stream 的 UI 不一致（owner: fix-ui2）**：runtime 已能构建官方 Stream 驱动（`internal/runtime/im.go:394-398`），
  但 `internal/view/channels.go:44-66` 仍把该选项标成「未实现」、面板不可选；DWS 默认路径不受影响。
- 设置页**来源组**已能开关/配置 IM 渠道、检查/授权，但**没有 Source 实例表**：GitHub 仓库/登录仍只能
  `-repo/-gh-login` 或 `setup` 配置；**评估组**只读，规则/admission/accept mode 不可编辑。
- **系统钥匙串未实现**：API Key 明文存 SQLite（0600）；worker 的 shell 环境已剥离 `*KEY*/*TOKEN*/*SECRET*/*PASSWORD*`（`internal/agent/tools.go` 的 `scrubEnv`）。
- 提示词：保存时校验未知变量/占位符/超长，历史每层最近 20 版已记录在 store（`prompt.history.<layer>`），
  面板展示"内置默认值已更新"提示，但还没有历史列表页。
- 领域类型 `domain.Thread`/`domain.UserError` 未落地；无 `steer/inject`；`Runner.Resume` 已删除（不再有生产未用的恢复 API）。
- 没有开机自启/休眠唤醒（交给 systemd/launchd）。
- worker 的 bash 子进程与 prepare 脚本、提示词拿到同一组 scrub 后工单变量（`ZEOKEEL_WORKDIR/TICKET_DIR/TICKET/TICKET_NAME/REPO`）：prepare 走 `runtime/prepare.go` 的 env，bash 走 `agent.Config.Env`（`internal/agent/agent.go:52-56`，由 `runtime/worker.go:429` 的 `executionVars` 填充）——凭证类变量在三处都已剥离。
- `execution.prepare` 在 **execution root（工单目录的父目录，`cfg.Root`）** 执行，工作区根由 `$ZEOKEEL_WORKDIR` 提供；面板 Help 文案已按同一口径写明。
- **shell 工具不是安全边界**：它能执行的命令可以读取本机任何可读文件（包括其它工单的工作树）。
  单机单人使用没问题；多人共机或处理他人数据时，请用独立系统账号或容器隔离（设计 §5.5 已写明）。

## License

MIT License. See [LICENSE](LICENSE).
