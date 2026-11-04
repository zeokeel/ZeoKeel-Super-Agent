# 对齐 DSH 面板：UI 功能 / 布局复刻清单

> 依据：`_ref/src/client/**`（React 客户端）、`_ref/docs/005-panel.md`、`_ref/src/client/index.tsx`。
> 结论先说：**DSH 的 SuperAgent 是一个“监督面板插件”，不是整个应用**。聊天/Session/Workspace 由 DSH Host 提供；插件只注册侧栏图标、主面板和 Plugin Manager 配置页。我们当前的“桌面 App + 聊天 + 工作区树 + 监控”是另一个产品形态，这是 UI 显得怪异的根因。

## 0. 先做架构决策（阻塞项）

**A. 真插件（推荐，和 DSH 完全一致）**
- 以 DSH 插件形式交付：TS/React，注册到 DeepSeek Harness Web 的 `sidebar.panellist`、`slots.main`、`plugins.row.config`。
- 聊天/Session/Workspace 使用 DSH Host 自带界面；插件只提供监督面板与配置表单。
- 依赖 DSH 宿主：`@deepseek-ai/dsh-client-ui-layout`、`ui-workspace`、`ui-settings`、`ui-renderer`、`api-session-controller`、Cordis 等。
- 优点：布局/组件/主题/键盘/ARIA/会话跳转完全原生；缺点：不再是独立桌面 App，需要 DSH 宿主。

**B. 独立 App 复刻（不是插件）**
- 保留 mygo 桌面壳，把左侧栏+工作区树+聊天全部改成 DSH 的 6 tab 顶部导航 + 头部工具栏 + 详情页。
- 需要自己实现 Host 的 Session 跳转、设计 token、面包屑/返回、settings 保存历史等；只能“像”，不能“是插件”。
- 优点：保持单机桌面；缺点：长期维护两套交互，且用户仍会感到“不是 DSH”。

> 本清单按 **A** 写；若选 B，页面/布局清单同样适用，只是 Host 能力要自研。

## 1. DSH 面板的骨架（必须照抄）

### 1.1 注册方式
- 侧栏：`sidebar.panellist` 注册一个图标（HammerIcon），label = “Superagent”。
- 主面板：`slots.main` 注册 `dsh-superagent` 面板。
- 配置：`plugins.row.config` 注册 Plugin Manager 配置视图（`PluginConfig`）。
- i18n：`locale.register(NS, {zh,en})`；所有文案走 `t()`。
- 面板只依赖 Host 的 slots/session/workspace/settings 能力，不拥有聊天。

### 1.2 顶部区域（Header）
- 左：面板标题。
- 右：**全局搜索框**（按文本/id/飞书 message id/GitHub URL 搜 Trigger、Thread、Ticket；结果分 Tickets/Threads/Items，高亮命中，Esc/点击外部关闭）。
- 右：**术语表按钮**（Modal：中英术语、状态图例、枚举值释义）。
- 右：**Open master session** 按钮（调用 Host `openSession(masterId)`，打开主 Session；未就绪时禁用）。
- 不放大状态数字、不显示版本/Go/CGO。

### 1.3 一级导航：6 个 Top Tabs
顺序固定：**Overview / Source / Trigger / Eval / Execution / Settings**。
- `SegmentedTabs`：`role=tablist`、左右方向键/Home/End、`aria-selected`、窄屏横向滚动。
- 路由：`Route` 类型 + 内存/ sessionStorage 历史栈（≤20）；详情页有返回按钮；点当前 tab 回到 tab 根；筛选/分页用 replace 不增加历史。
- **没有左侧边栏、没有工作区树、没有底部监控组**。

### 1.4 页面结构
- 统一 `Section`：标题 + 数量 + InfoTip + 右侧 action；卡片/列表。
- 统一 `PageHead`：返回 + 标题 + 右侧动作（打开原线程/重试/Open session）。
- 列表行：左侧状态 Dot/Mark，主标题，meta（来源/actor/时间），右侧动作；点击主区域进详情。
- 筛选：`Pill` 组；分页：`LoadMore`（limit +20）。
- 空态/错误：一句 Hint/Error；loading 文本；不造假数据。
- 状态：Dot 图例（done/ongoing/warning/error/idle），所有页面一致。
- 时间：相对时间 + title 完整时间；Markdown 安全子集；代码/路径 mono。

## 2. 六个 Tab 的功能/布局清单

### 2.1 Overview
- 区块顺序：
  1. **Pending Tickets**（需要人处理的 Ticket；每行直接给动作，如回答/重试/打开）；
  2. **Problems**（setup/source/eval/outgoing 等系统问题；可操作，带“去修复”链接）；
  3. **Last 24h**：Received / Filtered / Judged / Accepted / Dropped 计数（可点击去对应筛选）；
  4. **Executor**：running/maxWorkers、queued、idle；setup 未完成时显示等待。
- 每个区块的 Count 必须等于其列表长度；Overview 只汇总+链接，不重复详情。
- 无 pending 时明确“没有需要你处理的事”。

### 2.2 Source
- 每个 Source 一张卡：标题/启用开关/健康状态/账号事实/字段/高级折叠/保存条。
- 健康条目（Feishu/History/GitHub）各自状态；setup 问题内联。
- Filtered Triggers 折叠列表（谁/预览/原因，链接到 Thread/Ticket）。
- Dropped messages 折叠列表。
- 操作：启用/停用、Check setup、Sync now、Advanced、打开设置。
- 不在 Overview 重复 Source 详情。

### 2.3 Trigger
- 筛选 Pills：Subscribed / Evaluating / New Triggers in 24h / All Threads。
- Thread 列表行：标题（可读 marker）、来源、最后活动、订阅数/状态。
- Thread 详情页：
  - PageHead：标题、打开原线程、返回；
  - meta：来源、latest、created、poll error；
  - evaluating banner（数量/strength/deadline）；
  - Subscribed Tickets 区块（链接到 Execution）；
  - Decisions 折叠；
  - Correlation 区块；
  - Timeline（Trigger 倒序，load earlier，disposition tags：queued/evaluating/filtered/delivered/fanout/handback）；
  - Unsubscribe 确认按钮。
- 行内动作：requeue（仅无有效判断时）、evaluate again、open Ticket。

### 2.4 Eval
- 状态横幅：processing paused 时提示去 Execution 恢复。
- 筛选 Pills：Work / 各决策类型 / Last 24h only。
- Decision 行/卡：kind、reason、规则命中（matched/unmatched 与理由）、Trigger 预览、时间；展开详情。
- Settings 区块（同页）：Policy 版本、rules（增删改/复制/启停）、每 Source 的 allowed/bots/admission、accept mode、timeout、assessment model。
- Admission 表：行=scope，列=listed/unlisted × addressed/unaddressed，单元格 explicit/inferred/ignore/—；警告“任何人可 explicit”。

### 2.5 Execution
- 状态行：executor running/paused/setup；running/maxWorkers、queued；Pause/Resume 确认。
- 筛选 Pills：Open / Pending / Working / Waiting for a Trigger / Ended（+ Idle hint）。
- Ticket 列表行：stage mark、标题、来源/actor、更新时间、需要动作的按钮；load more。
- Ticket 详情页：
  - PageHead：标题、打开原线程、Retry（条件）、Open worker session；
  - 状态行：stage、Mandate strength、actor、updated；
  - ended 说明；
  - Attention banner（问题 Markdown + “到来源会话回答”链接）；
  - failed/blocked/delivery 错误 banner；
  - Mandate Details：Trigger、Decision、reason、rules、scope、basis facts、Policy version；
  - Undelivered Inputs；
  - Subscribed Threads；
  - Assignments（>1 才显示；编号/状态/报告/时间/Open session）；
  - Environment 折叠；
  - Channels 折叠；
  - 高级/次要信息默认折叠。
- 动作只在需要人处理的行/详情上出现；不把动作藏在别处。

### 2.6 Settings
- 顶部二级导航：Sources / Eval / Execution / Prompts / Deployment（URL query group）。
- 每个 block 一个 SaveBar：只在 dirty/保存中/失败时出现；基于 revision 的 CAS；历史记录（ConfigHistory）。
- **Sources**：每 Source 卡（开关/字段/账号事实/高级）；GitHub observe 开关。
- **Eval**：Policy 版本/rules/每 Source 策略/admission/accept mode/timeout/model。
- **Execution**：worker model、maxWorkers、directory、prepare、blocked commands、环境变量插入；保存条。
- **Prompts**：Edit / Full prompt / History；团队分区、内置 parts、skills；Default updated 提示；Keep/Use new；删除的 part 列表。
- **Deployment**：setup 问题 + Check again；state dir、machine limits；保存后即时生效。
- 设置项只出现在一个 group；每组一个保存条；不改默认值的不做设置项。

## 3. 共享组件/交互清单（照抄）

- `Button`（primary/ghost/outline、sm/md、icon）、`Input`、`Switch`、`SegmentedTabs`、`SegmentedControl`、`Pill`、`Tag`、`Dot/StateDot`、`Modal`（焦点/ESC/标题/描述/footer）、`DisclosureRow`、`SaveBar`、`ConfigHistory`、`Section`、`PageHead`、`StatusHead`、`LoadState`、`LoadMore`、`MarkdownLite`、`ClampText`、`When`、`KeyValues`、`LabelValue`、`ConfirmButton`、`SessionButton`、`ActionLink`、`ExternalLink`、`StageMark`。
- 键盘/ARIA：tablist 方向键、按钮 role、Modal focus trap/ESC、搜索 Esc、Enter 提交、图标按钮 aria-label、`aria-selected/controls`。
- 视觉：DSH design tokens（`--dsw-alias-*`），不自定义一套；状态色只用于状态；行 hover/pressed；禁用原因可解释。
- 文案：i18n zh/en；术语表；一句解释+InfoTip；不出现内部接口名/里程碑/Go/CGO/rev。
- 数据：所有 Count 等于列表；所有状态互斥；所有 label 说“等谁/要什么”而不是内部 code。
- 路由：6 tab + 详情页 + 搜索定位 + 返回栈；详情页刷新保持。

## 4. 我们当前 UI 与 DSH 的差距（要改）

| 当前 | DSH | 动作 |
| --- | --- | --- |
| 左侧栏：超级Agent/收件箱/项目/执行/设置/诊断 | 顶部 6 Tabs：Overview/Source/Trigger/Eval/Execution/Settings | 改顶部导航；移除左侧栏 |
| 独立桌面壳 + 聊天/工作区树 | Host 的 Session/Workspace；插件只做监督面板 | 决定 A/B；A 需插件化 |
| 首页是聊天+概览混合 | Overview 只做“待办/问题/24h/Executor” | 拆出聊天到 Host；Overview 按 DSH 四块 |
| 来源/触发/评估/执行在诊断里 | Source/Trigger/Eval/Execution 是一级 tab | 提升为一级；页面细节按 DSH |
| Ticket 详情是抽屉/执行页 | Ticket 独立详情页 + PageHead/返回 | 做详情路由 |
| 设置分组不同（模型/来源/执行/允许名单/评估/提示词/部署） | Sources/Eval/Execution/Prompts/Deployment | 重组；模型归 Execution |
| 无 search/glossary/open session | Header 三件套 | 必做 |
| 自绘控件/颜色/焦点 | DSH 控件/tokens/ARIA | 若 A 直接复用；B 需逐项复刻 |
| 工作区/浏览器/skills 是核心 UI | DSH 没有；是我们的扩展 | 放 Settings 扩展 group 或独立 Projects tab（需拍板） |

## 5. 复刻工作清单（按阶段，可勾选）

### Phase 0：决策与底座
- [ ] 决定 A（DSH 插件）还是 B（独立 App 模仿）；若 A，确认 Host 版本/依赖与打包方式。
- [ ] 定义插件入口：sidebar icon、main panel、plugin config view、locale。
- [ ] 建立 route/back stack/详情路由。
- [ ] 建立 DSH 控件与 token 层（若 A 直接用 DSH primitives；B 逐项移植）。
- [ ] 建 6 Tabs 骨架 + Header（搜索/术语/Open session）。
- [ ] 统一状态 Dot/Legend/When/Markdown/Section/PageHead。
- [ ] 移除现有左侧栏/工作区树/诊断组（或作为扩展）。

### Phase 1：Overview + Source
- [ ] Pending Tickets 行与动作。
- [ ] Problems 列表（setup/source/eval/outgoing）与修复链接。
- [ ] 24h flow counts + executor 统计。
- [ ] Source 卡/开关/health/账号事实/高级/保存条。
- [ ] Filtered/Dropped 折叠。
- [ ] 空态/Count 一致。

### Phase 2：Trigger + Eval
- [ ] Trigger 筛选 Pills + Thread 列表。
- [ ] Thread 详情（banner/tickets/decisions/correlation/timeline/load earlier/unsubscribe）。
- [ ] Eval 筛选 + Decision 行/详情。
- [ ] Policy/rules/admission/accept mode/timeout/model 设置。
- [ ] Requeue/Evaluate again/Dismiss 动作。

### Phase 3：Execution + Settings
- [ ] Execution 状态/暂停/筛选/Ticket 列表。
- [ ] Ticket 详情（actions/mandate/inputs/threads/assignments/environment/channels）。
- [ ] Settings 5 groups + SaveBar + revision + history。
- [ ] Prompt 编辑器/Full prompt/History/Default updated。
- [ ] Deployment setup/check/limits。
- [ ] Plugin Manager 配置页（若 A）。

### Phase 4：扩展（我们的产品差异）
- [ ] Projects/Workspace、浏览器、skills、统计的落点（Settings 扩展 group 或第 7 个 Projects tab）。
- [ ] 聊天/Session 在 Host（A）或独立 Session 面板（B）。
- [ ] 浏览器 MCP / Node 前置提醒。
- [ ] 与 DSH 不兼容的旧概念（工作区树/收件箱/诊断）移除或降级。

### Phase 5：验收
- [ ] 6 tabs 布局/顺序/Header 与 DSH 截图/代码一致（A 可直接对照 DSH）。
- [ ] 所有页面 Count=列表；状态互斥；空态/错误；无内部术语。
- [ ] 键盘：tab 方向键、Modal、Esc、Enter、搜索、详情返回。
- [ ] 阅读顺序/ARIA/焦点可见；窄屏 tabs 滚动。
- [ ] 保存：dirty bar/CAS/历史/离开提示。
- [ ] E2E：Overview→Source→Trigger→Eval→Execution→Ticket→Settings 全链路。

## 6. 需要拍板的两件事

1. **A 还是 B？** 要做到“DSH 完全一致的插件”，选 A；要保留独立桌面，选 B（只能模仿布局）。
2. **Projects/Browser/Skills 放哪？** 建议：DSH 六个一级 tab 保持原样；我们的扩展作为 **Settings 里的 “Projects” group** 或独立第 7 tab（如果用户坚持工作区面板）。不要塞进 Overview/Execution 破坏 DSH 结构。
