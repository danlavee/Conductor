package main

import (
	"context"
	"os"

	conductor "github.com/danlavee/Conductor"
)

func runWatchCommand(client *conductor.Client, args []string) error {
	once := false
	for _, argument := range args {
		if argument != "--once" || once {
			return usageError()
		}
		once = true
	}
	return runWatch(context.Background(), client, conductor.DeliveryContent, once)
}

// runWatch owns the identity's delivery stream for as long as the process
// lives. Ownership is acquired once rather than per delivery, so the gap
// between draining a batch and the next signal is not a window in which a
// second watcher can arm for the same identity.
//
// This is the lifecycle an agent owns only on a host whose adapter has not
// landed; an adapter holds the stream through the client instead, and picks its
// own delivery mode there. The CLI resolves content because the agent reading
// this output has no second step in which to resolve a pointer.
//
// --once returns after the first delivery. That is not a lesser mode: on a
// host whose wake primitive is process exit, exiting is how the delivery
// reaches a turn.
func runWatch(ctx context.Context, client *conductor.Client, mode conductor.DeliveryMode, once bool) error {
	release, err := client.AcquireWatchOwnership()
	if err != nil {
		return err
	}
	defer func() { _ = release() }()
	for {
		continuing, err := deliverOnce(ctx, client, mode)
		if err != nil || once || !continuing {
			return err
		}
	}
}

// deliverOnce reports whether the stream may continue. A replacement
// activation ends it: the conductor this process was watching no longer
// exists, and the adapter re-arms against its successor.
func deliverOnce(ctx context.Context, client *conductor.Client, mode conductor.DeliveryMode) (bool, error) {
	result, err := client.WatchResultContext(ctx)
	if err != nil {
		return false, err
	}
	defer result.Close()
	if result.Activation != nil {
		return false, conductor.WriteJSON(os.Stdout, result.Activation)
	}
	batch, err := client.ResolveBatch(result.Summaries, mode)
	if err != nil {
		return false, err
	}
	if err := conductor.WriteJSON(os.Stdout, batch); err != nil {
		return false, err
	}
	return true, client.AcknowledgeBatch(batch)
}
