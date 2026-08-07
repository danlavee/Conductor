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

func runAdapterCommand(args []string) error {
	if len(args) < 2 || args[0] != "claude" {
		return adapterUsageError()
	}
	command := args[1]
	// Placement is answered before identity is, because it is the only adapter
	// command that runs before there is an installation to bind an identity to.
	if command == "install" {
		return runAdapterInstall(args[2:])
	}
	if len(args) != 2 || (command != "arm" && command != "release" && command != "identity") {
		return adapterUsageError()
	}
	agent, err := claude.ResolveIdentity(os.Getenv(claude.ProjectEnvironment))
	if err != nil {
		return err
	}
	if agent == "" {
		return reportAdapter(command, claude.Unbound, "", nil)
	}
	client, err := conductor.Open(os.Getenv("CONDUCTOR_HOME"), agent)
	if err != nil {
		return err
	}
	session := os.Getenv(claude.SessionEnvironment)
	switch command {
	case "arm":
		return runAdapterArm(client, session, agent)
	case "release":
		released, err := claude.Release(client, session)
		if err != nil {
			return err
		}
		return reportAdapter(command, releaseOutcome(released), agent, nil)
	default:
		status, err := client.WatchStatus()
		if err != nil {
			return err
		}
		return conductor.WriteJSON(os.Stdout, status)
	}
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
	return errors.New("usage: conductor adapter claude <arm|release|identity|install>")
}

func adapterInstallUsageError() error {
	return errors.New("usage: conductor adapter claude install <absolute-adapter-directory>")
}
