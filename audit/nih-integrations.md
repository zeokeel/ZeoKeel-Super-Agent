# NIH 审计：集成 / 工具链切片（Feishu · DingTalk · GitHub · 构建发布）

> 审计对象：`internal/im`（含 `feishu/`、`dingtalk/`）、`internal/sources`（含 `github/`）、
> `tools/`（`genicon`、`seedstate`）、`scripts/`、`packaging/`、`Makefile`、`.github/`。
> 只读审计：未改任何源文件、未执行 gofmt/go generate、未调用任何 IM/GitHub 真实 API、
> 未发消息、未提交。全部结论的 `file:line` 均指本仓库当前工作树。
>
> 判定口径沿用任务定义：**(a) Go 1.27 stdlib 已提供；(b) go.mod 里已有依赖提供；
> (c) 成熟纯 Go 库提供；(d) 本仓库别的包已实现** —— 四者之一命中即为"不必要自建"；
> 框架表达不了的领域逻辑记为 JUSTIFIED，并给出理由。

## 0. TL;DR（给 Lead 的三条）

1. **P0：飞书/钉钉的真实驱动整体没有接线。** `internal/im/feishu` 与 `internal/im/dingtalk`
   没有任何非测试导入（`go list -deps ./cmd/zeokeel` 里两者都不出现），`cmd/zeokeel` 既无
   `-feishu-*`/`-dingtalk-*` flag，唯一适配函数 `startIMLoop` 也无人调用
   （`cmd/zeokeel/extras.go:157`）。也就是说 **2229 行（非测试）生产代码不进二进制**，
   而 `README.md:96`、`CHANGELOG.md:52` 仍宣称"IM 驱动：飞书与钉钉……已实现"。
   这是本次审计最大的一处"自建但没用上"。
2. **P1：GitHub Source 用 `gh` 子进程，并且丢掉了原型有的 ETag / 分页。** 原型
   `_ref/src/sources/github.ts:200-248` 用 `If-None-Match` + `--include` 拿 304 省 rate limit、
   并翻页；Go 端口每次轮询固定起 2 个 `gh` 进程（`internal/sources/github/poll.go:88,118`），
   单页上限 100/50（`poll.go:22-23`），`find()` 只看第一页 100 条
   （`channel.go:107-131`）——评论多的线程会漏判"已发过"，产生重复评论。
3. **P0（工具链）：CI 的合并闸门自相矛盾、必红。** `.github/workflows/ci.yml:8-10` 全局
   `CGO_ENABLED: "0"`，`:35` 却跑 `go test ./... -race`；Go 明确规定 `-race requires cgo`。
   实测：`CGO_ENABLED=0 go test -race ./internal/im/` →
   `go: -race requires cgo; enable cgo by setting CGO_ENABLED=1`。本地
   `scripts/release-check.sh:38` 不带 `-race`，所以"本地 14 项全过 / CI 红"可以同时成立。

## 1. 范围与覆盖

### 1.1 目录 / 文件 / LOC

| 区域 | 非测试 LOC | 测试 LOC | 读法 |
| --- | ---: | ---: | --- |
| `internal/im/*.go`（im/poll/retry/status/util/addressing/fakes） | 770 | 324 | 全读 |
| `internal/im/feishu/`（driver/mapping/transport） | 1266 | 1015 | 全读 |
| `internal/im/dingtalk/`（driver/mapping/transport） | 963 | 801 | 全读 |
| `internal/sources/`（source/registry） | 289 | 137 | 全读 |
| `internal/sources/github/`（8 个文件） | 1014 | 1444 | 全读 |
| `tools/genicon` + `tools/seedstate` | 330 | 0 | 全读 |
| `scripts/`（check-nocgo/package/release-check/zipdir.py） | 209 | — | 全读 |
| `Makefile` | 75 | — | 全读 |
| `.github/workflows/ci.yml` + README | 98 + 12 | — | 全读 |
| `packaging/`（Info.plist/desktop/install.ps1/icons） | 81 | — | 全读 |
| **合计** | **≈ 4.6k Go + 0.5k 脚本/配置** | **3.7k** | — |

### 1.2 参照物与实测

- 原型：`_ref/src/sources/{feishu*,github*,poll.ts,kind.ts,registry.ts}`、`_ref/src/infra/commands.ts`
  （commit 6337fc8）。原型的设计事实：**飞书走 `lark-cli` 子进程、GitHub 走 `gh` 子进程**
  （`_ref/src/infra/commands.ts:1-3,76,82-84,103`），因此 Go 端"手写 HTTP 飞书客户端"不是移植，
  而是**新增**实现。
- 设计文档：`design/zeokeel-go-mygo-design.md` §0、§1.3、§2、§4.8–4.10、§7.2、§8、§9.4、§11、
  §11-实现状态对照（:1833 起）已读；其中 :182/:1603 把 `gh`、`lark-cli` 列为外部依赖，
  :1768（风险 7）自己写了"`gh` CLI 依赖……M6 评估换 REST 客户端"，:1846 承认"WS 长连接未接"。
- 依赖事实（GOMODCACHE=/root/go/pkg/mod，`go env GOMODCACHE` 实测）：
  `golang.org/x/time@v0.15.0/rate`、`github.com/cenkalti/backoff/v4@v4.3.0`（零依赖）、
  `github.com/gorilla/websocket@v1.5.3`、`golang.org/x/oauth2@v0.35.0`、`golang.org/x/image@v0.46.0`
  均在缓存里；**`github.com/larksuite/*`、`open-dingtalk/*`、`google/go-github`、
  `hashicorp/go-retryablehttp` 不在缓存**（需联网下载）。
- 基线实测：`CGO_ENABLED=0 go test ./... -count=1 -skip 'Live'` **全绿**
  （含 feishu/dingtalk/github 全部包）。`CGO_ENABLED=0 go test -race ...` 必失败（见 TL;DR 3）。
- 限制：未连真实飞书/钉钉/GitHub 租户（审计规则禁止），因此"SDK 能覆盖某能力"的结论
  来自 SDK 源码 / 官方仓库文件（已实际抓取的 URL 内联标注），实际租户联调仍需在迁移时做。

## 2. 总结表

| # | 区域 | 当前实现 | LOC | 判定 | 推荐替代 | 工作量 | 风险 | 优先级 |
| --- | --- | --- | ---: | --- | --- | --- | --- | --- |
| 1 | `internal/im` 契约 + `addressing.go` + `util.go` | 自研 Provider/Receiver/Poller/Sender + Capabilities | 219 | **JUSTIFIED**（领域契约，§4.8；框架无法表达） | — | — | — | — |
| 2 | `internal/im/poll.go` | 长轮询循环 + 游标纪律 | 172 | **JUSTIFIED**（§4.4 不变量表达；无库可替代） | — | — | — | P2 仅去重 | 
| 3 | `internal/im/retry.go` | `Do[T]` 指数退避 + `Retry-After` + `HTTPError` | 130 | **JUSTIFIED**（薄策略层）；可选用 `cenkalti/backoff/v4`，收益不足 | — | S | 低 | P2 |
| 4 | `internal/im/status.go` | `HealthTracker`，与 `sources.Health` 逐字重复 | 82 | **SHOULD-DEDUPE**（(d) 本仓库重复） | 合并到 `internal/sources`（或反向） | S | 低 | P2 |
| 5 | `internal/im/fakes.go` | 两个 mock driver + 假网络（demo/测试用） | 167 | **JUSTIFIED**（§4.8 的"差异只在能力与格式"证明；`-demo` 在用） | — | — | — | — |
| 6 | `internal/im/feishu/` | 手写 HTTP/JSON 客户端（token/发送/成员/历史/卡片/@） | 1266 | **DEAD（未接线）+ SHOULD-USE-SDK** | `github.com/larksuite/oapi-sdk-go/v3`（纯 Go） | M | 中 | P1（若飞书进 1.0） |
| 7 | `internal/im/dingtalk/` | 手写 HTTP 发送/成员 + 空壳 Stream 接口 + 手写 WS 未做 | 963 | **DEAD + 部分 JUSTIFIED**：Stream 用官方 SDK；HTTP 发送 ~120 行可保留 | `open-dingtalk/dingtalk-stream-sdk-go`（纯 Go） | M | 中 | P1 |
| 8 | `internal/sources/source.go` + `registry.go` | Source/Channel 端口 + 编译期注册表 | 289 | **JUSTIFIED**（原型 `registry.ts` 的合理精简；无反射） | — | — | — | — |
| 9 | `internal/sources/github/` | `Command` 封装 `os/exec` 调 `gh`，2 进程/轮询，无 ETag/分页 | 1014 | **SHOULD-USE-FRAMEWORK** | `github.com/google/go-github/v93` + 复用 `golang.org/x/oauth2` + `gh auth token` 取凭据 | M | 中 | P1 |
| 10 | `feishu/`+`dingtalk/` 的 HTTP 管线（`call/attempt/getJSON/postJSON/retryAfter/snippet`） | 两份几乎逐字相同的副本 | ≈ 240（重复部分） | **SHOULD-DEDUPE**（(d)）；补 `golang.org/x/time/rate` | 抽 `internal/httpx` 或 `internal/im/httpclient` | S | 低 | P2 |
| 11 | `tools/genicon` | stdlib 绘制 + 手写 ICO/ICNS 容器 | 207 | **LOW-PRIORITY / 基本 JUSTIFIED**；`sqrt` 应用 `math.Sqrt` | 保留容器代码；删手写开方 | XS | 低 | P2 |
| 12 | `tools/seedstate` | 开发用状态播种；`-allow` 解析坏的 | 123 | **LOW（近似 DEAD）** | 修 `fmt.Sscanf` 或删；`splitCSV`→`strings.Split` | XS | 低 | P2 |
| 13 | `scripts/check-nocgo.sh` | 四步 cgo 守卫 | 46 | **JUSTIFIED**（设计 §9.4 明确要，行业无标准替代） | — | — | — | — |
| 14 | `scripts/release-check.sh` + `ci.yml` | 手写发布闸门 + 4 job CI | 83+98 | **JUSTIFIED（脚本本身）**，但 **CI 有 P0 缺陷** | 修 `-race`；给 `window` job 加超时/退出 | XS | 低 | **P0** |
| 15 | `scripts/package.sh` + `zipdir.py` + `Makefile` + `packaging/` | 3 平台交叉编译 + .app/.desktop/ps1 + zip | 214+81 | **JUSTIFIED（小仓库）**；`Makefile dist` 与 `package.sh` 重复 | 删 `Makefile dist`，或迁 GoReleaser（可选） | S | 低 | P2 |

判定计数：**SHOULD-USE-FRAMEWORK/SDK 3**（#6 #7 #9）、**SHOULD-DEDUPE 2**（#4 #10）、
**DEAD/近似 DEAD 2**（#6 #12，其中 #6 同时是 SDK 项）、**LOW-PRIORITY 2**（#11 #12）、
**JUSTIFIED 8**（#1 #2 #3 #5 #8 #13 #14 #15）。合计 15 行、涵盖全部 8 个任务问题。

## 3. 详细发现

### F1 [P0][DEAD] 飞书 / 钉钉真实驱动没有接线（2229 行不进二进制）

**事实**

- `internal/im/feishu`、`internal/im/dingtalk` 的导入者只有它们自己 + 自身测试：
  `grep -rn "im/feishu\|im/dingtalk" --include=*.go .` 除 `_ref/` 外无第三方导入。
- `go list -deps ./cmd/zeokeel` 出现 `internal/im`、`internal/sources`、`internal/sources/github`，
  **不出现** feishu/dingtalk 两个包 → 发布二进制里没有它们（`go build ./...` 会编译，但不链接）。
- `cmd/zeokeel` 没有任何飞书/钉钉 flag（`cmd/zeokeel/main.go:30-54` 只有 model/repo/skills/mcp…），
  唯一把驱动接到 Engine 的适配器 `startIMLoop`（`cmd/zeokeel/extras.go:157-190`）与
  `triggerForIM`（`:192-210`）**无任何调用点**。
- 唯一在跑的是 `-demo` 走查里的假驱动：`cmd/zeokeel/main.go:392-393`
  （`im.NewTransport()` + `im.DingTalk{...}`），属于演示代码。
- 对外文档与代码不一致：`README.md:96`（"飞书/钉钉需要各自的应用凭据"）、
  `README.md:126`（"当前用 HTTP 长轮询兜底"）、`CHANGELOG.md:52` 都把 IM 驱动列为已实现；
  design 的 §11 对照表 `:1846` 也标 ✅（只注"WS 未接"）。实际是**没有任何入口能创建真实飞书/钉钉来源**。

**判断**：这不是"重复造轮子"，而是**造了轮子没装车**。对本审计而言结论更重：
在决定"飞书/钉钉是否 1.0 范围"之前，对这 2229 行的任何"换 SDK / 补 WS"建议都不产生用户价值。

**建议（二选一，先做决定再动代码）**

- A. **1.0 明确不含 IM**：删除或移入 `spike/`（或加 `//go:build imdriver` tag），
  README/CHANGELOG/design §11 同步改为"未接线/未验证"；保留 `internal/im` 契约 + fakes（demo 在用）
  与 `internal/im/poll.go`/`retry.go`（未来驱动复用）。收益：仓库承诺与代码一致，少维护 2.2k 行。
- B. **1.0 含 IM**：按 F2/F3 用官方 SDK 接线（不要继续补手写 WebSocket），并把
  `startIMLoop` 接上 `runtime`。工作量 M，但这是唯一能让"IM 驱动"宣称成立的路。

### F2 [P1][SHOULD-USE-SDK] Feishu（Lark）手写客户端 vs 官方 Go SDK

#### 现状清单（hand-built inventory）

| 能力 | 项目实现（file:line） | 官方 `oapi-sdk-go/v3` 是否覆盖 | 纯 Go？ | 迁移难度 |
| --- | --- | --- | --- | --- |
| tenant_access_token 缓存 + 提前刷新 | `feishu/transport.go:182-212`（mutex + 10%/1min skew；无 singleflight，并发下会打多次 token 接口） | ✅ `core/tokenmanager.go`（token 缓存/刷新；配置项见 `core/config.go`：`EnableTokenCache`/`TokenCache`） | ✅ | S |
| HTTP 客户端 / 鉴权头 / 超时 | `transport.go:364-406`（手写 attempt） | ✅ `core/httptransport.go`；`larkcore.Config.HttpClient/ReqTimeout` | ✅ | S |
| 发消息 / 回复 | `transport.go:227-251`（`/im/v1/messages`、`/{id}/reply`、`receive_id_type=chat_id`） | ✅ `service/im` 生成 API + README "Channel Module"（streaming replies） | ✅ | S |
| markdown→interactive 卡片 | `feishu/mapping.go:252-259`（最小 card JSON） | ✅ `card` 包（`ws/client.go` 里 `larkcard` 导入）、Channel module | ✅ | S |
| 成员分页 | `transport.go:254-278` + `driver.go:141-162` | ✅ `service/im` 的 chat members | ✅ | S |
| 历史消息增量（长轮询） | `transport.go:281-313` + `driver.go:245-288`（毫秒 + page_token + Seen 去重） | ⚠️ `service/im` 有 messages list，但**轮询游标语义（含 Seen）仍需自己维护** | ✅ | M |
| @ / @所有人 / 机器人判定 | `mapping.go:139-172`、`addressing.go:14-34` | ❌ 领域逻辑，SDK 不管（保留） | — | — |
| 事件签名校验（SHA256 + token） | **未实现**（无 webhook/事件入口） | ✅ `event/dispatcher.dispatcher.go` `VerifySign` / `verifyVerificationToken` | ✅ | — |
| AES 事件解密（CBC + paddings） | **未实现** | ✅ `DecryptEvent` → `larkevent.EventDecrypt`；`core/utils.go` 有 `EncryptedEventMsg` | ✅ | — |
| 长连接 WebSocket（桌面首选） | **未实现**（`driver.go:290-300` 明确用长轮询兜底） | ✅ `ws/client.go`：`NewClient`、自动重连（`reconnectCount/Interval`、`pingInterval`）、`WithEventHandler`、`WithHttpClient`、`WithDomain` | ✅ | S |
| 文件/图片上传、编辑/撤回、已读 | 全部 `Capabilities` 标 false（`driver.go:107-116`） | ✅ Channel module（media uploads）+ `service/im` | ✅ | S |
| 限流（客户端预限速） | **未实现**（只有 429 重试） | ❌ SDK 不内置 limiter（仅 `ReqTimeout`） | — | — |

**"官方 SDK 纯 Go"证据（实际抓取）**

- `https://raw.githubusercontent.com/larksuite/oapi-sdk-go/v3_main/go.mod`：
  `module github.com/larksuite/oapi-sdk-go/v3`，require 仅
  `github.com/gogo/protobuf v1.3.2`、`github.com/gorilla/websocket v1.5.0` —— 两者均无 cgo；
  且 `gorilla/websocket@v1.5.3` 已在 GOMODCACHE。
- 对照：**不带 `/v3` 的旧模块** `raw.githubusercontent.com/larksuite/oapi-sdk-go/main/go.mod` 要求
  `gin-gonic/gin`、`go-redis/redis/v8`、`logrus` —— 所以必须锁 `/v3`，否则体积/依赖显著变差。
- 风险提示（必须显式说明）：以上为 go.mod + 源码层面的"无 cgo 迹象"，
  **落地时必须跑 `scripts/check-nocgo.sh` 第 3 步（CGO_ENABLED=1 再审计）实测**，
  这是本仓库 §9.4 的既定纪律，不是本审计的结论。

**迁移草图（保持现有不变量）**

1. `Transport` 接口（`feishu/transport.go:37-48`）保留不动，新增一个
   `sdkTransport` 实现，内部持有 `*lark.Client`；`NewHTTPTransport` 只换实现。
   `BaseURL` → `larkcore.Config.BaseUrl`，`Client` → `Config.HttpClient`：
   **现有 `transport_test.go` 的 httptest 形状断言几乎可以原样复用**（同一批
   `/open-apis/...` 路径），这是本迁移成本低的关键。
2. token 缓存/刷新交给 SDK；删除 `token/tokenUntil/mu` 与 401 刷新环（`transport.go:155-212,339-362`）。
   注意：SDK 的 401 语义与本项目 `im.Do` 的外层重试要**只保留一层**，避免双重重试放大。
3. `Subscribe`（`driver.go:292-300`）改成真 WS：`larkws.NewClient(appID, appSecret,
   larkws.WithEventHandler(dispatcher), ...)` + `Start(ctx)`；`ws/client.go` 已负责
   ping/reconnect/断线。`Poll` 保留为无凭据/受限网络下的 fallback。
4. 幂等不变量不变：`Send` 继续用 Outbox key 作为 `uuid`（`driver.go:171-175`）。
   WS 收到的事件仍走 `RawEvent` + 内容派生 ID（`im.DeriveKey`），重复事件天然幂等。
5. `ReplyToBot` 可升级为权威判定：现在是进程内 512 条 own-message 缓存
   （`driver.go:17-24,325-351`），重启后父消息 ID 不在缓存就保守判 false
   （`mapping.go:41-43` 注释自认）；用 SDK 读父消息的 sender 可变成权威事实（多一次 API 调用，可缓存）。

**规模/收益估算（inference，未实测）**：feishu 包 1266 → 约 450–550 行
（driver 的 Poll/ReadThread/cursor/mapping 基本保留，transport 的 ~200 行信封与 ~120 行 HTTP 管线删除）；
二进制增量估计 +2~4 MB（生成 API + protobuf），仍在 §9.4 的 40 MB 预算内，但迁移后要跑
`make size` 与 `check-nocgo.sh` 验证。

### F3 [P1][SHOULD-USE-SDK + 部分 JUSTIFIED] DingTalk

**拆分判断（重要）**

- **Stream（接收）——SHOULD-USE-SDK。** 现状是空壳：`Transport.StreamEvents`
  直接返回 `ErrUnsupported`（`dingtalk/transport.go:276-281`），
  `Driver.Subscribe` 实际跑的是 `im.SubscribeLongPoll`（`dingtalk/driver.go:207-215`），
  也就是说**钉钉在当前实现下 100% 收不到消息**（HTTP 传输上 Poll 永远返回 ErrUnsupported → 长轮询循环永远退避）。
  README `:126` 承认"WS 未实现"。官方 `github.com/open-dingtalk/dingtalk-stream-sdk-go`
  （抓取 `raw.githubusercontent.com/open-dingtalk/dingtalk-stream-sdk-go/main/go.mod`）：
  require 仅 `google/uuid`、`gorilla/websocket`、`testify` → **纯 Go**；
  仓库顶层目录 `client/`（Stream 客户端）、`handler/`、`chatbot/`、`card/`、`event/`
  （抓取 `api.github.com/repos/open-dingtalk/dingtalk-stream-sdk-go/contents/` 得到；
  仓库描述即 "Go SDK for DingTalk Stream Mode API … easier to access the DingTalk chatbot"）。
  迁移：新增一个实现 `Transport.StreamEvents` 的适配器（SDK handler 回调 → 现有 `Event` 结构体，
  `dingtalk/transport.go:93-116` 已定义好字段），driver/mapping 不动。工作量 M。
- **HTTP 发送/成员——JUSTIFIED（保留手写）。** 只覆盖 3 个端点：
  `POST /v1.0/oauth2/accessToken`、`POST /v1.0/robot/groupMessages/send|oToMessages/batchSend`、
  `GET /v1.0/im/chatMembers`（`transport.go:170-274`），合计约 120 行有效代码。
  官方 OpenAPI 生成 SDK `github.com/alibabacloud-go/dingtalk`
  （抓取 `raw.githubusercontent.com/alibabacloud-go/dingtalk/master/go.mod`）依赖
  5 个阿里云 SDK 模块（darabonba-openapi/v2、gateway-dingtalk、openapi-util、tea、tea-utils/v2），
  生成代码量巨大、API 面远大于本项目所需 —— 为 3 个端点引入整套 aliyun tea 栈是**反向的 NIH**。
  结论：钉钉不要整体迁官方 OpenAPI SDK；只取 Stream SDK。

**顺带发现的两个真实缺陷（比"NIH"更值得修）**

1. **重试层与幂等层脱节。** 钉钉发送接口无服务端幂等键（`dingtalk/driver.go:127-133` 自认），
   driver 用进程内 `sent` map 去重；但 **transport 内部 `im.Do` 会重试 POST**
   （`dingtalk/transport.go:304` 包住 203-242 的 `SendRobot`）。若第一次 POST 已被服务端接受、
   响应丢失，`im.Do` 会再发一次 → driver 的 `sent` 缓存根本来不及生效 → **重复消息**。
   对比飞书：uuid 由服务端去重（`feishu/driver.go:171-175`），同样重试是安全的。
   修法：发送类请求 `RetryPolicy{Attempts: 1}`，把重试交给 Outbox（key 幂等）；
   或把重试提到 driver 层、命中 `sent` 缓存后直接回执。这是"手写 transport 把两层职责放错位置"的直接后果。
2. **不可见 marker 是 justified 的领域 workaround，但不能当幂等。** 零宽编码
   （`dingtalk/driver.go:283-303`）写进正文尾部，注释明确说"跨进程无法阻止重复"；
   保留它是合理的审计线索，别把它算成幂等实现。

### F4 [P1][SHOULD-USE-FRAMEWORK] GitHub：`gh` 子进程 → go-github + oauth2 + `gh auth token`

#### 现状

- `Command` 接口 + `ExecCommand`：`internal/sources/github/command.go:17-21,73-93`，每个 API 调用
  起一个 `gh` 进程；HTTP 状态码靠 **正则解析 stderr**（`command.go:48-62`：
  `(?i)\bHTTP[ /]?(\d{3})\b`，注释说 gh 形如 `"gh: Not Found (HTTP 404)"`）。
- 一次轮询固定 2 个 `gh` 进程：通知（`poll.go:85-108`，`per_page=50`）+ 评论
  （`poll.go:115-171`，`per_page=100`）；发送时再加 1 个"先查"（`channel.go:107-110`）+ 1 个 POST
  （`channel.go:63-65`）；健康检查 1 个（`members.go:45-47` `gh api user --jq .login`）；
  成员同步 1 个（`members.go:16-21`）。
- **无 ETag / 条件请求**：全仓库无 `If-None-Match`/`304` 处理。
  原型有：`_ref/src/sources/github.ts:200-216,246-248`（存 etag、`--include`、304 当成功且不耗 rate limit）。
- **无分页**：`commentPageSize=100`/`notificationPageSize=50` 硬上限（`poll.go:20-24`），
  注释写"翻页留到需要时再加"（`poll.go:21`）。后果（verified by code）：
  - 一次轮询窗口内通知 >50 条 → 多出的**静默丢失**（既不进 `Unreadable` 也不进 `Filtered`）；
  - 应用休眠/离线后重启，`since` 回溯到上次 `started-5s`（`poll.go:18,203-219`），
    若这期间评论 >100 条 → 只处理第一页，其余**静默丢失**；
  - `find()`（`channel.go:107-131`）只在第一页 100 条里找 marker：线程 >100 条评论时
    找不到上次写的 marker → 重发评论（**破坏 §7.2 出站幂等**）。
- **状态码分类靠自由文本**：`isGone`（`channel.go:145-158`，410 / 403+locked）依赖 `httpCode`
  从 stderr 抓码；gh 输出格式变化即失效（design §11 风险 7 已列）。
- **功能面比原型窄**：`admissionRow` 只映射 `comment`（`scope.go:66-74`）；无 Issue 正文、
  无 PR、无 CI、无 review thread（原型 `_ref/src/sources/github.ts:7-9` 引了
  `branchCiTrigger/ciTrigger/issueTrigger/pullTrigger/commentTrigger` 五类）；
  注释在 `poll.go:129-130` 声称"按 API 的 updated 升序"，但 GitHub 的
  `GET /repos/{o}/{r}/issues/comments` 不支持 `sort/direction` 参数（推断：参数被忽略、
  实际按 id 升序，行为无害但注释与协议不符）。

#### `gh` 是不是"合理的鉴权捷径"？

- 合理之处（保留）：`config.go:1-6` 的理由成立——认证、企业域名、私有仓库权限都交给 `gh`；
  原型同样如此；design `:1768` 也计划"M6 评估换 REST 客户端"。
- 不合理之处：**为了鉴权，把整个 API 访问都外包给进程**，代价是 ETag/分页/typed error/CI/PR
  全部缺失，且每个轮询起 2 个进程（推断：gh 冷启动约几十~百毫秒，60s 轮询即每天 ≥2880 次进程创建；
  未实测）。
- **最优解：`gh` 只当凭据来源，不当传输。**
  `go-github/v93`（抓取 `raw.githubusercontent.com/google/go-github/master/go.mod`）：
  `go 1.26.0`，require `google/go-cmp`、`google/go-querystring`、`golang.org/x/oauth2 v0.37.0`
  → 纯 Go；而 **`golang.org/x/oauth2` 已在 go.mod 且已在构建图里**
  （`go mod why golang.org/x/oauth2` → `internal/mcp` → `modelcontextprotocol/go-sdk/mcp`），
  `go-querystring` 已在 GOMODCACHE。凭据用 `gh auth token --hostname <host>` 取一次
  （仍是用户已登录的 gh，无新凭据栈），或读 `GH_TOKEN`。

#### 迁移草图

1. 新增 `internal/sources/github/client.go`：`*github.Client`（`BaseURL` 可注入 httptest，
   `UploadURL` 不用），`oauth2.StaticTokenSource`。
2. `Command` 接口保留给**凭据发现**（`gh auth token`）与"用户自定义 wrapper"（`Config.CLI`），
   删除 `httpCode` 的 stderr 正则；错误映射改 `*github.ErrorResponse.Response.StatusCode`
   （410/403-locked → `Gone`，与 `channel.go:145-158` 语义保持一致）。
3. **恢复 ETag**：把 `Cursor` 从 `repo|RFC3339` 扩展为 `repo|since|etag`（`poll.go:194-196`），
   用 `http.Client` + `If-None-Match` 或 go-github 的 `Response` 头；304 → 不推进 since、
   不计 unreadable（原型 `github.ts:246-248` 的行为）。
4. **分页**：`ListComments`/`ListNotifications` 用 `Response.NextPage` 循环，直到空页；
   `find()` 同理翻页（修 repeated-comment bug）。保留"游标只在整轮成功后推进"的契约
   （`poll.go:37-43,77-79`）——这是 §4.4 不变量，不能动。
5. Trigger 映射（`trigger.go:53-122`）、ThreadID（`thread.go:15-30`）、`IsBot`/`MentionsAgent`
   （`trigger.go:127-181`）**全部保留**，它们是领域事实层，与传输无关。
6. 测试：`fakeCommand`（`fake_test.go:20-38`）需要替换为 `httptest.Server` + go-github 的
   `BaseURL`；现有 1444 行 github 测试里约 60–70% 是映射/scope/cursor 的纯函数测试（不受影响），
   受影响的约 300–400 行（poll/channel/members/health）需改写。工作量 M。
7. 迁移后 `gh` 从"运行时依赖"降为"首次配置时的一次性命令 + 可选 wrapper"，README `:96`
   与 design `:1603` 可改口径。

### F5 [P2][SHOULD-DEDUPE] 两套 IM transport 的 HTTP 管线是复制粘贴

`feishu/transport.go` 与 `dingtalk/transport.go` 的以下函数**逐字相同或仅差 header 名/错误前缀**：

| 函数 | feishu | dingtalk | 差异 |
| --- | --- | --- | --- |
| `getJSON` / `postJSON` / `postJSONQuery` | :317-335 | :285-299 | feishu 多 query 参数 |
| `call`（重试 + 401 刷新 token 再试一次） | :339-356 | :301-320 | 鉴权头名不同 |
| `attempt`（构造请求、限流 body、限流读取、状态码→`HTTPError`） | :364-406 | :322-362 | 仅 `Content-Type`/header/注释 |
| `retryAfter` | :409-415 | :364-370 | 完全相同 |
| `snippet` | :417-423 | :372-378 | 完全相同 |

合计约 240 行重复。**建议**：抽 `internal/im/httpclient`（或 `internal/httpx`）：
一个 `Client{BaseURL, HTTPClient, Retry, AuthFn, Header}` + `DoJSON(ctx, method, path, query, body, out)`
+ `RetryAfter`/`Snippet` 公共函数；driver 只保留协议方法。工作量 S，测试可复用现有 httptest 服务器。
若采用 F2/F3 的官方 SDK，这部分重复会自然消失一半（Feishu 交给 SDK），所以**顺序上先做 F1 决策/迁移，再决定是否抽公共层**。

**限流**：现状只有被动 `429` 重试（`feishu/transport.go:401-403`、`dingtalk/transport.go:358-360`），
无客户端预限速；`Poll` 一轮内对每个 chat 串行请求，chat 多时仍可能成簇打满。
`golang.org/x/time/rate` **已在构建图里**（`go mod why golang.org/x/time` →
`internal/mcp` → `modelcontextprotocol/go-sdk/mcp` → `rate`；实际使用点
`$(go env GOMODCACHE)/github.com/modelcontextprotocol/go-sdk@v1.8.0/mcp/logging.go:146`
`rate.NewLimiter(rate.Every(opts.MinInterval), 1)`），所以引入 `rate.Limiter` **零新增依赖、零体积增量**。
建议：每个 driver 一个 `rate.Limiter`（按工具/会话分桶），`Do` 前 `Wait(ctx)`；SDK 迁移后仍然需要（官方 SDK 不内置 limiter）。

### F6 [P2][JUSTIFIED] `internal/im/retry.go`：薄策略层，不值得换库

- `Do[T]`（:84-106）+ `IsTransient`（:68-80）+ `Temporary`（:59-66）+ `sleepCtx`（:118-129）＝130 行，
  刻意只重试"暂时性"错误、尊重 `Retry-After`、`context` 取消立即返回——这是**策略**，不是样板。
- 候选 `github.com/cenkalti/backoff/v4@v4.3.0` 在 GOMODCACHE：**零依赖、纯 Go**
  （`go.mod` 只有 `module`+`go 1.18`；`retry.go:37 Retry`、`:42 RetryWithData[T]`、
  `exponential.go:87 NewExponentialBackOff`）。但它**没有 Retry-After 支持**
  （`grep -rn "RetryAfter\|Retry-After" backoff/v4@v4.3.0` 无结果），要自己包一层 `BackOff`
  才能保持现语义 → 净收益 ≈ 0，还多一个直接依赖。
- `github.com/hashicorp/go-retryablehttp` **不在缓存**（需下载），且它是 HTTP 层重试，
  与 `call()` 里"401 刷新 token 再试"的外层循环叠加会形成双重重试 —— 明确不建议。
- `golang.org/x/time/rate` 解决的是**限流**不是重试，两者互补（见 F5）。
- 结论：保留，只做两处小改：(1) 加 jitter（避免多实例同抖）；(2) `Retry-After` 目前只解析整数秒
  （`feishu/transport.go:409-415`），按 RFC 9110 还可能是 HTTP-date，可加
  `http.ParseTime` 兜底。这两处是改进，不是"必须换库"。

### F7 [P2][JUSTIFIED] `internal/sources` 契约 + 注册表：干净的移植，不是重造

- `source.go` 的三件套（`Kind` :55-69、可选 `Poller` :73-87、`Channel` :129-134）＋
  `Raw`/`ScopeResult`/`PollRequest`/`PollResult`/`Cursor` 与原型 `_ref/src/sources/kind.ts:85-115`
  的 `SourceKind` 端口一一对应；`Poll` 契约（游标只在整轮成功后推进、坏数据逐条跳过计数）
  直接落在接口注释里（`source.go:71-87`），这是把 §4.4 不变量写进类型系统的做法，**框架无法替代**。
- `registry.go` 91 行：`sync.RWMutex` + `map[Name]Factory` + `init()` 注册 + 重名 panic
  （:16-46,61-91）。**无反射、无插件加载、无魔法**；原型 `_ref/src/sources/registry.ts`
  的 `SourceRegistry` 承担配置校验/policy 校验/health claim（:13-60+），Go 端口有意只保留
  编译期注册，配置校验放在 `New()`（`github/config.go:82-106`）。这是**合理精简**，不是残缺，
  也远小于"手写插件框架"的规模。
- 唯一可说的：`Health`/`HealthState` 与 `internal/im/status.go` 重复（见 §5）。

### F8 [P2][LOW-PRIORITY] `tools/genicon`：容器格式手写可接受，`sqrt` 是纯 stdlib 重复

- `image/png` 已正确使用（:40-46）；ICO（:150-174）与 ICNS（:176-202）容器各约 25 行，
  且 `golang.org/x/image` **不提供** ico/icns 编码器（`x/image@v0.46.0` 有 bmp/ccitt/tiff/riff/vector/draw，
  无 ico/icns），第三方 `biessek/golang-ico`/`JackMordaunt/icns` 是单文件小库。
  考虑到这是**一次性开发工具**、产物已提交（`packaging/icons/` 8 个 png + .ico + .icns）、
  且不随应用运行，判定 **JUSTIFIED / LOW-PRIORITY**，不值得引依赖。
- 真问题：`sqrt`（`tools/genicon/main.go:115-124`）用 30 次牛顿迭代重写 `math.Sqrt`
  —— 这是 **(a) stdlib 已有** 的教科书级重复，改成 `math.Sqrt` 即可（XS）。
- 小瑕疵：`bar`/`disc` 的 `size float64` 参数未使用（:126,:136），`go vet` 不报；
  无抗锯齿是产品选择（注释 :82 说明），不算问题。

### F9 [P2][LOW / 近似 DEAD] `tools/seedstate`：开发工具，且 `-allow` 是坏的

- 定位明确：`tools/seedstate/main.go:1-7` 自述"development tool, not part of the shipped app"，
  不被 `cmd/` 引用、不在 Makefile/CI 里，只在 `design/prompts.md:145` 提到。
  它调用生产 API（`store.SaveSettings`、`engine.NewPersistence/Register`、`outbox.Open`），
  **不复制生产逻辑**，只是把向导的行为脚本化 → 不算 NIH。
- 但它部分重复了 `cmd/zeokeel/main.go:252-352`（`registerWorkspaces`/`seed`）的播种序列；
  且 `-allow` 解析是**坏的**：
  `tools/seedstate/main.go:72` `fmt.Sscanf(*allow, "%[^:]:%s", &source, &actor)` ——
  Go 的 `fmt` **不支持 `%[...]` scanset**（实测输出 `bad verb '%[' for string`），
  所以只要传 `-allow` 就必然报错退出。修法：`strings.Cut(*allow, ":")`（顺带 `splitCSV`
  :106-118 换成 `strings.Split`，`fmt.Sprint(n)`（`github/trigger.go:194`）换成 `strconv.Itoa`）。
  另一个选择是直接删除该工具（只有设计文档引用它）。

### F10 [P0][TOOLING] CI / 发布闸门：脚本本身 JUSTIFIED，配置有 P0 缺陷

- **闸门必红**：`ci.yml:8-10` 全局 `CGO_ENABLED: "0"`；`:35` `go test ./... -race`。
  实测 `CGO_ENABLED=0 go test -race` → `-race requires cgo`。两种修法都只需一行：
  - (a) 该 step 覆盖 `CGO_ENABLED: "1"`（官方 race detector 需要 cgo 运行时；**这只影响测试**，
    不改变 `CGO_ENABLED=0` 的发布约束——但与本项目"硬规则 CGO_ENABLED=0 everywhere"的字面
    冲突，**必须在 CI 注释里显式写明理由**，否则下一个维护者会把它当违规删掉）；
  - (b) 去掉 `-race`（保留 `CGO_ENABLED=0 go test`），另开一个可选的 `race` job。
  **冲突必须显式记录**，这正属于任务里"若官方 SDK 拉 cgo 要显式标注冲突"的同类问题。
- **`window` job 会挂到超时**：`ci.yml:79-98` 在 xvfb 里 `go run ./cmd/zeokeel`（无 `-demo`/
  `-headless`），应用是常驻窗口/托盘事件循环，不会退出（推断：窗口事件循环无自动退出路径）；job 无 `timeout-minutes`，
  `continue-on-error: true` 只能让失败不阻断，挂起仍会烧 CI 分钟数。修法：加 `-demo`（跑完退出）
  或 `timeout-minutes: 5` + 显式 kill。
- **没有 release job**：产物完全靠本地 `scripts/package.sh`；`dist` 不进 CI。对 1.0 可接受
  （发布动作本就要求 macOS 签名/Windows 真机），但 `CHANGELOG.md:16` 的
  "三平台安装包可从 CI 产出并安装"实际不成立（README `:116` 只写本地 make）。
- `scripts/check-nocgo.sh`：**JUSTIFIED**。四步法（:15-44）里第 3 步"`CGO_ENABLED=1` 再审计"
  是设计 §9.4 实测得出的关键一步（负例 `mattn/go-sqlite3` 只有这步能抓），没有标准工具可替代；
  `release-check.sh:46` 复用它是正确复用，不是重复实现。
- `scripts/release-check.sh`：83 行、7 组共 14 项机械检查 + SKIP 分类（:72-76），**JUSTIFIED**。
  小瑕疵：`:38-39` 把 `go test ./...` 跑了两遍（一遍判通过、一遍数用例）；
  `:31-35` 的 TODO/FIXME grep 与 CI 无重叠（CI 没有），可以保留。
- 判定：脚本是"小仓库的合理自建"，不建议为它引入 goreleaser 才能做的检查；
  **但 CI 配置的两个缺陷必须修（P0/P2）**，否则 1.0 的"check 闸门全绿"无法自证。

### F11 [P2][JUSTIFIED / 可选 GoReleaser] packaging + Makefile

- `scripts/package.sh`（58 行）做 3 平台交叉编译 + Linux tar.gz/.desktop + macOS .app（plist sed
  + icns + UNSIGNED.txt）+ Windows zip/ps1；`zipdir.py`（21 行）注释解释了为什么不用 `zip -r`
  （本机文件系统上 `zip -r` 报 "Nothing to do"）——**具体、可读、无隐藏行为**。
  GoReleaser 能替代"交叉编译 + 归档 + checksum + GitHub Release"，但 `.app` bundle 的
  plist 替换、`install.ps1`、`UNSIGNED.txt` 仍要 `extra_files`/hooks 自定义；对一个 3 平台、
  无签名、手动发布的小仓库，**当前做法 JUSTIFIED**。若将来要 CI 自动发版/校验和，再迁 GoReleaser（S）。
- `packaging/` 的 plist/desktop/ps1 都是必需产物，无重复。
- 真重复：`Makefile:55-60`（`dist`）与 `scripts/package.sh:12-21` 各自写了一遍 3 平台
  `go build -tags mygo_noinspector -ldflags ...`；`Makefile:42-44`（`size`）又是第三份。
  建议删 `dist`（`package` 已覆盖）或让 `dist` 调 `package.sh`；`size` 可保留（它只编 linux/amd64）。
- `.github/workflows/README.md` 与 `ci.yml` 的"4 个 job"描述一致，但 README 未提
  `window` job 会挂（见 F10）。

## 4. 正确使用了框架 / 依赖的地方（不要动）

| 位置 | 用对了什么 |
| --- | --- |
| `internal/im/*` | 纯 stdlib（`context`/`sync`/`time`/`crypto/sha256`）；接口抽象是自己该做的事，没有为"看起来现代"引框架 |
| `internal/im/retry.go:84` | 泛型 `Do[T]` + `context` 取消；`errors.As` 判错而不是字符串匹配 |
| `feishu/transport.go:236-238`、`dingtalk/transport.go:253` | `url.Values`/`url.PathEscape` 构造 query/path，无手拼 URL 编码 |
| 两个 transport | `net/http` + `io.LimitReader`（1 MiB body cap，`feishu:31`/`dingtalk:34`）+ `httptest` 测请求形状；没有自写 HTTP |
| `feishu/mapping.go:245,257`、`feishu/driver.go:438-439` | JSON 编码/游标统一走 `encoding/json` + `base64.RawURLEncoding`，没有手写 parser/encoder（钉钉的 `quote` 是例外，见 §5） |
| `mapping.go`（两驱动） | 消息类型分派用普通 `switch` + 匿名 struct 解码，正确避免了"通用消息框架"这种过度设计 |
| `sources/github/command.go:73-93` | 需要外部 CLI 时用 `os/exec` + `CommandContext`（可取消）+ 明确隔离，而不是自己实现进程通信 |
| `sources/github/thread.go:40-54` | `regexp` 解析 API/网页 URL，一个正则覆盖多种形态，没有为每种 URL 写一段 |
| `tools/genicon/main.go:40` | `image/png` 编码（没有手写 PNG） |
| `scripts/check-nocgo.sh:21,33` | 用 `go list -deps -f {{if .CgoFiles}}` 这个**官方元数据接口**，而不是扫源码猜 |
| `Makefile:24`、`ci.yml:17-20` | 用 `go tool mygo vet`、`actions/setup-go@v5` 这些上游推荐路径 |
| `cmd/zeokeel/github.go:14,41` | Source kind 用 blank import + `sources.New`，注册表与构造解耦，没有运行时反射加载 |

## 5. 重复的内部工具（同一能力多份实现）

| 能力 | 重复点 | 建议 |
| --- | --- | --- |
| `Retry-After` 解析 | `feishu/transport.go:409-415` ≡ `dingtalk/transport.go:364-370` | 提 `internal/im` 公共函数（或随 F5 抽 `httpx`） |
| 响应体片段 | `feishu:417-423` ≡ `dingtalk:372-378` | 同上 |
| JSON 字符串转义 | `dingtalk/driver.go:278-281` `quote()` 手写 `\`/`"`/`\n`/`\r`/`\t` | **(a)** 用 `encoding/json`（`json.Marshal` 一个 map 再取字符串）。当前实现只转义 `\`/`"`/`\n`/`\r`/`\t` 五个字符，正文里其余控制字符（0x00–0x1F，如 `\b`、`\f`）会产出**非法 JSON**；且与 `textParam/markdownParam` 的字符串拼接一起构成 3 处手写 JSON（:265-275） |
| Health 类型 | `internal/im/status.go:14-19,23-27,31-33` ≡ `internal/sources/source.go:180-198`（`HealthState` 四个常量 + `Health{State,Detail,At}` + `HealthReporter`） | 合并到一处（建议 `internal/sources`，im 反向引用或各自 alias）；`im.HealthOf`（status.go:36-41）无调用者，可删 |
| 成员分页循环 | `feishu/driver.go:141-162` ≡ `dingtalk/driver.go:105-125`（20 页上限、空 id 跳过、token 重复即停） | 泛型 `pageAll[T]` 放 `internal/im` |
| LRU-ish 发送/已发 id 缓存 | `feishu/driver.go:325-351`（own map + order 切片）≡ `dingtalk/driver.go:233-253`（sent map + order） | 抽 20 行 `im.ringCache`，或 `container/list` |
| 分页上限常量 | feishu `page_size:100`（transport:258）、`driver.PageSize=50`（driver:96-98）；dingtalk `maxResults:100`（transport:253）；github 100/50（poll:22-23） | 集中成各自 config，避免"同一工具三种页大小"（不算 bug，属可维护性） |
| CSV 切分 | `tools/seedstate/main.go:106-118` | `strings.Split` |
| int→string | `sources/github/trigger.go:194` `itoa = fmt.Sprint` | `strconv.Itoa`（stdlib 且更快） |
| 截断 | `internal/im/util.go:23`（rune 安全）vs `internal/agent/compact.go:66` `truncateRunes` vs `internal/agent/tools.go:271`/`internal/effects/effects.go:644` `truncate` | im 版本最完整；其余三处可复用（跨切片，非本审计范围，仅记录） |
| 状态码分类 | `internal/im/retry.go:38-66` `HTTPError.Temporary` vs `sources/github/channel.go:145-158` `isGone` + `command.go:48-62` `httpCode` | 两个来源的 gone/transient 语义应统一成一套命名（gh→go-github 后 `httpCode` 可直接删） |

## 6. 死代码 / 死依赖

### 6.1 生产未接线的代码

| 位置 | 状态 | 证据 |
| --- | --- | --- |
| `internal/im/feishu/`（1266 行） | **不进二进制** | 无非测试导入；`go list -deps ./cmd/zeokeel` 不含该包 |
| `internal/im/dingtalk/`（963 行） | **不进二进制**，且接收端 `StreamEvents` 恒 `ErrUnsupported`（transport:276-281）→ 即使接线也收不到消息 | 同上 |
| `cmd/zeokeel/extras.go:157-190` `startIMLoop`、`:192-210` `triggerForIM` | 无调用者 | `grep -rn "startIMLoop"`/`"triggerForIM"` 均只命中定义处 |
| `internal/im` 的复合类型 `im.Driver`（im.go:124-129）、`EffectiveCapabilities`（im.go:133-139） | 仅测试使用（`im_test.go:15-22`）；`cmd` 直接用 fake driver 调方法 | grep 无生产调用 |
| `internal/im/HealthOf`（status.go:36-41） | 无任何调用者 | grep 仅定义 |
| `internal/im/PollOnce`（poll.go:130-135） | 仅 `poll_test.go:157` 使用 | grep |
| `dingtalk/driver.go:307-334` `markerHash`、`:337-344` `stripMarker` | 仅测试使用（审计语义），生产只写不读 | grep |
| `feishu/driver.go` 的 own-message 缓存 | 生产逻辑需要，但仅在驱动接线后才有意义 | — |

### 6.2 依赖

- `go mod tidy -diff`（离线，`GOPROXY=off`）**无输出** → `go.mod` 当前是 tidy 的，**没有多余间接依赖**。
- `golang.org/x/time`（rate）与 `golang.org/x/oauth2` 都是间接依赖，且**已在构建图里**
  （经 MCP SDK）→ 用它们不增加依赖/体积（这是 F5/F4 推荐的前提）。
- 不在缓存、引入即需联网下载的候选：`larksuite/oapi-sdk-go/v3`、`open-dingtalk/dingtalk-stream-sdk-go`、
  `google/go-github/v93`、`hashicorp/go-retryablehttp`（后者不建议）。CI 的 `GOPROXY` 需能取到
  `goproxy.cn` 镜像（design §8.4 已写）。
- **外部运行时依赖**（不是 Go 依赖，但属于"运行时必需"）：
  - `gh`：GitHub Source 的唯一传输（`command.go:80-92`）；未安装时 `New()` 不会报错（`config.go:82-106` 不探测），
    第一次 `Health`（`members.go:45-62`）与 `Poll`（`poll.go:88`）全部失败，
    `cmd/zeokeel/github.go:76-88` 每 tick 把"轮询失败，检查 gh 是否登录"写进来源页。→ 功能全断，但 UI 能解释。
  - `lark-cli`：design `:182,:1603` 列为可选外部依赖，但代码里**没有**任何 lark-cli 调用
    （Go 端改成了手写 HTTP）→ 文档与实现不一致，属死文档项。
- `packaging/icons/*` 已提交且 `genicon` 可重生成（无 Makefile target，README `:79` 手动 go run）→ 不算死代码。

### 6.3 顺手记录（非本审计判定，但会误导读者）

- `README.md:126`："飞书/钉钉的 WebSocket 长连接未实现，当前用 HTTP 长轮询兜底" ——
  对钉钉不成立（HTTP transport 的 `StreamEvents` 直接 `ErrUnsupported`，见 F3），且两驱动都未接线（F1）。
- `CHANGELOG.md:52`："IM 驱动：飞书与钉钉（…幂等发送、退避重试）" —— 未接线 + 钉钉幂等在
  transport 重试下会被绕过（F3.1）。
- `design/zeokeel-go-mygo-design.md:1846` 的 ✅ 需要按 F1 改口径。
- `RELEASE.md:25-31`（C 段：build / 交叉编译 / `check-nocgo.sh` / 测试 / 体积 / TODO 全部未勾选）
  与 `CHANGELOG.md:8`（"机械闸门 14 项全过"）口径不一致；即便以 rc 状态解释，也应在发布前对齐，
  否则"发布清单"与"发布说明"互相证伪。

## 7. 建议执行顺序（供 Lead 合并到最终报告）

1. **P0（决策 + 修闸门）**：先定 1.0 是否含 IM（F1 A/B）；同时修 `ci.yml` 的 `-race`（F10）
   与 `window` job 超时；否则所有"CI 全绿"的表述都不成立。
2. **P1（若 GitHub 是 1.0 卖点）**：go-github 迁移（F4），因为当前实现有**静默丢事件**与
   **重复评论**两类正确性问题，且原型已有的 ETag/分页被丢了。这一步与"是否用 SDK"无关，
   即使保留 `gh` 也应至少补分页与 `find()` 翻页。
3. **P1（若 IM 进 1.0）**：Feishu 用 lark v3（F2，M）；DingTalk 只引 Stream SDK（F3，M），
   HTTP 发送保留并修"重试绕过去重"（F3.1）。
4. **P2**：抽 HTTP 公共层 + `x/time/rate`（F5）；合并 Health/重复函数（§5）；
   `math.Sqrt`/`strings.Split`/`strconv.Itoa`/`quote()` 等 stdlib 替换（XS）；
   修 `seedstate -allow`（F9）；删 `Makefile dist` 重复（F11）。
5. **不建议做**：为 retry 引 `hashicorp/go-retryablehttp`（F6）；为 ICO/ICNS 引第三方库（F8）；
   把钉钉整体迁 `alibabacloud-go/dingtalk`（F3）；为小仓库引入 GoReleaser（F11，除非要 CI 自动发版）。
