// Package outbox is the reliable outbound path: a message is written to SQLite
// before anybody tries to send it, its business ID is the idempotency key, and the
// receipt is kept forever so the same comment can never be posted twice. See
// design §7.2, §4.4 invariant 8 and appendix A ("Outbox 回执幂等").
//
// Delivery runs as a durable effect of kind KindReply, so a crash keeps both the
// receipt and the pending attempt: the next start retries. Messages of one Ticket
// to one conversation are delivered in queue order; an earlier message holds a
// later one back until it either succeeds or has failed enough times to be a
// known problem rather than a transient one.
package outbox

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/zeokeel/super-agent/internal/domain"
	"github.com/zeokeel/super-agent/internal/effects"
	"github.com/zeokeel/super-agent/internal/store"
)

// KindReply is the effect kind every outbound delivery uses. One kind means one
// retry budget and one health entry for delivery, which is what the panel shows.
const KindReply = "reply"

// State is where a receipt stands. It is permanent: sent and gone receipts are
// never deleted, which is what makes a replayed message ID a no-op.
type State string

const (
	// Pending is recorded but not delivered yet.
	Pending State = "pending"
	// Sent is delivered. Kept forever.
	Sent State = "sent"
	// Gone means the target can never receive it (a recalled message, a deleted
	// Issue, a blocked user): retrying cannot help, so it settles instead of
	// retrying forever, and its receipt still blocks a duplicate.
	Gone State = "gone"
)

// SendOutcome is the Sender's verdict on one attempt.
type SendOutcome string

const (
	// OutcomeSent means the Source accepted the message.
	OutcomeSent SendOutcome = "sent"
	// OutcomeGone means the destination is permanently unreachable.
	OutcomeGone SendOutcome = "gone"
)

// Message is one outbound message. ID is the business idempotency key: deriving it
// from the thing being sent (a Ticket plus text, a review id) is what makes a
// retry after a crash safe.
type Message struct {
	ID           string
	Workspace    domain.WorkspaceID
	Ticket       domain.ID // empty for a free-standing notice
	Source       string    // channel source, e.g. "dingtalk", "github"
	Conversation string    // chat/dm/channel id; ordering is per Ticket+conversation
	Thread       string    // Source-side thread to reply into, when it has one
	ReplyTo      string    // message being replied to, when it has one
	Kind         string    // "reply" or "notice"; defaults to reply
	Text         string
}

// Sender delivers one recorded message. Returning an error means "try again
// later" (the queue backs off); returning OutcomeGone means "never again".
type Sender interface {
	Send(ctx context.Context, m Message) (SendOutcome, error)
}

// Receipt is a stored message plus how far it got.
type Receipt struct {
	Message
	State     State
	CreatedAt time.Time
	UpdatedAt time.Time
	Attempts  int    // delivery attempts so far, from the reply effect row
	Error     string // last delivery error
}

// Outbox records and delivers outbound messages. One Outbox serves every Source;
// the Source is chosen per message.
type Outbox struct {
	store   *store.Store
	effects *effects.Queue
	sender  Sender
	now     func() time.Time
	limit   func() int
}

// Option configures an Outbox.
type Option func(*Outbox)

// WithClock replaces the clock, for tests.
func WithClock(now func() time.Time) Option {
	return func(o *Outbox) { o.now = now }
}

// WithLimit bounds how many deliveries run at once; zero holds delivery (for
// example while processing is paused) without marking anything failed.
func WithLimit(limit func() int) Option {
	return func(o *Outbox) { o.limit = limit }
}

// Open attaches an outbox to the store and the effect queue and registers the
// delivery handler. The sender may be nil while wiring is incomplete: messages
// are then recorded and delivery says Later instead of failing.
func Open(st *store.Store, q *effects.Queue, sender Sender, opts ...Option) (*Outbox, error) {
	if st == nil || q == nil {
		return nil, errors.New("outbox: needs a store and an effect queue")
	}
	o := &Outbox{store: st, effects: q, sender: sender, now: time.Now}
	for _, opt := range opts {
		opt(o)
	}
	err := q.Handle(KindReply, func(ctx context.Context, key string, _ []byte) error {
		return o.deliver(ctx, key)
	}, effects.Options{
		// Delivery failures are surfaced by the receipt and the Ticket, so they must
		// not also flip the delivery kind's health to error (design §7.2: the
		// "delivery" kind is the one exception).
		Quiet: true,
		Limit: o.limit,
	})
	if err != nil {
		return nil, err
	}
	return o, nil
}

// Send records the message and enqueues its delivery in one transaction. A crash
// after the commit leaves the receipt and the pending effect together, so nothing
// is lost and a duplicate Send with the same ID is ignored. The bool reports
// whether this call was the one that recorded the message.
func (o *Outbox) Send(m Message) (bool, error) {
	if m.ID == "" {
		return false, errors.New("outbox: a message needs an ID to be idempotent")
	}
	if m.Kind == "" {
		m.Kind = "reply"
	}
	now := o.now().UTC()
	created := false
	err := o.store.Atomic(func() error {
		var (
			ws, ticket any
		)
		if m.Workspace != "" {
			ws = string(m.Workspace)
		}
		if m.Ticket != "" {
			ticket = string(m.Ticket)
		}
		res, err := o.store.Exec(`
			INSERT INTO outbox(key,workspace_id,ticket_id,channel,state,text,created_at,
			                   conversation,thread,reply_to,kind,updated_at)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,?)
			ON CONFLICT(key) DO NOTHING`,
			m.ID, ws, ticket, m.Source, string(Pending), m.Text, now.Format(time.RFC3339Nano),
			m.Conversation, m.Thread, m.ReplyTo, m.Kind, now.Format(time.RFC3339Nano))
		if err != nil {
			return fmt.Errorf("outbox: record %s: %w", m.ID, err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		created = n > 0
		if !created {
			// The business ID was already recorded: the receipt is the idempotency
			// check, so this is a no-op and not an error.
			return nil
		}
		return o.effects.Enqueue(KindReply, m.ID, nil)
	})
	if err != nil {
		return false, err
	}
	return created, nil
}

// Status reports where a message stands, or false when it was never recorded.
func (o *Outbox) Status(id string) (State, bool, error) {
	r, ok, err := o.receipt(id)
	if err != nil || !ok {
		return "", false, err
	}
	return r.State, true, nil
}

// Receipt returns the stored receipt, attempts and last error included.
func (o *Outbox) Receipt(id string) (Receipt, bool, error) { return o.receipt(id) }

// Waiting returns pending receipts, oldest first.
func (o *Outbox) Waiting() ([]Receipt, error) {
	rows, err := o.store.Query(`
		SELECT o.key, IFNULL(o.workspace_id,''), IFNULL(o.ticket_id,''), o.channel,
		       IFNULL(o.conversation,''), IFNULL(o.thread,''), IFNULL(o.reply_to,''),
		       IFNULL(o.kind,'reply'), o.state, o.text, o.created_at, IFNULL(o.updated_at,''),
		       IFNULL(e.attempts,0), IFNULL(e.error,'')
		FROM outbox o LEFT JOIN effect e ON e.kind=? AND e.key=o.key
		WHERE o.state='pending' ORDER BY o.rowid`, KindReply)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Receipt
	for rows.Next() {
		r, err := scanReceipt(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Withdraw removes a message that has not been delivered. The receipt is deleted
// (not settled), which means the same ID could be recorded again later: this is
// the explicit "I did not mean to say that" path, so it is the caller's choice.
func (o *Outbox) Withdraw(id string) (bool, error) {
	removed := false
	err := o.store.Atomic(func() error {
		res, err := o.store.Exec(`DELETE FROM outbox WHERE key=? AND state='pending'`, id)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		removed = n > 0
		if !removed {
			return nil
		}
		return o.effects.Cancel(KindReply, id)
	})
	return removed, err
}

// Sent waits until no outbound delivery is running or ready. It is the
// "everything queued so far has been dealt with" barrier for tests and for
// graceful shutdown.
func (o *Outbox) Sent(ctx context.Context) error { return o.effects.Idle(ctx, KindReply) }

// deliver runs one pending receipt through the Sender. It is an effect handler, so
// it runs after the recording transaction committed and outside any command
// transaction.
func (o *Outbox) deliver(ctx context.Context, id string) error {
	var (
		r  Receipt
		ok bool
	)
	err := o.store.Guarded(func() error {
		var err error
		r, ok, err = o.receipt(id)
		return err
	})
	if err != nil {
		return err
	}
	if !ok || r.State != Pending {
		// Withdrawn or already settled while the effect was queued: nothing to send.
		return nil
	}
	if o.sender == nil {
		return effects.Later("no Source is wired for delivery yet")
	}
	behind, err := o.behind(r)
	if err != nil {
		return err
	}
	if behind {
		return effects.Later("an earlier message of this Ticket to this conversation is still waiting")
	}
	outcome, err := o.sender.Send(ctx, r.Message)
	if err != nil {
		return err // transient: the effect queue backs off and reports on the receipt
	}
	return o.settle(r, outcome)
}

// behind reports whether an earlier pending message of the same Ticket and
// conversation still blocks this one. An earlier message that already failed
// effects.FailuresBeforeError times is let through: at that point it is a known
// problem, and holding the rest of the conversation hostage helps nobody
// (invariant §4.4.8).
func (o *Outbox) behind(r Receipt) (bool, error) {
	if r.Ticket == "" {
		return false, nil
	}
	var n int
	err := o.store.Guarded(func() error {
		return o.store.QueryRow(`
			SELECT COUNT(*) FROM outbox earlier
			LEFT JOIN effect e ON e.kind=? AND e.key=earlier.key
			WHERE earlier.key <> ?
			  AND earlier.state = 'pending'
			  AND earlier.ticket_id = ?
			  AND earlier.channel = ?
			  AND earlier.conversation = ?
			  AND earlier.rowid < (SELECT rowid FROM outbox WHERE key = ?)
			  AND IFNULL(e.attempts, 0) < ?`,
			KindReply, r.ID, string(r.Ticket), r.Source, r.Conversation, r.ID,
			effects.FailuresBeforeError).Scan(&n)
	})
	return n > 0, err
}

// settle records the final state. If the receipt vanished while the message was
// in flight (withdrawn), it is recreated settled rather than lost: the business ID
// has been sent, so it must never be sent again.
func (o *Outbox) settle(r Receipt, outcome SendOutcome) error {
	state := Sent
	if outcome == OutcomeGone {
		state = Gone
	}
	now := o.now().UTC()
	return o.store.Atomic(func() error {
		ok, err := o.store.SettleOutbox(r.ID, string(state), now)
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
		var ws, ticket any
		if r.Workspace != "" {
			ws = string(r.Workspace)
		}
		if r.Ticket != "" {
			ticket = string(r.Ticket)
		}
		_, err = o.store.Exec(`
			INSERT OR IGNORE INTO outbox(key,workspace_id,ticket_id,channel,state,text,created_at,
			                             conversation,thread,reply_to,kind,updated_at)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
			r.ID, ws, ticket, r.Source, string(state), r.Text, now.Format(time.RFC3339Nano),
			r.Conversation, r.Thread, r.ReplyTo, r.Kind, now.Format(time.RFC3339Nano))
		return err
	})
}

type scanner interface {
	Scan(dest ...any) error
}

func (o *Outbox) receipt(id string) (Receipt, bool, error) {
	var (
		r                 Receipt
		ws, ticket        string
		kind, state, text string
		created, updated  string
		attempts          int
		lastErr           string
	)
	err := o.store.QueryRow(`
		SELECT o.key, IFNULL(o.workspace_id,''), IFNULL(o.ticket_id,''), o.channel,
		       IFNULL(o.conversation,''), IFNULL(o.thread,''), IFNULL(o.reply_to,''),
		       IFNULL(o.kind,'reply'), o.state, o.text, o.created_at, IFNULL(o.updated_at,''),
		       IFNULL(e.attempts,0), IFNULL(e.error,'')
		FROM outbox o LEFT JOIN effect e ON e.kind=? AND e.key=o.key
		WHERE o.key=?`, KindReply, id).
		Scan(&r.ID, &ws, &ticket, &r.Source, &r.Conversation, &r.Thread, &r.ReplyTo,
			&kind, &state, &text, &created, &updated, &attempts, &lastErr)
	if errors.Is(err, sql.ErrNoRows) {
		return Receipt{}, false, nil
	}
	if err != nil {
		return Receipt{}, false, err
	}
	r.Workspace = domain.WorkspaceID(ws)
	r.Ticket = domain.ID(ticket)
	r.Kind, r.State, r.Text = kind, State(state), text
	r.Attempts, r.Error = attempts, lastErr
	r.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	r.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	return r, true, nil
}

func scanReceipt(rows scanner) (Receipt, error) {
	var (
		r                Receipt
		ws, ticket       string
		kind, state      string
		text             string
		created, updated string
		attempts         int
		lastErr          string
	)
	err := rows.Scan(&r.ID, &ws, &ticket, &r.Source, &r.Conversation, &r.Thread, &r.ReplyTo,
		&kind, &state, &text, &created, &updated, &attempts, &lastErr)
	if err != nil {
		return Receipt{}, err
	}
	r.Workspace = domain.WorkspaceID(ws)
	r.Ticket = domain.ID(ticket)
	r.Kind, r.State, r.Text = kind, State(state), text
	r.Attempts, r.Error = attempts, lastErr
	r.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	r.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	return r, nil
}
