package ui

import (
	"testing"

	mui "github.com/egoist/mygo/ui"

	"github.com/zeokeel/super-agent/internal/view"
	"github.com/zeokeel/super-agent/internal/view/viewtest"
)

// 待处理 is one number: the topbar chip, the sidebar badge and the overview's
// heading all read view.OverviewVM.PersonCount, so they cannot disagree (audit
// F-04 / design §6.5 "诚实状态").
func TestPersonCountAgreesAcrossTheSurfaces(t *testing.T) {
	a := newTestApp(t, false)
	ov := viewtest.DemoOverview(a.now)
	ov.NeedsPerson = ov.NeedsPerson[:2]
	ov.Approvals = []view.ApprovalRow{{
		DecisionID: "d-9", From: "张三", Source: "钉钉", Said: "把用量发我",
		Intent: "跑 acme 的用量查询", Deliverable: "report.md", Workspace: "acme/data",
	}}
	a.snap.PublishOverview(ov)

	tt := mui.NewTester(a.view, 1440, 900)
	// Two Tickets wait for a person and one request waits for approval: three on
	// the chip, three in the heading, and the nav badge that links to this page.
	wantTexts(t, tt, "待你决定 3", "需要你关注的只有 3 件事")
	if got := view.Nav(a.snap.Overview())[1].Count; got != 3 {
		t.Fatalf("the sidebar badge says %d, the chip says 3", got)
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
