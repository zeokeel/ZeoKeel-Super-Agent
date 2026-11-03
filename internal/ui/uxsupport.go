package ui

// UI/UX optimization round: the optional runtime capabilities the interaction
// audit's P0s need, the off-frame seams that call them, a toast layer, and the
// one "待你决定" count. Nothing here imports the runtime: each capability is an
// optional interface the command object either has or does not.

import (
	"context"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"
	mui "github.com/egoist/mygo/ui"

	"github.com/zeokeel/super-agent/internal/view"
)

// ModelTester probes the configured model endpoint with one tiny call. The
// runtime implements it; without it the 模型 page says so instead of pretending.
type ModelTester interface {
	CheckModel(ctx context.Context) (ok bool, detail string)
}

// WorkspaceDispatcher sends a line to one project (the composer's 项目 target),
// instead of the super-agent chat.
type WorkspaceDispatcher interface {
	DispatchTo(workspaceID, text string) error
}

// OnboardingSkipper finishes setup without model/workspace (演示模式).
type OnboardingSkipper interface {
	SkipOnboarding() (uint64, error)
}

// DemoModer reports that the install was finished in demo mode: the overview then
// explains the parked model instead of re-opening a wizard that can never finish.
type DemoModer interface {
	DemoMode() bool
}

// OnboardingFinisher marks setup complete, so the window leaves the wizard.
type OnboardingFinisher interface {
	FinishOnboarding() error
}

// AttentionCounter is the runtime's canonical attention count. The panel falls
// back to OverviewVM.PersonCount when it is absent.
type AttentionCounter interface {
	AttentionCount() int
}

// Clipboard, openers and notifications. Each is a seam so headless tests can see
// the call without a desktop.
var (
	copyTextToClipboard = func(text string) error {
		return mygo.Clipboard.Write(transfer.TextData(text))
	}
	openPathWithDefaultApp  = func(path string) error { return mygo.Shell.OpenPath(path) }
	showDesktopNotification = func(title, body string) error {
		return mygo.NewNotification(mygo.NotificationOptions{
			Title: title, Body: body, Group: "zeokeel-attention",
		}).Show()
	}
	startModelProbe = func(a *App) {
		a.runOffMain(func() {
			tester, ok := a.cmd.(ModelTester)
			var okModel bool
			var detail string
			if !ok || tester == nil {
				detail = "暂时不可用：运行时还没有接入模型检查"
			} else {
				okModel, detail = tester.CheckModel(context.Background())
			}
			a.postToUI(func() {
				a.modelTestPending = false
				a.modelTestOK = okModel
				a.modelTestDetail = detail
			})
		})
	}
	startDispatchTo = func(a *App, workspaceID, text string) {
		a.runOffMain(func() {
			dispatcher, ok := a.cmd.(WorkspaceDispatcher)
			var err error
			if !ok || dispatcher == nil {
				err = errNoWorkspaceDispatch{}
			} else {
				err = dispatcher.DispatchTo(workspaceID, text)
			}
			a.postToUI(func() { a.dispatchDone(text, err) })
		})
	}
	startSkipOnboarding = func(a *App) {
		a.runOffMain(func() {
			skipper, ok := a.cmd.(OnboardingSkipper)
			var err error
			if !ok || skipper == nil {
				err = errNoOnboardingSkip{}
			} else {
				_, err = skipper.SkipOnboarding()
			}
			a.postToUI(func() {
				a.onbSkipping = false
				if err != nil {
					a.onb.err = err.Error()
					return
				}
				a.sec = view.SecOverview
				a.toast("已进入演示模式：任务会停在待你决定，随时可在设置 › 模型 里配置", toastInfo)
			})
		})
	}
	startFinishOnboarding = func(a *App) {
		a.runOffMain(func() {
			finisher, ok := a.cmd.(OnboardingFinisher)
			var err error
			if !ok || finisher == nil {
				err = errNoOnboardingFinish{}
			} else {
				err = finisher.FinishOnboarding()
			}
			a.postToUI(func() {
				a.onbFinishing = false
				if err != nil {
					a.onb.err = err.Error()
					return
				}
				a.sec = view.SecOverview
				a.toast("设置完成：直接说一句，或到收件箱处理等你的请求", toastOK)
			})
		})
	}
	startOnboardingRootPicker = func(a *App, apply func(string)) {
		a.runOffMain(func() {
			paths, err := openFolderDialog(a.win, "选择项目目录")
			applyPick := func() {
				a.pickingFolder = false
				if err != nil {
					a.onb.err = err.Error()
					return
				}
				if len(paths) > 0 {
					if p := strings.TrimSpace(paths[0]); p != "" {
						apply(p)
					}
				}
			}
			if a.win != nil {
				a.win.Update(applyPick)
				return
			}
			applyPick()
		})
	}
)

type errNoWorkspaceDispatch struct{}

func (errNoWorkspaceDispatch) Error() string {
	return "暂时不可用：运行时还没有接入按项目派活"
}

type errNoTicketRetrier struct{}

func (errNoTicketRetrier) Error() string {
	return "暂时不可用：运行时还没有接入重试"
}

type errNoOnboardingSkip struct{}

func (errNoOnboardingSkip) Error() string {
	return "暂时不可用：运行时还没有接入跳过设置"
}

type errNoOnboardingFinish struct{}

func (errNoOnboardingFinish) Error() string {
	return "暂时不可用：运行时还没有接入完成设置"
}

// toastKind decides the tone of a toast: neutral, success or failure.
type toastKind int

const (
	toastInfo toastKind = iota
	toastOK
	toastErr
)

// toast is one transient line above the composer.
type toast struct {
	text string
	kind toastKind
	at   time.Time
}

// toast queues a message; at most three show at once, newest last.
func (a *App) toast(text string, kind toastKind) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	a.toasts = append(a.toasts, toast{text: text, kind: kind, at: a.now})
	if len(a.toasts) > 3 {
		a.toasts = a.toasts[len(a.toasts)-3:]
	}
}

// toastsView draws the toasts above the composer and drops old ones. It runs on
// the main thread; the expiry uses the frame's clock, so a frame is not scheduled
// just to remove a toast.
func (a *App) toastsView(c *mui.Context) {
	if len(a.toasts) == 0 {
		return
	}
	tk := a.tk
	live := a.toasts[:0]
	for _, t := range a.toasts {
		if !t.at.IsZero() && a.now.Sub(t.at) > 6*time.Second {
			continue
		}
		live = append(live, t)
	}
	a.toasts = live
	for _, t := range a.toasts {
		fg, bg := tk.TextMuted, tk.SurfaceAlt
		switch t.kind {
		case toastOK:
			fg, bg = tk.Green, tk.GreenSoft
		case toastErr:
			fg, bg = tk.Red, tk.RedSoft
		}
		mui.Row(c).Padding(6, 12).Radius(8).Background(bg).Border(1, tk.BorderSoft).
			AlignItems(mui.Center).Gap(8).Children(func() {
			dot(c, fg, 6)
			mui.Text(c, t.text).FontSize(fs(12)).TextColor(fg)
		})
	}
}

// copyValue copies a path or an ID and says so; a failure is reported instead of
// silently doing nothing.
func (a *App) copyValue(text, what string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	if err := copyTextToClipboard(text); err != nil {
		a.toast("复制失败："+err.Error(), toastErr)
		return
	}
	a.toast("已复制"+what, toastOK)
}

// openPath opens a directory or file in the desktop's default application, off
// the frame.
func (a *App) openPath(path string) {
	path = strings.TrimSpace(path)
	if path == "" {
		return
	}
	a.runOffMain(func() {
		if err := openPathWithDefaultApp(path); err != nil {
			a.postToUI(func() { a.toast("打开失败："+err.Error(), toastErr) })
		}
	})
}

// attentionCount is the one number: the runtime's counter when it has one, else
// the overview's derivation.
func (a *App) attentionCount() int {
	if c, ok := a.cmd.(AttentionCounter); ok && c != nil {
		return c.AttentionCount()
	}
	if ov := a.snap.Overview(); ov != nil {
		return ov.PersonCount()
	}
	return 0
}

// watchAttention notifies once when new things start waiting while the window is
// hidden. It compares counts frame to frame; the runtime itself never notifies
// twice.
func (a *App) watchAttention() {
	n := a.attentionCount()
	if n > a.lastAttention && a.hidden {
		body := "有新的请求待你决定"
		if n > 1 {
			body = "有 " + itoa(n) + " 件事待你决定"
		}
		a.toast(body, toastInfo)
		title := "超级Agent · 待你决定"
		a.runOffMain(func() {
			_ = showDesktopNotification(title, body)
		})
	}
	a.lastAttention = n
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
