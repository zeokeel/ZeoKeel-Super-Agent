package github

import (
	"encoding/json"
	"strings"
	"testing"
)

// `gh api -i` 的输出解析是 ETag/304 的基础：状态行 + 响应头 + 空行 + body。
func TestSplitIncludeOutputParsesHeadersAndBody(t *testing.T) {
	raw := []byte("HTTP/2.0 200 OK\r\n" +
		"Content-Type: application/json; charset=utf-8\r\n" +
		"Etag: W/\"abc123\"\r\n" +
		"\r\n" +
		`[{"id":1}]`)
	body, status, header := splitIncludeOutput(raw)
	if status != 200 {
		t.Fatalf("status = %d, want 200", status)
	}
	if got := header.Get("Etag"); got != `W/"abc123"` {
		t.Fatalf("ETag = %q", got)
	}
	var items []struct {
		ID int `json:"id"`
	}
	if err := json.Unmarshal(body, &items); err != nil {
		t.Fatalf("body = %q: %v", body, err)
	}
	if len(items) != 1 || items[0].ID != 1 {
		t.Fatalf("items = %+v", items)
	}
}

// 304 只有头没有 body：必须解析出 304（ETag 复用靠它判断"列表没变"）。
func TestSplitIncludeOutputParsesNotModified(t *testing.T) {
	raw := []byte("HTTP/2.0 304 Not Modified\r\nEtag: W/\"abc123\"\r\n\r\n")
	body, status, header := splitIncludeOutput(raw)
	if status != 304 {
		t.Fatalf("status = %d, want 304", status)
	}
	if len(body) != 0 {
		t.Fatalf("body = %q, want empty", body)
	}
	if got := header.Get("Etag"); got != `W/"abc123"` {
		t.Fatalf("ETag = %q", got)
	}
}

// 没有 HTTP 头（普通 `gh api` 输出）时按 body 原样处理，不能把 JSON 吃掉。
func TestSplitIncludeOutputFallsBackToRawBody(t *testing.T) {
	raw := []byte(`[{"id":1}]`)
	body, status, _ := splitIncludeOutput(raw)
	if status != 0 {
		t.Fatalf("status = %d, want 0 (no headers)", status)
	}
	if strings.TrimSpace(string(body)) != strings.TrimSpace(string(raw)) {
		t.Fatalf("body = %q, want raw output", body)
	}
}

// LF 空行（某些构建输出）也要能解析。
func TestSplitIncludeOutputAcceptsLFSeparator(t *testing.T) {
	raw := []byte("HTTP/1.1 200 OK\nEtag: W/\"lf\"\n\n{\"ok\":true}")
	body, status, header := splitIncludeOutput(raw)
	if status != 200 || header.Get("Etag") != `W/"lf"` {
		t.Fatalf("status = %d, etag = %q", status, header.Get("Etag"))
	}
	if strings.TrimSpace(string(body)) != `{"ok":true}` {
		t.Fatalf("body = %q", body)
	}
}

// gh 输出的 body 已经是解码后的明文；即使头里有 chunked 也不能再分块解码。
func TestSplitIncludeOutputKeepsChunkedBodyRaw(t *testing.T) {
	raw := []byte("HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\nEtag: W/\"c\"\r\n\r\n{\"ok\":true}")
	body, status, header := splitIncludeOutput(raw)
	if status != 200 || header.Get("Etag") != `W/"c"` {
		t.Fatalf("status = %d, etag = %q", status, header.Get("Etag"))
	}
	if string(body) != `{"ok":true}` {
		t.Fatalf("body = %q, want the raw JSON", body)
	}
}
