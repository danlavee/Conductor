package state

import (
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestPublishCreatesAndEditsInOnePublication(t *testing.T) {
	client := newTestClient(t, t.TempDir(), "")
	if _, err := client.Join("writer", "records"); err != nil {
		t.Fatal(err)
	}
	first, err := client.Put("dev/tasks", "first")
	if err != nil {
		t.Fatal(err)
	}
	dispatch, err := client.Publish("dev/tasks", []Op{{Text: "second"}, {Index: first.Record().Index, Text: "edited"}, {Text: "third"}})
	if err != nil {
		t.Fatal(err)
	}
	want := []Record{{Index: 1, Text: "edited"}, {Index: 2, Text: "second"}, {Index: 3, Text: "third"}}
	if len(dispatch.Records) != len(want) {
		t.Fatalf("records = %#v, want %#v", dispatch.Records, want)
	}
	for i := range want {
		if dispatch.Records[i] != want[i] {
			t.Fatalf("records = %#v, want %#v", dispatch.Records, want)
		}
	}
	history, err := client.readHistory("dev/tasks")
	if err != nil || len(history) != 2 {
		t.Fatalf("history = %#v, %v; want one publication per call", history, err)
	}
}

func TestEditOfAMissingRecordPublishesNothing(t *testing.T) {
	client := newTestClient(t, t.TempDir(), "")
	if _, err := client.Join("writer", "records"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Edit("dev/tasks", 1, "missing"); err == nil {
		t.Fatal("missing edit succeeded")
	} else {
		assertCode(t, err, "NOT_FOUND")
	}
	// One bad op rejects the whole publication, including the ops beside it.
	if _, err := client.Publish("dev/tasks", []Op{{Text: "new"}, {Index: 7, Text: "missing"}}); err == nil {
		t.Fatal("publication with a missing edit succeeded")
	}
	history, err := client.readHistory("dev/tasks")
	if err != nil || len(history) != 0 {
		t.Fatalf("rejected operations published history: %#v, %v", history, err)
	}
	if _, err := client.Put("dev/tasks", "works after failure"); err != nil {
		t.Fatalf("failed publication left the topic locked: %v", err)
	}
}

func TestEditsStayInTheirTopic(t *testing.T) {
	client := newTestClient(t, t.TempDir(), "")
	if _, err := client.Join("writer", "records"); err != nil {
		t.Fatal(err)
	}
	alpha, err := client.Put("group/alpha", "alpha")
	if err != nil {
		t.Fatal(err)
	}
	beta, err := client.Put("group/beta", "beta")
	if err != nil || beta.Record().Index != alpha.Record().Index {
		t.Fatalf("beta = %#v, alpha = %#v, %v", beta, alpha, err)
	}
	if _, err := client.Edit("group/alpha", alpha.Record().Index, "changed"); err != nil {
		t.Fatal(err)
	}
	full, err := client.Get(ReadRequest{Topic: "group/beta", Mode: ReadFull})
	if err != nil || len(full.Records) != 1 || full.Records[0] != beta.Record() {
		t.Fatalf("other topic changed: %#v, %v", full, err)
	}
}

func TestTextIsPreservedExactly(t *testing.T) {
	client := newTestClient(t, t.TempDir(), "")
	if _, err := client.Join("writer", "records"); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"", "   ", "first\nsecond", "שלום 🌍"} {
		dispatch, err := client.Put("messages/exact", text)
		if err != nil || dispatch.Record().Text != text {
			t.Fatalf("put %#v = %#v, %v", text, dispatch, err)
		}
		edited := text + "\nchanged"
		dispatch, err = client.Edit("messages/exact", dispatch.Record().Index, edited)
		if err != nil || dispatch.Record().Text != edited {
			t.Fatalf("edit %#v = %#v, %v", edited, dispatch, err)
		}
	}
}

// A publication that landed but whose wake failed is readable and announced
// to nobody, and its answer says both.
func TestPublishReturnsTheRecordWhenTheWakeFails(t *testing.T) {
	home := t.TempDir()
	client := newTestClient(t, home, "")
	if _, err := client.Join("writer", "records"); err != nil {
		t.Fatal(err)
	}
	// Registration itself already consumes global sequences 1 (the
	// collaboration/agents roster record) and 2 (join); this Put's own event
	// lands at 3.
	if err := os.Mkdir(filepath.Join(home, "events", indexName(3)), 0o700); err != nil {
		t.Fatal(err)
	}
	dispatch, err := client.Put("dev/tasks", "visible")
	record := dispatch.Record()
	if err == nil || record.Index != 1 || record.Text != "visible" || len(dispatch.Recipients) != 0 {
		t.Fatalf("result = %#v, error = %v", dispatch, err)
	}
	full, err := client.Get(ReadRequest{Topic: "dev/tasks", Mode: ReadFull})
	if err != nil || len(full.Records) != 1 || full.Records[0] != record {
		t.Fatalf("full = %#v, %v", full, err)
	}
}

func TestPublishReturnsNoRecordWhenItFailsBeforeVisibility(t *testing.T) {
	home := t.TempDir()
	client := newTestClient(t, home, "")
	if _, err := client.Join("writer", "records"); err != nil {
		t.Fatal(err)
	}
	if err := writeJSONAtomic(filepath.Join(home, "state", "index.json"), map[string]int64{"index": math.MaxInt64}); err != nil {
		t.Fatal(err)
	}
	dispatch, err := client.Put("dev/tasks", "not visible")
	// Sequence zero is what the CLI branches on to decide the record never
	// became visible, so the contract is pinned there and not only on Record.
	if err == nil || dispatch.Sequence != 0 || dispatch.Record() != (Record{}) {
		t.Fatalf("result = %#v, error = %v", dispatch, err)
	}
	history, historyErr := client.readHistory("dev/tasks")
	if historyErr != nil || len(history) != 0 {
		t.Fatalf("history = %#v, %v", history, historyErr)
	}
}

func TestTextIdenticalEditStillPublishes(t *testing.T) {
	client := newTestClient(t, t.TempDir(), "")
	if _, err := client.Join("writer", "records"); err != nil {
		t.Fatal(err)
	}
	record, err := client.Put("dev/tasks", "same")
	if err != nil {
		t.Fatal(err)
	}
	edited, err := client.Edit("dev/tasks", record.Record().Index, record.Record().Text)
	if err != nil || edited.Record() != record.Record() {
		t.Fatalf("edit = %#v, %v", edited, err)
	}
	history, err := client.readHistory("dev/tasks")
	if err != nil || len(history) != 2 {
		t.Fatalf("history = %#v, %v", history, err)
	}
}

// A publisher subscribed to its own topic looks addressed and reaches nobody.
// The dispatch must not hand the publisher its own name back, or an agent
// alone on a topic it created reads a non-empty audience and stops there.
func TestDispatchNeverNamesThePublisher(t *testing.T) {
	home := t.TempDir()
	writer := newTestClient(t, home, "")
	if _, err := writer.Join("writer", "records"); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.SubscribeTopic("solo/notes"); err != nil {
		t.Fatal(err)
	}
	alone, err := writer.Put("solo/notes", "anyone there")
	if err != nil {
		t.Fatal(err)
	}
	if len(alone.Recipients) != 0 {
		t.Fatalf("recipients = %#v; want none -- the publisher is not its own audience", alone.Recipients)
	}

	// The same act with a peer subscribed names the peer and only the peer,
	// so the emptiness above is the exclusion and not a broken lookup.
	reader := newTestClient(t, home, "")
	if _, err := reader.Join("reader", "review"); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.SubscribeTopic("solo/notes"); err != nil {
		t.Fatal(err)
	}
	heard, err := writer.Put("solo/notes", "still there?")
	if err != nil {
		t.Fatal(err)
	}
	if len(heard.Recipients) != 1 || heard.Recipients[0] != "reader" {
		t.Fatalf("recipients = %#v; want exactly the peer", heard.Recipients)
	}

	// Broadcast topics take the same rule: every registered agent but the one
	// publishing.
	broadcast, err := writer.Put("collaboration/rules", "a rule")
	if err != nil {
		t.Fatal(err)
	}
	if len(broadcast.Recipients) != 1 || broadcast.Recipients[0] != "reader" {
		t.Fatalf("broadcast recipients = %#v; want every registered agent but the publisher", broadcast.Recipients)
	}
}
