package codex

import (
	"fmt"
	"os/exec"
	"strings"
)

func resolveExecutable(executable string) (string, error) {
	if strings.TrimSpace(executable) != "" {
		return executable, nil
	}
	resolved, err := exec.LookPath("codex")
	if err != nil {
		return "", fmt.Errorf("find Codex CLI: install it or set %s to its executable: %w", BinaryEnvironment, err)
	}
	return resolveDesktopExecutable(resolved)
}
