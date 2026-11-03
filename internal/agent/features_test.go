package agent

// Tests for the framework-provided built-in tools the Worker adopted:
// eino plantask (plan/todo), eino agentsmd (AGENTS.md injection), and the
// stdlib-only web_fetch / web_search tools.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/tool"
)

// ---- plantask ---------------------------------------------------------------

func toolInvoker(t *testing.T, bt tool.BaseTool) func(string) (string, error) {
	t.Helper()
	inv, ok := bt.(interface {
		InvokableRun(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error)
	})
	if !ok {
		t.Fatalf("tool %T is not invokable", bt)
	}
	return func(args string) (string, error) {
		return inv.InvokableRun(context.Background(), args)
	}
}

func TestPlanTaskToolsRegisteredAndPersist(t *testing.T) {
	dir := t.TempDir()
	create := startMock(t)
	create.ToolName = "TaskCreate"
	create.ToolArgs = `{"subject":"write tests","description":"add coverage for the new tools"}`
	r := newTestRunner(t, testConfig(create, dir, "mock-tool", func(c *Config) { c.PlanTasks = true }))
	runSessionForTest(t, r, "plan the work", nil)

	names := map[string]bool{}
	for _, name := range create.LastRequest().ToolNames {
		names[name] = true
	}
	for _, want := range planTaskToolNames {
		if !names[want] {
			t.Fatalf("plan task tool %q is missing from the model's tool list", want)
		}
	}
	taskFile := filepath.Join(dir, "1.json")
	data, err := os.ReadFile(taskFile)
	if err != nil {
		// plantask numbers tasks from 1 in BaseDir; a missing file means the tool
		// did not reach the workspace backend.
		entries, _ := os.ReadDir(dir)
		var seen []string
		for _, e := range entries {
			seen = append(seen, e.Name())
		}
		t.Fatalf("plantask did not write %s: %v (dir has %v)", taskFile, err, seen)
	}
	if !strings.Contains(string(data), "write tests") {
		t.Fatalf("task file does not contain the subject: %s", data)
	}

	// A second run with a TaskList call sees the persisted task.
	list := startMock(t)
	list.ToolName = "TaskList"
	list.ToolArgs = `{}`
	r2 := newTestRunner(t, testConfig(list, dir, "mock-tool", func(c *Config) { c.PlanTasks = true }))
	res := runSessionForTest(t, r2, "list the plan", nil)
	if !strings.Contains(res.Answer, "#1") || !strings.Contains(res.Answer, "write tests") {
		t.Fatalf("TaskList did not list the created task: %q", res.Answer)
	}
}

// ---- agentsmd ---------------------------------------------------------------

func TestAgentsMDInjectedIntoModelInput(t *testing.T) {
	srv := startMock(t)
	dir := t.TempDir()
	marker := "PROJECT-RULES: run the tests before claiming the work is done"
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("# Repo rules\n\n"+marker+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := newTestRunner(t, testConfig(srv, dir, "mock-text", func(c *Config) { c.AgentsMD = true }))
	runSessionForTest(t, r, "do the work", nil)

	reqs := srv.RecordedRequests()
	if len(reqs) == 0 {
		t.Fatal("no model request recorded")
	}
	var injected bool
	for _, m := range reqs[0].Messages {
		if strings.Contains(m.Content, marker) {
			injected = true
		}
	}
	if !injected {
		t.Fatalf("AGENTS.md was not injected into the first model input: %+v", reqs[0].Messages)
	}

	// A workspace without AGENTS.md must not fail the run.
	clean := t.TempDir()
	srv2 := startMock(t)
	r2 := newTestRunner(t, testConfig(srv2, clean, "mock-text", func(c *Config) { c.AgentsMD = true }))
	if _, err := r2.RunSession(context.Background(), RunRequest{System: "sys", Task: "hi"}, nil); err != nil {
		t.Fatalf("a missing AGENTS.md must be tolerated: %v", err)
	}
	for _, m := range srv2.LastRequest().Messages {
		if strings.Contains(m.Content, "PROJECT-RULES") {
			t.Fatalf("AGENTS.md content leaked from another workspace: %+v", m)
		}
	}
}

// ---- web_fetch --------------------------------------------------------------

func TestWebFetchHTMLToText(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<html><head><title>Hello &amp; Welcome</title>
<script>bad_script()</script><style>.x{color:red}</style></head>
<body><h1>Heading one</h1><p>Body text &amp; more.</p>
<div>Second <b>paragraph</b>.</div><noscript>noscript thing</noscript></body></html>`))
	}))
	defer ts.Close()

	out, err := fetchWeb(context.Background(), ts.URL, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Hello & Welcome", "Heading one", "Body text & more.", "Second paragraph."} {
		if !strings.Contains(out, want) {
			t.Fatalf("fetched text lost %q: %q", want, out)
		}
	}
	for _, bad := range []string{"bad_script", "<script", "color:red", "<h1>"} {
		if strings.Contains(out, bad) {
			t.Fatalf("fetched text kept raw markup/script %q: %q", bad, out)
		}
	}
}

func TestWebFetchPolicyAndLimits(t *testing.T) {
	// Private hosts and non-http(s) schemes are refused before any request.
	for _, tc := range []struct {
		name, url, want string
	}{
		{"loopback ip", "http://127.0.0.1:9/x", "private address"},
		{"localhost name", "http://localhost:9/x", "local host"},
		{"private ip", "http://10.0.0.5/x", "private address"},
		{"file scheme", "file:///etc/passwd", "http(s)"},
		{"ftp scheme", "ftp://example.com/x", "http(s)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := fetchWeb(context.Background(), tc.url, false); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("fetchWeb(%q) = %v, want an error containing %q", tc.url, err, tc.want)
			}
		})
	}
	// The 2MB body cap is enforced with an actionable message.
	big := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(strings.Repeat("A", webFetchMaxBytes+4096)))
	}))
	defer big.Close()
	if _, err := fetchWeb(context.Background(), big.URL, true); err == nil || !strings.Contains(err.Error(), "2 MB") {
		t.Fatalf("oversized page = %v, want a 2 MB limit error", err)
	}
	// A redirect target is validated with the same policy (the CheckRedirect hook
	// calls validateWebURL); this is the same call the hook makes.
	if _, err := validateWebURL(context.Background(), "http://127.0.0.1:9/redirected", false); err == nil {
		t.Fatal("a redirect target on a private address was not refused")
	}
}

func TestWebFetchToolIsAnObservation(t *testing.T) {
	tool, err := newWebFetchTool(true)
	if err != nil {
		t.Fatal(err)
	}
	run := toolInvoker(t, tool)
	out, err := run(`{"url":"ftp://example.com/x"}`)
	if err != nil {
		t.Fatalf("a refused fetch must be an observation, not an error: %v", err)
	}
	if !strings.Contains(out, "web_fetch failed") || !strings.Contains(out, "http(s)") {
		t.Fatalf("unhelpful observation: %q", out)
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("<html><title>T</title><body><p>Fetched body</p></body></html>"))
	}))
	defer ts.Close()
	out, err = run(`{"url":"` + ts.URL + `"}`)
	if err != nil || !strings.Contains(out, "Fetched body") {
		t.Fatalf("tool fetch = %q err=%v", out, err)
	}
}

// ---- web_search -------------------------------------------------------------

func searchFixture(results int) string {
	var b strings.Builder
	b.WriteString(`<html><body>`)
	for i := 0; i < results; i++ {
		b.WriteString(`<div class="result"><h2><a rel="nofollow" class="result__a" href="https://example.com/`)
		b.WriteString(string(rune('a' + i)))
		b.WriteString(`">Result <b>`)
		b.WriteString(string(rune('A' + i)))
		b.WriteString(`</b></a></h2><a class="result__snippet" href="#">snippet `)
		b.WriteString(string(rune('a' + i)))
		b.WriteString(`</a></div>`)
	}
	b.WriteString(`</body></html>`)
	return b.String()
}

func TestWebSearchParsesResults(t *testing.T) {
	var gotQuery string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query().Get("q")
		if gotQuery == "fail" {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(searchFixture(7)))
	}))
	defer ts.Close()
	endpoint := ts.URL + "/search?q=%s"

	out, err := searchWeb(context.Background(), endpoint, "zeokeel agent")
	if err != nil {
		t.Fatal(err)
	}
	if gotQuery != "zeokeel agent" {
		t.Fatalf("query was not passed through: %q", gotQuery)
	}
	for _, want := range []string{"Result A — https://example.com/a", "snippet a", "Result E — https://example.com/e"} {
		if !strings.Contains(out, want) {
			t.Fatalf("search output lost %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Result F") {
		t.Fatalf("search output was not limited to 5 results:\n%s", out)
	}
	if strings.Contains(out, "<a") || strings.Contains(out, "<b>") {
		t.Fatalf("search output kept raw markup:\n%s", out)
	}

	if _, err := searchWeb(context.Background(), endpoint, "fail"); err == nil || !strings.Contains(err.Error(), "503") {
		t.Fatalf("non-200 search = %v, want a 503 error", err)
	}
}

// ---- registration / whitelist ----------------------------------------------

func TestAllBuiltinFeaturesToolNames(t *testing.T) {
	srv := startMock(t)
	r := newTestRunner(t, testConfig(srv, t.TempDir(), "mock-text", func(c *Config) {
		c.PlanTasks = true
		c.AgentsMD = true
		c.WebTools = true
	}))
	runSessionForTest(t, r, "hi", nil)
	first := srv.LastRequest()
	names := map[string]bool{}
	for _, n := range first.ToolNames {
		names[n] = true
	}
	for _, want := range append([]string{
		"bash", "read", "write", "edit", "glob", "grep", "job_output", "job_kill",
		"web_fetch", "web_search",
	}, planTaskToolNames...) {
		if !names[want] {
			t.Fatalf("tool %q is missing from the Worker's tool list: %v", want, first.ToolNames)
		}
	}
	// Order stays stable across turns (prompt cache prefix).
	firstOrder := strings.Join(first.ToolNames, ",")
	runSessionForTest(t, r, "hi again", nil)
	if second := strings.Join(srv.LastRequest().ToolNames, ","); second != firstOrder {
		t.Fatalf("tool order changed between turns:\n%s\n%s", firstOrder, second)
	}

	// Defaults stay off: a bare Runner gets neither web nor plan tools.
	plain := startMock(t)
	r2 := newTestRunner(t, testConfig(plain, t.TempDir(), "mock-text"))
	runSessionForTest(t, r2, "hi", nil)
	plainNames := map[string]bool{}
	for _, n := range plain.LastRequest().ToolNames {
		plainNames[n] = true
	}
	for _, unwanted := range []string{"web_fetch", "web_search", plantaskToolNameForTest()} {
		if plainNames[unwanted] {
			t.Fatalf("default config exposed %q", unwanted)
		}
	}
}

func plantaskToolNameForTest() string { return planTaskToolNames[0] }

func TestSubagentWhitelistAcceptsBuiltinTools(t *testing.T) {
	dir := t.TempDir()
	r, err := New(Config{Model: "mock", Workdir: dir, BaseURL: "http://127.0.0.1:1",
		WebTools: true, PlanTasks: true})
	if err != nil {
		t.Fatal(err)
	}
	tools, handlers, err := r.toolsFor([]string{"web_fetch", "web_search", "TaskList"})
	if err != nil {
		t.Fatalf("web/task whitelist was refused: %v", err)
	}
	var names []string
	for _, bt := range tools {
		if info, err := bt.Info(context.Background()); err == nil {
			names = append(names, info.Name)
		}
	}
	if strings.Join(names, ",") != "web_fetch,web_search" {
		t.Fatalf("whitelist returned %v", names)
	}
	if len(handlers) != 1 {
		t.Fatalf("the plan task whitelist must add the plantask middleware, handlers=%d", len(handlers))
	}

	// Task tools without PlanTasks enabled are a clear configuration error.
	plain, err := New(Config{Model: "mock", Workdir: t.TempDir(), BaseURL: "http://127.0.0.1:1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := plain.toolsFor([]string{"TaskList"}); err == nil || !strings.Contains(err.Error(), "PlanTasks") {
		t.Fatalf("expected a PlanTasks configuration error, got %v", err)
	}
}

// AGENTS.md is injected once per Session: the second turn must reuse the persisted
// inserted message rather than appending the instructions again (prompt-cache and
// context-budget safety).
func TestAgentsMDInjectedOnceAcrossTurns(t *testing.T) {
	srv := startMock(t)
	dir := t.TempDir()
	marker := "PROJECT-RULES: single injection per session"
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte(marker), 0o644); err != nil {
		t.Fatal(err)
	}
	r := newTestRunner(t, testConfig(srv, dir, "mock-text", func(c *Config) { c.AgentsMD = true }))
	first, err := r.RunSession(context.Background(), RunRequest{System: "sys", Task: "first"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.RunSession(context.Background(), RunRequest{
		SessionID: first.SessionID, System: "sys", Task: "second",
	}, nil); err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, m := range srv.LastRequest().Messages {
		if strings.Contains(m.Content, marker) {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("AGENTS.md injected %d times in the second turn's messages, want 1", count)
	}
}
