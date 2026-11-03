package runtime

import (
	"path/filepath"
	"strconv"
	"testing"

	"github.com/zeokeel/super-agent/internal/engine"
	"github.com/zeokeel/super-agent/internal/store"
	"github.com/zeokeel/super-agent/internal/view"
)

// Two windows on one database is the real desktop situation. The settings
// revision has to live on disk, or the second window believes it is current and
// silently overwrites the first (design §6.5).
//
// The state directory now allows exactly one writable Store (store.Open takes the
// single-instance lock), so "two windows" is modelled as two Runtime views over
// one shared Store handle. The stale-revision CAS is still exercised end to end:
// window B holds the old revision in memory, and its save must be refused by the
// store transaction exactly as it would be across processes.
func TestSettingsConflictAcrossWindows(t *testing.T) {
	db := filepath.Join(t.TempDir(), "zeokeel.db")
	st, err := store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	winA := New(engine.New(engine.RuleAssessor{}), view.NewSnapshot())
	winB := New(engine.New(engine.RuleAssessor{}), view.NewSnapshot())
	if err := winA.LoadSettings(st); err != nil {
		t.Fatal(err)
	}
	if err := winB.LoadSettings(st); err != nil {
		t.Fatal(err)
	}

	revA, err := winA.SaveSettings("execution", map[string]string{"concurrency": "3", "__revision": "0"})
	if err != nil {
		t.Fatal(err)
	}
	if revA == 0 {
		t.Fatal("the first save should produce a revision")
	}

	// Window B still holds the old revision: its save must be refused, not applied.
	_, err = winB.SaveSettings("execution", map[string]string{
		"concurrency": "6", "__revision": "0",
	})
	if err == nil {
		t.Fatal("a stale window overwrote a newer setting without a conflict")
	}

	// After reloading, the same save goes through and carries the fresh revision.
	if err := winB.LoadSettings(st); err != nil {
		t.Fatal(err)
	}
	revB, err := winB.SaveSettings("execution", map[string]string{
		"concurrency": "6", "__revision": strconv.FormatUint(revA, 10),
	})
	if err != nil {
		t.Fatalf("a reloaded window should be able to save: %v", err)
	}
	if revB <= revA {
		t.Fatalf("revision did not advance: %d → %d", revA, revB)
	}
}
