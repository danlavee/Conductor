package main

import (
	"fmt"
	"os"

	conductor "github.com/danlavee/Conductor"
	"github.com/danlavee/Conductor/internal/cutover"
	"github.com/danlavee/Conductor/internal/migrate"
)

func runMigrateCommand(source, destination string) error {
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
