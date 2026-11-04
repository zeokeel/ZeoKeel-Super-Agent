# 1.0.0 发布清单

**当前候选：`1.0.0-rc.3`（2026-11-04）。** 发布说明：[RELEASE-NOTES-v1.0.0-rc.3.md](RELEASE-NOTES-v1.0.0-rc.3.md)。

一条不过就不发 1.0.0。每项都要有可复现的证据（测试名、命令输出或文件路径）。
**本清单按 2026-11-04 rc.3 代码重写；未验证/未实现的不勾，不用"完成了"装饰。**

闸门命令（本地与 CI 同一个）：

```sh
sh scripts/verify-fixes.sh    # build + test + vet + zero-cgo + -race
sh scripts/release-check.sh   # 版本/产物/文档/验收测试 + 上面的闸门
```

## A. 功能完整（对照测试/代码，逐条可复现）

> **本清单对应 2026-11-04 rc.3 最终代码（P0/P1 + UI/UX 轮 + rc.2 + DSH 对齐）。**
> 2026-11-03 20:53 的历史快照曾全绿（2×非 race + 1×-race + release-check 16/0/6），但修复波改了大量并发/IM/UI 代码，
> **不能拿旧快照当本轮证据**：由 Lead 在合并前跑 `sh scripts/verify-fixes.sh` 与 `sh scripts/release-check.sh` 后再勾 C/B。
> 以下 A 组行为勾选按最终代码；未能本机验证的仍不勾。

- [x] 一条请求从聊天出发，经两阶段 Eval 创建 Decision/Ticket，执行后回写原会话（`TestEndToEndFromChatToReply`）
- [x] 同事的请求按 gate + admission 判定强度；`RequireApproval` 时生成待批单（`TestA2ColleagueRequestCarriesTheFourParts`、`TestV2GateAdmissionNineCells`）
- [x] **DSH 对齐（行为变更）**：admitted 明确 IM/GitHub 请求由 Eval 直接接单 → Ticket（无默认审批）；推断请求按
      accept mode off/observe/auto（auto 需匹配规则）；`DecPropose` 仅 opt-in `RequireApproval` 且非 explicit
      （`TestA3OperatorRequestRunsWithoutApproval`；`cmd/zeokeel/acceptance_test.go:129-160`；`internal/engine/evaluate.go:348-390`）
- [x] 敏感外发要人批准（`TestA4SensitiveOutboundEscalates`）
- [x] 首次运行向导能完成并**落库**（`TestOnboardingLeavesAWorkingAgent`；`SaveOnboarding` 写 store）
- [x] 陌生人/机器人不触发动作（`TestA6StrangersAndBotsDoNothing`、`TestV2BotNeverExplicit`）
- [x] 重复投递只产生一个 Ticket（`TestA7RepeatedDeliveryCreatesOneTicket`）
- [x] 两个工单并发执行互不干扰（`TestTwoTicketsRunConcurrently`）
- [x] 人工闭环：回答 Attention / 停止 / 重试 / 暂停恢复（`TestV2LifecycleReadyDroppedStopRetryAttention`、`TestV2PauseShiftsDeadline`）
- [x] 崩溃恢复：中断的 Assignment 变成 note Input + 新 Assignment + patchtoolcalls（`RecoverAssignments`；测试见 `internal/runtime/recovery_test.go`）
- [x] PR 生命周期 follows 与 ended 结果（`TestV2FollowsOutcomes`）
- [x] 后台作业（eino `backgroundtask`，`job_output`/`job_kill`）与子智能体（`internal/agent/jobs.go`、`subagent.go`）
- [x] IM 渠道已接线且**DSH 对齐**：飞书新装默认 `larkws` 长连接、旧配置迁移 pin longpoll（fallback）；钉钉默认 DWS
      个人 OAuth CLI（长驻 NDJSON，无 poll/cursor），官方 Stream 为 bot 模式可选 transport；admitted 明确请求直接接单，
      推断按 accept mode，旧式 opt-in `RequireApproval` 才 proposal；三态启用 + bool 兼容 + flag-only bootstrap；
      per-channel generation/Close/sender 注销；secret 不回显、status/Inspect redaction（`cmd/zeokeel/imwiring.go`、
      `internal/runtime/im.go`、`internal/engine/evaluate.go:348-390`、`design/im-channels.md`）
      **未验证**：真实凭据/真实 DWS 租户端到端（需要环境）
- [ ] **面板可配置 Source 实例 / 规则 / admission / accept mode**：设置页来源组用连接向导配置 IM（凭据/transport/检查/授权），评估组只读事实；
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
- [ ] `-race` 全量无数据竞争：2026-11-03 快照曾全绿；**修复波后（异步边界/IM 生命周期/chat）必须由 Lead 重跑**再勾

## C. 工程

- [ ] `CGO_ENABLED=0 go build ./...`（待 final gate）
- [ ] `CGO_ENABLED=0 go test ./... -count=1 -skip Live`（待 final gate）
- [ ] `CGO_ENABLED=0 go vet ./...`（待 final gate）
- [x] `sh scripts/check-nocgo.sh` PASS
- [x] release 二进制 < 40 MB（`make package`：linux 33MB / darwin 32MB；压缩包 12–13MB）
- [x] 无 `TODO`/`FIXME` 遗留在用户路径（`release-check.sh` 检查）
- [ ] `CGO_ENABLED=1 go test -race ./... -skip Live`（**唯一**允许 cgo 的测试步骤；待 final gate）
- [ ] 六平台交叉编译在 CI 跑过本分支（CI cross job 存在；本次未触发 CI）
- [ ] `sh scripts/verify-fixes.sh` 整体全绿（待 Lead final gate）

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

## F. 阻塞 1.0.0 的已知缺口（按 owner；2026-11-04 最终代码 + 微调后重排）

1. **fix-ui2**：设置页评估组可编辑（规则、admission、accept mode）；GitHub Source 实例/仓库/CLI 表单与持久化。
2. **fix-engine/fix-runtime**：领域类型收尾（`domain.Source`/`domain.Thread`/`domain.UserError` 未落地）。
3. **fix-runtime/fix-agent**：`steer/inject`；飞书 webhook 与 IM 媒体下载；多账号/source instance 模型
   （当前是单实例 `source.*` 键 + 每渠道 self id）。
4. **安全**：系统钥匙串（ADR-12）未实现——密钥仍明文存 SQLite（0600）；读取侧已 redaction
   （`store/redact.go`、`inspect.go`），worker 环境已剥离凭证。真实 IM 凭据 E2E 仍需环境。
5. **分发**：`.dmg`/deb/PKGBUILD/NSIS/自动更新；CI 出包与签名公证。
6. **运维**：开机自启/休眠唤醒（委托 systemd/launchd）；`--respect-stop` 由 stop 标记 + 巡检承担（无独立 flag）。
7. **平台/工具/UX 收尾**：真正在原项目执行（当前执行副本 + 显式「应用到项目」）；多窗口；自动更新只检查不下载；
   真实飞书/DWS 租户 E2E；`read_image` 无像素（模型 text-only）；无 `orchestrator_read` 与 DSH main orchestrator 工具集；
   浏览器需 Chrome/Edge + node/npx；打包仍无 `.dmg`/deb/安装器。
## G. rc.3 的签发语义（不要误读）

`VERSION=1.0.0-rc.3` 表示：**候选版可构建、机械闸门（build/test/vet/zero-cgo/-race/package/release-check）全绿**，
但 F 组的产品面缺口未完成。F 组全部完成、E 组最后一项通过后，才由发布负责人把 `VERSION` 改为 `1.0.0`。

## H. Round 2/3 + 修复波 + UI/UX 优化轮增量（2026-11-04，不新增 release tag）

> 同一 rc 候选内的用户可见增量（当时 rc.1；后续 rc.2/rc.3 延续，不改变“仍是候选”的结论）。证据见 `design/im-channels.md` 与设计 §13。

- [x] 侧栏（UX 轮后）：超级Agent / 收件箱 / 项目 / 执行 / 设置 + 折叠「诊断」；footer OS/arch + 时区
- [x] 工作区：原生目录选择器、opaque ID、重命名、删除（仅空工作区，有工单拒绝）、绝对路径/重复 root 拒绝、
  工作区过滤（消息/执行可切「全部」）
- [x] 超级Agent 聊天 + 派活双态：模型回答（未配模型则确定性回答并说明）、「已派发 #N」、「需要你确认」；
  回答内联在输入框上方与总览对话；非失败不标红；历史最多 20 条并持久化
- [x] 异步命令边界：发送/新建会话/保存设置走后台命令锁，面板显示「发送中/保存中/连接中」；主线程不等 DB/模型/渠道
- [x] Ticket 身份：行/详情查找与详情 VM 的 ID 都是 domain ID，`Number` 单独显示 `#number`
- [x] IM DSH 对齐：明确请求直接接单；推断按 accept mode；`DecPropose` 仅旧式 opt-in `RequireApproval`；
  收件箱只显示「旧式请求（需要批准）」；A3/A7 已按 DSH 语义更新
- [x] 飞书：**新装默认** `feishu_transport=ws`（官方 `larkws` 长连接，推荐）；旧凭据-only 配置迁移时 pin `longpoll`；
  面板 Options 只有 `ws`/`longpoll`；longpoll 为显式 fallback（cursor）；401/403 给 `open.feishu.cn/app`
- [x] 钉钉 DWS 真实契约：长驻 `dws event +listen-im … -f ndjson`（无 poll/cursor；event id 去重；1s→30s 重连）、
  `dws chat +messages-send --as user`（无 idempotency-key，隐藏 marker）、`dws auth status --format json`、
  `dws auth login [--device]`；DWS=个人 OAuth，stream=bot（app key/secret/robot）
- [x] IM 生命周期：三态启用 + bool 编码兼容 + flag-only bootstrap；每渠道 generation/串行 apply；
  停止 cancel→Close→注销 sender；切换 transport 清 cursor；CheckChannel 复用运行中 DWS 后端
- [x] 设置：UI 开关读写 canonical `1/0`（读兼容旧 true/false）；`LoadSettings` 一次性迁移 bool/IM 启用/transport/`im_self` + schema stamp；
  SaveSettings 事务化（先快照后换内存）；分组真实生效方式；secret 不回显且 status/Inspect/health redaction（`TestLegacySettingsMigration`）
- [x] 工具：TaskCreate/Get/Update/List 与注册名一致、任务 JSON 在 `.zeokeel/plan`；AGENTS.md 不可信仓库信封 ≤64KB；
  web_fetch DNS pin/`Proxy:nil`/拒私网/2MiB/`<untrusted-web-content>`；`plan_tasks/agents_md/web_tools` 开关
- [x] 界面：`ui.font_scale` 在设置›部署投影并可编辑（90/100/110%），经 `fs()` 统一缩放；启动窗口最小 1100×720
- [x] **导航/IA**：日常 超级Agent / 收件箱 / 项目 / 执行 / 设置 + 折叠「诊断」组（对话记录·消息·连接·触发·评估）；
  UI 以「项目」称呼原工作区（`internal/view/vm.go:486-520`、`internal/ui/chrome.go:60-110`）
- [x] **模型设置**：设置›模型（端点/API Key/模型名）+「测试连接」探测已保存配置；保存后即时生效；
  未配模型时总览「去配置模型」、任务停在待你决定（`runtime/ux.go:36-125`、`runtime/pages.go:543-558`）
- [x] **首跑**：必填/可选标记、模型「保存并测试」、「稍后设置（演示模式）」、项目根目录原生选择器、
  完成后落点提示（`internal/ui/onboarding.go:60-180`、`internal/runtime/ux.go:445-452`）
- [x] **输入条**：多行 Enter 换行/⌘Ctrl+Enter 发送（IME 安全）、target chip 聊天/项目派活、失败保留草稿+再次发送、
  回答展开/复制/查看全部到「对话记录」（`internal/ui/composer.go:19-220`、`internal/ui/commands.go:137-188`）
- [x] **收件箱**：统一「待你决定 N」（runtime 单源）、批准/拒绝/回答/重试/停止、j/k/Enter、pending/就地错误、
  隐藏窗口系统通知（`internal/ui/inbox.go:1-350`、`internal/runtime/ux.go:279-440`、`view/attention.go`）
- [x] **执行页行内动作**：回答/重试/停止 + pending/错误、`#N`（`internal/ui/execution.go:150-210`、`view/ticket.go:69-88`）
- [x] **路径真相**：详情「项目目录 / 执行副本」可打开/复制，交付物可打开/复制；提示词不再声称修改项目
  （`internal/ui/ticketdetail.go:250-286`、`internal/runtime/ux.go:279-303`、`internal/runtime/worker.go:570-582`）
- [x] **IM 连接向导**：按 transport 字段、draft 校验+检查不保存、通过才保存/显式仍保存、DWS 版本/登录命令/授权链接、
  Feishu chats（ws 可选/longpoll 必填）、暂停监听（`internal/ui/channelwizard.go:1-520`、`internal/runtime/ux_im.go`、
  `internal/runtime/im.go:1392-1505`、`internal/im/dingtalk/dws_setup.go`、`internal/im/feishu/ws_chats.go`）
- [x] **视觉/可访问性**：对比度 token、focus token、Focusable/Role/Label/Tooltip、28px 目标/16px 图标、
  状态栏去 debug、`StateKey:"main"`+最小 1100×720、⌘N/⌘, 快捷键（`internal/ui/tokens.go:100-145`、
  `internal/ui/chrome.go:143-160`、`internal/ui/app.go:320-330,441-455`）
- [ ] **明确未做（F-7）**：真正原项目执行、系统钥匙串、多窗口、自动更新下载/安装、真实飞书/DWS 租户 E2E
  （rc.2 已补原生菜单/系统无障碍偏好/计划/About；见 I 组与 README「已知限制」）
- [ ] **最终全量闸门未在本轮代码上运行**：由 Lead 在合并前跑 `sh scripts/verify-fixes.sh` 与
  `sh scripts/release-check.sh`；本组不预称全绿。`VERSION` 在 I 组（rc.2）由发布负责人 bump。

## I. rc.2 增量（2026-11-04，发布候选）

> 在 UI/UX 轮之后补齐交互审计剩余项；`VERSION` 由发布负责人 bump 为 `1.0.0-rc.2`。逐条有代码证据，不预称测试全绿。

- [x] 聊天流式输出：SSE 增量、60ms 节流、打字光标、停止生成（部分保留）、最终只追加一次
  （`internal/runtime/rc2.go:24-190`、`internal/ui/composer.go:241-305`）
- [x] Markdown 渲染（标题/粗斜体/行内代码/围栏代码/列表/http(s) 链接；不解析 HTML）
  （`internal/ui/markdown.go:19-180`、`internal/ui/chat.go:75`）
- [x] 计划/待办可见：`.zeokeel/plan/*.json` → PlanVM；执行详情+对话任务卡；状态计数
  （`internal/view/plan.go:1-51`、`internal/runtime/rc2.go:302-433`、`internal/ui/planpanel.go:29-85`）
- [x] 应用到项目：只复制记录交付物、拒绝越界、同名 `.bak-<timestamp>` 备份、返回相对路径、UI 确认列文件/目标/覆盖警告
  （`internal/runtime/rc2.go:193-270`、`internal/ui/applyproject.go:1-199`）
- [x] 拒绝无损 + 撤销：保留 proposal/RejectedAt；10s toast；幂等、superseded 拒绝、重启安全；不建单/不启动/不回复
  （`internal/engine/lifecycle.go:723-815`、`internal/engine/persist.go:835-860`、`internal/runtime/rc2.go:436-463`、`internal/ui/inbox.go:59-66`）
- [x] 原生应用菜单 App/文件/编辑/窗口；新建对话 ⌘N、设置 ⌘,、打开主窗口、标准编辑 roles
  （`internal/ui/menu.go:1-72`、`internal/ui/app.go:345`）
- [x] 系统无障碍偏好：TextScale×`ui.font_scale`、ReduceMotion、HighContrast、系统 Accent
  （`internal/ui/prefs.go:1-61`、`internal/ui/app.go:441-462`、`internal/ui/stream.go:59`）
- [x] 设置›关于：版本/许可证；检查更新只查询 GitHub Releases API（不下载），显示最新版本/发布页
  （`internal/ui/about.go:1-117`、`internal/runtime/rc2.go:355-415`、`internal/view/pages.go:469-470,644`）
- [ ] **rc.2 明确未做**：真正在原项目执行（执行副本 + apply-back）、系统钥匙串、多窗口、自动更新下载/安装、
  真实飞书/DWS 租户 E2E；打包仍无 `.dmg`/deb/安装器
- [ ] **最终闸门**：Lead 在 bump VERSION 后跑 `sh scripts/verify-fixes.sh` 与 `sh scripts/release-check.sh`，
  再 tag `v1.0.0-rc.2`；本文不预称全绿。

## J. rc.3 增量（2026-11-04，发布候选）

> DSH 主链对齐 + 项目级浏览器/MCP + 工具补齐；`VERSION` 由发布负责人 bump 为 `1.0.0-rc.3`。

- [x] **DSH 对齐**：admitted 明确 IM/GitHub 请求 Eval 直接接单；推断请求 accept mode off/observe/auto（auto 需规则）；
  `DecPropose` 仅 opt-in `RequireApproval`（推断）；收件箱旧式 proposal 文案；移除 `AutoApproveIM`
  （`internal/engine/evaluate.go:348-390`、`internal/engine/gate.go:52-56`、`internal/ui/inbox.go:106`、`internal/ui/overview.go:109`）
- [x] **项目面板**：概览/设置/统计；per-workspace 浏览器（Chrome/Edge、无头、CDP 端口 0=自动、profile 只读）、启停/状态、
  技能开关、统计（会话/任务/进行中/等待/完成/失败/最近活动[无投影时空态]/工具可用性）
  （`internal/view/workspacepanel.go:1-99`、`internal/ui/workspacepanel.go:193-520`、`internal/runtime/workspacesettings.go:75-160`）
- [x] **浏览器+MCP**：默认路径检测、每项目独立 profile/唯一端口、启动 flags、`npx -y chrome-devtools-mcp@latest
  --browserUrl http://127.0.0.1:<port>`、`mcp__browser__*` 只注入该项目、env scrub、缺 Chrome/Edge/node 可操作错误
  （`internal/browser/detect.go:10-65`、`internal/browser/manager.go:99-107,185-245`、`internal/browser/launch.go:22-56`、`internal/browser/mcp.go:12-15,44-48,118-140`、
  `internal/runtime/worker.go:468`、`internal/runtime/workspacesettings.go:427-480`）
- [x] **工具**：`read_image`（元数据-only，无像素）、`job_list`、`subagent` 命名；Worker 域工具
  `ack/handback/thread_unrelated/request_status`；SuperAgent 无工具
  （`internal/agent/imageread.go:1-120`、`internal/agent/jobs.go:275-309`、`internal/agent/subagent.go:55`、
  `internal/runtime/artifacts.go:121-222`、`internal/runtime/rc2.go:121-132`）
- [ ] **rc.3 明确未做**：真正原项目执行（副本+apply-back）、系统钥匙串、多窗口、自动更新下载、真实飞书/DWS E2E、
  `read_image` 视觉、`orchestrator_read`/DSH main orchestrator 工具、浏览器依赖 Chrome/Edge+node/npx、打包无 .dmg/deb
- [ ] **最终闸门**：Lead bump VERSION 后跑 `verify-fixes.sh`+`release-check.sh`，tag `v1.0.0-rc.3`；本文不预称全绿。
