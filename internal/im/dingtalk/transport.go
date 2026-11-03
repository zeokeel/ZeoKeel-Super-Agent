// Package dingtalk 是钉钉（企业内部机器人）的 IM 驱动。
//
// 接收端的现实：钉钉没有"拉取机器人消息"的 HTTP 历史接口，官方方案是
// Stream 模式——机器人主动连出去的 WebSocket 长连接，客户端在 NAT 后面也能
// 用（design §4.8）。StreamTransport（stream.go）用官方 SDK 实现接收：SDK 的
// 机器人回调 → 有界队列 → Transport.StreamEvents → driver.Poll → RawEvent。
// HTTPTransport 也能作为 Transport 注入（测试、或无 Stream 的 NoStream 部署），
// 此时它诚实报告"没有接收端"而不是伪造空批。
//
// 发送与成员列表是普通 HTTP，按钉钉 v1.0 OpenAPI 形状实现并用 httptest 验证
// 请求形状；鉴权头是 x-acs-dingtalk-access-token。
package dingtalk

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/zeokeel/super-agent/internal/im"
)

const (
	// DefaultBaseURL 是钉钉新版 OpenAPI 域名（Stream 的长连接地址另由
	// Stream 传输实现决定，不在 HTTP 传输里）。
	DefaultBaseURL = "https://api.dingtalk.com"
)

// StreamSupport 是可选能力：传输层明确报告自己能否提供接收端（Stream 帧）。
// 可选接口而不是必选方法，是为了让未来的真实 WebSocket 传输不必实现一个
// 永远返回 true 的方法；没有实现时按"能"处理（驱动会进入 Subscribe 循环）。
type StreamSupport interface {
	SupportsStream() bool
}

// Transport 是驱动依赖的钉钉最小面。
type Transport interface {
	// AccessToken 取企业内机器人 access_token。
	AccessToken(ctx context.Context) (Token, error)
	// SendRobot 发一条机器人消息（单聊或群聊）。
	SendRobot(ctx context.Context, req SendRequest) (SendResult, error)
	// ListMembers 取一个会话的成员，用于白名单同步。
	ListMembers(ctx context.Context, convID, pageToken string) (MembersPage, error)
	// StreamEvents 取一批 Stream 事件（长连接的一帧/一批帧），cursor 由传输
	// 定义；真实实现是 WebSocket，测试用假实现。
	StreamEvents(ctx context.Context, cursor string) (EventsPage, error)
}

// Token 是 access_token 及其剩余有效期。
type Token struct {
	Value  string
	Expire time.Duration
}

// SendRequest 是一条机器人消息。MsgKey/MsgParam 与钉钉机器人模板对齐：
// sampleText 收 {"content":...}，sampleMarkdown 收 {"title":...,"text":...}。
type SendRequest struct {
	RobotCode      string
	ConversationID string // 群聊：openConversationId
	Direct         bool   // true：单聊，用 UserID 而不是会话 id
	UserID         string // 单聊对方 userId（去掉 "dm:" 前缀后）
	MsgKey         string
	MsgParam       string
	ReplyTo        string // 钉钉机器人不能引用回复，保留字段供未来 Stream 回复使用
}

// SendResult 是发送回执。
type SendResult struct {
	MessageID string // 群聊/单聊接口不一定返回；空表示"已接受"
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

// AtUser 是钉钉回调里的一个 @ 对象。
type AtUser struct {
	DingTalkID string
	StaffID    string
	Name       string
}

// Event 是钉钉 Stream 回调里的机器人消息（驱动层形状）。
type Event struct {
	MsgID             string
	MsgType           string
	Content           string // 原始 content JSON
	SenderStaffID     string
	SenderID          string
	SenderNick        string
	ConversationID    string
	ConversationType  string // "1" 单聊 / "2" 群聊
	ConversationTitle string
	ChatbotUserID     string
	RobotCode         string
	CreateAt          int64
	IsInAtList        bool
	IsReplyMsg        bool
	// SenderIsBot 是传输层给出的权威 bot 标记（DWS 的 sender.bot）；
	// Stream 回调没有这个字段，保留 false 让 mapping 用 staffId 启发式。
	SenderIsBot bool
	AtUsers     []AtUser
}

// EventsPage 是一批 Stream 事件。
type EventsPage struct {
	Events []Event
	Cursor string
}

// APIError 是钉钉业务错误。不可重试：参数/权限问题重试只会延后失败。
type APIError struct {
	Code    string
	Message string
}

// Error 实现 error。
func (e *APIError) Error() string { return fmt.Sprintf("dingtalk: api code %s: %s", e.Code, e.Message) }

// goneCodes 是"会话/机器人永久不可达"的错误码：机器人被移出群、会话已解散。
// 显式集合而不是前缀匹配：新增码必须确认语义后再加，否则会把可重试的故障
// 误判成永久丢弃（007 的 drop 语义）。
var goneCodes = map[string]bool{
	"invalidOpenConversationId":  true,
	"openConversationIdNotFound": true,
	"robotNotInGroup":            true,
	"botNotInGroup":              true,
}

func apiError(code, message string) error {
	e := &APIError{Code: code, Message: message}
	if goneCodes[code] {
		return fmt.Errorf("%w: %s", im.ErrGone, e.Error())
	}
	return e
}

// HTTPTransport 是钉钉的 HTTP 传输：token 缓存、鉴权头、429/5xx 退避重试。
// 协议无关的 HTTP 管线在 im.HTTPClient，令牌缓存在 im.TokenCache（与飞书共用）。
//
// 重试契约与飞书一致（design §7.2 / N-15）：读类请求按 Retry 重试；**发送类
// 请求只尝试一次**，重试交给持有业务幂等键的 Outbox。钉钉发送接口没有服务端
// 幂等键，传输层重试会直接绕过 driver 的 sent 去重并产生重复消息，所以这里
// 必须把"尝试一次 + 报错"作为契约的一部分。
type HTTPTransport struct {
	BaseURL   string
	Client    *http.Client
	AppKey    string
	AppSecret string
	Retry     im.RetryPolicy
	// Now 便于测试注入时钟；空用 time.Now。
	Now func() time.Time

	tokens im.TokenCache
	http   im.HTTPClient
}

// NewHTTPTransport 构造真实 HTTP 传输；BaseURL 为空用 DefaultBaseURL。
func NewHTTPTransport(baseURL, appKey, appSecret string, client *http.Client) *HTTPTransport {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	t := &HTTPTransport{BaseURL: strings.TrimRight(baseURL, "/"), Client: client, AppKey: appKey, AppSecret: appSecret, Now: time.Now}
	t.tokens.Now = func() time.Time { return t.now() }
	t.http = im.HTTPClient{
		BaseURL: t.BaseURL,
		HTTP:    client,
		Authorize: func(ctx context.Context, req *http.Request) error {
			tok, err := t.AccessToken(ctx)
			if err != nil {
				return err
			}
			req.Header.Set("x-acs-dingtalk-access-token", tok.Value)
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

// AccessToken 取并缓存 token（提前 10% 刷新）。并发调用共享同一次刷新。
func (t *HTTPTransport) AccessToken(ctx context.Context) (Token, error) {
	value, expire, err := t.tokens.Get(ctx, func(ctx context.Context) (string, time.Time, error) {
		raw, err := t.fetchAccessToken(ctx)
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

func (t *HTTPTransport) fetchAccessToken(ctx context.Context) (rawToken, error) {
	var resp tokenResponse
	body := map[string]string{"appKey": t.AppKey, "appSecret": t.AppSecret}
	if err := t.postJSON(ctx, "/v1.0/oauth2/accessToken", body, &resp, false); err != nil {
		return rawToken{}, err
	}
	if resp.AccessToken == "" {
		return rawToken{}, apiError(resp.Code, resp.Message)
	}
	life := time.Duration(resp.ExpireIn) * time.Second
	if life <= 0 {
		life = time.Hour
	}
	return rawToken{value: resp.AccessToken, until: t.now().Add(life - im.TokenSkew(life))}, nil
}

// SendRobot 发机器人消息：群聊用 openConversationId，单聊用 userIds。
func (t *HTTPTransport) SendRobot(ctx context.Context, req SendRequest) (SendResult, error) {
	if req.RobotCode == "" {
		return SendResult{}, errors.New("dingtalk: send needs a robot code")
	}
	var (
		path    string
		payload map[string]any
	)
	if req.Direct {
		if req.UserID == "" {
			return SendResult{}, errors.New("dingtalk: direct send needs a user id")
		}
		path = "/v1.0/robot/oToMessages/batchSend"
		payload = map[string]any{
			"robotCode": req.RobotCode,
			"userIds":   []string{req.UserID},
			"msgKey":    req.MsgKey,
			"msgParam":  req.MsgParam,
		}
	} else {
		if req.ConversationID == "" {
			return SendResult{}, errors.New("dingtalk: group send needs a conversation id")
		}
		path = "/v1.0/robot/groupMessages/send"
		payload = map[string]any{
			"robotCode":          req.RobotCode,
			"openConversationId": req.ConversationID,
			"msgKey":             req.MsgKey,
			"msgParam":           req.MsgParam,
		}
	}
	var resp sendResponse
	if err := t.postJSONOnce(ctx, path, payload, &resp, true); err != nil {
		return SendResult{}, err
	}
	if resp.Code != "" && !strings.EqualFold(resp.Code, "0") {
		return SendResult{}, apiError(resp.Code, resp.Message)
	}
	return SendResult{MessageID: resp.ProcessQueryKey}, nil
}

// ListMembers 取一页群成员。
//
// 尚未在真实租户验证：本路径（/v1.0/im/chatMembers）按官方"群成员查询"整理，
// 在真实租户验证；transport 隔离了它，接入时以官方文档校正路径/字段即可，
// 驱动与上层不受影响。
func (t *HTTPTransport) ListMembers(ctx context.Context, convID, pageToken string) (MembersPage, error) {
	if convID == "" {
		return MembersPage{}, errors.New("dingtalk: members need a conversation id")
	}
	query := url.Values{"openConversationId": {convID}, "maxResults": {"100"}}
	if pageToken != "" {
		query.Set("nextToken", pageToken)
	}
	var resp membersResponse
	if err := t.getJSON(ctx, "/v1.0/im/chatMembers", query, &resp, true); err != nil {
		if isNotFound(err) {
			// 没有成员接口/权限：交给上层用"手工白名单"降级，而不是假装返回空名单
			//（空名单会把所有人的许可都关掉，是更坏的默认）。
			return MembersPage{}, fmt.Errorf("%w: %v", im.ErrUnsupported, err)
		}
		return MembersPage{}, err
	}
	page := MembersPage{PageToken: resp.NextToken, HasMore: resp.HasMore}
	for _, item := range resp.Members {
		if item.UserID == "" {
			continue
		}
		page.Items = append(page.Items, MemberItem{ID: item.UserID, Name: item.Name})
	}
	return page, nil
}

// StreamEvents 在 HTTP 传输上不可用：钉钉 Stream 是 WebSocket 长连接，
// 不是 HTTP 拉取。返回 ErrUnsupported 而不是伪造空批，避免驱动空转、
// 也让接线方明确知道"要换一个 Stream 传输实现"。
func (t *HTTPTransport) StreamEvents(context.Context, string) (EventsPage, error) {
	return EventsPage{}, fmt.Errorf("%w: dingtalk Stream mode needs a WebSocket transport", im.ErrUnsupported)
}

// SupportsStream 实现 StreamSupport：HTTP 传输没有接收端。
func (*HTTPTransport) SupportsStream() bool { return false }

// --- HTTP 细节 ---
//
// 协议无关部分（请求构造/重试/401/错误归一/响应体上限）在 im.HTTPClient；
// 这里只剩"用哪个 policy、解哪个信封"。

func (t *HTTPTransport) getJSON(ctx context.Context, path string, query url.Values, out any, auth bool) error {
	return t.http.DoJSON(ctx, http.MethodGet, path, query, nil, out, auth, t.Retry)
}

func (t *HTTPTransport) postJSON(ctx context.Context, path string, body, out any, auth bool) error {
	return t.http.DoJSON(ctx, http.MethodPost, path, nil, body, out, auth, t.Retry)
}

// postJSONOnce 发一个不重试的 POST：发送类请求的契约见 HTTPTransport 注释。
func (t *HTTPTransport) postJSONOnce(ctx context.Context, path string, body, out any, auth bool) error {
	return t.http.DoJSON(ctx, http.MethodPost, path, nil, body, out, auth, im.NoRetry)
}

func isNotFound(err error) bool {
	var he *im.HTTPError
	return errors.As(err, &he) && (he.Status == http.StatusNotFound || he.Status == http.StatusNotImplemented)
}

// --- 钉钉 JSON 信封 ---

type tokenResponse struct {
	Code        string `json:"code"`
	Message     string `json:"message"`
	AccessToken string `json:"accessToken"`
	ExpireIn    int    `json:"expireIn"`
}

type sendResponse struct {
	Code            string `json:"code"`
	Message         string `json:"message"`
	ProcessQueryKey string `json:"processQueryKey"`
}

type membersResponse struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	NextToken string `json:"nextToken"`
	HasMore   bool   `json:"hasMore"`
	Members   []struct {
		UserID string `json:"userId"`
		Name   string `json:"name"`
	} `json:"members"`
}
