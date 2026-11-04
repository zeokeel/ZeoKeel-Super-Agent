# IM 渠道：飞书 / 钉钉（2026-11-04 rc.4，DSH 对齐）

> 规格来源：`audit/fixes/im-channel-spec.md` + `audit/design-review-r2.md`（P0-1/P0-4/P0-6/P0-7/P1-10/P1-11）。
> 本文写**已实现**行为；结论尽量给生产代码 file:line。行号随代码移动，以函数名为主。
> 产品策略（rc.4）：**admitted 的明确 IM 请求直接由 Eval 接单 → Ticket**；推断请求按 accept mode off/observe/auto
> （auto 需匹配规则）。**旧式审批/提案机制已彻底删除**：没有 Approve/Reject/proposal/`RequireApproval`；
> 旧 DB `propose` 行载入为 ignore（`legacyImport`）。收件箱 attention-only。

## 0. 一句话

两个渠道各自有生命周期：飞书走官方 SDK 长连接（`larkws`，推荐；HTTP 长轮询保留为显式 fallback），
钉钉默认走本机 DWS CLI（**操作员个人 OAuth 身份**，长驻 NDJSON consumer，不是 bot/app；官方 Stream SDK 是 bot 模式的可选 transport）。
明确请求直接进入 Eval 主链；**没有审批队列**，收件箱只处理任务问题/受阻/失败（attention-only）。
`待你决定 N` 仍是统一注意力计数；来源页每渠道的「连接」打开**连接向导**（只测填写的值、通过才保存）。

## 1. DSH 对齐：明确请求直接接单（无审批）

- **明确请求（默认路径）**：admitted 的来源（IM/GitHub）被 gate 判为 `DecAccept` 且 `Strength=explicit` 时，
  Eval **直接建 Ticket**；accept mode 对 explicit 不生效（002:95-101 的 DSH 主链）。
  代码：`internal/engine/evaluate.go:348-390`（explicit 分支跳过 accept mode，只有 `Kind==DecAccept` 才建单）。
- **推断请求**：`Strength != explicit` 时按 accept mode：
  - `off`：忽略（`ReasonAcceptModeOff`）；
  - `observe`：记录 `ReasonObserved`，不建单；
  - `auto`：必须至少匹配一条 enabled 规则（否则忽略 `ReasonNoRuleMatched`），才 `DecAccept`。
  代码：`internal/engine/evaluate.go:352-372`、`internal/engine/gate.go:52-56`。
- **审批已彻底删除（rc.4）**：没有 `proposal`/`DecPropose`/Approve/Reject/UndoReject/`RequireApproval`；
  Decision 只有 accept/stop/ignore/unavailable。旧 DB `propose` 行在 load 时转 `ignore` 并记 `legacyImport`；
  旧 `requireApproval` 字段被忽略（`internal/engine/persist.go:156-162`、`internal/domain/ticket.go:10-13`）。
- **收件箱 attention-only**：只显示任务问题/受阻/失败，不显示批准；历史 proposal 不再可批准/拒绝
  （`internal/ui/inbox.go:1-140`）。
- **重复投递**：同一消息重发仍只产生一个 Ticket（内容派生 ID 幂等；验收 A7 `cmd/zeokeel/acceptance_test.go:207-232`）。
- **验收语义**：A3 是“admitted 的操作员 IM 请求直接执行，不产生 proposal”（`acceptance_test.go:129-160`）；
  A7 重复投递只建一个 Ticket。

## 2. 配置模型（settings group `source`）

| key | 类型 | 默认/语义 | 说明 |
| --- | --- | --- | --- |
| `feishu_enabled` | switch/三态 | 未显式写时按凭据推导 | 显式 `1/0` 优先；缺省由 app id/secret 完整性推导（P0-4） |
| `feishu_transport` | select | **新装默认 `ws`**；`longpoll` 为显式 fallback（旧凭据-only 配置迁移时 pin longpoll） | ws=官方 larkws 长连接（推荐）；longpoll=HTTP 历史接口 + cursor。面板 Options 只有 `ws`/`longpoll`，无 webhook |
| `feishu_app_id` / `feishu_app_secret` | text/secret | — | 两种 transport 都使用 |
| `feishu_chats` | text | — | **ws：可选**（留空监听应用可见会话；填了只收这些）；**longpoll：必填**（只能轮询明确会话） |
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
  `im.RawEvent`/同一 Eval 入口进入引擎。
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

## 5. 面板与状态（连接向导）

- 来源页每渠道卡片显示三态启用与运行时连接状态；点「连接」打开向导（`ui/channelwizard.go`）。
- 向导只显示**所选 transport 的字段**（运行时通过 `ChannelFieldMetadata` 发布 `channel|transport|字段|必填|默认|选项|label|help`）；
  必填项就地校验；「检查连接」用**填写的值**测试，不保存；通过后「保存并启用」，也可显式「仍然保存（未验证）」。
- DWS：向导显示本机 `dws` 版本、登录命令（可复制）、授权链接、失败状态；未安装/未登录时提示先安装登录再检查。
  飞书：401/403 显示可打开/复制的授权 URL；`feishu_chats` 规则见 §2。
- 钉钉卡片有「发送时暂停监听」开关（`dingtalk_pause_listen_on_send`，默认关；仅 CLI 要求 profile 独占时打开）。
- 禁用=未监听，卡片显示「未启用」，health 不假装在线；保存异步（「保存中…」），只重启 enable/transport/凭据/profile 变化的渠道。
- 明确请求不产生 proposal（见 §1）；审批已删除，收件箱只显示任务问题/受阻/失败（attention-only）。
- 运行时未接检查/授权接口时，向导显示「暂时不可用」而不是假成功。

## 6. 秘密与诊断

- 设置页不回显 secret（`internal/runtime/pages.go:539-550`）；留空保存=保留旧值。
- `store.Inspect`/`status --json` 的 KV 与 health detail 走 redaction（`internal/store/redact.go:23-87`、
  `internal/store/inspect.go:46-117,231,260`）：`***` 保留"已配置/未配置"事实，不泄露值。
- IM 驱动错误/health detail 经 `redactIMDetail` 脱敏；DWS stderr 按 `Secrets` 列表脱敏。

## 7. 运维步骤

1. 飞书（推荐 WS）：开放平台建应用、开事件订阅/长连接权限；设置›来源›连接：transport=ws，填 app id/secret
   （会话 ID 可留空）→「检查连接」（用填写值，不保存）→ 通过后「保存并启用」。
   若网络/环境不适合长连接，选 longpoll（`feishu_chats` 必填，cursor 存在 kv）。
2. 钉钉 DWS：安装 `dws` → `dws auth login`（headless `--device`）→ `dws auth status --format json` 应为 `loggedIn:true`；
   设置›来源›连接：transport=dws，按需填 dws 路径/profile/listen kind/chat-query/暂停监听 → 检查 → 保存。
   向导会显示本机 `dws` 版本与可复制的登录命令；未安装/未登录会明确提示。
3. 钉钉 Stream（bot）：transport=stream，填 app key/secret/robot code → 检查 → 保存。
4. 收到消息后：默认**直接成为 Ticket**（明确请求，见 §1）；没有审批队列。窗口关着时任务问题/受阻/失败会发系统通知。
5. 排障：`status --json` 看 `im:feishu`/`im:dingtalk` health；日志搜 `IM channel started`/`IM poll failed`/
   `dws`；DWS 自身 `dws auth status --format json`。
6. 升级：旧配置的 `*_enabled` 缺省时由凭据/flag 推导；flag-only 部署由 `BootstrapIMFlags` 补写启用；
   旧共享 `im_self` 复制到每渠道 id（`settingscodec.go:106-130`）。`LoadSettings` 在首次启动/升级时真正执行 `migrateSettings`
   并写 `SettingsSchema` stamp（`internal/runtime/pages.go:678-707`；测试 `TestLegacySettingsMigration`）；失败不会静默写入半套值。

## 8. 已知边界

- 飞书 webhook 未实现（面板不再提供）；飞书媒体只保留引用。
- DWS 能力受 CLI 契约限制（无成员列表/历史/模板发送）；真实 CLI 版本/登录流程仍需真实租户验证。
- 旧式审批/提案已彻底删除（无 RequireApproval/Approve/Reject/UndoReject）；旧 DB propose 载入为 ignore。
- 连接向导只做 draft 校验+凭据/订阅检查，不代替真实消息 E2E；真实 DWS 租户/飞书权限仍需环境验证。
- 连接向导不使用系统相机/扫码，DWS 登录仍在外部终端完成；系统 TextScale/ReduceMotion/HighContrast/Accent 已在 rc.2 接入面板。
- `ui.font_scale`/删除工作区/详情 ID/迁移等曾登记为差异，已在 2026-11-04 微调修齐；见 README/设计 §13。

## 9. 代码证据索引

- DSH 对齐：`internal/engine/evaluate.go:348-390`（explicit 直接接单；inferred accept mode/规则）；
  `internal/engine/gate.go:52-56`；`internal/domain/ticket.go:10-13`（Decision kinds）；
  `internal/engine/persist.go:156-162`（旧 propose → ignore/legacyImport）；`internal/ui/inbox.go:1-140`（attention-only）；
  `cmd/zeokeel/acceptance_test.go:129-160,207-232`。
- 三态/迁移：`internal/runtime/im.go:33-130,186-248`；`internal/runtime/settingscodec.go:1-70,106-160,182-230`；
  `cmd/zeokeel/main.go:300`、`cmd/zeokeel/imwiring.go:91`。
- 飞书 WS/longpoll：`internal/im/feishu/ws.go:1-100`；`internal/im/feishu/driver.go:71,94-107`；
  `internal/runtime/im.go:180-205,500-505,859-873,1045-1075`；`go.mod:12`。
- DWS 契约/生命周期：`internal/im/dingtalk/dws.go:1-28,63-68,90-130,286-330,569-700,842-890,970-1032,1069-1240`；
  `internal/runtime/dws.go:1-20,130-205`；`internal/runtime/im.go:344-470,749-760,1152-1179`。
- 面板/来源：`internal/ui/channels.go:1-360`；`internal/ui/overview.go:51-130`；`internal/runtime/pages.go:101-111,468-500`。
- 连接向导：`internal/ui/channelwizard.go:1-520`（transport 字段/draft 检查/保存）；`internal/runtime/ux_im.go:1-95`
  （`ValidateChannelDraft`/`CheckChannelDraft`/`DWSVersionValue`/`DWSLoginArgs`/`IMConnectionState`/字段元数据）；
  `internal/runtime/im.go:1392-1505`（per-transport specs 与 required 校验：ws chats 可选、longpoll chats 必填）；
  `internal/im/dingtalk/dws_setup.go`（版本/安装/登录命令）；`internal/im/feishu/ws_chats.go`（ws 会话可见范围）；
  `internal/runtime/ux.go:279-303`（收件箱 attention/路径）。
- 秘密：`internal/runtime/pages.go:539-550`；`internal/store/redact.go:23-87`；`internal/store/inspect.go:46-117,231,260`。
