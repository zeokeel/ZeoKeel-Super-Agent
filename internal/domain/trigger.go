package domain

import "time"

// ConversationKind names the shape of the conversation a message arrived in.
// It is a fact, not a classification of intent: a direct message can be a
// request or a notice, and so can a group mention. See design §4.9.
type ConversationKind string

const (
	ConvDirect    ConversationKind = "direct"
	ConvGroup     ConversationKind = "group"
	ConvTopic     ConversationKind = "topic"
	ConvBroadcast ConversationKind = "broadcast"
	ConvSystem    ConversationKind = "system"
)

// Conversation describes where a message arrived.
type Conversation struct {
	Kind   ConversationKind
	ID     string // chat id / dm id / channel id
	RootID string // topic root, empty when the conversation is not a topic
	Name   string // group name; also feeds the workspace brief's keywords
}

// Addressed records what the message does to address the agent. Both fields are
// facts. Two rules are easy to get wrong:
//   - an @everyone mention is not addressing the agent;
//   - replying to an agent message is a fact, not by itself an address.
//
// See design §4.9.
type Addressed struct {
	Mention bool // mentions the agent explicitly
	Direct  bool // a direct message to the agent
	ReplyTo bool // replies to a message of the agent's
}

// AddressesAgent reports whether the message addresses the agent at all.
func (a Addressed) AddressesAgent() bool { return a.Mention || a.Direct }

// Actor is who sent a message, in the Source's own identity space. Identities
// are never mapped across Sources (001 Domain model).
type Actor struct {
	ID  string
	Bot bool
}

// ThreadRef is the Source's own id of a Thread, plus the Source that names it.
type ThreadRef struct {
	Source string
	ID     string
}

// ChannelRef is where a reply to this Trigger goes.
type ChannelRef struct {
	Source  string
	Thread  string
	Address map[string]string
}

// Trigger is one immutable event from a Source. It belongs to one or more
// Threads; accepting it twice creates one Ticket, because its ID is derived from
// the event.
type Trigger struct {
	ID    ID
	Scope Scope // a workspace for a workspace-scoped Source, Global otherwise

	Source  string
	Threads []ThreadRef

	Actor Actor
	Human bool
	// Self is true when the message came from the operator's own account on this
	// Source — their own DingTalk or WeChat, not a colleague's. The same words
	// mean something different from your own account than from someone else's,
	// so this is a Trigger fact, decided when the message is translated.
	Self      bool
	Addressed Addressed

	Conversation Conversation
	Text         string
	At           time.Time
	Channel      ChannelRef

	// DefaultWorkspace is the workspace a workspace-scoped Source is bound to.
	// It is empty for a global Source, whose Trigger cannot know its workspace
	// until the Router decides. See design §4.7 修正 1.
	DefaultWorkspace WorkspaceID

	// Row is the receiving scope's admission-table row, as the Source names it:
	// "issue" | "pull" | "comment" | "ci" for GitHub, a chat id for an IM, and
	// "panel" for the local Source. Empty when the Source does not say.
	Row string `json:"row,omitempty"`
	// ScopeReceivesAll is the Source's fact that this scope receives every
	// message rather than only mentions, which unlocks the two unaddressed
	// admission cells (002:71).
	ScopeReceivesAll bool `json:"scopeReceivesAll,omitempty"`
	// OwnThread is the Thread the Source considers the Trigger's own; empty means
	// the first Thread. Subscriptions to every other Thread are context only.
	OwnThread string `json:"ownThread,omitempty"`
}

// DispositionKind records what happened to a Trigger.
type DispositionKind string

const (
	DispFiltered   DispositionKind = "filtered" // dropped by scope or the gate
	DispEvaluating DispositionKind = "evaluating"
	DispDecided    DispositionKind = "decided"
	DispFanout     DispositionKind = "fanout"  // delivered to subscribed Tickets as context
	DispOwned      DispositionKind = "owned"   // delivered to the Ticket that owns the Thread; no Eval
	DispResumed    DispositionKind = "resumed" // resumed an ended Ticket under its Mandate; no Eval
	DispHeld       DispositionKind = "held"    // accepted but held while processing is paused
)

// Disposition is the audit record of one Trigger: why it went where it went.
type Disposition struct {
	TriggerID ID
	Kind      DispositionKind
	Reason    []Reason
	TicketIDs []ID
	// Strength is what the deterministic gate decided (002:11-16).
	Strength Strength `json:"strength,omitempty"`
	// Basis explains the strength.
	Basis Basis `json:"basis,omitempty"`
	// EvalID is the Eval that judged the Trigger, when one did.
	EvalID ID `json:"evalId,omitempty"`
	// DecisionID is the Decision the Eval produced.
	DecisionID ID `json:"decisionId,omitempty"`
}
