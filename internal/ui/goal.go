package ui

import (
	"fmt"

	mui "github.com/egoist/mygo/ui"

	"github.com/zeokeel/super-agent/internal/view"
)

// 目标: long-running objectives. 总览 shows a strip only while something is being
// worked toward (design: 默认安静 — a finished or paused goal takes no room); the
// 触发 page lists them all, because both are "what makes it start work". Read-only
// here: acting on a goal would be a work-item action and belongs to 总览 if added.

// goalsStrip is the overview band: at most two active goals, each one line of
// objective and one of progress. It is absent when nothing is active, so it never
// becomes an empty banner.
func (a *App) goalsStrip(c *mui.Context) {
	tk := a.tk
	active := a.snap.Goals().Active(2)
	if len(active) == 0 {
		return
	}
	card(c, tk, 0, func() {
		mui.Row(c).Padding(12, 16).Gap(8).AlignItems(mui.Center).Children(func() {
			mui.Icon(c, icSpark).FontSize(fs(14)).TextColor(tk.Accent)
			mui.Text(c, "目标").FontSize(fs(17)).Bold().TextColor(tk.Text)
			// The chip counts the rows the strip draws, so a badge never
			// contradicts its list; the hint carries the total.
			chip(c, tk, fmt.Sprint(len(active)), tk.Accent, tk.AccentSoft)
			mui.Spacer(c)
			hint := "长期目标 · 点一条看它在做什么"
			if total := a.snap.Goals().ActiveCount(); total > len(active) {
				hint = fmt.Sprintf("共 %d 个目标进行中 · 点一条看它在做什么", total)
			}
			mui.Text(c, hint).FontSize(fs(12)).TextColor(tk.TextFaint)
		})
		mui.Box(c).Height(1).Background(tk.BorderSoft)
		for i, g := range active {
			if i > 0 {
				mui.Box(c).Height(1).Background(tk.BorderSoft)
			}
			a.goalStripRow(c, g)
		}
	})
}

// goalStripRow is one active goal on 总览. The whole row is the way to 触发, where
// the complete list is: this is navigation, not an action.
func (a *App) goalStripRow(c *mui.Context, g view.GoalRow) {
	tk := a.tk
	row := mui.Row(c).Padding(11, 16).Gap(12).AlignItems(mui.Center)
	if row.Clicked() {
		a.openGoal(g.ID)
	}
	row.Children(func() {
		mui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
			mui.Text(c, g.Objective).FontSize(fs(15)).SingleLine().TextColor(tk.Text)
			mui.Row(c).Gap(8).AlignItems(mui.Center).Children(func() {
				mui.Text(c, g.ProgressLabel()).FontSize(fs(13)).TextColor(tk.TextMuted)
				if g.Workspace != "" {
					textChip(c, tk, g.Workspace)
				}
			})
		})
		goalBadge(c, tk, g.State)
		mui.Icon(c, icChevR).FontSize(fs(14)).TextColor(tk.TextFaint)
	})
}

// openGoal jumps to the 触发 page's goal list and highlights the goal. It drops a
// search location: two highlights pointing at different things is one too many.
func (a *App) openGoal(id string) {
	a.goalFocus = id
	a.locate = nil
	a.sec = view.SecTrigger
}

// goalSection is the 触发 page's goal list: every goal, whatever its state, so a
// paused or blocked objective is visible instead of silently missing.
func (a *App) goalSection(c *mui.Context) {
	tk := a.tk
	vm := a.snap.Goals()

	mui.Column(c).Width(380).Background(tk.Surface).Radius(tk.Radius).
		Border(1, tk.Border).Children(func() {
		mui.Row(c).Height(34).Padding(0, 14).Gap(8).AlignItems(mui.Center).Children(func() {
			mui.Icon(c, icSpark).FontSize(fs(13)).TextColor(tk.Accent)
			mui.Text(c, "目标").FontSize(fs(15)).Bold().TextColor(tk.Text)
			if n := vm.ActiveCount(); n > 0 {
				chip(c, tk, fmt.Sprintf("%d 个进行中", n), tk.Accent, tk.AccentSoft)
			}
			mui.Spacer(c)
			mui.Text(c, "只读 · 由 agent 设定").FontSize(fs(12)).TextColor(tk.TextFaint)
		})
		mui.Box(c).Height(1).Background(tk.BorderSoft)
		switch {
		case vm == nil:
			a.panelNote(c, "等待运行时发布目标快照…", tk.TextMuted)
		case vm.Empty():
			a.panelNote(c, "还没有长期目标。让它自己定一个目标，或在消息里说清要达成的结果。", tk.TextMuted)
		default:
			mui.Scroll(c).Grow(1).Children(func() {
				mui.Column(c).Children(func() {
					for i, g := range vm.Rows {
						if i > 0 {
							mui.Box(c).Height(1).Background(tk.BorderSoft)
						}
						a.goalRow(c, g, g.ID == a.goalFocus)
					}
				})
			})
		}
	})
}

// goalRow is one goal: the objective, its state and the round it is in. The focused
// one is the goal 总览's strip jumped to.
func (a *App) goalRow(c *mui.Context, g view.GoalRow, focused bool) {
	tk := a.tk
	row := mui.Column(c).Padding(7, 14).Gap(2)
	if focused {
		row.Background(tk.AccentSoft)
	}
	row.Children(func() {
		mui.Row(c).Gap(8).AlignItems(mui.Center).Children(func() {
			mui.Text(c, g.Objective).FontSize(fs(14)).SingleLine().Grow(1).TextColor(tk.Text)
			if focused {
				chip(c, tk, "定位", tk.Accent, tk.Surface)
			}
			goalBadge(c, tk, g.State)
		})
		mui.Row(c).Gap(8).AlignItems(mui.Center).Children(func() {
			mui.Text(c, g.ProgressLabel()).FontSize(fs(12)).TextColor(tk.TextMuted)
			if g.Workspace != "" {
				textChip(c, tk, g.Workspace)
			}
			if g.State == view.GoalBlocked && g.BlockedReason != "" {
				mui.Text(c, "· "+g.BlockedReason).FontSize(fs(12)).SingleLine().TextColor(tk.Red)
			}
		})
	})
}

// goalBadge colors a goal state: an active goal is the only one that needs no
// attention, so only it takes the accent.
func goalBadge(c *mui.Context, tk Tokens, state view.GoalState) {
	fg, bg := tk.TextMuted, tk.SurfaceAlt
	switch state {
	case view.GoalActive:
		fg, bg = tk.Accent, tk.AccentSoft
	case view.GoalPaused:
		fg, bg = tk.Amber, tk.AmberSoft
	case view.GoalBlocked:
		fg, bg = tk.Red, tk.RedSoft
	case view.GoalCompleted:
		fg, bg = tk.Green, tk.GreenSoft
	}
	chip(c, tk, state.Label(), fg, bg)
}
