package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/danlavee/Conductor/internal/adapters/binding"
	"github.com/danlavee/Conductor/internal/adapters/hold"
	"github.com/danlavee/Conductor/internal/platform"
	conductor "github.com/danlavee/Conductor/internal/state"
)

// waitForOwner waits for a holder's owner record, which appears once it holds
// the guard; probing the guard before then would take it first.
func waitForOwner(t *testing.T, root, agent string) string {
	t.Helper()
	owner := filepath.Join(root, "state", "watch", agent+".owner.json")
	waitFor(t, "a holder to take the guard", func() bool {
		_, err := os.Stat(owner)
		return err == nil
	})
	return owner
}

func waitFor(t *testing.T, what string, condition func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); !condition(); time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for " + what)
		}
	}
}

func selfTarget(t *testing.T) platform.Process {
	t.Helper()
	target, err := platform.ProcessOf(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	return target
}

// A host that serves its session in process gets the session and target from
// the shared arm path, and Hold owns the agent's guard exactly while serve runs.
func TestAnArmWithServeHoldsTheSessionWhileItServes(t *testing.T) {
	root := filepath.Join(t.TempDir(), "runtime-state")
	var served hold.Session
	target := serveFake(t, root, func(_ context.Context, client *conductor.Client, s hold.Session) error {
		served = s
		if client.Agent != "a" || !guardLocked(t, root) {
			return errors.New("serve ran without the agent's guard")
		}
		return nil
	})
	bindFake(t, root, "session-1")
	if err := run([]string{"adapter", "fake", "arm", "session-1"}); err != nil {
		t.Fatal(err)
	}
	if want := (hold.Session{Binding: "fake", ID: "session-1", Target: target}); served != want {
		t.Fatalf("serve got %+v, want %+v", served, want)
	}
	if guardLocked(t, root) {
		t.Fatal("the guard outlived serve")
	}
}

// The last join wins across sessions while the old session's Hold still owns
// the guard, which it lets go only once its poll sees the binding gone. The
// new session's arm meets LOCKED then, and must still end up holding the
// agent: declining leaves nothing holding it once the old Hold lets go.
func TestAnArmWithServeTakesTheAgentOverFromAnotherSession(t *testing.T) {
	root := filepath.Join(t.TempDir(), "runtime-state")
	holding, lost, letGo := make(chan string, 2), make(chan struct{}), make(chan struct{})
	var once sync.Once
	var arms sync.WaitGroup
	// Every arm ends before the state root is removed, however the test ends.
	t.Cleanup(func() {
		once.Do(func() { close(letGo) })
		_ = binding.Forget(root, "fake", "session-2")
		arms.Wait()
	})
	serveFake(t, root, func(ctx context.Context, _ *conductor.Client, s hold.Session) error {
		holding <- s.ID
		<-ctx.Done()
		if s.ID == "session-1" {
			close(lost)
			<-letGo // the old Hold keeps the guard until the test lets it go
		}
		return nil
	})
	arm := func(session string) <-chan error {
		done := make(chan error, 1)
		arms.Add(1)
		go func() { defer arms.Done(); done <- run([]string{"adapter", "fake", "arm", session}) }()
		return done
	}
	within := func(what string, ready <-chan struct{}) {
		t.Helper()
		select {
		case <-ready:
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for " + what)
		}
	}

	bindFake(t, root, "session-1")
	first := arm("session-1")
	select {
	case id := <-holding:
		if id != "session-1" {
			t.Fatalf("%s holds, want session-1", id)
		}
	case err := <-first:
		t.Fatalf("the first arm returned %v without holding", err)
	}
	bindFake(t, root, "session-2")
	within("the old Hold to see its binding gone", lost)
	second := arm("session-2")

	select {
	case err := <-second:
		t.Fatalf("the new session's arm returned %v while the old Hold held the guard; nothing holds the agent once it lets go", err)
	case <-time.After(time.Second):
	}
	once.Do(func() { close(letGo) })
	if err := <-first; err != nil {
		t.Fatalf("the old session's arm: %v", err)
	}
	select {
	case id := <-holding:
		if id != "session-2" || !guardLocked(t, root) {
			t.Fatalf("%s holds, guard locked %v; want session-2 holding", id, guardLocked(t, root))
		}
	case err := <-second:
		t.Fatalf("the new session's arm returned %v without holding", err)
	case <-time.After(5 * time.Second):
		t.Fatal("the new session never took the guard")
	}
	// The owner already holds for session-2, so another arm there declines
	// rather than wait out the session: hooks must not pile up.
	select {
	case err := <-arm("session-2"):
		if err != nil {
			t.Fatalf("a second arm in the holding session: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a second arm in the holding session waited for the guard")
	}
}

// serveFake adds, for this test only, a host row that holds its session in
// process with serve, and joins agent a.
func serveFake(t *testing.T, root string, serve func(context.Context, *conductor.Client, hold.Session) error) platform.Process {
	t.Helper()
	t.Setenv("CONDUCTOR_HOME", root)
	target := selfTarget(t)
	hosts := adapterHosts
	t.Cleanup(func() { adapterHosts = hosts })
	adapterHosts = append(slices.Clone(hosts), adapterHost{
		binding: "fake", verbs: []string{"arm"},
		event:  func(_ string, args []string, _ io.Reader) (string, bool, error) { return args[0], true, nil },
		target: func() (platform.Process, error) { return target, nil },
		serve:  serve,
	})
	client, err := conductor.New(root, "a")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Join("a", "dev"); err != nil {
		t.Fatal(err)
	}
	return target
}

// bindFake joins agent a from the fake host's session, which takes it over.
func bindFake(t *testing.T, root, session string) {
	t.Helper()
	if err := binding.Serve(root, "fake", session); err != nil {
		t.Fatal(err)
	}
	if bound, _, err := binding.Bind(root, "fake", session, "a"); !bound || err != nil {
		t.Fatalf("bind %s = %v, %v", session, bound, err)
	}
}

// guardLocked reports whether something holds agent a's watch guard.
func guardLocked(t *testing.T, root string) bool {
	t.Helper()
	client, err := conductor.Open(root, "a")
	if err != nil {
		t.Fatal(err)
	}
	release, err := client.AcquireWatchOwnership(conductor.HolderSelf, "")
	if err == nil {
		return release() != nil
	}
	var protocol *conductor.ProtocolError
	return errors.As(err, &protocol) && protocol.Code == "LOCKED"
}
