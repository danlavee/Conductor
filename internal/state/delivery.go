package state

import "fmt"

// DeliveryMode selects how much data a watch caller receives with a summary.
type DeliveryMode string

const (
	DeliverySummary DeliveryMode = "summary"
	DeliveryContent DeliveryMode = "content"
)

// ParseDeliveryMode validates a --mode flag value. Empty defaults to content.
func ParseDeliveryMode(value string) (DeliveryMode, error) {
	switch DeliveryMode(value) {
	case "":
		return DeliveryContent, nil
	case DeliverySummary, DeliveryContent:
		return DeliveryMode(value), nil
	default:
		return "", fmt.Errorf("unknown watch mode %q; use summary or content", value)
	}
}

// Delivery is a summary with its resolved content in content mode.
type Delivery struct {
	Summary Summary      `json:"summary"`
	Mode    DeliveryMode `json:"mode"`
	Roster  []Agent      `json:"roster,omitempty"`
	Delta   *ReadResult  `json:"delta,omitempty"`
	// covered holds every original pending summary this delivery satisfies --
	// possibly several, when consecutive-or-not "update" summaries for the
	// same topic were grouped into one Get call. AcknowledgeBatch marks all
	// of them read, not just Summary.
	covered []Summary
}

func (c *Client) ResolveDelivery(summary Summary, mode DeliveryMode) (Delivery, error) {
	return c.resolveDeliveryWithLimit(summary, mode, 0)
}

// resolveDeliveryWithLimit is ResolveDelivery with an explicit record-count
// cap on the resolved delta. limit must be positive or zero; zero means
// "unset, use Get's own default cap" -- the same convention Get itself uses.
func (c *Client) resolveDeliveryWithLimit(summary Summary, mode DeliveryMode, limit int) (Delivery, error) {
	delivery := Delivery{Summary: summary, Mode: mode}
	if mode != DeliveryContent {
		return delivery, nil
	}
	switch summary.Type {
	case "join", "leave":
		roster, err := c.ListAgents()
		if err != nil {
			return Delivery{}, err
		}
		delivery.Roster = roster
	case "update":
		result, err := c.Get(ReadRequest{Topic: summary.Topic, Mode: ReadDelta, throughSequence: summary.Sequence, Limit: limit})
		if err != nil {
			return Delivery{}, err
		}
		delivery.Delta = &result
	}
	return delivery, nil
}

// AcknowledgeDelivery advances both the summary and content cursors after the
// downstream consumer accepts the delivery.
func (c *Client) AcknowledgeDelivery(delivery Delivery) error {
	if err := c.AcknowledgeSummary(delivery.Summary); err != nil {
		return err
	}
	if delivery.Delta != nil {
		return c.AcknowledgeRead(*delivery.Delta)
	}
	if delivery.Mode == DeliverySummary && delivery.Summary.Type == "update" {
		agent, err := c.requireAgent()
		if err != nil {
			return err
		}
		return c.updateCursor(agent, func(cursor *Cursor) {
			if delivery.Summary.Sequence > cursor.Topics[delivery.Summary.Topic] {
				cursor.Topics[delivery.Summary.Topic] = delivery.Summary.Sequence
			}
		})
	}
	return nil
}

// BatchDelivery is every delivery one watch call resolved, capped at
// DefaultReadLimit cumulative records across all included content
// deliveries -- the same mechanics as a capped get. Join, leave, and
// summary-mode deliveries never count against the budget; only resolved
// update content does.
type BatchDelivery struct {
	Deliveries   []Delivery `json:"deliveries"`
	Remaining    int        `json:"remaining,omitempty"`
	DefaultLimit int        `json:"default_read_limit,omitempty"`
}

// ResolveBatch resolves every pending summary watch returned, stopping once
// the cumulative record budget is spent -- except the first resolved
// delivery is always included whole, mirroring Get's atomic per-publication
// guarantee. Consecutive-or-not "update" summaries for the same topic are
// grouped and resolved with one Get call at their highest sequence, since
// nothing is acknowledged until the caller accepts the whole batch and
// resolving them independently would each re-fetch the same growing,
// still-unacknowledged window and wildly over-count the budget.
func (c *Client) ResolveBatch(summaries []Summary, mode DeliveryMode) (BatchDelivery, error) {
	groups := groupSummariesByTopic(summaries)
	budget := DefaultReadLimit
	batch := BatchDelivery{Deliveries: make([]Delivery, 0, len(groups))}
	for groupIndex, group := range groups {
		if len(batch.Deliveries) > 0 && budget <= 0 {
			for _, leftover := range groups[groupIndex:] {
				batch.Remaining += len(leftover)
			}
			break
		}
		target := highestSequenceSummary(group)
		delivery, err := c.resolveDeliveryWithLimit(target, mode, budget)
		if err != nil {
			return BatchDelivery{}, err
		}
		delivery, uncovered := finalizeGroupDelivery(delivery, group)
		batch.Deliveries = append(batch.Deliveries, delivery)
		batch.Remaining += len(uncovered)
		budget -= deliveryRecordCount(delivery)
	}
	if batch.Remaining > 0 {
		batch.DefaultLimit = DefaultReadLimit
	}
	return batch, nil
}

// finalizeGroupDelivery reconciles a resolved delivery against the group of
// summaries it was asked to cover, since Get's own cap may have stopped
// short of the group's highest sequence. It points the delivery at whichever
// covered member Get actually delivered through -- never the group's overall
// highest -- so AcknowledgeDelivery can never mark a sequence read whose
// content wasn't actually included, and records the covered set on the
// delivery for AcknowledgeBatch. It returns the summaries left uncovered.
func finalizeGroupDelivery(delivery Delivery, group []Summary) (Delivery, []Summary) {
	covered, uncovered := splitCoveredSummaries(group, delivery)
	if delivery.Delta != nil && len(covered) > 0 {
		delivery.Summary = covered[len(covered)-1]
	}
	delivery.covered = covered
	return delivery, uncovered
}

// AcknowledgeBatch acknowledges every summary ResolveBatch actually covered,
// including every member of a grouped delivery, not just the one summary it
// carries. Summaries left out by Remaining were never touched and stay
// pending for the next watch call.
func (c *Client) AcknowledgeBatch(batch BatchDelivery) error {
	for _, delivery := range batch.Deliveries {
		if err := c.AcknowledgeDelivery(delivery); err != nil {
			return err
		}
		for _, summary := range delivery.covered {
			if summary.Sequence == delivery.Summary.Sequence {
				continue
			}
			if err := c.AcknowledgeSummary(summary); err != nil {
				return err
			}
		}
	}
	return nil
}

// groupSummariesByTopic buckets pending "update" summaries for the same
// topic together, wherever they fall in summaries, since Get's delta for
// the highest-sequence one already spans every earlier one in the same
// unacknowledged window.
func groupSummariesByTopic(summaries []Summary) [][]Summary {
	var groups [][]Summary
	positions := make(map[string]int, len(summaries))
	for _, summary := range summaries {
		if summary.Type != "update" {
			groups = append(groups, []Summary{summary})
			continue
		}
		if position, ok := positions[summary.Topic]; ok {
			groups[position] = append(groups[position], summary)
			continue
		}
		positions[summary.Topic] = len(groups)
		groups = append(groups, []Summary{summary})
	}
	return groups
}

func highestSequenceSummary(group []Summary) Summary {
	highest := group[0]
	for _, candidate := range group[1:] {
		if candidate.Sequence > highest.Sequence {
			highest = candidate
		}
	}
	return highest
}

// splitCoveredSummaries partitions group by whether Get actually delivered
// their content. A join or leave has no sequence scope, so the whole group
// is always covered. An update is covered only up through the delta's last
// included publication -- Get's own cap may have stopped short of the
// group's highest sequence.
func splitCoveredSummaries(group []Summary, delivery Delivery) (covered, uncovered []Summary) {
	if delivery.Delta == nil {
		return group, nil
	}
	for _, summary := range group {
		if summary.Sequence <= delivery.Delta.maxSequence {
			covered = append(covered, summary)
		} else {
			uncovered = append(uncovered, summary)
		}
	}
	return covered, uncovered
}

func deliveryRecordCount(delivery Delivery) int {
	if delivery.Delta == nil {
		return 0
	}
	count := 0
	for _, publication := range delivery.Delta.Publications {
		count += len(publication.Records)
	}
	return count
}
