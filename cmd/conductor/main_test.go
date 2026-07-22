package main

import (
	"bytes"
	"encoding/json"
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

func TestConditionalConflictProcessContract(t *testing.T) {
	state := filepath.Join(t.TempDir(), "runtime-state")
	stdout, stderr, err := runCLIHelper(os.Args[0], state, "", "register", "writer", "development")
	if err != nil || len(stderr) != 0 || len(stdout) == 0 {
		t.Fatalf("register error = %v, stdout = %q, stderr = %q", err, stdout, stderr)
	}
	stdout, stderr, err = runCLIHelper(os.Args[0], state, "", "put", "messages/team", "entry", "note", "created", "--if-index", "entry=0")
	if err != nil || len(stderr) != 0 || len(stdout) == 0 {
		t.Fatalf("create error = %v, stdout = %q, stderr = %q", err, stdout, stderr)
	}
	stdout, stderr, err = runCLIHelper(os.Args[0], state, "", "put", "messages/team", "entry", "note", "stale", "--if-index", "entry=0")
	exit, ok := err.(*exec.ExitError)
	if !ok || exit.ExitCode() != 1 || len(stdout) != 0 {
		t.Fatalf("conflict error = %v, stdout = %q, stderr = %q", err, stdout, stderr)
	}
	var response struct {
		Code     string `json:"code"`
		Conflict struct {
			Resource      string `json:"resource"`
			Key           string `json:"key"`
			ExpectedIndex int64  `json:"expected_index"`
			CurrentIndex  int64  `json:"current_index"`
		} `json:"conflict"`
	}
	if err := json.Unmarshal(stderr, &response); err != nil {
		t.Fatalf("stderr is not JSON: %q: %v", stderr, err)
	}
	if response.Code != "CONFLICT" || response.Conflict.Resource != "messages/team" || response.Conflict.Key != "entry" || response.Conflict.ExpectedIndex != 0 || response.Conflict.CurrentIndex <= 0 {
		t.Fatalf("conflict response = %+v", response)
	}
}

func TestProtocolMismatchProcessContract(t *testing.T) {
	state := filepath.Join(t.TempDir(), "runtime-state")
	if _, _, err := runCLIHelper(os.Args[0], state, "", "register", "writer", "coordination"); err != nil {
		t.Fatal(err)
	}
	declaration := filepath.Join(state, "protocol.json")
	if err := os.WriteFile(declaration, []byte("{\n  \"version\": 2\n}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, err := runCLIHelper(os.Args[0], state, "", "list-agents")
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
	if response.Code != "PROTOCOL_MISMATCH" || response.Protocol.Supported != conductor.CurrentProtocolVersion || response.Protocol.Found != 2 {
		t.Fatalf("response = %+v", response)
	}
	if data, readErr := os.ReadFile(declaration); readErr != nil || string(data) != "{\n  \"version\": 2\n}\n" {
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
	for _, flag := range []string{"--codex <name>", "--codex-cli <name>"} {
		if !strings.Contains(message, flag) {
			t.Fatalf("usage omits %s: %s", flag, message)
		}
	}
	if strings.Contains(message, "--codex-app-server") {
		t.Fatalf("usage retains removed Codex mode: %s", message)
	}
}

func TestWatchWithoutModeReturnsUsageError(t *testing.T) {
	state := filepath.Join(t.TempDir(), "runtime-state")
	t.Setenv("CONDUCTOR_HOME", state)
	err := run([]string{"watch"})
	if err == nil || err.Error() != usageError().Error() {
		t.Fatalf("error = %v, want usage error", err)
	}
}

func TestCodexWatchUsesOneShotSignalDelivery(t *testing.T) {
	state := filepath.Join(t.TempDir(), "runtime-state")
	t.Setenv("CODEX_THREAD_ID", "")
	t.Setenv("CONDUCTOR_CODEX_BIN", filepath.Join(t.TempDir(), "missing-codex"))
	if _, stderr, err := runCLIHelper(os.Args[0], state, "", "register", "native", "Codex host delivery"); err != nil || len(stderr) != 0 {
		t.Fatalf("register error = %v, stderr = %q", err, stderr)
	}
	stdout, stderr, err := runCLIHelper(os.Args[0], state, "", "watch", "--codex", "native")
	if err != nil || len(stderr) != 0 {
		t.Fatalf("watch error = %v, stderr = %q", err, stderr)
	}
	var signal conductor.Signal
	if err := json.Unmarshal(stdout, &signal); err != nil {
		t.Fatalf("stdout is not a signal: %q: %v", stdout, err)
	}
	if signal.Type != "join" || signal.Agent != "native" || signal.Index <= 0 {
		t.Fatalf("signal = %+v", signal)
	}
}

func TestUsageExposesTwoAntigravityTransports(t *testing.T) {
	message := usageError().Error()
	for _, flag := range []string{"--agy <name>", "--agy-cli <name>"} {
		if !strings.Contains(message, flag) {
			t.Fatalf("usage omits %s: %s", flag, message)
		}
	}
}

func TestParseConditionalPut(t *testing.T) {
	resource, messages, options, err := parsePut([]string{
		"messages/team",
		"entry", "note", "done",
		"--if-index", "entry=12",
		"other", "rule", "review first",
		"--if-index", "guard=0",
	})
	if err != nil {
		t.Fatal(err)
	}
	if resource != "messages/team" || len(messages) != 2 || messages["entry"].Payload.Text != "done" || options.IfIndex["entry"] != 12 || options.IfIndex["guard"] != 0 {
		t.Fatalf("resource=%q messages=%v options=%v", resource, messages, options.IfIndex)
	}
}

func TestParseConditionalWriteRejectsInvalidConditions(t *testing.T) {
	for name, args := range map[string][]string{
		"missing":   {"messages/team", "entry", "note", "x", "--if-index"},
		"malformed": {"messages/team", "entry", "note", "x", "--if-index", "entry"},
		"negative":  {"messages/team", "entry", "note", "x", "--if-index", "entry=-1"},
		"duplicate": {"messages/team", "entry", "note", "x", "--if-index", "entry=1", "--if-index", "entry=2"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, _, err := parsePut(args); err == nil {
				t.Fatal("invalid conditional put succeeded")
			}
		})
	}
}

func TestParseConditionalBegin(t *testing.T) {
	resource, options, err := parseBegin([]string{"dev/tasks", "--if-index", "task-42=7"})
	if err != nil {
		t.Fatal(err)
	}
	if resource != "dev/tasks" || options.IfIndex["task-42"] != 7 {
		t.Fatalf("resource=%q options=%v", resource, options.IfIndex)
	}
}

func TestParsePutKeepsKindUnrestrictedAndTextIntact(t *testing.T) {
	_, messages, _, err := parsePut([]string{"messages/team", "entry", "team-defined kind / 任意", "plain text"})
	message := messages["entry"]
	if err != nil || message.Kind != "team-defined kind / 任意" || message.Payload == nil || message.Payload.Text != "plain text" {
		t.Fatalf("message=%#v error=%v", message, err)
	}
}
