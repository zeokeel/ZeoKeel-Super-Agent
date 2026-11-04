package ui

import (
	"fmt"

	mui "github.com/egoist/mygo/ui"
)

// ---- small building blocks ----

func card(c *mui.Context, tk Tokens, pad float32, fn func()) {
	// DSH section: no outer box. Spacing and hairline dividers come from the
	// content itself; only genuine form cards use .dt-card.
	mui.Column(c).Gap(12).Children(fn)
}

func cardG(c *mui.Context, tk Tokens, pad float32, fn func()) {
	mui.Column(c).Grow(1).Gap(12).Children(fn)
}

func dot(c *mui.Context, color mui.Color, size float32) {
	mui.Box(c).Size(size, size).Radius(size / 2).Background(color)
}

func textChip(c *mui.Context, tk Tokens, label string) {
	mui.Text(c, label).FontSize(fs(12)).TextColor(tk.TextMuted)
}

func heading(c *mui.Context, tk Tokens, title, sub string) {
	mui.Column(c).Gap(4).Children(func() {
		mui.Text(c, title).FontSize(fs(15)).Bold().TextColor(tk.TextMuted)
		mui.Text(c, sub).FontSize(fs(13)).TextColor(tk.TextFaint)
	})
}

func sourceGlyph(c *mui.Context, tk Tokens, kind string, size float32) {
	icon, fg := icTrigger, tk.TextMuted
	switch kind {
	case "github":
		icon = icBranch
	case "feishu":
		icon, fg = icSpark, tk.Text
	case "dingtalk":
		icon, fg = icBell, tk.Text
	case "local":
		icon = icTerminal
	case "schedule":
		icon = icClock
	}
	mui.Icon(c, icon).FontSize(fs(16)).TextColor(fg)
}

func filterChip(c *mui.Context, tk Tokens, label string, on bool, count int) bool {
	bg := tk.Surface
	fg := tk.TextMuted
	if on {
		bg, fg = tk.SurfaceAlt, tk.Text
	}
	text := label
	if count >= 0 {
		text = fmt.Sprintf("%s %d", label, count)
	}
	btn := mui.Button(c, text).Label(label).Height(28).FontSize(13).
		BorderWidth(0).Background(bg).TextColor(fg).Radius(14).PaddingX(10)
	return btn.Clicked()
}

func chip(c *mui.Context, tk Tokens, label string, fg, bg mui.Color) {
	// DSH rows use meta text, not pills: no background, no radius.
	mui.Text(c, label).FontSize(fs(12)).SingleLine().TextColor(fg)
}

func chipOn(c *mui.Context, tk Tokens, label string, on bool, fg, bg mui.Color) {
	chip(c, tk, label, fg, bg)
}
