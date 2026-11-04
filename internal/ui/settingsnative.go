package ui

// The native settings page: DSH-style groups with mygo's own controls (Input,
// TextArea, Switch, Select, Button, Badge, Modal) and one save bar per group.
// There is no custom card/rail/button class here: rows and borders are layout,
// controls are the framework's.

import (
	"fmt"
	"strconv"
	"strings"

	mui "github.com/egoist/mygo/ui"

	"github.com/zeokeel/super-agent/internal/view"
)

// settingsButton is the one button system: primary (black), outline and ghost
// share height, radius and typography.
func (a *App) settingsButton(c *mui.Context, label string, primary bool) mui.Element {
	if primary {
		return a.primaryBtn(c, label)
	}
	return a.outlineBtn(c, label)
}

// settingsNativePage is the DSH settings body: a 180px vertical group nav, a
// 960px content column with 24px section gaps, inputs at DSH geometry, and one
// sticky save bar (border-top only, hint left, primary right).
func (a *App) settingsNativePage(c *mui.Context) {
	tk := a.tk
	vm := a.snap.Settings()
	if vm == nil {
		mui.Column(c).Fill().Padding(24, 0).Children(func() {
			mui.Text(c, "等待运行时发布设置快照…").FontSize(fs(14)).TextColor(tk.TextMuted)
		})
		return
	}
	a.reconcileSettings(vm)
	groups := settingsGroups(vm)
	mui.Row(c).Width(contentW).Grow(1).AlignItems(mui.Stretch).Children(func() {
		// DSH .dt-nav-item: 32px high, padding 6/10, radius 8, 13/20, left aligned;
		// selected = interactive hover + weight 500.
		mui.Column(c).Width(180).Gap(2).Children(func() {
			for _, g := range groups {
				g := g
				a.settingsNavItem(c, g.Label, g.ID == a.settingsGroup, func() {
					a.settingsGroup = g.ID
					a.settingsErr = ""
				})
			}
			a.settingsNavItem(c, "项目", a.settingsGroup == "project", func() {
				a.settingsGroup = "project"
				a.settingsErr = ""
			})
		})
		mui.Box(c).Width(0.5).Background(tk.Border)
		// Right: content + one sticky save bar.
		mui.Column(c).Width(contentW-204).MinWidth(0).Padding(0, 0, 0, 24).Gap(24).Children(func() {
			if a.settingsGroup == "project" {
				mui.Text(c, "项目").FontSize(fs(13)).Bold().TextColor(tk.TextMuted)
				mui.Scroll(c).Grow(1).Children(func() { a.settingsProjectsNative(c) })
				return
			}
			var g *view.SettingsGroup
			for i := range groups {
				if groups[i].ID == a.settingsGroup {
					g = &groups[i]
				}
			}
			if g == nil {
				mui.Text(c, "这一组还没有投影。").FontSize(fs(13)).TextColor(tk.TextMuted)
				return
			}
			mui.Row(c).Gap(8).AlignItems(mui.Center).Children(func() {
				mui.Text(c, g.Label).FontSize(fs(13)).Bold().TextColor(tk.TextMuted)
				mui.Spacer(c)
				if g.Note != "" {
					mui.Text(c, g.Note).FontSize(fs(13)).TextAlign(mui.End).TextColor(tk.TextFaint)
				}
			})
			dirty := a.settingsDirty(g)
			mui.Scroll(c).Grow(1).Children(func() {
				mui.Column(c).Gap(24).Children(func() { a.settingsGroupBody(c, vm, g) })
			})
			if dirty > 0 {
				a.settingsNativeSaveBar(c, vm, g, dirty)
			}
			if g.ID == view.SettingsPrompt {
				if pvm := a.snap.Prompts(); pvm != nil {
					pdirty := a.promptDirty(pvm)
					if pdirty > 0 || a.promptConflict || a.promptErr != "" || a.promptReloaded != 0 {
						a.promptSaveBar(c, pvm, pdirty)
					}
				}
			}
		})
	})
}

// settingsGroupBody is the group's content with native controls.
func (a *App) settingsGroupBody(c *mui.Context, vm *view.SettingsVM, g *view.SettingsGroup) {
	if g.ID == view.SettingsPrompt {
		a.promptGroup(c, g)
		a.pendingBlock(c, *g)
		return
	}
	if g.ID == view.SettingsDeploy {
		a.deploymentSection(c, *g)
		a.browserFactsSection(c)
		a.browserPrereqCard(c, false)
		// The model endpoint/key live here as well: one native form, one save bar.
		if mg := vm.Group(view.SettingsWorker); mg != nil {
			mui.Text(c, "模型").FontSize(fs(14)).Bold().TextColor(a.tk.Text)
			for _, f := range mg.Fields {
				a.settingField(c, mg, f)
			}
		}
		return
	}
	if g.ID == view.SettingsAbout {
		a.aboutSection(c)
		return
	}
	if g.ID == view.SettingsSource {
		a.channelCards(c, g)
		if a.channelErr != "" {
			mui.Text(c, a.channelErr).FontSize(fs(12)).TextColor(a.tk.Red)
		}
	}
	if g.ID == view.SettingsExecution {
		a.pauseControl(c)
	}
	a.pendingBlock(c, *g)
	for _, f := range g.Fields {
		if view.ChannelKey(f.Key) {
			continue // drawn by the channel cards
		}
		a.settingField(c, g, f)
	}
}

// settingsNativeSaveBar is the group's one save bar: native text + one primary
// button. It exists only while the group is dirty.
func (a *App) settingsNativeSaveBar(c *mui.Context, vm *view.SettingsVM, g *view.SettingsGroup, dirty int) {
	tk := a.tk
	_, wired := a.cmd.(SettingsSaver)
	saving := a.savingSettings(g.ID)
	effect := g.Effect
	if effect == "" {
		effect = view.SaveEffect(g.ID)
	}
	mui.Column(c).Gap(4).BorderWidth(0.5, 0, 0, 0).BorderColor(tk.Border).
		Padding(12, 0).Children(func() {
		mui.Row(c).Gap(10).AlignItems(mui.Center).Children(func() {
			mui.Text(c, fmt.Sprintf("%d 处未保存更改 · 保存在 %s · rev %d · %s",
				dirty, vm.Source, vm.Revision, effect)).
				FontSize(fs(13)).TextColor(tk.TextMuted)
			mui.Spacer(c)
			label := "保存"
			if saving {
				label = "保存中…"
			}
			btn := a.primaryBtn(c, label)
			if saving {
				btn.Disabled(true)
			} else if btn.Clicked() {
				if !wired {
					a.settingsErr = "暂时不可用：运行时还没有接入保存设置：没有保存通道。"
				} else {
					a.saveSettings(vm, g)
				}
			}
			if !wired {
				mui.Text(c, "暂时不可用：运行时还没有接入保存设置").
					FontSize(fs(13)).TextColor(tk.Amber)
			}
		})
		if a.settingsConflict(vm) {
			mui.Text(c, fmt.Sprintf("设置已被其他窗口修改（rev %d）；保存前建议重新核对本组的值。", vm.Revision)).
				FontSize(fs(13)).TextColor(tk.Amber)
		}
		if a.settingsErr != "" {
			mui.Text(c, a.settingsErr).FontSize(fs(13)).TextColor(tk.Red)
		}
	})
}

// settingsProjectsNative is the Projects extension: one native form for the
// selected project's browser, skills and stats, plus a link to the full project
// page. Honest when the runtime does not publish the workspace panel yet.
func (a *App) settingsProjectsNative(c *mui.Context) {
	tk := a.tk
	vm := a.snap.Workspaces()
	if vm == nil || len(vm.Workspaces) == 0 {
		mui.Text(c, "还没有项目：先在侧栏新建一个项目。").FontSize(fs(13)).TextColor(tk.TextMuted)
		return
	}
	mui.Text(c, "项目").FontSize(fs(15)).Bold().TextColor(tk.Text)
	mui.Row(c).Gap(6).Wrap().Children(func() {
		for _, ws := range vm.Workspaces {
			ws := ws
			on := a.workspaceSel == ws.ID
			btn := a.settingsButton(c, ws.Name, on)
			if on {
				btn.Disabled(true)
			} else if btn.Clicked() {
				a.workspaceSel = ws.ID
				a.wsPanelFor = ""
				a.wsPanelTab = ""
			}
		}
	})
	ws := a.selectedProject()
	if ws == nil {
		return
	}
	if a.wsPanelFor != ws.ID {
		a.wsPanelFor = ws.ID
		a.wsPanelLoading = true
		startWorkspacePanelLoad(a, ws.ID)
	}
	if a.wsPanelLoading {
		spinner(c, tk, "正在读取项目设置…")
		return
	}
	if a.wsSettingsWired {
		on := a.wsSettings.BrowserEnabled
		mui.Row(c).Gap(8).AlignItems(mui.Center).Children(func() {
			if a.dshSwitch(c, "ws:browser-enabled", "为这个项目启用浏览器", &on) {
				a.wsSettings.BrowserEnabled = on
			}
			mui.Text(c, "为这个项目启用浏览器").FontSize(fs(12)).TextColor(tk.Text)
		})
		mui.Row(c).Gap(8).AlignItems(mui.Center).Children(func() {
			mui.Text(c, "浏览器").FontSize(fs(12)).TextColor(tk.TextMuted)
			for _, br := range a.wsDetected {
				br := br
				sel := a.wsSettings.Browser == br.ID
				b := a.settingsButton(c, br.Name, sel)
				if sel || !br.Available {
					b.Disabled(true)
				} else if b.Clicked() {
					a.wsSettings.Browser = br.ID
				}
			}
			mui.Spacer(c)
		})
		mui.Row(c).Gap(8).AlignItems(mui.Center).Children(func() {
			h := a.wsSettings.Headless
			if a.dshSwitch(c, "ws:browser-headless", "无头模式", &h) {
				a.wsSettings.Headless = h
			}
			mui.Text(c, "无头模式").FontSize(fs(12)).TextColor(tk.TextMuted)
		})
		mui.Row(c).Gap(8).AlignItems(mui.Center).Children(func() {
			mui.Text(c, "CDP 端口").FontSize(fs(12)).TextColor(tk.TextMuted)
			mui.TextInput(c, &a.wsPortText).Width(100).Height(30).Label("CDP 端口")
			mui.Text(c, "0 = 自动").FontSize(fs(12)).TextColor(tk.TextFaint)
		})
		a.browserPrereqCard(c, true)
	}
	// Skills: native switches, default on, per-project override.
	if a.wsSkillsWired {
		mui.Text(c, "技能").FontSize(fs(14)).Bold().TextColor(tk.Text)
		for _, sk := range a.wsSkills {
			sk := sk
			on := a.wsSettings.SkillEnabled(sk.ID, true)
			mui.Row(c).Gap(8).AlignItems(mui.Center).Children(func() {
				if a.dshSwitch(c, "ws:skill:"+sk.ID, "技能 "+sk.Name, &on) {
					a.wsSettings.SetSkillEnabled(sk.ID, on, sk.EnabledByDefault)
				}
				mui.Text(c, sk.Name).FontSize(fs(12)).TextColor(tk.Text)
				if sk.Description != "" {
					mui.Text(c, "· "+sk.Description).FontSize(fs(12)).TextColor(tk.TextFaint)
				}
			})
		}
	}
	// Stats.
	if a.wsStatsWired {
		st := a.wsStats
		mui.Text(c, fmt.Sprintf("会话 %d · 任务 %d · 进行中 %d · 等待你 %d · 已完成 %d · 失败 %d",
			st.Conversations, st.Tasks, st.Running, st.Waiting, st.Completed, st.Failed)).
			FontSize(fs(11)).TextColor(tk.TextMuted)
	}
	mui.Row(c).Gap(8).Children(func() {
		if b := a.settingsButton(c, "打开项目设置", false); b.Clicked() {
			a.openProject(ws.ID)
			a.wsTab = "settings"
		}
		_, wired := a.cmd.(WorkspaceSettingsSaver)
		label := "保存项目设置"
		if a.wsSaving {
			label = "保存中…"
		}
		save := a.settingsButton(c, label, true)
		if !wired || !a.wsSettingsWired || a.wsSaving {
			save.Disabled(true)
		} else if save.Clicked() {
			a.wsSaving = true
			a.wsSaveErr = ""
			if n, err := strconv.Atoi(strings.TrimSpace(a.wsPortText)); err == nil {
				a.wsSettings.Port = n
			}
			startWorkspaceSettingsSave(a, ws.ID, a.wsSettings)
		}
		if !wired {
			mui.Text(c, "暂时不可用：运行时还没有接入保存项目设置").
				FontSize(fs(10)).TextColor(tk.Amber)
		}
	})
	if a.wsSaveErr != "" {
		mui.Text(c, "保存失败："+a.wsSaveErr).FontSize(fs(12)).TextColor(tk.Red)
	}
}

// settingsNavItem is one DSH settings nav row: native button base, left aligned
// text, 32px high, radius 8, interactive hover when selected.
func (a *App) settingsNavItem(c *mui.Context, label string, selected bool, onClick func()) {
	tk := a.tk
	bg := tk.Surface
	if selected {
		bg = tk.SurfaceAlt
	}
	row := mui.ButtonBase(c).Width(156).Height(32).Padding(6, 10).Radius(8).
		Background(bg).Label(label)
	row.Children(func() {
		mui.Row(c).Grow(1).Gap(8).AlignItems(mui.Center).Children(func() {
			t := mui.Text(c, label).FontSize(fs(13)).Grow(1).TextColor(tk.Text)
			if selected {
				t.Bold()
			}
		})
	})
	if row.Clicked() {
		onClick()
	}
}
