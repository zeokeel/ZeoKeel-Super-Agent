package ui

import (
	"fmt"
	"testing"
	"time"

	mui "github.com/egoist/mygo/ui"

	"github.com/zeokeel/super-agent/internal/view"
	"github.com/zeokeel/super-agent/internal/view/viewtest"
)

// fakeCmd records what the input handed over, so the tests prove the panel asks
// the runtime instead of doing work itself.
type fakeCmd struct {
	got      []string
	fail     string
	approved []string
	rejected []string
}

func (f *fakeCmd) Dispatch(text string) error {
	if f.fail != "" {
		return fmt.Errorf("%s", f.fail)
	}
	f.got = append(f.got, text)
	return nil
}
func (f *fakeCmd) Approve(decisionID string) error {
	f.approved = append(f.approved, decisionID)
	return nil
}
func (f *fakeCmd) Reject(decisionID string) error {
	f.rejected = append(f.rejected, decisionID)
	return nil
}
func (f *fakeCmd) LastError() string { return "" }

// demoStream is one message from GitHub and one the operator typed, with what the
// agent did about each.
func demoStream() *view.StreamVM {
	now := time.Now()
	return &view.StreamVM{
		Threads: []view.Thread{
			{
				ID: "th-1", Workspace: "ws_webapp",
				Head: view.Line{Kind: view.LineInbound, Who: "lin", Source: "GitHub · #42",
					Text: "登录超时应可配置，默认 30 分钟", At: now},
				Steps: []view.Line{
					{Kind: view.LineDecision, Text: "派发", At: now,
						Badges: []string{"#1", "web", "等待回答"}, Tone: view.ToneWarn},
					{Kind: view.LineQuestion, At: now,
						Text: "配置项名用 session_timeout 还是 session.timeout？", Tone: view.ToneWarn},
				},
			},
			{
				ID: "th-2", Workspace: "ws_data",
				Head: view.Line{Kind: view.LineOperator, Who: "你", Source: "本地面板",
					Text: "整理 acme 上月用量摘要", At: now},
				Steps: []view.Line{
					{Kind: view.LineDecision, Text: "派发", At: now,
						Badges: []string{"#2", "acme_data", "执行中"}, Tone: view.ToneInfo},
				},
			},
		},
	}
}

func newTestApp(t *testing.T, withStream bool) *App {
	t.Helper()
	snap := view.NewSnapshot()
	snap.PublishOverview(viewtest.DemoOverview(time.Now()))
	if withStream {
		snap.PublishStream(demoStream())
	}
	a := newApp(snap, &fakeCmd{})
	a.now = time.Now()
	return a
}

// The stream is the working surface: every message, and what happened to it.
func TestStreamShowsMessagesAndDecisions(t *testing.T) {
	a := newTestApp(t, true)
	tt := mui.NewTester(a.view, 1440, 900)
	if err := tt.Click("消息"); err != nil {
		t.Fatalf("click 消息: %v", err)
	}
	for _, want := range []string{
		"GitHub · #42", "登录超时应可配置，默认 30 分钟",
		"#1", "web", "等待回答",
		"配置项名用 session_timeout 还是 session.timeout？",
		"本地面板", "整理 acme 上月用量摘要",
	} {
		if !tt.HasText(want) {
			t.Fatalf("stream missing %q; texts: %q", want, tt.Texts())
		}
	}
}

// An empty stream invites the first task instead of showing a dashboard.
func TestEmptyStreamInvitesATask(t *testing.T) {
	a := newTestApp(t, false)
	tt := mui.NewTester(a.view, 1440, 900)
	if err := tt.Click("消息"); err != nil {
		t.Fatalf("click 消息: %v", err)
	}
	if !tt.HasText("把事交给它，然后看着它做") {
		t.Fatalf("empty stream missing its line; texts: %q", tt.Texts())
	}
}

// The input is one line and hands the text to the runtime; where the work goes is
// the agent's decision, so no workspace is passed.
func TestInputSubmitsAndClears(t *testing.T) {
	cmd := &fakeCmd{}
	a := newTestApp(t, false)
	a.cmd = cmd
	a.draft = "把登录超时改成可配置"

	tt := mui.NewTester(a.view, 1440, 900)
	if err := tt.Click("发送"); err != nil {
		t.Fatalf("click 发送: %v", err)
	}
	if len(cmd.got) != 1 || cmd.got[0] != "把登录超时改成可配置" {
		t.Fatalf("dispatched %v", cmd.got)
	}
	if a.draft != "" {
		t.Fatalf("the input should clear, still %q", a.draft)
	}
}

// A dispatch refusal is usually "no work: answered or asked back", so the line
// is taken optimistically and the runtime's explanation is shown NEUTRAL, not as
// a red failure (P0-2/P0-7). The draft is cleared: the message was sent.
func TestInputTakesTheLineOnRefusal(t *testing.T) {
	cmd := &fakeCmd{fail: "没有匹配到工作区，请指定一个"}
	a := newTestApp(t, false)
	a.cmd = cmd
	a.draft = "今天中午吃什么"

	tt := mui.NewTester(a.view, 1440, 900)
	if err := tt.Click("发送"); err != nil {
		t.Fatalf("click 发送: %v", err)
	}
	if a.draft != "" {
		t.Fatalf("the line was taken but the draft is %q", a.draft)
	}
	if a.composerErr != "" {
		t.Fatalf("a refusal set a red error %q", a.composerErr)
	}
	if a.composerNote == "" {
		t.Fatal("a refusal said nothing")
	}
	if !tt.HasText("没有匹配到工作区，请指定一个") {
		t.Fatalf("the explanation should be visible; texts: %q", tt.Texts())
	}
}

// The dashboard is the home, and the sidebar switches the monitoring pages.
func TestDashboardIsHomeAndSidebarNavigates(t *testing.T) {
	a := newTestApp(t, true)
	tt := mui.NewTester(a.view, 1440, 900)
	if a.sec != view.SecOverview {
		t.Fatalf("home section %q, want overview", a.sec)
	}
	if !tt.HasText("需要我处理") {
		t.Fatalf("the dashboard did not render; texts: %q", tt.Texts())
	}
	if err := tt.Click("连接"); err != nil {
		t.Fatalf("click 来源: %v", err)
	}
	if a.sec != view.SecSource {
		t.Fatalf("section %q, want source", a.sec)
	}
}

// The dispatch input is on every page, including the dashboard: that entry was
// what the monitoring panel lacked.
func TestDispatchInputIsOnTheDashboard(t *testing.T) {
	a := newTestApp(t, false)
	tt := mui.NewTester(a.view, 1440, 900)
	if err := tt.Click("发送"); err != nil {
		t.Fatalf("the input must be reachable from the dashboard: %v", err)
	}
	if a.cmd == nil {
		t.Fatal("the dashboard must still know how to dispatch")
	}
}

// A publish asks for exactly one repaint.
func TestPublishRequestsOneFrame(t *testing.T) {
	snap := view.NewSnapshot()
	frames := 0
	snap.OnPublish(func() { frames++ })
	snap.PublishOverview(viewtest.DemoOverview(time.Now()))
	snap.PublishStream(demoStream())
	if frames != 2 || snap.Revision() != 2 {
		t.Fatalf("frames=%d rev=%d, want 2/2", frames, snap.Revision())
	}
}
