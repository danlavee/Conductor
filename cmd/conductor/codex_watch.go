package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	conductor "github.com/danlavee/Conductor"
	"github.com/danlavee/Conductor/internal/integrations/codex"
)

func runCodexWatchCommand(ctx context.Context, client *conductor.Client, agent string) error {
	threadID := os.Getenv(codex.ThreadEnvironment)
	if strings.TrimSpace(threadID) == "" {
		return fmt.Errorf("%s is required for Codex watch", codex.ThreadEnvironment)
	}
	if strings.TrimSpace(agent) == "" {
		return errors.New("Codex watch requires an agent name")
	}
	client.Agent = agent
	release, err := client.AcquireWatchOwnership()
	if err != nil {
		return err
	}
	defer release()
	session, err := codex.Start(ctx, os.Getenv(codex.BinaryEnvironment), threadID, codex.Sandbox(map[string]string{
		codex.SandboxEnvironment:    os.Getenv(codex.SandboxEnvironment),
		codex.PermissionEnvironment: os.Getenv(codex.PermissionEnvironment),
	}), os.Stdout, os.Stderr)
	if err != nil {
		return err
	}
	defer session.Close()
	return codex.Run(ctx, client, session, threadID, agent)
}

func runCodexCLIWatchCommand(ctx context.Context, client *conductor.Client, agent string) error {
	threadID := os.Getenv(codex.ThreadEnvironment)
	if strings.TrimSpace(threadID) == "" {
		return fmt.Errorf("%s is required for Codex CLI watch", codex.ThreadEnvironment)
	}
	if strings.TrimSpace(agent) == "" {
		return errors.New("Codex CLI watch requires an agent name")
	}
	client.Agent = agent
	release, err := client.AcquireWatchOwnership()
	if err != nil {
		return err
	}
	defer release()
	activator, err := codex.New(os.Getenv(codex.BinaryEnvironment), codex.Sandbox(map[string]string{
		codex.SandboxEnvironment:    os.Getenv(codex.SandboxEnvironment),
		codex.PermissionEnvironment: os.Getenv(codex.PermissionEnvironment),
	}), os.Stdout, os.Stderr)
	if err != nil {
		return err
	}
	if err := activator.Check(ctx); err != nil {
		return err
	}
	return codex.Run(ctx, client, activator, threadID, agent)
}
