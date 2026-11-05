package state

import (
	"encoding/base64"
	"errors"
	"path/filepath"
	"time"
)

// The collaboration topics reach every registered agent, with no subscription
// and no opt-out: the team's rules, its roster, and its broadcast channel.
const (
	collaborationRulesTopic     = "collaboration/rules"
	collaborationAgentsTopic    = "collaboration/agents"
	collaborationBroadcastTopic = "collaboration/broadcast"
)

// privateGroup holds one topic per agent, private/<name>, which reaches that
// agent alone. It is how one agent addresses another on a bus that otherwise
// has no addressees.
const privateGroup = "private"

func privateTopic(agent string) string { return privateGroup + "/" + agent }

// automaticTopics lists what reaches agent without subscribing: the
// collaboration topics and its own private topic. Everything else stays
// subscription-gated.
func automaticTopics(agent string) []string {
	return []string{collaborationRulesTopic, collaborationAgentsTopic, collaborationBroadcastTopic, privateTopic(agent)}
}

// Op is one record change within a publication: a zero Index creates a new
// record, a positive one replaces the text of an existing record.
type Op struct {
	Index int64
	Text  string
}

func (c *Client) Put(topic, text string) (Dispatch, error) {
	return c.Publish(topic, []Op{{Text: text}})
}

func (c *Client) Edit(topic string, index int64, text string) (Dispatch, error) {
	if index <= 0 {
		return Dispatch{}, errors.New("record index must be positive")
	}
	return c.Publish(topic, []Op{{Index: index, Text: text}})
}

// Publish applies every op to topic as one publication and answers with what
// publishing it did, recipients included.
//
// The topic's mutex makes the publication atomic with respect to every other
// writer and reader of the topic, and gives it the topic's next sequence. The
// append is both the publication and its wake: a watcher finds it in history.
func (c *Client) Publish(topic string, ops []Op) (Dispatch, error) {
	if err := c.validateProtocol(); err != nil {
		return Dispatch{}, err
	}
	agent, err := c.requireAgent()
	if err != nil {
		return Dispatch{}, err
	}
	if err := validTopic(topic); err != nil {
		return Dispatch{}, err
	}
	if len(ops) == 0 {
		return Dispatch{}, errors.New("publication has no records")
	}
	release, err := c.lockResource(topic)
	if err != nil {
		return Dispatch{}, err
	}
	defer release()

	history, err := c.readHistory(topic)
	if err != nil {
		return Dispatch{}, err
	}
	current := materializeMap(history)
	var last int64
	for index := range current {
		last = max(last, index)
	}
	records := make(map[int64]Record, len(ops))
	for _, op := range ops {
		switch {
		case op.Index == 0:
			last++
			records[last] = Record{Index: last, Text: op.Text}
		case current[op.Index].Index == 0:
			return Dispatch{}, &ProtocolError{Code: "NOT_FOUND", Text: "record does not exist"}
		default:
			records[op.Index] = Record{Index: op.Index, Text: op.Text}
		}
	}
	sequence := int64(1)
	if len(history) > 0 {
		sequence = history[len(history)-1].Sequence + 1
	}
	recipients, err := c.readersOf(topic, agent)
	if err != nil {
		return Dispatch{}, err
	}
	publication := Publication{
		Sequence: sequence, Topic: topic, Agent: agent,
		Timestamp: time.Now().UTC(), Records: sortedRecords(records),
	}
	dir, err := c.topicDir(topic)
	if err != nil {
		return Dispatch{}, err
	}
	// Only the publication is stored. Recipients answer who this act reaches
	// now, which the next subscription changes; history must not carry it.
	if err := appendJSONL(filepath.Join(dir, "history.jsonl"), publication); err != nil {
		return Dispatch{}, err
	}
	return Dispatch{Publication: publication, Recipients: recipients}, nil
}

// lockResource serializes everything that touches one resource: a topic's
// history, or the membership of the registry.
func (c *Client) lockResource(resource string) (func() error, error) {
	name := base64.RawURLEncoding.EncodeToString([]byte(resource))
	return c.acquireStateMutex(filepath.Join(c.Home, "locks", name+".mutex"))
}
