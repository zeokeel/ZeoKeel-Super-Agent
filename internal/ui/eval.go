package ui

import (
	"fmt"

	mui "github.com/egoist/mygo/ui"

	"github.com/zeokeel/super-agent/internal/view"
)

// evalPage shows Eval's output: every Decision with the reason code that
// produced it and the rule it hit. A free-text reason cannot be checked; a code
// plus its rendered template can (001 / 002). The kind filter narrows the rows,
// it never changes them: this page is read-only inspection (design §6.3).
func (a *App) evalPage(c *mui.Context) {
	tk := a.tk
	vm := a.snap.Decisions()

	mui.Column(c).Fill().Padding(20, 24).Gap(12).Children(func() {
		mui.Row(c).AlignItems(mui.Center).Gap(8).Children(func() {
			heading(c, tk, "评估", "每个 Decision 都带可检查的 reason 码与命中规则；不用自由文本猜")
			mui.Spacer(c)
			mui.Text(c, "过去 24 小时").FontSize(13).TextColor(tk.TextFaint)
			if vm != nil && vm.PolicyVersion != "" {
				textChip(c, tk, "Policy "+vm.PolicyVersion+" · 只读")
			}
		})
		if vm == nil {
			mui.Text(c, "等待运行时发布评估快照…").FontSize(15).TextColor(tk.TextMuted)
			return
		}

		a.evalCounts(c, vm)
		a.evalFilter(c, vm)
		a.evalList(c, vm)
	})
}

// evalCounts is the one surface of totals: the heading's numbers are the list's
// numbers (005 "诚实状态").
func (a *App) evalCounts(c *mui.Context, vm *view.DecisionVM) {
	tk := a.tk
	mui.Row(c).Gap(16).AlignItems(mui.Stretch).Children(func() {
		cardG(c, tk, 12, func() {
			mui.Row(c).Gap(26).AlignItems(mui.Center).Children(func() {
				stat(c, tk, "评估的 Trigger", vm.Counts.Evaluated)
				dividerV(c, tk)
				stat(c, tk, "Decision", vm.Counts.Decisions)
				dividerV(c, tk)
				stat(c, tk, "过滤 / 丢弃", vm.Counts.Filtered)
				dividerV(c, tk)
				stat(c, tk, "建 Ticket", vm.Counts.Tickets)
				mui.Spacer(c)
				mui.Column(c).Gap(1).Children(func() {
					mui.Text(c, "golden 用例").FontSize(12).TextColor(tk.TextFaint)
					for _, g := range vm.Golden {
						mui.Row(c).Gap(6).Children(func() {
							mui.Text(c, g.Name).FontSize(12).TextColor(tk.TextMuted)
							mui.Text(c, fmt.Sprintf("%d / %d", g.Passed, g.Total)).FontSize(12).
								TextColor(ifColor(g.Passed == g.Total, tk.Green, tk.Red))
						})
					}
				})
			})
		})
	})
}

// evalFilter is the kind filter plus the rules that fired: the filter narrows
// what the page draws, the rules explain why the Decisions happened at all.
func (a *App) evalFilter(c *mui.Context, vm *view.DecisionVM) {
	tk := a.tk

	mui.Row(c).Gap(6).AlignItems(mui.Center).Children(func() {
		if filterChip(c, tk, "全部", a.evalKind == "", len(vm.Rows)) {
			a.evalKind = ""
		}
		for _, k := range vm.Kinds {
			if filterChip(c, tk, k.Label, a.evalKind == k.Kind, k.Count) {
				a.evalKind = k.Kind
			}
		}
		mui.Spacer(c)
		if len(vm.Rules) > 0 {
			hits := 0
			for _, r := range vm.Rules {
				hits += r.Hits
			}
			mui.Text(c, fmt.Sprintf("Policy 规则 %d 条 · 命中 %d 次", len(vm.Rules), hits)).
				FontSize(10).TextColor(tk.TextFaint)
		}
	})
}

// evalList is the Decision list itself: time, what was judged, what was decided,
// the reason code and the rule that fired.
//
// It is a ui.Table: the header row, the column widths, resizing and the
// virtualized rows are the framework's (ui/table.go), so the page keeps only the
// cells. A Decision row does not expand, which is what a Table draws; the
// Ticket list, whose rows do, stays a ui.List.
func (a *App) evalList(c *mui.Context, vm *view.DecisionVM) {
	tk := a.tk
	rows := a.decisionsVisible(vm)

	mui.Column(c).Grow(1).MinHeight(0).Background(tk.Surface).Radius(tk.Radius).
		Border(1, tk.Border).Children(func() {
		if len(rows) == 0 {
			mui.Row(c).Padding(16, 14).Children(func() {
				mui.Text(c, "这个筛选下没有 Decision。").FontSize(14).TextColor(tk.TextMuted)
			})
			return
		}
		cols := []mui.TableColumn{
			{Title: "时间", Width: 56, MinWidth: 56},
			{Title: "决定", Width: 88, MinWidth: 88},
			{Title: "Trigger → 结果", MinWidth: 220},
			{Title: "reason 码 · 命中规则", MinWidth: 260},
		}
		mui.Table(c, &a.evalTable, cols, len(rows), func(row, col int) {
			a.decisionCell(c, rows[row], col)
		}).Grow(1)
	})
}

// decisionCell is one cell of one Decision row.
func (a *App) decisionCell(c *mui.Context, d view.DecisionRow, col int) {
	tk := a.tk
	located := a.located(view.SearchDecisions, d.ID)
	switch col {
	case 0:
		mui.Text(c, d.At.Format("15:04")).FontSize(12).Font("monospace").
			TextColor(ifColor(located, tk.Accent, tk.TextFaint))
	case 1:
		chip(c, tk, d.KindLabel, toneFg(tk, d.Tone), toneBg(tk, d.Tone))
	case 2:
		mui.Column(c).Gap(2).Children(func() {
			mui.Text(c, d.Trigger).FontSize(14).SingleLine().TextColor(tk.Text)
			mui.Text(c, d.Summary).FontSize(12).SingleLine().TextColor(tk.TextMuted)
		})
	case 3:
		mui.Column(c).Gap(2).Children(func() {
			mui.Row(c).Gap(6).AlignItems(mui.Center).Children(func() {
				if located {
					chip(c, tk, "定位", tk.Accent, tk.AccentSoft)
				}
				mono(c, tk, d.ReasonCode)
				mui.Text(c, d.ReasonText).FontSize(13).SingleLine().Grow(1).TextColor(tk.Text)
				if d.Workspace != "" {
					textChip(c, tk, d.Workspace)
				}
			})
			if d.Rule != "" {
				mui.Row(c).Gap(4).AlignItems(mui.Center).Children(func() {
					mui.Icon(c, icShield).FontSize(10).TextColor(tk.TextFaint)
					mui.Text(c, "命中规则："+d.Rule).FontSize(12).SingleLine().TextColor(tk.TextFaint)
				})
			}
		})
	}
}

// decisionsVisible returns the rows the chosen kind shows. The filtering is a
// pure function of published data and app state, cached on the main thread: a
// frame never sorts or filters while drawing (design §6.4).
func (a *App) decisionsVisible(vm *view.DecisionVM) []view.DecisionRow {
	if vm == a.evalSrc && a.evalKind == a.evalKindUsed {
		return a.evalRows
	}
	a.evalSrc = vm
	a.evalKindUsed = a.evalKind
	a.evalRows = view.FilterDecisions(vm.Rows, a.evalKind)
	return a.evalRows
}

// dividerV is a hairline between two stats.
func dividerV(c *mui.Context, tk Tokens) {
	mui.Box(c).Width(1).Height(26).Background(tk.BorderSoft)
}
