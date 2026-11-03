package main

// File logging for the long-running modes: slog JSON to
// <stateDir>/logs/zeokeel.log, rotated at local midnight, keeping 14 days
// (006 deployment, design/ops-runbook.md §2.2). Demo and pipeline runs keep
// stderr only, so a walkthrough never writes into the user's state directory.

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	logDirName  = "logs"
	logFileName = "zeokeel.log"
	logKeepDays = 14
)

// setupLogging returns the process logger and a close function. It never fails the
// app: a log file that cannot be opened falls back to stderr, because losing logs
// is bad but losing the window is worse.
func setupLogging(stateDir string, toFile, inMemory bool) (*slog.Logger, func()) {
	opts := &slog.HandlerOptions{Level: slog.LevelInfo}
	if toFile && !inMemory && stateDir != "" {
		if w, err := newRotatingLog(filepath.Join(stateDir, logDirName), time.Now); err == nil {
			return slog.New(slog.NewJSONHandler(w, opts)), func() { _ = w.Close() }
		}
	}
	return slog.New(slog.NewTextHandler(os.Stderr, opts)), func() {}
}

// rotatingLog is a daily-rotating file writer. Writes go to zeokeel.log; when the
// local day changes the old file is renamed to zeokeel-<date>.log and files older
// than logKeepDays are removed. Rotation is lazy (checked on write), which is
// enough for a desktop app that logs on activity.
type rotatingLog struct {
	mu   sync.Mutex
	dir  string
	now  func() time.Time
	day  string
	file *os.File
}

func newRotatingLog(dir string, now func() time.Time) (*rotatingLog, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	r := &rotatingLog{dir: dir, now: now}
	if err := r.open(); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *rotatingLog) currentPath() string { return filepath.Join(r.dir, logFileName) }

func (r *rotatingLog) open() error {
	f, err := os.OpenFile(r.currentPath(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if r.file != nil {
		_ = r.file.Close()
	}
	r.file = f
	r.day = r.now().Format("2006-01-02")
	return nil
}

func (r *rotatingLog) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	if day := now.Format("2006-01-02"); day != r.day {
		r.rotate(day)
	}
	if r.file == nil {
		return 0, fmt.Errorf("rotating log: not open")
	}
	return r.file.Write(p)
}

// rotate renames the current file to its dated name and opens a fresh one. A
// failed reopen leaves the old file in place; the next write tries again.
func (r *rotatingLog) rotate(day string) {
	if r.file != nil {
		_ = r.file.Close()
	}
	_ = os.Rename(r.currentPath(), filepath.Join(r.dir, "zeokeel-"+r.day+".log"))
	if err := r.open(); err != nil {
		r.file = nil
	}
	r.day = day
	r.prune()
}

// prune deletes dated logs older than logKeepDays. It only touches files that
// match our own name pattern, so a state directory shared with other tools is safe.
func (r *rotatingLog) prune() {
	entries, err := os.ReadDir(r.dir)
	if err != nil {
		return
	}
	cutoff := r.now().AddDate(0, 0, -logKeepDays)
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasPrefix(name, "zeokeel-") || !strings.HasSuffix(name, ".log") {
			continue
		}
		day := strings.TrimSuffix(strings.TrimPrefix(name, "zeokeel-"), ".log")
		at, err := time.ParseInLocation("2006-01-02", day, r.now().Location())
		if err != nil {
			continue
		}
		if at.Before(cutoff) {
			_ = os.Remove(filepath.Join(r.dir, name))
		}
	}
}

// Close flushes and closes the file.
func (r *rotatingLog) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.file == nil {
		return nil
	}
	err := r.file.Sync()
	_ = r.file.Close()
	r.file = nil
	return err
}
