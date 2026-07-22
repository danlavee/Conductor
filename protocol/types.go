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

// Summary is the lightweight wake information returned instead of content.
// Its sequence identifies the publication for an update.
type Summary struct {
	Type     string `json:"type"`
	Topic    string `json:"topic"`
	Sequence int64  `json:"sequence"`
	Agent    string `json:"agent"`
}
