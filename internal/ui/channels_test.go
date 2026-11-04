package ui

import (
	"strings"
	"testing"
	"time"

	mui "github.com/egoist/mygo/ui"

	"github.com/zeokeel/super-agent/internal/view"
	"github.com/zeokeel/super-agent/internal/view/viewtest"
)

// fakeChannelCmd is a runtime stand-in with ChannelAuthorizer: it records which
// channel was checked and answers with the scripted result.
type fakeChannelCmd struct {
	fakeCmd
	checked []string
	state   string
	detail  string
	authURL string
	err     error
}

func (f *fakeChannelCmd) CheckChannel(name string) (string, string, string, error) {
	f.checked = append(f.checked, name)
	return f.state, f.detail, f.authURL, f.err
}

// stubCheck makes the check answer at once instead of on a goroutine.
func stubCheck(t *testing.T, state, detail, authURL string, err error) {
	t.Helper()
	old := startChannelCheck
	t.Cleanup(func() { startChannelCheck = old })
	startChannelCheck = func(a *App, name string) {
		a.channelCheckDone(name, state, detail, authURL, err)
	}
}

// channelApp is 设置 › 来源 with the demo settings (feishu enabled, dingtalk off).
func channelApp(t *testing.T, cmd Commander) (*App, *mui.Tester) {
	t.Helper()
	a := pageApp(t)
	a.cmd = cmd
	tt := mui.NewTester(a.view, 1440, 1600)
	click(t, tt, "设置")
	return a, tt
}

// The two IM channels have a card each: who they are, whether they run, how they
// connect, their credentials, and a check button. The 来源 page draws their
// fields only here.
func TestSourceChannelCards(t *testing.T) {
	_, tt := channelApp(t, &fakeCmd{})
	wantTexts(t, tt,
		"IM 通道", "飞书", "钉钉",
		"HTTP 长轮询", "官方长连接", "DWS 长连接",
		"飞书 App ID", "飞书 App Secret", "飞书会话 ID（逗号分隔）",
		"钉钉 AppKey", "钉钉 AppSecret", "钉钉机器人 RobotCode",
		"检查通道",
	)
	// Enabled 飞书: no check ran yet, so it says it hasn't been checked.
	wantTexts(t, tt, "未检查")
	// Disabled 钉钉: 未启用, and the page does not claim it listens.
	wantTexts(t, tt, "未启用", "未启用 · 不监听")
	for _, claim := range []string{"监听中", "已连接", "在线", "已启用"} {
		if exactTexts(tt, claim) != 0 {
			t.Fatalf("a disabled channel claims %q; texts: %q", claim, tt.Texts())
		}
	}
}

// 检查通道 runs the runtime's check off the main thread and shows its answer.
func TestChannelCheckShowsTheAnswer(t *testing.T) {
	// This stub goes through the runtime (unlike stubCheck, which answers for it),
	// so the test also proves the panel asks the channel it is drawing.
	old := startChannelCheck
	t.Cleanup(func() { startChannelCheck = old })
	startChannelCheck = func(a *App, name string) {
		auth, ok := a.cmd.(ChannelAuthorizer)
		if !ok {
			a.channelCheckDone(name, "", "", "", errNoChannelChecker{})
			return
		}
		state, detail, url, err := auth.CheckChannel(name)
		a.channelCheckDone(name, state, detail, url, err)
	}
	cmd := &fakeChannelCmd{state: "ok", detail: "凭据有效 · 订阅 2 个群"}
	_, tt := channelApp(t, cmd)
	click(t, tt, "检查通道")

	if len(cmd.checked) != 1 || cmd.checked[0] != "feishu" {
		t.Fatalf("checked %v, want [feishu]", cmd.checked)
	}
	wantTexts(t, tt, "检查通过", "凭据有效 · 订阅 2 个群")
}

// A check that needs a person to authorize shows the state and the URL, and the
// page opens it only when clicked, through the browser seam.
func TestChannelCheckNeedsAuthorization(t *testing.T) {
	const url = "https://open.feishu.cn/open-apis/authen/v1/authorize?app_id=cli_x"
	stubCheck(t, "unauthorized", "需要授权才能读取消息", url, nil)
	opened := ""
	oldOpen := startOpenAuthorization
	t.Cleanup(func() { startOpenAuthorization = oldOpen })
	startOpenAuthorization = func(a *App, u string) { opened = u }

	_, tt := channelApp(t, &fakeChannelCmd{})
	click(t, tt, "检查通道")
	wantTexts(t, tt, "需要授权", "打开授权页", url)
	click(t, tt, "打开授权页")
	if opened != url {
		t.Fatalf("opened %q, want the authorization URL", opened)
	}
}

// A non-http authorization URL is never handed to the browser.
func TestChannelAuthorizationURLMustBeHTTP(t *testing.T) {
	stubCheck(t, "unauthorized", "需要授权", "file:///etc/passwd", nil)
	opened := ""
	oldOpen := startOpenAuthorization
	t.Cleanup(func() { startOpenAuthorization = oldOpen })
	startOpenAuthorization = func(a *App, u string) { opened = u }

	_, tt := channelApp(t, &fakeChannelCmd{})
	click(t, tt, "检查通道")
	click(t, tt, "打开授权页")
	if opened != "" {
		t.Fatalf("a non-http URL was opened: %q", opened)
	}
	wantTexts(t, tt, "授权链接不是 http(s) 地址")
}

// A runtime without ChannelAuthorizer gets a disabled check and the reason.
func TestChannelCheckUnwired(t *testing.T) {
	a, tt := channelApp(t, &fakeCmd{})
	wantTexts(t, tt, "暂时不可用：运行时还没有接入 CheckChannel")
	if err := tt.Click("检查通道"); err != nil {
		t.Fatalf("click 检查通道: %v", err)
	}
	if len(a.channelChecks) != 0 {
		t.Fatalf("an unwired check ran: %+v", a.channelChecks)
	}
}

// The enable switch goes through the settings save protocol: it makes the group
// dirty, the save bar appears, and the save carries the channel key.
func TestChannelEnableUsesSaveProtocol(t *testing.T) {
	saver := &fakeSaver{}
	a, tt := channelApp(t, saver)
	click(t, tt, "启用钉钉")
	tt.Frame()
	wantTexts(t, tt, "1 处未保存更改")

	click(t, tt, "保存")
	if got := saver.values["dingtalk_enabled"]; got != "1" {
		t.Fatalf("save carried dingtalk_enabled=%q; values=%v", got, saver.values)
	}
	// No optimistic update: the projection still says off until it is republished.
	ptr := a.settingsPtr(view.SettingsSource, view.SettingField{Key: "dingtalk_enabled"})
	if *ptr != "1" {
		t.Fatalf("the edit was dropped: %q", *ptr)
	}
}

// The transport selector writes the same string the runtime stores, and a
// runtime-published Options list replaces the build's transports as selectable.
func TestChannelTransportSelectsAndSaves(t *testing.T) {
	saver := &fakeSaver{}
	a, tt := channelApp(t, saver)
	click(t, tt, "官方 Stream")
	ptr := a.settingsPtr(view.SettingsSource, view.SettingField{Key: "dingtalk_transport"})
	if *ptr != "stream" {
		t.Fatalf("transport %q, want stream", *ptr)
	}
	tt.Frame()
	click(t, tt, "保存")
	if got := saver.values["dingtalk_transport"]; got != "stream" {
		t.Fatalf("save carried dingtalk_transport=%q; values=%v", got, saver.values)
	}
}

// Without the runtime's transport field the page still shows what this build
// knows, and does not save a transport that the runtime never projected.
func TestChannelTransportNotProjected(t *testing.T) {
	now := time.Now()
	snap := view.NewSnapshot()
	snap.PublishOverview(viewtest.DemoOverview(now))
	snap.PublishSettings(&view.SettingsVM{
		Source: "mygo.json", Revision: 3, UpdatedAt: now,
		Groups: []view.SettingsGroup{{
			ID: view.SettingsSource, Label: "连接", Note: "n",
			Fields: []view.SettingField{
				{Key: "dingtalk_enabled", Label: "钉钉：启用", Value: "1", Kind: view.SettingSwitch},
				{Key: "dingtalk_app_key", Label: "钉钉 AppKey", Value: "k", Kind: view.SettingText},
			},
		}},
	})
	a := newApp(snap, &fakeCmd{})
	a.now = now
	a.sec = view.SecSettings
	a.settingsGroup = view.SettingsSource
	tt := mui.NewTester(a.view, 1440, 1600)
	wantTexts(t, tt, "官方 Stream", "还没有数据：dingtalk_transport")
	// No projected key means no setting to write: clicking changes nothing.
	click(t, tt, "官方 Stream")
	if got := a.settingsDirty(&view.SettingsGroup{ID: view.SettingsSource, Fields: []view.SettingField{
		{Key: "dingtalk_transport", Value: "dws"},
	}}); got != 0 {
		t.Fatalf("an unimplemented transport produced %d edits", got)
	}
}

// A channel the runtime has not projected yet says which keys are missing; it
// never invents credentials.
func TestChannelNotProjected(t *testing.T) {
	now := time.Now()
	snap := view.NewSnapshot()
	snap.PublishOverview(viewtest.DemoOverview(now))
	snap.PublishSettings(&view.SettingsVM{
		Source: "mygo.json", Revision: 2, UpdatedAt: now,
		Groups: []view.SettingsGroup{{
			ID: view.SettingsSource, Label: "连接", Note: "n",
			Fields: []view.SettingField{{Key: "poll_interval", Label: "轮询间隔", Value: "12", Kind: view.SettingText}},
		}},
	})
	a := newApp(snap, &fakeCmd{})
	a.now = now
	a.sec = view.SecSettings
	a.settingsGroup = view.SettingsSource
	tt := mui.NewTester(a.view, 1440, 1600)
	wantTexts(t, tt, "还没有数据：feishu_enabled", "还没有数据：feishu_app_id", "未投影")
}

// Channel cards are on 来源 only: the 执行 group does not repeat them.
func TestChannelCardsOnlyOnSource(t *testing.T) {
	a, tt := channelApp(t, &fakeCmd{})
	a.settingsGroup = view.SettingsExecution
	tt.Frame()
	for _, t2 := range []string{"IM 通道", "飞书 App ID", "钉钉 AppKey"} {
		if exactTexts(tt, t2) != 0 {
			t.Fatalf("执行 drew %q", t2)
		}
	}
	if !strings.Contains(strings.Join(tt.Texts(), "|"), "并发执行器") {
		t.Fatalf("执行 group did not render; texts: %q", tt.Texts())
	}
}
