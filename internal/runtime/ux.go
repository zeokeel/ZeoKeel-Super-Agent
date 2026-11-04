package runtime

// UI/UX backend for the interaction audit (P0-A/P0-1/P0-B/P0-C/P0-2): model
// settings + probe, composer targets, ticket actions, execution-path truth,
// unified attention inbox and onboarding skip/demo.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/zeokeel/super-agent/internal/domain"
	"github.com/zeokeel/super-agent/internal/engine"
	"github.com/zeokeel/super-agent/internal/view"
)

// --- P0-A: model settings ---

// workerSettingKeys are the editable model fields (settings group "worker").
const (
	settingWorkerBaseURL = "worker.base_url"
	settingWorkerAPIKey  = "worker.api_key"
	settingWorkerModel   = "worker.model"
)

// ApplyModelSettings loads the model fields into the Worker configuration. It is
// called at startup (after LoadSettings) and after a successful settings save, so
// "保存即生效" also holds for the model (P0-A).
func (r *Runtime) ApplyModelSettings() {
	r.mu.Lock()
	base := strings.TrimSpace(r.settings[settingWorkerBaseURL])
	key := strings.TrimSpace(r.settings[settingWorkerAPIKey])
	model := strings.TrimSpace(r.settings[settingWorkerModel])
	cfg := r.worker
	if cfg == nil {
		r.mu.Unlock()
		return
	}
	// Empty means "not configured here": keep whatever the onboarding/flags gave.
	if base != "" {
		cfg.BaseURL = base
	}
	if key != "" {
		cfg.APIKey = key
	}
	if model != "" {
		cfg.Model = model
	}
	r.mu.Unlock()
	r.Publish()
}

// CheckModel probes the configured model with one small non-streaming chat call.
// It classifies auth errors separately so the wizard can say "key 不对" instead of
// a generic network error.
func (r *Runtime) CheckModel(ctx context.Context) (ok bool, detail string) {
	cfg := r.workerConfig()
	var base, key, model string
	if cfg != nil {
		base, key, model = cfg.BaseURL, cfg.APIKey, cfg.Model
	}
	r.mu.Lock()
	if strings.TrimSpace(base) == "" {
		base = strings.TrimSpace(r.settings[settingWorkerBaseURL])
	}
	if strings.TrimSpace(key) == "" {
		key = strings.TrimSpace(r.settings[settingWorkerAPIKey])
	}
	if strings.TrimSpace(model) == "" {
		model = strings.TrimSpace(r.settings[settingWorkerModel])
	}
	r.mu.Unlock()
	if base == "" || model == "" {
		return false, "未配置模型：请填写端点与模型名"
	}
	if ctx == nil {
		ctx = context.Background()
	}
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	body, _ := json.Marshal(map[string]any{
		"model":      model,
		"messages":   []map[string]string{{"role": "user", "content": "ping"}},
		"max_tokens": 1,
		"stream":     false,
	})
	url := strings.TrimRight(base, "/") + "/chat/completions"
	req, err := http.NewRequestWithContext(cctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return false, "端点地址无效：" + err.Error()
	}
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := httpDoModel(req)
	if err != nil {
		return false, "连接失败：" + err.Error()
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	switch {
	case resp.StatusCode == 200:
		return true, "模型可用：" + model
	case resp.StatusCode == 401 || resp.StatusCode == 403:
		return false, fmt.Sprintf("鉴权失败（HTTP %d）：API Key 不正确或没有权限", resp.StatusCode)
	case resp.StatusCode == 404:
		return false, "端点或模型名不存在（HTTP 404）：检查 base_url 是否以 /v1 结尾、模型名是否拼错"
	default:
		msg := strings.TrimSpace(string(raw))
		if len([]rune(msg)) > 200 {
			msg = string([]rune(msg)[:200]) + "…"
		}
		return false, fmt.Sprintf("模型返回 HTTP %d：%s", resp.StatusCode, msg)
	}
}

// httpDoModel exists so tests never touch the network.
var httpDoModel = func(req *http.Request) (*http.Response, error) {
	return (&http.Client{Timeout: 10 * time.Second}).Do(req)
}

// --- P0-1: composer target ---

// DispatchTo sends a panel message to an explicit workspace (composer target
// "工作区 X"). An empty workspace keeps the super-agent chat path.
func (r *Runtime) DispatchTo(workspaceID, text string) error {
	ws := domain.WorkspaceID(strings.TrimSpace(workspaceID))
	if ws == "" {
		return r.Dispatch(text)
	}
	r.commandMu.Lock()
	defer r.commandMu.Unlock()
	r.mu.Lock()
	p := r.persist
	r.mu.Unlock()
	if !r.hasWorkspace(ws) {
		return fmt.Errorf("工作区 %q 不存在", workspaceID)
	}
	call := r.eng.Dispatch
	if p != nil {
		call = p.Dispatch
	}
	out, err := call(engine.DispatchInput{Text: text, Workspace: ws, At: r.clock()})
	if err != nil {
		r.setErr(err.Error())
		return err
	}
	outs, evalErr := r.EvaluateDue(r.clock())
	if evalErr != nil {
		return evalErr
	}
	out = mergeEvalOutcome(out, outs)
	r.appendChat(view.ChatUser, view.ChatNote, text)
	switch {
	case out.Ticket != "":
		r.appendChat(view.ChatSystem, view.ChatOutcome,
			fmt.Sprintf("已派发到 %s：#%d", workspaceID, ticketNumber(r.eng, out.Ticket)))
	default:
		r.appendChat(view.ChatAssistant, view.ChatAnswer, r.answerChat(text))
	}
	r.setErr("")
	r.Publish()
	return nil
}

// workspaceRootOf / executionRootOf are the path facts the Ticket detail shows.
func workspaceRootOf(r *Runtime, t domain.Ticket) string {
	if ws, ok := r.eng.Workspace(t.Workspace); ok {
		return ws.Root
	}
	return ""
}

func executionRootOf(r *Runtime, t domain.Ticket) string {
	cfg := r.workerConfig()
	if cfg == nil {
		return ""
	}
	return ticketWorkdir(cfg, t)
}

// absoluteArtifactPath turns a stored deliverable path into the absolute path in
// the execution copy, so 打开/复制 always names a real file. Stored paths may be
// relative to the Ticket workdir.
func (r *Runtime) absoluteArtifactPath(a domain.Artifact) string {
	if strings.TrimSpace(a.Path) == "" {
		return ""
	}
	if filepath.IsAbs(a.Path) {
		return filepath.Clean(a.Path)
	}
	base := ""
	if tk, ok := r.ticketSnapshot(a.Ticket); ok {
		base = executionRootOf(r, tk)
	}
	if base == "" {
		return a.Path
	}
	return filepath.Join(base, a.Path)
}

// --- P0-B: ticket actions ---

// TicketAction is one action a person can take on a Ticket from the execution
// page. The view-independent shape keeps the projection testable and lets the UI
// add buttons without the runtime knowing the widget toolkit.
type TicketAction struct {
	ID        string
	Number    int
	Title     string
	Stage     string
	StageID   string
	CanStop   bool
	CanRetry  bool
	CanAnswer bool
	// Actions is the render-ready list: stop / retry / answer.
	Actions []string
}

// TicketActions lists the Tickets that accept a person's action right now.
func (r *Runtime) TicketActions() []TicketAction {
	now := r.clock()
	var out []TicketAction
	for _, t := range r.ticketSnapshots() {
		if t.Ended != nil {
			continue
		}
		st := domain.StageOf(&t, now)
		act := TicketAction{
			ID: string(t.ID), Number: t.Number, Title: t.Title,
			Stage: stageWord(st), StageID: string(st),
			CanStop:   st == domain.StageWorking || st == domain.StageWorkingQueued,
			CanRetry:  t.CanRetry(),
			CanAnswer: t.Attention != nil,
		}
		if act.CanStop {
			act.Actions = append(act.Actions, "stop")
		}
		if act.CanRetry {
			act.Actions = append(act.Actions, "retry")
		}
		if act.CanAnswer {
			act.Actions = append(act.Actions, "answer")
		}
		out = append(out, act)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Number < out[j].Number })
	return out
}

// --- P0-C: execution paths ---

// ArtifactPath is one deliverable's location, for "打开副本 / 复制路径".
type ArtifactPath struct {
	Title string
	Path  string
}

// ExecutionPaths tells the UI where work actually happened: the workspace root
// (the project), the execution copy for this Ticket, and the deliverable paths.
// The copy semantics are unchanged; the projection just stops hiding them.
type ExecutionPaths struct {
	WorkspaceRoot string
	ExecutionDir  string
	Artifacts     []ArtifactPath
}

// ExecutionPaths returns the truthful paths for one Ticket.
func (r *Runtime) ExecutionPaths(ticketID string) (ExecutionPaths, error) {
	tk, ok := r.ticketSnapshot(domain.ID(strings.TrimSpace(ticketID)))
	if !ok {
		return ExecutionPaths{}, fmt.Errorf("工单 %q 不存在", ticketID)
	}
	cfg := r.workerConfig()
	if cfg == nil {
		return ExecutionPaths{}, fmt.Errorf("执行器未配置")
	}
	out := ExecutionPaths{ExecutionDir: ticketWorkdir(cfg, tk)}
	if ws, ok := r.eng.Workspace(tk.Workspace); ok {
		out.WorkspaceRoot = ws.Root
	}
	for _, a := range r.eng.Artifacts(tk.ID) {
		out.Artifacts = append(out.Artifacts, ArtifactPath{Title: a.Title, Path: a.Path})
	}
	return out, nil
}

// --- P0-2: unified attention inbox ---

// AttentionItem is one thing waiting for the operator: an approval proposal or a
// Ticket that needs a person (answer / blocked / failed review).
type AttentionItem struct {
	Kind       string // "proposal" | "ticket"
	DecisionID string
	TicketID   string
	Number     int
	Source     string
	Sender     string
	Text       string
	Workspace  string
	At         time.Time
	Actions    []string
}

// AttentionCount is the canonical "待你决定 N" number.
func (r *Runtime) AttentionCount() int { return len(r.attentionItems(nil)) }

// SetAttentionNotify installs a callback for newly appeared attention items. The
// UI can bridge it to an OS notification; the runtime never notifies twice for
// the same item.
func (r *Runtime) SetAttentionNotify(fn func(AttentionItem)) {
	r.mu.Lock()
	r.attentionNotify = fn
	r.mu.Unlock()
}

// attentionItems builds the unified list. seen (optional) collects item ids so the
// caller can decide what is new.
func (r *Runtime) attentionItems(seen map[string]bool) []AttentionItem {
	now := r.clock()
	var out []AttentionItem
	for _, t := range r.ticketSnapshots() {
		if t.Ended != nil {
			continue
		}
		st := domain.StageOf(&t, now)
		if !isPersonStage(st) {
			continue
		}
		text := waitingFor(st, &t)
		if t.Attention != nil && t.Attention.Question != "" {
			text = t.Attention.Question
		}
		item := AttentionItem{
			Kind: "ticket", TicketID: string(t.ID), Number: t.Number,
			Source: ticketSource(r.eng, t), Sender: t.Mandate.Actor.ID,
			Text: text, Workspace: string(t.Workspace), At: t.CreatedAt,
			Actions: []string{"open"},
		}
		if t.CanRetry() {
			item.Actions = append(item.Actions, "retry")
		}
		if t.Attention != nil {
			item.Actions = append(item.Actions, "answer")
		}
		if st == domain.StageWorking || st == domain.StageWorkingQueued {
			item.Actions = append(item.Actions, "stop")
		}
		out = append(out, item)
		if seen != nil {
			seen["ticket:"+item.TicketID] = true
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.After(out[j].At) })
	return out
}

func ticketSource(eng *engine.Engine, t domain.Ticket) string {
	if tr, ok := eng.Trigger(t.Origin); ok && tr.Source != "" {
		return tr.Source
	}
	return "local"
}

// attentionVM is the view projection of the unified inbox.
func (r *Runtime) attentionVM() *view.AttentionVM {
	items := r.attentionItems(nil)
	vm := &view.AttentionVM{UpdatedAt: r.clock(), Items: make([]view.AttentionItemVM, 0, len(items))}
	for _, item := range items {
		vm.Items = append(vm.Items, view.AttentionItemVM{
			Kind: item.Kind, DecisionID: item.DecisionID, TicketID: item.TicketID,
			Number: item.Number, Source: item.Source, Sender: item.Sender,
			Text: item.Text, Workspace: item.Workspace, At: item.At, Actions: item.Actions,
		})
	}
	return vm
}

// notifyAttention publishes the callback for items the UI has not seen. It is
// called from Publish, so any state change reaches the notification hook.
func (r *Runtime) notifyAttention() {
	r.mu.Lock()
	fn := r.attentionNotify
	r.mu.Unlock()
	if fn == nil {
		return
	}
	seen := map[string]bool{}
	items := r.attentionItems(seen)
	r.mu.Lock()
	if r.attentionSeen == nil {
		r.attentionSeen = map[string]bool{}
	}
	fresh := make([]AttentionItem, 0)
	for _, item := range items {
		key := "proposal:" + item.DecisionID
		if item.Kind == "ticket" {
			key = "ticket:" + item.TicketID
		}
		if !r.attentionSeen[key] {
			r.attentionSeen[key] = true
			fresh = append(fresh, item)
		}
	}
	r.mu.Unlock()
	for _, item := range fresh {
		fn(item)
	}
}

// --- Onboarding skip/demo ---

// SkipOnboarding finishes setup without model/workspace (the wizard's 演示/稍后
// path). The app opens but Tickets stay parked until a model is configured.
func (r *Runtime) SkipOnboarding() (uint64, error) {
	values := map[string]string{"skip": "1", "done": "1"}
	rev, err := r.SaveOnboarding(values)
	if err != nil {
		return rev, err
	}
	return rev, r.MarkSetupComplete()
}

// DemoMode reports whether setup was skipped into demo mode.
func (r *Runtime) DemoMode() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	v, _ := ParseSettingBool(r.settings["onboard.demo"])
	return v
}
