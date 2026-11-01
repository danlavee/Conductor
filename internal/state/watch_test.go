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

	"github.com/danlavee/Conductor/internal/platform"
)

// A one-shot watcher ends at its first delivery, so waking a publisher for its
// own publication would un-arm any agent that followed the skill's own order:
// make yourself reachable, then say what you have taken. The record still has
// to accumulate, and peers still have to be woken -- only the author is spared.
func TestAnAgentIsNotWokenByItsOwnPublication(t *testing.T) {
	home := t.TempDir()
	author := newTestClient(t, home, "")
	if _, err := author.Join("author", "dev"); err != nil {
		t.Fatal(err)
	}
	if _, err := author.SubscribeTopic("dev/tasks"); err != nil {
		t.Fatal(err)
	}
	peer := newTestClient(t, home, "")
	if _, err := peer.Join("peer", "review"); err != nil {
		t.Fatal(err)
	}
	if _, err := peer.SubscribeTopic("dev/tasks"); err != nil {
		t.Fatal(err)
	}
	drainAllPending(t, author)
	drainAllPending(t, peer)

	if _, err := author.Put("dev/tasks", "mine"); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if summaries, err := author.WatchContext(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("the publisher was woken by its own publication: %#v, %v", summaries, err)
	}
	if delivered := watchOne(t, peer); delivered.Topic != "dev/tasks" {
		t.Fatalf("a subscribed peer was not woken: %#v", delivered)
	}
	// Sparing the author a wake must not cost it the record.
	full, err := author.Get(ReadRequest{Topic: "dev/tasks", Mode: ReadFull})
	if err != nil || len(full.Records) != 1 || full.Records[0].Text != "mine" {
		t.Fatalf("the publication did not accumulate for its author: %#v, %v", full, err)
	}
}

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
	if _, err := a.writeEvent(Event{Summary: Summary{Type: "update", Topic: "dev/tasks", Sequence: 500, Agent: "b"}, Recipients: []string{"a"}}); err != nil {
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
	drainAllPending(t, client)
	// Authored by a peer: a signal an agent published itself never reaches its
	// own inbox, so a self-addressed event would leave nothing to batch.
	recipients := []string{"a"}
	for _, sequence := range []int64{201, 202, 203} {
		if _, err := client.writeEvent(Event{Summary: Summary{Type: "update", Topic: "dev/tasks", Sequence: sequence, Agent: "b"}, Recipients: recipients}); err != nil {
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
	// Reach a clean slate before exercising late-delivery ordering, and let a
	// peer author the signals: an agent's own act never reaches its own inbox.
	drainAllPending(t, client)
	recipients := []string{"a"}
	if _, err := client.writeEvent(Event{Summary: Summary{Type: "update", Topic: "dev/tasks", Sequence: 103, Agent: "b"}, Recipients: recipients}); err != nil {
		t.Fatal(err)
	}
	higher := watchOne(t, client)
	if higher.Sequence != 103 {
		t.Fatalf("higher signal = %#v", higher)
	}
	if err := client.AcknowledgeSummary(higher); err != nil {
		t.Fatal(err)
	}
	if _, err := client.writeEvent(Event{Summary: Summary{Type: "update", Topic: "dev/tasks", Sequence: 102, Agent: "b"}, Recipients: recipients}); err != nil {
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
		if _, err := client.writeEvent(Event{
			Summary:    Summary{Type: "update", Topic: "dev/tasks", Sequence: index, Agent: "b"},
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
	// Reach a clean slate before exercising journal reconciliation, and let a
	// peer author the signal: an agent's own act never reaches its own inbox.
	drainAllPending(t, client)
	index, err := client.nextIndex()
	if err != nil {
		t.Fatal(err)
	}
	event := Event{Summary: Summary{Type: "update", Topic: "dev/tasks", Sequence: index, Agent: "b"}, Recipients: []string{"a"}}
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
			// A peer's signal is what creates the inbox this test corrupts:
			// an agent's own act never lands in its own inbox, so a solo join
			// leaves no file to append to.
			if _, err := client.publishEvent("update", "dev/tasks", "b", []string{"a"}); err != nil {
				t.Fatal(err)
			}
			drainAllPending(t, client)
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
			summary, err := client.publishEvent("update", "dev/tasks", "b", []string{"a"})
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
	// A peer's signal is what creates the inbox this test truncates: an agent's
	// own act never lands in its own inbox, so a solo join leaves no file.
	if _, err := client.publishEvent("update", "dev/tasks", "b", []string{"a"}); err != nil {
		t.Fatal(err)
	}
	drainAllPending(t, client)
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
	event := Event{Summary: Summary{Type: "update", Topic: "dev/tasks", Sequence: index, Agent: "b"}, Recipients: []string{"a"}}
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
	next, err := client.publishEvent("update", "dev/tasks", "b", []string{"a"})
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

// An identity held by an adapter and one holding its own watcher are both
// wakeable and differ in everything the agent must then do, so status has to
// separate them. It also has to say what reaches the agent: an agent that armed
// nothing and an agent subscribed to nothing are equally unreachable, and only
// one of them looks it.
func TestStatusReportsWhatHoldsTheStreamAndWhatReachesTheAgent(t *testing.T) {
	home := t.TempDir()
	client := newTestClient(t, home, "")
	if _, err := client.Join("nomad", "parser"); err != nil {
		t.Fatal(err)
	}
	nomad := newTestClient(t, home, "nomad")

	status, err := nomad.WatchStatus()
	if err != nil {
		t.Fatal(err)
	}
	if status.Holder != nil {
		t.Fatalf("holder = %+v, want none while nothing holds the stream", status.Holder)
	}
	if len(status.Subscriptions.TopicGroups) != 0 || len(status.Subscriptions.Topics) != 0 {
		t.Fatalf("subscriptions = %+v, want empty for an agent that has subscribed to nothing", status.Subscriptions)
	}

	for _, held := range []struct {
		acquire func() (func() error, error)
		want    WatchHolder
	}{
		{nomad.AcquireWatchOwnership, WatchHolder{Kind: HolderSelf}},
		{
			func() (func() error, error) { return nomad.AcquireAdapterWatchOwnership("claude", "session-1") },
			WatchHolder{Kind: HolderAdapter, Adapter: "claude", Session: "session-1"},
		},
	} {
		release, err := held.acquire()
		if err != nil {
			t.Fatal(err)
		}
		status, err := nomad.WatchStatus()
		if err != nil {
			t.Fatal(err)
		}
		if status.Holder == nil || *status.Holder != held.want {
			t.Fatalf("holder = %+v, want %+v", status.Holder, held.want)
		}
		if err := release(); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := nomad.SubscribeTopicGroup("development"); err != nil {
		t.Fatal(err)
	}
	status, err = nomad.WatchStatus()
	if err != nil {
		t.Fatal(err)
	}
	if len(status.Subscriptions.TopicGroups) != 1 || status.Subscriptions.TopicGroups[0] != "development" {
		t.Fatalf("subscriptions = %+v, want the group just subscribed", status.Subscriptions)
	}
}

// An owner record written before holders were recorded names a live process and
// says nothing about what it is. Reporting that as either kind would be an
// invention; the query has an answer for not knowing.
func TestStatusReportsAnUnlabelledHolderAsUnknown(t *testing.T) {
	home := t.TempDir()
	client := newTestClient(t, home, "")
	if _, err := client.Join("nomad", "parser"); err != nil {
		t.Fatal(err)
	}
	nomad := newTestClient(t, home, "nomad")
	_, ownerPath := watchGuardPaths(home, "nomad")
	pid := os.Getpid()
	processStart, _ := platform.ProcessStartToken(pid)
	if err := writeJSONAtomic(ownerPath, map[string]any{
		"pid": pid, "process_start": processStart,
	}); err != nil {
		t.Fatal(err)
	}
	status, err := nomad.WatchStatus()
	if err != nil {
		t.Fatal(err)
	}
	if !status.Wakeable {
		t.Fatal("wakeable = false; a live owner without a holder is still holding")
	}
	if status.Holder == nil || status.Holder.Kind != HolderUnknown {
		t.Fatalf("holder = %+v, want kind %q", status.Holder, HolderUnknown)
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
	// collaboration/agents strike commit, and while "a" no longer receives its
	// own commit, a peer deregistering it would deliver one — which would let
	// this watch resolve via an ordinary signal and mask the bug this test
	// targets. Removing the registry file directly isolates the actual
	// mechanism: a watch already blocked mid-poll, with no other bus activity
	// at all, must still notice its own agent is gone.
	home := t.TempDir()
	watcher := newTestClient(t, home, "")
	if _, err := watcher.Join("a", "dev"); err != nil {
		t.Fatal(err)
	}
	drainAllPending(t, watcher)

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
