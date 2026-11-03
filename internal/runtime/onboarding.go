package runtime

import (
	"fmt"
	"strings"

	"github.com/zeokeel/super-agent/internal/domain"
	"github.com/zeokeel/super-agent/internal/engine"
	"github.com/zeokeel/super-agent/internal/view"
)

// NeedsOnboarding reports whether this install has nothing configured yet. A
// fresh install must not open onto a dashboard that says "0 things to do": it has
// to ask what the agent is for (design §6.7).
func (r *Runtime) NeedsOnboarding() bool {
	return !r.onboardingDismissed() && len(r.eng.Workspaces()) == 0
}

func (r *Runtime) onboardingDismissed() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.settings["onboard.done"] == "1"
}

// SetNeedsOnboarding forces the wizard on (the command uses it when it finds an
// empty database), or off once the setup is saved. The flag is durable because a
// restart is exactly when a half-configured install must not look finished.
func (r *Runtime) SetNeedsOnboarding(on bool) {
	r.mu.Lock()
	if r.settings == nil {
		r.settings = map[string]string{}
	}
	base := r.settingsRev
	if on {
		// "0" rather than delete: the store only upserts, and a stale "1" would
		// otherwise survive a wipe that was meant to re-run the wizard.
		r.settings["onboard.done"] = "0"
	} else {
		r.settings["onboard.done"] = "1"
	}
	r.mu.Unlock()
	if _, err := r.persistSettings(base, true); err != nil {
		r.setErr(err.Error())
	}
	if !on {
		_ = r.MarkSetupComplete()
	}
	r.Publish()
}

// onboardingVM builds the wizard from what has been saved so far. Done-ness is
// derived from values, never from a stored flag, so a person who clears a field
// sees the step go back to unfinished.
func (r *Runtime) onboardingVM() *view.OnboardingVM {
	r.mu.Lock()
	values := make(map[string]string, len(r.settings))
	for k, v := range r.settings {
		values[k] = v
	}
	rev := r.settingsRev
	r.mu.Unlock()

	step := func(id view.OnboardingStepID, title, note string, fields []view.OnboardField, done bool, optional bool) view.OnboardingStep {
		return view.OnboardingStep{ID: id, Title: title, Note: note, Fields: fields, Done: done, Optional: optional}
	}
	modelDone := strings.TrimSpace(values["onboard.model"]) != ""
	wsDone := strings.TrimSpace(values["onboard.ws_name"]) != ""
	sourceDone := strings.TrimSpace(values["onboard.source_kind"]) != ""
	peopleDone := strings.TrimSpace(values["onboard.people"]) != ""

	vm := &view.OnboardingVM{Revision: rev, Active: r.NeedsOnboarding()}
	vm.Steps = []view.OnboardingStep{
		step(view.OnboardModel, "用哪个模型",
			"研判和执行都靠它。填一个 OpenAI 兼容端点即可。",
			[]view.OnboardField{
				{Key: "base_url", Label: "端点", Value: values["onboard.base_url"], Kind: view.SettingText, Help: "例如 https://api.deepseek.com/v1"},
				{Key: "model", Label: "模型", Value: values["onboard.model"], Kind: view.SettingText},
				{Key: "api_key", Label: "API Key", Value: values["onboard.api_key"], Kind: view.SettingSecret, Help: "保存在本机状态目录，权限 0600"},
			}, modelDone, false),
		step(view.OnboardWorkspace, "它管哪个仓库",
			"一个工作区对应一个仓库或一份数据。先建一个，以后可以再加。",
			[]view.OnboardField{
				{Key: "ws_name", Label: "名字", Value: values["onboard.ws_name"], Kind: view.SettingText, Help: "例如 webapp"},
				{Key: "ws_root", Label: "本地路径", Value: values["onboard.ws_root"], Kind: view.SettingText, Help: "例如 ~/code/webapp"},
				{Key: "ws_purpose", Label: "一句话说明", Value: values["onboard.ws_purpose"], Kind: view.SettingText, Help: "它进工作区简报，研判时用"},
				{Key: "ws_keywords", Label: "关键词", Value: values["onboard.ws_keywords"], Kind: view.SettingText, Help: "逗号分隔，用来把消息路由到这里"},
			}, wsDone, false),
		step(view.OnboardSource, "从哪里接活",
			"本地面板永远可用；接 GitHub 或 IM 就能从聊天里派活。",
			[]view.OnboardField{
				{Key: "source_kind", Label: "来源", Value: values["onboard.source_kind"], Kind: view.SettingSelect,
					Options: []string{"local", "github", "feishu", "dingtalk"}},
				{Key: "source_repo", Label: "GitHub 仓库", Value: values["onboard.source_repo"], Kind: view.SettingText, Help: "owner/name，可从设置页补"},
			}, sourceDone, true),
		step(view.OnboardPeople, "谁可以让它干活",
			"没在这里的人，它的消息不会被执行。多个账号用逗号分隔。",
			[]view.OnboardField{
				{Key: "people", Label: "允许的账号", Value: values["onboard.people"], Kind: view.SettingText,
					Help: "来源:账号，逗号分隔，例如 dingtalk:ding_zhangsan, github:lin"},
			}, peopleDone, true),
	}
	for _, s := range vm.Steps {
		if s.Done || s.Optional {
			continue
		}
	}
	vm.Ready = modelDone && wsDone
	return vm
}

// SaveOnboarding persists one step of the wizard. The workspace step registers a
// real workspace immediately, so the app is usable the moment setup finishes
// rather than after a restart.
func (r *Runtime) SaveOnboarding(values map[string]string) (uint64, error) {
	r.mu.Lock()
	if r.settings == nil {
		r.settings = map[string]string{}
	}
	for k, v := range values {
		r.settings["onboard."+k] = strings.TrimSpace(v)
	}
	merged := map[string]string{}
	for k, v := range r.settings {
		merged[strings.TrimPrefix(k, "onboard.")] = v
	}
	base := r.settingsRev
	r.mu.Unlock()

	// Commit the wizard values before registering anything: a crash after this
	// point leaves a configured install, never a workspace whose model was lost.
	rev, err := r.persistSettings(base, true)
	if err != nil {
		return base, err
	}
	// Registering the workspace is what makes "开始使用" mean something.
	if name := values["ws_name"]; name != "" {
		if err := r.addOnboardWorkspace(merged); err != nil {
			return rev, err
		}
	}
	// The model step has to reach the executor, otherwise the wizard collects
	// settings nobody reads (design §6.7: setup must leave a working agent).
	if model := merged["model"]; model != "" {
		r.SetWorkerModel(merged["base_url"], merged["api_key"], model)
		// Persist the same facts in the panel's 模型 group, so the settings page
		// shows them and CheckModel can use them without onboarding state.
		if _, err := r.SaveSettings("worker", map[string]string{
			"base_url": merged["base_url"], "api_key": merged["api_key"], "model": model,
		}); err != nil {
			return rev, err
		}
	}
	// P1-15: 来源 step configures the real channel and starts it.
	if err := r.applyOnboardSource(values); err != nil {
		return rev, err
	}
	// Demo/skip installs finish without a model; the overview says what is missing.
	if values["skip"] == "1" || values["demo"] == "1" {
		if _, err := r.SaveSettings("onboard", map[string]string{"demo": "1", "skip": "1"}); err != nil {
			return rev, err
		}
	}
	r.Publish()
	return rev, nil
}

// persistSettings writes the whole in-memory settings map and adopts the revision
// the store assigned. With no store it just advances the in-memory counter: the
// page still reports "内存", it does not pretend to be durable.
func (r *Runtime) persistSettings(base uint64, checkBase bool) (uint64, error) {
	r.mu.Lock()
	snapshot := make(map[string]string, len(r.settings))
	for k, v := range r.settings {
		snapshot[k] = v
	}
	st := r.settingsStore
	r.mu.Unlock()
	if st == nil {
		r.mu.Lock()
		r.settingsRev++
		rev := r.settingsRev
		r.mu.Unlock()
		return rev, nil
	}
	rev, err := st.SaveSettings(snapshot, base, checkBase)
	if err != nil {
		return base, err
	}
	r.mu.Lock()
	r.settingsRev = rev
	r.mu.Unlock()
	return rev, nil
}

// FinishOnboarding marks setup complete. It refuses while a required step is
// missing: an agent with no workspace would accept work it cannot do.
func (r *Runtime) FinishOnboarding() error {
	if r.DemoMode() {
		// Demo/skip installs are complete by definition; the missing model is
		// reported on the overview, not hidden behind a wizard that never ends.
		r.SetNeedsOnboarding(false)
		return r.MarkSetupComplete()
	}
	vm := r.onboardingVM()
	if !vm.Ready {
		missing := vm.Incomplete()
		if len(missing) > 0 {
			return fmt.Errorf("runtime: 还差 %s", strings.Join(missing, "、"))
		}
		return fmt.Errorf("runtime: 设置还没完成")
	}
	// Persist the completion flag and the setup marker together with the rest of
	// the settings, so `zeokeel status` can see it without opening the engine.
	r.SetNeedsOnboarding(false)
	_ = r.MarkSetupComplete()
	return nil
}

// addOnboardWorkspace registers the workspace described by the wizard.
func (r *Runtime) addOnboardWorkspace(v map[string]string) error {
	name := strings.TrimSpace(v["ws_name"])
	if name == "" {
		return nil
	}
	id := newWorkspaceID()
	root := normalizedRoot(v["ws_root"])
	if root == "" {
		root = "~/" + name
	}
	kw := []string{name}
	for _, k := range strings.Split(v["ws_keywords"], ",") {
		if k = strings.TrimSpace(k); k != "" {
			kw = append(kw, k)
		}
	}
	w := domain.Workspace{
		ID: id, Name: name, Root: root, Kind: domain.KindRepo,
		Brief: domain.Brief{Purpose: v["ws_purpose"], Keywords: kw},
	}
	// The allowlist is what makes the agent answer anyone at all: without it a
	// colleague's message is refused before Eval ever sees it.
	allowed, err := parseAllowlist(v["people"])
	if err != nil {
		return err
	}
	policy := engine.Policy{Version: "v1", Allowed: allowed}
	r.mu.Lock()
	p := r.persist
	r.mu.Unlock()
	if p != nil {
		return p.Register(w, policy)
	}
	r.eng.Register(w, policy)
	return nil
}

// applyOnboardSource turns the wizard's 来源 step into real source settings and
// lets the runtime's settings hook start/stop the channel (P1-15). It never
// invents credentials: enabling a channel without them produces the honest
// "配置不完整" row until the person fills them in.
func (r *Runtime) applyOnboardSource(values map[string]string) error {
	kind := strings.TrimSpace(strings.ToLower(values["source_kind"]))
	settings := map[string]string{}
	switch kind {
	case "", "local":
		return nil
	case "github":
		if repo := strings.TrimSpace(values["source_repo"]); repo != "" {
			settings["github_repo"] = repo
		}
	case "feishu":
		settings["feishu_enabled"] = "1"
		settings["feishu_transport"] = "longpoll"
	case "dingtalk":
		settings["dingtalk_enabled"] = "1"
	case "im", "chat":
		settings["feishu_enabled"] = "1"
		settings["dingtalk_enabled"] = "1"
	default:
		return fmt.Errorf("来源 %q 不支持（local/github/feishu/dingtalk）", kind)
	}
	if len(settings) == 0 {
		return nil
	}
	_, err := r.SaveSettings(string(view.SettingsSource), settings)
	return err
}

func slug(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ' || r == '-' || r == '_':
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		out = "workspace"
	}
	return out
}

// SavedModel returns the model the operator configured through the wizard, if
// any. Callers use it to fill in what the command line did not provide, so a
// configured install starts working without extra flags.
func (r *Runtime) SavedModel() (baseURL, apiKey, model string, ok bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	baseURL = r.settings["onboard.base_url"]
	apiKey = r.settings["onboard.api_key"]
	model = r.settings["onboard.model"]
	return baseURL, apiKey, model, model != ""
}

// parseAllowlist reads "source:actor, source:actor". Entries are strict on
// purpose: a typo here silently locks a colleague out, so it must fail loudly in
// the wizard instead of being guessed at.
func parseAllowlist(raw string) (map[string]map[string]bool, error) {
	out := map[string]map[string]bool{}
	for _, part := range strings.Split(raw, ",") {
		entry := strings.TrimSpace(part)
		if entry == "" {
			continue
		}
		source, actor, ok := strings.Cut(entry, ":")
		source, actor = strings.TrimSpace(source), strings.TrimSpace(actor)
		if !ok || source == "" || actor == "" {
			return nil, fmt.Errorf("允许名单要写成「来源:账号」，收到 %q", entry)
		}
		if out[source] == nil {
			out[source] = map[string]bool{}
		}
		out[source][actor] = true
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}
