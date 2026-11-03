package store

import (
	"fmt"
	"strconv"
	"strings"
)

// settingPrefix namespaces panel settings in the key-value table, so they cannot
// collide with migration markers like "schema".
const settingPrefix = "setting:"

// settingsRevKey holds the configuration revision across processes. Without it two
// windows on the same database cannot see each other's writes: a per-process
// counter always starts at zero, so the second window believes it is up to date
// and overwrites the first (design §6.5).
const settingsRevKey = "setting.__revision"

// Settings returns every saved setting as a flat map keyed "group.key". A setting
// that was never saved is simply absent; the caller supplies its default.
func (s *Store) Settings() (map[string]string, error) {
	rows, err := s.Query(`SELECT k, v FROM kv WHERE k LIKE ?`, settingPrefix+"%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		key := strings.TrimPrefix(k, settingPrefix)
		if key == "__revision" {
			continue // bookkeeping, not a setting
		}
		out[key] = v
	}
	return out, rows.Err()
}

// SettingsRevision is the configuration revision on disk. Zero means "nothing has
// been saved yet".
func (s *Store) SettingsRevision() (uint64, error) {
	raw, err := s.kvGet(settingsRevKey)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "no rows") {
			return 0, nil
		}
		return 0, err
	}
	n, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		return 0, nil
	}
	return n, nil
}

// ErrSettingsConflict means another window saved after this caller read the
// settings. The caller must reload instead of overwriting.
var ErrSettingsConflict = fmt.Errorf("store: settings were changed by another window")

// SaveSettings writes a whole settings map in one transaction and returns the new
// revision. The revision lives in the same transaction as the values, so a crash
// cannot leave a value saved without its version.
//
// base is the revision the caller read. Comparing it inside the transaction (not
// in the caller's memory) is what makes two windows on one database safe: each
// process's counter goes stale the moment the other writes (design §6.5).
func (s *Store) SaveSettings(values map[string]string, base uint64, checkBase bool) (uint64, error) {
	var rev uint64
	err := s.Atomic(func() error {
		cur, err := s.SettingsRevision()
		if err != nil {
			return err
		}
		// A caller that does not pass a revision is not doing optimistic
		// concurrency (a migration, a test, the wizard's first save). A caller that
		// does pass one is a window, and gets told when it is stale.
		if checkBase && cur != base {
			return ErrSettingsConflict
		}
		for k, v := range values {
			if k == "" {
				return fmt.Errorf("store: empty setting key")
			}
			if err := s.kvSet(settingPrefix+k, v); err != nil {
				return err
			}
		}
		rev = cur + 1
		return s.kvSet(settingsRevKey, strconv.FormatUint(rev, 10))
	})
	return rev, err
}
