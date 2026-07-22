package state

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTwelveAgentsAndConcurrentGlobalIndexes(t *testing.T) {
	home := t.TempDir()
	clients := make([]*Client, 12)
	for index := range clients {
		name := fmt.Sprintf("agent-%02d", index)
		clients[index] = newTestClient(t, home, "")
		if _, err := clients[index].Register(name, "test"); err != nil {
			t.Fatal(err)
		}
	}
	agents, err := clients[0].ListAgents()
	if err != nil {
		t.Fatal(err)
	}
	if len(agents) != 12 {
		t.Fatalf("registered agents = %d, want 12", len(agents))
	}

	errorsByAgent := make(chan error, len(clients))
	for index, client := range clients {
		go func(index int, client *Client) {
			client.Agent = fmt.Sprintf("agent-%02d", index)
			_, err := client.Put(fmt.Sprintf("parallel/topic-%02d", index), map[string]MessageMutation{"status": testMessage("done")})
			errorsByAgent <- err
		}(index, client)
	}
	for range clients {
		if err := <-errorsByAgent; err != nil {
			t.Fatal(err)
		}
	}

	seen := map[int64]bool{}
	for index := range clients {
		history, err := clients[index].readHistory(fmt.Sprintf("parallel/topic-%02d", index))
		if err != nil || len(history) != 1 {
			t.Fatalf("history %d = %#v, %v", index, history, err)
		}
		if seen[history[0].Index] {
			t.Fatalf("duplicate global index %d", history[0].Index)
		}
		seen[history[0].Index] = true
	}
	inbox, err := os.Open(filepath.Join(home, "inbox", "agent-00"))
	if err != nil {
		t.Fatal(err)
	}
	defer inbox.Close()
	scanner := bufio.NewScanner(inbox)
	for scanner.Scan() {
		var signal Signal
		if err := json.Unmarshal(scanner.Bytes(), &signal); err != nil {
			t.Fatalf("concurrent inbox append produced malformed JSON: %s", scanner.Bytes())
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentExpiredTakeoverKeepsSingleWriter(t *testing.T) {
	home := t.TempDir()
	owner := newTestClient(t, home, "")
	if _, err := owner.Register("owner", "development"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"first", "second"} {
		client := newTestClient(t, home, "")
		if _, err := client.Register(name, "recovery"); err != nil {
			t.Fatal(err)
		}
	}
	if err := owner.Begin("dev/tasks"); err != nil {
		t.Fatal(err)
	}
	if err := owner.Set("recovered", "test", "yes"); err != nil {
		t.Fatal(err)
	}
	var lock Lock
	if err := readJSON(owner.writeLockPath("dev/tasks"), &lock); err != nil {
		t.Fatal(err)
	}
	lock.PID = 999999
	lock.ProcessStart = "dead"
	lock.Timestamp = time.Now().Add(-time.Minute)
	lock.TimeoutSec = 1
	if err := writeJSONAtomic(owner.writeLockPath("dev/tasks"), lock); err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	results := make(chan error, 2)
	for _, name := range []string{"first", "second"} {
		go func(name string) {
			client := newTestClient(t, home, name)
			client.LockTimeout = time.Second
			<-start
			_, err := client.Put("dev/tasks", map[string]MessageMutation{name: testMessage("done")})
			results <- err
		}(name)
	}
	close(start)
	successes := 0
	for range 2 {
		if err := <-results; err == nil {
			successes++
		} else {
			var protocol *ProtocolError
			if !errors.As(err, &protocol) || (protocol.Code != "LOCKED" && protocol.Code != "TIMEOUT") {
				t.Fatalf("unexpected contender error: %v", err)
			}
		}
	}
	if successes == 0 {
		t.Fatal("no contender completed recovery")
	}
	history, err := owner.readHistory("dev/tasks")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[int64]bool{}
	for _, change := range history {
		if seen[change.Index] {
			t.Fatalf("duplicate index after recovery: %d", change.Index)
		}
		seen[change.Index] = true
	}
}

func TestConcurrentConditionalEditsPublishOnce(t *testing.T) {
	home := t.TempDir()
	first := newTestClient(t, home, "")
	if _, err := first.Register("first", "development"); err != nil {
		t.Fatal(err)
	}
	second := newTestClient(t, home, "")
	if _, err := second.Register("second", "review"); err != nil {
		t.Fatal(err)
	}
	created, err := first.Put("dev/tasks", map[string]MessageMutation{"task": testMessage("initial")})
	if err != nil {
		t.Fatal(err)
	}
	conditions := WriteOptions{IfIndex: map[string]int64{"task": created.Index}}
	start := make(chan struct{})
	type outcome struct {
		client *Client
		value  string
		change Publication
		err    error
	}
	results := make(chan outcome, 2)
	for client, value := range map[*Client]string{first: "one", second: "two"} {
		go func(client *Client, value string) {
			<-start
			change, err := client.PutWithOptions("dev/tasks", map[string]MessageMutation{"task": testMessage(value)}, conditions)
			results <- outcome{client: client, value: value, change: change, err: err}
		}(client, value)
	}
	close(start)
	outcomes := []outcome{<-results, <-results}
	successes := 0
	for index := range outcomes {
		if outcomes[index].err == nil {
			successes++
			continue
		}
		var protocol *ProtocolError
		if !errors.As(outcomes[index].err, &protocol) || (protocol.Code != "LOCKED" && protocol.Code != "CONFLICT") {
			t.Fatalf("conditional outcome = %v", outcomes[index].err)
		}
		if protocol.Code == "LOCKED" {
			_, retryErr := outcomes[index].client.PutWithOptions("dev/tasks", map[string]MessageMutation{"task": testMessage(outcomes[index].value)}, conditions)
			if !errors.As(retryErr, &protocol) || protocol.Code != "CONFLICT" {
				t.Fatalf("retry error = %v", retryErr)
			}
		}
	}
	if successes != 1 {
		t.Fatalf("successful conditional edits = %d", successes)
	}
	history, err := first.readHistory("dev/tasks")
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 2 {
		t.Fatalf("history publications = %d", len(history))
	}
	eventEntries, err := os.ReadDir(filepath.Join(home, "events"))
	if err != nil {
		t.Fatal(err)
	}
	winnerSignals := 0
	for _, entry := range eventEntries {
		var event Event
		if err := readJSON(filepath.Join(home, "events", entry.Name()), &event); err != nil {
			t.Fatal(err)
		}
		if event.Signal.Type == "update" && event.Signal.Resource == "dev/tasks" && event.Signal.Index > created.Index {
			winnerSignals++
		}
	}
	if winnerSignals != 1 {
		t.Fatalf("winner update signals = %d", winnerSignals)
	}
}
