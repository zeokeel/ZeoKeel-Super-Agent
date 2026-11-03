package view

import "time"

// ArtifactRow is one deliverable on the 交付物 panel. It is the same data the
// overview's short list shows, but complete and filterable: the "全部" link used to
// be a label with nowhere to go (design §6.10).
type ArtifactRow struct {
	ID    string
	Title string
	// Kind is file/image/report/pr/link, unchanged from the domain so the panel
	// draws each without translating it back.
	Kind   string
	Detail string
	Path   string
	URL    string
	// Workspace and Ticket say where it came from, which is what a person sorting
	// through a month of deliverables actually needs.
	Workspace string
	Ticket    string
	At        time.Time
}

// ArtifactVM is everything the 交付物 panel draws.
type ArtifactVM struct {
	Rows      []ArtifactRow
	Total     int
	UpdatedAt time.Time
}

// Workspaces lists the workspaces present, for the filter chips, in first-seen
// order so the chips do not jump around as artifacts arrive.
func (vm *ArtifactVM) Workspaces() []string {
	if vm == nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, r := range vm.Rows {
		if r.Workspace == "" || seen[r.Workspace] {
			continue
		}
		seen[r.Workspace] = true
		out = append(out, r.Workspace)
	}
	return out
}

// Filter returns the rows of one workspace, or every row when ws is empty.
func (vm *ArtifactVM) Filter(ws string) []ArtifactRow {
	if vm == nil {
		return nil
	}
	if ws == "" {
		return vm.Rows
	}
	var out []ArtifactRow
	for _, r := range vm.Rows {
		if r.Workspace == ws {
			out = append(out, r)
		}
	}
	return out
}

// Empty reports whether there is nothing to show at all.
func (vm *ArtifactVM) Empty() bool { return vm == nil || len(vm.Rows) == 0 }
