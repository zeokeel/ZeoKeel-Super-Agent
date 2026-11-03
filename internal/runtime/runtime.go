// Package runtime wires the engine to the window: it turns the engine's state
// into the read-only projections the UI draws, and gives the UI one way to submit
// work. It is the only package that knows both sides.
package runtime

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zeokeel/super-agent/internal/agent"
	"github.com/zeokeel/super-agent/internal/domain"
	"github.com/zeokeel/super-agent/internal/effects"
	"github.com/zeokeel/super-agent/internal/engine"
	"github.com/zeokeel/super-agent/internal/goal"
	"github.com/zeokeel/super-agent/internal/outbox"
	"github.com/zeokeel/super-agent/internal/schedule"
	"github.com/zeokeel/super-agent/internal/skills"
	"github.com/zeokeel/super-agent/internal/store"
	"github.com/zeokeel/super-agent/internal/view"
)

// Runtime publishes projections of the engine and accepts tasks from the panel.
type Runtime struct {
	eng   *engine.Engine
	snap  *view.Snapshot
	clock func() time.Time

	mu      sync.Mutex
	lastErr string
	worker  *WorkerConfig
	log     eventLog

	// sources is what the source drivers last reported, for the 来源 page.
	sources []SourceStatus
	// settings is the configuration the operator saved. M2 moves it into the
	// store; until then it is honest about living in memory.
	settings    map[string]string
	settingsRev uint64
	// replies are the outbound messages produced for Tickets, so the panel can
	// show what was said even when no channel is wired.
	replies []Reply
	// settingsStore is where settings are committed; nil means memory-only.
	settingsStore *store.Store
	// workers tracks running executor goroutines so shutdown and tests can wait.
	workers sync.WaitGroup
	// usage accumulates model tokens since this process started.
	usage view.Usage
	// active is how many executor goroutines are running right now. "x/y 执行中"
	// must be this number, not a guess derived from Ticket stages.
	active int64
	// cursors is the memory fallback for Source resume tokens (kv.go).
	cursors map[string]string
	// setupMarked is this process's answer to "is the install configured?".
	setupMarked bool
	// healthCache keeps the durable components' self-reports off the per-frame path.
	healthCache []store.HealthEntry
	healthAt    time.Time
	// logger is the process logger set up by cmd (slog). Nil means slog.Default().
	logger *slog.Logger
	// runners are kept per Ticket so a background job survives the turn that
	// started it (fix-agent handoff §5.1). jobsMu guards runners/jobWatch.
	runners  map[domain.ID]*agent.Runner
	jobWatch map[string]bool
	// bgKilled marks jobs the timeout rule killed, so their late Wait result can
	// never wake the session. bgLimit overrides the 30m setting (tests).
	bgKilled map[string]bool
	bgLimit  time.Duration
	jobsMu   sync.Mutex
	// stateDir is where the database and session logs live, for the storage card.
	stateDir      string
	storageCached view.Storage
	storageAt     time.Time
	// sched and goals are optional capabilities; nil disables their tools.
	sched *schedule.Manager
	goals *goal.Service
	// skills is the library the Worker prompt draws from; the load_skill tool
	// reads full bodies from it.
	skills *skills.Set

	// persist and queue are set only in durable mode. When they are nil the app
	// keeps everything in memory, which is what the demo and the tests want.
	persist *engine.Persistence
	queue   *effects.Queue
	ob      *outbox.Outbox
}

// New returns a runtime that publishes into snap.
func New(eng *engine.Engine, snap *view.Snapshot) *Runtime {
	return &Runtime{eng: eng, snap: snap, clock: time.Now}
}

// SetLogger attaches the process logger. cmd owns the file handler; the runtime
// only emits records so a headless run has an after-the-fact trail.
func (r *Runtime) SetLogger(l *slog.Logger) {
	r.mu.Lock()
	r.logger = l
	r.mu.Unlock()
}

// slogger returns the attached logger, or the process default.
func (r *Runtime) slogger() *slog.Logger {
	r.mu.Lock()
	l := r.logger
	r.mu.Unlock()
	if l == nil {
		return slog.Default()
	}
	return l
}

// RegisterWorkspace adds a workspace. In durable mode the workspace and its policy
// are committed before anything can reference them.
func (r *Runtime) RegisterWorkspace(w domain.Workspace, policy engine.Policy) error {
	r.mu.Lock()
	p := r.persist
	r.mu.Unlock()
	if p != nil {
		if err := p.Register(w, policy); err != nil {
			return err
		}
	} else {
		r.eng.Register(w, policy)
	}
	r.Publish()
	return nil
}

// Publish rebuilds the projections from engine state and hands them to the
// window. It may run on any goroutine: the values are immutable and the window is
// asked for one frame.
func (r *Runtime) Publish() {
	now := r.clock()
	feed := r.eng.Feed()
	triggers, decisions, tickets := r.eng.Counts()
	all := r.eng.AllTickets()

	running := int(atomic.LoadInt64(&r.active))
	maxJobs := r.concurrency()
	queued := r.queuedCount()
	totals := evalTotalsFrom(feed, all)
	ws := r.eng.Workspaces()

	// Every number below is derived here and now: the panel's "x/y 执行中", "队列
	// N", "今日运行 N 次" and the eval totals all come from real state, so a
	// healthy card cannot sit beside a busy or broken one (005 诚实状态, AUDIT
	// §0.2-10). The old hardcoded MaxJobs=2 and the never-assigned fields were
	// exactly that lie.
	ov := &view.OverviewVM{
		Counts: view.Counts{
			Triggers:  triggers,
			Evals:     totals.assessed,
			Decisions: decisions,
			Tickets:   tickets,
		},
		Activity:  activityByHour(feed, now),
		UpdatedAt: now,
		Storage:   r.storageInfo(now),
		Usage:     r.usageSnapshot(),
		Health:    r.healthItems(ws, running, maxJobs, now),
		Runtime: view.Runtime{
			Running: running, MaxJobs: maxJobs, Queued: queued,
			Today: runsToday(all, now), NextPoll: r.pollInterval(),
			Paused: r.Paused(), PausedKnown: true,
		},
	}
	for _, d := range r.eng.Proposals() {
		tr, _ := r.eng.TriggerOf(d.ID)
		p := d.Proposal
		if p == nil {
			continue
		}
		ov.Approvals = append(ov.Approvals, view.ApprovalRow{
			DecisionID: string(d.ID), From: tr.Actor.ID, Source: tr.Source,
			Said: p.Said, Intent: p.Intent, Deliverable: p.Deliverable,
			Workspace: p.Workspace, Sensitive: p.Sensitive,
			Age: now.Sub(tr.At),
		})
	}
	for _, t := range all {
		st := domain.StageOf(&t, now)
		if isPersonStage(st) {
			row := view.TicketRow{
				ID:         number(t.Number),
				Title:      t.Title,
				Workspace:  string(t.Workspace),
				SourceIcon: "local",
				Stage:      waitingLabel(st),
				StageID:    string(st),
				Detail:     waitingFor(st, &t),
				CanRetry:   t.CanRetry(),
				Tone:       toneOf(st),
				Age:        now.Sub(t.CreatedAt),
			}
			if t.Attention != nil {
				row.Attention = &view.TicketAttention{
					Question: t.Attention.Question,
					Options:  t.Attention.Options,
					AskedAt:  t.Attention.At,
				}
			}
			ov.NeedsPerson = append(ov.NeedsPerson, row)
		}
		switch st {
		case domain.StageWorkingQueued:
			ov.Runtime.Queued++
		}
		if len(ov.Recent) < 4 {
			ov.Recent = append(ov.Recent, view.TicketRow{
				ID:         number(t.Number),
				Title:      t.Title,
				Workspace:  string(t.Workspace),
				SourceIcon: "local",
				Stage:      stageWord(st),
				Detail:     "由你派发 · " + string(t.Workspace),
				Tone:       toneOf(st),
				Age:        now.Sub(t.CreatedAt),
			})
		}
	}
	for _, a := range r.eng.AllArtifacts() {
		if len(ov.Artifacts) >= 3 {
			break
		}
		ov.Artifacts = append(ov.Artifacts, view.Artifact{
			Kind: string(a.Kind), Title: a.Title, Detail: a.Detail,
		})
	}
	r.snap.PublishOverview(ov)
	r.snap.PublishStream(r.stream())
	// The five inspection pages read the same state through their own projections.
	r.snap.PublishSources(r.sourcesVM(now))
	r.snap.PublishThreads(r.threadsVM(now))
	r.snap.PublishDecisions(r.decisionsVM(now))
	r.snap.PublishTickets(r.ticketsVM(now))
	r.snap.PublishSettings(r.settingsVM(now))
	r.snap.PublishPrompts(r.promptsVM())
	r.snap.PublishOnboarding(r.onboardingVM())
	r.snap.PublishSchedules(r.schedulesVM(now))
	r.snap.PublishGoals(r.goalsVM(now))
	r.snap.PublishArtifacts(r.artifactsVM(now))
}

// evalTotalsFrom derives the eval-page numbers from the feed: a Trigger is
// "evaluated" when a Decision exists or an Eval is running for it, "filtered" when
// the gate dropped it before any model call, and a Ticket is counted once even if
// several Triggers fanned out to it. They are computed from facts, so "评估的
// Trigger" and "Decision" can differ (a batch Eval produces one Decision for
// several Triggers; a filtered Trigger produces none).
type evalTotals struct {
	assessed int
	filtered int
	tickets  int
}

func evalTotalsFrom(feed []engine.FeedItem, tickets []domain.Ticket) evalTotals {
	var t evalTotals
	seenTickets := map[domain.ID]bool{}
	for _, item := range feed {
		switch {
		case item.Decision != nil:
			t.assessed++
		case item.Disp.Kind == domain.DispEvaluating:
			t.assessed++
		case item.Disp.Kind == domain.DispFiltered:
			t.filtered++
		}
		// FeedItem.Ticket is an engine pointer for fan-out dispositions; resolve a
		// snapshot instead so the projection never reads engine memory.
		if tk := ticketFor(item, tickets); tk != nil && !seenTickets[tk.ID] {
			seenTickets[tk.ID] = true
			t.tickets++
		}
	}
	return t
}

// activityByHour buckets the last 24 hours of Triggers for the overview chart.
func activityByHour(feed []engine.FeedItem, now time.Time) []view.HourCount {
	const hours = 24
	out := make([]view.HourCount, hours)
	base := now.Truncate(time.Hour)
	for i := 0; i < hours; i++ {
		out[i].Hour = base.Add(-time.Duration(hours-1-i) * time.Hour).Hour()
	}
	for _, item := range feed {
		at := item.Trigger.At
		if at.IsZero() {
			continue
		}
		back := int(base.Sub(at.Truncate(time.Hour)) / time.Hour)
		if back < 0 || back >= hours {
			continue
		}
		out[hours-1-back].Count++
	}
	return out
}

// runsToday counts executor runs that started since local midnight. It is what
// "今日运行 N 次" means; counting Tickets instead would hide retries.
func runsToday(tickets []domain.Ticket, now time.Time) int {
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	n := 0
	for _, t := range tickets {
		for _, as := range t.Assignments {
			if !as.StartedAt.IsZero() && !as.StartedAt.Before(start) {
				n++
			}
		}
	}
	return n
}

// queuedCount is the real backlog: pending launch effects in durable mode (the
// queue is the source of truth there), Tickets waiting for an executor otherwise.
func (r *Runtime) queuedCount() int {
	r.mu.Lock()
	q := r.queue
	r.mu.Unlock()
	if q != nil {
		if s, err := q.Status(engine.EffectLaunch); err == nil {
			return s.Waiting
		}
	}
	now := r.clock()
	n := 0
	for _, t := range r.ticketSnapshots() {
		if t.Ended == nil && domain.StageOf(&t, now) == domain.StageWorkingQueued {
			n++
		}
	}
	return n
}

// healthItems is the health card: only parts that exist, each one's OK flag taken
// from a real signal (model configured, source last poll, durable health rows).
func (r *Runtime) healthItems(ws []domain.Workspace, running, maxJobs int, now time.Time) []view.HealthItem {
	items := []view.HealthItem{
		{Name: "运行时", Detail: "进程内引擎就绪", OK: true},
		{Name: "工作区", Detail: fmt.Sprintf("%d 个已登记", len(ws)), OK: true},
	}
	exec := view.HealthItem{Name: "执行器", Detail: fmt.Sprintf("%d/%d 在跑", running, maxJobs), OK: true}
	if !r.HasWorkerModel() {
		exec.OK = false
		exec.Detail = "未配置模型，工单会挂起"
	}
	items = append(items, exec)
	items = append(items, r.modelHealth()...)
	items = append(items, r.sourceHealth()...)
	for _, e := range r.healthEntries(now) {
		if e.State == "error" {
			items = append(items, view.HealthItem{Name: e.Entry, Detail: e.Detail, OK: false})
		}
	}
	r.mu.Lock()
	storeAttached := r.settingsStore != nil
	r.mu.Unlock()
	if storeAttached {
		items = append(items, view.HealthItem{Name: "数据库", Detail: fmt.Sprintf("SQLite · schema v%d", store.SchemaVersion), OK: true})
	} else {
		items = append(items, view.HealthItem{Name: "数据库", Detail: "内存模式（重启丢失）", OK: true})
	}
	return items
}

// sourceHealth reports a Source whose last poll failed. A disabled Source is not
// a problem: it was turned off on purpose.
func (r *Runtime) sourceHealth() []view.HealthItem {
	r.mu.Lock()
	rows := append([]SourceStatus(nil), r.sources...)
	r.mu.Unlock()
	var out []view.HealthItem
	for _, s := range rows {
		if !s.Enabled || s.Last.Outcome != view.PollFailed {
			continue
		}
		detail := "最近一次轮询失败"
		if strings.TrimSpace(s.Last.Problem) != "" {
			detail = clipLine(s.Last.Problem, 120)
		}
		out = append(out, view.HealthItem{Name: "来源 " + s.Kind, Detail: detail, OK: false})
	}
	return out
}

// healthEntries reads the durable components' self-reports, cached briefly: the
// supervisor writes them every minute and the projector must not query per frame.
func (r *Runtime) healthEntries(now time.Time) []store.HealthEntry {
	r.mu.Lock()
	st, cached, at := r.settingsStore, r.healthCache, r.healthAt
	r.mu.Unlock()
	if st == nil {
		return nil
	}
	if !at.IsZero() && now.Sub(at) < 2*time.Second {
		return cached
	}
	entries, err := st.HealthEntries()
	if err != nil {
		return cached
	}
	r.mu.Lock()
	r.healthCache, r.healthAt = entries, now
	r.mu.Unlock()
	return entries
}

// stream builds the one timeline the window shows: every message that arrived,
// and what the agent did about it. Design §6.3.
func (r *Runtime) stream() *view.StreamVM {
	now := r.clock()
	vm := &view.StreamVM{Runnable: false}

	tickets := r.ticketSnapshots()
	for _, item := range r.eng.Feed() {
		t := item.Trigger
		// Engine Feed fills Ticket only for fan-out/owned dispositions; a decided
		// request links through the Ticket's origin or the Decision. Derive it so
		// the stream never shows "未受理" next to a Ticket that exists.
		itemTicket := ticketFor(item, tickets)
		th := view.Thread{ID: string(t.ID), Head: inboundLine(t)}
		st := domain.Stage(reportStage(itemTicket, now))

		// What the agent did with it.
		if itemTicket != nil {
			th.Steps = append(th.Steps, view.Line{
				Kind:   view.LineDecision,
				Text:   "派发",
				At:     t.At,
				Badges: []string{"#" + number(itemTicket.Number), shortWorkspace(string(itemTicket.Workspace)), stageWord(st)},
				Tone:   toneOf(st),
			})
		} else if item.Decision != nil && item.Decision.Kind == domain.DecPropose {
			// History only: what happened to this message. The approval itself is a
			// to-do, and to-dos live in 总览 with their detail (design §6.3).
			th.Steps = append(th.Steps, view.Line{
				Kind: view.LineDecision, Text: "需要你批准", At: t.At,
				Badges: []string{shortWorkspace(string(item.Decision.Workspace))}, Tone: view.ToneWarn,
			})
		} else {
			reason := "未受理"
			if len(item.Disp.Reason) > 0 {
				reason = reasonText(item.Disp.Reason[0].Code)
			}
			th.Steps = append(th.Steps, view.Line{
				Kind: view.LineDecision, Text: reason, At: t.At, Tone: view.ToneNeutral,
			})
		}

		// The operator's own line needs no reply plan line: there is nobody else to
		// answer, and "replied to the operator" would be noise.
		// A proposal card says everything the reply line would: that it waits for a
		// person and why. Two lines saying it is noise (design §6.3).
		hasProposal := item.Decision != nil && item.Decision.Proposal != nil
		if item.Decision != nil && !hasProposal && !t.Self && t.Source != "local" {
			th.Steps = append(th.Steps, replyLine(item))
		}

		if itemTicket != nil && itemTicket.Attention != nil {
			att := itemTicket.Attention
			th.Steps = append(th.Steps, view.Line{
				Kind: view.LineQuestion, At: att.At, Tone: view.ToneWarn, Text: att.Question,
			})
		} else if itemTicket != nil {
			lines := r.log.get(string(itemTicket.ID))
			if len(lines) == 0 {
				th.Steps = append(th.Steps, view.Line{
					Kind: view.LineNotice, At: now, Text: vm.PendingNote(), Tone: view.ToneNeutral,
				})
			}
			if folded := r.log.foldedCount(string(itemTicket.ID)); folded > 0 {
				// Say it: a transcript that quietly lost its beginning would make a
				// person think the task did less than it did.
				th.Steps = append(th.Steps, view.Line{Kind: view.LineNotice, At: now,
					Text: fmt.Sprintf("更早的 %d 条记录已折叠（内存只保留最近 %d 条）",
						folded, maxEventsPerTicket)})
			}
			for _, e := range lines {
				if l := workerLine(e, itemTicket, now); l.Kind != "" {
					th.Steps = append(th.Steps, l)
				}
			}

		}
		vm.Threads = append(vm.Threads, th)
	}
	return vm
}

// ticketFor finds the Ticket a FeedItem opened. A decided request has no
// Disp.TicketIDs yet (engine Wave2 keeps the link on the Ticket's origin and
// DecisionIDs), so the projection derives it instead of losing the row.
func ticketFor(item engine.FeedItem, tickets []domain.Ticket) *domain.Ticket {
	for i := range tickets {
		tk := &tickets[i]
		if tk.Origin == item.Trigger.ID {
			return tk
		}
		if item.Decision != nil {
			for _, id := range tk.DecisionIDs {
				if id == item.Decision.ID {
					return tk
				}
			}
		}
	}
	return nil
}

// replyLine says who the answer goes to, and why. A person must always be able to
// see "it decided to speak for me" without opening anything (design §4.10).
func replyLine(item engine.FeedItem) view.Line {
	rp := item.Decision.Reply
	who := item.Trigger.Actor.ID
	switch rp.Route {
	case domain.ReplyDirect:
		return view.Line{Kind: view.LineReply, At: item.Trigger.At, Tone: view.ToneOK,
			Text:   "应答：直接回复 " + who,
			Badges: []string{purposeWord(rp.Purpose), rp.AuthorizedBy}}
	case domain.ReplyOperator:
		return view.Line{Kind: view.LineReply, At: item.Trigger.At, Tone: view.ToneWarn,
			Text:   "应答：先通知你，暂不回复 " + who,
			Badges: []string{purposeWord(rp.Purpose), reasonWord(rp)}}
	case domain.ReplyBoth:
		return view.Line{Kind: view.LineReply, At: item.Trigger.At, Tone: view.ToneWarn,
			Text:   "应答：等你点头后回复 " + who,
			Badges: []string{purposeWord(rp.Purpose), rp.AuthorizedBy}}
	}
	return view.Line{Kind: view.LineReply, At: item.Trigger.At, Tone: view.ToneNeutral,
		Text: "应答：不回复，仅记录", Badges: []string{rp.AuthorizedBy}}
}

func purposeWord(p domain.ReplyPurpose) string {
	switch p {
	case domain.PurposeAck:
		return "确认收到"
	case domain.PurposeAnswer:
		return "回答"
	case domain.PurposeResult:
		return "交付结果"
	case domain.PurposeAttention:
		return "待你决定"
	}
	return string(p)
}

func reasonWord(rp domain.ReplyPlan) string {
	if len(rp.Reason) == 0 {
		return rp.AuthorizedBy
	}
	switch rp.Reason[0].Code {
	case "sensitive_workspace":
		return "数据外发需你批准"
	case "reply_mode_notify":
		return "notify 档：默认不代你说话"
	case "clarify":
		return "无法判断派给谁"
	}
	return rp.Reason[0].Code
}

// workerLine turns one recorded Worker step into a line of the stream.
func workerLine(e workerEvent, t *domain.Ticket, now time.Time) view.Line {
	switch e.kind {
	case agent.EventToolStart:
		// The end carries the same command plus the result, so one card per call.
		return view.Line{}
	case agent.EventToolEnd:
		// present and report are how a Worker talks to the domain; drawing them as
		// shell cards would suggest it ran a command (design §5.3).
		switch e.tool {
		case "present":
			return view.Line{Kind: view.LineArtifact, At: now,
				Text: jsonField(e.args, "title"), Badges: []string{jsonField(e.args, "kind")}}
		case "report":
			return view.Line{Kind: view.LineNotice, At: now,
				Text: "报告 " + jsonField(e.args, "state") + "：" + jsonField(e.args, "summary")}
		}
		return view.Line{Kind: view.LineTool, At: now, Tool: e.tool,
			Command: toolCommand(e.tool, e.args), ExitCode: 0, Output: splitLines(e.output, 6)}
	case "reply":
		return view.Line{Kind: view.LineReply, At: now, Text: e.text}
	case agent.EventUsage:
		if e.usage == nil {
			return view.Line{}
		}
		// Cache hits are shown next to the cost: they are the only observable sign
		// of whether the prompt prefix stayed stable across turns.
		text := fmt.Sprintf("用量 prompt=%d completion=%d total=%d",
			e.usage.PromptTokens, e.usage.CompletionTokens, e.usage.TotalTokens)
		if e.usage.PromptTokens > 0 {
			hit := 100 * e.usage.CachedTokens / e.usage.PromptTokens
			text += fmt.Sprintf(" · 缓存命中 %d%%（%d/%d）", hit, e.usage.CachedTokens, e.usage.PromptTokens)
		}
		return view.Line{Kind: view.LineNotice, At: now, Text: text}
	case agent.EventCompact:
		return view.Line{Kind: view.LineNotice, At: now, Text: e.compact}
	case agent.EventCanceled:
		return view.Line{Kind: view.LineNotice, At: now, Text: "执行被取消", Tone: view.ToneWarn}
	case agent.EventResume:
		return view.Line{Kind: view.LineNotice, At: now, Text: "从会话日志恢复"}
	case agent.EventError:
		return view.Line{Kind: view.LineNotice, At: now, Text: "执行出错：" + e.err, Tone: view.ToneDanger}
	case agent.EventText:
		if e.notice {
			return view.Line{Kind: view.LineNotice, At: now, Text: e.text}
		}
		return view.Line{Kind: view.LineProgress, At: now, Text: e.text}
	case agent.EventDone:
		return view.Line{Kind: view.LineNotice, At: now, Text: "执行完成"}
	}
	return view.Line{Kind: view.LineNotice, At: now, Text: e.text}
}

// toolCommand renders a tool call's most informative argument as the card's title.
func toolCommand(tool, args string) string {
	if args == "" {
		return tool
	}
	for _, key := range []string{"command", "path", "pattern"} {
		if v := jsonField(args, key); v != "" {
			return v
		}
	}
	return args
}

// jsonField reads one top-level string field without pulling in reflection.
// jsonField pulls one string value out of a tool's JSON arguments for display.
//
// It must decode JSON, not slice at the next quote: a Chinese summary arrives as
// \uXXXX escapes, and naive slicing showed the operator "\u5728 webapp \u5de5..."
// in the panel (seen in a live screenshot).
func jsonField(args, key string) string {
	var m map[string]any
	if err := json.Unmarshal([]byte(args), &m); err != nil {
		// Tool arguments are built by the model; if they are not valid JSON there is
		// nothing reliable to show, so show nothing rather than garbage.
		return ""
	}
	v, ok := m[key]
	if !ok {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(t)
	}
	return ""
}

func splitLines(s string, max int) []string {
	if s == "" {
		return nil
	}
	all := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(all) > max {
		all = append(all[:max], fmt.Sprintf("… 其余 %d 行", len(all)-max))
	}
	return all
}

// inboundLine labels one arrival: which Source, who, and what they said.
func inboundLine(t domain.Trigger) view.Line {
	who, source := t.Actor.ID, t.Source
	if t.Self {
		who = "你"
	}
	switch t.Source {
	case "local":
		who, source = "你", "本地面板"
	case "github":
		source = "GitHub"
		if t.Conversation.RootID != "" {
			source = "GitHub · " + t.Conversation.RootID
		}
	case "dingtalk":
		source = "钉钉"
	case "feishu":
		source = "飞书"
	}
	kind := view.LineInbound
	if t.Source == "local" {
		kind = view.LineOperator
	}
	return view.Line{Kind: kind, Who: who, Source: source, Text: t.Text, At: t.At}
}

// reportStage is the Ticket's stage, or "ended" for a nil Ticket.
func reportStage(t *domain.Ticket, now time.Time) string {
	if t == nil {
		return string(domain.StageEnded)
	}
	return string(domain.StageOf(t, now))
}

// reasonText turns a reason code into the one line the stream shows.
func reasonText(code string) string {
	switch code {
	case domain.ReasonNotification:
		return "判定为通知，不建工单"
	case domain.ReasonNotAddressed:
		return "没有明确对我说话，只作为上下文"
	case domain.ReasonNotAllowed:
		return "发送者不在允许名单"
	case domain.ReasonClarify:
		return "无法判断派给哪个工作区"
	}
	return "未受理"
}

// Dispatch submits a line the operator typed. Where it goes is the Router's job,
// so no workspace is passed: deciding that is the whole reason the agent exists.
func (r *Runtime) Dispatch(text string) error {
	r.mu.Lock()
	p := r.persist
	r.mu.Unlock()
	call := r.eng.Dispatch
	if p != nil {
		call = p.Dispatch
	}
	out, err := call(engine.DispatchInput{Text: text, At: r.clock()})
	if err != nil {
		r.setErr(err.Error())
		return err
	}
	// Receive only queues the Eval now; evaluating immediately is what makes the
	// panel's input feel like a command instead of a 2s-later batch. The periodic
	// pass still exists for Triggers that arrive from Sources.
	if outs, evalErr := r.EvaluateDue(r.clock()); evalErr != nil {
		return evalErr
	} else if q := questionFrom(outs); q != "" {
		err = fmt.Errorf("%s", q)
		r.setErr(err.Error())
		return err
	}
	if out.NeedsPerson {
		err = fmt.Errorf("%s", out.Question)
		r.setErr(err.Error())
		r.Publish()
		return err
	}
	r.setErr("")
	// The Ticket exists; the Worker picks it up in the background. Starting it
	// here keeps the panel responsive: Dispatch returns as soon as the domain
	// committed.
	r.mu.Lock()
	durable := r.persist != nil
	r.mu.Unlock()
	if out.Ticket != "" && !durable {
		r.startLocalRun(out.Ticket)
	}
	r.Publish()
	return nil
}

// Approve accepts a proposal: the Ticket is created now, with the approval on
// record.
func (r *Runtime) Approve(decisionID string) error {
	r.mu.Lock()
	p := r.persist
	r.mu.Unlock()
	call := r.eng.Approve
	if p != nil {
		call = p.Approve
	}
	out, err := call(domain.ID(decisionID))
	if err != nil {
		return err
	}
	if out.Ticket != "" {
		if t, ok := r.ticketSnapshot(out.Ticket); ok {
			if err := r.wakeTicket(t); err != nil {
				r.slogger().Warn("approved ticket could not be launched", "ticket", t.Number, "error", err)
			}
		}
	}
	r.Publish()
	return nil
}

// Reject turns a proposal down.
func (r *Runtime) Reject(decisionID string) error {
	r.mu.Lock()
	p := r.persist
	r.mu.Unlock()
	call := r.eng.Reject
	if p != nil {
		call = p.Reject
	}
	if err := call(domain.ID(decisionID), "rejected in the panel"); err != nil {
		return err
	}
	r.Publish()
	return nil
}

// LastError returns the last dispatch problem, or "".
func (r *Runtime) LastError() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.lastErr
}

// Workspaces returns the choices the composer offers.
func (r *Runtime) Workspaces() []view.WorkspaceChoice {
	ws := r.eng.Workspaces()
	out := make([]view.WorkspaceChoice, 0, len(ws))
	for _, w := range ws {
		out = append(out, view.WorkspaceChoice{
			ID: string(w.ID), Name: w.Name, Purpose: firstLine(w.Brief.Purpose),
		})
	}
	return out
}

func (r *Runtime) setErr(s string) {
	r.mu.Lock()
	r.lastErr = s
	r.mu.Unlock()
}

func isPersonStage(s domain.Stage) bool {
	switch s {
	case domain.StagePersonAnswer, domain.StagePersonReview,
		domain.StagePersonBlocked, domain.StagePersonFailed, domain.StagePersonDelivery:
		return true
	}
	return false
}

func waitingFor(s domain.Stage, t *domain.Ticket) string {
	switch s {
	case domain.StagePersonAnswer:
		if t.Attention != nil {
			return t.Attention.Question
		}
		return "等待你的回答"
	case domain.StagePersonReview:
		return "结果已就绪，等待你评审"
	case domain.StagePersonBlocked:
		// A person deciding what to do next needs the reason, not the category:
		// "受阻" alone gives them nothing to act on (design §6.10).
		return "受阻：" + reportDetail(t, "需要你处理")
	case domain.StagePersonFailed:
		return "失败：" + reportDetail(t, "需要你处理")
	case domain.StagePersonDelivery:
		return "输入投递失败，将自动重试"
	}
	return "待处理"
}

// reportDetail is the Ticket's own explanation of why it stopped, shortened to one
// line. A missing report falls back to the generic phrase.
func reportDetail(t *domain.Ticket, fallback string) string {
	rep := t.LastReport()
	if rep == nil || strings.TrimSpace(rep.Summary) == "" {
		return fallback
	}
	return clipLine(rep.Summary, 90)
}

// clipLine shortens text to one readable line.
func clipLine(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}

// stageWord says what a row is doing, in a person's words.
func stageWord(s domain.Stage) string {
	switch s {
	case domain.StageWorking:
		return "执行中"
	case domain.StageWorkingQueued:
		return "排队中"
	case domain.StageIdle:
		return "等待触发"
	case domain.StageEnded:
		return "已结束"
	case domain.StagePersonReview:
		return "等待你评审"
	case domain.StagePersonAnswer:
		return "等待你回答"
	}
	return "进行中"
}

func toneOf(s domain.Stage) view.Tone {
	switch s {
	case domain.StagePersonAnswer:
		return view.ToneWarn
	case domain.StagePersonBlocked, domain.StagePersonFailed:
		return view.ToneDanger
	case domain.StagePersonReview:
		return view.ToneInfo
	}
	return view.ToneNeutral
}

// waitingLabel is the badge: whom the row waits for, never a status code.
func waitingLabel(s domain.Stage) string {
	switch s {
	case domain.StagePersonAnswer:
		return "等待回答"
	case domain.StagePersonReview:
		return "等待评审"
	case domain.StagePersonBlocked:
		return "受阻"
	case domain.StagePersonFailed:
		return "失败"
	case domain.StagePersonDelivery:
		return "投递失败"
	}
	return "待处理"
}

func number(n int) string {
	if n == 0 {
		return "—"
	}
	return fmt.Sprint(n)
}

func shortWorkspace(ws string) string {
	return strings.TrimPrefix(ws, "ws_")
}

func firstLine(s string) string {
	for i, r := range s {
		if r == '\n' || r == '。' {
			return s[:i]
		}
	}
	return s
}

// modelHealth says whether the executor can actually do anything. A green
// "执行器" while no model is configured is the worst kind of health card: it looks
// healthy right up to the moment work silently parks (design §5.7).
func (r *Runtime) modelHealth() []view.HealthItem {
	cfg := r.workerConfig()
	if cfg == nil {
		return []view.HealthItem{{Name: "模型", Detail: "未接入执行器", OK: true}}
	}
	if cfg.BaseURL == "" || cfg.Model == "" {
		return []view.HealthItem{{Name: "模型", Detail: "未配置，工单会挂起", OK: false}}
	}
	return []view.HealthItem{{Name: "模型", Detail: cfg.Model, OK: true}}
}

// recordUsage adds one model call to the running total. The panel says "since
// start" because that is what it can honestly claim: the counter is in memory and
// a restart resets it (design §5.4).
func (r *Runtime) recordUsage(u agent.Usage) {
	r.mu.Lock()
	r.usage.Prompt += u.PromptTokens
	r.usage.Completion += u.CompletionTokens
	r.usage.Total += u.TotalTokens
	r.usage.Cached += u.CachedTokens
	r.usage.Calls++
	r.mu.Unlock()
}

// usageSnapshot reads the totals for a projection.
func (r *Runtime) usageSnapshot() view.Usage {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.usage
}
