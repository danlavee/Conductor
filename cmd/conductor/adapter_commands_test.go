package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// On this host the wake *is* the exit code, so the exit code is the contract
// and only a real process can be asked about it. An adapter that prints a
// delivery and exits cleanly has done nothing at all: stdout reaches the
// session on a waking exit and on no other.
func TestAdapterArmWakesTheSessionAndOnlyWhenBound(t *testing.T) {
	root := filepath.Join(t.TempDir(), "runtime-state")
	unbound := t.TempDir()

	// A project that never opted in must stay silent rather than fail every
	// session start, and must not wake anything.
	stdout, stderr, err := runAdapterHelper(t, root, unbound, "session-1", "adapter", "claude", "arm")
	if err != nil {
		t.Fatalf("arm in an unbound project: %v: %s", err, stderr)
	}
	if len(stdout) != 0 {
		t.Fatalf("an unbound arm wrote to the model: %s", stdout)
	}
	// The code is spelled literally for the same reason the exit code below is:
	// its whole value is that it does not move, and a test that quotes the
	// constant back would keep passing if it did.
	var report adapterReport
	if err := json.Unmarshal(stderr, &report); err != nil {
		t.Fatalf("unbound report = %s: %v", stderr, err)
	}
	if report.Outcome != "unbound" || report.Code != 10 {
		t.Fatalf("unbound report = %+v", report)
	}

	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, ".conductor-agent"), []byte("a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, stderr, err := runCLIHelper(os.Args[0], root, "", "a", "join", "dev"); err != nil {
		t.Fatalf("join a: %v: %s", err, stderr)
	}
	// A second agent joining is work addressed to the first, so arming now has
	// something to deliver.
	if _, stderr, err := runCLIHelper(os.Args[0], root, "", "b", "join", "review"); err != nil {
		t.Fatalf("join b: %v: %s", err, stderr)
	}

	stdout, stderr, err = runAdapterHelper(t, root, project, "session-1", "adapter", "claude", "arm")
	// Asserted as the literal the host defines, not as the constant the code
	// under test reads: 2 is the contract, and a test that quotes the same
	// constant back would keep passing if it drifted.
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 2 {
		t.Fatalf("arm exit = %v, stderr = %s", err, stderr)
	}
	if !json.Valid(stdout) || len(bytes.TrimSpace(stdout)) == 0 {
		t.Fatalf("the waking exit carried no delivery: %s", stdout)
	}
}

// TestAdapterInstallPlacesARunnableAdapter exercises the placement end to end
// through a real process, because that is the only way to ask the question that
// matters: the installed tree is worthless unless the binary it placed is the
// one the hooks will spawn, and runnable when they do.
func TestAdapterInstallPlacesARunnableAdapter(t *testing.T) {
	destination := filepath.Join(t.TempDir(), ".conductor", "adapters", "claude-code")
	stdout, stderr, err := runCLIHelper(os.Args[0], t.TempDir(), "", "adapter", "claude", "install", destination)
	if err != nil {
		t.Fatalf("adapter install: %v: %s", err, stderr)
	}
	var result struct {
		Status     string `json:"status"`
		BinaryPath string `json:"binary_path"`
	}
	if err := json.Unmarshal(stdout, &result); err != nil {
		t.Fatalf("install result = %s: %v", stdout, err)
	}
	if result.Status != "installed" {
		t.Fatalf("install result = %+v", result)
	}
	// The hooks name ${CLAUDE_PLUGIN_ROOT}/bin/conductor and are spawned with
	// no shell, so this exact path is the whole wiring between the plugin the
	// host loads and the executable that holds the stream.
	expected := filepath.Join(destination, "bin", "conductor")
	if runtime.GOOS == "windows" {
		expected += ".exe"
	}
	if result.BinaryPath != expected {
		t.Fatalf("binary at %s, hooks look in %s", result.BinaryPath, expected)
	}
	// That the placed binary runs is not asserted here: Install smoke-checks
	// the staged executable before publishing it, and running it again from
	// this process would re-enter the test binary rather than the CLI.
	if _, err := os.Stat(filepath.Join(destination, ".claude-plugin", "marketplace.json")); err != nil {
		t.Errorf("the marketplace manifest the host is pointed at is missing: %v", err)
	}
	if _, _, err := runCLIHelper(os.Args[0], t.TempDir(), "", "adapter", "claude", "install", filepath.Join(t.TempDir(), "plugins", "claude-code")); err == nil {
		t.Fatal("a destination outside the adapter contract was accepted")
	}
}

// TestAdapterDispatchRejectsMalformedInvocations pins the arg shapes, because
// adding `install` loosened the gate every other adapter command passes
// through: the arity check used to be a single equality and is now split
// either side of a branch. A command that silently accepted a stray argument
// would run from a hook at every turn end with nobody reading its stderr.
func TestAdapterDispatchRejectsMalformedInvocations(t *testing.T) {
	for name, args := range map[string][]string{
		"arm with a stray argument":     {"adapter", "claude", "arm", "--session=s", "extra"},
		"arm with no session at all":    {"adapter", "claude", "arm"},
		"arm with a bare positional":    {"adapter", "claude", "arm", "session-1"},
		"install with no destination":   {"adapter", "claude", "install"},
		"install with two destinations": {"adapter", "claude", "install", "a", "b"},
		"install with a blank one":      {"adapter", "claude", "install", "   "},
		"a relative destination":        {"adapter", "claude", "install", filepath.Join("adapters", "claude-code")},
		"bind with no agent":            {"adapter", "claude", "bind"},
		"bind with a blank agent":       {"adapter", "claude", "bind", "   "},
		"bind with two agents":          {"adapter", "claude", "bind", "a", "b"},
		"an unknown command":            {"adapter", "claude", "enable"},
		"an unknown host":               {"adapter", "codex", "arm"},
	} {
		if err := run(args); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

// TestAdapterRefusesASessionItCannotTrust is the regression guard for the
// defect this whole change exists to close. The adapter used to read the
// session from an environment variable the host never exports, so every hook
// computed the empty string, every session shared it, and one session's
// teardown ended another session's stream. Nothing reported anything.
//
// Each case below therefore has to *fail loudly*. A hook that cannot tell which
// session it is running in is misconfigured, and this host shows a non-zero
// hook's stderr to the user -- which is the point, because the version of this
// bug that shipped was invisible precisely because it exited zero.
func TestAdapterRefusesASessionItCannotTrust(t *testing.T) {
	root := filepath.Join(t.TempDir(), "runtime-state")
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, ".conductor-agent"), []byte("a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, session := range map[string]string{
		"an empty substitution":        "",
		"an unsubstituted placeholder": "${CLAUDE_SESSION_ID}",
	} {
		t.Run(name, func(t *testing.T) {
			// Passed through the argument the host would have substituted, with
			// the environment variable set to a perfectly good value: reading
			// that variable instead is the mistake, so a run that succeeds here
			// has fallen back to it.
			_, stderr, err := runAdapterProcess(t, root, project, "session-1", "adapter", "claude", "arm", "--session="+session)
			if err == nil {
				t.Fatalf("%s was accepted; stderr = %s", name, stderr)
			}
		})
	}
}

// TestAdapterBindGivesTwoAgentsInOneProjectTheirOwnIdentities is the capability
// the session binding adds, asserted where it actually has to hold: through
// real processes, with one `.conductor-agent` naming one of the two.
func TestAdapterBindGivesTwoAgentsInOneProjectTheirOwnIdentities(t *testing.T) {
	root := filepath.Join(t.TempDir(), "runtime-state")
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, ".conductor-agent"), []byte("a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, agent := range []string{"a", "b"} {
		if _, stderr, err := runCLIHelper(os.Args[0], root, "", agent, "join", "dev"); err != nil {
			t.Fatalf("join %s: %v: %s", agent, err, stderr)
		}
	}

	// Binding is run by the model, not by a hook, so it takes its session from
	// the environment the host exports to child processes rather than from an
	// argument nobody would substitute for it.
	stdout, stderr, err := runAdapterProcess(t, root, project, "session-2", "adapter", "claude", "bind", "b")
	if err != nil {
		t.Fatalf("bind: %v: %s", err, stderr)
	}
	var bound bindResult
	if err := json.Unmarshal(stdout, &bound); err != nil {
		t.Fatalf("bind result = %s: %v", stdout, err)
	}
	if bound.Agent != "b" || bound.Session != "session-2" || bound.Previous != "" {
		t.Fatalf("bind result = %+v", bound)
	}

	// The bound session acts as b; the unbound one still reads the project file
	// and acts as a. Both in the same directory, which is what could not be
	// expressed before.
	assertIdentity(t, root, project, "session-2", "b", "session")
	assertIdentity(t, root, project, "session-1", "a", "project")

	// Teardown forgets the binding, or the record would outlive every session
	// that ever ran here -- which is exactly the litter the previous iteration
	// of this idea left behind.
	if _, stderr, err := runAdapterHelper(t, root, project, "session-2", "adapter", "claude", "release"); err != nil {
		t.Fatalf("release: %v: %s", err, stderr)
	}
	assertIdentity(t, root, project, "session-2", "a", "project")
}

// TestAdapterBindRefusesAnIdentityThatNeverJoined covers the gate that
// `.conductor-agent` cannot have. A typo there is caught later as a quiet
// unregistered no-op; bind is run by an agent that has already joined, so the
// same typo can be refused outright, once, while someone is reading.
func TestAdapterBindRefusesAnIdentityThatNeverJoined(t *testing.T) {
	root := filepath.Join(t.TempDir(), "runtime-state")
	if _, stderr, err := runCLIHelper(os.Args[0], root, "", "a", "join", "dev"); err != nil {
		t.Fatalf("join a: %v: %s", err, stderr)
	}
	if _, _, err := runAdapterProcess(t, root, t.TempDir(), "session-1", "adapter", "claude", "bind", "nobody"); err == nil {
		t.Fatal("bound an identity that is not on the roster")
	}
	// And it must refuse when it cannot tell which session it is in at all,
	// rather than binding some other session by accident.
	if _, _, err := runAdapterProcess(t, root, t.TempDir(), "", "adapter", "claude", "bind", "a"); err == nil {
		t.Fatal("bound without knowing which session was asking")
	}
}

func assertIdentity(t *testing.T, root, project, session, agent, source string) {
	t.Helper()
	stdout, stderr, err := runAdapterHelper(t, root, project, session, "adapter", "claude", "identity")
	if err != nil {
		t.Fatalf("identity for %s: %v: %s", session, err, stderr)
	}
	var report identityReport
	if err := json.Unmarshal(stdout, &report); err != nil {
		t.Fatalf("identity report = %s: %v", stdout, err)
	}
	if report.Status.Agent != agent || report.Source != source || report.Session != session {
		t.Fatalf("%s resolved to %+v; want agent %q from the %s binding", session, report, agent, source)
	}
}

// runAdapterHelper spawns a hook the way the host does: the session arrives as
// a substituted argument, and CLAUDE_CODE_SESSION_ID is set because the host
// exports it to every child -- including hooks, where it must be ignored.
func runAdapterHelper(t *testing.T, root, project, session string, args ...string) ([]byte, []byte, error) {
	t.Helper()
	return runAdapterProcess(t, root, project, session, append(args, "--session="+session)...)
}

func runAdapterProcess(t *testing.T, root, project, session string, args ...string) ([]byte, []byte, error) {
	t.Helper()
	command := exec.Command(os.Args[0], args...)
	command.Env = replaceEnvironment(os.Environ(), cliHelperEnvironment, "1")
	command.Env = replaceEnvironment(command.Env, "CONDUCTOR_HOME", root)
	command.Env = replaceEnvironment(command.Env, "CLAUDE_PROJECT_DIR", project)
	command.Env = replaceEnvironment(command.Env, "CLAUDE_CODE_SESSION_ID", session)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}
