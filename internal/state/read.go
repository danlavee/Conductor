package state

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Get reads published state in delta, historical, or full mode.
func (c *Client) Get(request ReadRequest) (ReadResult, error) {
	if err := c.validateProtocol(); err != nil {
		return ReadResult{}, err
	}
	agent, err := c.requireAgent()
	if err != nil {
		return ReadResult{}, err
	}
	if err := validResource(request.Resource); err != nil {
		return ReadResult{}, err
	}
	if request.Key != "" {
		if err := validName(request.Key); err != nil {
			return ReadResult{}, err
		}
	}
	release, err := c.acquireRead(request.Resource)
	if err != nil {
		return ReadResult{}, err
	}
	history, err := c.readHistory(request.Resource)
	if err != nil {
		_ = release()
		return ReadResult{}, err
	}
	if err := release(); err != nil {
		return ReadResult{}, err
	}
	result := ReadResult{Resource: request.Resource, key: request.Key}
	switch request.Mode {
	case ReadHistorical:
		result.Mode, result.From, result.To = "historical", request.From, request.To
		for _, change := range history {
			if change.Index < request.From || (request.To > 0 && change.Index > request.To) || !containsKey(change.Messages, request.Key) {
				continue
			}
			result.History = append(result.History, filterChange(change, request.Key))
		}
	case ReadFull:
		result.Mode = "full"
		result.Messages = materialize(history, request.Key, 0)
		if len(result.Messages) == 0 && request.Key != "" {
			return ReadResult{}, &ProtocolError{Code: "NOT_FOUND", Text: "message does not exist"}
		}
	case ReadDelta:
		cursor, err := c.loadCursor(agent)
		if err != nil {
			return ReadResult{}, err
		}
		slot := cursorSlot(request.Resource, request.Key)
		from := cursor.Resources[slot] + 1
		result.Mode, result.From = "delta", from
		for _, change := range history {
			if change.Index < from || !containsKey(change.Messages, request.Key) {
				continue
			}
			result.History = append(result.History, filterChange(change, request.Key))
			if change.Index > result.maxIndex {
				result.maxIndex = change.Index
			}
		}
	default:
		return ReadResult{}, errors.New("unknown read mode")
	}
	return result, nil
}

// AcknowledgeRead persists the last successfully delivered delta index. Call it
// only after the result has been written to the SDK consumer or CLI stdout.
func (c *Client) AcknowledgeRead(result ReadResult) error {
	if err := c.validateProtocol(); err != nil {
		return err
	}
	if result.Mode != "delta" || result.maxIndex == 0 {
		return nil
	}
	agent, err := c.requireAgent()
	if err != nil {
		return err
	}
	slot := cursorSlot(result.Resource, result.key)
	return c.updateCursor(agent, func(cursor *Cursor) {
		if result.maxIndex > cursor.Resources[slot] {
			cursor.Resources[slot] = result.maxIndex
		}
	})
}

func (c *Client) resourceDir(resource string) (string, error) {
	if err := validResource(resource); err != nil {
		return "", err
	}
	parts := strings.Split(resource, "/")
	return filepath.Join(c.Home, "topics", parts[0], parts[1]), nil
}

func (c *Client) readHistory(resource string) ([]Publication, error) {
	dir, err := c.resourceDir(resource)
	if err != nil {
		return nil, err
	}
	var head struct {
		Index int64 `json:"index"`
	}
	if err := readJSON(filepath.Join(dir, "head.json"), &head); errors.Is(err, os.ErrNotExist) {
		entries, readErr := os.ReadDir(filepath.Join(dir, "history"))
		if errors.Is(readErr, os.ErrNotExist) {
			return nil, nil
		}
		if readErr != nil {
			return nil, readErr
		}
		for _, entry := range entries {
			if !entry.IsDir() && filepath.Ext(entry.Name()) == ".json" {
				return nil, errors.New("resource history exists without a head")
			}
		}
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	if head.Index <= 0 {
		return nil, errors.New("invalid resource head state")
	}
	entries, err := os.ReadDir(filepath.Join(dir, "history"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, errors.New("resource head exists without history")
	}
	if err != nil {
		return nil, err
	}
	result := make([]Publication, 0, len(entries))
	seen := make(map[int64]bool, len(entries))
	seenHead := false
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		var publication Publication
		if err := readJSON(filepath.Join(dir, "history", entry.Name()), &publication); err != nil {
			return nil, err
		}
		if publication.Index <= 0 || entry.Name() != indexName(publication.Index) {
			return nil, fmt.Errorf("invalid history identity in %s", entry.Name())
		}
		if publication.Resource != resource || publication.Agent == "" || publication.Timestamp.IsZero() || len(publication.Messages) == 0 {
			return nil, fmt.Errorf("invalid history entry %s", entry.Name())
		}
		if seen[publication.Index] {
			return nil, fmt.Errorf("duplicate history index %d", publication.Index)
		}
		seen[publication.Index] = true
		for key := range publication.Messages {
			if err := validName(key); err != nil {
				return nil, fmt.Errorf("invalid history entry %s: %w", entry.Name(), err)
			}
		}
		if publication.Index > head.Index {
			return nil, fmt.Errorf("history index %d is beyond resource head %d", publication.Index, head.Index)
		}
		seenHead = seenHead || publication.Index == head.Index
		result = append(result, publication)
	}
	if !seenHead {
		return nil, fmt.Errorf("resource head %d has no history entry", head.Index)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Index < result[j].Index })
	return result, nil
}

func containsKey(values map[string]MessageMutation, key string) bool {
	_, ok := values[key]
	return key == "" || ok
}

func filterChange(change Publication, key string) Publication {
	if key == "" {
		return change
	}
	change.Messages = map[string]MessageMutation{key: change.Messages[key]}
	return change
}

func cursorSlot(resource, key string) string {
	if key == "" {
		return resource
	}
	return resource + "#" + key
}

func materializeLatest(history []Publication, key string, through int64) map[string]materializedMessage {
	messages := map[string]materializedMessage{}
	for _, publication := range history {
		if through > 0 && publication.Index > through {
			break
		}
		for changedKey, mutation := range publication.Messages {
			if key != "" && key != changedKey {
				continue
			}
			message := materializedMessage{Message: Message{Key: changedKey, Agent: publication.Agent, Index: publication.Index, Timestamp: publication.Timestamp}}
			if mutation.Operation == MutationScratch {
				message.Scratched = true
			} else {
				message.Kind, message.Payload = mutation.Kind, *mutation.Payload
			}
			messages[changedKey] = message
		}
	}
	return messages
}

func materialize(history []Publication, key string, through int64) []Message {
	latest := materializeLatest(history, key, through)
	keys := make([]string, 0, len(latest))
	for messageKey, message := range latest {
		if !message.Scratched {
			keys = append(keys, messageKey)
		}
	}
	sort.Strings(keys)
	result := make([]Message, 0, len(keys))
	for _, messageKey := range keys {
		result = append(result, latest[messageKey].Message)
	}
	return result
}
