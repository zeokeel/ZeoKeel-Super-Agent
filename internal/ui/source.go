package ui

import (
	"fmt"
	"time"

	mui "github.com/egoist/mygo/ui"

	"github.com/zeokeel/super-agent/internal/humanfmt"
	"github.com/zeokeel/super-agent/internal/view"
)

// sourcePage answers one question per Source: what is it, is it on, what are its
// account facts, where is the cursor, and what did the last poll do? It is
// read-only inspection: configuration lives on the settings page (design §6.3,
// one concept one place), so a failure shows the actionable line here and the way
// to the form, never the form itself.
func (a *App) sourcePage(c *mui.Context) {
	tk := a.tk
	vm := a.snap.Sources()

	mui.Column(c).Fill().Padding(20, 24).Gap(14).Children(func() {
		mui.Row(c).AlignItems(mui.Center).Gap(10).Children(func() {
			heading(c, tk, "来源", "每个事件源各管各的账号与游标；过滤在这里，Agent 之前")
			mui.Spacer(c)
			if vm != nil {
				chip(c, tk, fmt.Sprintf("%d 个已启用", vm.EnabledCount()), tk.Green, tk.GreenSoft)
				if n := vm.FailingCount(); n > 0 {
					chip(c, tk, fmt.Sprintf("%d 个需要你处理", n), tk.Amber, tk.AmberSoft)
				}
			}
		})
		if vm == nil {
			mui.Text(c, "等待运行时发布来源快照…").FontSize(fs(15)).TextColor(tk.TextMuted)
			return
		}

		mui.Row(c).Grow(1).AlignItems(mui.Stretch).Gap(16).Children(func() {
			mui.Column(c).Grow(1).MinWidth(0).Children(func() { a.sourceTable(c, vm) })
			mui.Column(c).Width(330).Gap(12).Children(func() {
				a.sourceFilters(c, vm)
				a.sourceAccountFacts(c, vm)
				a.settingsHint(c, view.SettingsSource, "设置 › 来源")
			})
		})
	})
}

// sourceTable is one row per Source: a status list, not a wall of cards.
func (a *App) sourceTable(c *mui.Context, vm *view.SourceVM) {
	tk := a.tk
	mui.Column(c).Fill().Background(tk.Surface).Radius(tk.Radius).Border(1, tk.Border).
		Children(func() {
			mui.Row(c).Height(34).Padding(0, 14).Gap(10).AlignItems(mui.Center).
				Background(tk.SurfaceAlt).Children(func() {
				mui.Text(c, "事件源").FontSize(fs(12)).TextColor(tk.TextFaint)
				mui.Spacer(c)
				mui.Text(c, "每行：账号事实 · 游标 · 最近一次轮询").FontSize(fs(12)).
					TextColor(tk.TextFaint)
			})
			mui.Box(c).Height(1).Background(tk.Border)
			if len(vm.Sources) == 0 {
				mui.Row(c).Padding(16, 14).Children(func() {
					mui.Text(c, "还没有登记任何来源。").FontSize(fs(14)).TextColor(tk.TextMuted)
				})
				return
			}
			mui.Scroll(c).Grow(1).Children(func() {
				mui.Column(c).Children(func() {
					for i, s := range vm.Sources {
						if i > 0 {
							mui.Box(c).Height(1).Background(tk.BorderSoft)
						}
						a.sourceRow(c, s)
					}
				})
			})
		})
}

// sourceRow is one Source: identity and state on the first line, the account
// facts on the second, the cursor and the last poll on the third, and — only
// when the poll failed — the actionable line.
func (a *App) sourceRow(c *mui.Context, s view.SourceRow) {
	tk := a.tk
	located := a.located(view.SearchSources, s.Kind)
	row := mui.Column(c).Padding(12, 14).Gap(7)
	if located {
		row.Background(tk.AccentSoft)
	}
	row.Children(func() {
		mui.Row(c).Gap(10).AlignItems(mui.Center).Children(func() {
			sourceGlyph(c, tk, s.Kind, 30)
			mui.Column(c).Width(190).Gap(1).Children(func() {
				mui.Text(c, s.Name).FontSize(fs(15)).Bold().TextColor(tk.Text)
				mui.Text(c, s.Account).FontSize(fs(13)).SingleLine().TextColor(tk.TextMuted)
			})
			if s.Enabled {
				chip(c, tk, "已启用", tk.Green, tk.GreenSoft)
			} else {
				chip(c, tk, "已停用", tk.Red, tk.RedSoft)
			}
			a.pollChip(c, s.LastPoll)
			if located {
				chip(c, tk, "定位", tk.Accent, tk.Surface)
			}
			mui.Spacer(c)
			mui.Text(c, fmt.Sprintf("24h 触发 %d · 丢弃 %d", s.Triggers24h, s.Filtered24h)).
				FontSize(11).TextColor(tk.TextFaint)
		})

		if len(s.Facts) > 0 {
			mui.Row(c).Gap(20).Children(func() {
				mui.Box(c).Width(30) // aligns under the glyph
				for _, f := range s.Facts {
					mui.Row(c).Gap(6).Children(func() {
						mui.Text(c, f.Label).FontSize(fs(13)).TextColor(tk.TextFaint)
						mui.Text(c, f.Value).FontSize(fs(13)).SingleLine().TextColor(tk.Text)
					})
				}
			})
		}

		mui.Row(c).Gap(20).AlignItems(mui.Center).Children(func() {
			mui.Box(c).Width(30)
			mui.Text(c, "游标 "+s.Cursor).FontSize(fs(13)).TextColor(tk.TextMuted)
			if s.LastPoll.Detail != "" {
				mui.Text(c, fmt.Sprintf("上次轮询 %s · %s", humanfmt.Ago(a.age(s.LastPoll.At)), s.LastPoll.Detail)).
					FontSize(11).TextColor(tk.TextFaint)
			}
		})

		if s.LastPoll.Problem != "" {
			fg, bg := tk.Amber, tk.AmberSoft
			if s.LastPoll.Outcome == view.PollFailed {
				fg, bg = tk.Red, tk.RedSoft
			}
			mui.Row(c).Padding(8, 10).Radius(7).Background(bg).Gap(8).AlignItems(mui.Start).
				Children(func() {
					mui.Icon(c, icBell).FontSize(fs(13)).TextColor(fg)
					mui.Text(c, s.LastPoll.Problem).FontSize(fs(13)).Grow(1).TextColor(fg)
				})
		}
	})
}

// pollChip says how the last poll ended in a person's words: a partial poll is
// not healthy, and must not look green (005 "诚实状态").
func (a *App) pollChip(c *mui.Context, p view.PollResult) {
	tk := a.tk
	switch p.Outcome {
	case view.PollOK:
		chip(c, tk, "轮询成功", tk.Green, tk.GreenSoft)
	case view.PollPartial:
		chip(c, tk, "部分接收", tk.Amber, tk.AmberSoft)
	case view.PollFailed:
		chip(c, tk, "轮询失败", tk.Red, tk.RedSoft)
	default:
		chip(c, tk, "未轮询", tk.TextMuted, tk.SurfaceAlt)
	}
}

// sourceFilters explains what the sources dropped: a filter that cannot be
// counted cannot be audited (design §4.8).
func (a *App) sourceFilters(c *mui.Context, vm *view.SourceVM) {
	tk := a.tk
	card(c, tk, 14, func() {
		mui.Row(c).Gap(8).AlignItems(mui.Center).Children(func() {
			mui.Text(c, "过滤与丢弃 · 24 小时").FontSize(fs(15)).Bold().TextColor(tk.Text)
			mui.Spacer(c)
			chip(c, tk, fmt.Sprintf("%d 条", vm.FilteredTotal), tk.TextMuted, tk.SurfaceAlt)
		})
		for _, f := range vm.Filtered {
			mui.Row(c).Padding(8, 9).Radius(7).Background(tk.SurfaceAlt).Gap(8).
				AlignItems(mui.Center).Children(func() {
				dot(c, tk.TextFaint, 6)
				mui.Text(c, f.Reason).FontSize(fs(13)).Grow(1).TextColor(tk.Text)
				mui.Text(c, fmt.Sprint(f.Count)).FontSize(fs(13)).TextColor(tk.TextFaint)
			})
		}
		mui.Text(c, "被丢弃的 Trigger 仍保留在库里，可审计").FontSize(fs(12)).TextColor(tk.TextFaint)
	})
}

// sourceAccountFacts shows the facts that belong to the whole account rather
// than one Source. They are facts, not settings: the page shows them as they are.
func (a *App) sourceAccountFacts(c *mui.Context, vm *view.SourceVM) {
	tk := a.tk
	card(c, tk, 14, func() {
		mui.Row(c).Gap(8).AlignItems(mui.Center).Children(func() {
			mui.Text(c, "账号事实").FontSize(fs(15)).Bold().TextColor(tk.Text)
			mui.Spacer(c)
			mui.Text(c, "事实，不是配置").FontSize(fs(12)).TextColor(tk.TextFaint)
		})
		for _, f := range vm.AccountFacts {
			kv(c, tk, f.Label, f.Value)
		}
	})
}

// settingsHint is the one line from a read-only page to the page that owns its
// configuration. Navigation, not an inline form: configuration has one home.
func (a *App) settingsHint(c *mui.Context, group view.SettingsGroupID, label string) {
	tk := a.tk
	row := mui.Row(c).Height(34).Padding(0, 12).Radius(8).Background(tk.Surface).
		Border(1, tk.Border).Gap(8).AlignItems(mui.Center).Children(func() {
		mui.Icon(c, icSettings).FontSize(fs(13)).TextColor(tk.TextMuted)
		mui.Text(c, label).FontSize(fs(14)).TextColor(tk.Accent)
		mui.Spacer(c)
		mui.Text(c, "配置在设置页").FontSize(fs(12)).TextColor(tk.TextFaint)
		mui.Icon(c, icChevR).FontSize(fs(12)).TextColor(tk.TextFaint)
	})
	if row.Clicked() {
		a.settingsGroup = group
		a.sec = view.SecSettings
		a.locate = nil
		a.goalFocus = ""
	}
}

// age is how long ago t was, against the frame's clock. The view never reads the
// clock itself, so a frame is consistent and a test is reproducible.
func (a *App) age(t time.Time) time.Duration {
	if a.now.IsZero() || t.IsZero() {
		return 0
	}
	if d := a.now.Sub(t); d > 0 {
		return d
	}
	return 0
}
