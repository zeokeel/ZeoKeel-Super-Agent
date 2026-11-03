package store

import (
	"database/sql"
	"errors"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/zeokeel/super-agent/internal/domain"
)

// writeLegacyDB creates a database the way a pre-v3 build left it: the v1 and v2
// statements from the ordered list, the version stamp, and (optionally) the M4
// schedule/goal tables that store.EnsureV3 used to create with IF NOT EXISTS plus
// the kv marker.
func writeLegacyDB(t *testing.T, path string, version int, withM4 bool) *sql.DB {
	t.Helper()
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := raw.Exec(query, args...); err != nil {
			t.Fatalf("setup %q: %v", firstLine(query), err)
		}
	}
	execAll := func(stmts []string) {
		t.Helper()
		for _, s := range stmts {
			exec(s)
		}
	}
	execAll(migrations[0].sql)
	if version >= 2 {
		execAll(migrations[1].sql)
	}
	if withM4 {
		// The M4-era statements, exactly as they shipped: idempotent, no task
		// column yet, and a second version truth in kv.
		exec(`CREATE TABLE IF NOT EXISTS schedule(
  id               TEXT PRIMARY KEY,
  workspace_id     TEXT,
  spec             TEXT NOT NULL,
  enabled          INTEGER NOT NULL DEFAULT 1,
  created_at       TEXT NOT NULL,
  updated_at       TEXT NOT NULL,
  last_fired_at    TEXT,
  next_at          TEXT,
  running_fire     TEXT NOT NULL DEFAULT '',
  last_status      TEXT NOT NULL DEFAULT '',
  last_skip_reason TEXT NOT NULL DEFAULT '',
  last_skip_at     TEXT
)`)
		exec(`CREATE TABLE IF NOT EXISTS schedule_fire(
  id          TEXT PRIMARY KEY,
  schedule_id TEXT NOT NULL,
  planned_at  TEXT NOT NULL,
  fired_at    TEXT NOT NULL,
  status      TEXT NOT NULL,
  reason      TEXT NOT NULL DEFAULT '',
  created_at  TEXT NOT NULL
)`)
		exec(`CREATE TABLE IF NOT EXISTS goal(
  id                  TEXT PRIMARY KEY,
  workspace_id        TEXT NOT NULL,
  state               TEXT NOT NULL,
  revision            INTEGER NOT NULL,
  objective           TEXT NOT NULL,
  max_rounds          INTEGER NOT NULL,
  block_threshold     INTEGER NOT NULL,
  blocked_reason      TEXT NOT NULL DEFAULT '',
  consecutive_blocked INTEGER NOT NULL DEFAULT 0,
  rounds_count        INTEGER NOT NULL DEFAULT 0,
  created_at          TEXT NOT NULL,
  updated_at          TEXT NOT NULL,
  json                TEXT NOT NULL
)`)
		exec(`INSERT INTO kv(k,v) VALUES('schema.schedule_goal','3')`)
	}
	exec(`INSERT OR REPLACE INTO kv(k,v) VALUES('schema',?)`, strconv.Itoa(version))
	return raw
}

func firstLine(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			return s[:i]
		}
	}
	return s
}

// A v2 file upgrades to v3 in place: old rows survive, the v3 tables and columns
// appear, and the version is stamped last. This is the regression the old split
// version model could not express.
func TestSchemaV2FileUpgradesToV3(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v2.db")
	raw := writeLegacyDB(t, path, 2, false)
	// Old rows written by the RFC3339Nano path, plus a canonical one, prove the
	// normalization and the backfill.
	for _, row := range []struct{ id, at string }{
		{"tr_frac", "2026-01-01T00:00:00Z"},
		{"tr_mid", "2026-01-01T00:00:00.5Z"},
		{"tr_canon", "2026-01-01T00:00:01.000000000Z"},
	} {
		if _, err := raw.Exec(`INSERT INTO trigger(id,scope,workspace_id,default_workspace,source,at,human,json)
			VALUES(?, 'global', NULL, NULL, 'local', ?, 1, ?)`, row.id, row.at,
			`{"ID":"`+row.id+`","Scope":"global","Source":"local","Human":true,"At":"2026-01-01T00:00:00Z","Threads":[{"Source":"local","ID":"task:1"}]}`); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := raw.Exec(`INSERT INTO outbox(key,ticket_id,channel,state,text,created_at,
		conversation,thread,reply_to,kind,updated_at) VALUES('gone1','t1','dingtalk','gone','hi',
		'2026-01-01T00:00:00Z','dm:1','dm:1','','reply','2026-01-02T03:04:05Z')`); err != nil {
		t.Fatal(err)
	}
	// A Decision written before decision.thread existed: its id derives from the
	// Trigger, so the migration can recover the Thread from the Trigger JSON.
	decID := string(domain.Key(domain.Global, "decision", "tr_mid"))
	if _, err := raw.Exec(`INSERT INTO decision(id,workspace_id,kind,json)
		VALUES(?,'ws_a','accept','{"ID":"`+decID+`","Kind":"accept","Workspace":"ws_a"}')`, decID); err != nil {
		t.Fatal(err)
	}
	raw.Close()

	s, err := Open(path)
	if err != nil {
		t.Fatalf("upgrade v2 -> v3: %v", err)
	}
	defer s.Close()

	if v, ok, err := s.Get("schema"); err != nil || !ok || v != "3" {
		t.Fatalf("schema version %q ok=%v err=%v, want 3", v, ok, err)
	}
	for _, table := range []string{"schedule", "schedule_fire", "goal", "trigger_thread"} {
		if !tableExists(t, s, table) {
			t.Fatalf("v3 table %q missing", table)
		}
	}
	// The legacy trigger rows survive and their at column is now fixed width, so
	// text order equals time order ("…00Z" must sort before "…00.5Z").
	rows, err := s.Query(`SELECT id, at FROM trigger ORDER BY at`)
	if err != nil {
		t.Fatal(err)
	}
	var ats []string
	for rows.Next() {
		var id, at string
		if err := rows.Scan(&id, &at); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		ats = append(ats, id+"@"+at)
	}
	rows.Close()
	want := []string{
		"tr_frac@2026-01-01T00:00:00.000000000Z",
		"tr_mid@2026-01-01T00:00:00.500000000Z",
		"tr_canon@2026-01-01T00:00:01.000000000Z",
	}
	if len(ats) != len(want) {
		t.Fatalf("trigger rows %v, want %v", ats, want)
	}
	for i := range want {
		if ats[i] != want[i] {
			t.Fatalf("trigger %d = %q, want %q (all: %v)", i, ats[i], want[i], ats)
		}
	}
	// The inverted index was backfilled from the Trigger JSON.
	page, err := s.TriggerThreadPage("global|local|task:1", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 3 {
		t.Fatalf("backfilled trigger_thread rows = %d, want 3", len(page))
	}
	for _, r := range page {
		if !r.Human || r.At.IsZero() {
			t.Fatalf("backfilled row lost its facts: %+v", r)
		}
	}
	// decision.thread was backfilled from the Trigger's Thread list.
	var decThread string
	if err := s.QueryRow(`SELECT IFNULL(thread,'') FROM decision WHERE id=?`, decID).Scan(&decThread); err != nil {
		t.Fatalf("read decision thread: %v", err)
	}
	if decThread != "global|local|task:1" {
		t.Fatalf("decision.thread = %q, want the trigger's thread", decThread)
	}

	// The gone receipt now carries the drop audit.
	recs, err := s.outboxWhere(`key='gone1'`)
	if err != nil || len(recs) != 1 {
		t.Fatalf("outbox record: %+v err=%v", recs, err)
	}
	if recs[0].DroppedReason != "gone" || recs[0].DroppedAt.IsZero() {
		t.Fatalf("drop audit missing: %+v", recs[0])
	}
	if !recs[0].DroppedAt.Equal(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)) {
		t.Fatalf("dropped_at = %v, want the receipt's updated_at", recs[0].DroppedAt)
	}
	// The two version truths are now one: the M4 marker is gone.
	if _, ok, _ := s.Get("schema.schedule_goal"); ok {
		t.Fatal("the legacy v3 marker survived the fold")
	}
}

// An M4-era file already has the schedule/goal tables (created by EnsureV3) while
// its global stamp says 2. Folding those tables into the ordered list must not hit
// "table already exists": this is the bug the old comment's plan would have.
func TestM4EraFileUpgradesToV3(t *testing.T) {
	path := filepath.Join(t.TempDir(), "m4.db")
	raw := writeLegacyDB(t, path, 2, true)
	if _, err := raw.Exec(`INSERT INTO schedule(id,workspace_id,spec,enabled,created_at,updated_at)
		VALUES('sch1','ws_a','{"kind":"daily","hour":9,"minute":30}',1,
		'2026-01-01T00:00:00.000000000Z','2026-01-01T00:00:00.000000000Z')`); err != nil {
		t.Fatal(err)
	}
	raw.Close()

	s, err := Open(path)
	if err != nil {
		t.Fatalf("upgrade M4-era file: %v", err)
	}
	defer s.Close()

	if v, _, _ := s.Get("schema"); v != "3" {
		t.Fatalf("schema version %q, want 3", v)
	}
	// The row survived and the task column was added with its default.
	var id, task string
	if err := s.QueryRow(`SELECT id, task FROM schedule WHERE id='sch1'`).Scan(&id, &task); err != nil {
		t.Fatalf("schedule row lost: %v", err)
	}
	if id != "sch1" || task != "" {
		t.Fatalf("schedule row = %q task=%q", id, task)
	}
	if _, ok, _ := s.Get("schema.schedule_goal"); ok {
		t.Fatal("the legacy v3 marker survived the fold")
	}
}

// A fresh file is stamped with the current version and has every table and
// column, which a first-migration-only path would hide.
func TestFreshStoreIsFullyMigrated(t *testing.T) {
	s, _ := openTemp(t)
	if v, _, _ := s.Get("schema"); v != "3" {
		t.Fatalf("fresh schema version %q, want 3", v)
	}
	for _, table := range []string{
		"workspace", "trigger", "disposition", "thread", "ticket", "decision",
		"effect", "outbox", "session_event", "health", "kv",
		"schedule", "schedule_fire", "goal", "trigger_thread",
	} {
		if !tableExists(t, s, table) {
			t.Fatalf("fresh table %q missing", table)
		}
	}
	for table, cols := range map[string][]string{
		"workspace": {"policy"},
		"effect":    {"since"},
		"outbox":    {"conversation", "thread", "reply_to", "kind", "updated_at", "dropped_at", "dropped_reason"},
		"decision":  {"thread"},
		"schedule":  {"task", "next_at", "running_fire"},
	} {
		have := columns(t, s, table)
		for _, c := range cols {
			if !have[c] {
				t.Fatalf("fresh table %s is missing column %s", table, c)
			}
		}
	}
}

// A newer file is refused rather than misread. The check reads the one version
// stamp; a v3-era marker cannot make an older build think the file is its own.
func TestNewerSchemaRefusedWithDetail(t *testing.T) {
	path := filepath.Join(t.TempDir(), "new.db")
	raw := writeLegacyDB(t, path, 4, false)
	raw.Close()
	s, err := Open(path)
	if err == nil {
		s.Close()
		t.Fatal("a v4 file was opened by a v3 build")
	}
	if !errors.Is(err, ErrNewerSchema) {
		t.Fatalf("got %v, want ErrNewerSchema", err)
	}
}

func tableExists(t *testing.T, s *Store, table string) bool {
	t.Helper()
	var n int
	if err := s.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n == 1
}

// The timeline read path: a page continues from an <at>|<id> cursor and rows that
// share an instant are not skipped.
func TestTriggerThreadPageCursorDoesNotSkipSameInstant(t *testing.T) {
	s, _ := openTemp(t)
	at := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	thread := string(domain.ThreadKey(domain.Global, "local", "task:9"))
	for _, id := range []domain.ID{"tr_a", "tr_b", "tr_c"} {
		tr := domain.Trigger{
			ID: id, Scope: domain.Global, Source: "local", Human: true, At: at,
			Threads: []domain.ThreadRef{{Source: "local", ID: "task:9"}},
		}
		if err := s.PutTrigger(tr, domain.Disposition{TriggerID: id, Kind: domain.DispFiltered}); err != nil {
			t.Fatal(err)
		}
	}
	first, err := s.TriggerThreadPage(thread, "", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 2 {
		t.Fatalf("first page = %d rows, want 2", len(first))
	}
	cursor := ThreadCursor(first[len(first)-1].At, first[len(first)-1].ID)
	second, err := s.TriggerThreadPage(thread, cursor, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != 1 {
		t.Fatalf("second page = %+v, want the one skipped instant", second)
	}
	seen := map[domain.ID]bool{}
	for _, r := range append(first, second...) {
		seen[r.ID] = true
	}
	if len(seen) != 3 {
		t.Fatalf("timeline lost rows at one instant: %v", seen)
	}
	if _, err := s.TriggerThreadPage(thread, "not-a-cursor", 2); !errors.Is(err, ErrBadCursor) {
		t.Fatalf("bad cursor: %v, want ErrBadCursor", err)
	}
}

// Decisions can be listed by Thread, which is what the Eval page needs after a
// restart (the old store could only reverse the in-memory projection).
func TestDecisionsByThread(t *testing.T) {
	s, _ := openTemp(t)
	thread := string(domain.ThreadKey(domain.Global, "local", "task:1"))
	dec := domain.Decision{ID: "d1", Kind: domain.DecAccept, Workspace: "ws_a", Thread: thread}
	if err := s.PutDecision(dec); err != nil {
		t.Fatal(err)
	}
	if err := s.PutDecision(domain.Decision{ID: "d2", Kind: domain.DecIgnore}); err != nil {
		t.Fatal(err)
	}
	got, err := s.DecisionsByThread(thread)
	if err != nil || len(got) != 1 || got[0].ID != "d1" || got[0].Thread != thread {
		t.Fatalf("decisions by thread: %+v err=%v", got, err)
	}
	all, err := s.Decisions()
	if err != nil || len(all) != 2 {
		t.Fatalf("all decisions: %+v err=%v", all, err)
	}
}
