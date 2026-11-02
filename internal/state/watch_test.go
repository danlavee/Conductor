package state

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// A one-shot watcher ends at its first delivery, so waking a publisher for its
// own publication would un-arm it. The record still accumulates and peers are
// still woken -- only the author is spared.
func TestAnAgentIsNotWokenByItsOwnPublication(t *testing.T) {
	home := t.TempDir()
	author := joined(t, home, "author", "dev/tasks")
	peer := joined(t, home, "peer", "dev/tasks")
	drainAllPending(t, author)
	drainAllPending(t, peer)

	dispatch, err := author.Put("dev/tasks", "mine")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(dispatch.Recipients, []string{"peer"}) {
		t.Fatalf("recipients = %v, want only the peer", dispatch.Recipients)
	}
	assertQuiet(t, author)
	if delivered := watchOne(t, peer); delivered.Topic != "dev/tasks" || delivered.Publications[0].Records[0].Text != "mine" {
		t.Fatalf("a subscribed peer was not woken with the record: %#v", delivered)
	}
	full, err := author.Get(ReadRequest{Topic: "dev/tasks", Mode: ReadFull})
	if err != nil || len(full.Records) != 1 || full.Records[0].Text != "mine" {
		t.Fatalf("the publication did not accumulate for its author: %#v, %v", full, err)
	}
}

// Content stays unread until the reader accepts it, so a failed acknowledgment
// delivers the same batch again rather than losing it.
func TestWatchRedeliversUntilAcknowledged(t *testing.T) {
	home := t.TempDir()
	reader := joined(t, home, "reader", "messages/team")
	writer := joined(t, home, "writer")
	drainAllPending(t, reader)
	if _, err := writer.Put("messages/team", "hello"); err != nil {
		t.Fatal(err)
	}
	batch, err := reader.Watch()
	if err != nil {
		t.Fatal(err)
	}
	saveFailure := errors.New("save failed")
	reader.saveCursorFn = func(string, Cursor) error { return saveFailure }
	if err := reader.AcknowledgeRead(batch.Deliveries...); !errors.Is(err, saveFailure) {
		t.Fatalf("error = %v, want save failure", err)
	}
	reader.saveCursorFn = nil
	replayed, err := reader.Watch()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(replayed, batch) {
		t.Fatalf("failed delivery did not replay: got %#v, want %#v", replayed, batch)
	}
	if err := reader.AcknowledgeRead(replayed.Deliveries...); err != nil {
		t.Fatal(err)
	}
	assertQuiet(t, reader)
}

// Content read with get is read: watch does not wake for it again.
func TestWatchSkipsContentReadOutOfBand(t *testing.T) {
	home := t.TempDir()
	reader := joined(t, home, "reader", "messages/team")
	writer := joined(t, home, "writer")
	drainAllPending(t, reader)
	if _, err := writer.Put("messages/team", "hello"); err != nil {
		t.Fatal(err)
	}
	delta, err := reader.Get(ReadRequest{Topic: "messages/team", Mode: ReadDelta})
	if err != nil {
		t.Fatal(err)
	}
	if err := reader.AcknowledgeRead(delta); err != nil {
		t.Fatal(err)
	}
	assertQuiet(t, reader)
}

// Joining and leaving are roster lines on collaboration/agents, which reaches
// every agent whatever it subscribed to.
func TestMembershipReachesAnUnsubscribedAgent(t *testing.T) {
	home := t.TempDir()
	reader := joined(t, home, "reader")
	drainAllPending(t, reader)
	peer := joined(t, home, "peer")
	if got := watchOne(t, reader); got.Topic != collaborationAgentsTopic || got.Publications[0].Records[0].Text != "peer: dev" {
		t.Fatalf("join delivered %#v", got)
	}
	if err := peer.Leave("peer"); err != nil {
		t.Fatal(err)
	}
	if got := watchOne(t, reader); got.Publications[0].Records[0].Text != "~~peer: dev~~" {
		t.Fatalf("leave delivered %#v", got)
	}
	assertQuiet(t, reader)
}

// A group subscription covers topics that do not exist yet when the watch
// starts.
func TestWatchFindsATopicCreatedInASubscribedGroup(t *testing.T) {
	home := t.TempDir()
	reader := joined(t, home, "reader")
	if _, err := reader.SubscribeTopicGroup("dev"); err != nil {
		t.Fatal(err)
	}
	writer := joined(t, home, "writer")
	drainAllPending(t, reader)
	idle := make(chan struct{}, 1)
	reader.watchIdleFn = func() {
		select {
		case idle <- struct{}{}:
		default:
		}
	}
	result := make(chan Batch, 1)
	go func() {
		batch, _ := reader.Watch()
		result <- batch
	}()
	<-idle
	if _, err := writer.Put("dev/new", "fresh"); err != nil {
		t.Fatal(err)
	}
	select {
	case batch := <-result:
		if len(batch.Deliveries) != 1 || batch.Deliveries[0].Topic != "dev/new" {
			t.Fatalf("batch = %#v, want the new topic", batch)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("watch never found the new topic")
	}
}

// One batch shares the read limit across topics. A publication is never split,
// so the last topic taken may overrun it; topics after it wait for the next
// watch.
func TestWatchBatchSharesTheReadLimitAcrossTopics(t *testing.T) {
	home := t.TempDir()
	reader := joined(t, home, "reader")
	if _, err := reader.SubscribeTopicGroup("work"); err != nil {
		t.Fatal(err)
	}
	writer := joined(t, home, "writer")
	drainAllPending(t, reader)
	// work/b's one publication overruns what work/a left of the cap and is
	// still delivered whole; work/c finds the cap spent and waits.
	for topic, count := range map[string]int{"work/a": 15, "work/b": 8, "work/c": 3} {
		if _, err := writer.Publish(topic, creates(count, "x")); err != nil {
			t.Fatal(err)
		}
	}
	batch, err := reader.Watch()
	if err != nil {
		t.Fatal(err)
	}
	var topics []string
	for _, delivery := range batch.Deliveries {
		topics = append(topics, delivery.Topic)
	}
	if !reflect.DeepEqual(topics, []string{"work/a", "work/b"}) || batch.Remaining != 3 || batch.DefaultLimit != DefaultReadLimit {
		t.Fatalf("batch topics = %v remaining = %d limit = %d", topics, batch.Remaining, batch.DefaultLimit)
	}
	if err := reader.AcknowledgeRead(batch.Deliveries...); err != nil {
		t.Fatal(err)
	}
	if got := watchOne(t, reader); got.Topic != "work/c" || len(got.Publications[0].Records) != 3 {
		t.Fatalf("deferred topic delivered %#v", got)
	}
}

// What a topic held before the subscription is read with get, never woken
// for, whether the topic predates the reader's join or not.
func TestASubscriptionIsNotRetroactive(t *testing.T) {
	home := t.TempDir()
	writer := joined(t, home, "writer")
	if _, err := writer.Put("work/early", "before join"); err != nil {
		t.Fatal(err)
	}
	reader := joined(t, home, "reader")
	drainAllPending(t, reader)
	if _, err := writer.Put("work/late", "before subscribe"); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.SubscribeTopicGroup("work"); err != nil {
		t.Fatal(err)
	}
	assertQuiet(t, reader)
	if _, err := writer.Put("work/late", "after"); err != nil {
		t.Fatal(err)
	}
	if got := watchOne(t, reader); got.Topic != "work/late" || len(got.Publications) != 1 || got.Publications[0].Records[0].Text != "after" {
		t.Fatalf("delivered %#v", got)
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
	if status.Wakeable || status.Holder != "" {
		t.Fatalf("status = %+v, want no holder while nothing holds the stream", status)
	}
	if len(status.Subscriptions.TopicGroups) != 0 || len(status.Subscriptions.Topics) != 0 {
		t.Fatalf("subscriptions = %+v, want empty for an agent that has subscribed to nothing", status.Subscriptions)
	}

	for _, holder := range []Holder{HolderSelf, HolderAdapter} {
		release, err := nomad.AcquireWatchOwnership(holder)
		if err != nil {
			t.Fatal(err)
		}
		status, err := nomad.WatchStatus()
		if err != nil {
			t.Fatal(err)
		}
		if !status.Wakeable || status.Holder != holder {
			t.Fatalf("status = %+v, want held by %q", status, holder)
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

func TestWatchOwnershipIsExclusiveAndCrashReleased(t *testing.T) {
	home := t.TempDir()
	first := newTestClient(t, home, "")
	if _, err := first.Join("a", "development"); err != nil {
		t.Fatal(err)
	}
	second := newTestClient(t, home, "a")

	release, err := first.AcquireWatchOwnership(HolderSelf)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := second.AcquireWatchOwnership(HolderSelf); err == nil {
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
	releaseAgain, err := second.AcquireWatchOwnership(HolderSelf)
	if err != nil {
		t.Fatal(err)
	}
	if err := releaseAgain(); err != nil {
		t.Fatal(err)
	}
}

func TestWatchDetectsDeregistrationInsteadOfHangingForever(t *testing.T) {
	// Removing the registry file directly, rather than leaving, isolates the
	// mechanism: a leave also publishes a roster edit that could end the watch
	// as an ordinary delivery. A watch already blocked mid-poll, with no other
	// bus activity at all, must still notice its own agent is gone.
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
