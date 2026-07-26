package main

import (
	"context"
	"os"

	conductor "github.com/danlavee/Conductor"
)

func runWatchCommand(client *conductor.Client, agent string, args []string) error {
	watchArgs, modeValue, err := extractMode(args)
	if err != nil {
		return err
	}
	mode, err := conductor.ParseDeliveryMode(modeValue)
	if err != nil {
		return err
	}
	if len(watchArgs) == 0 {
		return runOneShotWatch(context.Background(), client, mode)
	}
	if len(watchArgs) != 1 {
		return usageError()
	}
	switch watchArgs[0] {
	case "--codex-desktop":
		return runCodexDesktopWatchCommand(client, agent, mode)
	case "--claude-cli":
		return runClaudeCLIWatchCommand(context.Background(), client, agent, mode)
	}
	return usageError()
}

func runOneShotWatch(ctx context.Context, client *conductor.Client, mode conductor.DeliveryMode) error {
	release, err := client.AcquireWatchOwnership()
	if err != nil {
		return err
	}
	defer func() { _ = release() }()
	summaries, err := client.WatchContext(ctx)
	if err != nil {
		return err
	}
	batch, err := client.ResolveBatch(summaries, mode)
	if err != nil {
		return err
	}
	if err := conductor.WriteJSON(os.Stdout, batch); err != nil {
		return err
	}
	return client.AcknowledgeBatch(batch)
}
