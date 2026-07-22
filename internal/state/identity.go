package state

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/danlavee/Conductor/internal/platform"
)

// ResolveAgent resolves and validates explicit, environment, or terminal identity.
func (c *Client) ResolveAgent() (string, error) {
	if err := c.validateProtocol(); err != nil {
		return "", err
	}
	if c.Agent != "" {
		if err := validName(c.Agent); err != nil {
			return "", err
		}
		if c.ownerPID == 0 {
			c.ownerPID = os.Getpid()
		}
		return c.Agent, nil
	}
	if env := os.Getenv("CONDUCTOR_AGENT"); env != "" {
		if err := validName(env); err != nil {
			return "", err
		}
		c.Agent = env
		c.ownerPID = os.Getpid()
		return env, nil
	}
	var session Session
	parentPID := os.Getppid()
	parentStart, tokenOK := platform.ProcessStartToken(parentPID)
	if err := readJSON(filepath.Join(c.Home, "sessions", strconv.Itoa(parentPID)+".json"), &session); err == nil && tokenOK && session.Agent != "" && session.ParentPID == parentPID && session.ParentStart == parentStart {
		if err := validName(session.Agent); err != nil {
			return "", err
		}
		c.Agent = session.Agent
		c.ownerPID = parentPID
		return session.Agent, nil
	}
	return "", errors.New("agent identity is not bound; register in this terminal or set CONDUCTOR_AGENT")
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

func (c *Client) bindSession(agent string) error {
	parentPID := os.Getppid()
	parentStart, ok := platform.ProcessStartToken(parentPID)
	if !ok {
		if os.Getenv("CONDUCTOR_AGENT") == agent {
			return nil
		}
		return errors.New("cannot bind terminal identity; set CONDUCTOR_AGENT explicitly")
	}
	session := Session{Agent: agent, ParentPID: parentPID, ParentStart: parentStart, BoundAt: time.Now().UTC()}
	c.ownerPID = parentPID
	return writeJSONAtomic(filepath.Join(c.Home, "sessions", strconv.Itoa(parentPID)+".json"), session)
}

func (c *Client) ownerProcessID() int {
	if c.ownerPID != 0 {
		return c.ownerPID
	}
	return os.Getpid()
}
