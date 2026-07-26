// Package codexdesktop adapts Conductor's blocking watch contract to one
// short poll initiated by a Codex Desktop heartbeat turn.
package codexdesktop

import (
	"context"
	"errors"
	"io"
	"strings"

	conductor "github.com/danlavee/Conductor"
)

type Status string

const (
	StatusIdle     Status = "idle"
	StatusActivity Status = "activity"
)

type Result struct {
	Status    Status                   `json:"status"`
	Transport string                   `json:"transport"`
	Agent     string                   `json:"agent"`
	Batch     *conductor.BatchDelivery `json:"batch,omitempty"`
}

type WatchClient interface {
	WatchContext(context.Context) ([]conductor.Summary, error)
	ResolveBatch([]conductor.Summary, conductor.DeliveryMode) (conductor.BatchDelivery, error)
	AcknowledgeBatch(conductor.BatchDelivery) error
}

// Run checks the current watch backlog once. An already-cancelled child
// context lets WatchContext perform its normal pending scan but prevents it
// from entering the blocking wait when the backlog is empty.
func Run(ctx context.Context, client WatchClient, output io.Writer, agent string, mode conductor.DeliveryMode) error {
	if strings.TrimSpace(agent) == "" {
		return errors.New("Codex Desktop watch requires an agent name")
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	checkContext, cancel := context.WithCancel(ctx)
	cancel()
	summaries, err := client.WatchContext(checkContext)
	if errors.Is(err, context.Canceled) {
		return conductor.WriteJSON(output, Result{
			Status:    StatusIdle,
			Transport: "codex-desktop",
			Agent:     agent,
		})
	}
	if err != nil {
		return err
	}

	batch, err := client.ResolveBatch(summaries, mode)
	if err != nil {
		return err
	}
	result := Result{
		Status:    StatusActivity,
		Transport: "codex-desktop",
		Agent:     agent,
		Batch:     &batch,
	}
	if err := conductor.WriteJSON(output, result); err != nil {
		return err
	}
	return client.AcknowledgeBatch(batch)
}
