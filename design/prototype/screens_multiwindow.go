package main

import "github.com/egoist/mygo/ui"

// ---- 13 · Multiple windows (M5, not the first version) ----
// M2 ships the switcher and workspace admin; pinned windows are a later
// milestone, so this screen is marked M5 everywhere it appears.

func multiWindow(c *ui.Context) {
	tk := tokens()
	ui.Box(c).Fill().Gradient(ui.Hex("#0B1B33"), ui.Hex("#16345C"), 35).Children(func() {
		ui.Row(c).Height(64).Padding(0, 40).Gap(12).AlignItems(ui.Center).Children(func() {
			ui.Image(c, logo).Size(26, 26).Radius(7)
			ui.Text(c, "⌘N 多窗口并排").FontSize(20).Bold().TextColor(ui.Hex("#FFFFFF"))
			chip(c, tk, "M5 · 非首版", tk.Amber, tk.AmberSoft)
			ui.Spacer(c)
			ui.Text(c, "两个窗口各自钉住一个工作区，共用同一进程、同一 runtime、同一托盘").FontSize(11).
				TextColor(ui.RGBA(255, 255, 255, 0.65))
		})
		ui.Row(c).Grow(1).Padding(0, 40).Gap(24).AlignItems(ui.Stretch).Children(func() {
			windowFrame(c, tk, "acme/webapp", ui.Hex("#2F5BFF"), "3 需要我 · 1 执行中", true, func() {
				ui.Text(c, "修改登录超时配置").FontSize(13).Bold().TextColor(tk.Text)
				ui.Row(c).Gap(6).Children(func() {
					chip(c, tk, "● 执行中", tk.Green, tk.GreenSoft)
					chip(c, tk, "step 7", tk.Violet, tk.VioletSoft)
				})
				transcriptLine(c, tk, "bash rg -n \"15 * time.Minute\" · exit 0")
				transcriptLine(c, tk, "edit internal/auth/session.go · +12 −3")
				transcriptLine(c, tk, "(正在补默认值与校验… ▍)")
				ui.Spacer(c)
				ui.Row(c).Padding(8, 10).Gap(6).Radius(8).Background(tk.AmberSoft).AlignItems(ui.Center).
					Children(func() {
						ui.Icon(c, icBell).FontSize(11).TextColor(tk.Amber)
						ui.Text(c, "需要你确认配置名").FontSize(10).TextColor(ui.Hex("#7A2E0E"))
						ui.Spacer(c)
						primaryButton(c, tk, "回答")
					})
			})
			windowFrame(c, tk, "acme/api", ui.Hex("#0EA5C6"), "2 需要我 · 0 执行中", false, func() {
				ui.Text(c, "为 /v2 接口补 OpenAPI 描述").FontSize(13).Bold().TextColor(tk.Text)
				ui.Row(c).Gap(6).Children(func() {
					chip(c, tk, "○ 等待评审", tk.Violet, tk.VioletSoft)
					chip(c, tk, "PR #57", tk.Accent, tk.AccentSoft)
				})
				transcriptLine(c, tk, "go test ./api/... · exit 0")
				transcriptLine(c, tk, "edit openapi/v2.yaml · +208 −0")
				transcriptLine(c, tk, "(等待你 review 后合并)")
				ui.Spacer(c)
				ui.Row(c).Padding(8, 10).Gap(6).Radius(8).Background(tk.SurfaceAlt).AlignItems(ui.Center).
					Children(func() {
						ui.Icon(c, icFile).FontSize(11).TextColor(tk.TextMuted)
						ui.Text(c, "交付物 2 项 · 截图 / diff").FontSize(10).TextColor(tk.TextMuted)
					})
			})
		})
		ui.Row(c).Height(78).Padding(0, 40).Gap(14).AlignItems(ui.Center).Children(func() {
			notePill(c, "共享：单进程 core · 单写者 Store · Effects 队列 · 托盘 / 通知")
			notePill(c, "隔离：每个窗口钉住一个工作区；切走不暂停；Mandate 不跨窗口")
			notePill(c, "排期：M2 切换器 + 管理页；⌘N 多窗口在 M5")
		})
	})
}

func windowFrame(c *ui.Context, tk Tokens, name string, color ui.Color, meta string, active bool, body func()) {
	ui.Column(c).Grow(1).Radius(12).Background(tk.Surface).Border(1, tk.Border).
		Shadow(0, 24, 60, 0, ui.RGBA(0, 0, 0, 0.35)).Children(func() {
		ui.Row(c).Height(36).Padding(0, 12).Gap(8).AlignItems(ui.Center).
			Background(tk.SurfaceAlt).Children(func() {
			dot(c, tk.Red, 5)
			dot(c, tk.Amber, 5)
			dot(c, tk.Green, 5)
			ui.Spacer(c)
			ui.Image(c, logo).Size(15, 15).Radius(4)
			ui.Text(c, name+" — ZeoKeel").FontSize(11).TextColor(tk.Text)
			ui.Spacer(c)
			if active {
				chip(c, tk, "主窗口", tk.Accent, tk.AccentSoft)
			}
		})
		ui.Row(c).Grow(1).AlignItems(ui.Stretch).Children(func() {
			ui.Column(c).Width(150).FillHeight().Background(tk.SurfaceAlt).Padding(8, 8).Gap(3).
				Children(func() {
					ui.Text(c, "工作台").FontSize(9).TextColor(tk.TextFaint).Padding(2, 6, 4, 6)
					for _, it := range []struct {
						icon *ui.SVG
						name string
						on   bool
					}{{icOverview, "总览", true}, {icSource, "Source", false}, {icTrigger, "Trigger", false}, {icRun, "Execution", false}} {
						row := it
						ui.Row(c).Height(22).Padding(0, 6).Gap(6).Radius(5).AlignItems(ui.Center).
							Background(condColor(row.on, tk.AccentSoft, ui.Transparent)).Children(func() {
							ui.Icon(c, row.icon).FontSize(10).TextColor(condColor(row.on, tk.Accent, tk.TextMuted))
							ui.Text(c, row.name).FontSize(9).TextColor(condColor(row.on, tk.Accent, tk.Text))
						})
					}
					ui.Spacer(c)
					ui.Row(c).Gap(5).AlignItems(ui.Center).Children(func() {
						dot(c, color, 5)
						ui.Text(c, meta).FontSize(8).TextColor(tk.TextFaint)
					})
				})
			ui.Column(c).Grow(1).Padding(12).Gap(8).Children(func() {
				body()
			})
		})
		ui.Row(c).Height(26).Padding(0, 12).Gap(6).AlignItems(ui.Center).
			Background(tk.SurfaceAlt).BorderWidth(1, 0, 0, 0).BorderColor(tk.BorderSoft).Children(func() {
			ui.Text(c, "● 运行中 · 1 executor · 队列 0").FontSize(8).TextColor(tk.TextFaint)
			ui.Spacer(c)
			ui.Text(c, "SQLite 12.4 MB").FontSize(8).TextColor(tk.TextFaint)
		})
	})
}

func transcriptLine(c *ui.Context, tk Tokens, s string) {
	ui.Row(c).Padding(5, 7).Radius(5).Background(tk.SurfaceAlt).Children(func() {
		ui.Text(c, s).FontSize(9).Font("monospace").TextColor(tk.TextMuted).SingleLine()
	})
}

func notePill(c *ui.Context, s string) {
	ui.Row(c).Padding(8, 11).Radius(8).Background(ui.RGBA(255, 255, 255, 0.10)).
		Border(1, ui.RGBA(255, 255, 255, 0.14)).Children(func() {
		ui.Text(c, s).FontSize(10).TextColor(ui.RGBA(255, 255, 255, 0.82))
	})
}
