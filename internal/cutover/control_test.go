package cutover

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFreezeClosesAdmissionAndDrainsInflightOperation(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	release, err := Admit(root)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	finished := make(chan error, 1)
	validated := make(chan struct{}, 1)
	go func() {
		_, err := Freeze(root, "cut-1", "v5", func() error {
			validated <- struct{}{}
			return nil
		})
		finished <- err
	}()
	deadline := time.Now().Add(2 * time.Second)
	for {
		current, _, err := Observe(root)
		if err != nil {
			t.Fatal(err)
		}
		if current.Phase == Freezing {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("freeze did not close admission")
		}
		time.Sleep(time.Millisecond)
	}
	if _, err := Admit(root); err == nil {
		t.Fatal("writer admitted after freeze closed admission")
	} else {
		var blocked *BlockedError
		if !errors.As(err, &blocked) {
			t.Fatalf("admission error = %v", err)
		}
	}
	select {
	case <-validated:
		t.Fatal("freeze barrier completed before in-flight operation")
	default:
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("freeze did not drain")
	}
}

func TestFreezeValidationFailureSafelyAbortsBeforeReplacement(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	sentinel := errors.New("open transaction")
	state, err := Freeze(root, "cut-1", "v5", func() error { return sentinel })
	if !errors.Is(err, sentinel) {
		t.Fatalf("freeze error = %v", err)
	}
	if state.Phase != Active {
		t.Fatalf("phase = %s", state.Phase)
	}
	release, err := Admit(root)
	if err != nil {
		t.Fatalf("admission was not restored: %v", err)
	}
	_ = release()
}

func TestReplacementIsResumableButCannotBeAborted(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	if _, err := Freeze(root, "cut-1", "v5", nil); err != nil {
		t.Fatal(err)
	}
	replaced, err := MarkReplaced(root, "cut-1")
	if err != nil {
		t.Fatal(err)
	}
	if repeated, err := MarkReplaced(root, "cut-1"); err != nil || repeated != replaced {
		t.Fatalf("second replace = %#v, %v", repeated, err)
	}
	if _, err := Abort(root, "cut-1"); err == nil {
		t.Fatal("replacement was abortable")
	}
	active, err := Activate(root, "cut-1")
	if err != nil {
		t.Fatal(err)
	}
	if active.Phase != Active || active.Generation != replaced.Generation+1 || active.LastCutoverID != "cut-1" {
		t.Fatalf("active state = %#v", active)
	}
	if repeated, err := Activate(root, "cut-1"); err != nil || repeated != active {
		t.Fatalf("second activate = %#v, %v", repeated, err)
	}
}

func TestCorruptControlStateFailsClosed(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	dir, err := Directory(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Admit(root); err == nil {
		t.Fatal("corrupt control state admitted an operation")
	}
}

func TestMissingInitializedControlStateFailsEveryNewOperationClosed(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	release, err := Admit(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	dir, err := Directory(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "state.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := Admit(root); err == nil || err.Error() != "cutover state is missing" {
		t.Fatalf("admission error = %v", err)
	}
}
