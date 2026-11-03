package ui

// Plan/task panel: what the Agent intends to do for the running Ticket, drawn in
// the execution detail and in the chat as a task card.

import (
	"fmt"

	mui "github.com/egoist/mygo/ui"

	"github.com/zeokeel/super-agent/internal/view"
)

// planSummary is the one line: "3/5 完成 · 1 进行中 · 1 受阻".
func planSummary(plan *view.PlanVM) string {
	done, total, running, attention := plan.Counts()
	s := fmt.Sprintf("%d/%d 完成", done, total)
	if running > 0 {
		s += fmt.Sprintf(" · %d 进行中", running)
	}
	if attention > 0 {
		s += fmt.Sprintf(" · %d 需要你", attention)
	}
	return s
}

// planPanel draws the plan rows with their status icons, or the honest empty
// state. compact keeps it tight for the chat's task card.
func (a *App) planPanel(c *mui.Context, plan *view.PlanVM, compact bool) {
	tk := a.tk
	if plan == nil || len(plan.Items) == 0 {
		card(c, tk, 12, func() {
			mui.Text(c, "计划与待办").FontSize(fs(14)).Bold().TextColor(tk.Text)
			mui.Text(c, "还没有计划：任务开始后，它会在这里列出步骤。").
				FontSize(fs(12)).TextColor(tk.TextMuted)
		})
		return
	}
	card(c, tk, 12, func() {
		mui.Row(c).Gap(8).AlignItems(mui.Center).Children(func() {
			mui.Icon(c, icCheck).FontSize(fs(13)).TextColor(tk.Accent)
			mui.Text(c, "计划与待办").FontSize(fs(14)).Bold().TextColor(tk.Text)
			mui.Spacer(c)
			mui.Text(c, planSummary(plan)).FontSize(fs(11)).TextColor(tk.TextFaint)
		})
		items := plan.Items
		if compact && len(items) > 4 {
			items = items[:4]
		}
		for _, it := range items {
			a.planRow(c, it)
		}
		if compact && len(plan.Items) > len(items) {
			mui.Text(c, fmt.Sprintf("还有 %d 项…", len(plan.Items)-len(items))).
				FontSize(fs(10)).TextColor(tk.TextFaint)
		}
	})
}

// planRow is one task line: icon, title, optional detail.
func (a *App) planRow(c *mui.Context, it view.PlanItem) {
	tk := a.tk
	icon, fg := icChevR, tk.TextFaint
	switch it.Status {
	case view.PlanDone:
		icon, fg = icCheck, tk.Green
	case view.PlanRunning:
		icon, fg = icRun, tk.Accent
	case view.PlanBlocked:
		icon, fg = icBell, tk.Amber
	case view.PlanFailed:
		icon, fg = icBell, tk.Red
	}
	mui.Row(c).Gap(8).AlignItems(mui.Start).Children(func() {
		mui.Icon(c, icon).FontSize(fs(12)).TextColor(fg)
		mui.Column(c).Grow(1).Gap(1).Children(func() {
			mui.Text(c, it.Title).FontSize(fs(13)).TextColor(tk.Text)
			if it.Detail != "" {
				mui.Text(c, it.Detail).FontSize(fs(11)).SingleLine().TextColor(tk.TextFaint)
			}
		})
		mui.Text(c, planStatusWord(it.Status)).FontSize(fs(10)).TextColor(fg)
	})
}

// planStatusWord names a status in a person's words.
func planStatusWord(status string) string {
	switch status {
	case view.PlanDone:
		return "已完成"
	case view.PlanRunning:
		return "进行中"
	case view.PlanBlocked:
		return "受阻"
	case view.PlanFailed:
		return "失败"
	}
	return "待做"
}
