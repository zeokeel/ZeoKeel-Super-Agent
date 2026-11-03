# Wave 1 独立反证验证报告（verifier-A）

> 角色：verifier-A，adversarial verification（目标是证伪，不是确认）。
> 快照时间：2026-11-03 20:02–20:22（Asia/Shanghai）。
> 代码基线：HEAD `5b3507e`（fixers 全部未 commit，工作区 221 个未提交变更）。
> 验证对象：**仅限已有 handoff 的 Wave 1 修复**：`audit/fixes/{fix-core,fix-docs-ci,fix-ui,fix-agent,fix-integrations}.md`。
> `fix-engine-api.md` 是 Wave 2 交接，只在其变更污染机械闸门时记录归属，不评价其正确性。
> 只读约束：未修改任何生产文件；新增的独立测试只有本报告列出的 `*_verify_test.go`。
>
> **并发警告（影响所有时间敏感结论）**：验证期间 Wave 2（fix-engine / fix-runtime / fix-ui2）仍在落盘，工作区多次处于半编译状态。本报告对每个机械结论都标注了快照时刻；最终合并后必须由 Lead 在冻结树上重跑 `scripts/verify-fixes.sh`。

---

## 0. 结论

| 检查组 | 判定 | 摘要 |
| --- | --- | --- |
| 1. 机械闸门 | **FAIL** | 20:18–20:20 快照 `verify-fixes.sh` **3 passed / 2 failed**（build/vet/nocgo PASS；test/race FAIL）；失败全部在 **Wave 2 in-flight 文件**（`cmd/zeokeel` 验收测试未跟上 two-phase Eval、`internal/runtime` 已知无锁读写 race），非 Wave1 owner（见 §1） |
| 2. agent（fix-agent） | **PASS** | 7 个独立反证测试 + 3 项死代码静态检查全部通过 |
| 3. core（fix-core） | **PASS** | store 9 / schedule 5 / skills 3 个独立反证测试通过；v1/v2/M4 真实旧库升级、flock、游标、cron/DST、符号链接逃逸均证伪失败（即修复成立） |
| 4. ui（fix-ui） | **PASS** | 6 个独立反证测试通过；`ListState.Key`、暗色 token、全角空格、humanize 均成立 |
| 5. integrations（fix-integrations） | **PASS** | GitHub 分页/marker 全页扫描 + 失败关闭、DingTalk JSON 控制字符/发送去重均通过 |
| 6. docs/CI 真实性（fix-docs-ci） | **FAIL** | 13 项成立，**8 处文档与代码不一致**（含 3 处自我矛盾的"证据"引用，见 §6） |
| 7. 卫生与 scope | **PARTIAL** | `go.mod/go.sum` 干净、无密钥、无 commit、`audit/AUDIT.md` 未改；但 2 处越出契约 scope（见 §7） |

**逐项断言计数（20:22 当前状态）：69 PASS / 10 FAIL / 2 PARTIAL / 3 UNVERIFIED。**（20:15 曾出现的 3 个额外编译/测试失败已随 Wave 2 落盘消失，只作为并发污染证据保留在 §1.2。）

**底线**：Wave 1 的**行为性修复本身基本可信**——所有 Wave1 owner scope 的独立测试与 `-race` 均通过；剩余"红"来自 **Wave 2 正在改的 `cmd/zeokeel`、`internal/runtime`、`internal/engine`**（20:15 曾整体不可编译，20:22 已收敛到"2 个测试面失败"），以及 **docs/CI 把 Wave1 写作时的事实与随后落地的 v3/删除代码混在了一起**。**不能在当前快照上宣布 Wave1 全绿**；合并前必须冻结并重跑闸门 + 文档最终 pass。

---

## 1. 机械闸门（check 1）

命令（每次都在仓库根、`export PATH=/usr/local/go/bin:$PATH`）：

```sh
sh scripts/verify-fixes.sh     # 5 steps: CGO=0 build / test / vet / nocgo / CGO=1 -race
```

### 1.1 时间线（同一脚本，三个快照）

| 时刻 | build | test | vet | nocgo | race | 归属 |
| --- | --- | --- | --- | --- | --- | --- |
| 20:02–20:04 | PASS | **FAIL** | PASS | PASS (`check-nocgo: PASS (zero cgo)`) | 未跑到 | `internal/runtime` `TestSettingsConflictAcrossWindows`（fix-core handoff §5.1 已声明的跨 scope 阻塞；owner fix-runtime） |
| 20:10 启动、20:15 完成 | **FAIL** | **FAIL**（随 build） | **FAIL** | **FAIL**（停在第 1 步 build） | **FAIL**（cmd/runtime 编译失败 + engine 测试失败） | 全部为 Wave 2 in-flight 文件，见 1.2 |
| 20:18 启动、20:20 完成 | PASS | **FAIL**（仅 `cmd/zeokeel` 8 个验收/e2e 测试） | PASS | PASS | **FAIL**（`internal/runtime` 数据竞争 + cmd 构建已恢复） | 20:15 的 G-1/G-2/G-3 已被 Wave2 修掉；剩余失败仍是 Wave2 测试面，见 1.2′ |

20:15 的完整结果：`verify-fixes result: 0 passed, 5 failed — failed: CGO_ENABLED=0 go build ./... CGO_ENABLED=0 go test ./... -skip Live CGO_ENABLED=0 go vet ./... scripts/check-nocgo.sh CGO_ENABLED=1 go test -race ./... -skip Live`

20:20 的完整结果：`verify-fixes result: 3 passed, 2 failed — failed: CGO_ENABLED=0 go test ./... -skip Live CGO_ENABLED=1 go test -race ./... -skip Live`

### 1.2 每个失败的文件与 owner（最小复现）

| # | 失败输出（实测） | owning file | owner | 结论 |
| --- | --- | --- | --- | --- |
**20:15 快照（已被后续落盘修掉，保留作为"并发污染"证据）**：`cmd/zeokeel/main.go:40/74/268` 的 `runCLI`/`setupLogging`/`serveHeadless` 未定义（fix-runtime 半落盘）；`internal/runtime/live_test.go:138` 与 fix-engine 新 `Ticket()` 返回值不匹配；engine `TestUnsubscribeAndHandBack` FAIL。**这些在 20:20 快照中已消失**（build/vet/nocgo 恢复 PASS）。

**20:20 快照的剩余失败（当前有效）**：

| # | 失败输出（实测） | owning file | owner | 结论 |
| --- | --- | --- | --- | --- |
| G-1 | `go test ./... -skip Live` 仅 `cmd/zeokeel` FAIL：`TestA2ColleagueRequestCarriesTheFourParts`、`TestA3OperatorRequestRunsWithoutApproval`、`TestA4SensitiveOutboundEscalates`、`TestA7RepeatedDeliveryCreatesOneTicket`、`TestOverviewShowsTheTicketThatExists`、`TestAppStateSurvivesRestart`、`TestEndToEndFromChatToReply`、`TestAcceptedTicketRunsAfterRestart`（8 个）；样例：`acceptance_test.go:66: expected a proposal and no ticket: evaluating []` | `cmd/zeokeel/acceptance_test.go`、`e2e_test.go`、`recovery_test.go`、`durable_test.go` | **fix-runtime（Wave 2）** | 引擎已改为 two-phase Eval（`Receive` 返回 `evaluating`，Ticket/Decision 由 `EvaluateDue` 产生），cmd 验收测试仍假设单阶段；不是 Wave1 行为回归 |
| G-2 | `-race`：`WARNING: DATA RACE` — Write `engine.(*Engine).Report()` `internal/engine/lifecycle.go:35` ← `runtime.reportTicket` `internal/runtime/artifacts.go:144` ← `worker.go:312`；Previous read `domain.(*Ticket).LastReport()` `internal/domain/ticket.go:561` ← `runtime/compaction_test.go:50`、`usage_test.go:45`。`--- FAIL: TestWorkerRunsWithSubagentsAttached: race detected during execution of test` | `internal/runtime/*_test.go` 读 `Engine.Ticket()` 指针 + `internal/engine/lifecycle.go` 写 | **fix-runtime（读侧）+ fix-engine（快照语义）** | 与 fix-docs-ci 交接 §5.1 已知的"测试线程读 Ticket、worker 线程写 Ticket"同源；`TicketSnapshot` 约定尚未在 runtime 测试/读取路径落地 |
| G-3 | 其余 race 包均 `ok`（store 9.9s、ui 16.1s、agent、im/dingtalk、github、schedule、skills…） | — | — | Wave1 scope 的 race 为绿（见 1.3） |

### 1.3 反向确认（不是"闸门坏了"）

- **闸门本身可信**：20:15 报 5 FAIL、20:20 报 3 PASS/2 FAIL，退出非 0，没有假绿（fix-docs-ci 的核心主张成立）。
- **Wave1 scope 的 race 全绿**（独立复跑，`CGO_ENABLED=1 go test -race`）：
  ```text
  ok internal/store 9.660s / schedule 2.242s / skills 1.069s / agent 18.229s
  ok sources/github 2.449s / im/dingtalk 1.088s / ui 14.850s
  ```
- **Wave1 scope 的非 race 测试全绿**（17 个包，含我新增的 verify 测试）：`scoped_test_status=0`。

> **裁决：FAIL（闸门当前红），但 FAIL 不归属于任何 Wave1 交接。** 修复动作：冻结树 → 由 fix-runtime/fix-engine 完成 `.go` 文件 → Lead 重跑同一脚本。若要求"Wave1 结束时绿"，当前状态不满足。

---

## 2. agent（fix-agent handoff，check 2）

新增独立测试：`internal/agent/verify_agent_test.go`（7 tests）。命令：`CGO_ENABLED=0 go test ./internal/agent/ -run TestVerify -v`（全 PASS；`-race` 下同包亦 PASS）。

| 检查 | 期望 | 实测 | 判定 | 证据 |
| --- | --- | --- | --- | --- |
| glob `**` 递归 | `**/*.go` 命中 `a/b/c/z.go`、`internal/x/y/x.go`、`top.go`，不命中 `.md`；`**/x.go` 命中嵌套 | 全部命中 | PASS | 测试 `TestVerifyGlobDoubleStarIsRecursive`；实现 `workspace.go:278`（`doublestar.Glob(os.Root.FS(), …)`） |
| 符号链接逃逸 | 读/写经逃逸符号链接必须被拒，外部文件不变；目录链接同样拒绝 | `Read`/`Write` 均 error，外部文件仍为 `outside` | PASS | `TestVerifySymlinkEscapeRefused`；`workspace.go:129-155`（`os.OpenRoot`/`Root.*`）、`toolErrorObservation` 把 `path escapes from parent` 归为 observation |
| `blockedCommands` 多词 | `git push --force origin main` 命中 `git push --force`；`git status` 放行；`rm` 单词 legacy 仍有效；空白归一 | 全部符合 | PASS | `TestVerifyBlockedCommandsMultiWord`；`tools.go:210-235` |
| bash 超时是 observation | 生产路径（job registry 存在）下 `sleep 5` + 1s 超时 → `ExitCode=124`、无 Go error、~1s 返回 | 实测 124，1.0–1.5s 返回 | PASS | `TestVerifyBashTimeoutIsAnObservation`；`tools.go:108-135` |
| blocked 命令是 observation | `ExitCode!=0` + `refused` 文本、无 Go error | 符合 | PASS | `TestVerifyBlockedCommandIsObservation` |
| 悬空 tool_call 经 **RunSession** 修复 | 日志含 2 个未配对 tool_call → `RunSession` 成功、`Resumed=true`、模型收到 2 条 interrupted tool 消息 | 两条都配对（`call_a`/`call_b`） | PASS | `TestVerifyRunSessionRepairsMultipleDanglingToolCalls`；生产路径 `runtime/worker.go:282` → `agent.go:283`；补丁 `session.go:58`；官方中间件 `engine_adk.go`（`patchtoolcalls`） |
| 工具名/描述保留 | 5 个文件工具名与顺序 = `read,write,edit,glob,grep`；描述非空；edit 描述用 `old_string` 而非 `old_text` | 符合 | PASS | `TestVerifyFileToolNamesAndDescriptions`；`workspace.go:37-55,531-536` |
| `engine_react.go`/`Runner.Run` 删除 | 文件不存在、无导出 `Run` | 均不存在/无 | PASS | `ls internal/agent/engine_react.go` → No such file；`grep func (r *Runner) Run(` → 空 |
| `LoadEvents`/`SessionStats` 导出删除 | 无导出 | 无 | PASS | grep 为空（包内 `sessionStats` 保留） |
| MCP/workflow 死 API 删除 | `Manager.Health/Restart/Refresh/Ping`、`workflow sqlite.go` 不存在 | 均不存在 | PASS | grep 为空；`ls internal/workflow` → memory.go/workflow.go/tests |

**观察（低，非 Wave1 断言失败）**：`runBash(..., jobs=nil, def=timeout, ...)` 路径实际不执行 timeout——`executeShell` 用调用方 ctx 建 `CommandContext`，没有 `context.WithTimeout`；`def` 只影响错误文案。生产永远走 jobs!=nil 路径（`workspaceTools` 必建 registry），故不影响"bash 超时 124"的交接主张；但该防御分支是个 latent bug，建议 Wave 2 顺手修（`tools.go:139-160`）。

---

## 3. core（fix-core handoff，check 3）

新增独立测试：`internal/store/verify_core_test.go`（10 tests，含 1 helper）、`internal/schedule/verify_core_test.go`（5）、`internal/skills/verify_core_test.go`（3）。fixture 用**旧版布局 DDL**（v1 base、v2 ALTER、M4 时代 schedule/goal 表）手工建库，不复用当前 `migrations`，避免"代码自己定义测试"。

| 检查 | 期望 | 实测 | 判定 | 证据 |
| --- | --- | --- | --- | --- |
| `SchemaVersion==3` | 常量=3 | 3 | PASS | `TestVerifyV2FileUpgradesFromRealOldLayout`；`schema.go:19` |
| v1 真实库升级 | Open 成功；`kv.schema=3`；M4 marker 删除 | 符合 | PASS | 同上 |
| `trigger.at` 定宽 + 文本序=时间序 | 旧值 `...00.5Z`/`...00.55Z` 升级为 `.500000000Z`/`.550000000Z` 且 ORDER BY 序正确 | 符合 | PASS | 同上（v1 写入 `.5Z`/`.55Z`，升级后两条断言）；`store.go:115-124` `FormatTime` |
| 其它时间列规范化 | `thread.last_at`、`ticket.created_at` 定宽 | 符合 | PASS | 同上 |
| `trigger_thread` 回填 | v1 Trigger JSON → 2 行倒排，`ThreadTimeline` 旧→新 | 符合 | PASS | 同上；`migrate.go:backfillTriggerThread` |
| `outbox.dropped` 回填 | `state='gone'` 有 `dropped_at`+`dropped_reason='gone'`；`pending` 保持空 | 符合 | PASS | 同上；`migrate.go:backfillOutboxDropped` |
| v3 表/列 | schedule/schedule_fire/goal/trigger_thread 存在；decision.thread、schedule.task、outbox.dropped_* 存在 | 符合 | PASS | 同上（`pragma_table_info` 断言） |
| **M4 时代库**（schema=2 + kv marker + 已有 schedule 行） | Open 不报 "table already exists"；行保留；`schedule.task` 补上；marker 删除 | 符合（行 spec/status 不变） | PASS | `TestVerifyM4EraFileUpgrades`；`schema.go:9-18` 的设计理由、`migrate.go:applyV3` |
| 更新 schema 拒绝 | `kv.schema=4` → `ErrNewerSchema` | 符合 | PASS | `TestVerifyNewerSchemaRefused`；`store.go:22-23,292-295` |
| flock 第二写者 + Inspect | 第二次 `Open` → `ErrAlreadyRunning`；锁内 `Inspect` 读到 schema=3/kv；Close 后可重开；缺文件报错 | 符合 | PASS | `TestVerifySecondWriterRefusedInspectWorks`；`lock.go`/`lock_unix.go`、`inspect.go:46` |
| 嵌套 SAVEPOINT + afterCommit | 内层回滚只丢内层；提交才跑 callback；外层回滚不跑 | 符合 | PASS | `TestVerifyNestedAtomicAndAfterCommit`；`store.go:Atomic` |
| 跨 goroutine 读不加入事务 | 事务打开期间读者阻塞，提交后读到已提交值 | 阻塞 100ms 后读到 | PASS | `TestVerifyReaderWaitsForCommandTransaction`；`store.go:executor()` cond 等待 |
| 并发命令串行 | 6 writer + 4 reader 无错误、计数精确 | 符合 | PASS | `TestVerifyConcurrentCommandsSerialized` |
| 时间线 cursor 同刻不丢 | 同 `at` 3 行分页 2+1 无重复/跳过；坏 cursor → `ErrBadCursor` | 符合 | PASS | `TestVerifyTimelineCursorSkipsNoTies`；`load.go:104-160` |
| 跨进程定宽排序 | 子进程（真实第二进程）写两条，父进程 `ORDER BY at` 时间序正确、定宽 | 符合 | PASS | `TestVerifyTriggerAtSortableAcrossProcesses`（helper 子进程 `TestVerifyHelperWriteStore`） |
| cron `N/step` | `0 0/6` → 0,6,12,18 时；`5/10` 分 → 5,15,…,55；`*/15` 不变 | 逐次全部符合 | PASS | `TestVerifyCronNumericStartStep`；`schedule/cron.go:87-120` |
| DST 春季空洞 | NY 2026-03-08 02:30 跳过 → 03-09T06:30Z | 符合 | PASS | `TestVerifyDSTSpringGapSkipped`；`next.go:wallTime` |
| DST 秋季重复 | NY 2026-11-01 01:30 取较早 05:30Z | 符合 | PASS | `TestVerifyDSTFallBackPicksEarlier`（首版用例取错墙钟 02:30，已按 tz 规则修正） |
| Lord Howe 30 分钟 DST | 2026-10-04 02:15 空洞跳过 → 10-05 02:15 (+11) | 符合 | PASS | `TestVerifyDSTThirtyMinuteGap` |
| São Paulo 午夜空洞 | 2018-11-04 00:00 跳过 → 11-05 00:00 (-02) | 符合 | PASS | `TestVerifyDSTMidnightHole` |
| skills 路径包含（文件） | 逃逸 SKILL.md 符号链接 → Load 报错，不加载 | 报错 | PASS | `TestVerifySkillFileSymlinkEscapeRefused`；`skills.go:139-160,281-310` |
| skills 路径包含（目录） | 逃逸目录符号链接 → Load 报错 | 报错 | PASS | `TestVerifySkillDirSymlinkEscapeRefused` |
| 根内绝对符号链接仍可用 | 指向根内的绝对链接可读，正文正确 | 符合 | PASS | `TestVerifySkillAbsoluteSymlinkInsideRootAllowed` |
| 死 API 删除 | `store.OpenTickets/Outbox(key)/Health`、`outbox.SenderFunc`、`domain.ParseThreadKey`、`brief.Threshold/OneLine`、`schedule.Schedule.Spec/Location` | 全部 0 引用 | PASS | grep（`Threshold` 仅剩 goal 的 `BlockThreshold`，属无关） |
| `schedule.New`/`goal.New` 不再建表 | 无 `EnsureV3` 调用；注释声明 v3 已在有序迁移 | 符合 | PASS | `schedule/store.go:78-90`、`goal/goal.go:122-134`；`EnsureV3` 只在注释/历史测试名中出现 |
| 跨平台 | `GOOS=windows` store/schedule/skills/effects/outbox 编译；`GOOS=darwin` store 编译 | exit 0 | PASS | 命令实测 |

**一处需 Lead 知悉的行为变化（非缺陷）**：v2 构建无法打开 v3 库（`ErrNewerSchema`），升级不可回退；与 fix-core handoff §4.1 声明一致。

---

## 4. ui（fix-ui handoff，check 4）

新增独立测试：`internal/ui/verify_ui_test.go`（6 tests，PASS；`-race` 下整个 ui 包 PASS）。

| 检查 | 期望 | 实测 | 判定 | 证据 |
| --- | --- | --- | --- | --- |
| 执行列表 `ListState.Key` | 非 nil；同一批 Ticket 倒序发布后选中行仍指向同一 `TicketRow.ID` | 符合（行号 0→末行，ID 不变） | PASS | `TestVerifyTicketListKeyFollowsTheTicket`；`execution.go:89` |
| 触发列表 `ListState.Key` | 同上（Thread 重排） | 符合 | PASS | `TestVerifyThreadListKeyFollowsTheThread`；`trigger.go:84` |
| 消息流 Key | 有 Thread ID → Key 非 nil 且 `FollowEnd`；无 ID → Key=nil（不得重复空 key） | 符合 | PASS | `TestVerifyStreamListKey`；`stream.go:37`；**注意** `runtime.go:400` 目前不填 ID，生产上 Key 仍退回行号（fix-ui handoff 已如实声明，Wave 2 接线） |
| 暗色 token 派生自 theme | `tokensFrom(DarkTheme)` 的 Bg/Surface/Border/Accent 等于主题字段；明暗不同；`tokensFrom(nil)` 不 panic 且按 Light | 符合 | PASS | `TestVerifyTokensDeriveFromTheme`；`tokens.go:170-196`、`app.go:283-288` |
| 全角空格不派发 | 仅 U+3000 草稿点"发送" → 不调用 Dispatch | 符合 | PASS | `TestVerifyFullWidthSpaceIsNotATask`；`input.go:36`（`strings.TrimSpace`） |
| humanize 分组 | token 总数渲染 `1,204,331` | 符合 | PASS | `TestVerifyGroupedTokenTotalRenders`；`overview.go:270`（`humanize.Comma`） |
| 框架化 | stream→`mui.List`、eval→`mui.Table`、search→`mui.List`(Header) | 符合 | PASS | `stream.go:40`、`eval.go:120`、`search.go:166`（grep） |
| demo 文件删除 | `internal/view/demo*.go` 不存在；`viewtest` 仅测试包 import | 符合 | PASS | `ls internal/view | grep demo` 空；`grep viewtest` 仅 `_test.go`/`viewtest` 自身 |
| 9 个图标删除 | 剩余 20 个图标全部有引用 | 20 | PASS | `icons.go`（grep 计数 20） |
| 无用户可见 "M2" | 生产 UI 无 M2 字符串（唯一 grep 命中是 SVG `M22` 路径） | 符合 | PASS | `grep -rn "M2" internal/ui/*.go`（非测试） |

**说明**：fix-ui handoff 声称"三包合计 80 个 PASS"，当前树为 70(ui)+25(view)+5(humanfmt) 且 ui 含 Wave2 新增文件（`actions.go`/`ticketdetail.go` 等），该数字在并发落盘下不可作为判据；未计为 FAIL。

---

## 5. integrations（fix-integrations handoff，check 5）

新增独立测试：`internal/sources/github/verify_integrations_test.go`（4）、`internal/im/dingtalk/verify_integrations_test.go`（4）。全部只用 `fakeCommand`/包内 fake transport，**无网络、无 gh 进程**。

| 检查 | 期望 | 实测 | 判定 | 证据 |
| --- | --- | --- | --- | --- |
| GitHub 分页不静默截断 | 205 条 = 100+100+5，发 3 次 `page=1..3` | 符合 | PASS | `TestVerifyListPaginatesAndFailsOverCap`；`poll.go:list` |
| 超页上限失败而非截断 | 永远满页 → 第 20 页后 error `pagination exceeded`，共 20 请求 | 符合 | PASS | 同上；`poll.go:31` `maxListPages=20` |
| marker 搜全部页 | marker 在第 3 页（300 条）→ 返回既有评论 id `4242`，**0 次 POST**，恰好 3 次 GET | 符合 | PASS | `TestVerifyMarkerFoundOnLaterPage`；`channel.go:107-140` |
| marker 超限失败关闭 | 永远满页 → error（含 idempotency 说明）、0 次 POST、GET 次数 = 50 | 符合 | PASS | `TestVerifyMarkerScanFailsClosedAtCap`；`maxMarkerPages=50` |
| Poll 收到全部页 | 100+1 条评论 → receive 101 次、cursor 非空 | 符合 | PASS | `TestVerifyPollReceivesEveryPage` |
| DingTalk 文本 JSON 控制字符 | `\b \f \x00 \n \t`/引号/中文控制字符 round-trip 原样 | 全部原样 | PASS | `TestVerifyTextParamEscapesControlChars`；`driver.go:textParam` |
| DingTalk markdown JSON | 同上，`text` = 原文+marker，title 取首行 | 符合 | PASS | `TestVerifyMarkdownParamEscapesControlChars` |
| 发送去重（N-15） | 同一业务 key 第二次 Send 不再调 `SendRobot`（共 1 次），不同 key 才发第 2 次 | 符合 | PASS | `TestVerifySendIsIdempotentPerKey`；`driver.go:143-190` |
| 传输层不重试发送 | `im.NoRetry`（Attempts=1）用于 SendRobot | 符合 | PASS | `transport.go:332`、`httpclient.go:41-47` |
| HTTPTransport 不假装 Stream | `SupportsStream()==false` | 符合 | PASS | `TestVerifyHTTPTransportReportsNoStream`；`transport.go:310-320` |
| 测试不联网 | 无 `http.Get`/`net.Dial` 真实地址；仅 httptest / fake | 符合（inspection） | PASS | `grep` im/sources 测试 |
| 驱动回归 | 两个驱动包普通 + `-race` 全绿 | `ok im/feishu`、`ok im/dingtalk`（race 亦 ok） | PASS | §1.3 |

---

## 6. docs / CI 真实性（fix-docs-ci handoff，check 6）

### 6.1 成立的部分（13 项 PASS）

| # | 主张 | 验证 | 判定 |
| --- | --- | --- | --- |
| 1 | `design/prototype` 可离线构建（钉 mygo v0.7.4，无 `replace /tmp/mygo`） | `cd design/prototype && CGO_ENABLED=0 GOPROXY=off go build ./...` exit 0；`go vet` exit 0；`go.mod` 只有 `require mygo v0.7.4` | PASS |
| 2 | CI YAML 合法且 gate 就是 verify-fixes | `python3 yaml.safe_load` 通过；`ci.yml:33-34` `sh scripts/verify-fixes.sh` | PASS |
| 3 | launchd plist 合法 | `plistlib.load` 通过 | PASS |
| 4 | `watchdog-check.sh` 不假装监督 | `ZEOKEEL_BIN=/nonexistent sh scripts/watchdog-check.sh` → `no zeokeel binary found` 且 exit 0 | PASS |
| 5 | `verify-fixes.sh` 真的跑它所声称的 5 步，且唯一 cgo 步骤是 race | 读脚本：build/test/vet/check-nocgo + `env CGO_ENABLED=1 go test -race`；`SKIP_RACE` 仅手动 | PASS |
| 6 | `make verify` = 同一闸门 | `Makefile:32-35` | PASS |
| 7 | `release-check.sh` 复用同一闸门 | `release-check.sh:35-39` 调用 verify-fixes | PASS |
| 8 | 声称"没有 `zeokeel status`" | `cmd/zeokeel` 无 status 子命令/CLI 分支（grep 0） | PASS（对 Wave1 快照成立） |
| 9 | 声称"没有管理 API/HTTP" | `internal/runtime`/`cmd` 无 `net/http` 服务端（grep 0） | PASS |
| 10 | 声称"没有归档/VACUUM" | 无 archive/VACUUM 实现（唯一命中是 settings 文案） | PASS |
| 11 | 声称"没有系统钥匙串" | 无 keychain/Secrets 实现（唯一命中是 flag 帮助文本） | PASS |
| 12 | 声称 IM 驱动"未接线"（行为） | `startIMLoop` 仍无调用者；`cmd` 没有 IM flag | PASS（**但 import 描述错误，见 6.2-1**） |
| 13 | RELEASE.md F 组 = 12 项 | 计数 12 | PASS |

### 6.2 不成立的部分（8 项 FAIL，owner 均为 **conf-product / fix-docs-ci**）

| # | 文档主张（file:line） | 代码事实 | 最小复现 | 判定 |
| --- | --- | --- | --- | --- |
| D-1 | "飞书/钉钉驱动未接线（代码在，**`cmd` 不 import**）"；design 甚至写"`go list -deps ./cmd/zeokeel` 可证"：`README.md:17,145`、`CHANGELOG.md:72`、`design/zeokeel-go-mygo-design.md:1898` | `cmd/zeokeel` **确实 import** `internal/im`：`cmd/zeokeel/extras.go:14`、`main.go:23`；`go list -deps ./cmd/zeokeel` 输出含 `github.com/zeokeel/super-agent/internal/im`。正确说法是"驱动包（`im/feishu`、`im/dingtalk`）未 import、`startIMLoop` 无调用者" | `go list -deps ./cmd/zeokeel \| grep internal/im` | **FAIL（事实错误 + 自我否定的证据引用）** |
| D-2 | "v3 表走 `EnsureV3` 旁路 marker（`SchemaVersion` 仍 2）"：`design/…:1549`、`:1910` | fix-core 已折叠：`SchemaVersion=3`（`internal/store/schema.go:19`），`schema_v3.go`/`EnsureV3` **已删除**；文档引用了不存在的文件 | `ls internal/store/schema_v3.go` → No such file；`grep -n "SchemaVersion" internal/store/schema.go` → 3 | **FAIL（stale by merge order）** |
| D-3 | design §13 的 ✅ 证据行：`internal/domain/ticket.go:267`（StageOf）、`internal/engine/engine.go:590`（replyPlanLocked） | StageOf 在 `internal/domain/ticket.go:516`；`replyPlanLocked` 在 `internal/engine/evaluate.go:568`（engine 被 Wave2 拆分） | `grep -n "func StageOf"` / `"func (e \*Engine) replyPlanLocked"` | **FAIL（"✅ 必须指到证据行"规则被自身违反）** |
| D-4 | "托盘'暂停运行时'是空菜单项（Wave 2 接线）"：`README.md:148` | fix-ui 已删除该菜单项：`internal/ui/tray.go:10-12`（"A '暂停运行时' item that did nothing was removed"） | `grep -n "暂停" internal/ui/tray.go` | **FAIL（描述了一个已不存在的 bug）** |
| D-5 | "单写者 SQLite（**schema v2**）"：`CHANGELOG.md:59` | 当前 `SchemaVersion=3` | 同 D-2 | **FAIL（stale）** |
| D-6 | "修复 eino 默认工具检查器静默吞 tool_call 的坑"：`CHANGELOG.md:56`；`design/M0-tasks.md:214` 引用 `bufferToolCallChecker` | fix-agent 已删除 `engine_react.go`（该修复唯一载体），生产 ADK 路径未使用该 checker；文档未按交接 §5.3 改写 | `ls internal/agent/engine_react.go` → No such file | **FAIL（引用了已删除实现）** |
| D-7 | RELEASE.md C 组把 `CGO_ENABLED=0 go test ./... -skip Live` 标为 `[x]`：`RELEASE.md:39` | 20:02 快照该命令 FAIL（`TestSettingsConflictAcrossWindows`）；20:15 快照因 cmd 不可编译 FAIL。fix-core handoff §5.1 已声明这是已知阻塞 | `CGO_ENABLED=0 go test ./... -skip Live` | **FAIL（勾选早于事实；应由 Wave2 收尾时再勾）** |
| D-8 | "移除 11 张截图与 contact-sheet"：`CHANGELOG.md:81` | 清理提交实际删除 16 张 shot（`design/prototype/shots/00-flow.png` … `15-task-inbox.png`）+ 1 张 contact-sheet | `git show 03b25e9 --stat \| grep -c "prototype/shots/"` → 16 | **FAIL（历史数字错误，低严重度）** |

### 6.3 docs 相关 UNVERIFIED

- GitHub 上 CI 从未在本分支实际运行过（本机只按同一脚本模拟）；"CI 绿"不可由本报告证实。
- `scripts/release-check.sh` 完整跑（含打包产物）未执行（树红 + 无 dist 产物）。
- 文档/代码一致性为**抽样**核对（README、CHANGELOG、RELEASE、RELEASE-STEPS、design §13/§7.1/§6.7/§8、M0-tasks）；未逐字审计全部 7 份分片报告。
- **Wave 2 漂移提示（非 Wave1 失败）**：验证期间 Wave 2 已落盘 `cmd/zeokeel/{cli,logging,service}.go`（status/check/headless + 0/1/2/3）、`internal/runtime/{supervisor,status}.go`（进程内 watchdog/健康）。README/CHANGELOG/design 里"没有 status/watchdog/文件日志"的 Wave1 表述在最终文档 pass 之前会再次过时——fix-docs-final 必须重核这批断言，不要沿用 Wave1 文本。

---

## 7. 卫生与 scope（check 7）

| 检查 | 期望 | 实测 | 判定 |
| --- | --- | --- | --- |
| 无新增直接依赖 | `git diff go.mod go.sum` 为空 | 空 | PASS |
| 未 commit / 未 tidy | HEAD 仍 `5b3507e`；工作区可 diff | 符合 | PASS |
| 无提交密钥 | 扫描 `sk-…`/AKIA/PRIVATE KEY（排除 mock） | 0 命中（仅 `sk-mock` 测试值） | PASS |
| `audit/AUDIT.md` 未被改 | 不在 `git status` | 符合 | PASS |
| 零 cgo | 20:02 `check-nocgo.sh` → PASS；当前失败仅因树红 | PASS（对 Wave1 快照）/ 当前随闸门红 | PASS/PARTIAL |
| **scope 越界 1**：`internal/humanfmt/**`（新增包） | 契约 §1 给 fix-ui 的写范围 = `internal/ui/**`、`internal/view/**`、`internal/ui/assets/**` | fix-ui handoff 自述"已告知 Lead"，但契约里没有书面授权；包本身与实现质量无关 | **PARTIAL（需 Lead 追认）** |
| **scope 越界 2**：`tools/**`（genicon、seedstate 与新增测试） | 契约 §1 给 fix-integrations 的写范围 = `internal/im/**`、`internal/sources/**` | fix-integrations handoff 第 3 行自行把 scope 扩为含 `tools/**`；契约未改 | **PARTIAL（需 Lead 追认）** |
| 其它 scope | Wave1 各 handoff 列出的文件都在其契约范围内（store/domain/effects/outbox/schedule/skills/brief/scan/goal；agent/mcp/workflow；ui/view；im/sources；docs/CI/packaging/design） | 符合 | PASS |
| 并发污染 | 验证期间 Wave2（fix-engine/fix-runtime/fix-ui2）在写 `internal/engine`、`internal/runtime`、`cmd`、`internal/ui` 新文件 | 221 个未提交变更；`cmd` 曾两度不可编译 | **风险提示（非 FAIL）** |

---

## 8. 未验证（UNVERIFIED）清单

1. 真实窗口/托盘/暗色外观（无显示器；只验证了 `ui.NewTester` 无头路径与 token 派生）。
2. 真实 GitHub/飞书/钉钉凭据联调（按审计规则不执行网络）。
3. CI 在 GitHub 上的实际结果；`release-check.sh` 完整运行；三平台打包产物。
4. `-race ./...` 全仓绿（当前被 Wave2 编译错误阻断；Wave1 scope 的 race 已独立验证为绿）。
5. "所有历史文档主张"的穷尽核对（抽样）。

---

## 9. 失败清单（按 owner，供 Lead 直接转派）

**conf-product / fix-docs-ci（8 项，全部文本级，无需改行为）**
1. D-1：README:17,145 / CHANGELOG:72 / design:1898 的"`cmd` 不 import internal/im"错误 → 改为"驱动包未 import、`startIMLoop` 无调用者"。
2. D-2：design:1549,1910 的 v3 旁路描述 → 改为 `SchemaVersion=3`，删除 `schema_v3.go`/`EnsureV3` 引用。
3. D-3：design:1894 → `internal/domain/ticket.go:516`；design:1900 → `internal/engine/evaluate.go:568`。
4. D-4：README:148 删除"托盘暂停运行时是空菜单项"。
5. D-5：CHANGELOG:59 "schema v2" → v3。
6. D-6：CHANGELOG:56 / design/M0-tasks:214 的 checker 主张 → 按 fix-agent 交接 §5.3 改写。
7. D-7：RELEASE.md:39 的 `go test ./...` 勾选 → 待 Wave2 后重勾（当前红）。
8. D-8：CHANGELOG:81 "11 张截图" → 16 张（或删数字）。

**fix-runtime（Wave 2，仍阻塞闸门；非 Wave1 断言）**
9. G-1（20:20 当前）：`cmd/zeokeel` 的 8 个验收/e2e/恢复测试仍按单阶段 Eval 写 → `acceptance_test.go:66: expected a proposal and no ticket: evaluating []`。需跟进 two-phase Eval（`Receive` 只入队、`EvaluateDue` 建 Ticket）改测试，或修 runtime 的默认 pass。
10. G-2（20:20 当前）：`-race` 数据竞争 — `engine.(*Engine).Report()`（`internal/engine/lifecycle.go:35`）写 vs runtime 测试 `Ticket.LastReport()`（`internal/domain/ticket.go:561`）读；修复方向见 fix-engine-api 交接：runtime/UI 跨 goroutine 读取用 `TicketSnapshot`，测试不要拿 `Engine.Ticket()` 的指针。
11. （已解决，仅记录）20:15 的 `runCLI`/`setupLogging`/`serveHeadless` 未定义、`live_test.go` 类型不匹配、`TestUnsubscribeAndHandBack` FAIL 在 20:20 快照中已消失。

**fix-engine（Wave 2，与本报告唯一交集）**
12. 见 G-2 的写侧；另外新 handoff `audit/fixes/fix-engine.md`、`fix-ui2.md` 已在 20:21 落盘，属 Wave 2，不在本报告验证范围。

**Lead（协议）**
13. 追认 2 处 scope 越界（`internal/humanfmt`、`tools/**`），或在契约 §1 补授权后让后续 wave 遵守。
14. 冻结树后重跑 `sh scripts/verify-fixes.sh` 作为 Wave1 权威结果；本报告 20:15 的 0/5 与 20:20 的 3/2 都包含 Wave2 编辑，不能当作 Wave1 质量结论。

---

## 10. Wave1 handoff 主张覆盖表

| handoff | 主张 | 本次反证结果 |
| --- | --- | --- |
| fix-agent | N-2/N-3/N-5/N-6/F22/工具 error observation/死代码 | 全部 **CONFIRMED**（§2；1 条 latent nil-jobs 观察） |
| fix-core | N-13 v3、N-12 cron/DST、N-6 skills、store 不变量（flock/时间/Atomic）、H1 schema 部分、死 API | 全部 **CONFIRMED**（§3） |
| fix-ui | N-8 Key/List/Table、N-9 暗色、N-10 humanize/trimSpace、N-11 局部收敛、死代码/M2 文案 | 全部 **CONFIRMED**（§4；stream Key 生产未启用已被 handoff 如实声明） |
| fix-integrations | N-14 分页/marker/ETag、N-15 不重试、N-1 JSON/去重/Stream 降级、F8/F9、无联网 | **CONFIRMED**（§5；ETag 未独立写测试，但已由分页/304 相关代码与 owner 测试抽样核对，属抽样覆盖） |
| fix-docs-ci | CI 单闸门、prototype 可复现、ops 文档诚实、README/CHANGELOG/design 校正 | **部分 FAIL**：机制类 13 项 PASS；文本真实性 8 项 FAIL（§6） |

---

*报告生成：2026-11-03 20:16（Asia/Shanghai）。所有命令均在 `CGO_ENABLED=0`（除标注的 `-race` 步骤为 `CGO_ENABLED=1`）下执行；未修改生产代码。*
