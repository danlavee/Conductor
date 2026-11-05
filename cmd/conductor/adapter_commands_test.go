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

	"github.com/danlavee/Conductor/internal/adapters/claude"
)

// On this host the wake *is* the exit code, so the exit code is the contract
// and only a real process can be asked about it. An adapter that prints a
// delivery and exits cleanly has done nothing at all: stdout reaches the
// session on a waking exit and on no other.
func TestAdapterArmWakesTheSessionAndOnlyWhenBound(t *testing.T) {
	root := filepath.Join(t.TempDir(), "runtime-state")

	// A session nobody joined from must stay silent rather than fail every
	// turn end, and must not wake anything.
	stdout, stderr, err := runAdapterHelper(t, root, "session-1", "adapter", "claude", "arm")
	if err != nil {
		t.Fatalf("arm in an unbound session: %v: %s", err, stderr)
	}
	if len(stdout) != 0 {
		t.Fatalf("an unbound arm wrote to the model: %s", stdout)
	}
	var report adapterReport
	if err := json.Unmarshal(stderr, &report); err != nil || report.Outcome != "unbound" {
		t.Fatalf("unbound report = %s: %v", stderr, err)
	}

	assertIdentity(t, root, "session-1", "")
	if _, stderr, err := runAdapterProcess(t, root, "session-1", "a", "join", "dev"); err != nil {
		t.Fatalf("join a: %v: %s", err, stderr)
	}
	// A second agent joining is work addressed to the first, so arming now has
	// something to deliver.
	if _, stderr, err := runCLIHelper(os.Args[0], root, "", "b", "join", "review"); err != nil {
		t.Fatalf("join b: %v: %s", err, stderr)
	}

	stdout, stderr, err = runAdapterHelper(t, root, "session-1", "adapter", "claude", "arm")
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
// through a real process: the installed tree is worthless unless the binary it
// placed is the one the hooks will spawn.
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
	if _, err := os.Stat(filepath.Join(destination, ".claude-plugin", "marketplace.json")); err != nil {
		t.Errorf("the marketplace manifest the host is pointed at is missing: %v", err)
	}
	if _, _, err := runCLIHelper(os.Args[0], t.TempDir(), "", "adapter", "claude", "install", filepath.Join(t.TempDir(), "plugins", "claude-code")); err == nil {
		t.Fatal("a destination outside the adapter contract was accepted")
	}
}

// TestAdapterDispatchRejectsMalformedInvocations pins the arg shapes. A hook
// command that silently accepted a stray argument would run at every turn end
// with nobody reading its stderr.
func TestAdapterDispatchRejectsMalformedInvocations(t *testing.T) {
	for name, args := range map[string][]string{
		"arm with a stray argument":     {"adapter", "claude", "arm", "--session=s", "extra"},
		"arm with no session at all":    {"adapter", "claude", "arm"},
		"arm with a bare positional":    {"adapter", "claude", "arm", "session-1"},
		"identity with no session":      {"adapter", "claude", "identity"},
		"install with no destination":   {"adapter", "claude", "install"},
		"install with two destinations": {"adapter", "claude", "install", "a", "b"},
		"install with a blank one":      {"adapter", "claude", "install", "   "},
		"a relative destination":        {"adapter", "claude", "install", filepath.Join("adapters", "claude-code")},
		"the retired bind":              {"adapter", "claude", "bind", "a"},
		"an unknown command":            {"adapter", "claude", "enable"},
		"an unknown host":               {"adapter", "codex", "arm"},
	} {
		if err := run(args); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

// TestAdapterHooksFailLoudlyWithoutTheirContext covers the defect the session
// flag exists to close: the adapter once read the session from a variable the
// host never exports, so every session shared the empty identifier and one
// session's teardown ended another's stream, silently. A hook that cannot tell
// which session or project it serves is misconfigured, and this host shows a
// failing hook's stderr to the user -- which is the point.
func TestAdapterHooksFailLoudlyWithoutTheirContext(t *testing.T) {
	root := filepath.Join(t.TempDir(), "runtime-state")
	for _, command := range []string{"arm", "release", "identity"} {
		// The environment carries a usable session, so a run that succeeds
		// here has fallen back to it.
		for name, session := range map[string]string{
			"an empty substitution":        "",
			"an unsubstituted placeholder": claude.SessionPlaceholder,
		} {
			if _, stderr, err := runAdapterProcess(t, root, "session-1", "adapter", "claude", command, "--session="+session); err == nil {
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
		stdout, stderr, err := runAdapterProcess(t, root, join[0], join[1], "join", "dev")
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

	if _, stderr, err := runAdapterHelper(t, root, "session-2", "adapter", "claude", "release"); err != nil {
		t.Fatalf("release: %v: %s", err, stderr)
	}
	assertIdentity(t, root, "session-2", "")
}

// assertIdentity runs the session-start hook, which serves the session, and
// checks whom it announces.
func assertIdentity(t *testing.T, root, session, want string) {
	t.Helper()
	stdout, stderr, err := runAdapterHelper(t, root, session, "adapter", "claude", "identity")
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

// runAdapterHelper spawns a hook the way the host does: the session arrives as
// a substituted argument, and CLAUDE_CODE_SESSION_ID is set because the host
// exports it to every child -- including hooks, where it must be ignored.
func runAdapterHelper(t *testing.T, root, session string, args ...string) ([]byte, []byte, error) {
	t.Helper()
	return runAdapterProcess(t, root, session, append(args, "--session="+session)...)
}

// runAdapterProcess runs a command inside a host session, which the host names
// in the environment of everything the session starts.
func runAdapterProcess(t *testing.T, root, session string, args ...string) ([]byte, []byte, error) {
	t.Helper()
	command := exec.Command(os.Args[0], args...)
	command.Dir = t.TempDir()
	command.Env = replaceEnvironment(os.Environ(), cliHelperEnvironment, "1")
	command.Env = replaceEnvironment(command.Env, "CONDUCTOR_HOME", root)
	command.Env = replaceEnvironment(command.Env, claude.SessionEnvironment, session)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}
