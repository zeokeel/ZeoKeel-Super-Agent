//go:build !windows

package browser

import (
	"os"
	"syscall"
)

// terminateSignal is SIGTERM: browsers close cleanly, preserving the profile.
func terminateSignal() os.Signal { return syscall.SIGTERM }
