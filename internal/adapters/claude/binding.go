package claude

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

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

// sessionRecord is everything the adapter knows about one host session: that
// it served the session at all, and which identity that session claims. The
// two facts share a lifetime and one teardown, so they share a record -- and
// each writer carries the other's field forward, because SessionStart runs
// again on resume, compact and fork, over a binding the model made in between.
//
// ServedAt is written only by the hook. BoundAt tells a stale binding from a
// live one, since these outlive any session teardown missed.
type sessionRecord struct {
	ServedAt time.Time `json:"served_at"`
	Agent    string    `json:"agent,omitempty"`
	BoundAt  time.Time `json:"bound_at"`
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
	record, err := readSession(root, session)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(record.Agent), nil
}

// ServesSession answers whether this adapter served this session, from the
// record its own SessionStart hook wrote.
//
// That hook running is the only moment conductor can observe the adapter: the
// plugin is installed into the host's registry, which is private state nothing
// here may read, and the session environment says a host of this kind is
// running -- true whether or not the plugin was ever installed. A session that
// began under a build too old to record it reads as unserved, which costs that
// session the adapter and nothing after it: the gap is the absence of a record
// in a live session, and it goes when the session does.
func ServesSession(root, session string) (bool, error) {
	record, err := readSession(root, session)
	if err != nil {
		return false, err
	}
	return !record.ServedAt.IsZero(), nil
}

// RecordSession marks that the adapter served this session. Only the hook may
// call it: a model-started command shares the session and the environment with
// the hook, and would otherwise assert the very fact the hook exists to
// observe.
func RecordSession(root, session string) error {
	record, err := readSession(root, session)
	if err != nil {
		return err
	}
	record.ServedAt = time.Now().UTC()
	return writeSession(root, session, record)
}

// BindSession records that this session acts as agent, returning whatever it
// was bound to before so a replaced binding is visible rather than swallowed.
func BindSession(root, session, agent string) (string, error) {
	record, err := readSession(root, session)
	if err != nil {
		return "", err
	}
	previous := strings.TrimSpace(record.Agent)
	record.Agent, record.BoundAt = agent, time.Now().UTC()
	return previous, writeSession(root, session, record)
}

// ForgetSession forgets a session entirely. Teardown runs it for every session,
// including those that bound nothing, so absence is success -- and a session
// that only ever recorded being served still has that to clear.
func ForgetSession(root, session string) error {
	if err := ValidateSession(session); err != nil {
		return err
	}
	if err := os.Remove(sessionRecordPath(root, session)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// readSession is the one reader of a session's record, so every caller sees
// both facts and can write back the one it does not own. Most sessions on most
// hosts have no record at all, which is silence, not failure.
func readSession(root, session string) (sessionRecord, error) {
	if err := ValidateSession(session); err != nil {
		return sessionRecord{}, err
	}
	data, err := os.ReadFile(sessionRecordPath(root, session))
	if errors.Is(err, os.ErrNotExist) {
		return sessionRecord{}, nil
	}
	if err != nil {
		return sessionRecord{}, err
	}
	var record sessionRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return sessionRecord{}, err
	}
	return record, nil
}

func writeSession(root, session string, record sessionRecord) error {
	path := sessionRecordPath(root, session)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	return writeAtomic(path, data)
}

// adapterDirectory holds state the protocol has no concept of: which session
// holds a stream, which sessions this adapter serves, and which identity a
// session speaks for.
func adapterDirectory(root string) string {
	return filepath.Join(root, "adapters", "claude")
}

func sessionRecordPath(root, session string) string {
	return filepath.Join(adapterDirectory(root), "sessions", session+".json")
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
