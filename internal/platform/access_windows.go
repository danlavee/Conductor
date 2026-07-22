//go:build windows

package platform

import (
	"errors"
	"syscall"
)

const (
	errorSharingViolation syscall.Errno = 32
	errorLockViolation    syscall.Errno = 33
)

// IsTransientFileAccess reports a Windows sharing conflict during atomic publication.
func IsTransientFileAccess(err error) bool {
	return errors.Is(err, errorSharingViolation) || errors.Is(err, errorLockViolation)
}
