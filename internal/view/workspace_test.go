package view_test

// The workspace/conversation projection, the system facts the sidebar shows, and
// the new section: the panel's contracts are pinned here, without a window.

import (
	"testing"
	"time"

	. "github.com/zeokeel/super-agent/internal/view"
)

func sampleWorkspaces(now time.Time) *WorkspacePanelVM {
	return &WorkspacePanelVM{
		Selected: "ws_web",
		Workspaces: []WorkspaceEntryVM{
			{
				ID: "ws_web", Name: "acme/webapp", Root: "/code/webapp", Purpose: "前端", Kind: "repo",
				Conversations: []ConversationVM{
					{ID: "42", Number: 42, Title: "改登录超时", CreatedBy: CreatedByAgent, LastAt: now},
					{ID: "48", Number: 48, Title: "配置化", CreatedBy: CreatedByMe, LastAt: now.Add(-time.Hour)},
					{ID: "50", Number: 50, Title: "空状态", CreatedBy: CreatedByMe, LastAt: now.Add(-2 * time.Hour)},
				},
			},
			{ID: "ws_data", Name: "acme/data", Root: "/srv/data", Purpose: "数据", Kind: "data"},
		},
		UpdatedAt: now,
	}
}

// Find returns the workspace asked for, and SelectedWorkspace falls back to the
// first when the projection names one that does not exist.
func TestWorkspacePanelFindAndSelected(t *testing.T) {
	vm := sampleWorkspaces(time.Now())
	if w := vm.Find("ws_web"); w == nil || w.Name != "acme/webapp" {
		t.Fatalf("Find(ws_web) = %+v", w)
	}
	if w := vm.Find("nope"); w != nil {
		t.Fatalf("Find(nope) = %+v", w)
	}
	if sel := vm.SelectedWorkspace(); sel == nil || sel.ID != "ws_web" {
		t.Fatalf("SelectedWorkspace = %+v", sel)
	}
	vm.Selected = "gone"
	if sel := vm.SelectedWorkspace(); sel == nil || sel.ID != "ws_web" {
		t.Fatalf("a missing selection must fall back to the first workspace, got %+v", sel)
	}
	var nilVM *WorkspacePanelVM
	if nilVM.Find("x") != nil || nilVM.SelectedWorkspace() != nil {
		t.Fatal("a nil panel answered")
	}
	var empty WorkspacePanelVM
	if empty.SelectedWorkspace() != nil {
		t.Fatal("an empty panel selected a workspace")
	}
}

// Conversations are grouped by who created them: 我创建的 first, then
// 超级Agent 创建的, with empty groups left out.
func TestWorkspaceGroups(t *testing.T) {
	vm := sampleWorkspaces(time.Now())
	web := vm.Find("ws_web")
	groups := web.Groups()
	if len(groups) != 2 {
		t.Fatalf("%d groups, want 2: %+v", len(groups), groups)
	}
	if groups[0].CreatedBy != CreatedByMe || groups[0].Label != "我创建的" || len(groups[0].Conversations) != 2 {
		t.Fatalf("first group %+v", groups[0])
	}
	if groups[1].CreatedBy != CreatedByAgent || groups[1].Label != "超级Agent 创建的" || len(groups[1].Conversations) != 1 {
		t.Fatalf("second group %+v", groups[1])
	}
	// The data workspace has no conversations: no headings at all.
	if got := vm.Find("ws_data").Groups(); len(got) != 0 {
		t.Fatalf("an empty workspace drew groups: %+v", got)
	}
	var nilEntry *WorkspaceEntryVM
	if nilEntry.Groups() != nil || nilEntry.Mine() != nil || nilEntry.AgentMade() != nil {
		t.Fatal("a nil workspace answered")
	}
}

// The labels the sidebar and page put on a conversation.
func TestConversationLabels(t *testing.T) {
	me := ConversationVM{CreatedBy: CreatedByMe, Number: 42}
	agent := ConversationVM{CreatedBy: CreatedByAgent, Number: 0}
	if me.CreatedLabel() != "我" || agent.CreatedLabel() != "超级Agent" {
		t.Fatalf("created labels %q %q", me.CreatedLabel(), agent.CreatedLabel())
	}
	if me.IDLabel() != "#42" {
		t.Fatalf("IDLabel = %q, want #42", me.IDLabel())
	}
	if agent.IDLabel() != "" {
		t.Fatalf("a conversation without a number labelled itself %q", agent.IDLabel())
	}
}

// The system line the sidebar footer and 设置 › 部署 show.
func TestSystemInfoLabel(t *testing.T) {
	full := SystemInfo{OS: "linux", Arch: "amd64", Timezone: "Asia/Shanghai", UTCOffset: "UTC+08:00"}
	if !full.Known() {
		t.Fatal("a filled system is not known")
	}
	if got, want := full.Label(), "linux/amd64 · Asia/Shanghai (UTC+08:00)"; got != want {
		t.Fatalf("Label = %q, want %q", got, want)
	}
	if got := (SystemInfo{OS: "darwin", Arch: "arm64"}).Label(); got != "darwin/arm64" {
		t.Fatalf("partial label = %q", got)
	}
	if got := (SystemInfo{Timezone: "UTC"}).Label(); got != "UTC" {
		t.Fatalf("timezone-only label = %q", got)
	}
	var zero SystemInfo
	if zero.Known() || zero.Label() != "" {
		t.Fatalf("a zero system is known (%v) or labelled (%q)", zero.Known(), zero.Label())
	}
}

// 工作区 is a section of its own, and every section has a label a person reads.
func TestSectionsAndLabels(t *testing.T) {
	if SecWorkspace == "" || SecWorkspace == SecOverview {
		t.Fatal("SecWorkspace is not a distinct section")
	}
	if got := SectionLabel(SecWorkspace); got != "工作区" {
		t.Fatalf("SectionLabel(SecWorkspace) = %q", got)
	}
	for _, tc := range []struct {
		sec  Section
		want string
	}{
		{SecOverview, "超级Agent"},
		{SecMessages, "消息"},
		{SecSource, "来源"},
		{SecTrigger, "触发"},
		{SecEval, "评估"},
		{SecExecution, "执行"},
		{SecSettings, "设置"},
	} {
		if got := SectionLabel(tc.sec); got != tc.want {
			t.Fatalf("SectionLabel(%q) = %q, want %q", tc.sec, got, tc.want)
		}
	}
	// The monitoring rail keeps its order and does not carry 工作区: that is
	// reached through a workspace row.
	nav := Nav(nil)
	if len(nav) != 7 {
		t.Fatalf("Nav has %d items: %+v", len(nav), nav)
	}
	if nav[0].Section != SecOverview || nav[0].Label != "超级Agent" {
		t.Fatalf("Nav[0] = %+v", nav[0])
	}
	for _, it := range nav {
		if it.Section == SecWorkspace {
			t.Fatal("Nav carries 工作区, which the sidebar draws as a tree")
		}
	}
}

// The workspaces shard publishes, reads back and bumps the revision like every
// other shard; before the first publish it is nil, not an empty panel.
func TestSnapshotPublishWorkspaces(t *testing.T) {
	snap := NewSnapshot()
	if snap.Workspaces() != nil {
		t.Fatal("an empty snapshot returned a workspace panel")
	}
	frames := 0
	snap.OnPublish(func() { frames++ })
	vm := sampleWorkspaces(time.Now())
	snap.PublishWorkspaces(vm)
	if snap.Workspaces() != vm {
		t.Fatal("the workspace shard did not read back what it published")
	}
	if frames != 1 || snap.Revision() != 1 {
		t.Fatalf("frames=%d rev=%d, want 1/1", frames, snap.Revision())
	}
}
