// Package claude names the Claude Code host: where its adapter installs, where
// its sessions are bound, and what its hooks are handed. The wake mechanism
// itself is internal/adapters/hold.
//
// See docs/integrations/claude-code.md for this host's mechanism and limits,
// and docs/design/model.md for the invariants they serve.
package claude

const (
	// AdapterName is the install directory and the plugin name, deliberately one
	// spelling for both.
	AdapterName = "claude-code"

	// BindingName is this adapter's directory in the state root, holding its
	// session bindings, and the CLI host token.
	BindingName = "claude"

	// SessionEnvironment is exported to processes the model starts.
	SessionEnvironment = "CLAUDE_CODE_SESSION_ID"

	// WakeExitCode starts a turn in an idle session when a backgrounded hook
	// exits with it.
	WakeExitCode = 2
)
