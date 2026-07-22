package codex

import (
	"strings"

	"github.com/danlavee/Conductor/internal/integrations/execlocate"
)

func resolveExecutable(executable string) (string, error) {
	if strings.TrimSpace(executable) != "" {
		return executable, nil
	}
	resolved, err := execlocate.Find("Codex CLI", "codex", candidateExecutables())
	if err != nil {
		return "", err
	}
	return resolveDesktopExecutable(resolved)
}
