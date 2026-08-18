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

// wakeSignal is not a failure. On this host a delivery reaches the model by
// the hook process exiting with a particular code, so the exit status is the
// payload's envelope and has to survive all the way out of run().
type wakeSignal struct{ Code int }

func (w *wakeSignal) Error() string { return fmt.Sprintf("wake exit %d", w.Code) }

// adapterReport is diagnostic output, and goes to stderr for that reason:
// stdout belongs to the model on a waking exit, and anything else written
// there would arrive as if it were delivered work.
//
// Code is the outcome's number, carried here rather than in the process's exit
// status because the exit status is spoken for: this host reads anything but 0
// and claude.WakeExitCode as a hook failure worth showing the user, and most
// outcomes are ordinary no-ops that must not look like one.
type adapterReport struct {
	Adapter string `json:"adapter"`
	Command string `json:"command"`
	Outcome string `json:"outcome"`
	Code    int    `json:"code"`
	Agent   string `json:"agent,omitempty"`
	Detail  string `json:"detail,omitempty"`
}

// sessionFlag is how the host's substituted session identifier arrives. A flag
// rather than a bare positional, so a hook argument list that lost its
// substitution is a usage error instead of a value.
const sessionFlag = "--session="

// identityReport and bindResult go to stdout, unlike adapterReport. Both are
// answers rather than diagnostics: identity runs as a blocking SessionStart
// hook whose stdout enters the session's context, and bind is run by the model,
// which reads the command's own output.
type identityReport struct {
	Adapter string                `json:"adapter"`
	Source  string                `json:"source"`
	Session string                `json:"session"`
	Status  conductor.WatchStatus `json:"status"`
}

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
	// The two commands that are not hooks are dispatched first, because neither
	// can answer the question the rest start from. Placement runs before there
	// is an installation to bind an identity to, and binding is run by the model
	// rather than by the host, so it learns its session a different way.
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
	binding, err := claude.ResolveBinding(root, session, project)
	if err != nil {
		return err
	}
	// identity answers before the unbound short-circuit, because for identity
	// unbound is the answer rather than a reason to do nothing -- and it is the
	// one answer whose remedy the caller can act on alone.
	if command == "identity" {
		return runAdapterIdentity(root, session, binding)
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
// it is attached to, from whichever mechanisms its caller actually has. The
// discriminator is how the command was invoked, not which command it is: a hook
// is handed its session as a substituted argument and its project in the
// environment, and a process the model started inherits the session from the
// environment and may have no project at all.
//
// So the host contract is enforced on the hook path only, where the host owes
// both values and a missing one means it is misconfigured. Only argument-free
// identity is exempt, and only it can be: it is the one invocation the model
// makes itself, to find out why nothing can reach it. Demanding the project
// directory there would fail the diagnostic in the degraded state it exists to
// diagnose -- which is what an agent with no project binding is. It reports and
// changes nothing, so an absent project is an answer it can give.
//
// arm and release keep both as hard requirements. They change what a session
// owns, and a teardown that ran against a session identifier it merely guessed
// at is the failure that made the identifier a requirement in the first place.
func adapterContext(command string, args []string) (string, string, error) {
	project := os.Getenv(claude.ProjectEnvironment)
	if len(args) == 0 && command == "identity" {
		session := os.Getenv(claude.SessionEnvironment)
		if err := claude.ValidateSession(session); err != nil {
			return "", "", fmt.Errorf("%s: %w", claude.SessionEnvironment, err)
		}
		return session, project, nil
	}
	session, err := hookSession(args)
	if err != nil {
		return "", "", err
	}
	if strings.TrimSpace(project) == "" {
		return "", "", errors.New(claude.ProjectEnvironment + " is unset, so this host did not export it to the hook")
	}
	return session, project, nil
}

// hookSession takes the session identifier from the argument the host
// substituted, and refuses to run without a usable one.
//
// It is an argument rather than an environment variable because that is the
// only one of the two a hook actually gets: the host substitutes
// ${CLAUDE_SESSION_ID} into a hook's argument list, and exports
// CLAUDE_CODE_SESSION_ID to processes the model starts. Reading the variable
// from a hook -- which is what this adapter did until now -- yields an empty
// string on every host, in every session, silently, and every session then
// scopes its teardown to the same empty identifier: one session ending tears
// down the stream another session is holding.
//
// A missing or unsubstituted identifier is therefore a hard error rather than
// a degraded mode. It exits non-zero, which this host shows to the user, and
// that is the intent: an adapter that cannot tell one session from another is
// misconfigured, and the failure that taught us this was silent.
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

// runAdapterRelease ends the stream and forgets the binding, in that order and
// unconditionally. Teardown is the only event that says a session is gone, so
// it is the only chance to clear what was recorded about it -- including for a
// session that armed nothing, which still may have bound an identity.
// Both failures are reported together: a binding left behind by a failed
// unbind outlives the session and answers for whatever session identifier the
// host issues next, so it must not be hidden behind a release failure.
func runAdapterRelease(client *conductor.Client, root, session, agent string) error {
	released, releaseErr := claude.Release(client, session)
	if err := errors.Join(releaseErr, claude.UnbindSession(root, session)); err != nil {
		return err
	}
	return reportAdapter("release", releaseOutcome(released), agent, nil)
}

// runAdapterIdentity announces the binding into the session's context. It
// reports which of the two bindings answered, because an agent that finds
// itself acting as the wrong identity otherwise has no way to know which file
// to correct -- and with two agents in one directory, the wrong one is the
// failure that looks most like everything working.
// It is also the only command that can say why an identity is not wakeable,
// which is why an agent runs it. `status` reports that nothing holds the
// identity; it cannot report whether that is because no binding names this
// session, or because a binding does and the adapter is not running. Source and
// the holding PID separate those, and they are different problems with
// different fixes.
func runAdapterIdentity(root, session string, binding claude.Binding) error {
	report := identityReport{
		Adapter: "claude", Session: session, Source: string(binding.Source),
	}
	if binding.Agent == "" {
		report.Source = string(claude.NoBinding)
		return conductor.WriteJSON(os.Stdout, report)
	}
	client, err := conductor.Open(root, binding.Agent)
	if err != nil {
		return err
	}
	if report.Status, err = client.WatchStatus(); err != nil {
		return err
	}
	return conductor.WriteJSON(os.Stdout, report)
}

// runAdapterBind records that this session acts as agent. It is the one adapter
// command the model runs itself, and it exists for the case the project file
// cannot express: several agents working in one directory, where a per-project
// binding can only name one of them.
//
// The identity must already be on the roster. That is a real gate, not
// ceremony: `.conductor-agent` is written by hand before an agent joins and so
// cannot require registration, but bind is run by an agent that has already
// joined, and a name that is not on the roster at this point is a typo -- one
// that would otherwise produce a quiet unregistered no-op at every turn end
// for the life of the session.
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
// written for the model. Every other outcome exits cleanly: a restore attempt
// that finds the identity already held, unregistered, or torn down has done its
// job by declining, and reporting it as a failure would make the blind re-arm
// the design depends on look broken every time it worked.
//
// The outcome decides the wake before the error does, and deliberately so. Once
// the payload is on stdout the session must be woken to read it; a failure
// after that point -- an acknowledgement that did not land, say -- would
// otherwise convert a completed delivery into a silent exit 1, which is the
// precise failure this whole design exists to remove. Conductor redelivers what
// was never acknowledged, so waking anyway costs a duplicate while not waking
// costs the turn.
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
// with the same staging, hashing and atomic publication the skill gets. It
// stops there, and the boundary is deliberate: the host discovers plugins
// through private state of its own -- an install registry and a marketplace
// index this command has no documented contract with -- so pointing the host
// at what was placed stays a host command the user runs. Guessing at that
// state would make Conductor's correctness depend on a format nobody promised
// to keep.
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
