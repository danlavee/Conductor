package state

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/danlavee/Conductor/internal/platform"
)

// Batch is what one watch delivers: the unread delta of every topic that has
// one, taken in order while DefaultReadLimit records last. A publication is
// never split: one that does not fit waits for the next call, and Publish
// admits none larger than a wake, so the first always fits. Remaining counts
// the records left.
type Batch struct {
	Deliveries   []ReadResult `json:"deliveries"`
	Remaining    int          `json:"remaining,omitempty"`
	DefaultLimit int          `json:"default_read_limit,omitempty"`
}

// WatchContext blocks until a topic the agent reads has a publication by
// another agent past its cursor, then returns every such unread delta. It ends
// without acknowledging anything when ctx does.
//
// History is append-only, so a topic whose file has not changed size since
// the last look cannot have gained an unread publication; only the ones that
// did are read again.
func (c *Client) WatchContext(ctx context.Context) (Batch, error) {
	agent, err := c.requireAgent()
	if err != nil {
		return Batch{}, err
	}
	var seen map[string]int64
	for {
		heads, err := c.heads(agent)
		if err != nil {
			return Batch{}, err
		}
		changed := changedSince(heads, seen)
		seen = heads
		if len(changed) > 0 {
			batch, err := c.unreadBatch(changed)
			if err != nil || len(batch.Deliveries) > 0 {
				return batch, err
			}
		}
		if _, err := os.Stat(filepath.Join(c.Home, "registry", agent+".json")); errors.Is(err, os.ErrNotExist) {
			return Batch{}, &ProtocolError{Code: "NOT_FOUND", Text: "registered agent does not exist"}
		} else if err != nil && !platform.IsDeletePending(err) {
			// A leave caught mid-removal is settled by the next poll.
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

// Unread is the batch WatchContext would return now, without waiting: what a
// deliverer reads under its session's lock, so it never shows what another
// deliverer acknowledged since the wait ended.
func (c *Client) Unread() (Batch, error) {
	agent, err := c.requireAgent()
	if err != nil {
		return Batch{}, err
	}
	heads, err := c.heads(agent)
	if err != nil {
		return Batch{}, err
	}
	return c.unreadBatch(changedSince(heads, nil))
}

// Heads is the history size of every topic the agent reads. History is
// append-only and a cursor only advances, so heads equal to ones taken before
// a read that found nothing unread mean nothing is unread now.
func (c *Client) Heads() (map[string]int64, error) {
	agent, err := c.requireAgent()
	if err != nil {
		return nil, err
	}
	return c.heads(agent)
}

func (c *Client) heads(agent string) (map[string]int64, error) {
	topics, err := c.readableTopics(agent)
	if err != nil {
		return nil, err
	}
	heads := make(map[string]int64, len(topics))
	for _, topic := range topics {
		if heads[topic], err = c.historySize(topic); err != nil {
			return nil, err
		}
	}
	return heads, nil
}

// changedSince lists, in topic order, the topics whose head differs from
// seen's; a topic seen lacks counts as empty.
func changedSince(heads, seen map[string]int64) []string {
	var changed []string
	for topic, head := range heads {
		if head != seen[topic] {
			changed = append(changed, topic)
		}
	}
	slices.Sort(changed)
	return changed
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
		// A topic whose next publication no longer fits waits for the next
		// call, and so does every topic after it.
		case count > budget:
			batch.Remaining += count + result.Remaining
			budget = 0
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
