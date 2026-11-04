package ui

import (
	"fmt"
	"testing"
	"time"

	mui "github.com/egoist/mygo/ui"

	"github.com/zeokeel/super-agent/internal/view"
)

// ticketRowsOf is the list the execution page draws, in the order it draws it:
// the same derivation (bucket + workspace) the page uses.
func ticketRowsOf(a *App) []view.TicketRow {
	vm := a.snap.Tickets()
	if vm == nil {
		return nil
	}
	return a.ticketsVisible(vm)
}

// Reordering must not move the opened row to another Ticket. The rows are sorted
// by recent activity and the tickets by creation, so a publish inserts a row
// above the choice; the list is keyed by the Ticket's ID, so the choice follows
// its Ticket and the list writes the new row back (audit N-8, design §6.5).
func TestTicketSelectionFollowsTheTicketWhenRowsReorder(t *testing.T) {
	a := pageApp(t)
	tt := mui.NewTester(a.view, 1440, 900)
	click(t, tt, "执行")
	click(t, tt, "修改登录超时配置")

	rows := ticketRowsOf(a)
	if a.ticketSel < 0 || a.ticketSel >= len(rows) {
		t.Fatalf("opened row %d of %d", a.ticketSel, len(rows))
	}
	opened := rows[a.ticketSel]
	if opened.ID != "tk-42" {
		t.Fatalf("opened ticket %q, want 42", opened.ID)
	}
	wantTexts(t, tt, "step 7 · 正在补 SessionTimeout 默认值", "explicit · policy v7")

	// The same Tickets, newest first instead: every row index moves.
	vm := a.snap.Tickets()
	rev := *vm
	rev.Rows = make([]view.TicketRow, len(vm.Rows))
	for i := range vm.Rows {
		rev.Rows[len(vm.Rows)-1-i] = vm.Rows[i]
	}
	a.snap.PublishTickets(&rev)
	tt.Frame()

	rows = ticketRowsOf(a)
	if a.ticketSel < 0 || a.ticketSel >= len(rows) {
		t.Fatalf("after the reorder the opened row is %d of %d", a.ticketSel, len(rows))
	}
	if got := rows[a.ticketSel].ID; got != opened.ID {
		t.Fatalf("the opened row moved from ticket %q to %q: the choice stayed on a row number", opened.ID, got)
	}
	// The open detail is still the same Ticket's, not the row's new neighbour.
	wantTexts(t, tt, "step 7 · 正在补 SessionTimeout 默认值", "explicit · policy v7")
	if tt.HasText("已建单，尚未派给执行器") {
		t.Fatalf("a neighbour's detail is open; texts: %q", tt.Texts())
	}
}

// The same rule for the Thread inbox, which is sorted by recent activity and so
// reorders on every event.
func TestThreadSelectionFollowsTheThreadWhenRowsReorder(t *testing.T) {
	a := pageApp(t)
	tt := mui.NewTester(a.view, 1440, 900)
	click(t, tt, "触发")

	vm := a.snap.Threads()
	if a.threadSel < 0 || a.threadSel >= len(vm.Threads) {
		t.Fatalf("chosen row %d of %d", a.threadSel, len(vm.Threads))
	}
	chosen := vm.Threads[a.threadSel]
	wantTexts(t, tt, chosen.Title, "Issue #42 · 修改登录超时配置")

	rev := *vm
	rev.Threads = make([]view.ThreadRow, len(vm.Threads))
	for i := range vm.Threads {
		rev.Threads[len(vm.Threads)-1-i] = vm.Threads[i]
	}
	a.snap.PublishThreads(&rev)
	tt.Frame()

	if a.threadSel < 0 || a.threadSel >= len(rev.Threads) {
		t.Fatalf("after the reorder the chosen row is %d of %d", a.threadSel, len(rev.Threads))
	}
	if got := rev.Threads[a.threadSel].ID; got != chosen.ID {
		t.Fatalf("the choice moved from thread %q to %q: it stayed on a row number", chosen.ID, got)
	}
	// The timeline still belongs to the chosen Thread.
	wantTexts(t, tt, "Issue #42 · 修改登录超时配置", "命中工作区 acme/webapp 的关键词「登录」")
}

// The Decision list is a ui.Table: the framework draws the header, and only the
// rows in view are built. It used to be a Scroll over a Column of every row
// (audit N-8 / F-02).
func TestEvalTableBuildsOnlyTheRowsInView(t *testing.T) {
	now := time.Now()
	rows := make([]view.DecisionRow, 80)
	for i := range rows {
		rows[i] = view.DecisionRow{
			ID: fmt.Sprintf("d-%d", i), Kind: "accept", KindLabel: "接受",
			Trigger: fmt.Sprintf("Trigger %d", i+1), Summary: fmt.Sprintf("结果 %d", i+1),
			ReasonCode: "routed", ReasonText: "按工作区简报路由到这里", At: now.Add(-time.Duration(i) * time.Minute),
		}
	}
	snap := view.NewSnapshot()
	snap.PublishDecisions(&view.DecisionVM{Rows: rows, UpdatedAt: now})
	a := newApp(snap, &fakeCmd{})
	a.now = now

	tt := mui.NewTester(a.view, 1440, 900)
	click(t, tt, "评估")
	// The header is the Table's, and the first rows are there.
	wantTexts(t, tt, "Trigger → 结果", "reason 码 · 命中规则", "Trigger 1", "结果 1")
	if tt.HasText("Trigger 80") {
		t.Fatalf("the table built rows past the view; texts: %q", tt.Texts())
	}
}

// An empty filter keeps the honest line instead of an empty table.
func TestEvalTableEmptyFilter(t *testing.T) {
	a := pageApp(t)
	a.evalKind = "没有这种决定"
	tt := mui.NewTester(a.view, 1440, 900)
	click(t, tt, "评估")
	wantTexts(t, tt, "这个筛选下没有 Decision。")
}

// The palette is a ui.List of headings and hits: a heading is not selectable, the
// keyboard's choice is never a heading, and a click on a hit opens it in the
// same frame (audit N-8 / F-06).
func TestPaletteListHeadingsAreNotChoices(t *testing.T) {
	a := pageApp(t)
	tt := mui.NewTester(a.view, 1440, 900)

	openPalette(t, tt)
	tt.Type("登录")
	tt.Frame()

	vm := a.searchVM
	rows := paletteRows(vm)
	if len(rows) == 0 || len(a.searchFlat) == 0 {
		t.Fatalf("no rows for 登录: %+v", vm)
	}
	if !rows[0].header {
		t.Fatal("a group's heading must come before its hits")
	}
	// Every position the keyboard can reach is a hit, never a heading.
	seen := 0
	for i := 0; i < len(a.searchFlat)+2; i++ {
		tt.Key(0, mui.KeyDown)
		if _, _, ok := hitAtRow(rows, a.paletteSel); !ok {
			t.Fatalf("↓ moved the choice onto the heading at row %d", a.paletteSel)
		}
		seen++
	}
	if seen == 0 {
		t.Fatal("the keyboard moved over no rows")
	}

	// A click on a hit's context line opens it: one click is the jump.
	click(t, tt, "#42 · acme/webapp · 执行中")
	if a.searchOpen {
		t.Fatal("a click on a hit must close the palette")
	}
	if a.sec != view.SecExecution {
		t.Fatalf("section %q, want execution", a.sec)
	}
	if a.locate == nil || a.locate.TicketID != "tk-42" {
		t.Fatalf("located %+v, want ticket #42", a.locate)
	}
}

// A click on a heading does nothing: headings are not choices, so the palette
// stays open instead of jumping somewhere arbitrary.
func TestPaletteListClickOnHeadingDoesNothing(t *testing.T) {
	a := pageApp(t)
	tt := mui.NewTester(a.view, 1440, 900)
	openPalette(t, tt)
	tt.Type("登录")
	tt.Frame()
	if !a.searchOpen {
		t.Fatal("the palette closed on typing")
	}
	if err := tt.Click("工单"); err != nil {
		t.Fatalf("click heading: %v", err)
	}
	if !a.searchOpen {
		t.Fatal("a click on a heading jumped somewhere; headings are not choices")
	}
}
