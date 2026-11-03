package ui

import (
	"fmt"

	mui "github.com/egoist/mygo/ui"
)

// artifactsPanel is the full deliverable list, opened from the overview's 全部
// link. It is a modal rather than an eighth rail item: the rail is the shape of the
// product, and deliverables are a view over work that already has a home
// (design §6.10).
func (a *App) artifactsPanel(c *mui.Context) {
	if !a.artifactsOpen {
		return
	}
	tk := a.tk
	vm := a.snap.Artifacts()

	mui.Modal(c, &a.artifactsOpen, func() {
		mui.Column(c).Width(720).Gap(10).Children(func() {
			mui.Row(c).Gap(8).AlignItems(mui.Center).Children(func() {
				mui.Text(c, "交付物").FontSize(fs(17)).Bold().TextColor(tk.Text)
				if vm != nil && vm.Total > 0 {
					textChip(c, tk, fmt.Sprintf("%d 个", vm.Total))
				}
				mui.Spacer(c)
				mui.Text(c, "Esc 关闭").FontSize(fs(12)).TextColor(tk.TextFaint)
			})

			if vm == nil || vm.Empty() {
				mui.Text(c, "还没有交付物。工单产出后会出现在这里。").FontSize(fs(14)).
					TextColor(tk.TextFaint)
				return
			}

			// Workspace chips narrow the list the way a person scans a month of
			// output: by project, not by date.
			spaces := vm.Workspaces()
			if len(spaces) > 1 {
				mui.Row(c).Gap(6).Wrap().Children(func() {
					chipButton(c, tk, "全部", a.artifactWS == "", func() { a.artifactWS = "" })
					for _, ws := range spaces {
						ws := ws
						chipButton(c, tk, ws, a.artifactWS == ws, func() { a.artifactWS = ws })
					}
				})
			}

			rows := vm.Filter(a.artifactWS)
			if len(rows) == 0 {
				mui.Text(c, "这个工作区还没有交付物。").FontSize(fs(13)).TextColor(tk.TextFaint)
				return
			}
			mui.Scroll(c).Height(420).Children(func() {
				for _, r := range rows {
					r := r
					mui.Row(c).Padding(8, 10).Radius(7).Background(tk.SurfaceAlt).Gap(10).
						AlignItems(mui.Center).Children(func() {
						iconForArtifact(c, tk, r.Kind)
						mui.Column(c).Grow(1).Gap(2).Children(func() {
							mui.Text(c, r.Title).FontSize(fs(14)).TextColor(tk.Text)
							// Detail already carries size and time from the producer;
							// adding another timestamp here printed it twice.
							meta := fmt.Sprintf("%s · %s", r.Workspace, r.Ticket)
							if r.Detail != "" {
								meta = r.Detail + " · " + meta
							} else {
								meta = r.At.Format("01-02 15:04") + " · " + meta
							}
							mui.Text(c, meta).FontSize(fs(12)).SingleLine().TextColor(tk.TextFaint)
						})
						textChip(c, tk, r.Kind)
					})
				}
			})
		})
	})
}

// chipButton is one filter chip with its selected state.
func chipButton(c *mui.Context, tk Tokens, label string, active bool, fn func()) {
	mui.Row(c).Padding(4, 10).Radius(6).
		Background(ifColor(active, tk.SurfaceAlt, tk.Surface)).
		OnClick(fn).Children(func() {
		mui.Text(c, label).FontSize(fs(13)).
			TextColor(ifColor(active, tk.Text, tk.TextMuted))
	})
}
