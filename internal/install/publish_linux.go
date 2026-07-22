//go:build linux

package install

import (
	"errors"

	"golang.org/x/sys/unix"
)

func publishNoReplace(source, destination string) error {
	err := unix.Renameat2(unix.AT_FDCWD, source, unix.AT_FDCWD, destination, unix.RENAME_NOREPLACE)
	if errors.Is(err, unix.EEXIST) {
		return errDestinationExists
	}
	return err
}
