package runtime

// IM channel management (im-channel-spec §1/§2/§4): each channel (feishu,
// dingtalk) has its own lifecycle, configured from settings/flags. Disabled means
// no listener and an honest "未启用" row; half configuration is an error row with
// actionable detail; a change on save restarts only the affected channel.
//
// Both channels share one shape: a poller (feishu long-poll, dingtalk stream, or
// DWS CLI) that yields im.RawEvents, which become Triggers; a cursor persisted in
// kv only after the whole batch was handed over; and health/status projections.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/zeokeel/super-agent/internal/domain"
	"github.com/zeokeel/super-agent/internal/engine"
	"github.com/zeokeel/super-agent/internal/im"
	"github.com/zeokeel/super-agent/internal/im/dingtalk"
	"github.com/zeokeel/super-agent/internal/im/feishu"
	"github.com/zeokeel/super-agent/internal/store"
	"github.com/zeokeel/super-agent/internal/view"
)

// IMConfig is one snapshot of the two channels' configuration.
type IMConfig struct {
	FeishuEnabled   bool
	FeishuTransport string
	FeishuAppID     string
	FeishuAppSecret string
	FeishuChats     string
	SelfID          string

	DingEnabled   bool
	DingTransport string
	DingAppKey    string
	DingAppSecret string
	DingRobot     string
	DingBotID     string
	DWSCmd        string
}

// IMConfigFromSettings reads the settings snapshot (keys with the "source."
// prefix) and fills the channel model. Defaults follow the spec: disabled,
// feishu longpoll, dingtalk dws with command "dws".
func IMConfigFromSettings(values map[string]string) IMConfig {
	get := func(key string) string { return strings.TrimSpace(values["source."+key]) }
	cfg := IMConfig{
		FeishuEnabled:   get("feishu_enabled") == "1",
		FeishuTransport: orDefault(get("feishu_transport"), "longpoll"),
		FeishuAppID:     get("feishu_app_id"),
		FeishuAppSecret: get("feishu_app_secret"),
		FeishuChats:     get("feishu_chats"),
		SelfID:          get("im_self"),
		DingEnabled:     get("dingtalk_enabled") == "1",
		DingTransport:   orDefault(get("dingtalk_transport"), "dws"),
		DingAppKey:      get("dingtalk_app_key"),
		DingAppSecret:   get("dingtalk_app_secret"),
		DingRobot:       get("dingtalk_robot"),
		DingBotID:       get("dingtalk_bot_id"),
		DWSCmd:          orDefault(get("dingtalk_dws_cmd"), "dws"),
	}
	if cfg.DingBotID == "" {
		cfg.DingBotID = cfg.SelfID
	}
	return cfg
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// imOptionsForFlags overlays command-line credentials on the stored config: a
// flag is what the person typed for this run, settings are the fallback.
type IMFlags struct {
	FeishuAppID, FeishuAppSecret, FeishuChats string
	DingAppKey, DingAppSecret, DingRobot      string
	SelfID                                    string
}

// WithFlags returns the config with flag values taking precedence.
func (c IMConfig) WithFlags(f IMFlags) IMConfig {
	pick := func(flag, base string) string {
		if strings.TrimSpace(flag) != "" {
			return strings.TrimSpace(flag)
		}
		return base
	}
	c.FeishuAppID = pick(f.FeishuAppID, c.FeishuAppID)
	c.FeishuAppSecret = pick(f.FeishuAppSecret, c.FeishuAppSecret)
	c.FeishuChats = pick(f.FeishuChats, c.FeishuChats)
	c.DingAppKey = pick(f.DingAppKey, c.DingAppKey)
	c.DingAppSecret = pick(f.DingAppSecret, c.DingAppSecret)
	c.DingRobot = pick(f.DingRobot, c.DingRobot)
	c.SelfID = pick(f.SelfID, c.SelfID)
	if c.DingBotID == "" {
		c.DingBotID = c.SelfID
	}
	return c
}

func (c IMConfig) feishuConfigured() bool {
	return c.FeishuAppID != "" || c.FeishuAppSecret != "" || c.FeishuChats != ""
}
func (c IMConfig) dingConfigured() bool {
	return c.DingAppKey != "" || c.DingAppSecret != "" || c.DingRobot != "" ||
		c.DingTransport == "dws" // DWS may manage its own login
}

// IMManagerOptions lets the app (and tests) inject drivers, the sender registry
// and the endpoints.
type IMManagerOptions struct {
	PollInterval time.Duration
	// RegisterSender hands the channel's Sender to the outbox router.
	RegisterSender func(kind string, s im.Sender)
	// BuildFeishu / BuildDingtalk replace the real drivers in tests.
	BuildFeishu   func(IMConfig) (im.Provider, error)
	BuildDingtalk func(IMConfig) (im.Provider, error)
	// DWS is the dingtalk DWS client; nil means the command implementation.
	DWS           dingtalk.DWSClient
	FeishuBaseURL string
	Logger        *slog.Logger
}

// imChannel is one running channel.
type imChannel struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// IMManager owns the channels' lifecycle.
type IMManager struct {
	rt   *Runtime
	opts IMManagerOptions

	mu    sync.Mutex
	ctx   context.Context
	cfg   IMConfig
	chans map[string]*imChannel
	// cursors is the memory fallback when no store is attached; with a store the
	// cursor lives in kv under "im.cursor.<channel>".
	cursors map[string]string
}

// NewIMManager builds a manager for one Runtime. Start/Apply do the wiring.
func NewIMManager(rt *Runtime, opts IMManagerOptions) *IMManager {
	if opts.PollInterval <= 0 {
		opts.PollInterval = 2 * time.Second
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	return &IMManager{rt: rt, opts: opts, chans: map[string]*imChannel{}, cursors: map[string]string{}}
}

// ChannelStates are the UI's words for one channel. They mirror the source page.
const (
	IMReady    = "ready"
	IMDisabled = "disabled"
	IMAuth     = "auth"
	IMError    = "error"
)

// Start applies the configuration for the first time.
func (m *IMManager) Start(ctx context.Context, cfg IMConfig) {
	m.mu.Lock()
	m.ctx = ctx
	before := m.cfg
	m.cfg = cfg
	m.mu.Unlock()
	for _, ch := range []string{"feishu", "dingtalk"} {
		if channelChanged(before, cfg, ch) {
			m.applyChannel(ch, cfg)
		}
	}
}

// ApplyIMSettings diffs the old and new configuration and restarts only the
// channels whose enable/transport/credentials changed. It is called after a
// settings save committed and at startup.
func (m *IMManager) ApplyIMSettings(before, after IMConfig) {
	m.mu.Lock()
	m.cfg = after
	m.mu.Unlock()
	for _, ch := range []string{"feishu", "dingtalk"} {
		if channelChanged(before, after, ch) {
			m.applyChannel(ch, after)
		}
	}
}

// Config returns the current configuration (tests/UI facts).
func (m *IMManager) Config() IMConfig {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cfg
}

// Stop cancels every channel and waits up to 5s for each to finish.
func (m *IMManager) Stop() {
	m.mu.Lock()
	chans := m.chans
	m.chans = map[string]*imChannel{}
	m.mu.Unlock()
	for name, ch := range chans {
		ch.cancel()
		select {
		case <-ch.done:
		case <-time.After(5 * time.Second):
			m.opts.Logger.Warn("IM channel did not stop in time", "channel", name)
		}
	}
}

// channelChanged reports whether one channel needs a restart.
func channelChanged(a, b IMConfig, ch string) bool {
	if ch == "feishu" {
		return a.FeishuEnabled != b.FeishuEnabled ||
			a.FeishuTransport != b.FeishuTransport ||
			a.FeishuAppID != b.FeishuAppID || a.FeishuAppSecret != b.FeishuAppSecret ||
			a.FeishuChats != b.FeishuChats || a.SelfID != b.SelfID
	}
	return a.DingEnabled != b.DingEnabled ||
		a.DingTransport != b.DingTransport ||
		a.DingAppKey != b.DingAppKey || a.DingAppSecret != b.DingAppSecret ||
		a.DingRobot != b.DingRobot || a.DingBotID != b.DingBotID ||
		a.DWSCmd != b.DWSCmd || a.SelfID != b.SelfID
}

// applyChannel stops the old runner and starts the new configuration.
func (m *IMManager) applyChannel(ch string, cfg IMConfig) {
	m.mu.Lock()
	old := m.chans[ch]
	delete(m.chans, ch)
	ctx := m.ctx
	m.mu.Unlock()
	if old != nil {
		old.cancel()
		select {
		case <-old.done:
		case <-time.After(5 * time.Second):
			m.opts.Logger.Warn("IM channel did not stop in time", "channel", ch)
		}
	}
	if ctx == nil {
		ctx = context.Background()
	}

	enabled, half, problem := channelReadiness(cfg, ch)
	label := "飞书"
	if ch == "dingtalk" {
		label = "钉钉"
	}
	if !enabled {
		if half {
			m.setStatus(ch, label, false, false, "配置不完整", problem)
			m.rt.SetComponentHealth("im:"+ch, "error", problem)
			return
		}
		// Disabled: no listener, no "online" claim; health says it is off.
		m.setStatus(ch, label, false, true, "未启用", "")
		m.rt.SetComponentHealth("im:"+ch, "ready", "未启用")
		return
	}

	drv, err := m.buildDriver(ch, cfg)
	if err != nil {
		m.setStatus(ch, label, false, false, "启动失败", err.Error())
		m.rt.SetComponentHealth("im:"+ch, "error", err.Error())
		m.opts.Logger.Error("IM driver failed to start", "channel", ch, "error", err)
		return
	}
	if m.opts.RegisterSender != nil {
		if s, ok := drv.(im.Sender); ok {
			m.opts.RegisterSender(ch, s)
		}
	}
	selfID := cfg.SelfID
	if ch == "dingtalk" {
		selfID = cfg.DingBotID
	}
	if selfID == "" {
		if id, err := drv.Self(ctx); err == nil {
			selfID = id.ID
		}
	}
	// Polling is the preferred shape (feishu long-poll, dingtalk stream/DWS). A
	// Receiver-only driver (the in-memory fakes, a future socket driver) gets a
	// subscribe loop instead of being rejected.
	poller, canPoll := drv.(im.Poller)
	receiver, canReceive := drv.(im.Receiver)
	if !canPoll && !canReceive {
		m.setStatus(ch, label, false, false, "驱动不支持接收", fmt.Sprintf("%T 没有 Poll/Subscribe", drv))
		m.rt.SetComponentHealth("im:"+ch, "error", "驱动不支持接收")
		return
	}
	cctx, cancel := context.WithCancel(ctx)
	run := &imChannel{cancel: cancel, done: make(chan struct{})}
	m.mu.Lock()
	m.chans[ch] = run
	m.mu.Unlock()
	m.setStatus(ch, label, true, true, "启动中", "")
	m.rt.SetComponentHealth("im:"+ch, "starting", "连接中")
	m.opts.Logger.Info("IM channel started", "channel", ch, "transport", transportOf(cfg, ch))
	if canPoll {
		go m.pollLoop(cctx, ch, label, cfg, drv, poller, selfID, run.done)
	} else {
		go m.subscribeLoop(cctx, ch, label, receiver, selfID, run.done)
	}
}

func transportOf(cfg IMConfig, ch string) string {
	if ch == "feishu" {
		return cfg.FeishuTransport
	}
	return cfg.DingTransport
}

// channelReadiness decides enabled/half/problem for one channel.
func channelReadiness(cfg IMConfig, ch string) (enabled, half bool, problem string) {
	if ch == "feishu" {
		if !cfg.FeishuEnabled {
			return false, false, ""
		}
		switch cfg.FeishuTransport {
		case "longpoll":
		case "ws", "webhook":
			return false, true, "飞书 " + cfg.FeishuTransport + " 传输尚未实现；请选择 longpoll"
		default:
			return false, true, "未知的飞书传输 " + cfg.FeishuTransport
		}
		var missing []string
		if cfg.FeishuAppID == "" {
			missing = append(missing, "app id")
		}
		if cfg.FeishuAppSecret == "" {
			missing = append(missing, "app secret")
		}
		if len(missing) > 0 {
			return false, true, "飞书 配置不完整：缺少 " + strings.Join(missing, "、")
		}
		return true, false, ""
	}
	if !cfg.DingEnabled {
		return false, false, ""
	}
	switch cfg.DingTransport {
	case "dws":
		if cfg.DWSCmd == "" {
			return false, true, "钉钉 DW​S 可执行文件未配置"
		}
		return true, false, ""
	case "stream":
		var missing []string
		if cfg.DingAppKey == "" {
			missing = append(missing, "app key")
		}
		if cfg.DingAppSecret == "" {
			missing = append(missing, "app secret")
		}
		if cfg.DingRobot == "" {
			missing = append(missing, "robot code")
		}
		if len(missing) > 0 {
			return false, true, "钉钉 Stream 配置不完整：缺少 " + strings.Join(missing, "、")
		}
		return true, false, ""
	}
	return false, true, "未知的钉钉传输 " + cfg.DingTransport
}

// buildDriver constructs the real driver for a channel (test hooks override it).
func (m *IMManager) buildDriver(ch string, cfg IMConfig) (im.Provider, error) {
	if ch == "feishu" {
		if m.opts.BuildFeishu != nil {
			return m.opts.BuildFeishu(cfg)
		}
		return feishu.New(feishu.Config{
			AppID: cfg.FeishuAppID, AppSecret: cfg.FeishuAppSecret,
			Chats: splitFeishuChats(cfg.FeishuChats), SelfID: cfg.SelfID, SelfName: cfg.SelfID,
		})
	}
	if m.opts.BuildDingtalk != nil {
		return m.opts.BuildDingtalk(cfg)
	}
	if cfg.DingTransport == "stream" {
		return dingtalk.New(dingtalk.Config{
			AppKey: cfg.DingAppKey, AppSecret: cfg.DingAppSecret,
			RobotCode: cfg.DingRobot, BotName: "ZeoKeel",
		})
	}
	client := m.opts.DWS
	if client == nil {
		client = dingtalk.NewCommandDWSClient(dingtalk.DWSConfig{Command: cfg.DWSCmd})
	}
	return newDWSPoller(cfg, client), nil
}

// pollLoop is the channel's receive loop: poll, hand every event to the engine,
// then persist the cursor. A failed batch leaves the cursor alone.
func (m *IMManager) pollLoop(ctx context.Context, ch, label string, cfg IMConfig,
	drv im.Provider, poller im.Poller, selfID string, done chan struct{}) {
	defer close(done)
	backoff := time.Second
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(m.opts.PollInterval):
		}
		cursor := m.cursor(ch)
		events, next, err := poller.Poll(ctx, cursor)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			state, detail := classifyIMError(err)
			m.setStatus(ch, label, true, false, "", detail)
			m.rt.SetComponentHealth("im:"+ch, state, detail)
			m.opts.Logger.Warn("IM poll failed", "channel", ch, "error", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			if backoff < 30*time.Second {
				backoff *= 2
			}
			continue
		}
		backoff = time.Second
		failed := false
		for _, ev := range events {
			if ev.Unsupported {
				continue
			}
			if _, err := m.rt.ReceiveTrigger(imTrigger(ch, ev, selfID)); err != nil &&
				!errors.Is(err, engine.ErrDuplicate) {
				m.opts.Logger.Warn("IM message rejected", "channel", ch, "error", err)
				failed = true
			}
		}
		if failed {
			continue // cursor stays: the batch will be retried rather than skipped
		}
		if next != "" && next != cursor {
			if err := m.setCursor(ch, next); err != nil {
				m.opts.Logger.Warn("IM cursor could not be saved", "channel", ch, "error", err)
			}
		}
		m.setStatus(ch, label, true, true, fmt.Sprintf("轮询正常（%d 条）", len(events)), "")
		m.rt.SetComponentHealth("im:"+ch, "ready", fmt.Sprintf("最近一轮 %d 条", len(events)))
	}
}

// subscribeLoop is the Receiver-shaped channel: events arrive on a channel, no
// cursor to persist (the driver owns its position).
func (m *IMManager) subscribeLoop(ctx context.Context, ch, label string,
	recv im.Receiver, selfID string, done chan struct{}) {
	defer close(done)
	events := make(chan im.RawEvent, 32)
	errCh := make(chan error, 1)
	go func() { errCh <- recv.Subscribe(ctx, events) }()
	m.setStatus(ch, label, true, true, "已订阅", "")
	m.rt.SetComponentHealth("im:"+ch, "ready", "已订阅")
	for {
		select {
		case <-ctx.Done():
			return
		case err := <-errCh:
			if ctx.Err() != nil || err == nil {
				return
			}
			state, detail := classifyIMError(err)
			m.setStatus(ch, label, false, false, "", detail)
			m.rt.SetComponentHealth("im:"+ch, state, detail)
			m.opts.Logger.Warn("IM subscribe stopped", "channel", ch, "error", err)
			return
		case ev := <-events:
			if ev.Unsupported {
				continue
			}
			if _, err := m.rt.ReceiveTrigger(imTrigger(ch, ev, selfID)); err != nil &&
				!errors.Is(err, engine.ErrDuplicate) {
				m.opts.Logger.Warn("IM message rejected", "channel", ch, "error", err)
				continue
			}
			m.setStatus(ch, label, true, true, "收到消息 "+ev.ID, "")
			m.rt.SetComponentHealth("im:"+ch, "ready", "收到消息")
		}
	}
}

// imTrigger converts one IM event into a Trigger (same shape as the GitHub one).
func imTrigger(source string, ev im.RawEvent, selfID string) domain.Trigger {
	tr := domain.Trigger{
		ID:           domain.Key(domain.WorkspaceScope(""), source, "trigger", ev.ID),
		Source:       source,
		Human:        !ev.Actor.Bot,
		Addressed:    im.AddressedFor(ev, selfID),
		Threads:      []domain.ThreadRef{{Source: source, ID: ev.Conv.ID}},
		Conversation: ev.Conv,
		Actor:        ev.Actor,
		Channel:      domain.ChannelRef{Source: source, Thread: ev.Conv.ID},
		Text:         ev.Text,
		At:           time.UnixMilli(ev.At),
	}
	if tr.At.IsZero() {
		tr.At = time.Now()
	}
	return tr
}

// classifyIMError turns a driver error into a health state + actionable detail.
func classifyIMError(err error) (state, detail string) {
	msg := err.Error()
	lower := strings.ToLower(msg)
	if errors.Is(err, im.ErrUnsupported) {
		return IMError, "该驱动不支持接收：" + msg
	}
	for _, marker := range []string{"401", "403", "unauthorized", "invalid_token", "token expired", "loggedin\":false", "not logged in"} {
		if strings.Contains(lower, marker) {
			return IMAuth, "需要重新授权：" + msg
		}
	}
	return IMError, msg
}

// cursor reads the channel cursor from kv, falling back to memory.
func (m *IMManager) cursor(ch string) string {
	if st := m.store(); st != nil {
		if v, ok, err := st.Get("im.cursor." + ch); err == nil && ok {
			return v
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cursors[ch]
}

// setCursor persists only after a batch was fully handed to the engine.
func (m *IMManager) setCursor(ch, value string) error {
	if value == "" {
		return nil
	}
	m.mu.Lock()
	m.cursors[ch] = value
	m.mu.Unlock()
	if st := m.store(); st != nil {
		return st.Set("im.cursor."+ch, value)
	}
	return nil
}

func (m *IMManager) store() *store.Store {
	m.rt.mu.Lock()
	st := m.rt.settingsStore
	m.rt.mu.Unlock()
	return st
}

// setStatus publishes one 来源 row for a channel.
func (m *IMManager) setStatus(ch, label string, enabled, ok bool, detail, problem string) {
	outcome := view.PollFailed
	if ok {
		outcome = view.PollOK
	}
	at := time.Now()
	m.rt.SetSourceStatus(SourceStatus{
		Kind: ch, Name: label, Enabled: enabled,
		Facts: []view.Fact{{Label: "驱动", Value: label}},
		At:    at,
		Last:  view.PollResult{At: at, Outcome: outcome, Detail: detail, Problem: problem},
	})
}

// CheckChannel implements the UI's optional ChannelAuthorizer: it performs one
// authorization probe and returns a state the panel can show plus a URL the
// person can open. It never blocks the caller beyond the probe's own timeout.
func (m *IMManager) CheckChannel(name string) (state, detail, authURL string, err error) {
	cfg := m.Config()
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "feishu":
		return checkFeishuAuth(cfg, m.opts.FeishuBaseURL)
	case "dingtalk", "dingtalk-dws", "dws":
		if cfg.DingTransport == "stream" || (cfg.DingAppKey != "" && cfg.DingTransport != "dws") {
			return checkDingStreamAuth(cfg)
		}
		client := m.opts.DWS
		if client == nil {
			client = dingtalk.NewCommandDWSClient(dingtalk.DWSConfig{Command: cfg.DWSCmd})
		}
		return checkDWSAuth(client, cfg.DWSCmd)
	default:
		return "", "", "", fmt.Errorf("未知的 IM 渠道 %q（feishu / dingtalk）", name)
	}
}

// checkDWSAuth asks `dws auth status --json` and returns the login URL when the
// person still needs to authorize.
func checkDWSAuth(client dingtalk.DWSClient, cmd string) (state, detail, authURL string, err error) {
	if cmd == "" {
		cmd = "dws"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	auth, err := client.AuthStatus(ctx)
	if err != nil {
		return IMError, "无法执行 dws：" + err.Error() + "（安装 dws 或设置 dingtalk_dws_cmd）", "", err
	}
	if auth.LoggedIn {
		return IMReady, "已登录：" + auth.Account, "", nil
	}
	detail = "未登录：运行 `" + cmd + " auth login`"
	if auth.AuthURL != "" {
		detail += "，或打开授权链接"
	}
	return IMAuth, detail, auth.AuthURL, nil
}

// checkDingStreamAuth is the stream transport's check: credentials are the only
// verifiable fact without opening a socket.
func checkDingStreamAuth(cfg IMConfig) (state, detail, authURL string, err error) {
	var missing []string
	if cfg.DingAppKey == "" {
		missing = append(missing, "app key")
	}
	if cfg.DingAppSecret == "" {
		missing = append(missing, "app secret")
	}
	if cfg.DingRobot == "" {
		missing = append(missing, "robot code")
	}
	if len(missing) > 0 {
		return IMAuth, "钉钉 Stream 配置不完整：缺少 " + strings.Join(missing, "、"), "", nil
	}
	return IMReady, "凭据已配置（Stream 连接由启动后的订阅验证）", "", nil
}

// SaveSettings hook helpers live here so pages.go stays about projection.
// imConfigFromRuntime reads the current settings snapshot.
func (r *Runtime) imConfigFromRuntime() IMConfig {
	r.mu.Lock()
	values := make(map[string]string, len(r.settings))
	for k, v := range r.settings {
		values[k] = v
	}
	r.mu.Unlock()
	return IMConfigFromSettings(values)
}

// CheckChannel implements the panel's optional ChannelAuthorizer. The manager owns
// the probe; without one (a custom embedding that never attached IM) the call says
// so instead of pretending to have checked.
func (r *Runtime) CheckChannel(name string) (state, detail, authURL string, err error) {
	r.mu.Lock()
	m := r.im
	r.mu.Unlock()
	if m == nil {
		return IMError, "IM 未接线（没有 IM 管理器）", "", fmt.Errorf("runtime: IM manager not attached")
	}
	return m.CheckChannel(name)
}

// IMConfig is the current source-group configuration (boot and tests).
func (r *Runtime) IMConfig() IMConfig { return r.imConfigFromRuntime() }

// AttachIM records the manager so a settings save can restart changed channels.
func (r *Runtime) AttachIM(m *IMManager) {
	r.mu.Lock()
	r.im = m
	r.mu.Unlock()
}

// imAfterSettingsSave is called by SaveSettings after the commit: only a change
// to the source group restarts channels.
func (r *Runtime) imAfterSettingsSave(group string, before map[string]string) {
	if group != string(view.SettingsSource) {
		return
	}
	r.mu.Lock()
	m := r.im
	r.mu.Unlock()
	if m == nil {
		return
	}
	m.ApplyIMSettings(IMConfigFromSettings(before), r.imConfigFromRuntime())
}
