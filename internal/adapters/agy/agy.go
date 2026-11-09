// Package agy names the Antigravity host: where its hooks find their session
// and their session process, and how a delivery enters a turn. The wake
// mechanism itself is internal/adapters/hold.
//
// See docs/integrations/agy.md for this host's mechanism and limits.
package agy

import (
	"encoding/json"
	"io"
	"path/filepath"
	"strings"

	"github.com/danlavee/Conductor/internal/adapters/binding"
	"github.com/danlavee/Conductor/internal/platform"
)

const (
	// AdapterName is the install directory, the plugin name, this adapter's
	// directory in the state root, and the CLI host token.
	AdapterName = "agy"

	// SessionEnvironment is the conversation identifier exported to processes
	// the model starts.
	SessionEnvironment = "ANTIGRAVITY_CONVERSATION_ID"

	// StopDecision continues the agent loop with the Stop output's reason.
	StopDecision = "continue"

	// endTurn is the termination reason of a turn the model ended itself.
	endTurn = "NO_TOOL_CALL"
)

// Hook is the part of a hook's stdin payload this adapter reads.
type Hook struct {
	ConversationID    string `json:"conversationId"`
	TerminationReason string `json:"terminationReason"`
}

// ReadHook decodes a hook's payload. The host substitutes nothing into a
// hook's command, so the payload is the only source of its session.
func ReadHook(r io.Reader) (Hook, error) {
	var hook Hook
	if err := json.NewDecoder(r).Decode(&hook); err != nil {
		return Hook{}, err
	}
	return hook, binding.Validate(hook.ConversationID, "")
}

// EndsTurn reports a Stop of a turn the model ended, the one a delivery may continue.
func (h Hook) EndsTurn() bool { return h.TerminationReason == endTurn }

// Shell reports the shell the host wraps every hook command in, which stands
// between a hook and the session process.
func Shell(process platform.ProcessInstance) bool {
	switch strings.TrimSuffix(strings.ToLower(filepath.Base(process.Executable)), ".exe") {
	case "cmd", "sh", "bash", "dash", "zsh":
		return true
	}
	return false
}
