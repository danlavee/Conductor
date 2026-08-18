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
	// coverageKnown distinguishes an intentionally empty coverage set produced
	// by ResolveBatch from a delivery constructed outside batch resolution.
	coveredSummaries []Summary
	coverageKnown    bool
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
	agent, offset, err := c.prepareSummaryAcknowledgment(delivery.Summary)
	if err != nil {
		return err
	}

	cursorSlot, throughSequence := deliveryCursorAdvance(delivery)

	return c.updateCursor(agent, func(cursor *Cursor) {
		applySummaryAcknowledgment(cursor, delivery.Summary, offset)
		if throughSequence > cursor.Topics[cursorSlot] {
			cursor.Topics[cursorSlot] = throughSequence
		}
	})
}

func deliveryCursorAdvance(delivery Delivery) (cursorSlot string, throughSequence int64) {
	if delivery.Delta != nil {
		if delivery.Delta.Mode == "delta" && delivery.Delta.maxSequence > 0 {
			return recordCursorSlot(delivery.Delta.Topic, delivery.Delta.record), delivery.Delta.maxSequence
		}
	} else if delivery.Mode == DeliverySummary && delivery.Summary.Type == "update" {
		return delivery.Summary.Topic, delivery.Summary.Sequence
	}
	return "", 0
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
// still-unacknowledged window and wildly over-count the budget. "join" and
// "leave" summaries group the same way across every topic, since they all
// resolve to the same current-roster read.
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
	delivery.coveredSummaries = covered
	delivery.coverageKnown = true
	return delivery, uncovered
}

// AcknowledgeBatch acknowledges every summary ResolveBatch actually covered,
// including every member of a grouped delivery, not just the one summary it
// carries. Summaries left out by Remaining were never touched and stay
// pending for the next watch call.
func (c *Client) AcknowledgeBatch(batch BatchDelivery) error {
	type summaryAcknowledgment struct {
		summary Summary
		offset  int64
	}
	var agent string
	var acknowledgments []summaryAcknowledgment
	sequenceByCursorSlot := map[string]int64{}
	for _, delivery := range batch.Deliveries {
		summaries := delivery.coveredSummaries
		if !delivery.coverageKnown {
			summaries = []Summary{delivery.Summary}
		}
		for _, summary := range summaries {
			currentAgent, offset, err := c.prepareSummaryAcknowledgment(summary)
			if err != nil {
				return err
			}
			if agent == "" {
				agent = currentAgent
			} else if currentAgent != agent {
				return fmt.Errorf("batch summaries resolve to different agents")
			}
			acknowledgments = append(acknowledgments, summaryAcknowledgment{summary: summary, offset: offset})
		}
		cursorSlot, throughSequence := deliveryCursorAdvance(delivery)
		if throughSequence > sequenceByCursorSlot[cursorSlot] {
			sequenceByCursorSlot[cursorSlot] = throughSequence
		}
	}
	if agent == "" && len(sequenceByCursorSlot) > 0 {
		if err := c.validateProtocol(); err != nil {
			return err
		}
		var err error
		agent, err = c.requireAgent()
		if err != nil {
			return err
		}
	}
	if agent == "" {
		return nil
	}
	return c.updateCursor(agent, func(cursor *Cursor) {
		for _, acknowledgment := range acknowledgments {
			applySummaryAcknowledgment(cursor, acknowledgment.summary, acknowledgment.offset)
		}
		for cursorSlot, throughSequence := range sequenceByCursorSlot {
			if throughSequence > cursor.Topics[cursorSlot] {
				cursor.Topics[cursorSlot] = throughSequence
			}
		}
	})
}

// membershipGroupKey buckets every "join"/"leave" summary together regardless of topic.
const membershipGroupKey = "\x00membership"

// groupSummariesByTopic buckets "update" summaries per topic and all "join"/"leave" summaries into one shared bucket.
func groupSummariesByTopic(summaries []Summary) [][]Summary {
	var groups [][]Summary
	positions := make(map[string]int, len(summaries))
	for _, summary := range summaries {
		key := summary.Topic
		if summary.Type != "update" {
			key = membershipGroupKey
		}
		if position, ok := positions[key]; ok {
			groups[position] = append(groups[position], summary)
			continue
		}
		positions[key] = len(groups)
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
