package conductor

import (
	"github.com/danlavee/Conductor/internal/state"
	"github.com/danlavee/Conductor/protocol"
)

type Agent = protocol.Agent
type MessagePayload = protocol.MessagePayload
type Message = protocol.Message
type MutationOperation = protocol.MutationOperation
type MessageMutation = protocol.MessageMutation
type Signal = protocol.Signal
type Publication = protocol.Publication

const (
	MutationSet     = protocol.MutationSet
	MutationScratch = protocol.MutationScratch
)

type WriteOptions = state.WriteOptions
type Snapshot = state.Snapshot
type ReadMode = state.ReadMode
type ReadRequest = state.ReadRequest
type ReadResult = state.ReadResult

const (
	ReadDelta      = state.ReadDelta
	ReadHistorical = state.ReadHistorical
	ReadFull       = state.ReadFull
)
