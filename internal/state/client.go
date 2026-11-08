package state

import (
	"os"
	"path/filepath"
	"time"
)

// lockTimeout bounds every wait for a state mutex.
const lockTimeout = 3 * time.Minute

// Client is safe to construct per CLI invocation.
type Client struct {
	Home         string
	Agent        string
	PollInterval time.Duration
	saveCursorFn func(string, Cursor) error
	watchIdleFn  func()
}

// Open constructs a client without reading, initializing, or otherwise
// touching the protocol root.
func Open(home, agent string) (*Client, error) {
	root, err := Root(home)
	if err != nil {
		return nil, err
	}
	return &Client{Home: root, Agent: agent, PollInterval: 200 * time.Millisecond}, nil
}

// Root resolves a state root the same way opening a client does, without an
// agent and without touching the filesystem. A caller that has to locate state
// *before* it knows which identity it belongs to needs exactly this and nothing
// else -- and passing a placeholder agent to Open to get at Home would make the
// agent field a lie for as long as that client lived.
func Root(home string) (string, error) {
	if home == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		home = filepath.Join(userHome, ".conductor")
	}
	absolute, err := filepath.Abs(home)
	if err != nil {
		return "", err
	}
	return filepath.Clean(absolute), nil
}

// New opens and, when necessary, initializes a Conductor state root.
func New(home, agent string) (*Client, error) {
	c, err := Open(home, agent)
	if err != nil {
		return nil, err
	}
	home = c.Home
	if err := initializeProtocol(home); err != nil {
		return nil, err
	}
	for _, dir := range []string{"registry", "topics", "subscriptions", "locks", "cursors", "state"} {
		if err := os.MkdirAll(filepath.Join(home, dir), 0o700); err != nil {
			return nil, err
		}
	}
	return c, nil
}
