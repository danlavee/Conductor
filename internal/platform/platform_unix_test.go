//go:build !windows

package platform

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Outside Windows a permission error is never transient, so it must answer at
// once rather than after the wait reserved for Windows sharing conflicts.
func TestAPermissionDenialIsNotWaitedOut(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root is not denied")
	}
	directory := t.TempDir()
	path := filepath.Join(directory, "target")
	if err := os.WriteFile(path, []byte("v"), 0o000); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(directory, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(directory, 0o700) })
	for name, attempt := range map[string]func() error{
		"remove": func() error { return Remove(path) },
		"read":   func() error { _, err := ReadFile(path); return err },
	} {
		started := time.Now()
		if err := attempt(); !os.IsPermission(err) {
			t.Fatalf("%s error = %v, want a permission denial", name, err)
		}
		if elapsed := time.Since(started); elapsed > replaceWait/2 {
			t.Fatalf("%s waited %v on a genuine denial", name, elapsed)
		}
	}
}
