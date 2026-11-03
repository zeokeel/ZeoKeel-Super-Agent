// Command prototype renders the ZeoKeel desktop client's design with mygo's
// own native UI toolkit: each screen is a real ui view, drawn headlessly by
// ui.Render and saved as PNG. No window and no display are needed.
package main

import (
	"flag"
	"fmt"
	"image/png"
	"os"
	"path/filepath"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

type screen struct {
	slug  string
	title string
	view  func(c *ui.Context)
}

func screens() []screen {
	return []screen{
		{"01-overview", "总览：现在需要我做什么", overview},
		{"02-execution", "Execution：Ticket 详情与实时过程", execution},
		{"03-attention", "Attention：结构化提问", attention},
		{"04-approval", "授权：危险操作的人工确认", approval},
		{"05-artifacts", "交付物：PR、报告、截图", artifacts},
		{"06-trigger", "Trigger：线程、订阅与决策", trigger},
		{"07-eval", "Eval：可检查的 reason 与规则", evalScreen},
		{"08-source", "Source：事件源与账号事实", sourceScreen},
		{"09-settings", "设置：模型、MCP、Skills、自动化", settings},
		{"10-tray", "托盘与通知：常驻但不打扰", tray},
		{"11-workspaces", "多个工作区：切换、聚合与隔离", workspacesScreen},
		{"12-workspace-admin", "工作区管理：根、Source、策略与预算", workspaceAdmin},
		{"13-multiwindow", "⌘N 多窗口并排（M5，非首版）", multiWindow},
		{"14-new-task", "⌘N 新建任务：描述 + 工作区 + 附件 + 派发", newTask},
		{"15-task-inbox", "Execution · 任务列表：按最近活动排序", taskInbox},
		{"00-flow", "一次 Trigger 的旅程", flow},
	}
}

var themeCache = theme()

func main() {
	window := flag.Bool("window", false, "open the prototype as a real mygo window instead of rendering PNGs")
	screenSlug := flag.String("screen", "01-overview", "screen to open with -window")
	outDir := flag.String("out", "shots", "directory for the rendered PNGs")
	flag.Parse()
	out := *outDir
	if !*window && flag.NArg() > 0 {
		out = flag.Arg(0)
	}
	if *window {
		for _, s := range screens() {
			if s.slug == *screenSlug {
				view := s.view
				mygo.App.WhenReady(func() {
					mygo.NewWindow(mygo.WindowOptions{
						Title:  "ZeoKeel 原型 · " + s.title,
						Width:  1440,
						Height: 900,
						Content: ui.View(func(c *ui.Context) {
							ui.Themed(c, themeCache).Fill().Children(func() { view(c) })
						}),
					})
				})
				if err := mygo.App.Run(); err != nil {
					fmt.Fprintln(os.Stderr, err)
					os.Exit(1)
				}
				return
			}
		}
		fmt.Fprintln(os.Stderr, "no screen", *screenSlug)
		os.Exit(1)
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	for _, s := range screens() {
		img := ui.Render(func(c *ui.Context) {
			ui.Themed(c, themeCache).Fill().Children(func() {
				s.view(c)
			})
		}, 1440, 900, 1.5)
		path := filepath.Join(out, s.slug+".png")
		f, err := os.Create(path)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if err := png.Encode(f, img); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		f.Close()
		fmt.Println("rendered", path)
	}
}
