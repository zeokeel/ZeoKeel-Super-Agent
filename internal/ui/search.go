package ui

import (
	"fmt"
	"strings"

	mui "github.com/egoist/mygo/ui"

	"github.com/zeokeel/super-agent/internal/view"
)

// The cross-area palette (⌘K / Ctrl+K): one query over the projections the window
// already holds — ticket titles, message text, Decision summaries, workspace names
// and Source names — with grouped results, ↑/↓ over one flat list, and Enter to
// jump to the page that owns the hit and locate it there.
//
// It is a Modal + SearchField + a plain column of rows: each group is capped by
// view.SearchGroupLimit, so a few dozen rows per group never need virtualization,
// and the list stays bounded while the operator types. Results come from
// view.BuildSearch, a pure function, unless the runtime published a SearchVM for
// the same query (a runtime that indexes history beyond what the window holds).

// paletteRow is one line of the palette's list: a group heading, or a hit.
// Flattening the groups into rows is what lets a ui.List draw them: the headings
// are ListState.Header rows, which the list keeps at the top of their section and
// never chooses (ui/list.go:93-97).
type paletteRow struct {
	header   bool
	label    string
	count    int
	hit      view.SearchHit
	hitIndex int    // the hit's index over the flat list, -1 for a heading
	key      string // unique among the rows, for ListState.Key
}

// paletteRows flattens the result groups into the list's rows: a heading, then
// its hits.
func paletteRows(vm *view.SearchVM) []paletteRow {
	if vm == nil {
		return nil
	}
	var rows []paletteRow
	n := 0
	for _, g := range vm.Groups {
		rows = append(rows, paletteRow{
			header: true, label: g.Label, count: len(g.Hits),
			hitIndex: -1, key: "h:" + g.Label,
		})
		for _, h := range g.Hits {
			rows = append(rows, paletteRow{
				hit: h, hitIndex: n, key: "r:" + fmt.Sprint(n),
			})
			n++
		}
	}
	return rows
}

// rowOfHit is the row the given hit is drawn on, or 0 when there is none.
func rowOfHit(rows []paletteRow, hit int) int {
	for i := range rows {
		if rows[i].hitIndex == hit {
			return i
		}
	}
	return 0
}

// hitAtRow is the hit drawn on a row, and its index over the flat list.
func hitAtRow(rows []paletteRow, row int) (view.SearchHit, int, bool) {
	if row < 0 || row >= len(rows) || rows[row].header {
		return view.SearchHit{}, 0, false
	}
	return rows[row].hit, rows[row].hitIndex, true
}

// openSearch shows the palette with an empty query and no stale result.
func (a *App) openSearch() {
	a.searchOpen = true
	a.searchQuery = ""
	a.searchSel = 0
	a.paletteSel = 0
	a.searchReady = false
	a.locate = nil
}

// searchPalette draws the palette while it is open. Everything it needs is app
// state: no element and no context survives the frame.
//
// The results are a ui.List of rows (headings and hits), not a hand-drawn
// Column: the list draws only the rows in view, keeps its place, and knows that a
// heading is not a choice. The keyboard still moves the choice from here because
// the focus belongs to the search field, not to the list: ↓ and ↑ are shortcuts
// of the frame, and Enter is the field's own Submitted.
func (a *App) searchPalette(c *mui.Context) {
	if !a.searchOpen {
		return
	}
	tk := a.tk

	mui.Modal(c, &a.searchOpen, func() {
		mui.Column(c).Width(660).Gap(9).Children(func() {
			sf := mui.SearchField(c.Key("palette-search"), &a.searchQuery).
				AutoFocus().Label("搜索工单、消息、决策、工作区、来源")

			// The result list depends on the query, so compute it before the keys
			// move the choice: one frame, one answer.
			vm := a.searchResults()
			rows := paletteRows(vm)
			if c.Shortcut(0, mui.KeyDown) {
				a.moveSearch(rows, 1)
			}
			if c.Shortcut(0, mui.KeyUp) {
				a.moveSearch(rows, -1)
			}
			a.paletteSel = rowOfHit(rows, a.searchSel)
			if sf.Submitted() {
				a.jumpSearch()
			}

			mui.Row(c).Gap(8).AlignItems(mui.Center).Children(func() {
				mui.Text(c, "↑↓ 选择 · ↵ 跳转 · Esc 关闭").FontSize(10).TextColor(tk.TextFaint)
				mui.Spacer(c)
				if q := strings.TrimSpace(a.searchQuery); q != "" {
					mui.Text(c, q).FontSize(10).SingleLine().TextColor(tk.TextMuted)
				}
			})

			if strings.TrimSpace(a.searchQuery) == "" {
				mui.Row(c).Padding(14, 16).Children(func() {
					mui.Text(c, "输入关键词搜索：工单标题、消息文本、决策摘要、工作区名、来源名。").
						FontSize(12).TextColor(tk.TextMuted)
				})
				return
			}
			if vm.Empty() {
				mui.Row(c).Padding(14, 16).Gap(8).AlignItems(mui.Start).Children(func() {
					mui.Icon(c, icSearch).FontSize(13).TextColor(tk.TextFaint)
					mui.Text(c, fmt.Sprintf("没有匹配「%s」的结果。试试更短的关键词，或工单号、工作区名。",
						strings.TrimSpace(a.searchQuery))).FontSize(12).TextColor(tk.TextMuted)
				})
				return
			}

			a.searchListBody(c, rows)
			note := fmt.Sprintf("共 %d 条结果", vm.Total)
			if vm.Truncated {
				note = fmt.Sprintf("共 %d 条结果 · 每组只显示前 %d 条", vm.Total, view.SearchGroupLimit)
			}
			mui.Row(c).Height(20).AlignItems(mui.Center).Children(func() {
				mui.Text(c, note).FontSize(10).TextColor(tk.TextFaint)
				mui.Spacer(c)
				mui.Text(c, "回车跳到那一页并定位").FontSize(10).TextColor(tk.TextFaint)
			})
		})
	})
}

// searchListBody draws the palette's rows. A click chooses a row in the list and
// this frame opens it: the palette's rows are a jump list, so one click is the
// jump (Enter does the same for the keyboard's choice).
func (a *App) searchListBody(c *mui.Context, rows []paletteRow) {
	a.paletteList.Selected = &a.paletteSel
	a.paletteList.Header = func(i int) bool { return rows[i].header }
	a.paletteList.Key = func(i int) any { return rows[i].key }
	lst := mui.List(c, &a.paletteList, len(rows), func(i int) {
		if rows[i].header {
			a.searchGroupHeader(c, rows[i])
			return
		}
		a.searchRow(c, rows[i].hit, i == a.paletteSel)
	}).MaxHeight(430)
	if lst.Changed() {
		if h, idx, ok := hitAtRow(rows, a.paletteSel); ok {
			a.searchSel = idx
			a.jumpTo(h)
		}
	}
}

// searchGroupHeader is one group heading of the palette. The list paints a
// heading's background, so it needs one (ui/list.go:96).
func (a *App) searchGroupHeader(c *mui.Context, r paletteRow) {
	tk := a.tk
	mui.Row(c).Height(24).Padding(0, 6).Gap(6).AlignItems(mui.Center).
		Background(tk.Surface).Children(func() {
		mui.Text(c, r.label).FontSize(10).TextColor(tk.TextFaint)
		mui.Text(c, fmt.Sprint(r.count)).FontSize(10).TextColor(tk.TextFaint)
		mui.Box(c).Height(1).Grow(1).Background(tk.BorderSoft)
	})
}

// searchRow is one result: an icon for its kind, the line, and one line of
// context. The chosen row is the one Enter opens.
func (a *App) searchRow(c *mui.Context, h view.SearchHit, on bool) {
	tk := a.tk
	row := mui.Row(c).Height(40).Padding(0, 8).Radius(7).Gap(10).AlignItems(mui.Center)
	if on {
		row.Background(tk.AccentSoft)
	}
	row.Children(func() {
		mui.Icon(c, searchIcon(h.Kind)).FontSize(13).
			TextColor(ifColor(on, tk.Accent, tk.TextMuted))
		mui.Column(c).Grow(1).MinWidth(0).Gap(1).Children(func() {
			mui.Text(c, h.Title).FontSize(12).SingleLine().
				TextColor(ifColor(on, tk.Accent, tk.Text))
			if h.Detail != "" {
				mui.Text(c, h.Detail).FontSize(10).SingleLine().
					TextColor(ifColor(on, tk.Accent, tk.TextFaint))
			}
		})
		if on {
			mui.Text(c, "↵").FontSize(10).TextColor(tk.Accent)
		}
	})
}

// searchIcon is the glyph of one result kind.
func searchIcon(k view.SearchKind) *mui.SVG {
	switch k {
	case view.SearchTickets:
		return icRun
	case view.SearchMessages:
		return icTrigger
	case view.SearchDecisions:
		return icEval
	case view.SearchWorkspaces:
		return icFolder
	default:
		return icSource
	}
}

// searchResults returns the results of the current query, cached until the query
// or a published projection changes. The runtime's own SearchVM is preferred when
// it answers the same query: it may know history the window does not.
func (a *App) searchResults() *view.SearchVM {
	rev := a.snap.Revision()
	if a.searchReady && a.searchQuery == a.searchQueryUsed && rev == a.searchSrcRev {
		return a.searchVM
	}
	vm := a.snap.Search()
	if vm == nil || vm.Query != a.searchQuery {
		vm = view.BuildSearch(a.searchQuery, view.SearchInput{
			Tickets:   a.snap.Tickets(),
			Stream:    a.snap.Stream(),
			Decisions: a.snap.Decisions(),
			Sources:   a.snap.Sources(),
		})
	}
	a.searchVM = vm
	a.searchFlat = vm.Flat()
	a.searchQueryUsed = a.searchQuery
	a.searchSrcRev = rev
	a.searchReady = true
	a.clampSearch()
	return a.searchVM
}

// clampSearch keeps the keyboard choice inside the current result list.
func (a *App) clampSearch() {
	if a.searchSel >= len(a.searchFlat) {
		a.searchSel = len(a.searchFlat) - 1
	}
	if a.searchSel < 0 {
		a.searchSel = 0
	}
}

// moveSearch moves the choice by one, never past either end: a palette that wraps
// makes Enter unpredictable. The choice is an index over the hits; the list's row
// follows it, headings and all, and scrolls to show it.
func (a *App) moveSearch(rows []paletteRow, delta int) {
	if len(a.searchFlat) == 0 {
		return
	}
	a.searchSel += delta
	a.clampSearch()
	a.paletteSel = rowOfHit(rows, a.searchSel)
	a.paletteList.ScrollIntoView(a.paletteSel)
}

// jumpSearch opens the hit the keyboard chose.
func (a *App) jumpSearch() {
	if len(a.searchFlat) == 0 {
		return
	}
	a.jumpTo(a.searchFlat[a.searchSel])
}

// jumpTo closes the palette, switches to the page that owns the hit, and records
// the hit so that page highlights it. Selecting the row is the location: the list
// opens the row in place (design §6.3: opening a row is navigation, not an action).
func (a *App) jumpTo(h view.SearchHit) {
	a.searchOpen = false
	loc := h
	a.locate = &loc
	a.goalFocus = ""
	a.sec = h.Section

	switch h.Kind {
	case view.SearchTickets, view.SearchWorkspaces:
		a.ticketFilter = ""
		a.ticketSel = a.ticketRowIndex(h)
		if a.ticketSel >= 0 {
			a.ticketList.ScrollTo(a.ticketSel, mui.Start)
		}
	case view.SearchDecisions:
		// The Decision's own kind filter would hide it: clear it before showing
		// the page the hit names.
		a.evalKind = ""
	}
}

// ticketRowIndex finds the row of a ticket hit, or of the first work item of a
// workspace hit, in the published order the page draws.
func (a *App) ticketRowIndex(h view.SearchHit) int {
	vm := a.snap.Tickets()
	if vm == nil {
		return -1
	}
	for i, r := range vm.Rows {
		switch h.Kind {
		case view.SearchTickets:
			if r.ID == h.TicketID {
				return i
			}
		case view.SearchWorkspaces:
			if r.Workspace == h.Workspace {
				return i
			}
		}
	}
	return -1
}

// located reports whether the page is showing the row the palette jumped to for
// the kind, and with the given ID: the highlight follows the last jump only.
func (a *App) located(kind view.SearchKind, id string) bool {
	if a.locate == nil || a.locate.Kind != kind {
		return false
	}
	switch kind {
	case view.SearchTickets:
		return a.locate.TicketID == id
	case view.SearchMessages:
		return a.locate.Text == id
	case view.SearchDecisions:
		return a.locate.DecisionID == id
	case view.SearchWorkspaces:
		return a.locate.Workspace == id
	case view.SearchSources:
		return a.locate.Source == id
	}
	return false
}
