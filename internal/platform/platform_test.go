package platform

import (
	"errors"
	"os"
	"os/exec"
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

// A process is only ever judged alive by its start token, and one whose token
// cannot be read is refused at the moment it would be recorded.
func TestProcessAlive(t *testing.T) {
	self := os.Getpid()
	process, err := ProcessOf(self)
	if err != nil {
		t.Fatal(err)
	}
	token := process.Start
	exited := exec.Command(os.Args[0], "-test.run=^$")
	if err := exited.Run(); err != nil {
		t.Fatal(err)
	}
	if recorded, err := ProcessOf(exited.Process.Pid); err == nil {
		t.Errorf("an exited process was recorded as %+v", recorded)
	}
	for name, test := range map[string]struct {
		pid   int
		start string
		want  bool
	}{
		"own token":      {self, token, true},
		"no token":       {self, "", false},
		"other token":    {self, "not-this-process", false},
		"no pid":         {0, token, false},
		"exited process": {exited.Process.Pid, token, false},
	} {
		if got := (Process{PID: test.pid, Start: test.start}).Alive(); got != test.want {
			t.Errorf("%s: Alive(%d, %q) = %v, want %v", name, test.pid, test.start, got, test.want)
		}
	}
}

func TestProcessRoundTrips(t *testing.T) {
	process, err := ProcessOf(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if parsed, err := ParseProcess(process.String()); err != nil || parsed != process {
		t.Fatalf("parsed %+v, %v; want %+v", parsed, err, process)
	}
	for _, bad := range []string{"", "12", "12:", "x:y", "0:y"} {
		if _, err := ParseProcess(bad); err == nil {
			t.Errorf("ParseProcess(%q) succeeded", bad)
		}
	}
}

// WriteFile creates its directory, replaces whole, and leaves no temporary
// file behind for a directory listing to trip over.
func TestWriteFileReplacesWhole(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "nested")
	path := filepath.Join(directory, "target")
	for _, content := range []string{"first", "second"} {
		if err := WriteFile(path, []byte(content)); err != nil {
			t.Fatal(err)
		}
		if data, err := os.ReadFile(path); err != nil || string(data) != content {
			t.Fatalf("target = %q, %v; want %q", data, err, content)
		}
	}
	if entries, err := os.ReadDir(directory); err != nil || len(entries) != 1 {
		t.Fatalf("directory holds %v, %v; want only the target", entries, err)
	}
}

// A reader holding the file open is a moment's obstacle, not a failed removal;
// a file already gone is a removal already done.
func TestRemoveWaitsOutAnOpenReader(t *testing.T) {
	path := filepath.Join(t.TempDir(), "target")
	if err := os.WriteFile(path, []byte("v"), 0o600); err != nil {
		t.Fatal(err)
	}
	reader, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(100 * time.Millisecond)
		reader.Close()
	}()
	if err := Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("removed file still exists: %v", err)
	}
	if err := Remove(path); err != nil {
		t.Fatalf("removing an absent file = %v", err)
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
		for time.Now().Before(stop) {
			if err := WriteFile(target, []byte("v")); err != nil {
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
