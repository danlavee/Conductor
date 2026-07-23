package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	conductor "github.com/danlavee/Conductor"
	installer "github.com/danlavee/Conductor/internal/install"
)

const cliHelperEnvironment = "CONDUCTOR_CLI_TEST_HELPER"

func TestMain(m *testing.M) {
	if os.Getenv(cliHelperEnvironment) == "1" {
		main()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestInstallArgumentFailuresDoNotInitializeRuntime(t *testing.T) {
	for name, args := range map[string][]string{
		"missing":   {"install"},
		"empty":     {"install", ""},
		"relative":  {"install", filepath.Join(".agents", "skills", "conductor")},
		"malformed": {"install", filepath.Join(t.TempDir(), ".agents", "skills", "other")},
		"extra":     {"install", "first", "second"},
	} {
		t.Run(name, func(t *testing.T) {
			state := filepath.Join(t.TempDir(), "runtime-state")
			t.Setenv("CONDUCTOR_HOME", state)
			err := run(args)
			if err == nil {
				t.Fatal("invalid install invocation succeeded")
			}
			if err.Error() != "usage: conductor install <absolute-skill-directory>" {
				t.Fatalf("error = %q", err)
			}
			if _, statErr := os.Stat(state); !os.IsNotExist(statErr) {
				t.Fatalf("runtime state was initialized: %v", statErr)
			}
		})
	}
}

func TestMissingInstallFolderProcessContract(t *testing.T) {
	state := filepath.Join(t.TempDir(), "runtime-state")
	stdout, stderr, err := runCLIHelper(os.Args[0], state, "", "install")
	exit, ok := err.(*exec.ExitError)
	if !ok || exit.ExitCode() != 1 {
		t.Fatalf("error = %v, want exit code 1", err)
	}
	if len(stdout) != 0 {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	var response struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(stderr, &response); err != nil {
		t.Fatalf("stderr is not JSON: %q: %v", stderr, err)
	}
	if response.Code != "INVALID" || response.Message != "usage: conductor install <absolute-skill-directory>" {
		t.Fatalf("stderr response = %+v", response)
	}
	if _, err := os.Stat(state); !os.IsNotExist(err) {
		t.Fatalf("runtime state was initialized: %v", err)
	}
}

func TestRecordOperationProcessContract(t *testing.T) {
	state := filepath.Join(t.TempDir(), "runtime-state")
	stdout, stderr, err := runCLIHelper(os.Args[0], state, "", "writer", "register", "development")
	if err != nil || len(stderr) != 0 || len(stdout) == 0 {
		t.Fatalf("register error = %v, stdout = %q, stderr = %q", err, stdout, stderr)
	}
	stdout, stderr, err = runCLIHelper(os.Args[0], state, "", "writer", "put", "messages/team", "created")
	if err != nil || len(stderr) != 0 || len(stdout) == 0 {
		t.Fatalf("create error = %v, stdout = %q, stderr = %q", err, stdout, stderr)
	}
	var created map[string]any
	if err := json.Unmarshal(stdout, &created); err != nil || len(created) != 2 || created["index"] != float64(1) || created["text"] != "created" {
		t.Fatalf("created record = %#v, error = %v", created, err)
	}
	stdout, stderr, err = runCLIHelper(os.Args[0], state, "", "writer", "edit", "messages/team", "1", "updated")
	if err != nil || len(stderr) != 0 {
		t.Fatalf("edit error = %v, stderr = %q", err, stderr)
	}
	var edited map[string]any
	if err := json.Unmarshal(stdout, &edited); err != nil || len(edited) != 2 || edited["index"] != float64(1) || edited["text"] != "updated" {
		t.Fatalf("edited record = %#v, error = %v", edited, err)
	}
	stdout, stderr, err = runCLIHelper(os.Args[0], state, "", "writer", "strike", "messages/team", "1")
	if err != nil || len(stderr) != 0 {
		t.Fatalf("strike error = %v, stderr = %q", err, stderr)
	}
	var struck conductor.Record
	if err := json.Unmarshal(stdout, &struck); err != nil || struck.Index != 1 || struck.Text != "~~updated~~" {
		t.Fatalf("struck record = %#v, error = %v", struck, err)
	}
}

func TestRecordOperationProcessVisibilityBoundary(t *testing.T) {
	t.Run("before-head", func(t *testing.T) {
		state := filepath.Join(t.TempDir(), "runtime-state")
		if _, _, err := runCLIHelper(os.Args[0], state, "", "writer", "register", "development"); err != nil {
			t.Fatal(err)
		}
		indexData := fmt.Sprintf("{\n  \"index\": %d\n}\n", int64(math.MaxInt64))
		if err := os.WriteFile(filepath.Join(state, "state", "index.json"), []byte(indexData), 0o600); err != nil {
			t.Fatal(err)
		}
		stdout, stderr, err := runCLIHelper(os.Args[0], state, "", "writer", "put", "messages/team", "not visible")
		if err == nil || len(stdout) != 0 || len(stderr) == 0 {
			t.Fatalf("error = %v, stdout = %q, stderr = %q", err, stdout, stderr)
		}
	})

	t.Run("after-head", func(t *testing.T) {
		state := filepath.Join(t.TempDir(), "runtime-state")
		if _, _, err := runCLIHelper(os.Args[0], state, "", "writer", "register", "development"); err != nil {
			t.Fatal(err)
		}
		// Registration itself already consumes global sequences 1 (the
		// collaboration/agents roster commit) and 2 (join); the put below
		// lands its own event at 3.
		if err := os.Mkdir(filepath.Join(state, "events", fmt.Sprintf("%020d.json", 3)), 0o700); err != nil {
			t.Fatal(err)
		}
		stdout, stderr, err := runCLIHelper(os.Args[0], state, "", "writer", "put", "messages/team", "visible")
		if err == nil || len(stdout) == 0 || len(stderr) == 0 {
			t.Fatalf("error = %v, stdout = %q, stderr = %q", err, stdout, stderr)
		}
		var record conductor.Record
		if decodeErr := json.Unmarshal(stdout, &record); decodeErr != nil || record.Index != 1 || record.Text != "visible" {
			t.Fatalf("record = %#v, decode error = %v", record, decodeErr)
		}
	})
}

func TestProtocolMismatchProcessContract(t *testing.T) {
	state := filepath.Join(t.TempDir(), "runtime-state")
	if _, _, err := runCLIHelper(os.Args[0], state, "", "writer", "register", "coordination"); err != nil {
		t.Fatal(err)
	}
	declaration := filepath.Join(state, "protocol.json")
	unsupported := conductor.CurrentProtocolVersion + 1
	declarationText := fmt.Sprintf("{\n  \"version\": %d\n}\n", unsupported)
	if err := os.WriteFile(declaration, []byte(declarationText), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, err := runCLIHelper(os.Args[0], state, "", "writer", "list-agents")
	exit, ok := err.(*exec.ExitError)
	if !ok || exit.ExitCode() != 1 || len(stdout) != 0 {
		t.Fatalf("error = %v, stdout = %q", err, stdout)
	}
	var response struct {
		Code     string `json:"code"`
		Protocol struct {
			Supported int `json:"supported"`
			Found     int `json:"found"`
		} `json:"protocol"`
	}
	if err := json.Unmarshal(stderr, &response); err != nil {
		t.Fatalf("stderr is not JSON: %q: %v", stderr, err)
	}
	if response.Code != "PROTOCOL_MISMATCH" || response.Protocol.Supported != conductor.CurrentProtocolVersion || response.Protocol.Found != unsupported {
		t.Fatalf("response = %+v", response)
	}
	if data, readErr := os.ReadFile(declaration); readErr != nil || string(data) != declarationText {
		t.Fatalf("declaration changed: %q, %v", data, readErr)
	}
}

func TestInstallProcessSuccessAndIdempotency(t *testing.T) {
	caseRoot := t.TempDir()
	destination := filepath.Join(caseRoot, ".agents", "skills", "conductor")
	state := filepath.Join(caseRoot, "runtime-state")
	stdout, stderr, err := runCLIHelper(os.Args[0], state, "", "install", destination)
	if err != nil || len(stderr) != 0 {
		t.Fatalf("first install error = %v, stderr = %q", err, stderr)
	}
	var first installer.Result
	if err := json.Unmarshal(stdout, &first); err != nil {
		t.Fatal(err)
	}
	if first.Status != "installed" || first.SkillPath != destination {
		t.Fatalf("first result = %+v", first)
	}
	if first.Protocol != conductor.CurrentProtocolVersion {
		t.Fatalf("installed protocol = %d", first.Protocol)
	}
	stdout, stderr, err = runCLIHelper(os.Args[0], state, "", "install", destination)
	if err != nil || len(stderr) != 0 {
		t.Fatalf("second install error = %v, stderr = %q", err, stderr)
	}
	var second installer.Result
	if err := json.Unmarshal(stdout, &second); err != nil {
		t.Fatal(err)
	}
	if second.Status != "already-installed" || second.ManifestHash != first.ManifestHash {
		t.Fatalf("second result = %+v", second)
	}
	stdout, stderr, err = runCLIHelper(first.BinaryPath, state, t.TempDir(), "version")
	if err != nil || len(stderr) != 0 {
		t.Fatalf("installed version error = %v, stderr = %q", err, stderr)
	}
	var version struct {
		Version  string `json:"version"`
		Protocol int    `json:"protocol"`
	}
	if err := json.Unmarshal(stdout, &version); err != nil || version.Version == "" || version.Protocol != conductor.CurrentProtocolVersion {
		t.Fatalf("version = %q, error = %v", stdout, err)
	}
	if _, err := os.Stat(state); !os.IsNotExist(err) {
		t.Fatalf("runtime state was initialized: %v", err)
	}
}

func runCLIHelper(executable, state, directory string, args ...string) ([]byte, []byte, error) {
	command := exec.Command(executable, args...)
	command.Env = replaceEnvironment(os.Environ(), cliHelperEnvironment, "1")
	command.Env = replaceEnvironment(command.Env, "CONDUCTOR_HOME", state)
	command.Dir = directory
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}

func replaceEnvironment(environment []string, key, value string) []string {
	prefix := key + "="
	result := make([]string, 0, len(environment)+1)
	for _, entry := range environment {
		if !strings.EqualFold(entry[:strings.Index(entry, "=")+1], prefix) {
			result = append(result, entry)
		}
	}
	return append(result, prefix+value)
}

func TestVersionDoesNotInitializeRuntime(t *testing.T) {
	state := filepath.Join(t.TempDir(), "runtime-state")
	t.Setenv("CONDUCTOR_HOME", state)
	if err := run([]string{"version"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(state); !os.IsNotExist(err) {
		t.Fatalf("runtime state was initialized: %v", err)
	}
}

func TestUsageExposesOnlyTwoCodexTransports(t *testing.T) {
	message := usageError().Error()
	for _, flag := range []string{"--codex", "--codex-cli"} {
		if !strings.Contains(message, flag) {
			t.Fatalf("usage omits %s: %s", flag, message)
		}
	}
	if strings.Contains(message, "--codex-app-server") {
		t.Fatalf("usage retains removed Codex mode: %s", message)
	}
}

func TestWatchDefaultsToContent(t *testing.T) {
	state := filepath.Join(t.TempDir(), "runtime-state")
	t.Setenv("CONDUCTOR_HOME", state)
	t.Setenv("CONDUCTOR_AGENT", "a")
	if err := run([]string{"a", "register", "dev"}); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"a", "watch"}); err != nil {
		t.Fatalf("bare watch failed: %v", err)
	}
}

func TestRemovedWatchModesReturnUsage(t *testing.T) {
	t.Setenv("CONDUCTOR_HOME", filepath.Join(t.TempDir(), "runtime-state"))
	for _, args := range [][]string{{"a", "watch", "--wait"}, {"a", "watch", "--since", "1"}} {
		if err := run(args); err == nil || err.Error() != usageError().Error() {
			t.Fatalf("%v: error = %v, want usage error", args, err)
		}
	}
}

func TestParseGetRangeDefaultsAndGuards(t *testing.T) {
	request, err := parseGet([]string{"messages/team", "7", "--start=0", "--end=12", "--limit=5"})
	if err != nil {
		t.Fatal(err)
	}
	if request.Topic != "messages/team" || request.RecordIndex != 7 || request.Mode != conductor.ReadRange || request.Start != 0 || request.End != 12 || request.Limit != 5 {
		t.Fatalf("request = %+v", request)
	}
	defaults, err := parseGet([]string{"messages/team"})
	if err != nil || defaults.Start != 0 || defaults.End != 0 || defaults.Limit != 0 || defaults.Mode != conductor.ReadRange {
		t.Fatalf("defaults = %+v, error = %v", defaults, err)
	}
}

func TestParseGetDeltaAndFull(t *testing.T) {
	delta, err := parseGet([]string{"messages/team", "7", "--delta", "--limit=5"})
	if err != nil || delta.Mode != conductor.ReadDelta || delta.RecordIndex != 7 || delta.Limit != 5 {
		t.Fatalf("delta = %+v, error = %v", delta, err)
	}
	full, err := parseGet([]string{"messages/team", "--full"})
	if err != nil || full.Mode != conductor.ReadFull {
		t.Fatalf("full = %+v, error = %v", full, err)
	}
	for _, args := range [][]string{
		{"messages/team", "--delta", "--start=1"},
		{"messages/team", "--full", "--limit=1"},
		{"messages/team", "--delta", "--full"},
	} {
		if _, err := parseGet(args); err == nil {
			t.Fatalf("parseGet(%v) succeeded, want error", args)
		}
	}
}

func TestGetDeltaAcknowledgesSuccessfulDelivery(t *testing.T) {
	home := filepath.Join(t.TempDir(), "runtime-state")
	t.Setenv("CONDUCTOR_HOME", home)
	t.Setenv("CONDUCTOR_AGENT", "a")
	if err := run([]string{"a", "register", "dev"}); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"a", "subscribe", "--topic=messages/team"}); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"a", "put", "messages/team", "hello"}); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"a", "get", "messages/team", "--delta"}); err != nil {
		t.Fatal(err)
	}
	client, err := conductor.New(home, "a")
	if err != nil {
		t.Fatal(err)
	}
	delta, err := client.Get(conductor.ReadRequest{Topic: "messages/team", Mode: conductor.ReadDelta})
	if err != nil {
		t.Fatal(err)
	}
	if len(delta.Publications) != 0 {
		t.Fatalf("CLI delta was not acknowledged: %#v", delta)
	}
}

func TestCodexWatchRequiresThreadID(t *testing.T) {
	state := filepath.Join(t.TempDir(), "runtime-state")
	t.Setenv("CONDUCTOR_HOME", state)
	t.Setenv("CODEX_THREAD_ID", "")
	err := run([]string{"a", "watch", "--codex"})
	if err == nil || !strings.Contains(err.Error(), "thread ID") {
		t.Fatalf("error = %v, want missing thread ID", err)
	}
}

func TestUsageExposesTwoAntigravityTransports(t *testing.T) {
	message := usageError().Error()
	for _, flag := range []string{"--agy", "--agy-cli"} {
		if !strings.Contains(message, flag) {
			t.Fatalf("usage omits %s: %s", flag, message)
		}
	}
}

func TestClaudeCLIWatchRejectsTrailingArguments(t *testing.T) {
	state := filepath.Join(t.TempDir(), "runtime-state")
	t.Setenv("CONDUCTOR_HOME", state)
	t.Setenv("CLAUDE_SESSION_ID", "session-1")
	if err := run([]string{"a", "watch", "--claude-cli", "extra"}); err == nil || err.Error() != usageError().Error() {
		t.Fatalf("error = %v, want usage error", err)
	}
}

func TestClaudeCLIWatchRequiresSessionIDEnvironment(t *testing.T) {
	state := filepath.Join(t.TempDir(), "runtime-state")
	t.Setenv("CONDUCTOR_HOME", state)
	t.Setenv("CLAUDE_SESSION_ID", "")
	err := run([]string{"a", "watch", "--claude-cli"})
	if err == nil || !strings.Contains(err.Error(), "session ID") {
		t.Fatalf("error = %v, want missing session ID", err)
	}
}

func TestClaudeCLIWatchRefusesSelfTargetingTheLiveSession(t *testing.T) {
	state := filepath.Join(t.TempDir(), "runtime-state")
	t.Setenv("CONDUCTOR_HOME", state)
	t.Setenv("CLAUDE_SESSION_ID", "session-1")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "session-1")
	err := run([]string{"a", "watch", "--claude-cli"})
	if err == nil || !strings.Contains(err.Error(), "live session") {
		t.Fatalf("error = %v, want a live-session refusal", err)
	}
}

func TestParseRecordIndex(t *testing.T) {
	if index, err := parseRecordIndex("7"); err != nil || index != 7 {
		t.Fatalf("index = %d, error = %v", index, err)
	}
	for _, value := range []string{"", "0", "-1", "text"} {
		if _, err := parseRecordIndex(value); err == nil {
			t.Fatalf("invalid index %q succeeded", value)
		}
	}
}
