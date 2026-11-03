// Package mockllm implements a minimal OpenAI-compatible /v1/chat/completions
// server used to test Go agent SDKs without any real API key.
//
// It supports:
//   - streaming SSE (data: {...}\n\n ... data: [DONE])
//   - non-streaming JSON responses
//   - tool_calls in both modes
//   - OpenAI streaming chunk format incl. finish_reason and usage chunks
//
// Behaviour is selected by the "model" field of the request:
//
//	"mock-text"  -> stream plain text, no tools
//	"mock-tool"  -> round 1: emit tool_call to echo_upper; round 2 (after a
//	                role=tool message): emit final text answer
//	"mock-slow"  -> like mock-text but 300ms between chunks (for cancel tests)
//	"mock-500"   -> always returns HTTP 500 (for retry tests)
package mockllm

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Server is a running mock LLM.
type Server struct {
	URL     string // e.g. http://127.0.0.1:PORT/v1
	Root    string // e.g. http://127.0.0.1:PORT
	ln      net.Listener
	httpSrv *http.Server
	// Requests is guarded by mu; it is exported for tests that only need a count.
	Requests int
	// mu guards Requests and the recorded request history. Tests read both after a
	// run has finished, so the lock only has to make those reads race-free against
	// the HTTP handler goroutine.
	mu       sync.Mutex
	requests []RequestInfo
	// ToolName and ToolArgs are what the mock calls; zero values mean the default
	// echo_upper tool. A demo sets them to a real tool so the panel shows a real
	// tool call.
	ToolName string
	ToolArgs string
	// assessJSON overrides what "mock-assess" answers.
	assessJSON string
}

func (s *Server) toolName() string {
	if s.ToolName != "" {
		return s.ToolName
	}
	return "echo_upper"
}

func (s *Server) toolArgs() string {
	if s.ToolArgs != "" {
		return s.ToolArgs
	}
	return `{"text":"hello from mock"}`
}

// RequestMessage is one message as the agent sent it. It is a flattened view so a
// test can assert on conversation shape (roles, tool rounds) without importing the
// agent's own types.
type RequestMessage struct {
	Role       string
	Content    string
	ToolCallID string
	ToolCalls  int
}

// RequestInfo is one completed /v1/chat/completions call, as seen by the server.
type RequestInfo struct {
	Model    string
	Stream   bool
	Tools    int
	Messages []RequestMessage
	// ToolNames is the tool list in the order the request carries it. Prompt caches
	// key on the serialized prefix, so a stable order is part of the contract.
	ToolNames []string
	// System is the system message verbatim, for prefix-stability checks.
	System string
}

// LastRequest returns the most recent request, or a zero value if none arrived.
func (s *Server) LastRequest() RequestInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.requests) == 0 {
		return RequestInfo{}
	}
	return s.requests[len(s.requests)-1]
}

// RecordedRequests returns every request seen, oldest first.
func (s *Server) RecordedRequests() []RequestInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]RequestInfo, len(s.requests))
	copy(out, s.requests)
	return out
}

// RequestCount returns how many completions were served. Reading Server.Requests
// directly races with the handler goroutine, so tests should use this instead.
func (s *Server) RequestCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Requests
}

func (s *Server) record(req chatRequest) {
	info := RequestInfo{Model: req.Model, Stream: req.Stream, Tools: len(req.Tools)}
	for _, m := range req.Messages {
		rm := RequestMessage{Role: m.Role, Content: contentStr(m.Content), ToolCallID: m.ToolCallID, ToolCalls: len(m.ToolCalls)}
		if m.Role == "system" {
			info.System = rm.Content
		}
		for _, tc := range m.ToolCalls {
			if rm.Content == "" {
				rm.Content = tc.Function.Name
			}
		}
		info.Messages = append(info.Messages, rm)
	}
	for _, raw := range req.Tools {
		b, err := json.Marshal(raw)
		if err != nil {
			continue
		}
		var shape struct {
			Function struct {
				Name string `json:"name"`
			} `json:"function"`
		}
		if json.Unmarshal(b, &shape) == nil {
			info.ToolNames = append(info.ToolNames, shape.Function.Name)
		}
	}
	s.mu.Lock()
	s.Requests++
	s.requests = append(s.requests, info)
	s.mu.Unlock()
}

// Start launches the mock server on a random loopback port.
func Start() (*Server, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	s := &Server{ln: ln}
	s.Root = "http://" + ln.Addr().String()
	s.URL = s.Root + "/v1"
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat/completions", s.handleChat)
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"object":"list","data":[{"id":"mock-tool","object":"model"}]}`)
	})
	s.httpSrv = &http.Server{Handler: mux}
	go func() { _ = s.httpSrv.Serve(ln) }()
	return s, nil
}

// Close stops the server.
func (s *Server) Close() { _ = s.httpSrv.Close() }

// ---- request/response wire types (subset of OpenAI schema) ----

type reqMsg struct {
	Role       string          `json:"role"`
	Content    json.RawMessage `json:"content"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
	ToolCalls  []reqToolCall   `json:"tool_calls,omitempty"`
}

type reqToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type chatRequest struct {
	Model         string         `json:"model"`
	Messages      []reqMsg       `json:"messages"`
	Stream        bool           `json:"stream"`
	Tools         []any          `json:"tools"`
	StreamOptions map[string]any `json:"stream_options"`
	Temperature   *float64       `json:"temperature"`
}

func contentStr(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return string(raw)
}

// sawToolResult reports whether the conversation already contains a tool result,
// i.e. this is the 2nd+ round of an agent loop.
func sawToolResult(msgs []reqMsg) bool {
	for _, m := range msgs {
		if m.Role == "tool" || m.Role == "function" {
			return true
		}
	}
	return false
}

func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	var req chatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request: "+err.Error(), 400)
		return
	}
	log.Printf("[mockllm] model=%s stream=%v msgs=%d tools=%d", req.Model, req.Stream, len(req.Messages), len(req.Tools))
	s.record(req)

	// "mock-assess" answers the assessment prompt with a valid decision JSON, so
	// the Eval path can be exercised end to end without a real model.
	if req.Model == "mock-assess" {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"index":         0,
				"message":       map[string]any{"role": "assistant", "content": s.assessReply()},
				"finish_reason": "stop",
			}},
			"usage": map[string]any{"prompt_tokens": 12, "completion_tokens": 8, "total_tokens": 20},
		})
		return
	}

	if req.Model == "mock-500" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(500)
		fmt.Fprint(w, `{"error":{"message":"mock upstream failure","type":"server_error"}}`)
		return
	}

	useTool := strings.HasPrefix(req.Model, "mock-tool") && !sawToolResult(req.Messages) && len(req.Tools) > 0
	slow := req.Model == "mock-slow"
	includeUsage := false
	if req.StreamOptions != nil {
		if v, ok := req.StreamOptions["include_usage"].(bool); ok {
			includeUsage = v
		}
	}

	if !req.Stream {
		s.writeNonStream(w, req, useTool)
		return
	}
	s.writeStream(w, req, useTool, slow, includeUsage)
}

// AssessReply is the decision "mock-assess" returns. A test or demo sets it to
// match the workspaces and rules it declared.
func (s *Server) SetAssessReply(v string) {
	s.mu.Lock()
	s.assessJSON = v
	s.mu.Unlock()
}

func (s *Server) assessReply() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.assessJSON != "" {
		return s.assessJSON
	}
	return defaultAssessReply
}

// defaultAssessReply accepts into ws_web, the workspace the demo seeds.
const defaultAssessReply = `{"kind":"accept","workspace":"ws_web","strength":"explicit",` +
	`"reason":"said","reason_text":"明确要求修改 webapp",` +
	`"rules":[{"id":"implementation","matched":true,"note":"是明确的实现请求"},` +
	`{"id":"data-request","matched":false,"note":"不涉及取数"},` +
	`{"id":"off-topic","matched":false,"note":"不是闲聊"}],` +
	`"worktree_name":"webapp-change","intent_evidence":"把登录超时改成可配置"}`

func (s *Server) writeNonStream(w http.ResponseWriter, req chatRequest, useTool bool) {
	msg := map[string]any{"role": "assistant"}
	finish := "stop"
	if useTool {
		msg["content"] = "Let me check that with a tool."
		msg["tool_calls"] = []map[string]any{{
			"id":   "call_mock_1",
			"type": "function",
			"function": map[string]any{
				"name":      s.toolName(),
				"arguments": s.toolArgs(),
			},
		}}
		finish = "tool_calls"
	} else {
		msg["content"] = finalAnswer(req)
	}
	resp := map[string]any{
		"id":      "chatcmpl-mock",
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   req.Model,
		"choices": []map[string]any{{
			"index":         0,
			"message":       msg,
			"finish_reason": finish,
		}},
		"usage": map[string]any{"prompt_tokens": 21, "completion_tokens": 9, "total_tokens": 30},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// chunk writes one SSE data frame.
func chunk(w http.ResponseWriter, fl http.Flusher, delta map[string]any, finish any) {
	body := map[string]any{
		"id":      "chatcmpl-mock",
		"object":  "chat.completion.chunk",
		"created": time.Now().Unix(),
		"model":   "mock",
		"choices": []map[string]any{{
			"index":         0,
			"delta":         delta,
			"finish_reason": finish,
		}},
	}
	b, _ := json.Marshal(body)
	fmt.Fprintf(w, "data: %s\n\n", b)
	fl.Flush()
}

func (s *Server) writeStream(w http.ResponseWriter, req chatRequest, useTool, slow, includeUsage bool) {
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	w.WriteHeader(200)
	fl, ok := w.(http.Flusher)
	if !ok {
		return
	}
	delay := func(d time.Duration) {
		if slow {
			d = 300 * time.Millisecond
		}
		if d > 0 {
			time.Sleep(d)
		}
	}

	// 1. role chunk
	chunk(w, fl, map[string]any{"role": "assistant", "content": ""}, nil)
	delay(20 * time.Millisecond)

	if useTool {
		// 2. optionally stream a preamble before the tool call. Real models
		// (Claude, and DeepSeek in some modes) do this: text, then tool_call.
		// "mock-tool-first" skips the preamble, like OpenAI gpt-4o does.
		if req.Model != "mock-tool-first" {
			for _, piece := range []string{"Let me ", "check that ", "with a tool."} {
				chunk(w, fl, map[string]any{"content": piece}, nil)
				delay(15 * time.Millisecond)
			}
		}
		// 3. tool call: name first, then arguments split across chunks
		chunk(w, fl, map[string]any{"tool_calls": []map[string]any{{
			"index": 0, "id": "call_mock_1", "type": "function",
			"function": map[string]any{"name": s.toolName(), "arguments": ""},
		}}}, nil)
		delay(15 * time.Millisecond)
		for _, frag := range splitPieces(s.toolArgs()) {
			chunk(w, fl, map[string]any{"tool_calls": []map[string]any{{
				"index":    0,
				"function": map[string]any{"arguments": frag},
			}}}, nil)
			delay(15 * time.Millisecond)
		}
		chunk(w, fl, map[string]any{}, "tool_calls")
	} else {
		ans := finalAnswer(req)
		// stream in small pieces, word-ish
		for _, piece := range splitPieces(ans) {
			chunk(w, fl, map[string]any{"content": piece}, nil)
			delay(15 * time.Millisecond)
		}
		chunk(w, fl, map[string]any{}, "stop")
	}

	if includeUsage {
		b, _ := json.Marshal(map[string]any{
			"id": "chatcmpl-mock", "object": "chat.completion.chunk",
			"created": time.Now().Unix(), "model": "mock",
			"choices": []map[string]any{},
			"usage":   map[string]any{"prompt_tokens": 42, "completion_tokens": 17, "total_tokens": 59},
		})
		fmt.Fprintf(w, "data: %s\n\n", b)
		fl.Flush()
	}
	fmt.Fprint(w, "data: [DONE]\n\n")
	fl.Flush()
}

func finalAnswer(req chatRequest) string {
	last := ""
	for _, m := range req.Messages {
		if m.Role == "tool" {
			last = contentStr(m.Content)
		}
	}
	if last != "" {
		return "TOOL_RESULT_SEEN[" + last + "]"
	}
	return "Hello! This is a streamed answer from the mock server."
}

func splitPieces(s string) []string {
	var out []string
	cur := ""
	for _, r := range s {
		cur += string(r)
		if r == ' ' || r == '!' || r == '.' || r == ']' {
			out = append(out, cur)
			cur = ""
		}
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}

// Quiet discards log output (useful in tests).
func Quiet() { log.SetOutput(discard{}) }

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }
