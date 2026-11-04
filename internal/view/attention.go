package view

// AttentionVM is the unified attention inbox: everything that waits for a person
// (an IM proposal that would start work, or a started task that needs an answer).
// The runtime publishes it with its own attention count; the panel falls back to
// OverviewVM.NeedsPerson until it does, so 收件箱 is never empty while the
// topbar badge says there is something.

import "time"

// AttentionItemVM is one thing to decide.
type AttentionItemVM struct {
	Kind       string // "proposal" | "ticket"
	DecisionID string
	TicketID   string
	Number     int
	Source     string
	Sender     string
	Text       string
	Workspace  string
	At         time.Time
	// Actions are the choices offered: approve, reject (proposal) and answer,
	// retry, stop, open (ticket).
	Actions []string
}

// Has reports whether the item offers one action.
func (i AttentionItemVM) Has(action string) bool {
	for _, a := range i.Actions {
		if a == action {
			return true
		}
	}
	return false
}

// AttentionVM is the inbox projection.
type AttentionVM struct {
	Items     []AttentionItemVM
	UpdatedAt time.Time
}

// Count is the one "待你决定 N" number.
func (vm *AttentionVM) Count() int {
	if vm == nil {
		return 0
	}
	return len(vm.Items)
}
