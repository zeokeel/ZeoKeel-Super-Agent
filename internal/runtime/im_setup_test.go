package runtime

// IM setup usability tests (UX U-03/A18): draft checks use unsaved values, DWS
// install/version/login facts, per-transport required fields, empty-chat WS vs
// long-poll validation, and the connection state model published on source rows.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zeokeel/super-agent/internal/domain"
	"github.com/zeokeel/super-agent/internal/im"
	"github.com/zeokeel/super-agent/internal/im/dingtalk"
)

// setupCLI is a fake `dws` CLI: version/auth are scriptable, StartStream is not
// used by the probes, and every run is recorded so tests can prove the panel
// never performs an interactive login itself.
type setupCLI struct {
	mu       sync.Mutex
	runs     [][]string
	version  string
	missing  bool
	authJSON string
	authErr  error
}

func (c *setupCLI) Run(_ context.Context, args ...string) ([]byte, []byte, error) {
	c.mu.Lock()
	c.runs = append(c.runs, append([]string(nil), args...))
	c.mu.Unlock()
	if c.missing {
		return nil, nil, &exec.Error{Name: "dws", Err: exec.ErrNotFound}
	}
	joined := strings.Join(args, " ")
	switch {
	case strings.Contains(joined, "auth status"):
		if c.authErr != nil {
			return nil, nil, c.authErr
		}
		out := c.authJSON
		if out == "" {
			out = `{"loggedIn":true,"account":"ops","profile":"work"}`
		}
		return []byte(out), nil, nil
	case strings.Contains(joined, "version"):
		v := c.version
		if v == "" {
			v = "dws v1.2.3"
		}
		return []byte(v + "\n"), nil, nil
	default:
		return nil, nil, errors.New("setupCLI: unexpected command " + joined)
	}
}

func (c *setupCLI) StartStream(context.Context, ...string) (dingtalk.DWSStream, error) {
	return nil, errors.New("setupCLI: StartStream is not used by setup probes")
}

func (c *setupCLI) ran(substr string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, run := range c.runs {
		if strings.Contains(strings.Join(run, " "), substr) {
			return true
		}
	}
	return false
}

func newSetupManager(t *testing.T, cli dingtalk.DWSCLI) (*Runtime, *IMManager) {
	t.Helper()
	rt, _, _ := newIMTestRuntime(t)
	return rt, NewIMManager(rt, IMManagerOptions{
		Logger: testLogger(t), PollInterval: 10 * time.Millisecond, DWSCLI: cli,
	})
}

// A draft check must use the values being typed, not the saved configuration,
// and must leave the saved configuration untouched (A18: the old button checked
// the saved values).
func TestCheckChannelDraftUsesUnsavedValues(t *testing.T) {
	rt, m := newSetupManager(t, &setupCLI{})
	old := httpDo
	defer func() { httpDo = old }()
	var body string
	httpDo = func(req *http.Request) (*http.Response, error) {
		buf := make([]byte, 512)
		n, _ := req.Body.Read(buf)
		body = string(buf[:n])
		return ioResponse(200, `{"code":0,"tenant_access_token":"t"}`), nil
	}

	state, detail, _, err := m.CheckChannelDraft("feishu", map[string]string{
		"feishu_transport":  "longpoll",
		"feishu_app_id":     "draft_id",
		"feishu_app_secret": "draft_secret",
		"feishu_chats":      "oc_draft",
	})
	if err != nil || state != IMStateReady {
		t.Fatalf("draft feishu = %q/%q/%v", state, detail, err)
	}
	if !strings.Contains(body, "draft_id") {
		t.Fatalf("draft check did not use the typed app id; body=%q", body)
	}
	if cfg := rt.IMConfig(); cfg.FeishuAppID != "" || cfg.FeishuEnabled {
		t.Fatalf("draft check mutated the saved configuration: %+v", cfg)
	}
}

// Long polling needs a chat list; the WS transport does not (empty = every chat
// the app can see), and drafts typed with WS must validate without chats.
func TestValidateChannelConfigTransportFields(t *testing.T) {
	wsIssues := ValidateChannelConfig("feishu", map[string]string{
		"feishu_transport": "ws", "feishu_app_id": "a", "feishu_app_secret": "b",
	})
	if len(wsIssues) != 0 {
		t.Fatalf("ws without chats = %+v, want no issues", wsIssues)
	}
	longIssues := ValidateChannelConfig("feishu", map[string]string{
		"feishu_transport": "longpoll", "feishu_app_id": "a", "feishu_app_secret": "b",
	})
	if len(longIssues) == 0 || longIssues[0].Field != "feishu_chats" {
		t.Fatalf("longpoll without chats = %+v, want a feishu_chats issue", longIssues)
	}
	streamIssues := ValidateChannelConfig("dingtalk", map[string]string{
		"dingtalk_transport": "stream",
	})
	if len(streamIssues) < 3 {
		t.Fatalf("stream missing fields = %+v", streamIssues)
	}
	if issues := ValidateChannelConfig("dingtalk", map[string]string{
		"dingtalk_transport": "dws", "dingtalk_listen_kind": "nope",
	}); len(issues) == 0 || issues[0].Field != "dingtalk_listen_kind" {
		t.Fatalf("bad listen kind = %+v", issues)
	}
	if issues := ValidateChannelConfig("nope", nil); len(issues) == 0 {
		t.Fatal("unknown channel validated clean")
	}
}

// The per-transport field model is the backend contract the UI should render:
// DWS fields are profile/listen kind/chat query/cmd/pause; Stream keeps app
// credentials; Feishu chats are required only for long polling.
func TestIMChannelConfigSpecs(t *testing.T) {
	specs := IMChannelConfigSpecs()
	byName := map[string]ChannelConfigSpec{}
	for _, spec := range specs {
		byName[spec.Name] = spec
	}
	feishu := byName["feishu"]
	if len(feishu.Transports) != 2 {
		t.Fatalf("feishu transports = %+v", feishu.Transports)
	}
	var wsChats, longChats *ChannelFieldSpec
	for i := range feishu.Transports {
		tr := feishu.Transports[i]
		for j := range tr.Fields {
			if tr.Fields[j].Key == "feishu_chats" {
				f := tr.Fields[j]
				switch tr.ID {
				case "ws":
					wsChats = &f
				case "longpoll":
					longChats = &f
				}
			}
		}
	}
	if wsChats == nil || wsChats.Required {
		t.Fatalf("ws chats field = %+v (must be optional)", wsChats)
	}
	if longChats == nil || !longChats.Required {
		t.Fatalf("longpoll chats field = %+v (must be required)", longChats)
	}
	dws := byName["dingtalk"].Transports[0]
	if dws.ID != "dws" {
		t.Fatalf("first dingtalk transport = %q", dws.ID)
	}
	keys := map[string]bool{}
	for _, f := range dws.Fields {
		keys[f.Key] = true
		switch f.Key {
		case "dingtalk_app_key", "dingtalk_app_secret":
			t.Fatalf("DWS fields must not include Stream credentials: %+v", dws.Fields)
		}
	}
	for _, want := range []string{"dingtalk_profile", "dingtalk_listen_kind", "dingtalk_chat_query", "dingtalk_dws_cmd", "dingtalk_pause_listen_on_send"} {
		if !keys[want] {
			t.Fatalf("DWS fields missing %q: %+v", want, dws.Fields)
		}
	}
}

// Missing dws is a configuration state with an install action, not a transient
// error: the panel can show "install dws" instead of a red "connection failed".
func TestCheckChannelDraftDWSMissingBinary(t *testing.T) {
	_, m := newSetupManager(t, &setupCLI{missing: true})
	state, detail, _, err := m.CheckChannelDraft("dingtalk", map[string]string{
		"dingtalk_transport": "dws",
	})
	if err != nil || state != IMStateMisconfigured {
		t.Fatalf("missing dws = %q/%q/%v", state, detail, err)
	}
	if !strings.Contains(detail, "未检测到 dws") || !strings.Contains(detail, "安装") {
		t.Fatalf("missing-dws detail is not actionable: %q", detail)
	}
}

// Not logged in returns the auth URL and the exact terminal command; the panel
// never runs `auth login` itself.
func TestCheckChannelDraftDWSNeedsAuth(t *testing.T) {
	cli := &setupCLI{authJSON: `{"loggedIn":false,"authUrl":"https://login.test"}`}
	_, m := newSetupManager(t, cli)
	state, detail, authURL, err := m.CheckChannelDraft("dingtalk", map[string]string{
		"dingtalk_transport": "dws", "dingtalk_profile": "work",
	})
	if err != nil || state != IMStateNeedsAuth {
		t.Fatalf("needs auth = %q/%q/%v", state, detail, err)
	}
	if authURL != "https://login.test" {
		t.Fatalf("auth URL = %q", authURL)
	}
	for _, want := range []string{"dws", "auth login", "--profile work", "1.2.3"} {
		if !strings.Contains(detail, want) {
			t.Fatalf("needs-auth detail %q misses %q", detail, want)
		}
	}
	if cli.ran("auth login") {
		t.Fatal("the draft check ran an interactive login; it must only report it")
	}
	if cli.ran("chat") {
		t.Fatal("a missing-login check must not send a message")
	}
}

// Version + login command are exposed as setup facts (install detection and a
// copyable command).
func TestDWSVersionAndLoginCommand(t *testing.T) {
	cli := &setupCLI{version: "dws v9.9.9"}
	rt, m := newSetupManager(t, cli)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	m.Start(ctx, IMConfig{DingTransport: "dws", DWSCmd: "dws"})
	defer m.Stop()

	version, err := m.DWSVersion(ctx)
	if err != nil || !strings.Contains(version, "9.9.9") {
		t.Fatalf("DWSVersion = %q/%v", version, err)
	}
	cmd := m.DWSLoginCommand()
	if len(cmd) < 3 || cmd[0] != "dws" || cmd[1] != "auth" || cmd[2] != "login" {
		t.Fatalf("DWSLoginCommand = %v", cmd)
	}
	rtCmd := rt.DWSLoginCommand()
	if strings.Join(rtCmd, " ") != strings.Join(cmd, " ") {
		t.Fatalf("runtime wrapper = %v, manager = %v", rtCmd, cmd)
	}
}

func TestDWSVersionMissingBinary(t *testing.T) {
	_, m := newSetupManager(t, &setupCLI{missing: true})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := m.DWSVersion(ctx)
	if err == nil || !dingtalk.IsMissingBinary(err) {
		t.Fatalf("DWSVersion missing = %v (want missing-binary classification)", err)
	}
}

// Stream validation is presence-only (no network): missing robot is
// misconfigured; complete credentials are ready.
func TestCheckChannelDraftStream(t *testing.T) {
	_, m := newSetupManager(t, &setupCLI{})
	state, detail, _, err := m.CheckChannelDraft("dingtalk", map[string]string{
		"dingtalk_transport": "stream", "dingtalk_app_key": "k", "dingtalk_app_secret": "s",
	})
	if err != nil || state != IMStateMisconfigured || !strings.Contains(detail, "robot") {
		t.Fatalf("incomplete stream = %q/%q/%v", state, detail, err)
	}
	state, _, _, err = m.CheckChannelDraft("dingtalk", map[string]string{
		"dingtalk_transport": "stream", "dingtalk_app_key": "k",
		"dingtalk_app_secret": "s", "dingtalk_robot": "r",
	})
	if err != nil || state != IMStateReady {
		t.Fatalf("complete stream = %q/%v", state, err)
	}
}

// Connection states are published on the source row (facts) so the panel can
// render "misconfigured → what next" without parsing error strings.
func TestConnectionStatePublishedOnSourceRow(t *testing.T) {
	rt, _, _ := newIMTestRuntime(t)
	m := NewIMManager(rt, IMManagerOptions{Logger: testLogger(t), PollInterval: 10 * time.Millisecond,
		BuildFeishu: func(IMConfig) (im.Provider, error) { return &fakeIM{name: "feishu"}, nil }})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Half configuration: long polling without a chat list.
	m.Start(ctx, IMConfig{FeishuEnabled: true, FeishuExplicit: true, FeishuTransport: "longpoll",
		FeishuAppID: "a", FeishuAppSecret: "b"})
	defer m.Stop()

	conn, ok := m.Connection("feishu")
	if !ok || conn.State != IMStateMisconfigured || conn.NextAction == "" {
		t.Fatalf("connection = %+v/%v", conn, ok)
	}
	row := findSourceRow(t, rt, "feishu")
	facts := map[string]string{}
	for _, f := range row.Facts {
		facts[f.Label] = f.Value
	}
	if facts["状态"] != IMStateMisconfigured {
		t.Fatalf("source facts = %+v", row.Facts)
	}
	if facts["下一步"] == "" {
		t.Fatalf("source facts lack a next action: %+v", row.Facts)
	}
	if row.Enabled {
		t.Fatalf("a misconfigured channel claimed to be listening: %+v", row)
	}
}

// A draft check for Stream/Feishu does not need a running channel and never
// mutates saved state; a second draft check must not reuse the saved creds.
func TestCheckChannelDraftOverridesSavedValues(t *testing.T) {
	rt, m := newSetupManager(t, &setupCLI{})
	// Saved values are deliberately unusable; the draft supplies good ones.
	rt.mu.Lock()
	rt.settings["source.feishu_app_id"] = "saved_id"
	rt.mu.Unlock()
	old := httpDo
	defer func() { httpDo = old }()
	var seen string
	httpDo = func(req *http.Request) (*http.Response, error) {
		buf := make([]byte, 512)
		n, _ := req.Body.Read(buf)
		seen = string(buf[:n])
		return ioResponse(200, `{"code":0,"tenant_access_token":"t"}`), nil
	}
	values := map[string]string{
		"feishu_transport": "longpoll", "feishu_app_id": "draft_id",
		"feishu_app_secret": "draft_secret", "feishu_chats": "oc_1",
	}
	if state, detail, _, err := m.CheckChannelDraft("feishu", values); err != nil || state != IMStateReady {
		t.Fatalf("draft override = %q/%q/%v", state, detail, err)
	}
	if !strings.Contains(seen, "draft_id") {
		t.Fatalf("saved app id was used instead of the draft: %q", seen)
	}
	_ = json.Marshal // keep encoding/json if the fixture changes
	var _ = domain.Actor{}
}
