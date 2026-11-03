# fix-runtime handoff — Wave 2（internal/runtime + cmd/zeokeel）

> 日期：2026-11-03　角色：fix-runtime　scope：`internal/runtime/**`、`cmd/**`。
> 未触碰 engine/domain/assess/store/agent/im/ui/go.mod/go.sum；未 commit、未 `go mod tidy`、未新增依赖。
> 依据：`audit/fixes/wave2-spec.md` §3/§5/§6、`audit/fix-contract.md` §2.2、`audit/fixes/fix-engine-api.md`、`audit/fixes/fix-ui2.md`、`audit/fixes/fix-agent.md`、`design/ops-runbook.md`。

## 1. 完成的条目

### Phase A（先做、不依赖 engine 重写的部分）

| 工作项 | 状态 | 关键改动 |
| --- | --- | --- |
| A1 单实例测试冲突 | fixed | `internal/runtime/concurrency_rev_test.go` 改为两个 Runtime 视图共享**一个** `store.Store`；stale-revision CAS 断言不变（独占 flock 保留）。 |
| A2 入口统一 + 游标持久化 + sender 诚实 | fixed | 新增 `internal/runtime/entry.go`（`ReceiveTrigger`，durable 走 `Persistence.Receive`）与 `internal/runtime/kv.go`（`Cursor/SetCursor` 走 `store.Get/Set`，键 `source.cursor.<instance>`）。`cmd/zeokeel/github.go` 启动读游标、每轮推进后落盘；`extras.go` 的 schedule/IM 循环改走 `rt.ReceiveTrigger`；`sender.go` 无 driver → 返回错误（Outbox 退避），不再 `OutcomeSent`。`Dispatch`/`Approve` 同样走 durable 包装。 |
| A3 onboarding 持久化 | fixed | `onboarding.go` 新增 `persistSettings`：`SaveOnboarding` 先 `store.SaveSettings` 再登记工作区；`SetNeedsOnboarding`/`FinishOnboarding` 落盘 `onboard.done` 与 `setup.complete`；重启后 `LoadSettings` 能读回（有测试）。 |
| A4 诚实投影 | fixed | `runtime.go`：`Running` 用 atomic 计数（真实在跑的执行器）、`MaxJobs=concurrency()`、`Queued`=durable launch 队列 waiting / 内存模式 queued ticket、`Today`=今天启动的 Assignment 数、`NextPoll`=设置、`Activity`=过去 24h 每小时 Trigger 数、`Health` 来自模型/来源/持久化健康行、`Paused/PausedKnown`；`pages.go` 的 `EvalCounts{Evaluated,Decisions,Filtered,Tickets}` 四项独立派生（不再恒等）、`PolicyVersion` 从 workspace policy JSON 读、`Storage.LastBackup` 扫描 backups 目录。`ticketsVM` 按最近活动排序并补 `StageID` + `DetailVMs`（Mandate/Attention/Assignments/Reports/Inputs/Follows/Artifacts/Delivery）；`NeedsPerson` 行补 `StageID/Attention/CanRetry`；`stream` 的 `view.Thread.ID` 已设置。 |
| A5 status/exit codes/headless | fixed | 新增 `cmd/zeokeel/cli.go`（`status/check/setup/login/backup/archive` 在 flag 解析前分发；`--json`）、`internal/runtime/status.go`（`InspectStatus`：`store.Inspect` 只读 + 单实例锁探测 + schema/health/setup/load；`ExitCode` 0/1/2/3）、`cmd/zeokeel/service.go`（`-headless` 真服务：2s pass + supervisor + 信号 drain + 退出码）。`check` 做目录/数据库/日志/模型/setup 自检；`setup` 支持非交互配置（模型/工作区/允许名单，写 store 的 `onboard.*` + workspace + `setup.complete`）；`login` 检查 `gh auth status`。 |
| A6 日志/watchdog/备份/归档 | fixed | 新增 `cmd/zeokeel/logging.go`（slog JSON → `<stateDir>/logs/zeokeel.log`，按天轮转，保留 14 天，写失败回退 stderr）；`runtime/supervisor.go`（60s 巡检：DB 可写、launch 队列失败/Kick、>6h 的 running Assignment 记 health；连续 3 次问题写 `runtime` health error）；`cmd/zeokeel/ops.go`（`backup` = `VACUUM INTO` 落 `<stateDir>/backups/zeokeel-<date>.db` 并 0600；`archive` = ATTACH archive.db + `CREATE TABLE AS SELECT WHERE 0` + INSERT/DELETE 事务，把结束 >180d 的 ticket/trigger/disposition 移走，**outbox 保留**；`--dry-run`）。两个命令都要求 writer lock，运行中执行返回退出码 1 并明确提示先退出应用。 |
| A7 提示词热更新 | fixed | `cmd/zeokeel/assess.go` 的 `newAssessor` 不再冻结 prompt：`hotAssessor.Assess` 每次 Eval 从 store 读 `prompt.router`/`prompt.eval_rules` 重建（便宜的）`assess.Config`；Worker prompt 本来每轮从 settings 展开。新增 `TestAssessorReadsTheSavedPromptPerEval`。 |
| A8 -race 修复 | fixed | 外部 Ticket 读全部改**深拷贝**快照：`ticketSnapshot` 直接 `Engine.TicketSnapshot`，`ticketSnapshots` 对 `AllTickets()` 的每个 ID 再深拷贝（`AllTickets` 是浅拷贝，Assignments 里的 Report 指针与引擎共享，正是 -race 报的写读冲突）；投影不再使用 `FeedItem.Ticket` 指针（用 `ticketFor` 解析快照）；`disk_test.go`/`faults_test.go`/`live_test.go` 的 `eng.Ticket` 指针读改成 `TicketSnapshot`；`longtask/compaction/usage/phaseb/worker` 测试读 Ticket 改 `allSnapshots`。`CGO_ENABLED=1 go test -race ./internal/runtime/ ./cmd/zeokeel/` 均通过。 |

### Phase B（engine API 就绪后）

| 工作项 | 状态 | 关键改动 |
| --- | --- | --- |
| 两阶段 Eval 接线 | fixed | `runtime.Dispatch`/`ReceiveTrigger` 在入口命令后立即 `EvaluateDue`（面板输入不等 2s）；GUI/headless 的 2s pass 调 `rt.EvaluateDue(now)`；`EvaluateDue` durable 走 `Persistence.EvaluateDue`（含 launch effect 入队），内存模式对新建 Ticket 直接 `startLocalRun`。 |
| Worker inbox + 投递三段式 | fixed | `worker.go` 的 `runTicket` 改为多轮：每轮取 `UndeliveredInputs()`（无则 `UnreadInputs()`，再退原始 task）拼任务 → `MarkInputsDelivered` → `MarkInputsRead` → `RunSession` → `MarkTurnEnded`；交付失败 `DeliveryFailed` 并返回 error 让 effect 以 2s→1min 退避重试；回合后若还有未读/未投递 input 再跑一轮（最多 3 轮，间隔 100ms）；`InputTask` 把人类消息与 context 区分标注。 |
| Assignment 崩溃恢复 | fixed（best-effort） | `runtime/recovery.go` 的 `RecoverAssignments`：启动时按创建时间扫描 `running` Assignment → 补 recovery note Input → 开新 Assignment → wakeTicket；main 在 `EnableWorker` 后调用。**注意**：engine 没有“把旧 running Assignment 标记 interrupted”的 API（见 §5），旧 Assignment 仍留在 `Ticket.Assignments` 里（投影按 `LastAssignment` 工作，不影响状态判定，但历史里有两条 running）。 |
| 优先级 launch | fixed | `EnableDurable` 注册 `engine.EffectLaunch` 与 `EffectLaunchHuman` 两个 kind：normal 用 `r.concurrency`、human 用 `concurrencyHuman()=1`，Retry 2s/1min；`wakeTicket` 按 origin Trigger 的 `Human||Self` 选 kind，并用 `Pending` 去重。 |
| UI action 包装 | fixed | 新增 `runtime/wrappers.go`：`AnswerAttention/StopTicket/RetryTicket/PauseExecution/Paused/PendingInputs/TicketDetail`，全部走 Persistence 包装后 `wakeTicket`；`cmd/zeokeel/wiring_assert.go` 加 4 条编译期断言（`ui.AttentionAnswerer` 等），防止签名漂移让面板静默降级。设备组/评估组投影仍按 UI 契约提供事实。 |
| 后台作业唤醒 | fixed | `worker.go`：每 Ticket 复用一个 `*agent.Runner`（`Runtime.runners`）；回合后 `consumeFinishedJobs` 把已完成 job 变 Input 并继续一轮；运行中的 job 起 watcher（`Jobs().Wait`）完成后投递 Input 唤醒；Ticket 结束/回合收尾 `cancelJobs`。 |
| 通道适配 | partial | `deliverReply` 现在会记录 outbox 入队失败；reply composer 仍是既有模板（背景/交付物/结论），**未**做"IM ≤100 词 / GitHub 结构化 / notify 档拦截"的按 source 模板与投递决策（见 §5，属剩余项）。 |
| 提示词版本化/校验 | partial | 热更新已真；**未**做团队三段（communication/scope/delivery）、base-hash、"Default updated"、版本历史表、占位符/长度/未知键校验——`internal/prompts/**` 不在本 scope（见 §5）。 |

## 2. 改动文件（只列本 scope）

新增：
- `internal/runtime/`：`entry.go`、`kv.go`、`status.go`、`supervisor.go`、`delivery.go`、`wrappers.go`、`recovery.go`、`evaltest_test.go`、`phaseb_test.go`
- `cmd/zeokeel/`：`cli.go`、`logging.go`、`service.go`、`ops.go`、`wiring_assert.go`、`ops_test.go`、`assess_test.go`

修改：
- `internal/runtime/`：`runtime.go`（投影/日志/plan）、`worker.go`（inbox+三段式+runner 复用+job 唤醒）、`pages.go`（诚实计数/详情/排序）、`onboarding.go`（持久化）、`storage.go`（LastBackup）、`artifacts.go`（Persistence.RaiseAttention）、`durable.go`（双 launch kind、Queue 访问器）、`reply.go`（投递失败日志、删占位符）、`concurrency_rev_test.go`、`disk_test.go`、`faults_test.go`、`stages_test.go`、`longtask_test.go`、`extras_test.go`、`settings_test.go`、`onboarding_test.go`、`reply_test.go`、`live_test.go`
- `cmd/zeokeel/`：`main.go`（realMain/CLI/日志/headless/2s pass/recovery/setup marker）、`assess.go`（hotAssessor）、`github.go`（ReceiveTrigger+游标）、`extras.go`（schedule/IM 入口+slog+Threads）、`sender.go`（无 driver 返回错误）、`acceptance_test.go`、`durable_test.go`、`recovery_test.go`、`e2e_test.go`

未改：`internal/{engine,domain,assess,store,agent,im,ui,view}/**`、`go.mod/go.sum`、`audit/AUDIT.md`、其他 agent 的 handoff。

## 3. 测试与结果

```
$ CGO_ENABLED=0 go build ./...            → exit 0
$ CGO_ENABLED=0 go vet ./internal/runtime/ ./cmd/zeokeel/   → 干净
$ gofmt -l internal/runtime cmd/zeokeel   → 空
$ CGO_ENABLED=0 go test ./internal/runtime/ -count=1        → ok（~10s）
$ CGO_ENABLED=0 go test ./cmd/zeokeel/ -count=1             → ok（~1.3s）
$ CGO_ENABLED=0 go test ./... -skip Live -count=1           → ok（全仓；见 /tmp/all-tests2.log）
$ CGO_ENABLED=1 go test -race ./internal/runtime/ -count=1  → ok（22.9s）
$ CGO_ENABLED=1 go test -race ./cmd/zeokeel/ -count=1        → ok（5.0s）
```

新增回归测试（全部无显示器、无网络）：
- `TestSettingsConflictAcrossWindows`（单 store 双视图 + stale revision CAS）
- `TestEvaluateDueCreatesTheTicket`（Receive 只排队，pass 建单）
- `TestInputDeliveryThreeStages`（delivered/read/turnEnded 三段式 + mock 模型）
- `TestCursorAndOnboardingPersistAcrossRestart`（store kv 游标 + onboarding + setup marker 重启可读）
- `TestPauseAndHealthProjections`（Paused/PausedKnown；无模型 health=setup/3）
- `TestSupervisorWritesAHealthRow`（确定性：直接调用 `writeHealth`，不再 sleep 等 60s 循环）
- `TestStatusExitCodes`（0/1/2/3：空目录、运行中缺 setup=3、健康=0、health error=2）
- `TestLogRotationAndPrune`（跨天重命名 + 14 天清理）
- `TestBackupCopiesTheDatabase`（运行中拒绝=1；VACUUM INTO 副本可读、0600）
- `TestArchiveMovesOldTickets`（旧 ticket/trigger 移入 archive.db；outbox 回执保留）
- `TestParseAge`、`TestSenderWithoutDriverReturnsRetryableError`
- `TestAssessorReadsTheSavedPromptPerEval`（保存 prompt 后下一次 Eval 使用新值）

## 4. 行为变化与兼容性风险

1. **Receive/Dispatch 不再同步建单**：调用方必须跑 `EvaluateDue`（runtime 的入口命令已自动跑一次，2s pass 兜底）。直接调用 `engine.*` 的测试/工具需要显式 pass（本 scope 测试已改）。
2. **`Report(ready)` 会结束 Ticket**（003:94）：runtime 不再自己判结束；原来期望 `person/review` 的测试已按新语义改为 `ended`。
3. **显式请求不走审批**（002:101）：`RequireApproval` 只对 inferred 生效；acceptance 里"同事请求→proposal"的场景改为 inferred + policy rules + accept mode auto 才成立。
4. **`-headless` 语义变化**：原为 `-demo` 别名，现在是真服务模式（无窗口、2s pass、supervisor、信号 drain、退出码 0/1/2/3）。`-demo` 仍是无副作用的 walkthrough。
5. **status 探测会短暂 `store.Open`**：未运行时 status 可能触发迁移（当前 schema 下无数据变更）并立即 Close；运行中由 flock 判定，绝不迁移。`store.Inspect` 未扩展 Load 计数前，status 用一条只读连接查 `ticket.stage/workspace`（`internal/runtime/status.go` 有 TODO，待 store 扩展后切换）。
6. **launch 重试改为 2s→1min**（003:11）；原 5s/5min。投递失败会重试，模型失败仍报 Ticket failed（不重试模型）。
7. **后台作业**：每 Ticket 持有一个 Runner（不再每轮新建）；作业结果以 Input 形式唤醒下一轮；进程崩溃丢本地作业（v1 已接受）。Runner 没有 Close，Ticket 结束时只 Cancel 运行中作业并丢 map 引用。
8. **sender 无 driver 返回错误**：面板不再显示"已发"；Outbox 会持续退避并将 kind 标 error（这是诚实状态，但一个未接线的 Source 会让该 kind 显示失败）。
9. **归档在运行中拒绝**（退出码 1）：必须先退出应用；`archive.db` 的 schema 由 `CREATE TABLE AS SELECT ... WHERE 0` 从主库复制，避免手写 DDL 漂移。
10. **`setup` 非交互配置**会写 `onboard.*` 设置 + workspace 行 + `setup.complete`；已运行中的应用仍以设置页修改为准（运行中执行 setup 返回 1）。

## 4bis. 第二轮补齐（Lead 第二批清单）

| 条目 | 状态 | 说明 |
| --- | --- | --- |
| 1 IM 接线（N-1） | fixed | `cmd/zeokeel/imwiring.go`：`-feishu-app-id/-feishu-app-secret/-feishu-chats/-dingtalk-app-key/-dingtalk-app-secret/-dingtalk-robot/-im-self` + settings 回退；`wireIM` 把 driver 的 `im.Sender` 注册进 `sourceSender`，`startIMLoop` 在 goroutine 里跑并轮询 `HealthReporter` → `SetSourceStatus` + health 行 `im:<kind>`；`ErrUnsupported` 变成禁用来源行 + health error，不静默。**未配置不注册来源行**（不假装监听），半配置=错误行。测试 5 条（unconfigured/partial/unsupported/event→Trigger/sender）。 |
| 2 通道适配（G6） | fixed | `internal/runtime/reply.go`：IM（中文 ≤100 rune，结论+产出，rune 边界截断）、GitHub（背景/改动/验证/交付物）、local（短）；`ReplyPlan` 参与投递决策：`mayReplyDirectly`（self/local 或 `Route==direct`）否则 hold（面板记录 "拟回复（未直发同事）"），不再静默直发同事。测试 4 条。 |
| 3 Prompt 团队三段+版本/校验（G4） | fixed（runtime 侧；view/ui 无需改） | `runtime/prompts.go`：新增 communication/scope/delivery 三层（默认文案在 runtime，UI 按 `vm.Layers` 渲染）；`preparePromptSave` 拒绝未知层/未知 `{{var}}`/超长（>20000 字符，不截断）/worker 缺必需占位符；完全相同不写、等于默认=清除覆盖；`recordPromptMeta` 已在 `SaveSettings` prompt 路径调用，kv 记录 `prompt.history.<layer>`（最近 20 版）与 `prompt.base.<layer>`（Default updated 检测）；团队三段追加进 Worker 指令。测试 3 条。 |
| 4 store.Inspect 新字段 | fixed | `internal/runtime/status.go` 用 `HealthView.TicketsByStage/Workspaces`；删除只读兜底查询与 TODO；`Load.Running/Queued` 来自 stage 计数。 |
| 5 InterruptAssignment | fixed | `recovery.go` 改调 `Persistence.InterruptAssignment`（或 engine），由引擎写 interrupted 事实、新 pending Assignment 与 recovery note；不再手工补 note/新 Assignment。 |
| 6a manually_stopped + stop | fixed | `zeokeel stop`：写 `<stateDir>/STOPPED` 文件标记（运行中也能写）+ 可选 `--pid` SIGTERM；`InspectStatus` 读标记置 `manually_stopped`，`ExitCode` 对"手动停止且未运行"返回 0（监督脚本不重启）；应用启动时清除标记。 |
| 6b check 退出码 | fixed | 空安装/缺 DB/schema → 3（setup waiting）；仅真实组件错误 → 2。 |
| 7 设置 Eval 组 + GitHub 来源可配 | fixed（只读事实 + 可用字段） | Eval 组发布 `PolicyVersion/accept/reply/RequireApproval/启用规则/admission 数`（只读事实，读取 workspace policy JSON）；Sources 组新增飞书/钉钉/IM self 可编辑字段与 GitHub `repo/login`（启动时作为 flag 回退），observe/CI 过滤器标为事实（GitHub Source 尚未接线，避免假旋钮）。 |
| 8 ReplyPlan 消费 | fixed | 见条目 2；notify 计划 → hold，不直发。测试 `TestReplyPlanGatesDirectReplies`。 |
| 环境变量清洗 | **跨 scope（agent）** | bash 子进程 env 清洗属于 `internal/agent`（fix-agent scope），本 agent 未改：需要在 `agent/tools.go` 的 exec.Command env 构造处删除匹配 `KEY|TOKEN|SECRET|PASSWORD` 的变量（白名单除外）并加测试。已在本文件与给 Lead 的消息中标注。 |
| keychain | documented-deviation | 无 OS keychain 接入；Settings 存 SQLite 0600，handoff/README 需继续如实说明。 |
| Runner.Resume 生产未用 | **跨 scope（agent）** | 属 `internal/agent`（fix-agent）：删除或用 RunSession 恢复路径替代；steer/inject 作为 documented-deviation。runtime 侧已用 `adk/middlewares/patchtoolcalls`+RunSession 恢复，不依赖 Resume。 |

**构建依赖（已由 Lead 解决）**：`github.com/open-dingtalk/dingtalk-stream-sdk-go`（fix-integrations 引入）曾因 go.mod 标 `// indirect` + go.sum 缺 `gorilla/websocket` 导致 cmd build 失败；Lead 已 `go mod tidy`，现 `CGO_ENABLED=0 go build ./...` 与全仓测试均通过。本 scope 未改 go.mod/go.sum。

**stop marker 修复（最后一处红）**：`TestStopMarkerQuietsStatus` 暴露 `InspectStatus` 在数据库缺失时提前返回、没读 STOPPED 文件标记；已改为进入 DB 检查前先读 `StopMarkerPath`（手动停止也可能没有数据库/进程），测试通过。

**最终验证（本 scope）**：
- `CGO_ENABLED=0 go test ./cmd/zeokeel/ ./internal/runtime/ -count=1` → ok（cmd 1.2s / runtime 10.3s）
- `CGO_ENABLED=1 go test -race ./cmd/zeokeel/ ./internal/runtime/ -count=1` → ok（5.1s / 23.9s，无 DATA RACE）
- `CGO_ENABLED=0 go test ./... -skip Live -count=1` → 全仓通过（27 个 ok 包，0 FAIL）
- `CGO_ENABLED=0 go build ./...` → exit 0；`gofmt -l internal/runtime cmd/zeokeel` → 空；`go vet` 干净。

## 4ter. 第三轮 003 合规（冻结前最后三项）

| 条目 | 状态 | 说明 |
| --- | --- | --- |
| PR 生命周期（003:96-106 / 001:37） | fixed | `cmd/zeokeel/github.go` 每个轮询 Trigger 先 `rt.CloseThread(t)`：`ClosingThreadOutcome` 识别 `state` 含 merged/closed（kind=pull/issue 均可，open 不算），调 `Persistence.ThreadClosed(key, outcome, trigger.At)`（无 store 走 engine）。`runtime/github_lifecycle.go` 的 `BindThread` + Worker 新工具 `follow`（`mode=own|context`）调 `Persistence.BindFollow(ticketID, thread, own=true)`；线程 key 兼容 `owner/repo#12`（补 scope|github|id）与不同 scope 的 Follow（关闭时按后缀匹配所有 Ticket Follow 的 key）。ready 后跟随的 PR 未关 → 保持 person/review；关闭 → Ended(outcome=merged/closed)；重复关闭幂等。测试：`TestFollowThenCloseEndsTicketByOutcome`。 |
| prepare 命令 + 执行变量（003:49-63） | fixed | `runtime/prepare.go`：首轮前若工单目录不存在且 `execution.prepare` 非空 → `sh -c` 在 workspace root 执行（10 分钟超时），env 注入 `ZEOKEEL_WORKDIR/ZEOKEEL_TICKET_DIR/ZEOKEEL_TICKET/ZEOKEEL_TICKET_NAME/ZEOKEEL_REPO` 且**清洗** KEY/TOKEN/SECRET/PASSWORD；失败/超时 → `ReportFailed` 带输出、不建目录、不自动重试；成功但未建目录也算失败。同一组变量作为"工单事实"写入 Worker 提示词。Settings 的 Execution 组新增 `prepare` 与 `background_timeout`（可编辑，有 Help）。测试：`TestPrepareRunsWithVariablesAndFailsCleanly`、`TestPrepareFailureFailsTicketOnce`（含"只执行一次"）。Worker bash 子进程 **已注入** 同一组 vars（`agent.Config.Env`，仅路径/编号，无凭据）：`worker.go` 的 `runnerFor` 设置 `Env: r.executionVars(t, workdir)`；测试 `TestWorkerBashSeesTicketEnv`。 |
| 后台 30 分钟规则（003:33-39） | fixed | `runtime/background.go`：`backgroundLimit()` 读 `execution.background_timeout`（秒，30s..24h，默认 30m）+ `SetBackgroundLimit` 供测试；`watchJob` 改为 Wait vs 计时器：正常完成→Input 唤醒下一轮；到点仍运行→标记 killed、`Jobs().Cancel`、`ReportWaiting` 带 job id + 最后输出（≥1000 字截断）+ 错误，且**不再唤醒 session**（`bgKilled` 防 late-Wait 投递）。回合结束时若有运行中作业 → 先报 waiting（不结束工单）；`consumeFinishedJobs` 在 ready 报告之前运行。Assignment 结束/工单结束仍 `cancelJobs`。子代理不是 shell job，不受此规则。测试：`TestBackgroundJobThirtyMinuteRule`（短限时；kill 后模型调用数不再增长）。 |

**最终验证（第三轮）**：
- `CGO_ENABLED=0 go test ./... -skip Live -count=1` → 27 个包 ok，0 FAIL
- `CGO_ENABLED=0 go test ./internal/runtime/ ./cmd/zeokeel/ -count=1` → ok（11.2s / 1.3s）
- `CGO_ENABLED=1 go test -race ./internal/runtime/ ./cmd/zeokeel/ -count=1` → ok，无 DATA RACE（24.5s / 5.2s）
- `gofmt -l internal/runtime cmd/zeokeel` 空；未改 go.mod/go.sum。
- 冻结后追加：worker bash env 已注入（`agent.Config.Env`，测试 `TestWorkerBashSeesTicketEnv`）；`execution.prepare` Help 改为 execution root（行为未变）。
- 异步清理 flake：`TestEditedWorkerPromptReachesTheModel` 等 12 个含 `EnableWorker` 的测试现在注册 `settleRuntime(t, rt)`（t.Cleanup 中 `rt.WaitIdle(20s)`），保证 worker 停止后才做 TempDir 清理；`-count=10` 该测试通过，runtime `-race` 全绿。
- 发布门 -race（全仓）：`CGO_ENABLED=1 go test -race ./... -skip Live -count=1` → exit 0，27 ok，无 DATA RACE；`CGO_ENABLED=0 go test ./... -skip Live -count=1` ×2 → 均 exit 0。修掉的最后竞态：`cmd/zeokeel/recovery_test.go`/`e2e_test.go` 用 `eng.Ticket` 指针读（改为 `TicketSnapshot`），并顺手把两处生产调用（`runtime/deliverables.go` 投影、`main.go -demo` 的 attention 演示）改为快照/`RaiseAttention`，无行为变化。`Engine.Ticket` 仍是指针 API，对引擎外部调用者仍是竞态隐患（引擎冻结，建议后续改为返回值或加 `TicketSnapshot` 强制注释）。

## 4quater. Runtime round 3（ui2-spec §2/§4）

| 条目 | 状态 | 说明 |
| --- | --- | --- |
| 工作区/会话投影 | fixed | `runtime/pages.go` 新增 `workspacesVM(now, all)` + `conversationVM`：每个 `eng.Workspaces()` 一个 `view.WorkspaceEntryVM`（ID/Name/Root/Purpose/Kind），只含该工作区 Ticket（ended 也含，按 LastAt desc，最多 50）；`CreatedBy` = origin Trigger.Source=="local" ? "me" : "agent"，`Source/Stage/StageID/Attention/LastAt` 齐；`Publish()` 每次调用 `snap.PublishWorkspaces(...)`（启动/变更/2s pass 都经由它）。 |
| `CreateWorkspace` | fixed | `runtime/workspaces.go`：校验非空 name/root、root 必须是已存在目录、kind=repo/data/docs/mixed；`scan.Scan(root)` 填 Facts + Purpose/Keywords；ID=`ws_`+slug；重名/重 ID 拒绝；持久化走 `Persistence.Register`（默认 policy Allowed local:operator），内存走 `Engine.Register`；Publish。 |
| `NewConversation` | fixed | 校验 workspace 存在/文本非空；durable→`Persistence.Dispatch`、内存→`Engine.Dispatch`，`DispatchInput.Workspace` 指定工作区；立即 `EvaluateDue` 并合并两阶段结果；`wakeTicket`（local 触发 Self→human 优先）；Publish；返回可操作 error。测试含 durable 重启存活。 |
| System 事实 | fixed | `Publish()` 填 `view.OverviewVM.System`：OS/Arch 用 `runtime.GOOS/GOARCH`，Timezone=`time.Local.String()`，UTCOffset=`±HH:MM`；`deployFacts` 增加「操作系统」「时区」。 |
| 未受理修复 | fixed | `stream()`：local 且无 Ticket/Proposal 时 Decision 文案改为「已答复」（非 local 为「未派发」），追加 `LineReply`「超级Agent：…」；问工作区（工作区/workspace/哪些/有什么/几个）列出全部名称+用途+类型，普通闲聊说明未派发原因并给两种下一步；`Dispatch` 在无单无 proposal 时返回可操作 error（不再静默 nil）。有真实请求仍建单。 |
| 工具开关 | fixed | `runnerFor` 的 `agent.Config` 设 `PlanTasks/AgentsMD/WebTools=true`；`systemPromptFor` 增加计划工具（task_create/get/update/list）、网络工具（web_fetch/web_search）、AGENTS.md 注入说明；测试断言 `Runner.Config()` 与 prompt。 |
| 测试 | — | 新增 `runtime/round3_test.go`（建工作区/面板/会话/系统/本地答复）、`cmd/zeokeel/round3_test.go`（durable 重启存活）、`TestWorkerToolSwitches`；`cmd/wiring_assert.go` 增加 `ui.WorkspaceCreator`/`ui.ConversationCreator` 编译期断言。回归：`TestPrepareFailureFailsTicketOnce`/`TestBackgroundJobThirtyMinuteRule` 因 Dispatch 合并两阶段结果后双重启动，已在 Dispatch/NewConversation 去掉多余 `startLocalRun`（EvaluateDue 已启动），并加 `settleRuntime` root 静默等待消除 TempDir cleanup flake。 |
| 验证 | — | `go build ./...` ok；`go test ./internal/runtime/ ./cmd/zeokeel/ -count=1` ok（18.9s/1.4s）；`go test ./... -skip Live` → 27 ok / 0 FAIL；`-race` runtime/cmd ok（33.0s/5.7s）；gofmt/vet 干净；未改 go.mod、未动 engine/agent/store/ui/view。 |

## 4quinquies. IM-1/2/4/5（im-channel-spec）

| 条目 | 状态 | 说明 |
| --- | --- | --- |
| Settings 模型 | fixed | Sources 组发布 `feishu_enabled`(switch)/`feishu_transport`(select: longpoll, ws, webhook 后两者标注未实现)/`dingtalk_enabled`(switch)/`dingtalk_transport`(select: dws/stream，默认 dws)/`dingtalk_dws_cmd`(默认 dws)/`dingtalk_bot_id`，保留既有凭据键，带 Help；kv 设置无需迁移。 |
| IMManager | fixed | `runtime/im.go`：`IMConfig` 从 settings+flags 读；每渠道 lifecycle（ctx cancel + ≤5s 等待）；禁用→来源 `disabled` 行 + health ok「未启用」、不构建 driver；半配置→error 行/health；启用→driver（飞书 longpoll；钉钉 DWS 用 nih-integrations `dingtalk.DWSClient`，stream 用官方 driver）→注册 `im.Sender` 到 outbox→轮询/receive 循环→health `im:<channel>`/来源状态；cursor 写 kv `im.cursor.<channel>` 且整批成功后才推进，失败 1s→30s 退避。`ApplyIMSettings(old,new)` 只重启 enable/transport/凭据变化渠道；`SaveSettings("source")` 提交后自动调用。聚合授权检查 `Runtime.CheckChannel` → `IMManager.CheckChannel`（UI `ChannelAuthorizer` 已断言）：飞书 POST token（10s，401/403→auth + open.feishu.cn/app）、DWS `auth status`（未登录返回 authUrl+登录命令）、Stream 检查凭据。 |
| 工具 | fixed | Runner 构造已设 `PlanTasks/AgentsMD/WebTools=true`；新增 `execution.web_search_url` 设置→`agent.Config.WebSearchURL`（空则 DuckDuckGo）；prompt 已含 task/web 工具名。 |
| 测试 | — | `runtime/im_test.go`：默认值、禁用不监听、半配置错误（含 health）、轮询→Trigger+cursor 持久化、失败不推进 cursor、Apply 重启/不变不重启、飞书 CheckChannel(httptest 覆盖 httpDo：ready/401/网络错误)、DWS CheckChannel(auth/ready/未知渠道)；`TestWorkerToolSwitches`/`TestWebSearchURLComesFromSettings`；`TestIMEventBecomesATrigger`。cmd 侧改写 verifier IM 测试走 IMManager（Receiver-only driver 走 subscribeLoop），sender 路由测试保留。 |
| 验证 | — | runtime+cmd build ok；scoped tests ok（19.1s/1.3s）；runtime/cmd `-race` ok（34.9s/5.6s）；gofmt/vet 干净；无 go.mod 改动。**全仓 `go test ./... -skip Live` 仍有 3 个失败在 `internal/im/dingtalk`（nih-integrations 新 DWS 测试：missing-executable 分类、cancel unblock 超时、marker 含换行），非本 scope。** |

## 5. 跨 scope 待办 / 未完成

1. **engine（conf-domain）**：缺“把 running Assignment 标记 interrupted”的 API。`RecoverAssignments` 目前只能补 note + 新 Assignment，旧 running 记录仍留在 `Ticket.Assignments`（`runtime/recovery.go`；期望 `Engine.InterruptAssignment(ticketID, assignmentID, reason)` 或 `RecoverAssignments` 内建实现）。
2. **engine**：`Feed()` 的 `Disposition.TicketIDs` 在 `EvaluateDue` 建单后未回填（`engine/engine.go` Feed 循环），runtime 投影已用 `Origin`/`DecisionIDs` 反推兜底（`runtime.ticketFor`）；建议 engine 回填以让 Feed 自洽。
3. **store（fix-core/nih-core）**：`Inspect/HealthView` 缺 `TicketsByStage`/`EffectsByKind`/`Workspaces`。`internal/runtime/status.go` 已用一条 mode=ro 连接查 `ticket.stage`/`workspace` 兜底（有 TODO），扩展后用官方字段替换。
4. **prompts（fix-engine/fix-ui2）**：团队三段、版本历史、base-hash、"Default updated"、占位符/长度/未知键校验未实现——`internal/prompts/**` 不在 fix-runtime scope；`prompt_version` 表若 engine 不加，可用 kv JSON（当前 kv 已有 `setting:prompt.*`）。
5. **reply 通道适配（本 scope 剩余）**：按 source 的模板（IM ≤100 字/词、GitHub 背景/改动/验证/交付物结构化）与 notify 档"不得直发同事"的投递决策尚未实现；`ReplyPlan` 已由 engine 产出，`deliverReply` 目前仍统一发往 origin 通道。建议下一轮在 `runtime/reply.go` 用 `t.Mandate`/`Decision.Reply` 收紧。
6. **IM 接线**：已完成（第二轮，见 §4bis），此条已关闭。
7. **`check` 的模型判定**：用 setup gaps 文本判断，未直接读 `setting:onboard.model`（`Status` 未导出 KV）；扩展 Inspect 后改成字段判断。
8. **`archive` 的 decisions/thread 行**：当前只移 ticket/trigger/disposition；decision 保留在主库（避免跨表悬空引用），若要求"按 180 天全清"需要 engine/store 确认引用关系。
9. **-race 全仓**：本 scope `internal/runtime` 已过；`cmd/zeokeel` 的 race 未单独跑（CI 统一步骤跑全仓）。
10. **docs（fix-docs-final）**：`design/ops-runbook.md` 每节"现状"可改为已实现；README/CHANGELOG 的 status/logs/backup/archive/headless/-demo 描述需按 §4 更新；`scripts/watchdog-check.sh` 可接 `zeokeel status --json`（现在 CLI 存在了）。

## 6. 未完成/被阻塞

- **无阻塞项**：Phase A 8 项全部完成；Phase B 除 §5.5（reply 通道模板）与 §5.6（IM flag 接线）外完成。
- **稳定性**：`TestSupervisorWritesAHealthRow` 原实现 sleep 等 60s 巡检，在全仓并行下偶发失败；已改为直接验证 `writeHealth` 写路径（无 sleep、无全局时钟依赖），全仓 `-count=1` 连续 2 次通过。
- **-race**：`internal/runtime` 与 `cmd/zeokeel` 均通过（CGO_ENABLED=1 单步，符合契约 §0.1）。
- **依赖外部 API**：Assignment 精确中断（§5.1）、Feed 回填（§5.2）、Inspect 计数（§5.3）、prompt 版本表（§5.4）需要对应 owner 提供后再收口；均已用诚实兜底实现并通过测试。
