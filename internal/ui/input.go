package ui

import (
	"strings"

	mui "github.com/egoist/mygo/ui"
)

// inputBar is the whole input: one line, no workspace picker, no "new task"
// action. Where the work goes is the agent's decision (design §6.9).
func (a *App) inputBar(c *mui.Context) {
	tk := a.tk

	mui.Row(c).Height(40).Radius(10).Background(tk.Surface).Border(1, tk.Border).
		Gap(8).Padding(0, 12).AlignItems(mui.Center).Children(func() {
		mui.Icon(c, icSpark).FontSize(15).TextColor(tk.Accent)
		field := mui.TextInput(c.Key("input"), &a.draft).
			Placeholder("说点什么，或派一件事…")
		field.Grow(1).FontSize(13)
		if field.Submitted() {
			a.submit()
		}
		if mui.PrimaryButton(c, "发送").Label("发送").Height(26).Clicked() {
			a.submit()
		}
	})

	if a.composerErr != "" {
		mui.Row(c).Padding(4, 0).Children(func() {
			mui.Text(c, a.composerErr).FontSize(11).TextColor(tk.Red)
		})
	}
}

// submit hands the line to the agent. It runs on the main thread and Dispatch is
// memory-only, so it cannot stall a frame.
func (a *App) submit() {
	text := strings.TrimSpace(a.draft)
	if text == "" || a.cmd == nil {
		return
	}
	if err := a.cmd.Dispatch(text); err != nil {
		a.composerErr = err.Error()
		return // keep the text: losing what a person typed is worse than a red line
	}
	a.draft = ""
	a.composerErr = ""
}
