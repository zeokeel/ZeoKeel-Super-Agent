package view_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	// The exported view API is dot-imported, and the sample projections come from
	// viewtest: these tests read like the package's own, as they did before the
	// fixtures moved out of the production package.
	. "github.com/zeokeel/super-agent/internal/view"
	"github.com/zeokeel/super-agent/internal/view/viewtest"
)

// The prompt editor's layers are fixed and orderly: the rail, the save values and
// the page all walk PromptLayerIDs, so a missing or reordered layer is a bug the
// projection can be checked for.
func TestPromptLayersKeepTheirOrderAndDefaults(t *testing.T) {
	vm := viewtest.DemoPrompts(time.Now())
	if vm == nil {
		t.Fatal("DemoPrompts returned nil")
	}
	if vm.Revision == 0 {
		t.Error("the prompt projection must name the revision it was read at")
	}
	if vm.Source == "" {
		t.Error("the prompt projection must name where its values live")
	}
	if got, want := len(vm.Layers), len(PromptLayerIDs()); got != want {
		t.Fatalf("%d layers, want %d", got, want)
	}
	for i, id := range PromptLayerIDs() {
		l := vm.Layer(id)
		if l == nil {
			t.Fatalf("layer %q missing", id)
		}
		if l.ID != vm.Layers[i].ID {
			t.Errorf("layer %d is %q, want %q", i, vm.Layers[i].ID, l.ID)
		}
		if l.Label == "" || l.Note == "" {
			t.Errorf("layer %q has no label or note", id)
		}
		if l.Content == "" || l.Default == "" {
			t.Errorf("layer %q has no content or default", id)
		}
		if !l.Editable {
			t.Errorf("layer %q should be editable", id)
		}
		if l.Revision == 0 {
			t.Errorf("layer %q has no revision", id)
		}
	}
	if vm.Layer("nope") != nil {
		t.Error("an unknown layer must read as nil")
	}
	if vm.EditableCount() != 3 {
		t.Errorf("editable count %d, want 3", vm.EditableCount())
	}
	// The demo has exactly one overridden layer, so the "已覆盖" chip and the
	// default-vs-overridden split are both exercised.
	if vm.ChangedCount() != 1 {
		t.Errorf("changed count %d, want 1", vm.ChangedCount())
	}
	if vm.Layer(PromptRouter).IsDefault() {
		t.Error("the Router layer should be overridden in the demo")
	}
	if !vm.Layer(PromptWorker).IsDefault() || !vm.Layer(PromptEvalRules).IsDefault() {
		t.Error("the Worker and rules layers should still be at their defaults")
	}
}

// demoSearchInput is the smallest published data that names all five search areas.
func demoSearchInput(now time.Time) SearchInput {
	return SearchInput{
		Tickets:   viewtest.DemoTickets(now),
		Decisions: viewtest.DemoDecisions(now),
		Sources:   viewtest.DemoSource(now),
		Stream: &StreamVM{Threads: []Thread{
			{Head: Line{Kind: LineInbound, Who: "张三", Source: "钉钉",
				Text: "帮我把 acme 上个月的用量数据发我"}},
			{Head: Line{Kind: LineOperator, Who: "你", Source: "本地面板",
				Text: "把登录超时改成可配置"}},
		}},
	}
}

// The palette covers the five areas the design names: 工单标题、消息文本、Decision
// 摘要、工作区名、来源名. Groups keep SearchKindOrder and each hit carries where to
// jump: a title without a page is useless.
func TestBuildSearchCoversFiveAreas(t *testing.T) {
	now := time.Now()
	vm := BuildSearch("acme", demoSearchInput(now))
	if vm.Empty() {
		t.Fatalf("no hits for acme in %+v", vm)
	}
	// "acme" appears in ticket workspaces and decision workspaces.
	if g := viewtest.GroupOf(vm, SearchWorkspaces); g == nil || len(g.Hits) == 0 {
		t.Fatalf("workspace hits missing; groups=%+v", vm.Groups)
	}
	if h := viewtest.GroupOf(vm, SearchWorkspaces).Hits[0]; h.Section != SecExecution || h.Workspace == "" {
		t.Errorf("workspace hit cannot jump: %+v", h)
	}
	if g := viewtest.GroupOf(vm, SearchTickets); g == nil {
		t.Errorf("ticket hits missing for a workspace name")
	}

	// A ticket title: 工单.
	vm = BuildSearch("登录", demoSearchInput(now))
	tg := viewtest.GroupOf(vm, SearchTickets)
	if tg == nil || len(tg.Hits) == 0 {
		t.Fatalf("ticket hit missing; groups=%+v", vm.Groups)
	}
	if h := tg.Hits[0]; h.Section != SecExecution || h.TicketID == "" {
		t.Errorf("ticket hit cannot jump: %+v", h)
	}
	// A Decision summary: 决策.
	dg := viewtest.GroupOf(vm, SearchDecisions)
	if dg == nil || len(dg.Hits) == 0 {
		t.Fatalf("decision hit missing; groups=%+v", vm.Groups)
	}
	if h := dg.Hits[0]; h.Section != SecEval || h.DecisionID == "" {
		t.Errorf("decision hit cannot jump: %+v", h)
	}

	// A message body: 消息. The stream head has no ID, so the exact text locates it.
	vm = BuildSearch("用量数据", demoSearchInput(now))
	mg := viewtest.GroupOf(vm, SearchMessages)
	if mg == nil || len(mg.Hits) != 1 {
		t.Fatalf("message hit missing; groups=%+v", vm.Groups)
	}
	if h := mg.Hits[0]; h.Section != SecMessages || h.Text != "帮我把 acme 上个月的用量数据发我" {
		t.Errorf("message hit cannot locate its line: %+v", h)
	}

	// A Source name: 来源.
	vm = BuildSearch("GitHub", demoSearchInput(now))
	sg := viewtest.GroupOf(vm, SearchSources)
	if sg == nil || len(sg.Hits) == 0 {
		t.Fatalf("source hit missing; groups=%+v", vm.Groups)
	}
	if h := sg.Hits[0]; h.Section != SecSource || h.Source != "github" {
		t.Errorf("source hit cannot jump: %+v", h)
	}

	// The groups show work first, in the documented order.
	want := []SearchKind{SearchTickets, SearchMessages, SearchDecisions, SearchWorkspaces, SearchSources}
	for i, g := range BuildSearch("a", demoSearchInput(now)).Groups {
		if i >= len(want) || g.Kind != want[i] {
			t.Fatalf("group %d is %q, want %q (order %v)", i, g.Kind, want[i], want)
		}
		if g.Label != g.Kind.Label() {
			t.Errorf("group %q label %q", g.Kind, g.Label)
		}
	}
}

// An empty query shows nothing, and a query with no match says so: the palette
// never invents rows.
func TestBuildSearchEmptyAndNoMatch(t *testing.T) {
	in := demoSearchInput(time.Now())
	for _, q := range []string{"", "   "} {
		vm := BuildSearch(q, in)
		if !vm.Empty() || len(vm.Groups) != 0 || vm.Total != 0 || vm.Truncated {
			t.Errorf("query %q returned %+v, want nothing", q, vm)
		}
	}
	vm := BuildSearch("这个词一定不存在-zzz", in)
	if !vm.Empty() || len(vm.Groups) != 0 {
		t.Errorf("an unmatched query returned %+v", vm)
	}
}

// A hit whose text starts with the query ranks before one that only contains it,
// so typing the beginning of a title puts it first.
func TestBuildSearchRanksPrefixMatchesFirst(t *testing.T) {
	tickets := &TicketListVM{Total: 2, Rows: []TicketRow{
		{ID: "1", Title: "修改登录超时配置", Workspace: "acme/webapp", Stage: "执行中"},
		{ID: "2", Title: "登录超时配置的默认值", Workspace: "acme/webapp", Stage: "排队中"},
	}}
	vm := BuildSearch("登录", SearchInput{Tickets: tickets})
	g := viewtest.GroupOf(vm, SearchTickets)
	if g == nil || len(g.Hits) != 2 {
		t.Fatalf("hits %+v", vm.Groups)
	}
	if g.Hits[0].TicketID != "2" {
		t.Errorf("first hit is #%s, want the prefix match #2", g.Hits[0].TicketID)
	}
}

// Each group is capped, while Total keeps the honest count: the palette says
// "只显示前 N 条" instead of lying about how much matched.
func TestBuildSearchCapsEachGroupHonestly(t *testing.T) {
	n := SearchGroupLimit + 5
	rows := make([]TicketRow, 0, n)
	for i := 0; i < n; i++ {
		rows = append(rows, TicketRow{ID: fmt.Sprint(i), Title: fmt.Sprintf("登录问题 %d", i)})
	}
	vm := BuildSearch("登录", SearchInput{Tickets: &TicketListVM{Rows: rows, Total: n}})
	g := viewtest.GroupOf(vm, SearchTickets)
	if g == nil || len(g.Hits) != SearchGroupLimit {
		t.Fatalf("group has %d hits, want the cap %d", len(g.Hits), SearchGroupLimit)
	}
	if vm.Total != n {
		t.Errorf("total %d, want the honest %d", vm.Total, n)
	}
	if !vm.Truncated {
		t.Error("a capped group must say it is truncated")
	}
	if got := len(vm.Flat()); got != SearchGroupLimit {
		t.Errorf("flat list has %d hits, want %d", got, SearchGroupLimit)
	}
}

// A long message is clipped for the row but kept whole for locating, so Enter
// still finds the line it came from.
func TestBuildSearchClipsTitlesButKeepsTheText(t *testing.T) {
	long := strings.Repeat("很长的消息", 80)
	vm := BuildSearch("很长的消息", SearchInput{Stream: &StreamVM{Threads: []Thread{
		{Head: Line{Kind: LineInbound, Text: long}},
	}}})
	g := viewtest.GroupOf(vm, SearchMessages)
	if g == nil || len(g.Hits) != 1 {
		t.Fatalf("hits %+v", vm.Groups)
	}
	h := g.Hits[0]
	if len([]rune(h.Title)) > 121 || !strings.HasSuffix(h.Title, "…") {
		t.Errorf("title not clipped: %q", h.Title)
	}
	if h.Text != long {
		t.Error("the exact text must survive clipping, or the page cannot locate it")
	}
}
