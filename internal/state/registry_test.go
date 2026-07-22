package state

import (
	"errors"
	"fmt"
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

	change, err := client.Put("messages/team", map[string]MessageMutation{"status": testMessage("done")})
	if err != nil {
		t.Fatal(err)
	}
	if change.Index != 2 {
		t.Fatalf("put index = %d, want 2 after join event", change.Index)
	}

	result, err := client.Get(ReadRequest{Resource: "messages/team", Key: "status", Mode: ReadFull})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Messages) != 1 || result.Messages[0].Kind != "test" || result.Messages[0].Payload.Text != "done" {
		t.Fatalf("unexpected full read: %#v", result)
	}

	assertJSONFile(t, filepath.Join(home, "registry", "a.json"))
	assertJSONFile(t, filepath.Join(home, "topics", "messages", "team", "messages", "status.json"))
	assertJSONFile(t, filepath.Join(home, "topics", "messages", "team", "history", indexName(change.Index)))
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
	if err := os.Remove(filepath.Join(home, "sessions", fmt.Sprintf("%d.json", os.Getppid()))); err != nil {
		t.Fatal(err)
	}
	unbound := newTestClient(t, home, "")
	if _, err := unbound.Put("messages/team", map[string]MessageMutation{"status": testMessage("done")}); err == nil || !strings.Contains(err.Error(), "identity is not bound") {
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

func TestStaleTerminalSessionCannotBindIdentity(t *testing.T) {
	home := t.TempDir()
	registered := newTestClient(t, home, "")
	if _, err := registered.Register("only", "dev"); err != nil {
		t.Fatal(err)
	}
	sessionPath := filepath.Join(home, "sessions", fmt.Sprintf("%d.json", os.Getppid()))
	var session Session
	if err := readJSON(sessionPath, &session); err != nil {
		t.Fatal(err)
	}
	session.ParentStart = "stale-process-instance"
	if err := writeJSONAtomic(sessionPath, session); err != nil {
		t.Fatal(err)
	}
	unbound := newTestClient(t, home, "")
	if _, err := unbound.ResolveAgent(); err == nil {
		t.Fatal("stale parent-PID session silently bound identity")
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
	if err != nil || len(events) < 2 || len(events) > 3 {
		t.Fatalf("events = %#v, %v", events, err)
	}
	if events[0].Signal.Type != "join" || events[1].Signal.Type != "leave" || len(events) == 3 && events[2].Signal.Type != "join" {
		t.Fatalf("membership events are not ordered: %#v", events)
	}
	_, statErr := os.Stat(filepath.Join(home, "registry", "same.json"))
	present := statErr == nil
	lastType := events[len(events)-1].Signal.Type
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
