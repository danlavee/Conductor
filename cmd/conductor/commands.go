package main

import (
	"errors"
	"os"
	"runtime"
	"runtime/debug"
	"strings"

	conductor "github.com/danlavee/Conductor"
	"github.com/danlavee/Conductor/internal/install"
	skillbundle "github.com/danlavee/Conductor/skills"
)

func run(args []string) error {
	if len(args) == 0 {
		return usageError()
	}
	switch args[0] {
	case "install":
		if len(args) != 2 || strings.TrimSpace(args[1]) == "" {
			return installUsageError()
		}
		if err := install.ValidateDestination(args[1]); err != nil {
			return installUsageError()
		}
		source, err := installationSource(true)
		if err != nil {
			return err
		}
		result, err := install.Install(args[1], source)
		if err != nil {
			return err
		}
		return conductor.WriteJSON(os.Stdout, result)
	case "verify":
		if len(args) != 2 || strings.TrimSpace(args[1]) == "" {
			return errors.New("usage: conductor verify <absolute-skill-directory>")
		}
		if err := install.ValidateDestination(args[1]); err != nil {
			return err
		}
		source, err := installationSource(false)
		if err != nil {
			return err
		}
		result, err := install.CheckCurrency(args[1], source)
		if err != nil {
			return err
		}
		return conductor.WriteJSON(os.Stdout, result)
	case "version":
		if len(args) != 1 {
			return errors.New("usage: conductor version")
		}
		return conductor.WriteJSON(os.Stdout, struct {
			Version  string `json:"version"`
			Protocol int    `json:"protocol"`
		}{Version: currentVersion(), Protocol: conductor.CurrentProtocolVersion})
	case "migrate":
		if len(args) != 3 {
			return errors.New("usage: conductor migrate <absolute-source-root> <absolute-destination-root>")
		}
		return runMigrateCommand(args[1], args[2])
	}
	if len(args) < 2 {
		return usageError()
	}
	return runAgentCommand(args[0], args[1], args[2:])
}

func installationSource(smokeCheck bool) (install.Source, error) {
	executablePath, err := os.Executable()
	if err != nil {
		return install.Source{}, err
	}
	return install.Source{
		Bundle:         skillbundle.Files,
		ExecutablePath: executablePath,
		Version:        currentVersion(),
		Protocol:       conductor.CurrentProtocolVersion,
		GOOS:           runtime.GOOS,
		GOARCH:         runtime.GOARCH,
		SmokeCheck:     smokeCheck,
	}, nil
}

func currentVersion() string {
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" {
		return info.Main.Version
	}
	return "(devel)"
}
