# Wave 2 反证验证报告（verifier-B）— FINAL（冻结树）

> 角色：verifier-B，adversarial verification（目标是证伪）。
> **冻结时点**：2026-11-03 21:02:49。**非测试产品代码最后一次 mtime = 20:58:30**（engine 20:56:16）；21:02:51 仅有 5 个 `internal/runtime/*_test.go`（compaction/usage/evaltest/faults/settings）被改，**无生产代码变动**。验证执行：21:02:52–21:09。
> **基线**：HEAD `5b3507e`（分支 `fix/audit-remediation`，264 个未提交变更；未 commit）。`VERSION=1.0.0-rc.1`。
> 输入：`wave2-spec.md` §1/§2/§3/§6、`fix-engine.md`/`fix-engine-api.md`/`fix-ui2.md`/`fix-runtime.md`/`fix-docs-final.md`、冻结树代码与文档。
> 只读约束：未改生产代码；新增独立测试仅 `internal/{engine,domain,ui}/verify_wave2_test.go`、`cmd/zeokeel/verify_im_test.go`。
>
> **Lead 裁决已并入**（O-1 3 次过期→DOC-DEVIATION；O-2 revalidate 回落走 gate/Eval 判 PASS；O-3 peer fixture 由 fix-agent 修；O-4 handoff 仅作快照）。
> **冻结完整性备注**：Lead 第一次 "FREEZE"（约 20:51）之后代码仍被改到 20:58（`cmd/zeokeel/{main,e2e_test,recovery_test}.go`、`internal/runtime/deliverables.go`、`internal/engine/engine.go`、`internal/agent/tools_test.go`、`internal/runtime/conformance3_test.go`）；21:02:51 又落了 5 个 runtime 测试文件（**仅 test**）。**本报告所有最终结论以 21:02:49 之后、产品代码不再变动（最近非测试 20:58:30）的树为准**；21:0x 之前的 gate 结果（含 20:53 的 5/5）仅作历史。
>
> **并发警告（历史）**：20:27–20:33 期间 fix-engine/fix-runtime 持续落盘，早期快照曾红；下列判定均已在冻结树复跑。

---

## 0. 最终结论（frozen tree）

| 组 | 判定 | 证据 |
| --- | --- | --- |
| ENGINE（fix-engine） | **PASS（27/27）** | 独立 gate/admission/Eval/生命周期/订阅/snapshot 测试 + `-race`；`internal/engine` 在冻结树 `-race` ok |
| RUNTIME（fix-runtime） | **PASS（20/20）** | 全量非 race/race 全绿；真实二进制 status 0/1/2/3 + check/setup/stop/backup/archive/headless + `stop --pid`；IM 启动/入站/回写 e2e（fake，无网络） |
| UI2（fix-ui2） | **PASS（9/9）** | 动作表/诚实禁用/回答弹窗/详情/五组设置/无假数字/glossary/编译期断言 |
| DOCS（conf-product） | **PASS\*（1 个 minor FAIL）** | README/CHANGELOG/RELEASE/design §13/ops-runbook/AUDIT §9 与实测一致；**5 处 file:line 证据漂移**（§4，owner conf-product，无需改代码） |
| 机械闸门 | **PASS（5/5）** | 21:07 `verify-fixes.sh` 全过；全量非 race 27 包 0 fail；全量 `-race` 27 包 0 fail |
| 卫生/零 cgo/依赖 | **PASS** | gofmt 0、vet 干净、`check-nocgo: PASS`、依赖仅 Lead 批准的纯 Go 四项、无密钥、无 commit |

**逐项计数：PASS 96 / FAIL 1（docs 行号漂移，minor）/ UNVERIFIED 1（`release-check.sh 16 PASS` 未在本次独立重跑）。**

**底线：冻结树通过了我能独立执行的每一项机械、行为与反证检查；没有发现任何需要重新打开代码的缺陷。** 唯一 FAIL 是文档里 5 处过时的 `file:line`（由 20:54–20:58 的代码移动造成，docs 在 21:00 定稿时漏改），由 conf-product 改指即可，不涉及行为。

### 冻结树哈希（供 Lead 固定提交）
```
HEAD 5b3507e；dirty 264 files；last non-test mtime 2026-11-03 20:58:30；last *_test.go 21:02:51
08122aee2a6abe7c7d57d71a53e6c80855e16bd3beabd1cea77547b48ab39a96  internal/engine/gate.go
f713ff4c85bedb0e71d5f240f5fe9ff3bfc3929e2f96daf8d3ff3ba8a630711c  internal/engine/engine.go
253e79fb65a412f9d203a2eb4263f30fe57d1d4e28c1c3eece85574998ad9972  internal/engine/evaluate.go
1966b00be490f4e7291d007c8a9f8d86f2264a79cd9aef20a475e5da64220650  internal/runtime/worker.go
df0f41d432e99fcd247bdbd5f1058a03aee1c3d831aabbe4f7e29748b5c07c15  internal/runtime/entry.go
5690aea2859b6fda882c71af9dd003129069747cb2c74b3a44d0f9ec26095612  internal/runtime/recovery.go
9f2511da18e74741998572fd075aacb83fad3951698896700f9ef2905a5d1737  internal/runtime/status.go
e9928b8dc18f0fad4441c9eed9b8dd9489c0aab1f0e98c359c02b87ceac08cf8  internal/runtime/supervisor.go
325bb38ed77ec724e38cafd853b16d5cdf55d73eee539a2af087f16ec206ce4b  internal/runtime/reply.go
8a5ec047f776e83131803d3fa127bc800edfcbd558322b22380490b4f32865a5  internal/runtime/prompts.go
2220bdd4b82b5b82bf11320f20087f373764ecd037ba7dcd8c1e2ef07a622cdf  cmd/zeokeel/main.go
2c11ee8b92e3ec1458bf46195eb2886ffee9aa8274f66b873a743aa779a1df36  cmd/zeokeel/cli.go
08b073036330a9aa38922059e51519724e6717bdc8cb9446645699d3cce7e69f  cmd/zeokeel/imwiring.go
fbc6c6a6147d2a1d0a90eacd53e03a9755126ade6b71cdba75b427b8bba17c89  cmd/zeokeel/ops.go
67b7c9e5fc1342ec6ab1fac4885fa514f8e7d943e0c5eadab029e0ac807fbf60  go.mod
90b9d42337a2f44e8ab609f495331b8a155fa7b5fd205e024b0d39181bbd7a95  go.sum
4326ba5a512582acaa09ba7b74fba861e6219fd3cfe8f33dc89aff8d55876be9  /tmp/zeokeel-frozen（本次 CLI 验证用的冻结源码构建）
```

---

## 1. ENGINE — PASS（27/27，冻结树复跑）

独立测试：`internal/engine/verify_wave2_test.go`（18 tests）+ `internal/domain/verify_wave2_test.go`（StageOf 15 子例）。命令与结果：

```sh
CGO_ENABLED=0 go test ./internal/engine -run TestV2 -v        # 18/18 PASS
CGO_ENABLED=0 go test ./internal/domain -run TestVerifyStageOf -v  # PASS
CGO_ENABLED=0 go test ./internal/engine ./internal/domain ./internal/assess -count=1 # ok
CGO_ENABLED=1 go test -race ./internal/engine -count=1        # ok（冻结树全量 -race 中 engine 3.6s ok）
```

| # | 检查 | 结果 |
| --- | --- | --- |
| E1 | gate 六情形（002:11-16/50-54/63-79） | PASS |
| E2 | admission 九格（3 列 × explicit/inferred/ignore） | PASS |
| E3 | bot 永不 explicit（列名/`Self` 均最多 inferred；未列 bot 拒） | PASS |
| E4 | 模型不能声明 strength（`AssessOutput` 无 Strength 字段 + 推断 Mandate 仍 inferred/Basis=admission） | PASS |
| E5 | `Receive` 不调模型、返回 evaluating、无 Ticket；`EvaluateDue` 才调一次 | PASS |
| E6 | `EvaluateDue` 锁外调模型：assessor 阻塞时 Receive/读不阻塞、并发 Assess ≤1 | PASS |
| E7 | 同 Thread 批次合并（2 Trigger 1 Eval） | PASS |
| E8 | 每 Thread 至多 1 在飞；运行期新 Trigger 进 pending 下批 | PASS |
| E9 | deadline/3 次过期→unavailable（`expireDueLocked`）+ 未投递不超时 | PASS（Lead 裁决 O-1：进程内 hung 由 90s 超时兜底，DOC-DEVIATION，非缺陷） |
| E10 | pause 不超时且 resume 顺延 deadline（精确 +pause） | PASS |
| E11 | PolicyChanged 重排 queued Eval + 受影响 Ticket 收 policy Input | PASS |
| E12-E17 | ready→completed、dropped→ended、stop 撤 Mandate、retry 解 blocked、AnswerAttention 清除+投递、follows（open/merged/未完成） | PASS（6/6） |
| E18 | StageOf 003 表 15 例 golden | PASS |
| E19-E23 | fanout Input、owned 不入 Eval、HandBack/DropSubscription/MarkUnrelated、ended-not-stopped resume、revalidate 失败撤 Mandate（Lead O-2：gate 过滤合法 → PASS） | PASS（5/5） |
| E24-E26 | TicketSnapshot 深拷贝（含 Follow.ClosedAt）、Feed 不暴露内部指针、-race | PASS |
| E27 | engine/domain/assess 全量测试 + `-race` | PASS |

**验证过程中被证伪、随后被 fix-engine 修复的真实缺陷**（建议 Lead 在 STATUS 记 FIXED+VERIFIED）：`Follow.ClosedAt` 指针别名、`Feed` 暴露 `*Ticket`、HandBack 在另一 owner 仍持有时抢 Eval、global-trigger PolicyView 为空、`AllTickets` 浅拷贝。

---

## 2. RUNTIME — PASS（20/20，冻结树）

### 2.1 全量证据

- `CGO_ENABLED=0 go test ./... -count=1 -skip Live` → **27 包 ok，0 FAIL**（runtime 11.2s、cmd 1.3s）。
- `CGO_ENABLED=1 go test -race ./... -count=1 -skip Live` → **27 包 ok，0 FAIL**（runtime 25s、cmd 5s 级）。
- `sh scripts/verify-fixes.sh` → **5/5 PASS**（build/test/vet/check-nocgo/full-race）。

### 2.2 真实二进制 CLI（`/tmp/zeokeel-frozen`，冻结源码构建）

| # | 场景 | 期望 | 实测 |
| --- | --- | --- | --- |
| R6 | `status` 空安装 | 1（未运行，可重启修复） | **1** ✅ |
| R6 | `status` 运行中（headless） | 0 | **0** ✅ |
| R6 | `status` 组件错误（注入 health error，无 STOPPED） | 2 | **2** ✅ |
| R6 | `status` 运行中但 setup 未完成 | 3 | **3** ✅ |
| R6 | `status` 手动停且未运行 | 0（监督不重启） | **0** ✅ |
| R6a | `check` 空安装 | 3（等 setup） | **3** ✅ |
| R7 | `status` 只读（app 运行时） | 不阻塞/不迁移 | 运行中 status=0 ✅ |
| R8 | slog 文件 | `<stateDir>/logs/zeokeel.log` JSON | 存在，含 drain/exitCode 行 ✅ |
| R9 | `backup` | 0 + `backups/` 0600 文件 | 0 + 文件 ✅ |
| R10 | `archive --dry-run` | 0 且输出归档范围 | 0 ✅ |
| R11 | `-headless` + SIGTERM | 排空后退出码 0 | 0，日志 `state=ready exitCode=0` ✅ |
| R12 | `stop` 未运行 | 0 + 写 STOPPED | 0 + marker ✅ |
| R12 | `stop --pid <running>` | 0 + SIGTERM 生效 | 0，进程退出 ✅ |
| R13 | `setup` 非交互 | 0 + 落库 | 0，随后 status/backup 可读 ✅ |

其他（冻结树代码 + 包测试）：2s pass→`EvaluateDue`（`main.go:199`/`service.go:17,41`）；入口统一 `ReceiveTrigger`（`runtime/entry.go:18`）；游标 kv（`runtime/kv.go`，`cmd/github.go:61,95`）；sender 无 driver 返回 error；onboarding 落库；worker inbox 三段式（`worker.go:263,275,300`→`delivery.go:65-92`）；`RecoverAssignments` + `Engine.InterruptAssignment`（`engine/lifecycle.go:524`）；human slot（`durable.go:47`）；supervisor（`supervisor.go:105,151`）；提示词 6 层热更新 + 版本历史/校验（`TestPromptSaveValidationAndHistory` PASS；`pages.go:557 recordPromptMeta` 已接线）；按 source 的 reply 模板（`reply.go` IM≤100 runes/GitHub 结构化/`Held`）；诚实投影（`runtime.go:148-165`；UI 测试断言无 `0/2`）。

### 2.3 IM 启动/入站 e2e（fake，无网络）— PASS

新增 `cmd/zeokeel/verify_im_test.go`（独立、只用 `internal/im` 的 fake Transport），冻结树实测：

```sh
go test ./cmd/zeokeel -run 'TestVerifyFeishuLongPoll|TestVerifyDingTalkStream|TestVerifyOutbound' -v
--- PASS: TestVerifyFeishuLongPollInboundBecomesTicket   # 入站→Trigger→Ticket→来源 ready
--- PASS: TestVerifyDingTalkStreamInboundBecomesTicket   # Stream 路径同上
--- PASS: TestVerifyOutboundRoutesThroughLiveIMDriver    # 出站经 live 驱动
```
同时既有 IM wiring 测试（`TestUnconfiguredIMDoesNotListen`/`TestPartialIMConfigIsAnErrorRow`/`TestUnsupportedReceiverSurfacesAsSourceError`/`TestIMLoopTurnsEventsIntoTriggers`/`TestSenderRoutesThroughLiveIMDriver`）全 PASS；`internal/im/dingtalk -run TestStream|TestSubscribe`、`internal/im/feishu -run TestSubscribe|TestPollOnce` ok。

---

## 3. UI2 — PASS（9/9）

`internal/ui/verify_wave2_test.go`（8 tests）+ 编译期断言，冻结树 `-race ./internal/ui` ok（14.7s）。

| # | 检查 | 结果 |
| --- | --- | --- |
| U1 | `view.ActionsFor` 全 stage 表（answer/blocked/failed/delivery/review/working/queued/idle/ended/unknown+CanRetry） | PASS |
| U2 | 总览按 stage 画动作；动作只在总览 | PASS |
| U3 | 缺 four 通道时按钮禁用 + "动作未接线"具名 | PASS |
| U4 | 回答弹窗显示真实问题/选项，选择经 `AnswerAttention` 发送 | PASS |
| U5 | Ticket 详情全投影（Mandate/Inputs 三段/Follows/Artifacts） | PASS |
| U6 | 短投影显示"暂无数据"而非空框 | PASS |
| U7 | 无硬编码数字（1/3、12,345、PersonCount=len(rows)） | PASS |
| U8 | Glossary 27 条完整无重复 | PASS |
| U9 | `cmd/zeokeel/wiring_assert.go:13-16` 四条编译期断言 | PASS |

**小差异（不 FAIL）**：回答弹窗提交按钮实际文案是"回答"（handoff 写"提交回答"），功能正确。

---

## 4. DOCS — PASS\*（1 个 minor FAIL：5 处 file:line 漂移）

已读最终文档（mtime：README/CHANGELOG/RELEASE/design 21:00–21:01，ops-runbook 21:00，cookbook 20:55，fix-docs-final 21:02，STATUS 21:05）并抽查：

**成立（与实测一致）**：
- README/CHANGELOG/RELEASE/AUDIT §9：非 race 全量 ×2 绿、全量 `-race` 全绿、`verify-fixes 5/5`、CLI 矩阵、IM 已接线（flag+设置+e2e 可跑）、打包 12–13MB/二进制 33/32MB、`-race` 已绿、冻结后未再复现 flake。
- design §13 每行 ✅/🟡/❌ 与代码一致（抽查 §4.2/§5.8/§7/§8.5/§13）；ops-runbook status 0/1/2/3、JSON 形状、日志、stop 语义（含"不带 --pid 不杀进程"）与实测一致；`design/how-to-add-a-source.md` 可执行（§8 如实列缺口）；systemd/launchd 样例存在且 plist 合法。

**FAIL（1 个，owner conf-product/fix-docs-final；纯文本，无需改代码）**：5 处 `file:line` 指向已漂移的行（docs 在 21:00 定稿，晚于 20:54–20:58 的代码移动，但漏改）：

| # | 文档位置 | 现引用 | 实际（冻结树） |
| --- | --- | --- | --- |
| D-1 | `design/…:1367`（§6.7 health 端点行） | `cli.go:74` + `status.go:104`（均为空行/注释） | `cmd/zeokeel/cli.go:76`（`cmdStatus`）+ `internal/runtime/status.go:137`（`InspectStatus`） |
| D-2 | `design/…:1652`（§8.1 status 行） | `cli.go:74`；`status.go:104` | `cli.go:76`；`status.go:137` |
| D-3 | `design/…:1586`、`:1653`（check 行） | `cli.go:141`（注释行） | `cmd/zeokeel/cli.go:143`（`cmdCheck`） |
| D-4 | `design/…:1921`（§5.7） | `runtime.go:412`（`}`） | `internal/runtime/runtime.go:416`（`stream`） |
| D-5 | `design/…:1657`（supervisor 行） | `supervisor.go:98-160`（98 是注释；函数在 105/151） | 建议改 `:105-151` 或直接引函数名 |

复现：`sed -n '74p;141p' cmd/zeokeel/cli.go; sed -n '104p' internal/runtime/status.go; sed -n '412p' internal/runtime/runtime.go` 均为空/注释；对照 `grep -n "func cmdStatus\|func cmdCheck" cmd/zeokeel/cli.go`、`grep -n "func InspectStatus" internal/runtime/status.go`、`grep -n "func (r \*Runtime) stream" internal/runtime/runtime.go`。

**UNVERIFIED（1）**：STATUS/AUDIT 引用的 `sh scripts/release-check.sh` "16 PASS / 0 FAIL / 6 SKIP" 未在本次独立重跑（会重复 gate + 三平台打包约 8–10 分钟）；其余 release-check 依赖的 gate 我已独立全过。

**Lead 侧小项**：`audit/fixes/STATUS.md` 第 54 行已写"最终 PASS"，但 N-4 行仍是 `FIXED（待绿）`（应改 FIXED+VERIFIED）；`fix-docs-final.md` §4 的 3 个代码缺口已被 20:54–20:58 的 pass-3 修掉（handoff 是快照，按 O-4 处理即可）。

---

## 5. 机械闸门与历史 flake

| 步骤 | 冻结树结果 |
| --- | --- |
| ① `CGO_ENABLED=0 go test ./... -skip Live -count=1`（21:02:52） | **PASS**：27 包 ok，0 FAIL |
| ② `CGO_ENABLED=1 go test -race ./... -skip Live -count=1`（21:04:40） | **PASS**：27 包 ok，0 FAIL |
| ③ `sh scripts/verify-fixes.sh`（21:07:20） | **PASS 5/5**：build/test/vet/`check-nocgo: PASS (zero cgo)`/full-race |

**历史 flake 记录**：20:51 我在并发执行 CLI 冒烟时跑全量 `-race`，`cmd/zeokeel TestAcceptedTicketRunsAfterRestart` 失败；隔离复跑 PASS，冻结树全量 `-race` 连续 PASS → 判定为**负载下的时序 flake，不是数据竞争**（与 README/RELEASE 的 flake 说明一致）。20:47 的 3 个 runtime 测试红在冻结树未复现。

---

## 6. 卫生、依赖、零 cgo

| 检查 | 结果 |
| --- | --- |
| `gofmt -l internal cmd tools` | 0 个文件 |
| `go vet ./...` | 干净（闸门第 3 步 PASS） |
| `scripts/check-nocgo.sh` | PASS（CGO=0 build + 无 cgo 文件 + CGO=1 第三方审计；纯 Go 依赖含 `open-dingtalk/dingtalk-stream-sdk-go`/`gorilla/websocket`） |
| go.mod 依赖新增（Lead 批准） | direct：`doublestar/v4`、`go-humanize`、`open-dingtalk/dingtalk-stream-sdk-go`、`x/sys`；indirect：`gorilla/websocket`；其余不变。`go.sum` 同步 |
| 无密钥 | `sk-…`/AKIA/PRIVATE KEY 扫描 0 命中（仅 `sk-mock` 测试值） |
| 无 commit | HEAD 仍 `5b3507e`；264 脏文件（fix 分支预期） |
| AUDIT.md | Lead 新增 §9（20:52），非 fixer 改动；内容与本验证一致 |
| 交叉编译（抽验） | `GOOS=windows/amd64`、`darwin/arm64`、`linux/arm64` `go build ./...` 全 PASS |
| design/prototype 离线构建 | `CGO_ENABLED=0 GOPROXY=off go build ./...` PASS（钉 mygo v0.7.4） |

---

## 7. PASS/FAIL per group + owners

| 组 | PASS | FAIL | Owner（FAIL） |
| --- | --- | --- | --- |
| ENGINE | 27 | 0 | — |
| RUNTIME | 20 | 0 | — |
| UI2 | 9 | 0 | — |
| DOCS | 6（含 5 类断言） | 1（5 处行号） | **conf-product / fix-docs-final**（纯文档，无代码改动） |
| 机械闸门 | 5 | 0 | — |
| CLI/IM/卫生 | 30（14 CLI + 7 IM + 9 卫生） | 0 | — |

**唯一需要 Lead 动作的**：让 conf-product 按 §4 更新 5 处行号（含修复 `STATUS.md` N-4 状态）；`release-check 16 PASS` 可保留 Lead 实跑记录，或由我在下一轮补跑确认。

**需要重新打开代码的缺陷：无。**

---

## 8. 复现命令

```sh
export PATH=/usr/local/go/bin:$PATH
# ① ② ③
CGO_ENABLED=0 go test ./... -count=1 -skip Live
CGO_ENABLED=1 go test -race ./... -count=1 -skip Live
sh scripts/verify-fixes.sh
# ④ 真实二进制
go build -o /tmp/zeokeel-frozen ./cmd/zeokeel
ST=$(mktemp -d); /tmp/zeokeel-frozen status --state-dir $ST; echo $?   # 1
/tmp/zeokeel-frozen check --state-dir $ST; echo $?                    # 3
/tmp/zeokeel-frozen setup --state-dir $ST --model mock --base-url http://127.0.0.1:1 --workspace web
/tmp/zeokeel-frozen backup --state-dir $ST; /tmp/zeokeel-frozen archive --state-dir $ST --dry-run
/tmp/zeokeel-frozen stop --state-dir $ST; /tmp/zeokeel-frozen status --state-dir $ST; echo $?  # 0（手动停）
(/tmp/zeokeel-frozen -headless --state-dir $ST &); sleep 2
/tmp/zeokeel-frozen status --state-dir $ST; echo $?                   # 0（运行）
/tmp/zeokeel-frozen stop --state-dir $ST --pid <pid>; sleep 2; kill -0 <pid> || echo stopped
# ⑤ IM e2e（fake，无网络）
go test ./cmd/zeokeel -run 'TestVerifyFeishuLongPoll|TestVerifyDingTalkStream|TestVerifyOutbound' -v
go test ./internal/im/dingtalk -run 'TestStream|TestSubscribe' -v
go test ./internal/im/feishu -run 'TestSubscribe|TestPollOnce' -v
# ⑥ 文档行号复现
grep -n "func cmdStatus\|func cmdCheck" cmd/zeokeel/cli.go    # 76 / 143
grep -n "func InspectStatus" internal/runtime/status.go         # 137
grep -n "func (r \*Runtime) stream" internal/runtime/runtime.go # 416
# ⑦
gofmt -l internal cmd tools; go vet ./...; sh scripts/check-nocgo.sh
```

---

*报告：2026-11-03 21:09（Asia/Shanghai），verifier-B。冻结树 21:02:49；无生产代码修改。*
