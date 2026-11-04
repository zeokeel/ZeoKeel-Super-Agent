package ui

import (
	"testing"

	mui "github.com/egoist/mygo/ui"
)

// The glossary is the prototype's (?): ⌘/ opens it, it carries both languages,
// and Escape closes it.
func TestHelpOpensWithShortcutAndShowsGlossary(t *testing.T) {
	a := pageApp(t)
	tt := mui.NewTester(a.view, 1440, 900)

	tt.Key(mui.Cmd, mui.KeySlash)
	if !a.helpOpen {
		t.Fatal("⌘/ did not open the glossary")
	}
	wantTexts(t, tt,
		"名词表", "Glossary",
		"Ticket", "工单", "Mandate", "授权", "Attention", "待人工决定",
		"accept 接单", "explicit 明确",
	)
	tt.Key(0, mui.KeyEscape)
	if a.helpOpen {
		t.Fatal("Escape did not close the glossary")
	}
	if tt.HasText("名词表") {
		t.Fatalf("the glossary is still drawn; texts: %q", tt.Texts())
	}
}

func TestHelpOpensFromThePanel(t *testing.T) {
	a := pageApp(t)
	tt := mui.NewTester(a.view, 1440, 900)
	click(t, tt, "超级Agent")
	click(t, tt, "术语表")
	if !a.helpOpen {
		t.Fatal("the panel's 术语表 button did not open the glossary")
	}
}
