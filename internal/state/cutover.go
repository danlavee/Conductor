package state

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/danlavee/Conductor/internal/cutover"
	"github.com/danlavee/Conductor/internal/platform"
)

// ValidateCutoverPreflight inspects a root after operation admission has
// closed. It performs no root writes. Active transactions make a root
// nondurable for cutover, and an active legacy watcher proves that seamless
// replacement cannot be honored by every watcher.
func ValidateCutoverPreflight(root string) error {
	var protocol struct {
		Version int `json:"version"`
	}
	if err := readJSON(filepath.Join(root, "protocol.json"), &protocol); err != nil {
		return fmt.Errorf("cutover source protocol declaration: %w", err)
	}
	if protocol.Version <= 0 {
		return errors.New("cutover source protocol version must be positive")
	}
	transactions, err := os.ReadDir(filepath.Join(root, "transactions"))
	if err != nil {
		return err
	}
	for _, entry := range transactions {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".json") {
			return fmt.Errorf("cutover requires transaction %s to be committed or aborted", strings.TrimSuffix(entry.Name(), ".json"))
		}
	}
	agents, err := registeredAgentNames(root)
	if err != nil {
		return err
	}
	controlDir, err := cutover.Directory(root)
	if err != nil {
		return err
	}
	for _, agent := range agents {
		legacyGuard := filepath.Join(root, "state", "watch", agent+".guard")
		if _, err := os.Lstat(legacyGuard); err == nil {
			release, lockErr := platform.AcquireFileMutex(legacyGuard, 0)
			if errors.Is(lockErr, platform.ErrMutexTimeout) {
				return fmt.Errorf("active watcher %q lacks seamless cutover capability", agent)
			}
			if lockErr != nil {
				return lockErr
			}
			_ = release()
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}

		guard := filepath.Join(controlDir, "watch", agent+".guard")
		if _, err := os.Lstat(guard); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return err
		}
		release, lockErr := platform.AcquireFileMutex(guard, 0)
		if lockErr == nil {
			_ = release()
			continue
		}
		if !errors.Is(lockErr, platform.ErrMutexTimeout) {
			return lockErr
		}
		var owner watchOwner
		if err := readJSON(filepath.Join(controlDir, "watch", agent+".owner.json"), &owner); err != nil {
			return fmt.Errorf("active watcher %q has no valid capability metadata: %w", agent, err)
		}
		if owner.Capability != cutover.Capability {
			return fmt.Errorf("active watcher %q has cutover capability %d, need %d", agent, owner.Capability, cutover.Capability)
		}
		if strings.TrimSpace(owner.Build) == "" || owner.Protocol <= 0 {
			return fmt.Errorf("active watcher %q has incomplete build metadata", agent)
		}
	}
	return nil
}

// ValidateReplacementRoot checks the minimum root contract before replacement
// is announced to old watchers. It is intentionally read-only and bypasses
// normal admission because replacement validation occurs while frozen.
func ValidateReplacementRoot(root string) error {
	exists, err := validateExistingProtocol(root)
	if err != nil {
		return err
	}
	if !exists {
		return errors.New("replacement protocol declaration is missing")
	}
	for _, directory := range []string{"registry", "topics", "subscriptions", "locks", "inbox", "events", "cursors", "transactions", "state"} {
		info, err := os.Stat(filepath.Join(root, directory))
		if err != nil {
			return fmt.Errorf("replacement state directory %s: %w", directory, err)
		}
		if !info.IsDir() {
			return fmt.Errorf("replacement state path %s is not a directory", directory)
		}
	}
	return nil
}

func registeredAgentNames(root string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(root, "registry"))
	if err != nil {
		return nil, err
	}
	var names []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".json") {
			names = append(names, strings.TrimSuffix(entry.Name(), ".json"))
		}
	}
	return names, nil
}
