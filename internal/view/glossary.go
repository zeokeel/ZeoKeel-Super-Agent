package view

// The domain glossary: the words the panel and the prompts use, in Chinese and
// English, as _ref/docs/001-domain.md §Glossary defines them. The panel's (?)
// entry shows this list; the agents' messages to people use plain language, not
// these terms, so the glossary is for the operator reading the panel.

// GlossaryEntry is one term.
type GlossaryEntry struct {
	// Term is the English term, as the prototype's docs use it.
	Term string
	// Chinese is the term the panel and the prompts use.
	Chinese string
	// Meaning is one line a person can read without the rest of the docs.
	Meaning string
}

// GlossaryValueGroup is the closed set of values one field takes, which the
// glossary shows under the terms: a person meeting "blocked" in a report needs
// the other states beside it.
type GlossaryValueGroup struct {
	Title  string
	Values []string
}

// Glossary is the term list, in the order 001:73-107 gives it.
func Glossary() []GlossaryEntry {
	return []GlossaryEntry{
		{"Agent", "智能体", "由 GitHub、飞书等身份对外行动的那个自动同事；它的回复发回请求来的地方，不在这个窗口里。"},
		{"Main Session", "主 Session", "做研判、接单、派工与统一回复的那个会话；代码与提示词里叫 orchestrator。"},
		{"Executor Session", "执行器 Session", "一个执行器为一张工单跑的会话；代码与提示词里叫 worker。"},
		{"Source", "来源", "一条事件流：一种由插件提供的 kind，加一份实例配置。它自己保存读位置。"},
		{"Trigger", "触发", "来自某个来源的一条不可变事件，是审计与幂等的单位。"},
		{"Thread", "线程", "按时间排列的 Trigger；同一线程上一次只跑一个 Eval。"},
		{"Correlation", "关联", "同一个 Trigger 属于多条线程时的关系；只增加上下文，不做路由。"},
		{"Channel", "渠道", "线程的写侧：回执、回复、PR、评审，都是一种写。"},
		{"Policy", "策略", "受理工作的版本化规则，以及每个来源各自的部分（谁能提需求、允许名单、机器人名单、准入表）。"},
		{"Policy version", "策略版本", "一份规则 + 来源部分 + 受理档位的组合编号；每张 Mandate 都记录它。"},
		{"Eval", "评估", "把一批 Trigger 变成 Decision 的过程，分 gate 与 assessment 两层。"},
		{"Gate", "准入", "Eval 的第一层，确定性的：谁可以发起工作、以什么强度；模型不参与。"},
		{"Assessment", "研判", "Eval 里模型做的语义判断，不决定授权强度。"},
		{"Decision", "决定", "Eval 的结果：accept / stop / ignore / unavailable。"},
		{"Mandate", "授权", "accept 给出的东西：强度、范围、所依据的策略版本与命中规则；不可变。"},
		{"Ticket", "工单", "一个被授权的工作单位；只有 Eval 能创建它。"},
		{"Assignment", "派工", "把工单绑定到一个执行器实例；一张工单先后可以有多个。"},
		{"Subscription", "订阅", "工单与线程之间的关系，由线程持有；后续事件据此扇出。"},
		{"Fan-out", "扇出", "把已订阅线程的 Trigger 当作上下文投递给工单；不重新评估，也不授权。"},
		{"Executor", "执行器", "运行 Assignment 的端口：一张工单一个执行器会话与一个目录。"},
		{"Execution environment", "执行环境", "执行器会话与 prepare 命令所在的环境：执行器与来源各自登记变量。"},
		{"Report", "报告", "一次派工向工单汇报的状态：progress / waiting / ready / blocked / failed / dropped。"},
		{"Attention", "待人工决定", "工单上的一个结构化提问，等一个人来回答。"},
		{"Disposition", "去向", "一条 Trigger 被过滤、评估或扇出的记录。"},
		{"Input", "输入", "投递给工单的上下文：扇出的 Trigger、策略变更、恢复、以及人的文字。"},
		{"Scope", "范围", "Mandate 允许做的工作范围。"},
		{"Strength", "强度", "Mandate 或 Eval 依据的强度：explicit（明确）或 inferred（推断）。"},
	}
}

// GlossaryValues is the closed value sets the domain tables define (001:108-113).
func GlossaryValues() []GlossaryValueGroup {
	return []GlossaryValueGroup{
		{"Decision 决定", []string{"accept 接单", "stop 停止", "ignore 忽略", "unavailable 未完成"}},
		{"Strength 强度", []string{"explicit 明确", "inferred 推断", "not_admitted 不准入"}},
		{"Assignment 派工", []string{"pending 待启动", "running 执行中", "ended 已结束"}},
		{"Report 报告", []string{"progress 进展", "waiting 等待", "ready 就绪", "blocked 受阻", "failed 失败", "dropped 无需处理"}},
		{"Stage 阶段", []string{"person/answer 等你回答", "person/blocked 受阻", "person/failed 失败", "person/delivery 投递失败", "person/review 等你评审", "working 执行中", "working/queued 排队中", "idle 空闲", "ended 已结束"}},
	}
}

// GlossaryLen is how many terms the glossary has; the panel writes it in the
// heading so a missing entry is visible instead of silent.
func GlossaryLen() int { return len(Glossary()) }
