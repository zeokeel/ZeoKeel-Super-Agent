package runtime

// Round-3 panel projections and commands (ui2-spec §2/§4): workspace creation,
// conversations, the workspace panel, system facts, and never leaving a local
// message at "未受理".

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zeokeel/super-agent/internal/domain"
	"github.com/zeokeel/super-agent/internal/engine"
	"github.com/zeokeel/super-agent/internal/view"
)

func newRound3(t *testing.T) (*Runtime, *engine.Engine, *view.Snapshot) {
	t.Helper()
	eng := engine.New(engine.RuleAssessor{})
	eng.Register(domain.Workspace{
		ID: "ws_web", Name: "web", Root: t.TempDir(), Kind: domain.KindRepo,
		Brief: domain.Brief{Purpose: "webapp", Keywords: []string{"webapp"}},
	}, engine.Policy{Version: "v1", Allowed: map[string]map[string]bool{
		"dingtalk": {"colleague": true},
	}})
	snap := view.NewSnapshot()
	return New(eng, snap), eng, snap
}

func TestCreateWorkspaceRegistersAndPublishes(t *testing.T) {
	rt, eng, snap := newRound3(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module x\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := rt.CreateWorkspace("数据仓库", root, "每天早上出用量报表", "data"); err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	found := false
	for _, w := range eng.Workspaces() {
		if w.Name == "数据仓库" {
			found = true
			if w.Brief.Purpose != "每天早上出用量报表" || w.Kind != domain.KindData || w.Root != root {
				t.Fatalf("workspace brief = %+v", w)
			}
		}
	}
	if !found {
		t.Fatal("the workspace was not registered in the engine")
	}
	// Published: the panel sees it without another call.
	found = false
	for _, w := range snap.Workspaces().Workspaces {
		if w.Name == "数据仓库" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the new workspace was not published: %+v", snap.Workspaces())
	}

	for _, c := range []struct{ name, root, purpose, kind, want string }{
		{"", root, "", "data", "名称"},
		{"x", "", "", "data", "根目录"},
		{"x", filepath.Join(root, "missing"), "", "data", "不可用"},
		{"x", root, "", "weird", "类型"},
	} {
		if err := rt.CreateWorkspace(c.name, c.root, c.purpose, c.kind); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("CreateWorkspace(%+v) err = %v, want %q", c, err, c.want)
		}
	}
	if err := rt.CreateWorkspace("数据仓库", root, "", "data"); err == nil {
		t.Fatal("a duplicate workspace name was accepted")
	}
}

func TestNewConversationDispatchesToTheNamedWorkspace(t *testing.T) {
	rt, eng, _ := newRound3(t)
	if err := rt.NewConversation("ws_web", "在 webapp 里跑个命令"); err != nil {
		t.Fatalf("NewConversation: %v", err)
	}
	all := eng.AllTickets()
	if len(all) != 1 {
		t.Fatalf("tickets = %d after the conversation", len(all))
	}
	snap, _ := eng.TicketSnapshot(all[0].ID)
	if snap.Workspace != "ws_web" {
		t.Fatalf("ticket workspace = %q, want ws_web", snap.Workspace)
	}

	if err := rt.NewConversation("", "x"); err == nil {
		t.Fatal("empty workspace accepted")
	}
	if err := rt.NewConversation("ws_web", "  "); err == nil {
		t.Fatal("empty text accepted")
	}
	if err := rt.NewConversation("ws_missing", "x"); err == nil || !strings.Contains(err.Error(), "不存在") {
		t.Fatalf("unknown workspace err = %v", err)
	}
}

func TestWorkspacePanelListsConversations(t *testing.T) {
	rt, eng, snap := newRound3(t)
	// A local ticket says "me"; a sourced one says "agent".
	if err := rt.Dispatch("在 webapp 里跑个命令"); err != nil {
		t.Fatal(err)
	}
	if _, err := eng.Receive(withThread(domain.Trigger{
		ID: "trig-ding", Source: "dingtalk", Human: true,
		Actor: domain.Actor{ID: "colleague"}, Addressed: domain.Addressed{Direct: true},
		Threads:          []domain.ThreadRef{{Source: "dingtalk", ID: "dm-1"}},
		Text:             "把 webapp 的登录超时改成可配置",
		At:               time.Now(),
		Scope:            domain.WorkspaceScope("ws_web"),
		DefaultWorkspace: "ws_web",
	})); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.EvaluateDue(time.Now()); err != nil {
		t.Fatal(err)
	}
	rt.Publish()

	panel := snap.Workspaces()
	if panel == nil {
		t.Fatal("workspace panel was not published")
	}
	web := panel.Find("ws_web")
	if web == nil || len(web.Conversations) == 0 {
		t.Fatalf("panel = %+v", panel)
	}
	seenMe, seenAgent := false, false
	for _, c := range web.Conversations {
		if c.CreatedBy == view.CreatedByMe {
			seenMe = true
		}
		if c.CreatedBy == view.CreatedByAgent {
			seenAgent = true
		}
		if c.Stage == "" || c.StageID == "" {
			t.Fatalf("conversation missing stage: %+v", c)
		}
	}
	if !seenMe || !seenAgent {
		t.Fatalf("createdBy split wrong: me=%v agent=%v (%+v)", seenMe, seenAgent, web.Conversations)
	}
}

func TestOverviewSystemFacts(t *testing.T) {
	rt, _, snap := newRound3(t)
	rt.Publish()
	sys := snap.Overview().System
	if sys.OS == "" || sys.Arch == "" || sys.Timezone == "" || sys.UTCOffset == "" {
		t.Fatalf("system facts incomplete: %+v", sys)
	}
	facts := map[string]string{}
	for _, f := range rt.deployFacts() {
		facts[f.Key] = f.Value
	}
	if facts["os"] == "" || facts["timezone"] == "" {
		t.Fatalf("deploy facts missing OS/timezone: %+v", facts)
	}
}

// A local message is never left at "未受理": the stream carries an answer line.
func TestLocalMessagesGetAnAnswer(t *testing.T) {
	rt, _, _ := newRound3(t)
	rt.Publish()

	// Asking about workspaces lists them.
	if err := rt.Dispatch("我有哪些工作区？"); err == nil {
		t.Fatal("a non-dispatching local message must tell the composer why")
	}
	lines := streamLines(rt.stream())
	if !hasLine(lines, view.LineReply, "超级Agent：") || !hasLineText(lines, "web") {
		t.Fatalf("workspace answer did not answer in-stream: %+v", lines)
	}

	// Plain chat answers why nothing was dispatched, and how to dispatch.
	if err := rt.Dispatch("你好"); err == nil || !strings.Contains(err.Error(), "没有派发") {
		t.Fatalf("plain chat err = %v", err)
	}
	lines = streamLines(rt.stream())
	if !hasLineText(lines, "没有派发") || !hasLineText(lines, "新建会话") {
		t.Fatalf("plain-chat answer is not actionable: %+v", lines)
	}
	if hasLineText(lines, "未受理") && !hasLineText(lines, "超级Agent：") {
		t.Fatalf("a local message ended at 未受理: %+v", lines)
	}

	// Real work still creates a Ticket.
	if err := rt.Dispatch("在 webapp 里跑个命令"); err != nil {
		t.Fatalf("work request failed: %v", err)
	}
}

func streamLines(vm *view.StreamVM) []view.Line {
	var out []view.Line
	for _, th := range vm.Threads {
		out = append(out, th.Steps...)
	}
	return out
}

func hasLine(lines []view.Line, kind view.LineKind, prefix string) bool {
	for _, l := range lines {
		if l.Kind == kind && strings.HasPrefix(l.Text, prefix) {
			return true
		}
	}
	return false
}

func hasLineText(lines []view.Line, sub string) bool {
	for _, l := range lines {
		if strings.Contains(l.Text, sub) {
			return true
		}
	}
	return false
}
