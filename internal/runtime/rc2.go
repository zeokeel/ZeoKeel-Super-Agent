package runtime

// rc2 UX backend: streaming Super-Agent chat, apply-back of deliverables,
// plantask visibility, and the minimal update check.

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/zeokeel/super-agent/internal/domain"
	"github.com/zeokeel/super-agent/internal/view"
)

// --- Streaming chat ---

// chatPartial is the in-progress assistant text published with ChatVM while the
// model streams. It is never persisted; the final text is appended once.
func (r *Runtime) setChatPartial(text string) {
	r.mu.Lock()
	r.chatPartial = text
	r.mu.Unlock()
}

// chatStreamState cancels an in-flight answer when a new send or stop arrives.
type chatStreamState struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// StopChat cancels the in-flight streaming answer, if any, and publishes the stop.
// The UI's stop button (and a new send) calls this.
func (r *Runtime) StopChat() {
	r.mu.Lock()
	st := r.chatStream
	r.chatStream = nil
	r.mu.Unlock()
	if st == nil {
		return
	}
	st.cancel()
	select {
	case <-st.done:
	case <-time.After(2 * time.Second):
	}
	r.mu.Lock()
	partial := r.chatPartial
	r.chatPartial = ""
	r.mu.Unlock()
	if strings.TrimSpace(partial) != "" {
		r.appendChat(view.ChatAssistant, view.ChatNote, partial+"\n（已停止生成）")
	} else {
		r.appendChat(view.ChatAssistant, view.ChatNote, "（已停止生成）")
	}
	r.Publish()
}

// ChatStreaming reports whether a streamed answer is in flight (UI hint).
func (r *Runtime) ChatStreaming() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.chatStream != nil
}

// chatAnswerStreaming asks the model for an answer with streaming. It returns the
// full text; partial text is published through ChatVM while it arrives. The
// deterministic fallback is used when no model is configured or the stream fails
// before producing anything.
func (r *Runtime) chatAnswerStreaming(text string) string {
	cfg := r.workerConfig()
	var base, key, model string
	if cfg != nil {
		base, key, model = cfg.BaseURL, cfg.APIKey, cfg.Model
	}
	if base == "" || model == "" {
		return r.answerChat(text)
	}
	ctx, cancel := context.WithCancel(context.Background())
	st := &chatStreamState{cancel: cancel, done: make(chan struct{})}
	r.mu.Lock()
	r.chatStream = st
	r.mu.Unlock()
	defer func() {
		cancel()
		close(st.done)
		r.mu.Lock()
		if r.chatStream == st {
			r.chatStream = nil
		}
		r.chatPartial = ""
		r.mu.Unlock()
	}()

	answer, err := r.streamChat(ctx, base, key, model, text)
	if ctx.Err() != nil {
		// StopChat already published the partial text and the stop note; the caller
		// must not append a second, competing message.
		return ""
	}
	if err != nil {
		if strings.TrimSpace(answer) == "" {
			return r.answerChat(text) // stream failed before any text: deterministic
		}
		return strings.TrimSpace(answer) + "\n（生成中断：" + err.Error() + "）"
	}
	if strings.TrimSpace(answer) == "" {
		return r.answerChat(text)
	}
	return strings.TrimSpace(answer)
}

// streamChat performs one OpenAI-compatible SSE request and returns the full text.
func (r *Runtime) streamChat(ctx context.Context, base, key, model, text string) (string, error) {
	payload := map[string]any{
		"model":    model,
		"stream":   true,
		"messages": []map[string]string{{"role": "system", "content": chatSystemBase}, {"role": "user", "content": r.chatSystemPrompt(text)}},
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(base, "/")+"/chat/completions", strings.NewReader(string(body)))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := httpDoStream(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return "", fmt.Errorf("模型返回 HTTP %d：%s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var b strings.Builder
	lastPublish := time.Time{}
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if json.Unmarshal([]byte(data), &chunk) != nil {
			continue
		}
		for _, c := range chunk.Choices {
			b.WriteString(c.Delta.Content)
		}
		if time.Since(lastPublish) >= 60*time.Millisecond {
			r.setChatPartial(b.String())
			r.Publish()
			lastPublish = time.Now()
		}
	}
	r.setChatPartial(b.String())
	r.Publish()
	if err := scanner.Err(); err != nil {
		return b.String(), err
	}
	return b.String(), nil
}

// httpDoStream is injectable for tests.
var httpDoStream = func(req *http.Request) (*http.Response, error) {
	return (&http.Client{Timeout: 120 * time.Second}).Do(req)
}

// --- Apply back to project ---

// ApplyArtifacts copies a Ticket's recorded deliverables from the execution copy
// into the project (workspace) root. It never silently overwrites: an existing
// destination is renamed to "<name>.bak-<timestamp>" first. Paths outside the
// execution copy are skipped as unsafe. The returned paths are relative to the
// project root. The caller must have asked the person to confirm.
func (r *Runtime) ApplyArtifacts(ticketID string) (copied []string, err error) {
	tk, ok := r.ticketSnapshot(domain.ID(strings.TrimSpace(ticketID)))
	if !ok {
		return nil, fmt.Errorf("工单 %q 不存在", ticketID)
	}
	cfg := r.workerConfig()
	if cfg == nil {
		return nil, fmt.Errorf("执行器未配置")
	}
	ws, ok := r.eng.Workspace(tk.Workspace)
	if !ok || ws.Root == "" {
		return nil, fmt.Errorf("工作区目录未知")
	}
	execRoot := ticketWorkdir(cfg, tk)
	for _, a := range r.eng.Artifacts(tk.ID) {
		src, rel, ok := safeExecPath(execRoot, a.Path)
		if !ok {
			continue // absolute/escaping path: not a deliverable we may copy
		}
		info, statErr := os.Stat(src)
		if statErr != nil || info.IsDir() {
			continue // recorded but gone; not an error for the rest of the batch
		}
		dst := filepath.Join(ws.Root, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return copied, err
		}
		if _, exists := os.Stat(dst); exists == nil {
			backup := dst + ".bak-" + time.Now().Format("20060102-150405")
			if err := os.Rename(dst, backup); err != nil {
				return copied, fmt.Errorf("备份已存在文件失败：%w", err)
			}
		}
		if err := copyFile(src, dst); err != nil {
			return copied, err
		}
		copied = append(copied, filepath.ToSlash(rel))
	}
	sort.Strings(copied)
	return copied, nil
}

// safeExecPath resolves an artifact path and rejects anything outside execRoot.
func safeExecPath(execRoot, rel string) (string, string, bool) {
	rel = strings.TrimSpace(rel)
	if rel == "" || filepath.IsAbs(rel) {
		return "", "", false
	}
	clean := filepath.Clean(rel)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", "", false
	}
	full := filepath.Join(execRoot, clean)
	resolved, err := filepath.Rel(execRoot, full)
	if err != nil || strings.HasPrefix(resolved, "..") {
		return "", "", false
	}
	return full, resolved, true
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// ArtifactRelPaths lists the deliverables as project-relative paths (for the
// confirmation dialog: "这些文件会写回项目").
func (r *Runtime) ArtifactRelPaths(ticketID string) []string {
	tk, ok := r.ticketSnapshot(domain.ID(strings.TrimSpace(ticketID)))
	if !ok {
		return nil
	}
	cfg := r.workerConfig()
	if cfg == nil {
		return nil
	}
	execRoot := ticketWorkdir(cfg, tk)
	var out []string
	for _, a := range r.eng.Artifacts(tk.ID) {
		if _, rel, ok := safeExecPath(execRoot, a.Path); ok {
			out = append(out, filepath.ToSlash(rel))
		}
	}
	sort.Strings(out)
	return out
}

// --- Plan/task visibility ---

// PlanTask is one row of the Worker's plantask list, read from the JSON files the
// tools persist under <ticketWorkdir>/.zeokeel/plan.
type PlanTask struct {
	ID          string
	Title       string
	Description string
	Status      string
	Order       int
}

// PlanTasks reads a Ticket's plan. It is empty when no plan was written.
func (r *Runtime) PlanTasks(ticketID string) []PlanTask {
	tk, ok := r.ticketSnapshot(domain.ID(strings.TrimSpace(ticketID)))
	if !ok {
		return nil
	}
	cfg := r.workerConfig()
	if cfg == nil {
		return nil
	}
	dir := filepath.Join(ticketWorkdir(cfg, tk), ".zeokeel", "plan")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []PlanTask
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var t struct {
			ID          string `json:"id"`
			Subject     string `json:"subject"`
			Description string `json:"description"`
			Status      string `json:"status"`
		}
		if json.Unmarshal(raw, &t) != nil {
			continue
		}
		order := 0
		if n, err := strconv.Atoi(strings.TrimSuffix(e.Name(), ".json")); err == nil {
			order = n
		}
		out = append(out, PlanTask{ID: t.ID, Title: t.Subject, Description: t.Description,
			Status: t.Status, Order: order})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Order < out[j].Order })
	return out
}

// --- Update check ---

// SetVersion records the running build's version (cmd passes the ldflags value).
func (r *Runtime) SetVersion(v string) {
	r.mu.Lock()
	r.version = strings.TrimSpace(v)
	r.mu.Unlock()
}

// Version is the running build's version ("" when unknown).
func (r *Runtime) Version() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.version
}

// CheckForUpdate asks the public GitHub releases API for the latest release. It
// does not download anything; the UI offers the URL and decides whether to show
// "有更新" using upToDate.
func (r *Runtime) CheckForUpdate(ctx context.Context) (latest string, upToDate bool, url string, err error) {
	if ctx == nil {
		ctx = context.Background()
	}
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet,
		"https://api.github.com/repos/zeokeel/ZeoKeel-Super-Agent/releases/latest", nil)
	if err != nil {
		return "", false, "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "zeokeel-update-check")
	resp, err := httpDoUpdate(req)
	if err != nil {
		return "", false, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", false, "", fmt.Errorf("更新检查失败（HTTP %d）", resp.StatusCode)
	}
	var rel struct {
		Tag  string `json:"tag_name"`
		HTML string `json:"html_url"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&rel); err != nil {
		return "", false, "", err
	}
	upToDate = r.Version() != "" && normalizeVersion(rel.Tag) == normalizeVersion(r.Version())
	return rel.Tag, upToDate, rel.HTML, nil
}

// normalizeVersion strips a leading "v" so tags and ldflags values compare.
func normalizeVersion(v string) string {
	v = strings.TrimSpace(v)
	return strings.TrimPrefix(strings.TrimPrefix(v, "v"), "V")
}

// httpDoUpdate is injectable for tests.
var httpDoUpdate = func(req *http.Request) (*http.Response, error) {
	return (&http.Client{Timeout: 15 * time.Second}).Do(req)
}

// currentPlan publishes the plan of the newest Ticket that has one (the panel's
// "current work"). It is nil when nothing was planned.
func (r *Runtime) currentPlan() *view.PlanVM {
	tickets := r.ticketSnapshots()
	sort.SliceStable(tickets, func(i, j int) bool { return tickets[i].CreatedAt.After(tickets[j].CreatedAt) })
	for _, t := range tickets {
		tasks := r.PlanTasks(string(t.ID))
		if len(tasks) == 0 {
			continue
		}
		vm := &view.PlanVM{TicketID: string(t.ID), UpdatedAt: r.clock(), Items: make([]view.PlanItem, 0, len(tasks))}
		for _, task := range tasks {
			vm.Items = append(vm.Items, view.PlanItem{ID: task.ID, Title: task.Title, Status: task.Status, Detail: task.Description})
		}
		return vm
	}
	return nil
}

// --- Undo reject ---

// UndoReject puts a rejected proposal back on the approval list within the
// engine's contract (only rejections, not accepted work; a newer decision on the
// same thread supersedes it). Nothing is launched and no reply is sent.
func (r *Runtime) UndoReject(decisionID string) error {
	r.commandMu.Lock()
	defer r.commandMu.Unlock()
	id := domain.ID(strings.TrimSpace(decisionID))
	if id == "" {
		return fmt.Errorf("缺少 decision id")
	}
	r.mu.Lock()
	p := r.persist
	r.mu.Unlock()
	var err error
	if p != nil {
		_, err = p.UndoReject(id)
	} else {
		_, err = r.eng.UndoReject(id)
	}
	if err != nil {
		return err
	}
	r.Publish()
	return nil
}
