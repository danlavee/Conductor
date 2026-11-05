// Package binding stores the identity a host session speaks for.
// The file is the agent name alone, which is the shape Claude already uses.
package binding

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/danlavee/Conductor/internal/platform"
)

const byteOrderMark = "\xef\xbb\xbf"

// Validate rejects a session id that is empty, unsubstituted, or unsafe as a filename.
func Validate(session, placeholder string) error {
	if strings.TrimSpace(session) == "" {
		return errors.New("no session identifier")
	}
	if placeholder != "" && session == placeholder {
		return errors.New("session identifier is the literal " + placeholder + ", so the host did not substitute it")
	}
	for _, r := range session {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-', r == '_':
		default:
			return errors.New("session identifier contains an unusable character: " + string(r))
		}
	}
	return nil
}

// Directory is adapters/<name> beside the state root's other durable files.
func Directory(root, adapter string) string {
	return filepath.Join(root, "adapters", adapter)
}

// Identity reads the agent this session is bound to. Absence is an empty name.
func Identity(root, adapter, session string) (string, error) {
	path, err := sessionPath(root, adapter, session)
	if err != nil {
		return "", err
	}
	return readName(path)
}

// Bind records that this session acts as agent.
func Bind(root, adapter, session, agent string) error {
	path, err := sessionPath(root, adapter, session)
	if err != nil {
		return err
	}
	return writeAtomic(path, []byte(agent))
}

// Forget removes a session binding. Absence is success.
func Forget(root, adapter, session string) error {
	path, err := sessionPath(root, adapter, session)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func sessionPath(root, adapter, session string) (string, error) {
	if err := Validate(session, ""); err != nil {
		return "", err
	}
	return filepath.Join(Directory(root, adapter), "sessions", session), nil
}

func readName(path string) (string, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(strings.TrimPrefix(string(data), byteOrderMark)), nil
}

func writeAtomic(path string, data []byte) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(directory, ".conductor-*.tmp")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return platform.ReplaceFile(tempPath, path)
}
