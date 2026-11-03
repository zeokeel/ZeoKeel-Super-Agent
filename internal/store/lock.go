package store

// The state directory may be opened by exactly one writer per process/host. The
// lock lives on a file in the directory rather than on the database file so a
// read-only Inspect (which needs no lock) and a running app can coexist, and so a
// second `zeokeel` invocation fails with "already running" instead of corrupting
// the WAL. Platform-specific code is in lock_<goos>.go: flock on unix,
// LockFileEx on windows, an exclusive-create file elsewhere.

import "os"

// lockFileName is the state directory's single-instance lock.
const lockFileName = ".zeokeel.lock"

// fileLock is an acquired state-directory lock. A nil *fileLock is valid and
// does nothing, which is what a platform without advisory locks degrades to.
type fileLock struct {
	f    *os.File
	path string
}
