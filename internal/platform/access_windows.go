//go:build windows

package platform

import (
	"errors"
	"syscall"
)

const (
	errorAccessDenied     syscall.Errno = 5
	errorSharingViolation syscall.Errno = 32
	errorLockViolation    syscall.Errno = 33
)

// isTransientFileAccess reports a Windows sharing conflict during atomic publication.
func isTransientFileAccess(err error) bool {
	return errors.Is(err, errorSharingViolation) || errors.Is(err, errorLockViolation)
}

// IsDeletePending reports the answer Windows gives for a removed file that
// another handle still holds open: access denied, until the handle closes and
// the file is gone.
func IsDeletePending(err error) bool {
	return errors.Is(err, errorAccessDenied)
}
