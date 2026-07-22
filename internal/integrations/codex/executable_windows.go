package codex

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func resolveDesktopExecutable(resolved string) (string, error) {
	clean := strings.ToLower(filepath.Clean(resolved))
	if !strings.Contains(clean, `\windowsapps\openai.codex_`) || !strings.HasSuffix(clean, `\app\resources\codex.exe`) {
		return resolved, nil
	}
	localAppData := os.Getenv("LOCALAPPDATA")
	if strings.TrimSpace(localAppData) == "" {
		return "", packagedCodexError(resolved)
	}
	candidates, err := filepath.Glob(filepath.Join(localAppData, "OpenAI", "Codex", "bin", "*", "codex.exe"))
	if err != nil {
		return "", fmt.Errorf("find launchable Codex Desktop CLI: %w", err)
	}
	sort.Strings(candidates)
	want, err := executableDigest(resolved)
	if err != nil {
		return "", fmt.Errorf("inspect packaged Codex CLI %q: %w", resolved, err)
	}
	for _, candidate := range candidates {
		got, err := executableDigest(candidate)
		if err == nil && got == want {
			return candidate, nil
		}
	}
	return "", packagedCodexError(resolved)
}

func executableDigest(path string) ([sha256.Size]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return [sha256.Size]byte{}, err
	}
	var result [sha256.Size]byte
	copy(result[:], hash.Sum(nil))
	return result, nil
}

func packagedCodexError(resolved string) error {
	return fmt.Errorf("Codex CLI %q is a protected Codex Desktop package resource and no matching launchable cached copy was found; set %s to a launchable Codex executable", resolved, BinaryEnvironment)
}
