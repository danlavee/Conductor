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
func (c *Client) Redact(topic string, start, end int64) error {
	releaseOperation, err := c.beginOperation()
	if err != nil {
		return err
	}
	defer releaseOperation()
	if err := c.validateProtocol(); err != nil {
		return err
	}
	agent, err := c.requireAgent()
	if err != nil {
		return err
	}
	if err := validTopic(topic); err != nil {
		return err
	}
	if start <= 0 || end <= 0 || end < start {
		return errors.New("invalid redaction range")
	}

	releaseMutex, err := c.acquireStateMutex(c.resourceMutexPath(topic))
	if err != nil {
		return err
	}
	defer releaseMutex()

	history, err := c.readHistory(topic)
	if err != nil {
		return err
	}
	if len(history) == 0 {
		return nil
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
		return nil
	}

	dir, err := c.topicDir(topic)
	if err != nil {
		return err
	}
	originalPath := filepath.Join(dir, "history.jsonl")
	now := time.Now()
	backupPath := filepath.Join(dir, fmt.Sprintf("history.jsonl.bak-%s-%09d", now.Format("20060102150405"), now.Nanosecond()))
	data, err := os.ReadFile(originalPath)
	if err != nil {
		return fmt.Errorf("failed to read history for backup: %w", err)
	}
	if err := os.WriteFile(backupPath, data, 0o600); err != nil {
		return fmt.Errorf("failed to write history backup: %w", err)
	}

	tempPath := filepath.Join(dir, "history.jsonl.tmp")
	tempFile, err := os.OpenFile(tempPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("failed to create temporary history file: %w", err)
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
			return fmt.Errorf("failed to serialize history entry: %w", err)
		}
		bytes = append(bytes, '\n')
		if _, err := tempFile.Write(bytes); err != nil {
			return fmt.Errorf("failed to write filtered history: %w", err)
		}
	}
	if err := tempFile.Sync(); err != nil {
		return fmt.Errorf("failed to sync filtered history: %w", err)
	}
	if err := tempFile.Close(); err != nil {
		return fmt.Errorf("failed to close filtered history: %w", err)
	}
	if err := platform.ReplaceFile(tempPath, originalPath); err != nil {
		return fmt.Errorf("failed to atomically update history: %w", err)
	}

	var recipients []string
	if forcedBroadcastTopic(topic) {
		recipients, err = c.recipientNames()
	} else {
		recipients, err = c.subscribedRecipientNames(topic)
	}
	if err != nil {
		return err
	}
	sequence, err := c.nextIndex()
	if err != nil {
		return err
	}
	summary := Summary{Type: "update", Topic: topic, Sequence: sequence, Agent: agent}
	return c.writeEvent(Event{Summary: summary, Recipients: recipients})
}
