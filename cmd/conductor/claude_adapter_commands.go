package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	adapterbundle "github.com/danlavee/Conductor/adapters"
	"github.com/danlavee/Conductor/internal/adapters/binding"
	"github.com/danlavee/Conductor/internal/adapters/claude"
	"github.com/danlavee/Conductor/internal/install"
	conductor "github.com/danlavee/Conductor/internal/state"
)

var claudeHost = adapterHost{
	binding:     claude.BindingName,
	environment: claude.SessionEnvironment,
	// The hooks resolve ${CLAUDE_PLUGIN_ROOT}/bin/conductor, which fixes where
	// the executable goes; the directory above adapters/ is the user's.
	payload:  install.Payload{Bundle: adapterbundle.ClaudeCode, Parent: "adapters", Name: claude.AdapterName, ExecutableDir: "bin"},
	verbs:    []string{"identity", "arm", "release"},
	input:    "(hook event on stdin)",
	event:    claudeHookEvent,
	target:   hookParent,
	wakeCode: claude.WakeExitCode,
	wake: func(batch conductor.Batch) ([]conductor.ReadResult, error) {
		text, shown := conductor.RenderWake(batch)
		_, err := io.WriteString(os.Stdout, text)
		return shown, err
	},
	announce: func(agent string) error {
		return conductor.WriteJSON(os.Stdout, identityReport{Agent: agent, Advice: identityAdvice})
	},
}

// identityReport is the SessionStart hook's announcement into the session it
// serves: who this session acts as, if anyone yet, and how that is decided.
type identityReport struct {
	Agent  string `json:"agent,omitempty"`
	Advice string `json:"advice"`
}

const identityAdvice = "Conductor's Claude Code adapter serves this session. Joining binds the session to the name you join as, " +
	"and the adapter keeps that name wakeable until the session ends; a join under another name rebinds it."

// claudeHookEvent takes the session from the event on stdin, the one place this
// host tells a hook which session it is in: it substitutes no session into a
// hook's arguments. A missing one fails loudly, as hookSession's does. Every
// event carries session_id; agent_id is set only inside a subagent, where
// PostToolUse fires with the parent's session id and must not arm its watcher.
func claudeHookEvent(verb string, args []string, stdin io.Reader) (string, bool, error) {
	if len(args) != 0 {
		return "", false, errUsage
	}
	var event struct {
		SessionID string `json:"session_id"`
		AgentID   string `json:"agent_id"`
	}
	if err := json.NewDecoder(stdin).Decode(&event); err != nil {
		return "", false, fmt.Errorf("hook event on stdin: %w", err)
	}
	if err := binding.Validate(event.SessionID, ""); err != nil {
		return "", false, fmt.Errorf("hook event session_id: %w", err)
	}
	return event.SessionID, verb != "arm" || event.AgentID == "", nil
}
