package runtime

// IM channel management tests (im-channel-spec §5): defaults, disabled means no
// listener, half configuration is an error, start/stop on settings change, cursor
// persistence, and CheckChannel's ready/auth/error states.

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zeokeel/super-agent/internal/domain"
	"github.com/zeokeel/super-agent/internal/engine"
	"github.com/zeokeel/super-agent/internal/im"
	"github.com/zeokeel/super-agent/internal/im/dingtalk"
	"github.com/zeokeel/super-agent/internal/store"
	"github.com/zeokeel/super-agent/internal/view"
)

// fakeIM is a driver that answers Poll from a scripted list.
type fakeIM struct {
	name   string
	events []im.RawEvent
	next   string
	err    error
	polls  atomic.Int32
	sent   []im.Outbound
}

func (f *fakeIM) Name() string { return f.name }
func (f *fakeIM) Capabilities() im.Capabilities {
	return im.Capabilities{Markdown: true, Mentions: true, MaxTextLen: 4000}
}
func (f *fakeIM) Self(context.Context) (im.Identity, error) {
	return im.Identity{ID: "bot"}, nil
}
func (f *fakeIM) Members(context.Context, string) ([]im.Member, error) { return nil, nil }
func (f *fakeIM) Poll(_ context.Context, cursor string) ([]im.RawEvent, string, error) {
	f.polls.Add(1)
	if f.err != nil {
		return nil, cursor, f.err
	}
	evs := f.events
	f.events = nil // one batch, then empty pages
	return evs, orDefault(f.next, cursor), nil
}
func (f *fakeIM) Send(_ context.Context, msg im.Outbound) (im.Receipt, error) {
	f.sent = append(f.sent, msg)
	return im.Receipt{MessageID: "m-" + msg.Key}, nil
}
func (f *fakeIM) ReadThread(context.Context, domain.Conversation, string) (im.Page, error) {
	return im.Page{Done: true}, nil
}

func newIMTestRuntime(t *testing.T) (*Runtime, *store.Store, *view.Snapshot) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "zeokeel.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	eng := engine.New(engine.RuleAssessor{})
	eng.Register(domain.Workspace{ID: "ws_web", Name: "web", Root: t.TempDir(), Kind: domain.KindRepo,
		Brief: domain.Brief{Purpose: "webapp", Keywords: []string{"webapp"}}}, engine.Policy{Version: "v1"})
	snap := view.NewSnapshot()
	rt := New(eng, snap)
	if err := rt.LoadSettings(st); err != nil {
		t.Fatal(err)
	}
	return rt, st, snap
}

func TestIMConfigDefaults(t *testing.T) {
	cfg := IMConfigFromSettings(nil)
	if cfg.FeishuEnabled || cfg.DingEnabled {
		t.Fatal("channels must default to disabled")
	}
	if cfg.FeishuTransport != "longpoll" || cfg.DingTransport != "dws" || cfg.DWSCmd != "dws" {
		t.Fatalf("defaults = %+v", cfg)
	}
}

func TestIMDisabledMeansNoListener(t *testing.T) {
	rt, _, _ := newIMTestRuntime(t)
	m := NewIMManager(rt, IMManagerOptions{Logger: testLogger(t), PollInterval: 10 * time.Millisecond})
	built := 0
	m.opts.BuildFeishu = func(IMConfig) (im.Provider, error) {
		built++
		return &fakeIM{name: "feishu"}, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.Start(ctx, IMConfig{FeishuTransport: "longpoll", DingTransport: "dws"})
	defer m.Stop()

	if built != 0 {
		t.Fatal("a disabled channel built a listener")
	}
	rows := rt.SourceRows(time.Now())
	if len(rows) != 2 { // both channels report honestly: disabled
		t.Fatalf("source rows = %+v", rows)
	}
	for _, row := range rows {
		if row.Enabled {
			t.Fatalf("disabled channel claimed to be enabled: %+v", row)
		}
		if !containsAll(row.LastPoll.Detail, "未启用") {
			t.Fatalf("disabled channel detail = %q", row.LastPoll.Detail)
		}
	}
}

func TestIMHalfConfigIsAnErrorRow(t *testing.T) {
	rt, st, _ := newIMTestRuntime(t)
	m := NewIMManager(rt, IMManagerOptions{Logger: testLogger(t), PollInterval: 10 * time.Millisecond})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.Start(ctx, IMConfig{FeishuEnabled: true, FeishuTransport: "longpoll", FeishuAppID: "cli_x", DingTransport: "dws"})
	defer m.Stop()

	feishu := findSourceRow(t, rt, "feishu")
	if feishu.Enabled || !containsAll(feishu.LastPoll.Problem, "配置不完整", "app secret") {
		t.Fatalf("half config row = %+v", feishu)
	}
	entries, err := st.HealthEntries()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range entries {
		if e.Entry == "im:feishu" && e.State == "error" {
			found = true
		}
	}
	if !found {
		t.Fatalf("health entries = %+v", entries)
	}
}

func TestIMChannelPollsAndPersistsCursor(t *testing.T) {
	rt, st, _ := newIMTestRuntime(t)
	fake := &fakeIM{name: "feishu", next: "c2", events: []im.RawEvent{{
		ID: "m1", Text: "把 webapp 的登录超时改一下",
		Actor: domain.Actor{ID: "colleague"},
		Conv:  domain.Conversation{ID: "dm", Kind: domain.ConvDirect},
		At:    time.Now().UnixMilli(),
	}}}
	m := NewIMManager(rt, IMManagerOptions{
		Logger: testLogger(t), PollInterval: 10 * time.Millisecond,
		BuildFeishu: func(IMConfig) (im.Provider, error) { return fake, nil },
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.Start(ctx, IMConfig{FeishuEnabled: true, FeishuTransport: "longpoll",
		FeishuAppID: "cli_x", FeishuAppSecret: "sec", DingTransport: "dws"})
	defer m.Stop()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if v, ok, _ := st.Get("im.cursor.feishu"); ok && v == "c2" {
			row := findSourceRow(t, rt, "feishu")
			if !row.Enabled || row.LastPoll.Outcome != view.PollOK {
				t.Fatalf("row after poll = %+v", row)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("cursor was never persisted after a successful batch")
}

func TestIMCursorNotAdvancedOnFailure(t *testing.T) {
	rt, st, _ := newIMTestRuntime(t)
	fake := &fakeIM{name: "feishu", err: errors.New("401 invalid_token")}
	m := NewIMManager(rt, IMManagerOptions{
		Logger: testLogger(t), PollInterval: 10 * time.Millisecond,
		BuildFeishu: func(IMConfig) (im.Provider, error) { return fake, nil },
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.Start(ctx, IMConfig{FeishuEnabled: true, FeishuTransport: "longpoll",
		FeishuAppID: "a", FeishuAppSecret: "b", DingTransport: "dws"})
	defer m.Stop()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		row := findSourceRow(t, rt, "feishu")
		if !row.Enabled && row.LastPoll.Outcome != "" {
			break
		}
		if row.LastPoll.Problem != "" {
			if _, ok, _ := st.Get("im.cursor.feishu"); ok {
				t.Fatal("cursor advanced after a failed batch")
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the auth failure was never classified")
}

func TestApplyIMSettingsRestartsChangedChannels(t *testing.T) {
	rt, _, _ := newIMTestRuntime(t)
	built := 0
	m := NewIMManager(rt, IMManagerOptions{
		Logger: testLogger(t), PollInterval: 10 * time.Millisecond,
		BuildFeishu: func(IMConfig) (im.Provider, error) {
			built++
			return &fakeIM{name: "feishu", next: "c1"}, nil
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	base := IMConfig{FeishuEnabled: true, FeishuTransport: "longpoll",
		FeishuAppID: "a", FeishuAppSecret: "b", DingTransport: "dws"}
	m.Start(ctx, base)
	defer m.Stop()
	waitBuilt := func(want int) {
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			if built == want {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("built = %d, want %d", built, want)
	}
	waitBuilt(1)

	// Unchanged config: no restart.
	m.ApplyIMSettings(base, base)
	time.Sleep(100 * time.Millisecond)
	waitBuilt(1)

	// Credential change: restart exactly once.
	changed := base
	changed.FeishuAppSecret = "b2"
	m.ApplyIMSettings(base, changed)
	waitBuilt(2)
}

func TestCheckChannelFeishu(t *testing.T) {
	old := httpDo
	defer func() { httpDo = old }()

	rt, _, _ := newIMTestRuntime(t)
	m := NewIMManager(rt, IMManagerOptions{Logger: testLogger(t), FeishuBaseURL: "https://feishu.test"})
	// CheckChannel reads the current config, so give the manager one (disabled is
	// fine: the check itself is what is under test).
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer m.Stop()
	m.Start(ctx, IMConfig{FeishuAppID: "cli_x", FeishuAppSecret: "sec",
		FeishuTransport: "longpoll", DingTransport: "dws"})

	var gotURL string
	httpDo = func(req *http.Request) (*http.Response, error) {
		gotURL = req.URL.String()
		return ioResponse(200, `{"code":0,"tenant_access_token":"t"}`), nil
	}
	state, detail, _, err := m.CheckChannel("feishu")
	if err != nil || state != IMReady {
		t.Fatalf("ready check = %q/%q/%v (url %s)", state, detail, err, gotURL)
	}

	httpDo = func(*http.Request) (*http.Response, error) {
		return ioResponse(401, `{"code":10003,"msg":"invalid param"}`), nil
	}
	state, detail, authURL, err := m.CheckChannel("feishu")
	if err != nil || state != IMAuth || authURL != "https://open.feishu.cn/app" {
		t.Fatalf("auth check = %q/%q/%q/%v", state, detail, authURL, err)
	}

	httpDo = func(*http.Request) (*http.Response, error) { return nil, errors.New("network down") }
	state, _, _, err = m.CheckChannel("feishu")
	if err == nil || state != IMError {
		t.Fatalf("network failure = %q/%v", state, err)
	}
}

type fakeDWS struct {
	auth dingtalk.AuthStatus
	err  error
}

func (f *fakeDWS) AuthStatus(context.Context) (dingtalk.AuthStatus, error) {
	if f.err != nil {
		return dingtalk.AuthStatus{}, f.err
	}
	return f.auth, nil
}
func (f *fakeDWS) Poll(context.Context, string, int) (dingtalk.PollPage, error) {
	return dingtalk.PollPage{}, nil
}
func (f *fakeDWS) Send(context.Context, string, string, string) (string, error) { return "m1", nil }
func (f *fakeDWS) Health(context.Context) (dingtalk.DWSHealth, error) {
	return dingtalk.DWSHealth{State: "ok"}, nil
}

func TestCheckChannelDWS(t *testing.T) {
	rt, _, _ := newIMTestRuntime(t)
	dws := &fakeDWS{auth: dingtalk.AuthStatus{LoggedIn: false, AuthURL: "https://login.test"}}
	m := NewIMManager(rt, IMManagerOptions{Logger: testLogger(t), DWS: dws})
	state, detail, url, err := m.CheckChannel("dingtalk")
	if err != nil || state != IMAuth || url != "https://login.test" || !containsAll(detail, "auth login") {
		t.Fatalf("dws auth check = %q/%q/%q/%v", state, detail, url, err)
	}

	dws.auth = dingtalk.AuthStatus{LoggedIn: true, Account: "ops"}
	state, detail, _, err = m.CheckChannel("dingtalk")
	if err != nil || state != IMReady || !containsAll(detail, "ops") {
		t.Fatalf("dws ready check = %q/%q/%v", state, detail, err)
	}

	if _, _, _, err := m.CheckChannel("nope"); err == nil {
		t.Fatal("an unknown channel did not error")
	}
}

func findSourceRow(t *testing.T, rt *Runtime, kind string) view.SourceRow {
	t.Helper()
	for _, row := range rt.SourceRows(time.Now()) {
		if row.Kind == kind {
			return row
		}
	}
	t.Fatalf("no source row for %q: %+v", kind, rt.SourceRows(time.Now()))
	return view.SourceRow{}
}

func testLogger(t *testing.T) *slog.Logger {
	t.Helper()
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func containsAll(s string, needles ...string) bool {
	for _, n := range needles {
		if !strings.Contains(s, n) {
			return false
		}
	}
	return true
}

func ioResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(bytes.NewBufferString(body)),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
	}
}

// A chat message keeps the driver's facts: @a colleague is not addressing the
// agent, and a bot is not a person.
func TestIMEventBecomesATrigger(t *testing.T) {
	ev := im.RawEvent{
		ID: "am-1", Text: "把登录超时改成可配置",
		Actor: domain.Actor{ID: "ding"},
		Conv:  domain.Conversation{Kind: domain.ConvDirect, ID: "dm-1"},
		At:    time.Now().UnixMilli(),
	}
	tr := imTrigger("dingtalk", ev, "bot-self")
	if tr.Source != "dingtalk" || !tr.Human || !tr.Addressed.Direct {
		t.Fatalf("bad trigger: %+v", tr)
	}
	if tr.Channel.Thread != "dm-1" {
		t.Fatal("the reply must go back to the same conversation")
	}
	ev.Conv = domain.Conversation{Kind: domain.ConvGroup, ID: "grp-1"}
	ev.Mentions = []string{"someone-else"}
	tr = imTrigger("dingtalk", ev, "bot-self")
	if tr.Addressed.Mention || tr.Addressed.Direct {
		t.Fatal("mentioning a colleague must not address the agent")
	}
	ev.Actor.Bot = true
	if tr := imTrigger("dingtalk", ev, "bot-self"); tr.Human {
		t.Fatal("a bot message must not count as a person")
	}
}
