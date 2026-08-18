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

// BindingSource names which binding answered, so an agent looking at a wrong
// identity knows which file to correct.
type BindingSource string

const (
	// FromSession is a binding an agent made for its own session, and wins as
	// the more specific claim.
	FromSession BindingSource = "session"

	// FromProject is the project's IdentityFile, the ordinary case.
	FromProject BindingSource = "project"

	// NoBinding is reported when neither answered. It shares its wire value
	// with the Outcome of the same meaning.
	NoBinding BindingSource = "unbound"
)

// Binding is the identity a hook is acting for, and where it came from. An
// empty Agent is a session that opted out, which is silence, not failure.
type Binding struct {
	Agent  string
	Source BindingSource
}

// sessionBinding is one session's claim on an identity. BoundAt tells a stale
// record from a live one, since these outlive any session teardown missed.
type sessionBinding struct {
	Agent   string    `json:"agent"`
	BoundAt time.Time `json:"bound_at"`
}

// ValidateSession rejects a session identifier that cannot be trusted to name
// one session. Callers name the origin in the wrapping error, since the two
// ways a session arrives fail differently.
//
// The identifier becomes a filename, so the character set is not stylistic.
// Rejecting beats sanitizing: one that needed rewriting to be safe is not one
// the host issued.
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

// Resolution is what each binding said, before precedence picks between them.
// The winner alone would hide the act that matters: once a session binding
// lands, the name the project gave is visible nowhere, so an agent could not
// see that it bound over an assigned identity.
type Resolution struct {
	Session string
	Project string
}

// Binding applies precedence.
func (r Resolution) Binding() Binding {
	if r.Session != "" {
		return Binding{Agent: r.Session, Source: FromSession}
	}
	if r.Project != "" {
		return Binding{Agent: r.Project, Source: FromProject}
	}
	return Binding{}
}

// Resolve reads both bindings.
func Resolve(root, session, projectDir string) (Resolution, error) {
	var resolution Resolution
	var err error
	if resolution.Session, err = SessionIdentity(root, session); err != nil {
		return Resolution{}, err
	}
	if strings.TrimSpace(projectDir) == "" {
		return resolution, nil
	}
	if resolution.Project, err = ResolveIdentity(projectDir); err != nil {
		return Resolution{}, err
	}
	return resolution, nil
}

// ResolveBinding answers which identity this session acts for, stopping at the
// session binding when one answers.
//
// That is not Resolve with a field ignored. arm and release run at every turn
// boundary and must not acquire a failure they had no use for: an unreadable
// project file would otherwise stop delivery for a session that had said who
// it is. Reporting is where reading both is worth a failure; delivering is not.
func ResolveBinding(root, session, projectDir string) (Binding, error) {
	agent, err := SessionIdentity(root, session)
	if err != nil {
		return Binding{}, err
	}
	if agent != "" {
		return Binding{Agent: agent, Source: FromSession}, nil
	}
	if strings.TrimSpace(projectDir) == "" {
		return Binding{}, nil
	}
	if agent, err = ResolveIdentity(projectDir); err != nil {
		return Binding{}, err
	}
	return Resolution{Project: agent}.Binding(), nil
}

// SessionIdentity reads the identity a session bound for itself. Most sessions
// have none and fall through to the project's, so absence is not an error.
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

// BindSession records that this session acts as agent, returning whatever it
// was bound to before so a replaced binding is visible rather than swallowed.
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

// UnbindSession forgets a session's binding. Teardown runs it for every
// session, including those that never bound one, so absence is success.
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

// adapterDirectory holds state the protocol has no concept of: which session
// holds a stream, and which identity a session speaks for. It hangs off the
// control directory because both must outlive a cutover.
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
