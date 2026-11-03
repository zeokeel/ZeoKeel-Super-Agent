package runtime

// DWS and Feishu adapters for the IM manager (im-channel-spec §2/§3, corrected
// to the real DWS CLI contract).
//
// DWS is the DingTalk Workspace CLI (`dws`): one long-lived consumer per channel
// (`dws event +listen-im --kind … -f ndjson`), a send path (`dws chat message
// send` / `send-by-bot`) and `dws auth status --format json`. nih-integrations
// owns that CLI adapter; this file is the single runtime seam to it and the
// Feishu credential probe.
//
// The manager treats DWS as a Receiver (long-lived stream, reconnect/dedupe owned
// by the adapter) and a Sender (serialized with the consumer by the adapter), so
// there is no invented poll/cursor contract in this package (P0-6). Feishu keeps
// HTTP long polling and its persisted cursor.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/zeokeel/super-agent/internal/domain"
	"github.com/zeokeel/super-agent/internal/im"
	"github.com/zeokeel/super-agent/internal/im/dingtalk"
	"github.com/zeokeel/super-agent/internal/im/feishu"
)

// dwsSecrets is what the transport must redact from stderr/health details.
func dwsSecrets(cfg IMConfig) []string {
	out := make([]string, 0, 2)
	for _, s := range []string{cfg.DingAppSecret} {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// dwsAuth is the auth fact the panel needs, independent of the CLI's wire JSON.
type dwsAuth struct {
	LoggedIn bool
	Account  string
	Profile  string
	AuthURL  string
	LoginCmd string
}

// dwsChannel is the DWS integration the manager runs: a Provider that is also a
// long-lived Receiver (NDJSON consumer) and a Sender (serialized with the
// consumer), plus auth/health facts and Close (kill the child process).
type dwsChannel interface {
	im.Provider
	im.Receiver
	im.Sender
	AuthStatus(ctx context.Context) (dwsAuth, error)
	SetStatus(fn func(im.HealthState, string))
	ioCloser
}

// ioCloser is io.Closer spelled locally so the interface stays readable.
type ioCloser interface {
	Close() error
}

// dwsChannelFactory builds the DWS integration for one channel generation.
type dwsChannelFactory func(IMConfig) (dwsChannel, error)

// dwsAdapter translates nih-integrations' *dingtalk.DWSChannel to the manager's
// narrow interface: the package type is the long-lived consumer + serialized
// sender + auth probe; only AuthStatus's return type needs translating.
type dwsAdapter struct{ ch *dingtalk.DWSChannel }

func (a *dwsAdapter) Name() string                                  { return a.ch.Name() }
func (a *dwsAdapter) Capabilities() im.Capabilities                 { return a.ch.Capabilities() }
func (a *dwsAdapter) Self(ctx context.Context) (im.Identity, error) { return a.ch.Self(ctx) }
func (a *dwsAdapter) Members(ctx context.Context, conv string) ([]im.Member, error) {
	return a.ch.Members(ctx, conv)
}
func (a *dwsAdapter) Subscribe(ctx context.Context, out chan<- im.RawEvent) error {
	return a.ch.Subscribe(ctx, out)
}
func (a *dwsAdapter) Send(ctx context.Context, msg im.Outbound) (im.Receipt, error) {
	return a.ch.Send(ctx, msg)
}
func (a *dwsAdapter) ReadThread(ctx context.Context, conv domain.Conversation, cursor string) (im.Page, error) {
	return a.ch.ReadThread(ctx, conv, cursor)
}
func (a *dwsAdapter) AuthStatus(ctx context.Context) (dwsAuth, error) {
	st, err := a.ch.AuthStatus(ctx)
	if err != nil {
		return dwsAuth{}, err
	}
	return dwsAuth{LoggedIn: st.LoggedIn, Account: st.Account, Profile: st.Profile,
		AuthURL: st.AuthURL, LoginCmd: st.LoginCommand}, nil
}
func (a *dwsAdapter) SetStatus(fn func(im.HealthState, string)) { a.ch.SetStatus(fn) }
func (a *dwsAdapter) Close() error                              { return a.ch.Close() }

// newDWSChannel is the one wiring point to nih-integrations' DWS adapter. It is a
// variable so the manager is testable; the default builds the real long-lived
// consumer over the CLI (injectable for fake CLI tests).
var newDWSChannel = func(cfg IMConfig, cli dingtalk.DWSCLI) (dwsChannel, error) {
	ch, err := dingtalk.NewDWSChannel(dingtalk.DWSConfig{
		Command:    cfg.DWSCmd,
		Profile:    cfg.Profile,
		ListenKind: effectiveListenKind(cfg),
		ChatQuery:  cfg.ChatQuery,
		Secrets:    dwsSecrets(cfg),
	}, cli)
	if err != nil {
		return nil, err
	}
	return &dwsAdapter{ch: ch}, nil
}

// dwsAuthOnly is a legacy, auth-only adapter over the injected old-style client: it
// answers CheckChannel (auth status) without claiming to listen. The real
// long-lived consumer replaces it; it exists so a fake CLI can be injected
// before that adapter lands.
type dwsAuthOnly struct {
	client dingtalk.DWSClient
	status func(im.HealthState, string)
}

func (*dwsAuthOnly) Name() string { return "dingtalk" }
func (*dwsAuthOnly) Capabilities() im.Capabilities {
	return im.Capabilities{Mentions: true, MaxTextLen: 4000}
}
func (*dwsAuthOnly) Self(context.Context) (im.Identity, error) {
	return im.Identity{}, fmt.Errorf("dws: identity comes from the CLI profile")
}
func (*dwsAuthOnly) Members(context.Context, string) ([]im.Member, error) {
	return nil, fmt.Errorf("%w: DWS has no member API", im.ErrUnsupported)
}
func (*dwsAuthOnly) Subscribe(context.Context, chan<- im.RawEvent) error {
	return fmt.Errorf("%w: DWS consumer integration is not wired yet", im.ErrUnsupported)
}
func (*dwsAuthOnly) ReadThread(context.Context, domain.Conversation, string) (im.Page, error) {
	return im.Page{}, fmt.Errorf("%w: DWS has no message history API", im.ErrUnsupported)
}
func (a *dwsAuthOnly) Send(ctx context.Context, msg im.Outbound) (im.Receipt, error) {
	id, err := a.client.Send(ctx, msg.Conv.ID, msg.Text, msg.Key)
	if err != nil {
		return im.Receipt{}, err
	}
	return im.Receipt{MessageID: id}, nil
}
func (a *dwsAuthOnly) AuthStatus(ctx context.Context) (dwsAuth, error) {
	st, err := a.client.AuthStatus(ctx)
	if err != nil {
		return dwsAuth{}, err
	}
	return dwsAuth{LoggedIn: st.LoggedIn, Account: st.Account, AuthURL: st.AuthURL}, nil
}
func (a *dwsAuthOnly) SetStatus(fn func(im.HealthState, string)) { a.status = fn }
func (*dwsAuthOnly) Close() error                                { return nil }

// dwsAuthState maps one auth-status answer to the panel's vocabulary. An auth
// error is a state ("需要授权"), not a failed check: the button can then offer
// the URL/command instead of a red error without an action.
func dwsAuthState(auth dwsAuth, err error, cmd string) (state, detail, authURL string, outErr error) {
	if cmd = strings.TrimSpace(cmd); cmd == "" {
		cmd = "dws"
	}
	login := orDefault(strings.TrimSpace(auth.LoginCmd), cmd+" auth login")
	if err != nil {
		if state, detail, authURL, ok := dwsAuthError(err); ok {
			return state, detail, authURL, nil
		}
		return IMError, "无法执行 " + cmd + "：" + redactIMDetail(err.Error()) + "（安装 dws 或设置 dingtalk_dws_cmd）", "", err
	}
	if auth.LoggedIn {
		who := strings.TrimSpace(auth.Account)
		if p := strings.TrimSpace(auth.Profile); p != "" {
			who = pickFirst(who+" ("+p+")", p, who)
		}
		return IMReady, orDefault("已登录："+who, "已登录"), "", nil
	}
	detail = "未登录：运行 `" + login + "` 完成授权"
	if auth.AuthURL != "" {
		detail += "，或打开授权链接"
	}
	return IMAuth, detail, auth.AuthURL, nil
}

// dwsAuthError recognizes the auth-shaped failures of the CLI adapter.
func dwsAuthError(err error) (state, detail, authURL string, ok bool) {
	if errors.Is(err, dingtalk.ErrDWSAuthRequired) || errors.Is(err, dingtalk.ErrAuthRequired) {
		return IMAuth, "dws 未登录：运行 `dws auth login` 完成授权", "", true
	}
	var de *dingtalk.DWSError
	if errors.As(err, &de) && (de.Status == 401 || de.Status == 403 || de.AuthURL != "") {
		detail = "dws 未登录：运行 `dws auth login` 完成授权"
		if de.AuthURL != "" {
			detail += "，或打开授权链接"
		}
		return IMAuth, detail, de.AuthURL, true
	}
	return "", "", "", false
}

// newFeishuWS builds the official larkws long-connection driver: a Receiver
// (Subscribe) with no cursor, closed on channel stop (P1-10). Long polling stays
// the explicit fallback and keeps its persisted cursor.
var newFeishuWS = func(cfg IMConfig) (im.Provider, ioCloser, error) {
	drv, err := feishu.New(feishu.Config{
		Transport: feishu.TransportWS,
		AppID:     cfg.FeishuAppID, AppSecret: cfg.FeishuAppSecret,
		Chats:  splitFeishuChats(cfg.FeishuChats),
		SelfID: selfIDFor(cfg, "feishu"), SelfName: selfIDFor(cfg, "feishu"),
	})
	if err != nil {
		return nil, nil, err
	}
	return drv, drv, nil
}

// feishuWSAvailable is true when the WS transport above is part of this build;
// channelReadiness and CheckChannel consult it so the panel accepts every
// transport the runtime can actually run.
var feishuWSAvailable = feishu.SupportsTransport(feishu.TransportWS)

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

// checkFeishuAuth probes the tenant token endpoint (im-channel-spec §3).
func checkFeishuAuth(cfg IMConfig, baseURL string) (state, detail, authURL string, err error) {
	if cfg.FeishuAppID == "" || cfg.FeishuAppSecret == "" {
		return IMAuth, "飞书凭据不完整：填写 app id 与 app secret", "https://open.feishu.cn/app", nil
	}
	base := strings.TrimRight(baseURL, "/")
	if base == "" {
		base = "https://open.feishu.cn"
	}
	body, _ := json.Marshal(map[string]string{"app_id": cfg.FeishuAppID, "app_secret": cfg.FeishuAppSecret})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		base+"/open-apis/auth/v3/tenant_access_token/internal", strings.NewReader(string(body)))
	if err != nil {
		return IMError, err.Error(), "", err
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	resp, err := httpDo(req)
	if err != nil {
		return IMError, "飞书 token 检查失败：" + redactIMDetail(err.Error()), "", err
	}
	defer resp.Body.Close()
	var out struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	switch {
	case resp.StatusCode == 200 && out.Code == 0:
		return IMReady, "飞书凭据有效", "", nil
	case resp.StatusCode == 401 || resp.StatusCode == 403 || out.Code == 10003 || out.Code == 10014:
		return IMAuth, fmt.Sprintf("飞书授权失败（%d/%s）：请在开放平台检查 app 权限", resp.StatusCode, out.Msg),
			"https://open.feishu.cn/app", nil
	default:
		return IMError, fmt.Sprintf("飞书检查失败（HTTP %d, code %d: %s)", resp.StatusCode, out.Code, out.Msg),
			"", fmt.Errorf("feishu: HTTP %d code %d", resp.StatusCode, out.Code)
	}
}

// httpDo is the token-check client; a variable so tests never touch the network.
var httpDo = func(req *http.Request) (*http.Response, error) {
	return (&http.Client{Timeout: 10 * time.Second}).Do(req)
}

// splitFeishuChats parses the comma-separated chat list; empty ids are dropped
// (a blank id would make the driver reject the whole config).
func splitFeishuChats(raw string) []feishu.Chat {
	var out []feishu.Chat
	for _, part := range strings.Split(raw, ",") {
		if id := strings.TrimSpace(part); id != "" {
			out = append(out, feishu.Chat{ID: id})
		}
	}
	return out
}
