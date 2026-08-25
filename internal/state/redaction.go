package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/danlavee/Conductor/internal/platform"
)

// Redact physically deletes records within the inclusive index range [start, end],
// creates a pre-redaction backup, and notifies subscribers.
//
// It answers with the dispatch the notification made, for the reason every
// record verb does: an agent that removed something from a shared topic and
// cannot see who was told has no way to know the removal reached anyone. The
// dispatch carries no records -- nothing was added -- and a zero one means the
// range matched nothing, which the caller could not otherwise tell from a
// redaction that happened.
func (c *Client) Redact(topic string, start, end int64) (Dispatch, error) {
	releaseOperation, err := c.beginOperation()
	if err != nil {
		return Dispatch{}, err
	}
	defer releaseOperation()
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
	if start <= 0 || end <= 0 || end < start {
		return Dispatch{}, errors.New("invalid redaction range")
	}

	releaseMutex, err := c.acquireStateMutex(c.resourceMutexPath(topic))
	if err != nil {
		return Dispatch{}, err
	}
	defer releaseMutex()

	history, err := c.readHistory(topic)
	if err != nil {
		return Dispatch{}, err
	}
	if len(history) == 0 {
		return Dispatch{}, nil
	}
	hasTargets := false
	for _, entry := range history {
		for _, record := range entry.Records {
			if record.Index >= start && record.Index <= end {
				hasTargets = true
				break
			}
		}
		if hasTargets {
			break
		}
	}
	if !hasTargets {
		return Dispatch{}, nil
	}

	dir, err := c.topicDir(topic)
	if err != nil {
		return Dispatch{}, err
	}
	// Reserved before anything is destroyed, so that the moment the removal
	// becomes irreversible there is already a sequence to answer with. A failure
	// here has removed nothing and answers with nothing; sequences are not
	// contiguous, so spending one on an abandoned redaction costs nothing.
	sequence, err := c.nextIndex()
	if err != nil {
		return Dispatch{}, err
	}
	var result Dispatch
	originalPath := filepath.Join(dir, "history.jsonl")
	now := time.Now()
	backupPath := filepath.Join(dir, fmt.Sprintf("history.jsonl.bak-%s-%09d", now.Format("20060102150405"), now.Nanosecond()))
	data, err := os.ReadFile(originalPath)
	if err != nil {
		return Dispatch{}, fmt.Errorf("failed to read history for backup: %w", err)
	}
	if err := os.WriteFile(backupPath, data, 0o600); err != nil {
		return Dispatch{}, fmt.Errorf("failed to write history backup: %w", err)
	}

	tempPath := filepath.Join(dir, "history.jsonl.tmp")
	tempFile, err := os.OpenFile(tempPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return Dispatch{}, fmt.Errorf("failed to create temporary history file: %w", err)
	}
	defer func() {
		_ = tempFile.Close()
		_ = os.Remove(tempPath)
	}()
	for _, entry := range history {
		var filteredRecords []Record
		for _, record := range entry.Records {
			if record.Index < start || record.Index > end {
				filteredRecords = append(filteredRecords, record)
			}
		}
		entry.Records = filteredRecords
		bytes, err := json.Marshal(entry)
		if err != nil {
			return Dispatch{}, fmt.Errorf("failed to serialize history entry: %w", err)
		}
		bytes = append(bytes, '\n')
		if _, err := tempFile.Write(bytes); err != nil {
			return Dispatch{}, fmt.Errorf("failed to write filtered history: %w", err)
		}
	}
	if err := tempFile.Sync(); err != nil {
		return Dispatch{}, fmt.Errorf("failed to sync filtered history: %w", err)
	}
	if err := tempFile.Close(); err != nil {
		return Dispatch{}, fmt.Errorf("failed to close filtered history: %w", err)
	}
	if err := platform.ReplaceFile(tempPath, originalPath); err != nil {
		return Dispatch{}, fmt.Errorf("failed to atomically update history: %w", err)
	}
	// The records are gone. From here every answer carries the sequence, because
	// the caller's only signal that no sequence means no removal has to stay
	// true -- a failure after this point is a removal that happened and an
	// announcement that did not, never a redaction that never ran.
	result = Dispatch{
		Publication: Publication{Sequence: sequence, Topic: topic, Agent: agent, Timestamp: now},
		Recipients:  []string{},
	}

	var recipients []string
	if forcedBroadcastTopic(topic) {
		recipients, err = c.recipientNames()
	} else {
		recipients, err = c.subscribedRecipientNames(topic)
	}
	if err != nil {
		return result, err
	}
	summary := Summary{Type: "update", Topic: topic, Sequence: sequence, Agent: agent}
	woken, err := c.writeEvent(Event{Summary: summary, Recipients: recipients})
	if err != nil {
		return result, err
	}
	result.Recipients = woken
	return result, nil
}
