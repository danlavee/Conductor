package state

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
)

// DefaultReadLimit caps get, join (per topic), and watch responses. A get may
// go past it with an explicit limit or a range with an end.
const DefaultReadLimit = 20

// MaxRecordText is the most characters (runes) one record's text may hold.
const MaxRecordText = 2000

// newest keeps a topic's latest limit records -- the ones a reader catching
// up acts on -- and counts what it left behind, which an --end range reaches.
func newest(records []Record, limit int) ([]Record, int) {
	if len(records) <= limit {
		return records, 0
	}
	return records[len(records)-limit:], len(records) - limit
}

// Get reads a current record range, the full current topic, or unread
// subscribed content.
func (c *Client) Get(request ReadRequest) (ReadResult, error) {
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
	if request.RecordIndex > 0 && request.Mode != ReadRange {
		return ReadResult{}, errors.New("a record index reads a range; it cannot be used with --delta or --full")
	}
	history, err := c.lockedHistory(request.Topic)
	if err != nil {
		return ReadResult{}, err
	}
	limit := request.Limit
	switch {
	case limit > 0:
	// A range with an end names its records: all of them are asked for.
	case request.Mode == ReadRange && request.End > 0:
		limit = math.MaxInt
	default:
		limit = DefaultReadLimit
	}
	result := ReadResult{Topic: request.Topic, Records: []Record{}, Publications: []Publication{}}
	switch request.Mode {
	case ReadRange:
		result.Mode = "range"
		start, end := request.Start, request.End
		if request.RecordIndex > 0 {
			start, end = request.RecordIndex, request.RecordIndex
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
		result.Records, result.Remaining = newest(materialize(history), limit)
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
		read := cursor.Topics[request.Topic]
		// The reader's own publications are passed over rather than delivered;
		// the cursor still moves past every one scanned before the limit stops
		// the read.
		included, full := 0, false
		for _, publication := range history {
			if publication.Sequence <= read {
				continue
			}
			size := len(publication.Records)
			switch {
			case full:
				if publication.Agent != agent {
					result.Remaining += size
				}
				continue
			case publication.Agent == agent:
			// The first unread publication is always included whole, even if it
			// alone exceeds limit -- a publication can't be split across reads.
			case included > 0 && included+size > limit:
				full = true
				result.Remaining += size
				continue
			default:
				result.Publications = append(result.Publications, publication)
				included += size
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
	agent, err := c.requireAgent()
	if err != nil {
		return err
	}
	return c.updateCursor(agent, func(cursor *Cursor) {
		for _, result := range results {
			if result.Mode == "delta" && result.maxSequence > cursor.Topics[result.Topic] {
				cursor.Topics[result.Topic] = result.maxSequence
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

// lockedHistory reads a topic's history under the topic's lock.
func (c *Client) lockedHistory(topic string) ([]Publication, error) {
	release, err := c.lockResource(topic)
	if err != nil {
		return nil, err
	}
	history, err := c.readHistory(topic)
	return history, errors.Join(err, release())
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
