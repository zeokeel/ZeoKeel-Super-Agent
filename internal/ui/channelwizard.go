package ui

// IM 连接向导: the connection flow behind a channel card: pick a transport, fill
// ONLY that transport's fields, validate, test the typed values (not the saved
// ones), handle authorization/login, and save only after a successful test — or
// with an explicit "still save" warning. Every runtime capability is optional and
// primitive; without the validator the wizard says so instead of pretending.

import (
	"strings"

	mui "github.com/egoist/mygo/ui"
	"github.com/zeokeel/super-agent/internal/view"
)

// ChannelValidator is the runtime adapter for the connection wizard.
type ChannelValidator interface {
	ValidateChannelDraft(name string, values map[string]string) []string
	CheckChannelDraft(name string, values map[string]string) (state, detail, authURL string, err error)
	DWSVersionValue() (string, error)
	DWSLoginArgs() []string
	IMConnectionState(name string) (state, detail, nextAction, authURL, login, version string, ok bool)
	ChannelFieldMetadata() []string
}

// channelValidator probes the optional adapter.
func (a *App) channelValidator() (ChannelValidator, bool) {
	if a.cmd == nil {
		return nil, false
	}
	v, ok := a.cmd.(ChannelValidator)
	return v, ok && v != nil
}

// channelCheckResult is one draft check's answer, on the main thread.
type channelCheckResult struct {
	state, detail, authURL string
	login, version         string
	errs                   []string
	err                    string
}

// startChannelDraftCheck runs validation + the draft check off the frame; a DWS
// install/version probe runs with it, because it may execute the CLI.
var startChannelDraftCheck = func(a *App, name, transport string, values map[string]string) {
	validator, ok := a.channelValidator()
	a.runOffMain(func() {
		var out channelCheckResult
		if !ok || validator == nil {
			out.err = "暂时不可用：运行时还没有接入连接检查"
			a.postToUI(func() { a.draftCheckDone(out) })
			return
		}
		if errs := validator.ValidateChannelDraft(name, values); len(errs) > 0 {
			out.errs = errs
			a.postToUI(func() { a.draftCheckDone(out) })
			return
		}
		if version, err := validator.DWSVersionValue(); err == nil {
			out.version = version
		}
		out.login = strings.Join(validator.DWSLoginArgs(), " ")
		out.state, out.detail, out.authURL, _ = safeDraftCheck(validator, name, values)
		a.postToUI(func() { a.draftCheckDone(out) })
	})
}

// safeDraftCheck calls the adapter, turning a panic in a misbehaving adapter into
// an error instead of a dead frame.
func safeDraftCheck(v ChannelValidator, name string, values map[string]string) (state, detail, authURL string, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = errChannelCheckPanic{}
			state = "error"
			detail = "连接检查出现了内部错误"
		}
	}()
	return v.CheckChannelDraft(name, values)
}

// draftCheckDone applies a check's answer.
func (a *App) draftCheckDone(out channelCheckResult) {
	a.connChecking = false
	a.connChecked = true
	a.connValidErrs = out.errs
	a.connState = out.state
	a.connDetail = out.detail
	a.connAuthURL = out.authURL
	a.connLogin = out.login
	a.connVersion = out.version
	a.connCheckErr = out.err
	if out.state == "ok" {
		a.connForce = false
	}
}

// openConnectionWizard opens the wizard for a channel, on its published transport
// (or the first one).
func (a *App) openConnectionWizard(name string) {
	spec := view.ChannelSpecOf(name)
	if spec == nil {
		return
	}
	a.connOpen = true
	a.connChannel = name
	a.connTransport = a.defaultTransport(spec)
	a.connChecking = false
	a.connChecked = false
	a.connValidErrs = nil
	a.connState, a.connDetail, a.connAuthURL, a.connLogin, a.connVersion, a.connCheckErr = "", "", "", "", "", ""
	a.connForce = false
	a.connSaving = false
}

// defaultTransport picks the configured transport when it is a real one.
func (a *App) defaultTransport(spec *view.ChannelSpec) string {
	transports := a.channelTransports(spec)
	if len(transports) == 0 {
		return ""
	}
	if g := a.settingsGroupSpec(); g != nil {
		if f, ok := settingFieldOf(g, spec.TransportKey); ok {
			if v := strings.TrimSpace(*a.settingsPtr(g.ID, f)); v != "" {
				if _, ok := view.ChannelTransportOf(spec.Name, v); ok {
					return v
				}
			}
		}
	}
	return transports[0].ID
}

// channelTransports is the transport list of a channel: runtime metadata when the
// adapter publishes it, the view's fallback table otherwise.
func (a *App) channelTransports(spec *view.ChannelSpec) []view.ChannelTransportSpec {
	fallback := view.ChannelTransports(spec.Name)
	validator, ok := a.channelValidator()
	if !ok || validator == nil {
		return fallback
	}
	byKey := parseChannelMetadata(validator.ChannelFieldMetadata(), spec.Name)
	if len(byKey) == 0 {
		return fallback
	}
	out := make([]view.ChannelTransportSpec, 0, len(byKey))
	for _, f := range fallback {
		if fields, ok := byKey[f.ID]; ok {
			f.Fields = fields
		}
		out = append(out, f)
	}
	return out
}

// parseChannelMetadata turns the adapter's lines into per-transport fields.
// Line: channel|transport|implemented(1/0)|key|kind|required(1/0)|default|options(csv)|label|help
func parseChannelMetadata(lines []string, name string) map[string][]view.ChannelFieldSpec {
	out := map[string][]view.ChannelFieldSpec{}
	for _, line := range lines {
		parts := strings.Split(line, "|")
		if len(parts) < 10 || parts[0] != name {
			continue
		}
		if strings.TrimSpace(parts[2]) != "1" {
			continue
		}
		f := view.ChannelFieldSpec{
			Key: parts[3], Kind: parts[4], Required: strings.TrimSpace(parts[5]) == "1",
			Default: parts[6], Label: parts[8], Help: parts[9],
		}
		if opts := strings.TrimSpace(parts[7]); opts != "" {
			for _, o := range strings.Split(opts, ",") {
				if o = strings.TrimSpace(o); o != "" {
					f.Options = append(f.Options, o)
				}
			}
		}
		out[parts[1]] = append(out[parts[1]], f)
	}
	return out
}

// connDraft is the wizard's edit buffer for one field; it starts at the saved
// value so a small fix does not retype everything.
func (a *App) connDraft(name, transport string, f view.ChannelFieldSpec) *string {
	key := name + "|" + transport + "|" + f.Key
	if a.connDrafts == nil {
		a.connDrafts = map[string]*string{}
	}
	if p, ok := a.connDrafts[key]; ok {
		return p
	}
	start := f.Default
	if g := a.settingsGroupSpec(); g != nil {
		if sf, ok := settingFieldOf(g, f.Key); ok {
			start = *a.settingsPtr(g.ID, sf)
		}
	}
	v := start
	a.connDrafts[key] = &v
	return &v
}

// settingsGroupSpec returns the 连接 settings group, when published.
func (a *App) settingsGroupSpec() *view.SettingsGroup {
	vm := a.snap.Settings()
	if vm == nil {
		return nil
	}
	return vm.Group(view.SettingsSource)
}

// currentTransport is the chosen transport with runtime metadata (labels/help/
// required/options) applied when the adapter publishes it.
func (a *App) currentTransport(spec *view.ChannelSpec) (view.ChannelTransportSpec, bool) {
	for _, t := range a.channelTransports(spec) {
		if t.ID == a.connTransport {
			return t, true
		}
	}
	return view.ChannelTransportSpec{}, false
}

// connValues collects the typed values of the chosen transport, with a redundant
// transport field so the adapter reads either convention.
func (a *App) connValues(spec *view.ChannelSpec) map[string]string {
	values := map[string]string{spec.TransportKey: a.connTransport}
	if t, ok := a.currentTransport(spec); ok {
		for _, f := range t.Fields {
			values[f.Key] = strings.TrimSpace(*a.connDraft(spec.Name, a.connTransport, f))
		}
	}
	values["transport"] = a.connTransport
	if spec.Name == "dingtalk" {
		if g := a.settingsGroupSpec(); g != nil {
			if sf, ok := settingFieldOf(g, view.ChannelPauseListenKey); ok {
				values[view.ChannelPauseListenKey] = *a.settingsPtr(g.ID, sf)
			}
		}
	}
	return values
}

// requiredErrors is the inline validation: what the wizard can check without the
// runtime, shown under the fields as the person leaves them.
func (a *App) requiredErrors(spec *view.ChannelSpec) map[string]string {
	out := map[string]string{}
	t, ok := a.currentTransport(spec)
	if !ok {
		return out
	}
	for _, f := range t.Fields {
		if !f.Required {
			continue
		}
		if strings.TrimSpace(*a.connDraft(spec.Name, a.connTransport, f)) == "" {
			out[f.Key] = f.Label + " 必填"
		}
	}
	return out
}

// checkConnectionDraft validates and tests the typed values without saving.
func (a *App) checkConnectionDraft(spec *view.ChannelSpec) {
	if a.connChecking {
		return
	}
	values := a.connValues(spec)
	a.connChecking = true
	a.connChecked = false
	a.connState, a.connDetail, a.connAuthURL, a.connCheckErr = "", "", "", ""
	startChannelDraftCheck(a, spec.Name, a.connTransport, values)
}

// saveConnection writes the typed values only after a successful check (or when
// the person explicitly accepts an unverified save).
func (a *App) saveConnection(spec *view.ChannelSpec, forced bool) {
	if a.connSaving {
		return
	}
	values := a.connValues(spec)
	values[spec.EnabledKey] = "true"
	if !forced && a.connState != "ok" {
		return
	}
	a.connSaving = true
	startSaveSettings(a, string(view.SettingsSource), values, func(rev uint64, err error) {
		a.connSaving = false
		if err != nil {
			a.connCheckErr = err.Error()
			return
		}
		a.connOpen = false
		delete(a.channelChecks, spec.Name) // the next check tests the saved config
		if forced {
			a.toast("已保存（未验证，可能连不上）", toastErr)
		} else {
			a.toast("已保存，正在重启"+spec.Label+"连接", toastOK)
		}
	})
}

// connectionWizard draws the modal for the open channel.
func (a *App) connectionWizard(c *mui.Context) {
	if !a.connOpen {
		return
	}
	spec := view.ChannelSpecOf(a.connChannel)
	if spec == nil {
		a.connOpen = false
		return
	}
	tk := a.tk
	transports := a.channelTransports(spec)
	transport, hasTransport := a.currentTransport(spec)
	_, wired := a.channelValidator()
	open := true
	mui.Modal(c, &open, func() {
		mui.Column(c).Width(680).Gap(10).Children(func() {
			mui.Row(c).Gap(8).AlignItems(mui.Center).Children(func() {
				mui.Icon(c, channelIcon(spec.Name)).FontSize(fs(16)).TextColor(tk.Accent)
				mui.Text(c, "连接"+spec.Label).FontSize(fs(18)).Bold().TextColor(tk.Text)
				mui.Spacer(c)
				mui.Text(c, "检查只测试填写的值，不会保存").FontSize(fs(10)).TextColor(tk.TextFaint)
			})
			// Transport: only the ways this build can run.
			mui.Row(c).Gap(8).Wrap().Children(func() {
				for _, t := range transports {
					t := t
					on := t.ID == a.connTransport
					label := t.Label
					if !t.Implemented {
						label += "（未实现）"
					}
					btn := mui.Button(c, label).Label("传输：" + t.Label).Height(28).FontSize(fs(12))
					if !t.Implemented {
						btn.Disabled(true)
					} else if on {
						btn.Disabled(true) // the chosen one reads as selected, not clickable
					} else if btn.Clicked() {
						a.connTransport = t.ID
						a.connChecked = false
						a.connState, a.connDetail, a.connAuthURL = "", "", ""
					}
				}
			})
			if !hasTransport {
				mui.Text(c, "这个传输方式还没有字段定义").FontSize(fs(12)).TextColor(tk.Amber)
				return
			}
			reqErrs := a.requiredErrors(spec)
			for _, f := range transport.Fields {
				a.connField(c, spec, transport, f, reqErrs[f.Key])
			}
			// Inline validation (required fields) and the runtime's own answers.
			for _, f := range transport.Fields {
				if msg := reqErrs[f.Key]; msg != "" {
					mui.Text(c, "· "+msg).FontSize(fs(11)).TextColor(tk.Amber)
				}
			}
			// Status of the last check.
			a.connStatus(c)
			// Actions.
			mui.Row(c).Gap(8).AlignItems(mui.Center).Children(func() {
				checkLabel := "检查连接"
				if a.connChecking {
					checkLabel = "检查中…"
				}
				check := mui.PrimaryButton(c, checkLabel).Label(checkLabel).Height(30).FontSize(fs(13))
				if !wired || a.connChecking {
					check.Disabled(true)
				} else if check.Clicked() {
					a.checkConnectionDraft(spec)
				}
				mui.Spacer(c)
				if mui.Button(c, "取消").Label("取消").Clicked() {
					a.connOpen = false
				}
				saveLabel := "保存并启用"
				if a.connSaving {
					saveLabel = "保存中…"
				}
				save := mui.PrimaryButton(c, saveLabel).Label(saveLabel).Height(30).FontSize(fs(13))
				canSave := a.connState == "ok" || a.connForce
				if !wired || a.connSaving || !canSave {
					save.Disabled(true)
				} else if save.Clicked() {
					a.saveConnection(spec, a.connState != "ok")
				}
			})
			if a.connChecked && a.connState != "ok" {
				force := mui.Button(c, "仍然保存（未验证，可能连不上）").
					Label("仍然保存（未验证）").Height(26).FontSize(fs(11))
				if a.connSaving {
					force.Disabled(true)
				} else if force.Clicked() {
					a.connForce = true
					a.saveConnection(spec, true)
				}
				mui.Text(c, "没有通过检查就保存，渠道可能连不上；保存后仍可再来检查。").
					FontSize(fs(10)).TextColor(tk.TextFaint)
			}
			if !wired {
				mui.Text(c, "暂时不可用：运行时还没有接入连接检查（ChannelValidator）；字段可看，但检查不能跑").
					FontSize(fs(11)).TextColor(tk.Amber)
			}
			if a.connCheckErr != "" {
				mui.Text(c, a.connCheckErr).FontSize(fs(11)).TextColor(tk.Red)
			}
		})
	})
	if !open {
		a.connOpen = false
	}
}

// connField is one wizard input: only the chosen transport's fields are drawn.
func (a *App) connField(c *mui.Context, spec *view.ChannelSpec, transport view.ChannelTransportSpec, f view.ChannelFieldSpec, reqErr string) {
	tk := a.tk
	label := f.Label
	if f.Required {
		label = "* " + label
	}
	mui.Column(c).Gap(3).Children(func() {
		mui.Row(c).Gap(6).AlignItems(mui.Center).Children(func() {
			mui.Text(c, label).FontSize(fs(12)).TextColor(tk.TextMuted)
			if f.Help != "" {
				mui.Text(c, "· "+f.Help).FontSize(fs(10)).SingleLine().TextColor(tk.TextFaint)
			}
		})
		ptr := a.connDraft(spec.Name, transport.ID, f)
		switch f.Kind {
		case "switch":
			on := settingBool(*ptr)
			if mui.Switch(c.Key("conn:"+spec.Name+":"+f.Key), &on).Label(f.Label).Changed() {
				*ptr = boolText(on)
			}
		case "select":
			idx := 0
			for i, o := range f.Options {
				if o == *ptr {
					idx = i
				}
			}
			if len(f.Options) > 0 {
				if mui.Segmented(c.Key("conn:"+spec.Name+":"+f.Key), &idx, f.Options...).Label(f.Label).Changed() {
					*ptr = f.Options[idx]
				}
			}
		default:
			in := mui.TextInput(c.Key("conn:"+spec.Name+":"+f.Key), ptr).
				Height(30).FontSize(fs(14)).Label("输入 " + f.Label)
			if f.Kind == "secret" {
				in.Password()
			}
		}
		if reqErr != "" {
			mui.Text(c, reqErr).FontSize(fs(10)).TextColor(tk.Amber)
		}
	})
}

// connStatus is the one status area of the wizard: pending, real state (never a
// fake 已连接), auth/login/version help.
func (a *App) connStatus(c *mui.Context) {
	tk := a.tk
	if a.connChecking {
		spinner(c, tk, "正在测试填写的配置…")
		return
	}
	if !a.connChecked {
		return
	}
	if a.connState == "" && len(a.connValidErrs) == 0 {
		return
	}
	if len(a.connValidErrs) > 0 {
		mui.Text(c, "先修正这些再检查：").FontSize(fs(12)).TextColor(tk.Amber)
		for _, e := range a.connValidErrs {
			mui.Text(c, "· "+e).FontSize(fs(11)).TextColor(tk.Amber)
		}
		return
	}
	fg := tk.Red
	switch a.connState {
	case "ok":
		fg = tk.Green
	case "needs_auth":
		fg = tk.Amber
	}
	mui.Row(c).Gap(6).AlignItems(mui.Center).Children(func() {
		dot(c, fg, 6)
		mui.Text(c, a.connDetail).FontSize(fs(12)).TextColor(fg)
	})
	if a.connAuthURL != "" {
		mui.Row(c).Gap(8).AlignItems(mui.Center).Children(func() {
			mui.Text(c, checkedURLText(a.connAuthURL)).FontSize(fs(10)).SingleLine().
				TextColor(tk.TextMuted)
			if b := mui.Button(c, "打开授权页").Height(24).FontSize(fs(11)); b.Clicked() {
				a.openAuthorization(a.connAuthURL)
			}
			if b := mui.Button(c, "复制链接").Height(24).FontSize(fs(11)); b.Clicked() {
				a.copyValue(a.connAuthURL, "授权链接")
			}
		})
	}
	if a.connVersion != "" {
		mui.Text(c, "DWS 版本："+a.connVersion).FontSize(fs(11)).TextColor(tk.TextMuted)
	}
	if a.connLogin != "" {
		mui.Row(c).Gap(8).AlignItems(mui.Center).Children(func() {
			mui.Text(c, a.connLogin).FontSize(fs(11)).Font("monospace").SingleLine().
				TextColor(tk.TextMuted).Tooltip(a.connLogin)
			if b := mui.Button(c, "复制登录命令").Height(24).FontSize(fs(11)); b.Clicked() {
				a.copyValue(a.connLogin, "登录命令")
			}
		})
	}
	if a.connState != "ok" && a.connVersion == "" && strings.Contains(a.connDetail, "dws") {
		mui.Text(c, "没有检测到可用的 dws：先安装并登录（复制上面的命令），再回来检查。").
			FontSize(fs(10)).TextColor(tk.Amber)
	}
}

// dingtalkPauseSwitch is the `dingtalk_pause_listen_on_send` switch: it pauses
// listening while sending so the bot does not read its own messages.
func (a *App) dingtalkPauseSwitch(c *mui.Context, g *view.SettingsGroup) {
	tk := a.tk
	f, ok := settingFieldOf(g, view.ChannelPauseListenKey)
	if !ok {
		mui.Text(c, "还没有数据：发送时暂停监听（"+view.ChannelPauseListenKey+"）").
			FontSize(fs(10)).TextColor(tk.TextFaint)
		return
	}
	p := a.settingsPtr(g.ID, f)
	mui.Row(c).Gap(8).AlignItems(mui.Center).Children(func() {
		on := settingBool(*p)
		if mui.Switch(c.Key("channel:pause-listen"), &on).Label("发送时暂停监听").Changed() {
			*p = boolText(on)
		}
		mui.Text(c, "发送时暂停监听").FontSize(fs(12)).TextColor(tk.TextMuted)
		mui.Text(c, "· 防止机器人读到自己刚发的消息").FontSize(fs(10)).TextColor(tk.TextFaint)
	})
}

type errChannelCheckPanic struct{}

func (errChannelCheckPanic) Error() string { return "连接检查崩溃" }
