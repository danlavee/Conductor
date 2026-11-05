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

// TestReadFileWaitsOutAReplacement reads a file while another goroutine keeps
// replacing it: every read must see one whole version, never a refusal.
func TestReadFileWaitsOutAReplacement(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "target")
	if err := os.WriteFile(target, []byte("v"), 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error)
	stop := time.Now().Add(500 * time.Millisecond)
	go func() {
		for i := 0; time.Now().Before(stop); i++ {
			source := filepath.Join(directory, "source")
			if err := os.WriteFile(source, []byte("v"), 0o600); err != nil {
				done <- err
				return
			}
			if err := ReplaceFile(source, target); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	for time.Now().Before(stop) {
		data, err := ReadFile(target)
		if err != nil || string(data) != "v" {
			t.Errorf("read during replacement = %q, %v", data, err)
			break
		}
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
