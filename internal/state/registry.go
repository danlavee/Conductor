package state

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Register adds an agent, auto-subscribes it, and returns the full current snapshot.
func (c *Client) Register(name, responsibility string) (snapshot Snapshot, err error) {
	if err := c.validateProtocol(); err != nil {
		return Snapshot{}, err
	}
	if err := validName(name); err != nil {
		return Snapshot{}, err
	}
	if strings.TrimSpace(responsibility) == "" {
		return Snapshot{}, errors.New("responsibility must not be empty")
	}
	if err := c.acquireMembership(name); err != nil {
		return Snapshot{}, err
	}
	defer func() {
		if releaseErr := c.releaseWrite("_registry/membership", name); err == nil && releaseErr != nil {
			err = releaseErr
		}
	}()
	registrationPath := filepath.Join(c.Home, "registry", name+".json")
	var existing Agent
	if readErr := readJSON(registrationPath, &existing); readErr == nil {
		if existing.Responsibility != responsibility {
			return Snapshot{}, &ProtocolError{Code: "LOCKED", Agent: name, Text: "agent name is already registered with another responsibility"}
		}
		c.Agent = name
		if err := c.bindSession(name); err != nil {
			return Snapshot{}, err
		}
		latest, err := c.latestMembershipType(name)
		if err != nil {
			return Snapshot{}, err
		}
		if latest != "join" {
			if _, err := c.publishEvent("join", "registry", name, nil); err != nil {
				return Snapshot{}, err
			}
		}
		return c.FullSnapshot()
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return Snapshot{}, readErr
	}
	c.Agent = name
	if err := c.bindSession(name); err != nil {
		return Snapshot{}, err
	}
	agent := Agent{Name: name, Responsibility: responsibility, Timestamp: time.Now().UTC()}
	if err := writeJSONAtomic(registrationPath, agent); err != nil {
		return Snapshot{}, err
	}
	snapshot, err = c.FullSnapshot()
	if err != nil {
		_ = removeEventually(registrationPath)
		return Snapshot{}, err
	}
	if _, err := c.publishEvent("join", "registry", name, nil); err != nil {
		return Snapshot{}, err
	}
	return snapshot, nil
}

// Deregister removes an agent after any active transaction has been resolved.
func (c *Client) Deregister(name string) (err error) {
	if err := c.validateProtocol(); err != nil {
		return err
	}
	if err := validName(name); err != nil {
		return err
	}
	c.Agent = name
	if err := c.acquireMembership(name); err != nil {
		return err
	}
	defer func() {
		if releaseErr := c.releaseWrite("_registry/membership", name); err == nil && releaseErr != nil {
			err = releaseErr
		}
	}()
	path := filepath.Join(c.Home, "registry", name+".json")
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		latest, eventErr := c.latestMembershipType(name)
		if eventErr != nil {
			return eventErr
		}
		if latest == "leave" {
			return nil
		}
		if latest == "join" {
			_, eventErr = c.publishEvent("leave", "registry", name, nil)
			return eventErr
		}
		return &ProtocolError{Code: "NOT_FOUND", Text: "agent does not exist"}
	}
	if _, statErr := os.Stat(c.transactionPath(name)); statErr == nil {
		return &ProtocolError{Code: "LOCKED", Agent: name, Text: "agent has an active transaction; commit or abort it before deregistering"}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return statErr
	}
	if err := removeEventually(path); err != nil {
		return err
	}
	_, err = c.publishEvent("leave", "registry", name, nil)
	return err
}

// ListAgents returns the complete registry ordered by name.
func (c *Client) ListAgents() ([]Agent, error) {
	if err := c.validateProtocol(); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(filepath.Join(c.Home, "registry"))
	if err != nil {
		return nil, err
	}
	agents := make([]Agent, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		var agent Agent
		if err := readJSON(filepath.Join(c.Home, "registry", entry.Name()), &agent); err != nil {
			return nil, err
		}
		agents = append(agents, agent)
	}
	sort.Slice(agents, func(i, j int) bool { return agents[i].Name < agents[j].Name })
	return agents, nil
}

// FullSnapshot returns all agents and every current message.
func (c *Client) FullSnapshot() (Snapshot, error) {
	if err := c.validateProtocol(); err != nil {
		return Snapshot{}, err
	}
	if _, err := c.requireAgent(); err != nil {
		return Snapshot{}, err
	}
	agents, err := c.ListAgents()
	if err != nil {
		return Snapshot{}, err
	}
	result := Snapshot{Agents: agents, Resources: map[string]map[string]Message{}}
	groups, err := os.ReadDir(filepath.Join(c.Home, "topics"))
	if err != nil {
		return Snapshot{}, err
	}
	for _, group := range groups {
		if !group.IsDir() {
			continue
		}
		topics, err := os.ReadDir(filepath.Join(c.Home, "topics", group.Name()))
		if err != nil {
			return Snapshot{}, err
		}
		for _, topic := range topics {
			if !topic.IsDir() {
				continue
			}
			resource := group.Name() + "/" + topic.Name()
			release, err := c.acquireRead(resource)
			if err != nil {
				return Snapshot{}, err
			}
			history, err := c.readHistory(resource)
			releaseErr := release()
			if err != nil {
				return Snapshot{}, err
			}
			if releaseErr != nil {
				return Snapshot{}, releaseErr
			}
			messages := materialize(history, "", 0)
			if len(messages) > 0 {
				result.Resources[resource] = map[string]Message{}
				for _, message := range messages {
					result.Resources[resource][message.Key] = message
				}
			}
		}
	}
	return result, nil
}

// AcknowledgeSnapshot records the state delivered by Register. As with delta
// acknowledgement, CLI callers invoke it only after stdout accepts the result.
func (c *Client) AcknowledgeSnapshot(snapshot Snapshot) error {
	if err := c.validateProtocol(); err != nil {
		return err
	}
	agent, err := c.requireAgent()
	if err != nil {
		return err
	}
	return c.updateCursor(agent, func(cursor *Cursor) {
		for resource, messages := range snapshot.Resources {
			var maximum int64
			for key, message := range messages {
				if message.Index > maximum {
					maximum = message.Index
				}
				slot := cursorSlot(resource, key)
				if message.Index > cursor.Resources[slot] {
					cursor.Resources[slot] = message.Index
				}
			}
			if maximum > cursor.Resources[resource] {
				cursor.Resources[resource] = maximum
			}
		}
	})
}

func (c *Client) recipientNames() ([]string, error) {
	agents, err := c.ListAgents()
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(agents))
	for _, agent := range agents {
		names = append(names, agent.Name)
	}
	return names, nil
}

func (c *Client) latestMembershipType(agent string) (string, error) {
	events, err := c.unreadEvents(nil)
	if err != nil {
		return "", err
	}
	latest := ""
	for _, event := range events {
		if event.Signal.Resource == "registry" && event.Signal.Key == agent {
			latest = event.Signal.Type
		}
	}
	return latest, nil
}

func (c *Client) acquireMembership(agent string) error {
	deadline := time.Now().Add(c.LockTimeout)
	for {
		if _, err := c.acquireWrite("_registry/membership", agent); err == nil {
			return nil
		} else {
			var protocol *ProtocolError
			if !errors.As(err, &protocol) || protocol.Code != "LOCKED" || time.Now().After(deadline) {
				return err
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
}
