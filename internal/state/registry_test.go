package state

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRegisterPutGetAndDiskState(t *testing.T) {
	home := t.TempDir()
	client := newTestClient(t, home, "")
	snapshot, err := client.Register("a", "dev")
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Agents) != 1 || snapshot.Agents[0].Name != "a" {
		t.Fatalf("unexpected registration snapshot: %#v", snapshot)
	}

	record, err := client.Put("messages/team", "done")
	if err != nil {
		t.Fatal(err)
	}
	if record.Index != 1 {
		t.Fatalf("record index = %d, want 1", record.Index)
	}

	result, err := client.Get(ReadRequest{Topic: "messages/team", RecordIndex: record.Index, Mode: ReadFull})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Records) != 1 || result.Records[0].Text != "done" {
		t.Fatalf("unexpected full read: %#v", result)
	}

	assertJSONFile(t, filepath.Join(home, "registry", "a.json"))
	history, err := client.readHistory("messages/team")
	if err != nil {
		t.Fatal(err)
	}
	assertJSONFile(t, filepath.Join(home, "topics", "messages", "team", "history", indexName(history[0].Sequence)))
	assertJSONFile(t, filepath.Join(home, "topics", "messages", "team", "head.json"))
	assertJSONFile(t, filepath.Join(home, "state", "index.json"))
	assertJSONFile(t, filepath.Join(home, "events", indexName(1)))
	assertJSONFile(t, filepath.Join(home, "events", indexName(2)))
}

func TestIdentityIsNeverInferredFromSoleRegistration(t *testing.T) {
	home := t.TempDir()
	registered := newTestClient(t, home, "")
	if _, err := registered.Register("only", "dev"); err != nil {
		t.Fatal(err)
	}
	unbound := newTestClient(t, home, "")
	if _, err := unbound.Put("messages/team", "done"); err == nil || !strings.Contains(err.Error(), "identity is required") {
		t.Fatalf("unbound client silently used sole identity: %v", err)
	}
}

func TestExplicitIdentityRejectsPathTraversal(t *testing.T) {
	client := newTestClient(t, t.TempDir(), `..\victim`)
	if _, err := client.ResolveAgent(); err == nil || !strings.Contains(err.Error(), "invalid name") {
		t.Fatalf("traversing identity accepted: %v", err)
	}
}

func TestNamesArePortableAcrossSupportedPlatforms(t *testing.T) {
	for _, name := range []string{"Alice", "alice.", "con", "com1", "two words"} {
		if err := validName(name); err == nil {
			t.Errorf("non-portable name accepted: %q", name)
		}
	}
	for _, name := range []string{"alice", "agent-01", "task.item"} {
		if err := validName(name); err != nil {
			t.Errorf("portable name rejected: %q: %v", name, err)
		}
	}
}

func TestDuplicateNameCannotOverwriteResponsibility(t *testing.T) {
	home := t.TempDir()
	first := newTestClient(t, home, "")
	if _, err := first.Register("same", "original"); err != nil {
		t.Fatal(err)
	}
	second := newTestClient(t, home, "")
	_, err := second.Register("same", "replacement")
	assertCode(t, err, "LOCKED")
	agents, err := first.ListAgents()
	if err != nil || len(agents) != 1 || agents[0].Responsibility != "original" {
		t.Fatalf("duplicate registration overwrote identity: %#v, %v", agents, err)
	}
}

func TestRegistrationRetryRepairsMissingJoinEvent(t *testing.T) {
	home := t.TempDir()
	client := newTestClient(t, home, "")
	agent := Agent{Name: "retry", Responsibility: "dev", Timestamp: time.Now().UTC()}
	if err := writeJSONAtomic(filepath.Join(home, "registry", "retry.json"), agent); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Register("retry", "dev"); err != nil {
		t.Fatal(err)
	}
	latest, err := client.latestMembershipType("retry")
	if err != nil || latest != "join" {
		t.Fatalf("registration retry did not repair join: %q, %v", latest, err)
	}
}

func TestDeregistrationRetryRepairsMissingLeaveEvent(t *testing.T) {
	home := t.TempDir()
	client := newTestClient(t, home, "")
	if _, err := client.Register("retry", "dev"); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(home, "registry", "retry.json")); err != nil {
		t.Fatal(err)
	}
	if err := client.Deregister("retry"); err != nil {
		t.Fatal(err)
	}
	latest, err := client.latestMembershipType("retry")
	if err != nil || latest != "leave" {
		t.Fatalf("deregistration retry did not repair leave: %q, %v", latest, err)
	}
}

func TestRegistrationSnapshotFailureDoesNotCreateMembership(t *testing.T) {
	home := t.TempDir()
	client := newTestClient(t, home, "")
	resourceDir := filepath.Join(home, "topics", "broken", "state")
	if err := os.MkdirAll(resourceDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(resourceDir, "head.json"), []byte("not-json\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Register("a", "dev"); err == nil {
		t.Fatal("registration unexpectedly succeeded with unreadable snapshot")
	}
	if _, err := os.Stat(filepath.Join(home, "registry", "a.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed registration mutated registry: %v", err)
	}
}

func TestConcurrentRegisterAndDeregisterStayOrdered(t *testing.T) {
	home := t.TempDir()
	initial := newTestClient(t, home, "")
	if _, err := initial.Register("same", "initial"); err != nil {
		t.Fatal(err)
	}
	registering := newTestClient(t, home, "")
	leaving := newTestClient(t, home, "same")
	results := make(chan error, 2)
	go func() {
		_, err := registering.Register("same", "initial")
		results <- err
	}()
	go func() { results <- leaving.Deregister("same") }()
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	events, err := initial.unreadEvents(nil)
	if err != nil {
		t.Fatal(err)
	}
	// collaboration/agents roster commits are interleaved with the registry
	// join/leave events being tested here; filter to membership events only.
	membership := make([]Event, 0, len(events))
	for _, event := range events {
		if event.Summary.Topic == "registry" {
			membership = append(membership, event)
		}
	}
	if len(membership) < 2 || len(membership) > 3 {
		t.Fatalf("membership events = %#v", membership)
	}
	if membership[0].Summary.Type != "join" || membership[1].Summary.Type != "leave" || len(membership) == 3 && membership[2].Summary.Type != "join" {
		t.Fatalf("membership events are not ordered: %#v", membership)
	}
	_, statErr := os.Stat(filepath.Join(home, "registry", "same.json"))
	present := statErr == nil
	lastType := membership[len(membership)-1].Summary.Type
	if present != (lastType == "join") {
		t.Fatalf("registry present=%v but last membership event=%s", present, lastType)
	}
}

func TestConcurrentBeginAndDeregisterCannotStrandTransaction(t *testing.T) {
	home := t.TempDir()
	client := newTestClient(t, home, "")
	if _, err := client.Register("same", "development"); err != nil {
		t.Fatal(err)
	}
	beginning := newTestClient(t, home, "same")
	leaving := newTestClient(t, home, "same")
	start := make(chan struct{})
	results := make(chan struct {
		operation string
		err       error
	}, 2)
	go func() {
		<-start
		results <- struct {
			operation string
			err       error
		}{"begin", beginning.Begin("dev/tasks")}
	}()
	go func() {
		<-start
		results <- struct {
			operation string
			err       error
		}{"deregister", leaving.Deregister("same")}
	}()
	close(start)
	outcomes := map[string]error{}
	for range 2 {
		result := <-results
		outcomes[result.operation] = result.err
	}
	if outcomes["begin"] == nil && outcomes["deregister"] == nil {
		t.Fatal("begin and deregister both succeeded")
	}
	if outcomes["begin"] == nil {
		assertCode(t, outcomes["deregister"], "LOCKED")
		if err := beginning.Abort(); err != nil {
			t.Fatal(err)
		}
	} else if outcomes["deregister"] != nil {
		t.Fatalf("neither operation completed: begin=%v deregister=%v", outcomes["begin"], outcomes["deregister"])
	}
}
