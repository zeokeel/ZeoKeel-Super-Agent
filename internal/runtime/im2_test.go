package runtime

// IM lifecycle tests for the P0-6/P1-8/P1-10/P1-11 fixes: generations (no
// double-start), close/withdraw on stop/disable, DWS serialized-runner reuse by
// CheckChannel, transport-scoped cursors, tri-state enabled, async identity
// probing, Global-scope triggers and DWS as a long-lived receiver (no poll/cursor).

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zeokeel/super-agent/internal/domain"
	"github.com/zeokeel/super-agent/internal/engine"
	"github.com/zeokeel/super-agent/internal/im"
	"github.com/zeokeel/super-agent/internal/im/dingtalk"
	"github.com/zeokeel/super-agent/internal/view"
)

// fakePoller is a Provider+Poller+Sender(+Closer) stand-in with counters.
type fakePoller struct {
	name   string
	polls  atomic.Int32
	closed atomic.Int32
	err    error
	events []im.RawEvent
	slow   chan struct{} // when set, Self waits for close or ctx
}

func (f *fakePoller) Name() string { return f.name }
func (f *fakePoller) Capabilities() im.Capabilities {
	return im.Capabilities{Markdown: true, MaxTextLen: 4000}
}
func (f *fakePoller) Self(ctx context.Context) (im.Identity, error) {
	if f.slow != nil {
		select {
		case <-f.slow:
		case <-ctx.Done():
			return im.Identity{}, ctx.Err()
		}
	}
	return im.Identity{ID: "bot-self"}, nil
}
func (f *fakePoller) Members(context.Context, string) ([]im.Member, error) { return nil, nil }
func (f *fakePoller) Poll(context.Context, string) ([]im.RawEvent, string, error) {
	f.polls.Add(1)
	if f.err != nil {
		return nil, "", f.err
	}
	evs := f.events
	f.events = nil
	return evs, "c2", nil
}
func (f *fakePoller) Send(context.Context, im.Outbound) (im.Receipt, error) {
	return im.Receipt{MessageID: "m1"}, nil
}
func (f *fakePoller) ReadThread(context.Context, domain.Conversation, string) (im.Page, error) {
	return im.Page{Done: true}, nil
}
func (f *fakePoller) Close() error { f.closed.Add(1); return nil }

// fakeDWSChannel is the long-lived DWS stand-in: a Receiver (no Poll), a Sender,
// auth/status and Close. It proves the manager runs DWS as a subscription, not a
// poll-with-cursor channel.
type fakeDWSChannel struct {
	events    chan im.RawEvent
	subs      atomic.Int32
	authCalls atomic.Int32
	closed    atomic.Int32
	auth      dwsAuth
	authErr   error
}

func (f *fakeDWSChannel) Name() string { return "dingtalk" }
func (f *fakeDWSChannel) Capabilities() im.Capabilities {
	return im.Capabilities{Mentions: true, MaxTextLen: 4000}
}
func (f *fakeDWSChannel) Self(context.Context) (im.Identity, error) {
	return im.Identity{ID: "personal-dws"}, nil
}
func (f *fakeDWSChannel) Members(context.Context, string) ([]im.Member, error) {
	return nil, im.ErrUnsupported
}
func (f *fakeDWSChannel) Subscribe(ctx context.Context, out chan<- im.RawEvent) error {
	f.subs.Add(1)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case ev := <-f.events:
			select {
			case out <- ev:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
}
func (f *fakeDWSChannel) Send(context.Context, im.Outbound) (im.Receipt, error) {
	return im.Receipt{MessageID: "dws-1"}, nil
}
func (f *fakeDWSChannel) ReadThread(context.Context, domain.Conversation, string) (im.Page, error) {
	return im.Page{Done: true}, nil
}
func (f *fakeDWSChannel) AuthStatus(context.Context) (dwsAuth, error) {
	f.authCalls.Add(1)
	return f.auth, f.authErr
}
func (f *fakeDWSChannel) SetStatus(func(im.HealthState, string)) {}
func (f *fakeDWSChannel) Close() error                           { f.closed.Add(1); return nil }

func (f *fakeDWSChannel) Push(ev im.RawEvent) {
	select {
	case f.events <- ev:
	default:
	}
}

func dwsBaseConfig() IMConfig {
	return IMConfig{DingEnabled: true, DingExplicit: true, DingTransport: "dws",
		DWSCmd: "dws", ListenKind: "at-me"}
}

func newDWSFakeManager(t *testing.T) (*Runtime, *IMManager, *fakeDWSChannel, *senderRegistry) {
	t.Helper()
	rt, _, _ := newIMTestRuntime(t)
	dws := &fakeDWSChannel{events: make(chan im.RawEvent, 8), auth: dwsAuth{LoggedIn: true, Account: "ops"}}
	reg := &senderRegistry{senders: map[string]im.Sender{}}
	m := NewIMManager(rt, IMManagerOptions{
		Logger: testLogger(t), PollInterval: 10 * time.Millisecond,
		BuildDWS: func(IMConfig) (dwsChannel, error) { return dws, nil },
		RegisterSender: func(kind string, s im.Sender) {
			reg.mu.Lock()
			reg.registered = append(reg.registered, kind)
			reg.senders[kind] = s
			reg.mu.Unlock()
		},
		UnregisterSender: func(kind string) {
			reg.mu.Lock()
			reg.unregistered = append(reg.unregistered, kind)
			delete(reg.senders, kind)
			reg.mu.Unlock()
		},
	})
	return rt, m, dws, reg
}

type senderRegistry struct {
	mu           sync.Mutex
	registered   []string
	unregistered []string
	senders      map[string]im.Sender
}

func (r *senderRegistry) counts(kind string) (registered, unregistered int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, k := range r.registered {
		if k == kind {
			registered++
		}
	}
	for _, k := range r.unregistered {
		if k == kind {
			unregistered++
		}
	}
	return
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", what)
}

// DWS runs as a long-lived subscription: no kv cursor is written for it, its
// events become Triggers, and CheckChannel reuses the running consumer instead
// of building a second client (P0-6).
func TestIMDWSIsSubscriptionAndCheckReusesRunner(t *testing.T) {
	rt, m, dws, _ := newDWSFakeManager(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.Start(ctx, dwsBaseConfig())
	defer m.Stop()

	dws.Push(im.RawEvent{ID: "e1", Text: "把 webapp 的登录超时改一下",
		Actor: domain.Actor{ID: "colleague"},
		Conv:  domain.Conversation{ID: "dm", Kind: domain.ConvDirect}, At: time.Now().UnixMilli()})
	waitFor(t, "a trigger from the DWS consumer", func() bool {
		triggers, _, _ := rt.eng.Counts()
		return triggers > 0
	})
	if _, ok, _ := m.store().Get("im.cursor.dingtalk.dws"); ok {
		t.Fatal("DWS wrote a poll cursor; it has no cursor (long-lived subscription)")
	}
	if dws.subs.Load() == 0 {
		t.Fatal("DWS was not started as a Receiver")
	}

	before := m.dwsBuilt
	state, _, _, err := m.CheckChannel("dingtalk")
	if err != nil || state != IMReady {
		t.Fatalf("CheckChannel = %q/%v", state, err)
	}
	if got := dws.authCalls.Load(); got != 1 {
		t.Fatalf("auth calls = %d, want CheckChannel to use the running consumer once", got)
	}
	if m.dwsBuilt != before {
		t.Fatalf("CheckChannel built another DWS client (%d -> %d)", before, m.dwsBuilt)
	}
}

// Stop must cancel, close the driver and withdraw the outbox sender; it is
// idempotent and must not leave the source row claiming to listen.
func TestIMStopClosesDriverAndWithdrawsSender(t *testing.T) {
	rt, m, dws, reg := newDWSFakeManager(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.Start(ctx, dwsBaseConfig())
	waitFor(t, "registered sender", func() bool {
		n, _ := reg.counts("dingtalk")
		return n == 1
	})
	m.Stop()
	m.Stop() // idempotent

	if dws.closed.Load() == 0 {
		t.Fatal("Stop did not close the DWS consumer")
	}
	if _, unreg := reg.counts("dingtalk"); unreg == 0 {
		t.Fatal("Stop did not withdraw the outbox sender")
	}
	row := findSourceRow(t, rt, "dingtalk")
	if row.Enabled {
		t.Fatalf("source row still claims enabled after Stop: %+v", row)
	}
}

// Disabling a channel withdraws its sender and must not register a new one.
func TestIMDisableWithdrawsSender(t *testing.T) {
	rt, m, _, reg := newDWSFakeManager(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	enabled := dwsBaseConfig()
	m.Start(ctx, enabled)
	defer m.Stop()
	waitFor(t, "registered sender", func() bool { n, _ := reg.counts("dingtalk"); return n == 1 })

	disabled := enabled
	disabled.DingEnabled = false
	disabled.DingExplicit = true
	m.ApplyIMSettings(enabled, disabled)
	if _, unreg := reg.counts("dingtalk"); unreg == 0 {
		t.Fatal("disable did not withdraw the outbox sender")
	}
	if n, _ := reg.counts("dingtalk"); n != 1 {
		t.Fatalf("disable registered another sender: %d", n)
	}
	if row := findSourceRow(t, rt, "dingtalk"); row.Enabled {
		t.Fatalf("disabled source row still enabled: %+v", row)
	}
}

// Concurrent saves must never double-start one channel: applyMu serializes them
// and a stale generation cannot overwrite the new channel's state.
func TestIMConcurrentApplyNoDoubleStart(t *testing.T) {
	rt, _, _ := newIMTestRuntime(t)
	var built atomic.Int32
	var active, maxActive atomic.Int32
	m := NewIMManager(rt, IMManagerOptions{
		Logger: testLogger(t), PollInterval: 5 * time.Millisecond,
		BuildFeishu: func(IMConfig) (im.Provider, error) {
			built.Add(1)
			return &activePoller{active: &active, maxActive: &maxActive, name: "feishu"}, nil
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	base := IMConfig{FeishuEnabled: true, FeishuExplicit: true, FeishuTransport: "longpoll",
		FeishuAppID: "a", FeishuAppSecret: "b"}
	m.Start(ctx, base)
	defer m.Stop()

	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		cfg := base
		cfg.FeishuAppSecret = "b" + string(rune('a'+i))
		wg.Add(1)
		go func() { defer wg.Done(); m.ApplyIMSettings(base, cfg) }()
	}
	wg.Wait()
	time.Sleep(150 * time.Millisecond)
	if maxActive.Load() > 1 {
		t.Fatalf("two Feishu pollers ran at once (max %d)", maxActive.Load())
	}
	if built.Load() < 2 {
		t.Fatalf("restarts did not build new drivers: %d", built.Load())
	}
}

// activePoller tracks how many poll loops are alive at once.
type activePoller struct {
	name      string
	active    *atomic.Int32
	maxActive *atomic.Int32
	closed    atomic.Int32
}

func (p *activePoller) Name() string { return p.name }
func (p *activePoller) Capabilities() im.Capabilities {
	return im.Capabilities{MaxTextLen: 4000}
}
func (p *activePoller) Self(context.Context) (im.Identity, error) {
	return im.Identity{ID: "bot"}, nil
}
func (p *activePoller) Members(context.Context, string) ([]im.Member, error) { return nil, nil }
func (p *activePoller) Poll(ctx context.Context, cursor string) ([]im.RawEvent, string, error) {
	n := p.active.Add(1)
	for {
		old := p.maxActive.Load()
		if n <= old || p.maxActive.CompareAndSwap(old, n) {
			break
		}
	}
	<-ctx.Done()
	p.active.Add(-1)
	return nil, cursor, ctx.Err()
}
func (p *activePoller) Send(context.Context, im.Outbound) (im.Receipt, error) {
	return im.Receipt{MessageID: "m"}, nil
}
func (p *activePoller) ReadThread(context.Context, domain.Conversation, string) (im.Page, error) {
	return im.Page{Done: true}, nil
}
func (p *activePoller) Close() error { p.closed.Add(1); return nil }

// Async start: a slow driver.Self (network) must not block Start/SaveSettings.
func TestIMStartDoesNotBlockOnSelf(t *testing.T) {
	rt, _, _ := newIMTestRuntime(t)
	release := make(chan struct{})
	drv := &fakePoller{name: "feishu", slow: release}
	m := NewIMManager(rt, IMManagerOptions{
		Logger: testLogger(t), PollInterval: 10 * time.Millisecond,
		SelfTimeout: 30 * time.Millisecond,
		BuildFeishu: func(IMConfig) (im.Provider, error) { return drv, nil },
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	start := time.Now()
	m.Start(ctx, IMConfig{FeishuEnabled: true, FeishuExplicit: true, FeishuTransport: "longpoll",
		FeishuAppID: "a", FeishuAppSecret: "b"})
	if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
		close(release)
		t.Fatalf("Start blocked on Self for %s; it must be asynchronous (got %s)", elapsed, elapsed)
	}
	close(release)
	m.Stop()
}

// Transport changes clear the old read position: a DWS subscription id must not
// be fed to Feishu, and vice versa (P1-11).
func TestIMTransportChangeClearsCursor(t *testing.T) {
	_, m, _, _ := newDWSFakeManager(t)
	st := m.store()
	if st == nil {
		t.Fatal("no store")
	}
	_ = st.Set("im.cursor.dingtalk.dws", "dws-session-1")
	_ = st.Set("im.cursor.dingtalk", "legacy-cursor")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dwsCfg := dwsBaseConfig()
	m.Start(ctx, dwsCfg)

	streamCfg := dwsCfg
	streamCfg.DingTransport = "stream"
	streamCfg.DingAppKey, streamCfg.DingAppSecret, streamCfg.DingRobot = "k", "s", "r"
	m.ApplyIMSettings(dwsCfg, streamCfg)
	defer m.Stop()

	for _, key := range []string{"im.cursor.dingtalk.dws", "im.cursor.dingtalk"} {
		if v, ok, _ := st.Get(key); ok && strings.TrimSpace(v) != "" {
			t.Fatalf("cursor %q still holds %q after transport switch", key, v)
		}
	}
}

// Explicit "0" always wins; absent keys derive from credentials/flags (P0-4).
func TestIMEnabledTriState(t *testing.T) {
	creds := map[string]string{
		"source.feishu_app_id":     "a",
		"source.feishu_app_secret": "b",
	}
	cfg := IMConfigFromSettings(creds)
	if !cfg.FeishuEnabled || cfg.FeishuExplicit {
		t.Fatalf("derived enabled = %+v", cfg)
	}

	off := map[string]string{
		"source.feishu_enabled":      "0",
		"source.feishu_app_id":       "a",
		"source.feishu_app_secret":   "b",
		"source.dingtalk_app_key":    "k",
		"source.dingtalk_app_secret": "s",
	}
	cfg = IMConfigFromSettings(off)
	if cfg.FeishuEnabled || !cfg.FeishuExplicit {
		t.Fatalf("explicit 0 was not honored: %+v", cfg)
	}
	if cfg.FeishuEnabled = cfg.WithFlags(IMFlags{FeishuAppID: "x", FeishuAppSecret: "y"}).FeishuEnabled; cfg.FeishuEnabled {
		t.Fatal("flags re-enabled an explicitly disabled channel")
	}
	// Stream credentials are not DWS evidence: under the default (absent)
	// transport they must not silently enable DWS.
	if cfg.DingEnabled {
		t.Fatalf("stream credentials enabled DWS under the default transport: %+v", cfg)
	}
	stream := IMConfigFromSettings(map[string]string{
		"source.dingtalk_transport":  "stream",
		"source.dingtalk_app_key":    "k",
		"source.dingtalk_app_secret": "s",
		"source.dingtalk_robot":      "r",
	})
	if !stream.DingEnabled || stream.DingTransport != "stream" {
		t.Fatalf("explicit stream creds did not derive enablement: %+v", stream)
	}
	// DWS evidence (profile) does enable it when the key is absent.
	dwsCfg := IMConfigFromSettings(map[string]string{"source.dingtalk_profile": "work"})
	if !dwsCfg.DingEnabled || dwsCfg.DingTransport != "dws" {
		t.Fatalf("dws profile did not derive enablement: %+v", dwsCfg)
	}
}

// imTrigger: Global scope, conversation in the id, zero time replaced by now and
// the per-channel self id used for @ detection (P1-8).
func TestIMTriggerIdentityFacts(t *testing.T) {
	ev := im.RawEvent{ID: "m1", Text: "hi", Actor: domain.Actor{ID: "u1"},
		Conv: domain.Conversation{ID: "dm-1", Kind: domain.ConvDirect}, At: 0}
	tr := imTrigger("dingtalk", ev, "bot-self")
	if tr.Scope != domain.Global {
		t.Fatalf("scope = %q, want global", tr.Scope)
	}
	if tr.ID == "" || tr.ID == imTrigger("dingtalk", im.RawEvent{ID: "m1",
		Conv: domain.Conversation{ID: "dm-2", Kind: domain.ConvDirect}}, "bot-self").ID {
		t.Fatal("identical message ids in different conversations produced the same Trigger id")
	}
	if tr.At.IsZero() || time.Since(tr.At) > time.Minute || tr.At.Year() < 2000 {
		t.Fatalf("zero event time was not replaced with now: %s", tr.At)
	}
	group := im.RawEvent{ID: "m2", Text: "hi", Actor: domain.Actor{ID: "u1"},
		Conv:     domain.Conversation{ID: "g1", Kind: domain.ConvGroup},
		Mentions: []string{"feishu-self"}, At: time.Now().UnixMilli()}
	if !imTrigger("feishu", group, "feishu-self").Addressed.Mention {
		t.Fatal("per-channel self id was not used for @ detection")
	}
	if imTrigger("feishu", group, "dingtalk-bot").Addressed.Mention {
		t.Fatal("another channel's self id matched the mention")
	}
}

// A failing poll backs off instead of busy-looping (P1-11).
func TestIMPollFailureBacksOff(t *testing.T) {
	rt, _, _ := newIMTestRuntime(t)
	drv := &fakePoller{name: "feishu", err: errors.New("dws: connection refused")}
	m := NewIMManager(rt, IMManagerOptions{
		Logger: testLogger(t), PollInterval: 5 * time.Millisecond,
		BuildFeishu: func(IMConfig) (im.Provider, error) { return drv, nil },
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.Start(ctx, IMConfig{FeishuEnabled: true, FeishuExplicit: true, FeishuTransport: "longpoll",
		FeishuAppID: "a", FeishuAppSecret: "b"})
	defer m.Stop()
	time.Sleep(250 * time.Millisecond)
	if n := drv.polls.Load(); n > 4 {
		t.Fatalf("failing poll was retried %d times in 250ms (busy loop)", n)
	}
}

// The cursor is namespaced per channel+transport; the legacy per-channel key is
// only a one-time migration source.
func TestIMCursorNamespacedAndLegacyMigrated(t *testing.T) {
	rt, _, _ := newIMTestRuntime(t)
	drv := &fakePoller{name: "feishu"}
	m := NewIMManager(rt, IMManagerOptions{
		Logger: testLogger(t), PollInterval: 10 * time.Millisecond,
		BuildFeishu: func(IMConfig) (im.Provider, error) { return drv, nil },
	})
	st := m.store()
	if st == nil {
		t.Fatal("no store")
	}
	_ = st.Set("im.cursor.feishu", "legacy-1")
	cfg := IMConfig{FeishuEnabled: true, FeishuExplicit: true, FeishuTransport: "longpoll",
		FeishuAppID: "a", FeishuAppSecret: "b"}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.Start(ctx, cfg)
	defer m.Stop()
	waitFor(t, "namespaced cursor", func() bool {
		v, ok, _ := st.Get("im.cursor.feishu.longpoll")
		return ok && v == "c2"
	})
}

// Keep the view package used even when a future edit removes the row checks.
var _ = view.PollOK
var _ = engine.ErrDuplicate

// --- real DWS adapter with a fake CLI (NDJSON) -------------------------------

// fakeDWSCLI is the CLI seam: Run answers auth/send JSON, StartStream emits NDJSON
// lines exactly like `dws event +listen-im ... -f ndjson`.
type fakeDWSCLI struct {
	mu    sync.Mutex
	lines []string
	runs  []string
	push  func(string)
}

func (f *fakeDWSCLI) Run(_ context.Context, args ...string) ([]byte, []byte, error) {
	joined := strings.Join(args, " ")
	f.mu.Lock()
	f.runs = append(f.runs, joined)
	f.mu.Unlock()
	switch {
	case strings.Contains(joined, "auth status"):
		return []byte(`{"loggedIn":true,"account":"ops","profile":"work"}`), nil, nil
	case strings.Contains(joined, "messages-send"):
		return []byte(`{"messageId":"sent-1"}`), nil, nil
	default:
		return nil, []byte("fake dws: unknown command " + joined), errors.New("fake dws: unknown command")
	}
}

func (f *fakeDWSCLI) StartStream(ctx context.Context, args ...string) (dingtalk.DWSStream, error) {
	f.mu.Lock()
	f.runs = append(f.runs, strings.Join(args, " "))
	lines := append([]string(nil), f.lines...)
	push := f.push
	f.mu.Unlock()
	r, w := io.Pipe()
	stream := &fakeDWSStream{stdout: r, stderr: strings.NewReader(""), kill: make(chan struct{})}
	go func() {
		defer w.Close()
		for _, line := range lines {
			if _, err := io.WriteString(w, line+"\n"); err != nil {
				return
			}
		}
		if push != nil {
			push = nil
		}
		<-ctx.Done()
	}()
	return stream, nil
}

func (f *fakeDWSCLI) countRun(needle string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, run := range f.runs {
		if strings.Contains(run, needle) {
			n++
		}
	}
	return n
}

type fakeDWSStream struct {
	stdout io.Reader
	stderr io.Reader
	kill   chan struct{}
	once   sync.Once
}

func (s *fakeDWSStream) Stdout() io.Reader { return s.stdout }
func (s *fakeDWSStream) Stderr() io.Reader { return s.stderr }
func (s *fakeDWSStream) Wait() error {
	<-s.kill
	return nil
}
func (s *fakeDWSStream) Kill() error {
	s.once.Do(func() { close(s.kill) })
	return nil
}

// The runtime runs the real nih-integrations DWS adapter over a fake CLI: NDJSON
// inbound becomes a pending approval, auth and send go through the same channel,
// and no poll/cursor appears (DWS is a subscription).
func TestIMDWSFakeCLINdjson(t *testing.T) {
	rt, _ := newApprovalRuntime(t)
	line := `{"event_id":"evt-1","event_type":"im.message.receive_v1","conversation_id":"cid-1",` +
		`"conversation_type":"direct","sender_open_dingtalk_id":"ding_zhangsan","create_time":` +
		fmt.Sprint(time.Now().UnixMilli()) + `,"text":"把 webapp 的登录超时改一下"}`
	cli := &fakeDWSCLI{lines: []string{line}}
	m := NewIMManager(rt, IMManagerOptions{
		Logger: testLogger(t), PollInterval: 10 * time.Millisecond,
		DWSCLI: cli,
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cfg := dwsBaseConfig()
	cfg.Profile = "work"
	m.Start(ctx, cfg)
	defer m.Stop()

	okEvent := false
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, decisions, _ := rt.eng.Counts(); decisions > 0 {
			okEvent = true
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !okEvent {
		cli.mu.Lock()
		runs := append([]string(nil), cli.runs...)
		cli.mu.Unlock()
		triggers, decisions, tickets := rt.eng.Counts()
		t.Fatalf("no approval from the DWS NDJSON event; CLI runs=%v triggers=%d decisions=%d tickets=%d sources=%+v",
			runs, triggers, decisions, tickets, rt.SourceRows(time.Now()))
	}
	for _, row := range rt.SourceRows(time.Now()) {
		if row.Kind == "dingtalk" {
			found := false
			for _, f := range row.Facts {
				if f.Label == "待审批" {
					found = true
				}
			}
			if !found && row.LastPoll.Outcome != view.PollOK {
				t.Fatalf("dingtalk row = %+v", row)
			}
		}
	}
	if st := m.store(); st != nil {
		if _, ok, _ := st.Get("im.cursor.dingtalk.dws"); ok {
			t.Fatal("DWS wrote a cursor")
		}
	}

	state, detail, _, err := m.CheckChannel("dingtalk")
	if err != nil || state != IMReady || !strings.Contains(detail, "ops") {
		t.Fatalf("CheckChannel over the same consumer = %q/%q/%v", state, detail, err)
	}
	if cli.countRun("auth status") == 0 {
		t.Fatal("CheckChannel did not run `dws auth status` through the injected CLI")
	}

	dws := m.runningDWS("dingtalk")
	if dws == nil {
		t.Fatal("no running DWS channel")
	}
	if _, err := dws.Send(ctx, im.Outbound{Key: "k1", Conv: domain.Conversation{ID: "dm:u1", Kind: domain.ConvDirect}, Text: "done"}); err != nil {
		t.Fatalf("DWS send: %v", err)
	}
	if cli.countRun("messages-send") == 0 {
		t.Fatal("DWS send did not go through the injected CLI")
	}
}

// Sanity: the dingtalk DWS adapter over the fake CLI yields one RawEvent.
func TestDWSAdapterFakeCLIReceivesNDJSON(t *testing.T) {
	line := `{"event_id":"evt-x","event_type":"im.message.receive_v1","conversation_id":"cid-x",` +
		`"conversation_type":"direct","sender_open_dingtalk_id":"ding_zhangsan","create_time":` +
		fmt.Sprint(time.Now().UnixMilli()) + `,"text":"hello"}`
	cli := &fakeDWSCLI{lines: []string{line}}
	ch, err := dingtalk.NewDWSChannel(dingtalk.DWSConfig{ListenKind: "at-me"}, cli)
	if err != nil {
		t.Fatal(err)
	}
	defer ch.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out := make(chan im.RawEvent, 4)
	go func() { _ = ch.Subscribe(ctx, out) }()
	select {
	case ev := <-out:
		if ev.ID != "evt-x" || ev.Text != "hello" {
			t.Fatalf("event = %+v", ev)
		}
	case <-ctx.Done():
		state, detail := ch.Status()
		t.Fatalf("no RawEvent from NDJSON; status=%s/%s dropped=%d", state, detail, ch.Dropped())
	}
}

// Feishu ws is a real transport in this build: readiness must accept it (and
// long poll stays the fallback); an unknown transport is still an honest error.
func TestIMFeishuWSTransportReadiness(t *testing.T) {
	ws := IMConfigFromSettings(map[string]string{
		"source.feishu_transport":  "ws",
		"source.feishu_app_id":     "a",
		"source.feishu_app_secret": "b",
	})
	if !ws.FeishuEnabled {
		t.Fatalf("ws credentials did not derive enablement: %+v", ws)
	}
	if enabled, half, problem := channelReadiness(ws, "feishu"); !enabled || half {
		t.Fatalf("ws readiness = %v/%v/%q (feishuWSAvailable=%v)", enabled, half, problem, feishuWSAvailable)
	}
	bad := ws
	bad.FeishuTransport = "webhook"
	if enabled, half, problem := channelReadiness(bad, "feishu"); enabled || !half || !strings.Contains(problem, "webhook") {
		t.Fatalf("webhook readiness = %v/%v/%q", enabled, half, problem)
	}
}
