package ui

// The native application menu. mygo installs a default menu when an app sets
// none; this one keeps the standard App/Edit/Window items (so Cmd+C/V work in
// text fields) and adds the product's commands. The same gestures stay available
// as window shortcuts (⌘K, ⌘N, ⌘,), so the menu is a discoverability layer.

import (
	"github.com/egoist/mygo"

	"github.com/zeokeel/super-agent/internal/view"
)

// appMenu builds the menu; callbacks run on the main thread.
func (a *App) appMenu() *mygo.Menu {
	showWindow := func(*mygo.MenuItem, *mygo.Window) {
		a.hidden = false
		if a.win != nil {
			a.win.Show()
			a.win.Focus()
		}
	}
	openSettings := func(*mygo.MenuItem, *mygo.Window) {
		a.sec = view.SecSettings
		a.locate = nil
		a.invalidate()
	}
	newConversation := func(*mygo.MenuItem, *mygo.Window) {
		a.newConversationShortcut()
		a.invalidate()
	}
	quit := func(*mygo.MenuItem, *mygo.Window) { a.Quit() }

	return mygo.NewMenu([]*mygo.MenuItem{
		{Label: "ZeoKeel", Submenu: []*mygo.MenuItem{
			{Role: mygo.RoleAbout},
			mygo.Separator(),
			{Role: mygo.RoleHide},
			mygo.Separator(),
			{Label: "退出 ZeoKeel", Role: mygo.RoleQuit, Click: quit},
		}},
		{Label: "文件", Submenu: []*mygo.MenuItem{
			{Label: "新建对话", Accelerator: "CmdOrCtrl+N", Click: newConversation},
			{Label: "设置…", Accelerator: "CmdOrCtrl+,", Click: openSettings},
			mygo.Separator(),
			{Label: "打开主窗口", Click: showWindow},
		}},
		{Label: "编辑", Submenu: []*mygo.MenuItem{
			{Role: mygo.RoleUndo},
			{Role: mygo.RoleRedo},
			mygo.Separator(),
			{Role: mygo.RoleCut},
			{Role: mygo.RoleCopy},
			{Role: mygo.RolePaste},
			{Role: mygo.RolePasteAndMatchStyle},
			mygo.Separator(),
			{Role: mygo.RoleSelectAll},
		}},
		{Label: "窗口", Submenu: []*mygo.MenuItem{
			{Role: mygo.RoleMinimize},
			{Role: mygo.RoleZoom},
		}},
	})
}

// invalidate asks for one frame after a menu command changed app state.
func (a *App) invalidate() {
	if a.win != nil {
		a.win.Invalidate()
	}
}
