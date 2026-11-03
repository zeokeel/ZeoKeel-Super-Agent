# fix-ui2 handoff (Wave 2 UI)

范围：`internal/ui/**`、`internal/view/**`（未触碰 runtime / engine / cmd / store / agent / design；未 commit、未 tidy、未加依赖）。
目标基线：`audit/fixes/wave2-spec.md` §4/§5/§6、`audit/fix-contract.md` §2.3、`audit/fixes/fix-ui.md`。

**结论：工作项 1–4 全部完成；工作项 5（Divider/Progress/Avatar）判定 defer（原因见末尾）；工作项 6 即本文件。**
关键前提已核实：fix-runtime 已实现与 UI 约定**完全一致**的四个方法（`internal/runtime/wrappers.go:17 AnswerAttention`、`:36 StopTicket`、`:59 RetryTicket`、`:78 PauseExecution`，另有 `:100 Paused()`），所以 `a.cmd.(interface)` 探测在生产路径会成立。

---

## 完成的条目

### 1. 总览动作（spec §4 第 1 条 / 审计 005 面板）

新增 `internal/ui/actions.go`：

- **Optional interfaces（定义在 ui 包，不 import runtime）**：
  `AttentionAnswerer{AnswerAttention(ticketID, answer)}`、`TicketStopper{StopTicket(ticketID, reason)}`、
  `TicketRetrier{RetryTicket(ticketID)}`、`ExecutionPauser{PauseExecution(paused)}`。
- **探测与诚实降级**：`actionChannels()` 对 `a.cmd` 做类型断言；缺失的通道让按钮 `.Disabled(true)`，
  并在"需要我处理"卡片里输出一行 `动作未接线：运行时未提供 回答（AttentionAnswerer）、重试（TicketRetrier）、停止（TicketStopper）`
  ——按钮不可点、原因具名到接口，不存在"点了没反应"。
- **stage-correct 动作**：规则在 `internal/view/ticket.go` 的 `ActionsFor(TicketRow)`（纯函数、表驱动测试）：
  `person/answer → 回答+停止`；`person/blocked|failed|delivery`（或投影 `CanRetry`）`→ 重试+停止`；
  `person/review|working|working/queued|idle → 停止`；`ended → 无动作`；未知 stage → 停止（运行时再校验）。
- **回答 Modal**（`answerDialog`）：标题 `回答 #42`、问题（`Attention.Question`，未投影时退回 row.Detail）、
  结构化选项按钮（`Attention.Options`）、自由文本输入（AutoFocus，Enter 提交、空文本无提交按钮）、
  `取消` + `提交回答`；失败时保留 Modal 并显示运行时错误（不假装成功）。
- **重试**：`retryTicket` 直接调用通道；失败显示在"需要我处理"卡片。
- **停止确认**（`stopDialog`）：`停止 #42？` + 撤销 Mandate 的说明 + `取消` / `确认停止`；
  reason 固定为 `操作员在总览停止`（`go: const stopReason`），让审计能看出停止来自面板。
- 动作只在总览：`personRow(row, withActions)`，最近任务行无动作；执行页无任何动作按钮（测试钉死）。

### 2. 工单详情（spec §4 第 2 条）

新增 `internal/view/ticket.go` 的投影类型 + `internal/ui/ticketdetail.go` 的渲染：

- `view.TicketDetailVM{ID,Title,Workspace,Stage,StageLabel,Tone,Source,Thread,Actor,Detail,
  Mandate *MandateDetail, Attention *TicketAttention, Assignments []AssignmentDetail,
  Inputs []InputDetail, Follows []FollowDetail, Artifacts, Delivery *DeliveryDetail, LastActivity, UpdatedAt}`，
  以及 `MandateDetail{Strength,PolicyVersion,Rules,Actor,Basis,Scope,Workspace,At}`、
  `ReportDetail`、`AssignmentDetail`、`InputDetail{Delivered,Read}`（投递三段式分开显示）、`FollowDetail`、`DeliveryDetail`。
- `TicketListVM` **新增** `DetailVMs map[string]*TicketDetailVM` + `DetailVMOf(id)`；旧 `Details` 保留（兼容只发短投影的 core）。
- 打开方式：点击总览行 → `openTicket` 导航到执行页并展开该行（`ui.ListState.ScrollTo`）；⌘K 命中工单本来就是
  选择并展开同一行，因此两条路径都到位。**设计 §6.3"没有详情页"保留**：详情是行内展开的专用区块（spec 允许 "dedicated section"），
  不是第三个页面。
- **诚实"暂无数据"**：`TicketDetailVM.EmptySections()` 列出投影缺失的段落；渲染时
  ① 有完整投影 → 逐段渲染（Mandate/Attention/派工与报告/输入/订阅线程/交付物/投递失败），
  只对 nil 的段落输出一行 `暂无数据：Attention`；
  ② 只有短投影 → 保留原有字段 + 一行
  `暂无数据：Mandate 依据 · Assignments / Reports · Inputs · Follows（运行时尚未投影完整详情）`。
- 打不开的工单（不在当前执行投影）不会乱开一行：`openTicket` 停在原页并说 `工单 #999 不在当前的执行投影里…`。

### 3. 设置五组（spec §4 第 3 条）

`internal/ui/settings.go` + `internal/view/pages.go`：

- 新增 `view.RequiredSettingsGroups()`：**来源 / 评估 / 执行 / 提示词 / 部署**五组及各自"将来会投影的事实"清单；
  `view.SettingsGroup` 新增 `Pending []string`。
- `settingsGroups(vm)` 合并：发布过的组用发布值（缺 Label/Note/Pending 时用骨架补），未发布的组以骨架出现；
  运行时多发布的组（如"人员"）保留、不丢。
- 每组新增 `pendingBlock`：`等待运行时投影：规则列表（id / 是否启用 / instruction / proactive）· 受理档位 accept mode · Policy 版本与变更历史`
  ——空表单不会被读成"没什么可配"。
- **部署组 = 运维台**（只读，无保存条）：日志路径与轮转、`zeokeel backup` 目标、`zeokeel archive` 语义、
  `zeokeel status` 退出码 `0 健康 · 1 可重启修复 · 2 组件错误 · 3 等待初次设置`、数据目录，加**实时健康行**（与总览同一份 `ov.Health`）。
  不伪造任何运行时数值。
- **执行组新增暂停开关**（`pauseControl`）：读投影 `Runtime.Paused/PausedKnown`；
  已投影 → `运行中/已暂停` + 单键 `暂停/恢复`；未投影 → `状态未知` + 两个方向按钮 + 一行
  `暂停状态未投影：运行时需发布 Runtime.Paused / PausedKnown（按钮仍可用）`（不猜、不假报"运行中"）；
  无 `ExecutionPauser` → 按钮禁用 + `暂停通道未接线：运行时未实现 ExecutionPauser`。命令发出后状态仍以投影为准（无乐观更新）。

### 4. Glossary（spec §4 第 4 条）

- `internal/view/glossary.go`：`Glossary()` 27 个术语（English / 中文 / 一句中文解释，取自 `_ref/docs/001-domain.md` §Glossary），
  `GlossaryValues()` 5 组取值表（Decision/Strength/Assignment/Report/Stage），`GlossaryLen()`。
- `internal/ui/help.go`：`名词表 / Glossary / 27` Modal，双列（术语+中文 / 解释），下接取值表；`Esc` 关闭；
  `⌘/`（Linux `Ctrl+/`）打开；**状态栏新增 `帮助 Ctrl+/` 入口**（可点击，平台感知文案），发现路径不依赖快捷键。

### 5. Wave-1 遗留清理（item 5）

**Deferred，未做**（理由见"未完成"）：Divider（27 处手绘 hairline 用的是品牌色 `tk.Border/BorderSoft`，`ui.Divider` 用主题 Border，
换掉会改变视觉细节）、Progress（总览占用条 `w*286` 字面量确实该换 `ui.Progress`，但高度/配色会变，且有更重要的 1–4 项）、
Avatar（自绘头像用品牌色与"你"字，`ui.Avatar` 自选配色）。报为 deferred，不在本波改。

---

## 改动文件（仅本人 scope）

生产代码：
- 新增：`internal/ui/actions.go`、`internal/ui/ticketdetail.go`、`internal/ui/help.go`；
  `internal/view/ticket.go`、`internal/view/glossary.go`。
- 修改：`internal/ui/app.go`（Action/Help 状态 + ⌘/ + 三个 overlay）、`internal/ui/overview.go`（动作列、行内导航、诚实提示、动作错误）、
  `internal/ui/execution.go`（短/全详情分派 + 短投影缺项说明）、`internal/ui/settings.go`（五组合并、pending、部署台、暂停、图标兜底）、
  `internal/ui/chrome.go`（状态栏帮助入口 + 平台快捷键文案）、`internal/view/vm.go`（TicketRow: StageID/Attention/CanRetry；
  Runtime.Paused/PausedKnown）、`internal/view/pages.go`（TicketListVM.DetailVMs/DetailVMOf、SettingsGroup.Pending、RequiredSettingsGroups）。
- 测试夹具：`internal/view/viewtest/fixtures.go`、`fixtures_pages.go` 补 `StageID`/`Attention`/`CanRetry`（形状与 runtime 契约一致）。

测试：
- 新增 `internal/ui/actions_test.go`（8）、`internal/ui/ticketdetail_test.go`（4）、`internal/ui/settings_groups_test.go`（7）、
  `internal/ui/help_test.go`（2）、`internal/view/ticket_test.go`（6）。
- 修正既有测试：`internal/ui/help_test.go` 用 `helpKeyHint()`；`settings_groups_test.go` 覆盖 pause 未知/已知两态。

规模：本波新增生产代码约 917 行（actions/ticketdetail/help/view ticket/glossary），新增测试约 751 行；
`git diff --stat -- internal/ui internal/view` = 34 files, +905/−929（含 Wave-1 的未提交改动；本波相对 Wave-1 的增量以新增文件为主）。

---

## 新增/修改的测试与结果

```
$ CGO_ENABLED=0 go test ./internal/ui/... ./internal/view/... ./internal/humanfmt/... -count=1
ok  github.com/zeokeel/super-agent/internal/ui        (82 PASS)
ok  github.com/zeokeel/super-agent/internal/view      (25 PASS, 含 verifier 的 6 条)
?   github.com/zeokeel/super-agent/internal/view/viewtest
ok  github.com/zeokeel/super-agent/internal/humanfmt  (5 PASS)

$ CGO_ENABLED=0 go vet ./internal/ui/... ./internal/view/...   → 干净
$ gofmt -l internal/ui internal/view internal/humanfmt          → 空
$ CGO_ENABLED=0 go build ./internal/ui/... ./internal/view/...  → OK
$ CGO_ENABLED=0 go build ./...                                  → FAIL（非本 scope）
$ sh scripts/check-nocgo.sh                                     → FAIL（同一原因）
    cmd/zeokeel/main.go:40 undefined: runCLI
    cmd/zeokeel/main.go:74 undefined: setupLogging
    cmd/zeokeel/main.go:268 undefined: serveHeadless
    ← fix-runtime 正在改 cmd/zeokeel（Wave2 新增 status/logging/headless）；与 UI 无关，未修。
```

新测试与它们证明的事：

| 测试 | 证明 |
| --- | --- |
| `TestOverviewOffersStageCorrectActions` | answer 行只有 1 个"回答"、4 个非 ended 行各 1 个"停止"；stage→动作规则生效 |
| `TestAnswerDialogSendsTheChosenOption` / `...SendsFreeText` | 回答 Modal 显示问题与选项；点选项/自由文本都经 `AttentionAnswerer` 发出（`42\|答案`） |
| `TestRetrySendsThroughTheChannel` | blocked 行 "重试" → `TicketRetrier("44")` |
| `TestStopAsksThenSends` | "停止"先弹确认、取消不发、确认后 `TicketStopper("42\|操作员在总览停止")` |
| `TestMissingActionChannelsAreDisabledWithAReason` | 四个通道缺失时按钮禁用、行内具名原因；点禁用按钮不产生任何状态 |
| `TestActionErrorIsShown` | 运行时错误显示在原处；空回答不可提交；Modal 不因失败而关闭 |
| `TestActionsExistOnlyOnOverview` | 执行页 0 个动作按钮（动作只在总览） |
| `TestTicketDetailShowsTheFullProjection` | Mandate（strength/policy/rules/basis/scope）、派工+报告、Inputs（已读/已投递分开）、Follows、Artifacts 全渲染；仅 Attention 为空 → 只报 `暂无数据：Attention` |
| `TestTicketDetailShortSaysWhatIsMissing` | 只有短投影时仍显示旧字段 + 具名缺项一行 |
| `TestOverviewRowOpensTheTicketOnExecution` | 点总览行 → 执行页 + 该行展开（不是随机行） |
| `TestOpenTicketOutsideTheProjection` | 不存在的工单不开行、留在原页、给出原因 |
| `TestSettingsPageShowsTheFiveGroups` | 五组恒在 |
| `TestDeploymentGroupIsTheOperatorsConsole` | 日志路径/backup/archive/退出码 0-3/健康行都在；无保存条 |
| `TestSettingsGroupSaysWhatItWaitsFor` | 评估组的 `等待运行时投影：` + 具名事实 |
| `TestMissingSettingsGroupAppearsAsProjectionGap` | 只发布 1 个组时其余 4 组以骨架出现；已发布组不被塞假字段 |
| `TestPauseControlSendsAndFollowsTheProjection` | 未投影=`状态未知`+双向按钮；投影后=`运行中`+单键；`暂停/恢复`发送正确布尔；无乐观更新 |
| `TestPauseControlUnwired` | 无 ExecutionPauser → 禁用 + 具名原因，点击无副作用 |
| `TestHelpOpensWithShortcutAndShowsGlossary` / `...FromTheStatusBar` | ⌘/ 与状态栏都能开；双语言术语与取值表可见；Esc 关闭 |
| `internal/view/ticket_test.go` 6 条 | `ActionsFor` 全 stage 表、未知 stage/CanRetry、`EmptySections`、glossary 完整性/去重、`RequiredSettingsGroups` 五组与 Pending |

人工核对：临时渲染测试（已删除）导出并查看了 总览动作列 / 回答 Modal / 工单完整详情 / 部署设置 / 执行设置(暂停) / 名词表
（`/tmp/w2-*.png`），布局与中文字体正常；状态栏因新增帮助入口会换行的问题已用 `SingleLine()` 修掉。

---

## 行为变化与兼容性风险

1. **总览行多了动作按钮**（回答/重试/停止）并可点击行内导航到执行页；"最近任务"行没有动作。行高不变。
2. **运行时还没投影新字段时，生产面板只会显示"停止"**：`TicketRow.StageID` 为空 → `ActionsFor` 只给停止（诚实兜底）。
   回答/重试要在 runtime 填 `StageID`（+`Attention`/`CanRetry`）后出现。这是本波最重要的跨 scope 项（下方第 1–2 条）。
   注意：`TestOverviewOffersStageCorrectActions` 用构造数据，不依赖 runtime。
3. **暂停状态**：`Runtime.PausedKnown=false` 时显示"状态未知"并给两个方向按钮；runtime 发布后自动变为单键 + 真实状态。
4. **详情**：有 `DetailVMs` 时优先全量渲染，旧 `Details` 仍是 fallback；`DetailVMOf` 为 nil 时输出具名缺项一行，不画空盒。
5. **设置**：rail 恒有五组（另保留 runtime 多发布的组）；部署组无保存条；每组出现 `等待运行时投影：…` 一行（当 Pending 非空）。
   旧的 `TestSettingsPageFormsAndSaveBarOnlyWhenChanged` 等仍通过（发布组的值/保存条逻辑未变）。
6. **新增快捷键 Ctrl+/（macOS ⌘/）** 全局打开名词表；与 ⌘K 不冲突；焦点在输入框时 mygo Shortcut 仍会触发（与 ⌘K 同机制）。
7. **view API**：新增导出类型/函数（TicketAction、ActionsFor、TicketDetailVM 家族、Glossary、RequiredSettingsGroups、DetailVMs/DetailVMOf、
   Runtime.Paused/PausedKnown、TicketRow.StageID/Attention/CanRetry、SettingsGroup.Pending）。没有删除或改签名，旧代码不受影响。
8. 未新增依赖、未改 `go.mod/go.sum`、未改 `CGO_ENABLED` 语义。`internal/ui/verify_ui_test.go`（verifier-A 所有）未改动且全绿。

---

## 跨 scope 待办（file:line + 期望改动）

### runtime（fix-runtime）—— UI 已就绪，只差投影字段

1. **总览行**：`internal/runtime/runtime.go:178-187`（NeedsPerson 的 `view.TicketRow{...}`）加
   `StageID: string(st)`（同函数 `st := domain.StageOf(&t, now)` 已有）、
   `Attention: &view.TicketAttention{ID: ..., Question: t.Attention.Question, Options: t.Attention.Options, AskedAt: t.Attention.At}`
   （`t.Attention` 非 nil 时）、`CanRetry: t.CanRetry()`。没有这三项，面板不会出现"回答/重试"按钮。
2. **执行页行**：`internal/runtime/pages.go:270-276`（ticketsVM 的 `view.TicketRow{...}`）加 `StageID: st`
   （同函数已有 `st := string(domain.StageOf(&t, now))`）。执行页本身不用动作，但详情/阶段高亮与未来动作需要。
3. **完整详情投影**：`internal/runtime/pages.go:257` 的 `TicketListVM` 现在是
   `&view.TicketListVM{Details: map[string]view.TicketDetail{}, ...}`，请**追加** `DetailVMs: map[string]*view.TicketDetailVM{}` 并逐工单填充：
   `Mandate`（`t.Mandate.Strength/PolicyVersion/Rules/Basis/Scope/Workspace/Actor`）、`Attention`、`Assignments`（含 `Report`）、
   `Inputs`（`t.Inputs`，Delivered/Read 分开）、`Follows`（`t.Follows`，ClosedAt 指针）、`Artifacts`、`Delivery`。
   字段语义与字段名见 `internal/view/ticket.go`（已加 doc）。旧 `Details` 可同时保留。
4. **暂停投影**：`internal/runtime/runtime.go` 构造 `view.OverviewVM` 处（约 `:120-145`）
   加 `ov.Runtime.Paused, ov.Runtime.PausedKnown = r.Paused(), true`（`runtime/wrappers.go:100` 已有 `Paused()`）。
   不发布则设置页显示"状态未知"（诚实兜底，但会多两个按钮）。
5. **（Wave1 遗留）** `internal/runtime/runtime.go:400` `view.Thread{Head: inboundLine(t)}` 仍未设 `ID`：
   `internal/ui/stream.go` 的 `threadKeys` 在 ID 为空时退回索引 key；补 `ID: string(t.ID)` 即启用按 ID 定位。
6. **（Wave1 遗留，非 UI）** 执行页文案/注释说"按最近活动排序"，`ticketsVM` 仍未排序；runtime 侧决定排序或改文案。

10. **（建议，cmd/runtime）** 加编译期断言，防止运行时方法签名漂移让面板静默降级：
    ```go
    // cmd/zeokeel 或 internal/runtime
    var (
        _ ui.AttentionAnswerer = (*runtime.Runtime)(nil)
        _ ui.TicketStopper     = (*runtime.Runtime)(nil)
        _ ui.TicketRetrier     = (*runtime.Runtime)(nil)
        _ ui.ExecutionPauser   = (*runtime.Runtime)(nil)
    )
    ```
    当前四个方法签名已逐一核对一致（`internal/runtime/wrappers.go:17/36/59/78`），但只有编译期断言能把"一致"钉住。

### brief / runtime（Wave1 遗留，非本波）

7. `internal/brief/digest.go:161 humanAge`、`internal/runtime/pages.go` 的 `humanAge/clip/humanDuration` 仍应改用
   `internal/humanfmt`（Age/Ago/Duration/Clip），消除"12 分前 / 12 分钟前"两种措辞。

### docs（fix-docs-final）

8. 名词表新增了 `?`/⌘/ 入口与五组设置的"等待运行时投影"说法；文档若要截图/描述面板，可据此更新（代码在
   `internal/ui/help.go`、`internal/ui/settings.go`）。
9. Wave2 已确认：桌面无 HTTP 管理 API 是 documented-deviation；`zeokeel status` 退出码在设置页部署组里作为固定契约展示。

---

## 未完成 / 被阻塞

- **无阻塞**：全部按 optional interface + view struct 实现，不依赖 runtime 先落地。
- **工作项 5 Deferred**：Divider / Progress / Avatar 三处，理由见上文；建议 Wave 2 收尾或标 documented-deviation。
  若要求 Progress：`internal/ui/overview.go` 的 `w*286` 一行替换即可（低风险，但会改变占用条高度，需再截图确认）。
- 未跑 `-race`（CI 专用步骤需 CGO=1，且当前 `go build ./...` 被 fix-runtime 的 in-flight cmd 改动挡着）。
- 全仓 `go build ./...` / `check-nocgo.sh` 当前红在 `cmd/zeokeel`（`runCLI`/`setupLogging`/`serveHeadless` undefined），
  **非本 scope、非本次改动**；UI/VIEW/HUMANFMT 三包 build+test+vet+gofmt 全绿。
