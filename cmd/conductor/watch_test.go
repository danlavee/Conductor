package main

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A watch's stdout is the wake and nothing else: a preface before it is text
// an agent can mistake for the delivery and drop the mail with.
func TestWatchPrintsOnlyTheDelivery(t *testing.T) {
	root := filepath.Join(t.TempDir(), "runtime-state")
	for _, join := range [][2]string{{"a", "dev"}, {"peer", "review"}} {
		if _, _, err := (cli{home: root}).run(join[0], "join", join[1]); err != nil {
			t.Fatal(err)
		}
	}
	stdout, stderr, err := cli{home: root}.run("a", "watch")
	if err != nil {
		t.Fatalf("watch: %v: %s", err, stderr)
	}
	if !strings.HasPrefix(string(stdout), "Conductor: ") || !bytes.Contains(stdout, []byte("peer: review")) {
		t.Fatalf("watch stdout = %q; want the wake and nothing before it", stdout)
	}
}

// An armed watch with nothing pending stays silent: anything it wrote would be
// read as a delivery.
func TestAnEmptyStreamWritesNothing(t *testing.T) {
	root := filepath.Join(t.TempDir(), "runtime-state")
	if _, _, err := (cli{home: root}).run("a", "join", "dev"); err != nil {
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
	if _, _, err := (cli{home: root}).run("a", "join", "dev"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := (cli{home: root}).run("a", "subscribe", "--topic=dev/log"); err != nil {
		t.Fatal(err)
	}
	_, deliveries := startWatch(t, root)

	if _, stderr, err := (cli{home: root}).run("a", "put", "dev/log", "picked this up"); err != nil {
		t.Fatalf("put: %v: %s", err, stderr)
	}
	expectSilence(t, deliveries, "the stream took the agent's own publication as a delivery")
}

// A failed wait exits non-zero having written nothing, so an exit with no
// document is never mistaken for a delivery. Leaving the roster while blocked
// is the cheapest way to fail a wait.
func TestAFailedWaitWritesNoDocument(t *testing.T) {
	root := filepath.Join(t.TempDir(), "runtime-state")
	if _, _, err := (cli{home: root}).run("a", "join", "dev"); err != nil {
		t.Fatal(err)
	}
	watch := cli{home: root}.command("a", "watch")
	var stdout, stderr bytes.Buffer
	watch.Stdout, watch.Stderr = &stdout, &stderr
	if err := watch.Start(); err != nil {
		t.Fatal(err)
	}
	defer stopWatch(t, watch)
	waitForOwner(t, root, "a")

	if _, _, err := (cli{home: root}).run("a", "leave"); err != nil {
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
