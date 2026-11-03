# fix-ui handoff (Wave 1)

范围：`internal/ui/**`、`internal/view/**`、**新增** `internal/humanfmt/**`（已在开工时告知 Lead）。
未触碰 runtime / engine / cmd / store / design / audit 报告；未 `git commit`、未 `go mod tidy`、未改 `go.mod/go.sum`。
`gofmt -l` 空；`go vet` 干净；`CGO_ENABLED=0 go test ./internal/ui/... ./internal/view/... ./internal/humanfmt/... -count=1` 全绿（80 个测试）。
`sh scripts/check-nocgo.sh` → **PASS (zero cgo)**（humanize 为纯 Go，已确认）。

---

## 完成的条目（对应 audit 编号）

### A. AUDIT.md P1 #4（N-8）/ nih-ui F-01/F-02/F-03/F-06 —— UI 4 处手搓 mygo 控件

1. **F-01 消息流改 ui.List（虚拟化 + follow end）** `internal/ui/stream.go`
   - `Scroll+Column` 每帧构建全部 Thread → `mui.List(c, &a.streamList, n, row)`；行内版式、缩进竖线、`maxStreamWidth=860` 居中、中文文案全部保留。
   - `ListState{FollowEnd: true, FollowAnimation: &mui.Smooth}`：新消息到达时跟随底部（`ui/list.go:54-65`）。
   - 每行一个 Thread（`a.thread` 原样复用）；空态仍是一句话邀请，不再构建整段历史。
   - `view.Thread` 新增 **`ID string`**（vm.go:414-424）：`threadKeys(rows)` 只在**每个** Thread 都有 ID 时返回 `ListState.Key`，否则返回 nil——空的重复 key 会让两行共用一个状态（`ui/list.go` 对 duplicate key 会 panic）。`internal/runtime` 目前不填 ID，列表退回按行号，行为与之前一致。

2. **F-02 评估页改 ui.Table** `internal/ui/eval.go`
   - 手搓表头 + `Scroll`+`Column`（不虚拟化）→ `mui.Table(c, &a.evalTable, []mui.TableColumn{…}, n, cell)`；表头、列宽、行虚拟化归框架（`ui/table.go`）。
   - 列：时间 `Width:56`、决定 `Width:88`、Trigger→结果 / reason 码两列弹性 `MinWidth 220/260`（原来是 `Grow(2)/Grow(3)`，Table 只能均分弹性列；见"行为变化"）。
   - `decisionRow` 拆成 `decisionCell(row,col)`；空筛选仍显示"这个筛选下没有 Decision。"。

3. **F-06 命令面板改 ui.List（表头不可选）** `internal/ui/search.go`
   - 新增 `paletteRow`/`paletteRows`：每组一个 heading 行 + hits；`ListState{Header, Key, Selected}`（heading 永不被选中，`ui/list.go:93-97`；列表滚动时 section 头钉在顶部）。
   - 手写的"只数 hits 的 n"与 `clampSearch` 保留（选择仍是 hit 下标），但行的选择由 List 持有：`hitAtRow`/`rowOfHit` 做映射。
   - **单点即跳**：`if lst.Changed() { jump }` —— 点击一行仍是"点一下就跳"，不是先选中再回车；heading 上的点击什么都不做。
   - Enter 仍用 `SearchField.Submitted()`：焦点在输入框，List 的 `Submitted` 只在列表获焦时触发（代码注释写明原因）。
   - ↑↓ 仍是 frame shortcut（焦点在输入框，List 自己的按键不生效），新增 `ScrollIntoView` 让选择滚进视野；删掉了 `searchRow` 里的 `Clicked()`。

4. **F-03 选择跟随实体（真 bug）** `internal/ui/app.go`+`execution.go`+`trigger.go`
   - `a.ticketList.Key = rows[i].ID`、`a.threadList.Key = rows[i].ID`（`ui/list.go:42-47`；`ui/list.go:548-554` 会把新行号回写给 `*Selected`）。
   - 之前：`TicketListVM.Rows` 由 `engine.AllTickets()` 按 CreatedAt 倒序给出（`internal/engine/engine.go:459`）→ 新任务置顶，行号整体 +1；`ThreadVM.Threads` 按 ActivityAt 倒序（`internal/runtime/pages.go:156`）→ 任何活动都重排。展开的行会静默指向**另一条**任务/线程。

### B. N-9（nih-ui F-08）—— token 层派生自 mygo Theme，暗色真正生效

`internal/ui/tokens.go` 重写、`internal/ui/app.go` 每帧派生：

```go
a.theme = brandFor(c.Theme().Dark).theme(c.Theme())
a.tk = tokensFrom(a.theme)
mui.Themed(c, a.theme)
```

- 共享色（Background/Surface/Border/Text/TextMuted/Accent/Success/Warning/Danger）**读回自 `*mui.Theme`**；只有 mygo 没有名字的品牌色（软底、代码面）留在 `brand` 表里，**浅色/暗色各一套**。
- `theme()`/`tokens()` 两个各自硬编码 18 个 hex 的函数删除；暗色不再被 `LightTheme()` 写死。`Tester.SetDark(true)` 后应用真的变暗（截图人工核对过 overview/eval）。
- **圆角漂移修复**：`cardRadius=12`（品牌）与 `controlRadius=8`（写入 `Theme.Radius`，`Tokens.RadiusSmall` 从主题读）各一个来源，不再两处魔数。
- `a.theme`/`a.tk` 仍是主线程每帧状态，无跨帧 Element/Context（§6.1 不变）。浅色下所有 hex 与改前一致，视觉无变化。

### C. N-10 / F-13 / F-14 —— 标准库与既有依赖

- `groupedNumber`（21 行）删除 → `humanize.Comma(int64(...))`（`overview.go:270`）。`go-humanize v1.1.0` 本就在 `go.mod` 间接依赖里，**未改 go.mod**；纯 Go。
- `trimSpace`（10 行）删除 → `strings.TrimSpace`（`input.go:36`）。**行为修复**：原实现只认 4 个 ASCII 字符，全角空格（U+3000）输入会被当作任务派发；现由 `unicode.IsSpace` 处理。

### D. N-11 / F—— 中文相对时间收敛（新增 `internal/humanfmt`）

- 新包 `internal/humanfmt`：`Age`（"12 分"）、`Ago`（"12 分前"）、`Until`（"2 小时后"）、`Duration`（"1 小时 5 分"）、`Clip`（rune 截断 + …），配 5 个表驱动测试（含负时长/边界/全角不拆字）。
- `internal/ui` 三处实现（`overview.humanAge/agoWord`、`schedule.untilWord`）删除，全部改用 `humanfmt`；`internal/view/search.go` 的 `clipText` 删除 → `humanfmt.Clip`。用户可见文案逐字不变（"12 分前"）。
- **runtime/brief 的两份仍在**（跨 scope，见"跨 scope 待办"）：`internal/runtime/pages.go:365 humanAge`（逐字相同）、`:377 clip`、`:526 humanDuration`；`internal/brief/digest.go:161 humanAge`（措辞不同："12 分钟前"）。

### E. 死代码（F-05/F-22/F-23/F-24 + item 7）

- **F-22 demo 投影**：`internal/view/demo.go`+`demo_pages.go`（572 行）从生产包删除。**判定偏差**：没有 inline 到各测试，而是移到新包 **`internal/view/viewtest`**（测试支撑包，生产代码零 import；`grep -rn viewtest internal | grep -v _test` 为空）。理由：ui 与 view 两个测试包需要同一批富数据，inline 会复制 ~570 行两次；如果 Lead 要求严格 inline，我可以改，但会引入两份重复 fixture。附带把 view 的 3 个 fixture 测试文件从 `package view` 改成 `package view_test`（dot-import view + `viewtest.DemoX`），否则会出现测试包 import cycle。
- **F-23** 删除 9 个未用图标：`icPause/icGear/icSend/icStop/icExternal/icPlus/icTray/icClose/icArrowR`（icons.go 剩 20 个，全部有引用）。
- **F-24** 删除 `App.inspect`、`App.focusComposer`、`App.wsPick/wsNames/wsIDs`（0 引用）；删除 test-only 导出 `view.SearchKindOrder()` 与 `(*view.SearchVM).Group()`（查找函数移到 `viewtest.GroupOf`，13 处测试调用点已改）。
- **F-05** 删除托盘里点了没反应的"暂停运行时"菜单项（`tray.go`；runtime 无 pause，注释说明将来接上再加）。
- **M2 文案**：`"保存通道未接线（M2 接入）…"` 等 9 处用户可见字符串改为 `"当前运行时不能保存设置…"`（settings.go/prompt.go/onboarding.go），测试同步改。生产路径本来全接线（runtime 实现 SettingsSaver/OnboardingSaver/PromptReloader）。

### F. AUDIT #10 的 UI 侧 / F-04（诚实状态）

- 新增 **`view.OverviewVM.PersonCount()`**（`internal/view/vm.go`）：`len(NeedsPerson)+len(Approvals)` 的唯一推导。顶栏 chip（原先 `len(NeedsPerson)`）、总览标题、侧边栏 `Nav` 三处统一读它——有 2 条待批单时三个数字不再分别是 4/2/2。
- 总览"运行时"卡片原先恒定 `chip("正常")`，现在与健康行同源：`healthProblems(ov)` >0 → `"%d 项需关注"`（amber），否则"正常"。同屏健康卡与运行时卡不再一个说正常、一个列失败项。
- 新增测试：`TestPersonCountAgreesAcrossTheSurfaces`、`TestRuntimeCardFollowsHealth`、`TestNilOverviewPersonCount`。

---

## 改动文件（只在本人 scope）

生产代码：
- `internal/ui/`：`app.go` `chrome.go` `eval.go` `execution.go` `icons.go` `input.go` `onboarding.go` `overview.go` `prompt.go` `schedule.go` `search.go` `settings.go` `source.go` `stream.go` `tokens.go` `trigger.go` `tray.go` `widgets.go`
- `internal/view/`：`vm.go` `search.go`；删除 `demo.go`、`demo_pages.go`
- 新增 `internal/humanfmt/humanfmt.go`、新增 `internal/view/viewtest/fixtures.go` `fixtures_pages.go`

测试：
- `internal/ui/`：修改 `view_test.go` `pages_test.go` `prompt_test.go` `search_test.go` `schedule_goal_test.go` `deliverables_test.go` `drain_test.go` `onboarding_test.go`；新增 `lists_test.go` `tokens_test.go` `format_test.go` `honest_test.go`
- `internal/view/`：`pages_test.go` `prompt_search_test.go` `schedule_goal_test.go`（改 package/import/调用点）
- 新增 `internal/humanfmt/humanfmt_test.go`

规模：`git diff --shortstat -- internal/ui internal/view` = **33 files, +532 / −906**；另新增 humanfmt 199 行、viewtest 598 行（其中 572 行是 demo 迁移）。

---

## 新增/修改的测试与结果

命令与结果：

```
$ CGO_ENABLED=0 go test ./internal/ui/... ./internal/view/... ./internal/humanfmt/... -count=1
ok  github.com/zeokeel/super-agent/internal/ui        1.7s
ok  github.com/zeokeel/super-agent/internal/view      0.005s
?   github.com/zeokeel/super-agent/internal/view/viewtest  [no test files]
ok  github.com/zeokeel/super-agent/internal/humanfmt  0.005s
（三包合计 80 个 PASS）

$ CGO_ENABLED=0 go vet ./internal/ui/... ./internal/view/... ./internal/humanfmt/...   → 干净
$ gofmt -l internal/ui internal/view internal/humanfmt                                  → 空
$ sh scripts/check-nocgo.sh                                                             → PASS (zero cgo)
$ CGO_ENABLED=0 go build ./...                                                          → OK（全仓）
$ CGO_ENABLED=0 go test ./... -count=1 -skip Live
  → 仅 internal/runtime 失败：TestSettingsConflictAcrossWindows
    "store: the state directory is already in use by another process (lock …)"
    → 与 UI 改动无关（状态目录锁被别人占用；疑似并发跑测试的进程），留给 runtime owner。
```

新增回归测试（18 个 + humanfmt 5 个）与它们证明的事：

| 测试 | 证明 |
| --- | --- |
| `TestTicketSelectionFollowsTheTicketWhenRowsReorder` | 反序发布后 `ticketSel` 跟到同一 Ticket（#42），展开详情仍是它 |
| `TestThreadSelectionFollowsTheThreadWhenRowsReorder` | 线程重排后选择跟着 thread，时间线不串 |
| `TestStreamBuildsOnlyTheRowsInView` | 40 条消息只构建可见行，最新一条在视野内（FollowEnd） |
| `TestStreamWithoutThreadIDsHasNoKey` | 无 ID 时不给重复 key（否则 List panic） |
| `TestEvalTableBuildsOnlyTheRowsInView` | 80 行只构建可见行；表头来自 Table |
| `TestEvalTableEmptyFilter` | 空筛选仍是诚实一句话 |
| `TestPaletteListHeadingsAreNotChoices` | ↑↓ 从不落在 heading；点击命中行**同一帧**跳转并定位 #42 |
| `TestPaletteListClickOnHeadingDoesNothing` | 点 heading 不跳、不关面板 |
| `TestPanelFollowsTheSystemAppearance` | `Tester.SetDark` 后 `a.theme.Dark`、token 换暗色、再切回浅色 |
| `TestTokenRadiiSeparateCardsFromControls` | 卡片 12 / 控件取主题 Radius 8，两个来源各一 |
| `TestTokensFromNilTheme` | nil theme 不 panic |
| `TestTokenTotalIsGrouped` | token 总数显示 `1,204,331`（humanize） |
| `TestInputTrimsFullWidthSpace` / `…KeepsTheTask` | 全角空格不再被派发；正常文本照样派发 |
| `TestPersonCountAgreesAcrossTheSurfaces` | 顶栏 chip / 侧边栏 badge / 标题三处 = 3 |
| `TestRuntimeCardFollowsHealth` | 健康项失败时运行时卡显示 "1 项需关注"，不显示正常 |
| `TestNilOverviewPersonCount` | nil VM 安全 |
| humanfmt `TestAge/TestAgo/TestUntil/TestDuration/TestClip` | 中文措辞与边界逐字锁定，rune 截断不拆字 |

另外用临时渲染测试（已删除）在 `/tmp` 导出了 overview/eval/execution/messages/palette + 暗色两张 PNG 人工核对：流页居中与 22px 间距、表格列宽、面板分组与选中态均正常；暗色对比度可读。

---

## 行为变化与兼容性风险

1. **消息流**：从"每帧构建全部历史"改为虚拟化；滚到底部会自动跟随新消息（新增）。行距/居中/文案不变（截图核对）。无 Thread ID 时退回按行号 key，行为同旧版。
2. **评估页**：表头改为 Table 的主题化样式（字号/字重/列分隔与旧手搓不同）；列宽从 `42/74/Grow2/Grow3` 改为 `56/88/均分/均分`（Table 的弹性列只能均分）。定位行不再整行铺 `AccentSoft` 背景，"定位" chip 与时间列强调色仍在（Table 不支持整行底色）。若必须保持 2:3，可改固定宽——留待 review。
3. **命令面板**：视觉基本一致（分组头有 Surface 底色、滚动时钉顶）；点击 heading 从"无效果"变为"明确无选择"（`Changed` 不触发）；点击命中行仍单点跳转。焦点始终在输入框，↑↓ 用 frame shortcut（非 List 内建键盘），理由写在代码注释。
4. **执行/触发列表**：重排后选择跟随实体（bug fix）；键盘/滚轮行为不变。
5. **主题**：暗色从"失效"变为"跟随系统"；浅色 hex 全部不变。`Tokens` 字段名未变（`Radius`=卡片 12、`RadiusSmall`=控件 8，语义现在写在注释与常量里）。
6. **文案**：5 个文件 9 处里程碑代号替换为用户语言；测试同步。
7. **view 包 API**：新增 `Thread.ID`、`OverviewVM.PersonCount()`；删除仅测试使用的 `SearchKindOrder()`、`(*SearchVM).Group()`。全仓 `go build ./...` 通过，无生产 importer 受影响。
8. **humanize 直接 import**：`go.mod` 里仍是 `// indirect`。构建/测试不需要联网（已在 go.sum 与模块缓存）。**请 Lead 在统一 tidy 时把它提升为直接依赖**。
9. 未改 `CGO_ENABLED` 语义、未新增第三方依赖、未改任何构建标签。

---

## 跨 scope 待办（file:line + 期望改动）

### runtime（fix-runtime / Wave 2）——诚实状态仍缺投影

1. `internal/runtime/runtime.go:175` `ov.Runtime.MaxJobs = 2` → 用真实并发上限（`r.concurrency()` 已在同文件健康行使用）。UI 顶栏 `chrome.go:45` 现在仍会显示 "0/2"。
2. `ov.Runtime.Running` **从未赋值**：`runtime.go:100-112` 已经算出 `running` 局部变量，却只给了健康行（`{Name:"执行器", Detail: fmt.Sprintf("%d/%d 在跑", running, r.concurrency())}`）。期望 `ov.Runtime.Running = running`。
3. `ov.Runtime.Today` **从未赋值** → 总览恒"今日运行 0 次"。需要今日完成/开始计数。
4. `internal/runtime/pages.go:202` `Counts: EvalCounts{Evaluated: len(vm.Rows), Decisions: len(vm.Rows)}` → 两个数恒等（评估页首两个总数相同）。期望分别投影"评估的 Trigger 数"与"Decision 数"。
5. `internal/runtime/runtime.go:207` `th := view.Thread{Head: inboundLine(t)}` → 补 `ID:`（如 Trigger 的 domain ID），`internal/ui/stream.go:threadKeys` 即刻启用按 ID 定位（代码已就绪，无 ID 时安全退回）。
6. `internal/runtime/pages.go:208-230` `ticketsVM` **没有按最近活动排序**，但执行页文案与注释都写"按最近活动排序，不是创建时间"（`internal/ui/execution.go:20`）。期望按最后活动时间排序（或改文案——设计 §6.10 要求按活动）。
7. 设计 §6.5 细节：侧边栏"执行" badge 用的是 `Running`，链接的却是全部任务页；若要严格"计数=列表长度"，需要 `OverviewVM` 增 `TicketTotal` 字段，UI 我可以配合改（本波未动）。

### brief / runtime 的中文时间与截断（N-11 收尾）

8. `internal/brief/digest.go:161 humanAge`（措辞 "12 分钟前"）→ `humanfmt.Ago`/`Age`。
9. `internal/runtime/pages.go:365 humanAge`、`:377 clip`、`:526 humanDuration` → `humanfmt.Age`/`Clip`/`Duration`。改完"12 分前 / 12 分钟前"两种说法消失。

### docs（fix-docs-ci / fix-docs-final）

10. `design/M0-tasks.md` 里 "M0-8 删除 demo" 已兑现（现在 demo 在 `internal/view/viewtest`，生产包零引用）；文档如需提及测试夹具位置可更新。
11. `design/prototype/go.mod:14` 仍是 `replace github.com/egoist/mygo => /tmp/mygo`（不可复现，不在根 module 测试范围内）——docs/prototype 由对应 owner 决定"钉版本 + 标注视觉参考"，我不动 `design/**`。
12. `README/CHANGELOG` 若提到"M2 保存通道未接线"文案，现在用户可见文案已改（`当前运行时不能保存设置`）。

### Lead

13. `go mod tidy`：把 `github.com/dustin/go-humanize` 从间接提升为直接依赖（代码已 import，无需下载）。
14. 决定 F-10/F-11/F-12（Divider 27 处、Progress 占用条、Avatar）是否入本波：**本波未做**——不在 8 项工作清单内，且都是 LOW（可用 `ui.Divider`/`ui.Progress`/`ui.Avatar`），可留 Wave 2 或标 documented-deviation。

---

## 未完成 / 被阻塞

- **无阻塞**。两处主动偏差已在上面标注：
  1. demo 夹具"移动而非 inline"（`internal/view/viewtest`），理由：ui+view 两个测试包共用同一批数据，inline 会重复 ~570 行。
  2. 评估页定位行不再整行底色（Table 的单元格模型限制），"定位" chip 保留。
- 未跑 `-race`（契约：该 step 需 `CGO_ENABLED=1`，且不是我的闸门）；未跑三平台交叉编译。
- 全仓 `go test ./...` 仅 `internal/runtime` 一个用例因状态目录锁失败（并发进程占用），非本 scope、非本次改动引起。
