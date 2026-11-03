// Package store persists the domain in one SQLite file. There is one writer for
// the whole process: a command is one transaction, and side effects run after it
// commits, so a crash between the two is recoverable. See design §7.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	_ "modernc.org/sqlite"

	"github.com/zeokeel/super-agent/internal/domain"
)

// ErrNewerSchema reports a database written by a newer build.
var ErrNewerSchema = errors.New("store: database schema is newer than this build")

// ErrAlreadyRunning reports that another process (or another Store in this
// process) already holds the state directory's write lock. Two writers on one
// SQLite file are the failure this guards against: WAL would let them interleave
// commands that both assume they are the single writer.
var ErrAlreadyRunning = errors.New("store: the state directory is already in use by another process")

// Store is the process's single write path.
type Store struct {
	db   *sql.DB
	path string
	lock *fileLock

	// guard is held for the whole duration of an outermost transaction, so only
	// one command writes at a time. txMu protects tx and depth; it is never held
	// across fn, which is what makes nested Atomic possible.
	guard sync.Mutex
	txMu  sync.Mutex

	// tx is the outermost transaction of the command being executed, and nil
	// between commands. Every statement of the command goes through it, so a
	// command and its side effects are one atom. Statements from other goroutines
	// use db and therefore wait for the transaction instead of accidentally
	// joining it (the failure mode of the old raw-BEGIN implementation).
	tx    *sql.Tx
	owner uint64 // goroutine id of the command that owns tx (valid while tx != nil)
	cond  *sync.Cond
	depth int
	after []func()
	clock func() time.Time
}

// Open opens (or creates) the database at path, takes the state directory's
// write lock and applies migrations.
//
// The lock is per directory, not per file: one state directory holds exactly one
// database (plus logs and receipts), and two processes writing it is never
// correct. A read-only reader does not need the lock: see Inspect.
func Open(path string) (*Store, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("store: create directory: %w", err)
	}
	lock, err := acquireLock(dir)
	if err != nil {
		return nil, err
	}
	// _txlock=immediate makes database/sql's BEGIN an IMMEDIATE one, so a write
	// command takes SQLite's write lock when it starts instead of upgrading half
	// way through and finding the file busy.
	dsn := "file:" + path + "?_txlock=immediate&_pragma=journal_mode(WAL)" +
		"&_pragma=synchronous(FULL)&_pragma=foreign_keys(ON)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		lock.release()
		return nil, fmt.Errorf("store: open: %w", err)
	}
	// One connection: a single writer serializes every command, which is what the
	// engine's invariants assume.
	db.SetMaxOpenConns(1)
	s := &Store{db: db, path: path, lock: lock, clock: time.Now}
	s.cond = sync.NewCond(&s.txMu)
	if err := s.migrate(); err != nil {
		db.Close()
		lock.release()
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		db.Close()
		lock.release()
		return nil, fmt.Errorf("store: chmod: %w", err)
	}
	return s, nil
}

// Close closes the database and releases the state directory lock.
func (s *Store) Close() error {
	err := s.db.Close()
	s.lock.release()
	return err
}

// Path returns the database file path this store was opened with.
func (s *Store) Path() string { return s.path }

// DB exposes the handle for read-only queries in tests and views.
func (s *Store) DB() *sql.DB { return s.db }

// executor returns the handle statements must run on: the command's transaction
// while one is open, the pool otherwise. Taking txMu on every statement is cheap
// and is what keeps "one command = one transaction" true no matter who calls
// Exec.
func (s *Store) executor() interface {
	Exec(query string, args ...any) (sql.Result, error)
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
} {
	s.txMu.Lock()
	if s.tx != nil {
		if s.owner == goroutineID() {
			tx := s.tx
			s.txMu.Unlock()
			return tx
		}
		// Another goroutine's command owns the transaction. Wait for it to finish,
		// then read committed state: a background reader must never join it.
		for s.tx != nil {
			s.cond.Wait()
		}
	}
	s.txMu.Unlock()
	return s.db
}

// Exec runs one statement. Inside Atomic it joins the caller's transaction.
// Call it on the command goroutine only; a background writer must use Guarded.
func (s *Store) Exec(query string, args ...any) (sql.Result, error) {
	return s.executor().Exec(query, args...)
}

// Query runs one read. It has the same in-transaction property as Exec.
func (s *Store) Query(query string, args ...any) (*sql.Rows, error) {
	return s.executor().Query(query, args...)
}

// QueryRow runs one read of at most one row.
func (s *Store) QueryRow(query string, args ...any) *sql.Row {
	return s.executor().QueryRow(query, args...)
}

// Guarded runs fn with the process's write guard held and no command
// transaction open, so a background worker's statements can never race a
// command: Atomic holds the guard for the whole transaction, so a guarded write
// waits for the command to commit.
//
// fn must not begin its own transaction: acquire the guard, use Exec/Query
// directly, release. Do not call Guarded from inside Atomic: the command
// goroutine already holds the guard and would deadlock.
func (s *Store) Guarded(fn func() error) error {
	s.guard.Lock()
	defer s.guard.Unlock()
	return fn()
}

// InTransaction reports whether a command transaction is open. It is for
// assertions and diagnostics, not for deciding how to take the guard.
func (s *Store) InTransaction() bool {
	s.txMu.Lock()
	defer s.txMu.Unlock()
	return s.tx != nil
}

// FormatTime renders a timestamp as fixed-width UTC. Stored text then compares
// chronologically, which is what lets the effect queue ask `due <= now` in SQL
// and the trigger/thread/ticket timelines sort in one indexed read.
// RFC3339Nano is not fixed width (it drops trailing zeros), so it must not be
// used for a column that is compared or ordered in SQL.
func FormatTime(t time.Time) string { return t.UTC().Format(timeLayout) }

// ParseTime parses a timestamp written by FormatTime. It also accepts
// time.RFC3339Nano, because files written by older builds used it for some
// columns; the v3 migration normalizes them, and the tolerant read keeps an
// interrupted upgrade from turning into data loss.
func ParseTime(v string) (time.Time, error) {
	if t, err := time.Parse(timeLayout, v); err == nil {
		return t, nil
	}
	return time.Parse(time.RFC3339Nano, v)
}

const timeLayout = "2006-01-02T15:04:05.000000000Z"

func (s *Store) migrate() error {
	// The version lives in kv, which the first migration creates, so ask the
	// catalog before reading it.
	var haveKV int
	if err := s.db.QueryRow(
		`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='kv'`).Scan(&haveKV); err != nil {
		return fmt.Errorf("store: inspect schema: %w", err)
	}
	if haveKV == 0 {
		// A fresh file. Apply every migration, not just the first: an install of a
		// later build must not be stamped with a version it does not have.
		return s.applyMigrations(0)
	}
	var current int
	v, err := s.kvGet("schema")
	switch {
	case errors.Is(err, sql.ErrNoRows):
		current = 0
	case err != nil:
		return err
	default:
		current, err = strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("store: bad schema version %q: %w", v, err)
		}
	}
	if current > SchemaVersion {
		return fmt.Errorf("%w: file has %d, build supports %d", ErrNewerSchema, current, SchemaVersion)
	}
	return s.applyMigrations(current)
}

// applyMigrations upgrades the file from version from to SchemaVersion in one
// transaction. A failed migration leaves the file at its old version, so the next
// open retries instead of meeting half-applied tables.
//
// It runs through Atomic, so migration statements use the same transaction
// machinery a command does (and the same single connection) instead of a second,
// parallel implementation of BEGIN/COMMIT.
func (s *Store) applyMigrations(from int) error {
	if from >= SchemaVersion {
		return nil
	}
	return s.Atomic(func() error {
		for v := from; v < SchemaVersion; v++ {
			m := migrations[v]
			for _, stmt := range m.sql {
				if _, err := s.Exec(stmt); err != nil {
					return fmt.Errorf("store: migrate %d -> %d (%s): %w", v, v+1, m.name, err)
				}
			}
			if m.apply != nil {
				if err := m.apply(s); err != nil {
					return fmt.Errorf("store: migrate %d -> %d (%s): %w", v, v+1, m.name, err)
				}
			}
		}
		return s.kvSet("schema", strconv.Itoa(SchemaVersion))
	})
}

// Atomic runs fn in one transaction. Side effects registered with AfterCommit run
// only when the outermost transaction commits, and are dropped on rollback.
//
// Nesting is allowed: an inner Atomic on the command goroutine becomes a
// SAVEPOINT. An Atomic from any other goroutine waits for the running command and
// then starts its own transaction, so two goroutines (a Source poll loop and a
// panel dispatch, say) can never interleave statements inside one transaction.
func (s *Store) Atomic(fn func() error) error {
	id := goroutineID()
	s.txMu.Lock()
	nested := s.tx != nil && s.owner == id
	if nested {
		s.depth++
		sp := s.depth
		s.txMu.Unlock()
		if _, err := s.Exec(fmt.Sprintf("SAVEPOINT sp%d", sp)); err != nil {
			s.txMu.Lock()
			s.depth--
			s.txMu.Unlock()
			return fmt.Errorf("store: savepoint: %w", err)
		}
		err := fn()
		if err != nil {
			s.Exec(fmt.Sprintf("ROLLBACK TO sp%d", sp))
			s.Exec(fmt.Sprintf("RELEASE sp%d", sp))
		} else if _, rerr := s.Exec(fmt.Sprintf("RELEASE sp%d", sp)); rerr != nil {
			err = fmt.Errorf("store: release: %w", rerr)
		}
		s.txMu.Lock()
		s.depth--
		s.txMu.Unlock()
		return err
	}
	s.txMu.Unlock()

	// Outermost command: the guard makes the wait explicit for other writers, and
	// BeginTx checks out the single connection for the whole command.
	s.guard.Lock()
	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		s.guard.Unlock()
		return fmt.Errorf("store: begin: %w", err)
	}
	s.txMu.Lock()
	s.tx = tx
	s.owner = id
	s.depth = 1
	s.txMu.Unlock()

	err = fn()
	if err != nil {
		s.rollback()
		return err
	}
	if cerr := tx.Commit(); cerr != nil {
		s.rollback()
		return fmt.Errorf("store: commit: %w", cerr)
	}
	s.txMu.Lock()
	s.tx = nil
	s.owner = 0
	s.depth = 0
	callbacks := s.after
	s.after = nil
	s.cond.Broadcast()
	s.txMu.Unlock()
	s.guard.Unlock()
	// Run the callbacks outside the transaction and outside the guard, so a
	// failing side effect cannot roll back a committed command.
	for _, cb := range callbacks {
		cb()
	}
	return nil
}

func (s *Store) currentTx() *sql.Tx {
	s.txMu.Lock()
	defer s.txMu.Unlock()
	return s.tx
}

// rollback aborts the outermost transaction, drops its deferred side effects and
// releases the write guard. It is only called by the command's own goroutine.
func (s *Store) rollback() {
	if tx := s.currentTx(); tx != nil {
		tx.Rollback()
	}
	s.txMu.Lock()
	s.tx = nil
	s.owner = 0
	s.depth = 0
	s.after = nil
	s.cond.Broadcast()
	s.txMu.Unlock()
	s.guard.Unlock()
}

// AfterCommit registers a side effect to run once the current command commits.
// Call it from inside Atomic, on the command goroutine: the store's lock is held
// for the whole transaction, so this method must not take it again.
func (s *Store) AfterCommit(fn func()) {
	s.txMu.Lock()
	pending := s.tx != nil
	if pending {
		s.after = append(s.after, fn)
	}
	s.txMu.Unlock()
	if !pending {
		// Outside a transaction there is nothing to wait for.
		fn()
	}
}

// PutWorkspace stores or replaces a workspace.
func (s *Store) PutWorkspace(w domain.Workspace) error {
	brief, err := json.Marshal(w.Brief)
	if err != nil {
		return err
	}
	now := FormatTime(s.clock())
	_, err = s.Exec(`
		INSERT INTO workspace(id,name,root,remote,kind,brief,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET
		  name=excluded.name, root=excluded.root, remote=excluded.remote,
		  kind=excluded.kind, brief=excluded.brief, updated_at=excluded.updated_at`,
		string(w.ID), w.Name, w.Root, w.Remote, string(w.Kind), string(brief), now, now)
	return err
}

// DeleteWorkspace removes one workspace row (and its embedded policy). The
// caller is responsible for the higher-level invariant (no Ticket refers to it).
func (s *Store) DeleteWorkspace(id domain.WorkspaceID) error {
	_, err := s.Exec(`DELETE FROM workspace WHERE id=?`, string(id))
	return err
}

// Workspaces returns every workspace ordered by id.
func (s *Store) Workspaces() ([]domain.Workspace, error) {
	rows, err := s.Query(`SELECT id,name,root,remote,kind,brief FROM workspace ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Workspace
	for rows.Next() {
		var w domain.Workspace
		var brief string
		if err := rows.Scan(&w.ID, &w.Name, &w.Root, &w.Remote, &w.Kind, &brief); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(brief), &w.Brief); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// PutTrigger stores a Trigger and its disposition in one command. The trigger's
// threads are also written to the trigger_thread inverted index, which is what
// the panel's timeline pages read; see TriggerThreadPage.
func (s *Store) PutTrigger(t domain.Trigger, d domain.Disposition) error {
	payload, err := json.Marshal(t)
	if err != nil {
		return err
	}
	reason, err := json.Marshal(d.Reason)
	if err != nil {
		return err
	}
	tickets, err := json.Marshal(d.TicketIDs)
	if err != nil {
		return err
	}
	human := 0
	if t.Human {
		human = 1
	}
	var wsID any
	if id, ok := t.Scope.WorkspaceIDOf(); ok {
		wsID = string(id)
	}
	at := FormatTime(t.At)
	_, err = s.Exec(`
		INSERT INTO trigger(id,scope,workspace_id,default_workspace,source,at,human,json)
		VALUES(?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO NOTHING`,
		string(t.ID), string(t.Scope), wsID, nullString(string(t.DefaultWorkspace)),
		t.Source, at, human, string(payload))
	if err != nil {
		return err
	}
	for _, ref := range t.Threads {
		thread := string(domain.ThreadKey(t.Scope, ref.Source, ref.ID))
		if _, err := s.Exec(`
			INSERT INTO trigger_thread(thread,id,at,human) VALUES(?,?,?,?)
			ON CONFLICT(thread,id) DO NOTHING`, thread, string(t.ID), at, human); err != nil {
			return err
		}
	}
	_, err = s.Exec(`
		INSERT INTO disposition(trigger_id,kind,reason,ticket_ids) VALUES(?,?,?,?)
		ON CONFLICT(trigger_id) DO UPDATE SET kind=excluded.kind,
		  reason=excluded.reason, ticket_ids=excluded.ticket_ids`,
		string(t.ID), string(d.Kind), string(reason), string(tickets))
	return err
}

// Trigger returns a stored Trigger.
func (s *Store) Trigger(id domain.ID) (domain.Trigger, bool, error) {
	var payload string
	err := s.QueryRow(`SELECT json FROM trigger WHERE id=?`, string(id)).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Trigger{}, false, nil
	}
	if err != nil {
		return domain.Trigger{}, false, err
	}
	var t domain.Trigger
	if err := json.Unmarshal([]byte(payload), &t); err != nil {
		return domain.Trigger{}, false, err
	}
	return t, true, nil
}

// PutTicket stores a Ticket. The stage column is a query cache derived from the
// domain's StageOf; it is never read as the truth.
func (s *Store) PutTicket(t domain.Ticket, now time.Time) error {
	payload, err := json.Marshal(t)
	if err != nil {
		return err
	}
	var ended any
	if t.Ended != nil {
		ended = FormatTime(t.Ended.At)
	}
	_, err = s.Exec(`
		INSERT INTO ticket(id,workspace_id,stage,ended_at,created_at,json)
		VALUES(?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET stage=excluded.stage,
		  ended_at=excluded.ended_at, json=excluded.json`,
		string(t.ID), string(t.Workspace), string(domain.StageOf(&t, now)),
		ended, FormatTime(t.CreatedAt), string(payload))
	return err
}

// Ticket returns a stored Ticket.
func (s *Store) Ticket(id domain.ID) (domain.Ticket, bool, error) {
	var payload string
	err := s.QueryRow(`SELECT json FROM ticket WHERE id=?`, string(id)).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Ticket{}, false, nil
	}
	if err != nil {
		return domain.Ticket{}, false, err
	}
	var t domain.Ticket
	if err := json.Unmarshal([]byte(payload), &t); err != nil {
		return domain.Ticket{}, false, err
	}
	return t, true, nil
}

// PutDecision stores a Decision and the Thread it was about.
func (s *Store) PutDecision(d domain.Decision) error {
	payload, err := json.Marshal(d)
	if err != nil {
		return err
	}
	_, err = s.Exec(`INSERT INTO decision(id,workspace_id,kind,thread,json) VALUES(?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET kind=excluded.kind, thread=excluded.thread, json=excluded.json`,
		string(d.ID), nullString(string(d.Workspace)), string(d.Kind), nullString(d.Thread), string(payload))
	return err
}

// PutWorkspacePolicy stores one workspace's policy as opaque JSON. The engine
// owns the type; the store only has to give it back after a restart.
func (s *Store) PutWorkspacePolicy(ws domain.WorkspaceID, policyJSON string) error {
	_, err := s.Exec(`UPDATE workspace SET policy=? WHERE id=?`, policyJSON, string(ws))
	return err
}

// kvGet returns a value, or sql.ErrNoRows.
func (s *Store) kvGet(k string) (string, error) {
	var v string
	err := s.QueryRow(`SELECT v FROM kv WHERE k=?`, k).Scan(&v)
	return v, err
}

func (s *Store) kvSet(k, v string) error {
	_, err := s.Exec(`INSERT INTO kv(k,v) VALUES(?,?) ON CONFLICT(k) DO UPDATE SET v=excluded.v`, k, v)
	return err
}

// Get reads a key, reporting whether it exists. It is the store's small state
// channel: schema version, Source cursors, the prompt revision, the Router
// session id. Wave-2 status and Source cursor persistence use it.
func (s *Store) Get(k string) (string, bool, error) {
	v, err := s.kvGet(k)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return v, err == nil, err
}

// Set writes a key.
func (s *Store) Set(k, v string) error { return s.kvSet(k, v) }

func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}
