# 独立验证：fix-all 冻结轮（design-fix-verify）

> 角色：conf-domain（独立验证，只读生产代码）。验证对象：HEAD **未提交工作树**（2026-11-04 快照）。
> 方法：读 handoff（fix-runtime/fix-agent/fix-integrations/fix-ui/fix-ui2/fix-store）+ 静态核对每个声明的实现与测试 + 运行指定闸门 + 针对声明的反证（测试是否在断言"正确行为"、代码是否被接线）。
> 本文件之外未改动任何文件；临时日志在 `/tmp/v_*.log`。

## 1. 机械闸门（本机实测）

| 命令 | 结果 | 证据 |
| --- | --- | --- |
| `CGO_ENABLED=0 go build ./...` | **PASS** | exit 0 |
| `CGO_ENABLED=0 go test ./... -count=1 -skip Live` | **PASS** | 27 包 ok，0 FAIL（`/tmp/v_test.log`） |
| `sh scripts/verify-fixes.sh`（含全仓 `-race`） | **PASS 5/0** | 含 `PASS CGO_ENABLED=1 go test -race ./... -skip Live`（`/tmp/v_verifyfix.log`） |
| `sh scripts/check-nocgo.sh` | **PASS** | `check-nocgo: PASS (zero cgo)`，四步全过 |
| `sh scripts/release-check.sh` | **PASS 17/0/6 skip** | `机械检查全部通过`（`/tmp/v_release2.log`） |
| `gofmt -l` / `go vet ./...`（由 verify-fixes 覆盖） | PASS | 同上 |

> 注：首次 release-check 失败是验证者调用时丢了 `PATH=/usr/local/go/bin`（`verify-fixes.sh` 内 `go: not found`），带 PATH 重跑为 PASS；**不是仓库问题**。

## 2. 逐项判定

| # | 声明（Lead 清单） | 判定 | 关键证据 |
| --- | --- | --- | --- |
| 1 | P0-1/4 设置布尔编码/三态/迁移/legacy 不静默禁用 | **FAIL（部分）** | 编码器在（`settingscodec.go:17,28`）；三态在（`im.go:42,50,113-118,245-252`；`TestIMEnabledTriState` PASS：derived/explicit 0/flags 不覆盖/stream≠dws）；**但**：UI switch 读 `*ptr=="true"`、写 `strconv.FormatBool`（`ui/settings.go:405,415`），而 runtime 发布 `"1"/"0"`（`runtime/pages.go:555-559`）→ 所有 switch 在启用时显示为关；`migrateSettings`（`settingscodec.go:81-140`）与 `SetSettingsSchema`（`store/settings.go:52`）**零调用方**（全仓 grep）→ "LoadSettings 归一 + 写 schema + legacy enabled 迁移" 未接线。见 §3 V-1/V-2。 |
| 2 | P0-2 UI 命令离开主线程 + commandMu + 事务 SaveSettings | **PASS** | `ui/commands.go:24-105` 四个命令都 `runOffMain` + `postToUI`；`runtime/runtime.go:81-84` commandMu，`pages.go:567-660` SaveSettings 先构 next 再 CAS，失败不改进内存/rev（`TestSettingsConflictAcrossWindows`、`store.TestSaveSettingsRejectsStaleBase`、`TestConflictLeavesDatabaseUnchanged`）。残余：Approve/Reject/Answer/Stop/Retry 仍在帧内同步（见 V-4）。 |
| 3 | P0-3 投影行 ID=domain ID、UI 按 ID 打开/工作区会话 | **PASS** | `runtime/pages.go:1094` `ID=string(t.ID), Number=t.Number`；`pages.go:302/310` detail 按 domain ID；UI `ticketdetail.go:33,62`、`execution.go:114-161`、`round3_test.go:285,292`（`tk-42` vs `#42` 不混用）PASS。 |
| 4 | P0-7 ChatVM 发布/渲染/无红字/末条 outcome/模型+确定性回退/有界持久化 | **PASS** | `runtime/runtime.go:304` PublishChat；`runtime/chat.go:25,29-58,66-99`（20 条上限、store AppendChat/Chat 持久化、模型→确定性回退）；UI `chat.go:153`、`round3_test.go:220-260`（`TestChatAnswerIsNotAnError`、`TestChatPanelIsBounded`）。残余：回答模型调用占 commandMu 最长 45s（V-5）。 |
| 5 | IM 审批（全部入站待批） | **PASS** | engine：`gate.go` imSources/AutoApproveIM、`evaluate.go proposeIMLocked`（每条 Decision、无 Ticket/launch/reply）；engine 6 测试全 PASS（`TestIMExplicitBecomesProposal/.../TestIMProposalsSurviveRestart`）；runtime `im_approval_test.go:55` `TestIMMessagesBecomePendingApprovals` PASS；cmd `-race`+全仓测试 PASS。 |
| 6 | 官方接收（Feishu ws + 长轮询 fallback；DingTalk Stream SDK；DWS 长驻单 consumer；Close/Stop） | **PASS** | `im/feishu/ws.go:25,70,73`（larkws + `OnP2MessageReceiveV1`）；`runtime/dws.go:209-225` + `im.go:788` 就绪判定；longpoll 显式 fallback（`feishu/driver.go:70,97,105`、`im.go:87,767`）；`im/dingtalk/stream.go:28-29` 官方 SDK；`im/dingtalk/dws.go:9,845-851` `event +listen-im --flatten -f ndjson`（无 poll，全仓无 `event poll`）；`runtime/im.go:1088-1100` 游标命名空间；`im2_test.go` 14 条 IM 测试全 PASS（含 Stop 关闭/注销、单 consumer 复用、并发 Apply、ws readiness）。 |
| 7 | AGENTS.md 不可信封装/64KB/不用 eino agentsmd；plan 目录；web fetch 固定 IP/Proxy nil/envelope；web_search 转义/cache/rate | **PASS** | `agent/builtin.go:30-41,137-141`（自定义 middleware，未 import eino agentsmd——全仓 grep 空；64KB `:34`）；`:71` `.zeokeel/plan`（`features_test.go:719`）；`webtools.go:12-13,145`（Proxy nil）、`:164-213`（IP pin+rebind）、`:294-388`（scheme/private/CT/限流）、`:581` QueryEscape、`:467-560` cache+rate；对应 8 条 `TestWebFetch*`/`TestWebSearch*`/`TestAgentsMD*` PASS。 |
| 8 | allowlist 保字段；Inspect/status/VM 脱敏；opaque ws id/dup root/rename/delete；workspace filter/ownership；trigger scope/id/time/self；异步 ReceiveTrigger；onboarding sources | **PASS（1 项残余）** | `store/policy.go:108-140` + `policy_test.go:68`（只改 Allowed）；`store/redact.go` + `redact_test.go:80,164`（Inspect/health 脱敏），`runtime/pages.go:549,626`（secret 不回显/空=保留）；`runtime/workspaces.go:15,47-57,109-230`（随机 `ws_` ID、同 root 拒绝、Rename 稳定 ID、Delete 有单拒绝）；workspace filter（`ui/execution.go:217-224` + `round3_test.go:310`）；`runtime/entry.go:18-45` + `kickEval`（异步）；`onboarding.go:236-262`（source_kind→真实 source.* + SaveSettings）。残余：IM `Trigger.Self` 从不赋值（V-3）。 |
| 9 | DWS/Feishu 错误/永久错误；游标命名空间；sender 注销 | **PASS** | `cmd/zeokeel/imwiring.go:42-95` permanentAwareSender（永久→Gone，Canceled 可重试）；`runtime/im.go:1043-1100`（namespaced cursor + legacy 迁移 + transport 变更清 cursor；`TestIMCursorNamespacedAndLegacyMigrated` PASS）；`im.go:758-760` + Stop/Disable 注销 sender（`TestIMStopClosesDriverAndWithdrawsSender`、`TestIMDisableWithdrawsSender` PASS）。 |

## 3. 发现的问题（按严重度）

### V-1（P0，用户可见）：UI switch 编码与 runtime 不一致，启用的开关显示为"关"
- runtime 发布：`view.SettingSwitch` 的值统一为 `EncodeSettingBool` → `"1"/"0"`（`internal/runtime/pages.go:555-559`，`settingscodec.go:28`）。
- UI 渲染：`on := *ptr == "true"`；拨动后 `*ptr = strconv.FormatBool(on)` → 写 `"true"/"false"`（`internal/ui/settings.go:405,415`）。
- 后果：飞书/钉钉启用、计划/网络工具等所有 switch，在真实投影下**显示为关**（点一次把已启用项写成 `"true"`，仍能工作但语义/显示错）；与 P0-1/P0-4"switch 统一 1/0"的声明相反。
- 为什么测试没抓到：UI 测试夹具用 `Value:"true"`（`internal/ui/round3_test.go:423-427`、`channels_test.go:194`），**断言了旧编码**，属"测试掩盖/断言错误行为"。
- 建议修复（UI scope）：读 `ParseSettingBool` 语义（或 view 提供 helper），写 `"1"/"0"`；夹具改 `"1"` 并加一条"投影值 1 → 开关为开"的测试。

### V-2（P1，声明不实）：设置迁移/schema 打点是死代码
- `migrateSettings`（`internal/runtime/settingscodec.go:81-140`）与 `SetSettingsSchema`（`internal/store/settings.go:52`）在全仓**没有任何调用方**（grep 仅定义+store 测试）。
- `LoadSettings`（`runtime/pages.go:675-714`）只读入 settings、chat、rev，不调用迁移、不写 `setting.__schema`。
- 影响：handoff P0-1 的"LoadSettings 归一既有 bool 键、写 SettingsSchema(1)、旧安装 IM enabled 迁移、`im_self` 拆分"**未接线**。功能上被 `IMConfigFromSettings` 的凭据推导兜底（`im.go:245-252`：无 enabled 键时按凭据/transport 推导；`SelfID` 回退读旧 `im_self`），所以 legacy IM 不会立刻静默禁用；但"schema 版本/归一持久化"的承诺是假的，且没有测试覆盖迁移本身。
- 建议：在 `LoadSettings` 里调 `migrateSettings` 并用 `store.SaveSettings` 持久化变更 + `SetSettingsSchema`；加 `TestLegacySettingsMigration`（旧 `"true"/"false"` 键、只有凭据的 feishu/dingtalk、旧 `im_self`）。

### V-3（P2，语义残余）：`Trigger.Self` 在 IM 路径从不赋值
- `runtime/im.go:975-993` 构造 IM Trigger 时不设置 `Self`；`selfID` 参数仅用于 `@` 判定（P1-8 的"分渠道 self id"本身 PASS）。
- 后果：操作员本人在 IM 里发的消息在领域事实里不是"自己"；`domain.Trigger.IsOperatorMessage()/Ticket.CreatedByOperator()`（本轮新增）未被 runtime 使用；`conversationVM`（`pages.go:1077-1090`）用 `tr.Self || tr.Source=="local" || Mandate.Actor=="operator"` 自行判定，经 IM 批准创建的工单会归到"超级Agent 创建的"。当前 IM 全量审批使该事实不承重（授权在批准时发生），但审计/归属语义不精确。
- 建议：IM 管理器在把 RawEvent 翻译成 Trigger 时，用渠道 self id 置 `Self`（或在 Approval 时把 operator 身份写入 Mandate/Audit）。

### V-4（P2，P0-2 的边界）：动作按钮仍在 UI 帧内同步
- `ui/actions.go:227,242,296`（Answer/Retry/Stop）与 `ui/overview.go:419,430`（Approve/Reject）在帧内直接调 runtime；都是 SQLite 事务（无模型），当前体量无卡顿，但不符合"命令一律离帧"的通用化表述。属文档/后续项，非 P0-2 清单四条命令的违反。

### V-5（P2，响应性）：chat 回答的模型调用占着 commandMu
- `runtime/chat.go:66-84` 在 `Dispatch`（持 `commandMu`）内同步调模型，最长 45s；帧不冻结（离帧），但期间其他 UI 命令排队。建议超时/并发策略或把 chat 回答移出 commandMu。

## 4. 结论

- **机械闸门全绿**：build / 全仓测试 / 全仓 -race / 零 cgo / release-check（17/0）均 PASS。
- **10 项声明中 8 项 PASS**；**1 项 FAIL（P0-1 UI switch，用户可见且被测试夹具掩盖）**；**1 项部分 FAIL（P0-1 迁移/schema 死代码，声明不实但有推导兜底）**；另有 3 条语义/响应性残余。
- 最需要先修：V-1（UI switch 编码 + 夹具），其次 V-2（接上迁移与 schema 打点并补测试）。

## 5. 复现命令

```sh
export PATH=/usr/local/go/bin:$PATH CGO_ENABLED=0
go build ./...
go test ./... -count=1 -skip Live
sh scripts/verify-fixes.sh      # 含 CGO_ENABLED=1 go test -race ./... -skip Live
sh scripts/check-nocgo.sh
sh scripts/release-check.sh
# 反证检索
grep -rn "migrateSettings" internal/ cmd/ --include=*.go   # 只有定义
grep -rn "SetSettingsSchema" internal/runtime --include=*.go  # 空
sed -n '403,416p' internal/ui/settings.go                   # *ptr=="true"
sed -n '553,560p' internal/runtime/pages.go                 # EncodeSettingBool
```


## 6. 修复后复验（V-1 / V-2）

工作树更新后二次验证（2026-11-04，仍只读生产代码；临时探针已删除）。

### V-1（UI switch 编码）— **PASS**
- 代码：`internal/ui/settings.go:405,415` 改为 `on := settingBool(*ptr)` / `*ptr = boolText(on)`；
  `internal/ui/channels.go:382-397` 提供 `boolText`（写 `"1"/"0"`）与 `settingBool`（读 1/true/yes/on/enabled）；
  `channels.go:162,175` 也走同一 codec。fixtures 已改 `Value:"1"`（`round3_test.go:423,427`、`channels_test.go:194`）。
- 探针（临时 `internal/ui/zz_verify_probe_test.go`，已删除）：`settingBool` 对 `1/true/TRUE/yes/on/enabled/" 1 "` 全 true，
  对 `0/false/""/no/off/disabled/garbage` 全 false；`boolText(true|false)=="1"|"0"` → **PASS**。
  渲染/点击路径按代码静态核对：投影值 "1" → `on=true`（开关开）；拨动写 "1"/"0"；legacy "true" 同路径渲染为开。
- 测试：`go test ./internal/ui ./internal/view` → ok（4.8s / 0.005s）。
- 残余：没有专门断言 switch 控件 on/off 的 UI 测试（codec 有探针、渲染为静态核对）；建议后续补一条投影→开关状态测试。

### V-2（设置迁移/schema 打点）— **PASS**
- 代码：`internal/runtime/pages.go:693`（`migrateSettings(values)` + 持久化 changed）与 `:705`
  （`st.SetSettingsSchema(store.SettingsSchemaVersion)`）已接线；`settingscodec.go:83` 迁移实现未变。
- 测试：`TestLegacySettingsMigration`（`internal/runtime/migration_test.go:14`）覆盖 bool 归一（`"true"→"1"`）、
  legacy feishu/dingtalk 凭据→enabled=1、legacy dingtalk transport 固定为 stream、`im_self` 拆分到
  `feishu_self_id`/`dingtalk_bot_id`、schema 打点；`go test ./internal/runtime -run TestLegacySettingsMigration` → **PASS**。

### 重新跑闸门（修复后）
| 命令 | 结果 |
| --- | --- |
| `CGO_ENABLED=0 go build ./...` | **PASS** |
| `CGO_ENABLED=0 go test ./... -count=1 -skip Live` | **PASS**：27 包 ok / 0 FAIL（`/tmp/r_test.log`） |
| `sh scripts/verify-fixes.sh` | **PASS 5/0**，含 `CGO_ENABLED=1 go test -race ./... -skip Live` PASS（`/tmp/r_verifyfix.log`） |
| `sh scripts/release-check.sh` | **PASS 17/0/6 skip**（`/tmp/r_release.log`） |

### 回归点核对（Lead 点名的四处）
1. **Feishu 默认 ws**：确为当前默认（`runtime/im.go:87,767`、`pages.go:474-476` 选项 ws/longpoll，`im_test.go:86` 断言
   `ws`+`dws` 默认）。**风险（非测试失败）**：`migrateSettings` 不为 feishu 固定 transport，旧 longpoll-only 部署升级后
   无 `source.feishu_transport` 键会默认切到 ws；若该飞书应用未开长连接会变成渠道错误。建议迁移里对"有 feishu 凭据但无
   transport 的旧安装"固定 `longpoll`（或文档写明默认变更）。
2. **font_scale 字段**：仍在（`runtime/pages.go:537`，设置组可编辑，`settings_test.go:187` 断言可写）→ PASS。
3. **DeleteWorkspace 空删除**：`store.DeleteWorkspace`（`store/store.go:390`）存在，`runtime/workspaces.go:151-190`
   空工作区走删除并 `eng.Unregister`；带工单拒绝。**残余**：runtime 层没有"空工作区成功删除"的回归测试（UI 仅 fake）。
4. **TicketDetailVM ID**：`pages.go:310` 以 `string(t.ID)` 建 map，`view/pages.go:387-391` 按同 ID 查；UI
   `ticketdetail.go:33,62` 与 `TestConversationOpensTicketByDomainID` 等 PASS → 无回归。

### 结论（复验）
V-1、V-2 均已修复且有测试/探针证据；全部门禁复跑通过，无由这两处修复引入的回归。仅 3 条残余建议：
feishu 旧部署 transport 迁移、UI switch 状态断言测试、DeleteWorkspace 成功路径测试。
