package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	conductor "github.com/danlavee/Conductor"
	adapterbundle "github.com/danlavee/Conductor/adapters"
	"github.com/danlavee/Conductor/internal/adapters/claude"
	"github.com/danlavee/Conductor/internal/install"
)

// wakeSignal is a successful wake, carried as an error only so the exit status
// survives out of run().
type wakeSignal struct{ Code int }

func (w *wakeSignal) Error() string { return fmt.Sprintf("wake exit %d", w.Code) }

// adapterReport is diagnostic output, written to stderr because stdout belongs
// to the model on a waking exit.
type adapterReport struct {
	Adapter string `json:"adapter"`
	Command string `json:"command"`
	Outcome string `json:"outcome"`
	Code    int    `json:"code"`
	Agent   string `json:"agent,omitempty"`
	Detail  string `json:"detail,omitempty"`
}

// sessionFlag carries the host's substituted session identifier. A flag rather
// than a positional, so a lost substitution is a usage error, not a value.
const sessionFlag = "--session="

// identityReport is the adapter's announcement into the session it serves:
// who this session acts as, and every input that answer was resolved from, on
// every outcome. Written to stdout, which a SessionStart hook feeds into
// context. It reports no stream state -- an agent acts on none of it.
//
// This is where host-specific identity knowledge reaches an agent, since the
// shared skill cannot carry facts about one host.
type identityReport struct {
	Adapter      string `json:"adapter"`
	Agent        string `json:"agent"`
	Source       string `json:"source"`
	Session      string `json:"session"`
	Project      string `json:"project"`
	ProjectAgent string `json:"project_agent"`
	Root         string `json:"root"`
	Advice       string `json:"advice"`
}

// identityAdvice is this host's remedy for a wrong identity. Constant, because
// only the reader knows whether the identity is wrong.
const identityAdvice = "If this is not the identity you were told to act as, run: conductor adapter claude bind <your-agent-name>. " +
	"It binds this session alone, outranks the project's .conductor-agent, leaves that file and other sessions untouched, " +
	"and is forgotten when this session ends. Run it once, and never for an agent that is not you."

type bindResult struct {
	Adapter  string `json:"adapter"`
	Session  string `json:"session"`
	Agent    string `json:"agent"`
	Previous string `json:"previous,omitempty"`
}

func runAdapterCommand(args []string) error {
	if len(args) < 2 || args[0] != "claude" {
		return adapterUsageError()
	}
	command, rest := args[1], args[2:]
	// Neither of these is a hook: install runs before there is an installation
	// to bind an identity to, and bind is run by the model.
	switch command {
	case "install":
		return runAdapterInstall(rest)
	case "bind":
		return runAdapterBind(rest)
	}
	if command != "arm" && command != "release" && command != "identity" {
		return adapterUsageError()
	}
	session, project, err := adapterContext(command, rest)
	if err != nil {
		return err
	}
	root, err := conductor.Root(os.Getenv("CONDUCTOR_HOME"))
	if err != nil {
		return err
	}
	// identity answers before the unbound short-circuit, because there unbound
	// is the answer rather than a reason to do nothing, and it resolves both
	// bindings rather than the winner.
	if command == "identity" {
		resolution, err := claude.Resolve(root, session, project)
		if err != nil {
			return err
		}
		return runAdapterIdentity(root, session, project, resolution)
	}
	binding, err := claude.ResolveBinding(root, session, project)
	if err != nil {
		return err
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

// adapterContext resolves which session an invocation is in and which project
// it is attached to, from the one mechanism its own caller has: a hook is
// handed both, while a model-started process inherits the session and finds
// the project itself.
//
// Neither path falls back to the other. A caller that cannot reach its own
// source is misconfigured, not degraded, and the fallback answers "nothing
// names you" for a directory that does.
func adapterContext(command string, args []string) (string, string, error) {
	if len(args) == 0 && command == "identity" {
		session := os.Getenv(claude.SessionEnvironment)
		if err := claude.ValidateSession(session); err != nil {
			return "", "", fmt.Errorf("%s: %w", claude.SessionEnvironment, err)
		}
		working, err := os.Getwd()
		if err != nil {
			return "", "", err
		}
		project, err := claude.ProjectDirectory(working)
		if err != nil {
			return "", "", err
		}
		return session, project, nil
	}
	session, err := hookSession(args)
	if err != nil {
		return "", "", err
	}
	project := os.Getenv(claude.ProjectEnvironment)
	if strings.TrimSpace(project) == "" {
		return "", "", errors.New(claude.ProjectEnvironment + " is unset, so this host did not export it to the hook")
	}
	return session, project, nil
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

// runAdapterRelease ends the stream and forgets the binding, unconditionally:
// a session that armed nothing may still have bound an identity, and teardown
// is the only event that says a session is gone. Both failures are joined so a
// leftover binding cannot hide behind a release failure.
func runAdapterRelease(client *conductor.Client, root, session, agent string) error {
	released, releaseErr := claude.Release(client, session)
	if err := errors.Join(releaseErr, claude.UnbindSession(root, session)); err != nil {
		return err
	}
	return reportAdapter("release", releaseOutcome(released), agent, nil)
}

// runAdapterIdentity announces the binding into the session's context. Acting
// as the wrong identity is the failure that looks most like everything
// working, so which binding answered is part of the answer.
func runAdapterIdentity(root, session, project string, resolution claude.Resolution) error {
	binding := resolution.Binding()
	source := binding.Source
	if binding.Agent == "" {
		source = claude.NoBinding
	}
	return conductor.WriteJSON(os.Stdout, identityReport{
		Adapter: "claude", Agent: binding.Agent, Session: session, Source: string(source),
		Project: project, ProjectAgent: resolution.Project, Root: root, Advice: identityAdvice,
	})
}

// runAdapterBind records that this session acts as agent, for the case the
// project file cannot express: several agents in one directory.
//
// The identity must already be on the roster. Unlike the hand-written project
// file, bind is run by an agent that has joined, so a name absent from the
// roster is a typo -- and would be a quiet no-op for the session's whole life.
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
	previous, err := claude.BindSession(root, session, agent)
	if err != nil {
		return err
	}
	return conductor.WriteJSON(os.Stdout, bindResult{
		Adapter: "claude", Session: session, Agent: agent, Previous: previous,
	})
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
	if outcome.Wakes() {
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
	if len(args) != 1 || strings.TrimSpace(args[0]) == "" {
		return adapterInstallUsageError()
	}
	payload := install.AdapterPayload(claude.AdapterName)
	if err := install.ValidateDestination(args[0], payload); err != nil {
		return err
	}
	source, err := installationSource(payload, adapterbundle.ClaudeCode, true)
	if err != nil {
		return err
	}
	result, err := install.Install(args[0], source)
	if err != nil {
		return err
	}
	return conductor.WriteJSON(os.Stdout, result)
}

func releaseOutcome(released bool) claude.Outcome {
	if released {
		return claude.Released
	}
	return claude.NotOwned
}

func reportAdapter(command string, outcome claude.Outcome, agent string, detail error) error {
	report := adapterReport{
		Adapter: "claude", Command: command,
		Outcome: string(outcome), Code: outcome.Code(), Agent: agent,
	}
	if detail != nil {
		report.Detail = detail.Error()
	}
	return conductor.WriteJSON(os.Stderr, report)
}

func adapterUsageError() error {
	return errors.New("usage: conductor adapter claude <arm|release> --session=<id> | identity [--session=<id>] | bind <agent> | install <dir>")
}

func adapterInstallUsageError() error {
	return errors.New("usage: conductor adapter claude install <absolute-adapter-directory>")
}

func adapterBindUsageError() error {
	return errors.New("usage: conductor adapter claude bind <agent>")
}
