package im

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRetryAfterAcceptsSecondsAndHTTPDates(t *testing.T) {
	cases := map[string]time.Duration{
		"":        0,
		"0":       0,
		"-5":      0,
		"garbage": 0,
		"2":       2 * time.Second,
		" 3 ":     3 * time.Second,
	}
	for raw, want := range cases {
		if got := RetryAfter(raw); got != want {
			t.Errorf("RetryAfter(%q) = %v, want %v", raw, got, want)
		}
	}
	future := time.Now().Add(90 * time.Second).UTC().Format(http.TimeFormat)
	if got := RetryAfter(future); got < 80*time.Second || got > 91*time.Second {
		t.Errorf("RetryAfter(HTTP-date) = %v, want ~90s", got)
	}
	past := time.Now().Add(-time.Minute).UTC().Format(http.TimeFormat)
	if got := RetryAfter(past); got != 0 {
		t.Errorf("RetryAfter(past HTTP-date) = %v, want 0", got)
	}
}

func TestSnippetTruncates(t *testing.T) {
	if got := Snippet([]byte("  short  ")); got != "short" {
		t.Fatalf("Snippet = %q", got)
	}
	long := make([]byte, 400)
	for i := range long {
		long[i] = 'x'
	}
	got := Snippet(long)
	if len([]rune(got)) != 301 || got[len(got)-1:] == "x" {
		t.Fatalf("Snippet long = %d bytes", len(got))
	}
}

// NoRetry 是发送类请求的契约：一次失败只尝试一次，错误原样上报给 Outbox。
func TestHTTPClientNoRetryAttemptsOnce(t *testing.T) {
	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	c := &HTTPClient{BaseURL: srv.URL, HTTP: srv.Client()}
	_, err := c.Call(context.Background(), http.MethodPost, "/send", nil, map[string]string{"x": "y"}, false, NoRetry)
	var he *HTTPError
	if !errors.As(err, &he) || he.Status != http.StatusInternalServerError {
		t.Fatalf("err = %v, want HTTPError 500", err)
	}
	if got := atomic.LoadInt32(&attempts); got != 1 {
		t.Fatalf("attempts = %d, want 1", got)
	}
}

// 读类请求仍按策略重试：429 之后成功。
func TestHTTPClientRetriesTransientReads(t *testing.T) {
	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&attempts, 1) == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	c := &HTTPClient{BaseURL: srv.URL, HTTP: srv.Client()}
	var out struct {
		OK bool `json:"ok"`
	}
	if err := c.DoJSON(context.Background(), http.MethodGet, "/list", nil, nil, &out, false,
		RetryPolicy{Attempts: 3, Base: time.Millisecond, Max: time.Millisecond}); err != nil {
		t.Fatalf("DoJSON: %v", err)
	}
	if !out.OK || attempts != 2 {
		t.Fatalf("ok = %v, attempts = %d", out.OK, attempts)
	}
}

// 401 触发一次 token 失效 + 重试；重试成功后不 Infinite loop。
func TestHTTPClientRefreshesOn401Once(t *testing.T) {
	var calls, invalidated int32
	token := "old"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if token == "old" { // 旧 token 一律 401：验证"失效 → 换新 → 重试一次"
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+token {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		atomic.AddInt32(&calls, 1)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	c := &HTTPClient{
		BaseURL: srv.URL,
		HTTP:    srv.Client(),
		Authorize: func(ctx context.Context, req *http.Request) error {
			req.Header.Set("Authorization", "Bearer "+token)
			return nil
		},
		OnUnauthorized: func() {
			atomic.AddInt32(&invalidated, 1)
			token = "new"
		},
	}
	var out struct {
		OK bool `json:"ok"`
	}
	if err := c.DoJSON(context.Background(), http.MethodGet, "/self", nil, nil, &out, true, NoRetry); err != nil {
		t.Fatalf("DoJSON: %v", err)
	}
	if !out.OK {
		t.Fatal("payload was not decoded after the token refresh")
	}
	if calls != 1 || invalidated != 1 {
		t.Fatalf("calls = %d, invalidations = %d", calls, invalidated)
	}
}

// TokenCache：并发只刷新一次；提前刷新窗口内命中；401 失效后重新取。
func TestTokenCacheSingleFlightAndExpiry(t *testing.T) {
	var fetches int32
	now := time.Unix(1_700_000_000, 0)
	cache := &TokenCache{Now: func() time.Time { return now }}
	fetch := func(context.Context) (string, time.Time, error) {
		atomic.AddInt32(&fetches, 1)
		time.Sleep(10 * time.Millisecond)
		life := 100 * time.Second
		return "tok", now.Add(life - TokenSkew(life)), nil
	}
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			value, expire, err := cache.Get(context.Background(), fetch)
			if err == nil && (value != "tok" || expire != 90*time.Second) {
				err = errors.New("unexpected token value/expiry")
			}
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if got := atomic.LoadInt32(&fetches); got != 1 {
		t.Fatalf("fetches = %d, want 1", got)
	}
	now = now.Add(91 * time.Second)
	if _, _, err := cache.Get(context.Background(), fetch); err != nil {
		t.Fatal(err)
	}
	if got := atomic.LoadInt32(&fetches); got != 2 {
		t.Fatalf("fetches = %d, want 2 after the refresh window", got)
	}
	cache.Invalidate()
	if _, _, err := cache.Get(context.Background(), fetch); err != nil {
		t.Fatal(err)
	}
	if got := atomic.LoadInt32(&fetches); got != 3 {
		t.Fatalf("fetches = %d, want 3 after invalidate", got)
	}
}

// 刷新失败时等待者拿到同一个错误；错误不缓存，下一次会重新尝试。
func TestTokenCacheSharesFetchErrorWithoutCachingIt(t *testing.T) {
	var fetches int32
	boom := errors.New("boom")
	cache := &TokenCache{}
	failing := func(context.Context) (string, time.Time, error) {
		atomic.AddInt32(&fetches, 1)
		time.Sleep(5 * time.Millisecond)
		return "", time.Time{}, boom
	}
	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, err := cache.Get(context.Background(), failing)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if !errors.Is(err, boom) {
			t.Fatalf("err = %v, want boom", err)
		}
	}
	if got := atomic.LoadInt32(&fetches); got != 1 {
		t.Fatalf("fetches = %d, want 1 (waiters share the failure)", got)
	}
	if _, _, err := cache.Get(context.Background(), failing); !errors.Is(err, boom) {
		t.Fatalf("second Get err = %v", err)
	}
}
