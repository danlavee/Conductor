package conductor

import (
	"github.com/danlavee/Conductor/internal/state"
	"github.com/danlavee/Conductor/protocol"
)

type Agent = protocol.Agent
type Record = protocol.Record
type Publication = protocol.Publication
type Dispatch = protocol.Dispatch
type Summary = protocol.Summary
type WatchStatus = state.WatchStatus
type WatchHolder = state.WatchHolder
type HolderKind = state.HolderKind
type RosterEntry = state.RosterEntry
type Snapshot = state.Snapshot
type Subscription = state.Subscription
type ReadMode = state.ReadMode
type ReadRequest = state.ReadRequest
type ReadResult = state.ReadResult
type Delivery = state.Delivery
type BatchDelivery = state.BatchDelivery

const (
	ReadRange = state.ReadRange
	ReadDelta = state.ReadDelta
	ReadFull  = state.ReadFull

	HolderSelf    = state.HolderSelf
	HolderAdapter = state.HolderAdapter
	HolderUnknown = state.HolderUnknown
)

// DefaultReadLimit caps get and watch responses. Range and delta reads may
// request more explicitly; full reads always use this cap.
const DefaultReadLimit = state.DefaultReadLimit
