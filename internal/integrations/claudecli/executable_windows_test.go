//go:build windows

package claudecli

import (
	"path/filepath"
	"testing"
)

func TestCandidateExecutablesListsKnownClaudeInstallLocations(t *testing.T) {
	root := t.TempDir()
	appData := filepath.Join(root, "roaming")
	home := filepath.Join(root, "home")
	t.Setenv("APPDATA", appData)
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home) // os.UserHomeDir prefers USERPROFILE on Windows, HOME is a harmless belt-and-braces set

	candidates := candidateExecutables()
	want := []string{
		filepath.Join(appData, "npm", "claude.cmd"),
		filepath.Join(home, ".local", "bin", "claude.exe"),
	}
	for _, path := range want {
		if !containsString(candidates, path) {
			t.Fatalf("candidates = %#v, want them to include %q", candidates, path)
		}
	}
}

func containsString(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}
