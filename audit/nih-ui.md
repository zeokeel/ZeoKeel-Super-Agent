# NIH 审计 · 原生 UI 切片（internal/ui + internal/view + design/prototype）

> 审计对象：`internal/ui/**`、`internal/view/**`、`design/prototype/**`
> UI 框架：`github.com/egoist/mygo v0.7.4`（`ui` 包；`mygo` 根包负责 Window/Tray/App）
> 工具链：Go 1.27.3，`CGO_ENABLED=0`
> 约定：mygo 源码路径前缀 `MC = /root/go/pkg/mod/github.com/egoist/mygo@v0.7.4/`，下文写作 `ui/list.go:54`；
> 仓库内路径相对 `/data/ZeoKeel-Super-Agent`，行号以当前工作树为准（`git status` 干净，未做任何修改）。
> 本审计为只读：只新建本文件，未改源码、未 gofmt、未提交、未运行构建。

---

## 0. 一页结论

**没有"自造 UI 框架"级别的 NIH。** 视图层完全建立在 mygo 之上：`ui.Themed / ui.Modal / ui.SearchField /
ui.TextInput / ui.List / ui.Scroll / ui.Icon / ui.Image` 都在用，markdown / diff / editor / chart 一个都没自己写
（也没写"半套"）。快照架构（atomic.Pointer 分片 + 每帧一次 Load）与设计 §6.1 三条铁律执行得比预期严格。

真正的 NIH 是**局部**的，分四类：

1. **把 mygo 已有控件手工拼回去了 4 处**（P1×3 + P2×1）：
   - 消息页用 `Scroll + Column` 手搓"无限日志"，全部行每帧重建（`stream.go:17-36`）→ 应用 `ui.List` + `ListState.FollowEnd`
   - 评估页手搓表头 + `Scroll + Column` 非虚拟化列表（`eval.go:97-127`）→ 应用 `ui.Table`
   - 命令面板手搓 ↑↓ 选择状态机（`search.go:98-143`）→ 应用 `ui.List` + `ListState.Header`（分组表头"不可选"语义完全对得上）
   - 两个 `ui.List` **没设 `ListState.Key`**（`app.go:352-353`），而投影按"最近活动/创建时间倒序"重排 → 这是**真 bug**，不是风格问题
2. **该用 stdlib / 已在依赖图里的库，却手写了 2 处**（P2）：`groupedNumber`（`widgets.go:98-116`）重复 `humanize.Comma`；
   `trimSpace`（`input.go:48-57`）重复 `strings.TrimSpace`。
3. **该派生自 mygo Theme，却平行抄了一套 token**（P2）：`tokens.go:33-83` 两个函数各自硬编码同一批 hex，
   `theme()` 永远 `LightTheme()` → 暗色主题（mygo 自带、跟随系统）实际失效；两套 `Radius`（8 / 12）已经不一致。
4. **内部重复与死代码**（P2）：保存条协议、表单字段渲染、chip 家族、状态→颜色映射、页面骨架各重复 1–3 份；
   中文相对时间在 `internal/ui`、`internal/runtime`、`internal/brief` **三份实现、三种措辞**；
   `view/demo*.go` 572 行只被测试引用；9/30 个图标未被使用；`design/prototype` 是 3015 行**陈旧分叉**且不被 CI 编译。

**用户可见的正确性问题 3 个**（都在 P1，见 §3）：
- 列表选择以 index 存在 app 里，新任务置顶后"已展开的行"指向另一条任务（`ListState.Key` 未设）；
- "待处理"计数三处口径不一致（顶栏 / 侧边栏 / 总览标题），违反设计 §6.5"诚实状态 = 计数等于它链接的列表长度"；
- 托盘菜单"暂停运行时"点了没有任何反应，且注释还停在 M0。

判定计数：**SHOULD-USE-MYGO/LIB/STDLIB 10 项**（P1 4、P2 6）、**JUSTIFIED 自建 11 类**、**LOW-PRIORITY 内部重复 6 项**、
**DEAD 6 项**。无 P0。合计可删/可省约 **700–900 行**（含测试专属 demo 572 行），净新增约 150 行迁移胶水。

---

## 1. 范围与覆盖

### 1.1 代码量

| 目录 | 生产代码 | 测试 | 文件 |
| --- | --- | --- | --- |
| `internal/ui/` | **3 937** 行（26 文件） | 1 349 行（9 文件） | app / chrome / widgets / tokens / icons / overview / execution / source / trigger / eval / settings / onboarding / input / prompt / stream / search / tray / schedule / goal / deliverables |
| `internal/view/` | **2 415** 行（14 文件，含 demo 572） | 711 行 | vm / pages / reason / demo / demo_pages / prompt / search / artifacts / onboarding / schedule / goal |
| `design/prototype/` | **3 015** 行（16 文件，独立 module） | 0 | main / chrome / theme / icons / data / 12 个 screens_* |
| `internal/ui/assets/` | 1 个 PNG（12 901 B） | — | logo.png |
| `design/prototype/assets/` | 4 个文件（33 874 B） | — | logo(.svg/.png)、logo-dark(.svg/.png) |

### 1.2 已通读的文件（全部）

- `internal/ui`：全部 26 个生产文件**逐行**读完（app.go 394、overview.go 416、prompt.go 353、settings.go 327、
  search.go 280、trigger.go 239、source.go 230、execution.go 216、onboarding.go 202、stream.go 198、eval.go 186、
  chrome.go 163、goal.go 161、schedule.go 156、widgets.go 116、deliverables.go 89、tokens.go 83、input.go 57、
  icons.go 37、tray.go 34）；测试文件按需抽读（`pages_test.go`、`search_test.go`、`view_test.go`、`drain_test.go`、
  `deliverables_test.go`、`onboarding_test.go`）。
- `internal/view`：全部 14 个文件；`demo_pages.go`（525）与 `pages.go`（498）通读，`vm.go` 通读。
- `design/prototype`：`main.go`、`README.md`、`go.mod`、`chrome.go`、`theme.go`、`icons.go` 通读；
  12 个 `screens_*.go` 抽读（结构均为"手写 chrome + 样例数据"）。
- mygo 源码（`MC/ui`）：`theme.go`、`tabs.go`、`list.go`（ListState 全字段）、`table.go`（TableColumn / TableLayout）、
  `api_gen.go`（Badge / Divider / Avatar / Progress / Meter / SearchField / Segmented / Select / Spinner / Modal /
  Table / Tabs / Toolbar / Tree / Themed / SetTheme）、`number.go`、`svg.go`、`toast.go`、`locale.go`、
  `runtime.go`（defaultTheme / 跟随系统外观）、`list_builder.go`、`ui/chart/*`、`ui/markdown/*`、`ui/editor/diffview.go`。
- 设计文档：§0、§1.3、§2（ADR）、§6.1–6.10、§9.3、§11；`README.md`、`CHANGELOG.md`、`design/M0-tasks.md` 相关段落。

### 1.3 未做（限制）

- 未运行构建/测试（只读审计；且 `make check` 会写 `bin/`、缓存）。结论中的"可编译/行为"均为源码阅读推断，已标注。
- 未逐帧截图对比 UI 视觉差异；布局迁移风险按代码内的固定宽度（26/120/96/170 等）评估。
- `design/prototype` 无法构建：`go.mod:14` 的 `replace` 指向仓库外 `/tmp/mygo`，且该目录内容与 v0.7.4 **不同**
  （`diff MC/ui/theme.go /tmp/mygo/ui/theme.go` 有差异）——见 F-25。

---

## 2. 汇总表

| # | 区域 | 现状实现 | 行数 | 判定 | 建议替换（mygo / lib 符号） | 工作量 | 风险 | 优先级 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| F-01 | 消息页（工作台流） | `ui.Scroll` + `Column` 循环画全部 Thread/Line，无虚拟化、无"跟随底部" | ~20（`stream.go:17-36`） | **SHOULD-USE-MYGO** | `ui.List` + `ListState{Key, FollowEnd, FollowAnimation}`（`ui/list.go:42-65`） | M | 中：行高不一、缩进版式要保留 | **P1** |
| F-02 | 评估页列表 | 手搓表头 4 列 + `Scroll` + `Column` 画全部 Decision（不虚拟化） | ~30（`eval.go:97-127`） | **SHOULD-USE-MYGO** | `ui.Table(c, s, []TableColumn{…}, n, cell)`（`ui/table.go:10`、`ui/api_gen.go:2169`） | M | 中：Grow(2)/Grow(3) 比例改固定宽；Tester 断言按文本仍成立 | **P1** |
| F-03 | 执行/触发两列表 | `Selected *int` 用**行索引**，未设 `ListState.Key`；投影按倒序重排 | ~6（`app.go:352-353`、`execution.go:62,84-88`、`trigger.go:56-86`） | **SHOULD-USE-MYGO** | `ListState.Key = func(i) any { return rows[i].ID }`（`ui/list.go:42-47`、`ui/list.go:548-554` 会自动把选择跟到新行号） | S | 低：纯新增字段；需把 `ticketSel`/`threadSel` 语义改"由列表回写" | **P1** |
| F-04 | "待处理"计数 | 三处各自推导：顶栏 `len(NeedsPerson)`、总览标题 `len(NeedsPerson)`、侧边栏 `len(NeedsPerson)+len(Approvals)` | ~4（`chrome.go:48-50`、`overview.go:24`、`vm.go:344-359`） | **LOW-PRIORITY（诚实状态缺陷）** | 统一由 `view.Nav()`/VM 提供一个 `PersonCount()`；或标题直接用 `view.Nav(nil)` 的 Count | S | 低 | **P1** |
| F-05 | 托盘菜单 | "暂停运行时" 空实现，注释停在 M0 | 3（`tray.go:23-25`） | **DEAD** | 删除该项，或接 runtime 暂停能力（当前 `internal/runtime` 无 Pause） | S | 低 | **P1** |
| F-06 | 命令面板列表 | 手写 `searchSel` + `c.Shortcut(KeyUp/KeyDown)` + `Scroll`+`Column`；分组标题不可选靠自己计数 | ~46（`search.go:98-143`） | **SHOULD-USE-MYGO** | `ui.List` + `ListState{Header: func(i) bool, Key, Selected, Label}`（`ui/list.go:89-97` 头注释："Headers are not chosen"；`ui/list.go:66-70` 键盘选择） | M | 中：分组拍平成行；Enter 走 `Submitted()` | P2 |
| F-07 | Logo | `//go:embed logo.png` + `sync.Once` 解码 + 失败回落蓝方块 | ~19（`chrome.go:110-128`、`app.go:23-24`） | **SHOULD-USE-MYGO** | `ui.MustParseSVG(logo/logo.svg)` + `ui.Image`（`ui/svg.go:11-30`：SVG 即 ImageSource，`ParseSVG` 一次、矢量任意缩放）；可删 `logoOnce/logoBmp` 与 PNG 资产 | S | 低：SVG 不支持 filter/text，logo 是渐变+路径（README 已确认可渲染） | P2 |
| F-08 | 主题 / tokens | `theme()` 抄 LightTheme 改色；`tokens()` 再抄同一批 hex 成独立 struct；`Radius` 8 vs 12 已不一致；`Themed(c, a.theme)` 永远浅色 | ~50（`tokens.go:33-83`、`app.go:284`） | **SHOULD-USE-MYGO（部分）** | token 从 `c.Theme()` / `ui.DarkTheme()` 派生：`Theme` 已含 Background/Surface/Border/Text/Accent/Warning/Success/Radius/Spacing（`ui/theme.go:8-64`），`Context.Theme()` 返回"跟随系统外观"的主题（`ui/api_gen.go:518`、`ui/runtime.go:507-526`）；设计 §6.5 亦要求 `FromTheme(t)` | M | 中：暗色下所有软色（`AccentSoft` 等 CLDR 外的 brand 色）需给出 dark 版本 | P2 |
| F-09 | onboarding 下拉 | `formField` 里手搓 segmented（Row+OnClick 逐项） | ~14（`onboarding.go:186-198`） | **SHOULD-USE-MYGO** | `ui.Segmented(c, &sel, labels...)` / `ui.Select(c, &s, options)`（`ui/api_gen.go:1821,1849`） | S | 低 | P2 |
| F-10 | 分隔线 | 27 处 `mui.Box(c).Height(1).Background(...)`，另有 1 个竖线 `dividerV` | 27 处（如 `eval.go:110`、`overview.go:62`、`trigger.go:76`） | **SHOULD-USE-MYGO** | `ui.Divider(c)`（`ui/api_gen.go:1090`；prototype 自己在用） | S | 极低 | P2 |
| F-11 | 运行时占用条 | 手写 `Row` 背景 + `Box` 宽度 = `w*286`（写死像素，与 320 卡片宽强耦合） | 8（`overview.go:245-251`） | **SHOULD-USE-MYGO** | `ui.Progress(c, running/max)`（`ui/api_gen.go:1604`）或 `ui.Meter(…, &ui.MeterLevels{})`（`ui/api_gen.go:1409`） | S | 低（Progress 有主题色，不再需要手算像素） | P2 |
| F-12 | 头像 | `Row().Size(24,24).Radius(12).Children(Text)` | 5（`stream.go:194-198`） | LOW-PRIORITY | `ui.Avatar(c, name, nil)`（`ui/api_gen.go:715`）；但要求"你"用 accent 色、who 取首字，Avatar 自选颜色 → 收益小 | S | 低 | P2 |
| F-13 | 千分位 | 手写 `groupedNumber`（除 3 取模 + Builder） | 19（`widgets.go:98-116`） | **SHOULD-USE-LIB** | `humanize.Comma(int64)`（`github.com/dustin/go-humanize@v1.1.0/comma.go`，已在 `go.mod:26` 间接依赖、模块缓存内） | S | 低：需把 go.mod 提升为直接依赖（无网络需求） | P2 |
| F-14 | 字符串去空白 | 手写 ASCII 版 `trimSpace` | 10（`input.go:48-57`） | **SHOULD-USE-STDLIB** | `strings.TrimSpace`（stdlib）；当前版本不过滤 U+3000 全角空格与不可见空白 → 全角空格的"空输入"会被当成有内容派发出去（行为改善，不是退化） | S | 低 | P2 |
| F-15 | 页面骨架 | 5 个巡检页各自重复"`Fill().Padding(20,24).Gap(12)` + heading + chips + `vm==nil` 等待文案 + 内容" | ~120（`source.go:21-46`、`trigger.go:19-54`、`eval.go:19-37`、`execution.go:18-35`、`settings.go:21-62`） | LOW-PRIORITY | 一个 `section(tk, title, sub, chips, vm, func())` + `listCard(header, rows)` 组件 | M | 中：纯视觉重构，Tester 断言可能受 padding 影响 | P2 |
| F-16 | 保存条协议 | `settingsSaveBar` 与 `promptSaveBar` 两份；`settingsPtr/promptPtr`、`reconcileSettings/reconcilePrompts`、`settingsDirty/promptDirty`、`settingsConflict/promptRevisionMoved` 四对同构 | ~180（`settings.go:178-271`、`prompt.go:149-258,309-314`） | LOW-PRIORITY | 泛型 `editSession[T]`（ptr 表 + dirty + reconcile + conflict + save bar） | M | 中：并发语义（§6.2 不做乐观更新）必须逐字保留 | P2 |
| F-17 | chip 家族 | `chip / textChip / chipOn / filterChip / chipButton / goalBadge` 6 个变体，`chipButton` 与 `filterChip` 几乎同一语义 | ~70（`widgets.go:30-96`、`deliverables.go:82-89`、`goal.go:148-161`） | LOW-PRIORITY | 收敛成 `chip(style, label)` + `toggleChip`；`ui.Badge`（`ui/api_gen.go:742`）无颜色参数，故自建**是合理的** | S | 低 | P2 |
| F-18 | 状态→颜色 | `toneFg/toneBg`、`goalBadge`、`pollChip`、`scheduleRow` 内联 fg/bg、`triggerEvent` 内联 5 处各写一份映射 | ~90 | LOW-PRIORITY | 一张 `stateColors map[state]{fg,bg}` 或 `toneColors(tk, tone)` 兜底 | S | 低 | P2 |
| F-19 | 过滤缓存 | ticket / eval / search 三套手写 memo（`src指针+filter`三元组 + 手动失效），逻辑同构 | ~50（`app.go:119-128,150-158`；`execution.go:187-198`；`eval.go:170-181`；`search.go:161-185`） | LOW-PRIORITY | 一个 `memo[K,V]`（或直接每帧算——行数已由投影限界） | S | 低 | P2 |
| F-20 | Snapshot 发布器 | 13 组 `PublishXxx` + `Xxx()` 样板，每组 4 行，共 ~90 行 | ~90（`vm.go:158-334`） | LOW-PRIORITY | 泛型 `shard[T]{atomic.Pointer[T]}` + `Snapshot.shards`；保留 `bump()` 不变 | M | 中：§6.2 每条 doc 注释是规范，迁移易丢文档 | P2 |
| F-21 | 用户可见文案 | 5 处把里程碑代号写给用户看："保存通道未接线（M2 接入）" | 5（`settings.go:202,207,279`；`prompt.go:42,70,175,181,274`；`onboarding.go:133`） | LOW-PRIORITY（copy bug） | 改成用户语言（"这次改动还无法保存"）；渠道是否存在已是事实，无需代号 | S | 低 | P2 |
| F-22 | Demo 投影 | `view/demo.go` + `view/demo_pages.go` 572 行，生产路径 0 引用（仅测试用） | 572 | **DEAD（生产）** | 移到 `internal/view/demotest/` 或改为 `_test.go` 内的 fixture；`M0-tasks.md:87` 曾承诺 M0-8 删除 | S–M | 低：测试需改包名/导入 | P2 |
| F-23 | 图标 | 30 个内联 SVG，**9 个从未使用**（icPause/icGear/icSend/icStop/icExternal/icPlus/icTray/icClose/icArrowR） | ~30 行 | **DEAD（子集）** | 删 9 个；图标数据本身**不是 NIH**（mygo 无图标集，只有 `ParseSVG`+`Icon`：`ui/svg.go:11-30`） | S | 极低 | P2 |
| F-24 | 死字段 / 测试专属 API | `App.inspect`、`focusComposer`、`wsPick/wsNames/wsIDs`（0 引用）；`view.SearchKindOrder`、`SearchVM.Group`（仅测试） | ~10 | **DEAD** | 删除；`labelOf`（`app.go:387`）可直接用 `view.Nav` 结果缓存 | S | 极低 | P3 |
| F-25 | design/prototype | 3015 行独立 module：复制了 `theme/tokens/icons/card/chip/kv/topbar/sidebar/statusbar` 等 15 个函数与 28 个 hex；12 屏样例；`replace => /tmp/mygo` 不可复现；`go test ./...`（根 module）不编译它 | 3 015 | **DEAD/FROZEN（设计产物）** | 定性为"视觉稿快照"：钉死 mygo 版本（或 vendor SVG 资产）、加"非应用代码"抬头、从 README/设计文档的"可运行代码"措辞降级；不要从中回迁代码 | M | 低（不影响产物），但**文档可信度**风险 | P2 |
| F-26 | 品牌资产 | `logo/logo.svg+dark.svg`、`design/prototype/assets/*` 4 个、`internal/ui/assets/logo.png` 1 个 = 三处副本 | 5 文件 47 KB | LOW-PRIORITY | 单一真源：embed `logo/logo.svg`（F-07 之后 PNG 可全删） | S | 低 | P2 |

---

## 3. 详细发现

### 3.1 P1

#### F-01 消息页把"无限日志"手搓成 Scroll+Column（`stream.go:17-36`）

```go
// internal/ui/stream.go:21-35
mui.Column(c).Grow(1).AlignItems(mui.Stretch).Background(tk.Bg).Children(func() {
    mui.Scroll(c).Grow(1).Children(func() {
        mui.Column(c).Padding(28, 24).Gap(22).AlignItems(mui.Center).Children(func() {
            mui.Column(c).Width(maxStreamWidth).Gap(22).Children(func() {
                if vm == nil || len(vm.Threads) == 0 { a.emptyStream(c); return }
                for _, th := range vm.Threads { a.thread(c, th) }   // ← 每帧构建全部历史
```

- **为什么算 NIH**：设计 §6.4 明说 List（虚拟化）开箱即用；这一页恰好是**唯一会无限增长**的列表（"一条连续的流"，消息只增不减）。
  现状每帧把 `Threads × Steps` 全部元素建出来再布局，帧成本随历史线性增长；`Scroll` 也不会自动跟随底部（新消息在下面，需要人手动滚）。
- **mygo 已有**：`ui.List(c, s, n, row func(int))`（`ui/list_builder.go:22`）+ `ListState.FollowEnd / FollowAnimation`
  （`ui/list.go:54-65`，注释原文："as a chat or a log does: scrolling away from the end stops following it"）+ `Key`
  （`ui/list.go:42-47`）。行高可以不等并被测量（`ui/list.go:12-24` 的 anchor 模型），所以"一条消息一棵小结"的异高
  版式不需要变。
- **迁移草图**：线程/步骤先拍平成 `[]view.Line` 或 `[]view.Thread`（保持现有的 `thread()`/`stepLine()` 渲染函数不动，
  只把外层 `for` 换成 `ui.List` 的 `row func(i)`）；`FollowEnd: true`、`Key: func(i) any { return rows[i].ID }`；
  左缩进竖线用每个 step 行自己的 `Box` 画（现在就是）。
- **风险**：`threads`/`steps` 两层嵌套拍平后，"消息头 + 步骤缩进"的宽窄（`maxStreamWidth` 居中）需要由每行自己负责，
  否则居中列会随虚拟化失效；性能回归要靠 `MYGO_FRAME_STATS=1` 对比。**主线程规则不变**（仍只读 VM）。

#### F-02 评估页手搓表头 + 非虚拟化列表（`eval.go:97-127`）

```go
// internal/ui/eval.go:103-126
mui.Row(c).Height(30).Padding(0, 14).Gap(10).AlignItems(mui.Center).Background(tk.SurfaceAlt).Children(func() {
    mui.Text(c, "时间").…Width(42)
    mui.Text(c, "决定").…Width(74)
    mui.Text(c, "Trigger → 结果").…Grow(2)
    mui.Text(c, "reason 码 · 命中规则").…Grow(3)
})
…
mui.Scroll(c).Grow(1).Children(func() {
    mui.Column(c).Children(func() { for i, d := range rows { … a.decisionRow(c, d) } })   // ← 全部行
```

- **为什么算 NIH**：表头列宽 + 行内容 + 滚动 = `ui.Table` 的完整职责；这里手写的 4 列宽和 `decisionRow` 里的
  `Width(42)/Width(74)/Grow(2)/Grow(3)` 必须手工保持一致（已经要靠人眼对齐），而 Decision 是**可增长**的（24h 窗口）。
  同项目里执行页/触发页都用了 `ui.List` 虚拟化，只有这页没虚拟化——不是设计选择，是漏了。
- **mygo 已有**：`ui.Table(c, s *ListState, columns []TableColumn, n int, cell func(row, col int)) Element`
  （`ui/api_gen.go:2169`）；`TableColumn{Title, Width, MinWidth, MaxWidth, Align, Sortable, Fixed}`（`ui/table.go:10-47`）；
  行虚拟化与 List 同源（doc："it builds only those in view"）。
- **迁移草图**：`cols := []ui.TableColumn{{Title:"时间", Width:42}, {Title:"决定", Width:74}, {Title:"Trigger → 结果", Width:0}, {Title:"reason 码 · 命中规则", Width:0}}`；
  `cell(row, col)` 内 `switch col`（列 0/1 单 Text，列 2/3 用现有 Column 片段）。表头点击排序可先不接（投影已排序）。
- **风险**：`Grow(2)/Grow(3)` 的 2:3 比例，`Table` 只能让两列**均分**剩余空间；若必须保比例，改成固定 `Width/MinWidth`
  （如 300/420），或接受均分。Tester 断言（`pages_test.go:123 TestEvalPageFiltersByDecisionKind`）按文本存在性判断，
  迁移后仍应通过——但需重跑。

#### F-03 两个 `ui.List` 用索引当选择：投影重排后"展开的行"会跳到另一条（`app.go:352-353`）

```go
// internal/ui/app.go:112-116, 352-353
threadList mui.ListState
threadSel  int
ticketList mui.ListState
ticketSel  int
…
a.threadList.Selected = &a.threadSel
a.ticketList.Selected = &a.ticketSel
```

- **为什么是 bug**：`ticketSel`/`threadSel` 是**行号**。而投影顺序不是稳定的：
  `engine.AllTickets()` 按 `CreatedAt` **倒序**（`internal/engine/engine.go:459`）→ 新任务一到就置顶，所有行号 +1；
  `ThreadVM.Threads` 按 `ActivityAt` **倒序**（`internal/runtime/pages.go:156-158`）→ 任何活动都会重排。
  于是：用户展开 `#42`（index 3），下一帧新任务置顶，`ticketSel` 仍是 3 → 展开的是原来 index 2 的任务，
  `ticketDetail(c, vm, r)` 会显示**另一条任务**的详情。这不是视觉瑕疵，而是"面板显示的和你点开的不是同一件事"，
  直接违反设计 §6.5"诚实状态"。
- **mygo 已有**：`ListState.Key func(row int) any`（`ui/list.go:42-47`）："With it, the state of a row … follows its item,
  and the list keeps its place **and its choice** when rows are added or removed above them"。实现见 `ui/list.go:548-554`：
  选择会按 key 重新定位并**回写到 `*Selected`**。
- **迁移草图**：`a.ticketList.Key = func(i int) any { return rows[i].ID }`（thread 同）；因为 rows 是每帧重算的切片，
  闭包每帧重建即可。`a.ticketSel` 从"我记住的行号"变成"列表回写的行号"，`jumpTo` 里 `ScrollTo` 的重定位逻辑可保留。
- **风险**：极低。唯一注意点：`view.FilterTickets` 过滤后 rows 变化时，key 找不到会把选择置 -1（`ui/list.go:551-552`），
  这与现在"过滤后 `ticketSel = -1`"的行为一致。可在 `ui/pages_test.go` 补一条"新投影置顶后展开仍在同一条"的回归。

#### F-04 "待处理"三处口径不一致（`chrome.go:49` / `overview.go:24` / `vm.go:347`）

```go
// internal/view/vm.go:346-348   侧边栏 badge
person = len(o.NeedsPerson) + len(o.Approvals)
// internal/ui/chrome.go:48-50    顶栏 chip
if ov != nil && len(ov.NeedsPerson) > 0 { chip(… fmt.Sprintf("%d 待处理", len(ov.NeedsPerson)) …) }
// internal/ui/overview.go:24     总览标题
heading(c, tk, "总览", fmt.Sprintf("需要你关注的只有 %d 件事，其余都在后台运行", len(ov.NeedsPerson)))
```

- 同一份"需要人"的事实被三处各自推导：有 2 条待批准时，侧边栏说 4，顶栏说 2，总览标题说 2——而总览页面上方
  明明还画着"待你批准"那块（`overview.go:49-70`）。设计 §6.5 要求"计数等于它链接的列表长度"，这里三个数没有一个
  同时等于它所在表面显示的行数。
- **建议**：在 `internal/view` 提供唯一推导，例如 `func (o *OverviewVM) PersonCount() int` 与 `Nav()` 共用它
  （`vm.go:344-359` 已经在做），`chrome.go`/`overview.go` 都调它。工作量 S，风险低（只改数字）。
- **同类小问题**：`overview.go:174-179` 在 view 里手数 `Health` 的坏项（可以但没必要，VM 提供 `Problems` 更符合 §6.2 "投影携带事实"）。

#### F-05 托盘菜单里的死项（`tray.go:23-25`）

```go
{Label: "暂停运行时", Click: func(*mygo.MenuItem, *mygo.Window) {
    // The runtime is wired in M1; M0 only proves the menu works.
}},
```

- 1.0.0-rc.1 里仍是一个点了没反应的菜单项，且 `internal/runtime` **没有** Pause 能力（`grep func (r *Runtime) Pause` 为空），
  注释还停在 M0。属于用户可见的死控件。**建议**：删掉（等能力有了再加），或改成"打开面板/退出"这类已实现的项；
  同时可顺带用 `Tray` 菜单表达"需要人"计数（设计 §6.7 期望的托盘职责），但那需要每帧/每次 publish 更新菜单——
  注意主线程规则（走 `win.Update`）。工作量 S，风险低。

### 3.2 P2 · SHOULD-USE-MYGO

#### F-06 命令面板：`List` 的分组表头与键盘选择就是现成的（`search.go:98-143`）

现状：`Scroll` + `Column` + 手写 `n` 计数 + `a.searchSel` + `c.Shortcut(0, mui.KeyUp/KeyDown)`（`search.go:48-56`）。
分组标题"不可选"是靠手写 `n` 只对 hit 自增实现的（`search.go:100-111`），键盘越界靠 `clampSearch`（`search.go:188-195`）。

- **mygo 已有**：`ListState.Header func(row int) bool`（`ui/list.go:93-97`）："reports whether row i heads a section…
  **Headers are not chosen**"；`Key`/`Label`（`ui/list.go:42-47,89-92`，Label 同时给无障碍读）；`Selected` + `Changed()`
  （`ui/list.go:66-70`：点击/↑↓/Home/End/PageUp/Down 全部内建）；行虚拟化（面板有 `SearchGroupLimit=20`，收益小但无所谓）。
- **迁移草图**：`rows := append(headers, hits...)` 拍平为 `[]row`，`s.Header = func(i) bool { return rows[i].isHeader }`，
  `s.Key = …`，`s.Selected = &a.searchSel`（行号 → 需要把"第几个 hit"改"第几行"）；Enter 走 `lst.Submitted()`；
  `jumpTo` 用 `rows[*Selected]`。可删 `clampSearch/moveSearch`（约 20 行）。
- **风险**：`jumpTo` 依赖"hit 下标"，改行号后要与 group 边界对齐；`search_test.go` 的 6 个用例需逐一核对（它们点多行、
  按文本断言，预计可行）。若判断投入产出不划算，**降级为 P2 可暂不做**——现状功能正确，只是重复了框架能力 + 无障碍缺失。

#### F-07 Logo：SVG 是 ImageSource，不需要 PNG+Once（`chrome.go:110-128`）

```go
//go:embed assets/logo.png
var logoPNG []byte
var (logoOnce sync.Once; logoBmp *mui.Bitmap)
func uiLogo(c *mui.Context, size float32) { logoOnce.Do(func(){ if b,err := mui.DecodeBitmap(logoPNG); err==nil { logoBmp=b } }); … }
```

- **mygo 已有**：`ParseSVG`/`MustParseSVG` 返回 `*SVG`，而 `ImageSource` 就是 `imageSize()`（`ui/widgets.go:520-522`），
  `*SVG` 实现了它（`ui/svg.go:29-46`）；`ui.Image` 对 SVG "in its own colors"（`ui/svg.go:12-15`：
  "show it with Icon, in the color of the text, or with Image, in its own colors"）。`DecodeImage` 也会回落 SVG
  （`ui/widgets.go:533-540`）。
- **迁移**：`//go:embed ../../logo/logo.svg`（或把 svg 复制进 `internal/ui/assets/`）→ `var logo = mui.MustParseSVG(logoBytes)`；
  `uiLogo` 变成 `mui.Image(c, logo).Size(size, size)` 一行；删 `logoOnce/logoBmp/DecodeBitmap` 分支。托盘图标仍可保留 PNG
  （`TrayOptions.Icon` 要字节，系统托盘对 SVG 支持不一），但 `internal/ui/assets/logo.png` 可只服务托盘。
- **风险**：SVG 渲染器不支持 filter/text（`ui/svg.go:20-23`）；`logo/logo.svg` 是渐变+路径，README 说 UI 已能直接渲染。
  若某些平台渐变有偏差，保留 PNG 回退——但**只留一个真源**。

#### F-08 token 层应派生自 mygo Theme（`tokens.go`、`app.go:284`）

- 现状：`theme()` 从 `LightTheme()` 起改 17 个字段；`tokens()` 又把**同一批 18 个颜色**再写一遍成独立 `Tokens` struct；
  两者没有函数关系，只能靠人肉同步。已经漂移的证据：`theme()` 里 `t.Radius = 8`（`tokens.go:51`），
  `tokens()` 里 `Radius: 12`（`tokens.go:80`）——同一界面里控件圆角 8、卡片圆角 12，是**有意**的层级差，但代码上
  看不出是"有意的两层"，因为两处各自硬编码。更关键的是 `theme()` 永远 `LightTheme()`（`tokens.go:34`）：
  mygo 默认跟系统外观（`ui/runtime.go:507-526`：`defaultTheme()` 按 `rt.host.isDark()` 选 `LightTheme/DarkTheme`），
  而 `Themed(c, a.theme)`（`app.go:284`）把这个能力盖掉了 → 暗色主题实际不可用，且暗色下 `tokens()` 的浅色硬编码会全错。
- **mygo 已有**：`Theme` 全字段 + `Dark bool`（`ui/theme.go:8-64`）、`ui.Themed`（`ui/api_gen.go:2357`）、
  `Context.Theme()`（`ui/api_gen.go:518`"the light or dark theme following the system's appearance"）、
  `Theme.Space(n)`/`Theme.Rem(n)`（`ui/theme.go:66-78,96-104`，可替代散落的 `Spacing` 魔法数）。
- **建议**（与设计 §6.5 的 `FromTheme(t)` 一致）：`func tokensFrom(t *mui.Theme) Tokens` 只映射已有字段（Bg=Background、
  Surface=Surface、Border、Text、TextMuted、Accent、Green=Success、Red=Danger、Amber=Warning、Radius=t.Radius），
  brand 专有软色（`SurfaceAlt/BorderSoft/TextFaint/AccentSoft/Cyan/Violet/VioletSoft/GreenSoft/AmberSoft/RedSoft/CodeBg/CodeText`）
  作为**相对 token 的派生或常数表**；每帧 `a.tk = tokensFrom(c.Theme())`（值类型，无分配）或按 `Dark` 分支缓存两份。
- **风险**：中。暗色化会暴露所有"软色"配比问题；图片/托盘不受影响。若产品决定"首版只有浅色"（设计未这么写），
  那至少应删掉 `tokens()` 里与 `theme()` 重复的 10 个颜色，改成从 `*mui.Theme` 读，避免两套真源。

#### F-09 / F-10 / F-11 / F-12（薄控件）

- **F-09 onboarding 下拉**（`onboarding.go:186-198`）：手搓 `Row`+`OnClick`+`ifColor(active…)` 的 segmented。
  `ui.Segmented(c, &sel, labels...)`（`ui/api_gen.go:1821`，带箭头键与 radio 语义）或 `ui.Select(c, &s, options)`
  （`ui/api_gen.go:1849`）直接可替。注意 `SettingKind=select` 在设置页 `settingField`（`settings.go:133-173`）里**根本没实现**
  ——只有 onboarding 实现了 → 两个渲染器已经分叉；见 F-16。
- **F-10 分隔线**（27 处 + `eval.go:184 dividerV`）：`ui.Divider(c)`（`ui/api_gen.go:1090`）已存在，且 `design/prototype`
  自己在用（`screens_*.go` 里十余处 `ui.Divider(c)`）——应用反而没用。竖线可用 `Column()` 内的 Divider（Direction 跟随）。
- **F-11 运行时占用条**（`overview.go:245-251`）：`Width(w*286)` 把"320 卡片 - 2*16 padding - …"写成了字面量，卡片宽度一改就裂。
  `ui.Progress(c, float64(running)/float64(maxJobs))`（`ui/api_gen.go:1604`）或 `ui.Meter`（levels 支持 warning/critical，
  `ui/api_gen.go:1409`）两行搞定，且跟随主题。
- **F-12 头像**（`stream.go:194-198`）：`ui.Avatar(c, name, nil)`（`ui/api_gen.go:715`，"initials of name on a color it picks"）
  可替，但应用需要"你"= accent 色，"张三"取首字且配色由 brand 控制 → 自建 4 行**可接受**（判 LOW）。
  若采用 `Avatar`，可顺手拿到无障碍（"Assistive technology sees an image named name"）。

### 3.3 P2 · SHOULD-USE-LIB / STDLIB

#### F-13 `groupedNumber` → `humanize.Comma`（`widgets.go:98-116`）

```go
func groupedNumber(n int) string { if n < 1000 { return fmt.Sprint(n) }; … strings.Builder + 取模插逗号 … }
```

- `github.com/dustin/go-humanize v1.1.0` 已在 `go.mod:26` 作为 **indirect** 依赖（eino 链路），模块缓存里就有
  `comma.go`：`humanize.Comma(1204331) == "1,204,331"`。唯一调用点是 `overview.go:255`（token 总数）。
- **迁移**：`usage = fmt.Sprintf("… · %s tokens …", humanize.Comma(int64(ov.Usage.Total)))`；删 19 行。
  代价：`go.mod` 把它从 indirect 提升为直接依赖（`go mod tidy` 无需网络，包在缓存内）。
- **风险**：低。若不想在 UI 层新增直接依赖，也可自己保留——但"已引入的库不用"是典型的 NIH 账。

#### F-14 `trimSpace` → `strings.TrimSpace`（`input.go:48-57`）

- 手写版本只认 `' ' \t \n \r`；`strings.TrimSpace` 用 `unicode.IsSpace`（含 U+3000 全角空格、NBSP、\v\f）。
  中文输入法下全角空格极常见：输入"　"（全角空格）时手写版返回非空 → `submit()` 会把空白当作任务派发出去。
  **替换是行为改善**，不是等价替换（应在 commit 说明里写清）。风险极低。

### 3.4 LOW-PRIORITY · 内部重复

- **F-15 页面骨架**：5 个巡检页开头 15–60 行几乎同构（`Fill().Padding(20,24).Gap(12)` + `Row(heading, Spacer, chips)` +
  `if vm == nil { Text("等待运行时发布X快照…"); return }`）。抽 `sectionHeader(tk, title, sub, chips...)` +
  `waitingLine(tk, what)` + `listCard(tk, header, rows)` 可省约 100–150 行；风险是视觉微调（padding/gap 是像素级），
  建议随下一次页面改动顺手做，不做独立重构。
- **F-16 保存条/编辑会话协议**（`settings.go:178-271` vs `prompt.go:149-258,309-314`）：两套同构实现（指针表、dirty 计数、
  reconcile、conflict、save bar、未接线禁用态）。加第三种可编辑投影（如工作区管理）时必然第三次复制。建议泛型化**在第三次出现之前**。
- **F-16b 表单字段渲染**：`settingField`（`settings.go:133-173`）与 `formField`（`onboarding.go:174-202`）重复，
  且后者有 `select`、前者没有（同一 `view.SettingKind` 两套解释）。已有注释自认："It mirrors settingField but takes a plain buffer"
  ——这句话就是重复的证据。
- **F-17 chip 家族 / F-18 状态→颜色 / F-19 memo / F-20 Snapshot 样板**：见汇总表。其中 F-20 要注意 `vm.go` 的 13 组方法
  每条都带规范注释（§6.2 的行为约定），泛型化会丢注释，**收益小于损失**，建议保持现状（判 LOW，列出来是为了防止误"优化"）。
- **F-21 用户可见的里程碑代号**：`"保存通道未接线（M2 接入）"` 出现在 5 个文件 9 处。生产路径其实全接线
  （`internal/runtime/pages.go:277 SaveSettings`、`internal/runtime/onboarding.go:104,140`、`internal/runtime/prompts.go:65`），
  这些字符串只在"别家 runtime/测试"时出现；但**用户能看到的文案里不该有 M2**。

### 3.5 DEAD / 未用资产

- **F-22 `view/demo*.go` 572 行**：`DemoOverview/DemoSource/DemoThreads/DemoDecisions/DemoPrompts/DemoSchedules/DemoGoals/DemoTickets/DemoSettings`
  在生产代码中 0 引用（`grep` 只命中 `internal/view/*_test.go` 与 `internal/ui/pages_test.go` 等）。文件头自己写着
  "removed at M0-8"（`demo.go:5-7`），`design/M0-tasks.md:87` 也登记过。**建议**：移到 `internal/view/demotest` 子包
  （导出、带 doc）或保持导出但加 `//nolint`/文件级说明"test fixture only"；最省事是加一个包级 `_test.go` 变体。
  注意 `internal/runtime` 的 `humanAge` 与 demo 数据无关，不要一起删。
- **F-23 9 个未用图标**：`icPause`(icons.go:15)、`icGear`(16)、`icSend`(27)、`icStop`(28)、`icExternal`(29)、`icPlus`(30)、
  `icTray`(34)、`icClose`(35)、`icArrowR`(36) —— 全仓库出现次数 = 1（即仅声明）。删除无风险。
  （图标机制本身**不是 NIH**：mygo 没有图标集，只有 `ParseSVG`+`Icon`，见 `ui/svg.go:11-30`；`navigation` 里常用图标
  用 24×24 stroke SVG 是这个框架下的正确做法。）
- **F-24 死字段与测试专属导出**：`App.inspect`（`app.go:86`）、`App.focusComposer`（`app.go:92`）、
  `App.wsPick/wsNames/wsIDs`（`app.go:105-107`）——0 引用（设计 §6.9 明确"输入框不传工作区"，这些字段是那版设计的残留）；
  `view.SearchKindOrder()`（`view/search.go:35`）与 `(*SearchVM).Group()`（`view/search.go:124`）——只被测试使用。
- **F-25 `design/prototype` 的定位**：见下节。
- **F-26 品牌资产三份副本**：`logo/logo.svg` + `logo/logo-dark.svg`（4 482 B each）、`design/prototype/assets/` 4 个文件
  （33 874 B）、`internal/ui/assets/logo.png`（12 901 B）。`internal/ui` 只 embed PNG；SVG 真源在 repo 根但没被任何代码引用。
  F-07 之后应只剩 `logo/logo.svg`（+ 托盘用 PNG 一份）。

---

## 4. "正确使用 mygo / 框架"的部分（明确不算 NIH）

这些是自建**合理**或有意的，报告里不应计为 NIH：

| 自建物 | 位置 | 为什么合理 |
| --- | --- | --- |
| `card` / `cardG` | `widgets.go:12-24` | 设计 §6.4 已把 Card 列为"需要自建"缺口（"`Column().Padding().Radius().Background().Border()` 包一层，1 天"）。实现与设计给的方案一字不差。 |
| `chip` / `textChip` / `chipOn` / `filterChip` | `widgets.go:30-96` | `ui.Badge` 只接受文本、不接颜色（`ui/api_gen.go:742`）；带 tone 色和计数的 filter chip 是信息架构的一部分（§6.10 阶段过滤）。自建合理；只有 `chipButton`（`deliverables.go:82`）与 `filterChip` 语义重叠，可收敛（F-17）。 |
| `heading` / `dot` / `kv` / `stat` / `detailFact` / `mono` / `panelNote` | 各文件 | 纯布局组合，mygo 没有"Stat/KV"这类复合件；每个 5–8 行，是 view 该有的东西。 |
| `icons.go` 的 30 个 SVG | `icons.go:7-37` | mygo 提供的是 SVG 渲染器（`ParseSVG`/`Icon`，`ui/svg.go:11-30`），不是图标集（MC 里 grep 无 lucide/icon 集合包）。图标**数据**属于应用内容。只需删未用的 9 个（F-23）。 |
| 中文相对时间（`humanAge`/`agoWord`/`untilWord`） | `overview.go:347-364`、`schedule.go:129-141` | `go-humanize` 的相对时间只有英文（`humanize.Time/RelTime` 输出 "3 minutes ago"），中文无复数、量词不同，套用会产出"3 分钟前前"这类拼接。**保留自建合理**，但要收敛成一份（见 §5）。 |
| `view/reason.go` 码表、`TicketBucket.Label()`、`GoalState.Label()`、`ScheduleRow.LastStatusLabel()` | `view/reason.go:9-42`、`pages.go:304-318`、`goal.go:26-38`、`schedule.go:36-56` | 设计 §1.3 决定"不做 i18n 全量，首版中文 + 保留码表结构"。码表放 view（纯函数、可测）是正确分层；mygo 的 `Locale/i18n`（`ui/locale.go:1-16`、`Context.Locale()`）留给将来真做多语言时再上，现在**不该**为它提前造框架。 |
| `view.BuildSearch` 的 substring + 前缀加权、分组、截断 | `view/search.go:150-179,343-360` | 域内行为（按"工单/消息/决策/工作区/来源"分组是设计 §6.4 的产品语义）。`sahilm/fuzzy` 之类**不在依赖图**，引入会改变匹配语义（fuzzy 会把"wrk"匹配到"workspace"，与"输工单号"的用途冲突），且丢失"前缀>包含"的可解释排序。**判 JUSTIFIED**。没有自建高亮（全仓库无 highlight 渲染），也就没有可替换的高亮逻辑。 |
| 24h 活动柱状图 | `overview.go:198-230` | 设计 §6.4 把"Chart 高层封装（活动柱状图）"列为自建项，并给了方案 `chart.NewBand + Rect`。现状用 flex 画柱（10 行），比 `ui/chart`（`ui/chart/chart.go:67 Plot`、`ui/chart/scale.go:272 NewBand`）更短，但没有轴/标签/悬停。**判 JUSTIFIED**；若要"有刻度的图"再迁 `ui/chart`。 |
| 自绘 timeline（消息流缩进、触发页时间线） | `stream.go:49-67`、`trigger.go:127-191` | mygo 的 `Timeline` 是**动画时间线**（`ui/timeline.go:1-45`：Tween/AfterPrevious/播放头），不是事件时间线，不可替换。**注意不要误判**。 |
| 快照架构（`atomic.Pointer` 分片 + `OnPublish→Invalidate`） | `view/vm.go:158-207`、`app.go:231-236` | 设计 §6.2 的指定实现；只用 stdlib `sync/atomic` + `sync.Mutex`，没有自造 observable/event-bus。严格说不算"框架能力缺失"。 |
| 未自建 Markdown / Diff / 代码编辑器 / 富文本 | 全包无引用 | 应用目前不需要（消息是纯文本行；`Line` 只带 `DiffAdd/DiffDel` 计数显示为 chip）。**没有半套自建**是优点；将来要渲染 agent 回复时用 `ui/markdown.View/Stream`（`ui/markdown/view.go:98,115`）与 `editor.DiffView`（`ui/editor/diffview.go:412`），不要再手写。 |
| 键盘/焦点：`ui.Handle` + `Bind`/`Focus`、`c.Shortcut`、`AutoFocus` | `prompt.go:110-125`、`search.go:42`、`app.go:296` | 没有自建 focus 管理器；`Handle` 正是设计 §6.1 规则 2 允许跨帧的唯一句柄。 |
| 文本输入：`ui.TextInput` / `TextArea` + `Key()` + `Password()` + `Submitted()` | `input.go:15`、`settings.go:168-171`、`prompt.go:123` | 没有自建输入框/光标/选区/IME。 |
| 弹层：`ui.Modal`（面板与交付物）、`ui.SearchField` | `search.go:40-42`、`deliverables.go:20` | 没有自建 dialog/overlay/toast。 |
| 列表：执行页与触发页的 `ui.List` + `ListState.Selected` + `Changed()` | `execution.go:84-88`、`trigger.go:79-85` | 用对了（行虚拟化），只是漏了 `Key`（F-03）与表头（F-02）。 |

**结论**：`internal/ui` 是仓库里唯一 import mygo 的生产包（`grep -rln egoist/mygo` 只另有 `spike*`），符合设计 §2 的
"mygo 用法收在一处"约束；没有自造 UI 框架、组件库、布局系统、主题系统的迹象。

---

## 5. 仓库内部的重复工具（跨包）

| 工具 | 副本 | 差异 | 建议 |
| --- | --- | --- | --- |
| 中文相对时间 | `internal/ui/overview.go:354 humanAge`、`internal/runtime/pages.go:365 humanAge`（**逐字相同**）、`internal/brief/digest.go:161 humanAge`（措辞不同："12 分钟前" vs "12 分前"） | 同一事实三种措辞，同一 UI（总览说"12 分前"，近况摘要说"12 分钟前"） | 抽 `internal/ui/format`（或 `internal/humanize/zh`）一个函数 + 两个 wrapper（`agoWord`/`untilWord`），ui/runtime/brief 全用。工作量 S–M。 |
| 截断（`clipText`/`clip`） | `internal/view/search.go:364`、`internal/runtime/pages.go:377`、`internal/brief/digest.go:150-158` | 三处 rune 截断 + "…" | 同上，放同一 format 包。 |
| 阶段/状态中文词 | `view.BucketOf`（`pages.go:327`）、`runtime.stageWordOfTicket`（`pages.go:420`）、`runtime.toneOfStage`（`pages.go:442`） | 粒度不同（bucket vs stage 词），但"哪个阶段叫什么"有两套真源 | 建议 stage→词 收敛到 `view`（UI 词都在 view），runtime 只提供 stage 字符串。**注：runtime 不在本审计范围，供 Lead 汇总时交叉参考。** |
| 三份 demo/沙盒数据 | `view/demo*.go` 与 `internal/runtime` 的测试夹具 | — | 见 F-22。 |

> 相邻观察（**超出本审计范围**，供交叉核对）：`internal/runtime/pages.go:202` 把
> `EvalCounts{Evaluated: len(vm.Rows), Decisions: len(vm.Rows)}` 赋成同一个数——评估页 4 个数字里前两个必然相等
> （`eval.go:46-48` 会显示"评估的 Trigger == Decision"）。如果这是占位，应像其他页面一样在投影里写清"此项未接线"，
> 否则读起来像是真的统计。

---

## 6. `design/prototype/` 的定位（F-25 展开）

**它现在已经不是"证明 UI 可行"的探针，而是一份 3015 行的陈旧分叉。** 证据：

1. **与 `internal/ui` 同源复制**：`design/prototype/chrome.go` 里 `card`(22)/`cardG`(29)/`dot`(43)/`chip`(47)/
   `textChip`(52)/`heading`(65)/`kv`(72)/`topbar`(139)/`sidebar`(181)/`statusbar`(235)`，与 `internal/ui/widgets.go`、
   `chrome.go` 同名同义；`theme.go:33 theme()`、`theme.go:57 tokens()` 与 `internal/ui/tokens.go:33,57` 同名同结构；
   28 个 hex 颜色**集合完全相同**（`grep -o '#[0-9A-Fa-f]\{6\}' | sort -u` 两侧一致）；`icons.go` 与
   `internal/ui/icons.go` 的图标变量集只差 `icUser`。`design/M0-tasks.md:87` 也写明 M0-7 就是"从 prototype 提炼 chrome"。
2. **两边已开始漂移**：prototype 有 12 屏（含 `13-multiwindow`、`14-new-task`、`12-workspace-admin` 这些**应用里不存在**的
   界面），且自己声明"real widget is ui.SearchField"/"the real app uses ui.Tabs"（`chrome.go:152`、`screens_execution.go:229`）
   ——即它连"用真控件"的目标都没达到，不能当作控件用法的参考。
3. **不可复现**：`design/prototype/go.mod:14` `replace github.com/egoist/mygo => /tmp/mygo`，而 `/tmp/mygo` 不是 v0.7.4
   （`ui/theme.go` 与 MC 版本有 diff）。换台机器 `go run . -out shots` 直接失败；根 module 的 `go test ./...` 不编译它
   （nested module），`Makefile` 的 `check/cross/test` 全都不覆盖 → 不受任何闸门保护。
4. **是否还有人用**：`README.md` 与 `CHANGELOG.md` 说"截图不再入库，用 `go run . -out shots` 重新生成"；设计文档
   §6/§11（`design/zeokeel-go-mygo-design.md:1154,1824`）把它当"视觉稿"。`Makefile`、`scripts/`、`.github` 无引用
   （grep 空）。→ **它是设计交付物，不是构建目标。**

**建议**（不删，但要止损）：
- `go.mod` 的 `replace` 改成 `github.com/egoist/mygo v0.7.4`（与根 module 一致）；若必须用 `/tmp/mygo` 的新特性，
  说明理由并钉 commit（不可接受现状）。
- `main.go` 顶部 + `README.md` 加一句"**这是视觉稿，不随产品维护；唯一真源是 `internal/ui`**"，避免后人从 prototype 回迁。
- 可选的治理：把 12 屏拆到 `design/prototype/screens/` 下并加 `//go:build ignore` 之外的说明，或整体标注 ARCHIVED。
- **不要**做的：把 prototype 的 chrome/tokens 反向合并回 `internal/ui`（会带回更多手搓控件）。

---

## 7. 与设计文档的偏差（可当 review checklist）

| 设计条款 | 现状 | 影响 |
| --- | --- | --- |
| §6.4 CommandPalette = `Modal + SearchField + List` | `Modal + SearchField + Scroll/Column`（`search.go:40,98`） | 丢掉 List 的键盘/无障碍/虚拟化（F-06） |
| §6.4 开箱即用清单里 Table 在列 | 评估页手搓表（`eval.go:97`） | F-02 |
| §6.5 "计数等于它链接的列表长度" | "待处理"三处不一致（F-04） | 用户可见 |
| §6.5 "从 `LightTheme()/DarkTheme()` 派生我们自己的 token 层" + `FromTheme` | 两个独立函数，永远 LightTheme（F-08） | 暗色不可用；双真源 |
| §6.4 Chart 高层封装用 `chart.NewBand + Rect` | flex 手画柱（`overview.go:214-221`） | 无刻度/标签（判可接受） |
| §6.4 DataGrid 过滤/分组/排序"纯函数管线，不在 view 里排序" | ✅ 遵守（`view.FilterTickets/FilterDecisions`、ui 侧只缓存；`grep sort. internal/ui` 空） | 正确 |
| §6.1 规则 2 不跨帧保存 Element/Context | ✅ 只有 `int/string/*string/mui.Handle/ListState` 跨帧 | 正确 |
| §6.2 不做乐观更新 | ✅ settings/prompt 都是"等投影匹配才清 edit"（`reconcile*`） | 正确 |
| §1.3 不做 i18n 全量 | ✅ 中文硬编码 + view 码表，没有自造 i18n 框架 | 正确（mygo 的 `Locale/i18n` 留待将来） |
| §6.7 托盘：重启运行时 / 需要人通知 | 只有"打开面板/暂停（空）/退出"（`tray.go:16-29`） | 功能缺口 + 死项（F-05） |

---

## 8. 建议执行顺序

**第一批（P1，1–2 天，风险可控，含 2 个用户可见 bug）**
1. F-03 给两个 `ListState` 加 `Key`（S）——顺手补回归测试。
2. F-04 收敛"待处理"计数到 view 一个函数（S）。
3. F-05 删掉托盘死菜单项（S）。
4. F-13 / F-14 两个 stdlib/lib 替换（S，含 `go.mod` 提升 humanize）。
5. F-23 删 9 个未用图标（S）。

**第二批（P2 结构，3–5 天）**
6. F-02 评估页迁 `ui.Table`（M）——可先只换表头 + 行虚拟化，保持 `decisionRow` 内容不动。
7. F-01 消息页迁 `ui.List + FollowEnd`（M）——先加 `FollowEnd`（只改外层），虚拟化单独一步。
8. F-07/F-26 logo 单真源 + `ParseSVG`（S）。
9. F-08 token 派生（M，暗色可分开做）。
10. F-09/F-10/F-11 薄控件替换（S×3）。

**第三批（可选，P2/P3）**
11. F-06 面板迁 `List`（M，收益是键盘/无障碍，功能已正确）。
12. F-15/F-16 页面骨架与保存条收敛（M，建议随下一次页面开发顺手做，不做独立重构）。
13. F-22 demo 移出生产包（S）、F-24 删死字段（S）、F-25 prototype 定性（S）。
14. F-12 头像、F-21 文案（S，随时可做）。

**明确不建议做的"优化"**：
- 把 `view.Snapshot` 的 13 组 Publish 泛型化（F-20）——注释即规范，收益小于损失。
- 引入 `sahilm/fuzzy` 替掉 `matchScore`——会改变搜索语义（失去"前缀>包含"与工单号精确匹配的可解释性）。
- 用 `go-humanize` 的中文相对时间——它会输出英文。
- 从 `design/prototype` 回迁任何 chrome/token 代码。

---

## 9. 方法与限制

- **证据纪律**：每条"mygo 已有 X"都给出 `MC/ui/*.go` 的文件:行与符号；每条"仓库里 Y"都给 `internal/**` 文件:行。
  推断处用"（推断）"标注，例如 F-03 的"重排导致展开行漂移"由三段代码合成
  （`engine.go:459` 倒序 → `runtime/pages.go:211` 保持顺序 → `execution.go:62,84-88` 用索引选择），
  未实际构造运行时复现；建议按 §8 第 1 条修完后补一条 Tester 回归把它钉死。
- **未验证项**：`go vet` / `go test` 未运行（只读约束）；F-07 的 SVG logo 在三个平台的渲染一致性未实测；
  F-02 列宽改固定后 `pages_test.go` 是否仍绿未跑。
- **计数口径**：LOC 用 `wc -l`；"未使用"= 全仓库 `grep` 出现次数为 1（仅声明）或仅测试文件引用。
- **可复核命令**（只读）：
  ```sh
  MC=$(go env GOMODCACHE)/github.com/egoist/mygo@v0.7.4
  grep -n "FollowEnd\|Headers are not chosen\|The choice follows its item" $MC/ui/list.go
  grep -n "func Table\|TableColumn" $MC/ui/table.go $MC/ui/api_gen.go
  grep -rn "fmt.Sprint\|strings.Builder" internal/ui/widgets.go
  grep -rn "Demo" --include=*.go internal | grep -v _test
  for v in icPause icGear icSend icStop icExternal icPlus icTray icClose icArrowR; do echo "$v $(grep -rho $v internal/ui | wc -l)"; done
  ```
