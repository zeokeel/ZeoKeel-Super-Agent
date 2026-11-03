// Package feishu 是飞书（Lark）的 IM 驱动：把消息翻译成 im.RawEvent，把回复
// 写回同一个会话。协议细节全部收在 Transport 后面，驱动只依赖 5 个调用。
//
// 为什么接收端不是 webhook：桌面应用在 NAT 后面，没有公网入口（design §4.8
// 写死的约束）。飞书的首选接入是"事件订阅长连接"（WebSocket）；本包在没有
// SDK / 凭据的情况下用 im/v1/messages 历史接口做"游标 + 增量"的长轮询兜底，
// 两者都满足同一个 Receiver 形状——以后接入长连接只需换 Transport，驱动与
// 领域层都不用动。
package feishu

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/zeokeel/super-agent/internal/im"
)

const (
	// DefaultBaseURL 是飞书开放平台域名；测试注入 httptest 的地址。
	DefaultBaseURL = "https://open.feishu.cn"
)

// Transport 是驱动依赖的飞书最小 HTTP 面。接口里全是"一页消息/一条发送"这种
// 粗粒度操作，而不是裸 HTTP：这样测试注入假实现时不用拼 HTTP，真实实现也只
// 需要关心协议；换长连接时新增一个实现即可，驱动不改。
type Transport interface {
	// TenantToken 取 tenant_access_token（机器人身份）。
	TenantToken(ctx context.Context) (Token, error)
	// BotInfo 取机器人自己的 open_id 与名字，用于 @ 判定（@ 的是不是自己）。
	BotInfo(ctx context.Context) (Bot, error)
	// SendMessage 发一条消息；ReplyTo 非空时走"回复某条消息"的端点。
	SendMessage(ctx context.Context, req SendRequest) (SendResult, error)
	// ListMembers 取一个会话的成员（白名单同步用），分页。
	ListMembers(ctx context.Context, chatID, pageToken string) (MembersPage, error)
	// ListMessages 按"起始毫秒 + 页游标"增量读取会话消息（长轮询兜底）。
	ListMessages(ctx context.Context, req ListRequest) (MessagesPage, error)
}

// Token 是访问令牌及其剩余有效期。
type Token struct {
	Value  string
	Expire time.Duration
}

// Bot 是机器人自己的身份。
type Bot struct {
	OpenID string
	Name   string
}

// SendRequest 是一条待发送消息，字段与飞书 im/v1/messages 对齐。
type SendRequest struct {
	ChatID  string
	ReplyTo string // 非空 → POST /open-apis/im/v1/messages/{id}/reply
	MsgType string // "text" 或 "interactive"
	Content string // 飞书要求 content 是 JSON 字符串
	UUID    string // 幂等键：飞书据此去重（重试不产生第二条）
}

// SendResult 是发送回执。
type SendResult struct {
	MessageID string
}

// MemberItem 是会话成员。
type MemberItem struct {
	ID   string
	Name string
}

// MembersPage 是一页成员。
type MembersPage struct {
	Items     []MemberItem
	PageToken string
	HasMore   bool
}

// ListRequest 是一页增量消息的请求。
type ListRequest struct {
	ChatID      string
	StartMillis int64 // 含起点：边界重复由内容派生 ID 幂等吸收，避免漏一条
	PageToken   string
	PageSize    int
}

// Mention 是消息里的一个 @：Key 是正文占位符（如 @_user_1），OpenID 是
// 被 @ 的人；@所有人 的 OpenID 为空。
type Mention struct {
	Key    string
	OpenID string
	Name   string
}

// Message 是飞书消息的驱动层形状（不是 RawEvent，也不绑定 HTTP JSON 嵌套）。
type Message struct {
	MessageID    string
	ChatID       string
	ChatType     string // p2p / group / topic；历史接口可能不给，由配置兜底
	MsgType      string
	Content      string // 原始 content JSON 字符串
	ParentID     string
	RootID       string
	ThreadID     string
	SenderID     string
	SenderType   string // user / app（app 即机器人）
	Mentions     []Mention
	CreateMillis int64
	Deleted      bool
}

// MessagesPage 是一页增量消息。
type MessagesPage struct {
	Messages  []Message
	PageToken string
	HasMore   bool
}

// APIError 是飞书业务错误（HTTP 200 但 code != 0）。它不可重试：参数/权限
// 问题重试三次只是把失败延后。gone 判定见 apiError。
type APIError struct {
	Code int
	Msg  string
}

// Error 实现 error。
func (e *APIError) Error() string { return fmt.Sprintf("feishu: api code %d: %s", e.Code, e.Msg) }

// goneCodes 是"这个会话/消息永远送不到"的业务码：机器人被移出群、会话不存在。
// 保留为显式集合而不是范围判断，新增码必须有人确认过语义（见 007 的 drop 语义）。
var goneCodes = map[int]bool{
	230002: true, // 机器人不在该会话 / 会话不可用
	230006: true, // 会话不存在或已解散
}

// errGone 把业务码转成 im.ErrGone，让驱动层统一转成 Receipt.Gone。
func apiError(code int, msg string) error {
	e := &APIError{Code: code, Msg: msg}
	if goneCodes[code] {
		return fmt.Errorf("%w: %s", im.ErrGone, e.Error())
	}
	return e
}

// HTTPTransport 是 Transport 的真实实现：令牌缓存与提前刷新、鉴权头、
// 429/5xx 退避重试、401 刷新 token 重试一次、错误码归一。协议无关的 HTTP
// 管线在 im.HTTPClient，令牌缓存在 im.TokenCache（两个 IM 驱动共用）。
//
// 重试契约（与 Outbox 的分工，design §7.2）：读类请求（GET）在传输层按 Retry
// 重试；**发送类请求不重试**（im.NoRetry）。重试必须由持有业务幂等键的
// Outbox 发起：传输层重试会绕过 driver 的 sent 去重（钉钉没有服务端幂等键，
// 见 N-15），也会让"失败一次"在面板上晚三轮才可见。飞书的 uuid 只保证服务端
// 去重，不改变"传输层只尝试一次、错误原样上报"这条契约。
type HTTPTransport struct {
	BaseURL   string
	Client    *http.Client
	AppID     string
	AppSecret string
	Retry     im.RetryPolicy
	// Now 便于测试注入时钟；空用 time.Now。
	Now func() time.Time

	tokens im.TokenCache
	http   im.HTTPClient
}

// NewHTTPTransport 构造真实传输。BaseURL 为空用 DefaultBaseURL。
func NewHTTPTransport(baseURL, appID, appSecret string, client *http.Client) *HTTPTransport {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	t := &HTTPTransport{BaseURL: strings.TrimRight(baseURL, "/"), Client: client, AppID: appID, AppSecret: appSecret, Now: time.Now}
	t.tokens.Now = func() time.Time { return t.now() }
	t.http = im.HTTPClient{
		BaseURL: t.BaseURL,
		HTTP:    client,
		Authorize: func(ctx context.Context, req *http.Request) error {
			tok, err := t.TenantToken(ctx)
			if err != nil {
				return err
			}
			req.Header.Set("Authorization", "Bearer "+tok.Value)
			return nil
		},
		OnUnauthorized: t.tokens.Invalidate,
	}
	return t
}

func (t *HTTPTransport) now() time.Time {
	if t.Now != nil {
		return t.Now()
	}
	return time.Now()
}

// TenantToken 取令牌。缓存到过期前 10%（最多提前 1 分钟）：飞书 token 2 小时，
// 提前刷新避免"刚好在边界过期"的偶发 401。并发调用共享同一次刷新。
func (t *HTTPTransport) TenantToken(ctx context.Context) (Token, error) {
	value, expire, err := t.tokens.Get(ctx, func(ctx context.Context) (string, time.Time, error) {
		raw, err := t.fetchTenantToken(ctx)
		return raw.value, raw.until, err
	})
	if err != nil {
		return Token{}, err
	}
	return Token{Value: value, Expire: expire}, nil
}

// rawToken 是缓存里的一条令牌：值 + 绝对过期时刻（提前刷新后的时刻）。
type rawToken struct {
	value string
	until time.Time
}

func (t *HTTPTransport) fetchTenantToken(ctx context.Context) (rawToken, error) {
	var resp tokenResponse
	body := map[string]string{"app_id": t.AppID, "app_secret": t.AppSecret}
	if err := t.postJSON(ctx, "/open-apis/auth/v3/tenant_access_token/internal", body, &resp, false); err != nil {
		return rawToken{}, err
	}
	if resp.Code != 0 {
		return rawToken{}, apiError(resp.Code, resp.Msg)
	}
	life := time.Duration(resp.Expire) * time.Second
	if life <= 0 {
		life = time.Hour
	}
	return rawToken{value: resp.TenantAccessToken, until: t.now().Add(life - im.TokenSkew(life))}, nil
}

// BotInfo 取机器人自身信息。
func (t *HTTPTransport) BotInfo(ctx context.Context) (Bot, error) {
	var resp botResponse
	if err := t.getJSON(ctx, "/open-apis/bot/v3/info", nil, &resp, true); err != nil {
		return Bot{}, err
	}
	if resp.Code != 0 {
		return Bot{}, apiError(resp.Code, resp.Msg)
	}
	return Bot{OpenID: resp.Bot.OpenID, Name: resp.Bot.AppName}, nil
}

// SendMessage 发一条消息：有 ReplyTo 走回复端点，否则走新建消息端点。
func (t *HTTPTransport) SendMessage(ctx context.Context, req SendRequest) (SendResult, error) {
	if req.ChatID == "" && req.ReplyTo == "" {
		return SendResult{}, errors.New("feishu: send needs a chat id or a reply target")
	}
	payload := map[string]any{"msg_type": req.MsgType, "content": req.Content}
	if req.UUID != "" {
		payload["uuid"] = req.UUID
	}
	path := "/open-apis/im/v1/messages"
	query := url.Values{"receive_id_type": {"chat_id"}}
	if req.ReplyTo != "" {
		path = "/open-apis/im/v1/messages/" + url.PathEscape(req.ReplyTo) + "/reply"
		query = nil
	} else {
		payload["receive_id"] = req.ChatID
	}
	var resp sendResponse
	if err := t.postJSONQuery(ctx, path, query, payload, &resp, true, im.NoRetry); err != nil {
		return SendResult{}, err
	}
	if resp.Code != 0 {
		return SendResult{}, apiError(resp.Code, resp.Msg)
	}
	return SendResult{MessageID: resp.Data.MessageID}, nil
}

// ListMembers 取一页会话成员。
func (t *HTTPTransport) ListMembers(ctx context.Context, chatID, pageToken string) (MembersPage, error) {
	if chatID == "" {
		return MembersPage{}, errors.New("feishu: members need a chat id")
	}
	query := url.Values{"member_id_type": {"open_id"}, "page_size": {"100"}}
	if pageToken != "" {
		query.Set("page_token", pageToken)
	}
	var resp membersResponse
	path := "/open-apis/im/v1/chats/" + url.PathEscape(chatID) + "/members"
	if err := t.getJSON(ctx, path, query, &resp, true); err != nil {
		return MembersPage{}, err
	}
	if resp.Code != 0 {
		return MembersPage{}, apiError(resp.Code, resp.Msg)
	}
	page := MembersPage{PageToken: resp.Data.PageToken, HasMore: resp.Data.HasMore}
	for _, item := range resp.Data.Items {
		if item.MemberID == "" {
			continue // 空 id 不能进白名单：它会匹配任何空 actor
		}
		page.Items = append(page.Items, MemberItem{ID: item.MemberID, Name: item.Name})
	}
	return page, nil
}

// ListMessages 读取一页增量消息（按创建时间升序）。
func (t *HTTPTransport) ListMessages(ctx context.Context, req ListRequest) (MessagesPage, error) {
	if req.ChatID == "" {
		return MessagesPage{}, errors.New("feishu: messages need a chat id")
	}
	size := req.PageSize
	if size <= 0 {
		size = 50
	}
	query := url.Values{
		"container_id_type": {"chat"},
		"container_id":      {req.ChatID},
		"sort_type":         {"ByCreateTimeAsc"},
		"page_size":         {strconv.Itoa(size)},
	}
	if req.StartMillis > 0 {
		query.Set("start_time", strconv.FormatInt(req.StartMillis/1000, 10))
	}
	if req.PageToken != "" {
		query.Set("page_token", req.PageToken)
	}
	var resp messagesResponse
	if err := t.getJSON(ctx, "/open-apis/im/v1/messages", query, &resp, true); err != nil {
		return MessagesPage{}, err
	}
	if resp.Code != 0 {
		return MessagesPage{}, apiError(resp.Code, resp.Msg)
	}
	page := MessagesPage{PageToken: resp.Data.PageToken, HasMore: resp.Data.HasMore}
	for _, w := range resp.Data.Items {
		page.Messages = append(page.Messages, w.toMessage())
	}
	return page, nil
}

// --- HTTP 细节 ---
//
// 协议无关部分（请求构造/重试/401/错误归一/响应体上限）在 im.HTTPClient；
// 这里只剩"用哪个 policy、解哪个信封"。

func (t *HTTPTransport) getJSON(ctx context.Context, path string, query url.Values, out any, auth bool) error {
	return t.http.DoJSON(ctx, http.MethodGet, path, query, nil, out, auth, t.Retry)
}

func (t *HTTPTransport) postJSON(ctx context.Context, path string, body, out any, auth bool) error {
	return t.postJSONQuery(ctx, path, nil, body, out, auth, t.Retry)
}

// postJSONQuery 发一个 POST。policy 决定是否在传输层重试：读类（token）用
// t.Retry；发送类用 im.NoRetry（见 HTTPTransport 注释里的重试契约）。
func (t *HTTPTransport) postJSONQuery(ctx context.Context, path string, query url.Values, body, out any, auth bool, policy im.RetryPolicy) error {
	return t.http.DoJSON(ctx, http.MethodPost, path, query, body, out, auth, policy)
}

// --- 飞书 JSON 信封 ---

type tokenResponse struct {
	Code              int    `json:"code"`
	Msg               string `json:"msg"`
	TenantAccessToken string `json:"tenant_access_token"`
	Expire            int    `json:"expire"`
}

type botResponse struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Bot  struct {
		OpenID  string `json:"open_id"`
		AppName string `json:"app_name"`
	} `json:"bot"`
}

type sendResponse struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data struct {
		MessageID string `json:"message_id"`
	} `json:"data"`
}

type membersResponse struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data struct {
		Items []struct {
			MemberID string `json:"member_id"`
			Name     string `json:"name"`
		} `json:"items"`
		PageToken string `json:"page_token"`
		HasMore   bool   `json:"has_more"`
	} `json:"data"`
}

type messagesResponse struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data struct {
		Items     []wireMessage `json:"items"`
		PageToken string        `json:"page_token"`
		HasMore   bool          `json:"has_more"`
	} `json:"data"`
}

// wireMessage 是飞书历史接口的嵌套形状；toMessage 把它拍平成驱动层形状，
// 让驱动与假传输都只面对一层字段（测试不必复刻嵌套）。
type wireMessage struct {
	MessageID  string `json:"message_id"`
	ChatID     string `json:"chat_id"`
	ChatType   string `json:"chat_type"`
	MsgType    string `json:"msg_type"`
	CreateTime string `json:"create_time"`
	ParentID   string `json:"parent_id"`
	RootID     string `json:"root_id"`
	ThreadID   string `json:"thread_id"`
	Deleted    bool   `json:"deleted"`
	Sender     struct {
		ID         string `json:"id"`
		SenderType string `json:"sender_type"`
	} `json:"sender"`
	Body struct {
		Content string `json:"content"`
	} `json:"body"`
	Mentions []struct {
		Key  string `json:"key"`
		Name string `json:"name"`
		ID   struct {
			OpenID string `json:"open_id"`
		} `json:"id"`
	} `json:"mentions"`
}

func (w wireMessage) toMessage() Message {
	m := Message{
		MessageID: w.MessageID, ChatID: w.ChatID, ChatType: w.ChatType, MsgType: w.MsgType,
		Content: w.Body.Content, ParentID: w.ParentID, RootID: w.RootID, ThreadID: w.ThreadID,
		SenderID: w.Sender.ID, SenderType: w.Sender.SenderType, Deleted: w.Deleted,
	}
	if millis, err := strconv.ParseInt(w.CreateTime, 10, 64); err == nil {
		m.CreateMillis = millis
	}
	for _, mention := range w.Mentions {
		m.Mentions = append(m.Mentions, Mention{Key: mention.Key, OpenID: mention.ID.OpenID, Name: mention.Name})
	}
	return m
}
