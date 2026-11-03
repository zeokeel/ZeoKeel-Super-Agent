# IM 渠道管理 / DWS 传输规格（2026-11-03）

## 0. 目标
1. 渠道可**单独启用/停用**；禁用时明确“未监听”，不假装连接。
2. 钉钉启用时默认走 **DWS**（DingTalk Workspace 网关 CLI；本机未安装，按外部命令适配），官方 Stream SDK 保留为可选 transport。
3. 飞书保留 HTTP 长轮询；可检查凭据并给出授权/修复入口。
4. 轮询与用户手工调用（检查/授权/发送）共用**每渠道一个串行 worker**，不并发调 CLI，避免命令互相打断/登录态竞争。
5. 设置页可为每个渠道配置并显示健康状态；授权按钮能打开浏览器（或给出明确命令）。

## 1. 配置（settings group `source`）
| key | 类型 | 默认 | 说明 |
| --- | --- | --- | --- |
| `feishu_enabled` | switch | false | 启用飞书渠道 |
| `feishu_transport` | select: `longpoll` | `longpoll` | 当前只有长轮询；WS/webhook 列选项但标未实现 |
| `feishu_app_id` / `feishu_app_secret` / `feishu_chats` / `im_self` | text/secret | — | 已有 |
| `dingtalk_enabled` | switch | false | 启用钉钉渠道 |
| `dingtalk_transport` | select: `dws`/`stream` | `dws` | 默认 DWS；stream=官方 Stream SDK |
| `dingtalk_dws_cmd` | text | `dws` | DWS 可执行文件/包装脚本 |
| `dingtalk_app_key` / `dingtalk_app_secret` / `dingtalk_robot` | text/secret | — | stream 模式与 DWS 登录共用（DWS 若自己管理登录可留空） |
| `dingtalk_bot_id` | text | — | 机器人自身 id，用于 @ 判定 |
- 保存设置后 runtime 比较旧/新配置：变化则优雅停止并重启对应渠道；未变化不重启。
- 禁用：取消订阅、停止轮询、来源状态 `disabled`、健康 `ok`（“未启用”），不清除配置。

## 2. DWS CLI 契约（外部命令；本仓库只承诺适配这组 JSON 命令）
所有调用：`exec.CommandContext(ctx, cmd, args...)`，不经过 shell；超时 30s；stderr 进入 health detail（截断，去密钥）。
- 授权检查：`dws auth status --json` → `{"loggedIn":true,"account":"...","authUrl":"https://..."}`
  - exit 0 + loggedIn=true → ready；loggedIn=false → state `auth`，detail 给登录命令/打开 authUrl。
- 收件轮询：`dws poll --cursor <cursor> --limit 50 --json`
  → `{"cursor":"c2","messages":[{"id":"m1","conversation":{"id":"cid","type":"group|direct","name":""},"sender":{"id":"u1","name":"张三","bot":false},"text":"...","at":"2026-11-03T10:00:00+08:00","mentions":["bot-id"],"replyToBot":false}]}`
  - 仅当本批全部成功交给引擎后才持久化 cursor；失败退避 1s→30s。
- 发件：`dws send --conversation <id> --text <text> --idempotency-key <key> --json`
  → `{"messageId":"..."}`；文本尾部可附加隐藏 marker `<!-- zeokeel:<key> -->` 供重复检查；失败 4xx 永久错误 / 5xx 重试由 Outbox 决定。
- 健康：`dws health --json` → `{"ok":true,"detail":"..."}`（可选）。
- 命令缺失/不可执行/超时/JSON 坏 → 来源状态 `error` + 健康 error，detail 可操作（“安装 dws / 运行 dws auth login”）；不自动 fallback 到 stream，除非用户把 transport 改成 stream。
- 串行：每渠道一个 `dwsRunner`（mutex/queue）：poll tick（2s）、send、auth check 都进同一队列；同一时刻最多一个子进程；send 优先于 poll；ctx 取消时杀子进程。

## 3. 飞书授权检查
- `CheckChannel("feishu")`：POST `https://open.feishu.cn/open-apis/auth/v3/tenant_access_token/internal`（app_id/app_secret，10s），成功→ready，失败 401/403→state `auth`，detail 含错误码与 `https://open.feishu.cn/app` 入口；网络失败→state `error`。
- 长轮询由现有 `internal/im/feishu` 驱动；启用开关控制 start/stop。

## 4. 代码归属
- `internal/im/dingtalk/dws.go`（nih-integrations）：`DWSClient`（接口 + command 实现 + fake）、poll/send/auth 解析、单串行 runner、cursor 由 runtime 提供、marker/幂等、测试用 fake `dws` 脚本或 fake client。
- `internal/runtime/im.go` + `cmd/zeokeel/imwiring.go`（nih-runtime）：`IMConfig` 从 settings/flags 读；`IMManager` 每渠道生命周期；`ApplyIMSettings(before,after)`；`CheckChannel(name) (state,detail,authURL,error)`；把 driver health 投到来源/健康；持久化 cursor 到 kv；启动时按配置启动。
- `internal/ui`（nih-ui）：Sources 组每渠道卡片：开关、transport 选项、凭据字段、检查/授权按钮（`ChannelAuthorizer` 可选接口）、状态行；authURL 用浏览器打开（mygo `Browser.OpenURL` 或系统命令，需查 mygo API）。
- UI 的“新建工作区”目录选择器独立：mygo `Dialog.Open(Directory:true)` 原生选择器，三平台由 mygo 处理；测试用可替换 dialog seam。

## 5. 验收
- 单测：配置变更启停、禁用不监听、半配置报错、DWS fake（poll/send/auth/坏 JSON/超时/命令缺失/串行不重叠/游标仅成功后推进）、飞书检查成功/失败、UI 开关/授权按钮、原生选择器 fake。
- 全量 `go build ./...`、`go test ./... -skip Live`、相关 `-race` 通过。
- 文档：README/ops-runbook 写明 DWS 契约与“未安装时如何配置/授权”。
