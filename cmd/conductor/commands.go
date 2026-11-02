package main

import (
	"errors"
	"fmt"
	"os"
	"runtime/debug"
	"strings"

	"github.com/danlavee/Conductor/internal/install"
	conductor "github.com/danlavee/Conductor/internal/state"
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
		if len(args) < 2 {
			return installUsageError()
		}
		replace, err := replaceRequested(args[2:])
		if err != nil {
			return err
		}
		if skillPayload.Check(args[1]) != nil {
			return installUsageError()
		}
		return runInstall(skillPayload, args[1], replace)
	case "verify":
		if len(args) != 2 {
			return errors.New("usage: conductor verify <absolute-skill-directory>")
		}
		source, err := installationSource(skillPayload)
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

// skillPayload is placed where a host looks for skills; the skill's own
// instructions name scripts/ as where its executable sits.
var skillPayload = install.Payload{Bundle: skillbundle.Files, Parent: "skills", Name: "conductor", ExecutableDir: "scripts"}

func runInstall(payload install.Payload, destination string, replace bool) error {
	source, err := installationSource(payload)
	if err != nil {
		return err
	}
	result, err := install.Install(destination, source, replace)
	if err != nil {
		return err
	}
	return conductor.WriteJSON(os.Stdout, result)
}

func installationSource(payload install.Payload) (install.Source, error) {
	executable, err := os.Executable()
	if err != nil {
		return install.Source{}, err
	}
	return install.Source{Payload: payload, Executable: executable, Version: currentVersion(), Protocol: conductor.CurrentProtocolVersion}, nil
}

func currentVersion() string {
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" {
		return info.Main.Version
	}
	return "(devel)"
}
