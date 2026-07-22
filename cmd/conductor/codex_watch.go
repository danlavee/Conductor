package main

import (
	"context"
	"os"

	conductor "github.com/danlavee/Conductor"
	"github.com/danlavee/Conductor/internal/integrations/codex"
)

func runCodexWatchCommand(ctx context.Context, client *conductor.Client, agent, threadID string, mode conductor.DeliveryMode) error {
	if err := codex.Validate(threadID, agent); err != nil {
		return err
	}
	client.Agent = agent
	release, err := client.AcquireWatchOwnership()
	if err != nil {
		return err
	}
	defer release()
	session, err := codex.Start(ctx, "", threadID, codex.Sandbox(map[string]string{
		codex.SandboxEnvironment:    os.Getenv(codex.SandboxEnvironment),
		codex.PermissionEnvironment: os.Getenv(codex.PermissionEnvironment),
	}), os.Stdout, os.Stderr)
	if err != nil {
		return err
	}
	defer session.Close()
	return codex.Run(ctx, client, session, threadID, agent, mode)
}

func runCodexCLIWatchCommand(ctx context.Context, client *conductor.Client, agent, threadID string, mode conductor.DeliveryMode) error {
	if err := codex.Validate(threadID, agent); err != nil {
		return err
	}
	client.Agent = agent
	release, err := client.AcquireWatchOwnership()
	if err != nil {
		return err
	}
	defer release()
	activator, err := codex.New("", codex.Sandbox(map[string]string{
		codex.SandboxEnvironment:    os.Getenv(codex.SandboxEnvironment),
		codex.PermissionEnvironment: os.Getenv(codex.PermissionEnvironment),
	}), os.Stdout, os.Stderr)
	if err != nil {
		return err
	}
	if err := activator.Check(ctx); err != nil {
		return err
	}
	return codex.Run(ctx, client, activator, threadID, agent, mode)
}
