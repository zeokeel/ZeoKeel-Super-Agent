package im

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// HTTPClient 是两个 IM 传输共用的 HTTP 管线：请求构造、鉴权头、响应体上限、
// 状态码归一（HTTPError）、重试与 401 刷新。
//
// 飞书与钉钉的差异只在于 base URL、鉴权头与 token 失效动作，所以把协议无关
// 的这部分收在一处（N-1/N-15 的修复顺带消掉两包之间约 200 行复制粘贴）。
//
// 分工（design §7.2）：读类请求按调用方传入的 RetryPolicy 重试；发送类请求
// 传 NoRetry——重试必须由持有业务幂等键的 Outbox 发起，否则会绕过 driver 的
// 发送去重。401 的"刷新 token 再试一次"独立于 RetryPolicy：token 过期是确定
// 可恢复的鉴权失败，不是"服务端可能已经接受"的含混失败。
type HTTPClient struct {
	BaseURL string
	HTTP    *http.Client
	// Authorize 为一个鉴权请求设置头（如 Authorization / x-acs-...-token）。
	Authorize func(ctx context.Context, req *http.Request) error
	// OnUnauthorized 在鉴权请求收到 401 后调用（清 token 缓存）。可选。
	OnUnauthorized func()
	// MaxBodyBytes 是单次响应体上限；<=0 时用 DefaultMaxBodyBytes。
	MaxBodyBytes int64
}

// DefaultMaxBodyBytes 是响应体上限：单条消息/成员页远小于 1 MiB，限制可以
// 防一条坏响应把内存撑爆。
const DefaultMaxBodyBytes = 1 << 20

// NoRetry 是发送类请求的策略：只尝试一次，重试交给 Outbox（见包注释）。
//
// 失败时 Call 返回 *HTTPError：Status 让上层区分"要人处理"（4xx）与
// "值得重试"（429/5xx），RetryAfter（可被 im.RetryAfter 解析）让 Outbox/effects
// 用服务端建议的退避而不是自己猜。这条链是"传输层不重试"契约的配套：
// attempt=1 不会让失败被藏起来，反而让重试次数与退避策略只有一个所有者。
var NoRetry = RetryPolicy{Attempts: 1}

// DoJSON 执行一次请求并解码 JSON 响应。
func (c *HTTPClient) DoJSON(ctx context.Context, method, path string, query url.Values, body, out any, auth bool, policy RetryPolicy) error {
	raw, err := c.Call(ctx, method, path, query, body, auth, policy)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}

// Call 执行一次请求：先按 policy 重试 429/5xx/网络错误；鉴权请求收到 401 时
// 清 token 并再试一轮（每轮独立重试）。
func (c *HTTPClient) Call(ctx context.Context, method, path string, query url.Values, body any, auth bool, policy RetryPolicy) ([]byte, error) {
	refreshed := false
	for {
		raw, err := Do(ctx, policy, func(ctx context.Context) ([]byte, error) {
			return c.attempt(ctx, method, path, query, body, auth)
		})
		if err == nil {
			return raw, nil
		}
		var he *HTTPError
		if auth && !refreshed && errors.As(err, &he) && he.Status == http.StatusUnauthorized {
			refreshed = true
			if c.OnUnauthorized != nil {
				c.OnUnauthorized()
			}
			continue
		}
		return nil, err
	}
}

func (c *HTTPClient) attempt(ctx context.Context, method, path string, query url.Values, body any, auth bool) ([]byte, error) {
	endpoint := c.BaseURL + path
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json; charset=utf-8")
	}
	if auth && c.Authorize != nil {
		if err := c.Authorize(ctx, req); err != nil {
			return nil, err
		}
	}
	httpClient := c.HTTP
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		// 网络错误包成 HTTPError(Status 0)：Temporary()==true，值得重试。
		return nil, &HTTPError{Err: err}
	}
	defer func() { _ = resp.Body.Close() }()
	limit := c.MaxBodyBytes
	if limit <= 0 {
		limit = DefaultMaxBodyBytes
	}
	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, limit))
	if readErr != nil {
		return nil, &HTTPError{Status: resp.StatusCode, Err: readErr}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &HTTPError{Status: resp.StatusCode, RetryAfter: RetryAfter(resp.Header.Get("Retry-After")), Body: Snippet(raw)}
	}
	return raw, nil
}

// RetryAfter 解析 Retry-After：优先整数秒（RFC 9110 的常见形式），也接受
// HTTP-date；都解析不了返回 0，让退避策略兜底。
func RetryAfter(value string) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(value); err == nil {
		if seconds <= 0 {
			return 0
		}
		return time.Duration(seconds) * time.Second
	}
	if when, err := http.ParseTime(value); err == nil {
		if d := time.Until(when); d > 0 {
			return d
		}
	}
	return 0
}

// Snippet 截一段响应体用于错误信息：太长会淹没日志，太短又定位不到问题。
func Snippet(raw []byte) string {
	const max = 300
	s := strings.TrimSpace(string(raw))
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}
