package main

import "github.com/egoist/mygo/ui"

// ---- 02 · Execution: one Ticket, its live transcript, its inspector ----

func execution(c *ui.Context) {
	executionScene(c, nil)
}

func executionScene(c *ui.Context, overlay func(*ui.Context)) {
	tk := tokens()
	windowWith(c, tk, "execution", func() {
		content(c, func() {
			execHeader(c, tk)
			ui.Row(c).Grow(1).AlignItems(ui.Stretch).Gap(16).Children(func() {
				ui.Column(c).Grow(1).Gap(8).Children(func() {
					ui.Scroll(c).Grow(1).Gap(8).Children(func() {
						transcript(c, tk)
					})
					attentionBar(c, tk)
					composer(c, tk)
				})
				inspector(c, tk)
			})
		})
	}, overlay)
}

func execHeader(c *ui.Context, tk Tokens) {
	ui.Row(c).AlignItems(ui.Center).Children(func() {
		ui.Column(c).Grow(1).Gap(5).Children(func() {
			ui.Text(c, "Execution / acme/webapp / Ticket #42").FontSize(11).TextColor(tk.TextFaint)
			ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "修改登录超时配置").FontSize(22).Bold().TextColor(tk.Text)
				chip(c, tk, "● 执行中", tk.Green, tk.GreenSoft)
				chip(c, tk, "PR #128 草案", tk.Accent, tk.AccentSoft)
				chip(c, tk, "12m · step 7", tk.Violet, tk.VioletSoft)
			})
		})
		smallButton(c, tk, "在目录中打开")
		ui.Row(c).Height(30).Width(30).Radius(7).Background(tk.Surface).Border(1, tk.Border).
			Center().Children(func() { ui.Icon(c, icPause).FontSize(13).TextColor(tk.Text) })
		ui.Row(c).Height(30).Padding(0, 11).Radius(7).Background(tk.RedSoft).
			AlignItems(ui.Center).Gap(6).Children(func() {
			ui.Icon(c, icStop).FontSize(12).TextColor(tk.Red)
			ui.Text(c, "停止").FontSize(12).TextColor(tk.Red)
		})
	})
}

func transcript(c *ui.Context, tk Tokens) {
	// 1. the Trigger that started it
	ui.Row(c).Padding(12).Gap(10).Radius(10).Background(tk.Surface).
		Border(1, tk.Border).AlignItems(ui.Start).Children(func() {
		ui.Box(c).Size(30, 30).Radius(8).Background(ui.Hex("#24292F")).Center().
			Children(func() { ui.Text(c, "GH").FontSize(10).Bold().TextColor(ui.Hex("#FFFFFF")) })
		ui.Column(c).Grow(1).Gap(3).Children(func() {
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "GitHub · Issue #42 · @lin 创建").FontSize(11).TextColor(tk.TextMuted)
				chip(c, tk, "Trigger", tk.Accent, tk.AccentSoft)
			})
			ui.Text(c, "登录超时应该可配置，默认 30 分钟。现在写死 15 分钟，运维没法改。").
				FontSize(12).TextColor(tk.Text)
		})
		ui.Text(c, "12:04").FontSize(11).TextColor(tk.TextFaint)
	})
	// 2. the agent's opening line
	ui.Row(c).Padding(2).Gap(10).AlignItems(ui.Start).Children(func() {
		ui.Box(c).Size(26, 26).Radius(13).Background(tk.Violet).Center().
			Children(func() { ui.Icon(c, icSpark).FontSize(14).TextColor(ui.Hex("#FFFFFF")) })
		ui.Column(c).Grow(1).Gap(3).Children(func() {
			ui.Text(c, "ZeoKeel Agent · 开始处理").FontSize(11).TextColor(tk.TextMuted)
			ui.Text(c, "我先定位认证模块里的超时硬编码，再抽出配置项，最后补测试与文档。").
				FontSize(13).TextColor(tk.Text)
		})
	})
	// 3. plan
	ui.Column(c).Padding(10).Gap(6).Radius(10).Background(tk.Surface).
		Border(1, tk.Border).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "计划").FontSize(12).Bold().TextColor(tk.Text)
			chip(c, tk, "3 步", tk.TextMuted, tk.SurfaceAlt)
			ui.Spacer(c)
			ui.Text(c, "自动继续 · 开").FontSize(10).TextColor(tk.TextFaint)
		})
		for _, p := range plan {
			planRow(c, tk, p)
		}
	})
	// 4. bash tool call
	toolCard(c, tk, icTerminal, "bash", `rg -n "15 * time.Minute" internal/auth`, "exit 0 · 0.4s", tk.Green,
		[]string{`internal/auth/session.go:42:    Timeout: 15 * time.Minute,`, `internal/auth/session.go:88:    Timeout: 15 * time.Minute,`}, nil)
	// 5. edit tool call with a real diff
	diffCard(c, tk)
	// 6. the test passed: a compact receipt, not a full card
	ui.Row(c).Padding(2, 10).Gap(6).AlignItems(ui.Center).Children(func() {
		ui.Icon(c, icCheck).FontSize(12).TextColor(tk.Green)
		ui.Text(c, "go test ./internal/auth/... 通过 · 1.82s").FontSize(11).Font("monospace").TextColor(tk.TextMuted)
	})
	// 7. streaming answer
	ui.Row(c).Padding(2).Gap(10).AlignItems(ui.Start).Children(func() {
		ui.Box(c).Size(26, 26).Radius(13).Background(tk.Violet).Center().
			Children(func() { ui.Icon(c, icSpark).FontSize(14).TextColor(ui.Hex("#FFFFFF")) })
		ui.Column(c).Grow(1).Gap(2).Children(func() {
			ui.Text(c, "配置项已抽出，正在补 SessionTimeout 的默认值与校验… ▍").
				FontSize(13).TextColor(tk.Text)
		})
	})
}

// attentionBar is pinned above the composer: the one thing that needs the
// person stays visible while the transcript scrolls.
func attentionBar(c *ui.Context, tk Tokens) {
	ui.Row(c).Padding(9, 11).Gap(10).Radius(9).Background(tk.AmberSoft).
		AlignItems(ui.Center).Children(func() {
		ui.Icon(c, icBell).FontSize(15).TextColor(tk.Amber)
		ui.Column(c).Grow(1).Gap(1).Children(func() {
			ui.Text(c, "需要你确认：配置名用 session_timeout 还是 session.timeout？").
				FontSize(12).TextColor(ui.Hex("#7A2E0E"))
			ui.Text(c, "ask_user_question · 已等待 3m · 托盘与 Dock 已提示").FontSize(10).TextColor(ui.Hex("#B54708"))
		})
		primaryButton(c, tk, "回答")
	})
}

func planRow(c *ui.Context, tk Tokens, p planStep) {
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		switch p.state {
		case 2:
			ui.Box(c).Size(15, 15).Radius(8).Background(tk.Green).Center().
				Children(func() { ui.Icon(c, icCheck).FontSize(10).TextColor(ui.Hex("#FFFFFF")) })
		case 1:
			ui.Box(c).Size(15, 15).Radius(8).Border(2, tk.Accent).Center().Children(func() {
				ui.Box(c).Size(5, 5).Radius(3).Background(tk.Accent)
			})
		default:
			ui.Box(c).Size(15, 15).Radius(8).Border(1.5, tk.Border).Background(tk.Surface)
		}
		ui.Text(c, p.text).FontSize(12).TextColor(condColor(p.state == 0, tk.TextMuted, tk.Text))
	})
}

func toolCard(c *ui.Context, tk Tokens, icon *ui.SVG, kind, detail, result string, resultColor ui.Color, out []string, diff []ui.Color) {
	ui.Column(c).Padding(11).Gap(8).Radius(10).Background(tk.Surface).
		Border(1, tk.Border).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, icon).FontSize(14).TextColor(tk.TextMuted)
			ui.Text(c, kind).FontSize(11).Bold().Font("monospace").TextColor(tk.Text)
			ui.Text(c, detail).FontSize(11).Font("monospace").TextColor(tk.TextMuted)
			ui.Spacer(c)
			ui.Text(c, result).FontSize(11).TextColor(resultColor)
		})
		ui.Column(c).Padding(8, 10).Radius(7).Background(tk.CodeBg).Gap(2).Children(func() {
			for _, line := range out {
				ui.Text(c, line).FontSize(11).Font("monospace").TextColor(tk.CodeText)
			}
		})
	})
}

func diffCard(c *ui.Context, tk Tokens) {
	lines := []struct {
		kind int // 0 context, 1 added, 2 removed
		text string
	}{
		{0, "@@ internal/auth/session.go @@ NewSession"},
		{2, "- Timeout: 15 * time.Minute,"},
		{1, "+ Timeout: cfg.SessionTimeout,"},
		{1, "+ if cfg.SessionTimeout <= 0 {"},
		{1, "+     cfg.SessionTimeout = 30 * time.Minute"},
		{1, "+ }"},
	}
	ui.Column(c).Padding(11).Gap(8).Radius(10).Background(tk.Surface).
		Border(1, tk.Border).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, icFile).FontSize(14).TextColor(tk.TextMuted)
			ui.Text(c, "edit").FontSize(11).Bold().Font("monospace").TextColor(tk.Text)
			ui.Text(c, "internal/auth/session.go").FontSize(11).Font("monospace").TextColor(tk.TextMuted)
			ui.Spacer(c)
			chip(c, tk, "+12 −3", tk.Green, tk.GreenSoft)
		})
		ui.Column(c).Radius(7).Background(tk.CodeBg).Children(func() {
			for _, l := range lines {
				line := l
				bg := ui.Transparent
				fg := tk.CodeText
				switch line.kind {
				case 1:
					bg = ui.RGBA(34, 197, 94, 0.16)
					fg = ui.Hex("#86EFAC")
				case 2:
					bg = ui.RGBA(248, 113, 113, 0.16)
					fg = ui.Hex("#FCA5A5")
				}
				ui.Row(c).Background(bg).Padding(2, 10).Children(func() {
					ui.Text(c, line.text).FontSize(11).Font("monospace").TextColor(fg)
				})
			}
		})
	})
}

func composer(c *ui.Context, tk Tokens) {
	ui.Column(c).Padding(12).Gap(8).Radius(11).Background(tk.Surface).
		Border(1, tk.Border).Shadow(0, 2, 8, 0, ui.RGBA(16, 24, 40, 0.05)).Children(func() {
		ui.Text(c, "继续指挥这个 Ticket…  ⌘↵ 发送").FontSize(12).TextColor(tk.TextFaint).
			Padding(2, 4, 4, 4)
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Row(c).Height(26).Padding(0, 9).Gap(5).Radius(6).Background(tk.SurfaceAlt).
				AlignItems(ui.Center).Children(func() {
				ui.Icon(c, icSpark).FontSize(12).TextColor(tk.Violet)
				ui.Text(c, "gpt-5-codex").FontSize(11).TextColor(tk.Text)
				ui.Icon(c, icChevD).FontSize(10).TextColor(tk.TextFaint)
			})
			textChip(c, tk, "计划模式")
			textChip(c, tk, "@ 文件")
			textChip(c, tk, "# 工具")
			ui.Spacer(c)
			ui.Text(c, "12.4k / 200k tokens").FontSize(10).TextColor(tk.TextFaint)
			ui.Row(c).Height(28).Width(34).Radius(7).Background(tk.Accent).Center().
				Children(func() { ui.Icon(c, icSend).FontSize(14).TextColor(ui.Hex("#FFFFFF")) })
		})
	})
}

func inspector(c *ui.Context, tk Tokens) {
	ui.Column(c).Width(380).Gap(12).Children(func() {
		// tab strip (the real app uses ui.Tabs; this shows the chosen page)
		ui.Row(c).Gap(6).Children(func() {
			chip(c, tk, "计划", tk.Accent, tk.AccentSoft)
			chip(c, tk, "工具 12", tk.TextMuted, tk.Surface)
			chip(c, tk, "授权 2", tk.TextMuted, tk.Surface)
			chip(c, tk, "交付物 2", tk.TextMuted, tk.Surface)
		})
		cardG(c, tk, 14, func() {
			ui.Row(c).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "这一轮在做什么").FontSize(13).Bold().TextColor(tk.Text)
				ui.Spacer(c)
				ui.Text(c, "step 7 / 20").FontSize(10).TextColor(tk.TextFaint)
			})
			for _, p := range plan {
				planRow(c, tk, p)
			}
			ui.Divider(c)
			kv(c, tk, "执行器", "A · 12m")
			kv(c, tk, "tokens", "12.4k / 200k")
			kv(c, tk, "花费", "$0.04 / $2.00")
			kv(c, tk, "工作区", "~/code/webapp")
			ui.Divider(c)
			ui.Text(c, "授权").FontSize(12).Bold().TextColor(tk.Text)
			approvalRow(c, tk, true, "bash · 本 Ticket 内始终允许", "读/写工作区 · 1 次使用")
			approvalRow(c, tk, false, "git push --force · 申请中", "等待你在对话框里决定")
			ui.Divider(c)
			ui.Text(c, "交付物 (2)").FontSize(12).Bold().TextColor(tk.Text)
			artifactMini(c, tk, "PR #128 — 支持 session_timeout", "+128 −36 · 9 files")
			artifactMini(c, tk, "report.md — 变更说明", "4.2 KB · 已生成")
		})
	})
}

func approvalRow(c *ui.Context, tk Tokens, ok bool, title, meta string) {
	color := tk.Amber
	if ok {
		color = tk.Green
	}
	ui.Row(c).Padding(8).Gap(8).Radius(7).Background(tk.SurfaceAlt).Children(func() {
		dot(c, color, 7)
		ui.Column(c).Grow(1).Gap(1).Children(func() {
			ui.Text(c, title).FontSize(11).TextColor(tk.Text)
			ui.Text(c, meta).FontSize(10).TextColor(tk.TextFaint)
		})
		if !ok {
			chip(c, tk, "待决定", tk.Amber, tk.AmberSoft)
		}
	})
}

func artifactMini(c *ui.Context, tk Tokens, title, meta string) {
	ui.Row(c).Padding(8).Gap(8).Radius(7).Background(tk.SurfaceAlt).AlignItems(ui.Center).
		Children(func() {
			ui.Box(c).Size(24, 24).Radius(6).Background(tk.AccentSoft).Center().
				Children(func() { ui.Icon(c, icFile).FontSize(13).TextColor(tk.Accent) })
			ui.Column(c).Grow(1).Gap(1).Children(func() {
				ui.Text(c, title).FontSize(11).TextColor(tk.Text).SingleLine()
				ui.Text(c, meta).FontSize(10).TextColor(tk.TextFaint)
			})
			ui.Icon(c, icChevR).FontSize(11).TextColor(tk.TextFaint)
		})
}
