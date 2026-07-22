// Package agycli delivers Conductor signals through non-interactive Antigravity CLI turns.
package agycli

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
	BinaryEnvironment       = "CONDUCTOR_AGY_BIN"
	ConversationEnvironment = "ANTIGRAVITY_CONVERSATION_ID"
	DeliveryEnvironment     = "CONDUCTOR_AGY_DELIVERY"
)

type WatchClient interface {
	WatchContext(context.Context) (conductor.Signal, error)
	AcknowledgeSignal(conductor.Signal) error
}

type Environment struct {
	Executable     string
	ConversationID string
}

func EnvironmentFrom(getenv func(string) string) Environment {
	return Environment{
		Executable:     getenv(BinaryEnvironment),
		ConversationID: getenv(ConversationEnvironment),
	}
}

func (e Environment) Validate(agent string) error {
	if strings.TrimSpace(e.ConversationID) == "" {
		return fmt.Errorf("%s is required for Antigravity CLI watch", ConversationEnvironment)
	}
	if strings.TrimSpace(agent) == "" {
		return errors.New("Antigravity CLI watch requires an agent name")
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
	command    func(context.Context, string, ...string) *exec.Cmd
}

func New(executable string, stdout, stderr io.Writer) (*CLI, error) {
	if strings.TrimSpace(executable) == "" {
		var err error
		executable, err = exec.LookPath("agy")
		if err != nil {
			return nil, fmt.Errorf("find Antigravity CLI: install it or set %s to its executable: %w", BinaryEnvironment, err)
		}
	}
	return &CLI{executable: executable, stdout: stdout, stderr: stderr, command: exec.CommandContext}, nil
}

func (a *CLI) Check(ctx context.Context) error {
	command := a.command(ctx, a.executable, "--version")
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("run Antigravity CLI %q: %w%s", a.executable, err, outputSuffix(output))
	}
	return nil
}

func (a *CLI) Activate(ctx context.Context, conversationID, agent string, signal conductor.Signal) error {
	prompt, err := SignalPrompt(agent, signal)
	if err != nil {
		return err
	}
	command := a.command(ctx, a.executable, ResumeArguments(conversationID, prompt)...)
	command.Env = setEnvironment(os.Environ(), map[string]string{
		"CONDUCTOR_AGENT":       agent,
		DeliveryEnvironment:     "1",
		ConversationEnvironment: conversationID,
	})
	command.Stderr = a.stderr
	var output bytes.Buffer
	command.Stdout = io.MultiWriter(&output, writerOrDiscard(a.stdout))
	if err := command.Run(); err != nil {
		return fmt.Errorf("resume Antigravity conversation %s: %w (note: if this is an active interactive session, use 'conductor watch --agy %s' instead of '--agy-cli')", conversationID, err, agent)
	}
	if len(bytes.TrimSpace(output.Bytes())) == 0 {
		return fmt.Errorf("resume Antigravity conversation %s: CLI exited without output", conversationID)
	}
	return nil
}

func Run(ctx context.Context, client WatchClient, activator Activator, conversationID, agent string) error {
	if strings.TrimSpace(conversationID) == "" {
		return fmt.Errorf("%s is required for Antigravity CLI watch", ConversationEnvironment)
	}
	if strings.TrimSpace(agent) == "" {
		return errors.New("Antigravity CLI watch requires an agent name")
	}
	for {
		signal, err := client.WatchContext(ctx)
		if err != nil {
			return err
		}
		if err := activator.Activate(ctx, conversationID, agent, signal); err != nil {
			return fmt.Errorf("deliver Conductor signal %d to Antigravity CLI: %w", signal.Index, err)
		}
		if err := client.AcknowledgeSignal(signal); err != nil {
			return fmt.Errorf("acknowledge Conductor signal %d after Antigravity CLI delivery: %w", signal.Index, err)
		}
	}
}

func ResumeArguments(conversationID, prompt string) []string {
	return []string{"--print", "--conversation", conversationID, prompt}
}

func SignalPrompt(agent string, signal conductor.Signal) (string, error) {
	payload, err := json.Marshal(signal)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Conductor activated this Antigravity CLI turn for agent %q with signal %s. Use the installed Conductor skill to process the signal. For an update, read the named resource; for a join or leave, refresh the roster. The adapter already owns the wait loop, so do not start conductor watch. Process this signal idempotently and report the result.", agent, payload), nil
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
