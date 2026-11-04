# ZeoKeel Super Agent — Go + mygo 原生 UI 技术方案

> 状态：设计草案 v0.1（基于 2026-11-03 的调研）
> 对象：把 [dsh-superagent-prototype](https://github.com/deepseek-ai/dsh-superagent-prototype)（MIT）的想法完整移植为 **Go 单二进制桌面应用**，UI 用 [mygo](https://github.com/egoist/mygo) 原生控件（`ui` 包，非 webview）。
> 配套调研（同仓库）：[参考原型分析](../research/reference-analysis.md) · [mygo 能力报告](../research/mygo-analysis.md) · [Go Agent SDK 选型](../research/go-sdk-analysis.md) · [环境可行性实测](../research/env-feasibility.md)

---

## 0. 一页结论

**能做，而且这个技术栈选得对。但成本的重心不在 UI，在 Agent 运行时。**

1. **UI 不是风险点。** mygo 的原生控件覆盖度接近一个成熟设计系统：虚拟化 List/Table（百万行只建可见行）、Tabs/Sidebar/Split/Form、Markdown（含流式）、代码编辑器与 Diff 视图、Chart、Toast、AlertDialog、SearchField、Tree/Outline、Tooltip 全部开箱。缺口只有 Card / Drawer / CommandPalette 三个复合件和 data-grid 的过滤分组，自建成本可控。
2. **真正的风险是 Agent Runtime。** 参考原型**自己并没有 agent 实现**——它是 DSH（DeepSeek Harness）的宿主插件，模型调用、工具循环、会话、沙箱、中断全部由宿主提供。脱离 DSH 之后，这部分要我们自己造，约占总工作量的 1/3、占风险的 2/3。
3. **领域层可以直接照搬，而且必须照搬。** 原型里最有价值的不是代码而是**确定性设计**：内容派生的幂等 ID、Ticket 阶段表、gate + admission table + Mandate/revalidate、durable Effects 队列、Outbox 回执、投递三段式（delivered / read / turnEnded）、`followupOwners` 归属规则。这些是踩过坑的产物，任何一条"简化"都会以重复劳动、重复评论、误结束 Ticket 的形式还回来。
4. **一个决定性的架构约束**：mygo 的 view 在主线程每帧重跑，后台 goroutine **不能**直接改 UI 状态。整个 UI 必须建立在"后台产出不可变快照 → 原子换指针 → 请求重绘"之上。这条边界守住，mygo 的性能模型和测试模型就都是朋友；守不住，会得到随机卡死和必现死锁。
5. **工作量诚实估算**：约 **2.6–3.1 万行 Go**（含测试）。2 人约 4.5 个月，1 人约 7 个月。**已决定不做简化过渡版**，直接实现完整 Eval；补偿是在 M1 末加 2 天 `make smoke`（真实模型跑 3 个任务）提前暴露 Runtime 质量。

**已定技术栈**：`eino`（编排）+ `openai-go/v3`（provider）+ `mygo`（原生 UI）+ `modernc.org/sqlite` + 官方 MCP go-sdk，**全链路 `CGO_ENABLED=0`**。

**推荐路线**：M0 地基 → M1 Agent Runtime（含提问/交付物）→ M2 授权与端到端（完整 Eval，不做过渡版）→ M4 可靠性（含子智能体/goal/schedule）→ M5 面板完整 → M6 扩展与发布（workflow/skills/MCP）。详见 §10。

**核心架构 = 官方双层**（ADR-14）：**Router 全局唯一**（一个大脑：全局上下文、两层过滤、Eval、**跨工作区派发**、统一回复、轻量模型）；**Worker 每 Ticket 一个**（上下文隔离、绑定所属工作区目录、完整工具、不直接对外说话）。**Engine 全局唯一**，工作区是实体上的 `workspace_id` 属性与执行作用域，**不是并行的状态机**。v1 即支持多仓库，但仍不是多租户（§4.5）。

**全功能定位**：ZeoKeel 不是"GitHub 机器人"，而是**一个带原生面板的全功能本地 AI Agent**，GitHub/Feishu 只是它的两个事件源。`ask_user_question`/`present`/`goal`/`schedule`/`workflow`/子智能体/skills/MCP 都属于能力集（§5.10），且都能长在现有领域模型上而不污染它（§5.9）。

---

## 1. 背景与目标

### 1.1 参考原型是什么

`dsh-superagent` 是"一个 GitHub 仓库的主动式编码智能体"，形态是 **DSH bundle**（宿主插件）。它的工作流：

```
Source(GitHub/Feishu) → Trigger → Thread → Eval → Decision(accept) → Mandate + Ticket
                                                                    → Assignment → Executor(agent 会话 + 目录) → PR/评论
```

它的设计哲学（`docs/000`）值得整套继承：

> **Take as little human attention as possible, and give as much value as possible.**
> 智能体只在被请求时、或团队写下的规则允许时行动；只在有人必须行动、或结果可用时说话。

三条对设计有直接约束的推论：
- **概念要少**——业务需求变成某个已有概念的属性，而不是新概念。
- **设置要少**——一个默认值能满足几乎所有团队时，就不做设置；团队用自然语言描述的行为是提示词，不是配置项。
- **面板只显示需要人的东西**——健康的状不占空间；每个元素都要回答"现在有事需要我吗"。

### 1.2 我们要做什么

**完整移植**原型的业务能力与领域语义，只更换技术栈：

| 维度 | 参考原型 | 本项目 |
| --- | --- | --- |
| 语言 | TypeScript | Go 1.27.3+ |
| 运行时宿主 | DSH 插件进程内 | 独立单二进制 |
| Agent 执行 | 宿主提供（Session/preset/tools） | **自研 Agent Runtime**（见 §5） |
| 面板 | 宿主 Web 面板中的 React 插件 | **mygo 原生桌面窗口** |
| 持久化 | SQLite（宿主环境内） | SQLite（同构 schema） |
| 进程 | 由 tmux/脚本守护 | **桌面进程 + 托盘常驻 + 子命令** |

**能力范围（必须全部保留）**：GitHub Source（notifications、订阅 Issue/PR、CI、可选的"接收全部"）、Feishu Source（事件/历史/topic 轮询/图片）、Policy（规则 + admission table + accept mode + 版本）、Eval、Ticket/Assignment/Mandate/Subscription 全生命周期、PR 生命周期绑定（`follows`）、并发 slot、pause/retry/resume/stop、提示词分层与版本化、出站幂等。

### 1.3 不做什么

- **不做 webview / HTML 前端**。UI 全部 mygo 原生控件（这也是选型的目的）。
- **不复刻 DSH 宿主**。不实现 agent preset 市场、插件管理器/市场、open-in-app、Office/PDF 转换、语音输入、CoT 翻译、产品遥测等宿主级工程。
- **但 agent 能力要全**。`ask_user_question` / `present` / `goal` / `schedule` / `workflow` / 子智能体 / skills / MCP / web / plan mode 都属于**必备能力**，不是可选项——见 §5.10 能力矩阵。
- **硬约束：全项目零 cgo。** 任何引入 cgo 依赖的方案一律否决，包括「某个 SDK 性能更好所以要 cgo」这类理由。CI 有断言守护（§9.2）。这条约束直接决定了几个选型：SQLite 用 `modernc.org/sqlite` 而非 mattn/go-sqlite3；UI 用 mygo（purego）而非 Fyne/GTK 绑定。
- **多工作区，但不是多租户**（ADR-14）。单机、单 operator、**多个工作区**（一个工作区 = 一个仓库 + 一个本地根目录 + 自己的 Source/Policy/提示词/预算/槽位）。单进程、单 SQLite、单 runtime；切换工作区只换 UI 作用域，不重启 runtime、不暂停在跑的 Ticket。仍**不做**：多用户、多副本、每工作区一个进程、一个窗口混多个工作区。
- **不搬 migration/legacy**。原型 `src/app/migration.ts` 616 行是 schema 10→11 的历史包袱，新库从 v1 开始。
- **不做 i18n 全量**。首版中文（+ 关键英文），面板文案与 reason 模板保留码表结构以便后续加语言。

### 1.4 已核实的事实（不是假设）

全部结论来自本机实测（[env-feasibility.md](../research/env-feasibility.md)、[mygo-analysis.md](../research/mygo-analysis.md)），不是文档推断：

| 事实 | 证据 | 影响 |
| --- | --- | --- |
| Go 1.27.3 可用，且正是 mygo 要求的最低版本 | `/usr/local/go/bin/go version`；mygo `go.mod` | CI/开发机必须钉死该版本 |
| **模块代理默认不可用，必须换镜像** | `proxy.golang.org` 30s 超时；`GOPROXY=https://goproxy.cn,direct` 0.13s 拉包；默认 `GOSUMDB` 经镜像 `/sumdb/` 校验即可，**无需关闭校验** | 写进开发环境说明与 CI 配置（§8.4） |
| mygo 原生 UI 在 `CGO_ENABLED=0` 下可编译 | `go build` 成功，stripped **9.5 MB**（CGO=1 为 14.1 MB） | 单二进制成立 |
| **但产物不是纯静态**：purego 的 fakecgo 仍动态链接 glibc | 实测产物依赖 glibc | **不能进 Alpine/scratch**；分发用 glibc 基础镜像或普通发行版（§8.3） |
| 无头环境跑不了真窗口，但 Xvfb 下完全正常 | 裸跑 `mygo: cannot open display`（exit 1）；`xvfb-run -a` 后窗口正常、截图完整；自动回落 CPU 渲染，首帧 5–10 ms（`MYGO_GPU=1` 走 llvmpipe 首帧 1.5s，无必要） | 桌面开发机无问题；CI 用 Xvfb（§9.2） |
| **view 单测完全无头** | `ui.NewTester` 在 CGO=0、无 DISPLAY 下：点击 "+" → 状态变 1 → 断言文本 "1"，0.02s PASS | CI 主流程不需要显示器 |
| mygo 自身可回归 | `CGO_ENABLED=0 go test ./ui` 941 个测试 77s 全绿 | 依赖质量可信 |
| 真窗口 E2E 可自动化 | `MYGO_E2E=1 xvfb-run -a dbus-run-session` 6.1s PASS；另用 `automation` socket 完成 tree→press→capture 断言（截图已随研究目录清理；E2E 脚本见 `spike/run_xvfb.sh`） | 可以给面板写真实交互回归 |
| 后台→UI 有且仅有两个安全点 | `Window.Update(fn)`、`Window.Invalidate()`（`content.go:153/173`）；`loop.go` 的 `onMain/postMain` 未导出 | 决定了 §6.2 的快照架构 |
| 托盘可驻留，但**关最后窗口默认退出** | `Tray` + `OnWindowAllClosed`；`examples/menubar-native` | 必须"关窗=隐藏"（ADR-1） |
| 二进制体积基线 | release `counter-native` 8.9 MB / 实测 stripped 9.5 MB；估算加 editor/markdown/chart/sqlite 后 12–16 MB | 可接受 |
| `go vet` / `mygo vet` 可用 | 全部 exit 0（`mygo vet` 专查 build 值逃逸） | 纳入 CI |
| 参考原型逻辑代码量 | `src/{core,app,sources,executor,infra}` ≈ 10.6k 行 TS；测试 15k 行 | 移植工作量估算依据（§10） |
| **选定技术栈可在零 cgo 下共存并运行** | 一个探针程序同时引入 mygo + eino(adk) + openai-go/v3 + modernc/sqlite + MCP go-sdk，`CGO_ENABLED=0` 编译通过并 **6/6 运行时检查通过**（含原生窗口、eino 两轮工具调用、sqlite 读写、MCP 调用）；`go list -deps` 的 cgo 包集合为**空** | 用户红线可满足，开工无阻塞（§9.4） |
| 原型没有任何 agent 运行时实现 | `src/executor/dsh.ts` 全程调用 `ctx.sessionController` / `agent.*` | 自研 runtime 是最大增量（§5） |

---

### 1.5 从 000–008 得到的启发（每篇一条可执行的设计约束）

九篇文档的价值不在信息量，在于它们把**踩过的坑写成了规则**。对我们影响最大的：

| 文档 | 最关键的启发 | 落到 ZeoKeel 的硬约束 |
| --- | --- | --- |
| **000 哲学** | 判断标准是"注意力成本"，不是功能数量 | 一个常驻窗口本身就是注意力成本 → **默认最小化到托盘**，窗口只在需要人时打开；Settings 面板每加一项都要能回答"为什么不能是默认值" |
| **001 领域** | **状态是推导的，不是存储的**（Ticket stage 是查表函数） | Go 里 `Ticket` **不设 `Stage` 字段**；同理不存"是否等待人"等派生量。派生量用函数 + 测试锁死 |
| | 幂等来自**内容派生 ID**，不是锁或去重表 | `domain.Key(parts...)` 是唯一 ID 来源；禁 UUID |
| | 维护一份"**被拒绝的概念**"清单 | 我们也在 `design/rejected-concepts.md` 维护，防止设计腐败 |
| **002 授权** | 授权是四层：判断 / 表格 / 凭据 / 复核，**能力与语境分离** | Subscription 扇出**永不授权**；消息、评论、文档一律是数据，改不了权限 |
| | "先只记录、不行动"的 `observe` 是产品级安全阀 | 默认 `observe`，任何自动行为上线前先跑一段"本会做什么" |
| | 提示词**不是**权限 | 自研 agent 最容易犯的错：让模型自己判断能不能干。权限必须在代码里 |
| **003 执行** | 阶段表"自上而下首个匹配"是可测试性的来源 | 阶段函数 + 逐行 golden 测试；面板过滤器直接复用同一张表 |
| | **人永远不被机器挤掉**：人的消息额外占一个 slot | 并发计数器必须区分 `by=person` |
| | 投递三段式：发出 ≠ 读到 ≠ 回合结束 | runtime 必须实现 `delivered / read / turnEnded` 三个回调，否则归属与去重全错 |
| | 反直觉但正确的规则：prepare 失败**不自动重试**（目录存在即视为已准备） | 这类规则一律照抄，不"优化" |
| **004 提示词** | 提示词是**可版本化的团队资产**，升级要能 diff | 覆盖存 base-hash + "默认已更新"提示；缺占位符拒绝保存，超长拒绝而非截断 |
| | 强制力排序：代码强制 > 内置段 > 规则 > 团队段 > **外部文本是数据** | 外部文本永不改变规则——这是防提示注入的结构性保证 |
| **005 面板** | 三个问题 = 信息架构；一个元素不回答三问就删 | 原生窗口的 Overview 首屏只放"需要我 / 健康 / 历史" |
| | **诚实状态**：计数必须等于它链接的列表长度 | VM 的 `Count` 由 `len(Rows)` 派生，不另算 |
| **006 部署** | 退出码语义分层：**1 可重启修复，2/3 重启无用** | `zeokeel status` 照抄 0/1/2/3，否则 watchdog 会疯狂重启 |
| | 程序化重启必须**尊重"人主动停"** | `--respect-stop` 语义 + 持久 stop marker |
| | Troubleshooting 表 = 一组"可读错误码 + 修复动作" | 从第一天就用 `UserError{Code, Place, Values}`，该表直接当验收清单 |
| | 安全基调诚实：shim **不是**安全边界，要隔离请用独立 OS 账户 | 我们不吹沙箱，文档写明边界 |
| **007 架构** | 单写者 + 五个窄接口（`EngineFor*`） | Go 用 5 个小 interface，禁止一个大 `Engine` |
| | 纯读查询层（`views/*`）与 UI 解耦 | 这正是我们的 `internal/view`（VM 层）；Go 里能做到零反射 |
| | `MemoryRepositories` 与 SQLite 语义对齐 | 领域测试用内存实现，避免为测试起数据库 |
| **008 扩展** | 新增 Source 的 11 步清单 = 接口设计的验收标准 | `sources.Kind` 用 interface + 可选接口（type assertion），不是一个大结构体 |
| | **稳定 ID 先行**：改了 ID 就断历史 | Source 实现第一步先定 ID 映射，写进文档再写代码 |
| | 游标必须在 `receive` 全部成功后才推进；中断轮次游标不动 | 轮询循环的验收项 |
| | `scope` **只能依赖 `meta` + 配置** | 因为 revalidate 会拿存下来的 meta 再调一次；违反会导致恢复时判定漂移 |

**贯穿九篇的一条主线**：原型把"智能"尽量压缩到最小面积，其余全部做成**确定性的、可测试的、可解释的状态机**。LLM 只负责"判断这件事该不该做、怎么做"，不负责记住状态、不负责保证幂等、不负责决定权限。我们自研 agent runtime 时最该继承的就是这条——**runtime 是仆人，领域是主人**。

---

## 2. 关键决策（ADR）

| # | 决策 | 选择 | 理由 | 主要风险 |
| --- | --- | --- | --- | --- |
| ADR-1 | 进程形态 | **单二进制桌面 app**；关窗=隐藏，托盘常驻；`--headless` 无窗口运行 | 用户选定；mygo `Tray` + `OnWindowAllClosed` 可等价实现原型的 7×24 常驻 | 托盘在 Linux 依赖 AppIndicator，可能无点击回调 → 保留菜单与全局快捷键兜底 |
| ADR-2 | Agent 执行 | **进程内自研 Runtime**，不 subprocess 外部 CLI | 原型的核心语义（回合中 steer/inject、已读回执、中断、恢复、后台作业）**只有进程内实现才能等价**；外部 CLI 会丢掉"回合中插话"和"读回执"，进而破坏 follow-up 归属与"未读消息回 Eval" | 自研质量决定产品上限（§5、§11-3） |
| ADR-3 | 角色 | 保留**两个 agent 角色**：**Router**（代码里叫 `orchestrator`，主会话，全局唯一持久）与 **Worker**（每 Ticket 一个会话）。Router 用**轻量模型**、Worker 用**最强模型** | 与官方双层架构 1:1（§4.5）；能力差异用**工具白名单**表达而非两套代码 | 两个角色共享 runtime，复杂度增量小 |
| ADR-4 | 领域层 | **照搬**：状态机 + 内容派生 ID + durable Effects + Outbox + 三段式投递 | 这些是原型踩坑后的产物，简化必遭反噬（附录 A 有清单） | 移植偏差难发现 → 用原型的 spec 用例做 golden 测试 |
| ADR-5 | 持久化 | **SQLite**，8 张表 + KV，WAL + `synchronous=FULL`，单写者 | 与原型 schema 同构，移植测试可直接对照；内置事务/排序/崩溃安全 | 驱动选择见 ADR-5b |
| ADR-5b | SQLite 驱动 | **`modernc.org/sqlite`**（纯 Go） | mygo 自带 `plugins/sqlite` 走 purego dlopen 预编译 C 库，需要随包分发 `.so` 且裸 `go run/test` 会联网下载；modernc 零构建摩擦、天然多平台 | 二进制 +10–15 MB、性能略低（单机场景无感） |
| ADR-6 | Source 扩展 | **编译期注册**（Go interface） | 单机桌面不需要热插拔；类型安全、无协议版本问题 | 新增 Source 需重新编译 → 可接受 |
| ADR-7 | GitHub 接入 | **`gh` CLI 包装**（首版），抽象 `Channel` 接口 | 复用登录态与现成能力，与原型一致，agent 侧本来也要用 `gh` | 依赖用户装 `gh` 并登录；CLI 输出格式变化 → 接口收敛在 `internal/sources/github/gh` |
| ADR-8 | UI 数据流 | **不可变快照 + `atomic.Pointer` + `Invalidate`**；UI→core 走命令总线 | 唯一能同时满足 mygo 主线程约束与高事件量的方案（§6.2） | 快照构建成本 → 分片快照 + 抖动合并 |
| ADR-9 | mygo 隔离 | 所有 `mygo`/`ui` 引用收敛在 `internal/ui` 适配层 | mygo 是 0.7.x，无 API 兼容承诺，已发生过 `*Element → Element` 破坏性迁移 | 适配层维护成本（可控） |
| ADR-10 | 无头模式 | 同一二进制 `--headless` 不初始化 mygo，只跑 core + CLI | core 与 UI 完全解耦后**免费得到**；让服务器/CI 部署与端到端测试成为可能 | 无头下无面板 → 靠 `zeokeel status` + 日志 |
| ADR-11 | Agent 内核 | **`cloudwego/eino` 全家桶（含其 `adk` 子包与 `adk/prebuilt`）+ `openai-go/v3` + 官方 MCP go-sdk**；会话/投递语义自研 | 实测 12 个候选：eino 自带编码 Agent 脚手架、压缩、结果截断落盘，16 MB 依赖；手写 loop 生产级要 2200–3600 行 | eino 仍 alpha（锁版本）；默认工具检查器会静默吞 tool_call（必须改） |
| ADR-12 | 凭据 | **目标**：系统钥匙串（`mygo.Secrets`），无 keyring 时降级为 0600 文件并提示。**现状（2026-11-03）**：未实现——API Key 明文存 SQLite `kv`（`runtime/onboarding.go` → `store/settings.go`），只有 DB 0600（`store/store.go`）；worker 的 shell 仍继承操作者环境（`internal/agent/tools.go` 的 `os.Environ()`，`*KEY*/*TOKEN*` 未剥离）；备份数据库即带走密钥 | 不落盘明文（目标）；当前缺口在 1.0 前必须关闭 | owner: fix-runtime（keychain + env 剥离），RELEASE F-8 |
| ADR-13 | **零 cgo（硬约束）** | **全项目禁止 cgo**：唯一构建方式 `CGO_ENABLED=0`，且任何传递依赖不得使用 cgo；CI 硬断言 | 用户要求；也是 mygo 的硬规则。收益：任意机器交叉编译、无 C 工具链依赖、供应链面小 | 「零 cgo」≠「纯静态」：mygo 的 purego/fakecgo 仍动态链接 glibc，不能进 Alpine/scratch（见 §8.3） |
| ADR-14 | **双层架构 + 多工作区** | 官方 Router/Worker 双层：**Router 全局唯一**（全局上下文、两层过滤、Eval、跨工作区派发、统一回复、轻量模型）；Worker 每 Ticket 一个、上下文隔离、绑定所属工作区目录、不直接对外说话。**Engine 全局唯一**，实体带 `workspace_id` 属性 | 官方文章明确"Router 只分派，Worker 只干活"，原型亦如此（worker 会话绑定 worktree 目录）。把 Engine 按工作区切开会把 Router 的大脑切碎，跨区派发无法原子 | 跨工作区派发必须逐区授权，否则越权 |

---

## 3. 总体架构

### 3.1 进程模型

```
┌──────────────────────────── 单二进制 zeokeel ────────────────────────────┐
│                                                                          │
│  ┌──────────────── 主线程（mygo 事件循环）────────────────┐               │
│  │  view(c *ui.Context)  每帧纯函数：快照 → 控件            │               │
│  │  输入轮询：Clicked() / Changed() / Shortcut()           │               │
│  │  仅持有 UI 状态：选中项、过滤词、滚动、弹窗开关          │               │
│  └───────────────▲──────────────────────────┬────────────┘               │
│         Invalidate() / Update()              │ 命令总线（非阻塞投递）        │
│  ┌───────────────┴──────────────────────────▼────────────┐               │
│  │              internal/ui  快照层（只读投影 + 命令）      │               │
│  └───────────────▲──────────────────────────┬────────────┘               │
│                  │ atomic.Pointer[Snapshot] │                             │
│  ┌───────────────┴──────────────────────────▼────────────┐               │
│  │                 core 运行时（后台 goroutine）            │               │
│  │  Engine(单写者) │ Effects 队列 │ Passes(2s) │ Outbox      │               │
│  │  Sources 轮询   │ Eval/Policy  │ Executor  │ Agent Runtime│               │
│  │  SQLite         │ 健康/日志     │ 定时器     │ 工具执行     │               │
│  └───────────────────────────────────────────────────────┘               │
└──────────────────────────────────────────────────────────────────────────┘
外部依赖：gh CLI · lark-cli · git · LLM API(OpenAI 兼容/DeepSeek) · 系统钥匙串
```

**多工作区在进程内的形态**（见 §4.5）：`Runtime` 持有**一个全局 Engine**（单写者）+ **一个 Router 会话** + N 个 Worker 会话；所有实体带 `workspace_id` 属性。UI 的「工作区切换」只是换一个**只读投影的作用域**，不重启 runtime、不暂停在跑的 Ticket。

**没有 HTTP、没有 IPC、没有序列化边界。**这是单二进制相对原型（插件 + HTTP API + React 轮询）最大的简化：面板读的是内存里的 Go 结构体，不是每 2 秒轮询一次 JSON。

### 3.2 分层与依赖规则

```
cmd/zeokeel                 ← 唯一装配处
  ├── internal/runtime      组合根：装配、passes、健康、停机、单实例
  ├── internal/ui           mygo 适配层：窗口/托盘/视图/控件（唯一 import mygo/ui）
  │      └── internal/view  只读投影模型（ViewModel，pure data）
  ├── internal/executor     Assignment ↔ Agent Session 的桥（等效原型 DshExecutor）
  ├── internal/agent        自研 Agent Runtime（provider/loop/tools/session/compact）
  ├── internal/sources      github / feishu（实现 sources.Kind 契约）
  ├── internal/engine       Engine 单写者：领域命令与状态迁移
  │      └── internal/domain  纯领域类型/状态机/ID/reason（零 I/O、零 mygo）
  ├── internal/policy       gate / admission / policyVersion / revalidate / effects
  ├── internal/store        SQLite 实现
  ├── internal/effects      durable 副作用队列
  ├── internal/outbox       出站幂等队列
  └── internal/prompts      part 组装 / 覆盖 / 版本
```

**依赖规则（CI 用 `go vet` + 静态检查强制）：**

| 包 | 可以 import | 禁止 |
| --- | --- | --- |
| `domain` | 仅 stdlib | 任何其他 internal 包、任何 I/O |
| `engine` | `domain`、`policy`、`store` 的窄接口 | `ui`、`agent`、`sources`（通过 ports 反转） |
| `sources` | `domain`、`engine` 的 `ForSources` | `ui`、`agent` |
| `agent` | 仅 stdlib + SDK | `engine`、`ui`、`domain`（用回调/接口） |
| `ui` / `view` | `view`、`ui`、命令接口 | `store`、`agent`、`sources`（否则就是 UI 直接摸数据库） |
| `executor` | `agent`、`engine` 的 `ForExecutor` | `ui` |

这条规则是"面板能在没有窗口时被测试"和"`--headless` 免费"的前提。

### 3.3 一次请求的完整时序（Trigger → PR）

```
 Source 轮询(60s)                    Engine(单写者)                  Agent Runtime
 ───────────────                     ──────────────                  ─────────────
 gh notifications/issues
   │ 产出 Trigger
   └──► receive(tx) ──► 去重(key) ──► scope 过滤 ──► 扇出到订阅 Ticket
                                       │
                                       └─ followupOwners 为空 ──► queueForEval
                                                                        │
 Passes(2s) ──► evaluateDue ──► 逐条 gate(确定性) ──► candidates ───────┤
                                                                        ▼
                                              orchestrator 会话：candidate 卡片
                                                 │ 工具: eval_read / eval_decide
                                                 ▼
                                     decide → Decision(accept) + Mandate
                                                 │
                                     Tickets.create → pending Assignment
                                                 │ enqueue('launch')
                                                 ▼
 launch effect ──► slot 预留 ──► prepare 目录(10min) ──► worker 会话 ──► prompt
                                                                        │
                                              worker 工具: bash/fs/edit/report/ack
                                                                        ▼
                                               commit/push/gh pr create（agent 自己干）
                                                 │ orchestrator_report(Pull request: <url>)
                                                 ▼
                                     claimPull → follows 绑定 → ready Report
                                                 │
                                     reportRelay → orchestrator → reply 工具
                                                 ▼
                                     Outbox(幂等) ──► gh 评论/飞书回复
```

关键实现提示：**`Engine` 的所有命令同步、单事务**；副作用（启动会话、投递消息、发评论）一律通过 **durable Effects** 在事务提交后才执行。崩溃安全来自"命令已提交 → effect 行已落库 → 重启重跑"。

---

## 4. 领域层移植

### 4.1 类型映射（**目标 vs 现状**，2026-11-03 Wave 2 后）

> 第二列是设计目标，第三列是代码事实。✅ 表示有可核对类型；🟡/❌ 是剩余类型债。

| 原型（TS） | Go 目标 | 现状（代码证据） |
| --- | --- | --- |
| `Source` / `SourceKind<C>` | `domain.Source` / `sources.Kind` | 🟡 `sources.Kind` 完整（`internal/sources/source.go:78-104`），但**没有 `domain.Source` 类型**：Source 在领域里是字符串 + `sources.Kind` 实例 |
| `Trigger` | `domain.Trigger` | ✅ `internal/domain/trigger.go` |
| `Thread` | `domain.Thread` | 🟡 **无 `domain.Thread` 类型**：Thread 以 `domain.ThreadRef`/`ThreadKey`（`trigger.go:49`、`ids.go:51`）+ engine 内部 thread map（`engine.go:682`）表达；Eval 已按 Thread 批量 |
| `Decision{kind, reason[], audit}` | `domain.Decision` | ✅ `internal/domain/ticket.go:111`（kind/reason；proposal 已随 rc.4 删除；无独立 audit 字段，审计走 Decision 表） |
| `Mandate{strength, policyVersion, basis, actor, scope}` | `domain.Mandate` | ✅ `ticket.go:101`；strength 由确定性 gate 产出（`engine/gate.go:184`），不再来自模型 |
| `Ticket{stage, ended, assignments, channels, inputs, follows, ...}` | `domain.Ticket` | ✅ `ticket.go:400-470`：`Inputs`/`Delivery`/`Follows`/`FollowupOwners`/`Attention`/`EvalID` 均已落地；`stage` 仍是派生（§4.2） |
| `Assignment{state, report}` | `domain.Assignment` | ✅ `ticket.go:184` |
| `Report{state, summary, at}` | `domain.Report` | ✅ `ticket.go:168` |
| `Evaluation` | `domain.Eval` | ✅ `ticket.go:469`（两阶段 Eval 的持久队列行） |
| `key(...)` | `domain.Key(parts ...any) ID` | ✅ 内容派生 ID（`internal/domain/ids.go:39`），禁止 UUID |
| `Reason{code, values, triggerId}` | `domain.Reason` | ✅ `ticket.go:20` |
| `UserError` | `domain.UserError{Code, Missing}` | ❌ 不存在（grep 无）；CLI/面板错误仍是自由文本 + 退出码 |
| （新增，非原型）`AdmissionRow` / `AdmissionCell` / `Strength` / `Basis` / `Rule` / `Proposal` | 领域值对象 | ✅ `ticket.go:118-200,315`：确定性授权的承载类型；这是相对原型的**有意扩展**，须在设计 ADR/本表登记 |

**不要用 UUID 替换 `key()`。**所有幂等路径（trigger 去重、accept 去重、input 去重、ack/reply 去重）都建立在"同一语义输入 → 同一 ID"之上。

### 4.2 Ticket 阶段：查表函数，不是状态字段

原型最优雅的一处设计：Ticket 的"阶段"**不是存储的状态，而是根据事实推导出的视图**。这消除了一整类状态不同步的 bug，也让面板过滤器有了可检查的定义。

```go
// domain/stage.go
type Stage string

const (
    StageEnded         Stage = "ended"
    StagePersonDelivery Stage = "person/delivery"
    StagePersonAnswer   Stage = "person/answer"
    StagePersonReview   Stage = "person/review"
    StagePersonBlocked  Stage = "person/blocked"
    StagePersonFailed   Stage = "person/failed"
    StageWorkingQueued  Stage = "working/queued"
    StageIdle           Stage = "idle"
    StageWorking        Stage = "working"
)

// StageOf 按表中自上而下首个匹配返回；改顺序就是改语义，必须与原型表一致。
func StageOf(t *Ticket) Stage {
    if t.Ended != nil { return StageEnded }
    if t.Delivery != nil { return StagePersonDelivery }
    if t.Attention != nil { return StagePersonAnswer }
    last := t.LastReport()
    switch {
    case last != nil && last.State == ReportReady:   return StagePersonReview
    case last != nil && last.State == ReportBlocked && waitsForPerson(t): return StagePersonBlocked
    case last != nil && last.State == ReportFailed && waitsForPerson(t):  return StagePersonFailed
    case t.NoAssignmentOrPending():                  return StageWorkingQueued
    case last != nil && last.State == ReportWaiting: return StageIdle
    case t.LastAssignmentEnded():                    return StageIdle
    default:                                         return StageWorking
    }
}
```

配套规则（必须一起移植，否则阶段会算错）：
- `waitsForPerson`：最近一次 report 是 blocked/failed **且此后没有人的消息到达**。
- 人的新消息让 blocked/failed 回到通常阶段（多半是 `working`）。
- `canRetry`：`!ended && (waitsForPerson || delivery)`——只有三种阶段出现"重试"。

### 4.3 授权：gate + admission + Mandate + revalidate

四层，缺一层就会出现"越权"或"被弱化策略误伤"：

1. **Gate（确定性，无模型）**：按 Source 的 `scope()` + allowed list + admission table 判定 `strength ∈ {explicit, inferred}` 或拒绝，并给出 reason 码。
2. **Admission table**：行 = 接收范围（GitHub：新 Issue / 新 PR / 评论 / 默认分支 CI），列 = 三个 cell（名单内未 @、名单外已 @、名单外未 @）。cell 可选 `explicit | inferred | ignore`；"未 @"两格要求该 scope `observed=true`。
3. **Accept mode**：`off` / `observe`（默认，评估但只记录 `would_accept`）/ `auto`。
4. **Mandate + revalidate**：Mandate 记录 `strength / policyVersion / basis{via: addressed|followup|admission|listed|bot}`。恢复已结束 Ticket **前**必须 `revalidate(currentPolicy, mandate)`——策略弱化不撤销在跑的 Mandate，只挡恢复。

Go 侧用窄接口把"判定"和"是否还成立"分开：`policy.Gate(...)`、`policy.Revalidate(mandate, current)`。

### 4.4 必须保住的不变量（Go 移植 checklist）

写进 `internal/domain/invariants_test.go`，每条一个测试：

1. 同一 Trigger 接受两次 → 只产生一个 Ticket（ID 幂等）。
2. 每个 Thread 同时最多一个 Eval；期间的 Trigger 合并进下一批。
3. 人的消息**优先扇出**；只有 `followupOwners` 为空才进 Eval。
4. `settled(decision)`：stop / unavailable / 带 `stopNotExplicit|stopUnknown|routed` 的决策**不算**已处理完这条请求。
5. Ticket 结束时未读的人消息要 `letGo`（放回 Eval），但"人 stop 结束"的那条不自动重评。
6. `waitsForPerson` 期间**自动输入被扣住**（CI/bot/note 投递但不唤醒）。
7. 策略变更**不撤销**已发的 Mandate，只写 policy-changed Input。
8. 同一 Ticket 对同一会话的出站消息保序；前者连续失败 3 次才放行后者。
9. `followsDone()` 且 `!unfinished()` 才允许按 PR 结局结束 Ticket；`merged`/`closed` 取最后一次 close 的 outcome。
10. 三个投递回调各司其职：`delivered`（进隧道）、`read`（进模型历史）、`turnEnded`（后台静默）。
11. 只有最终 Report 能从 Executor 侧结束 Ticket。
12. reason / error 一律"码 + 模板 + 参数"，不存自由文本。

---

### 4.5 双层架构（Router / Worker）与工作区

**官方设计原文（技术文章《Super-agent 设计探索》）：**

> **双层架构：Router 只分派，Worker 只干活。** 常驻的 Router 承接所有入口的人类交互，维护全局上下文，本身不执行具体任务，甚至可以不配 Bash 工具。Worker 之间上下文隔离，挂载完整执行工具，专注单一任务，不直接对外说话，结果由 Router 统一回复。团队实践中 Router 用较轻量的模型就够，Worker 则需要能力最强的模型。

原型的实现在 DSH 里印证了这一点：**Router 会话的 workspace 是 `stateDir`，每张 Ticket 用 `executionRoot/<worktree>` 目录注册成 workspace，再据此建 `worker-<ticketId>` 持久会话**（`src/app/runtime.ts`、`src/executor/dsh.ts`、`src/app/sessions.ts`）。也就是说：**一个大脑 + 多双手，每只手绑定一个目录**。

**ZeoKeel 把它扩成三层：官方两层 + 工作区作为执行作用域。**

```
                     ┌──────── Router / 超级 Agent（全局唯一）─────────┐
人类交互 ─ Trigger ─►│ 全局上下文 · 两层过滤 · Eval · 决策 · 跨区派发 │
                     │ 统一回复 · 轻量模型                             │
                     └──────┬────────────────────────┬───────────────┘
                            │ Ticket(ws=A)           │ Ticket(ws=B)
                     ┌──────▼──────┐          ┌──────▼──────┐
                     │ Worker A    │          │ Worker B    │  上下文隔离
                     │ cwd=ws A 根 │          │ cwd=ws B 根 │  完整工具
                     └─────────────┘          └─────────────┘  不直接对外说话
                            │                        │
                     ┌──────▼────────────────────────▼──────┐
                     │  Engine（全局唯一，单写者）+ 单 SQLite │
                     │  所有实体带 workspace_id 属性          │
                     └──────────────────────────────────────┘
```

#### 三条纠正（相对上一版草案）

1. **Engine 是全局唯一的，不是每工作区一个。** 上一版写"Engine 按工作区实例化"——**那是把 Router 的大脑切碎了**：跨工作区派发会退化成跨引擎调用，破坏单写者与事务性，也让"一个决定同时影响两个仓库"无法原子。正确做法：**一个全局 Engine（单写者）+ 实体上的 `workspace_id` 属性**。工作区是**执行与授权的作用域**，不是并行的状态机。

2. **跨工作区派发是一等能力。** 一个 Trigger 进来，Router 可以决定在**多个工作区各开一张 Ticket**（例如"两个仓库都要升这个内部库"）。**但每张 Ticket 的 Mandate 必须由目标工作区的 Policy 授权**——Router 有权规划，无权越权。这是"能力与语境分离"（002）在多工作区下的自然延伸。

3. **Worker 绑定所属工作区，Router 不绑定。** Worker 的 cwd 在对应工作区根下的 Ticket 目录，上下文隔离，工具完整，但**不直接对外说话**；结果回 Router，由 Router 统一回写 Source。

#### workspace_id 的三条纪律（M0 必须做对）

1. **领域 ID 的第一段是 workspaceID**——`Key(wsID, "github", commentID)`、threadKey `wsID|github|12`。否则仓库 A 的 `#12` 与仓库 B 的 `#12` 撞成同一个 Thread，幂等直接崩。
2. **WorkspaceID 创建时生成并持久化，不用路径派生**。`hash(remote + 绝对路径)` 是错的：用户移动/重命名仓库目录，整段历史就对不上了。（"禁 UUID"纪律针对**业务实体幂等**；Workspace 是本地配置实体，没有内容可言。）
3. **领域 API 不接收 workspaceID 参数**。它是实体的属性，不是调用参数；工作区边界在**构造**与**查询**处生效，不在每个方法签名里。

#### 三个层级不要混

借鉴 DSH 的教训（DSH 的 workspace 只是**会话分组**，模型不可见、零 token），ZeoKeel 要把它分层清楚：

| 层级 | 是什么 | 进不进切换器 |
| --- | --- | --- |
| **用户工作区（项目）** | 用户视角的一个仓库/项目，UI 作用域、Policy 与预算边界 | ✅ 是切换器的一项 |
| **Ticket 执行目录** | 每 Ticket 一个 worktree，在工作区根下 | ❌ 内部实现，**不要注册成用户工作区**（否则侧栏被 ticket 目录淹没） |
| **会话分组** | 用于把会话按目录归组展示 | ❌ 由工作区推导，不做成独立概念 |

#### 工作区提供什么（scope，不是 brain）

工作目录与 worktree 根 · 凭据命名空间（钥匙串 `zeokeel/<wsID>`）· Policy（谁能在该仓库提需求）· 预算与并发槽位 · Source 绑定（游标按 `(workspace, source)`）。

**Source 有两种作用域**（§4.7）：`workspace-scoped`（GitHub / Feishu 群 / Schedule，Trigger 有默认工作区）与 `global`（钉钉私聊 / 本地面板对话，Trigger 无工作区，必须由 Router 路由）。**`workspace` 是 Ticket 的属性，不是 Trigger 的属性。**

**仍然不做**：多用户、多副本、每工作区一个进程、一个窗口混多个工作区的会话、一个工作区绑多个仓库根（monorepo 算一个）。

#### 链式召回（官方第 4 点在多工作区下的含义）

"每个对象都内联下一跳的 ID，Router 可以从一条消息顺着 Thread、Worker Session 一路找回旧任务"——跨工作区时这条尤其重要：Router 必须能从一个工作区的 Trigger 追溯到另一个工作区里做过的相关工作，否则"两个仓库一起改"会各说各话。实现上就是在资源读取里带上 `workspace_id`，并让 Router 的检索默认跨区。

---

### 4.6 工作区简报（Workspace Brief）—— Router 怎么知道每个工作区是做什么的

**先承认一个空白：上一版没有回答这个问题。** 官方原型是**单仓库**，且 `docs/004` 明确写着：

> Executors also read DeepSeek Harness's coding prompt and the repository's `AGENTS.md`; **the main Session does not.**

也就是说官方 Router 对仓库内容是**刻意零认知**的——它只需要看 Trigger 卡片和领域资源就够了，因为只有一个仓库。但到了多工作区，Router 必须做**跨区派发决策**（"这件事该在哪个仓库做"），再对它零认知就是致命的。**这是我们的新问题，没有现成答案可抄。**

#### 为什么不能直接塞仓库内容

Router 的上下文里有 N 个工作区。把每个仓库的 AGENTS.md、目录树、README 都塞进去，官方那句原话就成立了：

> 浪费上下文窗口，就是浪费模型的智能。

所以必须按官方第 4 点 **Inline & Spill + 链式召回** 来做：**常驻一份极小的结构化摘要（inline），需要时再按需展开全文（spill），历史工作靠 ID 链式召回。**

#### Workspace Brief：每工作区一张名片

固定字段、固定格式、**≤300 token**，常驻 Router 上下文：

```
## ws_acme_webapp · acme/webapp · main · TypeScript/Go
目的  面向客户的 web 控制台：登录、账单、设置
范围  auth / billing / settings / session
不归  后端 API（在 ws_acme_api）、基础设施（在 ws_infra）
构建  pnpm build    测试  pnpm test（集成需 make test-integration）
约定  AGENTS.md 12 条；PR 必须过 review-required；conventional commits
红线  不得修改 migrations/ 下的历史迁移；发布由 release-manager 执行
近期  3 个开放 PR；最后活动 2 小时前
```

字段分三类来源，**谁能写死定死**：

| 来源 | 字段 | 谁写 | 何时 |
| --- | --- | --- | --- |
| **人写** | `purpose` `scope` `out_of_scope` `constraints` **`keywords`/`aliases`**（客户名、代号、简称——路由靠它） | 人在工作区管理页 | 添加时 + 随时可改 |
| **探测** | `remote` `default_branch` `language` `pkg_manager` `build/test/lint cmd` `dir_summary` `agents_md_summary` `open_prs` `last_activity` | 扫描器 | 添加时"首次索引" + 手动刷新 + 每周 |
| **学习** | `learned`（如"集成测试必须用 `make test-integration`"） | Router / Worker **提议**，人**批准** | 工作中发现 |

**Router 不能静默改写 brief。** 理由同 004 的强制力排序：brief 里有大量来自仓库文件的内容（AGENTS.md、README、构建脚本），这些是**不可信数据**；如果 Router 能凭它们改写路由依据，就等于让外部文本影响授权。提议必须进"待确认"队列，由人在面板批准。

#### Router 的三个工具

| 工具 | 作用 | 成本 |
| --- | --- | --- |
| `workspace_list` | 返回所有 brief（就是上面那段常驻文本） | 常驻，已计入预算 |
| `workspace_read(id, part)` | 按需展开：`agentsmd`(全文) / `tree` / `recent_tickets` / `ci` / `pulls` / `readme` | 调用时才花 token |
| `workspace_propose(id, field, value)` | 提议更新 brief（进待确认队列） | 极低 |

这就是官方的 **Inline（brief）→ Spill（workspace_read）→ 链式召回（recent_tickets 顺着 Ticket/Thread ID 找回旧工作）**。

#### 预算与降级

| 工作区数 | 策略 |
| --- | --- |
| ≤ 10 | 全部 brief 常驻（10 × 300 ≈ 3000 token，可接受） |
| > 10 | 退化为 **index 模式**：每个只放 `名称 + 一句话目的`（≈50 token），需要时 `workspace_read` 展开 |
| 任意 | `stale` 标记：探测事实超过 7 天未刷新，brief 里显式标"⚠ 信息可能过期"，提示 Router 先展开确认 |

#### brief 是缓存，不是真相

真相永远是仓库本身。brief 过期、仓库重构、README 改了而 brief 没跟——都会发生。所以：
- 所有探测字段带 `observed_at`；
- 超过阈值标 `stale`，并在 UI 上可点"刷新";
- Router 的提示词里有一条硬规则：**brief 与 workspace_read 冲突时，以 workspace_read 为准**。

#### 同一份 brief 双用途

它同时是**工作区切换器的描述文案**（原型里那一行 `~/code/webapp · github.com/acme/webapp` 就是 brief 的极简版）。所以人写目的字段不是额外负担——写一次，Router 和 UI 都用。

#### 路由也是两级（复用官方的 Fast Path / Slow Path 思想）

| 级 | 做什么 | 成本 |
| --- | --- | --- |
| **候选召回**（确定性） | 用 brief 的 `keywords`/`aliases`/`scope` 做倒排索引，把 N 个工作区缩到 1–3 个 | 微秒级，0 token |
| **Router 判断**（语义） | 拿候选的 brief + 必要时 `workspace_read` 展开，判断该派给谁 | 一次轻量调用 |
| **拿不准** | `ask_user_question` 列出候选让 operator 选 | 绝不猜 |

索引是 brief 的派生物（brief 变更时重建），不单独存储——避免两处真相。

#### 路由决策流程（Router 怎么用 brief 决定派给谁）

```
Trigger 到达
  ├─ 来自某个 Source → 该 Source 绑定的工作区是**默认目标**（绝大多数情况到此为止）
  ├─ 文本里显式提到别的仓库（URL / 名字 / 缩写）→ 跨区派发候选
  ├─ 请求本身跨仓库（"两个仓库统一升级 X"）→ 多个目标工作区
  └─ 依据：brief 的 purpose / scope / out_of_scope
        ├─ 明确落在某工作区的 scope 内 → 派给它
        ├─ 落在 out_of_scope → 排除
        └─ 拿不准 → ask_user_question（绝不猜）
```

**每个目标工作区的 Ticket 仍需该工作区 Policy 单独授权**（§4.5 第 2 条），brief 只影响"派给谁"，不影响"能不能做"。

---

### 4.7 端到端例子：同事从钉钉要一份客户数据

> 场景：同事在**钉钉私聊**里说「帮我把 acme 这个客户上个月的用量数据发我一下」。
> 对应工作区 `ws_acme_data`——注意它**不是代码仓库**，而是 `~/data/acme/`：数据导出目录 + 取数脚本 + 报表模板。

这一步就把前面几节没考虑到的三个问题全逼出来了。

#### 走一遍完整链路

```
① 接入     钉钉 Source（新）收到私聊消息
            → Trigger{ id: Key("global","dingtalk",msgID), actor: dingtalk:zhangsan,
                       human: true, addressed: direct, text: "…acme 上个月用量…" }
            ⚠ 这个 Trigger 不属于任何工作区——钉钉私聊是跨工作区的入口

② 准入     钉钉 Source 的 allowed list（张三在名单）+ addressed=direct
            → strength = explicit
            ⚠ 这只代表「他能提需求」，不代表「他能拿客户数据」

③ 路由     Router 读 workspace_list（brief 常驻）
            brief.keywords = [acme, 客户, 用量, 报表, 计费]
            → 候选召回命中唯一工作区 ws_acme_data
            → workspace_read(ws_acme_data, "tree") 确认取数脚本确实在
            → 若命中多个 / 一个都不中 → ask_user_question（绝不猜）

④ 授权     Mandate(ws=ws_acme_data) 由该工作区的 Policy 签发
            ⚠ 数据敏感 → 追加第二道门：ask_user_question 问 operator
              「要把 acme 2026-10 用量发给钉钉的张三吗？」→ 人批准

⑤ 派发     Ticket(ws=ws_acme_data) + Assignment
            Worker cwd = ~/data/acme/
            工具白名单：bash + sqlite 查询 + present（不带 git/gh）

⑥ 执行     Worker 跑取数脚本 → 生成 acme-2026-10.csv + 一张趋势图
            → present 交付物 → report(ready, "已生成 acme 10 月用量 CSV 与趋势图")

⑦ 回写     Router 统一组织回复（摘要 + 附件）
            Channel = 钉钉 → 发回原私聊（幂等键 = 原始消息 ID）
            ⚠ 绝不能发到 GitHub；也绝不能在别的会话里说

⑧ 落地     outbox 记录回执；面板里可审计「谁要了什么数据、谁批准的」
```

#### 它逼出来的三个修正

**修正 1：Source 有两种作用域，`workspace` 是 Ticket 的属性而不是 Trigger 的属性。**

上一版写「Source 绑定在工作区上」——对 GitHub 成立（一个 Source 盯一个仓库），但**钉钉私聊是跨工作区的**。
正确模型：

| Source 作用域 | 例子 | Trigger 的 `default_workspace` |
| --- | --- | --- |
| **workspace-scoped** | GitHub（一个仓库）、Feishu 群（一个团队）、Schedule | 有：该工作区 |
| **global** | 钉钉私聊、本地面板对话、跨团队群 | 无：必须由 Router 决定 |

推论：
- **Trigger 可以没有工作区**；`Decision` 必须给出目标工作区；`Ticket`/`Mandate` 一定属于某个工作区。
- 幂等 ID 的第一段不再总是 wsID，而是 **scope**：`Key(scope, kind, id)`，`scope` = `ws_xxx` 或 `"global"`。
- 这反而更符合官方语义——官方 Router 本来就是"接到事件再决定派给谁"。

**修正 2：工作区不一定是代码仓库。**

「这个客户的数据在某个工作区里可以获取到」意味着工作区的主语是**一个本地根目录 + 它能提供什么**，仓库只是其中一种。

```go
type Workspace struct {
    ID    WorkspaceID
    Root  string            // 本地根目录，唯一必需
    Kind  WorkspaceKind     // repo | data | docs | mixed
    Brief Brief
}
```

`Kind` 决定 Worker 的工具白名单：`repo` 给 git/gh；`data` 给 sqlite/数据脚本、**不给** git push；
`mixed` 两者都给。brief 的 `facts` 要能描述「数据从哪来、怎么取」，而不只是构建命令。

```yaml
# ws_acme_data 的 brief（数据型工作区）
目的  客户 acme 的用量数据、计费报表与对账
范围  acme / 用量 / 计费 / 对账 / 月报
关键词 acme, ACME, 艾克米, 用量, billing, usage
数据源  ~/data/acme/exports/*.sqlite（每月 1 日同步）
取数    ./scripts/query_usage.sh --month YYYY-MM
约束  只发汇总与趋势，不导出明细行；对外必须经 operator 确认（ask_user_question；无 proposal 审批）
```

**修正 3：数据敏感需要「第二道门」——代理授权，这是原型没有的。**

原型只有一道授权：**「你能不能让我干活」**。但这里还有第二问：**「我能不能用 operator 的身份，把 X 的数据发给 Y」**。
agent 用的是 operator 的凭据，能读到 operator 能读的一切——同事的权限被"代理放大"了。

首版不搞细粒度数据 ACL（成本高、易错），用最简单可靠的办法：

| 做法 | 说明 |
| --- | --- |
| **默认需要 operator 确认** | brief 里 `kind: data` 或 `constraints` 标了敏感 → Ticket 通过 `ask_user_question`/Attention 让人确认（不是 Decision proposal 审批） |
| **批准记录进审计** | 「谁要的 / 要什么 / 谁批的 / 什么时候」写进 Ticket，面板可查 |
| **Worker 无外发工具** | 数据型 Worker 只有 `present`，没有 `reply`——**它自己发不出去**，必须经 Router |
| **汇总优先** | brief 的约束写死"只发汇总不发明细"，Worker 的提示词里重复一遍 |

#### 这个例子还验证了 §5.9

钉钉是**新增入口，但没有新增领域概念**：
- 钉钉 = 一个新的 `Source` kind（与 Feishu 同构，Channel 负责回写）
- "要数据" = `Ticket` + `present` 交付物，不是 PR
- "批准" = 已有的 `Attention`（`ask_user_question`）
- 回写 = 已有的 `Outbox` + `Channel`

`Engine` 一行不用改。

---

### 4.8 IM 接入：一个抽象，N 个驱动

钉钉那个例子不该是一次性代码。飞书、钉钉、企业微信、Slack、Telegram 在领域层是**同一个东西**：会话 → Thread，消息 → Trigger，发言 → Channel。差异只在协议。

#### 领域层零改动

| 领域概念 | 每个 IM 的映射 |
| --- | --- |
| Thread | 一个单聊 / 一个群 / 一个话题（`chat_id` + 可选 `root_id`） |
| Trigger | 一条消息（含发送者、是否 @ 我、是否有附件） |
| Channel | 发回同一个会话（幂等键 = 消息 id） |
| Policy 的 allowed list | **该 IM 自己的用户 id 空间** |
| admission table 的行 | 每个群 / 单聊 / 其他群 |

**身份不跨 IM 映射。** 这是 001 的硬纪律：*"A person is not a domain concept… That one person has accounts on several Sources is prompt text that the agents read when they need it."* 同一个人在飞书和钉钉是两个 actor，要各自进各自的白名单。想建立关联，就写成一个团队 skill（`identities`），由 agent 读——**不是**在代码里做映射表。

#### 桌面应用的硬约束：不能用公网 webhook

原型跑在服务器上，可以收事件推送。**桌面 app 在 NAT 后面，没有公网入口**。所以：

| 接入模式 | 可用性 | 谁用 |
| --- | --- | --- |
| **长连接**（WebSocket / Socket Mode / Stream 模式） | ✅ 桌面首选 | 飞书、钉钉、Slack |
| **长轮询**（getUpdates） | ✅ 桌面可用 | Telegram |
| **公网 webhook** | ❌ 桌面不可用（除非用户自己配内网穿透） | 企微部分能力、Teams |

这条直接决定了 driver 的接口形状：**接收端以 `Subscribe(ctx)` 长连接为主，`Poll(ctx, cursor)` 作为兜底。**

#### 接口设计

```go
// internal/im —— 所有 IM 驱动的公共契约
type Provider interface {
    Name() string                    // "feishu" | "dingtalk" | "slack" | "telegram"
    Capabilities() Capabilities
    Self(ctx context.Context) (Identity, error)              // 机器人自己的 id/名字
    Members(ctx context.Context, chatID string) ([]Member, error) // 用于白名单同步
}

// 接收：实现其一或两者
type Receiver interface {
    Subscribe(ctx context.Context, out chan<- RawEvent) error   // 长连接，桌面首选
}
type Poller interface {
    Poll(ctx context.Context, cursor Cursor) ([]RawEvent, Cursor, error)
}

// 发送与读取
type Sender interface {
    Send(ctx context.Context, msg Outbound) (Receipt, error)   // 幂等键幂等
    ReadThread(ctx context.Context, ref ThreadRef, cursor string) (Page, error)
    FetchMedia(ctx context.Context, ref MediaRef) ([]byte, string, error)
}

// 能力描述：决定 Router 怎么回信、Worker 能做什么
type Capabilities struct {
    Markdown, RichCard, Files, Threads bool
    Mentions, ReadReceipt              bool
    MaxTextLen                         int
    SupportsEdit                       bool   // 能否撤回/编辑已发消息
}
```

**为什么把 `Capabilities` 单列**：回信格式要按渠道自适应。飞书支持卡片，Telegram 只认少量 Markdown，企微群机器人只认纯文本 + 有限 markdown。Router 的提示词里注入当前渠道的能力，**不要写一份"万能消息"**——这是最容易让 agent 在 IM 里出洋相的地方。

#### 每个 IM 的接入方式与成本

| IM | 接收 | 发送 | 话题/Thread | 成员 API | 接入成本 | 备注 |
| --- | --- | --- | --- | --- | --- | --- |
| **飞书** | 事件订阅**长连接**（WebSocket） | 消息/卡片/文件 | ✅ 群内话题 | ✅ | 中 | 原型已验证（`lark-cli`）；桌面友好 |
| **钉钉** | **Stream 模式**长连接 | 消息/卡片/文件 | 部分 | ✅ | 中 | 企业内部机器人，无需公网 |
| **Slack** | **Socket Mode** 长连接 | Block Kit / 文件 | ✅ 原生 thread | ✅ | 中 | 长连接需 app-level token |
| **Telegram** | `getUpdates` 长轮询 | MarkdownV2 / 文件 | ✅ reply 链 | 部分 | **低** | 最适合做第一个 driver 验证抽象 |
| **企业微信** | ⚠ 回调需公网；群机器人只能发 | 应用消息/群机器人 | ❌ | ✅ | 高 | 桌面场景需内网穿透，**建议首版不做** |
| **Teams** | ⚠ 需公网 + Graph 授权 | Graph | ✅ | ✅ | 高 | **建议不做** |

**建议顺序**：先做 **Telegram**（最简单，能最快验证抽象是否成立）→ **飞书**（原型已验证，且有需求）→ **钉钉**（用户场景）→ 其余按需。**每多一个 driver ≈ 400–800 行**，领域层零改动。

#### 一个 IM = 一个 Source kind，而不是一个大 kind

看似"合并成一个 `im` kind"更省，但**不要这么做**：

| 方案 | 问题 |
| --- | --- |
| 一个 `im` kind + `provider` 字段 | kind 是稳定 id，出现在 Trigger.source、Thread key 前缀、settings 块、Policy 里。合并后不同 IM 的**账号、白名单、admission 行会混在一个配置空间**，设置表单也要按 provider 分支，反而更复杂 |
| **每个 IM 一个 kind，共享 `internal/im` 实现** ✅ | 配置、白名单、权限天然隔离；差异字段各自声明；代码复用靠公共包而不是靠合并身份 |

即：`feishu` / `dingtalk` / `slack` 各自注册一个 kind，内部都调 `internal/im` 的同一套流程（翻译 → scope → channel → threadContext → policy → environment），这正好是 008 的 Source 契约十一步。

#### 回写也必须复用

钉钉例子里的 `Channel` 不是每个 IM 重写一遍：`Sender.Send` + 幂等键 + `sent`/`gone` 语义是公共的，driver 只实现"把这个结构体发出去"。**Outbox 的重试、保序、永久回执对所有 IM 一样。**

---

### 4.9 单聊 / 群聊 / 通知：渠道决定不了意图

前两节假设"来消息 = 有请求"，这是个错误假设。同一个 IM 里：

- 单聊可能是**请求**（"帮我要 acme 的数据"），也可能是**通知**（"我明天休假"）；
- 群里 @ 我可能是请求，也可能是**公告**（"@所有人 今晚 8 点发版"）；
- 没 @ 我的消息几乎都是**讨论/通知**，但恰恰可能是在跑的 Ticket 需要的上下文；
- CI / 机器人 / 系统消息是**纯通知**，但可能要唤醒在跑的 Ticket。

**所以不能按渠道类型分流。**必须分三层判定，每层都可查、可覆盖：

| 层 | 性质 | 判什么 | 在哪 |
| --- | --- | --- | --- |
| **事实层** | 确定性 | 谁发的（人/机器人/CI）、在哪个会话（单聊/群/话题/广播）、有没有明确对 agent 说话、是否引用/回复 | Trigger 的字段 |
| **配置层** | 确定性 | 这个会话属于 admission table 的哪一行、该行三个 cell 怎么设 | Policy |
| **语义层** | 模型 | 这是**请求 / 通知 / 闲聊 / 追问**？要不要接 | Eval → Decision |

三层各自的输出都落到 `Disposition` + `reason` 码上，面板可回答"**这条为什么没接 / 为什么接了**"——这是"诚实状态"原则的要求。

#### 关键结论：「通知」不需要新的领域概念

原型已经有位置了。一条通知只是**「扇出但不应授权」**那一格：

| 场景 | human | addressed | 进 Eval？ | 扇出给 Ticket？ | 领域落点 |
| --- | --- | --- | --- | --- | --- |
| 单聊：同事要数据 | ✅ | `direct` | ✅ 是（explicit） | — | `Decision{accept}` |
| 群里：@我修 bug | ✅ | `mention` | ✅ 是（explicit） | — | `Decision{accept}` |
| 群里：没 @我的讨论 | ✅ | `none` | 看表（默认 ignore） | ✅ 是（**纯上下文**） | `Disposition{filtered}` 或 `Decision{ignore}` |
| 群里：同事发公告 | ✅ | 可能 `mention` | **语义层判 ignore** | ✅ 是 | `Decision{ignore, reason: notification}` |
| CI 失败 | ❌ | `none` | 仅 bot list 内 | ✅ 是（**按普通 slot 唤醒**） | 扇出 + `Input{trigger}` |
| 机器人播报 | ❌ | — | 否（除非 bot list） | ✅ 是 | `Disposition{filtered}` |
| 入群/退群/系统事件 | ❌ | `none` | 否 | 否 | `Disposition{filtered}` |

**三件事因此自动成立：**

1. **通知不会制造工作**——语义层判 `ignore`，不建 Ticket。
2. **通知不会丢**——它照样扇出给已订阅的 Ticket 作为上下文（`Subscription` 是无条件扇出，这正是 001 的设计）。
3. **通知能唤醒在跑的工作**——CI 结果走"普通 slot"的 Input 队列（003 原文：*"Other Inputs, such as CI results, wait for a normal slot"*），不会像人的消息那样抢额外 slot。

所以领域层**零新增概念**：只需要给 `Decision` 的 reason 码表加一个 `notification`，给 `Disposition` 加一个 `context`。

#### 要补的只有两个事实字段

`Trigger` 需要更准确地描述"是什么形态的消息"，供三层判定使用：

```go
type Conversation struct {
    Kind    ConversationKind // direct | group | topic | broadcast | system
    ID      string           // chat_id / channel / dm_id
    RootID  string           // 话题根，非话题会话为空
    Name    string           // 群名，用于 UI 与 brief 的 keywords
}

type Addressed struct {
    Mention  bool // 明确 @ 了 agent
    Direct   bool // 单聊
    ReplyTo  bool // 引用了 agent 的消息（不自动等于 addressed，仅作为事实）
}
```

**两条容易踩的规则：**

- **`@所有人` 不算 addressed。** 只统计"明确对 agent 说话"。否则群里一条公告就能把 agent 叫起来干活。
- **`ReplyTo` 不等于 addressed。** 引用一条消息只是事实；是否对 agent 说话要单独判。宁可少认，不可多认。

#### 误判成本是不对称的

| 误判 | 代价 |
| --- | --- |
| 把通知当请求 | 白干活、打扰人、**可能在错误的仓库里改错东西** |
| 把请求当通知 | 漏接一条需求，人问一句就能补上 |

所以默认**宁可漏接也不误接**：
- 默认 `observe` 模式，把"本会接的"记下来给人看（002 的安全阀）；
- 群聊里没 @ 我的消息**默认不进 Eval**（admission 表默认 ignore）；
- 语义层拿不准时，宁可选 `ignore` + reason `unclear`，让人在面板里一键"重新评估"（原型已有 **Evaluate again** 能力）。

#### 出站：通知是发送侧的事，不是接收侧

agent 主动通知（"PR 已就绪"、"CI 挂了，已修"）走的是 `Outbox` + 对应 IM 的 `Channel`，与本节的入站判定无关。区别在**语气与渠道**：通知类消息应该短、不要求回复；请求类回复才需要结论。

---

### 4.10 应答策略：回给同事，还是通知你

前面的授权只回答一个问题：**这活能不能干**。但同事从钉钉/飞书发来一句话时，Router 还要回答第二个：**这句话该怎么回、回给谁**。

原型有这个维度但埋得深：`orchestrator_reply` 带一个 `purpose`（`acknowledgement` / `answer` / `result` / `attention`）。我们把它提升成一等决策。

#### 三种来源，三种处理

| 来源 | 执行 | 回复 | 依据 |
| --- | --- | --- | --- |
| **你自己**（面板输入，或绑定到自己的钉钉 / 微信 / 飞书） | **无条件执行** | 直接回你 | 你拥有这台机器，你的话就是授权 |
| **同事**（在该 Source 的允许名单内；明确请求） | **直接执行**（DSH 主链） | 按本节的四条路 | 不能替你做承诺，也不能替你把数据发出去；旧式待批单已删除 |
| 同事（名单外）/ 机器人 / CI | 不执行 | 不回，或只通知你 | 未获授权 |

**"自己"与"同事"的区分是一个领域事实，不是启发式判断。** 每个 IM Source 在绑定时登记**你自己的账号 id**（`operatorIDs`，可多设备），翻译消息时置 `Trigger.Self`：

```go
// 翻译时判定，之后不再猜
if slices.Contains(cfg.OperatorIDs, ev.Actor.ID) {
    tr.Self = true
}
```

同一条消息，"我自己在钉钉里说的" 与 "同事在钉钉里说的" 走完全不同的路径——这是整个应答策略的分水岭。

#### 同事的明确请求：DSH 直接接单；旧式待批单已删除

DSH 路径：admitted 的**明确**请求由 Eval 直接接单 → Ticket，不先找你批准；推断请求按 accept mode（off/observe/auto，auto 需匹配规则）。
**rc.4 已删除旧式待批单与 `RequireApproval`**；下面卡片只是历史交互示意，不再产生。旧 DB proposal 载入为 ignore：

```
┌ 🔔 需要你决定 ─────────────────────────────── 钉钉 · 张三 ─┐
│ 他说：帮我把 acme 这个客户上个月的用量数据发我一下            │
│                                                            │
│ 要做什么：从 acme-data 导出 2026-10 用量，汇总后回复张三     │
│ 计划：① 跑 query_usage.sh --month 2026-10                  │
│       ② 按客户口径汇总，去掉明细行                          │
│       ③ 回复张三：附件 + 一句话结论                         │
│ 产出：一份汇总 CSV（不含明细）+ 一段结论                     │
│ 派给：ws_acme_data                                          │
│                                                            │
│ ⚠ 敏感：客户数据外发 · 收件人在名单内，但仍需你批准           │
│                                                            │
│ [批准执行]   [只回复不执行]   [拒绝]        ⌘↵ 批准 · esc 拒绝 │
└────────────────────────────────────────────────────────────┘
```

**四个必填字段，少一个就没法判断：**

| 字段 | 内容 | 不写会怎样 |
| --- | --- | --- |
| **他说了什么** | 原文摘要，不加工 | 你得点进去自己读一遍 |
| **要做什么** | 意图 + 1–3 步计划 | 你不知道它要动什么 |
| **产出大概是什么** | 交付物形态（PR / 文件 / 一段回复 / 无产出） | 你不知道批准后会发生什么 |
| **敏感点** | 为什么需要你而不是它自己决定 | 你不知道风险在哪 |

这四项正是用户要的"总结"——**没有它，"需要我处理"就退化成一句"张三找你"，占用注意力却不给价值**（000 的原话）。

#### 审批与派发必须分开

这是两件不同的事，混在一起会让"待办列表"失去意义：

| | 旧式审批（rc.4 已删除） | 派发（DSH 默认） |
| --- | --- | --- |
| 问题 | ~~这件事该不该做？~~ | **这件事正在怎么做？** |
| 有没有 Ticket | ~~没有~~ | 有 |
| 领域位置 | ~~`Decision{kind: propose}`~~（历史 ignore） | `Decision{kind: accept}` + `Ticket` |
| 批准之后 | ~~才创建 Ticket~~ | — |
| 面板分组 | ~~旧式请求（需要批准）~~（已移除） | 需要我处理 / 执行中 |
| 超时行为 | — | 按阶段推进 |

`propose` 已不存在：rc.4 删除 proposal/Approval；旧 DB 的 `propose` 行在 load 时转为 `ignore` 并记 `legacyImport`。DSH 路径是 admitted 明确请求直接 `DecAccept` → Ticket。

反之，`Attention`（`ask_user_question`）是**工单已经存在、执行中需要你回答一个问题**，它属于 Ticket，不属于 Decision。两者在面板上是两个不同的分组，在流里是两种不同的卡。

#### 敏感信息的判定

外发内容含以下任一项 → 必须你批准：

| 类别 | 例子 |
| --- | --- |
| 客户 / 个人数据 | 明细行、联系方式、用量、账单 |
| 金额与商业条款 | 报价、折扣、合同 |
| 对外承诺 | "我们会改"、"下周上线" |
| 公开渠道发言 | GitHub 公开 PR / 评论、公开群 |

**回给你自己的渠道不受此限**：泄露面是零。所以"自己"无条件执行 + 直接回，与"同事"必须审批，是一致的同一条规则的两面。

#### 四条路

| 路由 | 含义 | 典型场景 |
| --- | --- | --- |
| **`direct`** | 直接在原会话回复发送者 | 事务性、已授权、不涉及承诺（"CI 现在是绿的"） |
| **`operator`** | **只通知你**（面板流 + 系统通知 + 徽标），不回复同事 | 需要你判断、涉及数据外发、涉及对外承诺 |
| **`both`** | 先报你，你点头后才回同事 | 请求合理但边界模糊，半自动 |
| **`none`** | 不回复，只记录 | 群里的闲聊、公告、与你无关的消息 |

**关键是 `operator` 这一档**：它让 Router 可以"看见但不出声"，把判断权交回给你——而不是为了保持安静而假装没看到，也不是为了显得勤快而擅自回复。

#### 判定顺序（前一条命中就停）

1. **发送者是 operator 本人**（本地面板）→ `direct`：你对自己说话，不需要再问。
2. **不是请求**（通知/闲聊/公告）→ `none` 或仅作上下文（§4.9 已定）。
3. **超出发送者的授权范围** → `operator`：只有你能决定。
4. **内容敏感**（数据外发、钱、对外承诺、公开渠道发言）→ `operator`。
5. **拿不准** → `operator`。**默认可疑即上报，不可疑才直接回。**

#### 一条硬规则：Router 不代表 operator 对外做承诺

`direct` 只允许**事实性与事务性**内容：状态、结果、链接、已做的事。

不允许直接回：承诺（"我们会改"）、判断（"这个方案更好"）、价格与时间、对外公告、代表 operator 的人际表态。

一旦需要这些，走 `operator`：把草稿递给你，你改一个字再发——**这是能力与信任的正确边界**。

#### Reply mode：把 accept mode 的思想复用到回复上

002 有 `off / observe / auto` 三档控制"要不要接活"。回复同样需要：

| 档 | 行为 | 何时用 |
| --- | --- | --- |
| **`notify`（默认）** | 同事的所有消息只通知你，绝不直接回 | 团队刚开始用，你还不信任它的措辞 |
| `assist` | 能答的事实性问题直接答，其余通知你 | 跑顺之后的日常档 |
| `auto` | 按规则自动回，只把 `operator` 档的递给你 | 长期稳定后 |

**默认必须是 `notify`。** 理由和 accept mode 默认 `observe` 完全一样：对外说话的成本不可逆，先观察它"本来会怎么回"，再放开。

#### 学习机制：让信任可以累积

同一类请求被你在面板里确认/配置过 N 次之后，可以一键"以后这类直接回"：
- 生成一条 **reply rule**（不是改默认档，而是加一条窄规则）
- 规则可随时撤销，撤销后立即回到 `operator`
- 每条规则记录是谁在什么时候批准了它——**审计链要能回答"这个自动回复是谁授权的"**

#### 通知你的三个通道

`operator` 档不是"发个消息给你"，而是让它在你能看见的地方出现：

| 通道 | 用途 |
| --- | --- |
| **面板流里一行 🔔** | 带原文、草稿、和「直接回」/「改一下再回」/「不理」三个动作 |
| **系统通知** | 只在你不看面板时；同一条只通知一次 |
| **托盘/Dock 徽标** | 数字 = 待你处理的条数，与你关掉窗口无关 |

#### 审计：每条回复都能追到授权

```go
type ReplyPlan struct {
    Route   ReplyRoute   // direct | operator | both | none
    Purpose ReplyPurpose // acknowledgement | answer | result | attention
    Reason  []domain.Reason
    // 谁授权的：policy(档位) / rule(学习出来的规则) / person(本次人工确认)
    AuthorizedBy string
    ApprovedAt   time.Time
}
```

流里每条 `direct` 回复下面都有一行 `应答：直接回了张三 · assist 档 · 命中规则 #3`，点开能看规则原文。**不能出现"它自己决定就发了"而无处可查的情况。**

---

## 5. Agent Runtime（自研，本项目核心）

### 5.1 选型：SDK 负责什么，我们负责什么

> 12 个候选 × 11 维对比、真实编译与运行日志见 [Go Agent SDK 选型](../research/go-sdk-analysis.md)、实验代码在 `spike-sdk/`。

**结论先行：用 `cloudwego/eino` 全家桶（含其 `adk` 子包 + `adk/prebuilt/deep`）做编排内核，`openai/openai-go/v3` 做 provider，官方 `modelcontextprotocol/go-sdk` 做 MCP，CheckPoint/EventStore 自己落 SQLite。**

| 层 | 内容 | 归属 | 具体 |
| --- | --- | --- | --- |
| Provider | OpenAI 兼容（DeepSeek 等）、SSE、tool_call 增量、reasoning、重试 | 现成 | `openai-go/v3`（含 stream accumulator），或 eino-ext 的 openai 适配器 |
| 编排/循环 | 模型→tool_calls→执行→回填→再模型；并行工具；取消；压缩；结果截断落盘 | **现成（eino）** | `adk.ChatModelAgent` + `Runner` + `TurnLoop`；`middlewares/summarization`（压缩）、`reduction`（超长工具结果落盘成 `<persisted-output>`） |
| 编码 Agent 脚手架 | 文件读写/编辑、shell 后台任务、plan/todo、skill、子智能体 | **现成（eino `prebuilt/deep`）** | 返回 `adk.ResumableAgent`，这是省掉最多自研量的一块 |
| **会话/投递语义** | `steer`/`inject`/`followup`、**已读回执**、`turnEnded`、按角色投递 | **必须自研** | 实现 eino 的 `CheckPointStore` / `SessionEventStore` 接口，把状态落 SQLite |
| 工具层 | `bash`/`read`/`write`/`edit`/`glob`/`grep`/`jobs` + 领域工具（`report`/`ack`/`eval_decide`…）+ `ask_user_question`/`present`/`goal`/`schedule`/`subagent`/`workflow` | 混合 | 通用工具用 eino 中间件；**领域工具 + 全功能能力自研** |
| 沙箱层 | 目录约束、blocked commands、env 隔离、5s 前台转后台 | 自研 | 见 §5.5 |

> **一个必须写死的消歧义**：本项目用的是 **`github.com/cloudwego/eino` 的 `adk` 子包**，它属于 eino，随便用。
> 调研里被否决的是 **Google 的 `google.golang.org/adk`（ADK Go v2）**——另一个项目、另一套依赖。
> 两处都叫 "ADK"，混起来会做错决定。一句话记法：**`cloudwego/eino/adk` 可以，`google.golang.org/adk` 不行。**

**为什么不是 Google ADK Go v2**（它的事件模型其实更适合面板）：eino 自带完整的编码 Agent 脚手架（`prebuilt/deep`）与压缩/结果截断中间件，能省掉最多自研；体积 16 MB vs 42 MB（355 个额外包、otel/grpc/gorm/a2a 全家桶），对桌面应用更友好。若后续最看重"官方 semver 稳定 + 事件模型最完整"，可迁 ADK v2。

**为什么不用 langchaingo**：无 checkpoint/续跑、无压缩、回调粒度只到 LLM/Chain/Tool 级——拿不到逐 token + usage 的稳定事件，且抽象是"聊天机器人时代"的，与无人值守长任务错位。

**手写 loop 到底要多少代码**（实测基线 175 行可跑，生产级 2200–3600 行）：175 行覆盖了流式 + 两轮工具循环 + 取消；剩下 60% 在**上下文压缩、持久化续跑、多 provider 兼容**——正好是这些 SDK 已经做好的部分。所以"复用现成 SDK"的判断成立。

**两个必须处理的坑（实测发现）：**
1. **eino 默认 `StreamToolCallChecker` 只看第一个 chunk**——当模型先输出文本再发 `tool_call`（Claude/DeepSeek 常见）时，**工具被静默跳过，半句话当最终答案**。无人值守场景下这是"假装干完"级别的静默错误。必须换成缓冲整轮的工具调用检查器（调研里已写修复版并验证）。
2. **eino 仍是 `v0.10.0-alpha.35`**：锁死版本 + 预留升级预算。另：Go 1.27 上 eino 依赖的 `sonic` 会降级到 `encoding/json` 并打印告警——但 mygo 要求 Go **1.27.3**，无法回退到 1.26，故**接受告警**（功能正常），详见 §9.4。

**为什么不能用"外部 CLI 当黑盒"**：原型语义里"人可以在 agent 干活中途插一句话进去，agent 在下一个 step 看到它"（`steer`）、"消息进入会话历史才算已读"（`inputsRead`）、"回合结束时后台还有 job 就继续占 slot"——这三条决定了 follow-up 归属、未读消息回 Eval、并发计数的正确性。黑盒 CLI 给不了这些，会连带破坏授权与去重。这是 ADR-2 的根本理由，也是"用 SDK 但不把语义外包"的分界线。

### 5.2 会话与轮次模型

```go
// internal/agent/session.go
type Session struct {
    ID       string        // worker-<ticketId> / orchestrator-<hash>
    Events   EventLog      // append-only，落 SQLite
    Inbox    Inbox         // nextTurn / nextStep 两级队列
    Turn     *Turn         // 当前回合（可能为 nil）
    Compact  CompactState
}

type Event struct {
    Seq       uint64
    Kind      EventKind    // user_message | assistant_text | tool_call | tool_result | compact | error
    At        time.Time
    Payload   json.RawMessage
    Source    EventSource  // person | zeokeel | model | tool
}
```

四条语义（对应原型的 DSH Session 行为）：

1. **两级投递**：`steer` 在**当前回合的下一个 step** 生效；`inject` 只写历史不唤醒（用于 notice/context）；`followup` 在下一回合成队首。
2. **已读回执**：消息写入事件日志并成为下一轮模型输入时，才回调 `inputsRead`。**"发出去"≠"读到了"**。
3. **回合结束判定**：模型不再产出 tool_call 且后台作业静默 → `turnEnded`。
4. **崩溃恢复**：事件日志即事实。重启后按 `Seq` 重放，未配对的 `tool_call` 标记为 interrupted 并以 recovery note 续跑。

### 5.3 工具集与角色白名单

**能力差异用白名单表达**，不是两套代码（原型同款策略）：

| 角色 | 工具 | 说明 |
| --- | --- | --- |
| **worker**（每 Ticket） | `bash` `read` `write` `edit` `glob` `grep` `jobs` + `report` `ack` `ask` `present` `handback` `follow` `read_domain` + `subagent` `skill` `todo` | 只读写自己的 Ticket 目录；`report` 是唯一能把状态写回领域的通道 |
| **orchestrator**（持久） | `read_domain` `eval_read` `eval_decide` `route` `reply` `retry` `note` `stop` `follow` `ask` `present` + `skill` `todo` | **没有写文件/执行命令的能力**——评估者不该能改仓库 |
| 两者共享 | `skill`、`todo`、`ask_user_question`、`present`、`subagent`、`goal`、`schedule`、`web`、`mcp` | 通用 agent 能力，按角色限权而非删减 |

**能力差异用白名单表达，不是两套代码。**完整能力清单与优先级见 §5.10。

### 5.4 上下文预算与压缩

无人值守长任务的最大技术风险是上下文管理。策略：

1. **工具结果分级截断**：命令输出保留头尾各 N 行（中间标注省略行数）、文件读取带 `offset/limit` 与"文件共 X 行"提示、搜索结果上限 + 超限提示。
2. **资源分页读取**（照搬原型的 `read_domain`）：Ticket/Thread/Trigger/Decision 不是一次性塞进 prompt，而是给"摘要 + 计数 + next_offset"，模型按需展开。这是原型控制成本的关键设计。
3. **压缩**：超过预算阈值时，把早期事件摘要成一段 `compact` 事件（保留决策、文件路径、未完成事项），原始事件仍留在库里（面板可见，模型不再读）。
4. **预算可观测**：每次模型调用的 token 用量写入事件，面板显示"本 Ticket 已用 X tokens / 轮次 N"（原型没有这个，是我们的加分项）。

### 5.5 命令执行与沙箱

原型的 `prepare` + `worker-bin` shim 方案值得照搬，但要在设计上明确它**不是安全边界**：

- 每个 Ticket 一个工作目录（默认 `<stateDir>/work/<name>-<ticketId8>`）。
- `prepare` 命令（10 分钟超时）在**父目录**用 `sh -c` 执行，成功后校验目录；失败 → `failed` 且**不自动重试**（因为"目录存在即视为已准备"，自动重试会把残留当成品）。
- 执行环境变量由 Executor 与各 Source 注册，**不含凭据**；变量名避开 `KEY|PASSWORD|SECRET|TOKEN`。
- `blockedCommands` 以"同名命令不可用"实现（原型明说不是安全边界）。
- **首版不做真沙箱**，但把 `Sandbox` 做成接口，为后续接 Linux namespace / Landlock / 容器留位。

### 5.6 后台作业与子智能体

原型最"重"的一块，也是最能体现产品差异的一块。建议**分期**：

- **v1（M1–M2）**：`bash` 支持超时 + 前台 5 秒转后台 job，job 结束唤醒回合；不做子智能体。
- **v2（M4）**：30 分钟后台规则、handoff 时保留 shell job 供 review、子智能体（复用同一 runtime，白名单受限）。
- 明确记录：**这一块的简化不会破坏领域语义**（Ticket 状态由 Report 决定，不由 Session 决定），所以可以安全延后。

### 5.7 可观测性：把 Agent 的工作过程变成一等公民

这是原生桌面应用相对 Web 面板的**最大体验优势**，建议作为产品卖点：

- 每个 Ticket 的详情里可以展开 **Live Transcript**：流式文本（`ui/markdown.Stream`）、工具调用卡片（命令 + 退出码 + 耗时）、token 用量、当前 step。
- 事件从 runtime 通过 `atomic.Pointer` 快照 / 环形缓冲推给 UI（§6.2），不阻塞主线程。
- **这让"无人值守"从盲目信任变成可审计**：面板默认仍然安静（符合哲学），但想深看时能看到每一步。

### 5.8 崩溃恢复

| 崩溃点 | 恢复动作 |
| --- | --- |
| 命令已提交、effect 未执行 | 重启后 Effects 表扫描 `due <= now` 重跑；`generation` 防旧任务删新负载 |
| 工具调用中 | 未配对 `tool_call` → 标记 interrupted，带 recovery note 续跑（提示"可能已部分执行，先检查状态再重复"） |
| 出站消息已发未记 | Outbox 先落库再发；回执永久保留 → 重复发送被 ID 幂等挡掉 |
| Ticket 工作中重启 | Assignment 以 `hostRestarted` 结束并新建，`oldest first` |
| 窗口关闭 | 不影响（core 独立于 UI） |

### 5.9 新增入口一律用 Source 吸收（不污染领域模型）

要把 ZeoKeel 做成"全功能本地 AI agent"，最危险的诱惑是给领域加概念（Goal、Schedule、Task、Session…）。原型的 `001` 已经给过答案，在"Rejected concepts"里写着：

> *Eval scheduling its own delayed Triggers — a condition that needs waiting is an ordinary Source, so Eval never depends back on Sources.*

照这条纪律，新增能力全部收敛到已有概念：

| 新能力 | 归属 | 为什么不新增概念 |
| --- | --- | --- |
| **定时 / 周期任务**（`schedule`） | 新增 `schedule` **Source kind**，产出 Trigger | 时间推移是一种"事件源"，与 GitHub 轮询同构；游标 = 上次触发时间 |
| **本地面板对话** | 新增 `local` **Source kind** | 人在面板里说的话就是一条 Trigger；`addressed=direct`、`human=true` |
| **子智能体 / workflow** | Assignment 内部的 runtime 能力 | 它们不改变 Ticket 的事实，产出仍通过 `report` 回到 Ticket |
| **ask_user_question** | 已有 **Attention**（Ticket 上等待人回答） | 原型已定义"结构化提问"，只差一个工具入口 |
| **present（交付物）** | 已有 **Report + Artifacts + Channel** | 本地面板的交付物卡片 = 一种 Channel 呈现 |
| **goal（长目标）** | Ticket 的 `follows` + Assignment 的继续轮次策略 | "跨消息持续的工作"原型已有语义；goal 只是它的显式化 |

**收益**：`Engine` 一行不用改就能支持"定时任务""本地对话"。代价只是多实现两个 ~300 行的 Source。这是原型架构最好的地方——**入口是 Source，阻塞是 Attention，产出是 Report**，三者已经覆盖了扩展面。

### 5.10 全功能 Agent 能力矩阵

以当前 DSH（305 个包）为参照系，逐项给出 ZeoKeel 的形态与优先级。**P0** = M1–M2 必须，**P1** = M3–M4，**P2** = M5–M6，**—** = 明确不做。

| 组 | 能力 | DSH 对应 | ZeoKeel 原生形态 | 优先级 |
| --- | --- | --- | --- | --- |
| **会话与上下文** | 事件日志 + 持久化 + 恢复 | `session`, `session-persistence-jsonl` | SQLite `session_event` 表 | P0 |
| | 上下文压缩 | `compaction`, `-basic`, `-tool-result-pruner`, `-image-offload` | 摘要为 `compact` 事件；工具结果分级裁剪 | P0 |
| | token 计量 / 输出落盘 | `token-meter`, `output-retention`, `spill` | 面板显示每 Ticket 用量；超长输出写文件 | P0 |
| | 会话标题 / 轮次大纲 | `session-title`, `session-turn-outline` | 详情页的 Transcript 导航 | P2 |
| **工具基座** | shell（含持久 shell） | `tool-bash`, `-bash-persistent`, `terminal` | `bash` 工具 + 内嵌终端面板页 | P0 |
| | 文件读写 / 搜索 | `tool-fs`, `tool-fs-search`, `tool-str-replace-editor` | `read`/`write`/`edit`/`glob`/`grep` | P0 |
| | 后台作业 | `jobs`, `tool-jobs` | `jobs` 工具 + 前台 5s 转后台 | P0 |
| | todo | `tool-todo` | `todo` 工具 + 面板折叠显示 | P1 |
| | web 抓取 / 搜索 | `tool-web`, `web-fetch-http`, `web-search-deepseek` | `web_fetch` / `web_search` | P1 |
| **交互与产出** | **结构化提问** | `tool-ask-user`, `user-questions` | → **Attention**：原生对话框 + 托盘徽标 + 通知；选项可点 | **P0** |
| | **交付物呈现** | `tool-present`, `client-ui-deliverables` | → **Artifacts 卡片**：文件/图片/diff 缩略、系统"在访达中显示"、Quick Look | **P0** |
| | plan mode | `plan-mode`, `client-ui-plan` | 工具白名单降为只读 + 面板"计划待批"卡 | P1 |
| **长程自主** | **goal** | `goal`, `goal-round-driver`, `tool-goal`, `command-goal` | Ticket 上的"目标 + 自动继续轮次 + 上限"；完成/受阻/暂停 | **P1** |
| | **schedule** | `schedule`, `tool-schedule`, `client-ui-schedule` | `schedule` Source kind + 面板"定时任务"页；系统通知 | **P1** |
| | **workflow 编排** | `workflow`, `workflow-ptc`, `tool-workflow` | 子智能体的脚本化 fan-out；面板显示阶段与结构化结果 | **P1** |
| | 子智能体 | `subagent`, `-spawn/-fork-in-process`, `tool-subagent(-control)` | 复用同一 runtime，受限白名单；面板显示父子树 | P1 |
| | 多智能体团队 | `experimental-agent-team` | 原型的 orchestrator/worker 已覆盖；不额外做 | P2 |
| **组织与扩展** | skills | `skill`, `skill-filesystem`, `tool-skill`, `skill-office` | 文件系统发现 + `skill` 工具；面板浏览与编辑 | P1 |
| | MCP 客户端 | `mcp-client`, `mcp-resources` | 连接外部 MCP server，工具/资源注入 | P2 |
| | 自定义命令 | `commands`, `cmdline`, `native-command` | 面板命令面板（⌘K）+ 可注册命令 | P2 |
| **安全与治理** | 权限预设 / 审批 | `permission-presets`, `authorization`, `user-approval` | 按角色白名单；危险操作走原生确认框 | P0 |
| | 沙箱 | `sandbox`, `sandbox-policy`, `fs-sandbox`, `bash-sandbox` | 目录约束 + blocked commands（首版不做真沙箱，留接口） | P1 |
| | 防打转 | `repeat-tool-reminder`, `fs-observation-policy`, `tool-call-timeout-policy` | 重复工具检测 + 超时策略 | P1 |
| **检索与回溯** | 会话检索 | `session-query`, `session-query-sqlite` | 全局搜索 ⌘K 已覆盖 Trigger/Ticket；扩展到时序事件 | P2 |
| | checkpoint / rewind | `session-checkpoint-policy` | Ticket 详情"回到第 N 轮重来" | P2 |
| **多模态与工作区** | 附件 / 图片 | `attachment`, `attachment-local`, `file-reference` | 拖拽进窗口；图片直接显示 | P1 |
| | 工作区 / 工作目录 | `workspace`, `tool-working-directory` | 每 Ticket 一个目录；面板"打开目录"；**Router 侧靠 Workspace Brief 路由（§4.6）** | P0 |
| | **IM 接入（飞书/钉钉/Slack/Telegram）** | `dsh-*` 无通用抽象，原型只有飞书 | `internal/im` 公共抽象 + 每 IM 一个 Source kind + 长连接驱动（§4.8） | P1 |
| | **会话类型与意图判定** | 原型只有 GitHub/Feishu 两种范围 | `Conversation` / `Addressed` 事实 + admission 行 + 语义层 `ignore(notification)`（§4.9） | P0 |
| **授权与回复** | **应答策略（回同事 / 通知你）** | `orchestrator_reply` 的 `purpose`（埋得深） | `ReplyPlan{route,purpose,reason,authorizedBy}` + reply mode（notify/assist/auto）+ 学习规则（§4.10） | **P1** |
| **人机交互** | **任务下发入口** | 原型没有（工作只能从外部流进来） | 顶栏常驻输入 + `⌘N` composer + `⌘K` 命令面板；`local` Source；选了工作区走 direct-dispatch（§6.9） | **P0** |
| | **任务列表与产物呈现** | `client-ui-deliverables` | Execution 任务列表（按阶段）+ 交付物三层展示 + Quick Look 预览 + 产物→请求反查（§6.10） | **P0** |
| **模型与成本** | provider / 重试 / 预算 | `llm`, `llm-retry`, `agent-default-model`, `token-meter` | OpenAI 兼容 + 退避 + 每 Ticket 预算硬上限 | P0 |
| **明确不做** | — | 插件市场/管理器、open-in-app、Office/PDF、语音、CoT 翻译、产品遥测、ralph、worktree 实验 | 与桌面单人 agent 无关或与 goal 重叠 | — |

**三点值得单独强调：**

1. **这些能力在原生 UI 上比在 Web 上更自然。** `ask_user_question` → 原生对话框（能抢焦点、能在 Dock 弹徽标）；`present` → Quick Look 预览；`schedule` → 系统通知；`goal` → 托盘进度；子智能体 → 面板里的树。这是选 mygo 原生路线的一个真实收益，不是审美偏好。
2. **`ask_user_question` 必须映射到 Attention，而不是自由文本。** 原型明确拒绝"从 report 的自由文本推断等待人"——因为分类要可检查。同理，提问必须是结构化事实（问题 + 选项 + 归属 Ticket），否则面板无法排序、无法计数、无法"只显示需要人的东西"。
3. **`goal` 要小心。** 它的本质是"自动继续轮次"，很容易变成"agent 自己无限跑"。必须复用原型的 **accept mode = observe** 思路：goal 的第一阶段只记录"如果继续会做什么"，人确认后再放开自动轮次，并设硬性轮次上限与预算上限。

---

## 6. UI 设计（mygo 原生）

> **视觉稿是可运行的真代码，但只作视觉参考**：`design/prototype/` 下 16 个界面（00–15）是 mygo `ui` view 函数的真实渲染（`ui.Render` 无头绘制导出 PNG，中文字体与控件都是真的），同一份 view 可 `-window` 开成原生窗口。Wave 1 已把依赖从 `replace => /tmp/mygo` 改为钉住 `github.com/egoist/mygo v0.7.4` 并实测 `CGO_ENABLED=0 go build/vet` 与无头渲染通过。截图产物不再入库：`cd design/prototype && go run . -out shots` 可重新生成。**不要从该目录回迁 view/tokens 到 `internal/ui`**（两套 tokens 已漂移）；它不在根 module 的测试范围内。

### 6.1 三条铁律

写在 `internal/ui/README.md`，Code Review 检查项：

1. **view 里不做 I/O、不加锁、不 `time.Sleep`、不调阻塞式 mygo API**（`Dialog`/`Clipboard`/`Screen`）。主线程同时是事件循环、布局、绘制、所有 handler——卡 100 ms 用户能看见。
2. **不保存 `*ui.Context` / `ui.Element` 跨帧**。跨帧只能 `ui.Handle`；用 `go tool mygo vet` 兜底（它能查出"把 build 值存进 struct/全局/goroutine"）。
3. **持有自己的 mutex 时不调用会等主线程的 API**（`RunOnMain`、`Clipboard`、`Dialog`）。锁内只碰纯 Go 数据，跨线程一律交给 `Window.Update`（非阻塞）。

### 6.2 状态快照架构（最关键的一张图）

```
core 后台 goroutine                         主线程
─────────────────                          ──────
状态变更 ──► 标脏 ──► projector(合并 16–33ms)
                        │ 构建不可变 VM 分片
                        ▼
              atomic.Pointer[VM].Store() ──► win.Invalidate()
                                              │
                                              ▼
                                   view(c): vm := ptr.Load()
                                   （一次 Load，帧内一致）
```

```go
// internal/view/snapshot.go —— 只读投影，构建后永不修改
type OverviewVM struct {
    NeedsPerson []TicketRow
    Problems    []HealthItem
    Counts      ActivityCounts
    Executors   ExecutorLoad
}
type TicketListVM struct {
    Gen     uint64
    Filter  Filter
    Rows    []TicketRow   // 已过滤/排序的最终结果
    Total   int
}
// internal/ui/app.go
type app struct {
    win    *mygo.Window
    overview atomic.Pointer[view.OverviewVM]   // core 写，主线程读
    tickets  atomic.Pointer[view.TicketListVM]
    dirty    atomic.Bool

    // 以下仅主线程读写
    route    *ui.Router
    section  int
    list     ui.ListState
    query    string
    detailID string
}

func (a *app) view(c *ui.Context) {
    a.dirty.Store(false)
    ov := a.overview.Load()          // 一帧一次，帧内自洽
    if ov == nil { ui.Spinner(c); return }
    // …纯渲染，无 I/O
}
```

**为什么分片而不是一个大 Snapshot**：一次 Health 心跳不应该重建 5 万条 Ticket 列表。分片后"哪个 tab 变脏就只重建哪片"，且未激活的 tab 可以完全不构建。

**重绘节流**：projector 用 16–33 ms 合并窗口；`Invalidate()` 自身已 CAS 合并成"最多一帧"。禁止每个事件调一次 `Update`（闭包会堆积）。

**UI → core 的命令**：`CommandBus` 接口，方法**只入队不阻塞**，返回 `Future`/回调（可选）。写操作（stop、retry、pause、保存设置）由 core 校验后落库，UI 通过下一次快照看到结果——**不做乐观更新**，避免"界面说成功了其实失败了"。

### 6.3 页面结构：一条流，一个输入框，一个巡检入口

**这里改过两次，两次都是因为没读懂"Super Agent 是什么"。**

- **第一次**：照原型的 6 个 tab 做，得到一个监控仪表盘——大块空间给空状态卡片，输入框缩成一张卡，人不知道自己该干什么。
- **第二次**：改成"任务列表 / 执行流 / 检查器"三栏。仍然错：它假装人要在多个 Ticket 之间切换上下文。

**正确的理解，来自 000 与 004：**

> *"Messages are short and **go to the conversation where the request was made**."*

Router **不在这个窗口里跟你对话**。它的回复发回请求的来源（GitHub 评论、钉钉单聊、飞书群）。这个窗口里你看到的是**它看到了什么、做了什么**——一条连续的流，不是聊天。

> 官方第 4 点：上下文当缓存管，Inline & Spill + 链式召回。

Router 是**一个**持续会话，靠压缩管理上下文。所以 UI 不该按 Ticket 切上下文——**没有"详情页"**。

#### 唯一的界面

```
┌──────────────────────────────────────────────────────────────────────────┐
│ ⬢ ZeoKeel            ● 0/2 执行中 · 队列 2     [1 待处理]      [巡检]     │
├──────────────────────────────────────────────────────────────────────────┤
│                                                                          │
│    ┌ 钉钉 · 张三 ────────────────────────────────────────── 12:04 ┐      │
│    │ 帮我把 acme 这个客户上个月的用量数据发我一下                 │      │
│    └─────────────────────────────────────────────────────────────┘      │
│      → 派发   #46   acme-data   已接单                               │
│      🔔 要把 acme 2026-10 用量摘要发给钉钉的张 three 吗？      [回答]     │
│                                                                          │
│    ┌ GitHub · #42 · @lin ─────────────────────────────────── 13:10 ┐    │
│    │ 登录超时应可配置，默认 30 分钟。                              │    │
│    └──────────────────────────────────────────────────────────────┘     │
│      → 派发   #47   web   排队中                                         │
│      ▸ bash  rg -n "15 * time.Minute"                  exit 0 · 0.4s     │
│      ▸ edit  internal/auth/session.go                    +12 −3          │
│      ▸ test  go test ./internal/auth/...            通过 · 1.82s         │
│      → 交付   PR #128  +128 −36  已通过                                  │
│                                                                          │
│    ┌ 你 · 本地面板 ──────────────────────────────────────── 13:28 ┐      │
│    │ 把登录超时改成可配置，默认 30 分钟                            │      │
│    └──────────────────────────────────────────────────────────────┘     │
│      → 派发   #48   web   执行中                                         │
│                                                                          │
├──────────────────────────────────────────────────────────────────────────┤
│  ✦  说点什么，或派一件事…                                         [发送] │
└──────────────────────────────────────────────────────────────────────────┘
│ 一条连续的上下文 · 满了自动压缩      rev 8 · Go 1.27.3 · CGO=0 · SQLite   │
```

**五条设计规则：**

1. **所有来源进同一条流。** GitHub、钉钉、飞书、你在这里打的字——全是同一条时间线上的消息。不分组、不切换、不"进入某个任务"。
2. **每条消息下面缩进写清"它做了什么"。** 派给谁（`#46 acme-data`）、什么结果（工具卡、验证、交付物）、要不要你（🔔 行内直接给按钮）。**一个消息就是一棵小结**。
3. **输入框只有一行，没有工作区选择，没有"新建任务"。** 判断"这活该派给谁"本来就是它的事（001/002 的 Eval），把它做成人的前置表单是本末倒置。你想指定，就在句子里说（"在 web 仓库里…"）。
4. **没有详情页。** 按 Ticket 切上下文会打断这条流，而且 Router 本身也没有按 Ticket 分上下文——它只有一个会话，靠压缩。
5. **巡检在右上角，是一个独立屏幕。** 总览/来源/触发/评估/设置收在那里。它回答"它健康吗"，主界面回答"它在为我干什么"。

#### 动作只有一个地方：待办

同一件事的动作出现在两个地方，就一定会乱。所以主界面的六页各有明确分工：

“动作只有一个地方”说的是**工作项动作**，不是所有按钮。按类别分：

| 类别 | 例子 | 出现在哪 |
| --- | --- | --- |
| **工作项动作** | 回答 / 重试 / 停止（批准/拒绝已随 rc.4 删除） | **收件箱/执行**（它回答"我现在要做什么"）；无工作项审批动作 |
| **配置动作** | 保存设置、同步白名单、检查连接 | **只在设置页**（它回答"它该怎么工作"） |

| 页 | 回答 | 动作 |
| --- | --- | --- |
| **总览** | 我现在要做什么 | 工作项动作：回答 / 重试 / 停止（无批准/拒绝） |
| **消息** | 它看到了什么、做了什么 | **没有，只读时间线** |
| 来源 / 触发 / 评估 / 执行 | 某个概念本身的细节 | **只读**；点行就地展开 |
| 设置 | 它该怎么工作 | 配置动作：分组表单 + 保存条（有改动才出现） |

**配置不散落在各页。** 来源页只回答"这个来源现在是什么状态"，要改它的配置就给一条 `设置 → 设置 › 来源` 的链接，不内联表单——否则同一份配置会有两个入口。

因此**消息页只记录，不求助**。一条同事的请求在流里就是两行：

```
钉钉 · 张三
  帮我把 acme 这个客户上个月的用量数据发我一下
  → 已接单 · acme_data
```

rc.4 已删除 proposal/审批：收件箱只处理任务问题/受阻/失败（attention-only）；历史 proposal 不再显示/不可操作。动作跟着"我现在要做什么"走，记录跟着"发生过什么"走。

#### 与原型 005 的关系

原型的面板是**监督台**（单仓库、服务器常驻、人偶尔来看）。ZeoKeel 的主界面是**工作台**（人在这里派活、看活）。所以：

- 005 的七条原则（只显示需要人的、诚实状态、默认安静）**全盘适用，但降级为巡检页的原则**；
- 主界面的第一原则是 **"别挡路"**：没有卡片、没有仪表、没有空状态插图，只有流和输入。

### 6.4 控件映射（缺口清单）

**开箱即用**：Tabs / Sidebar / List（虚拟化）/ Table / Split / Form / Markdown（含流式）/ editor + DiffView / chart 原语 / Toast / AlertDialog / SearchField / Tree / Outline / Collapsible / Badge / Tooltip / Progress / Spinner / Icon / ContextMenu / Toolbar / Switch / Select / TextInput / NumberInput。

**需要自建（成本可控）**：

| 缺口 | 方案 | 估算 |
| --- | --- | --- |
| Card | `Column().Padding().Radius().Background().Border()` 包一层 | 1 天 |
| Drawer（详情抽屉） | `Overlay` + `Absolute` + `Transition` | 2–3 天 |
| CommandPalette（⌘K） | `Modal` + `SearchField` + `List` | 2 天 |
| DataGrid 过滤/分组/排序 | 纯函数管线，结果放进 VM（**不在 view 里排序**） | 3 天 |
| Chart 高层封装（活动柱状图） | `chart.NewBand` + `Rect` 拼 | 2 天 |

### 6.5 视觉与 tokens

`ui.Theme` 是纯 Go 结构体（无 CSS）。做法：**从 `ui.LightTheme()/DarkTheme()` 派生我们自己的 token 层**，而不是散落硬编码颜色：

```go
// internal/ui/tokens.go
type Tokens struct {
    Bg, Surface, Border, Text, TextMuted, Accent, Warn, Danger ui.Color
    Radius, Gap, Pad                                          float32
    FontBody, FontSmall, FontTitle                            float32
}
func FromTheme(t *ui.Theme) Tokens { /* 映射 + 覆盖 */ }
```

- 跟随系统深浅色（mygo 自带）——✅ 已实现：每帧 `brandFor(c.Theme().Dark)` 派生 token（`internal/ui/app.go:297-304`，`tokens.go:141-200`），深色/系统外观切换不是第二条代码路径。
- 对齐原型的 7 条面板原则：**每元素回答三问之一**；**分类定义可检查**；**一个概念一个地方**；**用领域术语**；**诚实状态**（计数等于列表长度）；**默认安静**（健康不占空间）；**控件一致**（每个设置块只有一个保存条）。

### 6.6 让"原生"真正赢的地方

这三点是 Web 面板做不到或要付出大代价的，应该主动做：

1. **零延迟**：进程内读内存结构体，无 HTTP、无轮询、无 JSON 序列化。打开面板即见数据。
2. **低占用**：release 12–16 MB 二进制、22–37 MB 空闲内存，比一个 Electron/浏览器标签页低一个数量级。
3. **系统集成**：托盘常驻 + 原生通知（`report` 就绪时通知）+"需要人"时 Dock/任务栏徽标 + 全局快捷键唤起。

### 6.7 生命周期（桌面 app 的"常驻"问题）

原型的 7×24 由 tmux + 每小时外部 check 保证；桌面版等价物：

| 原型机制 | 桌面版等价（**目标**） | 2026-11-03 现状（Wave 2 后） |
| --- | --- | --- |
| stateDir 文件锁 | 启动时单实例锁；第二次启动**唤起已有窗口**并退出 | ✅ state dir 写锁在 `store.Open` 时获取（flock/LockFileEx，`internal/store/lock.go`），**早于** durable recovery；第二个写进程立即得到 `ErrAlreadyRunning` 并退出（`cmd/zeokeel/ops.go:30`）。GUI 另有 mygo 单实例锁 + 唤起窗口（`internal/ui/app.go:197,238`），但通常先被 store 锁拦下 |
| tmux 守护 + 每小时 check | 托盘常驻 + 开机自启 + 进程内 watchdog（窗口可关，进程不退） | 🟡 托盘常驻 ✅（`internal/ui/tray.go:10-28`）、关窗隐藏 ✅（`app.go`）、**进程内 supervisor ✅**（`-headless` 每 60s 检查 health/launch 队列/卡住 Assignment，`internal/runtime/supervisor.go:105`）；**开机自启 ❌、休眠唤醒 ❌**（交给 systemd/launchd，文档化） |
| health 端点 | `zeokeel status` 子命令读同一 SQLite health 表，退出码 **0/1/2/3** | ✅ 已实现：`cmd/zeokeel/cli.go:76` + `internal/runtime/status.go:137`；health 表由 effects 队列与 supervisor 写，`status` 只读 inspect（应用运行时也能查） |
| 休眠唤醒 | 监听系统唤醒 → 立刻跑一次 pass | ❌ 无内置实现；`-headless` 每 2s 的 pass 在进程存活时会自然补上，但系统休眠/唤醒事件不监听（委托 systemd/launchd） |
| 强制重启 | 托盘菜单"重启运行时" → 优雅停机 → 重新装配 | 🟡 优雅停机 ✅（SIGINT/SIGTERM → 排空 ≤10s → 0/1/2/3 退出，`cmd/zeokeel/service.go:20-47`）；**没有托盘"重启运行时"菜单**；重启动作交给 systemd/launchd + 巡检脚本 |

> **结论**：原型 7×24 的桌面等价物现在有"单实例锁 + 托盘/关窗隐藏 + `-headless` 服务 + 进程内 supervisor + `status` 退出码 + 外部巡检脚本 + systemd/launchd 样例"。
> 仍缺：开机自启/休眠唤醒（委托系统）、托盘重启菜单、外部 watchdog 独立进程（006 的"完整工具控制 Session"形态没有实现，替代物是脚本 + 进程内 supervisor）。

**关窗 = 隐藏**（`OnClose` 里 `PreventDefault` + `Hide`），注册 `OnWindowAllClosed` 阻止默认退出；macOS 用 `ActivationPolicyAccessory` 让托盘应用不进 Dock。

### 6.8 可测试性

```go
func TestTicketListFiltersByStage(t *testing.T) {
    a := newTestApp(t) // 注入固定 VM，不建窗口
    tt := ui.NewTester(a.view, 1200, 800)
    if err := tt.Click("Execution"); err != nil { t.Fatal(err) }
    tt.Click("Awaiting review")
    if !tt.HasText("#42 修改登录超时") { t.Fatalf("texts=%q", tt.Texts()) }
}
```

`ui.NewTester` 支持点击/键入/滚轮/快捷键/菜单、虚拟时钟 `Advance`、断言 `Texts/HasText/Find/Focused/Clipboard`、截图 `Image()`。**CI 不需要显示器。**（真正的窗口 E2E 用 mygo `automation` 插件 + Xvfb，见 §9。）

---

### 6.9 人怎么派活：任务下发入口

原型假设工作从 GitHub / 飞书**流进来**，所以没有"我来派活"的入口。ZeoKeel 是本地 app，**operator 自己就是最主要的请求来源**。这一端必须补齐。

#### 三个入口，一个输入框

| 入口 | 场景 | 行为 |
| --- | --- | --- |
| **顶栏输入框**（常驻） | 想到了就派一句 | 输入 → `⌘↵` 派发；不切页、不弹窗 |
| **`⌘N` 新建任务** | 要指定工作区或带附件 | 打开 composer：任务描述 + 工作区选择 + 附件 |
| **`⌘K` 命令面板** | 已经在键盘上 | 输入自然语言 → 候选项里出现"派给 ws_xxx" |

**刻意不做**：优先级、截止时间、标签、看板拖拽。000 的"设置要少"同样适用于输入：一句话 + 可选工作区就够。需要更多上下文的，写进任务描述里，由 agent 读。

#### 建模：`local` Source（不是新概念）

人派的活和一个 IM 消息在领域里是**同一种东西**——一条 Trigger（§5.9 已定：新增入口用 Source 吸收）。

```go
// 面板派活 = local Source 的一条 Trigger
Trigger{
    ID:    domain.Key(scope, "local", taskID),
    Scope: scope,                       // 选定工作区 → ws:<id>；让 Router 判断 → global
    Source: "local",
    Actor:  Actor{ID: "operator"},      // 永远是被允许的
    Human:  true,
    Addressed: Addressed{Direct: true},
    Conversation: Conversation{Kind: ConvDirect, ID: "panel", Name: "本地面板"},
    Text:   text,
    Attachments: files,                 // 附件进 Input，agent 能看到
    DefaultWorkspace: picked,           // 选了工作区就填上
}
```

#### 为什么没有工作区选择

上一版在这里放了一个"自动判断 / ws_web / ws_acme_data"的下拉。**那是反的**：判断"这活该派给谁"正是 Super Agent 存在的理由（001/002 的 Eval）。让 operator 每次先选一次，等于把 Router 的核心工作外包给了人，还把"它到底能不能自己想明白"这件事藏了起来。

- 输入框只有一行。**不传工作区**。
- 想指定就写在句子里（"在 web 仓库里把…"），Router 从文字里读出来。
- Router 判断不了时**才**反问（§4.6 两级路由的最后一档）。
- 保留的快捷路径：如果一句话里明确点了工作区名，`routeLocked` 的候选召回会命中它，assessor 直接采纳，不额外多问。

**但状态机不变**：仍然产生 `Decision{accept}` + `Mandate` + `Ticket`，只是没有"人预先指定"这条特殊入口了。审计、幂等、resume 全部复用。

#### 派发后的反馈

- 输入框清空，**200 ms 内**这一行出现在 Execution 列表顶部，阶段 `working/queued`。
- **不弹窗、不跳页、不 toast**（除非失败）。新任务出现在总览"需要我处理"下方——它此刻确实需要你（确认 agent 理解对了）。
- 失败只有一种：没有工作区（且 Router 也判断不了）→ 输入框下方一行红字 + 保留已输入内容。

#### 附件

拖拽或选择文件 → 落到 `<stateDir>/artifacts/<ticketId>/input/`，作为 `Input` 的一部分交给 Worker。
- 图片：Worker 能直接看（多模态）。
- 其它文件：Worker 用 `read` 打开，提示词里给出路径。
- 附件也是产物的一部分（输入→输出的追溯链）。

#### 入口与"继续指挥"的区别

| | 派新活 | 继续指挥 |
| --- | --- | --- |
| 位置 | 顶栏 / ⌘N / ⌘K | Ticket 详情底部 composer |
| 结果 | **新建 Ticket** | 作为 `Input` 投递给既有 Ticket |
| 领域动作 | `Decision{accept}` + `Ticket` | `Input{kind: text}` |

同一个视觉组件，两种语义——靠上下文位置区分，不靠模式切换。

---

### 6.10 任务到产物：怎么显示

#### 一条任务的三个可见位置

| 位置 | 显示什么 | 回答什么 |
| --- | --- | --- |
| **总览 · 需要我处理** | 只显示等你动作的（回答/授权/评审/受阻） | 现在要我做什么 |
| **Execution · 任务列表** | **全部**任务，按阶段过滤（Open / Pending / Working / Waiting / Ended） | 我派过的活现在都到哪了 |
| **Ticket 详情** | transcript + 授权 + 等待输入 + **交付物** | 这一件具体发生了什么 |

执行列表的默认排序是**最近活动**，不是创建时间：正在推进的排前面，卡住的一眼可见。

#### 产物（Artifact）的数据模型

```go
type Artifact struct {
    ID       ArtifactID
    Ticket   domain.ID
    Kind     ArtifactKind // pr | commit | file | diff | image | report | link | service
    Title    string
    Detail   string        // "+128 −36 · 9 files" / "4.2 KB" / "1440×900"
    Path     string        // 本地路径，用于"在访达中显示"
    URL      string        // 远程地址，如 PR
    Source   string        // 谁生成的：worker / automation / operator
    CreatedAt time.Time
    // 验证状态：产物"看起来完成"和"确认可交付"是两件事
    Verified  VerifyState  // unknown | passed | failed
    VerifyNote string      // "go test ./internal/auth/... · 1.82s"
}
```

**`Verified` 是必须的。** 原型已有这个直觉（截图里 report.md 写了"## 验证"、coverage.html 有覆盖率），但我们把它提升为一等字段：一个 PR 产物在 CI 没过之前不能显示为"可交付"。

#### 三个展示层级

| 层级 | 外观 | 用在 |
| --- | --- | --- |
| **行内徽章** | `PR #128` `+128 −36` 一行 chip | 列表行、transcript 里的工具结果 |
| **卡片** | 类型图标 + 标题 + 摘要 + 缩略预览（`05-artifacts` 已设计） | Ticket 详情的"交付物"区 |
| **Quick Look 式预览** | 右侧预览面板 + 三个动作 | 点卡片后 |

预览面板的三个动作（原型已有，保留）：**在目录中显示** · **用默认应用/浏览器打开** · **复制路径**。

#### 产物 → 请求的反查链

这是"诚实可审计"的关键：任何产物都能一路追回是谁要的。

```
产物卡片 → Ticket 详情 → Mandate（谁授权、哪条策略）→ 原始 Trigger（谁的哪句话）
```

`Artifact.Ticket` 是硬链接；`Ticket.Mandate` 记录 `strength / policyVersion / actor`；`Ticket` 引用原始 Trigger。**三跳之内到人**。

#### 产物的存放与归档

- 落盘：`<stateDir>/artifacts/<ticketID>/`，Worker 的 `present` 写到这里，面板只读。
- 跟着 Ticket 归档（§7.3）：Ticket 结束时产物保留 180 天，之后随库归档。
- 面板不复制产物内容进数据库，只存元数据 + 路径——大文件不进 SQLite。

#### 总览的"最近交付物"

原型已有：最近三件产物 + 类型图标 + 摘要。它回答的是"我刚才派的事有结果了吗"，**不是完整列表**——完整列表在 Execution。

---

## 7. 数据与持久化

### 7.1 Schema（与原型同构，从 v1 开始）

| 表 | 内容 | 备注 |
| --- | --- | --- |
| `workspace` | id（创建时生成、持久化）、name、root、remote、sources、policy、prompts、budget、slots、**brief**（人写字段 + 探测事实 JSON + `observed_at`） | **所有其他表带 `workspace_id` 列**；它是属性，不是分库分区。brief 见 §4.6 |
| `trigger` | Trigger JSON + `at` + `default_workspace`（可空） | `at` 统一 RFC3339Nano（字典序可比）；global Source 的 Trigger 无默认工作区（§4.7） |
| `disposition` | 每个 Trigger 的去向（filtered/eval/decision/fanout/context/acked） | 幂等与 follow-up 归属的基础 |
| `trigger_thread` | Trigger↔Thread 倒排 `(thread, id, at, human)` | 时间线分页 |
| `thread` / `ticket` / `decision` | 聚合 JSON | 单写者 |
| `assignment` | 派工与 report | 独立于 ticket 生命周期 |
| `outbox` | 出站回执（pending/sent/dropped） | **永久保留**，ID 幂等 |
| `effect` | durable 副作用队列（kind+key 唯一，`seq` 保序，`generation` 防旧任务） | 崩溃恢复的核心 |
| `session_event` | Agent 会话事件流（按 session, seq） | 新增（原型在宿主里） |
| `kv` | settings 版本、prompt 版本、Source 游标、CI 指纹、master 会话 id | |
| `health` | 健康条目 | 供 `zeokeel status` |

索引策略：活跃集用部分索引（`WHERE ended IS NULL`），每次 pass 只扫活跃行。

> **现状（2026-11-03 Wave 2 后）**：`SchemaVersion = 3`（`internal/store/schema.go:17`），v3 的
> `schedule`/`schedule_fire`/`goal`/`trigger_thread` 表已折入有序迁移（`schema.go:180-260`）；
> 旧的 `schema_v3.go`/`EnsureV3` 旁路已删除（AUDIT N-13 关闭）。
> 实际没有 `assignment` 表：Assignment 放在 `ticket` 的 JSON 列里；`health` 由 effects 队列与
> 进程内 supervisor 写入，`zeokeel status` 读它（§8.5）。

### 7.2 Effects / Outbox（必须原样保留）

- `Effects`：命令事务内 `enqueue`，**提交后**才执行；同 `kind+key` 未执行的覆盖并立即到期（保序）；`Later` = "现在不能跑"，退避但不计失败；连续 3 次失败才把该 kind 标 error（`delivery` 除外）。
- `Outbox`：出站消息**先落库再发**；用业务 ID 作幂等键；永久保留回执 → 重复评论被挡；同 Ticket 同会话保序；`gone`（永久不可达）与临时错误区分处理。

### 7.3 归档（原型已知的坑）——✅ 已实现（2026-11-03）

原型没有任何 TTL/清理，trigger/disposition/decision/ended ticket/outbox 会无限增长。现在的实现：

- `zeokeel archive --before 180d [--dry-run]`（`cmd/zeokeel/ops.go:90`）：
  把结束超过 N 天的 Ticket 及其 Trigger/Disposition 复制到 `<stateDir>/archive.db`
  （ATTACH + INSERT + DELETE 同一事务），结束后 DETACH；`--dry-run` 只列出将移动的工单。
- Outbox 回执**不删**（幂等键保留），归档后重复投递仍被挡住。
- 命令在应用运行时拒绝执行（state dir 写锁被占用）。
- 覆盖：`TestArchiveMovesOldTickets`（`cmd/zeokeel/ops_test.go:149`）验证票移到 archive.db、
  主库不再有、outbox 回执仍在。
- 面板：设置页部署组显示备份目录与 schema；归档目前只有 CLI（面板按钮是后续项）。
- 未做：按月的 archive 分文件、outbox 回执压缩成"最近 90 天 + 计数"、自动 VACUUM 定时器。

## 8. 运行与部署

### 8.1 模式与 CLI（2026-11-03 Wave 2 后）

| 模式 | 启动 | 用途 | 现状 |
| --- | --- | --- | --- |
| 桌面（默认） | `zeokeel` → 窗口 + 托盘 + runtime | 日常使用 | ✅ `cmd/zeokeel/main.go:282`（`ui.Run`） |
| 无头服务 | `zeokeel -headless` → 只跑 core，常驻；SIGINT/SIGTERM 排空 ≤10s 后按 006 退出码退出 | 服务器 7×24、容器、systemd/launchd | ✅ `cmd/zeokeel/service.go:20-47`；`main.go` signal 分支 |
| 演示 | `zeokeel -demo` → 领域 walkthrough 后退出（无窗口、无副作用） | CI 冒烟、读设计 | ✅ `main.go` `runDemo` |
| 批处理 | `zeokeel -pipeline FILE` | JSON 流水线，退出码是 pipeline 私有语义 | ✅ `cmd/zeokeel/pipeline.go` |
| CLI 子命令 | `status` / `check` / `setup` / `login` / `backup` / `archive` | 运维、自愈脚本、CI | ✅ `cmd/zeokeel/cli.go:47`（`cliCommands`）与 `cmd/zeokeel/ops.go` |

`-headless` 的"core 与 UI 解耦"成立：`internal/core` 不 import `mygo`（ADR-10），无显示器可跑。
`status` 的退出码（0 正常 / 1 可重启修复 / 2 组件错误 / 3 等待 setup）唯一来源是
`internal/runtime/status.go:21-26`；`check` 复用同一 inspect（`cli.go:143`）。

### 8.2 首次配置（**目标 vs 现状**，Wave 2 后）

**目标**（原型 `SETUP.md` 交互式问答的桌面化）：
1. 选择仓库 `owner/name` + 检查 `gh auth`。
2. Agent 账号（专用账号，非 operator 本人）。
3. 允许名单（默认只有 operator）。
4. 工作目录根 + `prepare` 命令模板。
5. 模型 provider + API Key（进钥匙串）。
6. 接受模式默认 **observe**（只记录不行动）。

**现状**（`internal/runtime/onboarding.go`、`internal/ui/onboarding.go`、`cmd/zeokeel/cli.go:216`）：
- ✅ 四步向导：模型 / 工作区 / 来源（local|github|feishu|dingtalk 选择）/ 允许名单；必填未完成时"开始使用"不出现；
  保存即注册工作区；**设置已落库**（`SaveOnboarding` 走 `persistSettings`，重启不丢）。
- ✅ 无显示器可用 `zeokeel setup` 等价完成模型/工作区/允许名单（`cli.go:216-290`）。
- ❌ 不检查 `gh auth`、不校验 repo、不区分"专用 agent 账号"。
- ✅ `prepare` 命令已实现（设置›执行 `execution.prepare`；`runtime/prepare.go`）；❌ 向导里没有接受模式选择（engine 默认 observe 生效，但向导里看不到）。
- ❌ API Key **明文进 SQLite kv**，没有钥匙串（ADR-12 现状见 §2）。
- ✅ 面板可直接**新建工作区**（名称/根目录/用途/类型 + mygo 原生目录选择器）并立刻建会话（Round 2，
  `internal/runtime/workspaces.go:20,101`）；向导只是空库首启的一条路径，不是唯一入口。
- 🟡 来源配置：IM 可按渠道开关/选 transport/编辑凭据并检查授权（`design/im-channels.md`）；GitHub Source 仍需
  `-repo/-gh-login`，没有 Source 实例表/「添加来源」表单（RELEASE F-1）。

### 8.3 打包分发（**目标 vs 现状**，Wave 2 后）

**目标**：macOS `.app` + `.dmg`（含签名/公证）、Windows `.exe` + NSIS、Linux 可执行 + `.desktop` + Debian 包 + PKGBUILD、`plugins/updater` 自更新。

**现状**（`scripts/package.sh`，2026-11-03 实测 `make package` 成功）：
- ✅ Linux：可执行 + `.desktop` + `tar.gz`（`scripts/package.sh:29`）。
- ✅ macOS：`.app` bundle（`Info.plist` 版本注入）+ **未签名** zip，并附 `UNSIGNED.txt`（`:35-45`）。
- ✅ Windows：`.exe` + zip + `install.ps1`（复制到 `%LOCALAPPDATA%` + 开始菜单快捷方式；`:47-54`）。
- ✅ 产物实测（2026-11-03）：三个压缩包各 12–13 MB；解包后 linux 33MB / darwin 32MB，均 < 40MB 预算。
- ❌ `.dmg`、NSIS/MSI、Debian 包、PKGBUILD、自动更新：**全部未实现**（grep 无 hdiutil/nsis/deb/updater）。
- ❌ CI 不跑 `make package`：只有 cross build 与体积检查，发布包只能本机 `make package` 产出。

**注意**（仍然有效）：
- `modernc.org/sqlite` 无需随包分发原生库（选它而非 mygo 自带 sqlite 插件的主要原因）。
- **产物不是纯静态二进制**：mygo 用 purego 的 fakecgo 调系统 API，实测仍动态链接 **glibc**，不能塞进 Alpine/scratch；
  Linux 分发用 `debian-slim`/`ubuntu` 基础镜像或常规发行版包。与「零 cgo」不矛盾——见 §9.4。

### 8.4 开发环境（本机实测可用）

```sh
# Go 1.27.3（mygo 的最低要求，钉死）
export PATH=$PATH:/usr/local/go/bin
# 模块代理：默认 proxy.golang.org 在本网络不可用；镜像可用，且无需关闭校验
export GOPROXY=https://goproxy.cn,direct

# 唯一机械闸门（本地与 CI 相同）
sh scripts/verify-fixes.sh

# 无显示器环境跑真窗口（CI / 服务器开发）
xvfb-run -a -s "-screen 0 1400x900x24" ./zeokeel
# 帧耗时诊断（>8ms 的帧会打印 build/layout/paint/present 分解）
MYGO_FRAME_STATS=1 ./zeokeel
# 强制 CPU 渲染（无 GPU 或远程桌面）
MYGO_GPU=0 ./zeokeel
```

依赖：Go 1.27.3+、`git`、`gh`（GitHub Source）；`lark-cli` 只在 IM 驱动未来接线时需要（当前未用）；
Linux 桌面需 GTK3 + X11/Wayland。

### 8.5 可观测性与运维 CLI（2026-11-03 Wave 2 后）

实现（不是计划）：

| 能力 | 实现 | 证据 |
| --- | --- | --- |
| `zeokeel status [--json]` | 只读 inspect（应用运行时也能查），输出 stateDir/db/running/schema/health/errors/setup/load；退出码 0/1/2/3 | `cmd/zeokeel/cli.go:76`；`internal/runtime/status.go:137` |
| `zeokeel check` | 启动前自检：目录、数据库 schema、日志目录、模型、setup；不依赖应用 | `cmd/zeokeel/cli.go:143` |
| `zeokeel setup/login/backup/archive` | 非交互配置 / gh 登录检查 / VACUUM INTO / archive.db | `cli.go:216`；`ops.go:46,83` |
| `-headless` 服务 | 2s pass + supervisor；信号退出并返回 006 码 | `cmd/zeokeel/service.go:20-47` |
| 文件日志 | slog JSON → `<stateDir>/logs/zeokeel.log`，按天轮转 `zeokeel-YYYY-MM-DD.log`，保留 14 天，0600；打开失败回退 stderr | `cmd/zeokeel/logging.go:27-137` |
| 进程内 supervisor | 每 60s：DB 可写、launch 队列连续失败、运行 >6h 的 Assignment；连续 3 次问题把 runtime health 标 error；写 `health` 表 | `internal/runtime/supervisor.go:105-151` |
| 外部巡检 | `scripts/watchdog-check.sh`（006:133-141 语义） | `scripts/watchdog-check.sh` |
| systemd/launchd | 样例已与真实 CLI 对齐 | `packaging/systemd/zeokeel.service`、`packaging/launchd/com.zeokeel.agent.plist` |
| 面板部署组 | 日志路径、备份目录、schema、status 退出码、OS/arch、时区（只读事实） | `internal/runtime/pages.go:749`（`deployFacts`） |

退出码唯一语义（006:113-124）：0 正常；1 没在跑/不响应/runtime 报错（可重启修复）；
2 组件错误（Source/sync/launch 队列/schema 新于 build，不重启）；3 等待 setup（不重启）。

**手动停**：`zeokeel stop` 写 `<stateDir>/STOPPED`（`internal/runtime/status.go:111-128`、`cmd/zeokeel/cli.go:354-383`）；
`status` 在“手动停且未运行”时返回 **0**（不触发重启），手动启动清除标记（`cmd/zeokeel/main.go:82`）。
**仍缺（RELEASE F 组）**：无开机自启/休眠唤醒/自动更新（委托 systemd/launchd）；无 `--respect-stop` 独立 flag（由 STOPPED 标记承担）。
面板部署组只读事实见 `internal/runtime/pages.go:749`（`deployFacts`）。

详细操作手册：`design/ops-runbook.md`。

## 9. 工程实践

### 9.1 测试三层

| 层 | 工具 | 覆盖 | 速度 |
| --- | --- | --- | --- |
| 领域 | 纯 Go + `MemoryRepositories` + **golden 状态迁移表** | 阶段表、gate、revalidate、ID 幂等、结算规则 | ms |
| Agent Runtime | fake provider（SSE fixture）+ fake 工具 | 两轮工具循环、中断、恢复、压缩、并行工具 | ms–s |
| UI | `ui.NewTester` | tab 切换、过滤、详情、确认对话框、toast | ms |
| 端到端 | `httptest` 假 GitHub + `go-shim` 假 gh + 真实 SQLite | Trigger → PR → 评论全链路；kill -9 后恢复 | s |

**移植期的高价值技巧**：原型有 14,960 行测试。挑出**领域语义**相关的 spec（`core-engine`、`policy`、`stage`、`admission-context`、`queue`、`runtime-concurrency`、`followups`、`timeline`）逐条翻译成 Go 表驱动测试，作为"移植是否等价"的判据。UI 相关的 spec 作废（换栈了）。

### 9.2 CI（按实测能力分层，主流程不依赖显示器）

**每个 PR 都跑（无需显示器）：**（零 cgo 守卫见 §9.4，是最先执行的一步）
```sh
export GOPROXY=https://goproxy.cn,direct
CGO_ENABLED=0 go build ./...                    # 主构建
GOOS=darwin GOOS=windows CGO_ENABLED=0 go build ./...   # 交叉编译
go vet ./... && go tool mygo vet ./...          # 后者查 build 值逃逸
gofmt -l . | tee /dev/stderr | test ! -s /dev/stdin
go test ./...                                   # 含 ui.NewTester（0.02s 级）
# optional: mygo build -platform web            # 4.2s，产 wasm 静态站
```

**每日 / 合并前（Xvfb 容器 job）：**
```sh
MYGO_E2E=1 xvfb-run -a dbus-run-session -- go test ./internal/e2e
```
容器需 `xvfb xauth dbus-x11 libgl1-mesa-dri libgtk-3-0`。用于真窗口 E2E、面板截图回归、`automation` socket 驱动。

**发布 job**：macOS / Windows 打包必须在对应 runner（`mygo build` 产 `.app`/`.dmg`/`.exe`）；Linux 用 glibc 基础镜像。附二进制体积回归（`-ldflags "-s -w" -tags mygo_noinspector`）。

### 9.3 代码规范

- 错误：`domain.UserError{Code}` + `errors.Is/As`；面向用户的 error/reason 一律走码表渲染。
- 并发：**单写者**（Engine 一个 goroutine 消费命令 channel）+ `Effects` 工作池；读路径用不可变快照。
- 时间：所有时间入库存 UTC，展示时转本地。
- 依赖白名单：`internal/domain` **零第三方**；`engine/policy/store` 只允许 SQLite 驱动；`agent` 允许 `eino`/`openai-go`/`go-sdk`；`ui` 允许 `mygo`。**强制检查**：一条 `go list` 断言的 CI 步骤，防止领域层被拖进 agent 或 UI 依赖。

---

### 9.4 零 cgo 政策与守卫（硬约束）

**规则**：唯一构建方式是 `CGO_ENABLED=0`。任何依赖（直接或传递）使用 cgo，一律否决。这不是偏好，是用户定的项目红线。

**为什么值得坚持**：
- 任意机器（含无 C 工具链、无交叉编译器的机器）都能产出三平台产物。
- 供应链面显著变小（不需要 gcc/clang、不需要系统库头文件）。
- 与 mygo 的硬规则一致（mygo 本身就是 no-cgo + purego）。

**已经因此做出的选型**：

| 需求 | 因零 cgo 的选择 | 被否决的方案 |
| --- | --- | --- |
| SQLite | `modernc.org/sqlite`（纯 Go 转译） | `mattn/go-sqlite3`（cgo）、mygo `plugins/sqlite`（purego dlopen 预编译库，避开） |
| 原生 UI | `mygo`（purego，无 cgo） | Fyne（cgo/GLFW）、GTK/Qt 绑定 |
| Agent 编排 | `eino`（实测 0 cgo）、`openai-go/v3`（0 cgo） | 任何需要 cgo 的高性能推理绑定 |
| MCP | 官方 `modelcontextprotocol/go-sdk`（0 cgo） | — |

**必须澄清的一个区别（很容易被误解）**：

> **「零 cgo」≠「纯静态二进制」**。mygo 通过 purego 的 fakecgo 调用系统 API，**仍然是动态链接 libc 的**（实测依赖 glibc）。
> 所以产物**不能进 Alpine / scratch**。Linux 分发用 `debian-slim` / `ubuntu` 基础镜像或常规发行版包。
> 如果哪天真需要静态单文件，那是一个独立课题（会与 mygo 的架构冲突），不要指望零 cgo 顺带解决。

**CI 守卫（四步，任一失败即阻断合并）**——已落盘为 [`scripts/check-nocgo.sh`](../scripts/check-nocgo.sh)：

```sh
#!/bin/sh
# 零 cgo 守卫：依赖图里任何东西需要 cgo 就失败
set -eu
export CGO_ENABLED=0
# 1. 环境里 cgo 必须是关的（防止被 export 覆盖）
[ "$(go env CGO_ENABLED)" = "0" ] || { echo "CGO_ENABLED is not 0" >&2; exit 1; }

# 2. 构建必须过（没有纯 Go 回退的 cgo 依赖会在这里失败）
go build ./...

# 3. CGO_ENABLED=0 下构建图里不得有 cgo 文件
go list -deps -f '{{if .CgoFiles}}{{.ImportPath}}: {{.CgoFiles}}{{end}}' ./... | grep -v '^$' && { echo "cgo in build graph" >&2; exit 1; } || true

# 4. ★ 关键时刻：用 CGO_ENABLED=1 再审计一遍第三方 cgo 依赖
#    第 3 步抓不到「带 !cgo 桩的库」（见下方负例），这一步才能抓住。
#    stdlib 的可选 cgo 路径（net/os/user/runtime/cgo）没有 module 前缀，被 grep '|' 过滤掉；
#    purego 的 cgo 回退是已知且允许的。
CGO_ENABLED=1 go list -deps -f '{{if .CgoFiles}}{{.ImportPath}}{{with .Module}}|{{.Path}}{{end}}{{end}}' ./... \
  | grep '|' | grep -v '|github.com/ebitengine/purego$' \
  && { echo "third-party cgo dependency" >&2; exit 1; } || true

go vet ./...
echo "check-nocgo: PASS (zero cgo)"
```

> **★ 这一条是本次验证最重要的发现，别删。** 负例实测：只额外引入 `mattn/go-sqlite3` 时，
> `CGO_ENABLED=0 go build` **仍然会通过**（该库带了 `!cgo` 的桩文件），第 3 步也仍然为空——
> 因为 `CGO_ENABLED=0` 时那些 cgo 文件根本不进入编译图。**只有第 4 步（CGO=1 再审计）能抓住它。**
> 一个只做"CGO=0 能编译"的守卫是**假守卫**。

**实测证据（已在本机跑通，不是推断）**：

| 检查项 | 命令 | 结果 |
| --- | --- | --- |
| cgo 包集合（`CGO_ENABLED=0`） | `go list -deps -f '{{if .CgoFiles}}{{.ImportPath}}{{end}}' ./...` | **空**（0 个包） |
| 同一命令在 `CGO_ENABLED=1` 下 | 同上 | `runtime/cgo`、`net`、`os/user`、`purego/internal/cgo` —— **证明这条断言真的有区分度，不是摆设** |
| 逐依赖源码扫描 | 在 module cache 里 grep `import "C"` | mygo / eino / eino-ext(openai,mcp) / openai-go/v3 / mcp go-sdk / modernc sqlite **全部为空**；只有 `purego` 带 `internal/cgo`（那是它给 CGO 构建用的另一条路径，CGO=0 时不参与编译） |
| 交叉编译 | 6 个目标 | linux/darwin/windows × amd64/arm64 **全部成功** |
| 运行时联调 | 单个探针二进制 | **6/6 通过**：sqlite 往返、openai-go 流式、eino adk 两轮工具调用、MCP client/server CallTool、mygo 无头 tester 点击、mygo 原生窗口（Xvfb） |
| 模块规模 | `go list -m all` / 内嵌模块 | 151 个模块 / 55 个进二进制 |
| 动态链接 | `ldd` | `libc.so.6`、`libdl.so.2`、`libpthread.so.0` → **确认非静态**，与上面"零 cgo ≠ 纯静态"一致 |
| **负例验证（守卫有效性）** | 临时引入 `mattn/go-sqlite3` | `CGO_ENABLED=0 go build` **仍通过**（有 !cgo 桩）；只有守卫的第 4 步（CGO=1 审计）能抓住 → 证明守卫必须有这一步 |

**产物体积**：探针（全依赖、未裁剪、无 `-s -w`）**48 MB**。真实应用的 release 体积预计 **25–35 MB**，比早期估计的 12–16 MB 大——主要来自 eino + modernc sqlite + openai-go。这是可接受的桌面体积，但已登记为风险（§11）；若超标，用 build tag 把可选能力（MCP、Feishu、workflow）拆成可选包。

**一个必须记录的环境坑**：Go **1.27** 上 eino 依赖的 `sonic` 会打印 *"sonic/ast only supports go1.17~1.26 … fallback to encoding/json"* 并降级到标准库 JSON。功能正常，但性能下降且有告警噪音。**建议开发/发布统一钉 Go 1.26 LTS**——但注意 mygo `go.mod` 要求 **1.27.3**，两者冲突。结论：**先钉 1.27.3 并接受告警**，等 mygo 支持更低版本或 sonic 支持 1.27 再调整；把这条写进 README 的"已知告警"。

**验收**：M0 结束时这条 CI 必须已经是绿的，之后一次都不许红。

---

## 10. 里程碑与工作量

> 估算基于：原型逻辑 10.6k 行 TS、测试 15k 行 TS；Go 通常相当或略少；Agent Runtime 为净新增。

| 里程碑 | 目标 | 交付 | 验收标准 | 工期 |
| --- | --- | --- | --- | --- |
| **M0 地基** | 能编译、能开窗、能存 | `cmd/zeokeel` 骨架；`internal/domain` 全类型 + 阶段表 + ID（**ID 首段含 workspace**）；`workspace` 表 + 全表 `workspace_id`；SQLite store + schema；`internal/ui` 空窗口 + 托盘 + 单实例；`ui.NewTester` 冒烟；零 cgo 守卫进 CI | `go test ./...` 绿；release 二进制 < 40 MB；关窗后进程仍在、托盘可唤回；两个工作区的 `#12` 不串 | 2 周 |
| **M1 Agent Runtime** | 能自己干活 | provider（流式 + tool_call）、loop、session 事件流、`bash/read/write/edit/glob/grep`、`jobs`、中断、恢复、压缩、token 统计、**`ask_user_question` + `present`** | fake provider 下两轮工具循环通过；kill -9 后会话续跑；一个真实模型能独立完成"改一个文件并提交"；提问能在原生对话框弹出并收回答案 | 4 周 |
| **M2 授权与端到端**（原 M2+M3 合并） | 一次做对，不做过渡版 | GitHub Source（评论 @agent）→ gate → **完整 Eval（持久 orchestrator 会话 + `eval_read`/`eval_decide`/`route` 全工具）** → Ticket → worker → PR → 回评论；admission table、rules、accept mode（off/observe/auto）、`would_accept`、Mandate/revalidate、reason 码表、policyVersion/effects；**工作区切换器 + 管理页（添加/扫描/绑 Source/默认 Policy）**；面板：Overview + Ticket 列表 + 详情（含 Live Transcript） | 两个仓库并行工作互不串源；@agent 一句请求 → 20 分钟内出现 PR + 评论；面板实时可见过程；原型 `policy`/`admission`/`stage` spec 的 Go 移植版全绿；observe 模式能正确记录「本会接受」 | 6 周 |
| **M4 可靠性** | 不丢、不重、不卡 | Effects/Outbox 完整、投递三段式、`followupOwners`、`letGo`、`follows`/PR 生命周期、并发 slot（人的消息 +1）、pause/retry/resume/stop、30 分钟后台与 handoff；**子智能体 + `goal` + `schedule` Source + `local` Source** | kill -9 随机点重启 100 次无重复评论、无丢失 Trigger；并发不超限；pause 语义正确；定时任务按 cron 触发且不重不漏；goal 有硬轮次/预算上限 | 4 周 |
| **M5 面板完整** | 好用 | 6 区全部、设置（含保存历史）、提示词编辑器 + 版本 + diff、全局搜索 ⌘K（**含跨工作区**）、详情抽屉、i18n 骨架、通知与徽标、`⌘N` 多窗口（每窗口钉一个工作区） | `docs/005` 的 7 条原则逐条过；所有交互有 `Tester` 测试 | 5 周 |
| **M6 扩展与发布** | 可交付 | Feishu Source、**`workflow` 编排、skills、MCP 客户端、web 工具、终端面板页**、归档、安装包 + 自更新、`--headless` + status、运维文档 | 三平台安装包可装可用；连续运行 7 天无泄漏、库增长可控；接一个外部 MCP server 能调通 | 4 周 |
| | | | **合计** | **≈26.5 周** |

**2 人并行**（一人 core/agent，一人 UI/sources）约 **4.5 个月**；1 人约 **7 个月**。

**已决定不做 M2 过渡版**（不做「简化 Eval 单次结构化判定」），直接实现完整 orchestrator 会话。这样少一次返工，但**取消了原来的早期验证闸门**——项目最大的不确定性（Runtime 质量）会推迟到 M2 末才暴露。

**补偿措施**：在 **M1 末**加一个轻量 smoke harness（约 2 天）：用真实模型在 3 个真实任务上跑通「读仓库→改文件→提交」，只验证 Runtime 本身，不涉及领域层。作为 `make smoke` 目标并纳入 CI 手动 job。这是放弃 M2 闸门后最低成本的兜底。

**代码量估算**：domain+engine+policy ≈ 6k、store/effects/outbox ≈ 2k、sources（含 schedule/local）≈ 3.5k、agent runtime（含子智能体/workflow/goal）≈ 7.5k、executor/orchestrator ≈ 2.5k、ui+view ≈ 5k、测试 ≈ 9–11k，合计 **2.4–2.9 万行 Go**。

---

## 11. 风险登记册

| # | 风险 | 概率 | 影响 | 缓解 |
| --- | --- | --- | --- | --- |
| 1 | **自研 Agent Runtime 质量不达标**（工具调用成功率、长任务漂移、上下文爆掉） | 高 | 高 | 用成熟 SDK 做 provider+loop 底座；M2 设决策点；建 20–30 个真实任务的评测集，每次改动跑回归 |
| 2 | **mygo 0.x 破坏性变更**（已有 `*Element → Element` 前科） | 高 | 中 | 所有 mygo 用法收在 `internal/ui`；锁版本；升级前跑 `go tool mygo migrate-ui`；CI 钉 Go 1.27.3 |
| 3 | **主线程阻塞导致 UI 卡死/死锁** | 中 | 高 | §6.1 三条铁律 + review checklist；`MYGO_FRAME_STATS=1` 监控 >8ms 帧；把阻塞调用统一封进 `internal/ui/async.go` |
| 4 | **领域语义移植偏差**（重复劳动、误结束 Ticket、重复评论） | 中 | 高 | §4.4 的 12 条不变量各写测试 + 原型 spec 逐条翻译；端到端 kill -9 测试 |
| 5 | **7×24 常驻在桌面 app 上不可靠**（用户关机、休眠、崩溃） | 中 | 中 | 托盘 + 开机自启 + watchdog + 休眠唤醒补 pass；文档明确"要真正无人值守请用 `--headless` + systemd" |
| 6 | **Linux 原生 UI 需要 GTK3 + 显示服务器** | 高 | 低 | 开发/使用都在桌面；CI 用 `ui.Tester`（无需显示器）；需要服务器场景走 headless |
| 7 | **`gh` CLI 依赖**（用户未装/未登录/输出格式变） | 中 | 中 | 封装在 `Channel` 接口后；启动时探测并给出可操作提示；M6 评估换 REST 客户端 |
| 8 | **数据无限增长**（原型已知坑） | 高 | 低 | §7.3 归档从 M0 就纳入 schema 设计 |
| 9 | **模型成本失控**（observe 模式也烧钱） | 中 | 中 | 默认 observe；每 Ticket token 预算硬上限；面板显示成本；压缩策略 |
| 10 | **范围蔓延**（Feishu、子智能体、多仓库都很诱人） | 高 | 中 | 严格按里程碑；M6 之前不碰 Feishu |
| 11 | **二进制体积超标**（探针全依赖 48 MB，应用估 25–35 MB） | 中 | 低 | M0 起做体积回归；可选能力用 build tag 拆分 |
| 12 | **Go 1.27 与 sonic 不兼容告警**（mygo 要求 1.27.3，sonic 只支持 ≤1.26） | 高 | 低 | 接受告警并记录；跟踪 sonic/mygo 修复；功能已自动降级 |
| 13 | **多工作区导致 ID 碰撞或历史撕裂**（漏加 workspace 前缀 / 路径派生 ID） | 中 | 高 | M0 用两个工作区同编号 `#12` 的跨区隔离测试锁死；`WorkspaceID` 用持久化本地 ID（§4.5） |
| 14 | **多工作区把 v1 范围撑大**（切换器/管理页/多窗口/跨区搜索） | 高 | 中 | 已计入工期（23.5→26.5 周）；M2 前只做切换器与管理页，`⌘N` 与跨区搜索留到 M5 |
| 15 | **跨工作区派发越权**（Router 在一个仓库被授权，却去动另一个仓库） | 中 | 高 | 每张 Ticket 的 Mandate 必须由**目标工作区**的 Policy 单独授权；跨区测试用例进 M2 验收 |
| 16 | **Router 与 Worker 混淆**（让 Router 执行任务 / 让 Worker 直接对外说话） | 中 | 中 | Router 工具白名单不含写文件与执行命令；Worker 工具白名单不含对外回复工具（§5.3） |
| 22 | **Router 擅自对外回复**（代表 operator 向同事承诺/表态） | 中 | 高 | `direct` 只允许事实性内容；默认 `notify` 档；reply rule 必须可撤销且记录批准人；流里每条回复带授权来源（§4.10） |
| 21 | **任务下发的快捷路径绕开领域**（operator 亲手派的活走特殊通道，导致审计/幂等/resume 分叉） | 中 | 高 | 仍产生 Decision+Mandate+Ticket，只跳过语义评估；不加第二条派活通道（§6.9） |
| 20 | **把通知误判成请求**（尤其群公告、@所有人） | 高 | 高 | `@所有人` 不算 addressed；没 @ 默认不进 Eval；语义层 `ignore(notification/clarify)`；默认 observe + 面板"重新评估"（§4.9） |
| 19 | **IM 协议碎片化**（每个 IM 的卡片/富文本/限流都不一样，易写出 N 份相似代码） | 高 | 中 | `internal/im` 抽象 + `Capabilities` 描述；先做 Telegram 验证抽象；每个 driver 独立可测 |
| 18 | **代理授权放大**（agent 用 operator 凭据，把数据发给本无权查看的人） | 中 | 高 | 数据型工作区敏感操作通过 `ask_user_question`/Attention 让人确认；无 Decision 审批；数据型 Worker 无外发工具，只能 `present`（§4.7） |
| 17 | **Router 对工作区认知失真**（brief 过期/写错 → 派错仓库） | 中 | 高 | brief 带 `observed_at` 与 `stale` 标记；冲突时以 `workspace_read` 为准；拿不准必须 `ask_user_question`；brief 内容是不可信数据，不得改规则（§4.6） |

---

## 12. 待拍板问题

1. ~~首版 Eval 是否用完整 orchestrator 会话~~ → **已定：直接做完整 orchestrator 会话，不做过渡版。**
2. **worker 工具集边界**：是否需要 `web_fetch`（读 issue 里的链接）？原型有，但会显著扩大攻击面和成本。
3. ~~是否做子智能体~~ → **已定：做（M4）**，eino `prebuilt/deep` 已自带基础，成本可控。
4. **Feishu 是否在首版范围内**：同意砍到 M6，先把 GitHub 做扎实。
5. **`prepare` 命令默认值**：是否内置 `git worktree` 方案（原型要求用户自己配），还是给一个"一键 clone + worktree"的默认命令？
6. **i18n**：首版只做中文，还是中英双语（原型有完整的 reason 码表便于双语，但文案量大）？
7. **模型默认值**：默认接 DeepSeek 官方 API，还是让用户必须自己填 OpenAI 兼容 endpoint？
8. **是否需要"多人 operator"**：单机桌面隐含单人。若未来要多人，SQLite 单写者模型要改（Postgres + 租约）。**建议明确排除**。

---

## 附录 A：参考原型不可简化的设计资产

按"简化后会以什么形式还回来"排序：

| 资产 | 简化后的症状 |
| --- | --- |
| 内容派生 ID + 幂等 | 重复评论、重复建 Ticket、重放风暴 |
| `followupOwners` 归属规则 | 同一个请求被多个 agent 同时接走，互相打架 |
| 投递三段式（delivered/read/turnEnded） | "未读消息回 Eval"判断错误 → 请求丢失或重复处理 |
| `revalidate` | 策略弱化后，旧授权仍能恢复 Ticket 干越权的事 |
| durable Effects | 崩溃后丢任务；或 half-done 状态无法收敛 |
| Outbox 回执幂等 | 网络抖动导致同一条评论发两次 |
| `followsDone` / `unfinished` | PR 一关就误判 Ticket 完成 |
| Ticket 阶段查表 | 状态字段与事实不同步，面板显示错误 |
| admission table + accept mode | 无法安全地"先观察再放开"，一上来就自动接受 |

## 附录 B：调研产物索引

| 文件 | 内容 |
| --- | --- |
| [`research/reference-analysis.md`](../research/reference-analysis.md) | 参考原型的领域/授权/执行/提示词/架构/工具契约/桌面化等价方案/8 个决策问题 |
| [`research/mygo-analysis.md`](../research/mygo-analysis.md) | mygo 生命周期、主线程模型、后台→UI 四套骨架、控件映射、主题、测试、SQLite、不可行点 |
| [`research/go-sdk-analysis.md`](../research/go-sdk-analysis.md) | Go agent/LLM SDK 候选对比与实测 |
| [`research/env-feasibility.md`](../research/env-feasibility.md) | 本机编译/无头/测试/web 目标实测结论 |
| [`design/ops-runbook.md`](ops-runbook.md) | 运维 runbook：status 退出码 0/1/2/3、日志、supervisor、备份/归档、systemd/launchd、排障 |
| [`design/how-to-add-a-source.md`](how-to-add-a-source.md) | 新增 Source 的 Go cookbook（Kind/registry/配置/游标/Policy/admission/Channel/测试 + 仍缺什么） |
| [`design/rejected-concepts.md`](rejected-concepts.md) | 被拒绝的概念（原型 + 本项目），新增概念前先读 |
| [`design/prototype/`](prototype/) | **可运行的 UI 视觉稿（仅参考，勿回迁）**：16 个界面 = mygo view 函数；依赖已钉 `mygo v0.7.4`；`go run . -out shots` 重渲染（无头），`-window -screen N` 开真窗口 |
| [`spike-skeleton/`](../spike-skeleton/) | **§4.5–§4.9 的可编译 Go 骨架**：2122 行、20 个测试、零第三方依赖；`go run .` 跑通「钉钉要数据」全流程 |
| `spike-nocgo/` | 零 cgo 联合验证探针（5 类依赖同进程运行，6/6 通过） |
| `_ref/` | 参考原型 clone（只读，不参与构建） |
| `spike/`、`spike-sdk/` | 可行性实验代码与产物 |


---

## 13. 实现状态对照（2026-11-04 rc.4，按代码重写）

> 规则：**✅ 必须能在代码里指到证据行**；指不到就降级 🟡/❌ 并写原因。有冲突以代码为准，
> 同一提交内改这里。Wave 1 的高报（§4.3/§4.4/§4.8/§5.2/§5.8/§5.10/§7）已在 Wave 2 大部分关闭，
> 逐级见下。历史审计：`audit/AUDIT.md` §6。

| 设计 | 实现 | 状态 | 代码证据（**只指生产代码**） / 未完成原因 |
| --- | --- | --- | --- |
| §3.1 进程模型（单机单进程） | `cmd/zeokeel` + `internal/ui` + `internal/runtime` | ✅ | `cmd/zeokeel/main.go:313`（`ui.Run` 起窗口，runtime 同进程）；真机托盘未验证（无显示器） |
| §3.2 分层与依赖规则 | domain→store/engine→runtime→view→ui 单向；cmd 装配 | ✅ | `internal/ui/app.go`（只 import mygo/mui/view）；`cmd/zeokeel/main.go`（唯一装配处） |
| §4.1 类型映射 | 见 §4.1（目标 vs 现状） | 🟡 | 剩余：无 `domain.Source` / `domain.Thread` / `UserError`；`domain.Actor`/identity 辅助已加入 `internal/domain/actors.go` |
| §4.2 阶段查表而非状态字段 | `domain.StageOf` | ✅ | `internal/domain/ticket.go:520`（`StageOf`）；面板桶映射 `internal/view/pages.go` |
| §4.3 gate + admission + accept mode | 确定性 gate、admission 三格、bot list、Rules、AcceptMode（无 RequireApproval） | ✅ | `internal/engine/gate.go:200`（`gate`）、`:27-56`（`Policy`）；Eval 消费 `internal/engine/evaluate.go:294`（`applyEvalLocked`）、`internal/engine/receive.go:360`（`enqueueEvalLocked`） |
| §4.4 不变量（12 条） | 内容 ID/workspace 隔离/幂等/提议≠接受/回写由生产代码保证 | 🟡 | 生产证据：`internal/domain/ids.go:39`、`internal/engine/receive.go:17`（同 ID 去重）、`internal/engine/evaluate.go:294`（判定→Decision 校验）、`internal/runtime/reply.go:213`（回写）；测试未作为本表证据 |
| §4.6 工作区简报 | `internal/brief` + `internal/scan` | ✅ | `internal/brief/brief.go:66`（`RenderAll`）；`internal/scan/scan.go:31`（`Scan`） |
| §4.8 IM 抽象 | 接口完整；两渠道可独立启停；**DSH 对齐：明确请求直接接单，推断按 accept mode；旧式审批已删除**；飞书 WS/longpoll；钉钉 DWS/Stream | ✅（webhook/媒体下载除外） | 接口 `internal/sources/source.go`；生命周期 `internal/runtime/im.go:33-130`（配置/三态）、`:345-470`（Start/Apply/Stop/重启）；DSH `internal/engine/evaluate.go:348-390`；`design/im-channels.md` |
| §4.9 会话类型与意图 | `im.AddressedFor` 三规则 | ✅ | `internal/im/addressing.go:14` |
| §4.10 应答策略 | `replyPlanLocked` + 按频道 `composeReply` + `deliverReply` 的 Held/direct | ✅ | `internal/engine/evaluate.go:524`（`replyPlanLocked`）；`internal/runtime/reply.go:50`（`composeReply`）、`:213-264`（`deliverReply` 读 ReplyPlan） |
| 003 PR/Issue 生命周期（follows） | GitHub 关闭事件 → `CloseThread` → `ThreadClosed`；Worker `follow` → `BindFollow`；ready 等 PR 关闭 | ✅ | `internal/runtime/github_lifecycle.go:39`（`CloseThread`）、`:88`（`BindThread`）；`cmd/zeokeel/github.go:76`；`internal/runtime/artifacts.go`（`follow` 工具）；`internal/engine/lifecycle.go:48-53,368` |
| 003 工单准备（`execution.prepare` + 执行变量） | `sh -c`（execution root）、10m、env 剥离后 `ZEOKEEL_*`、失败不重试、提示词展示；bash 同变量 | ✅ | `internal/runtime/prepare.go:28,31,68,104,123`；调用 `internal/runtime/worker.go`；bash env `internal/agent/agent.go` 的 `Config.Env`、`internal/agent/tools.go:236`；设置 `internal/runtime/pages.go:513-525` |
| 003 30 分钟后台作业规则 | 到上限 kill + `ReportWaiting`（job id/最后输出）、不唤醒；子智能体不适用 | ✅ | `internal/runtime/background.go:22,89,127`；设置 `internal/runtime/pages.go`（`background_timeout`）；提示 `internal/runtime/worker.go` |
| **DSH 对齐（rc.3/rc.4 行为变更）** | admitted 明确 IM/GitHub 请求由 Eval 直接接单 → Ticket；推断按 accept mode off/observe/auto（auto 需规则）；**旧式审批/提案彻底删除**：Decision 只有 accept/stop/ignore/unavailable，无 Approve/Reject/UndoReject/proposal/`RequireApproval`；旧 DB `propose` 载入为 ignore（`legacyImport`），旧 `requireApproval` 忽略；收件箱 attention-only | ✅ | `internal/engine/evaluate.go:348-390`（explicit 直接建单/inferred accept mode）；`internal/engine/gate.go:52-56`（Policy 无 RequireApproval）；`internal/domain/ticket.go:10-13`；`internal/engine/persist.go:156-162`（legacy import）；`internal/ui/inbox.go:1-140` |
| **IM 渠道三态与生命周期** | 显式 1/0 优先、缺省按凭据/flag 推导；bool 编码兼容；flag-only bootstrap；每渠道 generation/串行 apply；Stop/Close/sender 注销；切 transport 清 cursor | ✅ | `internal/runtime/im.go:33-130,186-248,345-470,749-760,1045-1075`；`internal/runtime/settingscodec.go:1-70,182-230`；`cmd/zeokeel/main.go:300`、`cmd/zeokeel/imwiring.go:91` |
| **钉钉 DWS（默认，个人 OAuth）** | 长驻 `dws event +listen-im … -f ndjson`（无 poll/cursor、event id 去重、1s→30s 重连）；`dws chat +messages-send --as user`（隐藏 marker）；`dws auth status --format json`/`auth login [--device]`；短命令串行；无 app-key | ✅ | `internal/im/dingtalk/dws.go:1-28,63-68,90-130,569-700,842-890,970-1032,1069-1240`；`internal/runtime/dws.go:1-205`；设置 `internal/runtime/pages.go:483-500`；文档 `design/im-channels.md` |
| **飞书官方长连接（新装默认）+ longpoll fallback** | 新装默认 `feishu_transport=ws`（`larkws`+`OnP2MessageReceiveV1`）；旧凭据-only 配置迁移时 pin `longpoll`；面板 Options 只有 `ws`/`longpoll`（无 webhook）；longpoll 保留 cursor；检查 tenant token、401/403 授权入口 | ✅ | `internal/im/feishu/ws.go:1-100`；`go.mod:12`；`internal/runtime/im.go:87,859-873`；`internal/runtime/pages.go:474-476`；`internal/view/channels.go:45-51`；`internal/runtime/settingscodec.go:101-106`（迁移 pin）；`internal/runtime/dws.go`（`checkFeishuAuth`） |
| §5.1 eino 选型 | eino ADK + 官方中间件 + plantask/agentsmd/web | ✅ | `internal/agent/engine_adk.go`；`internal/agent/builtin.go:76,158`；`internal/agent/webtools.go:84-160` |
| §5.2 会话与轮次 | `adk.Runner`+`session.FileStore`；恢复走 patchtoolcalls + recovery note | ✅ | `internal/agent/agent.go:306`（`RunSession`）；`internal/agent/session.go`；`internal/agent/engine_adk.go`（`patchtoolcalls`）；`internal/runtime/recovery.go:21` |
| §5.4 上下文预算 | summarization + reduction | ✅ | `internal/agent/compact.go`、`internal/agent/truncate.go` |
| §5.5 命令执行与沙箱 | 相对路径拒绝、blocked（咨询性）、超时；非安全边界 | 🟡 | `internal/agent/workspace.go`、`internal/agent/tools.go:75`（`runBash`）、`:204`（`scrubEnv`）；没有 `os.Root` 边界 |
| §5.6 子智能体 | 官方 subagent + explorer/verifier | ✅ | `internal/agent/subagent.go`；`internal/runtime/worker.go`（`DefaultSubagents`） |
| §5.7 可观测性 | 消息流 + 用量/压缩 + slog + health | ✅ | `internal/runtime/runtime.go:483`（`stream`）；`cmd/zeokeel/logging.go:27`；`internal/runtime/supervisor.go:151` |
| §5.8 崩溃恢复 | `RecoverAssignments` + patchtoolcalls | ✅ | `internal/runtime/recovery.go:21`；`cmd/zeokeel/main.go:265`；`internal/agent/engine_adk.go` |
| §5.10 能力矩阵 | MCP/skill/定时/目标/⌘K/present/ask/子智能体/后台/plantask/AGENTS.md/web/`read_image`/`job_list`/per-workspace browser MCP | 🟡（仍缺 plan mode 与 agent 可调用 workflow） | 工具 `internal/agent/imageread.go`、`internal/agent/jobs.go:275-309`、`internal/runtime/worker.go:468`；仍缺 plan mode 与 agent 可调用 workflow |
| **内置工具（rc.4）** | plantask/Task*、AGENTS.md 不可信信封 ≤64KB、web_fetch DNS pin/Proxy:nil、web_search 前 5；`read_image`（元数据-only，无像素）、`job_list`、`subagent` 命名；`read_resource`（DSH orchestrator_read 等价，分页读自己的 ticket/inputs/trigger/thread，只读/redacted）；Worker 域工具 `ack/handback/thread_unrelated/request_status`；SuperAgent 无工具 | ✅（read_image 无视觉；DSH main orchestrator 工具集缺席） | `internal/agent/imageread.go:1-120`、`internal/agent/jobs.go:275-309`、`internal/agent/subagent.go:55`、`internal/runtime/artifacts.go:121-222,205-214`、`internal/runtime/rc3.go:17-160`、`internal/runtime/worker.go:580`；`internal/runtime/rc2.go:121-132`（SuperAgent payload 无 tools）；`audit/tool-gap.md` |
| §6.3 界面结构（UX 轮 IA） | 日常五项：超级Agent / 收件箱 / 项目 / 执行 / 设置；工程页（对话记录·消息·连接·触发·评估）收进默认折叠的「诊断（高级）」；UI 用「项目」称呼工作区 | ✅ | `internal/view/vm.go:486-520`（`Nav`/`NavAdvanced`/`SectionLabel`）；`internal/ui/chrome.go:60-110`（rail + 诊断折叠）；`internal/ui/app.go:441-455`（⌘K/⌘//⌘N/⌘,） |
| **项目面板（概览/设置/统计）+ per-workspace 浏览器/MCP/技能** | 项目页三 tab；设置编辑浏览器（Chrome/Edge、无头、CDP 0=自动、profile 只读）、启停/状态、技能开关（全局默认开/项目可关）；**node/npx 前置检查缺失时显示警告+OS 安装命令+重新检查，启动按钮禁用并说明原因**；统计显示会话/任务/进行中/等待/完成/失败/最近活动/工具可用性；浏览器每项目独立 profile+唯一 CDP 端口，`chrome-devtools-mcp` 的 `mcp__browser__*` 只注入该项目 Worker | ✅（最近活动 runtime 未投影时为空态） | `internal/view/workspacepanel.go:1-99`；`internal/ui/workspacepanel.go:193-520`；`internal/runtime/workspacesettings.go:75-160,296-333,427-480`；`internal/store/workspacesettings.go`；`internal/browser/detect.go:10-65`、`internal/browser/manager.go:99-107,185-245`、`internal/browser/launch.go:22-56`、`internal/browser/mcp.go:12-15,44-48,118-140`；前置 `internal/browser/prereq.go:1-140`、`internal/runtime/rc3.go:170-205`、`internal/ui/workspacepanel.go:613-692`；注入 `internal/runtime/worker.go:468` |
| **输入条（聊天/派活双 target）** | 多行 TextArea；Enter 换行、⌘/Ctrl+Enter 发送（IME 安全）；target chip「超级Agent（聊天）/项目 X（派活）」决定提交目标；失败保留草稿+再次发送；回答可展开/复制/查看全部 | ✅ | `internal/ui/composer.go:19-220`（target bar/inputBar/context/展开复制）；`internal/ui/commands.go:137-188`（submit/resend/失败保留）；`internal/runtime/ux.go:134-172`（`DispatchTo`）；对话记录 `internal/ui/chat.go`/`internal/view/chat.go` |
| **收件箱（attention-only）** | 一个「待你决定 N」（runtime `AttentionCount` 单源）；**只显示任务问题/受阻/失败**（回答/重试/停止）；没有批准/拒绝/撤销；j/k/Enter；pending 与就地错误；窗口隐藏时新待办系统通知 | ✅ | `internal/ui/inbox.go:1-233`；`internal/runtime/ux.go:314-438`（`attentionItems`/`AttentionCount`/`notifyAttention`）；`internal/view/attention.go`；`internal/ui/uxsupport.go:286-300`（hidden notification） |
| **超级Agent 聊天+派活双态 / 内联反馈 / 忙碌态** | 提问→模型/确定性回答；派活→「已派发到 项目 #N」；待批→「需要你确认」；回答内联在输入条上方+总览；非失败不标红；发送/保存/重启有 pending；有「对话记录」页 | ✅ | `internal/runtime/chat.go:1-116`；`internal/view/chat.go:1-83`；`internal/ui/chat.go:20-98`；`internal/ui/composer.go:163-220`；`internal/ui/commands.go:1-220`；`internal/ui/settings.go:436-470` |
| **聊天流式输出 / 停止生成** | OpenAI 兼容 SSE；增量文本 60ms 节流发布；对话显示打字光标；`停止生成` 取消并保留已生成部分（标「已停止生成」）；最终文本只追加一次 | ✅ | `internal/runtime/rc2.go:24-190`（`setChatPartial`/`StopChat`/`chatAnswerStreaming`/`streamChat`）；`internal/runtime/chat.go:38-52`（partial → ChatVM）；`internal/ui/uxsupport.go:232-240`（`ChatStreamer`）；`internal/ui/composer.go:241-305`（光标/停止按钮） |
| **聊天/历史 Markdown 渲染** | 标题/粗斜体/行内代码/围栏代码块/有序·无序列表/http(s) 链接；不解释 HTML；未匹配标记按字面；链接经系统浏览器 | ✅ | `internal/ui/markdown.go:19-180`（`renderMarkdown`/`parseInline`/`openExternal`）；`internal/ui/chat.go:75`；`internal/ui/composer.go:263-267` |
| **计划/待办可见** | `.zeokeel/plan/*.json` → `view.PlanVM`；执行详情与对话任务卡显示步骤/状态；`3/5 完成 · 1 进行中 · 1 需要你` 计数 | ✅ | `internal/view/plan.go:1-51`（`PlanVM`/`Counts`）；`internal/runtime/rc2.go:302-433`（`PlanTasks`/`currentPlan`）；`internal/ui/planpanel.go:29-85`；`internal/ui/ticketdetail.go:97-98`；`internal/ui/composer.go:238-239` |
| **应用到项目（显式回写）** | 只复制记录过的交付物；拒绝绝对/越界路径；同名文件先备份 `.bak-<timestamp>`；返回相对路径；UI 确认列出目标/文件/覆盖警告 | ✅ | `internal/runtime/rc2.go:193-270`（`ApplyArtifacts`/`safeExecPath`/`ArtifactRelPaths`）；`internal/ui/applyproject.go:1-199`；`internal/ui/ticketdetail.go:291-301` |
| **旧式审批/提案（rc.4 删除）** | 无 Approve/Reject/UndoReject/proposal/`RequireApproval`；旧 DB `propose` 由 load 转 `ignore`（legacyImport）；收件箱 attention-only | ✅ | `internal/domain/ticket.go:10-13`；`internal/engine/persist.go:156-162`；`internal/engine/gate.go:52-56`；`internal/ui/inbox.go:1-140` |
| **原生应用菜单** | App/文件/编辑/窗口；新建对话 ⌘N、设置 ⌘,、打开主窗口；标准编辑 roles（撤销/重做/剪切/复制/粘贴/全选） | ✅ | `internal/ui/menu.go:1-72`（`appMenu`）；`internal/ui/app.go:345`（`mygo.App.SetMenu`）；窗口内快捷键 `internal/ui/app.go:441-455` |
| **系统无障碍偏好** | mygo `Theme.Preferences`：TextScale × `ui.font_scale`、ReduceMotion、HighContrast（强化边框/文字）、系统 Accent | ✅ | `internal/ui/prefs.go:1-61`（`readUIPrefs`/`applyPrefs`/`accentPrefs`）；`internal/ui/app.go:441-462`（合并 scale）；`internal/ui/stream.go:59-62`（ReduceMotion）；`internal/ui/tokens.go:63`（`fs`） |
| **关于 / 更新检查** | 设置›关于显示版本（runtime `Version`）/MIT 许可证；「检查更新」只查询 GitHub Releases API（15s、不下载），显示最新版本与发布页链接 | ✅ | `internal/ui/about.go:1-117`；`internal/runtime/rc2.go:355-415`（`CheckForUpdate`，仅 GET）；`internal/view/pages.go:469-470,644`（About group）；菜单入口 `internal/ui/app.go:345` |
| **Ticket 身份（domain ID + Number）** | 行/详情查找键=domain ID；详情 VM 的 ID 也是 domain ID、Number 单独展示 `#number`；工作区会话点击同一 Ticket | ✅ | `internal/runtime/pages.go:302,310`（行/详情 key）、`:385`（`ID`=domain ID、`Number` 单独）；`internal/view/pages.go:377-401`；`internal/view/vm.go:87-95`；`internal/ui/execution.go:95,114-120,150` |
| **项目（工作区）opaque ID / 重命名 / 删除 / 过滤** | UI 称「项目」；`ws_*` 随机 ID、名字可改、根目录绝对化/重复拒绝；选中项目过滤消息/执行；删除空项目（行+策略+engine 路由），有工单拒绝 | ✅ | `internal/runtime/workspaces.go:23-95`（创建/ID/root）、`:111-150`（Rename）、`:155-190`（Delete，`:182` `Unregister`）；`internal/store/store.go:390`（删除行）；`internal/engine/engine.go:157`（路由注销）；过滤 `internal/ui/workspace.go:346-400`、`internal/view/pages.go:419-430`、`internal/view/vm.go:566-580` |
| **设置 codec / 一次性迁移 / 事务保存 / secret** | UI 开关读写 `1/0`（读兼容旧 true/false）；`LoadSettings` 一次性迁移（bool 归一、IM 启用/transport、`im_self` 拆分）+ schema stamp；SaveSettings 先快照后提交；secret 不回显、Inspect/status/health redaction | ✅ | `internal/ui/channels.go:381-395`、`internal/ui/settings.go:405-414`；`internal/runtime/settingscodec.go:1-70,76-230`；`internal/runtime/pages.go:678-707`（迁移）、`:567-690`（SaveSettings）、`:539-550`（secret）；`internal/store/settings.go:28-55,106-146`；`internal/store/redact.go`；`internal/store/inspect.go:46-117,231,260` |
| **模型设置与测试连接** | 设置›模型：端点/API Key/模型名；「测试连接」探测已保存配置（401/403/404 分类）；保存后即时生效；未配模型时总览「去配置模型」，任务停在待你决定；模型响应超时在设置›执行 | ✅ | `internal/runtime/pages.go:543-558`（字段）、`:524-526`（超时）；`internal/runtime/ux.go:36-125`（`ApplyModelSettings`/`CheckModel`）；`internal/ui/settings.go:501-535`（测试行）；`internal/ui/overview.go:50-95`（停车卡） |
| **首跑引导（必填/测试/演示/选择器）** | 四步向导；必填/可选标记；模型「保存并测试」；「稍后设置（演示模式）」；项目根目录原生「浏览…」；完成后落点提示 | ✅ | `internal/ui/onboarding.go:60-180`；`internal/ui/uxsupport.go:100-170`（skip/finish/picker）；`internal/runtime/ux.go:423-438`（`SkipOnboarding`/`DemoMode`）；`internal/runtime/onboarding.go:71-108` |
| **路径真相（项目 vs 执行副本）** | 详情「位置」分别列项目目录/执行副本，可打开/复制；交付物可打开/复制；提示词明确只在副本工作、不声称改项目；需要回写时用「应用到项目」（显式、带备份） | ✅ | `internal/ui/ticketdetail.go:250-286`；`internal/runtime/ux.go:279-303`（`ExecutionPaths`/`absoluteArtifactPath`）；`internal/runtime/worker.go:570-582`（提示词事实）；`internal/ui/deliverables.go:60-85` |
| **IM 连接向导** | 按 transport 只显示该方式字段；draft 校验+用填写值检查（不保存）；通过才保存/显式仍保存；DWS 版本/登录命令/授权链接可复制；Feishu chats（ws 可选/longpoll 必填）；暂停监听开关 | ✅ | `internal/ui/channelwizard.go:1-520`；`internal/runtime/ux_im.go:1-95`；`internal/runtime/im.go:1392-1505`（per-transport specs/校验）；`internal/im/dingtalk/dws_setup.go`；`internal/im/feishu/ws_chats.go` |
| §6.5 主题 / 深色 / 字号 / 最小窗口**与可访问性** | 品牌 token 随 frame theme；`ui.font_scale` 三档可编辑；提高 faint 对比度与 focus token；行 `Focusable/Role/Label/Tooltip`、28px 目标/16px 图标；状态栏去 debug；`StateKey:"main"` 记忆窗口、最小 1100×720；系统 TextScale/ReduceMotion/HighContrast/Accent 已接 | ✅ | `internal/ui/tokens.go:100-145,195`；`internal/ui/app.go:238-264,320-330`；`internal/ui/settings.go:244-300`；`internal/ui/chrome.go:143-160`；`internal/ui/uxsupport.go`；投影 `internal/runtime/pages.go:537-539` |
| §6.7 生命周期（常驻/恢复） | 单实例锁 + 托盘/关窗隐藏 + `-headless` + supervisor + systemd/launchd | ✅（自启/唤醒除外） | `internal/store/lock.go`；`cmd/zeokeel/service.go:20`；`internal/runtime/supervisor.go:105`；`packaging/systemd`、`packaging/launchd` |
| §7 持久化与 Effects/Outbox | schema v3、Effects、Outbox、会话事件、归档 | ✅ | `internal/store/schema.go:17`（`SchemaVersion=3`）；`internal/effects`；`internal/outbox`；`cmd/zeokeel/ops.go:83` |
| §7.3 归档 | `archive --before 180d` → archive.db；outbox 保留 | ✅ | `cmd/zeokeel/ops.go:83` |
| §8.1 模式与 CLI | 桌面 / `-headless` / 演示 / pipeline / 七个子命令 | ✅ | `cmd/zeokeel/cli.go:47`（`cliCommands`）；`cmd/zeokeel/service.go:20` |
| §8.2 首次配置 | 向导四步落库（必填/测试/演示跳过/原生选择器）；`setup` 非交互；面板可新建项目（原生选择器） | ✅（gh 校验/钥匙串除外） | `internal/runtime/onboarding.go:114`；`cmd/zeokeel/cli.go:216`；`internal/ui/onboarding.go:60-180`；`internal/runtime/ux.go:423-438`；`internal/runtime/workspaces.go:23` |
| §8.3 打包分发 | tar.gz / 未签名 .app zip / Windows zip + install.ps1 | 🟡 | `scripts/package.sh`；缺 `.dmg`/NSIS/deb/PKGBUILD/updater；CI 不出包 |
| §8.5 可观测性与运维 CLI | status/check/setup/login/backup/archive/stop、日志、supervisor、退出码 0/1/2/3、secret redaction | ✅（自启/唤醒除外） | `cmd/zeokeel/cli.go:76,143,216,354`；`cmd/zeokeel/ops.go:46,83`；`cmd/zeokeel/logging.go:27`；`internal/runtime/status.go:21-26,137`；`internal/runtime/supervisor.go:105`；`internal/store/inspect.go` |
| §9 零 cgo | `check-nocgo.sh` + CI gate | ✅ | `scripts/check-nocgo.sh`；`scripts/verify-fixes.sh`；CI `check` job |
| §1.2 + 附录 A 能力清单 | pause/retry/resume/stop、follows、投递三段式、prompt 6 层+历史；DSH 对齐（明确直接接单/推断 accept mode）；steer/inject ❌ | 🟡（仅 steer/inject） | `internal/engine/lifecycle.go`；`internal/runtime/prompts.go`；`internal/runtime/chat.go`；`internal/runtime/im.go` |
| 008 扩展路径（新增 Source） | Kind/registry/配置/游标/Channel 完整；IM 管理器可参考；无 Source 实例表/面板添加来源 | 🟡 | `internal/sources/`；`cmd/zeokeel/github.go`、`cmd/zeokeel/imwiring.go`；cookbook `design/how-to-add-a-source.md` |

**明确未做（2026-11-04 rc.4）**：
- `.dmg`、NSIS/deb/PKGBUILD、自动更新、代码签名与公证、CI 出包。
- 系统钥匙串（API Key 明文 SQLite 0600）；`UserError{Code,Place,Values}`（错误仍是自由文本）。
- 领域类型 `domain.Source` / `domain.Thread`；`steer/inject`、已读回执的完整 003 语义；飞书 webhook 与 IM 媒体下载（只保留引用）。
- Source 实例表/面板“添加来源”：GitHub Source 仍需 `-repo/-gh-login`；评估组只读（规则/admission/accept mode 不可编辑）。
- 开机自启、休眠唤醒（委托 systemd/launchd）。
- 工作区删除只允许空工作区（有工单拒绝）；会话不能重命名/删除；每工作区会话只投影最近 50 条。
- IM 真实凭据/DWS 租户端到端未验证；连接向导只验证凭据/订阅，不代替真实消息 E2E。
- **rc.4 明确未做**：真正在原项目执行（执行副本 + apply-back）、系统钥匙串（SQLite 0600）、多窗口、
  自动更新只检查不下载、真实飞书/DWS 租户 E2E、`read_image` 无像素（需 vision endpoint）、DSH main orchestrator 工具集、
  浏览器需 Chrome/Edge+node/npx；打包仍无 `.dmg`/deb/安装器。
- **测试/闸门状态**：2026-11-03 快照曾全绿；修复波 + UI/UX 优化轮 + rc.2/rc.3/rc.4 后由 Lead 在 bump VERSION 后跑最终 `verify-fixes.sh`/`release-check.sh`，本文不预称全绿。
**维护规则**：以后每次改代码，同一提交内改这里；新增 ✅ 必须附 `file:line`，否则按 🟡 处理。
