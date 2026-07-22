// Package agycli configures cliresume for Antigravity CLI: an agent loop
// with no external wake path, resumed non-interactively once per signal.
package agycli

import (
	"context"
	"fmt"
	"io"

	conductor "github.com/danlavee/Conductor"
	"github.com/danlavee/Conductor/internal/integrations/cliresume"
)

const (
	DeliveryEnvironment = "CONDUCTOR_AGY_DELIVERY"
)

// Antigravity CLI has no confidently known standard install location beyond
// PATH, so its Transport carries no CandidateExecutables.
var transport = cliresume.Transport{
	RuntimeLabel:        "Antigravity CLI",
	TargetNoun:          "conversation",
	DefaultExecutable:   "agy",
	DeliveryEnvironment: DeliveryEnvironment,
	ResumeArguments:     ResumeArguments,
	ValidateOutput:      cliresume.NonEmptyOutputValidator(),
	ResumeFailureHint: func(_, agent string) string {
		return fmt.Sprintf(" (note: if this is an active interactive session, use 'conductor %s watch --agy <conversation-id>' instead of '--agy-cli')", agent)
	},
}

// Validate checks the explicit conversation ID and agent name given on the command line.
func Validate(conversationID, agent string) error {
	return transport.Validate(conversationID, agent)
}

type WatchClient = cliresume.WatchClient
type Activator = cliresume.Activator
type CLI = cliresume.CLI

func New(executable string, stdout, stderr io.Writer) (*CLI, error) {
	return cliresume.New(transport, executable, stdout, stderr)
}

func Run(ctx context.Context, client WatchClient, activator Activator, conversationID, agent string, mode conductor.DeliveryMode) error {
	return cliresume.Run(ctx, transport, client, activator, conversationID, agent, mode)
}

func ResumeArguments(conversationID, prompt string) []string {
	return []string{"--print", "--conversation", conversationID, prompt}
}

func SignalPrompt(agent string, delivery conductor.Delivery) (string, error) {
	return cliresume.SignalPrompt(transport.RuntimeLabel, agent, delivery)
}
