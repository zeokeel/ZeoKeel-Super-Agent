package view

// PlanVM is the task list of a running Ticket: what the Agent intends to do and
// where each step is. The panel draws it in the execution detail and in the chat
// as a task card, so progress is visible without opening a log.

import "time"

// Plan status values, in the words the panel shows.
const (
	PlanPending = "pending"
	PlanRunning = "running"
	PlanDone    = "done"
	PlanBlocked = "blocked"
	PlanFailed  = "failed"
)

// PlanItem is one step.
type PlanItem struct {
	ID     string
	Title  string
	Status string
	Detail string
}

// PlanVM is the published plan of one Ticket (TicketID "" means "the current
// work": the panel shows it where no Ticket is selected).
type PlanVM struct {
	TicketID  string
	Items     []PlanItem
	UpdatedAt time.Time
}

// Counts returns (done, total, running, blocked) for the summary line.
func (vm *PlanVM) Counts() (done, total, running, attention int) {
	if vm == nil {
		return 0, 0, 0, 0
	}
	for _, it := range vm.Items {
		switch it.Status {
		case PlanDone:
			done++
		case PlanRunning:
			running++
		case PlanBlocked, PlanFailed:
			attention++
		}
	}
	return done, len(vm.Items), running, attention
}
