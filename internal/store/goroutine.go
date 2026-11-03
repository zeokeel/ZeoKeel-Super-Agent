package store

// The store needs to tell its command goroutine (the one that owns the open
// transaction) from every other goroutine. Go has no goroutine-local storage, and
// handing a *sql.Tx to another goroutine is exactly the bug this file's callers
// must not have: a background reader would join a command's transaction, and a
// second command would run inside the first one. The store therefore identifies
// its owner by goroutine id.
//
// runtime.Stack(_, false) during an open transaction is the only standard way to
// ask; it costs a few microseconds, and it is only read while a command
// transaction is open (i.e. at most once per statement of one command).

import (
	"runtime"
	"strconv"
	"strings"
)

// goroutineID returns the current goroutine's id. The format of the first line of
// runtime.Stack is stable: "goroutine 18 [running]:".
func goroutineID() uint64 {
	var buf [64]byte
	n := runtime.Stack(buf[:], false)
	s := strings.TrimPrefix(string(buf[:n]), "goroutine ")
	if i := strings.IndexByte(s, ' '); i > 0 {
		s = s[:i]
	}
	id, _ := strconv.ParseUint(s, 10, 64)
	return id
}
