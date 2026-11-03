# 运维 Runbook（ZeoKeel Super Agent）

> **状态：2026-11-03 最终快照。** 本文命令均存在；`status`/`stop`/`backup`/`archive`/`-headless` 已本机实测。
> 退出码唯一来源：`internal/runtime/status.go`（原型 006:113-124）。仍不可用的条目在 §8 明写，不用 pending 占位。

## 1. 可用的运维手段（今天）

| 能力 | 命令/事实 | 证据 |
| --- | --- | --- |
| 启动桌面应用 | `zeokeel`（窗口 + 托盘 + runtime；关窗 = 隐藏） | `cmd/zeokeel/main.go`（`ui.Run` 分支） |
| 服务模式 | `zeokeel -headless`（无窗口；SIGINT/SIGTERM 排空后按 0/1/2/3 退出） | `cmd/zeokeel/service.go:20`；`cmd/zeokeel/main.go` signal 分支 |
| 状态 | `zeokeel status [--json]`；可在应用运行时执行（只读 inspect，不拿写锁） | `cmd/zeokeel/cli.go:76`；`internal/runtime/status.go:137` |
| 启动前自检 | `zeokeel check [--json]`（全新安装返回 3=等待 setup） | `cmd/zeokeel/cli.go:143` |
| 首次配置（非交互） | `zeokeel setup --model ... --workspace ... --root ... [--people ...]` | `cmd/zeokeel/cli.go:216` |
| 登录检查 | `zeokeel login [--gh gh]`（只检查 GitHub CLI）；IM 在设置›来源按渠道开关/配置/检查/授权（详见 `design/im-channels.md`） | `cmd/zeokeel/cli.go`；`internal/ui/channels.go:134-230`；`internal/runtime/im.go:588` |
| 手动停 | `zeokeel stop [--pid N] [--no-signal]`：写 `<stateDir>/STOPPED`（巡检不自动重启）；`status` 在“手动停且未运行”时返回 0；启动时清标记 | `cmd/zeokeel/cli.go:354`；`internal/runtime/status.go:111-128`；`internal/store` 启动清标记 |
| 备份 | `zeokeel backup [--out FILE]`（`VACUUM INTO`；应用运行时拒绝） | `cmd/zeokeel/ops.go:46` |
| 归档 | `zeokeel archive [--before 180d] [--dry-run]` → `archive.db` | `cmd/zeokeel/ops.go:83` |
| 单实例 | state dir 文件锁（flock / LockFileEx），`store.Open` 时即拿；第二个写者得到"另一个 zeokeel 正在使用" | `internal/store/lock.go`；`cmd/zeokeel/ops.go:26` |
| 日志 | `<stateDir>/logs/zeokeel.log`（slog JSON，按天轮转为 `zeokeel-YYYY-MM-DD.log`，保留 14 天，0600） | `cmd/zeokeel/logging.go:27` |
| 进程内监督 | `-headless` 每 60s：health 表 / launch 队列失败 / 运行超过 6h 的 Assignment；连续 3 次失败把 runtime health 标 error | `internal/runtime/supervisor.go:105` |
| 外部巡检 | `scripts/watchdog-check.sh`（006:133-141 语义，见 §7） | `scripts/watchdog-check.sh` |
| 桌面集成 | `packaging/systemd/zeokeel.service`、`packaging/launchd/com.zeokeel.agent.plist` | 见 §3/§4 |

## 2. `zeokeel status` 退出码（0/1/2/3）

| 退出码 | 含义 | 重启能否修复 | 外部动作 |
| --- | --- | --- | --- |
| 0 | 正常运行（有模型、有工作区、无组件错误） | — | 什么都不做 |
| 1 | 没在跑 / 跑了两个 / 不响应 / runtime 报错 | **能** | 允许重启（`systemctl restart` / `launchctl kickstart -k`） |
| 2 | 在跑，但组件（Source、sync、launch 队列、schema 新于 build 等）报错 | **不能** | 上报 `errors` 明细，不重启 |
| 3 | 在跑但等待 setup（缺模型/缺工作区/首次配置未完成） | **不能** | 上报 `setup` 缺什么，不重启 |

`status --json` 的真实形状（实测）：

```json
{"stateDir":"/root/.zeokeel","dbPath":"/root/.zeokeel/zeokeel.db",
 "running":true,"manually_stopped":false,
 "schemaVersion":3,"schemaKnown":true,"buildSchema":3,
 "health":[{"part":"effect:launch","state":"ready"}],
 "errors":null,
 "setup":["还没有配置模型"],
 "load":{"running":0,"queued":0,"maxWorkers":0},"ok":false}
```

**手动停（`zeokeel stop` / STOPPED 标记）**：
- `zeokeel stop` 在 `<stateDir>/STOPPED` 写一个 UTC 时间戳（0600），`status --json` 的 `manually_stopped` 变 `true`。
- `status` 的退出码顺序：手动停且未运行 → **0**（"人让它停的，重启无意义"）；否则组件错误 2 / 未运行 1 / 等 setup 3。
- 巡检脚本看到 `manually_stopped=true` 时**不重启**；下次手动启动（GUI/headless）清除 `STOPPED`。
- `stop` 可选 `--pid N` 向运行中的进程发 SIGTERM（桌面版没有管理 HTTP API，不给 pid 时提示用托盘退出）。
- `--respect-stop` 没有独立 flag；语义由"STOPPED 标记 + 巡检判断"承担。

## 3. systemd（Linux，用户级）

样例已与真实 `-headless`/`status` 对齐：`packaging/systemd/zeokeel.service`。

```sh
mkdir -p ~/.config/systemd/user
cp packaging/systemd/zeokeel.service ~/.config/systemd/user/
systemctl --user daemon-reload
systemctl --user enable --now zeokeel.service
systemctl --user status zeokeel.service
zeokeel status            # 0 正常 / 1 可重启修复 / 2 组件错误 / 3 等待 setup
```

关键点：`Restart=on-failure` 会在退出码非 0 时重启，**这对 2/3 是错的**。
所以单元只负责"进程退出后重拉"，"该不该重启"交给外部巡检（§7，006:133-141 语义）。
单元里的 `ExecStart=%h/.local/bin/zeokeel -state-dir=%h/.zeokeel --headless`
使用本机已有的 flag（`-headless` 是真服务模式，不再是 demo）。

## 4. launchd（macOS，用户级）

样例：`packaging/launchd/com.zeokeel.agent.plist`（`ProgramArguments` 已带 `-state-dir`）。

```sh
cp packaging/launchd/com.zeokeel.agent.plist ~/Library/LaunchAgents/
launchctl load ~/Library/LaunchAgents/com.zeokeel.agent.plist
zeokeel status
```

`KeepAlive` 故意为 `false`：`true` 会在退出码 2/3（人/配置问题）时疯狂重拉。
需要重启时由巡检脚本决定是否 `launchctl kickstart -k`。
plist 里的 `/usr/local/bin/zeokeel` 与 `/Users/CHANGEME/.zeokeel` 是占位，安装时替换。

## 5. 日志与排障

- 日志：`<stateDir>/logs/zeokeel.log`（GUI 与 `-headless` 都写；`-demo`/`-pipeline` 只写 stderr）。
  按本地日切分、保留 14 天、权限 0600；打开文件失败时回退 stderr（不会因为日志打不开而丢窗口）。
- 进程内 supervisor 每 60s 写 `health(entry=runtime)`；`status` 读同一张表，所以"巡检看到了什么"与
  "运维命令报告什么"是同一事实。
- 崩溃恢复：启动时 `RecoverAssignments` 把中断的 Assignment 写成 note Input、重开 Assignment 并唤醒；
  eino `patchtoolcalls` 补悬空 tool_call（不重放坏历史）。证据：`internal/runtime/recovery.go:21`、
  `internal/agent/engine_adk.go:118`。

| 症状 | 原因 / 处理 | 现状 |
| --- | --- | --- |
| 启动后没反应/闪退 | 终端启动看 stderr；GUI 看 `<stateDir>/logs/zeokeel.log` | ✅ 有文件日志 |
| 模型未配置，工单一直"排队" | 工单会标 blocked 并说明"还没有配置模型"；`zeokeel setup` 或在向导里填 | ✅ |
| 同事的消息没有执行 | 检查允许名单（`来源:账号`）、Source 是否启用、是否暂停；GitHub 编辑 Issue 正文追加 @ 不会被收到，发新评论 | ✅ |
| 总览里的问题答不了 | 现在可以：待办行内"回答"提交后写回 Input 并唤醒工单 | ✅ |
| `exit 1/2/3` 含义 | 见 §2；`status --json` 的 `errors`/`setup` 给明细 | ✅ |
| 数据库被更新版本迁移过，旧版本打不开 | `store.Open` 返回 `ErrNewerSchema`；`status` 归为 exit 2（组件错误，不重启）；升级前先 `zeokeel backup` | ✅ |
| prepare 失败/工单直接失败 | 看回执里的命令输出；确认脚本创建了 `$ZEOKEEL_TICKET_DIR`、cwd 是 execution root（工单目录父目录）、10 分钟内完成；失败不会自动重试 | ✅ |
| 后台作业超时后工单变 waiting | 30 分钟规则（`execution.background_timeout`）：作业被 kill，工单等下一次输入；子智能体不适用 | ✅ |
| PR 合并了但工单还 waiting/没结束 | 确认 Worker 调过 `follow`（消息流有"已关注 Thread"）且绑对 PR；`CloseThread` 只在 GitHub 轮询到 merged/closed 时触发 | ✅ |
| 重启后工单还停在"执行中" | 启动时 `RecoverAssignments` 会恢复（中断 note + 新 Assignment）；超过 6h 未完成会被 supervisor 点名 | ✅ |
| 飞书/钉钉没反应 | 先看启用状态（三态：显式/缺省推导）与来源页 error 行；飞书 transport=ws（官方长连接）或 longpoll；钉钉 DWS 需 `dws auth status --format json` 为 loggedIn；配置不全/dws 缺失都会给出可操作 detail | ✅（真实凭据 E2E 未验证） |
| 想“人主动停、别被自动拉起” | `zeokeel stop`（可加 `--pid`）；`status` 会返回 0 且 `manually_stopped=true`，巡检不会重启；下次启动清标记 | ✅ |
| 面板计数 | 执行中/今日/24h/队列已真实投影（`internal/runtime/runtime.go:152-168`） | ✅ |

## 6. 备份、恢复、归档

```sh
# 备份：先退出应用（或确认 status 显示 running=false），VACUUM INTO 生成单文件副本
zeokeel backup --state-dir ~/.zeokeel --out ~/backups/zeokeel-$(date +%F).db

# 恢复：停应用，把备份文件复制回 <stateDir>/zeokeel.db，再启动（旧文件会被 WAL 重建）
cp ~/backups/zeokeel-2026-11-03.db ~/.zeokeel/zeokeel.db
zeokeel check && zeokeel status

# 归档：把结束超过 180 天的工单/Trigger/Disposition 移到 archive.db（同一事务）
zeokeel archive --state-dir ~/.zeokeel --before 180d --dry-run
zeokeel archive --state-dir ~/.zeokeel --before 180d
```

- 备份/归档命令在应用运行时**拒绝执行**（state dir 写锁被占用），返回"可重启修复"类错误。
- 备份文件 0600。**数据库里有明文 API Key**（ADR-12 钥匙串未实现），备份请按密钥保管，不要进共享盘/仓库。
- 归档不删除 outbox 回执（幂等键保留），所以归档后重复投递仍会被挡住。

## 6.5 提示词层版本历史

- 提示词保存路径会调用 `recordPromptMeta`（`internal/runtime/pages.go:557`）：
  每层最近 **20 版**写入 kv 的 `prompt.history.<layer>`（JSON：时间/设置 revision/content/hash），
  首次覆盖时写 `prompt.base.<layer>` = 当时内置默认值的 hash（"Default updated" 判定用）。
- 提示词层：`router`、`worker`、`eval_rules` 加团队三段 `communication`/`scope`/`delivery`。
- 保存校验：未知层/未知变量/缺必需占位符/超过 20000 字 → 拒绝（不截断）；与默认相同 → 清除覆盖；
  与当前相同 → 不写、不产生历史。
- `zeokeel backup` 会连 kv 一起备份，因此历史/base-hash 随备份保留；面板当前展示"内置默认值已更新"提示，
  **还没有历史列表页**（RELEASE F-1）。
- 运维注意：改提示词后研判器下一次 Eval 即生效（`internal/runtime/prompts.go` + `cmd/zeokeel/assess.go` 的 hotAssessor），
  Worker 从下一次运行生效。

## 6.6 回复格式（按频道）

出站回复不是同一段文本发所有地方（`internal/runtime/reply.go`）：

| 频道 | 格式 | 证据 |
| --- | --- | --- |
| 飞书/钉钉 | 中文短回复，结论先行，**≤100 runes** | `reply.go:27`（`imMaxRunes`）、`composeIMReply` |
| GitHub | 结构化：背景 / 改动 / 验证 / 交付物 | `reply.go` 的 `composeGitHubReply` |
| 本地面板 | 简短结论 + 交付物列表 | `composeLocalReply` |

`ReplyPlan` 决定"能不能直接回给同事"：notify/both/none 的回复被标记 `Held`，只进面板不直发
（`deliverReply`，`reply.go:213-245`）；只有 direct 才进 Outbox。没有渠道驱动时 Outbox 返回错误
（不再假报 sent），失败会在面板/health 可见。

## 6.7 工单准备（`execution.prepare`）与执行变量

设置›执行 → 工单准备命令；首次 Assignment 且 `<stateDir>/work/ticket-<N>` 不存在时执行一次（`internal/runtime/prepare.go:68`）：

| 项 | 行为 |
| --- | --- |
| 运行方式 | `sh -c <script>`，cwd = **execution root（工单目录的父目录，`cfg.Root`，默认 `<stateDir>/work`）**；工作区根在 `$ZEOKEEL_WORKDIR` |
| 超时 | 10 分钟（`prepareTimeout`，`prepare.go:27`），超时按失败处理 |
| 环境 | 先 `scrubEnv` 去掉 `*KEY*/*TOKEN*/*SECRET*/*PASSWORD*`，再注入：`ZEOKEEL_WORKDIR`（工作区根）、`ZEOKEEL_TICKET_DIR`（本工单目录，**脚本必须创建它**）、`ZEOKEEL_TICKET`（编号）、`ZEOKEEL_TICKET_NAME`（`ticket-N`）、`ZEOKEEL_REPO`（工作区 remote 或名字） |
| 结果 | 命令失败或没有创建 `$ZEOKEEL_TICKET_DIR` → 工单失败并回执请求人；**不自动重试**（人的新消息或手动 retry 才会再试） |
| 提示词 | 同一组变量作为"工单事实"展示给 Worker（`runtime/prepare.go:123`、`worker.go:517`） |
| 面板 | 设置›执行有该字段；Help 与行为一致（execution root = 工单目录父目录） |

> **worker bash 同样拿到这组变量**：`agent.Config.Env`（`internal/agent/agent.go:52-56`）由 runtime 用 `executionVars` 填充
> （`internal/runtime/worker.go:429`），`buildShellEnv` 先 `scrubEnv` 再注入（`internal/agent/tools.go:236-248`）。
> 因此 prepare、提示词、bash 三处的 `ZEOKEEL_*` 值一致，凭证类变量都不注入。

## 6.8 后台作业 30 分钟规则

- 前台命令超过 5s 转后台作业（`internal/agent/jobs.go` 的 `defaultBackgroundAfter`），会话可以继续。
- 作业仍运行但工单回合结束时，Worker 报 `waiting`（job id 列表 + 上限）；`watchJob`（`internal/runtime/background.go:89`）继续等：
  - 正常结束 → 结果作为 Input 写回工单并唤醒下一轮；
  - 超过 `execution.background_timeout`（默认 **1800s**；30s–24h）→ `run.Jobs().Cancel`，记 `ReportWaiting`（含 job id 与最后输出），**不唤醒 session**；
  - 子智能体（eino subagent）不是 shell job，不受该规则约束。
- 设置›执行有 `background_timeout`（`internal/runtime/pages.go:478`）；测试可用 `SetBackgroundLimit` 覆盖。

## 6.9 PR / Issue 生命周期（follows）

- GitHub Source 轮询到 PR/Issue 的 `state=merged|closed` 时（`cmd/zeokeel/github.go:76`）：
  `runtime.CloseThread`（`internal/runtime/github_lifecycle.go:39`）→ `Persistence.ThreadClosed` → engine 结束跟随该 Thread 的工单，
  outcome 为 `merged`/`closed`（`internal/engine/lifecycle.go:368`）。
- Worker 在打开 PR 后调用 `follow` 工具（`internal/runtime/artifacts.go:102`，`own`/`context`）→ `BindThread/BindFollow`：
  - `own=true`（默认）：该 PR 未关闭时 `report(ready)` **不结束工单**（保持等待）；PR 关闭后按 merged/closed 结束。
  - `context`：只订阅上下文，不改变结束规则。
- 排障：PR 已合并但工单没结束，先看消息流有没有 "已关注 Thread …"，再看 `Ticket.Follows` 是否绑到了正确的 `owner/repo#N`
  （`domain.ThreadKey` 用 `<scope>|github|<id>`）；重复关闭事件是幂等的。

## 6.10 IM 渠道（飞书 / 钉钉）与审批队列

完整契约、DWS 命令形状与故障矩阵见 **[im-channels.md](im-channels.md)**；这里只列运维动作：

1. **审批优先（首要注意）**：飞书/钉钉的每条消息（包括操作员自己的）先变成**一个待批准项**；
   总览「待你批准」逐条 Approve/Reject。**批准才建 Ticket 并启动 Worker；拒绝什么都不留**；
   来源页显示「待审批 N」。重复投递只产生一个待批项。没有"IM 消息自动开工"的路径（除非部署显式打开
   Policy 的 `AutoApproveIM`，默认关闭）。
2. **启用/停用（三态）**：设置›来源每渠道有开关。显式 1/0 优先；没写过开关时按凭据/flags 推导；
   flag-only 部署由启动 bootstrap 补写启用。禁用=停止监听、来源"未启用"、health 不假装在线。
   保存后只重启 enable/transport/凭据/self/profile 变化的渠道（`internal/runtime/im.go:345-470`）。
3. **飞书**：**新装默认 ws（官方 SDK 长连接，推荐）**；旧凭据-only 配置在迁移时被 pin 到 longpoll，保持原接收路径；longpoll 是显式 fallback（需要 `feishu_chats`）。面板 Options 只有 `ws`/`longpoll`，没有 webhook。
   填 app id/secret 后点「检查通道」（换 tenant token）；401/403 时按提示打开 `https://open.feishu.cn/app`
   修应用权限/发布版本。没有 webhook，不下载媒体。WS 由 SDK 自带重连；longpoll 的 cursor 存 kv
   （`im.cursor.<channel>.<transport>`），整批接受后才推进，切换 transport 会清 cursor。
4. **钉钉（默认 DWS，个人 OAuth，不是 bot/app）**：本机安装并登录 DWS：
   - `dws auth status --format json` → `loggedIn:true`；未登录 `dws auth login`（headless 用 `--device`）；
   - 接收是**长驻进程**：`dws event +listen-im --kind at-me|group|all-direct|all-group [--chat-query q] [--profile p] --flatten -f ndjson`
     （无 poll/cursor；断线 1s→30s 重连，event id 去重）；
   - 发送：`dws chat +messages-send --as user --group <cid> --text <t> --format json`（单聊用 `--open-dingtalk-id`；
     **没有 idempotency-key**，靠文本尾部隐藏 marker；可 `--as bot --robot-code` 用 bot 身份）；
   - 短命令串行、30s 超时、不经 shell、stderr 脱敏；检查复用运行中的 DWS 后端；
   - **未安装/未登录** → 来源 error/auth + 可操作提示；不会自动 fallback 到 Stream。
   - **DWS 不用 app key/secret**；`dingtalk_app_key/app_secret/robot` 只属于 Stream（bot）模式。
5. **钉钉 Stream（bot）**：`dingtalk_transport=stream` + app key/secret/robot code；检查只验凭据齐全。
6. **排障**：`zeokeel status --json` 看 `im:feishu`/`im:dingtalk` 的 health（secret 已 redaction）；
   日志搜 `IM channel started` / `IM poll failed` / `IM driver failed` / `dws`；DWS 自身用
   `dws auth status --format json`。审批类问题看「待审批 N」与总览批准列表。

## 6.11 升级与迁移（IM / settings）

- **旧 IM 配置**：旧版本只有 `source.*_app_id/secret` 等凭据、没有 `*_enabled` 键。新版本启动时：
  读取层把旧 bool（`true/false`）解析为规范值；缺少 `*_enabled` 时按凭据/flags 推导；flag-only 部署由
  `BootstrapIMFlags` 补写启用。旧共享 `im_self` 会作为每渠道 self id 的回退/复制来源。
- **DWS/Stream 区分**：旧版只有 Stream，因此缺 `dingtalk_transport` 时若有 app key/secret/robot 证据则按 stream 处理；
  已有 `dingtalk_profile/listen_kind/chat_query/dws_cmd` 证据则按 dws 处理。升级不应把已工作的渠道切到不存在的 `dws`。
- **settings schema / 一次性迁移**：`LoadSettings` 在首次启动或 schema 旧时执行 `migrateSettings`（bool 归一为 `1/0`、
  旧 IM 凭据推导启用/transport（**旧飞书凭据-only pin longpoll**）、`im_self` 拆分），成功后写 `SettingsSchema` stamp（`internal/runtime/pages.go:678-707`、
  `internal/runtime/settingscodec.go:83`、`internal/store/settings.go:28-55`；测试 `TestLegacySettingsMigration`）。
  升级前仍建议备份 `zeokeel.db`；升级后检查设置›来源的启用状态与「检查通道」结果。
- **cursor**：per-channel+transport 命名空间；旧 `im.cursor.<channel>` 会被读取一次以兼容升级；切换 transport 清 cursor。
- **secret**：设置页不回显（留空=保留）；`status --json`/Inspect/health detail 的 secret 已 redaction。

## 7. 外部巡检（006 小时检查的等价物）

`scripts/watchdog-check.sh`：

```sh
ZEOKEEL_BIN=/usr/local/bin/zeokeel scripts/watchdog-check.sh
# 或带重启动作（仅 exit 1 时会执行）：
ZEOKEEL_BIN=/usr/local/bin/zeokeel scripts/watchdog-check.sh --restart-cmd "systemctl --user restart zeokeel.service"
```

语义（与 006:133-141 一致）：`status` 0 → 静默退出 0；1 → 可重启（给了 `--restart-cmd` 才重启，
重启后仍非 0 则上报并返回该码）；2/3 → 上报且**不重启**；`manually_stopped=true` → 不动。
脚本会自己识别 `status` 是否可用：早期构建没有子命令时打印说明并退出 0，不会假装监督成功。

## 8. 未实现/未覆盖（诚实清单）

- 开机自启、休眠唤醒补跑、自动更新：没有内置实现；用 systemd/launchd 的开机启动 + 系统唤醒策略替代（文档化，不假装内置）。
- IM：飞书 webhook 与媒体下载未实现（面板只提供 ws/longpoll）；DWS 依赖外部 CLI，真实 CLI 版本/登录/租户 E2E 未验证；
  官方 Stream 是 bot 模式（app key/secret/robot）。
- 工作区删除只允许空工作区（有工单拒绝）；会话不能重命名/删除；每工作区会话只投影最近 50 条。
- `ui.font_scale` 已投影并可在设置›部署编辑（0.9/1.0/1.1，保存立即生效）。
- 升级迁移已接线（`LoadSettings` 的 `migrateSettings` + schema stamp）；历史旧文件仍按读取层兼容。
- 监控指标导出（Prometheus 等）：没有；只有 `status --json` 与日志。
- 设置页规则/admission/accept mode 与 GitHub Source 实例：不可编辑（只读事实或 CLI flag）。
- 系统钥匙串未实现（API Key 明文 SQLite 0600；读取侧已 redaction）。
- 多实例/多副本/远端状态：单机单写者（ADR-1/ADR-5），节点级 HA 不在范围内。
