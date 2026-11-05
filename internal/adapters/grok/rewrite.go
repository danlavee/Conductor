package grok

import (
	"path/filepath"
	"runtime"
	"strings"
)

// HookCommandPlaceholder is rewritten to this install's executable at publish time.
const HookCommandPlaceholder = "${CONDUCTOR_BIN}"

// RewriteHookCommand names the binary the way Grok executes a hook: no shell.
func RewriteHookCommand(name string, data []byte) []byte {
	if filepath.ToSlash(name) != "hooks/hooks.json" {
		return data
	}
	command := "${GROK_PLUGIN_ROOT}/bin/conductor"
	if runtime.GOOS == "windows" {
		command += ".exe"
	}
	return []byte(strings.ReplaceAll(string(data), HookCommandPlaceholder, command))
}
