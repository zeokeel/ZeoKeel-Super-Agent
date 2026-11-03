package view_test

import (
	"testing"
	"time"

	// The exported view API is dot-imported, and the sample projections come from
	// viewtest: these tests read like the package's own, as they did before the
	// fixtures moved out of the production package.
	. "github.com/zeokeel/super-agent/internal/view"
	"github.com/zeokeel/super-agent/internal/view/viewtest"
)

// The bucket counts are the list's counts: a filter chip must never disagree
// with the rows it shows (005 "诚实状态").
func TestTicketBucketsCountTheirRows(t *testing.T) {
	now := time.Now()
	vm := viewtest.DemoTickets(now)
	if len(vm.Rows) != vm.Total {
		t.Fatalf("total %d, rows %d", vm.Total, len(vm.Rows))
	}
	seen := map[TicketBucket]bool{}
	for _, b := range vm.Buckets {
		if seen[b.Bucket] {
			t.Fatalf("bucket %q appears twice", b.Bucket)
		}
		seen[b.Bucket] = true
		if b.Label == "" {
			t.Errorf("bucket %q has no label", b.Bucket)
		}
		if got := len(FilterTickets(vm.Rows, b.Bucket)); got != b.Count {
			t.Errorf("bucket %q counts %d, filters to %d", b.Bucket, b.Count, got)
		}
	}
	for _, want := range []TicketBucket{BucketOpen, BucketPending, BucketWorking, BucketWaiting, BucketEnded} {
		if !seen[want] {
			t.Errorf("bucket %q has no chip", want)
		}
	}
}

// The page's default order is recent activity, not creation time (design
// §6.10): a stalled Ticket is what a person must be able to spot first.
func TestListsAreOrderedByRecentActivity(t *testing.T) {
	vm := viewtest.DemoTickets(time.Now())
	for i := 1; i < len(vm.Rows); i++ {
		if vm.Rows[i].Age < vm.Rows[i-1].Age {
			t.Errorf("ticket %q is older than %q above it", vm.Rows[i].ID, vm.Rows[i-1].ID)
		}
	}
	threads := viewtest.DemoThreads(time.Now())
	for i := 1; i < len(threads.Threads); i++ {
		if threads.Threads[i].ActivityAt.After(threads.Threads[i-1].ActivityAt) {
			t.Errorf("thread %q is newer than %q above it", threads.Threads[i].ID, threads.Threads[i-1].ID)
		}
	}
}

// The stage mapping is one table: every domain stage lands in exactly one bucket,
// and the buckets keep the order the page shows.
func TestBucketOfStage(t *testing.T) {
	cases := []struct {
		stage    string
		assigned bool
		want     TicketBucket
	}{
		{"ended", true, BucketEnded},
		{"person/answer", true, BucketWaiting},
		{"person/review", true, BucketWaiting},
		{"person/blocked", true, BucketWaiting},
		{"person/delivery", true, BucketWaiting},
		{"idle", true, BucketWaiting},
		{"working", true, BucketWorking},
		{"working/queued", false, BucketOpen},
		{"working/queued", true, BucketPending},
		{"something-new", false, BucketWorking},
	}
	for _, c := range cases {
		if got := BucketOf(c.stage, c.assigned); got != c.want {
			t.Errorf("BucketOf(%q, %v) = %q, want %q", c.stage, c.assigned, got, c.want)
		}
	}
	order := BucketOrder()
	if len(order) != 5 {
		t.Fatalf("bucket order has %d entries", len(order))
	}
	for _, b := range order {
		if b.Label() == "" || b.Label() == string(b) {
			t.Errorf("bucket %q has no Chinese label", b)
		}
	}
}

// The decision kinds count their rows, and "no filter" returns every row.
func TestDecisionKindsCountTheirRows(t *testing.T) {
	vm := viewtest.DemoDecisions(time.Now())
	sum := 0
	for _, k := range vm.Kinds {
		got := len(FilterDecisions(vm.Rows, k.Kind))
		if got != k.Count {
			t.Errorf("kind %q counts %d, filters to %d", k.Kind, k.Count, got)
		}
		sum += k.Count
	}
	if sum != len(vm.Rows) {
		t.Errorf("kinds sum %d, rows %d", sum, len(vm.Rows))
	}
	if got := len(FilterDecisions(vm.Rows, "")); got != len(vm.Rows) {
		t.Errorf("no filter returns %d of %d", got, len(vm.Rows))
	}
	if got := len(FilterTickets(vmRows(), "")); got != len(vmRows()) {
		t.Errorf("no bucket filter returns %d of %d", got, len(vmRows()))
	}
}

func vmRows() []TicketRow { return viewtest.DemoTickets(time.Now()).Rows }

// The source page's heading counts, and the filtered reasons' total, are facts
// of its own list — not separate numbers the core has to remember.
func TestSourceCountsMatchRows(t *testing.T) {
	vm := viewtest.DemoSource(time.Now())
	enabled, failing, filtered := 0, 0, 0
	for _, s := range vm.Sources {
		if s.Enabled {
			enabled++
		}
		if !s.Enabled || s.LastPoll.Outcome == PollFailed {
			failing++
		}
		filtered += s.Filtered24h
	}
	if got := vm.EnabledCount(); got != enabled {
		t.Errorf("enabled %d, rows say %d", got, enabled)
	}
	if got := vm.FailingCount(); got != failing {
		t.Errorf("failing %d, rows say %d", got, failing)
	}
	sum := 0
	for _, f := range vm.Filtered {
		sum += f.Count
	}
	if vm.FilteredTotal != sum {
		t.Errorf("filtered total %d, reasons sum %d", vm.FilteredTotal, sum)
	}
	// The heading's total is the Sources' own sum: one number, not two.
	if filtered != vm.FilteredTotal {
		t.Errorf("sources filtered %d, reasons total %d", filtered, vm.FilteredTotal)
	}
	// One failure exists, precisely so the actionable line is testable.
	if vm.FailingCount() == 0 {
		t.Error("demo source has no failure; the failure line would go untested")
	}
}

// Every Thread has a timeline, so selecting one never shows a blank pane.
func TestEveryThreadHasATimeline(t *testing.T) {
	vm := viewtest.DemoThreads(time.Now())
	if len(vm.Threads) == 0 {
		t.Fatal("no threads")
	}
	for _, th := range vm.Threads {
		d := vm.DetailOf(th.ID)
		if d == nil {
			t.Errorf("thread %q has no timeline", th.ID)
			continue
		}
		if d.ID != th.ID {
			t.Errorf("thread %q opens detail %q", th.ID, d.ID)
		}
		if len(d.Events) == 0 {
			t.Errorf("thread %q has an empty timeline", th.ID)
		}
		// Newest first: the timeline is a reverse-chronological log.
		for i := 1; i < len(d.Events); i++ {
			if d.Events[i].At.After(d.Events[i-1].At) {
				t.Errorf("thread %q: event %d is newer than the one above", th.ID, i)
			}
		}
	}
	// The suggested thread opens by default.
	if got := vm.SelectedDetail(); got == nil || got.ID != vm.Selected {
		t.Errorf("selected detail %v, want thread %q", got, vm.Selected)
	}
}

// A settings group is either a form or explicitly a placeholder; a group the
// page cannot find reads as nil, and saving is told the configuration revision.
func TestSettingsGroupsAndRevision(t *testing.T) {
	vm := viewtest.DemoSettings(time.Now())
	for _, id := range []SettingsGroupID{
		SettingsSource, SettingsEval, SettingsExecution, SettingsPrompt, SettingsDeploy,
	} {
		g := vm.Group(id)
		if g == nil {
			t.Fatalf("group %q missing", id)
		}
		if g.Label == "" {
			t.Errorf("group %q has no label", id)
		}
		if len(g.Fields) == 0 && g.Placeholder == "" {
			t.Errorf("group %q is neither a form nor a placeholder", id)
		}
	}
	if vm.Group("nope") != nil {
		t.Error("an unknown group must read as nil")
	}
	// 来源 and 执行 are the two real forms; the others keep their structure.
	src, exec := vm.Group(SettingsSource), vm.Group(SettingsExecution)
	if len(src.Fields) == 0 || len(exec.Fields) == 0 {
		t.Error("the wired groups must carry fields")
	}
	if src.Placeholder != "" || exec.Placeholder != "" {
		t.Error("the wired groups must not be placeholders")
	}
	// 提示词 has a real editor now (its own PromptVM shard); the other groups keep
	// their structure and say why their form is missing.
	for _, id := range []SettingsGroupID{SettingsEval, SettingsDeploy} {
		if vm.Group(id).Placeholder == "" {
			t.Errorf("group %q should say why its form is missing", id)
		}
	}
	if p := vm.Group(SettingsPrompt); p == nil || p.Placeholder != "" {
		t.Error("the prompt group must no longer be a placeholder")
	}
	if vm.Revision == 0 {
		t.Error("the settings projection must name the revision it was read at")
	}
}

// Every shard publishes and reads back independently, and every publish asks for
// exactly one frame (design §6.2). 提示词 and 搜索 are shards of their own so an
// edit or a keystroke never rebuilds the long ticket list.
func TestSnapshotPublishesEveryPage(t *testing.T) {
	now := time.Now()
	snap := NewSnapshot()
	frames := 0
	snap.OnPublish(func() { frames++ })

	src := viewtest.DemoSource(now)
	threads := viewtest.DemoThreads(now)
	decisions := viewtest.DemoDecisions(now)
	tickets := viewtest.DemoTickets(now)
	settings := viewtest.DemoSettings(now)
	prompts := viewtest.DemoPrompts(now)
	schedules := viewtest.DemoSchedules(now)
	goals := viewtest.DemoGoals(now)
	search := BuildSearch("登录", SearchInput{Tickets: tickets, Stream: nil, Decisions: decisions, Sources: src})

	snap.PublishSources(src)
	snap.PublishThreads(threads)
	snap.PublishDecisions(decisions)
	snap.PublishTickets(tickets)
	snap.PublishSettings(settings)
	snap.PublishPrompts(prompts)
	snap.PublishSearch(search)
	snap.PublishSchedules(schedules)
	snap.PublishGoals(goals)

	if frames != 9 || snap.Revision() != 9 {
		t.Fatalf("frames=%d rev=%d, want 9/9", frames, snap.Revision())
	}
	if snap.Sources() != src || snap.Threads() != threads || snap.Decisions() != decisions ||
		snap.Tickets() != tickets || snap.Settings() != settings ||
		snap.Prompts() != prompts || snap.Search() != search ||
		snap.Schedules() != schedules || snap.Goals() != goals {
		t.Fatal("a shard did not read back the value it published")
	}
	// A page that has not been published reads as nil, never as stale data.
	fresh := NewSnapshot()
	if fresh.Sources() != nil || fresh.Threads() != nil || fresh.Decisions() != nil ||
		fresh.Tickets() != nil || fresh.Settings() != nil || fresh.Prompts() != nil ||
		fresh.Search() != nil || fresh.Schedules() != nil || fresh.Goals() != nil {
		t.Fatal("an empty snapshot returned a page")
	}
}

// A nil VM answers safely: the page code may load before the first publish.
func TestNilPagesAnswerSafely(t *testing.T) {
	var src *SourceVM
	if src.EnabledCount() != 0 || src.FailingCount() != 0 {
		t.Error("nil source VM reported counts")
	}
	var th *ThreadVM
	if th.DetailOf("x") != nil || th.SelectedDetail() != nil {
		t.Error("nil thread VM returned a detail")
	}
	var dec *DecisionVM
	_ = dec
	var tk *TicketListVM
	if _, ok := tk.DetailOf("x"); ok {
		t.Error("nil ticket VM returned a detail")
	}
	var st *SettingsVM
	if st.Group(SettingsSource) != nil {
		t.Error("nil settings VM returned a group")
	}
	var pr *PromptVM
	if pr.Layer(PromptRouter) != nil || pr.EditableCount() != 0 || pr.ChangedCount() != 0 {
		t.Error("nil prompt VM answered for a layer")
	}
	var se *SearchVM
	if !se.Empty() || se.Flat() != nil || viewtest.GroupOf(se, SearchTickets) != nil {
		t.Error("nil search VM answered with results")
	}
	var sc *ScheduleVM
	if !sc.Empty() || sc.EnabledCount() != 0 || sc.RunningCount() != 0 {
		t.Error("nil schedule VM answered with rows")
	}
	var gv *GoalVM
	if !gv.Empty() || gv.ActiveCount() != 0 || gv.Active(2) != nil {
		t.Error("nil goal VM answered for goals")
	}
}
