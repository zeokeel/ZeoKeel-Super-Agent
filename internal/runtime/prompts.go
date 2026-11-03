package runtime

// Prompt layers: the router/worker/eval-rules prompts plus the team's three
// sections (communication / scope / delivery), each editable and versioned.
//
// Versioning lives in the store's kv table, not a schema table: the history is a
// short audit trail per layer (last 20 saves), it must survive a restart, and a
// new table needs a store migration owned by another scope.
// `prompt.base.<layer>` remembers the hash of the built-in default that was in
// effect when the layer was first overridden, so a later build that changes the
// default can say "Default updated" instead of silently keeping the old text.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/zeokeel/super-agent/internal/assess"
	"github.com/zeokeel/super-agent/internal/prompts"
	"github.com/zeokeel/super-agent/internal/view"
)

// Team layers are literals because view.PromptLayerID is owned by the UI scope;
// the editor renders every layer the runtime publishes, so no view change is
// needed for them to be editable.
const (
	promptCommunication view.PromptLayerID = "communication"
	promptScope         view.PromptLayerID = "scope"
	promptDelivery      view.PromptLayerID = "delivery"
)

// promptLayerIDs is the order the editor shows: what judges, what works, what
// the rules mean, then the team's three sections.
func promptLayerIDs() []view.PromptLayerID {
	return []view.PromptLayerID{
		view.PromptRouter, view.PromptWorker, view.PromptEvalRules,
		promptCommunication, promptScope, promptDelivery,
	}
}

// promptDefaults are the prompts this build ships. 恢复默认 writes these back, so
// they are the one place a person can compare an edit against (design §5.3).
func promptDefaults() map[view.PromptLayerID]string {
	return map[view.PromptLayerID]string{
		view.PromptRouter:    assess.SystemPrompt(),
		view.PromptWorker:    prompts.WorkerTemplate(),
		view.PromptEvalRules: evalRulesNote,
		promptCommunication:  teamCommunication,
		promptScope:          teamScope,
		promptDelivery:       teamDelivery,
	}
}

// evalRulesNote explains the rules Eval evaluates.
const evalRulesNote = `评估规则决定一条消息算不算工作请求。每条启用的规则都必须给出命中结论，
少一条整批判为无效，而不是"看起来对"就接受。

- implementation：明确要求修改这个仓库的代码或配置
- data-request：要求从这个工作区取数或出报表
- off-topic：与这个工作区无关的闲聊、通知或公告

改动规则时，请同时说明"什么情况下不该命中"，否则规则会吞掉边界消息。`

// The team's three sections. They are appended to the Worker instruction after
// the built-in sections, so a team that edits only its own voice never loses the
// safety/tool sections the build ships.
const (
	teamCommunication = `沟通方式：回答结论先行，再给证据。
- 对同事：短句、可执行；不超过一屏。
- 对 operator：说清做了什么、验证了什么、还缺什么。
- 不确定就问一句具体的问题，不猜。`
	teamScope = `改动范围：只动这个工单需要动的文件。
- 不做顺手重构；发现别的问题先记录，不扩大改动面。
- 工作区外的东西一律不碰（工具会拒绝，拒绝是信息不是墙）。`
	teamDelivery = `交付标准：每一件交付物都要能被验证。
- present() 每个产出；report(state="ready") 前先自己验证一次。
- 没验证就明说没验证；不要用"应该可以"结案。`
)

// maxPromptRunes rejects an over-long layer instead of truncating it: a silently
// cut prompt is a prompt nobody wrote.
const maxPromptRunes = 20000

// placeholderRE finds {{name}} references so unknown keys can be rejected.
var placeholderRE = regexp.MustCompile(`\{\{\s*([A-Za-z0-9_]+)\s*\}\}`)

// allowedPromptVars is the closed set the composer can expand. A new one must be
// added here, in the worker composer and in the editor's help together.
var allowedPromptVars = map[string]bool{"workspace": true, "purpose": true}

// requiredPromptVars lists the placeholders a layer must keep when overridden.
var requiredPromptVars = map[view.PromptLayerID][]string{
	view.PromptWorker: {"workspace", "purpose"},
}

// PromptHistoryEntry is one saved revision of one layer.
type PromptHistoryEntry struct {
	At       time.Time `json:"at"`
	Revision uint64    `json:"revision"`
	Content  string    `json:"content"`
	Hash     string    `json:"hash"`
}

func promptHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:16]
}

// promptsVM projects the editable prompt layers.
func (r *Runtime) promptsVM() *view.PromptVM {
	r.mu.Lock()
	values := make(map[string]string, len(r.settings))
	for k, v := range r.settings {
		values[k] = v
	}
	rev := r.settingsRev
	st := r.settingsStore
	r.mu.Unlock()

	defaults := promptDefaults()
	vm := &view.PromptVM{Source: r.settingsSource(), Revision: rev}
	for _, id := range promptLayerIDs() {
		content := defaults[id]
		if saved, ok := values["prompt."+string(id)]; ok && strings.TrimSpace(saved) != "" {
			content = saved
		}
		note := layerNote(id)
		if st != nil {
			// "Default updated": the build's default changed after this layer was
			// first overridden. The saved text is still what the model gets; the
			// note is how the operator learns a newer default exists.
			if base, ok, _ := st.Get(promptBaseKey(id)); ok && base != "" && base != promptHash(defaults[id]) {
				note += "（内置默认值已更新：编辑页可查看新版，或恢复默认）"
			}
		}
		layer := view.PromptLayer{
			ID: id, Label: layerLabel(id), Note: note,
			Content: content, Default: defaults[id], Editable: true,
		}
		if content != defaults[id] {
			layer.Revision = rev
		}
		vm.Layers = append(vm.Layers, layer)
	}
	return vm
}

// preparePromptSave validates and normalizes one prompt save. It returns the
// value map to write (with no-op/identical keys removed) or a refusal. Nothing is
// truncated: a layer that is too long is rejected so the person can edit it.
func (r *Runtime) preparePromptSave(values map[string]string) (map[string]string, error) {
	defaults := promptDefaults()
	known := map[view.PromptLayerID]bool{}
	for _, id := range promptLayerIDs() {
		known[id] = true
	}
	out := make(map[string]string, len(values))
	for k, v := range values {
		if k == "__revision" {
			out[k] = v
			continue
		}
		id := view.PromptLayerID(k)
		if !known[id] {
			return nil, fmt.Errorf("runtime: 未知提示词层 %q（可用：%s）", k, layerList())
		}
		if n := len([]rune(v)); n > maxPromptRunes {
			return nil, fmt.Errorf("runtime: 提示词层 %q 超长（%d 字 > 上限 %d），请精简后重试（不会截断保存）", k, n, maxPromptRunes)
		}
		for _, m := range placeholderRE.FindAllStringSubmatch(v, -1) {
			if !allowedPromptVars[m[1]] {
				return nil, fmt.Errorf("runtime: 提示词层 %q 用了未知变量 {{%s}}（可用：workspace / purpose）", k, m[1])
			}
		}
		for _, name := range requiredPromptVars[id] {
			if !strings.Contains(v, "{{"+name+"}}") {
				return nil, fmt.Errorf("runtime: 提示词层 %q 必须保留占位符 {{%s}}", k, name)
			}
		}
		r.mu.Lock()
		current := r.settings["prompt."+k]
		r.mu.Unlock()
		if v == current {
			continue // identical to what is already saved: no write, no history
		}
		if strings.TrimSpace(v) != "" && v == defaults[id] {
			out[k] = "" // identical to the built-in default: clearing the override
			continue
		}
		out[k] = v
	}
	return out, nil
}

// recordPromptMeta appends a history entry for each changed layer and remembers
// the default hash in effect at the first override (for "Default updated").
func (r *Runtime) recordPromptMeta(values map[string]string, rev uint64) {
	r.mu.Lock()
	st := r.settingsStore
	now := r.clock()
	r.mu.Unlock()
	if st == nil {
		return
	}
	defaults := promptDefaults()
	for k, v := range values {
		if k == "__revision" {
			continue
		}
		id := view.PromptLayerID(k)
		if _, ok := defaults[id]; !ok {
			continue
		}
		if _, ok, _ := st.Get(promptBaseKey(id)); !ok {
			_ = st.Set(promptBaseKey(id), promptHash(defaults[id]))
		}
		if strings.TrimSpace(v) == "" {
			continue // clearing an override is not a new text version
		}
		history := r.PromptHistory(id)
		history = append(history, PromptHistoryEntry{At: now, Revision: rev, Content: v, Hash: promptHash(v)})
		if len(history) > 20 {
			history = history[len(history)-20:]
		}
		if raw, err := json.Marshal(history); err == nil {
			_ = st.Set(promptHistoryKey(id), string(raw))
		}
	}
}

// PromptHistory returns the saved versions of one layer, oldest first.
func (r *Runtime) PromptHistory(id view.PromptLayerID) []PromptHistoryEntry {
	r.mu.Lock()
	st := r.settingsStore
	r.mu.Unlock()
	if st == nil {
		return nil
	}
	raw, ok, err := st.Get(promptHistoryKey(id))
	if err != nil || !ok || raw == "" {
		return nil
	}
	var out []PromptHistoryEntry
	if json.Unmarshal([]byte(raw), &out) != nil {
		return nil
	}
	return out
}

// PromptDefaultUpdated reports whether the build's default for a layer changed
// since the layer was first overridden.
func (r *Runtime) PromptDefaultUpdated(id view.PromptLayerID) bool {
	r.mu.Lock()
	st := r.settingsStore
	r.mu.Unlock()
	if st == nil {
		return false
	}
	base, ok, err := st.Get(promptBaseKey(id))
	if err != nil || !ok || base == "" {
		return false
	}
	return base != promptHash(promptDefaults()[id])
}

func promptBaseKey(id view.PromptLayerID) string    { return "prompt.base." + string(id) }
func promptHistoryKey(id view.PromptLayerID) string { return "prompt.history." + string(id) }

// teamSections returns the team's three sections in order, using the saved
// override when there is one. They are appended to the Worker instruction.
func (r *Runtime) teamSections() []string {
	defaults := promptDefaults()
	out := make([]string, 0, 3)
	for _, id := range []view.PromptLayerID{promptCommunication, promptScope, promptDelivery} {
		if body := strings.TrimSpace(r.effectivePrompt(id, defaults[id])); body != "" {
			out = append(out, body)
		}
	}
	return out
}

// ReloadPrompts implements the panel's optional PromptReloader.
func (r *Runtime) ReloadPrompts() (uint64, error) {
	r.Publish()
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.settingsRev, nil
}

func layerList() string {
	names := make([]string, 0, len(promptLayerIDs()))
	for _, id := range promptLayerIDs() {
		names = append(names, string(id))
	}
	return strings.Join(names, ", ")
}

func layerLabel(id view.PromptLayerID) string {
	switch id {
	case view.PromptRouter:
		return "研判提示词"
	case view.PromptWorker:
		return "执行提示词"
	case view.PromptEvalRules:
		return "评估规则说明"
	case promptCommunication:
		return "团队 · 沟通方式"
	case promptScope:
		return "团队 · 改动范围"
	case promptDelivery:
		return "团队 · 交付标准"
	}
	return string(id)
}

func layerNote(id view.PromptLayerID) string {
	switch id {
	case view.PromptRouter:
		return "决定「算不算工作、派给哪个工作区」。它从不执行、从不回复。"
	case view.PromptWorker:
		return "决定「怎么干活」：工具选择、交付物与完成标准。必须保留 {{workspace}} / {{purpose}}。"
	case view.PromptEvalRules:
		return "给规则本身写的说明，会随规则一起进入研判。"
	case promptCommunication:
		return "追加到执行器提示词：怎么跟同事和 operator 说话。"
	case promptScope:
		return "追加到执行器提示词：改动范围与不做的事。"
	case promptDelivery:
		return "追加到执行器提示词：交付物与验证标准。"
	}
	return ""
}
