package ui

import (
	"testing"

	mui "github.com/egoist/mygo/ui"

	"github.com/zeokeel/super-agent/internal/view"
	"github.com/zeokeel/super-agent/internal/view/viewtest"
)

// openPalette clicks the topbar chip, the discoverable way into ⌘K. Typing then
// goes to the search field the palette focused.
func openPalette(t *testing.T, tt *mui.Tester) {
	t.Helper()
	click(t, tt, "搜索")
}

// ⌘K is the shortcut; the chip does the same so the palette is discoverable:
// it opens over any page, shows nothing for an empty query, and searches as soon
// as there is one.
func TestSearchPaletteOpensFiltersAndJumps(t *testing.T) {
	a := pageApp(t)
	tt := mui.NewTester(a.view, 1440, 900)

	openPalette(t, tt)
	if !a.searchOpen {
		t.Fatal("the palette did not open")
	}
	// An empty query shows the hint, not every record in the window.
	wantTexts(t, tt, "输入关键词搜索：工单标题、消息文本、决策摘要、工作区名、来源名。")
	if a.searchVM != nil && a.searchVM.Total != 0 {
		t.Fatalf("an empty query produced %d results", a.searchVM.Total)
	}
	if tt.HasText("条结果") {
		t.Fatalf("an empty query must not show a result count; texts: %q", tt.Texts())
	}

	tt.Type("登录")
	if a.searchQuery != "登录" {
		t.Fatalf("typing reached %q, want 登录", a.searchQuery)
	}
	tt.Frame()
	// Three areas have a hit: 工单 titles, 消息 text and 决策 summaries. The
	// palette's own rows carry the context lines, which the pages behind do not.
	for _, kind := range []view.SearchKind{view.SearchTickets, view.SearchMessages, view.SearchDecisions} {
		if g := viewtest.GroupOf(a.searchVM, kind); g == nil || len(g.Hits) == 0 {
			t.Fatalf("no %q hits; groups=%+v", kind, a.searchVM.Groups)
		}
	}
	if viewtest.GroupOf(a.searchVM, view.SearchWorkspaces) != nil || viewtest.GroupOf(a.searchVM, view.SearchSources) != nil {
		t.Fatalf("a query about a title must not match workspaces or sources: %+v", a.searchVM.Groups)
	}
	wantTexts(t, tt, "#42 · acme/webapp · 执行中", "GitHub · #42 · lin")
	if tt.HasText("更新 OpenAPI 描述") {
		t.Fatalf("unrelated tickets must not match; texts: %q", tt.Texts())
	}

	// ↓ moves over the flat list; ↵ opens the hit's page and selects its row.
	tt.Key(0, mui.KeyDown)
	tt.Key(0, mui.KeyEnter)
	if a.searchOpen {
		t.Fatal("jumping must close the palette")
	}
	if a.sec != view.SecExecution {
		t.Fatalf("section %q, want execution", a.sec)
	}
	if a.locate == nil || a.locate.TicketID != "tk-48" {
		t.Fatalf("located %+v, want ticket #48", a.locate)
	}
	// DemoTickets orders #48 last; the jump selects and opens that row in place.
	if a.ticketSel != 7 {
		t.Fatalf("selected row %d, want 7 (#48)", a.ticketSel)
	}
	wantTexts(t, tt, "定位", "已合并 · PR #127")

	// Navigating away drops the highlight: it must point at something on screen.
	click(t, tt, "超级Agent")
	if a.locate != nil {
		t.Fatalf("navigation kept the location %+v", a.locate)
	}
}

// ⌘K / Ctrl+K opens the palette from anywhere and Escape closes it: the shortcut
// is the reason the chip exists.
func TestSearchOpensWithShortcutAndClosesWithEscape(t *testing.T) {
	a := pageApp(t)
	tt := mui.NewTester(a.view, 1440, 900)

	tt.Key(mui.Cmd, mui.KeyK)
	if !a.searchOpen {
		t.Fatal("⌘K / Ctrl+K did not open the palette")
	}
	tt.Key(0, mui.KeyEscape)
	if a.searchOpen {
		t.Fatal("Escape did not close the palette")
	}
}

// A workspace hit jumps to 执行 and selects the first work item of that workspace,
// so the page opens where the name was found.
func TestSearchJumpToWorkspaceSelectsItsFirstTicket(t *testing.T) {
	a := pageApp(t)
	tt := mui.NewTester(a.view, 1440, 900)

	openPalette(t, tt)
	tt.Type("acme/api")
	tt.Frame()
	if g := viewtest.GroupOf(a.searchVM, view.SearchWorkspaces); g == nil || len(g.Hits) == 0 {
		t.Fatalf("no workspace hit; groups=%+v", a.searchVM.Groups)
	}
	tt.Key(0, mui.KeyEnter)
	if a.sec != view.SecExecution {
		t.Fatalf("section %q, want execution", a.sec)
	}
	if a.locate == nil || a.locate.Workspace != "acme/api" {
		t.Fatalf("located %+v, want workspace acme/api", a.locate)
	}
	// DemoTickets' first row (#47) is the first work item of acme/api.
	if a.ticketSel != 0 {
		t.Fatalf("selected row %d, want 0", a.ticketSel)
	}
	wantTexts(t, tt, "定位", "更新 OpenAPI 描述")
}

// A query with no match says so in words; the palette never shows a blank list.
func TestSearchEmptyQueryAndNoResults(t *testing.T) {
	a := pageApp(t)
	tt := mui.NewTester(a.view, 1440, 900)

	openPalette(t, tt)
	tt.Type("zzz-不存在")
	tt.Frame()
	wantTexts(t, tt, "没有匹配「zzz-不存在」的结果")
	if a.searchVM.Total != 0 || len(a.searchVM.Groups) != 0 {
		t.Fatalf("a failed search produced %+v", a.searchVM)
	}
	if tt.HasText("条结果") {
		t.Fatalf("a failed search must not show a result count; texts: %q", tt.Texts())
	}

	// Clearing the query returns to the hint, not to stale rows.
	a.searchQuery = ""
	tt.Frame()
	wantTexts(t, tt, "输入关键词搜索：工单标题、消息文本、决策摘要、工作区名、来源名。")
	if tt.HasText("没有匹配") {
		t.Fatalf("an empty query is not a failed search; texts: %q", tt.Texts())
	}
}

// A Decision hit jumps to 评估, clears a filter that would hide it, and locates the
// row; a message hit jumps to 消息 and marks the line the palette found.
func TestSearchJumpToDecisionAndMessageLocates(t *testing.T) {
	a := pageApp(t)
	tt := mui.NewTester(a.view, 1440, 900)
	a.evalKind = "propose" // would hide the accept rows if it survived the jump

	openPalette(t, tt)
	tt.Type("提案")
	tt.Frame()
	tt.Key(0, mui.KeyEnter)
	if a.sec != view.SecEval {
		t.Fatalf("section %q, want eval", a.sec)
	}
	if a.evalKind != "" {
		t.Fatalf("the kind filter %q must be cleared or the hit is hidden", a.evalKind)
	}
	if a.locate == nil || a.locate.DecisionID != "d-2" {
		t.Fatalf("located %+v, want decision d-2", a.locate)
	}
	wantTexts(t, tt, "定位", "提案：等你批准后新建 Ticket")

	openPalette(t, tt)
	// A phrase only the message body has, so the first hit is the message.
	tt.Type("应可配置")
	tt.Frame()
	tt.Key(0, mui.KeyEnter)
	if a.sec != view.SecMessages {
		t.Fatalf("section %q, want messages", a.sec)
	}
	want := "登录超时应可配置，默认 30 分钟"
	if a.locate == nil || a.locate.Text != want {
		t.Fatalf("located %+v, want the message %q", a.locate, want)
	}
	wantTexts(t, tt, want, "定位")
}

// A runtime that indexes more history than the window holds answers through its
// own SearchVM; when its answer is for another query the palette builds its own
// from the published projections, so typing never sticks on a stale index.
func TestSearchPrefersThePublishedSearchVMForTheSameQuery(t *testing.T) {
	a := pageApp(t)
	tt := mui.NewTester(a.view, 1440, 900)

	// The last publish would win over a SearchVM with another query; publish it
	// after the pages so its revision is current.
	a.snap.PublishSearch(&view.SearchVM{
		Query: "登录",
		Total: 1,
		Groups: []view.SearchGroup{{
			Kind: view.SearchSources, Label: "来源",
			Hits: []view.SearchHit{{
				Kind: view.SearchSources, Title: "运行时索引命中",
				Section: view.SecSource, Source: "github",
			}},
		}},
	})

	openPalette(t, tt)
	tt.Type("登录")
	tt.Frame()
	wantTexts(t, tt, "运行时索引命中")

	// One more keystroke leaves that answer behind.
	tt.Type("x")
	tt.Frame()
	if tt.HasText("运行时索引命中") {
		t.Fatalf("a stale index must not answer a new query; texts: %q", tt.Texts())
	}
	wantTexts(t, tt, "没有匹配「登录x」的结果")
}
