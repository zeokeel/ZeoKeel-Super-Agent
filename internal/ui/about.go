package ui

// 设置 › 关于: version, license, and a 检查更新 button behind an optional runtime
// capability. Without the capability the button is disabled and says so; the
// panel never claims to have checked.

import (
	"context"
	"strings"

	mui "github.com/egoist/mygo/ui"
)

// VersionReporter is the runtime's exact build version (About prefers it over the
// panel's own Version variable, which is the app's version).
type VersionReporter interface {
	Version() string
}

// UpdateChecker asks the release feed for the newest version.
type UpdateChecker interface {
	CheckForUpdate(ctx context.Context) (latest string, upToDate bool, url string, err error)
}

var startUpdateCheck = func(a *App) {
	a.runOffMain(func() {
		checker, ok := a.cmd.(UpdateChecker)
		var latest, url string
		var upToDate bool
		var err error
		if !ok || checker == nil {
			err = errNoUpdateChecker{}
		} else {
			latest, upToDate, url, err = checker.CheckForUpdate(context.Background())
		}
		a.postToUI(func() {
			a.aboutChecking = false
			a.aboutChecked = true
			a.aboutLatest = latest
			a.aboutUpToDate = upToDate
			a.aboutURL = url
			a.aboutErr = ""
			if err != nil {
				a.aboutErr = err.Error()
			}
		})
	})
}

type errNoUpdateChecker struct{}

func (errNoUpdateChecker) Error() string {
	return "暂时不可用：运行时还没有接入检查更新"
}

// aboutSection draws 设置 › 关于.
func (a *App) aboutSection(c *mui.Context) {
	tk := a.tk
	_, wired := a.cmd.(UpdateChecker)
	mui.Text(c, "ZeoKeel 超级Agent").FontSize(fs(15)).Bold().TextColor(tk.Text)
	version := Version
	if r, ok := a.cmd.(VersionReporter); ok && r != nil {
		if v := strings.TrimSpace(r.Version()); v != "" {
			version = v
		}
	}
	mui.Row(c).Gap(8).AlignItems(mui.Center).Children(func() {
		mui.Text(c, "版本").FontSize(fs(11)).Width(60).Shrink(0).TextColor(tk.TextFaint)
		mui.Text(c, version).FontSize(fs(12)).Font("monospace").TextColor(tk.Text)
	})
	mui.Row(c).Gap(8).AlignItems(mui.Center).Children(func() {
		mui.Text(c, "许可证").FontSize(fs(11)).Width(60).Shrink(0).TextColor(tk.TextFaint)
		mui.Text(c, "MIT").FontSize(fs(12)).TextColor(tk.Text)
	})
	mui.Row(c).Gap(8).AlignItems(mui.Center).Children(func() {
		label := "检查更新"
		if a.aboutChecking {
			label = "检查中…"
		}
		btn := mui.Button(c, label).Label(label).Height(28).FontSize(fs(12))
		if !wired || a.aboutChecking {
			btn.Disabled(true)
		} else if btn.Clicked() {
			a.aboutChecking = true
			a.aboutChecked = false
			a.aboutErr = ""
			startUpdateCheck(a)
		}
		if !wired {
			mui.Text(c, "暂时不可用：运行时还没有接入检查更新").
				FontSize(fs(10)).TextColor(tk.Amber)
		}
	})
	if !a.aboutChecked {
		return
	}
	switch {
	case a.aboutErr != "":
		mui.Text(c, "检查更新失败："+a.aboutErr).FontSize(fs(11)).TextColor(tk.Red)
	case a.aboutUpToDate:
		ok := mui.Row(c).Gap(6).AlignItems(mui.Center)
		ok.Children(func() {
			dot(c, tk.Green, 6)
			mui.Text(c, "已是最新版本").FontSize(fs(12)).TextColor(tk.Green)
		})
	case a.aboutLatest != "":
		mui.Row(c).Gap(8).AlignItems(mui.Center).Children(func() {
			mui.Text(c, "有新版本："+a.aboutLatest).FontSize(fs(12)).TextColor(tk.Amber)
			if strings.TrimSpace(a.aboutURL) != "" {
				if b := mui.Button(c, "打开发布页").Height(24).FontSize(fs(11)); b.Clicked() {
					a.openExternal(a.aboutURL)
				}
			}
		})
	}
}
