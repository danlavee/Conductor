package state

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Join adds or reconnects an agent and returns the full current snapshot.
func (c *Client) Join(name, responsibility string) (snapshot Snapshot, err error) {
	releaseOperation, err := c.beginOperation()
	if err != nil {
		return Snapshot{}, err
	}
	defer func() {
		if releaseErr := releaseOperation(); err == nil {
			err = releaseErr
		}
	}()
	if err := c.validateProtocol(); err != nil {
		return Snapshot{}, err
	}
	if err := validName(name); err != nil {
		return Snapshot{}, err
	}

	registrationPath := filepath.Join(c.Home, "registry", name+".json")
	var existing Agent
	readErr := readJSON(registrationPath, &existing)
	exists := readErr == nil
	if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
		return Snapshot{}, readErr
	}

	if !exists {
		if strings.TrimSpace(responsibility) == "" {
			return Snapshot{}, &ProtocolError{Code: "INVALID", Text: "responsibility is required for new agent"}
		}
	} else {
		if responsibility != "" {
			return Snapshot{}, &ProtocolError{Code: "INVALID", Text: "responsibility must be omitted for existing agent. To change responsibility unregister and re-register under the same name"}
		}
		responsibility = existing.Responsibility
	}

	if err := c.acquireMembership(name); err != nil {
		return Snapshot{}, err
	}
	defer func() {
		if releaseErr := c.releaseWrite("_registry/membership", name); err == nil && releaseErr != nil {
			err = releaseErr
		}
	}()

	if exists {
		c.Agent = name
		if err := c.ensureRosterRecord(name, responsibility); err != nil {
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
	}

	c.Agent = name
	agent := Agent{Name: name, Responsibility: responsibility, Timestamp: time.Now().UTC()}
	if err := writeJSONAtomic(registrationPath, agent); err != nil {
		return Snapshot{}, err
	}
	if err := writeJSONAtomic(c.subscriptionPath(name), Subscription{TopicGroups: []string{}, Topics: []string{}}); err != nil {
		_ = removeEventually(registrationPath)
		return Snapshot{}, err
	}
	if err := c.ensureRosterRecord(name, responsibility); err != nil {
		_ = removeEventually(registrationPath)
		return Snapshot{}, err
	}
	snapshot, err = c.FullSnapshot()
	if err != nil {
		_ = removeEventually(registrationPath)
		return Snapshot{}, err
	}
	if _, err := c.publishEvent("join", "registry", name, nil); err != nil {
		return snapshot, err
	}
	return snapshot, nil
}

// Leave removes an agent after any active transaction has been resolved.
func (c *Client) Leave(name string) (err error) {
	releaseOperation, err := c.beginOperation()
	if err != nil {
		return err
	}
	defer func() {
		if releaseErr := releaseOperation(); err == nil {
			err = releaseErr
		}
	}()
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
		return &ProtocolError{Code: "LOCKED", Agent: name, Text: "agent has an active transaction; commit or abort it before leaving"}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return statErr
	}
	// Strike the roster record before removing anything else, so a crash
	// partway through deregistration never leaves the collaboration/agents
	// index mapping unreachable.
	if err := c.strikeRosterRecord(name); err != nil {
		return err
	}
	if err := removeEventually(path); err != nil {
		return err
	}
	if err := removeEventually(c.subscriptionPath(name)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	_, err = c.publishEvent("leave", "registry", name, nil)
	return err
}

// ListAgents returns the complete registry ordered by name.
func (c *Client) ListAgents() ([]Agent, error) {
	releaseOperation, err := c.beginOperation()
	if err != nil {
		return nil, err
	}
	defer releaseOperation()
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

// RosterEntry is a registered agent together with whether a signal addressed
// to it would reach a turn right now. Wakeability is not a field on the
// persisted Agent record and must not become one: registration is durable and
// self-declared, while wakeability is observed at the moment of asking and
// goes stale the instant a stream dies.
type RosterEntry struct {
	Agent
	Wakeable bool `json:"wakeable"`
	PID      int  `json:"pid,omitempty"`
}

// Roster lists the registry the way an agent choosing whom to address needs to
// see it -- not who signed up, but who would answer. ListAgents remains the
// bare membership answer because delivery resolves recipients on every
// publication and must not pay for a process probe per agent to do it.
//
// One unreadable guard record fails the whole listing rather than marking that
// agent not wakeable. Reporting a corrupt record as a quiet false is the one
// answer that must never be invented here: it is indistinguishable from the
// truth, and it is the exact claim -- "nobody is listening" -- that would send
// a caller past a teammate who is. Failing loud costs visibility into the
// other entries, which is recoverable; a fabricated false is not.
func (c *Client) Roster() ([]RosterEntry, error) {
	agents, err := c.ListAgents()
	if err != nil {
		return nil, err
	}
	roster := make([]RosterEntry, 0, len(agents))
	for _, agent := range agents {
		pid, err := c.liveWatchOwner(agent.Name)
		if err != nil {
			return nil, err
		}
		roster = append(roster, RosterEntry{Agent: agent, Wakeable: pid > 0, PID: pid})
	}
	return roster, nil
}

// FullSnapshot returns all agents and every current record.
func (c *Client) FullSnapshot() (Snapshot, error) {
	releaseOperation, err := c.beginOperation()
	if err != nil {
		return Snapshot{}, err
	}
	defer releaseOperation()
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
	result := Snapshot{Agents: agents, Topics: map[string][]Record{}, heads: map[string]int64{}}
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
			topicName := group.Name() + "/" + topic.Name()
			release, err := c.acquireRead(topicName)
			if err != nil {
				return Snapshot{}, err
			}
			history, err := c.readHistory(topicName)
			releaseErr := release()
			if err != nil {
				return Snapshot{}, err
			}
			if releaseErr != nil {
				return Snapshot{}, releaseErr
			}
			records := materialize(history)
			if len(records) > 0 {
				result.Topics[topicName] = records
				result.heads[topicName] = history[len(history)-1].Sequence
			}
		}
	}
	return result, nil
}

// AcknowledgeSnapshot records the state delivered by Register. As with delta
// acknowledgement, CLI callers invoke it only after stdout accepts the result.
func (c *Client) AcknowledgeSnapshot(snapshot Snapshot) error {
	releaseOperation, err := c.beginOperation()
	if err != nil {
		return err
	}
	defer releaseOperation()
	if err := c.validateProtocol(); err != nil {
		return err
	}
	agent, err := c.requireAgent()
	if err != nil {
		return err
	}
	return c.updateCursor(agent, func(cursor *Cursor) {
		for topic, head := range snapshot.heads {
			if head > cursor.Topics[topic] {
				cursor.Topics[topic] = head
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
		if event.Summary.Topic == "registry" && event.Summary.Agent == agent {
			latest = event.Summary.Type
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

// rosterIndexRecord is the durable mapping from a registered agent's name to
// its record index on the collaboration/agents topic. It lives outside
// registry/ (which ListAgents enumerates as agent identity files) and
// outside the topic history itself (record indexes are Conductor-assigned
// and carry no agent identity of their own), so recovering "which index is
// mine" after a crash needs this small side-index rather than a scan of
// topic text.
type rosterIndexRecord struct {
	Index int64 `json:"index"`
}

func (r *rosterIndexRecord) validate() error {
	if r.Index <= 0 {
		return errors.New("invalid collaboration/agents index state")
	}
	return nil
}

func (c *Client) rosterIndexPath(agent string) string {
	return filepath.Join(c.Home, "state", "collaboration-agents-index", agent+".json")
}

func (c *Client) loadRosterIndex(agent string) (int64, error) {
	var record rosterIndexRecord
	err := readJSON(c.rosterIndexPath(agent), &record)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return record.Index, nil
}

func (c *Client) saveRosterIndex(agent string, index int64) error {
	return writeJSONAtomic(c.rosterIndexPath(agent), rosterIndexRecord{Index: index})
}

func (c *Client) removeRosterIndex(agent string) error {
	return removeEventually(c.rosterIndexPath(agent))
}

func rosterText(name, responsibility string) string {
	return name + ": " + responsibility
}

// ensureRosterRecord makes sure name has a current record on
// collaboration/agents, publishing one if (and only if) the durable index
// mapping shows none exists yet. It is idempotent so Register can call it
// both for a brand-new agent and when repairing a partially completed
// registration. The caller must already hold the agent's membership lock and
// have c.Agent set to name.
func (c *Client) ensureRosterRecord(name, responsibility string) error {
	index, err := c.loadRosterIndex(name)
	if err != nil {
		return err
	}
	if index > 0 {
		return nil
	}
	if err := c.beginLocked(name, collaborationAgentsTopic); err != nil {
		return err
	}
	record, err := c.StagePut(rosterText(name, responsibility))
	if err != nil {
		if abortErr := c.Abort(); abortErr != nil {
			return errors.Join(err, abortErr)
		}
		return err
	}
	if _, err := c.commit(); err != nil {
		return err
	}
	return c.saveRosterIndex(name, record.Index)
}

// strikeRosterRecord strikes name's current collaboration/agents record, if
// the durable index mapping still shows one exists, and then clears the
// mapping. It is a no-op when no record is known (nothing was ever
// published, or a previous call already struck and cleared it). The caller
// must already hold the agent's membership lock and have c.Agent set to
// name.
func (c *Client) strikeRosterRecord(name string) error {
	index, err := c.loadRosterIndex(name)
	if err != nil {
		return err
	}
	if index == 0 {
		return nil
	}
	if err := c.beginLocked(name, collaborationAgentsTopic); err != nil {
		return err
	}
	if _, err := c.StageStrike(index); err != nil {
		if abortErr := c.Abort(); abortErr != nil {
			return errors.Join(err, abortErr)
		}
		return err
	}
	if _, err := c.commit(); err != nil {
		return err
	}
	return c.removeRosterIndex(name)
}
