package runtime

// DSH round 5: read_resource worker tool and browser prerequisites.

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	goruntime "runtime"
	"strings"

	"github.com/zeokeel/super-agent/internal/domain"
	"github.com/zeokeel/super-agent/internal/view"
)

// --- read_resource (DSH orchestrator_read equivalent) ---

// ResourcePage is one bounded page of a read_resource answer.
type ResourcePage struct {
	Resource string `json:"resource"`
	Total    int    `json:"total"`
	Offset   int    `json:"offset"`
	Limit    int    `json:"limit"`
	Items    []any  `json:"items"`
}

// resourceText redacts credential-looking fragments from resource text.
func resourceText(s string) string {
	fields := strings.Fields(s)
	for i, f := range fields {
		lower := strings.ToLower(f)
		if strings.Contains(lower, "sk-") || strings.Contains(lower, "bearer") ||
			strings.Contains(lower, "token=") || strings.Contains(lower, "secret=") {
			fields[i] = "[redacted]"
		}
	}
	return strings.Join(fields, " ")
}

// ReadResourceInput is the read_resource request.
type ReadResourceInput struct {
	Resource string `json:"resource"`
	Offset   int    `json:"offset,omitempty"`
	Limit    int    `json:"limit,omitempty"`
}

// readResource implements the bound/ownership rules: a Worker sees only its own
// Ticket/workspace, never another Ticket's trigger, and never secrets.
func (r *Runtime) readResource(t domain.Ticket, in ReadResourceInput) (string, error) {
	offset, limit := in.Offset, in.Limit
	if offset < 0 {
		offset = 0
	}
	if limit <= 0 {
		limit = 20
	}
	if limit > 200 {
		limit = 200
	}
	resource := strings.TrimSpace(in.Resource)
	if resource == "" {
		resource = "ticket"
	}
	var items []any
	switch {
	case resource == "ticket":
		snap, ok := r.ticketSnapshot(t.ID)
		if !ok {
			return "", fmt.Errorf("ticket not found")
		}
		report := ""
		if rep := snap.LastReport(); rep != nil {
			report = string(rep.State) + "：" + resourceText(rep.Summary)
		}
		items = append(items, map[string]any{
			"id": string(snap.ID), "number": snap.Number, "title": resourceText(snap.Title),
			"workspace": string(snap.Workspace), "stage": string(domain.StageOf(&snap, r.clock())),
			"report": report, "inputs": len(snap.Inputs), "assignments": len(snap.Assignments),
			"follows": len(snap.Follows),
		})
	case resource == "inputs":
		snap, ok := r.ticketSnapshot(t.ID)
		if !ok {
			return "", fmt.Errorf("ticket not found")
		}
		for _, in := range snap.Inputs {
			items = append(items, map[string]any{
				"id": string(in.ID), "kind": string(in.Kind), "thread": in.Thread,
				"text": resourceText(in.Text), "at": in.At, "read": in.Read,
			})
		}
	case strings.HasPrefix(resource, "trigger/"):
		id := strings.TrimSpace(strings.TrimPrefix(resource, "trigger/"))
		if id != string(t.Origin) {
			return "", fmt.Errorf("trigger %q 不属于本工单（只能读自己的触发）", id)
		}
		tr, ok := r.eng.Trigger(t.Origin)
		if !ok {
			return "", fmt.Errorf("trigger not found")
		}
		items = append(items, map[string]any{
			"id": string(tr.ID), "source": tr.Source, "actor": tr.Actor.ID,
			"text": resourceText(tr.Text), "at": tr.At, "threads": len(tr.Threads),
		})
	case strings.HasPrefix(resource, "thread/"):
		key := strings.TrimSpace(strings.TrimPrefix(resource, "thread/"))
		if !r.ownsThread(t, key) {
			return "", fmt.Errorf("thread %q 不属于本工单", key)
		}
		for _, item := range r.eng.Feed() {
			for _, ref := range item.Trigger.Threads {
				if domain.ThreadKey(item.Trigger.Scope, ref.Source, ref.ID) != key {
					continue
				}
				items = append(items, map[string]any{
					"trigger": string(item.Trigger.ID), "text": resourceText(item.Trigger.Text),
					"at": item.Trigger.At, "kind": string(item.Disp.Kind),
				})
			}
		}
	default:
		return "", fmt.Errorf("unknown resource %q（ticket / trigger/<id> / thread/<key> / inputs）", resource)
	}
	total := len(items)
	start := offset
	if start > total {
		start = total
	}
	end := start + limit
	if end > total {
		end = total
	}
	page := ResourcePage{Resource: resource, Total: total, Offset: start, Limit: limit, Items: items[start:end]}
	raw, err := json.Marshal(page)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// ownsThread is true for the Ticket's origin conversation/subscribed threads.
func (r *Runtime) ownsThread(t domain.Ticket, key string) bool {
	for _, sub := range t.SubscribedThreads {
		if sub == key {
			return true
		}
	}
	if tr, ok := r.eng.Trigger(t.Origin); ok {
		for _, ref := range tr.Threads {
			if domain.ThreadKey(tr.Scope, ref.Source, ref.ID) == key {
				return true
			}
		}
	}
	return false
}

// --- Browser prerequisites ---

// Hooks for tests: real LookPath/command by default.
var (
	prereqLookPath = exec.LookPath
	prereqCommand  = func(name string, args ...string) (string, error) {
		out, err := exec.Command(name, args...).CombinedOutput()
		return string(out), err
	}
)

// BrowserPrerequisites checks node/npx (the browser MCP prerequisites) and
// returns the panel's view type. A missing dependency is reported with an
// actionable hint, never faked.
func (r *Runtime) BrowserPrerequisites() view.BrowserPrereq {
	var out view.BrowserPrereq
	if path, err := prereqLookPath("node"); err == nil && path != "" {
		out.NodeInstalled = true
		if raw, err := prereqCommand("node", "--version"); err == nil {
			out.NodeVersion = prereqFirstLine(raw)
		}
	}
	if path, err := prereqLookPath("npx"); err == nil && path != "" {
		out.NpxInstalled = true
		if raw, err := prereqCommand("npx", "--version"); err == nil {
			out.NpxVersion = prereqFirstLine(raw)
		}
	}
	hint := "安装 Node.js 18+（含 npx）"
	switch goruntime.GOOS {
	case "darwin":
		hint = "brew install node（或从 https://nodejs.org 安装）"
	case "windows":
		hint = "winget install OpenJS.NodeJS（或从 https://nodejs.org 安装）"
	default:
		hint = "用发行版包管理器安装 nodejs/npm（如 apt install nodejs npm），或使用 nvm"
	}
	switch {
	case !out.NodeInstalled || !out.NpxInstalled:
		out.Detail = "缺少 node/npx：" + hint
	case out.NodeVersion == "" || out.NpxVersion == "":
		out.Detail = "node/npx 已安装但版本读取失败；浏览器 MCP 需要 Node 18+"
	default:
		out.Detail = "Node " + out.NodeVersion + " / npx " + out.NpxVersion + "，可运行浏览器 MCP"
	}
	out.CheckedAt = r.clock()
	return out
}

func prereqFirstLine(s string) string {
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

var _ = context.Background
