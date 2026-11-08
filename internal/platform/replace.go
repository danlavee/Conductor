package platform

import (
	"errors"
	"os"
	"path/filepath"
	"time"
)

// replaceWait bounds how long an atomic replacement waits out an operating
// system that reports the target momentarily untouchable.
const replaceWait = 3 * time.Second

// ReplaceFile atomically replaces target with source, waiting out a target the
// operating system reports as momentarily untouchable. Windows answers a file
// another process has open, or one already deleted but not yet released, with a
// sharing violation or an access denial rather than a queued rename; both clear
// on their own in milliseconds. Every caller here is publishing state a reader
// may hold open at that instant, and treating that as a failed write loses a
// publication or a cursor over a condition that was gone
// before anyone could observe it. A genuine permission error still fails, a
// moment later. Platforms whose rename does not have this behaviour report
// nothing transient and take the first answer.
func ReplaceFile(source, target string) error {
	return waitOut(func() error { return replaceFile(source, target) })
}

// Rename moves a directory into a free path, waiting out the same transient
// answers: a tree just written is briefly held open by whatever scans new files.
func Rename(source, target string) error {
	return waitOut(func() error { return os.Rename(source, target) })
}

// Open opens a file for reading, waiting out the same transient answers: a
// reader that arrives while ReplaceFile holds the file is refused, not queued.
// Every file published by replacement is read through here or ReadFile.
func Open(path string) (*os.File, error) {
	var file *os.File
	err := waitOut(func() (err error) {
		file, err = os.Open(path)
		return err
	})
	return file, err
}

// ReadFile reads a whole file the way Open opens it.
func ReadFile(path string) ([]byte, error) {
	var data []byte
	err := waitOut(func() (err error) {
		data, err = os.ReadFile(path)
		return err
	})
	return data, err
}

// WriteFile publishes data at path by atomic replacement, so a reader sees the
// old file or the new one and never a partial one.
func WriteFile(path string, data []byte) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(directory, ".conductor-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	_, err = temp.Write(data)
	if err == nil {
		err = temp.Sync()
	}
	if closeErr := temp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return ReplaceFile(temp.Name(), path)
}

// Remove deletes path the way ReplaceFile replaces it, waiting out a reader
// that holds the file open, and persists the removal. Absence is success.
func Remove(path string) error {
	err := waitOut(func() error { return os.Remove(path) })
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return SyncParent(path)
}

// waitOut retries only what Windows reports transiently; elsewhere a
// permission error is genuine and answers at once.
func waitOut(attempt func() error) error {
	deadline := time.Now().Add(replaceWait)
	for {
		err := attempt()
		if err == nil {
			return nil
		}
		transient := isTransientFileAccess(err) || IsDeletePending(err)
		if !transient || time.Now().After(deadline) {
			return err
		}
		time.Sleep(5 * time.Millisecond)
	}
}
