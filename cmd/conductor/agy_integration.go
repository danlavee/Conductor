package main

import (
	"context"
	"os"

	conductor "github.com/danlavee/Conductor"
	"github.com/danlavee/Conductor/internal/integrations/agycli"
	"github.com/danlavee/Conductor/internal/integrations/agydesktop"
)

func runAgyWatchCommand(ctx context.Context, client *conductor.Client, agent string, mode conductor.DeliveryMode) error {
	conversationID := os.Getenv(agydesktop.ConversationIDEnvironment)
	if err := agydesktop.Validate(conversationID, agent); err != nil {
		return err
	}
	client.Agent = agent
	release, err := client.AcquireWatchOwnership()
	if err != nil {
		return err
	}
	defer func() { _ = release() }()
	activator, err := agydesktop.New("", os.Stdout, os.Stderr)
	if err != nil {
		return err
	}
	if err := activator.Check(ctx, conversationID); err != nil {
		return err
	}
	return agydesktop.Run(ctx, client, activator, conversationID, agent, mode)
}

func runAgyCLIWatchCommand(ctx context.Context, client *conductor.Client, agent string, mode conductor.DeliveryMode) error {
	conversationID := os.Getenv(agydesktop.ConversationIDEnvironment)
	if err := agycli.Validate(conversationID, agent); err != nil {
		return err
	}
	client.Agent = agent
	release, err := client.AcquireWatchOwnership()
	if err != nil {
		return err
	}
	defer func() { _ = release() }()
	activator, err := agycli.New("", os.Stdout, os.Stderr)
	if err != nil {
		return err
	}
	if err := activator.Check(ctx); err != nil {
		return err
	}
	return agycli.Run(ctx, client, activator, conversationID, agent, mode)
}
