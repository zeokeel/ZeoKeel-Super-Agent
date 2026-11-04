package ui

import (
	"fmt"
	"strings"
	"testing"

	mui "github.com/egoist/mygo/ui"

	"github.com/zeokeel/super-agent/internal/view"
	"github.com/zeokeel/super-agent/internal/view/viewtest"
)

// fakePromptSaver is a settings writer that can also answer a reload: it stands in
// for a runtime that owns the prompt layers.
type fakePromptSaver struct {
	fakeCmd
	snap *view.Snapshot

	group  string
	values map[string]string
	rev    uint64
	err    error
	// conflictRev is what a failed save reports as the current revision: the
	// runtime's optimistic-concurrency answer.
	conflictRev uint64

	reloadNew   *view.PromptVM
	reloadErr   error
	reloadCalls int
}

func (f *fakePromptSaver) SaveSettings(group string, values map[string]string) (uint64, error) {
	f.group, f.values = group, values
	if f.err != nil {
		return f.conflictRev, f.err
	}
	f.rev++
	return f.rev, nil
}

func (f *fakePromptSaver) ReloadPrompts() (uint64, error) {
	f.reloadCalls++
	if f.reloadErr != nil {
		return 0, f.reloadErr
	}
	if f.reloadNew != nil && f.snap != nil {
		f.snap.PublishPrompts(f.reloadNew)
	}
	rev := f.rev
	if f.reloadNew != nil {
		rev = f.reloadNew.Revision
	}
	return rev, nil
}

// promptApp is the settings page with the prompt editor open and a writer wired.
func promptApp(t *testing.T) (*App, *fakePromptSaver, *mui.Tester) {
	t.Helper()
	a := pageApp(t)
	saver := &fakePromptSaver{snap: a.snap, rev: 7}
	a.cmd = saver
	tt := mui.NewTester(a.view, 1440, 1400)
	click(t, tt, "设置")
	click(t, tt, "提示词")
	if a.settingsGroup != view.SettingsPrompt {
		t.Fatalf("rail chose %q, want prompt", a.settingsGroup)
	}
	return a, saver, tt
}

// The editor starts clean, editing one layer shows the group's one save bar, and
// 保存 hands the changed layer plus the base revision to the runtime. The edit is
// not cleared by the save itself: it waits for the published content (no
// optimistic update).
func TestPromptEditorEditsSavesAndShowsBarOnlyWhenDirty(t *testing.T) {
	a, saver, tt := promptApp(t)

	wantTexts(t, tt, "Router 提示词", "Worker 提示词", "评估规则说明", "编辑 Router")
	if tt.HasText("处未保存更改") {
		t.Fatalf("a clean group must not show the save bar; texts: %q", tt.Texts())
	}

	// Open the Router layer: it becomes an editor with 完成编辑 and 恢复默认.
	click(t, tt, "编辑 Router")
	wantTexts(t, tt, "完成编辑", "恢复默认")
	tt.Type("补丁")
	ptr, ok := a.promptEdits[string(view.PromptRouter)]
	if !ok || !strings.Contains(*ptr, "补丁") {
		t.Fatalf("typing did not reach the layer edit: %v", a.promptEdits)
	}
	tt.Frame()
	wantTexts(t, tt, "1 处未保存更改", "保存")

	click(t, tt, "保存")
	if saver.group != string(view.SettingsPrompt) {
		t.Fatalf("saved group %q, want prompt", saver.group)
	}
	if got := saver.values[string(view.PromptRouter)]; !strings.Contains(got, "补丁") {
		t.Fatalf("saved router %q", got)
	}
	if got := saver.values[SettingsRevisionKey]; got != "7" {
		t.Fatalf("save carried revision %q, want the base 7", got)
	}
	if _, ok := saver.values[string(view.PromptWorker)]; ok {
		t.Fatalf("an unchanged layer must not be written: %v", saver.values)
	}
	if a.promptDirty(a.snap.Prompts()) != 1 {
		t.Fatal("the edit must stay until the child publishes it")
	}

	// The new projection clears the bar.
	next := viewtest.DemoPrompts(a.now)
	next.Revision = 8
	next.Layers[0].Revision = 8
	next.Layers[0].Content = saver.values[string(view.PromptRouter)]
	a.snap.PublishPrompts(next)
	tt.Frame()
	if a.promptDirty(a.snap.Prompts()) != 0 {
		t.Fatalf("dirty = %d after the new projection, want 0", a.promptDirty(a.snap.Prompts()))
	}
	if tt.HasText("处未保存更改") {
		t.Fatalf("the save bar should be gone; texts: %q", tt.Texts())
	}
}

// A save that fails while the configuration moved is a conflict, not a generic
// error: the page says 别人改过了 and offers 重新加载, which really replaces the
// edits with the runtime's current layers.
func TestPromptSaveConflictOffersReload(t *testing.T) {
	a, saver, tt := promptApp(t)
	saver.err = fmt.Errorf("runtime: 设置已被其他窗口改过（当前 rev 9），请重新加载")
	saver.conflictRev = 9

	click(t, tt, "编辑 Worker")
	tt.Type("改一下")
	tt.Frame()
	click(t, tt, "保存")
	if !a.promptConflict {
		t.Fatal("a save whose revision moved must be reported as a conflict")
	}
	wantTexts(t, tt, "提示词已被别人改过", "重新加载")

	// The runtime republishes at the revision the conflict named: 重新加载 asks it,
	// so the editor really reads the new layers instead of clearing a flag.
	next := viewtest.DemoPrompts(a.now)
	next.Revision = 9
	next.Layers[1].Revision = 9
	next.Layers[1].Content = "worker v9：只读模式"
	saver.reloadNew = next

	click(t, tt, "重新加载")
	if saver.reloadCalls != 1 {
		t.Fatalf("reload calls %d, want 1", saver.reloadCalls)
	}
	if a.promptConflict || len(a.promptEdits) != 0 {
		t.Fatalf("reload must drop the conflict and the edits: %v", a.promptEdits)
	}
	wantTexts(t, tt, "已重新加载（rev 9）", "worker v9：只读模式")
}

// Without a PromptReloader the reload still re-reads the last published projection:
// it says which revision it shows instead of pretending it re-read the source.
func TestPromptReloadFallsBackToThePublishedProjection(t *testing.T) {
	// A writer that cannot answer a reload: the page then re-reads the last
	// published projection, and says which revision it shows.
	a := pageApp(t)
	a.cmd = &fakeSaver{rev: 7}
	tt := mui.NewTester(a.view, 1440, 1400)
	click(t, tt, "设置")
	click(t, tt, "提示词")
	next := viewtest.DemoPrompts(a.now)
	next.Revision = 10
	next.Layers[0].Content = "router v10：客户数据一律 proposal"
	a.snap.PublishPrompts(next)
	a.promptConflict = true
	p := "旧编辑"
	a.promptEdits[string(view.PromptRouter)] = &p
	tt.Frame()
	wantTexts(t, tt, "提示词已被别人改过")

	click(t, tt, "重新加载")
	if a.promptConflict || len(a.promptEdits) != 0 {
		t.Fatalf("reload must drop the conflict and the edits: %v", a.promptEdits)
	}
	wantTexts(t, tt, "已重新加载（rev 10）", "router v10：客户数据一律 proposal")
}

// A runtime without SettingsSaver must be visible, not silent: the editor still
// edits, the save is disabled, and both the group and the bar say why.
func TestPromptEditorSaysWhenUnwired(t *testing.T) {
	a := pageApp(t) // fakeCmd does not implement SettingsSaver
	tt := mui.NewTester(a.view, 1440, 1400)
	click(t, tt, "设置")
	click(t, tt, "提示词")
	wantTexts(t, tt, "暂时不可用：运行时还没有接入保存设置")

	click(t, tt, "编辑 Router")
	tt.Type("x")
	tt.Frame()
	wantTexts(t, tt, "1 处未保存更改", "暂时不可用：运行时还没有接入保存设置")
	click(t, tt, "保存")
	if !tt.HasText("暂时不可用：运行时还没有接入保存设置：没有保存通道。") {
		t.Fatalf("a disabled save must say why; texts: %q", tt.Texts())
	}
	if a.promptDirty(a.snap.Prompts()) != 1 {
		t.Fatal("an unwired save must not drop the edit")
	}
}

// 恢复默认 writes the built-in value into the edit, so the diff and the save bar
// show it before anything is written: the operator sees what will be saved.
func TestPromptRestoreDefaultBecomesAnEdit(t *testing.T) {
	a, _, tt := promptApp(t)
	click(t, tt, "编辑 Router")
	click(t, tt, "恢复默认")
	ptr, ok := a.promptEdits[string(view.PromptRouter)]
	if !ok {
		t.Fatal("恢复默认 must create an edit")
	}
	if def := a.snap.Prompts().Layer(view.PromptRouter).Default; *ptr != def {
		t.Fatalf("edit %q, want the default %q", *ptr, def)
	}
	tt.Frame()
	wantTexts(t, tt, "1 处未保存更改")
}
