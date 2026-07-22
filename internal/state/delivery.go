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
}

func (c *Client) ResolveDelivery(summary Summary, mode DeliveryMode) (Delivery, error) {
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
		result, err := c.Get(ReadRequest{Topic: summary.Topic, Mode: ReadDelta, throughSequence: summary.Sequence})
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
