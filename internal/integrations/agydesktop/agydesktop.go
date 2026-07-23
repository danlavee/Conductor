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
	"github.com/danlavee/Conductor/internal/integrations/execlocate"
)

// ConversationIDEnvironment is set by the Antigravity host itself, identifying
// the conversation that watch --agy and --agy-cli should resume and deliver
// signals into.
const ConversationIDEnvironment = "ANTIGRAVITY_CONVERSATION_ID"

type WatchClient interface {
	WatchContext(context.Context) (conductor.Summary, error)
	ResolveDelivery(conductor.Summary, conductor.DeliveryMode) (conductor.Delivery, error)
	AcknowledgeDelivery(conductor.Delivery) error
}

// Validate checks the conversation ID (sourced from ConversationIDEnvironment) and agent name.
func Validate(conversationID, agent string) error {
	if strings.TrimSpace(conversationID) == "" {
		return errors.New("Antigravity watch requires a conversation ID")
	}
	if strings.TrimSpace(agent) == "" {
		return errors.New("Antigravity watch requires an agent name")
	}
	return nil
}

type Activator interface {
	Check(context.Context, string) error
	Activate(context.Context, string, string, conductor.Delivery) error
}

type AgentAPI struct {
	executable string
	stdout     io.Writer
	stderr     io.Writer
	command    func(context.Context, string, ...string) *exec.Cmd
}

// agentapi has no confidently known standard install location beyond PATH:
// Antigravity 2.0 adds it to PATH itself when run as an enabled sidecar, so
// resolution relies on PATH alone.
func New(executable string, stdout, stderr io.Writer) (*AgentAPI, error) {
	if strings.TrimSpace(executable) == "" {
		var err error
		executable, err = execlocate.Find("Antigravity agentapi", "agentapi", nil)
		if err != nil {
			return nil, err
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

func (a *AgentAPI) Activate(ctx context.Context, conversationID, agent string, delivery conductor.Delivery) error {
	prompt, err := SignalPrompt(agent, delivery)
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

func Run(ctx context.Context, client WatchClient, activator Activator, conversationID, agent string, mode conductor.DeliveryMode) error {
	if err := Validate(conversationID, agent); err != nil {
		return err
	}
	for {
		summary, err := client.WatchContext(ctx)
		if err != nil {
			return err
		}
		delivery, err := client.ResolveDelivery(summary, mode)
		if err != nil {
			return fmt.Errorf("resolve Conductor summary %d for Antigravity: %w", summary.Sequence, err)
		}
		if err := activator.Activate(ctx, conversationID, agent, delivery); err != nil {
			return fmt.Errorf("deliver Conductor summary %d to Antigravity: %w", summary.Sequence, err)
		}
		if err := client.AcknowledgeDelivery(delivery); err != nil {
			return fmt.Errorf("acknowledge Conductor summary %d after Antigravity delivery: %w", summary.Sequence, err)
		}
	}
}

func SignalPrompt(agent string, delivery conductor.Delivery) (string, error) {
	payload, err := json.Marshal(delivery)
	if err != nil {
		return "", err
	}
	if delivery.Mode == conductor.DeliverySummary {
		return fmt.Sprintf("Conductor activated this Antigravity turn for agent %q with signal summary %s. Resolve it as needed and process it idempotently. The sidecar already owns the watch loop.", agent, payload), nil
	}
	return fmt.Sprintf("Conductor activated this Antigravity turn for agent %q with delivery %s. The complete topic delta or roster is included; do not fetch it again. Process it idempotently. The sidecar already owns the watch loop.", agent, payload), nil
}

func writerOrDiscard(writer io.Writer) io.Writer {
	if writer == nil {
		return io.Discard
	}
	return writer
}
