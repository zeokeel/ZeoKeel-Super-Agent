package view_test

import (
	"testing"
	"time"

	. "github.com/zeokeel/super-agent/internal/view"
)

// The 超级Agent conversation: bounded history, newest first for the panel, and a
// clear line between "the agent answered" and "a genuine failure".
func TestChatVM(t *testing.T) {
	now := time.Now()
	vm := &ChatVM{Messages: []ChatMessage{
		{Role: ChatUser, Text: "改一下登录超时", At: now},
		{Role: ChatAssistant, Kind: ChatAnswer, Text: "好，我看看", At: now},
		{Role: ChatSystem, Kind: ChatOutcome, Text: "已派发 #42", At: now},
		{Role: ChatUser, Text: "谢谢", At: now},
	}}
	if tail := vm.Tail(2); len(tail) != 2 || tail[1].Text != "谢谢" {
		t.Fatalf("Tail(2) = %+v", tail)
	}
	if tail := vm.Tail(0); tail != nil {
		t.Fatalf("Tail(0) = %+v", tail)
	}
	if tail := vm.Tail(99); len(tail) != 4 {
		t.Fatalf("Tail(99) = %d messages", len(tail))
	}
	last, ok := vm.Last()
	if !ok || last.Text != "谢谢" {
		t.Fatalf("Last = %+v %v", last, ok)
	}
	answer, ok := vm.LastAnswer()
	if !ok || answer.Role != ChatSystem || answer.Text != "已派发 #42" {
		t.Fatalf("LastAnswer = %+v %v", answer, ok)
	}
	if (ChatMessage{Kind: ChatError}).IsFailure() != true ||
		(ChatMessage{Kind: ChatAnswer}).IsFailure() ||
		(ChatMessage{Kind: ChatOutcome}).IsFailure() ||
		(ChatMessage{Kind: ChatNote}).IsFailure() {
		t.Fatal("only ChatError is a failure")
	}
	var nilVM *ChatVM
	if nilVM.Tail(3) != nil {
		t.Fatal("a nil chat has no tail")
	}
	if _, ok := nilVM.LastAnswer(); ok {
		t.Fatal("a nil chat answered")
	}
	if _, ok := nilVM.Last(); ok {
		t.Fatal("a nil chat has no last message")
	}
}

// The workspace filters compare ids and keep the row order; "" is 全部.
func TestWorkspaceFilters(t *testing.T) {
	rows := []TicketRow{
		{ID: "a", WorkspaceID: "ws_1"},
		{ID: "b", WorkspaceID: "ws_2"},
		{ID: "c", WorkspaceID: "ws_1"},
	}
	if got := FilterTicketsByWorkspace(rows, ""); len(got) != 3 {
		t.Fatalf("全部 = %d rows", len(got))
	}
	got := FilterTicketsByWorkspace(rows, "ws_1")
	if len(got) != 2 || got[0].ID != "a" || got[1].ID != "c" {
		t.Fatalf("ws_1 = %+v", got)
	}
	if got := FilterTicketsByWorkspace(rows, "nope"); len(got) != 0 {
		t.Fatalf("an unknown workspace matched %d rows", len(got))
	}
	threads := []Thread{{ID: "t1", Workspace: "ws_1"}, {ID: "t2", Workspace: "ws_2"}, {ID: "t3"}}
	if got := FilterThreadsByWorkspace(threads, ""); len(got) != 3 {
		t.Fatalf("全部 = %d threads", len(got))
	}
	if got := FilterThreadsByWorkspace(threads, "ws_1"); len(got) != 1 || got[0].ID != "t1" {
		t.Fatalf("ws_1 = %+v", got)
	}
	// An unclassified thread belongs to no workspace: only 全部 shows it.
	if got := FilterThreadsByWorkspace(threads, "ws_2"); len(got) != 1 || got[0].ID != "t2" {
		t.Fatalf("ws_2 = %+v", got)
	}
}

// The label shows the number; the id is for lookups, and a projector without a
// number still shows something honest.
func TestTicketLabel(t *testing.T) {
	for _, tc := range []struct {
		id     string
		number int
		want   string
	}{
		{"tk-42", 42, "#42"},
		{"tk-42", 0, "#tk-42"},
		{"", 0, "#?"},
	} {
		if got := TicketLabel(tc.id, tc.number); got != tc.want {
			t.Fatalf("TicketLabel(%q, %d) = %q, want %q", tc.id, tc.number, got, tc.want)
		}
	}
}

// SaveEffect says when a save takes effect; only the prompt group is hot.
func TestSaveEffect(t *testing.T) {
	if got := SaveEffect(SettingsPrompt); got != "热更新：下一次模型调用生效" {
		t.Fatalf("prompt effect %q", got)
	}
	if got := SaveEffect(SettingsSource); got != "保存后重启相关渠道（几秒；正在跑的工单不受影响）" {
		t.Fatalf("source effect %q", got)
	}
	for _, id := range []SettingsGroupID{SettingsEval, SettingsExecution, SettingsPeople, SettingsDeploy, "other"} {
		if SaveEffect(id) == "" {
			t.Fatalf("%q has no effect wording", id)
		}
	}
}
