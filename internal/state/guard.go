package state

import (
	"errors"
	"path/filepath"

	"github.com/danlavee/Conductor/internal/platform"
)

// AcquireWatchOwnership prevents more than one watcher from delivering
// signals for the same agent. The OS releases ownership on crash.
func (c *Client) AcquireWatchOwnership() (func() error, error) {
	if err := c.validateProtocol(); err != nil {
		return nil, err
	}
	agent, err := c.requireAgent()
	if err != nil {
		return nil, err
	}
	release, err := platform.AcquireFileMutex(filepath.Join(c.Home, "state", "watch", agent+".guard"), 0)
	if errors.Is(err, platform.ErrMutexTimeout) {
		return nil, &ProtocolError{Code: "LOCKED", Agent: agent, Text: "another watcher already owns this agent"}
	}
	return release, err
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
