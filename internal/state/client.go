package state

import (
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/danlavee/Conductor/internal/cutover"
)

const defaultLockTimeout = 3 * time.Minute

// Client is safe to construct per CLI invocation.
type Client struct {
	Home             string
	Agent            string
	LockTimeout      time.Duration
	PollInterval     time.Duration
	ownerPID         int
	saveCursorFn     func(string, Cursor) error
	watchIdleFn      func()
	watchGeneration  int64
	watchParticipant bool
	operationMu      sync.Mutex
	operationDepth   int
	operationRelease func() error
}

// Open constructs a client without reading, initializing, or otherwise
// touching the protocol root. Watches use it so a frozen process can wait on
// the external cutover plane without opening the root it is replacing.
func Open(home, agent string) (*Client, error) {
	root, err := Root(home)
	if err != nil {
		return nil, err
	}
	return &Client{Home: root, Agent: agent, LockTimeout: defaultLockTimeout, PollInterval: 200 * time.Millisecond}, nil
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

// New opens and, when necessary, initializes a Conductor state root. Root
// writes happen only while an admitted operation lease is held.
func New(home, agent string) (*Client, error) {
	c, err := Open(home, agent)
	if err != nil {
		return nil, err
	}
	release, err := c.beginOperation()
	if err != nil {
		return nil, err
	}
	defer release()
	home = c.Home
	if err := initializeProtocol(home); err != nil {
		return nil, err
	}
	for _, dir := range []string{"registry", "topics", "subscriptions", "locks", "inbox", "events", "cursors", "transactions", "state"} {
		if err := os.MkdirAll(filepath.Join(home, dir), 0o700); err != nil {
			return nil, err
		}
	}
	return c, nil
}

// beginOperation joins this client's current admitted operation, or creates a
// new cross-process lease when it is the outermost call. Nested SDK methods
// and concurrent calls on the same client share the lease until all return.
func (c *Client) beginOperation() (func() error, error) {
	c.operationMu.Lock()
	defer c.operationMu.Unlock()
	if c.operationDepth == 0 {
		release, err := cutover.Admit(c.Home)
		if err != nil {
			return nil, err
		}
		c.operationRelease = release
	}
	c.operationDepth++
	var once sync.Once
	var releaseErr error
	return func() error {
		once.Do(func() {
			c.operationMu.Lock()
			defer c.operationMu.Unlock()
			c.operationDepth--
			if c.operationDepth == 0 {
				releaseErr = c.operationRelease()
				c.operationRelease = nil
			}
		})
		return releaseErr
	}, nil
}
