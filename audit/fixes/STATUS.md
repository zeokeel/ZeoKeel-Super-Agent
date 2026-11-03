# 修复状态总表（Lead 维护）

> 状态口径：`FIXED` = 代码/文档已改且有回归测试；`VERIFIED` = 另经独立反证通过；`DOC-DEVIATION` = 有据的产品/平台偏离，文档已明写；`REMOVED` = 删除；`NOT-REQUIRED` = 文章明确开放问题；`OPEN` = 尚未关闭。
> 证据索引：`audit/fixes/*.md`（各 owner 交接）、`audit/verify/wave1.md` / `wave2.md`（反证）、`audit/AUDIT.md`（原始审计）。
> 代码基线：分支 `fix/audit-remediation`；所有改动未 commit，最终由 Lead 统一提交。

## A. 框架/自研（AUDIT §3）

| ID | 问题 | 处置 | 状态 |
| --- | --- | --- | --- |
| N-1 | 飞书/钉钉手写驱动未接线（2,229 行） | 驱动修复（token 单飞/事件签名+AES/分页/去重/JSON 转义/重试分层）+ cmd 接线 + 官方 DingTalk Stream SDK 接收 | FIXED+VERIFIED |
| N-2 | 悬空 tool_call 恢复未接线 | eino `patchtoolcalls` + RunSession 恢复；回归测试 | FIXED+VERIFIED |
| N-3 | 文件工具手写 | 迁 eino `adk/middlewares/filesystem` + os.Root Backend，名字/描述保留 | FIXED+VERIFIED |
| N-4 | CI `-race` 与零 cgo 冲突 | CI 单步 `CGO_ENABLED=1`；`scripts/verify-fixes.sh` 5/5 PASS；产品构建仍零 cgo | FIXED+VERIFIED |
| N-5 | glob `**` 假承诺 | doublestar over os.Root.FS；测试 | FIXED+VERIFIED |
| N-6 | `inside()` 词法越界 | os.Root（工具 + skills） | FIXED+VERIFIED |
| N-7 | skills 自建加载 | 保留确定性匹配（eino `skill` 中间件评估后不采纳，理由记录）；路径 os.Root | DOC-DEVIATION |
| N-8 | UI 4 处手搓 mygo 控件 + ListState.Key bug | List/Table/Header/Key；选择跟随实体测试 | FIXED+VERIFIED |
| N-9 | theme/tokens 硬编码、暗色失效 | 从 `c.Theme()` 派生；暗色测试 | FIXED+VERIFIED |
| N-10 | groupedNumber/trimSpace 重复 | humanize.Comma / strings.TrimSpace（全角空格修复） | FIXED+VERIFIED |
| N-11 | 格式化/截断重复 | `internal/humanfmt`；ui/view 已切；runtime/brief 收敛（Wave2） | FIXED（部分待切） |
| N-12 | cron N/step bug + DST 自研 | 修复 + time.Date 往返；四类 DST 测试；不引入 robfig | FIXED+VERIFIED |
| N-13 | schema v3 旁路 | 折叠进 migrations，SchemaVersion=3；真实旧库升级测试 | FIXED+VERIFIED |
| N-14 | GitHub gh CLI 手写分页/幂等 | 全翻页、marker 全页、ETag/304（`gh api -i`）、ReceiveAll/CI；官方 SDK 未引入（ADR-7 理由更新） | FIXED+VERIFIED / DOC-DEVIATION（SDK） |
| N-15 | DingTalk transport 重试重复发送 | 发送 Attempts=1；Outbox 重试；测试 | FIXED+VERIFIED |
| P2 | engine_react/MCP API/workflow 持久层/demo 572/9 图标/seedstate/genicon/占位符 | 删除/修复；handoff 列明 | FIXED（VERIFIED 部分） |

## B. 需求符合性（AUDIT §4/§5；分片矩阵）

| 组 | 关键缺口 | 处置 | 状态 |
| --- | --- | --- | --- |
| 000 哲学 | Proposal/Goal/Schedule/ReplyMode 概念膨胀；Goal/Schedule 违反 §5.9 | Proposal 保留（数据敏感第二道门，文档登记）；Goal/Schedule 仍为独立实体（DOC-DEVIATION，理由写入设计） | DOC-DEVIATION |
| 001 领域 | 无 Eval/Thread 聚合、Mandate 不完整、订阅半实现、Attention 死路 | Engine 重写：Eval 队列/批处理、Mandate basis/rules/scope、Inputs/Follows/Owners、TicketSnapshot、Attention/Answer、revalidate | FIXED（待 verifier） |
| 002 授权 | gate 不产 strength、无 admission/bot list、revalidate 缺失、模型可决定授权 | 确定性 gate + 3-cell admission + bot list + accept mode + revalidate；模型输出无 strength | FIXED（verifier 大部分 PASS；5 个缺陷修复中） |
| 003 执行 | 阶段/结束/交付/PR/slot/retry/pause/prepare/后台 | 阶段表 golden、ready/follows/dropped 结束、ThreadClosed/follow tool、Delivery 三段、human slot、stop/retry/pause/resume、prepare+执行变量、eino backgroundtask+30 分钟规则、Assignment 恢复 | FIXED+VERIFIED |
| 004 提示词 | 团队三段/版本/base-hash/校验/热更新 | 团队三段、版本历史(20/层)、base-hash/Default updated、校验、每轮热更新；旧 strength 已移除 | FIXED+VERIFIED |
| 005 面板 | 诚实状态、Ticket 详情/动作、设置组、glossary、暗色 | UI2 完成；runtime 投影已填；需 verifier | FIXED（待 verifier） |
| 006 部署 | status/退出码/日志/备份/归档/watchdog/巡检 | runtime 实现 status 0/1/2/3、slog、supervisor、backup/archive、-headless；systemd/launchd 模板 | FIXED（待 verifier） |
| 007 架构 | 入口绕过持久化、游标、schema 缺表、扩展路径 | 入口统一 Persistence、游标 kv、trigger_thread/decision.thread/outbox.dropped、assignment（engine 侧）；`design/how-to-add-a-source.md` | FIXED+VERIFIED |
| 008 扩展 | 新增 Source 不可执行 | `design/how-to-add-a-source.md` Go cookbook（可执行步骤+现存缺口清单） | FIXED(docs) |
| 文章 Router 层 | 无持久会话/批处理/全局记忆/统一回复 | 逻辑 Router：持久化状态 + 两阶段批处理 + 统一 composer（变体，文档写明） | FIXED/DOC-DEVIATION |
| 文章订阅直通 | 无 Worker 直达/退订/交回 | Inputs 投递 + HandBack/DropSubscription/MarkUnrelated；mid-turn steer 未做（下轮投递，文档写明） | DOC-DEVIATION |
| 文章全双工 | start/stop/steer/inject | eino backgroundtask jobs + 后台结果唤醒；steer/inject/TurnLoop 未接（文档写明） | DOC-DEVIATION |
| 文章 Watchdog | 外部 watchdog | 进程内 supervisor + systemd/launchd + watchdog-check.sh；外部独立 Session 未做（桌面无宿主） | FIXED/DOC-DEVIATION |
| 文章存算分离 | 单机 | 单机单二进制（ADR-1/10），保留 SQLite/崩溃恢复；跨设备/休眠/离线不实现 | DOC-DEVIATION |
| 文章 §8 A2A/多租户/安全 | 开放问题 | 不实现 | NOT-REQUIRED |

## C. 反证与门禁（最终）

| 验证 | 状态 |
| --- | --- |
| Wave1 反证（agent/core/ui/integrations） | 69 PASS / 10 FAIL（FAIL 属 Wave2 in-flight 或 docs），行为性修复 CONFIRMED |
| Wave2 反证（engine/runtime/ui2/docs） | **最终：96 PASS / 1 FAIL（仅 5 处 docs 行号，已回派 docs-only）/ 1 UNVERIFIED；无代码缺陷**；engine 27/27、runtime 20/20、UI 9/9、gate 5/5 |
| 机械闸门 | **最终 PASS**：`verify-fixes.sh` 5/5（build/test/vet/nocgo/full -race）；`release-check.sh` 16 PASS / 0 FAIL / 6 SKIP；全量 test ×2 绿；三平台交叉编译绿 |
| 文档真实性 | Wave1 8 处已修；Wave2 功能已二轮复核（final 微调后复核） |
| 零 cgo / 依赖 | 批准新增纯 Go `open-dingtalk/dingtalk-stream-sdk-go`（gorilla/websocket indirect）；doublestar/humanize/x/sys direct；`check-nocgo.sh` PASS |
