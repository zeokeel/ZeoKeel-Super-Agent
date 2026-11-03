package store

// This file is the read side of the durable half: the queries the engine uses to
// rebuild its in-memory state on start (design §5.8 crash recovery), plus the
// timeline/audit reads the prototype's schema exists for. They are plain typed
// reads and writes; no state machine lives here. The meaning of a row belongs to
// the package that owns it (engine for aggregates, effects for the queue, outbox
// for receipts).

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/zeokeel/super-agent/internal/domain"
)

// WorkspacePolicies returns every stored policy, keyed by workspace. The engine
// owns the Policy type; the store gives the JSON back after a restart.
func (s *Store) WorkspacePolicies() (map[domain.WorkspaceID]string, error) {
	rows, err := s.Query(`SELECT id, policy FROM workspace`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[domain.WorkspaceID]string{}
	for rows.Next() {
		var id, policy string
		if err := rows.Scan(&id, &policy); err != nil {
			return nil, err
		}
		out[domain.WorkspaceID(id)] = policy
	}
	return out, rows.Err()
}

// TriggerRecord is a Trigger and the Disposition written with it, which is how a
// command commits them together.
type TriggerRecord struct {
	Trigger     domain.Trigger
	Disposition domain.Disposition
}

// Triggers returns every Trigger with its Disposition, oldest first. The order
// matters because Load rebuilds a Thread's trigger list as a timeline.
func (s *Store) Triggers() ([]TriggerRecord, error) {
	rows, err := s.Query(`
		SELECT t.json, IFNULL(d.kind,''), IFNULL(d.reason,'[]'), IFNULL(d.ticket_ids,'[]')
		FROM trigger t LEFT JOIN disposition d ON d.trigger_id = t.id
		ORDER BY t.at, t.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TriggerRecord
	for rows.Next() {
		var payload, kind, reason, tickets string
		if err := rows.Scan(&payload, &kind, &reason, &tickets); err != nil {
			return nil, err
		}
		var rec TriggerRecord
		if err := json.Unmarshal([]byte(payload), &rec.Trigger); err != nil {
			return nil, err
		}
		rec.Disposition.TriggerID = rec.Trigger.ID
		if kind != "" {
			rec.Disposition.Kind = domain.DispositionKind(kind)
			if err := json.Unmarshal([]byte(reason), &rec.Disposition.Reason); err != nil {
				return nil, err
			}
			if err := json.Unmarshal([]byte(tickets), &rec.Disposition.TicketIDs); err != nil {
				return nil, err
			}
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

// ThreadTrigger is one row of the Trigger↔Thread inverted index: which Trigger
// appeared in a Thread, when, and whether a person wrote it. The (at, id) pair is
// the keyset cursor a timeline page continues from.
type ThreadTrigger struct {
	Thread string
	ID     domain.ID
	At     time.Time
	Human  bool
}

// ThreadCursor renders the cursor for one timeline row: "<fixed-width at>|<id>".
// The fixed-width time is what makes text comparison equal to time comparison; the
// id is the tie-break so Triggers that share an instant are not skipped.
func ThreadCursor(at time.Time, id domain.ID) string {
	return FormatTime(at) + "|" + string(id)
}

// TriggerThreadPage returns up to limit rows of one Thread's timeline, newest
// first, strictly older than cursor. An empty cursor starts at the newest row.
//
// This is the read path the prototype's trigger_thread table exists for: a page
// continues from an <at>|<id> cursor and rows that share a time are not skipped.
func (s *Store) TriggerThreadPage(thread, cursor string, limit int) ([]ThreadTrigger, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	var (
		rows *sql.Rows
		err  error
	)
	if cursor == "" {
		rows, err = s.Query(`
			SELECT thread, id, at, human FROM trigger_thread
			WHERE thread=? ORDER BY at DESC, id DESC LIMIT ?`, thread, limit)
	} else {
		at, id, ok := splitThreadCursor(cursor)
		if !ok {
			return nil, ErrBadCursor
		}
		rows, err = s.Query(`
			SELECT thread, id, at, human FROM trigger_thread
			WHERE thread=? AND (at < ? OR (at = ? AND id < ?))
			ORDER BY at DESC, id DESC LIMIT ?`, thread, at, at, id, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ThreadTrigger
	for rows.Next() {
		var (
			t     ThreadTrigger
			atRaw string
			human int
		)
		if err := rows.Scan(&t.Thread, &t.ID, &atRaw, &human); err != nil {
			return nil, err
		}
		t.At, _ = ParseTime(atRaw)
		t.Human = human != 0
		out = append(out, t)
	}
	return out, rows.Err()
}

// ThreadTimeline returns the last limit rows of one Thread, oldest first. It is the
// convenience read for a panel pane that renders a whole small timeline.
func (s *Store) ThreadTimeline(thread string, limit int) ([]ThreadTrigger, error) {
	page, err := s.TriggerThreadPage(thread, "", limit)
	if err != nil {
		return nil, err
	}
	for i, j := 0, len(page)-1; i < j; i, j = i+1, j-1 {
		page[i], page[j] = page[j], page[i]
	}
	return page, nil
}

// ErrBadCursor reports a timeline cursor that was not written by ThreadCursor.
var ErrBadCursor = errors.New("store: bad timeline cursor")

// splitThreadCursor parses "<at>|<id>". The fixed-width timestamp contains no
// "|", so the first separator is the boundary.
func splitThreadCursor(cursor string) (at, id string, ok bool) {
	at, id, ok = strings.Cut(cursor, "|")
	if !ok || at == "" || id == "" {
		return "", "", false
	}
	return at, id, true
}

// Decisions returns every stored Decision.
func (s *Store) Decisions() ([]domain.Decision, error) {
	return s.decisionsWhere(`1=1`)
}

// DecisionsByThread returns the Decisions about one Thread. The panel's Eval page
// reads this instead of reversing an in-memory projection, so it still works after
// a restart.
func (s *Store) DecisionsByThread(thread string) ([]domain.Decision, error) {
	return s.decisionsWhere(`thread=?`, thread)
}

func (s *Store) decisionsWhere(where string, args ...any) ([]domain.Decision, error) {
	rows, err := s.Query(`SELECT json, IFNULL(thread,'') FROM decision WHERE `+where+` ORDER BY rowid`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Decision
	for rows.Next() {
		var payload, thread string
		if err := rows.Scan(&payload, &thread); err != nil {
			return nil, err
		}
		var d domain.Decision
		if err := json.Unmarshal([]byte(payload), &d); err != nil {
			return nil, err
		}
		if d.Thread == "" {
			d.Thread = thread
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// Tickets returns every stored Ticket, oldest first. The panel's stream shows
// ended Tickets too, so this is the Load path rather than the open-tickets one.
func (s *Store) Tickets() ([]domain.Ticket, error) {
	rows, err := s.Query(`SELECT json FROM ticket ORDER BY created_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Ticket
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var t domain.Ticket
		if err := json.Unmarshal([]byte(payload), &t); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// ThreadRecord is one Thread's engine bookkeeping. The engine owns what the ids
// mean; the store keeps the row so the fan-out graph survives a restart. Without
// it a restarted process would not know which Tickets a later message on a
// subscribed Thread has to reach as context (invariant §4.4.3).
type ThreadRecord struct {
	Key        string
	Workspace  domain.WorkspaceID
	Source     string
	LastAt     time.Time
	TriggerIDs []domain.ID
	TicketIDs  []domain.ID
}

type threadJSON struct {
	TriggerIDs []domain.ID `json:"triggers"`
	TicketIDs  []domain.ID `json:"tickets"`
}

// PutThread stores or replaces one Thread row.
func (s *Store) PutThread(t ThreadRecord) error {
	payload, err := json.Marshal(threadJSON{TriggerIDs: t.TriggerIDs, TicketIDs: t.TicketIDs})
	if err != nil {
		return err
	}
	var wsID any
	if t.Workspace != "" {
		wsID = string(t.Workspace)
	}
	last := t.LastAt
	if last.IsZero() {
		last = s.clock()
	}
	_, err = s.Exec(`
		INSERT INTO thread(key,workspace_id,source,last_at,json) VALUES(?,?,?,?,?)
		ON CONFLICT(key) DO UPDATE SET workspace_id=excluded.workspace_id,
		  source=excluded.source, last_at=excluded.last_at, json=excluded.json`,
		t.Key, wsID, t.Source, FormatTime(last), string(payload))
	return err
}

// Threads returns every Thread row.
func (s *Store) Threads() ([]ThreadRecord, error) {
	rows, err := s.Query(`SELECT key, IFNULL(workspace_id,''), source, last_at, json FROM thread ORDER BY last_at, key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ThreadRecord
	for rows.Next() {
		var rec ThreadRecord
		var ws, last, payload string
		if err := rows.Scan(&rec.Key, &ws, &rec.Source, &last, &payload); err != nil {
			return nil, err
		}
		rec.Workspace = domain.WorkspaceID(ws)
		rec.LastAt, _ = ParseTime(last)
		var body threadJSON
		if err := json.Unmarshal([]byte(payload), &body); err != nil {
			return nil, err
		}
		rec.TriggerIDs, rec.TicketIDs = body.TriggerIDs, body.TicketIDs
		out = append(out, rec)
	}
	return out, rows.Err()
}

// OutboxRecord is one stored receipt. Receipts are the permanent record of what
// was queued for delivery (design §7.2), so this type has no delete path.
type OutboxRecord struct {
	Key           string
	Workspace     domain.WorkspaceID
	Ticket        domain.ID
	Channel       string
	Conversation  string
	Thread        string
	ReplyTo       string
	Kind          string
	State         string
	Text          string
	CreatedAt     time.Time
	UpdatedAt     time.Time
	DroppedAt     time.Time
	DroppedReason string
}

// OutboxKeys returns every receipt key, whatever its state. Load uses it to
// rebuild the engine's in-memory reply dedupe set: a message that was already
// queued before a restart must not be queued again after it.
func (s *Store) OutboxKeys() ([]string, error) {
	rows, err := s.Query(`SELECT key FROM outbox ORDER BY rowid`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// PendingOutbox returns the receipts still waiting for delivery, oldest first.
func (s *Store) PendingOutbox() ([]OutboxRecord, error) {
	return s.outboxWhere(`state='pending'`)
}

func (s *Store) outboxWhere(where string, args ...any) ([]OutboxRecord, error) {
	rows, err := s.Query(`
		SELECT key, IFNULL(workspace_id,''), IFNULL(ticket_id,''), channel, state, text,
		       created_at, conversation, thread, reply_to, kind, IFNULL(updated_at,''),
		       IFNULL(dropped_at,''), IFNULL(dropped_reason,'')
		FROM outbox WHERE `+where+` ORDER BY rowid`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OutboxRecord
	for rows.Next() {
		var rec OutboxRecord
		var ws, ticket, created, updated, dropped string
		if err := rows.Scan(&rec.Key, &ws, &ticket, &rec.Channel, &rec.State, &rec.Text,
			&created, &rec.Conversation, &rec.Thread, &rec.ReplyTo, &rec.Kind, &updated,
			&dropped, &rec.DroppedReason); err != nil {
			return nil, err
		}
		rec.Workspace = domain.WorkspaceID(ws)
		rec.Ticket = domain.ID(ticket)
		rec.CreatedAt, _ = ParseTime(created)
		rec.UpdatedAt, _ = ParseTime(updated)
		rec.DroppedAt, _ = ParseTime(dropped)
		out = append(out, rec)
	}
	return out, rows.Err()
}

// SettleOutbox moves a pending receipt to a final state. It reports whether a
// pending row was still there: a false result means the receipt was withdrawn
// while the message was in flight, and the caller must not lose the fact.
//
// A receipt that settled as gone also records when and why it can never be
// delivered; that is the drop audit the prototype's outbox.dropped columns carry.
func (s *Store) SettleOutbox(key, state string, at time.Time) (bool, error) {
	var (
		res sql.Result
		err error
	)
	if state == "gone" {
		res, err = s.Exec(`UPDATE outbox SET state=?, updated_at=?, dropped_at=?, dropped_reason=?
			WHERE key=? AND state='pending'`, state, FormatTime(at), FormatTime(at), "gone", key)
	} else {
		res, err = s.Exec(`UPDATE outbox SET state=?, updated_at=? WHERE key=? AND state='pending'`,
			state, FormatTime(at), key)
	}
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// MarkOutboxDropped records that a pending receipt can never be delivered, with
// the reason the Source gave. It reports whether a pending receipt was still
// there; the receipt itself is kept so a replayed business id stays a no-op.
func (s *Store) MarkOutboxDropped(key, reason string, at time.Time) (bool, error) {
	res, err := s.Exec(`UPDATE outbox SET state='gone', updated_at=?, dropped_at=?, dropped_reason=?
		WHERE key=? AND state='pending'`, FormatTime(at), FormatTime(at), reason, key)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}
