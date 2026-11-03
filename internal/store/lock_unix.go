//go:build unix

package store

// flock is the unix single-instance lock. It is per open file description, so two
// Open calls in one process conflict exactly like two processes do, which is what
// the tests rely on. The lock is released by the kernel if the process dies, so a
// crash cannot leave a stale "already running" state behind.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

func acquireLock(dir string) (*fileLock, error) {
	path := filepath.Join(dir, lockFileName)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("store: open lock %q: %w", path, err)
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
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
	unix.Flock(int(l.f.Fd()), unix.LOCK_UN)
	l.f.Close()
	l.f = nil
}
