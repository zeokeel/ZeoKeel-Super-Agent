package store

import (
	"database/sql"
	"errors"
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

// settingsSchemaKey is where the settings *layout* version lives. It is separate
// from settingsRevKey: the revision is the optimistic-concurrency counter (it
// changes on every save), while the schema version changes only when the meaning or
// set of keys changes and a migration must run. Runtime owns applying migrations;
// the store exposes the stamp so a migration can be ordered and tested.
const settingsSchemaKey = "setting.__schema"

// SettingsSchemaVersion is the settings layout this build understands. Bump it when
// a migration is needed and teach the runtime's migration hook about the new value.
const SettingsSchemaVersion = 1

// SettingsSchema returns the settings layout version stamped on disk. known is
// false for an install that has never been migrated (or a v0 file); that is a
// migration decision for the caller, not an error.
func (s *Store) SettingsSchema() (version int, known bool, err error) {
	raw, err := s.kvGet(settingsSchemaKey)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	n, perr := strconv.Atoi(strings.TrimSpace(raw))
	if perr != nil {
		return 0, false, fmt.Errorf("store: bad settings schema %q: %w", raw, perr)
	}
	return n, true, nil
}

// SetSettingsSchema stamps the settings layout version. The runtime's migration
// calls it once after its migration succeeded; SaveSettings does not touch it.
func (s *Store) SetSettingsSchema(version int) error {
	if version < 0 {
		return fmt.Errorf("store: negative settings schema %d", version)
	}
	return s.kvSet(settingsSchemaKey, strconv.Itoa(version))
}

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
		// "__" keys are the store's own bookkeeping (revision, schema version).
		// They are not settings and callers must not see or write them through this
		// API; returning them would make a whole-snapshot save write them back.
		if strings.HasPrefix(key, "__") {
			continue
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
		if errors.Is(err, sql.ErrNoRows) {
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
			// "__" is the store's bookkeeping namespace (revision, schema version).
			// A caller that passes the revision back (some save paths do) must not
			// be able to overwrite the store's own keys; ignoring it keeps the old
			// accepted-input behavior without letting it corrupt the CAS counter.
			if strings.HasPrefix(k, "__") {
				continue
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
