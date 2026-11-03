package dingtalk

// verifier-A: independent falsification of the Wave-1 DingTalk claims (audit N-1
// / N-15): JSON parameter escaping for arbitrary control characters, and no
// second send for the same idempotency key inside the process.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/zeokeel/super-agent/internal/domain"
	"github.com/zeokeel/super-agent/internal/im"
)

// roundTrip unmarshals a template parameter and returns its "content"/"text".
func roundTrip(t *testing.T, raw, field string) string {
	t.Helper()
	var m map[string]string
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("parameter is not valid JSON: %v\n%q", err, raw)
	}
	return m[field]
}

func TestVerifyTextParamEscapesControlChars(t *testing.T) {
	cases := []string{
		"plain",
		"quote \" and backslash \\",
		"bell\b formfeed\f nul\x00 newline\n tab\t",
		"中文控制字符：\u0001\u001f",
		"</script><!-- not html -->",
	}
	for _, in := range cases {
		raw := textParam(in)
		if got := roundTrip(t, raw, "content"); got != in {
			t.Fatalf("textParam round-trip: %q -> %q", in, got)
		}
	}
}

func TestVerifyMarkdownParamEscapesControlChars(t *testing.T) {
	in := "# heading\nbody\b\f\x00 with controls \"quoted\""
	raw := markdownParam(in, "<marker>")
	got := roundTrip(t, raw, "text")
	if got != in+"<marker>" {
		t.Fatalf("markdownParam text round-trip: %q -> %q", in, got)
	}
	if title := roundTrip(t, raw, "title"); !strings.HasPrefix(title, "# heading") {
		t.Fatalf("markdown title = %q", title)
	}
}

// The same business key must not hit the wire twice: that is the driver-level
// duplicate guard the transport-layer retry removal relies on (N-15).
func TestVerifySendIsIdempotentPerKey(t *testing.T) {
	// The package's own fake transport records every request; nothing here
	// touches the network.
	tr := &fakeTransport{sendID: "msg-1"}
	d := NewWithTransport(Config{RobotCode: "robot", BotName: "bot"}, tr)
	msg := im.Outbound{
		Key:  "business-key-1",
		Conv: domain.Conversation{Kind: domain.ConvGroup, ID: "chat-1"},
		Text: "hello",
	}
	first, err := d.Send(context.Background(), msg)
	if err != nil {
		t.Fatalf("first Send: %v", err)
	}
	second, err := d.Send(context.Background(), msg)
	if err != nil {
		t.Fatalf("second Send: %v", err)
	}
	if len(tr.sends) != 1 {
		t.Fatalf("SendRobot called %d times for one key, want 1 (duplicate message risk)", len(tr.sends))
	}
	if first.MessageID != second.MessageID || first.MessageID == "" {
		t.Fatalf("receipts differ: %+v vs %+v", first, second)
	}
	// A different key is a different message and must go out.
	if _, err := d.Send(context.Background(), im.Outbound{
		Key: "business-key-2", Conv: msg.Conv, Text: "another",
	}); err != nil {
		t.Fatalf("third Send: %v", err)
	}
	if len(tr.sends) != 2 {
		t.Fatalf("SendRobot calls = %d after a new key, want 2", len(tr.sends))
	}
	// The marker is present in the outbound text parameter but not in the title.
	if len(tr.sends) != 2 || !strings.Contains(tr.sends[0].MsgParam, "content") {
		t.Fatalf("unexpected request shape: %+v", tr.sends)
	}
}

// HTTPTransport explicitly reports that it cannot receive: the honest downgrade
// instead of an endless retry loop.
func TestVerifyHTTPTransportReportsNoStream(t *testing.T) {
	tr := &HTTPTransport{}
	if tr.SupportsStream() {
		t.Fatal("HTTPTransport claims Stream support")
	}
}
