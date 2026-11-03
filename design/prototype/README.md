# ZeoKeel 客户端原型（mygo 真实绘制）

这不是手绘线框图，也不是 HTML/CSS 截图：**每一个界面都是 Go + mygo `ui` 包的 view 函数**，
由 `ui.Render` 在内存里真实布局、排版、绘制（中文字体走系统字体），导出为 1440×900 @2x PNG。
同一份 view 可以直接 `-window` 打开成真实的 mygo 原生窗口（已在 Xvfb 下验证）。

- 品牌资产：`/data/ZeoKeel-Super-Agent/logo/logo.svg` 与 `logo-dark.svg`（已复制到 `assets/` 并转成 PNG 供 `ui.Image` 使用；`ui.Image` 也直接支持这组 SVG 渐变，见 `main.go`）。
- 设计依据：[`design/zeokeel-go-mygo-design.md`](../zeokeel-go-mygo-design.md) §6（页面结构、三条铁律、tokens）与 `research/reference-analysis.md` 的面板三问。
- 数据：全部为填充用的样例数据（Ticket #42/#44/#45、PR #128、真实感的工具调用/diff/token/费用、GitHub/Feishu/Schedule 三个 Source），不是占位 lorem。

## 界面清单

> 截图产物不入库。生成：`cd design/prototype && go run . -out shots`（会写出 PNG 与 `index.html` 画廊）。


| # | 文件 | 内容 |
| --- | --- | --- |
| 00 | `00-flow` | 一次 Trigger 的旅程：事件 → 领域判定 → Agent 执行 → 需要人 → 交付回写；关键不变量、授权四层、投递三段式、崩溃恢复、里程碑切片 |
| 01 | `01-overview` | 总览：三问（需要我 3 件 / 健康 / 24h 活动）+ 运行时与执行器 + 最近交付物 |
| 02 | `02-execution` | Execution：Ticket 详情，流式 transcript + 工具卡片 + diff + 计划 + 右侧 inspector + 固定「需要你」条 + composer |
| 03 | `03-attention` | Attention：`ask_user_question` → 原生对话框（结构化选项 + 可选补充） |
| 04 | `04-approval` | 授权：高风险工具调用的 gate（允许一次 / 本 Ticket 内始终允许 / 拒绝；默认 observe） |
| 05 | `05-artifacts` | 交付物：PR、report.md、截图、coverage 卡片 + Quick Look 式预览与操作 |
| 06 | `06-trigger` | Trigger：线程列表、Trigger/Decision 时间线、订阅与 Policy |
| 07 | `07-eval` | Eval：每个 Decision 的 reason 与命中规则、golden 用例、admission |
| 08 | `08-source` | Source：GitHub / Feishu / Schedule 的账号事实、游标、过滤与丢弃 |
| 09 | `09-settings` | 设置：Provider/模型/预算、MCP、Skills 与自动化、单一保存条 |
| 10 | `10-tray` | 托盘与通知：关窗=隐藏、需要人才通知、Dock 徽标、运行时遥控器 |
| 11 | `11-workspaces` | 多工作区切换器：全部工作区聚合 + 按工作区的「需要我 / 执行中」，⌘1-9 切换 |
| 12 | `12-workspace-admin` | 工作区管理：根与身份（稳定 WorkspaceID）、绑定的 Source、Policy/预算/并发、跨工作区硬规则 |
| 13 | `13-multiwindow` | ⌘N 多窗口并排（**M5 规划，非首版**）；每窗口钉住一个工作区、共享 runtime/托盘 |
| 14 | `14-new-task` | `⌘N` 新建任务 composer：任务描述、工作区选择（含「自动判断」与 brief purpose）、附件缩略、`⌘↵ 派发`；右下角说明走 direct-dispatch 还是 Router；左下角标出派发后的即时状态（不弹窗/不跳页/输入清空/顶部 queued） |
| 15 | `15-task-inbox` | Execution · 任务列表：Open/Pending/Working/Waiting/Ended 过滤；来源（GitHub / 钉钉 / 飞书 / **local**）、`#id 标题`、工作区、阶段、最近活动、产物徽标；按最近活动排序；空态 = 一句话 + 输入框 |

`go run . -out shots` 还会生成可点的 `index.html` 总览页与 `contact-sheet.png` 缩略图总览，两者都不入库。

## 重新生成

```sh
export PATH=$PATH:/usr/local/go/bin
cd design/prototype
CGO_ENABLED=0 go run . -out shots          # 渲染全部 PNG（无头、无 DISPLAY）
CGO_ENABLED=0 go run . -window -screen 02-execution   # 打开真实原生窗口（需 X11/Wayland）
```

依赖：`github.com/egoist/mygo v0.7.4`（**已钉住，不再 replace 到 `/tmp/mygo`**），Go 1.27.3，零 cgo。
Wave 1（2026-11-03）验证：`CGO_ENABLED=0 GOPROXY=off go build ./... && go vet ./...` 通过，
`go run . -out /tmp/shots` 在无显示器下渲染 16 张 PNG。

> **本目录是视觉参考，不是产品代码。** 不要从这里把 view/tokens 回迁到 `internal/ui`：
> 两套 tokens 已经漂移（Radius 12 vs 8、重复 hex），产品 UI 有自己的 `internal/ui/tokens.go`。
> 本目录不在根 module 的 `go test ./...` 范围内，也不参与 CI gate。

## 设计 tokens（`theme.go`）

- 背景 `#F5F6F8` / 表面 `#FFFFFF` / 台面 `#FAFBFC` / 边框 `#E4E7EC`
- 文本 `#101828` / 次要 `#667085` / 弱 `#98A2B3`
- 主色 `#2F5BFF`（取自 logo 蓝），辅色青 `#0EA5C6`、紫 `#6D4AFF`
- 语义色：成功 `#12805C`、警告 `#B54708`、危险 `#D92D20`
- 圆角 12（卡片）/ 8（控件）/ 6（chip），间距 4 的倍数；正文 13、次要 11–12、标题 22–26
- 深色主题：`ui.DarkTheme()` 派生同一套 token（下一步补深色版截图）

## mygo 控件映射（原型里真正用到的）

`ui.Column/Row/Box` flex 布局、`ui.Text`（字号/字重/颜色/`monospace`/`MaxLines`）、
`ui.Icon`（内联 SVG，lucide 风格）、`ui.Image`（logo PNG）、`ui.Scroll`、`ui.Themed`、
`ui.Divider`、`ui.Spacer`、`ui.RGBA/Hex`、`Radius/Border/Shadow/Gradient`。
真实 app 里再换成 `ui.Sidebar`、`ui.Tabs`、`ui.Split`、`ui.SearchField`、`ui.Modal`、
`ui.Table/List`、`ui.Markdown`、`ui/editor` + `DiffView`、`ui.Badge`、`ui.Toast`（见设计文档 §6.4）。

## 多工作区（设计说明）

结论：**单进程、单 SQLite，但 `workspace` 从第一天就是一等记录**（ADR-14）；切换工作区只是 UI 作用域变化，
runtime 不重启，正在跑的 Ticket 照常继续。

- **工作区身份**：`WorkspaceID` 在**首次创建时生成并持久化**（`ws_` + 8 字节 hex），**稳定不变**；`remote`
  只用于「同一仓库不许重复添加」的唯一性校验，不参与 ID 计算。移动或重命名仓库目录不会改变 WorkspaceID，
  因此历史（Trigger/Ticket/Decision/会话事件）不会失配。Workspace 是本地配置实体，不走「内容派生 ID」纪律。
- **工作区记录**：`id`、`title`、`roots`（本地目录 / 分支）、绑定的 `sources`、`policy`、`prompts`、`budget`、
  `executor slots`、`secrets key`、`created_at`。
- **领域 ID**：**第一段是 `workspaceID`**（`ws_xxx:issue-42`），否则仓库 A 的 `#12` 与仓库 B 的 `#12` 会撞成同一个
  Thread；Trigger/Ticket 等**业务实体**仍按内容派生 ID（禁 UUID），幂等与重放不受影响。
- **构造期边界**：Runtime 持有**每工作区一个 Engine**，共享单写者 `Store` 与 `Effects` 队列；领域 API **不接收
  `workspaceID` 参数**，由 Engine 构造时绑定。SQLite 单文件，全表带 `workspace_id`，查询按 workspace 分区。
- **运行时**：每工作区隔离 scheduler/executor 槽位与预算；工具白名单、工作目录、钥匙串条目按工作区解析；
  跨工作区的 Mandate 永不复用；评论 / PR 只回它来源的工作区。
- **UI**：顶栏 workspace switcher（图 11）＋ `全部工作区` 聚合概览；切走不暂停；`⌘N` 新窗口钉住一个工作区；
  「需要我」按工作区分组但可聚合排序；通知与托盘徽标带工作区名。
- **管理**：图 12 的 roots / Sources / Policy / 预算 / 删除；添加流程 = 选目录或 clone → 扫描 → 绑 Source →
  默认 Policy → 首次索引。
- **实施顺序（已排期）**：v1 直接上多工作区；M0 写 schema 与领域 ID；**M2 做顶栏切换器 + 工作区管理页**；
  **M5 做 ⌘N 多窗口并排**与跨工作区搜索（图 13 明确标了 M5，不是首版能力）。仍不做多租户/多副本。

## 已知取舍 / 下一步

- 界面是静态渲染（无交互状态），重点在信息架构、密度与视觉；交互逻辑由后续真实 app 的 `ui.NewTester` 测试覆盖。
- 顶栏是自绘；正式版用系统窗口装饰（macOS 红绿灯 / Linux GTK 标题栏），只保留工具条。
- 滚动条、字体子像素、窗口失焦态等平台细节未逐一模拟。
- 深色主题、1280×800 紧凑尺寸、⌘K 命令面板、Source 认证流程对话框尚未出图。
- 多工作区还差：添加/移除工作区的确认流、跨工作区搜索结果页；`⌘N` 多窗口已出示意图但标注为 M5。
