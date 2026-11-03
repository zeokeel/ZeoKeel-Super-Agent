package im

import (
	"context"
	"sync"
	"time"
)

// TokenCache 是 IM 传输共用的令牌缓存：并发安全、提前刷新、单飞刷新。
//
// 为什么需要"单飞"（single flight）：飞书/钉钉的 token 有效期以小时计，但
// 多个会话同时收到 401 时会各自去换 token，把一次鉴权故障放大成 N 次请求。
// 这里让并发调用共享同一次刷新；刷新失败时等待者拿到同一个错误，不各自重试
// （错误不会缓存，下一次 Get 会重新尝试）。
//
// 刷新窗口 = 有效期前 10%（最多提前 1 分钟）：修掉"刚好在边界过期"造成的
// 偶发 401，同时不会因为窗口太大而白白多换几次 token。
type TokenCache struct {
	// Now 便于测试注入时钟；空用 time.Now。
	Now func() time.Time

	mu       sync.Mutex
	value    string
	until    time.Time
	fetching *fetchEntry
}

// fetchEntry 是一次进行中的刷新。
type fetchEntry struct {
	done chan struct{}
	err  error
}

func (c *TokenCache) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// Get 返回缓存里的令牌；过期或不存在时调用 fetch 刷新。
//
// fetch 返回 (value, absoluteExpiry, error)：absoluteExpiry 是本实现按提前
// 刷新窗口算好的**可用截止时刻**（不是服务端原始有效期）。Expire 返回它的
// 剩余时间。
func (c *TokenCache) Get(ctx context.Context, fetch func(context.Context) (string, time.Time, error)) (string, time.Duration, error) {
	for {
		if value, expire, ok := c.cached(); ok {
			return value, expire, nil
		}
		c.mu.Lock()
		f := c.fetching
		if f != nil {
			c.mu.Unlock()
			select {
			case <-f.done:
			case <-ctx.Done():
				return "", 0, ctx.Err()
			}
			if f.err != nil {
				return "", 0, f.err
			}
			continue // 别人刚刷好：回到循环读缓存
		}
		f = &fetchEntry{done: make(chan struct{})}
		c.fetching = f
		c.mu.Unlock()

		value, until, err := fetch(ctx)

		c.mu.Lock()
		if err == nil {
			c.value, c.until = value, until
		}
		f.err = err
		c.fetching = nil
		c.mu.Unlock()
		close(f.done)
		if err != nil {
			return "", 0, err
		}
		return value, until.Sub(c.now()), nil
	}
}

// Invalidate 清掉缓存（401 后调用）。
func (c *TokenCache) Invalidate() {
	c.mu.Lock()
	c.value, c.until = "", time.Time{}
	c.mu.Unlock()
}

// TokenSkew 返回"提前刷新量"：有效期的 10%，最多 1 分钟。
func TokenSkew(life time.Duration) time.Duration {
	skew := life / 10
	if skew > time.Minute {
		skew = time.Minute
	}
	return skew
}

func (c *TokenCache) cached() (string, time.Duration, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	if c.value == "" || !now.Before(c.until) {
		return "", 0, false
	}
	return c.value, c.until.Sub(now), true
}
