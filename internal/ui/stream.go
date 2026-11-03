package ui

import (
	"fmt"

	mui "github.com/egoist/mygo/ui"

	"github.com/zeokeel/super-agent/internal/view"
)

// maxStreamWidth keeps a line of text readable on a wide screen.
const maxStreamWidth = 860

// stream is the whole working surface: one continuous timeline, newest at the
// bottom, everything the agent saw and did. The operator reads it like a log and
// never has to switch context.
//
// It is a ui.List, not a Column: a log only grows, and mygo builds only the rows
// in view (ui/list.go). FollowEnd keeps the newest line in view as it arrives,
// the way the list is meant for "a chat or a log" (ui/list.go:54-65), and
// FollowAnimation glides to it instead of jumping. A row is one message and what
// the agent did about it, indented under it (a.thread).
func (a *App) streamPage(c *mui.Context) {
	tk := a.tk
	vm := a.snap.Stream()

	mui.Column(c).Grow(1).AlignItems(mui.Stretch).Background(tk.Bg).Children(func() {
		if vm == nil || len(vm.Threads) == 0 {
			mui.Scroll(c).Grow(1).Children(func() {
				mui.Column(c).Padding(28, 24).AlignItems(mui.Center).Children(func() {
					a.emptyStream(c)
				})
			})
			return
		}
		rows := vm.Threads
		a.streamList.Key = threadKeys(rows)
		a.streamList.FollowEnd = true
		a.streamList.FollowAnimation = &mui.Smooth
		mui.List(c, &a.streamList, len(rows), func(i int) {
			mui.Column(c).Padding(11, 24).AlignItems(mui.Center).Children(func() {
				mui.Column(c).Width(maxStreamWidth).Children(func() {
					a.thread(c, rows[i])
				})
			})
		}).Grow(1)
	})
}

// threadKeys is the list's key for the stream's rows: the Thread's ID, so the
// list keeps its place and its row state when older messages are prepended. A
// projection without IDs (the runtime has not published them yet) gets nil
// instead of a key that repeats: two rows sharing one key would share one state.
func threadKeys(rows []view.Thread) func(int) any {
	for _, th := range rows {
		if th.ID == "" {
			return nil
		}
	}
	return func(i int) any { return rows[i].ID }
}

// emptyStream is the first-run state: one line and the input, no illustration.
func (a *App) emptyStream(c *mui.Context) {
	tk := a.tk
	mui.Column(c).Padding(0, 120).Gap(8).AlignItems(mui.Center).Children(func() {
		mui.Icon(c, icSpark).FontSize(26).TextColor(tk.Accent)
		mui.Text(c, "把事交给它，然后看着它做").FontSize(18).Bold().TextColor(tk.Text)
		mui.Text(c, "所有来源的消息都会落到这条流里：GitHub、钉钉、飞书，以及你在这里打的字。").
			FontSize(12).TextColor(tk.TextMuted)
	})
}

// thread is one message and what the agent did about it. The steps are indented
// and drawn with a left rule, so a busy stream still scans.
func (a *App) thread(c *mui.Context, th view.Thread) {
	tk := a.tk
	a.headLine(c, th.Head)
	if len(th.Steps) == 0 {
		return
	}
	mui.Row(c).Gap(0).AlignItems(mui.Start).Children(func() {
		mui.Box(c).Width(22).AlignItems(mui.Center).Children(func() {
			mui.Box(c).Width(1).Grow(1).Background(tk.BorderSoft)
		})
		mui.Column(c).Grow(1).Gap(8).Padding(0, 0, 0, 0).Children(func() {
			for _, s := range th.Steps {
				a.stepLine(c, s)
			}
		})
	})
}

// headLine is the message itself: who, from where, what.
func (a *App) headLine(c *mui.Context, l view.Line) {
	tk := a.tk
	mine := l.Kind == view.LineOperator
	located := a.located(view.SearchMessages, l.Text)
	bg, border := tk.Surface, tk.Border
	if mine || located {
		bg = tk.AccentSoft
	}
	if located {
		border = tk.Accent
	}
	mui.Row(c).Padding(12, 14).Radius(10).Background(bg).Border(1, border).
		Gap(10).AlignItems(mui.Start).Children(func() {
		a.avatar(c, avatarLetter(l), ifColor(mine, tk.Accent, tk.TextMuted))
		mui.Column(c).Grow(1).Gap(3).Children(func() {
			mui.Row(c).Gap(6).AlignItems(mui.Center).Children(func() {
				mui.Text(c, l.Source).FontSize(11).TextColor(tk.TextMuted)
				if l.Who != "" {
					mui.Text(c, "· "+l.Who).FontSize(11).TextColor(tk.TextFaint)
				}
				mui.Spacer(c)
				mui.Text(c, l.At.Format("15:04")).FontSize(10).TextColor(tk.TextFaint)
			})
			mui.Row(c).Gap(8).AlignItems(mui.Center).Children(func() {
				mui.Text(c, l.Text).FontSize(13).Grow(1).TextColor(tk.Text)
				if located {
					chip(c, tk, "定位", tk.Accent, tk.Surface)
				}
			})
		})
	})
}

// stepLine is one thing the agent did: a decision, a tool call, a question, a
// deliverable.
func (a *App) stepLine(c *mui.Context, l view.Line) {
	tk := a.tk
	switch l.Kind {
	case view.LineQuestion:
		// A record that the agent asked something, not a place to answer it: the
		// answer belongs to 总览, where the to-dos are (design §6.3).
		mui.Row(c).Gap(8).AlignItems(mui.Center).Children(func() {
			mui.Text(c, "问").FontSize(10).TextColor(tk.TextFaint)
			mui.Text(c, "问你："+l.Text).FontSize(12).SingleLine().TextColor(tk.Text)
			mui.Spacer(c)
			mui.Text(c, "待你回答").FontSize(10).TextColor(tk.TextFaint)
		})
		return

	case view.LineDecision:
		mui.Row(c).Gap(8).AlignItems(mui.Center).Children(func() {
			mui.Text(c, "→").FontSize(11).TextColor(tk.TextFaint)
			mui.Text(c, l.Text).FontSize(12).TextColor(tk.TextMuted)
			for _, b := range l.Badges {
				textChip(c, tk, b)
			}
		})
		return

	case view.LineReply:
		mui.Row(c).Gap(8).AlignItems(mui.Center).Children(func() {
			mui.Text(c, "✉").FontSize(11).TextColor(toneFg(tk, l.Tone))
			mui.Text(c, l.Text).FontSize(11).TextColor(tk.TextMuted)
			for _, b := range l.Badges {
				textChip(c, tk, b)
			}
		})
		return

	case view.LineProgress:
		mui.Row(c).Gap(8).AlignItems(mui.Start).Children(func() {
			mui.Text(c, "ZeoKeel").FontSize(11).Bold().TextColor(tk.TextMuted)
			mui.Text(c, l.Text).FontSize(12).Grow(1).TextColor(tk.Text)
		})
		return

	case view.LineArtifact:
		// A deliverable is not a command: draw it as the thing it produced, with
		// the kind as a chip (design §6.10).
		mui.Row(c).Gap(8).AlignItems(mui.Center).Children(func() {
			mui.Text(c, "交付").FontSize(10).TextColor(tk.TextFaint)
			mui.Text(c, l.Text).FontSize(12).TextColor(tk.Text)
			for _, b := range l.Badges {
				textChip(c, tk, b)
			}
		})
		return

	case view.LineNotice:
		mui.Row(c).Gap(8).AlignItems(mui.Center).Children(func() {
			dot(c, tk.TextFaint, 4)
			mui.Text(c, l.Text).FontSize(11).TextColor(tk.TextFaint)
		})
		return
	}

	// A tool or verification row: monospace, with the result on the right.
	mui.Row(c).Padding(8, 10).Radius(6).Background(tk.SurfaceAlt).Gap(8).
		AlignItems(mui.Center).Children(func() {
		mui.Text(c, "▸ "+l.Tool).FontSize(11).Bold().TextColor(tk.TextMuted)
		mui.Text(c, l.Command).FontSize(11).SingleLine().Grow(1).TextColor(tk.Text)
		if l.DiffAdd > 0 || l.DiffDel > 0 {
			chip(c, tk, fmt.Sprintf("+%d", l.DiffAdd), tk.Green, tk.GreenSoft)
			chip(c, tk, fmt.Sprintf("−%d", l.DiffDel), tk.Red, tk.RedSoft)
		} else {
			chip(c, tk, fmt.Sprintf("exit %d", l.ExitCode),
				ifColor(l.ExitCode == 0, tk.Green, tk.Red),
				ifColor(l.ExitCode == 0, tk.GreenSoft, tk.RedSoft))
		}
	})
}

// avatarLetter picks the letter in a message's avatar.
func avatarLetter(l view.Line) string {
	switch l.Kind {
	case view.LineOperator:
		return "你"
	}
	if l.Who != "" {
		return string([]rune(l.Who)[0])
	}
	return "?"
}

func (a *App) avatar(c *mui.Context, label string, bg mui.Color) {
	mui.Row(c).Size(24, 24).Radius(12).Background(bg).Center().Children(func() {
		mui.Text(c, label).FontSize(11).TextColor(mui.Hex("#FFFFFF"))
	})
}
