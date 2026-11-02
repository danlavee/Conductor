// Package claude implements the Claude Code adapter: the host-specific half of
// wake delivery, which Conductor's core deliberately does not contain. It
// reaches the bus through the same client the CLI uses.
//
// See docs/integrations/claude-code.md for this host's mechanism and limits,
// and docs/design/model.md for the invariants they serve.
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

	"github.com/danlavee/Conductor/internal/platform"
	conductor "github.com/danlavee/Conductor/internal/state"
)

const (
	// AdapterName is the install directory and the plugin name, deliberately one
	// spelling for both.
	AdapterName = "claude-code"

	// SessionEnvironment is exported to processes the model starts.
	SessionEnvironment = "CLAUDE_CODE_SESSION_ID"

	// SessionPlaceholder is substituted into a hook's argument list, and is
	// named here so an unsubstituted one can be rejected.
	SessionPlaceholder = "${CLAUDE_SESSION_ID}"

	// ProjectEnvironment is substituted into a hook's argument list. Hooks only:
	// a command the model starts uses ProjectDirectory instead.
	ProjectEnvironment = "CLAUDE_PROJECT_DIR"

	// IdentityFile binds a project to a Conductor identity.
	IdentityFile = ".conductor-agent"

	// byteOrderMark is U+FEFF, spelled as bytes because Go rejects the
	// character outside a file's first position.
	byteOrderMark = "\xef\xbb\xbf"

	// WakeExitCode starts a turn in an idle session when a backgrounded hook
	// exits with it.
	WakeExitCode = 2
)

// Outcome is why an arm attempt ended. Only the ones Wakes reports have
// something for the model; every other is a no-op, not a fault.
type Outcome string

const (
	// Delivered means work reached the model.
	Delivered Outcome = "delivered"
	// Refused means another stream already owns this identity.
	Refused Outcome = "refused"
	// Released means the session this stream belonged to ended.
	Released Outcome = "released"
	// Unregistered means the identity has not joined.
	Unregistered Outcome = "unregistered"
	// Unbound means the project names no identity. Reached without the bus.
	Unbound Outcome = "unbound"
	// NotOwned means teardown found no stream this session was holding.
	NotOwned Outcome = "not-owned"
	// Orphaned means the session this stream exists to wake is gone.
	Orphaned Outcome = "orphaned"
)

// Outcome codes start at ten, clear of the exit statuses this adapter
// produces: 0, 1, and WakeExitCode.
const (
	CodeUnbound = 10 + iota
	CodeDelivered
	CodeRefused
	CodeUnregistered
	CodeReleased
	CodeNotOwned
	CodeOrphaned
)

// Code is the outcome's stable numeric identity. Zero is not an outcome.
func (o Outcome) Code() int {
	switch o {
	case Unbound:
		return CodeUnbound
	case Delivered:
		return CodeDelivered
	case Refused:
		return CodeRefused
	case Unregistered:
		return CodeUnregistered
	case Released:
		return CodeReleased
	case NotOwned:
		return CodeNotOwned
	case Orphaned:
		return CodeOrphaned
	}
	return 0
}

// Wakes reports whether the outcome wrote something the model has to see.
// Writing and waking are one decision: stdout reaches the model only on a
// waking exit.
func (o Outcome) Wakes() bool { return o == Delivered }

// residency records which session's stream holds an identity, and is the
// channel teardown speaks through.
type residency struct {
	Session  string     `json:"session"`
	PID      int        `json:"pid"`
	Target   wakeTarget `json:"target"`
	Released bool       `json:"released"`
}

// wakeTarget is the process this stream exists to wake. Persisted so an
// outlived stream is diagnosable from outside the process.
type wakeTarget struct {
	PID   int    `json:"pid"`
	Start string `json:"start,omitempty"`
}

// currentWakeTarget is the session process: the host spawns hooks in exec
// form, with no shell in between, so it is this process's parent.
func currentWakeTarget() wakeTarget {
	pid := os.Getppid()
	start, _ := platform.ProcessStartToken(pid)
	return wakeTarget{PID: pid, Start: start}
}

// alive reports whether the stream can still reach what it exists to wake. An
// unreadable start token counts as dead, not as absent evidence: a dropped
// stream is restored at the next turn boundary, an orphan destroys work.
func (t wakeTarget) alive() bool {
	if t.PID <= 0 || !platform.ProcessAlive(t.PID) {
		return false
	}
	if t.Start == "" {
		return true
	}
	start, ok := platform.ProcessStartToken(t.PID)
	return ok && start == t.Start
}

// ProjectDirectory walks up from startDir to the nearest ancestor holding an
// IdentityFile, reporting the empty string when none does. It is how a
// model-started command finds what this host tells only its hooks.
func ProjectDirectory(startDir string) (string, error) {
	directory, err := filepath.Abs(startDir)
	if err != nil {
		return "", err
	}
	for {
		_, err := os.Stat(filepath.Join(directory, IdentityFile))
		if err == nil {
			return directory, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			return "", nil
		}
		directory = parent
	}
}

// ResolveIdentity reads the Conductor identity bound to a project. An absent
// file is silence, not an error -- a project may simply not have opted in --
// while an empty projectDir is an error, being a caller that never established
// which project it is in rather than a project without an identity.
func ResolveIdentity(projectDir string) (string, error) {
	if strings.TrimSpace(projectDir) == "" {
		return "", errors.New("no project directory to resolve an identity from")
	}
	data, err := os.ReadFile(filepath.Join(projectDir, IdentityFile))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	// Notepad and PowerShell's Set-Content -Encoding utf8 both add a byte order
	// mark, and TrimSpace does not consider it whitespace.
	return strings.TrimSpace(strings.TrimPrefix(string(data), byteOrderMark)), nil
}

// Arm holds the identity's delivery stream and converts the first delivery
// into a wake, exiting on it. One process therefore wakes at most once; the
// event ending the woken turn arms the successor.
func Arm(ctx context.Context, client *conductor.Client, session string, out io.Writer) (Outcome, error) {
	return arm(ctx, client, session, out, currentWakeTarget())
}

// arm takes the target explicitly so a test can reach the orphaned path.
func arm(ctx context.Context, client *conductor.Client, session string, out io.Writer, target wakeTarget) (Outcome, error) {
	release, err := client.AcquireAdapterWatchOwnership("claude", session)
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
	if err := writeResidency(path, residency{Session: session, PID: os.Getpid(), Target: target}); err != nil {
		return "", err
	}
	defer func() { _ = os.Remove(path) }()

	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go awaitStreamEnd(streamCtx, path, session, target, client.PollInterval, cancel)

	summaries, err := client.WatchContext(streamCtx)
	if err != nil {
		if errors.Is(err, context.Canceled) && ctx.Err() == nil {
			if !target.alive() {
				return Orphaned, nil
			}
			return Released, nil
		}
		return "", err
	}
	// Closes the window between the poller's last check and this delivery.
	if !target.alive() {
		return Orphaned, nil
	}
	batch, err := client.ResolveBatch(summaries)
	if err != nil {
		return "", err
	}
	if err := conductor.WriteJSON(out, batch); err != nil {
		return "", err
	}
	return Delivered, client.AcknowledgeBatch(batch)
}

// Release ends the stream this session owns, and is a no-op when it owns none.
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

// awaitStreamEnd cancels the stream when teardown releases this session's
// residency, or when the session it wakes is gone. The second is not a safety
// net for the first: teardown fires only on an orderly exit, and the hook's
// timeout is a day.
func awaitStreamEnd(ctx context.Context, path, session string, target wakeTarget, interval time.Duration, cancel context.CancelFunc) {
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
		if !target.alive() {
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
	return filepath.Join(adapterDirectory(client.Home), agent+".json"), nil
}

// writeResidency publishes by rename, so the poller never reads a partial
// record and mistakes it for a release.
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
