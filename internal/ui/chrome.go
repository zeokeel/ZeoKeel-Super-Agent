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
		mui.Text(c, "ZeoKeel").FontSize(14).Bold().TextColor(tk.Text)

		mui.Spacer(c)

		// The palette is one shortcut away; the chip makes it discoverable without
		// taking a toolbar row (design §6.4: CommandPalette).
		search := mui.Row(c).Height(26).Padding(0, 10).Radius(8).Gap(7).
			Background(tk.SurfaceAlt).Border(1, tk.BorderSoft).AlignItems(mui.Center).
			Children(func() {
				mui.Icon(c, icSearch).FontSize(12).TextColor(tk.TextMuted)
				mui.Text(c, "搜索").FontSize(11).TextColor(tk.TextMuted)
				mui.Text(c, searchKeyHint()).FontSize(10).TextColor(tk.TextFaint)
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

// sidebar is the monitoring navigation, in Chinese, in the order work flows.
func (a *App) sidebar(c *mui.Context) {
	tk := a.tk
	items := view.Nav(a.snap.Overview())

	mui.Column(c).Width(196).AlignItems(mui.Stretch).Background(tk.Surface).Children(func() {
		mui.Column(c).Gap(2).Padding(10, 10).Children(func() {
			for _, it := range items {
				on := it.Section == a.sec
				row := mui.Row(c).Height(34).Radius(8).Padding(0, 10).Gap(9).AlignItems(mui.Center)
				if on {
					row.Background(tk.AccentSoft)
				}
				if row.Clicked() {
					a.sec = it.Section
					a.locate = nil
					a.goalFocus = ""
				}
				row.Children(func() {
					mui.Icon(c, iconFor(it.Section)).FontSize(16).
						TextColor(ifColor(on, tk.Accent, tk.TextMuted))
					mui.Text(c, it.Label).FontSize(13).TextColor(ifColor(on, tk.Accent, tk.Text))
					mui.Spacer(c)
					if it.Count > 0 {
						chip(c, tk, fmt.Sprint(it.Count), tk.Amber, tk.AmberSoft)
					}
				})
			}
		})
		mui.Spacer(c)
	})
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
		mui.Text(c, left).FontSize(10).TextColor(tk.TextFaint)
		mui.Spacer(c)
		// The glossary's discoverable entry: the shortcut opens it, and this says
		// so in the platform's own words.
		mui.Text(c, "帮助 "+helpKeyHint()).FontSize(10).SingleLine().TextColor(tk.TextMuted).
			OnClick(func() { a.openHelp() })
		mui.Text(c, "·").FontSize(10).TextColor(tk.TextFaint)
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
