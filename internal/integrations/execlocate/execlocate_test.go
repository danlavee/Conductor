package execlocate

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestFindPrefersPathOverCandidates(t *testing.T) {
	dir := t.TempDir()
	found := filepath.Join(dir, exeName("execlocate-path-tool"))
	writeExecutable(t, found)
	t.Setenv("PATH", dir)

	got, err := Find("Test Tool", "execlocate-path-tool", []string{"should-not-be-used"})
	if err != nil {
		t.Fatal(err)
	}
	if got != found {
		t.Fatalf("resolved = %q, want %q", got, found)
	}
}

func TestFindFallsBackToFirstExistingCandidateWhenNotOnPath(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	dir := t.TempDir()
	candidate := filepath.Join(dir, "execlocate-candidate-tool.exe")
	writeExecutable(t, candidate)

	got, err := Find("Test Tool", "execlocate-missing-tool", []string{filepath.Join(dir, "does-not-exist.exe"), candidate})
	if err != nil {
		t.Fatal(err)
	}
	if got != candidate {
		t.Fatalf("resolved = %q, want %q", got, candidate)
	}
}

func TestFindSkipsDirectoryCandidates(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	dir := t.TempDir()
	candidate := filepath.Join(dir, "execlocate-file-tool.exe")
	writeExecutable(t, candidate)

	got, err := Find("Test Tool", "execlocate-missing-tool", []string{dir, candidate})
	if err != nil {
		t.Fatal(err)
	}
	if got != candidate {
		t.Fatalf("resolved = %q, want %q (directory candidate should be skipped)", got, candidate)
	}
}

func TestFindFailsWithHelpfulErrorWhenNotFoundAnywhere(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	_, err := Find("Test Tool", "execlocate-missing-tool", []string{filepath.Join(t.TempDir(), "does-not-exist.exe"), ""})
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, want := range []string{"Test Tool", "PATH", "install"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %q, want it to contain %q", err.Error(), want)
		}
	}
}

func writeExecutable(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("stub"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func exeName(base string) string {
	if runtime.GOOS == "windows" {
		return base + ".exe"
	}
	return base
}
