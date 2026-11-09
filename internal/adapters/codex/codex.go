// Package codex hands Conductor wakes to a native Codex task.
package codex

import (
	"context"

	"github.com/danlavee/Conductor/internal/adapters/hold"
	"github.com/danlavee/Conductor/internal/state"
)

const (
	AdapterName       = "codex"
	ThreadEnvironment = "CODEX_THREAD_ID"
)

// Serve runs beneath the dispatcher's hold and acknowledges only accepted text.
func Serve(ctx context.Context, client *state.Client, session hold.Session, queue func(context.Context, string, string) error) error {
	for {
		delivered, err := hold.Watch(ctx, client, session, func(batch state.Batch) ([]state.ReadResult, error) {
			text, shown := state.RenderWake(batch)
			if err := queue(ctx, session.ID, text); err != nil {
				return nil, err
			}
			return shown, nil
		})
		if err != nil || !delivered {
			return err
		}
	}
}
