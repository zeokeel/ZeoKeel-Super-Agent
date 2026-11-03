package runtime

// Workspace and conversation commands for the panel (ui2-spec §2/§4): a person
// can register a workspace and start a conversation in it. Both go through the
// durable layer when a store is attached, so the row survives a restart.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/zeokeel/super-agent/internal/domain"
	"github.com/zeokeel/super-agent/internal/engine"
	"github.com/zeokeel/super-agent/internal/scan"
)

// CreateWorkspace registers a new workspace from the panel: validated name/root,
// scanned facts, persisted row + default policy, then published.
func (r *Runtime) CreateWorkspace(name, root, purpose, kind string) error {
	name = strings.TrimSpace(name)
	root = expandHome(strings.TrimSpace(root))
	if name == "" {
		return fmt.Errorf("工作区名称不能为空")
	}
	if root == "" {
		return fmt.Errorf("工作区根目录不能为空")
	}
	if fi, err := os.Stat(root); err != nil || !fi.IsDir() {
		return fmt.Errorf("工作区根目录不可用：%s（%v）", root, err)
	}
	k, err := parseWorkspaceKind(kind)
	if err != nil {
		return err
	}
	id := domain.WorkspaceID("ws_" + slug(name))
	for _, existing := range r.eng.Workspaces() {
		if existing.ID == id || strings.EqualFold(existing.Name, name) {
			return fmt.Errorf("工作区 %q 已经存在", name)
		}
	}
	facts := scan.Scan(root)
	keywords := []string{name}
	if filepath.Base(root) != name {
		keywords = append(keywords, filepath.Base(root))
	}
	w := domain.Workspace{
		ID: id, Name: name, Root: root, Kind: k,
		Brief: domain.Brief{Purpose: purpose, Keywords: keywords, Facts: facts},
	}
	policy := engine.Policy{Version: "v1",
		Allowed: map[string]map[string]bool{"local": {"operator": true}}}

	r.mu.Lock()
	p := r.persist
	r.mu.Unlock()
	if p != nil {
		if err := p.Register(w, policy); err != nil {
			return fmt.Errorf("工作区保存失败：%w", err)
		}
		// Persistence.Register also updates the engine view; fall back to the
		// engine when an older build only persisted the row.
	} else {
		r.eng.Register(w, policy)
	}
	// Register is idempotent in the engine; make sure the in-memory routing view
	// has it even when the store wrote it.
	if !r.hasWorkspace(id) {
		r.eng.Register(w, policy)
	}
	r.Publish()
	return nil
}

func (r *Runtime) hasWorkspace(id domain.WorkspaceID) bool {
	for _, w := range r.eng.Workspaces() {
		if w.ID == id {
			return true
		}
	}
	return false
}

// parseWorkspaceKind accepts the panel's four kinds, with repo as the default.
func parseWorkspaceKind(raw string) (domain.WorkspaceKind, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "repo":
		return domain.KindRepo, nil
	case "data":
		return domain.KindData, nil
	case "docs":
		return domain.KindDocs, nil
	case "mixed":
		return domain.KindMixed, nil
	}
	return "", fmt.Errorf("工作区类型 %q 不支持（repo / data / docs / mixed）", raw)
}

// NewConversation starts a conversation in a named workspace from the panel. The
// workspace is explicit (the person picked it), so the Router is not asked again.
func (r *Runtime) NewConversation(workspaceID, text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return fmt.Errorf("会话内容不能为空")
	}
	ws := domain.WorkspaceID(strings.TrimSpace(workspaceID))
	if ws == "" {
		return fmt.Errorf("请选择工作区")
	}
	if !r.hasWorkspace(ws) {
		return fmt.Errorf("工作区 %q 不存在：先在左侧新建工作区", workspaceID)
	}
	r.mu.Lock()
	p := r.persist
	r.mu.Unlock()
	in := engine.DispatchInput{Text: text, Workspace: ws, At: r.clock()}
	var out engine.Outcome
	var err error
	if p != nil {
		out, err = p.Dispatch(in)
	} else {
		out, err = r.eng.Dispatch(in)
	}
	if err != nil {
		r.setErr(err.Error())
		return err
	}
	outs, evalErr := r.EvaluateDue(r.clock())
	if evalErr != nil {
		return evalErr
	}
	out = mergeEvalOutcome(out, outs)
	if q := questionFrom(outs); q != "" {
		return fmt.Errorf("%s", q)
	}
	if out.Ticket == "" {
		// A proposal or a question is a real outcome; a silent ignore is not.
		if out.Proposal || out.NeedsPerson {
			r.Publish()
			return nil
		}
		msg := r.noDispatchMessage(text)
		r.setErr(msg)
		r.Publish()
		return fmt.Errorf("%s", msg)
	}
	if t, ok := r.ticketSnapshot(out.Ticket); ok {
		// A person typed this: it is human-priority work.
		if err := r.wakeTicket(t); err != nil {
			r.slogger().Warn("new conversation ticket could not launch",
				"ticket", t.Number, "error", err)
		}
	}
	r.setErr("")
	r.Publish()
	return nil
}

// noDispatchMessage is the actionable text when a local message produced no work:
// the person must always know what happened and what to do next.
func (r *Runtime) noDispatchMessage(text string) string {
	if list, ok := r.workspaceAnswer(text); ok {
		return list
	}
	return "这次没有派发工单（超级Agent 没有匹配到工作区或工作请求）。" +
		"你可以：① 在左侧选择一个工作区并「新建会话」，把任务交给它；" +
		"② 或直接说明要做什么（例如「在 webapp 里改登录超时」）。"
}

// workspaceAnswer lists the workspaces when the message asks about them.
func (r *Runtime) workspaceAnswer(text string) (string, bool) {
	lower := strings.ToLower(text)
	asking := false
	for _, kw := range []string{"工作区", "workspace", "哪些", "有什么", "几个"} {
		if strings.Contains(lower, kw) {
			asking = true
			break
		}
	}
	if !asking {
		return "", false
	}
	ws := r.eng.Workspaces()
	if len(ws) == 0 {
		return "当前还没有工作区。点左侧「＋ 新建工作区」添加一个（名称 + 根目录即可）。", true
	}
	var b strings.Builder
	b.WriteString("当前有 ")
	b.WriteString(fmt.Sprint(len(ws)))
	b.WriteString(" 个工作区：")
	for _, w := range ws {
		b.WriteString("\n- ")
		b.WriteString(w.Name)
		if w.Brief.Purpose != "" {
			b.WriteString("：")
			b.WriteString(w.Brief.Purpose)
		}
		b.WriteString("（")
		b.WriteString(string(w.Kind))
		b.WriteString("）")
	}
	b.WriteString("\n要派活：选一个工作区「新建会话」，或告诉我任务属于哪个工作区。")
	return b.String(), true
}

// localAnswer is what the stream shows for a local message that produced no work:
// the question was answered, not ignored.
func (r *Runtime) localAnswer(text string) string {
	if list, ok := r.workspaceAnswer(text); ok {
		return list
	}
	return "这次没有派发工单：没有匹配到工作区或明确的工作请求。" +
		"你可以选一个工作区新建会话，或直接说明要做什么。"
}
