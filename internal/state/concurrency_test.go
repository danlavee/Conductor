package state

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestConcurrentCreatesAreUniqueWithinTopic(t *testing.T) {
	home := t.TempDir()
	clients := make([]*Client, 12)
	for i := range clients {
		client := newTestClient(t, home, "")
		name := fmt.Sprintf("agent-%02d", i)
		if _, err := client.Register(name, "test"); err != nil {
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
			for attempt := 0; attempt < 200; attempt++ {
				record, err := client.Put("parallel/shared", fmt.Sprintf("record-%02d", i))
				if err == nil {
					indexes <- record.Index
					return
				}
				time.Sleep(time.Millisecond)
			}
			errors <- fmt.Errorf("agent %d never acquired topic", i)
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
	if _, err := client.Register("writer", "records"); err != nil {
		t.Fatal(err)
	}
	first, err := client.Put("group/one", "first")
	if err != nil {
		t.Fatal(err)
	}
	second, err := client.Put("group/two", "second")
	if err != nil {
		t.Fatal(err)
	}
	if first.Index != 1 || second.Index != 1 {
		t.Fatalf("record indexes are not topic-local: %#v %#v", first, second)
	}
	one, err := client.readHistory("group/one")
	if err != nil {
		t.Fatal(err)
	}
	two, err := client.readHistory("group/two")
	if err != nil {
		t.Fatal(err)
	}
	if one[0].Sequence == first.Index || two[0].Sequence == second.Index || one[0].Sequence == two[0].Sequence {
		t.Fatalf("commit sequences conflated with records: one=%d two=%d", one[0].Sequence, two[0].Sequence)
	}
}
