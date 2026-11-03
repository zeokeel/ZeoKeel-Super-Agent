package store

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestChatRoundTripAndBound(t *testing.T) {
	s, _ := openTemp(t)
	base := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	for i := 0; i < maxChatMessages+10; i++ {
		if _, err := s.AppendChat("dm:ding_zhangsan", ChatMessage{
			Role: "user", Text: fmt.Sprintf("m%d", i), At: base.Add(time.Duration(i) * time.Second),
		}); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}
	got, err := s.Chat("dm:ding_zhangsan")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != maxChatMessages {
		t.Fatalf("history length = %d, want the bound %d", len(got), maxChatMessages)
	}
	// The oldest turns were dropped; the newest survived.
	if got[0].Text != "m10" {
		t.Fatalf("oldest kept = %q, want m10", got[0].Text)
	}
	if last := got[len(got)-1]; last.Text != "m209" {
		t.Fatalf("newest = %q", last.Text)
	}
	// Timestamps round-trip through the canonical UTC encoding.
	if !got[0].At.Equal(base.Add(10 * time.Second)) {
		t.Fatalf("time = %v, want %v", got[0].At, base.Add(10*time.Second))
	}
}

func TestAppendChatIsIdempotentByID(t *testing.T) {
	s, _ := openTemp(t)
	if _, err := s.AppendChat("c1", ChatMessage{ID: "turn-1", Role: "user", Text: "a"}); err != nil {
		t.Fatal(err)
	}
	got, err := s.AppendChat("c1", ChatMessage{ID: "turn-1", Role: "user", Text: "b"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Text != "b" {
		t.Fatalf("retried append duplicated a turn: %+v", got)
	}
}

func TestPutChatClearChatAndChatIDs(t *testing.T) {
	s, _ := openTemp(t)
	msgs := []ChatMessage{
		{Role: "system", Text: "welcome"},
		{Role: "assistant", Text: "hello"},
	}
	if err := s.PutChat("conv-empty-id", msgs); err != nil {
		t.Fatal(err)
	}
	got, err := s.Chat("conv-empty-id")
	if err != nil || len(got) != 2 || got[1].Text != "hello" {
		t.Fatalf("PutChat round trip: %+v err=%v", got, err)
	}
	if got[0].At.IsZero() {
		t.Fatal("PutChat did not stamp a timestamp")
	}
	ids, err := s.ChatIDs()
	if err != nil || len(ids) != 1 || ids[0] != "conv-empty-id" {
		t.Fatalf("ChatIDs = %v err=%v", ids, err)
	}
	if err := s.ClearChat("conv-empty-id"); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Chat("conv-empty-id"); err != nil || len(got) != 0 {
		t.Fatalf("after ClearChat: %+v err=%v", got, err)
	}
	if ids, _ := s.ChatIDs(); len(ids) != 0 {
		t.Fatalf("ChatIDs after clear = %v", ids)
	}
	if got, err := s.Chat("never-used"); err != nil || got != nil {
		t.Fatalf("unknown chat = %+v err=%v, want nil,nil", got, err)
	}
}

// Chat rows live in their own kv namespace: a settings load must not treat recent
// chat as configuration (and a whole-snapshot save must not write it back).
func TestChatIsNotASetting(t *testing.T) {
	s, _ := openTemp(t)
	if _, err := s.AppendChat("dm:1", ChatMessage{Role: "user", Text: "hi"}); err != nil {
		t.Fatal(err)
	}
	values, err := s.Settings()
	if err != nil {
		t.Fatal(err)
	}
	for k := range values {
		if strings.Contains(strings.ToLower(k), "chat") {
			t.Fatalf("Settings() leaked the chat row as %q", k)
		}
	}
}

// A single huge pasted message must not blow the row budget: the store keeps a
// valid UTF-8 prefix and the encoded row stays within the bound.
func TestChatBoundsOversizedMessage(t *testing.T) {
	s, _ := openTemp(t)
	big := strings.Repeat("汉", 200<<10) // ~600 KB of CJK
	if _, err := s.AppendChat("c-big", ChatMessage{Role: "user", Text: big}); err != nil {
		t.Fatal(err)
	}
	raw, err := s.kvGet(chatKeyPrefix + "c-big")
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) > maxChatBytes {
		t.Fatalf("stored row is %d bytes, over the %d bound", len(raw), maxChatBytes)
	}
	var stored []ChatMessage
	if err := json.Unmarshal([]byte(raw), &stored); err != nil {
		t.Fatalf("stored row is not readable JSON: %v", err)
	}
	if len(stored) != 1 || stored[0].Text == "" {
		t.Fatalf("oversized message was dropped instead of truncated: %+v", stored[:minInt(1, len(stored))])
	}
	if !utf8.ValidString(stored[0].Text) {
		t.Fatal("truncation produced invalid UTF-8")
	}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func TestChatValidation(t *testing.T) {
	s, _ := openTemp(t)
	if err := s.PutChat("", nil); err == nil {
		t.Fatal("empty chat id accepted")
	}
	if err := s.PutChat("bad\nid", nil); err == nil {
		t.Fatal("control character accepted in chat id")
	}
	if _, err := s.AppendChat("c1", ChatMessage{Role: ""}); err == nil {
		t.Fatal("message without a role accepted")
	}
	if _, err := s.AppendChat("c1", ChatMessage{Role: strings.Repeat("r", maxChatRoleLen+1)}); err == nil {
		t.Fatal("oversized role accepted")
	}
	// A corrupt row is an error, not silent data loss.
	if err := s.Set(chatKeyPrefix+"corrupt", "{not json"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Chat("corrupt"); err == nil {
		t.Fatal("corrupt chat row was accepted as empty")
	}
}
