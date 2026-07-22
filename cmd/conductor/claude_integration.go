package main

import (
	"context"
	"os"

	conductor "github.com/danlavee/Conductor"
	"github.com/danlavee/Conductor/internal/integrations/claudechannel"
	"github.com/danlavee/Conductor/internal/integrations/claudecli"
)

func runClaudeCLIWatchCommand(ctx context.Context, client *conductor.Client, agent, sessionID string, mode conductor.DeliveryMode) error {
	if err := claudecli.Validate(sessionID, agent, os.Getenv(claudecli.LiveSessionEnvironment)); err != nil {
		return err
	}
	client.Agent = agent
	release, err := client.AcquireWatchOwnership()
	if err != nil {
		return err
	}
	defer func() { _ = release() }()
	activator, err := claudecli.New("", os.Stdout, os.Stderr)
	if err != nil {
		return err
	}
	if err := activator.Check(ctx); err != nil {
		return err
	}
	return claudecli.Run(ctx, client, activator, sessionID, agent, mode)
}

func runClaudeChannelCommand(ctx context.Context, client *conductor.Client, agent string) error {
	client.Agent = agent
	release, err := client.AcquireWatchOwnership()
	if err != nil {
		return err
	}
	defer func() { _ = release() }()
	return claudechannel.Run(ctx, client, os.Stdin, os.Stdout, agent)
}
