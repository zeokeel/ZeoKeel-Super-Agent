package main

// Operator-command tests: status exit codes, log rotation, backup/archive, the
// sender's retryable failure, and the age parser. They run with no display and
// CGO_ENABLED=0, like the rest of the suite.

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zeokeel/super-agent/internal/domain"
	"github.com/zeokeel/super-agent/internal/outbox"
	"github.com/zeokeel/super-agent/internal/runtime"
	"github.com/zeokeel/super-agent/internal/store"
)

// status must read schema/health/setup without taking the writer lock: the second
// Open in this test fails with ErrAlreadyRunning, which is exactly how a running
// app is detected.
func TestStatusExitCodes(t *testing.T) {
	dir := t.TempDir()

	// Nothing on disk yet: not running, not setup.
	if got := runtime.InspectStatus(dir).ExitCode(); got != runtime.ExitRestart {
		t.Fatalf("empty state dir: exit %d, want %d", got, runtime.ExitRestart)
	}

	st, err := store.Open(filepath.Join(dir, "zeokeel.db"))
	if err != nil {
		t.Fatal(err)
	}
	setup := func(withSetup bool) {
		t.Helper()
		if !withSetup {
			return // a fresh install: no model, no workspace -> waiting setup
		}
		if _, err := st.SaveSettings(map[string]string{"onboard.model": "mock", "onboard.done": "1"}, 0, false); err != nil {
			t.Fatal(err)
		}
		if err := st.PutWorkspace(domain.Workspace{ID: "ws_web", Name: "web", Root: "/tmp/web", Kind: domain.KindRepo}); err != nil {
			t.Fatal(err)
		}
	}

	setup(false)
	got := runtime.InspectStatus(dir)
	if !got.Running {
		t.Fatal("status did not detect the running store")
	}
	if got.ExitCode() != runtime.ExitSetup {
		t.Fatalf("waiting setup: exit %d, want %d (%v)", got.ExitCode(), runtime.ExitSetup, got.Setup)
	}

	setup(true)
	if code := runtime.InspectStatus(dir).ExitCode(); code != runtime.ExitOK {
		t.Fatalf("healthy: exit %d, want 0 (%+v)", code, runtime.InspectStatus(dir))
	}

	// A component error is a 2 even while running: restarting would not fix it.
	if _, err := st.Exec(`INSERT INTO health(entry,state,detail,at) VALUES(?,?,?,?)
		ON CONFLICT(entry) DO UPDATE SET state=excluded.state, detail=excluded.detail, at=excluded.at`,
		"effect:launch", "error", "boom", store.FormatTime(time.Now())); err != nil {
		t.Fatal(err)
	}
	if code := runtime.InspectStatus(dir).ExitCode(); code != runtime.ExitComponent {
		t.Fatalf("component error: exit %d, want %d", code, runtime.ExitComponent)
	}
	_ = st.Close()
}

// A fresh install is setup waiting (3), not a component failure (2).
func TestCheckFreshInstallIsSetup(t *testing.T) {
	if code := cmdCheck([]string{"--state-dir", t.TempDir()}); code != runtime.ExitSetup {
		t.Fatalf("check on a fresh install: exit %d, want %d", code, runtime.ExitSetup)
	}
}

// A deliberate stop is quiet: status reports the marker, and a supervisor script
// must not be told to restart (006 "manually stopped").
func TestStopMarkerQuietsStatus(t *testing.T) {
	dir := t.TempDir()
	if code := cmdStop([]string{"--state-dir", dir}); code != runtime.ExitOK {
		t.Fatalf("stop: exit %d", code)
	}
	st := runtime.InspectStatus(dir)
	if !st.ManuallyStopped {
		t.Fatal("status did not read the stop marker")
	}
	if code := st.ExitCode(); code != runtime.ExitOK {
		t.Fatalf("a manually stopped install: exit %d, want 0 (do not restart)", code)
	}
	if err := runtime.ClearManuallyStopped(dir); err != nil {
		t.Fatal(err)
	}
	if runtime.InspectStatus(dir).ManuallyStopped {
		t.Fatal("starting the app must clear the stop marker")
	}
}

// The log rotates at local midnight and prunes files older than 14 days.
func TestLogRotationAndPrune(t *testing.T) {
	dir := t.TempDir()
	day := time.Date(2026, 11, 3, 12, 0, 0, 0, time.Local)
	clock := func() time.Time { return day }
	log, err := newRotatingLog(dir, clock)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := log.Write([]byte("day-one\n")); err != nil {
		t.Fatal(err)
	}
	day = day.Add(24 * time.Hour)
	if _, err := log.Write([]byte("day-two\n")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "zeokeel-2026-11-03.log")); err != nil {
		t.Fatalf("yesterday's log was not rotated: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "zeokeel.log")); err != nil {
		t.Fatalf("today's log is missing: %v", err)
	}

	old := filepath.Join(dir, "zeokeel-2026-01-01.log")
	if err := os.WriteFile(old, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	day = day.Add(24 * time.Hour)
	if _, err := log.Write([]byte("day-three\n")); err != nil { // triggers prune
		t.Fatal(err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatal("a log older than 14 days was not pruned")
	}
	_ = log.Close()
}

// backup writes a consistent copy; it refuses while the app holds the lock.
func TestBackupCopiesTheDatabase(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "zeokeel.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.SaveSettings(map[string]string{"execution.concurrency": "3"}, 0, false); err != nil {
		t.Fatal(err)
	}
	// While this store is open, backup must refuse (exit 1: restart-fixable).
	if code := cmdBackup([]string{"--state-dir", dir}); code != runtime.ExitRestart {
		t.Fatalf("backup while running: exit %d, want %d", code, runtime.ExitRestart)
	}
	_ = st.Close()

	out := filepath.Join(dir, "backups", "copy.db")
	if code := cmdBackup([]string{"--state-dir", dir, "--out", out}); code != runtime.ExitOK {
		t.Fatalf("backup: exit %d", code)
	}
	copyStore, err := store.Inspect(out)
	if err != nil {
		t.Fatalf("the copy is not readable: %v", err)
	}
	if !copyStore.SchemaVersionKnown || copyStore.SchemaVersion < 1 {
		t.Fatalf("the copy has no schema stamp: %+v", copyStore)
	}
	info, err := os.Stat(out)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("backup perms = %o, want 600", info.Mode().Perm())
	}
}

// archive moves ended Tickets older than --before into archive.db and leaves the
// outbox (the delivery idempotency receipts) in the main database.
func TestArchiveMovesOldTickets(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "zeokeel.db"))
	if err != nil {
		t.Fatal(err)
	}
	old := domain.Ticket{
		ID: "t-old", Number: 1, Workspace: "ws_web", Title: "old work",
		CreatedAt: time.Now().Add(-400 * 24 * time.Hour),
		Ended:     &domain.Ended{At: time.Now().Add(-200 * 24 * time.Hour), Outcome: domain.OutcomeCompleted},
		Origin:    "trig-old",
	}
	if err := st.PutTicket(old, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := st.PutTrigger(domain.Trigger{ID: "trig-old", Source: "local", Text: "old", At: time.Now().Add(-400 * 24 * time.Hour)},
		domain.Disposition{TriggerID: "trig-old"}); err != nil {
		t.Fatal(err)
	}
	// A settled delivery receipt: the outbox row is the idempotency record.
	if _, err := st.Exec(`INSERT INTO outbox(key,workspace_id,ticket_id,channel,state,text,created_at)
		VALUES(?,?,?,?,?,?,?)`, "receipt-1", "ws_web", "", "dingtalk", "sent", "hello",
		store.FormatTime(time.Now())); err != nil {
		t.Fatal(err)
	}
	_ = st.Close()

	if code := cmdArchive([]string{"--state-dir", dir, "--before", "180d"}); code != runtime.ExitOK {
		t.Fatalf("archive: exit %d", code)
	}
	st2, err := store.Open(filepath.Join(dir, "zeokeel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()
	if got, err := st2.Tickets(); err != nil || len(got) != 0 {
		t.Fatalf("archived ticket still in the main db: %+v %v", got, err)
	}
	// The outbox receipt is kept: it is the idempotency record, not history.
	keys, err := st2.OutboxKeys()
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 || keys[0] != "receipt-1" {
		t.Fatalf("outbox receipts were lost by archive: %v", keys)
	}
	if _, err := os.Stat(filepath.Join(dir, "archive.db")); err != nil {
		t.Fatalf("archive.db is missing: %v", err)
	}
}

func TestParseAge(t *testing.T) {
	cases := map[string]time.Duration{
		"180d": 180 * 24 * time.Hour,
		"720h": 720 * time.Hour,
	}
	for raw, want := range cases {
		got, err := parseAge(raw)
		if err != nil || got != want {
			t.Fatalf("parseAge(%q) = %v, %v; want %v", raw, got, err, want)
		}
	}
	if _, err := parseAge("soon"); err == nil {
		t.Fatal("a bad --before value must be refused")
	}
}

// A Source with no live driver must not report "sent": that is AUDIT §0.2-8.
func TestSenderWithoutDriverReturnsRetryableError(t *testing.T) {
	s := newSourceSender()
	if _, err := s.Send(context.Background(), outbox.Message{Source: "dingtalk"}); err == nil {
		t.Fatal("a message with no driver must fail, not claim to be sent")
	}
}
