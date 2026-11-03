package ui

import (
	"fmt"
	"strings"

	mui "github.com/egoist/mygo/ui"

	"github.com/zeokeel/super-agent/internal/view"
)

// The glossary: the terms the panel and the prompts use, in Chinese and English
// (001:73). It opens from ⌘/ and from the status bar's (?) entry; the panel shows
// one language at a time, so the opposite language is carried beside each term
// as the prototype asks.
//
// The list lives in internal/view, not here: it is domain vocabulary, and the
// panel only draws it. Adding a term is a data change in one place.

// openHelp shows the glossary.
func (a *App) openHelp() { a.helpOpen = true }

// helpDialog draws the glossary while it is open.
func (a *App) helpDialog(c *mui.Context) {
	if !a.helpOpen {
		return
	}
	tk := a.tk
	mui.Modal(c, &a.helpOpen, func() {
		mui.Column(c).Width(760).Gap(10).Children(func() {
			mui.Row(c).Gap(8).AlignItems(mui.Center).Children(func() {
				mui.Text(c, "名词表").FontSize(15).Bold().TextColor(tk.Text)
				textChip(c, tk, "Glossary")
				chip(c, tk, fmt.Sprint(view.GlossaryLen()), tk.TextMuted, tk.SurfaceAlt)
				mui.Spacer(c)
				mui.Text(c, "Esc 关闭 · "+helpKeyHint()+" 打开").FontSize(10).TextColor(tk.TextFaint)
			})
			mui.Text(c, "面板与提示词用的是这些术语；发给同事的消息用日常语言，不用它们。").
				FontSize(11).TextColor(tk.TextMuted)
			mui.Scroll(c).MaxHeight(540).Children(func() {
				mui.Column(c).Gap(8).Children(func() {
					for _, e := range view.Glossary() {
						mui.Row(c).Gap(10).AlignItems(mui.Start).Children(func() {
							mui.Column(c).Width(150).Shrink(0).Gap(1).Children(func() {
								mui.Text(c, e.Term).FontSize(12).Bold().TextColor(tk.Text)
								mui.Text(c, e.Chinese).FontSize(10).TextColor(tk.Accent)
							})
							mui.Text(c, e.Meaning).FontSize(11).Grow(1).TextColor(tk.TextMuted)
						})
					}
					// The closed value sets: a person meeting "blocked" in a
					// report needs the states beside it.
					mui.Box(c).Height(1).Background(tk.BorderSoft)
					mui.Text(c, "取值").FontSize(12).Bold().TextColor(tk.Text)
					for _, g := range view.GlossaryValues() {
						mui.Row(c).Gap(10).AlignItems(mui.Start).Children(func() {
							mui.Text(c, g.Title).FontSize(11).Width(150).Shrink(0).TextColor(tk.Text)
							mui.Text(c, strings.Join(g.Values, " · ")).FontSize(10).Grow(1).
								TextColor(tk.TextMuted)
						})
					}
				})
			})
		})
	})
}
