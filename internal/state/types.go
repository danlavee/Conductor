package state

import (
	"time"

	"github.com/danlavee/Conductor/protocol"
)

type Agent = protocol.Agent
type Record = protocol.Record
type Publication = protocol.Publication
type Summary = protocol.Summary
type ProtocolError = protocol.Error
type ProtocolVersionDetail = protocol.ProtocolVersionDetail

// Lock is durable resource-lease metadata.
type Lock struct {
	PID          int       `json:"pid"`
	ProcessStart string    `json:"process_start,omitempty"`
	LeaseID      uint64    `json:"lease_id"`
	Agent        string    `json:"agent"`
	Timestamp    time.Time `json:"timestamp"`
	TimeoutSec   int       `json:"timeout_sec"`
}

// Transaction is the durable overlay created by Begin.
type Transaction struct {
	Topic    string           `json:"topic"`
	Agent    string           `json:"agent"`
	PID      int              `json:"pid"`
	Started  time.Time        `json:"started"`
	Sequence int64            `json:"sequence,omitempty"`
	Records  map[int64]Record `json:"records"`
	Created  map[int64]bool   `json:"created"`
}

type IndexRange struct {
	From int64 `json:"from"`
	To   int64 `json:"to"`
}

type Cursor struct {
	Summary       int64            `json:"summary_sequence"`
	SummaryRanges []IndexRange     `json:"summary_ranges,omitempty"`
	InboxOffset   int64            `json:"inbox_offset"`
	Topics        map[string]int64 `json:"topic_sequences"`
}

type Event struct {
	Summary    Summary  `json:"summary"`
	Recipients []string `json:"recipients"`
}

type Snapshot struct {
	Agents []Agent             `json:"agents"`
	Topics map[string][]Record `json:"topics"`
	heads  map[string]int64
}

type ReadMode int

const (
	ReadRange ReadMode = iota
	ReadDelta
	ReadFull
)

type ReadRequest struct {
	Topic           string
	RecordIndex     int64
	Mode            ReadMode
	Start           int64
	End             int64
	Limit           int
	throughSequence int64
}

type ReadResult struct {
	Mode         string        `json:"mode"`
	Topic        string        `json:"topic"`
	Records      []Record      `json:"records,omitempty"`
	Publications []Publication `json:"publications,omitempty"`
	maxSequence  int64
	record       int64
}

// Subscription selects topic content for one agent's delta.
type Subscription struct {
	TopicGroups []string `json:"topic_groups"`
	Topics      []string `json:"topics"`
}
