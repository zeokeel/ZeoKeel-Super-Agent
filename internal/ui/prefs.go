package ui

// Desktop accessibility preferences, consumed where mygo exposes them:
// TextScale (system text size), ReduceMotion, HighContrast and the system Accent.
// The panel's own ui.font_scale multiplies with the system text scale; motion is
// disabled when the desktop asks for less; high contrast strengthens borders and
// text; the system accent wins when the desktop has one.

import (
	"github.com/egoist/mygo"
	mui "github.com/egoist/mygo/ui"
)

// uiPrefs is the subset of mygo's ThemePreferences the panel uses.
type uiPrefs struct {
	TextScale    float64
	ReduceMotion bool
	HighContrast bool
	Accent       mui.Color
}

// readUIPrefs reads the desktop's preferences. A headless run (tests) has no
// desktop: it gets the defaults, and tests replace this seam to fake them.
var readUIPrefs = func(a *App) uiPrefs {
	if a.win == nil || !mygo.App.IsReady() {
		return uiPrefs{TextScale: 1}
	}
	p := mygo.Theme.Preferences()
	return uiPrefs{
		TextScale:    p.TextScale,
		ReduceMotion: p.ReduceMotion,
		HighContrast: p.HighContrast,
		Accent:       mui.RGBA(p.Accent.R, p.Accent.G, p.Accent.B, float32(p.Accent.A)/255),
	}
}

// systemAccent reports a usable accent color (opaque enough to read as a color).
func systemAccent(c mui.Color) bool { return c.A > 0 }

// applyPrefs applies the accessibility preferences to the frame's tokens.
func (a *App) applyPrefs(p uiPrefs) {
	a.reduceMotion = p.ReduceMotion
	a.highContrast = p.HighContrast
	if !a.highContrast {
		return
	}
	// High contrast: borders become as strong as secondary text and secondary
	// text becomes primary, which is what the audit's contrast failures need.
	a.tk.Border = a.tk.TextMuted
	a.tk.BorderSoft = a.tk.TextMuted
	a.tk.TextMuted = a.tk.Text
	a.tk.TextFaint = a.tk.Text
}

// accentPrefs applies the system accent over the brand accent when present.
func (a *App) accentPrefs(p uiPrefs) {
	if systemAccent(p.Accent) {
		a.tk.Accent = p.Accent
	}
}
