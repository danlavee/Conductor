package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/danlavee/Conductor/internal/adapters/claude"
	conductor "github.com/danlavee/Conductor/internal/state"
)

// PostToolUse arms too, so a turn a wake started gets a watcher mid-turn. It
// fires on every tool result of a turn whose watcher is live, where it must
// leave that watcher alone and write nothing to the model.
func TestAToolResultArmsOnlyAMissingWatcher(t *testing.T) {
	root := filepath.Join(t.TempDir(), "runtime-state")
	assertIdentity(t, root, "session-1", "")
	if _, stderr, err := inSession(t, root, "session-1").run("a", "join", "dev"); err != nil {
		t.Fatalf("join a: %v: %s", err, stderr)
	}

	// Nothing is pending, so this watcher waits.
	hook := inSession(t, root, "session-1")
	hook.stdin = sessionEvent("session-1")
	watcher := hook.command("adapter", "claude", "arm")
	if err := watcher.Start(); err != nil {
		t.Fatal(err)
	}
	defer stopWatch(t, watcher)
	waker := filepath.Join(root, "adapters", "watch", "a.json")
	waitFor(t, "the first watcher to register", func() bool {
		data, err := os.ReadFile(waker)
		return err == nil && bytes.Contains(data, []byte(`"pid":`+strconv.Itoa(watcher.Process.Pid)))
	})
	if stdout, stderr, err := claudeHook(t, root, "session-1", "arm"); err != nil || len(stdout) != 0 {
		t.Fatalf("an arm beside a live watcher = %s, %v: %s", stdout, err, stderr)
	}
	if _, stderr, err := claudeHook(t, root, "session-1", "release"); err != nil {
		t.Fatalf("release: %v: %s", err, stderr)
	}
}

// The holder a watcher spawns is routed for every wake host: it holds the
// session's agent until the session is forgotten, and does nothing for a
// session nobody joined from.
func TestAdapterHoldKeepsTheSessionWakeableUntilItEnds(t *testing.T) {
	root := filepath.Join(t.TempDir(), "runtime-state")
	args := []string{"adapter", claude.BindingName, "hold", "--session=session-1", "--target=" + selfTarget(t).String()}
	assertIdentity(t, root, "session-1", "")
	if _, stderr, err := inSession(t, root, "session-1").run(args...); err != nil {
		t.Fatalf("hold in an unbound session: %v: %s", err, stderr)
	}
	if _, stderr, err := inSession(t, root, "session-1").run("a", "join", "dev"); err != nil {
		t.Fatalf("join a: %v: %s", err, stderr)
	}

	holder := cli{home: root, dir: t.TempDir()}.command(args...)
	var stderr bytes.Buffer
	holder.Stderr = &stderr
	if err := holder.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- holder.Wait() }()

	t.Cleanup(func() { _ = holder.Process.Kill() })
	waitForOwner(t, root, "a")
	client, err := conductor.Open(root, "a")
	if err != nil {
		t.Fatal(err)
	}
	var protocol *conductor.ProtocolError
	if _, err := client.AcquireWatchOwnership(conductor.HolderSelf, ""); !errors.As(err, &protocol) || protocol.Code != "LOCKED" {
		t.Fatalf("watch while the holder runs = %v, want LOCKED", err)
	}

	if _, stderr, err := claudeHook(t, root, "session-1", "release"); err != nil {
		t.Fatalf("release: %v: %s", err, stderr)
	}
	select {
	case err := <-exited:
		if err != nil {
			t.Fatalf("holder exit = %v: %s", err, stderr.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the holder outlived its forgotten session")
	}
}

// TestAdapterDispatchRejectsMalformedInvocations pins the arg shapes. A hook
// command that silently accepted a stray argument would run at every turn end
// with nobody reading its stderr.
func TestAdapterDispatchRejectsMalformedInvocations(t *testing.T) {
	for name, args := range map[string][]string{
		"arm with a bare positional":   {"adapter", "claude", "arm", "session-1"},
		"identity with the flag":       {"adapter", "claude", "identity", "--session=s"},
		"hold with no target":          {"adapter", "claude", "hold", "--session=s"},
		"hold with a malformed target": {"adapter", "claude", "hold", "--session=s", "--target=s"},
	} {
		if err := run(args); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

// TestAdapterHooksFailLoudlyWithoutTheirContext covers the defect the stdin
// session exists to close: the adapter once read the session from a variable
// the host never exports, so every session shared the empty identifier and one
// session's teardown ended another's stream, silently. A hook that cannot tell
// which session it serves is misconfigured, and this host shows a failing
// hook's stderr to the user -- which is the point.
func TestAdapterHooksFailLoudlyWithoutTheirContext(t *testing.T) {
	root := filepath.Join(t.TempDir(), "runtime-state")
	for _, command := range []string{"arm", "release", "identity"} {
		// The environment carries a usable session, so a run that succeeds
		// here has fallen back to it.
		for name, event := range map[string]string{
			"no event":                  "",
			"an event that is not JSON": "session-1",
			"no session_id":             `{"agent_id":"worker-1"}`,
			"an empty session_id":       `{"session_id":""}`,
			"an unusable session_id":    `{"session_id":"../session-1"}`,
		} {
			if _, stderr, err := claudeEvent(t, root, "session-1", command, event); err == nil {
				t.Errorf("%s with %s was accepted; stderr = %s", command, name, stderr)
			}
		}
	}
}

// Joining is the agent's whole part of being wakeable where an adapter serves
// the session: the join answers "adapter" and binds the session, and two
// sessions in one directory act as two agents. A session no hook served is
// one the agent wakes itself, and a released one is forgotten.
func TestJoinHandsAServedSessionToItsAdapter(t *testing.T) {
	root := filepath.Join(t.TempDir(), "runtime-state")
	assertIdentity(t, root, "session-1", "")
	assertIdentity(t, root, "session-2", "")

	for _, join := range [][3]string{{"session-1", "a", "adapter"}, {"session-2", "b", "adapter"}, {"session-3", "c", "watch"}} {
		stdout, stderr, err := inSession(t, root, join[0]).run(join[1], "join", "dev")
		if err != nil {
			t.Fatalf("join %s: %v: %s", join[1], err, stderr)
		}
		var answer struct {
			Wake string `json:"wake"`
		}
		if err := json.Unmarshal(stdout, &answer); err != nil || answer.Wake != join[2] {
			t.Fatalf("join %s in %s: wake = %q, %v; want %q", join[1], join[0], answer.Wake, err, join[2])
		}
	}
	assertIdentity(t, root, "session-1", "a")
	assertIdentity(t, root, "session-2", "b")

	if _, stderr, err := claudeHook(t, root, "session-2", "release"); err != nil {
		t.Fatalf("release: %v: %s", err, stderr)
	}
	assertIdentity(t, root, "session-2", "")
}

// Joining as an agent bound in another session takes it over, and the join
// says so; the first join, and joining again in the same session, do not.
func TestJoinReportsTakingTheAgentOverFromAnotherSession(t *testing.T) {
	root := filepath.Join(t.TempDir(), "runtime-state")
	assertIdentity(t, root, "session-1", "")
	assertIdentity(t, root, "session-2", "")
	for _, join := range [][3]string{{"session-1", "dev", ""}, {"session-1", "", ""}, {"session-2", "", "claude"}} {
		args := []string{"a", "join"}
		if join[1] != "" {
			args = append(args, join[1])
		}
		stdout, stderr, err := inSession(t, root, join[0]).run(args...)
		if err != nil {
			t.Fatalf("join in %s: %v: %s", join[0], err, stderr)
		}
		var answer struct {
			Wake     string `json:"wake"`
			TookOver string `json:"took_over"`
		}
		if err := json.Unmarshal(stdout, &answer); err != nil || answer.Wake != "adapter" || answer.TookOver != join[2] {
			t.Fatalf("join in %s = %+v, %v; want took_over %q", join[0], answer, err, join[2])
		}
	}
	assertIdentity(t, root, "session-1", "")
	assertIdentity(t, root, "session-2", "a")
}

// The last join wins outside an adapter session too: a join that binds no
// session unbinds the agent's adapter session, so that holder releases and
// cannot lock the new watcher out.
func TestAWatchJoinTakesTheAgentOverFromAnAdapterSession(t *testing.T) {
	root := filepath.Join(t.TempDir(), "runtime-state")
	assertIdentity(t, root, "session-1", "")
	if _, stderr, err := inSession(t, root, "session-1").run("a", "join", "dev"); err != nil {
		t.Fatalf("join in session-1: %v: %s", err, stderr)
	}
	stdout, stderr, err := cli{home: root, dir: t.TempDir()}.run("a", "join")
	if err != nil {
		t.Fatalf("join outside a session: %v: %s", err, stderr)
	}
	var answer struct {
		Wake     string `json:"wake"`
		TookOver string `json:"took_over"`
	}
	if err := json.Unmarshal(stdout, &answer); err != nil || answer.Wake != "watch" || answer.TookOver != claude.BindingName {
		t.Fatalf("join outside a session = %+v, %v; want watch, took_over %q", answer, err, claude.BindingName)
	}
	assertIdentity(t, root, "session-1", "")
}

// assertIdentity runs the session-start hook, which serves the session, and
// checks whom it announces.
func assertIdentity(t *testing.T, root, session, want string) {
	t.Helper()
	stdout, stderr, err := claudeHook(t, root, session, "identity")
	if err != nil {
		t.Fatalf("identity for %s: %v: %s", session, err, stderr)
	}
	var report identityReport
	if err := json.Unmarshal(stdout, &report); err != nil {
		t.Fatalf("identity report = %s: %v", stdout, err)
	}
	if report.Agent != want || report.Advice == "" {
		t.Fatalf("%s announced %+v; want agent %q", session, report, want)
	}
}

// claudeHook runs a hook the way the host does: the session arrives in the
// event on stdin, inside a session the environment also names.
func claudeHook(t *testing.T, root, session, verb string) ([]byte, []byte, error) {
	return claudeEvent(t, root, session, verb, sessionEvent(session))
}

// claudeEvent runs a hook in session with event as the host's stdin.
func claudeEvent(t *testing.T, root, session, verb, event string) ([]byte, []byte, error) {
	hook := inSession(t, root, session)
	hook.stdin = event
	return hook.run("adapter", "claude", verb)
}

func sessionEvent(session string) string { return `{"session_id":"` + session + `"}` }

// inSession is a process started inside a host session, which the host names
// in the environment of everything the session starts -- hooks included,
// where it must be ignored.
func inSession(t *testing.T, root, session string) cli {
	return cli{home: root, dir: t.TempDir(), env: map[string]string{claude.SessionEnvironment: session}}
}
