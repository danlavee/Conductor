package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	conductor "github.com/danlavee/Conductor/internal/state"
)

// TestArmingAnswersBeforeItWaits covers the act an agent used to conclude from
// with nothing in hand. Starting the watcher backgrounds a process and hands
// back a host pid, which says a process exists -- not that the identity is
// reachable, and not that anything reaches it. The answer has to arrive at arm
// time, before the wait, because that is when the agent decides the act is done
// and ends its turn; a fact that only appears with the first delivery arrives
// after every decision it was needed for.
func TestArmingAnswersBeforeItWaits(t *testing.T) {
	root := filepath.Join(t.TempDir(), "runtime-state")
	if _, _, err := runCLIHelper(os.Args[0], root, "", "nomad", "join", "parser"); err != nil {
		t.Fatal(err)
	}
	// A peer's registration is what lets the one-shot watch below terminate;
	// nomad is not woken by its own join.
	if _, _, err := runCLIHelper(os.Args[0], root, "", "peer", "join", "review"); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, err := runCLIHelper(os.Args[0], root, "", "nomad", "watch", "--once")
	if err != nil {
		t.Fatalf("watch: %v: %s", err, stderr)
	}
	armed := readArmAcknowledgement(t, json.NewDecoder(bytes.NewReader(stdout)))
	if !armed.Registered || armed.Holder != conductor.HolderSelf {
		t.Fatalf("arm acknowledgement = %#v; want the registration and the holding process", armed)
	}
	// The other half of the same answer, and the one an agent cannot get from
	// the host: it is held, and nothing beyond the two broadcast topics
	// reaches it. Reachable-looking and unreachable is the state this reports.
	if len(armed.Subscriptions.TopicGroups) != 0 || len(armed.Subscriptions.Topics) != 0 {
		t.Fatalf("subscriptions = %+v; want the empty set an agent that subscribed to nothing has", armed.Subscriptions)
	}
}

// TestArmingAnswersOnAStreamThatNeverDelivers is the fault shape itself. An
// agent arms, nothing is pending, and it ends the turn -- which is where the
// claim of reachability used to be invented, because the alternative was a
// silent process. The answer has to arrive with no delivery behind it and while
// the watcher is still blocked, or it is not available when it is needed.
func TestArmingAnswersOnAStreamThatNeverDelivers(t *testing.T) {
	root := filepath.Join(t.TempDir(), "runtime-state")
	if _, _, err := runCLIHelper(os.Args[0], root, "", "a", "join", "dev"); err != nil {
		t.Fatal(err)
	}
	// Nothing to consume first: an agent is not woken by its own join, so
	// this stream starts empty.
	watch, deliveries := startWatch(t, root)
	defer stopWatch(t, watch)

	// startWatch already read the acknowledgement. Nothing may follow it: a
	// second document here would mean the answer rode in on a delivery.
	quiet := make(chan error, 1)
	go func() {
		var next json.RawMessage
		quiet <- deliveries.Decode(&next)
	}()
	select {
	case err := <-quiet:
		t.Fatalf("something followed the arm acknowledgement on an empty stream: %v", err)
	case <-time.After(500 * time.Millisecond):
	}
}

// TestPublishingDoesNotEndTheStreamThatArmedIt is the field failure this test
// file's neighbours could not catch. An agent follows the order the skill sets
// -- arm, then report what it has taken -- and its own put comes back to it as
// a delivery, ending the one-shot stream it just armed. The turn then closes
// with a claim of reachability that was true when written and false when read.
func TestPublishingDoesNotEndTheStreamThatArmedIt(t *testing.T) {
	root := filepath.Join(t.TempDir(), "runtime-state")
	if _, _, err := runCLIHelper(os.Args[0], root, "", "a", "join", "dev"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runCLIHelper(os.Args[0], root, "", "a", "subscribe", "--topic=dev/log"); err != nil {
		t.Fatal(err)
	}
	watch, deliveries := startWatch(t, root)
	defer stopWatch(t, watch)

	if _, stderr, err := runCLIHelper(os.Args[0], root, "", "a", "put", "dev/log", "picked this up"); err != nil {
		t.Fatalf("put: %v: %s", err, stderr)
	}
	delivered := make(chan error, 1)
	go func() {
		var next json.RawMessage
		delivered <- deliveries.Decode(&next)
	}()
	select {
	case err := <-delivered:
		t.Fatalf("the stream took the agent's own publication as a delivery: %v", err)
	case <-time.After(500 * time.Millisecond):
	}
	// Still held, so the record the agent is waiting for can still reach it.
	stdout, stderr, err := runCLIHelper(os.Args[0], root, "", "a", "status")
	if err != nil {
		t.Fatalf("status: %v: %s", err, stderr)
	}
	var reported conductor.WatchStatus
	if err := json.Unmarshal(stdout, &reported); err != nil {
		t.Fatalf("status JSON: %v: %s", err, stdout)
	}
	if !reported.Wakeable {
		t.Fatalf("publishing left the agent unreachable: %+v", reported)
	}
}

// TestAFailedWaitStillCarriesItsArmAnswer pins what a reader of the stream can
// conclude from counting documents. The arm answer is written before the wait,
// so a watch whose wait ends in failure exits having written one document and
// not two -- and a reader holding "two documents" as the contract would take
// that for a delivery still on its way. Leaving the roster while blocked is the
// cheapest way to fail a wait; every other failure exits in the same shape.
func TestAFailedWaitStillCarriesItsArmAnswer(t *testing.T) {
	root := filepath.Join(t.TempDir(), "runtime-state")
	if _, _, err := runCLIHelper(os.Args[0], root, "", "nomad", "join", "parser"); err != nil {
		t.Fatal(err)
	}
	// Nothing to drain: an agent is not woken by its own join, so the watcher
	// below blocks instead of delivering a backlog and exiting successfully.
	watch := exec.Command(os.Args[0], "nomad", "watch", "--once")
	watch.Env = replaceEnvironment(os.Environ(), cliHelperEnvironment, "1")
	watch.Env = replaceEnvironment(watch.Env, "CONDUCTOR_HOME", root)
	stdout, err := watch.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	watch.Stderr = &stderr
	if err := watch.Start(); err != nil {
		t.Fatal(err)
	}
	defer stopWatch(t, watch)
	deliveries := json.NewDecoder(stdout)
	readArmAcknowledgement(t, deliveries)

	if _, _, err := runCLIHelper(os.Args[0], root, "", "nomad", "leave"); err != nil {
		t.Fatal(err)
	}
	ended := make(chan error, 1)
	go func() {
		var next json.RawMessage
		ended <- deliveries.Decode(&next)
	}()
	select {
	case err := <-ended:
		if err == nil {
			t.Fatal("watch wrote a second document after its identity left the roster")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("watch kept waiting for an identity that is no longer on the roster")
	}
	if err := watch.Wait(); err == nil {
		t.Fatal("watch exited 0 after the identity it was waiting for left the roster")
	}
	if !strings.Contains(stderr.String(), "NOT_FOUND") {
		t.Fatalf("watch stderr = %s; want the failure an absent identity earns", stderr.String())
	}
}
