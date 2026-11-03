package store

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/zeokeel/super-agent/internal/domain"
)

func openTemp(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "load.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, path
}

func columns(t *testing.T, s *Store, table string) map[string]bool {
	t.Helper()
	rows, err := s.DB().Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var (
			cid     int
			name    string
			ctype   string
			notnull int
			dflt    sql.NullString
			pk      int
		)
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			t.Fatal(err)
		}
		out[name] = true
	}
	return out
}

// A file written by schema v1 upgrades in place to v2: old rows survive, the new
// columns appear, and the version is stamped last.
func TestSchemaV1FileUpgradesToV2(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(migrations[0].sql[0]); err != nil {
		t.Fatalf("create v1: %v", err)
	}
	if _, err := raw.Exec(`INSERT INTO kv(k,v) VALUES('schema','1')`); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`INSERT INTO workspace(id,name,root,remote,kind,brief,created_at,updated_at)
		VALUES('ws_old','old','/old','','repo','{}','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`INSERT INTO effect(kind,key,seq,generation,attempts,due,error,payload)
		VALUES('relay','k1',1,0,0,'2026-01-01T00:00:00.000000000Z','','{}')`); err != nil {
		t.Fatal(err)
	}
	raw.Close()

	s, err := Open(path)
	if err != nil {
		t.Fatalf("upgrade v1 -> v2: %v", err)
	}
	defer s.Close()

	if v, ok, err := s.Get("schema"); err != nil || !ok || v != "3" {
		t.Fatalf("schema version %q ok=%v err=%v, want 3", v, ok, err)
	}
	// The v1 rows are still there.
	wss, err := s.Workspaces()
	if err != nil || len(wss) != 1 || wss[0].ID != "ws_old" {
		t.Fatalf("v1 workspace lost: %+v err=%v", wss, err)
	}
	var effects int
	if err := s.QueryRow(`SELECT COUNT(*) FROM effect`).Scan(&effects); err != nil || effects != 1 {
		t.Fatalf("v1 effect lost: %d err=%v", effects, err)
	}
	// The v2 columns are present.
	for table, cols := range map[string][]string{
		"workspace": {"policy"},
		"effect":    {"since"},
		"outbox":    {"conversation", "thread", "reply_to", "kind", "updated_at"},
	} {
		have := columns(t, s, table)
		for _, c := range cols {
			if !have[c] {
				t.Fatalf("table %s is missing v2 column %s", table, c)
			}
		}
	}
	// The policy of an upgraded workspace defaults to the empty policy.
	policies, err := s.WorkspacePolicies()
	if err != nil || policies["ws_old"] != "{}" {
		t.Fatalf("default policy %q err=%v", policies["ws_old"], err)
	}

	// A second open sees a v2 file and changes nothing.
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	again, err := Open(path)
	if err != nil {
		t.Fatalf("reopen v2: %v", err)
	}
	again.Close()
}

// A fresh file is stamped with the current version and has every column, which is
// the bug a first-migration-only fresh path would hide.
func TestFreshStoreHasEveryMigration(t *testing.T) {
	s, _ := openTemp(t)
	if v, _, _ := s.Get("schema"); v != "3" {
		t.Fatalf("fresh schema version %q, want 3", v)
	}
	for table, cols := range map[string][]string{
		"workspace": {"policy"},
		"effect":    {"since"},
		"outbox":    {"conversation", "thread", "reply_to", "kind", "updated_at"},
	} {
		have := columns(t, s, table)
		for _, c := range cols {
			if !have[c] {
				t.Fatalf("fresh table %s is missing column %s", table, c)
			}
		}
	}
}

// The engine's load path can read back every aggregate a command wrote.
func TestLoadHelpersRoundTrip(t *testing.T) {
	s, _ := openTemp(t)
	now := time.Now().UTC()
	ws := domain.Workspace{ID: "ws_a", Name: "a", Root: "/a", Kind: domain.KindRepo,
		Brief: domain.Brief{Purpose: "p"}}
	if err := s.PutWorkspace(ws); err != nil {
		t.Fatal(err)
	}
	if err := s.PutWorkspacePolicy("ws_a", `{"Version":"v1"}`); err != nil {
		t.Fatal(err)
	}

	tr := domain.Trigger{ID: "tr1", Scope: domain.Global, Source: "local", Human: true,
		At: now, Text: "hello", Threads: []domain.ThreadRef{{Source: "local", ID: "t1"}}}
	disp := domain.Disposition{TriggerID: tr.ID, Kind: domain.DispDecided,
		Reason: []domain.Reason{{Code: domain.ReasonSaid}}, TicketIDs: []domain.ID{"t1"}}
	if err := s.PutTrigger(tr, disp); err != nil {
		t.Fatal(err)
	}
	dec := domain.Decision{ID: "d1", Kind: domain.DecAccept, Workspace: "ws_a"}
	if err := s.PutDecision(dec); err != nil {
		t.Fatal(err)
	}
	tk := domain.Ticket{ID: "t1", Number: 7, Workspace: "ws_a", CreatedAt: now,
		Mandate: domain.Mandate{Workspace: "ws_a"}, Title: "hello"}
	if err := s.PutTicket(tk, now); err != nil {
		t.Fatal(err)
	}
	if err := s.PutThread(ThreadRecord{Key: "global|local|t1", Source: "local", LastAt: now,
		TriggerIDs: []domain.ID{"tr1"}, TicketIDs: []domain.ID{"t1"}}); err != nil {
		t.Fatal(err)
	}

	policies, err := s.WorkspacePolicies()
	if err != nil || policies["ws_a"] != `{"Version":"v1"}` {
		t.Fatalf("policy %q err=%v", policies["ws_a"], err)
	}
	triggers, err := s.Triggers()
	if err != nil || len(triggers) != 1 {
		t.Fatalf("triggers %+v err=%v", triggers, err)
	}
	if triggers[0].Trigger.ID != "tr1" || triggers[0].Disposition.Kind != domain.DispDecided {
		t.Fatalf("trigger round trip: %+v", triggers[0])
	}
	if len(triggers[0].Disposition.TicketIDs) != 1 || triggers[0].Disposition.TicketIDs[0] != "t1" {
		t.Fatalf("disposition tickets: %+v", triggers[0].Disposition)
	}
	decisions, err := s.Decisions()
	if err != nil || len(decisions) != 1 || decisions[0].ID != "d1" {
		t.Fatalf("decisions %+v err=%v", decisions, err)
	}
	tickets, err := s.Tickets()
	if err != nil || len(tickets) != 1 || tickets[0].Number != 7 {
		t.Fatalf("tickets %+v err=%v", tickets, err)
	}
	threads, err := s.Threads()
	if err != nil || len(threads) != 1 || threads[0].Key != "global|local|t1" {
		t.Fatalf("threads %+v err=%v", threads, err)
	}
	if len(threads[0].TriggerIDs) != 1 || len(threads[0].TicketIDs) != 1 {
		t.Fatalf("thread ids: %+v", threads[0])
	}
}

// Duplicate Trigger writes are ignored rather than failing: one command may be
// persisted twice by a retry.
func TestPutTriggerIsIdempotent(t *testing.T) {
	s, _ := openTemp(t)
	now := time.Now().UTC()
	tr := domain.Trigger{ID: "tr1", Scope: domain.Global, Source: "local", Human: true, At: now}
	d := domain.Disposition{TriggerID: "tr1", Kind: domain.DispFiltered}
	if err := s.PutTrigger(tr, d); err != nil {
		t.Fatal(err)
	}
	d.Kind = domain.DispDecided
	if err := s.PutTrigger(tr, d); err != nil {
		t.Fatalf("second write: %v", err)
	}
	triggers, err := s.Triggers()
	if err != nil || len(triggers) != 1 || triggers[0].Disposition.Kind != domain.DispDecided {
		t.Fatalf("idempotent write: %+v err=%v", triggers, err)
	}
}

// Receipt helpers expose pending rows and keys without exposing the state machine.
func TestOutboxReceiptHelpers(t *testing.T) {
	s, _ := openTemp(t)
	now := time.Now().UTC()
	insert := func(key, state string) {
		t.Helper()
		if _, err := s.Exec(`INSERT INTO outbox(key,ticket_id,channel,state,text,created_at,
			conversation,thread,reply_to,kind,updated_at) VALUES(?,?,?,'pending','hi',?,'dm:1','dm:1','',?,?)`,
			key, "t1", "dingtalk", now.Format(time.RFC3339Nano), "reply", now.Format(time.RFC3339Nano)); err != nil {
			t.Fatal(err)
		}
	}
	insert("m1", "pending")
	insert("m2", "pending")

	keys, err := s.OutboxKeys()
	if err != nil || len(keys) != 2 {
		t.Fatalf("keys %v err=%v", keys, err)
	}
	pending, err := s.PendingOutbox()
	if err != nil || len(pending) != 2 {
		t.Fatalf("pending %+v err=%v", pending, err)
	}
	if pending[0].Conversation != "dm:1" || pending[0].Ticket != "t1" || pending[0].Kind != "reply" {
		t.Fatalf("pending record: %+v", pending[0])
	}

	ok, err := s.SettleOutbox("m1", "sent", now)
	if err != nil || !ok {
		t.Fatalf("settle: ok=%v err=%v", ok, err)
	}
	// Settling again changes nothing: a receipt settles once.
	if ok, err := s.SettleOutbox("m1", "sent", now); err != nil || ok {
		t.Fatalf("second settle: ok=%v err=%v", ok, err)
	}
	pending, _ = s.PendingOutbox()
	if len(pending) != 1 || pending[0].Key != "m2" {
		t.Fatalf("after settle: %+v", pending)
	}
	// The settled receipt is kept.
	var kept int
	if err := s.QueryRow(`SELECT count(*) FROM outbox WHERE key='m1' AND state='sent'`).Scan(&kept); err != nil || kept != 1 {
		t.Fatalf("settled receipt lost: n=%d err=%v", kept, err)
	}
	// Keys include settled receipts, which is what the engine's dedupe needs.
	keys, _ = s.OutboxKeys()
	if len(keys) != 2 {
		t.Fatalf("keys after settle: %v", keys)
	}
}
