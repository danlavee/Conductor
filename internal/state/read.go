package state

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// DefaultReadLimit caps get/watch responses. Range and delta reads may
// request a larger explicit limit; full reads always use this cap.
const DefaultReadLimit = 20

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
	release, err := c.lockResource(request.Topic)
	if err != nil {
		return ReadResult{}, err
	}
	history, err := c.readHistory(request.Topic)
	if err := errors.Join(err, release()); err != nil {
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
		limit := request.Limit
		if limit == 0 {
			limit = DefaultReadLimit
		}
		matched := 0
		for _, record := range materialize(history) {
			if record.Index < start || (end > 0 && record.Index > end) {
				continue
			}
			matched++
			if len(result.Records) < limit {
				result.Records = append(result.Records, record)
			}
		}
		result.Remaining = matched - len(result.Records)
		if request.RecordIndex > 0 && len(result.Records) == 0 {
			return ReadResult{}, &ProtocolError{Code: "NOT_FOUND", Text: "record does not exist"}
		}
	case ReadFull:
		result.Mode = "full"
		all := materialize(history)
		if len(all) > DefaultReadLimit {
			result.Records = all[:DefaultReadLimit]
			result.Remaining = len(all) - DefaultReadLimit
		} else {
			result.Records = all
		}
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
		read := cursor.Topics[recordCursorSlot(request.Topic, request.RecordIndex)]
		limit := request.Limit
		if limit == 0 {
			limit = DefaultReadLimit
		}
		// The reader's own publications, and those not touching the requested
		// record, are passed over rather than delivered; the cursor still moves
		// past every one scanned before the limit stops the read.
		included, full := 0, false
		for _, publication := range history {
			if publication.Sequence <= read {
				continue
			}
			changed := filterRecords(publication.Records, request.RecordIndex)
			switch {
			case full:
				if publication.Agent != agent {
					result.Remaining += len(changed)
				}
				continue
			case publication.Agent == agent || len(changed) == 0:
			// The first unread publication is always included whole, even if it
			// alone exceeds limit -- a publication can't be split across reads.
			case included > 0 && included+len(changed) > limit:
				full = true
				result.Remaining += len(changed)
				continue
			default:
				publication.Records = changed
				result.Publications = append(result.Publications, publication)
				included += len(changed)
			}
			result.maxSequence = publication.Sequence
		}
	default:
		return ReadResult{}, errors.New("unknown read mode")
	}
	if result.Remaining > 0 {
		result.DefaultLimit = DefaultReadLimit
	}
	return result, nil
}

// AcknowledgeRead records the sequence through which each delta's content was
// accepted. Range and full reads never change delta progress.
func (c *Client) AcknowledgeRead(results ...ReadResult) error {
	if err := c.validateProtocol(); err != nil {
		return err
	}
	agent, err := c.requireAgent()
	if err != nil {
		return err
	}
	return c.updateCursor(agent, func(cursor *Cursor) {
		for _, result := range results {
			slot := recordCursorSlot(result.Topic, result.record)
			if result.Mode == "delta" && result.maxSequence > cursor.Topics[slot] {
				cursor.Topics[slot] = result.maxSequence
			}
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
	path := filepath.Join(dir, "history.jsonl")
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	lines := bytes.Split(data, []byte{'\n'})
	var result []Publication
	// A publication exists once its newline lands. The final element is either
	// empty or a crash-torn tail that never got one, which appendJSONL trims.
	for _, line := range lines[:len(lines)-1] {
		if len(line) == 0 {
			continue
		}
		var entry Publication
		if err := json.Unmarshal(line, &entry); err != nil {
			return nil, fmt.Errorf("corrupt history entry: %w", err)
		}
		result = append(result, entry)
	}
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
