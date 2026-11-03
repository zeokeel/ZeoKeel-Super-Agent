package store

import (
	"errors"
	"testing"
)

// Settings use optimistic concurrency: the revision a caller read is compared
// inside the write transaction, so a window that read an old revision is refused
// instead of overwriting a newer save (design §6.5). Since the state directory now
// admits one writer at a time, the conflict is exercised through the caller's base
// revision — the same comparison SaveSettings makes — rather than by opening two
// contradicting writers on one file.
func TestSaveSettingsRejectsStaleBase(t *testing.T) {
	s, _ := openTemp(t)

	rev, err := s.SaveSettings(map[string]string{"execution.concurrency": "3"}, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if rev == 0 {
		t.Fatal("the first save did not advance the revision")
	}

	// A second window still holds revision 0: its save must be refused.
	if _, err := s.SaveSettings(map[string]string{"execution.concurrency": "6"}, 0, true); !errors.Is(err, ErrSettingsConflict) {
		t.Fatalf("stale save err = %v, want ErrSettingsConflict", err)
	}
	vals, err := s.Settings()
	if err != nil {
		t.Fatal(err)
	}
	if got := vals["execution.concurrency"]; got != "3" {
		t.Fatalf("a refused save changed the value to %q", got)
	}

	// The revision is on disk for the next process/window to read.
	fmtRev, err := s.SettingsRevision()
	if err != nil || fmtRev != rev {
		t.Fatalf("SettingsRevision = %d err=%v, want %d", fmtRev, err, rev)
	}

	// A caller with the fresh revision goes through and the revision advances.
	rev2, err := s.SaveSettings(map[string]string{"execution.concurrency": "6"}, rev, true)
	if err != nil {
		t.Fatal(err)
	}
	if rev2 <= rev {
		t.Fatalf("revision went %d -> %d", rev, rev2)
	}
	vals, _ = s.Settings()
	if got := vals["execution.concurrency"]; got != "6" {
		t.Fatalf("value = %q, want 6", got)
	}
	// Bookkeeping revision is not a setting.
	if _, ok := vals["__revision"]; ok {
		t.Fatal("the revision leaked into the settings map")
	}
}
