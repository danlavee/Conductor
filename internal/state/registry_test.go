package state

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/danlavee/Conductor/internal/platform"
)

// TestRosterSaysHowLongEachWakeableEntryStaysTrue covers what every roster
// carries. Two entries can both be wakeable while one holds across turns and
// the other has already spent its single delivery, and the boolean reports
// those identically.
func TestRosterSaysHowLongEachWakeableEntryStaysTrue(t *testing.T) {
	home := t.TempDir()
	client := newTestClient(t, home, "")
	for name, holder := range map[string]Holder{"served": HolderAdapter, "selfarm": HolderSelf} {
		if _, err := client.Join(name, "dev"); err != nil {
			t.Fatal(err)
		}
		release, err := newTestClient(t, home, name).AcquireWatchOwnership(holder, "")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = release() }()
	}
	if _, err := client.Join("absent", "dev"); err != nil {
		t.Fatal(err)
	}

	roster, err := client.Roster()
	if err != nil {
		t.Fatal(err)
	}
	held := map[string]RosterEntry{}
	for _, entry := range roster {
		held[entry.Name] = entry
	}
	for name, want := range map[string]Holder{"served": HolderAdapter, "selfarm": HolderSelf, "absent": ""} {
		if entry := held[name]; entry.Holder != want || entry.Wakeable != (want != "") {
			t.Errorf("%s = %+v, want holder %q", name, entry, want)
		}
	}
}

// An owner record outlives a killed holder, and its PID can be reused. Only a
// record whose process is alive and still matches its start token answers.
func TestADeadOrRecycledHolderIsUnreachable(t *testing.T) {
	home := t.TempDir()
	client := newTestClient(t, home, "")
	exited := exec.Command(os.Args[0], "-test.run=^$")
	if err := exited.Run(); err != nil {
		t.Fatal(err)
	}
	self, err := platform.ProcessOf(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	owners := map[string]watchOwner{
		"live":      {Process: self, Holder: HolderAdapter},
		"tokenless": {Process: platform.Process{PID: self.PID}, Holder: HolderAdapter},
		"recycled":  {Process: platform.Process{PID: self.PID, Start: "not-this-process"}, Holder: HolderAdapter},
		"dead":      {Process: platform.Process{PID: exited.Process.Pid, Start: self.Start}, Holder: HolderAdapter},
	}
	for name, owner := range owners {
		if _, err := client.Join(name, "dev"); err != nil {
			t.Fatal(err)
		}
		_, ownerPath := watchGuardPaths(home, name)
		if err := writeJSONAtomic(ownerPath, owner); err != nil {
			t.Fatal(err)
		}
	}
	roster, err := client.Roster()
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range roster {
		if want := entry.Name == "live"; entry.Wakeable != want {
			t.Errorf("%s wakeable = %v, want %v", entry.Name, entry.Wakeable, want)
		}
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

	result, err := client.Get(ReadRequest{Topic: "messages/team", Mode: ReadFull})
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
	if _, err := client.requireAgent(); err == nil || !strings.Contains(err.Error(), "invalid name") {
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

// The roster line is a record, struck through on leave, so a responsibility
// that would push the struck line past MaxRecordText is refused at join.
func TestJoinRefusesAResponsibilityTooLongForTheRoster(t *testing.T) {
	home := t.TempDir()
	client := newTestClient(t, home, "")
	fixed := len("~~" + rosterText("agent", "") + "~~")
	if _, err := client.Join("agent", strings.Repeat("r", MaxRecordText-fixed+1)); err == nil {
		t.Fatal("an over-long responsibility was accepted")
	} else {
		assertCode(t, err, "INVALID")
	}
	if _, err := os.Stat(filepath.Join(home, "registry", "agent.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a refused join left a registration: %v", err)
	}
	if _, err := client.Join("agent", strings.Repeat("r", MaxRecordText-fixed)); err != nil {
		t.Fatalf("a responsibility that just fits was refused: %v", err)
	}
	if err := client.Leave("agent"); err != nil {
		t.Fatalf("leave could not strike the longest roster line: %v", err)
	}
}
