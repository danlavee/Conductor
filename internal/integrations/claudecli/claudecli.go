// Package claudecli delivers Conductor signals through non-interactive Claude Code turns.
package claudecli

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
)

const (
	BinaryEnvironment   = "CONDUCTOR_CLAUDE_BIN"
	SessionEnvironment  = "CLAUDE_SESSION_ID"
	DeliveryEnvironment = "CONDUCTOR_CLAUDE_DELIVERY"
)

type WatchClient interface {
	WatchContext(context.Context) (conductor.Signal, error)
	AcknowledgeSignal(conductor.Signal) error
}

type Environment struct {
	Executable string
	SessionID  string
}

func EnvironmentFrom(getenv func(string) string) Environment {
	return Environment{
		Executable: getenv(BinaryEnvironment),
		SessionID:  getenv(SessionEnvironment),
	}
}

func (e Environment) Validate(agent string) error {
	if strings.TrimSpace(e.SessionID) == "" {
		return fmt.Errorf("%s is required for Claude CLI watch", SessionEnvironment)
	}
	if strings.TrimSpace(agent) == "" {
		return errors.New("Claude CLI watch requires an agent name")
	}
	return nil
}

type Activator interface {
	Activate(context.Context, string, string, conductor.Signal) error
}

type CLI struct {
	executable string
	stdout     io.Writer
	stderr     io.Writer
}

func New(executable string, stdout, stderr io.Writer) (*CLI, error) {
	if strings.TrimSpace(executable) == "" {
		var err error
		executable, err = exec.LookPath("claude")
		if err != nil {
			return nil, fmt.Errorf("find Claude Code CLI: install it or set %s to its executable: %w", BinaryEnvironment, err)
		}
	}
	return &CLI{executable: executable, stdout: stdout, stderr: stderr}, nil
}

func (a *CLI) Check(ctx context.Context) error {
	command := exec.CommandContext(ctx, a.executable, "--version")
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("run Claude Code CLI %q: %w%s", a.executable, err, outputSuffix(output))
	}
	return nil
}

func (a *CLI) Activate(ctx context.Context, sessionID, agent string, signal conductor.Signal) error {
	prompt, err := SignalPrompt(agent, signal)
	if err != nil {
		return err
	}
	command := exec.CommandContext(ctx, a.executable, ResumeArguments(sessionID, prompt)...)
	command.Env = setEnvironment(os.Environ(), map[string]string{
		"CONDUCTOR_AGENT":   agent,
		DeliveryEnvironment: "1",
		SessionEnvironment:  sessionID,
	})
	command.Stderr = a.stderr
	var output bytes.Buffer
	command.Stdout = io.MultiWriter(&output, writerOrDiscard(a.stdout))
	if err := command.Run(); err != nil {
		return fmt.Errorf("resume Claude Code session %s: %w", sessionID, err)
	}
	if !json.Valid(bytes.TrimSpace(output.Bytes())) {
		return fmt.Errorf("resume Claude Code session %s: CLI exited without valid JSON output", sessionID)
	}
	return nil
}

func Run(ctx context.Context, client WatchClient, activator Activator, sessionID, agent string) error {
	if strings.TrimSpace(sessionID) == "" {
		return fmt.Errorf("%s is required for Claude CLI watch", SessionEnvironment)
	}
	if strings.TrimSpace(agent) == "" {
		return errors.New("Claude CLI watch requires an agent name")
	}
	for {
		signal, err := client.WatchContext(ctx)
		if err != nil {
			return err
		}
		if err := activator.Activate(ctx, sessionID, agent, signal); err != nil {
			return fmt.Errorf("deliver Conductor signal %d to Claude CLI: %w", signal.Index, err)
		}
		if err := client.AcknowledgeSignal(signal); err != nil {
			return fmt.Errorf("acknowledge Conductor signal %d after Claude CLI delivery: %w", signal.Index, err)
		}
	}
}

func ResumeArguments(sessionID, prompt string) []string {
	return []string{"--print", "--output-format", "json", "--resume", sessionID, prompt}
}

func SignalPrompt(agent string, signal conductor.Signal) (string, error) {
	payload, err := json.Marshal(signal)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Conductor activated this Claude Code turn for agent %q with signal %s. Use the installed Conductor skill to process the signal. For an update, read the named resource; for a join or leave, refresh the roster. The adapter already owns the wait loop, so do not start conductor watch. Process this signal idempotently and report the result.", agent, payload), nil
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
