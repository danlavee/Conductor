package state

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// DefaultReadLimit caps how many records get/watch return when the caller
// doesn't pass an explicit --limit, so an unbounded backlog or topic can't
// flood a caller's context in one response. An explicit --limit is always
// honored as given, never re-capped by this default.
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
		from := cursor.Topics[recordCursorSlot(request.Topic, request.RecordIndex)] + 1
		limit := request.Limit
		if limit == 0 {
			limit = DefaultReadLimit
		}
		var candidates []Publication
		total := 0
		for _, publication := range history {
			if publication.Sequence < from || (request.throughSequence > 0 && publication.Sequence > request.throughSequence) {
				continue
			}
			changed := filterRecords(publication.Records, request.RecordIndex)
			if len(changed) == 0 {
				continue
			}
			publication.Records = changed
			candidates = append(candidates, publication)
			total += len(changed)
		}
		included := 0
		for _, publication := range candidates {
			// The first qualifying publication is always included whole, even
			// if it alone exceeds limit -- a publication is delivered atomically
			// and can't be split across two delta reads.
			if included > 0 && included+len(publication.Records) > limit {
				break
			}
			result.Publications = append(result.Publications, publication)
			result.maxSequence = publication.Sequence
			included += len(publication.Records)
		}
		result.Remaining = total - included
	default:
		return ReadResult{}, errors.New("unknown read mode")
	}
	if result.Remaining > 0 {
		result.DefaultLimit = DefaultReadLimit
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
	slot := recordCursorSlot(result.Topic, result.record)
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
	path := filepath.Join(dir, "history.jsonl")
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var result []Publication
	scanner := bufio.NewScanner(file)
	const maxTokenSize = 10 * 1024 * 1024
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, maxTokenSize)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var entry Publication
		if err := json.Unmarshal(line, &entry); err != nil {
			return nil, fmt.Errorf("corrupt history entry: %w", err)
		}
		result = append(result, entry)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
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
