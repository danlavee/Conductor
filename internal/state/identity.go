package state

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// ResolveAgent validates the explicit agent identity the caller supplied at
// construction (via New or by setting Client.Agent directly). Callers must
// provide identity explicitly; Conductor no longer infers it from an
// environment variable or a terminal-session binding.
func (c *Client) ResolveAgent() (string, error) {
	if err := c.validateProtocol(); err != nil {
		return "", err
	}
	if strings.TrimSpace(c.Agent) == "" {
		return "", errors.New("agent identity is required")
	}
	if err := validName(c.Agent); err != nil {
		return "", err
	}
	if c.ownerPID == 0 {
		c.ownerPID = os.Getpid()
	}
	return c.Agent, nil
}

func (c *Client) requireAgent() (string, error) {
	agent, err := c.ResolveAgent()
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(filepath.Join(c.Home, "registry", agent+".json")); errors.Is(err, os.ErrNotExist) {
		return "", &ProtocolError{Code: "NOT_FOUND", Text: "registered agent does not exist"}
	} else if err != nil {
		return "", err
	}
	return agent, nil
}

func (c *Client) ownerProcessID() int {
	if c.ownerPID != 0 {
		return c.ownerPID
	}
	return os.Getpid()
}
