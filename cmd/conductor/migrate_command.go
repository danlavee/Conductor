package main

import (
	"fmt"
	"os"

	conductor "github.com/danlavee/Conductor"
	"github.com/danlavee/Conductor/internal/migrate"
)

func runMigrateCommand(source, destination string) error {
	version, err := migrate.DetectSourceVersion(source)
	if err != nil {
		return err
	}
	var report migrate.Report
	switch version {
	case 1:
		report, err = migrate.Run(source, destination)
	case 2:
		report, err = migrate.RunV2ToV3(source, destination)
	case 3:
		report, err = migrate.RunV3ToV4(source, destination)
	default:
		err = fmt.Errorf("migrate supports v1, v2 or v3 source roots, found protocol %d", version)
	}
	if err != nil {
		return err
	}
	return conductor.WriteJSON(os.Stdout, report)
}
