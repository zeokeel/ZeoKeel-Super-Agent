package ui

import (
	"fmt"
	"strings"

	mui "github.com/egoist/mygo/ui"

	"github.com/zeokeel/super-agent/internal/humanfmt"
	"github.com/zeokeel/super-agent/internal/view"
)

// The Ticket's expanded detail: the full projection when the runtime publishes
// one, the short one plus an honest note when it does not. Opening a row is
// navigation, not an action (design §6.3): the detail never sends a command, and
// every action stays on 总览.
//
// The detail is drawn in place, not as a separate page: design §6.3 says the
// panel has no detail page — a Ticket is read where it appears, on 执行, and a
// ⌘K hit opens the same row (openTicket).

// openTicket navigates to 执行 and opens the Ticket's row, scrolling it into
// view. It is what a 总览 row and a palette hit both do.
func (a *App) openTicket(id string) {
	vm := a.snap.Tickets()
	if vm == nil || id == "" {
		a.actionErr = "没有执行快照，打不开工单 #" + id
		return
	}
	a.ticketFilter = ""
	rows := a.ticketsVisible(vm)
	for i, r := range rows {
		if r.ID == id {
			a.sec = view.SecExecution
			a.locate = nil
			a.goalFocus = ""
			a.actionErr = ""
			a.ticketSel = i
			a.ticketList.ScrollTo(i, mui.Start)
			return
		}
	}
	// The Ticket is not in the published list (a projection from another
	// workspace, or one that just closed): stay where the click was and say so,
	// instead of opening a row at random.
	a.actionErr = "工单 #" + id + " 不在当前的执行投影里（可能刚结束或属于其他工作区）"
}

// ticketDetailEmpty is one missing section's line: the panel says what is not
// projected instead of drawing an empty box as if nothing had happened.
func ticketDetailEmpty(c *mui.Context, tk Tokens, what ...string) {
	mui.Text(c, "暂无数据："+strings.Join(what, " · ")+"（运行时尚未投影）").
		FontSize(10).TextColor(tk.TextFaint)
}

// detailTitle is one section's heading inside the expanded row.
func detailTitle(c *mui.Context, tk Tokens, title string) {
	mui.Text(c, title).FontSize(fs(13)).Bold().TextColor(tk.TextMuted)
}

// ticketDetailRich draws everything the detail projection carries.
func (a *App) ticketDetailRich(c *mui.Context, r view.TicketRow, d *view.TicketDetailVM) {
	tk := a.tk
	mui.Column(c).Padding(10, 16).Gap(9).Background(tk.SurfaceAlt).
		BorderWidth(1, 0, 0, 0).BorderColor(tk.BorderSoft).Children(func() {
		// What it waits for, and — when the projector published it — the
		// question that is waiting to be answered.
		mui.Row(c).Gap(10).AlignItems(mui.Center).Children(func() {
			stage := d.StageLabel
			if stage == "" {
				stage = r.Stage
			}
			chip(c, tk, stage, toneFg(tk, d.Tone), toneBg(tk, d.Tone))
			mui.Text(c, humanfmt.Clip(d.Detail, 100)).FontSize(fs(13)).Grow(1).SingleLine().
				TextColor(tk.Text)
			if d.LastActivity != "" {
				mui.Text(c, "最近活动 "+d.LastActivity).FontSize(fs(12)).SingleLine().
					TextColor(tk.TextFaint)
			}
		})
		if d.Attention != nil {
			mui.Row(c).Padding(8, 10).Radius(7).Background(tk.AmberSoft).Gap(8).
				AlignItems(mui.Start).Children(func() {
				mui.Icon(c, icBell).FontSize(fs(12)).TextColor(tk.Amber)
				mui.Column(c).Grow(1).Gap(2).Children(func() {
					mui.Text(c, "待回答："+d.Attention.Question).FontSize(fs(13)).TextColor(tk.Text)
					if len(d.Attention.Options) > 0 {
						mui.Text(c, "可选："+strings.Join(d.Attention.Options, " · ")).
							FontSize(10).TextColor(tk.TextMuted)
					}
				})
			})
		}

		// Origin: where the work came from, in the panel's words.
		mui.Row(c).Gap(18).AlignItems(mui.Center).Children(func() {
			detailFact(c, tk, "来源", d.Source)
			detailFact(c, tk, "线程", d.Thread)
			detailFact(c, tk, "发起人", d.Actor)
		})

		// Mandate: why the work may be done, and how far it may go.
		detailTitle(c, tk, "授权（Mandate）")
		if d.Mandate == nil {
			ticketDetailEmpty(c, tk, "Mandate 依据")
		} else {
			m := d.Mandate
			line := m.Strength
			if m.PolicyVersion != "" {
				line += " · policy " + m.PolicyVersion
			}
			if m.Basis != "" {
				line += " · 依据 " + m.Basis
			}
			if m.Actor != "" {
				line += " · 由 " + m.Actor + " 的请求授权"
			}
			mui.Text(c, line).FontSize(fs(13)).TextColor(tk.Text)
			if len(m.Rules) > 0 {
				mui.Row(c).Gap(6).AlignItems(mui.Center).Children(func() {
					mui.Text(c, "命中规则").FontSize(fs(12)).TextColor(tk.TextFaint)
					for _, rule := range m.Rules {
						mono(c, tk, rule)
					}
				})
			}
			if m.Scope != "" || m.Workspace != "" {
				mui.Text(c, "范围："+m.Scope+" · 工作区："+m.Workspace).
					FontSize(10).TextColor(tk.TextMuted)
			}
		}

		// Assignments and their reports, oldest first.
		detailTitle(c, tk, fmt.Sprintf("派工与报告（%d）", len(d.Assignments)))
		if len(d.Assignments) == 0 {
			ticketDetailEmpty(c, tk, "Assignments / Reports")
		}
		for _, as := range d.Assignments {
			mui.Column(c).Gap(2).Children(func() {
				line := "#" + as.ID
				if as.Executor != "" {
					line += " · " + as.Executor
				}
				if as.State != "" {
					line += " · " + as.State
				}
				if !as.StartedAt.IsZero() {
					line += " · 开始 " + as.StartedAt.Format("01-02 15:04")
				}
				mui.Text(c, line).FontSize(fs(12)).TextColor(tk.TextMuted)
				if as.Report != nil {
					report := as.Report.State
					if as.Report.Summary != "" {
						report += " · " + as.Report.Summary
					}
					mui.Text(c, "报告："+report).FontSize(fs(12)).TextColor(tk.Text)
					if as.Report.Detail != "" {
						mui.Text(c, as.Report.Detail).FontSize(fs(12)).SingleLine().TextColor(tk.TextFaint)
					}
					if len(as.Report.Artifacts) > 0 {
						mui.Row(c).Gap(6).Children(func() {
							for _, art := range as.Report.Artifacts {
								textChip(c, tk, art.Title+" · "+art.Detail)
							}
						})
					}
				}
			})
		}

		// Inputs: what was delivered to the Ticket, and the three-stage facts.
		detailTitle(c, tk, fmt.Sprintf("输入（%d）", len(d.Inputs)))
		if len(d.Inputs) == 0 {
			ticketDetailEmpty(c, tk, "Inputs")
		}
		for _, in := range d.Inputs {
			mui.Row(c).Gap(6).AlignItems(mui.Center).Children(func() {
				mono(c, tk, in.Kind)
				mui.Text(c, humanfmt.Clip(in.Text, 80)).FontSize(fs(12)).Grow(1).
					SingleLine().TextColor(tk.Text)
				state := "未投递"
				switch {
				case in.Read:
					state = "已读"
				case in.Delivered:
					state = "已投递"
				}
				mui.Text(c, state).FontSize(fs(11)).TextColor(tk.TextFaint)
				if !in.At.IsZero() {
					mui.Text(c, humanfmt.Ago(a.age(in.At))).FontSize(fs(11)).TextColor(tk.TextFaint)
				}
			})
		}

		// Follows: the Threads this Ticket follows, and how they closed.
		detailTitle(c, tk, fmt.Sprintf("订阅线程（%d）", len(d.Follows)))
		if len(d.Follows) == 0 {
			ticketDetailEmpty(c, tk, "Follows")
		}
		for _, f := range d.Follows {
			line := f.Thread
			if f.Own {
				line += " · 本工单线程"
			}
			if f.Outcome != "" {
				line += " · " + f.Outcome
			}
			if f.ClosedAt != nil {
				line += " · 已关闭 " + f.ClosedAt.Format("01-02 15:04")
			}
			mui.Text(c, line).FontSize(fs(12)).TextColor(tk.TextMuted)
		}

		// Artifacts: what the work produced.
		detailTitle(c, tk, fmt.Sprintf("交付物（%d）", len(d.Artifacts)))
		if len(d.Artifacts) == 0 {
			ticketDetailEmpty(c, tk, "Artifacts")
		} else {
			mui.Row(c).Gap(6).Wrap().Children(func() {
				for _, art := range d.Artifacts {
					textChip(c, tk, art.Title+" · "+art.Detail)
				}
			})
		}

		// Delivery: only a failure is a fact worth a line.
		if d.Delivery != nil {
			mui.Text(c, fmt.Sprintf("投递失败 %d 次 · %s", d.Delivery.Attempts, d.Delivery.LastError)).
				FontSize(10).TextColor(tk.Red)
		}

		// One honest summary of what the projector has not filled in, so an
		// empty section is never read as "nothing happened".
		if missing := d.EmptySections(); len(missing) > 0 {
			mui.Text(c, "暂无数据："+strings.Join(missing, " · ")).
				FontSize(10).TextColor(tk.TextFaint)
		}
	})
}
