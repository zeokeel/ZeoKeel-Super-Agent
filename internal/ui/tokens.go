package ui

import mui "github.com/egoist/mygo/ui"

// Tokens are the colors and radii the pages draw with. They derive from the
// frame's theme instead of being a second copy of it: the colors mygo names
// (background, surface, border, text, accent, success/warning/danger) are read
// back from *mui.Theme, which follows the system's appearance, so dark mode
// moves them. Only the brand colors mygo has no name for (soft tints, the code
// surface) live here, in a light and a dark set.
//
// design §6.5: derive our token layer from ui.LightTheme()/DarkTheme(), never
// scatter hard-coded colors.
type Tokens struct {
	Bg          mui.Color
	Surface     mui.Color
	SurfaceAlt  mui.Color
	Border      mui.Color
	BorderSoft  mui.Color
	Text        mui.Color
	TextMuted   mui.Color
	TextFaint   mui.Color
	Accent      mui.Color
	AccentSoft  mui.Color
	Cyan        mui.Color
	Violet      mui.Color
	VioletSoft  mui.Color
	Green       mui.Color
	GreenSoft   mui.Color
	Amber       mui.Color
	AmberSoft   mui.Color
	Red         mui.Color
	RedSoft     mui.Color
	CodeBg      mui.Color
	CodeText    mui.Color
	Radius      float32
	RadiusSmall float32
}

const (
	// cardRadius is the radius of a card; a control keeps the theme's Radius,
	// which is smaller. These were two numbers written in two places that had
	// drifted apart (12 beside 8) with nothing saying which was meant where.
	cardRadius = 12
	// controlRadius is what the theme gives controls, and what Tokens.RadiusSmall
	// reports.
	controlRadius = 8
	// baseFontSize and baseSpacing are the panel's scale, on mygo's units.
	baseFontSize = 15
	baseSpacing  = 4
	// minViewScale/maxViewScale bound the ui.font_scale setting.
	minViewScale = 0.8
	maxViewScale = 1.4
)

// viewScale is the panel's one text scale, read from the ui.font_scale setting
// (0.9 / 1.0 / 1.1 today). It replaces the magic "+2 at every number": the
// explicit sizes go through fs(), and the theme's FontSize through here.
var viewScale float32 = 1

// fs scales one explicit text size: the design's number times the panel's scale.
func fs(n float32) float32 { return n * viewScale }

// ViewScale is the scale the last frame used (for tests and the settings page).
func ViewScale() float32 { return viewScale }

// clampViewScale keeps a hand-edited setting from making the panel unusable.
func clampViewScale(f float32) float32 {
	if f < minViewScale {
		return minViewScale
	}
	if f > maxViewScale {
		return maxViewScale
	}
	return f
}

// brand is ZeoKeel's palette around mygo's theme: the colors the framework has
// no field for, in the two appearances. brandFor picks one, theme applies it to
// a base theme, and tokensFrom reads the shared colors back from the result, so
// there is one palette rather than a light build and a dark build that drift.
type brand struct {
	bg, surface, surfaceHover, surfacePressed      mui.Color
	surfaceAlt, border, borderSoft                 mui.Color
	text, textMuted, textFaint                     mui.Color
	accent, accentHover, accentPressed, accentText mui.Color
	accentSoft                                     mui.Color
	cyan, violet, violetSoft                       mui.Color
	green, greenSoft, amber, amberSoft             mui.Color
	red, redSoft                                   mui.Color
	codeBg, codeText                               mui.Color
	selection, focus                               mui.Color
}

// brandFor is the logo's cyan/blue accent (#0946EF..#37E7FE) with violet, in
// the appearance asked for.
func brandFor(dark bool) brand {
	if dark {
		return brand{
			bg:             mui.Hex("#0B0F19"),
			surface:        mui.Hex("#131A2A"),
			surfaceHover:   mui.Hex("#1A2336"),
			surfacePressed: mui.Hex("#222C42"),
			surfaceAlt:     mui.Hex("#182134"),
			border:         mui.Hex("#26314A"),
			borderSoft:     mui.Hex("#1E2740"),
			text:           mui.Hex("#E7ECF5"),
			textMuted:      mui.Hex("#98A3B8"),
			textFaint:      mui.Hex("#6B7891"),
			accent:         mui.Hex("#4D74FF"),
			accentHover:    mui.Hex("#6B8CFF"),
			accentPressed:  mui.Hex("#3A5FE0"),
			accentText:     mui.Hex("#FFFFFF"),
			accentSoft:     mui.Hex("#1B2A55"),
			cyan:           mui.Hex("#35C4DE"),
			violet:         mui.Hex("#9B82FF"),
			violetSoft:     mui.Hex("#241F45"),
			green:          mui.Hex("#34C08B"),
			greenSoft:      mui.Hex("#12352A"),
			amber:          mui.Hex("#E0A44A"),
			amberSoft:      mui.Hex("#3A2A12"),
			red:            mui.Hex("#F0665B"),
			redSoft:        mui.Hex("#3A1B1B"),
			codeBg:         mui.Hex("#0A0F1E"),
			codeText:       mui.Hex("#D6DEEB"),
			selection:      mui.RGBA(77, 116, 255, 0.35),
			focus:          mui.RGBA(77, 116, 255, 0.55),
		}
	}
	return brand{
		bg:             mui.Hex("#F5F6F8"),
		surface:        mui.Hex("#FFFFFF"),
		surfaceHover:   mui.Hex("#F0F2F5"),
		surfacePressed: mui.Hex("#E8EBEF"),
		surfaceAlt:     mui.Hex("#FAFBFC"),
		border:         mui.Hex("#E4E7EC"),
		borderSoft:     mui.Hex("#EEF1F4"),
		text:           mui.Hex("#101828"),
		textMuted:      mui.Hex("#667085"),
		textFaint:      mui.Hex("#98A2B3"),
		accent:         mui.Hex("#2F5BFF"),
		accentHover:    mui.Hex("#1D45E8"),
		accentPressed:  mui.Hex("#1736C4"),
		accentText:     mui.Hex("#FFFFFF"),
		accentSoft:     mui.Hex("#EAF0FF"),
		cyan:           mui.Hex("#0EA5C6"),
		violet:         mui.Hex("#6D4AFF"),
		violetSoft:     mui.Hex("#F1EDFF"),
		green:          mui.Hex("#12805C"),
		greenSoft:      mui.Hex("#E7F6EF"),
		amber:          mui.Hex("#B54708"),
		amberSoft:      mui.Hex("#FEF3D6"),
		red:            mui.Hex("#D92D20"),
		redSoft:        mui.Hex("#FEECEB"),
		codeBg:         mui.Hex("#0F172A"),
		codeText:       mui.Hex("#E2E8F0"),
		selection:      mui.RGBA(47, 91, 255, 0.20),
		focus:          mui.RGBA(47, 91, 255, 0.45),
	}
}

// theme is the base theme with the brand's colors, keeping the base's
// appearance (dark, and the user's preferences) and giving controls its metrics.
// Widgets built inside Themed(c, theme) take their colors from it;
// Context.Theme reports it while they build.
func (b brand) theme(base *mui.Theme) *mui.Theme {
	if base == nil {
		base = mui.LightTheme()
	}
	t := *base
	t.Background = b.bg
	t.Surface = b.surface
	t.SurfaceHover = b.surfaceHover
	t.SurfacePressed = b.surfacePressed
	t.Border = b.border
	t.Text = b.text
	t.TextMuted = b.textMuted
	t.Accent = b.accent
	t.AccentHover = b.accentHover
	t.AccentPressed = b.accentPressed
	t.AccentText = b.accentText
	t.Danger = b.red
	t.Warning = b.amber
	t.Success = b.green
	t.Selection = b.selection
	t.Focus = b.focus
	t.Radius = controlRadius
	t.Spacing = baseSpacing
	t.FontSize = baseFontSize * viewScale
	return &t
}

// tokensFrom is the page-level token layer for a frame theme: the colors mygo
// names come from the theme itself, so dark mode cannot be forgotten here.
func tokensFrom(t *mui.Theme) Tokens {
	if t == nil {
		t = mui.LightTheme()
	}
	b := brandFor(t.Dark)
	return Tokens{
		Bg:          t.Background,
		Surface:     t.Surface,
		SurfaceAlt:  b.surfaceAlt,
		Border:      t.Border,
		BorderSoft:  b.borderSoft,
		Text:        t.Text,
		TextMuted:   t.TextMuted,
		TextFaint:   b.textFaint,
		Accent:      t.Accent,
		AccentSoft:  b.accentSoft,
		Cyan:        b.cyan,
		Violet:      b.violet,
		VioletSoft:  b.violetSoft,
		Green:       t.Success,
		GreenSoft:   b.greenSoft,
		Amber:       t.Warning,
		AmberSoft:   b.amberSoft,
		Red:         t.Danger,
		RedSoft:     b.redSoft,
		CodeBg:      b.codeBg,
		CodeText:    b.codeText,
		Radius:      cardRadius,
		RadiusSmall: t.Radius,
	}
}
