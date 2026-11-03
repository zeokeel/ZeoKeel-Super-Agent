// Package view holds the read-only projections the native window draws. The core
// publishes immutable values through atomic pointers; the UI loads one per frame
// and never locks. See design §6.2.
package view

import (
	"sync"
	"sync/atomic"
	"time"
)

// Section is one area of the sidebar.
type Section string

const (
	SecOverview  Section = "overview"
	SecWorkspace Section = "workspace"
	SecSource    Section = "source"
	SecTrigger   Section = "trigger"
	SecEval      Section = "eval"
	SecExecution Section = "execution"
	SecSettings  Section = "settings"
	SecMessages  Section = "messages"
	SecInbox     Section = "inbox"
	SecChat      Section = "chat"
)

// Tone colors a row's state chip.
type Tone string

const (
	ToneNeutral Tone = "neutral"
	ToneInfo    Tone = "info"
	ToneWarn    Tone = "warn"
	ToneDanger  Tone = "danger"
	ToneOK      Tone = "ok"
)

// NavItem is one sidebar entry.
type NavItem struct {
	Section Section
	Label   string
	Count   int
}

// TicketRow is one Ticket as a list row: the label says what it waits for, not an
// internal status code (design §6.3).
type TicketRow struct {
	// ID is the Ticket's domain id (the runtime's key): lookups for its detail
	// use this, never the number.
	ID string
	// Number is the Ticket's number, the part a person reads as "#42".
	Number int
	Title  string
	// Workspace is the workspace's display name; WorkspaceID is the id the
	// panel filters by (they are different things: a rename changes one).
	Workspace   string
	WorkspaceID string
	Source      string
	SourceIcon  string
	// Stage is the badge: what this row is waiting for, in a person's words.
	Stage string
	// StageID is the fact behind the badge: the derived stage id (see
	// StagePersonAnswer and the rest in ticket.go). The row's actions follow it,
	// never the wording.
	StageID string
	// Attention is the question the row waits to have answered; nil when the row
	// waits for something else. The panel's 回答 dialog draws it.
	Attention *TicketAttention
	// CanStop is the projector's answer to "may this Ticket be stopped now?" (a
	// running or queued Ticket may). The execution row and the inbox use it.
	CanStop bool
	// CanRetry is the projector's answer to "may this Ticket run again?" (a
	// blocked, failed or undeliverable Assignment may). It adds 重试 to the row's
	// actions for a stage the panel does not know.
	CanRetry bool
	// Detail is the one line under the title: why, or who asked.
	Detail string
	// Bucket is the stage filter the execution page offers (design §6.10).
	// Zero means the projector did not classify the row.
	Bucket TicketBucket
	// Activity is the one phrase the execution list shows for "最近活动":
	// "12 分前 · 报告已就绪".
	Activity string
	Tone     Tone
	Age      time.Duration
}

// TicketLabel is how a row reads: "#42". The number is what a person sees; the
// ID is the domain key used for every lookup. A projector that has not filled
// the number yet falls back to the id, so a row is never blank.
func TicketLabel(id string, number int) string {
	if number > 0 {
		return "#" + itoa(number)
	}
	if id != "" {
		return "#" + id
	}
	return "#?"
}

// ApprovalRow is one request waiting for the operator's yes. It is not a Ticket:
// no work has been authorized yet.
type ApprovalRow struct {
	DecisionID  string
	From        string
	Source      string
	Said        string
	Intent      string
	Deliverable string
	Workspace   string
	Sensitive   []string
	Age         time.Duration
}

// HealthItem is one part of the system. A healthy part takes one line; only a
// problem gets detail.
type HealthItem struct {
	Name   string
	Detail string
	OK     bool
}

// HourCount is one bar of the last 24 hours.
type HourCount struct {
	Hour  int
	Count int
}

// Runtime holds what the runtime is doing right now.
type Runtime struct {
	Running  int
	MaxJobs  int
	Queued   int
	Today    int
	NextPoll time.Duration
	// Paused is the run/pause switch a person controls (PauseExecution), and
	// PausedKnown says the projector actually read it. Without the second, a
	// false Paused cannot be told from "not projected", and the panel would show
	// 运行中 for a paused runtime.
	Paused      bool
	PausedKnown bool
}

// Artifact is one deliverable of recent work.
type Artifact struct {
	Kind   string // "pr" | "file" | "report" | "image"
	Title  string
	Detail string
	// Path is where the file is, when the runtime knows: 打开/复制 on the panel.
	Path string
}

// Counts are the four activity totals.
type Counts struct {
	Triggers  int
	Evals     int
	Decisions int
	Tickets   int
}

// Usage is how much model work this process has done. It is per-run, not
// all-time: the honest label is "since start", because the counter lives in
// memory and a restart legitimately starts it over (design §5.4).
type Usage struct {
	Prompt     int
	Completion int
	Total      int
	// Cached is how much of the prompt the provider served from its KV cache.
	Cached int
	// Calls counts model calls, including the summarizer's.
	Calls int
}

// Storage describes the state directory.
type Storage struct {
	DB         string
	Events     int
	LastBackup string
}

// SystemInfo is the machine the window runs on, as a person reads it: the OS and
// architecture, the timezone, and the offset from UTC. The runtime fills it from
// its own process; the panel shows it, and never guesses one ("UTC" is a fact,
// "" is not).
type SystemInfo struct {
	OS       string // linux, darwin, windows
	Arch     string // amd64, arm64
	Timezone string // Asia/Shanghai, Local…
	// UTCOffset is "+08:00" or "UTC+08:00" as the runtime writes it; the panel
	// shows it as it comes.
	UTCOffset string
}

// Known reports whether the projector filled the system facts.
func (s SystemInfo) Known() bool { return s.OS != "" || s.Arch != "" || s.Timezone != "" }

// Label is the one line the sidebar footer and 设置 › 部署 show:
// "linux/amd64 · Asia/Shanghai (UTC+08:00)".
func (s SystemInfo) Label() string {
	head := ""
	switch {
	case s.OS != "" && s.Arch != "":
		head = s.OS + "/" + s.Arch
	case s.OS != "":
		head = s.OS
	case s.Arch != "":
		head = s.Arch
	}
	tz := s.Timezone
	if tz == "" {
		tz = "时区未知"
	}
	if s.UTCOffset != "" {
		tz += " (" + s.UTCOffset + ")"
	}
	switch {
	case head == "" && s.Timezone == "" && s.UTCOffset == "":
		return ""
	case head == "":
		return tz
	case s.Timezone == "" && s.UTCOffset == "":
		return head
	}
	return head + " · " + tz
}

// OverviewVM is everything the overview page draws.
type OverviewVM struct {
	// Approvals are requests that would start work but need a person first. They
	// are not Tickets yet, so they are a separate list from NeedsPerson.
	Approvals   []ApprovalRow
	NeedsPerson []TicketRow
	// Recent is what the person handed over lately, newest first. The overview
	// is where they look right after dispatching, so it must show the hand-off,
	// not only the exceptions.
	Recent    []TicketRow
	Health    []HealthItem
	Activity  []HourCount
	Counts    Counts
	Runtime   Runtime
	Artifacts []Artifact
	Storage   Storage
	// Usage is the token total since this process started.
	Usage Usage
	// System is the machine the window runs on: OS, architecture and timezone.
	System    SystemInfo
	UpdatedAt time.Time
}

// Snapshot holds the published projections. Writers store a whole new value;
// readers load it once per frame, so a frame never sees two versions of a page.
type Snapshot struct {
	overview  atomic.Pointer[OverviewVM]
	workbench atomic.Pointer[StreamVM]

	// The inspection pages are separate shards: a health heartbeat must not
	// rebuild a long ticket list (design §6.2).
	sources   atomic.Pointer[SourceVM]
	threads   atomic.Pointer[ThreadVM]
	decisions atomic.Pointer[DecisionVM]
	tickets   atomic.Pointer[TicketListVM]
	settings  atomic.Pointer[SettingsVM]
	prompts   atomic.Pointer[PromptVM]
	search    atomic.Pointer[SearchVM]
	// 定时 and 目标 are what makes the agent start work by itself: their own
	// shards, so a schedule's next-fire countdown never rebuilds a ticket list.
	schedules atomic.Pointer[ScheduleVM]
	goals     atomic.Pointer[GoalVM]
	// workspaces is the sidebar's shard: workspace rows and their conversations.
	workspaces atomic.Pointer[WorkspacePanelVM]
	// chat is the 超级Agent conversation shard.
	chat atomic.Pointer[ChatVM]
	// attention is the unified inbox shard.
	attention atomic.Pointer[AttentionVM]
	// plan is the running task's plan shard.
	plan atomic.Pointer[PlanVM]
	// onboarding is the first-run wizard; nil means "this install is set up".
	onboarding atomic.Pointer[OnboardingVM]
	artifacts  atomic.Pointer[ArtifactVM]

	mu       sync.Mutex
	revision uint64
	notify   func()
}

// NewSnapshot returns an empty snapshot.
func NewSnapshot() *Snapshot { return &Snapshot{} }

// OnPublish registers the function that asks the window for a frame. It must not
// block: it is called from whichever goroutine published.
func (s *Snapshot) OnPublish(fn func()) {
	s.mu.Lock()
	s.notify = fn
	s.mu.Unlock()
}

// bump counts one publish and asks for one frame. The caller stores the value
// first, so a frame triggered by this call always sees the new value.
func (s *Snapshot) bump() {
	s.mu.Lock()
	s.revision++
	fn := s.notify
	s.mu.Unlock()
	if fn != nil {
		fn()
	}
}

// PublishOverview stores a new overview and asks for one frame.
func (s *Snapshot) PublishOverview(vm *OverviewVM) {
	s.overview.Store(vm)
	s.bump()
}

// PublishStream stores a new stream and asks for one frame.
func (s *Snapshot) PublishStream(vm *StreamVM) {
	s.workbench.Store(vm)
	s.bump()
}

// PublishSources stores a new source page projection.
func (s *Snapshot) PublishSources(vm *SourceVM) {
	s.sources.Store(vm)
	s.bump()
}

// PublishThreads stores a new trigger page projection.
func (s *Snapshot) PublishThreads(vm *ThreadVM) {
	s.threads.Store(vm)
	s.bump()
}

// PublishDecisions stores a new eval page projection.
func (s *Snapshot) PublishDecisions(vm *DecisionVM) {
	s.decisions.Store(vm)
	s.bump()
}

// PublishTickets stores a new execution page projection.
func (s *Snapshot) PublishTickets(vm *TicketListVM) {
	s.tickets.Store(vm)
	s.bump()
}

// PublishSettings stores a new settings page projection.
func (s *Snapshot) PublishSettings(vm *SettingsVM) {
	s.settings.Store(vm)
	s.bump()
}

// PublishPrompts stores a new prompt editor projection. It is a shard of its own
// so a settings save does not rebuild the long prompt bodies, and typing into one
// group never redraws another.
// PublishOnboarding hands the wizard to the window.
func (s *Snapshot) PublishOnboarding(vm *OnboardingVM) {
	s.onboarding.Store(vm)
	s.bump()
}

// PublishWorkspaces hands the sidebar its workspace rows and conversations.
func (s *Snapshot) PublishWorkspaces(vm *WorkspacePanelVM) {
	s.workspaces.Store(vm)
	s.bump()
}

// Workspaces returns the last workspace projection, or nil before the first
// publish. The sidebar then says it waits instead of drawing an empty group as
// if no workspace existed.
func (s *Snapshot) Workspaces() *WorkspacePanelVM { return s.workspaces.Load() }

// PublishPlan hands the panel the running task's plan.
func (s *Snapshot) PublishPlan(vm *PlanVM) {
	s.plan.Store(vm)
	s.bump()
}

// Plan returns the last published plan, or nil when nothing is planned.
func (s *Snapshot) Plan() *PlanVM { return s.plan.Load() }

// PublishAttention hands the panel the unified attention inbox.
func (s *Snapshot) PublishAttention(vm *AttentionVM) {
	s.attention.Store(vm)
	s.bump()
}

// Attention returns the last attention inbox, or nil before the first publish
// (the panel then falls back to the overview's two lists).
func (s *Snapshot) Attention() *AttentionVM { return s.attention.Load() }

// PublishChat hands the panel the 超级Agent conversation.
func (s *Snapshot) PublishChat(vm *ChatVM) {
	s.chat.Store(vm)
	s.bump()
}

// Chat returns the last 超级Agent conversation, or nil before the first publish.
// The panel then draws no chat rather than an empty one.
func (s *Snapshot) Chat() *ChatVM { return s.chat.Load() }

// PublishArtifacts hands the deliverables panel its rows.
func (s *Snapshot) PublishArtifacts(vm *ArtifactVM) {
	s.artifacts.Store(vm)
	s.bump()
}

// Artifacts returns the last deliverables projection, or nil.
func (s *Snapshot) Artifacts() *ArtifactVM { return s.artifacts.Load() }

// Onboarding returns the last first-run wizard state, or nil.
func (s *Snapshot) Onboarding() *OnboardingVM { return s.onboarding.Load() }

func (s *Snapshot) PublishPrompts(vm *PromptVM) {
	s.prompts.Store(vm)
	s.bump()
}

// PublishSearch stores results the core computed for a query: a runtime that
// indexes history beyond what the window holds answers through it. A nil value
// clears the shard, and the palette falls back to BuildSearch over the published
// projections.
func (s *Snapshot) PublishSearch(vm *SearchVM) {
	s.search.Store(vm)
	s.bump()
}

// PublishSchedules stores a new 定时 projection for the 触发 page.
func (s *Snapshot) PublishSchedules(vm *ScheduleVM) {
	s.schedules.Store(vm)
	s.bump()
}

// PublishGoals stores a new 目标 projection: the overview strip and the 触发
// page's goal list both read it.
func (s *Snapshot) PublishGoals(vm *GoalVM) {
	s.goals.Store(vm)
	s.bump()
}

// Stream returns the current stream, or nil before the first publish.
func (s *Snapshot) Stream() *StreamVM { return s.workbench.Load() }

// Overview returns the current overview, or nil before the first publish.
func (s *Snapshot) Overview() *OverviewVM { return s.overview.Load() }

// Sources returns the current source page, or nil before the first publish.
func (s *Snapshot) Sources() *SourceVM { return s.sources.Load() }

// Threads returns the current trigger page, or nil before the first publish.
func (s *Snapshot) Threads() *ThreadVM { return s.threads.Load() }

// Decisions returns the current eval page, or nil before the first publish.
func (s *Snapshot) Decisions() *DecisionVM { return s.decisions.Load() }

// Tickets returns the current execution page, or nil before the first publish.
func (s *Snapshot) Tickets() *TicketListVM { return s.tickets.Load() }

// Settings returns the current settings page, or nil before the first publish.
func (s *Snapshot) Settings() *SettingsVM { return s.settings.Load() }

// Prompts returns the current prompt editor projection, or nil before the first
// publish. The editor says so instead of inventing defaults.
func (s *Snapshot) Prompts() *PromptVM { return s.prompts.Load() }

// Search returns the search projection the core published, or nil. It answers the
// query it names; a palette whose query moved on builds its own results.
func (s *Snapshot) Search() *SearchVM { return s.search.Load() }

// Schedules returns the 定时 projection, or nil before the first publish. The page
// then says it waits instead of drawing an empty list as if there were none.
func (s *Snapshot) Schedules() *ScheduleVM { return s.schedules.Load() }

// Goals returns the 目标 projection, or nil before the first publish.
func (s *Snapshot) Goals() *GoalVM { return s.goals.Load() }

// Revision counts publishes, for tests and the status bar.
func (s *Snapshot) Revision() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.revision
}

// PersonCount is how many things wait for the operator: the requests that would
// start work plus the Tickets that wait for an answer. It is the one derivation
// of that number: the sidebar badge, the topbar chip and the overview's heading
// all read it, so three surfaces cannot show three counts for the same page
// (design §6.5, "honest state": a count equals the list it links to).
func (o *OverviewVM) PersonCount() int {
	if o == nil {
		return 0
	}
	return len(o.NeedsPerson) + len(o.Approvals)
}

// Nav returns the sidebar model. The counts are the lengths of the lists they
// link to, so a badge never contradicts its page (design §6.5, "honest state").
func Nav(o *OverviewVM) []NavItem {
	person, running := o.PersonCount(), 0
	if o != nil {
		running = o.Runtime.Running
	}
	// The daily rail: five places a person uses every day. Everything else is
	// diagnostics, reached below a group separator (NavAdvanced).
	return []NavItem{
		{Section: SecOverview, Label: "超级Agent"},
		{Section: SecInbox, Label: "收件箱", Count: person},
		{Section: SecWorkspace, Label: "项目"},
		{Section: SecExecution, Label: "执行", Count: running},
		{Section: SecSettings, Label: "设置"},
	}
}

// NavAdvanced is the diagnostics group: the engineering pages stay reachable,
// below a separator and out of the daily path (interaction audit §III).
func NavAdvanced() []NavItem {
	return []NavItem{
		{Section: SecChat, Label: "对话记录"},
		{Section: SecMessages, Label: "消息"},
		{Section: SecSource, Label: "连接"},
		{Section: SecTrigger, Label: "触发"},
		{Section: SecEval, Label: "评估"},
	}
}

// AttentionLabel is the one wording for everything that waits for a person.
func AttentionLabel(n int) string {
	if n <= 0 {
		return "待你决定"
	}
	return "待你决定 " + itoa(n)
}

// SectionLabel is how a section reads in the panel. Nav holds the monitoring rail
// (the sidebar's bottom group and 设置); 工作区 is reached through a workspace row,
// not a rail item, and this names it too.
func SectionLabel(s Section) string {
	switch s {
	case SecWorkspace:
		return "项目"
	case SecChat:
		return "对话记录"
	case SecInbox:
		return "收件箱"
	}
	for _, it := range append(Nav(nil), NavAdvanced()...) {
		if it.Section == s {
			return it.Label
		}
	}
	return string(s)
}

// WorkspaceChoice is one option in the task composer: what the person can pick.
type WorkspaceChoice struct {
	ID      string // domain.WorkspaceID, kept as a string so view stays leaf-level
	Name    string
	Purpose string
}

// ---------------------------------------------------------------------------
// The stream: one continuous timeline of everything the agent saw and did.
// Everything arrives here; the agent does not hold a conversation in this window,
// it replies in the conversation the request came from (000 Philosophy).
// ---------------------------------------------------------------------------

// LineKind says what one line of the stream is.
type LineKind string

const (
	LineInbound  LineKind = "inbound"  // a message arrived from a Source
	LineDecision LineKind = "decision" // what the agent decided to do with it
	LineProgress LineKind = "progress" // a worker's step
	LineVerify   LineKind = "verify"   // a check that passed or failed
	LineArtifact LineKind = "artifact" // a deliverable
	LineQuestion LineKind = "question" // the agent waits for a person
	LineNotice   LineKind = "notice"   // a system line: queued, blocked, resumed
	LineOperator LineKind = "operator" // the operator typed in this window
	LineReply    LineKind = "reply"    // who the answer goes to
	LineTool     LineKind = "tool"     // one tool call and its result
)

// Line is one row of the stream.
type Line struct {
	Kind LineKind
	// Who and Source label the row: "钉钉 · 张三", "GitHub · #42 · @lin", "你".
	Who    string
	Source string
	// Text is the message body, or the one-line summary of a decision.
	Text string
	At   time.Time

	// Badges are short facts: workspace, ticket number, stage.
	Badges []string
	Tone   Tone

	// Tool rows only.
	Tool     string
	Command  string
	ExitCode int
	Duration time.Duration
	DiffAdd  int
	DiffDel  int
	Output   []string
}

// Thread is one message and everything the agent did about it, indented under
// it. Grouping is what makes one timeline readable instead of a log.
type Thread struct {
	// ID identifies the Thread across publishes, so the list drawing the stream
	// keeps its place and its row state as older messages load. "" means the
	// projector does not publish one yet, and the rows are keyed by index.
	ID string
	// Workspace is the workspace id the message belongs to, so the panel can
	// filter the stream to the selected workspace. "" means the projector did not
	// classify it, and the thread shows only under "全部".
	Workspace string
	Head      Line
	Steps     []Line
}

// FilterThreadsByWorkspace returns the threads of one workspace, or every thread
// when workspace is "". A thread the projector did not classify belongs to no
// workspace, so it shows only under 全部.
func FilterThreadsByWorkspace(threads []Thread, workspace string) []Thread {
	if workspace == "" {
		return threads
	}
	out := make([]Thread, 0, len(threads))
	for _, t := range threads {
		if t.Workspace == workspace {
			out = append(out, t)
		}
	}
	return out
}

// StreamVM is everything the workbench draws.
type StreamVM struct {
	Threads []Thread
	// Runnable is false until the Agent Runtime is wired (M1).
	Runnable bool
}

// PendingNote explains an empty plan honestly.
func (vm *StreamVM) PendingNote() string {
	if vm.Runnable {
		return "正在等待执行器。"
	}
	return "已排队，等待执行器接手。"
}
