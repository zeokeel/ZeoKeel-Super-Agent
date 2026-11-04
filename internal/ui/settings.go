package ui

import (
	"fmt"
	"strconv"
	"strings"

	mui "github.com/egoist/mygo/ui"

	"github.com/zeokeel/super-agent/internal/view"
)

// settingsPage is the one page with configuration actions (design §6.3: 配置动作
// 只在设置页). The groups are 来源 / 评估 / 执行 / 提示词 / 部署; 来源 and 执行 are
// real forms, the others keep their structure and say why their form is not
// here yet. Each group has one save bar, and it appears only when the operator
// has changed something (005 "默认安静").
func (a *App) settingsPage(c *mui.Context) {
	tk := a.tk
	vm := a.snap.Settings()

	mui.Column(c).Fill().Padding(20, 24).Gap(12).Children(func() {
		mui.Row(c).AlignItems(mui.Center).Gap(8).Children(func() {
			heading(c, tk, "设置", "每块只有一个保存条；能从默认值来的，就不做成设置项")
			mui.Spacer(c)
			if vm != nil {
				textChip(c, tk, "来自 "+vm.Source)
				chip(c, tk, fmt.Sprintf("配置 rev %d", vm.Revision), tk.TextMuted, tk.SurfaceAlt)
			}
		})
		if vm == nil {
			mui.Text(c, "等待运行时发布设置快照…").FontSize(fs(15)).TextColor(tk.TextMuted)
			return
		}

		a.reconcileSettings(vm)
		groups := settingsGroups(vm)
		g := &view.SettingsGroup{ID: a.settingsGroup, Label: string(a.settingsGroup), Note: "这一组还没有投影。"}
		for i := range groups {
			if groups[i].ID == a.settingsGroup {
				g = &groups[i]
			}
		}
		dirty := a.settingsDirty(g)

		mui.Row(c).Grow(1).AlignItems(mui.Stretch).Gap(16).Children(func() {
			a.settingsRail(c, groups)
			mui.Column(c).Grow(1).MinWidth(0).Gap(10).Children(func() {
				mui.Scroll(c).Grow(1).Children(func() { a.settingsForm(c, g) })
				if dirty > 0 {
					a.settingsSaveBar(c, vm, g, dirty)
				}
				// 提示词 has its own bar, pinned here for the same reason: a save
				// bar inside a long scrolling form can be off screen when it appears.
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
	})
}

// settingsGroups merges what the runtime published with the five groups the page
// always shows (view.RequiredSettingsGroups). A group that has not been projected
// yet appears with its skeleton and its "还没有数据" facts, so a missing group
// cannot look like a page with nothing to configure.
func settingsGroups(vm *view.SettingsVM) []view.SettingsGroup {
	required := view.RequiredSettingsGroups()
	out := make([]view.SettingsGroup, 0, len(required)+len(vm.Groups))
	seen := map[view.SettingsGroupID]bool{}
	for _, req := range required {
		req := req
		if pub := vm.Group(req.ID); pub != nil {
			g := *pub
			if g.Label == "" {
				g.Label = req.Label
			}
			if g.Note == "" {
				g.Note = req.Note
			}
			if len(g.Pending) == 0 {
				g.Pending = req.Pending
			}
			out = append(out, g)
		} else {
			out = append(out, req)
		}
		seen[req.ID] = true
	}
	// A group the runtime publishes beyond the five (people, for one) is kept:
	// the page does not drop a projection just because it is not required.
	for _, g := range vm.Groups {
		if !seen[g.ID] {
			out = append(out, g)
		}
	}
	return out
}

// settingsRail is the group list. A dot marks a group with unsaved changes, so
// the operator can see them without opening the group.
func (a *App) settingsRail(c *mui.Context, groups []view.SettingsGroup) {
	tk := a.tk
	mui.Column(c).Width(200).Background(tk.Surface).Radius(tk.Radius).Border(1, tk.Border).
		Padding(8).Gap(2).Children(func() {
		for _, g := range groups {
			on := g.ID == a.settingsGroup
			dirty := a.settingsDirty(&g)
			if g.ID == view.SettingsPrompt {
				dirty += a.promptRailDirty()
			}
			row := mui.Row(c).Height(34).Padding(0, 10).Radius(7).Gap(9).AlignItems(mui.Center).
				Background(ifColor(on, tk.AccentSoft, mui.Transparent))
			if row.Clicked() {
				if a.settingsGroup != g.ID {
					a.promptReloaded = 0 // the hint belongs to the group it was made in
				}
				a.settingsGroup = g.ID
				a.settingsErr = ""
			}
			row.Children(func() {
				mui.Icon(c, settingsIcon(g.ID)).FontSize(fs(14)).
					TextColor(ifColor(on, tk.Accent, tk.TextMuted))
				mui.Text(c, g.Label).FontSize(fs(14)).TextColor(ifColor(on, tk.Accent, tk.Text))
				mui.Spacer(c)
				if dirty > 0 {
					dot(c, tk.Amber, 6)
				}
			})
		}
		mui.Spacer(c)
		mui.Row(c).Padding(6, 8).Children(func() {
			mui.Text(c, "改完只影响之后的运行。").FontSize(fs(12)).TextColor(tk.TextFaint)
		})
	})
}

// settingsForm is the selected group. A group with a Placeholder keeps its
// structure (title, note, facts) and says what is missing, instead of hiding.
// 提示词 is the exception: it has a real editor now, and its facts stay below it.
func (a *App) settingsForm(c *mui.Context, g *view.SettingsGroup) {
	tk := a.tk
	card(c, tk, 16, func() {
		mui.Row(c).Gap(8).AlignItems(mui.Center).Children(func() {
			mui.Text(c, g.Label).FontSize(fs(17)).Bold().TextColor(tk.Text)
			mui.Spacer(c)
			mui.Text(c, g.Note).FontSize(fs(12)).TextAlign(mui.End).TextColor(tk.TextFaint)
		})
		if g.ID == view.SettingsPrompt {
			a.promptGroup(c, g)
			a.pendingBlock(c, *g)
			return
		}
		if g.ID == view.SettingsDeploy {
			// 部署 is the operator's console, not a form: the log path, the
			// backup and archive commands and the exit codes are fixed facts of
			// the CLI, and the health rows are the live ones.
			a.deploymentSection(c, *g)
			a.browserFactsSection(c)
		}
		if g.ID == view.SettingsWorker {
			a.modelTestRow(c)
		}
		if g.ID == view.SettingsAbout {
			a.aboutSection(c)
		}
		if g.ID == view.SettingsSource {
			// The IM channels get a card each: enable, transport, credentials and
			// the runtime's check. Their fields are drawn inside the cards, not
			// again below (the generic loop skips channel keys).
			a.channelCards(c, g)
			if a.channelErr != "" {
				mui.Text(c, a.channelErr).FontSize(fs(11)).TextColor(tk.Red)
			}
		}
		if g.ID == view.SettingsExecution {
			a.pauseControl(c)
		}
		if g.Placeholder != "" {
			mui.Row(c).Padding(9, 12).Radius(8).Background(tk.SurfaceAlt).Gap(8).
				AlignItems(mui.Start).Children(func() {
				mui.Icon(c, icClock).FontSize(fs(12)).TextColor(tk.TextMuted)
				mui.Text(c, g.Placeholder).FontSize(fs(13)).Grow(1).TextColor(tk.TextMuted)
			})
		}
		a.pendingBlock(c, *g)
		for _, f := range g.Fields {
			if view.ChannelKey(f.Key) {
				continue // drawn by the IM channel cards above
			}
			a.settingField(c, g, f)
		}
	})
}

// pendingBlock says what the group will carry once the runtime projects it. It
// is how a group with no form yet stays honest: the page names the facts that
// are missing instead of drawing an empty form that reads as "nothing to set".
func (a *App) pendingBlock(c *mui.Context, g view.SettingsGroup) {
	if len(g.Pending) == 0 {
		return
	}
	tk := a.tk
	mui.Row(c).Padding(9, 12).Radius(8).Background(tk.SurfaceAlt).Gap(8).
		AlignItems(mui.Start).Children(func() {
		mui.Icon(c, icClock).FontSize(fs(12)).TextColor(tk.TextMuted)
		mui.Text(c, "还没有数据："+strings.Join(g.Pending, " · ")).
			FontSize(11).Grow(1).TextColor(tk.TextMuted)
	})
}

// deploymentSection is 设置 › 部署: the operator's console. The commands and exit
// codes are the CLI's fixed contract (006:113-124); the health rows are the live
// projection. It shows no editable field, so it carries no save bar.
func (a *App) deploymentSection(c *mui.Context, g view.SettingsGroup) {
	tk := a.tk
	mui.Text(c, "运维面（命令行）").FontSize(fs(14)).Bold().TextColor(tk.Text)
	mui.Text(c, "以下路径与退出码是命令行的固定契约；运行状态以健康行为准。").
		FontSize(10).TextColor(tk.TextFaint)
	for _, fact := range [][2]string{
		{"日志", "<stateDir>/logs/zeokeel.log · 按天轮转，保留 14 天"},
		{"备份", "zeokeel backup → <stateDir>/backups/zeokeel-<date>.db"},
		{"归档", "zeokeel archive → 结束超过 180 天的工单 / 触发 / 决策移到 archive.db"},
		{"状态", "zeokeel status：0 健康 · 1 可重启修复 · 2 组件错误 · 3 等待初次设置"},
		{"数据目录", "<stateDir>：SQLite 状态库、日志、备份都在它下面"},
	} {
		kv(c, tk, fact[0], fact[1])
	}

	// The machine, as the runtime reports it: OS/arch and timezone. Without the
	// projection the page says so instead of writing "linux" from a guess.
	ov := a.snap.Overview()
	mui.Text(c, "运行环境").FontSize(fs(14)).Bold().TextColor(tk.Text)
	if ov == nil || !ov.System.Known() {
		mui.Text(c, "还没有数据操作系统与时区（Runtime.System）").
			FontSize(11).TextColor(tk.TextMuted)
	} else {
		kv(c, tk, "操作系统", strings.TrimSuffix(ov.System.OS+"/"+ov.System.Arch, "/"))
		tz := ov.System.Timezone
		if ov.System.UTCOffset != "" {
			tz += " (" + ov.System.UTCOffset + ")"
		}
		kv(c, tk, "时区", tz)
	}

	// The panel's own scale: the "global +2" became one setting (ui.font_scale),
	// applied through fs() rather than baked into 300 numbers (P2).
	mui.Text(c, "界面").FontSize(fs(12)).Bold().TextColor(tk.Text)
	a.fontScaleControl(c, g)

	// The live part: the same health rows the overview draws, so the two cannot
	// disagree about whether the system is well.
	mui.Text(c, "健康").FontSize(fs(14)).Bold().TextColor(tk.Text)
	if ov == nil {
		mui.Text(c, "等待运行时发布健康快照…").FontSize(fs(13)).TextColor(tk.TextMuted)
		return
	}
	if len(ov.Health) == 0 {
		mui.Text(c, "运行时还没有投影任何健康项。").FontSize(fs(13)).TextColor(tk.TextMuted)
		return
	}
	for _, h := range ov.Health {
		mui.Row(c).Gap(10).AlignItems(mui.Center).Children(func() {
			dot(c, ifColor(h.OK, tk.Green, tk.Amber), 7)
			mui.Text(c, h.Name).FontSize(fs(13)).TextColor(tk.Text)
			mui.Spacer(c)
			mui.Text(c, h.Detail).FontSize(fs(12)).TextColor(tk.TextMuted)
		})
	}
}

// fontScaleControl is 设置 › 部署's 界面字号: it reads ui.font_scale from the
// published settings and writes an edit through the one save protocol. A runtime
// that has not projected the field gets an honest read-only line instead of a
// control that saves nothing.
func (a *App) fontScaleControl(c *mui.Context, g view.SettingsGroup) {
	tk := a.tk
	scales := []float32{0.9, 1.0, 1.1}
	labels := []string{"90%", "100%", "110%"}
	field, ok := settingFieldOf(&g, "ui.font_scale")
	if !ok {
		mui.Row(c).Gap(10).AlignItems(mui.Center).Children(func() {
			mui.Text(c, "界面字号").FontSize(fs(12)).TextColor(tk.Text)
			mui.Text(c, fmt.Sprintf("当前 %d%%", int(a.settingViewScale()*100))).
				FontSize(12).TextColor(tk.TextMuted)
			mui.Text(c, "还没有数据：ui.font_scale（0.9 / 1.0 / 1.1）").
				FontSize(10).TextColor(tk.Amber)
		})
		return
	}
	ptr := a.settingsPtr(g.ID, field)
	cur := a.settingViewScale()
	idx := 1
	for i, v := range scales {
		if abs32(v-cur) < 0.001 {
			idx = i
		}
	}
	mui.Row(c).Gap(10).AlignItems(mui.Center).Children(func() {
		mui.Text(c, "界面字号").FontSize(fs(12)).TextColor(tk.Text)
		if mui.Segmented(c.Key("font-scale"), &idx, labels...).Label("界面字号").Changed() {
			*ptr = strconv.FormatFloat(float64(scales[idx]), 'f', 1, 32)
		}
		mui.Text(c, "保存后按新字号重绘").FontSize(fs(10)).TextColor(tk.TextFaint)
	})
}

func abs32(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}

// pauseControl is 设置 › 执行's run/pause switch: the one place the operator
// pauses automatic work. It shows the projected state (False = running), and a
// runtime without PauseExecution gets a disabled button and the reason.
func (a *App) pauseControl(c *mui.Context) {
	tk := a.tk
	ch := a.actionChannels()
	ov := a.snap.Overview()
	paused, known := false, false
	if ov != nil {
		paused, known = ov.Runtime.Paused, ov.Runtime.PausedKnown
	}
	state, fg, bg := "状态未知", tk.TextMuted, tk.SurfaceAlt
	switch {
	case known && paused:
		state, fg, bg = "已暂停", tk.Amber, tk.AmberSoft
	case known:
		state, fg, bg = "运行中", tk.Green, tk.GreenSoft
	}
	mui.Row(c).Gap(10).AlignItems(mui.Center).Children(func() {
		mui.Text(c, "自动派工").FontSize(fs(14)).TextColor(tk.Text)
		chip(c, tk, state, fg, bg)
		mui.Text(c, "暂停后不再启动新的执行；已在跑的 Assignment 不受影响。").
			FontSize(10).TextColor(tk.TextFaint)
		mui.Spacer(c)
		if !known {
			// Without the projected state a single toggle would guess, so both
			// directions are offered and each button means exactly one thing.
			a.pauseButton(c, "暂停", true, ch.pause)
			a.pauseButton(c, "恢复", false, ch.pause)
			return
		}
		label := "暂停"
		if paused {
			label = "恢复"
		}
		a.pauseButton(c, label, !paused, ch.pause)
	})
	if !known {
		mui.Text(c, "暂停状态未投影：运行时需发布 Runtime.Paused / PausedKnown（按钮仍可用）。").
			FontSize(10).TextColor(tk.Amber)
	}
	if !ch.pause {
		mui.Text(c, "暂停通道未接线：运行时未实现 ExecutionPauser").
			FontSize(10).TextColor(tk.Amber)
	}
	if a.pauseErr != "" {
		mui.Text(c, a.pauseErr).FontSize(fs(12)).TextColor(tk.Red)
	}
}

// pauseButton is one pause/resume button; disabled when the runtime has no
// PauseExecution, so a click can never be a silent no-op.
func (a *App) pauseButton(c *mui.Context, label string, paused bool, wired bool) {
	btn := mui.Button(c, label).Label(label).Height(24).FontSize(fs(13))
	if !wired {
		btn.Disabled(true)
		return
	}
	if btn.Clicked() {
		a.setPaused(paused)
	}
}

// setPaused sends the pause/resume command. The button's state follows the
// published projection, not this call: no optimistic update (design §6.2).
func (a *App) setPaused(paused bool) {
	pauser, ok := a.cmd.(ExecutionPauser)
	if !ok || pauser == nil {
		a.pauseErr = "暂停通道未接线：运行时未实现 ExecutionPauser"
		return
	}
	if err := pauser.PauseExecution(paused); err != nil {
		a.pauseErr = err.Error()
		return
	}
	a.pauseErr = ""
}

// settingField draws one field. Facts are read-only lines; editable fields bind
// to the app's own edit, so no element is stored across frames and the value
// survives typing (design §6.1, rule 2).
func (a *App) settingField(c *mui.Context, g *view.SettingsGroup, f view.SettingField) {
	tk := a.tk
	switch f.Kind {
	case view.SettingFact:
		mui.Row(c).Gap(12).AlignItems(mui.Center).Children(func() {
			mui.Text(c, f.Label).FontSize(fs(13)).Width(180).TextColor(tk.TextMuted)
			mui.Text(c, f.Value).FontSize(fs(13)).Grow(1).TextColor(tk.Text)
		})
		return
	case view.SettingSwitch:
		ptr := a.settingsPtr(g.ID, f)
		on := settingBool(*ptr)
		mui.Row(c).Gap(12).AlignItems(mui.Center).Children(func() {
			mui.Column(c).Grow(1).Gap(1).Children(func() {
				mui.Text(c, f.Label).FontSize(fs(14)).TextColor(tk.Text)
				if f.Help != "" {
					mui.Text(c, f.Help).FontSize(fs(12)).TextColor(tk.TextFaint)
				}
			})
			if mui.Switch(c.Key("set:"+f.Key), &on).Changed() {
				*ptr = boolText(on)
			}
		})
		return
	}

	// A text, number or secret field.
	ptr := a.settingsPtr(g.ID, f)
	mui.Column(c).Gap(4).Children(func() {
		mui.Row(c).Gap(6).AlignItems(mui.Center).Children(func() {
			mui.Text(c, f.Label).FontSize(fs(13)).TextColor(tk.TextMuted)
			if f.Help != "" {
				mui.Text(c, "· "+f.Help).FontSize(fs(12)).SingleLine().TextColor(tk.TextFaint)
			}
		})
		field := mui.TextInput(c.Key("set:"+f.Key), ptr).Height(30).FontSize(fs(14))
		if f.Kind == view.SettingSecret {
			field.Password()
		}
	})
}

// settingsSaveBar is the group's one save bar. It exists only while the group is
// dirty; when no writer is wired it shows a disabled save and says so, instead of
// pretending the values were stored.
func (a *App) settingsSaveBar(c *mui.Context, vm *view.SettingsVM, g *view.SettingsGroup, dirty int) {
	tk := a.tk
	_, wired := a.cmd.(SettingsSaver)
	saving := a.savingSettings(g.ID)

	mui.Column(c).Gap(4).Children(func() {
		mui.Row(c).Padding(10, 14).Gap(10).Radius(10).Background(tk.Surface).
			Border(1, tk.Border).AlignItems(mui.Center).Children(func() {
			dot(c, tk.Amber, 7)
			mui.Text(c, fmt.Sprintf("%d 处未保存更改", dirty)).FontSize(fs(14)).TextColor(tk.Text)
			// Only a group whose changes are really live says 热更新; the rest say
			// when they take effect (P2; SettingsGroup.Effect overrides the default).
			effect := g.Effect
			if effect == "" {
				effect = view.SaveEffect(g.ID)
			}
			mui.Text(c, "· 保存到 "+vm.Source+" · "+effect).FontSize(fs(12)).
				TextColor(tk.TextFaint)
			mui.Spacer(c)
			if wired {
				label := "保存"
				if saving {
					label = "保存中…"
				}
				btn := mui.PrimaryButton(c, label).Label(label).Height(26)
				if saving {
					btn.Disabled(true)
				} else if btn.Clicked() {
					a.saveSettings(vm, g)
				}
			} else {
				// A disabled save that explains itself when clicked: a control
				// that silently does nothing is worse than a grey one.
				btn := mui.Row(c).Height(26).Padding(0, 12).Radius(7).
					Background(tk.SurfaceAlt).AlignItems(mui.Center).Children(func() {
					mui.Text(c, "保存").FontSize(fs(14)).TextColor(tk.TextFaint)
				})
				if btn.Clicked() {
					a.settingsErr = "暂时不可用：运行时还没有接入保存设置：没有保存通道。"
				}
			}
		})
		if !wired {
			mui.Text(c, "暂时不可用：运行时还没有接入保存设置").FontSize(fs(12)).TextColor(tk.TextFaint)
		}
		if a.settingsConflict(vm) {
			mui.Text(c, fmt.Sprintf(
				"设置已被其他窗口修改（rev %d）；保存前建议重新核对本组的值。", vm.Revision)).
				FontSize(10).TextColor(tk.Amber)
		}
		if a.settingsErr != "" {
			mui.Text(c, a.settingsErr).FontSize(fs(12)).TextColor(tk.Red)
		}
	})
}

// modelTestRow is 设置 › 模型's 测试连接: it probes the SAVED configuration (the
// runtime reads its store), so the copy says to save first. The probe runs off the
// frame and its result stays here; no fake "已连接".
func (a *App) modelTestRow(c *mui.Context) {
	tk := a.tk
	_, wired := a.cmd.(ModelTester)
	mui.Row(c).Gap(10).AlignItems(mui.Center).Children(func() {
		label := "测试连接"
		if a.modelTestPending {
			label = "测试中…"
		}
		btn := mui.Button(c, label).Label(label).Height(28).FontSize(fs(12))
		if !wired || a.modelTestPending {
			btn.Disabled(true)
		} else if btn.Clicked() {
			a.modelTestPending = true
			a.modelTestOK = false
			a.modelTestDetail = ""
			startModelProbe(a)
		}
		mui.Text(c, "测试使用已保存的配置；改完先保存").FontSize(fs(10)).TextColor(tk.TextFaint)
		if a.modelTestPending {
			spinner(c, tk, "正在连接模型…")
		}
	})
	if a.modelTestDetail != "" {
		fg := tk.Red
		if a.modelTestOK {
			fg = tk.Green
		}
		mui.Row(c).Gap(6).AlignItems(mui.Center).Children(func() {
			dot(c, fg, 6)
			mui.Text(c, a.modelTestDetail).FontSize(fs(12)).TextColor(fg)
		})
	}
	if !wired {
		mui.Text(c, "暂时不可用：运行时还没有接入模型检查").FontSize(fs(11)).TextColor(tk.Amber)
	}
}

// settingsPtr returns the live edit of one field. The pointer belongs to the app,
// not to a frame, so a text input keeps editing the same value frame to frame.
func (a *App) settingsPtr(group view.SettingsGroupID, f view.SettingField) *string {
	key := string(group) + "/" + f.Key
	p, ok := a.settingsEdits[key]
	if !ok {
		v := f.Value
		p = &v
		a.settingsEdits[key] = p
	}
	return p
}

// settingsDirty counts the changed fields of one group.
func (a *App) settingsDirty(g *view.SettingsGroup) int {
	n := 0
	for _, f := range g.Fields {
		if f.Kind == view.SettingFact {
			continue
		}
		if p, ok := a.settingsEdits[string(g.ID)+"/"+f.Key]; ok && *p != f.Value {
			n++
		}
	}
	return n
}

// reconcileSettings drops an edit once the child publishes a value that matches
// it. This is what "no optimistic update" means: the value is shown as saved only
// after the new projection arrives, and an edit is never silently discarded.
func (a *App) reconcileSettings(vm *view.SettingsVM) {
	if len(a.settingsEdits) == 0 {
		// No local change: the page follows the published configuration, and the
		// next save is based on what it shows. This is what keeps an old edit
		// session from reporting a conflict after the page was merely reopened.
		a.settingsEditRev = vm.Revision
	}
	for _, g := range vm.Groups {
		for _, f := range g.Fields {
			key := string(g.ID) + "/" + f.Key
			if p, ok := a.settingsEdits[key]; ok && *p == f.Value {
				delete(a.settingsEdits, key)
			}
		}
	}
}

// settingsConflict reports that the configuration changed under the edits: the
// published revision is no longer the one the edits were based on.
func (a *App) settingsConflict(vm *view.SettingsVM) bool {
	return a.settingsEditRev != 0 && vm.Revision != a.settingsEditRev && len(a.settingsEdits) > 0
}

// saveSettings hands the changed fields of one group to the runtime and keeps
// the edits until the new projection matches them. It never waits: SaveSettings
// only enqueues (design §6.2).
func (a *App) saveSettings(vm *view.SettingsVM, g *view.SettingsGroup) {
	saver, ok := a.cmd.(SettingsSaver)
	if !ok || saver == nil {
		a.settingsErr = "暂时不可用：运行时还没有接入保存设置：没有保存通道。"
		return
	}
	values := make(map[string]string, len(g.Fields))
	for _, f := range g.Fields {
		if f.Kind == view.SettingFact {
			continue
		}
		p, ok := a.settingsEdits[string(g.ID)+"/"+f.Key]
		if !ok || *p == f.Value {
			continue
		}
		values[f.Key] = *p
	}
	if len(values) == 0 {
		a.settingsErr = ""
		return
	}
	base := a.settingsEditRev
	if base == 0 {
		base = vm.Revision
	}
	values[SettingsRevisionKey] = strconv.FormatUint(base, 10)
	group := g.ID
	a.settingsErr = ""
	a.markSaving(string(group))
	startSaveSettings(a, string(group), values, func(rev uint64, err error) {
		a.savingGroup = ""
		if err != nil {
			a.settingsErr = err.Error()
			return
		}
		a.settingsEditRev = rev
	})
}

// settingsIcon is the icon of one group.
func settingsIcon(id view.SettingsGroupID) *mui.SVG {
	switch id {
	case view.SettingsSource:
		return icSource
	case view.SettingsEval:
		return icEval
	case view.SettingsExecution:
		return icRun
	case view.SettingsPeople:
		return icUser
	case view.SettingsPrompt:
		return icFile
	default:
		return icTerminal
	}
}
