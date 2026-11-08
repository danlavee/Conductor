package state

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// requireAgent validates the explicit agent identity the caller supplied at
// construction (via New or by setting Client.Agent directly), and requires it
// to be registered.
func (c *Client) requireAgent() (string, error) {
	if err := c.validateProtocol(); err != nil {
		return "", err
	}
	if strings.TrimSpace(c.Agent) == "" {
		return "", errors.New("agent identity is required")
	}
	if err := validName(c.Agent); err != nil {
		return "", err
	}
	if _, err := os.Stat(filepath.Join(c.Home, "registry", c.Agent+".json")); errors.Is(err, os.ErrNotExist) {
		return "", &ProtocolError{Code: "NOT_FOUND", Text: "registered agent does not exist"}
	} else if err != nil {
		return "", err
	}
	return c.Agent, nil
}
