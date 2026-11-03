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

// A refused save must not write anything: the CAS check runs inside the same
// transaction as the values, so an ErrSettingsConflict leaves every stored value
// and the revision exactly as they were (the runtime's in-memory copy is a
// separate, caller-side problem).
func TestConflictLeavesDatabaseUnchanged(t *testing.T) {
	s, _ := openTemp(t)
	rev, err := s.SaveSettings(map[string]string{
		"execution.concurrency": "3",
		"onboard.model":         "deepseek-chat",
	}, 0, false)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := s.SaveSettings(map[string]string{
		"execution.concurrency": "6",
		"onboard.model":         "other-model",
		"onboard.base_url":      "https://example.invalid", // new key: must not appear
	}, rev-1, true); !errors.Is(err, ErrSettingsConflict) {
		t.Fatalf("stale save err = %v, want ErrSettingsConflict", err)
	}

	values, err := s.Settings()
	if err != nil {
		t.Fatal(err)
	}
	if values["execution.concurrency"] != "3" || values["onboard.model"] != "deepseek-chat" {
		t.Fatalf("a refused save changed values: %+v", values)
	}
	if _, ok := values["onboard.base_url"]; ok {
		t.Fatalf("a refused save created a new key: %+v", values)
	}
	if got, _ := s.SettingsRevision(); got != rev {
		t.Fatalf("revision = %d after a refused save, want %d", got, rev)
	}
}

// SaveSettings is all-or-nothing: a validation failure mid-save rolls the whole
// transaction back instead of leaving the keys that happened to be written first.
func TestSaveSettingsValidationRollsBack(t *testing.T) {
	s, _ := openTemp(t)
	if _, err := s.SaveSettings(map[string]string{
		"execution.concurrency": "3",
		"":                      "empty key must fail",
	}, 0, false); err == nil {
		t.Fatal("an empty setting key was accepted")
	}
	values, err := s.Settings()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := values["execution.concurrency"]; ok {
		t.Fatalf("partial save survived the rollback: %+v", values)
	}
	if rev, _ := s.SettingsRevision(); rev != 0 {
		t.Fatalf("revision advanced on a failed save: %d", rev)
	}
}

// The store owns its "__" bookkeeping namespace. A caller that passes the revision
// back (some save paths do) must not be able to overwrite the CAS counter, and
// Settings() must never expose bookkeeping as if it were configuration.
func TestSettingsBookkeepingKeysAreProtected(t *testing.T) {
	s, _ := openTemp(t)
	rev, err := s.SaveSettings(map[string]string{"execution.concurrency": "3"}, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetSettingsSchema(SettingsSchemaVersion); err != nil {
		t.Fatal(err)
	}

	// Passing bookkeeping keys back must be ignored, not applied.
	if _, err := s.SaveSettings(map[string]string{
		"__revision":   "999",
		"__schema":     "999",
		"onboard.done": "1",
	}, rev, true); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.SettingsRevision(); got != rev+1 {
		t.Fatalf("revision = %d, want %d (a caller overwrote the CAS counter)", got, rev+1)
	}
	if got, known, err := s.SettingsSchema(); err != nil || !known || got != SettingsSchemaVersion {
		t.Fatalf("settings schema = %d known=%v err=%v", got, known, err)
	}
	values, _ := s.Settings()
	for k := range values {
		if len(k) >= 2 && k[:2] == "__" {
			t.Fatalf("Settings() exposed bookkeeping key %q", k)
		}
	}
	if values["onboard.done"] != "1" {
		t.Fatalf("ordinary key lost: %+v", values)
	}
}

func TestSettingsSchemaStamp(t *testing.T) {
	s, _ := openTemp(t)
	if v, known, err := s.SettingsSchema(); err != nil || known || v != 0 {
		t.Fatalf("fresh schema = %d known=%v err=%v, want unknown", v, known, err)
	}
	if err := s.SetSettingsSchema(SettingsSchemaVersion); err != nil {
		t.Fatal(err)
	}
	if v, known, err := s.SettingsSchema(); err != nil || !known || v != SettingsSchemaVersion {
		t.Fatalf("stamped schema = %d known=%v err=%v", v, known, err)
	}
	// A later settings save must not disturb the stamp.
	if _, err := s.SaveSettings(map[string]string{"execution.concurrency": "2"}, 0, false); err != nil {
		t.Fatal(err)
	}
	if v, _, _ := s.SettingsSchema(); v != SettingsSchemaVersion {
		t.Fatalf("settings save changed the schema stamp to %d", v)
	}
	if err := s.SetSettingsSchema(-1); err == nil {
		t.Fatal("negative settings schema accepted")
	}
}
