package state

import "testing"

func TestReadModes(t *testing.T) {
	home := t.TempDir()
	writer := newTestClient(t, home, "")
	if _, err := writer.Join("writer", "dev"); err != nil {
		t.Fatal(err)
	}
	reader := newTestClient(t, home, "")
	if _, err := reader.Join("reader", "review"); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.SubscribeTopic("dev/tasks"); err != nil {
		t.Fatal(err)
	}
	firstDispatch, err := writer.Put("dev/tasks", "one")
	if err != nil {
		t.Fatal(err)
	}
	secondDispatch, err := writer.Put("dev/tasks", "two")
	if err != nil {
		t.Fatal(err)
	}
	first := firstDispatch.Record()
	if _, err := writer.Edit("dev/tasks", first.Index, "one-updated"); err != nil {
		t.Fatal(err)
	}
	delta, err := reader.Get(ReadRequest{Topic: "dev/tasks", Mode: ReadDelta})
	if err != nil || len(delta.Publications) != 3 || delta.Publications[1].Sequence != secondDispatch.Sequence {
		t.Fatalf("delta = %#v, error = %v", delta, err)
	}
	if err := reader.AcknowledgeRead(delta); err != nil {
		t.Fatal(err)
	}
	if again, err := reader.Get(ReadRequest{Topic: "dev/tasks", Mode: ReadDelta}); err != nil || len(again.Publications) != 0 {
		t.Fatalf("delta after acknowledging = %#v, error = %v", again, err)
	}
	for _, mode := range []ReadMode{ReadDelta, ReadFull} {
		if _, err := reader.Get(ReadRequest{Topic: "dev/tasks", RecordIndex: first.Index, Mode: mode}); err == nil {
			t.Errorf("mode %d accepted a record index", mode)
		}
	}
	full, err := reader.Get(ReadRequest{Topic: "dev/tasks", Mode: ReadFull})
	if err != nil || len(full.Records) != 2 || full.Records[0].Text != "one-updated" || full.Records[1].Text != "two" {
		t.Fatalf("full = %#v, error = %v", full, err)
	}
}

func TestRangeAndLimitUseCurrentRecordIndexes(t *testing.T) {
	client := newTestClient(t, t.TempDir(), "")
	if _, err := client.Join("writer", "records"); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"one", "two", "three"} {
		if _, err := client.Put("messages/range", text); err != nil {
			t.Fatal(err)
		}
	}
	result, err := client.Get(ReadRequest{Topic: "messages/range", Mode: ReadRange, Limit: 2})
	if err != nil || len(result.Records) != 2 || result.Records[0].Index >= result.Records[1].Index {
		t.Fatalf("range = %#v, error = %v", result, err)
	}
	index := result.Records[1].Index
	bounded, err := client.Get(ReadRequest{Topic: "messages/range", Mode: ReadRange, Start: index, End: index})
	if err != nil || len(bounded.Records) != 1 || bounded.Records[0].Index != index {
		t.Fatalf("bounded = %#v, error = %v", bounded, err)
	}
}

func TestStruckRecordRemainsInFullAndDelta(t *testing.T) {
	home := t.TempDir()
	writer := joined(t, home, "writer")
	client := joined(t, home, "reader", "collaboration/shared")
	dispatch, err := writer.Put("collaboration/shared", "plain")
	if err != nil {
		t.Fatal(err)
	}
	record := dispatch.Record()
	if _, err := writer.Edit("collaboration/shared", record.Index, "~~plain~~"); err != nil {
		t.Fatal(err)
	}
	full, err := client.Get(ReadRequest{Topic: "collaboration/shared", Mode: ReadFull})
	if err != nil || len(full.Records) != 1 || full.Records[0].Text != "~~plain~~" {
		t.Fatalf("full = %#v, error = %v", full, err)
	}
	delta, err := client.Get(ReadRequest{Topic: "collaboration/shared", Mode: ReadDelta})
	if err != nil || len(delta.Publications) != 2 || delta.Publications[1].Records[0].Text != "~~plain~~" {
		t.Fatalf("delta = %#v, error = %v", delta, err)
	}
}

// putN writes count records to topic, one publication each, so a test can
// build a backlog larger than DefaultReadLimit without spelling out each one.
func putN(t *testing.T, client *Client, topic string, count int) {
	t.Helper()
	for i := 0; i < count; i++ {
		if _, err := client.Put(topic, "x"); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRangeDefaultsToReadLimitAndReportsRemaining(t *testing.T) {
	client := newTestClient(t, t.TempDir(), "")
	if _, err := client.Join("writer", "records"); err != nil {
		t.Fatal(err)
	}
	putN(t, client, "messages/capped", DefaultReadLimit+5)

	result, err := client.Get(ReadRequest{Topic: "messages/capped", Mode: ReadRange})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Records) != DefaultReadLimit {
		t.Fatalf("records = %d, want %d", len(result.Records), DefaultReadLimit)
	}
	if result.Remaining != 5 {
		t.Fatalf("remaining = %d, want 5", result.Remaining)
	}
	if result.DefaultLimit != DefaultReadLimit {
		t.Fatalf("default_read_limit = %d, want %d", result.DefaultLimit, DefaultReadLimit)
	}

	// An explicit --limit above the default is honored as-is, not re-capped.
	explicit, err := client.Get(ReadRequest{Topic: "messages/capped", Mode: ReadRange, Limit: DefaultReadLimit + 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(explicit.Records) != DefaultReadLimit+5 || explicit.Remaining != 0 || explicit.DefaultLimit != 0 {
		t.Fatalf("explicit = %#v", explicit)
	}

	// A range with an end names its records and gets all of them; one with
	// only a start is still capped.
	closed, err := client.Get(ReadRequest{Topic: "messages/capped", Mode: ReadRange, Start: 2, End: DefaultReadLimit + 4})
	if err != nil || len(closed.Records) != DefaultReadLimit+3 || closed.Remaining != 0 {
		t.Fatalf("closed range = %#v, %v", closed, err)
	}
	open, err := client.Get(ReadRequest{Topic: "messages/capped", Mode: ReadRange, Start: 2})
	if err != nil || len(open.Records) != DefaultReadLimit || open.Remaining != 4 {
		t.Fatalf("open range = %#v, %v", open, err)
	}
}

// A full read is how an agent catches up, and what it acts on is the latest
// word: the cap keeps the newest records and counts the older ones.
func TestFullKeepsTheNewestRecordsAndReportsTheRest(t *testing.T) {
	client := newTestClient(t, t.TempDir(), "")
	if _, err := client.Join("writer", "records"); err != nil {
		t.Fatal(err)
	}
	putN(t, client, "messages/capped-full", DefaultReadLimit+3)

	result, err := client.Get(ReadRequest{Topic: "messages/capped-full", Mode: ReadFull})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Records) != DefaultReadLimit || result.Records[0].Index != 4 || result.Remaining != 3 || result.DefaultLimit != DefaultReadLimit {
		t.Fatalf("full = %#v", result)
	}
	whole, err := client.Get(ReadRequest{Topic: "messages/capped-full", Mode: ReadFull, Limit: DefaultReadLimit + 3})
	if err != nil || len(whole.Records) != DefaultReadLimit+3 || whole.Remaining != 0 {
		t.Fatalf("explicit limit = %#v, %v", whole, err)
	}
}

// Join hands every topic over the same way a full read does, so a busy bus
// cannot bury the newcomer under its whole history.
func TestJoinSnapshotCapsEachTopicToItsNewest(t *testing.T) {
	home := t.TempDir()
	writer := joined(t, home, "writer")
	putN(t, writer, "messages/busy", DefaultReadLimit+2)

	snapshot, err := newTestClient(t, home, "").Join("late", "review")
	if err != nil {
		t.Fatal(err)
	}
	busy := snapshot.Topics["messages/busy"]
	if len(busy.Records) != DefaultReadLimit || busy.Records[0].Index != 3 || busy.Remaining != 2 {
		t.Fatalf("busy topic = %#v", busy)
	}
}

func TestDeltaDefaultsToReadLimitAndReportsRemaining(t *testing.T) {
	home := t.TempDir()
	writer := joined(t, home, "writer")
	client := joined(t, home, "reader", "messages/capped-delta")
	// One publication per Put, so this backlog spans more atomic publications
	// than the default cap.
	putN(t, writer, "messages/capped-delta", DefaultReadLimit+4)

	result, err := client.Get(ReadRequest{Topic: "messages/capped-delta", Mode: ReadDelta})
	if err != nil {
		t.Fatal(err)
	}
	delivered := 0
	for _, publication := range result.Publications {
		delivered += len(publication.Records)
	}
	if delivered != DefaultReadLimit || result.Remaining != 4 || result.DefaultLimit != DefaultReadLimit {
		t.Fatalf("delta = %#v, delivered = %d", result, delivered)
	}
}

// An agent's own publications are not unread to it, but reading past them
// moves its cursor all the same.
func TestDeltaPassesOverTheReadersOwnPublications(t *testing.T) {
	home := t.TempDir()
	writer := joined(t, home, "writer")
	client := joined(t, home, "reader", "dev/tasks")
	for _, author := range []*Client{writer, client, writer, client} {
		if _, err := author.Put("dev/tasks", author.Agent); err != nil {
			t.Fatal(err)
		}
	}
	delta, err := client.Get(ReadRequest{Topic: "dev/tasks", Mode: ReadDelta})
	if err != nil || len(delta.Publications) != 2 || delta.Publications[1].Sequence != 3 || delta.maxSequence != 4 {
		t.Fatalf("delta = %#v, error = %v", delta, err)
	}
}

// A publication is never split, so a delta limited below the size of its
// first publication still returns that publication whole.
func TestDeltaAlwaysIncludesFirstOversizedPublicationWhole(t *testing.T) {
	home := t.TempDir()
	writer := joined(t, home, "writer")
	client := joined(t, home, "reader", "messages/big-batch")
	if _, err := writer.Publish("messages/big-batch", creates(3, "x")); err != nil {
		t.Fatal(err)
	}

	result, err := client.Get(ReadRequest{Topic: "messages/big-batch", Mode: ReadDelta, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Publications) != 1 || len(result.Publications[0].Records) != 3 {
		t.Fatalf("expected the single oversized publication delivered whole, got %#v", result)
	}
	if result.Remaining != 0 || result.DefaultLimit != 0 {
		t.Fatalf("expected no remaining backlog after the one oversized publication, got remaining=%d default=%d", result.Remaining, result.DefaultLimit)
	}
}
