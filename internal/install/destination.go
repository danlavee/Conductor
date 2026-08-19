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

// replacement is permission to overwrite the installation already at the
// destination, carrying what it was so the result can name what it replaced.
// Nothing is displaced when this is issued: the move happens once the incoming
// tree is staged and verified, so a rejected candidate leaves the working
// installation exactly where it was.
type replacement struct {
	retired         string
	previousVersion string
}

// retire moves an installed tree off the destination and reports where it went.
// Both callers displace to the same place under the same naming -- an upgrade,
// and the safety net for a tree carrying no manifest at all -- so an operator
// has one location to look in rather than one per reason.
func retire(destination string) (string, error) {
	userHome, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get user home directory for backup: %w", err)
	}
	// Grouped by the directory the payload was being installed into --
	// "skills", "adapters" -- rather than by a fixed name, so a backup taken
	// during an adapter install is not filed under a word that describes
	// something else.
	backupDirectory := filepath.Join(userHome, ".conductor", "backups", filepath.Base(filepath.Dir(destination)))
	if err := os.MkdirAll(backupDirectory, 0o700); err != nil {
		return "", fmt.Errorf("failed to create backup directory: %w", err)
	}
	backupPath := filepath.Join(backupDirectory, filepath.Base(destination)+".pre-upgrade-"+time.Now().Format("20060102150405"))
	if err := os.Rename(destination, backupPath); err != nil {
		return "", fmt.Errorf("failed to move %s aside: %w", destination, err)
	}
	return backupPath, nil
}

// prepareDestination classifies what already occupies destination. Only two of
// its outcomes let the install proceed: nothing is there, or what is there was
// moved aside first.
func prepareDestination(destination string, expected manifest, replace bool) (*Result, *replacement, error) {
	info, err := os.Lstat(destination)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("install preflight: inspect destination: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, nil, errors.New("install conflict: destination is a symbolic link")
	}
	if !info.IsDir() {
		return nil, nil, errors.New("install conflict: destination is not a directory")
	}
	// A successful verification is also what proves the destination holds this
	// payload rather than the other one: the two disagree about where the
	// executable lives, so an adapter tree fails a skill's verification before
	// any version is compared.
	existing, manifestData, err := verifyInstallation(destination, expected.Executable)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			empty, emptyErr := isDirEmpty(destination)
			if emptyErr == nil && !empty {
				backupPath, retireErr := retire(destination)
				if retireErr != nil {
					return nil, nil, fmt.Errorf("install preflight: %w", retireErr)
				}
				log.Printf("Warning: Destination %s had no install manifest. Its contents were moved to %s.", destination, backupPath)
				return nil, nil, nil
			}
		}
		return nil, nil, fmt.Errorf("install conflict: %w", err)
	}
	if existing.DistributionID == expected.DistributionID {
		result := resultFor("already-installed", destination, existing, manifestData)
		return &result, nil, nil
	}
	if !replace {
		return nil, nil, fmt.Errorf("install conflict: destination holds Conductor %s and this is %s; replacing an installation is a separate operation", existing.Version, expected.Version)
	}
	// Refused rather than handled. Only the state roots this installation
	// serves can be migrated across a protocol change, and the installer knows
	// none of them -- replacing the binary would leave that work undone while
	// looking like it had succeeded.
	if existing.Protocol != expected.Protocol {
		return nil, nil, fmt.Errorf("install conflict: installed state protocol %d differs from %d; migrate every state root this installation serves first", existing.Protocol, expected.Protocol)
	}
	return nil, &replacement{previousVersion: existing.Version}, nil
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
