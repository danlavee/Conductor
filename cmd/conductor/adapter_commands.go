package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	adapterbundle "github.com/danlavee/Conductor/adapters"
	"github.com/danlavee/Conductor/internal/adapters/binding"
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
// serves: who this session acts as, if anyone yet, and how that is decided.
type identityReport struct {
	Agent  string `json:"agent,omitempty"`
	Advice string `json:"advice"`
}

const identityAdvice = "Conductor's Claude Code adapter serves this session. Joining binds the session to the name you join as, " +
	"and the adapter keeps that name wakeable until the session ends; a join under another name rebinds it."

// sessionFlag carries the host's substituted session identifier. A flag rather
// than a positional, so a lost substitution is a usage error, not a value.
const sessionFlag = "--session="

// runAdapterCommand routes a host's hook to that host. Each host owns its own
// usage, so the verbs only hooks run never reach the surface agents read.
func runAdapterCommand(args []string) error {
	if len(args) == 0 {
		return adapterHostsUsageError()
	}
	switch args[0] {
	case "claude":
		return runClaudeAdapterCommand(args[1:])
	case "grok":
		return runGrokAdapterCommand(args[1:])
	case "agy":
		return runAGYAdapterCommand(args[1:])
	}
	return adapterHostsUsageError()
}

func runClaudeAdapterCommand(args []string) error {
	if len(args) == 0 {
		return adapterUsageError()
	}
	command, rest := args[0], args[1:]
	switch command {
	case "install":
		return runAdapterInstall(rest)
	case "arm", "release", "identity":
	default:
		return adapterUsageError()
	}
	session, err := hookSession(rest)
	if err != nil {
		return err
	}
	root, err := conductor.Root(os.Getenv("CONDUCTOR_HOME"))
	if err != nil {
		return err
	}
	if command == "identity" {
		if err := binding.Serve(root, claude.BindingName, session); err != nil {
			return err
		}
	}
	agent, err := binding.Identity(root, claude.BindingName, session)
	if err != nil {
		return err
	}
	if command == "identity" {
		return conductor.WriteJSON(os.Stdout, identityReport{Agent: agent, Advice: identityAdvice})
	}
	if agent == "" {
		return reportAdapter(command, claude.Unbound, "", nil)
	}
	client, err := conductor.Open(root, agent)
	if err != nil {
		return err
	}
	if command == "arm" {
		return runAdapterArm(client, session, agent)
	}
	return runAdapterRelease(client, root, session, agent)
}

// hookSession takes the session identifier from the substituted argument. A
// missing one fails loudly: every session would otherwise scope teardown to
// the same empty identifier and tear down each other's streams.
func hookSession(args []string) (string, error) {
	if len(args) != 1 || !strings.HasPrefix(args[0], sessionFlag) {
		return "", adapterUsageError()
	}
	session := strings.TrimPrefix(args[0], sessionFlag)
	if err := binding.Validate(session, claude.SessionPlaceholder); err != nil {
		return "", fmt.Errorf("%s: %w", sessionFlag, err)
	}
	return session, nil
}

// runAdapterRelease ends the stream and forgets the session's binding. Both
// failures are joined so a leftover binding cannot hide behind a release
// failure.
func runAdapterRelease(client *conductor.Client, root, session, agent string) error {
	released, releaseErr := claude.Release(client, session)
	if err := errors.Join(releaseErr, binding.Forget(root, claude.BindingName, session)); err != nil {
		return err
	}
	outcome := claude.NotOwned
	if released {
		outcome = claude.Released
	}
	return reportAdapter("release", outcome, agent, nil)
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

func adapterHostsUsageError() error {
	return errors.New("usage: conductor adapter <claude|grok|agy> <command>")
}

func adapterUsageError() error {
	return errors.New("usage: conductor adapter claude <arm|release|identity> --session=<id> | install <dir>")
}

func adapterInstallUsageError() error {
	return errors.New("usage: conductor adapter claude install <absolute-adapter-directory> [--replace]")
}
