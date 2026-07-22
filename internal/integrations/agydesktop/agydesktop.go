// Package agydesktop delivers Conductor signals to Antigravity 2.0 through agentapi.
package agydesktop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"

	conductor "github.com/danlavee/Conductor"
)

const (
	BinaryEnvironment       = "CONDUCTOR_AGY_AGENTAPI_BIN"
	ConversationEnvironment = "CONDUCTOR_AGY_CONVERSATION_ID"
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
	conversationID := getenv(ConversationEnvironment)
	if strings.TrimSpace(conversationID) == "" {
		conversationID = getenv("ANTIGRAVITY_CONVERSATION_ID")
	}
	return Environment{
		Executable:     getenv(BinaryEnvironment),
		ConversationID: conversationID,
	}
}

func (e Environment) Validate(agent string) error {
	if strings.TrimSpace(e.ConversationID) == "" {
		return fmt.Errorf("%s is required for Antigravity watch", ConversationEnvironment)
	}
	if strings.TrimSpace(agent) == "" {
		return errors.New("Antigravity watch requires an agent name")
	}
	return nil
}

type Activator interface {
	Check(context.Context, string) error
	Activate(context.Context, string, string, conductor.Signal) error
}

type AgentAPI struct {
	executable string
	stdout     io.Writer
	stderr     io.Writer
	command    func(context.Context, string, ...string) *exec.Cmd
}

func New(executable string, stdout, stderr io.Writer) (*AgentAPI, error) {
	if strings.TrimSpace(executable) == "" {
		var err error
		executable, err = exec.LookPath("agentapi")
		if err != nil {
			return nil, fmt.Errorf("find Antigravity agentapi: run this watcher as an enabled Antigravity sidecar or set %s: %w", BinaryEnvironment, err)
		}
	}
	return &AgentAPI{executable: executable, stdout: stdout, stderr: stderr, command: exec.CommandContext}, nil
}

func (a *AgentAPI) Check(ctx context.Context, conversationID string) error {
	command := a.command(ctx, a.executable, "get-conversation-metadata", conversationID)
	command.Stdout = writerOrDiscard(a.stdout)
	command.Stderr = writerOrDiscard(a.stderr)
	if err := command.Run(); err != nil {
		return fmt.Errorf("validate Antigravity conversation %s through agentapi: %w", conversationID, err)
	}
	return nil
}

func (a *AgentAPI) Activate(ctx context.Context, conversationID, agent string, signal conductor.Signal) error {
	prompt, err := SignalPrompt(agent, signal)
	if err != nil {
		return err
	}
	command := a.command(ctx, a.executable, "send-message", conversationID, prompt)
	command.Stdout = writerOrDiscard(a.stdout)
	command.Stderr = writerOrDiscard(a.stderr)
	if err := command.Run(); err != nil {
		return fmt.Errorf("send Antigravity message to conversation %s: %w", conversationID, err)
	}
	return nil
}

func Run(ctx context.Context, client WatchClient, activator Activator, conversationID, agent string) error {
	if strings.TrimSpace(conversationID) == "" {
		return fmt.Errorf("%s is required for Antigravity watch", ConversationEnvironment)
	}
	if strings.TrimSpace(agent) == "" {
		return errors.New("Antigravity watch requires an agent name")
	}
	for {
		signal, err := client.WatchContext(ctx)
		if err != nil {
			return err
		}
		if err := activator.Activate(ctx, conversationID, agent, signal); err != nil {
			return fmt.Errorf("deliver Conductor signal %d to Antigravity: %w", signal.Index, err)
		}
		if err := client.AcknowledgeSignal(signal); err != nil {
			return fmt.Errorf("acknowledge Conductor signal %d after Antigravity delivery: %w", signal.Index, err)
		}
	}
}

func SignalPrompt(agent string, signal conductor.Signal) (string, error) {
	payload, err := json.Marshal(signal)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Conductor activated this Antigravity turn for agent %q with signal %s. Use the installed Conductor skill now and set CONDUCTOR_AGENT=%s on every Conductor command. For an update, read the named resource; for a join or leave, refresh the roster. The Antigravity sidecar already owns the wait loop, so do not start conductor watch. Process this signal idempotently and report the result.", agent, payload, agent), nil
}

func writerOrDiscard(writer io.Writer) io.Writer {
	if writer == nil {
		return io.Discard
	}
	return writer
}
