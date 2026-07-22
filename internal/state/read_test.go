package state

import "testing"

func TestReadModesAndIndependentCursors(t *testing.T) {
	home := t.TempDir()
	writer := newTestClient(t, home, "")
	if _, err := writer.Register("writer", "dev"); err != nil {
		t.Fatal(err)
	}
	reader := newTestClient(t, home, "")
	if _, err := reader.Register("reader", "review"); err != nil {
		t.Fatal(err)
	}
	for _, messages := range []map[string]MessageMutation{
		{"one": testMessage("1")},
		{"two": testMessage("2")},
		{"one": testMessage("3")},
	} {
		if _, err := writer.Put("dev/tasks", messages); err != nil {
			t.Fatal(err)
		}
	}

	one, err := reader.Get(ReadRequest{Resource: "dev/tasks", Key: "one", Mode: ReadDelta})
	if err != nil {
		t.Fatal(err)
	}
	if len(one.History) != 2 {
		t.Fatalf("key delta count = %d, want 2", len(one.History))
	}
	if err := reader.AcknowledgeRead(one); err != nil {
		t.Fatal(err)
	}
	two, err := reader.Get(ReadRequest{Resource: "dev/tasks", Key: "two", Mode: ReadDelta})
	if err != nil {
		t.Fatal(err)
	}
	if len(two.History) != 1 {
		t.Fatalf("independent key cursor lost update: %#v", two.History)
	}

	historical, err := reader.Get(ReadRequest{Resource: "dev/tasks", Mode: ReadHistorical, From: 4, To: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(historical.History) != 2 || historical.History[0].Index != 4 || historical.History[1].Index != 5 {
		t.Fatalf("historical endpoints are not inclusive: %#v", historical.History)
	}
}

func TestConcurrentCursorAcknowledgementsMerge(t *testing.T) {
	home := t.TempDir()
	client := newTestClient(t, home, "")
	if _, err := client.Register("a", "dev"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Put("dev/tasks", map[string]MessageMutation{"status": testMessage("done")}); err != nil {
		t.Fatal(err)
	}
	read, err := client.Get(ReadRequest{Resource: "dev/tasks", Mode: ReadDelta})
	if err != nil {
		t.Fatal(err)
	}
	signal, err := client.Watch()
	if err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 2)
	go func() { results <- client.AcknowledgeRead(read) }()
	go func() { results <- client.AcknowledgeSignal(signal) }()
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	cursor, err := client.loadCursor("a")
	if err != nil {
		t.Fatal(err)
	}
	if cursor.Resources["dev/tasks"] != read.maxIndex || !indexAcknowledged(cursor.SignalRanges, signal.Index) {
		t.Fatalf("concurrent acknowledgements did not merge: %#v", cursor)
	}
}

func TestScratchRemovesCurrentMessageButRemainsInHistory(t *testing.T) {
	home := t.TempDir()
	writer := newTestClient(t, home, "")
	if _, err := writer.Register("writer", "messages"); err != nil {
		t.Fatal(err)
	}
	created, err := writer.Put("collaboration/shared", map[string]MessageMutation{
		"entry": testMessage("draft"),
	})
	if err != nil {
		t.Fatal(err)
	}
	scratched, err := writer.PutWithOptions(
		"collaboration/shared",
		map[string]MessageMutation{"entry": {Operation: MutationScratch}},
		WriteOptions{IfIndex: map[string]int64{"entry": created.Index}},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = writer.Get(ReadRequest{Resource: "collaboration/shared", Key: "entry", Mode: ReadFull})
	assertCode(t, err, "NOT_FOUND")
	history, err := writer.Get(ReadRequest{Resource: "collaboration/shared", Key: "entry", Mode: ReadHistorical, From: created.Index, To: scratched.Index})
	if err != nil {
		t.Fatal(err)
	}
	if len(history.History) != 2 || history.History[1].Messages["entry"].Operation != MutationScratch {
		t.Fatalf("scratch missing from history: %#v", history.History)
	}
	newcomer := newTestClient(t, home, "")
	snapshot, err := newcomer.Register("newcomer", "review")
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := snapshot.Resources["collaboration/shared"]; exists {
		t.Fatalf("scratched message remained in current snapshot: %#v", snapshot.Resources)
	}
	recreated, err := writer.PutWithOptions(
		"collaboration/shared",
		map[string]MessageMutation{"entry": testMessage("recreated")},
		WriteOptions{IfIndex: map[string]int64{"entry": scratched.Index}},
	)
	if err != nil {
		t.Fatal(err)
	}
	current, err := writer.Get(ReadRequest{Resource: "collaboration/shared", Key: "entry", Mode: ReadFull})
	if err != nil || len(current.Messages) != 1 || current.Messages[0].Index != recreated.Index || current.Messages[0].Payload.Text != "recreated" {
		t.Fatalf("recreated message = %#v, %v", current, err)
	}
}

func TestMessageKindIsUnrestrictedAndPayloadIsText(t *testing.T) {
	client := newTestClient(t, t.TempDir(), "")
	if _, err := client.Register("writer", "messages"); err != nil {
		t.Fatal(err)
	}
	payload := MessagePayload{Text: "plain text"}
	_, err := client.Put("messages/kinds", map[string]MessageMutation{
		"entry": {Operation: MutationSet, Kind: "team-defined kind / 任意", Payload: &payload},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Get(ReadRequest{Resource: "messages/kinds", Key: "entry", Mode: ReadFull})
	if err != nil || len(result.Messages) != 1 || result.Messages[0].Kind != "team-defined kind / 任意" || result.Messages[0].Payload.Text != "plain text" {
		t.Fatalf("message = %#v, %v", result, err)
	}
}
