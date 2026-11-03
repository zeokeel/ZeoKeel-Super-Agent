# UX / 视觉 / 交互 / 无障碍评审（Round 3 面板）

- 日期：2026-11-04（Asia/Shanghai）· 只读评审，未改代码。对象：`internal/ui/**` + `/tmp/r2-*.png`（22:54）、`/tmp/r3-*.png`（22:59）、`/tmp/r4-*.png`（00:44）+ mygo v0.7.4 API。
- 方法：逐张看截图（必要时 3× 放大 `/tmp/crops/exec-glyphs.png`）+ 读渲染代码 + 算 WCAG 对比度（sRGB 相对亮度）。无显示器、无真人测试；"会卡/过小"来自静态判断。
- 标准：macOS HIG、Windows Fluent、Material 3、WCAG 2.2（AA）。严重度 S1=发布前必修 / S2=高 / S3=中 / S4=低。

## 总评

结构（超级Agent + 工作区树 + 底部监控）与"禁用/未投影就明说"的诚实状态是强项，保留。但面板按"网页"而非"桌面应用"的方式做交互：可点的行/标签**不在 Tab 序列、屏幕阅读器读作 group**（I1），系统字号/高对比/减少动态被忽略（I2），低对比的 10–12px 文本与 1.2:1 的边框大量出现（V4），执行行图标实际不可辨（V1），窗口状态不记忆、最小尺寸硬钳（I3）。以下 30 条按标准逐项给出证据与改法。

## 发现

| ID | 严重度 | 领域 | 证据（文件:行 / 截图） | 标准 | 影响 | 建议 |
| --- | --- | --- | --- | --- | --- | --- |
| V1 | S2 | 图标可读性 | `widgets.go:49-67`：26px 圆角块内图标 13px、描边 1.8（≈1px）；`/tmp/r4-execution-filter.png` 左侧行首（放大见 `/tmp/crops/exec-glyphs.png`）像 `{?}`/杂点 | HIG icon clarity；WCAG 1.4.11 | 用户无法一眼分辨来源，行首装饰化 | 图标 16–18px、描边 1.5–1.75、去掉 1px 内嵌渐变边框；或改 16px 无底图标与标题同基线 |
| V2 | S3 | 图标一致性 | `icons.go:8-`：描边 1.8/2.0/2.2 混用，`icSpark` 是 fill；同一 spark 既是"超级Agent"又是"飞书"（`widgets.go:55`），bell 同时是"钉钉"与"注意"（`channels.go`） | HIG/Fluent iconography；WCAG 1.1.1（含义） | 语义漂移，品牌符号被稀释 | 一套 24 网格、统一 1.75 描边、20/16/12 三档；Feishu/钉钉用不同字形或首字母徽标；spark 只给超级Agent |
| V3 | S2 | 字体层级 | 实际字号 10,11,12,13,14,15,16,17,19,20,26,28 共 14 档（`grep fs(` 统计：12×133、13×94、14×61、15×33）；中文正文大量 12–13px；分区标题与行标签同 15px 仅靠加粗（`/tmp/r2-settings-deploy.png` 运维面/运行环境/健康） | HIG/M3 type scale；WCAG 1.4.4 | 层级弱、小字难读，中文尤其紧 | 收敛 4 档：12（辅助）/13（元数据）/15（正文）/20（标题）+26 展示；分区标题 13→15 加字距/色阶区分；中文正文不低于 14 |
| V4 | S1 | 对比度 | `tokens.go:139-140,108-109`：TextFaint `#98A2B3` 亮 2.38–2.58:1、`#6B7891` 暗 3.61–4.30:1；边框 `#E4E7EC`/`#26314A` 1.13–1.48:1；暗色 accent-soft 上文字 3.48:1、白字在 `#4D74FF` 3.99:1、亮色 cyan 2.81、绿/红在 soft 底 4.4/4.2（截图：状态栏、时间戳、"暂无数据"、表头、输入框边） | WCAG 1.4.3（4.5）/1.4.11（3:1） | 低视力用户读不到提示、时间、边框；状态与输入边界不可辨 | TextFaint→亮 `#5B6472`（≈5.9）、暗 `#94A0B8`（≈6.6）；边框亮 `#C9D0DA`、暗 `#39465F`（≥3:1）；soft 底文字用 accentHover 等深一档；主按钮白字对比不足时改深一档底 |
| V5 | S2 | 截断无提示 | `internal/ui` 中 `Tooltip` 使用 0 次；`SingleLine` 大量；`/tmp/r4-workspace-actions.png` "#48 把登录超时改成可…"、执行行标题、状态栏 | HIG truncation；WCAG 1.4.1（信息丢失） | 长名称/标题信息不可得 | 所有 SingleLine 名称/标题加 `Tooltip`（mygo 已支持）；或允许 2 行；行悬停显示完整文本 |
| V6 | S2 | 主框架调试文本 | `chrome.go:139-156`：状态栏常驻 `v1.0.0-dev · rev 10 · Go 1.27.3 · CGO=0 · SQLite 12.4 MB`，10px TextFaint；截图 r2/r4 右下 | HIG window chrome；Fluent status bar | 开发信息占据用户视野、暴露实现细节 | 移到 About（默认菜单）与 设置›部署；状态栏只留"帮助 ⌘/ · 需要你处理 N" |
| V7 | S2 | 交互态缺失 | 全 `internal/ui` 无 `Hovered()` 调用（grep 0）；行/标签均为 `Row.OnClick`，无 hover/pressed；仅选中态（r4 蓝色行） | HIG/Fluent/M3 states；WCAG 2.4.7（可见焦点） | 鼠标不知哪里可点，误点/不敢点 | 可点行/标签统一加 hover（SurfaceHover）与 pressed（SurfacePressed）；可点文字下划线或右侧 chevron；与选中态区分 |
| V8 | S3 | 密度/节奏 | 执行行 46px 但行首块 26px（`execution.go:112-140`）；评估行 ~59px；设置行 28px；chip/按钮交界 22–24px；工作区页头 20/15/13 混排 + 24px 按钮（r4） | HIG spacing/M3 density | 松散与拥挤并存，视线跳 | 定 4/8/12/16 间距阶；列表行 40（紧凑）/44（舒适）；同区控件同高（28 或 32） |
| V9 | S3 | 数据排版 | `/tmp/r2-settings-deploy.png`：`<stateDir>/logs/...`、`zeekeele backup → ...` 用正文字体右对齐，非等宽；长值无换行策略 | HIG data display | 路径/命令难辨认，小窗易撞左列 | 路径/命令/ID 一律 mono（已有 `mono()`）；右列允许 2 行；窄窗左列 120→100 |
| I1 | S1 | 键盘/AT | `actions.go:26` 只置 `flagClickable`；`mygo ui/access.go:174-178` 需 clickable+focusable 才是 button，否则 label 存在也只是 group；`internal/ui` 无 `.Focusable()/.Role()/.FocusRing()`（grep 0） | WCAG 2.1.1 / 2.4.3 / 4.1.2 | Tab 跳过侧栏工作区/会话、执行行、筛选 chip、状态栏"帮助"；屏幕阅读器无法激活 | 行/标签用 `mui.Button`（透明样式）或加 `.Focusable().Role(RoleButton).FocusRing(true)` + Enter/Space；表用 `RoleTable/Row/Cell`，侧栏树用 `RoleTree/TreeItem` |
| I2 | S2 | 系统偏好被忽略 | `app.go:375` 只用 `c.Theme().Dark`；mygo `modules.go:326-351` 的 `TextScale/HighContrast/ReduceMotion/Accent` 未读（grep `Preferences()` 0）；`stream.go:59` 恒定 `Smooth` 动画 | WCAG 1.4.4 / 2.3.3；HIG accessibility | 系统放大字号/高对比/减少动态全不生效；品牌 accent 覆盖系统色 | scale = clamp(OS TextScale × ui.font_scale)；高对比 token 变体；ReduceMotion 时关 Smooth；Accent 默认沿用系统、品牌色只做强调 |
| I3 | S2 | 窗口约定 | `app.go:278-286` 未传 `MinWidth/MinHeight`（mygo `window.go:79`），仅在 Run 里钳 1100×720；未用 `StateKey`/`SetWindowStateStore`（`window.go:192-235`）；无 X/Y 记忆 | HIG window sizing；Fluent restore | 1024/1366 小屏被撑出屏幕；用户缩小后布局崩；每次启动位置/大小丢失 | 传 MinWidth/MinHeight（如 1000×640）+ StateKey/状态存储；<1000 时侧栏可折叠；首次按屏幕 80% 居中 |
| I4 | S2 | 语言一致性 | 中文界面但 mygo 内置串为英文（搜索面板 "Clear"；默认菜单 Copy/Paste）；全仓无 `SetLocale`（grep 0） | WCAG 3.1.2；HIG 本地化 | 中英混排，专业感与可读性受损 | 启动 `mygo.App.SetLocale("zh-Hans")`（或 mui 对应 API）；内置控件文案随 locale；键盘键名用平台符号（已有 `⌘K`/`Ctrl+K` ✓） |
| I5 | S2 | 后台反馈 | 关窗=隐藏（`app.go:290-296`）+ 仅托盘（`tray.go:15-45`）；mygo `Notification`（`modules.go:832-914`）未用 | HIG/Fluent notifications | 隐藏后"需你回答/已完成"无提示，用户以为丢了 | 窗口隐藏且 NeedsPerson 时发系统通知，点击打开对应行；每工单去重 |
| I6 | S3 | 菜单/平台身份 | mygo 默认应用菜单存在（`app.go:368-372`）✓，但无应用级命令（新建会话/设置/退出入口）；macOS `Accessory` 无 Dock（`app.go:299-301`）；托盘图标 256px 彩色未设 `IconIsTemplate`（`tray.go:15`，mygo TrayOptions 有该字段） | HIG menu bar / Dock | 菜单里找不到应用命令；菜单栏图标不随主题 | 设应用菜单：新建会话 ⌘N、设置 ⌘,、打开面板、退出；托盘改 16pt 黑白 template 图；评估是否保留无 Dock |
| I7 | S3 | 上下文菜单/拖放 | mygo ContextMenu/OnFileDrop 均未用（grep 0）；行右键无操作；文件夹拖入不填根目录 | HIG/Fluent conventions | 重命名/删除/复制须绕到页面按钮；拖放是自然手势 | 行右键菜单（打开/复制 #N/重命名/删除）；`OnFileDrop` 填新建工作区根目录 |
| I8 | S2 | 加载/空/错/撤销 | 全 ui 无 `Progress/Spinner/Skeleton`（grep 0）；异步仅文字 pending（发送中/创建中/保存中 ✓）；通道连接无进度；无 toast；删除工作区/停止工单不可撤销 | WCAG 2.2.1 不适用，但 HIG feedback | 长操作"只有灰字"；误删无退路 | >1s 操作加 3 点 spinner 或进度行；连通/扫描给阶段文案；删除提供 10s 撤销 toast 或改"归档" |
| I9 | S3 | 快捷键覆盖 | `app.go:389-395` 仅 ⌘K、⌘/；无 ⌘,（设置）、⌘N（新会话）、⌘↩（发送）；Esc 只关弹窗/面板 | HIG/Fluent shortcuts | 键盘用户效率低；发送无替代手势 | 补 ⌘, ⌘N ⌘↩；帮助弹窗列全部快捷键；按钮 tooltip 带快捷键 |
| I10 | S4 | 多窗口 | 单窗口 + 托盘；无独立工作区/工单窗口 | HIG/Fluent windows | 多任务需来回切页 | 可后置；至少支持"在新窗口打开工作区" |
| A1 | S2 | 颜色唯一编码 | 侧栏会话行仅一个琥珀点表示待处理（`workspace.go` conversationRow；r4 截图 #42 行） | WCAG 1.4.1 | 色盲/读屏不知含义 | 点旁加"待你"文字或 `Label("待你处理")`；行可读名含状态 |
| A2 | S2 | 点击目标 | `workspace.go:224` "+" 18×18 无 Label；`prompt.go:99,102,106,196` 按钮 22px；`widgets.go:81` chip 24px | WCAG 2.5.8（≥24）；HIG 28 | 手抖/触控难命中 | 最小 28×28（触控 32）；图标按钮加 Label + Tooltip |
| A3 | S3 | 可读名 | 无 Label 的图标按钮（同 A2 "+"）；行名靠内文拼接可读但无 role；"Clear" 英文（I4） | WCAG 4.1.2 | 读屏听到空按钮/中英混杂 | 所有纯图标控件 `Label`/`Description`；行名用短句（"打开工作区 acme/webapp"） |
| A4 | S3 | 语义结构 | mygo `RoleTable/Row/Cell/Tree/TreeItem` 未被使用（`internal/ui` grep `.Role(` 0） | WCAG 1.3.1 | 读屏无法按表/树导航 | 评估表加 table/row/cell；侧栏工作区树加 tree/treeitem；列表沿用 mui.List |
| A5 | S3 | 焦点可见 | 自定义可点元素不可聚焦（I1）故无焦点环；可聚焦控件未显式 `FocusRing`；选中态与焦点仅色差 | WCAG 2.4.7 | 键盘用户失去位置感 | 统一 2px focus ring（token `focus` 已有）+ 选中行左 2px 条 |
| A6 | S4 | 字号设置 | `tokens.go:44-66` 三档 0.9/1.0/1.1，90% 时 10–11px→9–10px | WCAG 1.4.4 | 小数号不可读 | 至少 0.9/1.0/1.1/1.25，或跟随系统 TextScale（I2） |
| A7 | S4 | 暗色选中行 | 选中行白字在 `#4D74FF`：3.99:1（`tokens.go` accent + 白） | WCAG 1.4.3 | 暗色下选中行标题偏淡 | 暗色选中行用 `#3A5FE0`（≈5.2:1）或白字加 text-shadow/加粗 |
| T1 | S4 | 主题/DPI | 明暗跟随系统 ✓（`app.go:375`）；无高对比主题；logo 256px 位图渲染 24–28DIP 在 3x 略软 | HIG appearance；DPI | 高对比用户无适配；4K 下 logo 边缘略糊 | 高对比 token 变体；logo 用矢量/多尺寸；rest 图标改用 SVG ✓ |
| T2 | S4 | 空/错误文案 | 空态诚实但偏技术："等待运行时发布快照…""等待运行时投影…"（r2/r4） | WCAG 3.3.1（帮助） | 新用户不知下一步 | 空态加一句行动（"先新建工作区 / 让它做一件事"）；技术原因收进"详情" |

## Top 10（按影响排序）

1. **I1 可点行不可聚焦/无按钮语义** — Tab 与读屏都失效，桌面应用底线；先修。
2. **V4 对比度** — TextFaint/边框/soft 底/暗色选中行（1.2–4.4:1），全局 token 一次修。
3. **V1 执行行首图标不可辨** — 截图第一眼就是它，16–18px 统一图标。
4. **V3 字号层级** — 中文正文 12–13px、14 档字号；收敛到 4 档。
5. **I2 系统可达性偏好** — 字号/高对比/减少动态；接 mygo `Theme.Preferences`。
6. **I3 窗口约定** — MinWidth/StateKey/小屏折叠；避免"打开就超出屏幕"。
7. **V7 交互态** — 行/标签 hover/pressed/focus 三态。
8. **V5 截断披露** — 名称/标题 Tooltip，信息不再丢失。
9. **V6 状态栏调试信息** — 移到 About/部署，主框架只留用户信息。
10. **I4 语言一致性 + I5 后台通知** — locale 与隐藏时的"需你处理"通知。

## 具体视觉修复（非代码）

- 图标：执行/工单行首改为 16px 无底图标（GitHub=分支、飞书=纸飞机/蓝、钉钉=铃/蓝紫、本地=终端、定时=时钟），与标题同基线；保留 24px 品牌块仅在页头/大卡片。
- 颜色：TextFaint 亮 `#5B6472`、暗 `#94A0B8`；Border 亮 `#C9D0DA`、暗 `#39465F`；暗色选中行底 `#3A5FE0`；soft 底文字统一深一档（accentHover/amber-dark 等），保证 ≥4.5:1。
- 字体：正文 15（中文 14 下限），元数据 13，辅助 12；分区标题 15 加粗 + 上 16px 留白；命令/路径/ID 等宽 12–13。
- 层级：卡片标题 15/加粗，卡片说明 13 muted；行内"#N + 标题"用等宽编号 + 单行标题，标题最多 2 行。
- 状态条：下状态栏只留 "帮助 ⌘/ · 需要你处理 3"；版本/rev/Go/CGO 移入设置›部署与 About。
- 列表：行高 40/44；hover 用 SurfaceHover、pressed 用 SurfacePressed、选中左侧 2px accent 条 + 8% accent 底。
- 交互披露：所有被截断名称给 Tooltip；所有图标按钮加 Tooltip+Label；22px 按钮统一 28px。
- 空态：一句行动建议 + "为什么"（"还没有工作区 · 新建一个，Agent 才有地方干活"）。
- 加载：异步操作在按钮文字旁加 12px 三点 spinner；>3s 操作显示阶段文案（扫描目录…/重启渠道…）。
- 主题：提供高对比变体（边框 ≥3:1、文字 ≥7:1）、跟随系统字号（TextScale），减少动态时禁用滚动动画。
- 平台：设置应用菜单（⌘N/⌘,/打开面板/退出）、托盘图标换 16pt template 黑白、窗口记 StateKey 与最小尺寸、<1000 宽折叠侧栏。

## 限制

- 无显示器/未开真实窗口：hover、焦点环、通知、系统缩放均未实测；"会卡"未计时。
- 截图是 1440×900 @1x（`mui.NewTester` 渲染），非 Retina；3x 下小字/描边会更清楚，但对比度与目标尺寸结论不变。
- 仅评 UI/view；runtime 投影缺失处的空态按"诚实但技术"计（T2），未计入 runtime 缺陷。
