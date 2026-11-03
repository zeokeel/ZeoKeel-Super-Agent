# UX 标准复审（2026-11-04，post fix wave，只读）

- 范围：`/tmp/r2-*.png`（pre-fix）、`/tmp/r3-*.png`（pre-fix）、`/tmp/r4-*.png`（post-fix）+ `internal/ui`/`internal/view` 代码与测试。
- 方法：截图 + 代码阅读，**未运行 GUI**（无显示器）。r2/r3 是修复波前状态，r4 是当前状态；两者都保留为证据。
- 标准：macOS HIG、Windows Fluent、Material 3、WCAG 2.2、Nielsen 十律、Claude/ChatGPT/Slack/Linear/GitHub Desktop/DSH。
- 限制：无法实测屏幕阅读器、焦点环、高 DPI 重排；相关结论来自代码缺失/样式 token，而不是实测。

## 结论

产品骨架（超级Agent + 工作区树 + 监控 + 设置）方向正确，诚实状态（禁用=未监听、无假计数）是强项。
但作为面向非技术操作员的桌面产品，**聊天、审批、IM 接入三件事仍停留在“工程可用”，不是“标准桌面产品可用”**：
SuperAgent 没有流式/多行/消息操作/历史；审批没有收件箱与注意力管理；IM 接入没有引导式授权状态机。
以下 P0/P1 应在 1.0 前处理，P2 可排期。

## 发现（Severity：P0 阻塞发布 / P1 下一轮必修 / P2 排期）

| ID | Sev | Area | 证据（file:line / screenshot） | 标准 | 为什么非标准 | 用户影响 | 建议 |
| --- | --- | --- | --- | --- | --- | --- | --- |
| U-01 | **P0** | Chat / SuperAgent | 输入框单行 `ui/input.go:15-27`；内联回答截断单行 `Clip(...,140).SingleLine()` `ui/chat.go:27,44,71`；无流式（`runtime/chat.go:63-90` 用 `Generate`，无 token 流）；气泡只渲染纯文本 `ui/chat.go:130-150`；无复制/重生成/停止；历史仅 `Tail(8)` `ui/chat.go:88`、内存 20 条 `runtime/chat.go:25-49`；截图 r4-overview-chat、r2-agent-answer | HIG 反馈/直接操控；Claude/ChatGPT 流式+多行+消息操作；Nielsen 可见性 & 控制 | 中央承诺“超级Agent 聊天”缺少桌面聊天的最低标准交互；回答在输入框上方被截成一行，长答案不可展开 | 用户认为回答被截断/不完整，无法复制/追问/回滚；长对话只能看最近 8 条，找不到历史 | 支持多行 composer（Shift+Enter 换行、Enter 发送）、流式/typing、Markdown/代码块、消息操作（复制/重试/停止）、可展开的内联回答与“查看全部”历史页 |
| U-02 | **P0** | Approvals / 注意力 | 审批与“需要我处理”混在总览 `ui/overview.go:51-140`；导航无审批页 `view/vm.go:467-484`；无快捷键/批量/撤销/过期/always-allow；无 OS 通知；tray 仅打开/退出 `ui/tray.go:16-34`；截图 r4-overview-chat | HIG 通知；Slack/Linear 收件箱与未读；Nielsen 可见性/用户控制 | 所有 IM 入站先审批（fix wave 核心策略），却没有专门收件箱：批准项只能去总览找；错批/误拒无撤销；“待处理 3”与“待审批 N”语义叠加 | 操作员错过待批（尤其窗口隐藏时）；批量/键盘操作不可能；误操作不可恢复 | 增加「待审批」专用收件箱（可筛选来源/时间/会话）、键盘批准/拒绝、批量、撤销 toast、`总是允许该发送者`、系统通知/托盘 badge、过期提示 |
| U-03 | **P1** | IM 接入 / 引导 | 渠道卡只有「检查通道/打开授权页」`ui/channels.go:205-235`；无 QR/device-code/waiting/success/failure 状态机；DWS 需外部终端 `dws auth login`，授权 URL 以 10px 原文显示 `channels.go:232-235`；无 CLI 版本检测；截图 r3-source-channels-auth | HIG 引导式设置；Nielsen 帮助/错误预防；Fluent 连接流程 | 非技术操作员无法完成飞书/DWS 授权：没有“扫码/设备码/等待中/成功”界面，只给命令和原始 URL；错误文案暴露 CLI 细节 | 首配 IM 失败或半配置；用户不知道下一步该在终端做什么 | 做授权向导：二维码/设备码、等待与超时、成功/失败重试、安装检测（`dws --version`）、可复制命令/打开终端按钮、明确“未启用=不监听” |
| U-04 | **P1** | Onboarding 首跑 | 4 步向导 `ui/onboarding.go:39-140`；无“测试连接”，无整段跳过；workspace 只能手输路径（无原生选择器）；source 步骤只存 `onboard.*` 不接 IM/GitHub；步骤文案含“路由”等术语；progress 只有“3 / 4”文本；`runtime/onboarding.go:71-100` | HIG/M3 onboarding；Nielsen 帮助/一致性 | 首跑没有“验证模型可用”，也没有最短路径；用户填完仍不知道 IM/GitHub 是否接通；AI 产品标准是 test-connection + 明确下一步 | 前 5 分钟失败在模型 401 或 IM 未接线；用户以为已配置 | 每步内联“测试连接”；允许跳过非必要项并显示“稍后配置”；工作区用原生 picker；source 步骤写清“只记录，稍后启用”或真正接线；去掉行话 |
| U-05 | **P1** | 可访问性 / 对比度 | `TextFaint` 浅色 #98A2B3 on #FFFFFF = **2.58:1**、深色 #6B7891 on #131A2A ≈ **3.9:1**，用于 10-12px 时间/说明 `ui/tokens.go:109,139-140`、`ui/chat.go:140`；无 focus-visible 样式；`Smooth` 动画无条件 `ui/stream.go:59`；字号仅 0.9/1.0/1.1 三档 `runtime/pages.go:537-539`；中文单语 | WCAG 2.2 1.4.3（4.5:1）、2.4.7 焦点可见、2.3.3 减少动效、1.4.4 文本缩放、4.1.2 | 大量辅助文字低于 AA；键盘焦点不可见（代码无自定义 token）；动画不尊重 reduce-motion；不能跟随 OS 文本大小 | 低视力/键盘用户读不到时间、无法定位焦点；系统放大字号无效 | 提升 faint 到 ≥4.5:1（或只用于装饰）；加 focus ring token；尊重 `prefers-reduced-motion`；跟随 OS 字体缩放并提供“默认/大/最大”；i18n 至少预留 |
| U-06 | **P1** | Trust / 文案 | “动作未接线：运行时未提供 AttentionAnswerer / TicketStopper / TicketRetrier” `ui/overview.go:133-137`、`actions.go:104-111`；“来自 mygo.json / 配置 rev” `ui/settings.go:27-28`；“本组表单随后续里程碑出现” `runtime/pages.go` deploy note；名词表含 Agent/Main Session/Executor Session/Policy/Eval/Thread 等内部术语 `ui/help.go`；状态栏 debug `ui/chrome.go:141-146`；截图 r4-overview-chat、r2-help | Nielsen 匹配真实世界/一致性；HIG 文案；DSH 用户语言 | 把 Go 接口名、里程碑、配置版本、架构术语直接给用户；禁用按钮旁写实现细节，像开发者调试页 | 用户不理解、不信任；不知道“动作未接线”是产品未完成还是自己操作错 | 面向用户的空态/禁用原因改为“此操作暂不可用（预计下版）”；移除接口名/里程碑/rev；技术事实收进 About/诊断；名词表默认展开用户术语，架构术语折叠 |
| U-07 | **P1** | 键盘 / 平台 | 全局快捷键只有 ⌘K/⌘/ `ui/app.go:389-394`；命令面板只搜内容、无 actions/recent/fuzzy `ui/search.go:147-160`；无菜单栏/右键菜单；侧栏固定 232px 不可调/折叠/记忆 `ui/chrome.go:65`；窗口尺寸仅启动参数、几何不持久化 `cmd/zeokeel/main.go:313-314`、`ui/app.go:238-263`；tray 无 badge/通知；无 About/自动更新 | HIG/Fluent 菜单与键盘；WCAG 2.1.1 键盘可达；Slack/Linear 命令面板 | 桌面产品缺少标准菜单/上下文菜单/快捷键体系；窗口与侧栏状态不记忆；隐藏窗口后没有任何外部提醒 | 键盘用户无法完成审批/新建/切换；每次启动恢复默认布局；错过后台事件 | 增加菜单栏（File/Edit/View）+ 上下文菜单 + 快捷键（⌘N/⌘Enter/⌘1-7/⌘W/Esc）；命令面板加 actions/recent；持久化 window/sidebar；tray badge+通知；About/更新检查 |
| U-08 | **P1** | 视觉 / 图标 | 执行/触发行左侧出现 `{?}`/`1'` 占位字形（r4-execution-filter.png、r2-trigger.png）；图标来源 `ui/widgets.go:49-66`，行图标 key 硬编码/回退有限 | HIG/M3 图标一致性；可见性 | 关键列表首列出现乱码式字形，像坏数据/未完成产品 | 降低信任；用户无法区分来源类型 | 修图标渲染/字体回退；统一 icon set 与尺寸；加视觉回归截图（含 SVG 渲染） |
| U-09 | **P2** | Settings | 无搜索/重置默认/高级折叠；切换分组或离开页面无未保存提示；secret 只能“留空保留”、无显示/替换按钮 `ui/settings.go:443-470`；校验仅在保存后 error；部署组混入日志路径/退出码/health/OS 等 debug 事实 `runtime/pages.go:830-850`；截图 r2-settings-deploy | HIG 偏好设置；Nielsen 用户控制/错误预防 | 设置页像运维事实页；无 reset/搜索；未保存更改靠用户自觉；secret 管理不标准 | 改错难恢复；不知道哪些必填；离开页面丢改动 | 加搜索、Reset、Basic/Advanced 分组、未保存离开确认、secret reveal/replace；把 debug facts 移到 About/诊断 |
| U-10 | **P2** | 工作区/会话 | ConversationVM 无 preview/unread/pin/search/sort/context menu `internal/view/workspace.go:18-36`；每工作区最多 50 条 `runtime/pages.go:1087-1088`；列表行仅标题/来源/阶段 `ui/workspace.go:416-440`；picker 无最近目录；rename/delete 只在工作区页内；无多窗口 | Slack/Linear 会话列表；GitHub Desktop 仓库切换；HIG 窗口 | 会话列表不可扫读/不可组织；长历史被硬截断且无分页提示；管理入口不直观 | 用户找不到旧会话；不能置顶/搜索；50 条后“消失” | 列表加预览/未读/置顶/搜索/排序/右键菜单；分页或“显示更多”；picker 最近目录；管理入口在行 hover/右键 |
| U-11 | **P2** | 空态/加载/反馈 | 页面加载态是纯文本“等待运行时发布…”（如 `ui/execution.go:28`、`ui/eval.go:29`、`ui/overview.go:19`）；无 skeleton/重试；异步成功无 toast；错误只内联红字；无全局 offline/连接状态条 | M3 进度/反馈；HIG 反馈；Nielsen 系统状态 | 慢投影/后端失败时页面像空白，用户不知道在加载还是坏了；成功操作没有确认 | 用户重复点击/误判故障 | skeleton/进度条 + 超时重试；toast 成功/失败；全局连接状态条 |
| U-12 | **P2** | 信息层级/密度 | 触发/评估页是工程师看板：Policy v7、游标、Eval/Thread、上下文/评估原因码（r2-trigger.png、r2-help.png）；聊天/执行页大量空白；10-12px 辅助文字密集；状态栏常驻版本/rev/Go/SQLite 信息 `ui/chrome.go:141-146` | HIG 信息层级；M3 type scale；Nielsen 美学/极简 | 面向“操作员”的监控页混合了研发调试信息；核心页留白与低密度不匹配；技术信息占据高价值状态栏 | 认知负担高；非技术用户不知道哪些需要行动 | 监控页默认“人话摘要 + 展开技术详情”；把 rev/Go/CGO/SQLite 收进 About；统一 12/14/16/20 字号与密度 |
| U-13 | **P2** | 审批计数语义 | 顶栏“3 待处理”、侧栏超级Agent 3、需要我处理 3、来源页“待审批 N”并存；`view/vm.go` PersonCount 与 `runtime/pages.go:101-111` 各自计数 | Nielsen 一致性；HIG badge 语义 | 同一数字在不同位置的统计口径可能不同（审批 vs 待办 vs 来源待批），没有说明 | 用户不知道“3”到底指什么，可能漏掉审批 | 统一为“待你决定 N（含审批 X）”并给 tooltip/分解；badge 只在一处主入口 |
| U-14 | **P2** | 屏幕阅读器/标签 | 图标按钮无文本标签：工作区 heading 的“＋”行无 Label `ui/workspace.go:220-227`；部分 clickable Row 无 Label；无焦点顺序测试 | WCAG 2.2 4.1.2 名称角色值、2.4.3 焦点顺序 | 键盘/读屏用户无法知道按钮用途；焦点顺序未验证 | 无障碍用户无法独立完成新建/审批 | 所有可点击元素加 Label/aria；补键盘走查与读屏 smoke test |

## Top 10 优先 UX 缺口

1. **U-01 聊天体验**：多行+流式+Markdown+复制/重试/停止+可展开内联回答+历史页。
2. **U-02 审批收件箱**：专用页、键盘/批量/撤销/通知、`always allow`、过期与 badge 单一真源。
3. **U-05 可访问性**：faint 对比度 ≥4.5、focus ring、reduce-motion、跟随 OS 字号、标签。
4. **U-03 IM 授权向导**：QR/设备码/等待/成功/失败、安装与版本检测、可复制命令/打开终端。
5. **U-04 首跑**：测试连接、跳过/稍后配置、原生 picker、source 步骤真实接线或明说。
6. **U-06 文案/信任**：移除接口名/里程碑/rev，技术事实移 About，用户语言重写。
7. **U-07 键盘与平台**：菜单栏/上下文菜单/快捷键/窗口与侧栏持久化/tray 通知/About。
8. **U-09 Settings**：搜索、Reset、Basic/Advanced、未保存 guard、secret reveal/replace。
9. **U-10 会话管理**：预览/未读/置顶/搜索/排序/右键、分页、picker 最近目录。
10. **U-08/U-11/U-12 视觉与反馈**：修 `{?}` 图标、skeleton/loading、toast、层级/密度、状态栏去 debug。

## 已经做得好的（保留）

- **诚实状态**：禁用渠道显示“未启用”，不假装在线；无假计数；空态直接说“没有需要你处理的事”。
- **删除确认解释边界**：明确“会移除工作区与会话记录，磁盘目录不删”；比假删/静默删好。
- **原生目录选择器**：后台 goroutine + 测试 seam，不卡帧；重复 root/绝对路径服务端校验。
- **审批行信息**：来源、谁、原话、要做、产出、敏感项、时间都给到；批准/拒绝就地完成。
- **聊天内联反馈**（fix wave）：已回答不标红、输入框上方固定显示最新一条——方向正确，只差展开/历史/操作。
- **命令面板 + 名词表**：⌘K/⌘/ 可发现，分组结果和 Esc 关闭符合桌面习惯；名词表可折叠技术术语后即达标。
- **主题/计数一致性**：深浅色跟随系统；多处计数来自同一快照，未发现互相矛盾的假数据。
