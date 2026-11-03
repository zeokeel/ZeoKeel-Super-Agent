package ui

import (
	"regexp"
	"strings"

	"github.com/egoist/mygo"
	mui "github.com/egoist/mygo/ui"

	"github.com/zeokeel/super-agent/internal/view"
)

// 设置 › 来源's IM channel cards: 飞书 and 钉钉. The configuration comes from the
// 来源 settings group (enable switch, transport, credentials) so editing and saving
// keep the one settings protocol; the live check comes from the runtime's
// optional ChannelAuthorizer, run off the main thread.

// ChannelAuthorizer checks one IM channel's credentials and subscriptions and,
// when a person must authorize it, returns the URL to open. The runtime
// implements it; the panel never holds the credentials.
type ChannelAuthorizer interface {
	CheckChannel(name string) (state, detail, authURL string, err error)
}

// channelCheck is the last answer of CheckChannel for one channel.
type channelCheck struct {
	pending bool
	state   string // ok | warn | error | unauthorized (what the runtime says)
	detail  string
	authURL string
	err     string
}

// The seams: the check blocks on the runtime (network included) and the browser
// call waits for the main thread, so both run off it and post their answer back.
// Tests replace the start functions to answer at once.
var (
	startChannelCheck = func(a *App, name string) {
		auth, ok := a.cmd.(ChannelAuthorizer)
		go func() {
			if !ok || auth == nil {
				a.postToUI(func() { a.channelCheckDone(name, "", "", "", errNoChannelChecker{}) })
				return
			}
			state, detail, authURL, err := auth.CheckChannel(name)
			a.postToUI(func() { a.channelCheckDone(name, state, detail, authURL, err) })
		}()
	}
	openAuthorizationURL   = func(url string) error { return mygo.Shell.OpenExternal(url) }
	startOpenAuthorization = func(a *App, url string) {
		go func() {
			if err := openAuthorizationURL(url); err != nil {
				a.postToUI(func() { a.channelErr = err.Error() })
			}
		}()
	}
)

type errNoChannelChecker struct{}

func (errNoChannelChecker) Error() string {
	return "动作未接线：运行时未提供 CheckChannel"
}

// postToUI runs fn on the main thread: a window posts through Window.Update, and
// a headless run (no window loop) runs it now. The app's state is main-thread
// only (design §6.1).
func (a *App) postToUI(fn func()) {
	if a.win != nil {
		a.win.Update(fn)
		return
	}
	fn()
}

// channelChecker reports the runtime's check channel, if it has one.
func (a *App) channelChecker() (ChannelAuthorizer, bool) {
	if a.cmd == nil {
		return nil, false
	}
	c, ok := a.cmd.(ChannelAuthorizer)
	return c, ok && c != nil
}

// checkChannelState returns the last check for a channel (zero when none).
func (a *App) checkChannelState(name string) channelCheck {
	if a.channelChecks == nil {
		return channelCheck{}
	}
	return a.channelChecks[name]
}

// checkChannel asks the runtime to check one channel, without blocking the frame.
func (a *App) checkChannel(name string) {
	if _, ok := a.channelChecker(); !ok {
		a.channelErr = "动作未接线：运行时未提供 CheckChannel"
		return
	}
	if c := a.checkChannelState(name); c.pending {
		return
	}
	a.channelErr = ""
	a.channelChecks[name] = channelCheck{pending: true}
	startChannelCheck(a, name)
}

// channelCheckDone applies a check's answer, on the main thread.
func (a *App) channelCheckDone(name, state, detail, authURL string, err error) {
	c := channelCheck{state: state, detail: detail, authURL: authURL}
	if err != nil {
		c.err = err.Error()
	}
	if a.channelChecks == nil {
		a.channelChecks = map[string]channelCheck{}
	}
	a.channelChecks[name] = c
}

// openAuthorization opens the channel's authorization page in the browser, off
// the main thread: the browser call waits for it.
func (a *App) openAuthorization(url string) {
	if strings.TrimSpace(url) == "" {
		return
	}
	if !validAuthURL(url) {
		a.channelErr = "授权链接不是 http(s) 地址，未打开：" + url
		return
	}
	a.channelErr = ""
	startOpenAuthorization(a, url)
}

// channelCards draws one card per IM channel in 设置 › 来源.
func (a *App) channelCards(c *mui.Context, g *view.SettingsGroup) {
	tk := a.tk
	mui.Text(c, "IM 通道").FontSize(fs(13)).Bold().TextColor(tk.Text)
	mui.Text(c, "通道要启用并填好凭据才会监听；空着的通道不连接（不会假装在线）。").
		FontSize(11).TextColor(tk.TextFaint)
	for _, spec := range view.ChannelSpecs() {
		a.channelCard(c, g, spec)
	}
}

// settingFieldOf finds one field of a group by key.
func settingFieldOf(g *view.SettingsGroup, key string) (view.SettingField, bool) {
	for _, f := range g.Fields {
		if f.Key == key {
			return f, true
		}
	}
	return view.SettingField{}, false
}

// channelCard is one channel: state, enable switch, transport, credentials and the
// check/authorize button. Every value comes from the settings group, so an edit
// goes through the existing save protocol (no optimistic update): the page shows
// what the runtime published until the save is accepted.
func (a *App) channelCard(c *mui.Context, g *view.SettingsGroup, spec view.ChannelSpec) {
	tk := a.tk
	enabledField, hasEnabled := settingFieldOf(g, spec.EnabledKey)
	transportField, hasTransport := settingFieldOf(g, spec.TransportKey)
	enabled := hasEnabled && settingBool(*a.settingsPtr(g.ID, enabledField))
	check := a.checkChannelState(spec.Name)

	card(c, tk, 14, func() {
		// Header: which channel, and whether it runs.
		mui.Row(c).Gap(8).AlignItems(mui.Center).Children(func() {
			mui.Icon(c, channelIcon(spec.Name)).FontSize(fs(14)).TextColor(tk.TextMuted)
			mui.Text(c, spec.Label).FontSize(fs(15)).Bold().TextColor(tk.Text)
			state, fg, bg := a.channelState(spec, hasEnabled, enabled, check)
			chip(c, tk, state, fg, bg)
			mui.Spacer(c)
			if hasEnabled {
				p := a.settingsPtr(g.ID, enabledField)
				on := settingBool(*p)
				mui.Row(c).Gap(7).AlignItems(mui.Center).Children(func() {
					mui.Text(c, "启用").FontSize(fs(12)).TextColor(tk.TextMuted)
					if mui.Switch(c.Key("channel:"+spec.Name+":enabled"), &on).
						Label("启用" + spec.Label).Changed() {
						*p = boolText(on)
					}
				})
			} else {
				mui.Text(c, "等待运行时投影："+spec.EnabledKey).FontSize(fs(11)).TextColor(tk.Amber)
			}
		})

		// One honest status line: never "监听中" for a channel that is off.
		detail := a.channelDetail(hasEnabled, enabled, check)
		if detail != "" {
			mui.Text(c, detail).FontSize(fs(11)).TextColor(tk.TextMuted)
		}

		// Transport.
		a.channelTransport(c, g, spec, transportField, hasTransport, enabled)

		// Credentials, in the existing save protocol.
		for _, key := range spec.CredentialKeys {
			f, ok := settingFieldOf(g, key)
			if !ok {
				mui.Text(c, "等待运行时投影："+key).FontSize(fs(11)).TextColor(tk.TextFaint)
				continue
			}
			a.settingField(c, g, f)
		}

		// Check / authorize.
		mui.Row(c).Gap(8).AlignItems(mui.Center).Children(func() {
			label := "检查通道"
			if check.pending {
				label = "检查中…"
			}
			btn := mui.Button(c, label).Label(label).Height(26).FontSize(fs(13))
			if _, wired := a.channelChecker(); !wired {
				btn.Disabled(true)
			} else if check.pending {
				btn.Disabled(true)
			} else if btn.Clicked() {
				a.checkChannel(spec.Name)
			}
			if checkedAt := check; checkedAt.authURL != "" {
				mui.Text(c, "需要打开授权页完成授权").FontSize(fs(11)).TextColor(tk.Amber)
				open := mui.Button(c, "打开授权页").Label("打开授权页").Height(26).FontSize(fs(13))
				if open.Clicked() {
					a.openAuthorization(checkedAt.authURL)
				}
			}
			mui.Spacer(c)
			if _, wired := a.channelChecker(); !wired {
				mui.Text(c, "动作未接线：运行时未提供 CheckChannel").FontSize(fs(11)).TextColor(tk.Amber)
			}
		})
		if check.authURL != "" {
			mui.Text(c, checkedURLText(check.authURL)).FontSize(fs(10)).SingleLine().TextColor(tk.TextFaint)
		}
		if errText := check.err; errText != "" {
			mui.Text(c, errText).FontSize(fs(11)).TextColor(tk.Red)
		}
	})
}

// channelState is the chip of one channel: 未投影, 未启用, 检查中, the check's
// answer, or 未检查. A disabled channel says 未启用, never 监听中.
func (a *App) channelState(spec view.ChannelSpec, hasEnabled, enabled bool, check channelCheck) (string, mui.Color, mui.Color) {
	tk := a.tk
	switch {
	case !hasEnabled:
		return "未投影", tk.TextMuted, tk.SurfaceAlt
	case !enabled:
		return "未启用", tk.TextMuted, tk.SurfaceAlt
	case check.pending:
		return "检查中…", tk.TextMuted, tk.SurfaceAlt
	case check.err != "":
		return "检查失败", tk.Red, tk.RedSoft
	}
	switch check.state {
	case "ok":
		return "检查通过", tk.Green, tk.GreenSoft
	case "warn":
		return "注意", tk.Amber, tk.AmberSoft
	case "unauthorized":
		return "需要授权", tk.Violet, tk.VioletSoft
	case "error":
		return "检查失败", tk.Red, tk.RedSoft
	}
	return "未检查", tk.TextMuted, tk.SurfaceAlt
}

// channelDetail is the one line under a channel's header: what its state means,
// in words that never claim a connection it does not have.
func (a *App) channelDetail(hasEnabled, enabled bool, check channelCheck) string {
	switch {
	case !hasEnabled:
		return "运行时还没有投影这个通道的启用开关与凭据；先等待投影，不会假装在线。"
	case !enabled:
		return "未启用 · 不监听：保存并启用后才会连接。"
	case check.pending:
		return "正在向运行时检查凭据与订阅…"
	case check.err != "":
		return "检查没有完成：" + check.err
	case check.detail != "":
		return check.detail
	}
	return "已启用；还没有检查过，点「检查通道」验证凭据与订阅。"
}

// channelTransport draws the transport selector. A runtime that publishes the
// transport field's Options replaces the build's list (all of them implemented);
// without the field the options are shown with their marks, so a person can see
// what exists without saving a value that cannot run (a not-implemented option
// shows 未实现 and is not selectable).
func (a *App) channelTransport(c *mui.Context, g *view.SettingsGroup, spec view.ChannelSpec,
	field view.SettingField, hasField, enabled bool) {
	tk := a.tk
	options := spec.Options
	if hasField && len(field.Options) > 0 {
		// The runtime decides which transports it offers; the build decides which
		// of those it can actually run. An option the build does not know is shown
		// as 未实现, never selectable: the transport catalog and the runtime must
		// agree before a person can pick one (F-11).
		options = nil
		for _, id := range field.Options {
			opt := view.ChannelOption{ID: id, Label: id, Implemented: false,
				Note: "构建未声明该传输；需要先支持它"}
			if known := findChannelOption(spec, id); known != nil {
				opt = *known
			}
			options = append(options, opt)
		}
	}
	var ptr *string
	if hasField {
		ptr = a.settingsPtr(g.ID, field)
	}
	current := ""
	if ptr != nil {
		current = *ptr
	}

	mui.Row(c).Gap(8).AlignItems(mui.Center).Children(func() {
		mui.Text(c, "传输方式").FontSize(fs(12)).TextColor(tk.TextMuted)
		for _, opt := range options {
			opt := opt
			on := current == opt.ID
			label := opt.Label
			if !opt.Implemented {
				label += "（未实现）"
			}
			row := mui.Row(c).Height(26).Padding(0, 10).Radius(6).Border(1, tk.BorderSoft).
				Background(ifColor(on, tk.AccentSoft, tk.Surface)).AlignItems(mui.Center).
				OnClick(func() {
					if ptr == nil || !opt.Implemented {
						return
					}
					*ptr = opt.ID
				})
			row.Children(func() {
				fg := ifColor(on, tk.Accent, tk.TextMuted)
				if !opt.Implemented {
					fg = tk.TextFaint
				}
				mui.Text(c, label).FontSize(fs(12)).TextColor(fg)
			})
		}
		mui.Spacer(c)
		switch {
		case !hasField:
			mui.Text(c, "等待运行时投影："+spec.TransportKey).FontSize(fs(11)).TextColor(tk.Amber)
		case !enabled:
			mui.Text(c, "未启用时不连接").FontSize(fs(11)).TextColor(tk.TextFaint)
		}
	})
	if hasField && field.Help != "" {
		mui.Text(c, field.Help).FontSize(fs(10)).TextColor(tk.TextFaint)
	}
	if !hasField {
		mui.Text(c, "以上为编译期已知的传输方式；运行时投影 "+spec.TransportKey+" 后才能选择。").
			FontSize(10).TextColor(tk.TextFaint)
	}
}

// findChannelOption returns a known option of a channel, or nil.
func findChannelOption(spec view.ChannelSpec, id string) *view.ChannelOption {
	for i := range spec.Options {
		if spec.Options[i].ID == id {
			return &spec.Options[i]
		}
	}
	return nil
}

// channelIcon is the channel's glyph: 飞书 and 钉钉 have no icon of their own in the
// panel's set, so they use the two shapes that read as "messages from a service".
func channelIcon(name string) *mui.SVG {
	if name == "feishu" {
		return icSpark
	}
	return icBell
}

// boolText is the canonical settings switch encoding. The runtime writes "1"/"0";
// older files may still hold true/false, so reads go through settingBool.
func boolText(on bool) string {
	if on {
		return "1"
	}
	return "0"
}

// settingBool parses any historical switch spelling. One codec at the UI boundary
// keeps a stored "1" from rendering as OFF (design-review V-1).
func settingBool(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on", "enabled":
		return true
	}
	return false
}

// checkedURLText shortens the authorization URL for the line under the button.
func checkedURLText(url string) string {
	if len(url) > 120 {
		return url[:117] + "…"
	}
	return url
}

// authURLPattern is what the panel is willing to hand to the browser: http(s)
// pages only. A runtime that returns anything else gets an error instead of a
// shell command.
var authURLPattern = regexp.MustCompile(`^https?://`)

// validAuthURL reports whether the authorization page is one the panel may open.
func validAuthURL(url string) bool { return authURLPattern.MatchString(strings.TrimSpace(url)) }
