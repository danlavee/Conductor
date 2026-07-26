package main

import (
	"fmt"
	"os"

	conductor "github.com/danlavee/Conductor"
	"github.com/danlavee/Conductor/internal/cutover"
	"github.com/danlavee/Conductor/internal/migrate"
)

func runMigrateCommand(source, destination string) error {
	current, exists, err := cutover.Observe(source)
	if err != nil {
		return err
	}
	if !exists || current.Phase != cutover.Frozen {
		return fmt.Errorf("migration requires a frozen cutover source")
	}
	report, err := migrate.Run(source, destination)
	if err != nil {
		return err
	}
	return conductor.WriteJSON(os.Stdout, report)
}
