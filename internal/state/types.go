package state

import (
	"time"

	"github.com/danlavee/Conductor/protocol"
)

type Agent = protocol.Agent
type MessagePayload = protocol.MessagePayload
type Message = protocol.Message
type MutationOperation = protocol.MutationOperation
type MessageMutation = protocol.MessageMutation
type Signal = protocol.Signal
type Publication = protocol.Publication
type ProtocolError = protocol.Error
type ConflictDetail = protocol.ConflictDetail
type ProtocolVersionDetail = protocol.ProtocolVersionDetail

const (
	MutationSet     = protocol.MutationSet
	MutationScratch = protocol.MutationScratch
)

// Lock is durable resource-lease metadata.
type Lock struct {
	PID          int       `json:"pid"`
	ProcessStart string    `json:"process_start,omitempty"`
	LeaseID      uint64    `json:"lease_id"`
	Agent        string    `json:"agent"`
	Timestamp    time.Time `json:"timestamp"`
	TimeoutSec   int       `json:"timeout_sec"`
}

// Transaction is the durable buffer created by Begin and updated by Set.
type Transaction struct {
	Resource string                     `json:"resource"`
	Agent    string                     `json:"agent"`
	PID      int                        `json:"pid"`
	Started  time.Time                  `json:"started"`
	Index    int64                      `json:"index,omitempty"`
	Messages map[string]MessageMutation `json:"messages"`
}

// WriteOptions controls admission of a resource write. A zero value is unconditional.
type WriteOptions struct {
	// IfIndex requires each named message to remain at its last-read publication
	// index. Index zero requires absence. Conditions may include unchanged messages;
	// changed messages without a condition remain unconditional. The map is copied.
	IfIndex map[string]int64
}

type IndexRange struct {
	From int64 `json:"from"`
	To   int64 `json:"to"`
}

type Cursor struct {
	Signal       int64            `json:"signal_index"`
	SignalRanges []IndexRange     `json:"signal_ranges,omitempty"`
	InboxOffset  int64            `json:"inbox_offset"`
	Resources    map[string]int64 `json:"resource_indexes"`
}

type Event struct {
	Signal     Signal   `json:"signal"`
	Recipients []string `json:"recipients"`
}

type Session struct {
	Agent       string    `json:"agent"`
	ParentPID   int       `json:"parent_pid"`
	ParentStart string    `json:"parent_start"`
	BoundAt     time.Time `json:"bound_at"`
}

type Snapshot struct {
	Agents    []Agent                       `json:"agents"`
	Resources map[string]map[string]Message `json:"resources"`
}

type ReadMode int

const (
	ReadDelta ReadMode = iota
	ReadHistorical
	ReadFull
)

type ReadRequest struct {
	Resource string
	Key      string
	Mode     ReadMode
	From     int64
	To       int64
}

type ReadResult struct {
	Mode     string        `json:"mode"`
	Resource string        `json:"resource"`
	From     int64         `json:"from,omitempty"`
	To       int64         `json:"to,omitempty"`
	Messages []Message     `json:"messages,omitempty"`
	History  []Publication `json:"history,omitempty"`
	maxIndex int64
	key      string
}

type materializedMessage struct {
	Message
	Scratched bool `json:"scratched,omitempty"`
}
