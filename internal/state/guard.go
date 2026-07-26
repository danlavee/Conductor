package state

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"

	"github.com/danlavee/Conductor/internal/cutover"
	"github.com/danlavee/Conductor/internal/platform"
)

// watchOwner is a diagnostic-only sidecar recording which process currently
// holds an agent's watch-ownership guard. It plays no part in the mutual
// exclusion itself -- that's the OS file lock in platform.AcquireFileMutex --
// so a crash between acquiring the lock and writing this file just means a
// LOCKED error falls back to its generic message, never a false LOCKED result.
type watchOwner struct {
	PID        int    `json:"pid"`
	Capability int    `json:"cutover_capability"`
	Build      string `json:"build"`
	Protocol   int    `json:"protocol"`
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
	controlDir, err := cutover.Directory(c.Home)
	if err != nil {
		return nil, err
	}
	guardPath := filepath.Join(controlDir, "watch", agent+".guard")
	ownerPath := filepath.Join(controlDir, "watch", agent+".owner.json")
	release, err := platform.AcquireFileMutex(guardPath, 0)
	if errors.Is(err, platform.ErrMutexTimeout) {
		return nil, &ProtocolError{Code: "LOCKED", Agent: agent, Text: watchOwnershipHint(ownerPath)}
	}
	if err != nil {
		return nil, err
	}
	if writeErr := writeJSONAtomic(ownerPath, watchOwner{
		PID: os.Getpid(), Capability: cutover.Capability,
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
