package state

import (
	"fmt"
	"time"
)

// Agent is one registry entry.
type Agent struct {
	Name           string    `json:"name"`
	Responsibility string    `json:"responsibility"`
	Timestamp      time.Time `json:"timestamp"`
}

// Record is one editable topic value. Its index is its stable key.
type Record struct {
	Index int64  `json:"index"`
	Text  string `json:"text"`
}

// Publication is one atomic addition or change of records in one topic.
// Sequence orders publications globally and is unrelated to record indexes.
type Publication struct {
	Sequence  int64     `json:"sequence"`
	Topic     string    `json:"topic"`
	Agent     string    `json:"agent"`
	Timestamp time.Time `json:"timestamp"`
	Records   []Record  `json:"records"`
}

// Dispatch is what one publishing act did: the publication it made, and every
// agent the bus will wake for it -- never the publisher itself. It wraps
// Publication rather than extending it because the audience is true only of
// the instant it was resolved, while Publication is what history stores.
type Dispatch struct {
	Publication
	Recipients []string `json:"recipients"`
}

// Record answers the one record a single-record verb published; a dispatch
// that published none answers the zero record, since index 0 is never valid.
func (d Dispatch) Record() Record {
	if len(d.Records) == 0 {
		return Record{}
	}
	return d.Records[0]
}

// Summary is the lightweight wake information returned instead of content.
// Its sequence identifies the publication for an update.
type Summary struct {
	Type     string `json:"type"`
	Topic    string `json:"topic"`
	Sequence int64  `json:"sequence"`
	Agent    string `json:"agent"`
}

// ProtocolError is a stable machine-readable coordination error.
type ProtocolError struct {
	Code     string                 `json:"code"`
	Agent    string                 `json:"agent,omitempty"`
	Text     string                 `json:"message"`
	Protocol *ProtocolVersionDetail `json:"protocol,omitempty"`
}

// ProtocolVersionDetail identifies the supported and encountered state versions.
type ProtocolVersionDetail struct {
	Supported int  `json:"supported"`
	Found     *int `json:"found,omitempty"`
}

func (e *ProtocolError) Error() string {
	if e.Agent != "" {
		return fmt.Sprintf("%s: %s (agent: %s)", e.Code, e.Text, e.Agent)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Text)
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
