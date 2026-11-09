package codex

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/danlavee/Conductor/internal/adapters/binding"
)

// HookEvent is Codex's native envelope; other host fields are ignored.
type HookEvent struct {
	SessionID     string `json:"session_id"`
	HookEventName string `json:"hook_event_name"`
	AgentID       string `json:"agent_id,omitempty"`
}

func ParseHookEvent(reader io.Reader) (HookEvent, error) {
	decoder := json.NewDecoder(reader)
	var event HookEvent
	if err := decoder.Decode(&event); err != nil {
		return HookEvent{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return HookEvent{}, errors.New("hook input contains multiple JSON values")
		}
		return HookEvent{}, err
	}
	switch event.HookEventName {
	case "SessionStart", "Stop", "PostToolUse", "SessionEnd":
	default:
		return HookEvent{}, fmt.Errorf("unsupported hook event %q", event.HookEventName)
	}
	if err := binding.Validate(event.SessionID, ""); err != nil {
		return HookEvent{}, fmt.Errorf("hook session_id: %w", err)
	}
	return event, nil
}
