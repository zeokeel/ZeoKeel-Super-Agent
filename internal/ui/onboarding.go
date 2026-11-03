package ui

import (
	"fmt"

	mui "github.com/egoist/mygo/ui"

	"github.com/zeokeel/super-agent/internal/view"
)

// OnboardingSaver is the optional write channel of the first-run wizard. A runtime
// without it shows the form read-only and says so, rather than collecting answers
// it cannot keep.
type OnboardingSaver interface {
	SaveOnboarding(values map[string]string) (uint64, error)
	FinishOnboarding() error
}

// onboardingState is the wizard's cross-frame state: which step is open and what
// the person has typed. Only strings and indexes cross frames (design §6.1).
type onboardingState struct {
	sel   int
	edits map[string]*string
	err   string
}

// edit returns the buffer for one field, so typing survives frames without
// storing an element.
func (a *App) onboardingEdit(key, current string) *string {
	if a.onb == nil {
		a.onb = &onboardingState{edits: map[string]*string{}}
	}
	if s, ok := a.onb.edits[key]; ok {
		return s
	}
	v := current
	a.onb.edits[key] = &v
	return &v
}

// onboardingPage draws the first-run wizard. It owns the window until the
// required steps are filled in: a fresh install has nothing else worth showing.
func (a *App) onboardingPage(c *mui.Context, vm *view.OnboardingVM) {
	tk := a.tk
	done, total := vm.DoneStepCount()

	if a.onb == nil {
		a.onb = &onboardingState{edits: map[string]*string{}}
	}
	if a.onb.sel >= len(vm.Steps) {
		a.onb.sel = 0
	}
	if a.onb.sel < 0 {
		a.onb.sel = 0
	}
	cur := vm.Steps[a.onb.sel]

	mui.Column(c).Fill().Background(tk.Bg).Children(func() {
		// Header: what this is, and how far along the setup is.
		mui.Row(c).Padding(28, 32, 12, 32).AlignItems(mui.Center).Children(func() {
			mui.Column(c).Children(func() {
				mui.Text(c, "初次设置").FontSize(24).Bold().TextColor(tk.Text)
				mui.Text(c, "回答几个问题就能开始派活。之后都能在设置页改。").
					FontSize(12).TextColor(tk.TextMuted)
			})
			mui.Spacer(c)
			textChip(c, tk, fmt.Sprintf("%d / %d 已完成", done, total))
		})

		mui.Row(c).Grow(1).AlignItems(mui.Stretch).Padding(0, 32, 0, 32).Children(func() {
			// Step rail: clicking a step opens it; a finished step says so.
			mui.Column(c).Width(220).Gap(2).Children(func() {
				for i, s := range vm.Steps {
					i, s := i, s
					active := i == a.onb.sel
					bg := tk.Surface
					if active {
						bg = tk.SurfaceAlt
					}
					mui.Row(c).Padding(9, 10).Radius(7).Background(bg).Gap(8).
						AlignItems(mui.Center).OnClick(func() { a.onb.sel = i }).Children(func() {
						mark := "○"
						if s.Done {
							mark = "●"
						}
						mui.Text(c, mark).FontSize(11).TextColor(ifColor(s.Done, tk.Green, tk.TextFaint))
						mui.Text(c, s.Title).FontSize(12).TextColor(tk.Text)
						if s.Optional {
							mui.Text(c, "可选").FontSize(9).TextColor(tk.TextFaint)
						}
					})
				}
			})
			mui.Box(c).Width(1).Background(tk.Border)
			// The open step's card.
			mui.Column(c).Grow(1).MinWidth(0).Padding(20, 24).Children(func() {
				mui.Column(c).Width(560).Gap(14).Children(func() {
					mui.Text(c, cur.Title).FontSize(16).Bold().TextColor(tk.Text)
					mui.Text(c, cur.Note).FontSize(12).TextColor(tk.TextMuted)
					for _, f := range cur.Fields {
						f := f
						p := a.onboardingEdit(string(cur.ID)+"."+f.Key, f.Value)
						a.formField(c, f.Label, f.Help, f.Kind, p, f.Options)
					}
					if a.onb.err != "" {
						mui.Text(c, a.onb.err).FontSize(11).TextColor(tk.Red)
					}
					mui.Row(c).Gap(10).AlignItems(mui.Center).Children(func() {
						btn := func(label string, primary bool, fn func()) {
							mui.Row(c).Padding(7, 14).Radius(7).
								Background(ifColor(primary, tk.Accent, tk.SurfaceAlt)).
								OnClick(fn).Children(func() {
								mui.Text(c, label).FontSize(12).
									TextColor(ifColor(primary, mui.Hex("#FFFFFF"), tk.Text))
							})
						}
						btn("保存这一步", true, func() {
							vals := map[string]string{}
							for _, f := range cur.Fields {
								vals[f.Key] = a.onboardingValue(string(cur.ID) + "." + f.Key)
							}
							if saver, ok := a.cmd.(OnboardingSaver); ok {
								if _, err := saver.SaveOnboarding(vals); err != nil {
									a.onb.err = err.Error()
								} else {
									a.onb.err = ""
									if a.onb.sel < len(vm.Steps)-1 {
										a.onb.sel++
									}
								}
								return
							}
							a.onb.err = "当前运行时不能保存设置：没有保存通道"
						})
						if cur.Optional {
							btn("跳过", false, func() {
								if a.onb.sel < len(vm.Steps)-1 {
									a.onb.sel++
								}
							})
						}
						mui.Spacer(c)
						if vm.Ready {
							btn("开始使用", false, func() {
								if saver, ok := a.cmd.(OnboardingSaver); ok {
									if err := saver.FinishOnboarding(); err != nil {
										a.onb.err = err.Error()
									}
								}
							})
						} else {
							mui.Text(c, "还有必填项没完成").FontSize(11).TextColor(tk.TextFaint)
						}
					})
				})
			})
		})
	})
}

// onboardingValue reads the buffer for one field.
func (a *App) onboardingValue(key string) string {
	if a.onb == nil {
		return ""
	}
	if s, ok := a.onb.edits[key]; ok {
		return *s
	}
	return ""
}

// formField draws one wizard input. It mirrors settingField but takes a plain
// buffer, because the wizard is not a settings group.
func (a *App) formField(c *mui.Context, label, help string, kind view.SettingKind, ptr *string, options []string) {
	tk := a.tk
	mui.Column(c).Gap(4).Children(func() {
		mui.Row(c).Gap(6).AlignItems(mui.Center).Children(func() {
			mui.Text(c, label).FontSize(11).TextColor(tk.TextMuted)
			if help != "" {
				mui.Text(c, "· "+help).FontSize(10).SingleLine().TextColor(tk.TextFaint)
			}
		})
		if kind == view.SettingSelect && len(options) > 0 {
			mui.Row(c).Gap(6).Children(func() {
				for _, o := range options {
					o := o
					active := *ptr == o
					mui.Row(c).Padding(5, 10).Radius(6).
						Background(ifColor(active, tk.SurfaceAlt, tk.Surface)).
						OnClick(func() { *ptr = o }).Children(func() {
						mui.Text(c, o).FontSize(11).TextColor(ifColor(active, tk.Text, tk.TextMuted))
					})
				}
			})
			return
		}
		field := mui.TextInput(c.Key("onb:"+label), ptr).Height(30).FontSize(12)
		if kind == view.SettingSecret {
			field.Password()
		}
	})
}
