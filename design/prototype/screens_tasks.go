package main

import "github.com/egoist/mygo/ui"

// ---- helper: a source badge for github / dingtalk / local / feishu ----

func sourceBadge(c *ui.Context, kind, label string, size float32) {
	bg := ui.Hex("#24292F")
	fg := ui.Hex("#FFFFFF")
	switch kind {
	case "dingtalk":
		bg = ui.Hex("#1677FF")
	case "feishu":
		bg = ui.Hex("#3370FF")
	case "local":
		bg = ui.Hex("#1F2937")
	}
	ui.Box(c).Size(size, size).Radius(size * 0.24).Background(bg).Center().Children(func() {
		if kind == "local" {
			ui.Icon(c, icTerminal).FontSize(size * 0.55).TextColor(fg)
			return
		}
		ui.Text(c, label).FontSize(size * 0.36).Bold().TextColor(fg)
	})
}

// ---- 14 · New task composer: the operator assigns work to a workspace ----

func newTask(c *ui.Context) {
	tk := tokens()
	taskInboxScene(c, func(c *ui.Context) {
		ui.Box(c).Absolute().Top(0).Left(0).Right(0).Bottom(0).
			Background(ui.RGBA(15, 23, 42, 0.28))
		composerDrawer(c, tk)
		postDispatchNote(c)
	})
}

func composerDrawer(c *ui.Context, tk Tokens) {
	ui.Column(c).Absolute().Top(0).Right(0).Bottom(0).Width(600).Background(tk.Surface).
		BorderWidth(0, 0, 0, 1).BorderColor(tk.Border).
		Shadow(-16, 0, 48, 0, ui.RGBA(15, 23, 42, 0.22)).Children(func() {
		// header
		ui.Row(c).Height(58).Padding(0, 22).Gap(10).AlignItems(ui.Center).
			BorderWidth(0, 0, 1, 0).BorderColor(tk.Border).Children(func() {
			ui.Box(c).Size(28, 28).Radius(8).Background(tk.AccentSoft).Center().
				Children(func() { ui.Icon(c, icPlus).FontSize(15).TextColor(tk.Accent) })
			ui.Column(c).Grow(1).Gap(1).Children(func() {
				ui.Text(c, "新建任务").FontSize(15).Bold().TextColor(tk.Text)
				ui.Text(c, "描述 + 工作区 + 附件；派发后回到列表").FontSize(10).TextColor(tk.TextMuted)
			})
			chip(c, tk, "⌘N", tk.TextMuted, tk.SurfaceAlt)
			ui.Icon(c, icClose).FontSize(15).TextColor(tk.TextFaint)
		})
		ui.Column(c).Padding(22).Gap(14).Children(func() {
			// description
			ui.Column(c).Gap(5).Children(func() {
				labelRow(c, tk, "任务描述", "必填")
				ui.Column(c).Padding(10, 12).Radius(8).Background(tk.SurfaceAlt).
					Border(1, tk.BorderSoft).Gap(2).Children(func() {
					ui.Text(c, "把 acme 2026-10 的用量数据整理成周报：CSV + 一张趋势图，").FontSize(13).TextColor(tk.Text)
					ui.Text(c, "写完发到钉钉给张三。").FontSize(13).TextColor(tk.Text)
					ui.Text(c, "可以按客户维度拆，注明统计口径。 ▍").FontSize(12).TextColor(tk.TextFaint)
				})
			})
			// workspace select, open with options
			ui.Column(c).Gap(5).Children(func() {
				labelRow(c, tk, "工作区", "选了就直派")
				ui.Row(c).Height(38).Padding(0, 10).Gap(8).Radius(8).Background(tk.Surface).
					Border(1.5, tk.Accent).AlignItems(ui.Center).Children(func() {
					ui.Box(c).Size(22, 22).Radius(6).Background(ui.Hex("#C026D3")).Center().
						Children(func() { ui.Text(c, "D").FontSize(10).Bold().TextColor(ui.Hex("#FFFFFF")) })
					ui.Text(c, "acme/data").FontSize(12).Bold().TextColor(tk.Text)
					ui.Text(c, "ws_acme_data").FontSize(10).Font("monospace").TextColor(tk.TextFaint)
					ui.Spacer(c)
					ui.Icon(c, icChevD).FontSize(12).TextColor(tk.TextMuted)
				})
				// the open menu
				ui.Column(c).Padding(6).Gap(2).Radius(8).Background(tk.Surface).
					Border(1, tk.Border).Shadow(0, 10, 26, 0, ui.RGBA(15, 23, 42, 0.12)).Children(func() {
					menuRow(c, tk, "自动判断", "由 Router 判断派给哪个工作区", "", false)
					menuRow(c, tk, "acme/webapp", "前端 · 登录与设置", "W", false)
					menuRow(c, tk, "acme/api", "v2 接口与 OpenAPI", "A", false)
					menuRow(c, tk, "acme/data", "客户用量取数 · 报表 · 趋势分析", "D", true)
				})
			})
			// what this task becomes in the domain: no second channel
			ui.Column(c).Gap(5).Children(func() {
				labelRow(c, tk, "派发后会发生什么", "仍走领域，不加第二条通道")
				ui.Column(c).Padding(10, 12).Gap(7).Radius(8).Background(tk.SurfaceAlt).
					Border(1, tk.BorderSoft).Children(func() {
					flowStep(c, tk, icTrigger, "local Source 的一条 Trigger", "actor=operator · human · addressed=direct")
					flowStep(c, tk, icCheck, "Decision{accept}（跳过语义评估）", "reason: said + direct-dispatch")
					flowStep(c, tk, icRun, "Ticket 在 acme/data 排队", "Mandate 由该工作区 Policy 签发；状态机不变")
				})
			})
			// attachments
			ui.Column(c).Gap(6).Children(func() {
				labelRow(c, tk, "附件 3", "图片 worker 能直接看；其它文件给路径")
				ui.Row(c).Gap(8).Children(func() {
					attachment(c, tk, "image", "看板截图.png", "PNG · 1440×900")
					attachment(c, tk, "image", "口径说明.png", "PNG · 720×480")
					attachment(c, tk, "file", "acme-2026-09.csv", "2.1 MB · 样例")
					ui.Column(c).Width(118).Height(74).Radius(8).Border(1, tk.Border).
						Background(tk.SurfaceAlt).Center().Gap(4).Children(func() {
						ui.Icon(c, icPlus).FontSize(15).TextColor(tk.TextFaint)
						ui.Text(c, "拖拽 / 选择文件").FontSize(9).TextColor(tk.TextFaint)
					})
				})
			})
		})
		ui.Spacer(c)
		// footer: actions + routing explanation at the bottom right
		ui.Column(c).Padding(18, 22).Gap(8).BorderWidth(1, 0, 0, 0).BorderColor(tk.Border).
			Children(func() {
				ui.Row(c).AlignItems(ui.Center).Children(func() {
					ui.Text(c, "⌘↵ 派发后输入框清空，任务出现在列表顶部").FontSize(10).TextColor(tk.TextFaint)
					ui.Spacer(c)
					smallButton(c, tk, "取消")
					primaryButton(c, tk, "⌘↵ 派发")
				})
				ui.Row(c).Children(func() {
					ui.Spacer(c)
					ui.Text(c, "直接派发到 ws_acme_data（不再询问）").FontSize(10).TextColor(tk.TextFaint)
				})
			})
	})
}

func flowStep(c *ui.Context, tk Tokens, icon *ui.SVG, title, meta string) {
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Icon(c, icon).FontSize(12).TextColor(tk.TextMuted)
		ui.Column(c).Grow(1).Gap(1).Children(func() {
			ui.Text(c, title).FontSize(10).TextColor(tk.Text)
			ui.Text(c, meta).FontSize(9).TextColor(tk.TextFaint)
		})
	})
}

func labelRow(c *ui.Context, tk Tokens, label, hint string) {
	ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
		ui.Text(c, label).FontSize(11).TextColor(tk.TextMuted)
		ui.Text(c, hint).FontSize(9).TextColor(tk.TextFaint)
	})
}

func menuRow(c *ui.Context, tk Tokens, name, purpose, initial string, selected bool) {
	ui.Row(c).Padding(7, 9).Gap(9).Radius(6).AlignItems(ui.Center).
		Background(condColor(selected, tk.AccentSoft, ui.Transparent)).Children(func() {
		if initial == "" {
			ui.Box(c).Size(20, 20).Radius(5).Background(tk.SurfaceAlt).Border(1, tk.BorderSoft).Center().
				Children(func() { ui.Icon(c, icSpark).FontSize(11).TextColor(tk.Violet) })
		} else {
			ui.Box(c).Size(20, 20).Radius(5).Background(ui.Hex("#C026D3")).Center().
				Children(func() { ui.Text(c, initial).FontSize(9).Bold().TextColor(ui.Hex("#FFFFFF")) })
		}
		ui.Column(c).Grow(1).Gap(1).Children(func() {
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				ui.Text(c, name).FontSize(11).TextColor(tk.Text)
				if selected {
					chip(c, tk, "已选择", tk.Accent, tk.Surface)
				}
			})
			ui.Text(c, purpose).FontSize(10).TextColor(tk.TextFaint)
		})
		if selected {
			ui.Icon(c, icCheck).FontSize(12).TextColor(tk.Accent)
		}
	})
}

func attachment(c *ui.Context, tk Tokens, kind, name, meta string) {
	ui.Column(c).Width(118).Height(74).Radius(8).Background(tk.SurfaceAlt).
		Border(1, tk.BorderSoft).Children(func() {
		if kind == "image" {
			ui.Box(c).Grow(1).Radius(7).Gradient(ui.Hex("#DBEAFE"), ui.Hex("#EEF2FF"), 60).Children(func() {
				ui.Column(c).Fill().Padding(6).Justify(ui.End).Gap(2).Children(func() {
					ui.Row(c).Gap(3).AlignItems(ui.End).Children(func() {
						for _, h := range []float32{8, 14, 11, 20, 16} {
							ui.Box(c).Width(8).Height(h).Radius(2).Background(ui.RGBA(47, 91, 255, 0.45))
						}
					})
				})
			})
		} else {
			ui.Row(c).Grow(1).Radius(7).Background(tk.SurfaceAlt).Center().Children(func() {
				ui.Icon(c, icFile).FontSize(18).TextColor(tk.TextMuted)
			})
		}
		ui.Column(c).Padding(5, 7).Gap(0).Children(func() {
			ui.Text(c, name).FontSize(9).TextColor(tk.Text).SingleLine()
			ui.Text(c, meta).FontSize(8).TextColor(tk.TextFaint)
		})
	})
}

func postDispatchNote(c *ui.Context) {
	tk := tokens()
	ui.Column(c).Absolute().Left(280).Bottom(40).Width(380).Padding(14).Gap(7).Radius(10).
		Background(ui.RGBA(255, 255, 255, 0.96)).Border(1, tk.Border).
		Shadow(0, 14, 36, 0, ui.RGBA(15, 23, 42, 0.18)).Children(func() {
		ui.Text(c, "派发后（同一界面，200ms 内）").FontSize(12).Bold().TextColor(tk.Text)
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Box(c).Size(22, 22).Radius(6).Background(ui.Hex("#1F2937")).Center().
				Children(func() { ui.Icon(c, icTerminal).FontSize(12).TextColor(ui.Hex("#FFFFFF")) })
			ui.Column(c).Grow(1).Gap(1).Children(func() {
				ui.Text(c, "#46 给 acme 2026-10 用量出周报").FontSize(10).TextColor(tk.Text)
				ui.Text(c, "acme/data · 排队中 · 刚刚").FontSize(9).TextColor(tk.TextFaint)
			})
			chip(c, tk, "queued", tk.Amber, tk.AmberSoft)
		})
		ui.Text(c, "不弹窗、不跳页、不 toast；输入框清空，新任务出现在 Execution 列表顶部。").
			FontSize(10).TextColor(tk.TextMuted)
	})
}

// ---- 15 · Task inbox: everything the operator assigned, by latest activity ----

type taskRow struct {
	source, sourceLabel     string
	id, title, workspace    string
	stage, stageFG, stageBG string
	activity                string
	artifacts               []string
}

var taskRows = []taskRow{
	{"local", "", "#46", "给 acme 2026-10 用量出周报", "acme/data", "执行中", "#12805C", "#E7F6EF", "刚刚", []string{}},
	{"github", "GH", "#45", "为 session 配置补迁移脚本", "acme/webapp", "等待评审", "#6941C6", "#F1EDFF", "4 分钟前", []string{"PR #130"}},
	{"github", "GH", "#44", "CI 失败：auth 包测试超时", "acme/webapp", "需要授权", "#B42318", "#FEECEB", "12 分钟前", []string{}},
	{"local", "", "#43", "给 /v2 接口补 OpenAPI 描述", "acme/api", "等待评审", "#6941C6", "#F1EDFF", "26 分钟前", []string{"PR #57", "2 交付物"}},
	{"github", "GH", "#42", "修改登录超时配置", "acme/webapp", "等待回答", "#B54708", "#FEF3D6", "1 小时前", []string{"PR #128", "3 交付物"}},
	{"dingtalk", "钉", "#41", "acme 9 月用量数据核对", "acme/data", "受阻", "#B42318", "#FEECEB", "2 小时前", []string{"1 交付物"}},
	{"feishu", "飞", "#38", "支持飞书图片附件", "acme/webapp", "排队", "#475467", "#F2F4F7", "3 小时前", []string{}},
	{"github", "GH", "#40", "每周用量周报（定时）", "acme/data", "等待触发", "#475467", "#F2F4F7", "6 小时前", []string{}},
	{"github", "GH", "#37", "回滚 gopkg 依赖升级", "acme/webapp", "已完成", "#12805C", "#E7F6EF", "昨天 18:20", []string{"1 交付物"}},
	{"github", "GH", "#39", "每日 CI 巡检报告", "acme/webapp", "已完成", "#12805C", "#E7F6EF", "昨天 09:12", []string{"1 交付物"}},
}

func taskInbox(c *ui.Context) {
	taskInboxScene(c, nil)
}

func taskInboxScene(c *ui.Context, overlay func(*ui.Context)) {
	tk := tokens()
	windowWith(c, tk, "execution", func() {
		content(c, func() {
			ui.Row(c).AlignItems(ui.Center).Children(func() {
				ui.Column(c).Grow(1).Gap(5).Children(func() {
					ui.Text(c, "Execution / 任务列表").FontSize(11).TextColor(tk.TextFaint)
					ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
						ui.Text(c, "任务").FontSize(22).Bold().TextColor(tk.Text)
						chip(c, tk, "8 个进行中 / 共 46", tk.Accent, tk.AccentSoft)
						ui.Text(c, "按最近活动排序，不是创建时间").FontSize(11).TextColor(tk.TextMuted)
					})
				})
				primaryButton(c, tk, "＋ 新建任务")
			})
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				for i, f := range []string{"Open", "Pending", "Working", "Waiting", "Ended"} {
					if i == 0 {
						chip(c, tk, f+" 8", tk.Accent, tk.AccentSoft)
					} else {
						chip(c, tk, f, tk.TextMuted, tk.Surface)
					}
				}
				ui.Spacer(c)
				ui.Text(c, "⌘N 新建任务 · ⌘K 搜索").FontSize(10).TextColor(tk.TextFaint)
			})
			ui.Column(c).Grow(1).Background(tk.Surface).Radius(12).Border(1, tk.Border).
				Shadow(0, 1, 2, 0, ui.RGBA(16, 24, 40, 0.04)).Padding(0).Children(func() {
				// table header
				ui.Row(c).Height(34).Padding(0, 16).Gap(12).AlignItems(ui.Center).
					Background(tk.SurfaceAlt).BorderWidth(0, 0, 1, 0).BorderColor(tk.Border).Children(func() {
					ui.Text(c, "来源").FontSize(10).TextColor(tk.TextFaint).Width(38)
					ui.Text(c, "任务").FontSize(10).TextColor(tk.TextFaint).Grow(1)
					ui.Text(c, "工作区").FontSize(10).TextColor(tk.TextFaint).Width(112)
					ui.Text(c, "阶段").FontSize(10).TextColor(tk.TextFaint).Width(88)
					ui.Text(c, "最近活动").FontSize(10).TextColor(tk.TextFaint).Width(86)
					ui.Text(c, "产物").FontSize(10).TextColor(tk.TextFaint).Width(170)
				})
				for _, r := range taskRows {
					row := r
					ui.Row(c).Height(46).Padding(0, 16).Gap(12).AlignItems(ui.Center).
						BorderWidth(0, 0, 1, 0).BorderColor(tk.BorderSoft).Children(func() {
						sourceBadge(c, row.source, row.sourceLabel, 26)
						ui.Row(c).Grow(1).Gap(6).AlignItems(ui.Center).Children(func() {
							ui.Text(c, row.id).FontSize(10).Font("monospace").TextColor(tk.TextFaint)
							ui.Text(c, row.title).FontSize(12).TextColor(tk.Text).SingleLine()
						})
						ui.Box(c).Width(112).Children(func() {
							chip(c, tk, row.workspace, tk.TextMuted, tk.SurfaceAlt)
						})
						ui.Box(c).Width(88).Children(func() {
							chip(c, tk, row.stage, ui.Hex(row.stageFG), ui.Hex(row.stageBG))
						})
						ui.Text(c, row.activity).FontSize(10).TextColor(tk.TextFaint).Width(86)
						ui.Row(c).Width(170).Gap(5).AlignItems(ui.Center).Children(func() {
							if len(row.artifacts) == 0 {
								ui.Text(c, "—").FontSize(10).TextColor(tk.TextFaint)
							}
							for _, a := range row.artifacts {
								chip(c, tk, a, tk.Accent, tk.AccentSoft)
							}
						})
					})
				}
				ui.Spacer(c)
				ui.Row(c).Height(34).Padding(0, 16).Gap(8).AlignItems(ui.Center).
					BorderWidth(1, 0, 0, 0).BorderColor(tk.BorderSoft).Children(func() {
					ui.Text(c, "共 46 个任务 · 显示最近 10 个，滚动加载更多").FontSize(10).TextColor(tk.TextFaint)
					ui.Spacer(c)
					ui.Text(c, "⌘K 搜索全部任务").FontSize(10).TextColor(tk.TextFaint)
				})
			})
			// empty state, as a design note inside the screen
			ui.Row(c).Height(64).Padding(12, 16).Gap(14).Radius(10).Background(tk.Surface).
				Border(1, tk.Border).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "空态 · 首次使用").FontSize(10).Bold().TextColor(tk.TextMuted)
				ui.Text(c, "还没有任务。").FontSize(12).TextColor(tk.Text)
				ui.Row(c).Grow(1).Height(32).Padding(0, 10).Radius(7).Background(tk.SurfaceAlt).
					Border(1, tk.BorderSoft).AlignItems(ui.Center).Children(func() {
					ui.Text(c, "描述你想让它做的事…").FontSize(11).TextColor(tk.TextFaint)
					ui.Spacer(c)
					chip(c, tk, "⌘↵ 派发", tk.TextMuted, tk.Surface)
				})
				ui.Text(c, "一句话 + 输入框，不放插画").FontSize(9).TextColor(tk.TextFaint)
			})
		})
	}, overlay)
}
