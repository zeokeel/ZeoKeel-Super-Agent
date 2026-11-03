package main

// `zeokeel backup` and `zeokeel archive` are operator commands. Both need the
// state directory's write lock, so both refuse to run while the app is up and say
// so instead of corrupting a live WAL. Backup is VACUUM INTO; archive moves old
// ended Tickets (and their Triggers/Dispositions) into archive.db through an
// ATTACH + INSERT + DELETE transaction, keeping the outbox whose ids are the
// delivery idempotency receipts.

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/zeokeel/super-agent/internal/domain"
	"github.com/zeokeel/super-agent/internal/runtime"
	"github.com/zeokeel/super-agent/internal/store"
)

// openWriter opens the state directory for a maintenance command, or explains why
// it cannot. A running app is a restart-fixable condition, not a component error.
func openWriter(stateDir string) (*store.Store, int, error) {
	if stateDir == "" {
		return nil, runtime.ExitComponent, fmt.Errorf("没有状态目录")
	}
	dbPath := filepath.Join(stateDir, "zeokeel.db")
	if _, err := os.Stat(dbPath); err != nil {
		return nil, runtime.ExitComponent, fmt.Errorf("状态目录里没有 zeokeel.db：%w", err)
	}
	st, err := store.Open(dbPath)
	if err == nil {
		return st, 0, nil
	}
	if errors.Is(err, store.ErrAlreadyRunning) {
		return nil, runtime.ExitRestart, fmt.Errorf("另一个 zeokeel 正在使用该状态目录：请先退出应用（zeokeel status 确认）再执行该命令")
	}
	return nil, runtime.ExitComponent, err
}

// cmdBackup writes a consistent copy with VACUUM INTO. The copy is a single file
// (the WAL is folded in), so it can be restored by copying it back.
func cmdBackup(args []string) int {
	fs := flag.NewFlagSet("backup", flag.ContinueOnError)
	stateDir := fs.String("state-dir", defaultStateDir(), "where the database lives")
	out := fs.String("out", "", "destination file (default <stateDir>/backups/zeokeel-<date>.db)")
	if err := fs.Parse(args); err != nil {
		return runtime.ExitComponent
	}
	st, code, err := openWriter(*stateDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "zeokeel backup:", err)
		return code
	}
	defer st.Close()

	dst := *out
	if dst == "" {
		dst = filepath.Join(*stateDir, "backups", "zeokeel-"+time.Now().Format("2006-01-02")+".db")
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		fmt.Fprintln(os.Stderr, "zeokeel backup:", err)
		return runtime.ExitComponent
	}
	// VACUUM INTO takes a literal path (SQLite does not bind it as a parameter).
	if _, err := st.DB().Exec(`VACUUM INTO '` + sqlString(dst) + `'`); err != nil {
		fmt.Fprintln(os.Stderr, "zeokeel backup:", err)
		return runtime.ExitComponent
	}
	if err := os.Chmod(dst, 0o600); err != nil {
		fmt.Fprintln(os.Stderr, "zeokeel backup: chmod:", err)
	}
	fmt.Printf("备份完成：%s\n（数据库里有明文 API Key，备份文件按密钥保管，chmod 600）\n", dst)
	return runtime.ExitOK
}

// cmdArchive moves ended Tickets older than --before into archive.db. Rows are
// copied first and deleted only in the same transaction, so a crash leaves the
// data in exactly one file.
func cmdArchive(args []string) int {
	fs := flag.NewFlagSet("archive", flag.ContinueOnError)
	stateDir := fs.String("state-dir", defaultStateDir(), "where the database lives")
	before := fs.String("before", "180d", "move Tickets ended longer ago than this (e.g. 180d, 720h)")
	dry := fs.Bool("dry-run", false, "report what would move, change nothing")
	if err := fs.Parse(args); err != nil {
		return runtime.ExitComponent
	}
	age, err := parseAge(*before)
	if err != nil {
		fmt.Fprintln(os.Stderr, "zeokeel archive:", err)
		return runtime.ExitComponent
	}
	st, code, err := openWriter(*stateDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "zeokeel archive:", err)
		return code
	}
	defer st.Close()

	cutoff := time.Now().Add(-age)
	cands, origins, err := archiveCandidates(st, cutoff)
	if err != nil {
		fmt.Fprintln(os.Stderr, "zeokeel archive:", err)
		return runtime.ExitComponent
	}
	if len(cands) == 0 {
		fmt.Printf("没有需要归档的工单（早于 %s 结束的 0 条）\n", cutoff.Format("2006-01-02"))
		return runtime.ExitOK
	}
	if *dry {
		fmt.Printf("将归档 %d 个工单、%d 个 Trigger：\n", len(cands), len(origins))
		for _, c := range cands {
			fmt.Printf("  #%d %s（结束于 %s）\n", c.Number, c.Title, c.EndedAt.Format("2006-01-02"))
		}
		return runtime.ExitOK
	}

	archivePath := filepath.Join(*stateDir, "archive.db")
	if _, err := st.DB().Exec(`ATTACH DATABASE '` + sqlString(archivePath) + `' AS archive`); err != nil {
		fmt.Fprintln(os.Stderr, "zeokeel archive:", err)
		return runtime.ExitComponent
	}
	defer func() { _, _ = st.DB().Exec(`DETACH DATABASE archive`) }()

	// Copy the live schema shape into archive.db once, instead of hand-writing the
	// DDL here (the store owns the schema; this must not drift from it).
	for _, table := range []string{"ticket", "trigger", "disposition"} {
		if _, err := st.DB().Exec(`CREATE TABLE IF NOT EXISTS archive.` + table +
			` AS SELECT * FROM main.` + table + ` WHERE 0`); err != nil {
			fmt.Fprintln(os.Stderr, "zeokeel archive: create archive schema:", err)
			return runtime.ExitComponent
		}
	}

	if err := st.Atomic(func() error {
		for _, c := range cands {
			if _, err := st.Exec(`INSERT INTO archive.ticket SELECT * FROM main.ticket WHERE id=?`, string(c.ID)); err != nil {
				return err
			}
			if _, err := st.Exec(`DELETE FROM main.ticket WHERE id=?`, string(c.ID)); err != nil {
				return err
			}
		}
		for _, id := range origins {
			if _, err := st.Exec(`INSERT INTO archive.trigger SELECT * FROM main.trigger WHERE id=?`, string(id)); err != nil {
				return err
			}
			if _, err := st.Exec(`INSERT INTO archive.disposition SELECT * FROM main.disposition WHERE trigger_id=?`, string(id)); err != nil {
				return err
			}
			if _, err := st.Exec(`DELETE FROM main.disposition WHERE trigger_id=?`, string(id)); err != nil {
				return err
			}
			if _, err := st.Exec(`DELETE FROM main.trigger WHERE id=?`, string(id)); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		fmt.Fprintln(os.Stderr, "zeokeel archive:", err)
		return runtime.ExitComponent
	}
	fmt.Printf("归档完成：%d 个工单、%d 个 Trigger → %s\n", len(cands), len(origins), archivePath)
	fmt.Println("outbox（投递幂等回执）保持在主库中，不随工单归档。")
	return runtime.ExitOK
}

type archivedTicket struct {
	ID      domain.ID
	Number  int
	Title   string
	EndedAt time.Time
}

// archiveCandidates reads the Tickets that ended before cutoff from their JSON
// (the table columns are a cache; the JSON is the row). Origins are the Triggers
// to move with them; Triggers still referenced by a live Ticket are not moved.
func archiveCandidates(st *store.Store, cutoff time.Time) ([]archivedTicket, []domain.ID, error) {
	rows, err := st.Query(`SELECT json FROM ticket WHERE ended_at IS NOT NULL AND ended_at < ?`,
		store.FormatTime(cutoff))
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var (
		out     []archivedTicket
		origins []domain.ID
	)
	seen := map[domain.ID]bool{}
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, nil, err
		}
		var tk domain.Ticket
		if err := json.Unmarshal([]byte(raw), &tk); err != nil {
			continue // an unreadable row must not block the archive of others
		}
		if tk.Ended == nil {
			continue
		}
		out = append(out, archivedTicket{ID: tk.ID, Number: tk.Number, Title: tk.Title, EndedAt: tk.Ended.At})
		if tk.Origin != "" && !seen[tk.Origin] {
			seen[tk.Origin] = true
			origins = append(origins, tk.Origin)
		}
	}
	return out, origins, rows.Err()
}

// sqlString quotes a string for SQLite: only single quotes are special, and
// doubling them is the documented escape.
func sqlString(s string) string {
	out := make([]byte, 0, len(s)+2)
	for i := 0; i < len(s); i++ {
		if s[i] == '\'' {
			out = append(out, '\'', '\'')
			continue
		}
		out = append(out, s[i])
	}
	return string(out)
}

// parseAge accepts Go durations plus a day suffix ("180d"), which
// time.ParseDuration does not.
func parseAge(raw string) (time.Duration, error) {
	if n := len(raw); n > 1 && raw[n-1] == 'd' {
		var days int
		if _, err := fmt.Sscanf(raw[:n-1], "%d", &days); err == nil && days > 0 {
			return time.Duration(days) * 24 * time.Hour, nil
		}
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("看不懂 --before %q（用 180d 或 720h）", raw)
	}
	return d, nil
}
