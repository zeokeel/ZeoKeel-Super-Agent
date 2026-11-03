package runtime

import (
	"testing"
	"time"

	"github.com/zeokeel/super-agent/internal/engine"
	"github.com/zeokeel/super-agent/internal/view"
)

// Two Sources configured means two rows on the page. A poll from one must not
// erase the other: they report independently and at different times.
func TestTwoSourcesBothStayOnThePage(t *testing.T) {
	rt := New(engine.New(engine.RuleAssessor{}), view.NewSnapshot())
	now := rt.clock()
	rt.SetSourceStatus(SourceStatus{
		Kind: "github", Name: "acme/webapp", Account: "zeokeel-bot", Enabled: true,
		At: now, Cursor: "c1",
		Last: view.PollResult{At: now, Outcome: view.PollOK, Detail: "收到 2 条"},
	})
	rt.SetSourceStatus(SourceStatus{
		Kind: "dingtalk", Name: "研发群", Account: "机器人", Enabled: true,
		At: now, Cursor: "c2",
		Last: view.PollResult{At: now, Outcome: view.PollFailed, Problem: "token 过期",
			Detail: "鉴权失败，需要重新登录"},
	})

	vm := rt.sourcesVM(now)
	if len(vm.Sources) != 2 {
		t.Fatalf("expected two sources, got %d: %+v", len(vm.Sources), vm.Sources)
	}
	seen := map[string]view.SourceRow{}
	for _, s := range vm.Sources {
		seen[s.Kind] = s
	}
	if seen["github"].LastPoll.Outcome != view.PollOK {
		t.Fatalf("github poll outcome lost: %+v", seen["github"])
	}
	if seen["dingtalk"].LastPoll.Outcome != view.PollFailed ||
		seen["dingtalk"].LastPoll.Problem == "" {
		t.Fatalf("the failure must stay visible: %+v", seen["dingtalk"])
	}
}

// A later poll of the same source updates its row rather than adding a second one.
func TestSourceStatusIsUpdatedInPlace(t *testing.T) {
	rt := New(engine.New(engine.RuleAssessor{}), view.NewSnapshot())
	now := rt.clock()
	rt.SetSourceStatus(SourceStatus{Kind: "github", Name: "acme/webapp", Enabled: true, At: now,
		Last: view.PollResult{At: now, Outcome: view.PollOK}})
	rt.SetSourceStatus(SourceStatus{Kind: "github", Name: "acme/webapp", Enabled: false, At: now,
		Last: view.PollResult{At: now, Outcome: view.PollFailed, Problem: "gh 未登录"}})

	vm := rt.sourcesVM(now)
	if len(vm.Sources) != 1 {
		t.Fatalf("expected one row, got %d", len(vm.Sources))
	}
	if vm.Sources[0].LastPoll.Outcome != view.PollFailed || vm.Sources[0].Enabled {
		t.Fatalf("the update did not replace the row: %+v", vm.Sources[0])
	}
}

// Switching a source off must remove it, not leave a stale "ok" behind.
func TestRemovedSourceDisappears(t *testing.T) {
	rt := New(engine.New(engine.RuleAssessor{}), view.NewSnapshot())
	now := rt.clock()
	rt.SetSourceStatus(SourceStatus{Kind: "github", Name: "a/b", Enabled: true, At: now})
	rt.SetSourceStatus(SourceStatus{Kind: "dingtalk", Name: "群", Enabled: true, At: now})
	rt.RemoveSourceStatus("github", "a/b")
	vm := rt.sourcesVM(now)
	if len(vm.Sources) != 1 || vm.Sources[0].Kind != "dingtalk" {
		t.Fatalf("removal left %+v", vm.Sources)
	}
}

// No driver configured is not an error: the page says nothing is listening, and it
// must not invent a row.
func TestNoSourcesMeansAnHonestEmptyPage(t *testing.T) {
	rt := New(engine.New(engine.RuleAssessor{}), view.NewSnapshot())
	vm := rt.sourcesVM(time.Now())
	if len(vm.Sources) != 0 {
		t.Fatalf("invented %d sources", len(vm.Sources))
	}
}

// The cursor is a driver token, not a sentence. The page says whether it advanced;
// printing "acme/webapp|2026-11-03T15:57:00Z" at a person is noise.
func TestCursorIsPresentedAsAPhrase(t *testing.T) {
	rt := New(engine.New(engine.RuleAssessor{}), view.NewSnapshot())
	now := rt.clock()
	rt.SetSourceStatus(SourceStatus{Kind: "github", Name: "a/b", Enabled: true, At: now})
	rt.SetSourceStatus(SourceStatus{Kind: "github", Name: "a/c", Enabled: true, At: now,
		Cursor: "a/c|2026-11-03T15:57:00Z"})
	vm := rt.sourcesVM(now)
	var first, second string
	for _, s := range vm.Sources {
		if s.Name == "a/b" {
			first = s.Cursor
		}
		if s.Name == "a/c" {
			second = s.Cursor
		}
	}
	if first != "尚未开始" || second != "已推进" {
		t.Fatalf("cursor labels = %q / %q", first, second)
	}
}
