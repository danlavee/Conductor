//go:build !windows

package platform

import (
	"os"
	"path/filepath"
)

// replaceFile atomically replaces target with source and syncs its directory.
func replaceFile(source, target string) error {
	if err := os.Rename(source, target); err != nil {
		return err
	}
	return SyncParent(target)
}

// SyncParent persists directory metadata for path.
func SyncParent(path string) error {
	directory, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
