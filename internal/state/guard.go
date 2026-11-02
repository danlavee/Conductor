package state

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/danlavee/Conductor/internal/platform"
)

// watchOwner records which process currently holds an agent's watch-ownership
// guard. The mutual exclusion itself is the OS file lock; this record is what
// WatchStatus derives wakeability from, so an unreadable one fails that query
// rather than reading as "not wakeable".
type watchOwner struct {
	PID          int    `json:"pid"`
	ProcessStart string `json:"process_start,omitempty"`
	Holder       Holder `json:"holder"`
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
// for the same agent. The OS releases ownership on crash.
func (c *Client) AcquireWatchOwnership(holder Holder) (func() error, error) {
	if err := c.validateProtocol(); err != nil {
		return nil, err
	}
	agent, err := c.requireAgent()
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
	pid := os.Getpid()
	processStart, _ := platform.ProcessStartToken(pid)
	if writeErr := writeJSONAtomic(ownerPath, watchOwner{PID: pid, ProcessStart: processStart, Holder: holder}); writeErr != nil {
		_ = release()
		return nil, writeErr
	}
	return func() error {
		_ = os.Remove(ownerPath)
		return release()
	}, nil
}

// watchGuardPaths locates an agent's ownership guard and its owner record.
func watchGuardPaths(home, agent string) (guard, owner string) {
	directory := filepath.Join(home, "state", "watch")
	return filepath.Join(directory, agent+".guard"), filepath.Join(directory, agent+".owner.json")
}

// WatchStatus is whether an identity can be woken right now -- on the roster,
// with a live stream holding its guard -- and what would reach it. An agent
// that armed nothing and one subscribed to nothing are both unreachable, and
// only one of them looks it, so subscriptions are reported either way.
type WatchStatus struct {
	Agent         string       `json:"agent"`
	Registered    bool         `json:"registered"`
	Wakeable      bool         `json:"wakeable"`
	Holder        Holder       `json:"holder,omitempty"`
	Subscriptions Subscription `json:"subscriptions"`
}

// WatchStatus answers without acquiring the guard, because a status check must
// never displace or block a live stream. It reads no protocol version, so it
// still answers for a root that was never initialized.
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
	holder, err := c.liveHolder(agent)
	if err != nil {
		return WatchStatus{}, err
	}
	status.Wakeable, status.Holder = holder != "", holder
	if status.Subscriptions, err = c.loadSubscription(agent); err != nil {
		return WatchStatus{}, err
	}
	return status, nil
}

// liveHolder reports what holds an agent's stream, or "" when nothing would
// answer a signal for it: no owner record, or one naming a dead or recycled
// process.
func (c *Client) liveHolder(agent string) (Holder, error) {
	_, ownerPath := watchGuardPaths(c.Home, agent)
	var owner watchOwner
	if err := readJSON(ownerPath, &owner); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	if owner.PID <= 0 || !watchOwnerAlive(owner) {
		return "", nil
	}
	return owner.Holder, nil
}

// watchOwnerAlive rejects a recycled PID by comparing the operating system's
// process-instance token, where the platform can supply one.
func watchOwnerAlive(owner watchOwner) bool {
	if current, ok := platform.ProcessStartToken(owner.PID); ok && owner.ProcessStart != "" {
		return current == owner.ProcessStart && platform.ProcessAlive(owner.PID)
	}
	return platform.ProcessAlive(owner.PID)
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
	release, err := platform.AcquireFileMutex(path, c.LockTimeout)
	if errors.Is(err, platform.ErrMutexTimeout) {
		return nil, &ProtocolError{Code: "TIMEOUT", Text: "state mutex did not become available"}
	}
	return release, err
}
