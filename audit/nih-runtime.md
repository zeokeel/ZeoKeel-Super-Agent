# 审计：Agent Runtime 切片中的"不必要自研"（NIH）

- 审计对象：`/data/ZeoKeel-Super-Agent`（Go 1.27.3，`CGO_ENABLED=0` 全链路）
- 范围：`internal/agent`（含 `mockllm`）、`internal/runtime`、`internal/mcp`、`internal/assess`、`internal/workflow`、`internal/goal`、`internal/prompts`、`cmd/zeokeel`
- 方式：只读。用 read/grep/glob 逐行读生产代码，用 `go list -deps` / `go mod why` 确认依赖闭包，在 GOMODCACHE 里读依赖源码，`go doc` 确认标准库符号；在 `/tmp` 做了一次 `filepath.Glob` vs `doublestar` 的对照实验，并跑了一次 `CGO_ENABLED=0 go build -o /tmp/zeokeel-audit ./cmd/zeokeel`（exit 0）。未修改任何源文件。

**引用约定**

- 项目文件路径相对仓库根。
- `E = $(go env GOMODCACHE)/github.com/cloudwego/eino@v0.10.0-alpha.35`
- `S = $(go env GOMODCACHE)/github.com/modelcontextprotocol/go-sdk@v1.8.0`
- `G = /usr/local/go/src`（go1.27.3）
- 所有"标准库/依赖已提供"的替代品都验证过 pure Go：`doublestar`、`go-humanize`、`x/time/rate`、eino、go-sdk 在 `CGO_ENABLED=0` 构建下均可用（`go build` 通过；`go list -deps` 里的 `runtime/cgo` 只是按 cgo 门控的标准库桩，不构成链接依赖）。**不需要重新谈判零 cgo 规则。**

---

## 0. 结论速览

一句话：**runtime 层整体是"框架 + 领域胶水"，不是重写 SDK；真正的不必要自研集中在三处：`internal/agent/tools.go` 的六个工具、`internal/agent/session.go` 的悬空 tool_call 修补、以及 `internal/workflow` 这条只有 CLI 在用的持久化"假流水线"。此外有一批"写了但没接线"的代码让 README/CHANGELOG 的可靠性承诺在真实进程里不成立。**

按 24 项判定统计：

| 判定 | 数量 | 条目 |
| --- | --- | --- |
| SHOULD-USE-FRAMEWORK / STDLIB / DEPENDENCY | 5 | F2 F3 F4 F6 F23 |
| JUSTIFIED（自研是对的） | 8 | F8 F9 F10 F12 F17 F19 F20 F21 |
| LOW-PRIORITY | 5 | F5 F11 F14 F18 F22 |
| DUPLICATION | 2 | F13 F16 |
| DEAD | 4 | F1 F7 F15 F24 |

**Lead 追加问题的结论（详见 §3.5）**：eino v0.10.0-alpha.35 并不缺"异步作业 / 回合中投递 / checkpoint"的原语——`adk/backgroundtask`（job 生命周期 + 5s 前台转后台 hook）、`adk.TurnLoop`（`Push` + preempt + checkpoint-resume，即 steer/inject 的机制）、`adk.CheckPointStore`/`Runner.Resume`/`adk.Interrupt` 都在。真正缺失的只有"已读回执"这种一等 API 与投递**策略**。因此这些条目应记为"框架能力未采用 + 功能缺失"，而不是"不必要自研"；只有 read-receipt 与投递策略属于 MUST-SELF-BUILD（JUSTIFIED）。

**Top 5 发现（按重要性）**

1. **悬空工具调用的恢复逻辑没有接线（P0）**。`session.go:58-109` 的 `patchInterruptedToolCalls` 只被 `Runner.Resume` 调用，而 `Runner.Resume`（`agent.go:308`）在生产代码里没有任何调用者：崩溃重启后走的是 `worker.go:282` 的 `RunSession`（同一个 session id，见 `worker.go:257`），历史会从事件日志重放，但"有 tool_call 没有 tool_result"的历史不会被补——真实 provider 会拒。官方 `adk/middlewares/patchtoolcalls` 就是干这个的。
2. **`glob` 工具承诺 `**`，实现用的是 `filepath.Glob`（P1，S 工作量）**。`tools.go:124` 描述 `**/*.go`，`tools.go:135` 用 `filepath.Glob`（`**` 等同 `*`，不跨目录）。实测三层的 `a/b/c/z.go` 匹配不到；而 `bmatcuk/doublestar/v4` 已因 `eino/adk/filesystem` 在模块图里（`go mod why` 证），换过去一行代码。
3. **`read/write/edit/glob/grep` 整套工具在重写 eino 已有能力（P1/P2，M 工作量）**。项目自己的设计文档写明要用 `adk/prebuilt/deep`（`design/zeokeel-go-mygo-design.md:978`），代码却自建了 350 行工具。eino 的 `adk/middlewares/filesystem` 提供同名工具 + `ls` + 行号 read + `**` glob + 大结果落盘，允许用 `ToolConfig.Name/Desc` 保留现有工具名与中文描述。见 F4。
4. **`inside()` 只做词法越界检查，挡不住符号链接逃逸；标准库 `os.Root` 才是真约束（P1，S 工作量）**。`tools.go:294-308`；`os.Root` 在 Go 1.24+ 提供 `OpenRoot/ReadFile/WriteFile/Mkdir/Stat`（`G/os/root.go:82,102,108,149,253,264`），保证不越界且不允许绝对符号链接。
5. **`engine_react.go` 是死生产代码（P2，S 工作量）**：`Runner.Run`（`agent.go:263`）无生产调用者，只有 7 处测试调用点在用；CHANGELOG 宣称"修复 eino 默认工具检查器"（`CHANGELOG.md:36`）的那个 `bufferToolCallChecker` 只存在于这条死路径，ADK 生产路径没有用它。

**必须让 Lead 知道的正确性缺口（不属于"删自研"，但由自研代码引发）**

- `cmd/zeokeel/main.go:76` 配置 `Blocked: []string{"git push --force"}`，而 `tools.go:230` 用 `firstWord(cmdline)` 查表 → 多词条目永远匹配不到，`blockedCommands` 在默认配置下等于没有。（测试里用的是单词 `"git"`，`agent_test.go:144`，所以测试没暴露。）
- `bash` 超时会 `return res, fmt.Errorf(...)`（`tools.go:256-259`），`read` 越界会返回 error（`tools.go:64-66`），`edit` 命中多次会返回 error（`tools.go:110-111`）——eino `compose.ToolsNode.Invoke` 把工具 error 当整轮失败（`E/compose/tool_node.go:1347`；项目自己在 `mcp/tool.go:114-117` 注明了这一点）。这与设计 §5.5/§5.8 "工具级问题应当是模型可读的 observation" 相反：一次慢命令或一次差 1 行的 read 就能让整个 Ticket 失败。

---

## 1. 范围与覆盖

| 目录 | 生产文件 | 生产 LOC | 测试 LOC | 阅读程度 |
| --- | --- | --- | --- | --- |
| `internal/agent` | 10 | 1904 | 1162 | 全量逐行（10/10） |
| `internal/agent/mockllm` | 1 | 453 | 0 | 全量逐行 |
| `internal/runtime` | 11 | 3047 | 2834 | `worker.go`/`durable.go`/`extras.go`/`prompts.go`/`storage.go`/`reply.go` 全量；`runtime.go`/`pages.go`/`artifacts.go`/`onboarding.go` 读函数与关键段；`deliverables.go` 读签名 |
| `internal/mcp` | 5 | 1123 | 1431 | 全量逐行（5/5） |
| `internal/assess` | 3 | 405 | 287 | 全量逐行（3/3） |
| `internal/workflow` | 3 | 463 | 245 | 全量逐行（3/3） |
| `internal/goal` | 1 | 383 | 337 | 全量逐行 |
| `internal/prompts` | 2 | 274 | 93 | 全量逐行 |
| `cmd/zeokeel` | 7 | 1209 | 701 | 全量逐行（7/7） |
| **合计** | **44** | **9261** | **7090** | 全仓 ~16.4k LOC（含测试） |

方法说明与局限：

- 判定"能力已被提供"的三条硬标准：能在 GOMODCACHE 里指到符号，或能在标准库 GOROOT 里指到符号，或能在本仓库内指到另一处实现。仅"包名像"不算证据，所有 eino/go-sdk 结论都读了源码。
- 未做 git 历史考古（除 `git log --oneline` 确认当前 HEAD）；"生产未接线"的结论基于全仓 `grep -rn`（含 `internal/ui`、`internal/view`、`cmd`），不是基于测试覆盖。
- 未跑 `go vet`/测试套件（避免任何写操作），只跑了 `/tmp` 输出目录的 `go build` 与两个对照小程序。

---

## 2. 汇总表

| # | 区域 | 当前实现（file:line） | 涉及 LOC | 判定 | 建议替代 | Effort | Risk | Prio |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| F1 | `engine_react.go` + `Runner.Run` | `engine_react.go:20,82`；`agent.go:263-273` | ~126 | DEAD | 删除；7 处测试调用点改走 `RunSession` | S | 中（测试语义） | P2 |
| F2 | `glob` 工具 | `tools.go:123-149`（`:135 filepath.Glob`） | ~27 | SHOULD-USE-DEPENDENCY | `bmatcuk/doublestar/v4` `FilepathGlob` | S | 低 | P1 |
| F3 | `inside()` 工作区约束 | `tools.go:294-309` | ~16 | SHOULD-USE-STDLIB | `os.Root`（+ `filepath.IsLocal`） | S | 低 | P1 |
| F4 | `read/write/edit/glob/grep` 工具集 | `tools.go:44-175` | ~200 | SHOULD-USE-FRAMEWORK | `E/adk/middlewares/filesystem` + 本地 `filesystem.Backend` | M | 中（面板契约/工具名） | P2 |
| F5 | `bash` 工具 | `tools.go:222-269` | ~55 | LOW-PRIORITY | `filesystem.Shell` 适配 + 修正 timeout/exit 语义 | M | 中 | P2 |
| F6 | 悬空 tool_call 修补 + `Resume` | `session.go:58-109`；`agent.go:308` | ~57 | SHOULD-USE-FRAMEWORK | `E/adk/middlewares/patchtoolcalls` + 生产接线 | S/M | 中 | **P0** |
| F7 | 会话统计/事件读取 | `session.go:125-199`；`agent.go:334-348` | ~92 | DEAD（test-only） | 删除或接到面板 | S | 低 | P2 |
| F8 | 事件日志半行修复 | `session.go:18-42` | ~25 | JUSTIFIED | 保留；加格式 conformance 测试 | — | — | — |
| F9 | 压缩/截断/子智能体 | `compact.go:30`；`truncate.go:48`；`subagent.go:51` | ~267 | JUSTIFIED | 保留（官方 middleware） | — | — | — |
| F10 | 事件流/回调/流分叉 | `recorder.go:36-108`；`stream.go:33` | ~213 | JUSTIFIED | 保留（官方 callback + `StreamReader.Copy`） | — | — | — |
| F11 | `mockllm` | `mockllm.go:151-171` | ~21 可省 | LOW-PRIORITY | 标准库 `httptest.NewServer` | S | 低 | P2 |
| F12 | MCP 协议/传输/错误映射 | `conn.go:88,117,216`；`mcp.go:344,353`；`errors.go:58` | 主体 | JUSTIFIED | 保留（官方 SDK） | — | — | — |
| F13 | MCP 结果截断 | `result.go:79-136` | ~100 | DUPLICATION | 交给 `reduction`（`truncate.go:48`），MCP 层只留上限保护 | S | 低 | P2 |
| F14 | MCP 重连退避 | `conn.go:158-181` | ~24 | LOW-PRIORITY | `golang.org/x/time/rate`（已在图里） | S | 低 | P2 |
| F15 | MCP 管理 API | `mcp.go:234,257,269,292` | ~120 | DEAD（未接线） | 接到面板或删除 | S | 低 | P2 |
| F16 | runtime 设置解析 ×4 | `durable.go:81-143` | ~60 | DUPLICATION | 一个泛型 `settingInt(key,def,min,max)` | S | 低 | P2 |
| F17 | 自然语言定时解析 | `extras.go:64-123` | ~60 | JUSTIFIED | 保留 | — | — | — |
| F18 | `internal/workflow` | `workflow.go:206-285`；`sqlite.go:24` | 463 | LOW-PRIORITY（含 DEAD 子集） | 删死代码；需要编排时用 `compose.Graph`+`WithCheckPointStore` | S/M | 中 | P2 |
| F19 | `internal/goal` FSM | `goal.go:284-381` | 383 | JUSTIFIED | 保留 | — | — | — |
| F20 | `internal/prompts` 组装/插值 | `prompts.go:105-144` | 274 | JUSTIFIED | 保留（KV-cache 字节稳定） | — | — | — |
| F21 | `internal/assess` 校验/解析 | `assess.go:144-266` | ~120 | JUSTIFIED | 保留；可选 ResponseFormat | — | — | — |
| F22 | `cmd/zeokeel` 小工具 | `pipeline.go:105-122`；`main.go:76` | ~20 | LOW-PRIORITY | `strings.IndexByte`/`strings.Cut`；修 blocked 匹配 | S | 低 | P1 |
| F23 | 字节大小/时长/截断 helper 重复 | `storage.go:81`；`artifacts.go:216`；`pages.go:365,526` | ~50 | SHOULD-USE-DEPENDENCY | `dustin/go-humanize`（已在图里）+ 合并 | S | 低 | P2 |
| F24 | 未接线导出 API / 占位符 | 见 §7 | ~150 | DEAD | 删除或接线 | S | 低 | P2 |

---

## 3. 九问直答

**Q1：`engine_react.go` vs `engine_adk.go`，谁生产、谁死？`compact.go`/`truncate.go`、`subagent.go` 是否重复官方 middleware？**

- 生产路径是 ADK：`worker.go:282 RunSession` → `agent.go:300 runADK`（`engine_adk.go:19-62`），`adk.NewRunner` + 官方 `session.FileStore`（`agent.go:246`、`engine_adk.go:38-43`）。
- `engine_react.go` 整文件是死生产代码：`Run`（`agent.go:263`）在全仓无生产调用者；仅 `agent_test.go:81,171,193`、`usage_test.go:58`、`session_test.go:281`、`internal/mcp/worker_integration_test.go:214,337` 使用。注释里给它的存在理由"Router 的一次性评估"已经不成立——Router/assess 直接用 eino 模型（`internal/assess/eino.go:26-43`），不经过 `runner.Run`。见 F1。
- `compact.go` **正确使用**官方 `summarization`（`compact.go:30`，`E/adk/middlewares/summarization/summarization.go:296`）；`truncate.go` **正确使用**官方 `reduction`（`truncate.go:48`，`E/adk/middlewares/reduction/reduction.go:358`）；`subagent.go` **正确使用**官方 `subagent`（`subagent.go:51`，`E/adk/middlewares/subagent/middleware.go:148`）。这三处不是 NIH，是要求的胶水（自定义 Backend / 中文摘要指令 / 工具白名单）。见 §5。

**Q2：tools.go 是否重写 eino 工具设施？glob 是否该用 doublestar？schema 是否手搓？**

- Tool **schema 不是手搓**：`utils.InferTool` + struct tag `jsonschema:"description=..."`（`tools.go:34,44,73,92,123,153`，`runtime/artifacts.go:25,49,83,117`；`E/components/tool/utils`）。MCP 侧用 `eino-contrib/jsonschema` 把 server 的 InputSchema 转成 `schema.ToolInfo`（`mcp/tool.go:42-52`）。这一点**正确**。
- 工具**实现**是自建：`read/write/edit/glob/grep` 在 eino 里有现成 middleware（`E/adk/middlewares/filesystem/filesystem.go:49-56` 六个工具名、`:529 New`），`bash` 有 `filesystem.Shell` 接口（`E/adk/filesystem/backend.go:388`）。项目自己的设计文档写明"编码 Agent 脚手架：文件读写/编辑、shell 后台任务、plan/todo、skill、子智能体 —— 现成（eino `prebuilt/deep`）…… 省掉最多自研量的一块"（`design/zeokeel-go-mygo-design.md:978`），并与 `prebuilt/deep` 的组装逻辑对得上（`E/adk/prebuilt/deep/deep.go:243,421-477`）。实现没有走这条路。见 F4。
- `glob` 应换 `doublestar`：已实测 `filepath.Glob("**/*.go")` 漏掉两层以下文件，`doublestar.FilepathGlob` 全中；doublestar 已在模块图（`go mod why` 链：`internal/agent` → `eino/adk/filesystem` → `doublestar`；符号 `E/../doublestar/v4@v4.10.0/utils.go:87`）。见 F2。
- `inside()` 该换 `os.Root`：见 F3。

**Q3：Session/stream/recorder 是否重写了框架保证？**

- 事件存储：**用的是官方 `session.FileStore`**（`agent.go:246`）。这是正确用法。
- Stream 重组：**用的是 eino 自己的 `StreamReader.Copy(2)`**（`stream.go:33`），没有手写 SSE 解析。正确。
- Recorder：把 eino `callbacks` 的 tool/model 回调翻成面板事件（`recorder.go:36-108`），属于"框架不管的 UI 事件层"，JUSTIFIED。
- 中断恢复：**这部分重写了官方能力**——`unpairedToolCalls` + `patchInterruptedToolCalls`（`session.go:58-109`）等价于 `adk/middlewares/patchtoolcalls`（`E/adk/middlewares/patchtoolcalls/patchtoolcalls.go:119`，含 `PatchedToolResultGenerator` 自定义文案 `:70-93`、`RemoveOrphanResults`、`Strict`）。且当前版本**没有接进生产**（`Runner.Resume` 无生产调用者；重启走 `RunSession`），见 F6。
- 半行日志修复（`session.go:18-42`）：框架没有提供，FileStore 明确把无换行末行判为损坏（`E/adk/session/file_store.go:277`），所以这是**必要的**兼容层，但强依赖 `<id>\t<kind>\t<data>\n` 与 `url.PathEscape` 的落盘格式（`file_store.go:44-52`、`session.go:28`），属于脆弱胶水，见 F8。
- 框架还提供了 `Runner.Resume` + `CheckPointStore`（`E/adk/runner.go:86,200`）用于 interrupt 级恢复；本项目没用它。对于"进程被杀"场景，checkpoint 存的是执行中状态而非 durable 事实，设计 §5.8 选择"事件日志 + recovery prompt"是合理取舍，JUSTIFIED；但那条 recovery 路径要接线（F6）。

**Q4：MCP 是否全部走官方 SDK？**

是。初始化握手/协商 `client.Connect`（`conn.go:88`）、工具分页 `sess.Tools` 迭代器（`conn.go:117`，注释明确不手写 cursor）、调用 `sess.CallTool`（`conn.go:216`）、探活 `sess.Ping`（`conn.go:251`）、stdio/HTTP 传输 `CommandTransport`/`StreamableClientTransport`（`mcp.go:344,353`）、错误类型 `jsonrpc.Error` + `mcpsdk.ErrConnectionClosed`（`errors.go:71,79`）。没有手写 JSON-RPC/分帧/SSE。见 F12。
例外的小自研：`limitResult` 截断（`result.go:79`）与 `reduction` 重复（F13）；`restart` 退避（`conn.go:158`）可用已在图里的 `x/time/rate`（F14）；`Manager.Restart/Refresh/Ping/Health` 写了但没接面板（F15）。

**Q5：runtime 的 durable queue/effects/retry 是不是通用重造？**

- 主体是 `internal/effects`（649 LOC，SQLite 行即队列：`coalescing/retry/backoff/failure threshold` 全在 SQLite）与 `internal/outbox`。这是**领域特定的 durable outbox**（design §7.2、附录 A），不是通用队列重造：`x/time/rate` 是 token bucket，不能提供"崩溃后行还在"；eino checkpoint 面向图内 interrupt，不面向跨进程副作用投递。**JUSTIFIED**。
- `runtime/durable.go` 自身只是把 `EffectLaunch` 接到 `runTicketOnce`（`durable.go:30-46`），JUSTIFIED。
- 真·通用重造只有 `mcp/conn.go:158-181` 的退避（F14），以及 `durable.go:81-143` 四份重复的"读设置→Atoi→clamp"（F16，内部重复）。

**Q6：`internal/workflow` 是什么？`internal/goal` 呢？**

- `workflow` 是**线性步骤执行器 + 可选 SQLite 存储**，不是 DAG、没有 fan-out/并发（`workflow.go:206-285` 是 `for _, s := range d.Steps`）。生产只被 `cmd/zeokeel/pipeline.go:43-79` 的 `-pipeline` 头less 模式使用，而且用的是 **MemoryStore**（`pipeline.go:55`）；`NewSQLiteStore` 只在 `sqlite_test.go:18,49` 出现，`Resume` 只在 `workflow_test.go:112`。即"可恢复/落库"的承诺在应用里根本没启用（`workflow.go:1-6`、`sqlite.go:14-16` 的注释与实现相反）。另设计 §5.10 说 workflow 是"子智能体的脚本化 fan-out"（`design/...:1123`），实现是串行 CLI，属能力错位。判定：LOW-PRIORITY（含 DEAD 子集），见 F18。
- `goal` 是 SQLite 上的领域 FSM：revision CAS（`goal.go:327-347,351-381`）、同一阻塞条件连续 N 轮才 blocked（`:284-322`）、轮次硬上限。没有框架能替它，JUSTIFIED（F19）。

**Q7：prompts 手搓 `{{}}` 插值 vs `text/template`？byte-stable 前缀是否足以justify？**

- 是。两条硬约束：(a) KV-cache 要求（`internal/agent/cache_test.go:18-68` 断言跨轮 system prompt / tool 顺序 / 消息前缀字节不变；`design/prompts.md` §八）；(b) prompt 编辑器按 **section** 覆盖，未知变量必须原样显示成 `{{workspace}}` 而不是空白或报错（`prompts.go:102-104` 注释与 `:118-144` 实现）。`text/template` 引入 `{{if}}/{{range}}` 语义、未知 key 处理与额外空白风险，且没有任何收益；eino 也提供了模板组件（`E/components/prompt/chat_template.go:42`，FString/GoTemplate/Jinja2），但它返回 `[]*schema.Message`、不支持 section 覆盖。**JUSTIFIED**（F20）。
- `defaults.go` 与 `design/prompts.md`：逐行对照过，`design/prompts.md:28-46` 只是 section 清单表格，正文只在 `defaults.go`；不是复制粘贴，属于"文档记录结构 + 代码唯一事实来源"。**无重复**。
- 小问题：`Set.Add/Remove/Names/Get`（`prompts.go:46,68,79,88`）只有测试用；`New` 与 `resort` 抄了同一段比较器（`:36-41` vs `:58-64`）。

**Q8：token/usage 计费与上下文预算估算是否该由 provider/库给精确值？**

- 用量：取自 eino callback 的 `model.CallbackOutput.TokenUsage`（`recorder.go:72-107`），流式在 `OnEndWithStreamOutput` 里读最后一个 chunk（覆盖 OpenAI 兼容端的 `include_usage`），非流式摘要调用走 `OnEnd`；数据字段包含 cached/reasoning（`agent.go:472-490`）。**这是 provider 给的精确值，不是估算。**
- 会话级持久统计：从 assistant 消息的 `ResponseMeta.Usage` 累计，span 作 fallback（`session.go:151-199`）。做法合理（面板要跨重启出数），但当前**没接到面板**（F7）。
- 上下文预算：完全交给 `summarization` 的默认 token counter（`TokenCounter` 可选，默认"最后一条 assistant 的 total + 增量按 ~4 chars/token"：`E/adk/middlewares/summarization/summarization.go:485-519`），项目只传两个阈值（`compact.go:32-36`）。**没有自研 tokenizer**，正确。无 finding。

**Q9：死代码 / 死依赖 / 重复 helper？**

- 死代码/未接线：见 §7（`Runner.Run`、`SessionStats/LoadEvents`、`Resume`、MCP `Restart/Refresh/Ping/Health`、`workflow` 持久层、`prompts.Set` 变体、`WorkerConfig.CommandTimeout/ModelTimeout`、`clampForDisplay`、5 处 `var _ = ...` 等）。
- 死依赖：**没有**。`go.mod` 直接依赖全部在用；间接依赖都是传递必需。反过来，`doublestar`、`go-humanize`、`x/time/rate`、`adk/prebuilt/deep`、`patchtoolcalls` 是"已在图里但没用上"的现成能力（机会，不是负担）。
- 重复 helper：见 §6（截断 6 份、字节大小 2 份、时长 2+3 份、文件名清洗 3 份、JSON 抽取 1 份手写、设置解析 4 份）。

### 3.5 eino 是否能直接提供异步作业 / 回合中投递（Lead 追加核查）

背景：用户提供的《Super-agent 设计探索》§4.1 要求 full-duplex async ReAct loop：昂贵工具调用变成 `start()/stop()`（返回 "Started"），后台结果作为新事件注入，循环不等；design ADR-2/§5.2/§5.6 声称这些语义"必须自研"。核查 `cloudwego/eino v0.10.0-alpha.35` 的结果如下（全部为源码证据）：

| 能力 | eino 是否提供 | 关键符号（GOMODCACHE 路径以 E 开头） | 对判定的影响 |
| --- | --- | --- | --- |
| (a) 后台/异步工具执行、job 生命周期 | **提供脚手架**（宿主实现 backend/store） | `adk/backgroundtask`：`Manager`/`New`（`E/adk/backgroundtask/manager.go:195,215`）、`TaskStore`/`ExecutorRegistry`（`store.go`、`executor.go`）、租约过期策略（`types.go:29-35`）、取消（`manager.go` RequestCancelOption）、`NotifyParent`（`executor.go:265`）、`NotificationWriter.EnqueueTaskNotification`（`store.go:172`）、`TaskCreatedSessionEventSender`（`session_event.go:29-34`，写 `x.eino.background_task.created` 会话事件）；`middlewares/backgroundtask` 注入 `task_output`/`task_stop`（`E/adk/middlewares/backgroundtask/middleware.go:42,104-131`）；`backgroundtask/shell.NewRegistration` 提供 Start/Recover 适配（`E/adk/backgroundtask/shell/shell.go:59`） | "job 控制"**不需要自研**。本项目连 jobs 能力都没实现（design §5.6 v1 未落地），所以这是**功能缺失、框架有脚手架**，不是 NIH。仍须宿主实现 `filesystem.Shell`/`TaskStore`（eino 只给接口，没有本地 shell 实现） |
| (a2) 前台 5s 转后台 | **直接提供** | `filesystem.MiddlewareConfig.Background` + `ForegroundTimeoutMs`/`ShouldAutoBackground`（`E/adk/middlewares/filesystem/filesystem.go:141-144,316-319`）；`backgroundtask.ForegroundCandidate`（`E/adk/backgroundtask/foreground.go:25`）；`LocalBackgroundConfig`（`filesystem.go:105-130`） | design §5.6 的"5s 前台转后台"是 eino 现成 policy hook，自研这套策略会是 NIH |
| (b) 向运行中的回合注入消息（steer/inject/followup） | **提供机制**（策略留给宿主） | `adk.TurnLoop`：`NewTurnLoop`（`E/adk/turn_loop.go:370`）、`Run`/`Wait`（`:408,418`）；`Push(item, opts...) (bool, <-chan struct{})`（`E/adk/turn_loop_preempt.go:181`）——未启动时缓冲、运行中投递；`WithPreempt(safePoint)`/`WithPreemptTimeout`/`WithPreemptDelay`/`WithPushStrategy`（`:82,101,123,148`）在安全点打断当前 turn 处理新输入（= "next step 生效的 steer"）；`GenInput` 决定"现在消费 vs 留到下一轮"（`turn_loop.go:59-64`）；`Stop` 优雅/立即/超时（`turn_loop_stop.go:42,58,74,90,360`）；`TurnLoopExitState.TakeLateItems()` 回收晚到项 | **投递机制不必自研**；"谁可以 steer / nextStep vs nextTurn 队列 / 已读回执"仍是领域策略（JUSTIFIED self-build）。design §5.1:979 "会话/投递语义必须自研" 对 alpha.35 已不准确。另：普通 `Runner.Run` 路径下同一 session 是互斥的（`E/adk/session_admission.go:31-47`，并发 `Run` 返回 `ErrSessionBusy`），所以普通 `Runner.Run` 没有投递入口；要么换成 TurnLoop，要么自己在 middleware 里轮询队列（后者就是 NIH） |
| (c) checkpoint / interrupt-resume | **提供接口与机制，不提供持久实现** | `compose.CheckPointStore`（`E/compose/checkpoint.go:53`）与 `WithCheckPointStore`（`:58`）、`adk.CheckPointStore` 别名（`E/adk/runner.go:86`）；接口仅 `Get/Set`（`E/internal/core/interrupt.go:36-39`，可选 `Delete` `:48-50`）；`adk.Interrupt/StatefulInterrupt`（`E/adk/interrupt.go:93,128`）、`ResumeInfo/InterruptInfo`（`:33,49`）、`Runner.Resume/ResumeWithParams`（`E/adk/runner.go:200,223`）、`ResumeParams.Targets`（`:148-155`）；TurnLoop 自动 checkpoint + `GenResume` 选择 Resume/StartNewTurn（`turn_loop.go:67-80,104-128`） | "机制"可用；**durable store 是实现者的事**（2 个方法，落到现有 SQLite 即可）。本项目用"事件日志 + recovery prompt"（design §5.8）是另一种合理选择，但当前未接线（F6）。若要"回合中 interrupt 后精确续跑"，应改用 `Runner.Resume` + 自实现 `CheckPointStore` |
| (d) 回合结束回调 / 已读回执 | **部分提供** | 回合边界：SessionEvent 带 `TurnID`（`E/adk/session.go:200-204`）、`session.status_running/idle`（`:218-219`）、`StopReasonEndTurn`（内部判定 `isCommittedIdleEvent` `:2007-2013`），Runner 写入（`E/adk/runner.go:479,1263`）；`WithTimelineEvents()` 把 live SessionEvent 透出到 AgentEvent 流（`E/adk/call_option.go:71-77`）；`WithCallbacks` 提供 agent 级 OnStart/OnEnd（`:103-107`）。**"已读回执"没有一等 API**：`ModelContextEvent` 只记 tool infos（`session.go:304-307`），没有 per-message read 标记 | turn-end 可观测（idle 事件 + TurnID + timeline），**这一半不必自研**；read-receipt 确实缺失，自建薄层 JUSTIFIED（在 `GenInput`/`BeforeModelRewriteState`/事件观察处记录"哪些消息进入了本轮模型输入"） |

**结论（对 NIH 判定的影响）**：

- 对 (a)(a2)(b)(c) 四项，**eino 已经提供了原语**；如果本项目将来手写一套"异步 job 调度 / 回合中注入 / checkpoint 存储机制"，那才是 NIH。当前的真实状态是**没实现**（不是自研替代），所以这些条目不应记为 "unnecessary self-built"，而应记为"框架能力未采用 + 功能缺失"。
- 只有 (d) 的 **read-receipt**（以及"谁可以 steer / 投递到哪一级"的领域策略）是框架确实没有的，**必须自研**——这正是 design §5.2 里合理的部分。
- 因此 design §5.1:979 的"会话/投递语义必须自研"应改写为"投递机制用 eino `TurnLoop`，自研的是投递策略与已读回执"；这与本报告 §4 中"ADK 生产路径没有 TurnLoop、因此没有 steer/inject"的现状一致。

---

## 4. 详细发现

### F1 `engine_react.go` + `Runner.Run` = 死生产代码（DEAD）

**现状**：`runReact`（`engine_react.go:20-61`）用 eino `flow/agent/react` 跑无会话循环；`Runner.Run`（`agent.go:263-273`）是唯一入口；`bufferToolCallChecker`（`engine_react.go:82-98`）是注释强调"不允许回退"的 eino 默认检查器修复。

**证据**：
- 生产调用者为 0：全仓 `grep -rn "\.Run(context\|\.Run(ctx"` 在 `internal/`、`cmd/`（排除测试、`exec.Cmd.Run`）无命中；`RunSession` 才是 Worker 路径（`runtime/worker.go:282`）。
- 注释里的保留理由"Router 的一次性评估"不成立：Router 走 `external assess`（`internal/assess/eino.go:26-43`、`cmd/zeokeel/assess.go:28-41`），不碰 `agent.Run`。
- 测试依赖：`agent_test.go:81,171,193`、`usage_test.go:58`、`session_test.go:281`、`internal/mcp/worker_integration_test.go:214,337`。
- `CHANGELOG.md:36` "修复 eino 默认工具检查器静默吞 tool_call 的坑"在 ADK 生产路径上没有对应代码：`newADKAgent`（`engine_adk.go:90-119`）没有设 `StreamToolCallChecker` 之类的东西，那条修复只作用于 `react.AgentConfig`（`engine_react.go:65-74`）。

**为什么算不必要**：同仓已有 `runADK` 覆盖同一职责且是生产路径；保留第二条循环意味着两套工具/事件语义要同步维护，而它服务于一个已不存在的调用方。

**替代/迁移**：删除 `engine_react.go` 与 `Runner.Run`；把 7 处测试调用点改成 `RunSession`（需要 `SessionDir`，测试里 `t.TempDir()` 就够）。若确实想要"不落盘的 one-shot"，`runADK` + `session.InMemoryStore`（`E/adk/session/in_memory_store.go`）也能表达，不必回到 `react`。

**风险**：测试语义变化（事件序列、usage 断言）；`checker_test.go` 整文件要删/改写。Effort S。

**价值**：-126 LOC，少一条需要维护的 eino 集成路径；同时让 CHANGELOG 的措辞不再误导（要么删掉那条、要么把检查器修复搬到 ADK 路径并验证）。

### F2 `glob` 工具：描述承诺 `**`，实现不支持；doublestar 已在图里（SHOULD-USE-DEPENDENCY）

**现状**：`utils.InferTool("glob", "…**/*.go…", ...)`（`tools.go:123-125`），实现 `filepath.Glob(filepath.Join(root, pattern))`（`:135`），结果 `sort.Strings`（`:139`）后截断 200（`:140`）。

**证据**：
- `filepath.Glob` 的 `**` 等价于 `*`，不跨目录。实测（/tmp 三层目录）：`filepath.Glob("/tmp/globtest/**/*.go")` 只返回 `a/x.go`；`doublestar.FilepathGlob` 返回根 + 三层全部 4 个文件。
- doublestar 已在模块图：`go mod why -m github.com/bmatcuk/doublestar/v4` → `internal/agent` → `cloudwego/eino/adk/filesystem` → `doublestar`（因为 `internal/agent/truncate.go` import 了 `adk/filesystem`）。**零新增依赖**。
- 描述还说 "newest first"，代码是按字母序（`sort.Strings`），且 200 上限被静默截断（模型不知道还有更多）——手写实现与描述已经漂移。

**替代**：`doublestar.FilepathGlob`（`$(go env GOMODCACHE)/github.com/bmatcuk/doublestar/v4@v4.10.0/utils.go:87`；`Match` 见 `match.go:52`）。若采纳 F4，官方 glob 工具用的就是 doublestar（`E/adk/filesystem/backend_inmemory.go:29,393,624` 是参照实现）。

**迁移草图**：

```go
matches, err := doublestar.FilepathGlob(filepath.Join(root, pattern))
// 排序：按 os.Stat 的 ModTime 降序（兑现 "newest first"），或在描述里改成 "sorted"。
```

**风险**：`**/*.go` 在 doublestar 里会匹配根目录文件（零层目录），与部分模型预期一致；Windows 分隔符用 `doublestar.FilepathGlob` 处理。Effort S，无 cgo 影响。

### F3 `inside()` 只做词法约束，符号链接可逃逸（SHOULD-USE-STDLIB）

**现状**：`inside(root, p)`（`tools.go:294-309`）拒绝绝对路径与 `..` 前缀，然后 `filepath.Join(root, clean)` + `filepath.Rel` 复查。`read`/`write`/`edit` 后续用 `os.ReadFile/os.WriteFile`（`:52,84,100,114`）。

**问题**：
- 纯词法。工作区内一个指向 `/etc/passwd` 的符号链接（模型完全可以用 `bash ln -s` 造出来，或工作区本来就带）会让 `inside` 判定合法，随后 `os.ReadFile` 跟随链接读到工作区外。注释（`:278-289`）只讨论了"拒绝而不是 clamp"，没有讨论链接。
- 同时，`toolOutputBackend.Write`（`truncate.go:27-39`）也复用了 `inside`，同样风险。

**替代（标准库）**：`os.Root`（Go 1.24+，本仓 1.27.3）：
- `os.OpenRoot(dir)`（`G/os/root.go:82`）→ `Root.ReadFile/WriteFile/Mkdir/MkdirAll/Stat/Open/Create/OpenFile`（`:253,264,149,161,200,102,108,117`）；
- 文档明确：方法只能访问 root 之下，任何越界分量返回错误；会跟随符号链接但**不允许链接指向 root 外**（`go doc os.Root`）。
- 若只想替换词法部分并保留现有调用形状：`filepath.IsLocal`（`G/path/filepath/path.go:73`）就是"非绝对、不越界、非空"的标准实现，可替掉 `:298-307` 的 10 行。

**迁移草图**：`workspaceTools` 里 `rootHandle, err := os.OpenRoot(root)`，工具闭包用 `rootHandle.ReadFile(in.Path)` / `WriteFile` / `MkdirAll`；目录遍历类工具（glob/grep）继续用 root 字符串 + `filepath.Rel` 检查。`os.Root` 在 Windows 上额外禁止保留设备名（`NUL`、`COM1`），对三平台发布是加分项。

**风险**：`os.Root` 持有一个目录 fd；工具生命周期内需要 Close（可在 `Runner` 里持有、或每工具调用打开关闭）。跨平台注意 `Root.Chmod/Chown/Chtimes` 在 Unix 上有 TOCTOU（`go doc os.Root` 已列），本项目不用这三个方法。Effort S。

### F4 `read/write/edit/glob/grep` 整套工具在重写 eino 官方工具（SHOULD-USE-FRAMEWORK）

**现状**：`workspaceTools`（`tools.go:21-175`）自建 5 个文件/搜索工具 + bash，返回自定义 `ToolResult{Output,ExitCode,DiffAdd,DiffDel}`（`:215-220`）。设计文档要求用 eino 脚手架（`design/zeokeel-go-mygo-design.md:978`），代码没走。

**eino 现有能力**：
- 工具集合与名称：`ls/read_file/write_file/edit_file/glob/grep/execute`（`E/adk/middlewares/filesystem/filesystem.go:49-56`），构造函数 `filesystem.New(ctx, &MiddlewareConfig{...})`（`filesystem.go:529`）。
- `ToolConfig{Name, Desc, CustomTool, Disable}` 允许**保留现有工具名与中文描述**（`filesystem.go:62-88`），因此 `defaults.go:17-18` 的"read/grep/glob"提示词、`worker.go:366,372` 的子智能体白名单、`truncate.go:52,54` 的 `ReadFileToolName: "read"`/`TruncExcludeTools` 都能不变。
- read_file 默认 Limit=2000 + 行号输出 + "空文件/offset 越界"是文本而非 error（`filesystem.go:834-893`），正好修掉 F5 里"read 越界让整轮失败"的问题。
- 大工具结果落盘默认开启；**若与已有 `reduction` 并用必须设 `WithoutLargeToolResultOffloading: true`**，否则同一结果被落盘两次（`filesystem.go:190-192` 字段、`:289` 启用点）。
- `Backend` 接口 6 个方法：`LsInfo/Read/GrepRaw/GlobInfo/Write/Edit`（`E/adk/filesystem/backend.go:308`）；仓库自带实现只有 InMemory（`backend_inmemory.go`）与 automemory 的只读 LocalBackend（`E/adk/middlewares/automemory/local_backend.go:36`，仅 Read+GlobInfo，不满足完整接口）。所以要自己写一个 ~120 行的本地 Backend（用 `os.Root` + doublestar + `regexp`/`WalkDir` 实现 grep）。
- `prebuilt/deep` 已经把"filesystem middleware + subagent + todo + background shell"组装好（`E/adk/prebuilt/deep/deep.go:243 NewTyped`、`:421-477 buildTypedBuiltinAgentMiddlewares`），是一条更彻底的迁移路线，但会一并带走 instruction/todo/background 的产品决策，风险更大。

**为什么算不必要**：工具注册、schema、描述模板、glob/grep 语义、结果截断/落盘都是框架现成能力；自研版本已经出现与描述不一致（F2 "newest first"）和错误语义不一致（F5 error vs observation）的漂移，维护成本只会上升。

**迁移草图（渐进，不一次性替换）**：
1. 先做 F2/F3/F5（不改变工具形状）。
2. 写 `localBackend`（root + os.Root + doublestar + Go regexp），用 `filesystem.New` 把 read/write/edit/glob/grep 换成官方工具，`Name` 设为现有名字、`Desc` 设为 `defaults.go` 对应中文描述；`ls` 可以 `Disable: true`（或作为新能力开放）。
3. bash 单独处理（F5），先保持自定义。
4. 删除 `tools.go` 对应 ~200 行与对应测试（`tools_test.go` 若存在需改写；当前工具行为由 agent 测试间接覆盖）。

**风险 / 不变量**：
- 面板契约其实**不受影响**：`runtime` 的 `workerEvent` 不转发 `ExitCode/Duration/DiffAdd/DiffDel`（`runtime/worker.go:100-116`），`workerLine` 把 `ExitCode` 硬编码成 0（`runtime/runtime.go:346`）；`view.Line` 的 `Duration/DiffAdd/DiffDel`（`internal/view/vm.go:405-411`）也从未被赋值。也就是说 `ToolResult` 的 `exit_code/diff_add/diff_del`（`tools.go:215-220`）只有模型能看到，面板看不到——迁移到官方工具不会丢面板信息，反而顺手修掉"失败命令也显示 exit 0"与"diff chip 永不出现"（`internal/ui/stream.go:171-177`）的问题。这条也归入 F24。
- 工具名/描述/白名单/reduction 名称必须保留（见上）。
- 官方 read_file 输出带行号，模型看到的内容格式变；对已写死的测试断言（如 `agent_test.go` 里对 `Output` 的匹配）需要更新。
- 官方 tools 错误语义不同：模型拿到的失败文本更结构化，但 `read` 越界不再中断整轮（这是改进）。
Effort M，风险中，P2（在 F2/F3/F5 之后）。

### F5 `bash` 工具：自建 + 错误语义与设计相反（LOW-PRIORITY，含 P1 语义 bug）

**现状**：`runBash`（`tools.go:222-269`）用 `exec.CommandContext(ctx, "sh", "-c", cmdline)`（`:243`），`CombinedOutput`（`:254`）后 `truncate` 到 8000 字节（`:255,271`）。

**问题**：
1. 超时返回 **error**（`:256-259`）：`compose.ToolsNode` 会把工具 error 当整轮失败（`E/compose/tool_node.go:1347`；项目自己在 `mcp/tool.go:114-117` 写了同样的判断）。设计 §5.5 要求超时是结果（写 124 退出码给模型看），当前实现会把"命令慢"升级成"Ticket 失败"。
2. `blockedCommands` 实际失效：`:230` 用 `firstWord(cmdline)` 查 map；`cmd/zeokeel/main.go:76` 配置的是 `"git push --force"`（多词），永远不命中。测试用单词 `"git"`（`agent_test.go:144`）所以没暴露。
3. 平台：`sh -c` 与 `grep` 二进制的依赖（`:243`、`:159-165`）与 README 宣称的 Windows 安装包冲突（`README.md:31-40` 列了三平台包；`README.md:125` 明确有 Windows zip）；Windows 上 `sh`/`grep` 默认不存在。
4. 输出截断成纯文本（`:271`）与官方 `execute` 的 `ExecuteResponse{Output,ExitCode,TimedOut,Truncated}`（`E/adk/filesystem/backend.go` 的 `ExecuteResponse`）相比，丢失了结构化信号。更实际的问题是：`exit_code` 只进了模型看到的 JSON，`runtime` 并不转发它（`runtime/runtime.go:346` 硬编码 0），所以面板上失败的命令也会显示 `exit 0`（`internal/ui/stream.go:175-177`）。

**替代**：
- 结构上：实现 `filesystem.Shell` 接口的 `Execute(ctx, *ExecuteRequest) (*ExecuteResponse, error)`（`E/adk/filesystem/backend.go:388` + `ExecuteRequest/ExecuteResponse`），交给 `filesystem.New` 的 `Shell` 字段，由官方 execute 工具负责"非零退出码是结果、超时写 TimedOut"（`E/adk/middlewares/filesystem/filesystem.go:1442-1450`）。若还要"前台 5 秒转后台"（design §5.6），eino 的 `adk/backgroundtask/local` + `middlewares/backgroundtask` 是现成路径（`E/adk/backgroundtask/local/local.go:103`、`E/adk/middlewares/filesystem/filesystem.go:94-142` BackgroundConfig）。
- 最小修复（不改结构）：超时返回 `ToolResult{ExitCode:124, Output:"timed out after …"}`, `nil` error；blocked 匹配改成对规范化后的整行做首词/前缀判断；把 `grep` 工具改成 Go 实现（`WalkDir` + `regexp`）以脱离外部二进制。
- `grep` 工具本身用 `runBash("grep " + shellQuote(args))`（`:153-170`）是"用 shell 调外部程序"的套壳，既没有复用 bash 的错误语义，又把 shellQuote 的转义风险引进来（`:319-325`）。

**风险**：如果改成官方 execute 工具，`ToolResult.Output` 的 JSON 形状消失（模型看到纯文本），但面板本来就没解析这个 JSON（见上），所以 UI 侧风险接近零；`BlockedCommands` 的产品语义（"同名命令不可用"，design §5.5 明言不是安全边界）不要悄悄升级成"安全"。
Effort M（结构迁移）/ S（最小修复），P2（最小修复里的 blocked 匹配可提到 P1 与 F22 合并）。

### F6 悬空 tool_call 修补重写了官方 middleware，且恢复路径未接线（SHOULD-USE-FRAMEWORK，P0 接线）

**现状**：
- `unpairedToolCalls`（`session.go:91-109`）扫事件日志找"有 assistant tool_call、无 tool 结果"的 call；
- `patchInterruptedToolCalls`（`session.go:58-87`）给每个补一条 `schema.ToolMessage(interruptedToolNote, id)` 追加进日志；
- `recoveryTask`（`:113-121`）生成 recovery prompt；
- 以上只被 `Runner.Resume`（`agent.go:308-331`）调用。

**eino 现成能力**：`adk/middlewares/patchtoolcalls`：
- `New(ctx, &Config{...})`（`E/adk/middlewares/patchtoolcalls/patchtoolcalls.go:119`）；
- `BeforeModelRewriteState` 在每次模型调用前扫描历史，给悬空 tool_call 插入占位 tool 消息（`patchtoolcalls.go:130-160`），可自定义文案（`PatchedContentGenerator` / `PatchedToolResultGenerator`，`:70-122`），还能 `RemoveOrphanResults` / `RemoveDuplicateResults` / `Strict`；
- 它是 `ChatModelAgentMiddleware`，可以直接 append 进 `newADKAgent` 的 `Handlers`（`engine_adk.go:114`）。

**为什么算不必要 + 更严重的问题**：
1. 机制重复：`patchInterruptedToolCalls` 是"持久化版"的同一个修复；官方 middleware 是"每次模型调用前"的工具，覆盖面更广（不止崩溃恢复，也覆盖 tool 执行中途被 cancel、工具并发丢失结果等）。
2. **恢复路径没接线**：全仓只有 `agent`/`mcp` 的测试调用 `Resume`（`session_test.go:202,325,350`、`recovery_test.go` 测的是 durable launch 而不是 mid-run resume）。生产崩溃后：`worker.go:255-300` 的 goroutine 用固定 session id `worker-<N>` 调 `RunSession`，ADK 从 SessionStore 重放历史（`cache_test.go:70-115` 证明第二次 `RunSession` 是在旧历史上追加），但**没有**任何代码补悬空 tool_call 或发 recovery prompt。若崩溃点落在 tool 执行中，下一轮请求就是"assistant(tool_calls) 后面没有 tool 消息"，真实 provider 会 400。
3. 因此 `CHANGELOG.md:32` "会话恢复：未配对工具调用补 interrupted + recovery prompt" 与 README "kill -9 后重启接着跑" 目前只在测试里成立。

**迁移草图**：
1. 把 `patchtoolcalls.New(ctx, &Config{MarkSynthetic: true, PatchedToolResultGenerator: func(...) { return &PatchedToolResult{Content: interruptedToolNote}, nil }})` 加入 `newADKAgent` 的 `Handlers`（放在 summarization/reduction 之后、subagent 之前/之后均可，它只改 state 消息）。
2. 保留 `unpairedToolCalls` 给"是否需要发 recovery prompt / 面板显示 Interrupted"用（或让 `Resume` 继续负责持久化补丁），删掉 `patchInterruptedToolCalls` 的日志写入，或反过来只用 `Resume` 并在 durable launch handler 里按"日志末态非 idle"决定调 `Resume` 而不是 `RunSession`。
3. 加一条集成测试：中途 cancel / 模拟崩溃 → 重启 `RunSession` → 断言发给 mock 的请求里每个 tool_call 都有对应 tool 消息。

**风险**：官方 middleware 只改内存历史，不落盘；如果产品要求"事件日志里也能看到 interrupted"，保留 `Resume` 的落盘补丁（两者可以共存，代价是重复 fix）。中间件顺序要与 `reduction`（会改写 tool 消息）确认无冲突——两者都作用于 messages，patch 插入的是 tool 消息，reduction 只砍长度，语义上正交。
Effort S（middleware 接入）/ M（恢复接线 + 测试），P0（对应 1.0.0 可靠性承诺）。

### F7 会话统计/事件读取：写了但没人用（DEAD，test-only）

**现状**：`computeSessionStats`（`session.go:125-158`）、`addUsageFromMessages`（`:162-182`）、`addUsageFromSpans`（`:185-199`）、`Runner.LoadEvents`（`agent.go:334-339`）、`Runner.SessionStats`（`:342-348`），共约 92 行；注释声称"面板要展开 Live Transcript 时用"（`agent.go:333`）。

**证据**：全仓（`internal/`、`cmd/`、`internal/ui`、`internal/view`）`grep -rn "SessionStats\|computeSessionStats\|LoadEvents"` 在 `internal/agent` 之外**零命中**；只有 `agent` 包自己的测试在用（`usage_test.go:38`、`compact_test.go:51`、`session_test.go`）。

**判定与建议**：要么接进面板（设计 §5.4 "预算可观测"是承诺项），要么删掉 ~92 行。当前状态是"测试替不存在的消费者买单"。若接线，建议把统计做成 `runtime` 的投影（它已经缓存了 workerEvent 与 usage，见 `runtime/worker.go:127-190`），不要重复解析 ADK 事件日志。
Effort S，P2。

### F8 事件日志半行修复：框架没提供，保留（JUSTIFIED）

`repairTornEventLog`（`session.go:18-42`）修的是 FileStore 的 `<event_id>\t<kind>\t<data>\n` 行格式（`E/adk/session/file_store.go:44-52`）；FileStore 明确把无换行末行判为 corrupted（`file_store.go:277`），没有 repair 选项。append-only 语义允许丢最后一条未提交记录，所以这是必要兼容层。

**风险**：硬编码了框架私有格式与 `url.PathEscape` 路径（`session.go:28` vs `file_store.go:44-46`）；eino 升级若换格式会静默失效（`repairTornEventLog` 会返回"not torn"而 load 继续失败，表现为可恢复会话不可恢复）。建议加一条对 FileStore 行格式的 conformance 测试（现有 `TestSessionLogFileOnDisk`，`session_test.go:250`，可以扩展成"截断半行→Resume 成功"）。**不需要替换，不建议删。**

### F9 压缩 / 工具结果截断 / 子智能体：正确的官方 middleware 用法（JUSTIFIED）

- `newSummarizer`（`compact.go:28-50`）：`summarization.New`（`E/.../summarization/summarization.go:296`），只自定义 `UserInstruction` 中文摘要指令（`compact.go:17-20`）和 `Callback` 上报面板。阈值语义（`ContextTokens`/`ContextMessages`）来自框架（`:194-199`）。
- `newToolResultReducer`（`truncate.go:48-63`）：`reduction.New`（`E/.../reduction/reduction.go:358`）；自定义 `reduction.Backend` 把落盘内容写进工作区 `.zeokeel/tool-output`（`truncate.go:19-39`），并再走一次 `inside()` 保证不越界；`SkipClear: true` 的取舍有注释（`:41-47`）。
- `newSubagents`（`subagent.go:20-56`）：`subagent.New`（`E/.../subagent/middleware.go:148`），子智能体是真实 `adk.ChatModelAgent` + 工具白名单（`:33-45`、`:60-91`），能力差异用工具而不是提示词，符合设计 §5.6。
- 唯一可讨论的是 `truncateRunes`（`compact.go:66-72`）与其它截断 helper 重复（见 §6），不影响判定。

### F10 事件流/回调/流分叉：框架不管的部分，手写合理（JUSTIFIED）

- 面板"逐字出字"必须绕过 ReAct/ADK"整轮读完才定最终答案"的限制：`streamingModel` 在模型层 `StreamReader.Copy(2)` 分叉（`stream.go:27-50`），主链路拿到带 tool_call 的完整分片，旁路只发展示文本；`WithTools` 递归包装保证派生的模型仍是旁路模型（`:52-60`）。这是对 `model.ToolCallingChatModel` 接口的合理适配，没有重新实现 eino。
- 工具/模型的 start/end/usage 只能从 `callbacks` 拿（`recorder.go:29-35` 注释解释得准确）；`OnEndWithStreamOutput` 必须在后台协程读（`recorder.go:78-102`），否则会把整轮输出堵在回调里——这是 eino 回调语义的正确使用，不是绕过。
- `recorder` 用 `Seq` 排序、`emit` 串行化、`streamWG/usageWG` 收尾（`agent.go:441-532`）都是 UI 契约层，框架不提供。
- 小瑕疵：`type streamWaitGroup = sync.WaitGroup`（`stream.go:70`）是无意义别名；`send()` 用两把锁（`agent.go:447-458`）注释解释清楚，可接受。

### F11 `mockllm`：测试桩，可小改（LOW-PRIORITY）

**现状**：453 行手写 OpenAI 兼容 server（`net.Listen` + `http.Server`，`mockllm.go:151-171`），含 SSE 分帧、tool_call 分片、`mock-500`/`mock-slow`/`mock-assess` 行为（文件头注释）。

**判定**：行为是产品特有的测试矩阵（先文本后 tool_call、分片 arguments、慢流、500），没有现成库能替，**JUSTIFIED 主体**。两处可省：
- `httptest.NewServer(handler)`（`G/net/http/httptest/server.go:266`，`Close` `:470`）替掉 `Start` 里手写的 listener/Server 管理（约 18 行），URL/Close 语义一样；
- 死代码：`chatRequest.Extra`（`mockllm.go:198`）无读写；文件尾 `var _ = strings.TrimSpace`（`:453`）；`_ = mockSrv`（`cmd/zeokeel/main.go:95`）冗余。

Effort S，P2。

### F12 MCP 协议层：官方 SDK 用对了（JUSTIFIED）

- 连接/握手：`mcpsdk.NewClient` + `client.Connect(dctx, tr, nil)`（`conn.go:87-93`）；transport 由 SDK 提供（`mcp.go:342-354`）。
- 工具发现用迭代器，注释明确不手写 cursor（`conn.go:111-128`）。
- 调用：`sess.CallTool`（`conn.go:216`），`IsError` 是结果级错误、返回给模型（`tool.go:114-127`），传输错误才上抛（`conn.go:217-226`）——这个区分正是 MCP 语义要求的。
- 错误映射：`errors.go:58-102` 是**领域策略**（可重试/永久），不是协议重写；用了 `jsonrpc.Error` code（`:79-86`）与 `mcpsdk.ErrConnectionClosed`（`:71`）。风险点：`:87-96` 对错误文本做子串匹配是脆弱兜底（SDK 升级/本地化会失效），建议逐步改为 `errors.Is/As`；Effort S。
- 工具名 `mcp__<server>__<tool>` + 64 字符哈希收敛（`tool.go:16-95`）是 provider 限制导致的产品决策，JUSTIFIED。
- `flattenResult`（`result.go:37-73`）：SDK 没有"把 Content 拼成文本"的 helper（只有 `SetError/GetError`，`S/mcp/protocol.go:337,347`），手写合理。
- `decodeArgs` 把 `""`/`"null"` 归一（`result.go:21-31`）是模型兼容胶水，合理。

### F13 MCP 结果截断与 `reduction` 重复（DUPLICATION）

**现状**：`limitResult`（`result.go:79-115`）+ `spill`（`:121-136`）按"前 N 行 + 字节上限"截断并落盘全文，默认 120 行 / 32KB（`mcp.go:55-56`）。

**为什么重复**：MCP 工具是通过 `ExtraTools` 进入同一个 ADK agent 的（`runtime/worker.go:216`、`cmd/zeokeel/main.go:201`），而 agent 已挂 `reduction` 中间件，`MaxLengthForTrunc = cfg.ToolOutputLimit`，默认也是 32KB（`agent/agent.go:218-220`、`truncate.go:48-63`）。所以同一条 MCP 结果会被截断两次，且两处默认值相同——这不是巧合，是职责重叠。
**替代**：MCP 层只保留一个很宽的保护上限（防止结果大到 OOM），把"给模型看的截断 + 落盘路径"统一交给 `reduction`；或者在 `mcp.Config` 注释里明确"MCP 独立使用时才截断，被 agent 挂载时由 agent 负责"，并把默认上限调高。
**风险**：MCP Manager 是独立包，在非 agent 场景（未来 CLI/测试）仍需自我保护；删掉会改变那些场景的行为。Effort S，P2。
另注：`spill` 的"时间戳+随机后缀"（`:121-136`）与 reduction 的 `GenTruncOffloadFilePath`（`truncate.go:55-61`）是两套落盘命名策略，迁移后只留一套。

### F14 MCP 重连退避可用 `x/time/rate`（LOW-PRIORITY）

`serverConn.restart`（`conn.go:158-181`）手写"连续次数上限 + 最小间隔"：`restarts`、`lastRestart`、`MaxRestarts`、`RestartBackoff`（`mcp.go:57-58,96-99`）。
`golang.org/x/time/rate` 已在模块图（`go mod why -m golang.org/x/time` → `internal/mcp` → `go-sdk/mcp` → `rate`），符号 `rate.NewLimiter(rate.Every(backoff), burst)`（`$(go env GOMODCACHE)/golang.org/x/time@v0.15.0/rate/rate.go:100,25`），pure Go。
**但不能直接替换全部语义**：`rate` 是限速，不提供"连续失败 N 次后停止重试"的计数语义（`restarts` 成功清零，`conn.go:228-232`）；后者需要保留。只有"两次重连之间最小间隔"（`:171-175`）适合 `rate`。收益 ~10 行，优先级低。保留现有实现也完全可以接受。

### F15 MCP 管理 API 未接线（DEAD）

`Manager.Health`（`mcp.go:234-242`）、`Restart`（`:257-263`）、`Refresh`（`:269-286`）、`Ping`（`:292-298`）在 `internal/mcp` 之外零调用（`grep -rn "\.Health()\|\.Restart(\|\.Refresh(\|\.Ping("` 只命中 `manager_test.go` 与 SDK 内部 `sess.Ping`）。注释描述的"面板重连按钮 / 长任务前预检"（`mcp.go:255-256,289-291`）没有实现。
**建议**：要么接到 设置/来源页（MCP server 健康卡），要么删掉（保留 `Health` 可能对排障有用，但没面板就没有"给人看"的出口）。Effort S，P2。

### F16 `runtime/durable.go` 四份相同的设置解析（DUPLICATION）

`concurrency`（`durable.go:81-94`）、`commandTimeout`（`:97-109`）、`pollInterval`（`:115-127`）、`modelTimeout`（`:131-143`）结构完全一致：`r.settings[key]` → `strconv.Atoi(strings.TrimSpace(...))` → 下限/上限 clamp → `time.Duration`。四份复制意味着"默认值/边界"四个地方维护。
**替代**：

```go
func (r *Runtime) settingInt(key string, def, min, max int) int
```

一个泛型 helper（约 15 行）+ 四个调用点，纯内部重构，无依赖。Effort S，P2。注意保留各 key 的上下界差异（并发 1..8、超时 5..3600、轮询 5..600、模型 10..3600）。

### F17 自然语言时间解析：领域逻辑，保留（JUSTIFIED）

`parseWhen`/`parseClock`/`parseEvery`（`extras.go:64-123`）把"每天 09:30 / 每 2 小时 / cron …"转成 `schedule.Spec`，不认识就报错（注释 `:61-63` 的理由充分：猜错时间比问一句更贵）。
**没有成熟 pure Go 库值得引入**：模块图里没有自然语言时间解析库；引入一个（如 when/dateparse 类）会新增依赖、且无法保证"说不清楚就报错"的产品语义。JUSTIFIED。小瑕疵：`fmt.Sscanf(rest, "%d", &n)` 与 `strings.TrimPrefix(rest, num)` 的组合对 `"2小时"`/`"2 小时"` 都工作，但可读性一般；`goalBlocked`（`:203`）是 `return goal.Blocked` 的无意义包装，建议删。Effort S（仅清理），P2。

### F18 `internal/workflow`：串行 CLI 步骤器 + 未启用的持久化层（LOW-PRIORITY，含 DEAD 子集）

**它实际是什么**：`Step{ID,Title,Handler,Args,SkipIf}` + `Definition` + `Runner.Start/run/Resume`，每步 `SaveRun`（`workflow.go:206-285`）；线性顺序、无分支、无并发、无 DAG。
**使用面**：唯一生产调用 `cmd/zeokeel/pipeline.go:43-79`（`-pipeline FILE`），用 `NewMemoryStore`（`pipeline.go:55`）——进程退出即丢，`Resume` 无从谈起。
**DEAD 子集**（约 150 行）：
- `NewSQLiteStore`/`SQLiteStore`（`sqlite.go:24-111`）仅 `sqlite_test.go:18,49` 使用；
- `Runner.Resume`（`workflow.go:177-202`）仅 `workflow_test.go:112`；
- `Step.SkipIf`（`workflow.go:28-29`）从未被设置（CLI 的 `pipelineFile` 没有该字段，`pipeline.go:21-29`）；
- `MemoryStore.All`（`memory.go:43-53`）无调用者；`Runner.Handlers`（`workflow.go:144-153`）无调用者。
**判定**：既不是框架能替的"编排能力"（eino `compose.Graph` 是静态图 + checkpoint，面向进程内执行，不是本用例），也不是设计 §5.10 描述的"子智能体脚本化 fan-out"（`design/...:1123`）。当前它只是一个 120 行可用的 CLI 顺序执行器，外加 150 行没人用的"durable"外壳。
**建议（二选一）**：
- 保守：删除 `sqlite.go`、`Resume`、`SkipIf`、`All`、`Handlers`，把 `workflow.go` 缩成 CLI 内部 ~60 行循环（或直接用 `cmd/zeokeel` 里的 `for` + `exec`），删掉 463 行中的 ~250 行；
- 扩展：若产品真要做 P1 的 workflow fan-out，用 eino `compose.NewGraph` + `WithCheckPointStore`（`E/compose/checkpoint.go:58`；store 接口别名 `E/compose/checkpoint.go:53`，`E/adk/runner.go:86`），把"步骤注册"换成图的节点/边，而不是继续在 SQLite 里手写 step_status。
**风险**：`-pipeline` 是对外 CLI（README 参数表列了），改动要保持 JSON 形状与退出码（`pipeline.go:43-79`）。Effort S（删死）/ M（迁 compose），P2。

### F19 `internal/goal`：领域 FSM，保留（JUSTIFIED）

- revision CAS：`mutate` 先比对 `g.Revision != rev` 再 `UPDATE ... WHERE revision=? AND created_at=?`（`goal.go:327-347,351-381`），并发写不会互相覆盖；`RowsAffected==0 → ErrConflict`。
- 阻塞判定：同一条件连续 `BlockThreshold` 轮才 `Blocked`（`goal.go:284-312`），不同条件或 productive 轮会重置（`:297-312`）。
- 轮次硬上限（`:314-319`），完成后的状态冻结（`:258-262`）。
这些都是产品语义，没有框架/库能替；SQLite 单写者模式与 `internal/store` 一致。JUSTIFIED。
小问题：`goalBlocked` 包装（`runtime/extras.go:203`）无意义；`goal.Service.New` 里 `EnsureV3`（`goal.go:127`）让 goal 包拥有 schema 迁移，与 `sqlite.go` 里 workflow 自建表的做法不一致（两套"谁拥有迁移"的约定）——建议统一到 store 的 schema 版本管理，Effort S，P2。

### F20 `internal/prompts`：手写插值是对的（JUSTIFIED）

见 §3 Q7。补三点证据：
- `expand` 单遍替换（`prompts.go:118-144`）保证"值里带 `{{}}` 不会被二次展开"，`text/template` 同样不会二次展开，所以这条不是差异点；真正的差异点是未知变量留在原文（`:136-140`）与 section 覆盖（`Add/Get`）的产品语义。
- KV-cache：`cache_test.go:18-68` 已经用"跨请求 system 字节相等 + tool 顺序相等 + 消息前缀相等"证明稳定性；这是要求不是猜测（`design/prompts.md` §八）。
- eino 模板组件确实存在（`E/components/prompt/chat_template.go:42`，FString 用 pyfmt、GoTemplate、Jinja2），但它的输出是 `[]*schema.Message`，且没有"按名字覆盖 section / 未知变量保留"的语义；`E/components/prompt` 的 `FromMessages` 还会引入 pyfmt/gonja 的格式规则（`{name}` vs `{{name}}`），与现有 `{{}}` 模板不兼容，迁移是纯风险。
小清理：`Add/Remove/Names/Get`（`prompts.go:46,68,79,88`）只有测试用（约 35 行）；`New` 与 `resort` 重复比较器（`:36-41` vs `:58-64`）可抽 `less()`。Effort S，P2。

### F21 `internal/assess`：领域校验，保留；解析可小改（JUSTIFIED）

- `validate`（`assess.go:144-208`）是产品硬约束（accept 四要素、每条 enabled 规则恰好评估一次、worktree 名 kebab 格式），框架没有等价能力。JUSTIFIED。
- `extractJSON`（`:250-266`）手写花括号配平：模型把 JSON 包在解说/代码块里是现实（注释 `:236-237`），15 行不算过度。可选替代：`json.Decoder` 从第一个 `{` 开始逐 token 扫（仍是手写，但少一个配平循环）；或让 provider 强制 JSON——eino-ext openai 支持 `ResponseFormat`（`$(go env GOMODCACHE)/github.com/cloudwego/eino-ext/components/model/openai@v0.1.13/chatmodel.go:157-159`，`ChatCompletionResponseFormatTypeJSONSchema`），但要改 `Generator` 接口且部分 OpenAI 兼容端点不支持 `response_format`，**不建议**作为近期动作。LOW-PRIORITY。
- 小问题：`Config.Model`（`assess.go:41`）在包内从未被读（模型在 `Generator` 里）；`NewEinoGenerator`（`assess/eino.go:33`）与 `agent.newChatModel`（`agent/agent.go:369`）各自构造一次 `einoopenai.NewChatModel`，两处配置漂移的风险（目前字段一样）。Effort S，P2。

### F22 `cmd/zeokeel` 小工具与 blocked 配置（LOW-PRIORITY，含 P1）

- `indexByte`（`pipeline.go:115-122`）就是 `strings.IndexByte`（`G/strings/strings.go:102`）；`oneLine`（`pipeline.go:105-113`）可用 `strings.Cut`（`:1271`）；`bashHandler` 里的手写截断（`:94-96`）与其它截断 helper 重复（§6）。
- blocked 配置失配：`main.go:76` 多词条目 vs `tools.go:230` 首词查表（已在 F5 详述）。这是"安全相关声明在默认配置下不生效"，建议 P1 修（S：改成对 `strings.Fields(cmdline)` 的前缀匹配，或把配置写成单词 `"git"`——但后者语义太宽，建议前者）。
- `var _ = fmt.Sprintf`（`extras.go:216`）、`_ = mockSrv`（`main.go:95`）是死代码（§7）。
- `runPipeline` 用 `NewMemoryStore`（`pipeline.go:55`）却保留了 SQLite store（F18）：如果 `-pipeline` 是发布特性，重启后不续跑与其"durable"命名不符，建议在 README 参数表里写明"一次性"，避免过度承诺。

### F23 字节大小/时长/截断 helper 重复；`go-humanize` 已在图里（SHOULD-USE-DEPENDENCY）

- 字节大小：`humanSize`（`runtime/storage.go:81-91`，B/KB/MB/GB）与 `humanBytes`（`runtime/artifacts.go:216-224`，B/KB/MB）逻辑重复、上限不同。`dustin/go-humanize` 已在模块图（`go mod why -m github.com/dustin/go-humanize` → `internal/store` → `modernc.org/sqlite` → `.../libc` → `humanize`），`humanize.IBytes`（`$(go env GOMODCACHE)/github.com/dustin/go-humanize@v1.1.0/bytes.go:136`）就是 1024 进制 + 一位小数，pure Go。
- 时长：`humanAge`（`runtime/pages.go:365-375`）与 `humanDuration`（`:526-539`）是同一套"刚刚/分/时/天"的两个变体；另外 `internal/ui/overview.go:354` 与 `internal/brief/digest.go:161` 各有一份 `humanAge`（跨包三份）。
- 截断：见 §6（6 份）。
**建议**：抽 `internal/textutil`（或 `internal/pagesfmt`）放 `Clip/ClipRunes/HumanBytes/HumanAge/HumanDuration`，字节大小用 `humanize.IBytes`；不要为此新建大包。Effort S，P2。

### F24 未接线导出 API 与占位符（DEAD，见 §7 清单）

约 150 行"写了但没人调用"的导出 API，加上 5 处 `var _ = ...`。它们不是 bug，但会让读者误以为功能已接（注释都写着"面板会用/上层会调"）。建议要么接线，要么删。Effort S，P2。

---

## 5. 正确使用框架的部分（无需改）

| 能力 | 实现 | 框架符号 |
| --- | --- | --- |
| 编排/循环 | `adk.NewChatModelAgent` + `adk.NewRunner`（`engine_adk.go:38-43,104-115`） | `E/adk/runner.go:159`、`E/adk/chatmodel.go` |
| 会话事件落盘 | 官方 `session.NewFileStore`（`agent.go:244-251`） | `E/adk/session/file_store.go:85` |
| 事件流/回调 | `callbacks.InitCallbacks` + `utils/callbacks` HandlerHelper（`engine_adk.go:46`、`recorder.go:36-107`） | `E/callbacks`、`E/utils/callbacks` |
| 流分叉 | `StreamReader.Copy(2)`（`stream.go:33`） | `E/schema/stream.go` |
| 上下文压缩 | `summarization.New`（`compact.go:30`） | `E/adk/middlewares/summarization/summarization.go:296` |
| 工具结果截断/落盘 | `reduction.New`（`truncate.go:48`） | `E/adk/middlewares/reduction/reduction.go:358` |
| 子智能体 | `subagent.New`（`subagent.go:51`） | `E/adk/middlewares/subagent/middleware.go:148` |
| 工具 schema 生成 | `utils.InferTool` + struct tag（`tools.go:34`、`runtime/artifacts.go:25`） | `E/components/tool/utils` |
| 模型 provider | `einoopenai.NewChatModel`（`agent.go:369`、`assess/eino.go:33`） | `eino-ext/components/model/openai@v0.1.13` |
| MCP | 官方 go-sdk 全协议（`conn.go`、`mcp.go`、`errors.go`） | `S/mcp`、`S/jsonrpc` |
| 结构化 SQLite | `modernc.org/sqlite`（`internal/store`） | pure Go，CGO=0 |
| 原生 UI | `egoist/mygo`（`internal/ui`） | pure Go（purego on darwin），CGO=0 |

**"框架-in-name-only"检查**：范围内没有发现"import 了 eino 但实际绕过它"的情况。所有 eino import 都在真实调用点（`go list -deps ./internal/agent` 里 eino 的 package 均可达执行代码）。唯一"形式存在、实质未用"的是 `flow/agent/react`（F1）和 `eino-ext` 只用了 model 组件——后者是设计选择，不是问题。

**相邻观察（不在本切片，未计入判定）**：`internal/skills` 的注入（`runtime/worker.go:273-279`）与 eino `adk/middlewares/skill` + `agentsmd` 存在功能重叠；`adk/middlewares/plantask`/`dynamictool`/`backgroundtask` 也未使用（design §5.6 的 jobs 能力尚未实现）。这些属于 skill/背景任务切片的审计范围，此处仅记录线索。

---

## 6. 内部重复工具函数

| 类别 | 位置 | 说明 |
| --- | --- | --- |
| 截断（字节） | `agent/tools.go:271 truncate`、`runtime/artifacts.go:128-130`（skill 20000）、`cmd/zeokeel/pipeline.go:94-96`、`mcp/result.go:79 limitResult`、`runtime/worker.go:444 clampForDisplay`（仅测试用）、`agent/compact.go:66 truncateRunes`、`runtime/pages.go:377 clip`、`engine/engine.go:580 clip` | 6+ 份，语义（字节/行/UTF-8 安全/是否加省略号）各不相同 |
| 字节大小 | `runtime/storage.go:81 humanSize`、`runtime/artifacts.go:216 humanBytes` | 上限不同；`humanize.IBytes` 可替 |
| 时长 | `runtime/pages.go:365 humanAge`、`runtime/pages.go:526 humanDuration`、`internal/ui/overview.go:354`、`internal/brief/digest.go:161` | 4 份 |
| 文件名清洗 | `agent/truncate.go:66 sanitizeFileName`、`mcp/tool.go:83 sanitizeName`、`mcp/result.go:139 fileComponent`、`runtime/onboarding.go:194 slug` | 同一"非 [A-Za-z0-9_-] → _" 规则的 4 个变体 |
| 设置解析 | `runtime/durable.go:81,97,115,131` | 4 份相同结构 |
| sort 比较器 | `prompts/prompts.go:36-41` 与 `:58-64` | New 与 resort 重复 |
| JSON 抽取 | `assess/assess.go:250 extractJSON` | 全仓唯一，不算重复，但可简化（F21） |
| 默认值 helper | `agent/engine_adk.go:218 maxSteps`、`agent/subagent.go:107 maxOr`、`agent/agent.go:212-220` 内联 | 可用 Go 1.21 内建 `max` 统一 |
| 模型构造 | `agent/agent.go:369` 与 `assess/eino.go:33` | 两处 `einoopenai.NewChatModel` 配置 |
| 工具结果 envelope | `agent.ToolResult`（`tools.go:215`）vs 官方 `string` 返回 | 迁移 F4 时统一 |

---

## 7. Dead code / 未接线 API / dead deps

**未接线（生产零调用，测试或注释声称有人用）**

| 符号 | 位置 | 现状 |
| --- | --- | --- |
| `Runner.Resume` + 其恢复链 | `agent/agent.go:308`；`session.go:58-121` | 仅 `session_test.go:137,202,325,350`；崩溃重启走 `RunSession`（严重，见 F6） |
| `Runner.LoadEvents` / `SessionStats` | `agent/agent.go:334,342` | 仅测试（F7） |
| `Runner.Run` / `runReact` | `agent/agent.go:263`；`engine_react.go` | 仅测试（F1） |
| `Manager.Restart/Refresh/Ping/Health` | `mcp/mcp.go:257,269,292,234` | 仅 `manager_test.go`（F15） |
| `Runtime.Persistence` / `OutboxQ` | `runtime/durable.go:50,65` | 零调用 |
| `Runtime.SavedModel` | `runtime/onboarding.go:214` | 零调用 |
| `Runtime.HasWorkerModel` | `runtime/worker.go:95` | 仅 `cmd/zeokeel/acceptance_test.go:214,253` |
| `WorkerConfig.CommandTimeout` / `ModelTimeout` | `runtime/worker.go:44,46` | 字段从未被读（实际用 `r.commandTimeout()/r.modelTimeout()`，`worker.go:221-222`） |
| `workflow.NewSQLiteStore` / `Resume` / `SkipIf` / `All` / `Handlers` | `workflow/sqlite.go:24`；`workflow.go:177,29,144`；`memory.go:43` | 仅测试或零调用（F18） |
| `prompts.Set.Add/Remove/Names/Get` | `prompts/prompts.go:46,68,79,88` | 仅 `prompts_test.go` |
| `assess.Config.Model` | `assess/assess.go:41` | 写入后从不读 |
| `runtime.worker.go clampForDisplay` | `runtime/worker.go:444` | 仅 `longtask_test.go:64-77` |
| `agent.ToolResult.ExitCode/DiffAdd/DiffDel` + `diffCounts` | `agent/tools.go:215-220,328-349` | 只进模型可见 JSON；`runtime` 不转发（`workerEvent` `worker.go:100-116`），`workerLine` 硬编码 `ExitCode: 0`（`runtime.go:346`）；`view.Line.Duration/DiffAdd/DiffDel`（`vm.go:405-411`）无赋值点 → `ui/stream.go:171-177` 的 diff chip 永不出现、失败命令显示 `exit 0` |
| `agent.Event.Duration` | `agent/agent.go:151`（`recorder.go:141-142` 有值） | 同上，`workerEvent` 未携带 → 设计 §5.7 的"耗时"没到面板 |
| `streamWaitGroup` 别名 | `agent/stream.go:70` | 无意义别名 |
| `goalBlocked` | `runtime/extras.go:203` | 无意义包装 |

**占位符/噪声**

- `var _ = fmt.Sprintf`：`cmd/zeokeel/extras.go:216`、`internal/workflow/memory.go:57`、`internal/view/onboarding.go:97`
- `var _ = context.Background`：`internal/runtime/reply.go:133`
- `var _ = strings.TrimSpace`：`internal/agent/mockllm/mockllm.go:453`
- `var _ = time.Now`：`internal/runtime/extras_test.go:138`（测试里）
- `mockllm.chatRequest.Extra`（`mockllm.go:198`）无读写
- `_ = mockSrv`（`cmd/zeokeel/main.go:95`）冗余
- 空注释段：`runtime/worker.go:336-337`（"workerSystemPrompt is what a Worker is told…" 下面是空行，没有函数）

这些占位符说明有 import 被保留却没有真实用途，是"死代码被人工压住"的信号。

**Dead dependencies**：无。`go.mod` 直接依赖（eino、eino-ext/openai、mygo、eino-contrib/jsonschema、go-sdk、modernc.org/sqlite）全部有真实调用点；间接依赖均为传递必需。未使用的**能力**（非依赖）：`adk/prebuilt/deep`、`adk/middlewares/patchtoolcalls`、`adk/middlewares/filesystem`、`adk/backgroundtask/*`、`doublestar`、`x/time/rate`、`go-humanize`——都是"已在图里、可零成本启用"的替代品，构成本报告推荐的基础。

---

## 8. 建议执行顺序

**第一批（P0/P1，小改动、直接消除正确性风险）**

1. F6：把 `patchtoolcalls` middleware 接进 `newADKAgent` + 让崩溃重启路径真正带 recovery 语义（S/M）。
2. F5/F22：修 `bash` 超时与 `blockedCommands` 匹配（S）；`read`/`edit` 的错误改 `refusal()` 风格（S）。
3. F2：`glob` 换 `doublestar`（S）。
4. F3：`inside()` 换 `os.Root`/`filepath.IsLocal`（S）。

**第二批（P2，删死与去重，无行为变化）**

5. F1 删 `engine_react` 与 `Runner.Run`（改 7 处测试调用点）。
6. F7/F15/F24 处理未接线 API（接线或删除）；删占位符。
7. F16/F23/§6 合并重复 helper；`humanize.IBytes`。
8. F11 `httptest.NewServer`；F13 MCP 截断去重；F14 可选 `x/time/rate`。
9. F18 删 workflow 死持久层（或按需迁 `compose.Graph`）。
10. F20/F21 的 `prompts.Set` 死 API、`assess.Config.Model` 清理。

**第三批（P2，结构性，需要单独开任务）**

11. F4/F5：把文件工具与 shell 迁到 `adk/middlewares/filesystem`（+ 自定义 `Backend`/`Shell`），或明确"不迁"并记录理由（设计文档 §5.1 与实现不一致需要收口）。
12. F7：若要做"面板展示会话统计"，接到 `runtime` 投影而不是保留无人调用的 `agent.SessionStats`。

**不需要动**：F8/F9/F10/F12/F17/F19/F20 主体/F21 主体——这些是"框架 + 领域胶水"的正确形态，替换只会增加风险。

---

## 附录 A：本次用到的验证命令（可复现）

```sh
export PATH=/usr/local/go/bin:$PATH
cd /data/ZeoKeel-Super-Agent

# 依赖闭包 / 纯 Go 证据
go mod why -m github.com/bmatcuk/doublestar/v4
go mod why -m github.com/dustin/go-humanize
go mod why -m golang.org/x/time
go list -deps github.com/bmatcuk/doublestar/v4 | grep -c 'runtime/cgo'   # 0

# 零 cgo 构建（输出到 /tmp，不动仓库）
CGO_ENABLED=0 go build -o /tmp/zeokeel-audit ./cmd/zeokeel && echo OK

# glob 行为对照实验（结论：filepath.Glob 不支持 **）
# /tmp/globtest 内建 a/x.go a/b/y.go a/b/c/z.go
# filepath.Glob("/tmp/globtest/**/*.go") => [a/x.go]
# doublestar.FilepathGlob(同 pattern)      => 全部 4 个

# 标准库符号
go doc os.Root
go doc path/filepath.IsLocal
```

**一句话总结**：这套 runtime 的主体（ADK 循环、压缩、截断、子智能体、MCP、durable effects、goal FSM、prompt 组装、usage 采集）是正确的框架使用或合理的领域自研；需要收口的是 **agent 工具层、悬空调用恢复、workflow 的持久化外壳，以及一批"写了没接"的 API**——其中"悬空调用恢复没接线"是本切片唯一会让 1.0.0 可靠性承诺在真实进程里失效的问题，应最先处理。
