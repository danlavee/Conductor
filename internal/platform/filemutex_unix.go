//go:build !windows

package platform

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"time"
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
		err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() error {
				unlockErr := syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
				if closeErr := file.Close(); unlockErr == nil {
					unlockErr = closeErr
				}
				return unlockErr
			}, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			file.Close()
			return nil, err
		}
		if time.Now().After(deadline) {
			file.Close()
			return nil, ErrMutexTimeout
		}
		time.Sleep(5 * time.Millisecond)
	}
}
