package ui

import (
	"testing"

	mui "github.com/egoist/mygo/ui"

	"github.com/zeokeel/super-agent/internal/view"
	"github.com/zeokeel/super-agent/internal/view/viewtest"
)

func TestPersonCountAgreesAcrossTheSurfaces(t *testing.T) {
	a := pageApp(t)
	tt := mui.NewTester(a.view, 1440, 900)
	// The panel badge carries the one number.
	if n := a.attentionCount(); n != 3 {
		t.Fatalf("attention count %d, want 3", n)
	}
	click(t, tt, "超级Agent")
	wantTexts(t, tt, "需要你关注的只有 3 件事")
	// The navigation model is the same derivation (one count, no second one).
	if got := view.Nav(a.snap.Overview())[1].Count; got != 3 {
		t.Fatalf("nav badge says %d, want 3", got)
	}
}

// A runtime card must not read "正常" while a part of the system needs a person:
// its chip is the same derivation the health rows use (audit item 10).
func TestRuntimeCardFollowsHealth(t *testing.T) {
	a := newTestApp(t, false)
	ov := viewtest.DemoOverview(a.now)
	for i := range ov.Health {
		ov.Health[i].OK = true
	}
	a.snap.PublishOverview(ov)
	tt := mui.NewTester(a.view, 1440, 900)
	if tt.HasText("项需关注") {
		t.Fatalf("a healthy system shows an attention chip; texts: %q", tt.Texts())
	}

	// One part becomes unhealthy: the chip follows, and says the count.
	ov.Health[0].OK = false
	a.snap.PublishOverview(ov)
	tt.Frame()
	wantTexts(t, tt, "1 项需关注")
	if tt.HasText("MCP notion") && tt.HasText("0 项需关注") {
		t.Fatalf("stale health count; texts: %q", tt.Texts())
	}
}

// A nil overview answers with zero, as every other nil VM in the view does.
func TestNilOverviewPersonCount(t *testing.T) {
	var ov *view.OverviewVM
	if got := ov.PersonCount(); got != 0 {
		t.Fatalf("nil overview counts %d people items", got)
	}
	if got := view.Nav(nil)[1].Count; got != 0 {
		t.Fatalf("nil overview gave the nav a count of %d", got)
	}
}
