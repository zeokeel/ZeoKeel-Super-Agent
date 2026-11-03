package ui

import (
	"github.com/egoist/mygo"
)

// newTray puts the app in the menu bar or notification area, so closing the
// window leaves the runtime running. It returns nil when the platform has no
// tray, and the app then relies on the window alone.
//
// Every item here does something. A "暂停运行时" item that did nothing was
// removed (audit F-05): the runtime has no pause yet, and a menu item that
// ignores a click is worse than one that is not there. When the runtime grows
// one, add it here wired to it.
func (a *App) newTray() *mygo.Tray {
	tray, err := mygo.NewTray(mygo.TrayOptions{
		ID:      "zeokeel",
		Icon:    logoPNG,
		Title:   "ZeoKeel",
		ToolTip: "ZeoKeel Super Agent",
		Menu: mygo.NewMenu([]*mygo.MenuItem{
			{Label: "打开面板", Click: func(*mygo.MenuItem, *mygo.Window) {
				if a.win != nil {
					a.win.Show()
					a.win.Focus()
				}
			}},
			mygo.Separator(),
			{Label: "退出 ZeoKeel", Click: func(*mygo.MenuItem, *mygo.Window) { a.Quit() }},
		}),
	})
	if err != nil {
		return nil
	}
	return tray
}
