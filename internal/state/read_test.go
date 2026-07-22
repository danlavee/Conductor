package state

import "testing"

func TestReadModesAndIndependentRecordCursors(t *testing.T) {
	home := t.TempDir()
	writer := newTestClient(t, home, "")
	if _, err := writer.Register("writer", "dev"); err != nil {
		t.Fatal(err)
	}
	reader := newTestClient(t, home, "")
	if _, err := reader.Register("reader", "review"); err != nil {
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
	if _, err := client.Register("writer", "records"); err != nil {
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
	if _, err := client.Register("writer", "records"); err != nil {
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
