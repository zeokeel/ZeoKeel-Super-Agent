package ui

// The IM connection wizard: only the chosen transport's fields, validation and a
// check of the TYPED (unsaved) values, authorization/login help, and saving only
// after a real success (or an explicit unverified save).

import (
	"fmt"
	"testing"

	mui "github.com/egoist/mygo/ui"

	"github.com/zeokeel/super-agent/internal/view"
)

// fakeValidator is a runtime adapter stand-in with primitives only.
type fakeValidator struct {
	fakeCmd
	metadata []string
	checked  []map[string]string
	checkFor string
	state    string
	detail   string
	authURL  string
	login    []string
	version  string
	verr     error
	errors   []string
	checkErr error
}

func (f *fakeValidator) ValidateChannelDraft(name string, values map[string]string) []string {
	return f.errors
}

func (f *fakeValidator) CheckChannelDraft(name string, values map[string]string) (string, string, string, error) {
	f.checkFor = name
	f.checked = append(f.checked, values)
	return f.state, f.detail, f.authURL, f.checkErr
}

func (f *fakeValidator) DWSVersionValue() (string, error) { return f.version, f.verr }
func (f *fakeValidator) DWSLoginArgs() []string           { return f.login }
func (f *fakeValidator) IMConnectionState(name string) (string, string, string, string, string, string, bool) {
	return "", "", "", "", "", "", false
}
func (f *fakeValidator) ChannelFieldMetadata() []string { return f.metadata }

// openWizard opens 飞书's wizard from its card.
func openWizard(t *testing.T, cmd Commander) (*App, *mui.Tester) {
	t.Helper()
	a, tt := channelApp(t, cmd)
	click(t, tt, "配置连接 飞书")
	if !a.connOpen {
		t.Fatalf("the wizard did not open; texts: %q", tt.Texts())
	}
	return a, tt
}

// Without the adapter the card says the wizard is unavailable; nothing silently
// does nothing.
func TestChannelWizardRequiresValidator(t *testing.T) {
	a, tt := channelApp(t, &fakeCmd{})
	wantTexts(t, tt, "暂时不可用：运行时还没有接入连接向导")
	_ = tt.Click("配置连接 飞书")
	if a.connOpen {
		t.Fatal("an unwired wizard opened")
	}
}

// Every transport shows only its own fields.
func TestChannelWizardPerTransportFields(t *testing.T) {
	fake := &fakeValidator{state: "ok", detail: "连接可用"}
	_, tt := openWizard(t, fake)

	// ws: chats are optional.
	wantTexts(t, tt, "* App ID", "* App Secret", "监听会话（可选）")
	click(t, tt, "传输：HTTP 长轮询（兜底）")
	wantTexts(t, tt, "* 监听会话（必填）")

	// 钉钉 DWS and Stream have disjoint fields.
	click(t, tt, "取消")
	click(t, tt, "配置连接 钉钉")
	click(t, tt, "传输：DWS（本机命令行）")
	wantTexts(t, tt, "dws 命令", "监听类型", "发送时暂停监听")
	click(t, tt, "传输：官方 Stream（推荐）")
	wantTexts(t, tt, "AppKey", "AppSecret", "RobotCode", "机器人自身 ID（@ 判定）")
	if tt.HasText("dws 命令") {
		t.Fatal("the Stream transport still asks for the DWS command")
	}
}

// 检查连接 tests what is typed, without saving.
func TestChannelWizardCheckUsesTypedValues(t *testing.T) {
	fake := &fakeValidator{state: "ok", detail: "连接可用", version: "dws 0.9.1", login: []string{"dws", "auth", "login"}}
	saver := &fakeSaver{}
	a, tt := openWizard(t, &fakeValidator{})
	_ = a
	_ = tt
	_ = saver

	// Re-open with the recording validator + saver is not possible through the
	// helper; build it directly.
	a2, tt2 := channelApp(t, fake)
	click(t, tt2, "配置连接 飞书")
	// Type values into the drafts the wizard uses.
	for _, f := range []view.ChannelFieldSpec{
		{Key: "feishu_app_id", Label: "App ID"},
		{Key: "feishu_app_secret", Label: "App Secret"},
	} {
		p := a2.connDraft("feishu", a2.connTransport, f)
		*p = "typed-" + f.Key
	}
	click(t, tt2, "检查连接")
	if fake.checkFor != "feishu" || len(fake.checked) != 1 {
		t.Fatalf("check called %q %d times", fake.checkFor, len(fake.checked))
	}
	got := fake.checked[0]
	if got["feishu_app_id"] != "typed-feishu_app_id" || got["feishu_app_secret"] != "typed-feishu_app_secret" {
		t.Fatalf("the check did not receive the typed values: %v", got)
	}
	wantTexts(t, tt2, "连接可用", "DWS 版本：dws 0.9.1", "dws auth login")
}

// needs_auth shows the URL, opens it, and copies the login command.
func TestChannelWizardNeedsAuth(t *testing.T) {
	fake := &fakeValidator{state: "needs_auth", detail: "需要授权", authURL: "https://example.test/auth", login: []string{"dws", "auth", "login"}}
	opened := ""
	oldOpen := startOpenAuthorization
	t.Cleanup(func() { startOpenAuthorization = oldOpen })
	startOpenAuthorization = func(a *App, u string) { opened = u }
	copied := ""
	oldCopy := copyTextToClipboard
	t.Cleanup(func() { copyTextToClipboard = oldCopy })
	copyTextToClipboard = func(s string) error { copied = s; return nil }

	_, tt := openWizard(t, fake)
	click(t, tt, "检查连接")
	wantTexts(t, tt, "需要授权", "https://example.test/auth", "打开授权页", "复制链接", "复制登录命令")
	click(t, tt, "打开授权页")
	if opened != "https://example.test/auth" {
		t.Fatalf("opened %q", opened)
	}
	click(t, tt, "复制登录命令")
	if copied != "dws auth login" {
		t.Fatalf("copied %q", copied)
	}
}

// Saving is disabled until the check succeeds; then it writes the typed values.
func TestChannelWizardSaveOnlyAfterSuccess(t *testing.T) {
	fake := &fakeValidator{state: "ok", detail: "连接可用"}
	saver := &fakeSaver{}
	cmd := &wizardCmd{fakeValidator: fake, saver: saver}
	a, tt := channelApp(t, cmd)
	click(t, tt, "配置连接 飞书")
	// Before checking, the save button exists but is disabled: clicking it must
	// not save anything.
	_ = tt.Click("保存并启用")
	if len(saver.values) != 0 {
		t.Fatalf("saved before a successful check: %v", saver.values)
	}
	p := a.connDraft("feishu", a.connTransport, view.ChannelFieldSpec{Key: "feishu_app_id", Label: "App ID"})
	*p = "cli-typed"
	tt.Frame()
	click(t, tt, "检查连接")
	click(t, tt, "保存并启用")
	if len(saver.values) == 0 {
		t.Fatalf("save after success sent nothing; texts: %q", tt.Texts())
	}
	if saver.values["feishu_enabled"] != "true" || saver.values["feishu_app_id"] != "cli-typed" {
		t.Fatalf("saved %v", saver.values)
	}
	if a.connOpen {
		t.Fatal("the wizard stayed open after a successful save")
	}
}

// A failed check offers only the explicit unverified save, and says so.
func TestChannelWizardForceSaveAfterFailure(t *testing.T) {
	saver := &fakeSaver{}
	fake := &fakeValidator{state: "error", detail: "连不上", checkErr: fmt.Errorf("network down")}
	a, tt := channelApp(t, &wizardCmd{fakeValidator: fake, saver: saver})
	click(t, tt, "配置连接 飞书")
	click(t, tt, "检查连接")
	wantTexts(t, tt, "连不上", "仍然保存（未验证，可能连不上）")
	click(t, tt, "仍然保存（未验证，可能连不上）")
	if len(saver.values) == 0 {
		t.Fatal("the explicit unverified save sent nothing")
	}
	if a.connOpen {
		t.Fatal("the wizard stayed open after the forced save")
	}
}

// 解析器 reads the adapter's metadata lines; the wizard then uses its labels.
func TestChannelFieldMetadataOverridesLabels(t *testing.T) {
	fake := &fakeValidator{
		state: "ok", detail: "连接可用",
		metadata: []string{
			"feishu|longpoll|1|feishu_app_id|text|1|||Adapter AppID|来自适配器",
			"feishu|longpoll|1|feishu_app_secret|secret|1|||Adapter Secret|",
			"feishu|longpoll|1|feishu_chats|text|0|||Adapter Chats|",
		},
	}
	_, tt := openWizard(t, fake)
	click(t, tt, "传输：HTTP 长轮询（兜底）")
	wantTexts(t, tt, "Adapter AppID", "来自适配器", "Adapter Chats")
}

// The pause switch is published in the dingtalk card and saves through the one
// protocol.
func TestDingTalkPauseListenSwitch(t *testing.T) {
	saver := &fakeSaver{}
	_, tt := channelApp(t, saver)
	wantTexts(t, tt, "发送时暂停监听")
	click(t, tt, "发送时暂停监听")
	tt.Frame()
	click(t, tt, "保存")
	if got := saver.values[view.ChannelPauseListenKey]; got == "" {
		t.Fatalf("the pause switch did not reach the save: %v", saver.values)
	}
}

// wizardCmd is Commander + SettingsSaver + ChannelValidator, without the method
// ambiguity of embedding two commands.
type wizardCmd struct {
	*fakeValidator
	saver *fakeSaver
}

func (w *wizardCmd) Dispatch(text string) error { return w.fakeValidator.Dispatch(text) }
func (w *wizardCmd) SaveSettings(group string, values map[string]string) (uint64, error) {
	return w.saver.SaveSettings(group, values)
}
