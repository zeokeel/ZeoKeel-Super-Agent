package runtime

// DWS: DingTalk Workspace gateway via the external `dws` CLI (im-channel-spec §2).
// The client, JSON parsing, serial runner and error classification live in
// internal/im/dingtalk (nih-integrations); this file only adapts that client to
// the manager's Poller/Sender shape.
//
// Commands definitely do not go through a shell: the client uses exec.Command
// directly, one subprocess at a time, with a 30s timeout.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/zeokeel/super-agent/internal/domain"
	"github.com/zeokeel/super-agent/internal/im"
	"github.com/zeokeel/super-agent/internal/im/dingtalk"
	"github.com/zeokeel/super-agent/internal/im/feishu"
)

// dwsPoller adapts the DWS client to im.Provider/Poller/Sender.
type dwsPoller struct {
	client dingtalk.DWSClient
	cfg    IMConfig
}

func newDWSPoller(cfg IMConfig, client dingtalk.DWSClient) *dwsPoller {
	return &dwsPoller{client: client, cfg: cfg}
}

func (d *dwsPoller) Name() string { return "dingtalk" }

func (d *dwsPoller) Capabilities() im.Capabilities {
	return im.Capabilities{Markdown: false, RichCard: false, Files: false,
		Threads: false, Mentions: true, MaxTextLen: 4000}
}

func (d *dwsPoller) Self(context.Context) (im.Identity, error) {
	id := d.cfg.DingBotID
	if id == "" {
		id = d.cfg.SelfID
	}
	if id == "" {
		return im.Identity{}, fmt.Errorf("dingtalk: bot id 未配置（dingtalk_bot_id）")
	}
	return im.Identity{ID: id, Name: "ZeoKeel"}, nil
}

func (d *dwsPoller) Members(context.Context, string) ([]im.Member, error) {
	return nil, fmt.Errorf("%w: DWS 没有成员列表命令", im.ErrUnsupported)
}

// Poll maps one DWS page to the common RawEvent shape.
func (d *dwsPoller) Poll(ctx context.Context, cursor string) ([]im.RawEvent, string, error) {
	page, err := d.client.Poll(ctx, cursor, 50)
	if err != nil {
		return nil, cursor, err
	}
	out := make([]im.RawEvent, 0, len(page.Messages))
	for _, m := range page.Messages {
		conv := domain.Conversation{ID: m.ConversationID, Name: m.ConversationName}
		if strings.EqualFold(m.ConversationType, "group") {
			conv.Kind = domain.ConvGroup
		} else {
			conv.Kind = domain.ConvDirect
		}
		out = append(out, im.RawEvent{
			ID: m.ID, Conv: conv,
			Actor:      domain.Actor{ID: m.SenderID, Bot: m.SenderBot},
			Text:       m.Text,
			At:         m.At.UnixMilli(),
			Mentions:   m.Mentions,
			ReplyToBot: m.ReplyToBot,
		})
	}
	next := page.Cursor
	if next == "" {
		next = cursor
	}
	return out, next, nil
}

// Send writes one message through `dws send` (idempotency key included).
func (d *dwsPoller) Send(ctx context.Context, msg im.Outbound) (im.Receipt, error) {
	id, err := d.client.Send(ctx, msg.Conv.ID, msg.Text, msg.Key)
	if err != nil {
		return im.Receipt{}, err
	}
	return im.Receipt{MessageID: id}, nil
}

func (d *dwsPoller) ReadThread(context.Context, domain.Conversation, string) (im.Page, error) {
	return im.Page{Done: true}, fmt.Errorf("%w: DWS 没有消息历史命令", im.ErrUnsupported)
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
		return IMError, "飞书 token 检查失败：" + err.Error(), "", err
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
	client := &http.Client{Timeout: 10 * time.Second}
	return client.Do(req)
}

// splitFeishuChats turns "oc_1,oc_2" into the driver's chat list.
func splitFeishuChats(raw string) []feishu.Chat {
	var out []feishu.Chat
	for _, id := range strings.Split(raw, ",") {
		if id = strings.TrimSpace(id); id != "" {
			out = append(out, feishu.Chat{ID: id})
		}
	}
	return out
}
