package im

import (
	"context"
	"sync"
	"time"
)

// HealthState 是驱动自己那一行"连接是否活着"的状态，与 sources.HealthState
// 取值保持一致：面板把 Source 的 health 显示在"来源"页，驱动级 health 用来
// 回答"为什么这个 Source 收不到消息"（token 过期、Stream 断线…）。
type HealthState string

const (
	HealthStarting HealthState = "starting"
	HealthReady    HealthState = "ready"
	HealthPaused   HealthState = "paused"
	HealthError    HealthState = "error"
)

// Health 是一条只读状态行。Detail 写人话，不写错误码，原因见原型 §3.5：
// 面板与状态脚本读的是同一份事实，而不是各自拼字符串。
type Health struct {
	State  HealthState
	Detail string
	At     time.Time
}

// HealthReporter 是可选能力：能连网的驱动实现它；纯假驱动不必实现。调用方
// 用类型断言探测（`r, ok := p.(HealthReporter)`），"没有实现"与"连接坏了"
// 是两件事，不要混为一谈。
//
// 为什么这里和 sources.Health 是两份结构（而不是复用 sources 的）：im 是
// Source 下面的驱动契约，反向 import sources 会造成分层倒挂。两份结构描述
// 同一个面板"来源"行规格，字段语义由两边的测试分别守护。
type HealthReporter interface {
	Health(ctx context.Context) Health
}

// HealthTracker 是 HealthReporter 的线程安全实现。驱动的轮询 goroutine、
// 发送路径与面板读取会并发碰它，所以状态本身必须带锁，而不是各驱动各写一份。
type HealthTracker struct {
	mu sync.Mutex
	h  Health
}

// NewHealthTracker 返回初始为 starting 的 tracker。
func NewHealthTracker() *HealthTracker {
	return &HealthTracker{h: Health{State: HealthStarting, Detail: "not connected yet"}}
}

// Snapshot 返回当前状态（值拷贝，调用方可放心读）。
func (t *HealthTracker) Snapshot() Health {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.h
}

// Set 直接覆盖状态。
func (t *HealthTracker) Set(h Health) {
	t.mu.Lock()
	t.h = h
	t.mu.Unlock()
}

// Ready 记一次成功：连接/轮询正常时调用。
func (t *HealthTracker) Ready(at time.Time, detail string) {
	t.Set(Health{State: HealthReady, Detail: detail, At: at})
}

// Failed 记一次失败：只在连续失败达到阈值时置 error（网络抖动不该报警）。
func (t *HealthTracker) Failed(at time.Time, detail string) {
	t.Set(Health{State: HealthError, Detail: detail, At: at})
}

// Unhealthy 便于轮询循环决定要不要发通知。
func (t *HealthTracker) Unhealthy() bool {
	return t.Snapshot().State == HealthError
}
