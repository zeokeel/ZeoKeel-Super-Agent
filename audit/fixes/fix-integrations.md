# fix-integrations handoff

Wave 1 修复：scope = `internal/im/**`、`internal/sources/**`、`tools/**`。
未改 `cmd/`、`engine`、`runtime`、`domain`、`store`、`ui`、`go.mod/go.sum`、`scripts/`、文档、其他分片报告。
零新增依赖；`go.mod` 未动。

## 完成的条目

| 条目（audit 编号） | 修复 | 落地位置 |
| --- | --- | --- |
| N-1 / F2 飞书 token 缓存正确性 | 并发刷新单飞（8 并发只打一次 token 接口）+ 提前刷新窗口（10%，≤1min）+ 可注入时钟；长轮询路径补文档说明"WS/事件推送接法" | `internal/im/token.go`、`internal/im/feishu/transport.go`（`TenantToken`）、`internal/im/feishu/driver.go`（Subscribe 文档） |
| N-1 / F2 飞书签名校验 + AES 解密（Wave2 事件推送的前置） | 纯 stdlib 实现 `EventSignature`/`CheckEventSignature`（常量时间比较）、`CheckVerificationToken`、`DecryptEvent`（AES-256-CBC + PKCS#7，信封 `{"encrypt"}`），带 4MB 上限 | `internal/im/feishu/event.go`（新增）+ `event_test.go` |
| N-1 / F2 飞书发送/回复/卡片/分页/健康/媒体引用 | 逐项核对并补测试：uuid 幂等、回复端点、interactive 卡片、成员分页（超上限报错而不是半份名单）、image/file/media/audio 的 `MediaRef`、`New` 拒绝空/重复 chat id | `feishu/driver.go`、`feishu/mapping.go`（未改逻辑）、`feishu/feishu_test.go` |
| N-1 钉钉 Stream 诚实降级 | `StreamSupport` 可选接口：`HTTPTransport` 明确报不支持；`Subscribe` 立刻返回 `im.ErrUnsupported` 并把 health 置 error（不再永久退避空转）；真 Stream 传输走同一轮询循环 | `dingtalk/transport.go`、`dingtalk/driver.go` |
| **Wave2：钉钉 Stream 真实接收（Lead 批准的依赖）** | 用官方 `github.com/open-dingtalk/dingtalk-stream-sdk-go@v0.9.1` 实现 `StreamTransport`：`NewStreamClient(WithAppCredential(...), WithAutoReconnect(true))` → `RegisterChatBotCallbackRouter` → `Start`；回调映射成 `dingtalk.Event`（MsgID/会话/群聊-单聊/发送者/@列表/时间/文本或任意 JSON content），有界队列 + 批交给 `StreamEvents`；`New()` 在 AppKey/AppSecret 存在时默认启用 Stream（`Config.NoStream` 可关），`Subscribe` 退出时关闭 SDK 连接；连接状态经 `SetStatus` 写入驱动 health | `dingtalk/stream.go`（新增）、`dingtalk/driver.go`（Config/New/Subscribe）、`dingtalk/stream_test.go`（新增） |
| N-1 钉钉 JSON 参数 | 删除手写 `quote()`，`textParam`/`markdownParam` 改 `encoding/json`（含控制字符的正文不再产出非法 JSON） | `dingtalk/driver.go` |
| **Wave3：钉钉 DWS 传输（IM-3，规格 audit/fixes/im-channel-spec.md）** | `internal/im/dingtalk/dws.go`：`DWSClient` 接口 + 命令行实现（`dws auth status/poll/send/health --json`，exec 无 shell、30s 超时、4xx 永久/5xx 可重试分类、stderr 截断进 detail）、每渠道一个优先级串行 runner（send > auth/health > poll，同一时刻最多一个子进程，ctx 取消杀子进程）、`DWSTransport`（StreamEvents 拉模式、TextSender 发送加 marker、消息 id 有界去重、2s tick/1s→30s 退避、health 状态 ok/auth/error）；`driver.Config` 增加 `Transport:"dws"`、`DWSCommand`、`DWSClient`、`NoDWS`、`SelfID` 及 DWS 超时/退避配置；`SubscribeFrom`（im.CursorReceiver）在整批交付后保存游标，`AuthStatus`/`DWSHealth` 供 runtime 检查/授权 | `dingtalk/dws.go`（新增）、`dws_command_test.go`/`dws_test.go`（新增）、`driver.go`、`transport.go`(Event.SenderIsBot)、`mapping.go`；`internal/im/{im,poll}.go`（TextSender/CursorReceiver/SaveCursor） |
| N-1 钉钉收件去重 | 进程内按 `MsgID` 有界去重（512），Stream 重连重放不产生重复 `RawEvent` | `dingtalk/driver.go`（`seen`/`rememberSeen`） |
| N-15 发送重试越权 | 传输层发送类请求统一 `im.NoRetry`（Attempts=1）：401 仍刷新 token 重试一次，5xx/429 直接上报 `*im.HTTPError`（含 `RetryAfter`），重试归 Outbox；读类请求仍按 `RetryPolicy` | `internal/im/httpclient.go`、`feishu/transport.go`、`dingtalk/transport.go` |
| N-14 GitHub 分页静默丢失 | 通知/评论/Issue/PR/review-comment/协作者全部 Go 侧翻页（`per_page=100`，最多 20 页）；超过上限**报错并保持游标**（不静默截断） | `sources/github/poll.go`、`members.go` |
| N-14 marker 只搜第一页 | `find()` 翻完所有评论页（最多 50 页）；找不到且超限 → 失败关闭（宁可让 Outbox 重试，不重复评论） | `sources/github/channel.go` |
| N-14 无 ETag / 条件请求 | `Command` 新增可选 `HeaderCommand`：`ExecCommand` 跑 `gh api -i` 解析状态行/头/body，支持 `If-None-Match` 与 304；游标 v2 存两条流的 ETag（旧格式仍可读）；`since` 只在"真的有更新条目"时前进（原型 `readStream` 的 `moved > since` 语义），否则复用 ETag 拿 304 | `sources/github/command.go`、`poll.go`（cursorState） |
| N-14 接收面窄（001:27 / 003 / 006） | 新增 `Config.ReceiveAll`（Issue、PR 本体、对话评论、PR 行内 review 评论）与 `Config.ObserveCI`（默认分支 CI：首次建基线、失败集合变化才发、检查在跑时不报恢复、恢复只报一次、revision 递增保证 Trigger ID 唯一）；新增 Raw kind `issue`/`pull`/`review-comment`/`ci` 与 admission 行（单数，与 domain 注释一致）`comment`/`issue`/`pull`/`ci`；翻译时同时填好 `Trigger.Row` 与 `Trigger.ScopeReceivesAll`（供 002 的 gate 直接读） | `sources/github/{config,trigger,scope,poll}.go` |
| N-14 游标只在成功时推进 | 保持并补测试（既有）；CI 读失败同样保持游标 | `sources/github/poll.go` |
| F8 genicon 手写开方 | 30 次牛顿迭代 `sqrt` → `math.Hypot`；删除 `bar`/`disc` 未使用参数；补 ICO/ICNS 头/偏移/净荷、绘制确定性测试 | `tools/genicon/main.go` + `main_test.go`（新增） |
| F9 seedstate `-allow` 必失败 | `fmt.Sscanf("%[^:]")`（Go 不支持 scanset）→ `strings.Cut`；`splitCSV` → `strings.Split`；补解析测试 | `tools/seedstate/main.go` + `main_test.go`（新增） |
| §5 重复 HTTP 管线 | 抽出 `im.HTTPClient`（请求构造/鉴权头/body 上限/状态码归一/重试/401 刷新）与 `im.TokenCache`（单飞+提前刷新）；飞书/钉钉 transport 各自只剩协议方法，删除约 400 行复制粘贴 | `internal/im/httpclient.go`、`internal/im/token.go`（新增） |
| §5 重复小工具 | `im.RetryAfter`（整数秒 + HTTP-date）、`im.Snippet`、`im.NoRetry`、`im.TokenSkew` 集中实现，两个 transport 不再各写一份 | `internal/im/httpclient.go`、`token.go` |
| 死代码 | 删除无调用者的 `im.HealthOf`；`dingtalk` 的 `markerHash`/`stripMarker`（仅测试用）移入 `dingtalk_test.go`；`im.PollOnce` 保留（Wave2 单次拉取可用） | `internal/im/status.go`、`dingtalk/driver.go` |
| Health 去重（Lead 项 4） | **不做合并**：`im.Health` 与 `sources.Health` 是同一面板行的两个端口形状，合并会让 `im` 反向依赖 `sources`（分层倒挂）。已在 `im/status.go` 写清理由；字段漂移由两边测试守护 | `internal/im/status.go` 注释 |

## 改动文件（仅本 scope）

新增：`internal/im/httpclient.go`、`internal/im/token.go`、`internal/im/httpclient_test.go`、
`internal/im/feishu/event.go`、`internal/im/feishu/event_test.go`、
`sources/github/poll_n14_test.go`、`sources/github/poll_ci_test.go`、`sources/github/channel_paging_test.go`、
`sources/github/command_test.go`、
`tools/genicon/main_test.go`、`tools/seedstate/main_test.go`。

修改：`internal/im/status.go`、`internal/im/retry.go`(仅注释)、
`internal/im/feishu/{transport,driver}.go` 与测试、`internal/im/dingtalk/{transport,driver}.go` 与测试、
`internal/sources/github/{poll,trigger,scope,config,command,members,channel}.go` 与测试、
`tools/genicon/main.go`、`tools/seedstate/main.go`。

## 新增/修改的测试与结果

```text
$ CGO_ENABLED=0 go test ./internal/im/... ./internal/sources/... ./tools/... -count=1
ok  github.com/zeokeel/super-agent/internal/im            1.127s
ok  github.com/zeokeel/super-agent/internal/im/dingtalk    0.032s
ok  github.com/zeokeel/super-agent/internal/im/feishu      0.045s
ok  github.com/zeokeel/super-agent/internal/sources        0.005s
ok  github.com/zeokeel/super-agent/internal/sources/github 0.046s
ok  github.com/zeokeel/super-agent/tools/genicon           0.573s
ok  github.com/zeokeel/super-agent/tools/seedstate         0.004s

$ gofmt -l internal/im internal/sources tools     # 空
$ CGO_ENABLED=0 go vet ./internal/im/... ./internal/sources/... ./tools/...   # 空
$ CGO_ENABLED=1 go test -race ./internal/im/... ./internal/sources/... ./tools/genicon -count=1
ok  internal/im 2.154s / im/dingtalk 1.075s / im/feishu 1.103s / sources 1.017s / sources/github 1.645s / tools/genicon 4.361s
```

**新增测试规模**：55+ 个新测试函数（23 个加在既有测试文件，32+ 个在新测试文件）。
**最终 scope 验证（fix-engine 落盘后重跑，全绿）**：

```text
$ gofmt -l internal/im internal/sources tools                          # 空
$ CGO_ENABLED=0 go vet ./internal/im/... ./internal/sources/... ./tools/...   # 空
$ CGO_ENABLED=0 go test ./internal/im/... ./internal/sources/... ./tools/... -count=1
ok  internal/im            1.129s
ok  internal/im/dingtalk    0.032s
ok  internal/im/feishu      0.043s
ok  internal/sources        0.003s
ok  internal/sources/github 0.102s
ok  tools/genicon           0.498s
ok  tools/seedstate         0.004s
```

**跨 scope 状态**：Wave1 验证时 fix-runtime 的 `cmd/zeokeel` 尚未落盘；Wave2 结束时已全量复跑，
四件套全绿（见下节 Wave2 验证）。`go test -race`（CGO_ENABLED=1，仅测试运行时）本 scope 全绿。

## Wave 2 追加：DingTalk Stream 接收端（本文件之外的增量）

### 实现

- `internal/im/dingtalk/stream.go`（新增）：
  - `StreamTransport` 嵌入 `*HTTPTransport`（发送/成员/令牌原样复用），覆盖
    `StreamEvents`/`SupportsStream`/`Close`；`Transport` 契约不变。
  - SDK 表面收窄成 `StreamClient` 接口（`RegisterChatBotCallbackRouter`/`Start`/`Close`），
    生产实现是官方 SDK 的薄封装 `NewSDKStreamClient`；测试注入 `fakeStreamClient`。
  - `messageFromCallback` 是纯函数：`chatbot.BotCallbackDataModel` → `dingtalk.Event`。
    文本取 `Text.Content` 并 JSON 化成 `{"content":...}`；其它类型把 `Content` 原样
    JSON 化（mapping 的 `renderContent` 按 msgtype 解析同一套键）。
  - 有界队列 256（满时对 SDK 回调施加背压，不丢消息）；单批最多 50 条；
    content 上限 1 MiB、消息/会话 id 上限 256 字节；坏回调仍回空 ack（避免钉钉重投风暴）
    并计入 `Dropped()`，面板/测试可解释"少了一条"。
  - 建连失败：1s→30s 指数退避重试，错误交给 `StreamEvents` → `SubscribeLongPoll`
    的退避与 health 计数（连续 3 次才标红）。连接建立后由 SDK `WithAutoReconnect`
    负责断线重连；`Subscribe` 退出时 `Close()`，不会泄漏 SDK 读循环。
  - 秘密：ClientID/ClientSecret 仅用于构造 SDK，不写日志/不进 Event/不进错误文本；
    SessionWebhook（带 token）不落任何结构体字段。SDK 默认 logger 是 no-op
    （`logger.GetLogger()` 在未 `SetLogger` 时返回 doNothingLogger），本实现不调用
    `logger.SetLogger`，因此 SDK 的 wire/消息内容日志不会打到 stdout——用户消息正文
    不会进日志。
- `internal/im/dingtalk/driver.go`：`Config` 新增 `NoStream`、`StreamKeepAlive`、
  `StreamBackoff/MaxStreamBackoff`、`StreamClient`（测试注入）；`New()` 构造 HTTP 传输后，
  只要未设 `NoStream` 就包一层 `StreamTransport` 并把 `SetStatus` 接到 `d.setStreamStatus`；
  `Subscribe` defer `Close`。
- `dingtalk/transport.go` 包注释更新：接收端不再是"接口与假实现"。

### runtime 接线契约（fix-runtime 已完成的代码 **不需要改**）

`cmd/zeokeel/imwiring.go:80-84` 现有调用就是正确契约：

```go
dingtalk.New(dingtalk.Config{
    AppKey: o.DingAppKey, AppSecret: o.DingAppSecret, RobotCode: o.DingRobot, BotName: "ZeoKeel",
})
```

- 凭据来源（flags 或 settings，`imwiring.go:40-48` 的 `saved()`）：
  - `-dingtalk-app-key` / settings `source.dingtalk_app_key`
  - `-dingtalk-app-secret` / settings `source.dingtalk_app_secret`
  - `-dingtalk-robot` / settings `source.dingtalk_robot`
  - AppKey/AppSecret 同时是 Stream 的 clientId/clientSecret（钉钉自建应用同一套凭据）。
- 行为：有这三个键 → `New()` 自动启用 Stream 接收，`wireIM` → `startIMLoop` → `Subscribe`
  开始收消息；**没有新增 settings env key**。
- 无 WebSocket 出网的部署：`Config.NoStream=true` → `Subscribe` 立即返回
  `im.ErrUnsupported` 且 health=error（诚实报不可用，不伪装在线）。
- health 文案：`stream connected; waiting for messages`（starting）→ 首条消息后
  `SubscribeLongPoll` 置 ready；建连失败 `stream connect failed: ...`（error）。

### 语义限制（documented deviation）

- `chatbot.BotCallbackDataModel` **没有 `isReplyMsg`/父消息 id**：Stream 路径上
  `RawEvent.ReplyToBot` 恒为 false（"回复机器人"不能当 addressed 事实）。@ 与单聊仍然
  可靠（`AtUsers`/`ConversationType` 有值），这与既有 mapping 注释一致。
- SDK 不暴露"连接已断开"的回调（只有自带 logger）；连接建立后若远端静默断开，
  health 会停在 waiting，直到 SDK 自动重连后下一条消息。这是 SDK 能力边界，不是本实现
  的静默丢弃——消息本身在 SDK 读循环里，不会主动丢。
- 未实现：卡片/事件/插件回调（只注册机器人消息回调）；媒体下载；这些不属于
  "能收到消息"的 1.0 范围，保留 documented-deviation。

### 验证（repo 内最终结果；Lead 已补 gorilla/websocket 行）

```text
$ gofmt -l internal/im/dingtalk                                  # 空
$ CGO_ENABLED=0 go vet ./internal/im/... ./internal/sources/...  # 空
$ CGO_ENABLED=0 go test ./internal/im/... ./internal/sources/... ./tools/... -count=1
ok  internal/im 1.126s / im/dingtalk 0.143s / im/feishu 0.044s / sources 0.004s
ok  sources/github 0.112s / tools/genicon 0.517s / tools/seedstate 0.004s
$ CGO_ENABLED=0 go build ./...                                  # 空（全仓）
$ sh scripts/check-nocgo.sh                                      # PASS (zero cgo)
$ CGO_ENABLED=1 go test -race ./internal/im/... -count=1         # ok（im 2.15s / dingtalk 1.25s / feishu 1.12s）
$ CGO_ENABLED=0 go test ./... -count=1 -skip 'Live'              # 两次连续全绿（exit 0）
```

新增 Stream 测试 8 个：group/direct 映射、非文本 content、坏负载/超限、建连退避+错误上报、
ctx 取消、端到端 Subscribe+MsgID 去重、NoStream 诚实 ErrUnsupported、未知类型 Unsupported 交付。
（中途因 repo go.mod 缺 gorilla/websocket 行，曾在 `/tmp/fi-check` 隔离模块里先验证过同一份代码；
Lead 补上依赖后 repo 内复跑通过。）

纯 Go 证据：`grep -rln 'import "C"' open-dingtalk/dingtalk-stream-sdk-go@v0.9.1
gorilla/websocket@v1.5.0 google/uuid@v1.6.0` → 无；`scripts/check-nocgo.sh` 第 3 步
（CGO_ENABLED=1 第三方 cgo 审计）PASS。

## Wave 3 追加：DingTalk DWS 传输（IM-3）

规格：`audit/fixes/im-channel-spec.md` §2（DWS CLI 契约）与 §4（代码归属）。
实现只在 `internal/im/**`；未改 runtime/cmd/ui/go.mod。

### 文件

- `internal/im/dingtalk/dws.go`（新增）：
  - `DWSClient` 接口（`AuthStatus`/`Poll`/`Send`/`Health`）；
  - `commandDWSClient`：全部调用 `exec.CommandContext(cmd, args...)`，不过 shell；
    单次超时 `DWSConfig.Timeout`（默认 30s，`cmd.WaitDelay=1s` 防止被杀的 shell
    子进程握住管道导致挂起）；stderr/stdout 截断后进 detail；
  - `DWSError`（`Temporary()`/`IsPermanent(err)`）：4xx = 永久，5xx/未知 =
    可重试，命令缺失 = 永久，超时 = 可重试，ctx 取消 = 不可重试；
  - `dwsRunner`：每渠道一条优先级队列（send=0 > auth/health=1 > poll=2），
    同一时刻最多一个子进程；`Stop` 取消在跑的子进程并让排队调用失败；
  - `DWSTransport`：实现 `Transport`（StreamEvents 拉取）、`StreamSupport`、
    `StreamCloser`、`im.TextSender`；2s poll tick、失败退避 1s→30s、消息 id
    有界去重（512）、坏 item 计数（Dropped）、duplicates 计数；
  - `AuthStatus`/`DWSHealth`：渠道级授权/健康（state `ok`/`auth`/`error`/`starting`，
    auth 带 `AuthURL` 与人话 detail）。
- `internal/im/dingtalk/driver.go`：`Config` 新字段（见下表）；`New()` 按
  `Transport` 分派 `newStreamDriver`/`newDWSDriver`（未知值报错，绝不静默回退）；
  `Subscribe` → `SubscribeFrom`（实现 `im.CursorReceiver`）；`Send` 优先走
  `im.TextSender`（DWS：纯文本 + 业务幂等键）；`Self` 支持 `SelfID`（DWS bot id）；
  `AuthStatus`/`DWSHealth` 转发给 DWS 传输。
- `internal/im/im.go`：新增可选能力 `TextSender`、`CursorReceiver`；
  `internal/im/poll.go`：`SubscribeOptions.SaveCursor`（游标真正推进时、整批交付后调用，
  保存失败即停止且不推进）。
- `internal/im/dingtalk/mapping.go`/`transport.go`：`Event.SenderIsBot` 权威 bot 标记
  （DWS `sender.bot`）；`Actor.Bot` 优先用它，Stream 仍用 staffId 启发式。

### runtime 必须使用的接线契约（settings → `dingtalk.Config`）

| settings key（group `source`） | Config 字段 | 行为 |
| --- | --- | --- |
| `dingtalk_enabled` | —（runtime 决定是否启动） | false：取消订阅、来源 disabled，不构造 driver |
| `dingtalk_transport` (`dws`\|`stream`，默认 `dws`) | `Transport` | 必填传递；`dws`=DWS CLI，`stream`=官方 Stream SDK；未知值 `New` 返回错误 |
| `dingtalk_dws_cmd`（默认 `dws`） | `DWSCommand` | DWS 可执行文件/包装脚本（可以是绝对路径） |
| `dingtalk_app_key`/`dingtalk_app_secret` | `AppKey`/`AppSecret` | **stream 模式必填；dws 模式可空**（DWS 自己管理登录）；DWS 下若提供仅用于可选 HTTP 发送回退（本实现发送走 DWS） |
| `dingtalk_robot` | `RobotCode` | stream 模式必填；dws 模式可空 |
| `dingtalk_bot_id` | `SelfID` | **dws 模式强烈建议填**：@ 判定用（mapping 的 selfID）；缺省回退 `RobotCode` |
| —（测试/显式不监听） | `NoDWS` | true：不构造 DWS 传输；`Subscribe` 返回 `im.ErrUnsupported`（health error），不会 fallback 到 Stream |

构造（runtime 现有 `dingtalk.New` 调用点替换为）：

```go
drv, err := dingtalk.New(dingtalk.Config{
    Transport:  transport,          // "dws" | "stream"
    DWSCommand: dwsCmd,             // source.dingtalk_dws_cmd
    AppKey:     appKey, AppSecret: appSecret, RobotCode: robot,
    SelfID:     botID,              // source.dingtalk_bot_id
})
```

收件/游标（`im.CursorReceiver`）：

```go
recv, ok := drv.(im.CursorReceiver) // *dingtalk.Driver 恒为 true
err := recv.SubscribeFrom(ctx, kvCursor, func(next string) error {
    return kv.Put("im.cursor.dingtalk", next) // 建议键：im.cursor.<channel>
}, events)
```

- 初始 `kvCursor` 从 KV 读，空 = 首次；`save` **只在整批事件交付成功且游标推进后**
  调用；save 失败 → Subscribe 返回错误且不推进（下一轮重读，重复由内容派生 ID 吸收）。
- 禁用渠道：cancel ctx（`SubscribeFrom` 会 Close 传输/杀子进程）后清空该 KV 游标。
- `stream` 模式同样可用 `SubscribeFrom`：游标是 Stream 传输自己的（通常不变），
  runtime 可统一按渠道持久化。

检查/授权（runtime 的 `CheckChannel`）：

```go
if a, ok := drv.(interface{ AuthStatus(context.Context) (dingtalk.AuthStatus, error) }); ok {
    st, err := a.AuthStatus(ctx)   // LoggedIn/Account/AuthURL
}
if h, ok := drv.(interface{ DWSHealth(context.Context) (dingtalk.DWSHealth, error) }); ok {
    hh, err := h.DWSHealth(ctx)    // State: ok|auth|error|starting, Detail, AuthURL
}
```

- 未登录：`AuthStatus.LoggedIn=false`，`DWSHealth.State="auth"`、`Detail` 含
  `dws auth login` 与 authUrl；UI 用系统浏览器打开 authUrl 或显示该命令。
- 命令缺失/超时/坏 JSON：`DWSHealth.State="error"`，detail 可直接显示
  （"dws command is not executable: ..." / "dws poll: unreadable JSON..."）。
- **不要**在 runtime 里另起 `dws` 子进程（会绕开串行 runner 导致登录态竞争）；
  检查/授权/发送分别用 `AuthStatus`/`DWSHealth`/`Driver.Send`。

错误与 Outbox（fix-core 配合）：

- `Driver.Send` 失败返回 `*dingtalk.DWSError`：`IsPermanent(err)==true` 表示 4xx/
  参数/命令缺失（Outbox 不应重试）；`Temporary()==true` 表示 5xx/超时（可重试，
  由 Outbox 退避）。`im.IsTransient(err)` 对 `DWSError` 生效。
- DWS 没有服务端幂等键：driver 仍在进程内按业务 key 去重；command client 在文本尾部
  追加 `<!-- zeokeel:<key> -->` 供审计/重复检查。Markdown 标志在 DWS 路径退化为
  纯文本（规格 §2 无 markdown 命令）。

### 测试与验证

新增 `dws_command_test.go`（POSIX 临时脚本冒充 dws）：
auth ok/not-logged-in+URL、poll 解析群聊/单聊/mentions/坏 item 计数/cursor 参数、
send marker+幂等键+**无 shell 插值**（`$(...)`/反引号不执行）、4xx 永久 vs 5xx
可重试、命令缺失、坏 JSON、超时、ctx 取消杀进程（脚本不再写 finished）、health
可选降级、marker 清洗。
新增 `dws_test.go`（注入 fake DWSClient）：消息映射/去重、401→health auth+authURL、
失败退避与恢复、**串行不重叠**（并发 poll/send/auth、maxConcurrent==1）、runner
send 优先于排队 poll、SendText 透传 key、health fallback、Close 语义、driver
`New(Transport:"dws")`+`SubscribeFrom` 游标持久化、DWS Send 文本路径、bot/replyToBot
事实、NoDWS/未知 transport、缺命令→health error、AuthStatus/DWSHealth 暴露。

```text
$ gofmt -l internal/im                                            # 空
$ CGO_ENABLED=0 go vet ./internal/im/...                          # 空
$ CGO_ENABLED=0 go test ./internal/im/... ./internal/sources/... -count=1
ok  internal/im / im/dingtalk / im/feishu / sources / sources/github
$ CGO_ENABLED=1 go test -race ./internal/im/... -count=1           # ok
$ CGO_ENABLED=0 go build ./...                                    # ok（全仓）
$ sh scripts/check-nocgo.sh                                       # PASS
```

### Hardening（runtime 复现的三个失败点）

- 命令缺失：`*exec.Error`、`*fs.PathError`、裸 `exec.ErrNotFound`/`fs.ErrNotExist`
  统一归类为**永久错误**（`Temporary()==false`），detail 为可操作的中英双语
  "dws 未安装或不可执行（not executable）: ..."。
- 取消/超时：`exec.CommandContext` + 显式 `cmd.Cancel` + `cmd.WaitDelay=500ms`
  （Shell 包装脚本的子进程可能握住管道；不设 WaitDelay 会让 cmd.Run 卡住）。
  runner 的排队调用在 ctx 取消后立即返回，且**已取消的 job 不会被执行**（新测试
  `TestDWSRunnerCancelledJobReturnsImmediatelyAndNeverRuns`）；runner 执行 fn 时不持锁。
- marker：HTML 注释内部不能出现 `--`/控制字符/行分隔符。普通 key 原样保留；
  含 `--`/`<>`/控制字符时整键 hex 编码（可反查），测试覆盖 `-->`、控制字符、
  U+2028/2029、`<script>` 等。

未实现（deferred，均有理由）：
- DWS 成员列表/媒体下载（规格没有对应命令；`Members`/`AccessToken` 诚实返回
  `im.ErrUnsupported`）；
- 卡片/事件回调（只适配机器人消息 poll/send/auth/health 四条命令）；
- runtime 的渠道生命周期/设置页/飞书授权检查（规格 §4 归 nih-runtime/nih-ui）；
- DWS `auth login` 启动（UI/CLI 用 authURL 或让用户执行命令；不在串行 runner 里
  跑交互式登录）。

## 行为变化与兼容性风险

1. **GitHub 游标格式升级**（opaque，仅 `internal/sources/github` 解析）：
   `repo|<RFC3339Nano>|<notifETag>|<commentETag>|<ciBranch>|<ciFailures>|<ciRevision>`；
   旧的 `repo|<time>` 仍可读（只是首轮无条件请求）。Wave2 只要把 `PollResult.Next` 原样存 KV 即可，不要解析。
2. **GitHub `since` 推进语义改变**：从"每轮 `started-5s`"改为"最新条目 `updated_at-5s` 且必须更晚"。
   这是 ETag/304 生效的前提（原型同语义）。没有新条目时 since 不动 → 下一轮条件请求可 304。
3. **分页上限失败关闭**：单个流 20 页（2000 条）、marker 50 页（5000 条）是硬上限，超限返回错误并保持游标
   （下一轮重试同样的窗口，会再次失败）——这是有意的"不静默丢数据"取舍，极端忙碌仓库可能需要调大常量。
4. **新增 Raw kind / admission 行**：`issue`/`pull`/`review-comment`/`ci` → 行 `comment`/`issue`/`pull`/`ci`
   （单数，与 `domain.Trigger.Row` 注释、002 门禁表一致）；Trigger 同时带 `ScopeReceivesAll`（评论行看
   `ObserveComments||ReceiveAll`，issue/pull 行看 `ReceiveAll`，ci 恒 true——CI 是广播不是"对 agent 说话"）。
   `Config.ReceiveAll`、`Config.ObserveCI` 默认 false（行为与旧版一致）。
   CI Trigger 的 Thread id 是 `repo#ci:<branch>`，**不是** `repo#<number>`：`SplitThreadID` 会拒绝它（有意），
   按 Thread 订阅/展示的代码不要假设 Thread id 可解析成数字。
5. **CI Trigger 没有 domain 新字段**：状态/分支/sha/revision 放在 `Channel.Address`（`kind=ci, branch, sha, url, state, revision`），
   正文是摘要。若 Wave2 想给面板/引擎一个类型化事件，建议加 `domain.Trigger.Event string`（见跨 scope 待办）。
6. **IM 发送不再在传输层重试**：`im.NoRetry`（Attempts=1）；5xx/429 以 `*im.HTTPError`（含 `RetryAfter`）上报，
   401 仍刷新 token 重试一次。语义变化：以前一次发送失败可能晚 3 次尝试才报错，现在第一次就报，重试由 Outbox 负责。
7. **钉钉 `Subscribe` 对 HTTP 传输立即失败**：返回 `im.ErrUnsupported`（ wrapped）并置 health=error。
   Wave2 接线时这是"来源不可用，需要 Stream 传输"的可解释状态，不是 bug；真实 Stream 传输实现 `StreamSupport`/`SupportsStream() true` 后同一循环可用。
8. **新增导出 API（只增不改）**：`im.NoRetry`、`im.HTTPClient`、`im.TokenCache`、`im.TokenSkew`、`im.RetryAfter`、`im.Snippet`；
   `feishu.HTTPTransport.Now`、`dingtalk.HTTPTransport.Now`（测试注入时钟）；
   `feishu.EventSignature/CheckEventSignature/CheckVerificationToken/DecryptEvent`；
   `dingtalk.StreamSupport`、`dingtalk.HTTPTransport.SupportsStream()`；
   `github.HeaderCommand`、`github.CIThreadID`、`github.RawIssue/RawPull/RawReviewComment/RawCI`。
   删除：`im.HealthOf`（无调用者）。`dingtalk.markerHash/stripMarker` 移入测试文件（同包，外部无影响）。
9. **未做（明确记录）**：官方 Lark/DingTalk Stream/go-github SDK 迁移需要 Lead 批准（新增 go.mod 依赖）；
   IM 媒体**下载**（`im.Sender.FetchMedia` 在设计中存在、实现中没有）：本轮只保证 `RawEvent.MediaRef` 正确；
   GitHub 旧式 commit statuses（`/statuses`）未纳入 CI 快照，只读 check-runs（与原型 `pullChecks` 的主路径一致）。

## 跨 scope 待办（file:line + 期望改动）

1. **fix-runtime（Wave2 接线，必须）**
   - `cmd/zeokeel/extras.go:157` `startIMLoop` 与 `:192` `triggerForIM` 目前无调用者；`cmd` 也没有 IM flag。
     建议：新增 `-feishu-app-id/-feishu-app-secret/-feishu-chats`、`-dingtalk-app-key/-dingtalk-app-secret/-dingtalk-robot-code`
     （或从 settings 读），构造 `feishu.New(feishu.Config{...})` / `dingtalk.New(dingtalk.Config{...})`，
     `selfID, _ := drv.Self(ctx)`，对 Feishu 调 `startIMLoop(ctx, eng, rt, drv, selfID.ID, logger)`；
     DingTalk 接线时 `Subscribe` 会立刻返回 `im.ErrUnsupported` → 把它写进来源页 health（"需要 Stream 传输"），不要重试风暴。
   - `cmd/zeokeel/github.go:58,68,91`：游标是 goroutine 局部变量、`eng.Receive` 直调；Wave2 要改 `Persistence.Receive`
     + kv 持久化 `res.Next`（`sources.Cursor` 原样存），并把 `githubOptions` 扩出 `ReceiveAll`/`ObserveCI` 两个配置项
     （对应 `Config.ReceiveAll` / `Config.ObserveCI`），`self` 列表可透传。
   - 事件推送（可选，Wave2 之后）：若加飞书 webhook，用 `feishu.CheckEventSignature` + `DecryptEvent` + `CheckVerificationToken`
     再交给既有 translate 路径（`internal/im/feishu/mapping.go:15` 同款事实翻译）。

2. **fix-engine（P1）**
   - admission table 需要认识新行（单数）：`comment`（含 review 评论）、`issue`、`pull`、`ci`；
     Source 已把 `Trigger.Row` / `Trigger.ScopeReceivesAll` 填好（`sources/github/trigger.go`），
     gate 直接读，不需要再回调 Source。CI 的 `machineGate` 要求该行 `UnlistedUnaddressed=inferred`
     （默认 cell 是 ignore）才成为候选，Wave2 的默认 Policy/UI 需要给出这个默认值。
   - CI/PR Trigger 的 `Human=false`、`Addressed` 为空；`Channel.Address` 的键契约见上（`kind`、`state`、`branch`、`sha`、`revision`）。
   - 细化的事件形状（review vs 对话评论）仍在 `Channel.Address["kind"]`（`comment`/`review-comment`/`issue`/`pull`/`ci`）；
     若 Wave2 需要类型化字段，可加 `domain.Trigger.Event`，Source 侧一行即可填；本轮未改 domain。

3. **fix-core（P1）**
   - `internal/outbox/outbox.go:280`：发送失败目前一律 `return err`，由 effects 退避重试。因为传输层现在不重试，
     建议区分：`*im.HTTPError` 且 `Temporary()==false`（4xx）→ 视为永久失败写回执，不要重试 3 次；
     `Temporary()==true` 且 `RetryAfter>0` → 用 `im.RetryAfter` 的值作为该次退避（或至少不早于它）。
   - 若 Wave2 需要媒体下载，`im.Sender` 需新增 `FetchMedia(ctx, ref) ([]byte, string, error)`（设计 §4.8 有、实现没有）。

4. **fix-docs-ci（P0/P2）**
   - `.github/workflows/ci.yml:8-10` 全局 `CGO_ENABLED=0` + `:35` `go test -race` 必红（实测 `-race requires cgo`）：
     该 step 单独 `CGO_ENABLED=1` 并注释说明"仅测试运行时，不改变产品零 cgo"，或去掉 `-race`。
   - `README.md:96,126`、`CHANGELOG.md:52`、`design/…:1846` 仍宣称 IM 已实现/钉钉长轮询可用：
     Wave2 接线前应改为"代码存在但未接线"；钉钉接收仍是 `ErrUnsupported`（需要 Stream 传输）。
   - `design/…:182,1603` 的 `lark-cli` 依赖已不存在（Go 端手写 HTTP）；`RELEASE.md:25-31` 未勾选 vs `CHANGELOG.md:8` 的"14 项全过"需对齐。
   - `Makefile:55-60` 的 `dist` 与 `scripts/package.sh:12-21` 重复（可删 `dist`）；CI `window` job（`ci.yml:79-98`）跑 `go run ./cmd/zeokeel`
     不会退出，需 `-demo` 或 `timeout-minutes`。

## 未完成/被阻塞

- **官方 SDK 迁移（Lark v3 / DingTalk Stream / go-github）**：按契约 §2.4 需要 Lead 批准新依赖；本轮用零新依赖方案补齐了
  分页/ETag/重试/幂等/CI，但 WS 长连接与 go-github 的类型化 API 未迁。审批后可作为 Wave2+ 的独立任务
  （迁移草稿见 `audit/nih-integrations.md` F2/F3/F4）。
- **IM 接线**属 Wave2 fix-runtime；本 scope 只把驱动修到"可接线"。
- **真实凭据联调**（飞书/钉钉/GitHub 租户）按审计规则未执行；所有测试都是 httptest / fake Command / fake transport。
