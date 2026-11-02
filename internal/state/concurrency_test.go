package state

import (
	"fmt"
	"sync"
	"testing"
)

func TestConcurrentCreatesAreUniqueWithinTopic(t *testing.T) {
	home := t.TempDir()
	clients := make([]*Client, 12)
	for i := range clients {
		client := newTestClient(t, home, "")
		name := fmt.Sprintf("agent-%02d", i)
		if _, err := client.Join(name, "test"); err != nil {
			t.Fatal(err)
		}
		clients[i] = client
	}
	indexes := make(chan int64, len(clients))
	errors := make(chan error, len(clients))
	var wg sync.WaitGroup
	for i, client := range clients {
		wg.Add(1)
		go func(i int, client *Client) {
			defer wg.Done()
			dispatch, err := client.Put("parallel/shared", fmt.Sprintf("record-%02d", i))
			if err != nil {
				errors <- fmt.Errorf("agent %d: %w", i, err)
				return
			}
			indexes <- dispatch.Record().Index
		}(i, client)
	}
	wg.Wait()
	close(indexes)
	close(errors)
	for err := range errors {
		t.Fatal(err)
	}
	seen := map[int64]bool{}
	for index := range indexes {
		if seen[index] {
			t.Fatalf("duplicate record index %d", index)
		}
		seen[index] = true
	}
	if len(seen) != len(clients) {
		t.Fatalf("created %d records, want %d", len(seen), len(clients))
	}
}

func TestCommitSequenceAndRecordIndexesAreIndependent(t *testing.T) {
	client := newTestClient(t, t.TempDir(), "")
	if _, err := client.Join("writer", "records"); err != nil {
		t.Fatal(err)
	}
	first, err := client.Put("group/one", "first")
	if err != nil {
		t.Fatal(err)
	}
	// An edit is a new publication of the same record.
	edited, err := client.Edit("group/one", first.Record().Index, "edited")
	if err != nil {
		t.Fatal(err)
	}
	if edited.Record().Index != first.Record().Index || edited.Sequence != first.Sequence+1 {
		t.Fatalf("commit sequences conflated with records: %#v then %#v", first, edited)
	}
}
