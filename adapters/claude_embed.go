package adapterbundle

import "embed"

// The all: prefix is what carries `.claude-plugin`, which the host reads.
//
//go:embed all:claude-code
var claudeCode embed.FS

// ClaudeCode is rooted at the plugin manifest the host loads.
var ClaudeCode = mustSub(claudeCode, "claude-code")
