// Package grok names the Grok host and how a delivery enters its turn: a Stop
// hook's block decision, whose reason becomes the session's next prompt. The
// wake mechanism itself is internal/adapters/hold.
//
// See docs/integrations/grok.md for this host's mechanism and limits.
package grok

const (
	// AdapterName is the install directory, the plugin name, this adapter's
	// directory in the state root, and the CLI host token.
	AdapterName = "grok"

	// SessionEnvironment is exported to processes the model starts.
	SessionEnvironment = "GROK_SESSION_ID"

	// SessionPlaceholder is substituted into a hook's arguments, and is named
	// here so an unsubstituted one can be rejected.
	SessionPlaceholder = "${GROK_SESSION_ID}"

	// StopDecision blocks the stop and continues with the Stop output's reason,
	// which the host keeps to its first 10,000 characters: a wake fits whole.
	StopDecision = "block"
)
