package state

import "github.com/danlavee/Conductor/protocol"

type Agent = protocol.Agent
type Record = protocol.Record
type Publication = protocol.Publication
type Dispatch = protocol.Dispatch
type Summary = protocol.Summary
type ProtocolError = protocol.Error
type ProtocolVersionDetail = protocol.ProtocolVersionDetail

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
	Agents []RosterEntry       `json:"agents"`
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
	Remaining    int           `json:"remaining,omitempty"`
	DefaultLimit int           `json:"default_read_limit,omitempty"`
	maxSequence  int64
	record       int64
}

// Subscription selects topic content for one agent's delta.
type Subscription struct {
	TopicGroups []string `json:"topic_groups"`
	Topics      []string `json:"topics"`
}
