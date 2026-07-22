package state

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/danlavee/Conductor/internal/platform"
)

// CurrentProtocolVersion is the one on-disk state protocol this binary supports.
const CurrentProtocolVersion = 2

const (
	protocolFileName = "protocol.json"
	protocolInitDir  = ".protocol-init"
	protocolGuard    = "guard"
	protocolTempBase = "protocol-"
	protocolTempExt  = ".tmp"
)

type protocolDocument struct {
	Version *int `json:"version"`
}

func initializeProtocol(home string) (err error) {
	if exists, err := validateProtocolDuringInitialization(home); exists || err != nil {
		return err
	}
	candidate, err := undeclaredRootIsEmpty(home)
	if err != nil {
		return err
	}
	if !candidate {
		if exists, validateErr := validateProtocolDuringInitialization(home); exists || validateErr != nil {
			return validateErr
		}
		return protocolMismatch("state root contains unversioned data", nil)
	}

	supportDir := filepath.Join(home, protocolInitDir)
	if err := os.MkdirAll(supportDir, 0o700); err != nil {
		return err
	}
	if err := validateInitSupport(supportDir); err != nil {
		return err
	}
	release, err := platform.AcquireFileMutex(filepath.Join(supportDir, protocolGuard), defaultLockTimeout)
	if err != nil {
		if errors.Is(err, platform.ErrMutexTimeout) {
			return &ProtocolError{Code: "TIMEOUT", Text: "protocol initialization did not become available"}
		}
		return err
	}
	defer func() {
		if releaseErr := release(); err == nil && releaseErr != nil {
			err = releaseErr
		}
	}()

	if exists, err := validateProtocolDuringInitialization(home); exists || err != nil {
		return err
	}
	candidate, err = undeclaredRootIsEmpty(home)
	if err != nil {
		return err
	}
	if !candidate {
		if exists, validateErr := validateProtocolDuringInitialization(home); exists || validateErr != nil {
			return validateErr
		}
		return protocolMismatch("state root contains unversioned data", nil)
	}
	if err := validateInitSupport(supportDir); err != nil {
		return err
	}
	if err := removeProtocolTemporaries(supportDir); err != nil {
		return err
	}
	version := CurrentProtocolVersion
	return writeJSONAtomicFrom(
		supportDir,
		protocolTempBase+"*"+protocolTempExt,
		filepath.Join(home, protocolFileName),
		protocolDocument{Version: &version},
	)
}

func validateProtocolDuringInitialization(home string) (bool, error) {
	deadline := time.Now().Add(defaultLockTimeout)
	for {
		exists, err := validateExistingProtocol(home)
		if !platform.IsTransientFileAccess(err) || time.Now().After(deadline) {
			return exists, err
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (c *Client) validateProtocol() error {
	exists, err := validateExistingProtocol(c.Home)
	if err != nil {
		return err
	}
	if !exists {
		return protocolMismatch("state protocol declaration is missing", nil)
	}
	return nil
}

func validateExistingProtocol(home string) (bool, error) {
	path := filepath.Join(home, protocolFileName)
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return true, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return true, protocolMismatch("state protocol declaration must be a regular file", nil)
	}
	var document protocolDocument
	if err := readJSON(path, &document); err != nil {
		var pathErr *os.PathError
		if errors.As(err, &pathErr) {
			return true, err
		}
		return true, protocolMismatch("state protocol declaration is malformed", nil)
	}
	if document.Version == nil {
		return true, protocolMismatch("state protocol declaration has no version", nil)
	}
	if *document.Version != CurrentProtocolVersion {
		return true, protocolMismatch("state protocol is unsupported", document.Version)
	}
	return true, nil
}

func undeclaredRootIsEmpty(home string) (bool, error) {
	info, err := os.Lstat(home)
	if errors.Is(err, os.ErrNotExist) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return false, protocolMismatch("state root must be a regular directory", nil)
	}
	entries, err := os.ReadDir(home)
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		switch entry.Name() {
		case protocolFileName:
			return false, nil
		case protocolInitDir:
			if err := validateInitSupport(filepath.Join(home, protocolInitDir)); err != nil {
				return false, err
			}
		default:
			return false, nil
		}
	}
	return true, nil
}

func validateInitSupport(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return protocolMismatch("protocol initialization support is invalid", nil)
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Name() != protocolGuard && !isProtocolTemporary(entry.Name()) {
			return protocolMismatch("protocol initialization support contains an unknown entry", nil)
		}
		entryInfo, err := os.Lstat(filepath.Join(path, entry.Name()))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if !entryInfo.Mode().IsRegular() || entryInfo.Mode()&os.ModeSymlink != 0 {
			return protocolMismatch("protocol initialization support contains a non-regular entry", nil)
		}
	}
	return nil
}

func removeProtocolTemporaries(path string) error {
	entries, err := os.ReadDir(path)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !isProtocolTemporary(entry.Name()) {
			continue
		}
		if err := os.Remove(filepath.Join(path, entry.Name())); err != nil {
			return err
		}
	}
	return platform.SyncParent(filepath.Join(path, protocolGuard))
}

func isProtocolTemporary(name string) bool {
	return strings.HasPrefix(name, protocolTempBase) && strings.HasSuffix(name, protocolTempExt) && len(name) > len(protocolTempBase)+len(protocolTempExt)
}

func protocolMismatch(message string, found *int) *ProtocolError {
	detail := &ProtocolVersionDetail{Supported: CurrentProtocolVersion}
	if found != nil {
		value := *found
		detail.Found = &value
	}
	return &ProtocolError{Code: "PROTOCOL_MISMATCH", Text: message, Protocol: detail}
}

func protocolPath(home string) string {
	return filepath.Join(home, protocolFileName)
}

func protocolInitPath(home string) string {
	return filepath.Join(home, protocolInitDir)
}
