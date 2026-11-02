package state

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/danlavee/Conductor/internal/platform"
)

// TestRosterSaysHowLongEachWakeableEntryStaysTrue covers what every roster
// carries -- the join snapshot and the roster a join or leave delivery brings,
// both of which come through here. Two entries can both be wakeable while one
// holds across turns and the other has already spent its single delivery, and
// the boolean reports those identically.
func TestRosterSaysHowLongEachWakeableEntryStaysTrue(t *testing.T) {
	home := t.TempDir()
	client := newTestClient(t, home, "")
	for name, acquire := range map[string]func(*Client) (func() error, error){
		"served": func(c *Client) (func() error, error) {
			return c.AcquireAdapterWatchOwnership("claude", "session-1")
		},
		"selfarm": (*Client).AcquireWatchOwnership,
	} {
		if _, err := client.Join(name, "dev"); err != nil {
			t.Fatal(err)
		}
		release, err := acquire(newTestClient(t, home, name))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = release() }()
	}
	if _, err := client.Join("absent", "dev"); err != nil {
		t.Fatal(err)
	}
	// A record an older build wrote. Both readers of the owner record share one
	// normalization so they cannot disagree about it, and testing that on the
	// status side alone would leave the shared part half covered.
	if _, err := client.Join("legacy", "dev"); err != nil {
		t.Fatal(err)
	}
	_, ownerPath := watchGuardPaths(home, "legacy")
	pid := os.Getpid()
	processStart, _ := platform.ProcessStartToken(pid)
	if err := writeJSONAtomic(ownerPath, map[string]any{
		"pid": pid, "process_start": processStart,
	}); err != nil {
		t.Fatal(err)
	}

	roster, err := client.Roster()
	if err != nil {
		t.Fatal(err)
	}
	held := map[string]*WatchHolder{}
	for _, entry := range roster {
		held[entry.Name] = entry.Holder
	}
	if held["served"] == nil || held["served"].Kind != HolderAdapter || held["served"].Adapter != "claude" {
		t.Errorf("served holder = %+v, want the adapter that holds it", held["served"])
	}
	if held["selfarm"] == nil || held["selfarm"].Kind != HolderSelf {
		t.Errorf("self-armed holder = %+v, want kind self", held["selfarm"])
	}
	if held["absent"] != nil {
		t.Errorf("unheld holder = %+v, want none", held["absent"])
	}
	if held["legacy"] == nil || held["legacy"].Kind != HolderUnknown {
		t.Errorf("unlabelled holder = %+v, want kind %q", held["legacy"], HolderUnknown)
	}
}

func TestRegisterPutGetAndDiskState(t *testing.T) {
	home := t.TempDir()
	client := newTestClient(t, home, "")
	snapshot, err := client.Join("a", "dev")
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Agents) != 1 || snapshot.Agents[0].Name != "a" {
		t.Fatalf("unexpected registration snapshot: %#v", snapshot)
	}

	dispatch, err := client.Put("messages/team", "done")
	if err != nil {
		t.Fatal(err)
	}
	record := dispatch.Record()
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
	_, err = client.readHistory("messages/team")
	if err != nil {
		t.Fatal(err)
	}
	assertJSONFile(t, filepath.Join(home, "topics", "messages", "team", "history.jsonl"))
}

func TestIdentityIsNeverInferredFromSoleRegistration(t *testing.T) {
	home := t.TempDir()
	registered := newTestClient(t, home, "")
	if _, err := registered.Join("only", "dev"); err != nil {
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
	if _, err := first.Join("same", "original"); err != nil {
		t.Fatal(err)
	}
	second := newTestClient(t, home, "")
	// re-joining with responsibility should fail with INVALID
	_, err := second.Join("same", "replacement")
	assertCode(t, err, "INVALID")
	if !strings.Contains(err.Error(), "responsibility must be omitted for existing agent") {
		t.Fatalf("expected arity error, got %v", err)
	}

	agents, err := first.ListAgents()
	if err != nil || len(agents) != 1 || agents[0].Responsibility != "original" {
		t.Fatalf("duplicate registration overwrote identity: %#v, %v", agents, err)
	}
}

// rosterPublications counts the publications on collaboration/agents.
func rosterPublications(t *testing.T, client *Client) int {
	t.Helper()
	history, err := client.readHistory(collaborationAgentsTopic)
	if err != nil {
		t.Fatal(err)
	}
	return len(history)
}

// A join that crashed after registering but before its roster line is
// finished by repeating it, and repeating it again publishes nothing.
func TestJoinRetryPublishesAMissingRosterLineOnce(t *testing.T) {
	home := t.TempDir()
	client := newTestClient(t, home, "")
	agent := Agent{Name: "retry", Responsibility: "dev", Timestamp: time.Now().UTC()}
	if err := writeJSONAtomic(filepath.Join(home, "registry", "retry.json"), agent); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := client.Join("retry", ""); err != nil {
			t.Fatal(err)
		}
	}
	result, err := client.Get(ReadRequest{Topic: collaborationAgentsTopic, Mode: ReadFull})
	if err != nil || len(result.Records) != 1 || result.Records[0].Text != "retry: dev" || rosterPublications(t, client) != 1 {
		t.Fatalf("roster = %#v, error = %v", result.Records, err)
	}
}

func TestLeaveStrikesThroughTheRosterLine(t *testing.T) {
	client := newTestClient(t, t.TempDir(), "")
	if _, err := client.Join("gone", "dev"); err != nil {
		t.Fatal(err)
	}
	if err := client.Leave("gone"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Join("reader", "dev"); err != nil {
		t.Fatal(err)
	}
	result, err := client.Get(ReadRequest{Topic: collaborationAgentsTopic, Mode: ReadFull})
	if err != nil || len(result.Records) == 0 || result.Records[0].Text != "~~gone: dev~~" {
		t.Fatalf("roster = %#v, error = %v", result.Records, err)
	}
}

// A leave that crashed after striking the roster line is finished by
// repeating it, without striking the line again.
func TestLeaveRetryFinishesWithoutStrikingAgain(t *testing.T) {
	home := t.TempDir()
	client := newTestClient(t, home, "")
	snapshot, err := client.Join("retry", "dev")
	if err != nil {
		t.Fatal(err)
	}
	if err := client.retireRosterRecord(snapshot.Agents[0].Agent); err != nil {
		t.Fatal(err)
	}
	if err := client.Leave("retry"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, "registry", "retry.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("registration survived the leave: %v", err)
	}
	if got := rosterPublications(t, client); got != 2 {
		t.Fatalf("roster publications = %d, want the join and one strike", got)
	}
	assertCode(t, client.Leave("retry"), "NOT_FOUND")
}

func TestRegistrationSnapshotFailureDoesNotCreateMembership(t *testing.T) {
	home := t.TempDir()
	client := newTestClient(t, home, "")
	resourceDir := filepath.Join(home, "topics", "broken", "state")
	if err := os.MkdirAll(resourceDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(resourceDir, "history.jsonl"), []byte("not-json\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Join("a", "dev"); err == nil {
		t.Fatal("registration unexpectedly succeeded with unreadable snapshot")
	}
	if _, err := os.Stat(filepath.Join(home, "registry", "a.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed registration mutated registry: %v", err)
	}
}

func TestConcurrentJoinsStayOrdered(t *testing.T) {
	home := t.TempDir()
	initial := newTestClient(t, home, "")
	if _, err := initial.Join("same", "initial"); err != nil {
		t.Fatal(err)
	}
	registering1 := newTestClient(t, home, "")
	registering2 := newTestClient(t, home, "")
	results := make(chan error, 2)
	go func() {
		_, err := registering1.Join("same", "")
		results <- err
	}()
	go func() {
		_, err := registering2.Join("same", "")
		results <- err
	}()
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	if got := rosterPublications(t, initial); got != 1 {
		t.Fatalf("roster publications = %d, want one for the one agent", got)
	}
}

func TestJoinArityValidation(t *testing.T) {
	home := t.TempDir()
	client := newTestClient(t, home, "")

	// 1. First join without responsibility -> must fail with INVALID
	_, err := client.Join("new-agent", "")
	if err == nil {
		t.Fatal("expected error for first join without responsibility")
	}
	assertCode(t, err, "INVALID")
	if !strings.Contains(err.Error(), "responsibility is required for new agent") {
		t.Fatalf("unexpected error message: %v", err)
	}

	// 2. First join with responsibility -> must succeed
	_, err = client.Join("new-agent", "developer")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 3. Re-join with responsibility -> must fail with INVALID and tell them how to change it
	_, err = client.Join("new-agent", "developer")
	if err == nil {
		t.Fatal("expected error for re-join with responsibility")
	}
	assertCode(t, err, "INVALID")
	if !strings.Contains(err.Error(), "responsibility must be omitted for existing agent. To change responsibility unregister and re-register under the same name") {
		t.Fatalf("unexpected error message: %v", err)
	}

	// 4. Re-join without responsibility -> must succeed
	_, err = client.Join("new-agent", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 5. Leave and then re-join with new responsibility -> must succeed
	err = client.Leave("new-agent")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_, err = client.Join("new-agent", "reviewer")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify that the responsibility indeed changed
	agents, err := client.ListAgents()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(agents) != 1 || agents[0].Responsibility != "reviewer" {
		t.Fatalf("expected responsibility to be 'reviewer', got %#v", agents)
	}
}
