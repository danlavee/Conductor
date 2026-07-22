//go:build !windows

package codex

func resolveDesktopExecutable(resolved string) (string, error) {
	return resolved, nil
}

// candidateExecutables has no confidently known non-Windows fallback
// locations yet; resolution relies on PATH alone.
func candidateExecutables() []string { return nil }
