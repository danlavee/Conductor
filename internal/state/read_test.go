package state

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestReadModesAndIndependentRecordCursors(t *testing.T) {
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
	first, err := writer.Put("dev/tasks", "one")
	if err != nil {
		t.Fatal(err)
	}
	second, err := writer.Put("dev/tasks", "two")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Edit("dev/tasks", first.Index, "one-updated"); err != nil {
		t.Fatal(err)
	}
	delta, err := reader.Get(ReadRequest{Topic: "dev/tasks", RecordIndex: first.Index, Mode: ReadDelta})
	if err != nil || len(delta.Publications) != 2 || delta.Publications[0].Records[0].Index != first.Index || delta.Publications[1].Records[0].Index != first.Index {
		t.Fatalf("delta = %#v, error = %v", delta, err)
	}
	if err := reader.AcknowledgeRead(delta); err != nil {
		t.Fatal(err)
	}
	other, err := reader.Get(ReadRequest{Topic: "dev/tasks", RecordIndex: second.Index, Mode: ReadDelta})
	if err != nil || len(other.Publications) != 1 || other.Publications[0].Records[0].Index != second.Index {
		t.Fatalf("independent delta = %#v, error = %v", other, err)
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
	client := newTestClient(t, t.TempDir(), "")
	if _, err := client.Join("writer", "records"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.SubscribeTopic("collaboration/shared"); err != nil {
		t.Fatal(err)
	}
	record, err := client.Put("collaboration/shared", "plain")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Strike("collaboration/shared", record.Index); err != nil {
		t.Fatal(err)
	}
	full, err := client.Get(ReadRequest{Topic: "collaboration/shared", RecordIndex: record.Index, Mode: ReadFull})
	if err != nil || len(full.Records) != 1 || full.Records[0].Text != "~~plain~~" {
		t.Fatalf("full = %#v, error = %v", full, err)
	}
	delta, err := client.Get(ReadRequest{Topic: "collaboration/shared", RecordIndex: record.Index, Mode: ReadDelta})
	if err != nil || len(delta.Publications) != 2 || delta.Publications[1].Records[0].Text != "~~plain~~" {
		t.Fatalf("delta = %#v, error = %v", delta, err)
	}
}

// putN writes count records to topic and returns the client that wrote them,
// so a test can build a backlog larger than DefaultReadLimit without
// spelling out every record.
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
}

func TestFullDefaultsToReadLimitAndReportsRemaining(t *testing.T) {
	client := newTestClient(t, t.TempDir(), "")
	if _, err := client.Join("writer", "records"); err != nil {
		t.Fatal(err)
	}
	putN(t, client, "messages/capped-full", DefaultReadLimit+3)

	result, err := client.Get(ReadRequest{Topic: "messages/capped-full", Mode: ReadFull})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Records) != DefaultReadLimit || result.Remaining != 3 || result.DefaultLimit != DefaultReadLimit {
		t.Fatalf("full = %#v", result)
	}
}

func TestDeltaDefaultsToReadLimitAndReportsRemaining(t *testing.T) {
	client := newTestClient(t, t.TempDir(), "")
	if _, err := client.Join("writer", "records"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.SubscribeTopic("messages/capped-delta"); err != nil {
		t.Fatal(err)
	}
	// One publication per Put, so this backlog spans more atomic publications
	// than the default cap.
	putN(t, client, "messages/capped-delta", DefaultReadLimit+4)

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

func TestDeltaAlwaysIncludesFirstOversizedPublicationWhole(t *testing.T) {
	client := newTestClient(t, t.TempDir(), "")
	if _, err := client.Join("writer", "records"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.SubscribeTopic("messages/big-batch"); err != nil {
		t.Fatal(err)
	}
	lines := ""
	for i := 0; i < DefaultReadLimit+2; i++ {
		encoded, err := json.Marshal("x")
		if err != nil {
			t.Fatal(err)
		}
		lines += string(encoded) + "\n"
	}
	path := filepath.Join(t.TempDir(), "bulk.jsonl")
	if err := os.WriteFile(path, []byte(lines), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := client.Begin("messages/big-batch"); err != nil {
		t.Fatal(err)
	}
	scanner := bufio.NewScanner(bytes.NewReader([]byte(lines)))
	for scanner.Scan() {
		var text string
		if err := json.Unmarshal(scanner.Bytes(), &text); err != nil {
			t.Fatal(err)
		}
		if _, err := client.StagePut(text); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := client.Commit(); err != nil {
		t.Fatal(err)
	}

	result, err := client.Get(ReadRequest{Topic: "messages/big-batch", Mode: ReadDelta})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Publications) != 1 || len(result.Publications[0].Records) != DefaultReadLimit+2 {
		t.Fatalf("expected the single oversized publication delivered whole, got %#v", result)
	}
	if result.Remaining != 0 || result.DefaultLimit != 0 {
		t.Fatalf("expected no remaining backlog after the one oversized publication, got remaining=%d default=%d", result.Remaining, result.DefaultLimit)
	}
}
