package runtime

// Ticket preparation (003:49-63): a deployment can declare a per-workspace
// preparation command that runs in the execution root (the parent of the per-
// ticket directories) before the first Assignment
// when the Ticket directory does not exist yet. Its output and environment are
// the operator's, so secrets are scrubbed from the inherited environment.
//
// The same facts are shown to the Worker in its prompt. The Worker's bash
// environment itself is owned by internal/agent (out of this scope); the prompt
// names the variables so the model can use the values explicitly.

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/zeokeel/super-agent/internal/domain"
)

// prepareTimeout bounds one prepare command. Ten minutes covers a clone or a
// dependency bootstrap; longer work belongs in the Ticket itself.
const prepareTimeout = 10 * time.Minute

// executionVars are the facts a prepare script and the Worker prompt share.
func (r *Runtime) executionVars(t domain.Ticket, workdir string) map[string]string {
	wsRoot, repo := "", ""
	if ws, ok := r.eng.Workspace(t.Workspace); ok {
		wsRoot = ws.Root
		repo = ws.Remote
		if repo == "" {
			repo = ws.Name
		}
	}
	if repo == "" {
		if tr, ok := r.eng.Trigger(t.Origin); ok {
			repo = tr.Channel.Address["repository"]
			if repo == "" {
				repo = tr.Conversation.Name
			}
		}
	}
	return map[string]string{
		"ZEOKEEL_WORKDIR":     wsRoot,
		"ZEOKEEL_TICKET_DIR":  workdir,
		"ZEOKEEL_TICKET":      fmt.Sprint(t.Number),
		"ZEOKEEL_TICKET_NAME": fmt.Sprintf("ticket-%d", t.Number),
		"ZEOKEEL_REPO":        repo,
	}
}

// prepareString is the execution.prepare setting (empty means "no prepare").
func (r *Runtime) prepareString() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return strings.TrimSpace(r.settings["execution.prepare"])
}

// runPrepare runs the preparation command before the first Assignment when the
// Ticket directory is missing. A failure is a failed Ticket with the command's
// output: no directory is created, and there is no automatic retry (a person's
// retry or a new message may try again).
func (r *Runtime) runPrepare(ctx context.Context, t domain.Ticket, cfg *WorkerConfig, workdir string) error {
	if _, err := os.Stat(workdir); err == nil {
		return nil // already prepared (or the directory already existed)
	}
	script := r.prepareString()
	if script == "" {
		return nil // agent.New creates the directory
	}
	cctx, cancel := context.WithTimeout(ctx, prepareTimeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, "sh", "-c", script)
	cmd.Dir = cfg.Root
	env := scrubEnv(os.Environ())
	vars := r.executionVars(t, workdir)
	keys := make([]string, 0, len(vars))
	for k := range vars {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		env = append(env, k+"="+vars[k])
	}
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("prepare 命令失败（%v）：\n%s", err, clipRunes(string(out), 2000))
	}
	if _, statErr := os.Stat(workdir); statErr != nil {
		return fmt.Errorf("prepare 命令成功但没有创建工单目录 %s", workdir)
	}
	return nil
}

// scrubEnv removes credential-looking variables from an inherited environment.
// The prepare script is operator-configured but must not receive model API keys,
// GitHub tokens or IM secrets by default.
func scrubEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		name, _, ok := strings.Cut(kv, "=")
		if !ok {
			out = append(out, kv)
			continue
		}
		upper := strings.ToUpper(name)
		if strings.Contains(upper, "KEY") || strings.Contains(upper, "TOKEN") ||
			strings.Contains(upper, "SECRET") || strings.Contains(upper, "PASSWORD") {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// promptVars is the stable, sorted "facts" block the Worker prompt shows.
func (r *Runtime) promptVars(t domain.Ticket, workdir string) string {
	vars := r.executionVars(t, workdir)
	keys := make([]string, 0, len(vars))
	for k := range vars {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString("工单事实：")
	for _, k := range keys {
		b.WriteString("\n- ")
		b.WriteString(k)
		b.WriteString("=")
		b.WriteString(vars[k])
	}
	return b.String()
}

// ticketWorkdir is the one place the per-Ticket directory name is derived.
func ticketWorkdir(cfg *WorkerConfig, t domain.Ticket) string {
	return filepath.Join(cfg.Root, fmt.Sprintf("ticket-%d", t.Number))
}
