package ui

import (
	"testing"

	mui "github.com/egoist/mygo/ui"
)

// The panel follows the desktop's appearance: mygo reports light or dark through
// Context.Theme, and the brand derives from it every frame instead of hard-coding
// a light palette (audit N-9). Under dark mode the shared colors come from the
// theme and the brand tints switch to their dark set.
func TestPanelFollowsTheSystemAppearance(t *testing.T) {
	a := newTestApp(t, false)
	tt := mui.NewTester(a.view, 1440, 900)
	if a.theme == nil || a.theme.Dark {
		t.Fatalf("the default tester is light, theme=%+v", a.theme)
	}
	light := a.tk

	tt.SetDark(true)
	tt.Frame()
	if !a.theme.Dark {
		t.Fatal("the panel did not follow the system into dark mode")
	}
	if a.tk.Bg == light.Bg {
		t.Fatal("dark mode kept the light background")
	}
	if a.tk.Bg != a.theme.Background || a.tk.Surface != a.theme.Surface ||
		a.tk.Border != a.theme.Border || a.tk.Accent != a.theme.Accent {
		t.Fatalf("tokens stopped reading the frame theme: tokens=%+v theme=%+v", a.tk, a.theme)
	}
	if a.tk.SurfaceAlt == light.SurfaceAlt || a.tk.TextFaint == light.TextFaint {
		t.Fatal("the brand tints did not switch to their dark set")
	}

	// Back to light: the same frame code, the other palette.
	tt.SetDark(false)
	tt.Frame()
	if a.theme.Dark || a.tk.Bg != light.Bg {
		t.Fatalf("returning to light kept dark values: dark=%v bg=%v", a.theme.Dark, a.tk.Bg)
	}
}

// Cards and controls have different radii, and each has one source: the card's
// is the brand's constant, the control's is the theme's Radius, so the two
// cannot silently swap (they had drifted to 12 beside 8).
func TestTokenRadiiSeparateCardsFromControls(t *testing.T) {
	for _, dark := range []bool{false, true} {
		base := mui.LightTheme()
		if dark {
			base = mui.DarkTheme()
		}
		th := brandFor(dark).theme(base)
		tk := tokensFrom(th)
		if tk.Radius != cardRadius {
			t.Errorf("dark=%v: card radius %v, want %v", dark, tk.Radius, float32(cardRadius))
		}
		if tk.RadiusSmall != th.Radius || tk.RadiusSmall != controlRadius {
			t.Errorf("dark=%v: control radius %v, want the theme's %v", dark, tk.RadiusSmall, th.Radius)
		}
		if tk.Radius <= tk.RadiusSmall {
			t.Errorf("dark=%v: a card's radius %v must exceed a control's %v", dark, tk.Radius, tk.RadiusSmall)
		}
		if th.Background != brandFor(dark).bg || th.Text != brandFor(dark).text {
			t.Errorf("dark=%v: the brand theme lost its colors", dark)
		}
		if !dark && th.Dark {
			t.Error("the light brand theme claims to be dark")
		}
	}
}

// A nil theme is what a headless build could hand in: it must not panic.
func TestTokensFromNilTheme(t *testing.T) {
	tk := tokensFrom(nil)
	if tk.Bg == (mui.Color{}) || tk.Radius != cardRadius {
		t.Fatalf("nil theme gave unusable tokens: %+v", tk)
	}
}
