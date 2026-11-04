//go:build windows

package browser

import "os"

// terminateSignal on Windows: os.Interrupt is the closest graceful equivalent;
// Kill is used if the process does not exit within StopTimeout.
func terminateSignal() os.Signal { return os.Interrupt }
