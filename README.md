# ZeoKeel Super Agent

你的 AI 同事：从聊天里接活，自己研判该不该做、该派给哪个工作区，干完把交付物和结论送回原处。
单人模式、单机、单二进制、原生窗口——**不是又一个聊天框**。

> **当前状态：`1.0.0-rc.1` 候选。** 机械闸门（build/test/vet/zero-cgo/(CGO=1) `-race`/package/release-check）
> 已在 2026-11-03 最终快照全绿。**未完成的是产品面**：打包只有 tar/zip（无 `.dmg`/deb/安装器）、
> 系统钥匙串未做（API Key 明文存库）、设置页规则/admission/Source 实例仍只读、飞书只做 HTTP 长轮询（无 WS）、无 IM 媒体下载、
> `steer/inject` 与 `domain.Thread`/`UserError` 未落地。`VERSION` 是否升 1.0.0 由发布负责人按 [RELEASE.md](RELEASE.md) F 组决定。

## 当前状态（2026-11-03，Wave 2 之后）

| 层 | 已实现（代码证据见设计 §13） | 未完成 / 限制 |
| --- | --- | --- |
| 领域与授权 | Deterministic gate（Strength/Basis）、admission table（三格 × 九种）、bot list、团队规则（Rules）、accept mode、Mandate/Policy version、Proposal 审批、两阶段 Eval（`Receive` 入队、`EvaluateDue` 按 Thread 批量判定，不占入口锁）、Input/Delivery、PR/Issue 生命周期（`follow` 工具 + `CloseThread → ThreadClosed`：ready 等跟随 PR 关闭，merged/closed 结束工单）、followup owners、hand-back、revalidate、pause/resume/stop/retry、`AnswerAttention`、投递三段式（delivered/read/turnEnded） | 领域仍未搬 `Thread`/`UserError` 类型；Eval 的 deadline/暂停细节仍在 spec/代码之间演进 |
| 执行 | 每 Ticket 一个 Worker 会话 + inbox（未投递/未读 Input、上下文无关权威标注）；eino `filesystem` 工具、`backgroundtask`（`job_output`/`job_kill`）、`patchtoolcalls`、`reduction`、`summarization`、`subagent`；skills/MCP/定时/目标；崩溃恢复（中断 note + 新 Assignment + `patchtoolcalls` 补悬空调用）；**`execution.prepare` 工单准备**（`sh -c`、10 分钟、scrub 后的 env + `ZEOKEEL_*`、失败即失败不自动重试、提示词展示变量）；**30 分钟后台作业规则**（`execution.background_timeout` 默认 1800s，到点 kill + `ReportWaiting` 含 job id/最后输出、不唤醒；子智能体不适用） | `Runner.Resume` 已删除（恢复只走 `RecoverAssignments` + `patchtoolcalls`）；无 steer/inject；worker bash 子进程获得与 prepare/提示词相同的 scrub 后 `ZEOKEEL_*` 变量（`ZEOKEEL_WORKDIR/TICKET_DIR/TICKET/TICKET_NAME/REPO`） |
| 来源 | GitHub（notifications + Issue/PR 评论；配置结构支持 receive-all/CI，但 CLI/面板未暴露）；**飞书/钉钉已接线**（`-feishu-*/-dingtalk-*` flag 或 设置›来源 的 8 个字段；钉钉走官方 Stream 长连接、飞书走 HTTP 长轮询（无 WS）、均不下载媒体；未配置=不监听）；本地面板；定时任务；Source `Kind`/registry/配置/游标持久化/Channel 抽象完整 | 没有 Source 实例表/面板“添加来源”：GitHub 仍需 `-repo/-gh-login` 启动；Source 实例/规则/admission/accept mode 不能从 UI 编辑 |
| 面板 | 七页（总览/消息/来源/触发/评估/执行/设置）+ 首次向导（设置落库）；待办行内动作（批准/拒绝/回答/重试/停止）；暂停/恢复执行；Ticket 详情（Mandate/Inputs/Assignments/Follows/Artifacts/Delivery/Attention）；名词表（⌘/ 或状态栏 `?`）；⌘K 搜索；提示词编辑器（6 层：Router/Worker/规则说明 + 团队三段；校验未知变量/保留占位符/超长拒绝/与默认相同即清除；研判提示每次 Eval 热更新）；来源组可编辑 IM 凭据；执行组含 prepare / background_timeout；部署组只读事实（日志/备份/schema/status 码） | 设置页**评估组**仍是“等待运行时投影”（规则/admission/accept mode 不可编辑）；`ui/overview` 仍有 demo 数据路径只供测试 |
| 运维 | `zeokeel status/check/setup/login/backup/archive/stop`（status 退出码 0/1/2/3；stop 写 STOPPED 标记）；`-headless` 真服务模式（SIGINT/SIGTERM 排空后按 006 退出码退出）；slog JSON 日志 `<stateDir>/logs/zeokeel.log` 按天轮转保留 14 天；进程内 supervisor（每 60s 检查 health/launch 队列/卡住的 Assignment，连续 3 次失败写 error）；state dir 单实例锁（flock/LockFileEx，在 store 打开时即拿，早于 recovery）；systemd/launchd 样例；`scripts/watchdog-check.sh`（006 小时巡检语义） | 无开机自启/休眠唤醒/自动更新（交给 systemd/launchd）；无 Feishu WS、无 IM 媒体下载；无设置页 Source 实例/规则编辑 |

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
| `-feishu-*` / `-dingtalk-*` / `-im-self` | IM 入口（app id/secret/chats、app key/secret/robot、机器人账号）；留空不监听 |
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

## 依赖

- 必需：Go 1.27.3+（构建时）；运行时无 Node、无浏览器、无 gcc、无数据库服务
- 直接依赖（go.mod）：`cloudwego/eino` 全家桶、`eino-ext` openai、`egoist/mygo`、`modernc.org/sqlite`、
  `modelcontextprotocol/go-sdk`、`eino-contrib/jsonschema`、`bmatcuk/doublestar/v4`、`dustin/go-humanize`、
  `golang.org/x/sys`、**`open-dingtalk/dingtalk-stream-sdk-go v0.9.1`**（钉钉 Stream 长连接）
- indirect（不直接使用）：`gorilla/websocket`（钉钉 SDK 的传输依赖）等
- 可选：`gh`（GitHub 来源；执行者也用同一个 gh 登录）；飞书/钉钉应用凭据（不配置则不监听）

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

## 已知限制（诚实清单）

- macOS 包**未签名/未公证**，首次打开需右键 → 打开；`.dmg`、Debian 包、PKGBUILD、自动更新均未实现。
- Windows 只有 zip + `install.ps1`，没有 MSI/NSIS 安装器；CI 只交叉编译与查体积，不产包。
- **IM 已接线但需要凭据**：`-feishu-app-id/-feishu-app-secret/-feishu-chats`、`-dingtalk-app-key/-dingtalk-app-secret/-dingtalk-robot`、`-im-self`，或设置›来源的 8 个字段（保存在本机 SQLite）。
  传输：**钉钉走官方 Stream SDK 长连接**；**飞书只做 HTTP 历史接口长轮询（无 WS）**；两者都**不下载图片/文件**（只保留引用）。未配置时不监听。
- 设置页的 **来源/评估** 两组还不能编辑 Source 实例、规则、admission 表、accept mode（只显示"等待运行时投影"）；
  来源只能通过 `-repo/-gh-login` 或 `setup` 配置。
- **系统钥匙串未实现**：API Key 明文存 SQLite（0600）；worker 的 shell 环境已剥离 `*KEY*/*TOKEN*/*SECRET*/*PASSWORD*`（`internal/agent/tools.go` 的 `scrubEnv`）。
- 设置页**评估组**只显示只读事实（规则/admission/accept mode 不能编辑）；GitHub Source 实例仍只能 `-repo/-gh-login`。
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
