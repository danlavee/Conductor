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
	var report adapterReport
	if err := json.Unmarshal(stderr, &report); err != nil || report.Outcome != "unbound" {
		t.Fatalf("unbound report = %s: %v", stderr, err)
	}

	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, claude.IdentityFile), []byte("a\n"), 0o600); err != nil {
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

// TestAdapterHooksFailLoudlyWithoutTheirContext covers the defect the session
// flag exists to close: the adapter once read the session from a variable the
// host never exports, so every session shared the empty identifier and one
// session's teardown ended another's stream, silently. A hook that cannot tell
// which session or project it serves is misconfigured, and this host shows a
// failing hook's stderr to the user -- which is the point.
func TestAdapterHooksFailLoudlyWithoutTheirContext(t *testing.T) {
	root := filepath.Join(t.TempDir(), "runtime-state")
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, claude.IdentityFile), []byte("a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{"arm", "release", "identity"} {
		// The environment carries a usable session, so a run that succeeds
		// here has fallen back to it.
		for name, session := range map[string]string{
			"an empty substitution":        "",
			"an unsubstituted placeholder": claude.SessionPlaceholder,
		} {
			if _, stderr, err := runAdapterProcess(t, root, project, "session-1", "adapter", "claude", command, "--session="+session); err == nil {
				t.Errorf("%s with %s was accepted; stderr = %s", command, name, stderr)
			}
		}
		if _, stderr, err := runAdapterHelper(t, root, "", "session-1", "adapter", "claude", command); err == nil {
			t.Errorf("%s ran without the project directory the host owes it; stderr = %s", command, stderr)
		}
	}
}

// TestAdapterBindGivesTwoAgentsInOneProjectTheirOwnIdentities is the capability
// the session binding adds, through real processes, with one
// `.conductor-agent` naming one of the two.
func TestAdapterBindGivesTwoAgentsInOneProjectTheirOwnIdentities(t *testing.T) {
	root := filepath.Join(t.TempDir(), "runtime-state")
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, claude.IdentityFile), []byte("a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, agent := range []string{"a", "b"} {
		if _, stderr, err := runCLIHelper(os.Args[0], root, "", agent, "join", "dev"); err != nil {
			t.Fatalf("join %s: %v: %s", agent, err, stderr)
		}
	}

	// Binding is run by the model, so it takes its session from the
	// environment the host exports to child processes.
	stdout, stderr, err := runAdapterProcess(t, root, project, "session-2", "adapter", "claude", "bind", "b")
	if err != nil {
		t.Fatalf("bind: %v: %s", err, stderr)
	}
	var bound claude.Binding
	if err := json.Unmarshal(stdout, &bound); err != nil || bound != (claude.Binding{Agent: "b", Source: claude.FromSession}) {
		t.Fatalf("bind result = %s: %v", stdout, err)
	}

	// Both in the same directory, which the project file alone cannot express.
	assertIdentity(t, root, project, "session-2", claude.Binding{Agent: "b", Source: claude.FromSession})
	assertIdentity(t, root, project, "session-1", claude.Binding{Agent: "a", Source: claude.FromProject})

	// Teardown forgets the binding, or it would outlive every session that ever
	// ran here.
	if _, stderr, err := runAdapterHelper(t, root, project, "session-2", "adapter", "claude", "release"); err != nil {
		t.Fatalf("release: %v: %s", err, stderr)
	}
	assertIdentity(t, root, project, "session-2", claude.Binding{Agent: "a", Source: claude.FromProject})
}

// TestAdapterBindRefusesWhatWouldBeAQuietNoOp covers the gates
// `.conductor-agent` cannot have: bind is run by an agent that has already
// joined, inside a session that exists, so both can be refused outright.
func TestAdapterBindRefusesWhatWouldBeAQuietNoOp(t *testing.T) {
	root := filepath.Join(t.TempDir(), "runtime-state")
	if _, stderr, err := runCLIHelper(os.Args[0], root, "", "a", "join", "dev"); err != nil {
		t.Fatalf("join a: %v: %s", err, stderr)
	}
	if _, _, err := runAdapterProcess(t, root, t.TempDir(), "session-1", "adapter", "claude", "bind", "nobody"); err == nil {
		t.Fatal("bound an identity that is not on the roster")
	}
	if _, _, err := runAdapterProcess(t, root, t.TempDir(), "", "adapter", "claude", "bind", "a"); err == nil {
		t.Fatal("bound without knowing which session was asking")
	}
}

func assertIdentity(t *testing.T, root, project, session string, want claude.Binding) {
	t.Helper()
	stdout, stderr, err := runAdapterHelper(t, root, project, session, "adapter", "claude", "identity")
	if err != nil {
		t.Fatalf("identity for %s: %v: %s", session, err, stderr)
	}
	var report identityReport
	if err := json.Unmarshal(stdout, &report); err != nil {
		t.Fatalf("identity report = %s: %v", stdout, err)
	}
	if report.Binding != want || report.Advice == "" {
		t.Fatalf("%s resolved to %+v; want %+v", session, report, want)
	}
}

// runAdapterHelper spawns a hook the way the host does: the session arrives as
// a substituted argument, and CLAUDE_CODE_SESSION_ID is set because the host
// exports it to every child -- including hooks, where it must be ignored.
func runAdapterHelper(t *testing.T, root, project, session string, args ...string) ([]byte, []byte, error) {
	t.Helper()
	return runAdapterProcess(t, root, project, session, append(args, "--session="+session)...)
}

// runAdapterProcess runs from a directory with no identity file above it, so
// only the project the environment names can answer.
func runAdapterProcess(t *testing.T, root, project, session string, args ...string) ([]byte, []byte, error) {
	t.Helper()
	command := exec.Command(os.Args[0], args...)
	command.Dir = t.TempDir()
	command.Env = replaceEnvironment(os.Environ(), cliHelperEnvironment, "1")
	command.Env = replaceEnvironment(command.Env, "CONDUCTOR_HOME", root)
	command.Env = replaceEnvironment(command.Env, claude.ProjectEnvironment, project)
	command.Env = replaceEnvironment(command.Env, claude.SessionEnvironment, session)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}
