package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	adapterbundle "github.com/danlavee/Conductor/adapters"
	"github.com/danlavee/Conductor/internal/adapters/claude"
	"github.com/danlavee/Conductor/internal/install"
	conductor "github.com/danlavee/Conductor/internal/state"
)

// wakeSignal is a successful wake, carried as an error only so the exit status
// survives out of run().
type wakeSignal struct{ Code int }

func (w *wakeSignal) Error() string { return fmt.Sprintf("wake exit %d", w.Code) }

// adapterReport is diagnostic output, written to stderr because stdout belongs
// to the model on a waking exit.
type adapterReport struct {
	Command string `json:"command"`
	Outcome string `json:"outcome"`
	Agent   string `json:"agent,omitempty"`
	Detail  string `json:"detail,omitempty"`
}

// identityReport is the SessionStart hook's announcement into the session it
// serves: who this session acts as, which binding said so, and how to change
// it. This is where host-specific identity knowledge reaches an agent, since
// the shared skill cannot carry facts about one host.
type identityReport struct {
	claude.Binding
	Advice string `json:"advice"`
}

// identityAdvice is this host's remedy for a wrong identity. Constant, because
// only the reader knows whether the identity is wrong.
const identityAdvice = "If this is not the identity you were told to act as, run: conductor adapter claude bind <your-agent-name>. " +
	"It binds this session alone, outranks the project's .conductor-agent, and is forgotten when this session ends. " +
	"Never bind an agent that is not you."

// sessionFlag carries the host's substituted session identifier. A flag rather
// than a positional, so a lost substitution is a usage error, not a value.
const sessionFlag = "--session="

func runAdapterCommand(args []string) error {
	if len(args) >= 1 && args[0] == "grok" {
		return runGrokAdapterCommand(args[1:])
	}
	if len(args) >= 1 && args[0] == "agy" {
		return runAGYAdapterCommand(args[1:])
	}
	if len(args) < 2 || args[0] != "claude" {
		return adapterUsageError()
	}
	command, rest := args[1], args[2:]
	switch command {
	case "install":
		return runAdapterInstall(rest)
	case "bind":
		return runAdapterBind(rest)
	case "arm", "release", "identity":
	default:
		return adapterUsageError()
	}
	session, err := hookSession(rest)
	if err != nil {
		return err
	}
	// Hooks are handed the project; their working directory is nothing in
	// particular, so there is nothing to fall back to.
	project := os.Getenv(claude.ProjectEnvironment)
	if strings.TrimSpace(project) == "" {
		return errors.New(claude.ProjectEnvironment + " is unset, so this host did not export it to the hook")
	}
	root, err := conductor.Root(os.Getenv("CONDUCTOR_HOME"))
	if err != nil {
		return err
	}
	binding, err := claude.ResolveBinding(root, session, project)
	if err != nil {
		return err
	}
	if command == "identity" {
		return conductor.WriteJSON(os.Stdout, identityReport{Binding: binding, Advice: identityAdvice})
	}
	if binding.Agent == "" {
		return reportAdapter(command, claude.Unbound, "", nil)
	}
	client, err := conductor.Open(root, binding.Agent)
	if err != nil {
		return err
	}
	if command == "arm" {
		return runAdapterArm(client, session, binding.Agent)
	}
	return runAdapterRelease(client, root, session, binding.Agent)
}

// hookSession takes the session identifier from the substituted argument. A
// missing one fails loudly: every session would otherwise scope teardown to
// the same empty identifier and tear down each other's streams.
func hookSession(args []string) (string, error) {
	if len(args) != 1 || !strings.HasPrefix(args[0], sessionFlag) {
		return "", adapterUsageError()
	}
	session := strings.TrimPrefix(args[0], sessionFlag)
	if err := claude.ValidateSession(session); err != nil {
		return "", fmt.Errorf("%s: %w", sessionFlag, err)
	}
	return session, nil
}

// runAdapterRelease ends the stream and forgets the session's binding. Both
// failures are joined so a leftover binding cannot hide behind a release
// failure.
func runAdapterRelease(client *conductor.Client, root, session, agent string) error {
	released, releaseErr := claude.Release(client, session)
	if err := errors.Join(releaseErr, claude.ForgetSession(root, session)); err != nil {
		return err
	}
	outcome := claude.NotOwned
	if released {
		outcome = claude.Released
	}
	return reportAdapter("release", outcome, agent, nil)
}

// runAdapterBind records that this session acts as agent, for the case the
// project file cannot express: several agents in one directory. An identity
// absent from the roster is a typo, refused rather than bound into a session
// that would then hold a stream for nobody.
func runAdapterBind(args []string) error {
	if len(args) != 1 || strings.TrimSpace(args[0]) == "" {
		return adapterBindUsageError()
	}
	agent := strings.TrimSpace(args[0])
	session := os.Getenv(claude.SessionEnvironment)
	if err := claude.ValidateSession(session); err != nil {
		return fmt.Errorf("%s: %w", claude.SessionEnvironment, err)
	}
	root, err := conductor.Root(os.Getenv("CONDUCTOR_HOME"))
	if err != nil {
		return err
	}
	client, err := conductor.Open(root, agent)
	if err != nil {
		return err
	}
	status, err := client.WatchStatus()
	if err != nil {
		return err
	}
	if !status.Registered {
		return errors.New("cannot bind to " + agent + ": it is not on the roster, so join first")
	}
	if err := claude.BindSession(root, session, agent); err != nil {
		return err
	}
	return conductor.WriteJSON(os.Stdout, claude.Binding{Agent: agent, Source: claude.FromSession})
}

// runAdapterArm exits with the host's wake code exactly when something was
// written for the model, and cleanly otherwise: a restore attempt that declines
// has done its job, and reporting that as a failure would make the blind re-arm
// look broken every time it worked.
//
// The outcome decides the wake before the error does. Once the payload is on
// stdout, a later failure must not turn a completed delivery into a silent exit
// 1 -- waking anyway costs a duplicate, not waking costs the turn.
func runAdapterArm(client *conductor.Client, session, agent string) error {
	outcome, err := claude.Arm(context.Background(), client, session, os.Stdout)
	if outcome == claude.Delivered {
		if err != nil {
			_ = reportAdapter("arm", outcome, agent, err)
		}
		return &wakeSignal{Code: claude.WakeExitCode}
	}
	if err != nil {
		return err
	}
	return reportAdapter("arm", outcome, agent, nil)
}

// runAdapterInstall places the plugin tree and the executable its hooks name,
// and stops there. Enabling stays a host command the user runs, because the
// host's plugin registry is private state with no documented contract.
func runAdapterInstall(args []string) error {
	if len(args) < 1 || strings.TrimSpace(args[0]) == "" {
		return adapterInstallUsageError()
	}
	replace, err := replaceRequested(args[1:])
	if err != nil {
		return err
	}
	// The hooks resolve ${CLAUDE_PLUGIN_ROOT}/bin/conductor, which fixes where
	// the executable goes; the directory above adapters/ is the user's choice.
	payload := install.Payload{Bundle: adapterbundle.ClaudeCode, Parent: "adapters", Name: claude.AdapterName, ExecutableDir: "bin"}
	return runInstall(payload, args[0], replace)
}

func reportAdapter(command string, outcome claude.Outcome, agent string, detail error) error {
	report := adapterReport{Command: command, Outcome: string(outcome), Agent: agent}
	if detail != nil {
		report.Detail = detail.Error()
	}
	return conductor.WriteJSON(os.Stderr, report)
}

func adapterUsageError() error {
	return errors.New("usage: conductor adapter claude <arm|release|identity> --session=<id> | bind <agent> | install <dir>")
}

func adapterInstallUsageError() error {
	return errors.New("usage: conductor adapter claude install <absolute-adapter-directory> [--replace]")
}

func adapterBindUsageError() error {
	return errors.New("usage: conductor adapter claude bind <agent>")
}
