# IM 渠道管理：飞书与钉钉（2026-11-03 Round 3）

> 规格来源：`audit/fixes/im-channel-spec.md`。本文描述**已实现**的行为与外部 `dws` CLI 契约；
> 未能从代码证实的地方明确写"未实现"。证据行以生产代码为准，文件行号随代码移动，以函数名为主。

## 0. 一句话

两个渠道各自有**启用开关 + 传输方式 + 凭据**；禁用就是"未监听"，不假装在线。
飞书只有 **HTTP 长轮询**；钉钉默认走**本机 DWS 网关 CLI**，官方 Stream SDK 作为可选 transport。
设置页每渠道有**检查 / 打开授权页**按钮；轮询与人工检查共用**每渠道一个串行 worker**。

## 1. 配置模型（settings group `source`）

| key | 类型 | 默认 | 说明 |
| --- | --- | --- | --- |
| `feishu_enabled` | switch | false | 启用飞书渠道；关闭=取消订阅/停止轮询，配置保留 |
| `feishu_transport` | select | `longpoll` | 当前实现只有长轮询；ws/webhook 是占位，选中后来源明确报错（不静默降级） |
| `feishu_app_id` / `feishu_app_secret` / `feishu_chats` | text/secret | — | 已有凭据；chats 逗号分隔 |
| `im_self` | text | — | 机器人自己的 id（@ 判定）；钉钉缺 `dingtalk_bot_id` 时回退到它 |
| `dingtalk_enabled` | switch | false | 启用钉钉渠道 |
| `dingtalk_transport` | select | `dws` | `dws`（默认，外部 CLI）/ `stream`（官方 Stream SDK） |
| `dingtalk_dws_cmd` | text | `dws` | DWS 可执行文件或包装脚本 |
| `dingtalk_app_key` / `dingtalk_app_secret` / `dingtalk_robot` | text/secret | — | Stream 模式与 DWS 登录共用（DWS 自管登录可留空） |
| `dingtalk_bot_id` | text | — | 机器人自身 id，用于 @ 判定；空则用 `im_self` |

代码：`internal/runtime/im.go:31`（`IMConfig`）、`:51`（`IMConfigFromSettings`，默认值在这里）、
`:88-107`（命令行凭据覆盖 settings，flag 只覆盖凭据，不覆盖 enable/transport）。
设置页字段：`internal/runtime/pages.go:458-484`（开关/transport/命令/凭据）。

## 2. 生命周期与状态

- 保存设置后，runtime 比较旧/新配置（`imAfterSettingsSave` → `IMManager.ApplyIMSettings`，
  `internal/runtime/im.go:189`、`:224` 的 `channelChanged`）：**只有该渠道的 enable/transport/凭据/self 变化才优雅停止并重启**，
  未变化不重启。启动时 `IMConfig().WithFlags(flags)` 后同样走 `Start`（`:173`）。
- 启用/禁用/半配置的判定在 `channelReadiness`（`:328`）：
  - 未启用 → 状态"未启用"，health `ready`（"未启用"），不监听、不报错、不说"在线"（`applyChannel` `:239`，禁用分支 `:268-273`）；
  - 半配置（只填一半、或选了未实现的 transport）→ 来源 `error` + 可操作 detail，不静默启动；
  - 完整 → 启动 driver，来源状态 `启动中` → `ready`（`setStatus` `:571`）。
- 收件循环在 `pollLoop`（`:408`）：每轮 poll → 逐事件 `ReceiveTrigger` → **本批全部成功后才持久化 cursor**
  （`:446-458`）；任一事件失败则 cursor 不动，下一轮重试同一批（幂等由 Trigger/内容 ID 保证）。
  失败退避 1s→30s（`:415-437`）；cursor 存 kv `im.cursor.<ch>`（`cursor`/`setCursor` `:538/:550`）。
- 健康：来源卡片与 `health` 表都写；错误分类 `classifyIMError`（`:519`）把 401/403/unauthorized/token expired
  归为 `auth`，其余为 `error`。

## 3. 飞书：HTTP 长轮询 + 凭据检查

- 传输：`internal/im/feishu` 用 `im/v1/messages` 历史接口做"游标 + 增量"长轮询（`transport.go:1-31`）；
  **没有 WS 长连接、没有 webhook、没有媒体下载**（`mapping.go` 只保留 image_key/file_key 引用）。
- 检查/授权：`CheckChannel("feishu")` → `checkFeishuAuth`（`internal/runtime/dws.go:101-140`）：
  - 凭据不完整 → state `auth`，detail 说明缺 app id/secret，authURL = `https://open.feishu.cn/app`；
  - POST `https://open.feishu.cn/open-apis/auth/v3/tenant_access_token/internal`（10s 级超时）；
  - 401/403 → `auth` + 授权入口；网络/5xx → `error`。
- 授权按钮：面板用 `ChannelAuthorizer.CheckChannel`（`internal/ui/channels.go:14-25`）在后台线程检查，
  返回 authURL 时显示「打开授权页」，用 `mygo.Shell.OpenExternal` 打开（`channels.go` 的 `openAuthorizationURL`）；
  只有 http(s) 地址才打开（防注入）。

## 4. 钉钉：默认 DWS（外部命令契约）

### 4.1 契约

所有调用 `exec.CommandContext` 直接执行（不经 shell），单次超时 **30s**（`internal/im/dingtalk/dws.go:87-95` 的
`DWSConfig.Timeout` 默认值），stderr 截断后进 health detail（去密钥）。

| 操作 | 命令 | 期望 JSON | 失败处理 |
| --- | --- | --- | --- |
| 授权检查 | `dws auth status --json` | `{"loggedIn":true,"account":"...","authUrl":"https://..."}` | 未登录 → state `auth`，detail 给 `dws auth login` 或 authUrl |
| 收件轮询 | `dws poll --cursor <c> --limit 50 --json` | `{"cursor":"c2","messages":[{"id","conversation":{"id","type","name"},"sender":{"id","name","bot"},"text","at","mentions","replyToBot"}]}` | 坏 JSON/超时 → 报错退避；**cursor 不前进** |
| 发件 | `dws send --conversation <id> --text <t> --idempotency-key <k> --json` | `{"messageId":"..."}` | 4xx 永久错误 / 5xx 交给 Outbox 重试；文本尾部附加隐藏 marker |
| 健康（可选） | `dws health --json` | `{"ok":true,"detail":"..."}` | 失败/未实现不判死，回退到 auth status |

实现：`commandDWSClient`（`dws.go:255`，`run` `:305`）、`AuthStatus`（`:337`）、`Health`（`:351`）、
`Poll`（`:398`）、`Send`（`:434`）；传输适配 `DWSTransport`（`:718` 起），`SendText` 优先用 `TextSender`+幂等键（`:810`）。

### 4.2 串行 worker（为什么）

**每渠道一个 `dwsRunner`**（`dws.go:561-700`）：poll tick、send、auth/health check 全部进同一队列，
同一时刻最多一个 `dws` 子进程；优先级 **send > auth/health > poll**（`dws.go:577-581`），同优先级 FIFO；
`ctx` 取消会杀掉正在跑的子进程（`Stop` `:647`）。

理由：DWS 是**外部 CLI**，登录态/配置文件是全局的；并发调用会互相打断登录刷新、让 stdout 交错、让"检查"和"轮询"
同时改本地状态。串行队列把竞态收敛到一个进程内，代价只是 send 最多等一个 poll（2s 级）。

### 4.3 游标、幂等、退避

- **cursor 只在本批全部被引擎接受后持久化**（`internal/runtime/im.go:446-458`）：失败批次重试而不是跳过；
  引擎侧同 ID 幂等（内容派生 Trigger ID），所以重读安全。
- **幂等标记**：发送文本尾部附加 `<!-- zeokeel:<key> -->`（`markerForDWS` `dws.go:458-465`），
  对方客户端不渲染；重试用同一 key，配合 DWS 的 `--idempotency-key` 避免重复消息。
- **退避**：poll 失败 1s 起、×2 到 30s 封顶（`dws.go:114-121`，runtime 侧同节奏 `im.go:415-437`）；
  成功后重置为 1s。
- **未安装/不可执行**：`dws auth status` 失败 → 来源 `error` + "安装 dws 或设置 dingtalk_dws_cmd"
  （`internal/runtime/im.go:617`）；**不自动 fallback 到 stream**，除非用户把 transport 改成 `stream`。
- 只支持接口里声明的能力：DWS 没有成员列表/消息历史/机器人模板发送，驱动对它们明确返回 `ErrUnsupported`
  （`internal/runtime/dws.go:53,96`；`dws.go` 的 `SendRobot`/`ListMembers`/`AccessToken`）。

## 5. 官方 Stream（可选 transport）

`dingtalk_transport=stream` 时用官方 SDK：`internal/im/dingtalk/driver.go:102`（`New`，流式客户端在
`stream.go:28-29,60,98`；依赖 `open-dingtalk/dingtalk-stream-sdk-go v0.9.1`）。
检查按钮对 stream 只验证凭据是否齐全（`checkDingStreamAuth` `internal/runtime/im.go:631`），
真实连接由启动后的订阅验证。**默认仍是 DWS**。

> **已知 UI 偏差（owner: fix-ui2）**：`internal/view/channels.go:44-66` 把 stream 标成 `Implemented: false`
> （面板显示"未实现"且不可选），但 runtime 已能构建官方 Stream 驱动（`internal/runtime/im.go:394-398`），
> 设置字段的 Help 也写"stream：官方 Stream SDK"。要么把该选项改为可用，要么在设置 Help 里去掉 stream 说法。

## 6. 面板交互

- 来源组每渠道一张卡片：开关、传输方式、凭据字段、状态行、「检查通道」按钮（`internal/ui/channels.go:134-230`）；
  禁用时状态"未启用"，绝不显示"监听中"（`channelState` `:244`）。
- 检查在后台 goroutine 执行（`startChannelCheck` `:38-50`），答案回主线程；auth 状态显示「打开授权页」。
- 运行时未接 `ChannelAuthorizer` 时按钮文案说明"动作未接线"，不假装检查过（`:62`）。

## 7. 运维步骤

1. 安装 DWS 并登录：`dws auth login`（或点面板「检查通道」拿 authUrl）；`dws auth status --json` 应 `loggedIn:true`。
2. 设置›来源：打开「启用钉钉」，transport 选 `dws`，`dws` 命令可用（或填绝对路径/包装脚本）；点「检查通道」。
3. 飞书：填 app id/secret/chats → 启用 → 检查；401/403 时按 authURL 去开放平台修应用权限/发布版本。
4. 禁用渠道：关开关保存即可；轮询停止、来源状态"未启用"，配置保留。
5. 排障：`zeokeel status --json` 的 health 里看 `im:feishu` / `im:dingtalk`；日志 `logs/zeokeel.log` 搜
   `IM poll failed` / `IM driver failed`；DWS 细节看 `dws health --json`。

## 8. 未实现 / 已知限制

- 飞书 WS/webhook、IM 媒体下载（只保留引用）。
- DWS 成员列表/消息历史/模板发送（明确 `ErrUnsupported`）。
- 钉钉 Stream 选项的 UI 可用性（见 §5 偏差）。
- 真实凭据端到端未在本机验证（CI/发布前需要凭据）。
