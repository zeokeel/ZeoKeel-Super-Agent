package main

import "github.com/egoist/mygo/ui"

// ---- 03 · Attention: ask_user_question becomes a native dialog ----

func attention(c *ui.Context) {
	tk := tokens()
	executionScene(c, func(c *ui.Context) {
		dimOverlay(c, func() { attentionDialog(c, tk) })
	})
}

func attentionDialog(c *ui.Context, tk Tokens) {
	ui.Column(c).Width(640).Padding(24).Gap(16).Radius(16).Background(tk.Surface).
		Border(1, tk.Border).Shadow(0, 28, 70, 0, ui.RGBA(15, 23, 42, 0.28)).
		Children(func() {
			ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
				ui.Box(c).Size(38, 38).Radius(10).Background(tk.VioletSoft).Center().
					Children(func() { ui.Icon(c, icSpark).FontSize(19).TextColor(tk.Violet) })
				ui.Column(c).Grow(1).Gap(2).Children(func() {
					ui.Text(c, "需要你决定").FontSize(16).Bold().TextColor(tk.Text)
					ui.Text(c, "Ticket #42 · ask_user_question · 已等待 3 分钟").FontSize(11).TextColor(tk.TextMuted)
				})
				ui.Icon(c, icClose).FontSize(15).TextColor(tk.TextFaint)
			})
			ui.Text(c, "配置项的名字用哪种？这会影响后续所有 Ticket 的写法。").
				FontSize(14).TextColor(tk.Text)
			choice(c, tk, true, true, "session_timeout", "snake_case，与现有配置项一致", "推荐")
			choice(c, tk, false, false, "session.timeout", "点号命名，与前端配置一致", "")
			choice(c, tk, false, false, "两者都支持，默认 session_timeout", "兼容两种写法，代价是校验更复杂", "")
			ui.Column(c).Height(58).Padding(10, 12).Radius(8).Background(tk.SurfaceAlt).
				Border(1, tk.BorderSoft).Children(func() {
				ui.Text(c, "补充说明（可选）…").FontSize(12).TextColor(tk.TextFaint)
			})
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, icBell).FontSize(12).TextColor(tk.TextFaint)
				ui.Text(c, "回答会作为结构化事件写回 Ticket，Agent 继续执行").FontSize(10).TextColor(tk.TextMuted)
				ui.Spacer(c)
				smallButton(c, tk, "稍后处理")
				primaryButton(c, tk, "提交回答")
			})
		})
}

func choice(c *ui.Context, tk Tokens, selected, recommended bool, title, desc, badge string) {
	border := tk.BorderSoft
	bg := tk.Surface
	if selected {
		border = tk.Accent
		bg = tk.AccentSoft
	}
	ui.Row(c).Padding(11, 12).Gap(11).Radius(9).Background(bg).
		Border(1.5, border).AlignItems(ui.Center).Children(func() {
		ui.Box(c).Size(17, 17).Radius(9).Border(1.5, condColor(selected, tk.Accent, tk.Border)).
			Background(condColor(selected, tk.Accent, tk.Surface)).Center().Children(func() {
			if selected {
				ui.Box(c).Size(6, 6).Radius(3).Background(ui.Hex("#FFFFFF"))
			}
		})
		ui.Column(c).Grow(1).Gap(2).Children(func() {
			ui.Text(c, title).FontSize(13).TextColor(tk.Text)
			ui.Text(c, desc).FontSize(11).TextColor(tk.TextMuted)
		})
		if badge != "" {
			chip(c, tk, badge, tk.Accent, tk.Surface)
		}
	})
}

// ---- 04 · Authorization: the gate in front of a dangerous tool call ----

func approval(c *ui.Context) {
	tk := tokens()
	executionScene(c, func(c *ui.Context) {
		dimOverlay(c, func() { approvalDialog(c, tk) })
	})
}

func approvalDialog(c *ui.Context, tk Tokens) {
	ui.Column(c).Width(720).Padding(24).Gap(15).Radius(16).Background(tk.Surface).
		Border(1, tk.Border).Shadow(0, 28, 70, 0, ui.RGBA(15, 23, 42, 0.28)).
		Children(func() {
			ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
				ui.Box(c).Size(38, 38).Radius(10).Background(tk.RedSoft).Center().
					Children(func() { ui.Icon(c, icShield).FontSize(19).TextColor(tk.Red) })
				ui.Column(c).Grow(1).Gap(2).Children(func() {
					ui.Text(c, "授权请求").FontSize(16).Bold().TextColor(tk.Text)
					ui.Text(c, "Ticket #42 · 高风险操作 · Agent 暂停等待你决定").FontSize(11).TextColor(tk.TextMuted)
				})
				chip(c, tk, "gate", tk.Amber, tk.AmberSoft)
			})
			ui.Column(c).Padding(10, 12).Gap(4).Radius(9).Background(tk.CodeBg).Children(func() {
				ui.Text(c, "$ git push --force origin feat/session-timeout").
					FontSize(12).Font("monospace").TextColor(tk.CodeText)
				ui.Text(c, "# 会覆盖远端分支历史；不使用 --force-with-lease").
					FontSize(11).Font("monospace").TextColor(ui.Hex("#FCA5A5"))
			})
			ui.Column(c).Padding(12).Gap(7).Radius(9).Background(tk.SurfaceAlt).Children(func() {
				kv(c, tk, "作用域", "Ticket #42 · 工作区 ~/code/webapp · executor A")
				kv(c, tk, "风险", "覆盖远端分支历史，不可撤销")
				kv(c, tk, "命中规则", "policy: git push --force → 必须人工授权")
			})
			ui.Row(c).Gap(10).Children(func() {
				decision(c, tk, true, "允许一次", "只执行这一次，下次再问")
				decision(c, tk, false, "本 Ticket 内始终允许", "到期随 Ticket 结束")
				decision(c, tk, false, "拒绝", "Agent 收到拒绝原因后继续")
			})
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Box(c).Size(15, 15).Radius(4).Border(1.5, tk.Border).Background(tk.Surface)
				ui.Text(c, "记住到仓库 policy（需复核）").FontSize(11).TextColor(tk.TextMuted)
				chip(c, tk, "默认关", tk.TextFaint, tk.SurfaceAlt)
			})
			ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
				ui.Icon(c, icShield).FontSize(12).TextColor(tk.Amber)
				ui.Text(c, "默认 observe：自动行为上线前只记录、不执行").FontSize(10).TextColor(tk.Amber)
				ui.Spacer(c)
				ui.Row(c).Height(30).Padding(0, 13).Radius(7).Background(tk.RedSoft).
					AlignItems(ui.Center).Children(func() {
					ui.Text(c, "拒绝").FontSize(12).TextColor(tk.Red)
				})
				primaryButton(c, tk, "允许一次")
			})
		})
}

func decision(c *ui.Context, tk Tokens, selected bool, title, desc string) {
	border := tk.BorderSoft
	bg := tk.Surface
	if selected {
		border = tk.Accent
		bg = tk.AccentSoft
	}
	ui.Column(c).Grow(1).Padding(10).Gap(3).Radius(9).Background(bg).Border(1.5, border).
		Children(func() {
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				ui.Box(c).Size(13, 13).Radius(7).Border(1.5, condColor(selected, tk.Accent, tk.Border)).
					Background(condColor(selected, tk.Accent, tk.Surface)).Center().Children(func() {
					if selected {
						ui.Box(c).Size(5, 5).Radius(3).Background(ui.Hex("#FFFFFF"))
					}
				})
				ui.Text(c, title).FontSize(12).Bold().TextColor(tk.Text)
			})
			ui.Text(c, desc).FontSize(10).TextColor(tk.TextMuted)
		})
}
