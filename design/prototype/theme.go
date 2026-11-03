package main

import "github.com/egoist/mygo/ui"

// Design tokens, derived from mygo's light theme with ZeoKeel's own choices.
// Colors follow the logo: cyan/blue accent (#0946EF..#37E7FE) with violet.
type Tokens struct {
	Bg          ui.Color
	Surface     ui.Color
	SurfaceAlt  ui.Color
	Border      ui.Color
	BorderSoft  ui.Color
	Text        ui.Color
	TextMuted   ui.Color
	TextFaint   ui.Color
	Accent      ui.Color
	AccentSoft  ui.Color
	Cyan        ui.Color
	Violet      ui.Color
	VioletSoft  ui.Color
	Green       ui.Color
	GreenSoft   ui.Color
	Amber       ui.Color
	AmberSoft   ui.Color
	Red         ui.Color
	RedSoft     ui.Color
	CodeBg      ui.Color
	CodeText    ui.Color
	Radius      float32
	RadiusSmall float32
}

func theme() *ui.Theme {
	t := ui.LightTheme()
	t.Background = ui.Hex("#F5F6F8")
	t.Surface = ui.Hex("#FFFFFF")
	t.SurfaceHover = ui.Hex("#F0F2F5")
	t.SurfacePressed = ui.Hex("#E8EBEF")
	t.Border = ui.Hex("#E4E7EC")
	t.Text = ui.Hex("#101828")
	t.TextMuted = ui.Hex("#667085")
	t.Accent = ui.Hex("#2F5BFF")
	t.AccentHover = ui.Hex("#1D45E8")
	t.AccentPressed = ui.Hex("#1736C4")
	t.AccentText = ui.Hex("#FFFFFF")
	t.Danger = ui.Hex("#D92D20")
	t.Warning = ui.Hex("#DC6803")
	t.Success = ui.Hex("#12805C")
	t.Selection = ui.RGBA(47, 91, 255, 0.20)
	t.Focus = ui.RGBA(47, 91, 255, 0.45)
	t.Radius = 8
	t.Spacing = 4
	t.FontSize = 13
	return t
}

func tokens() Tokens {
	return Tokens{
		Bg:          ui.Hex("#F5F6F8"),
		Surface:     ui.Hex("#FFFFFF"),
		SurfaceAlt:  ui.Hex("#FAFBFC"),
		Border:      ui.Hex("#E4E7EC"),
		BorderSoft:  ui.Hex("#EEF1F4"),
		Text:        ui.Hex("#101828"),
		TextMuted:   ui.Hex("#667085"),
		TextFaint:   ui.Hex("#98A2B3"),
		Accent:      ui.Hex("#2F5BFF"),
		AccentSoft:  ui.Hex("#EAF0FF"),
		Cyan:        ui.Hex("#0EA5C6"),
		Violet:      ui.Hex("#6D4AFF"),
		VioletSoft:  ui.Hex("#F1EDFF"),
		Green:       ui.Hex("#12805C"),
		GreenSoft:   ui.Hex("#E7F6EF"),
		Amber:       ui.Hex("#B54708"),
		AmberSoft:   ui.Hex("#FEF3D6"),
		Red:         ui.Hex("#D92D20"),
		RedSoft:     ui.Hex("#FEECEB"),
		CodeBg:      ui.Hex("#0F172A"),
		CodeText:    ui.Hex("#E2E8F0"),
		Radius:      12,
		RadiusSmall: 8,
	}
}
