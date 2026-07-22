package platform

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAcquireFileMutexTimesOut(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.mutex")
	release, err := AcquireFileMutex(path, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := release(); err != nil {
			t.Error(err)
		}
	})

	if _, err := AcquireFileMutex(path, 20*time.Millisecond); !errors.Is(err, ErrMutexTimeout) {
		t.Fatalf("second acquisition error = %v, want %v", err, ErrMutexTimeout)
	}
}

func TestReplaceFile(t *testing.T) {
	directory := t.TempDir()
	source := filepath.Join(directory, "source")
	target := filepath.Join(directory, "target")
	if err := os.WriteFile(source, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ReplaceFile(source, target); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "new" {
		t.Fatalf("target contains %q", data)
	}
	if _, err := os.Stat(source); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("source still exists: %v", err)
	}
}
