package conductor

import (
	"github.com/danlavee/Conductor/internal/state"
	"github.com/danlavee/Conductor/protocol"
)

type Agent = protocol.Agent
type Record = protocol.Record
type Publication = protocol.Publication
type Summary = protocol.Summary
type Snapshot = state.Snapshot
type Subscription = state.Subscription
type ReadMode = state.ReadMode
type ReadRequest = state.ReadRequest
type ReadResult = state.ReadResult
type DeliveryMode = state.DeliveryMode
type Delivery = state.Delivery
type BatchDelivery = state.BatchDelivery

const (
	ReadRange = state.ReadRange
	ReadDelta = state.ReadDelta
	ReadFull  = state.ReadFull
)

// DefaultReadLimit is the record cap get and watch apply per response when
// the caller doesn't request more explicitly.
const DefaultReadLimit = state.DefaultReadLimit

const (
	DeliverySummary = state.DeliverySummary
	DeliveryContent = state.DeliveryContent
)

// ParseDeliveryMode validates a --mode flag value. An empty value defaults to content.
func ParseDeliveryMode(value string) (DeliveryMode, error) {
	return state.ParseDeliveryMode(value)
}
