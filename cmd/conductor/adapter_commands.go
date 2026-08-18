package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	conductor "github.com/danlavee/Conductor"
	"github.com/danlavee/Conductor/internal/adapters/claude"
)

// wakeSignal is not a failure. On this host a delivery reaches the model by
// the hook process exiting with a particular code, so the exit status is the
// payload's envelope and has to survive all the way out of run().
type wakeSignal struct{ Code int }

func (w *wakeSignal) Error() string { return fmt.Sprintf("wake exit %d", w.Code) }

// adapterReport is diagnostic output, and goes to stderr for that reason:
// stdout belongs to the model on a waking exit, and anything else written
// there would arrive as if it were delivered work.
type adapterReport struct {
	Adapter string `json:"adapter"`
	Command string `json:"command"`
	Outcome string `json:"outcome"`
	Agent   string `json:"agent,omitempty"`
	Detail  string `json:"detail,omitempty"`
}

func runAdapterCommand(args []string) error {
	if len(args) != 2 || args[0] != "claude" {
		return adapterUsageError()
	}
	command := args[1]
	if command != "arm" && command != "release" && command != "identity" {
		return adapterUsageError()
	}
	agent, err := claude.ResolveIdentity(os.Getenv(claude.ProjectEnvironment))
	if err != nil {
		return err
	}
	if agent == "" {
		return reportAdapter(command, "unbound", "", nil)
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
			_ = reportAdapter("arm", string(outcome), agent, err)
		}
		return &wakeSignal{Code: claude.WakeExitCode}
	}
	if err != nil {
		return err
	}
	return reportAdapter("arm", string(outcome), agent, nil)
}

func releaseOutcome(released bool) string {
	if released {
		return "released"
	}
	return "not-owned"
}

func reportAdapter(command, outcome, agent string, detail error) error {
	report := adapterReport{Adapter: "claude", Command: command, Outcome: outcome, Agent: agent}
	if detail != nil {
		report.Detail = detail.Error()
	}
	return conductor.WriteJSON(os.Stderr, report)
}

func adapterUsageError() error {
	return errors.New("usage: conductor adapter claude <arm|release|identity>")
}
