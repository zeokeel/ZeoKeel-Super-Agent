package ui

import (
	"testing"
	"time"

	mui "github.com/egoist/mygo/ui"

	"github.com/zeokeel/super-agent/internal/view"
	"github.com/zeokeel/super-agent/internal/view/viewtest"
)

// artifactsSnap publishes the page the panel needs, with two workspaces so the
// filter chips have something to do.
func artifactsSnap(rows ...view.ArtifactRow) *view.Snapshot {
	now := time.Now()
	snap := view.NewSnapshot()
	snap.PublishOverview(viewtest.DemoOverview(now))
	snap.PublishArtifacts(&view.ArtifactVM{Rows: rows, Total: len(rows), UpdatedAt: now})
	return snap
}

// The overview's 全部 link must open something. It used to be a label that looked
// clickable and did nothing (design §6.10).
func TestArtifactsPanelOpensFromTheOverview(t *testing.T) {
	app := newApp(artifactsSnap(
		view.ArtifactRow{Title: "hello.txt", Kind: "file", Workspace: "ws_web",
			Ticket: "#1", At: time.Now()},
		view.ArtifactRow{Title: "usage.md", Kind: "report", Workspace: "ws_acme_data",
			Ticket: "#2", At: time.Now()},
	), &fakeCmd{})
	tt := mui.NewTester(app.view, 1440, 1400)

	click(t, tt, "全部")
	wantTexts(t, tt, "交付物", "2 个", "hello.txt", "usage.md", "ws_web")
}

// With nothing produced yet the panel says so rather than showing an empty box.
func TestArtifactsPanelEmptyState(t *testing.T) {
	app := newApp(artifactsSnap(), &fakeCmd{})
	tt := mui.NewTester(app.view, 1440, 1400)
	click(t, tt, "全部")
	wantTexts(t, tt, "交付物", "还没有交付物。工单产出后会出现在这里。")
}

// Filter chips narrow by workspace, the way a person scans a month of output.
func TestArtifactsPanelFiltersByWorkspace(t *testing.T) {
	app := newApp(artifactsSnap(
		view.ArtifactRow{Title: "hello.txt", Kind: "file", Workspace: "ws_web",
			Ticket: "#1", At: time.Now()},
		view.ArtifactRow{Title: "usage.md", Kind: "report", Workspace: "ws_acme_data",
			Ticket: "#2", At: time.Now()},
	), &fakeCmd{})
	tt := mui.NewTester(app.view, 1440, 1400)
	click(t, tt, "全部")
	wantTexts(t, tt, "hello.txt", "usage.md")

	click(t, tt, "ws_web")
	if !tt.HasText("hello.txt") {
		t.Fatalf("the selected workspace lost its row: %q", tt.Texts())
	}
	if tt.HasText("usage.md") {
		t.Fatalf("the filter did not narrow: %q", tt.Texts())
	}
}
