package main

import "github.com/egoist/mygo/ui"

// ---- 11 · Multiple workspaces: switching is a UI scope change only ----

type workspace struct {
	name, path, remote, initial, color string
	needs, running, today              string
	current                            bool
}

var workspaces = []workspace{
	{"acme/webapp", "~/code/webapp", "github.com/acme/webapp", "W", "#2F5BFF", "3", "1", "42", true},
	{"acme/api", "~/code/api", "github.com/acme/api", "A", "#0EA5C6", "2", "0", "18", false},
	{"keel-cli", "~/code/keel-cli", "git.sr.ht/~chen/keel-cli", "K", "#6D4AFF", "0", "0", "6", false},
}

func workspacesScreen(c *ui.Context) {
	tk := tokens()
	overviewScene(c, func(c *ui.Context) {
		// scrim, then the switcher popover under the workspace chip
		ui.Box(c).Absolute().Top(0).Left(0).Right(0).Bottom(0).
			Background(ui.RGBA(15, 23, 42, 0.10))
		switcher(c, tk)
	})
}

func switcher(c *ui.Context, tk Tokens) {
	ui.Column(c).Absolute().Top(50).Left(140).Width(372).Padding(8).Gap(2).
		Radius(12).Background(tk.Surface).Border(1, tk.Border).
		Shadow(0, 22, 56, 0, ui.RGBA(15, 23, 42, 0.28)).Children(func() {
		ui.Row(c).Height(32).Padding(0, 9).Gap(8).Radius(7).Background(tk.SurfaceAlt).
			AlignItems(ui.Center).Children(func() {
			ui.Icon(c, icSearch).FontSize(13).TextColor(tk.TextFaint)
			ui.Text(c, "切换工作区…").FontSize(12).TextColor(tk.TextFaint)
			ui.Spacer(c)
			ui.Text(c, "⌘1-9").FontSize(10).TextColor(tk.TextFaint)
		})
		ui.Divider(c)
		// Aggregate across every workspace.
		ui.Row(c).Height(44).Padding(0, 9).Gap(9).Radius(7).AlignItems(ui.Center).Children(func() {
			ui.Box(c).Size(26, 26).Radius(7).Background(tk.SurfaceAlt).Border(1, tk.BorderSoft).Center().
				Children(func() { ui.Icon(c, icOverview).FontSize(14).TextColor(tk.TextMuted) })
			ui.Column(c).Grow(1).Gap(1).Children(func() {
				ui.Text(c, "全部工作区").FontSize(12).Bold().TextColor(tk.Text)
				ui.Text(c, "聚合概览 · 3 个仓库").FontSize(10).TextColor(tk.TextFaint)
			})
			chip(c, tk, "5 需要我", tk.Amber, tk.AmberSoft)
			chip(c, tk, "1 执行中", tk.Green, tk.GreenSoft)
		})
		ui.Divider(c)
		for _, w := range workspaces {
			ws := w
			ui.Row(c).Height(46).Padding(0, 9).Gap(9).Radius(7).AlignItems(ui.Center).
				Background(condColor(ws.current, tk.AccentSoft, ui.Transparent)).Children(func() {
				ui.Box(c).Size(26, 26).Radius(7).Background(ui.Hex(ws.color)).Center().
					Children(func() { ui.Text(c, ws.initial).FontSize(11).Bold().TextColor(ui.Hex("#FFFFFF")) })
				ui.Column(c).Grow(1).Gap(1).Children(func() {
					ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
						ui.Text(c, ws.name).FontSize(12).Bold().TextColor(tk.Text)
						if ws.current {
							chip(c, tk, "当前", tk.Accent, tk.Surface)
						}
					})
					ui.Text(c, ws.remote+" · "+ws.path).FontSize(10).TextColor(tk.TextFaint)
				})
				if ws.needs != "0" {
					chip(c, tk, ws.needs+" 需要我", tk.Amber, tk.AmberSoft)
				}
				if ws.running != "0" {
					chip(c, tk, ws.running+" 执行中", tk.Green, tk.GreenSoft)
				}
			})
		}
		ui.Divider(c)
		ui.Row(c).Height(34).Padding(0, 9).Gap(9).Radius(7).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, icSettings).FontSize(13).TextColor(tk.TextMuted)
			ui.Text(c, "管理工作区…").FontSize(12).TextColor(tk.Text)
			ui.Spacer(c)
		})
		ui.Row(c).Height(34).Padding(0, 9).Gap(9).Radius(7).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, icPlus).FontSize(13).TextColor(tk.Accent)
			ui.Text(c, "添加工作区…").FontSize(12).TextColor(tk.Accent)
			ui.Spacer(c)
			ui.Text(c, "⌘N").FontSize(10).TextColor(tk.TextFaint)
		})
	})
}

// ---- 12 · Workspace administration: roots, sources, policy, budget ----

func workspaceAdmin(c *ui.Context) {
	tk := tokens()
	window(c, tk, "", func() {
		content(c, func() {
			ui.Row(c).AlignItems(ui.Center).Children(func() {
				ui.Column(c).Grow(1).Gap(5).Children(func() {
					ui.Text(c, "工作区 / 管理").FontSize(11).TextColor(tk.TextFaint)
					ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
						ui.Text(c, "工作区").FontSize(22).Bold().TextColor(tk.Text)
						chip(c, tk, "3 个", tk.Accent, tk.AccentSoft)
						chip(c, tk, "每工作区一个 Engine", tk.Violet, tk.VioletSoft)
						chip(c, tk, "共享单写者 Store / Effects", tk.TextMuted, tk.SurfaceAlt)
					})
				})
				smallButton(c, tk, "从 Git 克隆…")
				primaryButton(c, tk, "添加工作区")
			})
			ui.Row(c).Grow(1).AlignItems(ui.Stretch).Gap(16).Children(func() {
				ui.Column(c).Width(430).Gap(12).Children(func() {
					for _, w := range workspaces {
						ws := w
						workspaceCard(c, tk, ws)
					}
					ui.Spacer(c)
					ui.Row(c).Padding(12).Gap(8).Radius(10).Background(tk.SurfaceAlt).
						Border(1, tk.BorderSoft).Children(func() {
						ui.Icon(c, icBell).FontSize(13).TextColor(tk.Amber)
						ui.Text(c, "需要我 5 件 · 来自 2 个工作区").FontSize(11).TextColor(tk.Amber)
					})
					crossNote(c, tk)
				})
				ui.Column(c).Grow(1).Gap(12).Children(func() {
					workspaceDetail(c, tk, workspaces[0])
				})
			})
		})
	})
}

func workspaceCard(c *ui.Context, tk Tokens, w workspace) {
	border := tk.Border
	bg := tk.Surface
	if w.current {
		border = tk.Accent
		bg = tk.Surface
	}
	ui.Column(c).Padding(14).Gap(9).Radius(12).Background(bg).Border(1.5, border).
		Shadow(0, 1, 2, 0, ui.RGBA(16, 24, 40, 0.04)).Children(func() {
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Box(c).Size(32, 32).Radius(8).Background(ui.Hex(w.color)).Center().
				Children(func() { ui.Text(c, w.initial).FontSize(13).Bold().TextColor(ui.Hex("#FFFFFF")) })
			ui.Column(c).Grow(1).Gap(1).Children(func() {
				ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
					ui.Text(c, w.name).FontSize(13).Bold().TextColor(tk.Text)
					if w.current {
						chip(c, tk, "当前", tk.Accent, tk.AccentSoft)
					}
				})
				ui.Text(c, w.path+"  ·  "+w.remote).FontSize(10).TextColor(tk.TextFaint)
			})
			ui.Icon(c, icChevR).FontSize(13).TextColor(tk.TextFaint)
		})
		ui.Row(c).Gap(6).Children(func() {
			chip(c, tk, w.needs+" 需要我", condColor(w.needs != "0", tk.Amber, tk.TextMuted),
				condColor(w.needs != "0", tk.AmberSoft, tk.SurfaceAlt))
			chip(c, tk, w.running+" 执行中", condColor(w.running != "0", tk.Green, tk.TextMuted),
				condColor(w.running != "0", tk.GreenSoft, tk.SurfaceAlt))
			ui.Spacer(c)
			ui.Text(c, "今日 "+w.today+" 次运行").FontSize(10).TextColor(tk.TextFaint)
		})
	})
}

func workspaceDetail(c *ui.Context, tk Tokens, w workspace) {
	card(c, tk, 16, func() {
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Box(c).Size(36, 36).Radius(9).Background(ui.Hex(w.color)).Center().
				Children(func() { ui.Text(c, w.initial).FontSize(14).Bold().TextColor(ui.Hex("#FFFFFF")) })
			ui.Column(c).Grow(1).Gap(1).Children(func() {
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Text(c, w.name).FontSize(16).Bold().TextColor(tk.Text)
					chip(c, tk, "运行中", tk.Green, tk.GreenSoft)
				})
				ui.Text(c, w.remote+" · "+w.path).FontSize(11).TextColor(tk.TextMuted)
			})
			smallButton(c, tk, "打开目录")
			smallButton(c, tk, "暂停")
		})
		ui.Divider(c)
		ui.Text(c, "根与身份").FontSize(12).Bold().TextColor(tk.Text)
		kv(c, tk, "本地目录", "~/code/webapp（工作目录按 Ticket 隔离）")
		kv(c, tk, "远端", "github.com/acme/webapp · 默认分支 main")
		kv(c, tk, "实例 ID", "ws_7f3a9c（创建时生成，持久化，稳定不变）")
		ui.Text(c, "移动或重命名仓库目录不会改变工作区 ID；remote 只用于「同一仓库不许重复添加」的校验").
			FontSize(10).TextColor(tk.TextFaint)
		ui.Divider(c)
		ui.Text(c, "绑定的 Source").FontSize(12).Bold().TextColor(tk.Text)
		bindRow(c, tk, icBranch, "GitHub · acme/webapp", "Issue / PR / CI · 游标 12s", true)
		bindRow(c, tk, icBell, "Feishu · 研发群", "事件 + topic 轮询 · 仅上下文", true)
		bindRow(c, tk, icClock, "Schedule ×2", "工作日 09:00 巡检 · 02:00 备份", true)
		ui.Divider(c)
		ui.Text(c, "Policy 与预算").FontSize(12).Bold().TextColor(tk.Text)
		kv(c, tk, "授权模式", "standard · 高风险操作逐次问人")
		kv(c, tk, "每 Ticket 预算", "$2.00 · 超出暂停并询问")
		kv(c, tk, "并发槽位", "2 个 executor（人发消息额外占 1 个）")
		ui.Divider(c)
		ui.Row(c).Children(func() {
			ui.Text(c, "SQLite 8.1 MB · 1,284 条事件 · 密钥在系统钥匙串（ws_7f3a9c）").
				FontSize(10).TextColor(tk.TextFaint)
		})
		ui.Text(c, "移除工作区会停止它的 Source 与运行中 Ticket；其它工作区不受影响").
			FontSize(10).TextColor(tk.TextFaint)
	})
}

func bindRow(c *ui.Context, tk Tokens, icon *ui.SVG, name, meta string, on bool) {
	ui.Row(c).Padding(7, 9).Gap(9).Radius(7).Background(tk.SurfaceAlt).AlignItems(ui.Center).
		Children(func() {
			ui.Icon(c, icon).FontSize(13).TextColor(tk.TextMuted)
			ui.Column(c).Grow(1).Gap(1).Children(func() {
				ui.Text(c, name).FontSize(11).TextColor(tk.Text)
				ui.Text(c, meta).FontSize(10).TextColor(tk.TextFaint)
			})
			chip(c, tk, "已绑定", tk.Green, tk.GreenSoft)
		})
}

func crossNote(c *ui.Context, tk Tokens) {
	card(c, tk, 14, func() {
		ui.Text(c, "跨工作区的硬规则").FontSize(12).Bold().TextColor(tk.Text)
		ui.Text(c, "· 领域 ID 第一段是 workspaceID（A/#12 与 B/#12 不会撞成同一 Thread）\n· 领域 API 不接收 workspaceID 参数：workspace 是构造期边界，Engine 构造时绑定\n· Runtime 持有每工作区一个 Engine，共享单写者 Store 与 Effects\n· Mandate 不跨工作区；在 A 授权的工具，B 里仍需重新授权\n· 评论 / PR 只回到它来源的工作区，永不串源\n· 删除工作区不动其它仓；移动/重命名目录不改工作区 ID\n· 多窗口（M5，非首版）：⌘N 新窗口钉住一个工作区，共用 runtime 与托盘").
			FontSize(10).TextColor(tk.TextMuted)
	})
}
