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

func validateDestination(destination string, payload Payload, goos string) (string, error) {
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
	if !equal(filepath.Base(clean), payload.directory) || !equal(filepath.Base(filepath.Dir(clean)), payload.parent) {
		return "", payload.destinationError()
	}
	// An empty grandparent set is not a laxer check by accident. It says the
	// payload's outer directory is Conductor's to choose, because no vendor
	// convention governs it -- so there is nothing to validate against and
	// inventing one would only reject legitimate placements.
	if len(payload.grandparents) > 0 {
		grandparent := filepath.Base(filepath.Dir(filepath.Dir(clean)))
		matched := false
		for _, candidate := range payload.grandparents {
			if equal(grandparent, candidate) {
				matched = true
				break
			}
		}
		if !matched {
			return "", payload.destinationError()
		}
	}
	return clean, nil
}

func (p Payload) destinationError() error {
	suffix := p.parent + "/" + p.directory
	if len(p.grandparents) == 0 {
		return fmt.Errorf("destination must end in %s", suffix)
	}
	return fmt.Errorf("destination must end in <vendor-dir>/%s (vendor-dir one of %s)", suffix, strings.Join(p.grandparents, ", "))
}

// executableRelativePath is where this payload's copy of the Conductor binary
// lives inside the installed tree. The adapter's is named by its hook
// registrations, which resolve `${CLAUDE_PLUGIN_ROOT}/bin/conductor`; the
// skill's is named by the skill's own instructions.
func (p Payload) executableRelativePath(goos string) string {
	name := "/conductor"
	if goos == "windows" {
		name += ".exe"
	}
	return p.executableDir + name
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
	existing, manifestData, err := verifyInstallation(destination, expected.Executable)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			empty, emptyErr := isDirEmpty(destination)
			if emptyErr == nil && !empty {
				userHome, homeErr := os.UserHomeDir()
				if homeErr != nil {
					return nil, fmt.Errorf("install preflight: failed to get user home directory for backup: %w", homeErr)
				}
				// Grouped by the directory the payload was being installed
				// into -- "skills", "adapters" -- rather than by a fixed name,
				// so a backup taken during an adapter install is not filed
				// under a word that describes something else.
				backupDirectory := filepath.Join(userHome, ".conductor", "backups", filepath.Base(filepath.Dir(destination)))
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
