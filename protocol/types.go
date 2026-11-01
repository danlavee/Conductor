// Package protocol defines Conductor's transport-neutral wire model.
package protocol

import "time"

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
// agent the bus will wake for it. A publisher that only sees its record back
// has no way to learn it announced into a topic nobody is subscribed to, which
// is a delivery that never happens and reads exactly like one that did.
//
// Recipients is who was woken, never who is subscribed: an agent is not an
// addressee of its own act, so the publisher is absent from its own dispatch
// even on a topic it subscribes to. Reporting the subscriber set instead would
// hand an agent alone on a topic it created its own name back as an audience,
// which is the failure this type exists to make visible.
//
// It wraps Publication rather than extending it because Publication is what
// history stores. The audience is true only of the instant it was resolved, so
// a stored copy would be a claim that goes stale on the next subscription --
// see wire_compat_test.go for what the persisted shape costs to change.
type Dispatch struct {
	Publication
	Recipients []string `json:"recipients"`
}

// Record answers the one record a single-record verb published. Put and Edit
// each publish exactly one, so their callers have no index to choose with. A
// dispatch that published none -- one returned with an error before the
// publication became visible -- answers the zero record, which is unambiguous:
// index 0 is not a valid record index anywhere in the protocol.
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
