# 第二轮设计复审：新 UI / 工作区 / IM 渠道 / 内置工具

- 日期：2026-11-04（Asia/Shanghai）
- 复审对象：HEAD `f4200da`（R2/R3：DSH 式侧边栏、工作区/会话、原生目录选择器、字体 +2、OS/时区、IM 渠道管理 + DWS、plantask/agentsmd/web 工具）
- 性质：**只读设计评审，没有改生产代码**。四份独立详细报告：
  - [架构/并发/命令边界](verify/design-r2-arch.md)（conf-arch）
  - [持久化/settings/IM 配置](verify/design-r2-store.md)（nih-core）
  - [工具与安全](verify/design-r2-tools.md)（fix-agent）
  - [UX/产品一致性](verify/design-r2-ux.md)（conf-product）
- 说明：无显示器，未开真实窗口；"会卡"来自同步调用链与超时参数，不是实测卡顿时长；DWS 本机未安装，按其 CLI 契约评审。

## 结论

方向是对的：**超级Agent + 工作区树 + 底部监控**比七页侧栏更接近用户心智；原生目录选择器、渠道级启停、"禁用=未监听"、无乐观更新都是应该保留的设计。但当前实现有 **7 个 P0 级结构问题**，其中"在设置页启用 IM"和"发消息/保存设置"在真实运行中是**失效或会冻结窗口**的；这些不是文案问题，而是 1.0 承诺不成立。

## P0（发布前必须修）

| ID | 问题 | 证据 | 后果 |
| --- | --- | --- | --- |
| **P0-1** | **IM 启用开关编码分裂，开关永久无效** | 面板开关写/读 `"true"/"false"`（`internal/ui/settings.go:357,366`、`internal/ui/channels.go:162`）；runtime 只认 `== "1"`（`internal/runtime/im.go:54,60`）。黑盒探针：kv 写 `feishu_enabled=true` → health `im:feishu ready/未启用`；写 `"1"` 才进入配置检查。没有任何 UI/CLI/setup 路径写 `"1"`。两边测试各自通过，边界从未被测。 | 用户打开"启用飞书/钉钉"→保存→面板显示"已启用/未检查"，渠道**永不启动**。这是"设置保存了但什么都没发生"的最坏形态。 |
| **P0-2** | **UI 命令边界仍是同步 RPC，主线程会冻结** | `Dispatch`→DB + `EvaluateDue`（模型，评估器超时 90s）在 UI 帧内同步执行（`runtime/runtime.go:752-796`）；`NewConversation` 同链（`workspaces.go:101-156`）；`CreateWorkspace` 同步 scan+DB 写（`workspaces.go:20-72`）；`SaveSettings` 同步写 store，并同步重启 IM（旧渠道 `Stop` ≤5s/渠道，`pages.go:568-590`、`im.go:208-270`）。接口注释明确要求"只入队、不等待文件/数据库"（`ui/app.go:51-56`），与实现相反。 | 发一句话、新建会话、保存来源设置都可能让窗口"点了没反应"，最长等模型/渠道停止返回；违反设计 §6.1 三条铁律。 |
| **P0-3** | **投影与面板的 Ticket 身份未定义：详情/跳转失效** | 行 ID 是编号字符串 `fmt.Sprint(t.Number)`（`runtime/pages.go:288`），详情 map 用 `domain.ID` 做 key（`:296`）；UI 用行 ID 查详情（`ui/execution.go:149,160`）→ 永远查不到富详情；`viewtest` fixture 把两边都写 `"42"` 掩盖了这个洞。工作区会话点击 `openTicket(conv.ID)` 依赖另一套查找。 | 真实运行时 Ticket 详情（Mandate/Inputs/Follows/Artifacts）**永不显示**；工作区点会话可能提示"不在执行投影"。 |
| **P0-4** | **升级迁移缺失：已配置的 IM 静默停摆；flag 无法启用** | 切换前设置页已有 `source.feishu_app_id`/`source.dingtalk_app_key` 等凭据；切换后 `*_enabled` 缺省 false，flag 只覆盖凭据、不能置 enabled（`im.go:83-108`、`cmd/zeokeel/imwiring.go:28-33`）；store 只有 CAS revision，没有 settings schema 版本/迁移（`store/settings.go:17`）。 | 老用户升级后"已配置=未启用"，且命令行只给凭据也启动不了；没有任何迁移提示。 |
| **P0-5** | **AGENTS.md 被当作权威指令注入 + web_fetch SSRF** | eino agentsmd 的框架文本是"These instructions OVERRIDE any default behavior and you MUST follow them exactly"（vendor `agentsmd/loader.go:241-243`），仓库内容（PR 分支/第三方仓库）本就不可信，而 Worker 同时有 bash + web_fetch；web_fetch 先解析 DNS 校验一次，随后默认 `http.Client` 再解析一次（DNS rebinding TOCTOU），且默认 Transport 读 `HTTP(S)_PROXY`（`agent/webtools.go:101-190`），模型可控 URL = 外发通道。 | 间接 prompt injection → 越权/数据外发；SSRF/代理绕过内网限制。 |
| **P0-6** | **DWS 的"每渠道串行 worker"只在死代码里，生产路径并发调 dws** | runtime 生产路径用裸 `commandDWSClient` + `newDWSPoller`（`runtime/dws.go:31-98`、`im.go:400-404`），`CheckChannel` 又每次 new client（`im.go:597-601`）；`DWSTransport` 的 `dwsRunner`/去重/Close 未被生产使用（`dingtalk/driver.go:155-190`、`dws.go:586-790`）。 | poll/send/manual-check 可并发起多个 `dws` 子进程，违反 im-channel-spec §0.4，登录态/管道可能互相打断；手工检查与轮询不一致。 |
| **P0-7** | **"超级Agent"没有真正的聊天，反馈自相矛盾** | README/ui2-spec 承诺"全局问答"，实现只有派活；无工作命中时用关键词模板兜底（`runtime/workspaces.go:171-214`），默认总览页不渲染消息流（`ui/overview.go:15-45`）；同一条消息 runtime 返回 error → 输入框红字+保留草稿（`ui/input.go:42-44`），同时流里显示「已答复 + 超级Agent：…」（`runtime.go:458-473`）。 | 用户认为"没发出去/坏了"；红色错误被日常消耗后真正的失败不再醒目。这是产品选择（聊天 vs 派活）未拍板导致的实现撕裂。 |

## P1（应在下一轮修）

| ID | 问题 | 证据/后果 |
| --- | --- | --- |
| P1-1 | **prompt 工具名与注册名不一致** | prompt 写 `task_create/task_get/...`（`runtime/worker.go:573`），eino 注册 `TaskCreate/TaskGet/...`（`agent/builtin.go:34-39`）；模型照 prompt 调用会 "tool not found" → 整轮失败。`conformance3_test.go:314` 还断言了错误名字，CI 保护 bug。 |
| P1-2 | **AGENTS.md 上限无效 + 压缩后重注入** | eino 总是包含第一个文件（vendor loader.go:121-122），272KB 实测全量发送；agentsmd 在 summarization 之后，压缩会丢掉注入再重注 → 每次模型调用多一次摘要、历史抖动（探针 2 轮=4 次请求/2 次压缩，每次带 96KB AGENTS.md）。 |
| P1-3 | **plantask 把任务 JSON 写进工作区根目录** | `BaseDir=workdir`（`agent/builtin.go:44-49`）产生 `1.json`/`.highwatermark` 与用户文件冲突；`TaskList` 会把任意数字 JSON 当任务；artifact 扫描只跳过 `.zeokeel`（`runtime/artifacts.go:189`），任务文件会被当成交付物。建议 `BaseDir=.zeokeel/plan`。 |
| P1-4 | **设置保存先改内存再 CAS，失败不回滚** | `pages.go:537-580` 冲突/写盘失败时内存仍是新值，面板显示未落库配置（含 secret），`settingsRev` 与磁盘漂移；IM 重启 hook 可能不执行。 |
| P1-5 | **允许名单保存会清掉每个工作区的策略字段** | `pages.go:887-908` 用两字段字面量重写 Policy，wipe accept/reply/rules，且 N 个事务。 |
| P1-6 | **workspace ID 由显示名 slug 派生，中文名全部塌缩** | `workspaces.go:36` + `slug` 丢非 ASCII：所有纯中文名都成 `ws_workspace`，第二个中文工作区被拒"已经存在"；`Acme 网页`/`Acme API` 同 ID；改名会孤儿历史。建议 opaque ID + 可变 name。 |
| P1-7 | **工作区选择只是"一个页面"，不是上下文** | `workspaceSel` 只影响工作区页与侧栏高亮（`ui/app.go:178`、`workspace.go:294-312`）；消息/执行始终全局（`ui/execution.go:20` "全部任务"）。侧栏树暗示"当前工作区"，离开即失效；同一 Ticket 有 3 个入口。 |
| P1-8 | **IM 身份/Trigger 字段错误** | `im_self` 被飞书与钉钉共用（不同 ID 空间，`im.go:59,68-70`）；`imTrigger` 用 `WorkspaceScope("")` = `"ws:"` 畸形 scope、ID 不含 conversation（`im.go:505-513`）；`ev.At==0` 产生 1970 时间且 `IsZero()` 永不触发（`:514-517`）。 |
| P1-9 | **key 明文存储 + Inspect 返回全部 KV** | `store/settings.go` 明文 secret；`store.Inspect` 把整个 KV（含 `source.*_app_secret`、`onboard.api_key`）放进 HealthView（`inspect.go:76,96-98`）；任何 status/debug 打印都会泄密。 |
| P1-10 | **IM 渠道生命周期/停止不完整** | `ApplyIMSettings` 无 generation/串行，可双启动；`im.Stop` 从未在 main 调用（`cmd/zeokeel/main.go:301` 只有 start）；disable 不注销 outbox sender；`Stop` 不 Close driver（`im.go:208-270`）。 |
| P1-11 | **DWS 契约/错误处理脆弱** | 文本猜状态码、严格 at/skipped 不展示、marker 可能可见、切 transport 不清 cursor、stderr 不脱敏（`dws.go:255-263`）、outbox 不处理 `ErrDWSAuthRequired`/永久错误、失败批无限重试可 wedge cursor。 |
| P1-12 | **工具默认开启且无策略** | `PlanTasks/AgentsMD/WebTools=true` 对每个 Worker 硬编码（`runtime/worker.go:481`），无 per-workspace/设置开关；prompt 无条件宣传；子智能体默认白名单不含 web/task；AGENTS.md 对子智能体不生效。 |
| P1-13 | **web_search 端点/实现脆弱** | `fmt.Sprintf` 对配置 URL 里的 `%` 会变成 `%!`(NOVERB)；DDG HTML 抓取无 ToS/限流/封禁检测/缓存；查询隐私；30KB 输出低于 reduction 32KB 阈值，永不 offload；无 Content-Type 检查。 |
| P1-14 | **ReceiveTrigger 内联 EvaluateDue+Publish** | `entry.go:40-50` 让 IM poll 循环等待模型评估；违背两阶段/轮询解耦，慢模型会拖住整个渠道。 |
| P1-15 | **onboarding 来源步骤没有实际接线** | `onboarding.go:84-94`/`main.go:158-161` 只存 `onboard.*`，选 GitHub/IM 后没有连接任何东西。 |

## P2（可排期）

- 投影每帧/每次 Publish 全量重建，无上限（D-09）；`Selected` 是死字段（D-11）。
- 工作区只 create-only：不能 rename/delete/编辑，重复 root 允许，相对 root 接受，kind 无解释，scan facts 不可见（D10/D19/F-06）。
- 字体是固定 15 + 295 处字面量，没有比例/最小窗口；232px 侧栏固定，高 DPI/小窗口会裁切（F-08/F-09）。
- 传输选项与 runtime 能力不一致：飞书 ws/webhook 可选但未实现；钉钉 stream 一度被标未实现（已修）；检查只验凭据，不验证 transport 可运行（F-10/D-14）。
- 设置保存"热更新"文案对 boot-only 项（GitHub）不成立（F-14）。
- 工作区事实只创建时 scan 一次，路由会持续用陈旧 facts（D19）。
- flat `source.*` 键无法表达多实例/多账号；cursor 已有 instance 维度而 config 没有（D20）。
- DWS 消息 messageId 丢弃、失败批重试语义、marker 可见（D13/D15/D16）。
- SettingsVM 带明文 secret（D-15）。
- UI 无 plan/task 展示（T10）；工具能力对用户不可见（F-13）。

## 结构性建议（按收益排序）

1. **异步命令边界（先做，收益最大）**：UI 只 `submit(op)`，runtime 后台队列执行，快照回传 `pending/running/failed/done`；UI 显示"发送中/保存中/正在重启渠道"。禁止 handler 内同步 DB/模型/渠道 Restart。
2. **一个 settings 类型层 + schema 版本 + 迁移**：`BoolSetting` 统一编码（读兼容 true/false/1/0，写一种）；`setting.__schema` v1；`enabled` 三态（显式优先，缺省按凭据完整性推导，flag 也算）；`source.<instance>.*` 预留多实例。
3. **统一身份模型**：workspace 用 opaque stable ID；Ticket 行/详情/会话统一用 domain ID（或显式 number→ID 映射）；projector→snapshot→UI 的 seam 测试用真实 ID，不用"42"。
4. **IM 渠道状态机**：desired vs actual（enabled/transport/auth/error）、串行 worker、监督/重连、sender 随状态注册/注销、Close 驱动的 Stop、统一一个 DWS runner（poll/send/auth/check 共用）。
5. **工作区收口**：要么真正作为上下文（消息/执行默认按 workspace 过滤，可清除），要么取消工作区页的会话列表、只做筛选器；补 rename/delete、root 唯一、kind/facts 展示；CreatedBy 用 Self/Actor 而不是 Source。
6. **信任边界**：AGENTS.md 用"不可信仓库指令"信封（或替换 eino 框架文案）+ 工作区信任开关；web 内容作为不可信数据；web_fetch 用 pin IP 的自定义 Dialer、`Proxy:nil`、按工作区 allowlist/默认关闭；per-workspace 工具策略。
7. **UI 设计系统**：字体用 scale token（设置项，而不是 magic +2），最小窗口/侧栏自适应；workspace tree 键盘/读屏；目录选择器保留手输 fallback。
8. **DWS 作为可替换适配器**：明确 CLI 版本/契约，接真实测试租户；auth 流程能真正触发登录；transport 切换清 cursor；错误映射到 outbox 永久/重试。

## 建议修复顺序

- **Sprint A（P0，阻塞发布）**：P0-1 布尔编码 + P0-4 迁移 → P0-3 身份 → P0-2 异步边界 → P0-6 DWS runner → P0-5 信任边界 → P0-7 产品拍板（聊天或派活）。
- **Sprint B（P1）**：P1-1 工具名、P1-2/3、P1-4/5、P1-6/7、P1-8/9/10/11、P1-12/13、P1-14/15。
- **Sprint C（P2）**：主题/字体系统、工作区管理、投影增量、实例化 sources。

## 需要你拍板的产品问题

1. **超级Agent 是聊天，还是只派活？** 决定 P0-7、P1-7 的修法。建议先做"聊天 + 派活"双态：全局输入→Super Agent 回答/追问；选中工作区→派活。
2. **选中工作区是否过滤「消息/执行」？** 建议过滤（可一键"全部"），否则侧栏树是假上下文。
3. **DWS**：真实 CLI/版本/登录流程是什么？当前按 JSON 契约实现但未接真实租户。
4. **web/AGENTS.md 默认策略**：建议默认关闭 web，按工作区开启；AGENTS.md 改为不可信信封。
5. **多账号/source instance 是否进 1.0？** 若不进，README 明确"仅单实例"，否则后续迁移面很大。

## 保留的优点

原生目录选择器（goroutine + postToUI，不阻塞帧）、禁用渠道诚实"未启用"、DWS 无 shell/超时/杀进程、扫描有界、store CAS、设置无乐观更新、工具错误作为 observation 而非崩轮，这些都是正确方向，修复时不要回退。
