package state

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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
		t.Fatalf("unacknowledged signal was not replayed: first=%#v replay=%#v", first, replayed)
	}
	if err := a.AcknowledgeSignal(first); err != nil {
		t.Fatal(err)
	}
	next, err := a.Watch()
	if err != nil {
		t.Fatal(err)
	}
	if next.Index <= first.Index {
		t.Fatalf("acknowledged signal replayed: first=%d next=%d", first.Index, next.Index)
	}
}

func TestWatchDoesNotLoseLateLowerIndex(t *testing.T) {
	home := t.TempDir()
	client := newTestClient(t, home, "")
	if _, err := client.Register("a", "dev"); err != nil {
		t.Fatal(err)
	}
	join, err := client.Watch()
	if err != nil {
		t.Fatal(err)
	}
	if err := client.AcknowledgeSignal(join); err != nil {
		t.Fatal(err)
	}
	recipients := []string{"a"}
	if err := client.writeEvent(Event{Signal: Signal{Type: "update", Resource: "dev/tasks", Key: "later", Index: 3, Agent: "a"}, Recipients: recipients}); err != nil {
		t.Fatal(err)
	}
	higher, err := client.Watch()
	if err != nil || higher.Index != 3 {
		t.Fatalf("higher signal = %#v, %v", higher, err)
	}
	if err := client.AcknowledgeSignal(higher); err != nil {
		t.Fatal(err)
	}
	if err := client.writeEvent(Event{Signal: Signal{Type: "update", Resource: "dev/tasks", Key: "late", Index: 2, Agent: "a"}, Recipients: recipients}); err != nil {
		t.Fatal(err)
	}
	late, err := client.Watch()
	if err != nil || late.Index != 2 {
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
			Signal:     Signal{Type: "update", Resource: "dev/tasks", Key: "status", Index: index, Agent: "a"},
			Recipients: []string{"a"},
		}); err != nil {
			t.Fatal(err)
		}
	}
	signal, err := client.WatchSinceContext(context.Background(), 2)
	if err != nil {
		t.Fatal(err)
	}
	if signal.Index != 3 {
		t.Fatalf("watch since 2 returned index %d", signal.Index)
	}
	if err := client.AcknowledgeSignal(signal); err != nil {
		t.Fatal(err)
	}
	cursor, err := client.loadCursor("a")
	if err != nil {
		t.Fatal(err)
	}
	if !indexAcknowledged(cursor.SignalRanges, 1) || !indexAcknowledged(cursor.SignalRanges, 2) {
		t.Fatalf("since floor was not persisted: %#v", cursor.SignalRanges)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := client.WatchContext(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("default watch replayed a discarded lower index: %v", err)
	}
}

func TestAcknowledgeSignalRejectsInvalidSDKInput(t *testing.T) {
	home := t.TempDir()
	client := newTestClient(t, home, "")
	if _, err := client.Register("a", "development"); err != nil {
		t.Fatal(err)
	}
	if err := client.AcknowledgeSignal(Signal{}); err == nil {
		t.Fatal("invalid signal acknowledgement succeeded")
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
	join, err := client.Watch()
	if err != nil {
		t.Fatal(err)
	}
	if err := client.AcknowledgeSignal(join); err != nil {
		t.Fatal(err)
	}
	index, err := client.nextIndex()
	if err != nil {
		t.Fatal(err)
	}
	event := Event{Signal: Signal{Type: "update", Resource: "dev/tasks", Key: "missing-inbox", Index: index, Agent: "a"}, Recipients: []string{"a"}}
	if err := writeJSONAtomic(filepath.Join(home, "events", indexName(index)), event); err != nil {
		t.Fatal(err)
	}
	signal, err := client.Watch()
	if err != nil || signal.Index != index {
		t.Fatalf("journal-only event not reconciled: %#v, %v", signal, err)
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
