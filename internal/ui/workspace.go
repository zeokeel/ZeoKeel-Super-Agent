package ui

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo"
	mui "github.com/egoist/mygo/ui"

	"github.com/zeokeel/super-agent/internal/humanfmt"
	"github.com/zeokeel/super-agent/internal/view"
)

// The native folder picker, as two seams a headless test can replace.
//
// mygo's Dialog.Open blocks until the person dismisses the dialog (its doc says it
// may be called from any goroutine), so startFolderPicker runs it on its own
// goroutine and posts the chosen path back through Window.Update: a frame never
// waits for a person. Tests replace startFolderPicker to answer at once, and
// openFolderDialog when they want to see the options asked for.
var (
	openFolderDialog = func(parent *mygo.Window, title string) ([]string, error) {
		return mygo.Dialog.Open(folderDialogOptions(parent, title))
	}
	startFolderPicker = func(a *App) {
		go func() {
			paths, err := openFolderDialog(a.win, "选择工作区目录")
			apply := func() { a.pickFolderDone(paths, err) }
			if a.win != nil {
				// The app's state is the main thread's: come back through it.
				a.win.Update(apply)
				return
			}
			apply()
		}()
	}
)

// folderDialogOptions is the native dialog the picker asks for: a directory
// chooser with a "new folder" button, attached to the window. It is one function
// so a test can pin the contract without a display.
func folderDialogOptions(parent *mygo.Window, title string) mygo.OpenDialogOptions {
	return mygo.OpenDialogOptions{
		Parent:            parent,
		Title:             title,
		Directory:         true,
		CreateDirectories: true,
		ButtonLabel:       "选择此目录",
	}
}

// browseFolder opens the native directory picker for the workspace root. The
// dialog is native on every platform (mygo's own: GTK/portal, Windows, macOS);
// the panel does not shell out to anything.
func (a *App) browseFolder() {
	if a.pickingFolder {
		return
	}
	a.pickingFolder = true
	a.wsErr = ""
	startFolderPicker(a)
}

// pickFolderDone applies the picker's answer, on the main thread. A cancelled
// dialog returns no paths: the form keeps what it had, and no error is shown for
// a decision a person made.
func (a *App) pickFolderDone(paths []string, err error) {
	a.pickingFolder = false
	if err != nil {
		a.wsErr = err.Error()
		return
	}
	if len(paths) == 0 {
		return
	}
	if path := strings.TrimSpace(paths[0]); path != "" {
		a.wsDraft.root = path
	}
}

// 工作区 and 会话: the sidebar's middle group, the 工作区 page, and the two dialogs
// that create them. The runtime owns the projections and the commands; the panel
// only draws and asks, through optional interfaces, so the window never imports
// the runtime (design §6.2).

// WorkspaceCreator creates one workspace, the container the agent works in.
type WorkspaceCreator interface {
	CreateWorkspace(name, root, purpose, kind string) error
}

// ConversationCreator starts a conversation (a Ticket) in a workspace.
type ConversationCreator interface {
	NewConversation(workspaceID, text string) error
}

// The kinds a workspace can be, and how they read. The ids are what the runtime
// stores; the labels are what the chips say.
var (
	workspaceKinds  = []string{"repo", "data", "docs", "mixed"}
	workspaceKindZH = []string{"仓库", "数据", "文档", "混合"}
)

// workspaceDraft is the 新建工作区 form's text, held across frames in app state so
// the inputs keep what was typed (design §6.1, rule 2).
type workspaceDraft struct {
	name, root, purpose string
}

func (a *App) workspaceCreator() (WorkspaceCreator, bool) {
	if a.cmd == nil {
		return nil, false
	}
	c, ok := a.cmd.(WorkspaceCreator)
	return c, ok && c != nil
}

func (a *App) conversationCreator() (ConversationCreator, bool) {
	if a.cmd == nil {
		return nil, false
	}
	c, ok := a.cmd.(ConversationCreator)
	return c, ok && c != nil
}

// workspaceKind returns the chosen kind's id and its label.
func (a *App) workspaceKind() (id, label string) {
	i := a.wsKind
	if i < 0 || i >= len(workspaceKinds) {
		i = 0
	}
	return workspaceKinds[i], workspaceKindZH[i]
}

// systemLabel is the one line the sidebar footer shows: OS/arch and the
// timezone the runtime reported. Without a projection it says so rather than
// guessing "UTC".
func (a *App) systemLabel() string {
	ov := a.snap.Overview()
	if ov == nil || !ov.System.Known() {
		return "系统信息未投影"
	}
	return ov.System.Label()
}

// workspaceSection is the sidebar's 工作区 group: a heading with the new-workspace
// entry, then one row per workspace, each expandable to its conversations.
func (a *App) workspaceSection(c *mui.Context) {
	tk := a.tk
	mui.Column(c).AlignItems(mui.Stretch).Children(func() {
		mui.Row(c).Padding(14, 10, 4, 10).Gap(6).AlignItems(mui.Center).Children(func() {
			mui.Text(c, "工作区").FontSize(13).Bold().TextColor(tk.TextFaint)
			mui.Spacer(c)
			if a.newWorkspaceEntry(c) {
				return
			}
		})
		vm := a.snap.Workspaces()
		if vm == nil {
			mui.Row(c).Padding(4, 16).Children(func() {
				mui.Text(c, "等待运行时发布工作区…").FontSize(13).TextColor(tk.TextFaint)
			})
			mui.Row(c).Padding(2, 10).Children(func() { a.newWorkspaceRow(c) })
			return
		}
		if len(vm.Workspaces) == 0 {
			mui.Row(c).Padding(4, 16).Children(func() {
				mui.Text(c, "还没有工作区。新建一个，Agent 才有地方干活。").FontSize(13).
					TextColor(tk.TextMuted)
			})
			mui.Row(c).Padding(2, 10).Children(func() { a.newWorkspaceRow(c) })
			return
		}
		for i := range vm.Workspaces {
			a.workspaceRow(c, &vm.Workspaces[i])
		}
		mui.Row(c).Padding(2, 10).Children(func() { a.newWorkspaceRow(c) })
	})
}

// newWorkspaceEntry is the "＋" of the 工作区 heading; it returns true when it
// was clicked.
func (a *App) newWorkspaceEntry(c *mui.Context) bool {
	tk := a.tk
	plus := mui.Row(c).Size(18, 18).Center().Radius(5).Children(func() {
		mui.Text(c, "＋").FontSize(16).TextColor(tk.TextMuted)
	})
	if plus.Clicked() {
		a.openWorkspaceModal()
		return true
	}
	return false
}

// newWorkspaceRow is the sidebar's discoverable "＋ 新建工作区".
func (a *App) newWorkspaceRow(c *mui.Context) {
	tk := a.tk
	row := mui.Row(c).Height(30).Padding(0, 8).Gap(8).Radius(7).AlignItems(mui.Center).
		OnClick(func() { a.openWorkspaceModal() })
	row.Children(func() {
		mui.Icon(c, icFolder).FontSize(14).TextColor(tk.TextFaint)
		mui.Text(c, "＋ 新建工作区").FontSize(15).TextColor(tk.TextMuted)
	})
}

// workspaceRow is one workspace in the sidebar: an expand arrow, the name, and —
// when expanded — its conversations and its "＋ 新建会话".
func (a *App) workspaceRow(c *mui.Context, ws *view.WorkspaceEntryVM) {
	tk := a.tk
	expanded := a.wsExpanded[ws.ID]
	on := a.sec == view.SecWorkspace && a.workspaceSel == ws.ID
	mui.Column(c).Children(func() {
		mui.Row(c).Height(32).Padding(0, 6).Gap(6).Radius(7).AlignItems(mui.Center).
			Background(ifColor(on, tk.AccentSoft, mui.Transparent)).Children(func() {
			// The arrow toggles; the rest of the row opens the workspace page.
			chev := mui.Row(c).Size(18, 18).Center().Label("展开工作区 " + ws.Name).OnClick(func() {
				a.wsExpanded[ws.ID] = !expanded
			})
			chev.Children(func() {
				mui.Icon(c, ifIcon(expanded, icChevD, icChevR)).FontSize(12).TextColor(tk.TextFaint)
			})
			body := mui.Row(c).Grow(1).Gap(6).AlignItems(mui.Center).
				Label("工作区 " + ws.Name).OnClick(func() {
				a.openWorkspace(ws.ID)
			})
			body.Children(func() {
				mui.Icon(c, icFolder).FontSize(14).
					TextColor(ifColor(on, tk.Accent, tk.TextMuted))
				mui.Text(c, ws.Name).FontSize(16).SingleLine().
					TextColor(ifColor(on, tk.Accent, tk.Text))
				mui.Spacer(c)
				if n := len(ws.Conversations); n > 0 {
					mui.Text(c, fmt.Sprint(n)).FontSize(12).TextColor(tk.TextFaint)
				}
			})
		})
		if !expanded {
			return
		}
		mui.Column(c).Padding(0, 0, 0, 26).Children(func() {
			a.conversationRows(c, ws)
			row := mui.Row(c).Height(26).Padding(0, 8).Gap(6).Radius(6).AlignItems(mui.Center).
				OnClick(func() { a.openConversationModal(ws.ID) })
			row.Children(func() {
				mui.Text(c, "＋ 新建会话").FontSize(14).TextColor(tk.TextMuted)
			})
		})
	})
}

// conversationRows draws one workspace's conversations in the sidebar, grouped by
// who created them.
func (a *App) conversationRows(c *mui.Context, ws *view.WorkspaceEntryVM) {
	tk := a.tk
	groups := ws.Groups()
	if len(groups) == 0 {
		mui.Row(c).Padding(2, 8).Children(func() {
			mui.Text(c, "还没有会话").FontSize(13).TextColor(tk.TextFaint)
		})
		return
	}
	for _, g := range groups {
		mui.Row(c).Padding(4, 8).Children(func() {
			mui.Text(c, g.Label).FontSize(12).TextColor(tk.TextFaint)
		})
		for i := range g.Conversations {
			a.conversationRow(c, ws, g.Conversations[i])
		}
	}
}

// conversationRow is one conversation in the sidebar: who made it, its number and
// title, and a dot while it waits for a person.
func (a *App) conversationRow(c *mui.Context, ws *view.WorkspaceEntryVM, conv view.ConversationVM) {
	tk := a.tk
	icon := icUser
	if conv.CreatedBy == view.CreatedByAgent {
		icon = icSpark
	}
	row := mui.Row(c).Height(28).Padding(0, 8).Gap(6).Radius(6).AlignItems(mui.Center).
		Label("会话 " + conv.Title).OnClick(func() { a.openConversation(ws.ID, conv) })
	row.Children(func() {
		mui.Icon(c, icon).FontSize(12).TextColor(tk.TextMuted)
		if id := conv.IDLabel(); id != "" {
			mui.Text(c, id).FontSize(13).Font("monospace").TextColor(tk.TextFaint)
		}
		mui.Text(c, conv.Title).FontSize(15).SingleLine().Grow(1).TextColor(tk.Text)
		if conv.Attention {
			dot(c, tk.Amber, 6)
		}
	})
}

// openWorkspace shows a workspace on 工作区.
func (a *App) openWorkspace(id string) {
	a.workspaceSel = id
	a.wsExpanded[id] = true
	a.sec = view.SecWorkspace
	a.locate = nil
	a.goalFocus = ""
}

// openConversation opens the Ticket behind a conversation: it selects the Ticket
// on 执行 and expands its row. Navigation, not an action (design §6.3).
func (a *App) openConversation(wsID string, conv view.ConversationVM) {
	a.workspaceSel = wsID
	a.wsExpanded[wsID] = true
	a.openTicket(conv.ID)
}

// ---------------------------------------------------------------------------
// 工作区 page
// ---------------------------------------------------------------------------

// workspacePage is 工作区: what the workspace is for, where its root is, and its
// conversations, grouped by who created them.
func (a *App) workspacePage(c *mui.Context) {
	tk := a.tk
	vm := a.snap.Workspaces()

	mui.Column(c).Fill().Padding(20, 24).Gap(12).Children(func() {
		mui.Row(c).AlignItems(mui.Center).Gap(10).Children(func() {
			heading(c, tk, "工作区", "一个仓库或数据目录；会话就是它里面的工单，点一条打开它的执行")
			mui.Spacer(c)
			if a.newWorkspaceEntry(c) {
				return
			}
		})
		if vm == nil {
			mui.Text(c, "等待运行时发布工作区投影…").FontSize(15).TextColor(tk.TextMuted)
			return
		}
		ws := vm.Find(a.workspaceSel)
		if ws == nil {
			ws = vm.SelectedWorkspace()
		}
		if ws == nil {
			card(c, tk, 16, func() {
				mui.Text(c, "还没有工作区").FontSize(17).Bold().TextColor(tk.Text)
				mui.Text(c, "新建一个工作区之后，Agent 才有地方干活；也可以继续在总览里派活。").
					FontSize(13).TextColor(tk.TextMuted)
				a.newWorkspaceRow(c)
			})
			return
		}
		a.workspaceSel = ws.ID

		// What the workspace is.
		card(c, tk, 16, func() {
			mui.Row(c).Gap(10).AlignItems(mui.Center).Children(func() {
				mui.Icon(c, icFolder).FontSize(20).TextColor(tk.Accent)
				mui.Text(c, ws.Name).FontSize(20).Bold().TextColor(tk.Text)
				textChip(c, tk, ws.Kind)
				mui.Spacer(c)
				if n := len(ws.Conversations); n > 0 {
					chip(c, tk, fmt.Sprintf("%d 个会话", n), tk.TextMuted, tk.SurfaceAlt)
				}
				if !ws.LastAt.IsZero() {
					mui.Text(c, "最近 "+humanfmt.Ago(a.age(ws.LastAt))).FontSize(13).
						TextColor(tk.TextFaint)
				}
			})
			if ws.Purpose != "" {
				mui.Text(c, ws.Purpose).FontSize(15).TextColor(tk.TextMuted)
			}
			if ws.Root != "" {
				mui.Row(c).Gap(8).AlignItems(mui.Center).Children(func() {
					mui.Text(c, "根目录").FontSize(13).TextColor(tk.TextFaint)
					mono(c, tk, ws.Root)
				})
			}
		})

		// Its conversations.
		mui.Column(c).Grow(1).MinHeight(0).Background(tk.Surface).Radius(tk.Radius).
			Border(1, tk.Border).Children(func() {
			mui.Row(c).Height(34).Padding(0, 16).Gap(8).AlignItems(mui.Center).
				Background(tk.SurfaceAlt).Children(func() {
				mui.Text(c, "会话").FontSize(14).Bold().TextColor(tk.Text)
				mui.Spacer(c)
				row := mui.Row(c).Height(24).Padding(0, 10).Radius(6).AlignItems(mui.Center).
					Background(tk.Surface).Border(1, tk.Border).OnClick(func() {
					a.openConversationModal(ws.ID)
				})
				row.Children(func() {
					mui.Text(c, "＋ 新建会话").FontSize(14).TextColor(tk.Text)
				})
			})
			mui.Box(c).Height(1).Background(tk.Border)
			groups := ws.Groups()
			if len(groups) == 0 {
				mui.Row(c).Padding(16, 14).Children(func() {
					mui.Text(c, "这个工作区还没有会话。新建一个，或在总览里派活。").
						FontSize(13).TextColor(tk.TextMuted)
				})
				return
			}
			mui.Scroll(c).Grow(1).Children(func() {
				mui.Column(c).Children(func() {
					for _, g := range groups {
						mui.Row(c).Height(28).Padding(0, 16).AlignItems(mui.Center).
							Background(tk.SurfaceAlt).Children(func() {
							mui.Text(c, g.Label).FontSize(13).Bold().TextColor(tk.TextMuted)
						})
						for i := range g.Conversations {
							a.workspaceConversationRow(c, ws, g.Conversations[i])
						}
					}
				})
			})
		})
	})
}

// workspaceConversationRow is one conversation on 工作区: its number, title, where
// it came from, its stage, and when it last moved. A click opens its Ticket.
func (a *App) workspaceConversationRow(c *mui.Context, ws *view.WorkspaceEntryVM, conv view.ConversationVM) {
	tk := a.tk
	located := a.locate != nil && a.locate.TicketID == conv.ID
	row := mui.Row(c).Height(44).Padding(0, 16).Gap(10).AlignItems(mui.Center)
	if located || conv.Attention {
		row.Background(tk.AccentSoft)
	}
	row.Label("会话 " + conv.Title).OnClick(func() { a.openConversation(ws.ID, conv) }).Children(func() {
		mui.Icon(c, ifIcon(conv.CreatedBy == view.CreatedByAgent, icSpark, icUser)).
			FontSize(14).TextColor(ifColor(conv.Attention, tk.Amber, tk.TextMuted))
		if id := conv.IDLabel(); id != "" {
			mui.Text(c, id).FontSize(13).Font("monospace").TextColor(tk.TextFaint)
		}
		mui.Text(c, conv.Title).FontSize(16).SingleLine().Grow(1).TextColor(tk.Text)
		textChip(c, tk, conv.CreatedLabel())
		if conv.Source != "" {
			mui.Text(c, conv.Source).FontSize(12).TextColor(tk.TextFaint)
		}
		if conv.Stage != "" {
			chip(c, tk, conv.Stage, tk.TextMuted, tk.SurfaceAlt)
		}
		if conv.Attention {
			chip(c, tk, "待你处理", tk.Amber, tk.AmberSoft)
		}
		if !conv.LastAt.IsZero() {
			mui.Text(c, humanfmt.Ago(a.age(conv.LastAt))).FontSize(12).TextColor(tk.TextFaint)
		}
		mui.Icon(c, icChevR).FontSize(13).TextColor(tk.TextFaint)
	})
}

// ---------------------------------------------------------------------------
// 新建工作区 dialog
// ---------------------------------------------------------------------------

// openWorkspaceModal starts the 新建工作区 form with the defaults the runtime
// wants: a repo workspace with nothing filled in yet.
func (a *App) openWorkspaceModal() {
	a.wsModal = true
	a.wsDraft = workspaceDraft{}
	a.wsKind = 0
	a.wsErr = ""
}

// workspaceDialog is 新建工作区: name, root, purpose and kind. Without a
// WorkspaceCreator the form still opens, but creating is disabled with the reason
// — the operator can see what the field asks for and the runtime cannot silently
// do nothing.
func (a *App) workspaceDialog(c *mui.Context) {
	if !a.wsModal {
		return
	}
	tk := a.tk
	creator, wired := a.workspaceCreator()
	open := true
	mui.Modal(c, &open, func() {
		mui.Column(c).Width(560).Gap(10).Children(func() {
			mui.Text(c, "新建工作区").FontSize(19).Bold().TextColor(tk.Text)
			mui.Text(c, "工作区是一个仓库或数据目录；超级Agent 在里面干活，会话属于它。").
				FontSize(12).TextColor(tk.TextMuted)
			a.formTextField(c, "名称", "如 acme/webapp；面板里显示的名字", &a.wsDraft.name, "workspace-name", true)
			a.workspaceRootField(c)
			a.formTextField(c, "用途", "这个工作区负责什么；Router 用它判断派给谁", &a.wsDraft.purpose, "workspace-purpose", false)
			mui.Row(c).Gap(8).AlignItems(mui.Center).Children(func() {
				mui.Text(c, "类型").FontSize(14).TextColor(tk.TextMuted)
				mui.Segmented(c.Key("workspace-kind"), &a.wsKind, workspaceKindZH...).
					Label("工作区类型")
			})
			if !wired {
				mui.Text(c, "动作未接线：运行时未实现 WorkspaceCreator（表单可看，创建不可用）").
					FontSize(11).TextColor(tk.Amber)
			}
			if a.wsErr != "" {
				mui.Text(c, a.wsErr).FontSize(13).TextColor(tk.Red)
			}
			mui.Row(c).Gap(8).AlignItems(mui.Center).Children(func() {
				mui.Spacer(c)
				if mui.Button(c, "取消").Clicked() {
					a.wsModal = false
					a.wsErr = ""
				}
				label := "创建"
				btn := mui.PrimaryButton(c, label).Label(label)
				if !wired || strings.TrimSpace(a.wsDraft.name) == "" || strings.TrimSpace(a.wsDraft.root) == "" {
					btn.Disabled(true)
				} else if btn.Clicked() && creator != nil {
					a.submitWorkspace(creator)
				}
			})
		})
	})
	if !open {
		a.wsModal = false
	}
}

// formTextField is one labeled text input of a dialog. The first field takes the
// focus, so typing starts there; the label names the input for a test and for
// assistive technology.
func (a *App) formTextField(c *mui.Context, label, help string, value *string, key string, focus bool) {
	tk := a.tk
	mui.Column(c).Gap(4).Children(func() {
		mui.Row(c).Gap(6).AlignItems(mui.Center).Children(func() {
			mui.Text(c, label).FontSize(14).TextColor(tk.TextMuted)
			if help != "" {
				mui.Text(c, "· "+help).FontSize(13).SingleLine().TextColor(tk.TextFaint)
			}
		})
		in := mui.TextInput(c.Key(key), value).Height(32).FontSize(16).Label("输入" + label)
		if focus {
			in.AutoFocus()
		}
	})
}

// workspaceRootField is the root directory input with the native folder picker
// beside it. While a picker is open the button says so and is disabled: one
// dialog at a time, and nothing looks frozen.
func (a *App) workspaceRootField(c *mui.Context) {
	tk := a.tk
	mui.Column(c).Gap(4).Children(func() {
		mui.Row(c).Gap(6).AlignItems(mui.Center).Children(func() {
			mui.Text(c, "根目录").FontSize(14).TextColor(tk.TextMuted)
			mui.Text(c, "· 仓库或数据的本地路径，Agent 的沙箱根").FontSize(13).SingleLine().
				TextColor(tk.TextFaint)
		})
		mui.Row(c).Gap(8).AlignItems(mui.Center).Children(func() {
			mui.TextInput(c.Key("workspace-root"), &a.wsDraft.root).Height(32).FontSize(16).
				Label("输入根目录").Grow(1)
			label := "浏览…"
			if a.pickingFolder {
				label = "选择中…"
			}
			btn := mui.Button(c, label).Label(label).Height(32)
			if a.pickingFolder {
				btn.Disabled(true)
			} else if btn.Clicked() {
				a.browseFolder()
			}
		})
		if a.pickingFolder {
			mui.Text(c, "系统目录选择器已打开；选完会把路径填回上面。").
				FontSize(11).TextColor(tk.TextFaint)
		}
	})
}

// submitWorkspace sends the form; the dialog stays open with the runtime's error
// when it refuses, so nothing typed is lost.
func (a *App) submitWorkspace(creator WorkspaceCreator) {
	kind, _ := a.workspaceKind()
	if err := creator.CreateWorkspace(strings.TrimSpace(a.wsDraft.name),
		strings.TrimSpace(a.wsDraft.root), strings.TrimSpace(a.wsDraft.purpose), kind); err != nil {
		a.wsErr = err.Error()
		return
	}
	a.wsErr = ""
	a.wsModal = false
}

// ---------------------------------------------------------------------------
// 新建会话 dialog
// ---------------------------------------------------------------------------

// openConversationModal starts 新建会话 in a workspace.
func (a *App) openConversationModal(wsID string) {
	a.convModal = true
	a.convWS = wsID
	a.convDraft = ""
	a.convErr = ""
}

// conversationWorkspace returns the workspace the dialog is for, for its heading.
func (a *App) conversationWorkspace() *view.WorkspaceEntryVM {
	if vm := a.snap.Workspaces(); vm != nil {
		return vm.Find(a.convWS)
	}
	return nil
}

// conversationDialog is 新建会话: the message that starts it, in the workspace the
// sidebar row belonged to. The runtime turns it into a Ticket and a decision.
func (a *App) conversationDialog(c *mui.Context) {
	if !a.convModal {
		return
	}
	tk := a.tk
	creator, wired := a.conversationCreator()
	ws := a.conversationWorkspace()
	open := true
	mui.Modal(c, &open, func() {
		mui.Column(c).Width(560).Gap(10).Children(func() {
			mui.Text(c, "新建会话").FontSize(19).Bold().TextColor(tk.Text)
			where := a.convWS
			if ws != nil && ws.Name != "" {
				where = ws.Name
			}
			mui.Row(c).Gap(8).AlignItems(mui.Center).Children(func() {
				mui.Icon(c, icFolder).FontSize(14).TextColor(tk.TextMuted)
				mui.Text(c, where).FontSize(15).TextColor(tk.TextMuted)
			})
			mui.Text(c, "说一句话就开一个会话：超级Agent 会把它变成工单，并在同一个工作区里执行。").
				FontSize(12).TextColor(tk.TextMuted)
			a.formTextField(c, "会话内容", "要它做什么；说清楚目标就行", &a.convDraft, "conversation-text", true)
			if !wired {
				mui.Text(c, "动作未接线：运行时未实现 ConversationCreator（表单可看，创建不可用）").
					FontSize(11).TextColor(tk.Amber)
			}
			if a.convErr != "" {
				mui.Text(c, a.convErr).FontSize(13).TextColor(tk.Red)
			}
			mui.Row(c).Gap(8).AlignItems(mui.Center).Children(func() {
				mui.Spacer(c)
				if mui.Button(c, "取消").Clicked() {
					a.convModal = false
					a.convErr = ""
				}
				label := "创建会话"
				btn := mui.PrimaryButton(c, label).Label(label)
				if !wired || strings.TrimSpace(a.convDraft) == "" {
					btn.Disabled(true)
				} else if btn.Clicked() && creator != nil {
					if err := creator.NewConversation(a.convWS, strings.TrimSpace(a.convDraft)); err != nil {
						a.convErr = err.Error()
					} else {
						a.convErr = ""
						a.convModal = false
					}
				}
			})
		})
	})
	if !open {
		a.convModal = false
	}
}
