package main

import (
	"encoding/json"
	"io"
	"os"

	adapterbundle "github.com/danlavee/Conductor/adapters"
	"github.com/danlavee/Conductor/internal/adapters/grok"
	"github.com/danlavee/Conductor/internal/install"
	conductor "github.com/danlavee/Conductor/internal/state"
)

var grokHost = adapterHost{
	binding:     grok.AdapterName,
	environment: grok.SessionEnvironment,
	payload: install.Payload{
		Bundle: adapterbundle.Grok, Parent: "adapters", Name: grok.AdapterName, ExecutableDir: "bin",
		// Grok executes a hook with no shell, so the command is the binary itself.
		HookCommand: func(executable string) string { return "${GROK_PLUGIN_ROOT}/" + executable },
	},
	verbs:  []string{"identity", "arm", "inject", "release"},
	input:  "--session=<id>",
	event:  grokEvent,
	target: hookParent,
	// Grok reads a Stop hook's block decision on a clean exit.
	wake: func(batch conductor.Batch) ([]conductor.ReadResult, error) {
		reason, shown := conductor.RenderWake(batch)
		return shown, conductor.WriteJSON(os.Stdout, stopDelivery{Decision: grok.StopDecision, Reason: reason})
	},
	inject: func(batch conductor.Batch) ([]conductor.ReadResult, error) {
		text, shown := conductor.RenderWake(batch)
		return shown, conductor.WriteJSON(os.Stdout, toolContext(text))
	},
}

// grokEvent takes the session from the substituted argument. Stop, PostToolUse
// and SessionEnd also fire for subagents, which must neither wake, feed nor end
// the session, and only a turn the model ended is one a delivery may continue.
func grokEvent(verb string, args []string, stdin io.Reader) (string, bool, error) {
	session, err := hookSession(args, grok.SessionPlaceholder)
	if err != nil || verb == "identity" {
		return session, true, err
	}
	var event struct {
		Reason       string `json:"reason"`
		SubagentType string `json:"subagentType"`
	}
	if err := json.NewDecoder(stdin).Decode(&event); err != nil {
		return "", false, err
	}
	return session, event.SubagentType == "" && (verb != "arm" || event.Reason == "end_turn"), nil
}

// postToolUseOutput adds text to the turn after the tool result that ran the
// hook; Grok keeps its first 10,000 characters, so a wake passes whole.
type postToolUseOutput struct {
	HookSpecificOutput struct {
		HookEventName     string `json:"hookEventName"`
		AdditionalContext string `json:"additionalContext"`
	} `json:"hookSpecificOutput"`
}

func toolContext(text string) postToolUseOutput {
	var output postToolUseOutput
	output.HookSpecificOutput.HookEventName = "PostToolUse"
	output.HookSpecificOutput.AdditionalContext = text
	return output
}
