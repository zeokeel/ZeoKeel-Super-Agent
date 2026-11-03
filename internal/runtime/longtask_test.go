package runtime

import (
	"strings"
	"testing"
	"time"

	"github.com/zeokeel/super-agent/internal/agent"
	"github.com/zeokeel/super-agent/internal/domain"
	"github.com/zeokeel/super-agent/internal/engine"
	"github.com/zeokeel/super-agent/internal/view"
)

// A task may run for hours. The transcript kept in memory has to be bounded, and
// the panel has to say that older lines were folded instead of pretending the task
// did less than it did (design §7.3).
func TestTranscriptIsBoundedForLongTasks(t *testing.T) {
	log := &eventLog{}
	for i := 0; i < maxEventsPerTicket+3000; i++ {
		log.append("t1", workerEvent{seq: uint64(i), kind: agent.EventToolEnd,
			tool: "bash", output: "line"})
	}
	if got := len(log.get("t1")); got > maxEventsPerTicket {
		t.Fatalf("kept %d events, cap is %d", got, maxEventsPerTicket)
	}
	if got := log.foldedCount("t1"); got != 3000 {
		t.Fatalf("folded %d, want 3000", got)
	}

	// The panel shows the fold, so nobody assumes the beginning vanished silently.
	eng := engine.New(engine.RuleAssessor{})
	eng.Register(domain.Workspace{ID: "ws_web", Name: "web", Root: "/tmp/web",
		Kind: domain.KindRepo, Brief: domain.Brief{Purpose: "webapp", Keywords: []string{"webapp"}}},
		engine.Policy{Version: "v1"})
	if _, err := eng.Dispatch(engine.DispatchInput{Text: "在 webapp 里做事", Workspace: "ws_web"}); err != nil {
		t.Fatal(err)
	}
	out := evalNow(eng)
	rt := New(eng, view.NewSnapshot())
	// Overfill the real ticket's transcript too, so the projection has something
	// to fold and report.
	for i := 0; i < maxEventsPerTicket+500; i++ {
		rt.log.append(string(out.Ticket), workerEvent{kind: agent.EventToolEnd, tool: "bash"})
	}
	_ = log
	vm := rt.stream()
	found := false
	for _, th := range vm.Threads {
		for _, step := range th.Steps {
			if strings.Contains(step.Text, "更早的") && strings.Contains(step.Text, "已折叠") {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("the transcript did not say that older lines were folded")
	}
}

// Tool output stored for the panel is a preview: keeping hundreds of megabytes of
// scrollback is how a multi-hour task kills the app.
func TestTranscriptClampsToolOutput(t *testing.T) {
	big := strings.Repeat("x", 50000)
	got := clampForDisplay(big, 4000)
	if len(got) > 4100 {
		t.Fatalf("clamped output is %d bytes", len(got))
	}
	if !strings.Contains(got, "显示截断") {
		t.Fatal("the clamp is silent")
	}
	// Multi-byte text must not be cut mid-rune.
	cn := strings.Repeat("中", 4000)
	got = clampForDisplay(cn, 1001)
	if strings.ContainsRune(got, '\uFFFD') {
		t.Fatal("invalid UTF-8 after clamping")
	}
	if clampForDisplay("short", 100) != "short" {
		t.Fatal("short output must pass through unchanged")
	}
}

// A ticket that has been running for hours says so, rather than "刚刚".
func TestRunningTicketShowsElapsed(t *testing.T) {
	eng := engine.New(engine.RuleAssessor{})
	eng.Register(domain.Workspace{ID: "ws_web", Name: "web", Root: "/tmp/web",
		Kind: domain.KindRepo, Brief: domain.Brief{Purpose: "webapp", Keywords: []string{"webapp"}}},
		engine.Policy{Version: "v1"})
	if _, err := eng.Dispatch(engine.DispatchInput{Text: "在 webapp 里做长任务", Workspace: "ws_web"}); err != nil {
		t.Fatal(err)
	}
	out := evalNow(eng)
	// Pretend the assignment started three hours ago: with a fake clock the
	// engine's own startedAt is old, not just a copy the projection never sees.
	eng.SetClock(func() time.Time { return time.Now().Add(-3*time.Hour - 12*time.Minute) })
	if err := eng.StartAssignment(out.Ticket); err != nil {
		t.Fatal(err)
	}
	eng.SetClock(time.Now)

	rt := New(eng, view.NewSnapshot())
	vm := rt.ticketsVM(time.Now())
	var activity string
	for _, row := range vm.Rows {
		if row.ID == string(out.Ticket) {
			activity = row.Activity
		}
	}
	if !strings.Contains(activity, "3 小时") {
		t.Fatalf("a long run reads %q", activity)
	}
}

// Usage lines arrive between streamed text chunks; they must not split a sentence
// into two rows (seen in a live-model screenshot).
func TestUsageLinesDoNotSplitStreamedText(t *testing.T) {
	var log eventLog
	log.append("t", workerEvent{kind: agent.EventText, text: "Created hello.txt at the"})
	log.append("t", workerEvent{kind: agent.EventUsage})
	log.append("t", workerEvent{kind: agent.EventText, text: " workspace root."})
	lines := log.get("t")
	var texts []string
	for _, l := range lines {
		if l.kind == agent.EventText {
			texts = append(texts, l.text)
		}
	}
	if len(texts) != 1 || texts[0] != "Created hello.txt at the workspace root." {
		t.Fatalf("text was split: %v", texts)
	}
}
