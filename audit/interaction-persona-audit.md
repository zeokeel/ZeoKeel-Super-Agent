# Interaction persona audit — non-technical first-time user (read-only)
Basis: README.md, UI strings in internal/ui + internal/view, and /tmp/r2-*, r3-*, r4-* (3 r4 files: execution-filter, overview-chat, workspace-actions). r2/r3 shots predate the final build, and the r4 capture runs a demo runtime; “(code)” = re-verified in current source. No code modified.
Persona: knows Slack, ChatGPT and a file picker; does not know Ticket, Trigger, Eval, DWS, transport, profile, or workspace root.

### 1. First open — Goal: install and be ready to use
- Saw: “初次设置” (“n / 4 已完成”): 用哪个模型 (端点/模型/API Key) → 它管哪个仓库 (名字/本地路径/一句话说明/关键词) → 从哪里接活 → 谁可以让它干活; one “保存这一步” per step; “开始使用” only when model+name are filled.
- Expected: sign in or “try it without an account”, pick a folder, start.
- Stuck: no idea what 端点 is or where an API Key comes from; no skip/demo path in the wizard; 本地路径 has no 浏览… (新建工作区 does) and may be left blank — it silently becomes `~/<name>` (code); the greyed “开始使用” says only “还有必填项没完成” with no list; the macOS package is unsigned (README only, not the app); after setup there is no model setting anywhere (code), and the no-model failure tells me “在设置 › 提示词旁边的模型配置…” — no such control exists (code).
- Steps: ~14. Alone: No — needs a colleague for endpoint/key (and for the macOS “unidentified developer” dialog).
- Quote: “It wants an ‘endpoint’ and an ‘API Key’ before I can even look around. I have neither, there’s no skip, and ‘开始使用’ just stays grey.”

### 2. Ask about my projects — Goal: a question, a usable answer
- Saw: 总览 with one bar “说点什么，或派一件事…”, a 超级Agent conversation card, and the latest answer as a one-line strip above the input on every page.
- Expected: a chat thread where my question stays and I can scroll back.
- Stuck: the panel keeps only the last 8 lines and there is no chat-history page; “消息” replays my words with a *different, canned* answer (“这次没有派发工单…” from localAnswer) while the strip shows the model’s answer (code); “记录 / 结果 / 说明” labels are unexplained; no “new chat”.
- Steps: 3 to ask; 6+ to find it again. Alone: asks yes; finding it again no.
- Quote: “I asked a question and got an answer, but it floats in a strip above the text box. When I came back it was gone, and the ‘消息’ page shows a different answer to the same question.”

### 3. Real task in my folder — Goal: tidy files in ~/Documents/Reports
- Saw: must create a workspace first (名称 / 根目录 + 浏览… / 用途 / 类型 仓库·数据·文档·混合), then type in the global input; stages 待开工／排队中／执行中; “已派发 #N”; finally “已结束”.
- Expected: “do it in this folder” → files change in that folder → “done, here are the files”.
- Stuck: 类型 chips mean nothing; selecting a workspace does not steer the global input (Dispatch(text) has no workspace, code) so the router may answer “这次没有派发工单”; work actually runs in `<stateDir>/work/ticket-N` while the prompt insists “工作目录就是它的根目录” (code), so my folder is untouched and results live in a hidden directory; no path is ever shown.
- Steps: ~10. Alone: No.
- Quote: “I told it to tidy the reports in my Documents folder. It said ‘已派发 #44’ and later ‘已结束’, but my folder is untouched — where did it put the files?”

### 4. Find the result and the conversation later — Goal: open the report; reread the exchange
- Saw: 总览 “最近交付物” rows with a “›” each and a “全部” link; the 交付物 modal lists icon/title/size/time; 执行 rows expand to a detail whose 交付物 are text chips (“报告.md · 12 KB”).
- Expected: click the result → it opens or reveals in Finder; my conversation is retrievable.
- Stuck: the “›” has no click handler (code) and neither do the modal rows; the overview projection drops Path/URL entirely (code); no full path, no “open folder”, no copy path; the chat is not in 搜索 (scopes: 工单/消息/决策/工作区/来源) and 消息 shows the canned answer.
- Steps: ~6. Alone: No.
- Quote: “There’s an arrow next to the file, so I clicked it — nothing. I clicked ‘全部’ and clicked the row — still nothing. I can see the file exists but I can’t open it or find where it is.”

### 5. Approve a Feishu/DingTalk message — Goal: say yes once, know it started
- Saw: top-right “3 待处理”; 总览 “待你批准 · 批准后才会开工” cards (“张三 找你 / 他说：… / 要做：… / 产出：…” + 批准/拒绝); 来源 shows “待审批 3 条” as a read-only fact.
- Expected: a notification that opens the message; approve → “已批准，已派发 #45”.
- Stuck: one thing is called 待处理 / 待你批准 / 待审批 on three surfaces; the top-right count is a chip, not a link (code); 来源’s “待审批” has no buttons; after 批准 the row vanishes with no confirmation and no ticket number; a failure is written to the composer line, which is suppressed whenever the chat has any answer (code).
- Steps: 4–5. Alone: Yes, if I happen to be on 总览 — but I don’t trust the result.
- Quote: “I clicked ‘批准’. The row disappeared. Did it start? Did it fail? There’s no ‘approved’ message anywhere — I only know it’s gone.”

### 6. Connect company Feishu/DingTalk from zero — Goal: messages arrive in the app
- Saw: 设置›来源: 飞书 card (启用, 传输方式, App ID, App Secret, 飞书会话 ID（逗号分隔）, 检查通道) and 钉钉 card (DWS 长连接 / 官方 Stream, AppKey, AppSecret, RobotCode), then an ungrouped list: DWS 可执行文件 / DWS 登录 profile / DWS 监听范围 / DWS 群过滤 / 兼容旧配置 / GitHub 仓库…
- Expected: “连接飞书” → sign in to Feishu → done.
- Stuck: I must create a developer app on another website, with no steps or link (only after a 401 does an 打开授权页 button appear); “会话 ID（oc_…）” is unknowable from the app; 传输方式 is a chip pair (官方长连接 / HTTP 长轮询) whose difference is buried in 10px help, and the capture shows a lone “长轮询” chip that reads as a label, not a choice; onboarding silently pins 长轮询 though the app recommends 官方长连接 (code); default 钉钉 = DWS needs an external `dws` CLI logged in (help text only), yet its card asks for Stream bot credentials and its CLI fields sit outside the card; 检查通道 tests the *saved* config, so checking after typing tests the old value; there is no 保存 button until I edit, and it sits under the whole long form.
- Steps: 15+. Alone: No — needs a Feishu/DingTalk admin, a developer, and a terminal.
- Quote: “The DingTalk box asks for AppKey and RobotCode, but the small print says I need something called ‘dws’ logged in on the command line. Which one am I supposed to do? And there’s no save button until I scroll past everything.”

### 7. Stop a running task / retry a failed one — Goal: kill a runaway; rerun a failure
- Saw: 执行 rows with stages, header “点一行就地展开 · 动作在总览”; the expanded row has no buttons; 总览 “需要我处理” has 回答/重试/停止 only for rows waiting on me; an r4 demo runtime shows “动作未接线：运行时未提供 回答 (AttentionAnswerer)、停止 (TicketStopper)、重试 (TicketRetrier)” above those very buttons.
- Expected: a stop control on the running task, a retry that says what happened.
- Stuck: a working ticket is not a person stage (code), so it never appears in 需要我处理, and 执行 has no actions → a running task cannot be stopped at all; 重试 has no “重试中…/已重试”, no confirm, and its error goes to a band/composer away from the click; “已结束” rows have no actions.
- Steps: 5+, ends in a dead end. Alone: No for stop; Probably for retry.
- Quote: “My task has been running 20 minutes and I want it to stop. The task page says ‘动作在总览’, but the overview only shows things waiting for me — my running task isn’t there. So there’s no stop button at all.”

### 8. Rename/delete a workspace, recover — Goal: fix a wrong name; remove a dead project
- Saw: select a workspace → 重命名 / 删除 on its card; 重命名 dialog; 删除 dialog: “会从面板移除这个工作区及其会话记录；磁盘上的目录不会被删除。”
- Expected: rename inline; delete warns “this has 3 tasks — archive or keep?”, with undo.
- Stuck: rename has no “已改名” and no undo (retype the old name); delete is refused for any used workspace with `工作区 "ws_ab12…" 还有 3 个工单，不能删除` (opaque id + 工单) even though the dialog promised to remove conversations; tickets cannot be deleted anywhere, so a used workspace can never be deleted; a successful delete has no undo/trash; similar names differ only in the tree.
- Steps: 5–6. Alone: rename yes; delete no; recovery no.
- Quote: “I renamed the wrong project and there’s no undo — I typed the old name back and hoped. Then I tried to delete a project I don’t use and it said ‘ws_7f3a…’ has 3 ‘工单’; I don’t know what a 工单 is or where to delete one.”

## 10 most confusing things (ranked)
1. **P0 — Setup dead-ends on a model endpoint/key with no help, no skip, and no way back in.** The wizard asks for 端点/模型/API Key and only enables “开始使用” once 模型+工作区名字 exist; the key is silently optional, but nothing works without it. After setup there is no 模型 setting, and the “未配置模型” report points to a nonexistent “设置 › 提示词旁边的模型配置” (code). A non-technical user cannot start, and cannot fix a wrong key afterwards.
2. **P0 — A running task cannot be stopped.** 执行 has no actions and 总览 only lists person-stage rows; ActionsFor includes 停止 for running rows but nothing renders it (code). Runaway work/cost has no brake, which is exactly when a novice panics.
3. **P0 — “Do it in my folder” is false by construction.** The worker’s cwd is `<stateDir>/work/ticket-N`; the prompt says “工作目录就是它的根目录” (code); artifacts are scanned from that ticket dir and the panel never shows a path (the overview drops Path/URL). The agent reports success while the user’s folder never changes.
4. **P1 — One thing, five names.** 会话 (workspace page), 工单 (dialogs/errors), 任务 (执行), 线程 (触发), Ticket (README); plus 待处理/待你批准/待审批 for two different things. A novice cannot tell whether “2 个会话” and “共 8 个任务” are the same objects.
5. **P1 — Selecting a workspace does not decide where my message goes.** The composer always calls Dispatch(text); routing is keyword-based, so a correct-looking selection can still answer “这次没有派发工单” (code). The README says the opposite (“先选中工作区再用输入框”), so the failure looks like my mistake.
6. **P1 — The consequential clicks are silent.** 批准/重试/停止 have no pending or success state; 批准 rows just vanish. Errors go to `composerErr`/`actionErr` in a distant band, and composerErr is hidden whenever the chat has a last answer (code). I cannot tell “worked” from “ignored”.
7. **P1 — Dialogs show internal IDs instead of the number I clicked.** “回答 #<32-hex>”, “停止 #<32-hex>？”, and the delete error names the workspace `ws_…` (code). After clicking “#42”, the modal says something else — I hesitate on an irreversible action.
8. **P1 — Results are visible but not openable.** Rows draw a “›” chevron and 全部 opens a list, but nothing is clickable and no path exists (code). Finding the file is impossible without leaving the app and guessing; the pointer only adds false affordance.
9. **P1 — “Connect Feishu/DingTalk” assumes I am a developer.** Feishu needs an app in the Feishu console and an `oc_…` chat id; DingTalk’s default DWS mode needs a CLI login but its card asks for Stream bot keys; 检查通道 tests the saved config, not what I just typed; 保存 is below the fold. The one task an office user must self-serve is the least self-servable.
10. **P2 — Monitoring pages speak engineer.** 触发 “订阅只增加上下文，永不授权（Policy v7）”, 评估 “Decision golden 64/64 / admission 用例 / reason 码”, 来源 “游标已推进 · 12 秒前轮询”, health “MCP notion 未连接 / WAL 正常”, and raw internals like “动作未接线…AttentionAnswerer”. I stop reading the app’s own explanations.

## Five “I would expect this to work differently” changes
1. **A setup that proves itself.** One screen: pick a provider or paste a key, then “发送测试消息” must show a real reply before “开始使用”; offer “先用内置演示模型” so nobody is locked out; keep a permanent 模型 entry in 设置 and never print a pointer to a missing control.
2. **A visible work folder.** Say “将在 /Users/me/Documents/Reports 里工作” on the dispatch confirmation, actually write there, and make every 交付物 row open the file / reveal in Finder with its full path (copy path available).
3. **One object, one word, one place.** Call it 任务 everywhere; 消息 becomes 对话 with the real model answers, scrollback and search; move 线程/触发/评估/游标 under “诊断” and stop putting them in the primary rail.
4. **Actions live on the object.** 停止/重试/回答 on the task row itself (执行 and 总览), with live “停止中…/已停止/重试已排队” and the reason inline; 批准 starts from the “待处理” chip, which becomes a real link.
5. **Projects, not workspaces.** “＋ 添加项目” → native folder picker, name auto-filled; the selection scopes everything (composer included) and is shown as a “在 <project> 中” chip; delete = archive with undo, never a refusal naming `ws_…`.

## Missing feedback / affordance that made me distrust it
- “动作未接线：运行时未提供 回答 (AttentionAnswerer)、停止 (TicketStopper)、重试 (TicketRetrier)” rendered in-product while the buttons stay visible — the app admits its controls may be fake.
- “3 待处理” in the top bar looks like a button but is a static chip; clicking it does nothing (code).
- 最近交付物 rows show “›” and do nothing; the 交付物 modal rows also do nothing (code).
- 批准/重试/停止: no “处理中”, no “已完成”; the only failure channel is far from the click and often suppressed.
- 设置: no 保存 until you edit; after editing, the bar sits under a 20-field form; 检查通道 checks the saved value, so “type → check” silently tests the old credentials.
- 删除工作区 promises to remove “会话记录”, then refuses with an opaque `ws_…` id and “工单”; no trash/undo after a successful delete.
- 重命名 closes silently: no “已改名为…”, no undo.
- Health draws a to-do as healthy: “待审批 3 条” gets a green OK dot (code); “执行器 · 未配置模型，工单会挂起” is one more jargon line instead of a way to fix it.
