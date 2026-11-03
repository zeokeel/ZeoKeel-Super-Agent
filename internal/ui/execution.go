package ui

import (
	"fmt"

	mui "github.com/egoist/mygo/ui"

	"github.com/zeokeel/super-agent/internal/view"
)

// executionPage answers "我派过的活现在都到哪了" for every Ticket, by stage. It is
// a read-only list: opening a row expands it in place, and sends no command —
// work-item actions (retry, stop) live only in 总览 (design §6.3).
func (a *App) executionPage(c *mui.Context) {
	tk := a.tk
	vm := a.snap.Tickets()

	mui.Column(c).Fill().Padding(20, 24).Gap(12).Children(func() {
		mui.Row(c).AlignItems(mui.Center).Gap(8).Children(func() {
			heading(c, tk, "执行", "全部任务，按阶段过滤；默认按最近活动排序，不是创建时间")
			mui.Spacer(c)
			if vm != nil {
				chip(c, tk, fmt.Sprintf("%d 个进行中 / 共 %d", bucketCount(vm, view.BucketWorking), vm.Total),
					tk.Accent, tk.AccentSoft)
			}
		})
		if vm == nil {
			mui.Text(c, "等待运行时发布执行快照…").FontSize(15).TextColor(tk.TextMuted)
			return
		}
		if a.actionErr != "" {
			mui.Row(c).Padding(0, 2).Children(func() {
				mui.Text(c, a.actionErr).FontSize(12).TextColor(tk.Red)
			})
		}

		a.ticketFilters(c, vm)
		a.ticketListPage(c, vm)
	})
}

// ticketFilters is the stage filter. Its counts are the published bucket counts,
// so a chip never disagrees with the rows it shows (005 "诚实状态").
func (a *App) ticketFilters(c *mui.Context, vm *view.TicketListVM) {
	tk := a.tk
	mui.Row(c).Gap(6).AlignItems(mui.Center).Children(func() {
		if filterChip(c, tk, "全部", a.ticketFilter == "", vm.Total) {
			a.ticketFilter = ""
			a.ticketSel = -1
		}
		for _, b := range vm.Buckets {
			if filterChip(c, tk, b.Label, a.ticketFilter == b.Bucket, b.Count) {
				a.ticketFilter = b.Bucket
				a.ticketSel = -1
			}
		}
		mui.Spacer(c)
		mui.Text(c, "点一行就地展开 · 动作在总览").FontSize(12).TextColor(tk.TextFaint)
	})
}

// ticketListPage is the virtualized Ticket list: a list because Tick is not the
// only thing that grows; only the rows in view are built.
func (a *App) ticketListPage(c *mui.Context, vm *view.TicketListVM) {
	tk := a.tk
	rows := a.ticketsVisible(vm)
	if a.ticketSel >= len(rows) {
		a.ticketSel = -1
	}

	mui.Column(c).Grow(1).MinHeight(0).Background(tk.Surface).Radius(tk.Radius).
		Border(1, tk.Border).Children(func() {
		mui.Row(c).Height(32).Padding(0, 16).Gap(10).AlignItems(mui.Center).
			Background(tk.SurfaceAlt).Children(func() {
			mui.Text(c, "来源").FontSize(12).TextColor(tk.TextFaint).Width(26)
			mui.Text(c, "任务").FontSize(12).TextColor(tk.TextFaint).Grow(1)
			mui.Text(c, "工作区").FontSize(12).TextColor(tk.TextFaint).Width(120)
			mui.Text(c, "阶段").FontSize(12).TextColor(tk.TextFaint).Width(96)
			mui.Text(c, "最近活动").FontSize(12).TextColor(tk.TextFaint).Width(170)
			mui.Box(c).Width(12)
		})
		mui.Box(c).Height(1).Background(tk.Border)
		if len(rows) == 0 {
			mui.Row(c).Padding(16, 14).Children(func() {
				mui.Text(c, "这个阶段没有任务。").FontSize(14).TextColor(tk.TextMuted)
			})
			return
		}
		a.ticketList.Selected = &a.ticketSel
		// Keyed by the Ticket's own ID: the rows are sorted by recent activity,
		// so a publish can move them under the choice. With a key the list keeps
		// the choice (and the opened row) on the same Ticket and writes its new
		// row back to ticketSel (ui/list.go:42-47, 548-554).
		a.ticketList.Key = func(i int) any { return rows[i].ID }
		lst := mui.List(c, &a.ticketList, len(rows), func(i int) {
			a.ticketRow(c, vm, rows[i], i == a.ticketSel)
		}).Grow(1)
		lst.Changed()
		mui.Box(c).Height(1).Background(tk.BorderSoft)
		mui.Row(c).Height(28).Padding(0, 16).Gap(8).AlignItems(mui.Center).Children(func() {
			mui.Text(c, fmt.Sprintf("共 %d 个任务 · 当前筛选 %d 个 · 按最近活动排序",
				vm.Total, len(rows))).FontSize(12).TextColor(tk.TextFaint)
		})
	})
}

// ticketRow is one Ticket, and — when open — its facts in place. The detail is
// navigation, not an action: nothing here changes the Ticket.
func (a *App) ticketRow(c *mui.Context, vm *view.TicketListVM, r view.TicketRow, opened bool) {
	tk := a.tk
	// A ticket hit locates its own row; a workspace hit locates the first work
	// item of the workspace, which is the one the jump selected.
	located := a.located(view.SearchTickets, r.ID) ||
		(opened && a.located(view.SearchWorkspaces, r.Workspace))
	mui.Column(c).Children(func() {
		mui.Row(c).Height(46).Padding(0, 16).Gap(10).AlignItems(mui.Center).Children(func() {
			sourceGlyph(c, tk, r.SourceIcon, 26)
			mui.Row(c).Grow(1).MinWidth(0).Gap(8).AlignItems(mui.Center).Children(func() {
				mui.Text(c, "#"+r.ID).FontSize(12).Font("monospace").
					TextColor(ifColor(opened, tk.AccentSoft, tk.TextFaint))
				mui.Text(c, r.Title).FontSize(14).SingleLine().
					TextColor(ifColor(opened, tk.Surface, tk.Text))
				if located {
					chipOn(c, tk, "定位", opened, tk.Accent, tk.AccentSoft)
				}
			})
			mui.Row(c).Width(120).Children(func() { textChip(c, tk, r.Workspace) })
			mui.Row(c).Width(96).Children(func() {
				chipOn(c, tk, r.Stage, opened, toneFg(tk, r.Tone), toneBg(tk, r.Tone))
			})
			mui.Row(c).Width(170).Children(func() {
				mui.Text(c, r.Activity).FontSize(12).SingleLine().
					TextColor(ifColor(opened, tk.AccentSoft, tk.TextFaint))
			})
			mui.Icon(c, ifIcon(opened, icChevD, icChevR)).FontSize(12).
				TextColor(ifColor(opened, tk.Surface, tk.TextFaint))
		})
		if !opened {
			return
		}
		a.ticketDetail(c, vm, r)
	})
}

// ticketDetail is what the row opens. A projector that publishes the full detail
// gets it drawn (ticketDetailRich); one that publishes only the short form still
// gets that, plus the honest list of what is missing.
func (a *App) ticketDetail(c *mui.Context, vm *view.TicketListVM, r view.TicketRow) {
	if d := vm.DetailVMOf(r.ID); d != nil {
		a.ticketDetailRich(c, r, d)
		return
	}
	a.ticketDetailShort(c, vm, r)
}

// ticketDetailShort is the short form: why it waits, where it came from, what
// authorized it, and what it produced.
func (a *App) ticketDetailShort(c *mui.Context, vm *view.TicketListVM, r view.TicketRow) {
	tk := a.tk
	d, _ := vm.DetailOf(r.ID)
	// A partial projection is still shown: fall back to the row's own words
	// rather than an empty band.
	detail := d.Detail
	if detail == "" {
		detail = r.Detail
	}
	last := d.LastActivity
	if last == "" {
		last = r.Activity
	}
	mui.Column(c).Padding(10, 16).Gap(6).Background(tk.SurfaceAlt).
		BorderWidth(1, 0, 0, 0).BorderColor(tk.BorderSoft).Children(func() {
		mui.Row(c).Gap(10).AlignItems(mui.Center).Children(func() {
			chip(c, tk, r.Stage, toneFg(tk, r.Tone), toneBg(tk, r.Tone))
			mui.Text(c, detail).FontSize(13).Grow(1).TextColor(tk.Text)
		})
		mui.Row(c).Gap(18).AlignItems(mui.Center).Children(func() {
			detailFact(c, tk, "来源", d.Source)
			if d.Thread != "" {
				detailFact(c, tk, "线程", d.Thread)
			}
			detailFact(c, tk, "授权", d.Mandate)
			mui.Spacer(c)
			mui.Text(c, "最近活动 "+last).FontSize(12).TextColor(tk.TextFaint)
		})
		if len(d.Artifacts) > 0 {
			mui.Row(c).Gap(6).AlignItems(mui.Center).Children(func() {
				mui.Text(c, "产物").FontSize(12).TextColor(tk.TextFaint)
				for _, art := range d.Artifacts {
					textChip(c, tk, art.Title+" · "+art.Detail)
				}
			})
		}
		// The short projection carries no Mandate basis, Inputs or Follows: say
		// so, so an absent section is not read as "nothing happened".
		mui.Text(c, "暂无数据：Mandate 依据 · Assignments / Reports · Inputs · Follows（运行时尚未投影完整详情）").
			FontSize(10).TextColor(tk.TextFaint)
	})
}

// detailFact is one label and value on the opened row.
func detailFact(c *mui.Context, tk Tokens, label, value string) {
	if value == "" {
		return
	}
	mui.Row(c).Gap(5).AlignItems(mui.Center).Children(func() {
		mui.Text(c, label).FontSize(12).TextColor(tk.TextFaint)
		mui.Text(c, value).FontSize(12).SingleLine().TextColor(tk.TextMuted)
	})
}

// ticketsVisible returns the rows of the chosen bucket, cached until the
// published page or the filter changes. Filtering is a pure function in
// internal/view; the view never sorts or filters while drawing (design §6.4).
func (a *App) ticketsVisible(vm *view.TicketListVM) []view.TicketRow {
	if vm == a.ticketSrc && a.ticketFilter == a.ticketFilterUsed {
		return a.ticketRows
	}
	a.ticketSrc = vm
	a.ticketFilterUsed = a.ticketFilter
	a.ticketRows = view.FilterTickets(vm.Rows, a.ticketFilter)
	return a.ticketRows
}

// bucketCount returns one bucket's published count.
func bucketCount(vm *view.TicketListVM, b view.TicketBucket) int {
	for _, c := range vm.Buckets {
		if c.Bucket == b {
			return c.Count
		}
	}
	return 0
}

// ifIcon picks between two icons without a ternary.
func ifIcon(cond bool, yes, no *mui.SVG) *mui.SVG {
	if cond {
		return yes
	}
	return no
}
