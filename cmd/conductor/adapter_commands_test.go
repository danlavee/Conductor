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
	// argument nobody would substitute for it. The hook that serves the session
	// has already run, as it does before the model gets its first turn.
	serveSession(t, root, project, "session-2")
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

// TestAdapterReleaseClearsASessionThatBoundNothing covers the half of teardown
// the unbound short-circuit used to skip. Most served sessions bind no
// identity, and the record saying the adapter served them is written for every
// one -- so a teardown that stops at "nothing bound here" leaves a record that
// outlives its session and reports an adapter into whatever session the host
// hands that identifier to next.
func TestAdapterReleaseClearsASessionThatBoundNothing(t *testing.T) {
	root := filepath.Join(t.TempDir(), "runtime-state")
	project := t.TempDir()
	serveSession(t, root, project, "session-1")
	served, err := claude.ServesSession(root, "session-1")
	if err != nil || !served {
		t.Fatalf("served = %v, %v; want the hook's own record", served, err)
	}
	if _, stderr, err := runAdapterHelper(t, root, project, "session-1", "adapter", "claude", "release"); err != nil {
		t.Fatalf("release: %v: %s", err, stderr)
	}
	if served, err = claude.ServesSession(root, "session-1"); err != nil || served {
		t.Fatalf("served = %v, %v; want teardown to clear a session that bound nothing", served, err)
	}
}

// TestAdapterBindRefusesWhatWouldBeAQuietNoOp covers the gates that
// `.conductor-agent` cannot have. A binding is read by exactly one thing, in
// exactly one session, under exactly one name; each of these would otherwise
// write a record nothing ever reads and say nothing about it for the session's
// whole life. bind is run by an agent that has already joined, in a session a
// hook has already served, so all three can be refused outright, once, while
// someone is reading.
func TestAdapterBindRefusesWhatWouldBeAQuietNoOp(t *testing.T) {
	root := filepath.Join(t.TempDir(), "runtime-state")
	project := t.TempDir()
	if _, stderr, err := runCLIHelper(os.Args[0], root, "", "a", "join", "dev"); err != nil {
		t.Fatalf("join a: %v: %s", err, stderr)
	}
	// No hook has served this session, which is every Claude Code session on a
	// host that installed the skill and not the plugin.
	if _, _, err := runAdapterProcess(t, root, project, "session-1", "adapter", "claude", "bind", "a"); err == nil {
		t.Fatal("bound in a session no adapter serves")
	}
	serveSession(t, root, project, "session-1")
	if _, _, err := runAdapterProcess(t, root, project, "session-1", "adapter", "claude", "bind", "nobody"); err == nil {
		t.Fatal("bound an identity that is not on the roster")
	}
	// And it must refuse when it cannot tell which session it is in at all,
	// rather than binding some other session by accident.
	if _, _, err := runAdapterProcess(t, root, t.TempDir(), "", "adapter", "claude", "bind", "a"); err == nil {
		t.Fatal("bound without knowing which session was asking")
	}
}

// TestAdapterIdentityAnswersTheAgentThatRunsIt covers the question an agent
// actually asks: `status` says nothing can wake me, and I need to know whether
// that is because no binding names this session or because one does and the
// adapter is not running. Those have different fixes, and only this command
// separates them.
//
// It is run by the model rather than by a hook, so it takes the session from the
// environment with no argument at all -- and it has to answer when nothing is
// bound, because that is the case whose fix the caller can carry out alone.
func TestAdapterIdentityAnswersTheAgentThatRunsIt(t *testing.T) {
	root := filepath.Join(t.TempDir(), "runtime-state")
	project := t.TempDir()
	if _, stderr, err := runCLIHelper(os.Args[0], root, "", "a", "join", "dev"); err != nil {
		t.Fatalf("join a: %v: %s", err, stderr)
	}

	// Nothing names this session and the project names nobody either.
	stdout, stderr, err := runAdapterProcess(t, root, project, "session-1", "adapter", "claude", "identity")
	if err != nil {
		t.Fatalf("identity without an argument: %v: %s", err, stderr)
	}
	var report identityReport
	if err := json.Unmarshal(stdout, &report); err != nil {
		t.Fatalf("identity report = %s: %v", stdout, err)
	}
	if report.Source != "unbound" || report.Session != "session-1" {
		t.Fatalf("unbound session reported %+v", report)
	}

	// Bound, and the same argument-free call now names the identity and reports
	// that nothing holds it -- which is the second of the two causes.
	serveSession(t, root, project, "session-1")
	if _, stderr, err := runAdapterProcess(t, root, project, "session-1", "adapter", "claude", "bind", "a"); err != nil {
		t.Fatalf("bind: %v: %s", err, stderr)
	}
	stdout, stderr, err = runAdapterProcess(t, root, project, "session-1", "adapter", "claude", "identity")
	if err != nil {
		t.Fatalf("identity after bind: %v: %s", err, stderr)
	}
	if err := json.Unmarshal(stdout, &report); err != nil {
		t.Fatalf("identity report = %s: %v", stdout, err)
	}
	if report.Source != "session" || report.Agent != "a" {
		t.Fatalf("bound session reported %+v", report)
	}
}

// TestAdapterKeepsTheHardSessionGuardOnWhatItChanges pins the asymmetry to the
// commands that earn it. identity only reports, so it may fall back to the
// environment; arm and release change what a session owns, and a teardown run
// against a guessed identifier is the original defect. Letting the fallback
// spread to them would reintroduce it wearing a different name.
func TestAdapterKeepsTheHardSessionGuardOnWhatItChanges(t *testing.T) {
	root := filepath.Join(t.TempDir(), "runtime-state")
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, ".conductor-agent"), []byte("a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// The environment carries a perfectly usable session, so a command that
	// succeeds here has fallen back to it.
	for _, command := range []string{"arm", "release"} {
		if _, stderr, err := runAdapterProcess(t, root, project, "session-1", "adapter", "claude", command); err == nil {
			t.Fatalf("%s ran without being told which session it was in; stderr = %s", command, stderr)
		}
	}
}

// TestAdapterFindsTheProjectTheWayItsCallerCan covers the other half of the same
// asymmetry, and the failure it replaced. This host substitutes the project
// variable into hook arguments and does not export it, so a command the model
// runs reads it as empty in every session -- and resolving identity from it
// there answered "nothing names you" for a directory whose identity file named
// exactly that agent. The two callers therefore read two sources: a hook is owed
// the variable and fails without it, and a model-run identity walks up from its
// own working directory.
//
// The subdirectory is the point of the walk, not decoration: an agent works from
// wherever its task lives while the identity file sits at the project root.
func TestAdapterFindsTheProjectTheWayItsCallerCan(t *testing.T) {
	root := filepath.Join(t.TempDir(), "runtime-state")
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, claude.IdentityFile), []byte("a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	working := filepath.Join(project, "internal", "feature")
	if err := os.MkdirAll(working, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, stderr, err := runCLIHelper(os.Args[0], root, "", "a", "join", "dev"); err != nil {
		t.Fatalf("join a: %v: %s", err, stderr)
	}

	report := readIdentity(t, root, "", working, "session-1")
	if report.Source != string(claude.FromProject) || report.Agent != "a" {
		t.Fatalf("identity from a subdirectory of the project reported %+v; want the project's own agent", report)
	}
	if report.Project != project || report.Root == "" {
		t.Fatalf("identity reported %+v; want the directory it found and the root it read", report)
	}

	// Nothing above a temporary directory holds an identity file, so this is
	// the answer "nothing names you" -- reported, now, over a stated search.
	if report = readIdentity(t, root, "", t.TempDir(), "session-1"); report.Source != string(claude.NoBinding) || report.Project != "" {
		t.Fatalf("identity outside any project reported %+v; want unbound over an empty project", report)
	}

	for _, command := range []string{"arm", "release", "identity"} {
		if _, stderr, err := runAdapterHelper(t, root, "", "session-1", "adapter", "claude", command); err == nil {
			t.Fatalf("hook %s ran without the project directory the host owes it; stderr = %s", command, stderr)
		}
	}
}

// TestAdapterIdentityKeepsTheProjectsNameAfterABindOverIt covers the act the
// report exists to make visible. Binding displaces the project's identity for
// the session, and reporting only the winner would hide exactly the case worth
// seeing: an agent that bound over the name its project had already assigned,
// with nothing left in the answer to show it did.
func TestAdapterIdentityKeepsTheProjectsNameAfterABindOverIt(t *testing.T) {
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
	serveSession(t, root, project, "session-1")
	if _, stderr, err := runAdapterProcess(t, root, project, "session-1", "adapter", "claude", "bind", "b"); err != nil {
		t.Fatalf("bind: %v: %s", err, stderr)
	}
	report := readIdentity(t, root, "", project, "session-1")
	if report.Source != string(claude.FromSession) || report.Agent != "b" {
		t.Fatalf("bound session reported %+v; want the session's own identity", report)
	}
	if report.ProjectAgent != "a" {
		t.Fatalf("bound session reported project_agent %q; want the name it bound over", report.ProjectAgent)
	}
}

// readIdentity runs identity the way the model does -- no session argument, and
// the project reached through the working directory rather than the environment.
func readIdentity(t *testing.T, root, project, working, session string) identityReport {
	t.Helper()
	stdout, stderr, err := runAdapterFrom(t, root, project, working, session, "adapter", "claude", "identity")
	if err != nil {
		t.Fatalf("identity from %s: %v: %s", working, err, stderr)
	}
	var report identityReport
	if err := json.Unmarshal(stdout, &report); err != nil {
		t.Fatalf("identity report = %s: %v", stdout, err)
	}
	return report
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
	if report.Agent != agent || report.Source != source || report.Session != session {
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

// serveSession runs the SessionStart hook, which is what makes a session one
// this adapter serves. Every test that binds needs it for the same reason a
// real session does: the binding is only worth writing where a hook will read
// it, and the hook running is the only evidence there is one.
func serveSession(t *testing.T, root, project, session string) {
	t.Helper()
	if _, stderr, err := runAdapterHelper(t, root, project, session, "adapter", "claude", "identity"); err != nil {
		t.Fatalf("identity hook for %s: %v: %s", session, err, stderr)
	}
}

// runAdapterProcess spawns the adapter with the project supplied both ways at
// once -- in the environment, where a hook reads it, and as the working
// directory, where a model-run command finds it. That is the real arrangement:
// the two callers read two sources and the sources agree. A test that needs
// them to disagree, or needs one of them absent, passes "" and sets what it
// means explicitly; the working directory then falls to a temporary one so no
// identity file above the checkout can answer for it.
func runAdapterProcess(t *testing.T, root, project, session string, args ...string) ([]byte, []byte, error) {
	t.Helper()
	working := project
	if working == "" {
		working = t.TempDir()
	}
	return runAdapterFrom(t, root, project, working, session, args...)
}

// runAdapterFrom is the general form, and the two directories are separate
// arguments because the callers read them separately: project goes to the
// environment a hook is given, working is where the process runs and so what a
// model-run command resolves from. Tests that need the two to disagree, or one
// to be absent, are testing the thing this asymmetry exists for.
func runAdapterFrom(t *testing.T, root, project, working, session string, args ...string) ([]byte, []byte, error) {
	t.Helper()
	command := exec.Command(os.Args[0], args...)
	command.Dir = working
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
