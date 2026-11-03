package main

import "github.com/egoist/mygo/ui"

// ---- 07 · Eval: every decision carries a checkable reason ----

func evalScreen(c *ui.Context) {
	tk := tokens()
	window(c, tk, "eval", func() {
		content(c, func() {
			ui.Row(c).AlignItems(ui.Center).Children(func() {
				heading(c, tk, "Eval", "每个 Decision 都带可检查的 reason 与命中规则；不用自由文本猜「要不要人」")
				ui.Spacer(c)
				textChip(c, tk, "过去 24 小时")
				smallButton(c, tk, "导出 golden 用例")
			})
			ui.Row(c).Grow(1).AlignItems(ui.Stretch).Gap(16).Children(func() {
				ui.Column(c).Grow(1).Gap(12).Children(func() {
					decisionsCard(c, tk)
					ui.Row(c).Height(120).Gap(16).Children(func() {
						badgeStat(c, tk, "24", "评估的 Trigger", tk.Accent, tk.AccentSoft)
						badgeStat(c, tk, "9", "Decision", tk.Violet, tk.VioletSoft)
						badgeStat(c, tk, "5", "过滤 / 丢弃", tk.TextMuted, tk.SurfaceAlt)
						badgeStat(c, tk, "4", "建 Ticket", tk.Green, tk.GreenSoft)
					})
				})
				ui.Column(c).Width(400).Gap(12).Children(func() {
					rulesCard(c, tk)
					goldenCard(c, tk)
				})
			})
		})
	})
}

func decisionsCard(c *ui.Context, tk Tokens) {
	rows := []struct {
		at, trigger, decision, rule string
		fg, bg                      ui.Color
	}{
		{"12:04", "#42 新建 Issue", "新建 Ticket", "Issue 含「可配置 / 默认值」→ ticket", tk.Green, tk.GreenSoft},
		{"12:31", "#128 CI 失败", "归入 Thread #42", "PR 同分支 + 已订阅 → follows", tk.Violet, tk.VioletSoft},
		{"11:52", "#44 测试超时", "新建 Ticket", "失败日志含 timeout → ticket", tk.Green, tk.GreenSoft},
		{"11:20", "每日 CI 巡检", "过滤", "schedule 非工作日 → filtered", tk.TextMuted, tk.SurfaceAlt},
		{"10:18", "#45 迁移脚本", "新建 Ticket", "标题含 migration → ticket", tk.Green, tk.GreenSoft},
		{"09:42", "研发群：灰度?", "仅上下文", "群消息默认 context，不建 Ticket", tk.TextMuted, tk.SurfaceAlt},
	}
	ui.Column(c).Grow(1).Padding(14).Gap(8).Radius(12).Background(tk.Surface).
		Border(1, tk.Border).Shadow(0, 1, 2, 0, ui.RGBA(16, 24, 40, 0.04)).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "最近评估").FontSize(14).Bold().TextColor(tk.Text)
			ui.Spacer(c)
			ui.Text(c, "按时间倒序 · 点击行看完整 Decision").FontSize(10).TextColor(tk.TextFaint)
		})
		ui.Row(c).Height(26).Padding(0, 9).Gap(12).Radius(6).Background(tk.SurfaceAlt).Children(func() {
			ui.Text(c, "时间").FontSize(10).TextColor(tk.TextFaint).Width(46)
			ui.Text(c, "Trigger").FontSize(10).TextColor(tk.TextFaint).Width(150)
			ui.Text(c, "决定").FontSize(10).TextColor(tk.TextFaint).Width(120)
			ui.Text(c, "命中规则 / reason").FontSize(10).TextColor(tk.TextFaint).Grow(1)
		})
		for _, r := range rows {
			row := r
			ui.Row(c).Height(46).Padding(0, 9).Gap(12).Radius(7).AlignItems(ui.Center).Children(func() {
				ui.Text(c, row.at).FontSize(11).Font("monospace").TextColor(tk.TextFaint).Width(46)
				ui.Text(c, row.trigger).FontSize(11).TextColor(tk.Text).Width(150).SingleLine()
				ui.Box(c).Width(120).Children(func() { chip(c, tk, row.decision, row.fg, row.bg) })
				ui.Text(c, row.rule).FontSize(11).TextColor(tk.TextMuted).Grow(1).SingleLine()
			})
			ui.Divider(c)
		}
		ui.Spacer(c)
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Icon(c, icCheck).FontSize(12).TextColor(tk.Green)
			ui.Text(c, "138 条 golden 用例通过：阶段函数逐行快照，规则改动必须同时改用例").FontSize(10).TextColor(tk.TextMuted)
		})
	})
}

func badgeStat(c *ui.Context, tk Tokens, num, label string, fg, bg ui.Color) {
	ui.Column(c).Grow(1).Padding(12).Gap(2).Radius(10).Background(tk.Surface).Border(1, tk.Border).
		Children(func() {
			ui.Text(c, num).FontSize(22).Bold().TextColor(fg)
			ui.Text(c, label).FontSize(11).TextColor(tk.TextMuted)
			ui.Box(c).Height(3).Radius(2).Background(bg)
		})
}

func rulesCard(c *ui.Context, tk Tokens) {
	rules := []struct{ name, hit string }{
		{"Issue 含「可配置 / 默认值」→ ticket", "12 次"},
		{"PR 同分支 + 已订阅 → follows", "8 次"},
		{"失败日志含 timeout → ticket", "3 次"},
		{"schedule 非工作日 → filtered", "6 次"},
		{"群消息默认 context → 不建 Ticket", "24 次"},
	}
	cardG(c, tk, 14, func() {
		ui.Row(c).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "Policy 规则").FontSize(13).Bold().TextColor(tk.Text)
			ui.Spacer(c)
			chip(c, tk, "v7 · 只读", tk.TextMuted, tk.SurfaceAlt)
		})
		for _, r := range rules {
			rule := r
			ui.Row(c).Padding(8, 9).Gap(8).Radius(7).Background(tk.SurfaceAlt).AlignItems(ui.Center).
				Children(func() {
					ui.Icon(c, icShield).FontSize(11).TextColor(tk.TextMuted)
					ui.Text(c, rule.name).FontSize(10).TextColor(tk.Text).Grow(1)
					ui.Text(c, rule.hit).FontSize(10).TextColor(tk.TextFaint)
				})
		}
		ui.Spacer(c)
		ui.Text(c, "规则是代码 + 表格，不是提示词；改动走 diff 评审").FontSize(10).TextColor(tk.TextFaint)
	})
}

func goldenCard(c *ui.Context, tk Tokens) {
	card(c, tk, 14, func() {
		ui.Row(c).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "admission 与 golden").FontSize(13).Bold().TextColor(tk.Text)
			ui.Spacer(c)
			chip(c, tk, "全绿", tk.Green, tk.GreenSoft)
		})
		kv(c, tk, "阶段函数用例", "138 / 138")
		kv(c, tk, "Decision golden", "64 / 64")
		kv(c, tk, "revalidate 漂移", "0")
		kv(c, tk, "上次运行", "2 分钟前")
	})
}

// ---- 08 · Source: the event sources and their account facts ----

func sourceScreen(c *ui.Context) {
	tk := tokens()
	window(c, tk, "source", func() {
		content(c, func() {
			ui.Row(c).AlignItems(ui.Center).Children(func() {
				heading(c, tk, "Source", "三个事件源，各管各的账号与游标；过滤在这里，Agent 之前")
				ui.Spacer(c)
				textChip(c, tk, "3 个已启用")
				smallButton(c, tk, "重新认证")
			})
			ui.Row(c).Grow(1).AlignItems(ui.Stretch).Gap(16).Children(func() {
				ui.Column(c).Grow(1).Gap(14).Children(func() {
					sourceCard(c, tk, icBranch, "GitHub", "@acme · acme/webapp", true,
						[][2]string{
							{"订阅", "Issue、PR、CI（notifications + 订阅）"},
							{"24h 触发", "42 Triggers · 37 评估 · 5 丢弃"},
							{"游标", "已推进 · 上次同步 12 秒前"},
							{"凭据", "token 有效 · 40 天后过期"},
						})
					sourceCard(c, tk, icBell, "Feishu", "研发群 · 3 个群", true,
						[][2]string{
							{"订阅", "事件 + 历史 + topic 轮询 + 图片"},
							{"24h 触发", "8 Triggers · 全部作为上下文"},
							{"游标", "已推进 · 上次轮询 40 秒前"},
							{"凭据", "bot token 有效 · 权限 scopes 已核实"},
						})
					sourceCard(c, tk, icClock, "Schedule", "内部定时器", true,
						[][2]string{
							{"任务", "工作日 09:00 CI 巡检 · 每天 02:00 备份"},
							{"24h 触发", "2 Triggers · 1 过滤"},
							{"行为", "到点才唤醒；休眠唤醒后立即补跑一次"},
							{"开关", "随运行时启停"},
						})
				})
				ui.Column(c).Width(380).Gap(12).Children(func() {
					cardG(c, tk, 14, func() {
						ui.Row(c).AlignItems(ui.Center).Children(func() {
							ui.Text(c, "过滤与丢弃 · 24h").FontSize(13).Bold().TextColor(tk.Text)
							ui.Spacer(c)
							chip(c, tk, "23 条", tk.TextMuted, tk.SurfaceAlt)
						})
						filterRow(c, tk, "机器人账号 / 自身回复", "18")
						filterRow(c, tk, "非本仓库或非订阅 Thread", "9")
						filterRow(c, tk, "重复通知（内容 ID 相同）", "5")
						filterRow(c, tk, "已归档 Thread 的后续", "3")
						ui.Spacer(c)
						ui.Text(c, "被丢弃的 Trigger 仍保留在库里，可审计").
							FontSize(10).TextColor(tk.TextFaint)
					})
					card(c, tk, 14, func() {
						ui.Row(c).AlignItems(ui.Center).Children(func() {
							ui.Text(c, "账号事实").FontSize(13).Bold().TextColor(tk.Text)
							ui.Spacer(c)
							ui.Text(c, "事实，不是配置").FontSize(10).TextColor(tk.TextFaint)
						})
						kv(c, tk, "GitHub 剩余配额", "4,812 / 5,000")
						kv(c, tk, "Feishu 群权限", "读取消息 ✓ · 发评论 ✓")
						kv(c, tk, "token 到期", "40 天后")
						kv(c, tk, "轮询间隔", "12s（GitHub）· 40s（飞书）")
					})
					card(c, tk, 14, func() {
						ui.Text(c, "新增 Source 的 11 步（接口即验收标准）").FontSize(12).Bold().TextColor(tk.Text)
						ui.Text(c, "稳定 ID 先行 → receive 全成功才推进游标 → scope 只依赖 meta + 配置 → 中断轮次游标不动 → 过滤可审计…").
							FontSize(10).TextColor(tk.TextMuted)
					})
				})
			})
		})
	})
}

func sourceCard(c *ui.Context, tk Tokens, icon *ui.SVG, name, account string, on bool, rows [][2]string) {
	card(c, tk, 16, func() {
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Box(c).Size(32, 32).Radius(8).Background(tk.SurfaceAlt).Center().
				Children(func() { ui.Icon(c, icon).FontSize(17).TextColor(tk.Text) })
			ui.Column(c).Grow(1).Gap(1).Children(func() {
				ui.Text(c, name).FontSize(14).Bold().TextColor(tk.Text)
				ui.Text(c, account).FontSize(11).TextColor(tk.TextMuted)
			})
			chip(c, tk, "已启用", tk.Green, tk.GreenSoft)
			ui.Row(c).Width(34).Height(20).Radius(10).Background(tk.Accent).Padding(2).
				AlignItems(ui.Center).Children(func() {
				ui.Spacer(c)
				ui.Box(c).Size(16, 16).Radius(8).Background(tk.Surface)
			})
		})
		for _, r := range rows {
			row := r
			ui.Row(c).AlignItems(ui.Center).Children(func() {
				ui.Text(c, row[0]).FontSize(11).TextColor(tk.TextMuted).Width(92)
				ui.Text(c, row[1]).FontSize(11).TextColor(tk.Text).Grow(1)
			})
		}
	})
}

func filterRow(c *ui.Context, tk Tokens, reason, count string) {
	ui.Row(c).Padding(8, 9).Gap(8).Radius(7).Background(tk.SurfaceAlt).AlignItems(ui.Center).
		Children(func() {
			dot(c, tk.TextFaint, 6)
			ui.Text(c, reason).FontSize(11).TextColor(tk.Text).Grow(1)
			ui.Text(c, count).FontSize(11).TextColor(tk.TextFaint)
		})
}
