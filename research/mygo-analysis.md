# mygo 能力与约束报告（应用架构师视角）

> 对象：`github.com/egoist/mygo`（本地 `/tmp/mygo`，main 浅克隆）。形态：**单机单二进制桌面 app、mygo 原生窗口（`ui` 包）、UI 与 core 同进程直连**。
> 依据：源码 + `docs/`（architecture.md 363 KB 仅定点 grep）。代码片段取自 `examples/` 与源码/文档原文。

## 0. 结论摘要

1. **原生 UI 路线成立**：`ui` 覆盖度接近成熟设计系统——列表/表格虚拟化、Tabs、Sidebar、Split、Form、Markdown、代码编辑器、Diff、Chart、Toast、AlertDialog、Tooltip、Tree/Outline 全有。
2. **范式是 Dear ImGui / egui 派**：`view func(*ui.Context)` 每帧重跑，输入轮询（`Clicked()/Changed()`），状态在你自己结构体里，**`ui.Element` 只在本次 build pass 有效**。
3. **最关键约束**：view 只在主线程跑；后台 goroutine 改状态必须经 `Window.Update(fn)`（主线程执行+重绘）或「不可变快照 + `Window.Invalidate()`」；`ui` **没有**公开的 Post/Observable/Store，`loop.go` 的 `onMain/postMain/await` 是包内私有。
4. **主线程单点**：事件循环、view 构建、布局、绘制、handler 全在主线程，任何阻塞直接冻结 UI；持锁调用阻塞式 mygo API 会死锁。
5. **托盘驻留可行但有硬规则**：托盘图标不阻止退出——最后一个窗口关闭 app 就退出；必须「关闭=隐藏」或注册 `OnWindowAllClosed`。
6. **SQLite 在 `CGO_ENABLED=0` 下可用**：`plugins/sqlite` 用 purego dlopen 预编译 C 库（Zig 编译），`sqlite.Open` 直接给 native UI 用，不走 webview IPC。
7. **体积量级**：release（`-tags mygo_noinspector -ldflags "-s -w"`）counter-native **8.9 MB**、hello ~7 MB，idle 22–37 MB；加 editor/markdown/chart/sqlite 预计 12–16 MB。
8. **版本 0.7.4、0.x 无 API 兼容承诺**：已有 `migrate-ui` codemod 与 checked-elements 破坏性重构；把 mygo 用法收进薄适配层。
9. **Linux 真窗口需 GTK3 + X11/Wayland**（OpenGL 可选，`MYGO_GPU=0` 走 CPU 光栅）；无显示器起不来，但测试完全 headless：`ui.NewTester(view,w,h)` 在 `go test` 里点击/键入/断言/虚拟时钟/截图，E2E 用 automation 插件（socket 驱动真窗口，需显示器）。

## 1. 项目快照与 API 稳定性

- `mygo.Version = "0.7.4"`（`mygo.go:70`），与 `packages/runtime`、`packages/cli` 同版本，tag 触发 release。
- 浅克隆（`.git/shallow`）拿不到历史；`go.mod` 要求 `go 1.27.3`（用了 `encoding/json/v2`、`weak` 等新 API），工具链必须对齐。
- **无 CHANGELOG、无 API 稳定性条款**；`docs/ui/migration.md` 记录过 `*ui.Element → ui.Element`、`c.Key()` 这类破坏性迁移（配 `go tool mygo migrate-ui`）。→ 架构上把 mygo 用法集中到 `internal/ui` 适配层，锁 go.mod 版本。
- 依赖面小、质量高（`go.sum` 完整、无 replace）：`ebitengine/purego v0.11.1`、`go-text/typesetting v0.3.5`、`golang.org/x/{image,net,text}`、`hajimehoshi/go-mp3`、`jfreymuth/oggvorbis`。本环境无外网（proxy.golang.org 超时），**仅静态核对 go.sum，未做在线可达性验证**。

## 2. 生命周期、单实例、多窗口、托盘常驻

`mygo.App` 单例，`init` 里 `runtime.LockOSThread()`（`mygo.go:50`），`App.Run()` 必须在 `main()` 调用。

```go
func main() {
	if !mygo.App.RequestSingleInstanceLock() {
		return // 第二个实例把命令行交给第一个后退出
	}
	mygo.App.OnSecondInstance(func(args []string, wd string) { if w := mainWindow(); w != nil { w.Restore(); w.Focus() } })
	mygo.App.WhenReady(func() {
		app.win = mygo.NewWindow(mygo.WindowOptions{
			Title: "ZeoKeel", Width: 1280, Height: 800, MinWidth: 960, MinHeight: 600,
			StateKey: "main",                    // 记住位置/尺寸/最大化
			Content:  ui.View(app.view),
		})
	})
	if err := mygo.App.Run(); err != nil { log.Fatal(err) }
}
```

- `WhenReady(fn)`：就绪后主线程执行，已就绪则立即排队；窗口只能在此之后创建（其他 goroutine 创建会 `waitReady` 阻塞等待）。**多窗口**：`WhenReady` 内或就绪后任意 goroutine 都可 `NewWindow`；一个 `ui.View(...)` 可服务多窗口，**每窗口独立元素状态**。
- 退出链：`OnBeforeQuit` → 各窗口 `OnClose` → `OnWillQuit`（均可 `PreventDefault()`）；默认**最后一个窗口关闭即 `Quit()`**（`app.go lastWindowClosed`），除非注册了 `OnWindowAllClosed`。SIGINT/SIGTERM 同路径，第二次信号强退。
- **托盘常驻**：`mygo.NewTray(TrayOptions{Icon, IconIsTemplate, ToolTip, Menu, ID})` + `OnClick/OnRightClick/PopUpMenu/SetTitle/Destroy`；macOS 用 `App.SetActivationPolicy(mygo.ActivationPolicyAccessory)` 去 Dock 图标。

```go
var quitting bool                                     // OnBeforeQuit / OnClose 都在主线程
mygo.App.OnBeforeQuit(func(*mygo.QuitEvent) { quitting = true })
win.OnClose(func(e *mygo.CloseEvent) {
	if !quitting { e.PreventDefault(); win.Hide() }   // 关闭=隐藏，保留 view 状态与后台任务
})
tray.OnClick(win.Show)
```

`examples/menubar-native/main.go` 是完整范例（Frameless+AlwaysOnTop+SkipTaskbar+Hidden 面板、OnBlur 隐藏、OnWillQuit 里 `tray.Destroy()`）。**硬约束**（`docs/menus.md`）：“A tray icon does not keep the app running: the app quits when its last window closes.”。常驻只有两法：(a) close→hide（推荐，状态不丢）；(b) 注册空 `OnWindowAllClosed`（窗口真销毁，重开需重建）。Linux 托盘走 AppIndicator，需 `libayatana-appindicator3`，仅有菜单，无库时 `NewTray` 返回 error 要降级。

## 3. 原生 UI 编程模型

### 3.1 view 与状态

`ui.View(func(c *ui.Context))` → `*ui.Content` → `WindowOptions.Content`。主线程在需要新帧时重跑整棵树：输入后、`Window.Update/Invalidate` 后、动画中、`c.After` 到期。一帧内可能重建**最多 3 次**（handler 改了状态时），所以「动作+同帧可见结果」成立。

```go
type counter struct{ n int }

func (s *counter) view(c *ui.Context) {                 // 每帧重跑，主线程
	if c.Shortcut(0, ui.KeyUp) { s.n++ }                // 轮询快捷键
	ui.Column(c).Fill().Center().Gap(16).Children(func() {
		ui.Textf(c, "%d", s.n).FontSize(56).Bold()
		if ui.PrimaryButton(c, "Increment").Clicked() { s.n++ } // 轮询式点击
	})
}
```

- **模型状态**（数据、选中、草稿、`ListState`、`ui.Router`、`editor.State`、`markdown.Options`）放应用结构体，构造一次，传方法值。
- **控件内部状态**（焦点、滚动、编辑中文本、动画、折叠）由 mygo 按「兄弟位置或 Key」保存，不用你管。
- **绝不能存进 app 状态**：`ui.Element`（pass 后失效，dev/Tester panic、production 静默失效）、`*ui.Context`（仅调用内有效）、`ui.Local` 指针。跨帧引用控件用 `ui.Handle`（别名 `Ref`）：`el.Bind(&h)`、`h.Focus()`、`h.OnShortcut(...)`、`h.Bounds(c)`；`go tool mygo vet` 会抓「build 值存进 struct/包变量/goroutine」。`c.Key(x)` 给控件/容器按集合 key 绑定身份，同级唯一，比较同 map key（指针按地址）；集合增删时焦点/滚动/编辑状态跟着条目走，虚拟列表用 `ListState.Key`。

### 3.2 事件：轮询 + 回调

```go
if ui.Button(c, "Save").Clicked() { app.save() }          // 内联轮询，可配 return/break/continue
ui.Button(c, "Save").OnClick(app.save)                    // 回调：构建结束、绑定输入生效后执行
if ui.TextInput(c, &app.query).Changed() { app.filter() } // 控件写穿指针，下一句即读到新值
if ui.SearchField(c, &app.q).Submitted() { app.search() } // Enter
```

`OnClick/OnChange/OnSubmit` 用于「修改正在遍历的集合」；控件在构造返回前已写入用户输入（ImGui 血统的关键约定）。

### 3.3 一帧的代价（决定架构）

`docs/ui/performance.md`（M5 macOS，CPU 光栅）：1000 静态 label **397 µs / 0 alloc**；百万行列表的可见行 **14.5 µs / 20 alloc**；50 KB Markdown 整篇 1.3 ms，流式 0.8 ms。→ 普通面板帧亚毫秒；**几万条数据必须走 `ui.List`/`ui.Table` builder 只构建可见行**，绝不可全量塞进一帧。窗口空闲不画帧（frame-on-demand，无轮询、无定时器，counter 空转 ~0.005% 核）。

## 4. 主线程模型：调度器与重绘入口

`loop.go` 三件套 **均未导出**：`postMain(fn) bool`（FIFO+`Backend.Signal`）、`onMain(fn)`（主线程直接跑，否则 post+等待）、`await(ch)`（主线程上 `Backend.Step` 泵事件避免死锁）。公开保证是「`package mygo` 的每个函数/方法都可从任意 goroutine 调用」；唯一导出的显式调度器是 `mygo.RunOnMain(fn)`（同步等待，给原生 API 用）。

**`ui` 包没有公开 Post/Async**（`host.post` 是内部接口）。应用的两个入口在 `content.go`：

```go
// Invalidate：任意 goroutine 安全；CAS 合并成最多一帧；只重绘不执行你的代码
func (w *Window) Invalidate() {
	if w.content == nil || !w.invalidating.CompareAndSwap(false, true) { return }
	postMain(func() { w.invalidating.Store(false); w.contentChanged() })
}
// Update：主线程执行 fn 后重绘；从主线程调用也是异步排队，不内联
func (w *Window) Update(fn func()) { postMain(func() { fn(); w.contentChanged() }) }
```

| 需求 | API | goroutine 安全 | 执行你的代码 | 合并 |
|---|---|---|---|---|
| 改状态+重绘 | `win.Update(fn)` | ✅ | ✅ 主线程 | ❌ 每次一个闭包（自己节流） |
| 只重绘 | `win.Invalidate()` | ✅ | ❌ | ✅ 一帧一次 |
| view 内下一帧/延时/动画 | `c.Invalidate()` / `c.After(d)` / `c.AnimationFrame()` | 仅 view 内 | — | ✅ |
| view 外重绘句柄 | `svc := c.Services()` 存下，`svc.Invalidate()` | ✅（只重绘） | ❌ | ✅ |
| 同步跑主线程 | `mygo.RunOnMain(fn)` | ✅（等待） | ✅ | ❌ |

`ui/services.go` 原文即官方裁定：`// Services ... Clipboard and URL methods run on the UI thread; Invalidate may run on any goroutine. Models are still updated through Window.Update.`

**阻塞后果**：主线程同时是事件循环、handler、view、布局、绘制、`Dialog` 模态循环；任何慢点 = 全窗口冻结（`Update` 队列也延后）。`await` 会在主线程泵事件，故 `Dialog/Eval` 从主线程调用不死锁但会暂停该帧。诊断：`MYGO_FRAME_STATS=1` 打印 >8 ms 帧的 build/layout/paint/present 分解。**死锁雷区**：持自己的锁调用任何等待主线程的 API（`Clipboard/Dialog/Theme.IsDark/Screen/RunOnMain`）——主线程在 view 里等锁。规则：锁内只做纯 Go 事；跨线程交给非阻塞的 `Window.Update`。view 里弹系统对话框要 `go mygo.Dialog.Message(...)`（见 gallery/permissionflow），否则模态循环吃掉当前帧。

## 5. 专节：后台任务 → UI 更新的正确模式（可直接抄）

### 模式 A（多数状态首选）：单消费者 goroutine + `Window.Update`

UI 可见状态只在主线程写，后台只投递事件，天然无锁。

```go
type app struct {
	win   *mygo.Window
	ev    chan event      // 后台 → 主线程唯一通道（缓冲，满了丢中间态）
	state state           // 只在主线程读写，view 直接读，无需加锁
	list  ui.ListState
}

func (a *app) start(ctx context.Context) {
	a.ev = make(chan event, 256)
	go func() {                                  // 唯一消费者
		for {
			select {
			case <-ctx.Done(): return
			case e := <-a.ev: a.win.Update(func() { a.apply(e) })  // 主线程：改状态+重绘
			}
		}
	}()
}
func (a *app) notify(e event) { select { case a.ev <- e: default: } }  // 任意生产者：丢了也不堆积闭包
```

### 模式 B（高频/大数据量首选）：不可变快照 + `atomic.Pointer` + `Invalidate`

几万条 Trigger/Ticket 的轮询不要「一条一 Update」，让后台构建**不可变快照**，原子换指针，只请求一次重绘。

```go
type Snapshot struct {                            // 构建后永不修改
	Triggers []Trigger
	Tickets  []Ticket
	Err      error
}

type model struct {
	snap   atomic.Pointer[Snapshot]               // 后台写、主线程读
	dirty  atomic.Bool                            // 同帧最多一次重绘
	win    *mygo.Window
	list   ui.ListState                           // 以下仅主线程
	filter string
	sel    int
}

func (m *model) publish(s *Snapshot) {            // 任意 goroutine
	m.snap.Store(s)
	if m.dirty.CompareAndSwap(false, true) { m.win.Invalidate() }
}

func (m *model) view(c *ui.Context) {
	m.dirty.Store(false)                          // 先放开，允许下一帧再请求
	s := m.snap.Load()                            // 一帧一次读取
	if s == nil { ui.Spinner(c); return }
	rows := filterTriggers(s.Triggers, m.filter)  // 过滤=纯函数
	m.list.Key = func(i int) any { return rows[i].ID }
	m.list.Selected = &m.sel
	ui.List(c, &m.list, len(rows), func(i int) {
		ui.Row(c).Gap(8).Padding(6, 12).Children(func() {
			ui.Text(c, rows[i].Name).Grow(1)
			ui.Badge(c, rows[i].State)
		})
	}).Grow(1)
}
```

（一帧可重建多次、`view` 会重复进入，`snap.Load()` 每次重读；监控面板可接受（下一帧自愈），需严格一致就缓存到帧级字段复用。）

### 模式 C：节流（token/日志/进度流）

```go
var mu sync.Mutex
var buf strings.Builder
go func() {                                    // 生产者只 append，快
	for { n, err := r.Read(b); if n > 0 { mu.Lock(); buf.Write(b[:n]); mu.Unlock() }; if err != nil { return } }
}()

t := time.NewTicker(33 * time.Millisecond)     // ≤30 fps
defer t.Stop()
for {
	select {
	case <-ctx.Done(): return
	case <-t.C:
		mu.Lock(); s := buf.String(); mu.Unlock()
		m.win.Update(func() { m.reply = s })   // 每 33ms 一次，而非每 token
	}
}
```

（`examples/markdown-chat` 是「每 token 一次 Update」的简单版；真实 LLM 流建议按上法节流，配合 `markdown.Stream` 的 `Reveal` 匀速显示。）

### 模式 D：定时轮询 + 退出

```go
ctx, cancel := context.WithCancel(context.Background())
mygo.App.OnBeforeQuit(func(*mygo.QuitEvent) { cancel() })   // 全局退出信号
go func() {
	t := time.NewTicker(2 * time.Second); defer t.Stop()
	for {
		select {
		case <-ctx.Done(): return
		case <-t.C:
			if s, err := pollAll(ctx); err == nil { m.publish(s) } // 慢活全在后台
		}
	}
}()
```

**框架没有内置定时器/调度器**：`c.After`/`Painter.After` 只在 view 内表达「下一帧何时来」；周期任务就是 `time.Ticker`。退出后 `postMain` 丢弃投递（`loop.closed`），不会 panic。

### 反例（会出事）

```go
func (a *app) view(c *ui.Context) {
	rows, _ := db.Query("...")            // ✗ 阻塞主线程 = 卡死
	go func() { a.rows = rows }()         // ✗ 数据竞争 + 没人请求下一帧
	a.btn = ui.Button(c, "OK")            // ✗ Element 本次 pass 后失效
	a.ctx = c                             // ✗ Context 只在本次调用内有效
}
a.mu.Lock(); mygo.Clipboard.ReadText(); a.mu.Unlock()  // ✗ 持锁等主线程 → 死锁
```

## 6. 专节：控件映射表（本项目 UI → mygo）

| 需要 | mygo 控件 / 包 | 够用 | 缺口 |
|---|---|---|---|
| 主框架 6 大区 / 侧边栏 | `ui.Sidebar`+`SidebarSection`+`SidebarItem`+`Badge`+`ui.Router`（或 `ui.Tabs(c,&idx,...)`） | ✅ | Tabs 是标签行+索引，不可关闭/拖拽；侧边栏式导航更贴合 6 区，折叠/选中/键盘/无障碍齐全 |
| 概览卡片 | `ui.Box/Column/Row`+`Background/Radius/Border/Shadow` | ✅自建 | **无 Card 组件**，样式自写（<10 行） |
| Source 树/文件 | `ui.Tree`（小）、`ui.Outline`/`OutlineTable`（虚拟化） | ✅ | 图标 `ui.Icon`（SVG，Lucide/Heroicons 直接用） |
| Trigger/Thread 列表 | `ui.List`+`ListState`（Key/Selected/Selection/FollowEnd/Header/Label/DragSelect/Reorder/Visible/AtEnd/ScrollTo） | ✅ | 百万行级虚拟化；分组用 `Header`，过滤/排序自己写纯函数 |
| Ticket 表格 | `ui.Table`+`[]TableColumn`+`ListState`（列宽拖拽/换位/排序/SortBy/固定列/CellSelect/行头/行内 `EditableText`） | ✅✅ | 排序只给状态，真正排序由 app 做；无内建过滤/分组/分页/聚合 |
| Eval/决策输出 | `ui/markdown.View`/`Stream`（CommonMark+GFM 表格/任务列表/脚注/数学，增量解析） | ✅✅ | 杀手锏，专为 LLM 回复设计 |
| 代码/diff 视图 | `ui/editor.View`（Zed 风格）+ `editor.DiffView`（并排/统一、词级高亮、折叠未变行、可编辑） | ✅✅ | LSP 自接：`OnCompletion/OnHover/SetDiagnostics/SetInlays` |
| 搜索框/全局搜索 | `ui.SearchField`+`c.Shortcut(ui.Cmd,ui.KeyK)` | ✅ | **无命令面板**：`ui.Modal`+SearchField+List 自建（~60 行） |
| 详情抽屉 | 无 Drawer/Sheet | ⚠️ | 用 `ui.Modal`，或 `ui.Overlay`+`Absolute().Right(0).Top(0).Bottom(0)` 自建（有 `Transition`/`Motion` 动效） |
| 保存历史 | `ui.List`+`ListState`+`Collapsible`/`Accordion` | ✅ | 持久化自己选（SQLite / `App.PathUserData` 下 JSON） |
| 确认/自定义对话框 | `ui.AlertDialog`（返回按钮索引，Enter=默认、Escape=取消、点外不关）；`ui.Modal`/`DialogBase`（焦点陷阱、背景 inert） | ✅✅ | — |
| Toast | `c.Toast`、`c.ToastAction`、`c.AddToast(ui.Toast{...})`、`c.CloseToast` | ✅✅ | 3 条堆叠、悬停暂停、读屏播报 |
| 虚拟列表 | `ui.List(c,&st,n,row)` / `list.Rows(func(ui.ListRow))` | ✅✅ | — |
| Tabs / Split | `ui.Tabs`/`TabsBase`；`ui.Split`/`SplitVertical`/`SplitPercent`/`SplitVerticalPercent`（拖拽+键盘+Changed） | ✅✅ | Split 尺寸放 app 状态并持久化 |
| Form/Settings | `ui.Form`+`Field`（label/description/error）+`Fieldset`；`Switch/Select/TextInput/NumberInput/Slider/Checkbox/Radio/Combobox/Autocomplete/TokenField` | ✅✅ | 校验自己写（`Field.Error` 只展示+标红） |
| Markdown | `ui/markdown` `View`/`Stream`/`Options{Reveal}` | ✅✅ | — |
| Chart | `ui/chart`：`NewLinear/NewBand/NewTime`、`Ticks`、`Plot/Axes`、`Rect/Line/Area/Pie/Sector/RuleX/RuleY`、`Stack`、`Select` | ✅低层 | 无折线/柱状高层封装、无 legend，自己拼 |
| Badge/Tooltip/Collapsible/Tree | `ui.Badge`、`el.Tooltip(...)`、`ui.Collapsible`/`Accordion`、`ui.Tree`/`Outline` | ✅ | — |
| 右键菜单/工具条 | `el.ContextMenu(func(m *ui.Menu){...})`、`ui.Toolbar`（自动 overflow） | ✅ | — |
| 进度/加载/图标/图片 | `Progress`、`Spinner`、`Meter`、`plugins/loading`、`Icon`(SVG)、`Image`/`Avatar` | ✅ | — |
| 快捷键 | `c.Shortcut`、`el.OnShortcut`、`Handle.OnShortcut`、菜单 `Accelerator` | ✅ | 无全局命令注册表，自建 |
| 无障碍 | 全控件 `platform.AccessTree`（AX/ATK/UIA）+ `c.Announce` | ✅ | 自定义控件用 Base + `Access(...)` |

**总缺口**：Card / Drawer / CommandPalette 三个复合件自建；data-grid 过滤/分组/聚合/分页自写；chart 高层自建；无 HTML 渲染（只有 Markdown，或内嵌 `ui.WebView` 走混合，本项目不建议）。

## 7. 主题 / 样式（design tokens 可行性）

可以，但形态不同：**无 CSS/选择器**，`ui.Theme` 是纯 Go 结构体，`c.Theme()` 取当前主题，控件按字段取值；`c.SetTheme(&t)` 换整窗，`ui.Themed(c, t)` 换局部（可嵌 light/dark 预览）。

```go
t := *c.Theme()                        // 默认主题跟随系统
t.Accent, t.Radius = ui.Hex("#7c3aed"), 8
t.Spacing = 3                          // 紧凑：控件内边距/间距/行高按它缩放
c.SetTheme(&t)
```

- token 域（`ui/theme.go`）：`Background/Surface(+Hover/Pressed)/Border/Text/TextMuted/Accent(+Hover/Pressed/Text)/InactiveAccent/Danger/Warning/Success/Selection/Focus/Inverse(+Text)/Scrollbar(+Width)/Radius/Spacing/FontSize/Font/Dark`，辅助 `Space(n)`、`Rem(n)`；样式能力 `Background/Border(+Width/Style)/Radius/Shadow/InsetShadow/Gradient/Linear/Radial/Conic/Stripes/Blur/Cache/Material/Cursor/Opacity/Transform/Transition/Motion`，玻璃拟态有 `plugins/glass`。
- **深浅色跟随系统**：默认跟随；`mygo.Theme.SetSource(system/light/dark)` 强制、`mygo.Theme.OnUpdated` 监听；`c.Theme().Dark`、`ui.LightTheme()/DarkTheme()`。`c.Preferences()` 给 Accent/HighContrast/TextScale/ReduceMotion，默认主题自动跟随，ReduceMotion 下动画直达。
- 落地建议：token 收敛到 `internal/ui/tokens.go`，view 顶部只取一次 `t := c.Theme()`；统一走 `t.Space()/t.Rem()`，自动跟随系统 TextScale。

## 8. 测试（不打开窗口）

```go
func TestCounter(t *testing.T) {
	s := &counter{}
	tt := ui.NewTester(s.view, 360, 260)      // 纯内存，无窗口、无显示器
	if err := tt.Click("Increment"); err != nil { t.Fatal(err) }
	tt.Key(0, ui.KeyUp)
	if s.n != 2 || !tt.HasText("2") { t.Fatalf("n=%d texts=%q", s.n, tt.Texts()) }
}
```

- **能测**：指针（`Click/ClickAt/ClickWith(mods)/Press/Move/Release/Scroll/Wheel/RightClick`）、键盘（`Key/TypeKey/Type/Compose/Command/TextCaret`）、菜单（`tt.Menu()`、`ChooseMenuItem("Move to","Archive")`）、断言（`Texts/HasText/Find/Focused/Cursor/Clipboard/OpenedURLs/Announcements`）、快照（`tt.Image() *image.RGBA`、`ui.Render(view,w,h,scale)`）、帧与虚拟时钟（`tt.Frame/FramePending/Advance`，动画/`c.After`/双击/tooltip/toast 全按它走，禁止 `Sleep`）、窗口态（`SetDark/SetPreferences/SetDirection/SetSize/SetZoom/SetWindowActive`）。
- **限制**：只测 view，测不到 `Window.Update`（无 window）、真实事件循环、多线程时序；每次输入最多跑 20 帧；重复 Key 直接 panic（有用的断言）；无内建像素 diff。
- **整 app E2E**：`plugins/automation`（`MYGO_AUTOMATION=/tmp/app.sock ./app`，newline-JSON over Unix socket：列窗口、读元素、输入、截图、等稳定），需真窗口+显示器，`mygo build`/`mygo_noinspector` 会剔除，适合 Xvfb CI。落地建议：过滤/排序/归约写成纯函数单测；view 用 Tester 做「切 tab / 开详情 / 确认删除 / toast」；像素快照只覆盖少量稳定页。

## 9. 存储：SQLite 可用（CGO_ENABLED=0）

```go
import "github.com/egoist/mygo/plugins/sqlite"

db, err := sqlite.Open(ctx, "zeokeel.db", sqlite.OpenOptions{})
defer db.Close()
_, err = db.Execute(ctx, "CREATE TABLE IF NOT EXISTS triggers(id TEXT PRIMARY KEY, state TEXT)")
rows, err := db.Query(ctx, "SELECT id, state FROM triggers WHERE state = ?", "open") // rows.Columns / rows.Values
_, err = db.Transaction(ctx, []sqlite.Statement{
	{SQL: "INSERT INTO triggers VALUES(?,?)", Args: []any{"t1", "open"}},
	{SQL: "UPDATE tickets SET state=? WHERE id=?", Args: []any{"done", "k1"}},
})
```

实现：`plugins/sqlite/native.go` 用 `purego.Dlopen`+`SyscallN` 调 SQLite C API；库由 Zig 编译后放 GitHub Release；`go.mod` 无 cgo、无 modernc；`DB` 自带 gate 互斥 + context 取消，默认开外键、`BusyTimeout` 5s。**约束**（`docs/plugins/sqlite.md`）：`mygo dev/build` 按 `mygo-plugin.json` 把库放进 app 资源（macOS 一起签名）；裸 `go run`/`go test` 会**联网**下载并校验 SHA-256，离线用 `MYGO_SQLITE_LIBRARY`——**建议 vendor 库进流水线**。每 `Execute` 只能一条语句（批量用 `Transaction`，`BEGIN IMMEDIATE`）；禁 `ATTACH/DETACH/VACUUM INTO`、禁事务控制语句、禁扩展；path 只能落在 `Options.Directory`（默认 `mygo.PathUserData`）下，拒绝绝对路径/`..`/符号链接/Windows 设备名，`:memory:` 可用；类型 NULL/INTEGER/REAL/TEXT/BLOB ↔ nil/int64/float64/string/[]byte。**替代 `modernc.org/sqlite`**：纯 Go、同样 `CGO_ENABLED=0`，无原生库与下载，代价是二进制更大、略慢、不走 `mygo build` 资源打包——要零构建摩擦选它，要与页面插件共用库选官方插件。其它持久化：`App.PathUserData` 写 JSON；`mygo.WatchFiles` 后台监视 agent 产物目录（自带 goroutine，不占主线程）。

## 10. 长任务 / 后台：框架给什么

`mygo.Channel[T]`（`channel.go`）、`NewEvent[T]`/`Event[T]`（`ipc.go`/`events.go`）、`Session`（`session.go`）**只服务 webview IPC/数据分区，原生 UI 用不上**。可用的只有：`FileWatcher`（`watch.go`，后台 goroutine + 平台 watcher）、`audio` 包（独立线程解码）、`c.After`/`Painter.After`（仅 view 内「下一帧」）。**定时器 / worker pool / 事件总线不存在**，就用标准库 goroutine+channel+Ticker。结论：原生 UI 无内置异步框架，跨线程通信由 app 负责，框架只给「任意 goroutine 可调、主线程执行」的 `Window.Update/Invalidate` 两个安全点。

## 11. 平台与构建

- **渲染后端**：macOS Metal、Windows D3D11（无 GPU 用 WARP）、Linux OpenGL（`GtkGLArea`）、浏览器 WebGL 2；均可退 CPU 光栅（`MYGO_GPU=0`，`internal/raster`）。Linux 无 GL 时自动 `GtkDrawingArea` 软绘。
- **Linux 前提**：GTK3（dlopen：glib/gobject/gio/gdk/gtk3/cairo/gdkpixbuf，可选 AppIndicator；WebKitGTK 仅页面窗口需要）+ X11/Wayland；`internal/linux/app.go` 在 `gtk_init_check` 失败返回 `mygo: cannot open display (is DISPLAY or WAYLAND_DISPLAY set?)` → **无显示器起不了真窗口**，CI 用 Xvfb（仓库自己的 GUI 测试就这样）。headless 只有 `ui.Tester`（不需要任何后端，纯 Go 光栅），**整个 `App.Run` 不行**（`internal/fake` 只供 mygo 自身单测）。
- **WebAssembly**：`GOOS=js GOARCH=wasm` 可跑 native UI（`mygo dev -web`、`mygo build -platform web` 产出静态站点）。**只支持 native UI 窗口**：页面窗口、应用/托盘菜单、通知、全局快捷键、文件选择框、数据拖放不可用，无障碍未实现，窗口尺寸/位置由页面决定。可作演示加分项，非分发主路径。
- **分发与体积**：`mygo build` → macOS `.app`+`.dmg`（签名/公证）、Windows `.exe`+NSIS、Linux 可执行+`.desktop`+`.deb`+PKGBUILD+`install.sh`；`plugins/updater` 提供 Sparkle 式增量更新（含原生更新窗）。release 8.9 MB（counter-native）、~7 MB（hello）、idle 22–37 MB；加 editor/markdown/chart/sqlite 估 12–16 MB、+20–40 MB 内存（需实测）。Linux 首开 GPU 窗口加载 Mesa 约 +50 MB，`MYGO_GPU=0` 可避。

## 12. 专节：不可行 / 需要绕开的点

1. **不能在任何 goroutine 直接改 UI 可见状态**：无自动重绘/响应式；必须 `Window.Update` 或「快照+`Invalidate`」。忘了请求帧 → 数据变了界面不动。
2. **不能保存 `*ui.Context`/`ui.Element` 到 app 状态或 goroutine**：dev/Tester panic、production 静默失效；跨帧只能 `ui.Handle`，`go tool mygo vet` 兜底。
3. **不能在主线程阻塞**：没有后台渲染线程；重计算、同步网络/DB、`os/exec` 等待、`Sleep` 出现在 view/handler = 全窗口卡死；`Dialog` 等模态必须 `go`。
4. **不能持锁调用阻塞式 mygo API**（`Clipboard/Dialog/Theme.IsDark/Screen/RunOnMain`）→ 死锁；锁内只碰纯 Go 数据。
5. **没有内置状态容器**：`ui/ref.go` 的 `Ref` 只是 `Handle` 别名（控件身份），`ui/preferences.go` 是系统偏好，`ui/services.go` 是重绘/剪贴板服务；store/observable/selector 自建（模式 B）。
6. **没有 Drawer / Command Palette / Card / Data-grid 复合组件**：用 `Modal/Overlay/Split/Box/List/Table` 自建，别指望开箱。
7. **没有 HTML/CSS/Web 组件**：不能复用 web 前端；富内容只有 Markdown 或内嵌 `ui.WebView`（混合窗口可行，但本项目既选 native UI 就别引入 webview 依赖）。
8. **表格无内建过滤/分组/分页/聚合**：`Table` 只管虚拟化行、列宽/列序/排序状态；过滤排序必须是纯函数，否则每帧全量排序烧 CPU。
9. **托盘不保活**：最后窗口关闭即退出；必须 close→hide 或空 `OnWindowAllClosed`。Linux 托盘依赖 AppIndicator，可能没有点击回调。
10. **无显示器 = 无 GUI**：Linux 真窗口需 X11/Wayland（CI 用 Xvfb），headless 只能 `ui.Tester`。
11. **API 不稳定（0.x）**：已发生 checked-elements 破坏性迁移；升级先跑 `go tool mygo migrate-ui`，用法收进 `internal/ui` 适配层。
12. **外围要自己接**：代码编辑器只给高亮/补全/hover/diagnostics/inlay 接口（LSP 客户端自接）；SQLite 库分发要自己解决（打包能带、裸跑会联网下载；禁多语句/ATTACH/扩展，不想管就用 modernc）。
14. **无全局快捷键注册表/命令系统**：`c.Shortcut`、`Handle.OnShortcut`、菜单 `Accelerator` 分散，需自建「命令 ID → 执行+可用性+快捷键」中间层。

## 13. 落地方案骨架（6 大区 + 全局搜索 + 详情抽屉）

```go
type app struct {
	win   *mygo.Window
	snap  atomic.Pointer[Snapshot]   // 后台写、主线程读（§5 模式 B）
	dirty atomic.Bool
	route *ui.Router                 // 每窗口一个，页面即 6 大区
	list  ui.ListState
	query string
	detail, confirm, searchOpen bool  // 详情抽屉/确认框/命令面板
}

func (a *app) view(c *ui.Context) {
	a.dirty.Store(false)
	if c.Shortcut(ui.Cmd, ui.KeyK) { a.searchOpen = true }   // 全局搜索
	ui.Row(c).Fill().AlignItems(ui.Stretch).Children(func() {
		a.sidebar(c)                                         // Overview/Source/Trigger/Eval/Execution/Settings
		ui.Column(c).Grow(1).MinWidth(0).Children(func() {
			a.toolbar(c)
			a.route.View(c, func(r *ui.Route) {
				switch { case r.Match("/overview"): a.overview(c) /* … */ }
				r.Title("ZeoKeel")
			})
		})
	})
	a.drawer(c)                                              // Overlay+Absolute 自建详情抽屉
	ui.AlertDialog(c, &a.confirm, "确认执行？", msg, "取消", "执行")
}
```

分层（把 §12 风险收口）：

```
cmd/zeokeel/main.go       // 生命周期、单实例、托盘、WhenReady
internal/ui/              // 唯一 import mygo/ui 的地方（隔离 0.x 升级）；tokens.go、widgets/（Card/Drawer/CommandPalette）
internal/app/snapshot.go  // 不可变 Snapshot + publish/repaint（§5 模式 B）
internal/core/            // 纯 Go：轮询、agent 执行、SQLite、归约（不开 UI，可单测）
```

**架构裁定**：`internal/core` 全在后台 goroutine 跑并产出不可变快照；`internal/ui` 只读快照、只写 UI 状态；两者之间只有 `atomic.Pointer[Snapshot]` + `Window.Invalidate()`（高频）或 `Window.Update(fn)`（低频/需主线程语义）。守住这条边界，mygo 的主线程模型、测试模型、性能模型就都是你的朋友。

## 14. 参考索引

- 生命周期/线程：`docs/app.md`、`docs/architecture.md`（Threading model 183–242）、`app.go`、`loop.go`、`content.go:153/173`；视图/服务：`docs/ui/views.md`（"Change the state from other goroutines"）、`ui/doc.go`、`ui/context.go`、`ui/services.go`、`ui/ref.go`；性能/渲染：`docs/ui/performance.md`、`docs/ui/rendering.md`
- 控件：`docs/ui/{tabs,sidebar,table,list,split,form,markdown,code-editor,diff-viewer,chart,toast,alert-dialog,dialog,search-field,tree,outline,collapsible,badge,tooltip,overlays}.md`；示例：`examples/{counter-native,gallery,markdown-chat,menubar-native,code-editor,chart-streaming}`；存储/平台/稳定性：`plugins/sqlite/*`、`docs/plugins/{sqlite,automation}.md`、`internal/linux/app.go`、`docs/distribution.md`、`docs/ui/migration.md`、`AGENTS.md`
