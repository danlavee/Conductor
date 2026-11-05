// Package binding stores the identity a host session speaks for. A session's
// file exists once its adapter has served it, and holds the agent name once
// the agent joins from inside it.
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

// Serve records that the adapter's session-start hook ran for session, which
// is what lets a join inside it hand the session to the adapter. An identity
// already bound -- a resumed or compacted session -- stays bound.
func Serve(root, adapter, session string) error {
	path, err := sessionPath(root, adapter, session)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if errors.Is(err, os.ErrExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return file.Close()
}

// Served reports whether the adapter has served session.
func Served(root, adapter, session string) (bool, error) {
	path, err := sessionPath(root, adapter, session)
	if err != nil {
		return false, err
	}
	_, err = os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

// Bind records that this session acts as agent.
func Bind(root, adapter, session, agent string) error {
	path, err := sessionPath(root, adapter, session)
	if err != nil {
		return err
	}
	return WriteAtomic(path, []byte(agent))
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

// WriteAtomic publishes by rename, so a reader never sees a partial file.
func WriteAtomic(path string, data []byte) error {
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
