package store

// This file holds the Go-side half of the migrations: the steps SQLite cannot
// express as idempotent SQL (additive columns), plus the data normalization and
// backfill that make an old file look like a fresh one. Everything runs inside the
// migration transaction, on the same single connection as every command.

import (
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/zeokeel/super-agent/internal/domain"
)

// applyV3 upgrades a v2 file (or an M4-era file that already carries the
// schedule/goal tables created by the old EnsureV3) to v3.
func applyV3(s *Store) error {
	// Additive columns. SQLite has no ADD COLUMN IF NOT EXISTS, so ask the
	// catalog first; this is also what makes an M4-era file, whose tables already
	// exist, upgrade without losing rows.
	for _, c := range []struct{ table, column, ddl string }{
		{"schedule", "task", `ALTER TABLE schedule ADD COLUMN task TEXT NOT NULL DEFAULT ''`},
		{"decision", "thread", `ALTER TABLE decision ADD COLUMN thread TEXT`},
		{"outbox", "dropped_at", `ALTER TABLE outbox ADD COLUMN dropped_at TEXT NOT NULL DEFAULT ''`},
		{"outbox", "dropped_reason", `ALTER TABLE outbox ADD COLUMN dropped_reason TEXT NOT NULL DEFAULT ''`},
	} {
		if err := s.addColumn(c.table, c.column, c.ddl); err != nil {
			return err
		}
	}
	if _, err := s.Exec(`CREATE INDEX IF NOT EXISTS decision_by_thread ON decision(thread)`); err != nil {
		return err
	}

	// Canonical, fixed-width timestamps. v1/v2 wrote some columns with
	// time.RFC3339Nano, which drops trailing zeros and therefore sorts wrongly as
	// text: "…:00Z" sorts after "…:00.5Z". Columns that are compared or ordered in
	// SQL are rewritten in place; the tolerant ParseTime keeps the read path
	// working for a file whose upgrade was interrupted.
	for _, c := range []struct {
		table, column string
		nullable      bool
	}{
		{"trigger", "at", false},
		{"thread", "last_at", false},
		{"ticket", "created_at", false},
		{"ticket", "ended_at", true},
		{"outbox", "created_at", false},
		{"outbox", "updated_at", true},
		{"workspace", "created_at", false},
		{"workspace", "updated_at", false},
		{"health", "at", false},
	} {
		if err := normalizeTimeColumn(s, c.table, c.column, c.nullable); err != nil {
			return err
		}
	}

	if err := backfillTriggerThread(s); err != nil {
		return err
	}
	if err := backfillDecisionThread(s); err != nil {
		return err
	}
	if err := backfillOutboxDropped(s); err != nil {
		return err
	}
	// The M4 builds stamped a second version in kv; v3 is the one version truth.
	if _, err := s.Exec(`DELETE FROM kv WHERE k='schema.schedule_goal'`); err != nil {
		return err
	}
	return nil
}

// hasColumn reports whether table has column. Both are constants from this file.
func (s *Store) hasColumn(table, column string) (bool, error) {
	rows, err := s.Query(fmt.Sprintf(`SELECT name FROM pragma_table_info(%q)`, table))
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}

// addColumn runs ddl only when the column is missing.
func (s *Store) addColumn(table, column, ddl string) error {
	ok, err := s.hasColumn(table, column)
	if err != nil {
		return err
	}
	if ok {
		return nil
	}
	_, err = s.Exec(ddl)
	return err
}

// normalizeTimeColumn rewrites one column of one table into FormatTime form,
// leaving unparseable values untouched: a corrupt row must not brick the upgrade,
// and ParseTime's tolerant read keeps it visible instead.
func normalizeTimeColumn(s *Store, table, column string, nullable bool) error {
	// Collect first, update after the rows are closed: the store has one
	// connection, so an UPDATE issued while a read cursor is open would deadlock.
	rows, err := s.Query(fmt.Sprintf(`SELECT rowid, %s FROM %s`, column, table))
	if err != nil {
		return err
	}
	type fix struct {
		rowid int64
		at    string
	}
	var fixes []fix
	for rows.Next() {
		var (
			rowid int64
			raw   sql.NullString
		)
		if err := rows.Scan(&rowid, &raw); err != nil {
			rows.Close()
			return err
		}
		if !raw.Valid || raw.String == "" {
			continue
		}
		t, err := ParseTime(raw.String)
		if err != nil {
			continue
		}
		if canonical := FormatTime(t); canonical != raw.String {
			fixes = append(fixes, fix{rowid: rowid, at: canonical})
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, f := range fixes {
		if _, err := s.Exec(fmt.Sprintf(`UPDATE %s SET %s=? WHERE rowid=?`, table, column), f.at, f.rowid); err != nil {
			return err
		}
	}
	return nil
}

// backfillTriggerThread fills the inverted index from the Trigger rows written
// before the index existed. Triggers are the only source of truth for the pairing,
// so this reads their JSON and rebuilds (thread, id, at, human).
func backfillTriggerThread(s *Store) error {
	rows, err := s.Query(`SELECT json FROM trigger`)
	if err != nil {
		return err
	}
	var payloads []string
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			rows.Close()
			return err
		}
		payloads = append(payloads, payload)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	for _, payload := range payloads {
		var t domain.Trigger
		if err := json.Unmarshal([]byte(payload), &t); err != nil {
			// A Trigger row that cannot be decoded is a broken write from an old
			// build; the timeline skips it rather than failing the upgrade.
			continue
		}
		human := 0
		if t.Human {
			human = 1
		}
		at := FormatTime(t.At)
		for _, ref := range t.Threads {
			thread := string(domain.ThreadKey(t.Scope, ref.Source, ref.ID))
			if _, err := s.Exec(`
				INSERT INTO trigger_thread(thread,id,at,human) VALUES(?,?,?,?)
				ON CONFLICT(thread,id) DO NOTHING`, thread, string(t.ID), at, human); err != nil {
				return err
			}
		}
	}
	return nil
}

// backfillDecisionThread fills decision.thread for Decisions written before the
// column existed. A Decision's id is content-derived from its Trigger
// (domain.Key(Global, "decision", triggerID)), so the Trigger JSON is enough to
// recover which Thread the Decision was about; a Decision with no Trigger row is
// left empty rather than guessed.
func backfillDecisionThread(s *Store) error {
	rows, err := s.Query(`SELECT id, json FROM trigger`)
	if err != nil {
		return err
	}
	type fix struct {
		decisionID string
		thread     string
	}
	var fixes []fix
	for rows.Next() {
		var id, payload string
		if err := rows.Scan(&id, &payload); err != nil {
			rows.Close()
			return err
		}
		var t domain.Trigger
		if err := json.Unmarshal([]byte(payload), &t); err != nil || len(t.Threads) == 0 {
			continue
		}
		fixes = append(fixes, fix{
			decisionID: string(domain.Key(domain.Global, "decision", domain.ID(id))),
			thread:     string(domain.ThreadKey(t.Scope, t.Threads[0].Source, t.Threads[0].ID)),
		})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, f := range fixes {
		if _, err := s.Exec(`UPDATE decision SET thread=? WHERE id=? AND IFNULL(thread,'')=''`,
			f.thread, f.decisionID); err != nil {
			return err
		}
	}
	return nil
}

// backfillOutboxDropped gives a `gone` receipt the dropped_at/dropped_reason an
// audit view needs, using the timestamps the receipt already has.
func backfillOutboxDropped(s *Store) error {
	_, err := s.Exec(`
		UPDATE outbox
		SET dropped_at = CASE WHEN updated_at <> '' THEN updated_at ELSE created_at END,
		    dropped_reason = 'gone'
		WHERE state = 'gone' AND dropped_at = ''`)
	return err
}
