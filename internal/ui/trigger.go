package ui

import (
	"fmt"

	mui "github.com/egoist/mygo/ui"

	"github.com/zeokeel/super-agent/internal/humanfmt"
	"github.com/zeokeel/super-agent/internal/view"
)

// triggerPage answers "什么会让我开工": the Threads that arrived and what happened
// to them, plus the two things that start work without a message — timed triggers
// and long-running goals. Everything here is read-only: external text is data,
// never an instruction, and the actions live on 总览 or 设置 (design §6.3).
func (a *App) triggerPage(c *mui.Context) {
	tk := a.tk
	vm := a.snap.Threads()

	mui.Column(c).Fill().Padding(20, 24).Gap(12).Children(func() {
		mui.Row(c).AlignItems(mui.Center).Gap(8).Children(func() {
			heading(c, tk, "触发", "线程、处置与决策；外部文本永远只是数据，不是指令")
			mui.Spacer(c)
			if vm != nil {
				chip(c, tk, fmt.Sprintf("已订阅 %d", vm.Subscribed), tk.Accent, tk.AccentSoft)
				chip(c, tk, fmt.Sprintf("评估中 %d", vm.Evaluating), tk.TextMuted, tk.SurfaceAlt)
				chip(c, tk, fmt.Sprintf("24h 新增 %d", vm.New24h), tk.TextMuted, tk.SurfaceAlt)
			}
			if svm := a.snap.Schedules(); !svm.Empty() {
				chip(c, tk, fmt.Sprintf("%d 个定时", len(svm.Rows)), tk.Violet, tk.VioletSoft)
			}
			if n := a.snap.Goals().ActiveCount(); n > 0 {
				chip(c, tk, fmt.Sprintf("%d 个目标进行中", n), tk.Accent, tk.AccentSoft)
			}
		})
		switch {
		case vm == nil:
			mui.Text(c, "等待运行时发布触发快照…").FontSize(fs(15)).TextColor(tk.TextMuted)
		case len(vm.Threads) == 0:
			mui.Text(c, "还没有线程。来源收到消息后，线程会出现在这里。").FontSize(fs(15)).
				TextColor(tk.TextMuted)
		default:
			mui.Row(c).Grow(1).MinHeight(0).AlignItems(mui.Stretch).Gap(16).Children(func() {
				a.threadColumn(c, vm)
				a.threadTimeline(c, vm)
			})
		}
		// What starts work by itself: each is its own read-only card, and each
		// says where its configuration lives.
		mui.Row(c).Height(236).AlignItems(mui.Stretch).Gap(16).Children(func() {
			a.scheduleSection(c)
			a.goalSection(c)
		})
	})
}

// threadColumn is the virtualized Thread list. It is a ui.List because a Thread
// inbox grows without bound; only the rows in view are built.
func (a *App) threadColumn(c *mui.Context, vm *view.ThreadVM) {
	tk := a.tk
	rows := vm.Threads
	if a.threadSel >= len(rows) {
		a.threadSel = len(rows) - 1
	}
	if a.threadSel < -1 {
		a.threadSel = -1
	}

	mui.Column(c).Width(430).Background(tk.Surface).Radius(tk.Radius).Border(1, tk.Border).
		Children(func() {
			mui.Row(c).Height(36).Padding(0, 14).Gap(8).AlignItems(mui.Center).Children(func() {
				mui.Text(c, "线程").FontSize(fs(15)).Bold().TextColor(tk.Text)
				chip(c, tk, fmt.Sprint(len(rows)), tk.TextMuted, tk.SurfaceAlt)
				mui.Spacer(c)
				mui.Text(c, "按最近活动排序").FontSize(fs(12)).TextColor(tk.TextFaint)
			})
			mui.Box(c).Height(1).Background(tk.BorderSoft)
			// The chosen row lives in app state: the list keeps its place across
			// frames without an element being stored (design §6.1, rule 2).
			a.threadList.Selected = &a.threadSel
			// Keyed by the Thread's ID: the inbox is sorted by recent activity, so
			// any new event reorders it. With a key the choice follows its Thread
			// instead of staying on a row number (ui/list.go:42-47).
			a.threadList.Key = func(i int) any { return rows[i].ID }
			lst := mui.List(c, &a.threadList, len(rows), func(i int) {
				a.threadRow(c, rows[i], i == a.threadSel)
			}).Grow(1)
			// Apply this frame's input before reading the choice for the detail
			// pane, so one click opens the thread it landed on in the same frame.
			lst.Changed()
		})
}

// threadRow is one Thread: where it came from, what it says, what it is doing.
// on is the list's chosen row: the list paints it in the accent color, so the
// content switches to the colors that stay readable there.
func (a *App) threadRow(c *mui.Context, r view.ThreadRow, on bool) {
	tk := a.tk
	mui.Row(c).Padding(11, 12).Gap(10).AlignItems(mui.Start).Children(func() {
		sourceGlyph(c, tk, r.SourceIcon, 28)
		mui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
			mui.Row(c).Gap(6).AlignItems(mui.Center).Children(func() {
				mui.Text(c, r.Title).FontSize(fs(14)).SingleLine().Grow(1).
					TextColor(ifColor(on, tk.Surface, tk.Text))
				chipOn(c, tk, r.Stage, on, toneFg(tk, r.Tone), toneBg(tk, r.Tone))
			})
			mui.Row(c).Gap(6).AlignItems(mui.Center).Children(func() {
				mui.Text(c, r.Source).FontSize(fs(12)).
					TextColor(ifColor(on, tk.AccentSoft, tk.TextFaint))
				if r.Who != "" {
					mui.Text(c, "· "+r.Who).FontSize(fs(12)).
						TextColor(ifColor(on, tk.AccentSoft, tk.TextFaint))
				}
				mui.Spacer(c)
				if r.Subscribed {
					mui.Text(c, "已订阅").FontSize(fs(12)).
						TextColor(ifColor(on, tk.Surface, tk.Accent))
				}
				if r.TicketID != "" {
					mui.Text(c, "#"+r.TicketID).FontSize(fs(12)).
						TextColor(ifColor(on, tk.AccentSoft, tk.TextMuted))
				}
				mui.Text(c, humanfmt.Ago(a.age(r.ActivityAt))).FontSize(fs(12)).
					TextColor(ifColor(on, tk.AccentSoft, tk.TextFaint))
			})
		})
	})
}

// threadTimeline is the chosen Thread: its triggers, dispositions and decisions,
// newest first, with the code that explains each one.
func (a *App) threadTimeline(c *mui.Context, vm *view.ThreadVM) {
	tk := a.tk

	var row view.ThreadRow
	var detail *view.ThreadDetail
	if a.threadSel >= 0 && a.threadSel < len(vm.Threads) {
		row = vm.Threads[a.threadSel]
		detail = vm.DetailOf(row.ID)
	}
	if detail == nil {
		detail = vm.SelectedDetail()
	}
	if detail == nil {
		msg := "选择一个线程查看它的时间线。"
		if len(vm.Threads) > 0 {
			msg = "这个线程的时间线还没有投影。"
		}
		mui.Text(c, msg).FontSize(fs(15)).TextColor(tk.TextMuted)
		return
	}

	mui.Column(c).Grow(1).MinWidth(0).Background(tk.Surface).Radius(tk.Radius).
		Border(1, tk.Border).Children(func() {
		mui.Column(c).Padding(14, 16).Gap(4).Children(func() {
			mui.Row(c).Gap(8).AlignItems(mui.Center).Children(func() {
				mui.Text(c, detail.Title).FontSize(fs(16)).Bold().SingleLine().Grow(1).
					TextColor(tk.Text)
				if detail.Subscribed {
					chip(c, tk, "已订阅", tk.Accent, tk.AccentSoft)
				} else {
					chip(c, tk, "未订阅", tk.TextMuted, tk.SurfaceAlt)
				}
				if detail.TicketID != "" {
					chip(c, tk, "Ticket #"+detail.TicketID, tk.Violet, tk.VioletSoft)
				}
				if detail.Workspace != "" {
					textChip(c, tk, detail.Workspace)
				}
			})
			mui.Text(c, detail.Meta).FontSize(fs(13)).TextColor(tk.TextMuted)
		})
		mui.Box(c).Height(1).Background(tk.BorderSoft)
		mui.Scroll(c).Grow(1).Children(func() {
			mui.Column(c).Padding(12, 16).Gap(2).Children(func() {
				for _, e := range detail.Events {
					a.threadEvent(c, e)
				}
			})
		})
		if detail.CursorNote != "" || detail.PolicyNote != "" {
			mui.Box(c).Height(1).Background(tk.BorderSoft)
			mui.Row(c).Padding(9, 14).Gap(14).AlignItems(mui.Center).Children(func() {
				if detail.PolicyNote != "" {
					mui.Icon(c, icShield).FontSize(fs(12)).TextColor(tk.Green)
					mui.Text(c, detail.PolicyNote).FontSize(fs(12)).Grow(1).TextColor(tk.TextMuted)
				} else {
					mui.Spacer(c)
				}
				if detail.CursorNote != "" {
					mui.Text(c, detail.CursorNote).FontSize(fs(12)).TextColor(tk.TextFaint)
				}
			})
		}
	})
}

// threadEvent is one row of the timeline: the event, the kind, and — when the
// core recorded one — the reason code plus its rendered template.
func (a *App) threadEvent(c *mui.Context, e view.ThreadEvent) {
	tk := a.tk
	icon, fg, bg := icTrigger, tk.Accent, tk.AccentSoft
	label := "触发"
	switch e.Kind {
	case view.EventDecision:
		icon, fg, bg, label = icEval, tk.Violet, tk.VioletSoft, "评估"
	case view.EventDisposition:
		icon, fg, bg, label = icCheck, tk.Green, tk.GreenSoft, "处置"
	case view.EventContext:
		icon, fg, bg, label = icFile, tk.TextMuted, tk.SurfaceAlt, "上下文"
	}
	mui.Row(c).Padding(8, 8).Gap(10).AlignItems(mui.Start).Children(func() {
		mui.Row(c).Size(24, 24).Radius(6).Background(bg).Center().Children(func() {
			mui.Icon(c, icon).FontSize(fs(12)).TextColor(fg)
		})
		mui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
			mui.Row(c).Gap(8).AlignItems(mui.Center).Children(func() {
				mui.Text(c, e.Title).FontSize(fs(14)).SingleLine().Grow(1).TextColor(tk.Text)
				chip(c, tk, label, fg, bg)
			})
			mui.Row(c).Gap(8).AlignItems(mui.Center).Children(func() {
				if e.Code != "" {
					mono(c, tk, e.Code)
				}
				if e.Text != "" {
					mui.Text(c, e.Text).FontSize(fs(12)).SingleLine().Grow(1).TextColor(tk.TextMuted)
				}
				if e.Decision != "" {
					mui.Text(c, e.Decision).FontSize(fs(12)).TextColor(tk.TextFaint)
				}
				mui.Text(c, e.At.Format("15:04")).FontSize(fs(12)).TextColor(tk.TextFaint)
			})
		})
	})
}

// mono is a small code chip: a reason or disposition code, shown as itself so it
// can be checked against the rule table (001 / 002: 码 + 模板).
func mono(c *mui.Context, tk Tokens, s string) {
	mui.Row(c).Height(18).Padding(0, 6).Radius(5).Background(tk.SurfaceAlt).
		Border(1, tk.BorderSoft).AlignItems(mui.Center).Children(func() {
		mui.Text(c, s).FontSize(fs(12)).Font("monospace").TextColor(tk.TextMuted)
	})
}
