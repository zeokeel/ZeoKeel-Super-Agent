package runtime

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/zeokeel/super-agent/internal/agent/mockllm"
	"github.com/zeokeel/super-agent/internal/domain"
	"github.com/zeokeel/super-agent/internal/engine"
	"github.com/zeokeel/super-agent/internal/store"
	"github.com/zeokeel/super-agent/internal/view"
)

// A prompt edited today must still be there tomorrow: losing configuration on
// restart is the kind of bug that makes a tool untrustworthy (design §6.5).
func TestSettingsSurviveRestart(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "zeokeel.db")

	st, err := store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	eng := engine.New(engine.RuleAssessor{})
	rt := New(eng, view.NewSnapshot())
	if err := rt.LoadSettings(st); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.SaveSettings("prompt", map[string]string{
		"worker": "你是我的执行助手，先跑测试再改代码。工作区 {{workspace}}（{{purpose}}）。",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.SaveSettings("execution", map[string]string{"concurrency": "4"}); err != nil {
		t.Fatal(err)
	}
	st.Close()

	// Second process.
	st2, err := store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()
	rt2 := New(engine.New(engine.RuleAssessor{}), view.NewSnapshot())
	if err := rt2.LoadSettings(st2); err != nil {
		t.Fatal(err)
	}
	vm := rt2.promptsVM()
	found := false
	for _, l := range vm.Layers {
		if string(l.ID) == "worker" {
			found = strings.Contains(l.Content, "你是我的执行助手，先跑测试再改代码。")
		}
	}
	if !found {
		t.Fatalf("the worker prompt did not survive the restart: %+v", vm.Layers)
	}
	if got := rt2.settingsSource(); got != "SQLite · kv" {
		t.Fatalf("the page must say where values live, got %q", got)
	}
}

// Without a store the page must not claim persistence it does not have.
func TestSettingsSayWhenTheyAreMemoryOnly(t *testing.T) {
	rt := New(engine.New(engine.RuleAssessor{}), view.NewSnapshot())
	if got := rt.settingsSource(); got != "内存（未接数据库）" {
		t.Fatalf("source %q", got)
	}
	if _, err := rt.SaveSettings("execution", map[string]string{"concurrency": "2"}); err != nil {
		t.Fatal(err)
	}
	if got := rt.settingsVM(rt.clock()).Source; got != "内存（未接数据库）" {
		t.Fatalf("settings page claims %q", got)
	}
}

// Editing the worker prompt must change what the model is told. A text box that
// saves but changes nothing is worse than no editor at all (design §5.3).
func TestEditedWorkerPromptReachesTheModel(t *testing.T) {
	srv, err := mockllm.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	srv.ToolName = "bash"
	srv.ToolArgs = `{"command":"echo hi"}`

	eng := engine.New(engine.RuleAssessor{})
	eng.Register(domain.Workspace{
		ID: "ws_web", Name: "web", Root: t.TempDir(), Kind: domain.KindRepo,
		Brief: domain.Brief{Purpose: "webapp", Keywords: []string{"webapp"}},
	}, engine.Policy{Version: "v1"})
	rt := New(eng, view.NewSnapshot())
	rt.EnableWorker(WorkerConfig{
		BaseURL: srv.URL, APIKey: "sk-mock", Model: "mock-tool", Root: t.TempDir(),
	})
	settleRuntime(t, rt)
	custom := "记住：改代码前先跑测试。CUSTOM-MARKER-42 工作区 {{workspace}}（{{purpose}}）。"
	if _, err := rt.SaveSettings("prompt", map[string]string{"worker": custom}); err != nil {
		t.Fatal(err)
	}
	if err := rt.Dispatch("在 webapp 里跑个命令"); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		req := srv.LastRequest()
		if len(req.Messages) > 0 && req.Messages[0].Role == "system" {
			if strings.Contains(req.Messages[0].Content, "CUSTOM-MARKER-42") {
				return
			}
			t.Fatalf("the model was told something else: %q", req.Messages[0].Content[:min(120, len(req.Messages[0].Content))])
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("the worker never ran")
}

// 并发执行器 in the panel decides how many tickets run at once, within sane bounds.
func TestConcurrencyComesFromSettings(t *testing.T) {
	rt := New(engine.New(engine.RuleAssessor{}), view.NewSnapshot())
	if got := rt.concurrency(); got != 2 {
		t.Fatalf("default = %d, want 2", got)
	}
	for _, c := range []struct {
		val  string
		want int
	}{{"1", 1}, {"4", 4}, {"0", 2}, {"abc", 2}, {"99", 8}} {
		if _, err := rt.SaveSettings("execution", map[string]string{"concurrency": c.val}); err != nil {
			t.Fatal(err)
		}
		if got := rt.concurrency(); got != c.want {
			t.Fatalf("%q → %d, want %d", c.val, got, c.want)
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// The settings page must not offer knobs that save and do nothing: that teaches
// people the page is decorative. Only wired settings are listed.
func TestSettingsPageListsOnlyWiredFields(t *testing.T) {
	rt := New(engine.New(engine.RuleAssessor{}), view.NewSnapshot())
	vm := rt.settingsVM(rt.clock())
	allowed := map[string]bool{
		"source.poll_interval":         true,
		"source.feishu_enabled":        true,
		"source.feishu_transport":      true,
		"source.dingtalk_enabled":      true,
		"source.dingtalk_transport":    true,
		"source.dingtalk_dws_cmd":      true,
		"source.dingtalk_bot_id":       true,
		"source.feishu_self_id":        true,
		"source.dingtalk_profile":      true,
		"source.dingtalk_listen_kind":  true,
		"source.dingtalk_chat_query":   true,
		"source.feishu_app_id":         true,
		"source.feishu_app_secret":     true,
		"source.feishu_chats":          true,
		"source.dingtalk_app_key":      true,
		"source.dingtalk_app_secret":   true,
		"source.dingtalk_robot":        true,
		"source.im_self":               true,
		"source.github_repo":           true,
		"source.github_login":          true,
		"execution.concurrency":        true,
		"execution.command_timeout":    true,
		"execution.prepare":            true,
		"execution.background_timeout": true,
		"execution.web_search_url":     true,
		"execution.plan_tasks":         true,
		"execution.agents_md":          true,
		"execution.web_tools":          true,
		"execution.model_timeout":      true,
		"people.allowlist":             true,
		"deploy.font_scale":            true,
	}
	for _, g := range vm.Groups {
		for _, f := range g.Fields {
			if f.Kind == view.SettingFact {
				continue // deployment facts are read-only information, not knobs
			}
			key := string(g.ID) + "." + f.Key
			if !allowed[key] {
				t.Fatalf("settings page shows %q, which nothing reads", key)
			}
		}
	}
	// And every wired knob has to be present, or it is unreachable.
	for key := range allowed {
		found := false
		for _, g := range vm.Groups {
			for _, f := range g.Fields {
				if string(g.ID)+"."+f.Key == key {
					found = true
				}
			}
		}
		if !found {
			t.Fatalf("wired setting %q is not on the page", key)
		}
	}
}

// Saving the allowlist must change who gets through immediately: a page that says
// "saved" while the agent still refuses the person you just added is lying.
func TestAllowlistSettingTakesEffect(t *testing.T) {
	eng := engine.New(engine.RuleAssessor{})
	eng.Register(domain.Workspace{
		ID: "ws_web", Name: "web", Root: "/tmp/web", Kind: domain.KindRepo,
		Brief: domain.Brief{Purpose: "webapp", Keywords: []string{"webapp"}},
	}, engine.Policy{Version: "v1"})
	rt := New(eng, view.NewSnapshot())

	n := 0
	ask := func(actor string) bool {
		n++
		out, err := eng.Receive(withThread(domain.Trigger{
			ID:     domain.Key(domain.WorkspaceScope(""), "dingtalk", "t-"+actor+"-"+strconv.Itoa(n)),
			Source: "dingtalk", Human: true,
			Addressed:    domain.Addressed{Direct: true},
			Text:         "把 webapp 的登录超时改成可配置",
			Actor:        domain.Actor{ID: actor},
			Conversation: domain.Conversation{Kind: domain.ConvDirect, ID: "dm"},
			At:           rt.clock(),
		}))
		if err != nil {
			t.Fatal(err)
		}
		_ = out
		// A colleague the policy admits gets an approval proposal; one it does not
		// admit is filtered before either a proposal or a Ticket exists.
		ev := evalNow(eng)
		return ev.Ticket != "" || ev.Proposal
	}
	if ask("colleague") {
		t.Fatal("nobody is allowed yet")
	}
	if _, err := rt.SaveSettings("people", map[string]string{"allowlist": "dingtalk:colleague"}); err != nil {
		t.Fatal(err)
	}
	if !ask("colleague") {
		t.Fatal("the saved allowlist did not take effect")
	}
	if ask("someone-else") {
		t.Fatal("the list granted access to someone it does not name")
	}

	// And revoking works the same way.
	if _, err := rt.SaveSettings("people", map[string]string{"allowlist": ""}); err != nil {
		t.Fatal(err)
	}
	if ask("colleague") {
		t.Fatal("clearing the list did not revoke access")
	}
}

// A typo in the list is an error on save, not a silent lockout.
func TestAllowlistSettingRejectsMalformedEntries(t *testing.T) {
	eng := engine.New(engine.RuleAssessor{})
	eng.Register(domain.Workspace{ID: "ws_web", Name: "web", Root: "/tmp/web",
		Kind: domain.KindRepo, Brief: domain.Brief{Purpose: "webapp"}}, engine.Policy{Version: "v1"})
	rt := New(eng, view.NewSnapshot())
	if _, err := rt.SaveSettings("people", map[string]string{"allowlist": "colleague"}); err == nil {
		t.Fatal("an entry without a source must be refused")
	}
}

// A scan fills the brief with what nobody typed: the stack, the commands, and the
// directory outline the Router reads (design §4.6).
func TestRefreshScansTheWorkspaceIntoTheBrief(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "cmd"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("# 演示项目\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	eng := engine.New(engine.RuleAssessor{})
	eng.Register(domain.Workspace{
		ID: "ws_web", Name: "webapp", Root: root, Kind: domain.KindRepo,
		Brief: domain.Brief{Purpose: "控制台", Keywords: []string{"webapp"}},
	}, engine.Policy{Version: "v1"})
	rt := New(eng, view.NewSnapshot())
	rt.RefreshWorkspaces()

	w, ok := eng.Workspace("ws_web")
	if !ok {
		t.Fatal("workspace vanished")
	}
	if w.Brief.Facts.Language != "Go" || w.Brief.Facts.TestCmd != "go test ./..." {
		t.Fatalf("facts not scanned: %+v", w.Brief.Facts)
	}
	if len(w.Brief.Facts.DirSummary) == 0 {
		t.Fatal("no directory outline in the brief")
	}
	// The person's words survive the machine's facts.
	if w.Brief.Purpose != "控制台" {
		t.Fatalf("purpose was overwritten: %q", w.Brief.Purpose)
	}
}
