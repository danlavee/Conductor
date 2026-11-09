package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/danlavee/Conductor/internal/adapters/binding"
	"github.com/danlavee/Conductor/internal/adapters/claude"
	"github.com/danlavee/Conductor/internal/adapters/hold"
	installer "github.com/danlavee/Conductor/internal/install"
	"github.com/danlavee/Conductor/internal/platform"
	conductor "github.com/danlavee/Conductor/internal/state"
)

// The wake is one contract on every host, and the rows hold only what the
// hosts differ in. A session nobody joined from stays quiet rather than fail
// every turn end; a join from a served session hands it to the adapter; an end
// of turn the host's filter passes over delivers nothing; the main agent's end
// of turn wakes it with the delivery, and only what reached the model is
// acknowledged; and the holder the wake spawned keeps the agent wakeable until
// the host ends the session.
func TestAdapterArmWakesTheSessionAndOnlyWhenBound(t *testing.T) {
	type hook = func(verb, stdin string) ([]byte, []byte, error)
	isWake := func(text string) bool {
		return strings.HasPrefix(text, "Conductor: ") && strings.Contains(text, "b: review")
	}
	release := func(t *testing.T, run hook, stdin string) {
		t.Helper()
		if _, stderr, err := run("release", stdin); err != nil {
			t.Fatalf("release %s: %v: %s", stdin, err, stderr)
		}
	}
	for _, host := range []struct {
		name, environment, session string
		serve, endTurn             string
		passed                     []string
		nothing                    string // what a hook that delivers nothing writes
		announces                  bool   // identity writes an announcement, not nothing
		run                        func(root, dir, verb, stdin string) ([]byte, []byte, error)
		woke                       func(stdout []byte, err error) bool
		end                        func(t *testing.T, root string, run hook, in cli)
	}{{
		name: "claude", environment: claude.SessionEnvironment, session: "session-1",
		serve: sessionEvent("session-1"), endTurn: sessionEvent("session-1"), announces: true,
		// Subagents fire the hook with the parent's session id.
		passed: []string{`{"session_id":"session-1","agent_id":"worker-1"}`},
		run: func(root, dir, verb, stdin string) ([]byte, []byte, error) {
			return cli{home: root, dir: dir, stdin: stdin, env: map[string]string{claude.SessionEnvironment: "session-1"}}.run("adapter", "claude", verb)
		},
		// The wake *is* the exit code, asserted as the literal the host
		// defines: stdout reaches the session on a waking exit and on no other.
		woke: func(stdout []byte, err error) bool {
			var exit *exec.ExitError
			return errors.As(err, &exit) && exit.ExitCode() == 2 && isWake(string(stdout))
		},
		end: func(t *testing.T, _ string, run hook, _ cli) { release(t, run, sessionEvent("session-1")) },
	}} {
		t.Run(host.name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "runtime-state")
			run := func(verb, stdin string) ([]byte, []byte, error) { return host.run(root, t.TempDir(), verb, stdin) }
			quiet := func(what, verb, stdin string) {
				t.Helper()
				if stdout, stderr, err := run(verb, stdin); err != nil || string(bytes.TrimSpace(stdout)) != host.nothing {
					t.Fatalf("%s = %s, %v: %s; want %q", what, stdout, err, stderr, host.nothing)
				}
			}
			quiet("arm in an unbound session", "arm", host.endTurn)
			if host.announces {
				if _, stderr, err := run("identity", host.serve); err != nil {
					t.Fatalf("identity: %v: %s", err, stderr)
				}
			} else {
				quiet("identity", "identity", host.serve)
			}
			in := cli{home: root, dir: t.TempDir(), env: map[string]string{host.environment: host.session}}
			stdout, stderr, err := in.run("a", "join", "dev")
			var answer struct {
				Wake string `json:"wake"`
			}
			if err != nil || json.Unmarshal(stdout, &answer) != nil || answer.Wake != "adapter" {
				t.Fatalf("join in a served session = %s, %v: %s", stdout, err, stderr)
			}
			// A second agent joining is work addressed to the first, so a hook
			// that got past its filter would deliver it.
			if _, stderr, err := (cli{home: root}).run("b", "join", "review"); err != nil {
				t.Fatalf("join b: %v: %s", err, stderr)
			}
			for _, stdin := range host.passed {
				quiet("arm on "+stdin, "arm", stdin)
			}

			if stdout, stderr, err := run("arm", host.endTurn); !host.woke(stdout, err) {
				t.Fatalf("arm = %s, %v: %s; want the wake", stdout, err, stderr)
			}
			delta, stderr, err := cli{home: root}.run("a", "get", "collaboration/agents", "--delta")
			var unread struct {
				Publications []json.RawMessage `json:"publications"`
			}
			if err != nil || json.Unmarshal(delta, &unread) != nil || len(unread.Publications) != 0 {
				t.Fatalf("delta after the wake = %s, %v: %s", delta, err, stderr)
			}

			owner := waitForOwner(t, root, "a")
			if _, stderr, err := (cli{home: root}).run("a", "watch"); err == nil || !bytes.Contains(stderr, []byte(`"LOCKED"`)) {
				t.Fatalf("watch while the holder runs = %v: %s", err, stderr)
			}
			host.end(t, root, run, in)
			waitFor(t, "the holder to exit with its session", func() bool {
				_, err := os.Stat(owner)
				return os.IsNotExist(err)
			})
		})
	}
}

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

// Every host's install goes through one path, so one table pins them all: the
// installed tree is worthless unless the binary it placed is the one the
// hooks will spawn, and the hooks are spawned with no shell, so the command
// each host's rewrite leaves in them is the whole wiring.
func TestAdapterInstallPlacesARunnableAdapter(t *testing.T) {
	exe := ""
	if runtime.GOOS == "windows" {
		exe = ".exe"
	}
	for _, host := range []struct{ name, adapter, manifest, hooks, command string }{
		{"claude", claude.AdapterName, ".claude-plugin/marketplace.json", "hooks/hooks.json", "${CLAUDE_PLUGIN_ROOT}/bin/conductor"},
	} {
		t.Run(host.name, func(t *testing.T) {
			destination := filepath.Join(t.TempDir(), ".conductor", "adapters", host.adapter)
			stdout, stderr, err := cli{home: t.TempDir()}.run("adapter", host.name, "install", destination)
			if err != nil {
				t.Fatalf("install: %v: %s", err, stderr)
			}
			var result installer.Result
			if err := json.Unmarshal(stdout, &result); err != nil || result.Status != "installed" || result.InstallPath != destination {
				t.Fatalf("install result = %s, %v", stdout, err)
			}
			if expected := filepath.Join(destination, "bin", "conductor"+exe); result.BinaryPath != expected {
				t.Fatalf("binary at %s, hooks look in %s", result.BinaryPath, expected)
			}
			if _, err := os.Stat(filepath.Join(destination, host.manifest)); err != nil {
				t.Errorf("the manifest the host is pointed at is missing: %v", err)
			}
			hooks, err := os.ReadFile(filepath.Join(destination, host.hooks))
			if err != nil || !strings.Contains(string(hooks), host.command) || strings.Contains(string(hooks), "${CONDUCTOR_BIN}") {
				t.Fatalf("installed hooks = %s, %v; want %s", hooks, err, host.command)
			}

			for name, args := range map[string][]string{
				"no destination":                   {},
				"two destinations":                 {"a", "b"},
				"a blank one":                      {"   "},
				"a relative one":                   {filepath.Join("adapters", host.adapter)},
				"one outside the adapter contract": {filepath.Join(t.TempDir(), "plugins", host.adapter)},
			} {
				if err := run(append([]string{"adapter", host.name, "install"}, args...)); err == nil {
					t.Errorf("install with %s was accepted", name)
				}
			}
		})
	}
}
