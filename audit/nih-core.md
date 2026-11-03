# ZeoKeel 持久化 / 领域切片：重复造轮子审计（nih-core）

> 审计对象：`internal/{store,engine,domain,effects,outbox,schedule,brief,scan,skills}`
> 基线：commit 工作区 HEAD（`git status` 干净），go1.27.3，`CGO_ENABLED=0`。
> 方法：只读。逐文件精读 + `go doc` + 模块缓存源码核对 + 两个只读工具侧证：
> `go mod tidy -diff`（无输出 ⇒ go.mod/go.sum 已是 tidy 状态）、以及自建的 SSA/RTA 死代码工具（`/tmp/deadtool`，不写入仓库）。
> 输出仅本文件。

---

## 0. 结论摘要

1. **这个切片不需要新增任何第三方依赖**。所有"应该改"的地方，要么用 **stdlib**，要么用**本仓库已有的机制**。唯一可选的第三方候选是 `robfig/cron/v3`（纯 Go、零依赖、已在 module cache），但它是**部分替换**而非 drop-in，本审计**不建议**引入（理由见 §3.3）。
2. **真正值得动手的只有 4 处**（合计约 120 行自建代码可删或缩掉一半以上：`wallTime` 30→约 8；skills 路径防护 64→约 20；`truncateBlock` 10→2；`EnsureV3`+marker 逻辑约 40 行归并入 `migrations[]` 后消失）：
   - `internal/schedule/next.go:60-89` 的 30 行 DST 求解 → `time.Date` + 往返校验（**P1，收益/风险比最高**）；
   - `internal/store/schema_v3.go` 的 v2→v3 分叉迁移 → 并入既有 `migrations[]`（**P1，现在不修，将来折叠时会打不开老库**，见 §3.1.2）；
   - `internal/skills/skills.go:215-285` 的 64 行符号链接/路径包含校验 → `os.OpenRoot`/`os.Root`（**P1，语义更强且无 TOCTOU**）；
   - `internal/skills/render.go:90-99` → `strings.ToValidUTF8`（P2，小）。
3. **绝大多数"看起来像自建"的东西是 JUSTIFIED（有理由的自建）**：durable Effects/Outbox、gate + admission + accept mode、`domain.StageOf` 阶段推导、内容派生 ID、SKILL.md frontmatter、brief 摘要启发式、`scan` 的 git/语言探测。依据是设计文档 §0.3/§7.2 与原型 001/003 的显式承诺，以及**这些机制要么依赖 SQLite 事务语义，要么依赖中文/领域词汇，成熟库无法替代表达**。
4. 顺手挖出 2 个**真缺陷**（不是"自建"问题，是自建代码的质量问题）：
   - `cron.go` 的 `N/step` 语义与 cronie/robfig 不一致：写 `0/6 * * * *` 实际只在**小时 0** 触发（§3.3.2）；
   - `engine.Receive` 全程持 `e.mu`，且 `routeLocked` 在锁内调用**真实模型**（`assess.Assessor`），一次 Eval 期间全局状态机被锁住（§3.4.2）。
5. 死代码成簇：`brief.Threshold/OneLine/firstLine`（"工作区多于 10 个就退回单行索引"这个功能**只写了常量没接线**）、`domain.ParseThreadKey`、`store.OpenTickets/Outbox/Health`、`engine.ErrNoWorkspace`、`outbox.SenderFunc`、`schedule.Schedule.Spec/Location`、`domain.Disposition.Thread` 字段（§6）。

**verdict 计数**：SHOULD-USE-FRAMEWORK 4 · JUSTIFIED 10 · LOW-PRIORITY 5 · DEAD 1（共 20 项）。

> 术语约定：本项目里的 `SHOULD-USE-FRAMEWORK` 一律指"**应改用 stdlib / 已有依赖 / 仓库内既有实现**"三者之一；本切片**没有**任何一处值得为此新增第三方框架依赖。给出的替代库路径均来自本机 `$(go env GOMODCACHE)`，并标注了 symbol 与"纯 Go"证据。

---

## 1. 范围与覆盖

| 目录 | 非测试 LOC | 测试 LOC | 本审计读过的文件 |
| --- | ---: | ---: | --- |
| `internal/store` | 1180 | 536 | `store.go`(497) `schema.go`(151) `schema_v3.go`(134) `load.go`(303) `settings.go`(95) 全读；`schema_v3_test.go` 扫读 |
| `internal/engine` | 1542 | 944 | `engine.go`(935) `persist.go`(427) `router.go`(180) 全读 |
| `internal/domain` | 573 | 82 | `ids.go`(71) `ticket.go`(320) `trigger.go`(108) `workspace.go`(74) 全读 |
| `internal/effects` | 649 | 679 | `effects.go`(649) 全读 |
| `internal/outbox` | 415 | 498 | `outbox.go`(415) 全读 |
| `internal/schedule` | 928 | 355 | `cron.go`(190) `next.go`(90) `spec.go`(173) `store.go`(475) 全读；`schedule_test.go` 扫读 |
| `internal/brief` | 364 | 167 | `brief.go`(193) `digest.go`(171) 全读 |
| `internal/scan` | 237 | 116 | `scan.go`(237) 全读 |
| `internal/skills` | 847 | 726 | `skills.go`(317) `parse.go`(209) `match.go`(222) `render.go`(99) 全读 |
| **合计** | **6735** | **4103** | 9 个包的非测试代码 **100% 读过** |

侧证工具（只读，产物在 `/tmp`）：
- `golang.org/x/tools@v0.52.0` 的 `go/packages` + `go/ssa` + `go/callgraph/rta`，root = 所有 `main` 包，覆盖**函数与方法**；共 210 个"从 main 不可达"的符号，其中本切片 10 个（§6）。**局限**：RTA 对接口动态派发保守，因此本报告的"方法级死代码"都以 `grep` 复核过（`store.OpenTickets` 等不在 RTA 输出里，但非测试引用数确实为 0）。
- `go mod tidy -diff`：无 diff ⇒ 依赖清单无冗余/缺失。

未深挖（时间盒/超出切片）：`_ref` TS 原型的逐行对照（只在需要证明"原型就是这么设计的"时引用设计文档结论）、`internal/assess`（只在 §3.4.2 作为 engine 的调用方引用）、UI 层。

---

## 2. 汇总表

| # | 区域 | 现状实现 | LOC | verdict | 推荐替代 | effort | risk | 优先级 |
| --- | --- | --- | ---: | --- | --- | --- | --- | --- |
| 1 | store 迁移执行器 | `migrations []string` + 下标即版本 + kv 存版本 + 拒绝更新库（`store.go:126-183`） | 58 | LOW-PRIORITY | 保留；goose/golang-migrate 不值得 | S(若换) | 低 | P2 |
| 2 | store v2→v3 分叉迁移 | `EnsureV3` 由 owning package 调、marker 存 `kv.schema.schedule_goal`（`schema_v3.go:92-134`） | 134 | **SHOULD-USE-FRAMEWORK** | 既有 `migrations[]` + 提升 `SchemaVersion`（非新框架） | S | 中高 | **P1** |
| 3 | store 事务/afterCommit/单写者 | 手写 `guard`+`txMu`+`depth`+`afterTx`（`store.go:191-293`） | ~110 | JUSTIFIED | 保留（SQL 侧无 stdlib 等价物） | — | 低 | P0 保留 |
| 4 | store 事务底层 `BEGIN IMMEDIATE` | 在 `SetMaxOpenConns(1)` 的**池连接**上裸发 BEGIN/SAVEPOINT（`store.go:54,200,207,229`） | 同 #3 | LOW-PRIORITY | `database/sql` 的 `Conn`/`Tx`（`BeginTx`） | M | 中 | **P1** |
| 5 | store JSON-in-TEXT 聚合列 | `workspace/ticket/decision/trigger` 的 `json TEXT` + `json.Marshal`（`store.go:296-463`） | — | JUSTIFIED | 保留；sqlc/ent 无收益 | — | 低 | — |
| 6 | effects durable 队列 | 行即队列、`seq` 保序、`generation` 防旧、`Later`、3 次失败、health（`effects.go` 全） | 649 | JUSTIFIED | 保留；`cenkalti/backoff` 不适用（状态在 SQL） | — | 低 | — |
| 7 | outbox 幂等回执 | 先落库再发、业务 ID 幂等、`behind()` 同会话保序、`gone`（`outbox.go` 全） | 415 | JUSTIFIED | 保留；无成熟纯 Go SQLite job queue | — | 低 | — |
| 8 | schedule 5 字段 cron 解析 | `cronExpr` 布尔表 + `parseCronField`（`cron.go:26-127`） | ~125 | LOW-PRIORITY | 可换 `robfig/cron/v3` 的 **parser**；但有语义差异；先修 `N/step` | S | 中 | **P1**（修 bug） |
| 9 | schedule DST 墙钟求解 | `wallTime` 17 次时区探测 + map（`next.go:60-89`） | 30 | **SHOULD-USE-FRAMEWORK** | stdlib `time.Date` + 往返校验 | S | 低 | **P1** |
| 10 | schedule 领域层 | `Spec/Kind/Compile/nextDaily/nextWeekly/Label` + `ClaimDue/Complete/Recover`（`spec.go`,`store.go`） | ~800 | JUSTIFIED | 保留（at/every/daily/weekly + 悬崖策略 + 重叠守卫是领域语义） | — | 低 | — |
| 11 | engine gate/admission/routing | `gateLocked` + `Policy` + accept mode + `RuleAssessor`（`engine.go:339-369`,`router.go`） | ~300 | JUSTIFIED | 保留；OPA/CEL/expr 会**恶化**可审计性（见 §3.4） | — | 低 | — |
| 12 | engine 策略校验 / 锁内模型调用 | `json.Unmarshal` 无 schema 校验；`e.mu` 覆盖 `Assess`（`persist.go:124`,`engine.go:153`+`router.go:95`） | — | LOW-PRIORITY | 闭集校验函数（无需库）；锁外评估 | M | 中 | **P1**（仅风险登记） |
| 13 | domain 内容派生 ID | `sha256`+`hex`+`fmt.Fprint`（`ids.go:39-47`） | 10 | JUSTIFIED | 保留（原型硬要求，设计 §4） | — | 低 | — |
| 14 | domain StageOf 阶段表 | `StageOf` 首个匹配获胜（`ticket.go:258-283`） | 26 | JUSTIFIED | 保留（003 Execution 的真值表） | — | 低 | — |
| 15 | skills frontmatter + 匹配 | 手写扁平 `key: value` + 词边界/汉字段打分（`parse.go:107-148`,`match.go:102-123`） | ~250 | JUSTIFIED | 保留（yaml.v3 语义陷阱 + 中文分词无依赖方案） | — | 低 | — |
| 16 | skills 路径包含/符号链接防护 | `Lstat`+`EvalSymlinks`+`filepath.Rel`（`skills.go:215-285`） | 64 | **SHOULD-USE-FRAMEWORK** | stdlib `os.OpenRoot`/`os.Root`（Go 1.24+） | M | 中 | **P1** |
| 17 | skills 注入预算截断 | 逐字节回退判 `utf8.ValidString`（`render.go:90-99`） | 10 | **SHOULD-USE-FRAMEWORK** | `strings.ToValidUTF8` | S | 低 | P2 |
| 18 | brief / scan 启发式 | `humanAge`、14 天摘要、`.git/config` 解析、语言/命令探测（`digest.go:161-171`,`scan.go:51-193`） | ~250 | JUSTIFIED | 保留（humanize/go-enry/go-git 都不划算，见 §3.7） | — | 低 | P2 |
| 19 | 跨包重复工具 | 见 §5（11 组） | ~200 | LOW-PRIORITY | 收敛到 1~2 个内部 helper | M | 低 | P2 |
| 20 | 死代码簇 | 见 §6 | ~200 | **DEAD** | 删除 / 接线 | S | 低 | P2 |

---

## 3. 详细发现

### 3.1 Store（问题 1）

#### 3.1.1 手写迁移执行器：**LOW-PRIORITY（保留）**

**现状**：`store.go:126-156` 用 `sqlite_master` 探测 `kv` 表决定"新库/老库"，`kv['schema']` 存版本；`applyMigrations`（`store.go:161-183`）在一个 `BEGIN IMMEDIATE` 里按 `for v := from; v < SchemaVersion; v++ { Exec(migrations[v]) }` 顺序执行，末尾一次性写版本号。`SchemaVersion = 2`（`schema.go:7`）。

**为什么不该引入 goose/golang-migrate**：
- 只有 **2 条**迁移，且 v1/v2 已冻结（CHANGELOG 已到 1.0.0-rc.1）；框架的核心价值（down、多方言、embed 目录、advisory lock）这里全部用不上。
- 框架会引入自己的版本表与连接管理，而本项目的硬不变量是"**单连接 + 单写者 + 迁移和业务共用同一把 `guard`**"（`store.go:32-37,47-54`）。任何迁移框架都会引入**自己的版本表与连接/事务模型**，与本项目 `SetMaxOpenConns(1)` + 共享 `guard` 的假设叠加时，必须先证明不会死锁——这是引入框架的真实成本（inference：`pressly/goose`、`golang-migrate` 都不在本机 module cache，本次未能核对源码）。**收益 < 风险**。
- 判据（框架能带来而这里没有的）：① 逐条迁移的版本记录（现在是一次性写终值，无法知道"第 2 条是否执行过"）；② checksum 防漂移（改一条已应用的迁移不会报错）；③ 迁移与代码同 PR 的目录约定。三点都不构成本项目的实际痛点，因为迁移串只在 `schema.go` 一个文件里、且无分支开发。

**唯一值得吸收的框架习惯**（effort S，P2）：把 `migrations []string` 换成 `[]migration{version int, name, sql string}`，并在应用后写 `kv['schema.M<N>']`，这样 v3 折叠（下条）就有地方落。

#### 3.1.2 v2→v3 的 `EnsureV3` 分叉：**SHOULD-USE-FRAMEWORK（P1，本切片最该修的一处）**

**现状**：`schema_v3.go:10-20` 的注释自己承认这是权宜之计：

> "Why this is not another entry in migrations: the frozen v1/v2 layout and the version tests that pin it belong to the durable core … So the v3 tables are applied by EnsureV3, called by the packages that own them (internal/schedule, internal/goal), and marked in kv under `schema.schedule_goal` rather than moving the global schema stamp. A later M5 migration can fold this into the ordered list after the version tests move."

于是同一个数据库里有**两套版本真相**：`kv['schema']='2'`（`store.go:174`）和 `kv['schema.schedule_goal']='3'`（`schema_v3.go:124-128`）。`EnsureV3` 由 `schedule.New`（`internal/schedule/store.go:82`）和 `goal.New`（`internal/goal/goal.go:127`）在运行时各调用一次，靠 `CREATE TABLE IF NOT EXISTS`（`schema_v3.go:32-85`）和"错误字符串里含 duplicate column"（`schema_v3.go:117-123`）保证幂等。

**为什么是问题**（不是"不够优雅"，是**将来的必炸点**）：
- v1/v2 的迁移串用**裸 `CREATE TABLE`**（`schema.go:14,25,38,45,54,64,71,83,94,103,110`），没有 `IF NOT EXISTS`；v3 语句则**全都带 `IF NOT EXISTS`**（`schema_v3.go:35,49,54,63,68,83-84`）——正是为了掩盖"被调用多次"。
- 注释里计划的"M5 把 v3 折进有序列表"一旦执行（`migrations[2] = 上面的 CREATE TABLE …`），对**任何现存的 v3 数据库**（它们 `kv['schema']='2'`，但表已存在）会执行 `CREATE TABLE schedule(...)` → `table schedule already exists` → `applyMigrations` 回滚并返回错误 → **该库从此打不开**（`store.go:168-172`）。这不是理论风险：凡是跑过 schedule/goal 的库都是这个状态。
- 反过来，`migrate()` 的"拒绝更新库"只比较 `kv['schema']`（`store.go:152-154`），**看不见 v3 marker**：一个 v3 库被"只支持 v2"的老构建打开时不会被拒绝（只是忽略两张表）。目前无害，但说明版本模型已经不能表达真实布局。
- `strings.Contains(err.Error(), "duplicate column")`（`schema_v3.go:117-123`）是字符串化的迁移判定，是 SQLite 生态里典型的"框架帮你做掉的事"——不必换框架，但至少应改为查 `PRAGMA table_info(schedule)`。

**推荐做法**（用仓库内既有机制，不引入库）：
1. `SchemaVersion = 3`；`migrations[2]` 用**全 `IF NOT EXISTS`** 的语句（直接把 `schemaV3Statements` 搬过去），并在末尾把 `ALTER TABLE schedule ADD COLUMN task` 换成"查 `PRAGMA table_info` 再决定"（或保留 duplicate-column 容忍但只在 `migrations[2]` 内一次性执行）。
2. 删除 `EnsureV3` 与两个调用点（`schedule/store.go:82`、`goal/goal.go:127`）；删除 `kv['schema.schedule_goal']`（或写一个一次性 `DELETE FROM kv WHERE k='schema.schedule_goal'` 保持卫生）。
3. 更新 `schema_v3_test.go`（它现在断言 `EnsureV3` 的幂等性）为"v2 库 → Open → 版本=3 且两张表存在"的升级测试；`store_test.go`/`load_test.go` 里钉版本号的断言一并改。

**风险/不变量**：`applyMigrations` 仍在 `Open` 的 `guard` 内单连接执行，与单写者不变量一致；升级是一次性幂等操作，可在旧库副本上先跑。**effort S、风险中（仅测试改写）、收益高**。这也是"用既有机制"而非"引入迁移框架"的典型：真正缺的是**单一版本真相**，不是 goose。

#### 3.1.3 `Atomic`/SAVEPOINT/afterCommit：**JUSTIFIED（P0 保留）**

- `AfterCommit`（`store.go:279-293`）+ "事务内 `Enqueue`、提交后才执行"（`effects.go:192-226`）是 design §7.2 的核心语义，stdlib 没有对应物（`database/sql` 也没有 after-commit hook）。
- 单写者 guard（`store.go:29-37,101-105`）把"命令事务"和"后台 worker 的簿记"分开，注释里 why 很清楚（`store.go:92-100`），这是**领域不变量**，不是 reinvention。
- **但嵌套 SAVEPOINT 是"支持了却没人用"**：全仓库 `.Atomic(` 的 8 个调用点（`schedule/store.go:233,331,356`、`outbox/outbox.go:156,235,331`、`store/settings.go:72`、`engine/persist.go:318`）**没有任何一层嵌套**；嵌套只出现在 `store_test.go:140,160`。CHANGELOG 把"事务 + 嵌套 SAVEPOINT + afterCommit"列为可靠性特性，但生产路径并不需要它 → 建议要么补一个真实用例，要么降级为"仅测试覆盖的能力"并在注释里说清（P2，减少 ~40 行状态管理）。

#### 3.1.4 裸 `BEGIN IMMEDIATE` 在池连接上：**LOW-PRIORITY / P1 硬化项**

**现状**：`db.SetMaxOpenConns(1)`（`store.go:54`）后，`Atomic` 用 `s.db.Exec("BEGIN IMMEDIATE")`（`store.go:200`）在**池里的那唯一连接**上开事务，随后所有语句都走 `s.db.Exec/Query`（`store.go:77-90`，纯透传）。注释要求"命令 goroutine 专用"（`store.go:73-76,98-100`）。

**为什么这是 stdlib 已经解决得更好的事**：`database/sql` 的 `BeginTx` 会**独占**一条连接并返回 `*sql.Tx`，其他 goroutine 的 `db.Query` 只会**等待**（而不是被吸进别人的事务）。当前设计依赖"没人从别的 goroutine 读"这条口头约定，而仓库里存在并发读路径：
- `internal/runtime/pages.go:342` 在面板渲染路径上 `st.Settings()` → `store.Query`（`settings.go:24`）；
- `schedule` 的写路径 `Put/SetEnabled/Delete` 走 `m.st.Exec` 而**没有** `Guarded`/`Atomic`（`schedule/store.go:123,199,212`），对比 `effects`/`outbox` 的后台写都老老实实包了 `Guarded`（`effects.go:445,555,596,628`；`outbox.go:266,305`）——说明作者已经意识到该风险，只是 `schedule` 没跟上（inference：`Put/SetEnabled/Delete` 的调用方是 schedule 工具与面板，两者都可能在命令事务打开时进入）。
- 一旦与命令事务撞上，后果不是报错而是**读到未提交数据 / 写进将被回滚的事务**（静默错误）。

**推荐**（不必换掉整个 `Atomic` 设计）：把 `Store` 从"一条共享连接 + 裸 SQL"改成"`Atomic` 里持有一个 `*sql.Conn`（`db.Conn(ctx)`），把该 conn 存进 struct 供 `Exec/Query` 使用"，或直接 `BeginTx` + 在 `Atomic` 内把 `*sql.Tx` 暴露给 `Exec/Query`。这样"单写者 + 嵌套 SAVEPOINT + afterCommit"全部保留，但隔离性由 stdlib 保证。**effort M（穿透面 ~20 个调用点）、风险中、收益：消除一类静默数据错误**。

#### 3.1.5 JSON-in-TEXT 列 + sqlc/ent：**JUSTIFIED（不推荐 codegen）**

聚合 JSON 列（`schema.go:20,33,50,60,68,79`）对应的是"整体读写、从不用 SQL 过滤其内部字段"的聚合（`store.go:296-463`）。`ticket.stage` 只是查询缓存且注释明确"never the truth"（`schema.go:57`、`store.go:404-407`），`OpenTickets` 用部分索引只扫活跃行（`schema.go:62`）——这已经是"用 SQL 做 SQL 该做的事"。

sqlc 只生成 `:one/:many/:exec` 的样板，省不到 JSON 编解码；ent 会强加自己的 schema DSL 与代码生成，与"内容派生 ID + 聚合整体写"冲突，且引入 cgo-free 但很重的依赖树。**判据**：本切片的瓶颈从来不是 SQL 样板（`store.go` 里 47 行是纯 CRUD，剩下是设计），而是事务语义——那属于 §3.1.3/§3.1.4。

#### 3.1.6 KV settings 的乐观并发：**JUSTIFIED**

`store/settings.go:72-110` 把 revision 读/比/写放进同一事务，解决"两个窗口互相覆盖"（design §6.5）。这是领域级 CAS，没有 stdlib 替代；实现只有 ~40 行且注释把 why 写清了。

---

### 3.2 Effects + Outbox（问题 2）

#### 3.2.1 通用 vs 领域拆解

| 机制 | 位置 | 判定 |
| --- | --- | --- |
| 行即队列 / 崩溃恢复 | `effects.go:6-8,147` | **领域必需**（`SELECT MAX(seq)` 恢复队列位置） |
| `seq` 保序 + `Ordered` 队头阻塞 | `effects.go:450-462` | 领域（"顺序即语义"） |
| `generation` 防旧任务覆盖 | `effects.go:212-218,521-522` | 领域（经典 outbox 代际） |
| 同 `kind+key` 覆盖 + 保留 `seq` | `effects.go:206-218` | 领域（"最新世界状态"语义） |
| `Later` = 不算失败 | `effects.go:37-55,533-538` | **领域**（暂停/槽位满 → 不计失败） |
| 连续 3 次失败才 error | `effects.go:27,543-546,615` | 领域阈值（`Quiet` 例外给 delivery） |
| 指数退避计算 | `effects.go:558-570` | **通用，但只有 13 行**（`[initial,max]` 两参数 + 1<<n 封顶） |
| 并发上限 | `effects.go:410-419`（`Limit func() int`） | 通用 semaphore，但需要"0 = 暂停且不失败"的领域语义 |
| health 条目写入 | `effects.go:591-640` | 领域（面板投影） |
| 幂等回执 / `behind()` 同会话保序 | `outbox.go:147-192,300-320` | **领域（outbox 模式的核心）** |
| `gone` 永久不可达 vs 临时错误 | `outbox.go:38-52,288-292` | 领域 |

#### 3.2.2 为什么 `cenkalti/backoff` / `x/time/rate` / 纯 Go job queue 都不该引入

- `golang.org/x/time/rate` 确实已在 go.mod（`// indirect`，`x/time@v0.15.0` 在 module cache），但**本切片没有任何速率限制需求**：`effects.Options.Limit`（`effects.go:66-68`）是**并发上限**不是 QPS；outbox 的"限流"也是 `WithLimit`（`outbox.go:111-115`）。`rate.Limiter` 是内存态，跨重启失效，与"durable 队列"定位相反。**不需要**。
- `github.com/cenkalti/backoff`（v2.2.1 在 cache，不在 go.mod）：它能省掉 `backoff()` 那 13 行（`effects.go:558-570`），但 backoff 的状态在 **SQL 的 `attempts`/`due` 列**里（`schema.go:71-81`），库是内存态 `BackOff` 对象——用它反而要写"从 DB 重建 BackOff"的胶水。**不划算**（13 行 vs 一层适配）。
- 纯 Go SQLite job queue：主流方案（如 `river`）绑定 Postgres（inference：不在依赖图/缓存中，本次未核对源码）；SQLite 侧没有一个既 durable、又提供 `Later`/代际/按 kind 的成熟纯 Go 库。**没有替代品**。

**结论**：`effects`/`outbox` 是**两个包里唯一一处"自建但正确"的大型基础设施**，判 JUSTIFIED。可挑的刺只有重复代码（§5 D1/D10）与时间格式混用（§3.2.3）。

#### 3.2.3 时间格式在 outbox 里混用（LOW-PRIORITY，P2）

`store.FormatTime` 的存在理由写得很清楚：定宽 UTC 才能让 `due <= ?` 在 SQL 里比较（`store.go:115-124`）。但 `outbox` 用的是 `time.RFC3339Nano`：
- 写：`outbox.go:171-172`（`Send`，`created_at`+`updated_at`）
- 写：`outbox.go:350-351`（`settle` 重建回执）
- 写：`store/load.go:284`（`SettleOutbox`，`updated_at` 用 **FormatTime**）
- 读：`outbox.go:388-389`、`store/load.go:272-273`

同一个 `outbox.updated_at` 列被两种格式写入。当前没有 SQL 比较该列（排序用 `rowid`，`outbox.go:214,257`），所以**不是活 bug**，但正好踩在 `store.go:116-118` 警告的坑上：将来任何"按 updated_at 过滤"的查询都会得到字典序 ≠ 时间序的静默错误。建议：outbox 的 4 处写/读统一改为 `store.FormatTime/ParseTime`。

---

### 3.3 Schedule（问题 3）

#### 3.3.1 `wallTime` 的 30 行 DST 求解：**SHOULD-USE-FRAMEWORK（stdlib，P1）**

**现状**：`next.go:60-89`。为了"不存在的墙钟时间跳过、重复的取较早瞬间"，它构造 17 个 6 小时探针（`for k := -8; k <= 8`），收集时区偏移到 `map[int]struct{}`，再用每个偏移反推候选并往返校验。每次 `Next()` 调用都会分配一个 map 并做 17 次 `In(loc).Zone()`。

**stdlib 已经提供了什么**（本机 go1.27.3 实测，`/tmp/dst/main.go`）：

| 场景 | `time.Date(y,m,d,h,mi,0,0,loc)` 实测结果 | 往返校验 |
| --- | --- | --- |
| `America/New_York` 2026-11-01 01:30（fall-back 重复） | `01:30 -04:00`（**较早**那一次） | 通过 |
| `America/New_York` 2026-03-08 02:30（spring-forward 空洞） | `01:30 -05:00`（小时被归一化掉） | **失败 → 判定不存在** ✓ |
| `America/Sao_Paulo` 2018-11-04 00:00（午夜空洞） | `2018-11-03T23:00-03:00` | **失败** ✓ |
| `Australia/Lord_Howe` 2026-10-04 02:15（30 分钟 DST） | `02:45 +11:00` | **失败** ✓ |

即：**"不存在就跳过"这条规则可以用"`time.Date` 往返后墙钟是否等于请求值"精确判定**，不需要任何探针；`go doc time.Date` 也确认归一化是文档承诺的行为（"values may be outside their usual ranges and will be normalized"）。剩下的只有"重复时取较早"——文档明说"does not guarantee which"（`go doc time.Date`: "the choice of time zone … is not well-defined … does not guarantee which"），所以严格来说这一条需要一行显式 tie-break，而不是 17 次探测。

**迁移草图**（`next.go:60-89` → 约 8 行）：
```go
func wallTime(y int, mo time.Month, d, h, mi, sec int, loc *time.Location) (time.Time, bool) {
    t := time.Date(y, mo, d, h, mi, sec, 0, loc)
    // 空洞：Date 把不存在的墙钟归一化到别的时刻
    if t.Year() != y || t.Month() != mo || t.Day() != d ||
        t.Hour() != h || t.Minute() != mi || t.Second() != sec {
        return time.Time{}, false
    }
    // 重复：取较早的瞬间（文档不保证 Date 选哪个，显式 Tie-break）
    if t.IsDST() {                       // go1.17+ `time.Time.IsDST`
        if alt := t.Add(-time.Hour); alt.IsDST() == false && sameWall(alt, t) { /* 已在较早侧 */ }
    }
    return t, true
}
```
（`sameWall` 是 3 行墙钟比较；对 30/45 分钟制时区用 `t.Add(-30*time.Minute)` 探测已足够，也可以保留"取 `t` 与 `t.Add(-dstDelta)` 中较早且墙钟相同者"两行写法。）

**为什么值得做**：现有实现是**测试覆盖最好的一段自建代码**（`schedule_test.go:117` 春季空洞、`:140` 秋季重复，且 `schedule_test.go:8` 用 `_ "time/tzdata"` 内嵌时区数据——这个习惯很好），所以替换的风险极低（测试会立刻抓到回归），而收益是删掉 30 行"自研时区数学"、去掉每次 `Next()` 的一次 map 分配。**effort S，风险低，P1。**

**不变量**：`Schedule.Next` 的语义（"严格晚于 after、按本地墙钟、不存在则跳过、重复取较早"）必须保持不变；`Spec` 仍然**持久化墙钟字段**而不是预计算瞬间（`spec.go:25-27`），这条不能动——它是 DST 可重算的前提。

#### 3.3.2 手写 cron 解析：**LOW-PRIORITY（部分可换 robfig）+ 一个真 bug（P1）**

**① 与 robfig 的精确差异**（`robfig/cron/v3@v3.0.1`，module cache：`/root/go/pkg/mod/github.com/robfig/cron/v3@v3.0.1/`；`go.mod` 只有 `module`+`go 1.12`、源码无 `import "C"` ⇒ **纯 Go、零依赖**，满足项目硬约束）：

| 能力 | 本项目（`cron.go`） | robfig v3 | 影响 |
| --- | --- | --- | --- |
| 5 字段 + 名字（`MON-FRI`/`JAN`） | ✓ `cron.go:55-62,129-141` | ✓ `parser.go:36-58` | 等价 |
| DOM/DOW 组合规则 | 两字段都"受限"时取 **OR**，`*` 开头即使带 step 也算不受限（`cron.go:23-25,66-72,174-190`） | `starBit` 机制（`spec.go:184-188`），但 **`step>1` 会清掉 starBit**（`parser.go:297-299`） | **语义不同**：`0 0 */2 * 1` 本项目 = 与周一取 **AND**，robfig = 取 **OR** |
| `N/step` | 只取单值 N（`cron.go:105-111`：`start, end = v, v`） | 按 **N-max/step**（`parser.go:288-299`，注释 "Special handling: N/step means N-max/step"） | **本项目偏离 cronie** → 见 ② |
| `@daily`/`@every` 描述符 | ✗ | ✓（`parser.go:26,107-110,364+`；`@every` 返回 `ConstantDelaySchedule`，与 `SpecSchedule` 不同类型） | 本项目不用这些字符串（有结构化 `Kind`），无影响 |
| `CRON_TZ=` 前缀 | ✗（用 `Spec.Location` 字段 `spec.go:36-38`） | ✓（`doc.go:24,147`） | 本项目方案更适合持久化，保留 |
| 秒字段 / `?` | ✗ | ✓（可配 parser；`?`=star，`parser.go:264-265`） | 无需求 |
| 墙钟跳过/重复规则 | **显式且文档化**（`spec.go:113-115`,`next.go:52-89`） | 算法"逐字段加一"，DST 只有"午夜不存在"的特判（`spec.go:114-130`），无"跳过/取较早"的承诺 | robfig **更弱** |
| `Next` 返回值 | `(time.Time, bool)` | 零值 `time.Time` 表示无解（`spec.go:56-57` 注释） | 需适配 |
| 重叠保护 | durable（`running_fire` 列，`store.go:230-322`） | 进程内 `SkipIfStillRunning`（`chain.go:78`） | robfig 帮不上忙 |

**所以**：robfig **不能**整包替换 `internal/schedule`（`at/every/daily/weekly` 的结构化 Spec、Label 中文渲染、DST 墙钟规则、durable 重叠守卫都不在它范围内）。它最多替换 `cron.go:26-127` 的 **parser**（约 125 行）：用 `cron.NewParser(cron.Minute|cron.Hour|cron.Dom|cron.Month|cron.Dow).Parse` 得到 `*cron.SpecSchedule` 的位掩码（`spec.go:7-11`：`Second,Minute,Hour,Dom,Month,Dow` 与 `Location` 全是导出字段），再把现有的 `dayMatches`/`next` 改成位运算。**但**要额外处理 `starBit` 差异（上表第 3 行）才能保持现有语义。**effort S、收益中（换掉 125 行自研解析器）、代价：新增一个依赖 + 一处语义对齐。净收益不明显 ⇒ 判 LOW-PRIORITY，不建议在本轮做。**

**② 真 bug（P1，与是否换库无关）**：`cron.go:87-111`——当字段是"单值 + step"（如 `0/6`），`start = end = v`，循环只跑一次。即面板里写 `0/6 * * * *`（"每 6 小时"）会**只在 00:00 触发**；`5/10 * * * *` 只在第 5 分钟触发。cronie 与 robfig 都把 `N/step` 解释为 `N-max/step`。用户不会收到任何报错，只会发现"定时任务没按预期跑"。修法二选一：
- 最小改动：`if step > 1 && start == end { end = hi }`（与 cronie 一致，`cron.go:111` 之前加 3 行）；
- 或者：在 `parseCronField` 里**拒绝**单值+step，让 `Compile`（`spec.go:85-90`）报错（"猜时间比问一句更贵"——正好符合 README 的立场）。
现有测试（`schedule_test.go:150-183`）覆盖了 `*/15`、`MON-FRI`、DOM/DOW OR、DST，**但没有任何 `N/step` 用例**，所以这个缺陷一直没暴露。

**③ 手写 cron 的风险量化**：解析器 190 行里真正需要"正确性论证"的只有 `parseCronField`（62 行）+ `dayMatches`（17 行），已有 8 条非法输入 + 4 条语义测试（`schedule_test.go:36-70,150-183`），**风险等级低**。相对地，`next()`（`cron.go:145-172`）是逐日逐时逐分扫描，最坏 `366*5*1440 ≈ 2.6M` 次迭代（对 `0 0 31 2 *` 这种永不触发的表达式）——一次 `Next()` 毫秒级，只在构造/展示"下次触发"时调用，可接受；**不建议**为此换库。

#### 3.3.3 Schedule 其余部分是 JUSTIFIED

- `Spec`/`Compile`/`Label`（`spec.go:28-95,141-159`）：`at/every/daily/weekly` 是**面板要展示给人看**的四种形状，`Label()` 输出中文（`spec.go:141-159`），没有任何库能表达"每 2 小时"这种领域描述。
- `ClaimDue/Complete/Recover`（`store.go:230-388`）：一次调用每 schedule 最多补一次（catch-up 悬崖）、`running_fire` 重叠守卫、`schedule_fire` 幂等 id（`fireID` `store.go:419-425` 由 `domain.Key` 派生）、崩溃后 `Recover` 把 `fired` 改 `interrupted`（`store.go:354-388`）——**全是 design §7.2 的领域语义**，没有库对应物。
- 小瑕疵（P2）：`formatTime` 是 `store.FormatTime` 的 1 行别名（`store.go:461`）；`boolInt`（`store.go:470-475`）是多余的——`modernc.org/sqlite` 直接把 Go `bool` 绑定成 1/0（`/root/go/pkg/mod/modernc.org/sqlite@v1.61.1/conn.go:580-587`：`case bool: v:=0; if x {v=1}; c.bindInt(...)`）。

---

### 3.4 Engine（问题 4）

#### 3.4.1 gate + admission table + accept mode + routing：**JUSTIFIED（不要换成 OPA/CEL/expr）**

**现状**：`Policy`（`engine.go:25-39`）= `Allowed map[source]map[actor]bool` + `AdmitsUnaddressed` + `ReplyMode` + `AcceptMode`；`gateLocked`（`engine.go:339-369`）是 4 条早返回的确定性判定；accept mode 降级（`engine.go:216-235`）与"同事请求 → 待批单"（`engine.go:240-244`）是**策略的默认安全方向**；`routeLocked`（`router.go:52-124`）= 关键词召回（`brief.Index.Recall`）→ 模型判断。

**为什么不是规则引擎该做的事**：
- 判据（什么该用 CEL/OPA）：规则**由用户以表达式/策略文件编写**、数量大、需要热更新与独立测试。这里 `Policy` 是**面板写死的四个字段**（`engine.go:25-39`），规则数量固定为 4 条，每条都产出一个 `domain.Reason{Code,Values}`（`ticket.go:20-24`）用于本地化与审计——用 CEL 表达只会把"为什么被拒"从编译期类型检查变成运行时字符串，**可审计性变差**。
- 更关键：`engine.go:216-244` 的 accept/propose 降级不是"规则求值"，而是**状态迁移**（会改 `dec.Kind`、清 `dec.Mandate`、写 `dec.Proposal`）。CEL 只返回值，不擅长这个。
- 唯一的"规则语言"入口是团队自然语言规则（`assess.go:120-124` 的 `RulesNote` 拼进**系统提示**）——那是提示词，明确不是配置项（design §1.1"设置要少"）。

**schema 校验**：本切片**没有**手写 JSON schema 校验。JSON 面只有三处，都处理得对：
- `Policy` 反序列化无校验（`persist.go:122-128`），但未知 `AcceptMode`/`ReplyMode` 落入**安全默认**（observe / notify，`engine.go:219-221,627-631`）；
- `schedule.Spec` 反序列化后立刻 `Compile` 校验（`store.go:98-101,259-262`）；
- SKILL.md frontmatter 有完整字段校验（`parse.go:34-91`）。
`github.com/eino-contrib/jsonschema@v1.0.3` 已在 go.mod，但它是**给 eino 工具参数生成 schema**用的（`internal/mcp/tool.go:12,47`），拿它去校验一个强类型 Go struct 是反向的（struct → schema → 校验 struct）。**结论：现状正确，无需引入 jsonschema。**

**重复/冲突处理**：`Receive` 以内容派生 ID 去重（`engine.go:156-158`，`ErrDuplicate` `engine.go:19-21`）；`Present` 以派生 artifact ID 去重（`engine.go:813-824`）；skills 重名直接报错（`skills.go:119-126`）。**没有冲突合并语义，这是刻意的**（"宁可失败也不猜"）。

#### 3.4.2 两个风险登记（**LOW-PRIORITY，但 P1 让 Lead 知道**）

**(a) 全局锁覆盖模型调用**：`Engine.Receive` 进入即 `e.mu.Lock()`（`engine.go:153-154`），第 3 步 `routeLocked`（`engine.go:182`）→ `e.assessor.Assess`（`router.go:95`）→ 生产实现在 `internal/assess/assess.go:112-129` 里做**一次真实模型调用**（`a.gen.Generate`），且 engine 生产构造点就是 `cmd/zeokeel/main.go:131`（`engine.New(newAssessor(...))`）。后果：一次 Eval 的模型往返期间（`assess.Config.Timeout` 量级），**整个状态机不可读**——面板 `AllTickets/Feed/Counts`（`engine.go:452,778,464`）、`Reply`、`Present` 全部阻塞。这不是本次审计的"自建"问题，但它意味着**任何把评估逻辑挪出锁内的重构都有价值**，也说明"规则引擎"若不解决这一点就只是换个写法。建议（若 M5 有余量）：把 assessor 调用移到锁外（先取候选快照 → 释放锁 → 评估 → 重新加锁 + 版本校验）。

**(b) 模型输出校验**：`assess.validate`（`assess.go:144+`）对 `accept`/`ask` 做了必要字段校验，这部分是**手写但正确**的（拒绝"看起来对"的判断），不在本切片。

#### 3.4.3 `persist.go` 的写后重载：**JUSTIFIED**

`Load()`（`persist.go:69-177`）从 store 重建全部内存态；`commit()` 失败即 `resync()`（`persist.go:317-328`）保证"磁盘拒绝的命令不会在面板里显示为已应用"。这是单写者架构的**补偿事务**，stdlib 无替代。唯一的重复是内存重建与 `store/load.go` 的行映射耦合（§5 D1）。

---

### 3.5 Domain（问题 5）

- **`domain.Key`（`ids.go:39-47`）是 JUSTIFIED**：内容派生 ID 是原型最有价值的资产之一（design §0.3："内容派生的幂等 ID … 任何一条'简化'都会以重复劳动还回来"），也是全部幂等路径（Trigger/Ticket/Decision/Assignment/Artifact/Schedule fire/reply key，19 处调用）的基础。**它不能用 UUID**（`google/uuid` 在 module cache 里，但用 UUID 就直接丢掉幂等性；`Ticket.Number` 的注释 `ticket.go:215-218` 解释得很清楚：hash 用于身份、序号用于对话）。
- 判据检查：stdlib 有没有更合适的 hash/serialize？
  - `hash/maphash` 不可用（随机种子，跨进程不稳定）；
  - `crypto/sha256` + `encoding/hex` 正是当前用法，**没有更短的等价物**；
  - 唯一可改进点是 `fmt.Fprint(h, p)` 的 part 编码（`ids.go:46-50`）：分隔符是 `"\x00"`，若某个 part 自身含 `\x00` 会出现歧义（例如 part 序列 `("a\x00b")` 与 `("a","b")` 哈希相同——**这是 inference，不是已触发的 bug**，因为现有调用点传的都是 repo/number/text 等不含 NUL 的值）。若要做到类型级无歧义：`binary.Write` 长度前缀，或 `json.Marshal(parts)` 后哈希（都是 stdlib，effort S）。**P2。**
- `WorkspaceIDOf`（`ids.go:65-72`）手写了前缀切分，stdlib 更直接：`strings.CutPrefix(string(s), "ws:")`（Go 1.20+，本机 `go doc strings.CutPrefix` 已确认）。**P2，一行。**
- **`StageOf`（`ticket.go:258-283`）JUSTIFIED**：派生阶段（不存状态字段）是设计 §4.2 的核心，注释明确"changing the order changes the semantics, so it must match 003 Execution exactly"。这是**领域真值表**，任何框架都表达不了。
- **workspace 作为新实体 JUSTIFIED**：`Workspace`（`workspace.go:20-30`）+ `Facts`（`workspace.go:47-62`）+ `Brief.Stale`（`workspace.go:68-73`）是 ADR-14 的"属性而非分区"模型；`workspace_id` 列遍布各表（`schema.go:16,28,47,56,66,85`）是刻意的。
- `Disposition.Thread` 字段（`trigger.go:106`）**从未被赋值或读取**（所有 `Disposition{}` 字面量见 `engine.go:170,187,257,283`，都不带 Thread；`PutTrigger` 也只存 `reason`/`ticket_ids`）。**DEAD 字段**（§6）。

---

### 3.6 Skills（问题 6）

#### 3.6.1 frontmatter：**JUSTIFIED（明确不要 yaml.v3）**

`parse.go:107-148` 是 42 行扁平 `key: value` 解析器，`parse.go:102-106` 把 why 写在了代码里：

> "只支持扁平的 `key: value`，不引入 YAML 解析器。理由：技能元数据就是三个短字段，而 YAML 会把 `description: on` 解析成 bool、把带冒号的描述解析成 map——这些'聪明'的行为在配置里只会变成难查的坑。"

**核实**：`gopkg.in/yaml.v3@v3.0.1` **确实**在 module cache 里（`/root/go/pkg/mod/gopkg.in/yaml.v3@v3.0.1`，作为间接依赖的间接依赖在 go.sum 中），所以"技术上可以 import"。但该判断是对的：yaml.v3 解码到 `map[string]string` 时，`on`/`off` 之类的值会撞上 YAML 1.1 兼容解析；而项目要的恰恰是**字节级可预测**（`unquote` `parse.go:163-185` 只处理成对引号）。**判 JUSTIFIED。**

**代价需要有人知道**（P2，非代码问题）：不支持 YAML 块标量（`description: |` / `>`）与嵌套。若将来要兼容 Anthropic 生态里常见的多行 `SKILL.md`，`splitFrontMatter` 会报"不是 key: value 形式"。这是**格式契约**问题，应该在文档里写死，而不是换成 yaml.v3。

#### 3.6.2 匹配打分：**JUSTIFIED**

`match.go` 是纯函数打分（when-to-use 权重 3 / name 2 / description 1，`match.go:13-22`），排序键 `(score desc, name asc)`（`match.go:52-57`）— 确定性是**硬要求**（`skills.go:9-13`：同一 Ticket 重跑必须得到同一组技能）。`containsTerm`（`match.go:102-123`）手写词边界：

- 为什么不用 `regexp` 的 `\b`：Go 的 `\b` 是"**ASCII** word boundary"（`go doc regexp/syntax`），而这里必须同时支持"中文子串匹配 + 英文词边界"（`match.go:98-101` 注释）。一条正则表达不了两种语义；每次调用现编译正则在 prompt 热路径上更慢。**保留**。
- 不用分词器（gojieba 之类）：`match.go:174-176` 已论证"二元组不够准但不引依赖、不会因词典缺失而崩"，符合 CGO=0 与"少依赖"。**保留**。
- 小 nit（P2）：`isCJK`（`match.go:157-159`）用 `unicode.Is(unicode.Han, r)`，而 `brief.isCJK`（`brief.go:179-181`）用硬编码区间 `0x4E00-0x9FFF`/`0x3400-0x4DBF`（**不含**扩展 B+ 与兼容区）——两者对同一字符可能给出不同答案，属 §5 重复项。

#### 3.6.3 路径包含 / 符号链接防护：**SHOULD-USE-FRAMEWORK（stdlib `os.Root`，P1）**

**现状**：`resolveSkillDir`（`skills.go:222-249`）+ `resolveSkillFile`（`skills.go:256-273`）+ `ensureInside`（`skills.go:276-285`）= 64 行"Lstat → EvalSymlinks → `filepath.Rel` 判 `..`"。注释（`skills.go:219-221,253-255`）说得对：这是**提示注入入口**，不是小事。

**stdlib（Go 1.24+，本机 go1.27.3 已确认）提供的就是这件事**：`os.OpenRoot(name) (*os.Root, error)`，`go doc os.Root`：

> "Root may be used to only access files within a single directory tree. … Methods on Root will follow symbolic links, but symbolic links may not reference a location outside the root. … Methods on Root are safe to be used from multiple goroutines simultaneously."

`(*os.Root)` 的方法齐全：`Lstat/Stat/Open/OpenFile/ReadFile/ReadDir(经 FS)/EvalSymlinks?`（无 EvalSymlinks，但**不需要**了），见 `go doc os.Root`。关键收益是**语义更强**：`os.Root` 在 Linux 用 `openat2(RESOLVE_BENEATH)` 这类内核级约束，**没有 TOCTOU 窗口**；而现在的"先 `EvalSymlinks` 再 `ReadFile`"之间目标可以被替换（注释里担心的攻击者恰恰是能写工作区的人）。

**迁移草图**（`loadRoot` `skills.go:137-213` 改成以 root 为中心）：
```go
r, err := os.OpenRoot(absRoot)         // 一次
defer r.Close()
entries, err := os.ReadDir(absRoot)    // 列目录仍可用 ReadDir（名字来自 root）
dir, err := r.Stat(name)               // 不逃逸；内部 symlink 可跟随
raw, err := r.ReadFile(filepath.Join(name, "SKILL.md"))
```
逃逸错误直接来自 `os.Root`（`*PathError`，可 `errors.Is(err, fs.ErrPermission)`/字符串判定——**这点比现状弱一些**：现在能给出"通过符号链接逃出了根目录"的定制错误，`os.Root` 给的是"path escapes from parent"，需要在错误包装里翻译）。**effort M、风险中（错误文案与 `seenDirs` 去重逻辑要重写，`skills_test.go` 有 337 行符号链接用例可依赖）、收益：删 64 行 + 消除 TOCTOU。**

**同仓库同类代码**（超出本切片但同源）：`internal/agent/tools.go:304` 也在手写 containment——建议一起收敛。

#### 3.6.4 注入预算/UTF-8 截断：`strings.ToValidUTF8`（P2）

`render.go:90-99` 用"截到 max 字节 → 若非法 UTF-8 就逐字节回退"实现"按字符边界截断"。stdlib 一行：`strings.ToValidUTF8(cut, "")`（`go doc strings.ToValidUTF8` 已确认存在）；或 `utf8.DecodeLastRuneInString` 一次定位。**effort S、风险低、纯清扫级。**

---

### 3.7 Brief / Scan（问题 7）

- **`humanAge`（`digest.go:161-171`）JUSTIFIED**：输出"刚刚 / N 分钟前 / N 小时前 / N 天前"。`github.com/dustin/go-humanize@v1.1.0` 在 module cache，但 `humanize.Time` 是英文（"2 days ago"），没有中文 locale；设计明确要求中文文案（`design §1.3` 首版中文）。换成 humanize 等于自己再包一层中文，**不划算**。
- **14 天摘要 + 200 字（`digest.go:15-19,28-82`）JUSTIFIED**：`Digest` 每次从 durable tickets 现算（"cached summary would go stale exactly when it matters"，`digest.go:22-24`）+ 确定性输出（diff 可读、prompt cache 稳定）。这是**提示词工程约束**，不是文本工具能替代的。`oneLine`/`clipRunes` 的字符级截断是中文排版需求（"…"），见 §5 重复项。
- **`scan` 不 shell out**（已核实：`internal/scan` 无 `os/exec`），`gitFacts`（`scan.go:51-74`）直接读 `.git/HEAD` 与 `.git/config`。**JUSTIFIED**：`github.com/go-git/go-git/v5` **不在** go.mod/module cache（需新增 + 数十个传递依赖），为读两个值不值得；子进程方案被注释明确否决（`scan.go:49-50`："must not depend on a working git install or take a subprocess slot"）。
  **但要登记一个真实缺陷（P2）**：worktree / submodule 里 `.git` 是**文件**（`gitdir: …`）不是目录，`os.ReadFile(root/.git/HEAD)` 会失败 → `Remote`/`DefaultBranch` 静默为空（不是报错）。M5 若支持 worktree 需补 3 行 fallback。
- **`stackOf`/`commandsFor`（`scan.go:147-193`）JUSTIFIED**：从 `go.mod`/`package.json`/`pyproject.toml`/`Cargo.toml` + lockfile 判语言与包管理器，再给出 build/test/lint 命令。`github.com/go-enry/go-enry/v2` **不在**依赖图（新增依赖 + 数 MB 的 linguist 数据），而且它做的是"按文件内容猜语言"，与这里"按工程文件定工具链"是**不同问题**。判 JUSTIFIED。
- `dirHint`/`lastModified`（`scan.go:103-136,214-237`）有**质量小问题（P2，非框架）**：`dirOutline` 只在顶层跳过 `.`/`_` 目录（`scan.go:86`），而 `dirHint` 与 `lastModified` 的 `WalkDir` **不跳过** `.git`/`node_modules`（只有深度 2 与数量上限兜底，`scan.go:106-122,231-233`）→ 启动扫描会去 stat `.git/objects` 里的文件，还让"最近活动"被 `.git` 的 mtime 污染。加一行 `if d.IsDir() && strings.HasPrefix(d.Name(), ".") && path != root { return filepath.SkipDir }` 即可。

---

## 4. 正确使用框架/依赖的部分（避免误伤）

| 位置 | 做法 | 判据 |
| --- | --- | --- |
| `store.go:47` | modernc.org/sqlite 的 DSN pragma：`journal_mode(WAL)` + `synchronous(FULL)` + `foreign_keys(ON)` + `busy_timeout(5000)` | 用**驱动提供的 DSN 机制**而不是每次 `Exec("PRAGMA …")`；`synchronous=FULL` 与"崩溃可恢复"承诺匹配 |
| `store.go:302-309,356-370,401-408,459-462`；`effects.go:209-218`；`store/load.go:163-168` | 全面使用 SQL `UPSERT`（`ON CONFLICT … DO UPDATE/NOTHING`）而非"先查再写" | 把并发/幂等交给 SQLite，而不是应用层判断 |
| `schema.go:62,92,137,145-146` | 部分索引（`WHERE ended_at IS NULL` / `WHERE state='pending'`） | 用 SQL 的表达力支持"只扫活跃行"的设计（§7.1） |
| `store/settings.go:72-110` | 乐观并发（revision 在事务内比对） | 正确使用事务做 CAS，没有用文件锁/进程内计数器 |
| `skills.go:129`、`skills.go:209` | `errors.Join` 聚合"坏技能/坏根目录"，一次报全 | stdlib 正确用法（不是手写 `[]string` 拼错误） |
| `schedule/store.go:238-239`、`store/store.go:119-124` | 定宽 UTC 文本 + `next_at <= ?` 在 SQL 里比较 | 刻意**避免**应用层时间比较（含注释解释 RFC3339Nano 为什么不能用于比较） |
| `schedule_test.go:8` | `_ "time/tzdata"` 内嵌时区库 | DST 测试不依赖宿主 tzdata —— 这个习惯让 §3.3.1 的替换变得安全 |
| `internal/mcp/tool.go:12,47` | 用 `eino-contrib/jsonschema` 生成**工具参数** schema | 该依赖用在它该用的地方（本切片内无手写 JSON schema 校验） |
| `domain/*` | 纯 stdlib、零 I/O（`ids.go:1-3` 包注释） | 依赖方向干净，没有把 store/UI 拉进领域 |
| `effects.go:443-487` | 所有后台读都走 `Store.Guarded` | 正确认识"单连接 = 单事务"的陷阱（`schedule` 没跟上，见 §3.1.4） |
| `outbox.go:71-81` | `Sender` 接口 + `SenderFunc` 适配器 | 接口设计正确（虽然 `SenderFunc` 目前没人用，§6） |

---

## 5. 跨包重复工具（问题 8）

| # | 重复对 | 位置 | 说明 / 建议 |
| --- | --- | --- | --- |
| D1 | **outbox 行解码 3 份** | `outbox.go:360-391`（`receipt`，单行）vs `outbox.go:393-414`（`scanReceipt`，多行）vs `store/load.go:253-277`（`outboxWhere`） | 同一 12 列投影、同一 `time.Parse(RFC3339Nano)` 忽略错误（`outbox.go:388-389`、`load.go:272-273`）。建议：`rowScanner` 接口已在 `outbox.go:356-358`，`receipt()` 可以复用 `scanReceipt` 的字段绑定（把单行也包成 rows），并把 `store.OutboxRecord` 与 `outbox.Receipt` 合并成一个模型 |
| D2 | **按 rune 截断 + "…"** | `brief/digest.go:139-146` `oneLine`、`brief/digest.go:150-159` `clipRunes`、`engine/engine.go:580-586` `clip`、（切片外 `runtime/pages.go:378`、`view/search.go:365`、`agent/compact.go:66`） | 6 份、3 种语义（是否加省略号、是否按 field 折叠）。建议 `internal/text.ClipRunes(s string, n int, ellipsis bool)` |
| D3 | **按字节截断** | `effects/effects.go:644-649` `truncate`（**按字节**，可能切断 UTF-8）、`skills/render.go:90-99` `truncateBlock`（按字节 + 修复）、`engine/engine.go:417`（`[]rune(s)` 后再截） | `effects.truncate` 用在 error/health detail（`effects.go:544,576,638`）——中文错误消息会被切成半个字符，虽然只是日志/面板文本，但应统一走"按 rune"的那个 |
| D4 | **Thread key 两种格式** | `domain.ThreadKey`（`ids.go:51-53`）产出 `scope\|source\|id`；`engine.threadKeys`（`engine.go:423-429`）产出 `source\|id` 填进 `Ticket.SubscribedThreads`（`ticket.go:227`） | **真实隐患**：同名概念两种编码，`domain.ParseThreadKey`（`ids.go:56-62`）解析不了 engine 的那份；而 `ParseThreadKey` 恰好是死代码（§6）——说明这条链路没人校验过。建议 `SubscribedThreads` 改存结构化 `[]domain.ThreadRef` 或统一走 `domain.ThreadKey` |
| D5 | **CJK/ASCII 分词** | `brief/brief.go:154-177` `normalize`（ASCII 词 + **单个汉字**）vs `skills/match.go:177-222` `describeTerms`（ASCII≥3 + **汉字二元组**） | 语义刻意不同（召回 vs 打分），但共享的"按 rune 分类 ASCII/CJK/其它"可以抽一个 `tokenize(rune) (kind, rune)`；`isCJK` 两份实现不一致（`brief.go:179-181` 硬编码区间 vs `match.go:157-159` `unicode.Han`）——**应统一**，否则扩展区汉字在一个包里匹配、另一个包里不匹配 |
| D6 | **时间格式化两条路** | `store.FormatTime/ParseTime`（`store.go:115-124`）vs 各处的 `time.RFC3339Nano`（`outbox.go:171-172,350-351,388-389`、`store.go:301,361,399,407`、`load.go:167,186,272`） | 见 §3.2.3；建议"给 SQL 比较/定宽需求的列一律 `FormatTime`，且禁止直接 `time.RFC3339Nano` 入库" |
| D7 | **Ticket 创建代码块** | `engine.go:264-281`（`Receive`）vs `engine.go:658-676`（`Approve`） | 20 行逐字重复（`nextNumber++`、`domain.Key(... "ticket", dec.ID)`、初始 `Assignment`、`SubscribedThreads`）。两条路径改一处忘另一处的风险是实打实的（例如将来给新 Ticket 加字段）。建议抽 `newTicketLocked(dec, t)` |
| D8 | **engine 内 `clip`/`firstSentence`/`truncate` 三件套** | `engine.go:410-421`、`engine.go:580-586`、`engine.go:417-419` | 与 §5 D2 同族；`firstSentence`（句读切分 + 40 rune）有真实设计理由（`engine.go:406-409` 注释），保留但可归入同一个 text helper 包 |
| D9 | **health 聚合 SQL 两份** | `effects.go:591-625`（`report`）vs `effects.go:353-374`（`Status`） | 同一个 `SELECT COUNT(*), MIN(since), MAX(attempts), (SELECT error …)`，只差 health 写入。建议 `Status()` 复用 `report` 的查询 |
| D10 | **`formatTime`/`boolInt` 别名** | `schedule/store.go:461,470-475` | `formatTime` 纯别名；`boolInt` 冗余（驱动已处理 bool，见 §3.3.3） |
| D11 | **row→domain 映射模式** | `store.go:320-331,439-449`、`load.go:62-82,116-128,178-195,263-276`、`schedule/store.go:429-459`、`outbox.go:360-414` | 全仓库 ~10 处"Scan → 拼 domain struct"；若引入 codegen（sqlc）这是唯一有吸引力的点，但收益仍小于风险（§3.1.5）。至少统一 `time.Parse` 错误处理（现在全部 `_` 丢弃） |

---

## 6. 死代码 / 死依赖（问题 9）

### 6.1 从 `main` 不可达（RTA + grep 复核）

| 符号 | 位置 | 证据 |
| --- | --- | --- |
| `brief.OneLine` | `brief.go:184-186` | RTA 不可达；全仓 0 非测试引用 |
| `brief.firstLine` | `brief.go:188-193` | 只被 `OneLine` 调用 |
| `brief.Threshold`（const 10） | `brief.go:80-82` | 0 引用 ⇒ **"工作区超过 10 个退回单行索引"这个功能只有常量与格式化函数，从未接线**（`router.go:87-94` 永远渲染完整 brief） |
| `domain.ParseThreadKey` | `ids.go:56-62` | RTA 不可达；0 引用 |
| `outbox.SenderFunc`（+ `Send`） | `outbox.go:75-81` | RTA 不可达；3 处非测试命中全是定义/注释 |
| `schedule.Schedule.Spec` | `spec.go:107-108` | RTA 不可达（`Definition.Spec` 直接读 field） |
| `schedule.Schedule.Location` | `spec.go:110-111` | RTA 不可达 |
| `store.OpenTickets` | `store.go:428-451` | 非测试引用 0（仅 `store_test.go:188,195`）；`engine` 用 `store.Tickets()` 全量加载（`persist.go:90`） |
| `store.Outbox` | `load.go:241-251` | 全仓 0 引用（`outbox` 包自己走 `receipt()`） |
| `store.Health` | `load.go:295-303` | 非测试引用 0（仅 `effects_test.go:380`）。**注释声称"so `zeokeel status` can see what the effect queue reported"（`load.go:295-296`）——但 `cmd/zeokeel` 里没有任何 health 读取（grep 仅命中一条测试注释），文档与实现不符** |
| `engine.ErrNoWorkspace` | `router.go:179-180` | 全仓 0 引用（`routeLocked` 用 `NeedsPerson`+`Ask` 表达同一件事） |
| `domain.Disposition.Thread`（字段） | `trigger.go:106` | 所有 `Disposition{}` 字面量都不赋值（`engine.go:170,187,257,283`），`PutTrigger` 不存（`store.go:365-370`），`Triggers()` 不读（`load.go:72-80`） |
| `store.Get` / `store.Set`（KV 访问器） | `store.go:480-490` | 非测试引用 0（settings 走 `Settings()/SaveSettings()`；`kvGet/kvSet` 是内部实现）——**这两个是导出的"公共 API"但只有一个测试用** |

**建议**：删掉上面这些（保留 `Get/Set` 的话就写一个真实调用者，例如 `zeokeel status` 读 health + 读 KV cursor）；`Threshold/OneLine/firstLine` 要么接线（design §4.6 提到"简报是缓存"，多工作区时的确需要一个降级路径），要么整组删除——**留着是双重债务：既读不懂代码意图，又让人以为该功能已实现。**

### 6.2 仅测试使用（不算死代码，但要知道）

`effects.WithClock`（`effects.go:91-94`）、`outbox.WithClock`（`outbox.go:106-109`）、`outbox.WithLimit`（`outbox.go:111-115`）、`schedule.WithClock`（`store.go:74-75`）——RTA 报不可达，但它们是**测试 seam**（`schedule_test.go` 用它驱动 ClaimDue），保留合理。`SetEnabled/Delete`（`schedule/store.go:179-220`）在生产路径也没调用（面板未接），属"已实现未接线"，与 `OneLine` 同性质但更可能是 M5 计划内，**不建议删**。

### 6.3 死依赖

**无**。`go mod tidy -diff` 无输出 ⇒ go.mod/go.sum 已是 tidy。本切片只 import 了 `cloudwego/eino` 的相邻包之外的 stdlib + `modernc.org/sqlite`；`bmatcuk/doublestar/v4`、`golang.org/x/time`、`dustin/go-humanize`、`gopkg.in/yaml.v3`、`google/uuid`、`buger/jsonparser` 全部是 `// indirect`（由 eino/mygo 的依赖树带入），**本仓库代码一行都没用**（已 grep 确认）——所以：

- §3.3 提到"doublestar/v4 已在 module graph"是真的（`go.mod:16` 标 `// indirect`），但**它不是项目已采用的依赖**，只是"理论上不用下载就能 import"。同理 `x/time`（`go.mod:58`）、`yaml.v3`（`go.mod:59`）、`go-humanize`（`go.mod:23`）。
- 这意味着**任何"换成已有依赖"的建议都必须同时说明它是间接依赖**（升为直接依赖 = 改 go.mod 一行，无新下载，但仍应视为一次依赖决策）。
- 唯一需要**新增下载**的候选（`robfig/cron/v3`）不在 go.mod/go.sum，虽已在 module cache（`cache/download/github.com/robfig/cron/v3/@v/v3.0.1.zip` 齐全），落地仍需 `go get`。

---

## 7. 不确定 / 未覆盖

1. **`os.Root` 的错误文案**（§3.6.3）：我没在仓库里实跑改造，`os.Root` 逃逸错误的 `errors.Is` 目标与 `skills_test.go` 现有断言（337 行，含"符号链接逃出根目录必须报错"用例）是否兼容未验证 → 迁移时先写一个 spike（`/tmp` 里跑过 `go doc`，行为描述来自 go1.27.3 文档，**未实测 openat2 路径**）。
2. **§3.3.2 的 `N/step` 缺陷**：结论来自读代码（`cron.go:87-111`）与 cronie/robfig 语义对比，**没有在跑的二进制上复现**（不能改源码加测试）。风险低（逻辑是线性的）。
3. `time.Date` 歧义选择"较早瞬间"是**实测行为**（go1.27.3 linux/amd64），`go doc` 明确不承诺 → §3.3.1 的方案里保留了显式 tie-break，需在 `schedule_test.go` 补一条"重复墙钟取较早"的断言（现在 `:140` 只断言"存在下一次"）。
4. **`go build -tags mygo_noinspector` 全量构建未跑**（审计只读且耗时；`go mod tidy -diff` 与 RTA 加载 SSA 已经隐含编译了全部包，包括 `cmd/zeokeel`）。
5. 本文所有 "原型就是这么设计的" 结论引用的是 **design 文档**（§0.3/§4/§7.2），未逐行对照 `_ref/src`——原型语义的最终裁定权在 `_ref/docs/000-008` 与设计文档作者。
