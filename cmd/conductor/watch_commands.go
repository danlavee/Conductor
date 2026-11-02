package main

import (
	"context"
	"os"

	conductor "github.com/danlavee/Conductor/internal/state"
)

func runWatchCommand(client *conductor.Client, args []string) error {
	once := false
	for _, argument := range args {
		if argument != "--once" || once {
			return usageError()
		}
		once = true
	}
	return runWatch(context.Background(), client, once)
}

// runWatch owns the identity's delivery stream for as long as the process
// lives. Ownership is acquired once rather than per delivery, so the gap
// between draining a batch and the next signal is not a window in which a
// second watcher can arm for the same identity.
//
// This is the lifecycle an agent owns only on a host whose adapter has not
// landed; an adapter holds the stream through the client instead.
//
// --once returns after the first delivery. That is not a lesser mode: on a
// host whose wake primitive is process exit, exiting is how the delivery
// reaches a turn.
func runWatch(ctx context.Context, client *conductor.Client, once bool) error {
	release, err := client.AcquireWatchOwnership(conductor.HolderSelf)
	if err != nil {
		return err
	}
	defer func() { _ = release() }()
	// Answering before the wait puts what `status` reports in front of the
	// agent at the one moment it decides whether arming is done; a started
	// process alone says nothing about reachability.
	if err := writeStatus(client); err != nil {
		return err
	}
	for {
		if err := deliverOnce(ctx, client); err != nil || once {
			return err
		}
	}
}

func deliverOnce(ctx context.Context, client *conductor.Client) error {
	batch, err := client.WatchContext(ctx)
	if err != nil {
		return err
	}
	if err := conductor.WriteJSON(os.Stdout, batch); err != nil {
		return err
	}
	return client.AcknowledgeRead(batch.Deliveries...)
}
