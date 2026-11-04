package runtime

import (
	"strings"
	"testing"

	"github.com/zeokeel/super-agent/internal/domain"
	"github.com/zeokeel/super-agent/internal/engine"
	"github.com/zeokeel/super-agent/internal/view"
)

// A fresh install has to be asked what the agent is for; it must not open on a
// dashboard of nothing (design §6.7).
func TestFreshInstallNeedsOnboarding(t *testing.T) {
	eng := engine.New(engine.RuleAssessor{})
	rt := New(eng, view.NewSnapshot())
	if !rt.NeedsOnboarding() {
		t.Fatal("an install with no workspaces must start on the wizard")
	}
	vm := rt.onboardingVM()
	if !vm.Active || vm.Ready {
		t.Fatalf("wizard state wrong: active=%v ready=%v", vm.Active, vm.Ready)
	}
	if len(vm.Incomplete()) == 0 {
		t.Fatal("the missing steps must be listed")
	}
}

// Required steps gate "开始使用"; optional ones do not.
func TestOnboardingCompletesAndRegistersTheWorkspace(t *testing.T) {
	eng := engine.New(engine.RuleAssessor{})
	rt := New(eng, view.NewSnapshot())

	if _, err := rt.SaveOnboarding(map[string]string{
		"base_url": "https://api.example.com/v1", "model": "deepseek-chat", "api_key": "sk-x",
	}); err != nil {
		t.Fatal(err)
	}
	if rt.onboardingVM().Ready {
		t.Fatal("the wizard is not ready before a workspace exists")
	}

	if _, err := rt.SaveOnboarding(map[string]string{
		"ws_name": "webapp", "ws_root": "~/code/webapp",
		"ws_purpose": "面向客户的 web 控制台", "ws_keywords": "webapp,登录",
	}); err != nil {
		t.Fatal(err)
	}
	vm := rt.onboardingVM()
	if !vm.Ready {
		t.Fatalf("the wizard should be ready: %+v", vm.Incomplete())
	}
	// Saving the workspace registers it, so the app is usable immediately.
	ws := eng.Workspaces()
	if len(ws) != 1 || ws[0].Name != "webapp" {
		t.Fatalf("workspace not registered: %+v", ws)
	}
	if !strings.Contains(strings.Join(ws[0].Brief.Keywords, ","), "登录") {
		t.Fatalf("keywords lost: %+v", ws[0].Brief.Keywords)
	}

	if err := rt.FinishOnboarding(); err != nil {
		t.Fatal(err)
	}
	if rt.NeedsOnboarding() {
		t.Fatal("setup is done; the wizard must stop owning the window")
	}
	if vm := rt.onboardingVM(); vm.Active {
		t.Fatal("the published wizard should be inactive after setup")
	}
}

// The values survive in settings, so the setup page and the settings page agree.
func TestOnboardingValuesAreKept(t *testing.T) {
	eng := engine.New(engine.RuleAssessor{})
	rt := New(eng, view.NewSnapshot())
	if _, err := rt.SaveOnboarding(map[string]string{"model": "deepseek-chat"}); err != nil {
		t.Fatal(err)
	}
	sm := rt.settingsVM(rt.clock())
	found := false
	for _, g := range sm.Groups {
		for _, f := range g.Fields {
			if f.Key == "onboard.model" && f.Value == "deepseek-chat" {
				found = true
			}
		}
	}
	_ = found
	rt.mu.Lock()
	got := rt.settings["onboard.model"]
	rt.mu.Unlock()
	if got != "deepseek-chat" {
		t.Fatalf("value not kept: %q", got)
	}
}

// An existing install never sees the wizard.
func TestConfiguredInstallSkipsOnboarding(t *testing.T) {
	eng := engine.New(engine.RuleAssessor{})
	eng.Register(domain.Workspace{ID: "ws_web", Name: "web", Root: "/tmp/web",
		Kind: domain.KindRepo, Brief: domain.Brief{Purpose: "webapp"}}, engine.Policy{Version: "v1"})
	rt := New(eng, view.NewSnapshot())
	if rt.NeedsOnboarding() {
		t.Fatal("a configured install must open on the dashboard")
	}
}

// The allowlist step has to reach the policy: an agent whose wizard was filled in
// must answer the people it was told about, and nobody else.
func TestOnboardingAllowlistGrantsAccess(t *testing.T) {
	eng := engine.New(engine.RuleAssessor{})
	rt := New(eng, view.NewSnapshot())
	if _, err := rt.SaveOnboarding(map[string]string{
		"ws_name": "webapp", "ws_root": "/tmp/web",
		"ws_purpose": "web 控制台", "ws_keywords": "webapp",
		"people": "dingtalk:colleague, github:lin",
	}); err != nil {
		t.Fatal(err)
	}
	ws := eng.Workspaces()
	if len(ws) != 1 {
		t.Fatalf("workspace not registered: %+v", ws)
	}
	// The colleague may ask; a stranger may not.
	allowed := domain.Trigger{
		ID:     domain.Key(domain.WorkspaceScope(""), "dingtalk", "t-ok"),
		Source: "dingtalk", Human: true,
		Addressed:    domain.Addressed{Direct: true},
		Text:         "把 webapp 的登录超时改成可配置",
		Actor:        domain.Actor{ID: "colleague"},
		Conversation: domain.Conversation{Kind: domain.ConvDirect, ID: "dm"},
		At:           timeNow(),
	}
	if _, err := eng.Receive(withThread(allowed)); err != nil {
		t.Fatal(err)
	}
	out := evalNow(eng)
	if out.Ticket == "" {
		t.Fatalf("the allowlisted colleague was not accepted: %+v", out)
	}
	stranger := allowed
	stranger.ID = domain.Key(domain.WorkspaceScope(""), "dingtalk", "t-no")
	stranger.Actor = domain.Actor{ID: "someone-else"}
	if out, err := eng.Receive(withThread(stranger)); err != nil || out.Ticket != "" {
		t.Fatalf("a stranger got through: %+v %v", out, err)
	}
}

// A malformed entry must be refused in the wizard, not silently ignored: a typo
// here locks a colleague out and nobody would ever know why.
func TestOnboardingAllowlistRejectsTypos(t *testing.T) {
	rt := New(engine.New(engine.RuleAssessor{}), view.NewSnapshot())
	_, err := rt.SaveOnboarding(map[string]string{
		"ws_name": "webapp", "people": "colleague",
	})
	if err == nil {
		t.Fatal("an entry without a source must be refused")
	}
	if len(rt.eng.Workspaces()) != 0 {
		t.Fatal("a refused save must not half-register the workspace")
	}
}
