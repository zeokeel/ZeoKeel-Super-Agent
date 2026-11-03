package ui

import (
	"fmt"

	"github.com/dustin/go-humanize"
	mui "github.com/egoist/mygo/ui"

	"github.com/zeokeel/super-agent/internal/humanfmt"
	"github.com/zeokeel/super-agent/internal/view"
)

// overview answers the three questions in order: does anything need me, is the
// system healthy, what happened (design §6.3 / 005 Panel).
func (a *App) overview(c *mui.Context, ov *view.OverviewVM) {
	tk := a.tk
	if ov == nil {
		mui.Column(c).Fill().Padding(32).Children(func() {
			mui.Text(c, "等待运行时发布快照…").FontSize(fs(15)).TextColor(tk.TextMuted)
		})
		return
	}

	mui.Column(c).Fill().Padding(24).Gap(16).Children(func() {
		heading(c, tk, "总览", fmt.Sprintf("需要你关注的只有 %d 件事，其余都在后台运行", ov.PersonCount()))
		a.chatPanel(c)
		a.approvals(c, ov)
		a.needsPerson(c, ov)
		a.goalsStrip(c)
		mui.Row(c).Gap(16).AlignItems(mui.Start).Children(func() {
			// left column: health and activity
			mui.Column(c).Grow(2).Gap(16).Children(func() {
				mui.Row(c).Gap(16).Children(func() {
					a.health(c, ov)
					a.activity(c, ov)
				})
			})
			// right column: runtime, artifacts, storage
			mui.Column(c).Width(320).Gap(16).Children(func() {
				a.runtime(c, ov)
				a.artifacts(c, ov)
				a.storage(c, ov)
			})
		})
	})
}

// approvals lists requests that would start work but need the operator first.
// They are not Tickets, and they are deliberately a different band from
// "needs me", because the question is different: should this happen at all?
func (a *App) approvals(c *mui.Context, ov *view.OverviewVM) {
	tk := a.tk
	if len(ov.Approvals) == 0 {
		return
	}
	card(c, tk, 0, func() {
		mui.Row(c).Padding(14, 16).Gap(8).AlignItems(mui.Center).Children(func() {
			mui.Icon(c, icBell).FontSize(fs(15)).TextColor(tk.Amber)
			mui.Text(c, "待你批准").FontSize(fs(17)).Bold().TextColor(tk.Text)
			chip(c, tk, fmt.Sprint(len(ov.Approvals)), tk.Amber, tk.AmberSoft)
			mui.Spacer(c)
			mui.Text(c, "批准后才会开工").FontSize(fs(13)).TextColor(tk.TextFaint)
		})
		mui.Box(c).Height(1).Background(tk.BorderSoft)
		for i, ap := range ov.Approvals {
			if i > 0 {
				mui.Box(c).Height(1).Background(tk.BorderSoft)
			}
			a.approvalRow(c, ap)
		}
	})
}

// approvalRow is one request: who, what they said, what would be done.
func (a *App) approvalRow(c *mui.Context, ap view.ApprovalRow) {
	tk := a.tk
	mui.Row(c).Padding(14, 16).Gap(12).AlignItems(mui.Start).Children(func() {
		mui.Row(c).Size(28, 28).Radius(8).Background(tk.AmberSoft).Center().
			Children(func() { mui.Icon(c, icBell).FontSize(fs(14)).TextColor(tk.Amber) })
		mui.Column(c).Grow(1).Gap(3).Children(func() {
			mui.Row(c).Gap(8).AlignItems(mui.Center).Children(func() {
				mui.Text(c, ap.From+" 找你").FontSize(fs(15)).TextColor(tk.Text)
				textChip(c, tk, ap.Source)
				textChip(c, tk, ap.Workspace)
			})
			mui.Text(c, "他说："+ap.Said).FontSize(fs(14)).SingleLine().TextColor(tk.TextMuted)
			mui.Text(c, "要做："+ap.Intent).FontSize(fs(14)).SingleLine().TextColor(tk.Text)
			mui.Row(c).Gap(6).AlignItems(mui.Center).Children(func() {
				mui.Text(c, "产出："+ap.Deliverable).FontSize(fs(13)).TextColor(tk.TextFaint)
				for _, s := range ap.Sensitive {
					mui.Text(c, "⚠ "+s).FontSize(fs(13)).TextColor(tk.Red)
				}
			})
		})
		mui.Column(c).Gap(6).AlignItems(mui.End).Children(func() {
			mui.Row(c).Gap(6).Children(func() {
				if mui.PrimaryButton(c, "批准").Label("批准").Height(24).Clicked() {
					a.approve(ap.DecisionID)
				}
				if mui.Button(c, "拒绝").Label("拒绝").Height(24).Clicked() {
					a.reject(ap.DecisionID)
				}
			})
			mui.Text(c, humanfmt.Ago(ap.Age)).FontSize(fs(12)).TextColor(tk.TextFaint)
		})
	})
}

func (a *App) needsPerson(c *mui.Context, ov *view.OverviewVM) {
	tk := a.tk
	card(c, tk, 0, func() {
		mui.Row(c).Padding(14, 16).Gap(8).AlignItems(mui.Center).Children(func() {
			mui.Text(c, "需要我处理").FontSize(fs(17)).Bold().TextColor(tk.Text)
			chip(c, tk, fmt.Sprint(len(ov.NeedsPerson)), tk.Amber, tk.AmberSoft)
			mui.Spacer(c)
			mui.Text(c, "按等待时长排序").FontSize(fs(13)).TextColor(tk.TextFaint)
		})
		// An action's outcome is reported even when no row is left: the band is
		// where the action was offered, so it is where its error belongs.
		if a.actionErr != "" {
			mui.Row(c).Padding(0, 16).Children(func() {
				mui.Text(c, a.actionErr).FontSize(fs(12)).TextColor(tk.Red)
			})
		}
		if len(ov.NeedsPerson) == 0 {
			mui.Row(c).Padding(16, 16).Children(func() {
				mui.Text(c, "没有需要你处理的事。").FontSize(fs(15)).TextColor(tk.TextMuted)
			})
			return
		}
		// A row whose action cannot be sent says why: the buttons are disabled
		// and this line names the interface the runtime is missing.
		if note := a.missingActionsNote(ov.NeedsPerson); note != "" {
			mui.Row(c).Padding(0, 16).Children(func() {
				mui.Text(c, note).FontSize(fs(12)).TextColor(tk.Amber)
			})
		}
		mui.Box(c).Height(1).Background(tk.BorderSoft)
		for i, row := range ov.NeedsPerson {
			if i > 0 {
				mui.Box(c).Height(1).Background(tk.BorderSoft)
			}
			a.personRow(c, row, true)
		}
	})
}

// personRow is one Ticket on 总览. The text block opens the Ticket on 执行 (that
// is navigation), and only the 需要我处理 rows carry actions: a work-item action
// exists nowhere else (design §6.3).
func (a *App) personRow(c *mui.Context, r view.TicketRow, actions bool) {
	tk := a.tk
	mui.Row(c).Padding(14, 16).Gap(12).AlignItems(mui.Center).Children(func() {
		a.sourceChip(c, r)
		mui.Row(c).Grow(1).OnClick(func() { a.openTicket(r.ID) }).Children(func() {
			mui.Column(c).Grow(1).Gap(2).Children(func() {
				mui.Row(c).Gap(8).AlignItems(mui.Center).Children(func() {
					mui.Text(c, fmt.Sprintf("%s  %s", view.TicketLabel(r.ID, r.Number), r.Title)).FontSize(fs(15)).
						SingleLine().TextColor(tk.Text)
					textChip(c, tk, r.Workspace)
				})
				mui.Text(c, r.Detail).FontSize(fs(13)).SingleLine().TextColor(tk.TextMuted)
			})
		})
		chip(c, tk, r.Stage, toneFg(tk, r.Tone), toneBg(tk, r.Tone))
		mui.Text(c, humanfmt.Age(r.Age)).FontSize(fs(13)).TextColor(tk.TextFaint)
		if actions {
			a.overviewActions(c, r)
		}
		mui.Icon(c, icChevR).FontSize(fs(14)).TextColor(tk.TextFaint)
	})
}

func (a *App) recent(c *mui.Context, ov *view.OverviewVM) {
	tk := a.tk
	if len(ov.Recent) == 0 {
		return
	}
	card(c, tk, 0, func() {
		mui.Row(c).Padding(14, 16).Gap(8).AlignItems(mui.Center).Children(func() {
			mui.Text(c, "我派的任务").FontSize(fs(17)).Bold().TextColor(tk.Text)
			chip(c, tk, fmt.Sprint(len(ov.Recent)), tk.TextMuted, tk.SurfaceAlt)
			mui.Spacer(c)
			mui.Text(c, "按最近活动排序").FontSize(fs(13)).TextColor(tk.TextFaint)
		})
		mui.Box(c).Height(1).Background(tk.BorderSoft)
		for i, row := range ov.Recent {
			if i > 0 {
				mui.Box(c).Height(1).Background(tk.BorderSoft)
			}
			a.personRow(c, row, false)
		}
	})
}

// healthProblems counts the parts of the system that need a person. The health
// card and the runtime card read the same number, so "正常" cannot sit beside a
// failing part (design §6.5).
func healthProblems(ov *view.OverviewVM) int {
	bad := 0
	for _, h := range ov.Health {
		if !h.OK {
			bad++
		}
	}
	return bad
}

func (a *App) health(c *mui.Context, ov *view.OverviewVM) {
	tk := a.tk
	bad := healthProblems(ov)
	cardG(c, tk, 16, func() {
		mui.Row(c).Gap(8).AlignItems(mui.Center).Children(func() {
			mui.Text(c, "系统健康").FontSize(fs(16)).Bold().TextColor(tk.Text)
			if bad > 0 {
				chip(c, tk, fmt.Sprintf("%d 项待处理", bad), tk.Amber, tk.AmberSoft)
			}
		})
		for _, h := range ov.Health {
			mui.Row(c).Gap(10).AlignItems(mui.Center).Children(func() {
				dot(c, ifColor(h.OK, tk.Green, tk.Amber), 7)
				mui.Text(c, h.Name).FontSize(fs(14)).TextColor(tk.Text)
				mui.Spacer(c)
				mui.Text(c, h.Detail).FontSize(fs(13)).TextColor(tk.TextMuted)
			})
		}
	})
}

func (a *App) activity(c *mui.Context, ov *view.OverviewVM) {
	tk := a.tk
	cardG(c, tk, 16, func() {
		mui.Row(c).Gap(8).AlignItems(mui.Center).Children(func() {
			mui.Text(c, "过去 24 小时").FontSize(fs(16)).Bold().TextColor(tk.Text)
			mui.Spacer(c)
			mui.Text(c, fmt.Sprintf("%d Triggers", ov.Counts.Triggers)).FontSize(fs(13)).
				TextColor(tk.TextFaint)
		})
		// A bar per hour; the tallest bar sets the scale so a quiet day still reads.
		max := 1
		for _, h := range ov.Activity {
			if h.Count > max {
				max = h.Count
			}
		}
		mui.Row(c).Height(96).Gap(3).AlignItems(mui.End).Children(func() {
			for _, h := range ov.Activity {
				hh := 8 + 84*float32(h.Count)/float32(max)
				mui.Column(c).Grow(1).AlignItems(mui.Center).Children(func() {
					mui.Box(c).Width(10).Height(hh).Radius(3).
						Background(ifColor(h.Count*2 >= max, tk.Accent, tk.AccentSoft))
				})
			}
		})
		mui.Row(c).Gap(18).Children(func() {
			stat(c, tk, "Triggers", ov.Counts.Triggers)
			stat(c, tk, "Eval", ov.Counts.Evals)
			stat(c, tk, "Decision", ov.Counts.Decisions)
			stat(c, tk, "Ticket", ov.Counts.Tickets)
		})
	})
}

func (a *App) runtime(c *mui.Context, ov *view.OverviewVM) {
	tk := a.tk
	card(c, tk, 16, func() {
		mui.Row(c).Gap(8).AlignItems(mui.Center).Children(func() {
			mui.Text(c, "运行时").FontSize(fs(16)).Bold().TextColor(tk.Text)
			mui.Spacer(c)
			// The runtime card says what the health rows say: it used to read
			// "正常" while a source's token had expired (design §6.5).
			if bad := healthProblems(ov); bad > 0 {
				chip(c, tk, fmt.Sprintf("%d 项需关注", bad), tk.Amber, tk.AmberSoft)
			} else {
				chip(c, tk, "正常", tk.Green, tk.GreenSoft)
			}
		})
		mui.Row(c).Gap(6).AlignItems(mui.Baseline).Children(func() {
			mui.Text(c, fmt.Sprint(ov.Runtime.Running)).FontSize(fs(28)).Bold().TextColor(tk.Text)
			mui.Text(c, fmt.Sprintf("/ %d executor 忙", ov.Runtime.MaxJobs)).FontSize(fs(14)).
				TextColor(tk.TextMuted)
		})
		mui.Row(c).Height(6).Radius(3).Background(tk.SurfaceAlt).Children(func() {
			w := float32(0)
			if ov.Runtime.MaxJobs > 0 {
				w = float32(ov.Runtime.Running) / float32(ov.Runtime.MaxJobs)
			}
			mui.Box(c).Width(w * 286).Height(6).Radius(3).Background(tk.Accent)
		})
		usage := "还没有调用模型"
		if ov.Usage.Calls > 0 {
			usage = fmt.Sprintf("%d 次调用 · %s tokens（本次运行）",
				ov.Usage.Calls, humanize.Comma(int64(ov.Usage.Total)))
			if ov.Usage.Prompt > 0 {
				hit := 100 * ov.Usage.Cached / ov.Usage.Prompt
				usage += fmt.Sprintf(" · 缓存 %d%%", hit)
			}
		}
		mui.Text(c, fmt.Sprintf("队列 %d · 今日运行 %d 次", ov.Runtime.Queued, ov.Runtime.Today)).
			FontSize(10).TextColor(tk.TextFaint)
		mui.Text(c, usage).
			FontSize(11).TextColor(tk.TextMuted)
	})
}

func (a *App) artifacts(c *mui.Context, ov *view.OverviewVM) {
	tk := a.tk
	card(c, tk, 16, func() {
		mui.Row(c).Gap(8).AlignItems(mui.Center).Children(func() {
			mui.Text(c, "最近交付物").FontSize(fs(16)).Bold().TextColor(tk.Text)
			mui.Spacer(c)
			mui.Text(c, "全部").FontSize(fs(13)).TextColor(tk.Accent).
				OnClick(func() { a.artifactsOpen = true })
		})
		if len(ov.Artifacts) == 0 {
			mui.Text(c, "还没有交付物。工单产出后会出现在这里。").FontSize(fs(13)).
				TextColor(tk.TextFaint)
		}
		for _, art := range ov.Artifacts {
			mui.Row(c).Padding(6, 0).Gap(10).AlignItems(mui.Center).Children(func() {
				iconForArtifact(c, tk, art.Kind)
				mui.Column(c).Grow(1).Gap(1).Children(func() {
					mui.Text(c, art.Title).FontSize(fs(14)).TextColor(tk.Text)
					mui.Text(c, art.Detail).FontSize(fs(13)).TextColor(tk.TextFaint)
				})
				mui.Icon(c, icChevR).FontSize(fs(13)).TextColor(tk.TextFaint)
			})
		}
	})
}

func (a *App) storage(c *mui.Context, ov *view.OverviewVM) {
	tk := a.tk
	card(c, tk, 16, func() {
		mui.Text(c, "存储与保留").FontSize(fs(16)).Bold().TextColor(tk.Text)
		kv(c, tk, "SQLite", ov.Storage.DB)
		events := "—"
		if ov.Storage.Events >= 0 {
			events = fmt.Sprintf("%d 条", ov.Storage.Events)
		}
		kv(c, tk, "会话事件", events)
		kv(c, tk, "上次备份", ov.Storage.LastBackup)
	})
}

func kv(c *mui.Context, tk Tokens, k, v string) {
	mui.Row(c).Gap(8).Children(func() {
		mui.Text(c, k).FontSize(fs(14)).TextColor(tk.TextMuted)
		mui.Spacer(c)
		mui.Text(c, v).FontSize(fs(14)).TextColor(tk.Text)
	})
}

func stat(c *mui.Context, tk Tokens, label string, n int) {
	mui.Column(c).Gap(1).Children(func() {
		mui.Text(c, fmt.Sprint(n)).FontSize(fs(20)).Bold().TextColor(tk.Text)
		mui.Text(c, label).FontSize(fs(13)).TextColor(tk.TextFaint)
	})
}

func iconForArtifact(c *mui.Context, tk Tokens, kind string) {
	icon := icFile
	fg, bg := tk.Violet, tk.VioletSoft
	switch kind {
	case "pr":
		icon, fg, bg = icBranch, tk.Accent, tk.AccentSoft
	case "image":
		icon, fg, bg = icImage, tk.Cyan, tk.SurfaceAlt
	case "report":
		icon, fg, bg = icFile, tk.Violet, tk.VioletSoft
	}
	mui.Row(c).Size(28, 28).Radius(7).Background(bg).Center().Children(func() {
		mui.Icon(c, icon).FontSize(fs(14)).TextColor(fg)
	})
}

// sourceChip is the small square at the start of a row: which Source the work
// came from. The glyph itself is shared with the source page.
func (a *App) sourceChip(c *mui.Context, r view.TicketRow) {
	sourceGlyph(c, a.tk, r.SourceIcon, 28)
}

func toneFg(tk Tokens, tone view.Tone) mui.Color {
	switch tone {
	case view.ToneWarn:
		return tk.Amber
	case view.ToneDanger:
		return tk.Red
	case view.ToneInfo:
		return tk.Accent
	case view.ToneOK:
		return tk.Green
	}
	return tk.TextMuted
}

func toneBg(tk Tokens, tone view.Tone) mui.Color {
	switch tone {
	case view.ToneWarn:
		return tk.AmberSoft
	case view.ToneDanger:
		return tk.RedSoft
	case view.ToneInfo:
		return tk.AccentSoft
	case view.ToneOK:
		return tk.GreenSoft
	}
	return tk.SurfaceAlt
}

// approve accepts a proposal. This is the only place the action exists: the
// message stream records it, the dashboard decides it (005 Panel, "one place per
// concept").
func (a *App) approve(decisionID string) {
	if a.cmd == nil || decisionID == "" {
		return
	}
	a.composerErr = ""
	if err := a.cmd.Approve(decisionID); err != nil {
		a.composerErr = err.Error()
	}
}

// reject turns a proposal down.
func (a *App) reject(decisionID string) {
	if a.cmd == nil || decisionID == "" {
		return
	}
	a.composerErr = ""
	if err := a.cmd.Reject(decisionID); err != nil {
		a.composerErr = err.Error()
	}
}
