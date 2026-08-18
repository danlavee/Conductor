package state

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/danlavee/Conductor/internal/cutover"
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
	result, err := c.WatchResultSinceContext(ctx, since)
	if err != nil {
		return nil, err
	}
	defer result.Close()
	if result.Activation != nil {
		return nil, errors.New("watch crossed a conductor replacement; consume WatchResultSinceContext")
	}
	return result.Summaries, nil
}

// WatchResultContext is the cutover-aware watch boundary used by official
// adapters. A normal result retains its operation lease until Close so freeze
// cannot interleave between discovery, resolution, output, and ack.
func (c *Client) WatchResultContext(ctx context.Context) (WatchResult, error) {
	return c.WatchResultSinceContext(ctx, 0)
}

func (c *Client) WatchResultSinceContext(ctx context.Context, since int64) (WatchResult, error) {
	if since < 0 {
		return WatchResult{}, errors.New("watch sequence must not be negative")
	}
	initial, _, err := cutover.Observe(c.Home)
	if err != nil {
		return WatchResult{}, err
	}
	startedActive := initial.Phase == cutover.Active || (c.watchParticipant && c.watchGeneration == initial.Generation)
	initialGeneration := initial.Generation
	enteredCutover := false
	initialized := false
	var agent string
	var cursor Cursor
	journalToken := ""
	for {
		controlState, exists, err := cutover.Observe(c.Home)
		if err != nil {
			return WatchResult{}, err
		}
		if !exists && initialGeneration > 0 {
			return WatchResult{}, errors.New("cutover state disappeared after it was observed")
		}
		if controlState.Phase != cutover.Active || controlState.Generation != initialGeneration {
			if startedActive {
				enteredCutover = true
			}
			result, resume, err := c.waitForCutover(ctx, initialGeneration, enteredCutover)
			if err != nil || !resume {
				return result, err
			}
			startedActive = true
			activeState, _, err := cutover.Observe(c.Home)
			if err != nil {
				return WatchResult{}, err
			}
			initialGeneration = activeState.Generation
			initialized = false
			journalToken = ""
			continue
		}
		releaseOperation, err := c.beginOperation()
		if err != nil {
			var blocked *cutover.BlockedError
			if !errors.As(err, &blocked) {
				return WatchResult{}, err
			}
			if startedActive && blocked.State.Phase != cutover.Active {
				enteredCutover = true
			}
			result, resume, err := c.waitForCutover(ctx, initialGeneration, enteredCutover)
			if err != nil || !resume {
				return result, err
			}
			startedActive = true
			activeState, _, err := cutover.Observe(c.Home)
			if err != nil {
				return WatchResult{}, err
			}
			initialGeneration = activeState.Generation
			initialized = false
			journalToken = ""
			continue
		}
		if !initialized {
			if err := c.validateProtocol(); err != nil {
				_ = releaseOperation()
				return WatchResult{}, err
			}
			agent, err = c.requireAgent()
			if err != nil {
				_ = releaseOperation()
				return WatchResult{}, err
			}
			if since > 0 {
				if err := c.updateCursor(agent, func(cursor *Cursor) {
					if since > cursor.Summary {
						cursor.Summary = since
					}
					cursor.SummaryRanges = acknowledgeThrough(cursor.SummaryRanges, since)
				}); err != nil {
					_ = releaseOperation()
					return WatchResult{}, err
				}
			}
			cursor, err = c.loadCursor(agent)
			if err != nil {
				_ = releaseOperation()
				return WatchResult{}, err
			}
			initialized = true
		}
		pending, scannedTo, err := c.pendingInboxSummaries(agent, cursor, since)
		if err != nil {
			_ = releaseOperation()
			return WatchResult{}, err
		}
		if scannedTo > cursor.InboxOffset {
			if err := c.updateCursor(agent, func(current *Cursor) {
				if scannedTo > current.InboxOffset {
					current.InboxOffset = scannedTo
				}
			}); err != nil {
				_ = releaseOperation()
				return WatchResult{}, err
			}
			cursor.InboxOffset = scannedTo
		}
		currentToken, err := c.eventChangeToken()
		if err != nil {
			_ = releaseOperation()
			return WatchResult{}, err
		}
		if currentToken != journalToken {
			events, err := c.unreadEventsAfter(cursor.SummaryRanges, since)
			if err != nil {
				_ = releaseOperation()
				return WatchResult{}, err
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
			return WatchResult{Summaries: pending, release: releaseOperation}, nil
		}
		if _, err := os.Stat(filepath.Join(c.Home, "registry", agent+".json")); errors.Is(err, os.ErrNotExist) {
			_ = releaseOperation()
			return WatchResult{}, &ProtocolError{Code: "NOT_FOUND", Text: "registered agent does not exist"}
		} else if err != nil {
			_ = releaseOperation()
			return WatchResult{}, err
		}
		cursor, err = c.loadCursor(agent)
		if err != nil {
			_ = releaseOperation()
			return WatchResult{}, err
		}
		if err := releaseOperation(); err != nil {
			return WatchResult{}, err
		}
		if c.watchIdleFn != nil {
			c.watchIdleFn()
		}
		timer := time.NewTimer(c.PollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return WatchResult{}, ctx.Err()
		case <-timer.C:
		}
	}
}

// waitForCutover performs control-only polling. Once called, it never touches
// the protocol root. Missing or corrupt state fails closed; context expiry
// never changes cutover state.
func (c *Client) waitForCutover(ctx context.Context, generation int64, emit bool) (WatchResult, bool, error) {
	for {
		state, exists, err := cutover.Observe(c.Home)
		if err != nil {
			return WatchResult{}, false, err
		}
		if !exists {
			return WatchResult{}, false, errors.New("cutover state disappeared after freeze was observed")
		}
		switch state.Phase {
		case cutover.Replaced:
			if emit {
				return WatchResult{Activation: &cutover.Activation{
					Type: "conductor-replaced", CutoverID: state.CutoverID,
					Release: state.Release, Generation: state.Generation + 1,
				}}, false, nil
			}
		case cutover.Active:
			if emit && state.Generation > generation && state.LastCutoverID != "" {
				return WatchResult{Activation: &cutover.Activation{
					Type: "conductor-replaced", CutoverID: state.LastCutoverID,
					Release: state.LastRelease, Generation: state.Generation,
				}}, false, nil
			}
			return WatchResult{}, true, nil
		case cutover.Freezing, cutover.Frozen:
		default:
			return WatchResult{}, false, fmt.Errorf("invalid cutover phase %q", state.Phase)
		}
		timer := time.NewTimer(c.PollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return WatchResult{}, false, ctx.Err()
		case <-timer.C:
		}
	}
}

// AcknowledgeSummary advances the wake cursor after its consumer accepts the
// summary. A crash before this checkpoint causes replay, never silent loss.
func (c *Client) AcknowledgeSummary(summary Summary) error {
	releaseOperation, err := c.beginOperation()
	if err != nil {
		return err
	}
	defer releaseOperation()
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
