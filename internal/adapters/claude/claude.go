// Package claude implements the Claude Code adapter: the host-specific half of
// wake delivery, which Conductor's core deliberately does not contain.
//
// The core exposes one host-neutral contract -- a delivery stream and a
// wakeability query -- and knows nothing about hooks, sessions, or exit codes.
// Everything in this package is the inverse: it knows only how this host
// starts a turn, and reaches the bus through the same public client any other
// caller would use.
package claude

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	conductor "github.com/danlavee/Conductor"
)

const (
	// AdapterName is the directory this adapter installs into, and it is one
	// name for both halves on purpose: the installed path and the plugin the
	// host loads have to agree, and a second spelling is a second thing to
	// keep in step.
	AdapterName = "claude-code"

	// The host exposes the same session identifier two ways, to two different
	// kinds of caller, and this adapter has both kinds. Which one applies is
	// decided by who started the process, never by preference: a hook cannot
	// read SessionEnvironment and a command the model runs gets no argument
	// substitution.

	// SessionEnvironment is how a process the *model* starts learns which
	// session it is in. The host exports it to child processes, which is what
	// makes `bind` runnable by an agent from inside its own session.
	SessionEnvironment = "CLAUDE_CODE_SESSION_ID"

	// SessionPlaceholder is how a *hook* learns the same thing: the host
	// substitutes it into the hook's argument list before spawning it. It is
	// named here only so an unsubstituted one can be recognized. A host that
	// does not know the placeholder passes it through as text, and a session
	// literally called "${CLAUDE_SESSION_ID}" would be accepted as real -- and
	// then every session on the machine would share one identifier, which is
	// precisely the condition session scoping exists to prevent.
	SessionPlaceholder = "${CLAUDE_SESSION_ID}"

	// ProjectEnvironment locates the project a session is attached to. The
	// host substitutes it into hook arguments before the process starts.
	ProjectEnvironment = "CLAUDE_PROJECT_DIR"

	// IdentityFile binds a project to a Conductor identity. It is a file the
	// adapter reads for itself rather than an argument the model supplies,
	// because an identity the model can state is an identity it can get wrong,
	// forget, or lose to a compaction.
	IdentityFile = ".conductor-agent"

	// byteOrderMark is U+FEFF, spelled as its UTF-8 bytes because Go rejects
	// the character itself anywhere but the first position of a file -- and
	// because a literal one here would read as an empty string to anyone
	// reviewing the line that strips it.
	byteOrderMark = "\xef\xbb\xbf"

	// WakeExitCode is this host's wake primitive. A backgrounded hook that
	// exits with it starts a turn in an otherwise idle session and appends its
	// stdout to a system reminder the model reads.
	WakeExitCode = 2
)

// Outcome is why an arm attempt ended. An outcome either has something for the
// model or it does not; the ones that do wake the session, and the rest exit
// cleanly, because a restore attempt that finds nothing to do is a no-op and
// not a fault.
type Outcome string

const (
	// Delivered means work reached the model and the session must wake.
	Delivered Outcome = "delivered"
	// Refused means another stream already owns this identity. Requirement 8:
	// any number of blind restore attempts converge on exactly one stream.
	Refused Outcome = "refused"
	// Released means the session this stream belonged to ended.
	Released Outcome = "released"
	// Replaced means the conductor being watched was cut over beneath it.
	Replaced Outcome = "replaced"
	// Unregistered means the identity has not joined, so there is nothing to
	// hold a stream for. Arming again is harmless once it does.
	Unregistered Outcome = "unregistered"
	// Unbound means the project names no identity, so the adapter is installed
	// but opted out. It is the one outcome reached without touching the bus.
	Unbound Outcome = "unbound"
	// NotOwned means teardown found no stream this session was holding, which
	// is the ordinary result for every session that never armed one.
	NotOwned Outcome = "not-owned"
)

// Outcome codes are deliberately numbered from ten, clear of every process
// exit status this adapter can produce -- 0 for nothing to say, 1 for a
// genuine failure, WakeExitCode for a wake. The two numbering spaces answer
// different questions, and overlapping them would invite reading a report code
// as an exit status.
//
// The exit status cannot carry these itself. This host treats any code other
// than 0 and WakeExitCode as a non-blocking error and shows its stderr to the
// user, so giving Refused or Unregistered a status of its own would report an
// error at every turn end that armed correctly -- the blind re-arm looking
// broken precisely when it worked.
const (
	CodeUnbound = 10 + iota
	CodeDelivered
	CodeReplaced
	CodeRefused
	CodeUnregistered
	CodeReleased
	CodeNotOwned
)

// Code is the outcome's stable numeric identity, so a run can be classified
// without matching on prose. Zero means the value is not one of the outcomes
// above, which is a programming error rather than a result.
func (o Outcome) Code() int {
	switch o {
	case Unbound:
		return CodeUnbound
	case Delivered:
		return CodeDelivered
	case Replaced:
		return CodeReplaced
	case Refused:
		return CodeRefused
	case Unregistered:
		return CodeUnregistered
	case Released:
		return CodeReleased
	case NotOwned:
		return CodeNotOwned
	}
	return 0
}

// Wakes reports whether the outcome put something on the writer that the model
// has to see. On this host stdout reaches the model only on a waking exit, so
// an outcome that writes without waking is an outcome nobody hears -- the two
// have to be decided together rather than separately.
func (o Outcome) Wakes() bool { return o == Delivered || o == Replaced }

// residency records which session's stream currently holds an identity, and
// is the channel teardown speaks through. It is adapter-owned state kept out
// of the protocol root: the core has no concept of a host session, and giving
// it one would put back the vendor knowledge this design removed.
type residency struct {
	Session  string `json:"session"`
	PID      int    `json:"pid"`
	Released bool   `json:"released"`
}

// ResolveIdentity reads the Conductor identity bound to a project. An absent
// binding is not an error: a project that has not opted in should install the
// adapter and stay silent, not fail every session start.
func ResolveIdentity(projectDir string) (string, error) {
	if strings.TrimSpace(projectDir) == "" {
		return "", errors.New("no project directory: " + ProjectEnvironment + " is unset")
	}
	data, err := os.ReadFile(filepath.Join(projectDir, IdentityFile))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	// The byte order mark is stripped because this file is written by hand on
	// a host where the ordinary ways of doing that add one: Notepad and
	// PowerShell's Set-Content -Encoding utf8 both do. TrimSpace does not
	// consider it whitespace, so it would otherwise survive into the agent
	// name and fail validation at every session start, reporting an identity
	// that looks correct in every editor that hides the mark.
	return strings.TrimSpace(strings.TrimPrefix(string(data), byteOrderMark)), nil
}

// Arm holds the identity's delivery stream and converts the first delivery
// into a wake. It is the resident component and the turn-boundary trigger at
// once: the host backgrounds it, and it exits the moment it has something to
// say, which is exactly what wakes the session.
//
// One process therefore wakes at most once. That is not a limitation to work
// around -- the event that ends the woken turn arms the successor, so the
// cycle closes without the model participating in it.
func Arm(ctx context.Context, client *conductor.Client, session string, out io.Writer) (Outcome, error) {
	release, err := client.AcquireWatchOwnership()
	if err != nil {
		var protocol *conductor.ProtocolError
		if errors.As(err, &protocol) {
			switch protocol.Code {
			case "LOCKED":
				return Refused, nil
			case "NOT_FOUND":
				return Unregistered, nil
			}
		}
		return "", err
	}
	defer func() { _ = release() }()

	path, err := residencyPath(client)
	if err != nil {
		return "", err
	}
	if err := writeResidency(path, residency{Session: session, PID: os.Getpid()}); err != nil {
		return "", err
	}
	defer func() { _ = os.Remove(path) }()

	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go awaitRelease(streamCtx, path, session, client.PollInterval, cancel)

	result, err := client.WatchResultContext(streamCtx)
	if err != nil {
		if errors.Is(err, context.Canceled) && ctx.Err() == nil {
			return Released, nil
		}
		return "", err
	}
	defer result.Close()
	if result.Activation != nil {
		return Replaced, conductor.WriteJSON(out, result.Activation)
	}
	batch, err := client.ResolveBatch(result.Summaries, conductor.DeliveryContent)
	if err != nil {
		return "", err
	}
	if err := conductor.WriteJSON(out, batch); err != nil {
		return "", err
	}
	return Delivered, client.AcknowledgeBatch(batch)
}

// Release ends the stream a session owns. It is scoped to the caller's session
// so that a second session in the same project cannot tear down the first
// one's residency, and it is a no-op when this session owns nothing.
func Release(client *conductor.Client, session string) (bool, error) {
	path, err := residencyPath(client)
	if err != nil {
		return false, err
	}
	current, err := readResidency(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if current.Session != session || current.Released {
		return false, nil
	}
	current.Released = true
	return true, writeResidency(path, current)
}

// awaitRelease cancels the stream once teardown marks this session's residency
// released. Polling is what the watch loop already does, so this adds a file
// stat per interval and no new waiting machinery.
func awaitRelease(ctx context.Context, path, session string, interval time.Duration, cancel context.CancelFunc) {
	for {
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		current, err := readResidency(path)
		if err != nil || current.Session != session || current.Released {
			cancel()
			return
		}
	}
}

func residencyPath(client *conductor.Client) (string, error) {
	agent, err := client.ResolveAgent()
	if err != nil {
		return "", err
	}
	directory, err := adapterDirectory(client.Home)
	if err != nil {
		return "", err
	}
	return filepath.Join(directory, agent+".json"), nil
}

// writeResidency publishes the record by rename. Teardown rewrites this file
// from a different process than the one polling it, and a reader that catches
// a truncated write cannot tell it apart from a release -- it would cancel a
// live stream. Publishing atomically removes the window rather than leaving it
// to the fact that only one writer happens to exist today.
func writeResidency(path string, value residency) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return writeAtomic(path, data)
}

func readResidency(path string) (residency, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return residency{}, err
	}
	var value residency
	return value, json.Unmarshal(data, &value)
}
