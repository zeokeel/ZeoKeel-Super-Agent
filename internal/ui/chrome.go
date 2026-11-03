package ui

import (
	"fmt"
	"runtime"
	"sync"

	mui "github.com/egoist/mygo/ui"

	"github.com/zeokeel/super-agent/internal/view"
)

// topbar is the only chrome: identity, runtime state, and the way into 巡检.
func (a *App) topbar(c *mui.Context) {
	tk := a.tk
	ov := a.snap.Overview()

	mui.Row(c).Height(48).Padding(0, 16).Gap(12).AlignItems(mui.Center).
		Background(tk.Surface).Children(func() {
		uiLogo(c, 22)
		mui.Text(c, "ZeoKeel").FontSize(16).Bold().TextColor(tk.Text)

		mui.Spacer(c)

		// The palette is one shortcut away; the chip makes it discoverable without
		// taking a toolbar row (design §6.4: CommandPalette).
		search := mui.Row(c).Height(26).Padding(0, 10).Radius(8).Gap(7).
			Background(tk.SurfaceAlt).Border(1, tk.BorderSoft).AlignItems(mui.Center).
			Children(func() {
				mui.Icon(c, icSearch).FontSize(12).TextColor(tk.TextMuted)
				mui.Text(c, "搜索").FontSize(13).TextColor(tk.TextMuted)
				mui.Text(c, searchKeyHint()).FontSize(12).TextColor(tk.TextFaint)
			})
		if search.Clicked() {
			a.openSearch()
		}

		// Runtime state lives in the bar, not in a card: a healthy system takes no
		// room on screen (000 Philosophy, 005 Panel).
		running, maxJobs, queued := 0, 0, 0
		if ov != nil {
			running, maxJobs, queued = ov.Runtime.Running, ov.Runtime.MaxJobs, ov.Runtime.Queued
		}
		dot(c, ifColor(running > 0, tk.Accent, tk.Green), 7)
		mui.Text(c, fmt.Sprintf("%d/%d 执行中 · 队列 %d", running, maxJobs, queued)).
			FontSize(11).TextColor(tk.TextMuted)

		if n := ov.PersonCount(); n > 0 {
			chip(c, tk, fmt.Sprintf("%d 待处理", n), tk.Amber, tk.AmberSoft)
		}
	})
}

// sidebar is the panel's left column, DSH-like: 超级Agent above, then the
// workspaces with their conversations, then the monitoring pages and 设置 at the
// bottom, and the machine's OS/timezone in the footer.
//
// 工作区 is a tree, not a list: a workspace row expands to its conversations, and
// a conversation opens its Ticket on 执行. The monitoring pages stay where they
// were so nothing a person learned moves.
func (a *App) sidebar(c *mui.Context) {
	tk := a.tk
	items := view.Nav(a.snap.Overview())

	mui.Column(c).Width(232).AlignItems(mui.Stretch).Background(tk.Surface).Children(func() {
		// 超级Agent: the one entry above the workspaces. It is the overview page,
		// the place a person reads when no workspace is on screen.
		mui.Column(c).Padding(8, 8, 6, 8).Children(func() {
			a.railItem(c, items[0], 16, 17, true)
		})
		mui.Box(c).Height(1).Background(tk.BorderSoft)

		// 工作区: the group grows so a long list scrolls, and the monitoring
		// group stays at the bottom where a person expects it.
		mui.Scroll(c).Grow(1).Children(func() { a.workspaceSection(c) })
		mui.Box(c).Height(1).Background(tk.BorderSoft)

		// 监控: the inspection pages, unchanged in order and meaning.
		mui.Column(c).Padding(6, 8).Gap(2).Children(func() {
			mui.Row(c).Padding(2, 8, 2, 8).Children(func() {
				mui.Text(c, "监控").FontSize(13).Bold().TextColor(tk.TextFaint)
			})
			for _, it := range items[1 : len(items)-1] {
				a.railItem(c, it, 14, 15, false)
			}
		})
		// 设置: configuration, one place (design §6.3).
		mui.Column(c).Padding(2, 8, 6, 8).Children(func() {
			a.railItem(c, items[len(items)-1], 14, 15, false)
		})
		mui.Box(c).Height(1).Background(tk.BorderSoft)

		// The machine this window runs on, as the runtime reports it. It is the
		// smallest line in the panel on purpose: the whole "OS/arch · timezone
		// (UTC±h)" fits the rail's width, and wraps instead of being cut when a
		// timezone name is long.
		mui.Column(c).Padding(4, 10).Children(func() {
			mui.Text(c, a.systemLabel()).FontSize(10).TextColor(tk.TextFaint)
		})
	})
}

// railItem is one navigation row of the sidebar.
func (a *App) railItem(c *mui.Context, it view.NavItem, iconSize, textSize float32, big bool) {
	tk := a.tk
	on := it.Section == a.sec
	row := mui.Row(c).Height(36).Radius(8).Padding(0, 10).Gap(9).AlignItems(mui.Center)
	if on {
		row.Background(tk.AccentSoft)
	}
	clicked := row.Clicked()
	row.Children(func() {
		mui.Icon(c, iconFor(it.Section)).FontSize(iconSize).
			TextColor(ifColor(on, tk.Accent, tk.TextMuted))
		txt := mui.Text(c, it.Label).FontSize(textSize).TextColor(ifColor(on, tk.Accent, tk.Text))
		if big {
			txt.Bold()
		}
		mui.Spacer(c)
		if it.Count > 0 {
			chip(c, tk, fmt.Sprint(it.Count), tk.Amber, tk.AmberSoft)
		}
	})
	if clicked {
		a.sec = it.Section
		a.locate = nil
		a.goalFocus = ""
	}
}

// statusbar is the one line that must never lie.
func (a *App) statusbar(c *mui.Context) {
	tk := a.tk
	ov := a.snap.Overview()
	db, rev := "—", a.snap.Revision()
	if ov != nil {
		db = ov.Storage.DB
	}
	mui.Row(c).Height(24).Padding(0, 14).AlignItems(mui.Center).
		Background(tk.Surface).Children(func() {
		left := "一条连续的上下文 · 满了自动压缩"
		if a.draining {
			// Say what is happening: a window that seems to ignore 退出 is worse
			// than one that explains why it is waiting.
			left = "正在等执行器收尾，完成后自动退出…"
		}
		mui.Text(c, left).FontSize(12).TextColor(tk.TextFaint)
		mui.Spacer(c)
		// The glossary's discoverable entry: the shortcut opens it, and this says
		// so in the platform's own words.
		mui.Text(c, "帮助 "+helpKeyHint()).FontSize(12).SingleLine().TextColor(tk.TextMuted).
			OnClick(func() { a.openHelp() })
		mui.Text(c, "·").FontSize(12).TextColor(tk.TextFaint)
		mui.Text(c, fmt.Sprintf("v%s · rev %d · Go 1.27.3 · CGO=0 · SQLite %s", Version, rev, db)).
			FontSize(10).SingleLine().TextColor(tk.TextFaint)
	})
}

// uiLogo draws the embedded mark. The bitmap is decoded once: a view must not
// decode a PNG on every frame.
var (
	logoOnce sync.Once
	logoBmp  *mui.Bitmap
)

func uiLogo(c *mui.Context, size float32) {
	logoOnce.Do(func() {
		if b, err := mui.DecodeBitmap(logoPNG); err == nil {
			logoBmp = b
		}
	})
	if logoBmp != nil {
		mui.Image(c, logoBmp).Size(size, size)
		return
	}
	mui.Box(c).Size(size, size).Radius(size / 3).Background(mui.Hex("#2F5BFF"))
}

func iconFor(s view.Section) *mui.SVG {
	switch s {
	case view.SecOverview:
		return icOverview
	case view.SecWorkspace:
		return icFolder
	case view.SecMessages:
		return icTrigger
	case view.SecSource:
		return icSource
	case view.SecTrigger:
		return icTrigger
	case view.SecEval:
		return icEval
	case view.SecExecution:
		return icRun
	default:
		return icSettings
	}
}

// searchKeyHint names the platform's palette shortcut: Command on macOS, Control
// everywhere else, as mui.Cmd does.
func searchKeyHint() string {
	if runtime.GOOS == "darwin" {
		return "⌘K"
	}
	return "Ctrl+K"
}

// helpKeyHint names the glossary shortcut the same way.
func helpKeyHint() string {
	if runtime.GOOS == "darwin" {
		return "⌘/"
	}
	return "Ctrl+/"
}

func ifColor(cond bool, yes, no mui.Color) mui.Color {
	if cond {
		return yes
	}
	return no
}
