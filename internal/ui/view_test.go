package ui

import (
	"fmt"
	"testing"
	"time"

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
