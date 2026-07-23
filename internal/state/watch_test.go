package state

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWatchAcknowledgementReplaysBeforeCheckpoint(t *testing.T) {
	home := t.TempDir()
	a := newTestClient(t, home, "")
	if _, err := a.Register("a", "dev"); err != nil {
		t.Fatal(err)
	}
	b := newTestClient(t, home, "")
	if _, err := b.Register("b", "review"); err != nil {
		t.Fatal(err)
	}

	first, err := a.Watch()
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := a.Watch()
	if err != nil {
		t.Fatal(err)
	}
	if replayed != first {
		t.Fatalf("unacknowledged summary was not replayed: first=%#v replay=%#v", first, replayed)
	}
	if err := a.AcknowledgeSummary(first); err != nil {
		t.Fatal(err)
	}
	next, err := a.Watch()
	if err != nil {
		t.Fatal(err)
	}
	if next.Sequence <= first.Sequence {
		t.Fatalf("acknowledged summary replayed: first=%d next=%d", first.Sequence, next.Sequence)
	}
}

func TestWatchDoesNotLoseLateLowerIndex(t *testing.T) {
	home := t.TempDir()
	client := newTestClient(t, home, "")
	if _, err := client.Register("a", "dev"); err != nil {
		t.Fatal(err)
	}
	// Drain the collaboration/agents roster commit and the join signal
	// registration itself produces before exercising late-delivery ordering.
	drainSummaries(t, client, 2)
	recipients := []string{"a"}
	if err := client.writeEvent(Event{Summary: Summary{Type: "update", Topic: "dev/tasks", Sequence: 103, Agent: "a"}, Recipients: recipients}); err != nil {
		t.Fatal(err)
	}
	higher, err := client.Watch()
	if err != nil || higher.Sequence != 103 {
		t.Fatalf("higher signal = %#v, %v", higher, err)
	}
	if err := client.AcknowledgeSummary(higher); err != nil {
		t.Fatal(err)
	}
	if err := client.writeEvent(Event{Summary: Summary{Type: "update", Topic: "dev/tasks", Sequence: 102, Agent: "a"}, Recipients: recipients}); err != nil {
		t.Fatal(err)
	}
	late, err := client.Watch()
	if err != nil || late.Sequence != 102 {
		t.Fatalf("late lower signal was skipped: %#v, %v", late, err)
	}
}

func TestWatchSinceReturnsOnlyHigherIndexes(t *testing.T) {
	home := t.TempDir()
	client := newTestClient(t, home, "")
	if _, err := client.Register("a", "development"); err != nil {
		t.Fatal(err)
	}
	for index := int64(2); index <= 3; index++ {
		if err := client.writeEvent(Event{
			Summary:    Summary{Type: "update", Topic: "dev/tasks", Sequence: index, Agent: "a"},
			Recipients: []string{"a"},
		}); err != nil {
			t.Fatal(err)
		}
	}
	summary, err := client.WatchSinceContext(context.Background(), 2)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Sequence != 3 {
		t.Fatalf("watch since 2 returned sequence %d", summary.Sequence)
	}
	if err := client.AcknowledgeSummary(summary); err != nil {
		t.Fatal(err)
	}
	cursor, err := client.loadCursor("a")
	if err != nil {
		t.Fatal(err)
	}
	if !indexAcknowledged(cursor.SummaryRanges, 1) || !indexAcknowledged(cursor.SummaryRanges, 2) {
		t.Fatalf("since floor was not persisted: %#v", cursor.SummaryRanges)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := client.WatchContext(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("default watch replayed a discarded lower index: %v", err)
	}
}

func TestAcknowledgeSummaryRejectsInvalidSDKInput(t *testing.T) {
	home := t.TempDir()
	client := newTestClient(t, home, "")
	if _, err := client.Register("a", "development"); err != nil {
		t.Fatal(err)
	}
	if err := client.AcknowledgeSummary(Summary{}); err == nil {
		t.Fatal("invalid summary acknowledgement succeeded")
	}
	if _, err := os.Stat(filepath.Join(home, "cursors", "a.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("invalid acknowledgement mutated cursor: %v", err)
	}
}

func TestWatchReconcilesJournalWhenInboxAppendIsMissing(t *testing.T) {
	home := t.TempDir()
	client := newTestClient(t, home, "")
	if _, err := client.Register("a", "dev"); err != nil {
		t.Fatal(err)
	}
	// Drain the collaboration/agents roster commit and the join signal
	// registration itself produces before exercising journal reconciliation.
	drainSummaries(t, client, 2)
	index, err := client.nextIndex()
	if err != nil {
		t.Fatal(err)
	}
	event := Event{Summary: Summary{Type: "update", Topic: "dev/tasks", Sequence: index, Agent: "a"}, Recipients: []string{"a"}}
	if err := writeJSONAtomic(filepath.Join(home, "events", indexName(index)), event); err != nil {
		t.Fatal(err)
	}
	summary, err := client.Watch()
	if err != nil || summary.Sequence != index {
		t.Fatalf("journal-only event not reconciled: %#v, %v", summary, err)
	}
}

func TestWatchOwnershipIsExclusiveAndCrashReleased(t *testing.T) {
	home := t.TempDir()
	first := newTestClient(t, home, "")
	if _, err := first.Register("a", "development"); err != nil {
		t.Fatal(err)
	}
	second := newTestClient(t, home, "a")

	release, err := first.AcquireWatchOwnership()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := second.AcquireWatchOwnership(); err == nil {
		t.Fatal("second watch owner acquired the same agent")
	} else {
		assertCode(t, err, "LOCKED")
		wantHint := fmt.Sprintf("pid %d", os.Getpid())
		if !strings.Contains(err.Error(), wantHint) {
			t.Fatalf("error = %q, want it to name the holder: %q", err.Error(), wantHint)
		}
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	releaseAgain, err := second.AcquireWatchOwnership()
	if err != nil {
		t.Fatal(err)
	}
	if err := releaseAgain(); err != nil {
		t.Fatal(err)
	}
}

func TestWatchDetectsDeregistrationInsteadOfHangingForever(t *testing.T) {
	// Deregistering "a" the normal way also force-broadcasts a
	// collaboration/agents strike commit to every still-registered agent,
	// including "a" itself (its registry file isn't removed until after that
	// commit) — which would let this watch resolve via an ordinary signal and
	// mask the bug this test targets. Removing the registry file directly
	// isolates the actual mechanism: a watch already blocked mid-poll, with no
	// other bus activity at all, must still notice its own agent is gone.
	home := t.TempDir()
	watcher := newTestClient(t, home, "")
	if _, err := watcher.Register("a", "dev"); err != nil {
		t.Fatal(err)
	}
	drainSummaries(t, watcher, 2) // collaboration/agents roster commit, then join

	result := make(chan error, 1)
	go func() {
		_, err := watcher.WatchContext(context.Background())
		result <- err
	}()

	time.Sleep(20 * time.Millisecond) // let the watch enter its poll loop
	if err := os.Remove(filepath.Join(home, "registry", "a.json")); err != nil {
		t.Fatal(err)
	}

	select {
	case err := <-result:
		assertCode(t, err, "NOT_FOUND")
	case <-time.After(2 * time.Second):
		t.Fatal("watch did not detect deregistration and kept blocking")
	}
}
