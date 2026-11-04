package ui

// 收件箱: one place for everything that waits for a person — IM proposals that
// would start work, and Tickets that need an answer / review / decision. The count
// is the one "待你决定 N" number; until the runtime publishes its own attention
// list, the inbox is built from the overview's two lists (so it is never empty
// while the topbar badge says there is something).

import (
	"fmt"

	mui "github.com/egoist/mygo/ui"

	"github.com/zeokeel/super-agent/internal/view"
)

// inboxPage draws 收件箱.
func (a *App) inboxPage(c *mui.Context) {
	tk := a.tk
	ov := a.snap.Overview()
	mui.Column(c).Fill().Padding(24).Gap(12).Children(func() {
		heading(c, tk, "收件箱", "只有这些事需要你：任务的问题、受阻与失败")
		// The runtime's unified attention inbox is the source of truth; the
		// overview's two lists are the fallback (older runtimes, no projection yet).
		if avm := a.snap.Attention(); avm != nil && len(avm.Items) > 0 {
			a.attentionInbox(c, avm)
			return
		}
		if ov == nil {
			mui.Text(c, "等待运行时发布快照…").FontSize(fs(14)).TextColor(tk.TextMuted)
			return
		}
		total := ov.PersonCount()
		mui.Row(c).Gap(10).AlignItems(mui.Center).Children(func() {
			chip(c, tk, view.AttentionLabel(total), tk.Amber, tk.AmberSoft)
			mui.Text(c, "任务的问题、受阻与失败会出现在这里").FontSize(fs(12)).TextColor(tk.TextFaint)
			mui.Spacer(c)
			mui.Text(c, "j / k 选择 · Enter 打开").FontSize(fs(11)).TextColor(tk.TextFaint)
		})
		if len(ov.NeedsPerson) == 0 {
			card(c, tk, 14, func() {
				mui.Text(c, "没有待你决定的事").FontSize(fs(16)).Bold().TextColor(tk.Text)
				mui.Text(c, "新的请求或任务问题会出现在这里；窗口关着时也会发系统通知。").
					FontSize(fs(13)).TextColor(tk.TextMuted)
			})
			return
		}
		selected := a.inboxSel
		mui.Scroll(c).Grow(1).Children(func() {
			mui.Column(c).Gap(10).Children(func() {
				mui.Text(c, "任务需要你").FontSize(fs(13)).Bold().TextColor(tk.TextMuted)
				for i := range ov.NeedsPerson {
					r := ov.NeedsPerson[i]
					a.ticketInboxRow(c, r, selected == i)
				}
			})
		})
	})
}

// attentionInbox is the inbox built from the runtime's attention projection.
func (a *App) attentionInbox(c *mui.Context, avm *view.AttentionVM) {
	tk := a.tk
	mui.Row(c).Gap(10).AlignItems(mui.Center).Children(func() {
		chip(c, tk, view.AttentionLabel(avm.Count()), tk.Amber, tk.AmberSoft)
		mui.Text(c, "任务的问题、受阻与失败会出现在这里").FontSize(fs(12)).TextColor(tk.TextFaint)
	})
	mui.Scroll(c).Grow(1).Children(func() {
		mui.Column(c).Gap(10).Children(func() {
			for _, item := range avm.Items {
				item := item
				if item.Kind == "proposal" {
					// Legacy proposal items are history now: attention-only inbox.
					continue
				}
				card(c, tk, 12, func() {
					mui.Row(c).Gap(10).AlignItems(mui.Start).Children(func() {
						icon := icBell
						if item.Kind == "ticket" {
							icon = icRun
						}
						mui.Row(c).Size(30, 30).Radius(8).Background(tk.AmberSoft).Center().
							Children(func() { mui.Icon(c, icon).FontSize(fs(15)).TextColor(tk.Amber) })
						mui.Column(c).Grow(1).Gap(3).Children(func() {
							mui.Row(c).Gap(8).AlignItems(mui.Center).Children(func() {
								who := item.Sender
								if item.Kind == "proposal" {
									who += " 找你"
								}
								if who != "" {
									mui.Text(c, who).FontSize(fs(14)).Bold().TextColor(tk.Text)
								}
								if item.Source != "" {
									textChip(c, tk, item.Source)
								}
								if item.Workspace != "" {
									textChip(c, tk, item.Workspace)
								}
								if item.Number > 0 {
									mui.Text(c, view.TicketLabel(item.TicketID, item.Number)).
										FontSize(fs(11)).Font("monospace").TextColor(tk.TextFaint)
								}
							})
							mui.Text(c, item.Text).FontSize(fs(13)).TextColor(tk.TextMuted)
						})
						mui.Column(c).Gap(6).AlignItems(mui.End).Children(func() {
							mui.Row(c).Gap(6).Children(func() {
								if item.Has("answer") {
									if row, ok := a.ticketRowByID(item.TicketID); ok && row.Attention != nil {
										if b := mui.Button(c, "回答").Height(26).FontSize(fs(12)); b.Clicked() {
											a.openAnswer(row)
										}
									}
								}
								if item.Has("retry") {
									if row, ok := a.ticketRowByID(item.TicketID); ok {
										if b := mui.Button(c, "重试").Height(26).FontSize(fs(12)); b.Clicked() {
											a.retryRow(row)
										}
									}
								}
								if item.Has("stop") {
									if row, ok := a.ticketRowByID(item.TicketID); ok {
										if b := mui.Button(c, "停止").Height(26).FontSize(fs(12)); b.Clicked() {
											a.openStop(row)
										}
									}
								}
								if item.Has("open") && item.TicketID != "" {
									if b := mui.Button(c, "打开").Height(26).FontSize(fs(12)); b.Clicked() {
										a.openTicket(item.TicketID)
									}
								}
							})
						})
					})
				})
			}
		})
	})
}

// ticketRowByID finds one row in the published lists, for inbox actions.
func (a *App) ticketRowByID(id string) (view.TicketRow, bool) {
	if id == "" {
		return view.TicketRow{}, false
	}
	if vm := a.snap.Tickets(); vm != nil {
		for _, r := range vm.Rows {
			if r.ID == id {
				return r, true
			}
		}
	}
	if ov := a.snap.Overview(); ov != nil {
		for _, r := range ov.NeedsPerson {
			if r.ID == id {
				return r, true
			}
		}
	}
	return view.TicketRow{}, false
}

// ticketInboxRow is one Ticket that needs a person: answer, retry or open.
func (a *App) ticketInboxRow(c *mui.Context, r view.TicketRow, selected bool) {
	tk := a.tk
	card(c, tk, 12, func() {
		mui.Row(c).Gap(10).AlignItems(mui.Center).Children(func() {
			mui.Text(c, view.TicketLabel(r.ID, r.Number)).FontSize(fs(12)).Font("monospace").
				TextColor(tk.TextFaint)
			mui.Column(c).Grow(1).Gap(2).Children(func() {
				mui.Text(c, r.Title).FontSize(fs(14)).TextColor(tk.Text)
				mui.Text(c, r.Detail).FontSize(fs(12)).SingleLine().TextColor(tk.TextMuted)
			})
			chip(c, tk, r.Stage, toneFg(tk, r.Tone), toneBg(tk, r.Tone))
			if r.Attention != nil {
				if b := mui.PrimaryButton(c, "回答").Label("回答").Height(26).FontSize(fs(12)); b.Clicked() {
					a.openAnswer(r)
				}
			}
			if r.CanRetry {
				if b := mui.Button(c, "重试").Label("重试").Height(26).FontSize(fs(12)); b.Clicked() {
					a.retryTicket(r)
				}
			}
			if b := mui.Button(c, "打开").Label("打开").Height(26).FontSize(fs(12)); b.Clicked() {
				a.openTicket(r.ID)
			}
		})
	})
}

// inboxFocus selects a row for j/k navigation; the rows stay real buttons, so the
// keyboard is never the only path.
func (a *App) inboxFocus(key string) { _ = key }

// inboxKeys handles j/k/Enter on the 收件箱 page. Buttons remain focusable, so this
// is an addition, not the only way.
func (a *App) inboxKeys(c *mui.Context) {
	if a.sec != view.SecInbox {
		return
	}
	ov := a.snap.Overview()
	if ov == nil {
		return
	}
	n := len(ov.NeedsPerson)
	if n == 0 {
		return
	}
	if c.Shortcut(0, mui.KeyJ) {
		a.inboxSel = (a.inboxSel + 1) % n
	}
	if c.Shortcut(0, mui.KeyK) {
		a.inboxSel = (a.inboxSel - 1 + n) % n
	}
	if c.Shortcut(0, mui.KeyEnter) {
		if r := ov.NeedsPerson[a.inboxSel]; r.Attention != nil {
			a.openAnswer(r)
		} else {
			a.openTicket(r.ID)
		}
	}
}

// inboxCountLabel is the one wording used by the topbar, sidebar and page.
func inboxCountLabel(n int) string {
	if n <= 0 {
		return ""
	}
	return fmt.Sprint(n)
}
