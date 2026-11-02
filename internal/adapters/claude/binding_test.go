package claude

import (
	"os"
	"path/filepath"
	"testing"
)

// TestValidateSessionRejectsWhatCannotNameOneSession covers the identifiers
// every session would agree on -- so teardown scoped by one silences a stream a
// different session is holding -- and the path cases, because the identifier
// becomes a filename.
func TestValidateSessionRejectsWhatCannotNameOneSession(t *testing.T) {
	for name, session := range map[string]string{
		"empty":                  "",
		"blank":                  "   ",
		"unsubstituted":          SessionPlaceholder,
		"a parent reference":     "..",
		"a path separator":       "a/b",
		"a windows separator":    `a\b`,
		"an absolute path":       "/etc/passwd",
		"a drive-relative path":  `C:sessions`,
		"an embedded null-ish .": "session.1",
	} {
		if err := ValidateSession(session); err == nil {
			t.Errorf("%s (%q) was accepted as a session identifier", name, session)
		}
	}
	// The shapes the host actually issues: a UUID from ${CLAUDE_SESSION_ID},
	// and the prefixed form the desktop host uses.
	for _, session := range []string{
		"56559029-7fd8-4037-9d8b-0b38de0c8f7d",
		"local_4f6ef3b8-9dad-4409-8171-4eb2573fe44d",
	} {
		if err := ValidateSession(session); err != nil {
			t.Errorf("a real session identifier %q was rejected: %v", session, err)
		}
	}
}

// TestSessionBindingOutranksTheProjectFile is the whole point of the session
// binding: one directory, two agents, each waking as itself.
func TestSessionBindingOutranksTheProjectFile(t *testing.T) {
	root, project := t.TempDir(), t.TempDir()
	if binding, err := ResolveBinding(root, "session-1", project); err != nil || binding != (Binding{Source: NoBinding}) {
		t.Fatalf("unbound project = %+v, %v; want silence", binding, err)
	}
	// Notepad and PowerShell's Set-Content -Encoding utf8 both prepend a byte
	// order mark, which would otherwise ride into the agent name.
	if err := os.WriteFile(filepath.Join(project, IdentityFile), []byte("\xef\xbb\xbfplanner\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	first, err := ResolveBinding(root, "session-1", project)
	if err != nil || first != (Binding{Agent: "planner", Source: FromProject}) {
		t.Fatalf("unbound session = %+v, %v; want the project's binding", first, err)
	}

	if err := BindSession(root, "session-2", "reviewer"); err != nil {
		t.Fatal(err)
	}
	second, err := ResolveBinding(root, "session-2", project)
	if err != nil || second != (Binding{Agent: "reviewer", Source: FromSession}) {
		t.Fatalf("bound session = %+v, %v; want its own identity", second, err)
	}
	// A binding speaks for one session, not for the directory.
	again, err := ResolveBinding(root, "session-1", project)
	if err != nil || again.Agent != "planner" {
		t.Fatalf("session-1 = %+v, %v after session-2 bound", again, err)
	}

	// Teardown runs for every session, most of which never bound anything, so
	// forgetting is idempotent.
	for range 2 {
		if err := ForgetSession(root, "session-2"); err != nil {
			t.Fatal(err)
		}
	}
	if binding, err := ResolveBinding(root, "session-2", project); err != nil || binding.Source != FromProject {
		t.Fatalf("session-2 after teardown = %+v, %v; want the project's binding back", binding, err)
	}
}
