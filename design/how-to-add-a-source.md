# 如何新增一个 Source（Go 版 008 cookbook）

> 对应原型 `_ref/docs/008-cookbook.md` 的"加一个 Source"：把一个新的事件来源接进 ZeoKeel，
> 不改领域层、不改 UI 主流程。本文按 **2026-11-03 的真实代码**写；每一步都给出可对照的
> `file:line` 与现有实现（GitHub Source）作为样板。最后 §8 列出"今天还缺什么"——不够的地方
> 不假装能照做。

## 0. 先记住三条纪律

1. **入口一律用 Source 吸收，不给领域加概念**（design §5.9）。Source 只做三件事：
   判定接收范围（Scope）、把原始事件翻译成 `domain.Trigger`（Translate）、把回复写回原处（Channel）。
2. **Source 自己保存读位置**：游标只在"整轮 receive 全部成功"后推进；坏数据逐条跳过并计数。
3. **身份不跨 Source 映射**（001:39）：同一人在 GitHub/飞书是两个身份；允许名单按 Source 分开。

## 1. 实现 `sources.Kind`（必须）

接口在 `internal/sources/source.go:55-72`：

```go
type Kind interface {
    Name() Name
    Scope(t domain.Trigger) ScopeResult
    Translate(raw Raw) (domain.Trigger, error)
    Channel() Channel
}
```

| 方法 | 你要回答的问题 | 参考实现 |
| --- | --- | --- |
| `Name` | 稳定 id（`"github"`/`"feishu"`/`"linear"`…）。它出现在 `Trigger.Source`、Thread key 和 Policy 里，**定了就不能改**（改了就断历史） | `internal/sources/github/config.go:25` |
| `Scope` | 这条事件在不在我的地盘？配置了"接收全部"时 `Observed=true`；`Row` 填 admission 表的行名（`issue`/`pull`/`comment`/`ci`） | `internal/sources/github/scope.go` |
| `Translate` | 把 `Raw{Kind, Data}` 变成 `domain.Trigger`：`Source`/`Threads`/`Conversation`/`Channel`/`Actor`/`Addressed`/`Text`/`At`/`Scope`/`DefaultWorkspace` | `internal/sources/github/trigger.go` |
| `Channel` | 回写适配器；`Send` 必须幂等（用 `Outbound.Key`），目标永久不可达时返回 `Receipt{Gone:true}` | `internal/sources/github/channel.go` |

**最少四行**就能让一个 fake Source 进测试：不需要网络、不需要 CLI。

## 2. 可选能力（按需要实现，不要多写）

接口都在 `internal/sources/source.go`，用类型断言探测：

| 接口 | 何时实现 | 参考 |
| --- | --- | --- |
| `Poller` | 拉模式（GitHub 通知/评论、RSS、IM 历史）。契约见接口注释第 122-155 行：**游标只在全部 receive 成功后推进**；坏数据计入 `Unreadable` | `internal/sources/github/poll.go` |
| `MemberLister` | 允许名单能从工具侧同步（群成员/仓库协作者） | 见接口 `source.go:173-175` |
| `HealthReporter` | 能检查凭据/最近轮询并报 `starting/ready/paused/error` | 见接口 `source.go:196-198` |

## 3. 配置与注册

1. 定义配置结构体（每个实例一份 JSON，URL/凭据/仓库/chat id 都是实例的，不是 kind 的）：
   `internal/sources/github/config.go:31-74`。
2. 在构造函数里**先校验再相信**：仓库形状、必填字段；错误要在启动时暴露，不要带进每轮轮询
   （`repositoryPattern`，`config.go:79`）。
3. 在实现包里注册（文件顶层）：

```go
func init() { sources.Register(Name, newFromConfig) }
```

`Register` 的语义在 `internal/sources/registry.go:69-73`：空名/重名 panic（编程错误，越早越好）。
`New(name, config)`（`registry.go:85-91`）按名字构造实例。

4. 在 `cmd/zeokeel` 里 **blank import** 你的包以触发注册，然后按现有模式启动实例：
   `cmd/zeokeel/github.go:14`（`_ ".../sources/github"`）+ `:33-113`（读配置→`sources.New`→
   若有 `Poller` 起轮询 goroutine→receive→持久化游标）。

## 4. 接收：只从 Runtime 的入口进来

不要直接调 `engine.Receive`（那样会绕开持久化与两阶段 Eval）。用：

```go
rt.ReceiveTrigger(trigger)   // internal/runtime/entry.go:18
```

它内部走 `Persistence.Receive`（`internal/engine/persist.go:223`）：Trigger 落库 + 入 Eval 队列，
模型调用不在入口路径上（`EvaluateDue` 每 2s 批量跑，`internal/engine/evaluate.go:19`）。
重复投递返回 `engine.ErrDuplicate`，**按成功处理**（内容派生 ID 天然幂等）。

## 5. 游标：Source 自己的不透明 token

```go
key := "<kind>:<instance>"                 // 面板上看到的名字，如 "github:acme/widgets"
cursor := sources.Cursor(rt.Cursor(key))   // internal/runtime/kv.go:23
... Poll(ctx, PollRequest{Cursor: cursor}, receive)
if err == nil {
    _ = rt.SetCursor(key, string(res.Next)) // internal/runtime/kv.go:38
}
```

- 游标存 SQLite `kv`（`source.cursor.<key>`），重启后从上次成功位置继续。
- 空游标 = "从现在开始"，**绝不从头重放历史**（`PollRequest.Cursor` 注释）。
- 一轮里任何一条 receive 失败：返回 error、`Next` 保持轮次开始值；调用方不保存。
  重读一批是安全的（同一语义输入 → 同一 Trigger ID → 幂等）。

### 5.1 关闭事件（PR/Issue 生命周期）

如果 Source 能报告"某个 Thread 结束了"（GitHub 的 PR merged/closed、Issue closed），把事实放进
`Trigger.Channel.Address`（如 `"state": "merged"`），并在接线处**先 Receive 再 Close**：

```go
if _, err := rt.ReceiveTrigger(t); err != nil && !errors.Is(err, engine.ErrDuplicate) { ... }
if _, err := rt.CloseThread(t); err != nil { ... }   // cmd/zeokeel/github.go:76
```

`Runtime.CloseThread`（`internal/runtime/github_lifecycle.go:39`）识别 `merged`/`closed` 后调用
`Persistence.ThreadClosed`，engine 结束跟随该 Thread 的工单（outcome = merged/closed，`internal/engine/lifecycle.go:368`）。
Worker 侧用 `follow` 工具（`internal/runtime/artifacts.go:102`）把工单绑到自己的 PR；`own=true` 时
`report(ready)` 会等 PR 关闭后才结束。新 Source 不必自己实现 follows，只要如实提供关闭事实并调用 CloseThread。

> 工单准备（`execution.prepare`）与后台作业上限（`execution.background_timeout`）是**部署级设置**，不是
> 每个 Source 的属性；见 `design/ops-runbook.md` §6.7/§6.8。

## 6. Policy 部分 / admission 行 / 回写

1. **Policy part**：`engine.Policy`（`internal/engine/gate.go:27-56`）按 Source 名组织：
   - `Allowed[<source>][actor]`：谁可以通过这个 Source 发起工作；
   - `Bots[<source>][actor]`：哪些机器人可以产生 inferred 候选（永远不能 explicit）；
   - `Admissions["<source>|<row>"] = domain.AdmissionRow{ListedUnaddressed, UnlistedAddressed, UnlistedUnaddressed}`：
     每一格是 `explicit|inferred|ignore`（值定义 `internal/domain/ticket.go:145-165`）；
   - `Listened["<source>|<row>"]`：Source 是否还在监听该 scope；
   - `Rules []domain.Rule`：proactive 规则（每条 enabled 规则必须被判一次）；
   - `AcceptMode`、`ReplyMode`、`RequireApproval`。
   你的 Source 只需在 `ScopeResult.Row` 里给出 row 名，gate 自己查表，不需要认识你的配置。
2. **回写**：回复统一进 Outbox（幂等键 = 业务 ID），由 `Channel.Send` 发出。没有驱动的
   Source 现在返回错误让 Outbox 退避/暴露问题，**不再假报 sent**（`cmd/zeokeel/sender.go`）。
3. **频道格式**：`runtime/reply.go` 按 source 选格式——IM（飞书/钉钉）≤100 runes、中文结论先行；
   GitHub 结构化（背景/改动/验证/交付物）；local 简短。`ReplyPlan` 为 notify/both/none 时回复被 `Held`，
   只进面板不直发。新 Source 若属于 IM 类，用 `feishu`/`dingtalk` 同款源名即可复用该格式；否则在
   `composeReply` 的 switch 加一个分支（这是 runtime 的职责，不是 Source 的）。
3. **membership 同步**（若实现 `MemberLister`）：把 `[]Member` 合进 `Allowed[<source>]`；
   身份绝不跨 Source。

## 7. 测试（照 GitHub 的做法）

| 测试 | 断言什么 | 参考 |
| --- | --- | --- |
| `Translate` 表驱动 | 每条 fixture JSON → 期望 Trigger（ID 稳定、`Addressed`、`Self`、`Threads`） | `internal/sources/github/trigger_test.go` |
| `Poll` 游标纪律 | 整轮成功才推进；中途失败不推进；重读去重 | `internal/sources/github/poll_test.go` |
| 坏数据 | 一条坏 JSON 只让自己 `Unreadable+1`，同批其他照收 | `poll_test.go` |
| `Scope` | 别的仓库/别的会话=`Listened:false`；receive-all=`Observed:true` | `internal/sources/github/scope_test.go` |
| `Channel` | `Send` 幂等（同 key 只一条评论）、`Gone` 判定、marker 不进正文 | `internal/sources/github/channel_test.go` |
| 注册表 | 空名/重名 panic；`New` 未知 kind 报错 | `internal/sources/registry.go` + `internal/sources/source_test.go` |

测试约束：无网络、无真实凭据；把 `Command`（gh 包装）/HTTP 客户端做成接口注入
（`github.New(cfg, cmd)` 的模式），生产才用 exec/真实 HTTP。

## 8. 今天还缺什么（照做会卡住的地方，如实登记）

1. **没有 Source 实例的持久配置与面板表单**：实例仍由 `cmd/zeokeel` 的 flag（如 `-repo/-gh-login`）
   启动，没有 `source_instance` 表、没有"添加来源"UI；`sources.Names()` 已给面板列出 kind，
   但 UI 还没用上。owner: fix-runtime/fix-ui2（RELEASE F-4）。
2. **admission 表 / bot 名单 / 规则没有编辑 UI**：engine 支持（`Policy.Admissions/Bots/Rules`），
   设置页评估组仍是"等待运行时投影"。owner: fix-ui2。
3. **IM 类 Source 的配置面**：`cmd/zeokeel/imwiring.go` 是一条可参考的接线样板（driver → `startIMLoop` → `Runtime.ReceiveTrigger`，
   sender 注册到 outbox）。钉钉用官方 Stream SDK（`internal/im/dingtalk/stream.go`），飞书是 HTTP 长轮询
   （`internal/im/feishu/transport.go`）；新增 IM 类 Source 的凭据来自专用 flag/settings 键，不是通用 Source 实例配置，
   仍需改 `cmd/zeokeel`。
4. **没有动态插件/热加载**：Source 在编译期注册（ADR-6），新增必须重新编译——这是有意的，
   但意味着"装一个第三方 Source"不是 1.0 能力。
5. **`Poll` 的 `Threads` 提示**（订阅的 Thread）只有 GitHub 在用；一个新 Source 若不做增量过滤，
   需要自己实现"只读订阅线程 + 自己的通知"的有界轮询，否则会重复/超量。
6. **`MemberLister` 没有消费方**：runtime 还没做"从 Source 同步名单"的定时任务与面板按钮
   （`PolicyChanged` 已有，缺接线）。
7. **GitHub 的 receive-all/CI 配置字段**（`Config.ReceiveAll/ObserveCI`）在结构体里可用，
   但 CLI/UI 没暴露；新 Source 的等价开关同样没有配置面。

**验收一个新增 Source 的最小条件**：`go test ./internal/sources/<kind>/...` 全绿；
`go build ./...` 零 cgo；用 fake 事件跑通
`Translate → ReceiveTrigger → EvaluateDue → Ticket → Outbox 回执` 的端到端测试（可参考
`internal/engine/engine_test.go` 与 `cmd/zeokeel/e2e_test.go`）。
