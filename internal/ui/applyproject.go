package ui

// 应用到项目: copy a Ticket's deliverables from the execution copy back into the
// project directory — with a confirmation that names the destination and the
// files, and an overwrite warning. It is an optional capability: without it the
// button is disabled and says so.

import (
	"fmt"
	"strings"

	mui "github.com/egoist/mygo/ui"

	"github.com/zeokeel/super-agent/internal/view"
)

// ArtifactApplier copies one Ticket's artifacts into the project. The panel never
// copies files itself. ArtifactRelPaths is what the confirmation lists (relative
// paths, so the dialog names exactly what will be written).
type ArtifactApplier interface {
	ApplyArtifacts(ticketID string) ([]string, error)
	ArtifactRelPaths(ticketID string) []string
}

// startLoadArtifactPaths reads the file list for the confirmation dialog.
var startLoadArtifactPaths = func(a *App, ticketID string) {
	a.runOffMain(func() {
		applier, ok := a.cmd.(ArtifactApplier)
		var paths []string
		if ok && applier != nil {
			paths = applier.ArtifactRelPaths(ticketID)
		}
		a.postToUI(func() {
			a.applyPaths = paths
			a.applyPathsLoading = false
		})
	})
}

var startApplyArtifacts = func(a *App, ticketID string) {
	a.runOffMain(func() {
		applier, ok := a.cmd.(ArtifactApplier)
		var files []string
		var err error
		if !ok || applier == nil {
			err = errNoApplyToProject{}
		} else {
			files, err = applier.ApplyArtifacts(ticketID)
		}
		a.postToUI(func() {
			a.applyPending = false
			if err != nil {
				a.applyErr = err.Error()
				return
			}
			a.applyOpen = false
			a.applyErr = ""
			word := "文件"
			if len(files) == 1 {
				word = "1 个文件"
			} else if len(files) > 1 {
				word = fmt.Sprintf("%d 个文件", len(files))
			}
			a.toast("已应用到项目："+word, toastOK)
		})
	})
}

type errNoApplyToProject struct{}

func (errNoApplyToProject) Error() string {
	return "暂时不可用：运行时还没有接入应用到项目"
}

// openApply opens the confirmation with the ticket's artifacts and destination.
func (a *App) openApply(r view.TicketRow, d *view.TicketDetailVM) {
	a.applyOpen = true
	a.applyTicket = r.ID
	a.applyTitle = view.TicketLabel(r.ID, r.Number) + " " + r.Title
	a.applyProject = ""
	a.applyErr = ""
	a.applyPending = false
	a.applyArtifacts = nil
	a.applyPaths = nil
	a.applyPathsLoading = true
	if d != nil {
		a.applyProject = d.ProjectRoot
		a.applyArtifacts = append([]view.Artifact(nil), d.Artifacts...)
	}
	if _, ok := a.cmd.(ArtifactApplier); ok {
		startLoadArtifactPaths(a, r.ID)
	} else {
		a.applyPathsLoading = false
	}
}

// applyDialog draws the confirmation: destination, files, overwrite warning.
func (a *App) applyDialog(c *mui.Context) {
	if !a.applyOpen {
		return
	}
	tk := a.tk
	_, wired := a.cmd.(ArtifactApplier)
	open := true
	mui.Modal(c, &open, func() {
		mui.Column(c).Width(640).Gap(10).Children(func() {
			mui.Text(c, "应用到项目？").FontSize(fs(18)).Bold().TextColor(tk.Text)
			mui.Text(c, a.applyTitle).FontSize(fs(13)).TextColor(tk.TextMuted)
			mui.Text(c, "会把执行副本里的交付物复制到项目目录（项目里已有的同名文件会被覆盖）：").
				FontSize(fs(12)).TextColor(tk.TextMuted)
			mui.Row(c).Gap(8).AlignItems(mui.Center).Children(func() {
				mui.Text(c, "目标").FontSize(fs(11)).Width(48).Shrink(0).TextColor(tk.TextFaint)
				if a.applyProject == "" {
					mui.Text(c, "项目目录（运行时还没有提供路径）").FontSize(fs(12)).
						TextColor(tk.Amber)
				} else {
					mui.Text(c, a.applyProject).FontSize(fs(12)).Font("monospace").
						SingleLine().Grow(1).Tooltip(a.applyProject).TextColor(tk.TextMuted)
					if b := mui.Button(c, "打开").Height(24).FontSize(fs(10)); b.Clicked() {
						a.openPath(a.applyProject)
					}
				}
			})
			mui.Text(c, "将写入的文件").FontSize(fs(11)).TextColor(tk.TextFaint)
			switch {
			case a.applyPathsLoading:
				spinner(c, tk, "正在读取文件列表…")
			case len(a.applyPaths) > 0:
				for _, rel := range a.applyPaths {
					rel := rel
					mui.Row(c).Gap(8).AlignItems(mui.Center).Children(func() {
						mui.Icon(c, icFile).FontSize(fs(12)).TextColor(tk.TextMuted)
						mui.Text(c, rel).FontSize(fs(12)).Font("monospace").Grow(1).
							SingleLine().Tooltip(rel).TextColor(tk.Text)
					})
				}
			case len(a.applyArtifacts) > 0:
				for _, art := range a.applyArtifacts {
					art := art
					mui.Row(c).Gap(8).AlignItems(mui.Center).Children(func() {
						mui.Icon(c, iconForArtifactIcon(art.Kind)).FontSize(fs(12)).TextColor(tk.TextMuted)
						mui.Text(c, art.Title).FontSize(fs(13)).Grow(1).TextColor(tk.Text)
						if art.Path != "" {
							mui.Text(c, art.Path).FontSize(fs(10)).Font("monospace").SingleLine().
								Tooltip(art.Path).TextColor(tk.TextFaint)
						}
					})
				}
			default:
				mui.Text(c, "这个任务还没有可应用的交付物。").
					FontSize(fs(12)).TextColor(tk.TextFaint)
			}
			if a.applyErr != "" {
				mui.Text(c, a.applyErr).FontSize(fs(12)).TextColor(tk.Red)
			}
			if !wired {
				mui.Text(c, "暂时不可用：运行时还没有接入应用到项目").
					FontSize(fs(11)).TextColor(tk.Amber)
			}
			mui.Row(c).Gap(8).AlignItems(mui.Center).Children(func() {
				mui.Spacer(c)
				if mui.Button(c, "取消").Label("取消").Clicked() {
					a.applyOpen = false
					a.applyErr = ""
				}
				label := "确认应用"
				if a.applyPending {
					label = "应用中…"
				}
				btn := mui.PrimaryButton(c, label).Label(label).Height(30)
				noFiles := !a.applyPathsLoading && len(a.applyPaths) == 0 && len(a.applyArtifacts) == 0
				if !wired || a.applyPending || noFiles {
					btn.Disabled(true)
				} else if btn.Clicked() {
					a.applyPending = true
					a.applyErr = ""
					startApplyArtifacts(a, a.applyTicket)
				}
			})
		})
	})
	if !open {
		a.applyOpen = false
	}
}

// iconForArtifactIcon is the icon of an artifact kind for the dialog.
func iconForArtifactIcon(kind string) *mui.SVG {
	switch strings.ToLower(kind) {
	case "pr", "link":
		return icBranch
	case "image":
		return icImage
	case "report", "file":
		return icFile
	}
	return icFile
}
