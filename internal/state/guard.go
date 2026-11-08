package state

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/danlavee/Conductor/internal/platform"
)

// watchOwner records which process currently holds an agent's watch-ownership
// guard. The mutual exclusion itself is the OS file lock; this record is what
// liveHolder derives wakeability from, so an unreadable one fails that query
// rather than reading as "not wakeable".
type watchOwner struct {
	platform.Process
	Holder  Holder `json:"holder"`
	Session string `json:"session,omitempty"` // the adapter session it holds for, opaque here
}

// Holder is what armed a delivery stream, which decides how long wakeability
// stays true: an adapter re-arms at every turn boundary, while a watcher an
// agent armed itself exits on its first delivery.
type Holder string

const (
	HolderSelf    Holder = "self"
	HolderAdapter Holder = "adapter"
)

// AcquireWatchOwnership prevents more than one watcher from delivering signals
// for the same agent, recording session ("" for none) as the owner's. The OS
// releases ownership on crash.
func (c *Client) AcquireWatchOwnership(holder Holder, session string) (func() error, error) {
	agent, err := c.requireAgent()
	if err != nil {
		return nil, err
	}
	self, err := platform.ProcessOf(os.Getpid())
	if err != nil {
		return nil, err
	}
	guardPath, ownerPath := watchGuardPaths(c.Home, agent)
	release, err := platform.AcquireFileMutex(guardPath, 0)
	if errors.Is(err, platform.ErrMutexTimeout) {
		return nil, &ProtocolError{Code: "LOCKED", Agent: agent, Text: watchOwnershipHint(ownerPath)}
	}
	if err != nil {
		return nil, err
	}
	if writeErr := writeJSONAtomic(ownerPath, watchOwner{Process: self, Holder: holder, Session: session}); writeErr != nil {
		_ = release()
		return nil, writeErr
	}
	return func() error {
		_ = platform.Remove(ownerPath)
		return release()
	}, nil
}

// watchGuardPaths locates an agent's ownership guard and its owner record.
func watchGuardPaths(home, agent string) (guard, owner string) {
	directory := filepath.Join(home, "state", "watch")
	return filepath.Join(directory, agent+".guard"), filepath.Join(directory, agent+".owner.json")
}

// liveHolder reports what holds an agent's stream, or the zero record when
// nothing would answer a signal for it: no owner record, or one naming a dead
// or recycled process. It never acquires the guard, so a roster or publication
// reading reachability never displaces or blocks a live stream.
func (c *Client) liveHolder(agent string) (watchOwner, error) {
	_, ownerPath := watchGuardPaths(c.Home, agent)
	var owner watchOwner
	if err := readJSON(ownerPath, &owner); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return watchOwner{}, nil
		}
		return watchOwner{}, err
	}
	if !owner.Alive() {
		return watchOwner{}, nil
	}
	return owner, nil
}

// WatchOwnerSession is the session the live owner of this agent's guard holds
// for, or "" when nothing live holds it or the owner holds for no session.
func (c *Client) WatchOwnerSession() (string, error) {
	agent, err := c.requireAgent()
	if err != nil {
		return "", err
	}
	owner, err := c.liveHolder(agent)
	return owner.Session, err
}

// watchOwnershipHint names the holder's PID in a LOCKED error when the owner
// record can be read, so an operator knows which process to look at.
func watchOwnershipHint(ownerPath string) string {
	var owner watchOwner
	if err := readJSON(ownerPath, &owner); err != nil || owner.PID <= 0 {
		return "another watcher already owns this agent"
	}
	return fmt.Sprintf("another watcher already owns this agent (pid %d)", owner.PID)
}

func (c *Client) acquireStateMutex(path string) (func() error, error) {
	release, err := platform.AcquireFileMutex(path, lockTimeout)
	if errors.Is(err, platform.ErrMutexTimeout) {
		return nil, &ProtocolError{Code: "TIMEOUT", Text: "state mutex did not become available"}
	}
	return release, err
}
