package ui

import (
	"sync/atomic"
	"testing"
	"time"

	mui "github.com/egoist/mygo/ui"

	"github.com/zeokeel/super-agent/internal/view"
)

func TestQuitDrainsBeforeLeaving(t *testing.T) {
	a := pageApp(t)
	var drained atomic.Bool
	a.opt.Drain = func(timeout time.Duration) bool { drained.Store(true); return true }
	a.Quit()
	if !a.draining {
		t.Fatal("quit must wait for the executor drain")
	}
	deadline := time.Now().Add(time.Second)
	for !drained.Load() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !drained.Load() {
		t.Fatal("the drain never ran")
	}
	// The finished drain is what actually leaves the app.
	tt := mui.NewTester(a.view, 1440, 900)
	tt.Frame()
	if !a.quit {
		t.Fatal("a finished drain must quit")
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
