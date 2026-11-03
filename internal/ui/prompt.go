package ui

import (
	"fmt"
	"strconv"

	mui "github.com/egoist/mygo/ui"

	"github.com/zeokeel/super-agent/internal/view"
)

// PromptReloader is the optional read channel of the prompt editor. After a save
// conflicts with another window, 重新加载 asks the runtime to publish the current
// layers again and returns their revision; a runtime without it still reloads from
// the last projection the window holds, and says which revision it shows instead
// of pretending it re-read the file. The implementation must only enqueue the
// read: the main thread is the event loop (design §6.1).
type PromptReloader interface {
	ReloadPrompts() (revision uint64, err error)
}

// promptGroup is 设置 › 提示词: three editable layers, each with a read-only
// preview, an editor, and one save bar for the group that appears only when
// something changed (005 "默认安静"). It replaces the placeholder the group had
// while the write channel was missing: the page now says what is missing instead
// of hiding the form.
func (a *App) promptGroup(c *mui.Context, g *view.SettingsGroup) {
	tk := a.tk
	vm := a.snap.Prompts()
	wired := a.promptWired()

	if vm == nil {
		mui.Row(c).Padding(9, 12).Radius(8).Background(tk.SurfaceAlt).Gap(8).
			AlignItems(mui.Start).Children(func() {
			mui.Icon(c, icClock).FontSize(12).TextColor(tk.TextMuted)
			mui.Text(c, "等待运行时发布提示词快照…").FontSize(13).Grow(1).TextColor(tk.TextMuted)
		})
		for _, f := range g.Fields {
			a.settingField(c, g, f)
		}
		if !wired {
			mui.Text(c, "当前运行时不能保存设置：可编辑，但保存写不出去").FontSize(12).
				TextColor(tk.Amber)
		}
		return
	}

	a.reconcilePrompts(vm)
	mui.Row(c).Gap(8).AlignItems(mui.Center).Children(func() {
		mui.Text(c, fmt.Sprintf("%d 层可编辑 · %d 层已覆盖默认", vm.EditableCount(), vm.ChangedCount())).
			FontSize(11).TextColor(tk.TextMuted)
		mui.Spacer(c)
		if vm.Revision != 0 {
			chip(c, tk, fmt.Sprintf("提示词 rev %d", vm.Revision), tk.TextMuted, tk.SurfaceAlt)
		}
		textChip(c, tk, "来自 "+vm.Source)
	})

	for i, l := range vm.Layers {
		if i > 0 {
			mui.Box(c).Height(1).Background(tk.BorderSoft)
		}
		a.promptLayer(c, l)
	}
	for _, f := range g.Fields {
		a.settingField(c, g, f)
	}

	if !wired {
		mui.Text(c, "当前运行时不能保存设置：可编辑，但保存写不出去").FontSize(12).
			TextColor(tk.Amber)
	}
}

// promptLayer is one layer: label, revision, default/overridden chip, the note,
// and either the read-only preview or the editor. Only one layer is edited at a
// time, so 完成编辑 and 恢复默认 have one home and the save bar stays the group's.
func (a *App) promptLayer(c *mui.Context, l view.PromptLayer) {
	tk := a.tk
	editing := a.promptEditing == l.ID

	mui.Column(c).Gap(7).Children(func() {
		mui.Row(c).Gap(8).AlignItems(mui.Center).Children(func() {
			mui.Text(c, l.Label).FontSize(15).Bold().TextColor(tk.Text)
			if l.Revision != 0 {
				chip(c, tk, fmt.Sprintf("rev %d", l.Revision), tk.TextMuted, tk.SurfaceAlt)
			}
			if l.IsDefault() {
				chip(c, tk, "默认", tk.TextMuted, tk.SurfaceAlt)
			} else {
				chip(c, tk, "已覆盖", tk.Amber, tk.AmberSoft)
			}
			if !l.Editable {
				chip(c, tk, "只读", tk.TextFaint, tk.SurfaceAlt)
			}
			mui.Spacer(c)
			if l.Editable {
				if editing {
					if mui.Button(c, "完成编辑").Height(22).Clicked() {
						a.promptEditing = ""
					}
					if mui.Button(c, "恢复默认").Height(22).Clicked() {
						*a.promptPtr(l) = l.Default
						a.promptReloaded = 0
					}
				} else if mui.Button(c, promptEditLabel(l.ID)).Height(22).Clicked() {
					a.promptEditing = l.ID
					a.promptReloaded = 0
					// A click leaves the focus on the button; ask for the editor's
					// through a Handle, which waits for the binding of this build.
					a.promptFocus.Focus()
				}
			}
		})
		mui.Text(c, l.Note).FontSize(13).TextColor(tk.TextMuted)
		switch {
		case !l.Editable:
			if l.ReadOnlyNote != "" {
				mui.Text(c, l.ReadOnlyNote).FontSize(12).TextColor(tk.TextFaint)
			}
			a.promptPreview(c, l.Content)
		case editing:
			ta := mui.TextArea(c.Key("prompt-edit:"+string(l.ID)), a.promptPtr(l)).
				Height(190).FontSize(13).Font("monospace").Label(l.Label)
			ta.Bind(&a.promptFocus)
		default:
			a.promptPreview(c, l.Content)
		}
	})
}

// promptPreview is the read-only view of a layer. It scrolls: a prompt is longer
// than the card, and truncating it would hide the part being edited.
func (a *App) promptPreview(c *mui.Context, content string) {
	tk := a.tk
	mui.Scroll(c).Height(132).Radius(8).Background(tk.CodeBg).Children(func() {
		mui.Column(c).Padding(9, 12).Children(func() {
			if content == "" {
				mui.Text(c, "（空）").FontSize(13).Font("monospace").TextColor(tk.CodeText)
				return
			}
			mui.Text(c, content).FontSize(13).Font("monospace").TextColor(tk.CodeText)
		})
	})
}

// promptSaveBar is the group's one save bar: it appears with an edit, says when
// the write channel is missing, and offers 重新加载 when the save conflicted.
func (a *App) promptSaveBar(c *mui.Context, vm *view.PromptVM, dirty int) {
	tk := a.tk
	wired := a.promptWired()

	mui.Column(c).Gap(4).Children(func() {
		// The bar itself appears with an edit (or a save's outcome); a reload hint
		// alone is one quiet line, not a bar counting zero changes.
		if dirty > 0 {
			mui.Row(c).Padding(10, 14).Gap(10).Radius(10).Background(tk.Surface).
				Border(1, tk.Border).AlignItems(mui.Center).Children(func() {
				dot(c, tk.Amber, 7)
				mui.Text(c, fmt.Sprintf("%d 处未保存更改", dirty)).FontSize(14).TextColor(tk.Text)
				mui.Text(c, "· 保存后写入 "+vm.Source+" 并热更新").FontSize(12).TextColor(tk.TextFaint)
				mui.Spacer(c)
				if wired && !a.promptConflict {
					if mui.PrimaryButton(c, "保存").Label("保存").Height(26).Clicked() {
						a.savePrompts(vm)
					}
				} else if wired {
					mui.Text(c, "先重新加载").FontSize(12).TextColor(tk.Amber)
				} else {
					btn := mui.Row(c).Height(26).Padding(0, 12).Radius(7).
						Background(tk.SurfaceAlt).AlignItems(mui.Center).Children(func() {
						mui.Text(c, "保存").FontSize(14).TextColor(tk.TextFaint)
					})
					if btn.Clicked() {
						a.promptErr = "当前运行时不能保存设置：没有保存通道。"
					}
				}
			})
		}
		if !wired && dirty > 0 {
			mui.Text(c, "当前运行时不能保存设置").FontSize(12).TextColor(tk.TextFaint)
		}
		if a.promptConflict {
			mui.Row(c).Gap(10).AlignItems(mui.Center).Children(func() {
				mui.Icon(c, icBell).FontSize(12).TextColor(tk.Amber)
				mui.Text(c, "提示词已被别人改过，重新加载后再改。").
					FontSize(10).Grow(1).TextColor(tk.Amber)
				if mui.Button(c, "重新加载").Height(22).Clicked() {
					a.reloadPrompts()
				}
			})
		}
		if a.promptErr != "" {
			mui.Text(c, a.promptErr).FontSize(12).TextColor(tk.Red)
		}
		if a.promptReloaded != 0 {
			mui.Text(c, fmt.Sprintf("已重新加载（rev %d），本地未保存的改动已丢弃。", a.promptReloaded)).
				FontSize(10).TextColor(tk.TextFaint)
		}
	})
}

// promptPtr returns the live edit of one layer. The pointer belongs to the app, not
// to a frame, so a text area keeps editing the same value frame to frame (design
// §6.1, rule 2).
func (a *App) promptPtr(l view.PromptLayer) *string {
	key := string(l.ID)
	p, ok := a.promptEdits[key]
	if !ok {
		v := l.Content
		p = &v
		a.promptEdits[key] = p
	}
	return p
}

// promptDirty counts the layers whose edit differs from the published content.
func (a *App) promptDirty(vm *view.PromptVM) int {
	if vm == nil {
		return 0
	}
	n := 0
	for _, l := range vm.Layers {
		if p, ok := a.promptEdits[string(l.ID)]; ok && *p != l.Content {
			n++
		}
	}
	return n
}

// promptRailDirty is the count the settings rail shows on the 提示词 group.
func (a *App) promptRailDirty() int { return a.promptDirty(a.snap.Prompts()) }

// reconcilePrompts drops an edit once the child publishes content that matches it:
// the value shows as saved only after the projection arrives, and an edit is never
// silently discarded (design §6.2, no optimistic update). A conflict clears once
// no edit is left, since there is nothing left to overwrite.
func (a *App) reconcilePrompts(vm *view.PromptVM) {
	if vm == nil {
		return
	}
	known := make(map[string]bool, len(vm.Layers))
	for _, l := range vm.Layers {
		key := string(l.ID)
		known[key] = true
		if p, ok := a.promptEdits[key]; ok && *p == l.Content {
			delete(a.promptEdits, key)
		}
	}
	for key := range a.promptEdits {
		if !known[key] {
			delete(a.promptEdits, key)
		}
	}
	if len(a.promptEdits) == 0 {
		a.promptConflict = false
	}
}

// promptWired reports whether a write channel is wired. Without one the editor
// still edits, but saving is disabled and the page says so instead of pretending.
func (a *App) promptWired() bool {
	s, ok := a.cmd.(SettingsSaver)
	return ok && s != nil
}

// savePrompts hands the changed layers to the runtime, with the revision the
// projection was read at. A save that fails while the published revision moved is
// a conflict, not a generic error: the operator is offered 重新加载 rather than
// overwriting the other window's change.
func (a *App) savePrompts(vm *view.PromptVM) {
	saver, ok := a.cmd.(SettingsSaver)
	if !ok || saver == nil {
		a.promptErr = "当前运行时不能保存设置：没有保存通道。"
		return
	}
	values := make(map[string]string, len(vm.Layers)+1)
	for _, l := range vm.Layers {
		if !l.Editable {
			continue
		}
		if p, ok := a.promptEdits[string(l.ID)]; ok && *p != l.Content {
			values[string(l.ID)] = *p
		}
	}
	if len(values) == 0 {
		a.promptErr = ""
		return
	}
	base := vm.Revision
	values[SettingsRevisionKey] = strconv.FormatUint(base, 10)
	rev, err := saver.SaveSettings(string(view.SettingsPrompt), values)
	if err != nil {
		if a.promptRevisionMoved(vm, rev) {
			a.promptConflict = true
			a.promptErr = ""
		} else {
			a.promptErr = err.Error()
		}
		return
	}
	a.promptErr = ""
	a.promptConflict = false
	a.promptReloaded = 0
	_ = rev // the edit stays until the published content matches it
}

// promptRevisionMoved reports that the configuration changed under the edits.
func (a *App) promptRevisionMoved(vm *view.PromptVM, saved uint64) bool {
	if cur := a.snap.Prompts(); cur != nil && cur.Revision != vm.Revision {
		return true
	}
	return saved != 0 && saved != vm.Revision
}

// reloadPrompts discards the local edits and reads the layers again: through the
// runtime's PromptReloader when it has one, else from the last published
// projection. It never keeps a conflict badge: after a reload the operator edits
// the current values, staler by nothing.
func (a *App) reloadPrompts() {
	rev := uint64(0)
	if r, ok := a.cmd.(PromptReloader); ok && r != nil {
		v, err := r.ReloadPrompts()
		if err != nil {
			a.promptErr = err.Error()
			return
		}
		rev = v
	}
	a.promptEdits = map[string]*string{}
	a.promptEditing = ""
	a.promptConflict = false
	a.promptErr = ""
	if rev == 0 {
		if vm := a.snap.Prompts(); vm != nil {
			rev = vm.Revision
		}
	}
	a.promptReloaded = rev
}

// promptEditLabel names the edit button of a layer, once per layer: three buttons
// all reading 编辑 would be one label for three different targets.
func promptEditLabel(id view.PromptLayerID) string {
	switch id {
	case view.PromptRouter:
		return "编辑 Router"
	case view.PromptWorker:
		return "编辑 Worker"
	default:
		return "编辑 规则说明"
	}
}
