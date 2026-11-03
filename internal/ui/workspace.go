package ui

import (
	"fmt"
	"strconv"
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

// WorkspaceRenamer renames a workspace; WorkspaceDeleter removes its registration
// from the panel (the directory on disk is the runtime's business; the dialog says
// so). Both are optional: without them the buttons are disabled with the reason.
type WorkspaceRenamer interface {
	RenameWorkspace(id, name string) error
}

type WorkspaceDeleter interface {
	DeleteWorkspace(id string) error
}

// The kinds a workspace can be, and how they read. The ids are what the runtime
// stores; the labels are what the chips say.
var (
	workspaceKinds  = []string{"repo", "data", "docs", "mixed"}
	workspaceKindZH = []string{"仓库", "数据", "文档", "混合"}
)

// workspaceDraft is the 新建项目 form's text, held across frames in app state so
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

func (a *App) workspaceRenamer() (WorkspaceRenamer, bool) {
	if a.cmd == nil {
		return nil, false
	}
	r, ok := a.cmd.(WorkspaceRenamer)
	return r, ok && r != nil
}

func (a *App) workspaceDeleter() (WorkspaceDeleter, bool) {
	if a.cmd == nil {
		return nil, false
	}
	d, ok := a.cmd.(WorkspaceDeleter)
	return d, ok && d != nil
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

// viewScale reads the panel's text scale from the ui.font_scale setting (0.9 /
// 1.0 / 1.1 today), clamped so a bad value cannot make the panel unusable.
func (a *App) settingViewScale() float32 {
	vm := a.snap.Settings()
	if v, ok := vm.FieldValue("ui.font_scale"); ok {
		if f, err := strconv.ParseFloat(strings.TrimSpace(v), 32); err == nil {
			return clampViewScale(float32(f))
		}
	}
	return 1
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
			mui.Text(c, "项目").FontSize(fs(13)).Bold().TextColor(tk.TextFaint)
			mui.Spacer(c)
			if a.newWorkspaceEntry(c) {
				return
			}
		})
		vm := a.snap.Workspaces()
		if vm == nil {
			mui.Row(c).Padding(4, 16).Children(func() {
				mui.Text(c, "等待运行时发布工作区…").FontSize(fs(13)).TextColor(tk.TextFaint)
			})
			mui.Row(c).Padding(2, 10).Children(func() { a.newWorkspaceRow(c) })
			return
		}
		if len(vm.Workspaces) == 0 {
			mui.Row(c).Padding(4, 16).Children(func() {
				mui.Text(c, "还没有工作区。新建一个，Agent 才有地方干活。").FontSize(fs(13)).
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
	plus := mui.Row(c).Size(28, 28).Center().Radius(6).Label("新建项目").Focusable().
		Role(mui.RoleButton).Tooltip("新建项目").Children(func() {
		mui.Text(c, "＋").FontSize(fs(16)).TextColor(tk.TextMuted)
	})
	if plus.Clicked() {
		a.openWorkspaceModal()
		return true
	}
	return false
}

// newWorkspaceRow is the sidebar's discoverable "＋ 新建项目".
func (a *App) newWorkspaceRow(c *mui.Context) {
	tk := a.tk
	row := mui.Row(c).Height(30).Padding(0, 8).Gap(8).Radius(7).AlignItems(mui.Center).
		OnClick(func() { a.openWorkspaceModal() })
	row.Children(func() {
		mui.Icon(c, icFolder).FontSize(fs(14)).TextColor(tk.TextFaint)
		mui.Text(c, "＋ 新建项目").FontSize(fs(15)).TextColor(tk.TextMuted)
	})
}

// workspaceRow is one workspace in the sidebar: an expand arrow, the name, and —
// when expanded — its conversations and its "＋ 新建会话".
func (a *App) workspaceRow(c *mui.Context, ws *view.WorkspaceEntryVM) {
	tk := a.tk
	expanded := a.wsExpanded[ws.ID]
	on := a.filterWorkspace == ws.ID
	mui.Column(c).Children(func() {
		mui.Row(c).Height(32).Padding(0, 6).Gap(6).Radius(7).AlignItems(mui.Center).
			Background(ifColor(on, tk.AccentSoft, mui.Transparent)).Children(func() {
			// The arrow toggles; the rest of the row opens the workspace page.
			chev := mui.Row(c).Size(18, 18).Center().Label("展开项目 " + ws.Name).OnClick(func() {
				a.wsExpanded[ws.ID] = !expanded
			})
			chev.Children(func() {
				mui.Icon(c, ifIcon(expanded, icChevD, icChevR)).FontSize(fs(12)).TextColor(tk.TextFaint)
			})
			body := mui.Row(c).Grow(1).Gap(6).AlignItems(mui.Center).
				Label("项目 " + ws.Name).OnClick(func() {
				a.openWorkspace(ws.ID)
			})
			body.Children(func() {
				mui.Icon(c, icFolder).FontSize(fs(14)).
					TextColor(ifColor(on, tk.Accent, tk.TextMuted))
				mui.Text(c, ws.Name).FontSize(fs(16)).SingleLine().
					TextColor(ifColor(on, tk.Accent, tk.Text))
				mui.Spacer(c)
				if n := len(ws.Conversations); n > 0 {
					mui.Text(c, fmt.Sprint(n)).FontSize(fs(12)).TextColor(tk.TextFaint)
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
				mui.Text(c, "＋ 新建会话").FontSize(fs(14)).TextColor(tk.TextMuted)
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
			mui.Text(c, "还没有会话").FontSize(fs(13)).TextColor(tk.TextFaint)
		})
		return
	}
	for _, g := range groups {
		mui.Row(c).Padding(4, 8).Children(func() {
			mui.Text(c, g.Label).FontSize(fs(12)).TextColor(tk.TextFaint)
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
	row := mui.Row(c).Height(30).Padding(0, 8).Gap(6).Radius(6).AlignItems(mui.Center).
		Focusable().Role(mui.RoleButton).Label("会话 " + conv.Title).Tooltip(conv.Title).
		OnClick(func() { a.openConversation(ws.ID, conv) })
	row.Children(func() {
		mui.Icon(c, icon).FontSize(fs(12)).TextColor(tk.TextMuted)
		if id := conv.IDLabel(); id != "" {
			mui.Text(c, id).FontSize(fs(13)).Font("monospace").TextColor(tk.TextFaint)
		}
		mui.Text(c, conv.Title).FontSize(fs(15)).SingleLine().Grow(1).TextColor(tk.Text)
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
	// Selecting a workspace is a context, not just a page: 执行 and 消息 filter to
	// it until "全部" is chosen (P1-7).
	a.setWorkspaceFilter(id)
}

// setWorkspaceFilter makes a workspace the panel's context ("" = 全部).
func (a *App) setWorkspaceFilter(id string) {
	a.filterWorkspace = id
	a.ticketSel = -1
}

// workspaceName is the name a workspace id reads as; the id itself is the honest
// fallback when the projection has not arrived.
func (a *App) workspaceName(id string) string {
	if id == "" {
		return ""
	}
	if vm := a.snap.Workspaces(); vm != nil {
		if w := vm.Find(id); w != nil && w.Name != "" {
			return w.Name
		}
	}
	return id
}

// workspaceFilterBar is the visible context control: which workspace the page is
// filtered to, and the one way back to 全部.
func (a *App) workspaceFilterBar(c *mui.Context) {
	if a.filterWorkspace == "" {
		return
	}
	tk := a.tk
	mui.Row(c).Gap(8).AlignItems(mui.Center).Children(func() {
		chip(c, tk, "工作区", tk.Accent, tk.AccentSoft)
		mui.Text(c, a.workspaceName(a.filterWorkspace)).FontSize(fs(12)).TextColor(tk.Text)
		all := mui.Row(c).Height(28).Padding(0, 10).Radius(6).Border(1, tk.Border).
			Background(tk.Surface).AlignItems(mui.Center).Label("全部工作区").
			OnClick(func() { a.setWorkspaceFilter("") })
		all.Children(func() {
			mui.Text(c, "全部").FontSize(fs(12)).TextColor(tk.TextMuted)
		})
		mui.Text(c, "下面的列表只显示这个工作区；点「全部」恢复").
			FontSize(10).TextColor(tk.TextFaint)
	})
}

// openConversation opens the Ticket behind a conversation: it selects the Ticket
// on 执行 and expands its row. Navigation, not an action (design §6.3).
func (a *App) openConversation(wsID string, conv view.ConversationVM) {
	a.workspaceSel = wsID
	a.wsExpanded[wsID] = true
	a.setWorkspaceFilter(wsID)
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
			mui.Text(c, "等待运行时发布工作区投影…").FontSize(fs(15)).TextColor(tk.TextMuted)
			return
		}
		ws := vm.Find(a.workspaceSel)
		if ws == nil {
			ws = vm.SelectedWorkspace()
		}
		if ws == nil {
			card(c, tk, 16, func() {
				mui.Text(c, "还没有工作区").FontSize(fs(17)).Bold().TextColor(tk.Text)
				mui.Text(c, "新建一个工作区之后，Agent 才有地方干活；也可以继续在总览里派活。").
					FontSize(13).TextColor(tk.TextMuted)
				a.newWorkspaceRow(c)
			})
			return
		}
		a.workspaceSel = ws.ID

		a.workspaceFilterBar(c)
		// What the workspace is.
		card(c, tk, 16, func() {
			mui.Row(c).Gap(10).AlignItems(mui.Center).Children(func() {
				mui.Icon(c, icFolder).FontSize(fs(20)).TextColor(tk.Accent)
				mui.Text(c, ws.Name).FontSize(fs(20)).Bold().TextColor(tk.Text)
				textChip(c, tk, ws.Kind)
				mui.Spacer(c)
				if n := len(ws.Conversations); n > 0 {
					chip(c, tk, fmt.Sprintf("%d 个会话", n), tk.TextMuted, tk.SurfaceAlt)
				}
				if !ws.LastAt.IsZero() {
					mui.Text(c, "最近 "+humanfmt.Ago(a.age(ws.LastAt))).FontSize(fs(13)).
						TextColor(tk.TextFaint)
				}
			})
			// P1-6: manage this workspace. Both actions are optional channels;
			// without one the button is disabled and the line says which.
			mui.Row(c).Gap(8).AlignItems(mui.Center).Children(func() {
				_, canRename := a.workspaceRenamer()
				_, canDelete := a.workspaceDeleter()
				rename := mui.Button(c, "重命名").Label("重命名").Height(28).FontSize(fs(12))
				if !canRename {
					rename.Disabled(true)
				} else if rename.Clicked() {
					a.openRename(ws)
				}
				del := mui.Button(c, "删除").Label("删除").Height(28).FontSize(fs(12))
				if !canDelete {
					del.Disabled(true)
				} else if del.Clicked() {
					a.openDelete(ws)
				}
				mui.Spacer(c)
				var missing []string
				if !canRename {
					missing = append(missing, "WorkspaceRenamer")
				}
				if !canDelete {
					missing = append(missing, "WorkspaceDeleter")
				}
				if len(missing) > 0 {
					mui.Text(c, "暂时不可用：运行时还没有接入 "+strings.Join(missing, " / ")).
						FontSize(fs(10)).TextColor(tk.Amber)
				}
			})
			if ws.Purpose != "" {
				mui.Text(c, ws.Purpose).FontSize(fs(15)).TextColor(tk.TextMuted)
			}
			if ws.Root != "" {
				mui.Row(c).Gap(8).AlignItems(mui.Center).Children(func() {
					mui.Text(c, "根目录").FontSize(fs(13)).TextColor(tk.TextFaint)
					mono(c, tk, ws.Root)
				})
			}
		})

		// Its conversations.
		mui.Column(c).Grow(1).MinHeight(0).Background(tk.Surface).Radius(tk.Radius).
			Border(1, tk.Border).Children(func() {
			mui.Row(c).Height(34).Padding(0, 16).Gap(8).AlignItems(mui.Center).
				Background(tk.SurfaceAlt).Children(func() {
				mui.Text(c, "会话").FontSize(fs(14)).Bold().TextColor(tk.Text)
				mui.Spacer(c)
				row := mui.Row(c).Height(24).Padding(0, 10).Radius(6).AlignItems(mui.Center).
					Background(tk.Surface).Border(1, tk.Border).OnClick(func() {
					a.openConversationModal(ws.ID)
				})
				row.Children(func() {
					mui.Text(c, "＋ 新建会话").FontSize(fs(14)).TextColor(tk.Text)
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
							mui.Text(c, g.Label).FontSize(fs(13)).Bold().TextColor(tk.TextMuted)
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
			mui.Text(c, id).FontSize(fs(13)).Font("monospace").TextColor(tk.TextFaint)
		}
		mui.Text(c, conv.Title).FontSize(fs(16)).SingleLine().Grow(1).TextColor(tk.Text)
		textChip(c, tk, conv.CreatedLabel())
		if conv.Source != "" {
			mui.Text(c, conv.Source).FontSize(fs(12)).TextColor(tk.TextFaint)
		}
		if conv.Stage != "" {
			chip(c, tk, conv.Stage, tk.TextMuted, tk.SurfaceAlt)
		}
		if conv.Attention {
			chip(c, tk, "待你处理", tk.Amber, tk.AmberSoft)
		}
		if !conv.LastAt.IsZero() {
			mui.Text(c, humanfmt.Ago(a.age(conv.LastAt))).FontSize(fs(12)).TextColor(tk.TextFaint)
		}
		mui.Icon(c, icChevR).FontSize(fs(13)).TextColor(tk.TextFaint)
	})
}

// ---------------------------------------------------------------------------
// 新建项目 dialog
// ---------------------------------------------------------------------------

// openWorkspaceModal starts the 新建项目 form with the defaults the runtime
// wants: a repo workspace with nothing filled in yet.
func (a *App) openWorkspaceModal() {
	a.wsModal = true
	a.wsDraft = workspaceDraft{}
	a.wsKind = 0
	a.wsErr = ""
}

// workspaceDialog is 新建项目: name, root, purpose and kind. Without a
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
			mui.Text(c, "新建项目").FontSize(fs(19)).Bold().TextColor(tk.Text)
			mui.Text(c, "工作区是一个仓库或数据目录；超级Agent 在里面干活，会话属于它。").
				FontSize(12).TextColor(tk.TextMuted)
			a.formTextField(c, "名称", "如 acme/webapp；面板里显示的名字", &a.wsDraft.name, "workspace-name", true)
			a.workspaceRootField(c)
			a.formTextField(c, "用途", "这个工作区负责什么；Router 用它判断派给谁", &a.wsDraft.purpose, "workspace-purpose", false)
			mui.Row(c).Gap(8).AlignItems(mui.Center).Children(func() {
				mui.Text(c, "类型").FontSize(fs(14)).TextColor(tk.TextMuted)
				mui.Segmented(c.Key("workspace-kind"), &a.wsKind, workspaceKindZH...).
					Label("工作区类型")
			})
			if !wired {
				mui.Text(c, "暂时不可用：运行时还没有接入 WorkspaceCreator（表单可看，创建不可用）").
					FontSize(11).TextColor(tk.Amber)
			}
			if a.wsErr != "" {
				mui.Text(c, a.wsErr).FontSize(fs(13)).TextColor(tk.Red)
			}
			mui.Row(c).Gap(8).AlignItems(mui.Center).Children(func() {
				mui.Spacer(c)
				if mui.Button(c, "取消").Clicked() {
					a.wsModal = false
					a.wsErr = ""
				}
				label := "创建"
				if a.wsCreating {
					label = "创建中…"
				}
				btn := mui.PrimaryButton(c, label).Label(label)
				if !wired || a.wsCreating || strings.TrimSpace(a.wsDraft.name) == "" || strings.TrimSpace(a.wsDraft.root) == "" {
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
			mui.Text(c, label).FontSize(fs(14)).TextColor(tk.TextMuted)
			if help != "" {
				mui.Text(c, "· "+help).FontSize(fs(13)).SingleLine().TextColor(tk.TextFaint)
			}
		})
		in := mui.TextInput(c.Key(key), value).Height(32).FontSize(fs(16)).Label("输入" + label)
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
			mui.Text(c, "根目录").FontSize(fs(14)).TextColor(tk.TextMuted)
			mui.Text(c, "· 仓库或数据的本地路径，Agent 的沙箱根").FontSize(fs(13)).SingleLine().
				TextColor(tk.TextFaint)
		})
		mui.Row(c).Gap(8).AlignItems(mui.Center).Children(func() {
			mui.TextInput(c.Key("workspace-root"), &a.wsDraft.root).Height(32).FontSize(fs(16)).
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

// openRename starts the 重命名 dialog for a workspace, prefilled with its name.
func (a *App) openRename(ws *view.WorkspaceEntryVM) {
	a.renameOpen = true
	a.renameID = ws.ID
	a.renameDraft = ws.Name
	a.renameErr = ""
}

// renameDialog is 重命名: one name, saved through WorkspaceRenamer off the frame.
// Without the channel the dialog says so and its button is disabled.
func (a *App) renameDialog(c *mui.Context) {
	if !a.renameOpen {
		return
	}
	tk := a.tk
	_, wired := a.workspaceRenamer()
	open := true
	mui.Modal(c, &open, func() {
		mui.Column(c).Width(520).Gap(10).Children(func() {
			mui.Text(c, "重命名工作区").FontSize(fs(17)).Bold().TextColor(tk.Text)
			a.formTextField(c, "名称", "面板里显示的名字；ID 与目录不变", &a.renameDraft, "rename-workspace", true)
			if !wired {
				mui.Text(c, "暂时不可用：运行时还没有接入 WorkspaceRenamer").
					FontSize(11).TextColor(tk.Amber)
			}
			if a.renameErr != "" {
				mui.Text(c, a.renameErr).FontSize(fs(11)).TextColor(tk.Red)
			}
			mui.Row(c).Gap(8).AlignItems(mui.Center).Children(func() {
				mui.Spacer(c)
				if mui.Button(c, "取消").Clicked() {
					a.renameOpen = false
					a.renameErr = ""
				}
				label := "保存名称"
				if a.renamePending {
					label = "保存中…"
				}
				btn := mui.PrimaryButton(c, label).Label(label)
				if !wired || a.renamePending || strings.TrimSpace(a.renameDraft) == "" {
					btn.Disabled(true)
				} else if btn.Clicked() {
					a.renamePending = true
					a.renameErr = ""
					startRenameWorkspace(a, a.renameID, strings.TrimSpace(a.renameDraft))
				}
			})
		})
	})
	if !open {
		a.renameOpen = false
	}
}

// openDelete starts the 删除 confirmation.
func (a *App) openDelete(ws *view.WorkspaceEntryVM) {
	a.deleteOpen = true
	a.deleteID = ws.ID
	a.deleteName = ws.Name
	a.deleteErr = ""
}

// deleteDialog confirms 删除. It says plainly what is removed and what is not:
// the panel's registration and its conversations, not the directory on disk.
func (a *App) deleteDialog(c *mui.Context) {
	if !a.deleteOpen {
		return
	}
	tk := a.tk
	_, wired := a.workspaceDeleter()
	open := true
	mui.Modal(c, &open, func() {
		mui.Column(c).Width(520).Gap(10).Children(func() {
			mui.Text(c, "删除工作区“"+a.deleteName+"”？").FontSize(fs(17)).Bold().TextColor(tk.Text)
			mui.Text(c, "会从面板移除这个工作区及其会话记录；磁盘上的目录不会被删除。").
				FontSize(12).TextColor(tk.TextMuted)
			if !wired {
				mui.Text(c, "暂时不可用：运行时还没有接入 WorkspaceDeleter").
					FontSize(11).TextColor(tk.Amber)
			}
			if a.deleteErr != "" {
				mui.Text(c, a.deleteErr).FontSize(fs(11)).TextColor(tk.Red)
			}
			mui.Row(c).Gap(8).AlignItems(mui.Center).Children(func() {
				mui.Spacer(c)
				if mui.Button(c, "取消").Clicked() {
					a.deleteOpen = false
					a.deleteErr = ""
				}
				label := "确认删除"
				if a.deletePending {
					label = "删除中…"
				}
				btn := mui.PrimaryButton(c, label).Label(label)
				if !wired || a.deletePending {
					btn.Disabled(true)
				} else if btn.Clicked() {
					a.deletePending = true
					a.deleteErr = ""
					startDeleteWorkspace(a, a.deleteID)
				}
			})
		})
	})
	if !open {
		a.deleteOpen = false
	}
}

// submitWorkspace sends the form; the dialog stays open with the runtime's error
// when it refuses, so nothing typed is lost.
func (a *App) submitWorkspace(creator WorkspaceCreator) {
	kind, _ := a.workspaceKind()
	a.wsErr = ""
	a.wsCreating = true
	// CreateWorkspace scans the directory and writes the store: off the frame
	// (P0-2), with the answer applied on the main thread.
	startCreateWorkspace(a, strings.TrimSpace(a.wsDraft.name),
		strings.TrimSpace(a.wsDraft.root), strings.TrimSpace(a.wsDraft.purpose), kind)
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
			mui.Text(c, "新建会话").FontSize(fs(19)).Bold().TextColor(tk.Text)
			where := a.convWS
			if ws != nil && ws.Name != "" {
				where = ws.Name
			}
			mui.Row(c).Gap(8).AlignItems(mui.Center).Children(func() {
				mui.Icon(c, icFolder).FontSize(fs(14)).TextColor(tk.TextMuted)
				mui.Text(c, where).FontSize(fs(15)).TextColor(tk.TextMuted)
			})
			mui.Text(c, "说一句话就开一个会话：超级Agent 会把它变成工单，并在同一个工作区里执行。").
				FontSize(12).TextColor(tk.TextMuted)
			a.formTextField(c, "会话内容", "要它做什么；说清楚目标就行", &a.convDraft, "conversation-text", true)
			if !wired {
				mui.Text(c, "暂时不可用：运行时还没有接入 ConversationCreator（表单可看，创建不可用）").
					FontSize(11).TextColor(tk.Amber)
			}
			if a.convErr != "" {
				mui.Text(c, a.convErr).FontSize(fs(13)).TextColor(tk.Red)
			}
			mui.Row(c).Gap(8).AlignItems(mui.Center).Children(func() {
				mui.Spacer(c)
				if mui.Button(c, "取消").Clicked() {
					a.convModal = false
					a.convErr = ""
				}
				label := "创建会话"
				if a.convCreating {
					label = "创建中…"
				}
				btn := mui.PrimaryButton(c, label).Label(label)
				if !wired || a.convCreating || strings.TrimSpace(a.convDraft) == "" {
					btn.Disabled(true)
				} else if btn.Clicked() && creator != nil {
					a.convErr = ""
					a.convCreating = true
					// NewConversation evaluates the message (a model call): off the
					// frame (P0-2).
					startNewConversation(a, a.convWS, strings.TrimSpace(a.convDraft))
				}
			})
		})
	})
	if !open {
		a.convModal = false
	}
}
