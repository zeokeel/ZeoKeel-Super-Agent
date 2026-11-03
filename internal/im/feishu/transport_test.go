package feishu

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zeokeel/super-agent/internal/im"
)

// testServer 起一个本地 httptest 服务：测试只走 loopback，绝不联网。
func testServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv
}

// 传输层要真实到"请求形状正确"：token 端点、鉴权头、uuid、回复端点、
// 分页参数，全部断言。凭据是假的，但对协议形状的要求是真的。
func TestHTTPTokenSendAndReplyShapes(t *testing.T) {
	var (
		mu         sync.Mutex
		tokenCalls int
		sends      []map[string]any
		authHeads  []string
		replies    []string
	)
	srv := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/open-apis/auth/v3/tenant_access_token/internal":
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("token body: %v", err)
			}
			if body["app_id"] != "cli_test" || body["app_secret"] != "sec" {
				t.Errorf("token body = %v", body)
			}
			mu.Lock()
			tokenCalls++
			mu.Unlock()
			_, _ = w.Write([]byte(`{"code":0,"msg":"ok","tenant_access_token":"t-1","expire":7200}`))
		case r.URL.Path == "/open-apis/bot/v3/info":
			if got := r.Header.Get("Authorization"); got != "Bearer t-1" {
				t.Errorf("bot info auth = %q", got)
			}
			_, _ = w.Write([]byte(`{"code":0,"bot":{"open_id":"ou_bot","app_name":"ZeoKeel"}}`))
		case r.URL.Path == "/open-apis/im/v1/messages" && r.Method == http.MethodPost:
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			mu.Lock()
			sends = append(sends, body)
			authHeads = append(authHeads, r.Header.Get("Authorization"))
			mu.Unlock()
			if r.URL.Query().Get("receive_id_type") != "chat_id" {
				t.Errorf("receive_id_type = %q", r.URL.Query().Get("receive_id_type"))
			}
			_, _ = w.Write([]byte(`{"code":0,"data":{"message_id":"om_new"}}`))
		case r.Method == http.MethodPost && len(r.URL.Path) > len("/open-apis/im/v1/messages/") &&
			r.URL.Path[:len("/open-apis/im/v1/messages/")] == "/open-apis/im/v1/messages/":
			mu.Lock()
			replies = append(replies, r.URL.Path)
			mu.Unlock()
			_, _ = w.Write([]byte(`{"code":0,"data":{"message_id":"om_reply"}}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})
	tr := NewHTTPTransport(srv.URL, "cli_test", "sec", srv.Client())

	tok, err := tr.TenantToken(context.Background())
	if err != nil || tok.Value != "t-1" {
		t.Fatalf("token = %+v, %v", tok, err)
	}
	// 第二次必须命中缓存，不能再打一次 token 端点。
	if _, err := tr.TenantToken(context.Background()); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	if tokenCalls != 1 {
		t.Fatalf("token endpoint called %d times, want 1 (cached)", tokenCalls)
	}
	mu.Unlock()

	bot, err := tr.BotInfo(context.Background())
	if err != nil || bot.OpenID != "ou_bot" {
		t.Fatalf("bot = %+v, %v", bot, err)
	}

	result, err := tr.SendMessage(context.Background(), SendRequest{
		ChatID: "oc_1", MsgType: "text", Content: `{"text":"hi"}`, UUID: "k-1",
	})
	if err != nil || result.MessageID != "om_new" {
		t.Fatalf("send = %+v, %v", result, err)
	}
	mu.Lock()
	if len(sends) != 1 {
		t.Fatalf("sends = %d", len(sends))
	}
	body := sends[0]
	if body["receive_id"] != "oc_1" || body["msg_type"] != "text" || body["uuid"] != "k-1" {
		t.Fatalf("send body = %v", body)
	}
	if authHeads[0] != "Bearer t-1" {
		t.Fatalf("send auth = %q", authHeads[0])
	}
	mu.Unlock()

	if _, err := tr.SendMessage(context.Background(), SendRequest{ReplyTo: "om_parent", MsgType: "text", Content: `{"text":"re"}`, UUID: "k-2"}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	if len(replies) != 1 || replies[0] != "/open-apis/im/v1/messages/om_parent/reply" {
		t.Fatalf("reply paths = %v", replies)
	}
	mu.Unlock()
}

// 读类请求（GET）遇到 429 值得重试：传输层按策略退避后再打一次。
func TestHTTPRetriesReadsOn429(t *testing.T) {
	var attempts int32
	srv := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/open-apis/im/v1/messages" {
			_, _ = w.Write([]byte(`{"code":0,"tenant_access_token":"t","expire":7200}`))
			return
		}
		if atomic.AddInt32(&attempts, 1) == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write([]byte(`{"code":0,"data":{"items":[],"has_more":false}}`))
	})
	tr := NewHTTPTransport(srv.URL, "a", "b", srv.Client())
	tr.Retry = im.RetryPolicy{Attempts: 3, Base: time.Millisecond, Max: time.Millisecond}
	if _, err := tr.ListMessages(context.Background(), ListRequest{ChatID: "oc"}); err != nil {
		t.Fatal(err)
	}
	if got := atomic.LoadInt32(&attempts); got != 2 {
		t.Fatalf("attempts = %d, want 2", got)
	}
}

// 发送类请求不重试：重试由持有业务幂等键的 Outbox 发起（N-15 / §7.2）。
// 第一次 5xx 就返回错误，且错误里带着状态，供上层决定退避。
func TestHTTPSendIsNotRetriedOn5xx(t *testing.T) {
	var attempts int32
	srv := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/open-apis/im/v1/messages" {
			_, _ = w.Write([]byte(`{"code":0,"tenant_access_token":"t","expire":7200}`))
			return
		}
		atomic.AddInt32(&attempts, 1)
		w.WriteHeader(http.StatusBadGateway)
	})
	tr := NewHTTPTransport(srv.URL, "a", "b", srv.Client())
	tr.Retry = im.RetryPolicy{Attempts: 3, Base: time.Millisecond, Max: time.Millisecond} // 不能被发送路径使用
	_, err := tr.SendMessage(context.Background(), SendRequest{ChatID: "oc", MsgType: "text", Content: "{}"})
	if err == nil {
		t.Fatal("expected error")
	}
	var he *im.HTTPError
	if !errors.As(err, &he) || he.Status != http.StatusBadGateway {
		t.Fatalf("error = %v, want HTTPError 502", err)
	}
	if got := atomic.LoadInt32(&attempts); got != 1 {
		t.Fatalf("send attempts = %d, want 1", got)
	}
}

// 429 的 Retry-After 要原样交给上层（Outbox 用它决定退避），但发送本身只打一次。
func TestHTTPSendReportsRetryAfterWithoutRetrying(t *testing.T) {
	var attempts int32
	srv := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/open-apis/im/v1/messages" {
			_, _ = w.Write([]byte(`{"code":0,"tenant_access_token":"t","expire":7200}`))
			return
		}
		atomic.AddInt32(&attempts, 1)
		w.Header().Set("Retry-After", "2")
		w.WriteHeader(http.StatusTooManyRequests)
	})
	tr := NewHTTPTransport(srv.URL, "a", "b", srv.Client())
	_, err := tr.SendMessage(context.Background(), SendRequest{ChatID: "oc", MsgType: "text", Content: "{}"})
	var he *im.HTTPError
	if !errors.As(err, &he) || he.Status != http.StatusTooManyRequests {
		t.Fatalf("error = %v, want HTTPError 429", err)
	}
	if he.RetryAfter != 2*time.Second {
		t.Fatalf("RetryAfter = %v, want 2s", he.RetryAfter)
	}
	if got := atomic.LoadInt32(&attempts); got != 1 {
		t.Fatalf("send attempts = %d, want 1", got)
	}
}

// 400 不重试：参数/权限问题重试只会延后失败。
func TestHTTPDoesNotRetry400(t *testing.T) {
	var attempts int32
	srv := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/open-apis/im/v1/messages" {
			_, _ = w.Write([]byte(`{"code":0,"tenant_access_token":"t","expire":7200}`))
			return
		}
		atomic.AddInt32(&attempts, 1)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"code":1,"msg":"bad request"}`))
	})
	tr := NewHTTPTransport(srv.URL, "a", "b", srv.Client())
	tr.Retry = im.RetryPolicy{Attempts: 3, Base: time.Millisecond, Max: time.Millisecond}
	_, err := tr.SendMessage(context.Background(), SendRequest{ChatID: "oc", MsgType: "text", Content: "{}"})
	if err == nil {
		t.Fatal("expected error")
	}
	var he *im.HTTPError
	if !errors.As(err, &he) || he.Status != http.StatusBadRequest {
		t.Fatalf("error = %v, want HTTPError 400", err)
	}
	if got := atomic.LoadInt32(&attempts); got != 1 {
		t.Fatalf("attempts = %d, want 1", got)
	}
}

// 401 是 token 过期：清缓存、换新 token、重试一次，而不是把 401 当业务错误。
func TestHTTPRefreshesTokenOn401(t *testing.T) {
	var tokenCalls, sendCalls int32
	srv := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/open-apis/auth/v3/tenant_access_token/internal":
			n := atomic.AddInt32(&tokenCalls, 1)
			_, _ = w.Write([]byte(`{"code":0,"tenant_access_token":"t-` + string(rune('0'+n)) + `","expire":7200}`))
		case "/open-apis/im/v1/messages":
			n := atomic.AddInt32(&sendCalls, 1)
			if n == 1 {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte(`{"code":0,"data":{"message_id":"om_ok"}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	tr := NewHTTPTransport(srv.URL, "a", "b", srv.Client())
	tr.Retry = im.RetryPolicy{Attempts: 2, Base: time.Millisecond, Max: time.Millisecond}
	if _, err := tr.SendMessage(context.Background(), SendRequest{ChatID: "oc", MsgType: "text", Content: "{}"}); err != nil {
		t.Fatal(err)
	}
	if got := atomic.LoadInt32(&tokenCalls); got != 2 {
		t.Fatalf("token calls = %d, want 2 (refresh once)", got)
	}
	if got := atomic.LoadInt32(&sendCalls); got != 2 {
		t.Fatalf("send calls = %d, want 2", got)
	}
}

// 机器人被移出群这类业务码是"永久不可达"：转成 im.ErrGone，让 Outbox 停止重试。
func TestHTTPGoneCodeMapsToErrGone(t *testing.T) {
	srv := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/open-apis/im/v1/messages" {
			_, _ = w.Write([]byte(`{"code":0,"tenant_access_token":"t","expire":7200}`))
			return
		}
		_, _ = w.Write([]byte(`{"code":230002,"msg":"bot is not in the chat"}`))
	})
	tr := NewHTTPTransport(srv.URL, "a", "b", srv.Client())
	_, err := tr.SendMessage(context.Background(), SendRequest{ChatID: "oc", MsgType: "text", Content: "{}"})
	if !errors.Is(err, im.ErrGone) {
		t.Fatalf("error = %v, want ErrGone", err)
	}
}

// 普通业务错误不重试，且保留 code 供日志/面板定位。
func TestHTTPAPIErrorKeepsCode(t *testing.T) {
	var attempts int32
	srv := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/open-apis/im/v1/messages" {
			_, _ = w.Write([]byte(`{"code":0,"tenant_access_token":"t","expire":7200}`))
			return
		}
		atomic.AddInt32(&attempts, 1)
		_, _ = w.Write([]byte(`{"code":10003,"msg":"invalid parameter"}`))
	})
	tr := NewHTTPTransport(srv.URL, "a", "b", srv.Client())
	tr.Retry = im.RetryPolicy{Attempts: 3, Base: time.Millisecond, Max: time.Millisecond}
	_, err := tr.SendMessage(context.Background(), SendRequest{ChatID: "oc", MsgType: "text", Content: "{}"})
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Code != 10003 {
		t.Fatalf("error = %v, want APIError 10003", err)
	}
	if got := atomic.LoadInt32(&attempts); got != 1 {
		t.Fatalf("attempts = %d, want 1", got)
	}
}

// 消息列表：分页参数与嵌套 JSON 解析都要对（content 是 JSON 字符串）。
func TestHTTPListMessagesShape(t *testing.T) {
	srv := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/open-apis/auth/v3/tenant_access_token/internal":
			_, _ = w.Write([]byte(`{"code":0,"tenant_access_token":"t","expire":7200}`))
		case "/open-apis/im/v1/messages":
			q := r.URL.Query()
			if q.Get("container_id_type") != "chat" || q.Get("container_id") != "oc_1" {
				t.Errorf("container query = %v", q)
			}
			if q.Get("start_time") != "1700000000" {
				t.Errorf("start_time = %q, want millis/1000", q.Get("start_time"))
			}
			if q.Get("sort_type") != "ByCreateTimeAsc" {
				t.Errorf("sort_type = %q", q.Get("sort_type"))
			}
			_, _ = w.Write([]byte(`{"code":0,"data":{"has_more":true,"page_token":"p2","items":[
				{"message_id":"om_1","chat_id":"oc_1","chat_type":"group","msg_type":"text",
				 "create_time":"1700000000123","sender":{"id":"ou_alice","sender_type":"user"},
				 "body":{"content":"{\"text\":\"hi\"}"},
				 "mentions":[{"key":"@_user_1","name":"ZeoKeel","id":{"open_id":"ou_bot"}}]}]}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	tr := NewHTTPTransport(srv.URL, "a", "b", srv.Client())
	page, err := tr.ListMessages(context.Background(), ListRequest{ChatID: "oc_1", StartMillis: 1_700_000_000_000})
	if err != nil {
		t.Fatal(err)
	}
	if !page.HasMore || page.PageToken != "p2" || len(page.Messages) != 1 {
		t.Fatalf("page = %+v", page)
	}
	m := page.Messages[0]
	if m.MessageID != "om_1" || m.SenderID != "ou_alice" || m.SenderType != "user" || m.CreateMillis != 1_700_000_000_123 {
		t.Fatalf("message = %+v", m)
	}
	if len(m.Mentions) != 1 || m.Mentions[0].OpenID != "ou_bot" || m.Content != `{"text":"hi"}` {
		t.Fatalf("mentions/content = %+v %q", m.Mentions, m.Content)
	}
}

// 成员列表：分页参数与字段解析。
func TestHTTPListMembersShape(t *testing.T) {
	srv := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/open-apis/auth/v3/tenant_access_token/internal":
			_, _ = w.Write([]byte(`{"code":0,"tenant_access_token":"t","expire":7200}`))
		case "/open-apis/im/v1/chats/oc_1/members":
			q := r.URL.Query()
			if q.Get("member_id_type") != "open_id" || q.Get("page_token") != "p2" {
				t.Errorf("members query = %v", q)
			}
			_, _ = w.Write([]byte(`{"code":0,"data":{"has_more":false,"items":[{"member_id":"ou_a","name":"A"},{"member_id":"","name":"bad"}]}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	tr := NewHTTPTransport(srv.URL, "a", "b", srv.Client())
	page, err := tr.ListMembers(context.Background(), "oc_1", "p2")
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].ID != "ou_a" {
		t.Fatalf("members = %+v", page.Items)
	}
}

// 读类请求的重试次数仍受 RetryPolicy 约束：耗尽后返回最后一次错误。
func TestHTTPRetryExhaustionOnRead(t *testing.T) {
	var attempts int32
	srv := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/open-apis/im/v1/messages" {
			_, _ = w.Write([]byte(`{"code":0,"tenant_access_token":"t","expire":7200}`))
			return
		}
		atomic.AddInt32(&attempts, 1)
		w.WriteHeader(http.StatusBadGateway)
	})
	tr := NewHTTPTransport(srv.URL, "a", "b", srv.Client())
	tr.Retry = im.RetryPolicy{Attempts: 3, Base: time.Millisecond, Max: time.Millisecond}
	if _, err := tr.ListMessages(context.Background(), ListRequest{ChatID: "oc"}); err == nil {
		t.Fatal("expected error after retries")
	}
	if got := atomic.LoadInt32(&attempts); got != 3 {
		t.Fatalf("attempts = %d, want 3", got)
	}
}

// 并发取 token 只打一次接口：避免刷新惊群（N-1 的 token 缓存正确性）。
func TestHTTPTokenRefreshIsSingleFlight(t *testing.T) {
	var tokenCalls int32
	srv := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&tokenCalls, 1)
		time.Sleep(10 * time.Millisecond) // 让并发调用真正重叠
		_, _ = w.Write([]byte(`{"code":0,"tenant_access_token":"t-1","expire":7200}`))
	})
	tr := NewHTTPTransport(srv.URL, "a", "b", srv.Client())
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := tr.TenantToken(context.Background())
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("TenantToken: %v", err)
		}
	}
	if got := atomic.LoadInt32(&tokenCalls); got != 1 {
		t.Fatalf("token calls = %d, want 1", got)
	}
}

// 提前刷新：缓存到"过期前 10%（最多 1 分钟）"，时钟推进到该点之后要重新取。
func TestHTTPTokenRefreshesBeforeExpiry(t *testing.T) {
	var tokenCalls int32
	srv := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&tokenCalls, 1)
		_, _ = w.Write([]byte(`{"code":0,"tenant_access_token":"t-1","expire":100}`))
	})
	now := time.Unix(1_700_000_000, 0)
	tr := NewHTTPTransport(srv.URL, "a", "b", srv.Client())
	tr.Now = func() time.Time { return now }

	tok, err := tr.TenantToken(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if tok.Expire != 90*time.Second {
		t.Fatalf("Expire = %v, want 90s (100s life minus 10 percent skew)", tok.Expire)
	}
	now = now.Add(89 * time.Second) // 仍在窗口内
	if _, err := tr.TenantToken(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := atomic.LoadInt32(&tokenCalls); got != 1 {
		t.Fatalf("token calls = %d, want 1 (cache hit)", got)
	}
	now = now.Add(2 * time.Second) // 越过提前刷新点
	if _, err := tr.TenantToken(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := atomic.LoadInt32(&tokenCalls); got != 2 {
		t.Fatalf("token calls = %d, want 2 (early refresh)", got)
	}
}

// 发送遇到 401 仍恢复一次：401 是 token 过期，不是"服务端可能已接受"的含混失败。
func TestHTTPSendRefreshesExpiredTokenOnce(t *testing.T) {
	var tokenCalls, sends int32
	srv := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/open-apis/auth/v3/tenant_access_token/internal":
			n := atomic.AddInt32(&tokenCalls, 1)
			_, _ = w.Write([]byte(fmt.Sprintf(`{"code":0,"tenant_access_token":"t-%d","expire":7200}`, n)))
		case r.URL.Path == "/open-apis/im/v1/messages":
			if atomic.AddInt32(&sends, 1) == 1 {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			if got := r.Header.Get("Authorization"); got != "Bearer t-2" {
				t.Errorf("auth = %q, want refreshed token", got)
			}
			_, _ = w.Write([]byte(`{"code":0,"data":{"message_id":"om_ok"}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	tr := NewHTTPTransport(srv.URL, "a", "b", srv.Client())
	if _, err := tr.SendMessage(context.Background(), SendRequest{ChatID: "oc", MsgType: "text", Content: "{}"}); err != nil {
		t.Fatal(err)
	}
	if tokenCalls != 2 || sends != 2 {
		t.Fatalf("token calls = %d, sends = %d; want 2/2", tokenCalls, sends)
	}
}
