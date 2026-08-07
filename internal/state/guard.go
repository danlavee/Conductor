package state

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"

	"github.com/danlavee/Conductor/internal/cutover"
	"github.com/danlavee/Conductor/internal/platform"
)

// watchOwner records which process currently holds an agent's watch-ownership
// guard. It plays no part in the mutual exclusion itself -- that's the OS file
// lock in platform.AcquireFileMutex -- so a crash between acquiring the lock
// and writing this file costs a LOCKED error its specific message, never a
// false LOCKED result.
//
// It is no longer only a diagnostic, though. WatchStatus derives wakeability
// entirely from this file, which is why an unreadable one fails that query
// instead of degrading to "not wakeable": absence is a real answer, corruption
// is not, and reporting a corrupt guard record as a quiet false would hide
// exactly the condition it exists to expose.
type watchOwner struct {
	PID          int    `json:"pid"`
	ProcessStart string `json:"process_start,omitempty"`
	Capability   int    `json:"cutover_capability"`
	Build        string `json:"build"`
	Protocol     int    `json:"protocol"`
}

// AcquireWatchOwnership prevents more than one watcher from delivering
// signals for the same agent. The OS releases ownership on crash.
func (c *Client) AcquireWatchOwnership() (func() error, error) {
	releaseOperation, operationErr := c.beginOperation()
	controlOnly := false
	if operationErr != nil {
		var blocked *cutover.BlockedError
		if !errors.As(operationErr, &blocked) {
			return nil, operationErr
		}
		if blocked.State.Phase != cutover.Replaced {
			return nil, operationErr
		}
		controlOnly = true
	}
	if releaseOperation != nil {
		defer releaseOperation()
	}
	agent := c.Agent
	var watchGeneration int64
	watchParticipant := false
	if controlOnly {
		if err := validName(agent); err != nil {
			return nil, err
		}
	} else {
		if err := c.validateProtocol(); err != nil {
			return nil, err
		}
		var err error
		agent, err = c.requireAgent()
		if err != nil {
			return nil, err
		}
		controlState, _, err := cutover.Observe(c.Home)
		if err != nil {
			return nil, err
		}
		watchParticipant = true
		watchGeneration = controlState.Generation
	}
	guardPath, ownerPath, err := watchGuardPaths(c.Home, agent)
	if err != nil {
		return nil, err
	}
	release, err := platform.AcquireFileMutex(guardPath, 0)
	if errors.Is(err, platform.ErrMutexTimeout) {
		return nil, &ProtocolError{Code: "LOCKED", Agent: agent, Text: watchOwnershipHint(ownerPath)}
	}
	if err != nil {
		return nil, err
	}
	pid := os.Getpid()
	processStart, _ := platform.ProcessStartToken(pid)
	if writeErr := writeJSONAtomic(ownerPath, watchOwner{
		PID: pid, ProcessStart: processStart, Capability: cutover.Capability,
		Build: runningBuild(), Protocol: CurrentProtocolVersion,
	}); writeErr != nil {
		_ = release()
		return nil, writeErr
	}
	c.watchParticipant = watchParticipant
	c.watchGeneration = watchGeneration
	return func() error {
		_ = os.Remove(ownerPath)
		c.watchParticipant = false
		return release()
	}, nil
}

// watchGuardPaths locates an agent's ownership guard and its diagnostic owner
// sidecar. Both live in the control directory beside the versioned root, so a
// claim on an identity outlives a protocol cutover of the state it guards.
func watchGuardPaths(home, agent string) (guard, owner string, err error) {
	controlDir, err := cutover.Directory(home)
	if err != nil {
		return "", "", err
	}
	directory := filepath.Join(controlDir, "watch")
	return filepath.Join(directory, agent+".guard"), filepath.Join(directory, agent+".owner.json"), nil
}

// WatchStatus is the observable answer to whether an identity can be woken
// right now: present on the roster, and with a live delivery stream holding
// its ownership guard.
type WatchStatus struct {
	Agent      string `json:"agent"`
	Registered bool   `json:"registered"`
	Wakeable   bool   `json:"wakeable"`
	PID        int    `json:"pid,omitempty"`
}

// WatchStatus answers without acquiring the guard, because a status check must
// never displace or momentarily block a live stream. Wakeability is derived
// from the recorded owner still being the same live process -- never from a
// caller's assertion that it is watching, which is exactly the claim that goes
// stale when a stream dies without saying so.
//
// It deliberately does not go through ResolveAgent, and so takes no operation
// lease and reads no protocol version. Those admit a caller to the bus, and
// this question is asked from outside it: the times an operator most needs to
// know who would respond are the times the bus is least willing to say --
// frozen mid-cutover, or a root that was never initialized at all. The name
// check it does instead is ResolveAgent's, unchanged, so an identity this
// accepts is one every other command accepts too.
//
// The two fields are not equally trustworthy during a cutover, and the weaker
// one is the less important one. Wakeable comes from the control directory,
// which sits beside the versioned root and survives its replacement. Registered
// stats the root itself, and replacement swaps the root's generation at the
// same path while the bus is still frozen -- so within that operator-driven
// window a registered agent can read as Registered:false, indistinguishable
// from one that never joined. Answering during a freeze is worth that, because
// the field the query exists for is the one that stays sound.
func (c *Client) WatchStatus() (WatchStatus, error) {
	if strings.TrimSpace(c.Agent) == "" {
		return WatchStatus{}, errors.New("agent identity is required")
	}
	agent := c.Agent
	if err := validName(agent); err != nil {
		return WatchStatus{}, err
	}
	status := WatchStatus{Agent: agent}
	if _, err := os.Stat(filepath.Join(c.Home, "registry", agent+".json")); err == nil {
		status.Registered = true
	} else if !errors.Is(err, os.ErrNotExist) {
		return WatchStatus{}, err
	}
	pid, err := c.liveWatchOwner(agent)
	if err != nil {
		return WatchStatus{}, err
	}
	status.Wakeable = pid > 0
	status.PID = pid
	return status, nil
}

// liveWatchOwner reports the process currently holding an agent's watch
// ownership, or zero when nothing would answer a signal for it. Absence is an
// answer rather than an error: no sidecar means no stream ever armed, and a
// sidecar naming a dead or recycled process means one armed and did not
// survive -- which is precisely the silent failure the caller is asking about.
func (c *Client) liveWatchOwner(agent string) (int, error) {
	_, ownerPath, err := watchGuardPaths(c.Home, agent)
	if err != nil {
		return 0, err
	}
	var owner watchOwner
	if err := readJSON(ownerPath, &owner); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, err
	}
	if owner.PID <= 0 || !watchOwnerAlive(owner) {
		return 0, nil
	}
	return owner.PID, nil
}

// watchOwnerAlive rejects a recycled PID by comparing the operating system's
// process-instance token. A sidecar written before the token was recorded, or
// on a platform that cannot supply one, falls back to liveness alone.
func watchOwnerAlive(owner watchOwner) bool {
	if current, ok := platform.ProcessStartToken(owner.PID); ok && owner.ProcessStart != "" {
		return current == owner.ProcessStart && platform.ProcessAlive(owner.PID)
	}
	return platform.ProcessAlive(owner.PID)
}

func runningBuild() string {
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" {
		return info.Main.Version
	}
	return "(devel)"
}

// watchOwnershipHint reads the current holder's diagnostic sidecar, if one is
// present and readable, to name the exact PID to look at instead of leaving
// an operator guessing which of possibly several conductor.exe processes to
// stop. Falls back to a generic message if the sidecar is missing, stale, or
// unreadable -- it's a diagnostic best effort, not load-bearing.
func watchOwnershipHint(ownerPath string) string {
	var owner watchOwner
	if err := readJSON(ownerPath, &owner); err != nil || owner.PID <= 0 {
		return "another watcher already owns this agent"
	}
	return fmt.Sprintf("another watcher already owns this agent (pid %d)", owner.PID)
}

func (c *Client) acquireTransactionGuard(agent string) (func() error, error) {
	return c.acquireLeaseGuard(filepath.Join(c.Home, "transactions", agent+".guard"))
}

func (c *Client) acquireLeaseGuard(guard string) (func() error, error) {
	return c.acquireStateMutex(guard)
}

func (c *Client) acquireStateMutex(path string) (func() error, error) {
	release, err := platform.AcquireFileMutex(path, c.LockTimeout)
	if errors.Is(err, platform.ErrMutexTimeout) {
		return nil, &ProtocolError{Code: "TIMEOUT", Text: "state mutex did not become available"}
	}
	return release, err
}
