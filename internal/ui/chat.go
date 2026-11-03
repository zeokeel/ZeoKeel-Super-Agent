package ui

import (
	"strings"

	mui "github.com/egoist/mygo/ui"

	"github.com/zeokeel/super-agent/internal/humanfmt"
	"github.com/zeokeel/super-agent/internal/view"
)

// 超级Agent 的对话: the compact conversation on the overview, and the one line above
// the composer on every page. Chat answers, outcomes and notes are neutral; only
// a genuine failure (ChatKind error / a red error note) is red — "没有派发工单"
// is an answer, not a failure (P0-7).

// composerContext is the one line above the composer: what the agent last said,
// or that the line is still being sent. It is the fix for "回答只出现在消息页":
// the result of the input appears where the input is, on every page.
func (a *App) composerContext(c *mui.Context) {
	tk := a.tk
	if a.dispatchPending {
		mui.Row(c).Gap(8).AlignItems(mui.Center).Padding(0, 2, 6, 2).Children(func() {
			dot(c, tk.Accent, 6)
			mui.Text(c, "发送中…").FontSize(fs(12)).TextColor(tk.Accent)
			if a.dispatchEcho != "" {
				mui.Text(c, humanfmt.Clip(a.dispatchEcho, 80)).FontSize(fs(12)).SingleLine().
					Grow(1).TextColor(tk.TextMuted)
			}
		})
		return
	}
	// The chat's newest agent line is the result: an answer, a dispatched
	// outcome, or a note. It is neutral unless it is a real failure.
	if chat := a.snap.Chat(); chat != nil {
		if m, ok := chat.LastAnswer(); ok {
			a.chatContextLine(c, m)
			return
		}
	}
	if a.composerNote != "" {
		mui.Row(c).Padding(0, 2, 6, 2).Gap(8).AlignItems(mui.Center).Children(func() {
			mui.Icon(c, icBell).FontSize(fs(12)).TextColor(tk.TextMuted)
			mui.Text(c, humanfmt.Clip(a.composerNote, 140)).FontSize(fs(12)).SingleLine().
				Grow(1).TextColor(tk.TextMuted)
		})
		return
	}
	if a.composerErr != "" {
		mui.Row(c).Padding(0, 2, 6, 2).Children(func() {
			mui.Text(c, a.composerErr).FontSize(fs(12)).TextColor(tk.Red)
		})
	}
}

// chatContextLine is one chat line as the composer's context line.
func (a *App) chatContextLine(c *mui.Context, m view.ChatMessage) {
	tk := a.tk
	fg := tk.TextMuted
	icon := icSpark
	if m.IsFailure() {
		fg = tk.Red
		icon = icBell
	}
	who := "超级Agent："
	if m.Role == view.ChatSystem {
		who = ""
	}
	mui.Row(c).Padding(0, 2, 6, 2).Gap(8).AlignItems(mui.Center).Children(func() {
		mui.Icon(c, icon).FontSize(fs(12)).TextColor(ifColor(m.IsFailure(), tk.Red, tk.Accent))
		mui.Text(c, who+humanfmt.Clip(strings.TrimSpace(m.Text), 140)).FontSize(fs(12)).SingleLine().
			Grow(1).TextColor(fg)
		if !m.At.IsZero() {
			mui.Text(c, humanfmt.Ago(a.age(m.At))).FontSize(fs(10)).TextColor(tk.TextFaint)
		}
	})
}

// chatPanel is 超级Agent's own conversation on the overview: the last few turns,
// oldest first, so a question and its answer are read where the person is
// (P0-7). A projector without a chat shard draws no panel: an empty chat would
// read as "it never said anything".
func (a *App) chatPanel(c *mui.Context) {
	chat := a.snap.Chat()
	if chat == nil {
		return
	}
	msgs := chat.Tail(8)
	if len(msgs) == 0 && !a.dispatchPending {
		return
	}
	tk := a.tk
	card(c, tk, 14, func() {
		mui.Row(c).Padding(0, 2, 10, 2).Gap(8).AlignItems(mui.Center).Children(func() {
			mui.Icon(c, icSpark).FontSize(fs(14)).TextColor(tk.Accent)
			mui.Text(c, "超级Agent").FontSize(fs(15)).Bold().TextColor(tk.Text)
			mui.Spacer(c)
			mui.Text(c, "问点什么，它回答；派一件事，它去做").FontSize(fs(11)).TextColor(tk.TextFaint)
		})
		mui.Box(c).Height(1).Background(tk.BorderSoft)
		for _, m := range msgs {
			a.chatBubble(c, m)
		}
		if a.dispatchPending {
			if a.dispatchEcho != "" {
				a.chatBubble(c, view.ChatMessage{Role: view.ChatUser, Text: a.dispatchEcho})
			}
			mui.Row(c).Gap(8).AlignItems(mui.Center).Children(func() {
				dot(c, tk.Accent, 6)
				mui.Text(c, "发送中…").FontSize(fs(12)).TextColor(tk.Accent)
			})
		}
	})
}

// chatBubble is one line of the conversation. The operator's is tinted and
// right-aligned; the agent's is plain and red only for a genuine failure.
func (a *App) chatBubble(c *mui.Context, m view.ChatMessage) {
	tk := a.tk
	row := mui.Row(c).Gap(9).AlignItems(mui.Start).Padding(7, 4)
	if m.Role == view.ChatUser {
		row.Background(tk.AccentSoft)
	}
	row.Children(func() {
		label := "你"
		fg := tk.TextMuted
		switch m.Role {
		case view.ChatAssistant:
			label, fg = "超级Agent", tk.Accent
			if m.IsFailure() {
				fg = tk.Red
			}
		case view.ChatSystem:
			label, fg = "记录", tk.TextFaint
		}
		mui.Text(c, label).FontSize(fs(11)).Bold().Width(64).Shrink(0).TextColor(fg)
		mui.Column(c).Grow(1).Gap(2).Children(func() {
			mui.Text(c, m.Text).FontSize(fs(13)).TextColor(ifColor(m.IsFailure(), tk.Red, tk.Text))
			if kind := chatKindLabel(m.Kind); kind != "" {
				mui.Text(c, kind).FontSize(fs(10)).TextColor(tk.TextFaint)
			}
		})
		if !m.At.IsZero() {
			mui.Text(c, humanfmt.Ago(a.age(m.At))).FontSize(fs(10)).TextColor(tk.TextFaint)
		}
	})
}

// chatKindLabel says what a non-answer line is, in one word, or "" for a plain
// answer (the common case needs no label).
func chatKindLabel(k view.ChatKind) string {
	switch k {
	case view.ChatOutcome:
		return "结果"
	case view.ChatNote:
		return "说明"
	case view.ChatError:
		return "失败"
	}
	return ""
}
