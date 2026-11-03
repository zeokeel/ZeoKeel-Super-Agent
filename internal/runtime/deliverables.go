package runtime

import (
	"time"

	"github.com/zeokeel/super-agent/internal/view"
)

// artifactsVM projects every deliverable, newest first. The overview shows the
// first three; this is what its 全部 link opens (design §6.10).
func (r *Runtime) artifactsVM(now time.Time) *view.ArtifactVM {
	vm := &view.ArtifactVM{UpdatedAt: now}
	for _, a := range r.eng.AllArtifacts() {
		row := view.ArtifactRow{
			ID: string(a.ID), Title: a.Title, Kind: string(a.Kind),
			Detail: a.Detail, Path: a.Path, URL: a.URL, At: a.At,
		}
		// The workspace and ticket number live on the Ticket, not the artifact; a
		// deliverable without them is not findable a month later.
		// Snapshot: this projection runs while a Worker may be writing the Ticket.
		if tk, ok := r.eng.TicketSnapshot(a.Ticket); ok {
			row.Workspace = string(tk.Workspace)
			row.Ticket = "#" + number(tk.Number)
		}
		vm.Rows = append(vm.Rows, row)
	}
	vm.Total = len(vm.Rows)
	return vm
}
