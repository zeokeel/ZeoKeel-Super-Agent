package ui

import (
	"testing"

	mui "github.com/egoist/mygo/ui"

	"github.com/zeokeel/super-agent/internal/view"
	"github.com/zeokeel/super-agent/internal/view/viewtest"
)

// A token count reads with thousands separators, from humanize.Comma: the panel
// no longer has its own grouping code (audit N-10).
func TestTokenTotalIsGrouped(t *testing.T) {
	a := newTestApp(t, false)
	ov := viewtest.DemoOverview(a.now)
	ov.Usage = view.Usage{Calls: 3, Total: 1204331, Prompt: 1000000, Cached: 900000}
	a.snap.PublishOverview(ov)
	tt := mui.NewTester(a.view, 1440, 900)
	wantTexts(t, tt, "1,204,331", "3 次调用")
}

// Whitespace is trimmed by strings.TrimSpace, which knows what a Chinese IME
// inserts: an input holding only ideographic spaces must not dispatch a task
// (audit N-10: the hand-rolled trimmer knew four ASCII bytes).
func TestInputTrimsFullWidthSpace(t *testing.T) {
	cmd := &fakeCmd{}
	a := newTestApp(t, false)
	a.cmd = cmd
	a.draft = "\u3000\u3000"
	_ = a
	a.mode = "home"
	tt := mui.NewTester(a.view, 1440, 900)
	if err := tt.Click("发送"); err != nil {
		t.Fatalf("click 发送: %v", err)
	}
	if len(cmd.got) != 0 {
		t.Fatalf("a blank draft dispatched %q", cmd.got)
	}
}

// The same trimmer keeps the rest of the text: only the ends are whitespace.
func TestInputTrimsEndsButKeepsTheTask(t *testing.T) {
	cmd := &fakeCmd{}
	a := newTestApp(t, false)
	a.cmd = cmd
	a.draft = " \u3000 把登录超时改成可配置 \u3000 "
	_ = a
	a.mode = "home"
	tt := mui.NewTester(a.view, 1440, 900)
	if err := tt.Click("发送"); err != nil {
		t.Fatalf("click 发送: %v", err)
	}
	if len(cmd.got) != 1 || cmd.got[0] != "把登录超时改成可配置" {
		t.Fatalf("dispatched %q", cmd.got)
	}
}

// exactTexts counts the elements whose whole text is s. HasText and Click match
// loosely or exactly respectively; a test that means "this button exists" wants
// the exact count, since a label and its text both register.
func exactTexts(tt *mui.Tester, s string) int {
	n := 0
	for _, text := range tt.Texts() {
		if text == s {
			n++
		}
	}
	return n
}
