package conductor

import (
	"github.com/danlavee/Conductor/internal/state"
	"github.com/danlavee/Conductor/protocol"
)

type Agent = protocol.Agent
type Record = protocol.Record
type Publication = protocol.Publication
type Summary = protocol.Summary
type ReplacementActivation = state.ReplacementActivation
type WatchResult = state.WatchResult
type WatchStatus = state.WatchStatus
type RosterEntry = state.RosterEntry
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

// DefaultReadLimit caps get and watch responses. Range and delta reads may
// request more explicitly; full reads always use this cap.
const DefaultReadLimit = state.DefaultReadLimit

// Delivery mode is chosen by whoever holds the stream, as a typed argument.
// There is no parser, because there is no flag to parse: the CLI resolves
// content, and an adapter names one of these when it arms.
const (
	DeliverySummary = state.DeliverySummary
	DeliveryContent = state.DeliveryContent
)
