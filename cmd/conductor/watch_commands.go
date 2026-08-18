package main

import (
	"context"
	"os"

	conductor "github.com/danlavee/Conductor"
)

func runWatchCommand(client *conductor.Client, args []string) error {
	watchArgs, modeValue, err := extractMode(args)
	if err != nil {
		return err
	}
	mode, err := conductor.ParseDeliveryMode(modeValue)
	if err != nil {
		return err
	}
	once := false
	for _, argument := range watchArgs {
		if argument != "--once" || once {
			return usageError()
		}
		once = true
	}
	return runWatch(context.Background(), client, mode, once)
}

// runWatch owns the identity's delivery stream for as long as the process
// lives. Ownership is acquired once rather than per delivery, so the gap
// between draining a batch and the next signal is not a window in which a
// second watcher can arm for the same identity.
//
// --once returns after the first delivery. That is not a lesser mode: on a
// host whose wake primitive is process exit, exiting is how the delivery
// reaches a turn, and the adapter arms the successor from the event that ends
// the woken turn.
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
