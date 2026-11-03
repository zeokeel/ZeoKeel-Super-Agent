package store

// Regression tests for the store's process/tx invariants: one writer per state
// directory, a readable status view while that writer runs, and a command
// transaction that other goroutines cannot join.

import (
	"errors"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"
)

func TestSecondWriterIsRefusedAndInspectStillWorks(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// A second writer on the same state directory is refused with a clear error.
	second, err := Open(path)
	if err == nil {
		second.Close()
		t.Fatal("a second writer opened the same state directory")
	}
	if !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("second Open = %v, want ErrAlreadyRunning", err)
	}

	// A read-only inspection works while the writer holds the lock: that is what
	// `zeokeel status` needs.
	if err := s.Set("source.github.cursor", "abc"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Exec(`INSERT INTO health(entry,state,detail,at) VALUES('effect:reply','ready','nothing waiting',?)`,
		FormatTime(time.Date(2026, 2, 1, 10, 0, 0, 0, time.UTC))); err != nil {
		t.Fatal(err)
	}
	view, err := Inspect(path)
	if err != nil {
		t.Fatalf("Inspect while the writer runs: %v", err)
	}
	if !view.SchemaVersionKnown || view.SchemaVersion != SchemaVersion {
		t.Fatalf("Inspect schema version = %d known=%v, want %d", view.SchemaVersion, view.SchemaVersionKnown, SchemaVersion)
	}
	if got := view.KV["source.github.cursor"]; got != "abc" {
		t.Fatalf("Inspect kv = %q", got)
	}
	if len(view.Health) != 1 || view.Health[0].Entry != "effect:reply" || view.Health[0].State != "ready" {
		t.Fatalf("Inspect health = %+v", view.Health)
	}
	if !view.Health[0].At.Equal(time.Date(2026, 2, 1, 10, 0, 0, 0, time.UTC)) {
		t.Fatalf("Inspect health at = %v", view.Health[0].At)
	}

	// Closing releases the lock: a new writer may then open the same directory.
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	again, err := Open(path)
	if err != nil {
		t.Fatalf("reopen after Close: %v", err)
	}
	again.Close()

	// Inspect on a path that does not exist is an error, not an empty view.
	if _, err := Inspect(filepath.Join(dir, "missing.db")); err == nil {
		t.Fatal("Inspect of a missing file returned no error")
	}
}

func TestHealthEntriesOrdered(t *testing.T) {
	s, _ := openTemp(t)
	now := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	for _, e := range []struct{ entry, state string }{
		{"im:dingtalk", "ready"}, {"effect:reply", "error"}, {"source:github", "ready"},
	} {
		if _, err := s.Exec(`INSERT INTO health(entry,state,detail,at) VALUES(?,?,'d',?)`,
			e.entry, e.state, FormatTime(now)); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.HealthEntries()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"effect:reply", "im:dingtalk", "source:github"}
	if len(got) != len(want) {
		t.Fatalf("entries = %+v", got)
	}
	for i := range want {
		if got[i].Entry != want[i] {
			t.Fatalf("entry %d = %q, want %q", i, got[i].Entry, want[i])
		}
	}
}

// A command transaction must not be joined by another goroutine's read: with the
// old raw BEGIN on the single pooled connection, a reader that ran while a
// command was open would execute inside that transaction and could see (or
// block on) uncommitted state. database/sql now checks the connection out for the
// duration of the transaction, so the reader waits and then sees the committed
// value.
func TestReaderCannotJoinCommandTransaction(t *testing.T) {
	s, _ := openTemp(t)
	inTx := make(chan struct{})
	release := make(chan struct{})
	txDone := make(chan error, 1)
	go func() {
		txDone <- s.Atomic(func() error {
			close(inTx)
			<-release
			return s.Set("k", "committed")
		})
	}()
	<-inTx

	read := make(chan string, 1)
	go func() {
		var v string
		if err := s.QueryRow(`SELECT v FROM kv WHERE k='k'`).Scan(&v); err != nil {
			read <- "error: " + err.Error()
			return
		}
		read <- v
	}()
	// Give the reader time to reach the database; it must be blocked on the
	// connection, not running inside the open transaction.
	select {
	case v := <-read:
		t.Fatalf("reader saw %q while the command transaction was open", v)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	if err := <-txDone; err != nil {
		t.Fatalf("command: %v", err)
	}
	if v := <-read; v != "committed" {
		t.Fatalf("reader after commit = %q, want the committed value", v)
	}
}

// Writers serialize and no write is lost: each Atomic is one read-modify-write,
// and the guard is what makes the sum exact.
func TestConcurrentWritersAndReaders(t *testing.T) {
	s, _ := openTemp(t)
	if err := s.Set("counter", "0"); err != nil {
		t.Fatal(err)
	}
	const writers, perWriter, readers = 8, 25, 4
	errs := make(chan error, writers+readers)
	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := 0; n < perWriter; n++ {
				err := s.Atomic(func() error {
					var raw string
					if err := s.QueryRow(`SELECT v FROM kv WHERE k='counter'`).Scan(&raw); err != nil {
						return err
					}
					cur, err := strconv.Atoi(raw)
					if err != nil {
						return err
					}
					return s.Set("counter", strconv.Itoa(cur+1))
				})
				if err != nil {
					errs <- err
					return
				}
			}
		}()
	}
	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := 0; n < 50; n++ {
				rows, err := s.Query(`SELECT count(*) FROM kv`)
				if err != nil {
					errs <- err
					return
				}
				rows.Close()
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent access: %v", err)
	}
	v, ok, err := s.Get("counter")
	if err != nil || !ok {
		t.Fatalf("counter: ok=%v err=%v", ok, err)
	}
	if v != strconv.Itoa(writers*perWriter) {
		t.Fatalf("counter = %s, want %d (a write was lost)", v, writers*perWriter)
	}
}
