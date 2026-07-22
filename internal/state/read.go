package state

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Get reads a current record range, the full current topic, or unread
// subscribed content.
func (c *Client) Get(request ReadRequest) (ReadResult, error) {
	if err := c.validateProtocol(); err != nil {
		return ReadResult{}, err
	}
	agent, err := c.requireAgent()
	if err != nil {
		return ReadResult{}, err
	}
	if err := validTopic(request.Topic); err != nil {
		return ReadResult{}, err
	}
	if request.RecordIndex < 0 || request.Start < 0 || request.End < 0 {
		return ReadResult{}, errors.New("record indexes must not be negative")
	}
	if request.End > 0 && request.End < request.Start {
		return ReadResult{}, errors.New("record range end must be greater than or equal to start")
	}
	if request.Limit < 0 {
		return ReadResult{}, errors.New("read limit must not be negative")
	}
	release, err := c.acquireRead(request.Topic)
	if err != nil {
		return ReadResult{}, err
	}
	history, err := c.readHistory(request.Topic)
	if err != nil {
		_ = release()
		return ReadResult{}, err
	}
	if err := release(); err != nil {
		return ReadResult{}, err
	}
	result := ReadResult{Topic: request.Topic, record: request.RecordIndex, Records: []Record{}, Publications: []Publication{}}
	switch request.Mode {
	case ReadRange:
		result.Mode = "range"
		start, end := request.Start, request.End
		if request.RecordIndex > 0 {
			start, end = request.RecordIndex, request.RecordIndex
		}
		for _, record := range materialize(history) {
			if record.Index < start || (end > 0 && record.Index > end) {
				continue
			}
			result.Records = append(result.Records, record)
			if request.Limit > 0 && len(result.Records) == request.Limit {
				break
			}
		}
		if request.RecordIndex > 0 && len(result.Records) == 0 {
			return ReadResult{}, &ProtocolError{Code: "NOT_FOUND", Text: "record does not exist"}
		}
	case ReadFull:
		result.Mode = "full"
		result.Records = materialize(history)
	case ReadDelta:
		result.Mode = "delta"
		subscribed, err := c.isSubscribed(agent, request.Topic)
		if err != nil {
			return ReadResult{}, err
		}
		if !subscribed {
			return result, nil
		}
		cursor, err := c.loadCursor(agent)
		if err != nil {
			return ReadResult{}, err
		}
		from := cursor.Topics[recordSlot(request.Topic, request.RecordIndex)] + 1
		for _, publication := range history {
			if publication.Sequence < from || (request.throughSequence > 0 && publication.Sequence > request.throughSequence) {
				continue
			}
			changed := filterRecords(publication.Records, request.RecordIndex)
			if len(changed) == 0 {
				continue
			}
			if request.Limit > 0 && publicationRecordCount(result.Publications)+len(changed) > request.Limit {
				if len(result.Publications) == 0 {
					return ReadResult{}, errors.New("--limit is smaller than the next atomic record change")
				}
				break
			}
			publication.Records = changed
			result.Publications = append(result.Publications, publication)
			result.maxSequence = publication.Sequence
		}
	default:
		return ReadResult{}, errors.New("unknown read mode")
	}
	return result, nil
}

// AcknowledgeRead records the internal sequence through which delta content was
// accepted. Range and full reads never change delta progress.
func (c *Client) AcknowledgeRead(result ReadResult) error {
	if err := c.validateProtocol(); err != nil {
		return err
	}
	if result.Mode != "delta" || result.maxSequence == 0 {
		return nil
	}
	agent, err := c.requireAgent()
	if err != nil {
		return err
	}
	slot := recordSlot(result.Topic, result.record)
	return c.updateCursor(agent, func(cursor *Cursor) {
		if result.maxSequence > cursor.Topics[slot] {
			cursor.Topics[slot] = result.maxSequence
		}
	})
}

func (c *Client) topicDir(topic string) (string, error) {
	if err := validTopic(topic); err != nil {
		return "", err
	}
	parts := strings.Split(topic, "/")
	return filepath.Join(c.Home, "topics", parts[0], parts[1]), nil
}

func (c *Client) readHistory(topic string) ([]Publication, error) {
	dir, err := c.topicDir(topic)
	if err != nil {
		return nil, err
	}
	var head struct {
		Sequence int64 `json:"sequence"`
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
				return nil, errors.New("topic history exists without a head")
			}
		}
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	if head.Sequence <= 0 {
		return nil, errors.New("invalid topic head state")
	}
	entries, err := os.ReadDir(filepath.Join(dir, "history"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, errors.New("topic head exists without history")
	}
	if err != nil {
		return nil, err
	}
	result := make([]Publication, 0, len(entries))
	seen := make(map[int64]bool, len(entries))
	seenHead := false
	for _, file := range entries {
		if file.IsDir() || filepath.Ext(file.Name()) != ".json" {
			continue
		}
		var entry Publication
		if err := readJSON(filepath.Join(dir, "history", file.Name()), &entry); err != nil {
			return nil, err
		}
		if entry.Sequence <= 0 || file.Name() != indexName(entry.Sequence) {
			return nil, fmt.Errorf("invalid history identity in %s", file.Name())
		}
		if entry.Topic != topic || seen[entry.Sequence] || entry.Sequence > head.Sequence {
			return nil, fmt.Errorf("invalid history entry %s", file.Name())
		}
		seen[entry.Sequence] = true
		seenHead = seenHead || entry.Sequence == head.Sequence
		result = append(result, entry)
	}
	if !seenHead {
		return nil, fmt.Errorf("topic head %d has no history entry", head.Sequence)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Sequence < result[j].Sequence })
	return result, nil
}

func filterRecords(records []Record, index int64) []Record {
	if index == 0 {
		return append([]Record(nil), records...)
	}
	for _, record := range records {
		if record.Index == index {
			return []Record{record}
		}
	}
	return nil
}

func materializeMap(history []Publication) map[int64]Record {
	records := map[int64]Record{}
	for _, entry := range history {
		for _, record := range entry.Records {
			records[record.Index] = record
		}
	}
	return records
}

func materialize(history []Publication) []Record {
	return sortedRecords(materializeMap(history))
}

func publicationRecordCount(publications []Publication) int {
	count := 0
	for _, publication := range publications {
		count += len(publication.Records)
	}
	return count
}
