package state

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/danlavee/Conductor/internal/platform"
)

// Join adds or reconnects an agent and returns the full current snapshot.
func (c *Client) Join(name, responsibility string) (snapshot Snapshot, err error) {
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
		// Leaving strikes the roster line through, so that edit must fit too.
		if utf8.RuneCountInString(retiredRosterText(name, responsibility)) > MaxRecordText {
			return Snapshot{}, &ProtocolError{Code: "INVALID", Text: fmt.Sprintf("responsibility is too long: the roster line holds at most %d characters", MaxRecordText)}
		}
	} else {
		if responsibility != "" {
			return Snapshot{}, &ProtocolError{Code: "INVALID", Text: "responsibility must be omitted for existing agent. To change responsibility unregister and re-register under the same name"}
		}
		responsibility = existing.Responsibility
	}

	release, err := c.lockResource(membershipResource)
	if err != nil {
		return Snapshot{}, err
	}
	defer release()

	if exists {
		c.Agent = name
		if err := c.ensureRosterRecord(name, responsibility); err != nil {
			return Snapshot{}, err
		}
		return c.FullSnapshot()
	}

	c.Agent = name
	agent := Agent{Name: name, Responsibility: responsibility, Timestamp: time.Now().UTC()}
	if err := writeJSONAtomic(registrationPath, agent); err != nil {
		return Snapshot{}, err
	}
	if err := c.ensureRosterRecord(name, responsibility); err != nil {
		_ = platform.Remove(registrationPath)
		return Snapshot{}, err
	}
	snapshot, err = c.FullSnapshot()
	if err != nil {
		_ = platform.Remove(registrationPath)
		return Snapshot{}, err
	}
	return snapshot, nil
}

// Leave removes an agent.
func (c *Client) Leave(name string) error {
	if err := c.validateProtocol(); err != nil {
		return err
	}
	if err := validName(name); err != nil {
		return err
	}
	c.Agent = name
	release, err := c.lockResource(membershipResource)
	if err != nil {
		return err
	}
	defer release()
	path := filepath.Join(c.Home, "registry", name+".json")
	var agent Agent
	if err := readJSON(path, &agent); errors.Is(err, os.ErrNotExist) {
		return &ProtocolError{Code: "NOT_FOUND", Text: "agent does not exist"}
	} else if err != nil {
		return err
	}
	// Retire the roster line while the agent can still publish, so a crash
	// partway through leaves a registration that a repeated leave finishes.
	if err := c.retireRosterRecord(agent); err != nil {
		return err
	}
	if err := platform.Remove(path); err != nil {
		return err
	}
	return platform.Remove(c.subscriptionPath(name))
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

// RosterEntry is a registered agent together with whether a signal addressed
// to it would reach a turn right now. Wakeability is not a field on the
// persisted Agent record and must not become one: registration is durable and
// self-declared, while wakeability is observed at the moment of asking and
// goes stale the instant a stream dies.
//
// Holder says how long the true answer is likely to stay true; see Holder.
type RosterEntry struct {
	Agent
	Wakeable bool   `json:"wakeable"`
	Holder   Holder `json:"holder,omitempty"`
}

// Roster lists the registry the way an agent choosing whom to address needs to
// see it -- not who signed up, but who would answer.
//
// ListAgents remains the bare membership answer for recipient resolution,
// which runs on every publication and must not pay a process probe per agent.
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
		owner, err := c.liveHolder(agent.Name)
		if err != nil {
			return nil, err
		}
		roster = append(roster, RosterEntry{Agent: agent, Wakeable: owner.Holder != "", Holder: owner.Holder})
	}
	return roster, nil
}

// FullSnapshot returns all agents and each topic's newest current records.
func (c *Client) FullSnapshot() (Snapshot, error) {
	if _, err := c.requireAgent(); err != nil {
		return Snapshot{}, err
	}
	agents, err := c.Roster()
	if err != nil {
		return Snapshot{}, err
	}
	result := Snapshot{Agents: agents, Topics: map[string]TopicTail{}, heads: map[string]int64{}}
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
			history, err := c.lockedHistory(topicName)
			if err != nil {
				return Snapshot{}, err
			}
			records, remaining := newest(materialize(history), DefaultReadLimit)
			if len(records) > 0 {
				result.Topics[topicName] = TopicTail{Records: records, Remaining: remaining}
				result.heads[topicName] = history[len(history)-1].Sequence
			}
		}
	}
	return result, nil
}

// AcknowledgeSnapshot records the state delivered by Register. As with delta
// acknowledgement, CLI callers invoke it only after stdout accepts the result.
func (c *Client) AcknowledgeSnapshot(snapshot Snapshot) error {
	agent, err := c.requireAgent()
	if err != nil {
		return err
	}
	return c.acknowledgeHeads(agent, snapshot.heads)
}

// acknowledgeHeads moves each topic's cursor forward to the given head.
func (c *Client) acknowledgeHeads(agent string, heads map[string]int64) error {
	return c.updateCursor(agent, func(cursor *Cursor) {
		for topic, head := range heads {
			cursor.Topics[topic] = max(cursor.Topics[topic], head)
		}
	})
}

// membershipResource serializes Join and Leave across every agent.
const membershipResource = "_registry/membership"

func rosterText(name, responsibility string) string {
	return name + ": " + responsibility
}

func retiredRosterText(name, responsibility string) string {
	return "~~" + rosterText(name, responsibility) + "~~"
}

// rosterRecord answers the index of the agent's live line on
// collaboration/agents, or zero when it has none. The line itself is the only
// record of it: names cannot contain the separator, so the text identifies
// the agent.
func (c *Client) rosterRecord(name, responsibility string) (int64, error) {
	history, err := c.lockedHistory(collaborationAgentsTopic)
	if err != nil {
		return 0, err
	}
	for _, record := range materialize(history) {
		if record.Text == rosterText(name, responsibility) {
			return record.Index, nil
		}
	}
	return 0, nil
}

// ensureRosterRecord publishes the agent's roster line unless it is already
// live, so a join repeated after a crash never publishes it twice. That
// publication is how the other agents learn of the join. The caller must hold
// the membership lock and have c.Agent set to name.
func (c *Client) ensureRosterRecord(name, responsibility string) error {
	index, err := c.rosterRecord(name, responsibility)
	if err != nil || index > 0 {
		return err
	}
	_, err = c.Put(collaborationAgentsTopic, rosterText(name, responsibility))
	return err
}

// retireRosterRecord strikes through the agent's live roster line, which is
// how the other agents learn of the leave, and is a no-op once it has. The
// caller must hold the membership lock and have c.Agent set to the agent.
func (c *Client) retireRosterRecord(agent Agent) error {
	index, err := c.rosterRecord(agent.Name, agent.Responsibility)
	if err != nil || index == 0 {
		return err
	}
	_, err = c.Edit(collaborationAgentsTopic, index, retiredRosterText(agent.Name, agent.Responsibility))
	return err
}
