package ui

// 项目面板: one panel per workspace with 概览 / 设置 / 统计. Settings edit the
// browser (enable, Chrome/Edge, headless, CDP port) and the per-workspace skill
// switches; 统计 shows counts, recent activity and the tool categories the
// runtime reports. Every runtime capability is optional and honest when absent.

import (
	"fmt"
	mui "github.com/egoist/mygo/ui"

	"github.com/zeokeel/super-agent/internal/humanfmt"
	"github.com/zeokeel/super-agent/internal/view"
)

// The optional runtime capabilities behind 项目 › 设置 / 统计.
type BrowserDetector interface {
	DetectedBrowsers() []view.BrowserInfo
}

type WorkspaceSettingsProvider interface {
	WorkspaceSettings(workspaceID string) (view.WorkspaceSettings, bool)
}

type WorkspaceSettingsSaver interface {
	SaveWorkspaceSettings(workspaceID string, s view.WorkspaceSettings) error
}

// BrowserController runs the per-workspace browser. Status reports what is true;
// the panel never claims "运行中" without it.
type BrowserController interface {
	StartBrowser(workspaceID string) error
	StopBrowser(workspaceID string) error
	BrowserStatus(workspaceID string) (running bool, detail string)
}

type WorkspaceStatsProvider interface {
	WorkspaceStats(workspaceID string) (view.WorkspaceStats, bool)
}

// SkillLibrary is the global skill library (per-workspace switches configure it).
type SkillLibrary interface {
	Skills() []view.SkillInfo
}

// startWorkspacePanelLoad gathers one workspace's panel data off the frame.
var startWorkspacePanelLoad = func(a *App, workspaceID string) {
	a.runOffMain(func() {
		var detected []view.BrowserInfo
		var skills []view.SkillInfo
		var settings view.WorkspaceSettings
		var hasSettings bool
		var stats view.WorkspaceStats
		var hasStats bool
		var running bool
		var detail string
		detector, detectorOK := a.cmd.(BrowserDetector)
		provider, providerOK := a.cmd.(WorkspaceSettingsProvider)
		statsProvider, statsOK := a.cmd.(WorkspaceStatsProvider)
		library, libraryOK := a.cmd.(SkillLibrary)
		controller, controllerOK := a.cmd.(BrowserController)
		if detectorOK && detector != nil {
			detected = detector.DetectedBrowsers()
		}
		if libraryOK && library != nil {
			skills = library.Skills()
		}
		if providerOK && provider != nil {
			settings, hasSettings = provider.WorkspaceSettings(workspaceID)
		}
		if statsOK && statsProvider != nil {
			stats, hasStats = statsProvider.WorkspaceStats(workspaceID)
		}
		if controllerOK && controller != nil {
			running, detail = controller.BrowserStatus(workspaceID)
		}
		a.postToUI(func() {
			a.wsDetected = detected
			a.wsDetectedWired = detectorOK
			a.wsSkills = skills
			a.wsSkillsWired = libraryOK
			a.wsSettings = settings
			a.wsPort = float64(settings.Port)
			a.wsSettingsReady = hasSettings
			a.wsSettingsWired = providerOK
			a.wsStats = stats
			a.wsStatsReady = hasStats
			a.wsStatsWired = statsOK
			a.wsBrowserRunning = running
			a.wsBrowserDetail = detail
			a.wsBrowserWired = controllerOK
			a.wsPanelLoading = false
		})
	})
}

// startDetectedBrowsersLoad loads the browser list for 设置 › 部署.
var startDetectedBrowsersLoad = func(a *App) {
	a.runOffMain(func() {
		detector, ok := a.cmd.(BrowserDetector)
		var list []view.BrowserInfo
		if ok && detector != nil {
			list = detector.DetectedBrowsers()
		}
		a.postToUI(func() {
			a.detectedBrowsers = list
			a.detectedBrowsersLoaded = true
		})
	})
}

// startWorkspaceSettingsSave saves one workspace's settings off the frame.
var startWorkspaceSettingsSave = func(a *App, workspaceID string, s view.WorkspaceSettings) {
	a.runOffMain(func() {
		saver, ok := a.cmd.(WorkspaceSettingsSaver)
		var err error
		if !ok || saver == nil {
			err = errNoWorkspaceSettingsSaver{}
		} else {
			err = saver.SaveWorkspaceSettings(workspaceID, s)
		}
		a.postToUI(func() {
			a.wsSaving = false
			if err != nil {
				a.wsSaveErr = err.Error()
				return
			}
			a.wsSaveErr = ""
			a.toast("项目设置已保存", toastOK)
		})
	})
}

type errNoWorkspaceSettingsSaver struct{}

func (errNoWorkspaceSettingsSaver) Error() string {
	return "暂时不可用：运行时还没有接入保存项目设置"
}

// startBrowserAction starts or stops the workspace browser off the frame.
var startBrowserAction = func(a *App, workspaceID string, start bool) {
	a.runOffMain(func() {
		controller, ok := a.cmd.(BrowserController)
		var err error
		if !ok || controller == nil {
			err = errNoBrowserController{}
		} else if start {
			err = controller.StartBrowser(workspaceID)
		} else {
			err = controller.StopBrowser(workspaceID)
		}
		var running bool
		var detail string
		if ok && controller != nil {
			running, detail = controller.BrowserStatus(workspaceID)
		}
		a.postToUI(func() {
			a.wsBrowserPending = ""
			if err != nil {
				a.wsBrowserDetail = err.Error()
				return
			}
			a.wsBrowserRunning = running
			a.wsBrowserDetail = detail
			if start {
				a.toast("浏览器已启动", toastOK)
			} else {
				a.toast("浏览器已停止", toastOK)
			}
		})
	})
}

type errNoBrowserController struct{}

func (errNoBrowserController) Error() string {
	return "暂时不可用：运行时还没有接入浏览器控制"
}

// ensureWorkspacePanel loads the panel data when the workspace or tab changed.
func (a *App) ensureWorkspacePanel(wsID string) {
	if a.wsPanelFor == wsID && a.wsPanelTab == a.wsTab {
		return
	}
	a.wsPanelFor = wsID
	a.wsPanelTab = a.wsTab
	if a.wsTab == "settings" || a.wsTab == "stats" {
		a.wsPanelLoading = true
		startWorkspacePanelLoad(a, wsID)
	}
}

// workspaceTabs is the 概览 / 设置 / 统计 switch.
func (a *App) workspaceTabs(c *mui.Context) {
	for _, tab := range []struct{ ID, Label string }{
		{"overview", "概览"}, {"settings", "设置"}, {"stats", "统计"},
	} {
		tab := tab
		on := a.wsTab == tab.ID || (a.wsTab == "" && tab.ID == "overview")
		b := mui.Button(c, tab.Label).Label("项目" + tab.Label).Height(28).FontSize(fs(12))
		if on {
			b.Disabled(true)
		} else if b.Clicked() {
			a.wsTab = tab.ID
		}
	}
	mui.Spacer(c)
}

// workspaceSettingsTab draws 项目 › 设置.
func (a *App) workspaceSettingsTab(c *mui.Context, ws *view.WorkspaceEntryVM) {
	tk := a.tk
	_ = tk
	if a.wsPanelLoading {
		spinner(c, tk, "正在读取项目设置…")
	}
	if !a.wsSettingsWired && !a.wsDetectedWired {
		card(c, tk, 12, func() {
			mui.Text(c, "项目设置").FontSize(fs(15)).Bold().TextColor(tk.Text)
			mui.Text(c, "暂时不可用：运行时还没有接入工作区设置（WorkspaceSettingsProvider / SaveWorkspaceSettings）。").
				FontSize(fs(12)).TextColor(tk.Amber)
		})
	}
	// Browser.
	card(c, tk, 12, func() {
		mui.Row(c).Gap(8).AlignItems(mui.Center).Children(func() {
			mui.Icon(c, icGlobe).FontSize(fs(14)).TextColor(tk.Accent)
			mui.Text(c, "浏览器").FontSize(fs(15)).Bold().TextColor(tk.Text)
			mui.Spacer(c)
			a.browserStatusLine(c, ws)
		})
		if !a.wsDetectedWired {
			mui.Text(c, "暂时不可用：运行时还没有接入浏览器检测").
				FontSize(fs(11)).TextColor(tk.Amber)
		} else if len(a.wsDetected) == 0 {
			mui.Text(c, "没有检测到 Chrome/Edge：安装后再来，或手动指定路径（运行时未提供手动路径时不可用）。").
				FontSize(fs(11)).TextColor(tk.TextMuted)
		} else {
			for _, br := range a.wsDetected {
				br := br
				mui.Row(c).Gap(8).AlignItems(mui.Center).Children(func() {
					mui.Text(c, br.Name).FontSize(fs(11)).Width(64).Shrink(0).TextColor(tk.TextMuted)
					mui.Text(c, br.Path).FontSize(fs(10)).Font("monospace").SingleLine().
						Tooltip(br.Path).Grow(1).TextColor(tk.TextFaint)
					if br.Version != "" {
						mui.Text(c, br.Version).FontSize(fs(10)).TextColor(tk.TextFaint)
					}
					if br.Available {
						textChip(c, tk, "可用")
					} else {
						mui.Text(c, "不可用").FontSize(fs(10)).TextColor(tk.TextFaint)
					}
				})
			}
		}
		// Enable + choice + headless + port.
		if a.wsSettingsWired {
			on := a.wsSettings.BrowserEnabled
			mui.Row(c).Gap(8).AlignItems(mui.Center).Children(func() {
				if mui.Switch(c.Key("ws:browser-enabled"), &on).Label("为这个项目启用浏览器").Changed() {
					a.wsSettings.BrowserEnabled = on
				}
				mui.Text(c, "为这个项目启用浏览器").FontSize(fs(12)).TextColor(tk.Text)
				mui.Text(c, "· 用于预览/浏览器 MCP；关闭则完全不用").
					FontSize(fs(10)).TextColor(tk.TextFaint)
			})
			mui.Row(c).Gap(6).AlignItems(mui.Center).Children(func() {
				mui.Text(c, "浏览器").FontSize(fs(11)).TextColor(tk.TextFaint)
				for _, br := range a.wsDetected {
					br := br
					on := a.wsSettings.Browser == br.ID
					label := br.Name
					if !br.Available {
						label += "（不可用）"
					}
					b := mui.Button(c, label).Label("浏览器 " + br.Name).Height(26).FontSize(fs(11))
					if !br.Available || !a.wsDetectedWired {
						b.Disabled(true)
					} else if on {
						b.Disabled(true)
					} else if b.Clicked() {
						a.wsSettings.Browser = br.ID
					}
				}
				if len(a.wsDetected) == 0 {
					mui.Text(c, "（未检测无可用浏览器）").FontSize(fs(10)).TextColor(tk.TextFaint)
				}
			})
			mui.Row(c).Gap(8).AlignItems(mui.Center).Children(func() {
				headless := a.wsSettings.Headless
				if mui.Switch(c.Key("ws:browser-headless"), &headless).Label("无头模式").Changed() {
					a.wsSettings.Headless = headless
				}
				mui.Text(c, "无头模式（不显示窗口）").FontSize(fs(12)).TextColor(tk.TextMuted)
				mui.Spacer(c)
			})
			mui.Row(c).Gap(8).AlignItems(mui.Center).Children(func() {
				mui.Text(c, "CDP 端口").FontSize(fs(11)).TextColor(tk.TextFaint)
				mui.NumberInput(c.Key("ws:browser-port"), &a.wsPort, 0, 65535, 1).
					Label("CDP 端口").Width(100)
				mui.Text(c, "0 = 自动").FontSize(fs(10)).TextColor(tk.TextFaint)
			})
			profile := a.wsSettings.ProfileDir
			if profile != "" {
				mui.Row(c).Gap(8).AlignItems(mui.Center).Children(func() {
					mui.Text(c, "Profile").FontSize(fs(11)).TextColor(tk.TextFaint)
					mui.Text(c, profile).FontSize(fs(10)).Font("monospace").SingleLine().
						Tooltip(profile).Grow(1).TextColor(tk.TextMuted)
				})
			}
			port := "自动"
			if a.wsSettings.Port > 0 {
				port = fmt.Sprint(a.wsSettings.Port)
			}
			mui.Text(c, "CDP 端口："+port).FontSize(fs(10)).TextColor(tk.TextFaint)
		}
		// Start/stop, only with the controller.
		mui.Row(c).Gap(8).AlignItems(mui.Center).Children(func() {
			_, wired := a.cmd.(BrowserController)
			if a.wsBrowserPending != "" {
				spinner(c, tk, a.wsBrowserPending)
			}
			start := mui.Button(c, "启动浏览器").Label("启动浏览器").Height(28).FontSize(fs(12))
			stop := mui.Button(c, "停止浏览器").Label("停止浏览器").Height(28).FontSize(fs(12))
			if !wired || a.wsBrowserPending != "" || a.wsBrowserRunning {
				start.Disabled(true)
			} else if start.Clicked() {
				a.wsBrowserPending = "浏览器启动中…"
				startBrowserAction(a, ws.ID, true)
			}
			if !wired || a.wsBrowserPending != "" || !a.wsBrowserRunning {
				stop.Disabled(true)
			} else if stop.Clicked() {
				a.wsBrowserPending = "浏览器停止中…"
				startBrowserAction(a, ws.ID, false)
			}
			if !wired {
				mui.Text(c, "暂时不可用：运行时还没有接入浏览器控制").
					FontSize(fs(10)).TextColor(tk.Amber)
			}
		})
	})

	// Skills.
	card(c, tk, 12, func() {
		mui.Row(c).Gap(8).AlignItems(mui.Center).Children(func() {
			mui.Icon(c, icSpark).FontSize(fs(13)).TextColor(tk.Accent)
			mui.Text(c, "技能").FontSize(fs(15)).Bold().TextColor(tk.Text)
			mui.Spacer(c)
			mui.Text(c, "全局默认开启；这个项目可以单独关掉").FontSize(fs(10)).TextColor(tk.TextFaint)
		})
		if !a.wsSkillsWired {
			mui.Text(c, "暂时不可用：运行时还没有提供技能库（SkillLibrary）").
				FontSize(fs(11)).TextColor(tk.Amber)
			return
		}
		if len(a.wsSkills) == 0 {
			mui.Text(c, "技能库是空的").FontSize(fs(12)).TextColor(tk.TextMuted)
			return
		}
		for _, sk := range a.wsSkills {
			sk := sk
			enabled := a.wsSettings.SkillEnabled(sk.ID, true)
			mui.Row(c).Gap(8).AlignItems(mui.Center).Children(func() {
				sw := mui.Switch(c.Key("ws:skill:"+sk.ID), &enabled).
					Label("技能 " + sk.Name).Changed()
				if sw {
					a.wsSettings.SetSkillEnabled(sk.ID, enabled, sk.EnabledByDefault)
				}
				mui.Column(c).Grow(1).Gap(1).Children(func() {
					mui.Text(c, sk.Name).FontSize(fs(13)).TextColor(tk.Text)
					if sk.Description != "" {
						mui.Text(c, sk.Description).FontSize(fs(10)).SingleLine().
							TextColor(tk.TextFaint)
					}
				})
				if sk.EnabledByDefault {
					textChip(c, tk, "默认开")
				}
			})
		}
	})

	// Save.
	mui.Row(c).Gap(8).AlignItems(mui.Center).Children(func() {
		_, wired := a.cmd.(WorkspaceSettingsSaver)
		label := "保存项目设置"
		if a.wsSaving {
			label = "保存中…"
		}
		b := mui.PrimaryButton(c, label).Label(label).Height(30).FontSize(fs(12))
		if !wired || a.wsSaving || !a.wsSettingsWired {
			b.Disabled(true)
		} else if b.Clicked() {
			a.wsSaving = true
			a.wsSaveErr = ""
			a.wsSettings.Port = int(a.wsPort)
			startWorkspaceSettingsSave(a, ws.ID, a.wsSettings)
		}
		if !wired {
			mui.Text(c, "暂时不可用：运行时还没有接入保存项目设置").
				FontSize(fs(10)).TextColor(tk.Amber)
		}
		if a.wsSaveErr != "" {
			mui.Text(c, "保存失败："+a.wsSaveErr).FontSize(fs(11)).TextColor(tk.Red)
		}
	})
}

// browserStatusLine reports the browser state: running only when the controller
// says so.
func (a *App) browserStatusLine(c *mui.Context, ws *view.WorkspaceEntryVM) {
	tk := a.tk
	if !a.wsSettingsWired && !a.wsBrowserWired {
		return
	}
	if !a.wsBrowserWired {
		mui.Text(c, "状态未知（运行时没有浏览器控制）").FontSize(fs(10)).TextColor(tk.TextFaint)
		return
	}
	fg := tk.TextMuted
	word := "已停止"
	if a.wsBrowserRunning {
		fg, word = tk.Green, "运行中"
	}
	dot(c, fg, 6)
	mui.Text(c, word).FontSize(fs(11)).TextColor(fg)
	if a.wsBrowserDetail != "" {
		mui.Text(c, a.wsBrowserDetail).FontSize(fs(10)).SingleLine().TextColor(tk.TextFaint)
	}
	_ = ws
}

// workspaceStatsTab draws 项目 › 统计.
func (a *App) workspaceStatsTab(c *mui.Context, ws *view.WorkspaceEntryVM) {
	tk := a.tk
	if a.wsPanelLoading {
		spinner(c, tk, "正在读取项目统计…")
	}
	stats := a.wsStats
	if !a.wsStatsWired {
		// Honest fallback: the conversation list is published, task counters are not.
		stats.Conversations = len(ws.Conversations)
		mui.Text(c, "运行时还没有提供任务级统计；下面只按已发布的会话计数。").
			FontSize(fs(11)).TextColor(tk.Amber)
	}
	card(c, tk, 12, func() {
		mui.Row(c).Gap(8).AlignItems(mui.Center).Children(func() {
			mui.Icon(c, icEval).FontSize(fs(14)).TextColor(tk.Accent)
			mui.Text(c, "统计").FontSize(fs(15)).Bold().TextColor(tk.Text)
			mui.Spacer(c)
			if !a.wsStats.UpdatedAt.IsZero() {
				mui.Text(c, "更新于 "+humanfmt.Ago(a.age(a.wsStats.UpdatedAt))).
					FontSize(fs(10)).TextColor(tk.TextFaint)
			}
		})
		items := []struct {
			Label string
			Value int
		}{
			{"会话", stats.Conversations}, {"任务", stats.Tasks},
			{"进行中", stats.Running}, {"等待你", stats.Waiting},
			{"已完成", stats.Completed}, {"失败", stats.Failed},
		}
		mui.Row(c).Gap(18).Wrap().Children(func() {
			for _, it := range items {
				mui.Column(c).Gap(0).Children(func() {
					mui.Text(c, fmt.Sprint(it.Value)).FontSize(fs(20)).Bold().TextColor(tk.Text)
					mui.Text(c, it.Label).FontSize(fs(10)).TextColor(tk.TextFaint)
				})
			}
		})
	})

	card(c, tk, 12, func() {
		mui.Text(c, "最近活动").FontSize(fs(14)).Bold().TextColor(tk.Text)
		if len(stats.Recent) == 0 {
			mui.Text(c, "还没有活动记录").FontSize(fs(12)).TextColor(tk.TextMuted)
			return
		}
		for _, ev := range stats.Recent {
			ev := ev
			mui.Row(c).Gap(8).AlignItems(mui.Center).Children(func() {
				dot(c, tk.TextFaint, 5)
				mui.Text(c, ev.Text).FontSize(fs(12)).Grow(1).TextColor(tk.Text)
				if !ev.At.IsZero() {
					mui.Text(c, humanfmt.Ago(a.age(ev.At))).FontSize(fs(10)).TextColor(tk.TextFaint)
				}
			})
		}
	})

	card(c, tk, 12, func() {
		mui.Text(c, "Agent 可用的工具").FontSize(fs(14)).Bold().TextColor(tk.Text)
		if len(stats.Tools) == 0 {
			mui.Text(c, "运行时还没有报告工具清单（不在这里猜能力）").
				FontSize(fs(11)).TextColor(tk.TextMuted)
			return
		}
		for _, tool := range stats.Tools {
			tool := tool
			mui.Row(c).Gap(8).AlignItems(mui.Center).Children(func() {
				mui.Text(c, tool.Label).FontSize(fs(12)).Width(120).Shrink(0).TextColor(tk.Text)
				if tool.Available {
					textChip(c, tk, "可用")
				} else {
					mui.Text(c, "未启用").FontSize(fs(10)).TextColor(tk.TextFaint)
				}
				mui.Text(c, tool.Description).FontSize(fs(10)).Grow(1).SingleLine().
					TextColor(tk.TextFaint)
			})
		}
	})
}

// browserFactsSection is the 设置 › 部署 browser detection block.
func (a *App) browserFactsSection(c *mui.Context) {
	tk := a.tk
	if !a.detectedBrowsersLoaded {
		a.detectedBrowsersLoaded = true
		startDetectedBrowsersLoad(a)
	}
	mui.Text(c, "浏览器（默认路径检测）").FontSize(fs(12)).Bold().TextColor(tk.Text)
	if !a.detectedBrowsersLoaded {
		spinner(c, tk, "正在检测…")
		return
	}
	if len(a.detectedBrowsers) == 0 {
		mui.Text(c, "没有检测到 Chrome/Edge（装好后重启面板；每个项目的启用与端口在 项目 › 设置）。").
			FontSize(fs(11)).TextColor(tk.TextMuted)
		return
	}
	for _, br := range a.detectedBrowsers {
		br := br
		mui.Row(c).Gap(8).AlignItems(mui.Center).Children(func() {
			mui.Text(c, br.Name).FontSize(fs(12)).Width(80).Shrink(0).TextColor(tk.Text)
			mui.Text(c, br.Path).FontSize(fs(10)).Font("monospace").SingleLine().
				Tooltip(br.Path).Grow(1).TextColor(tk.TextFaint)
			if br.Version != "" {
				mui.Text(c, br.Version).FontSize(fs(10)).TextColor(tk.TextFaint)
			}
		})
	}
}
