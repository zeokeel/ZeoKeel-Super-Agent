package outbox_test

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zeokeel/super-agent/internal/domain"
	"github.com/zeokeel/super-agent/internal/effects"
	"github.com/zeokeel/super-agent/internal/outbox"
	"github.com/zeokeel/super-agent/internal/store"
)

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{t: time.Date(2026, 9, 9, 0, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func newStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "outbox.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func newQueue(t *testing.T, st *store.Store, clock *fakeClock) *effects.Queue {
	t.Helper()
	q, err := effects.New(st, effects.WithClock(clock.Now))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		q.Drain(ctx)
	})
	return q
}

// sent is a Sender that records what it was asked to deliver and answers with a
// scripted behaviour.
type sent struct {
	mu    sync.Mutex
	calls []outbox.Message
	// behave can override the default OutcomeSent for one message.
	behave func(m outbox.Message) (outbox.SendOutcome, error)
}

func (s *sent) Send(_ context.Context, m outbox.Message) (outbox.SendOutcome, error) {
	s.mu.Lock()
	s.calls = append(s.calls, m)
	s.mu.Unlock()
	if s.behave != nil {
		return s.behave(m)
	}
	return outbox.OutcomeSent, nil
}

func (s *sent) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.calls)
}

func (s *sent) deliveredIDs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.calls))
	for _, m := range s.calls {
		out = append(out, m.ID)
	}
	return out
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func msg(id string) outbox.Message {
	return outbox.Message{
		ID: id, Workspace: "ws_a", Ticket: "t1",
		Source: "dingtalk", Conversation: "dm:zhangsan",
		Thread: "dm:zhangsan", Kind: "reply", Text: "已处理",
	}
}

// A message is recorded before it is sent, and a duplicate business ID is never
// sent twice; the receipt is kept forever.
func TestSendRecordsFirstThenDeliversOnce(t *testing.T) {
	st := newStore(t)
	clock := newFakeClock()
	q := newQueue(t, st, clock)
	s := &sent{}
	o, err := outbox.Open(st, q, s, outbox.WithClock(clock.Now))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	created, err := o.Send(msg("m1"))
	if err != nil || !created {
		t.Fatalf("send: created=%v err=%v", created, err)
	}
	// The receipt exists before delivery has run: that is what "先落库再发" means.
	// The queue is not started yet, so this is not a race.
	if state, ok, err := o.Status("m1"); err != nil || !ok || state != outbox.Pending {
		t.Fatalf("receipt before delivery: state=%q ok=%v err=%v", state, ok, err)
	}
	q.Start()
	if err := o.Sent(ctx); err != nil {
		t.Fatal(err)
	}
	if state, _, _ := o.Status("m1"); state != outbox.Sent {
		t.Fatalf("state %q, want sent", state)
	}
	if s.count() != 1 {
		t.Fatalf("delivered %d times, want 1", s.count())
	}

	// The same business ID again is a no-op even though the receipt is settled.
	created, err = o.Send(msg("m1"))
	if err != nil {
		t.Fatal(err)
	}
	if created {
		t.Fatal("a duplicate ID was recorded as new")
	}
	if err := o.Sent(ctx); err != nil {
		t.Fatal(err)
	}
	if s.count() != 1 {
		t.Fatalf("a duplicate message reached the Source: %d calls", s.count())
	}
	// The receipt is still there after all of that: receipts are permanent.
	if _, ok, err := o.Receipt("m1"); err != nil || !ok {
		t.Fatalf("receipt vanished: ok=%v err=%v", ok, err)
	}
}

// A message queued before a restart is delivered by the next start, exactly once.
func TestPendingReceiptReplaysAfterRestart(t *testing.T) {
	st := newStore(t)
	clock := newFakeClock()

	// The old process recorded the message but crashed before delivery ran.
	oldQueue := newQueue(t, st, clock)
	old, err := outbox.Open(st, oldQueue, nil, outbox.WithClock(clock.Now))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.Send(msg("survivor")); err != nil {
		t.Fatal(err)
	}

	// The next process opens the same file and starts its own delivery.
	newQueue := newQueue(t, st, clock)
	s := &sent{}
	fresh, err := outbox.Open(st, newQueue, s, outbox.WithClock(clock.Now))
	if err != nil {
		t.Fatal(err)
	}
	newQueue.Start()
	if err := fresh.Sent(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := s.deliveredIDs(); len(got) != 1 || got[0] != "survivor" {
		t.Fatalf("delivered %v, want [survivor]", got)
	}
	if state, _, _ := fresh.Status("survivor"); state != outbox.Sent {
		t.Fatalf("state %q, want sent", state)
	}
}

// A permanently unreachable target settles as gone; its receipt still blocks a
// duplicate and it is never retried.
func TestGoneSettlesAndNeverRetries(t *testing.T) {
	st := newStore(t)
	clock := newFakeClock()
	q := newQueue(t, st, clock)
	s := &sent{behave: func(outbox.Message) (outbox.SendOutcome, error) {
		return outbox.OutcomeGone, nil
	}}
	o, err := outbox.Open(st, q, s, outbox.WithClock(clock.Now))
	if err != nil {
		t.Fatal(err)
	}
	q.Start()
	ctx := context.Background()

	if _, err := o.Send(msg("gone1")); err != nil {
		t.Fatal(err)
	}
	if err := o.Sent(ctx); err != nil {
		t.Fatal(err)
	}
	if state, _, _ := o.Status("gone1"); state != outbox.Gone {
		t.Fatalf("state %q, want gone", state)
	}
	if s.count() != 1 {
		t.Fatalf("gone was retried: %d calls", s.count())
	}
	// Receipts are permanent, so the same ID can never be sent again.
	if created, err := o.Send(msg("gone1")); err != nil || created {
		t.Fatalf("duplicate after gone: created=%v err=%v", created, err)
	}
	clock.Advance(time.Hour)
	if err := o.Sent(ctx); err != nil {
		t.Fatal(err)
	}
	if s.count() != 1 {
		t.Fatalf("gone message was retried later: %d calls", s.count())
	}
}

// A transient error is retried with backoff; the receipt carries the attempt count
// and the last error until the send succeeds.
func TestTransientErrorRetriesThenSends(t *testing.T) {
	st := newStore(t)
	clock := newFakeClock()
	q := newQueue(t, st, clock)
	var attempt atomic.Int64
	s := &sent{behave: func(outbox.Message) (outbox.SendOutcome, error) {
		if attempt.Add(1) == 1 {
			return "", errors.New("network blip")
		}
		return outbox.OutcomeSent, nil
	}}
	o, err := outbox.Open(st, q, s, outbox.WithClock(clock.Now))
	if err != nil {
		t.Fatal(err)
	}
	q.Start()
	ctx := context.Background()

	if _, err := o.Send(msg("retry1")); err != nil {
		t.Fatal(err)
	}
	if err := o.Sent(ctx); err != nil {
		t.Fatal(err)
	}
	r, ok, err := o.Receipt("retry1")
	if err != nil || !ok {
		t.Fatalf("receipt: ok=%v err=%v", ok, err)
	}
	if r.State != outbox.Pending || r.Attempts != 1 || r.Error == "" {
		t.Fatalf("after a transient failure: state=%q attempts=%d error=%q", r.State, r.Attempts, r.Error)
	}

	clock.Advance(effects.DefaultRetry[0])
	if err := o.Sent(ctx); err != nil {
		t.Fatal(err)
	}
	if state, _, _ := o.Status("retry1"); state != outbox.Sent {
		t.Fatalf("state %q, want sent after retry", state)
	}
	if s.count() != 2 {
		t.Fatalf("delivered %d times, want 2 (one failure, one success)", s.count())
	}
}

// Messages of one Ticket to one conversation keep their order: a later message
// waits behind an earlier one until that one has failed enough times to be a known
// problem.
func TestSameTicketAndConversationKeepsOrder(t *testing.T) {
	st := newStore(t)
	clock := newFakeClock()
	q := newQueue(t, st, clock)
	s := &sent{behave: func(m outbox.Message) (outbox.SendOutcome, error) {
		if m.ID == "first" {
			return "", errors.New("still failing")
		}
		return outbox.OutcomeSent, nil
	}}
	o, err := outbox.Open(st, q, s, outbox.WithClock(clock.Now))
	if err != nil {
		t.Fatal(err)
	}
	q.Start()
	ctx := context.Background()

	if err := st.Atomic(func() error {
		for _, id := range []string{"first", "second"} {
			if _, err := o.Send(msg(id)); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := o.Sent(ctx); err != nil {
		t.Fatal(err)
	}
	if state, _, _ := o.Status("second"); state != outbox.Pending {
		t.Fatalf("the later message went out ahead of the earlier one: %q", state)
	}
	if got := s.deliveredIDs(); len(got) != 1 || got[0] != "first" {
		t.Fatalf("delivered %v, want only [first] while it is still failing", got)
	}

	// Drive the first message past the failure threshold, then the second may go.
	for i := 0; i < effects.FailuresBeforeError+1; i++ {
		clock.Advance(60 * time.Second)
		if err := o.Sent(ctx); err != nil {
			t.Fatal(err)
		}
		if state, _, _ := o.Status("second"); state == outbox.Sent {
			break
		}
	}
	if state, _, _ := o.Status("second"); state != outbox.Sent {
		t.Fatalf("the second message never went out: %q", state)
	}
	ids := s.deliveredIDs()
	if len(ids) < 2 || ids[len(ids)-1] != "second" {
		t.Fatalf("delivery order %v, want second last", ids)
	}
	// The first message is still pending: being let through did not drop it.
	if state, _, _ := o.Status("first"); state != outbox.Pending {
		t.Fatalf("the failing first message was lost: %q", state)
	}
}

// A different conversation does not have to wait, even for the same Ticket.
func TestDifferentConversationDoesNotBlock(t *testing.T) {
	st := newStore(t)
	clock := newFakeClock()
	q := newQueue(t, st, clock)
	s := &sent{behave: func(m outbox.Message) (outbox.SendOutcome, error) {
		if m.ID == "one" {
			return "", errors.New("still failing")
		}
		return outbox.OutcomeSent, nil
	}}
	o, err := outbox.Open(st, q, s, outbox.WithClock(clock.Now))
	if err != nil {
		t.Fatal(err)
	}
	q.Start()
	ctx := context.Background()

	first := msg("one")
	second := msg("two")
	second.Conversation = "group:ops"
	second.Thread = "group:ops"
	if err := st.Atomic(func() error {
		if _, err := o.Send(first); err != nil {
			return err
		}
		_, err := o.Send(second)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := o.Sent(ctx); err != nil {
		t.Fatal(err)
	}
	if state, _, _ := o.Status("two"); state != outbox.Sent {
		t.Fatalf("a different conversation was held back: %q", state)
	}
}

// Sending is part of the command: a rolled-back command records nothing and sends
// nothing.
func TestSendInsideRolledBackTransactionLeavesNoTrace(t *testing.T) {
	st := newStore(t)
	clock := newFakeClock()
	q := newQueue(t, st, clock)
	s := &sent{}
	o, err := outbox.Open(st, q, s, outbox.WithClock(clock.Now))
	if err != nil {
		t.Fatal(err)
	}
	q.Start()

	boom := errors.New("command failed")
	if err := st.Atomic(func() error {
		if _, err := o.Send(msg("never")); err != nil {
			return err
		}
		return boom
	}); !errors.Is(err, boom) {
		t.Fatalf("rollback returned %v", err)
	}
	if err := o.Sent(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := o.Receipt("never"); ok {
		t.Fatal("a rolled-back command left a receipt")
	}
	if s.count() != 0 {
		t.Fatalf("a rolled-back command delivered %d messages", s.count())
	}
}

// Withdrawing a pending message removes it and cancels its delivery effect.
func TestWithdrawCancelsDelivery(t *testing.T) {
	st := newStore(t)
	clock := newFakeClock()
	q := newQueue(t, st, clock)
	s := &sent{}
	o, err := outbox.Open(st, q, s, outbox.WithClock(clock.Now))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	if _, err := o.Send(msg("draft")); err != nil {
		t.Fatal(err)
	}
	// Withdraw before the queue starts: "withdraw cancels a queued delivery" is
	// about the effect that has not begun yet. Starting the queue first makes the
	// assertion depend on scheduler timing (a delivery may legitimately be in
	// flight and cannot be un-sent), which is exactly the race CI used to hit.
	removed, err := o.Withdraw("draft")
	if err != nil || !removed {
		t.Fatalf("withdraw: removed=%v err=%v", removed, err)
	}
	if err := o.Sent(ctx); err != nil {
		t.Fatal(err)
	}
	if s.count() != 0 {
		t.Fatalf("a withdrawn message was delivered: %v", s.deliveredIDs())
	}
	if _, ok, _ := o.Receipt("draft"); ok {
		t.Fatal("withdrawn receipt still present")
	}
	q.Start()
	if err := o.Sent(ctx); err != nil {
		t.Fatal(err)
	}
	if s.count() != 0 {
		t.Fatalf("a withdrawn message was delivered after the queue started: %v", s.deliveredIDs())
	}
}

// One Ticket's messages to one conversation are delivered in queue order.
func TestQueueOrderIsOldestFirst(t *testing.T) {
	st := newStore(t)
	clock := newFakeClock()
	q := newQueue(t, st, clock)
	s := &sent{}
	o, err := outbox.Open(st, q, s, outbox.WithClock(clock.Now))
	if err != nil {
		t.Fatal(err)
	}
	q.Start()
	ctx := context.Background()

	if err := st.Atomic(func() error {
		for _, id := range []string{"a", "b", "c"} {
			m := msg(id)
			m.Ticket = domain.ID("t_order")
			if _, err := o.Send(m); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := o.Sent(ctx); err != nil {
		t.Fatal(err)
	}
	ids := s.deliveredIDs()
	if len(ids) != 3 || ids[0] != "a" || ids[1] != "b" || ids[2] != "c" {
		t.Fatalf("delivery order %v, want [a b c]", ids)
	}
	waitFor(t, "all receipts settled", func() bool {
		for _, id := range ids {
			if state, _, _ := o.Status(id); state != outbox.Sent {
				return false
			}
		}
		return true
	})
}
