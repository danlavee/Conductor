// Package claudecli configures cliresume for Claude Code CLI: an agent loop
// with no external wake path, resumed non-interactively once per signal.
package claudecli

import (
	"context"
	"fmt"
	"io"

	conductor "github.com/danlavee/Conductor"
	"github.com/danlavee/Conductor/internal/integrations/cliresume"
)

const (
	DeliveryEnvironment = "CONDUCTOR_CLAUDE_DELIVERY"
	// LiveSessionEnvironment is set by the Claude Code host process itself,
	// identifying the session the current, attended turn is running in.
	LiveSessionEnvironment = "CLAUDE_CODE_SESSION_ID"
)

var transport = cliresume.Transport{
	RuntimeLabel:         "Claude Code CLI",
	TargetNoun:           "session",
	DefaultExecutable:    "claude",
	CandidateExecutables: candidateExecutables(),
	DeliveryEnvironment:  DeliveryEnvironment,
	ResumeArguments:      ResumeArguments,
	ValidateOutput:       cliresume.JSONOutputValidator(),
}

// Validate checks the explicit session ID and agent name given on the command
// line, and refuses to target the live session this process is itself
// running in (see LiveSessionEnvironment, sourced by the caller).
func Validate(sessionID, agent, liveSessionID string) error {
	if err := transport.Validate(sessionID, agent); err != nil {
		return err
	}
	if liveSessionID != "" && liveSessionID == sessionID {
		return fmt.Errorf("%s watch --claude-cli targets session %s, which is the live session this command is running in: resuming it would spawn a competing headless process against the attended transcript, and that process has no attached terminal to approve anything, so it would silently stall; for an attended session, run 'conductor %s watch' instead (no --claude-cli)", transport.RuntimeLabel, sessionID, agent)
	}
	return nil
}

type WatchClient = cliresume.WatchClient
type Activator = cliresume.Activator
type CLI = cliresume.CLI

func New(executable string, stdout, stderr io.Writer) (*CLI, error) {
	return cliresume.New(transport, executable, stdout, stderr)
}

func Run(ctx context.Context, client WatchClient, activator Activator, sessionID, agent string, mode conductor.DeliveryMode) error {
	return cliresume.Run(ctx, transport, client, activator, sessionID, agent, mode)
}

func ResumeArguments(sessionID, prompt string) []string {
	return []string{"--print", "--output-format", "json", "--resume", sessionID, prompt}
}

func SignalPrompt(agent string, delivery conductor.Delivery) (string, error) {
	return cliresume.SignalPrompt(transport.RuntimeLabel, agent, delivery)
}
