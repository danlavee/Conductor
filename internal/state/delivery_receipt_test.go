package state

import (
	"context"
	"testing"
	"time"
)

func TestReceiptAcknowledgesTheBatchOnce(t *testing.T) {
	owner, err := New(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Join("reader", "reads"); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Join("writer", "writes"); err != nil {
		t.Fatal(err)
	}
	reader, err := Open(owner.Home, "reader")
	if err != nil {
		t.Fatal(err)
	}
	reader.PollInterval = time.Millisecond
	if _, err := reader.SubscribeTopic("dev/tasks"); err != nil {
		t.Fatal(err)
	}
	drainWatch(t, reader)
	writer, err := Open(owner.Home, "writer")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Put("dev/tasks", "hello"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	batch, err := reader.WatchContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := IssueReceipt(reader.Agent, batch)
	if err != nil {
		t.Fatal(err)
	}
	if len(receipt.Batch.Deliveries) == 0 || receipt.Batch.Deliveries[0].Publications[0].Records[0].Text != "hello" {
		t.Fatalf("receipt batch = %+v", receipt.Batch)
	}
	if err := reader.AcknowledgeDeliveryReceipt(receipt); err != nil {
		t.Fatal(err)
	}
	if err := reader.AcknowledgeDeliveryReceipt(receipt); err != nil {
		t.Fatal(err)
	}
	again, err := reader.Get(ReadRequest{Topic: "dev/tasks", Mode: ReadDelta})
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Publications) != 0 {
		t.Fatalf("acked delta still pending: %+v", again.Publications)
	}
}

func drainWatch(t *testing.T, client *Client) {
	t.Helper()
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
		batch, err := client.WatchContext(ctx)
		cancel()
		if err != nil {
			return
		}
		if err := client.AcknowledgeRead(batch.Deliveries...); err != nil {
			t.Fatal(err)
		}
	}
}
