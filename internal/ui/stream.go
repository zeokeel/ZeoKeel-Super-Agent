package ui

import (
	"fmt"
	"strings"

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
		mui.Text(c, "把事交给它，然后看着它做").FontSize(20).Bold().TextColor(tk.Text)
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
			for _, s := range threadSteps(th) {
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
				mui.Text(c, l.Source).FontSize(13).TextColor(tk.TextMuted)
				if l.Who != "" {
					mui.Text(c, "· "+l.Who).FontSize(13).TextColor(tk.TextFaint)
				}
				mui.Spacer(c)
				mui.Text(c, l.At.Format("15:04")).FontSize(12).TextColor(tk.TextFaint)
			})
			mui.Row(c).Gap(8).AlignItems(mui.Center).Children(func() {
				mui.Text(c, l.Text).FontSize(15).Grow(1).TextColor(tk.Text)
				if located {
					chip(c, tk, "定位", tk.Accent, tk.Surface)
				}
			})
		})
	})
}

// threadSteps returns the steps a Thread draws. A bare "未受理" decision is an
// outcome only when the agent said nothing: once it answered in the same Thread
// (the runtime adds a LineReply/LineNotice that starts with 超级Agent), the answer
// is the outcome, and showing "未受理" beside it reads as "ignored, then
// answered" — two truths for one message.
func threadSteps(th view.Thread) []view.Line {
	if !hasAgentAnswer(th.Steps) {
		return th.Steps
	}
	out := make([]view.Line, 0, len(th.Steps))
	for _, l := range th.Steps {
		if l.Kind == view.LineDecision && strings.TrimSpace(l.Text) == "未受理" {
			continue
		}
		out = append(out, l)
	}
	return out
}

// hasAgentAnswer reports whether the agent answered the Thread's message inside
// the panel: a reply line, or a notice that says 超级Agent in its text.
func hasAgentAnswer(steps []view.Line) bool {
	for _, l := range steps {
		if isAgentAnswer(l) {
			return true
		}
	}
	return false
}

// isAgentAnswer tells the agent's own answer apart from a delivery reply and a
// system notice: the runtime writes its answer as "超级Agent：…", and only that
// line is the answer to what the person said.
func isAgentAnswer(l view.Line) bool {
	if l.Kind != view.LineReply && l.Kind != view.LineNotice {
		return false
	}
	t := strings.TrimSpace(l.Text)
	return strings.HasPrefix(t, "超级Agent") || strings.Contains(t, "超级Agent：")
}

// agentAnswer draws the 超级Agent's answer to the operator: a panel of its own, so
// it is not read as one more status line (the "只显示未受理" fix).
func (a *App) agentAnswer(c *mui.Context, l view.Line) {
	tk := a.tk
	fg, bg := tk.Accent, tk.AccentSoft
	switch l.Tone {
	case view.ToneWarn:
		fg, bg = tk.Amber, tk.AmberSoft
	case view.ToneDanger:
		fg, bg = tk.Red, tk.RedSoft
	}
	text := strings.TrimSpace(l.Text)
	text = strings.TrimPrefix(text, "超级Agent：")
	text = strings.TrimPrefix(text, "超级Agent:")
	mui.Row(c).Padding(10, 12).Radius(8).Background(bg).Gap(9).AlignItems(mui.Start).Children(func() {
		mui.Icon(c, icSpark).FontSize(13).TextColor(fg)
		mui.Column(c).Grow(1).Gap(3).Children(func() {
			mui.Row(c).Gap(7).AlignItems(mui.Center).Children(func() {
				mui.Text(c, "超级Agent").FontSize(12).Bold().TextColor(fg)
				if !l.At.IsZero() {
					mui.Text(c, l.At.Format("15:04")).FontSize(10).TextColor(tk.TextFaint)
				}
				for _, b := range l.Badges {
					textChip(c, tk, b)
				}
			})
			mui.Text(c, text).FontSize(14).TextColor(tk.Text)
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
			mui.Text(c, "问").FontSize(12).TextColor(tk.TextFaint)
			mui.Text(c, "问你："+l.Text).FontSize(14).SingleLine().TextColor(tk.Text)
			mui.Spacer(c)
			mui.Text(c, "待你回答").FontSize(12).TextColor(tk.TextFaint)
		})
		return

	case view.LineDecision:
		mui.Row(c).Gap(8).AlignItems(mui.Center).Children(func() {
			mui.Text(c, "→").FontSize(13).TextColor(tk.TextFaint)
			mui.Text(c, l.Text).FontSize(14).TextColor(tk.TextMuted)
			for _, b := range l.Badges {
				textChip(c, tk, b)
			}
		})
		return

	case view.LineReply:
		// The agent's answer to the operator is the outcome of the message above;
		// a reply addressed to someone else stays one quiet line.
		if isAgentAnswer(l) {
			a.agentAnswer(c, l)
			return
		}
		mui.Row(c).Gap(8).AlignItems(mui.Center).Children(func() {
			mui.Text(c, "✉").FontSize(13).TextColor(toneFg(tk, l.Tone))
			mui.Text(c, l.Text).FontSize(13).TextColor(tk.TextMuted)
			for _, b := range l.Badges {
				textChip(c, tk, b)
			}
		})
		return

	case view.LineProgress:
		mui.Row(c).Gap(8).AlignItems(mui.Start).Children(func() {
			mui.Text(c, "ZeoKeel").FontSize(13).Bold().TextColor(tk.TextMuted)
			mui.Text(c, l.Text).FontSize(14).Grow(1).TextColor(tk.Text)
		})
		return

	case view.LineArtifact:
		// A deliverable is not a command: draw it as the thing it produced, with
		// the kind as a chip (design §6.10).
		mui.Row(c).Gap(8).AlignItems(mui.Center).Children(func() {
			mui.Text(c, "交付").FontSize(12).TextColor(tk.TextFaint)
			mui.Text(c, l.Text).FontSize(14).TextColor(tk.Text)
			for _, b := range l.Badges {
				textChip(c, tk, b)
			}
		})
		return

	case view.LineNotice:
		// A notice carrying the agent's answer uses the answer panel; other
		// notices stay the quiet line they were.
		if isAgentAnswer(l) {
			a.agentAnswer(c, l)
			return
		}
		mui.Row(c).Gap(8).AlignItems(mui.Center).Children(func() {
			dot(c, tk.TextFaint, 4)
			mui.Text(c, l.Text).FontSize(13).TextColor(tk.TextFaint)
		})
		return
	}

	// A tool or verification row: monospace, with the result on the right.
	mui.Row(c).Padding(8, 10).Radius(6).Background(tk.SurfaceAlt).Gap(8).
		AlignItems(mui.Center).Children(func() {
		mui.Text(c, "▸ "+l.Tool).FontSize(13).Bold().TextColor(tk.TextMuted)
		mui.Text(c, l.Command).FontSize(13).SingleLine().Grow(1).TextColor(tk.Text)
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
		mui.Text(c, label).FontSize(13).TextColor(mui.Hex("#FFFFFF"))
	})
}
