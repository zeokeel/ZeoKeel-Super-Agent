# M0 任务清单：地基（2 周）

> 目标：**能编译、能开窗、能存、零 cgo 守卫常绿**。
> 出口标准（Done 的定义）：见 §2。所有任务的文件范围互不重叠，可两人并行。

---

## 0. 进度（截至本次提交）

| 任务 | 状态 | 证据 |
| --- | --- | --- |
| **M0-1** 项目骨架与模块 | ✅ 完成 | 根 `go.mod`（`github.com/zeokeel/super-agent`）、`cmd/zeokeel`、`internal/{domain,brief,im,engine,store}`、`Makefile`、`.gitignore` |
| **M0-2** 领域层补全 | 🟡 部分 | 已有 17 个领域测试（含 ID 作用域、阶段表、跨区隔离）；12 条不变量中的 1/3/10/11 尚未写 |
| **M0-3** SQLite store | ✅ 完成 | schema v1 十一张表、单写者事务、嵌套 SAVEPOINT、`afterCommit`、schema 版本守卫、6 个 store 测试 |
| **M0-4** 零 cgo 守卫 + CI | ✅ 完成 | `scripts/check-nocgo.sh` PASS；`.github/workflows/ci.yml` 四个 job |
| **M0-5** mygo 窗口骨架 | ✅ 完成 | `internal/ui`：窗口、托盘、关窗=隐藏、`OnWindowAllClosed` 不退出、`RequestSingleInstanceLock` + `OnSecondInstance` 聚焦已有窗口、`ActivationPolicyAccessory` |
| **M0-6** 视图快照层 | ✅ 完成 | `internal/view`：`atomic.Pointer[OverviewVM]` + `OnPublish` 单次重绘 + `Win.Invalidate`；后台 publisher 每 2s 发布，状态栏 `rev` 递增可证 |
| **M0-7** 提取 chrome | ✅ 完成 | `internal/ui/{tokens,widgets,chrome}.go` 从 `design/prototype` 提炼：主题 tokens、侧边栏 6 区、顶栏、状态栏、logo |
| **M0-8** 总览页 + 冒烟 | ✅ 完成 | 总览页（需要我处理 / 系统健康 / 24h 活动 / 运行时 / 交付物 / 存储）+ 4 个 `ui.NewTester` 无头测试 |

**M0 出口验收（§2 十条）实测**：

| # | 标准 | 结果 |
| --- | --- | --- |
| 1 | `CGO_ENABLED=0` 构建 + 六平台交叉 | ✅ linux/darwin/windows × amd64/arm64 全过 |
| 2 | `check-nocgo.sh` | ✅ PASS (zero cgo) |
| 3 | `go test ./...` 无显示器 | ✅ 33 个测试全绿 |
| 4 | `go vet` | ✅ exit 0 |
| 5 | 领域不变量 12 条 | 🟡 8 条（1/3/10/11 依赖 Eval 批处理与投递回调，随 M1/M2 落地） |
| 6 | SQLite 重启后一致 | ✅ store 测试覆盖 |
| 7 | 窗口 + `ui.NewTester` | ✅ Xvfb 截图见 `artifacts/m0-window.png` |
| 8 | 关窗不退 + 单实例 | ✅ 代码就位（托盘需真桌面验证） |
| 9 | release < 40 MB | ✅ **9.5 MB** |
| 10 | 两工作区同编号隔离 | ✅ domain + store 双重覆盖 |

**当前代码量**：约 4200 行 Go（含测试）。外部依赖只有 `modernc.org/sqlite`（纯 Go）与 `github.com/egoist/mygo`（purego，无 cgo）。

**M0 后期调整（两次）**：① 从"6 个 tab 仪表盘"改为三栏工作台；② 再改为**单条消息流 + 一个薄输入框 + 巡检入口**——因为 Super Agent 不在窗口里对话（回复发回原渠道），且只有一个连续上下文（自动压缩）。侧边栏全中文。见设计 §6.3 / §6.9。

**已验证**：`CGO_ENABLED=0 go build ./...`、`go test ./...`、`sh scripts/check-nocgo.sh`（PASS）、linux/amd64+arm64、darwin/arm64、windows/amd64 交叉编译全部通过。

---

## 1. 起点与终点

| | 起点（现在） | 终点（M0 结束） |
| --- | --- | --- |
| 代码 | `spike-skeleton/` 2122 行、20 个测试、零第三方依赖 | 真实项目骨架：`cmd/zeokeel` + `internal/*` + SQLite + mygo 窗口 |
| 持久化 | 内存 map | SQLite（`workspace` 表 + 全表 `workspace_id` + 迁移 v1） |
| UI | 无 | 原生窗口：侧边栏 6 区 + 总览页假数据 + 托盘 + 关窗=隐藏 + 单实例 |
| CI | 无 | 零 cgo 守卫 + 三平台交叉编译 + `go test` + `mygo vet` 全绿 |
| 二进制 | 无 | release < 40 MB，`CGO_ENABLED=0`，Windows/macOS/Linux 均可构建 |

**M0 不做**：接真模型（M1）、真实 IM 长连接（M1）、真实 GitHub（M2）、完整面板（M5）。

---

## 2. 验收标准（M0 的 DOD）

一条不过就不算完成：

1. `CGO_ENABLED=0 go build ./...` 通过，且 `GOOS=darwin/linux/windows × amd64/arm64` 六目标全过。
2. `sh scripts/check-nocgo.sh` 输出 `PASS (zero cgo)`。
3. `CGO_ENABLED=0 go test ./...` 全绿，**不需要显示器**。
4. `go vet ./...` 与 `go tool mygo vet ./...` 均 exit 0。
5. 领域不变量测试 **12 条全绿**（清单见 §4）。
6. SQLite：建库 → 写入 → 重启进程 → 读回，内容一致；schema 版本写入 `kv`，低版本拒绝启动。
7. UI：`xvfb-run -a CGO_ENABLED=0 go run ./cmd/zeokeel` 能开窗并显示总览页；`ui.NewTester` 有一个点击测试通过。
8. 关窗后进程仍在，托盘菜单可唤回窗口；第二次启动不新开进程，而是唤起已有窗口。
9. release 二进制（`-tags mygo_noinspector -ldflags "-s -w"`）< 40 MB。
10. 两个工作区同编号 `#12` 在库里是两条互不相干的记录（跨区隔离测试）。

---

## 3. 任务表

依赖列写 `→` 的任务必须等前置完成。估时按 1 人算。

| ID | 任务 | 产出 | 文件范围 | 依赖 | 估时 |
| --- | --- | --- | --- | --- | --- |
| **M0-1** | 项目骨架与模块 | 根 `go.mod`（module `github.com/zeokeel/super-agent`）；`cmd/zeokeel/`、`internal/`、`scripts/`、`Makefile`、`.gitignore`；把 `spike-skeleton` 的 domain/brief/im/engine 迁入并保留全部测试 | `go.mod` `go.sum` `Makefile` `.gitignore` `cmd/**` `internal/**` | — | 0.5d |
| **M0-2** | 领域层补全 | 12 条不变量的测试；补 `Policy`、`Reason` 码表、`Disposition` 完整字段、`follows`、`Input`、`Attention` 生命周期；`UserError{Code, Place}` | `internal/domain/**` | M0-1 | 2d |
| **M0-3** | SQLite store | schema v1（`workspace`/`trigger`/`disposition`/`thread`/`ticket`/`decision`/`effect`/`outbox`/`kv`/`session_event`/`health`）；单写者事务、`afterCommit`、活跃集部分索引；迁移框架（v1 起）；`modernc.org/sqlite` | `internal/store/**` | M0-1 | 3d |
| **M0-4** | 零 cgo 守卫 + CI | `scripts/check-nocgo.sh` 接入；`.github/workflows/ci.yml`：build 矩阵 + vet + test + 体积回归 | `scripts/**` `.github/**` | M0-1 | 0.5d |
| **M0-5** | mygo 窗口骨架 | 窗口 + 托盘 + 关窗=隐藏 + 单实例（`mygo` `single_instance`）+ 主题 tokens；`--headless` 分支不 import 窗口路径 | `internal/ui/**` `cmd/zeokeel/**` | M0-1 | 3d |
| **M0-6** | 视图快照层 | `atomic.Pointer[VM]` 分片 + `Invalidate` + 16–33ms 抖动合并；UI→core 命令总线（非阻塞）；快照构建器 | `internal/view/**` `internal/ui/snapshot.go` | M0-5 | 2d |
| **M0-7** | 从原型提取 chrome | 把 `design/prototype` 的 `chrome.go`/`theme.go`/`icons.go` 提炼成 `internal/ui` 的侧边栏、顶栏、状态栏、tokens | `internal/ui/**` `assets/**` | M0-5 | 1d |
| **M0-8** | 总览页跑假数据 + 冒烟测试 | `view.OverviewVM` 假数据渲染；`ui.NewTester` 点击侧边栏切换 + 断言文本；`xvfb` 真窗口截图脚本 | `internal/ui/views/overview.go` `internal/ui/*_test.go` | M0-6 M0-7 | 1d |

**合计 ≈ 13 人日**（含缓冲到 2 周）。

**并行建议**：
- 人 A：M0-1 → M0-3 → M0-2（store 与领域交叉验证）
- 人 B：M0-1 → M0-5 → M0-7 → M0-6 → M0-8
- M0-4 谁先空谁做，半天。

---

## 4. 领域 12 条不变量（M0-2 的测试清单）

每条一个表驱动测试，文件 `internal/domain/invariants_test.go`：

1. 同一 Trigger 接受两次 → 只产生一个 Ticket。
2. 每个 Thread 同时最多一个 Eval；期间到达的 Trigger 合并进下一批。
3. 人的消息**优先扇出**；只有 `followupOwners` 为空才进 Eval。
4. `settled(decision)`：stop / unavailable / 带 `stopNotExplicit|stopUnknown|routed` 的决策**不算**已处理完。
5. Ticket 结束时未读的人消息要 `letGo`（放回 Eval）；**人 stop 结束的那条不自动重评**。
6. `waitsForPerson` 期间**自动输入被扣住**（CI/bot/note 投递但不唤醒）。
7. 策略变更**不撤销**已发 Mandate，只写 policy-changed Input。
8. 同一 Ticket 对同一会话的出站消息保序；前者连续失败 3 次才放行后者。
9. `followsDone()` 且 `!unfinished()` 才允许按 PR 结局结束 Ticket；`merged`/`closed` 取最后一次 close 的 outcome。
10. 三个投递回调各司其职：`delivered`（进隧道）、`read`（进模型历史）、`turnEnded`（后台静默）。
11. 只有最终 Report 能从 Executor 侧结束 Ticket。
12. reason / error 一律"码 + 模板 + 参数"，不存自由文本。

**补充的 M0 新增测试**（多工作区相关，来自 §4.5–§4.9）：

13. `ws_a|github|12` ≠ `ws_b|github|12`（ID 作用域）。
14. `WorkspaceID` 在 root 变更后保持不变（不是路径派生）。
15. 全局 Source 的 Trigger 无 `default_workspace`；Ticket 一定有 workspace。
16. `@所有人` 不算 addressed。
17. 召回不到工作区时返回空候选，不返回"最相近的"。

---

## 5. 存储 schema v1（M0-3 的验收）

```sql
-- 每个工作区一行；brief 是 JSON
CREATE TABLE workspace(
  id TEXT PRIMARY KEY, name TEXT, root TEXT, remote TEXT, kind TEXT,
  brief TEXT NOT NULL, created_at TEXT, updated_at TEXT);

-- 所有业务表都带 workspace_id；它是属性，不是分库分区
CREATE TABLE trigger(
  id TEXT PRIMARY KEY, workspace_id TEXT, scope TEXT NOT NULL,
  source TEXT, at TEXT NOT NULL, json TEXT NOT NULL, default_workspace TEXT);
CREATE INDEX trigger_by_ws_at ON trigger(workspace_id, at DESC);

CREATE TABLE disposition(
  trigger_id TEXT PRIMARY KEY, kind TEXT, reason TEXT, ticket_ids TEXT);
CREATE TABLE thread(
  key TEXT PRIMARY KEY, workspace_id TEXT, source TEXT, last_at TEXT,
  json TEXT NOT NULL);
CREATE TABLE ticket(
  id TEXT PRIMARY KEY, workspace_id TEXT NOT NULL, stage TEXT, -- 派生值，仅用于查询加速
  ended_at TEXT, created_at TEXT NOT NULL, json TEXT NOT NULL);
CREATE INDEX ticket_open ON ticket(workspace_id, created_at DESC) WHERE ended_at IS NULL;
CREATE TABLE decision(id TEXT PRIMARY KEY, workspace_id TEXT, kind TEXT, json TEXT NOT NULL);
CREATE TABLE effect(
  kind TEXT, key TEXT, seq INTEGER, generation INTEGER,
  attempts INTEGER, due TEXT, error TEXT, payload TEXT, PRIMARY KEY(kind,key));
CREATE TABLE outbox(
  key TEXT PRIMARY KEY, workspace_id TEXT, ticket_id TEXT, channel TEXT,
  state TEXT, text TEXT, created_at TEXT);
CREATE TABLE kv(k TEXT PRIMARY KEY, v TEXT);
CREATE TABLE session_event(
  session_id TEXT, seq INTEGER, at TEXT, kind TEXT, payload TEXT,
  PRIMARY KEY(session_id, seq));
CREATE TABLE health(entry TEXT PRIMARY KEY, state TEXT, detail TEXT, at TEXT);
```

**`ticket.stage` 是缓存列，不是真相**：写入时由 `StageOf` 计算，读取时用它过滤；测试要断言"缓存列 == 实时计算值"。

PRAGMA：`journal_mode=WAL`、`synchronous=FULL`、`foreign_keys=ON`；DB 文件 0600、目录 0700。

---

## 6. 风险与对策

| 风险 | 对策 |
| --- | --- |
| mygo 0.x API 变更 | 所有 mygo 用法只在 `internal/ui`；锁版本；升级跑 `go tool mygo migrate-ui` |
| 主线程阻塞 | §6.1 三条铁律写进 `internal/ui/README.md` 当 review checklist；`MYGO_FRAME_STATS=1` 监控 >8ms 帧 |
| 体积超 40 MB | M0-4 就加体积回归；超标先拆 build tag |
| 领域移植偏差 | 12 条不变量测试 + 后续用原型 spec 逐条翻译 |

---

## 7. 建议的每日顺序

| 天 | 人 A | 人 B |
| --- | --- | --- |
| D1 | M0-1 骨架 + 迁移 skeleton | M0-1 协同（CI/脚本） |
| D2 | M0-3 schema + 迁移框架 | M0-5 窗口 + 托盘 + 单实例 |
| D3 | M0-3 事务 + afterCommit | M0-5 关窗=隐藏 + 主题 |
| D4 | M0-3 活跃集查询 + store 测试 | M0-7 chrome 提取 |
| D5 | M0-2 不变量 1–6 | M0-6 快照层 |
| D6 | M0-2 不变量 7–17 | M0-6 命令总线 |
| D7 | M0-2 与 store 联调（跨区隔离） | M0-8 总览页 + Tester |
| D8 | 全量验收 §2 十条 | 全量验收 §2 十条 |

---

## 8. 下一里程碑的接口约定（M0 必须留好）

M0 结束时，下列接缝要已经存在，M1 才能直接插进来：

| 接缝 | 形状 | M1 填什么 |
| --- | --- | --- |
| `engine.Assessor` | 接口，当前是 `RuleAssessor` 确定性替身 | 接 eino + 真模型 |
| `im.Provider/Receiver/Sender` | 接口，当前是 Feishu/DingTalk mock | 真实长连接驱动 |
| `store.Store` | 接口，当前只有 SQLite 实现 | 不动（内存实现已有） |
| `view.Snapshot` | 只读投影 | 接真实 store 查询 |
| `agent.Session` | 空接口 + 事件类型 | 会话/回合/压缩 |

---

## 9. M1 进度（Agent Runtime）

| 任务 | 状态 | 证据 |
| --- | --- | --- |
| eino ReAct 内核 + 流式 | ✅ | `internal/agent`：`react.NewAgent` + `maxStep`；实测两轮工具调用 |
| **工具调用检查器修正** | ✅（实现已换代） | Wave 1 曾用自研 `bufferToolCallChecker`；Wave 2 迁到 eino 官方 `adk/middlewares/patchtoolcalls` 并删除 `engine_react.go`，旧 checker 不再存在。历史问题（默认只看第一个 chunk 会吞 tool_call）由官方中间件 + ADK 路径解决 |
| Worker 工具集 | ✅ | `bash` / `read` / `write` / `edit` / `glob` / `grep`，全部限制在工作区目录内 |
| 沙箱与黑名单 | ✅ | 路径逃逸拒绝、`blockedCommands` 按名拒绝、命令 60s 超时、输出截断 |
| 事件流 | ✅ | eino callbacks → `Event{text, tool_start, tool_end, done, error}` |
| 执行器接线 | ✅ | `runtime.runTicket`：Ticket 建好后后台跑 Worker，事件进 `eventLog`，快照发布 |
| 面板呈现真实工具卡 | ✅ | 截图 `artifacts/m1-worker.png`：`▸ bash echo worker-ran-this · exit 0` + 模型读到结果 |
| 用真实模型跑 | ⬜ | 需要 API Key；目前用内置 mock 验证闭环（`-demo-model`） |

**测试**：`go test ./internal/agent/` 覆盖工具生命周期、路径逃逸拒绝、黑名单；全套 **40 个测试**。

**体积**：eino 接入后 release **22 MB**（预算 40 MB，仍有余量）。

**尚未做（按设计文档的顺序）**：会话持久化与断点续跑、上下文压缩、真实模型 prompting、Eval 的 orchestrator 会话、Effects/Outbox、真实 GitHub/IM 驱动、Goal/Schedule/Subagent/Workflow、MCP、skill。

---

## 10. M1 团队并行交付（4 条工作流）

四条工作流文件范围互不重叠，全部测试通过后由 Lead 集成。

| 工作流 | 负责人 | 交付 | 测试 |
| --- | --- | --- | --- |
| Agent 内核 | agent-core | eino **ADK** 内核（`adk.Runner` + `session.FileStore`）、`RunSession`/`Resume`/`SessionStats`、官方 `summarization` 压缩、`reduction` 工具结果落盘、用量事件、取消 | 25 个测试 |
| 持久化与可靠投递 | durability | `internal/effects`（durable 队列）、`internal/outbox`（幂等出站）、`engine.Persistence` 落盘与 `Load` 重建、schema v2 | 崩溃重放/幂等/保序/退避 |
| 面板五页 | panel-pages | 来源/触发/评估/执行/设置（只读巡检 + 设置页真实表单与保存条）、`view.SourceVM/ThreadVM/DecisionVM/TicketListVM/SettingsVM` | 25 个用例 |
| GitHub Source | github-source | `internal/sources` 契约 + `sources/github`（gh CLI 封装、Trigger 映射、游标、幂等评论） | 60 个测试函数 / 85 用例 |

**Lead 集成**：`runtime` 发布五片投影、`SettingsSaver` 实现（乐观并发）、Worker 改走持久化会话、GitHub Source 接线（`-repo`/`-gh-login`）、流式文本合并。

**总验收**：12 个包全绿、**174 个测试**、`check-nocgo.sh` PASS、六平台交叉编译通过、release **29 MB**（预算 40 MB）、19,096 行 Go。

---

## 11. M2 进度（执行链路与持久化接线）

| 项 | 状态 | 证据 |
| --- | --- | --- |
| SQLite 成为应用状态的真源 | ✅ | `cmd/zeokeel/durable.go`：`-state-dir` 打开 `zeokeel.db`，`engine.Persistence` 提交每条命令 |
| durable `launch` effect 启动 Worker | ✅ | `runtime.EnableDurable` 注册 `engine.LaunchHandler`；队列 `Start()` 后由 effect 驱动执行 |
| 重启恢复 | ✅ | `cmd/zeokeel/durable_test.go`：重启后工作区/工单/未执行的 launch 行都在 |
| 幂等出站 | ✅ | `outbox` 已接线；无外部 Channel 时由 `panelSender` 记回执（诚实：还没有远程会话）|
| 版本与分发 | ✅ | `VERSION`、`-X main.version` 注入、状态栏显示 `v1.0.0-dev`、`make dist` 产出三平台（28–30 MB）|
| 发布清单 | ✅ | `RELEASE.md`：A 功能 / B 可靠性 / C 工程 / D 分发 / E 文档 |

**已澄清（不是 bug）**：durable 模式最初看不到工具卡，原因是 demo 的 Worker 工作目录留在 `/tmp/zeokeel-work`，第二次启动时 `session.FileStore` **恢复了上次会话**（mock 请求 `msgs=12`），模型看到已有工具结果就直接收尾——这恰好证明断点续跑生效。修法：工作目录改到 `<state-dir>/work`，一次 `-state-dir` 同时控制数据库与工作树。

**顺带修的两个真问题**：
- 事件按到达顺序追加，而 ADK 的工具/模型回调来自多个引擎协程 → 面板可能把工具卡排到答案后面。改为按 `Event.Seq` 稳定排序。
- 运行时自己写的"执行器已接手"会和模型第一句话合并成一行。给它单独的 notice 标记。

**总验收**：13 个包全绿、**175 个测试**、零 cgo PASS、三平台产物 28–30 MB。

---

## 12. M2 进度（模型驱动的 Eval）

| 项 | 状态 | 证据 |
| --- | --- | --- |
| `internal/assess`：模型研判器 | ✅ | 研判提示词 + JSON 解析 + **assessmentOutcome 校验** |
| 校验规则 | ✅ | 每条 enabled 规则恰好评估一次、不接受未启用规则、至少一条命中、工作区必须在候选内、`worktree_name` 1–4 个 kebab 词、accept 必须有 `intent_evidence` |
| accept mode | ✅ | `off / observe / auto`；inferred 候选默认 observe（只记 `would_accept`，不开单）；explicit 请求不受影响 |
| 与 Engine 接线 | ✅ | `-assess-model` 指定模型；未配置时回落到确定性 `RuleAssessor`，接口不变 |
| 端到端 | ✅ | `internal/assess/e2e_test.go`：mock 模型经真实 HTTP 研判 → Engine 建票到模型指定的工作区 |
| 面板 | ✅ | 评估页显示 Decision 的 reason 码与工作区（截图 `artifacts/m2-eval.png`）|

测试里有一条值得记的：mock 默认只回答了 2 条规则，校验立刻报 `rule "data-request" evaluated 0 times` 并拒绝——**校验是真的在拦，不是装饰**。

---

## 13. M2–M6 并行交付（4 路小队）

| 里程碑 | 内容 | 状态 |
| --- | --- | --- |
| M2 交付闭环 | Artifact/Report、幂等回写、回执按来源会话组织 | ✅ |
| M4 定时与目标 | `internal/schedule`（cron/DST/跳过/重启安全）、`internal/goal`（轮次/连续阻塞阈值） | ✅ 包级完成，runtime 已接定时 |
| M5 提示词与搜索 | 三层提示词编辑器（乐观并发/冲突重载）、⌘K 跨区搜索 | ✅ |
| M6 接入 | 飞书/钉钉驱动、MCP 客户端、Skill 库、三平台打包 | ✅ 驱动与包级完成；真实凭据下的端到端未验证 |

**集成**：`cmd/zeokeel/extras.go` 统一装载技能/MCP/定时/目标；定时触发转换成与聊天消息同样的 Trigger；IM 消息经 `im.AddressedFor` 判定而非"有 @ 就算"。

**总验收**：21 个包全绿、**368 个测试**、零 cgo PASS、三平台产物各 11 MB。

---

## 14. 首次运行向导

| 项 | 状态 | 证据 |
| --- | --- | --- |
| 全新安装进向导而非空仪表盘 | ✅ | 空库 + 非 demo 时 `SetNeedsOnboarding(true)` |
| 四步：模型 / 工作区 / 来源（可选）/ 允许名单（可选） | ✅ | `view.OnboardingVM` + `ui/onboarding.go` |
| 必填未完成不能开始 | ✅ | `Ready` 由字段值派生；按钮不出现 |
| 保存即刻注册工作区 | ✅ | `Runtime.SaveOnboarding` → `Persistence.Register` |
| 无保存通道时明说 | ✅ | 显示"保存通道未接线（M2 接入）" |
| 测试 | ✅ | runtime 4 个 + ui 3 个用例（含"已完成安装不进向导"）|

---

## 15. 1.0-rc 收尾加固

| 项 | 状态 | 证据 |
| --- | --- | --- |
| 设置与提示词落盘 | ✅ | `store.Settings/SaveSettings` + `runtime.LoadSettings`；重启测试 |
| 无库时诚实标注 | ✅ | 页面显示"内存（未接数据库）" |
| 应用图标（三平台） | ✅ | `tools/genicon` 纯标准库生成 png/ico/icns，打进三个包 |
| 崩溃后队列里的工单被执行 | ✅ | `cmd/zeokeel/recovery_test.go` |
| 技能确实进系统提示 | ✅ | `internal/runtime/worker_test.go` 断言 mock 收到的 system 消息 |
| workflow 编排 | ✅ | `internal/workflow` + `-pipeline`，含重启恢复测试 |

---

## 16. 1.0 验收场景化（第 11 轮）

把 `RELEASE.md` A 组从"声明"变成"可执行场景"，每条一个测试名：

| RELEASE 条目 | 测试 |
| --- | --- |
| 一条请求走完全链路 | `TestEndToEndFromChatToReply` |
| 同事请求四要素 | `TestA2ColleagueRequestCarriesTheFourParts` |
| 自身无条件执行 | `TestA3OperatorRequestRunsWithoutApproval` |
| 敏感外发要人批 | `TestA4SensitiveOutboundEscalates` |
| 陌生人/机器人不触发 | `TestA6StrangersAndBotsDoNothing` |
| 重复投递只一个工单 | `TestA7RepeatedDeliveryCreatesOneTicket` |
| 并发两个工单 | `TestTwoTicketsRunConcurrently` |

**场景化当场抓到两个缺陷**：
1. 同事请求的待批单「敏感点」为空——等于没告诉人为什么停下来。已修为"总是给出理由"，并补上"可能外发到公开渠道"的识别。
2. （设计判断）自身请求即使敏感也不拦——这是用户明确要的"自己发的无条件执行"，写进测试防回归。
