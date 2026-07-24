package state

import (
	"bufio"
	"bytes"
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

// Watch blocks until at least one unread signal is available, then returns
// every currently pending unread signal, in the order they were discovered.
func (c *Client) Watch() ([]Summary, error) {
	return c.WatchContext(context.Background())
}

// WatchContext returns every currently pending unread signal and then exits.
// An SDK trigger wrapper can use the context for cancellation and submit each
// signal through its agent runtime.
func (c *Client) WatchContext(ctx context.Context) ([]Summary, error) {
	return c.WatchSinceContext(ctx, 0)
}

// WatchSinceContext persists since as a discard floor, then returns every
// currently pending unread signal above it, in the order they were
// discovered -- a real backlog drains in one call instead of one rearm per
// publication. More may still arrive after this call returns, so the caller
// must still rearm.
func (c *Client) WatchSinceContext(ctx context.Context, since int64) ([]Summary, error) {
	if err := c.validateProtocol(); err != nil {
		return nil, err
	}
	if since < 0 {
		return nil, errors.New("watch sequence must not be negative")
	}
	agent, err := c.requireAgent()
	if err != nil {
		return nil, err
	}
	if since > 0 {
		if err := c.updateCursor(agent, func(cursor *Cursor) {
			if since > cursor.Summary {
				cursor.Summary = since
			}
			cursor.SummaryRanges = acknowledgeThrough(cursor.SummaryRanges, since)
		}); err != nil {
			return nil, err
		}
	}
	cursor, err := c.loadCursor(agent)
	if err != nil {
		return nil, err
	}
	journalToken := ""
	for {
		pending, scannedTo, err := c.pendingInboxSummaries(agent, cursor, since)
		if err != nil {
			return nil, err
		}
		if scannedTo > cursor.InboxOffset {
			if err := c.updateCursor(agent, func(current *Cursor) {
				if scannedTo > current.InboxOffset {
					current.InboxOffset = scannedTo
				}
			}); err != nil {
				return nil, err
			}
			cursor.InboxOffset = scannedTo
		}
		currentToken, err := c.eventChangeToken()
		if err != nil {
			return nil, err
		}
		if currentToken != journalToken {
			events, err := c.unreadEventsAfter(cursor.SummaryRanges, since)
			if err != nil {
				return nil, err
			}
			seen := make(map[int64]bool, len(pending))
			for _, summary := range pending {
				seen[summary.Sequence] = true
			}
			for _, event := range events {
				if contains(event.Recipients, agent) && !seen[event.Summary.Sequence] {
					pending = append(pending, event.Summary)
					seen[event.Summary.Sequence] = true
				}
			}
			journalToken = currentToken
		}
		if len(pending) > 0 {
			return pending, nil
		}
		timer := time.NewTimer(c.PollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
		if _, err := os.Stat(filepath.Join(c.Home, "registry", agent+".json")); errors.Is(err, os.ErrNotExist) {
			return nil, &ProtocolError{Code: "NOT_FOUND", Text: "registered agent does not exist"}
		} else if err != nil {
			return nil, err
		}
		cursor, err = c.loadCursor(agent)
		if err != nil {
			return nil, err
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
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		_ = release()
		return err
	}
	if err = trimUnterminatedInboxTail(file); err == nil {
		if _, err = file.Seek(0, io.SeekEnd); err == nil {
			var written int
			written, err = file.Write(line)
			if err == nil && written != len(line) {
				err = io.ErrShortWrite
			}
		}
	}
	if err == nil {
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

// pendingInboxSummaries: a malformed or unrecognized line is skipped,
// matching appendInbox's best-effort delivery guarantee; an unterminated
// trailing line (a write caught mid-flight) halts the scan without consuming
// it, so a later appendInbox can self-heal it via trimUnterminatedInboxTail.
func (c *Client) pendingInboxSummaries(agent string, cursor Cursor, since int64) ([]Summary, int64, error) {
	file, err := os.Open(filepath.Join(c.Home, "inbox", agent))
	if errors.Is(err, os.ErrNotExist) {
		return nil, cursor.InboxOffset, nil
	}
	if err != nil {
		return nil, cursor.InboxOffset, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, cursor.InboxOffset, err
	}
	offset := cursor.InboxOffset
	if offset > info.Size() {
		offset = 0
	}
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return nil, offset, err
	}
	reader := bufio.NewReader(file)
	var pending []Summary
	for {
		line, readErr := reader.ReadBytes('\n')
		if len(line) == 0 && errors.Is(readErr, io.EOF) {
			return pending, offset, nil
		}
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return pending, offset, readErr
		}
		if line[len(line)-1] != '\n' {
			return pending, offset, nil
		}
		offset += int64(len(line))
		var summary Summary
		if err := json.Unmarshal(line, &summary); err != nil {
			continue
		}
		if err := validateSummary(&summary); err != nil {
			continue
		}
		if summary.Sequence > since && !indexAcknowledged(cursor.SummaryRanges, summary.Sequence) {
			pending = append(pending, summary)
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
	info, err := file.Stat()
	if err != nil {
		return 0, err
	}
	offset := cursor.InboxOffset
	if offset > info.Size() {
		offset = 0
	}
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return 0, err
	}
	reader := bufio.NewReader(file)
	for {
		line, readErr := reader.ReadBytes('\n')
		if len(line) == 0 && errors.Is(readErr, io.EOF) {
			return offset, nil
		}
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return 0, readErr
		}
		if line[len(line)-1] != '\n' {
			return offset, nil
		}
		offset += int64(len(line))
		var candidate Summary
		if err := json.Unmarshal(line, &candidate); err != nil {
			continue
		}
		if candidate.Sequence == index {
			return offset, nil
		}
	}
}

func trimUnterminatedInboxTail(file *os.File) error {
	info, err := file.Stat()
	if err != nil || info.Size() == 0 {
		return err
	}
	last := []byte{0}
	if _, err := file.ReadAt(last, info.Size()-1); err != nil {
		return err
	}
	if last[0] == '\n' {
		return nil
	}

	// High-Performance backward search in 4KB chunks
	var size int64 = 0
	buf := make([]byte, 4096)
	fileSize := info.Size()
	offset := fileSize

	for offset > 0 {
		chunkSize := int64(len(buf))
		if offset < chunkSize {
			chunkSize = offset
		}
		offset -= chunkSize

		if _, err := file.ReadAt(buf[:chunkSize], offset); err != nil {
			return err
		}

		idx := bytes.LastIndexByte(buf[:chunkSize], '\n')
		if idx != -1 {
			size = offset + int64(idx) + 1
			break
		}
	}

	if err := file.Truncate(size); err != nil {
		return err
	}
	_, err = file.Seek(0, io.SeekEnd)
	return err
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
