package ui

// Visual layer: DSH's own numbers, applied to native mygo controls. No custom
// widget classes — spacing, radius, height and colors are Element settings.

import (
	"fmt"
	"strings"

	mui "github.com/egoist/mygo/ui"

	"github.com/zeokeel/super-agent/internal/view"
)

const (
	// DSH button geometry.
	btnH      = 36
	btnRadius = 18
	btnFont   = 14
	smallH    = 28
	smallRad  = 14
	smallFont = 12

	// DSH surfaces.
	dsDark    = "#1F1F1F"
	dsOnDark  = "#FFFFFF"
	dsBrand   = "#4176E6" // DSH send/blue brand
	dsDanger  = "#B42318"
	contentW  = 960
	pageTop   = 28
	pageSide  = 40
	rowRadius = 12
)

// primaryBtn: dark fill, 36px, radius 18.
func (a *App) primaryBtn(c *mui.Context, label string) mui.Element {
	return mui.Button(c, label).Label(label).Height(btnH).FontSize(btnFont).
		Background(mui.Hex(dsDark)).TextColor(mui.Hex(dsOnDark)).Radius(btnRadius).
		PaddingX(14).BorderWidth(0)
}

// outlineBtn: 0.5px border, 36px, radius 18.
func (a *App) outlineBtn(c *mui.Context, label string) mui.Element {
	return mui.Button(c, label).Label(label).Height(btnH).FontSize(btnFont).
		Background(a.tk.Surface).TextColor(a.tk.Text).BorderWidth(0.5).
		BorderColor(a.tk.Border).Radius(btnRadius).PaddingX(14)
}

// ghostBtn: no border, no fill, 36px.
func (a *App) ghostBtn(c *mui.Context, label string) mui.Element {
	return mui.Button(c, label).Label(label).Height(btnH).FontSize(btnFont).
		Background(a.tk.Surface).TextColor(a.tk.TextMuted).BorderWidth(0).
		Radius(btnRadius).PaddingX(14)
}

// dangerBtn: destructive, 36px.
func (a *App) dangerBtn(c *mui.Context, label string) mui.Element {
	return mui.Button(c, label).Label(label).Height(btnH).FontSize(btnFont).
		Background(a.tk.Surface).TextColor(mui.Hex(dsDanger)).BorderWidth(0.5).
		BorderColor(a.tk.Border).Radius(btnRadius).PaddingX(14)
}

// smallBtn: the DSH small button, 28px / radius 14 / 12px.
func (a *App) smallBtn(c *mui.Context, label string, primary bool) mui.Element {
	bg := a.tk.Surface
	fg := a.tk.Text
	if primary {
		bg, fg = mui.Hex(dsDark), mui.Hex(dsOnDark)
	}
	return mui.Button(c, label).Label(label).Height(smallH).FontSize(smallFont).
		Background(bg).TextColor(fg).BorderWidth(0.5).BorderColor(a.tk.Border).
		Radius(smallRad).PaddingX(10)
}

// smallOutlineBtn: small outline action (channel check, wizard…).
func (a *App) smallOutlineBtn(c *mui.Context, label string) mui.Element {
	return mui.Button(c, label).Label(label).Height(smallH).FontSize(smallFont).
		Background(a.tk.Surface).TextColor(a.tk.Text).BorderWidth(0.5).
		BorderColor(a.tk.Border).Radius(smallRad).PaddingX(10)
}

// sendBtn: DSH's circular blue send action, a ButtonBase circle with a white
// arrow (the label stays "发送" for tests/AT).
func (a *App) sendBtn(c *mui.Context, label string) mui.Element {
	btn := mui.ButtonBase(c).Size(34, 34).Radius(17).Background(mui.Hex(dsBrand)).
		Label(label)
	btn.Children(func() {
		mui.Text(c, "↑").FontSize(fs(16)).Bold().TextColor(mui.Hex(dsOnDark))
	})
	return btn
}

// dshSwitch is a native SwitchBase drawn with DSH values: 36x20, radius 10,
// off = border-l3 track, on = dark #1F1F1F, white 16px knob at 2px inset.
func (a *App) dshSwitch(c *mui.Context, key, label string, on *bool) bool {
	if on == nil {
		return false
	}
	cur := *on
	sw := mui.SwitchBase(c.Key(key), on).Size(36, 20).Radius(10).
		Background(a.tk.Surface).Label(label)
	sw.Draw(func(p *mui.Painter, r mui.Rect) {
		track := a.tk.Border
		at := float32(2)
		if cur {
			track = mui.Hex(dsDark)
			at = r.W - 16 - 2
		}
		p.Fill(mui.Rect{X: r.X, Y: r.Y, W: r.W, H: r.H}, track, 10)
		p.Fill(mui.Rect{X: r.X + at, Y: r.Y + 2, W: 16, H: 16}, mui.Hex("#FFFFFF"), 8)
	})
	return sw.Changed()
}

// navRow is a DSH sidebar nav row: native ButtonBase + a left-aligned Row child
// (36px, radius 12, padding 7/8, gap 8, selected interactive-hover).
func (a *App) navRow(c *mui.Context, icon *mui.SVG, label string, selected bool, badge string, onClick func()) {
	bg := a.tk.Surface
	if selected {
		bg = a.tk.SurfaceAlt
	}
	row := mui.ButtonBase(c).Width(256).Height(36).Radius(rowRadius).Padding(7, 8).
		Background(bg).Label(label)
	row.Children(func() {
		mui.Row(c).Width(240).Gap(8).AlignItems(mui.Center).Children(func() {
			if icon != nil {
				mui.Icon(c, icon).FontSize(fs(15)).TextColor(a.tk.TextMuted)
			}
			mui.Text(c, label).FontSize(fs(14)).Grow(1).TextColor(a.tk.Text)
			if badge != "" {
				mui.Badge(c, badge)
			}
		})
	})
	if row.Clicked() {
		onClick()
	}
}

// sidebarItem is a project/session row: folder (or dot) + visible name only; the
// Label keeps the test/AT name with its "项目/会话" prefix. 36px for projects,
// 32px for sessions, no bordered button.
func (a *App) sidebarItem(c *mui.Context, icon *mui.SVG, drawDot bool, labelAttr, visible string, selected bool, height float32, trailing string, onClick func()) {
	bg := a.tk.Surface
	if selected {
		bg = a.tk.SurfaceAlt
	}
	row := mui.ButtonBase(c).Width(256).Height(height).Padding(7, 8).Radius(rowRadius).
		Background(bg).Label(labelAttr)
	row.Children(func() {
		mui.Row(c).Width(240).Gap(8).AlignItems(mui.Center).Children(func() {
			if icon != nil {
				mui.Icon(c, icon).FontSize(fs(14)).TextColor(a.tk.TextFaint)
			}
			if drawDot {
				mui.Box(c).Size(6, 6).Radius(3).Background(a.tk.TextFaint)
			}
			mui.Text(c, visible).FontSize(fs(14)).Grow(1).TextColor(a.tk.Text)
			if trailing != "" {
				mui.Text(c, trailing).FontSize(fs(12)).TextColor(a.tk.TextFaint)
			}
		})
	})
	if row.Clicked() {
		onClick()
	}
}

// composerCard: bordered 16-radius card, section padding 16, TextArea 13/20,
// action row with project menu, meta text and DSH's blue circular send.
func (a *App) composerCard(c *mui.Context, width float32) {
	tk := a.tk
	mui.Column(c).Width(width).Background(tk.Surface).BorderWidth(0.5).
		BorderColor(tk.Border).Radius(16).Padding(16).Gap(8).Children(func() {
		mui.TextArea(c.Key("input"), &a.draft).
			Placeholder("说点什么…（Enter 换行，"+sendKeyHint()+" 发送）").
			Height(76).FontSize(fs(14)).Label("输入消息").BorderWidth(0).
			Background(tk.Surface).Padding(8, 12).Grow(1)
		mui.Box(c).Height(1).Background(tk.BorderSoft)
		mui.Row(c).Gap(8).AlignItems(mui.Center).Children(func() {
			label := "选择项目"
			if ws := a.workspaceEntry(a.targetWS); ws != nil {
				label = ws.Name
			}
			mui.MenuButton(c, label, func(m *mui.Menu) {
				if vm := a.snap.Workspaces(); vm != nil {
					for _, ws := range vm.Workspaces {
						ws := ws
						if m.Item(ws.Name).Chosen() {
							a.targetWS = ws.ID
						}
					}
				}
			}).Label("项目").Height(28).FontSize(smallFont).BorderWidth(0).
				Background(tk.Surface).TextColor(tk.TextMuted)
			model := "默认模型"
			if vm := a.snap.Settings(); vm != nil {
				if g := vm.Group(view.SettingsWorker); g != nil {
					for _, f := range g.Fields {
						if f.Key == "model" && f.Value != "" {
							model = f.Value
						}
					}
				}
			}
			mui.Text(c, model).FontSize(fs(12)).TextColor(tk.TextFaint)
			mui.Text(c, "· 默认权限").FontSize(fs(12)).TextColor(tk.TextFaint)
			mui.Spacer(c)
			label = "发送"
			if a.dispatchPending {
				label = "发送中…"
			}
			btn := a.sendBtn(c, label)
			if a.dispatchPending {
				btn.Disabled(true)
			} else if btn.Clicked() {
				a.submitFromComposer()
			}
		})
	})
	if c.Shortcut(mui.Cmd, mui.KeyEnter) {
		a.submitFromComposer()
	}
}

// firstLine is the session header's label: the first non-empty line, bounded.
func firstLine(s string, max int) string {
	line := strings.TrimSpace(s)
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = strings.TrimSpace(line[:i])
	}
	if len([]rune(line)) > max {
		line = string([]rune(line)[:max]) + "…"
	}
	return line
}

// nativePlanCard is the running plan as a native bordered column with plain rows.
func (a *App) nativePlanCard(c *mui.Context, plan *view.PlanVM) {
	tk := a.tk
	done, running, blocked, _ := plan.Counts()
	mui.Column(c).Width(contentW).Background(tk.Surface).BorderWidth(0.5).
		BorderColor(tk.Border).Radius(16).Padding(16).Gap(8).Children(func() {
		mui.Row(c).Gap(8).AlignItems(mui.Center).Children(func() {
			mui.Icon(c, icCheck).FontSize(fs(14)).TextColor(tk.TextMuted)
			mui.Text(c, "计划与待办").FontSize(fs(14)).Bold().TextColor(tk.Text)
			mui.Spacer(c)
			mui.Text(c, fmt.Sprintf("%d/%d 完成 · %d 进行中 · %d 需要你",
				done, len(plan.Items), running, blocked)).FontSize(fs(12)).TextColor(tk.TextFaint)
		})
		for _, it := range plan.Items {
			it := it
			mark := "○"
			fg := tk.TextMuted
			switch it.Status {
			case view.PlanDone:
				mark, fg = "✓", tk.Green
			case view.PlanRunning:
				mark, fg = "●", tk.Accent
			case view.PlanBlocked:
				mark, fg = "!", tk.Amber
			case view.PlanFailed:
				mark, fg = "×", tk.Red
			}
			mui.Row(c).Gap(8).AlignItems(mui.Center).Padding(4, 0).Children(func() {
				mui.Text(c, mark).FontSize(fs(12)).Width(16).TextColor(fg)
				mui.Column(c).Grow(1).Gap(1).Children(func() {
					mui.Text(c, it.Title).FontSize(fs(14)).Bold().TextColor(tk.Text)
					if it.Detail != "" {
						mui.Text(c, it.Detail).FontSize(fs(12)).TextColor(tk.TextFaint)
					}
				})
			})
		}
	})
}
