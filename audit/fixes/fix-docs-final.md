# fix-docs-final handoff — 最终文档 pass（Wave 2 后）

日期：2026-11-03 20:35（**快照**；仓库在此期间仍被其他 agent 并发修改，见 §5）
角色：fix-docs-final（conf-product）　scope：docs / CI / scripts / packaging / Makefile / VERSION / .gitignore（未改任何 `internal/**`、`cmd/**`、`go.mod`；未 commit）

## 1. Wave-1 验证者指出的 8 项（D-1…D-8）状态

| # | 问题 | 处理 | 证据 |
| --- | --- | --- | --- |
| D-1 | "`cmd` 不 import `internal/im`" 写错；正确是"驱动包未 import、`startIMLoop` 无调用者" | **已修，且按最新代码再次校正为"IM 已接线"**（Wave 2 在 20:30 后新增 `cmd/zeokeel/imwiring.go`：import 了 `internal/im/{feishu,dingtalk}`，`startIMLoop` 在 `extras.go:130` 有调用者，`main.go:288` 启动 `startConfiguredIM`，`-feishu-*`/`-dingtalk-*` flag 与设置›来源 8 字段均可配）。README/CHANGELOG/RELEASE/RELEASE-STEPS/design §13 §4.8/§8.2/ops-runbook/cookbook 全部改为当前事实；不再有"cmd 不 import / 驱动未 import / 无 flag"的错误表述 | `cmd/zeokeel/imwiring.go:14-15,74-130`；`main.go:63-69,288`；`extras.go:130,163`；`go list -deps ./cmd/zeokeel` 含 `internal/im/{feishu,dingtalk}` |
| D-2 | design:1549/1910 stale "EnsureV3/schema_v3/SchemaVersion=2" | 已修：§7.1 现状段改为 `SchemaVersion=3`（`internal/store/schema.go:17`）、v3 表折入有序迁移、`schema_v3.go/EnsureV3` 已删除；§13 §7 行同改；"明确未做"里的 v3 项删除 | `internal/store/schema.go:17,180-260`；`ls internal/store/schema_v3.go` → No such file |
| D-3 | §13 ✅ 行证据行 stale（StageOf、replyPlanLocked） | 已修并**逐行重跑**：`StageOf` → `internal/domain/ticket.go:516`；`replyPlanLocked` → `internal/engine/evaluate.go:568`；其它更新：gate `engine/gate.go:184`、Eval `evaluate.go:19`、status `runtime/status.go:104`、CLI `cli.go:74/141/209`、ops `ops.go:46/83`、logging `logging.go:27`、supervisor `supervisor.go:98`、tokens `ui/tokens.go:141-200`、stream `runtime/runtime.go:412` 等 | 逐项见 design §13 |
| D-4 | README 写"托盘暂停运行时是空菜单项"（已被 fix-ui 删除） | 已修：README/CHANGELOG 不再提该 bug；改为"暂停/恢复执行在设置页/runtime `PauseExecution`"，托盘只有"打开面板/退出"（`internal/ui/tray.go:10-28`） | `internal/ui/tray.go:10-28`；`internal/runtime/wrappers.go:78` |
| D-5 | CHANGELOG "schema v2" | 已修：改为 **v3**（v3 表折入有序迁移） | `CHANGELOG.md` 可靠性段 |
| D-6 | `bufferToolCallChecker`/`engine_react` 引用已删除实现 | 已修：CHANGELOG 与 `design/M0-tasks.md:214` 均改为"Wave 2 迁到 eino 官方 `patchtoolcalls`，旧自研 checker/engine_react 已删除"；design §5.2/§13 同口径 | `internal/agent/engine_adk.go:118`；`ls internal/agent/engine_react.go` → 不存在 |
| D-7 | RELEASE C 把测试标 [x] 早于事实 | 已修：A/C 组加"快照说明"；C 的 build/test/vet/nocgo 改为按最终重跑再确认，`-race` 保持 [ ] 并写清失败；只保留已实测的 package/产物 [x] | `RELEASE.md` A/C 组；本文件 §5 |
| D-8 | "11 张截图" → 实际 16 张 + contact sheet | 已修：CHANGELOG 写 16 张（00-flow … 15-task-inbox）+ 1 contact-sheet；design §6 intro/附录 B 保持 16 界面 | `CHANGELOG.md` 仓库清理段；design:1157 |

## 2. 本轮完整 truth pass 的改动（文件）

- `README.md`：整页重写（当前状态表按 Wave 2 事实、CLI 表、IM flag/设置、日志/status/supervisor/systemd、验证命令、依赖、完整"已知限制"）。
- `CHANGELOG.md`：`[1.0.0-rc.1]` 段按 Wave 2 重写（两阶段 Eval/生命周期/恢复/eino 工具/面板/运维/提示词 6 层/IM 接线），1.0.0 定义与"已实现"段校正；D-1/D-5/D-6/D-8 全修。
- `RELEASE.md`：A–G 重写；A 附快照说明；F 组按 owner 重排（10 项）；G 组定义 rc 语义。
- `RELEASE-STEPS.md`：Wave 2 开头；新增可执行的运维冒烟（setup/status/backup/headless）；IM 段改为"已接线、真实凭据未验证"；已知限制按最新事实。
- `design/zeokeel-go-mygo-design.md`：§4.1 目标 vs 现状（Eval/Input/Follows/Admission/Rule 等已落地，剩余 Source/Thread/UserError）；§6.5 深浅色 ✅；§6.7 三列真实现状（store 锁早于恢复、supervisor/status/日志 ✅、自启/唤醒 ❌、托盘重启 ❌）；§7.1 v3 校正；§7.3 归档 ✅（archive CLI + `ops_test.go:149`）；§8.1 模式/六子命令；§8.2 向导落库/IM 可配/GitHub 仍 flags；§8.3 打包实测（12–13MB 包、33/32MB 二进制）；§8.5 可观测性/CLI/supervisor/日志/退出码；ADR-12 现状（keychain 未做/env 未剥离）；§13 全表重写（每 ✅ 带 live file:line，其余 🟡/❌ 带原因，含 §1.2/附录 A、§6.3、008 扩展路径）；附录 B 增加 ops-runbook/how-to-add-a-source/rejected-concepts。
- `design/ops-runbook.md`：整页重写为可执行 runbook（status 0/1/2/3、JSON 形状、日志路径/轮转、supervisor、备份/恢复/归档、systemd/launchd、排障、巡检）；旧的 "requires Wave-2 CLI (pending)" 全部移除，未实现项显式写"未实现"。
- `design/how-to-add-a-source.md`（新）：Go 版 008 cookbook（Kind/Poller/Channel/MemberLister/HealthReporter、配置/注册、`Runtime.ReceiveTrigger`、游标 kv、Policy part/admission 行/bots/rules、回写 outbox、测试清单、以及 §8 "今天还缺什么"）。
- `design/rejected-concepts.md`：新增 C 节（Wave 2 概念登记：RequireApproval、两阶段 Eval、archive、CLI、supervisor 只算半替代、eino 迁移删自研、Runner.Resume 保留但生产未用、ReplyPlan 语义债、glossary、demo 数据纪律、IM 专用接线过渡形态、提示词 6 层 base-hash 未接线等）。
- `packaging/systemd/zeokeel.service`、`packaging/launchd/com.zeokeel.agent.plist`：去掉"sample, not yet usable"，与真实 `-headless`/`status`/日志对齐；`Restart=on-failure`/`KeepAlive=false` 的理由保留。
- `scripts/watchdog-check.sh`：去掉"Wave-2 CLI pending / do not wire yet"，改为可用状态；仍保留"旧构建无 status 时明确说明"的防御分支。
- `scripts/release-check.sh`：文档检查加入 `design/how-to-add-a-source.md` 与 systemd/launchd 样例；其余闸门逻辑不变。
- `design/M0-tasks.md`：D-6 的 checker 行改写。
- `.gitignore`：沿用 `/design/prototype/zeokeel-prototype` 忽略（Wave 1 加）。
- **未改** `VERSION`（保持 `1.0.0-rc.1`；Lead 决定何时升 1.0.0）。

## 3. 验证（本轮实测，命令可复现）

```sh
export PATH=/usr/local/go/bin:$PATH
CGO_ENABLED=0 go build ./...                         # 20:31 RC=0
CGO_ENABLED=0 go test ./... -count=1 -skip Live      # 20:24 全绿；20:33 重跑出现 build/测试失败（并发编辑）
CGO_ENABLED=0 go vet ./...                           # 20:24 干净
CGO_ENABLED=1 go test -race ./... -skip Live         # 红：真实 DATA RACE（见 §4）
sh scripts/package.sh                                # 20:27 PASS：tar 13MB / mac zip 12MB / win zip 13MB；linux 33MB / darwin 32MB（<40MB）
sh scripts/release-check.sh                          # 15 PASS / 1 FAIL（verify-fixes，因 -race）/ 7 SKIP：产物 3/3、文档 9/9、VERSION、验收测试全 PASS
```
CLI 冒烟（`/tmp/zeokeel2` 新构建）：`check` 全新安装 = 2；`status` 全新安装 = 1（setup 文本给出）；`setup` 无参数 = 3；
`-headless` 收到 SIGTERM → 日志 `state=setup exitCode=3`，日志文件 0600 JSON；`status` 在应用运行时 `running=true`（只读 inspect）。

**快照警告**：本文件所有"实测"都是 20:24–20:35 的快照。20:33 的 `go test` 因 `internal/runtime [build failed]` 失败，
说明其他 agent 还在改代码。**Lead 必须在 Wave 2 完全 settled 后重跑 `verify-fixes.sh`/`release-check.sh` 再更新勾选。**

## 4. 无法在 docs scope 内修复的代码问题（owner + file:line）

1. **提示词历史/Default updated 未接线**：`internal/runtime/prompts.go:199` 的 `recordPromptMeta` 没有调用者；
   `internal/runtime/pages.go:481`（SaveSettings 的 prompt 分支）只调 `preparePromptSave`。owner: fix-runtime。
   后果：history 永不增长、`prompt.base.*` 永不写入，§13 的"Default updated"永远不会出现。
2. **`check` 在全新安装返回 2**（语义应为 3=等待 setup）：`cmd/zeokeel/cli.go:141-190`（`code = ExitComponent`）。
   owner: fix-runtime。
3. **`manually_stopped` 无写入口**：`internal/runtime/status.go:94-151` 定义了 key，`cmd/zeokeel/cli.go:46` 没有
   `stop` 子命令。owner: fix-runtime。
4. **`-race` 数据竞争与 -race 失败测试**：
   - `internal/runtime/pages.go:393`（ticketDetailVM 读 `t.Inputs/Assignments` 切片）vs `internal/engine/lifecycle.go:35,40`
     （`Report` 写同一 Ticket）。根因：`runtime.pages.go:279` 用 `eng.AllTickets()` 的浅拷贝，切片仍别名 engine 状态。
     owner: fix-runtime（改成 `TicketSnapshot`/`copyTicket`）或 fix-engine（AllTickets 深拷贝）。
   - 20:33 的 `TestEndToEndFromChatToReply` 失败与 `internal/runtime [build failed]` 属并发编辑，owner 由 Lead 按最终树指派。
5. **设置页未完成**：`internal/runtime/pages.go:463` 评估组 placeholder；来源组有 IM 凭据但无 GitHub repo/CLI；
   `internal/store` 无 source 实例表。owner: fix-ui2/fix-runtime。
6. **安全债**：`internal/runtime/onboarding.go` API Key 明文 SQLite；`internal/agent/tools.go` 的 bash `os.Environ()`
   未剥离 `*KEY*/*TOKEN*`；无 keychain。owner: fix-runtime。
7. **`Runner.Resume` 生产未用、无 steer/inject**：`internal/agent/agent.go`（Resume）、`internal/runtime/worker.go`
   （RunSession）；`ReplyPlan` 不被 `deliverReply` 消费（`internal/runtime/reply.go:80-116`）。owner: fix-agent/fix-runtime。
8. **打包/分发**：`scripts/package.sh` 无 `.dmg`/NSIS/deb/PKGBUILD/updater；CI 无 package job。owner: Lead/Wave 3 决定。

## 5. 给后续的最终检查（Wave 2 settled 之后）

- [ ] 在最终代码重跑 `sh scripts/verify-fixes.sh`（必须全绿，含 `-race`）与 `sh scripts/release-check.sh`（0 失败）；更新 RELEASE A/C 勾选。
- [ ] `recordPromptMeta` 接线后，更新 §13 §1.2 行、README 面板行、RELEASE F-2 为 ✅。
- [ ] 设置页评估组/GitHub Source 配置完成后，更新 README 状态表、§8.2、RELEASE F-4。
- [ ] `check` 语义、`manually_stopped`/`stop` 修复后，更新 ops-runbook §2/§8、RELEASE F-3。
- [ ] `-race` 修复后重跑并删除 RELEASE B/C 的红色说明。
- [ ] 每次代码变动后核对 §13 的 file:line（本轮已逐行重跑，但代码仍会移动）。
- [ ] `VERSION` 只在 F 组清空、闸门全绿后改 `1.0.0`。

## 6. 微 pass（2026-11-03 20:40–20:50，verifier-B 反馈后）

**代码在 Wave 2 末尾又前进了一步，本轮按最新代码再次校正文档：**

1. **§13 证据只指生产代码**（verifier-B 的 D 点）：删除所有 `*_test.go` 证据（`verify_wave2_test.go` 等）。
   §4.3 改为 `internal/engine/gate.go:184/27-56/166-180` + `evaluate.go:294`（applyEvalLocked）+ `receive.go:360`（enqueueEvalLocked）；
   §4.4 改为 `domain/ids.go:39` / `receive.go:17` / `evaluate.go:294` / `runtime/reply.go:213`，并保留 🟡（12 条不变量未一一对应测试）。
   全文已再次逐行核对（发现并修正：replyPlanLocked 574、RecoverAssignments 21、StartSupervisor 105、cmdStatus 76/cmdCheck 143/cmdSetup 216、
   InspectStatus 137、deployFacts 708、ui.Run main.go:309 等）。
2. **`-race`/测试实际结果**（同一小时内多次测量，代码一直在被改）：
   - 20:40、20:47 两次 `CGO_ENABLED=1 go test -race ./... -skip Live` 全量**通过**（runtime/engine 深拷贝修复确实落地）。
   - 20:46 `release-check` 的全量 -race 在 `internal/runtime` 撞到 `TestFollowThenCloseEndsTicketByOutcome` /
     `TestBackgroundJobThirtyMinuteRule` / `TestSettingsPageListsOnlyWiredFields`，**不是 DATA RACE**；
     20:47 全量非 race 也复现这三个 → 是仍在演进中的实现/测试不一致（`pages.go` 新增 `execution.prepare` 字段与
     对应 settings 测试、follows 结束路径、30 分钟后台作业规则）。
   - `cmd/zeokeel` 的 `TestEndToEndFromChatToReply` 有一次全量 -race 偶发失败，单独 `-count=3` 与随后全量复跑通过 → 时序 flake。
   - **最终文档按当前红写**：README 验证段、RELEASE B/C 勾选（改回 [ ] 并列出三个测试名）、CHANGELOG、design §13 明确未做均如实标注；
     owner: fix-runtime / fix-ui2。不可把这次红写成 flake 一笔带过。
3. **依赖事实**：go.mod 直接依赖含 `open-dingtalk/dingtalk-stream-sdk-go v0.9.1`、`bmatcuk/doublestar/v4`、
   `dustin/go-humanize`、`golang.org/x/sys`；`gorilla/websocket` 为 indirect（钉钉 SDK 传输）。README "依赖" 已重写。
4. **IM 真实形态**：钉钉走官方 Stream SDK 长连接（`internal/im/dingtalk/stream.go:28-29,60,98`），
   飞书走 HTTP 历史接口长轮询（`internal/im/feishu/transport.go:1-31`），**无 Feishu WS、无媒体下载**
   （`mapping.go` 只保留 image_key/file_key 引用）。README/CHANGELOG/RELEASE/RELEASE-STEPS/ops-runbook/cookbook 已同步。
5. **`zeokeel stop` + STOPPED marker 已落地**：`cmd/zeokeel/cli.go:349-380` 写 `<stateDir>/STOPPED`（0600），
   `status` 在“手动停且未运行”时返回 0（`internal/runtime/status.go:69-76`），启动清标记（`cmd/zeokeel/main.go:82`）。
   ops-runbook 新增 §1 行、§2 "手动停" 段落、§5 排障行；RELEASE/RELEASE-STEPS/README CLI 表已列出 `stop`。
   旧 "`manually_stopped` 无写入口" 已从所有文档删除。
6. **提示词历史已接线**：`internal/runtime/pages.go:549` 调用 `recordPromptMeta`；`prompts.go:155/201/235`
   提供校验/历史（每层最近 20 版）/`PromptHistory`；`prompt.base.<layer>` 支持 "Default updated" 提示。
   ops-runbook 新增 §6.5（历史/备份/热更新）；cookbook 更新提示词相关说明。RELEASE F 组删除该项。
7. **频道格式已落地**：`runtime/reply.go:20-27`（IM ≤100 runes / GitHub 结构化 / local 简短）、
   `:34-40`（按 source 分派）、`:213-245`（读 ReplyPlan，notify/both/none → Held，只进面板）。
   ops-runbook 新增 §6.6；cookbook §6.3 说明新 Source 如何复用/扩展频道格式；§4.10 从 🟡 改为 ✅。
8. **`check` 语义已修**：全新安装现在返回 3（等待 setup，实测）；README/RELEASE/ops-runbook 的旧"返回 2"已删除。
9. **release-check**：20:46 一次 15 通过 / 1 失败（上述 flake）/ 7 跳过；产物 3/3、文档 9/9 均 PASS。
   第二次复跑见 `/tmp/micro-release-check2.log`（若仍撞 flake 则属同一测试问题，不影响文档事实）。

**仍未解决（代码，docs 只能如实写）：**
1. fix-ui2：设置页评估组只读（规则/admission/accept mode），GitHub Source 实例不可从面板配置（`internal/runtime/pages.go:468-478`）。
2. fix-engine/fix-runtime：`domain.Source`/`domain.Thread`/`domain.UserError` 未落地。
3. fix-runtime/fix-agent：`Runner.Resume` 生产未用（`internal/agent/agent.go:320`）；无 `steer/inject`。
4. fix-integrations：Feishu WS 长连接与 IM 媒体下载未实现。
5. 安全：系统钥匙串未实现（`cmd/zeokeel/main.go:51` 只是提示；API Key 明文 SQLite 0600）。worker env 剥离已实现（`internal/agent/tools.go:155` `scrubEnv`）。
6. 分发：`.dmg`/deb/NSIS/PKGBUILD/updater 与 CI 出包未做（`scripts/package.sh` 只有 tar/zip/未签名 .app）。
7. fix-runtime：`TestEndToEndFromChatToReply` 在 -race 全量下的偶发时序 flake（去 flake，不要跳过）。
8. `VERSION` 保持 `1.0.0-rc.1`：产品面 F 组未清，且 20:47 全量测试仍有 3 个红；由 Lead 决定何时修复并重跑闸门。

**最终检查清单（本轮）**：
- [x] §13 无 test-file 证据；每 ✅ 指生产代码
- [x] README/RELEASE/CHANGELOG/RELEASE-STEPS 的 -race 结果与 flake 如实
- [x] 依赖事实（direct/indirect）与 IM 传输形态
- [x] `stop`/STOPPED、提示词历史、频道格式进入 ops-runbook 与 cookbook
- [x] 无 "pending" 占位；未实现项统一在 RELEASE F 组与 ops-runbook §8

### 6.1 微 pass 的最终测量（消息给 Lead 时以此为准）

- `CGO_ENABLED=0 go build ./...`：PASS（20:47）。
- `CGO_ENABLED=0 go vet ./...`：PASS（20:47）。
- `CGO_ENABLED=0 go test ./... -count=1 -skip Live`：**FAIL**（20:47）— `internal/runtime` 三个测试：
  `TestFollowThenCloseEndsTicketByOutcome`、`TestBackgroundJobThirtyMinuteRule`、`TestSettingsPageListsOnlyWiredFields`。
- `CGO_ENABLED=1 go test -race ./... -skip Live`：20:40/20:47 通过；20:46 全量红（同上三测试，无 DATA RACE 报告）。
- `sh scripts/package.sh`：PASS（三平台包 12–13MB，二进制 33/32MB）。
- `sh scripts/release-check.sh`：15 PASS / 1 FAIL（verify-fixes，因上面测试红）/ 7 SKIP；文档 9/9、产物 3/3 通过。
- `VERSION` 保持 `1.0.0-rc.1`；升版由 Lead 决定（F 组未清 + 当前测试红）。

## 7. 最终同步（2026-11-03 20:55，code frozen）

**Lead 新任务：release-check skip 过时 + 最终闸门结果 + runtime pass-3 的三项 003 能力进文档。**

1. `scripts/release-check.sh`：删除过时的 `skip "IM 驱动接线决策（AUDIT N-1 / Wave 2）"`，与真实凭据 skip 合并为
   `"真实凭据下的 GitHub / 飞书 / 钉钉端到端（IM 已接线：Feishu 长轮询 + DingTalk Stream；CI 无凭据）"`。
2. **测试/闸门结果（code frozen 后实测）**：
   - `CGO_ENABLED=0 go build ./...` / `go vet ./...` / `sh scripts/check-nocgo.sh`：PASS。
   - 全量非 race `go test ./... -count=1 -skip Live`：**连续 2 次 PASS**（`/tmp/frozen-test1.log`、`/tmp/frozen-test3.log`）。
   - `sh scripts/package.sh`：PASS（三平台包）；`sh scripts/release-check.sh`：**16 通过 / 0 失败 / 6 跳过**（`/tmp/final-release-sync.log`）。
   - 全量 `CGO_ENABLED=1 go test -race ./... -skip Live`：**PASS**（`/tmp/frozen-race2.log`；`cmd/zeokeel` 5.192s、
     `internal/runtime` 25.696s 均 ok，无 DATA RACE；此前 20:52 的 `cmd` test build 失败是 `verify_im_test.go` 写入中途的瞬态）。
   - RELEASE.md B/C 的 test/race/verify-fixes 勾选改回 [x]，快照说明改为 20:53–20:54 最终冻结树测量；
     CHANGELOG 移除“当前测试红灯”段，改为 2× 非 race + 1× -race 全绿。VERSION 保持 `1.0.0-rc.1`（升版仍由 Lead 决定）。
3. **runtime pass-3 三项 003 能力已进文档**（README/CHANGELOG/design §13/§8.2/ops-runbook/cookbook）：
   - **PR/Issue 生命周期**：GitHub 轮询 `rt.CloseThread(t)`（`cmd/zeokeel/github.go:76`）→ `CloseThread`
     （`runtime/github_lifecycle.go:39`）→ `Persistence.ThreadClosed`；Worker `follow` 工具（`runtime/artifacts.go:102`）
     → `BindFollow`（own/context）；`ready` 在 own PR 未关闭前保持等待，关闭按 merged/closed 结束
     （`engine/lifecycle.go:48-53,368`）；README 执行/领域行、CHANGELOG、design §13 新增行、ops-runbook §6.9、cookbook §5.1。
   - **prepare + 执行变量**：`execution.prepare`（`runtime/prepare.go:27,67`，`sh -c`，10m，env 先 scrub，
     注入 ZEOKEEL_WORKDIR/TICKET_DIR/TICKET/TICKET_NAME/REPO，失败即失败不自动重试，提示词展示变量 `worker.go:514`）；
     `execution.background_timeout` 设置（`runtime/pages.go:478`）已进设置›执行。文档：design §13 新行、ops-runbook §6.7、
     README 已知限制、CHANGELOG。
   - **30 分钟后台作业规则**：`watchJob`（`runtime/background.go:89`）到上限 kill、`ReportWaiting`（job id + 最后输出）、
     **不唤醒**；子智能体不是 shell job、不适用（`background.go:22,127`）。文档：design §13 新行、ops-runbook §6.8、CHANGELOG。
   - ~~文档化偏差~~ **已消除（最终事实，见 §8）**：worker bash 现在通过 `agent.Config.Env`（`internal/agent/agent.go:52-56`）
     获得与 prepare/提示词相同的 scrub 后 `ZEOKEEL_*` 变量（`internal/runtime/worker.go:429` 的 `executionVars`；
     `buildShellEnv` 先 `scrubEnv` 再注入，`internal/agent/tools.go:236-248`）。
   - `execution.prepare` 的 Help 文案已改为 execution root（工单目录父目录），与行为一致；文档不再写 cwd/Help 偏差。

**仍需 Lead/owner 处理（不阻塞本次文档同步）**：F 组 1–7（设置页 Edit、领域类型、steer/inject、Feishu WS/媒体下载、
keychain、分发）。VERSION 仍 `1.0.0-rc.1`。

## 8. 最终两条事实（2026-11-03 21:00，Lead 确认）

1. **worker bash 收到工单 env**：`agent.Config.Env` 在凭证 scrub 之后注入（`internal/agent/agent.go:52-56`；
   `internal/agent/tools.go:236-248` `buildShellEnv`），runtime 用 `executionVars` 填充（`internal/runtime/worker.go:429`）：
   `ZEOKEEL_TICKET/TICKET_DIR/TICKET_NAME/WORKDIR/REPO`。prepare、提示词、bash 三处变量一致；凭证类变量都不注入。
   已从 README/CHANGELOG/design §13/RELEASE F/ops-runbook 删除旧偏差描述。
2. **`execution.prepare` Help 已改对**：Help 文案为 "sh -c 在 execution root（工单目录的父目录）执行"（`internal/runtime/pages.go:477`），
   与代码 cwd（`internal/runtime/prepare.go:78`，`cfg.Root`）一致；RELEASE F-7 的 cwd/Help 偏差条目已删除。
3. 未改任何代码与测试；VERSION 保持 `1.0.0-rc.1`。docs 一致性重扫：无 "只注入 ZEOKEEL_WORKDIR"、"workspace 根目录"、F-7 残留。

## 9. 两条最终事实同步（2026-11-03 21:00，docs-only，无代码改动）

1. **worker bash 收到工单 env（偏差已消除）**：`agent.Config.Env`（`internal/agent/agent.go:52-56`）在凭证 scrub 后注入，
   `buildShellEnv`（`internal/agent/tools.go:236-248`）先 `scrubEnv` 再合并；runtime 用 `executionVars` 填充
   （`internal/runtime/worker.go:429`）：`ZEOKEEL_TICKET/TICKET_DIR/TICKET_NAME/WORKDIR/REPO`。
   已从 README（执行行 + 已知限制）、CHANGELOG、design §13（行内与"明确未做"）、RELEASE F、ops-runbook §6.7 删除
   "只注入 ZEOKEEL_WORKDIR / 其余只在 prepare+提示词" 的旧表述；改为 "prepare、提示词、bash 三处拿到同一组 scrub 后变量"。
2. **`execution.prepare` Help 已改对**：Help 现为 "sh -c 在 execution root（工单目录的父目录）执行"（`internal/runtime/pages.go:477`），
   与代码 cwd（`internal/runtime/prepare.go:78`，`cfg.Root`）一致；RELEASE F 的第 7 条（cwd/Help 偏差）已删除，
   README/design/ops-runbook 不再写"workspace 根目录/待修文案"。
3. **docs 一致性重扫**：markdown 相对链接 0 broken；`release-check` 的文档检查 9/9 ✓（README/CHANGELOG/RELEASE/设计/runbook/cookbook/systemd/launchd）。
   `release-check` 最近一次整体 15/1/6：唯一失败是 `internal/runtime` 的 `TestEditedWorkerPromptReachesTheModel`
   在 `TempDir RemoveAll cleanup` 上报 "directory not empty"（测试清理时 worker 仍在写 session；该测试单独 `-count=3` 通过，
   属测试 hygiene flake，不是功能/竞争失败）。按 Lead "test claims 不变" 的要求，未改公开文档中的测试结论，
   仅在本交接记录；若 Lead 要公开说明再补一句即可。
4. 未改代码、`go.mod`；`VERSION` 仍 `1.0.0-rc.1`。

## 10. Verifier-B 最终 FAIL 修复（2026-11-03 21:10，docs-only）

Verifier-B: 96 PASS / 1 FAIL / 1 UNVERIFIED；唯一 FAIL 是 docs 21:00 冻结后代码再移动造成的 5 处行号漂移。全部修复：

| 位置 | 旧 | 新 | 核实 |
| --- | --- | --- | --- |
| design health 端点 | `cli.go:74` + `status.go:104` | `cli.go:76` + `status.go:137` | `func cmdStatus`@76、`func InspectStatus`@137 |
| design §8.5 状态/check 行 | `cli.go:141`（两处） | `cli.go:143` | `func cmdCheck`@143 |
| design §13 §5.7 | `runtime.go:412` | `runtime.go:416` | `func (r *Runtime) stream`@416 |
| design supervisor 行 | `supervisor.go:98-160` | `supervisor.go:105-151` | `StartSupervisor`@105、`SuperviseOnce`@151 |
| design §6.7 supervisor 行 | `supervisor.go:98` | `supervisor.go:105` | 同上 |

**顺手清掉同一块里另外发现的漂移**：`cli.go:46→47`（cliCommands）、`cli.go:209→216`（cmdSetup）、
`service.go:20-45→20-47`、`logging.go:27-140→27-137`、`status.go:111-135/111-122→111-128`、
`cli.go:349-380→354-383`（cmdStop@354-383）、`tools.go:72→75`（runBash）、`tools.go:155→204`（scrubEnv）、
`source.go:78-216→55-198`、`registry.go:63-88→69-88`、`imwiring.go:14-15,74-130→14-15,61-120`、
`sender.go:30-70→34-70`、`reply.go:20-27/34-40/213-245→25-28/50-82/213-264`、`prompts.go:235→236`（PromptHistory）、
`prepare.go:27/30/67/103/122→28/31/68/104/123`、`pages.go:549→557`（recordPromptMeta 调用）、`worker.go:514→517`（promptVars 调用）；
ops-runbook：`cli.go:349→354`、`status.go:111-135→111-128`、`pages.go:549→557`、`prepare.go:67→68`、`recovery.go:27→21`、
`runtime.go:148-165→152-168`；cookbook：`source.go:78-104→55-72`、`source.go:189-193→173-175`、`source.go:212-216→196-198`、
`registry.go:63-72→69-73`、`registry.go:82-88→85-91`。

**内容层面同时修正**：代码里 `Runner.Resume` 已删除（Wave 2 清理，`internal/agent/session_test.go` 注释确认），
文档（design §5.2 行改为 ✅ + RunSession@283、§13 明确未做、README、CHANGELOG、RELEASE F-3、rejected-concepts）统一改为
"已删除，恢复只走 RecoverAssignments + patchtoolcalls"，不再有"存在但生产未用"的过时说法。

**一致性检查**：相对 markdown 链接 0 broken；全 docs `file:line` 范围检查 0 错误；§13 无任何 `*_test.go` 证据引用；
未改代码/go.mod，`VERSION` 仍 `1.0.0-rc.1`。

## 11. Round 2/3 最终文档 pass（2026-11-03 23:05–23:15，docs-only）

按 Lead 指令读 `audit/fixes/ui2-spec.md`、`audit/fixes/im-channel-spec.md` 与当前代码后更新：

**文件**
- 新增 `design/im-channels.md`：IM 渠道模型、设置键、飞书长轮询+凭据检查、钉钉 DWS CLI 契约
  （JSON 形状/30s/serial runner/send>auth>poll/1s→30s/cursor 整批后推进/隐藏幂等 marker/缺 dws 报错/stream 可选）、
  面板检查与授权、运维步骤、未实现清单。
- `README.md`：状态表新增工作区/内置工具行并更新面板/来源/运维行；新增「界面结构」「内置工具」「IM 渠道」三节；
  FAQ（为什么是"已答复"、IM 未启用/检查失败、web 工具边界）；已知限制改为工作区/会话只能新建、
  DingTalk Stream UI 偏差、IM 外部条件（dws/凭据）。
- `CHANGELOG.md`：rc.1 下新增「Round 2/3」小节（UI/工作区/未受理修复/IM 渠道/DWS/工具 + 23:10 增量验证）。
- `RELEASE.md`：F-3 更新；G 改为"闸门全绿但产品面未完成"；新增 H「Round 2/3 增量」（勾选 + 验证 + 剩余项）。
- `design/zeokeel-go-mygo-design.md`：§13 更新 §4.8/§5.10/§6.3/§6.5/§8.2 行；新增 6 行（工作区/会话、原生选择器、
  系统信息、未受理→答复、IM 渠道管理、DWS、内置工具）；§8.2 现状补面板新建工作区；§8.5 部署组补 OS/时区；
  "明确未做"补工作区/会话不可改名删除、Stream UI 偏差、IM E2E 未验证。
- `design/ops-runbook.md`：§1 IM 行；新增 §6.10 IM 渠道运维（启用/飞书检查/钉钉 dws 命令与排障）；
  §8 IM 描述改为 DWS 默认。
- `design/rejected-concepts.md`：更新 ReplyPlan（已支付）、提示词历史（已接线）；新增 §D Round 2/3 概念登记
  （DWS 默认、渠道管理、plantask/agentsmd、web 工具、工作区树、原生选择器、未受理解释）。
- `scripts/release-check.sh`：文档检查列表加入 `design/im-channels.md`。

**代码偏差（docs pass 只记录，未改代码）**
- `internal/view/channels.go:56` 把钉钉 `stream` 标为 `Implemented:false`（Note 写"本版本未接入官方 Stream SDK"），
  面板显示「未实现」且不可选；但 `internal/runtime/im.go:394-398` 已用官方 SDK 构建 stream driver，
  `internal/runtime/pages.go:470-471` 的设置 Help 也写"stream：官方 Stream SDK"。
  需 owner **fix-ui2** 把该选项改为可用（或删掉 Help 里的 stream 说法），二选一，别让面板与 runtime 互相打脸。

**验证（本次 docs pass 实测，2026-11-03 23:10）**
- `CGO_ENABLED=0 go build ./...`、`go vet ./...`：PASS。
- `go test ./internal/ui ./internal/view ./internal/runtime ./internal/agent ./cmd/zeokeel -skip Live`：PASS。
- 文档一致性：相对 markdown 链接 0 broken；全 docs `file:line` 范围 0 错误；§13 无 `_test.go` 证据引用。
- 全量 `verify-fixes.sh`/`release-check.sh` 未在 Round 2/3 代码上重跑（已在 README/RELEASE/CHANGELOG 写明，
  发布前必须重跑）。`VERSION` 仍 `1.0.0-rc.1`，无新 tag。

## 12. 最终代码文档同步（2026-11-04，docs-only，code frozen）

按 Lead “CODE IS FINAL” 指令，在修复波后逐项核对代码再改文档；**没有改任何代码/go.mod**。VERSION 仍 `1.0.0-rc.1`，无新 tag。

**更新文件**
- `README.md`：状态表重写（IM 审批优先、飞书 ws/longpoll、DWS 真实契约、异步命令、聊天双态、Ticket 身份、工作区过滤/重命名、
  工具信任边界、设置/secret）；界面结构、内置工具、IM 渠道、FAQ、已知限制、验证（最终闸门由 Lead 跑，不预称全绿）、依赖
  （`larksuite/oapi-sdk-go/v3 v3.12.2`）。
- `CHANGELOG.md`：rc.1 下新增「Round 2/3 + 修复波（2026-11-04）」；修正旧条目里“Feishu 无 WS”“提示词无热更新/历史”等过时表述。
- `RELEASE.md`：A 组按最终代码（IM 审批优先、A3/A7 语义）；B/C 的 build/test/race/verify 勾选改回“待 final gate”；
  F 组按剩余产品面重排 + F-7 收尾对齐；H 组改为修复波增量；G 仍为 rc.1 语义。
- `RELEASE-STEPS.md`：顶部状态、IM 审批优先/传输、设置/提示词/环境/闸门说明同步。
- `design/zeokeel-go-mygo-design.md` §13：整表按最终代码重写（40+ 行），新增 IM 审批优先、三态/生命周期、DWS、飞书 WS/longpoll、
  聊天双态/忙碌态、Ticket 身份、工作区 opaque ID/过滤、设置 codec/事务/secret、工具信任边界；更新“明确未做”。
- `design/im-channels.md`：全文重写（审批优先、真实 DWS CLI 契约、飞书 larkws + longpoll fallback、三态/升级、生命周期/secret）。
- `design/ops-runbook.md`：§6.10 改为审批队列+新契约；新增 §6.11 升级与迁移；§8 未实现清单同步。
- `design/rejected-concepts.md`：修正 DWS/AGENTS/“未受理”旧登记；新增 §E 修复波概念登记（审批优先、larkws、DWS 身份、异步边界、
  聊天双态、domain ID、opaque 工作区、AGENTS/web 信任边界、settings codec、font scale）。
- `audit/verify/design-r2-ux.md`：加“基于 f4200da 历史评审”说明（行号不再随修复波更新）。

**文档一致性（本机重跑）**：相对 markdown 链接 0 broken；全 docs `file:line` 范围 0 错误；§13 无 `_test.go` 证据引用。
全量 `verify-fixes.sh`/`release-check.sh` **未由我运行**（按 Lead 安排由其在合并前跑）；README/RELEASE/CHANGELOG 均明确不预称全绿。

**代码/文档差异（需 Lead 在 push 前决定是否修；未改代码）**
1. 飞书 transport 面板目录/默认值落后：`internal/runtime/pages.go:474-476`（Options 含 webhook、Help 写“只实现 longpoll”、Value 默认 longpoll）、
   `internal/view/channels.go:53-57`（spec 只声明 longpoll）与 runtime 能力不符——runtime 已支持 ws
   （`internal/runtime/im.go:786-792,859-873`、`internal/im/feishu/ws.go`、`go.mod:12`）。文档按 Lead 口径写“ws 推荐/longpoll fallback”，
   但**代码缺省仍是 longpoll、面板会把 ws 显示为未实现**。
2. `ui.font_scale` 未被 runtime 设置投影：UI 有控件与 `fs()`（`internal/ui/settings.go:270-300`、`tokens.go:48-78`），
   但 `runtime/pages.go` 的设置组没有该字段，页面会显示“等待投影”。
3. 一次性 settings 迁移未接线：`internal/runtime/settingscodec.go:83` 的 `migrateSettings` 无调用者、
   `internal/store/settings.go:28-55` 的 `SetSettingsSchema` 无调用者；当前实际生效的是读取层 bool 兼容 + `BootstrapIMFlags`。
4. `DeleteWorkspace` 总是拒绝（缺 store/engine 删除接口，`internal/runtime/workspaces.go:151-190`）；文档写“明确拒绝”而非“已支持删除”。
5. `TicketDetailVM.ID` 仍被赋值为展示编号（`internal/runtime/pages.go:385`），而注释/查找键要求 domain ID（`:302,310`）；
   行/详情查找本身已正确，展示字段是遗留。
6. `im-channel-spec` 的“每渠道一个串行 worker”在最终代码里是长驻 consumer + 短命令串行（`dws.go:21-28`），不再是“poll/send 共用队列”；
   文档已按真实实现写，旧 spec 行文未改。
7. 飞书 `webhook` 仍出现在 settings Options（`pages.go:475`）但 runtime 明确拒绝（`im.go:792`）；文档写“webhook 未实现”。

## 13. 微调后文档复核（V-1…V-6，2026-11-04，docs-only）

Lead 的最终代码微调关闭了 §12 列出的全部收尾差异；本轮重 grep 后更新 README / CHANGELOG / RELEASE / design §13 /
design/im-channels / design/ops-runbook / rejected-concepts：

1. **V-1 开关 canonical 1/0**：UI 读写 `boolText`/`settingBool`（`internal/ui/channels.go:381-395`、
   `internal/ui/settings.go:405-414`），读取兼容旧 true/false；设置 VM 输出 `EncodeSettingBool`
   （`internal/runtime/pages.go:557-559`）。文档不再暗示“开关写 true 显示 off”。
2. **V-2 一次性迁移已接线**：`LoadSettings` 在 schema 旧/缺失时执行 `migrateSettings` 并 `SetSettingsSchema`
   （`internal/runtime/pages.go:678-707`；`internal/runtime/settingscodec.go:83`；`internal/store/settings.go:28-55`；
   测试 `internal/runtime/migration_test.go:14` `TestLegacySettingsMigration`）。README/RELEASE F/§13/ops §6.11 已改为“启动执行”。
3. **V-3 飞书默认 ws**：默认 `feishu_transport=ws`（`internal/runtime/im.go:87`），面板 Options 只有 `ws`/`longpoll`
   （`internal/runtime/pages.go:474-476`、`internal/view/channels.go:45-51`），webhook 不再发布。
   §13/im-channels/ops/README/H 组已写“默认 ws，longpoll 显式 fallback”。
4. **V-4 font_scale 已投影**：设置›部署字段 `ui.font_scale` 0.9/1.0/1.1（`internal/runtime/pages.go:537-539`），
   UI `fs()` 缩放（`internal/ui/tokens.go:48-78`）保存立即生效。移除“等待投影”限制。
5. **V-5 删除空工作区已实现**：`DeleteWorkspace` 检查无工单后删行+策略（`internal/runtime/workspaces.go:155-190`、
   `internal/store/store.go:390`），有工单拒绝。README/§13/ops/rejected-concepts/RELEASE H 已改为“仅空工作区”。
6. **V-6 详情 ID=domain ID**：`TicketDetailVM.ID = string(t.ID)`，`Number` 单独（`internal/runtime/pages.go:385`）。
   README/§13/RELEASE H/CHANGELOG 已改为“查找键与详情 VM 的 ID 都是 domain ID”。

**复核结果**：docs 相对链接 0 broken；所有 docs `file:line` 范围检查 0 错误；§13 无 `_test.go` 证据引用。
**未改代码**；`VERSION` 仍 `1.0.0-rc.1`、无新 tag；全量闸门仍由 Lead 在 push 前跑（README/RELEASE/CHANGELOG 不预称全绿）。

**补充（2026-11-04 最后一处 nuance）**：旧 Feishu 凭据-only 配置迁移 pin `source.feishu_transport=longpoll`
（`internal/runtime/settingscodec.go:101-106`），新装仍默认 `ws`（`internal/runtime/im.go:87`、`internal/runtime/pages.go:474-476`）；
文档已改为“新装默认 ws；旧配置迁移 pin longpoll”。`DeleteWorkspace` 现在删除空工作区（行 `internal/store/store.go:390` +
engine 路由 `internal/runtime/workspaces.go:182`/`internal/engine/engine.go:157`），有工单拒绝，测试
`internal/runtime/round3_test.go:244` `TestDeleteWorkspaceRemovesEmptyOnly`；README/§13 已补“策略+engine 路由”。

## 14. UI/UX 优化轮文档同步（2026-11-04，docs-only，code final）

按 Lead 指令把 README / CHANGELOG / RELEASE / design §13 / im-channels / ops-runbook / rejected-concepts
更新到 UI/UX 优化轮代码；**未改代码/go.mod**；`VERSION` 仍 `1.0.0-rc.1`，无新 tag。

**文档改动**
- README：顶部状态加 UX 轮；「当前状态」面板/项目行重写；「界面结构」整节重写（五项日常导航 + 折叠诊断、
  首跑/多行 composer/target chip/收件箱/路径真相/模型设置/执行行内动作/可访问性）；IM 节加连接向导；
  FAQ 重写（聊天 vs 派活、收件箱、IM 向导、模型测试、路径真相、快捷键）；已知限制加入明确未做（app 菜单、
  TextScale/ReduceMotion/高对比、计划可视化、原项目执行、多窗口、自动更新）。
- CHANGELOG：新增「### UI/UX 优化轮（2026-11-04，同一 rc.1，不新增 tag）」；修正“IM 入口待接线/飞书 WS”旧句；
  测试状态改为“旧快照不作本轮证据，Lead 重跑闸门”。
- RELEASE：H 标题加入 UX 轮，新增 11 条 UX 勾选 + 明确未做项；F 组加第 7 条平台/UX 收尾。
- design §13：标题改“UI/UX 优化轮后”；新增/重写 IA、composer、收件箱、模型设置/测试、首跑、路径真相、IM 向导、
  可访问性等行；“明确未做”加 UX 收尾。
- design/im-channels.md：§0/§2（feishu_chats 规则）/§5（连接向导）/§7（向导步骤）/§8/§9 证据更新。
- design/ops-runbook.md：§6.10 改为收件箱+连接向导+DWS 版本/登录；§8 加 UX 未做。
- design/rejected-concepts.md：新增 §F UX 轮概念登记（五项导航、项目词、composer、收件箱、模型测试、演示跳过、
  路径真相、连接向导、可访问性、StateKey；未做项明确拒绝假装）。

**代码/文档对齐（本轮新行为）**
- IA：`internal/view/vm.go:486-520`；`internal/ui/chrome.go:60-110`。
- composer：`internal/ui/composer.go:19-220`；`internal/ui/commands.go:137-188`；`internal/runtime/ux.go:134-172`。
- 收件箱/通知：`internal/ui/inbox.go:1-360`；`internal/runtime/ux.go:279-440`；`internal/ui/uxsupport.go:286-300`。
- 模型：`internal/runtime/ux.go:36-125`；`internal/runtime/pages.go:524-526,543-558`；`internal/ui/settings.go:501-535`。
- 首跑：`internal/ui/onboarding.go:60-180`；`internal/runtime/ux.go:445-452`。
- 路径真相：`internal/ui/ticketdetail.go:257-290`；`internal/runtime/ux.go:279-303`；`internal/runtime/worker.go:570-582`。
- IM 向导：`internal/ui/channelwizard.go:1-520`；`internal/runtime/ux_im.go:1-120`；`internal/runtime/im.go:1392-1505`；
  `internal/im/dingtalk/dws_setup.go`；`internal/im/feishu/ws_chats.go`。
- 视觉/窗口：`internal/ui/tokens.go:100-145`；`internal/ui/chrome.go:143-160`；`internal/ui/app.go:320-330,441-455`。

**仍需 Lead 注意**
- 模型超时实际在「设置›执行›模型响应超时」（`execution.model_timeout`），不在「模型」组；文档如实写，未虚构。
- 仍明确未做：mygo 原生 app 菜单、系统 TextScale/ReduceMotion/高对比、计划/任务面板可视化、真正原项目执行、
  多窗口、自动更新/About、系统钥匙串。README/RELEASE F-7/§13/ops §8 均已写清。
- 本轮未跑全量 `verify-fixes.sh`/`release-check.sh`；README/RELEASE/CHANGELOG 均声明由 Lead 在 push 前重跑，不预称全绿。

## 15. rc.2 文档与发布说明（2026-11-04，docs-only）

按 Lead 指令准备 `1.0.0-rc.2` 文档；**未改代码/go.mod，未改 VERSION**（文件仍 `1.0.0-rc.1`，由发布负责人 bump）。
新增 `RELEASE-NOTES-v1.0.0-rc.2.md`；更新 README / CHANGELOG / RELEASE / design §13 / im-channels / ops / rejected-concepts / handoff。

**文档记录（均以当前代码为准）**
- 流式聊天：`internal/runtime/rc2.go:24-190`（SSE/60ms/StopChat）、`chat.go:38-52`、`ui/composer.go:241-305`、`ui/uxsupport.go:232-240`。
- Markdown：`internal/ui/markdown.go:19-180`、`ui/chat.go:75`（安全子集，不解释 HTML，http(s) 链接）。
- 计划/待办：`internal/view/plan.go:1-51`、`runtime/rc2.go:302-433`、`ui/planpanel.go:29-85`、`ui/ticketdetail.go:97-98`、`ui/composer.go:238-239`。
- 应用到项目：`runtime/rc2.go:193-270`（safeExecPath/备份/相对路径）、`ui/applyproject.go:1-199`、`ui/ticketdetail.go:291-301`。
- 撤销拒绝：`engine/lifecycle.go:723-815`、`engine/persist.go:835-860`、`runtime/rc2.go:436-463`、`ui/inbox.go:59-66`、`ui/uxsupport.go:201-230,307-310`（10s）。
- 原生菜单：`ui/menu.go:1-72`、`ui/app.go:345`。
- 系统无障碍：`ui/prefs.go:1-61`、`ui/app.go:441-462`、`ui/stream.go:59-62`。
- 关于/更新：`ui/about.go:1-117`、`runtime/rc2.go:355-415`（只 GET，不下载）、`view/pages.go:469-470,644`。
- 明确未做：真正原项目执行（副本+显式 apply-back）、系统钥匙串（SQLite 0600）、多窗口、自动更新只检查、
  真实飞书/DWS 租户 E2E、打包无 .dmg/deb/安装器。写入 README 已知限制/RELEASE F-7/§13/ops §8/rejected §G。

**检查**：相对 markdown 链接 0 broken；docs `file:line` 范围 0 错误；§13 无 `_test.go` 证据。
未跑全量 `verify-fixes.sh`/`release-check.sh`；README/RELEASE/CHANGELOG/发布说明均声明由 Lead bump VERSION 后跑闸门并 tag，
不预称全绿。

## 16. rc.3 文档与发布说明（2026-11-04，docs-only）

Lead 已把 `VERSION` bump 为 `1.0.0-rc.3`；本轮只改文档（README/CHANGELOG/RELEASE/design §13/im-channels/ops/rejected + 新
`RELEASE-NOTES-v1.0.0-rc.3.md`），未改代码/go.mod。

**DSH 对齐（推翻 rc.1 的审批优先）**
- admitted 的明确 IM/GitHub 请求由 Eval 直接接单 → Ticket；推断请求按 accept mode off/observe/auto（auto 需匹配规则）。
- `DecPropose` 只保留给显式 opt-in `Policy.RequireApproval`（仅推断）；`AutoApproveIM` 已移除；旧 proposal 在收件箱/总览标
  「旧式请求（需要批准）」。证据：`internal/engine/evaluate.go:348-390`、`gate.go:52-56`、`ui/inbox.go:106`、`ui/overview.go:109`、
  `cmd/zeokeel/acceptance_test.go:129-160`。README/CHANGELOG/RELEASE A/H/J/design §13/im-channels/ops/rejected 均已改写。

**rc.3 新能力**
- 项目面板 概览/设置/统计：`view/workspacepanel.go:1-99`、`ui/workspacepanel.go:193-520`、`runtime/workspacesettings.go:75-160,296-333,427-480`、
  `store/workspacesettings.go`；统计的“最近活动”区当前 runtime 未投影时为空态（文档已注明，不伪造）。
- 浏览器 + MCP：默认路径检测 `internal/browser/detect.go:10-65`；独立 profile/唯一端口/启动 flags
  `manager.go:99-107,185-245`、`launch.go:22-56`；`npx -y chrome-devtools-mcp@latest --browserUrl ...` + `mcp__browser__*`
  `mcp.go:12-15,44-48,118-140`；只注入项目 Worker `runtime/worker.go:468`；env scrub / npx 缺失错误。
- 工具：`read_image`（元数据-only，无像素；webp 无维度）`agent/imageread.go:1-120`；`job_list` `agent/jobs.go:275-309`；
  `subagent` 命名 `agent/subagent.go:55`；域工具 `runtime/artifacts.go:121-222`；SuperAgent 无 tools `runtime/rc2.go:121-132`；
  `orchestrator_read`/DSH main orchestrator 工具仍缺（`audit/tool-gap.md`）。

**诚实限制**（写入 README 已知限制 / RELEASE F-7+J / design §13 / ops §8 / 发布说明）：真正原项目执行（执行副本+apply-back）、
系统钥匙串（SQLite 0600）、多窗口、自动更新只检查不下载、真实飞书/DWS 租户 E2E、`read_image` 无视觉、无 `orchestrator_read`、
浏览器需 Chrome/Edge+node/npx、打包无 .dmg/deb/安装器。

**检查**：docs 相对链接 0 broken；所有 docs `file:line` 范围 0 错误；§13 无 `_test.go` 证据。未运行全量闸门；README/RELEASE/
CHANGELOG/发布说明均声明 Lead bump 后跑 `verify-fixes.sh`+`release-check.sh` 并 tag `v1.0.0-rc.3`，不预称全绿。
