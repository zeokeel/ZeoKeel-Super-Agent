package main

import (
	_ "embed"

	"github.com/egoist/mygo/ui"
)

//go:embed assets/logo.png
var logoPNG []byte

var logo = func() *ui.Bitmap {
	b, err := ui.DecodeBitmap(logoPNG)
	if err != nil {
		panic(err)
	}
	return b
}()

// ---- small building blocks ----

func card(c *ui.Context, tk Tokens, pad float32, fn func()) {
	ui.Column(c).Background(tk.Surface).Radius(tk.Radius).
		Border(1, tk.Border).Padding(pad).Gap(12).
		Shadow(0, 1, 2, 0, ui.RGBA(16, 24, 40, 0.04)).
		Children(fn)
}

func cardG(c *ui.Context, tk Tokens, pad float32, fn func()) {
	ui.Column(c).Grow(1).Background(tk.Surface).Radius(tk.Radius).
		Border(1, tk.Border).Padding(pad).Gap(12).
		Shadow(0, 1, 2, 0, ui.RGBA(16, 24, 40, 0.04)).
		Children(fn)
}

func cardH(c *ui.Context, tk Tokens, pad, h float32, fn func()) {
	ui.Column(c).Height(h).Background(tk.Surface).Radius(tk.Radius).
		Border(1, tk.Border).Padding(pad).Gap(12).
		Shadow(0, 1, 2, 0, ui.RGBA(16, 24, 40, 0.04)).
		Children(fn)
}

func dot(c *ui.Context, color ui.Color, size float32) {
	ui.Box(c).Size(size, size).Radius(size / 2).Background(color)
}

func chip(c *ui.Context, tk Tokens, label string, fg, bg ui.Color) {
	ui.Row(c).Height(20).Padding(0, 8).Radius(6).Background(bg).AlignItems(ui.Center).
		Children(func() { ui.Text(c, label).FontSize(11).TextColor(fg) })
}

func textChip(c *ui.Context, tk Tokens, label string) {
	ui.Row(c).Height(22).Padding(0, 8).Radius(6).Background(tk.SurfaceAlt).
		Border(1, tk.BorderSoft).AlignItems(ui.Center).
		Children(func() { ui.Text(c, label).FontSize(11).TextColor(tk.TextMuted) })
}

func iconLabel(c *ui.Context, tk Tokens, icon *ui.SVG, label string, fg ui.Color, size float32) {
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Icon(c, icon).FontSize(size).TextColor(fg)
		ui.Text(c, label).FontSize(13).TextColor(fg)
	})
}

func heading(c *ui.Context, tk Tokens, title, sub string) {
	ui.Column(c).Gap(4).Children(func() {
		ui.Text(c, title).FontSize(24).Bold().TextColor(tk.Text)
		ui.Text(c, sub).FontSize(13).TextColor(tk.TextMuted)
	})
}

func kv(c *ui.Context, tk Tokens, k, v string) {
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, k).FontSize(12).TextColor(tk.TextMuted)
		ui.Spacer(c)
		ui.Text(c, v).FontSize(12).TextColor(tk.Text)
	})
}

func smallButton(c *ui.Context, tk Tokens, label string) {
	ui.Row(c).Height(30).Padding(0, 11).Radius(7).Background(tk.Surface).
		Border(1, tk.Border).AlignItems(ui.Center).Gap(6).Children(func() {
		ui.Text(c, label).FontSize(12).TextColor(tk.Text)
	})
}

func primaryButton(c *ui.Context, tk Tokens, label string) {
	ui.Row(c).Height(30).Padding(0, 13).Radius(7).Background(tk.Accent).
		AlignItems(ui.Center).Gap(6).Children(func() {
		ui.Text(c, label).FontSize(12).TextColor(ui.Hex("#FFFFFF"))
	})
}

// ---- the application chrome, shared by every screen ----

type navItem struct {
	id    string
	icon  *ui.SVG
	label string
	badge string
}

var navItems = []navItem{
	{"overview", icOverview, "总览", "3"},
	{"source", icSource, "Source", ""},
	{"trigger", icTrigger, "Trigger", "12"},
	{"eval", icEval, "Eval", "9"},
	{"execution", icRun, "Execution", "2"},
	{"settings", icSettings, "设置", ""},
}

func window(c *ui.Context, tk Tokens, active string, body func()) {
	windowWith(c, tk, active, body, nil)
}

// windowWith builds the chrome and, after it, an optional overlay that fills
// the window: dialogs are modal screens in this prototype, as mygo's Modal
// is for the real app.
func windowWith(c *ui.Context, tk Tokens, active string, body func(), overlay func(*ui.Context)) {
	ui.Column(c).Fill().Background(tk.Bg).Children(func() {
		topbar(c, tk)
		ui.Row(c).Grow(1).AlignItems(ui.Stretch).Children(func() {
			sidebar(c, tk, active)
			ui.Box(c).Grow(1).Children(body)
		})
		statusbar(c, tk)
		if overlay != nil {
			overlay(c)
		}
	})
}

// dimOverlay covers the window with a scrim and centers the dialog fn builds.
func dimOverlay(c *ui.Context, fn func()) {
	ui.Box(c).Absolute().Top(0).Left(0).Right(0).Bottom(0).
		Background(ui.RGBA(15, 23, 42, 0.38)).Center().Children(fn)
}

func topbar(c *ui.Context, tk Tokens) {
	ui.Row(c).Height(56).Padding(0, 18).Gap(14).AlignItems(ui.Center).
		Background(tk.Surface).BorderWidth(0, 0, 1, 0).BorderColor(tk.Border).
		Children(func() {
			ui.Image(c, logo).Size(26, 26).Radius(7)
			ui.Text(c, "ZeoKeel").FontSize(15).Bold().TextColor(tk.Text)
			ui.Row(c).Height(26).Padding(0, 9).Gap(6).Radius(7).
				Background(tk.SurfaceAlt).Border(1, tk.BorderSoft).AlignItems(ui.Center).
				Children(func() {
					ui.Text(c, "acme/webapp").FontSize(12).TextColor(tk.TextMuted)
					ui.Icon(c, icChevD).FontSize(12).TextColor(tk.TextFaint)
				})
			ui.Spacer(c)
			// Search field (styled; real widget is ui.SearchField).
			ui.Row(c).Width(380).Height(32).Padding(0, 11).Gap(8).Radius(8).
				Background(tk.SurfaceAlt).Border(1, tk.BorderSoft).AlignItems(ui.Center).
				Children(func() {
					ui.Icon(c, icSearch).FontSize(14).TextColor(tk.TextFaint)
					ui.Text(c, "搜索 Ticket、会话或命令").FontSize(12).TextColor(tk.TextFaint)
					ui.Spacer(c)
					ui.Row(c).Height(20).Padding(0, 6).Radius(5).Background(tk.Surface).
						Border(1, tk.BorderSoft).AlignItems(ui.Center).
						Children(func() { ui.Text(c, "⌘K").FontSize(10).TextColor(tk.TextFaint) })
				})
			ui.Spacer(c)
			ui.Row(c).Height(30).Padding(0, 11).Gap(6).Radius(7).
				Background(tk.SurfaceAlt).Border(1, tk.BorderSoft).AlignItems(ui.Center).
				Children(func() {
					ui.Icon(c, icPause).FontSize(13).TextColor(tk.TextMuted)
					ui.Text(c, "暂停运行时").FontSize(12).TextColor(tk.Text)
				})
			ui.Icon(c, icGear).FontSize(17).TextColor(tk.TextMuted)
			ui.Row(c).Height(28).Padding(0, 10).Gap(6).Radius(14).Background(tk.AmberSoft).
				AlignItems(ui.Center).Children(func() {
				ui.Icon(c, icBell).FontSize(13).TextColor(tk.Amber)
				ui.Text(c, "3 待处理").FontSize(12).TextColor(tk.Amber)
			})
			ui.Box(c).Size(28, 28).Radius(14).Background(tk.Accent).Center().
				Children(func() { ui.Text(c, "陈").FontSize(12).TextColor(ui.Hex("#FFFFFF")) })
		})
}

func sidebar(c *ui.Context, tk Tokens, active string) {
	ui.Column(c).Width(232).FillHeight().Background(tk.SurfaceAlt).
		BorderWidth(0, 1, 0, 0).BorderColor(tk.Border).Padding(10, 12).Children(func() {
		ui.Text(c, "工作台").FontSize(11).TextColor(tk.TextFaint).Padding(4, 10, 8, 10)
		for _, it := range navItems {
			item := it
			selected := item.id == active
			ui.Row(c).Height(34).Padding(0, 10).Gap(10).Radius(8).AlignItems(ui.Center).
				Background(condColor(selected, tk.AccentSoft, ui.Transparent)).
				Children(func() {
					fg := tk.TextMuted
					if selected {
						fg = tk.Accent
					}
					ui.Icon(c, item.icon).FontSize(16).TextColor(fg)
					ui.Text(c, item.label).FontSize(13).TextColor(condColor(selected, tk.Accent, tk.Text))
					ui.Spacer(c)
					if item.badge != "" {
						chip(c, tk, item.badge, condColor(selected, tk.Accent, tk.TextMuted),
							condColor(selected, tk.Surface, tk.Surface))
					}
				})
		}
		ui.Spacer(c)
		// Runtime card: the app lives in the tray; this is its pulse.
		ui.Column(c).Padding(12).Gap(7).Radius(10).Background(tk.Surface).
			Border(1, tk.BorderSoft).Children(func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				dot(c, tk.Green, 7)
				ui.Text(c, "运行时正常").FontSize(12).Bold().TextColor(tk.Text)
				ui.Spacer(c)
				ui.Text(c, "1/2").FontSize(11).TextColor(tk.TextMuted)
			})
			ui.Text(c, "executor 忙 1 · 队列 0").FontSize(11).TextColor(tk.TextMuted)
			ui.Row(c).Height(4).Radius(2).Background(tk.BorderSoft).Children(func() {
				ui.Box(c).WidthPercent(50).Height(4).Radius(2).Background(tk.Accent)
			})
			ui.Row(c).Gap(5).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, icClock).FontSize(11).TextColor(tk.TextFaint)
				ui.Text(c, "下次轮询 00:48").FontSize(11).TextColor(tk.TextFaint)
			})
		})
		ui.Text(c, "v0.1.0 · Go 1.27.3 · CGO=0").FontSize(10).TextColor(tk.TextFaint).
			Padding(10, 10, 0, 0)
	})
}

func condColor(cond bool, a, b ui.Color) ui.Color {
	if cond {
		return a
	}
	return b
}

func statusbar(c *ui.Context, tk Tokens) {
	ui.Row(c).Height(30).Padding(0, 18).Gap(16).AlignItems(ui.Center).
		Background(tk.SurfaceAlt).BorderWidth(1, 0, 0, 0).BorderColor(tk.Border).
		Children(func() {
			dot(c, tk.Green, 6)
			ui.Text(c, "运行中").FontSize(11).TextColor(tk.TextMuted)
			ui.Text(c, "·").FontSize(11).TextColor(tk.TextFaint)
			ui.Text(c, "1 executor").FontSize(11).TextColor(tk.TextMuted)
			ui.Text(c, "·").FontSize(11).TextColor(tk.TextFaint)
			ui.Text(c, "队列 0").FontSize(11).TextColor(tk.TextMuted)
			ui.Spacer(c)
			ui.Text(c, "⌘K 搜索 · ⌘. 暂停 · ⌘, 设置").FontSize(11).TextColor(tk.TextFaint)
			ui.Text(c, "SQLite 12.4 MB").FontSize(11).TextColor(tk.TextFaint)
		})
}

// content wraps a screen body in the standard page padding.
func content(c *ui.Context, body func()) {
	ui.Box(c).Fill().Padding(22, 26).Children(func() {
		ui.Column(c).Fill().Gap(18).Children(body)
	})
}
