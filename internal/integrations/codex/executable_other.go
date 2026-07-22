//go:build !windows

package codex

func resolveDesktopExecutable(resolved string) (string, error) {
	return resolved, nil
}
