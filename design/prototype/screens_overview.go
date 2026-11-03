package main

import "github.com/egoist/mygo/ui"

func iconFor(name string) *ui.SVG {
	switch name {
	case "branch":
		return icBranch
	case "file":
		return icFile
	case "image":
		return icImage
	case "code":
		return icTerminal
	}
	return icFile
}

// ---- 01 · Overview: the three questions of the panel ----
// ① What needs me? ② Is the system healthy? ③ What happened in 24h?

func overview(c *ui.Context) {
	overviewScene(c, nil)
}

func overviewScene(c *ui.Context, overlay func(*ui.Context)) {
	tk := tokens()
	windowWith(c, tk, "overview", func() {
		content(c, func() {
			ui.Row(c).AlignItems(ui.Center).Children(func() {
				heading(c, tk, "总览", "需要你关注的只有 3 件事，其余都在后台运行")
				ui.Spacer(c)
				textChip(c, tk, "过去 24 小时")
				smallButton(c, tk, "刷新")
			})
			ui.Row(c).Grow(1).AlignItems(ui.Stretch).Gap(20).Children(func() {
				// ---- left: what needs me, then health and activity ----
				ui.Column(c).Grow(1).Gap(16).Children(func() {
					needsCard(c, tk)
					ui.Row(c).Grow(1).AlignItems(ui.Stretch).Gap(16).Children(func() {
						healthCard(c, tk)
						activityCard(c, tk)
					})
				})
				// ---- right: runtime at a glance ----
				ui.Column(c).Width(320).Gap(16).Children(func() {
					runtimeCard(c, tk)
					executorsCard(c, tk)
					recentCard(c, tk)
					storageCard(c, tk)
				})
			})
		})
	}, overlay)
}

func needsCard(c *ui.Context, tk Tokens) {
	card(c, tk, 16, func() {
		ui.Row(c).AlignItems(ui.Center).Gap(10).Children(func() {
			ui.Text(c, "需要我处理").FontSize(15).Bold().TextColor(tk.Text)
			chip(c, tk, "3", ui.Hex("#FFFFFF"), tk.Amber)
			ui.Spacer(c)
			ui.Text(c, "按等待时长排序").FontSize(11).TextColor(tk.TextFaint)
		})
		for _, n := range needs {
			needRow(c, tk, n)
		}
	})
}

func needRow(c *ui.Context, tk Tokens, n needItem) {
	ui.Row(c).Height(62).Padding(0, 12).Gap(12).Radius(8).Background(tk.SurfaceAlt).
		AlignItems(ui.Center).Children(func() {
		ui.Box(c).Size(34, 34).Radius(9).Background(ui.Hex(n.sourceBG)).Center().
			Children(func() { ui.Text(c, n.sourceText).FontSize(12).Bold().TextColor(ui.Hex("#FFFFFF")) })
		ui.Column(c).Grow(1).Gap(3).Children(func() {
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				ui.Text(c, n.id).FontSize(11).TextColor(tk.TextFaint)
				ui.Text(c, n.title).FontSize(13).Bold().TextColor(tk.Text)
			})
			ui.Text(c, n.reason).FontSize(12).TextColor(tk.TextMuted)
		})
		chip(c, tk, n.stage, ui.Hex(n.stageFG), ui.Hex(n.stageBG))
		ui.Text(c, n.age).FontSize(11).TextColor(tk.TextFaint)
		ui.Icon(c, icChevR).FontSize(14).TextColor(tk.TextFaint)
	})
}

func healthCard(c *ui.Context, tk Tokens) {
	rows := []struct {
		name, value string
		color       ui.Color
	}{
		{"GitHub Token", "有效 · 40 天后过期", tk.Green},
		{"Feishu 订阅", "有效 · 3 个群", tk.Green},
		{"SQLite", "健康 · WAL 正常", tk.Green},
		{"执行器沙箱", "工作区可写 · 目录已隔离", tk.Green},
		{"MCP notion", "未连接（可选）", tk.Amber},
	}
	card(c, tk, 16, func() {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Text(c, "系统健康").FontSize(15).Bold().TextColor(tk.Text)
			ui.Spacer(c)
			chip(c, tk, "1 项待处理", tk.Amber, tk.AmberSoft)
		})
		ui.Column(c).Grow(1).Children(func() {
			for i, r := range rows {
				row := r
				last := i == len(rows)-1
				ui.Row(c).Height(40).Gap(10).AlignItems(ui.Center).
					BorderWidth(0, 0, condF(last), 0).BorderColor(tk.BorderSoft).
					Children(func() {
						dot(c, row.color, 7)
						ui.Text(c, row.name).FontSize(12).TextColor(tk.Text)
						ui.Spacer(c)
						ui.Text(c, row.value).FontSize(11).TextColor(tk.TextMuted)
					})
			}
		})
		ui.Text(c, "上次检查 12 秒前 · 每 30 秒自动检查").FontSize(11).TextColor(tk.TextFaint)
	})
}

func condF(b bool) float32 {
	if b {
		return 1
	}
	return 0
}

func activityCard(c *ui.Context, tk Tokens) {
	bars := []float32{0.22, 0.16, 0.12, 0.18, 0.3, 0.45, 0.58, 0.72, 0.6, 0.78, 0.55, 0.38}
	card(c, tk, 16, func() {
		ui.Row(c).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "过去 24 小时").FontSize(15).Bold().TextColor(tk.Text)
			ui.Spacer(c)
			ui.Text(c, "186 Triggers").FontSize(11).TextColor(tk.TextMuted)
		})
		ui.Row(c).Grow(1).AlignItems(ui.End).Gap(8).Children(func() {
			for i, v := range bars {
				h := v * 150
				bg := ui.RGBA(47, 91, 255, 0.22)
				if i == 9 {
					bg = tk.Accent
				}
				ui.Column(c).Grow(1).Justify(ui.End).Children(func() {
					ui.Box(c).Height(h).Radius(3).Background(bg)
				})
			}
		})
		ui.Row(c).Gap(8).Children(func() {
			for _, l := range []string{"00", "02", "04", "06", "08", "10", "12", "14", "16", "18", "20", "22"} {
				ui.Text(c, l).FontSize(10).TextColor(tk.TextFaint).TextAlign(ui.Center).Grow(1)
			}
		})
		ui.Row(c).Gap(10).Children(func() {
			stat(c, tk, "186", "Triggers")
			stat(c, tk, "24", "Eval")
			stat(c, tk, "9", "Decision")
			stat(c, tk, "4", "Ticket")
		})
	})
}

func stat(c *ui.Context, tk Tokens, num, label string) {
	ui.Column(c).Grow(1).Gap(1).Children(func() {
		ui.Text(c, num).FontSize(18).Bold().TextColor(tk.Text)
		ui.Text(c, label).FontSize(11).TextColor(tk.TextMuted)
	})
}

func runtimeCard(c *ui.Context, tk Tokens) {
	card(c, tk, 16, func() {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Text(c, "运行时").FontSize(14).Bold().TextColor(tk.Text)
			dot(c, tk.Green, 7)
			ui.Spacer(c)
			chip(c, tk, "正常", tk.Green, tk.GreenSoft)
		})
		ui.Row(c).AlignItems(ui.Baseline).Gap(6).Children(func() {
			ui.Text(c, "1").FontSize(26).Bold().TextColor(tk.Text)
			ui.Text(c, "/ 2").FontSize(16).TextColor(tk.TextMuted)
			ui.Text(c, "executor 忙").FontSize(12).TextColor(tk.TextMuted)
		})
		ui.Row(c).Height(5).Radius(3).Background(tk.BorderSoft).Children(func() {
			ui.Box(c).WidthPercent(50).Height(5).Radius(3).Background(tk.Accent)
		})
		ui.Row(c).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "队列 0 · 今日运行 42 次").FontSize(11).TextColor(tk.TextMuted)
			ui.Spacer(c)
			ui.Icon(c, icClock).FontSize(11).TextColor(tk.TextFaint)
			ui.Text(c, " 轮询 00:48").FontSize(11).TextColor(tk.TextFaint)
		})
	})
}

func executorsCard(c *ui.Context, tk Tokens) {
	card(c, tk, 16, func() {
		ui.Row(c).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "执行器").FontSize(14).Bold().TextColor(tk.Text)
			ui.Spacer(c)
			ui.Text(c, "打开 Execution →").FontSize(11).TextColor(tk.Accent)
		})
		ui.Column(c).Gap(8).Children(func() {
			execRow(c, tk, tk.Green, "A", "#42 修改登录超时配置", "12m · step 7")
			execRow(c, tk, tk.TextFaint, "B", "空闲", "等待派工")
		})
	})
}

func execRow(c *ui.Context, tk Tokens, color ui.Color, name, title, meta string) {
	ui.Row(c).Height(40).Padding(0, 10).Radius(7).Background(tk.SurfaceAlt).
		AlignItems(ui.Center).Gap(9).Children(func() {
		dot(c, color, 7)
		ui.Column(c).Grow(1).Gap(1).Children(func() {
			ui.Text(c, name+" · "+title).FontSize(12).TextColor(tk.Text)
			ui.Text(c, meta).FontSize(10).TextColor(tk.TextFaint)
		})
	})
}

func recentCard(c *ui.Context, tk Tokens) {
	cardG(c, tk, 16, func() {
		ui.Row(c).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "最近交付物").FontSize(14).Bold().TextColor(tk.Text)
			ui.Spacer(c)
			ui.Text(c, "全部").FontSize(11).TextColor(tk.Accent)
		})
		for _, a := range recentArtifacts {
			it := a
			ui.Row(c).Height(40).Padding(0, 8).Gap(9).Radius(7).Background(tk.SurfaceAlt).
				AlignItems(ui.Center).Children(func() {
				ui.Box(c).Size(26, 26).Radius(7).Background(tk.AccentSoft).Center().Children(func() {
					ui.Icon(c, iconFor(it.icon)).FontSize(14).TextColor(tk.Accent)
				})
				ui.Column(c).Grow(1).Gap(1).Children(func() {
					ui.Text(c, it.name).FontSize(11).TextColor(tk.Text).MaxLines(1).Ellipsis("…")
					ui.Text(c, it.meta).FontSize(10).TextColor(tk.TextFaint)
				})
				ui.Icon(c, icChevR).FontSize(12).TextColor(tk.TextFaint)
			})
		}
	})
}

func storageCard(c *ui.Context, tk Tokens) {
	card(c, tk, 16, func() {
		ui.Text(c, "存储与保留").FontSize(14).Bold().TextColor(tk.Text)
		kv(c, tk, "SQLite", "12.4 MB")
		kv(c, tk, "会话事件", "1,284 条")
		kv(c, tk, "上次备份", "今日 02:00")
	})
}
