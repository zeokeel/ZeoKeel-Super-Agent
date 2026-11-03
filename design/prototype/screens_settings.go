package main

import "github.com/egoist/mygo/ui"

// ---- 07 · Settings: one save bar per block, defaults first ----

func settings(c *ui.Context) {
	tk := tokens()
	window(c, tk, "settings", func() {
		content(c, func() {
			ui.Row(c).AlignItems(ui.Center).Children(func() {
				heading(c, tk, "设置", "每块只有一个保存条；能从默认值来的，就不做成设置项")
				ui.Spacer(c)
				textChip(c, tk, "来自 mygo.json")
				textChip(c, tk, "本地 SQLite")
			})
			ui.Row(c).Grow(1).AlignItems(ui.Stretch).Gap(16).Children(func() {
				settingsNav(c, tk)
				ui.Column(c).Grow(1).Gap(10).Children(func() {
					ui.Scroll(c).Grow(1).Gap(14).Children(func() {
						providerCard(c, tk)
						mcpCard(c, tk)
						skillsCard(c, tk)
					})
					saveBar(c, tk)
				})
			})
		})
	})
}

func settingsNav(c *ui.Context, tk Tokens) {
	items := []struct {
		label string
		icon  *ui.SVG
		on    bool
	}{
		{"模型与 Provider", icSpark, true},
		{"Source 与轮询", icSource, false},
		{"Eval 与 Policy", icEval, false},
		{"Execution 与沙箱", icRun, false},
		{"提示词", icFile, false},
		{"部署与更新", icTerminal, false},
		{"外观", icSettings, false},
	}
	ui.Column(c).Width(220).Padding(10).Gap(2).Background(tk.Surface).Radius(tk.Radius).
		Border(1, tk.Border).Children(func() {
		for _, it := range items {
			item := it
			ui.Row(c).Height(36).Padding(0, 10).Gap(9).Radius(7).AlignItems(ui.Center).
				Background(condColor(item.on, tk.AccentSoft, ui.Transparent)).Children(func() {
				ui.Icon(c, item.icon).FontSize(14).TextColor(condColor(item.on, tk.Accent, tk.TextMuted))
				ui.Text(c, item.label).FontSize(12).TextColor(condColor(item.on, tk.Accent, tk.Text))
			})
		}
	})
}

func providerCard(c *ui.Context, tk Tokens) {
	card(c, tk, 16, func() {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Text(c, "模型与 Provider").FontSize(14).Bold().TextColor(tk.Text)
			chip(c, tk, "OpenAI 兼容", tk.Accent, tk.AccentSoft)
			ui.Spacer(c)
			dot(c, tk.Green, 6)
			ui.Text(c, "已连接 · 180ms").FontSize(10).TextColor(tk.TextMuted)
			smallButton(c, tk, "测试连接")
		})
		field(c, tk, "Base URL", "https://api.deepseek.com/v1", false)
		ui.Row(c).Gap(12).Children(func() {
			ui.Column(c).Grow(1).Gap(4).Children(func() {
				ui.Text(c, "模型").FontSize(11).TextColor(tk.TextMuted)
				ui.Row(c).Height(32).Padding(0, 10).Radius(7).Background(tk.SurfaceAlt).
					Border(1, tk.BorderSoft).AlignItems(ui.Center).Children(func() {
					ui.Text(c, "deepseek-chat").FontSize(12).Font("monospace").TextColor(tk.Text)
					ui.Spacer(c)
					ui.Icon(c, icChevD).FontSize(12).TextColor(tk.TextFaint)
				})
			})
			ui.Column(c).Grow(1).Gap(4).Children(func() {
				ui.Text(c, "每 Ticket 预算上限").FontSize(11).TextColor(tk.TextMuted)
				ui.Row(c).Height(32).Padding(0, 10).Radius(7).Background(tk.SurfaceAlt).
					Border(1, tk.BorderSoft).AlignItems(ui.Center).Children(func() {
					ui.Text(c, "$ 2.00").FontSize(12).Font("monospace").TextColor(tk.Text)
					ui.Spacer(c)
					ui.Text(c, "超出即暂停并问人").FontSize(10).TextColor(tk.TextFaint)
				})
			})
		})
		ui.Column(c).Gap(4).Children(func() {
			ui.Text(c, "API Key").FontSize(11).TextColor(tk.TextMuted)
			ui.Row(c).Height(32).Padding(0, 10).Gap(8).Radius(7).Background(tk.SurfaceAlt).
				Border(1, tk.BorderSoft).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, icShield).FontSize(13).TextColor(tk.Green)
				ui.Text(c, "sk-••••••••••••7f3a").FontSize(12).Font("monospace").TextColor(tk.Text)
				ui.Spacer(c)
				ui.Text(c, "存于系统钥匙串").FontSize(10).TextColor(tk.TextFaint)
			})
		})
	})
}

func condStr(b bool, a, c string) string {
	if b {
		return a
	}
	return c
}

func field(c *ui.Context, tk Tokens, label, value string, masked bool) {
	ui.Column(c).Gap(4).Children(func() {
		ui.Text(c, label).FontSize(11).TextColor(tk.TextMuted)
		ui.Row(c).Height(32).Padding(0, 10).Radius(7).Background(tk.SurfaceAlt).
			Border(1, tk.BorderSoft).AlignItems(ui.Center).Children(func() {
			ui.Text(c, value).FontSize(12).Font("monospace").TextColor(tk.Text)
		})
	})
}

func mcpCard(c *ui.Context, tk Tokens) {
	servers := []struct {
		name, transport, tools string
		ok                     bool
	}{
		{"filesystem", "stdio · ~/.zeokeel/mcp/fs", "12 tools", true},
		{"github", "http · https://mcp.github.com", "28 tools", true},
		{"notion", "http · 未配置 token", "—", false},
	}
	card(c, tk, 16, func() {
		ui.Row(c).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "MCP 服务器").FontSize(14).Bold().TextColor(tk.Text)
			ui.Spacer(c)
			ui.Text(c, "+ 添加服务器").FontSize(11).TextColor(tk.Accent)
		})
		for _, s := range servers {
			srv := s
			color := tk.Green
			if !srv.ok {
				color = tk.Amber
			}
			ui.Row(c).Height(32).Padding(0, 9).Gap(9).Radius(7).Background(tk.SurfaceAlt).
				AlignItems(ui.Center).Children(func() {
				dot(c, color, 7)
				ui.Text(c, srv.name).FontSize(12).Font("monospace").TextColor(tk.Text)
				ui.Text(c, srv.transport).FontSize(10).TextColor(tk.TextFaint)
				ui.Spacer(c)
				chip(c, tk, srv.tools, tk.TextMuted, tk.Surface)
				chip(c, tk, condStr(srv.ok, "已连接", "未连接"), condColor(srv.ok, tk.Green, tk.Amber),
					condColor(srv.ok, tk.GreenSoft, tk.AmberSoft))
			})
		}
	})
}

func skillsCard(c *ui.Context, tk Tokens) {
	card(c, tk, 16, func() {
		ui.Row(c).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "Skills 与自动化").FontSize(14).Bold().TextColor(tk.Text)
			ui.Spacer(c)
			ui.Text(c, "6 个 skill 已发现").FontSize(10).TextColor(tk.TextFaint)
		})
		ui.Row(c).Height(34).AlignItems(ui.Center).Gap(9).Children(func() {
			ui.Icon(c, icFolder).FontSize(14).TextColor(tk.TextMuted)
			ui.Text(c, "~/.zeokeel/skills").FontSize(11).Font("monospace").TextColor(tk.Text)
			ui.Spacer(c)
			smallButton(c, tk, "浏览")
		})
		ui.Divider(c)
		ui.Row(c).Height(34).AlignItems(ui.Center).Gap(9).Children(func() {
			ui.Icon(c, icClock).FontSize(14).TextColor(tk.TextMuted)
			ui.Text(c, "每工作日 09:00 · CI 巡检").FontSize(11).TextColor(tk.Text)
			ui.Spacer(c)
			chip(c, tk, "已启用", tk.Green, tk.GreenSoft)
			chip(c, tk, "最近 2m 12s", tk.TextMuted, tk.SurfaceAlt)
		})
		ui.Divider(c)
		ui.Row(c).Height(30).AlignItems(ui.Center).Gap(9).Children(func() {
			ui.Icon(c, icSpark).FontSize(13).TextColor(tk.TextMuted)
			ui.Text(c, "goal 自动继续：上限 20 轮 · 首阶段只 observe").FontSize(10).TextColor(tk.TextFaint)
		})
	})
}

func saveBar(c *ui.Context, tk Tokens) {
	ui.Row(c).Padding(11, 14).Gap(10).Radius(10).Background(tk.Surface).
		Border(1, tk.Border).AlignItems(ui.Center).Children(func() {
		dot(c, tk.Amber, 7)
		ui.Text(c, "2 处未保存更改").FontSize(12).TextColor(tk.Text)
		ui.Text(c, "· 保存后写入 mygo.json 并热更新").FontSize(10).TextColor(tk.TextFaint)
		ui.Spacer(c)
		ui.Text(c, "查看保存历史（diff）").FontSize(11).TextColor(tk.Accent)
		primaryButton(c, tk, "保存")
	})
}
