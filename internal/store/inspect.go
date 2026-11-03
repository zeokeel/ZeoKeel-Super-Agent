package store

// Read-only inspection for `zeokeel status` and the panel's health view. Status
// must work while the app is running and holds the state directory lock, so
// Inspect opens the database with mode=ro, never migrates and takes no exclusive
// lock. It reads exactly the facts status prints: the schema version, the health
// entries the durable components wrote, the kv table (cursors, revisions), and the
// small aggregate counts that answer "is anything waiting?" without opening the
// writer.
//
// Everything this file returns is safe to print: secret values are redacted (see
// redact.go) and health details are scrubbed of credentials. The writer-side
// accessors (Settings, Get) still return real values because the runtime needs
// them; Inspect's job is the opposite: a status/debug surface that cannot leak a
// token into a terminal, a log file or a support bundle.

import (
	"database/sql"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

// HealthEntry is one durable component's self-report: the effect queue writes
// "effect:<kind>", the IM loops and the Source loops write their own names.
type HealthEntry struct {
	Entry  string
	State  string // ready | error | ...
	Detail string
	At     time.Time
}

// HealthView is what status shows about the store without opening it for writing.
type HealthView struct {
	Path string
	// SchemaVersion is the version stamped in kv, and SchemaVersionKnown says
	// whether the file had one at all (a v0 file, or a database that is not ours).
	SchemaVersion      int
	SchemaVersionKnown bool
	Health             []HealthEntry
	// KV is the kv table as a status view may show it: cursors, prompt revision,
	// settings revision, the schema stamp and the master session id all live there.
	// Values of secret-named keys (app secrets, API keys, tokens; see IsSecretKey)
	// are replaced with RedactedValue, so printing this map cannot leak a
	// credential. The key stays visible so "configured / not configured" is still
	// answerable. The writer-side Settings()/Get() accessors return the real
	// values; they are for the process that owns the store, not for status.
	KV map[string]string

	// Workspaces is the number of registered workspaces.
	Workspaces int

	// TicketsByStage counts Tickets per persisted stage ("ended", "working/queued",
	// "person/answer", ...).
	//
	// It reads the ticket.stage column, which PutTicket writes from
	// domain.StageOf at save time: it is a query cache, not a live re-derivation.
	// A Ticket whose facts changed since its last save (for example an Assignment
	// that became due while nothing wrote the Ticket) is counted under its stored
	// stage. That is the honest number from the file; status must not recompute
	// domain state with a different clock than the writer had.
	TicketsByStage map[string]int

	// EffectsByKind counts queued effects per kind ("reply", "launch", ...).
	//
	// Semantics: every row in the effect table is an effect that has not executed
	// yet — the queue deletes a row on success (internal/effects attempt). So this
	// is the number of waiting effects per kind, including rows held back by
	// backoff or by a Later() reschedule. There is deliberately no due<=now filter:
	// such a row is still waiting, and a count that silently dropped it would make
	// "3 waiting" disagree with the panel.
	EffectsByKind map[string]int
}

// Inspect reads a database at path without taking the writer's lock and without
// running migrations. It is safe to call while another process (or this one)
// holds the writable Store. A missing file is an error; a file that is not a
// zeokeel database yields empty views, not a failure, because status has to be
// able to say "no health yet".
//
// Fields that the schema cannot answer are returned as zero values (empty maps,
// zero counts), never guessed from unrelated rows.
func Inspect(path string) (HealthView, error) {
	view := HealthView{
		Path:           path,
		KV:             map[string]string{},
		TicketsByStage: map[string]int{},
		EffectsByKind:  map[string]int{},
	}
	if _, err := os.Stat(path); err != nil {
		return view, fmt.Errorf("store: inspect %q: %w", path, err)
	}
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro&_pragma=query_only(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		return view, fmt.Errorf("store: inspect %q: %w", path, err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	kv, err := inspectStringMap(db, `SELECT k, v FROM kv ORDER BY k`, "kv")
	if err != nil {
		return view, err
	}
	// Read the schema stamp from the raw map (it is not a secret; doing it before
	// redaction keeps this independent of IsSecretKey's rules).
	if raw, ok := kv["schema"]; ok {
		if v, err := strconv.Atoi(raw); err == nil {
			view.SchemaVersion = v
			view.SchemaVersionKnown = true
		}
	}
	for k, v := range kv {
		if IsSecretKey(k) {
			view.KV[k] = RedactedValue
			continue
		}
		view.KV[k] = v
	}

	health, err := inspectHealth(db)
	if err != nil {
		return view, err
	}
	view.Health = health

	if view.TicketsByStage, err = inspectIntCounts(db,
		`SELECT stage, COUNT(*) FROM ticket GROUP BY stage`, "ticket stages"); err != nil {
		return view, err
	}
	if view.EffectsByKind, err = inspectIntCounts(db,
		`SELECT kind, COUNT(*) FROM effect GROUP BY kind`, "effect kinds"); err != nil {
		return view, err
	}
	if view.Workspaces, err = inspectCount(db, `SELECT COUNT(*) FROM workspace`, "workspaces"); err != nil {
		return view, err
	}
	return view, nil
}

// inspectStringMap runs a two-column (key, value) query, returning an empty map
// when the table does not exist yet (a v0 or foreign database).
func inspectStringMap(db *sql.DB, query, what string) (map[string]string, error) {
	out := map[string]string{}
	rows, err := db.Query(query)
	switch {
	case err != nil && isNoSuchTable(err):
		return out, nil
	case err != nil:
		return nil, fmt.Errorf("store: inspect %s: %w", what, err)
	}
	defer rows.Close()
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, fmt.Errorf("store: inspect %s: %w", what, err)
		}
		out[k] = v
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: inspect %s: %w", what, err)
	}
	return out, nil
}

// inspectIntCounts runs a GROUP BY query whose second column is a count.
func inspectIntCounts(db *sql.DB, query, what string) (map[string]int, error) {
	out := map[string]int{}
	rows, err := db.Query(query)
	switch {
	case err != nil && isNoSuchTable(err):
		return out, nil
	case err != nil:
		return nil, fmt.Errorf("store: inspect %s: %w", what, err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			k string
			n int
		)
		if err := rows.Scan(&k, &n); err != nil {
			return nil, fmt.Errorf("store: inspect %s: %w", what, err)
		}
		out[k] = n
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: inspect %s: %w", what, err)
	}
	return out, nil
}

// inspectCount runs a single COUNT(*) query, tolerating a database without the
// table.
func inspectCount(db *sql.DB, query, what string) (int, error) {
	var n int
	err := db.QueryRow(query).Scan(&n)
	switch {
	case err != nil && isNoSuchTable(err):
		return 0, nil
	case err != nil:
		return 0, fmt.Errorf("store: inspect %s: %w", what, err)
	}
	return n, nil
}

// inspectHealth reads and orders the health table. The rows are drained into a
// slice before returning: Inspect has one connection, so leaving a cursor open
// across the aggregate queries that follow would deadlock.
func inspectHealth(db *sql.DB) ([]HealthEntry, error) {
	rows, err := db.Query(`SELECT entry, state, detail, at FROM health ORDER BY entry`)
	switch {
	case err != nil && isNoSuchTable(err):
		return nil, nil // no health table yet: a fresh install that has not started
	case err != nil:
		return nil, fmt.Errorf("store: inspect health: %w", err)
	}
	defer rows.Close()
	var out []HealthEntry
	for rows.Next() {
		var (
			e   HealthEntry
			raw string
		)
		if err := rows.Scan(&e.Entry, &e.State, &e.Detail, &raw); err != nil {
			return nil, fmt.Errorf("store: inspect health: %w", err)
		}
		e.At, _ = ParseTime(raw)
		e.Detail = redactText(e.Detail)
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: inspect health: %w", err)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Entry < out[j].Entry })
	return out, nil
}

// HealthEntries returns every health row, ordered by entry. It is the writer's
// read path (inside the process that owns the store); Inspect is the read-only
// path for a second process.
func (s *Store) HealthEntries() ([]HealthEntry, error) {
	rows, err := s.Query(`SELECT entry, state, detail, at FROM health ORDER BY entry`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []HealthEntry
	for rows.Next() {
		var (
			e   HealthEntry
			raw string
		)
		if err := rows.Scan(&e.Entry, &e.State, &e.Detail, &raw); err != nil {
			return nil, err
		}
		e.At, _ = ParseTime(raw)
		e.Detail = redactText(e.Detail)
		out = append(out, e)
	}
	return out, rows.Err()
}

func isNoSuchTable(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "no such table")
}
