package viewtest

// Demo projections for the five inspection pages. Like DemoOverview they exist
// so the pages can be seen and tested before the runtime publishes real ones
// (the projector wiring is the runtime's job); the shapes here are the shapes
// the core will fill. They are never published by the window itself: a page with
// no snapshot stays honest and says so.

import (
	"time"

	. "github.com/zeokeel/super-agent/internal/view"
)

// DemoSource is a plausible set of event sources: two healthy, one scheduled,
// and one whose token expired — the failure exists so the page's actionable line
// is exercised, not so the demo looks busy.
func DemoSource(now time.Time) *SourceVM {
	return &SourceVM{
		Sources: []SourceRow{
			{
				Kind: "github", Name: "GitHub", Account: "@acme · acme/webapp", Enabled: true,
				Facts: []Fact{
					{Label: "订阅", Value: "Issue、PR、CI（notifications + 订阅）"},
					{Label: "凭据", Value: "token 有效 · 40 天后过期"},
					{Label: "配额", Value: "4,812 / 5,000"},
				},
				Cursor: "已推进 · 上次同步 12 秒前", CursorAt: now.Add(-12 * time.Second),
				LastPoll: PollResult{At: now.Add(-12 * time.Second), Outcome: PollOK,
					Detail: "拉取 12 条 · 接收 12 · 丢弃 2"},
				Triggers24h: 42, Filtered24h: 23,
			},
			{
				Kind: "feishu", Name: "飞书", Account: "研发群 · 3 个群", Enabled: true,
				Facts: []Fact{
					{Label: "订阅", Value: "事件 + 历史 + topic 轮询 + 图片"},
					{Label: "权限", Value: "读取消息 ✓ · 发评论 ✓"},
				},
				Cursor: "已推进 · 上次轮询 40 秒前", CursorAt: now.Add(-40 * time.Second),
				LastPoll: PollResult{At: now.Add(-40 * time.Second), Outcome: PollOK,
					Detail: "拉取 8 条 · 接收 8 · 丢弃 0"},
				Triggers24h: 8, Filtered24h: 9,
			},
			{
				Kind: "schedule", Name: "Schedule", Account: "内部定时器", Enabled: true,
				Facts: []Fact{
					{Label: "任务", Value: "工作日 09:00 CI 巡检 · 每天 02:00 备份"},
					{Label: "行为", Value: "到点才唤醒；休眠唤醒后立即补跑一次"},
				},
				Cursor: "随运行时启停", CursorAt: now.Add(-2 * time.Minute),
				LastPoll: PollResult{At: now.Add(-2 * time.Minute), Outcome: PollOK,
					Detail: "2 个任务已排队"},
				Triggers24h: 2, Filtered24h: 0,
			},
			{
				Kind: "dingtalk", Name: "钉钉", Account: "个人账号 · 张三", Enabled: false,
				Facts: []Fact{
					{Label: "订阅", Value: "单聊 + 群 @（2 个群）"},
					{Label: "凭据", Value: "已失效 · 3 分钟前过期"},
				},
				Cursor: "停在 3 分钟前 · 未推进", CursorAt: now.Add(-3 * time.Minute),
				LastPoll: PollResult{At: now.Add(-3 * time.Minute), Outcome: PollFailed,
					Detail: "拉取失败 · 0 条接收",
					Problem: "tenant token 过期（99991663）· 在 设置 › 来源 里重新填写 App Secret，" +
						"下一次轮询会自动恢复；已收到的 3 条消息不会丢"},
				Triggers24h: 3, Filtered24h: 3,
			},
		},
		Filtered: []FilterReason{
			{Reason: "机器人账号 / 自身回复", Count: 18},
			{Reason: "非本仓库或非订阅 Thread", Count: 9},
			{Reason: "重复通知（内容 ID 相同）", Count: 5},
			{Reason: "已归档 Thread 的后续", Count: 3},
		},
		FilteredTotal: 35,
		AccountFacts: []Fact{
			{Label: "GitHub 剩余配额", Value: "4,812 / 5,000"},
			{Label: "token 到期", Value: "40 天后"},
			{Label: "轮询间隔", Value: "12s（GitHub）· 40s（飞书）"},
			{Label: "上次全部成功", Value: "12 秒前"},
		},
		UpdatedAt: now,
	}
}

// DemoThreads is a plausible Thread inbox: subscribed work, one context-only
// group message, and one filtered schedule run. The detail of every row is
// projected too, so selecting another row needs no new publish.
func DemoThreads(now time.Time) *ThreadVM {
	at := func(d time.Duration) time.Time { return now.Add(-d) }
	threads := []ThreadRow{
		{ID: "th-42", Title: "#42 修改登录超时配置", Source: "GitHub · Issue", SourceIcon: "github",
			Who: "@lin", Workspace: "acme/webapp", Stage: "执行中", Tone: ToneInfo,
			Subscribed: true, TicketID: "42", ActivityAt: at(12 * time.Minute), EventCount: 12},
		{ID: "th-feishu-1", Title: "研发群：上线前是否要灰度？", Source: "飞书 · 群消息", SourceIcon: "feishu",
			Who: "@wang", Workspace: "acme/api", Stage: "仅上下文", Tone: ToneNeutral,
			ActivityAt: at(18 * time.Minute), EventCount: 3},
		{ID: "th-128", Title: "#128 CI 失败：TestSessionTimeout", Source: "GitHub · PR", SourceIcon: "github",
			Who: "@lin", Workspace: "acme/webapp", Stage: "已归入 #42", Tone: ToneInfo,
			Subscribed: true, TicketID: "42", ActivityAt: at(34 * time.Minute), EventCount: 5},
		{ID: "th-44", Title: "#44 auth 包测试超时", Source: "GitHub · Issue", SourceIcon: "github",
			Who: "@zhou", Workspace: "acme/api", Stage: "需要授权", Tone: ToneWarn,
			Subscribed: true, TicketID: "44", ActivityAt: at(48 * time.Minute), EventCount: 7},
		{ID: "th-45", Title: "#45 补 session 迁移脚本", Source: "GitHub · Issue", SourceIcon: "github",
			Who: "@lin", Workspace: "acme/webapp", Stage: "等待评审", Tone: ToneInfo,
			Subscribed: true, TicketID: "45", ActivityAt: at(2 * time.Hour), EventCount: 4},
		{ID: "th-sched", Title: "每日 CI 巡检", Source: "Schedule · 工作日 09:00", SourceIcon: "schedule",
			Stage: "已过滤", Tone: ToneNeutral, ActivityAt: at(4 * time.Hour), EventCount: 2},
	}

	details := map[string]*ThreadDetail{
		"th-42": {
			ID: "th-42", Title: "Issue #42 · 修改登录超时配置",
			Meta: "acme/webapp · 12 个 Trigger · 最后活动 12:40", Workspace: "acme/webapp",
			TicketID: "42", Subscribed: true,
			Events: []ThreadEvent{
				{Kind: EventContext, Title: "飞书 研发群：讨论了超时默认值", Code: "",
					Text: "仅作为上下文进入，不改变权限", At: at(6 * time.Minute)},
				{Kind: EventTrigger, Title: "PR #128 CI 失败：TestSessionTimeout", Code: "",
					Text: "acme/webapp · workflow ci", At: at(34 * time.Minute)},
				{Kind: EventDecision, Title: "归入 Thread #42（follows 现有 Ticket）",
					Code: "observed", Text: "同一分支 + 订阅命中，只增加上下文",
					Decision: "accept · acme/webapp", At: at(34 * time.Minute)},
				{Kind: EventDecision, Title: "评估：新建 Ticket #42", Code: "routed",
					Text:     "命中工作区 acme/webapp 的关键词「登录」",
					Decision: "accept · acme/webapp · 授权 explicit", At: at(4*time.Hour + 12*time.Minute)},
				{Kind: EventTrigger, Title: "Issue #42 新建：登录超时应该可配置",
					Text: "acme/webapp · 由 @lin 创建", At: at(4*time.Hour + 12*time.Minute)},
			},
			CursorNote: "游标已推进 · 12 秒前轮询",
			PolicyNote: "订阅只增加上下文，永不授权（Policy v7）",
		},
		"th-128": {
			ID: "th-128", Title: "PR #128 · 支持 session_timeout 配置",
			Meta: "acme/webapp · 5 个 Trigger · 最后活动 12:31", Workspace: "acme/webapp",
			TicketID: "42", Subscribed: true,
			Events: []ThreadEvent{
				{Kind: EventDisposition, Title: "处置：扇出为上下文", Code: "fanout",
					Text: "投递给 Ticket #42，不唤醒已结束的工单", At: at(33 * time.Minute)},
				{Kind: EventTrigger, Title: "PR #128 CI 失败：TestSessionTimeout",
					Text: "acme/webapp · workflow ci · 3 个失败用例", At: at(34 * time.Minute)},
				{Kind: EventDecision, Title: "归入 Thread #42", Code: "observed",
					Text:     "同一分支 + 已订阅 → follows，不重复建单",
					Decision: "accept · acme/webapp", At: at(34 * time.Minute)},
			},
			CursorNote: "游标已推进 · 12 秒前轮询",
			PolicyNote: "CI 状态写入上下文；只有 Report 能结束 Ticket",
		},
		"th-44": {
			ID: "th-44", Title: "Issue #44 · auth 包测试超时",
			Meta: "acme/api · 7 个 Trigger · 最后活动 11:52", Workspace: "acme/api",
			TicketID: "44", Subscribed: true,
			Events: []ThreadEvent{
				{Kind: EventDisposition, Title: "处置：报告结果", Code: "fanout",
					Text: "Report blocked → Ticket 等待授权重跑流水线", At: at(48 * time.Minute)},
				{Kind: EventTrigger, Title: "Issue #44 新建：auth 包测试超时",
					Text: "acme/api · 由 @zhou 创建", At: at(3*time.Hour + 8*time.Minute)},
				{Kind: EventDecision, Title: "评估：新建 Ticket #44", Code: "started_ticket",
					Text: "失败日志含 timeout → 建单", Decision: "accept · acme/api", At: at(3*time.Hour + 8*time.Minute)},
			},
			CursorNote: "游标已推进 · 上次同步 30 秒前",
			PolicyNote: "重跑流水线需要人的授权；Ticket 已进入需要你处理",
		},
		"th-45": {
			ID: "th-45", Title: "Issue #45 · 补 session 迁移脚本",
			Meta: "acme/webapp · 4 个 Trigger · 最后活动 10:18", Workspace: "acme/webapp",
			TicketID: "45", Subscribed: true,
			Events: []ThreadEvent{
				{Kind: EventDisposition, Title: "处置：报告就绪", Code: "fanout",
					Text: "Report ready → 等待你评审 PR", At: at(2 * time.Hour)},
				{Kind: EventTrigger, Title: "Issue #45 新建：补 session 迁移脚本",
					Text: "acme/webapp · 由 @lin 创建", At: at(5 * time.Hour)},
				{Kind: EventDecision, Title: "评估：新建 Ticket #45", Code: "started_ticket",
					Text: "标题含 migration → 建单", Decision: "accept · acme/webapp", At: at(5 * time.Hour)},
			},
			CursorNote: "游标已推进 · 上次同步 40 秒前",
			PolicyNote: "评审是人的动作，只在总览出现",
		},
		"th-feishu-1": {
			ID: "th-feishu-1", Title: "研发群 · 上线前是否要灰度？",
			Meta: "acme/api · 3 个 Trigger · 最后活动 12:40", Workspace: "acme/api",
			Subscribed: false,
			Events: []ThreadEvent{
				{Kind: EventTrigger, Title: "研发群 · @wang：上线前要不要灰度？",
					Text: "飞书 · 群消息 · 未点名 agent", At: at(18 * time.Minute)},
				{Kind: EventDecision, Title: "评估：仅作为上下文", Code: "not_addressed",
					Text:     "群消息默认 context，不建 Ticket",
					Decision: "ignore · 不回复", At: at(18 * time.Minute)},
			},
			CursorNote: "游标已推进 · 上次轮询 40 秒前",
			PolicyNote: "群消息默认只做上下文；要它动手必须点名",
		},
		"th-sched": {
			ID: "th-sched", Title: "每日 CI 巡检",
			Meta: "内部定时器 · 2 个 Trigger · 最后活动 09:00", Subscribed: false,
			Events: []ThreadEvent{
				{Kind: EventTrigger, Title: "工作日 09:00 CI 巡检", At: at(4 * time.Hour)},
				{Kind: EventDecision, Title: "评估：过滤", Code: "notification",
					Text:     "schedule 非工作日 → filtered",
					Decision: "ignore · 已丢弃", At: at(4 * time.Hour)},
			},
			CursorNote: "随运行时启停",
			PolicyNote: "被丢弃的 Trigger 仍保留在库里，可审计",
		},
	}

	return &ThreadVM{
		Threads: threads, Details: details, Selected: "th-42",
		Subscribed: 4, Evaluating: 2, New24h: 6,
		UpdatedAt: now,
	}
}

// DemoDecisions is one window of Eval: every Decision carries a checkable reason
// code and the rule that fired.
func DemoDecisions(now time.Time) *DecisionVM {
	at := func(d time.Duration) time.Time { return now.Add(-d) }
	return &DecisionVM{
		Rows: []DecisionRow{
			{ID: "d-1", Kind: "accept", KindLabel: "接受", Trigger: "#42 新建 Issue",
				Summary: "新建 Ticket #42 · 修改登录超时配置", ReasonCode: "routed",
				ReasonText: "命中工作区 acme/webapp 的关键词「登录」",
				Rule:       "Issue 含「可配置 / 默认值」→ ticket",
				Workspace:  "acme/webapp", Tone: ToneOK, At: at(4*time.Hour + 12*time.Minute)},
			{ID: "d-2", Kind: "propose", KindLabel: "需要批准", Trigger: "PR #128 CI 失败",
				Summary: "提案：等你批准后新建 Ticket", ReasonCode: "sensitive_workspace",
				ReasonText: "涉及客户数据外发，需要你批准",
				Rule:       "workspace 敏感 → propose",
				Workspace:  "acme/data", Tone: ToneWarn, At: at(6 * time.Hour)},
			{ID: "d-3", Kind: "accept", KindLabel: "接受", Trigger: "#44 auth 包测试超时",
				Summary: "新建 Ticket #44 · CI 失败：auth 包测试超时", ReasonCode: "started_ticket",
				ReasonText: "失败日志含 timeout，起新单",
				Rule:       "失败日志含 timeout → ticket",
				Workspace:  "acme/api", Tone: ToneOK, At: at(3*time.Hour + 8*time.Minute)},
			{ID: "d-4", Kind: "ignore", KindLabel: "忽略", Trigger: "每日 CI 巡检",
				Summary: "丢弃，不建 Ticket", ReasonCode: "notification",
				ReasonText: "判定为通知，不建工单",
				Rule:       "schedule 非工作日 → filtered",
				Workspace:  "acme/api", Tone: ToneNeutral, At: at(7 * time.Hour)},
			{ID: "d-5", Kind: "stop", KindLabel: "停止", Trigger: "研发群：上线前是否要灰度？",
				Summary: "仅上下文，不建 Ticket，不回复", ReasonCode: "not_addressed",
				ReasonText: "群消息未点名，只作上下文",
				Rule:       "群消息默认 context",
				Workspace:  "acme/api", Tone: ToneNeutral, At: at(8 * time.Hour)},
			{ID: "d-6", Kind: "unavailable", KindLabel: "不可用", Trigger: "文档评论：配置项命名",
				Summary: "无法受理：发送者不在允许名单", ReasonCode: "not_a_member",
				ReasonText: "发送者不在允许名单",
				Rule:       "workspace 白名单",
				Workspace:  "acme/api", Tone: ToneDanger, At: at(9 * time.Hour)},
		},
		Kinds: []DecisionKindCount{
			{Kind: "accept", Label: "接受", Count: 2},
			{Kind: "propose", Label: "需要批准", Count: 1},
			{Kind: "ignore", Label: "忽略", Count: 1},
			{Kind: "stop", Label: "停止", Count: 1},
			{Kind: "unavailable", Label: "不可用", Count: 1},
		},
		Counts: EvalCounts{Evaluated: 24, Decisions: 9, Filtered: 5, Tickets: 4},
		Rules: []RuleHit{
			{Rule: "Issue 含「可配置 / 默认值」→ ticket", Hits: 12},
			{Rule: "PR 同分支 + 已订阅 → follows", Hits: 8},
			{Rule: "失败日志含 timeout → ticket", Hits: 3},
			{Rule: "schedule 非工作日 → filtered", Hits: 6},
			{Rule: "群消息默认 context → 不建 Ticket", Hits: 24},
		},
		Golden: []GoldenStat{
			{Name: "阶段函数用例", Passed: 138, Total: 138},
			{Name: "Decision golden", Passed: 64, Total: 64},
			{Name: "admission 用例", Passed: 31, Total: 31},
		},
		PolicyVersion: "v7",
		UpdatedAt:     now,
	}
}

// DemoPrompts is the prompt editor with three layers: a Router prompt the
// operator customized, a Worker prompt still at its built-in default, and the
// eval rules note. The revisions and the two different layers exist so the page's
// default chip, dirty bar and optimistic-concurrency path are all exercised.
func DemoPrompts(now time.Time) *PromptVM {
	const routerDefault = `你是 ZeoKeel 的研判器（Router 的一部分）。你只做判断，不执行任务、不回复任何人。

对给定的一条消息，你要回答：这算不算一个工作请求？如果算，该派给哪个工作区？

规则：
1. 只依据给定的候选工作区与它们的简报判断，不要在候选之外发明工作区。
2. 通知、闲聊、与你无关的讨论 → ignore，并给出 reason_code。
3. 拿不准该派给哪个工作区 → ask，写一个具体的问题，不要猜。
4. 只有真的是一次明确的工作请求，才 accept。
5. accept 时必须给出：目标工作区、强度（explicit/inferred）、命中的规则、
   intent_evidence 与 worktree_name。`

	const workerDefault = `你是 ZeoKeel 的执行者。你只做被派给你的那一个工作区里的那一个目标。

你可以使用的工具只有 bash、read、write、edit、report、ack；不要尝试越过白名单。
每一步都要留下可检查的结果：命令的退出码、改动的文件、测试的运行结果。
做完写 report；受阻写 report 并说明卡在哪里，不要自己扩大范围。`

	const evalRulesDefault = `评估规则是代码 + 表格，不是提示词：每条规则有 id、是否启用、一句 instruction。
Decision 只存 reason 码与命中规则，渲染时套模板；改动规则必须同时改 golden 用例。
这里是一段给人看的说明，可以用来说明某个工作区的例外，但不会被当成规则解析。`

	router := routerDefault + "\n\n（本工作区补充：涉及客户数据的请求一律 proposal，不直接受理。）"

	return &PromptVM{
		Source:    "mygo.json",
		Revision:  7,
		UpdatedAt: now,
		Layers: []PromptLayer{
			{ID: PromptRouter, Label: "Router 提示词",
				Note:    "研判者：只回答接不接、派给谁；它不执行、不回复任何人。",
				Content: router, Default: routerDefault, Revision: 7, Editable: true},
			{ID: PromptWorker, Label: "Worker 提示词",
				Note:    "执行者：工具白名单、交付与验证；与 Router 的提示词完全分开。",
				Content: workerDefault, Default: workerDefault, Revision: 7, Editable: true},
			{ID: PromptEvalRules, Label: "评估规则说明",
				Note:    "规则本身是代码 + 表格；这段文字只解释给人看，不会被当成规则解析。",
				Content: evalRulesDefault, Default: evalRulesDefault, Revision: 7, Editable: true},
		},
	}
}

// DemoSchedules is the 定时 section: one healthy daily job, one skipped because
// the previous fire still ran, and one switched off — the three states the panel
// must tell apart.
func DemoSchedules(now time.Time) *ScheduleVM {
	return &ScheduleVM{
		UpdatedAt: now,
		Rows: []ScheduleRow{
			{ID: "sch-daily-ci", Task: "每天 09:00 跑一次 CI 巡检", Workspace: "acme/webapp",
				Spec: "每天 09:00", Enabled: true,
				NextAt: now.Add(2*time.Hour + 14*time.Minute), LastFiredAt: now.Add(-22 * time.Hour),
				LastStatus: "fired"},
			{ID: "sch-usage", Task: "每 2 小时汇总 acme 用量", Workspace: "acme/data",
				Spec: "每 2 小时", Enabled: true,
				NextAt: now.Add(40 * time.Minute), LastFiredAt: now.Add(-80 * time.Minute),
				LastStatus: "skipped", LastSkipReason: "上一个定时还在执行，本次跳过"},
			{ID: "sch-backup", Task: "每天 02:00 备份状态库", Spec: "每天 02:00",
				Enabled: false, LastFiredAt: now.Add(-6 * time.Hour), LastStatus: "fired"},
		},
	}
}

// DemoGoals is the objective list: three active goals (so 总览's "最多 2 条" cap is
// exercised), one paused and one completed. Order is most recently updated first,
// as the core lists them.
func DemoGoals(now time.Time) *GoalVM {
	return &GoalVM{
		UpdatedAt: now,
		Rows: []GoalRow{
			{ID: "goal-login-timeout", Workspace: "acme/webapp",
				Objective: "把登录超时配置化并合入主干", State: GoalActive,
				Rounds: 2, MaxRounds: 12, Revision: 4, UpdatedAt: now.Add(-5 * time.Minute)},
			{ID: "goal-auth-coverage", Workspace: "acme/api",
				Objective: "补齐 auth 包的测试覆盖到 80%", State: GoalActive,
				Rounds: 0, MaxRounds: 5, Revision: 2, UpdatedAt: now.Add(-20 * time.Minute)},
			{ID: "goal-mcp-research", Workspace: "acme/webapp",
				Objective: "调研 MCP 工具接入的可行方案", State: GoalActive,
				Rounds: 1, MaxRounds: 3, Revision: 1, UpdatedAt: now.Add(-45 * time.Minute)},
			{ID: "goal-usage-report", Workspace: "acme/data",
				Objective: "把 acme 用量报表迁移到新模板", State: GoalPaused,
				Rounds: 3, MaxRounds: 8, Revision: 5, UpdatedAt: now.Add(-2 * time.Hour)},
			{ID: "goal-cleanup", Workspace: "acme/api",
				Objective: "清理 2026-10 的过期通知", State: GoalCompleted,
				Rounds: 4, MaxRounds: 4, Revision: 6, UpdatedAt: now.Add(-26 * time.Hour)},
		},
	}
}

// DemoTickets is the execution list: all Tickets, sorted by recent activity.
func DemoTickets(now time.Time) *TicketListVM {
	rows := []TicketRow{
		{ID: "tk-47", Number: 47, Title: "更新 OpenAPI 描述", Workspace: "acme/api", WorkspaceID: "ws_api", Source: "本地面板",
			SourceIcon: "local", Stage: "待开工", Bucket: BucketOpen, StageID: StageWorkingQueued,
			Detail: "已建单，尚未派给执行器", Activity: "刚刚 · 已受理",
			Tone: ToneNeutral, Age: 0},
		{ID: "tk-46", Number: 46, Title: "整理 acme 上月用量摘要", Workspace: "acme/data", WorkspaceID: "ws_data", Source: "本地面板",
			SourceIcon: "local", Stage: "排队中", Bucket: BucketPending, StageID: StageWorkingQueued,
			Detail: "工作区预算内，等待执行器", Activity: "1 分钟前 · 已排队",
			Tone: ToneNeutral, Age: time.Minute},
		{ID: "tk-49", Number: 49, Title: "生成季度用量趋势图", Workspace: "acme/data", WorkspaceID: "ws_data", Source: "钉钉 · 张三",
			SourceIcon: "dingtalk", Stage: "执行中", Bucket: BucketWorking, StageID: StageWorking,
			Detail: "bash 正在取数 · 已跑 2 个查询", Activity: "5 分前 · bash",
			Tone: ToneInfo, Age: 5 * time.Minute},
		{ID: "tk-42", Number: 42, Title: "修改登录超时配置", Workspace: "acme/webapp", WorkspaceID: "ws_webapp", Source: "GitHub · Issue #42",
			SourceIcon: "github", Stage: "执行中", Bucket: BucketWorking, StageID: StageWorking,
			Detail: "step 7 · 正在补 SessionTimeout 默认值", Activity: "12 分前 · edit 工具调用",
			Tone: ToneInfo, Age: 12 * time.Minute},
		{ID: "tk-50", Number: 50, Title: "修复账单页空状态", Workspace: "acme/webapp", WorkspaceID: "ws_webapp", Source: "飞书 · 研发群",
			SourceIcon: "feishu", Stage: "等待你回答", Bucket: BucketWaiting, StageID: StagePersonAnswer,
			Detail: "配置项名用 session_timeout 还是 session.timeout？", Activity: "20 分前 · 问你",
			Tone: ToneWarn, Age: 20 * time.Minute},
		{ID: "tk-44", Number: 44, Title: "CI 失败：auth 包测试超时", Workspace: "acme/api", WorkspaceID: "ws_api", Source: "GitHub · Issue #44",
			SourceIcon: "github", Stage: "受阻", Bucket: BucketWaiting, StageID: StagePersonBlocked,
			Detail: "等待授权重跑流水线", Activity: "34 分前 · 报告 blocked",
			Tone: ToneDanger, Age: 34 * time.Minute},
		{ID: "tk-45", Number: 45, Title: "为 session 配置补迁移脚本", Workspace: "acme/webapp", WorkspaceID: "ws_webapp", Source: "GitHub · Issue #45",
			SourceIcon: "github", Stage: "等待评审", Bucket: BucketWaiting, StageID: StagePersonReview,
			Detail: "PR 已就绪，等待你 Review", Activity: "42 分前 · 报告 ready",
			Tone: ToneInfo, Age: 42 * time.Minute},
		{ID: "tk-48", Number: 48, Title: "把登录超时改成可配置", Workspace: "acme/webapp", WorkspaceID: "ws_webapp", Source: "本地面板",
			SourceIcon: "local", Stage: "已结束", Bucket: BucketEnded, StageID: StageEnded,
			Detail: "已合并 · PR #127", Activity: "2 小时前 · 已合并",
			Tone: ToneNeutral, Age: 2 * time.Hour},
	}

	details := map[string]TicketDetail{
		"tk-42": {ID: "tk-42", Workspace: "acme/webapp", Stage: "执行中",
			Detail: "step 7 · 正在补 SessionTimeout 默认值", Source: "GitHub · Issue #42",
			Thread: "th-42", Mandate: "explicit · policy v7 · 由 @lin 的请求授权",
			Artifacts: []Artifact{
				{Kind: "pr", Title: "PR #128 草案", Detail: "+128 −36 · 9 files"},
			}, LastActivity: "12 分前 · edit internal/auth/session.go"},
		"tk-46": {ID: "tk-46", Workspace: "acme/data", Stage: "排队中",
			Detail: "工作区预算内，等待执行器", Source: "本地面板 · operator",
			Thread: "local", Mandate: "explicit · policy v7 · 由你派发",
			LastActivity: "1 分钟前 · 已排队"},
		"tk-49": {ID: "tk-49", Workspace: "acme/data", Stage: "执行中",
			Detail: "bash 正在取数 · 已跑 2 个查询", Source: "钉钉 · 张三",
			Thread: "dm:ding_zhangsan", Mandate: "inferred · policy v7 · 由工作区关键词路由",
			LastActivity: "5 分前 · bash ./scripts/query_usage.sh"},
		"tk-44": {ID: "tk-44", Workspace: "acme/api", Stage: "受阻",
			Detail: "等待授权重跑流水线", Source: "GitHub · Issue #44",
			Thread: "th-44", Mandate: "explicit · policy v7 · 由 @zhou 的请求授权",
			LastActivity: "34 分前 · 报告 blocked：流水线需要授权重跑"},
		"tk-50": {ID: "tk-50", Workspace: "acme/webapp", Stage: "等待你回答",
			Detail: "配置项名用 session_timeout 还是 session.timeout？", Source: "飞书 · 研发群",
			Thread: "th-feishu-1", Mandate: "inferred · policy v7 · 群消息点名后受理",
			LastActivity: "20 分前 · ask_user_question"},
		"tk-45": {ID: "tk-45", Workspace: "acme/webapp", Stage: "等待评审",
			Detail: "PR 已就绪，等待你 Review", Source: "GitHub · Issue #45",
			Thread: "th-45", Mandate: "explicit · policy v7 · 由 @lin 的请求授权",
			Artifacts:    []Artifact{{Kind: "pr", Title: "PR #129", Detail: "+64 −12 · 4 files"}},
			LastActivity: "42 分前 · 报告 ready"},
		"tk-47": {ID: "tk-47", Workspace: "acme/api", Stage: "待开工",
			Detail: "已建单，尚未派给执行器", Source: "本地面板 · operator",
			Thread: "local", Mandate: "explicit · policy v7 · 由你派发",
			LastActivity: "刚刚 · 已受理"},
		"tk-48": {ID: "tk-48", Workspace: "acme/webapp", Stage: "已结束",
			Detail: "已合并 · PR #127", Source: "本地面板 · operator",
			Thread: "local", Mandate: "explicit · policy v7 · 由你派发",
			Artifacts:    []Artifact{{Kind: "pr", Title: "PR #127", Detail: "+41 −8 · 3 files"}},
			LastActivity: "2 小时前 · 已合并"},
	}

	buckets := make([]BucketCount, 0, 5)
	for _, b := range BucketOrder() {
		buckets = append(buckets, BucketCount{Bucket: b, Label: b.Label()})
	}
	for _, r := range rows {
		for i := range buckets {
			if buckets[i].Bucket == r.Bucket {
				buckets[i].Count++
			}
		}
	}
	return &TicketListVM{Rows: rows, Buckets: buckets, Details: details, Total: len(rows), UpdatedAt: now}
}

// DemoSettings shows the grouped settings page: 来源 and 执行 are real forms,
// the other groups are bones with their facts, so the page never hides what is
// still missing.
func DemoSettings(now time.Time) *SettingsVM {
	return &SettingsVM{
		Groups: []SettingsGroup{
			{ID: SettingsWorker, Label: "模型",
				Note: "研判与执行都用这个 OpenAI 兼容端点。",
				Fields: []SettingField{
					{Key: "base_url", Label: "端点 base URL", Value: "https://api.deepseek.com/v1", Kind: SettingText},
					{Key: "api_key", Label: "API Key", Value: "", Kind: SettingSecret},
					{Key: "model", Label: "模型名", Value: "deepseek-chat", Kind: SettingText},
				}},
			{
				ID: SettingsSource, Label: "来源",
				Note: "每个 Source 的账号与轮询；凭据只写不读。",
				Fields: []SettingField{
					{Key: "poll_interval", Label: "轮询间隔（秒）", Value: "12", Kind: SettingNumber,
						Help: "GitHub 通知的最小轮询间隔；飞书按 40 秒轮询。"},
					{Key: "feishu_enabled", Label: "飞书：启用", Value: "1", Kind: SettingSwitch,
						Help: "启用并填好凭据后飞书通道才会监听。"},
					{Key: "feishu_transport", Label: "飞书：传输方式", Value: "ws",
						Kind: SettingSelect, Options: []string{"ws", "longpoll"}},
					{Key: "feishu_app_id", Label: "飞书 App ID", Value: "cli_a1b2c3", Kind: SettingText},
					{Key: "feishu_app_secret", Label: "飞书 App Secret", Value: "••••••••", Kind: SettingSecret},
					{Key: "feishu_chats", Label: "飞书会话 ID（逗号分隔）", Value: "oc_1, oc_2", Kind: SettingText},
					{Key: "dingtalk_enabled", Label: "钉钉：启用", Value: "0", Kind: SettingSwitch},
					{Key: "dingtalk_transport", Label: "钉钉：传输方式", Value: "dws",
						Kind: SettingSelect, Options: []string{"dws", "stream"}},
					{Key: "dingtalk_pause_listen_on_send", Label: "钉钉：发送时暂停监听", Value: "0",
						Kind: SettingSwitch, Help: "防止机器人读到自己刚发的消息"},
					{Key: "dingtalk_app_key", Label: "钉钉 AppKey", Value: "ding_key", Kind: SettingText},
					{Key: "dingtalk_app_secret", Label: "钉钉 AppSecret", Value: "••••••••", Kind: SettingSecret},
					{Key: "dingtalk_robot", Label: "钉钉机器人 RobotCode", Value: "ding_robot", Kind: SettingText},
					{Key: "im_self", Label: "机器人自己的账号（@ 判定）", Value: "bot_zeokeel", Kind: SettingText},
					{Key: "github_token", Label: "GitHub Token", Value: "ghp_••••••••7f3a", Kind: SettingSecret,
						Help: "存于系统钥匙串，改完下一次轮询生效。"},
					{Key: "feishu_groups", Label: "飞书订阅群", Value: "研发群, 产品群, 运维群", Kind: SettingText,
						Help: "逗号分隔；只读已加入的群。"},
					{Key: "drop_bot_messages", Label: "丢弃机器人消息", Value: "1", Kind: SettingSwitch,
						Help: "机器人账号与自身回复不进入评估。"},
					{Key: "archived_followups", Label: "接收已归档 Thread 的后续", Value: "0", Kind: SettingSwitch,
						Help: "关闭时，归档线程的后续直接丢弃并记录原因。"},
				},
			},
			{
				ID: SettingsEval, Label: "评估",
				Note:        "规则是代码 + 表格，不是提示词；改动走 diff 评审。",
				Placeholder: "本组表单随 M2 的 Policy 编辑器一起出现；当前规则由代码与 golden 用例约束。",
				Fields: []SettingField{
					{Key: "policy_version", Label: "Policy 版本", Value: "v7", Kind: SettingFact},
					{Key: "accept_mode", Label: "受理档位", Value: "先提案，再由你批准", Kind: SettingFact},
				},
			},
			{
				ID: SettingsExecution, Label: "执行",
				Note: "执行器、预算与沙箱；改完只影响之后新开的 Ticket。",
				Fields: []SettingField{
					{Key: "concurrency", Label: "并发执行器", Value: "2", Kind: SettingNumber,
						Help: "同一时刻最多几个 Worker 在跑；超出的排队。"},
					{Key: "budget_usd", Label: "每 Ticket 预算上限（美元）", Value: "2.00", Kind: SettingNumber,
						Help: "超出即暂停并问你。"},
					{Key: "command_timeout", Label: "命令超时（秒）", Value: "600", Kind: SettingNumber,
						Help: "单条 bash 命令的上限，超时记失败。"},
					{Key: "auto_continue", Label: "自动继续上限（轮）", Value: "20", Kind: SettingNumber,
						Help: "goal 循环的上限；首阶段只观察。"},
					{Key: "sandbox_readonly", Label: "沙箱只读工作区", Value: "0", Kind: SettingSwitch,
						Help: "打开后 Worker 只能读，不能写文件。"},
				},
			},
			{
				ID: SettingsPrompt, Label: "提示词",
				Note: "Router 与 Worker 的提示词分开，改动走 diff 评审；这里只写人读的覆盖与说明。",
				Fields: []SettingField{
					{Key: "prompt_version", Label: "提示词版本", Value: "2026-11-03", Kind: SettingFact},
					{Key: "compact_budget", Label: "压缩触发预算", Value: "80% 上下文窗口", Kind: SettingFact},
				},
			},
			{
				ID: SettingsDeploy, Label: "部署",
				Note:        "数据目录、备份与更新通道。",
				Placeholder: "本组表单随后续里程碑出现；当前部署参数由启动命令与环境变量决定。",
				Fields: []SettingField{
					{Key: "state_dir", Label: "数据目录", Value: "~/.local/share/zeokeel", Kind: SettingFact},
					{Key: "backup", Label: "备份", Value: "每天 02:00 · 保留 7 天", Kind: SettingFact},
				},
			},
		},
		Source:    "mygo.json",
		Revision:  7,
		UpdatedAt: now,
	}
}

// DemoChat is the 超级Agent conversation: a question, an answer, the dispatched
// outcome, and a question about the workspaces. It is what the panel shows on 总览
// and above the composer.
func DemoChat(now time.Time) *ChatVM {
	return &ChatVM{
		Messages: []ChatMessage{
			{Role: ChatUser, Text: "登录超时能改成可配置吗？", At: now.Add(-3 * time.Minute)},
			{Role: ChatAssistant, Kind: ChatAnswer, At: now.Add(-2 * time.Minute),
				Text: "可以。我把它派给 acme/webapp 了：超时做成配置项，默认 30 分钟，等执行器跑完我把结果发回这里。"},
			{Role: ChatSystem, Kind: ChatOutcome, At: now.Add(-2 * time.Minute),
				Text: "已派发 #42 · acme/webapp · 等待执行"},
			{Role: ChatUser, Text: "这里有哪些工作区？", At: now.Add(-time.Minute)},
			{Role: ChatAssistant, Kind: ChatAnswer, At: now.Add(-40 * time.Second),
				Text: "目前有 3 个工作区：acme/webapp（前端与登录流程）、acme/api（后端 API）、acme/data（用量数据）。想让我在哪个工作区里做事？"},
		},
		UpdatedAt: now,
	}
}

// DemoPlan is a running task's plan: a done step, a running one and a blocked one,
// so the panel's status icons all appear.
func DemoPlan(now time.Time) *PlanVM {
	return &PlanVM{
		TicketID: "tk-42",
		Items: []PlanItem{
			{ID: "p1", Title: "读 auth 的 session 代码", Status: PlanDone, Detail: "3 个文件"},
			{ID: "p2", Title: "补 SessionTimeout 默认值", Status: PlanRunning, Detail: "step 7"},
			{ID: "p3", Title: "跑 auth 包测试", Status: PlanPending},
			{ID: "p4", Title: "更新配置文档", Status: PlanBlocked, Detail: "等待你确认配置项名"},
		},
		UpdatedAt: now,
	}
}
