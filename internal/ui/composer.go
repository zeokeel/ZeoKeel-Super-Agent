package ui

// The composer: a multiline input with a visible target. Enter writes a new line,
// ⌘/Ctrl+Enter sends — so an IME's Enter commits the composition instead of
// sending half a sentence (interaction audit P0-1, P0-6). The target chip says
// where the line goes: the 超级Agent chat, or a project (派活).

import (
	"strings"

	mui "github.com/egoist/mygo/ui"

	"github.com/zeokeel/super-agent/internal/humanfmt"
	"github.com/zeokeel/super-agent/internal/view"
)

// composerTargetBar is the visible target: chat or one project, switchable from
// the same chip. It never guesses: whatever it shows is what submit() uses.
func (a *App) composerTargetBar(c *mui.Context) {
	tk := a.tk
	label := "发给 超级Agent（聊天）"
	target := a.targetWS
	if ws := a.workspaceEntry(target); ws != nil && ws.Name != "" {
		label = "项目 " + ws.Name + "（派活）"
	} else if target != "" {
		label = "项目 " + target + "（派活）"
	}
	mui.Row(c).Gap(8).AlignItems(mui.Center).Children(func() {
		mui.MenuButton(c, label, func(m *mui.Menu) {
			if m.Item("超级Agent（聊天）").Chosen() {
				a.targetWS = ""
			}
			if vm := a.snap.Workspaces(); vm != nil {
				for _, ws := range vm.Workspaces {
					ws := ws
					if m.Item("项目 " + ws.Name + "（派活）").Chosen() {
						a.targetWS = ws.ID
						a.workspaceSel = ws.ID
					}
				}
			}
		}).Label("发送到").Height(26).FontSize(fs(12))
		mui.Text(c, "Enter 换行 · "+sendKeyHint()+" 发送").FontSize(fs(10)).TextColor(tk.TextFaint)
		mui.Spacer(c)
		if a.targetWS != "" {
			clear := mui.Row(c).Height(26).Padding(0, 10).Radius(6).Border(1, tk.Border).
				Background(tk.Surface).AlignItems(mui.Center).Label("切换回超级Agent（聊天）").
				OnClick(func() { a.targetWS = "" })
			clear.Children(func() {
				mui.Text(c, "切回聊天").FontSize(fs(11)).TextColor(tk.TextMuted)
			})
		}
	})
}

// workspaceEntry finds a workspace in the last projection, or nil.
func (a *App) workspaceEntry(id string) *view.WorkspaceEntryVM {
	if id == "" {
		return nil
	}
	if vm := a.snap.Workspaces(); vm != nil {
		if ws := vm.Find(id); ws != nil {
			return ws
		}
	}
	return nil
}

// sendKeyHint names the send shortcut by platform.
func sendKeyHint() string {
	if isDarwin() {
		return "⌘Enter"
	}
	return "Ctrl+Enter"
}

// isDarwin reports the platform for key hints (kept in one place).
func isDarwin() bool { return runtimeGOOS == "darwin" }

// inputBar is the whole input: a multiline box, the target chip above it and one
// primary action. Enter is a newline; ⌘/Ctrl+Enter sends from anywhere in the
// window, so a person with an IME types naturally.
func (a *App) inputBar(c *mui.Context) {
	tk := a.tk
	mui.Column(c).Gap(6).Children(func() {
		a.composerTargetBar(c)
		mui.Row(c).Radius(10).Background(tk.Surface).Border(1, tk.Border).
			Gap(8).Padding(8, 12).AlignItems(mui.Start).Children(func() {
			mui.Icon(c, icSpark).FontSize(fs(15)).TextColor(tk.Accent)
			box := mui.TextArea(c.Key("input"), &a.draft).
				Placeholder("说点什么，或派一件事…（Enter 换行，" + sendKeyHint() + " 发送）").
				Height(56).FontSize(fs(15)).Label("输入消息")
			box.Grow(1)
			label := "发送"
			if a.dispatchPending {
				label = "发送中…"
			}
			btn := mui.PrimaryButton(c, label).Label(label).Height(28)
			if a.dispatchPending {
				btn.Disabled(true)
			} else if btn.Clicked() {
				a.submit()
			}
		})
		// ⌘/Ctrl+Enter sends without leaving the text box.
		if c.Shortcut(mui.Cmd, mui.KeyEnter) {
			a.submit()
		}
	})
}

// composerContext is what the composer says above itself: the pending echo, a
// send failure with 再次发送 (the draft is kept, not silently dropped), or the
// latest agent answer — expandable, with a way to the full history.
func (a *App) composerContext(c *mui.Context) {
	tk := a.tk
	if a.dispatchPending {
		mui.Row(c).Gap(8).AlignItems(mui.Center).Padding(0, 2, 4, 2).Children(func() {
			spinner(c, tk, "发送中…")
			if a.dispatchEcho != "" {
				mui.Text(c, humanfmt.Clip(a.dispatchEcho, 80)).FontSize(fs(12)).SingleLine().
					Grow(1).TextColor(tk.TextMuted)
			}
		})
		return
	}
	if a.sendFailedErr != "" {
		mui.Row(c).Gap(8).AlignItems(mui.Center).Padding(0, 2, 4, 2).Children(func() {
			mui.Icon(c, icBell).FontSize(fs(12)).TextColor(tk.Red)
			mui.Text(c, "没有发出去："+humanfmt.Clip(a.sendFailedErr, 120)).FontSize(fs(12)).
				SingleLine().Grow(1).TextColor(tk.Red)
			mui.Text(c, "内容已保留").FontSize(fs(10)).TextColor(tk.TextFaint)
			retry := mui.Button(c, "再次发送").Label("再次发送").Height(24).FontSize(fs(11))
			if retry.Clicked() {
				a.resend()
			}
		})
		return
	}
	if chat := a.snap.Chat(); chat != nil {
		if m, ok := chat.LastAnswer(); ok {
			a.chatContextLine(c, m)
			return
		}
	}
	if a.composerNote != "" {
		mui.Row(c).Padding(0, 2, 4, 2).Gap(8).AlignItems(mui.Center).Children(func() {
			mui.Icon(c, icBell).FontSize(fs(12)).TextColor(tk.TextMuted)
			mui.Text(c, humanfmt.Clip(a.composerNote, 140)).FontSize(fs(12)).SingleLine().
				Grow(1).TextColor(tk.TextMuted)
		})
		return
	}
	if a.composerErr != "" {
		mui.Row(c).Padding(0, 2, 4, 2).Children(func() {
			mui.Text(c, a.composerErr).FontSize(fs(12)).TextColor(tk.Red)
		})
	}
}

// chatContextLine is the newest agent line, collapsed to one line until 展开, with
// 查看全部 (the full conversation) and 复制 next to it.
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
	text := strings.TrimSpace(m.Text)
	long := len([]rune(text)) > 90
	mui.Column(c).Gap(2).Children(func() {
		mui.Row(c).Padding(0, 2, 2, 2).Gap(8).AlignItems(mui.Center).Children(func() {
			mui.Icon(c, icon).FontSize(fs(12)).TextColor(ifColor(m.IsFailure(), tk.Red, tk.Accent))
			line := mui.Text(c, who+text).FontSize(fs(12)).Grow(1).TextColor(fg)
			if !a.answerExpanded {
				line.SingleLine()
			}
			if !m.At.IsZero() && a.answerExpanded {
				mui.Text(c, humanfmt.Ago(a.age(m.At))).FontSize(fs(10)).TextColor(tk.TextFaint)
			}
		})
		mui.Row(c).Gap(6).AlignItems(mui.Center).Children(func() {
			if long {
				toggle := mui.Button(c, ifStr(a.answerExpanded, "收起", "展开")).Height(22).FontSize(fs(11))
				if toggle.Clicked() {
					a.answerExpanded = !a.answerExpanded
				}
			}
			if cp := mui.Button(c, "复制").Height(22).FontSize(fs(11)); cp.Clicked() {
				a.copyValue(text, "回答")
			}
			if all := mui.Button(c, "查看全部").Height(22).FontSize(fs(11)); all.Clicked() {
				a.sec = view.SecChat
				a.locate = nil
			}
			mui.Spacer(c)
			if !m.At.IsZero() && !a.answerExpanded {
				mui.Text(c, humanfmt.Ago(a.age(m.At))).FontSize(fs(10)).TextColor(tk.TextFaint)
			}
		})
	})
}

func ifStr(cond bool, yes, no string) string {
	if cond {
		return yes
	}
	return no
}

// spinner is the small waiting marker: the label plus animated dots.
func spinner(c *mui.Context, tk Tokens, label string) {
	dot(c, tk.Accent, 6)
	mui.Text(c, label).FontSize(fs(12)).TextColor(tk.Accent)
}

// chatPage is the full 超级Agent conversation (对话记录): bounded history, each
// line copyable, and the last request sendable again.
func (a *App) chatPage(c *mui.Context) {
	tk := a.tk
	chat := a.snap.Chat()
	mui.Column(c).Fill().Padding(24).Gap(12).Children(func() {
		heading(c, tk, "对话记录", "超级Agent 的回答与派发结果；最近的 20 条")
		if chat == nil || len(chat.Messages) == 0 {
			card(c, tk, 14, func() {
				mui.Text(c, "还没有对话").FontSize(fs(15)).Bold().TextColor(tk.Text)
				mui.Text(c, "在下面输入一句，开始和超级Agent 说话。").
					FontSize(fs(13)).TextColor(tk.TextMuted)
			})
			return
		}
		if plan := a.snap.Plan(); plan != nil && plan.TicketID != "" {
			a.planPanel(c, plan, true)
		}
		streaming := a.chatStreaming(chat)
		lastIdx := len(chat.Messages) - 1
		mui.Scroll(c).Grow(1).Children(func() {
			mui.Column(c).Gap(8).Children(func() {
				for i, m := range chat.Messages {
					i, m := i, m
					card(c, tk, 12, func() {
						mui.Row(c).Gap(8).AlignItems(mui.Start).Children(func() {
							role := "你"
							fg := tk.TextMuted
							if m.Role == view.ChatAssistant {
								role, fg = "超级Agent", tk.Accent
							} else if m.Role == view.ChatSystem {
								role, fg = "记录", tk.TextFaint
							}
							if m.IsFailure() {
								fg = tk.Red
							}
							mui.Text(c, role).FontSize(fs(11)).Bold().Width(64).Shrink(0).TextColor(fg)
							mui.Column(c).Grow(1).Children(func() {
								if m.Role == view.ChatAssistant || m.Role == view.ChatSystem {
									// Streamed answers render as Markdown while they arrive;
									// the cursor says "still writing".
									if streaming && i == lastIdx {
										a.renderMarkdown(c, m.Text+" ▍", 13)
									} else {
										a.renderMarkdown(c, m.Text, 13)
									}
								} else {
									mui.Text(c, m.Text).FontSize(fs(13)).TextColor(tk.Text)
								}
							})
							if cp := mui.Button(c, "复制").Height(22).FontSize(fs(10)); cp.Clicked() {
								a.copyValue(m.Text, "这条对话")
							}
						})
					})
				}
			})
		})
		// While the answer streams: 停止. Afterwards: send the last request again.
		mui.Row(c).Gap(8).AlignItems(mui.Center).Children(func() {
			if streaming {
				_, wired := a.cmd.(ChatStreamer)
				label := "停止生成"
				if a.chatStopping {
					label = "停止中…"
				}
				stop := mui.Button(c, label).Label(label).Height(28).FontSize(fs(12))
				if !wired || a.chatStopping {
					stop.Disabled(true)
				} else if stop.Clicked() {
					a.chatStopping = true
					startStopChat(a)
				}
				if !wired {
					mui.Text(c, "暂时不可用：运行时还没有接入停止生成").
						FontSize(fs(10)).TextColor(tk.Amber)
				}
			}
			if lastUser, ok := lastUserMessage(chat); ok {
				if again := mui.Button(c, "再次发送上一条").Label("再次发送上一条").Height(26); again.Clicked() {
					a.draft = lastUser
					a.submit()
				}
			}
			mui.Spacer(c)
		})
	})
}

// newConversationShortcut is ⌘N: a new conversation in the selected project (or
// the first one), or a new project when none exists.
func (a *App) newConversationShortcut() {
	wsID := a.targetWS
	if wsID == "" {
		wsID = a.workspaceSel
	}
	if wsID == "" && a.filterWorkspace != "" {
		wsID = a.filterWorkspace
	}
	if vm := a.snap.Workspaces(); vm != nil && wsID == "" && len(vm.Workspaces) > 0 {
		wsID = vm.Workspaces[0].ID
	}
	if wsID != "" {
		a.openConversationModal(wsID)
		return
	}
	a.openWorkspaceModal()
}
