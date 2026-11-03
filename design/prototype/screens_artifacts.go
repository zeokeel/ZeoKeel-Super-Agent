package main

import "github.com/egoist/mygo/ui"

// ---- 05 · Artifacts: what the run produced, as first-class cards ----

func artifacts(c *ui.Context) {
	tk := tokens()
	window(c, tk, "execution", func() {
		content(c, func() {
			ui.Row(c).AlignItems(ui.Center).Children(func() {
				ui.Column(c).Grow(1).Gap(5).Children(func() {
					ui.Text(c, "Execution / Ticket #42").FontSize(11).TextColor(tk.TextFaint)
					ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
						ui.Text(c, "交付物").FontSize(22).Bold().TextColor(tk.Text)
						chip(c, tk, "4 项", tk.Accent, tk.AccentSoft)
						chip(c, tk, "本次运行", tk.TextMuted, tk.SurfaceAlt)
					})
				})
				smallButton(c, tk, "在目录中打开")
				primaryButton(c, tk, "全部打包下载")
			})
			ui.Row(c).Grow(1).AlignItems(ui.Stretch).Gap(16).Children(func() {
				ui.Column(c).Grow(1).Gap(16).Children(func() {
					ui.Row(c).Grow(1).AlignItems(ui.Stretch).Gap(16).Children(func() {
						artifactCard(c, tk, icBranch, tk.Accent, "PR #128", "支持 session_timeout 配置",
							"feat/session-timeout → main · @acme/webapp", "passed", "go test · 1.82s", func() {
								ui.Row(c).Gap(6).Children(func() {
									chip(c, tk, "+128", tk.Green, tk.GreenSoft)
									chip(c, tk, "−36", tk.Red, tk.RedSoft)
									chip(c, tk, "9 files", tk.TextMuted, tk.SurfaceAlt)
								})
								ui.Column(c).Gap(3).Children(func() {
									fileStat(c, tk, "internal/auth/session.go", "+12 −1")
									fileStat(c, tk, "internal/auth/session_test.go", "+64")
									fileStat(c, tk, "docs/configuration.md", "+52 −2")
								})
							})
						artifactCard(c, tk, icFile, tk.Violet, "report.md", "变更说明与回滚方案",
							"4.2 KB · 结构化 Markdown", "passed", "lint · 0.3s", func() {
								ui.Column(c).Gap(2).Children(func() {
									previewLine(c, tk, "## 变更说明")
									previewLine(c, tk, "把写死的 15 分钟超时改为 SessionTimeout 配置…")
									previewLine(c, tk, "默认 30 分钟，可在 mygo.json 覆盖。")
									previewLine(c, tk, "## 验证")
									previewLine(c, tk, "go test ./internal/auth/...  ·  1.82s")
									previewLine(c, tk, "## 回滚方案")
								})
							})
					})
					ui.Row(c).Grow(1).AlignItems(ui.Stretch).Gap(16).Children(func() {
						artifactCard(c, tk, icImage, tk.Cyan, "login-timeout.png", "运行截图 · 1440×900",
							"由 automation 截图 · 3 分钟前", "unknown", "", func() {
								ui.Box(c).Fill().Radius(7).Background(tk.SurfaceAlt).Border(1, tk.BorderSoft).
									Padding(8).Children(func() { miniScreenshot(c, tk) })
							})
						artifactCard(c, tk, icTerminal, tk.Amber, "coverage.html", "覆盖报告 · auth 模块",
							"总覆盖率 86.2% · 比上周 +2.4%", "failed", "阈值 90% 未达", func() {
								ui.Row(c).Height(6).Radius(3).Background(tk.BorderSoft).Children(func() {
									ui.Box(c).WidthPercent(86).Height(6).Radius(3).Background(tk.Green)
								})
								ui.Row(c).Children(func() {
									ui.Text(c, "auth 94%").FontSize(10).TextColor(tk.TextMuted)
									ui.Spacer(c)
									ui.Text(c, "session 88%").FontSize(10).TextColor(tk.TextMuted)
								})
							})
					})
				})
				ui.Column(c).Width(400).Gap(12).Children(func() {
					cardG(c, tk, 16, func() {
						ui.Row(c).AlignItems(ui.Center).Children(func() {
							ui.Text(c, "预览").FontSize(14).Bold().TextColor(tk.Text)
							ui.Text(c, " login-timeout.png").FontSize(11).Font("monospace").TextColor(tk.TextMuted)
							ui.Spacer(c)
							ui.Icon(c, icExternal).FontSize(13).TextColor(tk.TextMuted)
						})
						ui.Box(c).Grow(1).Radius(9).Background(tk.SurfaceAlt).Border(1, tk.BorderSoft).
							Padding(18).Children(func() { bigScreenshot(c, tk) })
						ui.Row(c).Gap(8).Children(func() {
							textChip(c, tk, "在目录中显示")
							textChip(c, tk, "用浏览器打开")
							textChip(c, tk, "复制路径")
						})
						ui.Divider(c)
						kv(c, tk, "来源", "automation.capture")
						kv(c, tk, "Ticket", "#42 修改登录超时配置")
						kv(c, tk, "生成于", "12:16 · 运行 12 分钟时")
						ui.Divider(c)
						reverseChain(c, tk)
					})
				})
			})
		})
	})
}

func fileStat(c *ui.Context, tk Tokens, path, stat string) {
	ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
		ui.Icon(c, icFile).FontSize(10).TextColor(tk.TextFaint)
		ui.Text(c, path).FontSize(10).Font("monospace").TextColor(tk.TextMuted)
		ui.Spacer(c)
		ui.Text(c, stat).FontSize(10).Font("monospace").TextColor(tk.TextFaint)
	})
}

func verifyBadge(c *ui.Context, tk Tokens, state, note string) {
	fg, bg, label := tk.TextMuted, tk.SurfaceAlt, "未验证"
	switch state {
	case "passed":
		fg, bg, label = tk.Green, tk.GreenSoft, "已通过"
	case "failed":
		fg, bg, label = tk.Red, tk.RedSoft, "失败"
	}
	text := label
	if note != "" {
		text += " · " + note
	}
	ui.Row(c).Height(19).Padding(0, 7).Gap(4).Radius(5).Background(bg).AlignItems(ui.Center).
		Children(func() {
			ui.Box(c).Size(5, 5).Radius(3).Background(fg)
			ui.Text(c, text).FontSize(9).TextColor(fg)
		})
}

func reverseChain(c *ui.Context, tk Tokens) {
	seg := func(s string, accent bool) {
		fg := tk.TextMuted
		if accent {
			fg = tk.Accent
		}
		ui.Text(c, s).FontSize(9).TextColor(fg)
	}
	ui.Row(c).Gap(4).AlignItems(ui.Center).Wrap().Children(func() {
		seg("产物", false)
		ui.Icon(c, icChevR).FontSize(8).TextColor(tk.TextFaint)
		seg("Ticket #42", true)
		ui.Icon(c, icChevR).FontSize(8).TextColor(tk.TextFaint)
		seg("授权：explicit@v3", true)
		ui.Icon(c, icChevR).FontSize(8).TextColor(tk.TextFaint)
		seg("原始请求：钉钉 · 张三 · 12:04", true)
	})
	ui.Text(c, "反查链可点：三跳之内回到人").FontSize(9).TextColor(tk.TextFaint)
}

func previewLine(c *ui.Context, tk Tokens, s string) {
	ui.Text(c, s).FontSize(10).Font("monospace").TextColor(tk.TextMuted).SingleLine()
}

func artifactCard(c *ui.Context, tk Tokens, icon *ui.SVG, color ui.Color, title, sub, meta string,
	verify, verifyNote string, footer func()) {
	ui.Column(c).Grow(1).Padding(16).Gap(9).Radius(12).Background(tk.Surface).
		Border(1, tk.Border).Shadow(0, 1, 2, 0, ui.RGBA(16, 24, 40, 0.04)).Children(func() {
		ui.Row(c).Gap(9).AlignItems(ui.Center).Children(func() {
			ui.Box(c).Size(30, 30).Radius(8).Background(tk.SurfaceAlt).Center().
				Children(func() { ui.Icon(c, icon).FontSize(16).TextColor(color) })
			ui.Text(c, title).FontSize(13).Bold().TextColor(tk.Text).Grow(1)
			verifyBadge(c, tk, verify, verifyNote)
			ui.Icon(c, icExternal).FontSize(13).TextColor(tk.TextFaint)
		})
		ui.Text(c, sub).FontSize(11).TextColor(tk.TextMuted)
		footer()
		ui.Spacer(c)
		ui.Text(c, meta).FontSize(10).TextColor(tk.TextFaint)
	})
}

// A miniature native window used as the "screenshot" inside the PNG card.
func miniScreenshot(c *ui.Context, tk Tokens) {
	ui.Column(c).Fill().Radius(5).Background(tk.Surface).Children(func() {
		ui.Row(c).Height(14).Padding(0, 6).Gap(3).AlignItems(ui.Center).
			Background(tk.SurfaceAlt).Children(func() {
			dot(c, tk.Red, 3)
			dot(c, tk.Amber, 3)
			dot(c, tk.Green, 3)
			ui.Spacer(c)
			ui.Text(c, "登录设置").FontSize(6).TextColor(tk.TextFaint)
			ui.Spacer(c)
		})
		ui.Column(c).Padding(8).Gap(4).Children(func() {
			ui.Text(c, "会话超时").FontSize(7).TextColor(tk.Text)
			ui.Row(c).Height(10).Radius(3).Background(tk.SurfaceAlt).Padding(0, 4).
				AlignItems(ui.Center).Children(func() {
				ui.Text(c, "30 分钟").FontSize(6).TextColor(tk.TextMuted)
				ui.Spacer(c)
			})
			ui.Text(c, "空闲超时").FontSize(7).TextColor(tk.Text)
			ui.Row(c).Height(10).Radius(3).Background(tk.SurfaceAlt).Padding(0, 4).
				AlignItems(ui.Center).Children(func() {
				ui.Text(c, "15 分钟").FontSize(6).TextColor(tk.TextMuted)
				ui.Spacer(c)
			})
		})
	})
}

func bigScreenshot(c *ui.Context, tk Tokens) {
	ui.Column(c).Fill().Radius(8).Background(tk.Surface).Children(func() {
		ui.Row(c).Height(24).Padding(0, 10).Gap(4).AlignItems(ui.Center).
			Background(tk.SurfaceAlt).Children(func() {
			dot(c, tk.Red, 4)
			dot(c, tk.Amber, 4)
			dot(c, tk.Green, 4)
			ui.Spacer(c)
			ui.Text(c, "登录设置").FontSize(9).TextColor(tk.TextFaint)
			ui.Spacer(c)
		})
		ui.Column(c).Padding(18).Gap(10).Children(func() {
			ui.Text(c, "会话与安全").FontSize(13).Bold().TextColor(tk.Text)
			ui.Text(c, "空闲与登录的超时时间，保存后对新会话生效。").FontSize(9).TextColor(tk.TextMuted)
			kv(c, tk, "会话超时", "30 分钟")
			kv(c, tk, "空闲超时", "15 分钟")
			kv(c, tk, "记住登录", "7 天")
			ui.Row(c).Gap(6).Children(func() {
				ui.Row(c).Height(20).Padding(0, 10).Radius(5).Background(tk.Accent).Center().Children(func() {
					ui.Text(c, "保存").FontSize(8).TextColor(ui.Hex("#FFFFFF"))
				})
				textChip(c, tk, "取消")
			})
		})
	})
}

// ---- 06 · Trigger: the inbox of threads, subscriptions and decisions ----

func trigger(c *ui.Context) {
	tk := tokens()
	window(c, tk, "trigger", func() {
		content(c, func() {
			ui.Row(c).AlignItems(ui.Center).Children(func() {
				heading(c, tk, "Trigger", "线程、决策与订阅；外部文本永远只是数据，不是指令")
				ui.Spacer(c)
				textChip(c, tk, "过去 24 小时")
				smallButton(c, tk, "立即轮询")
			})
			ui.Row(c).Grow(1).AlignItems(ui.Stretch).Gap(16).Children(func() {
				threadsCard(c, tk)
				threadDetail(c, tk)
			})
		})
	})
}

func threadsCard(c *ui.Context, tk Tokens) {
	rows := []struct {
		title, meta, stage string
		fg, bg             ui.Color
	}{
		{"#42 修改登录超时配置", "GitHub · Issue · @lin · 12:04", "执行中", tk.Green, tk.GreenSoft},
		{"#128 CI 失败：TestSessionTimeout", "GitHub · PR · workflow · 12:31", "已归入 #42", tk.Violet, tk.VioletSoft},
		{"#44 auth 包测试超时", "GitHub · Issue · @zhou · 11:52", "需要授权", tk.Amber, tk.AmberSoft},
		{"研发群：上线前是否要灰度？", "飞书 · 群消息 · 12:40", "仅上下文", tk.TextMuted, tk.SurfaceAlt},
		{"#45 补 session 迁移脚本", "GitHub · Issue · @lin · 10:18", "等待评审", tk.Violet, tk.VioletSoft},
		{"每日 CI 巡检", "Schedule · 工作日 09:00", "已过滤", tk.TextFaint, tk.SurfaceAlt},
		{"PR #127 已合并且部署", "GitHub · PR · @lin · 昨天", "已归档", tk.TextFaint, tk.SurfaceAlt},
		{"文档评论：配置项命名", "飞书 · 文档 · @wang · 昨天", "仅上下文", tk.TextMuted, tk.SurfaceAlt},
	}
	ui.Column(c).Width(470).Background(tk.Surface).Radius(tk.Radius).Border(1, tk.Border).
		Padding(14).Gap(10).Shadow(0, 1, 2, 0, ui.RGBA(16, 24, 40, 0.04)).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "线程").FontSize(14).Bold().TextColor(tk.Text)
			ui.Spacer(c)
			ui.Text(c, "按最近 Trigger 排序").FontSize(10).TextColor(tk.TextFaint)
		})
		ui.Row(c).Gap(6).Children(func() {
			chip(c, tk, "已订阅 3", tk.Accent, tk.AccentSoft)
			chip(c, tk, "评估中 5", tk.TextMuted, tk.SurfaceAlt)
			chip(c, tk, "新 24h 12", tk.TextMuted, tk.SurfaceAlt)
			chip(c, tk, "全部", tk.TextMuted, tk.SurfaceAlt)
		})
		for _, r := range rows {
			row := r
			ui.Row(c).Height(58).Padding(0, 10).Gap(10).Radius(8).Background(tk.SurfaceAlt).
				AlignItems(ui.Center).Children(func() {
				ui.Box(c).Size(28, 28).Radius(7).Background(tk.Surface).Border(1, tk.BorderSoft).Center().
					Children(func() { ui.Icon(c, icTrigger).FontSize(13).TextColor(tk.TextMuted) })
				ui.Column(c).Grow(1).Gap(2).Children(func() {
					ui.Text(c, row.title).FontSize(12).TextColor(tk.Text).SingleLine()
					ui.Text(c, row.meta).FontSize(10).TextColor(tk.TextFaint)
				})
				chip(c, tk, row.stage, row.fg, row.bg)
			})
		}
		ui.Spacer(c)
		ui.Row(c).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "订阅 3 · 评估中 5 · 24h 新 12").FontSize(10).TextColor(tk.TextFaint)
			ui.Spacer(c)
			ui.Text(c, "⌘K 搜索全部线程").FontSize(10).TextColor(tk.TextFaint)
		})
	})
}

func threadDetail(c *ui.Context, tk Tokens) {
	ui.Column(c).Grow(1).Gap(12).Children(func() {
		cardG(c, tk, 16, func() {
			ui.Row(c).AlignItems(ui.Center).Children(func() {
				ui.Column(c).Grow(1).Gap(3).Children(func() {
					ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
						ui.Text(c, "Issue #42 · 修改登录超时配置").FontSize(15).Bold().TextColor(tk.Text)
						chip(c, tk, "已订阅", tk.Accent, tk.AccentSoft)
						chip(c, tk, "Ticket #42", tk.Violet, tk.VioletSoft)
					})
					ui.Text(c, "acme/webapp · Thread 12 个 Trigger · 最后活动 12:40").
						FontSize(11).TextColor(tk.TextMuted)
				})
				smallButton(c, tk, "取消订阅")
				smallButton(c, tk, "查看 Ticket")
			})
			ui.Divider(c)
			for _, e := range threadEvents {
				threadRow(c, tk, e)
			}
			ui.Divider(c)
			ui.Row(c).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, icShield).FontSize(12).TextColor(tk.Green)
				ui.Text(c, " 外部文本只作为数据进入上下文；权限由 Policy + Mandate 决定").
					FontSize(10).TextColor(tk.TextMuted)
				ui.Spacer(c)
				ui.Text(c, "游标已推进 · 12 秒前轮询").FontSize(10).TextColor(tk.TextFaint)
			})
		})
		// Subscriptions and policy, the deterministic half of the thread
		card(c, tk, 14, func() {
			ui.Row(c).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "订阅与 Policy").FontSize(13).Bold().TextColor(tk.Text)
				ui.Spacer(c)
				ui.Text(c, "规则版本 v7 · 只读").FontSize(10).TextColor(tk.TextFaint)
			})
			subRow(c, tk, "订阅：Issue #42（follows Ticket #42）", "新评论 / CI 状态写入上下文", tk.Accent)
			subRow(c, tk, "过滤：每日 CI 巡检 → 已过滤", "非本仓库、机器人账号、重复通知", tk.TextMuted)
			subRow(c, tk, "授权：本 Thread 不扇出 Mandate", "订阅只增加上下文，永不授权", tk.Green)
		})
	})
}

func subRow(c *ui.Context, tk Tokens, title, meta string, color ui.Color) {
	ui.Row(c).Padding(8, 9).Gap(8).Radius(7).Background(tk.SurfaceAlt).AlignItems(ui.Center).
		Children(func() {
			dot(c, color, 6)
			ui.Column(c).Grow(1).Gap(1).Children(func() {
				ui.Text(c, title).FontSize(11).TextColor(tk.Text)
				ui.Text(c, meta).FontSize(10).TextColor(tk.TextFaint)
			})
		})
}

func threadRow(c *ui.Context, tk Tokens, e threadEvent) {
	icon := icTrigger
	fg, bg := tk.Accent, tk.AccentSoft
	switch e.kind {
	case "decision":
		icon = icEval
		fg, bg = tk.Violet, tk.VioletSoft
	case "context":
		icon = icFile
		fg, bg = tk.TextMuted, tk.SurfaceAlt
	}
	ui.Row(c).Padding(9, 8).Gap(10).Radius(8).AlignItems(ui.Start).Children(func() {
		ui.Box(c).Size(24, 24).Radius(6).Background(bg).Center().
			Children(func() { ui.Icon(c, icon).FontSize(12).TextColor(fg) })
		ui.Column(c).Grow(1).Gap(2).Children(func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, e.title).FontSize(12).TextColor(tk.Text)
				chip(c, tk, e.kind, fg, bg)
			})
			ui.Text(c, e.reason).FontSize(10).TextColor(tk.TextMuted)
		})
		ui.Text(c, e.at).FontSize(10).TextColor(tk.TextFaint)
	})
}
