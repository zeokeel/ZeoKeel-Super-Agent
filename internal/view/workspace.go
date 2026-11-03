package view

// 工作区与会话：the panel's left column. A workspace is one repository (or data
// directory) the agent works in; a conversation is one Ticket in it, named after
// the message that started it. The runtime projects them; the panel draws them.

import "time"

// Who created a conversation. A Ticket that came from a local Trigger (the
// operator typed it in the panel) is "me"; anything a Source brought in is the
// agent's.
const (
	CreatedByMe    = "me"
	CreatedByAgent = "agent"
)

// ConversationVM is one conversation of a workspace, which is one Ticket.
type ConversationVM struct {
	// ID is the Ticket's id: what the panel selects on 执行.
	ID string
	// Number is the Ticket's number, for "#42".
	Number int
	Title  string
	// Workspace is the workspace id the conversation belongs to.
	Workspace string
	// CreatedBy is CreatedByMe or CreatedByAgent.
	CreatedBy string
	// Source is where it came from: local, dingtalk, github, feishu, schedule.
	Source string
	// Stage is the waiting word, StageID the fact behind it.
	Stage   string
	StageID string
	// Attention is true while the conversation waits for a person's answer.
	Attention bool
	LastAt    time.Time
}

// WorkspaceEntryVM is one workspace in the sidebar and on 工作区.
type WorkspaceEntryVM struct {
	ID      string
	Name    string
	Root    string
	Purpose string
	// Kind is repo, data, docs or mixed.
	Kind string
	// Conversations are the workspace's Tickets, newest first, ended included
	// (the projector caps the list; the panel draws what it gets).
	Conversations []ConversationVM
	LastAt        time.Time
}

// WorkspacePanelVM is the workspace shard.
type WorkspacePanelVM struct {
	Workspaces []WorkspaceEntryVM
	// Selected is the workspace the panel shows, "" for 超级Agent.
	Selected  string
	UpdatedAt time.Time
}

// Find returns one workspace by id, or nil.
func (vm *WorkspacePanelVM) Find(id string) *WorkspaceEntryVM {
	if vm == nil || id == "" {
		return nil
	}
	for i := range vm.Workspaces {
		if vm.Workspaces[i].ID == id {
			return &vm.Workspaces[i]
		}
	}
	return nil
}

// SelectedWorkspace returns the workspace the projection says is selected, or
// the first one when it names none that exists. It is the page's fallback, so 工作区
// never opens on a blank page while workspaces exist.
func (vm *WorkspacePanelVM) SelectedWorkspace() *WorkspaceEntryVM {
	if vm == nil {
		return nil
	}
	if w := vm.Find(vm.Selected); w != nil {
		return w
	}
	if len(vm.Workspaces) == 0 {
		return nil
	}
	return &vm.Workspaces[0]
}

// ConversationGroup is one heading of 工作区's conversation list: 我创建的 or
// 超级Agent 创建的. An empty group has no heading (a heading with no rows reads as
// a failure).
type ConversationGroup struct {
	CreatedBy     string
	Label         string
	Conversations []ConversationVM
}

// Groups returns the conversations grouped by who created them, newest first
// inside each group, in the order 我创建的, then 超级Agent 创建的. Empty groups are
// left out.
func (e *WorkspaceEntryVM) Groups() []ConversationGroup {
	if e == nil {
		return nil
	}
	mine := e.Mine()
	agent := e.AgentMade()
	var out []ConversationGroup
	if len(mine) > 0 {
		out = append(out, ConversationGroup{CreatedBy: CreatedByMe, Label: "我创建的", Conversations: mine})
	}
	if len(agent) > 0 {
		out = append(out, ConversationGroup{CreatedBy: CreatedByAgent, Label: "超级Agent 创建的", Conversations: agent})
	}
	return out
}

// Mine returns the conversations the operator created, in the projection's order.
func (e *WorkspaceEntryVM) Mine() []ConversationVM {
	if e == nil {
		return nil
	}
	return filterConversations(e.Conversations, CreatedByMe)
}

// AgentMade returns the conversations the agent created.
func (e *WorkspaceEntryVM) AgentMade() []ConversationVM {
	if e == nil {
		return nil
	}
	return filterConversations(e.Conversations, CreatedByAgent)
}

func filterConversations(in []ConversationVM, createdBy string) []ConversationVM {
	var out []ConversationVM
	for _, c := range in {
		if c.CreatedBy == createdBy {
			out = append(out, c)
		}
	}
	return out
}

// CreatedLabel is who created the conversation, in one word for a badge.
func (c ConversationVM) CreatedLabel() string {
	if c.CreatedBy == CreatedByMe {
		return "我"
	}
	return "超级Agent"
}

// IDLabel is the conversation's Ticket number, "#42", or "" when the projector
// has no number.
func (c ConversationVM) IDLabel() string {
	if c.Number <= 0 {
		return ""
	}
	return "#" + itoa(c.Number)
}

// itoa keeps view free of fmt; numbers are small.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
