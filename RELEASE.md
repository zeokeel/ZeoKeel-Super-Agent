# 1.0.0 发布清单

一条不过就不发 1.0.0。每项都要有可复现的证据（测试名、命令输出或文件路径）。
**本清单按 2026-11-03 Wave 2 之后的代码重写；未验证/未实现的不勾，不用"完成了"装饰。**

闸门命令（本地与 CI 同一个）：

```sh
sh scripts/verify-fixes.sh    # build + test + vet + zero-cgo + -race
sh scripts/release-check.sh   # 版本/产物/文档/验收测试 + 上面的闸门
```

## A. 功能完整（对照测试/代码，逐条可复现）

> **最终冻结树测量（2026-11-03 20:53–20:54）**：Lead 的 canonical gate 5/5 通过；
> `CGO_ENABLED=0 go build/vet/check-nocgo` PASS；全量非 race `go test ./... -skip Live` **连续 2 次全绿**；
> 全量 `CGO_ENABLED=1 go test -race ./... -skip Live` **全绿**（含 `cmd/zeokeel` 5.2s / `internal/runtime` 25.7s）。
> 此前 20:47 的 3 个 runtime 测试红与 cmd 偶发 flake 在冻结后的复跑中未再出现。
> `sh scripts/release-check.sh`：**16 通过 / 0 失败 / 6 跳过**（跳过项均需 CI/真机/真实凭据）。以下勾选以此为准。

- [x] 一条请求从聊天出发，经两阶段 Eval 创建 Decision/Ticket，执行后回写原会话（`TestEndToEndFromChatToReply`）
- [x] 同事的请求按 gate + admission 判定强度；`RequireApproval` 时生成待批单（`TestA2ColleagueRequestCarriesTheFourParts`、`TestV2GateAdmissionNineCells`）
- [x] 自身账号的请求无条件执行（`TestA3OperatorRequestRunsWithoutApproval`）
- [x] 敏感外发要人批准（`TestA4SensitiveOutboundEscalates`）
- [x] 首次运行向导能完成并**落库**（`TestOnboardingLeavesAWorkingAgent`；`SaveOnboarding` 写 store）
- [x] 陌生人/机器人不触发动作（`TestA6StrangersAndBotsDoNothing`、`TestV2BotNeverExplicit`）
- [x] 重复投递只产生一个 Ticket（`TestA7RepeatedDeliveryCreatesOneTicket`）
- [x] 两个工单并发执行互不干扰（`TestTwoTicketsRunConcurrently`）
- [x] 人工闭环：回答 Attention / 停止 / 重试 / 暂停恢复（`TestV2LifecycleReadyDroppedStopRetryAttention`、`TestV2PauseShiftsDeadline`）
- [x] 崩溃恢复：中断的 Assignment 变成 note Input + 新 Assignment + patchtoolcalls（`RecoverAssignments`；测试见 `internal/runtime/recovery_test.go`）
- [x] PR 生命周期 follows 与 ended 结果（`TestV2FollowsOutcomes`）
- [x] 后台作业（eino `backgroundtask`，`job_output`/`job_kill`）与子智能体（`internal/agent/jobs.go`、`subagent.go`）
- [x] IM（飞书/钉钉）入口已接线：`cmd/zeokeel/imwiring.go` import 驱动；flag/设置均可配置；未配置不监听。
      钉钉走官方 `dingtalk-stream-sdk-go v0.9.1` Stream 长连接，飞书走 HTTP 历史接口长轮询（无 WS）；两者都不下载媒体
      **未验证**：真实凭据下的端到端（需要飞书/钉钉应用凭据，见 RELEASE-STEPS）
- [ ] **面板可配置 Source 实例 / 规则 / admission / accept mode**：设置页来源组可编辑 IM 凭据，评估组只读事实；
      GitHub 仓库/CLI、规则、admission 表、accept mode 仍不可编辑（owner: fix-ui2）
- [x] 提示词历史与 "Default updated"：`recordPromptMeta` 已在保存路径调用（`internal/runtime/pages.go:557`），
      base-hash 与每层最近 20 版历史写入 kv；面板显示"内置默认值已更新"（无历史列表页）

## B. 可靠性

- [x] kill -9 后重启：Ticket 状态、未完成的 effect、出站回执都在（`internal/engine/persist_test.go`）
- [x] 运行中的 Assignment 在启动时被恢复（`RecoverAssignments`；`recovery_test.go`）
- [x] 悬空 tool_call 由 eino `patchtoolcalls` 修补，不再把坏历史重放给 provider（`internal/agent/engine_adk.go:118`）
- [x] 重复发送的评论被幂等挡住（`internal/sources/github`、`internal/outbox`）
- [x] 长任务上下文压缩后仍能继续（`internal/agent` compact 测试）
- [x] 投递三段式 delivered/read/turnEnded 落库（`internal/engine/lifecycle.go:286-337`）
- [x] `-race` 全量无数据竞争：最终冻结树全量 `-race` 全绿（`cmd/zeokeel` 5.2s、`internal/runtime` 25.7s 均 ok）；
      20:47 的三个 runtime 红与 cmd 偶发 flake 在冻结后未复现

## C. 工程

- [x] `CGO_ENABLED=0 go build ./...`（2026-11-03 实测）
- [x] `CGO_ENABLED=0 go test ./... -count=1 -skip Live`（冻结树连续 2 次全绿）
- [x] `CGO_ENABLED=0 go vet ./...`（20:40 干净）
- [x] `sh scripts/check-nocgo.sh` PASS
- [x] release 二进制 < 40 MB（`make package`：linux 33MB / darwin 32MB；压缩包 12–13MB）
- [x] 无 `TODO`/`FIXME` 遗留在用户路径（`release-check.sh` 检查）
- [x] `CGO_ENABLED=1 go test -race ./... -skip Live`（**唯一**允许 cgo 的测试步骤）——最终冻结树全绿
- [ ] 六平台交叉编译在 CI 跑过本分支（CI cross job 存在；本次未触发 CI）
- [x] `sh scripts/verify-fixes.sh` 整体全绿（最终冻结树：build/test/vet/nocgo/race 全过）

## D. 分发

- [x] Linux 可执行 + `.desktop` + tar.gz
- [x] macOS `.app` zip（**未签名/未公证**）
- [x] Windows `.exe` + zip + `install.ps1`（不是 MSI/NSIS）
- [ ] macOS `.dmg`（未做，需要 macOS 工具链；`RELEASE-STEPS.md` 有步骤）
- [ ] Debian 包 / PKGBUILD / 自动更新（未做）
- [ ] macOS 签名/公证（需要 Developer ID 证书）
- [ ] Windows 真机安装验证（需要 Windows）
- [ ] 三平台安装包由 CI 产出（CI 只做交叉编译 + 体积）

## E. 文档（本组在 Wave 2 收尾时做最终 pass）

- [x] README 说明它是什么、当前状态（已实现/未接线/未实现）、CLI、运维、已知限制
- [x] `design/zeokeel-go-mygo-design.md` §13 实现状态表按代码重写（✅ 行带 live file:line；❌/🟡 带原因）
- [x] `design/rejected-concepts.md`（原型否决 + 本项目否决，含 Wave 2 新增概念）
- [x] `design/ops-runbook.md` 与代码对齐（status/退出码/日志/supervisor/备份/归档/排障）
- [x] `design/how-to-add-a-source.md`（008 cookbook 的 Go 等价物，含仍缺什么）
- [x] `packaging/systemd/zeokeel.service`、`packaging/launchd/com.zeokeel.agent.plist` 与真实 headless/status 对齐
- [x] CHANGELOG 的 1.0.0 节按 Wave 2 事实重写
- [ ] `design/` 与实现最终逐条一致（本清单最后一步）

## F. 阻塞 1.0.0 的已知缺口（按 owner；2026-11-03 最终快照重排）

1. **fix-ui2**：设置页评估组可编辑（规则、admission 三格、accept mode）；GitHub Source 实例/仓库/CLI 的表单与持久化。
2. **fix-engine/fix-runtime**：领域类型收尾（`domain.Source`/`domain.Thread`/`domain.UserError` 未落地，错误仍是自由文本）。
3. **fix-runtime/fix-agent**：无 `steer/inject`；无 Feishu WS/webhook、无 IM 媒体下载；钉钉 Stream 在面板仍标未实现
   （runtime 已支持，`internal/view/channels.go:44-66` vs `internal/runtime/im.go:394-398`，owner fix-ui2）。
   `Runner.Resume` 已删除，恢复走 `RecoverAssignments` + `patchtoolcalls`。
4. **安全**：系统钥匙串（ADR-12）；worker 环境在三处均已剥离凭证变量——prepare（`runtime/prepare.go:103`）、
   bash 子进程（`internal/agent/tools.go` 的 `buildShellEnv/scrubEnv`，变量来自 `runtime/worker.go:429`）。
5. **分发**：`.dmg`/deb/PKGBUILD/NSIS/自动更新；CI 出包与签名公证。
6. **运维**：开机自启/休眠唤醒（委托 systemd/launchd，文档已写）；`--respect-stop` 语义由 `stop` 标记 + 巡检脚本承担，无独立 flag。

## G. rc.1 的签发语义（不要误读）

`VERSION=1.0.0-rc.1` 表示：**候选版可构建、机械闸门（build/test/vet/zero-cgo/-race/package/release-check）全绿**，
但 F 组的产品面缺口未完成。F 组全部完成、E 组最后一项通过后，才由发布负责人把 `VERSION` 改为 `1.0.0`。

## H. Round 2/3 增量（2026-11-03 晚，不新增 release tag）

> 同一 rc.1 内的用户可见增量；代码证据见设计 §13 与 `design/im-channels.md`。不改变"rc.1 仍是候选"的结论。

- [x] 侧栏重构：超级Agent / 工作区树 / 监控（消息·来源·触发·评估·执行）/ 设置；footer OS/arch + 时区
  （`internal/ui/chrome.go:54-99`、`internal/runtime/runtime.go:172-175`）
- [x] 工作区页 + 「＋ 新建工作区」（mygo 原生目录选择器，三平台）+ 「＋ 新建会话」（工作区显式、总是派发）
  （`internal/ui/workspace.go`、`internal/runtime/workspaces.go:20,101`）
- [x] 会话按「我创建的 / 超级Agent 创建的」分组，每工作区最近 50 条（`internal/view/workspace.go`、`internal/runtime/pages.go:953-995`）
- [x] 全局字号 15；设置›部署组显示 OS/arch 与时区（`internal/ui/tokens.go:49`、`internal/runtime/pages.go:763-764`）
- [x] 修复「未受理」：面板本地消息必有答复（列工作区或说明原因/下一步），真活仍建 Ticket
  （`internal/runtime/runtime.go:456-482`、`internal/runtime/workspaces.go:159-214`）
- [x] IM 渠道独立启用/停用 + transport + 检查/授权；禁用=未监听；设置变更只重启变化渠道
  （`internal/runtime/im.go:51,173,189,239,328,588`、`internal/ui/channels.go:134-230`）
- [x] 飞书长轮询 + tenant token 检查（401/403 → `open.feishu.cn/app`）（`internal/runtime/dws.go:101-140`）
- [x] 钉钉默认 DWS CLI：串行 worker、send 优先、1s→30s 退避、cursor 整批接受才推进、隐藏幂等 marker、缺 dws 明确报错；
  官方 Stream 作为可选 transport（`internal/im/dingtalk/dws.go`、`internal/runtime/dws.go`、`design/im-channels.md`）
- [x] 内置工具：plantask（TaskCreate/Get/Update/List）、agentsmd（AGENTS.md）、web_fetch（公网/10s/2MiB/拒私网）、
  web_search（前 5/端点 `execution.web_search_url`）（`internal/agent/builtin.go`、`internal/agent/webtools.go`、
  `internal/runtime/worker.go:480-482`）
- [x] 增量验证（2026-11-03 23:10）：`CGO_ENABLED=0 go build ./...` / `go vet ./...` 通过；
      `go test ./internal/ui ./internal/view ./internal/runtime ./internal/agent ./cmd/zeokeel -skip Live` 通过。
      **全量 verify-fixes/release-check 未在 Round 2/3 代码上重跑**；发布前必须重跑（本组不改变 rc.1 tag）。
- [ ] 已知 UI 不一致：钉钉 Stream 选项在面板仍标"未实现"（runtime 已支持）—— owner fix-ui2
- [ ] 会话/工作区重命名、删除；真实凭据下的 IM E2E（需要凭据/环境）
