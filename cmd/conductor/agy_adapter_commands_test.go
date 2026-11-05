package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/danlavee/Conductor/internal/adapters/agy"
	installer "github.com/danlavee/Conductor/internal/install"
	conductor "github.com/danlavee/Conductor/internal/state"
)

func TestAGYAdapterInstallAndCLIContract(t *testing.T) {
	caseRoot := t.TempDir()
	destination := filepath.Join(caseRoot, "adapters", "agy")
	state := filepath.Join(caseRoot, "runtime-state")

	stdout, stderr, err := runCLIHelper(os.Args[0], state, "", "adapter", "agy", "install", destination)
	if err != nil || len(stderr) != 0 {
		t.Fatalf("adapter agy install error = %v, stderr = %q", err, stderr)
	}
	var installed installer.Result
	if err := json.Unmarshal(stdout, &installed); err != nil {
		t.Fatal(err)
	}
	if installed.Status != "installed" || installed.InstallPath != destination {
		t.Fatalf("install result = %+v", installed)
	}

	// Verify hooks.json in installed destination has ${CONDUCTOR_BIN} rewritten.
	hooksData, err := os.ReadFile(filepath.Join(destination, "hooks.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(hooksData), "${CONDUCTOR_BIN}") {
		t.Fatalf("installed hooks.json still contains placeholder: %s", hooksData)
	}

	// Set up two agents on the bus.
	project := t.TempDir()
	if _, stderr, err := runCLIHelper(installed.BinaryPath, state, project, "alice", "join", "lead"); err != nil {
		t.Fatalf("alice join: %v: %s", err, stderr)
	}
	if _, stderr, err := runCLIHelper(installed.BinaryPath, state, project, "bob", "join", "worker"); err != nil {
		t.Fatalf("bob join: %v: %s", err, stderr)
	}
	if _, stderr, err := runCLIHelper(installed.BinaryPath, state, project, "bob", "subscribe", "--topic=dev/tasks"); err != nil {
		t.Fatalf("bob subscribe: %v: %s", err, stderr)
	}

	// 1. Run SessionStart hook (identity --hook) for session conv-e2e.
	hookStdin, _ := json.Marshal(agy.HookInput{
		ConversationID: "conv-e2e",
		WorkspacePaths: []string{project},
	})
	stdout, stderr, err = runCLIHelperWithStdin(installed.BinaryPath, state, project, hookStdin, map[string]string{
		agy.SessionEnvironment: "conv-e2e",
	}, "adapter", "agy", "identity", "--hook")
	if err != nil {
		t.Fatalf("identity --hook: %v: %s", err, stderr)
	}
	var startOut agy.SessionStartOutput
	if err := json.Unmarshal(stdout, &startOut); err != nil || len(startOut.InjectSteps) != 1 {
		t.Fatalf("SessionStartOutput = %s, %v", stdout, err)
	}
	if !strings.Contains(startOut.InjectSteps[0].EphemeralMessage, "no agent identity is bound yet") {
		t.Fatalf("unexpected announcement: %q", startOut.InjectSteps[0].EphemeralMessage)
	}

	// 2. Bind session conv-e2e to bob (starts resident automatically).
	stdout, stderr, err = runCLIHelperWithStdin(installed.BinaryPath, state, project, nil, map[string]string{
		agy.SessionEnvironment: "conv-e2e",
	}, "adapter", "agy", "bind", "bob")
	if err != nil {
		t.Fatalf("bind bob: %v: %s", err, stderr)
	}
	defer func() {
		_, _, _ = runCLIHelperWithStdin(installed.BinaryPath, state, project, nil, map[string]string{
			agy.SessionEnvironment: "conv-e2e",
		}, "adapter", "agy", "release")
	}()

	// 3. Check `conductor bob status` inside conv-e2e: must report holder == "adapter".
	stdout, stderr, err = runCLIHelperWithStdin(installed.BinaryPath, state, project, nil, map[string]string{
		agy.SessionEnvironment: "conv-e2e",
	}, "bob", "status")
	if err != nil {
		t.Fatalf("bob status: %v: %s", err, stderr)
	}
	var status conductor.WatchStatus
	if err := json.Unmarshal(stdout, &status); err != nil {
		t.Fatal(err)
	}
	if !status.Wakeable || status.Holder != conductor.HolderAdapter {
		t.Fatalf("bob status holder = %+v; want wakeable adapter holder", status)
	}

	// 4. Alice publishes a task on dev/tasks.
	if _, stderr, err := runCLIHelper(installed.BinaryPath, state, project, "alice", "put", "dev/tasks", "implement agy adapter"); err != nil {
		t.Fatalf("alice put: %v: %s", err, stderr)
	}

	// 5. Run Stop hook (stop --hook): should wake bob with {"decision": "continue", "reason": "..."}.
	transcriptPath := filepath.Join(t.TempDir(), "transcript.jsonl")
	step0, _ := json.Marshal(map[string]any{
		"step_index": 0,
		"source":     "MODEL",
		"type":       "PLANNER_RESPONSE",
		"status":     "DONE",
		"content":    "Joined and bound as bob.",
	})
	_ = os.WriteFile(transcriptPath, append(step0, '\n'), 0o600)

	stopStdin, _ := json.Marshal(agy.HookInput{
		ConversationID:    "conv-e2e",
		WorkspacePaths:    []string{project},
		TranscriptPath:    transcriptPath,
		TerminationReason: "NO_TOOL_CALL",
	})
	stdout, stderr, err = runCLIHelperWithStdin(installed.BinaryPath, state, project, stopStdin, map[string]string{
		agy.SessionEnvironment:     "conv-e2e",
		agy.WaitTimeoutEnvironment: "2s",
	}, "adapter", "agy", "stop", "--hook")
	if err != nil {
		t.Fatalf("stop --hook (1): %v: %s", err, stderr)
	}
	var stopOut agy.StopOutput
	if err := json.Unmarshal(stdout, &stopOut); err != nil {
		t.Fatal(err)
	}
	if stopOut.Decision != "continue" || !strings.Contains(stopOut.Reason, "CONDUCTOR_RECEIPT ") {
		t.Fatalf("stopOut (1) = %+v; want decision=continue with receipt token", stopOut)
	}

	// Extract the receipt token line from stopOut.Reason.
	var receiptToken string
	for _, line := range strings.Split(stopOut.Reason, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "CONDUCTOR_RECEIPT ") {
			receiptToken = strings.TrimSpace(line)
			break
		}
	}
	if receiptToken == "" {
		t.Fatalf("could not find CONDUCTOR_RECEIPT line in %q", stopOut.Reason)
	}

	// 6. Simulate bob completing the continuation turn and writing the receipt token to transcript.jsonl.
	step1, _ := json.Marshal(map[string]any{
		"step_index": 1,
		"source":     "MODEL",
		"type":       "PLANNER_RESPONSE",
		"status":     "DONE",
		"content":    "Completed task.\n" + receiptToken,
	})
	_ = os.WriteFile(transcriptPath, append(append(step0, '\n'), append(step1, '\n')...), 0o600)

	stdout, stderr, err = runCLIHelperWithStdin(installed.BinaryPath, state, project, stopStdin, map[string]string{
		agy.SessionEnvironment:     "conv-e2e",
		agy.WaitTimeoutEnvironment: "150ms",
	}, "adapter", "agy", "stop", "--hook")
	if err != nil {
		t.Fatalf("stop --hook (2): %v: %s", err, stderr)
	}
	var stopOut2 agy.StopOutput
	if err := json.Unmarshal(stdout, &stopOut2); err != nil {
		t.Fatal(err)
	}
	if stopOut2.Decision != "" {
		t.Fatalf("stopOut (2) = %+v; want allow-stop after receipt acknowledgement", stopOut2)
	}
}

func runCLIHelperWithStdin(executable, state, directory string, stdin []byte, extraEnv map[string]string, args ...string) ([]byte, []byte, error) {
	command := exec.Command(executable, args...)
	command.Env = replaceEnvironment(os.Environ(), cliHelperEnvironment, "1")
	command.Env = replaceEnvironment(command.Env, "CONDUCTOR_HOME", state)
	command.Env = replaceEnvironment(command.Env, agy.SessionEnvironment, "")
	for k, v := range extraEnv {
		command.Env = replaceEnvironment(command.Env, k, v)
	}
	command.Dir = directory
	if stdin != nil {
		command.Stdin = bytes.NewReader(stdin)
	}
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}
