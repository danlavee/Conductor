package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/danlavee/Conductor/internal/platform"
)

// collaborationRulesTopic and collaborationAgentsTopic are the two topics the
// collaboration group always broadcasts to every registered agent, with no
// subscription and no opt-out (see forcedBroadcastTopic).
const (
	collaborationRulesTopic  = "collaboration/rules"
	collaborationAgentsTopic = "collaboration/agents"
)

// forcedBroadcastTopic reports whether a topic bypasses subscription-based
// recipient selection and reaches every registered agent unconditionally.
// Only these two exact topics are special; the rest of the collaboration
// group (and everything else) stays subscription-gated as normal.
func forcedBroadcastTopic(topic string) bool {
	return topic == collaborationRulesTopic || topic == collaborationAgentsTopic
}

// Begin acquires one topic lease and creates a durable empty transaction.
func (c *Client) Begin(topic string) (err error) {
	if err := c.validateProtocol(); err != nil {
		return err
	}
	agent, err := c.ResolveAgent()
	if err != nil {
		return err
	}
	if err := c.acquireMembership(agent); err != nil {
		return err
	}
	defer func() {
		if releaseErr := c.releaseWrite("_registry/membership", agent); err == nil && releaseErr != nil {
			err = releaseErr
		}
	}()
	return c.beginLocked(agent, topic)
}

// beginLocked performs Begin's work for a caller that already holds the
// agent's membership lock (Register and Deregister, while durably publishing
// the collaboration/agents roster record for that same agent). Calling Begin
// itself from there would deadlock re-acquiring a lock the caller already
// holds.
func (c *Client) beginLocked(agent, topic string) error {
	if _, err := c.requireAgent(); err != nil {
		return err
	}
	if err := validTopic(topic); err != nil {
		return err
	}
	releaseGuard, err := c.acquireTransactionGuard(agent)
	if err != nil {
		return err
	}
	defer releaseGuard()
	txnPath := c.transactionPath(agent)
	if _, err := os.Stat(txnPath); err == nil {
		return &ProtocolError{Code: "LOCKED", Agent: agent, Text: "agent already has an active transaction"}
	}
	if _, err := c.acquireWriteWhileHoldingTransactionGuard(topic, agent); err != nil {
		return err
	}
	txn := Transaction{
		Topic: topic, Agent: agent, PID: c.ownerProcessID(), Started: time.Now().UTC(),
		Records: map[int64]Record{}, Created: map[int64]bool{},
	}
	if err := writeJSONAtomic(txnPath, txn); err != nil {
		_ = c.releaseWrite(topic, agent)
		return err
	}
	if err := c.renewWrite(topic, agent); err != nil {
		_ = removeEventually(txnPath)
		_ = c.releaseWrite(topic, agent)
		return err
	}
	return nil
}

func (c *Client) StagePut(text string) (Record, error) {
	return c.stageRecord(func(txn *Transaction) (Record, error) {
		index, err := c.nextRecordIndex(txn.Topic)
		if err != nil {
			return Record{}, err
		}
		record := Record{Index: index, Text: text}
		txn.Created[index] = true
		return record, nil
	})
}

func (c *Client) StageEdit(index int64, text string) (Record, error) {
	return c.stageRecord(func(txn *Transaction) (Record, error) {
		if _, err := c.recordForTransaction(*txn, index); err != nil {
			return Record{}, err
		}
		return Record{Index: index, Text: text}, nil
	})
}

func (c *Client) StageStrike(index int64) (Record, error) {
	return c.stageRecord(func(txn *Transaction) (Record, error) {
		record, err := c.recordForTransaction(*txn, index)
		if err != nil {
			return Record{}, err
		}
		record.Text = "~~" + record.Text + "~~"
		return record, nil
	})
}

func (c *Client) stageRecord(operation func(*Transaction) (Record, error)) (Record, error) {
	if err := c.validateProtocol(); err != nil {
		return Record{}, err
	}
	agent, err := c.requireAgent()
	if err != nil {
		return Record{}, err
	}
	releaseGuard, err := c.acquireTransactionGuard(agent)
	if err != nil {
		return Record{}, err
	}
	defer releaseGuard()
	var txn Transaction
	if err := readJSON(c.transactionPath(agent), &txn); errors.Is(err, os.ErrNotExist) {
		return Record{}, &ProtocolError{Code: "NO_BUFFER", Text: "record operation requires begin"}
	} else if err != nil {
		return Record{}, err
	}
	if txn.Sequence != 0 {
		return Record{}, &ProtocolError{Code: "LOCKED", Agent: agent, Text: "transaction commit has started; retry commit"}
	}
	record, err := operation(&txn)
	if err != nil {
		return Record{}, err
	}
	if err := c.renewWrite(txn.Topic, agent); err != nil {
		return Record{}, err
	}
	txn.Records[record.Index] = record
	if err := writeJSONAtomic(c.transactionPath(agent), txn); err != nil {
		return Record{}, err
	}
	return record, nil
}

func (c *Client) recordForTransaction(txn Transaction, index int64) (Record, error) {
	if index <= 0 {
		return Record{}, errors.New("record index must be positive")
	}
	if record, ok := txn.Records[index]; ok {
		return record, nil
	}
	history, err := c.readHistory(txn.Topic)
	if err != nil {
		return Record{}, err
	}
	record, ok := materializeMap(history)[index]
	if !ok {
		return Record{}, &ProtocolError{Code: "NOT_FOUND", Text: "record does not exist"}
	}
	return record, nil
}

func (c *Client) Put(topic, text string) (Record, error) {
	return c.oneShotRecord(topic, func() (Record, error) { return c.StagePut(text) })
}

func (c *Client) Edit(topic string, index int64, text string) (Record, error) {
	return c.oneShotRecord(topic, func() (Record, error) { return c.StageEdit(index, text) })
}

func (c *Client) Strike(topic string, index int64) (Record, error) {
	return c.oneShotRecord(topic, func() (Record, error) { return c.StageStrike(index) })
}

func (c *Client) oneShotRecord(topic string, operation func() (Record, error)) (Record, error) {
	if err := c.Begin(topic); err != nil {
		return Record{}, err
	}
	record, err := operation()
	if err != nil {
		if abortErr := c.Abort(); abortErr != nil {
			return Record{}, errors.Join(err, fmt.Errorf("abort failed: %w", abortErr))
		}
		return Record{}, err
	}
	entry, err := c.commit()
	if err != nil {
		if entry.Sequence == 0 {
			return Record{}, err
		}
		return record, err
	}
	return record, nil
}

// Commit publishes every final staged record atomically.
func (c *Client) Commit() (Publication, error) {
	return c.commit()
}

func (c *Client) commit() (Publication, error) {
	if err := c.validateProtocol(); err != nil {
		return Publication{}, err
	}
	agent, err := c.requireAgent()
	if err != nil {
		return Publication{}, err
	}
	releaseGuard, err := c.acquireTransactionGuard(agent)
	if err != nil {
		return Publication{}, err
	}
	defer releaseGuard()
	var txn Transaction
	if err := readJSON(c.transactionPath(agent), &txn); errors.Is(err, os.ErrNotExist) {
		return Publication{}, &ProtocolError{Code: "NO_LOCK", Text: "commit requires begin"}
	} else if err != nil {
		return Publication{}, err
	}
	if err := c.renewWrite(txn.Topic, agent); err != nil {
		return Publication{}, err
	}
	return c.commitTransaction(txn)
}

func (c *Client) commitTransaction(txn Transaction) (result Publication, err error) {
	defer func() {
		if err == nil {
			if removeErr := removeEventually(c.transactionPath(txn.Agent)); removeErr != nil {
				err = removeErr
				return
			}
			if releaseErr := c.releaseWrite(txn.Topic, txn.Agent); releaseErr != nil {
				err = releaseErr
			}
		}
	}()
	if len(txn.Records) == 0 {
		return Publication{}, errors.New("transaction has no records")
	}
	if txn.Sequence == 0 {
		if err := c.validateTransactionTargets(txn); err != nil {
			return Publication{}, err
		}
		txn.Sequence, err = c.nextIndex()
		if err != nil {
			return Publication{}, err
		}
		if err := writeJSONAtomic(c.transactionPath(txn.Agent), txn); err != nil {
			return Publication{}, err
		}
	}
	result = Publication{
		Sequence: txn.Sequence, Topic: txn.Topic, Agent: txn.Agent,
		Timestamp: txn.Started, Records: sortedRecords(txn.Records),
	}
	topicDir, err := c.topicDir(txn.Topic)
	if err != nil {
		return Publication{}, err
	}
	if err := appendJSONL(filepath.Join(topicDir, "history.jsonl"), result); err != nil {
		return Publication{}, err
	}
	var recipients []string
	if forcedBroadcastTopic(txn.Topic) {
		recipients, err = c.recipientNames()
	} else {
		recipients, err = c.subscribedRecipientNames(txn.Topic)
	}
	if err != nil {
		return result, err
	}
	summary := Summary{Type: "update", Topic: txn.Topic, Sequence: txn.Sequence, Agent: txn.Agent}
	if err := c.writeEvent(Event{Summary: summary, Recipients: recipients}); err != nil {
		return result, err
	}
	return result, nil
}

func (c *Client) validateTransactionTargets(txn Transaction) error {
	history, err := c.readHistory(txn.Topic)
	if err != nil {
		return err
	}
	current := materializeMap(history)
	for index := range txn.Records {
		_, exists := current[index]
		if txn.Created[index] && exists {
			return errors.New("created record index already exists")
		}
		if !txn.Created[index] && !exists {
			return &ProtocolError{Code: "NOT_FOUND", Text: "edited record does not exist"}
		}
	}
	return nil
}

func (c *Client) nextRecordIndex(topic string) (int64, error) {
	dir, err := c.topicDir(topic)
	if err != nil {
		return 0, err
	}
	path := filepath.Join(dir, "record-index.json")
	state := struct {
		Index int64 `json:"index"`
	}{}
	stateErr := readJSON(path, &state)
	if stateErr != nil && !errors.Is(stateErr, os.ErrNotExist) {
		return 0, stateErr
	}
	if state.Index < 0 {
		return 0, errors.New("invalid record index state")
	}
	history, err := c.readHistory(topic)
	if err != nil {
		return 0, err
	}
	var highWater int64
	for _, entry := range history {
		for _, record := range entry.Records {
			if record.Index > highWater {
				highWater = record.Index
			}
		}
	}
	if errors.Is(stateErr, os.ErrNotExist) && highWater > 0 {
		return 0, errors.New("record index state is missing for a nonempty topic")
	}
	if state.Index < highWater {
		return 0, errors.New("record index state is behind authoritative history")
	}
	if state.Index == math.MaxInt64 {
		return 0, errors.New("record index is exhausted")
	}
	state.Index++
	if err := writeJSONAtomic(path, state); err != nil {
		return 0, err
	}
	return state.Index, nil
}

func (c *Client) transactionPath(agent string) string {
	return filepath.Join(c.Home, "transactions", agent+".json")
}

func (c *Client) Abort() error {
	if err := c.validateProtocol(); err != nil {
		return err
	}
	agent, err := c.requireAgent()
	if err != nil {
		return err
	}
	releaseGuard, err := c.acquireTransactionGuard(agent)
	if err != nil {
		return err
	}
	defer releaseGuard()
	var txn Transaction
	if err := readJSON(c.transactionPath(agent), &txn); errors.Is(err, os.ErrNotExist) {
		return &ProtocolError{Code: "NO_LOCK", Text: "abort requires begin"}
	} else if err != nil {
		return err
	}
	if txn.Sequence != 0 {
		return &ProtocolError{Code: "LOCKED", Agent: agent, Text: "transaction commit has started and cannot be aborted"}
	}
	if err := removeEventually(c.transactionPath(agent)); err != nil {
		return err
	}
	return c.releaseWrite(txn.Topic, agent)
}

func recordSlot(topic string, index int64) string {
	if index == 0 {
		return topic
	}
	return topic + "#" + strconv.FormatInt(index, 10)
}

func appendJSONL(path string, value interface{}) error {
	bytes, err := json.Marshal(value)
	if err != nil {
		return err
	}
	bytes = append(bytes, '\n')

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()

	if _, err := f.Write(bytes); err != nil {
		return err
	}
	return f.Sync()
}

// Redact physically deletes records within the inclusive index range [start, end]
// from the topic's history.jsonl. It automatically creates a pre-redaction backup
// and notifies subscribers.
func (c *Client) Redact(topic string, start, end int64) error {
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

	// 1. Acquire exclusive resource lock.
	releaseMutex, err := c.acquireStateMutex(c.resourceMutexPath(topic))
	if err != nil {
		return err
	}
	defer releaseMutex()

	// 2. Read the entire history of the topic.
	history, err := c.readHistory(topic)
	if err != nil {
		return err
	}
	if len(history) == 0 {
		return nil // Nothing to redact if history is empty.
	}

	// 3. Determine if any records fall in the target range [start, end].
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
		return nil // No matching records to redact; abort safely with no changes.
	}

	// 4. Perform Automatic Backup.
	dir, err := c.topicDir(topic)
	if err != nil {
		return err
	}
	originalPath := filepath.Join(dir, "history.jsonl")
	now := time.Now()
	backupPath := filepath.Join(dir, fmt.Sprintf("history.jsonl.bak-%s-%09d", now.Format("20060102150405"), now.Nanosecond()))

	// Copy original file to backup path.
	data, err := os.ReadFile(originalPath)
	if err != nil {
		return fmt.Errorf("failed to read history for backup: %w", err)
	}
	if err := os.WriteFile(backupPath, data, 0o600); err != nil {
		return fmt.Errorf("failed to write history backup: %w", err)
	}

	// 5. Rewrite history.jsonl filtering out target records.
	tempPath := filepath.Join(dir, "history.jsonl.tmp")
	tempFile, err := os.OpenFile(tempPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("failed to create temporary history file: %w", err)
	}
	defer func() {
		_ = tempFile.Close()
		_ = os.Remove(tempPath) // Clean up temp file on failure or exit.
	}()

	for _, entry := range history {
		var filteredRecords []Record
		for _, record := range entry.Records {
			if record.Index < start || record.Index > end {
				filteredRecords = append(filteredRecords, record)
			}
		}
		// Write the entry with the filtered records. Even if empty, we write it to preserve contiguous Sequences.
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

	// Atomic replace original with temp.
	if err := platform.ReplaceFile(tempPath, originalPath); err != nil {
		return fmt.Errorf("failed to atomically update history: %w", err)
	}

	// 6. Broadcast event notification to subscribers.
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
	if err := c.writeEvent(Event{Summary: summary, Recipients: recipients}); err != nil {
		return err
	}

	return nil
}

