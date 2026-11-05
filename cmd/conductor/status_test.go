package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	conductor "github.com/danlavee/Conductor/internal/state"
)

// A watch's stdout is the delivery and nothing else. The agent reads what
// wakes it as one document; a preface before it is a document an agent can
// mistake for the delivery and drop the mail with.
func TestWatchPrintsOnlyTheDelivery(t *testing.T) {
	root := filepath.Join(t.TempDir(), "runtime-state")
	for _, join := range [][2]string{{"a", "dev"}, {"peer", "review"}} {
		if _, _, err := runCLIHelper(os.Args[0], root, "", join[0], "join", join[1]); err != nil {
			t.Fatal(err)
		}
	}
	stdout, stderr, err := runCLIHelper(os.Args[0], root, "", "a", "watch")
	if err != nil {
		t.Fatalf("watch: %v: %s", err, stderr)
	}
	documents := json.NewDecoder(bytes.NewReader(stdout))
	var batch conductor.Batch
	if err := documents.Decode(&batch); err != nil || len(batch.Deliveries) == 0 {
		t.Fatalf("first document = %#v, %v; want the delivery", batch, err)
	}
	var extra json.RawMessage
	if err := documents.Decode(&extra); !errors.Is(err, io.EOF) {
		t.Fatalf("watch wrote more than the delivery: %s", extra)
	}
}

// An armed watch with nothing pending stays silent: anything it wrote would be
// read as a delivery.
func TestAnEmptyStreamWritesNothing(t *testing.T) {
	root := filepath.Join(t.TempDir(), "runtime-state")
	if _, _, err := runCLIHelper(os.Args[0], root, "", "a", "join", "dev"); err != nil {
		t.Fatal(err)
	}
	_, deliveries := startWatch(t, root)
	expectSilence(t, deliveries, "an empty stream wrote a document")
}

// An agent arms, then reports what it took on. Its own put must not come back
// as a delivery: that would end the one-shot stream it just armed, and leave it
// unreachable for the record it is waiting for.
func TestPublishingDoesNotEndTheStreamThatArmedIt(t *testing.T) {
	root := filepath.Join(t.TempDir(), "runtime-state")
	if _, _, err := runCLIHelper(os.Args[0], root, "", "a", "join", "dev"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runCLIHelper(os.Args[0], root, "", "a", "subscribe", "--topic=dev/log"); err != nil {
		t.Fatal(err)
	}
	_, deliveries := startWatch(t, root)

	if _, stderr, err := runCLIHelper(os.Args[0], root, "", "a", "put", "dev/log", "picked this up"); err != nil {
		t.Fatalf("put: %v: %s", err, stderr)
	}
	expectSilence(t, deliveries, "the stream took the agent's own publication as a delivery")
	if !readStatus(t, root).Wakeable {
		t.Fatal("publishing left the agent unreachable")
	}
}

// A failed wait exits non-zero having written nothing, so an exit with no
// document is never mistaken for a delivery. Leaving the roster while blocked
// is the cheapest way to fail a wait.
func TestAFailedWaitWritesNoDocument(t *testing.T) {
	root := filepath.Join(t.TempDir(), "runtime-state")
	if _, _, err := runCLIHelper(os.Args[0], root, "", "a", "join", "dev"); err != nil {
		t.Fatal(err)
	}
	watch := exec.Command(os.Args[0], "a", "watch")
	watch.Env = replaceEnvironment(os.Environ(), cliHelperEnvironment, "1")
	watch.Env = replaceEnvironment(watch.Env, "CONDUCTOR_HOME", root)
	var stdout, stderr bytes.Buffer
	watch.Stdout, watch.Stderr = &stdout, &stderr
	if err := watch.Start(); err != nil {
		t.Fatal(err)
	}
	defer stopWatch(t, watch)
	waitForOwnership(t, root)

	if _, _, err := runCLIHelper(os.Args[0], root, "", "a", "leave"); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- watch.Wait() }()
	select {
	case err := <-exited:
		if err == nil {
			t.Fatal("watch exited 0 after its identity left the roster")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("watch kept waiting for an identity that is no longer on the roster")
	}
	if stdout.Len() != 0 {
		t.Fatalf("a failed wait wrote %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "NOT_FOUND") {
		t.Fatalf("watch stderr = %s; want the failure an absent identity earns", stderr.String())
	}
}

func expectSilence(t *testing.T, deliveries *json.Decoder, failure string) {
	t.Helper()
	decoded := make(chan error, 1)
	go func() {
		var next json.RawMessage
		decoded <- deliveries.Decode(&next)
	}()
	select {
	case err := <-decoded:
		t.Fatalf("%s: %v", failure, err)
	case <-time.After(500 * time.Millisecond):
	}
}
