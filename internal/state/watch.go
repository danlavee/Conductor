package state

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Watch blocks until one unread summary is available, then returns it.
func (c *Client) Watch() (Summary, error) {
	return c.WatchContext(context.Background())
}

// WatchContext returns one unread signal and then exits. An SDK trigger wrapper
// can use the context for cancellation and submit the signal through its agent runtime.
func (c *Client) WatchContext(ctx context.Context) (Summary, error) {
	return c.WatchSinceContext(ctx, 0)
}

// WatchSinceContext persists since as a discard floor, then returns one higher unread summary.
func (c *Client) WatchSinceContext(ctx context.Context, since int64) (Summary, error) {
	if err := c.validateProtocol(); err != nil {
		return Summary{}, err
	}
	if since < 0 {
		return Summary{}, errors.New("watch sequence must not be negative")
	}
	agent, err := c.requireAgent()
	if err != nil {
		return Summary{}, err
	}
	if since > 0 {
		if err := c.updateCursor(agent, func(cursor *Cursor) {
			if since > cursor.Summary {
				cursor.Summary = since
			}
			cursor.SummaryRanges = acknowledgeThrough(cursor.SummaryRanges, since)
		}); err != nil {
			return Summary{}, err
		}
	}
	cursor, err := c.loadCursor(agent)
	if err != nil {
		return Summary{}, err
	}
	journalToken := ""
	for {
		summary, scannedTo, found, err := c.nextInboxSummary(agent, cursor, since)
		if err != nil {
			return Summary{}, err
		}
		if found {
			return summary, nil
		}
		if scannedTo > cursor.InboxOffset {
			if err := c.updateCursor(agent, func(current *Cursor) {
				if scannedTo > current.InboxOffset {
					current.InboxOffset = scannedTo
				}
			}); err != nil {
				return Summary{}, err
			}
			cursor.InboxOffset = scannedTo
		}
		currentToken, err := c.eventChangeToken()
		if err != nil {
			return Summary{}, err
		}
		if currentToken != journalToken {
			events, err := c.unreadEventsAfter(cursor.SummaryRanges, since)
			if err != nil {
				return Summary{}, err
			}
			for _, event := range events {
				if contains(event.Recipients, agent) {
					return event.Summary, nil
				}
			}
			journalToken = currentToken
		}
		timer := time.NewTimer(c.PollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return Summary{}, ctx.Err()
		case <-timer.C:
		}
		if _, err := os.Stat(filepath.Join(c.Home, "registry", agent+".json")); errors.Is(err, os.ErrNotExist) {
			return Summary{}, &ProtocolError{Code: "NOT_FOUND", Text: "registered agent does not exist"}
		} else if err != nil {
			return Summary{}, err
		}
		cursor, err = c.loadCursor(agent)
		if err != nil {
			return Summary{}, err
		}
	}
}

// AcknowledgeSummary advances the wake cursor after its consumer accepts the
// summary. A crash before this checkpoint causes replay, never silent loss.
func (c *Client) AcknowledgeSummary(summary Summary) error {
	if err := c.validateProtocol(); err != nil {
		return err
	}
	if err := validateSummary(&summary); err != nil {
		return err
	}
	agent, err := c.requireAgent()
	if err != nil {
		return err
	}
	offset, err := c.inboxOffsetThrough(agent, summary.Sequence)
	if err != nil {
		return err
	}
	return c.updateCursor(agent, func(cursor *Cursor) {
		if summary.Sequence > cursor.Summary {
			cursor.Summary = summary.Sequence
		}
		cursor.SummaryRanges = acknowledgeIndex(cursor.SummaryRanges, summary.Sequence)
		if offset > cursor.InboxOffset {
			cursor.InboxOffset = offset
		}
	})
}

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

func (c *Client) appendInbox(agent string, line []byte) error {
	guard := filepath.Join(c.Home, "inbox", ".locks", agent+".guard")
	release, err := c.acquireLeaseGuard(guard)
	if err != nil {
		return err
	}
	path := filepath.Join(c.Home, "inbox", agent)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		_ = release()
		return err
	}
	if _, err = file.Write(line); err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if releaseErr := release(); err == nil {
		err = releaseErr
	}
	return err
}

func (c *Client) nextInboxSummary(agent string, cursor Cursor, since int64) (Summary, int64, bool, error) {
	file, err := os.Open(filepath.Join(c.Home, "inbox", agent))
	if errors.Is(err, os.ErrNotExist) {
		return Summary{}, cursor.InboxOffset, false, nil
	}
	if err != nil {
		return Summary{}, cursor.InboxOffset, false, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return Summary{}, cursor.InboxOffset, false, err
	}
	offset := cursor.InboxOffset
	if offset > info.Size() {
		offset = 0
	}
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return Summary{}, offset, false, err
	}
	reader := bufio.NewReader(file)
	for {
		line, readErr := reader.ReadBytes('\n')
		if len(line) == 0 && errors.Is(readErr, io.EOF) {
			return Summary{}, offset, false, nil
		}
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return Summary{}, offset, false, readErr
		}
		offset += int64(len(line))
		var summary Summary
		if err := json.Unmarshal(line, &summary); err != nil {
			return Summary{}, offset, false, fmt.Errorf("malformed inbox line for %s: %w", agent, err)
		}
		if err := validateSummary(&summary); err != nil {
			return Summary{}, offset, false, fmt.Errorf("invalid inbox line for %s: %w", agent, err)
		}
		if summary.Sequence > since && !indexAcknowledged(cursor.SummaryRanges, summary.Sequence) {
			return summary, offset, true, nil
		}
		if errors.Is(readErr, io.EOF) {
			return Summary{}, offset, false, nil
		}
	}
}

func (c *Client) inboxOffsetThrough(agent string, index int64) (int64, error) {
	cursor, err := c.loadCursor(agent)
	if err != nil {
		return 0, err
	}
	file, err := os.Open(filepath.Join(c.Home, "inbox", agent))
	if errors.Is(err, os.ErrNotExist) {
		return cursor.InboxOffset, nil
	}
	if err != nil {
		return 0, err
	}
	defer file.Close()
	if _, err := file.Seek(cursor.InboxOffset, io.SeekStart); err != nil {
		return 0, err
	}
	offset := cursor.InboxOffset
	reader := bufio.NewReader(file)
	for {
		line, readErr := reader.ReadBytes('\n')
		if len(line) == 0 && errors.Is(readErr, io.EOF) {
			return cursor.InboxOffset, nil
		}
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return 0, readErr
		}
		offset += int64(len(line))
		var candidate Summary
		if err := json.Unmarshal(line, &candidate); err != nil {
			return 0, fmt.Errorf("malformed inbox line for %s: %w", agent, err)
		}
		if candidate.Sequence == index {
			return offset, nil
		}
		if errors.Is(readErr, io.EOF) {
			return cursor.InboxOffset, nil
		}
	}
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

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func indexAcknowledged(ranges []IndexRange, index int64) bool {
	for _, interval := range ranges {
		if index < interval.From {
			return false
		}
		if index <= interval.To {
			return true
		}
	}
	return false
}

func acknowledgeIndex(ranges []IndexRange, index int64) []IndexRange {
	ranges = append(append([]IndexRange(nil), ranges...), IndexRange{From: index, To: index})
	return mergeIndexRanges(ranges)
}

func acknowledgeThrough(ranges []IndexRange, index int64) []IndexRange {
	ranges = append(append([]IndexRange(nil), ranges...), IndexRange{From: 1, To: index})
	return mergeIndexRanges(ranges)
}

func mergeIndexRanges(ranges []IndexRange) []IndexRange {
	sort.Slice(ranges, func(i, j int) bool { return ranges[i].From < ranges[j].From })
	merged := ranges[:0]
	for _, interval := range ranges {
		last := len(merged) - 1
		if last >= 0 && interval.From <= merged[last].To+1 {
			if interval.To > merged[last].To {
				merged[last].To = interval.To
			}
			continue
		}
		merged = append(merged, interval)
	}
	return merged
}
