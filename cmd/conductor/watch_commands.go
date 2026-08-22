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
	// An unreadable adapter state is the one condition under which arming is
	// the unsafe act -- it is how a watcher collides with an adapter that later
	// claims the identity -- so it stops the command rather than being shrugged
	// off. Resolved before the stream is taken so the refusal never opens a
	// window in which this identity is briefly owned by a command that is going
	// to fail; the deferred release closes that window either way, so the order
	// buys the absence of the window and not the outcome.
	adapter, err := hostAdapter(client.Home)
	if err != nil {
		return err
	}
	release, err := client.AcquireWatchOwnership()
	if err != nil {
		return err
	}
	defer func() { _ = release() }()
	// The arm is an act, and until now it returned nothing: the agent started
	// this process, saw an empty stream, and had only the host's pid to
	// conclude from -- which says a process exists, not that the identity is
	// reachable or that anything reaches it. Answering here, before the wait,
	// puts the same state `status` reports in front of the agent at the one
	// moment it decides whether the act is done.
	if err := writeParticipation(client, adapter); err != nil {
		return err
	}
	for {
		continuing, err := deliverOnce(ctx, client)
		if err != nil || once || !continuing {
			return err
		}
	}
}

// deliverOnce reports whether the stream may continue. A replacement
// activation ends it: the conductor this process was watching no longer
// exists, and the adapter re-arms against its successor.
func deliverOnce(ctx context.Context, client *conductor.Client) (bool, error) {
	result, err := client.WatchResultContext(ctx)
	if err != nil {
		return false, err
	}
	defer result.Close()
	if result.Activation != nil {
		return false, conductor.WriteJSON(os.Stdout, result.Activation)
	}
	batch, err := client.ResolveBatch(result.Summaries)
	if err != nil {
		return false, err
	}
	if err := conductor.WriteJSON(os.Stdout, batch); err != nil {
		return false, err
	}
	return true, client.AcknowledgeBatch(batch)
}
