package ui

import (
	"testing"
	"time"

	mui "github.com/egoist/mygo/ui"

	"github.com/zeokeel/super-agent/internal/view"
	"github.com/zeokeel/super-agent/internal/view/viewtest"
)

// Quitting while an executor is running must show why the window is still there,
// and must not block the UI thread while it waits.
func TestQuitDrainsBeforeLeaving(t *testing.T) {
	snap := view.NewSnapshot()
	snap.PublishOverview(viewtest.DemoOverview(time.Now()))
	released := make(chan struct{})
	drained := false

	app := newApp(snap, &fakeCmd{})
	app.opt = Options{Drain: func(time.Duration) bool {
		<-released
		drained = true
		return true
	}}

	// The first Quit asks the drain to run and returns without quitting.
	app.Quit()
	if !app.draining {
		t.Fatal("quit did not start draining")
	}
	if app.quit {
		t.Fatal("the app left before the executor finished")
	}

	tt := mui.NewTester(app.view, 1200, 800)
	wantTexts(t, tt, "正在等执行器收尾，完成后自动退出…")

	close(released)
	// Rendering after the drain completes is what issues the quit, on the UI thread.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && !app.quit {
		tt = mui.NewTester(app.view, 1200, 800)
		time.Sleep(20 * time.Millisecond)
	}
	if !drained {
		t.Fatal("the drain never ran")
	}
	if !app.quit {
		t.Fatal("the app never quit after the drain finished")
	}
}

// Without a drain the app leaves at once: headless runs and tests must not wait.
func TestQuitWithoutDrainIsImmediate(t *testing.T) {
	snap := view.NewSnapshot()
	app := newApp(snap, &fakeCmd{})
	app.Quit()
	if !app.quit {
		t.Fatal("without a drain, quit must be immediate")
	}
}
