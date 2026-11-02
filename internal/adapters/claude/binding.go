package claude

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/danlavee/Conductor/internal/platform"
)

// IdentityFile binds a project to a Conductor identity.
const IdentityFile = ".conductor-agent"

// byteOrderMark is U+FEFF, spelled as bytes because Go rejects the character
// outside a file's first position. Notepad and PowerShell's Set-Content
// -Encoding utf8 both write one, and TrimSpace does not remove it.
const byteOrderMark = "\xef\xbb\xbf"

// Source names which binding answered, so an agent looking at a wrong identity
// knows which one to correct.
type Source string

const (
	// FromSession is a binding an agent made for its own session, and wins as
	// the more specific claim.
	FromSession Source = "session"
	// FromProject is the project's IdentityFile, the ordinary case.
	FromProject Source = "project"
	// NoBinding is reported when neither answered.
	NoBinding Source = "unbound"
)

// Binding is the identity a session acts for, and where it came from. An empty
// Agent means nothing binds the session, which is silence, not failure.
type Binding struct {
	Agent  string `json:"agent,omitempty"`
	Source Source `json:"source"`
}

// ValidateSession rejects a session identifier that cannot be trusted to name
// one session. The identifier becomes a filename, and one that needed
// rewriting to be safe is not one the host issued.
func ValidateSession(session string) error {
	if strings.TrimSpace(session) == "" {
		return errors.New("no session identifier")
	}
	if session == SessionPlaceholder {
		return errors.New("session identifier is the literal " + SessionPlaceholder + ", so the host did not substitute it")
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

// ResolveBinding answers which identity this session acts for: its own
// binding when it made one, otherwise the project's IdentityFile.
func ResolveBinding(root, session, projectDir string) (Binding, error) {
	path, err := sessionPath(root, session)
	if err != nil {
		return Binding{}, err
	}
	agent, err := readName(path)
	if err != nil || agent != "" {
		return Binding{Agent: agent, Source: FromSession}, err
	}
	if agent, err = readName(filepath.Join(projectDir, IdentityFile)); err != nil || agent != "" {
		return Binding{Agent: agent, Source: FromProject}, err
	}
	return Binding{Source: NoBinding}, nil
}

// BindSession records that this session acts as agent, outranking the
// project's IdentityFile for this session alone.
func BindSession(root, session, agent string) error {
	path, err := sessionPath(root, session)
	if err != nil {
		return err
	}
	return writeAtomic(path, []byte(agent))
}

// ForgetSession drops a session's binding. Teardown runs it for every session,
// so absence is success.
func ForgetSession(root, session string) error {
	path, err := sessionPath(root, session)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// readName reads an identity file, where absence is silence rather than an
// error: most projects and sessions name no identity.
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

// adapterDirectory holds state the protocol has no concept of: which session
// holds a stream, and which identity a session speaks for.
func adapterDirectory(root string) string {
	return filepath.Join(root, "adapters", "claude")
}

func sessionPath(root, session string) (string, error) {
	if err := ValidateSession(session); err != nil {
		return "", err
	}
	return filepath.Join(adapterDirectory(root), "sessions", session), nil
}

// writeAtomic publishes by rename, so a reader never sees a partial record --
// which the stream's poller would otherwise mistake for a release.
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
