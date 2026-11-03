// Package ui is the only place that imports mygo. Everything the window draws
// comes from internal/view; everything it changes goes through a command.
//
// Three rules, from design §6.1, apply to every file here:
//  1. A view does no I/O, takes no lock, sleeps never and calls no blocking mygo
//     API. The main thread is the event loop, layout and paint.
//  2. No *mui.Context or element value is stored across frames.
//  3. A mutex is never held while calling something that waits for the main
//     thread. Cross-thread work goes through Window.Update.
package ui

import (
	_ "embed"
	"log"
	"time"

	"github.com/egoist/mygo"
	mui "github.com/egoist/mygo/ui"

	"github.com/zeokeel/super-agent/internal/view"
)

//go:embed assets/logo.png
var logoPNG []byte

// Commander is the one way the window submits work. The UI never calls the engine
// directly: it asks, and the runtime publishes a new projection.
type Commander interface {
	// Dispatch hands one line to the agent. Where the work goes is the agent's
	// decision, not a form the person fills in.
	Dispatch(text string) error
	// Approve and Reject answer a colleague's request: approval is what creates
	// the Ticket, so they are not the same as answering a question (design §4.10).
	Approve(decisionID string) error
	Reject(decisionID string) error
	LastError() string
}

// SettingsRevisionKey is the one reserved key of a SaveSettings values map: the
// configuration revision the edits were based on. The core compares it with its
// own and rejects a stale save ("别人改过了，重新加载") instead of silently
// overwriting another window's change. Implementations must not store it as a
// field.
const SettingsRevisionKey = "__revision"

// SettingsSaver is the optional configuration write channel of the settings page
// (design §6.3: 配置动作只在设置页). It is a separate interface from Commander so a
// runtime that has not wired configuration yet still compiles; the page then says
// "保存通道未接线" instead of pretending the save worked.
//
// The implementation must only enqueue the write and return: the main thread is
// the event loop, layout and paint, and it must not wait for a file or a database
// (design §6.1). It returns the new configuration revision; the page keeps that
// as the base of the next save.
type SettingsSaver interface {
	SaveSettings(group string, values map[string]string) (revision uint64, err error)
}

// Version is what the status bar shows. The release build injects it with
// -X main.version, and the pane says -dev when it is the development build.
var Version = "1.0.0-dev"

// Options configures the window.
type Options struct {
	Title  string
	Width  float32
	Height float32
	Snap   *view.Snapshot
	Cmd    Commander
	// StartSection opens a specific page; empty means the dashboard.
	StartSection string
	// Drain, when set, is how long the app waits for running executors before it
	// really quits. nil means "quit immediately" (tests and headless runs).
	Drain func(timeout time.Duration) bool
}

// App is the window's state. Everything here is read and written on the main
// thread, by the view and by mygo callbacks; nothing else touches it.
type App struct {
	opt   Options
	win   *mygo.Window
	tray  *mygo.Tray
	snap  *view.Snapshot
	cmd   Commander
	sec   view.Section // "" on the workbench, a section when inspecting
	tk    Tokens       // the frame's tokens, derived from the frame's theme
	theme *mui.Theme   // the brand theme of the frame
	quit  bool
	now   time.Time

	// onb is the first-run wizard's state; nil until the wizard is shown.
	onb *onboardingState
	// artifactsOpen is the deliverables panel; artifactWS is its filter.
	artifactsOpen bool
	artifactWS    string
	// draining is set while the app waits for running executors before quitting.
	draining  bool
	drainDone chan struct{}

	// Composer state, main thread only.
	draft       string
	composerErr string

	// Page state, main thread only. A list keeps its choice and its place in the
	// app's state, never in an element stored across frames (design §6.1).
	threadList mui.ListState
	threadSel  int
	// streamList is the workbench's log: virtualized, following its end.
	streamList mui.ListState

	ticketList   mui.ListState
	ticketSel    int
	ticketFilter view.TicketBucket

	// The filtered rows are recomputed only when the published page or the
	// filter changed: the view never sorts or filters while drawing.
	ticketSrc        *view.TicketListVM
	ticketFilterUsed view.TicketBucket
	ticketRows       []view.TicketRow

	evalKind     string
	evalSrc      *view.DecisionVM
	evalKindUsed string
	evalRows     []view.DecisionRow
	evalTable    mui.ListState

	// Settings page state. The edits live in app state until the child publishes
	// values that match them: no optimistic update (design §6.2).
	settingsGroup   view.SettingsGroupID
	settingsEdits   map[string]*string
	settingsErr     string
	settingsEditRev uint64

	// Prompt editor state (设置 › 提示词). Same rule as the settings edits, with
	// one layer edited at a time and a per-layer revision for optimistic
	// concurrency.
	promptEdits    map[string]*string
	promptEditing  view.PromptLayerID
	promptFocus    mui.Handle
	promptErr      string
	promptConflict bool
	promptReloaded uint64

	// Search state (⌘K). Results are a pure function of the published projections
	// and the query, recomputed only when one of them changed: the view never
	// searches while drawing (design §6.4).
	searchOpen      bool
	searchQuery     string
	searchQueryUsed string
	searchSrcRev    uint64
	searchReady     bool
	searchVM        *view.SearchVM
	searchFlat      []view.SearchHit
	searchSel       int // the chosen hit, by index into searchFlat
	paletteList     mui.ListState
	paletteSel      int // the chosen line, by index into the palette's rows

	// locate is the search hit the palette jumped to: the page it names
	// highlights it until the operator navigates elsewhere.
	locate *view.SearchHit

	// Action dialogs and their state (main thread only). The runtime's action
	// channels are probed per use, never stored: the command object cannot change
	// under a running frame.
	answerRow   *view.TicketRow // the 回答 dialog is open for this row
	answerDraft string
	answerErr   string
	stopRow     *view.TicketRow // the 停止 confirmation is open for this row
	// actionErr is the last action's error, shown where the action was offered.
	actionErr string
	// pauseErr is the last pause/resume error, shown in 设置 › 执行.
	pauseErr string

	// helpOpen shows the glossary (the (?) entry and ⌘/).
	helpOpen bool

	// 工作区 sidebar and page state (main thread only). wsExpanded keeps which
	// workspace rows are open; workspaceSel is the workspace 工作区 shows.
	wsExpanded   map[string]bool
	workspaceSel string
	// The 新建工作区 dialog and its draft. pickingFolder is true while the native
	// directory picker is open, so a second click cannot open a second dialog.
	wsModal       bool
	wsDraft       workspaceDraft
	wsKind        int
	wsErr         string
	pickingFolder bool
	// The 新建会话 dialog: its workspace, draft and error.
	convModal bool
	convWS    string
	convDraft string
	convErr   string

	// 设置 › 来源's channel cards. channelChecks holds the last check of each
	// channel (by name) and whether one is running; the runtime's projection
	// supplies the configuration, so editing keeps the settings save protocol.
	channelChecks map[string]channelCheck
	channelErr    string

	// goalFocus is the goal the overview strip jumped to: the 触发 page's goal
	// list highlights it. Empty when no jump is in effect.
	goalFocus string
}

// Run opens the window and blocks until the app quits. It returns nil when
// another instance is already running: the running one is focused instead.
func Run(o Options) error {
	if o.Title == "" {
		o.Title = "ZeoKeel"
	}
	if o.Width == 0 {
		o.Width = 1440
	}
	if o.Height == 0 {
		o.Height = 900
	}
	if o.Snap == nil {
		o.Snap = view.NewSnapshot()
	}
	if !mygo.App.RequestSingleInstanceLock() {
		return nil
	}

	a := newApp(o.Snap, o.Cmd)
	a.opt = o
	if o.StartSection != "" {
		a.sec = view.Section(o.StartSection)
	}

	mygo.App.WhenReady(func() {
		a.win = mygo.NewWindow(mygo.WindowOptions{
			Title:  o.Title,
			Width:  int(o.Width),
			Height: int(o.Height),
			Content: mui.View(func(c *mui.Context) {
				a.now = time.Now()
				a.view(c)
			}),
		})

		// Closing the window hides it: the runtime keeps working, which is what
		// makes unattended operation possible (design §6.7).
		a.win.OnClose(func(e *mygo.CloseEvent) {
			if a.quit {
				return
			}
			e.PreventDefault()
			a.win.Hide()
		})

		// macOS: no Dock icon, the app lives in the menu bar.
		if a.tray = a.newTray(); a.tray != nil {
			mygo.App.SetActivationPolicy(mygo.ActivationPolicyAccessory)
		}
	})

	// The last window closing must not quit the process.
	mygo.App.OnWindowAllClosed(func() {})

	// A second launch focuses this one instead of starting a second runtime.
	mygo.App.OnSecondInstance(func([]string, string) {
		if a.win != nil {
			a.win.Show()
			a.win.Focus()
		}
	})

	// Every publish asks for one frame; the snapshot coalesces bursts.
	a.snap.OnPublish(func() {
		if a.win != nil {
			a.win.Invalidate()
		}
	})

	if err := mygo.App.Run(); err != nil {
		log.Printf("zeokeel: %v", err)
		return err
	}
	return nil
}

// Quit stops the app for real, so the window's close handler lets it through.
//
// Running executors are given a moment to finish first: killing the process in the
// middle of a tool call leaves a half-written session log behind, and the durable
// design would rather recover than corrupt (design §5.8). The wait happens on its
// own goroutine and the actual quit is issued from the UI thread in view(), because
// only that thread may touch the window.
func (a *App) Quit() {
	if a.opt.Drain != nil && !a.draining {
		a.draining = true
		a.drainDone = make(chan struct{})
		go func() {
			a.opt.Drain(10 * time.Second)
			close(a.drainDone)
		}()
		return
	}
	a.quit = true
	mygo.App.Quit()
}

// drainFinished reports whether the executor drain completed, and issues the quit
// from the UI thread when it did.
func (a *App) drainFinished() bool {
	if a.drainDone == nil {
		return false
	}
	select {
	case <-a.drainDone:
		return true
	default:
		return false
	}
}

// view is the whole window: the monitoring pages in a sidebar, the dispatch
// input always at hand, and the status bar. The dashboard is the home; the input
// is what was missing (design §6.3).
func (a *App) view(c *mui.Context) {
	// The frame's theme follows the system's appearance (mygo does that on its
	// own); the brand derives from it every frame, so dark mode and a change of
	// the desktop's preferences are not a second code path (design §6.5).
	a.theme = brandFor(c.Theme().Dark).theme(c.Theme())
	a.tk = tokensFrom(a.theme)
	mui.Themed(c, a.theme)
	tk := a.tk

	// The drain finished on its goroutine; the window may only be touched here.
	if a.drainFinished() {
		a.quit = true
		mygo.App.Quit()
		return
	}

	// The one shortcut that works on every page: ⌘K / Ctrl+K opens the palette
	// over whatever is showing.
	if c.Shortcut(mui.Cmd, mui.KeyK) {
		a.openSearch()
	}
	// ⌘/ (and the status bar's ? entry) opens the glossary.
	if c.Shortcut(mui.Cmd, mui.KeySlash) {
		a.openHelp()
	}

	// A fresh install opens on the wizard, not on a dashboard that says "0 things
	// to do": the agent has to be told what it is for first (design §6.7).
	if ob := a.snap.Onboarding(); ob != nil && ob.Active {
		a.onboardingPage(c, ob)
		return
	}

	mui.Column(c).Fill().AlignItems(mui.Stretch).Background(tk.Bg).Children(func() {
		a.topbar(c)
		mui.Box(c).Height(1).Background(tk.Border)
		mui.Row(c).Grow(1).AlignItems(mui.Stretch).Children(func() {
			a.sidebar(c)
			mui.Box(c).Width(1).Background(tk.Border)
			mui.Column(c).Grow(1).MinWidth(0).AlignItems(mui.Stretch).Children(func() {
				// A page loads its own snapshot once and owns its scrolling: a
				// virtual list needs a bounded height, a form needs its own
				// scroll. The workbench is the exception, it scrolls as one.
				if a.sec == view.SecMessages {
					a.streamPage(c)
					return
				}
				a.page(c, a.snap.Overview())
			})
		})
		mui.Box(c).Height(1).Background(tk.Border)
		// The dispatch entry is always here, on every page: this is the entry the
		// dashboard was missing.
		mui.Column(c).Padding(10, 16).AlignItems(mui.Center).Background(tk.Surface).
			Children(func() {
				mui.Column(c).Width(maxStreamWidth).Children(func() { a.inputBar(c) })
			})
		mui.Box(c).Height(1).Background(tk.Border)
		a.statusbar(c)
	})

	// The overlays paint last: above every page, below nothing. The dialogs
	// only draw when their state says they are open.
	a.searchPalette(c)
	a.artifactsPanel(c)
	a.workspaceDialog(c)
	a.conversationDialog(c)
	a.answerDialog(c)
	a.stopDialog(c)
	a.helpDialog(c)
}

// newApp builds the window state. The two lists' chosen row is a field of the
// app, so their choice survives frames without storing an element: only an index
// and a pure-data list cross frames (design §6.1, rule 2).
func newApp(snap *view.Snapshot, cmd Commander) *App {
	a := &App{
		snap: snap, cmd: cmd, sec: view.SecOverview,
		threadSel: 0, ticketSel: -1,
		onb:           nil,
		settingsGroup: view.SettingsSource,
		settingsEdits: map[string]*string{},
		promptEdits:   map[string]*string{},
		wsExpanded:    map[string]bool{},
		channelChecks: map[string]channelCheck{},
	}
	a.threadList.Selected = &a.threadSel
	a.ticketList.Selected = &a.ticketSel
	a.paletteList.Selected = &a.paletteSel
	return a
}

// page draws the section the rail chose.
func (a *App) page(c *mui.Context, ov *view.OverviewVM) {
	switch a.sec {
	case view.SecOverview:
		// The overview is one long column; it scrolls as a whole.
		mui.Scroll(c).Grow(1).Children(func() { a.overview(c, ov) })
	case view.SecWorkspace:
		a.workspacePage(c)
	case view.SecSource:
		a.sourcePage(c)
	case view.SecTrigger:
		a.triggerPage(c)
	case view.SecEval:
		a.evalPage(c)
	case view.SecExecution:
		a.executionPage(c)
	case view.SecSettings:
		a.settingsPage(c)
	default:
		a.placeholder(c, a.sec)
	}
}

// placeholder is one honest line for a page that does not exist.
func (a *App) placeholder(c *mui.Context, sec view.Section) {
	tk := a.tk
	mui.Column(c).Fill().Padding(32).Gap(8).Children(func() {
		mui.Text(c, labelOf(sec)).FontSize(26).Bold().TextColor(tk.Text)
		mui.Text(c, "这一页不在侧边栏里。").FontSize(15).TextColor(tk.TextMuted)
	})
}

func labelOf(sec view.Section) string { return view.SectionLabel(sec) }
