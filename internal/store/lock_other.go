//go:build !unix && !windows

package store

// Best effort lock on platforms without flock/LockFileEx: an exclusively created
// marker file. It is not released by the kernel on a crash, so a stale file must
// be removed by hand; the supported desktop targets (linux, darwin, windows) use
// the real implementations next door.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

func acquireLock(dir string) (*fileLock, error) {
	path := filepath.Join(dir, lockFileName)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
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
	l.f.Close()
	os.Remove(l.path)
	l.f = nil
}
