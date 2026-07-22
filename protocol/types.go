// Package protocol defines Conductor's transport-neutral wire model.
package protocol

import "time"

// Agent is one registry entry.
type Agent struct {
	Name           string    `json:"name"`
	Responsibility string    `json:"responsibility"`
	Timestamp      time.Time `json:"timestamp"`
}

// MessagePayload is the complete authored message payload.
type MessagePayload struct {
	Text string `json:"text"`
}

// Message is the latest current message for one resource key.
type Message struct {
	Key       string         `json:"key"`
	Kind      string         `json:"kind"`
	Payload   MessagePayload `json:"payload"`
	Agent     string         `json:"agent"`
	Index     int64          `json:"index"`
	Timestamp time.Time      `json:"timestamp"`
}

// MutationOperation identifies a protocol edit. Message kinds remain unrestricted strings.
type MutationOperation string

const (
	MutationSet     MutationOperation = "set"
	MutationScratch MutationOperation = "scratch"
)

// MessageMutation creates, replaces, or scratches one keyed message.
type MessageMutation struct {
	Operation MutationOperation `json:"operation"`
	Kind      string            `json:"kind,omitempty"`
	Payload   *MessagePayload   `json:"payload,omitempty"`
}

// Signal wakes an agent after membership or resource publication.
type Signal struct {
	Type     string `json:"type"`
	Resource string `json:"resource"`
	Key      string `json:"key"`
	Index    int64  `json:"index"`
	Agent    string `json:"agent"`
}

// Publication is one atomically published resource update.
type Publication struct {
	Index     int64                      `json:"index"`
	Resource  string                     `json:"resource"`
	Agent     string                     `json:"agent"`
	Timestamp time.Time                  `json:"timestamp"`
	Messages  map[string]MessageMutation `json:"messages"`
}
