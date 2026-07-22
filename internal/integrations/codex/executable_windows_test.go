package codex

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveDesktopExecutableUsesMatchingCachedCopy(t *testing.T) {
	root := t.TempDir()
	packaged := filepath.Join(root, "WindowsApps", "OpenAI.Codex_test", "app", "resources", "codex.exe")
	cached := filepath.Join(root, "local", "OpenAI", "Codex", "bin", "version", "codex.exe")
	for _, path := range []string{packaged, cached} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("same executable"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("LOCALAPPDATA", filepath.Join(root, "local"))
	got, err := resolveDesktopExecutable(packaged)
	if err != nil {
		t.Fatal(err)
	}
	if got != cached {
		t.Fatalf("resolved executable = %q, want %q", got, cached)
	}
}

func TestCandidateExecutablesListsKnownCodexInstallLocations(t *testing.T) {
	root := t.TempDir()
	localAppData := filepath.Join(root, "local")
	appData := filepath.Join(root, "roaming")
	t.Setenv("LOCALAPPDATA", localAppData)
	t.Setenv("APPDATA", appData)

	versioned := filepath.Join(localAppData, "OpenAI", "Codex", "bin", "1.2.3", "codex.exe")
	if err := os.MkdirAll(filepath.Dir(versioned), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(versioned, []byte("codex"), 0o755); err != nil {
		t.Fatal(err)
	}

	candidates := candidateExecutables()
	npmCodex := filepath.Join(appData, "npm", "codex.cmd")
	want := map[string]bool{versioned: false, npmCodex: false}
	for _, candidate := range candidates {
		if _, ok := want[candidate]; ok {
			want[candidate] = true
		}
	}
	for path, found := range want {
		if !found {
			t.Fatalf("candidates = %#v, want them to include %q", candidates, path)
		}
	}
}

func TestResolveDesktopExecutableRejectsUnmatchedPackageResource(t *testing.T) {
	root := t.TempDir()
	packaged := filepath.Join(root, "WindowsApps", "OpenAI.Codex_test", "app", "resources", "codex.exe")
	cached := filepath.Join(root, "local", "OpenAI", "Codex", "bin", "version", "codex.exe")
	for path, contents := range map[string]string{packaged: "packaged", cached: "different"} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(contents), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("LOCALAPPDATA", filepath.Join(root, "local"))
	if _, err := resolveDesktopExecutable(packaged); err == nil {
		t.Fatal("protected package resource was accepted without a matching cached copy")
	}
}
