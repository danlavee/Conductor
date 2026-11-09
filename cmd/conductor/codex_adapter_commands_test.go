package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/danlavee/Conductor/internal/adapters/codex"
	"github.com/danlavee/Conductor/internal/install"
	conductor "github.com/danlavee/Conductor/internal/state"
)

func TestCodexAdapterInstallPlacesTheBinaryInThePluginCacheRoot(t *testing.T) {
	destination := filepath.Join(t.TempDir(), "adapters", "codex")
	stdout, stderr, err := (cli{home: t.TempDir()}).run("adapter", "codex", "install", destination)
	if err != nil {
		t.Fatalf("install: %v: %s", err, stderr)
	}
	var result install.Result
	if err := json.Unmarshal(stdout, &result); err != nil {
		t.Fatal(err)
	}
	expected := filepath.Join(destination, "plugins", "conductor", "bin", "conductor")
	if runtime.GOOS == "windows" {
		expected += ".exe"
	}
	if result.Status != "installed" || result.BinaryPath != expected {
		t.Fatalf("install result = %+v, want binary %q", result, expected)
	}
	for _, required := range []string{
		".agents/plugins/marketplace.json",
		"plugins/conductor/.codex-plugin/plugin.json",
		"plugins/conductor/hooks/hooks.json",
	} {
		if _, err := os.Stat(filepath.Join(destination, filepath.FromSlash(required))); err != nil {
			t.Errorf("missing %s: %v", required, err)
		}
	}
}

func TestCodexJoinBindsResumesAndHandsOffTheNativeSession(t *testing.T) {
	root := filepath.Join(t.TempDir(), "runtime-state")
	identity := func(session, want string) {
		t.Helper()
		stdout, stderr, err := (cli{home: root, stdin: codexHookInput(t, "SessionStart", session, "")}).run("adapter", "codex", "identity")
		if err != nil || json.Valid(stdout) || !strings.Contains(string(stdout), want) {
			t.Fatalf("native identity = %s, %v: %s; want %q", stdout, err, stderr, want)
		}
	}
	join := func(session, agent, responsibility, wantTookOver string) {
		t.Helper()
		args := []string{agent, "join"}
		if responsibility != "" {
			args = append(args, responsibility)
		}
		stdout, stderr, err := (cli{home: root, env: map[string]string{codex.ThreadEnvironment: session}}).run(args...)
		if err != nil {
			t.Fatalf("join: %v: %s", err, stderr)
		}
		var answer struct {
			Wake     string `json:"wake"`
			TookOver string `json:"took_over"`
		}
		if err := json.Unmarshal(stdout, &answer); err != nil || answer.Wake != "adapter" || answer.TookOver != wantTookOver {
			t.Fatalf("native join = %s, %v; want adapter, took_over %q", stdout, err, wantTookOver)
		}
	}
	identity("session-1", "No identity is bound yet")
	join("session-1", "planner", "dev", "")
	identity("session-1", "Current identity: planner.")
	join("session-1", "reviewer", "review", "")
	identity("session-1", "Current identity: reviewer.")
	identity("session-2", "No identity is bound yet")
	join("session-2", "reviewer", "", "codex")
	identity("session-1", "No identity is bound yet")
	childEnd := cli{home: root, stdin: codexHookInput(t, "SessionEnd", "session-2", "subagent-1")}
	if _, stderr, err := childEnd.run("adapter", "codex", "release"); err != nil {
		t.Fatalf("subagent release: %v: %s", err, stderr)
	}
	identity("session-2", "Current identity: reviewer.")
	end := cli{home: root, stdin: codexHookInput(t, "SessionEnd", "session-2", "")}
	if _, stderr, err := end.run("adapter", "codex", "release"); err != nil {
		t.Fatalf("release: %v: %s", err, stderr)
	}
	identity("session-2", "No identity is bound yet")
}

func TestCodexAdapterValidatesNativeEventsAndSkipsUnboundArms(t *testing.T) {
	root := t.TempDir()
	for name, test := range map[string]struct {
		args  []string
		input string
	}{
		"identity from Stop": {[]string{"identity"}, codexHookInput(t, "Stop", "s", "")},
		"arm from End":       {[]string{"arm"}, codexHookInput(t, "SessionEnd", "s", "")},
		"release from Stop":  {[]string{"release"}, codexHookInput(t, "Stop", "s", "")},
		"inject from Stop":   {[]string{"inject"}, codexHookInput(t, "Stop", "s", "")},
		"retired bind":       {[]string{"bind", "planner"}, ""},
		"unknown command":    {[]string{"wake"}, ""},
		"stray hook arg":     {[]string{"identity", "extra"}, ""},
		"empty install":      {[]string{"install"}, ""},
	} {
		if _, _, err := (cli{home: root, stdin: test.input}).run(append([]string{"adapter", "codex"}, test.args...)...); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	for _, event := range []string{"SessionStart", "PostToolUse", "Stop"} {
		for _, agentID := range []string{"", "subagent-1"} {
			stdout, stderr, err := (cli{home: root, stdin: codexHookInput(t, event, "unbound", agentID)}).run("adapter", "codex", "arm")
			if err != nil || len(stdout) != 0 {
				t.Fatalf("unbound %s arm = %s, %v: %s", event, stdout, err, stderr)
			}
		}
	}
}

func TestCodexPostToolUseInjectShowsExactlyItsWake(t *testing.T) {
	// Captured from an isolated native 0.167.0-alpha.3.2 parent and its child.
	load := func(name string) string {
		t.Helper()
		data, err := os.ReadFile(filepath.Join("testdata", "codex-0.167-post-tool-use-"+name+".json"))
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	parent, child := load("parent"), load("child")
	event, err := codex.ParseHookEvent(strings.NewReader(parent))
	if err != nil {
		t.Fatal(err)
	}
	session := event.SessionID
	childEvent, err := codex.ParseHookEvent(strings.NewReader(child))
	if err != nil || childEvent.SessionID != session || childEvent.AgentID == "" || event.AgentID != "" {
		t.Fatalf("native fixtures must distinguish a child sharing its parent's session: parent=%+v child=%+v err=%v", event, childEvent, err)
	}
	for _, test := range []struct {
		name, body   string
		total, first int
	}{
		{"five-record cap", "", 7, 5},
		{"UTF-16 cap", strings.Repeat("😀", 1900), 4, 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "runtime-state")
			writer, err := conductor.New(root, "writer")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := writer.Join("writer", "dev"); err != nil {
				t.Fatal(err)
			}
			if _, stderr, err := (cli{home: root, stdin: codexHookInput(t, "SessionStart", session, "")}).run("adapter", "codex", "identity"); err != nil {
				t.Fatalf("identity: %v: %s", err, stderr)
			}
			if _, stderr, err := (cli{home: root, env: map[string]string{codex.ThreadEnvironment: session}}).run("reader", "join", "review"); err != nil {
				t.Fatalf("join: %v: %s", err, stderr)
			}
			reader, err := conductor.Open(root, "reader")
			if err != nil {
				t.Fatal(err)
			}
			const topic = "work/tasks"
			if _, err := reader.SubscribeTopic(topic); err != nil {
				t.Fatal(err)
			}
			for i := 1; i <= test.total; i++ {
				if _, err := writer.Put(topic, fmt.Sprintf("wake-item-%d:%s", i, test.body)); err != nil {
					t.Fatal(err)
				}
			}
			inject := func(input string) []byte {
				t.Helper()
				stdout, stderr, err := (cli{home: root, stdin: input}).run("adapter", "codex", "inject")
				if err != nil {
					t.Fatalf("inject: %v: %s", err, stderr)
				}
				return stdout
			}
			if stdout := inject(child); len(stdout) != 0 {
				t.Fatalf("child inject wrote parent work: %s", stdout)
			}
			for round := 0; round < 2; round++ {
				batch, err := reader.Unread()
				if err != nil {
					t.Fatal(err)
				}
				if round == 0 && (len(batch.Deliveries) != 1 || len(batch.Deliveries[0].Publications) != test.total) {
					t.Fatalf("child inject acknowledged pending parent work: %+v", batch)
				}
				want, _ := conductor.RenderWake(batch)
				var output postToolUseOutput
				stdout := inject(parent)
				if err := json.Unmarshal(stdout, &output); err != nil || output.HookSpecificOutput.HookEventName != "PostToolUse" || output.HookSpecificOutput.AdditionalContext != want {
					t.Fatalf("inject was not exactly the expected additionalContext wake: %s, %v", stdout, err)
				}
				if units := len(utf16.Encode([]rune(want))); units > conductor.WakeTextLimit {
					t.Fatalf("wake used %d UTF-16 units", units)
				}
				remaining := test.total - test.first
				count := test.first
				if round == 1 {
					remaining, count = 0, remaining
				}
				if !strings.HasPrefix(want, fmt.Sprintf("Conductor: %d new records for you.", count)) || count > 5 {
					t.Fatalf("wake broke its record cap: %q", want)
				}
				if round == 0 && !strings.HasSuffix(want, fmt.Sprintf("(%d more unread; they follow on their own.)\n", remaining)) {
					t.Fatalf("wake omitted its remainder: %q", want)
				}
				delta, err := reader.Get(conductor.ReadRequest{Topic: topic, Mode: conductor.ReadDelta})
				if err != nil || len(delta.Publications) != remaining {
					t.Fatalf("ACK left %d publications, want %d: %v", len(delta.Publications), remaining, err)
				}
				for i, publication := range delta.Publications {
					if len(publication.Records) != 1 || publication.Records[0].Index != int64(test.first+i+1) {
						t.Fatalf("ACK skipped or replayed an unshown record: %+v", publication)
					}
				}
			}
			if stdout := inject(parent); len(stdout) != 0 {
				t.Fatalf("caught-up inject wrote a wake: %s", stdout)
			}
		})
	}
}

func codexHookInput(t *testing.T, event, session, agentID string) string {
	t.Helper()
	data, err := json.Marshal(codex.HookEvent{SessionID: session, HookEventName: event, AgentID: agentID})
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
