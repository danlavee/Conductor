package main

import (
	"context"
	"io"
	"os"

	conductor "github.com/danlavee/Conductor/internal/state"
)

func runWatchCommand(client *conductor.Client, args []string) error {
	if len(args) != 0 {
		return usageError()
	}
	return runWatch(context.Background(), client)
}

// runWatch holds the identity's stream for one delivery, then exits. Exiting
// is the point: wherever no adapter has landed, a host wakes the agent by
// noticing the process it backgrounded has completed, so a watch that kept
// streaming would deliver into a turn that never starts. Adapters hold the
// stream through the client instead and never come here.
func runWatch(ctx context.Context, client *conductor.Client) error {
	release, err := client.AcquireWatchOwnership(conductor.HolderSelf, "")
	if err != nil {
		return err
	}
	defer func() { _ = release() }()
	batch, err := client.WatchContext(ctx)
	if err != nil {
		return err
	}
	text, shown := conductor.RenderWake(batch)
	if _, err := io.WriteString(os.Stdout, text); err != nil {
		return err
	}
	return client.AcknowledgeRead(shown...)
}
