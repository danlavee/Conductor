package state

import (
	"os"
	"path/filepath"
	"time"
)

const defaultLockTimeout = 3 * time.Minute

// Client is safe to construct per CLI invocation.
type Client struct {
	Home         string
	Agent        string
	LockTimeout  time.Duration
	PollInterval time.Duration
	ownerPID     int
}

// New opens a Conductor state root. An empty home uses ~/.conductor.
func New(home, agent string) (*Client, error) {
	if home == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		home = filepath.Join(userHome, ".conductor")
	}
	if err := initializeProtocol(home); err != nil {
		return nil, err
	}
	c := &Client{Home: home, Agent: agent, LockTimeout: defaultLockTimeout, PollInterval: 200 * time.Millisecond}
	for _, dir := range []string{"registry", "topics", "subscriptions", "locks", "inbox", "events", "cursors", "transactions", "state"} {
		if err := os.MkdirAll(filepath.Join(home, dir), 0o700); err != nil {
			return nil, err
		}
	}
	return c, nil
}
