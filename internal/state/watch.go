package state

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
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
	agent, offset, err := c.prepareSummaryAcknowledgment(summary)
	if err != nil {
		return err
	}
	return c.updateCursor(agent, func(cursor *Cursor) {
		applySummaryAcknowledgment(cursor, summary, offset)
	})
}

func (c *Client) prepareSummaryAcknowledgment(summary Summary) (string, int64, error) {
	if err := c.validateProtocol(); err != nil {
		return "", 0, err
	}
	if err := validateSummary(&summary); err != nil {
		return "", 0, err
	}
	agent, err := c.requireAgent()
	if err != nil {
		return "", 0, err
	}
	offset, err := c.inboxOffsetThrough(agent, summary.Sequence)
	if err != nil {
		return "", 0, err
	}
	return agent, offset, nil
}

func applySummaryAcknowledgment(cursor *Cursor, summary Summary, offset int64) {
	if summary.Sequence > cursor.Summary {
		cursor.Summary = summary.Sequence
	}
	cursor.SummaryRanges = acknowledgeIndex(cursor.SummaryRanges, summary.Sequence)
	if offset > cursor.InboxOffset {
		cursor.InboxOffset = offset
	}
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
