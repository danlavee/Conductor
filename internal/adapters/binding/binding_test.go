package binding

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestBindLeavesAnUnservedSessionUnbound(t *testing.T) {
	root := t.TempDir()
	bound, _, err := Bind(root, "claude", "session", "planner")
	if err != nil || bound {
		t.Fatalf("bound=%v, %v", bound, err)
	}
	if _, err := os.Stat(filepath.Join(Directory(root, "claude"), "sessions", "session")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unserved bind wrote the session: %v", err)
	}
}

func TestOfferReachesOnlyTheCurrentIdentity(t *testing.T) {
	root := t.TempDir()
	if err := Serve(root, "claude", "session"); err != nil {
		t.Fatal(err)
	}
	if bound, _, err := Bind(root, "claude", "session", "planner"); err != nil || !bound {
		t.Fatalf("bound=%v, %v", bound, err)
	}
	ran := false
	offer := func(agent string) error {
		ran = false
		return Offer(root, "claude", "session", agent, SessionWait, func() error { ran = true; return nil })
	}
	if err := offer("planner"); err != nil || !ran {
		t.Fatalf("bound offer ran=%v, %v", ran, err)
	}
	if _, _, err := Bind(root, "claude", "session", "writer"); err != nil {
		t.Fatal(err)
	}
	if err := offer("planner"); !errors.Is(err, ErrInactive) || ran {
		t.Fatalf("offer after rebind ran=%v, %v", ran, err)
	}
	if err := Forget(root, "claude", "session"); err != nil {
		t.Fatal(err)
	}
	if err := offer("writer"); !errors.Is(err, ErrInactive) || ran {
		t.Fatalf("offer after forget ran=%v, %v", ran, err)
	}
	if bound, _, err := Bind(root, "claude", "session", "writer"); err != nil || bound {
		t.Fatalf("bind after forget bound=%v, %v", bound, err)
	}
}

// The last join wins: binding an agent in a second session, under any
// adapter, unbinds it from the first and names that session's adapter. The
// first stays served, so joining there again takes the agent back.
func TestBindTakesTheAgentOverFromItsOtherSession(t *testing.T) {
	root := t.TempDir()
	for _, s := range [][2]string{{"claude", "first"}, {"grok", "second"}, {"grok", "bystander"}} {
		if err := Serve(root, s[0], s[1]); err != nil {
			t.Fatal(err)
		}
	}
	bind := func(adapter, session, agent, wantTookOver string) {
		t.Helper()
		if bound, tookOver, err := Bind(root, adapter, session, agent); err != nil || !bound || tookOver != wantTookOver {
			t.Fatalf("Bind(%s, %s, %s) = %v, %q, %v; want took over %q", adapter, session, agent, bound, tookOver, err, wantTookOver)
		}
	}
	identity := func(adapter, session, want string) {
		t.Helper()
		if got, err := Identity(root, adapter, session); err != nil || got != want {
			t.Fatalf("%s/%s = %q, %v; want %q", adapter, session, got, err, want)
		}
	}
	bind("claude", "first", "planner", "")
	bind("grok", "bystander", "writer", "")
	bind("claude", "first", "planner", "")
	bind("grok", "second", "planner", "claude")
	identity("claude", "first", "")
	identity("grok", "second", "planner")
	identity("grok", "bystander", "writer")
	bind("claude", "first", "planner", "grok")
	identity("grok", "second", "")
}

// Concurrent joins of one agent from several sessions leave it bound in
// exactly one.
func TestConcurrentBindsLeaveOneSessionBound(t *testing.T) {
	root := t.TempDir()
	sessions := []string{"s1", "s2", "s3", "s4"}
	for _, session := range sessions {
		if err := Serve(root, "claude", session); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	errs := make(chan error, len(sessions))
	for _, session := range sessions {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, err := Bind(root, "claude", session, "planner")
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	bound := 0
	for _, session := range sessions {
		if name, err := Identity(root, "claude", session); err != nil {
			t.Fatal(err)
		} else if name == "planner" {
			bound++
		}
	}
	if bound != 1 {
		t.Fatalf("%d sessions bound, want 1", bound)
	}
}
