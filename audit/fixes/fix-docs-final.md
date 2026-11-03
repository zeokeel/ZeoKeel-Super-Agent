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
