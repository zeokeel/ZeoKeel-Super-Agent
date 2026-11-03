package runtime

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/zeokeel/super-agent/internal/domain"
	"github.com/zeokeel/super-agent/internal/engine"
	"github.com/zeokeel/super-agent/internal/store"
	"github.com/zeokeel/super-agent/internal/view"
)

// SourceStatus is what a source driver reports for the 来源 page. The runtime does
// not poll by itself: whoever owns the driver calls SetSourceStatus after a round,
// so the page shows the driver's own facts rather than a guess.
type SourceStatus struct {
	Kind    string
	Name    string
	Account string
	Enabled bool
	Facts   []view.Fact
	Cursor  string
	At      time.Time
	Last    view.PollResult
}

// SetSourceStatus upserts one or more sources and republishes. It merges by
// kind+name instead of replacing: with two Sources configured, a poll from one of
// them must not erase the other from the page.
func (r *Runtime) SetSourceStatus(rows ...SourceStatus) {
	r.mu.Lock()
	for _, row := range rows {
		replaced := false
		for i := range r.sources {
			if r.sources[i].Kind == row.Kind && r.sources[i].Name == row.Name {
				r.sources[i] = row
				replaced = true
				break
			}
		}
		if !replaced {
			r.sources = append(r.sources, row)
		}
	}
	r.mu.Unlock()
	r.Publish()
}

// RemoveSourceStatus forgets a source, for a driver that was switched off. Without
// it a disabled source would keep showing its last "poll ok" forever.
func (r *Runtime) RemoveSourceStatus(kind, name string) {
	r.mu.Lock()
	out := r.sources[:0]
	for _, s := range r.sources {
		if s.Kind == kind && s.Name == name {
			continue
		}
		out = append(out, s)
	}
	r.sources = out
	r.mu.Unlock()
	r.Publish()
}

// SourceRows is the read-only 来源 projection for one frame. Status/tests use it
// without going through the snapshot; the UI gets the same rows from Publish.
func (r *Runtime) SourceRows(now time.Time) []view.SourceRow {
	return r.sourcesVM(now).Sources
}

// sourcesVM builds the 来源 page.
func (r *Runtime) sourcesVM(now time.Time) *view.SourceVM {
	r.mu.Lock()
	rows := append([]SourceStatus(nil), r.sources...)
	r.mu.Unlock()

	vm := &view.SourceVM{UpdatedAt: now}
	if len(rows) == 0 {
		// An honest empty state: nothing is listening yet, and the page says so
		// rather than showing an empty table.
		return vm
	}
	for _, s := range rows {
		vm.Sources = append(vm.Sources, view.SourceRow{
			Kind: s.Kind, Name: s.Name, Account: s.Account, Enabled: s.Enabled,
			Facts: s.Facts,
			// The cursor is an opaque driver token ("owner/name|2026-11-03T15:57Z");
			// showing it raw in a Chinese panel tells a person nothing. The page says
			// whether it advanced; the raw value stays in the driver's logs.
			Cursor:   cursorLabel(s.Cursor),
			CursorAt: s.At, LastPoll: s.Last,
		})
	}
	return vm
}

// threadsVM groups the feed by Thread so the 触发 page can show one Thread with its
// timeline, which is what Eval actually reasons about.
func (r *Runtime) threadsVM(now time.Time) *view.ThreadVM {
	vm := &view.ThreadVM{Details: map[string]*view.ThreadDetail{}, UpdatedAt: now}
	feed := r.eng.Feed()
	tickets := r.ticketSnapshots()

	type group struct {
		key   string
		items []engine.FeedItem
	}
	order := []string{}
	groups := map[string]*group{}
	for _, item := range feed {
		key := threadKeyOf(item.Trigger)
		g, ok := groups[key]
		if !ok {
			g = &group{key: key}
			groups[key] = g
			order = append(order, key)
		}
		g.items = append(g.items, item)
	}

	for _, key := range order {
		g := groups[key]
		last := g.items[len(g.items)-1]
		t := last.Trigger
		detail := &view.ThreadDetail{ID: key}
		var ticketID string
		for i := len(g.items) - 1; i >= 0; i-- {
			it := g.items[i]
			itTicket := ticketFor(it, tickets)
			if itTicket != nil && ticketID == "" {
				ticketID = fmt.Sprintf("#%d", itTicket.Number)
			}
			ev := view.ThreadEvent{At: it.Trigger.At, Title: clip(it.Trigger.Text, 80)}
			if itTicket != nil {
				ev.Kind = view.EventTrigger
				ev.Decision = fmt.Sprintf("派发 → #%d %s", itTicket.Number, itTicket.Workspace)
			} else if it.Decision != nil && it.Decision.Kind == domain.DecPropose {
				ev.Kind = view.EventDecision
				ev.Decision = "待你批准"
			} else {
				ev.Kind = view.EventDisposition
				if len(it.Disp.Reason) > 0 {
					ev.Code = it.Disp.Reason[0].Code
				}
			}
			detail.Events = append(detail.Events, ev)
		}
		sort.Slice(detail.Events, func(i, j int) bool {
			return detail.Events[i].At.After(detail.Events[j].At)
		})
		detail.TicketID = ticketID
		detail.Subscribed = ticketID != ""
		vm.Details[key] = detail
		vm.Threads = append(vm.Threads, view.ThreadRow{
			ID: key, Title: clip(t.Text, 60), Source: t.Source, SourceIcon: t.Source,
			Who: t.Actor.ID, Workspace: string(t.DefaultWorkspace),
			Stage: stageWordOf(t), TicketID: ticketID, Subscribed: ticketID != "",
			ActivityAt: t.At, EventCount: len(detail.Events),
		})
	}
	sort.Slice(vm.Threads, func(i, j int) bool {
		return vm.Threads[i].ActivityAt.After(vm.Threads[j].ActivityAt)
	})
	for _, th := range vm.Threads {
		if th.Subscribed {
			vm.Subscribed++
		}
	}
	return vm
}

// decisionsVM builds the 评估 page from the same decisions the stream shows.
func (r *Runtime) decisionsVM(now time.Time) *view.DecisionVM {
	vm := &view.DecisionVM{UpdatedAt: now, PolicyVersion: r.policyVersion()}
	counts := map[string]int{}
	feed := r.eng.Feed()
	tickets := r.ticketSnapshots()
	totals := evalTotalsFrom(feed, tickets)
	for _, item := range feed {
		if item.Decision == nil {
			continue
		}
		d := item.Decision
		itTicket := ticketFor(item, tickets)
		code, text := "", ""
		if len(d.Reasons) > 0 {
			code = d.Reasons[0].Code
			// The code is the audit trail; the template is what a person reads.
			text = view.ReasonText(code)
		}
		summary := ""
		if itTicket != nil {
			summary = fmt.Sprintf("新建 Ticket #%d · %s", itTicket.Number, itTicket.Workspace)
		} else if d.Kind == domain.DecPropose {
			summary = "待你批准 · " + string(d.Workspace)
		}
		label := kindLabel(string(d.Kind))
		counts[label]++
		vm.Rows = append(vm.Rows, view.DecisionRow{
			ID: string(d.ID), Kind: string(d.Kind), KindLabel: label,
			Trigger: clip(item.Trigger.Text, 60), Summary: summary,
			ReasonCode: code, ReasonText: text, Workspace: string(d.Workspace),
			Tone: toneOfDecision(string(d.Kind)), At: item.Trigger.At,
		})
	}
	sort.Slice(vm.Rows, func(i, j int) bool { return vm.Rows[i].At.After(vm.Rows[j].At) })
	for kind, n := range counts {
		vm.Kinds = append(vm.Kinds, view.DecisionKindCount{Kind: kind, Label: kind, Count: n})
	}
	sort.Slice(vm.Kinds, func(i, j int) bool { return vm.Kinds[i].Kind < vm.Kinds[j].Kind })
	// The four totals come from four different facts, so they cannot be equal by
	// construction: Evaluated counts Triggers an Eval judged, Decisions counts the
	// Decisions produced, Filtered counts gate drops, Tickets counts the Tickets
	// that actually opened. A batch Eval or a filtered Trigger makes them differ.
	vm.Counts = view.EvalCounts{
		Evaluated: totals.assessed,
		Decisions: len(vm.Rows),
		Filtered:  totals.filtered,
		Tickets:   totals.tickets,
	}
	return vm
}

// policyVersion is the version stamp of the policy the Decisions were made
// under. It is read from the workspace rows (the same JSON the engine loaded), so
// the page can say which ruleset it is looking at after a restart.
func (r *Runtime) policyVersion() string {
	r.mu.Lock()
	st := r.settingsStore
	r.mu.Unlock()
	if st == nil {
		return ""
	}
	policies, err := st.WorkspacePolicies()
	if err != nil {
		return ""
	}
	seen := map[string]bool{}
	for _, raw := range policies {
		var p struct {
			Version string
		}
		if json.Unmarshal([]byte(raw), &p) == nil && strings.TrimSpace(p.Version) != "" {
			seen[p.Version] = true
		}
	}
	if len(seen) == 0 {
		return ""
	}
	out := make([]string, 0, len(seen))
	for v := range seen {
		out = append(out, v)
	}
	sort.Strings(out)
	return strings.Join(out, ", ")
}

// ticketsVM builds the 执行 page. Buckets come from view.BucketOf so the chips and
// the row counts cannot disagree; rows are sorted by recent activity and carry the
// full detail projection the expanded row draws (design §6.10).
func (r *Runtime) ticketsVM(now time.Time) *view.TicketListVM {
	vm := &view.TicketListVM{
		Details:   map[string]view.TicketDetail{},
		DetailVMs: map[string]*view.TicketDetailVM{},
		UpdatedAt: now,
	}
	counts := map[view.TicketBucket]int{}
	type rowAt struct {
		row view.TicketRow
		at  time.Time
	}
	var rows []rowAt
	for _, t := range r.ticketSnapshots() {
		st := string(domain.StageOf(&t, now))
		bucket := view.BucketOf(st, len(t.Assignments) > 0)
		counts[bucket]++
		last, activity := ticketActivity(t, now)
		rows = append(rows, rowAt{
			at: last,
			row: view.TicketRow{
				ID: fmt.Sprint(t.Number), Title: t.Title, Workspace: string(t.Workspace),
				Source: t.Mandate.Actor.ID, SourceIcon: "local",
				Stage: stageWordOfTicket(st), StageID: st,
				Detail: humanAge(now.Sub(t.CreatedAt)) + "前派发",
				Bucket: bucket, Activity: activity,
				Tone: toneOfStage(st), Age: now.Sub(t.CreatedAt),
			},
		})
		vm.DetailVMs[string(t.ID)] = r.ticketDetailVM(t, now)
	}
	// Most recent activity first: a Ticket that just reported must not sit below
	// one created this morning.
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].at.After(rows[j].at) })
	for _, r := range rows {
		vm.Rows = append(vm.Rows, r.row)
	}
	vm.Total = len(vm.Rows)
	for _, b := range view.BucketOrder() {
		vm.Buckets = append(vm.Buckets, view.BucketCount{Bucket: b, Count: counts[b]})
	}
	return vm
}

// ticketActivity is "最近活动": the newest observable fact about the Ticket and the
// phrase the list shows for it. Working Tickets say how long they have run; others
// say when they last moved.
func ticketActivity(t domain.Ticket, now time.Time) (time.Time, string) {
	last := t.CreatedAt
	phrase := "刚刚派发"
	note := func(at time.Time, text string) {
		if at.After(last) {
			last = at
			phrase = text
		}
	}
	for _, in := range t.Inputs {
		note(in.At, "收到新输入")
	}
	for _, as := range t.Assignments {
		note(as.StartedAt, "开始执行")
		note(as.EndedAt, "执行结束")
		if as.Report != nil {
			note(as.Report.At, "报告"+reportWord(as.Report.State))
		}
	}
	if t.Attention != nil {
		note(t.Attention.At, "等待你回答")
	}
	if t.Ended != nil {
		note(t.Ended.At, "已结束")
	}
	if st := domain.StageOf(&t, now); st == domain.StageWorking {
		if as := t.LastAssignment(); !as.StartedAt.IsZero() {
			return last, "已运行 " + humanDuration(now.Sub(as.StartedAt))
		}
	}
	return last, humanAge(now.Sub(last)) + "前" + phrase
}

// reportWord says a Report state in one word for the activity line.
func reportWord(s domain.ReportState) string {
	switch s {
	case domain.ReportProgress:
		return "进度"
	case domain.ReportWaiting:
		return "等待"
	case domain.ReportReady:
		return "就绪"
	case domain.ReportBlocked:
		return "受阻"
	case domain.ReportFailed:
		return "失败"
	case domain.ReportDropped:
		return "丢弃"
	}
	return ""
}

// ticketDetailVM is the expanded row: authorization, execution history, delivery
// facts and deliverables, each from the Ticket's own copy.
func (r *Runtime) ticketDetailVM(t domain.Ticket, now time.Time) *view.TicketDetailVM {
	_, activity := ticketActivity(t, now)
	d := &view.TicketDetailVM{
		ID: fmt.Sprint(t.Number), Title: t.Title, Workspace: string(t.Workspace),
		Stage: string(domain.StageOf(&t, now)), StageLabel: stageWordOfTicket(string(domain.StageOf(&t, now))),
		Tone:   toneOfStage(string(domain.StageOf(&t, now))),
		Source: t.Mandate.Basis.Source, Actor: t.Mandate.Actor.ID,
		Detail: reportDetail(&t, "由你派发"), LastActivity: activity, UpdatedAt: now,
	}
	if tr, ok := r.eng.Trigger(t.Origin); ok {
		d.Thread = threadKeyOf(tr)
	}
	if len(t.SubscribedThreads) > 0 && d.Thread == "" {
		d.Thread = t.SubscribedThreads[0]
	}
	d.Mandate = &view.MandateDetail{
		Strength: string(t.Mandate.Strength), PolicyVersion: t.Mandate.PolicyVersion,
		Rules: t.Mandate.Rules, Actor: t.Mandate.Actor.ID,
		Basis: string(t.Mandate.Basis.Via), Scope: string(t.Mandate.Scope),
		Workspace: string(t.Mandate.Workspace), At: t.CreatedAt,
	}
	if t.Attention != nil {
		d.Attention = &view.TicketAttention{
			Question: t.Attention.Question, Options: t.Attention.Options, AskedAt: t.Attention.At,
		}
	}
	for _, as := range t.Assignments {
		ad := view.AssignmentDetail{
			ID: string(as.ID), Executor: as.SessionID, State: string(as.State),
			StartedAt: as.StartedAt, EndedAt: as.EndedAt,
		}
		if as.Report != nil {
			ad.Report = &view.ReportDetail{
				State: string(as.Report.State), Summary: as.Report.Summary, At: as.Report.At,
			}
		}
		d.Assignments = append(d.Assignments, ad)
	}
	for _, in := range t.Inputs {
		d.Inputs = append(d.Inputs, view.InputDetail{
			ID: string(in.ID), Kind: string(in.Kind), Text: in.Text, At: in.At,
			Delivered: in.Delivered, Read: in.Read,
		})
	}
	for _, f := range t.Follows {
		d.Follows = append(d.Follows, view.FollowDetail{
			Thread: f.Thread, Own: f.Own, ClosedAt: f.ClosedAt, Outcome: f.Outcome,
		})
	}
	for _, a := range t.Artifacts {
		d.Artifacts = append(d.Artifacts, view.Artifact{
			Kind: string(a.Kind), Title: a.Title, Detail: a.Detail,
		})
	}
	if t.Delivery != nil {
		d.Delivery = &view.DeliveryDetail{
			Attempts: t.Delivery.Failed, LastError: t.Delivery.LastError, At: t.Delivery.At,
		}
	}
	return d
}

// settingsVM builds the 设置 page from the values the operator saved.
func (r *Runtime) settingsVM(now time.Time) *view.SettingsVM {
	r.mu.Lock()
	values := map[string]string{}
	for k, v := range r.settings {
		values[k] = v
	}
	rev := r.settingsRev
	r.mu.Unlock()

	pick := func(group string, keys ...string) []view.SettingField {
		out := make([]view.SettingField, 0, len(keys))
		for _, k := range keys {
			out = append(out, view.SettingField{Key: k, Label: labelOfKey(k), Value: values[k], Kind: view.SettingText})
		}
		return out
	}
	return &view.SettingsVM{
		Revision: rev, UpdatedAt: now, Source: r.settingsSource(),
		Groups: []view.SettingsGroup{
			{ID: view.SettingsSource, Label: "来源",
				// Only knobs that actually change behaviour are shown: a setting that
				// saves and does nothing teaches people not to trust the page.
				Note: "只列真正生效的项。GitHub 走 gh CLI 登录；IM 的 app secret 存在本机 SQLite。" +
					"留空的 IM 不会监听（不会假装在线）。",
				Fields: []view.SettingField{
					{Key: "poll_interval", Label: labelOfKey("poll_interval"), Value: values["source.poll_interval"], Kind: view.SettingText},
					{Key: "feishu_app_id", Label: "飞书 App ID", Value: values["source.feishu_app_id"], Kind: view.SettingText},
					{Key: "feishu_app_secret", Label: "飞书 App Secret", Value: values["source.feishu_app_secret"], Kind: view.SettingSecret},
					{Key: "feishu_chats", Label: "飞书会话 ID（逗号分隔）", Value: values["source.feishu_chats"], Kind: view.SettingText},
					{Key: "dingtalk_app_key", Label: "钉钉 AppKey", Value: values["source.dingtalk_app_key"], Kind: view.SettingText},
					{Key: "dingtalk_app_secret", Label: "钉钉 AppSecret", Value: values["source.dingtalk_app_secret"], Kind: view.SettingSecret},
					{Key: "dingtalk_robot", Label: "钉钉机器人 RobotCode", Value: values["source.dingtalk_robot"], Kind: view.SettingText},
					{Key: "im_self", Label: "机器人自己的账号（@ 判定）", Value: values["source.im_self"], Kind: view.SettingText},
					{Key: "github_repo", Label: "GitHub 仓库 owner/name", Value: values["source.github_repo"], Kind: view.SettingText},
					{Key: "github_login", Label: "GitHub 机器人登录名", Value: values["source.github_login"], Kind: view.SettingText},
					// Observe/CI filters are enforced by the GitHub Source (not this
					// scope yet): show them as facts so nobody edits a knob that saves
					// and does nothing.
					{Key: "github_observe", Label: "只观察（不建单）", Value: "由 GitHub Source 实现，尚未接线", Kind: view.SettingFact},
					{Key: "github_ci", Label: "接收 CI 事件", Value: "由 GitHub Source 实现，尚未接线", Kind: view.SettingFact},
				}},
			{ID: view.SettingsExecution, Label: "执行",
				Fields: []view.SettingField{
					{Key: "concurrency", Label: labelOfKey("concurrency"), Value: values["execution.concurrency"], Kind: view.SettingText},
					{Key: "command_timeout", Label: labelOfKey("command_timeout"), Value: values["execution.command_timeout"], Kind: view.SettingText},
					{Key: "model_timeout", Label: labelOfKey("model_timeout"), Value: values["execution.model_timeout"], Kind: view.SettingText},
					{Key: "prepare", Label: "工单准备命令（首次执行前）", Value: values["execution.prepare"], Kind: view.SettingText,
						Help: "sh -c 在 execution root（工单目录的父目录）执行；成功必须创建工单目录；失败则工单失败（不自动重试）"},
					{Key: "background_timeout", Label: "后台作业上限（秒）", Value: values["execution.background_timeout"], Kind: view.SettingText,
						Help: "默认 1800（30 分钟）：到点仍在运行的后台作业会被终止，工单转为等待并记录最后输出"},
				}},
			{ID: view.SettingsPeople, Label: "允许名单",
				Note:   "只有这里列出的人可以让它干活；你自己不受限制。改完立即生效。",
				Fields: pick("people", "allowlist")},
			{ID: view.SettingsEval, Label: "评估", Note: "只读事实；规则与受理档位的编辑在下一次 UI 迭代接入。", Fields: r.evalFacts()},
			{ID: view.SettingsPrompt, Label: "提示词", Placeholder: "提示词编辑在「提示词」页；这里保留入口。"},
			{ID: view.SettingsDeploy, Label: "部署", Note: "只读事实：日志、备份与 schema 由运行时维护。", Fields: r.deployFacts()},
		},
	}
}

// SaveSettings implements the panel's optional SettingsSaver. It commits to the
// store when one is attached, so a prompt edited today is still there tomorrow;
// without a store it is memory-only and the page says so.
func (r *Runtime) SaveSettings(group string, values map[string]string) (uint64, error) {
	if group == "" {
		return 0, fmt.Errorf("runtime: a settings group is required")
	}
	if group == string(view.SettingsPrompt) {
		// Validation happens before anything is written: unknown layer, unknown
		// placeholder, over-long text and missing required placeholders are refused,
		// never truncated. Identical values are dropped (no write, no history).
		filtered, verr := r.preparePromptSave(values)
		if verr != nil {
			r.mu.Lock()
			rev := r.settingsRev
			r.mu.Unlock()
			return rev, verr
		}
		values = filtered
	}
	r.mu.Lock()
	if r.settings == nil {
		r.settings = map[string]string{}
	}
	// The caller's revision is compared against the store inside the save
	// transaction. The in-memory check below only catches the same-process case
	// early, before any work is done.
	baseStr, hasBase := values["__revision"]
	base, _ := strconv.ParseUint(baseStr, 10, 64)
	if hasBase && base != r.settingsRev {
		rev := r.settingsRev
		r.mu.Unlock()
		return rev, fmt.Errorf("runtime: 设置已被其他窗口改过（当前 rev %d），请重新加载", rev)
	}
	for k, v := range values {
		if k == "__revision" {
			continue
		}
		r.settings[group+"."+k] = v
	}
	snapshot := make(map[string]string, len(r.settings))
	for k, v := range r.settings {
		snapshot[k] = v
	}
	st := r.settingsStore
	r.settingsRev++
	rev := r.settingsRev
	r.mu.Unlock()

	if st != nil {
		savedRev, err := st.SaveSettings(snapshot, base, hasBase)
		if err != nil {
			if errors.Is(err, store.ErrSettingsConflict) {
				return rev, fmt.Errorf("runtime: 设置已被其他窗口改过，请重新加载")
			}
			return rev, fmt.Errorf("runtime: 设置保存失败: %w", err)
		}
		// Adopt the revision the store assigned: another window may have written in
		// between, and our counter has to follow the file, not the process.
		r.mu.Lock()
		r.settingsRev = savedRev
		rev = savedRev
		r.mu.Unlock()
	}
	if group == string(view.SettingsPrompt) {
		// Version history + the default hash that powers "Default updated".
		r.recordPromptMeta(values, rev)
	}
	if group == string(view.SettingsPeople) {
		// Access control is not a display setting: apply it now, or the page would
		// say "saved" while the agent still refuses the person you just added.
		if err := r.applyAllowlist(values["allowlist"]); err != nil {
			return rev, err
		}
	}
	r.Publish()
	return rev, nil
}

// LoadSettings reads saved settings at startup. A failure is reported, not fatal:
// losing preferences should not stop the agent from running.
func (r *Runtime) LoadSettings(st *store.Store) error {
	if st == nil {
		return nil
	}
	values, err := st.Settings()
	if err != nil {
		return err
	}
	r.mu.Lock()
	r.settingsStore = st
	if r.settings == nil {
		r.settings = map[string]string{}
	}
	for k, v := range values {
		r.settings[k] = v
	}
	// The revision is read from the store, so a second window (or a restart) sees
	// the same number and an optimistic save can detect a conflict (design §6.5).
	rev, revErr := st.SettingsRevision()
	if revErr == nil {
		r.settingsRev = rev
	}
	r.mu.Unlock()
	r.Publish()
	return nil
}

func humanAge(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "刚刚"
	case d < time.Hour:
		return fmt.Sprintf("%d 分", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d 时", int(d.Hours()))
	}
	return fmt.Sprintf("%d 天", int(d.Hours()/24))
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func kindLabel(kind string) string {
	switch kind {
	case string(domain.DecPropose):
		return "待批准"
	case string(domain.DecAccept):
		return "已接受"
	case string(domain.DecStop):
		return "停止"
	case string(domain.DecIgnore):
		return "忽略"
	case string(domain.DecUnavailable):
		return "未完成"
	}
	return kind
}

func toneOfDecision(kind string) view.Tone {
	switch kind {
	case string(domain.DecPropose):
		return view.ToneWarn
	case string(domain.DecAccept):
		return view.ToneOK
	case string(domain.DecIgnore):
		return view.ToneNeutral
	}
	return view.ToneDanger
}

func stageWordOf(t domain.Trigger) string {
	if t.Self {
		return "你自己"
	}
	return "同事"
}

func stageWordOfTicket(st string) string {
	switch st {
	case "ended":
		return "已结束"
	case "person/answer":
		return "等待回答"
	case "person/review":
		return "等待评审"
	case "person/blocked":
		return "受阻"
	case "person/failed":
		return "失败"
	case "working":
		return "执行中"
	case "working/queued":
		return "排队中"
	case "idle":
		return "等待触发"
	}
	return st
}

func toneOfStage(st string) view.Tone {
	switch st {
	case "person/answer", "person/review", "person/delivery":
		return view.ToneWarn
	case "person/blocked", "person/failed":
		return view.ToneDanger
	case "working":
		return view.ToneInfo
	}
	return view.ToneNeutral
}

func labelOfKey(k string) string {
	switch k {
	case "poll_interval":
		return "轮询间隔（秒）"

	case "allowlist":
		return "来源:账号（逗号分隔）"
	case "concurrency":
		return "并发执行器"
	case "command_timeout":
		return "命令超时（秒）"
	case "model_timeout":
		return "模型响应超时（秒）"
	}
	return k
}

// threadKeyOf is the Thread a Trigger belongs to, as the 触发 page groups them.
func threadKeyOf(t domain.Trigger) string {
	if len(t.Threads) == 0 {
		return string(t.Source)
	}
	return t.Threads[0].Source + " · " + t.Threads[0].ID
}

// deployFacts is the read-only deployment group: where the logs and backups are,
// which schema the file carries, and what the status exit codes mean. They are
// facts, not editable settings, so the group uses view.SettingFact.
func (r *Runtime) deployFacts() []view.SettingField {
	r.mu.Lock()
	dir := r.stateDir
	st := r.settingsStore
	r.mu.Unlock()
	logs, backups, schema := "—", "—", "内存模式"
	if dir != "" {
		logs = filepath.Join(dir, "logs", "zeokeel.log")
		backups = filepath.Join(dir, "backups")
	}
	if st != nil {
		schema = fmt.Sprintf("SQLite v%d", store.SchemaVersion)
	}
	return []view.SettingField{
		{Key: "log", Label: "日志", Value: logs, Kind: view.SettingFact},
		{Key: "backup", Label: "备份目录", Value: backups, Kind: view.SettingFact},
		{Key: "schema", Label: "数据库", Value: schema, Kind: view.SettingFact},
		{Key: "status", Label: "status 退出码", Value: "0 正常 · 1 可重启修复 · 2 组件错误 · 3 等待 setup", Kind: view.SettingFact},
	}
}

// evalFacts is the read-only Eval group: the policy version, accept/reply mode,
// approval gate and enabled team rules, read from the workspace rows the engine
// loaded. Editing is not wired yet, so the fields are facts, not fake knobs.
func (r *Runtime) evalFacts() []view.SettingField {
	r.mu.Lock()
	st := r.settingsStore
	r.mu.Unlock()
	if st == nil {
		return []view.SettingField{{Key: "state", Label: "评估配置", Value: "内存模式（未持久化）", Kind: view.SettingFact}}
	}
	policies, err := st.WorkspacePolicies()
	if err != nil || len(policies) == 0 {
		return []view.SettingField{{Key: "state", Label: "评估配置", Value: "还没有工作区策略", Kind: view.SettingFact}}
	}
	var (
		versions  []string
		accept    = map[string]bool{}
		reply     = map[string]bool{}
		approval  bool
		rules     []string
		admission int
	)
	seen := map[string]bool{}
	for _, raw := range policies {
		var p struct {
			Version         string         `json:"Version"`
			AcceptMode      string         `json:"AcceptMode"`
			ReplyMode       string         `json:"ReplyMode"`
			RequireApproval bool           `json:"RequireApproval"`
			Admissions      map[string]any `json:"Admissions"`
			Rules           []domain.Rule  `json:"Rules"`
		}
		if json.Unmarshal([]byte(raw), &p) != nil {
			continue
		}
		if p.Version != "" && !seen["v:"+p.Version] {
			seen["v:"+p.Version] = true
			versions = append(versions, p.Version)
		}
		accept[string(p.AcceptMode)] = true
		reply[string(p.ReplyMode)] = true
		if p.RequireApproval {
			approval = true
		}
		admission += len(p.Admissions)
		for _, rule := range p.Rules {
			if rule.Enabled && !seen["r:"+rule.ID] {
				seen["r:"+rule.ID] = true
				rules = append(rules, rule.ID)
			}
		}
	}
	sort.Strings(versions)
	sort.Strings(rules)
	facts := []view.SettingField{
		{Key: "policy", Label: "Policy 版本", Value: strings.Join(versions, ", "), Kind: view.SettingFact},
		{Key: "accept", Label: "受理档位 accept mode", Value: joinSet(accept, "observe"), Kind: view.SettingFact},
		{Key: "reply", Label: "回复档位 reply mode", Value: joinSet(reply, "notify"), Kind: view.SettingFact},
		{Key: "approval", Label: "审批门 RequireApproval", Value: boolWord(approval), Kind: view.SettingFact},
		{Key: "rules", Label: "启用的团队规则", Value: orNone(strings.Join(rules, ", ")), Kind: view.SettingFact},
		{Key: "admission", Label: "admission 单元格", Value: fmt.Sprintf("%d 条", admission), Kind: view.SettingFact},
	}
	return facts
}

func joinSet(m map[string]bool, def string) string {
	var out []string
	for k := range m {
		if k != "" {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	if len(out) == 0 {
		return def
	}
	return strings.Join(out, ", ")
}

func boolWord(b bool) string {
	if b {
		return "开启"
	}
	return "关闭"
}

func orNone(s string) string {
	if s == "" {
		return "（无）"
	}
	return s
}

// settingsSource names where the values live, so the page never implies a
// persistence it does not have.
func (r *Runtime) settingsSource() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.settingsStore != nil {
		return "SQLite · kv"
	}
	return "内存（未接数据库）"
}

// cursorLabel turns an opaque driver cursor into a phrase the page can print.
func cursorLabel(raw string) string {
	if raw == "" {
		return "尚未开始"
	}
	return "已推进"
}

// applyAllowlist replaces the operator allowlist on every workspace. The list is
// workspace policy in the domain model, but a person manages it once: spreading it
// per workspace would make the panel another place to forget about.
func (r *Runtime) applyAllowlist(raw string) error {
	allowed, err := parseAllowlist(raw)
	if err != nil {
		return err
	}
	r.mu.Lock()
	p := r.persist
	r.mu.Unlock()
	for _, w := range r.eng.Workspaces() {
		policy := engine.Policy{Version: "v1", Allowed: allowed}
		if p != nil {
			// Persist through the durable wrapper so a restart keeps the grant.
			if err := p.Register(w, policy); err != nil {
				return err
			}
			continue
		}
		r.eng.Register(w, policy)
	}
	r.Publish()
	return nil
}

// humanDuration renders a span the way a person reads it: "3 小时 12 分",
// "47 分", "刚刚". Long tasks are the normal case here, so hours must read well.
func humanDuration(d time.Duration) string {
	if d < time.Minute {
		return "刚刚"
	}
	if d < time.Hour {
		return fmt.Sprintf("%d 分", int(d.Minutes()))
	}
	hours := int(d.Hours())
	minutes := int(d.Minutes()) - hours*60
	if minutes == 0 {
		return fmt.Sprintf("%d 小时", hours)
	}
	return fmt.Sprintf("%d 小时 %d 分", hours, minutes)
}
