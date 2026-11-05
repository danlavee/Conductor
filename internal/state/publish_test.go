package state

import (
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

func TestATornHistoryTailIsNeverAPublication(t *testing.T) {
	client := newTestClient(t, t.TempDir(), "")
	if _, err := client.Join("writer", "records"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Put("dev/tasks", "kept"); err != nil {
		t.Fatal(err)
	}
	dir, err := client.topicDir("dev/tasks")
	if err != nil {
		t.Fatal(err)
	}
	history, err := os.OpenFile(filepath.Join(dir, "history.jsonl"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := history.WriteString(`{"sequence":99,"top`); err != nil {
		t.Fatal(err)
	}
	history.Close()
	read := func() []Record {
		t.Helper()
		result, err := client.Get(ReadRequest{Topic: "dev/tasks", Mode: ReadFull})
		if err != nil {
			t.Fatal(err)
		}
		return result.Records
	}
	if records := read(); len(records) != 1 {
		t.Fatalf("records = %#v; want the torn tail ignored", records)
	}
	if _, err := client.Put("dev/tasks", "after"); err != nil {
		t.Fatal(err)
	}
	if records := read(); len(records) != 2 || records[1].Text != "after" {
		t.Fatalf("records = %#v; want the append to replace the torn tail", records)
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

func TestPublishReturnsNoRecordWhenItFails(t *testing.T) {
	home := t.TempDir()
	client := newTestClient(t, home, "")
	if _, err := client.Join("writer", "records"); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, "topics", "dev", "tasks", "history.jsonl"), 0o700); err != nil {
		t.Fatal(err)
	}
	dispatch, err := client.Put("dev/tasks", "not visible")
	if err == nil || dispatch.Sequence != 0 || dispatch.Record() != (Record{}) {
		t.Fatalf("result = %#v, error = %v", dispatch, err)
	}
}

// Sequences order publications within a topic, so each topic counts its own.
func TestSequencesArePerTopic(t *testing.T) {
	client := newTestClient(t, t.TempDir(), "")
	if _, err := client.Join("writer", "records"); err != nil {
		t.Fatal(err)
	}
	for _, step := range []struct {
		topic string
		want  int64
	}{{"dev/a", 1}, {"dev/b", 1}, {"dev/a", 2}} {
		dispatch, err := client.Put(step.topic, "x")
		if err != nil || dispatch.Sequence != step.want {
			t.Fatalf("%s sequence = %d, want %d (%v)", step.topic, dispatch.Sequence, step.want, err)
		}
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

// "Tell X" must reach X with nothing set up by either side, and nobody else:
// a private topic is the bus's only form of address.
func TestPrivateTopicReachesItsOwnerAlone(t *testing.T) {
	home := t.TempDir()
	sender := joined(t, home, "sender")
	joined(t, home, "target")
	joined(t, home, "bystander")

	dispatch, err := sender.Put("private/target", "for target only")
	if err != nil {
		t.Fatal(err)
	}
	if len(dispatch.Recipients) != 1 || dispatch.Recipients[0] != "target" {
		t.Fatalf("recipients = %#v; want exactly the owner", dispatch.Recipients)
	}
	news, err := sender.Put("collaboration/broadcast", "for everyone")
	if err != nil {
		t.Fatal(err)
	}
	if len(news.Recipients) != 2 {
		t.Fatalf("broadcast recipients = %#v; want every agent but the publisher", news.Recipients)
	}

	_, err = sender.SubscribeTopic("private/target")
	assertCode(t, err, "INVALID")
	_, err = sender.SubscribeTopicGroup("private")
	assertCode(t, err, "INVALID")
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
