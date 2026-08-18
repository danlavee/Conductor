package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	conductor "github.com/danlavee/Conductor"
)

func TestResolveIdentityReadsProjectBinding(t *testing.T) {
	project := t.TempDir()
	if agent, err := ResolveIdentity(project); err != nil || agent != "" {
		t.Fatalf("unbound project = %q, %v; want silence", agent, err)
	}
	if err := os.WriteFile(filepath.Join(project, IdentityFile), []byte("planner\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	agent, err := ResolveIdentity(project)
	if err != nil || agent != "planner" {
		t.Fatalf("bound project = %q, %v", agent, err)
	}
	if _, err := ResolveIdentity(" "); err == nil {
		t.Fatal("a missing project directory is a misconfiguration, not silence")
	}
}

// TestResolveIdentityIgnoresAByteOrderMark covers the identity file as it is
// actually produced on this host: Notepad and PowerShell's Set-Content
// -Encoding utf8 both prepend U+FEFF. TrimSpace does not treat it as
// whitespace, so an unstripped mark rides into the agent name and fails
// validation at every session start -- while the file looks correct in every
// editor that hides the mark.
func TestResolveIdentityIgnoresAByteOrderMark(t *testing.T) {
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, IdentityFile), []byte("\xef\xbb\xbfplanner\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	agent, err := ResolveIdentity(project)
	if err != nil || agent != "planner" {
		t.Fatalf("bound project = %q, %v; want %q", agent, err, "planner")
	}
}

// TestOutcomeCodesAreStableDistinctAndClearOfExitStatuses pins the numbering,
// which is the entire point of having it: a code that shifted between builds
// would silently reclassify every run already recorded under it. The literals
// are written out rather than derived, so moving one is a test change and not
// a quiet renumbering.
func TestOutcomeCodesAreStableDistinctAndClearOfExitStatuses(t *testing.T) {
	expected := map[Outcome]int{
		Unbound: 10, Delivered: 11, Replaced: 12, Refused: 13,
		Unregistered: 14, Released: 15, NotOwned: 16,
	}
	seen := map[int]Outcome{}
	for outcome, want := range expected {
		code := outcome.Code()
		if code != want {
			t.Errorf("%q code = %d, want %d", outcome, code, want)
		}
		// Clear of every exit status the adapter produces -- 0, 1, and the
		// wake -- so a report code can never be misread as one.
		if code <= WakeExitCode {
			t.Errorf("%q code %d collides with an exit status", outcome, code)
		}
		if other, taken := seen[code]; taken {
			t.Errorf("%q and %q share code %d", other, outcome, code)
		}
		seen[code] = outcome
	}
	if Outcome("invented").Code() != 0 {
		t.Error("an outcome with no code must report zero rather than guess one")
	}
}

func TestArmDeliversPendingWorkAndReleasesOwnership(t *testing.T) {
	root := filepath.Join(t.TempDir(), "runtime-state")
	client := joined(t, root)

	var payload bytes.Buffer
	outcome, err := Arm(context.Background(), client, "session-1", &payload)
	if err != nil || outcome != Delivered {
		t.Fatalf("outcome = %q, %v", outcome, err)
	}
	var batch conductor.BatchDelivery
	if err := json.Unmarshal(payload.Bytes(), &batch); err != nil || len(batch.Deliveries) == 0 {
		t.Fatalf("payload = %q: %v", payload.String(), err)
	}

	// A woken process must not keep the identity: the next turn boundary arms
	// the successor, and it can only take over if this one truly let go.
	successor := open(t, root)
	release, err := successor.AcquireWatchOwnership()
	if err != nil {
		t.Fatalf("a delivered arm kept ownership: %v", err)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
}

func TestArmRefusesWhenAnotherStreamOwnsTheIdentity(t *testing.T) {
	root := filepath.Join(t.TempDir(), "runtime-state")
	client := joined(t, root)
	release, err := client.AcquireWatchOwnership()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = release() }()

	outcome, err := Arm(context.Background(), open(t, root), "session-2", &bytes.Buffer{})
	if err != nil || outcome != Refused {
		t.Fatalf("outcome = %q, %v; a redundant arm must decline, not fail", outcome, err)
	}
}

func TestArmReportsAnIdentityThatNeverJoined(t *testing.T) {
	root := filepath.Join(t.TempDir(), "runtime-state")
	if _, err := conductor.New(root, "planner"); err != nil {
		t.Fatal(err)
	}
	outcome, err := Arm(context.Background(), open(t, root), "session-1", &bytes.Buffer{})
	if err != nil || outcome != Unregistered {
		t.Fatalf("outcome = %q, %v", outcome, err)
	}
}

func TestReleaseEndsOnlyTheCallingSessionsStream(t *testing.T) {
	root := filepath.Join(t.TempDir(), "runtime-state")
	client := joined(t, root)
	drain(t, client)

	// The stream reports back over a channel rather than calling t.Errorf from
	// its own goroutine: if Arm ever hangs -- the exact fault this test exists
	// to catch -- the timeout below ends the test first, and a later t.Errorf
	// from the orphan would panic instead of failing legibly.
	type armResult struct {
		outcome Outcome
		err     error
	}
	stream := open(t, root)
	armed := make(chan armResult, 1)
	go func() {
		outcome, err := Arm(context.Background(), stream, "session-1", &bytes.Buffer{})
		armed <- armResult{outcome: outcome, err: err}
	}()
	waitForResidency(t, root, "planner")

	// A second session in the same project must not be able to tear down the
	// stream the first one holds.
	if released, err := Release(open(t, root), "session-2"); err != nil || released {
		t.Fatalf("a foreign session released the stream: %v, %v", released, err)
	}
	select {
	case result := <-armed:
		t.Fatalf("stream ended on a foreign release: %q, %v", result.outcome, result.err)
	case <-time.After(200 * time.Millisecond):
	}

	if released, err := Release(open(t, root), "session-1"); err != nil || !released {
		t.Fatalf("the owning session could not release: %v, %v", released, err)
	}
	select {
	case result := <-armed:
		if result.err != nil || result.outcome != Released {
			t.Fatalf("outcome = %q, err = %v", result.outcome, result.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the stream ignored its own session ending")
	}
}

func joined(t *testing.T, root string) *conductor.Client {
	t.Helper()
	client, err := conductor.New(root, "planner")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Join("planner", "dev"); err != nil {
		t.Fatal(err)
	}
	return client
}

func open(t *testing.T, root string) *conductor.Client {
	t.Helper()
	client, err := conductor.Open(root, "planner")
	if err != nil {
		t.Fatal(err)
	}
	return client
}

// drain consumes the signals joining produces, so a later arm blocks on an
// empty stream instead of returning immediately with the join backlog.
func drain(t *testing.T, client *conductor.Client) {
	t.Helper()
	if outcome, err := Arm(context.Background(), client, "drain", &bytes.Buffer{}); err != nil || outcome != Delivered {
		t.Fatalf("drain = %q, %v", outcome, err)
	}
}

func waitForResidency(t *testing.T, root, agent string) {
	t.Helper()
	client := open(t, root)
	path, err := residencyPath(client)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the stream never recorded its residency")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
