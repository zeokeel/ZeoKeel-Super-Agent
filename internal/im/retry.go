package im

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// RetryPolicy 是传输层的一次性重试策略：只重试"可能自己好起来"的失败，
// 业务错误重试只会放大问题、拖长面板里"为什么发不出去"的定位时间。
//
// 发送类操作必须传 NoRetry（见 httpclient.go）：重试的所有者是持有业务幂等键
// 的 Outbox，传输层重试会绕过 driver 的去重（N-15）。读类操作才用默认策略。
type RetryPolicy struct {
	// Attempts 是总尝试次数（含第一次），<=0 视为默认 3。
	Attempts int
	// Base/Max 是退避起点与上限，默认 500ms → 5s，指数增长。
	Base, Max time.Duration
}

// WithDefaults 补齐零值，方便驱动配置只写关心的字段。
func (p RetryPolicy) WithDefaults() RetryPolicy {
	if p.Attempts <= 0 {
		p.Attempts = 3
	}
	if p.Base <= 0 {
		p.Base = 500 * time.Millisecond
	}
	if p.Max <= 0 {
		p.Max = 5 * time.Second
	}
	if p.Max < p.Base {
		p.Max = p.Base
	}
	return p
}

// HTTPError 是传输层对"服务端明确拒绝"的建模：保留状态码与响应体片段，
// 让上层能区分 404/401（要人处理）与 429/5xx（值得重试）。
type HTTPError struct {
	Status     int
	RetryAfter time.Duration // 429 时来自 Retry-After，0 表示不知道
	Body       string
	Err        error
}

// Error 实现 error。
func (e *HTTPError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("im: http %d: %v", e.Status, e.Err)
	}
	if e.Body != "" {
		return fmt.Sprintf("im: http %d: %s", e.Status, e.Body)
	}
	return fmt.Sprintf("im: http %d", e.Status)
}

// Unwrap 让 errors.Is/As 能穿过包装。
func (e *HTTPError) Unwrap() error { return e.Err }

// Temporary 说明这次失败是否值得重试：网络错误、408、429、5xx 值得；
// 其余 4xx 是请求本身的问题，重试不会变好。
func (e *HTTPError) Temporary() bool {
	if e.Err != nil {
		return true
	}
	return e.Status == 408 || e.Status == 429 || e.Status >= 500
}

// IsTransient 判断一个错误是否值得重试。只有实现了 Temporary() bool 的
// 错误才可能为真，业务错误（例如飞书 code!=0）默认不重试——这是故意的：
// 把 400 重试三遍只会让"发不出去"更晚被发现。
func IsTransient(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var t interface{ Temporary() bool }
	return errors.As(err, &t) && t.Temporary()
}

// Do 用指数退避执行一次可能失败的调用。返回最后一次的结果与错误；
// 上下文取消时立刻返回，不把取消误报成"重试耗尽"。
func Do[T any](ctx context.Context, p RetryPolicy, fn func(context.Context) (T, error)) (T, error) {
	p = p.WithDefaults()
	var zero T
	var lastErr error
	delay := p.Base
	for attempt := 1; attempt <= p.Attempts; attempt++ {
		value, err := fn(ctx)
		if err == nil {
			return value, nil
		}
		lastErr = err
		if !IsTransient(err) || attempt == p.Attempts {
			return zero, err
		}
		if werr := sleepCtx(ctx, retryDelay(err, delay)); werr != nil {
			return zero, lastErr
		}
		if delay *= 2; delay > p.Max {
			delay = p.Max
		}
	}
	return zero, lastErr
}

// retryDelay 优先尊重服务端给的 Retry-After：它比我们猜的退避更准。
func retryDelay(err error, fallback time.Duration) time.Duration {
	var he *HTTPError
	if errors.As(err, &he) && he.RetryAfter > 0 {
		return he.RetryAfter
	}
	return fallback
}

// sleepCtx 睡 d，但 ctx 结束就立刻返回，避免关闭应用时卡在退避里。
func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
