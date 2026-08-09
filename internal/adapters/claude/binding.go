package claude

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/danlavee/Conductor/internal/cutover"
	"github.com/danlavee/Conductor/internal/platform"
)

// BindingSource names which of the two bindings answered. It is reported rather
// than inferred because the two are resolved in a fixed order, and an agent
// looking at a wrong identity needs to know which file to correct.
type BindingSource string

const (
	// FromSession is a binding an agent made for its own session. It wins,
	// because it is the more specific of the two: a session that has said who
	// it is has said so about itself, while a project binding speaks for every
	// session that happens to open there.
	FromSession BindingSource = "session"

	// FromProject is the project's `.conductor-agent` file, which is the
	// binding for a directory with one agent in it -- the ordinary case, and
	// the one that needs no action from the model at all.
	FromProject BindingSource = "project"
)

// Binding is the identity a hook is acting for, and where it came from.
// An empty Agent means the adapter is installed and this session opted out;
// that is a silence, not a failure.
type Binding struct {
	Agent  string
	Source BindingSource
}

// sessionBinding is one session's claim on an identity. BoundAt is recorded
// for the human reading the directory: these files outlive the session that
// wrote one whenever teardown does not run, and a timestamp is what tells a
// stale record from a live one.
type sessionBinding struct {
	Agent   string    `json:"agent"`
	BoundAt time.Time `json:"bound_at"`
}

// ValidateSession rejects a session identifier that cannot be trusted to name
// one session. The caller supplies the origin, because the two ways a session
// arrives fail differently and an error naming the wrong one sends the reader
// to the wrong place.
//
// The character set is not stylistic: the identifier becomes a filename, so a
// separator or a parent reference in it would write outside the directory this
// adapter owns. Rejecting is right rather than sanitizing -- an identifier that
// needed rewriting to be safe is not one the host issued.
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

// ResolveBinding answers which identity this session is acting for. The session
// binding is consulted first and, when it answers, the project is not consulted
// at all -- including its absence. That is what lets several agents work in one
// directory: each has said who it is, and none of them needs the directory to
// agree.
func ResolveBinding(root, session, projectDir string) (Binding, error) {
	agent, err := SessionIdentity(root, session)
	if err != nil {
		return Binding{}, err
	}
	if agent != "" {
		return Binding{Agent: agent, Source: FromSession}, nil
	}
	agent, err = ResolveIdentity(projectDir)
	if err != nil {
		return Binding{}, err
	}
	if agent == "" {
		return Binding{}, nil
	}
	return Binding{Agent: agent, Source: FromProject}, nil
}

// SessionIdentity reads the identity a session bound for itself. An absent
// binding is not an error: most sessions have none and fall through to the
// project's.
func SessionIdentity(root, session string) (string, error) {
	if err := ValidateSession(session); err != nil {
		return "", err
	}
	path, err := sessionBindingPath(root, session)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var binding sessionBinding
	if err := json.Unmarshal(data, &binding); err != nil {
		return "", err
	}
	return strings.TrimSpace(binding.Agent), nil
}

// BindSession records that this session acts as agent, and reports whatever it
// was bound to before. Rebinding is allowed and the previous value is returned
// rather than swallowed: an agent that meant to bind a fresh session and
// instead replaced a live one has to be able to see that it did.
func BindSession(root, session, agent string) (string, error) {
	if err := ValidateSession(session); err != nil {
		return "", err
	}
	path, err := sessionBindingPath(root, session)
	if err != nil {
		return "", err
	}
	previous, err := SessionIdentity(root, session)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	data, err := json.Marshal(sessionBinding{Agent: agent, BoundAt: time.Now().UTC()})
	if err != nil {
		return "", err
	}
	return previous, writeAtomic(path, data)
}

// UnbindSession forgets a session's binding. It runs at teardown for every
// session, including the ones that never bound anything, so an absent binding
// is success.
func UnbindSession(root, session string) error {
	if err := ValidateSession(session); err != nil {
		return err
	}
	path, err := sessionBindingPath(root, session)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// adapterDirectory is where this adapter keeps the state the protocol has no
// concept of -- which session holds a stream, and which identity a session
// speaks for. It hangs off the control directory rather than the root because
// both facts have to outlive a cutover: the sessions carrying them keep running
// while the root underneath is replaced.
func adapterDirectory(root string) (string, error) {
	controlDir, err := cutover.Directory(root)
	if err != nil {
		return "", err
	}
	return filepath.Join(controlDir, "adapters", "claude"), nil
}

func sessionBindingPath(root, session string) (string, error) {
	directory, err := adapterDirectory(root)
	if err != nil {
		return "", err
	}
	return filepath.Join(directory, "sessions", session+".json"), nil
}

// writeAtomic publishes by rename, so a reader never sees a partial record.
func writeAtomic(path string, data []byte) error {
	directory := filepath.Dir(path)
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
