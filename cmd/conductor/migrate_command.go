package main

import (
	"os"

	conductor "github.com/danlavee/Conductor"
	"github.com/danlavee/Conductor/internal/migrate"
)

func runMigrateCommand(source, destination string) error {
	report, err := migrate.Run(source, destination)
	if err != nil {
		return err
	}
	return conductor.WriteJSON(os.Stdout, report)
}
