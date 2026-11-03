package dingtalk

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zeokeel/super-agent/internal/im"
)

func testServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv
}

// 真实传输的请求形状：token 请求体、鉴权头、群聊/单聊两个端点、内容模板。
func TestHTTPTokenAndSendShapes(t *testing.T) {
	var (
		mu         sync.Mutex
		tokenCalls int
		sends      []map[string]any
		paths      []string
		authHeads  []string
	)
	srv := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1.0/oauth2/accessToken":
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("token body: %v", err)
			}
			if body["appKey"] != "key" || body["appSecret"] != "secret" {
				t.Errorf("token body = %v", body)
			}
			mu.Lock()
			tokenCalls++
			mu.Unlock()
			_, _ = w.Write([]byte(`{"accessToken":"dt-1","expireIn":7200}`))
		case "/v1.0/robot/groupMessages/send", "/v1.0/robot/oToMessages/batchSend":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			mu.Lock()
			sends = append(sends, body)
			paths = append(paths, r.URL.Path)
			authHeads = append(authHeads, r.Header.Get("x-acs-dingtalk-access-token"))
			mu.Unlock()
			_, _ = w.Write([]byte(`{"processQueryKey":"q1"}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})
	tr := NewHTTPTransport(srv.URL, "key", "secret", srv.Client())

	if _, err := tr.AccessToken(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := tr.AccessToken(context.Background()); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	if tokenCalls != 1 {
		t.Fatalf("token endpoint called %d times, want 1 (cached)", tokenCalls)
	}
	mu.Unlock()

	group, err := tr.SendRobot(context.Background(), SendRequest{
		RobotCode: "robot", ConversationID: "cid_1", MsgKey: "sampleText", MsgParam: `{"content":"hi"}`,
	})
	if err != nil || group.MessageID != "q1" {
		t.Fatalf("group send = %+v, %v", group, err)
	}
	direct, err := tr.SendRobot(context.Background(), SendRequest{
		RobotCode: "robot", Direct: true, UserID: "ding_u1", MsgKey: "sampleText", MsgParam: `{"content":"hi"}`,
	})
	if err != nil || direct.MessageID != "q1" {
		t.Fatalf("direct send = %+v, %v", direct, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(sends) != 2 {
		t.Fatalf("sends = %d", len(sends))
	}
	if paths[0] != "/v1.0/robot/groupMessages/send" || sends[0]["openConversationId"] != "cid_1" || sends[0]["robotCode"] != "robot" {
		t.Fatalf("group request = %s %v", paths[0], sends[0])
	}
	if paths[1] != "/v1.0/robot/oToMessages/batchSend" {
		t.Fatalf("direct path = %s", paths[1])
	}
	if users, ok := sends[1]["userIds"].([]any); !ok || len(users) != 1 || users[0] != "ding_u1" {
		t.Fatalf("direct userIds = %v", sends[1]["userIds"])
	}
	for _, head := range authHeads {
		if head != "dt-1" {
			t.Fatalf("auth header = %q, want dt-1", head)
		}
	}
}

// 读类请求（GET）遇到 429 值得重试。
func TestHTTPRetriesReadsOn429(t *testing.T) {
	var attempts int32
	srv := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1.0/im/chatMembers" {
			_, _ = w.Write([]byte(`{"accessToken":"dt","expireIn":7200}`))
			return
		}
		if atomic.AddInt32(&attempts, 1) == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write([]byte(`{"nextToken":"","hasMore":false,"members":[]}`))
	})
	tr := NewHTTPTransport(srv.URL, "k", "s", srv.Client())
	tr.Retry = im.RetryPolicy{Attempts: 3, Base: time.Millisecond, Max: time.Millisecond}
	if _, err := tr.ListMembers(context.Background(), "cid", ""); err != nil {
		t.Fatal(err)
	}
	if got := atomic.LoadInt32(&attempts); got != 2 {
		t.Fatalf("attempts = %d, want 2", got)
	}
}

// 发送类请求不重试：钉钉没有服务端幂等键，传输层重试会绕过 driver 的 sent
// 去重并产生重复消息（N-15 / §7.2）。第一次 5xx 就返回错误，交给 Outbox。
func TestHTTPSendIsNotRetriedOn5xx(t *testing.T) {
	var attempts int32
	srv := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1.0/robot/groupMessages/send" {
			_, _ = w.Write([]byte(`{"accessToken":"dt","expireIn":7200}`))
			return
		}
		atomic.AddInt32(&attempts, 1)
		w.WriteHeader(http.StatusBadGateway)
	})
	tr := NewHTTPTransport(srv.URL, "k", "s", srv.Client())
	tr.Retry = im.RetryPolicy{Attempts: 3, Base: time.Millisecond, Max: time.Millisecond} // 不能被发送路径使用
	_, err := tr.SendRobot(context.Background(), SendRequest{RobotCode: "r", ConversationID: "c", MsgKey: "sampleText", MsgParam: "{}"})
	var he *im.HTTPError
	if !errors.As(err, &he) || he.Status != http.StatusBadGateway {
		t.Fatalf("error = %v, want HTTPError 502", err)
	}
	if got := atomic.LoadInt32(&attempts); got != 1 {
		t.Fatalf("send attempts = %d, want 1", got)
	}
}

// 429 的 Retry-After 交给上层（Outbox）做退避，但发送本身只打一次。
func TestHTTPSendReportsRetryAfterWithoutRetrying(t *testing.T) {
	var attempts int32
	srv := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1.0/robot/groupMessages/send" {
			_, _ = w.Write([]byte(`{"accessToken":"dt","expireIn":7200}`))
			return
		}
		atomic.AddInt32(&attempts, 1)
		w.Header().Set("Retry-After", "3")
		w.WriteHeader(http.StatusTooManyRequests)
	})
	tr := NewHTTPTransport(srv.URL, "k", "s", srv.Client())
	_, err := tr.SendRobot(context.Background(), SendRequest{RobotCode: "r", ConversationID: "c", MsgKey: "sampleText", MsgParam: "{}"})
	var he *im.HTTPError
	if !errors.As(err, &he) || he.Status != http.StatusTooManyRequests {
		t.Fatalf("error = %v, want HTTPError 429", err)
	}
	if he.RetryAfter != 3*time.Second {
		t.Fatalf("RetryAfter = %v, want 3s", he.RetryAfter)
	}
	if got := atomic.LoadInt32(&attempts); got != 1 {
		t.Fatalf("send attempts = %d, want 1", got)
	}
}

// 4xx 不重试。
func TestHTTPDoesNotRetry400(t *testing.T) {
	var attempts int32
	srv := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1.0/robot/groupMessages/send" {
			_, _ = w.Write([]byte(`{"accessToken":"dt","expireIn":7200}`))
			return
		}
		atomic.AddInt32(&attempts, 1)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"code":"invalidParameter","message":"bad"}`))
	})
	tr := NewHTTPTransport(srv.URL, "k", "s", srv.Client())
	tr.Retry = im.RetryPolicy{Attempts: 3, Base: time.Millisecond, Max: time.Millisecond}
	if _, err := tr.SendRobot(context.Background(), SendRequest{RobotCode: "r", ConversationID: "c", MsgKey: "sampleText", MsgParam: "{}"}); err == nil {
		t.Fatal("expected error")
	}
	if got := atomic.LoadInt32(&attempts); got != 1 {
		t.Fatalf("attempts = %d, want 1", got)
	}
}

// 401 刷新 token 后重试一次。
func TestHTTPRefreshesTokenOn401(t *testing.T) {
	var tokenCalls, sendCalls int32
	srv := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1.0/oauth2/accessToken":
			n := atomic.AddInt32(&tokenCalls, 1)
			_, _ = w.Write([]byte(`{"accessToken":"dt-` + string(rune('0'+n)) + `","expireIn":7200}`))
		case "/v1.0/robot/groupMessages/send":
			if atomic.AddInt32(&sendCalls, 1) == 1 {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte(`{"processQueryKey":"q"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	tr := NewHTTPTransport(srv.URL, "k", "s", srv.Client())
	tr.Retry = im.RetryPolicy{Attempts: 2, Base: time.Millisecond, Max: time.Millisecond}
	if _, err := tr.SendRobot(context.Background(), SendRequest{RobotCode: "r", ConversationID: "c", MsgKey: "sampleText", MsgParam: "{}"}); err != nil {
		t.Fatal(err)
	}
	if got := atomic.LoadInt32(&tokenCalls); got != 2 {
		t.Fatalf("token calls = %d, want 2", got)
	}
	if got := atomic.LoadInt32(&sendCalls); got != 2 {
		t.Fatalf("send calls = %d, want 2", got)
	}
}

// 机器人不在群这类错误码是永久不可达；其余业务码保留 code 不重试。
func TestHTTPErrorCodeMapping(t *testing.T) {
	code := "invalidOpenConversationId"
	srv := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1.0/robot/groupMessages/send" {
			_, _ = w.Write([]byte(`{"accessToken":"dt","expireIn":7200}`))
			return
		}
		_, _ = w.Write([]byte(`{"code":"` + code + `","message":"gone"}`))
	})
	tr := NewHTTPTransport(srv.URL, "k", "s", srv.Client())
	if _, err := tr.SendRobot(context.Background(), SendRequest{RobotCode: "r", ConversationID: "c", MsgKey: "sampleText", MsgParam: "{}"}); !errors.Is(err, im.ErrGone) {
		t.Fatalf("err = %v, want ErrGone", err)
	}

	code = "invalidParameter"
	tr2 := NewHTTPTransport(srv.URL, "k", "s", srv.Client())
	_, err := tr2.SendRobot(context.Background(), SendRequest{RobotCode: "r", ConversationID: "c", MsgKey: "sampleText", MsgParam: "{}"})
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Code != "invalidParameter" {
		t.Fatalf("err = %v, want APIError invalidParameter", err)
	}
}

// 成员列表：分页参数与字段解析。
func TestHTTPListMembersShape(t *testing.T) {
	srv := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1.0/oauth2/accessToken":
			_, _ = w.Write([]byte(`{"accessToken":"dt","expireIn":7200}`))
		case "/v1.0/im/chatMembers":
			q := r.URL.Query()
			if q.Get("openConversationId") != "cid_1" || q.Get("nextToken") != "p2" || q.Get("maxResults") != "100" {
				t.Errorf("members query = %v", q)
			}
			_, _ = w.Write([]byte(`{"hasMore":false,"members":[{"userId":"ding_a","name":"A"},{"userId":"","name":"bad"}]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	tr := NewHTTPTransport(srv.URL, "k", "s", srv.Client())
	page, err := tr.ListMembers(context.Background(), "cid_1", "p2")
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].ID != "ding_a" {
		t.Fatalf("members = %+v", page.Items)
	}
}

// 404/501 表示没有成员接口：转成 ErrUnsupported，让上层降级。
func TestHTTPListMembersUnsupportedOn404(t *testing.T) {
	srv := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1.0/oauth2/accessToken" {
			_, _ = w.Write([]byte(`{"accessToken":"dt","expireIn":7200}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})
	tr := NewHTTPTransport(srv.URL, "k", "s", srv.Client())
	if _, err := tr.ListMembers(context.Background(), "cid_1", ""); !errors.Is(err, im.ErrUnsupported) {
		t.Fatalf("err = %v, want ErrUnsupported", err)
	}
}

// HTTP 传输没有 Stream：必须明确报不支持，而不是伪造空批让驱动空转。
func TestHTTPStreamIsUnsupported(t *testing.T) {
	tr := NewHTTPTransport("http://127.0.0.1:1", "k", "s", nil)
	if _, err := tr.StreamEvents(context.Background(), ""); !errors.Is(err, im.ErrUnsupported) {
		t.Fatalf("err = %v, want ErrUnsupported", err)
	}
}

// 读类请求的重试次数仍受 RetryPolicy 约束。
func TestHTTPRetryExhaustionOnRead(t *testing.T) {
	var attempts int32
	srv := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1.0/im/chatMembers" {
			_, _ = w.Write([]byte(`{"accessToken":"dt","expireIn":7200}`))
			return
		}
		atomic.AddInt32(&attempts, 1)
		w.WriteHeader(http.StatusBadGateway)
	})
	tr := NewHTTPTransport(srv.URL, "k", "s", srv.Client())
	tr.Retry = im.RetryPolicy{Attempts: 3, Base: time.Millisecond, Max: time.Millisecond}
	if _, err := tr.ListMembers(context.Background(), "cid", ""); err == nil {
		t.Fatal("expected error after retries")
	}
	if got := atomic.LoadInt32(&attempts); got != 3 {
		t.Fatalf("attempts = %d, want 3", got)
	}
}

// 并发取 token 只打一次接口（避免刷新惊群）。
func TestHTTPTokenRefreshIsSingleFlight(t *testing.T) {
	var tokenCalls int32
	srv := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&tokenCalls, 1)
		time.Sleep(10 * time.Millisecond)
		_, _ = w.Write([]byte(`{"accessToken":"dt-1","expireIn":7200}`))
	})
	tr := NewHTTPTransport(srv.URL, "k", "s", srv.Client())
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := tr.AccessToken(context.Background())
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("AccessToken: %v", err)
		}
	}
	if got := atomic.LoadInt32(&tokenCalls); got != 1 {
		t.Fatalf("token calls = %d, want 1", got)
	}
}

// 提前刷新：过期前 10%（最多 1 分钟）之后重新取 token。
func TestHTTPTokenRefreshesBeforeExpiry(t *testing.T) {
	var tokenCalls int32
	srv := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&tokenCalls, 1)
		_, _ = w.Write([]byte(`{"accessToken":"dt-1","expireIn":100}`))
	})
	now := time.Unix(1_700_000_000, 0)
	tr := NewHTTPTransport(srv.URL, "k", "s", srv.Client())
	tr.Now = func() time.Time { return now }

	tok, err := tr.AccessToken(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if tok.Expire != 90*time.Second {
		t.Fatalf("Expire = %v, want 90s (100s life minus 10 percent skew)", tok.Expire)
	}
	now = now.Add(91 * time.Second)
	if _, err := tr.AccessToken(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := atomic.LoadInt32(&tokenCalls); got != 2 {
		t.Fatalf("token calls = %d, want 2 (early refresh)", got)
	}
}
