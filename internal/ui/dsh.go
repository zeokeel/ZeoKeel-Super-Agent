package ui

// DSH-aligned shell: a 280px host sidebar (native controls only), a Home view with
// a centered composer, a Session view with the message flow, and the 超级Agent as a
// plugin-like panel with native tabs. The host owns chat/sessions; the panel does
// supervision and configuration.

import (
	"strings"

	mui "github.com/egoist/mygo/ui"

	"github.com/zeokeel/super-agent/internal/humanfmt"
	"github.com/zeokeel/super-agent/internal/view"
)

// dshView is the whole window: sidebar + main area.
func (a *App) dshView(c *mui.Context) {
	tk := a.tk
	if a.mode == "" {
		a.mode = "home"
	}
	mui.Row(c).Fill().AlignItems(mui.Stretch).Background(tk.Bg).Children(func() {
		a.dshSidebar(c)
		mui.Box(c).Width(1).Background(tk.Border)
		mui.Column(c).Grow(1).MinWidth(0).AlignItems(mui.Stretch).Children(func() {
			a.dshMain(c)
			a.toastsView(c)
		})
	})
}

// dshSidebar: DSH geometry — 280px wide, 12px inline padding, 38px New Session
// button (outline, radius 12), 36px nav rows with radius 12, muted 12px section
// label, flat project/session rows, and 设置/更多 as rows.
func (a *App) dshSidebar(c *mui.Context) {
	tk := a.tk
	mui.Column(c).Width(280).AlignItems(mui.Stretch).Background(tk.Surface).
		BorderWidth(0, 0.5, 0, 0).BorderColor(tk.Border).Padding(0, 12).Children(func() {
		mui.Row(c).Padding(14, 4).Gap(8).AlignItems(mui.Center).Children(func() {
			uiLogo(c, 20)
			mui.Text(c, "ZeoKeel").FontSize(fs(15)).Bold().TextColor(tk.Text)
		})
		mui.Row(c).Padding(0, 0, 10, 0).Children(func() {
			if mui.Button(c, "＋ 新对话").Label("新对话").Width(256).Height(38).
				Radius(12).FontSize(14).BorderWidth(0.5).BorderColor(tk.Border).
				Background(tk.Surface).TextColor(tk.Text).Clicked() {
				a.newHostChat()
			}
		})
		mui.Column(c).Gap(2).Children(func() {
			a.navRow(c, icSpark, "超级Agent", a.mode == "panel", itoa0(a.attentionCount()), func() {
				a.openSuperAgentPanel(0)
			})
			a.navRow(c, icClock, "自动化任务", a.mode == "panel" && a.panelTab == 2, "", func() {
				a.openSuperAgentPanel(2)
			})
		})
		mui.Row(c).Padding(0, 4, 0, 4).Gap(8).AlignItems(mui.Center).Children(func() {
			mui.Text(c, "项目").FontSize(fs(12)).Bold().TextColor(tk.TextMuted)
			mui.Spacer(c)
			mui.MenuButton(c, "⋯", func(m *mui.Menu) {
				if m.Item("搜索项目…").Chosen() {
					a.openSearch()
				}
				if m.Item("新建项目…").Chosen() {
					a.openWorkspaceModal()
				}
				if m.Item("新建会话…").Chosen() {
					a.newConversationShortcut()
				}
			}).Label("项目菜单").BorderWidth(0).Background(tk.Surface)
		})
		mui.Scroll(c).Grow(1).Children(func() {
			mui.Column(c).Padding(0, 2, 4, 2).Gap(1).Children(func() {
				vm := a.snap.Workspaces()
				if vm == nil || len(vm.Workspaces) == 0 {
					mui.Text(c, "还没有项目 · 用 ＋ 新建").FontSize(fs(12)).TextColor(tk.TextFaint)
				} else {
					for _, ws := range vm.Workspaces {
						ws := ws
						a.sidebarItem(c, icFolder, false, "项目 "+ws.Name, ws.Name,
							a.workspaceSel == ws.ID, 36, "", func() {
								a.openProject(ws.ID)
							})
					}
					if ws := a.selectedProject(); ws != nil && len(ws.Conversations) > 0 {
						for _, conv := range ws.Conversations {
							conv := conv
							trailing := ""
							if !conv.LastAt.IsZero() {
								trailing = humanfmt.Ago(a.age(conv.LastAt))
							}
							a.sidebarItem(c, nil, true, "会话 "+conv.Title, conv.Title,
								false, 32, trailing, func() {
									a.openHostSession(conv.ID)
								})
						}
					}
				}
				a.sidebarItem(c, nil, false, "＋ 新建项目", "＋ 新建项目", false, 36, "",
					func() { a.openWorkspaceModal() })
			})
		})
		mui.Row(c).Padding(8, 0).Children(func() {
			mui.Box(c).Height(0.5).Background(tk.Border)
		})
		a.navRow(c, icSettings, "设置", a.mode == "settings", "", func() { a.openSettings() })
		mui.Row(c).Width(256).Gap(8).Height(36).Padding(7, 8).AlignItems(mui.Center).Children(func() {
			mui.Icon(c, icSpark).FontSize(fs(15)).TextColor(tk.TextMuted)
			mui.MenuButton(c, "更多", func(m *mui.Menu) {
				if m.Item("打开任务接收面板").Chosen() {
					a.openSuperAgentPanel(0)
				}
				if m.Item("自动化任务").Chosen() {
					a.openSuperAgentPanel(2)
				}
				if m.Item("术语表").Chosen() {
					a.openHelp()
				}
				if m.Item("关于").Chosen() {
					a.settingsGroup = view.SettingsAbout
					a.openSettings()
				}
			}).Label("更多").Height(28).FontSize(14).BorderWidth(0).
				Background(tk.Surface).TextColor(tk.Text).PaddingX(0)
		})
	})
}

// selectedProject returns the selected project when one is selected.
func (a *App) selectedProject() *view.WorkspaceEntryVM {
	if a.workspaceSel == "" {
		return nil
	}
	vm := a.snap.Workspaces()
	if vm == nil {
		return nil
	}
	return vm.Find(a.workspaceSel)
}

// openProject selects a project and shows its page.
func (a *App) openProject(id string) {
	a.workspaceSel = id
	a.sec = view.SecWorkspace
	a.mode = "project"
	a.targetWS = id
}

// openHostSession opens one conversation as a host session.
func (a *App) openHostSession(convID string) {
	a.sessionConv = convID
	a.mode = "session"
	if ws := a.selectedProject(); ws != nil {
		a.targetWS = ws.ID
		for _, conv := range ws.Conversations {
			if conv.ID == convID {
				a.sessionTitle = conv.Title
			}
		}
	}
}

// newHostChat is ＋ 新对话: an empty composer on the home view.
func (a *App) newHostChat() {
	a.mode = "home"
	a.sessionConv = ""
	a.sessionTitle = ""
	a.draft = ""
	a.dispatchPending = false
}

// openSuperAgentPanel opens the plugin-like panel on one tab.
func (a *App) openSuperAgentPanel(tab int) {
	a.mode = "panel"
	a.panelTab = tab
	a.sec = panelSection(tab)
	a.locate = nil
	a.goalFocus = ""
}

// openSettings opens the host settings view.
func (a *App) openSettings() {
	a.mode = "settings"
	a.sec = view.SecSettings
	a.locate = nil
}

// panelTabOf maps a page section back to its panel tab (-1 when it is not one).
func panelTabOf(sec view.Section) int {
	switch sec {
	case view.SecOverview:
		return 0
	case view.SecSource:
		return 1
	case view.SecTrigger:
		return 2
	case view.SecEval:
		return 3
	case view.SecExecution:
		return 4
	case view.SecSettings:
		return 5
	}
	return -1
}

// panelSection maps a panel tab to the page section its content reuses.
func panelSection(tab int) view.Section {
	switch tab {
	case 1:
		return view.SecSource
	case 2:
		return view.SecTrigger
	case 3:
		return view.SecEval
	case 4:
		return view.SecExecution
	case 5:
		return view.SecSettings
	}
	return view.SecOverview
}

// dshMain is the main area for the current mode.
func (a *App) dshMain(c *mui.Context) {
	tk := a.tk
	mui.Column(c).Grow(1).MinWidth(0).AlignItems(mui.Stretch).Background(tk.Bg).Children(func() {
		switch a.mode {
		case "session":
			a.sessionView(c)
		case "panel":
			a.superAgentPanel(c)
		case "settings":
			a.settingsView(c)
		case "project":
			a.workspacePage(c)
		default:
			a.homeView(c)
		}
	})
}

// homeView: DSH page padding 28/clamp side, a 960px content column, centered
// title and one composer card.
func (a *App) homeView(c *mui.Context) {
	tk := a.tk
	mui.Column(c).Fill().Padding(pageTop, pageSide, 0, pageSide).AlignItems(mui.Center).Children(func() {
		mui.Box(c).Height(88)
		mui.Text(c, "开始做点什么").FontSize(fs(28)).Bold().TextColor(tk.Text)
		mui.Text(c, "选一个项目，说一句要做什么。").
			FontSize(fs(13)).TextColor(tk.TextMuted)
		mui.Box(c).Height(18)
		a.composerCard(c, 860)
	})
}

// sessionView is the host session: header + message flow + composer.
func (a *App) sessionView(c *mui.Context) {
	tk := a.tk
	title := a.sessionTitle
	if title == "" {
		// The header names the conversation by its first line, never "新对话"
		// alone: DSH shows what the session is about.
		if chat := a.snap.Chat(); chat != nil {
			for _, m := range chat.Messages {
				if m.Role == view.ChatUser && strings.TrimSpace(m.Text) != "" {
					title = firstLine(m.Text, 48)
					break
				}
			}
		}
	}
	if title == "" {
		title = "会话"
	}
	mui.Column(c).Fill().Padding(pageTop, pageSide, 0, pageSide).AlignItems(mui.Stretch).Children(func() {
		// Host session: full main width, header title left, Chat/Trajectory right.
		mui.Row(c).Height(44).Gap(10).AlignItems(mui.Center).
			BorderWidth(0, 0, 0.5, 0).BorderColor(tk.Border).Children(func() {
			if b := a.ghostBtn(c, "←"); b.Label("返回首页").Clicked() {
				a.newHostChat()
			}
			mui.Text(c, title).FontSize(fs(15)).Bold().TextColor(tk.Text)
			mui.Spacer(c)
			mui.Segmented(c.Key("session-seg"), &a.sessionSeg, "Chat", "Trajectory").
				Label("会话视图")
			if chat := a.snap.Chat(); a.chatStreaming(chat) {
				_, wired := a.cmd.(ChatStreamer)
				label := "停止生成"
				if a.chatStopping {
					label = "停止中…"
				}
				stop := a.outlineBtn(c, label)
				if !wired || a.chatStopping {
					stop.Disabled(true)
				} else if stop.Clicked() {
					a.chatStopping = true
					startStopChat(a)
				}
			}
			mui.MenuButton(c, "⋯", func(m *mui.Menu) {
				if m.Item("复制最后回复").Chosen() {
					if last, ok := a.snap.Chat().LastAnswer(); ok {
						a.copyValue(stripAgentPrefix(last.Text), "最后回复")
					}
				}
				if m.Item("术语表").Chosen() {
					a.openHelp()
				}
			}).Label("会话操作").BorderWidth(0).Background(tk.Surface)
		})
		// The running task's plan, then the message flow, at full host width.
		mui.Column(c).Padding(16, 0, 0, 0).Children(func() {
			if plan := a.snap.Plan(); plan != nil && plan.TicketID != "" {
				a.nativePlanCard(c, plan)
			}
		})
		mui.Scroll(c).Grow(1).Children(func() {
			mui.Column(c).Padding(12, 0).Gap(10).AlignItems(mui.Stretch).Children(func() {
				chat := a.snap.Chat()
				if chat == nil || len(chat.Messages) == 0 {
					mui.Text(c, "还没有消息。在下面说一句开始。").
						FontSize(fs(13)).TextColor(tk.TextMuted)
					return
				}
				streaming := a.chatStreaming(chat)
				lastIdx := len(chat.Messages) - 1
				for i, m := range chat.Messages {
					i, m := i, m
					mui.Column(c).Padding(10, 12).BorderWidth(0, 0, 1, 0).
						BorderColor(tk.BorderSoft).Gap(4).Children(func() {
						who := "你"
						fg := tk.TextMuted
						if m.Role == view.ChatAssistant {
							who, fg = "Agent", tk.Accent
						} else if m.Role == view.ChatSystem {
							who, fg = "记录", tk.TextFaint
						}
						if m.IsFailure() {
							fg = tk.Red
						}
						mui.Row(c).Gap(8).AlignItems(mui.Center).Children(func() {
							mui.Text(c, who).FontSize(fs(11)).Bold().TextColor(fg)
							mui.Spacer(c)
							if !m.At.IsZero() {
								mui.Text(c, humanfmt.Ago(a.age(m.At))).FontSize(fs(10)).
									TextColor(tk.TextFaint)
							}
						})
						if m.Role == view.ChatAssistant || m.Role == view.ChatSystem {
							if streaming && i == lastIdx {
								a.renderMarkdown(c, stripAgentPrefix(m.Text)+" ▍", 13)
							} else {
								a.renderMarkdown(c, stripAgentPrefix(m.Text), 13)
							}
						} else {
							mui.Text(c, m.Text).FontSize(fs(13)).TextColor(tk.Text)
						}
					})
				}
			})
		})
		// Composer: the same bordered card as home, 860px centered, fixed bottom.
		mui.Column(c).Padding(12, 0).BorderWidth(0.5, 0, 0, 0).
			BorderColor(tk.Border).AlignItems(mui.Center).Children(func() {
			a.composerContext(c)
			a.composerCard(c, 860)
		})
	})
}

// dshComposer is the host composer: a project target menu, a multiline input and a
// native send button. Enter writes a new line; ⌘/Ctrl+Enter sends.
func (a *App) dshComposer(c *mui.Context) {
	// The one-line "latest answer" context belongs to the session, not to the
	// empty home composer.
	if a.mode == "session" || a.dispatchPending || a.sendFailedErr != "" {
		a.composerContext(c)
	}
	mui.Row(c).Gap(8).AlignItems(mui.Center).Children(func() {
		label := "选择项目"
		if ws := a.workspaceEntry(a.targetWS); ws != nil {
			label = ws.Name
		}
		mui.MenuButton(c, label, func(m *mui.Menu) {
			if vm := a.snap.Workspaces(); vm != nil {
				for _, ws := range vm.Workspaces {
					ws := ws
					if m.Item(ws.Name).Chosen() {
						a.targetWS = ws.ID
					}
				}
			}
		}).Label("项目").Height(30).FontSize(fs(12))
		box := mui.TextArea(c.Key("input"), &a.draft).
			Placeholder("说点什么…（Enter 换行，" + sendKeyHint() + " 发送）").
			Height(56).FontSize(fs(14)).Label("输入消息").Grow(1)
		_ = box
		label = "发送"
		if a.dispatchPending {
			label = "发送中…"
		}
		btn := mui.PrimaryButton(c, label).Label(label).Height(34)
		if a.dispatchPending {
			btn.Disabled(true)
		} else if btn.Clicked() {
			a.submitFromComposer()
		}
	})
	if c.Shortcut(mui.Cmd, mui.KeyEnter) {
		a.submitFromComposer()
	}
}

// submitFromComposer sends and moves to the session view.
func (a *App) submitFromComposer() {
	if strings.TrimSpace(a.draft) == "" || a.dispatchPending {
		return
	}
	a.submit()
	a.mode = "session"
}

// superAgentPanel is the plugin-like 超级Agent panel: native header + native tabs.
func (a *App) superAgentPanel(c *mui.Context) {
	tk := a.tk
	mui.Column(c).Fill().Padding(pageTop, pageSide, 0, pageSide).AlignItems(mui.Center).Children(func() {
		// DSH .dt-page: page title + right toolbar, then the tab pill group; no
		// floating back-arrow header and no toolbar box.
		if want := panelTabOf(a.sec); want >= 0 {
			a.panelTab = want
		}
		mui.Row(c).Width(contentW).Gap(10).AlignItems(mui.Center).Children(func() {
			mui.Text(c, "超级Agent · 接收任务").FontSize(fs(15)).Bold().TextColor(tk.TextMuted)
			mui.Spacer(c)
			search := mui.TextInput(c.Key("panel-search"), &a.panelSearch).
				Placeholder("搜索 工单 / 线程 / 决策…").Width(240).Height(32).FontSize(14).
				BorderWidth(0.5).BorderColor(tk.Border).Radius(8).Padding(0, 8).Label("搜索")
			if search.Submitted() && strings.TrimSpace(a.panelSearch) != "" {
				q := a.panelSearch
				a.openSearch()
				a.searchQuery = q
				a.searchReady = false
			}
			if b := a.outlineBtn(c, "术语表"); b.Clicked() {
				a.openHelp()
			}
			if b := a.primaryBtn(c, "打开主 Session"); b.Clicked() {
				a.openHostSession("")
			}
		})
		labels := []string{"总览", "来源", "触发", "评估", "执行", "设置"}
		if a.panelTab < 0 || a.panelTab >= len(labels) {
			a.panelTab = 0
		}
		// DSH tab group: full column width, 4px padding, 12 radius,
		// module-platform surface; no underline tabs.
		mui.Segmented(c.Key("panel-tabs"), &a.panelTab, labels...).
			Label("面板页面").Width(contentW).Height(34).FontSize(13).
			Background(tk.SurfaceAlt).Radius(12).Padding(4).BorderWidth(0)
		a.sec = panelSection(a.panelTab)
		mui.Column(c).Grow(1).AlignItems(mui.Stretch).Children(func() {
			switch a.panelTab {
			case 1:
				a.sourcePage(c)
			case 2:
				a.triggerPage(c)
			case 3:
				a.evalPage(c)
			case 4:
				a.executionPage(c)
			case 5:
				a.settingsNativePage(c)
			default:
				mui.Scroll(c).Grow(1).Children(func() {
					if avm := a.snap.Attention(); avm != nil && len(avm.Items) > 0 {
						a.attentionInbox(c, avm)
					}
					a.overview(c, a.snap.Overview())
				})
			}
		})
	})
}

// settingsView is the host settings view: page padding, a 960px column, and the
// native settings body (180px group nav + content + sticky save bar).
func (a *App) settingsView(c *mui.Context) {
	tk := a.tk
	mui.Column(c).Fill().Padding(pageTop, pageSide, 0, pageSide).AlignItems(mui.Center).Children(func() {
		mui.Column(c).Width(contentW).Grow(1).AlignItems(mui.Stretch).Children(func() {
			mui.Row(c).Height(44).Gap(10).AlignItems(mui.Center).
				BorderWidth(0, 0, 0.5, 0).BorderColor(tk.Border).Children(func() {
				if b := mui.Button(c, "←").Label("返回首页").Height(28).Width(32).FontSize(14).
					BorderWidth(0).Background(tk.Surface).Clicked(); b {
					a.newHostChat()
				}
				mui.Text(c, "设置").FontSize(fs(13)).Bold().TextColor(tk.TextMuted)
				mui.Spacer(c)
				if b := a.outlineBtn(c, "打开面板"); b.Clicked() {
					a.openSuperAgentPanel(0)
				}
			})
			a.settingsNativePage(c)
		})
	})
}

// stripAgentPrefix removes the runtime's old "超级Agent：" speaker prefix: in a
// conversation the agent has no product name of its own — it is just "Agent".
func stripAgentPrefix(text string) string {
	for _, p := range []string{"超级Agent：", "超级Agent:", "超级Agent "} {
		if strings.HasPrefix(text, p) {
			return strings.TrimSpace(strings.TrimPrefix(text, p))
		}
	}
	return text
}

// newConversationShortcut is ⌘N: a new conversation in the selected project (or
// the first one), or a new project when none exists.
func (a *App) newConversationShortcut() {
	wsID := a.targetWS
	if wsID == "" {
		wsID = a.workspaceSel
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

// itoa0 is Badge text: empty for zero so no hollow badge is drawn.
func itoa0(n int) string {
	if n <= 0 {
		return ""
	}
	return itoa(n)
}
