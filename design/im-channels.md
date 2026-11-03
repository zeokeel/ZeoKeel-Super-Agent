# IM 渠道：飞书 / 钉钉（2026-11-04 最终状态）

> 规格来源：`audit/fixes/im-channel-spec.md` + `audit/design-review-r2.md`（P0-1/P0-4/P0-6/P0-7/P1-10/P1-11）。
> 本文写**已实现**行为；结论尽量给生产代码 file:line。行号随代码移动，以函数名为主。
> 产品策略：**IM 入站=审批优先**。任何 IM 消息（包括操作员自己的）都先变成一个待批准项；
> 批准才创建 Ticket，拒绝什么都不留。

## 0. 一句话

两个渠道各自有生命周期：飞书走官方 SDK 长连接（`larkws`，推荐；HTTP 长轮询保留为显式 fallback），
钉钉默认走本机 DWS CLI（**操作员个人 OAuth 身份**，长驻 NDJSON consumer，不是 bot/app；官方 Stream SDK 是 bot 模式的可选 transport）。
入站消息全部进入审批队列，`待审批 N` 是来源页的注意力项。

## 1. 审批优先（release policy）

- 所有 IM 来源（`feishu` / `dingtalk`）被 gate 判为可接受（`DecAccept`）时，Eval **不建 Ticket**：
  `internal/engine/evaluate.go:396-399` 把这些消息交给 `proposeIMLocked`（`:432-461`），**每条消息一个** `DecPropose`，
  ReplyPlan=`none`（不自动回复），不启动 Worker。
- 批准路径：`Runtime.Approve`（`internal/runtime/runtime.go:894-912`）→ `Engine/Persistence.Approve` 创建 Ticket 并唤醒；
  拒绝路径：`Reject`（`:915-930`）只留 Decision，不留 Ticket/Worker/回复。
- 逃生阀：Policy 的 `AutoApproveIM` 可关闭该策略（`internal/engine/gate.go:56-60`，默认 false）。
- 面板：总览「待你批准」（`internal/ui/overview.go:51-130`）；来源页每渠道 `待审批 N`
  （`internal/runtime/pages.go:101-111`）；health 也有「待审批」项（`internal/runtime/runtime.go:420`）。
- 重复投递：同一消息重发只产生一个待批项/一个 Ticket（内容派生 ID 幂等；验收 A7 `cmd/zeokeel/acceptance_test.go:207-232`）。
- 验收语义变化：A3 从“操作员请求直接执行”改为**“操作员自己的 IM 消息也要批准”**（`acceptance_test.go:129-160`）；
  A7 也走先审批后建单。

## 2. 配置模型（settings group `source`）

| key | 类型 | 默认/语义 | 说明 |
| --- | --- | --- | --- |
| `feishu_enabled` | switch/三态 | 未显式写时按凭据推导 | 显式 `1/0` 优先；缺省由 app id/secret 完整性推导（P0-4） |
| `feishu_transport` | select | **新装默认 `ws`**；`longpoll` 为显式 fallback（旧凭据-only 配置迁移时 pin longpoll） | ws=官方 larkws 长连接（推荐）；longpoll=HTTP 历史接口 + cursor。面板 Options 只有 `ws`/`longpoll`，无 webhook |
| `feishu_app_id` / `feishu_app_secret` | text/secret | — | 两种 transport 都使用 |
| `feishu_chats` | text | — | 长轮询时监听的会话 id；WS 由事件推送决定 |
| `feishu_self_id` | text | 回退 `im_self` | 飞书机器人自身 id（@ 判定） |
| `dingtalk_enabled` | switch/三态 | 未显式写时按证据推导 | DWS 需要操作员信号（profile/listen/chat filter/cmd）才推导为启用，避免新机器报“dws 未安装” |
| `dingtalk_transport` | select | `dws` / `stream` | dws=个人 OAuth CLI；stream=官方 Stream SDK（bot） |
| `dingtalk_dws_cmd` | text | `dws` | DWS 可执行文件/包装脚本 |
| `dingtalk_profile` | text | 空=CLI 当前 profile | DWS 多账号 |
| `dingtalk_listen_kind` | select | `at-me` | `at-me`/`group`/`all-direct`/`all-group`（`dws.go:63-68`） |
| `dingtalk_chat_query` | text | 空 | `--chat-query` 群过滤 |
| `dingtalk_bot_id` | text | 回退 `im_self` | Stream 模式的 bot 自身 id（@ 判定） |
| `dingtalk_app_key`/`app_secret`/`robot` | text/secret | — | **只属于 stream（bot）模式**；DWS 不用 app-key/secret |
| `im_self` | text | — | 旧版共用值；迁移为每渠道 id 的回退 |

代码：`internal/runtime/im.go:33-130`（`IMConfig`/`IMConfigFromSettings`，含显式 vs 推导三态）、`:186-230`（flag overlay）、
`internal/runtime/pages.go:468-500`（设置字段）。bool 编码统一在 `internal/runtime/settingscodec.go:1-70`：
读兼容 `1/0/true/false/yes/no/on/off`，写只写 `1/0`；flag-only 部署由 `BootstrapIMFlags`（`:186-230`，
在 `cmd/zeokeel/main.go:300`/`cmd/zeokeel/imwiring.go:91` 调用）补 `enabled`。
旧文件兼容：bool 解析在读取层完成（`settingscodec.go:67-74`、`im.go:120-130`），旧 `true/false` 不会被误读成“关”。

## 3. 飞书：官方长连接（推荐）+ HTTP 长轮询（fallback）

- **WS（推荐）**：`internal/im/feishu/ws.go:1-100` 用 `github.com/larksuite/oapi-sdk-go/v3 v3.12.2`
  （`go.mod:12`）的 `larkws.Client` + `OnP2MessageReceiveV1`；`Run(ctx)` 阻塞消费，SDK 自带重连；
  无 cursor，channel 停止时 `Close` 关连接（`internal/runtime/im.go:859-873` 构建）。事件经与长轮询相同的
  `im.RawEvent`/审批入口进入引擎。
- **longpoll（显式 fallback）**：`internal/im/feishu/transport.go` 用 `im/v1/messages` 游标增量轮询；
  cursor 以 `<channel>.<transport>` 命名空间持久化（`internal/runtime/im.go:1045-1075`），整批被接受后才推进；
  切换 transport 会清掉旧 cursor（`:500-505`）。
- 凭据检查：设置卡片「检查通道」→ `checkFeishuAuth`（`internal/runtime/dws.go`）：POST tenant_access_token；
  401/403 返回授权入口 `https://open.feishu.cn/app`。WS 模式检查的是 app id/secret 是否可用（事件订阅权限需在开放平台开通）。
- **新装**默认 transport 是 **ws**（`internal/runtime/im.go:87`、`internal/runtime/pages.go:474-476`）；**旧版本只有 Feishu 凭据、没有 transport 键时，迁移会 pin `longpoll`**，不让升级改变已工作的接收路径（`internal/runtime/settingscodec.go:101-106`）。面板 Options 只有 `ws`/`longpoll`（`internal/view/channels.go:45-51`），webhook 未实现。
- 媒体：不下载图片/文件，只保留 image_key/file_key 引用。

## 4. 钉钉：DWS（默认，个人 OAuth）+ 官方 Stream（可选，bot）

### 4.1 DWS 真实 CLI 契约

来源：`github.com/DingTalk-Real-AI/dingtalk-workspace-cli`（外部 CLI，不可 import；适配为“外部命令 + NDJSON 长连接”）。
契约实现在 `internal/im/dingtalk/dws.go:1-30`：

| 操作 | 命令 | 行为 |
| --- | --- | --- |
| 接收（长驻） | `dws event +listen-im --kind at-me\|group\|all-direct\|all-group [--chat-query <q>] [--profile <p>] --flatten -f ndjson` | stdout 每行一个事件 JSON；stderr 出现 `[event] ready` 才算就绪；**没有 poll/cursor** |
| 接收（底层 fallback） | `dws event consume <EventKey> --flatten -f ndjson` | `EventKey` 显式配置时使用 |
| 发送 | `dws chat +messages-send --as user ... --format json`（群 `--group <cid>`；单聊 `--open-dingtalk-id <id>`；`--text`/`--markdown`；可加 `--profile`） | **没有 `--idempotency-key`**；幂等靠文本尾部隐藏 marker（`markerForDWS`，`dws.go:286-330`） |
| 发送（bot） | `--as bot --robot-code <code> --groups <cid>` | 仅 Stream/bot 身份可用 |
| 授权检查 | `dws auth status --format json` | `loggedIn/account/profile/authUrl`；未登录→state `auth` + 登录命令 |
| 登录（人类/headless） | `dws auth login` / `dws auth login --device` | `LoginCommand` 由 `LoginHeadless` 决定（`dws.go:1024-1032`） |

工程纪律（`dws.go:21-28`）：
- **每渠道一个长驻 consumer**，断线由适配器按 **1s→30s** 退避重连（`:994-998`）；事件按 **event id 去重**（有界 seen 集合）。
- 短命令（auth/send）串行：`execMu` 保证同一时刻只起一个短命令子进程；长驻 consumer 不打断；
  profile 互斥的真实 CLI 可开 `PauseListenOnSend`（默认 false）。
- 永不经过 shell；30s 超时 + `WaitDelay` 杀子进程；stderr 脱敏后进 health（`Secrets`）。
- 生命周期：manager 每渠道一个 generation，`ApplyIMSettings` 串行化（`internal/runtime/im.go:344-420`），
  停止时 cancel → 等 `StopGrace` → `Close` driver → 注销 outbox sender（`:397-470`、`:749-760`）；
  `Stop` 幂等，main 退出会调用（`:397`）。
- 检查复用运行中的 DWS 后端，不另起客户端（`im.go:1152-1179`，`dwsBuilt` 计数用于测试）。
- 命令缺失/未登录：来源 `error`/`auth` + 可操作 detail（安装 dws、`dws auth login`、设置 `dingtalk_dws_cmd`），
  **不自动 fallback 到 stream**（transport 由用户选）。

### 4.2 Stream（bot）模式

`dingtalk_transport=stream` 时使用官方 Stream SDK（`internal/im/dingtalk/stream.go`、`driver.go`），
需要 app key/secret/robot code；这是 **bot 身份**，与 DWS 的个人 OAuth 身份不同。
检查只验凭据齐全（`checkDingStreamAuth`），真实连接由订阅验证。

## 5. 面板与状态

- 来源组每渠道一张卡片：三态启用（显式开关覆盖推导；卡片状态来自运行时投影）、transport、"检查通道"、
  未登录时"打开授权页"（mygo `Shell.OpenExternal`，只接受 http(s)）、凭据字段（secret 不回显，留空=保留）。
- 禁用=未监听，来源行显示"未启用"，health 不假装在线；运行中显示"连接中"→`ready`。
- 检查在后台 goroutine 执行（`internal/ui/channels.go:38-50`）；面板不持有 payload 密钥。
- 保存设置异步：UI 显示"保存中…"，runtime 在后台命令锁内完成写库/渠道重启；来源页看到"连接中"。
- 来源页 `待审批 N`；总览「待你批准」；批准/拒绝行内完成。

## 6. 秘密与诊断

- 设置页不回显 secret（`internal/runtime/pages.go:539-550`）；留空保存=保留旧值。
- `store.Inspect`/`status --json` 的 KV 与 health detail 走 redaction（`internal/store/redact.go:23-87`、
  `internal/store/inspect.go:46-117,231,260`）：`***` 保留"已配置/未配置"事实，不泄露值。
- IM 驱动错误/health detail 经 `redactIMDetail` 脱敏；DWS stderr 按 `Secrets` 列表脱敏。

## 7. 运维步骤

1. 飞书（推荐 WS）：开放平台建应用、开事件订阅/长连接权限；设置›来源填 app id/secret，transport 选 ws，启用，点检查。
   若网络/环境不适合长连接，选 longpoll（需要 `feishu_chats`，cursor 存在 kv）。
2. 钉钉 DWS：安装 `dws` → `dws auth login`（headless `--device`）→ `dws auth status --format json` 应为 `loggedIn:true`；
   设置›来源启用钉钉，transport=dws，必要时填 profile/listen kind/chat-query；点检查。
3. 钉钉 Stream（bot）：transport=stream，填 app key/secret/robot code，启用。
4. 收到消息后：到总览「待你批准」逐条批准/拒绝；批准才开工，拒绝什么都不留。
5. 排障：`status --json` 看 `im:feishu`/`im:dingtalk` health；日志搜 `IM channel started`/`IM poll failed`/
   `dws`；DWS 自身 `dws auth status --format json`。
6. 升级：旧配置的 `*_enabled` 缺省时由凭据/flag 推导；flag-only 部署由 `BootstrapIMFlags` 补写启用；
   旧共享 `im_self` 复制到每渠道 id（`settingscodec.go:106-130`）。`LoadSettings` 在首次启动/升级时真正执行 `migrateSettings`
   并写 `SettingsSchema` stamp（`internal/runtime/pages.go:678-707`；测试 `TestLegacySettingsMigration`）；失败不会静默写入半套值。

## 8. 已知边界

- 飞书 webhook 未实现（面板不再提供）；飞书媒体只保留引用。
- DWS 能力受 CLI 契约限制（无成员列表/历史/模板发送）；真实 CLI 版本/登录流程仍需真实租户验证。
- 审批逃生阀 `AutoApproveIM` 只给测试/嵌入方，默认关闭。
- `ui.font_scale`/删除工作区/详情 ID/迁移等曾登记为差异，已在 2026-11-04 微调修齐；见 README/设计 §13。

## 9. 代码证据索引

- 审批优先：`internal/engine/evaluate.go:396-461`；`internal/engine/gate.go:56-60`；`internal/runtime/runtime.go:894-930`；
  `cmd/zeokeel/acceptance_test.go:129-160,207-232`；`internal/runtime/im_approval_test.go:55-129`。
- 三态/迁移：`internal/runtime/im.go:33-130,186-248`；`internal/runtime/settingscodec.go:1-70,106-160,182-230`；
  `cmd/zeokeel/main.go:300`、`cmd/zeokeel/imwiring.go:91`。
- 飞书 WS/longpoll：`internal/im/feishu/ws.go:1-100`；`internal/im/feishu/driver.go:71,94-107`；
  `internal/runtime/im.go:180-205,500-505,859-873,1045-1075`；`go.mod:12`。
- DWS 契约/生命周期：`internal/im/dingtalk/dws.go:1-28,63-68,90-130,286-330,569-700,842-890,970-1032,1069-1240`；
  `internal/runtime/dws.go:1-20,130-205`；`internal/runtime/im.go:344-470,749-760,1152-1179`。
- 面板/来源：`internal/ui/channels.go:1-360`；`internal/ui/overview.go:51-130`；`internal/runtime/pages.go:101-111,468-500`。
- 秘密：`internal/runtime/pages.go:539-550`；`internal/store/redact.go:23-87`；`internal/store/inspect.go:46-117,231,260`。
