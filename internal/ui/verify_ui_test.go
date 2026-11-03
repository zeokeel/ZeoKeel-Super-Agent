package ui

// verifier-A: independent falsification of the Wave-1 UI claims that are
// testable without a display. Written separately from the owner's tests: the
// selection tests build their own row orders, and the theme tests call the token
// layer directly instead of walking the same screenshot path.

import (
	"testing"
	"time"

	mui "github.com/egoist/mygo/ui"

	"github.com/zeokeel/super-agent/internal/view"
	"github.com/zeokeel/super-agent/internal/view/viewtest"
)

// The execution list must key rows by the Ticket, not the row number: publish the
// SAME tickets in the reverse order and the opened row must stay the same Ticket.
func TestVerifyTicketListKeyFollowsTheTicket(t *testing.T) {
	a := pageApp(t)
	tt := mui.NewTester(a.view, 1440, 900)
	click(t, tt, "执行")

	vm := a.snap.Tickets()
	if vm == nil || len(vm.Rows) < 2 {
		t.Fatalf("fixture has %d ticket rows", len(vm.Rows))
	}
	if a.ticketList.Key == nil {
		t.Fatal("execution list has no ListState.Key: a reorder would move the opened row")
	}
	// Open the first row.
	first := vm.Rows[0].ID
	a.ticketSel = 0
	tt.Frame()

	rev := *vm
	rev.Rows = make([]view.TicketRow, len(vm.Rows))
	for i := range vm.Rows {
		rev.Rows[len(vm.Rows)-1-i] = vm.Rows[i]
	}
	a.snap.PublishTickets(&rev)
	tt.Frame()

	rows := view.FilterTickets(rev.Rows, a.ticketFilter)
	if a.ticketSel < 0 || a.ticketSel >= len(rows) {
		t.Fatalf("after reorder selection = %d of %d", a.ticketSel, len(rows))
	}
	if got := rows[a.ticketSel].ID; got != first {
		t.Fatalf("selection followed the row number: opened ticket %q before, %q after", first, got)
	}
	// The row builder must return the same entity the list keyed by.
	if got := rev.Rows[0].ID; got == "" {
		t.Fatal("TicketRow.ID is empty: ListState.Key would be a zero value")
	}
}

// The same invariant for the Thread/trigger list, which reorders on every event.
func TestVerifyThreadListKeyFollowsTheThread(t *testing.T) {
	a := pageApp(t)
	tt := mui.NewTester(a.view, 1440, 900)
	click(t, tt, "触发")

	vm := a.snap.Threads()
	if vm == nil || len(vm.Threads) < 2 {
		t.Fatalf("fixture has %d thread rows", len(vm.Threads))
	}
	if a.threadList.Key == nil {
		t.Fatal("trigger list has no ListState.Key: a reorder would move the opened thread")
	}
	first := vm.Threads[0].ID
	if first == "" {
		t.Fatal("ThreadRow.ID is empty")
	}
	a.threadSel = 0
	tt.Frame()

	rev := *vm
	rev.Threads = make([]view.ThreadRow, len(vm.Threads))
	for i := range vm.Threads {
		rev.Threads[len(vm.Threads)-1-i] = vm.Threads[i]
	}
	a.snap.PublishThreads(&rev)
	tt.Frame()
	if a.threadSel < 0 || a.threadSel >= len(rev.Threads) {
		t.Fatalf("after reorder selection = %d of %d", a.threadSel, len(rev.Threads))
	}
	if got := rev.Threads[a.threadSel].ID; got != first {
		t.Fatalf("selection followed the row number: thread %q -> %q", first, got)
	}
}

// The stream list must derive its key from Thread IDs; without IDs it must fall
// back to nil rather than a repeated empty key (mygo panics on duplicates).
func TestVerifyStreamListKey(t *testing.T) {
	now := time.Now()
	threads := make([]view.Thread, 3)
	for i := range threads {
		threads[i] = view.Thread{
			ID:   "t-" + string(rune('a'+i)),
			Head: view.Line{Kind: view.LineInbound, Text: "msg", At: now},
		}
	}
	snap := view.NewSnapshot()
	snap.PublishStream(&view.StreamVM{Threads: threads})
	a := newApp(snap, &fakeCmd{})
	a.now = now
	tt := mui.NewTester(a.view, 1440, 900)
	click(t, tt, "消息")
	if a.streamList.Key == nil {
		t.Fatal("stream with Thread IDs has no ListState.Key")
	}
	if !a.streamList.FollowEnd {
		t.Fatal("stream does not follow its end")
	}

	// Same projection without IDs: nil key, no duplicate-key panic on the next frame.
	noIDs := make([]view.Thread, 2)
	for i := range noIDs {
		noIDs[i] = view.Thread{Head: view.Line{Kind: view.LineInbound, Text: "x", At: now}}
	}
	snap.PublishStream(&view.StreamVM{Threads: noIDs})
	tt.Frame()
	if a.streamList.Key != nil {
		t.Fatal("stream without Thread IDs kept a key (empty duplicates)")
	}
}

// The token layer must read the shared colors from the theme it is given; dark
// mode must not be a second hard-coded palette.
func TestVerifyTokensDeriveFromTheme(t *testing.T) {
	light := tokensFrom(mui.LightTheme())
	dark := tokensFrom(mui.DarkTheme())
	if light.Bg == dark.Bg {
		t.Fatalf("dark and light share the same background: %v vs %v", light.Bg, dark.Bg)
	}
	th := mui.DarkTheme()
	if dark.Bg != th.Background || dark.Surface != th.Surface ||
		dark.Border != th.Border || dark.Accent != th.Accent {
		t.Fatalf("tokens do not read the given theme: tokens=%+v theme=%+v", dark, th)
	}
	if light.Bg != mui.LightTheme().Background {
		t.Fatal("light tokens do not read LightTheme's background")
	}
	// nil must not panic and must behave like the light theme.
	nilTok := tokensFrom(nil)
	if nilTok.Bg != light.Bg {
		t.Fatalf("tokensFrom(nil) bg = %v, want the light %v", nilTok.Bg, light.Bg)
	}
}

// An input made only of ideographic spaces must not dispatch a task (the old
// hand-rolled trimmer knew four ASCII bytes).
func TestVerifyFullWidthSpaceIsNotATask(t *testing.T) {
	cmd := &fakeCmd{}
	a := newTestApp(t, false)
	a.cmd = cmd
	a.draft = "\u3000\u3000\u3000"
	tt := mui.NewTester(a.view, 1440, 900)
	if err := tt.Click("发送"); err != nil {
		t.Fatalf("click 发送: %v", err)
	}
	if len(cmd.got) != 0 {
		t.Fatalf("blank full-width draft dispatched %q", cmd.got)
	}
}

// The overview fixture proves the humanize grouping actually renders (the
// hand-rolled groupedNumber is gone).
func TestVerifyGroupedTokenTotalRenders(t *testing.T) {
	a := newTestApp(t, false)
	ov := viewtest.DemoOverview(a.now)
	ov.Usage = view.Usage{Calls: 3, Total: 1204331, Prompt: 1000000, Cached: 900000}
	a.snap.PublishOverview(ov)
	tt := mui.NewTester(a.view, 1440, 900)
	wantTexts(t, tt, "1,204,331")
}
