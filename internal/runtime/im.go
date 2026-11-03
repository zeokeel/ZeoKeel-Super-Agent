package runtime

// IM channel management (im-channel-spec §1/§2/§4, P0-6/P1-8/P1-10/P1-11):
// each channel (feishu, dingtalk) has its own lifecycle, configuration and
// generation. Disabled means no listener and an honest "未启用" row; a changed
// transport/credential restarts only that channel; stopping cancels the loop,
// closes the driver and withdraws the outbox sender.
//
// DWS is the operator's personal DingTalk identity and runs as one long-lived
// `dws event +listen-im … -f ndjson` process (nih-integrations owns the CLI);
// Feishu is HTTP long polling and keeps a persisted per-channel+transport cursor.
// Both are fed into the same two-phase entry (ReceiveTrigger) as every Source.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"regexp"
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
//
// Enabled has three states: an explicit "1"/"0" in the settings file wins; when
// the key is absent it is derived from the credentials/flags (P0-4 migration:
// an already-configured deployment must not silently stop working). The
// explicit flags are exported so tests and the panel can tell the two apart.
type IMConfig struct {
	FeishuEnabled   bool
	FeishuExplicit  bool
	FeishuTransport string
	FeishuAppID     string
	FeishuAppSecret string
	FeishuChats     string
	FeishuSelfID    string

	DingEnabled   bool
	DingExplicit  bool
	DingTransport string
	// stream (official SDK) credentials. DWS does not use them: its identity is
	// the operator's DingTalk OAuth profile.
	DingAppKey    string
	DingAppSecret string
	DingRobot     string
	DingBotID     string
	// DWS CLI settings (im-channel-spec §2, corrected to the real CLI contract).
	DWSCmd            string
	Profile           string
	ListenKind        string
	ChatQuery         string
	PauseListenOnSend bool

	// SelfID is the legacy shared identity (source.im_self); per-channel ids
	// fall back to it during the P0-4 migration.
	SelfID string

	// dingTransportSet/dwsEvidence are migration memory: a default transport and
	// a derived-enabled decision must not override an operator's explicit choice.
	dingTransportSet bool
	dwsEvidence      bool
}

// IMConfigFromSettings reads the settings snapshot (keys with the "source."
// prefix). Explicit presence is decided by the key existing in the map (the
// settings codec normalizes values at load); absent keys are derived.
func IMConfigFromSettings(values map[string]string) IMConfig {
	get := func(key string) string { return strings.TrimSpace(values["source."+key]) }
	_, dingTransportSet := values["source.dingtalk_transport"]
	_, dwsCmdSet := values["source.dingtalk_dws_cmd"]
	getRaw := func(key string) (string, bool) { v, ok := values["source."+key]; return strings.TrimSpace(v), ok }
	listenKind, listenSet := getRaw("dingtalk_listen_kind")
	if listenKind == "" {
		listenKind = "at-me"
	}
	cfg := IMConfig{
		FeishuTransport: orDefault(get("feishu_transport"), "ws"),
		FeishuAppID:     get("feishu_app_id"),
		FeishuAppSecret: get("feishu_app_secret"),
		FeishuChats:     get("feishu_chats"),
		FeishuSelfID:    get("feishu_self_id"),

		DingTransport: orDefault(get("dingtalk_transport"), defaultDingTransport(get("dingtalk_robot"), dingTransportSet)),
		DingAppKey:    get("dingtalk_app_key"),
		DingAppSecret: get("dingtalk_app_secret"),
		DingRobot:     get("dingtalk_robot"),
		DingBotID:     get("dingtalk_bot_id"),
		DWSCmd:        orDefault(get("dingtalk_dws_cmd"), "dws"),
		Profile:       get("dingtalk_profile"),
		ListenKind:    listenKind,
		ChatQuery:     get("dingtalk_chat_query"),
		PauseListenOnSend: func() bool {
			v, _ := ParseSettingBool(get("dingtalk_pause_listen_on_send"))
			return v
		}(),

		SelfID:           get("im_self"),
		dingTransportSet: dingTransportSet,
	}
	// Legacy migration: a robot code is a Stream-only field; an install that has
	// one but no transport key was a Stream deployment before DWS existed.
	cfg.dwsEvidence = dwsCmdSet || cfg.Profile != "" || listenSet || cfg.ChatQuery != ""
	cfg.FeishuSelfID = pickFirst(cfg.FeishuSelfID, cfg.SelfID)
	cfg.DingBotID = pickFirst(cfg.DingBotID, cfg.SelfID)

	if v, explicit := explicitSetting(values, "source.feishu_enabled"); explicit {
		cfg.FeishuEnabled, cfg.FeishuExplicit = v, true
	} else {
		cfg.FeishuEnabled = cfg.feishuDerivedEnabled()
	}
	if v, explicit := explicitSetting(values, "source.dingtalk_enabled"); explicit {
		cfg.DingEnabled, cfg.DingExplicit = v, true
	} else {
		cfg.DingEnabled = cfg.dingDerivedEnabled()
	}
	return cfg
}

// defaultDingTransport prefers the old Stream default for legacy credentials
// (robot code present, no explicit transport); new installs get DWS (spec §1).
func defaultDingTransport(robot string, transportSet bool) string {
	if transportSet {
		return ""
	}
	if strings.TrimSpace(robot) != "" {
		return "stream"
	}
	return "dws"
}

// explicitSetting reports a setting's value and whether the key exists.
func explicitSetting(values map[string]string, key string) (bool, bool) {
	raw, ok := values[key]
	if !ok {
		return false, false
	}
	v, parsed := ParseSettingBool(raw)
	if !parsed {
		// Corrupt value (normalization should have fixed it): treat as absent
		// rather than silently disabling a channel.
		return false, false
	}
	return v, true
}

func (c IMConfig) feishuDerivedEnabled() bool {
	if c.FeishuAppID == "" || c.FeishuAppSecret == "" {
		return false
	}
	switch c.FeishuTransport {
	case "ws":
		// WS sees every chat the app is in; a chat list is an optional filter.
		return true
	case "longpoll":
		// Long polling can only poll explicit chats, so an empty list is unusable.
		return strings.TrimSpace(c.FeishuChats) != ""
	default:
		return false
	}
}

// dingDerivedEnabled derives the legacy/default state from configuration
// evidence. App-key/secret are Stream fields and never enable DWS by themselves;
// DWS needs an operator signal (profile/listen kind/chat filter/cmd).
func (c IMConfig) dingDerivedEnabled() bool {
	switch c.DingTransport {
	case "dws":
		return c.dwsEvidence
	case "stream":
		return c.DingAppKey != "" && c.DingAppSecret != "" && c.DingRobot != ""
	default:
		return false
	}
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

func pickFirst(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// effectiveListenKind defaults the DWS listen scope to at-me (spec §1); an
// explicit bad value is rejected by channelReadiness rather than guessed.
func effectiveListenKind(cfg IMConfig) string {
	if strings.TrimSpace(cfg.ListenKind) == "" {
		return "at-me"
	}
	return cfg.ListenKind
}

func selfIDFor(cfg IMConfig, ch string) string {
	if ch == "feishu" {
		return pickFirst(cfg.FeishuSelfID, cfg.SelfID)
	}
	return pickFirst(cfg.DingBotID, cfg.SelfID)
}

// IMFlags is the command-line overlay for one run. Stored settings are the
// fallback; flags win for this run and are reapplied on every settings save so a
// flag-only credential is not silently dropped (P1-6/P0-4).
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
	if strings.TrimSpace(f.SelfID) != "" {
		c.SelfID = strings.TrimSpace(f.SelfID)
		c.FeishuSelfID = pickFirst(c.FeishuSelfID, c.SelfID)
		c.DingBotID = pickFirst(c.DingBotID, c.SelfID)
	}
	// Legacy flag-only deployments were Stream deployments: robot flag + no
	// explicit transport means Stream, not DWS.
	if !c.dingTransportSet && strings.TrimSpace(f.DingRobot) != "" {
		c.DingTransport = "stream"
	}
	// A flag can enable a flag-only deployment; it must never override an
	// explicit "0" (P0-4).
	if !c.FeishuExplicit {
		c.FeishuEnabled = c.feishuDerivedEnabled()
	}
	if !c.DingExplicit {
		c.DingEnabled = c.dingDerivedEnabled()
	}
	return c
}

// IMManagerOptions lets the app (and tests) inject drivers, the sender registry,
// the DWS integration and the endpoints.
type IMManagerOptions struct {
	PollInterval time.Duration
	// SelfTimeout bounds the driver.Self probe so a slow network never stalls a
	// settings save (the probe runs in the channel goroutine; P1-10).
	SelfTimeout time.Duration
	// StopGrace is how long a shutdown waits for one channel to exit.
	StopGrace time.Duration
	// RegisterSender/UnregisterSender publish one channel's Sender to the outbox
	// router. Registration happens only after a successful start; disabling or
	// stopping a channel withdraws it.
	RegisterSender   func(kind string, s im.Sender)
	UnregisterSender func(kind string)
	// BuildFeishu / BuildDingtalk replace the real drivers in tests.
	BuildFeishu   func(IMConfig) (im.Provider, error)
	BuildDingtalk func(IMConfig) (im.Provider, error)
	// DWS is the old-style CLI client kept for injected fakes/tests; DWSCLI is the
	// long-lived consumer's fake-CLI seam (both optional). BuildDWS replaces the
	// whole integration.
	DWS           dingtalk.DWSClient
	DWSCLI        dingtalk.DWSCLI
	BuildDWS      dwsChannelFactory
	FeishuBaseURL string
	Logger        *slog.Logger
	// Flags are reapplied on every save so flag-only credentials survive.
	Flags IMFlags
}

// imChannel is one running channel generation.
type imChannel struct {
	gen       uint64
	label     string
	transport string
	cancel    context.CancelFunc
	done      chan struct{}
	drv       im.Provider
	closer    io.Closer
	dws       dwsChannel
}

// IMManager owns the channels' lifecycle. All mutations of channels are
// serialized by applyMu; the generation number makes late writes from a stopped
// loop harmless.
type IMManager struct {
	rt   *Runtime
	opts IMManagerOptions

	applyMu sync.Mutex
	mu      sync.Mutex
	ctx     context.Context
	cfg     IMConfig
	flags   IMFlags
	chans   map[string]*imChannel
	seq     uint64
	started bool
	stopped bool
	// cursors is the memory fallback when no store is attached; with a store the
	// cursor lives in kv under "im.cursor.<channel>.<transport>".
	cursors map[string]string
	// dwsBuilt counts DWS backends built, so a test can prove CheckChannel reuses
	// the running one instead of constructing a second client.
	dwsBuilt int
	// conns is the last connection state per channel, published as source facts
	// and available to the panel through Connection() (UX U-03).
	conns map[string]ChannelConnection
}

// NewIMManager builds a manager for one Runtime. Start/Apply do the wiring.
func NewIMManager(rt *Runtime, opts IMManagerOptions) *IMManager {
	if opts.PollInterval <= 0 {
		opts.PollInterval = 2 * time.Second
	}
	if opts.SelfTimeout <= 0 {
		opts.SelfTimeout = 10 * time.Second
	}
	if opts.StopGrace <= 0 {
		opts.StopGrace = 5 * time.Second
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	return &IMManager{rt: rt, opts: opts, chans: map[string]*imChannel{},
		cursors: map[string]string{}, conns: map[string]ChannelConnection{}}
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
	m.applyMu.Lock()
	defer m.applyMu.Unlock()
	m.mu.Lock()
	if m.stopped {
		m.mu.Unlock()
		return
	}
	started := m.started
	before := m.cfg
	m.ctx = ctx
	m.cfg = cfg
	m.flags = m.opts.Flags
	m.started = true
	m.mu.Unlock()
	if !started {
		before = IMConfig{}
	}
	m.applyLocked(before, cfg)
}

// ApplyIMSettings diffs the old and new configuration and restarts only the
// channels whose enable/transport/credentials changed. Serialized and
// idempotent: two concurrent saves cannot double-start a channel (P1-10).
func (m *IMManager) ApplyIMSettings(before, after IMConfig) {
	m.applyMu.Lock()
	defer m.applyMu.Unlock()
	m.mu.Lock()
	if m.stopped {
		m.mu.Unlock()
		return
	}
	flags := m.flags
	if flags == m.opts.Flags {
		flags = m.opts.Flags
	}
	m.cfg = after.WithFlags(flags)
	m.mu.Unlock()
	m.applyLocked(before.WithFlags(flags), after.WithFlags(flags))
}

// Config returns the current configuration (tests/UI facts).
func (m *IMManager) Config() IMConfig {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cfg
}

// Stop cancels every channel, waits up to StopGrace, closes its driver and
// withdraws its outbox sender. It is idempotent and safe from main's defer
// (P1-10). After Stop, Apply/Start are no-ops: a shutdown cannot resurrect a
// listener.
func (m *IMManager) Stop() {
	m.applyMu.Lock()
	defer m.applyMu.Unlock()
	m.mu.Lock()
	if m.stopped {
		m.mu.Unlock()
		return
	}
	m.stopped = true
	m.ctx = nil
	chans := m.chans
	m.chans = map[string]*imChannel{}
	m.mu.Unlock()
	for ch, run := range chans {
		m.stopChannel(ch, run, true)
		m.recordConn(ch, ChannelConnection{Transport: run.transport,
			State: IMStateDisabled, Detail: "已停止", NextAction: "重新启动应用后生效"})
		m.setStatus(ch, imLabel(ch), false, true, "已停止", "")
		m.rt.SetComponentHealth("im:"+ch, "ready", "已停止")
	}
	m.unregisterSender("feishu")
	m.unregisterSender("dingtalk")
}

// stopChannel cancels one generation, closes its driver and withdraws the
// sender. wait=true is used at shutdown (bounded wait); restarts close the
// driver immediately and let the old goroutine drain in the background so a
// settings save never blocks on a slow model call.
func (m *IMManager) stopChannel(ch string, run *imChannel, wait bool) {
	if run == nil {
		return
	}
	run.cancel()
	if run.closer != nil {
		if err := run.closer.Close(); err != nil {
			m.opts.Logger.Warn("IM driver close failed", "channel", ch, "error", err)
		}
	}
	m.unregisterSender(ch)
	if !wait {
		go func() {
			select {
			case <-run.done:
			case <-time.After(m.opts.StopGrace):
				m.opts.Logger.Warn("IM channel did not exit after cancel", "channel", ch)
			}
		}()
		return
	}
	select {
	case <-run.done:
	case <-time.After(m.opts.StopGrace):
		m.opts.Logger.Warn("IM channel did not stop in time; forcing", "channel", ch)
	}
}

// channelChanged reports whether one channel needs a restart.
func channelChanged(a, b IMConfig, ch string) bool {
	if ch == "feishu" {
		return a.FeishuEnabled != b.FeishuEnabled ||
			a.FeishuExplicit != b.FeishuExplicit ||
			a.FeishuTransport != b.FeishuTransport ||
			a.FeishuAppID != b.FeishuAppID || a.FeishuAppSecret != b.FeishuAppSecret ||
			a.FeishuChats != b.FeishuChats || selfIDFor(a, ch) != selfIDFor(b, ch)
	}
	return a.DingEnabled != b.DingEnabled ||
		a.DingExplicit != b.DingExplicit ||
		a.DingTransport != b.DingTransport ||
		a.DingAppKey != b.DingAppKey || a.DingAppSecret != b.DingAppSecret ||
		a.DingRobot != b.DingRobot || a.DingBotID != b.DingBotID ||
		a.DWSCmd != b.DWSCmd || a.Profile != b.Profile ||
		a.ListenKind != b.ListenKind || a.ChatQuery != b.ChatQuery ||
		a.PauseListenOnSend != b.PauseListenOnSend ||
		a.SelfID != b.SelfID
}

// applyLocked restarts the channels that changed. applyMu is held by the caller.
func (m *IMManager) applyLocked(before, after IMConfig) {
	for _, ch := range []string{"feishu", "dingtalk"} {
		if channelChanged(before, after, ch) {
			m.restartChannelLocked(ch, before, after)
		}
	}
}

// restartChannelLocked stops the old generation (without blocking on it) and
// starts the new one if the configuration is complete.
func (m *IMManager) restartChannelLocked(ch string, before, after IMConfig) {
	label := imLabel(ch)
	m.mu.Lock()
	old := m.chans[ch]
	delete(m.chans, ch)
	m.mu.Unlock()
	m.stopChannel(ch, old, false)

	enabled, half, problem := channelReadiness(after, ch)
	if !enabled {
		if half {
			m.recordConn(ch, ChannelConnection{Transport: transportOf(after, ch),
				State: IMStateMisconfigured, Detail: problem,
				NextAction: "补全必填项后保存，再点「检查通道」"})
			m.setStatus(ch, label, false, false, "配置不完整", problem)
			m.rt.SetComponentHealth("im:"+ch, IMError, problem)
			return
		}
		m.recordConn(ch, ChannelConnection{Transport: transportOf(after, ch),
			State: IMStateDisabled, Detail: "未启用", NextAction: "在设置 › 来源里启用并保存"})
		m.setStatus(ch, label, false, true, "未启用", "")
		m.rt.SetComponentHealth("im:"+ch, "ready", "未启用")
		return
	}

	// A transport switch invalidates the old read position: a DWS subscription
	// id is not a Feishu long-poll cursor (and vice versa).
	if transportOf(after, ch) != transportOf(before, ch) {
		m.clearCursors(ch)
	}

	drv, closer, dws, err := m.buildDriver(ch, after)
	if err != nil {
		detail := redactIMDetail(err.Error())
		m.recordConn(ch, ChannelConnection{Transport: transportOf(after, ch),
			State: IMStateError, Detail: detail, NextAction: "检查配置或安装依赖后点「重新检查」"})
		m.setStatus(ch, label, false, false, "启动失败", detail)
		m.rt.SetComponentHealth("im:"+ch, IMError, detail)
		m.opts.Logger.Error("IM driver failed to start", "channel", ch, "error", err)
		return
	}

	ctx := m.channelContext()
	cctx, cancel := context.WithCancel(ctx)
	run := &imChannel{gen: m.nextGen(), label: label, transport: transportOf(after, ch),
		cancel: cancel, done: make(chan struct{}), drv: drv, closer: closer, dws: dws}
	m.mu.Lock()
	if m.stopped {
		m.mu.Unlock()
		cancel()
		if closer != nil {
			_ = closer.Close()
		}
		return
	}
	m.chans[ch] = run
	m.mu.Unlock()

	m.recordConn(ch, ChannelConnection{Transport: run.transport,
		State: IMStateConnecting, Detail: "连接中", NextAction: "等待首轮消息或点「检查通道」验证凭据"})
	m.setStatus(ch, label, true, true, "连接中", "")
	m.rt.SetComponentHealth("im:"+ch, "starting", "连接中")
	m.opts.Logger.Info("IM channel started", "channel", ch, "transport", run.transport, "gen", run.gen)
	go m.runChannel(cctx, ch, label, after, drv, run)
	// Sender registration only after the driver was built successfully; a
	// disabled/stopped channel withdraws it again.
	m.registerSender(ch, drv)
}

// runChannel resolves the channel's own identity (off the save path) and then
// runs the receive loop shaped by the driver.
func (m *IMManager) runChannel(ctx context.Context, ch, label string, cfg IMConfig, drv im.Provider, run *imChannel) {
	defer close(run.done)
	defer func() {
		if run.closer != nil {
			_ = run.closer.Close()
		}
	}()
	selfID := selfIDFor(cfg, ch)
	if selfID == "" {
		selfID = m.resolveSelf(ctx, ch, label, run.gen, drv)
	}
	if ctx.Err() != nil || !m.isCurrent(ch, run.gen) {
		return
	}
	if recv, ok := drv.(im.Receiver); ok {
		m.subscribeLoop(ctx, ch, label, run.gen, recv, selfID)
		return
	}
	if poller, ok := drv.(im.Poller); ok {
		m.pollLoop(ctx, ch, label, run.gen, cfg, poller, selfID)
		return
	}
	problem := fmt.Sprintf("%T 没有 Subscribe/Poll", drv)
	m.recordConn(ch, ChannelConnection{Transport: run.transport, State: IMStateError,
		Detail: redactIMDetail(problem), NextAction: "更新到支持该传输的版本"})
	m.setStatusFor(ch, label, run.gen, true, false, "", redactIMDetail(problem))
	m.rt.SetComponentHealth("im:"+ch, IMError, redactIMDetail(problem))
}

// resolveSelf asks the driver who it is. It is bounded and failure-tolerant: a
// channel without an identity can still receive direct messages; group @
// detection just stays conservative.
func (m *IMManager) resolveSelf(ctx context.Context, ch, label string, gen uint64, drv im.Provider) string {
	cctx, cancel := context.WithTimeout(ctx, m.opts.SelfTimeout)
	defer cancel()
	id, err := drv.Self(cctx)
	if err != nil {
		if ctx.Err() == nil {
			m.opts.Logger.Warn("IM identity probe failed", "channel", ch, "error", err)
			m.setStatusFor(ch, label, gen, true, true, "已连接；无法确认机器人身份", "")
		}
		return ""
	}
	return id.ID
}

// pollLoop is the cursor-shaped channel (Feishu long poll): poll, hand every
// event to the engine, persist the cursor only after the whole batch was
// accepted. A failed batch backs off without advancing and without busy-looping.
func (m *IMManager) pollLoop(ctx context.Context, ch, label string, gen uint64, cfg IMConfig, poller im.Poller, selfID string) {
	backoff := time.Second
	for {
		if !m.waitCtx(ctx, m.opts.PollInterval) {
			return
		}
		if !m.isCurrent(ch, gen) {
			return
		}
		cursor := m.cursor(ch, cfg)
		events, next, err := poller.Poll(ctx, cursor)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			state, detail := classifyIMError(err)
			m.recordConn(ch, m.failureConnection(ch, cfg, state, detail))
			m.setStatusFor(ch, label, gen, true, false, "", detail)
			m.rt.SetComponentHealth("im:"+ch, state, detail)
			m.opts.Logger.Warn("IM poll failed", "channel", ch, "error", err)
			if !m.waitCtx(ctx, backoff) {
				return
			}
			backoff = growBackoff(backoff)
			continue
		}
		backoff = time.Second
		failed, firstErr := m.deliver(ch, events, selfID)
		if failed > 0 {
			detail := fmt.Sprintf("%d 条消息未入库：%s（整批不推进游标，稍后重试）", failed, redactIMDetail(imErrText(firstErr)))
			m.recordConn(ch, ChannelConnection{Transport: transportOf(cfg, ch), State: IMStateError,
				Detail: detail, NextAction: "查看应用日志；持续失败时检查磁盘/数据库"})
			m.setStatusFor(ch, label, gen, true, false, "", detail)
			m.rt.SetComponentHealth("im:"+ch, IMError, detail)
			m.opts.Logger.Warn("IM batch was not fully accepted", "channel", ch, "failed", failed, "error", firstErr)
			if !m.waitCtx(ctx, backoff) {
				return
			}
			backoff = growBackoff(backoff)
			continue
		}
		if next != "" && next != cursor {
			if err := m.setCursor(ch, cfg, next); err != nil {
				m.opts.Logger.Warn("IM cursor could not be saved", "channel", ch, "error", err)
			}
		}
		detail := fmt.Sprintf("轮询正常（%d 条）", len(events))
		m.recordConn(ch, ChannelConnection{Transport: transportOf(cfg, ch),
			State: IMStateReady, Detail: detail})
		m.setStatusFor(ch, label, gen, true, true, detail, "")
		m.rt.SetComponentHealth("im:"+ch, "ready", detail)
	}
}

// subscribeLoop is the long-lived channel (DWS consumer; a future WebSocket):
// the driver owns reconnects and dedupe, this loop only delivers.
func (m *IMManager) subscribeLoop(ctx context.Context, ch, label string, gen uint64, recv im.Receiver, selfID string) {
	events := make(chan im.RawEvent, 32)
	errCh := make(chan error, 1)
	go func() { errCh <- recv.Subscribe(ctx, events) }()
	for {
		select {
		case <-ctx.Done():
			return
		case err := <-errCh:
			if ctx.Err() != nil || err == nil {
				return
			}
			state, detail := classifyIMError(err)
			m.recordConn(ch, m.failureConnection(ch, m.Config(), state, detail))
			m.setStatusFor(ch, label, gen, true, false, "", detail)
			m.rt.SetComponentHealth("im:"+ch, state, detail)
			m.opts.Logger.Warn("IM subscribe stopped", "channel", ch, "error", err)
			return
		case ev, open := <-events:
			if !open {
				return
			}
			if ev.Unsupported {
				continue
			}
			if !m.isCurrent(ch, gen) {
				return
			}
			if _, err := m.rt.ReceiveTrigger(imTrigger(ch, ev, selfID)); err != nil &&
				!errors.Is(err, engine.ErrDuplicate) {
				m.opts.Logger.Warn("IM message rejected", "channel", ch, "error", err)
				m.setStatusFor(ch, label, gen, true, false, "", "最近一条消息未入库："+redactIMDetail(err.Error()))
				continue
			}
			m.recordConn(ch, ChannelConnection{Transport: transportOf(m.Config(), ch),
				State: IMStateReady, Detail: "收到消息 " + ev.ID})
			m.setStatusFor(ch, label, gen, true, true, "收到消息 "+ev.ID, "")
			m.rt.SetComponentHealth("im:"+ch, "ready", "收到消息")
		}
	}
}

// deliver hands one batch to the engine; duplicate deliveries count as success
// (content-derived Trigger ids make replays safe).
func (m *IMManager) deliver(ch string, events []im.RawEvent, selfID string) (failed int, firstErr error) {
	for _, ev := range events {
		if ev.Unsupported {
			continue
		}
		if _, err := m.rt.ReceiveTrigger(imTrigger(ch, ev, selfID)); err != nil &&
			!errors.Is(err, engine.ErrDuplicate) {
			failed++
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return failed, firstErr
}

func imErrText(err error) string {
	if err == nil {
		return "未知错误"
	}
	return err.Error()
}

func growBackoff(d time.Duration) time.Duration {
	d *= 2
	if d > 30*time.Second {
		return 30 * time.Second
	}
	return d
}

// waitCtx sleeps for d, returning false when ctx is done.
func (m *IMManager) waitCtx(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func (m *IMManager) channelContext() context.Context {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ctx != nil {
		return m.ctx
	}
	return context.Background()
}

func (m *IMManager) nextGen() uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.seq++
	return m.seq
}

func (m *IMManager) isCurrent(ch string, gen uint64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	run := m.chans[ch]
	return run != nil && run.gen == gen
}

func (m *IMManager) registerSender(ch string, drv im.Provider) {
	if m.opts.RegisterSender == nil {
		return
	}
	if s, ok := drv.(im.Sender); ok {
		m.opts.RegisterSender(ch, s)
	}
}

func (m *IMManager) unregisterSender(ch string) {
	if m.opts.UnregisterSender != nil {
		m.opts.UnregisterSender(ch)
	}
}

// transportOf returns the channel's transport for cursor namespacing.
func transportOf(cfg IMConfig, ch string) string {
	if ch == "feishu" {
		return orDefault(cfg.FeishuTransport, "ws")
	}
	return orDefault(cfg.DingTransport, "dws")
}

func imLabel(ch string) string {
	if ch == "dingtalk" {
		return "钉钉"
	}
	return "飞书"
}

// channelReadiness decides enabled/half/problem for one channel.
func channelReadiness(cfg IMConfig, ch string) (enabled, half bool, problem string) {
	if ch == "feishu" {
		if !cfg.FeishuEnabled {
			return false, false, ""
		}
		if cfg.FeishuTransport == "ws" && !feishuWSAvailable {
			return false, true, "飞书 ws 传输尚未接线；请选择 longpoll"
		}
	} else if !cfg.DingEnabled {
		return false, false, ""
	}
	if issues := configIssues(cfg, ch); len(issues) > 0 {
		msgs := make([]string, 0, len(issues))
		for _, issue := range issues {
			msgs = append(msgs, issue.message)
		}
		return false, true, strings.Join(msgs, "；")
	}
	return true, false, ""
}

// dwsFactory returns the DWS integration factory: a test/injected one wins, then
// nih-integrations' adapter (fed the injected CLI client when it lands), and as a
// last resort an auth-only adapter so a fake client can still answer
// CheckChannel without pretending to listen.
func (m *IMManager) dwsFactory() dwsChannelFactory {
	if m.opts.BuildDWS != nil {
		return m.opts.BuildDWS
	}
	return func(cfg IMConfig) (dwsChannel, error) {
		if m.opts.DWS != nil && m.opts.DWSCLI == nil {
			// Legacy fake injection: auth-only, never claims to listen.
			return &dwsAuthOnly{client: m.opts.DWS}, nil
		}
		return newDWSChannel(cfg, m.opts.DWSCLI)
	}
}

// buildDriver constructs the real driver for a channel (test hooks override it).
func (m *IMManager) buildDriver(ch string, cfg IMConfig) (im.Provider, io.Closer, dwsChannel, error) {
	if ch == "feishu" {
		if m.opts.BuildFeishu != nil {
			d, err := m.opts.BuildFeishu(cfg)
			return d, closerOf(d), nil, err
		}
		if cfg.FeishuTransport == "ws" {
			d, closer, err := newFeishuWS(cfg)
			return d, closer, nil, err
		}
		d, err := feishu.New(feishu.Config{
			AppID: cfg.FeishuAppID, AppSecret: cfg.FeishuAppSecret,
			Chats: splitFeishuChats(cfg.FeishuChats), SelfID: selfIDFor(cfg, ch), SelfName: selfIDFor(cfg, ch),
		})
		return d, closerOf(d), nil, err
	}
	if m.opts.BuildDingtalk != nil {
		d, err := m.opts.BuildDingtalk(cfg)
		return d, closerOf(d), nil, err
	}
	if cfg.DingTransport == "stream" {
		return m.buildStreamDriver(cfg)
	}
	dws, err := m.dwsFactory()(cfg)
	if err != nil {
		return nil, nil, nil, err
	}
	m.mu.Lock()
	m.dwsBuilt++
	m.mu.Unlock()
	m.wireDWSStatus("dingtalk", imLabel("dingtalk"), dws)
	return dws, dws, dws, nil
}

// buildStreamDriver builds the official-SDK Stream driver itself so the manager
// owns a Close for it (a channel restart must not leave a WebSocket behind).
func (m *IMManager) buildStreamDriver(cfg IMConfig) (im.Provider, io.Closer, dwsChannel, error) {
	httpTr := dingtalk.NewHTTPTransport("", cfg.DingAppKey, cfg.DingAppSecret, nil)
	tr, err := dingtalk.NewStreamTransport(httpTr, dingtalk.StreamConfig{
		ClientID: cfg.DingAppKey, ClientSecret: cfg.DingAppSecret,
	})
	if err != nil {
		return nil, nil, nil, err
	}
	drv := dingtalk.NewWithTransport(dingtalk.Config{
		Transport: "stream", AppKey: cfg.DingAppKey, AppSecret: cfg.DingAppSecret,
		RobotCode: cfg.DingRobot, SelfID: selfIDFor(cfg, "dingtalk"), BotName: "ZeoKeel", Now: time.Now,
	}, tr)
	tr.SetStatus(func(state im.HealthState, detail string) {
		m.transportStatus("dingtalk", imLabel("dingtalk"), drv, state, detail)
	})
	return drv, tr, nil, nil
}

// wireDWSStatus forwards the DWS consumer's health to the source row.
func (m *IMManager) wireDWSStatus(ch, label string, dws dwsChannel) {
	if dws == nil {
		return
	}
	dws.SetStatus(func(state im.HealthState, detail string) {
		m.transportStatus(ch, label, dws, state, detail)
	})
}

// transportStatus maps a driver-level health event to the panel row, ignoring
// events from a generation that has already been replaced.
func (m *IMManager) transportStatus(ch, label string, owner any, state im.HealthState, detail string) {
	detail = redactIMDetail(detail)
	m.mu.Lock()
	run := m.chans[ch]
	current := run != nil && sameProvider(run.drv, owner)
	gen := uint64(0)
	if run != nil {
		gen = run.gen
	}
	m.mu.Unlock()
	if !current {
		return
	}
	cfg := m.Config()
	switch state {
	case im.HealthReady:
		m.recordConn(ch, ChannelConnection{Transport: transportOf(cfg, ch),
			State: IMStateReady, Detail: detail})
		m.setStatusFor(ch, label, gen, true, true, detail, "")
		m.rt.SetComponentHealth("im:"+ch, "ready", detail)
	case im.HealthPaused:
		m.recordConn(ch, ChannelConnection{Transport: transportOf(cfg, ch),
			State: IMStateDisabled, Detail: detail, NextAction: "重新启用渠道"})
		m.setStatusFor(ch, label, gen, true, true, detail, "")
		m.rt.SetComponentHealth("im:"+ch, "ready", detail)
	case im.HealthStarting:
		m.recordConn(ch, ChannelConnection{Transport: transportOf(cfg, ch),
			State: IMStateConnecting, Detail: detail, NextAction: "等待连接或点「检查通道」"})
		m.setStatusFor(ch, label, gen, true, true, detail, "")
		m.rt.SetComponentHealth("im:"+ch, "starting", detail)
	default:
		imState, det := classifyIMError(errors.New(detail))
		m.recordConn(ch, m.failureConnection(ch, cfg, imState, det))
		m.setStatusFor(ch, label, gen, true, false, "", det)
		m.rt.SetComponentHealth("im:"+ch, IMError, det)
	}
}

func sameProvider(a, b any) bool {
	if a == nil || b == nil {
		return false
	}
	type identity interface{ Name() string }
	ai, aok := a.(identity)
	bi, bok := b.(identity)
	if aok && bok && ai.Name() == bi.Name() && ai == bi {
		return true
	}
	return a == b
}

func closerOf(v any) io.Closer {
	if c, ok := v.(io.Closer); ok {
		return c
	}
	return nil
}

// imTrigger converts one IM event into a Trigger. Scope is Global (an IM event
// is not owned by a workspace), the id includes the conversation so identical
// message ids in different conversations cannot collide, and a missing time is
// replaced with now before UnixMilli (a zero would otherwise become 1970).
func imTrigger(source string, ev im.RawEvent, selfID string) domain.Trigger {
	at := time.Now()
	if ev.At > 0 {
		at = time.UnixMilli(ev.At)
	}
	return domain.Trigger{
		ID:           domain.Key(domain.Global, source, "trigger", ev.Conv.ID, ev.ID),
		Scope:        domain.Global,
		Source:       source,
		Human:        !ev.Actor.Bot,
		Addressed:    im.AddressedFor(ev, selfID),
		Threads:      []domain.ThreadRef{{Source: source, ID: ev.Conv.ID}},
		Conversation: ev.Conv,
		Actor:        ev.Actor,
		Channel:      domain.ChannelRef{Source: source, Thread: ev.Conv.ID},
		Text:         ev.Text,
		At:           at,
	}
}

// classifyIMError turns a driver error into a health state + actionable detail.
func classifyIMError(err error) (state, detail string) {
	if errors.Is(err, im.ErrUnsupported) {
		return IMError, redactIMDetail("该驱动不支持接收：" + err.Error())
	}
	if errors.Is(err, dingtalk.ErrDWSAuthRequired) || errors.Is(err, dingtalk.ErrAuthRequired) {
		return IMAuth, "需要重新授权：dws 未登录，运行 `dws auth login`"
	}
	var de *dingtalk.DWSError
	if errors.As(err, &de) && (de.Status == 401 || de.Status == 403 || de.AuthURL != "") {
		detail = "需要重新授权：dws 未登录"
		if de.AuthURL != "" {
			detail += "，或打开 " + de.AuthURL
		}
		return IMAuth, detail
	}
	msg := strings.ToLower(err.Error())
	for _, marker := range []string{"401", "403", "unauthorized", "invalid_token", "token expired", "loggedin\":false", "not logged in"} {
		if strings.Contains(msg, marker) {
			return IMAuth, "需要重新授权：" + redactIMDetail(err.Error())
		}
	}
	return IMError, redactIMDetail(err.Error())
}

// imSecretPatterns redact credentials that could reach stderr/health details
// (im-channel-spec §2 "截断，去密钥"). The vendor's Secrets option covers DWS
// config values; this is the last line for platform errors.
var imSecretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(app[_-]?secret|access[_-]?token|tenant_access_token|secret|password|api[_-]?key)("?\s*[:=]\s*)("[^"]*"|[^\s,;}]+)`),
	regexp.MustCompile(`sk-[A-Za-z0-9_-]{8,}`),
}

func redactIMDetail(s string) string {
	s = strings.TrimSpace(s)
	for i, p := range imSecretPatterns {
		if i == 0 {
			s = p.ReplaceAllString(s, "${1}${2}***")
			continue
		}
		s = p.ReplaceAllString(s, "***")
	}
	if len(s) > 300 {
		s = s[:300] + "…"
	}
	return s
}

// cursorKey is per channel and transport: switching transports must never feed
// one protocol's read position to another.
func (m *IMManager) cursorKey(ch, transport string) string {
	return "im.cursor." + ch + "." + transport
}

func (m *IMManager) legacyCursorKey(ch string) string { return "im.cursor." + ch }

// cursor reads the channel cursor from kv, falling back to memory. The legacy
// per-channel key is migrated once so an upgrade does not replay history.
func (m *IMManager) cursor(ch string, cfg IMConfig) string {
	key := m.cursorKey(ch, transportOf(cfg, ch))
	if st := m.store(); st != nil {
		if v, ok, err := st.Get(key); err == nil && ok && strings.TrimSpace(v) != "" {
			return v
		}
		if v, ok, err := st.Get(m.legacyCursorKey(ch)); err == nil && ok && strings.TrimSpace(v) != "" {
			return v
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cursors[key]
}

// setCursor persists only after a batch was fully accepted by the engine.
func (m *IMManager) setCursor(ch string, cfg IMConfig, value string) error {
	if value == "" {
		return nil
	}
	key := m.cursorKey(ch, transportOf(cfg, ch))
	m.mu.Lock()
	m.cursors[key] = value
	m.mu.Unlock()
	st := m.store()
	if st == nil {
		return nil
	}
	if err := st.Set(key, value); err != nil {
		return err
	}
	// Neutralize the legacy key: the namespaced one is now authoritative.
	_ = st.Set(m.legacyCursorKey(ch), "")
	return nil
}

// clearCursors drops every transport variant (and the legacy key) for a channel;
// used when the transport changes so a DWS subscription id never feeds Feishu.
func (m *IMManager) clearCursors(ch string) {
	st := m.store()
	for _, transport := range []string{"longpoll", "dws", "stream", ""} {
		m.mu.Lock()
		delete(m.cursors, m.cursorKey(ch, transport))
		m.mu.Unlock()
		if st != nil {
			_ = st.Set(m.cursorKey(ch, transport), "")
		}
	}
	if st != nil {
		_ = st.Set(m.legacyCursorKey(ch), "")
	}
}

func (m *IMManager) store() *store.Store {
	m.rt.mu.Lock()
	st := m.rt.settingsStore
	m.rt.mu.Unlock()
	return st
}

// setStatus publishes one 来源 row for a channel.
func (m *IMManager) setStatus(ch, label string, enabled, ok bool, detail, problem string) {
	m.setStatusFor(ch, label, 0, enabled, ok, detail, problem)
}

// setStatusFor is setStatus with a generation guard: a late poll from a stopped
// generation cannot overwrite the new channel's row.
func (m *IMManager) setStatusFor(ch, label string, gen uint64, enabled, ok bool, detail, problem string) {
	if gen != 0 && !m.isCurrent(ch, gen) {
		return
	}
	outcome := view.PollFailed
	if ok {
		outcome = view.PollOK
	}
	at := time.Now()
	m.rt.SetSourceStatus(SourceStatus{
		Kind: ch, Name: label, Enabled: enabled,
		Facts: m.factsFor(ch, label),
		At:    at,
		Last: view.PollResult{At: at, Outcome: outcome,
			Detail: redactIMDetail(detail), Problem: redactIMDetail(problem)},
	})
}

// runningDWS returns the active DWS integration for one channel, if any.
func (m *IMManager) runningDWS(ch string) dwsChannel {
	m.mu.Lock()
	defer m.mu.Unlock()
	if run := m.chans[ch]; run != nil {
		return run.dws
	}
	return nil
}

// CheckChannel implements the UI's optional ChannelAuthorizer: one authorization
// probe. For DWS it reuses the running consumer's serialized runner (and thus
// its `dws` child-process queue); only a stopped channel builds a temporary
// integration, which is closed before returning (P0-6).
func (m *IMManager) CheckChannel(name string) (state, detail, authURL string, err error) {
	cfg := m.Config()
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "feishu":
		return checkFeishuAuth(cfg, m.opts.FeishuBaseURL)
	case "dingtalk", "dingtalk-dws", "dws":
		if cfg.DingTransport == "stream" {
			return checkDingStreamAuth(cfg)
		}
		if dws := m.runningDWS("dingtalk"); dws != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			auth, err := dws.AuthStatus(ctx)
			return dwsAuthState(auth, err, cfg.DWSCmd)
		}
		dws, err := m.dwsFactory()(cfg)
		if err != nil {
			return IMError, "无法检查 dws：" + redactIMDetail(err.Error()), "", err
		}
		defer func() { _ = dws.Close() }()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		auth, err := dws.AuthStatus(ctx)
		return dwsAuthState(auth, err, cfg.DWSCmd)
	default:
		return "", "", "", fmt.Errorf("未知的 IM 渠道 %q（feishu / dingtalk）", name)
	}
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

// --- IM setup usability (UX U-03): connection state, per-transport fields, -----
// --- draft checks, DWS install/version/login facts. ---------------------------

// Connection states the panel can render. ready/needs_auth/misconfigured/
// disabled/error are the product states; connecting is the short "starting"
// window before the first poll/event (the panel may draw it as ready+“连接中”).
const (
	IMStateReady         = "ready"
	IMStateNeedsAuth     = "needs_auth"
	IMStateMisconfigured = "misconfigured"
	IMStateDisabled      = "disabled"
	IMStateConnecting    = "connecting"
	IMStateError         = "error"
)

// ChannelConnection is one channel's connection fact: the state, why, and the
// next action a person should take. It is published as source-row facts and is
// also available through Runtime.IMConnection for the panel.
type ChannelConnection struct {
	Name         string
	Label        string
	Transport    string
	State        string
	Detail       string
	NextAction   string
	AuthURL      string
	LoginCommand []string
	Version      string
}

// normalization for the panel's channel ids.
func normalizeChannelName(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "feishu":
		return "feishu"
	case "dingtalk", "dingtalk-dws", "dws", "ding":
		return "dingtalk"
	default:
		return ""
	}
}

// failureConnection maps a probe/poll failure to the connection model, with the
// actionable next step (install/version/login for DWS, credentials for others).
func (m *IMManager) failureConnection(ch string, cfg IMConfig, state, detail string) ChannelConnection {
	conn := ChannelConnection{Transport: transportOf(cfg, ch), State: IMStateError,
		Detail: redactIMDetail(detail), NextAction: "查看来源页的错误详情；修好后点「重新检查」"}
	switch state {
	case IMAuth:
		conn.State = IMStateNeedsAuth
		if ch == "dingtalk" && transportOf(cfg, ch) == "dws" {
			conn.LoginCommand = dwsLoginArgs(cfg)
			conn.NextAction = "在终端运行 " + strings.Join(conn.LoginCommand, " ") + "，完成后点「检查通道」"
		} else {
			conn.NextAction = "在飞书开放平台检查应用权限后点「检查通道」"
		}
	}
	if ch == "dingtalk" && transportOf(cfg, ch) == "dws" && dingtalk.IsMissingBinary(errors.New(detail)) {
		conn.State = IMStateMisconfigured
		conn.NextAction = "安装 DingTalk Workspace CLI（dws）后点「检查通道」"
	}
	return conn
}

// recordConn stores one channel's connection fact (thread-safe).
func (m *IMManager) recordConn(ch string, c ChannelConnection) {
	if ch == "" {
		return
	}
	c.Name = ch
	if c.Label == "" {
		c.Label = imLabel(ch)
	}
	m.mu.Lock()
	if c.Transport == "" {
		c.Transport = transportOf(m.cfg, ch)
	}
	m.conns[ch] = c
	m.mu.Unlock()
}

// connectionFor returns a channel's state, defaulting to disabled before the
// manager ever touched it.
func (m *IMManager) connectionFor(ch string) ChannelConnection {
	m.mu.Lock()
	c, ok := m.conns[ch]
	m.mu.Unlock()
	if ok {
		return c
	}
	return ChannelConnection{Name: ch, Label: imLabel(ch), State: IMStateDisabled,
		Detail: "未启用", NextAction: "在设置 › 来源里启用并保存"}
}

// Connection returns one channel's connection state (runtime wrapper below).
func (m *IMManager) Connection(name string) (ChannelConnection, bool) {
	ch := normalizeChannelName(name)
	if ch == "" {
		return ChannelConnection{}, false
	}
	return m.connectionFor(ch), true
}

// factsFor is the source-row fact list: driver + honest state/next action.
func (m *IMManager) factsFor(ch, label string) []view.Fact {
	facts := []view.Fact{{Label: "驱动", Value: label}}
	conn := m.connectionFor(ch)
	if conn.State != "" {
		facts = append(facts, view.Fact{Label: "状态", Value: conn.State})
	}
	if conn.NextAction != "" {
		facts = append(facts, view.Fact{Label: "下一步", Value: conn.NextAction})
	}
	if conn.Version != "" {
		facts = append(facts, view.Fact{Label: "版本", Value: conn.Version})
	}
	return facts
}

// ChannelFieldSpec is one credential/knob of one transport. Key is the settings
// key without the "source." prefix (the UI's settings group key).
type ChannelFieldSpec struct {
	Key      string
	Label    string
	Kind     string // text | secret | switch | select
	Required bool
	Default  string
	Help     string
	Options  []string
}

// ChannelTransportSpec is one transport of one channel.
type ChannelTransportSpec struct {
	ID          string
	Label       string
	Note        string
	Implemented bool
	Fields      []ChannelFieldSpec
}

// ChannelConfigSpec is the panel-facing contract of one IM channel: which
// transports exist and exactly which fields each transport needs. Runtime owns
// this list; the UI can render it without hard-coding credential keys.
type ChannelConfigSpec struct {
	Name       string
	Label      string
	Transports []ChannelTransportSpec
}

// IMChannelConfigSpecs returns the per-transport field model (UX U-03). The
// panel's current view.ChannelSpec is static; consuming this list needs a small
// view contract addition (reported to the Lead).
func IMChannelConfigSpecs() []ChannelConfigSpec {
	feishuCreds := []ChannelFieldSpec{
		{Key: "feishu_app_id", Label: "App ID", Kind: "text", Required: true,
			Help: "飞书开放平台 → 应用 → 凭证与基础信息"},
		{Key: "feishu_app_secret", Label: "App Secret", Kind: "secret", Required: true},
	}
	withChats := func(required bool, help string) []ChannelFieldSpec {
		out := append([]ChannelFieldSpec(nil), feishuCreds...)
		out = append(out, ChannelFieldSpec{Key: "feishu_chats", Label: "会话 ID（逗号分隔）",
			Kind: "text", Required: required, Help: help})
		return out
	}
	dwsFields := []ChannelFieldSpec{
		{Key: "dingtalk_dws_cmd", Label: "dws 可执行文件", Kind: "text", Default: "dws",
			Help: "留空用 PATH 里的 dws；也可填绝对路径或包装脚本"},
		{Key: "dingtalk_profile", Label: "DWS 登录 profile", Kind: "text",
			Help: "留空使用 dws 当前登录的 profile"},
		{Key: "dingtalk_listen_kind", Label: "监听范围", Kind: "select", Default: "at-me",
			Options: []string{"at-me", "group", "all-direct", "all-group"},
			Help:    "at-me：只接 @ 我；group：群消息；all-direct：全部私聊；all-group：全部群"},
		{Key: "dingtalk_chat_query", Label: "群过滤（可选）", Kind: "text",
			Help: "自然语言目标，交由 dws 解析；留空监听范围由监听范围决定"},
		{Key: "dingtalk_pause_listen_on_send", Label: "发送时暂停监听", Kind: "switch", Default: "0",
			Help: "仅当本机 dws 要求 profile 独占时打开；默认关闭"},
	}
	streamFields := []ChannelFieldSpec{
		{Key: "dingtalk_app_key", Label: "AppKey", Kind: "text", Required: true},
		{Key: "dingtalk_app_secret", Label: "AppSecret", Kind: "secret", Required: true},
		{Key: "dingtalk_robot", Label: "RobotCode", Kind: "text", Required: true},
		{Key: "dingtalk_bot_id", Label: "机器人自身 ID（@ 判定）", Kind: "text",
			Help: "留空回退到旧 im_self"},
	}
	return []ChannelConfigSpec{
		{Name: "feishu", Label: "飞书", Transports: []ChannelTransportSpec{
			{ID: "ws", Label: "官方长连接", Note: "推荐；无需公网地址",
				Implemented: feishuWSAvailable, Fields: withChats(false,
					"可选：留空监听应用可见的所有会话（含私聊与 @）；填了则只收这些会话")},
			{ID: "longpoll", Label: "HTTP 长轮询", Note: "需要显式会话列表",
				Implemented: true, Fields: withChats(true,
					"必填：长轮询只能轮询明确的会话 ID（oc_…）")},
		}},
		{Name: "dingtalk", Label: "钉钉", Transports: []ChannelTransportSpec{
			{ID: "dws", Label: "DWS（个人身份）", Note: "本机 CLI，个人 DingTalk 授权",
				Implemented: true, Fields: dwsFields},
			{ID: "stream", Label: "官方 Stream（机器人）", Note: "需要机器人应用凭据",
				Implemented: true, Fields: streamFields},
		}},
	}
}

// ChannelValidationIssue is one missing/invalid field of a draft configuration.
type ChannelValidationIssue struct {
	Field   string
	Message string
}

type channelIssue struct{ field, message string }

// configIssues is the single readiness validator (used by channelReadiness and
// ValidateChannelConfig): transport-specific required fields and known values.
func configIssues(cfg IMConfig, ch string) []channelIssue {
	var out []channelIssue
	add := func(field, msg string) { out = append(out, channelIssue{field: field, message: msg}) }
	if ch == "feishu" {
		switch cfg.FeishuTransport {
		case "longpoll":
			if cfg.FeishuAppID == "" {
				add("feishu_app_id", "飞书配置不完整：缺少 app id")
			}
			if cfg.FeishuAppSecret == "" {
				add("feishu_app_secret", "飞书配置不完整：缺少 app secret")
			}
			if strings.TrimSpace(cfg.FeishuChats) == "" {
				add("feishu_chats", "飞书长轮询需要至少一个会话 ID（oc_…）；或改用 ws（可留空监听所有）")
			}
		case "ws":
			if cfg.FeishuAppID == "" {
				add("feishu_app_id", "飞书配置不完整：缺少 app id")
			}
			if cfg.FeishuAppSecret == "" {
				add("feishu_app_secret", "飞书配置不完整：缺少 app secret")
			}
		default:
			add("feishu_transport", "未知的飞书传输 "+cfg.FeishuTransport+"（longpoll / ws）")
		}
		return out
	}
	switch cfg.DingTransport {
	case "dws":
		if strings.TrimSpace(cfg.DWSCmd) == "" {
			add("dingtalk_dws_cmd", "DWS 命令不能为空")
		}
		switch effectiveListenKind(cfg) {
		case "at-me", "group", "all-direct", "all-group":
		default:
			add("dingtalk_listen_kind", "未知的 DWS 监听范围 "+effectiveListenKind(cfg)+
				"（at-me/group/all-direct/all-group）")
		}
	case "stream":
		if cfg.DingAppKey == "" {
			add("dingtalk_app_key", "钉钉 Stream 配置不完整：缺少 app key")
		}
		if cfg.DingAppSecret == "" {
			add("dingtalk_app_secret", "钉钉 Stream 配置不完整：缺少 app secret")
		}
		if cfg.DingRobot == "" {
			add("dingtalk_robot", "钉钉 Stream 配置不完整：缺少 robot code")
		}
	default:
		add("dingtalk_transport", "未知的钉钉传输 "+cfg.DingTransport+"（dws / stream）")
	}
	return out
}

// ValidateChannelConfig validates unsaved settings-shaped values for the panel's
// inline validation (before save). Keys may carry or omit the "source." prefix.
func ValidateChannelConfig(name string, values map[string]string) []ChannelValidationIssue {
	ch := normalizeChannelName(name)
	if ch == "" {
		return []ChannelValidationIssue{{Message: "未知的 IM 渠道 " + name + "（feishu / dingtalk）"}}
	}
	cfg := IMConfigFromSettings(draftSettings(values))
	issues := configIssues(cfg, ch)
	out := make([]ChannelValidationIssue, 0, len(issues))
	for _, issue := range issues {
		out = append(out, ChannelValidationIssue{Field: issue.field, Message: issue.message})
	}
	return out
}

// draftSettings prefixes bare UI keys with "source." so IMConfigFromSettings can
// read them; already-prefixed keys win.
func draftSettings(values map[string]string) map[string]string {
	out := make(map[string]string, len(values))
	for k, v := range values {
		key := strings.TrimSpace(k)
		if key == "" {
			continue
		}
		switch {
		case strings.Contains(key, "."):
			out[key] = v
		default:
			out["source."+key] = v
		}
	}
	return out
}

// CheckChannelDraft checks the values being typed, without saving them (UX U-03:
// "检查通道" used to test the saved configuration, not the form). It reuses the
// same validators/probes as the saved path; the saved configuration is untouched.
func (m *IMManager) CheckChannelDraft(name string, values map[string]string) (state, detail, authURL string, err error) {
	ch := normalizeChannelName(name)
	if ch == "" {
		return "", "", "", fmt.Errorf("未知的 IM 渠道 %q（feishu / dingtalk）", name)
	}
	cfg := IMConfigFromSettings(m.rt.imSettingsWithDraft(values))
	if issues := configIssues(cfg, ch); len(issues) > 0 {
		msgs := make([]string, 0, len(issues))
		for _, issue := range issues {
			msgs = append(msgs, issue.message)
		}
		return IMStateMisconfigured, strings.Join(msgs, "；"), "", nil
	}
	if ch == "feishu" {
		st, det, url, perr := checkFeishuAuth(cfg, m.opts.FeishuBaseURL)
		switch st {
		case IMReady:
			return IMStateReady, det, "", nil
		case IMAuth:
			return IMStateNeedsAuth, det, url, nil
		default:
			if perr == nil {
				return IMStateError, det, url, nil
			}
			return IMStateError, det, url, perr
		}
	}
	if transportOf(cfg, ch) == "stream" {
		return IMStateReady, "凭据已填写；启用并保存后建立 Stream 连接", "", nil
	}
	return m.checkDWSConfig(cfg)
}

// checkDWSConfig probes install/version/auth for a draft DWS configuration. It
// builds one temporary channel (closed before returning) and never runs an
// interactive login.
func (m *IMManager) checkDWSConfig(cfg IMConfig) (state, detail, authURL string, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	dws, berr := m.draftDWS(cfg)
	if berr != nil {
		return IMStateError, "无法构造 DWS 渠道：" + redactIMDetail(berr.Error()), "", berr
	}
	defer func() { _ = dws.Close() }()
	version, verr := dws.DWSVersion(ctx)
	if verr != nil {
		if dingtalk.IsMissingBinary(verr) {
			return IMStateMisconfigured,
				"未检测到 dws 可执行文件（" + redactIMDetail(verr.Error()) + "）；" +
					"安装 DingTalk Workspace CLI 后点「重新检查」", "", nil
		}
		return IMStateError, "无法运行 dws：" + redactIMDetail(verr.Error()), "", verr
	}
	auth, aerr := dws.AuthStatus(ctx)
	if aerr != nil {
		if dingtalk.IsMissingBinary(aerr) {
			return IMStateMisconfigured, "未检测到 dws 可执行文件；安装后点「重新检查」", "", nil
		}
		if st, det, url, ok := dwsAuthError(aerr); ok {
			return st, det, url, nil
		}
		return IMStateError, "dws 授权检查失败：" + redactIMDetail(aerr.Error()), "", aerr
	}
	if !auth.LoggedIn {
		login := strings.Join(dws.LoginCommand(), " ")
		detail = "dws " + version + " 已安装，但未登录：在终端运行 `" + login + "`"
		if auth.AuthURL != "" {
			detail += "，或打开授权链接"
		}
		return IMStateNeedsAuth, detail, auth.AuthURL, nil
	}
	who := pickFirst(auth.Account, auth.Profile, "已登录")
	detail = "dws " + version + " 已登录：" + who
	if auth.Profile != "" {
		detail += "（profile " + auth.Profile + "）"
	}
	return IMStateReady, detail, "", nil
}

// draftDWS builds a temporary DWS integration for a probe (test hooks win).
func (m *IMManager) draftDWS(cfg IMConfig) (dwsChannel, error) {
	if m.opts.BuildDWS != nil {
		return m.opts.BuildDWS(cfg)
	}
	return newDWSChannel(cfg, m.opts.DWSCLI)
}

// DWSVersion reports the installed DWS CLI version, through the running consumer
// when there is one (never a second client otherwise, a temporary one is closed).
func (m *IMManager) DWSVersion(ctx context.Context) (string, error) {
	if running := m.runningDWS("dingtalk"); running != nil {
		return running.DWSVersion(ctx)
	}
	dws, err := m.draftDWS(m.Config())
	if err != nil {
		return "", err
	}
	defer func() { _ = dws.Close() }()
	return dws.DWSVersion(ctx)
}

// DWSLoginCommand returns the argv a person runs to log in. The panel never runs
// it itself (no interactive login from the app).
func (m *IMManager) DWSLoginCommand() []string {
	if running := m.runningDWS("dingtalk"); running != nil {
		return running.LoginCommand()
	}
	return dwsLoginArgs(m.Config())
}

// dwsLoginArgs is the login argv for a configuration.
func dwsLoginArgs(cfg IMConfig) []string {
	cmd := strings.TrimSpace(cfg.DWSCmd)
	if cmd == "" {
		cmd = "dws"
	}
	args := []string{cmd, "auth", "login"}
	if profile := strings.TrimSpace(cfg.Profile); profile != "" {
		args = append(args, "--profile", profile)
	}
	return args
}

// imSettingsWithDraft overlays unsaved settings-shaped values over the saved
// ones (used only by draft probes; nothing is written).
func (r *Runtime) imSettingsWithDraft(values map[string]string) map[string]string {
	r.mu.Lock()
	out := make(map[string]string, len(r.settings)+len(values))
	for k, v := range r.settings {
		out[k] = v
	}
	r.mu.Unlock()
	for k, v := range draftSettings(values) {
		if k == "__revision" {
			continue
		}
		out[k] = v
	}
	return out
}

// CheckChannelDraft checks unsaved IM settings from the panel (runtime wrapper).
func (r *Runtime) CheckChannelDraft(name string, values map[string]string) (state, detail, authURL string, err error) {
	r.mu.Lock()
	m := r.im
	r.mu.Unlock()
	if m == nil {
		return IMStateError, "IM 未接线（没有 IM 管理器）", "", fmt.Errorf("runtime: IM manager not attached")
	}
	return m.CheckChannelDraft(name, values)
}

// DWSVersion reports the DWS CLI version (runtime wrapper).
func (r *Runtime) DWSVersion(ctx context.Context) (string, error) {
	r.mu.Lock()
	m := r.im
	r.mu.Unlock()
	if m == nil {
		return "", fmt.Errorf("runtime: IM manager not attached")
	}
	return m.DWSVersion(ctx)
}

// DWSLoginCommand returns the terminal login command (runtime wrapper).
func (r *Runtime) DWSLoginCommand() []string {
	r.mu.Lock()
	m := r.im
	r.mu.Unlock()
	if m == nil {
		return []string{"dws", "auth", "login"}
	}
	return m.DWSLoginCommand()
}

// IMConnection returns one channel's connection state (runtime wrapper).
func (r *Runtime) IMConnection(name string) (ChannelConnection, bool) {
	r.mu.Lock()
	m := r.im
	r.mu.Unlock()
	if m == nil {
		return ChannelConnection{}, false
	}
	return m.Connection(name)
}

// IMChannelConfigSpecs returns the per-transport field model (runtime wrapper).
func (r *Runtime) IMChannelConfigSpecs() []ChannelConfigSpec { return IMChannelConfigSpecs() }
