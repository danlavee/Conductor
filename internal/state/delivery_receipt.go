package state

import "errors"

// DeliveryReceipt is a batch a resident has shown and not yet accepted.
// The cursor positions travel with it because a later process cannot recover
// them from the batch JSON.
type DeliveryReceipt struct {
	Agent   string          `json:"agent"`
	Cursors []receiptCursor `json:"cursors"`
	Batch   Batch           `json:"batch"`
}

type receiptCursor struct {
	Topic    string `json:"topic"`
	Sequence int64  `json:"sequence"`
}

// IssueReceipt snapshots the batch and the cursor positions its deltas reached.
func IssueReceipt(agent string, batch Batch) (DeliveryReceipt, error) {
	if agent == "" {
		return DeliveryReceipt{}, errors.New("delivery receipt has no agent")
	}
	receipt := DeliveryReceipt{Agent: agent, Batch: batch}
	for _, delivery := range batch.Deliveries {
		if delivery.Mode == "delta" && delivery.maxSequence > 0 {
			receipt.Cursors = append(receipt.Cursors, receiptCursor{Topic: delivery.Topic, Sequence: delivery.maxSequence})
		}
	}
	if len(receipt.Cursors) == 0 {
		return DeliveryReceipt{}, errors.New("delivery receipt covers nothing")
	}
	return receipt, nil
}

// AcknowledgeDeliveryReceipt moves the agent's cursors to the receipt.
// A repeat is a no-op. A receipt for another agent is refused and writes nothing.
func (c *Client) AcknowledgeDeliveryReceipt(receipt DeliveryReceipt) error {
	if err := c.validateProtocol(); err != nil {
		return err
	}
	agent, err := c.requireAgent()
	if err != nil {
		return err
	}
	if receipt.Agent != agent {
		return errors.New("delivery receipt is for " + receipt.Agent)
	}
	return c.updateCursor(agent, func(cursor *Cursor) {
		for _, item := range receipt.Cursors {
			slot := recordCursorSlot(item.Topic, 0)
			if item.Sequence > cursor.Topics[slot] {
				cursor.Topics[slot] = item.Sequence
			}
		}
	})
}
