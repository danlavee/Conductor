package state

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// Batch is what one watch delivers: the unread delta of every topic that has
// one, taken in order until DefaultReadLimit records are spent. A publication
// is never split, so the last delivery may overrun the cap, and Remaining
// counts the records left for the next call.
type Batch struct {
	Deliveries   []ReadResult `json:"deliveries"`
	Remaining    int          `json:"remaining,omitempty"`
	DefaultLimit int          `json:"default_read_limit,omitempty"`
}

// Watch blocks until a topic the agent reads has a publication by another
// agent past its cursor, then returns every such unread delta.
func (c *Client) Watch() (Batch, error) {
	return c.WatchContext(context.Background())
}

// WatchContext is Watch, ending without acknowledging anything when ctx does.
//
// History is append-only, so a topic whose file has not changed size since
// the last look cannot have gained an unread publication; only the ones that
// did are read again.
func (c *Client) WatchContext(ctx context.Context) (Batch, error) {
	if err := c.validateProtocol(); err != nil {
		return Batch{}, err
	}
	agent, err := c.requireAgent()
	if err != nil {
		return Batch{}, err
	}
	sizes := map[string]int64{}
	for {
		topics, err := c.readableTopics(agent)
		if err != nil {
			return Batch{}, err
		}
		var changed []string
		for _, topic := range topics {
			size, err := c.historySize(topic)
			if err != nil {
				return Batch{}, err
			}
			if size != sizes[topic] {
				sizes[topic] = size
				changed = append(changed, topic)
			}
		}
		if len(changed) > 0 {
			batch, err := c.unreadBatch(changed)
			if err != nil || len(batch.Deliveries) > 0 {
				return batch, err
			}
		}
		if _, err := os.Stat(filepath.Join(c.Home, "registry", agent+".json")); errors.Is(err, os.ErrNotExist) {
			return Batch{}, &ProtocolError{Code: "NOT_FOUND", Text: "registered agent does not exist"}
		} else if err != nil {
			return Batch{}, err
		}
		if c.watchIdleFn != nil {
			c.watchIdleFn()
		}
		timer := time.NewTimer(c.PollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return Batch{}, ctx.Err()
		case <-timer.C:
		}
	}
}

// unreadBatch reads the delta of each topic, in order, into one batch.
func (c *Client) unreadBatch(topics []string) (Batch, error) {
	batch := Batch{Deliveries: []ReadResult{}}
	budget := DefaultReadLimit
	for _, topic := range topics {
		result, err := c.Get(ReadRequest{Topic: topic, Mode: ReadDelta, Limit: max(budget, 1)})
		if err != nil {
			return Batch{}, err
		}
		count := 0
		for _, publication := range result.Publications {
			count += len(publication.Records)
		}
		switch {
		case count == 0:
		case budget <= 0:
			batch.Remaining += count + result.Remaining
		default:
			batch.Deliveries = append(batch.Deliveries, result)
			batch.Remaining += result.Remaining
			budget -= count
		}
	}
	if batch.Remaining > 0 {
		batch.DefaultLimit = DefaultReadLimit
	}
	return batch, nil
}

func (c *Client) historySize(topic string) (int64, error) {
	dir, err := c.topicDir(topic)
	if err != nil {
		return 0, err
	}
	info, err := os.Stat(filepath.Join(dir, "history.jsonl"))
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}
