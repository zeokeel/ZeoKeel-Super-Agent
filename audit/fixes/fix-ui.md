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

---

# Round 2（2026-11-03，UI 第二轮：Super-Agent 侧边栏 / 工作区 / 字体 / 系统信息）

依据：`audit/fixes/ui2-spec.md` §2–§3、§6。范围仍是 `internal/ui/**` + `internal/view/**`；未触碰 runtime/agent/cmd/design，未改 `go.mod`。

## 完成的条目

### 1. view 契约（spec §2，照抄签名）

`internal/view/workspace.go`（新增）、`internal/view/vm.go`：

- `const SecWorkspace Section = "workspace"`；`SectionLabel(sec)` 覆盖它（`工作区`），`Nav` 的 `SecOverview` 标签改为 `超级Agent`（顺序/其余标签不变）。
- `WorkspacePanelVM{Workspaces []WorkspaceEntryVM, Selected, UpdatedAt}`、`WorkspaceEntryVM{ID,Name,Root,Purpose,Kind,Conversations,LastAt}`、
  `ConversationVM{ID,Number,Title,Workspace,CreatedBy,Source,Stage,StageID,Attention,LastAt}`，`CreatedBy` 常量 `me`/`agent`。
- 纯函数助手（view 内可测）：`Find`、`SelectedWorkspace()`（Selected 失效时退回第一个）、`Groups()`（我创建的→超级Agent 创建的，空组不出现）、
  `Mine()`/`AgentMade()`、`ConversationVM.CreatedLabel()`/`IDLabel()`。
- `Snapshot` 新分片：`PublishWorkspaces(vm)` / `Workspaces()`（nil=未发布，与其它分片一致，bump 一次帧）。
- `SystemInfo{OS,Arch,Timezone,UTCOffset}` + `Known()`/`Label()`（"linux/amd64 · Asia/Shanghai (UTC+08:00)"，零值返回 ""）；`OverviewVM.System SystemInfo`。
- 兼容性：只新增字段/方法，未改任何既有签名；旧分片/旧 Section 全部保留。

### 2. 侧边栏重设计（spec §3）

`internal/ui/chrome.go` 重写 `sidebar`，宽 232（spec ~220，多出 12px 让系统行不截断）：

```
超级Agent（icOverview，计数=PersonCount）
────────
工作区 标题 + ＋
  ▸/▾ workspace 行（icFolder，右=会话数；箭头展开/收起；行其余部分打开 工作区 页）
      我创建的 / 超级Agent 创建的 分组
        会话行（icUser / icSpark，#N + 标题；Attention → 琥珀点）
      ＋ 新建会话（预填该工作区）
  ＋ 新建工作区
────────
监控（标签）+ 消息/来源/触发/评估/执行（执行=running 计数）
设置（独立一行，底部）
────────
系统行：OS/arch · 时区 (UTC±h)（来自 OverviewVM.System，未投影写"系统信息未投影"）
```

- 展开状态 `a.wsExpanded[id]`、选中工作区 `a.workspaceSel` 都在 app state（主线程），没有跨帧元素。
- 可访问性/可测性：箭头 `Label("展开工作区 <name>")`、行 `Label("工作区 <name>")`、会话行 `Label("会话 <title>")`（Tester 可按 label 点击）。

### 3. 工作区页 + 两个弹窗（spec §3 item 3）

`internal/ui/workspace.go`（新增）：

- **Optional interfaces**（ui 包定义，不 import runtime）：`WorkspaceCreator{CreateWorkspace(name,root,purpose,kind)}`、
  `ConversationCreator{NewConversation(workspaceID,text)}`；缺失时表单照常打开、创建按钮禁用并具名原因
  （同 `动作未接线：运行时未实现 WorkspaceCreator` 模式），点击禁用按钮无副作用。
- **新建工作区弹窗**：名称/根目录/用途 + 类型 `Segmented`（仓库/数据/文档/混合 → repo/data/docs/mixed）；名称与根目录为空不可创建；
  失败保留弹窗并显示运行时错误。输入框 `Label("输入"+label)` 以便测试/读屏区分标签与输入。
- **新建会话弹窗**：工作区名 + 会话内容；空文本不可创建；成功才关闭。
- **工作区页（SecWorkspace）**：标题+用途+`根目录` mono chip+kind+会话数+最近活动；会话卡片按「我创建的 / 超级Agent 创建的」分组，
  行内含 `#N`、标题、created badge、source、stage chip、`待你处理`、相对时间；点行打开该 Ticket。
- **导航**：点会话 → `openTicket`（执行页选中并滚动到该行、展开详情）；点工作区行 → `SecWorkspace`。
  工单不在当前执行投影时留在原页并给出原因（执行页/总览显示 `a.actionErr`）。
- 空态：无工作区投影 → "等待运行时发布工作区…"（侧栏）/ "等待运行时发布工作区投影…"（工作区页）；有投影但为空 → 新建引导。

### 4. 全局字体 +2（spec §3）

- `internal/ui/tokens.go`：`baseFontSize = 13 → 15`（theme.FontSize，控件默认字号随之 +2）。
- 全 `internal/ui` 显式 **文本** `.FontSize(N)` 共 295 处 +2（10→12、11→13、12→14、13→15、17→19、18→20、24→26、26→28…）；
  **图标尺寸保持不变**（按 spec "图标尺寸不强制"），避免与文本抢视觉。
- 手动调整两处布局：侧栏系统行固定 10px 并 232 宽（否则截断）；状态栏右侧「帮助 · 版本」包进 `Gap(6)` Row 且 11px（否则粘连/换行）。
- 逐页截图核对（总览/执行/评估/触发/设置/名词表/两个弹窗/消息答复）：无裁字、无行高溢出；未改列表行高常量。

### 5. 系统信息展示（spec §3）

- 侧栏底部：`SystemInfo.Label()`；未投影 = `系统信息未投影`（不猜 UTC）。
- 设置 › 部署新增「运行环境」：`操作系统` = OS/arch、`时区` = Timezone (UTCOffset)，全部读投影；未投影 = `等待运行时投影操作系统与时区（Runtime.System）`。

### 6. 超级Agent 答复行（spec §4 的 UI 侧）

`internal/ui/stream.go`：

- `LineReply` / `LineNotice` 中文本以 `超级Agent` 开头的行 → 渲染成独立**答复面板**（`agentAnswer`：icSpark + "超级Agent" 标签 + 时间 + 正文，
  按 Tone 取强调/琥珀/红色底），与普通 notice 明显不同；前缀 `超级Agent：` 不重复显示。
- `threadSteps(th)`：同一 Thread 已有超级Agent 答复时，丢弃裸 `未受理` 的 Decision 行——答复即结论，不再并排显示"未受理"；
  无答复时 `未受理` 照旧显示（两种情况都有测试）。别人的回执 `LineReply`（不含"超级Agent"）不算答复，不吞 Decision。

### 7. 测试

新增/修改：`internal/ui/workspace_test.go`（11 个：侧栏结构、树展开/收起、工作区页、会话跳转、两个弹窗（成功/失败/未接线）、系统事实（有/无投影）、
字体 token、答复行替换未受理、无答复保留未受理、他人回执不算答复）、`internal/view/workspace_test.go`（7 个：Find/Selected 回退、Groups、标签、
SystemInfo.Label、Section 标签、Snapshot 分片）。
既有 `verify_wave2_test.go` 的 3 处 `click("总览")` 改为 `click("超级Agent")`（侧栏标签按 spec 改名，语义未变）；`pages_test.go` 的缺投影用例加入 `SecWorkspace`。
夹具：`viewtest.DemoWorkspaces`、`DemoSystem`，`DemoOverview.System` 填充。

## 结果（本轮实测）

```
$ CGO_ENABLED=0 go test ./internal/ui/... ./internal/view/... -count=1
ok  internal/ui      (108 PASS)
ok  internal/view    (26 PASS，含 verifier 文件)
$ CGO_ENABLED=0 go vet ./internal/ui/... ./internal/view/...   → 干净
$ gofmt -l internal/ui internal/view                           → 空
$ CGO_ENABLED=0 go build ./...                                 → OK（全仓）
$ sh scripts/check-nocgo.sh                                    → PASS (zero cgo)
```

人工截图核对（临时渲染测试已删除）：`/tmp/r2-{overview-tree,workspace-page,workspace-modal,conversation-modal,agent-answer,settings-deploy,execution,eval,trigger,help}.png`。

## 偏差 / 说明

1. 侧栏宽度 232（spec "约 220"）：220 时系统行 `linux/amd64 · Asia/Shanghai (UTC+08:00)` 在 10px 下仍被截断；232 后完整一行。
   若时区名很长（如 America/Argentina/Buenos_Aires）允许换行，不截断。
2. 工作区页采用"会话列表 + 点击打开执行"，未做独立的"消息列表"页（spec 二选一允许）。
3. 字体只 +2 文本；图标尺寸未动（spec 不强制）。若视觉验收希望图标随字号 +1，可一次性在 `mui.Icon(...).FontSize` 上做。

## 跨 scope 待办（runtime，UI 已就绪）

1. **发布工作区投影**：`PublishWorkspaces(&view.WorkspacePanelVM{...})`——按 spec §2：每个 workspace 的 Ticket（含 ended，最多最近 50，LastAt desc），
   `ConversationVM.CreatedBy` = origin Trigger.Source=="local" ? "me" : "agent"，`Source/Stage/StageID` 复用派生阶段，`Attention = t.Attention != nil`。
2. **OverviewVM.System**：`SystemInfo{OS: runtime.GOOS, Arch: runtime.GOARCH, Timezone: time.Local.String(), UTCOffset: <±hh:mm>}`（或 zone offset 格式化）。
3. **命令**：实现 `CreateWorkspace(name,root,purpose,kind) error` 与 `NewConversation(workspaceID,text) error`（spec §4：
   Register+scan / engine.Dispatch+EvaluateDue+Publish）。cmd 已把 Runtime 作为 `Cmd` 传入，方法存在即被 UI 探测到；建议加编译期断言。
4. **答复行**：本地消息未派发时追加 `LineReply`/`LineNotice`（文本以 `超级Agent：` 开头），并把裸「未受理」改为「已答复/未派发 + 原因」；
   UI 已按此渲染（含"有答复就不显示裸未受理"）。
5. 沿用上一轮未关的两项：`TicketRow.StageID/Attention/CanRetry` 投影（否则总览只有“停止”）；`View.Thread.ID`（否则消息流不按 ID 定位）。

---

# Round 2 · Addendum（原生目录选择器 + 设置›来源的 IM 通道卡）

同一轮、同一 scope，补两个附加项。

## A1. 新建工作区的原生目录选择器

`internal/ui/workspace.go`：

- **原生对话框**：`mygo.Dialog.Open(mygo.OpenDialogOptions{Parent, Title:"选择工作区目录", Directory:true,
  CreateDirectories:true, ButtonLabel:"选择此目录"})`（mygo v0.7.4 `dialog.go`；跨平台由 mygo 自带的
  GTK/portal、Windows、macOS 后端处理，**没有** zenity/osascript 等 shell out）。选项集中在
  `folderDialogOptions(parent,title)`，测试可无显示器直接钉住契约。
- **不阻塞主线程**：`Dialog.Open` 会阻塞到人关闭对话框（其文档允许任意 goroutine 调用），所以
  `startFolderPicker` 在独立 goroutine 里打开它，选完用 `a.win.Update(...)` 把路径写回 app state；
  `win == nil`（纯 headless）则直接应用。帧从不等待对话框。
- **单实例**：`a.pickingFolder` 为 true 时按钮显示「选择中…」且禁用，第二次点击不会再开一个对话框；
  取消（无路径）不改表单、不报错；错误显示在弹窗里。
- **Seam（测试可替换）**：`openFolderDialog`（真正的 mygo 调用）与 `startFolderPicker`（线程/回投）两个 var；
  纯 headless 测试替换 `startFolderPicker` 同步作答。另加 `folderDialogOptions` 契约测试
  （Directory/CreateDirectories/ButtonLabel/Title/Multiple=false）。
- UI：根目录一行改为「输入框 + 浏览…」，选择中显示说明行；其余（名称/用途/类型）不变。

新增测试（`internal/ui/workspace_test.go` 追加 5 个）：
`TestWorkspaceFolderPickerFillsRoot`、`…Cancel`、`…Error`、`…IsSingle`、`TestWorkspaceFolderDialogOptions`。

## A2. 设置 › 来源的 IM 通道卡（飞书 / 钉钉）

`internal/view/channels.go`（新增，纯数据）+ `internal/ui/channels.go`（新增）：

- **契约（view）**：`ChannelOption{ID,Label,Implemented,Note}`、`ChannelSpec{Name,Label,EnabledKey,
  TransportKey,CredentialKeys,Options}`、`ChannelSpecs()`、`ChannelSpecOf()`、`ChannelKey(key)`
  （告诉设置页哪些 来源 组字段由通道卡拥有，避免重复渲染）。
  - 飞书：`feishu_enabled` / `feishu_transport`（longpoll=长轮询，已实现）/ `feishu_app_id`、`feishu_app_secret`、`feishu_chats`。
  - 钉钉：`dingtalk_enabled` / `dingtalk_transport`（dws=DWS 长连接；stream=官方 Stream——runtime/im.go 已构建该驱动，
    runtime 设置 Help 也宣传它，因此 **两者都可选**；「未实现」标记机制保留给将来未接入的传输）/ `dingtalk_app_key`、`dingtalk_app_secret`、`dingtalk_robot`。
  - 共同 `im_self`（机器人自身账号，@ 判定）留在通道卡下方的通用字段里渲染**一次**（同一 settings key 不能渲染两次）。
- **Optional interface（ui 定义，runtime 实现）**：
  `ChannelAuthorizer interface { CheckChannel(name string) (state, detail, authURL string, err error) }`。
- **检查/授权**：`startChannelCheck` 在 goroutine 里调 `CheckChannel`，用 `a.win.Update` 回主线程写结果；检查中按钮禁用显示「检查中…」；
  state 映射 chip：`ok→检查通过`、`warn→注意`、`unauthorized→需要授权`、`error/err→检查失败`、空→未检查；
  detail 原样显示。**authURL 非空**时显示「打开授权页」+ URL；打开走 `mygo.Shell.OpenExternal`
  （纯 Go、跨平台；它内部等主线程，所以同样在 goroutine 里调，`startOpenAuthorization` seam 可替换），
  且只接受 `http(s)` URL（`validAuthURL`），否则报错不打开。
- **设置保存协议不变**：启用开关（`mui.Switch`）、传输方式（chip 选择器）、凭据字段全部走
  `a.settingsPtr(g.ID, field)` → `settingsDirty` → 现有保存条 → `SettingsSaver`；无乐观更新（投影不变，值就还是投影的值）。
  传输方式没有 `SettingSelect` 的旧渲染问题：现在按字段 `Options` 渲染 chip；运行时发了 `Options` 就视为全部已实现（运行时为准），
  没发时用 build 内目录并把未实现项标灰、不可点，同时提示「等待运行时投影：<transport_key>」。
- **诚实状态**：未投影启用字段/凭据 → 「等待运行时投影：<key>」；未启用 → chip「未启用」+「未启用 · 不监听：保存并启用后才会连接」，
  页面不会出现「监听中/已连接/在线」这类它无法证明的说法（测试钉死）；无 `ChannelAuthorizer` → 检查按钮禁用 +
  「动作未接线：运行时未提供 CheckChannel」，点击无副作用。
- 通道卡只在 来源 组渲染（执行组不重复）；`ChannelKey` 命中的字段不再出现在下方通用列表。

新增测试（`internal/ui/channels_test.go` 10 个 + `internal/view/channels_test.go` 2 个）：
卡片渲染（飞书/钉钉/传输/凭据/检查按钮）、未启用不声称监听、检查成功显示 state/detail、需要授权显示 URL 且点击才打开、
非 http(s) URL 拒绝、未接线禁用+原因、启用开关走保存协议（dirty→保存值）、传输选择保存、未投影字段诚实提示、
传输未实现不可选、通道卡只在来源组；view 侧 pin 两个通道的键/传输/未实现标记与 `ChannelKey`。

## 结果（含 addenda）

```
$ CGO_ENABLED=0 go test ./internal/ui/... ./internal/view/... -count=1
ok  internal/ui      (123 PASS)
ok  internal/view    (28 PASS)
$ CGO_ENABLED=0 go vet ./internal/ui/... ./internal/view/...   → 干净
$ gofmt -l internal/ui internal/view                           → 空
$ CGO_ENABLED=0 go build ./...                                 → OK
$ sh scripts/check-nocgo.sh                                    → 步骤 4 (go vet ./...) 红：
    cmd/zeokeel/extras_test.go:48:8: undefined: triggerForIM
    ← 别人 scope 的 in-flight 改动（cmd 测试文件），与 UI/view 无关；我的三个包 vet 干净。
```
人工截图核对（临时测试已删）：`/tmp/r3-{source-channels,source-channels-auth,workspace-modal,workspace-modal-picked}.png`。

## 跨 scope 待办（addenda）

6. **runtime 实现 `ChannelAuthorizer`**：
   ```go
   func (r *Runtime) CheckChannel(name string) (state, detail, authURL string, err error)
   // name: "feishu" | "dingtalk"; state: "ok" | "warn" | "error" | "unauthorized"（空=未检查）
   // authURL 只允许 http(s)（面板会二次校验），无授权需求时返回 ""
   ```
   UI 侧接口签名已按此定义；缺省时面板禁用并说明。
7. **runtime 在 来源 设置组发布通道字段**：`feishu_enabled`(SettingSwitch) / `feishu_transport`(SettingSelect,
   Options ["longpoll"]) / `dingtalk_enabled` / `dingtalk_transport`(SettingSelect, Options ["dws","stream"]——
   运行时已构建官方 Stream 驱动，两个都可用)；凭据键沿用现有 `feishu_app_id`/`feishu_app_secret`/`feishu_chats`、`dingtalk_app_key`/`dingtalk_app_secret`/`dingtalk_robot`。
   发布后 UI 会自动把传输选项视为全部已实现（运行时是唯一事实源）。
8. **runtime 启用语义**：`enabled=false` 时不要启动监听（与面板「未启用 · 不监听」一致）；启用并保存后由 runtime 重启/启动对应 driver。
9. **cmd**：无需新增接线——`Cmd` 传入的 Runtime 实现上述方法后被 UI 类型断言探测到；建议在 cmd/runtime 加编译期断言
   `var _ ui.ChannelAuthorizer = (*runtime.Runtime)(nil)` 等（含 A1 的 WorkspaceCreator/ConversationCreator）。
10. `check-nocgo.sh` 目前红在 `cmd/zeokeel/extras_test.go`（`triggerForIM` 未定义），属 fix-runtime 的 in-flight 工作，需其收尾后才能全绿。

---

# Round 3（修复全部：设计复审 P0-2 / P0-3 / P0-7 / P1-6 / P1-7 / P2 的 UI 侧）

依据 `audit/design-review-r2.md` + 四份 detail 报告。范围仍是 `internal/ui/**` + `internal/view/**`；无 runtime/agent/im/store/design 改动，无 `go.mod`、无 commit。

## 1. P0-3 身份：行显示编号，查找用域 ID

- `view.TicketRow.Number int`、`view.TicketDetailVM.Number int`；`ID` 明确为域 ID（查找键），编号只用于显示（`view.TicketLabel(id, number)` → "#42"；未投影编号时回退 "#"+ID，永不为空）。
- UI 显示改用 `TicketLabel`（总览/执行/工作区会话/徽标）；**所有查找仍用 `ID`**：`DetailVMOf(r.ID)`、`DetailOf(r.ID)`、`openTicket(conv.ID)`、`located(...)`、搜索命中跳转（`hit.TicketID` = 域 ID），搜索行的人类可读行也用编号。
- 夹具改为域 ID（`tk-42`/`tk-44`…）+ `Number`；`Details`/`DetailVMs` map 以域 ID 为 key；工作区会话 ID 与工单域 ID 一致。测试断言同步更新（`lists/ticketdetail/search/workspace` + 新增身份测试）。

## 2. P0-2 异步命令边界

- `internal/ui/commands.go`：`runOffMain`（有 window 时 goroutine；headless 测试同步执行，保持确定性）+ 六个 seam：`startDispatch`、`startCreateWorkspace`、`startNewConversation`、`startRenameWorkspace`、`startDeleteWorkspace`、`startSaveSettings`；结果一律经 `postToUI`（`win.Update`）回主线程。
- 可见 pending + 防重入：发送中…（按钮禁用、草稿乐观清空、pending 回显）、创建中…（弹窗不关、不可二次提交）、保存中…（settings 与 prompt 共用 `savingGroup`）；测试用"按住"的 seam 断言 pending 与二次点击不会重复调用。
- 发送失败不再保留红字：Dispatch 拒绝通常是"已答复/需追问"，改中性 note（暗色说明），红色只留给真正的动作/保存失败。设置保存与提示词保存都走异步 seam，原有"无乐观更新"协议不变。

## 3. P0-7 超级Agent 聊天

- `internal/view/chat.go`：`ChatRole`（user/assistant/system）、`ChatKind`（outcome/answer/note/error）、`ChatMessage{Role,Kind,Text,At}`、`ChatVM`（`Tail/Last/LastAnswer/IsFailure`）+ `Snapshot.PublishChat/Chat`（未投影 = nil，不画空聊天）。
- 总览新增对话卡（最近 8 条，紧凑气泡；用户行浅色、系统行"记录/结果"、失败只有 `ChatKind error` 才红）；**每页输入框上方**固定显示最新 agent 行（`composerContext`），发送中显示"发送中… + 回显"，所以回答/派发结果不再只出现在消息页。
- 提交乐观清空草稿；聊天回答/追问中性色，红色只留给 `ChatKind error` 与真实动作失败。`ChatMessage.IsFailure` 有 view 单测钉住。

## 4. P1-7 工作区上下文

- `TicketRow.WorkspaceID`（过滤用 ID；`Workspace` 仍是显示名）、`Thread.Workspace`（消息所属工作区 ID）+ `view.FilterTicketsByWorkspace` / `FilterThreadsByWorkspace`（"" = 全部；未分类线程只在"全部"出现）。
- 选中工作区（侧栏行/工作区页/会话）设置 `filterWorkspace`：执行与消息过滤，页面顶部显示 `工作区：X · 全部` 控件（`全部工作区` 一键恢复）；侧栏选中高亮随 filter 走；过滤后为空时消息页给"这个工作区还没有消息"的诚实空态。执行页脚仍报全局总数 + 当前筛选数。

## 5. P1-6 工作区管理

- 可选接口（ui 定义，运行时实现）：`WorkspaceRenamer{RenameWorkspace(id, name string) error}`、`WorkspaceDeleter{DeleteWorkspace(id string) error}`。
- 工作区页头部 `重命名` / `删除`；重命名弹窗预填名称；删除先确认，明确写"磁盘上的目录不会被删除"；两者异步（pending：保存中…/删除中…），失败保留弹窗与运行时错误；未接线按钮禁用并写明缺哪个接口（没有假成功）。删除成功后若该工作区正是当前上下文则清除过滤。

## 6. P2

- **传输选项**：运行时 `Options` 仍是权威，但构建只勾选自己真能跑的传输：未知选项显示"（未实现）"且不可选（F-11）；飞书 longpoll、钉钉 dws/stream 均可选（上一轮已按 runtime 事实修正）。
- **保存条文案按组**：`SettingsGroup.Effect`（运行时优先）+ `view.SaveEffect(id)` 兜底：来源=保存后重启相关渠道、提示词=热更新、执行=下次新工单生效、允许名单=立即生效、部署=重启后生效。不再统一写"并热更新"。
- **字号 token**：`ViewScale` + `fs()`（385 处显式字号统一走比例），theme 字号 = 15×scale；`ui.font_scale`（0.9/1.0/1.1，clamp 0.8–1.4）在 设置›部署 提供控制（有投影字段时经保存协议写；没有则只读并提示等待投影）。原"一次性 +2"的魔数消失。
- **窗口/侧栏**：`Run` 最小窗口 1100×720（否则侧栏+页同时被裁）；侧栏工作区名/会话标题 `SingleLine` 省略 + Tooltip 显示全名与根目录。

## 7. 测试与结果

新增 `internal/ui/round3_test.go`（16 个：异步 pending×4、聊天渲染/上下文/边界/错误语义×4、身份×2、工作区过滤×2、改名/删除/未接线×3、传输真值、保存条文案、字号设置）+ `internal/view/chat_test.go`（5 个：ChatVM、过滤器、TicketLabel、SaveEffect、nil）。
更新既有：`view_test`（发送拒绝改为乐观清空+中性说明）、`lists_test`（身份 + 行推导含工作区）、`ticketdetail_test`、`search_test`、`workspace_test`（token 测试改为 scale token）、夹具（域 ID/Number/WorkspaceID/Thread.Workspace/Chat）。
```
$ CGO_ENABLED=0 go test ./internal/ui/... ./internal/view/... -count=1   → ok（ui 136 / view 37）
$ CGO_ENABLED=0 go vet ./internal/ui/... ./internal/view/...             → 干净
$ gofmt -l internal/ui internal/view                                     → 空
$ CGO_ENABLED=0 go build ./...                                           → OK
$ sh scripts/check-nocgo.sh → 步骤 4 红在 internal/runtime/im2_test.go（别人 in-flight 的测试），与 UI/view 无关
```
截图核对（临时测试已删）：`/tmp/r4-overview-chat.png`（总览对话 + 输入框上最新回答）、`/tmp/r4-execution-filter.png`（工作区过滤 + 全部）、`/tmp/r4-workspace-actions.png`（重命名/删除 + 过滤条）。

## 8. 跨 scope 待办（runtime，UI 已按契约预留）

1. `TicketRow.Number`（编号）与 `TicketRow.WorkspaceID`（`string(t.Workspace)`）填充；`Workspace` 继续放显示名。
2. 发布 `ChatVM`：消息 role user/assistant/system，kind `outcome`（"已派发 #N"/"需要你确认"）/`answer`/`note`（中性）——**只有真正失败（网络/DB/权限）用 `error`**，它才会红；"未命中工作区、已答复"不要用 error。
3. `Thread.Workspace` 填工作区 ID（inbound thread）。
4. 实现 `WorkspaceRenamer.RenameWorkspace(id, name string)`、`WorkspaceDeleter.DeleteWorkspace(id string)`（string id；UI 已在 goroutine 调用）。
5. `SettingsSaver.SaveSettings`/`Dispatch`/`CreateWorkspace`/`NewConversation` 保持"只入队/后台执行"，UI 不再等返回；慢操作不要占 main-thread handler。
6. 发布 `ui.font_scale` 字段（部署组，值 0.9/1.0/1.1）后，设置页字号控制才可编辑；未发布时面板显示当前比例 + 等待投影说明。
7. 传输 `Options` 只列真实可用的值（构建已知项才可选；未知项 UI 会灰显"未实现"）。
8. plan/task 可见性本轮未做（等 runtime 投影 task 列表再上 UI）。

---

# Round 4（交互审计前端：UX 优化）

范围：`internal/ui/**` + `internal/view/**`；无 runtime/cmd/store 改动，无 `go.mod`，无 commit。依据 `audit/interaction-audit.md` + 三份报告。

## 已完成（P0 前端）

- **P0-A 模型设置**：`view.SettingsWorker`（"worker"）加入 RequiredSettingsGroups，运行时未发布时用 `view.ModelGroup()` 兜底（端点/模型/API Key，保存即生效）；设置›模型增加「测试连接」(`ModelTester.TestModel(ctx)` off-frame，pending/成功/失败 inline，文案说明"测试已保存配置"）；总览在模型未配置时显示「还没有配置模型/去配置模型」停靠卡（运行时设置未发布时不误报）。
- **P0-1 输入框**：多行 `TextArea`（Enter 换行、⌘/Ctrl+Enter 发送，IME 安全）；可见目标 chip（"发给 超级Agent（聊天）" / "项目 X（派活）"），可切换，选中工作区自动设为派活目标；`WorkspaceDispatcher.DispatchTo` 走 off-frame seam；失败保留草稿 + 「再次发送」；最新回答可展开/收起 + 复制 + 「查看全部」→ 新「对话记录」页（每条可复制、可重发上一条）。
- **P0-B 执行行动作**：运行中行内「停止」、可重试行「重试」、待回答行「回答」，各自 `rowPending`/`rowErr` 局部状态；删除"动作在总览" dead-end 文案。
- **P0-2 收件箱**：新导航「收件箱」（统一"待你决定 N"，侧栏/顶栏/收件箱同一计数，顶栏 chip 可点）；页内请求卡（批准/拒绝，pending/局部错误、成功 toast）与任务卡（回答/重试/打开）；j/k/Enter 键盘路径；窗口隐藏且计数增长时发 `mygo.Notification`（UI 侧 diff，不需要 runtime 回调）。
- **P0-C 路径真相**：`TicketDetailVM.ProjectRoot/ExecutionRoot`、`Artifact.Path`（新增契约）在详情里显示「项目目录 / 执行副本」+ 打开/复制，明确"Agent 在副本里工作，项目目录不会被直接修改"；交付物（总览 + 交付物面板）有路径时可打开/复制。
- **P0-8 表单/引导**：必填 `*` 标记 + 「必填/可选」chip；向导工作区步骤加原生目录 picker；模型步骤「保存并测试」；`OnboardingSkipper`「稍后设置（演示模式）」、`OnboardingFinisher`「开始使用」off-frame，完成后落总览 + toast。
- **P0-6/视觉**：token 对比度（TextFaint 亮 `#5B6472`/暗 `#94A0B8`，Border `#C9D0DA`/`#39465F`）；可点行/筛选 chip/导航/传输选项/工作区行加 `Focusable().Role(RoleButton).Label().Tooltip()` + hover；28px 目标；执行行 source 图标 16px 无边框；状态栏移除版本/rev/Go/CGO（保留待决定/帮助）；窗口 MinWidth/MinHeight + `StateKey="main"`（mygo 记忆位置/大小）；⌘N/⌘, 快捷键；`mygo.Shell.OpenPath`/剪贴板/通知 seam 可测试。
- **P0-7 导航/术语**：日航 5 项（超级Agent/收件箱/项目/执行/设置），技术页进「诊断」分组（默认展开可折叠）；工作区称呼改「项目」；"动作未接线/等待运行时投影/尚未投影"等工程语改为"暂时不可用/还没有数据"；#N 显示（回答/停止标题、错误文案）；状态栏只留人话。

## 契约（供 runtime 填）

- UI 断言：`ModelTester{CheckModel(ctx) (bool,string)}`、`WorkspaceDispatcher{DispatchTo(ws,text) error}`、`OnboardingSkipper{SkipOnboarding()(uint64,error)}`、`OnboardingFinisher{FinishOnboarding() error}`、`DemoModer{DemoMode() bool}`、`AttentionCounter{AttentionCount() int}`。
- view 新增待填：`TicketRow.CanStop`、`TicketDetailVM.ProjectRoot/ExecutionRoot`、`Artifact.Path`；`view.AttentionVM`（收件箱专用投影）——收件箱当前用 OverviewVM.Approvals/NeedsPerson 兜底，runtime 发布 AttentionVM 后切换（已与 nih-runtime 对齐）。
- 已接受 conf-arch 的 `ChannelValidator/DWSVersionValue/DWSLoginArgs/IMConnectionState` 契约，**IM 连接向导尚未实现**（下一轮，见下）。

## 未完成（诚实清单）

1. **IM 连接向导**（P0-5）未做：按 transport 分流字段、草稿校验、连接 modal（步骤/等待/成功/失败/DWS 版本与登录命令复制/打开授权 URL）依赖 `ChannelValidator` 适配器；现有渠道卡仍是"开关+传输+凭据+检查"。
2. `TicketActions()/ExecutionPaths()/AttentionItems()` 的 runtime structs 不能从 ui 引用，需 runtime 填 view 字段（已在其待办）。
3. 菜单栏应用级命令（⌘N/⌘, 已用窗口快捷键替代，未改 mygo 应用菜单）；字体阶梯/高对比主题/系统 TextScale 仍待做；计划/任务可见性待 runtime 投影。
4. 部分测试用 1440×1600 视口（渠道卡/交付物在 900 高折叠下点击不可达）；这是 tester 视口限制而非功能缺陷，已加注释说明。

## 结果

```
$ CGO_ENABLED=0 go test ./internal/ui/... ./internal/view/... -count=1  → ok（ui/view 全绿）
$ CGO_ENABLED=0 go vet ./internal/ui/... ./internal/view/...            → 干净
$ gofmt -l internal/ui internal/view                                    → 空
$ CGO_ENABLED=0 go build ./...                                          → OK
$ sh scripts/check-nocgo.sh                                             → PASS（zero cgo）
```

## Round 4 addendum（IM 连接向导 + 统一收件箱投影）

- **IM 连接向导**（`internal/ui/channelwizard.go`）：每个渠道卡新增「连接向导…」；modal 内按 transport 分流字段（飞书 ws: app_id/secret + 可选 chats；longpoll: chats 必填；钉钉 dws: dws_cmd/profile/listen_kind/chat_query + 发送时暂停监听；stream: app_key/secret/robot/bot_id），优先使用 `ChannelValidator.ChannelFieldMetadata()` 的运行时字段/标签/必填/选项，缺省时用 `view.ChannelTransports` 静态表兜底。
  - 「检查连接」用**当前填写的值**调 `CheckChannelDraft`（off-frame，不保存）；`ValidateChannelDraft` 的错误内联显示；needs_auth 显示授权 URL + 打开/复制 + 登录命令复制；DWS 版本显示/缺失安装提示；状态只有运行时返回的 state（从不假装"已连接"）。
  - 保存：仅当检查 `ok` 才启用「保存并启用」；失败时提供显式的「仍然保存（未验证，可能连不上）」并 toast 警告；保存走既有 `SaveSettings(source, ...)`（enabled=true + transport + 本 transport 字段），成功后关闭向导/清检查、toast。
  - 无 `ChannelValidator` 时：卡片按钮禁用 + 说明"暂时不可用：运行时还没有接入连接向导"；字段可看，检查不可跑。
  - `dingtalk_pause_listen_on_send` 开关在钉钉卡片渲染（走 settingsPtr 保存协议），未投影时诚实提示。
- **统一收件箱投影**：新增 `view.AttentionVM/AttentionItemVM` + `Snapshot.PublishAttention/Attention()`；收件箱页优先渲染 runtime 的 attention items（approve/reject/answer/retry/open 按 Actions；ticket 行动作从已发布列表查行），未发布时回退 OverviewVM.Approvals/NeedsPerson（原行为不变）。
- 测试：`internal/ui/channelwizard_test.go`（无适配器禁用、per-transport 字段、检查用草稿值不保存、needs_auth URL/复制登录命令、成功后保存、失败后强制保存、metadata 覆盖标签、暂停开关保存）+ 既有 UI/view 测试全部更新为绿。
- Gates：`CGO_ENABLED=0 go test ./internal/ui/... ./internal/view/...` ok；`go vet` 干净；`gofmt` 空；`go build ./...` OK。

未做（明确推迟）：mygo 应用菜单项（窗口快捷键已有）、系统 TextScale/ReduceMotion/高对比主题（mygo Preferences 已可见，UI 尚未消费）、计划/任务可见性（待 runtime 投影）。

runtime 已发布 `AttentionVM`（items 与 AttentionCount/Overview.PersonCount 保持一致，runtime 测试断言）；收件箱已消费，仅在投影为空时回退 Overview 列表（UI 测试 `TestAttentionInboxConsumesPublishedProjection`）。`TicketActions`/`ExecutionPaths` 对应的 view 字段已就位（CanStop/ProjectRoot/ExecutionRoot/Artifact.Path）。IM `ChannelValidator` 适配器由 conf-arch/nih-runtime 提供，UI 已按契约探测。

---

# Round 5（rc2：流式/Markdown 聊天、计划面板、应用到项目、撤销拒绝、菜单、系统可达性、关于/更新）

范围：`internal/ui/**` + `internal/view/**`；无 runtime/cmd/store 改动，无 `go.mod`，无 commit。

## 已完成

1. **流式 + Markdown 聊天**：`renderMarkdown`（`internal/ui/markdown.go`）支持标题、粗体/斜体、行内 code、围栏代码块（mono + CodeBg）、无序/有序列表、链接（`Shell.OpenExternal` seam，只接受 http(s)）、换行；无 HTML。`ChatVM.Streaming` 存在时最新 assistant 行追加 `▍` 光标（`对话记录` 页与总览对话卡都走 Markdown），并出现「停止生成」（可选 `ChatStopper{StopChat() error}`，未接线则禁用 + 明说）；非流式时提供「再次发送上一条」。每条消息可复制。新增 `markdown`/streaming 测试与 link-open 测试。
2. **计划/待办面板**：新增 `view.PlanVM/PlanItem`（pending/running/done/blocked/failed、Counts()）+ `Snapshot.PublishPlan/Plan()`；执行详情内渲染「计划与待办」（状态图标 + 完成/进行中/需要你摘要 + 空态"还没有计划"），`对话记录` 页顶部显示同一计划的任务卡（compact，最多 4 项 + 剩余计数）。viewtest 新增 `DemoPlan`。
3. **应用到项目**：可选 `ApplyToProject{ApplyArtifacts(ticketID) ([]string,error)}`；详情「应用到项目」先弹确认（目标项目目录 + 文件列表/路径 + "会被覆盖" 警告 + 打开目标目录），确认后 off-frame 调用，pending/错误/成功 toast；未接线按钮禁用并明说。保留 项目目录/执行副本 的打开与复制。
4. **撤销拒绝**：可选 `UndoReject{UndoReject(decisionID) error}`；收件箱拒绝成功后 toast 带 10s「撤销」（真实调用后才显示；未接线不出现假按钮），撤销成功 toast 回收入件箱。
5. **原生应用菜单**（`internal/ui/menu.go`）：App（About/Hide/Quit）、文件（新建对话 ⌘N、设置… ⌘,、打开主窗口）、编辑（Undo/Redo/Cut/Copy/Paste/SelectAll 标准角色）、窗口（最小化/Zoom）；`Run` 里 `mygo.App.SetMenu`，回调主线程并 Invalidate；窗口内快捷键保留。
6. **系统可达性偏好**（`internal/ui/prefs.go`）：读取 `mygo.Theme.Preferences`（seam 便于测试）——字号 = OS TextScale × ui.font_scale（clamp 0.8–1.4）；ReduceMotion 时关闭消息流平滑滚动；HighContrast 时边框/次要文字提升到正文强度；系统 Accent 可用时覆盖品牌 accent（品牌色为兜底）。测试用 fake prefs 覆盖 4 项。
7. **关于/更新**：`view.SettingsAbout` + `关于` 设置组显示版本/许可证（MIT）；`检查更新` 走可选 `UpdateChecker{CheckForUpdate(ctx) (latest, upToDate, url, err)}`：已是最新/有新版本+打开发布页（Shell seam）/错误；未接线禁用并明说。测试覆盖 up-to-date、newer、unwired。
8. 测试：新增 `round5_test.go`（Markdown、流式光标/停止、计划详情/聊天卡/空态、应用到项目（含未接线）、拒绝撤销（含未接线）、系统偏好、关于/更新）；既有 ui/view 测试全部保持绿。

## 结果

```
$ CGO_ENABLED=0 go test ./internal/ui/... ./internal/view/... -count=1  → ok
$ CGO_ENABLED=0 go vet ./internal/ui/... ./internal/view/...            → 干净
$ gofmt -l internal/ui internal/view                                    → 空
$ CGO_ENABLED=0 go build ./...                                          → OK
$ sh scripts/check-nocgo.sh                                             → PASS (zero cgo)
```

## 未完成 / 待 runtime

- `view.AttentionVM` 已发布并被收件箱消费（runtime 已补 `PublishAttention`）；`PlanVM`、`ChatVM.Streaming`、`ApplyToProject`、`UndoReject`、`UpdateChecker` 均需 runtime 端适配器（UI 已按可选接口探测，缺失即诚实禁用）。
- macOS 菜单的角色在前端由 mygo 处理；headless 测试未覆盖真实菜单渲染（只编译级）。
- 更新检查的"最新版本号比较/协议"由 runtime/后端决定，UI 只显示其结果。

## Round 5 addendum（rc2 后端接口对齐）

- **ChatStreamer**（替代旧 ChatStopper）：`interface{ ChatStreaming() bool; StopChat() }`（与 runtime 的 `StopChat()` 无返回值一致）。UI 以 `ChatVM.Streaming || ChatStreaming()` 判断流式：显示 ▍ 光标与「停止生成」；点击 off-frame 调 `StopChat()`，toast「已停止生成」，runtime 追加「（已停止生成）」note；非流式才显示「再次发送上一条」。新消息取消旧流由 runtime 负责，UI 无需额外处理。
- **ArtifactApplier**：`interface{ ApplyArtifacts(ticketID string) ([]string,error); ArtifactRelPaths(ticketID string) []string }`。确认弹窗打开时 off-frame 读取 `ArtifactRelPaths` 并列出将写入的相对路径（无时回退 artifact 路径/标题），确认后 off-frame `ApplyArtifacts`，按返回数量 toast「已应用 N 个文件」；未接线禁用 + 明说。runtime 的备份（`.bak-<ts>`）/不安全路径跳过是后端行为，UI 只展示结果数量。
- **RejectUndoer**：`interface{ UndoReject(decisionID string) error }`；收件箱拒绝成功后仅当 cmd 实现它才显示 10s「撤销」。
- **UpdateChecker**：`CheckForUpdate(ctx) (latest string, upToDate bool, url string, err error)` 与 runtime 完全一致；新增 **VersionReporter**`{ Version() string }`，关于页优先显示 runtime 版本（否则用 ui.Version）。
- **模型组**：`view.SettingsWorker`（"worker"，label 模型）已是 RequiredSettingsGroups 的成员；当前静态组共 **7** 个：worker、source（连接）、eval、execution、about、prompt、deploy（people 由 runtime 发布，非静态骨架）。`dingtalk_pause_listen_on_send` 已由 runtime 发布，UI 卡片/向导读取并保存。
- Snapshot `Plan()` 消费 runtime 发布的 PlanVM（详情 + 对话页任务卡）；`AttentionVM` 已由 runtime 发布（`runtime.Publish → snap.PublishAttention`），收件箱优先消费它（含 stop 动作），仅在为空时回退 Overview 列表。
- 测试仍全绿：`go test ./internal/ui/... ./internal/view/...`、vet、gofmt、build。
