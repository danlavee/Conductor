package install

import (
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// vendorSkillDirs lists every known vendor skills root Conductor may be
// installed under. .agents is Conductor's own cross-vendor convention (see
// docs/installation.md); .claude is Claude Code's actual one, which doesn't
// follow it. Extend this as other vendors' real conventions are confirmed --
// never guess one in.
var vendorSkillDirs = []string{".agents", ".claude"}

func validateDestination(destination, goos string) (string, error) {
	if goos != "windows" && goos != "linux" {
		return "", fmt.Errorf("unsupported operating system %q", goos)
	}
	if strings.TrimSpace(destination) == "" {
		return "", errors.New("destination is required")
	}
	if !filepath.IsAbs(destination) {
		return "", errors.New("destination must be an absolute path")
	}
	clean := filepath.Clean(destination)
	if goos == "windows" && isUNCVolume(filepath.VolumeName(clean)) {
		return "", errors.New("UNC destinations are unsupported")
	}
	equal := func(a, b string) bool {
		if goos == "windows" {
			return strings.EqualFold(a, b)
		}
		return a == b
	}
	vendorDir := filepath.Base(filepath.Dir(filepath.Dir(clean)))
	skillsDir := filepath.Base(filepath.Dir(clean))
	conductorDir := filepath.Base(clean)
	vendorMatches := false
	for _, candidate := range vendorSkillDirs {
		if equal(vendorDir, candidate) {
			vendorMatches = true
			break
		}
	}
	if !vendorMatches || !equal(skillsDir, "skills") || !equal(conductorDir, "conductor") {
		return "", fmt.Errorf("destination must end in <vendor-dir>/skills/conductor (vendor-dir one of %s)", strings.Join(vendorSkillDirs, ", "))
	}
	return clean, nil
}

func isUNCVolume(volume string) bool {
	lower := strings.ToLower(volume)
	if strings.HasPrefix(lower, `\\?\unc\`) {
		return true
	}
	return strings.HasPrefix(volume, `\\`) && !strings.HasPrefix(lower, `\\?\`)
}

func prepareDestination(destination string, expected manifest) (*Result, error) {
	info, err := os.Lstat(destination)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("install preflight: inspect destination: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("install conflict: destination is a symbolic link")
	}
	if !info.IsDir() {
		return nil, errors.New("install conflict: destination is not a directory")
	}
	existing, manifestData, err := verifyInstallation(destination)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			empty, emptyErr := isDirEmpty(destination)
			if emptyErr == nil && !empty {
				userHome, homeErr := os.UserHomeDir()
				if homeErr != nil {
					return nil, fmt.Errorf("install preflight: failed to get user home directory for backup: %w", homeErr)
				}
				backupDirectory := filepath.Join(userHome, ".conductor", "backups", "skills")
				if err := os.MkdirAll(backupDirectory, 0o700); err != nil {
					return nil, fmt.Errorf("install preflight: failed to create backup directory: %w", err)
				}
				backupPath := filepath.Join(backupDirectory, filepath.Base(destination)+".pre-upgrade-"+time.Now().Format("20060102150405"))
				log.Printf("Warning: Destination %s has no install manifest. Creating a safety-net backup at %s and overwriting...", destination, backupPath)
				if renameErr := os.Rename(destination, backupPath); renameErr != nil {
					return nil, fmt.Errorf("install preflight: failed to backup conflicting destination: %w", renameErr)
				}
				return nil, nil
			}
		}
		return nil, fmt.Errorf("install conflict: %w", err)
	}
	if existing.DistributionID != expected.DistributionID {
		return nil, errors.New("install conflict: destination contains a different Conductor installation")
	}
	result := resultFor("already-installed", destination, existing, manifestData)
	return &result, nil
}

func isDirEmpty(name string) (bool, error) {
	f, err := os.Open(name)
	if err != nil {
		return false, err
	}
	defer f.Close()

	_, err = f.Readdirnames(1)
	if err == io.EOF {
		return true, nil
	}
	return false, err
}
