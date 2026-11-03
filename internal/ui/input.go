package ui

import (
	mui "github.com/egoist/mygo/ui"
)

// inputBar is the whole input: one line, no workspace picker, no "new task"
// action. It is a question box and a dispatch box at once (P0-7): 超级Agent
// answers a question, or takes the work and reports an outcome above this line.
// Sending is asynchronous (P0-2): the frame submits and returns, and the pending
// state is what a waiting person sees.
func (a *App) inputBar(c *mui.Context) {
	tk := a.tk

	mui.Row(c).Height(40).Radius(10).Background(tk.Surface).Border(1, tk.Border).
		Gap(8).Padding(0, 12).AlignItems(mui.Center).Children(func() {
		mui.Icon(c, icSpark).FontSize(fs(15)).TextColor(tk.Accent)
		field := mui.TextInput(c.Key("input"), &a.draft).
			Placeholder("问点什么，或派一件事…")
		field.Grow(1).FontSize(fs(15))
		if field.Submitted() {
			a.submit()
		}
		label := "发送"
		if a.dispatchPending {
			label = "发送中…"
		}
		btn := mui.PrimaryButton(c, label).Label(label).Height(26)
		if a.dispatchPending {
			btn.Disabled(true)
		} else if btn.Clicked() {
			a.submit()
		}
	})
}
