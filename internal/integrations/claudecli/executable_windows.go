//go:build windows

package claudecli

import (
	"os"
	"path/filepath"
)

// candidateExecutables lists known per-user Claude Code CLI install
// locations checked when claude isn't on PATH:
//   - npm's global install directory on Windows (npm's default prefix is
//     %APPDATA%\npm, where global packages get a .cmd shim), since Claude
//     Code is commonly installed via `npm install -g @anthropic-ai/claude-code`.
//   - the native installer's per-user bin directory, mirroring the
//     `~/.local/bin` convention it uses on macOS/Linux.
func candidateExecutables() []string {
	var candidates []string
	if appData := os.Getenv("APPDATA"); appData != "" {
		candidates = append(candidates, filepath.Join(appData, "npm", "claude.cmd"))
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		candidates = append(candidates, filepath.Join(home, ".local", "bin", "claude.exe"))
	}
	return candidates
}
