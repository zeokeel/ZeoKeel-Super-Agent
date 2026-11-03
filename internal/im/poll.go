package im

import (
	"context"
	"strconv"
	"time"
)

// SubscribeOptions 控制长轮询循环的行为。
//
// 为什么接收端是"轮询循环"而不是 webhook：桌面应用在 NAT 后面，没有公网
// 入口（design §4.8 写死的约束）。飞书/钉钉真正的首选是长连接（WebSocket /
// Stream），但长连接的协议实现需要 SDK 与凭据；传输层把"下一批事件"抽象成
// Poll，长连接实现与长轮询实现可以互换，驱动本身不感知——这就是本包存在的
// 理由：换传输不改驱动，也不改领域。
type SubscribeOptions struct {
	// Cursor 是驱动自己编码的读取位置；空表示"从配置的起点开始"。
	Cursor string
	// Backoff/MaxBackoff：一次轮询失败后的退避，默认 1s 起、30s 封顶。
	// 失败不推进游标，恢复后会重读失败的那一轮；Trigger ID 内容派生，重读
	// 是安全的（§4.4 不变量 1），所以"至少一次"比"可能丢一条"划算。
	Backoff, MaxBackoff time.Duration
	// IdleDelay：本轮没有任何新事件时先等一会儿再拉，避免对"立即返回空"
	// 的传输（测试假实现、服务端限流返空）忙等。真正的长轮询传输会自己
	// 阻塞到有数据或超时，这里只是兜底。
	IdleDelay time.Duration
	// MaxPages：一轮最多连续取几页，防止一次订阅长时间不回游标。
	MaxPages int
	// Health：可选，用来把"连接是否活着"报给面板；连续 3 次失败才置 error，
	// 一次抖动只更新 detail（与原型 §3.5 的 health 阈值一致）。
	Health *HealthTracker
	// Now 便于测试注入时钟；空用 time.Now。
	Now func() time.Time
}

func (o SubscribeOptions) withDefaults() SubscribeOptions {
	if o.Backoff <= 0 {
		o.Backoff = time.Second
	}
	if o.MaxBackoff <= 0 {
		o.MaxBackoff = 30 * time.Second
	}
	if o.MaxBackoff < o.Backoff {
		o.MaxBackoff = o.Backoff
	}
	if o.IdleDelay <= 0 {
		o.IdleDelay = 100 * time.Millisecond
	}
	if o.MaxPages <= 0 {
		o.MaxPages = 8
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	return o
}

// failuresBeforeError 是"连续几次失败才把连接标红"的阈值。
const failuresBeforeError = 3

// SubscribeLongPoll 把 Poller 变成 Receiver：反复取增量，把事件按顺序交给
// out，成功后才推进游标，失败则退避重试。
//
// 关键不变量（与 sources.Poller 的注释同一条纪律）：
//   - 游标只在"这一轮的事件全部交给调用方之后"推进。中途失败或 ctx 取消时
//     游标不动，恢复后重读该轮；重复事件由内容派生 ID 幂等吸收。
//   - 一轮内某一页失败时整轮丢弃（包括已取到但未交付的事件），因为游标
//     不能越过"没交付"的事件；重读整轮的代价可以接受，丢消息不可接受。
func SubscribeLongPoll(ctx context.Context, p Poller, opts SubscribeOptions, out chan<- RawEvent) error {
	opts = opts.withDefaults()
	cursor := opts.Cursor
	backoff := opts.Backoff
	failures := 0
	if opts.Health != nil {
		opts.Health.Set(Health{State: HealthStarting, Detail: "connecting", At: opts.Now()})
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		events, next, err := pollRound(ctx, p, cursor, opts.MaxPages)
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			failures++
			if opts.Health != nil {
				// 一次网络抖动不报警：连续失败到阈值才置 error，避免面板刷红。
				if failures >= failuresBeforeError {
					opts.Health.Failed(opts.Now(), "poll failed "+plural(failures)+" times: "+err.Error())
				} else {
					h := opts.Health.Snapshot()
					h.Detail = "poll failed: " + err.Error()
					opts.Health.Set(h)
				}
			}
			if sleepErr := sleepCtx(ctx, backoff); sleepErr != nil {
				return sleepErr
			}
			if backoff *= 2; backoff > opts.MaxBackoff {
				backoff = opts.MaxBackoff
			}
			continue
		}
		for _, ev := range events {
			select {
			case out <- ev:
			case <-ctx.Done():
				// 游标没推进到 next：恢复后会重读，重复由 ID 吸收。
				return ctx.Err()
			}
		}
		cursor = next
		failures = 0
		backoff = opts.Backoff
		if opts.Health != nil {
			opts.Health.Ready(opts.Now(), readyDetail(len(events), cursor))
		}
		if len(events) == 0 {
			// 没有新事件：让出时间片，避免对"立即返回空"的传输忙等。
			if err := sleepCtx(ctx, opts.IdleDelay); err != nil {
				return err
			}
		}
	}
}

// PollOnce 取一轮增量（最多 maxPages 页），供"单次拉取"场景复用。
// 返回的 next 只有在 err == nil 时才代表"这批都取到了、可以持久化"。
func PollOnce(ctx context.Context, p Poller, cursor string, maxPages int) ([]RawEvent, string, error) {
	if maxPages <= 0 {
		maxPages = 8
	}
	return pollRound(ctx, p, cursor, maxPages)
}

// pollRound 连续取页直到没有下一页；任何一页失败都返回轮次开始处的游标，
// 保证"失败不推进"这条契约成立。
func pollRound(ctx context.Context, p Poller, cursor string, maxPages int) ([]RawEvent, string, error) {
	var all []RawEvent
	next := cursor
	for page := 0; page < maxPages; page++ {
		events, after, err := p.Poll(ctx, next)
		if err != nil {
			return nil, cursor, err
		}
		all = append(all, events...)
		// 传输没有前进时停下：否则空轮次会变成死循环。
		if after == next {
			return all, next, nil
		}
		next = after
		if len(events) == 0 {
			return all, next, nil
		}
	}
	return all, next, nil
}

func readyDetail(events int, cursor string) string {
	if events == 0 {
		return "polling; no new messages"
	}
	return "polling; delivered " + plural(events) + " messages"
}

func plural(n int) string {
	if n == 1 {
		return "1 event"
	}
	return strconv.Itoa(n) + " events"
}
