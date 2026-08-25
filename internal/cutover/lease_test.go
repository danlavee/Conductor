package cutover

import (
	"path/filepath"
	"testing"
	"time"
)

func backdateLease(t *testing.T, root string) {
	t.Helper()
	dir, err := Directory(root)
	if err != nil {
		t.Fatal(err)
	}
	current, _, err := Observe(root)
	if err != nil {
		t.Fatal(err)
	}
	current.ExpiresAt = time.Now().UTC().Add(-time.Second)
	if err := writeState(dir, current); err != nil {
		t.Fatal(err)
	}
}

func TestAbandonedFreezeExpiresBackToAdmission(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	frozen, err := Freeze(root, "cut-1", "v5", nil)
	if err != nil {
		t.Fatal(err)
	}
	if frozen.ExpiresAt.IsZero() {
		t.Fatal("freeze left no lease to expire")
	}
	if _, err := Admit(root); err == nil {
		t.Fatal("a live barrier admitted an operation")
	}
	backdateLease(t, root)
	release, err := Admit(root)
	if err != nil {
		t.Fatalf("an expired barrier still blocked admission: %v", err)
	}
	defer release()
	current, _, err := Observe(root)
	if err != nil {
		t.Fatal(err)
	}
	if current.Phase != Active || current.Generation != frozen.Generation {
		t.Fatalf("reclaimed state = %#v", current)
	}
	if current.CutoverID != "" {
		t.Fatalf("reclaimed state kept a cutover identity: %#v", current)
	}
}

func TestExpiredFreezeNoLongerAuthorizesReplacement(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	if _, err := Freeze(root, "cut-1", "v5", nil); err != nil {
		t.Fatal(err)
	}
	frozen, err := IsFrozen(root)
	if err != nil {
		t.Fatal(err)
	}
	if !frozen {
		t.Fatal("a completed freeze did not authorize replacement")
	}
	backdateLease(t, root)
	frozen, err = IsFrozen(root)
	if err != nil {
		t.Fatal(err)
	}
	if frozen {
		t.Fatal("an expired barrier still authorized replacement")
	}
	if _, err := Freeze(root, "cut-2", "v6", nil); err != nil {
		t.Fatalf("a fresh cutover could not take an expired root: %v", err)
	}
}

func TestReplacedBarrierCarriesNoLease(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	if _, err := Freeze(root, "cut-1", "v5", nil); err != nil {
		t.Fatal(err)
	}
	replaced, err := MarkReplaced(root, "cut-1")
	if err != nil {
		t.Fatal(err)
	}
	if !replaced.ExpiresAt.IsZero() {
		t.Fatalf("replacement kept a lease: %#v", replaced)
	}
	// Replacement has already swapped the trees, so a deadline found on a
	// replaced root must not reopen admission behind the cutover's back.
	dir, err := Directory(root)
	if err != nil {
		t.Fatal(err)
	}
	replaced.ExpiresAt = time.Now().UTC().Add(-time.Hour)
	if err := writeState(dir, replaced); err != nil {
		t.Fatal(err)
	}
	if _, err := Admit(root); err == nil {
		t.Fatal("a replaced root admitted an operation")
	}
}

func TestExpiredBarrierIsNotReclaimedWhileHeld(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	if _, err := Freeze(root, "cut-1", "v5", nil); err != nil {
		t.Fatal(err)
	}
	releaseBarrier, err := HoldBarrier(root)
	if err != nil {
		t.Fatal(err)
	}
	backdateLease(t, root)
	// The lease has lapsed, but a replacement is running behind the barrier:
	// reopening admission now would expose a root mid-swap.
	if _, err := Admit(root); err == nil {
		t.Fatal("a barrier still being worked behind was reclaimed")
	}
	if err := releaseBarrier(); err != nil {
		t.Fatal(err)
	}
	release, err := Admit(root)
	if err != nil {
		t.Fatalf("an unheld expired barrier stayed closed: %v", err)
	}
	defer release()
}

func TestReplacementRefusesAnExpiredBarrier(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	if _, err := Freeze(root, "cut-1", "v5", nil); err != nil {
		t.Fatal(err)
	}
	// Nothing reclaims it: on a root no operation touches, the lapsed barrier
	// keeps its phase on disk, and replacement must refuse it on its own.
	backdateLease(t, root)
	if _, err := MarkReplaced(root, "cut-1"); err == nil {
		t.Fatal("replacement accepted a barrier that had lost its lease")
	}
	current, _, err := Observe(root)
	if err != nil {
		t.Fatal(err)
	}
	if current.Phase != Frozen {
		t.Fatalf("refused replacement still moved the phase: %#v", current)
	}
}

func TestReplacementRefusesWhileTheBarrierIsHeld(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	if _, err := Freeze(root, "cut-1", "v5", nil); err != nil {
		t.Fatal(err)
	}
	releaseBarrier, err := HoldBarrier(root)
	if err != nil {
		t.Fatal(err)
	}
	// The lease is live; what disqualifies replacement here is that the trees
	// this would record as swapped are still being copied.
	if _, err := MarkReplaced(root, "cut-1"); err == nil {
		t.Fatal("replacement was recorded while a command was still replacing")
	}
	if err := releaseBarrier(); err != nil {
		t.Fatal(err)
	}
	if _, err := MarkReplaced(root, "cut-1"); err != nil {
		t.Fatalf("replacement refused after the work behind the barrier finished: %v", err)
	}
}

func TestAbortRefusesWhileTheBarrierIsHeld(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	if _, err := Freeze(root, "cut-1", "v5", nil); err != nil {
		t.Fatal(err)
	}
	releaseBarrier, err := HoldBarrier(root)
	if err != nil {
		t.Fatal(err)
	}
	// Abort is the operator's way back to admission, and it must be refused for
	// the same reason the automatic reclaim is: something is mid-copy behind it.
	if _, err := Abort(root, "cut-1"); err == nil {
		t.Fatal("abort reopened admission while a command was working behind the barrier")
	}
	if err := releaseBarrier(); err != nil {
		t.Fatal(err)
	}
	if _, err := Abort(root, "cut-1"); err != nil {
		t.Fatalf("abort refused once nothing was working behind the barrier: %v", err)
	}
}

func TestFreezeHoldsTheBarrierAcrossItsDrain(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	release, err := Admit(root)
	if err != nil {
		t.Fatal(err)
	}
	frozen := make(chan error, 1)
	go func() {
		_, freezeErr := Freeze(root, "cut-1", "v5", nil)
		frozen <- freezeErr
	}()
	// Freeze publishes the freezing phase and then waits on the lease held
	// above. A drain outlasting its own lease must not leave the root open to
	// reclaim while the freeze is still running.
	waitForPhase(t, root, Freezing, frozen)
	backdateLease(t, root)
	if _, err := Admit(root); err == nil {
		t.Fatal("a freeze still draining was reclaimed out from under itself")
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	if err := <-frozen; err != nil {
		t.Fatalf("freeze failed after its drain completed: %v", err)
	}
}

// waitForPhase waits for a concurrent operation to publish a phase, watching
// that operation's own result at the same time. A freeze that fails never
// reaches any phase, and waiting blind for one turns its error into an
// expired deadline that says nothing about why.
func waitForPhase(t *testing.T, root string, phase Phase, failed <-chan error) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		current, exists, err := Observe(root)
		if err != nil {
			t.Fatal(err)
		}
		if exists && current.Phase == phase {
			return
		}
		select {
		case err := <-failed:
			t.Fatalf("operation ended before reaching %s: %v", phase, err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("cutover never reached %s", phase)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestBarrierWrittenBeforeLeasesNeverExpires(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	frozen, err := Freeze(root, "cut-1", "v5", nil)
	if err != nil {
		t.Fatal(err)
	}
	dir, err := Directory(root)
	if err != nil {
		t.Fatal(err)
	}
	frozen.ExpiresAt = time.Time{}
	if err := writeState(dir, frozen); err != nil {
		t.Fatal(err)
	}
	if _, err := Admit(root); err == nil {
		t.Fatal("a barrier carrying no lease was reclaimed")
	}
}
