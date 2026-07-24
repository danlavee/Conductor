package state

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestWatchAcknowledgementReplaysBeforeCheckpoint(t *testing.T) {
	home := t.TempDir()
	a := newTestClient(t, home, "")
	if _, err := a.Join("a", "dev"); err != nil {
		t.Fatal(err)
	}
	b := newTestClient(t, home, "")
	if _, err := b.Join("b", "review"); err != nil {
		t.Fatal(err)
	}

	first, err := a.Watch()
	if err != nil {
		t.Fatal(err)
	}
	if len(first) == 0 {
		t.Fatal("expected at least one pending summary")
	}
	replayed, err := a.Watch()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(replayed, first) {
		t.Fatalf("unacknowledged batch was not replayed identically: first=%#v replay=%#v", first, replayed)
	}
	for _, summary := range first {
		if err := a.AcknowledgeSummary(summary); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.writeEvent(Event{Summary: Summary{Type: "update", Topic: "dev/tasks", Sequence: 500, Agent: "a"}, Recipients: []string{"a"}}); err != nil {
		t.Fatal(err)
	}
	next := watchOne(t, a)
	if next.Sequence != 500 {
		t.Fatalf("acknowledged batch replayed instead of advancing to new signal: next=%#v", next)
	}
}

func TestWatchReturnsAllPendingSignalsInOneBatch(t *testing.T) {
	home := t.TempDir()
	client := newTestClient(t, home, "")
	if _, err := client.Join("a", "dev"); err != nil {
		t.Fatal(err)
	}
	drainSummaries(t, client, 2) // collaboration/agents roster commit, then join
	recipients := []string{"a"}
	for _, sequence := range []int64{201, 202, 203} {
		if err := client.writeEvent(Event{Summary: Summary{Type: "update", Topic: "dev/tasks", Sequence: sequence, Agent: "a"}, Recipients: recipients}); err != nil {
			t.Fatal(err)
		}
	}
	summaries, err := client.Watch()
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 3 {
		t.Fatalf("watch = %#v, want all 3 pending signals drained in one call", summaries)
	}
	for index, want := range []int64{201, 202, 203} {
		if summaries[index].Sequence != want {
			t.Fatalf("summaries[%d].Sequence = %d, want %d (discovery order preserved)", index, summaries[index].Sequence, want)
		}
	}
	for _, summary := range summaries {
		if err := client.AcknowledgeSummary(summary); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := client.WatchContext(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("acknowledged batch replayed: %v", err)
	}
}

func TestWatchDoesNotLoseLateLowerIndex(t *testing.T) {
	home := t.TempDir()
	client := newTestClient(t, home, "")
	if _, err := client.Join("a", "dev"); err != nil {
		t.Fatal(err)
	}
	// Drain the collaboration/agents roster commit and the join signal
	// registration itself produces before exercising late-delivery ordering.
	drainSummaries(t, client, 2)
	recipients := []string{"a"}
	if err := client.writeEvent(Event{Summary: Summary{Type: "update", Topic: "dev/tasks", Sequence: 103, Agent: "a"}, Recipients: recipients}); err != nil {
		t.Fatal(err)
	}
	higher := watchOne(t, client)
	if higher.Sequence != 103 {
		t.Fatalf("higher signal = %#v", higher)
	}
	if err := client.AcknowledgeSummary(higher); err != nil {
		t.Fatal(err)
	}
	if err := client.writeEvent(Event{Summary: Summary{Type: "update", Topic: "dev/tasks", Sequence: 102, Agent: "a"}, Recipients: recipients}); err != nil {
		t.Fatal(err)
	}
	late := watchOne(t, client)
	if late.Sequence != 102 {
		t.Fatalf("late lower signal was skipped: %#v", late)
	}
}

func TestWatchSinceReturnsOnlyHigherIndexes(t *testing.T) {
	home := t.TempDir()
	client := newTestClient(t, home, "")
	if _, err := client.Join("a", "development"); err != nil {
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
	summaries, err := client.WatchSinceContext(context.Background(), 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 1 || summaries[0].Sequence != 3 {
		t.Fatalf("watch since 2 returned %#v", summaries)
	}
	if err := client.AcknowledgeSummary(summaries[0]); err != nil {
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
	if _, err := client.Join("a", "development"); err != nil {
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
	if _, err := client.Join("a", "dev"); err != nil {
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
	summary := watchOne(t, client)
	if summary.Sequence != index {
		t.Fatalf("journal-only event not reconciled: %#v", summary)
	}
}

func TestWatchSkipsCorruptInboxLine(t *testing.T) {
	for _, test := range []struct {
		name string
		line string
	}{
		{name: "malformed", line: "not-json\n"},
		{name: "invalid", line: "{}\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			client := newTestClient(t, home, "")
			if _, err := client.Join("a", "dev"); err != nil {
				t.Fatal(err)
			}
			drainSummaries(t, client, 2)
			inboxPath := filepath.Join(home, "inbox", "a")
			file, err := os.OpenFile(inboxPath, os.O_WRONLY|os.O_APPEND, 0o600)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := file.WriteString(test.line); err != nil {
				t.Fatal(err)
			}
			if err := file.Close(); err != nil {
				t.Fatal(err)
			}
			summary, err := client.publishEvent("update", "dev/tasks", "a", []string{"a"})
			if err != nil {
				t.Fatal(err)
			}
			watched := watchOne(t, client)
			if watched.Sequence != summary.Sequence {
				t.Fatalf("watch = %#v", watched)
			}
			if err := client.AcknowledgeSummary(watched); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestWatchReconcilesJournalWhenInboxEndsWithTruncatedLine(t *testing.T) {
	home := t.TempDir()
	client := newTestClient(t, home, "")
	if _, err := client.Join("a", "dev"); err != nil {
		t.Fatal(err)
	}
	drainSummaries(t, client, 2)
	inboxPath := filepath.Join(home, "inbox", "a")
	file, err := os.OpenFile(inboxPath, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(`{"type":"update"`); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	index, err := client.nextIndex()
	if err != nil {
		t.Fatal(err)
	}
	event := Event{Summary: Summary{Type: "update", Topic: "dev/tasks", Sequence: index, Agent: "a"}, Recipients: []string{"a"}}
	if err := writeJSONAtomic(filepath.Join(home, "events", indexName(index)), event); err != nil {
		t.Fatal(err)
	}
	watched := watchOne(t, client)
	if watched.Sequence != index {
		t.Fatalf("watch = %#v", watched)
	}
	if err := client.AcknowledgeSummary(watched); err != nil {
		t.Fatal(err)
	}
	next, err := client.publishEvent("update", "dev/tasks", "a", []string{"a"})
	if err != nil {
		t.Fatal(err)
	}
	inbox, err := os.ReadFile(inboxPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(inbox), `{"type":"update"{"type":"update"`) {
		t.Fatalf("truncated tail remained in inbox: %q", inbox)
	}
	scanner := bufio.NewScanner(strings.NewReader(string(inbox)))
	for scanner.Scan() {
		if !json.Valid(scanner.Bytes()) {
			t.Fatalf("invalid inbox line after repair: %q", scanner.Bytes())
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	afterRepair := watchOne(t, client)
	if afterRepair.Sequence != next.Sequence {
		t.Fatalf("watch after repair = %#v", afterRepair)
	}
}

func TestWatchOwnershipIsExclusiveAndCrashReleased(t *testing.T) {
	home := t.TempDir()
	first := newTestClient(t, home, "")
	if _, err := first.Join("a", "development"); err != nil {
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
	if _, err := watcher.Join("a", "dev"); err != nil {
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
