// Package install places Conductor's embedded skill and native executable atomically.
package install

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/danlavee/Conductor/internal/skillcheck"
)

const (
	manifestName   = ".conductor-manifest.json"
	manifestFormat = 2
)

var errDestinationExists = errors.New("installation destination already exists")

// Source identifies the exact bundle and executable being installed.
type Source struct {
	Bundle         fs.FS
	ExecutablePath string
	Version        string
	Protocol       int
	GOOS           string
	GOARCH         string
	SmokeCheck     bool
}

// Result is the deterministic install response written by the CLI.
type Result struct {
	Status       string `json:"status"`
	Version      string `json:"version"`
	Protocol     int    `json:"protocol"`
	SkillPath    string `json:"skill_path"`
	BinaryPath   string `json:"binary_path"`
	FileCount    int    `json:"file_count"`
	ManifestHash string `json:"manifest_hash"`
}

// CurrencyResult reports whether destination's installed content matches
// what Install(destination, source) would produce, without writing anything.
type CurrencyResult struct {
	Status           string `json:"status"` // "current" | "outdated" | "unknown" | "not-installed"
	InstalledVersion string `json:"installed_version,omitempty"`
	SourceVersion    string `json:"source_version"`
	Detail           string `json:"detail,omitempty"`
}

// Install validates, stages, verifies, and publishes one exact skill directory.
func Install(destination string, source Source) (Result, error) {
	cleanDestination, files, expected, manifestData, err := preflight(destination, source)
	if err != nil {
		return Result{}, fmt.Errorf("install preflight: %w", err)
	}
	if existingResult, err := prepareDestination(cleanDestination, expected); err != nil {
		return Result{}, err
	} else if existingResult != nil {
		return *existingResult, nil
	}

	parent := filepath.Dir(cleanDestination)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return Result{}, fmt.Errorf("install stage: create skills directory: %w", err)
	}
	stage, err := os.MkdirTemp(filepath.Dir(parent), ".conductor-install-*")
	if err != nil {
		return Result{}, fmt.Errorf("install stage: %w", err)
	}
	defer os.RemoveAll(stage)

	if err := writeInstallation(stage, files, manifestData); err != nil {
		return Result{}, fmt.Errorf("install stage: %w", err)
	}
	if problems := skillcheck.ValidateSkill(os.DirFS(stage)); len(problems) > 0 {
		return Result{}, fmt.Errorf("install verify: invalid staged skill: %s", strings.Join(problems, "; "))
	}
	if _, _, err := verifyInstallation(stage); err != nil {
		return Result{}, fmt.Errorf("install verify: %w", err)
	}
	if source.SmokeCheck {
		if err := smokeCheck(filepath.Join(stage, filepath.FromSlash(expected.Executable)), expected.Version, expected.Protocol); err != nil {
			return Result{}, fmt.Errorf("install verify: %w", err)
		}
	}
	if err := publishNoReplace(stage, cleanDestination); err != nil {
		if existingResult, inspectErr := prepareDestination(cleanDestination, expected); inspectErr != nil {
			return Result{}, inspectErr
		} else if existingResult != nil {
			return *existingResult, nil
		}
		return Result{}, fmt.Errorf("install publish: %w", err)
	}
	return resultFor("installed", cleanDestination, expected, manifestData), nil
}

// ValidateDestination checks the native host's install-path contract without writing.
func ValidateDestination(destination string) error {
	_, err := validateDestination(destination, runtime.GOOS)
	return err
}

// CheckCurrency compares destination's installed manifest against source
// without touching disk at destination.
func CheckCurrency(destination string, source Source) (CurrencyResult, error) {
	cleanDestination, _, expected, _, err := preflight(destination, source)
	if err != nil {
		return CurrencyResult{}, fmt.Errorf("check currency preflight: %w", err)
	}
	if _, err := os.Stat(cleanDestination); errors.Is(err, os.ErrNotExist) {
		return CurrencyResult{Status: "not-installed", SourceVersion: expected.Version}, nil
	}
	existing, _, err := verifyInstallation(cleanDestination)
	if err != nil {
		return CurrencyResult{Status: "unknown", SourceVersion: expected.Version, Detail: err.Error()}, nil
	}
	if existing.DistributionID == expected.DistributionID {
		return CurrencyResult{Status: "current", InstalledVersion: existing.Version, SourceVersion: expected.Version}, nil
	}
	return CurrencyResult{
		Status:           "outdated",
		InstalledVersion: existing.Version,
		SourceVersion:    expected.Version,
		Detail:           "installed content differs from source",
	}, nil
}
