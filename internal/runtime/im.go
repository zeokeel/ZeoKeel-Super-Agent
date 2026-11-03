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
	DWSCmd     string
	Profile    string
	ListenKind string
	ChatQuery  string

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
	case "longpoll", "ws":
		return true
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
			m.setStatus(ch, label, false, false, "配置不完整", problem)
			m.rt.SetComponentHealth("im:"+ch, IMError, problem)
			return
		}
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
		m.setStatus(ch, label, false, false, "启动失败", redactIMDetail(err.Error()))
		m.rt.SetComponentHealth("im:"+ch, IMError, redactIMDetail(err.Error()))
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
		m.setStatusFor(ch, label, gen, true, true, fmt.Sprintf("轮询正常（%d 条）", len(events)), "")
		m.rt.SetComponentHealth("im:"+ch, "ready", fmt.Sprintf("最近一轮 %d 条", len(events)))
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
		switch cfg.FeishuTransport {
		case "longpoll":
		case "ws":
			if !feishuWSAvailable {
				return false, true, "飞书 ws 传输尚未接线；请选择 longpoll"
			}
		case "webhook":
			return false, true, "飞书 webhook 传输尚未实现；请选择 longpoll 或 ws"
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
			return false, true, "钉钉 DWS 可执行文件未配置"
		}
		switch effectiveListenKind(cfg) {
		case "at-me", "group", "all-direct", "all-group":
		default:
			return false, true, "未知的 DWS 监听范围 " + cfg.ListenKind + "（at-me/group/all-direct/all-group）"
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
	switch state {
	case im.HealthReady:
		m.setStatusFor(ch, label, gen, true, true, detail, "")
		m.rt.SetComponentHealth("im:"+ch, "ready", detail)
	case im.HealthPaused:
		m.setStatusFor(ch, label, gen, true, true, detail, "")
		m.rt.SetComponentHealth("im:"+ch, "ready", detail)
	default:
		m.setStatusFor(ch, label, gen, true, false, "", detail)
		m.rt.SetComponentHealth("im:"+ch, IMError, detail)
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
		Facts: []view.Fact{{Label: "驱动", Value: label}},
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
