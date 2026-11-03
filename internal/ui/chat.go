package ui

import (
	mui "github.com/egoist/mygo/ui"

	"github.com/zeokeel/super-agent/internal/humanfmt"
	"github.com/zeokeel/super-agent/internal/view"
)

// 超级Agent 的对话: the compact conversation on the overview, and the one line above
// the composer on every page. Chat answers, outcomes and notes are neutral; only
// a genuine failure (ChatKind error / a red error note) is red — "没有派发工单"
// is an answer, not a failure (P0-7).

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
