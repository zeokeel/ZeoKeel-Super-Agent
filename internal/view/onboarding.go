package view

import (
	"fmt"
	"time"
)

// OnboardingStepID names one step of the first-run setup. A person only sees this
// once, so each step must be answerable without reading documentation.
type OnboardingStepID string

const (
	OnboardModel     OnboardingStepID = "model"     // 用哪个模型
	OnboardWorkspace OnboardingStepID = "workspace" // 它管哪个仓库/数据
	OnboardSource    OnboardingStepID = "source"    // 从哪里接活
	OnboardPeople    OnboardingStepID = "people"    // 谁可以让它干活
)

// OnboardField is one input of a step.
type OnboardField struct {
	Key     string
	Label   string
	Value   string
	Kind    SettingKind
	Help    string
	Options []string
}

// OnboardingStep is one card of the wizard. Done is derived from whether the
// values it needs are present, so the rail cannot show a step as finished while
// its form is still empty.
type OnboardingStep struct {
	ID    OnboardingStepID
	Title string
	Note  string
	// Fields are the inputs this step needs. A step with none is informational.
	Fields []OnboardField
	Done   bool
	// Optional steps can be skipped; the wizard still finishes.
	Optional bool
}

// OnboardingVM is everything the first-run screen draws.
type OnboardingVM struct {
	Steps []OnboardingStep
	// Ready is true when every required step is done: only then does "开始使用"
	// work, so a half-configured agent never starts taking work.
	Ready bool
	// Active is true while the wizard should own the window. A saved setup sets it
	// false without erasing the values, so settings can still show them.
	Active    bool
	Revision  uint64
	UpdatedAt time.Time
}

// Incomplete lists the required steps still missing, for the button's tooltip.
func (o *OnboardingVM) Incomplete() []string {
	if o == nil {
		return nil
	}
	var out []string
	for _, s := range o.Steps {
		if !s.Done && !s.Optional {
			out = append(out, s.Title)
		}
	}
	return out
}

// DoneStepCount is "3 / 4" in the header.
func (o *OnboardingVM) DoneStepCount() (done, total int) {
	if o == nil {
		return 0, 0
	}
	for _, s := range o.Steps {
		if s.Optional {
			continue
		}
		total++
		if s.Done {
			done++
		}
	}
	return done, total
}

// Field returns one field by key, for the page's form state.
func (s OnboardingStep) Field(key string) (OnboardField, bool) {
	for _, f := range s.Fields {
		if f.Key == key {
			return f, true
		}
	}
	return OnboardField{}, false
}

var _ = fmt.Sprintf
