package im

import (
	"context"
	"sync"
)

// Subscriptions tracks a driver's active subscriptions so Close can cancel them.
//
// Why a helper instead of each driver rolling its own: a driver's Subscribe runs
// for the whole channel lifetime, and the runtime's Stop must be able to stop it
// deterministically (review P1-10: Stop must Close the driver; no leaked
// goroutines/subprocesses). Track derives a child context, registers it, and
// returns a release func; Close cancels every registered child and makes later
// Track calls fail with ErrClosed.
type Subscriptions struct {
	mu     sync.Mutex
	next   uint64
	m      map[uint64]context.CancelFunc
	closed bool
}

// Track derives a cancellable child of parent and registers it. Typical use:
//
//	ctx, release, err := d.subs.Track(ctx)
//	if err != nil { return err }
//	defer release()
func (s *Subscriptions) Track(parent context.Context) (context.Context, func(), error) {
	ctx, cancel := context.WithCancel(parent)
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		cancel()
		return nil, nil, ErrClosed
	}
	if s.m == nil {
		s.m = map[uint64]context.CancelFunc{}
	}
	s.next++
	id := s.next
	s.m[id] = cancel
	s.mu.Unlock()
	release := func() {
		s.mu.Lock()
		if c, ok := s.m[id]; ok {
			delete(s.m, id)
			c()
		}
		s.mu.Unlock()
	}
	return ctx, release, nil
}

// Close cancels every active subscription and rejects future ones. Idempotent.
func (s *Subscriptions) Close() {
	s.mu.Lock()
	s.closed = true
	cancels := make([]context.CancelFunc, 0, len(s.m))
	for _, cancel := range s.m {
		cancels = append(cancels, cancel)
	}
	s.m = nil
	s.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
}

// Closed reports whether Close has been called.
func (s *Subscriptions) Closed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}
