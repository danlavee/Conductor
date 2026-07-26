package main

import (
	"context"
	"os"

	conductor "github.com/danlavee/Conductor"
	"github.com/danlavee/Conductor/internal/integrations/codexdesktop"
)

func runCodexDesktopWatchCommand(client *conductor.Client, agent string, mode conductor.DeliveryMode) error {
	release, err := client.AcquireWatchOwnership()
	if err != nil {
		return err
	}
	defer func() { _ = release() }()
	return codexdesktop.Run(context.Background(), client, os.Stdout, agent, mode)
}
