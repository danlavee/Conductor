// Package cliresume is the engine behind process-per-signal CLI adapters:
// an agent loop with no external wake path, so each Conductor signal must
// launch a fresh, non-interactive resume of it.
package cliresume

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	conductor "github.com/danlavee/Conductor"
	"github.com/danlavee/Conductor/internal/integrations/execlocate"
)

// Transport is one runtime's process-per-signal resume mechanics.
type Transport struct {
	// RuntimeLabel names the runtime in errors and prompts, e.g. "Claude Code CLI".
	RuntimeLabel string
	// TargetNoun names what the target ID identifies, e.g. "session" or "conversation".
	TargetNoun string
	// DefaultExecutable is looked up on PATH when no executable is given.
	DefaultExecutable string
	// CandidateExecutables are known per-OS install locations checked, in
	// order, when DefaultExecutable is not found on PATH. May be nil.
	CandidateExecutables []string
	// DeliveryEnvironment is set to "1" in the resumed process, marking adapter-owned delivery.
	DeliveryEnvironment string
	// ResumeArguments builds the resume command's arguments for one target and prompt.
	ResumeArguments func(targetID, prompt string) []string
	// ValidateOutput rejects a resume that produced no usable output.
	ValidateOutput func(output []byte) error
	// ResumeFailureHint optionally appends runtime-specific guidance to a failed resume.
	ResumeFailureHint func(targetID, agent string) string
}

// Validate checks the explicit target ID and agent name given on the command line.
func (t Transport) Validate(targetID, agent string) error {
	if strings.TrimSpace(targetID) == "" {
		return fmt.Errorf("%s watch requires a %s ID", t.RuntimeLabel, t.TargetNoun)
	}
	if strings.TrimSpace(agent) == "" {
		return fmt.Errorf("%s watch requires an agent name", t.RuntimeLabel)
	}
	return nil
}

type WatchClient interface {
	WatchResultContext(context.Context) (conductor.WatchResult, error)
	ResolveDelivery(conductor.Summary, conductor.DeliveryMode) (conductor.Delivery, error)
	AcknowledgeDelivery(conductor.Delivery) error
}

type Activator interface {
	Activate(context.Context, string, string, conductor.Delivery) error
	ActivateReplacement(context.Context, string, string, conductor.ReplacementActivation) error
}

// CLI activates a Transport's runtime by spawning one resume process per
// signal. Fields are exported so a Transport-specific package (or its tests)
// can construct one directly, e.g. to inject a fake Command for testing.
type CLI struct {
	Transport  Transport
	Executable string
	Stdout     io.Writer
	Stderr     io.Writer
	Command    func(context.Context, string, ...string) *exec.Cmd
}

func New(transport Transport, executable string, stdout, stderr io.Writer) (*CLI, error) {
	if strings.TrimSpace(executable) == "" {
		var err error
		executable, err = execlocate.Find(transport.RuntimeLabel, transport.DefaultExecutable, transport.CandidateExecutables)
		if err != nil {
			return nil, err
		}
	}
	return &CLI{Transport: transport, Executable: executable, Stdout: stdout, Stderr: stderr, Command: exec.CommandContext}, nil
}

func (a *CLI) Check(ctx context.Context) error {
	command := a.Command(ctx, a.Executable, "--version")
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("run %s %q: %w%s", a.Transport.RuntimeLabel, a.Executable, err, outputSuffix(output))
	}
	return nil
}

func (a *CLI) Activate(ctx context.Context, targetID, agent string, delivery conductor.Delivery) error {
	prompt, err := SignalPrompt(a.Transport.RuntimeLabel, agent, delivery)
	if err != nil {
		return err
	}
	return a.activatePrompt(ctx, targetID, agent, prompt)
}

func (a *CLI) ActivateReplacement(ctx context.Context, targetID, agent string, activation conductor.ReplacementActivation) error {
	payload, err := json.Marshal(activation)
	if err != nil {
		return err
	}
	prompt := fmt.Sprintf("Conductor was replaced during cutover. Control activation: %s. This activation contains no topic delta and must not be acknowledged. Reload the installed Conductor skill and executable, then re-arm exactly one watch for agent %q. Do not read the old protocol root.", payload, agent)
	return a.activatePrompt(ctx, targetID, agent, prompt)
}

func (a *CLI) activatePrompt(ctx context.Context, targetID, agent, prompt string) error {
	command := a.Command(ctx, a.Executable, a.Transport.ResumeArguments(targetID, prompt)...)
	command.Env = setEnvironment(os.Environ(), map[string]string{
		a.Transport.DeliveryEnvironment: "1",
	})
	command.Stderr = a.Stderr
	var output bytes.Buffer
	command.Stdout = io.MultiWriter(&output, writerOrDiscard(a.Stdout))
	if err := command.Run(); err != nil {
		return fmt.Errorf("resume %s %s %s: %w%s", a.Transport.RuntimeLabel, a.Transport.TargetNoun, targetID, err, a.hint(targetID, agent))
	}
	if err := a.Transport.ValidateOutput(output.Bytes()); err != nil {
		return fmt.Errorf("resume %s %s %s: %w", a.Transport.RuntimeLabel, a.Transport.TargetNoun, targetID, err)
	}
	return nil
}

func (a *CLI) hint(targetID, agent string) string {
	if a.Transport.ResumeFailureHint == nil {
		return ""
	}
	return a.Transport.ResumeFailureHint(targetID, agent)
}

// Run owns the wait loop. Replacement is a control-only activation and exits
// the old adapter without resolution or acknowledgment.
func Run(ctx context.Context, transport Transport, client WatchClient, activator Activator, targetID, agent string, mode conductor.DeliveryMode) error {
	if err := transport.Validate(targetID, agent); err != nil {
		return err
	}
	for {
		result, err := client.WatchResultContext(ctx)
		if err != nil {
			return err
		}
		if result.Activation != nil {
			defer result.Close()
			if err := activator.ActivateReplacement(ctx, targetID, agent, *result.Activation); err != nil {
				return fmt.Errorf("deliver Conductor replacement to %s: %w", transport.RuntimeLabel, err)
			}
			return nil
		}
		for _, summary := range result.Summaries {
			delivery, err := client.ResolveDelivery(summary, mode)
			if err != nil {
				_ = result.Close()
				return fmt.Errorf("resolve Conductor summary %d for %s: %w", summary.Sequence, transport.RuntimeLabel, err)
			}
			if err := activator.Activate(ctx, targetID, agent, delivery); err != nil {
				_ = result.Close()
				return fmt.Errorf("deliver Conductor summary %d to %s: %w", summary.Sequence, transport.RuntimeLabel, err)
			}
			if err := client.AcknowledgeDelivery(delivery); err != nil {
				_ = result.Close()
				return fmt.Errorf("acknowledge Conductor summary %d after %s delivery: %w", summary.Sequence, transport.RuntimeLabel, err)
			}
		}
		if err := result.Close(); err != nil {
			return err
		}
	}
}

// SignalPrompt builds the resumed turn's prompt. In payload mode the signal's
// data is already resolved, so the resumed turn must not re-fetch it.
func SignalPrompt(runtimeLabel, agent string, delivery conductor.Delivery) (string, error) {
	payload, err := json.Marshal(delivery)
	if err != nil {
		return "", err
	}
	if delivery.Mode == conductor.DeliveryContent {
		return fmt.Sprintf("Conductor activated this %s turn for agent %q with a resolved delivery: %s. Its content is already included (a topic delta for an update, the roster for a join or leave). Do not call get, list-agents, or watch for it; act on the included data directly. The adapter will acknowledge the summary and any associated topic cursor after this turn succeeds, then re-arm its own wait loop. Process idempotently and report the result.", runtimeLabel, agent, payload), nil
	}
	return fmt.Sprintf("Conductor activated this %s turn for agent %q with summary %s. Use the installed Conductor skill to process it. For an update, read the named topic; for a join or leave, refresh the roster. The adapter already owns the wait loop, so do not start conductor watch. Process this summary idempotently and report the result.", runtimeLabel, agent, payload), nil
}

func JSONOutputValidator() func([]byte) error {
	return func(output []byte) error {
		if !json.Valid(bytes.TrimSpace(output)) {
			return errors.New("CLI exited without valid JSON output")
		}
		return nil
	}
}

func NonEmptyOutputValidator() func([]byte) error {
	return func(output []byte) error {
		if len(bytes.TrimSpace(output)) == 0 {
			return errors.New("CLI exited without output")
		}
		return nil
	}
}

func writerOrDiscard(writer io.Writer) io.Writer {
	if writer == nil {
		return io.Discard
	}
	return writer
}

func outputSuffix(output []byte) string {
	trimmed := strings.TrimSpace(string(output))
	if trimmed == "" {
		return ""
	}
	return ": " + trimmed
}

func setEnvironment(environment []string, values map[string]string) []string {
	result := make([]string, 0, len(environment)+len(values))
	for _, entry := range environment {
		key, _, ok := strings.Cut(entry, "=")
		if ok {
			if _, replace := values[strings.ToUpper(key)]; replace {
				continue
			}
		}
		result = append(result, entry)
	}
	for key, value := range values {
		result = append(result, key+"="+value)
	}
	return result
}
