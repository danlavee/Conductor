package protocol

import "fmt"

// Error is a stable machine-readable coordination error.
type Error struct {
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

func (e *Error) Error() string {
	if e.Agent != "" {
		return fmt.Sprintf("%s: %s (agent: %s)", e.Code, e.Text, e.Agent)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Text)
}
