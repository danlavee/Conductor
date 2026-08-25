package claude

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestValidateSessionRejectsWhatCannotNameOneSession covers the two failures
// that motivated having a validator at all, plus the one that makes it a
// safety check rather than a formality.
//
// The empty identifier is what this adapter actually used to compute, on every
// host, by reading a variable the host does not export. The literal placeholder
// is what an older host passes through when it does not know the substitution.
// Both share a property worse than being wrong: every session agrees on them,
// so teardown scoped by one silences a stream a different session is holding.
// The path cases matter because the identifier becomes a filename.
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
// binding: one directory, two agents, each waking as itself. Before it, the
// project file could name only one of them and the second was refused at every
// turn end while looking, from the outside, exactly like a working install.
func TestSessionBindingOutranksTheProjectFile(t *testing.T) {
	root, project := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(project, IdentityFile), []byte("planner\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	first, err := ResolveBinding(root, "session-1", project)
	if err != nil || first.Agent != "planner" || first.Source != FromProject {
		t.Fatalf("unbound session = %+v, %v; want the project's binding", first, err)
	}

	if _, err := BindSession(root, "session-2", "reviewer"); err != nil {
		t.Fatal(err)
	}
	second, err := ResolveBinding(root, "session-2", project)
	if err != nil || second.Agent != "reviewer" || second.Source != FromSession {
		t.Fatalf("bound session = %+v, %v; want its own identity", second, err)
	}
	// The other session is untouched. A binding speaks for one session, not for
	// the directory, or the second agent would have displaced the first.
	again, err := ResolveBinding(root, "session-1", project)
	if err != nil || again.Agent != "planner" {
		t.Fatalf("session-1 = %+v, %v after session-2 bound", again, err)
	}
}

// TestSessionBindingAnswersWithoutAProject covers the case the fallback order
// is written to allow: a session that has said who it is needs nothing from
// the directory, including the directory existing.
func TestSessionBindingAnswersWithoutAProject(t *testing.T) {
	root := t.TempDir()
	if _, err := BindSession(root, "session-1", "reviewer"); err != nil {
		t.Fatal(err)
	}
	binding, err := ResolveBinding(root, "session-1", "")
	if err != nil || binding.Agent != "reviewer" {
		t.Fatalf("binding = %+v, %v; a bound session must not need a project", binding, err)
	}
	// Without one, neither half answers, and that is itself the answer: nothing
	// names an identity for this session. Whether the caller should have had a
	// project directory is a question about the caller, and is asked there.
	binding, err = ResolveBinding(root, "session-2", "")
	if err != nil || binding.Agent != "" {
		t.Fatalf("unbound session with no project = %+v, %v; want an unbound answer", binding, err)
	}
}

func TestBindSessionReportsWhatItReplaced(t *testing.T) {
	root := t.TempDir()
	previous, err := BindSession(root, "session-1", "planner")
	if err != nil || previous != "" {
		t.Fatalf("first bind returned %q, %v", previous, err)
	}
	// Rebinding is allowed, but never silent: an agent that meant to claim a
	// fresh session and instead took over a live one has to be able to see it.
	previous, err = BindSession(root, "session-1", "reviewer")
	if err != nil || previous != "planner" {
		t.Fatalf("rebind returned %q, %v; want the displaced identity", previous, err)
	}
	agent, err := SessionIdentity(root, "session-1")
	if err != nil || agent != "reviewer" {
		t.Fatalf("identity = %q, %v", agent, err)
	}
}

// TestForgetSessionIsIdempotent matters because teardown runs it for every
// session, and most sessions never bound anything. A tantrum there would
// report an error to the user at the end of every unrelated session.
func TestForgetSessionIsIdempotent(t *testing.T) {
	root := t.TempDir()
	if err := ForgetSession(root, "session-1"); err != nil {
		t.Fatalf("forgetting what was never recorded failed: %v", err)
	}
	if _, err := BindSession(root, "session-1", "planner"); err != nil {
		t.Fatal(err)
	}
	if err := ForgetSession(root, "session-1"); err != nil {
		t.Fatal(err)
	}
	agent, err := SessionIdentity(root, "session-1")
	if err != nil || agent != "" {
		t.Fatalf("identity after teardown = %q, %v", agent, err)
	}
}

// TestServingIsRecordedNotInferred pins the fact the adapter field routes on.
// Nothing but the hook can write it, the model's own binding must not imply it,
// and teardown clears it for a session that never bound -- the case that used
// to leave the short-circuit before it reached any clearing at all.
func TestServingIsRecordedNotInferred(t *testing.T) {
	root := t.TempDir()
	served, err := ServesSession(root, "session-1")
	if err != nil || served {
		t.Fatalf("served = %v, %v; want no adapter for a session nothing recorded", served, err)
	}

	// A binding is the model's assertion, and the model runs on hosts with no
	// adapter at all. It must not answer the question the hook answers.
	if _, err := BindSession(root, "session-1", "planner"); err != nil {
		t.Fatal(err)
	}
	if served, err = ServesSession(root, "session-1"); err != nil || served {
		t.Fatalf("served = %v, %v; want binding alone to prove no adapter", served, err)
	}

	if err := RecordSession(root, "session-1"); err != nil {
		t.Fatal(err)
	}
	if served, err = ServesSession(root, "session-1"); err != nil || !served {
		t.Fatalf("served = %v, %v; want the recorded adapter", served, err)
	}
	// SessionStart runs again on resume, compact and fork, over a binding the
	// model made in between.
	agent, err := SessionIdentity(root, "session-1")
	if err != nil || agent != "planner" {
		t.Fatalf("identity after re-recording = %q, %v; want the binding carried forward", agent, err)
	}

	if err := ForgetSession(root, "session-1"); err != nil {
		t.Fatal(err)
	}
	if served, err = ServesSession(root, "session-1"); err != nil || served {
		t.Fatalf("served = %v, %v; want teardown to clear it", served, err)
	}
}

// TestBindingsLiveBesideTheRootRatherThanInsideIt pins the placement, because
// the reason for it is invisible from the code that reads them: a cutover
// replaces the root while the sessions holding these bindings keep running,
// and a binding stored inside the root would be replaced along with it.
func TestBindingsLiveBesideTheRootRatherThanInsideIt(t *testing.T) {
	root := filepath.Join(t.TempDir(), "runtime-state")
	path, err := sessionRecordPath(root, "session-1")
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(path, root+string(filepath.Separator)) {
		t.Fatalf("session binding at %s is inside the root a cutover replaces", path)
	}
	if filepath.Dir(filepath.Dir(path)) != mustAdapterDirectory(t, root) {
		t.Fatalf("session binding at %s is not under this adapter's directory", path)
	}
}

func mustAdapterDirectory(t *testing.T, root string) string {
	t.Helper()
	directory, err := adapterDirectory(root)
	if err != nil {
		t.Fatal(err)
	}
	return directory
}
