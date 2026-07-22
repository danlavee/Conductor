package state

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Begin acquires a resource lease and creates a durable empty transaction.
func (c *Client) Begin(resource string) error {
	return c.BeginWithOptions(resource, WriteOptions{})
}

// BeginWithOptions creates a durable transaction only when every per-message
// IfIndex condition matches the latest message mutation under the write lease.
func (c *Client) BeginWithOptions(resource string, options WriteOptions) (err error) {
	if err := c.validateProtocol(); err != nil {
		return err
	}
	expected, err := normalizeExpectedIndexes(options.IfIndex)
	if err != nil {
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
	if _, err := c.requireAgent(); err != nil {
		return err
	}
	if err := validResource(resource); err != nil {
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
	if _, err := c.acquireWriteWhileHoldingTransactionGuard(resource, agent); err != nil {
		return err
	}
	if err := c.checkExpectedIndexes(resource, expected); err != nil {
		if releaseErr := c.releaseWrite(resource, agent); releaseErr != nil {
			var protocol *ProtocolError
			if errors.As(err, &protocol) {
				copy := *protocol
				copy.Text = fmt.Sprintf("%s; rejected write could not release its lease: %v", protocol.Text, releaseErr)
				return &copy
			}
			return errors.Join(err, fmt.Errorf("release rejected write: %w", releaseErr))
		}
		return err
	}
	txn := Transaction{Resource: resource, Agent: agent, PID: c.ownerProcessID(), Started: time.Now().UTC(), Messages: map[string]MessageMutation{}}
	if err := writeJSONAtomic(txnPath, txn); err != nil {
		_ = c.releaseWrite(resource, agent)
		return err
	}
	if err := c.renewWrite(resource, agent); err != nil {
		_ = removeEventually(txnPath)
		_ = c.releaseWrite(resource, agent)
		return err
	}
	return nil
}

// Set adds or replaces one message in the current durable transaction buffer.
func (c *Client) Set(key, kind, text string) error {
	payload := MessagePayload{Text: text}
	return c.setMutation(key, MessageMutation{Operation: MutationSet, Kind: kind, Payload: &payload})
}

// Scratch removes one message from current state in the current transaction.
func (c *Client) Scratch(key string) error {
	return c.setMutation(key, MessageMutation{Operation: MutationScratch})
}

func (c *Client) setMutation(key string, mutation MessageMutation) error {
	if err := c.validateProtocol(); err != nil {
		return err
	}
	agent, err := c.requireAgent()
	if err != nil {
		return err
	}
	if err := validName(key); err != nil {
		return err
	}
	releaseGuard, err := c.acquireTransactionGuard(agent)
	if err != nil {
		return err
	}
	defer releaseGuard()
	var txn Transaction
	if err := readJSON(c.transactionPath(agent), &txn); errors.Is(err, os.ErrNotExist) {
		return &ProtocolError{Code: "NO_BUFFER", Text: "set requires begin"}
	} else if err != nil {
		return err
	}
	if txn.Index != 0 {
		return &ProtocolError{Code: "LOCKED", Agent: agent, Text: "transaction commit has started; retry commit"}
	}
	if err := validateMutations(map[string]MessageMutation{key: mutation}); err != nil {
		return err
	}
	if err := c.renewWrite(txn.Resource, agent); err != nil {
		return err
	}
	txn.Messages[key] = cloneMutation(mutation)
	return writeJSONAtomic(c.transactionPath(agent), txn)
}

// Commit atomically publishes the current buffer, signals recipients, and releases the lease.
func (c *Client) Commit() (Publication, error) {
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
	if err := c.renewWrite(txn.Resource, agent); err != nil {
		return Publication{}, err
	}
	return c.commitTransaction(txn)
}

// Put publishes message mutations as one operation.
func (c *Client) Put(resource string, messages map[string]MessageMutation) (Publication, error) {
	return c.PutWithOptions(resource, messages, WriteOptions{})
}

// PutWithOptions publishes mutations only when every per-message IfIndex condition
// matches authoritative published state under the write lease.
func (c *Client) PutWithOptions(resource string, messages map[string]MessageMutation, options WriteOptions) (Publication, error) {
	if err := c.validateProtocol(); err != nil {
		return Publication{}, err
	}
	if len(messages) == 0 {
		return Publication{}, errors.New("put requires at least one message")
	}
	if err := validateMutations(messages); err != nil {
		return Publication{}, err
	}
	if err := c.BeginWithOptions(resource, options); err != nil {
		return Publication{}, err
	}
	for key, mutation := range messages {
		if err := c.setMutation(key, mutation); err != nil {
			if abortErr := c.Abort(); abortErr != nil {
				return Publication{}, errors.Join(err, fmt.Errorf("abort failed: %w", abortErr))
			}
			return Publication{}, err
		}
	}
	return c.Commit()
}

func normalizeExpectedIndexes(values map[string]int64) (map[string]int64, error) {
	if len(values) == 0 {
		return nil, nil
	}
	result := make(map[string]int64, len(values))
	for key, index := range values {
		if err := validName(key); err != nil {
			return nil, err
		}
		if index < 0 {
			return nil, fmt.Errorf("expected index for %s must not be negative", key)
		}
		result[key] = index
	}
	return result, nil
}

func (c *Client) checkExpectedIndexes(resource string, expected map[string]int64) error {
	if len(expected) == 0 {
		return nil
	}
	history, err := c.readHistory(resource)
	if err != nil {
		return err
	}
	current := materializeLatest(history, "", 0)
	keys := make([]string, 0, len(expected))
	for key := range expected {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		message, exists := current[key]
		if err := c.verifyMaterializedMessage(resource, key, message, exists); err != nil {
			return err
		}
		currentIndex := message.Index
		if currentIndex != expected[key] {
			return &ProtocolError{
				Code: "CONFLICT",
				Text: "message changed since it was read",
				Conflict: &ConflictDetail{
					Resource:      resource,
					Key:           key,
					ExpectedIndex: expected[key],
					CurrentIndex:  currentIndex,
				},
			}
		}
	}
	return nil
}

func (c *Client) verifyMaterializedMessage(resource, key string, authoritative materializedMessage, exists bool) error {
	resourceDir, err := c.resourceDir(resource)
	if err != nil {
		return err
	}
	var materialized materializedMessage
	err = readJSON(filepath.Join(resourceDir, "messages", key+".json"), &materialized)
	if errors.Is(err, os.ErrNotExist) && !exists {
		return nil
	}
	if err != nil {
		return fmt.Errorf("cannot verify current message %s/%s: %w", resource, key, err)
	}
	if !exists || materialized != authoritative {
		return fmt.Errorf("current message %s/%s does not match authoritative history", resource, key)
	}
	return nil
}

func (c *Client) commitTransaction(txn Transaction) (result Publication, err error) {
	defer func() {
		if err == nil {
			if removeErr := removeEventually(c.transactionPath(txn.Agent)); removeErr != nil {
				err = removeErr
				return
			}
			if releaseErr := c.releaseWrite(txn.Resource, txn.Agent); releaseErr != nil {
				err = releaseErr
			}
		}
	}()
	if len(txn.Messages) == 0 {
		return Publication{}, errors.New("transaction has no messages")
	}
	if txn.Index == 0 {
		txn.Index, err = c.nextIndex()
		if err != nil {
			return Publication{}, err
		}
		if err := writeJSONAtomic(c.transactionPath(txn.Agent), txn); err != nil {
			return Publication{}, err
		}
	}
	result = Publication{Index: txn.Index, Resource: txn.Resource, Agent: txn.Agent, Timestamp: txn.Started, Messages: txn.Messages}
	resourceDir, err := c.resourceDir(txn.Resource)
	if err != nil {
		return Publication{}, err
	}
	if err := os.MkdirAll(filepath.Join(resourceDir, "history"), 0o700); err != nil {
		return Publication{}, err
	}
	if err := writeJSONAtomic(filepath.Join(resourceDir, "history", indexName(txn.Index)), result); err != nil {
		return Publication{}, err
	}
	if err := writeJSONAtomic(filepath.Join(resourceDir, "head.json"), map[string]int64{"index": txn.Index}); err != nil {
		return Publication{}, err
	}
	if err := os.MkdirAll(filepath.Join(resourceDir, "messages"), 0o700); err != nil {
		return Publication{}, err
	}
	keys := sortedKeys(txn.Messages)
	for _, key := range keys {
		mutation := txn.Messages[key]
		message := materializedMessage{Message: Message{Key: key, Agent: txn.Agent, Index: txn.Index, Timestamp: result.Timestamp}}
		if mutation.Operation == MutationScratch {
			message.Scratched = true
		} else {
			message.Kind, message.Payload = mutation.Kind, *mutation.Payload
		}
		if err := writeJSONAtomic(filepath.Join(resourceDir, "messages", key+".json"), message); err != nil {
			return Publication{}, err
		}
	}
	signalKey := keys[0]
	if len(keys) > 1 {
		signalKey = "*"
	}
	recipients, err := c.recipientNames()
	if err != nil {
		return Publication{}, err
	}
	if err := c.writeEvent(Event{Signal: Signal{Type: "update", Resource: txn.Resource, Key: signalKey, Index: txn.Index, Agent: txn.Agent}, Recipients: recipients}); err != nil {
		return Publication{}, err
	}
	return result, nil
}

func (c *Client) transactionPath(agent string) string {
	return filepath.Join(c.Home, "transactions", agent+".json")
}

// Abort discards the current agent's buffer and releases its resource lock.
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
	if txn.Index != 0 {
		return &ProtocolError{Code: "LOCKED", Agent: agent, Text: "transaction commit has started and cannot be aborted"}
	}
	if err := removeEventually(c.transactionPath(agent)); err != nil {
		return err
	}
	return c.releaseWrite(txn.Resource, agent)
}
