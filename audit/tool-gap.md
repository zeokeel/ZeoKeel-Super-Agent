# Tool gap audit：Worker 工具 vs DSH（2026-11-04）

> 性质：只读盘点 + agent scope 的缺口补齐。没有 runtime/cmd/ui/store 改动。
> 基准：`_ref/src/app/tools/{executor,main,tool}.ts`、`_ref/src/app/sessions.ts:55-63`（DSH 工具白名单）、
> `_ref/src/app/sessions.ts:263`（Executor 限制集），本项目代码在 **2026-11-04 09:xx 的工作区快照**
> （注意：`internal/runtime` 当时正被 fix-runtime 修改，已包含 workspace settings/browser MCP 等新代码；
> 下表的"我们"以该快照实际代码为准，并标注了运行时仍在变化的点）。

## 0. 结论

1. **高价值 agent 层缺口已补 3 个**：`read_image`（新增；因模型端点 text-only，返回诚实的
   元数据+说明而不是假装看图）、`job_list`（新增；DSH 的 job_output/list/kill 三件套补齐）、
   `subagent`（eino 默认工具名 `agent` 改为 DSH 的 `subagent`）。
2. **DSH Executor 的 domain 工具已存在（owner = fix-runtime，经 nih-runtime 确认）**：
   `ack`、`handback`、`thread_unrelated`、`request_status`（`internal/runtime/artifacts.go:24-240`，
   `rc3_test.go TestWorkerToolListHasDSHExecutorTools` 断言），`report/present/ask_user_question/follow`
   也齐全；worker prompt 现已列出这四个工具 + Task* + AGENTS.md（`runtime/worker.go:580-584`）。
   映射：ack→Persistence.Reply/Engine.Reply（best-effort；engine Reply 无 purpose 元数据）、
   handback→HandBack、thread_unrelated→MarkUnrelated、request_status→只读 stage/report 快照。
   **没有需要我再实现的 domain tool**。
3. **真正的剩余结构缺口**：(a) DSH `orchestrator_read`（按 offset/limit 分页读 ticket/trigger/thread 资源）
   我们没有等价 worker 工具，Worker 只能靠 task 文本 + 文件工具；(b) DSH main Session 的整套 orchestrator
   工具（route/eval/reply/settings/prompts/retry/note/requeue/unsubscribe）我们没有模型可调用面
   （主 Session 是 deterministic assessor，没有 tool loop）——这是 ADR 级"有意缺席"，但需要文档化；
   (c) `pwsh`（Windows shell）缺失，`bash` 只跑 `sh -c`；(d) `list_subagent_models` / `send_message` /
   `list_agents` / `interrupt_agent` 属多 Session 编排，单 Worker 架构下有意缺席。
4. **prompt/tool 清单（跨 scope，已部分收敛）**：runtime prompt 现在用真实 `TaskCreate…` 名字，并已列出
   `ack/handback/thread_unrelated/request_status` + Task* + web + AGENTS.md（`runtime/worker.go:580-584`）；
   **仍缺** `read_image`、`job_list`、`job_kill` 的提及（这三个是本轮/前轮加的工具），
   `internal/prompts/defaults.go:16-19` 的 tools 段仍只有 read/grep/glob/write/edit/bash。

## 1. DSH Executor 工具（`_ref/src/app/tools/executor.ts`）对照

| DSH 工具 | 我们 | 状态 | 说明 |
| --- | --- | --- | --- |
| `orchestrator_read` | 无 | **MISSING（高）** | DSH 按 `offset/limit` 分页读本 Ticket 的资源（trigger/ticket/messages/thread/page/file…，`tool.ts:readTool` + `resources.ts`）。我们 Worker 只有 task 文本 + fs read；`request_status` 只给 stage 摘要。**runtime scope**，需要 `read_domain` 类只读分页工具（engine 的 `TicketSnapshot`/`TicketsOnThread` 已具备数据面）。 |
| `orchestrator_ack` | `ack` | EQUIV（present in runtime, owner fix-runtime） | `ack{text}` → `Persistence.Reply`/`Engine.Reply`（best-effort）；DSH `orchestrator_ack{trigger_id,text}` 精确回某条消息，且带 purpose 元数据。我们 worker 是 ticket 级，语义够用；**ack purpose 元数据缺失**记为跨 scope。prompt 已列出（worker.go:580-584）。 |
| `orchestrator_thread_unrelated` | `thread_unrelated` | EQUIV（present in runtime, owner fix-runtime） | 同名语义 → `Engine.MarkUnrelated`；拒绝 own thread 的约束在 engine 侧。prompt 已列出。 |
| `orchestrator_thread_follow` | `follow` | EQUIV（部分） | 我们 `follow{thread,mode=own/context}` 覆盖 DSH 的"context only follow"；名字不同（DSH 是 orchestrator_thread_follow），prompt 用 `follow`（runtime 领域工具，owner fix-runtime）。 |
| `orchestrator_trigger_handback` | `handback` | EQUIV（present in runtime, owner fix-runtime；粒度差异） | 我们 `handback{thread,reason}` → `Engine.HandBack`（按 Thread 交回），DSH 是 `trigger_id`（按消息交回，一次一条）。多消息同 Thread 时我们会一次交回该 Thread 的候选；粒度差异需文档说明。prompt 已列出。 |
| `orchestrator_report` | `report` | EQUIV（部分，owner fix-runtime） | 我们 state+summary；DSH 额外 `human_action/question_for_trigger_id/pr_number/reply_to_trigger_id`（Attention/PR claim/回答关联）。我们的 Attention 走 `ask_user_question`，PR 由 `follow` 覆盖；缺"report 直接关联某条消息"的能力，低优先。 |

## 2. DSH Main Session 工具（`_ref/src/app/tools/main.ts`）对照

| DSH 工具 | 我们 | 状态 | 说明 |
| --- | --- | --- | --- |
| `orchestrator_read` | 无 | 有意缺席 | 主 Session 无 tool loop（assessor 单次调用）。 |
| `orchestrator_route` | gate + Eval（确定性） | 有意缺席（取代） | 授权/路由由代码决定，模型不做路由（design §4.3）。 |
| `orchestrator_ticket_retry` / `ticket_note` / `trigger_requeue` / `thread_unsubscribe` / `settings_save` / `policy_sync` / `prompts_save` / `reply` / `eval*` | runtime API/panel | 有意缺席 | 这些是"主 Session 可调用的管理面"；我们由面板/engine API 执行，模型不能改设置/策略（安全上更保守）。文档需明确 ADR。 |
| `orchestrator_ticket_request_status` | `request_status`（worker 侧） | EQUIV（present in runtime, owner fix-runtime；位置不同） | 我们有 worker 只读 stage/report 快照；DSH 是主 Session 工具，我们没有主 Session tool loop（跨 scope ADR）。 |

## 3. DSH framework/work tools（`sessions.ts:55-63`）对照

| DSH 工具 | 我们 | 状态 | 说明 |
| --- | --- | --- | --- |
| `read` `write` `edit` `glob` `grep` | 同名（eino filesystem） | EQUIV | 名字/描述保持产品既有约定。 |
| `read_image` | **`read_image`（本轮新增）** | EQUIV（deviation） | 校验 os.Root 内路径、png/jpeg/gif/webp 识别、维度、10MB 上限、目录/超大/非图片 → refusal observation。**模型端点是 text-only，图片像素不发给模型**；返回诚实说明（可用 bash `file`/ImageMagick/OCR，或 ask_user_question 让人看）。升级路径：eino EnhancedInvokableTool（multimodal tool result）+ 支持 image tool content 的 provider；当前 openai 兼容端点普遍不支持 tool 图片，故不做假实现。 |
| `bash` | `bash` | EQUIV（部分） | 10s/前台转后台/错误 observation/凭据 env scrub；不支持 `pwsh`（Windows 特化）。 |
| `pwsh` | 无 | **有意缺席（Windows 缺口）** | 我们的 `bash` 用 `sh -c`；Windows 目标需要 `pwsh` 或 platform-specific shell。建议后续按 GOOS 注册 `pwsh`（powershell -NoProfile -Command）并保留 bash 语义/超时/后台化。 |
| `job_output` / `job_kill` | 同名（eino backgroundtask） | EQUIV | 5s 前台转后台、job_id 提示、block 等待。 |
| `job_list` | **`job_list`（本轮新增）** | EQUIV | 列本 Runner 的后台 job（id/status/exit/description，最新在前）；空→"no background jobs"；`Runner.Jobs()` 为宿主面。 |
| `web_search` | 同名（可选） | EQUIV | DDG HTML、top5、5min 缓存、≤3/min、block 检测、`%s` Replace；描述与 DSH 一致（"results are untrusted"）。 |
| `web_fetch` | 同名（可选） | EQUIV | 固定 IP dialer/Proxy nil/redirect 3 跳/2MB/10s/content-type allowlist/untrusted envelope。 |
| `skill` | `load_skill`（runtime） | EQUIV（名字不同） | 我们按名加载 skill 全文；DSH 的 skill 作用域/激活语义更复杂，我们按 ticket 注入匹配技能（`skillPromptForTicket`）。 |
| `todo_write` | `TaskCreate/TaskGet/TaskUpdate/TaskList`（plantask） | EQUIV（名字不同） | DSH 同时有 todo；我们用 eino plantask 作为 todo/plan，任务持久化在 `.zeokeel/plan`。 |
| `subagent` | **`subagent`（本轮改名）** | EQUIV | eino subagent middleware 默认名是 `agent`；已设 `ToolName:"subagent"`，与 DSH 一致；`list_subagent_models` 无（模型固定）。 |
| `list_subagent_models` | 无 | 有意缺席 | 我们没有 per-subagent model 选择（部署单一模型）；若将来要多模型，应在 SubagentSpec 加 Model 并暴露工具。 |
| `send_message` / `list_agents` / `interrupt_agent` | 无 | 有意缺席 | 我们没有多 Session 注册表/消息总线（单 Worker + 子智能体调用栈）。DSH 的 main↔worker 通信在我们这里由 engine Input/Output 承担。 |

## 4. 我们独有的工具（DSH WORK_TOOLS 之外）

`report`、`present`、`ask_user_question`、`follow`、`ack`、`handback`、`thread_unrelated`、`request_status`
（runtime 领域工具）；`load_skill`（技能库）；`schedule_task`、`set_goal`（本产品扩展）；
`mcp__<server>__<tool>`（外部 MCP）；浏览器 MCP 工具（per-workspace，browser 后端接线后由 MCP 提供）；
`Task*`（plantask，见上）；`web_*`（见上）。这些是"领域/产品"能力，不是缺口。

## 5. 本轮 agent scope 实际改动（已实现 + 测试）

| 改动 | 文件 | 测试 |
| --- | --- | --- |
| `read_image` 工具（metadata-only，os.Root 约束，png/jpeg/gif/webp 识别、10MB、refusal observation、诚实说明） | `internal/agent/imageread.go`；`internal/agent/tools.go` 注册 | `TestReadImageMetadataAndRefusals` |
| `job_list` 工具（DSH 三件套补齐） | `internal/agent/jobs.go`；`internal/agent/agent.go` 注册 | `TestJobListReportsTrackedJobs` |
| `subagent` 工具名 parity（eino 默认 `agent` → `subagent`） | `internal/agent/subagent.go` | `TestSubagentToolIsNamedSubagent` |

（其余本轮及上一轮的 agent 工具见 `audit/fixes/fix-agent.md` §8.5/§8.6。）

## 6. 跨 scope 待办 / 建议优先级

1. **runtime（fix-runtime）**：prompt 工具清单补 `read_image`、`job_list`、`job_kill`
   （`runtime/worker.go:580-584` 已列 domain tools/Task*/web/AGENTS.md）。同时
   `internal/prompts/defaults.go:16-19` 的 tools 段可补一句 "plan/web/image/jobs/domain tools"（docs scope）。
2. **runtime（高，产品缺口）**：`orchestrator_read` 等价物——Worker 分页读 ticket/trigger/thread/messages。
   数据面已有（engine TicketSnapshot/TicketsOnThread/Decisions），只差只读工具 + Input 分页信封；
   无它时长 Ticket 的 Worker 只能靠 task 文本，容易重复问/漏上下文。
3. **runtime/engine**：`handback` 的粒度（thread vs DSH trigger）、`ack` 无 purpose 元数据、
   `request_status` 的可见性写进文档；main Session 无 tool loop 的 ADR 也在 design 里写明
   （原本只写了"Router 是逻辑 Router"）。
4. **平台**：`pwsh`（Windows）与 `bash` 的 `sh -c` 假设；若 1.0 承诺 Windows 可用，应加 platform shell
   或明确"Windows 需 Git Bash/WSL"。
5. **文档**：`audit/AUDIT.md`/design §5.10 的能力矩阵更新为"已实现/有意缺席"两列（本文件可作输入）。

## 7. 备注（快照差异）

- `internal/runtime/artifacts.go` 的 `ack/handback/thread_unrelated/request_status` 由 fix-runtime 实现
  （nih-runtime 确认；`rc3_test.go TestWorkerToolListHasDSHExecutorTools` 断言，worker prompt 已列出）；
  如后续 runtime 又移动这些工具，按"存在即 EQUIV"更新。
- `internal/runtime/workspacesettings.go`（browser MCP / per-workspace settings）与 `worker.go` 的
  `PlanTasks/AgentsMD/WebTools` 设置读取是 runtime 正在落地的能力；本审计按当前代码记录为已接线点。
- DSH 的 `orchestrator_read`/main tools 是"脚手架能力"而非"用户可见工具"，缺席对单 Worker 架构可接受，
  但必须在文档里说明，否则会被读成"功能缺失"。
