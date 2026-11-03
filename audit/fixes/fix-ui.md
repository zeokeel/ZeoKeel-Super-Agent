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
