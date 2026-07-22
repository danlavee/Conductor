//go:build !windows && !linux

package platform

import (
	"errors"
	"syscall"
)

// ProcessAlive reports whether pid identifies a live process.
func ProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// ProcessStartToken reports that process-instance metadata is unavailable.
func ProcessStartToken(int) (string, bool) { return "", false }
