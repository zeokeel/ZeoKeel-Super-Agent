# fix-core handoff — Wave 1（store/domain/effects/outbox/schedule/skills/brief/scan/goal）

> 范围：`internal/{store,domain,effects,outbox,schedule,skills,brief,scan,goal}`。
> 未触碰 engine/runtime/agent/ui/cmd/go.mod/go.sum/audit 报告。无 git commit，无 go mod tidy。
> 零 cgo 保持；未新增直接依赖（仅开始 import 已在 go.mod 的 `golang.org/x/sys`，见 §4.6）。

## 1. 完成的条目（引用 audit 编号）

| 工作项 | audit 编号 | 状态 | 关键改动 |
| --- | --- | --- | --- |
| 1. schema v3 折叠 | **N-13**、conformance-arch **C08** | fixed | `SchemaVersion=2→3`；`migrations` 改为 `[]migration{name, sql, apply}`；schedule/schedule_fire/goal/trigger_thread 进入有序迁移；新增 `migrate.go` 的 Go 侧步骤（条件加列、时间规范化、回填）；删除 `schema_v3.go`/`EnsureV3`/`kv['schema.schedule_goal']`，`schedule.New`、`goal.New` 不再建表 |
| 2. cron `N/step` + DST | **N-12** | fixed | `cron.go`：`N/step` = `N-max/step`（`0/6`→0,6,12,18；`5/10`→5,15,…；`*/15` 不变）；`next.go` 的 30 行时区探测换成 `time.Date` + 墙钟往返 + 显式重复时刻 tie-break（取较早）；新增"不采用 robfig/cron"的代码注释 |
| 3. skills 路径包含 | **N-6**（"与 skills 同类问题一起改"）、**N-7**（评估） | fixed / evaluated | `skills.go` 全部读取改走 `os.OpenRoot`/`os.Root`（内核级拒绝越界，无 TOCTOU）；绝对路径符号链接若解析到根内则映射为根内相对路径再读，解析到根外拒绝并保留原有中文文案；评估 eino `adk/middlewares/skill` → **不采用**（§5） |
| 4. store 不变量 | conformance-arch **A01**（stateDir 锁）、nih-core F-4（BEGIN 在池连接上）、Lead item 4b/4d、Lead addendum | fixed | (a) `.zeokeel.lock` flock（unix）/ LockFileEx（windows）/ O_EXCL（其它），`ErrAlreadyRunning`；(b) 写库时间统一 `FormatTime` 定宽 UTC 9 位纳秒 + 容错 `ParseTime` + 迁移规范化旧行；(c) `Atomic` 改为 `database/sql` Tx（DSN `_txlock=immediate`），按 goroutine 归属路由语句，嵌套 SAVEPOINT/afterCommit/回滚语义保留；(d) `HealthEntries()` + `Inspect(path)` 只读路径；删除 audit 列出的死 API |
| 5. H1 部分 schema | conformance-arch **C08** + §5 表级 diff（trigger_thread / decision.thread / outbox.dropped） | fixed | 新表 `trigger_thread(thread,id,at,human, PK(thread,id))` + 索引；`decision.thread` 列 + `DecisionsByThread`（旧行由 `domain.Key(Global,"decision",triggerID)` 反推 + Trigger JSON 回填 thread）；`outbox.dropped_at/dropped_reason` + `MarkOutboxDropped`/`SettleOutbox` 落账 + 迁移回填（`state='gone'` 用 `updated_at/created_at`）；`PutTrigger` 自动写入倒排索引 |
| 6. P2 死代码/重复 | nih-core §6、**N-11**（部分） | fixed（本 scope 内） | 删除 `brief.Threshold/OneLine/firstLine`、`domain.ParseThreadKey`、`schedule.Schedule.Spec/Location`、`outbox.SenderFunc`、`domain.Disposition.Thread`；`brief.oneLine` 复用 `clipRunes`；`skills.truncateBlock` → `strings.ToValidUTF8`；`effects.truncate` → 按 rune 截断（中文错误文本不再被切半） |
| addendum | Lead 消息 | fixed | `store.Inspect(path) (HealthView, error)`：`mode=ro`、不迁移、不拿排他锁；`HealthView{Path, SchemaVersion, SchemaVersionKnown, Health []HealthEntry, KV map[string]string}`；`HealthEntry{Entry, State, Detail, At}`。`store.HealthEntries()` 返回同一结构（writer 进程内读） |

**保留并注明理由的 API**：`Store.Get/Set`（未在生产调用，但 Wave-2 契约 §2.2 要求"游标写 kv"，因此保留并补了文档注释）；`Store.DB()`（测试/诊断用，跨 scope 现有测试依赖）。

## 1b. Wave-2 追加（Lead addendum：`zeokeel status` 只读事实）

`internal/store/inspect.go` 的 `HealthView` 增加三个只读聚合并保留原有字段（`SchemaVersion`/`SchemaVersionKnown`/`Health`/`KV` 语义不变；`Inspect` 仍 `mode=ro`、不迁移、不拿排他锁）：

```go
type HealthView struct {
    ... // Path, SchemaVersion, SchemaVersionKnown, Health, KV unchanged
    Workspaces     int            // SELECT COUNT(*) FROM workspace
    TicketsByStage map[string]int // SELECT stage, COUNT(*) FROM ticket GROUP BY stage
    EffectsByKind  map[string]int // SELECT kind, COUNT(*) FROM effect GROUP BY kind
}
```

语义（已写进代码注释，供 fix-runtime 原样展示）：
- `TicketsByStage` 读的是 `ticket.stage` **缓存列**（`PutTicket` 保存时用 `domain.StageOf` 写入）。它是"最后一次落盘的阶段"，不是用另一个时钟现场重算的 domain 状态——status 不能拿不同时刻的事实去猜。
- `EffectsByKind` 统计**尚未执行的 effect**：`effect` 表里每一行都是未执行的（队列成功后删除该行），因此包含退避/`Later` 挂起的行；**刻意不加 `due<=now` 过滤**（挂起仍是 waiting，过滤会让 status 与面板不一致）。如需"现在可跑"的计数，Wave-2 可另行增加字段，本次按上述语义实现。
- 外部/非 Zeokeel 数据库或 v0 文件：返回零值（空 map、0 计数、`SchemaVersionKnown=false`），不猜。

测试（新增 `internal/store/inspect_test.go`）：
- `TestInspectAggregateFacts`：种子库（2 workspace；ended / person/answer / working/queued 三种 stage 各 1；effect `reply`×2 其中一条 future-due、`launch`×1）→ 聚合逐一断言；原有字段不受影响。
- `TestInspectAggregatesWhileWriterRuns`：writer 持有 stateDir 锁时 `Inspect` 仍读到聚合；第二个可写 `Open` 仍 `ErrAlreadyRunning`。
- `TestInspectForeignDatabaseYieldsZeroes`：非 Zeokeel 库（只有无关表）→ 零值、无错误。
- 既有 `TestSecondWriterIsRefusedAndInspectStillWorks` 保持通过。

## 2. 改动文件（全部在本人 scope）

新增：
- `internal/store/schema.go`（重写为有序 migration 列表，v3 含新表）
- `internal/store/migrate.go`（新增：条件加列、时间规范化、trigger_thread/outbox 回填）
- `internal/store/lock.go`、`lock_unix.go`、`lock_windows.go`、`lock_other.go`（新增：进程锁）
- `internal/store/goroutine.go`（新增：命令 goroutine 归属判定）
- `internal/store/inspect.go`（新增：`Inspect`/`HealthView`/`HealthEntry`/`HealthEntries`）
- `internal/store/migrate_test.go`、`internal/store/invariants_test.go`、`internal/store/settings_test.go`（新增测试）
- 删除 `internal/store/schema_v3.go`、`internal/store/schema_v3_test.go`

修改：
- `internal/store/store.go`（Atomic/Tx、时间格式、PutTrigger/decision/thread 写入、Open/Close 锁）
- `internal/store/load.go`（时间解析、`TriggerThreadPage`/`ThreadTimeline`/`ThreadCursor`、`DecisionsByThread`、outbox dropped 字段/方法、删 `Outbox`/`Health`）
- `internal/store/store_test.go`、`load_test.go`（移除已删 API 的断言、版本 2→3）
- `internal/schedule/cron.go`、`next.go`、`spec.go`、`store.go`、`schedule_test.go`
- `internal/skills/skills.go`、`render.go`、`skills_test.go`
- `internal/effects/effects.go`（rune 截断）、`effects_test.go`（HealthEntries）
- `internal/outbox/outbox.go`（删 SenderFunc）
- `internal/brief/brief.go`、`digest.go`
- `internal/domain/ids.go`、`trigger.go`、`ticket.go`（`Decision.Thread` 新增）
- `internal/goal/goal.go`（去掉 EnsureV3）

## 3. 新增/修改的测试与结果（命令 + 实际输出）

```
$ CGO_ENABLED=0 go build ./...
（无输出 = 通过，整个根 module 可编译；含其他 agent 当前工作区）
$ CGO_ENABLED=0 go test ./internal/store/... ./internal/effects/... ./internal/outbox/... \
    ./internal/schedule/... ./internal/skills/... ./internal/brief/... ./internal/scan/... \
    ./internal/goal/... ./internal/domain/... -count=1
ok internal/store 0.672s | effects 0.297s | outbox 0.251s | schedule 0.145s | skills 0.024s
ok brief 0.004s | scan 0.008s | goal 0.254s | domain 0.004s
$ CGO_ENABLED=1 go test -race ./internal/store/ ./internal/effects/ ./internal/outbox/ -count=1
ok store 6.106s | effects 3.651s | outbox 3.156s
$ GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build ./internal/store/ ./internal/schedule/ ./internal/skills/   → exit 0
$ GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build ./...                                                    → exit 0
$ gofmt -l <9 个 scope 包>  → 空
$ CGO_ENABLED=0 go vet <9 个 scope 包> → 通过
$ ./scripts/check-nocgo.sh ./internal/store/ ./internal/skills/ ./internal/schedule/ → "check-nocgo: PASS (zero cgo)"
$ CGO_ENABLED=0 go test ./internal/engine/ -count=1 → ok（跨 scope 依赖无回归）
```

关键回归测试（新增）：
- `TestSchemaV2FileUpgradesToV3`：手工造 v2 文件（RFC3339Nano 的 trigger.at、gone 回执）→ Open → 版本=3、新表/新列齐全、`at` 规范化为定宽（文本序=时间序）、`trigger_thread` 由 Trigger JSON 回填、decision.thread 由 Trigger JSON 反推回填、gone 回执补齐 dropped_at/reason、M4 marker 被删。
- `TestM4EraFileUpgradesToV3`：**M4 时代文件**（schema=2 + `kv['schema.schedule_goal']` + schedule/goal 表已存在 + 一行 schedule 数据）→ Open 成功、行保留、`schedule.task` 列补上、marker 删除。这是旧注释"以后折进 migrations"会踩的 `table already exists` 回归。
- `TestNewerSchemaRefusedWithDetail` / 既有 `TestNewerSchemaIsRefused`：v4 文件被 v3 构建拒绝（`ErrNewerSchema`）。
- `TestFreshStoreIsFullyMigrated`：全新库版本=3，15 张表、所有新增列齐全。
- `TestTriggerThreadPageCursorDoesNotSkipSameInstant`：同一时刻 3 条 → 分页 2+1 不丢行；坏 cursor → `ErrBadCursor`。
- `TestDecisionsByThread`、`TestHealthEntriesOrdered`、`TestSaveSettingsRejectsStaleBase`。
- `TestSecondWriterIsRefusedAndInspectStillWorks`：同目录第二次 `Open` → `ErrAlreadyRunning`；同时 `Inspect` 读到 schema/health/kv；Close 后可重新 Open；缺文件 Inspect 报错。
- `TestReaderCannotJoinCommandTransaction`：命令事务打开期间另一 goroutine 的读**阻塞**，提交后读到已提交值（旧实现会读进事务，是静默错误）。
- `TestConcurrentWritersAndReaders`：8 goroutine × 25 次读改写 + 4 reader × 50 次查询，计数精确、无 `transaction already committed`。
- `TestCronNumericStartStepMeansStartToMax`：0/6、5/10、*/15 的逐次触发时刻。
- `TestWallTimeResolvesDSTRules`：NY 春季空洞、NY 秋季重复取较早（05:30Z）、Lord Howe 30 分钟空洞、São Paulo 午夜空洞。
- skills 新增：相对路径越界符号链接拒绝、根内绝对符号链接（目录与 SKILL.md）仍可用；原有 337 行符号链接/校验测试全部保持语义（文案不变）。

## 4. 行为变化与兼容性风险（需要 Lead/其他 agent 知道）

1. **schema 单向升级 2→3**：v1/v2 文件自动升级并规范化时间；已跑过 M4（有 `schema.schedule_goal`）的文件也能升级（`IF NOT EXISTS` + 条件加列）。**v2 构建会拒绝打开 v3 文件**（`ErrNewerSchema`），发布说明需写明"升级不可回退"。
2. **单实例锁**：`store.Open` 现在在数据库所在目录创建 `.zeokeel.lock` 并加排他锁，第二个可写打开返回 `ErrAlreadyRunning`（含锁文件路径）。影响：任何"同一 stateDir 同时开两个可写 Store"的代码/测试会失败——本仓库只有 **1 处**（见 §5）。崩溃不会留死锁（内核释放 flock/LockFileEx）。
3. **事务 goroutine 归属**：`Atomic` 内的语句只由发起事务的 goroutine 执行；其它 goroutine 的 `Exec/Query/QueryRow` 会**等待命令提交**后读取已提交状态，而不是加入事务。嵌套 `Atomic` 仍为 SAVEPOINT，但必须是同一 goroutine（与旧注释"one command goroutine"一致）。用 `runtime.Stack` 取 goroutine id，仅当事务打开时每条语句多几微秒（命令内语句数级别，可忽略）。
4. **时间列格式**：`trigger.at`、`thread.last_at`、`ticket.created_at/ended_at`、`outbox.created_at/updated_at`、`workspace.created_at/updated_at`、`health.at` 一律 `FormatTime`（`2006-01-02T15:04:05.000000000Z`）。旧数据在 v3 迁移里就地规范化；`ParseTime` 同时接受 RFC3339Nano，读到未迁移文件也不会报错。
5. **删除的导出 API**（均已 `grep` 确认零非测试引用，全仓 build 通过）：`store.OpenTickets`、`store.Outbox(key)`、`store.Health`（→ `HealthEntries`）、`outbox.SenderFunc`、`domain.ParseThreadKey`、`domain.Disposition.Thread` 字段、`brief.Threshold/OneLine`、`schedule.Schedule.Spec/Location`。若某个 Wave-2 分支持有旧引用，改用上述新 API。
6. **依赖**：新增 import `golang.org/x/sys/unix` / `golang.org/x/sys/windows`——它已在 `go.mod` 作为 `// indirect`（`golang.org/x/sys v0.49.0`），本次**未改 go.mod/go.sum**，`CGO_ENABLED=0` 构建通过。**Lead 执行 `go mod tidy` 后会把它提升为直接依赖**（预期 diff：`golang.org/x/sys` 移除 `// indirect` 注释）。
7. **skills 符号链接语义**：读取全部经 `os.Root`；根内相对链接原样可用；根内**绝对**链接会被映射成根内相对路径再读（保留旧行为）；越界（绝对或相对）一律拒绝，错误文案保持"逃出了根目录"/"指向根目录外"。差异：断链/列目录后被删除的条目从"报错"变为"跳过"（更符合最佳努力原则）。
8. **effects 截断**：health detail / effect error 改按 rune 截断（上限仍是 1000/500），CJK 不再产生半个字符；列是 TEXT，字节上限约 4×，不影响 SQL 比较。
9. **不再有迁移框架/编解码器新增**：未引入 goose/golang-migrate/sqlc/ent（理由见 nih-core §3.1）。

## 5. 跨 scope 待办（file:line + 期望改动）

1. **[阻塞全绿] `internal/runtime/concurrency_rev_test.go:18-28`**（TestSettingsConflictAcrossWindows；owner: fix-runtime）：现在两次 `store.Open` 同一 db 会因单实例锁失败。最小改法：只开**一个** store 句柄，两个窗口都用它（`winA.LoadSettings(stA)` 与 `winB.LoadSettings(stA)`）；stale-revision 断言不变（窗口 B 内存里仍是旧 revision）。覆盖已由 `internal/store/settings_test.go` 的 `TestSaveSettingsRejectsStaleBase` 在我的 scope 内保住。若 Lead 决定保留"多窗口多进程"产品能力，则需另加 `store.OpenShared`（LOCK_SH）——本人不推荐（effect queue 会双跑）。
2. **`internal/engine`（owner: fix-engine，Wave 2）**：创建 Decision 时填 `domain.Decision.Thread = <thread key>`（`engine.go` 里 `Receive`/`Approve` 的 `dec := domain.Decision{...}`，约 engine.go:197-254 与 640-660），这样 `decision.thread` 列才有值；`PutTrigger` 已自动写 `trigger_thread`，engine 无需改调用点。时间线读路径可用 `store.TriggerThreadPage(thread, cursor, limit)` / `ThreadTimeline(thread, limit)` / `store.ThreadCursor(at,id)`。
3. **`internal/runtime` + `cmd/zeokeel`（owner: fix-runtime，Wave 2）**：
   - `zeokeel status`：用 `store.Inspect(filepath.Join(stateDir, "zeokeel.db"))` 读取（**不要求写锁、不迁移、可在 app 运行时调用**）；打印 `SchemaVersion`、`Health[]`（entry/state/detail/at）、`KV`（含 `source.*` 游标、`setting.__revision`）。退出码按 006 对齐（0 健康 / 1 可重启修复 / 2 组件错误 / 3 等待 setup）。
   - 启动时若 `errors.Is(err, store.ErrAlreadyRunning)`，输出可读的"另一个 zeokeel 正在使用该 state 目录"并退出（不要静默继续）。
   - `zeokeel backup`/`archive` 是写操作：必须在 app 停止时运行（拿写锁），或新增只读归档路径；不要在 app 运行时再 `store.Open`。
4. **`internal/humanfmt` 未创建**：Lead 指示"若加先问"。本 wave 只在 scope 内去重（brief oneLine→clipRunes、skills truncateBlock→stdlib、effects truncate→rune）。跨包 humanAge/humanBytes/truncate/clip 的收敛建议由 Lead 指定 owner（fix-ui 可能拥有），涉及 `internal/{ui,view,runtime,brief}`。
5. **C08 剩余 schema 项（不在本 wave 指派）**：`thread.active/subscribed` 生成列、独立 `assignment` 表由 Wave-2 决定；`trigger_thread`/`decision.thread`/`outbox.dropped` 已完成。

## 6. 未完成 / 被阻塞

- **无本人 scope 内未完成项**：items 1–6 与 Lead addendum 全部实现并有回归测试；scoped build/test/vet/gofmt/race/cross-compile/nocgo 全绿。
- **一项跨 scope 阻塞（已由 Lead 决策关闭）**：`internal/runtime/concurrency_rev_test.go` 的 `TestSettingsConflictAcrossWindows` 曾因单实例锁变红。Lead 决策保留 flock、由 fix-runtime 改为共享一个 store 句柄；本 scope 的 `internal/store/settings_test.go::TestSaveSettingsRejectsStaleBase` 已覆盖 stale-revision CAS。
- **全仓 `go build ./...` 当前瞬时红**：`internal/runtime/worker.go:327: cannot indirect fresh (domain.Ticket)`——fix-engine 正在改 engine/domain/assess（值/指针形态变化），属其 scope 的在途修改，非本 scope；本人 9 个 scope 包 `go build`/`go test`/`go vet` 均绿。
- 说明：`internal/store/verify_core_test.go` 是 verifier-A 的独立反证测试文件（Wave-3），不在本人改动范围内、未编辑；当前它包含的 8 个 `TestVerify*` 全部通过（含 nested atomic + afterCommit、timeline 同刻不丢、`trigger.at` 跨进程可排序、M4 时代文件升级）。
- `/tmp` 下的死代码分析工具（来自 nih-core 审计）与本 wave 无关，未写入仓库。
