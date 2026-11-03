package runtime

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/zeokeel/super-agent/internal/view"
)

// SetStateDir tells the runtime where the database and session logs live, so the
// storage card can report real sizes instead of a dash.
func (r *Runtime) SetStateDir(dir string) {
	r.mu.Lock()
	r.stateDir = dir
	r.storageAt = time.Time{}
	r.mu.Unlock()
}

// storageInfo counts what is on disk. It walks the session directories, so it is
// cached: the projector runs every couple of seconds and walking on every frame
// would make the panel the heaviest thing on the machine.
func (r *Runtime) storageInfo(now time.Time) view.Storage {
	r.mu.Lock()
	dir := r.stateDir
	cached := r.storageCached
	at := r.storageAt
	r.mu.Unlock()

	if dir == "" {
		// Unknown is shown as unknown, never as zero: "0 条会话事件" on an install
		// that has been running for a month is a false statement.
		return view.Storage{DB: "—", Events: -1, LastBackup: "—"}
	}
	// Three seconds is short enough that a session file appears almost immediately
	// and long enough that a burst of publishes does not walk the tree each time.
	if !at.IsZero() && now.Sub(at) < 3*time.Second {
		return cached
	}

	out := view.Storage{DB: "—", LastBackup: "—"}
	if info, err := os.Stat(filepath.Join(dir, "zeokeel.db")); err == nil {
		out.DB = humanSize(info.Size())
	}
	out.LastBackup = latestBackup(filepath.Join(dir, "backups"))
	out.Events = countSessionEvents(filepath.Join(dir, "work"))

	r.mu.Lock()
	r.storageCached, r.storageAt = out, now
	r.mu.Unlock()
	return out
}

// countSessionEvents counts EventSeq files the agent wrote. One file per session
// turn is enough to answer "is it recording?" without reading their contents.
func countSessionEvents(workRoot string) int {
	entries, err := os.ReadDir(workRoot)
	if err != nil {
		return 0
	}
	total := 0
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), "ticket-") {
			continue
		}
		sessions := filepath.Join(workRoot, e.Name(), ".zeokeel", "sessions")
		files, err := os.ReadDir(sessions)
		if err != nil {
			continue
		}
		for _, f := range files {
			if !f.IsDir() {
				total++
			}
		}
	}
	return total
}

// latestBackup names the newest `zeokeel backup` file, so the storage card says
// when the last copy was made instead of always showing a dash. The backup
// command writes <stateDir>/backups/zeokeel-<date>.db.
func latestBackup(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "—"
	}
	var newest time.Time
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if newest.IsZero() || info.ModTime().After(newest) {
			newest = info.ModTime()
		}
	}
	if newest.IsZero() {
		return "—"
	}
	return newest.Format("2006-01-02 15:04")
}

func humanSize(n int64) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	case n < 1024*1024*1024:
		return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
	}
	return fmt.Sprintf("%.1f GB", float64(n)/(1024*1024*1024))
}
