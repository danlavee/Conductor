package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

func (c *Client) publishEvent(kind, topic, agent string, recipients []string) (Summary, error) {
	sequence, err := c.nextIndex()
	if err != nil {
		return Summary{}, err
	}
	if recipients == nil {
		recipients, err = c.recipientNames()
		if err != nil {
			return Summary{}, err
		}
	}
	summary := Summary{Type: kind, Topic: topic, Sequence: sequence, Agent: agent}
	return summary, c.writeEvent(Event{Summary: summary, Recipients: recipients})
}

func (c *Client) writeEvent(event Event) error {
	if err := writeJSONAtomic(filepath.Join(c.Home, "events", indexName(event.Summary.Sequence)), event); err != nil {
		return err
	}
	line, err := json.Marshal(event.Summary)
	if err != nil {
		return err
	}
	line = append(line, '\n')
	for _, recipient := range event.Recipients {
		if err := c.appendInbox(recipient, line); err != nil {
			return err
		}
	}
	return nil
}

func (c *Client) unreadEvents(acknowledged []IndexRange) ([]Event, error) {
	return c.unreadEventsAfter(acknowledged, 0)
}

func (c *Client) unreadEventsAfter(acknowledged []IndexRange, since int64) ([]Event, error) {
	entries, err := os.ReadDir(filepath.Join(c.Home, "events"))
	if err != nil {
		return nil, err
	}
	result := []Event{}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		prefix := strings.TrimSuffix(entry.Name(), ".json")
		value, err := strconv.ParseInt(prefix, 10, 64)
		if err != nil || value <= since || indexAcknowledged(acknowledged, value) {
			continue
		}
		var event Event
		if err := readJSON(filepath.Join(c.Home, "events", entry.Name()), &event); err != nil {
			return nil, err
		}
		result = append(result, event)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Summary.Sequence < result[j].Summary.Sequence })
	return result, nil
}

func (c *Client) eventChangeToken() (string, error) {
	var state struct {
		Index int64 `json:"index"`
	}
	if err := readJSON(filepath.Join(c.Home, "state", "index.json"), &state); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if state.Index < 0 {
		return "", errors.New("invalid global index state")
	}
	info, err := os.Stat(filepath.Join(c.Home, "events"))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%d:%d", state.Index, info.ModTime().UnixNano()), nil
}
