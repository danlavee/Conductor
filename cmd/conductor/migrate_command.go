package main

import (
	"fmt"
	"os"

	conductor "github.com/danlavee/Conductor"
	"github.com/danlavee/Conductor/internal/cutover"
	"github.com/danlavee/Conductor/internal/migrate"
)

func runMigrateCommand(source, destination string) error {
	// Held first: migration reads the whole source root, and a lease that
	// lapsed partway would otherwise let ordinary writes back in behind it.
	releaseBarrier, err := cutover.HoldBarrier(source)
	if err != nil {
		return fmt.Errorf("another command is already working behind this cutover: %w", err)
	}
	defer releaseBarrier()
	frozen, err := cutover.IsFrozen(source)
	if err != nil {
		return err
	}
	if !frozen {
		return fmt.Errorf("migration requires a frozen cutover source")
	}
	report, err := migrate.Run(source, destination)
	if err != nil {
		return err
	}
	return conductor.WriteJSON(os.Stdout, report)
}
