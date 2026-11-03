package main

import "github.com/egoist/mygo/ui"

// ---- 08 · Tray and notifications: resident, but quiet ----

func tray(c *ui.Context) {
	tk := tokens()
	ui.Box(c).Fill().Gradient(ui.Hex("#0B1B33"), ui.Hex("#16345C"), 35).Children(func() {
		// A tiny system tray strip, top right.
		ui.Row(c).Height(30).Padding(0, 14).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Spacer(c)
			ui.Text(c, "工作台 · webapp").FontSize(10).TextColor(ui.RGBA(255, 255, 255, 0.55))
			ui.Icon(c, icSearch).FontSize(13).TextColor(ui.RGBA(255, 255, 255, 0.75))
			ui.Icon(c, icBell).FontSize(13).TextColor(ui.RGBA(255, 255, 255, 0.75))
			ui.Text(c, "12:41").FontSize(11).TextColor(ui.RGBA(255, 255, 255, 0.75))
			trayIcon(c, tk, false)
		})
		// Tray popover.
		ui.Column(c).Absolute().Top(40).Right(20).Width(310).Padding(8).Gap(2).
			Radius(14).Background(tk.Surface).Border(1, tk.Border).
			Shadow(0, 24, 60, 0, ui.RGBA(0, 0, 0, 0.35)).Children(func() {
			ui.Row(c).Padding(8, 10).Gap(9).AlignItems(ui.Center).Children(func() {
				ui.Image(c, logo).Size(22, 22).Radius(6)
				ui.Text(c, "ZeoKeel").FontSize(13).Bold().TextColor(tk.Text)
				ui.Spacer(c)
				ui.Text(c, "v0.1.0").FontSize(10).TextColor(tk.TextFaint)
			})
			trayRow(c, tk, icBell, "需要我处理", "3")
			ui.Divider(c)
			trayRow(c, tk, icOverview, "打开 ZeoKeel", "⌘⇧Z")
			trayRow(c, tk, icPause, "暂停运行时", "⌘.")
			trayRow(c, tk, icClock, "重启运行时", "")
			trayRow(c, tk, icSettings, "设置…", "⌘,")
			ui.Divider(c)
			trayRowColor(c, tk, icClose, "退出", "", tk.Red)
			ui.Row(c).Padding(8, 10).Gap(6).AlignItems(ui.Center).Children(func() {
				dot(c, tk.Green, 6)
				ui.Text(c, "运行中 · 1 executor · 队列 0").FontSize(10).TextColor(tk.TextMuted)
			})
		})
		// Notification: the only interrupt the product allows by default.
		ui.Column(c).Absolute().Top(430).Right(20).Width(360).Padding(14).Gap(7).
			Radius(14).Background(tk.Surface).Border(1, tk.Border).
			Shadow(0, 20, 50, 0, ui.RGBA(0, 0, 0, 0.32)).Children(func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Image(c, logo).Size(18, 18).Radius(5)
				ui.Text(c, "ZeoKeel").FontSize(11).Bold().TextColor(tk.Text)
				ui.Text(c, "现在").FontSize(10).TextColor(tk.TextFaint)
				ui.Spacer(c)
				ui.Icon(c, icClose).FontSize(11).TextColor(tk.TextFaint)
			})
			ui.Text(c, "Ticket #42 需要你确认").FontSize(13).Bold().TextColor(tk.Text)
			ui.Text(c, "配置名用 session_timeout 还是 session.timeout？").FontSize(11).TextColor(tk.TextMuted)
			ui.Row(c).Gap(10).Children(func() {
				ui.Text(c, "回答").FontSize(11).Bold().TextColor(tk.Accent)
				ui.Text(c, "稍后").FontSize(11).TextColor(tk.TextMuted)
			})
		})
		// Dock / task bar at the bottom center.
		ui.Row(c).Absolute().Bottom(22).Left(0).Right(0).Justify(ui.Center).Children(func() {
			ui.Row(c).Padding(8, 10).Gap(10).Radius(16).Background(ui.RGBA(255, 255, 255, 0.14)).
				Border(1, ui.RGBA(255, 255, 255, 0.18)).AlignItems(ui.Center).Children(func() {
				for _, col := range []ui.Color{ui.Hex("#30B0C7"), ui.Hex("#8E8E93"), ui.Hex("#FF9F0A"), ui.Hex("#30D158")} {
					ui.Box(c).Size(44, 44).Radius(12).Background(col)
				}
				// The app itself, with the "needs me" badge.
				ui.Box(c).Size(44, 44).Radius(12).Background(tk.Surface).Center().Children(func() {
					ui.Image(c, logo).Size(34, 34).Radius(9)
					ui.Box(c).Size(18, 18).Radius(9).Background(tk.Red).Absolute().Top(-5).Right(-5).
						Center().Children(func() { ui.Text(c, "3").FontSize(10).Bold().TextColor(ui.Hex("#FFFFFF")) })
				})
			})
		})
		// Annotations, as a designer's notes over the screenshot.
		note(c, 40, 96, 300, "关窗 = 隐藏：最后一个窗口关闭时 app 不退出，进程继续跑 Ticket")
		note(c, 40, 200, 330, "需要人时才打断：结构化提问 → 原生对话框 + 系统通知 + 徽标")
		note(c, 40, 304, 300, "托盘菜单就是运行时遥控器：暂停 / 重启 / 设置 / 退出")
		note(c, 40, 720, 380, "单二进制（Go + mygo，零 cgo）；空闲内存 22–37 MB，不是 Electron")
	})
}

func trayIcon(c *ui.Context, tk Tokens, active bool) {
	bg := ui.RGBA(255, 255, 255, 0.10)
	if active {
		bg = tk.Surface
	}
	ui.Box(c).Size(26, 26).Radius(8).Background(bg).Center().Children(func() {
		ui.Image(c, logo).Size(20, 20).Radius(5)
	})
}

func trayRow(c *ui.Context, tk Tokens, icon *ui.SVG, label, shortcut string) {
	trayRowColor(c, tk, icon, label, shortcut, tk.Text)
}

func trayRowColor(c *ui.Context, tk Tokens, icon *ui.SVG, label, shortcut string, color ui.Color) {
	ui.Row(c).Height(32).Padding(0, 10).Gap(9).Radius(7).AlignItems(ui.Center).Children(func() {
		ui.Icon(c, icon).FontSize(13).TextColor(color)
		ui.Text(c, label).FontSize(12).TextColor(color)
		ui.Spacer(c)
		if shortcut != "" {
			ui.Text(c, shortcut).FontSize(10).TextColor(tk.TextFaint)
		}
	})
}

func note(c *ui.Context, x, y, w float32, text string) {
	ui.Row(c).Absolute().Left(x).Top(y).Width(w).Padding(10, 12).Radius(9).
		Background(ui.RGBA(255, 255, 255, 0.92)).Border(1, ui.RGBA(255, 255, 255, 0.5)).
		Shadow(0, 8, 24, 0, ui.RGBA(0, 0, 0, 0.18)).Children(func() {
		ui.Text(c, text).FontSize(11).TextColor(ui.Hex("#101828"))
	})
}

// ---- 00 · One Trigger's journey (the prototype's flow notes) ----

func flow(c *ui.Context) {
	tk := tokens()
	ui.Column(c).Fill().Background(tk.Bg).Padding(34, 40).Gap(16).Children(func() {
		ui.Row(c).AlignItems(ui.End).Children(func() {
			ui.Column(c).Grow(1).Gap(5).Children(func() {
				ui.Text(c, "一次 Trigger 的旅程").FontSize(26).Bold().TextColor(tk.Text)
				ui.Text(c, "领域是主人，Agent 运行时是仆人；确定性状态机 + 最小面积的 LLM 判断").
					FontSize(12).TextColor(tk.TextMuted)
			})
			ui.Image(c, logo).Size(40, 40).Radius(10)
		})
		ui.Row(c).Height(258).AlignItems(ui.Stretch).Children(func() {
			stage(c, tk, "01", "事件进入", tk.Accent, tk.AccentSoft, []string{
				"GitHub / Feishu / Schedule",
				"Source.Receive → 原子推进游标",
				"Trigger/Ticket 内容派生 ID，禁 UUID",
				"同一事件重复投递只落一条",
				"外部文本只是数据",
			}, "确定性")
			arrow(c, tk)
			stage(c, tk, "02", "领域判定", tk.TextMuted, tk.SurfaceAlt, []string{
				"Filter → Eval → Decision",
				"命中规则写出可检查的 reason",
				"Ticket 阶段 = 查表函数",
				"admission table + Mandate",
				"被拒绝的概念清单",
			}, "确定性")
			arrow(c, tk)
			stage(c, tk, "03", "Agent 执行", tk.Violet, tk.VioletSoft, []string{
				"eino adk ReAct 循环",
				"工具白名单 + 工作区隔离",
				"预算 / 超时 / 重复检测",
				"每步落 session_event",
				"子智能体复用同一 runtime",
			}, "LLM 判断")
			arrow(c, tk)
			stage(c, tk, "04", "需要人时", tk.Amber, tk.AmberSoft, []string{
				"ask_user_question → 原生对话框",
				"高风险工具 → 授权 gate",
				"托盘徽标 + 系统通知",
				"回答 = 结构化事件",
				"goal 首阶段只 observe",
			}, "人的决定")
			arrow(c, tk)
			stage(c, tk, "05", "交付与回写", tk.Green, tk.GreenSoft, []string{
				"present → 交付物卡片",
				"PR / 评论 / 文档",
				"Outbox 幂等发送 + 回执",
				"follows 绑定 PR 生命周期",
				"归档：完成后合并过期数据",
			}, "确定性")
		})
		ui.Row(c).Height(196).AlignItems(ui.Stretch).Gap(14).Children(func() {
			flowCard(c, tk, "必须保住的不变量", []string{
				"状态是推导的，不是存储的（Ticket 无 Stage 字段）",
				"业务实体幂等来自内容派生 ID（Workspace 用稳定随机 ID）",
				"人永远不被机器挤掉：人的消息额外占一个 slot",
				"外部文本永不改变规则（防提示注入）",
				"prepare 失败不自动重试；权限写在代码里",
				"面板诚实状态：计数必须等于列表长度",
			})
			flowCard(c, tk, "授权四层（能力与语境分离）", []string{
				"判断：Policy 规则决定「要不要人」",
				"表格：admission table 记录每次放行",
				"凭据：Mandate 绑定 Ticket / 工具 / 期限",
				"复核：revalidate 用存下的 meta 再算一次",
				"订阅扇出永不授权；消息只是数据",
				"默认 observe：先只记录、不行动",
			})
			flowCard(c, tk, "投递三段式", []string{
				"delivered：请求已发出，不代表被读到",
				"read：对方读过，用于归属与去重",
				"turnEnded：本回合结束，followupOwners 结算",
				"Outbox 先落库再发，回执永久保留",
				"UI 永远显示诚实状态：计数 = 行数",
				"通知只在「需要人」时发出",
			})
			flowCard(c, tk, "崩溃恢复（第一天就做）", []string{
				"effect 已提交未执行 → 重启扫描 due 重跑",
				"工具调用中 → 未配对 tool_call 标记 interrupted",
				"出站已发未记 → Outbox 幂等挡掉重复发送",
				"工作中重启 → Assignment 以 hostRestarted 结束并新建",
				"窗口关闭 → 不影响 core（UI 与 runtime 解耦）",
				"窗口崩溃 → 重启后从快照恢复面板",
			})
		})
		// Milestones: the vertical slice first.
		ui.Column(c).Grow(1).Gap(10).Children(func() {
			ui.Row(c).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "推荐路线：先用 6 周做垂直切片，再全量铺开").FontSize(13).Bold().TextColor(tk.Text)
				ui.Spacer(c)
				ui.Text(c, "UI 不是风险点；Agent Runtime 是").FontSize(11).TextColor(tk.TextFaint)
			})
			ui.Row(c).Grow(1).AlignItems(ui.Stretch).Gap(10).Children(func() {
				milestone(c, tk, "M0", "地基", "schema v1 · 领域类型 · Source 接口", true)
				milestone(c, tk, "M1", "Agent Runtime", "eino adk · 工具白名单 · 会话落库 · 追问/交付", true)
				milestone(c, tk, "M2", "授权与端到端", "Gate/Mandate · GitHub Issue→PR→回评 Eval", true)
				milestone(c, tk, "M4", "可靠性", "崩溃恢复 · 子智能体 · goal · schedule", false)
				milestone(c, tk, "M5", "面板完整", "Source/Eval/Settings · ⌘K · 托盘与通知", false)
				milestone(c, tk, "M6", "扩展与发布", "workflow · skills · MCP · 打包分发", false)
			})
		})
		ui.Row(c).AlignItems(ui.Center).Gap(16).Children(func() {
			legend(c, tk, tk.Accent, "事件源")
			legend(c, tk, tk.TextMuted, "确定性领域")
			legend(c, tk, tk.Violet, "LLM / Agent")
			legend(c, tk, tk.Amber, "人的注意力")
			legend(c, tk, tk.Green, "出站与交付")
			ui.Spacer(c)
			ui.Text(c, "目标：无人值守长跑，但每一步都能被审计").FontSize(11).TextColor(tk.TextFaint)
		})
	})
}

func milestone(c *ui.Context, tk Tokens, id, title, meta string, now bool) {
	border := tk.Border
	bg := tk.Surface
	if now {
		border = tk.Accent
		bg = tk.AccentSoft
	}
	ui.Column(c).Grow(1).Padding(12).Gap(6).Radius(10).Background(bg).Border(1, border).Children(func() {
		ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
			ui.Text(c, id).FontSize(11).Bold().Font("monospace").TextColor(condColor(now, tk.Accent, tk.TextMuted))
			ui.Text(c, title).FontSize(12).Bold().TextColor(tk.Text)
		})
		ui.Text(c, meta).FontSize(10).TextColor(tk.TextMuted)
		ui.Spacer(c)
		if now {
			chip(c, tk, "切片范围", tk.Accent, tk.Surface)
		}
	})
}

func flowCard(c *ui.Context, tk Tokens, title string, lines []string) {
	ui.Column(c).Grow(1).Padding(14).Gap(7).Radius(12).Background(tk.Surface).
		Border(1, tk.Border).Children(func() {
		ui.Text(c, title).FontSize(13).Bold().TextColor(tk.Text)
		for _, l := range lines {
			ui.Row(c).Gap(6).AlignItems(ui.Start).Children(func() {
				dot(c, tk.TextFaint, 5)
				ui.Text(c, l).FontSize(10).TextColor(tk.TextMuted)
			})
		}
	})
}

func stage(c *ui.Context, tk Tokens, num, title string, fg, bg ui.Color, rows []string, lane string) {
	ui.Column(c).Grow(1).Radius(12).Background(tk.Surface).Border(1, tk.Border).
		Shadow(0, 1, 2, 0, ui.RGBA(16, 24, 40, 0.04)).Children(func() {
		ui.Row(c).Height(6).Radius(12).Background(fg)
		ui.Column(c).Padding(14).Gap(9).Children(func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, num).FontSize(11).Bold().Font("monospace").TextColor(fg)
				ui.Text(c, title).FontSize(14).Bold().TextColor(tk.Text)
			})
			for _, r := range rows {
				ui.Row(c).Gap(6).AlignItems(ui.Start).Children(func() {
					dot(c, fg, 5)
					ui.Text(c, r).FontSize(10).TextColor(tk.TextMuted)
				})
			}
			ui.Spacer(c)
			chip(c, tk, lane, fg, bg)
		})
	})
}

func arrow(c *ui.Context, tk Tokens) {
	ui.Row(c).Width(30).Center().Children(func() {
		ui.Icon(c, icArrowR).FontSize(16).TextColor(tk.TextFaint)
	})
}

func legend(c *ui.Context, tk Tokens, color ui.Color, label string) {
	ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
		dot(c, color, 7)
		ui.Text(c, label).FontSize(10).TextColor(tk.TextMuted)
	})
}
