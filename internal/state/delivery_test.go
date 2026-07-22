package state

import "testing"

func TestParseDeliveryMode(t *testing.T) {
	if mode, err := ParseDeliveryMode(""); err != nil || mode != DeliveryContent {
		t.Fatalf("default mode = %v, %v", mode, err)
	}
	if mode, err := ParseDeliveryMode("summary"); err != nil || mode != DeliverySummary {
		t.Fatalf("summary mode = %v, %v", mode, err)
	}
	if _, err := ParseDeliveryMode("bogus"); err == nil {
		t.Fatal("expected error for unknown mode")
	}
}

func TestSummaryAcknowledgesReferencedTopicChange(t *testing.T) {
	home := t.TempDir()
	client := newTestClient(t, home, "")
	if _, err := client.Register("reader", "review"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.SubscribeTopic("messages/team"); err != nil {
		t.Fatal(err)
	}
	join, err := client.Watch()
	if err != nil {
		t.Fatal(err)
	}
	if err := client.AcknowledgeSummary(join); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Put("messages/team", "hello"); err != nil {
		t.Fatal(err)
	}
	summary, err := client.Watch()
	if err != nil {
		t.Fatal(err)
	}
	delivery, err := client.ResolveDelivery(summary, DeliverySummary)
	if err != nil || delivery.Delta != nil {
		t.Fatalf("delivery = %#v, error = %v", delivery, err)
	}
	if err := client.AcknowledgeDelivery(delivery); err != nil {
		t.Fatal(err)
	}
	delta, err := client.Get(ReadRequest{Topic: "messages/team", Mode: ReadDelta})
	if err != nil || len(delta.Publications) != 0 {
		t.Fatalf("delta = %#v, error = %v", delta, err)
	}
}

func TestContentCarriesChangedRecordsAndAcknowledgesDelta(t *testing.T) {
	home := t.TempDir()
	reader := newTestClient(t, home, "")
	if _, err := reader.Register("reader", "review"); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.SubscribeTopic("dev/tasks"); err != nil {
		t.Fatal(err)
	}
	writer := newTestClient(t, home, "")
	if _, err := writer.Register("writer", "dev"); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		summary, err := reader.Watch()
		if err != nil {
			t.Fatal(err)
		}
		if err := reader.AcknowledgeSummary(summary); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Begin("dev/tasks"); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.StagePut("one"); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.StagePut("two"); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Commit(); err != nil {
		t.Fatal(err)
	}
	summary, err := reader.Watch()
	if err != nil {
		t.Fatal(err)
	}
	delivery, err := reader.ResolveDelivery(summary, DeliveryContent)
	if err != nil || delivery.Delta == nil || len(delivery.Delta.Publications) != 1 || len(delivery.Delta.Publications[0].Records) != 2 {
		t.Fatalf("delivery = %#v, error = %v", delivery, err)
	}
	if err := reader.AcknowledgeDelivery(delivery); err != nil {
		t.Fatal(err)
	}
	delta, err := reader.Get(ReadRequest{Topic: "dev/tasks", Mode: ReadDelta})
	if err != nil || len(delta.Publications) != 0 {
		t.Fatalf("delta replayed: %#v, error = %v", delta, err)
	}
}

func TestContentJoinCarriesRoster(t *testing.T) {
	client := newTestClient(t, t.TempDir(), "")
	if _, err := client.Register("a", "dev"); err != nil {
		t.Fatal(err)
	}
	summary, err := client.Watch()
	if err != nil {
		t.Fatal(err)
	}
	delivery, err := client.ResolveDelivery(summary, DeliveryContent)
	if err != nil || len(delivery.Roster) != 1 || delivery.Roster[0].Name != "a" {
		t.Fatalf("delivery = %#v, error = %v", delivery, err)
	}
}
