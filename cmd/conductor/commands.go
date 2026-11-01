package main

import (
	"errors"
	"fmt"
	"io/fs"
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
	if helpRequested(args) {
		return conductor.WriteJSON(os.Stdout, usageResult{Usage: usageText})
	}
	switch args[0] {
	case "install":
		if len(args) < 2 || strings.TrimSpace(args[1]) == "" {
			return installUsageError()
		}
		replace, err := replaceRequested(args[2:])
		if err != nil {
			return err
		}
		if err := install.ValidateDestination(args[1], install.SkillPayload()); err != nil {
			return installUsageError()
		}
		source, err := installationSource(install.SkillPayload(), skillbundle.Files, true)
		if err != nil {
			return err
		}
		source.Replace = replace
		result, err := install.Install(args[1], source)
		if err != nil {
			return err
		}
		return conductor.WriteJSON(os.Stdout, result)
	case "verify":
		if len(args) != 2 || strings.TrimSpace(args[1]) == "" {
			return errors.New("usage: conductor verify <absolute-skill-directory>")
		}
		if err := install.ValidateDestination(args[1], install.SkillPayload()); err != nil {
			return err
		}
		source, err := installationSource(install.SkillPayload(), skillbundle.Files, false)
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
	case "adapter":
		return runAdapterCommand(args[1:])
	}
	if len(args) < 2 {
		return usageError()
	}
	return runAgentCommand(args[0], args[1], args[2:])
}

// replaceRequested reads the optional --replace that turns an install into a
// replacement of the installation already at the destination. Every process
// running the old binary must be stopped first.
func replaceRequested(args []string) (bool, error) {
	switch {
	case len(args) == 0:
		return false, nil
	case len(args) == 1 && args[0] == "--replace":
		return true, nil
	}
	return false, fmt.Errorf("unrecognized option %s", strings.Join(args, " "))
}

func installationSource(payload install.Payload, bundle fs.FS, smokeCheck bool) (install.Source, error) {
	executablePath, err := os.Executable()
	if err != nil {
		return install.Source{}, err
	}
	return install.Source{
		Payload:        payload,
		Bundle:         bundle,
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
