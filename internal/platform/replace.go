package platform

import (
	"errors"
	"os"
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
// publication, a cursor, or a cutover barrier over a condition that was gone
// before anyone could observe it. A genuine permission error still fails, a
// moment later. Platforms whose rename does not have this behaviour report
// nothing transient and take the first answer.
func ReplaceFile(source, target string) error {
	deadline := time.Now().Add(replaceWait)
	for {
		err := replaceFile(source, target)
		if err == nil {
			return nil
		}
		transient := errors.Is(err, os.ErrPermission) || IsTransientFileAccess(err)
		if !transient || time.Now().After(deadline) {
			return err
		}
		time.Sleep(5 * time.Millisecond)
	}
}
