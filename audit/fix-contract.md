# 修复契约（Remediation Contract）

> 状态：Wave 1 生效版。Lead 维护；任何跨包接口变更先改本文件并发消息给相关 agent。
> 目标：关闭 `audit/AUDIT.md` 与 7 份分片报告中的全部问题，并通过独立反证验证。

## 0. 不可违反的仓库规则

1. **零 cgo 是产品硬约束**：`go build` / `go test` 一律 `CGO_ENABLED=0`；`scripts/check-nocgo.sh` 必须 PASS。
   - 唯一例外：CI/本地做 `-race` 时该 step 单独 `CGO_ENABLED=1`（gcc 可用，已验证 `go test -race` 通过）；产品构建不接受 cgo。
2. **不擅自新增直接依赖**：网络/代理可用（`goproxy.cn`），但新增 `require` 前必须先发消息给 Lead 说明理由、纯 Go 证明、体积影响；Lead 同意后由 Lead 统一改 `go.mod/go.sum`（避免 4 个 agent 同时 tidy 冲突）。
3. **gofmt + go vet**：改完的文件必须 `gofmt -l` 为空；`go vet ./...` 无新增问题。
4. **每个行为变更都要有回归测试**；测试必须能在无显示器、无网络、`CGO_ENABLED=0` 下跑。
5. **不改 `audit/AUDIT.md`**（Lead 所有）；分片报告只读；自己的交接写在 `audit/fixes/<agent>.md`。
6. **不跨 scope 写文件**。需要别人 scope 的改动，发 `send_message` 给 lead（或对应 owner），写清 file:line 与期望改动；由 Lead 决定谁改。
7. **不提前报告完成**：只有你 scope 内 build+test 通过、交接文件写完，才向 Lead 发完成消息（≤40 行）。
8. **保留公共 API 兼容性**：`internal/agent`、`internal/engine`、`internal/runtime` 被多包引用。能加方法就不要改签名；必须改时，同时改自己 scope 内调用点，并在交接里列出跨 scope 待改点。
9. **禁止 `git commit`、禁止 `go mod tidy`**（Lead 统一做）；工作区保持可 diff。

## 1. 所有权（Wave 1）

| 角色 | 可写范围 | 禁止 |
| --- | --- | --- |
| fix-agent | `internal/agent/**`、`internal/mcp/**`、`internal/workflow/**` | runtime/engine/cmd/store/ui |
| fix-core | 另含 `internal/goal/**`（schema v3 折叠需要） | — |
| fix-core | `internal/store/**`、`internal/domain/**`、`internal/effects/**`、`internal/outbox/**`、`internal/schedule/**`、`internal/skills/**`、`internal/brief/**`、`internal/scan/**`、`internal/goal/**` | agent/runtime/engine/ui/cmd |
| fix-ui | `internal/ui/**`、`internal/view/**`、`internal/ui/assets/**`、`internal/humanfmt/**`（Lead 追认） | runtime/engine/cmd/store/design/ |
| fix-integrations | `internal/im/**`、`internal/sources/**`、`tools/**`（Lead 追认） | cmd/（接线由 Wave2 的 fix-runtime 做）、engine |
| fix-docs-ci | `README.md`、`CHANGELOG.md`、`RELEASE.md`、`RELEASE-STEPS.md`、`design/**`、`.github/**`、`scripts/**`、`packaging/**`、`Makefile`、`VERSION`、`.gitignore` | 任何 `internal/**`、`cmd/**`、`go.mod/go.sum` |
| fix-engine (Wave 2) | `internal/engine/**`、`internal/domain/**`（语义字段；Wave1 后接管）、`internal/assess/**` | runtime/ui/cmd |
| fix-runtime (Wave 2) | `internal/runtime/**`、`cmd/**` | engine 内部、ui、agent 内部 |
| fix-ui2 (Wave 2) | `internal/ui/**`、`internal/view/**` | runtime/engine/cmd |
| fix-docs-final (Wave 2) | 文档范围同 fix-docs-ci | 代码 |
| verifier-* (Wave 3) | `audit/verify/**`（只读代码；可写独立测试文件 `*_verify_test.go` 前先问 Lead） | 生产代码 |

## 2. 跨包契约（Wave 2 目标，供 runtime/ui 提前对齐）

### 2.1 Engine（owner: fix-engine）

保留现有公开方法与行为，**新增**：

```go
// 确定性授权（原型 002：gate 产出 strength，模型不再声明 strength）
type Strength string // explicit | inferred | not_admitted
type AdmissionCell string // explicit | inferred | ignore
type AdmissionRow struct {
    Source, Scope string // "<source>|<scope>"，如 "github|issue"
    ListedUnaddressed, UnlistedAddressed, UnlistedUnaddressed AdmissionCell
}
type Policy struct {
    Version string
    Allowed map[string]map[string]bool
    Rules   []Rule              // 新增：团队规则（ID/Instruction/Enabled/Proactive）
    Admissions map[string]AdmissionRow // 新增
    Bots    map[string]map[string]bool // 新增：bot list
    AcceptMode domain.AcceptMode
    ReplyMode  domain.ReplyMode
}

// 新增生命周期/投递方法（均需持久化对应修改）
func (e *Engine) AnswerAttention(ticketID domain.ID, answer string) error
func (e *Engine) StopTicket(ticketID domain.ID, reason string) error
func (e *Engine) RetryTicket(ticketID domain.ID) error
func (e *Engine) Pause() / Resume() / Paused() bool
func (e *Engine) DeliverInput(ticketID domain.ID, in domain.Input) error // Input 来自 fan-out/note/policy/resume
func (e *Engine) MarkInputsRead(ticketID domain.ID) error
func (e *Engine) BindFollow(ticketID domain.ID, thread string, own bool) error
func (e *Engine) ThreadClosed(thread, outcome string, at time.Time) error
func (e *Engine) RevalidateAndResume(ticketID domain.ID) error
func (e *Engine) PolicyChanged(from, to string) error
```

新增 domain 字段（兼容 JSON 缺省）：

```go
type Input struct { ID, Kind, Text, Thread string; At time.Time; Delivered, Read bool }
type DeliveryState struct { Failed int; LastError string; At time.Time }
type Follow struct { Thread string; Own bool; ClosedAt *time.Time; Outcome string }
type Ticket struct {
    // 已有字段保持不变；新增：
    Inputs    []Input
    Delivery  *DeliveryState
    Follows   []Follow
    FollowupOwners []string
    Ended     *Ended // 已有
}
```

`StageOf` 必须与 003 表逐行一致（含 `person/delivery`，blocked/failed 需 `waitsForPerson`），并配逐行 golden 测试。

### 2.2 Runtime（owner: fix-runtime）

```go
// 供 UI/CLI 调用的窄接口（ui 用 optional interface 探测，不 import runtime）
func (r *Runtime) AnswerAttention(ticketID string, answer string) error
func (r *Runtime) StopTicket(ticketID string, reason string) error
func (r *Runtime) RetryTicket(ticketID string) error
func (r *Runtime) PauseExecution(bool) error
func (r *Runtime) Health() (state string, detail string, exitCode int)
func (r *Runtime) PendingInputs(ticketID string) []string
```

- **入口统一**：GitHub/定时/IM/Dispatch 全部走 `Persistence.Receive`（或 `Engine.Receive` 但必须在同一事务入 launch effect）；游标写 `kv`；`sender` 无 driver 时返回可重试错误，不得报 sent。
- **Worker inbox**：`worker-<ticket>` 单会话；每次 run 前取 `Ticket.Inputs` 中未读项拼入任务（按序）；run 结束/Report 后 `MarkInputsRead`；有未读再起一轮；投递失败写 `Delivery`。
- **提示词热更新**：assessor 不再在进程启动时冻结；每轮从 settings 读最新 prompt/规则；版本历史写 store。
- **status/退出码**：`zeokeel status` 输出进程/健康/setup 问题；退出码 0 健康、1 可重启修复、2 组件错误、3 等待 setup（与 006:113-124 对齐）。
- **日志**：`slog` → `<stateDir>/logs/zeokeel.log`，按天轮转（保留 N 天）。
- **watchdog**：进程内 supervisor goroutine（每 60s 检查 health/队列/过期 Assignment，记录并可恢复重启挂起的 launch）；文档给出 systemd/launchd 外部 watchdog 配置。
- **备份/归档**：`zeokeel backup` + 每日 VACUUM/归档策略（ended>180 天到 `archive.db` 或按月表，最小实现：`zeokeel archive`）。

### 2.3 UI（owner: fix-ui / fix-ui2）

- `ui.App` 通过 optional interface（`AttentionAnswerer`/`TicketStopper`/`TicketRetrier`/`ExecutionPauser`）调用 runtime；不要 import runtime。
- Overview 的每条 `NeedsPerson` 行：按 stage 显示动作（批准/拒绝/回答/重试/停止）；动作只在总览（设计 §6.3）。
- 新增 Ticket 详情（点击行展开或 `⌘K` 跳转），显示 Mandate、Inputs、Assignment/Report、Follows、Artifacts。
- Settings 五组：Sources / Eval / Execution / Prompts / Deployment（至少只读+可保存现有值），来源页显示 admission table 与 bot list。
- 计数一律从 `len(rows)` 派生；`Runtime.Running/MaxJobs/Today/Queued` 必须真实投影（runtime 负责）。

### 2.4 依赖策略

- Wave1/Wave2 默认**零新增依赖**。允许的既有依赖：eino 全家桶、mygo、modernc/sqlite、MCP go-sdk、jsonschema、doublestar、humanize、x/time/rate。
- 如确需官方 SDK（Lark/DingTalk Stream/go-github）：先发消息给 Lead，说明替代范围、纯 Go 证据、预计删除行数；Lead 决定是否进 1.0。**没有 Lead 书面同意前不得改 go.mod。**
- 可用 `go get`/`go mod download` 做本地 spike（在 `/tmp` 独立 module），但不得写回仓库 go.mod。

## 3. 各 agent 交接格式（写到 `audit/fixes/<agent>.md`）

```markdown
# <agent> handoff
## 完成的条目（引用 audit 分片编号：N-x / F-x / 004-xx...）
## 改动文件（只列自己 scope）
## 新增/修改的测试与结果（命令 + 实际输出摘要）
## 行为变化与兼容性风险
## 跨 scope 待办（file:line + 期望改动）
## 未完成/被阻塞（如有，说明具体原因）
```

## 4. Lead 的合并与验收协议

1. 每波结束：`git diff --stat` 审查 + `gofmt -l` + `go build ./...` + `go test ./... -count=1 -skip Live` + `scripts/check-nocgo.sh` + `go vet ./...`。
2. 冲突：Lead 负责合并；跨包接口以本契约为准，改动成本高时 Lead 直接改。
3. 反证：Wave3 三个 verifier 独立写测试/脚本，尝试证伪每条 P0/P1；失败即回退给对应 owner 或 Lead 修。
4. 完成判据：`audit/AUDIT.md` 每条 finding 在 `audit/fixes/STATUS.md` 有 `fixed / documented-deviation / removed` 状态；全部行关闭；全量验证通过；文档与代码一致。
