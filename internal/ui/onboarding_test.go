package ui

import (
	"testing"
	"time"

	mui "github.com/egoist/mygo/ui"

	"github.com/zeokeel/super-agent/internal/view"
)

// fakeOnboarder is a runtime that can save the wizard and finish it.
type fakeOnboarder struct {
	fakeCmd
	saved  map[string]string
	finish int
	err    error
}

func (f *fakeOnboarder) SaveOnboarding(values map[string]string) (uint64, error) {
	f.saved = values
	return 1, f.err
}

func (f *fakeOnboarder) FinishOnboarding() error {
	f.finish++
	return f.err
}

func onboardingSnap(ready bool) *view.Snapshot {
	now := time.Now()
	snap := view.NewSnapshot()
	snap.PublishOnboarding(&view.OnboardingVM{
		Active: true, Ready: ready, Revision: 3, UpdatedAt: now,
		Steps: []view.OnboardingStep{
			{ID: view.OnboardModel, Title: "用哪个模型", Note: "研判和执行都靠它",
				Done: true,
				Fields: []view.OnboardField{
					{Key: "base_url", Label: "端点", Value: "https://api.example.com/v1", Kind: view.SettingText},
					{Key: "model", Label: "模型", Value: "deepseek-chat", Kind: view.SettingText},
				}},
			{ID: view.OnboardWorkspace, Title: "它管哪个仓库", Note: "一个工作区对应一个仓库",
				Fields: []view.OnboardField{
					{Key: "ws_name", Label: "名字", Kind: view.SettingText},
				}},
			{ID: view.OnboardSource, Title: "从哪里接活", Optional: true,
				Fields: []view.OnboardField{
					{Key: "source_kind", Label: "连接", Kind: view.SettingSelect,
						Options: []string{"local", "github"}},
				}},
		},
	})
	return snap
}

// A fresh install opens on the wizard and can save a step.
func TestOnboardingPageRendersAndSaves(t *testing.T) {
	cmd := &fakeOnboarder{}
	app := newApp(onboardingSnap(false), cmd)
	tt := mui.NewTester(app.view, 1440, 900)

	wantTexts(t, tt, "初次设置", "用哪个模型", "保存这一步")

	// Click "保存这一步" and check the step's values were handed to the runtime.
	click(t, tt, "保存这一步")
	if cmd.saved == nil {
		t.Fatal("the step was not saved")
	}
	if cmd.saved["model"] != "deepseek-chat" {
		t.Fatalf("wrong value saved: %+v", cmd.saved)
	}
}

// "开始使用" only exists once the required steps are done, so a half-configured
// agent cannot start taking work.
func TestOnboardingFinishOnlyWhenReady(t *testing.T) {
	notReady := newApp(onboardingSnap(false), &fakeOnboarder{})
	t1 := mui.NewTester(notReady.view, 1440, 900)
	if t1.HasText("开始使用") {
		t.Fatal("the finish button must not appear before the required steps are done")
	}

	cmd := &fakeOnboarder{}
	ready := newApp(onboardingSnap(true), cmd)
	t2 := mui.NewTester(ready.view, 1440, 900)
	wantTexts(t, t2, "开始使用")
	click(t, t2, "开始使用")
	if cmd.finish != 1 {
		t.Fatalf("finish called %d times", cmd.finish)
	}
}

// Without a runtime that can save, the wizard says so instead of pretending.
func TestOnboardingWithoutSaver(t *testing.T) {
	app := newApp(onboardingSnap(false), &fakeCmd{})
	tt := mui.NewTester(app.view, 1440, 900)
	click(t, tt, "保存这一步")
	wantTexts(t, tt, "暂时不可用：运行时还没有接入保存设置")
}
