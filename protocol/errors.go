package protocol

import "fmt"

// Error is a stable machine-readable coordination error.
type Error struct {
	Code     string                 `json:"code"`
	Agent    string                 `json:"agent,omitempty"`
	Text     string                 `json:"message"`
	Conflict *ConflictDetail        `json:"conflict,omitempty"`
	Protocol *ProtocolVersionDetail `json:"protocol,omitempty"`
}

// ProtocolVersionDetail identifies the supported and encountered state versions.
type ProtocolVersionDetail struct {
	Supported int  `json:"supported"`
	Found     *int `json:"found,omitempty"`
}

// ConflictDetail identifies the stale message that rejected a conditional write.
type ConflictDetail struct {
	Resource      string `json:"resource"`
	Key           string `json:"key"`
	ExpectedIndex int64  `json:"expected_index"`
	CurrentIndex  int64  `json:"current_index"`
}

func (e *Error) Error() string {
	if e.Conflict != nil {
		return fmt.Sprintf("%s: %s (%s/%s: expected index %d, current index %d)", e.Code, e.Text, e.Conflict.Resource, e.Conflict.Key, e.Conflict.ExpectedIndex, e.Conflict.CurrentIndex)
	}
	if e.Agent != "" {
		return fmt.Sprintf("%s: %s (agent: %s)", e.Code, e.Text, e.Agent)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Text)
}
