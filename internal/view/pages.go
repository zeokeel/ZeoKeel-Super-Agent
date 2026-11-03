package view

// The five inspection pages (来源 / 触发 / 评估 / 执行 / 设置) draw only from
// the projections below. Every projection is built by the core, is immutable
// once published, and carries the facts the page needs — including the codes,
// so the page never has to parse free text back into structure (design §6.2,
// §6.3). Configuration lives in the settings page only: the other pages are
// read-only inspection.

import "time"

// ---------------------------------------------------------------------------
// 来源：one event source and its account facts, cursor and last poll.
// ---------------------------------------------------------------------------

// Fact is one labeled fact. The label is the name a person reads; the value is
// what the Source actually reported, never a config default.
type Fact struct {
	Label string
	Value string
}

// PollOutcome is how a poll attempt ended.
type PollOutcome string

const (
	// PollOK received everything and advanced the cursor.
	PollOK PollOutcome = "ok"
	// PollPartial advanced the cursor with some events unreadable; the page
	// still shows it, because partial loss must not look healthy.
	PollPartial PollOutcome = "partial"
	// PollFailed advanced nothing and needs a person.
	PollFailed PollOutcome = "failed"
)

// PollResult is the last poll attempt in the words the page shows.
type PollResult struct {
	At      time.Time
	Outcome PollOutcome
	// Detail is the one healthy line: "拉取 12 条 · 接收 12 · 丢弃 0".
	Detail string
	// Problem is the actionable line when Outcome is not PollOK: what failed
	// and what the person should do next. The page shows it as text, never as
	// an inline form (configuration belongs to 设置).
	Problem string
}

// SourceRow is one Source as one row of the page.
type SourceRow struct {
	// Kind is the Source's own name: github, feishu, dingtalk, schedule, local.
	Kind    string
	Name    string
	Account string
	Enabled bool
	// Facts are the account facts: credential, quota, scopes, subscriptions.
	Facts []Fact
	// Cursor says where the read position stands, in a person's words; CursorAt
	// is the same fact as a time, for the row's age.
	Cursor   string
	CursorAt time.Time
	LastPoll PollResult
	// Triggers24h and Filtered24h are this Source's 24h counts.
	Triggers24h int
	Filtered24h int
}

// FilterReason is one reason Triggers were dropped, with its count. A filter
// that cannot be counted cannot be audited (design §4.8).
type FilterReason struct {
	Reason string
	Count  int
}

// SourceVM is everything the source page draws.
type SourceVM struct {
	Sources []SourceRow
	// Filtered explains the 24h drops; FilteredTotal is their sum, kept so the
	// heading cannot disagree with the list.
	Filtered      []FilterReason
	FilteredTotal int
	// AccountFacts are the facts of the whole account rather than one Source:
	// quota left, token expiry, poll interval.
	AccountFacts []Fact
	UpdatedAt    time.Time
}

// EnabledCount returns how many Sources are on. The heading and the rows show
// the same number, which is the "诚实状态" rule (design §6.5).
func (vm *SourceVM) EnabledCount() int {
	if vm == nil {
		return 0
	}
	n := 0
	for _, s := range vm.Sources {
		if s.Enabled {
			n++
		}
	}
	return n
}

// FailingCount returns how many Sources need a person: a failed poll, or a
// Source switched off by the core rather than by the operator.
func (vm *SourceVM) FailingCount() int {
	if vm == nil {
		return 0
	}
	n := 0
	for _, s := range vm.Sources {
		if !s.Enabled || s.LastPoll.Outcome == PollFailed {
			n++
		}
	}
	return n
}

// ---------------------------------------------------------------------------
// 触发：Threads and the timeline of the selected Thread.
// ---------------------------------------------------------------------------

// ThreadRow is one Thread as a list row.
type ThreadRow struct {
	ID         string
	Title      string
	Source     string // "GitHub · Issue"
	SourceIcon string // github | feishu | dingtalk | schedule | local
	Who        string // "@lin"
	Workspace  string
	// Stage says what this Thread is doing now, in a person's words.
	Stage      string
	Tone       Tone
	Subscribed bool
	TicketID   string
	ActivityAt time.Time
	EventCount int
}

// ThreadEventKind says what one timeline row is.
type ThreadEventKind string

const (
	EventTrigger     ThreadEventKind = "trigger"     // an event arrived
	EventDecision    ThreadEventKind = "decision"    // Eval decided something
	EventDisposition ThreadEventKind = "disposition" // what happened to a Trigger
	EventContext     ThreadEventKind = "context"     // context only, no authority
)

// ThreadEvent is one row of a Thread's timeline, newest first.
type ThreadEvent struct {
	Kind  ThreadEventKind
	Title string
	// Code is the reason or disposition code, kept apart so it stays checkable;
	// Text is that code rendered through its template (001 / 002: 码 + 模板 +
	// 参数，不存自由文本).
	Code     string
	Text     string
	Decision string // the one-line Decision summary, for EventDecision
	At       time.Time
}

// ThreadDetail is the selected Thread: its timeline and the facts around it.
type ThreadDetail struct {
	ID         string
	Title      string
	Meta       string // "acme/webapp · 12 个 Trigger · 最后活动 12:40"
	Workspace  string
	TicketID   string
	Subscribed bool
	Events     []ThreadEvent
	// CursorNote and PolicyNote are the deterministic half of the Thread: what
	// the cursor did, and what the rules allow (subscription adds context, it
	// never grants authority).
	CursorNote string
	PolicyNote string
}

// ThreadVM is everything the trigger page draws.
type ThreadVM struct {
	// Threads are already sorted by recent activity by the projector: the view
	// never sorts (design §6.4).
	Threads []ThreadRow
	// Details holds each Thread's timeline keyed by ThreadRow.ID, so changing
	// the selection in the page needs no new publish.
	Details map[string]*ThreadDetail
	// Selected is the Thread the core suggests opening; "" picks the first.
	Selected   string
	Subscribed int
	Evaluating int
	New24h     int
	UpdatedAt  time.Time
}

// DetailOf returns one Thread's timeline, or nil when it was not projected.
func (vm *ThreadVM) DetailOf(id string) *ThreadDetail {
	if vm == nil || vm.Details == nil {
		return nil
	}
	return vm.Details[id]
}

// SelectedDetail returns the timeline of the suggested Thread, falling back to
// the first row so the page is never blank while Threads exist.
func (vm *ThreadVM) SelectedDetail() *ThreadDetail {
	if vm == nil {
		return nil
	}
	if d := vm.DetailOf(vm.Selected); d != nil {
		return d
	}
	if len(vm.Threads) == 0 {
		return nil
	}
	return vm.DetailOf(vm.Threads[0].ID)
}

// ---------------------------------------------------------------------------
// 评估：Decisions, their reason codes and the rules they hit.
// ---------------------------------------------------------------------------

// DecisionRow is one Decision as a list row.
type DecisionRow struct {
	ID string
	// Kind is the domain decision kind, unchanged: propose, accept, stop,
	// ignore, unavailable. KindLabel is how it reads in Chinese.
	Kind      string
	KindLabel string
	// Trigger says what was judged: "#42 新建 Issue".
	Trigger string
	// Summary says what the Decision does: "新建 Ticket #46 · acme-webapp".
	Summary    string
	ReasonCode string // "routed", "not_addressed", …
	ReasonText string // the code rendered through its template
	// Rule is the checkable policy rule that fired; "" when none did.
	Rule      string
	Workspace string
	Tone      Tone
	At        time.Time
}

// DecisionKindCount is one filter chip on the eval page.
type DecisionKindCount struct {
	Kind  string
	Label string
	Count int
}

// RuleHit is one policy rule and how often it fired in the window.
type RuleHit struct {
	Rule string
	Hits int
}

// GoldenStat is one checkable invariant suite. A rule change must change its
// cases in the same commit, so the page shows both numbers.
type GoldenStat struct {
	Name   string
	Passed int
	Total  int
}

// EvalCounts are the four totals of the eval page.
type EvalCounts struct {
	Evaluated int
	Decisions int
	Filtered  int
	Tickets   int
}

// DecisionVM is everything the eval page draws.
type DecisionVM struct {
	Rows          []DecisionRow // newest first, filtered by the projector only
	Kinds         []DecisionKindCount
	Counts        EvalCounts
	Rules         []RuleHit
	Golden        []GoldenStat
	PolicyVersion string
	UpdatedAt     time.Time
}

// ---------------------------------------------------------------------------
// 执行：Tickets by stage bucket.
// ---------------------------------------------------------------------------

// TicketBucket is the stage filter a person reads. It is derived from the
// Ticket's facts by the projector (domain.StageOf), never stored.
type TicketBucket string

const (
	BucketOpen    TicketBucket = "open"    // 待开工：已建单，尚未派给执行器
	BucketPending TicketBucket = "pending" // 排队中：执行器还没轮到
	BucketWorking TicketBucket = "working" // 执行中
	BucketWaiting TicketBucket = "waiting" // 等你处理：回答 / 授权 / 评审 / 受阻
	BucketEnded   TicketBucket = "ended"   // 已结束
)

// BucketOrder is the order the stage filters show, so the chips and the counts
// the core publishes cannot disagree.
func BucketOrder() []TicketBucket {
	return []TicketBucket{BucketOpen, BucketPending, BucketWorking, BucketWaiting, BucketEnded}
}

// Label is how a bucket reads in Chinese. The page shows this, never the raw
// value.
func (b TicketBucket) Label() string {
	switch b {
	case BucketOpen:
		return "待开工"
	case BucketPending:
		return "排队中"
	case BucketWorking:
		return "执行中"
	case BucketWaiting:
		return "等你处理"
	case BucketEnded:
		return "已结束"
	}
	return string(b)
}

// BucketOf maps a Ticket's derived stage and whether it has ever been assigned
// to an executor into the filter bucket the execution page offers. It lives here,
// as strings, so the mapping has one home and the view package stays leaf-level;
// the runtime calls it with string(domain.StageOf(t, now)).
//
// "working/queued" is Pending once an assignment exists and Open before it; an
// idle Ticket waits for a person or an event, so it is Waiting, not Working.
func BucketOf(stage string, assigned bool) TicketBucket {
	switch stage {
	case "ended":
		return BucketEnded
	case "person/delivery", "person/answer", "person/review", "person/blocked", "person/failed":
		return BucketWaiting
	case "idle":
		return BucketWaiting
	case "working":
		return BucketWorking
	case "working/queued":
		if assigned {
			return BucketPending
		}
		return BucketOpen
	}
	// An unknown stage is still work in progress; it must not disappear from
	// every filter.
	return BucketWorking
}

// BucketCount is one filter chip: a bucket, its label and its count.
type BucketCount struct {
	Bucket TicketBucket
	Label  string
	Count  int
}

// TicketDetail is what the execution page opens in place. Opening a row is
// navigation, not an action: the page still sends no command (design §6.3).
type TicketDetail struct {
	ID           string
	Workspace    string
	Stage        string
	Detail       string // why it waits, or what the last report said
	Source       string // "GitHub · Issue #42"
	Thread       string
	Mandate      string // "explicit · policy v7 · 由你授权"
	Artifacts    []Artifact
	LastActivity string
}

// TicketListVM is everything the execution page draws.
type TicketListVM struct {
	// Rows are all Tickets, sorted by recent activity by the projector, not by
	// creation time (design §6.10).
	Rows    []TicketRow
	Buckets []BucketCount
	Details map[string]TicketDetail
	Total   int
	// DetailVMs are the full details, by Ticket ID: Mandate, Attention,
	// Assignments/Reports, Inputs, Follows, Artifacts, Delivery. Details above
	// stays for a projector that only publishes the short form; a full detail is
	// preferred when both exist.
	DetailVMs map[string]*TicketDetailVM
	UpdatedAt time.Time
}

// DetailVMOf returns one Ticket's full detail, or nil when the projector has not
// published it. A nil detail is a fact the panel states, not one it fills in.
func (vm *TicketListVM) DetailVMOf(id string) *TicketDetailVM {
	if vm == nil || vm.DetailVMs == nil {
		return nil
	}
	return vm.DetailVMs[id]
}

// DetailOf returns one Ticket's opened-row detail.
func (vm *TicketListVM) DetailOf(id string) (TicketDetail, bool) {
	if vm == nil || vm.Details == nil {
		return TicketDetail{}, false
	}
	d, ok := vm.Details[id]
	return d, ok
}

// FilterTickets returns the rows in one bucket, or every row when bucket is "".
// It is a pure function so the filtering can be tested without a window; the
// page caches the result for the frame instead of solving it while drawing.
func FilterTickets(rows []TicketRow, bucket TicketBucket) []TicketRow {
	if bucket == "" {
		return rows
	}
	out := make([]TicketRow, 0, len(rows))
	for _, r := range rows {
		if r.Bucket == bucket {
			out = append(out, r)
		}
	}
	return out
}

// FilterTicketsByWorkspace returns the rows of one workspace, or every row when
// workspace is "" (the "全部" state). It compares workspace ids, so a renamed
// workspace keeps filtering the same work.
func FilterTicketsByWorkspace(rows []TicketRow, workspace string) []TicketRow {
	if workspace == "" {
		return rows
	}
	out := make([]TicketRow, 0, len(rows))
	for _, r := range rows {
		if r.WorkspaceID == workspace {
			out = append(out, r)
		}
	}
	return out
}

// FilterDecisions returns the rows of one decision kind, or every row when kind
// is "". Pure for the same reason as FilterTickets.
func FilterDecisions(rows []DecisionRow, kind string) []DecisionRow {
	if kind == "" {
		return rows
	}
	out := make([]DecisionRow, 0, len(rows))
	for _, r := range rows {
		if r.Kind == kind {
			out = append(out, r)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// 设置：grouped configuration. This is the only page with configuration
// actions (design §6.3, clarified: 配置动作只在设置页).
// ---------------------------------------------------------------------------

// SettingsGroupID names one block of settings. The order of the constants is
// the order the page shows, so the rail and the forms cannot disagree.
type SettingsGroupID string

const (
	SettingsSource    SettingsGroupID = "source"
	SettingsEval      SettingsGroupID = "eval"
	SettingsExecution SettingsGroupID = "execution"
	SettingsPeople    SettingsGroupID = "people"
	SettingsPrompt    SettingsGroupID = "prompt"
	SettingsDeploy    SettingsGroupID = "deploy"
)

// SettingKind is the control a field draws. "fact" is a read-only fact inside
// a form: the page shows it, the runtime owns it.
type SettingKind string

const (
	SettingText   SettingKind = "text"
	SettingSecret SettingKind = "secret"
	SettingSwitch SettingKind = "switch"
	SettingNumber SettingKind = "number"
	SettingSelect SettingKind = "select"
	SettingFact   SettingKind = "fact"
)

// SettingField is one field of a settings group.
type SettingField struct {
	Key   string
	Label string
	Value string // the persisted value, as the core read it
	Kind  SettingKind
	Help  string
	// Options are the choices of a select, in the order the menu shows them.
	Options []string
}

// SettingsGroup is one block of the settings page.
type SettingsGroup struct {
	ID    SettingsGroupID
	Label string
	Note  string
	// Fields are the real form of a wired group, empty for a group whose form
	// will arrive later.
	Fields []SettingField
	// Placeholder, when set, says why this group has no form yet. The group
	// still appears, so the page does not hide what is missing.
	Placeholder string
	// Pending names the facts this group will carry once the runtime projects
	// them (an admission table, a rule list, a bot list…). The panel writes
	// "等待运行时投影：<pending>" instead of showing an empty form that reads as
	// "nothing to configure".
	Pending []string
	// Effect says when a save takes effect, in a person's words: "热更新",
	// "重启后生效", "下次新工单生效". The panel writes it on the save bar; when
	// the runtime does not project it, SaveEffect(id) gives the group's default.
	Effect string
}

// SettingsVM is everything the settings page draws.
type SettingsVM struct {
	Groups []SettingsGroup
	// Source names where the values live: "mygo.json".
	Source string
	// Revision is the configuration revision these values were read at. A save
	// returns the new one, so a concurrent change in another window is
	// detectable instead of silently overwritten.
	Revision  uint64
	UpdatedAt time.Time
}

// FieldValue returns a setting value by key, across every published group. The
// panel reads presentation settings this way (ui.font_scale) so a runtime that
// stores them in any group still drives it.
func (vm *SettingsVM) FieldValue(key string) (string, bool) {
	if vm == nil {
		return "", false
	}
	for _, g := range vm.Groups {
		for _, f := range g.Fields {
			if f.Key == key {
				return f.Value, true
			}
		}
	}
	return "", false
}

// SaveEffect is when a group's save takes effect, in a person's words, when the
// projection does not say (SettingsGroup.Effect). Only a group whose changes are
// really live says 热更新; boot-only and channel-restart groups say so plainly.
func SaveEffect(id SettingsGroupID) string {
	switch id {
	case SettingsSource:
		return "保存后重启相关渠道（几秒；正在跑的工单不受影响）"
	case SettingsEval:
		return "保存后对下一次评估生效"
	case SettingsExecution:
		return "下次新工单生效"
	case SettingsPeople:
		return "立即生效"
	case SettingsPrompt:
		return "热更新：下一次模型调用生效"
	case SettingsDeploy:
		return "重启后生效"
	}
	return "保存后生效"
}

// Group returns one group by ID, or nil.
func (vm *SettingsVM) Group(id SettingsGroupID) *SettingsGroup {
	if vm == nil {
		return nil
	}
	for i := range vm.Groups {
		if vm.Groups[i].ID == id {
			return &vm.Groups[i]
		}
	}
	return nil
}

// RequiredSettingsGroups is the settings page's five groups, in the order it
// shows them, with the facts each will carry once the runtime projects them.
//
// The page always shows these five: a runtime that publishes only some of them
// still gets the others as "等待运行时投影：<Pending>" instead of a page that
// looks complete because a group is missing. A group the runtime publishes keeps
// its own Note/Fields; these Notes are the fallback.
func RequiredSettingsGroups() []SettingsGroup {
	return []SettingsGroup{
		{
			ID: SettingsSource, Label: "来源",
			Note: "每个来源的启用、凭据与订阅",
			Pending: []string{
				"来源实例与启用状态",
				"允许名单 / bot 名单",
				"admission 表（source+scope 的三格）",
			},
		},
		{
			ID: SettingsEval, Label: "评估",
			Note: "规则、准入表与受理档位；规则是代码 + 表格，不是提示词",
			Pending: []string{
				"规则列表（id / 是否启用 / instruction / proactive）",
				"受理档位 accept mode",
				"Policy 版本与变更历史",
			},
		},
		{
			ID: SettingsExecution, Label: "执行",
			Note: "执行器、预算与沙箱；改完只影响之后新开的工单",
			Pending: []string{
				"并发执行器与人的额外 slot",
				"prepare 命令与命令超时",
				"被禁止的命令（blocked commands）",
			},
		},
		{
			ID: SettingsPrompt, Label: "提示词",
			Note: "Router 与 Worker 分开，另有团队三段（communication / scope / delivery）",
			Pending: []string{
				"团队三段提示词",
				"版本历史与 base-hash 提示",
			},
		},
		{
			ID: SettingsDeploy, Label: "部署",
			Note: "日志、健康、备份与归档；命令行是等价运维面",
			Pending: []string{
				"日志路径与轮转",
				"备份 / 归档结果",
			},
		},
	}
}
