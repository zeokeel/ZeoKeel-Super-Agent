package ui

import (
	"fmt"
	"time"

	mui "github.com/egoist/mygo/ui"

	"github.com/zeokeel/super-agent/internal/humanfmt"
	"github.com/zeokeel/super-agent/internal/view"
)

// 定时 on 触发: what makes the agent start work by itself, read-only. A schedule is
// configuration the agent may have created through its tool; changing it stays in
// 设置 (design §6.3: 配置动作只在设置), which the section's one guide row says.
func (a *App) scheduleSection(c *mui.Context) {
	tk := a.tk
	vm := a.snap.Schedules()

	mui.Column(c).Grow(1).MinWidth(0).Background(tk.Surface).Radius(tk.Radius).
		Border(1, tk.Border).Children(func() {
		mui.Row(c).Height(34).Padding(0, 14).Gap(8).AlignItems(mui.Center).Children(func() {
			mui.Icon(c, icClock).FontSize(fs(13)).TextColor(tk.Violet)
			mui.Text(c, "定时").FontSize(fs(15)).Bold().TextColor(tk.Text)
			if !vm.Empty() {
				chip(c, tk, fmt.Sprint(len(vm.Rows)), tk.TextMuted, tk.SurfaceAlt)
				if n := vm.RunningCount(); n > 0 {
					chip(c, tk, fmt.Sprintf("%d 个触发中", n), tk.Accent, tk.AccentSoft)
				}
			}
			mui.Spacer(c)
			mui.Text(c, "由 agent 创建").FontSize(fs(12)).TextColor(tk.TextFaint)
		})
		mui.Box(c).Height(1).Background(tk.BorderSoft)
		switch {
		case vm == nil:
			a.panelNote(c, "等待运行时发布定时快照…", tk.TextMuted)
		case vm.Empty():
			a.panelNote(c, "还没有定时任务，可以在消息里让它自己建，例如「每天 09:30 跑一次巡检」。", tk.TextMuted)
		default:
			mui.Scroll(c).Grow(1).Children(func() {
				mui.Column(c).Children(func() {
					for i, r := range vm.Rows {
						if i > 0 {
							mui.Box(c).Height(1).Background(tk.BorderSoft)
						}
						a.scheduleRow(c, r)
					}
				})
			})
		}
		mui.Box(c).Height(1).Background(tk.BorderSoft)
		mui.Column(c).Padding(7, 10).Children(func() {
			a.settingsHint(c, view.SettingsExecution, "在设置 › 执行里管理")
		})
	})
}

// scheduleRow is one timed trigger: identity, what it fires, where, when next,
// and what the last attempt did. A skipped fire is a fact with a reason, not an
// error: it reads amber, not red.
func (a *App) scheduleRow(c *mui.Context, r view.ScheduleRow) {
	tk := a.tk
	mui.Column(c).Padding(6, 14).Gap(2).Children(func() {
		mui.Row(c).Gap(8).AlignItems(mui.Center).Children(func() {
			mono(c, tk, r.ID)
			task := r.Task
			if task == "" {
				task = r.ID // a schedule the core could not describe is still visible
			}
			mui.Text(c, task).FontSize(fs(14)).SingleLine().Grow(1).TextColor(tk.Text)
			if r.Workspace != "" {
				textChip(c, tk, r.Workspace)
			}
			if r.Running {
				chip(c, tk, "触发中", tk.Accent, tk.AccentSoft)
			}
			fg, bg := tk.Green, tk.GreenSoft
			if !r.Enabled {
				fg, bg = tk.Red, tk.RedSoft
			}
			chip(c, tk, r.EnabledLabel(), fg, bg)
		})
		mui.Row(c).Gap(12).AlignItems(mui.Center).Children(func() {
			mui.Text(c, r.Spec).FontSize(fs(12)).TextColor(tk.TextMuted)
			mui.Text(c, a.scheduleNext(r)).FontSize(fs(12)).TextColor(tk.TextFaint)
			mui.Spacer(c)
			a.scheduleLast(c, r)
		})
	})
}

// scheduleLast is the honest line of the last attempt: when it fired, or why it
// was skipped. Nothing is shown before the first fire.
func (a *App) scheduleLast(c *mui.Context, r view.ScheduleRow) {
	tk := a.tk
	if r.LastStatus == "" {
		mui.Text(c, "还没触发过").FontSize(fs(12)).TextColor(tk.TextFaint)
		return
	}
	fg := tk.Green
	if r.LastStatus == "skipped" {
		fg = tk.Amber
	}
	mui.Text(c, "上次 "+r.LastStatusLabel()).FontSize(fs(12)).TextColor(fg)
	if r.LastSkipReason != "" {
		mui.Text(c, "· "+r.LastSkipReason).FontSize(fs(12)).SingleLine().TextColor(tk.Amber)
		return
	}
	if !r.LastFiredAt.IsZero() {
		mui.Text(c, "· "+humanfmt.Ago(a.age(r.LastFiredAt))).FontSize(fs(12)).TextColor(tk.TextFaint)
	}
}

// scheduleNext says when the next fire is, in relative and absolute time: "下次
// 11-03 11:30（2 小时后）". A disabled schedule says so instead of showing a stale
// instant.
func (a *App) scheduleNext(r view.ScheduleRow) string {
	switch {
	case !r.Enabled:
		return "已停用"
	case r.NextAt.IsZero():
		return "不再触发"
	}
	return fmt.Sprintf("下次 %s（%s）", r.NextAt.Format("01-02 15:04"), humanfmt.Until(a.until(r.NextAt)))
}

// until is how long it is from the frame's clock to t: negative once t passed.
func (a *App) until(t time.Time) time.Duration {
	if a.now.IsZero() || t.IsZero() {
		return 0
	}
	return t.Sub(a.now)
}

// panelNote is one honest line of a section that has nothing to list.
func (a *App) panelNote(c *mui.Context, text string, color mui.Color) {
	mui.Row(c).Padding(10, 14).Children(func() {
		mui.Text(c, text).FontSize(fs(13)).TextColor(color)
	})
}
