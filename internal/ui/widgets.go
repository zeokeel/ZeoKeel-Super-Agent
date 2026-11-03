package ui

import (
	"fmt"

	mui "github.com/egoist/mygo/ui"
)

// ---- small building blocks ----

func card(c *mui.Context, tk Tokens, pad float32, fn func()) {
	mui.Column(c).Background(tk.Surface).Radius(tk.Radius).
		Border(1, tk.Border).Padding(pad).Gap(12).
		Shadow(0, 1, 2, 0, mui.RGBA(16, 24, 40, 0.04)).
		Children(fn)
}

func cardG(c *mui.Context, tk Tokens, pad float32, fn func()) {
	mui.Column(c).Grow(1).Background(tk.Surface).Radius(tk.Radius).
		Border(1, tk.Border).Padding(pad).Gap(12).
		Shadow(0, 1, 2, 0, mui.RGBA(16, 24, 40, 0.04)).
		Children(fn)
}

func dot(c *mui.Context, color mui.Color, size float32) {
	mui.Box(c).Size(size, size).Radius(size / 2).Background(color)
}

func chip(c *mui.Context, tk Tokens, label string, fg, bg mui.Color) {
	mui.Row(c).Height(20).Padding(0, 8).Radius(6).Background(bg).AlignItems(mui.Center).
		Children(func() { mui.Text(c, label).FontSize(fs(13)).SingleLine().TextColor(fg) })
}

func textChip(c *mui.Context, tk Tokens, label string) {
	mui.Row(c).Height(22).Padding(0, 8).Radius(6).Background(tk.SurfaceAlt).
		Border(1, tk.BorderSoft).AlignItems(mui.Center).
		Children(func() { mui.Text(c, label).FontSize(fs(13)).TextColor(tk.TextMuted) })
}

func heading(c *mui.Context, tk Tokens, title, sub string) {
	mui.Column(c).Gap(4).Children(func() {
		mui.Text(c, title).FontSize(fs(26)).Bold().TextColor(tk.Text)
		mui.Text(c, sub).FontSize(fs(15)).TextColor(tk.TextMuted)
	})
}

// sourceGlyph is the small square that says which Source a row came from. A
// GitHub issue and a DingTalk message must not look alike (design §6.3).
func sourceGlyph(c *mui.Context, tk Tokens, kind string, size float32) {
	icon, fg := icTrigger, tk.TextMuted
	switch kind {
	case "github":
		icon = icBranch
	case "feishu":
		icon, fg = icSpark, tk.Accent
	case "dingtalk":
		icon, fg = icBell, tk.Violet
	case "local":
		icon = icTerminal
	case "schedule":
		icon = icClock
	}
	// 16px glyphs at one stroke width read at 1x; a hairline 13px glyph inside a
	// bordered tile looked like a missing-glyph box (visual audit V1).
	mui.Row(c).Size(size, size).Radius(tk.RadiusSmall).Background(tk.SurfaceAlt).
		Center().Children(func() {
		mui.Icon(c, icon).FontSize(fs(16)).TextColor(fg)
	})
}

// filterChip is one clickable filter of a page. It returns true when clicked;
// the caller records the choice in app state, never in the element.
func filterChip(c *mui.Context, tk Tokens, label string, on bool, count int) bool {
	bg, fg := tk.SurfaceAlt, tk.TextMuted
	border := tk.BorderSoft
	if on {
		bg, fg, border = tk.AccentSoft, tk.Accent, tk.Accent
	}
	text := label
	if count >= 0 {
		text = fmt.Sprintf("%s %d", label, count)
	}
	row := mui.Row(c).Height(28).Padding(0, 10).Radius(6).
		Focusable().Role(mui.RoleButton).Label(label)
	if !on && row.Hovered() {
		bg = tk.SurfaceHover
	}
	row.Background(bg).Border(1, border).AlignItems(mui.Center).Children(func() {
		mui.Text(c, text).FontSize(fs(13)).SingleLine().TextColor(fg)
	})
	return row.Clicked()
}

// chipOn is a chip that stays readable on a list's accent selection: the chosen
// row turns white text on the accent color, so a tone chip would disappear.
func chipOn(c *mui.Context, tk Tokens, label string, on bool, fg, bg mui.Color) {
	if on {
		fg, bg = tk.Accent, tk.Surface
	}
	chip(c, tk, label, fg, bg)
}
