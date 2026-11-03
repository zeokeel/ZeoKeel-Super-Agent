package store_test

// verifier-A: independent falsification tests for the Wave-1 "nit-core" claims
// (schema v3, fixed-width time, flock/Inspect, nested Atomic, timeline cursor).
// These are additive verify_* files: they do not modify any production file and
// they build their fixtures from the OLD release layout, not from the current
// migrations table, so a bug in the migration cannot also define its own test.

import (
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/zeokeel/super-agent/internal/domain"
	"github.com/zeokeel/super-agent/internal/store"
)

// -----------------------------------------------------------------------------
// Fixture DDL: copied from the released v1/v2 layout (git HEAD at the audit
// baseline), NOT from internal/store/schema.go. An M4-era file additionally
// carried the schedule/goal tables created by the removed EnsureV3.
// -----------------------------------------------------------------------------

const fixtureV1 = `
CREATE TABLE workspace(
  id TEXT PRIMARY KEY, name TEXT NOT NULL, root TEXT NOT NULL,
  remote TEXT NOT NULL DEFAULT '', kind TEXT NOT NULL, brief TEXT NOT NULL,
  created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
CREATE TABLE trigger(
  id TEXT PRIMARY KEY, scope TEXT NOT NULL, workspace_id TEXT,
  default_workspace TEXT, source TEXT NOT NULL, at TEXT NOT NULL,
  human INTEGER NOT NULL, json TEXT NOT NULL
);
CREATE INDEX trigger_by_scope_at ON trigger(scope, at DESC);
CREATE INDEX trigger_by_ws_at    ON trigger(workspace_id, at DESC);
CREATE TABLE disposition(
  trigger_id TEXT PRIMARY KEY REFERENCES trigger(id), kind TEXT NOT NULL,
  reason TEXT NOT NULL, ticket_ids TEXT NOT NULL
);
CREATE TABLE thread(
  key TEXT PRIMARY KEY, workspace_id TEXT, source TEXT NOT NULL,
  last_at TEXT NOT NULL, json TEXT NOT NULL
);
CREATE INDEX thread_by_ws_last ON thread(workspace_id, last_at DESC);
CREATE TABLE ticket(
  id TEXT PRIMARY KEY, workspace_id TEXT NOT NULL, stage TEXT NOT NULL,
  ended_at TEXT, created_at TEXT NOT NULL, json TEXT NOT NULL
);
CREATE INDEX ticket_open ON ticket(workspace_id, created_at DESC) WHERE ended_at IS NULL;
CREATE TABLE decision(
  id TEXT PRIMARY KEY, workspace_id TEXT, kind TEXT NOT NULL, json TEXT NOT NULL
);
CREATE TABLE effect(
  kind TEXT NOT NULL, key TEXT NOT NULL, seq INTEGER NOT NULL,
  generation INTEGER NOT NULL DEFAULT 0, attempts INTEGER NOT NULL DEFAULT 0,
  due TEXT NOT NULL, error TEXT NOT NULL DEFAULT '', payload TEXT NOT NULL DEFAULT '',
  PRIMARY KEY(kind, key)
);
CREATE TABLE outbox(
  key TEXT PRIMARY KEY, workspace_id TEXT, ticket_id TEXT, channel TEXT NOT NULL,
  state TEXT NOT NULL, text TEXT NOT NULL, created_at TEXT NOT NULL
);
CREATE INDEX outbox_pending ON outbox(created_at) WHERE state = 'pending';
CREATE TABLE session_event(
  session_id TEXT NOT NULL, seq INTEGER NOT NULL, at TEXT NOT NULL,
  kind TEXT NOT NULL, payload TEXT NOT NULL, PRIMARY KEY(session_id, seq)
);
CREATE TABLE health(
  entry TEXT PRIMARY KEY, state TEXT NOT NULL, detail TEXT NOT NULL DEFAULT '', at TEXT NOT NULL
);
CREATE TABLE kv(k TEXT PRIMARY KEY, v TEXT NOT NULL);
`

const fixtureV2Alter = `
ALTER TABLE workspace ADD COLUMN policy TEXT NOT NULL DEFAULT '{}';
ALTER TABLE effect ADD COLUMN since TEXT NOT NULL DEFAULT '';
CREATE INDEX effect_by_seq ON effect(kind, seq);
CREATE INDEX effect_by_due ON effect(due);
ALTER TABLE outbox ADD COLUMN conversation TEXT NOT NULL DEFAULT '';
ALTER TABLE outbox ADD COLUMN thread       TEXT NOT NULL DEFAULT '';
ALTER TABLE outbox ADD COLUMN reply_to     TEXT NOT NULL DEFAULT '';
ALTER TABLE outbox ADD COLUMN kind         TEXT NOT NULL DEFAULT 'reply';
ALTER TABLE outbox ADD COLUMN updated_at   TEXT NOT NULL DEFAULT '';
CREATE INDEX outbox_order ON outbox(ticket_id, channel, conversation, created_at)
  WHERE state = 'pending';
CREATE INDEX ticket_by_created ON ticket(created_at DESC);
CREATE INDEX decision_by_kind  ON decision(kind);
`

// fixtureM4Tables is what the removed EnsureV3 created (schedule without the
// later `task` column, and goal).
const fixtureM4Tables = `
CREATE TABLE IF NOT EXISTS schedule(
  id TEXT PRIMARY KEY, workspace_id TEXT, spec TEXT NOT NULL,
  enabled INTEGER NOT NULL DEFAULT 1, created_at TEXT NOT NULL, updated_at TEXT NOT NULL,
  last_fired_at TEXT, next_at TEXT, running_fire TEXT NOT NULL DEFAULT '',
  last_status TEXT NOT NULL DEFAULT '', last_skip_reason TEXT NOT NULL DEFAULT '',
  last_skip_at TEXT
);
CREATE INDEX IF NOT EXISTS schedule_due ON schedule(enabled, next_at);
CREATE TABLE IF NOT EXISTS schedule_fire(
  id TEXT PRIMARY KEY, schedule_id TEXT NOT NULL, planned_at TEXT NOT NULL,
  fired_at TEXT NOT NULL, status TEXT NOT NULL, reason TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS goal(
  id TEXT PRIMARY KEY, workspace_id TEXT NOT NULL, state TEXT NOT NULL,
  revision INTEGER NOT NULL, objective TEXT NOT NULL, max_rounds INTEGER NOT NULL,
  block_threshold INTEGER NOT NULL, blocked_reason TEXT NOT NULL DEFAULT '',
  consecutive_blocked INTEGER NOT NULL DEFAULT 0, rounds_count INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL, updated_at TEXT NOT NULL, json TEXT NOT NULL
);
`

func rawOpen(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatalf("raw sqlite open: %v", err)
	}
	db.SetMaxOpenConns(1)
	return db
}

func execAll(t *testing.T, db *sql.DB, script string) {
	t.Helper()
	for _, stmt := range strings.Split(script, ";") {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" || strings.HasPrefix(stmt, "--") {
			continue
		}
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("fixture exec %q: %v", firstLine(stmt), err)
		}
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func fixtureTrigger(id, thread string, at time.Time) domain.Trigger {
	return domain.Trigger{
		ID:      domain.ID(id),
		Scope:   domain.Global,
		Source:  "github",
		Threads: []domain.ThreadRef{{Source: "github", ID: thread}},
		Actor:   domain.Actor{ID: "alice"},
		Human:   true,
		Text:    "hello " + id,
		At:      at,
		Channel: domain.ChannelRef{Source: "github", Thread: thread},
	}
}

func insertOldTrigger(t *testing.T, db *sql.DB, tr domain.Trigger, atText string) {
	t.Helper()
	payload, err := json.Marshal(tr)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO trigger(id,scope,workspace_id,default_workspace,source,at,human,json)
		VALUES(?,?,NULL,NULL,?,?,1,?)`, string(tr.ID), string(tr.Scope), tr.Source, atText, string(payload)); err != nil {
		t.Fatalf("insert old trigger: %v", err)
	}
}

var fixedWidth = regexp.MustCompile(`^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d\.\d{9}Z$`)

// TestVerifyV2FileUpgradesFromRealOldLayout builds a v1 file with the OLD
// writer's RFC3339Nano values, then upgrades through store.Open and checks every
// claim the handoff makes: version 3, fixed-width rewrite, trigger_thread
// backfill, outbox dropped backfill, new tables/columns, marker removal.
func TestVerifyV2FileUpgradesFromRealOldLayout(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v1.db")
	db := rawOpen(t, path)
	execAll(t, db, fixtureV1)
	if _, err := db.Exec(`INSERT INTO kv(k,v) VALUES('schema','1')`); err != nil {
		t.Fatal(err)
	}

	threadID := "gh:acme/webapp#12"
	thread := domain.ThreadKey(domain.Global, "github", threadID)
	// Two Triggers in the same second, 50 ms apart, written the old way
	// (RFC3339Nano drops trailing zeros: ".5Z" and ".55Z" sort the WRONG way).
	at1 := time.Date(2026, 11, 3, 10, 0, 0, 500_000_000, time.UTC)
	at2 := time.Date(2026, 11, 3, 10, 0, 0, 550_000_000, time.UTC)
	insertOldTrigger(t, db, fixtureTrigger("tr-1", threadID, at1), "2026-11-03T10:00:00.5Z")
	insertOldTrigger(t, db, fixtureTrigger("tr-2", threadID, at2), "2026-11-03T10:00:00.55Z")
	// A thread whose last_at is also old-format.
	thJSON, _ := json.Marshal(map[string]any{"triggers": []string{"tr-1", "tr-2"}, "tickets": []string{}})
	if _, err := db.Exec(`INSERT INTO thread(key,workspace_id,source,last_at,json) VALUES(?,NULL,'github',?,?)`,
		thread, "2026-11-03T10:00:00.5Z", string(thJSON)); err != nil {
		t.Fatal(err)
	}
	// A dropped (gone) receipt written before dropped_at/dropped_reason existed,
	// and a pending one that must NOT be marked dropped.
	if _, err := db.Exec(`INSERT INTO outbox(key,workspace_id,ticket_id,channel,state,text,created_at)
		VALUES('gone-1',NULL,NULL,'github','gone','x','2026-11-03T10:00:00.5Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO outbox(key,workspace_id,ticket_id,channel,state,text,created_at)
		VALUES('pending-1',NULL,NULL,'github','pending','y','2026-11-03T10:00:00.5Z')`); err != nil {
		t.Fatal(err)
	}
	// A ticket with an old-format created_at to normalize.
	if _, err := db.Exec(`INSERT INTO ticket(id,workspace_id,stage,ended_at,created_at,json)
		VALUES('tk-1','ws_a','working',NULL,'2026-11-03T10:00:00.5Z','{}')`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	st, err := store.Open(path)
	if err != nil {
		t.Fatalf("Open(v1 file): %v", err)
	}
	defer st.Close()

	if store.SchemaVersion != 3 {
		t.Fatalf("SchemaVersion = %d, want 3", store.SchemaVersion)
	}
	v, ok, err := st.Get("schema")
	if err != nil || !ok || v != "3" {
		t.Fatalf("schema stamp = %q ok=%v err=%v, want 3", v, ok, err)
	}
	if _, ok, _ := st.Get("schema.schedule_goal"); ok {
		t.Fatal("the M4 marker survived the v3 upgrade: two version truths remain")
	}

	// Fixed-width rewrite + text order == time order (the whole point of v3).
	rows, err := st.Query(`SELECT at FROM trigger ORDER BY at`)
	if err != nil {
		t.Fatal(err)
	}
	var ats []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		ats = append(ats, s)
	}
	rows.Close()
	if len(ats) != 2 {
		t.Fatalf("trigger ats = %v", ats)
	}
	for _, s := range ats {
		if !fixedWidth.MatchString(s) {
			t.Fatalf("trigger.at %q is not fixed-width UTC", s)
		}
	}
	if ats[0] != "2026-11-03T10:00:00.500000000Z" || ats[1] != "2026-11-03T10:00:00.550000000Z" {
		t.Fatalf("text order is not time order after upgrade: %v", ats)
	}
	// thread.last_at and ticket.created_at normalized too.
	var lastAt, created string
	if err := st.QueryRow(`SELECT last_at FROM thread WHERE key=?`, thread).Scan(&lastAt); err != nil {
		t.Fatal(err)
	}
	if err := st.QueryRow(`SELECT created_at FROM ticket WHERE id='tk-1'`).Scan(&created); err != nil {
		t.Fatal(err)
	}
	if !fixedWidth.MatchString(lastAt) || !fixedWidth.MatchString(created) {
		t.Fatalf("last_at=%q created_at=%q not fixed-width", lastAt, created)
	}

	// trigger_thread backfilled from the Trigger JSON, oldest first.
	tl, err := st.ThreadTimeline(thread, 10)
	if err != nil {
		t.Fatalf("ThreadTimeline: %v", err)
	}
	if len(tl) != 2 || string(tl[0].ID) != "tr-1" || string(tl[1].ID) != "tr-2" {
		t.Fatalf("trigger_thread backfill = %+v", tl)
	}

	// dropped backfill: gone gets a time + reason, pending stays empty.
	var droppedAt, reason string
	if err := st.QueryRow(`SELECT dropped_at, dropped_reason FROM outbox WHERE key='gone-1'`).Scan(&droppedAt, &reason); err != nil {
		t.Fatal(err)
	}
	if droppedAt == "" || reason == "" {
		t.Fatalf("gone receipt not backfilled: at=%q reason=%q", droppedAt, reason)
	}
	if err := st.QueryRow(`SELECT dropped_at FROM outbox WHERE key='pending-1'`).Scan(&droppedAt); err != nil {
		t.Fatal(err)
	}
	if droppedAt != "" {
		t.Fatalf("pending receipt was marked dropped: %q", droppedAt)
	}

	// v3 tables/columns really exist.
	for _, table := range []string{"schedule", "schedule_fire", "goal", "trigger_thread"} {
		var n int
		if err := st.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Fatalf("table %q missing after upgrade", table)
		}
	}
	for _, col := range []struct{ table, column string }{
		{"decision", "thread"}, {"schedule", "task"},
		{"outbox", "dropped_at"}, {"outbox", "dropped_reason"},
	} {
		var n int
		if err := st.QueryRow(`SELECT count(*) FROM pragma_table_info(?) WHERE name=?`, col.table, col.column).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Fatalf("column %s.%s missing after upgrade", col.table, col.column)
		}
	}
}

// TestVerifyM4EraFileUpgrades is the regression the old comments predicted:
// a file the M4 builds touched already has schedule/goal tables AND the side
// marker. Folding v3 into the ordered list must not fail with "table exists" nor
// lose the existing schedule row.
func TestVerifyM4EraFileUpgrades(t *testing.T) {
	path := filepath.Join(t.TempDir(), "m4.db")
	db := rawOpen(t, path)
	execAll(t, db, fixtureV1)
	execAll(t, db, fixtureV2Alter)
	execAll(t, db, fixtureM4Tables)
	if _, err := db.Exec(`INSERT INTO kv(k,v) VALUES('schema','2')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO kv(k,v) VALUES('schema.schedule_goal','3')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO schedule(id,workspace_id,spec,enabled,created_at,updated_at,last_status)
		VALUES('sch-1','ws_a','{"kind":"daily","hour":9,"minute":30}',1,'2026-11-01T00:00:00.000000000Z','2026-11-01T00:00:00.000000000Z','ready')`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	st, err := store.Open(path)
	if err != nil {
		t.Fatalf("Open(M4-era file): %v (this is the 'table already exists' regression)", err)
	}
	defer st.Close()

	var spec, status string
	if err := st.QueryRow(`SELECT spec, last_status FROM schedule WHERE id='sch-1'`).Scan(&spec, &status); err != nil {
		t.Fatalf("the existing schedule row was lost: %v", err)
	}
	if !strings.Contains(spec, "daily") || status != "ready" {
		t.Fatalf("schedule row changed: spec=%q status=%q", spec, status)
	}
	var taskCol int
	if err := st.QueryRow(`SELECT count(*) FROM pragma_table_info('schedule') WHERE name='task'`).Scan(&taskCol); err != nil {
		t.Fatal(err)
	}
	if taskCol != 1 {
		t.Fatal("schedule.task was not added to an M4-era table")
	}
	if _, ok, _ := st.Get("schema.schedule_goal"); ok {
		t.Fatal("the side marker survived; v3 must be the single version truth")
	}
}

func TestVerifyNewerSchemaRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "future.db")
	db := rawOpen(t, path)
	execAll(t, db, fixtureV1)
	if _, err := db.Exec(`INSERT INTO kv(k,v) VALUES('schema','4')`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if _, err := store.Open(path); !errors.Is(err, store.ErrNewerSchema) {
		t.Fatalf("Open(future schema) err = %v, want ErrNewerSchema", err)
	}
}

func TestVerifySecondWriterRefusedInspectWorks(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "z.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	if _, err := store.Open(path); !errors.Is(err, store.ErrAlreadyRunning) {
		t.Fatalf("second Open err = %v, want ErrAlreadyRunning", err)
	}
	view, err := store.Inspect(path)
	if err != nil {
		t.Fatalf("Inspect while the writer holds the lock: %v", err)
	}
	if !view.SchemaVersionKnown || view.SchemaVersion != 3 {
		t.Fatalf("Inspect schema = %d known=%v, want 3/true", view.SchemaVersion, view.SchemaVersionKnown)
	}
	if _, ok := view.KV["schema"]; !ok {
		t.Fatal("Inspect returned no kv table")
	}
	if err := st.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	st2, err := store.Open(path)
	if err != nil {
		t.Fatalf("re-Open after Close: %v", err)
	}
	st2.Close()

	if _, err := store.Inspect(filepath.Join(dir, "missing.db")); err == nil {
		t.Fatal("Inspect(missing file) returned no error")
	}
}

func TestVerifyNestedAtomicAndAfterCommit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "z.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	committed := 0
	err = st.Atomic(func() error {
		if _, err := st.Exec(`INSERT INTO kv(k,v) VALUES('outer','1')`); err != nil {
			return err
		}
		innerErr := errors.New("inner failure")
		if err := st.Atomic(func() error {
			if _, err := st.Exec(`INSERT INTO kv(k,v) VALUES('inner','1')`); err != nil {
				return err
			}
			return innerErr
		}); !errors.Is(err, innerErr) {
			return err
		}
		st.AfterCommit(func() { committed++ })
		return nil
	})
	if err != nil {
		t.Fatalf("outer Atomic: %v", err)
	}
	if _, ok, _ := st.Get("outer"); !ok {
		t.Fatal("outer row missing")
	}
	if _, ok, _ := st.Get("inner"); ok {
		t.Fatal("inner SAVEPOINT rollback did not drop its row")
	}
	if committed != 1 {
		t.Fatalf("afterCommit ran %d times on commit, want 1", committed)
	}

	// A rolled-back outer transaction must drop the callback.
	committed = 0
	if err := st.Atomic(func() error {
		st.AfterCommit(func() { committed++ })
		return errors.New("rollback")
	}); err == nil {
		t.Fatal("expected rollback error")
	}
	if committed != 0 {
		t.Fatalf("afterCommit ran %d times on rollback, want 0", committed)
	}

	// Nested Atomic from the SAME goroutine only; a second goroutine must wait
	// for the first command and never join it (that was the pre-Wave1 bug).
	started := make(chan struct{})
	go func() {
		_ = st.Atomic(func() error {
			_, _ = st.Exec(`INSERT INTO kv(k,v) VALUES('a','1')`)
			close(started)
			time.Sleep(20 * time.Millisecond)
			return nil
		})
	}()
	<-started
	if err := st.Atomic(func() error {
		_, err := st.Exec(`INSERT INTO kv(k,v) VALUES('b','1')`)
		return err
	}); err != nil {
		t.Fatalf("second goroutine Atomic: %v", err)
	}
	if _, ok, _ := st.Get("b"); !ok {
		t.Fatal("second command's row missing")
	}
}

func TestVerifyConcurrentCommandsSerialized(t *testing.T) {
	path := filepath.Join(t.TempDir(), "z.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	const writers, reads = 6, 4
	var wg sync.WaitGroup
	errs := make(chan error, writers+reads)
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs <- st.Atomic(func() error {
				_, err := st.Exec(`INSERT OR REPLACE INTO kv(k,v) VALUES(?,?)`, "k"+string(rune('a'+i)), "v")
				return err
			})
		}(i)
	}
	for i := 0; i < reads; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rows, err := st.Query(`SELECT count(*) FROM kv`)
			if err == nil {
				for rows.Next() {
				}
				rows.Close()
				err = st.QueryRow(`SELECT count(*) FROM kv`).Scan(new(int))
			}
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent command error: %v", err)
		}
	}
	var n int
	if err := st.QueryRow(`SELECT count(*) FROM kv WHERE k LIKE 'k%'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != writers {
		t.Fatalf("rows = %d, want %d", n, writers)
	}
}

// TestVerifyTimelineCursorSkipsNoTies writes three Triggers at the SAME instant
// and pages 2+1; the prototype's trigger_thread exists so a tie is not skipped.
func TestVerifyTimelineCursorSkipsNoTies(t *testing.T) {
	path := filepath.Join(t.TempDir(), "z.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	at := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	threadID := "gh:acme/webapp#12"
	thread := domain.ThreadKey(domain.Global, "github", threadID)
	if err := st.Atomic(func() error {
		for _, id := range []string{"a", "b", "c"} {
			tr := fixtureTrigger(id, threadID, at)
			if err := st.PutTrigger(tr, domain.Disposition{TriggerID: tr.ID, Kind: domain.DispDecided}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	page1, err := st.TriggerThreadPage(thread, "", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(page1) != 2 {
		t.Fatalf("page1 = %d rows, want 2", len(page1))
	}
	last := page1[len(page1)-1]
	page2, err := st.TriggerThreadPage(thread, store.ThreadCursor(last.At, last.ID), 10)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, r := range append(page1, page2...) {
		if seen[string(r.ID)] {
			t.Fatalf("row %s repeated across pages", r.ID)
		}
		seen[string(r.ID)] = true
	}
	if len(seen) != 3 {
		t.Fatalf("cursor skipped a same-instant row: saw %v", seen)
	}
	if _, err := st.TriggerThreadPage(thread, "garbage", 2); !errors.Is(err, store.ErrBadCursor) {
		t.Fatalf("bad cursor err = %v, want ErrBadCursor", err)
	}
}

// TestVerifyHelperWriteStore is the second process of the cross-process check.
func TestVerifyHelperWriteStore(t *testing.T) {
	if os.Getenv("ZE_VERIFY_HELPER") != "1" {
		t.Skip("helper process only")
	}
	path := os.Getenv("ZE_VERIFY_DB")
	threadID := "gh:acme/webapp#12"
	st, err := store.Open(path)
	if err != nil {
		t.Fatalf("helper open %s: %v", path, err)
	}
	defer st.Close()
	t1 := time.Date(2026, 11, 3, 9, 0, 0, 500_000_000, time.UTC)
	t2 := time.Date(2026, 11, 3, 9, 0, 0, 550_000_000, time.UTC)
	for _, tr := range []domain.Trigger{fixtureTrigger("p-1", threadID, t1), fixtureTrigger("p-2", threadID, t2)} {
		if err := st.PutTrigger(tr, domain.Disposition{TriggerID: tr.ID, Kind: domain.DispDecided}); err != nil {
			t.Fatalf("helper put trigger: %v", err)
		}
	}
}

// TestVerifyTriggerAtSortableAcrossProcesses runs the helper as a REAL second
// process, then reads/orders the rows here: text order must equal time order.
func TestVerifyTriggerAtSortableAcrossProcesses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cross.db")
	cmd := exec.Command(os.Args[0], "-test.run=^TestVerifyHelperWriteStore$", "-test.v")
	cmd.Env = append(os.Environ(), "ZE_VERIFY_HELPER=1", "ZE_VERIFY_DB="+path)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("helper process: %v\n%s", err, out)
	}
	st, err := store.Open(path)
	if err != nil {
		t.Fatalf("parent open: %v", err)
	}
	defer st.Close()
	rows, err := st.Query(`SELECT id, at FROM trigger ORDER BY at`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var ids, ats []string
	for rows.Next() {
		var id, at string
		if err := rows.Scan(&id, &at); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
		ats = append(ats, at)
	}
	if len(ids) != 2 {
		t.Fatalf("rows = %v", ids)
	}
	if !fixedWidth.MatchString(ats[0]) || !fixedWidth.MatchString(ats[1]) {
		t.Fatalf("cross-process ats not fixed width: %v", ats)
	}
	if ids[0] != "p-1" || ids[1] != "p-2" {
		t.Fatalf("cross-process text order = %v, want [p-1 p-2]", ids)
	}
}

// A read from another goroutine while a command transaction is open must WAIT
// and then see the committed value: joining the transaction would let a reader
// observe (and hold) state that a rollback later erases.
func TestVerifyReaderWaitsForCommandTransaction(t *testing.T) {
	path := filepath.Join(t.TempDir(), "z.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	inTx := make(chan struct{})
	release := make(chan struct{})
	go func() {
		_ = st.Atomic(func() error {
			if _, err := st.Exec(`INSERT INTO kv(k,v) VALUES('tx-row','1')`); err != nil {
				return err
			}
			close(inTx)
			<-release
			return nil
		})
	}()
	<-inTx

	readDone := make(chan error, 1)
	go func() {
		var v string
		err := st.QueryRow(`SELECT v FROM kv WHERE k='tx-row'`).Scan(&v)
		if err == nil && v != "1" {
			err = errors.New("read returned " + v)
		}
		readDone <- err
	}()
	select {
	case err := <-readDone:
		t.Fatalf("a reader returned while the command transaction was open: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	select {
	case err := <-readDone:
		if err != nil {
			t.Fatalf("reader after commit: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("reader still blocked after the command committed")
	}
}
