//go:build windows

package store

// LockFileEx is the windows single-instance lock: a byte-range lock on the lock
// file. The lock is released when the handle closes (including on process death),
// so a crash cannot leave a stale "already running" state behind.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

func acquireLock(dir string) (*fileLock, error) {
	path := filepath.Join(dir, lockFileName)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("store: open lock %q: %w", path, err)
	}
	var overlapped windows.Overlapped
	err = windows.LockFileEx(windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0, 1, 0, &overlapped)
	if err != nil {
		f.Close()
		if errors.Is(err, windows.ERROR_LOCK_VIOLATION) || errors.Is(err, windows.ERROR_IO_PENDING) {
			return nil, fmt.Errorf("%w (lock %s)", ErrAlreadyRunning, path)
		}
		return nil, fmt.Errorf("store: lock %q: %w", path, err)
	}
	return &fileLock{f: f, path: path}, nil
}

func (l *fileLock) release() {
	if l == nil || l.f == nil {
		return
	}
	var overlapped windows.Overlapped
	windows.UnlockFileEx(windows.Handle(l.f.Fd()), 0, 1, 0, &overlapped)
	l.f.Close()
	l.f = nil
}
