//go:build !windows

package claudecli

// candidateExecutables has no confidently known non-Windows fallback
// locations yet; resolution relies on PATH alone.
func candidateExecutables() []string { return nil }
