// Package execlocate resolves an external CLI's executable when it is not
// on PATH, by checking a short list of known per-OS install locations. It
// backs every Conductor adapter that shells out to an external tool (Claude
// Code CLI, Antigravity CLI, and Antigravity's agentapi sidecar):
// each supplies its own candidate locations (if any are confidently known
// for its OS), and this package owns the shared "PATH, then candidates,
// then a helpful error" search order.
package execlocate

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Find resolves pathName via PATH first (exec.LookPath). If that fails, it
// checks each candidate absolute path in order and returns the first that
// exists and is not a directory. label names the tool in the not-found
// error, e.g. "Claude Code CLI".
func Find(label, pathName string, candidates []string) (string, error) {
	resolved, lookErr := exec.LookPath(pathName)
	if lookErr == nil {
		return resolved, nil
	}
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("find %s: not found on PATH or in known install locations; install it or add its executable to PATH: %w", label, lookErr)
}
