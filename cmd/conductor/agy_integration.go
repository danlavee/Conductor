package main

import (
	"context"
	"os"

	conductor "github.com/danlavee/Conductor"
	"github.com/danlavee/Conductor/internal/integrations/agycli"
	"github.com/danlavee/Conductor/internal/integrations/agydesktop"
)

func runAgyWatchCommand(ctx context.Context, client *conductor.Client, agent string) error {
	environment := agydesktop.EnvironmentFrom(os.Getenv)
	if err := environment.Validate(agent); err != nil {
		return err
	}
	client.Agent = agent
	release, err := client.AcquireWatchOwnership()
	if err != nil {
		return err
	}
	defer func() { _ = release() }()
	activator, err := agydesktop.New(environment.Executable, os.Stdout, os.Stderr)
	if err != nil {
		return err
	}
	if err := activator.Check(ctx, environment.ConversationID); err != nil {
		return err
	}
	return agydesktop.Run(ctx, client, activator, environment.ConversationID, agent)
}

func runAgyCLIWatchCommand(ctx context.Context, client *conductor.Client, agent string) error {
	environment := agycli.EnvironmentFrom(os.Getenv)
	if err := environment.Validate(agent); err != nil {
		return err
	}
	client.Agent = agent
	release, err := client.AcquireWatchOwnership()
	if err != nil {
		return err
	}
	defer func() { _ = release() }()
	activator, err := agycli.New(environment.Executable, os.Stdout, os.Stderr)
	if err != nil {
		return err
	}
	if err := activator.Check(ctx); err != nil {
		return err
	}
	return agycli.Run(ctx, client, activator, environment.ConversationID, agent)
}
