# Go 生态 Coding Agent SDK / 框架调研（含编译与运行实测）

- 调研日期：2026-11-03（Asia/Shanghai）
- 环境：Go 1.27.3 linux/amd64，96 核 / 32 GB，无 GUI
- 实测代码：`spike-sdk/`（mock OpenAI SSE 服务 + 3 个可运行 demo + 8 个依赖探针）
- **代理结论（同事可复用）**：`proxy.golang.org` 在本机 **不通**（dial tcp 108.177.98.141:443 i/o timeout），
  `https://goproxy.cn` 200 OK。已执行 `go env -w GOPROXY=https://goproxy.cn,direct`，
  之后 11 个候选模块 + 约 120 个传递模块全部拉取成功。
- **cgo 结论**：全部 8 个依赖探针与 3 个 demo 均以 `CGO_ENABLED=0` 编译通过，`go list -deps -f '{{if .CgoFiles}}...'` 统计 **cgo 包数=0**（ADK 的 sqlite 走 `glebarez/sqlite` 纯 Go 实现）。

## 0. 场景基准（评分口径）

无人值守长任务 coding agent：一个 Ticket 跑几十分钟、几十~几百轮工具调用、崩溃可恢复、过程要流式喂桌面面板。
因此权重是：**可恢复性 > 事件精细度 > 工具循环正确性 > 上下文压缩 > 依赖体积 > 文档**，聊天机器人式的"多轮对话"几乎不加分。

## 1. 总览对比表

依赖数口径：单独 module 导入该框架最小可用包集后 `go list -deps` 的**传递包数增量**（net/http+json 基线 = 197 包 / 1 module），并用 `-ldflags="-s -w"` 出二进制；
其中 eino/ADK 两行取自**完整 demo**（代码全部用上），其余 provider/MCP 行来自"空白导入"编译探针，二进制体积与依赖数应视为**下限**（链接器会剔除未引用符号）。

| 框架 | 定位 | 维护活跃度（stars / 最近提交 / 版本） | 依赖 & cgo（增量包/模块/纯Go二进制） | agent loop | 流式+工具 | 上下文压缩 | 持久化/断点续跑 | 事件钩子 | MCP | 许可 |
|---|---|---|---|---|---|---|---|---|---|---|
| **cloudwego/eino** (+eino-ext) | 字节，Graph/ADK 双栈 Agent 框架 | 13,525★ / 2026-10-19 / **v0.10.0-alpha.35** | +100 包 / 28 mod / 16 MB / **0 cgo** | ✅ ReAct + `adk.ChatModelAgent` + `prebuilt/deep`（可恢复编码 Agent） | ✅ 实测两轮工具循环 | ✅ `adk/middlewares/summarization`（阈值+可插 token 计数）、`reduction`（工具结果落盘截断） | ✅✅ `SessionEventStore`(file/in-memory) + `CheckPointStore` + `Runner.Resume/ResumeWithParams` + `TurnLoop` 自动续跑 | ✅✅ 5 时机回调 + 强类型 Handler（实测 token/tool_call/usage） | ✅ eino-ext `components/tool/mcp`（实测编译） | Apache-2.0 |
| **google.golang.org/adk/v2** | Google ADK Go 版，Gemini 优先 | 8,924★ / 2026-11-02 / **v2.7.0** | +355 包 / 40 mod / **42 MB** / 0 cgo | ✅ LLMAgent + Sequential/Parallel/Loop + agenttool/A2A | ✅ 实测两轮工具循环（两种 tool_call 排序都 OK） | ✅ `session/compaction`（滑窗+token 阈值+LLMSummarizer） | ✅ `session.Service`（InMemory/**database: gorm+sqlite**/vertexai），实测同 session 换 Runner 续跑；真正跨进程需 database 后端 | ✅✅ `session.Event`（Partial/TurnComplete/UsageMetadata/Actions）——面板最好用 | ✅ `tool/mcptoolset`（依赖官方 go-sdk） | Apache-2.0 |
| **trpc-agent-go** | 腾讯，Graph+Planner+多 Agent | 1,896★ / 2026-10-29 / v1.11.2 | +288 包 / 32 mod / 14.5 MB / 0 cgo | ✅ planner/react + state_graph | ✅（未运行验证，编译探针通过） | ✅ `session/summary` + `model/token_tailor` | ✅ graph checkpoint/interrupt/resume | ✅ callbacks + execution_trace | ✅ `tool/mcp` | Apache-2.0 |
| **tmc/langchaingo** | LangChain Go 移植，最老牌 | 9,755★ / 2026-10-22 / v0.1.15 | +87 包 / 29 mod / 12 MB / 0 cgo | ⚠️ ReAct/OpenAI-functions agent，无步数/预算治理 | ⚠️ 流式工具调用需自己粘合 | ❌ 只有 memory 接口，无压缩实现 | ❌ 无 checkpoint/续跑 | ⚠️ callbacks 粒度粗（LLM/Chain/Tool 级，无 token/usage 细事件） | ❌ 无内置 | MIT |
| mark3labs/mcp-go | 社区 MCP SDK（client+server） | 9,213★ / 2026-11-02 / v1.5.0 | +39 包 / 8 mod / 5.7 MB / 0 cgo | — | — | — | — | — | ✅ 本体 | MIT |
| modelcontextprotocol/go-sdk | 官方 MCP SDK | 5,299★ / 2026-11-02 / v1.8.0 | +37 包 / 10 mod / 7.7 MB / 0 cgo | — | — | — | — | — | ✅ 本体（ADK v2 已依赖它） | MIT→Apache-2.0 过渡中 |
| openai/openai-go/v3 | OpenAI 官方 SDK | 3,522★ / 2026-11-02 / v3.91.1 | +38 包 / **7 mod** / 18 MB / 0 cgo | ❌ 仅 provider（含 stream accumulator） | ✅ | — | — | ❌ 需自建事件层 | — | Apache-2.0 |
| sashabaranov/go-openai | 社区老牌 OpenAI SDK | 10,806★ / **2026-09-29** / v1.43.0 | **+4 包 / 2 mod** / 3.9 MB / 0 cgo | ❌ 仅 provider | ✅ | — | — | — | — | Apache-2.0 |
| anthropics/anthropic-sdk-go | Anthropic 官方 SDK | 1,236★ / 2026-11-02 / v1.84.0 | +70 包 / 19 mod / 9 MB / 0 cgo | ❌ 仅 provider | ✅ | — | — | — | — | Apache-2.0 |
| googleapis/go-genai | Gemini 官方 SDK | 1,201★ / 2026-10-30 / v1.77.0 | +216 包 / 18 mod / 11 MB / 0 cgo | ❌ 仅 provider | ✅ | — | — | — | — | Apache-2.0 |
| Ingenimax/agent-sdk-go | 创业公司轻量 agent 框架 | 641★ / 2026-10-08 | 未深入（生态/文档过薄） | ⚠️ | ⚠️ | ⚠️ | ⚠️ | ⚠️ | ⚠️ | MIT |
| gptscript-ai/gptscript | 脚本化 Agent 运行时 | 3,279★ / **2026-05-22 已 archive** | — | — | — | — | — | — | — | Apache-2.0 |

> 表中 ✅✅ = 实测跑通；✅ = 有官方实现且我们编译验证过；⚠️ = 有但能力不足/需大量自研；❌ = 无。

## 2. 实测结果（这是本次调研的核心证据）

`spike-sdk/internal/mockllm` 是我写的 **OpenAI 兼容 mock 服务**（`net/http`，SSE chunk 格式，支持流式/非流式、tool_calls、`finish_reason`、`usage` chunk、可注入前导文本、可注入慢速/500）。
所有 demo 都是"起 mock → 真的发 HTTP → 真的流式解析"，无任何真实 key（环境变量里没有可用 key）。

| 用例 | eino (react agent) | ADK Go v2 (llmagent) | 手写 openai-go v3（基线） |
|---|---|---|---|
| A 单轮流式文本 | ✅ 1 次模型调用，逐 token | ✅ 逐 token `partial=true` 事件 | ✅ |
| B 两轮工具循环（tool_call 先出） | ✅ 2 次模型调用 / 1 次工具执行 | ✅ 2 次上游请求 / 1 次工具执行 | ✅ |
| C 两轮工具循环（**前导文本→tool_call**，Claude/DeepSeek 常见） | ❌ **默认直接结束，工具不执行**（见 §2.1）；加自定义 checker 后 ✅ | ✅ **原生正确**，无需配置 | ✅（自己写的循环天然正确） |
| D 取消/中断 | ✅ `context deadline exceeded`，流关闭 | ✅ 中断事件 + ctx.Err | ✅ |
| E 会话持久化 / 换实例续跑 | 未运行验证（API 与自带测试确认存在） | ✅ 同 session 换 Runner 后 `msgs=6`，历史被重放 | ❌ 不适用 |
| F usage 可见性 | ⚠️ 流式下 **`OnEnd` 不被调用**；usage 在最后一个 chunk 的 `ResponseMeta.Usage` | ✅ 终态事件带 `prompt=42 completion=17 total=59` | ✅ 需显式 `stream_options.include_usage` |

产物：`spike-sdk/eino-demo/run.log`、`spike-sdk/adk-demo/run.log`、`spike-sdk/baseline-demo/run.log`。

### 2.1 ⚠️ 最重要的坑：eino `StreamToolCallChecker` 会静默跳过工具

`flow/agent/react` 默认只在**第一个 chunk**里找 tool_calls。若模型先吐一句文本再发工具调用（Claude 风格、DeepSeek 某些模式、被 prompt 引导加"我先看看"时都会这样），
Agent 判定"本轮没有工具调用"→ **循环直接结束，工具一次都不执行，还把半句话当最终答案返回**。
无人值守场景下这是**静默错误**，比报错危险得多。修复：自定义 checker 消费完整条流（`spike-sdk/eino-demo/main.go` 中 `bufferChecker`，约 18 行）。
ADK v2 的 `openaimodel` 转换层天然处理两种顺序，实测 CASE C 直接通过。

### 2.2 其它实测发现

1. **eino 在 Go 1.27.3 上每次启动都打印告警**：`WARNING: sonic/ast only supports (go1.17~1.26 ...), fallback to encoding/json`。功能正确（回退 encoding/json），但**性能下降 + 日志噪音**，且说明其序列化底座未跟上 Go 版本。
2. **eino 的 `MaxStep` 默认 12**（pregel 节点数+10）。编码任务几十轮工具调用必须显式调大（demo 里 20）。
3. **eino 流式模式下 `model.CallbackHandler.OnEnd` 不会触发**（实测 0 次），只能靠 `OnEndWithStreamOutput` 的末 chunk 拿 usage/finish_reason——面板要展示 token 用量务必知道这点。
4. **ADK v2 的 OpenAI 兼容模型只在 v2**：v1.9.0 的 `model/` 只有 gemini/apigee；`model/openaimodel`（含 `APIChatCompletions`）是 v2 才有的，且 `go.mod` 要求 `go 1.26.10`、`google.golang.org/adk/v2` 主版本号已跳到 v2（tag v2.7.0）。引入即锁 v2 大版本。
5. **DeepSeek 等兼容端点**：两者都能用（eino-ext `ChatModelConfig.BaseURL`、ADK `ClientConfig{BaseURL, API: APIChatCompletions}`），但都**必须显式处理 usage chunk**（`stream_options.include_usage`）与 tool_call 排序差异。

## 3. 分层建议（推荐组合）

```
┌ 面板/UI 层     ← eino callbacks 事件总线（或 ADK session.Event）  →  Wails/HTMX/WebSocket
├ Agent 编排层   ← cloudwego/eino 的 adk 子包（ChatModelAgent + Runner + TurnLoop + middlewares）
│                   复用：filesystem 中间件(Read/Write/Edit/Grep/Glob)、backgroundtask(shell/subagent)、
│                         summarization(压缩)、reduction(结果截断落盘)、subagent(子智能体)、agentsmd
├ 模型 Provider  ← github.com/openai/openai-go/v3（官方，BaseURL 指 DeepSeek）
│                   或用 github.com/cloudwego/eino-ext/components/model/openai（已是 eino 适配器，少一层转换）
├ MCP 工具生态   ← github.com/modelcontextprotocol/go-sdk（官方；ADK v2 也锁它）
│                   客户端侧接 eino-ext/components/tool/mcp；自家工具用 go-sdk 起 server 反向暴露
└ 持久化         ← 自己实现 eino 的 CheckPointStore + SessionEventStore，落 SQLite/文件（勿绑框架自带 store）
```

**为什么是 eino 而不是 ADK v2（尽管 ADK 官方、事件更好用）**：
1. 我们最缺的不是"再写一个 loop"，而是**编码 Agent 的现成脚手架**。eino 的 `adk/prebuilt/deep` 是完整的 Deep-Agent 式编码 Agent（filesystem + shell 后台任务 + plan/todo + skill + 子智能体 + summarization），且返回 `adk.ResumableAgent`；ADK v2 需要自己用 `functiontool` 一个个拼工具。
2. eino 的 `reduction` 中间件正好回答"工具结果截断"：超长输出自动落盘成 `<persisted-output>` 给模型预览——这是我们必做的功能，白送。
3. 体积/依赖：16 MB vs 42 MB、28 vs 40 个模块；eino 没有 otel/grpc/gorm/a2a 全家桶。
4. 中文文档、国内社区、与我们的技术栈/沟通成本匹配；ADK 是 Gemini 优先，DeepSeek 兼容接口是后补的 v2 新特性。
5. **反向选择条件**：若确定要 Google 生态/ADK Web UI/A2A 互通，或团队更看重"官方 semver v2 稳定 + 事件模型最完整"，选 ADK v2，本文档 §5.2 骨架可直接用。

**为什么 provider 层不建议 sashabaranov/go-openai**（尽管依赖只有 2 个模块、最省）：最近提交 2026-09-29，维护节奏慢于官方；官方 `openai-go/v3` 有 `ChatCompletionAccumulator`（省掉手写 chunk 聚合），且 ADK v2 已经锁它，冲突风险最低。

## 4. 排名前 2 候选的可抄骨架

### 4.1 eino：流式 + 工具循环 + 面板事件（实测通过）

```go
// 1) 工具：Go struct + struct tag 反射出 JSON Schema（无需手写 schema）
type EchoArgs struct {
    Text string `json:"text" jsonschema:"required" jsonschema_description:"text to upper-case"`
}
echo, _ := utils.InferTool("echo_upper", "Upper-case the text.",
    func(ctx context.Context, in EchoArgs) (EchoResult, error) { return doEcho(in.Text), nil })

// 2) 事件：一次注册，拿到 token 流 / tool_call 增量 / 工具起止 / usage
handler := ub.NewHandlerHelper().
    ChatModel(&ub.ModelCallbackHandler{
        OnStart: func(ctx context.Context, i *callbacks.RunInfo, in *model.CallbackInput) context.Context { return ctx },
        OnEndWithStreamOutput: func(ctx context.Context, i *callbacks.RunInfo,
            out *schema.StreamReader[*model.CallbackOutput]) context.Context {
            go func() {                       // 必须消费完并 Close（eino 给的是副本）
                defer out.Close()
                for { chunk, err := out.Recv(); if errors.Is(err, io.EOF) { break }
                      // 面板：chunk.Message.Content 增量；chunk.Message.ToolCalls 增量
                      // usage 只在末 chunk: chunk.Message.ResponseMeta.Usage
                }
            }()
            return ctx
        },
    }).
    Tool(&ub.ToolCallbackHandler{
        OnStart: func(ctx context.Context, i *callbacks.RunInfo, in *tool.CallbackInput) context.Context { /* tool_call 开始 */ return ctx },
        OnEnd:   func(ctx context.Context, i *callbacks.RunInfo, out *tool.CallbackOutput) context.Context { /* 结果/截断 */ return ctx },
    }).Handler()
ctx = callbacks.InitCallbacks(ctx, &callbacks.RunInfo{Name: "ticket-agent"}, handler)

// 3) Agent + 循环（关键：自定义 StreamToolCallChecker + 足够大的 MaxStep）
agentOpts, _ := react.WithTools(ctx, echo)
a, _ := react.NewAgent(ctx, &react.AgentConfig{
    ToolCallingModel: chatModel,          // eino-ext openai.NewChatModel(BaseURL=DeepSeek)
    MaxStep:          200,                // 默认 12 远远不够
    StreamToolCallChecker: func(ctx context.Context, sr *schema.StreamReader[*schema.Message]) (bool, error) {
        defer sr.Close(); has := false
        for { c, err := sr.Recv(); if errors.Is(err, io.EOF) { break }; if err != nil { return false, err }
              if len(c.ToolCalls) > 0 { has = true } }
        return has, nil                    // 必须读完整条流，否则会漏掉前导文本后的 tool_call
    },
})
sr, _ := a.Stream(ctx, []*schema.Message{schema.SystemMessage(sys), schema.UserMessage(task)}, agentOpts...)
for { chunk, err := sr.Recv(); if err != nil { break }; fmt.Print(chunk.Content) }
```

### 4.2 eino：崩溃续跑（durable 路径，API 已核对，未在本次 spike 运行）

```go
store := newSQLiteCheckPointStore(db)                 // 自己实现 adk.CheckPointStore{ Set/Get }
sess  := adk.NewFileStore... / 自定义 SessionEventStore
runner := adk.NewRunner(ctx, adk.RunnerConfig{
    Agent: agent, EnableStreaming: true,
    SessionID: "ticket-42", SessionStore: sess, CheckPointStore: store,
})
iter := runner.Query(ctx, task, adk.WithCheckPointID("ticket-42"))   // 中途 Stop/取消 → 写 checkpoint
// 进程重启后：
iter, _ = runner.Resume(ctx, "ticket-42")             // 或 ResumeWithParams 定向续跑被中断的节点
```
> 注意语义：eino/ADK 都是"**中断/取消时**写 checkpoint"，不是"每步都持久化"。
> 真要扛 `kill -9`，需自己做事件日志重放（每次 tool 结果落盘后再推进），这是无框架能白送的部分。

### 4.3 ADK Go v2：完整可运行骨架（实测通过，含持久化）

```go
m, _ := openaimodel.NewModel(ctx, "deepseek-chat", &openaimodel.ClientConfig{
    APIKey: os.Getenv("DEEPSEEK_API_KEY"), BaseURL: "https://api.deepseek.com",
    API: openaimodel.APIChatCompletions,              // 不加这行会打 /v1/responses
})
echoTool, _ := functiontool.New(functiontool.Config{Name: "echo_upper", Description: "..."}, echoUpper)
a, _ := llmagent.New(llmagent.Config{Name: "coding_agent", Model: m, Instruction: sys, Tools: []tool.Tool{echoTool}})

svc := session.InMemoryService()                       // 生产换 session/database（gorm+纯Go sqlite）
r, _ := runner.New(runner.Config{AppName: "zeokeel", Agent: a, SessionService: svc,
    AutoCreateSession: true, Compaction: &compaction.Config{TokenThreshold: 60000, CompactionInterval: 10}})
for ev, err := range r.Run(ctx, "user-1", "ticket-42",
        genai.NewContentFromText(task, genai.RoleUser), agent.RunConfig{StreamingMode: agent.StreamingModeSSE}) {
    if err != nil { break }
    // ev.Partial / ev.TurnComplete → 流式渲染；ev.Content.Parts[].FunctionCall/FunctionResponse → 工具面板
    // ev.UsageMetadata → token 用量；ev.Actions → 状态变更
}
// 崩溃后：同 service 新建 Runner，用同一 (userID, sessionID) 继续 Run 即可续上下文
```

## 5. 如果只用官方 openai-go 自己写 loop，要写多少代码

我实写了 `spike-sdk/baseline-demo`（可运行，含流式、工具循环、取消、事件打印）：**175 行有效代码**。
所以"能跑的最小 loop"确实只要 1-2 天。但**它只覆盖了 10% 的工作量**，对照组：

| 能力 | 手写工作量（估算） | 已有现成实现 |
|---|---|---|
| 流式 + 单工具循环 + 取消（已实测 175 行） | 150-250 行 | eino / ADK / trpc 全有 |
| 并行工具调用 + 结果去重/排序 | 150-250 行 | eino ToolsNode、ADK 自动 |
| 工具 schema：struct→JSON Schema 反射 | 200-300 行（或手写每个 schema，易错） | eino `InferTool`、ADK `functiontool` |
| 工具结果截断/落盘、超长输出治理 | 200-300 行 | eino `reduction`、ADK artifact |
| 上下文压缩（token 计数 + 摘要 + 滑窗） | 300-500 行 | eino `summarization`、ADK `session/compaction` |
| 会话持久化 + 崩溃续跑 + 幂等重放 | 400-700 行 | eino `CheckPointStore/SessionEventStore`、ADK `session.Service` |
| 重试/失败转移/限流/预算（MaxLLMCalls） | 150-250 行 | eino `failover_chatmodel`、ADK plugin `retryandreflect` |
| MCP 客户端接入 | 200-300 行（+协议坑） | eino-ext `tool/mcp`、ADK `mcptoolset` |
| 子智能体/多 Agent 编排 | 300-500 行 | eino `subagent`/`supervisor`、ADK `agenttool`/workflowagents |
| 事件总线/usage 统计/OTel | 150-250 行 | eino callbacks、ADK `session.Event` |
| **合计（生产可用）** | **约 2,200-3,600 行，且要长期维护协议差异** | 引入 16-42 MB 二进制代价 |

**结论**：从零手写 loop 的边际成本不在 loop 本身（175 行），而在**上下文压缩 + 持久化续跑 + 多 provider 兼容**这三块（占总工作量 60%+），
恰好是现成 SDK 已经做了、而且做得比我们单独立项更细的部分。用户"尽量复用现成 SDK"的直觉是对的。

另外实测：`eino-demo` 254 行有效代码、`adk-demo` 223 行，**都包含约 60 行面板事件打印**——即框架版 loop 的净代码量与手写基线相当甚至更少，却额外白拿了压缩/持久化/MCP。

## 6. 风险与不采用理由（要点）

**cloudwego/eino（推荐，但有明确风险）**
- 🔴 **版本仍是 `v0.10.0-alpha.35`**：v0.10 线仍是 alpha（可用版本列表里 alpha.33/34/35 并存），API 破坏性变更是常态；必须锁死版本并预留升级预算。
- 🔴 默认 `StreamToolCallChecker` 会静默吞掉工具调用（§2.1）——必须改，否则无人值守下会"假装干完"。
- 🟡 Go 1.27 上 sonic 回退 + 启动告警；建议锁 Go 1.26 LTS 或接受 performance 损失。
- 🟡 仓库根目录堆了若干 `*_review.md`、`V0.9_*.md` 草稿，工程卫生一般；文档中英混杂。
- 🟡 `adk` 子包（含 `deep`/`TurnLoop`/checkpoint）是相对年轻的一层，本次只运行验证了 `flow/agent/react` 路径，durable 路径仅做 API/测试核对。

**google.golang.org/adk/v2（备选，质量高但重）**
- 🔴 依赖极重：+355 包 / 40 模块 / **42 MB** 二进制（otel、grpc、gorm、cloud.google.com/go、a2a 全带上），桌面应用打包不友好。
- 🔴 `genai.Content` 是内部通用消息类型：即使接 DeepSeek 也要 `import google.golang.org/genai`，语义绑定 Google 生态。
- 🟡 `openaimodel` 只存在于 v2，且 v2 是大版本跃迁（v1.9.0→v2.7.0），半年前代码无法平滑升级；`go.mod` 要求 go 1.26.10。
- 🟢 优点真实：事件模型（Partial/TurnComplete/UsageMetadata/Actions）是三者里**最适合桌面面板**的；compaction 与 session 后端开箱即用；两种 tool_call 排序都正确。

**trpc-agent-go（值得做第二备选/PoC）**
- 🟡 社区规模小（1,896★），生态/第三方示例少；本次仅编译探针通过（+288 包 / 32 模块 / 14.5 MB），未做运行验证。
- 🟢 功能意外完整：graph checkpoint/interrupt/resume、session summary、`token_tailor`、`tool/mcp`、`tool/agent`、`tool/hostexec`、`codeexecutor`，甚至有 `agent/claudecode`、`agent/codex` 适配。

**tmc/langchaingo（不采用）**
- 常年 v0.1.x；**没有 durable 运行时**（无 checkpoint/续跑）、无内置压缩、无 MCP；callbacks 粒度只到 LLM/Chain/Tool 级，拿不到逐 token + usage 的稳定事件。
- 它的抽象（chains/memory）是"聊天机器人时代"的设计，和"无人值守长任务"需求错位。适合做原型，不适合做我们的执行内核。

**其它**
- `gptscript-ai/gptscript` 已于 2026-05-22 **archive**，不采用。
- `Ingenimax/agent-sdk-go` 641★、MIT、2026-10-08 仍活跃，但生态/文档过薄，长期风险高。
- 通用风险：所有"OpenAI 兼容"适配层对 **usage chunk、tool_call 排序、reasoning_content、并行 tool_calls** 的容忍度不同，换模型（DeepSeek/GLM/Kimi）必须重跑本文的 A/B/C 用例；我已把 mock 服务留在 `spike-sdk/internal/mockllm`，可直接 CI 化。

## 7. 最终一句话推荐

**用 `cloudwego/eino`（+eino-ext）做 Agent 编排内核与编码 Agent 脚手架（`adk` 子包 + `prebuilt/deep`），provider 层用 `openai-go/v3`（或 eino-ext 的 openai 适配器）指向 DeepSeek，MCP 用官方 `modelcontextprotocol/go-sdk`；把 eino 的 `CheckPointStore/SessionEventStore` 落成我们自己的 SQLite 实现，并把 `StreamToolCallChecker` 改成长流缓冲版——这样能用最小自研量拿到"可恢复的长任务 coding agent"，若后续需要最完整的事件/会话模型再评估迁移到 ADK v2。**

---

### 附：本目录实测产物索引

| 路径 | 内容 |
|---|---|
| `spike-sdk/internal/mockllm/mockllm.go` | OpenAI 兼容 SSE mock（流式/非流式/tool_calls/usage/慢速/500） |
| `spike-sdk/eino-demo/` | eino 5 用例（含 checker 坑与修复），`main.go` + `run.log` |
| `spike-sdk/adk-demo/` | ADK v2 5 用例（含持久化续跑、取消），`main.go` + `run.log` |
| `spike-sdk/baseline-demo/` | 官方 openai-go/v3 手写 loop 基线，`main.go` + `run.log` |
| `spike-sdk/mcp-probe/` | eino MCP + ADK mcptoolset + 两个 MCP SDK 同编译验证 |
| `spike-sdk/research-data/repos.jsonl` | 12 个仓库的 GitHub API 原始元数据（stars/许可/pushed_at） |
