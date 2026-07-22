//go:build windows

package platform

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"time"
	"unsafe"
)

var (
	lockFileEx   = syscall.NewLazyDLL("kernel32.dll").NewProc("LockFileEx")
	unlockFileEx = syscall.NewLazyDLL("kernel32.dll").NewProc("UnlockFileEx")
)

// AcquireFileMutex acquires an exclusive crash-released mutex within timeout.
func AcquireFileMutex(path string, timeout time.Duration) (func() error, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(timeout)
	for {
		var overlapped syscall.Overlapped
		const exclusiveNoWait = 0x00000002 | 0x00000001
		ok, _, callErr := lockFileEx.Call(
			uintptr(file.Fd()), exclusiveNoWait, 0, 1, 0,
			uintptr(unsafe.Pointer(&overlapped)),
		)
		if ok != 0 {
			return func() error {
				var unlockOverlapped syscall.Overlapped
				ok, _, unlockErr := unlockFileEx.Call(
					uintptr(file.Fd()), 0, 1, 0,
					uintptr(unsafe.Pointer(&unlockOverlapped)),
				)
				if ok == 0 && unlockErr != nil && !errors.Is(unlockErr, syscall.Errno(0)) {
					file.Close()
					return unlockErr
				}
				return file.Close()
			}, nil
		}
		const errorLockViolation = syscall.Errno(33)
		if callErr != nil && !errors.Is(callErr, errorLockViolation) && !errors.Is(callErr, syscall.Errno(0)) {
			file.Close()
			return nil, callErr
		}
		if time.Now().After(deadline) {
			file.Close()
			return nil, ErrMutexTimeout
		}
		time.Sleep(5 * time.Millisecond)
	}
}
