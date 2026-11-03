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
		mui.Text(c, "ZeoKeel").FontSize(fs(16)).Bold().TextColor(tk.Text)

		mui.Spacer(c)

		// The palette is one shortcut away; the chip makes it discoverable without
		// taking a toolbar row (design §6.4: CommandPalette).
		search := mui.Row(c).Height(26).Padding(0, 10).Radius(8).Gap(7).
			Background(tk.SurfaceAlt).Border(1, tk.BorderSoft).AlignItems(mui.Center).
			Children(func() {
				mui.Icon(c, icSearch).FontSize(fs(12)).TextColor(tk.TextMuted)
				mui.Text(c, "搜索").FontSize(fs(13)).TextColor(tk.TextMuted)
				mui.Text(c, searchKeyHint()).FontSize(fs(12)).TextColor(tk.TextFaint)
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

		if n := a.attentionCount(); n > 0 {
			inbox := mui.Row(c).Height(28).Padding(0, 10).Radius(6).Background(tk.AmberSoft).
				Focusable().Role(mui.RoleButton).Label(view.AttentionLabel(n)).
				Tooltip("打开收件箱：待你决定的请求").
				OnClick(func() { a.sec = view.SecInbox; a.locate = nil })
			inbox.Children(func() {
				mui.Text(c, view.AttentionLabel(n)).FontSize(fs(12)).TextColor(tk.Amber)
			})
		}
	})
}

// sidebar is the panel's left column. The daily rail is five places: 超级Agent,
// 收件箱, 项目 (the workspace tree), 执行 and 设置. The engineering pages stay
// reachable in a collapsed 诊断 group below, so the daily path stays short
// (interaction audit §III).
func (a *App) sidebar(c *mui.Context) {
	tk := a.tk
	items := view.Nav(a.snap.Overview())

	mui.Column(c).Width(232).AlignItems(mui.Stretch).Background(tk.Surface).Children(func() {
		// 超级Agent + 收件箱: the two places a conversation lives.
		mui.Column(c).Padding(8, 8, 6, 8).Gap(2).Children(func() {
			a.railItem(c, items[0], 16, 17, true)
			a.railItem(c, items[1], 14, 15, false)
		})
		mui.Box(c).Height(1).Background(tk.BorderSoft)

		// 项目: the workspace tree grows, so a long list scrolls.
		mui.Scroll(c).Grow(1).Children(func() { a.workspaceSection(c) })
		mui.Box(c).Height(1).Background(tk.BorderSoft)

		// 执行 and 设置, then 诊断 (collapsed by default).
		mui.Column(c).Padding(6, 8, 2, 8).Gap(2).Children(func() {
			a.railItem(c, items[3], 14, 15, false)
			a.railItem(c, items[4], 14, 15, false)
		})
		mui.Column(c).Padding(2, 8, 6, 8).Gap(2).Children(func() {
			header := mui.Row(c).Height(30).Radius(7).Padding(0, 8).Gap(6).
				AlignItems(mui.Center).Label("诊断（高级）").Focusable().Role(mui.RoleButton).
				Tooltip("触发、评估、消息流、连接等技术页").OnClick(func() { a.advancedOpen = !a.advancedOpen })
			header.Children(func() {
				mui.Icon(c, ifIcon(a.advancedOpen, icChevD, icChevR)).FontSize(fs(10)).
					TextColor(tk.TextFaint)
				mui.Text(c, "诊断").FontSize(fs(12)).Bold().TextColor(tk.TextFaint)
			})
			if a.advancedOpen {
				for _, it := range view.NavAdvanced() {
					a.railItem(c, it, 13, 13, false)
				}
			}
		})
		mui.Box(c).Height(1).Background(tk.BorderSoft)

		// The machine this window runs on. The smallest line in the panel on
		// purpose; it wraps instead of being cut when a timezone name is long.
		mui.Column(c).Padding(4, 10).Children(func() {
			mui.Text(c, a.systemLabel()).FontSize(fs(10)).TextColor(tk.TextFaint)
		})
	})
}

// railItem is one navigation row of the sidebar.
func (a *App) railItem(c *mui.Context, it view.NavItem, iconSize, textSize float32, big bool) {
	tk := a.tk
	on := it.Section == a.sec
	row := mui.Row(c).Height(36).Radius(8).Padding(0, 10).Gap(9).AlignItems(mui.Center).
		Focusable().Role(mui.RoleButton).Label(it.Label).Tooltip(it.Label)
	if on {
		row.Background(tk.AccentSoft)
	} else if row.Hovered() {
		row.Background(tk.SurfaceAlt)
	}
	clicked := row.Clicked()
	row.Children(func() {
		mui.Icon(c, iconFor(it.Section)).FontSize(fs(iconSize)).
			TextColor(ifColor(on, tk.Accent, tk.TextMuted))
		txt := mui.Text(c, it.Label).FontSize(fs(textSize)).TextColor(ifColor(on, tk.Accent, tk.Text))
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

// statusbar is the one line that must never lie: what needs you, and how to get
// help. Build/version/Go/CGO facts belong to 关于/诊断, not to the main chrome.
func (a *App) statusbar(c *mui.Context) {
	tk := a.tk
	mui.Row(c).Height(26).Padding(0, 14).AlignItems(mui.Center).
		Background(tk.Surface).Children(func() {
		left := "一条连续的上下文 · 满了自动压缩"
		if a.draining {
			left = "正在等执行器收尾，完成后自动退出…"
		}
		mui.Text(c, left).FontSize(fs(12)).TextColor(tk.TextMuted)
		mui.Spacer(c)
		if n := a.attentionCount(); n > 0 {
			mui.Text(c, view.AttentionLabel(n)).FontSize(fs(12)).TextColor(tk.Amber)
			mui.Text(c, "·").FontSize(fs(12)).TextColor(tk.TextFaint)
		}
		help := mui.Text(c, "帮助 "+helpKeyHint()).FontSize(fs(12)).TextColor(tk.TextMuted).
			OnClick(func() { a.openHelp() })
		_ = help
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

// runtimeGOOS names the platform for key hints (one place, like searchKeyHint).
var runtimeGOOS = runtime.GOOS

func ifColor(cond bool, yes, no mui.Color) mui.Color {
	if cond {
		return yes
	}
	return no
}
