package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/danlavee/Conductor/internal/adapters/grok"
)

// Grok has no watcher during a turn, so PostToolUse hands over what arrived:
// the wake as additionalContext, acknowledged once written. A subagent's tool
// result, an unbound session and nothing unread all write nothing, which is
// Grok's no-op.
func TestGrokInjectHandsOverWorkInsideATurn(t *testing.T) {
	root := filepath.Join(t.TempDir(), "runtime-state")
	inject := func(stdin string) ([]byte, []byte, error) {
		return cli{home: root, dir: t.TempDir(), stdin: stdin}.run("adapter", "grok", "inject", "--session=session-1")
	}
	if stdout, stderr, err := inject(`{}`); err != nil || len(stdout) != 0 {
		t.Fatalf("inject in an unbound session = %s, %v: %s", stdout, err, stderr)
	}
	if _, stderr, err := (cli{home: root, dir: t.TempDir(), stdin: `{}`}).run("adapter", "grok", "identity", "--session=session-1"); err != nil {
		t.Fatalf("identity: %v: %s", err, stderr)
	}
	inGrok := cli{home: root, dir: t.TempDir(), env: map[string]string{grok.SessionEnvironment: "session-1"}}
	if _, stderr, err := inGrok.run("a", "join", "dev"); err != nil {
		t.Fatalf("join a: %v: %s", err, stderr)
	}
	if _, stderr, err := (cli{home: root}).run("b", "join", "review"); err != nil {
		t.Fatalf("join b: %v: %s", err, stderr)
	}

	if stdout, stderr, err := inject(`{"subagentType":"worker"}`); err != nil || len(stdout) != 0 {
		t.Fatalf("a subagent's inject = %s, %v: %s", stdout, err, stderr)
	}
	stdout, stderr, err := inject(`{}`)
	var output struct {
		HookSpecificOutput struct{ HookEventName, AdditionalContext string } `json:"hookSpecificOutput"`
	}
	if err != nil || json.Unmarshal(stdout, &output) != nil || output.HookSpecificOutput.HookEventName != "PostToolUse" ||
		!strings.HasPrefix(output.HookSpecificOutput.AdditionalContext, "Conductor: ") || !strings.Contains(output.HookSpecificOutput.AdditionalContext, "b: review") {
		t.Fatalf("inject = %s, %v: %s", stdout, err, stderr)
	}
	if stdout, stderr, err := inject(`{}`); err != nil || len(stdout) != 0 {
		t.Fatalf("inject with nothing unread = %s, %v: %s", stdout, err, stderr)
	}
}

// A hook always gets a payload and a substituted session; without either it
// is misconfigured, and says so.
func TestGrokHooksFailLoudlyWithoutTheirContext(t *testing.T) {
	root := filepath.Join(t.TempDir(), "runtime-state")
	for name, args := range map[string][]string{
		"an unsubstituted session": {"adapter", "grok", "identity", "--session=" + grok.SessionPlaceholder},
		"no payload":               {"adapter", "grok", "arm", "--session=session-1"},
	} {
		if _, _, err := (cli{home: root}).run(args...); err == nil {
			t.Errorf("a hook with %s was accepted", name)
		}
	}
}
